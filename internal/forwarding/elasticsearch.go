package forwarding

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	elasticsearchAPIKeySecretName  = "elasticsearchapikey"
	defaultElasticsearchIndex      = "threadforge-metadata"
	defaultElasticsearchDataStream = "logs-threadforge.endpoint-default"
)

var elasticsearchIndexPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,254}$`)

// ElasticsearchForwardingOptions describes an inert customer-managed Vector
// pack. Threadforge never accepts or reads Elasticsearch credentials.
type ElasticsearchForwardingOptions struct {
	LogPath    string
	Endpoint   string
	Index      string
	SecretsDir string
}

// ElasticsearchAgentForwardingOptions configures a local metadata sanitizer
// and one Elastic shipper that reads only its separate sanitized JSONL output.
type ElasticsearchAgentForwardingOptions struct {
	LogPath         string
	MetadataLogPath string
	Endpoint        string
}

func RenderElasticsearchVectorConfig(options ElasticsearchForwardingOptions) (string, error) {
	normalized, err := normalizeElasticsearchOptions(options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Threadforge local JSONL -> customer-managed Elasticsearch.
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

[sinks.threadforge_elasticsearch]
type = "elasticsearch"
inputs = ["threadforge_runtime_metadata"]
endpoints = [%s]
bulk.index = %s

[sinks.threadforge_elasticsearch.batch]
max_events = 500
timeout_secs = 1

[sinks.threadforge_elasticsearch.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300

[sinks.threadforge_elasticsearch.request.headers]
Authorization = "ApiKey %s"
`, renderVectorSecretDirectory(normalized.SecretsDir), strconv.Quote(normalized.LogPath), metadataProjectionVRL,
		strconv.Quote(normalized.Endpoint), strconv.Quote(normalized.Index), vectorSecretReference(elasticsearchAPIKeySecretName)), nil
}

// RenderSyntheticElasticsearchSample returns a single metadata-only document
// with a generated timestamp and no source event content or identifiers.
func RenderSyntheticElasticsearchSample() ([]byte, error) {
	data, err := renderSyntheticMetadataExport()
	if err != nil {
		return nil, fmt.Errorf("encode synthetic Elasticsearch metadata sample: %w", err)
	}
	return data, nil
}

