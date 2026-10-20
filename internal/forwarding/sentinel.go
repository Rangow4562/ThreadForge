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
)

const (
	sentinelClientSecretName = "azureclientsecret"
	sentinelStreamName       = "Custom-ThreadforgeMetadata"
	sentinelOutputStreamName = "Custom-ThreadforgeAgentMetadata_CL"
	sentinelTableName        = "ThreadforgeAgentMetadata_CL"
	sentinelPathVector       = "vector"
	sentinelPathAMA          = "ama"
	sentinelPathBoth         = "both"
)

var (
	sentinelDCRIDPattern = regexp.MustCompile(`^dcr-[0-9a-fA-F]{32}$`)
	sentinelUUIDPattern  = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

const sentinelDCRTransform = `source
| project
    TimeGenerated = todatetime(TimeGenerated),
    Severity = tostring(Severity),
    SchemaVersion = tostring(SchemaVersion),
    RawContent = tobool(RawContent)
`

const sentinelAMAMetadataStreamName = "Custom-ThreadforgeAMAMetadata"

const sentinelAMAMetadataProjectionVRL = `event = parse_json!(.message)
severity = if event.severity == "info" { "info" } else if event.severity == "low" { "low" } else if event.severity == "medium" { "medium" } else if event.severity == "high" { "high" } else if event.severity == "critical" { "critical" } else { "unknown" }
event_time = parse_timestamp(event.timestamp, format: "%+") ?? now()
. = {
  "TimeGenerated": event_time,
  "Severity": severity,
  "SchemaVersion": "threadforge.metadata-export.v1",
  "RawContent": false
}`

const sentinelAMADCRTransform = `source`

const sentinelAMADCRTemplate = `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "dataCollectionRuleName": { "type": "string", "defaultValue": "dcr-threadforge-metadata-ama" },
    "workspaceResourceId": {
      "type": "string",
      "metadata": { "description": "Resource ID of the Log Analytics workspace enabled for Microsoft Sentinel." }
    },
    "location": { "type": "string", "defaultValue": "[resourceGroup().location]" }
  },
  "resources": [
    {
      "type": "Microsoft.Insights/dataCollectionRules",
      "apiVersion": "2023-03-11",
      "name": "[parameters('dataCollectionRuleName')]",
      "location": "[parameters('location')]",
      "properties": {
        "streamDeclarations": {
          "Custom-ThreadforgeAMAMetadata": {
            "columns": [
              { "name": "TimeGenerated", "type": "datetime" },
              { "name": "Severity", "type": "string" },
              { "name": "SchemaVersion", "type": "string" },
              { "name": "RawContent", "type": "boolean" }
            ]
          }
        },
        "dataSources": {
          "logFiles": [
            {
              "streams": ["Custom-ThreadforgeAMAMetadata"],
              "filePatterns": [{{LOG_PATH_JSON}}],
              "format": "json",
              "name": "threadforgeSanitizedMetadataJsonl"
            }
          ]
        },
        "destinations": {
          "logAnalytics": [
            { "workspaceResourceId": "[parameters('workspaceResourceId')]", "name": "threadforgeWorkspace" }
          ]
        },
        "dataFlows": [
          {
            "streams": ["Custom-ThreadforgeAMAMetadata"],
            "destinations": ["threadforgeWorkspace"],
            "transformKql": {{DCR_TRANSFORM_JSON}},
            "outputStream": "Custom-ThreadforgeAgentMetadata_CL"
          }
        ]
      }
    }
  ]
}
`

const sentinelDCRTemplate = `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "dataCollectionRuleName": { "type": "string", "defaultValue": "dcr-threadforge-metadata-ingestion" },
    "workspaceResourceId": {
      "type": "string",
      "metadata": { "description": "Resource ID of the Log Analytics workspace enabled for Microsoft Sentinel." }
    },
    "dataCollectionEndpointResourceId": {
      "type": "string",
      "metadata": { "description": "Resource ID of a Data Collection Endpoint in the workspace region." }
    },
    "location": { "type": "string", "defaultValue": "[resourceGroup().location]" }
  },
  "resources": [
    {
      "type": "Microsoft.Insights/dataCollectionRules",
      "apiVersion": "2023-03-11",
      "name": "[parameters('dataCollectionRuleName')]",
      "location": "[parameters('location')]",
      "kind": "Direct",
      "properties": {
        "dataCollectionEndpointId": "[parameters('dataCollectionEndpointResourceId')]",
        "streamDeclarations": {
          "Custom-ThreadforgeMetadata": {
            "columns": [
              { "name": "TimeGenerated", "type": "datetime" },
              { "name": "Severity", "type": "string" },
              { "name": "SchemaVersion", "type": "string" },
              { "name": "RawContent", "type": "boolean" }
            ]
          }
        },
        "destinations": {
          "logAnalytics": [
            { "workspaceResourceId": "[parameters('workspaceResourceId')]", "name": "threadforgeWorkspace" }
          ]
        },
        "dataFlows": [
          {
            "streams": ["Custom-ThreadforgeMetadata"],
            "destinations": ["threadforgeWorkspace"],
            "transformKql": {{DCR_TRANSFORM_JSON}},
            "outputStream": "Custom-ThreadforgeAgentMetadata_CL"
          }
        ]
      }
    }
  ],
  "outputs": {
    "dcrImmutableId": {
      "type": "string",
      "value": "[reference(resourceId('Microsoft.Insights/dataCollectionRules', parameters('dataCollectionRuleName'))).immutableId]"
    }
  }
}
`

const sentinelTableSchema = `{
  "name": "ThreadforgeAgentMetadata_CL",
  "properties": {
    "schema": {
      "name": "ThreadforgeAgentMetadata_CL",
      "columns": [
        { "name": "TimeGenerated", "type": "dateTime" },
        { "name": "Severity", "type": "string" },
        { "name": "SchemaVersion", "type": "string" },
        { "name": "RawContent", "type": "boolean" }
      ]
    }
  }
}
`

const sentinelQueriesKQL = `// Recent Threadforge metadata-only events.
ThreadforgeAgentMetadata_CL
| where TimeGenerated > ago(24h)
| project TimeGenerated, Severity, SchemaVersion, RawContent
| order by TimeGenerated desc
| take 100

// Event volume by exported severity. This is telemetry, not an incident verdict.
ThreadforgeAgentMetadata_CL
| where TimeGenerated > ago(24h)
| summarize Events = count() by Severity
| order by Events desc
`

const sentinelDetectionsKQL = `// Review higher source-severity labels; this query does not independently detect threats.
ThreadforgeAgentMetadata_CL
| where TimeGenerated > ago(1h)
| where Severity in ("high", "critical")
| project TimeGenerated, Severity, SchemaVersion
| order by TimeGenerated desc
`

// SentinelForwardingOptions configures an inert customer-managed Azure Logs
// Ingestion API pack. Azure client secrets are never accepted by Threadforge.
type SentinelForwardingOptions struct {
	Path            string
	LogPath         string
	MetadataLogPath string
	Endpoint        string
	DCRImmutableID  string
	TenantID        string
	ClientID        string
	SecretsDir      string
}

// RenderSentinelVectorConfig creates a metadata-only Azure Logs Ingestion sink.
func RenderSentinelVectorConfig(options SentinelForwardingOptions) (string, error) {
	normalized, err := normalizeSentinelOptions(options)
	if err != nil {
		return "", err
	}
	if normalized.Path == sentinelPathAMA {
		return "", errors.New("Sentinel path ama does not create a cloud Vector sink; use RenderSentinelLocalVectorConfig")
	}
	return fmt.Sprintf(`# Threadforge local JSONL -> Microsoft Sentinel Logs Ingestion API.
# Inert until you configure Vector and start it yourself.

%s
[sources.threadforge_runtime]
type = "file"
include = [%s]
read_from = "end"

[transforms.threadforge_sentinel_metadata]
type = "remap"
inputs = ["threadforge_runtime"]
source = '''
event = parse_json!(.message)
severity = if event.severity == "info" { "info" } else if event.severity == "low" { "low" } else if event.severity == "medium" { "medium" } else if event.severity == "high" { "high" } else if event.severity == "critical" { "critical" } else { "unknown" }
event_time = parse_timestamp(event.timestamp, format: "%%+") ?? now()
. = {
  "Severity": severity,
  "SchemaVersion": "threadforge.metadata-export.v1",
  "RawContent": false
}
.timestamp = event_time
'''

[sinks.threadforge_sentinel]
type = "azure_logs_ingestion"
inputs = ["threadforge_sentinel_metadata"]
endpoint = %s
dcr_immutable_id = %s
stream_name = %s
timestamp_field = "TimeGenerated"

[sinks.threadforge_sentinel.auth]
azure_credential_kind = "client_secret_credential"
azure_tenant_id = %s
azure_client_id = %s
azure_client_secret = "%s"

[sinks.threadforge_sentinel.batch]
max_bytes = 524288
max_events = 1000
timeout_secs = 30

[sinks.threadforge_sentinel.request]
retry_attempts = 10
retry_initial_backoff_secs = 1
retry_max_duration_secs = 300
`, renderVectorSecretDirectory(normalized.SecretsDir), strconv.Quote(normalized.LogPath), strconv.Quote(normalized.Endpoint),
		strconv.Quote(normalized.DCRImmutableID), strconv.Quote(sentinelStreamName), strconv.Quote(normalized.TenantID),
		strconv.Quote(normalized.ClientID), vectorSecretReference(sentinelClientSecretName)), nil
}

// RenderSentinelLocalVectorConfig writes only the local sanitized metadata
// file consumed by Azure Monitor Agent. It performs no network forwarding.
func RenderSentinelLocalVectorConfig(options SentinelForwardingOptions) (string, error) {
	normalized, err := normalizeSentinelOptions(options)
	if err != nil {
		return "", err
	}
	if normalized.Path != sentinelPathAMA && normalized.Path != sentinelPathBoth {
		return "", errors.New("Sentinel local Vector config requires --path ama or --path both")
	}
	return renderLocalMetadataVectorConfig(normalized.LogPath, normalized.MetadataLogPath, "threadforge_sentinel_metadata_file", sentinelAMAMetadataProjectionVRL), nil
}

// RenderSentinelDCRTransform returns the strict pass-through DCR projection.
func RenderSentinelDCRTransform() string { return sentinelDCRTransform }

// RenderSentinelDCRTemplate returns an ARM template for a Direct DCR that
// accepts only the four metadata fields and writes them to the custom table.
func RenderSentinelDCRTemplate() ([]byte, error) {
	transform, err := json.Marshal(strings.TrimSpace(sentinelDCRTransform))
	if err != nil {
		return nil, fmt.Errorf("encode Sentinel DCR transform: %w", err)
	}
	const marker = "{{DCR_TRANSFORM_JSON}}"
	if strings.Count(sentinelDCRTemplate, marker) != 1 {
		return nil, errors.New("Sentinel DCR template must contain exactly one transform marker")
	}
	rendered := strings.Replace(sentinelDCRTemplate, marker, string(transform), 1)
	if !json.Valid([]byte(rendered)) {
		return nil, errors.New("generated Sentinel DCR template is invalid JSON")
	}
	return []byte(rendered), nil
}

// RenderSentinelAMADCRTemplate creates the local-file DCR for an AMA deployment.
// Its source is a separate sanitized metadata JSONL file, never the raw trace.
func RenderSentinelAMADCRTemplate(metadataLogPath string) ([]byte, error) {
	metadataLogPath, err := normalizeWazuhMetadataLogPath(metadataLogPath)
	if err != nil {
		return nil, err
	}
	if err := validateRealDirectoryChain(filepath.Dir(metadataLogPath)); err != nil {
		return nil, fmt.Errorf("validate Sentinel metadata output directory: %w", err)
	}
	pathJSON, err := json.Marshal(metadataLogPath)
	if err != nil {
		return nil, fmt.Errorf("encode Sentinel metadata log path: %w", err)
	}
	transformJSON, err := json.Marshal(strings.TrimSpace(sentinelAMADCRTransform))
	if err != nil {
		return nil, fmt.Errorf("encode Sentinel AMA DCR transform: %w", err)
	}
	rendered := sentinelAMADCRTemplate
	for marker, value := range map[string]string{
		"{{LOG_PATH_JSON}}":      string(pathJSON),
		"{{DCR_TRANSFORM_JSON}}": string(transformJSON),
	} {
		if strings.Count(rendered, marker) != 1 {
			return nil, fmt.Errorf("Sentinel AMA DCR template must contain exactly one %s marker", marker)
		}
		rendered = strings.Replace(rendered, marker, value, 1)
	}
	if !json.Valid([]byte(rendered)) {
		return nil, errors.New("generated Sentinel AMA DCR template is invalid JSON")
	}
	return []byte(rendered), nil
}

// RenderSentinelSample returns a synthetic metadata-only event.
func RenderSentinelSample() ([]byte, error) { return renderSyntheticMetadataExport() }

// InstallSentinelPack writes local pack artifacts only; it never creates Azure
// resources, reads secrets, starts Vector, or sends telemetry.
func InstallSentinelPack(outputDir string, options SentinelForwardingOptions) (string, error) {
	normalized, err := normalizeSentinelOptions(options)
	if err != nil {
		return "", err
	}
	sample, err := RenderSentinelSample()
	if err != nil {
		return "", err
	}
	files := []privatePackFile{{name: "README.md", content: []byte(renderSentinelReadme(normalized))}}
	if normalized.Path == sentinelPathVector || normalized.Path == sentinelPathBoth {
		vectorConfig, err := RenderSentinelVectorConfig(normalized)
		if err != nil {
			return "", err
		}
		dcr, err := RenderSentinelDCRTemplate()
		if err != nil {
			return "", err
		}
		files = append(files,
			privatePackFile{name: "dcr-logs-ingestion-template.json", content: dcr},
			privatePackFile{name: "dcr-transform.kql", content: []byte(sentinelDCRTransform)},
			privatePackFile{name: "vector.toml", content: []byte(vectorConfig)},
		)
	}
	if normalized.Path == sentinelPathAMA || normalized.Path == sentinelPathBoth {
		localVectorConfig, err := RenderSentinelLocalVectorConfig(normalized)
		if err != nil {
			return "", err
		}
		amaDCR, err := RenderSentinelAMADCRTemplate(normalized.MetadataLogPath)
		if err != nil {
			return "", err
		}
		files = append(files,
			privatePackFile{name: "dcr-template.json", content: amaDCR},
			privatePackFile{name: "dcr-ama-transform.kql", content: []byte(sentinelAMADCRTransform)},
			privatePackFile{name: "vector-local.toml", content: []byte(localVectorConfig)},
		)
	}
	files = append(files,
		privatePackFile{name: "table-schema.json", content: []byte(sentinelTableSchema)},
		privatePackFile{name: "queries.kql", content: []byte(sentinelQueriesKQL)},
		privatePackFile{name: "detections.kql", content: []byte(sentinelDetectionsKQL)},
		privatePackFile{name: "sample-event.jsonl", content: sample},
	)
	return writePrivatePack(outputDir, files)
}

func normalizeSentinelOptions(options SentinelForwardingOptions) (SentinelForwardingOptions, error) {
	logPath, err := normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return SentinelForwardingOptions{}, err
	}
	options.Path = strings.ToLower(strings.TrimSpace(options.Path))
	if options.Path == "" {
		options.Path = sentinelPathVector
	}
	if options.Path != sentinelPathVector && options.Path != sentinelPathAMA && options.Path != sentinelPathBoth {
		return SentinelForwardingOptions{}, errors.New("Sentinel path must be vector, ama, or both")
	}
	options.LogPath = logPath
	needsAMA := options.Path == sentinelPathAMA || options.Path == sentinelPathBoth
	needsVector := options.Path == sentinelPathVector || options.Path == sentinelPathBoth
	if needsAMA {
		metadataPath, err := normalizeWazuhMetadataLogPath(options.MetadataLogPath)
		if err != nil {
			return SentinelForwardingOptions{}, fmt.Errorf("Sentinel Azure Monitor Agent path: %w", err)
		}
		if sameFilesystemPath(logPath, metadataPath) {
			return SentinelForwardingOptions{}, errors.New("Sentinel sanitized metadata path must differ from the raw Threadforge runtime log")
		}
		if err := validateRealDirectoryChain(filepath.Dir(metadataPath)); err != nil {
			return SentinelForwardingOptions{}, fmt.Errorf("validate Sentinel metadata output directory: %w", err)
		}
		options.MetadataLogPath = metadataPath
	} else if strings.TrimSpace(options.MetadataLogPath) != "" {
		return SentinelForwardingOptions{}, errors.New("--metadata-log-path is only used with Sentinel path ama or both")
	}
	if needsVector {
		endpointValue := strings.TrimSpace(options.Endpoint)
		if endpointValue == "" || len(endpointValue) > 2048 || strings.ContainsAny(endpointValue, "\x00\r\n") {
			return SentinelForwardingOptions{}, errors.New("Sentinel vector path requires a bounded HTTPS Azure Monitor ingestion endpoint")
		}
		endpoint, err := url.Parse(endpointValue)
		if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" || endpoint.User != nil ||
			endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.ForceQuery || strings.Contains(endpointValue, "#") ||
			endpoint.Fragment != "" || endpoint.Opaque != "" || endpoint.Port() != "" {
			return SentinelForwardingOptions{}, errors.New("Sentinel endpoint must be an HTTPS ingestion hostname without userinfo, port, path, query, or fragment")
		}
		host := strings.ToLower(endpoint.Hostname())
		if !strings.HasSuffix(host, ".ingest.monitor.azure.com") || strings.TrimSuffix(host, ".ingest.monitor.azure.com") == "" {
			return SentinelForwardingOptions{}, errors.New("Sentinel endpoint must be a DCE or DCR ingestion hostname under .ingest.monitor.azure.com")
		}
		if err := validateRemoteDestinationHost(host); err != nil {
			return SentinelForwardingOptions{}, errors.New("Sentinel endpoint must use a non-loopback valid Azure ingestion hostname")
		}
		endpoint.Scheme = "https"
		endpoint.Host = host
		options.Endpoint = endpoint.String()
		options.DCRImmutableID = strings.TrimSpace(options.DCRImmutableID)
		if !sentinelDCRIDPattern.MatchString(options.DCRImmutableID) {
			return SentinelForwardingOptions{}, errors.New("Sentinel DCR immutable ID must be dcr- followed by 32 hexadecimal characters")
		}
		options.TenantID = strings.TrimSpace(options.TenantID)
		if !sentinelUUIDPattern.MatchString(options.TenantID) {
			return SentinelForwardingOptions{}, errors.New("Sentinel tenant ID must be a UUID")
		}
		options.ClientID = strings.TrimSpace(options.ClientID)
		if !sentinelUUIDPattern.MatchString(options.ClientID) {
			return SentinelForwardingOptions{}, errors.New("Sentinel client ID must be a UUID")
		}
		options.SecretsDir, err = normalizeVectorSecretsDir(options.SecretsDir)
		if err != nil {
			return SentinelForwardingOptions{}, err
		}
	} else if strings.TrimSpace(options.Endpoint)+strings.TrimSpace(options.DCRImmutableID)+strings.TrimSpace(options.TenantID)+strings.TrimSpace(options.ClientID)+strings.TrimSpace(options.SecretsDir) != "" {
		return SentinelForwardingOptions{}, errors.New("Azure Logs Ingestion endpoint, DCR ID, app IDs, and secrets directory are only used with Sentinel path vector or both")
	}
	return options, nil
}

