package ci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

func TestValidateLogCountsOnlyValidMatchingHarnessEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.jsonl")
	claude := beacon.NewEvent(beacon.NewEventOptions{Action: "session.started", Harness: beacon.HarnessInfo{Name: "claude-code"}})
	codex := beacon.NewEvent(beacon.NewEventOptions{Action: "session.started", Harness: beacon.HarnessInfo{Name: "codex_cli"}})
	writeValidationEvents(t, path, claude, codex)

	result, err := ValidateLog(ValidationOptions{LogPath: path, Harness: "claude", MinEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "passed" || result.TotalEvents != 2 || result.MatchedEvents != 1 || result.MalformedRows != 0 {
		t.Fatalf("unexpected validation: %#v", result)
	}
}

func TestValidateLogFailsOnMalformedRowsAndInsufficientHarnessEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.jsonl")
	event := beacon.NewEvent(beacon.NewEventOptions{Action: "session.started", Harness: beacon.HarnessInfo{Name: "codex_cli"}})
	writeValidationEvents(t, path, event)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("not-json\n")
	_ = f.Close()

	result, err := ValidateLog(ValidationOptions{LogPath: path, Harness: "claude-code", MinEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || result.TotalEvents != 1 || result.MatchedEvents != 0 || result.MalformedRows != 1 {
		t.Fatalf("unexpected failed validation: %#v", result)
	}
}

func TestValidateLogRejectsSymlinksOversizeAndUnknownHarness(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.jsonl")
	oversized, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := oversized.Truncate(MaxValidationLogBytes + 1); err != nil {
		_ = oversized.Close()
		t.Fatal(err)
	}
	if err := oversized.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateLog(ValidationOptions{LogPath: target}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized log error=%v", err)
	}
	link := filepath.Join(dir, "linked.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if _, err := ValidateLog(ValidationOptions{LogPath: link}); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("symlink log error=%v", err)
	}
	if _, err := ValidateLog(ValidationOptions{LogPath: target, Harness: "unknown-agent"}); err == nil {
		t.Fatal("expected unsupported harness error")
	}
}

func writeValidationEvents(t *testing.T, path string, events ...beacon.Event) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
