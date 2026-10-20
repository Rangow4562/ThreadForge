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

func TestRenderS3VectorConfigIsValidBoundToLocalLogAndStartsAtEnd(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private events.jsonl")
	config, err := RenderS3VectorConfig(S3ForwardingOptions{
		LogPath: logPath, Bucket: "agent-audit-store", Prefix: "prod/threadforge",
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Vector config is not valid TOML: %v\n%s", err, config)
	}
	sources, ok := document["sources"].(map[string]any)
	if !ok {
		t.Fatalf("missing sources table: %#v", document)
	}
	source, ok := sources["threadforge_runtime"].(map[string]any)
	if !ok || source["read_from"] != "end" {
		t.Fatalf("runtime source must start at the end of the local log: %#v", source)
	}
	include, ok := source["include"].([]any)
	if !ok || len(include) != 1 || include[0] != logPath {
		t.Fatalf("runtime source path did not round-trip safely: %#v", source["include"])
	}
	sinks, ok := document["sinks"].(map[string]any)
	if !ok {
		t.Fatalf("missing sinks table: %#v", document)
	}
	sink, ok := sinks["threadforge_runtime_s3"].(map[string]any)
	if !ok || sink["bucket"] != "agent-audit-store" || sink["region"] != defaultS3Region ||
		sink["key_prefix"] != "prod/threadforge/runtime/date=%F/" {
		t.Fatalf("unexpected S3 sink config: %#v", sink)
	}
	transforms, ok := document["transforms"].(map[string]any)
	if !ok {
		t.Fatalf("missing metadata projection transform: %#v", document)
	}
	transform, ok := transforms["threadforge_runtime_json"].(map[string]any)
	projection, _ := transform["source"].(string)
	if !ok || !strings.Contains(projection, `"timestamp": event.timestamp`) ||
		!strings.Contains(projection, `"severity": severity`) ||
		!strings.Contains(projection, `"schema_version": "threadforge.metadata-export.v1"`) ||
		!strings.Contains(projection, `"raw_content": false`) {
		t.Fatalf("config lacks explicit metadata-only output projection: %s", projection)
	}
	for _, forbidden := range []string{`"action":`, `"harness":`, `"session":`, `"user":`, `"prompt":`, `"attributes":`, `. = event`} {
		if strings.Contains(projection, forbidden) {
			t.Fatalf("projection may forward non-allowlisted event data %q: %s", forbidden, projection)
		}
	}
	for _, forbidden := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "{{LOG_PATH}}", "${"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("config unexpectedly embeds credential, unresolved template, or env interpolation %q", forbidden)
		}
	}
}

func TestRenderSyntheticS3SampleIsValidAndContainsNoHostOrUserData(t *testing.T) {
	data, err := RenderSyntheticS3Sample()
	if err != nil {
		t.Fatal(err)
	}
	var event beacon.Event
	if err := json.Unmarshal(bytesTrimNewline(data), &event); err != nil {
		t.Fatalf("sample event is invalid JSON: %v", err)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("sample event violates Beacon contract: %v", err)
	}
	if event.User.Name != "" || event.Endpoint.Hostname != "" || event.Prompt != nil || event.Session != nil ||
		event.Message != "" || event.Destination == nil || event.Destination.Status != "synthetic" {
		t.Fatalf("sample event contains private data or lacks its synthetic marker: %#v", event)
	}
}

func TestInstallS3ForwardingPackWritesPrivateNewPackAndRefusesOverwrite(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "threadforge-s3-pack")
	options := S3ForwardingOptions{LogPath: filepath.Join(parent, "events.jsonl"), Bucket: "agent-audit-store"}
	installed, err := InstallS3ForwardingPack(output, options)
	if err != nil {
		t.Fatal(err)
	}
	if installed != output {
		t.Fatalf("installed path = %q, want %q", installed, output)
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "vector.toml"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("expected %s in generated pack: %v", name, err)
		}
	}
	readme, err := os.ReadFile(filepath.Join(output, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"does not install or start", "does not backfill", "metadata", "omits raw event", "sample-event.jsonl", "us-east-1"} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("generated README omits forwarding/privacy warning %q", phrase)
		}
	}
	marker := filepath.Join(output, "operator-note.txt")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallS3ForwardingPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing output path should be preserved and rejected, got %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
		t.Fatalf("existing pack was changed: content=%q err=%v", got, err)
	}
}

func TestRenderS3VectorConfigRejectsMalformedDestinationOptions(t *testing.T) {
	valid := S3ForwardingOptions{LogPath: filepath.Join(t.TempDir(), "events.jsonl"), Bucket: "valid-bucket"}
	cases := []struct {
		name   string
		change func(*S3ForwardingOptions)
	}{
		{name: "relative log path", change: func(value *S3ForwardingOptions) { value.LogPath = "events.jsonl" }},
		{name: "bucket config injection", change: func(value *S3ForwardingOptions) { value.Bucket = "bucket\"\n[sources.injected]" }},
		{name: "region control character", change: func(value *S3ForwardingOptions) { value.Region = "us-east-1\n[sinks.injected]" }},
		{name: "prefix traversal", change: func(value *S3ForwardingOptions) { value.Prefix = "team/../private" }},
		{name: "prefix backslash", change: func(value *S3ForwardingOptions) { value.Prefix = `team\private` }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.change(&options)
			if _, err := RenderS3VectorConfig(options); err == nil {
				t.Fatal("malformed destination option was accepted")
			}
		})
	}
}

func bytesTrimNewline(data []byte) []byte {
	return []byte(strings.TrimSuffix(string(data), "\n"))
}
