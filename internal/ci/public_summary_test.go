package ci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublicSessionSummaryIsPathFreeAggregateAndIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, PublicSessionSummaryFileName)
	result := SessionFinishResult{
		SchemaVersion: SessionFinishVersion, Status: "passed",
		ArtifactDir: filepath.Join(root, "private-artifacts"), StatePath: filepath.Join(root, "private-state.json"),
		Stopped: true, RequireTelemetry: true,
		Validation: ValidationResult{
			SchemaVersion: ValidationSchemaVersion, Status: "passed", Harness: "claude_code",
			TotalEvents: 3, MatchedEvents: 2, MinEvents: 1, MalformedRows: 0,
			LogBytes: 1234, GeneratedAt: "2026-10-05T00:00:00Z",
		},
	}
	if err := writePublicSessionSummary(path, result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 9 || decoded["schema_version"] != PublicSessionSummarySchemaVersion ||
		decoded["harness"] != "claude_code" || decoded["status"] != "passed" ||
		decoded["validation_status"] != "passed" || decoded["telemetry_required"] != true ||
		decoded["total_events"] != float64(3) || decoded["matched_events"] != float64(2) ||
		decoded["minimum_events"] != float64(1) || decoded["malformed_rows"] != float64(0) {
		t.Fatalf("unexpected public summary projection: %#v", decoded)
	}
	for _, sensitive := range []string{root, "private-state", "private-artifacts", "generated_at", "log_bytes", "run_id", "repository", "prompt"} {
		if strings.Contains(string(data), sensitive) {
			t.Fatalf("public summary retained %q: %s", sensitive, data)
		}
	}
	if err := writePublicSessionSummary(path, result); err != nil {
		t.Fatalf("identical summary retry should be idempotent: %v", err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePublicSessionSummary(path, result); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected conflicting summary refusal, got %v", err)
	}
}

func TestPublicSessionSummaryRejectsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file-symlink permission is not guaranteed")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte("user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, PublicSessionSummaryFileName)
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("file symlink unavailable: %v", err)
	}
	result := SessionFinishResult{
		Status: "passed", RequireTelemetry: true,
		Validation: ValidationResult{Harness: "claude_code", Status: "passed", MinEvents: 1},
	}
	if err := writePublicSessionSummary(path, result); err == nil {
		t.Fatal("expected symlink target refusal")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "user-owned" {
		t.Fatalf("summary write changed symlink target: %q, %v", data, err)
	}
}
