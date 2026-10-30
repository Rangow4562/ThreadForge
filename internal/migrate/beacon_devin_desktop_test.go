package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBeaconDevinDesktopHooksPathAllowlist(t *testing.T) {
	tests := []struct {
		name, relative, sourceRoot string
		want                       bool
	}{
		{"documented user path", filepath.Join(".codeium", "windsurf", "hooks.json"), `C:\Users\agent`, true},
		{"documented project path", filepath.Join("repo", ".windsurf", "hooks.json"), `D:\source`, true},
		{"selected Windsurf project config", "hooks.json", filepath.Join(`D:\source`, ".windsurf"), true},
		{"selected user Windsurf config", "hooks.json", filepath.Join(`C:\Users\agent`, ".codeium", "windsurf"), true},
		{"selected Codeium config", filepath.Join("windsurf", "hooks.json"), filepath.Join(`C:\Users\agent`, ".codeium"), true},
		{"Cursor config is distinct", filepath.Join(".cursor", "hooks.json"), `D:\source`, false},
		{"unrelated nested Windsurf name", filepath.Join(".codeium", "other", "hooks.json"), `C:\Users\agent`, false},
		{"wrong basename", filepath.Join(".windsurf", "settings.json"), `D:\source`, false},
		{"arbitrary selected root", "hooks.json", `D:\custom`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isBeaconDevinDesktopHooksPath(test.relative, test.sourceRoot); got != test.want {
				t.Fatalf("recognized=%v want=%v", got, test.want)
			}
		})
	}
}

func TestBeaconDevinDesktopCommandMatcherRequiresPinnedEmitterShape(t *testing.T) {
	valid := beaconDevinDesktopTestCommand("prompt-submit")
	escapedApostrophe := `'C:\Users\O'\''Brien\AppData\beacon-hooks.exe' --platform devin-desktop --log 'C:\Users\O\runtime.jsonl' --config 'C:\Users\O\config.json' prompt-submit`
	if !isBeaconDevinDesktopCommand(valid, "prompt-submit") || !isBeaconDevinDesktopCommand(escapedApostrophe, "prompt-submit") {
		t.Fatal("pinned Beacon command forms were not recognized")
	}
	for name, command := range map[string]string{
		"other executable": strings.Replace(valid, "beacon-hooks", "other-hooks", 1),
		"other platform":   strings.Replace(valid, "devin-desktop", "devin", 1),
		"missing log":      `'beacon-hooks' --platform devin-desktop --config '/private/config' prompt-submit`,
		"wrong action":     strings.TrimSuffix(valid, "prompt-submit") + "post-tool",
		"extra argument":   valid + " --unexpected",
		"shell operator":   valid + "; run-other-command",
		"double quotes":    strings.ReplaceAll(valid, "'", `"`),
	} {
		t.Run(name, func(t *testing.T) {
			if isBeaconDevinDesktopCommand(command, "prompt-submit") {
				t.Fatalf("accepted non-contract command %q", command)
			}
		})
	}
	if isBeaconDevinDesktopCommand(valid, "post-tool") {
		t.Fatal("prompt-submit was accepted for a post-tool event")
	}
}

