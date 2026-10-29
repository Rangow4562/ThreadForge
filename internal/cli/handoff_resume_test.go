package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/handoffbridge"
	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	handoffpkg "github.com/threadforge-ai/threadforge/internal/handoff"
)

func stubResumeRuntime(t *testing.T) {
	t.Helper()
	previous := handoffResumeLookPath
	handoffResumeLookPath = func(name string) (string, error) { return filepath.Join("/fixture/bin", name), nil }
	t.Cleanup(func() { handoffResumeLookPath = previous })
}

func resumeTraceFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	tracePath := filepath.Join(root, "events.jsonl")
	event := handoffFixtureEvent("codex-cli", "resume-source-123", "prompt.submitted", "PRIVATE_SOURCE_PROMPT")
	event.Session.WorkingDirectory = root
	appendHandoffFixture(t, tracePath, event)
	return tracePath, root
}

func TestHandoffResumePrintsBoundedCrossHarnessPlanWithoutWriting(t *testing.T) {
	stubResumeRuntime(t)
	tracePath, workspace := resumeTraceFixture(t)
	outputDir := filepath.Join(t.TempDir(), "briefs-not-created")
	var stdout, stderr bytes.Buffer
	code := RunWithInput([]string{"handoff", "resume", "resume-source-123", "--source", "runtime-log", "--harness", "codex", "--agent", "gemini", "--traces", tracePath, "--output-dir", outputDir, "--print", "--json"}, strings.NewReader(""), &stdout, &stderr)
	var plan resumePlan
	if code != 0 || json.Unmarshal(stdout.Bytes(), &plan) != nil || stderr.Len() != 0 {
		t.Fatalf("plan code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if plan.SchemaVersion != handoffResumePlanSchema || plan.Mode != "new_session" || plan.Reason != "runtime_log_only" ||
		plan.Target != "gemini_cli" || plan.Executable != filepath.Join("/fixture/bin", "gemini") || plan.Directory != workspace || plan.BriefPath == "" {
		t.Fatalf("unexpected resume plan: %#v", plan)
	}
	wantPrefix := []string{"--approval-mode", "default", "--prompt-interactive"}
	if !reflect.DeepEqual(plan.Args[:3], wantPrefix) || !strings.Contains(plan.Args[3], plan.BriefPath) ||
		!strings.Contains(plan.Args[3], "[beacon-handoff from=codex_cli session=resume-source-123]") || strings.Contains(plan.Args[3], "PRIVATE_SOURCE_PROMPT") {
		t.Fatalf("unsafe or incomplete continuation prompt: %#v", plan.Args)
	}
	if _, err := os.Lstat(outputDir); !os.IsNotExist(err) {
		t.Fatalf("--print created the brief directory: %v", err)
	}
}

func TestHandoffResumeNativeCodexPlanKeepsSourcePathOutOfJSON(t *testing.T) {
	stubResumeRuntime(t)
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
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-10-09T10-00-00-native-session-123.jsonl")
	var stdout, stderr bytes.Buffer
	code := RunWithInput([]string{"handoff", "resume", "native-session-123", "--harness", "codex", "--store-dir", "codex=" + codexDir, "--print", "--json"}, strings.NewReader(""), &stdout, &stderr)
	var plan resumePlan
	if code != 0 || json.Unmarshal(stdout.Bytes(), &plan) != nil || stderr.Len() != 0 {
		t.Fatalf("native plan code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if plan.Mode != "native" || plan.Target != "codex_cli" || !reflect.DeepEqual(plan.Args, []string{"resume", "native-session-123"}) ||
		strings.Contains(stdout.String(), rolloutPath) || strings.Contains(stdout.String(), "resume_path") {
		t.Fatalf("native plan leaked source identity or selected wrong mode: plan=%#v output=%q", plan, stdout.String())
	}
}

func TestHandoffResumeRequiresConfirmationBeforeBriefOrLaunch(t *testing.T) {
	stubResumeRuntime(t)
	tracePath, _ := resumeTraceFixture(t)
	outputDir := filepath.Join(t.TempDir(), "must-not-exist")
	previous := handoffResumeLaunch
	launched := false
	handoffResumeLaunch = func(resumePlan, io.Reader, io.Writer, io.Writer) (int, error) { launched = true; return 0, nil }
	t.Cleanup(func() { handoffResumeLaunch = previous })
	var stdout, stderr bytes.Buffer
	code := RunWithInput([]string{"handoff", "resume", "resume-source-123", "--source", "runtime-log", "--harness", "codex", "--agent", "gemini", "--traces", tracePath, "--output-dir", outputDir}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || launched || !strings.Contains(stderr.String(), "pass --yes") || stdout.Len() != 0 {
		t.Fatalf("unconfirmed resume code=%d launched=%t stdout=%q stderr=%q", code, launched, stdout.String(), stderr.String())
	}
	if _, err := os.Lstat(outputDir); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed resume wrote a brief: %v", err)
	}
}

func TestHandoffResumeRejectsChangedRuntimeLogBeforeWritingBriefOrLaunching(t *testing.T) {
	stubResumeRuntime(t)
	tracePath, workspace := resumeTraceFixture(t)
	outputDir := filepath.Join(t.TempDir(), "must-not-exist-after-stale-plan")
	previousLookPath := handoffResumeLookPath
	mutated := false
	handoffResumeLookPath = func(name string) (string, error) {
		if name == "gemini" && !mutated {
			mutated = true
			event := handoffFixtureEvent("codex-cli", "resume-source-123", "assistant.message.completed", "PRIVATE_POST_PLAN_EVENT")
			event.Session.WorkingDirectory = workspace
			appendHandoffFixture(t, tracePath, event)
		}
		return filepath.Join("/fixture/bin", name), nil
	}
	t.Cleanup(func() { handoffResumeLookPath = previousLookPath })
	previousLaunch := handoffResumeLaunch
	launched := false
	handoffResumeLaunch = func(resumePlan, io.Reader, io.Writer, io.Writer) (int, error) {
		launched = true
		return 0, nil
	}
	t.Cleanup(func() { handoffResumeLaunch = previousLaunch })
	var stdout, stderr bytes.Buffer
	code := RunWithInput([]string{"handoff", "resume", "resume-source-123", "--source", "runtime-log", "--harness", "codex", "--agent", "gemini", "--traces", tracePath, "--output-dir", outputDir, "--yes"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !mutated || launched || !strings.Contains(stderr.String(), "source session changed after planning") || stdout.Len() != 0 {
		t.Fatalf("stale resume code=%d mutated=%t launched=%t stdout=%q stderr=%q", code, mutated, launched, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "PRIVATE_POST_PLAN_EVENT") {
		t.Fatalf("stale event content leaked in error: %q", stderr.String())
	}
	if _, err := os.Lstat(outputDir); !os.IsNotExist(err) {
		t.Fatalf("stale plan created a private brief directory: %v", err)
	}
}

func TestHandoffResumeRechecksNativeReopenReference(t *testing.T) {
	stubResumeRuntime(t)
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
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-10-09T10-00-00-native-session-123.jsonl")
	subject, err := readResumeSubject("native", "native-session-123", "codex_cli", "", []string{"codex=" + codexDir})
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := buildResumePlan(subject, "codex_cli", "", "", false)
	if err != nil || plan.Mode != "native" {
		t.Fatalf("native plan=%#v err=%v", plan, err)
	}
	if err := revalidateResumeSubject(subject, plan); err != nil {
		t.Fatalf("unchanged native session failed revalidation: %v", err)
	}
	if err := os.Remove(rolloutPath); err != nil {
		t.Fatal(err)
	}
	if err := revalidateResumeSubject(subject, plan); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("vanished native session revalidation error=%v", err)
	}
}

func TestHandoffResumeYesWritesBriefBeforeLaunchAndPreservesChildExit(t *testing.T) {
	stubResumeRuntime(t)
	tracePath, _ := resumeTraceFixture(t)
	outputDir := filepath.Join(t.TempDir(), "private-handoffs")
	previous := handoffResumeLaunch
	launched := false
	handoffResumeLaunch = func(plan resumePlan, _ io.Reader, _, _ io.Writer) (int, error) {
		launched = true
		if plan.Mode != "new_session" || plan.BriefPath == "" {
			t.Fatalf("launch plan = %#v", plan)
		}
		data, err := os.ReadFile(plan.BriefPath)
		if err != nil || !strings.Contains(string(data), "PRIVATE_SOURCE_PROMPT") {
			t.Fatalf("brief unavailable before launch: %q err=%v", data, err)
		}
		return 7, nil
	}
	t.Cleanup(func() { handoffResumeLaunch = previous })
	var stdout, stderr bytes.Buffer
	code := RunWithInput([]string{"handoff", "resume", "resume-source-123", "--source", "runtime-log", "--harness", "codex", "--agent", "gemini", "--traces", tracePath, "--output-dir", outputDir, "--yes"}, strings.NewReader(""), &stdout, &stderr)
	if code != 7 || !launched || !strings.Contains(stderr.String(), "Start a new Gemini CLI session") {
		t.Fatalf("confirmed launch code=%d launched=%t stdout=%q stderr=%q", code, launched, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("brief directory entries=%v err=%v", entries, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(outputDir, entries[0].Name()))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("brief mode=%v err=%v", info.Mode(), err)
		}
	}
}

func TestResumePromptCarriesSourceLinkClaimMarker(t *testing.T) {
	marker := beacon.HandoffMarker("claude_code", "source-session-1")
	info, ok := beacon.ParseHandoffMarker(resumePrompt(handoffpkg.Session{Harness: "claude_code", ID: "source-session-1"}, filepath.Join(t.TempDir(), "brief.md")))
	if !ok || marker == "" || info.SourceHarness != "claude_code" || info.SourceSessionID != "source-session-1" {
		t.Fatalf("continuation prompt marker = %#v, ok=%t", info, ok)
	}
}

func TestResumeTargetCommandsCoverDocumentedStartableRuntimes(t *testing.T) {
	targets := resumeTargets()
	want := map[string]struct {
		executable string
		prefix     []string
	}{
		"claude_code": {"claude", nil}, "codex_cli": {"codex", nil}, "opencode": {"opencode", []string{"--prompt"}},
		"cline": {"cline", []string{"--tui", "--auto-approve", "false"}}, "pi_cli": {"pi", []string{"--"}},
		"prime_agent": {"prime-agent", []string{"--"}}, "factory": {"droid", nil},
		"copilot_cli": {"copilot", []string{"-i"}}, "grok": {"grok", []string{"--permission-mode", "default"}},
		"openclaw_gateway": {"openclaw", []string{"tui", "--session"}}, "cursor": {"agent", nil},
		"omp": {"omp", []string{"--approval-mode=always-ask"}}, "gemini_cli": {"gemini", []string{"--approval-mode", "default", "--prompt-interactive"}},
		"qwen_code": {"qwen", []string{"--approval-mode", "default", "--prompt-interactive"}}, "kiro": {"kiro-cli", []string{"chat"}},
		"antigravity_cli": {"agy", []string{"--prompt-interactive"}}, "devin-cli": {"devin", []string{"--permission-mode", "auto", "--"}},
		"muse_code": {"muse", []string{"--approval-mode", "on-request"}}, "openhands": {"openhands", []string{"--task"}},
		"goose": {"goose", []string{"run", "--interactive", "--text"}},
	}
	seen := 0
	for harness, target := range targets {
		if target.newArgs == nil {
			continue
		}
		seen++
		expected, ok := want[harness]
		if !ok || target.executable != expected.executable {
			t.Errorf("unexpected new-session target %q -> %q", harness, target.executable)
			continue
		}
		prompt := "bounded prompt fixture"
		args := target.newArgs(prompt, "threadforge-handoff-key")
		if len(args) == 0 || args[len(args)-1] != prompt || len(args) < len(expected.prefix) {
			t.Errorf("%s args do not end with the prompt: %q", harness, args)
			continue
		}
		for i, part := range expected.prefix {
			if args[i] != part {
				t.Errorf("%s args = %q; prefix[%d] = %q", harness, args, i, part)
				break
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("startable runtime count=%d, want %d", seen, len(want))
	}
	if targets["hermes"].newArgs != nil || targets["vercel_fx"].newArgs != nil {
		t.Fatal("runtimes without safe interactive brief-start modes must not appear as new-session targets")
	}
	if !reflect.DeepEqual(targets["cursor"].fallbackExecutables, []string{"cursor-agent"}) {
		t.Fatalf("Cursor fallback executables = %q, want legacy cursor-agent alias", targets["cursor"].fallbackExecutables)
	}
}

func TestCursorNativeResumeRequiresVerifiedStoreAndMatchingWorkspace(t *testing.T) {
	stubResumeRuntime(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	otherWorkspace := filepath.Join(root, "other-workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherWorkspace, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(root, "cursor-session-123.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user_message","text":"fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := resumeSubject{
		session: handoffpkg.Session{ID: "cursor-session-123", Harness: "cursor", Directory: workspace},
		source:  "native",
		native:  handoffbridge.NativeSession{ID: "cursor-session-123", Harness: "cursor", Store: "agent_transcript", ResumePath: transcript, NativeResume: true},
	}

	plan, _, err := buildResumePlan(base, "cursor", "", filepath.Join(root, "briefs"), false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "native" || plan.Executable != filepath.Join("/fixture/bin", "agent") || !reflect.DeepEqual(plan.Args, []string{"--resume", "cursor-session-123"}) {
		t.Fatalf("Cursor native plan = %#v", plan)
	}

	for _, tc := range []struct {
		name     string
		mutate   func(*resumeSubject)
		cwd      string
		forceNew bool
	}{
		{name: "no verified store", mutate: func(s *resumeSubject) { s.native.NativeResume = false }},
		{name: "Composer record", mutate: func(s *resumeSubject) { s.native.Store = "global_storage" }},
		{name: "subagent", mutate: func(s *resumeSubject) { s.native.Subagent = true }},
		{name: "different workspace", cwd: otherWorkspace},
		{name: "explicit new", forceNew: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := base
			if tc.mutate != nil {
				tc.mutate(&subject)
			}
			plan, _, err := buildResumePlan(subject, "cursor", tc.cwd, filepath.Join(root, "briefs"), tc.forceNew)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Mode != "new_session" || plan.BriefPath == "" {
				t.Fatalf("unsafe/unavailable native resume did not fall back to a brief: %#v", plan)
			}
		})
	}
}

func TestCursorExecutablePrefersAgentAndFallsBackToLegacyName(t *testing.T) {
	previous := handoffResumeLookPath
	t.Cleanup(func() { handoffResumeLookPath = previous })
	var seen []string
	handoffResumeLookPath = func(name string) (string, error) {
		seen = append(seen, name)
		if name == "agent" {
			return "", os.ErrNotExist
		}
		if name == "cursor-agent" {
			return filepath.Join("/fixture/bin", name), nil
		}
		return "", os.ErrNotExist
	}
	path, err := findResumeExecutable(resumeTargets()["cursor"])
	if err != nil || path != filepath.Join("/fixture/bin", "cursor-agent") || !reflect.DeepEqual(seen, []string{"agent", "cursor-agent"}) {
		t.Fatalf("Cursor executable = %q, calls=%q, err=%v", path, seen, err)
	}
}

func TestResumeNativeCommandsUseBoundedSessionReferences(t *testing.T) {
	stubResumeRuntime(t)
	dir := t.TempDir()
	resumePath := filepath.Join(dir, "source-session.jsonl")
	if err := os.WriteFile(resumePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		harness string
		id      string
		store   string
		key     string
		args    []string
	}{
		{"claude_code", "s-1", "", "", []string{"--resume", "s-1"}},
		{"codex_cli", "s-1", "", "", []string{"resume", "s-1"}},
		{"opencode", "s-1", "", "", []string{"--session", "s-1"}},
		{"cline", "s-1", "messages", "", []string{"--tui", "--auto-approve", "false", "--id", "s-1"}},
		{"pi_cli", "s-1", "", "", []string{"--session", resumePath}},
		{"prime_agent", "s-1", "", "", []string{"--resume", resumePath}},
		{"factory", "s-1", "", "", []string{"--resume", "s-1"}},
		{"copilot_cli", "s-1", "", "", []string{"--resume=s-1"}},
		{"hermes", "s-1", "", "", []string{"--resume", "s-1"}},
		{"vercel_fx", "s-1", "", "", []string{"--resume", "s-1"}},
		{"grok", "s-1", "", "", []string{"--permission-mode", "default", "--resume", "s-1"}},
		{"openclaw_gateway", "s-1", "", "agent:main:s-1", []string{"tui", "--session", "agent:main:s-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			subject := resumeSubject{
				session: handoffpkg.Session{ID: tc.id, Harness: tc.harness, Directory: dir},
				source:  "native",
				native:  handoffbridge.NativeSession{ID: tc.id, Harness: tc.harness, Store: tc.store, Key: tc.key, ResumePath: resumePath},
			}
			plan, _, err := buildResumePlan(subject, tc.harness, "", filepath.Join(t.TempDir(), "briefs"), false)
			if err != nil {
				t.Fatalf("build native plan: %v", err)
			}
			if plan.Mode != "native" || !reflect.DeepEqual(plan.Args, tc.args) {
				t.Fatalf("plan mode/args = %q/%q, want native/%q", plan.Mode, plan.Args, tc.args)
			}
		})
	}
}
