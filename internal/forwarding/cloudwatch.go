package forwarding

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const defaultCloudWatchStreamName = "threadforge-metadata"

var cloudWatchGroupPattern = regexp.MustCompile(`^[A-Za-z0-9._/#-]{1,512}$`)

// CloudWatchForwardingOptions describes an inert, metadata-only Vector config.
// No credentials are accepted and rendering never performs network I/O.
type CloudWatchForwardingOptions struct {
	LogPath  string
	LogGroup string
	Region   string
}

// RenderCloudWatchVectorConfig tails new runtime events and sends only the
// allowlisted metadata projection to a customer-managed CloudWatch log group.
func RenderCloudWatchVectorConfig(options CloudWatchForwardingOptions) (string, error) {
	normalized, err := normalizeCloudWatchOptions(options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Threadforge local JSONL -> customer-managed AWS CloudWatch Logs.
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

[sinks.threadforge_cloudwatch]
type = "aws_cloudwatch_logs"
inputs = ["threadforge_runtime_metadata"]
group_name = %s
stream_name = %s
region = %s
create_missing_group = false
create_missing_stream = true

[sinks.threadforge_cloudwatch.encoding]
codec = "json"

[sinks.threadforge_cloudwatch.batch]
max_events = 1000
timeout_secs = 30

[sinks.threadforge_cloudwatch.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300
`, strconv.Quote(normalized.LogPath), metadataProjectionVRL,
		strconv.Quote(normalized.LogGroup), strconv.Quote(defaultCloudWatchStreamName), strconv.Quote(normalized.Region)), nil
}

func RenderSyntheticCloudWatchSample() ([]byte, error) {
	return renderSyntheticForwardingSample("cloudwatch", "aws_cloudwatch_logs")
}

func InstallCloudWatchForwardingPack(outputDir string, options CloudWatchForwardingOptions) (string, error) {
	normalized, err := normalizeCloudWatchOptions(options)
	if err != nil {
		return "", err
	}
	config, err := RenderCloudWatchVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticCloudWatchSample()
	if err != nil {
		return "", err
	}
	readme := renderCloudWatchReadme(normalized)
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(readme)},
		{name: "sample-event.jsonl", content: sample},
		{name: "vector.toml", content: []byte(config)},
	})
}

func normalizeCloudWatchOptions(options CloudWatchForwardingOptions) (CloudWatchForwardingOptions, error) {
	var err error
	options.LogPath, err = normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return CloudWatchForwardingOptions{}, err
	}
	options.LogGroup = strings.TrimSpace(options.LogGroup)
	if !cloudWatchGroupPattern.MatchString(options.LogGroup) {
		return CloudWatchForwardingOptions{}, errors.New("CloudWatch log group must be 1..512 safe ASCII characters")
	}
	options.Region, err = normalizeAWSRegion(options.Region)
	if err != nil {
		return CloudWatchForwardingOptions{}, err
	}
	return options, nil
}

func renderCloudWatchReadme(options CloudWatchForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge AWS CloudWatch Logs forwarding pack

This pack contains an inert customer-managed Vector configuration for the
CloudWatch Logs group %s in region %s. It reads the local Threadforge runtime
JSONL and exports only event timestamp, allowlisted severity, and a fixed
schema/privacy marker. Raw events, action names, identities, harness/model
names, paths, attributes, and credentials are excluded.

## Forwarding is not enabled by this pack

Pack generation writes local files only. It does not install or start Vector,
access AWS credentials, connect to AWS, or upload telemetry. The file source
starts at the end of the log and does not backfill. Once started, Vector creates
the configured log stream in the pre-existing CloudWatch log group. The stream
name is the fixed, non-host-identifying value %s.

Provide AWS access to the Vector process through its standard AWS credential
provider chain or host identity. The log group must already exist; configure
retention, encryption, permissions, and downstream subscriptions in AWS.
Vector's data directory should be persistent and writable by its service
identity so file offsets survive restarts.

The included sample-event.jsonl is synthetic. Review the destination and its
retention/access controls before manually uploading it or activating Vector.
Runtime log path used by vector.toml: %s.
`, options.LogGroup, options.Region, defaultCloudWatchStreamName, options.LogPath)
}