func TestBeaconDevinDesktopProjectionKeepsOnlyOwnedEvents(t *testing.T) {
	root := map[string]any{
		"api_key": "must-not-be-copied",
		"hooks": map[string]any{
			"pre_user_prompt": []any{
				map[string]any{"command": "custom-prompt-handler", "show_output": true},
				map[string]any{"command": beaconDevinDesktopTestCommand("prompt-submit"), "show_output": false},
			},
			"post_write_code":      []any{map[string]any{"command": beaconDevinDesktopTestCommand("post-tool")}},
			"post_run_command":     []any{map[string]any{"command": beaconDevinDesktopTestCommand("post-tool")}},
			"post_mcp_tool_use":    []any{map[string]any{"command": beaconDevinDesktopTestCommand("post-tool")}},
			"post_read_code":       []any{map[string]any{"command": beaconDevinDesktopTestCommand("prompt-submit")}},
			"private_future_event": []any{map[string]any{"command": beaconDevinDesktopTestCommand("post-tool")}},
		},
	}
	payload, err := normalizeBeaconDevinDesktopHooks(root)
	if err != nil {
		t.Fatal(err)
	}
	var projected map[string]any
	if err := json.Unmarshal(payload, &projected); err != nil {
		t.Fatal(err)
	}
	gotEvents := projected["hook_events"].([]any)
	wantEvents := []any{"post_mcp_tool_use", "post_run_command", "post_write_code", "pre_user_prompt"}
	if !reflect.DeepEqual(gotEvents, wantEvents) || projected["beacon_hooks_configured"] != true ||
		projected["harness"] != "devin_desktop" || projected["human_review_required"] != true || projected["execution_authorized"] != false {
		t.Fatalf("unexpected projection: %s", payload)
	}
	for _, forbidden := range []string{"must-not-be-copied", "beacon-hooks", "runtime.jsonl", "config.json", "custom-prompt-handler", "private_future_event"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("projection retained %q: %s", forbidden, payload)
		}
	}
}

func TestBeaconDevinDesktopProjectionRejectsMalformedHookShapes(t *testing.T) {
	tests := map[string]map[string]any{
		"hooks not object": {"hooks": []any{}},
		"events not array": {"hooks": map[string]any{"pre_user_prompt": "invalid"}},
		"ref not object":   {"hooks": map[string]any{"pre_user_prompt": []any{"invalid"}}},
		"command wrong type": {"hooks": map[string]any{"pre_user_prompt": []any{
			map[string]any{"command": 42},
		}}},
		"show_output wrong type": {"hooks": map[string]any{"pre_user_prompt": []any{
			map[string]any{"command": beaconDevinDesktopTestCommand("prompt-submit"), "show_output": "yes"},
		}}},
	}
	for name, root := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeBeaconDevinDesktopHooks(root); err == nil {
				t.Fatal("malformed Cascade hook configuration was accepted")
			}
		})
	}
}

func TestBeaconDevinDesktopMigrationRoundTripsUserAndProjectConfigs(t *testing.T) {
	root := t.TempDir()
	config := map[string]any{
		"provider_token": "private-provider-token",
		"hooks": map[string]any{
			"pre_user_prompt": []any{map[string]any{
				"command": beaconDevinDesktopTestCommand("prompt-submit"), "show_output": false,
			}},
		},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	relatives := []string{
		filepath.Join(".codeium", "windsurf", "hooks.json"),
		filepath.Join(".windsurf", "hooks.json"),
	}
	for _, relative := range relatives {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesScanned != 2 || manifest.Summary.RecordsImported != 2 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("unexpected Devin Desktop migration summary: %+v", manifest.Summary)
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
		t.Fatalf("read back %d records; want 2", len(records))
	}
	for _, record := range records {
		if record.Type != "endpoint_configuration" || record.Scope != "local_pending_review" {
			t.Fatalf("unexpected imported record: %+v", record)
		}
		for _, forbidden := range []string{"private-provider-token", "beacon-hooks", "runtime.jsonl", "config.json"} {
			if strings.Contains(string(record.Payload), forbidden) {
				t.Fatalf("migration record retained %q: %s", forbidden, record.Payload)
			}
		}
	}
}

func TestBeaconDevinDesktopStrictJSONRejectsDuplicateKeysAndOversize(t *testing.T) {
	relative := filepath.Join(".windsurf", "hooks.json")
	duplicate := []byte(`{"hooks":{"pre_user_prompt":[],"pre_user_prompt":[]}}`)
	if _, err := decodeSourceItems(Beacon, relative, t.TempDir(), duplicate); err == nil {
		t.Fatal("duplicate JSON keys were accepted")
	}
	oversized := []byte(strings.Repeat(" ", maxBeaconDevinDesktopHooksBytes+1))
	if _, err := decodeSourceItems(Beacon, relative, t.TempDir(), oversized); err == nil {
		t.Fatal("oversized config was accepted")
	}
}

func beaconDevinDesktopTestCommand(action string) string {
	return `'/opt/beacon-hooks' --platform devin-desktop --log '/private/runtime.jsonl' --config '/private/config.json' ` + action
}
