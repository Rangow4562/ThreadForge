package forwarding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func validSentinelOptions(t *testing.T) SentinelForwardingOptions {
	t.Helper()
	root := t.TempDir()
	return SentinelForwardingOptions{
		LogPath:        filepath.Join(root, "runtime.jsonl"),
		Endpoint:       "https://dce.eastus.ingest.monitor.azure.com",
		DCRImmutableID: "dcr-0123456789abcdef0123456789abcdef",
		TenantID:       "01234567-89ab-cdef-0123-456789abcdef",
		ClientID:       "abcdef01-2345-6789-abcd-ef0123456789",
		SecretsDir:     filepath.Join(root, "secrets"),
	}
}

func TestRenderSentinelVectorConfigIsMetadataOnlyAndUsesExternalSecret(t *testing.T) {
	options := validSentinelOptions(t)
	config, err := RenderSentinelVectorConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Sentinel Vector config is not valid TOML: %v\n%s", err, config)
	}
	source := document["sources"].(map[string]any)["threadforge_runtime"].(map[string]any)
	if source["read_from"] != "end" || source["include"].([]any)[0] != options.LogPath {
		t.Fatalf("Sentinel source must read selected local JSONL from EOF: %#v", source)
	}
	transform := document["transforms"].(map[string]any)["threadforge_sentinel_metadata"].(map[string]any)
	transformVRL := transform["source"].(string)
	for _, required := range []string{"SchemaVersion", "RawContent", "threadforge.metadata-export.v1", "parse_timestamp"} {
		if !strings.Contains(transformVRL, required) {
			t.Fatalf("Sentinel VRL omitted required metadata field %q: %s", required, transformVRL)
		}
	}
	for _, forbidden := range []string{"RawData", "HOSTNAME", "inventory", "prompt", `event.message`} {
		if strings.Contains(transformVRL, forbidden) {
			t.Fatalf("Sentinel VRL references excluded source data %q", forbidden)
		}
	}
	sink := document["sinks"].(map[string]any)["threadforge_sentinel"].(map[string]any)
	if sink["type"] != "azure_logs_ingestion" || sink["endpoint"] != options.Endpoint ||
		sink["dcr_immutable_id"] != options.DCRImmutableID || sink["stream_name"] != sentinelStreamName ||
		sink["timestamp_field"] != "TimeGenerated" {
		t.Fatalf("unexpected Azure Logs Ingestion sink: %#v", sink)
	}
	auth := sink["auth"].(map[string]any)
	if auth["azure_credential_kind"] != "client_secret_credential" ||
		auth["azure_client_secret"] != "SECRET[threadforgesecrets.azureclientsecret]" ||
		auth["azure_tenant_id"] != options.TenantID || auth["azure_client_id"] != options.ClientID {
		t.Fatalf("Azure auth must keep only the client secret external: %#v", auth)
	}
	if document["secret"].(map[string]any)["threadforgesecrets"].(map[string]any)["path"] != options.SecretsDir {
		t.Fatal("generated Vector config omitted selected directory-secrets backend")
	}
	for _, forbidden := range []string{"client-secret-value", "AZURE_CLIENT_SECRET", "RawData", "prompt", "HOSTNAME"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("Sentinel Vector config contains credential value or excluded content %q", forbidden)
		}
	}
}

