package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestCodexCIConfigUsesLocalOTLPAndKeepsCredentialDataOut(t *testing.T) {
	artifactDir := t.TempDir()
	home, err := createCodexHome(artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://127.0.0.1:4318"
	if err := writeCodexConfig(home, endpoint); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := toml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Codex CI config is not valid TOML: %v", err)
	}
	for _, expected := range []string{
		"metrics_exporter = \"none\"", "log_user_prompt = false",
		"endpoint = \"http://127.0.0.1:4318/v1/logs\"", "endpoint = \"http://127.0.0.1:4318/v1/traces\"",
		"protocol = \"json\"",
	} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("Codex CI config missing %q:\n%s", expected, data)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatalf("isolated Codex home contains unexpected credential/config files: %#v", entries)
	}
	if strings.Contains(string(data), "CODEX_API_KEY") || strings.Contains(string(data), "auth.json") {
		t.Fatal("Codex CI config contains credential material")
	}
	if err := writeCodexConfig(home, "https://collector.example/otlp"); err == nil {
		t.Fatal("Codex CI config accepted a non-loopback exporter endpoint")
	}
}

func TestCodexChildEnvironmentUsesPrivateHomeAndPreservesOnlyRuntimeCredentials(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"CODEX_HOME=/user/codex-home",
		"CODEX_API_KEY=codex-api-key-must-remain-child-only",
		"OTEL_EXPORTER_OTLP_ENDPOINT=https://remote.example/v1",
		"OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer-do-not-forward",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=https://remote.example/v1/logs",
		"OTEL_RESOURCE_ATTRIBUTES=private.secret=must-not-export",
		"GITHUB_ACTIONS=true",
		"GITHUB_RUN_ID=codex-run-7",
	}
	values := envMapForTest(codexChildEnvironment(parent, "C:/private/codex-home", "http://127.0.0.1:4318", "C:/private/artifacts"))
	if values["CODEX_HOME"] != "C:/private/codex-home" || values["CODEX_API_KEY"] != "codex-api-key-must-remain-child-only" {
		t.Fatalf("Codex child env did not isolate config or preserve explicit child auth: %#v", values)
	}
	for _, key := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"} {
		if _, exists := values[key]; exists {
			t.Errorf("remote OTLP override %q reached Codex child: %#v", key, values)
		}
	}
	if strings.Contains(values["OTEL_RESOURCE_ATTRIBUTES"], "private.secret") || !strings.Contains(values["OTEL_RESOURCE_ATTRIBUTES"], "beacon.run.run_id=codex-run-7") {
		t.Fatalf("Codex resource attributes were not restricted to allowlisted CI metadata: %#v", values)
	}

	state := SessionState{Harness: "codex_cli", CodexHome: "C:/private/codex-home", StatePath: "C:/private/state.json", ArtifactDir: "C:/private/artifacts", Endpoint: "http://127.0.0.1:4318"}
	exports := codexSessionExports(state, parent)
	if exports["CODEX_HOME"] != state.CodexHome || exports["THREADFORGE_CI_STATE_PATH"] != state.StatePath {
		t.Fatalf("missing Codex session exports: %#v", exports)
	}
	for key, value := range exports {
		if strings.Contains(key, "API_KEY") || strings.Contains(value, "codex-api-key-must-remain-child-only") || strings.Contains(value, "private.secret") {
			t.Fatalf("Codex session export contains private data: %s=%s", key, value)
		}
	}
}

func envMapForTest(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
