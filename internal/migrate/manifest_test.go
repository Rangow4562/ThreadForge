package migrate

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func createBeaconLearningDatabase(t *testing.T, path string, version int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"PRAGMA user_version = " + strconv.Itoa(version),
		`CREATE TABLE evaluations (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, trace_id TEXT NOT NULL, status TEXT NOT NULL,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, rubric_hash TEXT NOT NULL, evaluator TEXT NOT NULL,
			evaluation_json TEXT NOT NULL)`,
		`CREATE TABLE candidates (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, state TEXT NOT NULL, kind TEXT NOT NULL, title TEXT NOT NULL,
			source_evaluation_id TEXT, memory_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			candidate_json TEXT NOT NULL)`,
		`CREATE TABLE memories (
			id TEXT PRIMARY KEY, candidate_id TEXT NOT NULL, project_id TEXT NOT NULL, kind TEXT NOT NULL, title TEXT NOT NULL,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, superseded_by TEXT, memory_json TEXT NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBeaconSnapshotSanitizesAndReconcilesWithCredentialExclusion(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".beacon", "endpoint", "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	good := beacon.NewEvent(beacon.NewEventOptions{Action: "session.end", Harness: beacon.HarnessInfo{Name: "claude"}, Message: "session finished"})
	unsafe := beacon.NewEvent(beacon.NewEventOptions{Action: "tool.invoked", Harness: beacon.HarnessInfo{Name: "claude"}})
	unsafe.Raw = map[string]interface{}{"api_key": "do-not-copy-this-credential"}
	var log bytes.Buffer
	encoder := json.NewEncoder(&log)
	if err := encoder.Encode(good); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(unsafe); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "runtime.jsonl"), log.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 2 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsCredentialBlocked != 1 {
		t.Fatalf("inspection counts do not reconcile: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), manifest.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Scope != "local_pending_review" {
		t.Fatalf("snapshot records = %#v", records)
	}
	if strings.Contains(string(records[0].Payload), "do-not-copy-this-credential") {
		t.Fatal("snapshot included a credential-bearing event")
	}
	second, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil || second.ID != snapshot.ID {
		t.Fatalf("idempotent snapshot = %#v err=%v", second, err)
	}
}

func TestBeaconEndpointConfigImportsOnlySanitizedProjection(t *testing.T) {
	root := t.TempDir()
	endpoint := filepath.Join(root, ".beacon", "endpoint")
	if err := os.MkdirAll(endpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"user_mode":true,"log_path":"C:\\Users\\private\\endpoint.log","collector":{"binary_path":"C:\\private\\otelcol.exe","config_path":"C:\\private\\otelcol.yaml","spool_path":"C:\\private\\spool","grpc_port":4317,"http_port":4318,"health_port":13133,"include_codex_spans":true},"harnesses":["claude-code","codex-cli"],"destinations":{"splunk_hec":{"enabled":true,"endpoint":"https://private.example/services/collector","token":"do-not-copy-token","index":"agent-prod","source":"threadforge","sourcetype":"agent-event","ca_file":"C:\\private\\ca.pem"}}}`
	if err := os.WriteFile(filepath.Join(endpoint, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	collector := `receivers:
  otlp:
    protocols:
      http: {}
processors:
  batch: {}
exporters:
  otlphttp:
    endpoint: https://private.example/v1/traces
    headers:
      Authorization: Bearer do-not-copy-exporter-secret
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlphttp]
`
	if err := os.WriteFile(filepath.Join(endpoint, "otelcol.yaml"), []byte(collector), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 2 || len(manifest.Files) != 2 {
		t.Fatalf("Beacon config files were not imported: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("sanitized Beacon configuration records = %d, want 2", len(records))
	}
	for _, record := range records {
		if record.Type != "endpoint_configuration" || record.Scope != "local_pending_review" {
			t.Fatalf("unexpected Beacon config record contract: %#v", record)
		}
		payload := string(record.Payload)
		for _, forbidden := range []string{"private.example", "do-not-copy-token", "do-not-copy-exporter-secret", `C:\\private`, "agent-prod", "Authorization"} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("sanitized Beacon config retained %q: %s", forbidden, payload)
			}
		}
		if !strings.Contains(payload, `"sensitive_values_omitted":true`) {
			t.Fatalf("config projection did not disclose omitted sensitive values: %s", payload)
		}
	}
	if !strings.Contains(string(records[0].Payload)+string(records[1].Payload), `"endpoint_configured":true`) {
		t.Fatal("projection did not retain endpoint-configuration presence")
	}
}

func TestBeaconConfigAllowlistRejectsUnrelatedJSONConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"user_mode":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 0 || manifest.Summary.FilesScanned != 0 {
		t.Fatalf("unrelated config.json was accepted as a Beacon source: %#v", manifest.Files)
	}
}

func TestBeaconSystemEndpointRootAcceptsDirectConfigJSON(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "Beacon", "Endpoint")
	if err := os.MkdirAll(endpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(endpoint, "config.json"), []byte(`{"user_mode":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 || manifest.Files[0].Path != "config.json" {
		t.Fatalf("direct system endpoint config was not recognized: %#v", manifest)
	}
}

func TestBeaconOpenCodePluginPresenceMigrationIsPathBoundAndSourceFree(t *testing.T) {
	root := t.TempDir()
	pluginPath := filepath.Join(root, ".opencode", "plugins", "beacon.ts")
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := beaconOpenCodePluginMarker + "\r\nconst privateEndpoint = 'https://private.example.invalid';\n" +
		"const apiKey = 'do-not-import-plugin-secret';\nexport default {};\n"
	if err := os.WriteFile(pluginPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesScanned != 1 || manifest.Summary.FilesReady != 1 || manifest.Summary.RecordsImported != 1 ||
		manifest.Summary.FilesUnsupported != 0 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("managed OpenCode plugin did not produce one presence record: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "beacon_opencode_plugin_presence" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected OpenCode plugin presence record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"privateEndpoint", "private.example.invalid", "do-not-import-plugin-secret", "apiKey", "export default", pluginPath,
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("OpenCode plugin presence projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-opencode-plugin-config.v1"`,
		`"kind":"managed_plugin_presence"`, `"source_format":"typescript_plugin"`, `"harness":"opencode"`,
		`"managed_marker_present":true`, `"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("OpenCode plugin presence projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconOpenCodePluginPathAllowlistAndMarkerValidation(t *testing.T) {
	paths := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{"project plugin", filepath.Join("repo", ".opencode", "plugins", "beacon.ts"), `C:\source`, true},
		{"user home config", filepath.Join(".config", "opencode", "plugins", "beacon.ts"), `C:\home`, true},
		{"selected XDG config root", filepath.Join("opencode", "plugins", "beacon.ts"), `C:\xdg`, true},
		{"selected project config root", filepath.Join("plugins", "beacon.ts"), filepath.Join(`C:\repo`, ".opencode"), true},
		{"selected user config root", filepath.Join("plugins", "beacon.ts"), filepath.Join(`C:\xdg`, "opencode"), true},
		{"selected plugin directory", "beacon.ts", filepath.Join(`C:\home`, ".config", "opencode", "plugins"), true},
		{"unrelated plugin", filepath.Join("repo", "plugins", "beacon.ts"), `C:\source`, false},
		{"wrong config", filepath.Join("repo", ".cursor", "plugins", "beacon.ts"), `C:\source`, false},
		{"wrong filename", filepath.Join("repo", ".opencode", "plugins", "custom.ts"), `C:\source`, false},
	}
	for _, test := range paths {
		t.Run(test.name, func(t *testing.T) {
			if got := isBeaconOpenCodePluginPath(test.relative, test.sourceRoot); got != test.want {
				t.Fatalf("isBeaconOpenCodePluginPath(%q, %q) = %v, want %v", test.relative, test.sourceRoot, got, test.want)
			}
		})
	}
	for _, source := range []string{
		"export default {};\n// beacon-managed-opencode-plugin:v1\n",
		"// beacon-managed-opencode-plugin:v2\nexport default {};\n",
		"// beacon-managed-opencode-plugin:v1x\nexport default {};\n",
		" // beacon-managed-opencode-plugin:v1\nexport default {};\n",
	} {
		if _, err := normalizeBeaconOpenCodePlugin([]byte(source)); !errors.Is(err, errBeaconOpenCodePlugin) {
			t.Fatalf("unmarked/unsupported plugin source returned %v", err)
		}
	}
	if _, err := normalizeBeaconOpenCodePlugin([]byte(strings.Repeat("x", maxBeaconOpenCodePluginBytes+1))); !errors.Is(err, errBeaconOpenCodePlugin) {
		t.Fatalf("oversized plugin source returned %v", err)
	}
}

func TestBeaconClinePluginPresenceMigrationIsPathBoundAndSourceFree(t *testing.T) {
	root := t.TempDir()
	pluginPath := filepath.Join(root, ".cline", "plugins", "beacon.ts")
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := beaconClinePluginMarker + "\r\nconst privateEndpoint = 'https://private.example.invalid';\n" +
		"const apiKey = 'do-not-import-plugin-secret';\nexport default {} as const;\n"
	if err := os.WriteFile(pluginPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesScanned != 1 || manifest.Summary.FilesReady != 1 || manifest.Summary.RecordsImported != 1 ||
		manifest.Summary.FilesUnsupported != 0 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("managed Cline plugin did not produce one presence record: %#v", manifest.Summary)
	}
	if format := migrationFormatID(Beacon, root, filepath.ToSlash(filepath.Join(".cline", "plugins", "beacon.ts"))); format != "beacon.cline-plugin" {
		t.Fatalf("Cline plugin got migration family %q", format)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "beacon_cline_plugin_presence" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Cline plugin presence record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"privateEndpoint", "private.example.invalid", "do-not-import-plugin-secret", "apiKey", "export default", pluginPath,
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("Cline plugin presence projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-cline-plugin-config.v1"`,
		`"kind":"managed_plugin_presence"`, `"source_format":"typescript_plugin"`, `"harness":"cline"`,
		`"managed_marker_present":true`, `"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("Cline plugin presence projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconClinePluginPathAllowlistAndMarkerValidation(t *testing.T) {
	paths := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{"project plugin", filepath.Join("repo", ".cline", "plugins", "beacon.ts"), `C:\source`, true},
		{"user home config", filepath.Join(".cline", "plugins", "beacon.ts"), `C:\home`, true},
		{"selected Cline config root", filepath.Join("plugins", "beacon.ts"), filepath.Join(`C:\repo`, ".cline"), true},
		{"selected plugin directory", "beacon.ts", filepath.Join(`C:\home`, ".cline", "plugins"), true},
		{"noncanonical config directory", filepath.Join("cline", "plugins", "beacon.ts"), `C:\home`, false},
		{"unrelated plugin", filepath.Join("repo", "plugins", "beacon.ts"), `C:\source`, false},
		{"wrong config", filepath.Join("repo", ".opencode", "plugins", "beacon.ts"), `C:\source`, false},
		{"wrong filename", filepath.Join("repo", ".cline", "plugins", "custom.ts"), `C:\source`, false},
	}
	for _, test := range paths {
		t.Run(test.name, func(t *testing.T) {
			if got := isBeaconClinePluginPath(test.relative, test.sourceRoot); got != test.want {
				t.Fatalf("isBeaconClinePluginPath(%q, %q) = %v, want %v", test.relative, test.sourceRoot, got, test.want)
			}
		})
	}
	for _, source := range []string{
		"export default {};\n// beacon-managed-cline-plugin:v1\n",
		"// beacon-managed-cline-plugin:v2\nexport default {};\n",
		"// beacon-managed-cline-plugin:v1x\nexport default {};\n",
		" // beacon-managed-cline-plugin:v1\nexport default {};\n",
	} {
		if _, err := normalizeBeaconClinePlugin([]byte(source)); !errors.Is(err, errBeaconClinePlugin) {
			t.Fatalf("unmarked/unsupported plugin source returned %v", err)
		}
	}
	if _, err := normalizeBeaconClinePlugin([]byte{0xff}); !errors.Is(err, errBeaconClinePlugin) {
		t.Fatalf("invalid UTF-8 plugin source returned %v", err)
	}
	if _, err := normalizeBeaconClinePlugin([]byte(strings.Repeat("x", maxBeaconClinePluginBytes+1))); !errors.Is(err, errBeaconClinePlugin) {
		t.Fatalf("oversized plugin source returned %v", err)
	}
}

func TestBeaconClaudeSettingsMigrationRetainsOnlyManagedHookEventNames(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"apiKey":     "do-not-copy-user-key",
		"env":        map[string]any{"API_TOKEN": "do-not-copy-env-token", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318"},
		"mcpServers": map[string]any{"private-server": map[string]any{"command": "echo private-user-config"}},
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "'C:\\Program Files\\Beacon\\beacon-hooks.exe' --platform 'claude' --log 'C:\\Private\\runtime.jsonl' --config 'C:\\Private\\config.json' session-start",
			}}}},
			"PreToolUse": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "beacon-hooks --platform factory pre-tool",
			}}}},
			"PostToolUseFailure": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "beacon-hooks --platform=claude post-tool",
			}}}},
		},
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Claude settings were not recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "endpoint_configuration" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Claude settings record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"do-not-copy-user-key", "do-not-copy-env-token", "127.0.0.1", "private-server",
		"private-user-config", "Program Files", "runtime.jsonl", "config.json", "factory", "beacon-hooks",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("Claude settings projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-harness-config.v1"`,
		`"harness":"claude_code"`,
		`"beacon_hooks_configured":true`,
		`"hook_events":["SessionStart","PostToolUseFailure"]`,
		`"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("Claude settings projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconClaudeSettingsPathAllowlistIsProductAndRootBounded(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "project settings", relative: filepath.Join("repo", ".claude", "settings.json"), sourceRoot: `C:\source`, want: true},
		{name: "selected user config root", relative: "settings.json", sourceRoot: filepath.Join(`C:\home`, ".claude"), want: true},
		{name: "unrelated json", relative: "settings.json", sourceRoot: `C:\source`, want: false},
		{name: "other harness config", relative: filepath.Join("repo", ".codex", "settings.json"), sourceRoot: `C:\source`, want: false},
		{name: "nested settings", relative: filepath.Join("repo", ".claude", "plugins", "settings.json"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconClaudeSettingsPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconClaudeSettingsPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconClaudeSettingsRejectsMalformedHooksObject(t *testing.T) {
	_, err := normalizeBeaconClaudeSettings(map[string]any{"hooks": []any{"not-an-object"}})
	if !errors.Is(err, errBeaconClaudeHooks) {
		t.Fatalf("malformed Claude hooks returned %v, want %v", err, errBeaconClaudeHooks)
	}
}

func TestBeaconQwenSettingsMigrationRetainsOnlyManagedHookEventNames(t *testing.T) {
	root := t.TempDir()
	qwenDir := filepath.Join(root, ".qwen")
	if err := os.MkdirAll(qwenDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"apiKey": "do-not-copy-qwen-key",
		"env":    map[string]any{"API_TOKEN": "do-not-copy-qwen-token"},
		"mcpServers": map[string]any{
			"private-server": map[string]any{"command": "echo private-qwen-config"},
		},
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "beacon-hooks --platform qwen session-start --log C:\\private\\runtime.jsonl",
			}}}},
			"PreToolUse": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "'C:\\Beacon\\beacon-hooks.exe' --platform 'qwen' pre-tool",
			}}}},
			"PermissionRequest": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "beacon-hooks --platform claude permission-request",
			}}}},
			"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "user-owned-hook prompt-submit",
			}}}},
		},
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qwenDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Qwen settings were not recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "endpoint_configuration" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Qwen settings record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"do-not-copy-qwen-key", "do-not-copy-qwen-token", "private-qwen-config", "private-server",
		"runtime.jsonl", "beacon-hooks", "user-owned-hook", "claude", "C:\\\\Beacon",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("Qwen settings projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-harness-config.v1"`,
		`"harness":"qwen_code"`,
		`"beacon_hooks_configured":true`,
		`"hook_events":["SessionStart","PreToolUse"]`,
		`"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("Qwen settings projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconQwenSettingsPathAllowlistIsProductAndRootBounded(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "project settings", relative: filepath.Join("repo", ".qwen", "settings.json"), sourceRoot: `C:\source`, want: true},
		{name: "selected user config root", relative: "settings.json", sourceRoot: filepath.Join(`C:\home`, ".qwen"), want: true},
		{name: "unrelated json", relative: "settings.json", sourceRoot: `C:\source`, want: false},
		{name: "other harness config", relative: filepath.Join("repo", ".codex", "settings.json"), sourceRoot: `C:\source`, want: false},
		{name: "nested settings", relative: filepath.Join("repo", ".qwen", "plugins", "settings.json"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconQwenSettingsPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconQwenSettingsPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconQwenSettingsRejectsMalformedHooksObject(t *testing.T) {
	_, err := normalizeBeaconQwenSettings(map[string]any{"hooks": []any{"not-an-object"}})
	if !errors.Is(err, errBeaconQwenHooks) {
		t.Fatalf("malformed Qwen hooks returned %v, want %v", err, errBeaconQwenHooks)
	}
}

func TestBeaconFactorySettingsMigrationRetainsOnlyManagedHookEventNames(t *testing.T) {
	root := t.TempDir()
	factoryDir := filepath.Join(root, ".factory")
	if err := os.MkdirAll(factoryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"apiKey": "do-not-copy-factory-key",
		"env":    map[string]any{"API_TOKEN": "do-not-copy-factory-token"},
		"mcpServers": map[string]any{
			"private-server": map[string]any{"command": "echo private-factory-config"},
		},
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "BEACON_ENDPOINT_MODE=1 beacon-hooks --platform factory session-start --log C:\\private\\runtime.jsonl",
			}}}},
			"PostToolUse": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "'C:\\Beacon\\beacon-hooks.exe' --platform 'factory' post-tool",
			}}}},
			"PreToolUse": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "beacon-hooks --platform factory pre-tool",
			}}}},
			"Stop": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "beacon-hooks --platform qwen stop",
			}}}},
			"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": "user-owned-hook prompt-submit",
			}}}},
		},
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(factoryDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Factory settings were not recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "endpoint_configuration" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Factory settings record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"do-not-copy-factory-key", "do-not-copy-factory-token", "private-factory-config", "private-server",
		"runtime.jsonl", "beacon-hooks", "user-owned-hook", "qwen", "C:\\\\Beacon",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("Factory settings projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-harness-config.v1"`,
		`"harness":"factory"`,
		`"beacon_hooks_configured":true`,
		`"hook_events":["SessionStart","PostToolUse"]`,
		`"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("Factory settings projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconFactorySettingsPathAllowlistIsProductAndRootBounded(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "project settings", relative: filepath.Join("repo", ".factory", "settings.json"), sourceRoot: `C:\source`, want: true},
		{name: "selected user config root", relative: "settings.json", sourceRoot: filepath.Join(`C:\home`, ".factory"), want: true},
		{name: "unrelated json", relative: "settings.json", sourceRoot: `C:\source`, want: false},
		{name: "other harness config", relative: filepath.Join("repo", ".codex", "settings.json"), sourceRoot: `C:\source`, want: false},
		{name: "nested settings", relative: filepath.Join("repo", ".factory", "plugins", "settings.json"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconFactorySettingsPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconFactorySettingsPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconFactorySettingsRejectsMalformedHooksObject(t *testing.T) {
	_, err := normalizeBeaconFactorySettings(map[string]any{"hooks": []any{"not-an-object"}})
	if !errors.Is(err, errBeaconFactoryHooks) {
		t.Fatalf("malformed Factory hooks returned %v, want %v", err, errBeaconFactoryHooks)
	}
}

func TestBeaconCursorHooksMigrationRetainsOnlyManagedHookEventNames(t *testing.T) {
	root := t.TempDir()
	cursorDir := filepath.Join(root, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]any{
		"version": 1,
		"apiKey":  "do-not-copy-cursor-key",
		"env":     map[string]any{"API_TOKEN": "do-not-copy-cursor-token"},
		"mcpServers": map[string]any{
			"private-server": map[string]any{"command": "echo private-cursor-config"},
		},
		"hooks": map[string]any{
			"sessionStart": []any{map[string]any{
				"command": "user-owned-hook session-start", "matcher": "private-matcher",
			}},
			"beforeSubmitPrompt": []any{map[string]any{
				"command": "BEACON_ENDPOINT_MODE=1 beacon-hooks --platform cursor prompt-submit --log C:\\private\\runtime.jsonl",
				"matcher": "private-prompt-matcher", "timeout": 30,
			}},
			"preToolUse": []any{map[string]any{
				"command": "beacon-hooks --platform claude pre-tool", "matcher": "private-tool-matcher",
			}},
			"afterFileEdit": []any{map[string]any{
				"command": "'C:\\Beacon\\beacon-hooks.exe' --platform 'cursor' post-tool",
			}},
			"unsupportedEvent": []any{map[string]any{
				"command": "beacon-hooks --platform cursor unsupported",
			}},
		},
	}
	data, err := json.Marshal(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "hooks.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Cursor hooks were not recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "endpoint_configuration" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Cursor hooks record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"do-not-copy-cursor-key", "do-not-copy-cursor-token", "private-cursor-config", "private-server",
		"private-matcher", "runtime.jsonl", "beacon-hooks", "user-owned-hook", "unsupportedEvent",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("Cursor hooks projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-harness-config.v1"`,
		`"harness":"cursor"`,
		`"beacon_hooks_configured":true`,
		`"hook_events":["beforeSubmitPrompt","afterFileEdit"]`,
		`"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("Cursor hooks projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconCursorHooksPathAllowlistIsProductAndRootBounded(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "project hooks", relative: filepath.Join("repo", ".cursor", "hooks.json"), sourceRoot: `C:\source`, want: true},
		{name: "selected user config root", relative: "hooks.json", sourceRoot: filepath.Join(`C:\home`, ".cursor"), want: true},
		{name: "unrelated json", relative: "hooks.json", sourceRoot: `C:\source`, want: false},
		{name: "other harness hooks", relative: filepath.Join("repo", ".codex", "hooks.json"), sourceRoot: `C:\source`, want: false},
		{name: "nested hooks", relative: filepath.Join("repo", ".cursor", "plugins", "hooks.json"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconCursorHooksPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconCursorHooksPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconCursorHooksRejectUnsupportedVersionAndMalformedHooks(t *testing.T) {
	if _, err := normalizeBeaconCursorHooks(map[string]any{"version": 2}); !errors.Is(err, errBeaconCursorVersion) {
		t.Fatalf("unsupported Cursor hooks version returned %v, want %v", err, errBeaconCursorVersion)
	}
	if _, err := normalizeBeaconCursorHooks(map[string]any{"version": "1"}); !errors.Is(err, errBeaconCursorVersion) {
		t.Fatalf("non-numeric Cursor hooks version returned %v, want %v", err, errBeaconCursorVersion)
	}
	if _, err := normalizeBeaconCursorHooks(map[string]any{"version": 1, "hooks": []any{"invalid"}}); !errors.Is(err, errBeaconCursorHooks) {
		t.Fatalf("malformed Cursor hooks returned %v, want %v", err, errBeaconCursorHooks)
	}
	if _, err := normalizeBeaconCursorHooks(map[string]any{"hooks": []any{"invalid"}}); !errors.Is(err, errBeaconCursorHooks) {
		t.Fatalf("malformed version-defaulted Cursor hooks returned %v, want %v", err, errBeaconCursorHooks)
	}
}

func TestBeaconCodexHooksMigrationRetainsOnlyManagedHookEventNames(t *testing.T) {
	root := t.TempDir()
	codexDir := filepath.Join(root, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]any{
		"apiKey": "do-not-copy-codex-key",
		"env":    map[string]any{"API_TOKEN": "do-not-copy-codex-token"},
		"mcp_servers": map[string]any{
			"private-server": map[string]any{"command": "echo private-codex-config"},
		},
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": "BEACON_ENDPOINT_MODE=1 beacon-hooks --platform codex codex-session-context --log C:\\private\\runtime.jsonl"},
			}}},
			"Stop": []any{map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": "beacon-hooks --platform codex codex-usage-sync"},
			}}},
			"UserPromptSubmit": []any{map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": "beacon-hooks --platform claude prompt-submit"},
			}}},
			"UnsupportedEvent": []any{map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": "beacon-hooks --platform codex private"},
			}}},
		},
	}
	data, err := json.Marshal(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "hooks.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Codex hooks were not recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "endpoint_configuration" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Codex hooks record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"do-not-copy-codex-key", "do-not-copy-codex-token", "private-codex-config", "private-server",
		"runtime.jsonl", "beacon-hooks", "codex-session-context", "codex-usage-sync", "prompt-submit", "UnsupportedEvent",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("Codex hooks projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-harness-config.v1"`,
		`"harness":"codex_cli"`,
		`"beacon_hooks_configured":true`,
		`"hook_events":["SessionStart","Stop"]`,
		`"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("Codex hooks projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconCodexHooksPathAllowlistIsProductAndRootBounded(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "project hooks", relative: filepath.Join("repo", ".codex", "hooks.json"), sourceRoot: `C:\source`, want: true},
		{name: "selected user config root", relative: "hooks.json", sourceRoot: filepath.Join(`C:\home`, ".codex"), want: true},
		{name: "unrelated json", relative: "hooks.json", sourceRoot: `C:\source`, want: false},
		{name: "other harness hooks", relative: filepath.Join("repo", ".cursor", "hooks.json"), sourceRoot: `C:\source`, want: false},
		{name: "nested hooks", relative: filepath.Join("repo", ".codex", "plugins", "hooks.json"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconCodexHooksPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconCodexHooksPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconCodexHooksRejectMalformedHooksObject(t *testing.T) {
	if _, err := normalizeBeaconCodexHooks(map[string]any{"hooks": []any{"invalid"}}); !errors.Is(err, errBeaconCodexHooks) {
		t.Fatalf("malformed Codex hooks returned %v, want %v", err, errBeaconCodexHooks)
	}
}

func TestBeaconCodexOTelConfigMigrationProjectsOnlySafeTelemetryMetadata(t *testing.T) {
	root := t.TempDir()
	codexDir := filepath.Join(root, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `[otel]
environment = "dev"
log_user_prompt = true

[otel.exporter."otlp-grpc"]
endpoint = "http://127.0.0.1:4317"
headers.authorization = "Bearer do-not-copy-exporter-token"

[otel.trace_exporter."otlp-http"]
endpoint = "https://private.example/v1/traces?token=do-not-copy-url-token"

[profile]
name = "do-not-copy-private-profile"
`
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Codex telemetry config was not recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "endpoint_configuration" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Codex telemetry record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"127.0.0.1", "private.example", "do-not-copy-exporter-token", "do-not-copy-url-token",
		"private-profile", "Bearer", "authorization",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("Codex telemetry projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-codex-otel-config.v1"`,
		`"kind":"codex_otel_configuration"`,
		`"harness":"codex_cli"`,
		`"otel_configured":true`,
		`"environment":"dev"`,
		`"log_user_prompt":true`,
		`"name":"otlp-grpc"`, `"endpoint_scope":"loopback"`,
		`"name":"otlp-http"`, `"endpoint_scope":"non_loopback"`,
		`"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("Codex telemetry projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconCodexOTelConfigPathAllowlistIsProductAndRootBounded(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "project config", relative: filepath.Join("repo", ".codex", "config.toml"), sourceRoot: `C:\source`, want: true},
		{name: "selected user config root", relative: "config.toml", sourceRoot: filepath.Join(`C:\home`, ".codex"), want: true},
		{name: "unrelated TOML", relative: "config.toml", sourceRoot: `C:\source`, want: false},
		{name: "other harness config", relative: filepath.Join("repo", ".cursor", "config.toml"), sourceRoot: `C:\source`, want: false},
		{name: "nested config", relative: filepath.Join("repo", ".codex", "plugins", "config.toml"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconCodexOTelConfigPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconCodexOTelConfigPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconCodexOTelConfigRejectsUnrecognizedOrMalformedTables(t *testing.T) {
	tests := []struct {
		name  string
		value map[string]any
	}{
		{name: "missing otel table", value: map[string]any{"profile": map[string]any{"name": "private"}}},
		{name: "no recognized exporters", value: map[string]any{"otel": map[string]any{"exporter": map[string]any{"custom": map[string]any{"endpoint": "http://127.0.0.1"}}}}},
		{name: "non-object exporter table", value: map[string]any{"otel": map[string]any{"exporter": []any{"invalid"}}}},
		{name: "non-string endpoint", value: map[string]any{"otel": map[string]any{"exporter": map[string]any{"otlp-grpc": map[string]any{"endpoint": 4317}}}}},
		{name: "non-boolean prompt setting", value: map[string]any{"otel": map[string]any{"log_user_prompt": "private", "exporter": map[string]any{"otlp-grpc": map[string]any{}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeBeaconCodexOTelConfig(tt.value); !errors.Is(err, errBeaconCodexOTelConfig) {
				t.Fatalf("normalizeBeaconCodexOTelConfig returned %v, want %v", err, errBeaconCodexOTelConfig)
			}
		})
	}
}

func TestBeaconGeminiSettingsMigrationRetainsOnlySafeOTelMetadata(t *testing.T) {
	root := t.TempDir()
	geminiDir := filepath.Join(root, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"apiKey":     "do-not-copy-gemini-key",
		"mcpServers": map[string]any{"private-server": map[string]any{"command": "echo private-gemini-config"}},
		"telemetry": map[string]any{
			"enabled":      true,
			"target":       "local",
			"otlpEndpoint": "http://127.0.0.1:4317",
			"otlpProtocol": "grpc",
			"useCollector": true,
			"logPrompts":   true,
			"traces":       true,
			"outfile":      `C:\private\telemetry.log`,
			"headers":      map[string]any{"Authorization": "Bearer do-not-copy-gemini-token"},
		},
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(geminiDir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Gemini telemetry settings were not recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "endpoint_configuration" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Gemini telemetry record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"127.0.0.1", "do-not-copy-gemini-key", "do-not-copy-gemini-token", "private-server",
		"private-gemini-config", "telemetry.log", "Authorization", "Bearer",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("Gemini telemetry projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-gemini-otel-config.v1"`,
		`"kind":"gemini_otel_configuration"`,
		`"harness":"gemini_cli"`,
		`"telemetry_configured":true`,
		`"target":"local"`,
		`"endpoint_scope":"loopback"`,
		`"otlp_protocol":"grpc"`,
		`"use_collector":true`,
		`"enabled":true`, `"log_prompts":true`, `"traces":true`,
		`"outfile_configured":true`,
		`"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("Gemini telemetry projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconGeminiSettingsPathAllowlistIsProductAndRootBounded(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "user settings", relative: filepath.Join("home", ".gemini", "settings.json"), sourceRoot: `C:\source`, want: true},
		{name: "selected Gemini root", relative: "settings.json", sourceRoot: filepath.Join(`C:\home`, ".gemini"), want: true},
		{name: "unrelated JSON", relative: "settings.json", sourceRoot: `C:\source`, want: false},
		{name: "other harness settings", relative: filepath.Join("repo", ".codex", "settings.json"), sourceRoot: `C:\source`, want: false},
		{name: "nested Gemini settings", relative: filepath.Join("repo", ".gemini", "nested", "settings.json"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconGeminiSettingsPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconGeminiSettingsPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconGeminiSettingsRejectsNonBeaconOrMalformedTelemetry(t *testing.T) {
	validTelemetry := func() map[string]any {
		return map[string]any{
			"target": "local", "otlpEndpoint": "http://127.0.0.1:4317", "otlpProtocol": "grpc", "useCollector": true,
		}
	}
	tests := []struct {
		name  string
		value map[string]any
	}{
		{name: "missing telemetry", value: map[string]any{"hooks": map[string]any{}}},
		{name: "remote target", value: map[string]any{"telemetry": map[string]any{"target": "gcp"}}},
		{name: "remote endpoint", value: map[string]any{"telemetry": map[string]any{"target": "local", "otlpEndpoint": "https://collector.example"}}},
		{name: "wrong protocol", value: map[string]any{"telemetry": map[string]any{"target": "local", "otlpEndpoint": "http://127.0.0.1:4317", "otlpProtocol": "http", "useCollector": true}}},
		{name: "collector disabled", value: map[string]any{"telemetry": map[string]any{"target": "local", "otlpEndpoint": "http://127.0.0.1:4317", "otlpProtocol": "grpc", "useCollector": false}}},
	}
	badPromptSetting := validTelemetry()
	badPromptSetting["logPrompts"] = "private"
	tests = append(tests, struct {
		name  string
		value map[string]any
	}{name: "malformed prompt preference", value: map[string]any{"telemetry": badPromptSetting}})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeBeaconGeminiSettings(tt.value); !errors.Is(err, errBeaconGeminiConfig) {
				t.Fatalf("normalizeBeaconGeminiSettings returned %v, want %v", err, errBeaconGeminiConfig)
			}
		})
	}
}

func TestBeaconAntigravityHooksMigrationRetainsOnlyManagedEventNames(t *testing.T) {
	root := t.TempDir()
	projectHooks := filepath.Join(root, ".agents")
	if err := os.MkdirAll(projectHooks, 0o700); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]any{
		"apiKey": "do-not-copy-antigravity-key",
		"beacon-endpoint": map[string]any{
			"PreInvocation": []any{map[string]any{"hooks": []any{
				map[string]any{"command": "BEACON_ENDPOINT_MODE=1 beacon-hooks --platform antigravity prompt-submit --log C:\\private\\runtime.jsonl"},
			}}},
			"UserPromptSubmit": []any{map[string]any{"hooks": []any{
				map[string]any{"command": "echo user-owned-prompt-hook"},
			}}},
			"PreToolUse": []any{map[string]any{"hooks": []any{
				map[string]any{"command": "'C:\\Beacon\\beacon-hooks.exe' --platform 'antigravity' pre-tool"},
			}}},
			"PostToolUse": []any{map[string]any{"hooks": []any{
				map[string]any{"command": "beacon-hooks --platform cursor post-tool"},
			}}},
			"PostInvocation": []any{map[string]any{"hooks": []any{
				map[string]any{"command": "beacon-hooks --platform antigravity stop"},
			}}},
			"Stop": []any{map[string]any{"hooks": []any{
				map[string]any{"command": "beacon-hooks --platform antigravity stop"},
			}}},
			"UnsupportedEvent": []any{map[string]any{"hooks": []any{
				map[string]any{"command": "beacon-hooks --platform antigravity private"},
			}}},
		},
		"other-hook-owner": map[string]any{"PostToolUse": []any{map[string]any{
			"hooks": []any{map[string]any{"command": "echo unrelated-private-hook"}},
		}}},
	}
	data, err := json.Marshal(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectHooks, "hooks.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Antigravity hooks were not recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "endpoint_configuration" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Antigravity hooks record: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, forbidden := range []string{
		"do-not-copy-antigravity-key", "private\\runtime.jsonl", "beacon-hooks", "C:\\Beacon",
		"user-owned-prompt-hook", "cursor", "UnsupportedEvent", "other-hook-owner", "unrelated-private-hook",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("Antigravity hooks projection retained %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{
		`"schema_version":"threadforge.beacon-harness-config.v1"`,
		`"harness":"antigravity"`,
		`"beacon_hooks_configured":true`,
		`"hook_events":["PreInvocation","PreToolUse","PostInvocation","Stop"]`,
		`"human_review_required":true`, `"execution_authorized":false`,
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("Antigravity hooks projection omitted %q: %s", expected, payload)
		}
	}
}

func TestBeaconAntigravityHooksPathAllowlistIsProductAndRootBounded(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "user Antigravity config", relative: filepath.Join("home", ".gemini", "config", "hooks.json"), sourceRoot: `C:\source`, want: true},
		{name: "project shared hooks", relative: filepath.Join("repo", ".agents", "hooks.json"), sourceRoot: `C:\source`, want: true},
		{name: "selected project hooks root", relative: "hooks.json", sourceRoot: filepath.Join(`C:\repo`, ".agents"), want: true},
		{name: "selected Gemini home root", relative: filepath.Join("config", "hooks.json"), sourceRoot: filepath.Join(`C:\home`, ".gemini"), want: true},
		{name: "selected user hooks root", relative: "hooks.json", sourceRoot: filepath.Join(`C:\home`, ".gemini", "config"), want: true},
		{name: "unrelated JSON", relative: "hooks.json", sourceRoot: `C:\source`, want: false},
		{name: "other host hooks", relative: filepath.Join("repo", ".cursor", "hooks.json"), sourceRoot: `C:\source`, want: false},
		{name: "nested shared hooks", relative: filepath.Join("repo", ".agents", "nested", "hooks.json"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconAntigravityHooksPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconAntigravityHooksPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconAntigravityHooksRejectMalformedManagedBlock(t *testing.T) {
	if _, err := normalizeBeaconAntigravityHooks(map[string]any{"beacon-endpoint": []any{"invalid"}}); !errors.Is(err, errBeaconAntigravity) {
		t.Fatalf("malformed Antigravity block returned %v, want %v", err, errBeaconAntigravity)
	}
}

func TestBeaconOpenHandsHooksMigrationSupportsBothDocumentedShapes(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "home", ".openhands")
	projectDir := filepath.Join(root, "repo", ".openhands")
	for _, dir := range []string{userDir, projectDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	userHooks := map[string]any{
		"apiKey": "do-not-copy-wrapper-sibling",
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"hooks": []any{
				map[string]any{"command": "beacon-hooks --platform openhands session-start --log C:\\private\\runtime.jsonl"},
			}}},
			"SessionEnd": []any{map[string]any{"hooks": []any{
				map[string]any{"command": "echo user-owned-session-end"},
			}}},
		},
	}
	projectHooks := map[string]any{
		"session_start": []any{map[string]any{"hooks": []any{
			map[string]any{"command": "beacon-hooks --platform openhands session-start --log C:\\private\\runtime.jsonl"},
		}}},
		"user_prompt_submit": []any{map[string]any{"hooks": []any{
			map[string]any{"command": "echo user-owned-prompt-hook"},
		}}},
		"pre_tool_use": []any{map[string]any{"hooks": []any{
			map[string]any{"command": "beacon-hooks --platform=openhands pre-tool"},
		}}},
		"post_tool_use": []any{map[string]any{"hooks": []any{
			map[string]any{"command": "beacon-hooks --platform cursor post-tool"},
		}}},
		"stop": []any{map[string]any{"hooks": []any{
			map[string]any{"command": "beacon-hooks --platform openhands stop"},
		}}},
		"session_end": []any{map[string]any{"hooks": []any{
			map[string]any{"command": "beacon-hooks --platform openhands session-end"},
		}}},
	}
	for path, value := range map[string]map[string]any{
		filepath.Join(userDir, "hooks.json"):    userHooks,
		filepath.Join(projectDir, "hooks.json"): projectHooks,
	} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 2 || len(manifest.Files) != 2 {
		t.Fatalf("OpenHands hooks were not recognized at both scopes: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("ReadSnapshotRecords returned %d OpenHands records, want 2", len(records))
	}
	wantByScope := map[string][]string{
		"user":    {"SessionStart"},
		"project": {"session_start", "pre_tool_use", "stop", "session_end"},
	}
	for _, record := range records {
		if record.Type != "endpoint_configuration" || record.Scope != "local_pending_review" {
			t.Fatalf("unexpected OpenHands record metadata: %#v", record)
		}
		var payload struct {
			Harness             string   `json:"harness"`
			Configured          bool     `json:"beacon_hooks_configured"`
			Events              []string `json:"hook_events"`
			HumanReviewRequired bool     `json:"human_review_required"`
			ExecutionAuthorized bool     `json:"execution_authorized"`
		}
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Harness != "openhands" || !payload.Configured || !payload.HumanReviewRequired || payload.ExecutionAuthorized {
			t.Fatalf("unexpected OpenHands projection: %#v", payload)
		}
		scope := "project"
		sourcePath := filepath.ToSlash(record.SourcePath)
		if strings.HasPrefix(sourcePath, "home/") || strings.Contains(sourcePath, "/home/") {
			scope = "user"
		}
		if !reflect.DeepEqual(payload.Events, wantByScope[scope]) {
			t.Fatalf("%s OpenHands event projection at %q = %#v, want %#v", scope, record.SourcePath, payload.Events, wantByScope[scope])
		}
		for _, forbidden := range []string{
			"do-not-copy", "private/runtime.jsonl", "beacon-hooks", "echo user-owned", "cursor", "apiKey",
		} {
			if strings.Contains(string(record.Payload), forbidden) {
				t.Fatalf("OpenHands projection retained %q: %s", forbidden, record.Payload)
			}
		}
	}
}

func TestBeaconOpenHandsHooksPathAllowlistIsProductAndRootBounded(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "user hooks", relative: filepath.Join("home", ".openhands", "hooks.json"), sourceRoot: `C:\source`, want: true},
		{name: "project hooks", relative: filepath.Join("repo", ".openhands", "hooks.json"), sourceRoot: `C:\source`, want: true},
		{name: "selected OpenHands root", relative: "hooks.json", sourceRoot: filepath.Join(`C:\home`, ".openhands"), want: true},
		{name: "explicit custom persistence root", relative: "hooks.json", sourceRoot: `C:\custom-openhands-state`, want: true},
		{name: "other harness hooks", relative: filepath.Join("repo", ".codex", "hooks.json"), sourceRoot: `C:\source`, want: false},
		{name: "nested OpenHands hooks", relative: filepath.Join("repo", ".openhands", "nested", "hooks.json"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconOpenHandsHooksPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconOpenHandsHooksPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconOpenHandsCustomPersistenceRootRequiresBeaconMarker(t *testing.T) {
	root := t.TempDir()
	hooks := map[string]any{
		"stop": []any{map[string]any{"hooks": []any{
			map[string]any{"command": "beacon-hooks --platform openhands stop"},
		}}},
	}
	data, err := json.Marshal(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hooks.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("custom OpenHands persistence root was not recognized: %#v", manifest.Summary)
	}

	withoutMarker := map[string]any{
		"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{
			map[string]any{"command": "echo user-owned"},
		}}}},
	}
	if _, err := normalizeBeaconOpenHandsHooksAtExplicitRoot(withoutMarker); !errors.Is(err, errBeaconOpenHandsHooks) {
		t.Fatalf("custom root without Beacon marker returned %v, want %v", err, errBeaconOpenHandsHooks)
	}
}

func TestBeaconOpenHandsHooksRejectMalformedOrAmbiguousEvents(t *testing.T) {
	tests := []struct {
		name  string
		value map[string]any
	}{
		{name: "non-object wrapper", value: map[string]any{"hooks": []any{"invalid"}}},
		{name: "unknown top-level event", value: map[string]any{"unknown_event": []any{}}},
		{name: "both event spellings", value: map[string]any{"stop": []any{}, "Stop": []any{}}},
		{name: "non-array event", value: map[string]any{"session_start": map[string]any{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeBeaconOpenHandsHooks(tt.value); !errors.Is(err, errBeaconOpenHandsHooks) {
				t.Fatalf("normalizeBeaconOpenHandsHooks returned %v, want %v", err, errBeaconOpenHandsHooks)
			}
		})
	}
}

func beaconKiroHookFile(triggers []string, disabledTrigger string) map[string]any {
	subcommands := map[string]string{
		"SessionStart": "session-start", "UserPromptSubmit": "prompt-submit", "PreToolUse": "pre-tool",
		"PostToolUse": "post-tool", "Stop": "stop",
	}
	timeouts := map[string]int{"SessionStart": 10, "UserPromptSubmit": 30, "PreToolUse": 10, "PostToolUse": 10, "Stop": 45}
	hooks := make([]any, 0, len(triggers))
	for _, trigger := range triggers {
		platformFlag := "--platform kiro"
		if trigger == "Stop" {
			platformFlag = "--platform=kiro"
		}
		hooks = append(hooks, map[string]any{
			"name":        "beacon-endpoint-telemetry-" + strings.ToLower(trigger),
			"description": "private-description-not-imported",
			"trigger":     trigger,
			"action": map[string]any{
				"type":    "command",
				"command": "'C:\\private\\beacon-hooks.exe' " + platformFlag + " --log 'C:\\private\\runtime.jsonl' " + subcommands[trigger],
			},
			"timeout": timeouts[trigger],
			"enabled": trigger != disabledTrigger,
		})
	}
	return map[string]any{"version": "v1", "hooks": hooks}
}

func TestBeaconKiroHooksMigrationCoversUserProjectAndSnapshotReadback(t *testing.T) {
	root := t.TempDir()
	userPath := filepath.Join(root, "home", ".kiro", "hooks", "beacon-endpoint.json")
	projectPath := filepath.Join(root, "repo", ".kiro", "hooks", "beacon-endpoint.json")
	for path, value := range map[string]map[string]any{
		userPath:    beaconKiroHookFile([]string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop"}, "Stop"),
		projectPath: beaconKiroHookFile([]string{"SessionStart"}, ""),
	} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 2 || len(manifest.Files) != 2 {
		t.Fatalf("Kiro hook files were not recognized at both scopes: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("ReadSnapshotRecords returned %d Kiro records, want 2", len(records))
	}
	wantByScope := map[string][]string{
		"user":    {"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse"},
		"project": {"SessionStart"},
	}
	for _, record := range records {
		if record.Type != "endpoint_configuration" || record.Scope != "local_pending_review" {
			t.Fatalf("unexpected Kiro record metadata: %#v", record)
		}
		var payload struct {
			Harness             string   `json:"harness"`
			Configured          bool     `json:"beacon_hooks_configured"`
			Events              []string `json:"hook_events"`
			HumanReviewRequired bool     `json:"human_review_required"`
			ExecutionAuthorized bool     `json:"execution_authorized"`
		}
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Harness != "kiro" || !payload.Configured || !payload.HumanReviewRequired || payload.ExecutionAuthorized {
			t.Fatalf("unexpected Kiro projection: %#v", payload)
		}
		scope := "project"
		sourcePath := filepath.ToSlash(record.SourcePath)
		if strings.HasPrefix(sourcePath, "home/") || strings.Contains(sourcePath, "/home/") {
			scope = "user"
		}
		if !reflect.DeepEqual(payload.Events, wantByScope[scope]) {
			t.Fatalf("%s Kiro event projection = %#v, want %#v", scope, payload.Events, wantByScope[scope])
		}
		for _, forbidden := range []string{
			"private-description", "C:\\private", "beacon-hooks", "runtime.jsonl", "beacon-endpoint-telemetry",
		} {
			if strings.Contains(string(record.Payload), forbidden) {
				t.Fatalf("Kiro projection retained %q: %s", forbidden, record.Payload)
			}
		}
	}
}

func TestBeaconKiroHooksSupportsCustomKIROHOMERootOnlyWithMarker(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "hooks", "beacon-endpoint.json")
	data, err := json.Marshal(beaconKiroHookFile([]string{"SessionStart"}, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 {
		t.Fatalf("custom KIRO_HOME was not recognized: %#v", manifest.Summary)
	}

	withoutMarker := beaconKiroHookFile([]string{"SessionStart"}, "")
	hooks := withoutMarker["hooks"].([]any)
	hooks[0].(map[string]any)["action"].(map[string]any)["command"] = "echo unrelated"
	if _, err := normalizeBeaconKiroHooks(withoutMarker, true); !errors.Is(err, errBeaconKiroHooks) {
		t.Fatalf("custom KIRO_HOME without Beacon marker returned %v, want %v", err, errBeaconKiroHooks)
	}
}

func TestBeaconKiroHooksPathsAreBoundedToOwnedFile(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "user file", relative: filepath.Join("home", ".kiro", "hooks", "beacon-endpoint.json"), sourceRoot: `C:\source`, want: true},
		{name: "project file", relative: filepath.Join("repo", ".kiro", "hooks", "beacon-endpoint.json"), sourceRoot: `C:\source`, want: true},
		{name: "selected Kiro home", relative: filepath.Join("hooks", "beacon-endpoint.json"), sourceRoot: `C:\home\.kiro`, want: true},
		{name: "custom KIRO_HOME", relative: filepath.Join("hooks", "beacon-endpoint.json"), sourceRoot: `C:\profiles\kiro-work`, want: true},
		{name: "selected hooks directory", relative: "beacon-endpoint.json", sourceRoot: `C:\repo\.kiro\hooks`, want: true},
		{name: "unrelated config", relative: "settings.json", sourceRoot: `C:\source`, want: false},
		{name: "nested non-Kiro config", relative: filepath.Join("repo", "hooks", "beacon-endpoint.json"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconKiroHooksPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconKiroHooksPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconKiroHooksRejectMalformedOrUnownedDefinitions(t *testing.T) {
	valid := func() map[string]any { return beaconKiroHookFile([]string{"SessionStart"}, "") }
	tests := []struct {
		name  string
		value map[string]any
	}{
		{name: "wrong version", value: map[string]any{"version": "v2", "hooks": []any{}}},
		{name: "unknown root field", value: map[string]any{"version": "v1", "hooks": []any{}, "token": "private"}},
		{name: "hooks is not array", value: map[string]any{"version": "v1", "hooks": map[string]any{}}},
		{name: "unsupported trigger", value: beaconKiroHookFile([]string{"SessionEnd"}, "")},
		{name: "non-Beacon command", value: func() map[string]any {
			root := valid()
			root["hooks"].([]any)[0].(map[string]any)["action"].(map[string]any)["command"] = "echo unrelated"
			return root
		}()},
		{name: "invalid timeout", value: func() map[string]any {
			root := valid()
			root["hooks"].([]any)[0].(map[string]any)["timeout"] = 0
			return root
		}()},
		{name: "missing enabled state", value: func() map[string]any {
			root := valid()
			delete(root["hooks"].([]any)[0].(map[string]any), "enabled")
			return root
		}()},
		{name: "duplicate trigger", value: beaconKiroHookFile([]string{"SessionStart", "SessionStart"}, "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeBeaconKiroHooks(tt.value, false); !errors.Is(err, errBeaconKiroHooks) {
				t.Fatalf("normalizeBeaconKiroHooks returned %v, want %v", err, errBeaconKiroHooks)
			}
		})
	}
}

func beaconKimiConfigTOML(events []string, includeUnownedHook bool) string {
	var config strings.Builder
	config.WriteString("[providers.private]\napi_key = \"do-not-copy-kimi-api-key\"\nmodel = \"do-not-copy-private-model\"\n\n")
	for index, event := range events {
		platformFlag := "--platform kimi"
		if index == len(events)-1 {
			platformFlag = "--platform=kimi"
		}
		timeout := 10
		if event == "UserPromptSubmit" {
			timeout = 30
		} else if event == "Stop" || event == "SessionEnd" {
			timeout = 45
		}
		command := "'C:\\private\\beacon-hooks.exe' " + platformFlag + " --log 'C:\\private\\runtime.jsonl' " + strings.ToLower(event)
		config.WriteString("[[hooks]]\nevent = " + strconv.Quote(event) + "\ncommand = " + strconv.Quote(command) + "\ntimeout = " + strconv.Itoa(timeout) + "\n")
		if index == 0 {
			config.WriteString("matcher = \"private-user-matcher\"\n")
		}
		config.WriteString("\n")
	}
	if includeUnownedHook {
		config.WriteString("[[hooks]]\nevent = \"SessionStart\"\ncommand = \"echo private-user-hook\"\ntimeout = 10\n")
	}
	return config.String()
}

func TestBeaconKimiCodeConfigMigrationProjectsOnlyBeaconHookNames(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "home", ".kimi-code", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(beaconKimiConfigTOML(beaconKimiHookEvents, true)), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Kimi Code config was not recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "endpoint_configuration" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Kimi Code config record: %#v", records)
	}
	var payload struct {
		SourceFormat        string   `json:"source_format"`
		Harness             string   `json:"harness"`
		Configured          bool     `json:"beacon_hooks_configured"`
		Events              []string `json:"hook_events"`
		HumanReviewRequired bool     `json:"human_review_required"`
		ExecutionAuthorized bool     `json:"execution_authorized"`
	}
	if err := json.Unmarshal(records[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SourceFormat != "toml" || payload.Harness != "kimi_code" || !payload.Configured ||
		!payload.HumanReviewRequired || payload.ExecutionAuthorized || !reflect.DeepEqual(payload.Events, beaconKimiHookEvents) {
		t.Fatalf("unexpected Kimi Code projection: %#v", payload)
	}
	for _, forbidden := range []string{
		"do-not-copy-kimi-api-key", "do-not-copy-private-model", "private-user-hook", "private-user-matcher",
		"C:\\private", "beacon-hooks", "api_key", "providers",
	} {
		if strings.Contains(string(records[0].Payload), forbidden) {
			t.Fatalf("Kimi Code projection retained %q: %s", forbidden, records[0].Payload)
		}
	}
}

func TestBeaconKimiCodeConfigSupportsCustomKIMICODEHOMERootWithMarker(t *testing.T) {
	root := t.TempDir()
	config := beaconKimiConfigTOML([]string{"SessionStart"}, false)
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 {
		t.Fatalf("custom KIMI_CODE_HOME config was not recognized: %#v", manifest.Summary)
	}
	if _, err := normalizeBeaconKimiCodeConfig(map[string]any{"hooks": []any{}}, true); !errors.Is(err, errBeaconKimiCodeConfig) {
		t.Fatalf("custom root without a Beacon hook marker returned %v, want %v", err, errBeaconKimiCodeConfig)
	}
}

func TestBeaconKimiCodeConfigPathsAreBoundedToConfigRoots(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{name: "default user config", relative: filepath.Join("home", ".kimi-code", "config.toml"), sourceRoot: `C:\source`, want: true},
		{name: "selected Kimi root", relative: "config.toml", sourceRoot: filepath.Join(`C:\home`, ".kimi-code"), want: true},
		{name: "custom KIMI_CODE_HOME", relative: "config.toml", sourceRoot: `C:\profiles\kimi-work`, want: true},
		{name: "other host config", relative: filepath.Join("repo", ".codex", "config.toml"), sourceRoot: `C:\source`, want: false},
		{name: "unrelated nested config", relative: filepath.Join("repo", "plugins", "config.toml"), sourceRoot: `C:\source`, want: false},
		{name: "wrong file", relative: filepath.Join("home", ".kimi-code", "settings.toml"), sourceRoot: `C:\source`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconKimiCodeConfigPath(tt.relative, tt.sourceRoot); got != tt.want {
				t.Fatalf("isBeaconKimiCodeConfigPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.want)
			}
		})
	}
}

func TestBeaconKimiCodeConfigRejectsMalformedBeaconHooks(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{"hooks": []any{map[string]any{
			"event": "SessionStart", "command": "beacon-hooks --platform kimi session-start", "timeout": int64(10),
		}}}
	}
	tests := []struct {
		name  string
		value map[string]any
	}{
		{name: "hooks not an array", value: map[string]any{"hooks": map[string]any{}}},
		{name: "entry not an object", value: map[string]any{"hooks": []any{"invalid"}}},
		{name: "unknown Beacon event", value: map[string]any{"hooks": []any{map[string]any{
			"event": "FutureEvent", "command": "beacon-hooks --platform kimi future",
		}}}},
		{name: "invalid Beacon timeout", value: map[string]any{"hooks": []any{map[string]any{
			"event": "SessionStart", "command": "beacon-hooks --platform kimi start", "timeout": int64(0),
		}}}},
		{name: "duplicate Beacon event", value: map[string]any{"hooks": []any{
			map[string]any{"event": "SessionStart", "command": "beacon-hooks --platform kimi start"},
			map[string]any{"event": "SessionStart", "command": "beacon-hooks --platform kimi start-again"},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeBeaconKimiCodeConfig(tt.value, false); !errors.Is(err, errBeaconKimiCodeConfig) {
				t.Fatalf("normalizeBeaconKimiCodeConfig returned %v, want %v", err, errBeaconKimiCodeConfig)
			}
		})
	}
	if _, err := normalizeBeaconKimiCodeConfig(valid(), true); err != nil {
		t.Fatalf("valid Kimi Code hook config rejected: %v", err)
	}
}

func beaconMuseHookFile(events []string) map[string]any {
	hooks := make(map[string]any, len(events))
	for _, event := range events {
		command := "'C:\\private\\beacon-hooks.exe' --platform muse --log 'C:\\private\\runtime.jsonl' " + strings.ToLower(event)
		hooks[event] = []any{map[string]any{
			"matcher": "*",
			"hooks": []any{
				map[string]any{
					"type": "command", "command": command, "timeout": 10,
					"statusMessage": "private-status-text",
				},
				map[string]any{"type": "command", "command": "echo private-user-owned-handler"},
			},
		}}
	}
	return map[string]any{
		"description":    "private-description",
		"beacon":         "beacon-managed-muse-hooks:v1",
		"schema_version": 1,
		"hooks":          hooks,
	}
}

func TestBeaconMuseConfigurationMigrationPreservesBothConfigurationHalvesAsEvidence(t *testing.T) {
	root := t.TempDir()
	museDir := filepath.Join(root, "home", ".config", "muse")
	if err := os.MkdirAll(museDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksBytes, err := json.Marshal(beaconMuseHookFile(beaconMuseHookEvents))
	if err != nil {
		t.Fatal(err)
	}
	settingsBytes := []byte(`{"managed_hooks_path":"C:\\Users\\private\\.config\\muse\\beacon-endpoint-hooks.json","apiKey":"do-not-copy-muse-secret","otherSetting":"private-user-setting"}`)
	if err := os.WriteFile(filepath.Join(museDir, "beacon-endpoint-hooks.json"), hooksBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(museDir, "settings.json"), settingsBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 2 || len(manifest.Files) != 2 {
		t.Fatalf("Muse configuration halves were not both recognized: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("ReadSnapshotRecords returned %d Muse records, want 2", len(records))
	}
	foundHooks, foundSettings := false, false
	for _, record := range records {
		if record.Type != "endpoint_configuration" || record.Scope != "local_pending_review" {
			t.Fatalf("unexpected Muse record metadata: %#v", record)
		}
		var payload map[string]any
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		switch payload["schema_version"] {
		case beaconClaudeConfigSchema:
			foundHooks = true
			if payload["harness"] != "muse_code" || payload["source_format"] != "json" ||
				payload["beacon_hooks_configured"] != true || payload["execution_authorized"] != false {
				t.Fatalf("unexpected Muse hook projection: %#v", payload)
			}
			events, ok := payload["hook_events"].([]any)
			wantEvents := make([]any, len(beaconMuseHookEvents))
			for i, event := range beaconMuseHookEvents {
				wantEvents[i] = event
			}
			if !ok || !reflect.DeepEqual(events, wantEvents) {
				t.Fatalf("Muse events = %#v, want %#v", payload["hook_events"], beaconMuseHookEvents)
			}
		case beaconMuseSettingsV1:
			foundSettings = true
			if payload["managed_hooks_path_configured"] != true || payload["target_basename_matches_beacon_file"] != true ||
				payload["effective_registration_verified"] != false || payload["execution_authorized"] != false {
				t.Fatalf("unexpected Muse settings pointer projection: %#v", payload)
			}
		default:
			t.Fatalf("unexpected Muse projection schema: %#v", payload)
		}
		for _, forbidden := range []string{
			"C:\\Users\\private", "C:\\private", "do-not-copy-muse-secret", "private-user-setting",
			"private-description", "private-status-text", "private-user-owned-handler", "beacon-hooks",
			"beacon-managed-muse-hooks:v1", "apiKey",
		} {
			if strings.Contains(string(record.Payload), forbidden) {
				t.Fatalf("Muse projection retained %q: %s", forbidden, record.Payload)
			}
		}
	}
	if !foundHooks || !foundSettings {
		t.Fatalf("Muse projections missing hooks=%v settings=%v", foundHooks, foundSettings)
	}
}

func TestBeaconMuseConfigurationSupportsSelectedXDGRoot(t *testing.T) {
	xdgRoot := t.TempDir()
	museDir := filepath.Join(xdgRoot, "muse")
	if err := os.MkdirAll(museDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksBytes, err := json.Marshal(beaconMuseHookFile([]string{"SessionStart"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(museDir, "beacon-endpoint-hooks.json"), hooksBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(museDir, "settings.json"), []byte(`{"managed_hooks_path":"/custom/xdg/muse/beacon-endpoint-hooks.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, xdgRoot)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 2 {
		t.Fatalf("selected XDG_CONFIG_HOME root was not recognized: %#v", manifest.Summary)
	}
}

func TestBeaconMuseConfigurationPathsDoNotCaptureProjectOrUnrelatedFiles(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		wantHooks, wantSettings    bool
	}{
		{name: "default hooks path", relative: filepath.Join("home", ".config", "muse", "beacon-endpoint-hooks.json"), sourceRoot: `C:\source`, wantHooks: true},
		{name: "default settings path", relative: filepath.Join("home", ".config", "muse", "settings.json"), sourceRoot: `C:\source`, wantSettings: true},
		{name: "selected XDG root hooks", relative: filepath.Join("muse", "beacon-endpoint-hooks.json"), sourceRoot: `C:\xdg`, wantHooks: true},
		{name: "selected Muse config root", relative: "settings.json", sourceRoot: `C:\xdg\muse`, wantSettings: true},
		{name: "project Muse hook path is ignored", relative: filepath.Join("repo", ".muse", "hooks.json"), sourceRoot: `C:\source`},
		{name: "unrelated settings", relative: filepath.Join("repo", "settings.json"), sourceRoot: `C:\source`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBeaconMuseManagedHooksPath(tt.relative, tt.sourceRoot); got != tt.wantHooks {
				t.Fatalf("isBeaconMuseManagedHooksPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.wantHooks)
			}
			if got := isBeaconMuseSettingsPath(tt.relative, tt.sourceRoot); got != tt.wantSettings {
				t.Fatalf("isBeaconMuseSettingsPath(%q, %q) = %v, want %v", tt.relative, tt.sourceRoot, got, tt.wantSettings)
			}
		})
	}
}

func TestBeaconMuseManagedHooksRejectMalformedBeaconFile(t *testing.T) {
	valid := func() map[string]any { return beaconMuseHookFile([]string{"SessionStart"}) }
	withoutMarker := valid()
	withoutMarker["beacon"] = "other-owner"
	wrongVersion := valid()
	wrongVersion["schema_version"] = 2
	badGroup := valid()
	badGroup["hooks"].(map[string]any)["SessionStart"] = "invalid"
	unknownHandlerField := valid()
	unknownHandlerField["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["env"] = map[string]any{"SECRET": "private"}
	unsupportedBeaconEvent := valid()
	unsupportedBeaconEvent["hooks"].(map[string]any)["PreLLMCall"] = []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "beacon-hooks --platform muse pre-llm"}},
	}}
	tests := []struct {
		name  string
		value map[string]any
	}{
		{name: "wrong owner marker", value: withoutMarker},
		{name: "unsupported schema version", value: wrongVersion},
		{name: "missing hooks map", value: map[string]any{"beacon": beaconMuseHooksMarker}},
		{name: "malformed group", value: badGroup},
		{name: "unknown handler key", value: unknownHandlerField},
		{name: "Beacon hook on unsupported event", value: unsupportedBeaconEvent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeBeaconMuseManagedHooks(tt.value); !errors.Is(err, errBeaconMuseHooks) {
				t.Fatalf("normalizeBeaconMuseManagedHooks returned %v, want %v", err, errBeaconMuseHooks)
			}
		})
	}
}

