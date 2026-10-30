package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/migrationsources"
)

func TestMancodeSourceSetCLIRequiresAndEmitsCompleteDiscoveredInventory(t *testing.T) {
	const workspaceID = "00000000000000000000000001"
	const operationID = "00000000000000000000000003"
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "project")
	coordinationRoot := filepath.Join(projectRoot, ".git", "mancode", "workspaces", workspaceID)
	operationPath := filepath.Join(coordinationRoot, "operations", operationID+".json")
	if err := os.MkdirAll(filepath.Dir(operationPath), 0o700); err != nil {
		t.Fatal(err)
	}
	operation := `{"schemaVersion":1,"operationId":"` + operationID + `","type":"claim_create","state":"repair_required","startedAt":"2026-10-18T10:00:00Z","updatedAt":"2026-10-18T10:01:00Z","sessionId":"source-session-private"}`
	if err := os.WriteFile(operationPath, []byte(operation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(parent, "threadforge-home")
	t.Setenv("THREADFORGE_HOME", home)
	run := func(wantCode int, args ...string) ([]byte, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run(args, &stdout, &stderr)
		if code != wantCode {
			t.Fatalf("threadforge %s returned %d, want %d: stdout=%q stderr=%q", strings.Join(args, " "), code, wantCode, stdout.String(), stderr.String())
		}
		return append([]byte(nil), stdout.Bytes()...), stderr.String()
	}

	localRootsJSON, _ := run(0, "migrate", "sources", "--root", projectRoot)
	var localRoots struct {
		Sources []struct {
			Role       string `json:"role"`
			SourceRoot string `json:"source_root"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(localRootsJSON, &localRoots); err != nil {
		t.Fatal(err)
	}
	if len(localRoots.Sources) != 2 {
		t.Fatalf("operator inventory omitted the coordination workspace: %s", localRootsJSON)
	}

	manifestPaths := make(map[string]string)
	for _, root := range []string{projectRoot, coordinationRoot} {
		snapshotJSON, _ := run(0, "migrate", "snapshot", "--product", "mancode", "--root", root)
		var snapshot struct {
			SourceFingerprint string `json:"source_fingerprint"`
		}
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		manifestPaths[root] = filepath.Join(home, "migrations", "snapshots", "mancode", snapshot.SourceFingerprint, "manifest.json")
	}

	args := []string{"migrate", "source-set", "--root", projectRoot,
		"--manifest", manifestPaths[coordinationRoot], "--manifest", manifestPaths[projectRoot]}
	reportJSON, _ := run(0, args...)
	var report migrationsources.MancodeReport
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		t.Fatal(err)
	}
	if err := migrationsources.ValidateMancode(report); err != nil {
		t.Fatalf("CLI emitted an invalid source-set report: %v", err)
	}
	if len(report.Components) != 2 || report.DuplicateRecordKeys != 0 {
		t.Fatalf("source-set report did not reconcile all component roots: %#v", report)
	}
	for _, privateValue := range []string{projectRoot, coordinationRoot, workspaceID, operationID, "source-session-private"} {
		if strings.Contains(string(reportJSON), privateValue) {
			t.Fatalf("shareable CLI inventory retained private value %q: %s", privateValue, reportJSON)
		}
	}

	missingArgs := []string{"migrate", "source-set", "--root", projectRoot, "--manifest", manifestPaths[projectRoot]}
	missingJSON, missingErr := run(1, missingArgs...)
	if len(missingJSON) != 0 || !strings.Contains(missingErr, "one snapshot for each of 2 discovered source roots") {
		t.Fatalf("source-set CLI did not fail visibly when a discovered root was omitted: stdout=%q stderr=%q", missingJSON, missingErr)
	}
}
