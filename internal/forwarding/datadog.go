package forwarding

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

const datadogAPIKeySecretName = "datadogapikey"

// DatadogForwardingOptions configures an inert customer-managed Vector pack.
// The API key is deliberately not an accepted option and is never read here.
type DatadogForwardingOptions struct {
	LogPath    string
	Endpoint   string
	SecretsDir string
}

// DatadogAgentForwardingOptions configures a local metadata-only Vector to
// Datadog Agent handoff. The Agent reads only the separate sanitized JSONL file.
type DatadogAgentForwardingOptions struct {
	LogPath         string
	MetadataLogPath string
}

func RenderDatadogVectorConfig(options DatadogForwardingOptions) (string, error) {
	normalized, err := normalizeDatadogOptions(options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Threadforge local JSONL -> customer-managed Datadog Logs.
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

[sinks.threadforge_datadog]
type = "datadog_logs"
inputs = ["threadforge_runtime_metadata"]
endpoint = %s
default_api_key = "%s"

[sinks.threadforge_datadog.batch]
max_events = 500
timeout_secs = 1

[sinks.threadforge_datadog.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300
`, renderVectorSecretDirectory(normalized.SecretsDir), strconv.Quote(normalized.LogPath), metadataProjectionVRL,
		strconv.Quote(normalized.Endpoint), vectorSecretReference(datadogAPIKeySecretName)), nil
}

func RenderSyntheticDatadogSample() ([]byte, error) {
	data, err := renderSyntheticMetadataExport()
	if err != nil {
		return nil, fmt.Errorf("encode synthetic Datadog metadata sample: %w", err)
	}
	return data, nil
}

// renderDatadogSmokeTestScripts returns opt-in customer-endpoint checks bound
// to the exact synthetic sample installed beside the scripts.
func renderDatadogSmokeTestScripts(endpoint string, sample []byte) (string, string) {
	digest := sha256.Sum256(sample)
	uploadURL := strings.TrimRight(endpoint, "/") + "/api/v2/logs"
	posix := fmt.Sprintf(`#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ "$1" != "--send-synthetic-event" ]; then
  echo "No request was sent. To upload exactly one synthetic metadata event, pass --send-synthetic-event." >&2
  exit 2
fi

if [ -z "${DD_API_KEY:-}" ]; then
  echo "DD_API_KEY is required; provide it through an approved secret manager." >&2
  exit 2
fi
api_key=$DD_API_KEY
unset DD_API_KEY
if printf '%%s' "$api_key" | LC_ALL=C grep -q '[[:cntrl:]]'; then
  echo "DD_API_KEY must not contain control characters." >&2
  exit 2
fi
escaped_api_key=$(printf '%%s' "$api_key" | sed 's/\\/\\\\/g; s/"/\\"/g')
unset api_key

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
sample_file="$script_dir/sample-event.jsonl"
if [ -L "$sample_file" ] || [ ! -f "$sample_file" ]; then
  echo "Synthetic sample is missing or is not a regular file; no request was sent." >&2
  exit 2
fi
if command -v sha256sum >/dev/null 2>&1; then
  sample_digest=$(sha256sum "$sample_file")
elif command -v shasum >/dev/null 2>&1; then
  sample_digest=$(shasum -a 256 "$sample_file")
else
  echo "sha256sum or shasum is required to verify the synthetic sample; no request was sent." >&2
  exit 2
fi
sample_digest=${sample_digest%%%% *}
if [ "$sample_digest" != "%s" ]; then
  echo "Synthetic sample digest mismatch; no request was sent." >&2
  exit 2
fi

if status=$(
  {
    printf 'url = %%s\n' %s
    printf 'header = "DD-API-KEY: %%s"\n' "$escaped_api_key"
  } | curl --config - --silent --show-error --connect-timeout 5 --max-time 20 --request POST --header 'Content-Type: application/json' --data-binary "@$sample_file" --output /dev/null --write-out '%%{http_code}'
); then
  unset escaped_api_key
  if [ "$status" = 202 ]; then
    printf 'Datadog accepted one synthetic metadata event (HTTP 202); runtime logs were not read.\n'
  else
    echo "Datadog did not accept the synthetic event (HTTP status was not 202); response body was suppressed." >&2
    exit 1
  fi
else
  unset escaped_api_key
  echo "Synthetic Datadog upload failed; response body was suppressed." >&2
  exit 1
fi
`, hex.EncodeToString(digest[:]), curlConfigString(uploadURL))

	powershell := fmt.Sprintf(`param(
    [switch]$SendSyntheticEvent
)

$ErrorActionPreference = 'Stop'
if (-not $SendSyntheticEvent) {
    [Console]::Error.WriteLine('No request was sent. Re-run with -SendSyntheticEvent to upload exactly one synthetic metadata event.')
    exit 2
}

$apiKey = [Environment]::GetEnvironmentVariable('DD_API_KEY', 'Process')
[Environment]::SetEnvironmentVariable('DD_API_KEY', $null, 'Process')
if ([string]::IsNullOrEmpty($apiKey)) {
    [Console]::Error.WriteLine('DD_API_KEY is required; provide it through an approved secret manager.')
    exit 2
}
if ($apiKey -match '[\x00-\x1F\x7F]') {
    [Console]::Error.WriteLine('DD_API_KEY must not contain control characters.')
    exit 2
}

$samplePath = Join-Path $PSScriptRoot 'sample-event.jsonl'
try {
    $sampleItem = Get-Item -LiteralPath $samplePath -Force
} catch {
    [Console]::Error.WriteLine('Synthetic sample is missing; no request was sent.')
    exit 2
}
if ($sampleItem.PSIsContainer -or (($sampleItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
    [Console]::Error.WriteLine('Synthetic sample is not a regular file; no request was sent.')
    exit 2
}
$sample = [IO.File]::ReadAllBytes($samplePath)
$sha = [Security.Cryptography.SHA256]::Create()
try {
    $sampleDigest = [BitConverter]::ToString($sha.ComputeHash($sample)).Replace('-', '').ToLowerInvariant()
} finally {
    $sha.Dispose()
}
if ($sampleDigest -ne '%s') {
    [Console]::Error.WriteLine('Synthetic sample digest mismatch; no request was sent.')
    exit 2
}

$handler = [Net.Http.HttpClientHandler]::new()
$handler.AllowAutoRedirect = $false
$client = [Net.Http.HttpClient]::new($handler)
$client.Timeout = [TimeSpan]::FromSeconds(20)
$request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::Post, %s)
$response = $null
$failed = $false
try {
    if (-not $request.Headers.TryAddWithoutValidation('DD-API-KEY', $apiKey)) {
        throw 'API key header could not be set'
    }
    $request.Content = [Net.Http.ByteArrayContent]::new($sample)
    $request.Content.Headers.ContentType = [Net.Http.Headers.MediaTypeHeaderValue]::new('application/json')
    $response = $client.SendAsync($request, [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
    $status = [int]$response.StatusCode
    if ($status -ne 202) {
        throw 'Datadog did not return HTTP 202 Accepted'
    }
    [Console]::Out.WriteLine('Datadog accepted one synthetic metadata event (HTTP 202); runtime logs were not read.')
} catch {
    [Console]::Error.WriteLine('Synthetic Datadog upload failed or was not accepted with HTTP 202; response body was suppressed.')
    $failed = $true
} finally {
    if ($null -ne $response) { $response.Dispose() }
    $request.Dispose()
    $client.Dispose()
    $handler.Dispose()
    $sample = $null
    $apiKey = $null
}
if ($failed) { exit 1 }
`, hex.EncodeToString(digest[:]), powershellSingleQuoted(uploadURL))
	return posix, powershell
}

// RenderDatadogAgentConfig creates an inert Datadog Agent file-log source for
// the sanitized metadata file, never for Threadforge's raw runtime log.
func RenderDatadogAgentConfig(options DatadogAgentForwardingOptions) (string, error) {
	normalized, err := normalizeDatadogAgentOptions(options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Threadforge metadata-only JSONL from the local Vector sanitizer.
# Keep this input pointed at the separate metadata file, never the raw trace.
logs:
  - type: file
    path: %s
    service: threadforge
    source: threadforge
    tags:
      - "threadforge_export:metadata-only"
`, strconv.Quote(normalized.MetadataLogPath)), nil
}

// RenderDatadogAgentVectorConfig creates the local-only raw-log sanitizer and
// metadata-file writer used by the Agent integration pack.
func RenderDatadogAgentVectorConfig(options DatadogAgentForwardingOptions) (string, error) {
	normalized, err := normalizeDatadogAgentOptions(options)
	if err != nil {
		return "", err
	}
	return renderLocalMetadataVectorConfig(normalized.LogPath, normalized.MetadataLogPath,
		"threadforge_datadog_metadata", metadataProjectionVRL), nil
}

// InstallDatadogAgentForwardingPack writes a local sanitizer and an Agent
// snippet. It does not install/start either service or contact Datadog.
func InstallDatadogAgentForwardingPack(outputDir string, options DatadogAgentForwardingOptions) (string, error) {
	normalized, err := normalizeDatadogAgentOptions(options)
	if err != nil {
		return "", err
	}
	vectorConfig, err := RenderDatadogAgentVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	agentConfig, err := RenderDatadogAgentConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticDatadogSample()
	if err != nil {
		return "", err
	}
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(renderDatadogAgentReadme(normalized))},
		{name: "sample-event.jsonl", content: sample},
		{name: "datadog-agent-conf.yaml", content: []byte(agentConfig)},
		{name: "vector.toml", content: []byte(vectorConfig)},
	})
}

func normalizeDatadogAgentOptions(options DatadogAgentForwardingOptions) (DatadogAgentForwardingOptions, error) {
	logPath, err := normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return DatadogAgentForwardingOptions{}, err
	}
	metadataPath, err := normalizeDatadogMetadataLogPath(options.MetadataLogPath)
	if err != nil {
		return DatadogAgentForwardingOptions{}, err
	}
	if sameFilesystemPath(logPath, metadataPath) {
		return DatadogAgentForwardingOptions{}, errors.New("Datadog metadata output path must differ from the raw Threadforge runtime log path")
	}
	options.LogPath = logPath
	options.MetadataLogPath = metadataPath
	return options, nil
}

