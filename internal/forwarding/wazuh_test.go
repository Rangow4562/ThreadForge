package forwarding

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestRenderWazuhVectorConfigIsLocalAndMetadataOnly(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "private events.jsonl")
	metadataPath := filepath.Join(root, "wazuh-metadata.jsonl")
	config, err := RenderWazuhVectorConfig(WazuhForwardingOptions{LogPath: logPath, MetadataLogPath: metadataPath})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Vector config is not valid TOML: %v\n%s", err, config)
	}
	source := document["sources"].(map[string]any)["threadforge_runtime"].(map[string]any)
	include := source["include"].([]any)
	if source["read_from"] != "end" || len(include) != 1 || include[0] != logPath {
		t.Fatalf("source must tail selected runtime log from EOF: %#v", source)
	}
	transform := document["transforms"].(map[string]any)["threadforge_runtime_metadata"].(map[string]any)
	assertMetadataOnlyProjection(t, transform["source"].(string))
	sink := document["sinks"].(map[string]any)["threadforge_wazuh_metadata"].(map[string]any)
	if sink["type"] != "file" || sink["path"] != metadataPath || sink["inputs"].([]any)[0] != "threadforge_runtime_metadata" {
		t.Fatalf("unexpected local metadata file sink: %#v", sink)
	}
	if sink["encoding"].(map[string]any)["codec"] != "json" || sink["framing"].(map[string]any)["method"] != "newline_delimited" {
		t.Fatalf("Wazuh sink must be newline-delimited JSON: %#v", sink)
	}
	for _, forbidden := range []string{"HOSTNAME", "THREADFORGE_.*TOKEN", "inventory", `"action":`, "prompt", "private events.jsonl\"\nmethod"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("Wazuh config includes credential, raw content, host identity, or unexpected event field %q", forbidden)
		}
	}
}

func TestRenderWazuhLocalfileConfigEscapesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threadforge&metadata<.jsonl")
	config, err := RenderWazuhLocalfileConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Location  string `xml:"location"`
		LogFormat string `xml:"log_format"`
	}
	if err := xml.Unmarshal([]byte(config), &parsed); err != nil {
		t.Fatalf("generated Wazuh localfile XML is invalid: %v\n%s", err, config)
	}
	if parsed.Location != path || parsed.LogFormat != "json" {
		t.Fatalf("Wazuh localfile must read the selected sanitized JSONL file: %#v", parsed)
	}
	if strings.Contains(config, "&<") {
		t.Fatalf("Wazuh XML path was not escaped: %s", config)
	}
}

