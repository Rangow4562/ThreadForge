package handoff

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

func TestListAggregatesOrdersAndFiltersCapturedSessions(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "repo")
	inside := filepath.Join(project, "nested")
	outside := filepath.Join(root, "repo-other")
	tracePath := filepath.Join(root, "events.jsonl")
	writeTraceEvents(t, tracePath,
		handoffEvent("claude-code", "claude-session", "2026-10-09T10:00:00Z", project, "main", "prompt.submitted"),
		handoffEvent("claude-code", "claude-session", "2026-10-09T10:01:00Z", project, "main", "tool.invoked"),
		handoffEvent("codex-cli", "codex-session", "2026-10-09T10:02:00Z", inside, "feature", "command.executed"),
		handoffEvent("codex-cli", "outside-session", "2026-10-09T10:03:00Z", outside, "main", "prompt.submitted"),
	)

	listing, err := List(tracePath, Filter{Directory: project})
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Sessions) != 2 || listing.Sessions[0].ID != "codex-session" || listing.Sessions[1].ID != "claude-session" {
		t.Fatalf("unexpected ordered sessions: %#v", listing.Sessions)
	}
	claude := listing.Sessions[1]
	if claude.EventCount != 2 || claude.PromptCount != 1 || claude.ToolEventCount != 1 || claude.DirectoryConflict || claude.BranchConflict {
		t.Fatalf("unexpected aggregate: %#v", claude)
	}
	if strings.Contains(mustJSON(t, listing), "outside-session") {
		t.Fatal("directory-prefix filter included a sibling directory")
	}

	limited, err := List(tracePath, Filter{Harness: "codex", Directory: project, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Sessions) != 1 || limited.Sessions[0].ID != "codex-session" {
		t.Fatalf("harness/limit filter returned %#v", limited.Sessions)
	}
}

func TestListKeepsDirectoryAndBranchConflictsSticky(t *testing.T) {
	root := t.TempDir()
	tracePath := filepath.Join(root, "events.jsonl")
	writeTraceEvents(t, tracePath,
		handoffEvent("codex-cli", "session-conflict", "2026-10-09T10:00:00Z", filepath.Join(root, "a"), "main", "prompt.submitted"),
		handoffEvent("codex-cli", "session-conflict", "2026-10-09T10:01:00Z", filepath.Join(root, "b"), "feature", "tool.invoked"),
		handoffEvent("codex-cli", "session-conflict", "2026-10-09T10:02:00Z", filepath.Join(root, "c"), "release", "tool.completed"),
	)

	listing, err := List(tracePath, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	got := listing.Sessions[0]
	if got.Directory != "" || !got.DirectoryConflict || got.Branch != "" || !got.BranchConflict {
		t.Fatalf("conflicting metadata was reintroduced: %#v", got)
	}
}

func TestReadSessionSupportsExactAndUniquePrefixAndReportsAmbiguity(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	writeTraceEvents(t, tracePath,
		handoffEvent("codex-cli", "same-id-123", "2026-10-09T10:00:00Z", "", "", "prompt.submitted"),
		handoffEvent("claude-code", "same-id-123", "2026-10-09T10:01:00Z", "", "", "tool.invoked"),
		handoffEvent("codex-cli", "unique-id-456", "2026-10-09T10:02:00Z", "", "", "command.executed"),
	)

	if _, _, _, err := ReadSession(tracePath, "same-id-123", ""); !errors.Is(err, ErrSessionAmbiguous) {
		t.Fatalf("duplicate cross-harness ID error = %v, want ambiguity", err)
	}
	got, events, _, err := ReadSession(tracePath, "same-id-123", "codex")
	if err != nil || got.Harness != "codex_cli" || got.EventCount != 1 || len(events) != 1 {
		t.Fatalf("exact harness-qualified read = %#v, %#v, %v", got, events, err)
	}
	got, _, _, err = ReadSession(tracePath, "unique-", "")
	if err != nil || got.ID != "unique-id-456" {
		t.Fatalf("unique-prefix read = %#v, %v", got, err)
	}
}

func TestReadSessionRejectsShortPrefixAndReportsMissing(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	writeTraceEvents(t, tracePath, handoffEvent("codex-cli", "session-123", "2026-10-09T10:00:00Z", "", "", "prompt.submitted"))
	if _, _, _, err := ReadSession(tracePath, "sess", ""); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("short non-exact id error = %v, want not found", err)
	}
	if _, _, _, err := ReadSession(tracePath, "absent-session", ""); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("missing id error = %v, want not found", err)
	}
	if _, _, _, err := ReadSession(tracePath, "bad\nid", ""); err == nil {
		t.Fatal("control character in session id was accepted")
	}
}

func TestScanTraceMarksMissingAndMalformedRows(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	listing, err := List(missing, Filter{})
	if err != nil || !listing.Scan.TraceMissing || len(listing.Sessions) != 0 {
		t.Fatalf("missing trace result = %#v, %v", listing, err)
	}

	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	valid, err := json.Marshal(handoffEvent("codex-cli", "session-123", "2026-10-09T10:00:00Z", "", "", "prompt.submitted"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePath, append(append([]byte("{broken}\n"), valid...), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	listing, err = List(tracePath, Filter{})
	if err != nil || listing.Scan.Malformed != 1 || len(listing.Sessions) != 1 {
		t.Fatalf("malformed trace result = %#v, %v", listing, err)
	}
}

func TestScanTraceRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.jsonl")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if _, err := List(link, Filter{}); err == nil {
		t.Fatal("symlink trace path was accepted")
	}
}

func handoffEvent(harness, id, timestamp, directory, branch, action string) beacon.Event {
	event := beacon.NewEvent(beacon.NewEventOptions{
		Action:  action,
		Harness: beacon.HarnessInfo{Name: harness},
	})
	event.Timestamp = timestamp
	event.Session = &beacon.SessionInfo{ID: id, WorkingDirectory: directory}
	event.Branch = branch
	return event
}

func writeTraceEvents(t *testing.T, path string, events ...beacon.Event) {
	t.Helper()
	var contents []byte
	for _, event := range events {
		if err := event.Validate(); err != nil {
			t.Fatalf("invalid test event: %v", err)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		contents = append(contents, encoded...)
		contents = append(contents, '\n')
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