func renderElasticsearchSmokeTestScripts(endpoint string, sample []byte) (string, string) {
	digest := sha256.Sum256(sample)
	streamURL := strings.TrimRight(endpoint, "/") + "/_data_stream/" + defaultElasticsearchDataStream
	documentURL := strings.TrimRight(endpoint, "/") + "/" + defaultElasticsearchDataStream + "/_doc?pipeline=" + elasticsearchMetadataPipeline

	posix := fmt.Sprintf(`#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ "$1" != "--send-synthetic-event" ]; then
  echo "No request was sent. To POST exactly one synthetic metadata record, pass --send-synthetic-event." >&2
  exit 2
fi

api_key=${ELASTIC_API_KEY:-}
username=${ELASTIC_USERNAME:-}
password=${ELASTIC_PASSWORD:-}
unset ELASTIC_API_KEY ELASTIC_USERNAME ELASTIC_PASSWORD

has_control() {
  printf '%%s' "$1" | LC_ALL=C grep -q '[[:cntrl:]]'
}
if has_control "$api_key" || has_control "$username" || has_control "$password"; then
  echo "Elasticsearch credentials must not contain control characters." >&2
  exit 2
fi
if [ -n "$api_key" ]; then
  if [ -n "$username" ] || [ -n "$password" ]; then
    echo "Choose ELASTIC_API_KEY or the username/password pair, not both." >&2
    exit 2
  fi
  auth_scheme=ApiKey
  auth_value=$api_key
elif [ -n "$username" ] && [ -n "$password" ]; then
  case "$username" in
    *:*) echo "ELASTIC_USERNAME must not contain a colon for Basic authentication." >&2; exit 2 ;;
  esac
  auth_scheme=Basic
  auth_value=$(printf '%%s:%%s' "$username" "$password" | base64 | tr -d '\r\n')
else
  echo "Set ELASTIC_API_KEY or both ELASTIC_USERNAME and ELASTIC_PASSWORD from an approved secret manager." >&2
  exit 2
fi
unset api_key username password
escaped_auth=$(printf '%%s' "$auth_value" | sed 's/\\/\\\\/g; s/"/\\"/g')
unset auth_value

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
sample_digest=${sample_digest%% *}
if [ "$sample_digest" != "%s" ]; then
  echo "Synthetic sample digest mismatch; no request was sent." >&2
  exit 2
fi

request_status() {
  request_url=$1
  request_method=$2
  {
    printf 'url = %%s\n' "$request_url"
    printf 'header = "Authorization: %%s %%s"\n' "$auth_scheme" "$escaped_auth"
  } | if [ "$request_method" = POST ]; then
    curl --config - --silent --show-error --fail --connect-timeout 5 --max-time 20 \
      --request POST --header 'Content-Type: application/json' \
      --data-binary "@$sample_file" --output /dev/null --write-out '%%{http_code}'
  else
    curl --config - --silent --show-error --fail --connect-timeout 5 --max-time 20 \
      --request GET --output /dev/null --write-out '%%{http_code}'
  fi
}

if stream_status=$(request_status %s GET); then
  case "$stream_status" in
    2[0-9][0-9]) ;;
    *) echo "The expected Elasticsearch data stream is unavailable; no document was sent." >&2; exit 1 ;;
  esac
else
  echo "Could not verify the Elasticsearch data stream; no document was sent." >&2
  exit 1
fi
if document_status=$(request_status %s POST); then
  case "$document_status" in
    2[0-9][0-9]) printf 'Synthetic metadata document accepted (HTTP %%s); runtime logs were not read.\n' "$document_status" ;;
    *) echo "Elasticsearch returned an unexpected HTTP status; response body was suppressed." >&2; exit 1 ;;
  esac
else
  echo "Synthetic Elasticsearch upload failed; response body was suppressed." >&2
  exit 1
fi
`, hex.EncodeToString(digest[:]), shellSingleQuoted(streamURL), shellSingleQuoted(documentURL))

	powershell := fmt.Sprintf(`param(
    [switch]$SendSyntheticEvent
)

$ErrorActionPreference = 'Stop'
if (-not $SendSyntheticEvent) {
    [Console]::Error.WriteLine('No request was sent. Re-run with -SendSyntheticEvent to upload exactly one synthetic metadata record.')
    exit 2
}

$apiKey = [Environment]::GetEnvironmentVariable('ELASTIC_API_KEY', 'Process')
$username = [Environment]::GetEnvironmentVariable('ELASTIC_USERNAME', 'Process')
$password = [Environment]::GetEnvironmentVariable('ELASTIC_PASSWORD', 'Process')
[Environment]::SetEnvironmentVariable('ELASTIC_API_KEY', $null, 'Process')
[Environment]::SetEnvironmentVariable('ELASTIC_USERNAME', $null, 'Process')
[Environment]::SetEnvironmentVariable('ELASTIC_PASSWORD', $null, 'Process')
foreach ($credential in @($apiKey, $username, $password)) {
    if ($null -ne $credential -and $credential -match '[\x00-\x1F\x7F]') {
        [Console]::Error.WriteLine('Elasticsearch credentials must not contain control characters.')
        exit 2
    }
}
if (-not [string]::IsNullOrEmpty($apiKey)) {
    if (-not [string]::IsNullOrEmpty($username) -or -not [string]::IsNullOrEmpty($password)) {
        [Console]::Error.WriteLine('Choose ELASTIC_API_KEY or the username/password pair, not both.')
        exit 2
    }
    $authorization = 'ApiKey ' + $apiKey
} elseif (-not [string]::IsNullOrEmpty($username) -and -not [string]::IsNullOrEmpty($password)) {
    if ($username.Contains(':')) {
        [Console]::Error.WriteLine('ELASTIC_USERNAME must not contain a colon for Basic authentication.')
        exit 2
    }
    $basicBytes = [Text.Encoding]::UTF8.GetBytes($username + ':' + $password)
    $authorization = 'Basic ' + [Convert]::ToBase64String($basicBytes)
    $basicBytes = $null
} else {
    [Console]::Error.WriteLine('Set ELASTIC_API_KEY or both ELASTIC_USERNAME and ELASTIC_PASSWORD from an approved secret manager.')
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
function Invoke-ThreadforgeSmokeRequest([string]$Method, [string]$Uri, [byte[]]$Body, [string]$Authorization) {
    $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::new($Method), $Uri)
    $response = $null
    try {
        if (-not $request.Headers.TryAddWithoutValidation('Authorization', $Authorization)) {
            throw 'authorization header could not be set'
        }
        if ($Method -eq 'POST') {
            $request.Content = [Net.Http.ByteArrayContent]::new($Body)
            $request.Content.Headers.ContentType = [Net.Http.Headers.MediaTypeHeaderValue]::new('application/json')
        }
        $response = $client.SendAsync($request, [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
        return [int]$response.StatusCode
    } finally {
        if ($null -ne $response) { $response.Dispose() }
        $request.Dispose()
    }
}
$failed = $false
try {
    $streamStatus = Invoke-ThreadforgeSmokeRequest 'GET' %s $null $authorization
    if ($streamStatus -lt 200 -or $streamStatus -ge 300) {
        throw 'expected data stream is unavailable'
    }
    $documentStatus = Invoke-ThreadforgeSmokeRequest 'POST' %s $sample $authorization
    if ($documentStatus -lt 200 -or $documentStatus -ge 300) {
        throw 'unexpected document response status'
    }
    [Console]::Out.WriteLine("Synthetic metadata document accepted (HTTP $documentStatus); runtime logs were not read.")
} catch {
    [Console]::Error.WriteLine('Synthetic Elasticsearch upload failed or the expected data stream is unavailable; response body was suppressed.')
    $failed = $true
} finally {
    $client.Dispose()
    $handler.Dispose()
    $sample = $null
    $apiKey = $null
    $username = $null
    $password = $null
    $authorization = $null
}
if ($failed) { exit 1 }
`, hex.EncodeToString(digest[:]), powershellSingleQuoted(streamURL), powershellSingleQuoted(documentURL))
	return posix, powershell
}

func shellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// RenderElasticsearchFilebeatConfig renders a credential-free Filebeat config
// for the separate sanitized metadata JSONL file.
func RenderElasticsearchFilebeatConfig(options ElasticsearchAgentForwardingOptions) (string, error) {
	normalized, err := normalizeElasticsearchAgentOptions(options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Threadforge sanitized metadata only; do not point this input at raw traces.
filebeat.inputs:
  - type: filestream
    id: threadforge-metadata
    enabled: true
    paths:
      - %s
    parsers:
      - ndjson:
          keys_under_root: true
          add_error_key: true
          overwrite_keys: true
    fields_under_root: true
    fields:
      data_stream.type: logs
      data_stream.dataset: threadforge.endpoint
      data_stream.namespace: default

output.elasticsearch:
  hosts: [%s]
  ssl.verification_mode: full
  index: %s
  # Configure authentication through Filebeat's secure keystore or your
  # deployment secret manager. Do not place credentials in this file.

setup.template.enabled: false
setup.ilm.enabled: false
`, strconv.Quote(normalized.MetadataLogPath), strconv.Quote(normalized.Endpoint), strconv.Quote(defaultElasticsearchDataStream)), nil
}

// RenderElasticsearchStandaloneAgentConfig renders a credential-free
// standalone Elastic Agent config for the separate sanitized metadata file.
func RenderElasticsearchStandaloneAgentConfig(options ElasticsearchAgentForwardingOptions) (string, error) {
	normalized, err := normalizeElasticsearchAgentOptions(options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`# Threadforge sanitized metadata only; do not point this input at raw traces.
outputs:
  default:
    type: elasticsearch
    hosts: [%s]
    ssl.verification_mode: full
    # Configure authentication through your deployment secret manager.
    # Do not place credentials in this file.

inputs:
  - id: threadforge-metadata
    type: filestream
    use_output: default
    streams:
      - id: threadforge-metadata-jsonl
        data_stream:
          type: logs
          dataset: threadforge.endpoint
          namespace: default
        paths:
          - %s
        parsers:
          - ndjson:
              keys_under_root: true
              add_error_key: true
              overwrite_keys: true
`, strconv.Quote(normalized.Endpoint), strconv.Quote(normalized.MetadataLogPath)), nil
}

// RenderElasticsearchAgentVectorConfig sanitizes local runtime events into a
// distinct metadata file for Filebeat or standalone Elastic Agent to consume.
func RenderElasticsearchAgentVectorConfig(options ElasticsearchAgentForwardingOptions) (string, error) {
	normalized, err := normalizeElasticsearchAgentOptions(options)
	if err != nil {
		return "", err
	}
	return renderLocalMetadataVectorConfig(normalized.LogPath, normalized.MetadataLogPath,
		"threadforge_elasticsearch_metadata", metadataProjectionVRL), nil
}

// InstallElasticsearchAgentForwardingPack writes a local Vector sanitizer and
// one native shipper config. It does not install/start services or contact the
// Elasticsearch endpoint.
func InstallElasticsearchAgentForwardingPack(outputDir, shipper string, options ElasticsearchAgentForwardingOptions) (string, error) {
	normalized, err := normalizeElasticsearchAgentOptions(options)
	if err != nil {
		return "", err
	}
	vectorConfig, err := RenderElasticsearchAgentVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	var shipperFile, shipperConfig string
	switch shipper {
	case "filebeat":
		shipperFile = "filebeat.yml"
		shipperConfig, err = RenderElasticsearchFilebeatConfig(normalized)
	case "elastic-agent":
		shipperFile = "elastic-agent-standalone.yml"
		shipperConfig, err = RenderElasticsearchStandaloneAgentConfig(normalized)
	default:
		return "", errors.New("Elasticsearch agent path must be filebeat or elastic-agent")
	}
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticElasticsearchSample()
	if err != nil {
		return "", err
	}
	posixSmokeTest, powershellSmokeTest := renderElasticsearchSmokeTestScripts(normalized.Endpoint, sample)
	assets, err := elasticsearchPackAssets(defaultElasticsearchIndex)
	if err != nil {
		return "", err
	}
	files := []privatePackFile{
		{name: "README.md", content: []byte(renderElasticsearchAgentReadme(shipper, normalized))},
		{name: "sample-event.jsonl", content: sample},
		{name: "elasticsearch-upload-smoke-test.sh", content: []byte(posixSmokeTest)},
		{name: "elasticsearch-upload-smoke-test.ps1", content: []byte(powershellSmokeTest)},
		{name: "vector.toml", content: []byte(vectorConfig)},
		{name: shipperFile, content: []byte(shipperConfig)},
	}
	files = append(files, assets...)
	files = append(files, elasticsearchLocalStackFiles(sample)...)
	return writePrivatePack(outputDir, files)
}

func normalizeElasticsearchAgentOptions(options ElasticsearchAgentForwardingOptions) (ElasticsearchAgentForwardingOptions, error) {
	logPath, err := normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return ElasticsearchAgentForwardingOptions{}, err
	}
	endpoint, _, err := normalizeElasticsearchDestination(options.Endpoint, defaultElasticsearchIndex)
	if err != nil {
		return ElasticsearchAgentForwardingOptions{}, err
	}
	metadataPath, err := normalizeMetadataOutputLogPath(options.MetadataLogPath, "Elasticsearch")
	if err != nil {
		return ElasticsearchAgentForwardingOptions{}, err
	}
	if sameFilesystemPath(logPath, metadataPath) {
		return ElasticsearchAgentForwardingOptions{}, errors.New("Elasticsearch metadata output path must differ from the raw Threadforge runtime log path")
	}
	return ElasticsearchAgentForwardingOptions{
		LogPath: logPath, MetadataLogPath: metadataPath, Endpoint: endpoint,
	}, nil
}

func InstallElasticsearchForwardingPack(outputDir string, options ElasticsearchForwardingOptions) (string, error) {
	normalized, err := normalizeElasticsearchOptions(options)
	if err != nil {
		return "", err
	}
	config, err := RenderElasticsearchVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticElasticsearchSample()
	if err != nil {
		return "", err
	}
	posixSmokeTest, powershellSmokeTest := renderElasticsearchSmokeTestScripts(normalized.Endpoint, sample)
	assets, err := elasticsearchPackAssets(normalized.Index)
	if err != nil {
		return "", err
	}
	files := []privatePackFile{
		{name: "README.md", content: []byte(renderElasticsearchReadme(normalized))},
		{name: "sample-event.jsonl", content: sample},
		{name: "elasticsearch-upload-smoke-test.sh", content: []byte(posixSmokeTest)},
		{name: "elasticsearch-upload-smoke-test.ps1", content: []byte(powershellSmokeTest)},
		{name: "vector.toml", content: []byte(config)},
	}
	files = append(files, assets...)
	files = append(files, elasticsearchLocalStackFiles(sample)...)
	return writePrivatePack(outputDir, files)
}

func normalizeElasticsearchOptions(options ElasticsearchForwardingOptions) (ElasticsearchForwardingOptions, error) {
	var err error
	options.LogPath, err = normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return ElasticsearchForwardingOptions{}, err
	}
	options.SecretsDir, err = normalizeVectorSecretsDir(options.SecretsDir)
	if err != nil {
		return ElasticsearchForwardingOptions{}, err
	}
	options.Endpoint, options.Index, err = normalizeElasticsearchDestination(options.Endpoint, options.Index)
	if err != nil {
		return ElasticsearchForwardingOptions{}, err
	}
	return options, nil
}

func normalizeElasticsearchDestination(rawEndpoint, rawIndex string) (string, string, error) {
	rawEndpoint = strings.TrimSpace(rawEndpoint)
	if rawEndpoint == "" || len(rawEndpoint) > 2048 || strings.ContainsAny(rawEndpoint, "\x00\r\n") {
		return "", "", errors.New("Elasticsearch endpoint must be a bounded HTTPS origin")
	}
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.ForceQuery ||
		strings.Contains(rawEndpoint, "#") || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return "", "", errors.New("Elasticsearch endpoint must be HTTPS with a host only; userinfo, paths, queries, and fragments are not accepted")
	}
	if err := validateRemoteDestinationHost(endpoint.Hostname()); err != nil {
		return "", "", errors.New("Elasticsearch endpoint must use a non-loopback valid DNS name or IP address")
	}
	if err := validateDestinationPort(endpoint.Port()); err != nil {
		return "", "", fmt.Errorf("Elasticsearch endpoint %w", err)
	}
	endpoint.Scheme = "https"
	endpoint.Path = ""
	endpoint.RawPath = ""
	index := strings.TrimSpace(rawIndex)
	if index == "" {
		index = defaultElasticsearchIndex
	}
	if !elasticsearchIndexPattern.MatchString(index) || strings.Contains(index, "..") {
		return "", "", errors.New("Elasticsearch index must be 1..255 lowercase letters, digits, dot, underscore, or hyphen; consecutive dots are not allowed")
	}
	return endpoint.String(), index, nil
}

func renderElasticsearchReadme(options ElasticsearchForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge Elasticsearch forwarding pack

This pack contains an inert customer-managed Vector configuration for the
HTTPS Elasticsearch origin %s and index %s. It reads only Threadforge runtime
JSONL and exports a newly constructed document containing the event timestamp,
allowlisted severity, and fixed schema/privacy marker. It excludes raw event
content, action names, identifiers, harness/model names, paths, attributes,
inventory, and host-specific values.

## Forwarding is not enabled by this pack

Pack generation writes local files only. It does not install or start Vector,
read credential environment variables, contact Elasticsearch, or upload
telemetry. The file source starts at end-of-file and does not backfill. The
endpoint and index are stored in vector.toml; keep the generated directory
private.

Before starting Vector, place the Elasticsearch API key in the Vector secrets
directory as the single-line file %s. The Threadforge CLI never opens or stores
the key. Configure it with only the privileges needed to write to the selected
index, and set appropriate retention, encryption, and access controls in
Elasticsearch.
Keep Vector's data directory persistent so file offsets survive restarts.

The pack also contains the metadata-only ingest pipeline, mappings, index and
data-stream templates, a 30-day rollover/90-day retention policy for the data
stream, Kibana data views/searches, and a loopback-only Docker Compose test
stack. These assets are inert files; install them through an authorized
Elasticsearch/Kibana administration workflow before starting a shipper. The
included sample-event.jsonl is synthetic and
contains no source event IDs, user or host fields, paths, prompt text, or
credentials. Runtime log path used by vector.toml: %s.
`, options.Endpoint, options.Index, filepath.Join(options.SecretsDir, elasticsearchAPIKeySecretName), options.LogPath) + elasticsearchSyntheticSmokeInstructions() + elasticsearchAssetInstallInstructions() + elasticsearchLocalStackInstructions()
}

