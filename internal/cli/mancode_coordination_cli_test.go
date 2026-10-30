package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMancodeCoordinationRootCompletesCLIReviewImportPath(t *testing.T) {
	const workspaceID = "00000000000000000000000001"
	const operationID = "00000000000000000000000002"
	parent := t.TempDir()
	sourceRoot := filepath.Join(parent, "source", ".git", "mancode", "workspaces", workspaceID)
	operationPath := filepath.Join(sourceRoot, "operations", operationID+".json")
	if err := os.MkdirAll(filepath.Dir(operationPath), 0o700); err != nil {
		t.Fatal(err)
	}
	operation, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"operationId":   operationID,
		"type":          "claim_create",
		"state":         "repair_required",
		"startedAt":     "2026-10-18T10:00:00Z",
		"updatedAt":     "2026-10-18T10:01:00Z",
		"sessionId":     "source-session-must-not-migrate",
		"recoveryNote":  "private-recovery-detail-must-not-migrate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(operationPath, append(operation, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	backupRoot := filepath.Join(parent, "independent-backup")
	backupPath := filepath.Join(backupRoot, "operations", operationID+".json")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, append(operation, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(parent, "threadforge-home")
	database := filepath.Join(home, "threadforge.db")
	t.Setenv("THREADFORGE_HOME", home)
	run := func(args ...string) []byte {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("threadforge %s failed: code=%d stderr=%q stdout=%q", strings.Join(args, " "), code, stderr.String(), stdout.String())
		}
		return append([]byte(nil), stdout.Bytes()...)
	}

	inspect := run("migrate", "inspect", "--product", "mancode", "--root", sourceRoot)
	var inspected struct {
		Summary struct {
			RecordsFound    int `json:"records_found"`
			RecordsImported int `json:"records_imported"`
			RecordsRejected int `json:"records_rejected"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(inspect, &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Summary.RecordsFound != 1 || inspected.Summary.RecordsImported != 1 || inspected.Summary.RecordsRejected != 0 {
		t.Fatalf("coordination workspace was not inventoried as a migration source: %s", inspect)
	}

	snapshotJSON := run("migrate", "snapshot", "--product", "mancode", "--root", sourceRoot)
	var snapshot struct {
		SourceFingerprint string `json:"source_fingerprint"`
		Summary           struct {
			RecordsImported int `json:"records_imported"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SourceFingerprint == "" || snapshot.Summary.RecordsImported != 1 {
		t.Fatalf("coordination snapshot did not reconcile: %s", snapshotJSON)
	}
	manifestPath := filepath.Join(home, "migrations", "snapshots", "mancode", snapshot.SourceFingerprint, "manifest.json")

	coverageJSON := run("migrate", "coverage", "--manifest", manifestPath)
	var coverage struct {
		RecordsFound    int `json:"records_found"`
		RecordsImported int `json:"records_imported"`
	}
	if err := json.Unmarshal(coverageJSON, &coverage); err != nil {
		t.Fatal(err)
	}
	if coverage.RecordsFound != 1 || coverage.RecordsImported != 1 {
		t.Fatalf("coordination coverage did not reconcile: %s", coverageJSON)
	}

	run("migrate", "backup-verify", "--manifest", manifestPath, "--backup-root", backupRoot)
	run("migrate", "apply", "--manifest", manifestPath, "--backup-root", backupRoot, "--db", database)
	verification := run("migrate", "verify", "--manifest", manifestPath, "--db", database)
	if !strings.Contains(string(verification), `"status": "reconciled"`) || strings.Contains(string(verification), workspaceID) {
		t.Fatalf("coordination migration did not verify or exposed its workspace identity: %s", verification)
	}
	records := run("migrate", "records", "--id", "mancode-"+snapshot.SourceFingerprint[:20], "--db", database)
	for _, omitted := range []string{workspaceID, operationID, "source-session-must-not-migrate", "private-recovery-detail-must-not-migrate"} {
		if strings.Contains(string(records), omitted) {
			t.Fatalf("sanitized coordination record output retained %q: %s", omitted, records)
		}
	}
}
