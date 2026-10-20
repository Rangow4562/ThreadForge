package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/handoffbridge"
	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/traces"
)

func TestHandoffExportWritesPrivateBoundedBriefAndMetadataReceipt(t *testing.T) {
	root := t.TempDir()
	tracePath := filepath.Join(root, "events.jsonl")
	outputDir := filepath.Join(root, "private-handoffs")
	appendHandoffFixture(t, tracePath,
		handoffFixtureEvent("codex-cli", "session-private-123", "prompt.submitted", "First task request"),
		handoffFixtureEvent("codex-cli", "session-private-123", "agent.message", "Latest visible result"),
	)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"handoff", "export", "session-private-123", "--traces", tracePath, "--output-dir", outputDir, "--json"}, &stdout, &stderr)
	var result handoffExportResult
	if code != 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || result.SchemaVersion != handoffExportSchemaVersion ||
		result.Source != "threadforge_runtime_log" || result.Harness != "codex_cli" || result.SessionID != "session-private-123" ||
		!validLowerSHA256(result.BriefSHA256) || result.BriefBytes <= 0 || result.OutputPath == "" ||
		strings.Contains(stdout.String(), "First task request") || stderr.Len() != 0 {
		t.Fatalf("export code=%d result=%#v stdout=%q stderr=%q", code, result, stdout.String(), stderr.String())
	}
	if filepath.Dir(result.OutputPath) != outputDir {
		t.Fatalf("output path escaped requested directory: %q", result.OutputPath)
	}
	content, err := os.ReadFile(result.OutputPath)
	if err != nil || !strings.Contains(string(content), "First task request") || !strings.Contains(string(content), "Latest visible result") {
		t.Fatalf("brief contents=%q err=%v", content, err)
	}
	if len(content) > 48<<10 {
		t.Fatalf("brief is not bounded: %d bytes", len(content))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(result.OutputPath)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("brief mode=%v err=%v", info.Mode(), err)
		}
	}
}

