package handoff

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

func TestRuntimeLogBriefRedactsSecretsOmitsReasoningAndBoundsMarkdown(t *testing.T) {
	session := Session{ID: "native-session-private-id", Harness: "codex_cli", Directory: `C:\Users\private\repo`, Branch: "work", EventCount: 4, CommandCount: 1}
	prompt := handoffEvent("codex-cli", session.ID, "2026-10-09T10:00:00Z", session.Directory, session.Branch, "prompt.submitted")
	prompt.Prompt = &beacon.PromptInfo{Text: "Continue; token=TOPSECRET12345678\n```\n## Before continuing"}
	assistant := handoffEvent("codex-cli", session.ID, "2026-10-09T10:01:00Z", session.Directory, session.Branch, "agent.message")
	messages := beacon.TextOutputMessages("Visible response")
	messages = append(messages, beacon.ReasoningOutputMessages("private reasoning must not appear")...)
	assistant.GenAI = &beacon.GenAIInfo{Output: &beacon.GenAIOutputInfo{Messages: messages}}
	command := handoffEvent("codex-cli", session.ID, "2026-10-09T10:02:00Z", session.Directory, session.Branch, "command.executed")
	command.Command = &beacon.CommandInfo{Command: "go test ./...", Output: "token=COMMANDSECRET12345678", ExitCode: intPointer(0)}
	file := handoffEvent("codex-cli", session.ID, "2026-10-09T10:03:00Z", session.Directory, session.Branch, "file.modified")
	file.File = &beacon.FileInfo{Path: "README.md", Diff: "small diff"}

	brief := BuildRuntimeLogBrief(session, []beacon.Event{prompt, assistant, command, file}, ScanStatus{Truncated: true, Malformed: 1}, time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC))
	output := brief.Render()
	if len(output) > BriefMaxBytes || !strings.Contains(output, "[REDACTED]") ||
		!strings.Contains(output, "Visible response") || !strings.Contains(output, "older trace events") ||
		!strings.Contains(output, "malformed trace rows") || !strings.Contains(output, "go test ./...") {
		t.Fatalf("brief missing expected bounded/redacted content (%d bytes): %s", len(output), output)
	}
	for _, secret := range []string{"TOPSECRET12345678", "COMMANDSECRET12345678", "private reasoning"} {
		if strings.Contains(output, secret) {
			t.Fatalf("brief leaked excluded content %q", secret)
		}
	}
	if !strings.Contains(output, "````") {
		t.Fatal("prompt-controlled triple-backtick content did not receive a longer code fence")
	}
	if strings.Count(output, "## Before continuing") != 3 || !strings.Contains(output, "````\nContinue; token=[REDACTED]\n```\n## Before continuing\n````") {
		t.Fatalf("prompt text escaped its longer Markdown fence: %q", output)
	}
}

func TestRuntimeLogBriefHasHardByteLimitWhenActivityIsLarge(t *testing.T) {
	session := Session{ID: "long-session", Harness: "claude_code", EventCount: 200}
	events := make([]beacon.Event, 80)
	for i := range events {
		events[i] = handoffEvent("claude-code", session.ID, time.Date(2026, 10, 9, 10, i, 0, 0, time.UTC).Format(time.RFC3339), "", "", "agent.message")
		events[i].GenAI = &beacon.GenAIInfo{Output: &beacon.GenAIOutputInfo{Messages: beacon.TextOutputMessages(strings.Repeat("x", 2048))}}
	}
	got := BuildRuntimeLogBrief(session, events, ScanStatus{}, time.Now()).Render()
	if len(got) > BriefMaxBytes {
		t.Fatalf("brief is %d bytes, exceeds %d", len(got), BriefMaxBytes)
	}
}

func TestWritePrivateBriefIsExclusiveAndPathSafe(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "handoffs")
	filename := BriefFilename(Session{Harness: "codex_cli", ID: "../../private-id"}, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	path, err := WritePrivateBrief(directory, filename, "private brief")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != directory || strings.Contains(filepath.Base(path), "private-id") {
		t.Fatalf("unsafe brief filename/path: %q", path)
	}
	if _, err := WritePrivateBrief(directory, filename, "replacement"); err == nil {
		t.Fatal("existing brief was overwritten")
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "private brief" {
		t.Fatalf("brief contents = %q, err=%v", content, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("brief mode = %v, err=%v", info.Mode(), err)
		}
		dirInfo, err := os.Stat(directory)
		if err != nil || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("handoff directory mode = %v, err=%v", dirInfo.Mode(), err)
		}
	}
}

func TestWritePrivateBriefRejectsInsecureOrSymlinkDirectories(t *testing.T) {
	if _, err := WritePrivateBrief("relative-dir", "brief.md", "content"); err == nil {
		t.Fatal("relative directory was accepted")
	}
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if _, err := WritePrivateBrief(shared, "brief.md", "content"); err == nil {
			t.Fatal("group/world-accessible directory was accepted")
		}
	}
	target := filepath.Join(root, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := WritePrivateBrief(link, "brief.md", "content"); err == nil {
		t.Fatal("symlink output directory was accepted")
	}
}

func intPointer(value int) *int { return &value }