func renderElasticsearchAgentReadme(shipper string, options ElasticsearchAgentForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge Elasticsearch %s metadata-only pack

This pack contains an inert local Vector sanitizer and a %s configuration.
Vector reads new Threadforge runtime JSONL from end-of-file, reconstructs a
fresh record containing only timestamp, allowlisted severity, and the fixed
Threadforge privacy marker, then writes it to:

  %s

The %s input reads only that separate metadata file. It never reads the raw
Threadforge trace. Filebeat and standalone Elastic Agent target the
logs-threadforge.endpoint-default data stream. The included
sample-event.jsonl is synthetic and contains no source event or identifying
details. Elasticsearch endpoint: %s.

## Local files; explicit activation

Pack generation does not install or start Vector, %s, or Elasticsearch, edit
host configuration, read credentials, or contact the endpoint. The configs
contain no API key, username, or password. Configure authentication only
through your existing secure deployment mechanism. TLS certificate
verification remains enabled.

Install vector.toml with a customer-managed Vector service and persist its
data directory so file offsets survive restarts. Vector reads the raw local
log but emits only the strict metadata projection. Configure Vector to write
the metadata file and grant the shipper read access to that file only. Review
the destination's access, retention, and encryption controls before starting
either service. Keep this pack private because its configs contain local paths
and the endpoint.

Raw runtime path: %s
Metadata-only output path: %s

The pack includes an ingest pipeline, strict metadata mappings, index and
data-stream templates, an ILM policy for the data stream, minimal Kibana
data views/saved searches, and a loopback-only synthetic Docker Compose test
stack. These files do not configure a cluster until an administrator
explicitly installs or starts them.
`, shipper, shipper, options.MetadataLogPath, shipper, options.Endpoint, shipper, options.LogPath, options.MetadataLogPath) + elasticsearchSyntheticSmokeInstructions() + elasticsearchAssetInstallInstructions() + elasticsearchLocalStackInstructions()
}

func elasticsearchSyntheticSmokeInstructions() string {
	return `
## Explicit synthetic-only customer endpoint check

After an administrator installs the generated data-stream template and ingest
pipeline, these optional scripts can check one customer endpoint. They are
inert until explicitly invoked with --send-synthetic-event or
-SendSyntheticEvent. They require either ELASTIC_API_KEY, or both
ELASTIC_USERNAME and ELASTIC_PASSWORD, supplied by an external secret manager.
The script first verifies that the existing Threadforge data stream is present,
then posts exactly the SHA-256-pinned sample-event.jsonl through the
threadforge-metadata ingest pipeline. It never reads runtime logs or the
Vector secrets file, suppresses response bodies, and does not follow redirects.
The POSIX script passes credentials to curl over configuration stdin; the
PowerShell script reads credentials only into its own process memory. Neither
script writes credentials to disk, and pack generation never makes a request.

PowerShell:

  ./elasticsearch-upload-smoke-test.ps1 -SendSyntheticEvent

POSIX shell:

  sh ./elasticsearch-upload-smoke-test.sh --send-synthetic-event

This sends one new synthetic document to the configured data stream. It does
not install cluster assets or configure a managed shipper. A successful local
fixture test is not customer-cluster authentication or delivery evidence.
`
}
