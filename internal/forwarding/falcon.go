package forwarding

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

const (
	falconTokenSecretName = "falconhectoken"
	falconSource          = "threadforge-endpoint-agent"
	falconSourcetype      = "threadforge:metadata:json"
)

var falconRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// FalconHECOptions configures an inert customer-managed HTTP HEC pack. Token
// material is never accepted by Threadforge and must be provided to Vector.
type FalconHECOptions struct {
	LogPath    string
	Endpoint   string
	Repository string
	SecretsDir string
}

type falconMetadataExport struct {
	Timestamp       string `json:"timestamp"`
	Severity        string `json:"severity"`
	ThreadforgeMark struct {
		SchemaVersion string `json:"schema_version"`
		RawContent    bool   `json:"raw_content"`
	} `json:"threadforge_export"`
}

type falconHECEnvelope struct {
	Time       float64              `json:"time"`
	Event      falconMetadataExport `json:"event"`
	Source     string               `json:"source"`
	Sourcetype string               `json:"sourcetype"`
	Index      string               `json:"index,omitempty"`
}

func RenderFalconHECVectorConfig(options FalconHECOptions) (string, error) {
	normalized, err := normalizeFalconOptions(options)
	if err != nil {
		return "", err
	}
	repository := strconv.Quote(normalized.Repository)
	transform := fmt.Sprintf(`%s
event_time = parse_timestamp!(metadata.timestamp, format: "%%+")
payload = {
  "time": to_unix_timestamp(event_time),
  "event": metadata,
  "source": %s,
  "sourcetype": %s
}
repository = %s
if repository != "" {
  payload.index = repository
}
. = payload`, metadataObjectVRL, strconv.Quote(falconSource), strconv.Quote(falconSourcetype), repository)
	return fmt.Sprintf(`# Threadforge local JSONL -> customer-managed CrowdStrike Falcon HEC.
# This file is inert until you install and start Vector yourself.

%s
[sources.threadforge_runtime]
type = "file"
include = [%s]
read_from = "end"

[transforms.threadforge_falcon_hec_envelope]
type = "remap"
inputs = ["threadforge_runtime"]
source = '''
%s
'''

[sinks.threadforge_falcon_hec]
type = "http"
inputs = ["threadforge_falcon_hec_envelope"]
uri = %s
method = "post"

[sinks.threadforge_falcon_hec.encoding]
codec = "json"

[sinks.threadforge_falcon_hec.framing]
method = "newline_delimited"

[sinks.threadforge_falcon_hec.batch]
max_events = 500
timeout_secs = 5

[sinks.threadforge_falcon_hec.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300

[sinks.threadforge_falcon_hec.request.headers]
Authorization = "Bearer %s"
Content-Type = "application/json"
`, renderVectorSecretDirectory(normalized.SecretsDir), strconv.Quote(normalized.LogPath), transform,
		strconv.Quote(normalized.Endpoint), vectorSecretReference(falconTokenSecretName)), nil
}

func RenderSyntheticFalconHECSample(repository string) ([]byte, error) {
	repository, err := normalizeFalconRepository(repository)
	if err != nil {
		return nil, err
	}
	sample, err := renderSyntheticForwardingSample("falcon_hec", "http_hec")
	if err != nil {
		return nil, err
	}
	event, err := decodeSyntheticSample(sample)
	if err != nil {
		return nil, err
	}
	parsedTimestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("parse synthetic Falcon timestamp: %w", err)
	}
	export := falconMetadataExport{
		Timestamp: event.Timestamp,
		Severity:  "info",
	}
	export.ThreadforgeMark.SchemaVersion = "threadforge.metadata-export.v1"
	export.ThreadforgeMark.RawContent = false
	envelope := falconHECEnvelope{
		Time:       float64(parsedTimestamp.UnixNano()) / float64(time.Second),
		Event:      export,
		Source:     falconSource,
		Sourcetype: falconSourcetype,
		Index:      repository,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode synthetic Falcon HEC envelope: %w", err)
	}
	return append(data, '\n'), nil
}

func InstallFalconHECForwardingPack(outputDir string, options FalconHECOptions) (string, error) {
	normalized, err := normalizeFalconOptions(options)
	if err != nil {
		return "", err
	}
	config, err := RenderFalconHECVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticFalconHECSample(normalized.Repository)
	if err != nil {
		return "", err
	}
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(renderFalconReadme(normalized))},
		{name: "sample-event.jsonl", content: sample},
		{name: "vector.toml", content: []byte(config)},
	})
}