func TestSentinelDCRAndTableHaveMatchingMetadataOnlyContract(t *testing.T) {
	dcr, err := RenderSentinelDCRTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var template struct {
		Resources []struct {
			Kind       string `json:"kind"`
			Properties struct {
				StreamDeclarations map[string]struct {
					Columns []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"columns"`
				} `json:"streamDeclarations"`
				DataFlows []struct {
					Streams      []string `json:"streams"`
					OutputStream string   `json:"outputStream"`
					TransformKQL string   `json:"transformKql"`
				} `json:"dataFlows"`
			} `json:"properties"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(dcr, &template); err != nil {
		t.Fatalf("generated DCR template is invalid JSON: %v", err)
	}
	if len(template.Resources) != 1 || template.Resources[0].Kind != "Direct" || len(template.Resources[0].Properties.DataFlows) != 1 {
		t.Fatalf("DCR must contain one Direct ingestion flow: %#v", template.Resources)
	}
	properties := template.Resources[0].Properties
	stream, ok := properties.StreamDeclarations[sentinelStreamName]
	if !ok {
		t.Fatalf("DCR does not declare Vector stream %s", sentinelStreamName)
	}
	wantColumns := map[string]string{"TimeGenerated": "datetime", "Severity": "string", "SchemaVersion": "string", "RawContent": "boolean"}
	columns := make(map[string]string)
	for _, column := range stream.Columns {
		columns[column.Name] = column.Type
	}
	if len(columns) != len(wantColumns) {
		t.Fatalf("DCR stream contains unexpected columns: %#v", columns)
	}
	for name, wantType := range wantColumns {
		if columns[name] != wantType {
			t.Fatalf("DCR column %s has type %q, want %q", name, columns[name], wantType)
		}
	}
	flow := properties.DataFlows[0]
	if len(flow.Streams) != 1 || flow.Streams[0] != sentinelStreamName || flow.OutputStream != sentinelOutputStreamName ||
		!strings.Contains(flow.TransformKQL, "RawContent = tobool(RawContent)") {
		t.Fatalf("DCR transform or stream mapping is inconsistent: %#v", flow)
	}
	var table struct {
		Name       string `json:"name"`
		Properties struct {
			Schema struct {
				Name    string `json:"name"`
				Columns []struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"columns"`
			} `json:"schema"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(sentinelTableSchema), &table); err != nil {
		t.Fatalf("table schema is invalid JSON: %v", err)
	}
	if table.Name != sentinelTableName || table.Properties.Schema.Name != sentinelTableName || len(table.Properties.Schema.Columns) != len(wantColumns) {
		t.Fatalf("table schema name/shape does not match DCR output: %#v", table)
	}
	tableColumns := make(map[string]string)
	for _, column := range table.Properties.Schema.Columns {
		tableColumns[column.Name] = column.Type
	}
	for name, wantType := range map[string]string{"TimeGenerated": "dateTime", "Severity": "string", "SchemaVersion": "string", "RawContent": "boolean"} {
		if tableColumns[name] != wantType {
			t.Fatalf("table column %s has type %q, want %q", name, tableColumns[name], wantType)
		}
	}
	if strings.Contains(string(dcr), "RawData") || strings.Contains(string(dcr), "dynamic") || strings.Contains(sentinelTableSchema, "RawData") {
		t.Fatal("Sentinel DCR/table must not retain raw or dynamic event data")
	}
}

func TestSentinelAMADCRReadsOnlySanitizedMetadataFile(t *testing.T) {
	root := t.TempDir()
	metadataPath := filepath.Join(root, "sentinel-metadata.jsonl")
	data, err := RenderSentinelAMADCRTemplate(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var template struct {
		Resources []struct {
			Kind       string `json:"kind"`
			Properties struct {
				StreamDeclarations map[string]struct {
					Columns []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"columns"`
				} `json:"streamDeclarations"`
				DataSources struct {
					LogFiles []struct {
						Streams      []string `json:"streams"`
						FilePatterns []string `json:"filePatterns"`
						Format       string   `json:"format"`
					} `json:"logFiles"`
				} `json:"dataSources"`
				DataFlows []struct {
					Streams      []string `json:"streams"`
					OutputStream string   `json:"outputStream"`
					TransformKQL string   `json:"transformKql"`
				} `json:"dataFlows"`
			} `json:"properties"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(data, &template); err != nil {
		t.Fatalf("generated AMA DCR template is invalid JSON: %v", err)
	}
	if len(template.Resources) != 1 || len(template.Resources[0].Properties.DataSources.LogFiles) != 1 || len(template.Resources[0].Properties.DataFlows) != 1 {
		t.Fatalf("AMA DCR must have one local file source and one flow: %#v", template.Resources)
	}
	fileSource := template.Resources[0].Properties.DataSources.LogFiles[0]
	if fileSource.Format != "json" || len(fileSource.FilePatterns) != 1 || fileSource.FilePatterns[0] != metadataPath {
		t.Fatalf("AMA DCR must tail the selected sanitized JSONL path: %#v", fileSource)
	}
	declared, ok := template.Resources[0].Properties.StreamDeclarations[sentinelAMAMetadataStreamName]
	if !ok {
		t.Fatalf("AMA DCR omitted JSON stream declaration %q", sentinelAMAMetadataStreamName)
	}
	wantColumns := map[string]string{"TimeGenerated": "datetime", "Severity": "string", "SchemaVersion": "string", "RawContent": "boolean"}
	columns := make(map[string]string)
	for _, column := range declared.Columns {
		columns[column.Name] = column.Type
	}
	if len(columns) != len(wantColumns) {
		t.Fatalf("AMA JSON stream columns = %#v, want exactly %#v", columns, wantColumns)
	}
	for name, wantType := range wantColumns {
		if columns[name] != wantType {
			t.Fatalf("AMA JSON stream column %s = %q, want %q", name, columns[name], wantType)
		}
	}
	flow := template.Resources[0].Properties.DataFlows[0]
	if len(flow.Streams) != 1 || flow.Streams[0] != sentinelAMAMetadataStreamName || flow.OutputStream != sentinelOutputStreamName || flow.TransformKQL != "source" {
		t.Fatalf("AMA DCR stream/table mapping is inconsistent: %#v", flow)
	}
	for _, forbidden := range []string{"RawData", "dynamic", "Metadata.event", "Metadata.prompt"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("AMA DCR must consume flat allowlisted JSON, found %q", forbidden)
		}
	}
}

func TestSentinelLocalVectorConfigSanitizesBeforeAMAFile(t *testing.T) {
	root := t.TempDir()
	options := SentinelForwardingOptions{
		Path:            sentinelPathAMA,
		LogPath:         filepath.Join(root, "runtime.jsonl"),
		MetadataLogPath: filepath.Join(root, "sentinel-metadata.jsonl"),
	}
	config, err := RenderSentinelLocalVectorConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Sentinel local Vector config is invalid TOML: %v\n%s", err, config)
	}
	source := document["sources"].(map[string]any)["threadforge_runtime"].(map[string]any)
	if source["read_from"] != "end" || source["include"].([]any)[0] != options.LogPath {
		t.Fatalf("local sanitizer must read only new runtime events: %#v", source)
	}
	transform := document["transforms"].(map[string]any)["threadforge_runtime_metadata"].(map[string]any)["source"].(string)
	for _, required := range []string{"TimeGenerated", "Severity", "SchemaVersion", "RawContent", `"RawContent": false`} {
		if !strings.Contains(transform, required) {
			t.Fatalf("AMA local sanitizer omitted flattened metadata field %q: %s", required, transform)
		}
	}
	for _, forbidden := range []string{"event.prompt", "threadforge_export", "raw_content", "RawData"} {
		if strings.Contains(transform, forbidden) {
			t.Fatalf("AMA local sanitizer contains nested or raw field %q", forbidden)
		}
	}
	sink := document["sinks"].(map[string]any)["threadforge_sentinel_metadata_file"].(map[string]any)
	if sink["type"] != "file" || sink["path"] != options.MetadataLogPath {
		t.Fatalf("Sentinel local sanitizer must write the selected metadata file: %#v", sink)
	}
	if strings.Contains(config, "azure_logs_ingestion") || strings.Contains(config, "SECRET[") {
		t.Fatal("AMA local sanitizer config must not contain a cloud sink or credentials")
	}
}

func TestSentinelSyntheticSampleIsMetadataOnly(t *testing.T) {
	sample, err := RenderSentinelSample()
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytesTrimNewline(sample), &event); err != nil {
		t.Fatalf("synthetic Sentinel sample is invalid JSON: %v", err)
	}
	if len(event) != 3 || event["severity"] != "info" {
		t.Fatalf("sample contains fields outside the shared metadata export: %#v", event)
	}
	if _, err := time.Parse(time.RFC3339Nano, event["timestamp"].(string)); err != nil {
		t.Fatalf("sample timestamp is invalid: %v", err)
	}
	marker := event["threadforge_export"].(map[string]any)
	if len(marker) != 2 || marker["schema_version"] != "threadforge.metadata-export.v1" || marker["raw_content"] != false {
		t.Fatalf("sample privacy marker is invalid: %#v", marker)
	}
}

func TestInstallSentinelPackIsLocalPrivateAndNoOverwrite(t *testing.T) {
	options := validSentinelOptions(t)
	root := filepath.Dir(options.LogPath)
	output := filepath.Join(root, "sentinel-pack")
	installed, err := InstallSentinelPack(output, options)
	if err != nil {
		t.Fatal(err)
	}
	if installed != output {
		t.Fatalf("pack path = %q, want %q", installed, output)
	}
	for _, name := range []string{"README.md", "dcr-logs-ingestion-template.json", "dcr-transform.kql", "table-schema.json", "vector.toml", "queries.kql", "detections.kql", "sample-event.jsonl"} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil {
			t.Fatalf("missing Sentinel pack file %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 && os.PathSeparator != '\\' {
			t.Fatalf("pack file %s permissions = %o, want 600", name, info.Mode().Perm())
		}
	}
	readme, err := os.ReadFile(filepath.Join(output, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"does not install/start Vector", "RawContent=false", "azureclientsecret", options.Endpoint, options.DCRImmutableID} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("Sentinel README omits required privacy/setup detail %q", phrase)
		}
	}
	if _, err := InstallSentinelPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing output directory should be refused, got %v", err)
	}
}

func TestInstallSentinelAMAPackContainsOnlyLocalSanitizerAndAMADCR(t *testing.T) {
	root := t.TempDir()
	options := SentinelForwardingOptions{
		Path:            sentinelPathAMA,
		LogPath:         filepath.Join(root, "runtime.jsonl"),
		MetadataLogPath: filepath.Join(root, "sentinel-metadata.jsonl"),
	}
	output := filepath.Join(root, "sentinel-ama-pack")
	if _, err := InstallSentinelPack(output, options); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "dcr-template.json", "dcr-ama-transform.kql", "table-schema.json", "vector-local.toml", "queries.kql", "detections.kql", "sample-event.jsonl"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing AMA pack asset %s: %v", name, err)
		}
	}
	for _, absent := range []string{"vector.toml", "dcr-logs-ingestion-template.json", "dcr-transform.kql"} {
		if _, err := os.Stat(filepath.Join(output, absent)); !os.IsNotExist(err) {
			t.Fatalf("AMA-only pack unexpectedly includes direct API asset %s: %v", absent, err)
		}
	}
}

func TestInstallSentinelBothPathsIncludesBothIndependentPipelines(t *testing.T) {
	options := validSentinelOptions(t)
	options.Path = sentinelPathBoth
	options.MetadataLogPath = filepath.Join(filepath.Dir(options.LogPath), "sentinel-metadata.jsonl")
	output := filepath.Join(filepath.Dir(options.LogPath), "sentinel-both-pack")
	if _, err := InstallSentinelPack(output, options); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vector.toml", "dcr-logs-ingestion-template.json", "vector-local.toml", "dcr-template.json"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("both-path pack is missing %s: %v", name, err)
		}
	}
}

func TestRenderSentinelRejectsInvalidEndpointAndIdentifiers(t *testing.T) {
	base := validSentinelOptions(t)
	bad := map[string]SentinelForwardingOptions{
		"http":       changeSentinel(base, func(o *SentinelForwardingOptions) { o.Endpoint = "http://dce.eastus.ingest.monitor.azure.com" }),
		"wrong-host": changeSentinel(base, func(o *SentinelForwardingOptions) { o.Endpoint = "https://example.com" }),
		"path":       changeSentinel(base, func(o *SentinelForwardingOptions) { o.Endpoint += "/path" }),
		"query":      changeSentinel(base, func(o *SentinelForwardingOptions) { o.Endpoint += "?token=value" }),
		"userinfo": changeSentinel(base, func(o *SentinelForwardingOptions) {
			o.Endpoint = "https://user:secret@dce.eastus.ingest.monitor.azure.com"
		}),
		"port":                    changeSentinel(base, func(o *SentinelForwardingOptions) { o.Endpoint = "https://dce.eastus.ingest.monitor.azure.com:443" }),
		"bad-dcr":                 changeSentinel(base, func(o *SentinelForwardingOptions) { o.DCRImmutableID = "dcr-not-a-guid" }),
		"bad-tenant":              changeSentinel(base, func(o *SentinelForwardingOptions) { o.TenantID = "tenant" }),
		"bad-client":              changeSentinel(base, func(o *SentinelForwardingOptions) { o.ClientID = "client" }),
		"relative-log":            changeSentinel(base, func(o *SentinelForwardingOptions) { o.LogPath = "runtime.jsonl" }),
		"secret-dir-control-char": changeSentinel(base, func(o *SentinelForwardingOptions) { o.SecretsDir += "\nchild" }),
	}
	for name, options := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := RenderSentinelVectorConfig(options); err == nil {
				t.Fatal("invalid Sentinel config values were accepted")
			}
		})
	}
}

