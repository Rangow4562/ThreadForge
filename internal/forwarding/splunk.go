package forwarding

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	splunkHECTokenSecretName = "splunkhectoken"
	defaultSplunkIndex       = "threadforge"
)

var splunkIndexPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// SplunkForwardingOptions configures an inert HEC Vector pack. Only the
// endpoint URL is written to the private config; token material is external.
type SplunkForwardingOptions struct {
	LogPath    string
	Endpoint   string
	Index      string
	SecretsDir string
}

func RenderSplunkVectorConfig(options SplunkForwardingOptions) (string, error) {
	normalized, err := normalizeSplunkOptions(options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Threadforge local JSONL -> customer-managed Splunk HEC.
# This file is inert until you install and start Vector yourself.

%s
[sources.threadforge_runtime]
type = "file"
include = [%s]
read_from = "end"

[transforms.threadforge_runtime_metadata]
type = "remap"
inputs = ["threadforge_runtime"]
source = '''
%s
'''

[sinks.threadforge_splunk]
type = "splunk_hec_logs"
inputs = ["threadforge_runtime_metadata"]
endpoint = %s
endpoint_target = "event"
default_token = "%s"
index = %s
source = "threadforge"
sourcetype = "threadforge:metadata:json"

[sinks.threadforge_splunk.encoding]
codec = "json"

[sinks.threadforge_splunk.batch]
max_events = 1000
timeout_secs = 1

[sinks.threadforge_splunk.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300
`, renderVectorSecretDirectory(normalized.SecretsDir), strconv.Quote(normalized.LogPath), metadataProjectionVRL,
		strconv.Quote(normalized.Endpoint), vectorSecretReference(splunkHECTokenSecretName), strconv.Quote(normalized.Index)), nil
}

func RenderSyntheticSplunkSample() ([]byte, error) {
	return renderSyntheticForwardingSample("splunk_hec", "splunk_hec_logs")
}

func InstallSplunkForwardingPack(outputDir string, options SplunkForwardingOptions) (string, error) {
	normalized, err := normalizeSplunkOptions(options)
	if err != nil {
		return "", err
	}
	config, err := RenderSplunkVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticSplunkSample()
	if err != nil {
		return "", err
	}
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(renderSplunkReadme(normalized))},
		{name: "sample-event.jsonl", content: sample},
		{name: "vector.toml", content: []byte(config)},
	})
}

func normalizeSplunkOptions(options SplunkForwardingOptions) (SplunkForwardingOptions, error) {
	var err error
	options.LogPath, err = normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return SplunkForwardingOptions{}, err
	}
	options.SecretsDir, err = normalizeVectorSecretsDir(options.SecretsDir)
	if err != nil {
		return SplunkForwardingOptions{}, err
	}
	options.Endpoint = strings.TrimSpace(options.Endpoint)
	if options.Endpoint == "" || len(options.Endpoint) > 2048 || strings.ContainsAny(options.Endpoint, "\x00\r\n") {
		return SplunkForwardingOptions{}, errors.New("Splunk HEC endpoint must be a nonempty bounded HTTPS origin")
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.ForceQuery ||
		strings.Contains(options.Endpoint, "#") || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return SplunkForwardingOptions{}, errors.New("Splunk HEC endpoint must be HTTPS with a host only; userinfo, paths, queries, and fragments are not accepted")
	}
	if err := validateRemoteDestinationHost(endpoint.Hostname()); err != nil {
		return SplunkForwardingOptions{}, errors.New("Splunk HEC endpoint must use a non-loopback valid DNS name or IP address")
	}
	if err := validateDestinationPort(endpoint.Port()); err != nil {
		return SplunkForwardingOptions{}, fmt.Errorf("Splunk HEC endpoint %w", err)
	}
	endpoint.Scheme = "https"
	endpoint.Path = ""
	endpoint.RawPath = ""
	options.Endpoint = endpoint.String()
	options.Index = strings.TrimSpace(options.Index)
	if options.Index == "" {
		options.Index = defaultSplunkIndex
	}
	if !splunkIndexPattern.MatchString(options.Index) || strings.Contains(options.Index, "kvstore") {
		return SplunkForwardingOptions{}, errors.New("Splunk index must start with a lowercase letter or digit, contain only lowercase letters, digits, underscore, or hyphen, and not contain kvstore")
	}
	return options, nil
}

func renderSplunkReadme(options SplunkForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge Splunk HEC forwarding pack

This pack contains an inert customer-managed Vector configuration for the HEC
origin %s and index %s. It reads only Threadforge runtime JSONL and exports the
versioned metadata-only projection: event timestamp, allowlisted severity, and
a fixed schema/privacy marker. Raw events, action names, identities, paths,
attributes, inventory, and host-specific values are omitted.

## Forwarding is not enabled by this pack

Pack generation writes local files only. It does not install or start Vector,
read the HEC token, contact Splunk, or upload telemetry. The runtime file source
starts at end-of-file and does not backfill existing records. The endpoint URL
is included in vector.toml; keep this private pack out of shared repositories.

Before starting Vector, place the HEC token in the Vector secrets directory as
the single-line file %s. The configured backend reads secrets from that
directory; Threadforge never opens or stores the token. Create the configured
index in Splunk first and restrict its access, retention, and downstream
consumers according to your policy. The fixed source and sourcetype labels are
threadforge and threadforge:metadata:json.

The included sample-event.jsonl is synthetic. Any manual HEC smoke test should
upload only that sample with an independently managed token; Threadforge does
not execute a network request.

Vector's data directory should be persistent and writable by its service
identity so local file offsets survive restarts. Runtime log path:
%s.
`, options.Endpoint, options.Index, filepath.Join(options.SecretsDir, splunkHECTokenSecretName), options.LogPath)
}