func renderSentinelReadme(options SentinelForwardingOptions) string {
	readme := `# Threadforge metadata-only Microsoft Sentinel pack

This pack contains inert customer-managed Vector/Azure Monitor Agent and Data
Collection Rule (DCR) assets, a four-column table schema, KQL examples, and one
synthetic sample. It does not install/start Vector or AMA, deploy Azure
resources, read credentials, or contact Microsoft services.

## Privacy and input contract

Every path applies the same allowlist before data leaves the local runtime
log: TimeGenerated, allowlisted Severity,
SchemaVersion=threadforge.metadata-export.v1, and RawContent=false. Prompts,
tool inputs/results, command output, paths, host/user/session identity, event
names, attributes, inventory, and the original raw event are excluded. The
generated table has no RawData or dynamic columns. Raw input path: %s.

The pack's forwarding path is: %s

## Azure setup

Create the ThreadforgeAgentMetadata_CL table using table-schema.json, then
deploy the DCR template for the selected path with your workspace resources.
Apply organization-specific retention, access, and analytic rules before
activation. The KQL examples consume only metadata. The high/critical query is
a review aid, not a threat detection or authorization decision. Remote export
requires explicit --allow-remote-metadata-forwarding consent at pack generation.
`
	var forwardingPath string
	switch options.Path {
	case sentinelPathAMA:
		forwardingPath = fmt.Sprintf(`Azure Monitor Agent path: run vector-local.toml to write flat four-column JSONL to %s, configure AMA Custom JSON Logs to tail only that file, and deploy dcr-template.json. Set filesystem access so Vector can write the metadata file and AMA can read it. The config contains no Azure application credentials.`, options.MetadataLogPath)
	case sentinelPathBoth:
		forwardingPath = fmt.Sprintf(`Both paths are included as alternatives. Do not activate both for the same source events unless duplicate ingestion is intended. For Azure Monitor Agent, run vector-local.toml to write sanitized JSONL to %s; AMA and dcr-template.json read only that output. For direct Vector ingestion, run vector.toml with dcr-logs-ingestion-template.json. The selected source path is %s and endpoint is %s (DCR ID %s). The client secret is external in the Vector directory-secrets backend file %s; Threadforge never accepts or reads it.`, options.MetadataLogPath, options.LogPath, options.Endpoint, options.DCRImmutableID, filepath.Join(options.SecretsDir, sentinelClientSecretName))
	default:
		forwardingPath = fmt.Sprintf(`Direct Logs Ingestion API path: deploy dcr-logs-ingestion-template.json with your workspace and Data Collection Endpoint resource IDs, then grant the app Monitoring Metrics Publisher on the DCR. Run vector.toml with persistent Vector state and outbound HTTPS to login.microsoftonline.com and %s. The DCR ID is %s. The client secret is external in the Vector directory-secrets backend file %s; Threadforge never accepts or reads it.`, options.Endpoint, options.DCRImmutableID, filepath.Join(options.SecretsDir, sentinelClientSecretName))
	}
	return fmt.Sprintf(readme, options.LogPath, forwardingPath)
}
