package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
	_ "modernc.org/sqlite"
)

func TestTeamRecoverySnapshotVerifyAndStagedRestore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("THREADFORGE_HOME", home)
	databasePath := filepath.Join(home, "threadforge.sqlite")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"task", "create", "--id", "recovery-task", "--project-id", "recovery-project",
		"--title", "Recovery fixture", "--objective", "Preserve local continuity", "--criterion", "Task survives backup"}, &stdout, &stderr); code != 0 {
		t.Fatalf("seed task code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"team", "recovery", "snapshot", "--id", "snapshot-1", "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("snapshot code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || receipt["activation_allowed"] != false {
		t.Fatalf("snapshot receipt=%#v decode error=%v", receipt, err)
	}
	snapshotDB := filepath.Join(home, "team-recovery", "snapshots", "snapshot-1", localRecoveryDB)
	if code := Run([]string{"team", "recovery", "verify", "--id", "snapshot-1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("verify snapshot code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	args := []string{"team", "recovery", "restore", "--snapshot-id", "snapshot-1", "--restore-id", "restore-1",
		"--confirm-restore-id", "restore-1", "--reason", "Recover local store after disk repair"}
	if code := Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("restore code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || receipt["activation_allowed"] != false {
		t.Fatalf("restore receipt=%#v decode error=%v", receipt, err)
	}
	restoredDB := filepath.Join(home, "team-recovery", "restored", "restore-1", localRecoveryDB)
	restoreDirectory := filepath.Dir(restoredDB)
	manifestPath := filepath.Join(restoreDirectory, localRecoveryFile)
	originalManifest := readFile(t, manifestPath)
	var forgedManifest localRecoveryManifest
	if err := json.Unmarshal(originalManifest, &forgedManifest); err != nil {
		t.Fatal(err)
	}
	forgedManifest.Kind = "activated_restore"
	forgedManifest.ActivationAllowed = true
	forgedManifest.ActivationReason = "Attempt to bypass scope review"
	forgedManifest.ActivatedAt = "2026-10-05T00:00:00Z"
	forgedManifest.ActivationMethod = "local-explicit-confirmation"
	forgedManifest.TeamProjectCount = intPointer(0)
	forgedManifest.TeamScopesFrozen = intPointer(0)
	forgedManifest.TeamAuditStatus = "no_team_projects"
	forgedBytes, err := json.Marshal(forgedManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, forgedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRecoveryDirectory(restoreDirectory, "restore-1", "restore"); err == nil ||
		!strings.Contains(err.Error(), "SQLite activation guard") {
		t.Fatalf("manifest-only activation bypass returned error=%v", err)
	}
	if err := os.WriteFile(manifestPath, originalManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(restoreDirectory, localRecoveryDB+"-wal")
	if err := os.WriteFile(sidecar, []byte("unexpected sidecar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRecoveryDirectory(restoreDirectory, "restore-1", "staged_restore"); err == nil ||
		!strings.Contains(err.Error(), "only manifest.json and store.sqlite") {
		t.Fatalf("unexpected SQLite sidecar returned error=%v", err)
	}
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(restoredDB); !errors.Is(err, store.ErrRecoveryStaged) {
		t.Fatalf("staged database Open error=%v, want ErrRecoveryStaged", err)
	}
	readOnly, err := sql.Open("sqlite", restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	var tasks int
	if err := readOnly.QueryRow(`SELECT COUNT(*) FROM continuity_tasks WHERE id = 'recovery-task'`).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("restored continuity task count=%d, error=%v", tasks, err)
	}
	_ = readOnly.Close()
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"team", "recovery", "verify", "--id", "restore-1", "--kind", "restore"}, &stdout, &stderr); code != 0 {
		t.Fatalf("verify restore code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	activateArgs := []string{"team", "recovery", "activate", "--restore-id", "restore-1", "--activation-id", "active-1",
		"--confirm-activation-id", "active-1", "--reason", "Reviewed and confirmed local recovery"}
	if code := Run(activateArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("activate local recovery code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || receipt["activation_allowed"] != true ||
		receipt["kind"] != "activated_restore" || receipt["team_audit_status"] != "no_team_projects" {
		t.Fatalf("activated local receipt=%#v decode error=%v", receipt, err)
	}
	activeRecoveryDB := filepath.Join(home, "team-recovery", "restored", "active-1", localRecoveryDB)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"team", "recovery", "verify", "--id", "active-1", "--kind", "restore"}, &stdout, &stderr); code != 0 {
		t.Fatalf("verify untouched activated restore code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || receipt["store_matches_manifest"] != true {
		t.Fatalf("untouched activation receipt=%#v decode error=%v", receipt, err)
	}
	activated, err := store.Open(activeRecoveryDB)
	if err != nil {
		t.Fatalf("explicitly activated local store did not open: %v", err)
	}
	_ = activated.Close()
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"team", "recovery", "verify", "--id", "active-1", "--kind", "restore"}, &stdout, &stderr); code != 0 {
		t.Fatalf("verify used activated restore code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || receipt["store_matches_manifest"] != false ||
		receipt["status"] != "modified_since_activation" {
		t.Fatalf("used activation receipt=%#v decode error=%v", receipt, err)
	}
	active, err := store.Open(databasePath)
	if err != nil {
		t.Fatalf("source store was affected by staged restore: %v", err)
	}
	_ = active.Close()
	if code := Run(args, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("duplicate restore ID code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if code := Run(activateArgs, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("duplicate activation ID code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if err := os.WriteFile(snapshotDB, append(readFile(t, snapshotDB), 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"team", "recovery", "verify", "--id", "snapshot-1"}, &stdout, &stderr); code == 0 ||
		!strings.Contains(stderr.String(), "does not match its manifest") {
		t.Fatalf("tampered snapshot verify code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestTeamRecoveryActivationSignsFrozenScopesAndRequiresAuthority(t *testing.T) {
	home := t.TempDir()
	t.Setenv("THREADFORGE_HOME", home)
	roles := []string{teamauth.RoleContinuityWrite, teamauth.RoleTeamScopeManage}
	identityPath := configureContinuityTrustForTest(t, home, "recovery-team-project", "recovery-admin", roles...)
	databasePath := filepath.Join(home, "threadforge.sqlite")
	var stdout, stderr bytes.Buffer
	createTask := []string{"task", "create", "--id", "team-recovery-task", "--project-id", "recovery-team-project",
		"--title", "Team recovery fixture", "--objective", "Keep team continuity frozen after restore",
		"--criterion", "Restored scope is decommissioned", "--identity-key", identityPath, "--db", databasePath}
	if code := Run(createTask, &stdout, &stderr); code != 0 {
		t.Fatalf("create team task code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, args := range [][]string{
		{"team", "recovery", "snapshot", "--id", "team-snapshot", "--db", databasePath},
		{"team", "recovery", "restore", "--snapshot-id", "team-snapshot", "--restore-id", "team-staged",
			"--confirm-restore-id", "team-staged", "--reason", "Recover team store after disk repair"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("setup recovery command %v code=%d stdout=%q stderr=%q", args[:3], code, stdout.String(), stderr.String())
		}
	}
	activateArgs := []string{"team", "recovery", "activate", "--restore-id", "team-staged", "--activation-id", "team-active",
		"--confirm-activation-id", "team-active", "--reason", "Freeze restored scopes before use"}
	stdout.Reset()
	stderr.Reset()
	if code := Run(activateArgs, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "requires --identity-key") {
		t.Fatalf("activation without team authority code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	activateArgs = append(activateArgs, "--identity-key", identityPath)
	stdout.Reset()
	stderr.Reset()
	if code := Run(activateArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("signed team recovery activation code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || receipt["activation_allowed"] != true ||
		receipt["team_project_count"] != float64(1) || receipt["team_scopes_frozen"] != float64(1) ||
		receipt["team_audit_status"] != "passed" || receipt["activation_method"] != "signed-team-scope-freezes" {
		t.Fatalf("team activation receipt=%#v decode error=%v", receipt, err)
	}
	activeDB := filepath.Join(home, "team-recovery", "restored", "team-active", localRecoveryDB)
	activated, err := store.Open(activeDB)
	if err != nil {
		t.Fatalf("activated team store did not open: %v", err)
	}
	status, err := activated.TeamProjectScopeStatus(t.Context(), "recovery-team-project")
	if err != nil || status.Status != "decommissioned" {
		t.Fatalf("activated team scope=%#v error=%v", status, err)
	}
	_ = activated.Close()
	stdout.Reset()
	stderr.Reset()
	updateArgs := []string{"task", "update", "--id", "team-recovery-task", "--expected-revision", "1", "--summary", "Must remain frozen",
		"--identity-key", identityPath, "--db", activeDB}
	if code := Run(updateArgs, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "scope is decommissioned") {
		t.Fatalf("activated frozen team write code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"team", "audit", "verify", "--db", activeDB}, &stdout, &stderr); code != 0 {
		t.Fatalf("activated recovery audit code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestTeamRecoveryActivationRejectsUnauthenticatedPreScopeHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("THREADFORGE_HOME", home)
	databasePath := filepath.Join(home, "threadforge.sqlite")
	var stdout, stderr bytes.Buffer
	createTask := []string{"task", "create", "--id", "pre-scope-task", "--project-id", "late-team-project",
		"--title", "Historical local task", "--objective", "Preserve provenance accurately", "--criterion", "History is review-required"}
	if code := Run(createTask, &stdout, &stderr); code != 0 {
		t.Fatalf("create pre-scope task code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	identityPath := configureContinuityTrustForTest(t, home, "late-team-project", "recovery-admin",
		teamauth.RoleContinuityWrite, teamauth.RoleTeamScopeManage)
	for _, args := range [][]string{
		{"team", "recovery", "snapshot", "--id", "history-snapshot", "--db", databasePath},
		{"team", "recovery", "restore", "--snapshot-id", "history-snapshot", "--restore-id", "history-staged",
			"--confirm-restore-id", "history-staged", "--reason", "Recover local history for review"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("setup history recovery command %v code=%d stdout=%q stderr=%q", args[:3], code, stdout.String(), stderr.String())
		}
	}
	activateArgs := []string{"team", "recovery", "activate", "--restore-id", "history-staged", "--activation-id", "history-active",
		"--confirm-activation-id", "history-active", "--identity-key", identityPath,
		"--reason", "Review unauthenticated pre-scope history"}
	stdout.Reset()
	stderr.Reset()
	if code := Run(activateArgs, &stdout, &stderr); code == 0 ||
		!strings.Contains(stderr.String(), "review_required") {
		t.Fatalf("activation with unauthenticated history code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "team-recovery", "restored", "history-active")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe active copy was published, stat error=%v", err)
	}
	if code := Run([]string{"team", "recovery", "verify", "--id", "history-staged", "--kind", "restore"}, &stdout, &stderr); code != 0 {
		t.Fatalf("failed activation damaged the original staged copy: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
