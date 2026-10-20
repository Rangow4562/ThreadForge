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

const (
	sumoTokenSecretName = "sumotoken"
	sumoAuthPath        = "/receiver/v1/http"
)

// SumoForwardingOptions configures an inert customer-managed Vector pack.
// Sumo's non-secret Auth Header endpoint and external token-file location are
// accepted; the token value itself is deliberately not accepted here.
type SumoForwardingOptions struct {
	LogPath    string
	Endpoint   string
	SecretsDir string
}

// RenderSumoVectorConfig creates a config that tails new runtime events and
// forwards only the shared metadata export. Sumo credentials remain external.
func RenderSumoVectorConfig(options SumoForwardingOptions) (string, error) {
	normalized, err := normalizeSumoOptions(options)
	if err != nil {
		return "", err
	}
	requestHeaders := fmt.Sprintf(`[sinks.threadforge_sumo.request.headers]
"x-sumo-token" = "%s"
`, vectorSecretReference(sumoTokenSecretName))
	return fmt.Sprintf(`# Threadforge local JSONL -> customer-managed Sumo Logic HTTP source.
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

[sinks.threadforge_sumo]
type = "http"
inputs = ["threadforge_runtime_metadata"]
uri = %s
method = "post"

[sinks.threadforge_sumo.encoding]
codec = "json"

[sinks.threadforge_sumo.framing]
method = "newline_delimited"

[sinks.threadforge_sumo.batch]
max_events = 500
timeout_secs = 5

[sinks.threadforge_sumo.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300

%s
`, renderVectorSecretDirectory(normalized.SecretsDir), strconv.Quote(normalized.LogPath), metadataProjectionVRL,
		strconv.Quote(normalized.Endpoint), requestHeaders), nil
}

// RenderSyntheticSumoSample returns a synthetic metadata-only event for
// customer-side sink validation. It never reads the runtime log.
func RenderSyntheticSumoSample() ([]byte, error) {
	data, err := renderSyntheticMetadataExport()
	if err != nil {
		return nil, fmt.Errorf("encode synthetic Sumo metadata sample: %w", err)
	}
	return data, nil
}

// renderSumoSmokeTestScript returns an opt-in POSIX script bound to the exact
// synthetic sample written beside it in the pack.
func renderSumoSmokeTestScript(endpoint string, sample []byte) string {
	digest := sha256.Sum256(sample)
	return fmt.Sprintf(`#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ "$1" != "--send-synthetic-event" ]; then
  echo "No request was sent. To upload exactly one synthetic metadata event, pass --send-synthetic-event." >&2
  exit 2
fi

if [ -z "${SUMO_TOKEN:-}" ]; then
  echo "SUMO_TOKEN is required; provide it through an approved secret manager." >&2
  exit 2
fi
token=$SUMO_TOKEN
unset SUMO_TOKEN
if printf '%%s' "$token" | LC_ALL=C grep -q '[[:cntrl:]]'; then
  echo "SUMO_TOKEN must not contain control characters." >&2
  exit 2
fi

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
    printf 'header = "x-sumo-token: '
    printf '%%s' "$token" | sed 's/\\/\\\\/g; s/"/\\"/g'
    printf '"\n'
  } | curl --config - --silent --show-error --fail --connect-timeout 5 --max-time 20 \
      --request POST --header 'Content-Type: application/json' \
      --data-binary "@$sample_file" --output /dev/null --write-out '%%{http_code}'
); then
  case "$status" in
    2[0-9][0-9]) printf 'Synthetic metadata event accepted (HTTP %%s); runtime logs were not read.\n' "$status" ;;
    *) echo "Sumo returned an unexpected HTTP status; response body was suppressed." >&2; exit 1 ;;
  esac
else
  echo "Synthetic Sumo upload failed; response body was suppressed." >&2
  exit 1
fi
`, hex.EncodeToString(digest[:]), shellSingleQuoted(curlConfigString(endpoint)))
}

func curlConfigString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func renderSumoPowerShellSmokeTestScript(endpoint string, sample []byte) string {
	digest := sha256.Sum256(sample)
	return fmt.Sprintf(`param(
    [switch]$SendSyntheticEvent
)

$ErrorActionPreference = 'Stop'
if (-not $SendSyntheticEvent) {
    [Console]::Error.WriteLine('No request was sent. Re-run with -SendSyntheticEvent to upload exactly one synthetic metadata event.')
    exit 2
}

$token = [Environment]::GetEnvironmentVariable('SUMO_TOKEN', 'Process')
[Environment]::SetEnvironmentVariable('SUMO_TOKEN', $null, 'Process')
if ([string]::IsNullOrEmpty($token)) {
    [Console]::Error.WriteLine('SUMO_TOKEN is required; provide it through an approved secret manager.')
    exit 2
}
if ($token -match '[\x00-\x1F\x7F]') {
    [Console]::Error.WriteLine('SUMO_TOKEN must not contain control characters.')
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
    if (-not $request.Headers.TryAddWithoutValidation('x-sumo-token', $token)) {
        throw 'token header could not be set'
    }
    $request.Content = [Net.Http.ByteArrayContent]::new($sample)
    $request.Content.Headers.ContentType = [Net.Http.Headers.MediaTypeHeaderValue]::new('application/json')
    $response = $client.SendAsync($request, [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
    $status = [int]$response.StatusCode
    if ($status -lt 200 -or $status -ge 300) {
        throw 'unexpected HTTP status'
    }
    [Console]::Out.WriteLine("Synthetic metadata event accepted (HTTP $status); runtime logs were not read.")
} catch {
    [Console]::Error.WriteLine('Synthetic Sumo upload failed; response body was suppressed.')
    $failed = $true
} finally {
    if ($null -ne $response) { $response.Dispose() }
    $request.Dispose()
    $client.Dispose()
    $handler.Dispose()
    $sample = $null
    $token = $null
}
if ($failed) { exit 1 }
`, hex.EncodeToString(digest[:]), powershellSingleQuoted(endpoint))
}

func powershellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// InstallSumoForwardingPack writes a new private, inert Vector pack locally.
func InstallSumoForwardingPack(outputDir string, options SumoForwardingOptions) (string, error) {
	normalized, err := normalizeSumoOptions(options)
	if err != nil {
		return "", err
	}
	config, err := RenderSumoVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticSumoSample()
	if err != nil {
		return "", err
	}
	smokeTest := renderSumoSmokeTestScript(normalized.Endpoint, sample)
	powershellSmokeTest := renderSumoPowerShellSmokeTestScript(normalized.Endpoint, sample)
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(renderSumoReadme(normalized))},
		{name: "sample-event.jsonl", content: sample},
		{name: "sumo-upload-smoke-test.sh", content: []byte(smokeTest)},
		{name: "sumo-upload-smoke-test.ps1", content: []byte(powershellSmokeTest)},
		{name: "vector.toml", content: []byte(config)},
	})
}

func normalizeSumoOptions(options SumoForwardingOptions) (SumoForwardingOptions, error) {
	logPath, err := normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return SumoForwardingOptions{}, err
	}
	options.LogPath = logPath
	options.Endpoint = strings.TrimSpace(options.Endpoint)
	if options.Endpoint == "" || len(options.Endpoint) > 2048 || strings.ContainsAny(options.Endpoint, "\x00\r\n") {
		return SumoForwardingOptions{}, errors.New("Sumo Logic Auth Header endpoint must be a bounded HTTPS source URL")
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || strings.Contains(options.Endpoint, "#") || endpoint.Fragment != "" || endpoint.Opaque != "" || endpoint.Path != sumoAuthPath {
		return SumoForwardingOptions{}, errors.New("Sumo Logic endpoint must be an HTTPS Auth Header URL ending exactly in /receiver/v1/http, without userinfo, query, or fragment")
	}
	if err := validateRemoteDestinationHost(endpoint.Hostname()); err != nil {
		return SumoForwardingOptions{}, errors.New("Sumo Logic endpoint must use a non-loopback valid DNS name or IP address")
	}
	if err := validateDestinationPort(endpoint.Port()); err != nil {
		return SumoForwardingOptions{}, fmt.Errorf("Sumo Logic endpoint %w", err)
	}
	endpoint.Scheme = "https"
	options.Endpoint = endpoint.String()
	options.SecretsDir, err = normalizeVectorSecretsDir(options.SecretsDir)
	if err != nil {
		return SumoForwardingOptions{}, err
	}
	return options, nil
}

func renderSumoReadme(options SumoForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge Sumo Logic forwarding pack

This private pack contains an inert customer-managed Vector configuration for
a Sumo Logic Hosted Collector HTTP Logs & Metrics Source. It reads only new
Threadforge runtime JSONL events and emits a newly constructed metadata record:
timestamp, allowlisted severity, and the fixed Threadforge schema/privacy
marker. Raw event content, event names, identifiers, harness/model names,
paths, attributes, inventory, and host-specific values are excluded.

## Forwarding is not enabled by this pack

Pack generation writes local files only. It does not install or start Vector,
read environment variables, contact Sumo Logic, or upload telemetry. The file
source starts at end-of-file and does not backfill. The generated configuration
contains the non-secret Auth Header URL %s and selected local runtime-log path;
keep the pack private. Store the single-line token in the Vector secrets
directory as %s. Keep the directory access-controlled and readable by the
Vector service identity. The Threadforge CLI never opens or stores the token.
This pack requires an Auth Header source; presigned URLs are rejected because
the URL itself is a bearer credential. Configure source
access, retention, and downstream consumers according to your policy. Keep
Vector's data directory persistent so file offsets survive restarts.

The included sample-event.jsonl contains one synthetic metadata-only record and
no source event identifiers, user/host fields, paths, prompt text, or
credentials. The optional sumo-upload-smoke-test.sh and
sumo-upload-smoke-test.ps1 are inert by default. They require an explicit send
switch (--send-synthetic-event or -SendSyntheticEvent) and SUMO_TOKEN from
an external secret manager, verify the exact sample digest, post only that one
synthetic event, and suppress the response body. They never read the runtime
log or Vector token file. The explicit PowerShell script reads SUMO_TOKEN into
its own process memory; neither script writes the token to disk, and the
threadforge CLI is not involved in the upload.
Runtime log path used by vector.toml: %s.
`, options.Endpoint, filepath.Join(options.SecretsDir, sumoTokenSecretName), options.LogPath)
}
