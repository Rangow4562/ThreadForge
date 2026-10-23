package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMancodeGitRefCLIOptInIsExplicitAndNeverFetches(t *testing.T) {
	root := t.TempDir()
	runCLIForMancodeGitRefTest(t, root, "", "init", "--quiet")
	workspaceID := "00000000000000000000000001"
	config := `{"schemaVersion":1,"revision":1,"workspaceId":"` + workspaceID + `","transport":{"mode":"git-ref","epoch":1},"lastOperationId":"` + workspaceID + `","updatedAt":"2026-10-08T12:00:00Z"}`
	configPath := filepath.Join(root, ".mancode", "shared", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	runCLIForMancodeGitRefTest(t, root, "", "remote", "add", "origin", "https://127.0.0.1:1/no-fetch.git")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"migrate", "inspect", "--product", "mancode", "--root", root}, &stdout, &stderr)
	var ordinary struct {
		Summary struct {
			RecordsRejected int `json:"records_rejected"`
		} `json:"summary"`
	}
	if code != 0 || json.Unmarshal(stdout.Bytes(), &ordinary) != nil || ordinary.Summary.RecordsRejected == 0 {
		t.Fatalf("git-ref authority was not visible without opt-in: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"migrate", "inspect", "--product", "mancode", "--root", root, "--include-mancode-git-ref"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "available locally") || stdout.Len() != 0 {
		t.Fatalf("opt-in did not fail safely for an absent local ref: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "FETCH_HEAD")); !os.IsNotExist(err) {
		t.Fatalf("migrate inspect fetched a remote ref or left FETCH_HEAD: %v", err)
	}

	threadforgeHome := t.TempDir()
	t.Setenv("THREADFORGE_HOME", threadforgeHome)
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"migrate", "snapshot", "--product", "mancode", "--root", root, "--include-mancode-git-ref"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "available locally") || stdout.Len() != 0 {
		t.Fatalf("snapshot CLI did not pass the explicit local-ref option: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(threadforgeHome)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed snapshot created Threadforge data: entries=%v err=%v", entries, err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"migrate", "inspect", "--product", "beacon", "--root", root, "--include-mancode-git-ref"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "valid only for Mancode") {
		t.Fatalf("git-ref option was accepted for Beacon: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func runCLIForMancodeGitRefTest(t *testing.T, root, stdin string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	command.Stdin = strings.NewReader(stdin)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output))
}
