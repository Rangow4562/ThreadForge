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

func TestRenderCloudWatchVectorConfigIsValidLocalAndMetadataOnly(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private events.jsonl")
	config, err := RenderCloudWatchVectorConfig(CloudWatchForwardingOptions{
		LogPath: logPath, LogGroup: "/threadforge/runtime", Region: "us-west-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated CloudWatch config is not valid TOML: %v\n%s", err, config)
	}
	sources := document["sources"].(map[string]any)
	source := sources["threadforge_runtime"].(map[string]any)
	include := source["include"].([]any)
	if source["read_from"] != "end" || len(include) != 1 || include[0] != logPath {
		t.Fatalf("source must tail the selected local log from EOF: %#v", source)
	}
	sinks := document["sinks"].(map[string]any)
	sink := sinks["threadforge_cloudwatch"].(map[string]any)
	if sink["type"] != "aws_cloudwatch_logs" || sink["group_name"] != "/threadforge/runtime" ||
		sink["stream_name"] != defaultCloudWatchStreamName || sink["region"] != "us-west-2" ||
		sink["create_missing_group"] != false || sink["create_missing_stream"] != true {
		t.Fatalf("unexpected CloudWatch sink configuration: %#v", sink)
	}
	transforms := document["transforms"].(map[string]any)
	transform := transforms["threadforge_runtime_metadata"].(map[string]any)
	projection := transform["source"].(string)
	assertMetadataOnlyProjection(t, projection)
	for _, forbidden := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "${", "HOSTNAME"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("config contains credential interpolation or host identity %q", forbidden)
		}
	}
}

func TestRenderSyntheticCloudWatchSampleIsValidAndSynthetic(t *testing.T) {
	data, err := RenderSyntheticCloudWatchSample()
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
	if event.Destination == nil || event.Destination.Type != "cloudwatch" ||
		event.Destination.Mode != "aws_cloudwatch_logs" || event.Destination.Status != "synthetic" ||
		event.User.Name != "" || event.Endpoint.Hostname != "" || event.Prompt != nil || event.Session != nil || event.Message != "" {
		t.Fatalf("sample is not clearly synthetic or contains private data: %#v", event)
	}
}

func TestInstallCloudWatchPackRefusesOverwriteAndWritesLocalAssets(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "cloudwatch-pack")
	installed, err := InstallCloudWatchForwardingPack(output, CloudWatchForwardingOptions{
		LogPath: filepath.Join(parent, "events.jsonl"), LogGroup: "/threadforge/runtime",
	})
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
	for _, phrase := range []string{"does not install or start Vector", "does not backfill", "Raw events", "threadforge-metadata", "us-east-1"} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("pack README omits safety/setup statement %q", phrase)
		}
	}
	marker := filepath.Join(output, "operator-note.txt")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCloudWatchForwardingPack(output, CloudWatchForwardingOptions{
		LogPath: filepath.Join(parent, "events.jsonl"), LogGroup: "/threadforge/runtime",
	}); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing output directory should be refused, got %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
		t.Fatalf("existing pack was modified: content=%q err=%v", got, err)
	}
}

func TestRenderCloudWatchConfigRejectsInvalidOptions(t *testing.T) {
	valid := CloudWatchForwardingOptions{
		LogPath: filepath.Join(t.TempDir(), "events.jsonl"), LogGroup: "/threadforge/runtime", Region: "us-east-1",
	}
	cases := []struct {
		name   string
		change func(*CloudWatchForwardingOptions)
	}{
		{name: "relative path", change: func(value *CloudWatchForwardingOptions) { value.LogPath = "events.jsonl" }},
		{name: "empty group", change: func(value *CloudWatchForwardingOptions) { value.LogGroup = "" }},
		{name: "group injection", change: func(value *CloudWatchForwardingOptions) { value.LogGroup = "group\"\n[sinks.injected]" }},
		{name: "region injection", change: func(value *CloudWatchForwardingOptions) { value.Region = "us-east-1\n[sinks.injected]" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.change(&options)
			if _, err := RenderCloudWatchVectorConfig(options); err == nil {
				t.Fatal("invalid option was accepted")
			}
		})
	}
}

func assertMetadataOnlyProjection(t *testing.T, projection string) {
	t.Helper()
	for _, required := range []string{
		`"timestamp": event.timestamp`, `"severity": severity`,
		`"schema_version": "threadforge.metadata-export.v1"`, `"raw_content": false`,
	} {
		if !strings.Contains(projection, required) {
			t.Fatalf("projection is missing allowlisted output %q: %s", required, projection)
		}
	}
	for _, forbidden := range []string{`"action":`, `"harness":`, `"session":`, `"user":`, `"prompt":`, `"attributes":`, `. = event`} {
		if strings.Contains(projection, forbidden) {
			t.Fatalf("projection may leak non-allowlisted data %q: %s", forbidden, projection)
		}
	}
}
