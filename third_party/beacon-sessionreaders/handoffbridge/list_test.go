package handoffbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/handoff"
	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/sessionlimits"
)

func TestListUsesNativeCodexStoreReadOnlyAndGroupsRollouts(t *testing.T) {
	root := t.TempDir()
	codexDir := filepath.Join(root, ".codex")
	rolloutDir := filepath.Join(codexDir, "sessions", "2026", "10", "09")
	project := filepath.Join(root, "repo")
	if err := os.MkdirAll(rolloutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(rolloutDir, "rollout-2026-10-09T10-00-00-session-123.jsonl")
	second := filepath.Join(rolloutDir, "rollout-2026-10-09T11-00-00-session-123.jsonl")
	line := `{"timestamp":"2026-10-09T10:00:00Z","type":"session_meta","payload":{"session_id":"session-123","id":"session-123","cwd":"` + filepath.ToSlash(project) + `"}}` + "\n"
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	firstTime := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Minute)
	if err := os.Chtimes(first, firstTime, firstTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(second, secondTime, secondTime); err != nil {
		t.Fatal(err)
	}
	index := `{"id":"session-123","thread_name":"Native Codex task","updated_at":"2026-10-09T11:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(codexDir, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	before := fileDigest(t, second)

	response, err := List(Request{
		SchemaVersion: RequestSchemaVersion,
		Harness:       "codex_cli",
		Directory:     project,
		Limit:         25,
		StoreDirs:     map[string]string{"codex_cli": codexDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != ResponseSchemaVersion || len(response.Sessions) != 1 || len(response.Warnings) != 0 {
		t.Fatalf("unexpected native list response: %#v", response)
	}
	got := response.Sessions[0]
	if got.ID != "session-123" || got.Harness != "codex_cli" || got.Title != "Native Codex task" || filepath.Clean(got.Directory) != filepath.Clean(project) || got.SourcePath != second {
		t.Fatalf("unexpected Codex session metadata: %#v", got)
	}
	if after := fileDigest(t, second); before != after {
		t.Fatal("native discovery modified the Codex source session")
	}
}

func TestReadReturnsMappedEventsWithoutExposingTheStorePath(t *testing.T) {
	root := t.TempDir()
	codexDir := filepath.Join(root, ".codex")
	rolloutDir := filepath.Join(codexDir, "sessions", "2026", "10", "09")
	project := filepath.Join(root, "repo")
	if err := os.MkdirAll(rolloutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rolloutDir, "rollout-2026-10-09T10-00-00-session-read-123.jsonl")
	transcript := `{"timestamp":"2026-10-09T10:00:00Z","type":"session_meta","payload":{"session_id":"session-read-123","id":"session-read-123","cwd":"` + filepath.ToSlash(project) + `"}}` + "\n" +
		`{"timestamp":"2026-10-09T10:00:01Z","type":"response_item","payload":{"type":"message","id":"user-1","role":"user","content":[{"type":"input_text","text":"Continue from the parser tests.\n\n[beacon-handoff from=claude_code session=source-session-1]"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	request := ReadRequest{
		SchemaVersion: ReadRequestSchemaVersion,
		Harness:       handoff.HarnessCodex,
		SessionID:     "session-read-123",
		StoreDirs:     map[string]string{handoff.HarnessCodex: codexDir},
	}
	response, err := Read(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != ReadResponseSchemaVersion || response.Session.ID != request.SessionID || response.Session.Harness != handoff.HarnessCodex || len(response.Events) == 0 {
		t.Fatalf("unexpected read response: %#v", response)
	}
	foundPrompt, foundHandoffClaim := false, false
	for _, event := range response.Events {
		if event.Event.Action == "prompt.submitted" && event.Prompt != nil && strings.HasPrefix(event.Prompt.Text, "Continue from the parser tests.") {
			foundPrompt = true
		}
		if event.Event.Action == "session.handoff" && event.Handoff != nil &&
			event.Handoff.SourceHarness == "claude_code" && event.Handoff.SourceSessionID == "source-session-1" {
			foundHandoffClaim = true
		}
	}
	if !foundPrompt {
		t.Fatalf("native read did not map the user prompt: %#v", response.Events)
	}
	if !foundHandoffClaim {
		t.Fatalf("native read did not preserve the marker-derived session.handoff claim: %#v", response.Events)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "source_path") || strings.Contains(string(encoded), path) {
		t.Fatalf("native read response exposed its source path: %s", encoded)
	}
}

func TestListRejectsUnversionedAndUnboundedRequests(t *testing.T) {
	for _, request := range []Request{
		{SchemaVersion: "wrong", Limit: 1},
		{SchemaVersion: RequestSchemaVersion, Limit: MaxSessions + 1},
		{SchemaVersion: RequestSchemaVersion, Harness: "gemini_cli"},
		{SchemaVersion: RequestSchemaVersion, StoreDirs: map[string]string{"codex_cli": "relative"}},
	} {
		if _, err := List(request); err == nil {
			t.Fatalf("unsafe request was accepted: %#v", request)
		}
	}
}

func TestWarningsExposePathFreeTruncationCode(t *testing.T) {
	got := warnings(&handoff.SourceError{
		Harness: "codex_cli",
		Err:     fmt.Errorf(`C:\Users\private\sessions: %w`, sessionlimits.ErrScanLimit),
	})
	if len(got) != 1 || got[0].Harness != "codex_cli" || got[0].Code != "scan_truncated" {
		t.Fatalf("warnings = %#v, want one path-free scan_truncated warning", got)
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
