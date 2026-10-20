package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBeaconHookLogMigrationImportsOnlyBoundedSummary(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, ".beacon", "claude", "hooks.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := strings.Join([]string{
		`{"timestamp":"2026-10-07T23:59:00Z","hook":"UserPromptSubmit","level":"info","message":"private prompt text","session_id":"private-session","prompt":"secret-prompt","future":{"token":"secret-token"}}`,
		`{"timestamp":"2026-10-08T00:01:00+00:00","hook":"PostToolUse","level":"ERROR","message":"private tool result","session_id":"private-session-2","result":"secret-result"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesScanned != 1 || manifest.Summary.FilesReady != 1 || manifest.Summary.RecordsFound != 1 ||
		manifest.Summary.RecordsImported != 1 || manifest.Files[0].Path != filepath.ToSlash(filepath.Join(".beacon", "claude", "hooks.log")) {
		t.Fatalf("hook log did not reconcile as one summary record: %#v", manifest)
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
	if len(records) != 1 || records[0].Type != "beacon_hook_log_summary" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected migrated hook log records: %#v", records)
	}
	var summary beaconHookLogSummary
	if err := json.Unmarshal(records[0].Payload, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Harness != "claude_code" || summary.EntryCount != 2 || summary.FirstDay == nil || *summary.FirstDay != "2026-10-07" ||
		summary.LastDay == nil || *summary.LastDay != "2026-10-08" || summary.LevelCounts["info"] != 1 || summary.LevelCounts["error"] != 1 ||
		summary.ExecutionAuthorized || !summary.HumanReviewRequired {
		t.Fatalf("unexpected sanitized hook log summary: %#v", summary)
	}
	for _, forbidden := range []string{
		"private prompt text", "private tool result", "private-session", "secret-prompt", "secret-token", "secret-result",
		"UserPromptSubmit", "PostToolUse",
	} {
		if strings.Contains(string(records[0].Payload), forbidden) {
			t.Fatalf("normalized hook log retained %q: %s", forbidden, records[0].Payload)
		}
	}
}

func TestBeaconHookLogPathAllowlistRequiresBeaconPlatformRoot(t *testing.T) {
	beaconRoot := filepath.Join("users", "tester", ".beacon")
	platformRoot := filepath.Join(beaconRoot, "claude")
	logsRoot := filepath.Join(platformRoot, "logs")
	outsideRoot := filepath.Join("users", "tester", "project")
	cases := []struct {
		name       string
		relative   string
		root       string
		wantAccept bool
	}{
		{name: "home root", relative: filepath.Join(".beacon", "claude", "hooks.log"), root: filepath.Join("users", "tester"), wantAccept: true},
		{name: "beacon root", relative: filepath.Join("claude", "hooks.log"), root: beaconRoot, wantAccept: true},
		{name: "platform root", relative: "hooks.log", root: platformRoot, wantAccept: true},
		{name: "home session log", relative: filepath.Join(".beacon", "claude", "logs", "session-1.log"), root: filepath.Join("users", "tester"), wantAccept: true},
		{name: "beacon session root", relative: filepath.Join("claude", "logs", "session-1.log"), root: beaconRoot, wantAccept: true},
		{name: "platform session root", relative: filepath.Join("logs", "session-1.log"), root: platformRoot, wantAccept: true},
		{name: "session logs root", relative: "session-1.log", root: logsRoot, wantAccept: true},
		{name: "endpoint log is not host hook log", relative: filepath.Join("endpoint", "hooks.log"), root: beaconRoot},
		{name: "unknown platform", relative: filepath.Join("unknown", "hooks.log"), root: beaconRoot},
		{name: "project log", relative: "hooks.log", root: outsideRoot},
		{name: "outside session log", relative: filepath.Join("claude", "logs", "session-1.log"), root: outsideRoot},
		{name: "session id cannot contain a path", relative: filepath.Join("claude", "logs", "..", "session-1.log"), root: beaconRoot},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := isBeaconHookLogPath(test.relative, test.root); got != test.wantAccept {
				t.Fatalf("isBeaconHookLogPath(%q, %q) = %t, want %t", test.relative, test.root, got, test.wantAccept)
			}
		})
	}
}

func TestBeaconPerSessionLogMigrationOmitsSessionIdentityAndMessage(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, ".beacon", "cursor", "logs", "private-session-id.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"2026-10-08T10:19:00Z","hook":"PostToolUse","level":"info","message":"private trajectory text","session_id":"private-session-id"}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesScanned != 1 || manifest.Summary.RecordsImported != 1 {
		t.Fatalf("per-session hook log was not summarized: %#v", manifest.Summary)
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
	if len(records) != 1 || records[0].Type != "beacon_hook_log_summary" {
		t.Fatalf("unexpected per-session summary record: %#v", records)
	}
	for _, forbidden := range []string{"private trajectory text", "private-session-id", "PostToolUse"} {
		if strings.Contains(string(records[0].Payload), forbidden) {
			t.Fatalf("per-session summary retained %q: %s", forbidden, records[0].Payload)
		}
	}
}

func TestBeaconHookLogRejectsMalformedDuplicateAndOversizedEntries(t *testing.T) {
	valid := `{"timestamp":"2026-10-08T10:00:00Z","hook":"SessionStart","level":"info","message":"private"}`
	invalidCases := []struct {
		name    string
		content string
	}{
		{name: "malformed json", content: "not-json\n"},
		{name: "duplicate key", content: `{"timestamp":"2026-10-08T10:00:00Z","hook":"SessionStart","hook":"Stop","level":"info","message":"private"}` + "\n"},
		{name: "invalid timestamp", content: `{"timestamp":"yesterday","hook":"SessionStart","level":"info","message":"private"}` + "\n"},
		{name: "non-string message", content: `{"timestamp":"2026-10-08T10:00:00Z","hook":"SessionStart","level":"info","message":42}` + "\n"},
		{name: "empty line", content: valid + "\n\n"},
		{name: "oversized line", content: `{"timestamp":"2026-10-08T10:00:00Z","hook":"SessionStart","level":"info","message":"` + strings.Repeat("x", maxBeaconHookLogLineBytes) + `"}` + "\n"},
	}
	for _, test := range invalidCases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			logPath := filepath.Join(root, ".beacon", "claude", "hooks.log")
			if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(logPath, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			manifest, err := Inspect(Beacon, root)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Summary.FilesUnsupported != 1 || manifest.Summary.RecordsImported != 0 || manifest.Summary.RecordsRejected != 1 {
				t.Fatalf("malformed hook log was not rejected atomically: %#v", manifest.Summary)
			}
		})
	}
}

func TestBeaconHookLogEntryCountLimitIsEnforced(t *testing.T) {
	line := `{"timestamp":"2026-10-08T10:00:00Z","hook":"SessionStart","level":"info","message":"private"}` + "\n"
	content := strings.Repeat(line, maxBeaconHookLogLines+1)
	root := filepath.Join(t.TempDir(), "home")
	_, err := normalizeBeaconHookLog(filepath.Join(".beacon", "claude", "hooks.log"), root, []byte(content))
	if err == nil || !strings.Contains(err.Error(), "entry limit") {
		t.Fatalf("over-limit Beacon hook log was not rejected: %v", err)
	}
}