func normalizeDatadogMetadataLogPath(path string) (string, error) {
	return normalizeMetadataOutputLogPath(path, "Datadog")
}

func InstallDatadogForwardingPack(outputDir string, options DatadogForwardingOptions) (string, error) {
	normalized, err := normalizeDatadogOptions(options)
	if err != nil {
		return "", err
	}
	config, err := RenderDatadogVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticDatadogSample()
	if err != nil {
		return "", err
	}
	smokeTest, powershellSmokeTest := renderDatadogSmokeTestScripts(normalized.Endpoint, sample)
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(renderDatadogReadme(normalized))},
		{name: "sample-event.jsonl", content: sample},
		{name: "datadog-upload-smoke-test.sh", content: []byte(smokeTest)},
		{name: "datadog-upload-smoke-test.ps1", content: []byte(powershellSmokeTest)},
		{name: "vector.toml", content: []byte(config)},
	})
}

func normalizeDatadogOptions(options DatadogForwardingOptions) (DatadogForwardingOptions, error) {
	var err error
	options.LogPath, err = normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return DatadogForwardingOptions{}, err
	}
	options.SecretsDir, err = normalizeVectorSecretsDir(options.SecretsDir)
	if err != nil {
		return DatadogForwardingOptions{}, err
	}
	options.Endpoint = strings.TrimSpace(options.Endpoint)
	if options.Endpoint == "" || len(options.Endpoint) > 2048 || strings.ContainsAny(options.Endpoint, "\x00\r\n") {
		return DatadogForwardingOptions{}, errors.New("Datadog Logs endpoint must be a bounded HTTPS origin")
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.ForceQuery ||
		strings.Contains(options.Endpoint, "#") || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return DatadogForwardingOptions{}, errors.New("Datadog Logs endpoint must be HTTPS with a host only; userinfo, paths, queries, and fragments are not accepted")
	}
	if err := validateRemoteDestinationHost(endpoint.Hostname()); err != nil {
		return DatadogForwardingOptions{}, errors.New("Datadog Logs endpoint must use a non-loopback valid DNS name or IP address")
	}
	if err := validateDestinationPort(endpoint.Port()); err != nil {
		return DatadogForwardingOptions{}, fmt.Errorf("Datadog Logs endpoint %w", err)
	}
	endpoint.Scheme = "https"
	endpoint.Path = ""
	endpoint.RawPath = ""
	options.Endpoint = endpoint.String()
	return options, nil
}