func TestBeaconMuseSettingsProjectionRejectsNonStringManagedPath(t *testing.T) {
	if _, err := normalizeBeaconMuseSettings(map[string]any{"managed_hooks_path": 17}); !errors.Is(err, errBeaconMuseSettings) {
		t.Fatalf("invalid Muse managed_hooks_path returned %v, want %v", err, errBeaconMuseSettings)
	}
}

func TestBeaconCompressedRotationImportsWithinExpansionBudget(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".beacon", "endpoint", "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	event := beacon.NewEvent(beacon.NewEventOptions{Action: "session.end", Harness: beacon.HarnessInfo{Name: "claude"}})
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzipBytes(t, append(line, '\n'))
	if err := os.WriteFile(filepath.Join(logs, "runtime.jsonl.1.gz"), compressed, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 || manifest.Files[0].SourceBytes != int64(len(compressed)) {
		t.Fatalf("compressed rotation was not reconciled against its compressed source: %#v", manifest)
	}
	_, expanded, err := decodeBeaconGzipJSONL("runtime.jsonl.gz", compressed, 64)
	if err == nil || expanded != 65 {
		t.Fatalf("gzip expansion limit was not enforced: expanded=%d err=%v", expanded, err)
	}
}

func TestBeaconCorruptCompressedRotationIsReportedUnsupported(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".beacon", "endpoint", "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	corrupt := gzipBytes(t, []byte(`{"event":"ok"}`))
	corrupt = corrupt[:len(corrupt)-4]
	if err := os.WriteFile(filepath.Join(logs, "inventory_state.jsonl.gz"), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesUnsupported != 1 || len(manifest.Files) != 1 || manifest.Files[0].Status != "unsupported" {
		t.Fatalf("corrupt gzip was not safely reported: %#v", manifest)
	}
}

func TestBeaconLearningSQLiteSnapshotIsTypedReadOnlyAndReconciled(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, ".beacon", "endpoint", "memory.db")
	createBeaconLearningDatabase(t, databasePath, 1)
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	evaluation := beacon.LearningEvaluationV1{
		SchemaVersion: beacon.LearningSchemaVersion, ID: "evaluation-good",
		Project: beacon.LearningProjectV1{ID: "project-1"},
	}
	evaluationJSON, _ := json.Marshal(evaluation)
	candidate := beacon.LearningCandidateV1{
		SchemaVersion: beacon.LearningSchemaVersion, ID: "candidate-good",
		Project: beacon.LearningProjectV1{ID: "project-1"},
	}
	candidateJSON, _ := json.Marshal(candidate)
	secretCandidate := []byte(`{"schema_version":"beacon.learning.v1","id":"candidate-secret","project":{"id":"project-1"},"api_key":"do-not-copy-this-secret"}`)
	memory := beacon.LearningMemoryV1{
		SchemaVersion: beacon.LearningSchemaVersion, ID: "memory-good", CandidateID: "candidate-good",
		Project: beacon.LearningProjectV1{ID: "project-1"},
	}
	memoryJSON, _ := json.Marshal(memory)
	if _, err := db.Exec(`INSERT INTO evaluations VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		evaluation.ID, "project-1", "trace-1", "completed", "2026-01-01", "2026-01-01", "rubric", "test", string(evaluationJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO evaluations VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"evaluation-invalid", "project-1", "trace-2", "completed", "2026-01-01", "2026-01-01", "rubric", "test", "{"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id   string
		json []byte
	}{
		{candidate.ID, candidateJSON}, {"candidate-secret", secretCandidate},
	} {
		if _, err := db.Exec(`INSERT INTO candidates VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.id, "project-1", "candidate", "procedure", "title", "", "", "2026-01-01", "2026-01-01", string(row.json)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO memories VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		memory.ID, "candidate-good", "project-1", "procedure", "title", "2026-01-01", "2026-01-01", "", string(memoryJSON)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := hashFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 1 || manifest.Summary.RecordsFound != 5 || manifest.Summary.RecordsImported != 3 ||
		manifest.Summary.RecordsCredentialBlocked != 1 || manifest.Summary.RecordsRejected != 1 {
		t.Fatalf("Beacon learning-store inspection did not reconcile: %#v", manifest)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	gotTypes := map[string]bool{}
	for _, record := range records {
		gotTypes[record.Type] = true
		if record.Scope != "local_pending_review" || record.SourcePath != ".beacon/endpoint/memory.db" {
			t.Fatalf("Beacon learning record escaped local review/source scope: %#v", record)
		}
		if strings.Contains(string(record.Payload), "do-not-copy-this-secret") {
			t.Fatal("credential-bearing Beacon learning record entered the snapshot")
		}
	}
	for _, kind := range []string{"beacon_learning_evaluation", "beacon_learning_candidate", "beacon_learning_memory"} {
		if !gotTypes[kind] {
			t.Fatalf("snapshot is missing typed Beacon learning record %q", kind)
		}
	}
	after, err := hashFile(databasePath)
	if err != nil || before != after {
		t.Fatalf("migration mutated Beacon memory.db: before=%s after=%s err=%v", before, after, err)
	}
}

func TestBeaconLearningSQLiteSidecarsBlockCachedSnapshot(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, ".beacon", "endpoint", "memory.db")
	createBeaconLearningDatabase(t, databasePath, 1)
	dataRoot := t.TempDir()
	if _, err := CreateSnapshot(Beacon, root, dataRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databasePath+"-wal", []byte("uncheckpointed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSnapshot(Beacon, root, dataRoot); err == nil || !strings.Contains(err.Error(), "sidecars") {
		t.Fatalf("snapshot reused despite SQLite sidecar: %v", err)
	}
}

func TestBeaconLearningSQLiteRejectsUnknownSchemaWithoutGuessing(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".beacon", "endpoint")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	createBeaconLearningDatabase(t, filepath.Join(root, "memory.db"), 2)
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesUnsupported != 1 || manifest.Summary.RecordsImported != 0 || manifest.Files[0].Status != "unsupported" || manifest.Files[0].Path != "memory.db" {
		t.Fatalf("unknown Beacon learning schema was not reported unsupported: %#v", manifest)
	}
}

func TestMancodeAndRRSIFormatsAreRecognizedWithoutSourceCopying(t *testing.T) {
	mancodeRoot := t.TempDir()
	contextDir := filepath.Join(mancodeRoot, ".mancode", "shared", "context")
	if err := os.MkdirAll(contextDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "glossary.json"), []byte(`{"schemaVersion":1,"revision":1,"entries":[{"term":"release","definition":"A human-approved publication.","aliases":[],"sourceTaskRef":null,"confirmedAt":"2026-10-08T10:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mancodeManifest, err := Inspect(Mancode, mancodeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if mancodeManifest.Summary.RecordsImported != 1 || mancodeManifest.Files[0].Status != "ready" {
		t.Fatalf("Mancode manifest = %#v", mancodeManifest)
	}

	rrsiRoot := t.TempDir()
	runsDir := filepath.Join(rrsiRoot, "runs", "coding")
	if err := os.MkdirAll(runsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runsDir, "history.jsonl"), []byte("{\"t\":1,\"variant\":\"A\",\"edit_id\":\"E1\",\"component\":\"prompt\",\"outcome\":\"REJECTED\",\"bundle\":1,\"delta_S\":0.02}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rrsiManifest, err := Inspect(RRSI, rrsiRoot)
	if err != nil {
		t.Fatal(err)
	}
	if rrsiManifest.Summary.RecordsImported != 1 || rrsiManifest.Files[0].Status != "ready" {
		t.Fatalf("RRSI manifest = %#v", rrsiManifest)
	}
}

func TestAnyJevModelBundleSplitsArtifactsAndKeepsCredentialsOutOfSnapshots(t *testing.T) {
	root := t.TempDir()
	bundleDir := filepath.Join(root, "anyjev-heads")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	model := "local-test-model"
	l1Key := "1111111111111111"
	l2Key := "2222222222222222"
	bundle := map[string]any{
		"model": model,
		"artifacts": map[string]any{
			l1Key: map[string]any{"model": model, "question": l1Key, "method": "temperature", "temperature": 1.2, "prior_method": "batch", "prior_strength": 0.75, "n_calib": 8},
		},
		"heads": map[string]any{
			l2Key: map[string]any{"model": model, "question": l2Key, "question_id": "q-head", "kind": "choice", "text": "Choose a safe check", "options": []string{"inspect", "test"},
				"method": "head:ridge", "layer_abs": 2, "n_blocks": 4, "hidden_size": 2, "temperature": 1.1, "n_calib": 8,
				"W": [][]int{{1, 0}, {0, 1}}, "b": []int{0, 0}, "mean": []int{0, 0}, "scale": []int{1, 1}, "params": map[string]any{}, "cv": map[string]any{}},
		},
		"adaptation": map[string]any{l1Key: map[string]any{"sum": []int{1, 2}, "sumsq": []int{1, 4}, "n": 2}},
		"observations": map[string]any{l1Key: map[string]any{
			"question_id": "q-choice", "kind": "choice", "text": "Choose a safe check", "options": []string{"inspect", "test"},
			"states": []string{"safe state", "API_TOKEN=do-not-copy-this"}, "labels": []int{0, 1}, "fits": 0, "next_fit": 30,
		}},
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "model.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(AnyJev, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 4 || manifest.Summary.RecordsImported != 3 || manifest.Summary.RecordsCredentialBlocked != 1 || manifest.Files[0].Status != "partially_excluded" {
		t.Fatalf("AnyJev semantic import counts do not reconcile: %#v files=%#v", manifest.Summary, manifest.Files)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(AnyJev, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(AnyJev), manifest.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("semantic AnyJev snapshot has %d records, want 3", len(records))
	}
	types := map[string]bool{}
	for _, record := range records {
		if strings.Contains(string(record.Payload), "do-not-copy-this") {
			t.Fatalf("credential-bearing labeled observation entered snapshot: %s", record.Payload)
		}
		types[record.Type] = true
		if record.Scope != "local_pending_review" {
			t.Fatalf("AnyJev record escaped local pending-review scope: %#v", record)
		}
	}
	for _, kind := range []string{"decision_calibration_artifact", "decision_adaptation_state"} {
		if !types[kind] {
			t.Fatalf("semantic AnyJev snapshot missing record type %q: %v", kind, types)
		}
	}
	second, err := CreateSnapshot(AnyJev, root, dataRoot)
	if err != nil || second.ID != snapshot.ID {
		t.Fatalf("AnyJev semantic snapshot was not idempotent: %#v err=%v", second, err)
	}
}

func TestAnyJevBuilderBundleImportsSanitizedValidationMetadata(t *testing.T) {
	root := t.TempDir()
	bundleDir := filepath.Join(root, "anyjev-heads")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(anyJevBuilderBundleFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "model.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(AnyJev, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 2 || manifest.Summary.RecordsImported != 2 || manifest.Summary.RecordsRejected != 0 || manifest.Files[0].Status != "ready" {
		t.Fatalf("AnyJev enriched bundle did not reconcile: summary=%#v file=%#v", manifest.Summary, manifest.Files[0])
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(AnyJev, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(AnyJev), manifest.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("snapshot has %d records, want the head plus metadata", len(records))
	}
	var metadata map[string]any
	for _, record := range records {
		if record.Scope != "local_pending_review" {
			t.Fatalf("imported AnyJev bundle record was not staged for review: %#v", record)
		}
		if record.Type == "decision_bundle_metadata" {
			if err := json.Unmarshal(record.Payload, &metadata); err != nil {
				t.Fatal(err)
			}
		}
	}
	if metadata == nil {
		t.Fatal("snapshot omitted the bundle metadata record")
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for _, privateValue := range []string{"private-model-label", "private-question-id", "private-validation-row-key", "private-hardware-id", "2026-09-27T10:11:12"} {
		if strings.Contains(string(encoded), privateValue) {
			t.Fatalf("private source metadata %q was copied into the normalized record", privateValue)
		}
	}
	if metadata["schema_version"] != anyJevBundleMetadataSchema || metadata["artifact_count"] != float64(0) || metadata["head_count"] != float64(1) {
		t.Fatalf("normalized bundle metadata lost its schema or collection counts: %#v", metadata)
	}
	omitted, ok := metadata["omitted_fields"].([]any)
	if !ok || len(omitted) != 5 {
		t.Fatalf("metadata does not declare all privacy omissions: %#v", metadata["omitted_fields"])
	}
	if _, err := os.ReadFile(filepath.Join("..", "..", "schema", "anyjev-bundle-metadata-v1.schema.json")); err != nil {
		t.Fatalf("versioned bundle metadata schema missing: %v", err)
	}
}

func TestAnyJevBuilderBundleRejectsMalformedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "candidate outside model", mutate: func(bundle map[string]any) { bundle["candidates"] = []any{5} }},
		{name: "invalid metric", mutate: func(bundle map[string]any) {
			validation := bundle["validation"].(map[string]any)
			rows := validation["per_question"].(map[string]any)
			row := rows["private-validation-row-key"].(map[string]any)
			row["nll"] = -1.0
		}},
		{name: "unknown producer field", mutate: func(bundle map[string]any) { bundle["unreviewed"] = true }},
		{name: "partial producer metadata", mutate: func(bundle map[string]any) {
			delete(bundle, "n_blocks")
		}},
		{name: "credential-bearing question id", mutate: func(bundle map[string]any) {
			bundle["questions"] = map[string]any{"1234567890abcdef": "TOKEN=ghp_123456789012345678901234567890123456"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := anyJevBuilderBundleFixture()
			test.mutate(bundle)
			if _, err := normalizeAnyJevBundle(bundle); err == nil {
				t.Fatal("malformed or unsafe AnyJev metadata was accepted")
			}
		})
	}
}

func anyJevBuilderBundleFixture() map[string]any {
	model := "private-model-label"
	questionKey := "1234567890abcdef"
	return map[string]any{
		"model":     model,
		"artifacts": map[string]any{},
		"heads": map[string]any{
			questionKey: map[string]any{
				"model": model, "question": questionKey, "question_id": "private-question-id", "kind": "choice",
				"text": "Choose a safe check", "options": []string{"inspect", "test"}, "method": "head:ridge",
				"layer_abs": 4, "n_blocks": 4, "hidden_size": 2, "temperature": 1.1, "n_calib": 8,
				"W": [][]int{{1, 0}, {0, 1}}, "b": []int{0, 0}, "mean": []int{0, 0}, "scale": []int{1, 1},
			},
		},
		"anyjev": "0.1.0", "n_blocks": 4, "hidden_size": 2, "candidates": []any{3, 4},
		"questions": map[string]any{questionKey: "private-question-id"},
		"date":      "2026-09-27T10:11:12", "env": map[string]any{"gpu": "private-hardware-id"},
		"validation": map[string]any{
			"per_question": map[string]any{
				"private-validation-row-key": map[string]any{
					"K": 2, "n_calib": 8, "n_test": 4, "layer_abs": 4, "relative_depth": 1.0,
					"method": "head:ridge", "oof_acc": 0.75, "oof_nll": 0.5, "temperature": 1.0,
					"acc": 0.75, "ece": 0.1, "nll": 0.5, "fit_seconds": 0.2, "ms_per_decision": 0.3,
					"cache_mean_abs_dp": 0.02, "cache_argmax_disagreement": 0.0,
				},
			},
			"summary": map[string]any{
				"typed_pooled": map[string]any{"n": 4, "acc": 0.75, "ece": 0.1, "nll": 0.5},
				"all_pooled":   map[string]any{"n": 4, "acc": 0.75, "ece": 0.1, "nll": 0.5},
				"mean_blocks":  4.0, "mean_ms_per_decision": 0.3, "cache_mean_abs_dp": 0.02,
			},
		},
	}
}

func TestAnyJevBenchmarkResultsAreNotMisclassifiedAsCalibrationArtifacts(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "bench/results/Qwen__Qwen3-8B.json", want: "decision_label_or_evaluation"},
		{path: "bench/results_typed/2026-09-22/model.json", want: "decision_label_or_evaluation"},
		{path: "demo/results/jev_mode_model.json", want: "decision_label_or_evaluation"},
		{path: "labels/questions.csv", want: "decision_label_or_evaluation"},
		{path: "anyjev-heads/Qwen__Qwen3-8B.json", want: "decision_calibration_artifact"},
		{path: "artifacts/question.json", want: "decision_calibration_artifact"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			if got := classifyRecord(AnyJev, test.path); got != test.want {
				t.Fatalf("classifyRecord(AnyJev, %q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestCredentialConfigIsSanitizedAndUnsupportedDataDoesNotEnterSnapshot(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, ".beacon", "endpoint", "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "runtime.jsonl"), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".beacon", "endpoint", "config.yaml"), []byte("api_key: never-copy-this-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesUnsupported != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsCredentialBlocked != 0 {
		t.Fatalf("unsupported file was not reported: %#v", manifest.Summary)
	}
	if len(manifest.Files) != 2 || manifest.Files[0].Status != "ready" || manifest.Files[1].Status != "unsupported" {
		t.Fatalf("unexpected eligible file set: %#v", manifest.Files)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := ReadSnapshotRecords(manifestPath, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || strings.Contains(string(records[0].Payload), "never-copy-this-value") ||
		!strings.Contains(string(records[0].Payload), `"sensitive_values_omitted":true`) {
		t.Fatalf("credential-bearing config was not safely projected: %#v", records)
	}
}

func TestSnapshotRecordMustMatchItsSourceFileContract(t *testing.T) {
	path := ".beacon/endpoint/logs/runtime.jsonl"
	sourceRoot := filepath.Join(t.TempDir(), "home")
	payload := json.RawMessage(`{"action":"session.end"}`)
	record := Record{
		Key: path + "#000001", Type: "telemetry_event", SourcePath: path,
		ContentSHA256: digest(payload), Scope: "local_pending_review", Payload: payload,
	}
	file := FileEntry{Path: path, RecordCount: 1}
	if err := validateSnapshotRecord(record, file, Beacon, sourceRoot); err != nil {
		t.Fatalf("valid snapshot record was rejected: %v", err)
	}
	tampered := []Record{record}
	tampered[0].SourcePath = "other/runtime.jsonl"
	if err := validateSnapshotRecord(tampered[0], file, Beacon, sourceRoot); err == nil {
		t.Fatal("record with a substituted source path was accepted")
	}
	tampered[0] = record
	tampered[0].Type = "project_policy"
	if err := validateSnapshotRecord(tampered[0], file, Beacon, sourceRoot); err == nil {
		t.Fatal("record with a product-incompatible type was accepted")
	}
	tampered[0] = record
	tampered[0].Scope = "team_shared"
	if err := validateSnapshotRecord(tampered[0], file, Beacon, sourceRoot); err == nil {
		t.Fatal("record outside the local review scope was accepted")
	}
}

func TestSnapshotReaderRejectsSymlinkedRecordFile(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".beacon", "endpoint", "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	event := beacon.NewEvent(beacon.NewEventOptions{Action: "session.end", Harness: beacon.HarnessInfo{Name: "codex"}})
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "runtime.jsonl"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	dataRoot := t.TempDir()
	manifest, err := CreateSnapshot(Beacon, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Beacon), manifest.SourceFingerprint, "manifest.json")
	recordPath := filepath.Join(filepath.Dir(manifestPath), filepath.FromSlash(manifest.Files[0].SnapshotPath))
	external := filepath.Join(t.TempDir(), "records.jsonl")
	if err := os.WriteFile(external, []byte("not the snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, recordPath); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := LoadSnapshot(manifestPath); err == nil {
		t.Fatal("snapshot reader accepted a symlinked record file")
	}
}