func TestHandoffExportPrintIsExplicitAndNeverWritesAFile(t *testing.T) {
	root := t.TempDir()
	tracePath := filepath.Join(root, "events.jsonl")
	outputDir := filepath.Join(root, "must-not-exist")
	appendHandoffFixture(t, tracePath, handoffFixtureEvent("claude-code", "print-session", "prompt.submitted", "Visible only on explicit print"))
	var stdout, stderr bytes.Buffer
	code := Run([]string{"handoff", "export", "print-session", "--traces", tracePath, "--output-dir", outputDir, "--print"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "Visible only on explicit print") || stderr.Len() != 0 {
		t.Fatalf("print export code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Lstat(outputDir); !os.IsNotExist(err) {
		t.Fatalf("--print unexpectedly created output directory: %v", err)
	}
}

func TestHandoffExportCanBuildAReadOnlyNativeStoreBrief(t *testing.T) {
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
	path := filepath.Join(rolloutDir, "rollout-2026-10-09T10-00-00-session-native-123.jsonl")
	lines := []string{
		`{"timestamp":"2026-10-09T10:00:00Z","type":"session_meta","payload":{"session_id":"session-native-123","id":"session-native-123","cwd":"` + filepath.ToSlash(project) + `"}}`,
		`{"timestamp":"2026-10-09T10:00:01Z","type":"response_item","payload":{"type":"message","id":"user-1","role":"user","content":[{"type":"input_text","text":"Resume with unit tests."}]}}`,
		`{"timestamp":"2026-10-09T10:00:02Z","type":"response_item","payload":{"type":"message","id":"assistant-1","role":"assistant","content":[{"type":"output_text","text":"I will inspect the tests first."}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(root, "handoffs")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"handoff", "export", "session-native-123", "--source", "native", "--harness", "codex",
		"--store-dir", "codex_cli=" + codexDir, "--output-dir", outputDir, "--print",
	}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "Resume with unit tests.") || !strings.Contains(stdout.String(), "native codex_cli session store") || stderr.Len() != 0 {
		t.Fatalf("native export code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), path) {
		t.Fatalf("native brief leaked its source path: %q", stdout.String())
	}
	if _, err := os.Lstat(outputDir); !os.IsNotExist(err) {
		t.Fatalf("--print unexpectedly wrote a brief file: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"handoff", "export", "session-native-123", "--source", "native", "--harness", "codex",
		"--store-dir", "codex_cli=" + codexDir, "--output-dir", outputDir, "--json",
	}, &stdout, &stderr)
	var receipt handoffExportResult
	if code != 0 || json.Unmarshal(stdout.Bytes(), &receipt) != nil || receipt.Source != "native_session_store" || receipt.Harness != "codex_cli" || receipt.SessionID != "session-native-123" || !validLowerSHA256(receipt.BriefSHA256) || stderr.Len() != 0 {
		t.Fatalf("native receipt code=%d receipt=%#v stdout=%q stderr=%q", code, receipt, stdout.String(), stderr.String())
	}
	content, err := os.ReadFile(receipt.OutputPath)
	if err != nil || !strings.Contains(string(content), "Resume with unit tests.") {
		t.Fatalf("native receipt file content=%q err=%v", content, err)
	}
}

func TestHandoffExportRequiresHarnessForAmbiguousIDsAndRejectsMixedOutputModes(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	appendHandoffFixture(t, tracePath,
		handoffFixtureEvent("codex-cli", "same-id-123", "prompt.submitted", "codex request"),
		handoffFixtureEvent("claude-code", "same-id-123", "prompt.submitted", "claude request"),
	)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"handoff", "export", "same-id-123", "--traces", tracePath, "--print"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "ambiguous") || stdout.Len() != 0 {
		t.Fatalf("ambiguous export code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"handoff", "export", "same-id-123", "--traces", tracePath, "--harness", "codex", "--print", "--json"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "cannot be combined") || stdout.Len() != 0 {
		t.Fatalf("mixed output mode code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"handoff", "export", "same-id-123", "--traces", tracePath, "--harness", "codex", "--print"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "codex request") || strings.Contains(stdout.String(), "claude request") {
		t.Fatalf("harness-qualified export code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestHandoffExportRejectsRelativeOverridesAndDoesNotEchoSecrets(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"handoff", "export", "session", "--traces", "relative.jsonl", "--print"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "absolute local file") || stdout.Len() != 0 {
		t.Fatalf("relative trace override code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"handoff", "export", "session", "--output-dir", "relative-output"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "clean absolute local directory") {
		t.Fatalf("relative output directory code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestHandoffListReadsNativeCodexSessionsAndSanitizesHumanOutput(t *testing.T) {
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
	writeCodexHandoffFixture(t, codexDir, rolloutDir, project)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"handoff", "list", "--harness", "codex", "--store-dir", "codex=" + codexDir, "--json"}, &stdout, &stderr)
	var response handoffbridge.Response
	if code != 0 || json.Unmarshal(stdout.Bytes(), &response) != nil || response.SchemaVersion != handoffbridge.ResponseSchemaVersion ||
		len(response.Sessions) != 1 || len(response.Warnings) != 0 || stderr.Len() != 0 {
		t.Fatalf("native list code=%d response=%#v stdout=%q stderr=%q", code, response, stdout.String(), stderr.String())
	}
	if got := response.Sessions[0]; got.ID != "native-session-123" || got.Harness != "codex_cli" || got.Directory != project || !strings.HasPrefix(got.Title, "Native Codex task") {
		t.Fatalf("native session metadata = %#v", got)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"handoff", "list", "--harness", "codex", "--store-dir", "codex=" + codexDir}, &stdout, &stderr)
	if code != 0 || strings.Contains(stdout.String(), "\x1b") || strings.Count(stdout.String(), "\n") != 2 ||
		!strings.Contains(stdout.String(), "Native Codex task") || stderr.Len() != 0 {
		t.Fatalf("human native list code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func writeCodexHandoffFixture(t *testing.T, codexDir, rolloutDir, project string) {
	t.Helper()
	meta := map[string]any{
		"timestamp": "2026-10-09T10:00:00Z", "type": "session_meta",
		"payload": map[string]any{"session_id": "native-session-123", "id": "native-session-123", "cwd": project},
	}
	line, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(rolloutDir, "rollout-2026-10-09T10-00-00-native-session-123.jsonl")
	if err := os.WriteFile(rollout, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(map[string]string{
		"id": "native-session-123", "thread_name": "Native Codex task\n\x1b[31mInjected", "updated_at": "2026-10-09T10:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "session_index.jsonl"), append(index, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func handoffFixtureEvent(harness, sessionID, action, text string) beacon.Event {
	event := beacon.NewEvent(beacon.NewEventOptions{Action: action, Harness: beacon.HarnessInfo{Name: harness}})
	event.Timestamp = time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	event.Session = &beacon.SessionInfo{ID: sessionID, WorkingDirectory: tSafeWorkspace()}
	switch action {
	case "prompt.submitted":
		event.Prompt = &beacon.PromptInfo{Text: text}
	case "agent.message":
		event.GenAI = &beacon.GenAIInfo{Output: &beacon.GenAIOutputInfo{Messages: beacon.TextOutputMessages(text)}}
	}
	return event
}

func tSafeWorkspace() string {
	return filepath.Clean(filepath.Join(os.TempDir(), "threadforge-handoff-test"))
}

func appendHandoffFixture(t *testing.T, path string, events ...beacon.Event) {
	t.Helper()
	trace, err := traces.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Append(t.Context(), events); err != nil {
		t.Fatal(err)
	}
}
