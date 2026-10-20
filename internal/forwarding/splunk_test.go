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

func TestRenderSplunkVectorConfigIsValidHTTPSAndMetadataOnly(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private events.jsonl")
	secretsDir := filepath.Join(t.TempDir(), "threadforge-secrets")
	config, err := RenderSplunkVectorConfig(SplunkForwardingOptions{
		LogPath: logPath, Endpoint: "https://splunk.example:8088/", Index: "security_ops", SecretsDir: secretsDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Splunk config is not valid TOML: %v\n%s", err, config)
	}
	sources := document["sources"].(map[string]any)
	source := sources["threadforge_runtime"].(map[string]any)
	include := source["include"].([]any)
	if source["read_from"] != "end" || len(include) != 1 || include[0] != logPath {
		t.Fatalf("source must tail selected local log from EOF: %#v", source)
	}
	sinks := document["sinks"].(map[string]any)
	sink := sinks["threadforge_splunk"].(map[string]any)
	if sink["type"] != "splunk_hec_logs" || sink["endpoint"] != "https://splunk.example:8088" ||
		sink["endpoint_target"] != "event" || sink["default_token"] != "SECRET[threadforgesecrets.splunkhectoken]" ||
		sink["index"] != "security_ops" || sink["source"] != "threadforge" ||
		sink["sourcetype"] != "threadforge:metadata:json" {
		t.Fatalf("unexpected Splunk HEC sink config: %#v", sink)
	}
	transforms := document["transforms"].(map[string]any)
	transform := transforms["threadforge_runtime_metadata"].(map[string]any)
	assertMetadataOnlyProjection(t, transform["source"].(string))
	for _, forbidden := range []string{"real-token-value", "Authorization", "HOSTNAME", "inventory", `"action":`} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("Splunk config contains token material, host identity, or non-allowlisted content %q", forbidden)
		}
	}
}

func TestRenderSyntheticSplunkSampleIsValidAndSynthetic(t *testing.T) {
	data, err := RenderSyntheticSplunkSample()
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
	if event.Destination == nil || event.Destination.Type != "splunk_hec" ||
		event.Destination.Mode != "splunk_hec_logs" || event.Destination.Status != "synthetic" ||
		event.User.Name != "" || event.Endpoint.Hostname != "" || event.Prompt != nil || event.Session != nil || event.Message != "" {
		t.Fatalf("sample is not synthetic or contains private data: %#v", event)
	}
}

func TestInstallSplunkPackRefusesOverwriteAndWritesPrivateFiles(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "splunk-pack")
	options := SplunkForwardingOptions{LogPath: filepath.Join(parent, "events.jsonl"), Endpoint: "https://splunk.example:8088", SecretsDir: filepath.Join(parent, "secret-values")}
	installed, err := InstallSplunkForwardingPack(output, options)
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
	for _, phrase := range []string{"does not install or start Vector", "does not backfill", filepath.Join(options.SecretsDir, splunkHECTokenSecretName), "synthetic", "restrict its access, retention"} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("README omits privacy or setup requirement %q", phrase)
		}
	}
	if _, err := InstallSplunkForwardingPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing output directory should be refused, got %v", err)
	}
}

func TestRenderSplunkConfigRejectsInsecureOrMalformedInputs(t *testing.T) {
	valid := SplunkForwardingOptions{LogPath: filepath.Join(t.TempDir(), "events.jsonl"), Endpoint: "https://splunk.example:8088"}
	cases := []struct {
		name   string
		change func(*SplunkForwardingOptions)
	}{
		{name: "relative log path", change: func(value *SplunkForwardingOptions) { value.LogPath = "events.jsonl" }},
		{name: "http endpoint", change: func(value *SplunkForwardingOptions) { value.Endpoint = "http://splunk.example:8088" }},
		{name: "endpoint userinfo", change: func(value *SplunkForwardingOptions) { value.Endpoint = "https://token@splunk.example:8088" }},
		{name: "endpoint path", change: func(value *SplunkForwardingOptions) {
			value.Endpoint = "https://splunk.example:8088/services/collector"
		}},
		{name: "endpoint query", change: func(value *SplunkForwardingOptions) { value.Endpoint = "https://splunk.example:8088?token=secret" }},
		{name: "endpoint fragment", change: func(value *SplunkForwardingOptions) { value.Endpoint = "https://splunk.example:8088#secret" }},
		{name: "empty endpoint fragment", change: func(value *SplunkForwardingOptions) { value.Endpoint = "https://splunk.example:8088#" }},
		{name: "localhost endpoint", change: func(value *SplunkForwardingOptions) { value.Endpoint = "https://localhost:8088" }},
		{name: "loopback endpoint", change: func(value *SplunkForwardingOptions) { value.Endpoint = "https://127.0.0.1:8088" }},
		{name: "dynamic host interpolation", change: func(value *SplunkForwardingOptions) { value.Endpoint = "https://splunk.${HEC_HOST}:8088" }},
		{name: "out-of-range port", change: func(value *SplunkForwardingOptions) { value.Endpoint = "https://splunk.example:99999" }},
		{name: "index config injection", change: func(value *SplunkForwardingOptions) { value.Index = "index\"\n[sinks.injected]" }},
		{name: "uppercase index", change: func(value *SplunkForwardingOptions) { value.Index = "Threadforge" }},
		{name: "index leading hyphen", change: func(value *SplunkForwardingOptions) { value.Index = "-threadforge" }},
		{name: "reserved index text", change: func(value *SplunkForwardingOptions) { value.Index = "threadforge-kvstore" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.change(&options)
			if _, err := RenderSplunkVectorConfig(options); err == nil {
				t.Fatal("unsafe or malformed option was accepted")
			}
		})
	}
}
