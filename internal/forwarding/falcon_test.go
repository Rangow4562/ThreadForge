package forwarding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestRenderFalconHECVectorConfigIsHTTPSCredentialFreeAndMetadataOnly(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private events.jsonl")
	config, err := RenderFalconHECVectorConfig(FalconHECOptions{
		LogPath: logPath, Endpoint: "https://tenant.ingest.us-2.crowdstrike.com/services/collector", Repository: "team_repo", SecretsDir: filepath.Join(t.TempDir(), "secret-values"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Falcon config is not valid TOML: %v\n%s", err, config)
	}
	sources := document["sources"].(map[string]any)
	source := sources["threadforge_runtime"].(map[string]any)
	include := source["include"].([]any)
	if source["read_from"] != "end" || len(include) != 1 || include[0] != logPath {
		t.Fatalf("Falcon source must read only new local runtime events: %#v", source)
	}
	if _, exists := sources["threadforge_inventory"]; exists {
		t.Fatal("Falcon forwarding must not read inventory snapshots")
	}
	sinks := document["sinks"].(map[string]any)
	sink := sinks["threadforge_falcon_hec"].(map[string]any)
	if sink["type"] != "http" || sink["uri"] != "https://tenant.ingest.us-2.crowdstrike.com/services/collector" || sink["method"] != "post" {
		t.Fatalf("unexpected Falcon HTTP sink: %#v", sink)
	}
	headers := sink["request"].(map[string]any)["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer SECRET[threadforgesecrets.falconhectoken]" || headers["Content-Type"] != "application/json" {
		t.Fatalf("Falcon HTTP headers must use only the external token reference: %#v", headers)
	}
	transforms := document["transforms"].(map[string]any)
	transform := transforms["threadforge_falcon_hec_envelope"].(map[string]any)
	projection := transform["source"].(string)
	assertMetadataOnlyProjection(t, projection)
	for _, required := range []string{`"time": to_unix_timestamp(event_time)`, `"source": "threadforge-endpoint-agent"`, `"sourcetype": "threadforge:metadata:json"`, `payload.index = repository`} {
		if !strings.Contains(projection, required) {
			t.Fatalf("Falcon HEC envelope misses required mapping %q: %s", required, projection)
		}
	}
	for _, forbidden := range []string{"HOSTNAME", "inventory", "THREADFORGE_FALCON_HEC_TOKEN\" =", `"host":`, `"action":`} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("Falcon config includes host identity, credentials, inventory, or raw fields %q", forbidden)
		}
	}
}

func TestRenderSyntheticFalconHECSampleMatchesVersionedEnvelope(t *testing.T) {
	data, err := RenderSyntheticFalconHECSample("team_repo")
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("Falcon sample is invalid JSON: %v", err)
	}
	event, ok := envelope["event"].(map[string]any)
	if !ok || envelope["time"] == nil || envelope["source"] != falconSource ||
		envelope["sourcetype"] != falconSourcetype || envelope["index"] != "team_repo" {
		t.Fatalf("synthetic Falcon envelope is incomplete: %#v", envelope)
	}
	if len(envelope) != 5 || len(event) != 3 || event["severity"] != "info" ||
		event["timestamp"] == nil || event["prompt"] != nil || event["host"] != nil {
		t.Fatalf("Falcon sample includes unexpected fields: envelope=%#v event=%#v", envelope, event)
	}
	if marker, ok := event["threadforge_export"].(map[string]any); !ok ||
		marker["schema_version"] != "threadforge.metadata-export.v1" || marker["raw_content"] != false {
		t.Fatalf("Falcon sample misses the metadata export marker: %#v", event)
	}
}

func TestInstallFalconHECPackWritesSyntheticSampleAndRefusesOverwrite(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "falcon-pack")
	options := FalconHECOptions{
		LogPath:    filepath.Join(parent, "events.jsonl"),
		Endpoint:   "https://tenant.ingest.us-2.crowdstrike.com/api/v1/ingest/hec",
		SecretsDir: filepath.Join(parent, "secret-values"),
	}
	installed, err := InstallFalconHECForwardingPack(output, options)
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
	for _, phrase := range []string{"does not install or start Vector", "does not backfill", filepath.Join(options.SecretsDir, falconTokenSecretName), "sample-event.jsonl", "never opens or stores the token"} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("Falcon pack README omits privacy/setup guidance %q", phrase)
		}
	}
	if !strings.Contains(string(readme), "curl --fail-with-body") || !strings.Contains(string(readme), "THREADFORGE_FALCON_CURL_CONFIG") ||
		!strings.Contains(string(readme), "--data-binary @sample-event.jsonl") || strings.Contains(string(readme), "@runtime.jsonl") {
		t.Fatal("manual Falcon smoke test must target the synthetic sample only")
	}
	if _, err := InstallFalconHECForwardingPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing pack path must be preserved and refused, got %v", err)
	}
}

func TestRenderFalconHECConfigRejectsUnsafeEndpointsAndRepository(t *testing.T) {
	valid := FalconHECOptions{
		LogPath:  filepath.Join(t.TempDir(), "events.jsonl"),
		Endpoint: "https://tenant.ingest.us-2.crowdstrike.com/services/collector",
	}
	cases := []struct {
		name   string
		change func(*FalconHECOptions)
	}{
		{name: "relative log path", change: func(value *FalconHECOptions) { value.LogPath = "events.jsonl" }},
		{name: "http endpoint", change: func(value *FalconHECOptions) {
			value.Endpoint = "http://tenant.ingest.us-2.crowdstrike.com/services/collector"
		}},
		{name: "endpoint userinfo", change: func(value *FalconHECOptions) {
			value.Endpoint = "https://token@tenant.ingest.us-2.crowdstrike.com/services/collector"
		}},
		{name: "endpoint query", change: func(value *FalconHECOptions) { value.Endpoint += "?token=secret" }},
		{name: "endpoint fragment", change: func(value *FalconHECOptions) { value.Endpoint += "#secret" }},
		{name: "unsupported endpoint path", change: func(value *FalconHECOptions) { value.Endpoint = "https://tenant.example/custom" }},
		{name: "loopback endpoint", change: func(value *FalconHECOptions) { value.Endpoint = "https://127.0.0.1/services/collector" }},
		{name: "bad port", change: func(value *FalconHECOptions) { value.Endpoint = "https://tenant.example:99999/services/collector" }},
		{name: "repository config injection", change: func(value *FalconHECOptions) { value.Repository = "repo\"\n[sinks.injected]" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.change(&options)
			if _, err := RenderFalconHECVectorConfig(options); err == nil {
				t.Fatal("unsafe or malformed Falcon option was accepted")
			}
		})
	}
}