func normalizeFalconOptions(options FalconHECOptions) (FalconHECOptions, error) {
	var err error
	options.LogPath, err = normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return FalconHECOptions{}, err
	}
	options.SecretsDir, err = normalizeVectorSecretsDir(options.SecretsDir)
	if err != nil {
		return FalconHECOptions{}, err
	}
	options.Endpoint = strings.TrimSpace(options.Endpoint)
	if options.Endpoint == "" || len(options.Endpoint) > 2048 || strings.ContainsAny(options.Endpoint, "\x00\r\n") {
		return FalconHECOptions{}, errors.New("Falcon HEC endpoint must be a bounded HTTPS connector URL")
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || strings.Contains(options.Endpoint, "#") ||
		endpoint.Fragment != "" || endpoint.Opaque != "" {
		return FalconHECOptions{}, errors.New("Falcon HEC endpoint must be HTTPS and cannot contain userinfo, a query, or fragment")
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/")
	if endpoint.Path != "/services/collector" && endpoint.Path != "/api/v1/ingest/hec" {
		return FalconHECOptions{}, errors.New("Falcon HEC endpoint path must be /services/collector or /api/v1/ingest/hec")
	}
	if err := validateRemoteDestinationHost(endpoint.Hostname()); err != nil {
		return FalconHECOptions{}, errors.New("Falcon HEC endpoint must use a non-loopback valid DNS name or IP address")
	}
	if err := validateDestinationPort(endpoint.Port()); err != nil {
		return FalconHECOptions{}, fmt.Errorf("Falcon HEC endpoint %w", err)
	}
	endpoint.Scheme = "https"
	endpoint.RawPath = ""
	options.Endpoint = endpoint.String()
	options.Repository, err = normalizeFalconRepository(options.Repository)
	if err != nil {
		return FalconHECOptions{}, err
	}
	return options, nil
}

func normalizeFalconRepository(repository string) (string, error) {
	repository = strings.TrimSpace(repository)
	if repository != "" && !falconRepositoryPattern.MatchString(repository) {
		return "", errors.New("Falcon repository must be 1..128 letters, digits, underscore, or hyphen")
	}
	return repository, nil
}

func decodeSyntheticSample(data []byte) (beacon.Event, error) {
	var event beacon.Event
	if err := json.Unmarshal(data, &event); err != nil {
		return beacon.Event{}, fmt.Errorf("decode synthetic forwarding event: %w", err)
	}
	if err := event.Validate(); err != nil {
		return beacon.Event{}, fmt.Errorf("validate synthetic forwarding event: %w", err)
	}
	return event, nil
}

func renderFalconReadme(options FalconHECOptions) string {
	return fmt.Sprintf(`# Threadforge Falcon HEC forwarding pack

This pack contains an inert customer-managed Vector configuration for the
HTTPS HEC endpoint %s. It reads only Threadforge runtime JSONL and sends a
HEC envelope whose event is the strict metadata-only export: timestamp,
allowlisted severity, and a fixed schema/privacy marker. It excludes raw event
content, inventory, action names, IDs, paths, host identity, and credentials.

## Forwarding is not enabled by this pack

Pack generation writes local files only. It does not install or start Vector,
read the HEC token, contact Falcon, or upload telemetry. The file source starts
at end-of-file and does not backfill. The HTTPS endpoint is stored in
vector.toml; keep the generated pack private.

Before starting Vector, place the HEC token in the Vector secrets directory as
the single-line file %s. Threadforge never opens or stores the token. Source and sourcetype labels
are fixed to %s and %s. The configured HEC endpoint must accept those labels
and optional repository/index %s (empty means no index field is sent).

The included sample-event.jsonl contains one synthetic HEC envelope. For a
manual destination smoke test, upload only that sample:

    curl --fail-with-body -X POST %s \
      --config "${THREADFORGE_FALCON_CURL_CONFIG:?set a private curl config path}" \
      -H "Content-Type: application/json" \
      --data-binary @sample-event.jsonl

Threadforge does not run this command. Runtime log path used by vector.toml:
%s.
`, options.Endpoint, filepath.Join(options.SecretsDir, falconTokenSecretName), falconSource, falconSourcetype, options.Repository,
		options.Endpoint, options.LogPath)
}