func TestNormalizeSentinelPathRequiresOnlyItsOwnCredentialsAndOutputs(t *testing.T) {
	root := t.TempDir()
	base := SentinelForwardingOptions{Path: sentinelPathAMA, LogPath: filepath.Join(root, "runtime.jsonl"), MetadataLogPath: filepath.Join(root, "metadata.jsonl")}
	if _, err := RenderSentinelLocalVectorConfig(base); err != nil {
		t.Fatalf("AMA path should not require Azure app IDs or client secrets: %v", err)
	}
	for name, options := range map[string]SentinelForwardingOptions{
		"ama-missing-metadata-path":   {Path: sentinelPathAMA, LogPath: base.LogPath},
		"ama-same-source-and-output":  {Path: sentinelPathAMA, LogPath: base.LogPath, MetadataLogPath: base.LogPath},
		"ama-unused-cloud-values":     {Path: sentinelPathAMA, LogPath: base.LogPath, MetadataLogPath: base.MetadataLogPath, Endpoint: "https://dce.eastus.ingest.monitor.azure.com"},
		"vector-unused-metadata-path": {Path: sentinelPathVector, LogPath: base.LogPath, MetadataLogPath: base.MetadataLogPath, Endpoint: "https://dce.eastus.ingest.monitor.azure.com", DCRImmutableID: "dcr-0123456789abcdef0123456789abcdef", TenantID: "01234567-89ab-cdef-0123-456789abcdef", ClientID: "abcdef01-2345-6789-abcd-ef0123456789"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeSentinelOptions(options); err == nil {
				t.Fatal("invalid or unused mode-specific values were accepted")
			}
		})
	}
}

func changeSentinel(options SentinelForwardingOptions, change func(*SentinelForwardingOptions)) SentinelForwardingOptions {
	change(&options)
	return options
}
