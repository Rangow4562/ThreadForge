package forwarding

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

const defaultGCSPrefix = "threadforge"

var (
	gcsBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	gcsPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
)

// GCSForwardingOptions configures an inert customer-managed Vector handoff.
// Google credentials are not accepted or embedded.
type GCSForwardingOptions struct {
	LogPath string
	Bucket  string
	Prefix  string
}

// RenderGCSVectorConfig tails only new runtime events and emits the shared
// metadata-only schema to a customer-managed Google Cloud Storage bucket.
func RenderGCSVectorConfig(options GCSForwardingOptions) (string, error) {
	normalized, err := normalizeGCSOptions(options)
	if err != nil {
		return "", err
	}
	keyPrefix := normalized.Prefix + "/runtime/date=%F/"
	return fmt.Sprintf(`# Threadforge local JSONL -> customer-managed Google Cloud Storage.
# This file is inert until you install and start Vector yourself.

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

[sinks.threadforge_runtime_gcs]
type = "gcp_cloud_storage"
inputs = ["threadforge_runtime_metadata"]
bucket = %s
key_prefix = %s
filename_time_format = "%%s"
filename_append_uuid = true
filename_extension = "jsonl"
content_type = "application/x-ndjson"
storage_class = "STANDARD"

[sinks.threadforge_runtime_gcs.encoding]
codec = "json"

[sinks.threadforge_runtime_gcs.framing]
method = "newline_delimited"

[sinks.threadforge_runtime_gcs.batch]
max_bytes = 10000000
timeout_secs = 300

[sinks.threadforge_runtime_gcs.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300

[sinks.threadforge_runtime_gcs.healthcheck]
enabled = false
`, strconv.Quote(normalized.LogPath), metadataProjectionVRL,
		strconv.Quote(normalized.Bucket), strconv.Quote(keyPrefix)), nil
}

func RenderSyntheticGCSSample() ([]byte, error) {
	return renderSyntheticForwardingSample("gcs", "google_cloud_storage_jsonl")
}

func InstallGCSForwardingPack(outputDir string, options GCSForwardingOptions) (string, error) {
	normalized, err := normalizeGCSOptions(options)
	if err != nil {
		return "", err
	}
	config, err := RenderGCSVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticGCSSample()
	if err != nil {
		return "", err
	}
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(renderGCSReadme(normalized))},
		{name: "sample-event.jsonl", content: sample},
		{name: "vector.toml", content: []byte(config)},
	})
}

func normalizeGCSOptions(options GCSForwardingOptions) (GCSForwardingOptions, error) {
	var err error
	options.LogPath, err = normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return GCSForwardingOptions{}, err
	}
	options.Bucket = strings.TrimSpace(options.Bucket)
	if !gcsBucketPattern.MatchString(options.Bucket) || strings.Contains(options.Bucket, "..") ||
		strings.Contains(options.Bucket, ".-") || strings.Contains(options.Bucket, "-.") || net.ParseIP(options.Bucket) != nil {
		return GCSForwardingOptions{}, errors.New("bucket must be a valid lowercase Google Cloud Storage bucket name")
	}
	options.Prefix = strings.Trim(strings.TrimSpace(options.Prefix), "/")
	if options.Prefix == "" {
		options.Prefix = defaultGCSPrefix
	}
	if len(options.Prefix) > 160 || !gcsPrefixPattern.MatchString(options.Prefix) || strings.Contains(options.Prefix, "\\") {
		return GCSForwardingOptions{}, errors.New("prefix must be a bounded relative object key prefix")
	}
	for _, segment := range strings.Split(options.Prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return GCSForwardingOptions{}, errors.New("prefix cannot contain empty or traversal segments")
		}
	}
	return options, nil
}

func renderGCSReadme(options GCSForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge Google Cloud Storage forwarding pack

This pack contains an inert customer-managed Vector configuration for bucket
%s under prefix %s/runtime/. It reads only Threadforge runtime JSONL and
exports the versioned metadata-only projection: event timestamp, allowlisted
severity, and a fixed schema/privacy marker. It does not export inventory,
prompts, tool bodies, identifiers, host identity, or raw event objects.

## Forwarding is not enabled by this pack

Pack generation writes only local files. It does not install or start Vector,
read Google credentials, contact Google Cloud, or upload telemetry. Runtime
tailing starts at end-of-file and does not backfill historical events. The
generated configuration disables the GCS startup healthcheck to support
write-only object-creator credentials; monitor upload errors and validate
delivery separately with an authorized reader.

Provide Google credentials to the Vector service through its supported
credential provider or host identity. Configure bucket IAM, lifecycle,
retention, and encryption in Google Cloud. Keep Vector's data directory
persistent so file offsets survive restarts.

The sample-event.jsonl file contains one synthetic event. If you choose, upload
only that synthetic record for a destination smoke test:

    gcloud storage cp sample-event.jsonl \
      gs://%s/%s/smoke-tests/threadforge-synthetic-event.jsonl \
      --content-type application/x-ndjson

This command is not run by Threadforge. Runtime log path used by vector.toml:
%s.
`, options.Bucket, options.Prefix, options.Bucket, options.Prefix, options.LogPath)
}
