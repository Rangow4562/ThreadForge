package forwarding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/pelletier/go-toml/v2"
)

func TestRenderGCSVectorConfigIsValidLocalAndMetadataOnly(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private events.jsonl")
	config, err := RenderGCSVectorConfig(GCSForwardingOptions{
		LogPath: logPath, Bucket: "agent-audit-store", Prefix: "prod/threadforge",
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated GCS config is not valid TOML: %v\n%s", err, config)
	}
	sources := document["sources"].(map[string]any)
	source := sources["threadforge_runtime"].(map[string]any)
	include := source["include"].([]any)
	if source["read_from"] != "end" || len(include) != 1 || include[0] != logPath {
		t.Fatalf("GCS must read only new events from the selected local runtime log: %#v", source)
	}
	if _, exists := sources["threadforge_inventory"]; exists {
		t.Fatal("GCS config must not read or forward inventory snapshots")
	}
	sinks := document["sinks"].(map[string]any)
	sink := sinks["threadforge_runtime_gcs"].(map[string]any)
	if sink["type"] != "gcp_cloud_storage" || sink["bucket"] != "agent-audit-store" ||
		sink["key_prefix"] != "prod/threadforge/runtime/date=%F/" ||
		sink["filename_extension"] != "jsonl" || sink["content_type"] != "application/x-ndjson" ||
		sink["storage_class"] != "STANDARD" {
		t.Fatalf("unexpected GCS sink configuration: %#v", sink)
	}
	healthcheck := sink["healthcheck"].(map[string]any)
	if healthcheck["enabled"] != false {
		t.Fatalf("GCS sink healthcheck must stay disabled for write-only credentials: %#v", healthcheck)
	}
	transforms := document["transforms"].(map[string]any)
	transform := transforms["threadforge_runtime_metadata"].(map[string]any)
	assertMetadataOnlyProjection(t, transform["source"].(string))
	for _, forbidden := range []string{"GOOGLE_APPLICATION_CREDENTIALS", "private_key", "${", "HOSTNAME", "inventory"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("GCS config contains credentials, host identity, or inventory input %q", forbidden)
		}
	}
}

func TestRenderSyntheticGCSSampleIsValidAndSynthetic(t *testing.T) {
	data, err := RenderSyntheticGCSSample()
	if err != nil {
		t.Fatal(err)
	}
	var event beacon.Event
	if err := json.Unmarshal(bytesTrimNewline(data), &event); err != nil {
		t.Fatalf("sample is invalid JSON: %v", err)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("sample violates Beacon event contract: %v", err)
	}
	if event.Destination == nil || event.Destination.Type != "gcs" ||
		event.Destination.Mode != "google_cloud_storage_jsonl" || event.Destination.Status != "synthetic" ||
		event.User.Name != "" || event.Endpoint.Hostname != "" || event.Prompt != nil || event.Session != nil || event.Message != "" {
		t.Fatalf("sample is not clearly synthetic or contains private data: %#v", event)
	}
}

func TestInstallGCSPackWritesPrivateAssetsAndRefusesOverwrite(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "gcs-pack")
	options := GCSForwardingOptions{LogPath: filepath.Join(parent, "events.jsonl"), Bucket: "agent-audit-store"}
	installed, err := InstallGCSForwardingPack(output, options)
	if err != nil {
		t.Fatal(err)
	}
	if installed != output {
		t.Fatalf("installed path = %q, want %q", installed, output)
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "vector.toml"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing generated asset %s: %v", name, err)
		}
	}
	readme, err := os.ReadFile(filepath.Join(output, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"does not install or start Vector", "does not backfill", "does not export inventory", "write-only object-creator", "threadforge/runtime/"} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("pack README omits privacy/setup statement %q", phrase)
		}
	}
	if !strings.Contains(string(readme), "gs://agent-audit-store/threadforge/smoke-tests/threadforge-synthetic-event.jsonl") {
		t.Fatal("README must show only the explicit synthetic-event upload target")
	}
	marker := filepath.Join(output, "operator-note.txt")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallGCSForwardingPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing output should be preserved and refused, got %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
		t.Fatalf("existing pack was modified: content=%q err=%v", got, err)
	}
}

func TestRenderGCSConfigRejectsInvalidOptions(t *testing.T) {
	valid := GCSForwardingOptions{LogPath: filepath.Join(t.TempDir(), "events.jsonl"), Bucket: "valid-bucket", Prefix: "threadforge"}
	cases := []struct {
		name   string
		change func(*GCSForwardingOptions)
	}{
		{name: "relative log path", change: func(value *GCSForwardingOptions) { value.LogPath = "events.jsonl" }},
		{name: "bucket injection", change: func(value *GCSForwardingOptions) { value.Bucket = "bucket\"\n[sinks.injected]" }},
		{name: "invalid bucket shape", change: func(value *GCSForwardingOptions) { value.Bucket = "127.0.0.1" }},
		{name: "prefix traversal", change: func(value *GCSForwardingOptions) { value.Prefix = "team/../private" }},
		{name: "prefix config injection", change: func(value *GCSForwardingOptions) { value.Prefix = "team\"\n[sinks.injected]" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.change(&options)
			if _, err := RenderGCSVectorConfig(options); err == nil {
				t.Fatal("invalid GCS option was accepted")
			}
		})
	}
}