func TestWazuhRuleContractAndSyntheticSample(t *testing.T) {
	var document struct {
		Rules []struct {
			ID    int `xml:"id,attr"`
			Level int `xml:"level,attr"`
			IfSID int `xml:"if_sid"`
			Field []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:",chardata"`
			} `xml:"field"`
		} `xml:"rule"`
	}
	if err := xml.Unmarshal([]byte(wazuhRulesXML), &document); err != nil {
		t.Fatalf("generated Wazuh rules XML is invalid: %v", err)
	}
	if len(document.Rules) != 7 || document.Rules[0].ID != 119500 {
		t.Fatalf("unexpected Wazuh rule count/base rule: %#v", document.Rules)
	}
	seen := make(map[int]bool)
	for _, rule := range document.Rules {
		if rule.ID < 100000 || rule.ID > 120000 || seen[rule.ID] {
			t.Fatalf("Wazuh custom rule ID is invalid or duplicated: %d", rule.ID)
		}
		seen[rule.ID] = true
	}
	for _, want := range []string{"threadforge.metadata-export.v1", "threadforge_export.raw_content", "severity"} {
		if !strings.Contains(wazuhRulesXML, want) {
			t.Fatalf("Wazuh rules do not constrain events to the metadata contract: %s", want)
		}
	}

	sample, err := RenderSyntheticWazuhSample()
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytesTrimNewline(sample), &event); err != nil {
		t.Fatalf("synthetic Wazuh sample is invalid JSON: %v", err)
	}
	if len(event) != 3 || event["severity"] != "info" {
		t.Fatalf("sample includes fields outside the metadata export: %#v", event)
	}
	if _, err := time.Parse(time.RFC3339Nano, event["timestamp"].(string)); err != nil {
		t.Fatalf("synthetic sample has invalid timestamp: %v", err)
	}
	marker := event["threadforge_export"].(map[string]any)
	if len(marker) != 2 || marker["schema_version"] != "threadforge.metadata-export.v1" || marker["raw_content"] != false {
		t.Fatalf("synthetic sample export marker is invalid: %#v", marker)
	}
}

func TestWazuhDashboardHelperUsesOnlyMetadataAndExternalHTTPSCredentials(t *testing.T) {
	for _, field := range []string{
		"timestamp", "severity", "threadforge_export.schema_version", "threadforge_export.raw_content",
	} {
		if !strings.Contains(wazuhDashboardColumnsScript, field) {
			t.Fatalf("Wazuh dashboard helper is missing metadata field %q", field)
		}
	}
	for _, forbidden := range []string{
		"data.prompt", "data.command", "data.file", "data.session", "SecretPassword",
		"WAZUH_DASHBOARD_PASSWORD", "curl -k", "--insecure", "--location",
	} {
		if strings.Contains(wazuhDashboardColumnsScript, forbidden) {
			t.Fatalf("Wazuh dashboard helper contains forbidden content or unsafe option %q", forbidden)
		}
	}
	for _, required := range []string{
		"WAZUH_DASHBOARD_NETRC", "--netrc-file", "--proto '=https'", "--output /dev/null",
		"without credentials, path, query, or fragment", "must have mode 0600", "! -L",
	} {
		if !strings.Contains(wazuhDashboardColumnsScript, required) {
			t.Fatalf("Wazuh dashboard helper omits safety contract %q", required)
		}
	}
	if shell, err := exec.LookPath("sh"); err == nil {
		path := filepath.Join(t.TempDir(), "apply-dashboard-default-columns.sh")
		if err := os.WriteFile(path, []byte(wazuhDashboardColumnsScript), 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(shell, "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("dashboard helper shell syntax failed: %v: %s", err, output)
		}
	}
}

func TestInstallWazuhPackWritesPrivateFilesAndRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "wazuh-pack")
	options := WazuhForwardingOptions{
		LogPath:         filepath.Join(root, "runtime.jsonl"),
		MetadataLogPath: filepath.Join(root, "threadforge-metadata.jsonl"),
	}
	installed, err := InstallWazuhMetadataPack(output, options)
	if err != nil {
		t.Fatal(err)
	}
	if installed != output {
		t.Fatalf("pack path = %q, want %q", installed, output)
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "threadforge-rules.xml", "ossec-localfile.xml", "vector.toml", "apply-dashboard-default-columns.sh"} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil {
			t.Fatalf("missing Wazuh pack file %s: %v", name, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("pack file %s permissions = %o, want 600", name, info.Mode().Perm())
		}
	}
	readme, err := os.ReadFile(filepath.Join(output, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"does not install", "metadata-only", options.MetadataLogPath, "review that downstream path before activation", "WAZUH_DASHBOARD_NETRC", "four"} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("Wazuh README omits privacy/setup note %q", phrase)
		}
	}
	if _, err := InstallWazuhMetadataPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing pack directory should be refused, got %v", err)
	}
}

func TestRenderWazuhRejectsUnsafeOrAliasedPaths(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "runtime.jsonl")
	for name, options := range map[string]WazuhForwardingOptions{
		"missing-source":    {MetadataLogPath: filepath.Join(root, "metadata.jsonl")},
		"missing-output":    {LogPath: logPath},
		"relative-output":   {LogPath: logPath, MetadataLogPath: "metadata.jsonl"},
		"same-path":         {LogPath: logPath, MetadataLogPath: logPath},
		"control-character": {LogPath: logPath, MetadataLogPath: filepath.Join(root, "metadata\n.jsonl")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RenderWazuhVectorConfig(options); err == nil {
				t.Fatal("invalid Wazuh paths were accepted")
			}
		})
	}
}
