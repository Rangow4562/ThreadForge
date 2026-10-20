package forwarding

import (
	"fmt"
	"strconv"
)

const rapid7WebhookEnvironmentName = "THREADFORGE_RAPID7_WEBHOOK_URL"

// Rapid7ForwardingOptions configures an inert customer-managed Vector pack.
// The bearer webhook URL is never accepted by Threadforge.
type Rapid7ForwardingOptions struct {
	LogPath string
}

// RenderRapid7VectorConfig tails new local events and emits the strict shared
// metadata projection as newline-delimited JSON to the external webhook.
func RenderRapid7VectorConfig(options Rapid7ForwardingOptions) (string, error) {
	normalized, err := normalizeRapid7Options(options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Threadforge local JSONL -> customer-managed Rapid7 InsightIDR Custom Logs.
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

[sinks.threadforge_rapid7]
type = "http"
inputs = ["threadforge_runtime_metadata"]
uri = "${%s}"
method = "post"

[sinks.threadforge_rapid7.encoding]
codec = "json"

[sinks.threadforge_rapid7.framing]
method = "newline_delimited"

[sinks.threadforge_rapid7.batch]
max_events = 500
timeout_secs = 5

[sinks.threadforge_rapid7.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300

[sinks.threadforge_rapid7.request.headers]
"Content-Type" = "application/x-ndjson"
`, strconv.Quote(normalized.LogPath), metadataProjectionVRL, rapid7WebhookEnvironmentName), nil
}

// RenderSyntheticRapid7Sample returns a synthetic metadata-only event.
func RenderSyntheticRapid7Sample() ([]byte, error) {
	data, err := renderSyntheticMetadataExport()
	if err != nil {
		return nil, fmt.Errorf("encode synthetic Rapid7 metadata sample: %w", err)
	}
	return data, nil
}

// InstallRapid7ForwardingPack writes an inert customer-managed Vector pack.
func InstallRapid7ForwardingPack(outputDir string, options Rapid7ForwardingOptions) (string, error) {
	normalized, err := normalizeRapid7Options(options)
	if err != nil {
		return "", err
	}
	config, err := RenderRapid7VectorConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticRapid7Sample()
	if err != nil {
		return "", err
	}
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(renderRapid7Readme(normalized))},
		{name: "sample-event.jsonl", content: sample},
		{name: "vector.toml", content: []byte(config)},
	})
}

func normalizeRapid7Options(options Rapid7ForwardingOptions) (Rapid7ForwardingOptions, error) {
	logPath, err := normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return Rapid7ForwardingOptions{}, err
	}
	options.LogPath = logPath
	return options, nil
}

func renderRapid7Readme(options Rapid7ForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge Rapid7 InsightIDR forwarding pack

This private pack contains an inert customer-managed Vector configuration for
a Rapid7 InsightIDR Custom Logs webhook event source. It reads only new
Threadforge runtime JSONL events and emits a newly constructed metadata record:
timestamp, allowlisted severity, and the fixed Threadforge schema/privacy
marker. Raw event content, event names, identifiers, harness/model names,
paths, attributes, inventory, and host-specific values are excluded.

## Forwarding is not enabled by this pack

Pack generation writes local files only. It does not install or start Vector,
read environment variables, contact Rapid7, or upload telemetry. The file
source starts at end-of-file and does not backfill. The generated configuration
contains only the environment-variable name, not the webhook URL; keep the pack
private because it includes the selected local runtime-log path.

Set %s in the Vector service's protected process environment using your
approved secret manager. Threadforge never reads or stores its value. Vector
0.57+ disables environment interpolation by default; start Vector with
--dangerously-allow-env-var-interpolation (or set
VECTOR_DANGEROUSLY_ALLOW_ENV_VAR_INTERPOLATION=true) only after reviewing
that this enables interpolation for the Vector process. Restrict process and
service inspection access. Use the HTTPS webhook URL supplied for a Rapid7
InsightIDR Custom Logs event source configured for Webhook collection. Keep
Vector's data directory persistent so file offsets survive restarts.

The included sample-event.jsonl contains one synthetic metadata-only record and
no source event identifiers, user/host fields, paths, prompt text, or
credentials. Validate it only through your approved Rapid7/Vector procedure.
Runtime log path used by vector.toml: %s.
`, rapid7WebhookEnvironmentName, options.LogPath)
}
