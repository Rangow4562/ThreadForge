package forwarding

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

const (
	defaultS3Region = "us-east-1"
	defaultS3Prefix = "threadforge"
)

var (
	s3BucketPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	awsRegionPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)+$`)
	s3PrefixPattern  = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
)

const metadataObjectVRL = `event = parse_json!(.message)
severity = if event.severity == "info" { "info" } else if event.severity == "low" { "low" } else if event.severity == "medium" { "medium" } else if event.severity == "high" { "high" } else if event.severity == "critical" { "critical" } else { "unknown" }
metadata = {
  "timestamp": event.timestamp,
  "severity": severity,
  "threadforge_export": {
    "schema_version": "threadforge.metadata-export.v1",
    "raw_content": false
  }
}`

const metadataProjectionVRL = metadataObjectVRL + "\n. = metadata"

// S3ForwardingOptions describes an inert customer-managed Vector configuration.
// Creating a pack never starts a shipper or performs network I/O.
type S3ForwardingOptions struct {
	LogPath string
	Bucket  string
	Region  string
	Prefix  string
}

// RenderS3VectorConfig creates a config that tails only new runtime events and
// projects them to a small metadata allowlist. Credentials are not accepted.
func RenderS3VectorConfig(options S3ForwardingOptions) (string, error) {
	normalized, err := normalizeS3Options(options)
	if err != nil {
		return "", err
	}
	logPath := strconv.Quote(normalized.LogPath)
	bucket := strconv.Quote(normalized.Bucket)
	region := strconv.Quote(normalized.Region)
	runtimePrefix := strconv.Quote(normalized.Prefix + "/runtime/date=%F/")
	return fmt.Sprintf(`# Threadforge local JSONL -> customer-managed AWS S3.
# This file is inert until you install and start Vector yourself.

[sources.threadforge_runtime]
type = "file"
include = [%s]
read_from = "end"

[transforms.threadforge_runtime_json]
type = "remap"
inputs = ["threadforge_runtime"]
source = '''
%s
'''

[sinks.threadforge_runtime_s3]
type = "aws_s3"
inputs = ["threadforge_runtime_json"]
bucket = %s
region = %s
key_prefix = %s
filename_time_format = "%%s"
filename_append_uuid = true
filename_extension = "jsonl.gz"
compression = "gzip"
content_encoding = "gzip"
content_type = "application/x-ndjson"

[sinks.threadforge_runtime_s3.encoding]
codec = "json"

[sinks.threadforge_runtime_s3.framing]
method = "newline_delimited"

[sinks.threadforge_runtime_s3.batch]
max_bytes = 10000000
timeout_secs = 300

