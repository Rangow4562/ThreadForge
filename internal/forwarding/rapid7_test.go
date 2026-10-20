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

func TestRenderRapid7VectorConfigIsMetadataOnlyAndUsesExternalEnvironment(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private events.jsonl")
	config, err := RenderRapid7VectorConfig(Rapid7ForwardingOptions{LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Rapid7 config is not valid TOML: %v\n%s", err, config)
	}
	source := document["sources"].(map[string]any)["threadforge_runtime"].(map[string]any)
	include := source["include"].([]any)
	if source["read_from"] != "end" || len(include) != 1 || include[0] != logPath {
		t.Fatalf("source must tail selected local log from EOF: %#v", source)
	}
	sink := document["sinks"].(map[string]any)["threadforge_rapid7"].(map[string]any)
	if sink["type"] != "http" || sink["uri"] != "${THREADFORGE_RAPID7_WEBHOOK_URL}" || sink["method"] != "post" {
		t.Fatalf("unexpected Rapid7 HTTP sink config: %#v", sink)
	}
	framing := sink["framing"].(map[string]any)
	if framing["method"] != "newline_delimited" {
		t.Fatalf("Rapid7 sink must preserve one JSON event per line: %#v", framing)
	}
	headers := sink["request"].(map[string]any)["headers"].(map[string]any)
	if headers["Content-Type"] != "application/x-ndjson" {
		t.Fatalf("Rapid7 sink must send NDJSON: %#v", headers)
	}
	transform := document["transforms"].(map[string]any)["threadforge_runtime_metadata"].(map[string]any)
	assertMetadataOnlyProjection(t, transform["source"].(string))
	for _, forbidden := range []string{"secret-rapid7-webhook", "HOSTNAME", "inventory", `"action":`} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("Rapid7 config contains secret material, host identity, or non-allowlisted content %q", forbidden)
		}
	}
}

func TestRenderSyntheticRapid7SampleIsMetadataOnly(t *testing.T) {
	data, err := RenderSyntheticRapid7Sample()
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytesTrimNewline(data), &event); err != nil {
		t.Fatalf("sample is invalid JSON: %v", err)
	}
	if len(event) != 3 || event["severity"] != "info" {
		t.Fatalf("sample contains fields outside the metadata export: %#v", event)
	}
	if _, err := time.Parse(time.RFC3339Nano, event["timestamp"].(string)); err != nil {
		t.Fatalf("sample timestamp is invalid: %v", err)
	}
	marker := event["threadforge_export"].(map[string]any)
	if len(marker) != 2 || marker["schema_version"] != "threadforge.metadata-export.v1" || marker["raw_content"] != false {
		t.Fatalf("sample export marker is invalid: %#v", marker)
	}
}

func TestInstallRapid7PackRefusesOverwriteAndWritesPrivateFiles(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "rapid7-pack")
	options := Rapid7ForwardingOptions{LogPath: filepath.Join(parent, "runtime.jsonl")}
	installed, err := InstallRapid7ForwardingPack(output, options)
	if err != nil {
		t.Fatal(err)
	}
	if installed != output {
		t.Fatalf("installed path = %q, want %q", installed, output)
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "vector.toml"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing pack asset %s: %v", name, err)
		}
	}
	readme, err := os.ReadFile(filepath.Join(output, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"does not install or start Vector", "does not backfill", rapid7WebhookEnvironmentName, "dangerously-allow-env-var-interpolation", "metadata-only"} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("README omits privacy or setup requirement %q", phrase)
		}
	}
	if _, err := InstallRapid7ForwardingPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing output directory should be refused, got %v", err)
	}
}

func TestRenderRapid7RejectsInvalidLogPath(t *testing.T) {
	for _, path := range []string{"", "relative/runtime.jsonl", "runtime\nsecret.jsonl"} {
		t.Run(path, func(t *testing.T) {
			if _, err := RenderRapid7VectorConfig(Rapid7ForwardingOptions{LogPath: path}); err == nil {
				t.Fatal("invalid runtime log path was accepted")
			}
		})
	}
}
