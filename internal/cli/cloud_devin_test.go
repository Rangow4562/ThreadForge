package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudDevinPullRequiresReauthorizationWithoutCreatingTraceFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("THREADFORGE_HOME", root)
	t.Setenv("DEVIN_API_KEY", "")
	t.Setenv("DEVIN_ORG_ID", "")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"cloud", "devin", "pull"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "DEVIN_API_KEY and DEVIN_ORG_ID") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "traces")); !os.IsNotExist(err) {
		t.Fatalf("missing-credential pull created trace directory: %v", err)
	}
}

func TestCloudDevinStatusReturnsOnlyAggregateCursorCounts(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state", "devin-cloud.v1.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"schema_version": "threadforge.devin-cloud-state.v1",
		"sessions": map[string]any{
			strings.Repeat("a", 64): map[string]any{
				"updated_at": 100, "synced_at": 0, "status": "working", "done": false,
				"ended_emitted": false, "emitted": map[string]bool{},
			},
		},
	}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THREADFORGE_HOME", root)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"cloud", "devin", "status"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var result struct {
		SchemaVersion string         `json:"schema_version"`
		State         map[string]int `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != "threadforge.devin-cloud-status.v1" || result.State["sessions"] != 1 ||
		result.State["pending"] != 1 || result.State["complete"] != 0 || strings.Contains(stdout.String(), strings.Repeat("a", 64)) {
		t.Fatalf("unsafe or incorrect status: %s", stdout.String())
	}
}

func TestCloudDevinPullValidatesPreviewAndPathOptions(t *testing.T) {
	t.Setenv("DEVIN_API_KEY", "cog_valid")
	t.Setenv("DEVIN_ORG_ID", "org-valid")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"cloud", "devin", "pull", "--print", "--watch"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("accepted preview plus watch: code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"cloud", "devin", "pull", "--state-path", "relative.json"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "must be absolute") {
		t.Fatalf("accepted relative state path: code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"cloud", "devin", "pull", "--help"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "maximum sessions per sweep") {
		t.Fatalf("help code=%d stderr=%q", code, stderr.String())
	}
}