[sinks.threadforge_runtime_s3.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300
`, logPath, metadataProjectionVRL, bucket, region, runtimePrefix), nil
}

// RenderSyntheticS3Sample returns one Beacon-compatible synthetic event. It has
// no hostname, user identity, prompt, path, trace, or provider data.
func RenderSyntheticS3Sample() ([]byte, error) {
	return renderSyntheticForwardingSample("s3", "aws_s3_jsonl")
}

func renderSyntheticForwardingSample(destinationType, destinationMode string) ([]byte, error) {
	event := beacon.Event{
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Vendor:        beacon.Vendor,
		Product:       beacon.Product,
		SchemaVersion: beacon.SchemaVersion,
		Event: beacon.EventInfo{
			Kind:   "agent_runtime",
			Action: "threadforge.forwarding.synthetic_validation",
		},
		Severity: beacon.SeverityInfo,
		Endpoint: beacon.EndpointInfo{OS: "unknown"},
		Harness:  beacon.HarnessInfo{Name: "threadforge-forwarding-pack"},
		Destination: &beacon.DestinationInfo{
			Type: destinationType, Mode: destinationMode, Status: "synthetic",
		},
	}
	if err := event.Validate(); err != nil {
		return nil, fmt.Errorf("validate synthetic forwarding sample: %w", err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encode synthetic forwarding sample: %w", err)
	}
	return append(data, '\n'), nil
}

// InstallS3ForwardingPack writes a private pack into a new directory. Existing
// directories are never merged or overwritten. On partial write failure, the
// directory is retained for explicit operator inspection.
func InstallS3ForwardingPack(outputDir string, options S3ForwardingOptions) (string, error) {
	normalized, err := normalizeS3Options(options)
	if err != nil {
		return "", err
	}
	config, err := RenderS3VectorConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticS3Sample()
	if err != nil {
		return "", err
	}
	readme := renderS3Readme(normalized)
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(readme)},
		{name: "sample-event.jsonl", content: sample},
		{name: "vector.toml", content: []byte(config)},
	})
}

func normalizeS3Options(options S3ForwardingOptions) (S3ForwardingOptions, error) {
	options.LogPath = strings.TrimSpace(options.LogPath)
	var err error
	options.LogPath, err = normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return S3ForwardingOptions{}, err
	}
	options.Bucket = strings.TrimSpace(options.Bucket)
	if !s3BucketPattern.MatchString(options.Bucket) || strings.Contains(options.Bucket, "..") ||
		strings.Contains(options.Bucket, ".-") || strings.Contains(options.Bucket, "-.") || net.ParseIP(options.Bucket) != nil {
		return S3ForwardingOptions{}, errors.New("bucket must be a valid lowercase S3 bucket name")
	}
	options.Region, err = normalizeAWSRegion(options.Region)
	if err != nil {
		return S3ForwardingOptions{}, err
	}
	options.Prefix = strings.Trim(strings.TrimSpace(options.Prefix), "/")
	if options.Prefix == "" {
		options.Prefix = defaultS3Prefix
	}
	if len(options.Prefix) > 160 || !s3PrefixPattern.MatchString(options.Prefix) || strings.Contains(options.Prefix, "\\") {
		return S3ForwardingOptions{}, errors.New("prefix must be a bounded relative S3 key prefix")
	}
	for _, segment := range strings.Split(options.Prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return S3ForwardingOptions{}, errors.New("prefix cannot contain empty or traversal segments")
		}
	}
	return options, nil
}

func newPackDirectory(outputDir string) (string, error) {
	if strings.TrimSpace(outputDir) == "" || len(outputDir) > 4096 || strings.ContainsAny(outputDir, "\x00\r\n") {
		return "", errors.New("output directory must be nonempty and bounded")
	}
	abs, err := filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("resolve output directory: %w", err)
	}
	abs = filepath.Clean(abs)
	volumeRoot := filepath.Clean(filepath.VolumeName(abs) + string(filepath.Separator))
	if abs == volumeRoot {
		return "", errors.New("refusing to use a filesystem root as the pack directory")
	}
	parent := filepath.Dir(abs)
	if err := validateRealDirectoryChain(parent); err != nil {
		return "", fmt.Errorf("validate output parent: %w", err)
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", errors.New("output directory already exists; refusing to overwrite or merge it")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect output directory: %w", err)
	}
	if err := os.Mkdir(abs, 0o700); err != nil {
		return "", fmt.Errorf("create new output directory: %w", err)
	}
	return abs, nil
}

func normalizeRuntimeLogPath(logPath string) (string, error) {
	logPath = strings.TrimSpace(logPath)
	if logPath == "" || len(logPath) > 4096 || strings.ContainsAny(logPath, "\x00\r\n") {
		return "", errors.New("runtime log path must be nonempty, bounded, and contain no control separators")
	}
	if !filepath.IsAbs(logPath) {
		return "", errors.New("runtime log path must be absolute")
	}
	return filepath.Clean(logPath), nil
}

func normalizeAWSRegion(region string) (string, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		region = defaultS3Region
	}
	if len(region) > 64 || !awsRegionPattern.MatchString(region) {
		return "", errors.New("region must contain lowercase letters, digits, and hyphen-separated segments")
	}
	return region, nil
}

func validateRealDirectoryChain(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	root := volume + string(filepath.Separator)
	relative, err := filepath.Rel(root, abs)
	if err != nil {
		return err
	}
	current := filepath.Clean(root)
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("output parent components must be real directories, not links")
		}
	}
	return nil
}

func renderS3Readme(options S3ForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge AWS S3 forwarding pack

This pack contains a Vector configuration for forwarding Threadforge's local
runtime JSONL to your AWS S3 bucket %s in region %s, under %s/runtime/.
The sample event is synthetic and contains no user, host, prompt, trace, or
provider data.

## Important: forwarding is not enabled by this pack

Pack generation writes only these local files. It does not install or start
Vector, access AWS credentials, connect to S3, or upload telemetry. The config
reads from the end of the runtime log, so it does not backfill existing events.
Once you install and start Vector with this config, it exports only the event
timestamp, an allowlisted severity (info, low, medium, high, critical, or
unknown), and a fixed schema/privacy marker. It omits raw event
content, event/action names, identifiers, harness/model names, paths, and
attributes. Review timestamp metadata and destination controls before enabling
the service. Vector's checkpoint directory should be persistent and writable
by its service identity.

## Files

- vector.toml: customer-managed Vector configuration. Credentials are not
  embedded; configure AWS access in the Vector service environment or your
  approved AWS credential provider.
- sample-event.jsonl: one synthetic event for manual upload testing.
- README.md: this setup and privacy guidance.

## Validate the destination with the synthetic event

After reviewing the target bucket, you may upload only the sample event:

    aws s3 cp sample-event.jsonl \
      s3://%s/%s/smoke-tests/threadforge-synthetic-event.jsonl \
      --region %s \
      --content-type application/x-ndjson

This manual command is the only upload example in the pack. It does not copy
the local runtime log. Confirm destination access and retention controls in
AWS before configuring a long-running shipper.

## Review the Vector configuration

The Vector file source tails only the selected local runtime JSONL. Its remap
transform parses each line and constructs a fresh metadata-only object from
the allowlisted fields above; it never forwards the original event object.
The S3 sink writes compressed newline-delimited JSON with non-overwriting
object names. The pack does not manage IAM, encryption, lifecycle policy,
Vector installation, service startup, or AWS secrets.

Runtime log path used by vector.toml: %s.
`, options.Bucket, options.Region, options.Prefix,
		options.Bucket, options.Prefix, options.Region, options.LogPath)
}