func renderDatadogReadme(options DatadogForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge Datadog Logs forwarding pack

This pack contains an inert customer-managed Vector configuration for the
HTTPS Datadog Logs endpoint %s. It reads only Threadforge runtime JSONL and
exports a newly constructed document containing the event timestamp,
allowlisted severity, and fixed schema/privacy marker. Raw event content,
action names, identifiers, harness/model names, paths, attributes, inventory,
and host-specific values are excluded.

## Forwarding is not enabled by this pack

Pack generation writes local files only. It does not install or start Vector,
read an API key, contact Datadog, or upload telemetry. The file source starts
at end-of-file and does not backfill. The endpoint is included in vector.toml;
keep the generated directory private.

Before starting Vector, place the API key in the Vector secrets directory as
the single-line file %s. Threadforge never opens or stores the key. Set the endpoint to the
Datadog Logs intake for your account's site, and configure destination access,
retention, and downstream consumers according to your policy. Keep Vector's
data directory persistent so file offsets survive restarts.

The included sample-event.jsonl contains one synthetic metadata-only document.
It has no source event identifiers, user or host fields, paths, prompt text,
or credentials. The optional datadog-upload-smoke-test.sh and
datadog-upload-smoke-test.ps1 scripts are inert by default. Only an explicit
--send-synthetic-event or -SendSyntheticEvent invocation reads DD_API_KEY from
the external process environment and submits the SHA-256-pinned sample to the
Datadog Logs HTTP API at /api/v2/logs. They require HTTP 202 Accepted, do not
follow redirects, suppress response bodies, and never read runtime JSONL or the
Vector secrets file. A 202 confirms acceptance for processing, not indexing or
retention. Regenerate the pack if its sample timestamp is more than 18 hours
old. Runtime log path used by vector.toml: %s.
`, options.Endpoint, filepath.Join(options.SecretsDir, datadogAPIKeySecretName), options.LogPath)
}

func renderDatadogAgentReadme(options DatadogAgentForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge Datadog Agent metadata-only pack

This pack contains two inert configuration files. Vector reads new Threadforge
runtime JSONL from end-of-file, constructs a fresh record containing only the
event timestamp, allowlisted severity, and the fixed Threadforge privacy marker,
then writes it to:

  %s

The Datadog Agent snippet reads only that separate metadata file. It never
points at the raw Threadforge trace. The included sample-event.jsonl is
synthetic and contains no runtime record or identifying details.

## Explicit activation and forwarding boundary

Generating this pack is local-only; it does not install or start Vector or the
Datadog Agent, edit host configuration, read credentials, or contact Datadog.
The datadog-agent-conf.yaml snippet contains no API key or site value.
Configure the Agent's global log-collection setting and credentials through
your existing Datadog deployment process, then review its host tags and remote
destination policy before activation. Starting the Agent may forward sanitized
Threadforge metadata and the Agent's normal host metadata to that account.

Install vector.toml with your customer-managed Vector service and keep
its data directory persistent. The runtime input starts at EOF and does not
backfill raw logs. Ensure Vector can write the metadata file and the Datadog
Agent service identity can read it, without granting either service broader
access to Threadforge's raw trace than required.

Before activation, confirm datadog-agent-conf.yaml points only to the
metadata path above. Keep the pack private because the files contain local
filesystem paths. Use the synthetic sample to inspect the metadata shape.

Raw runtime path: %s
Metadata-only output path: %s
`, options.MetadataLogPath, options.LogPath, options.MetadataLogPath)
}
