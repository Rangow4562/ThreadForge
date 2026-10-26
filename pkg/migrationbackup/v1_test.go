package migrationbackup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/migrate"
)

func TestVerifySnapshotReportsExactIndependentCopyWithoutPathsOrContent(t *testing.T) {
	sourceRoot, backupRoot := backupFixture(t)
	privateName := "private-source-name.jsonl"
	privateMessage := "private hook message must not appear in verification output"
	writeBackupFixtureFile(t, filepath.Join(sourceRoot, ".beacon", "endpoint", "logs", "runtime.jsonl"), validBeaconEventLog(t, privateMessage))
	writeBackupFixtureFile(t, filepath.Join(sourceRoot, privateName), "not imported\n")
	copyBackupFixtureTree(t, sourceRoot, backupRoot)
	manifestPath := createBackupFixtureSnapshot(t, sourceRoot)
	report, err := VerifySnapshot(manifestPath, backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "verified" || report.Product != string(migrate.Beacon) || report.SourceFingerprint == "" ||
		report.CurrentSourceFingerprint != report.SourceFingerprint ||
		report.SnapshotSHA256 == "" || report.SourceTreeSHA256 != report.BackupTreeSHA256 ||
		report.SourceFiles != 2 || report.BackupFiles != 2 || report.DifferentFiles != 0 ||
		report.SnapshotSourceMismatches != 0 || report.SharedFiles != 0 {
		t.Fatalf("identical independent backup was not verified: %#v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), sourceRoot) || strings.Contains(string(encoded), backupRoot) ||
		strings.Contains(string(encoded), privateName) || strings.Contains(string(encoded), privateMessage) {
		t.Fatalf("backup receipt exposed a path or source content: %s", encoded)
	}
	if err := Validate(report); err != nil {
		t.Fatalf("valid backup receipt failed validation: %v", err)
	}
}

func TestVerifySnapshotRejectsChangedAndIncompleteBackups(t *testing.T) {
	sourceRoot, backupRoot := backupFixture(t)
	writeBackupFixtureFile(t, filepath.Join(sourceRoot, ".beacon", "endpoint", "logs", "runtime.jsonl"), validBeaconEventLog(t, "source bytes"))
	copyBackupFixtureTree(t, sourceRoot, backupRoot)
	manifestPath := createBackupFixtureSnapshot(t, sourceRoot)

	writeBackupFixtureFile(t, filepath.Join(backupRoot, ".beacon", "endpoint", "logs", "runtime.jsonl"), validBeaconEventLog(t, "different bytes"))
	report, err := VerifySnapshot(manifestPath, backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "mismatch" || report.DifferentFiles != 1 || report.SourceTreeSHA256 == report.BackupTreeSHA256 {
		t.Fatalf("changed backup did not produce a mismatch receipt: %#v", report)
	}
	if err := Validate(report); err != nil {
		t.Fatalf("structurally valid mismatch receipt was rejected: %v", err)
	}

	if err := os.Remove(filepath.Join(backupRoot, ".beacon", "endpoint", "logs", "runtime.jsonl")); err != nil {
		t.Fatal(err)
	}
	report, err = VerifySnapshot(manifestPath, backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "mismatch" || report.DifferentFiles != 1 || report.BackupFiles != 0 {
		t.Fatalf("missing backup file did not block verification: %#v", report)
	}
}

func TestVerifySnapshotBindsCurrentSourceToSnapshot(t *testing.T) {
	sourceRoot, backupRoot := backupFixture(t)
	sourceFile := filepath.Join(sourceRoot, ".beacon", "endpoint", "logs", "runtime.jsonl")
	writeBackupFixtureFile(t, sourceFile, validBeaconEventLog(t, "snapshot bytes"))
	manifestPath := createBackupFixtureSnapshot(t, sourceRoot)
	writeBackupFixtureFile(t, sourceFile, validBeaconEventLog(t, "changed after snapshot"))
	copyBackupFixtureTree(t, sourceRoot, backupRoot)

	report, err := VerifySnapshot(manifestPath, backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "mismatch" || report.SnapshotSourceMismatches != 1 || report.DifferentFiles != 0 {
		t.Fatalf("backup identical to a changed source was accepted against the old snapshot: %#v", report)
	}
}

func TestVerifySnapshotBindsMancodeGitRefInIndependentBackup(t *testing.T) {
	sourceRoot, backupRoot := backupFixture(t)
	workspaceID := "00000000000000000000000001"
	writeBackupFixtureFile(t, filepath.Join(sourceRoot, ".mancode", "shared", "config.json"),
		`{"schemaVersion":1,"revision":1,"workspaceId":"`+workspaceID+`","transport":{"mode":"git-ref","epoch":1},"lastOperationId":"`+workspaceID+`","updatedAt":"2026-10-08T12:00:00Z"}`)
	for _, args := range [][]string{
		{"init", "--quiet"}, {"config", "user.name", "Threadforge Test"},
		{"config", "user.email", "threadforge-test@example.invalid"},
	} {
		runMigrationBackupGit(t, sourceRoot, "", args...)
	}
	refManifest, err := json.Marshal(map[string]any{
		"schemaVersion": 1, "workspaceId": workspaceID, "schemaEpoch": workspaceID,
		"minReaderVersion": "0.0.0", "minWriterVersion": "0.0.0", "transportEpoch": 1,
		"configRevision": 1, "configDigest": "sha256:" + strings.Repeat("a", 64),
		"authorityState": "active", "authorityFreeze": nil, "authorityTombstone": nil,
		"revision": 1, "lastOperationId": workspaceID, "actorProfiles": []any{},
		"ownershipFences": []any{}, "claims": []any{}, "handoffs": []any{},
		"taskBundles": []any{}, "receipts": []any{map[string]any{
			"schemaVersion": 1, "kind": "actor_profile", "operationId": workspaceID,
			"actorId": "00000000000000000000000002", "taskRef": nil, "remoteRevision": 1, "ownershipEpoch": nil,
			"entityDigests": map[string]any{
				"actorProfiles": migrationBackupEmptyArrayDigest(), "ownershipFence": nil,
				"claims": migrationBackupEmptyArrayDigest(), "handoffs": migrationBackupEmptyArrayDigest(), "taskBundle": nil,
			},
			"committedAt": "2026-10-08T12:00:00Z",
		}},
		"lastMutation": map[string]any{
			"schemaVersion": 1, "kind": "actor_profile", "operationId": workspaceID,
			"actorId": "00000000000000000000000002", "taskRef": nil, "remoteRevision": 1, "ownershipEpoch": nil,
			"entityDigests": map[string]any{
				"actorProfiles": migrationBackupEmptyArrayDigest(), "ownershipFence": nil,
				"claims": migrationBackupEmptyArrayDigest(), "handoffs": migrationBackupEmptyArrayDigest(), "taskBundle": nil,
			},
			"committedAt": "2026-10-08T12:00:00Z",
		},
		"updatedAt": "2026-10-08T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	blob := runMigrationBackupGit(t, sourceRoot, string(refManifest), "hash-object", "-w", "--stdin")
	tree := runMigrationBackupGit(t, sourceRoot, fmt.Sprintf("100644 blob %s\tmanifest.json\n", blob), "mktree")
	commit := runMigrationBackupGit(t, sourceRoot, "", "commit-tree", tree, "-m", "fixture")
	runMigrationBackupGit(t, sourceRoot, "", "update-ref", "refs/mancode/team", commit)

	dataRoot := filepath.Join(t.TempDir(), "threadforge-data")
	snapshot, err := migrate.CreateSnapshotWithOptions(migrate.Mancode, sourceRoot, dataRoot, migrate.InspectOptions{IncludeMancodeGitRef: true})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(migrate.Mancode), snapshot.SourceFingerprint, "manifest.json")
	copyBackupFixtureTree(t, sourceRoot, backupRoot)
	report, err := VerifySnapshot(manifestPath, backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "verified" || report.SnapshotSourceMismatches != 0 || report.DifferentFiles != 0 {
		t.Fatalf("independent backup with the captured local team ref did not verify: %#v", report)
	}

	refPath := filepath.Join(backupRoot, ".git", "refs", "mancode", "team")
	if err := os.WriteFile(refPath, []byte(strings.Repeat("0", 40)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = VerifySnapshot(manifestPath, backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "mismatch" || report.SnapshotSourceMismatches == 0 {
		t.Fatalf("backup with a missing Mancode team ref passed verification: %#v", report)
	}
}

func migrationBackupEmptyArrayDigest() string {
	digest := sha256.Sum256([]byte("[]"))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func runMigrationBackupGit(t *testing.T, root, stdin string, args ...string) string {
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

func TestVerifySnapshotDetectsNewEligibleSourceFiles(t *testing.T) {
	sourceRoot, backupRoot := backupFixture(t)
	logsRoot := filepath.Join(sourceRoot, ".beacon", "endpoint", "logs")
	writeBackupFixtureFile(t, filepath.Join(logsRoot, "runtime.jsonl"), validBeaconEventLog(t, "captured source"))
	manifestPath := createBackupFixtureSnapshot(t, sourceRoot)
	writeBackupFixtureFile(t, filepath.Join(logsRoot, "additional", "runtime.jsonl"), validBeaconEventLog(t, "added after snapshot"))
	copyBackupFixtureTree(t, sourceRoot, backupRoot)

	report, err := VerifySnapshot(manifestPath, backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "mismatch" || report.DifferentFiles != 0 || report.SnapshotSourceMismatches == 0 {
		t.Fatalf("new eligible source input was not bound to the prior snapshot: %#v", report)
	}
}

func TestVerifySnapshotRejectsOverlappingAndLinkedTrees(t *testing.T) {
	sourceRoot, backupRoot := backupFixture(t)
	writeBackupFixtureFile(t, filepath.Join(sourceRoot, ".beacon", "endpoint", "logs", "runtime.jsonl"), validBeaconEventLog(t, "fixture"))
	manifestPath := createBackupFixtureSnapshot(t, sourceRoot)
	if _, err := VerifySnapshot(manifestPath, sourceRoot); err == nil {
		t.Fatal("same source and backup root was accepted")
	}
	nestedBackup := filepath.Join(sourceRoot, "nested-backup")
	if err := os.MkdirAll(nestedBackup, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySnapshot(manifestPath, nestedBackup); err == nil {
		t.Fatal("nested backup root was accepted")
	}
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sourceRoot, ".beacon"), filepath.Join(backupRoot, "linked")); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	if _, err := VerifySnapshot(manifestPath, backupRoot); err == nil {
		t.Fatal("backup containing a linked directory was accepted")
	}
}

func TestVerifySnapshotRejectsHardLinkedBackupFiles(t *testing.T) {
	sourceRoot, backupRoot := backupFixture(t)
	sourceFile := filepath.Join(sourceRoot, ".beacon", "endpoint", "logs", "runtime.jsonl")
	writeBackupFixtureFile(t, sourceFile, validBeaconEventLog(t, "hard link fixture"))
	manifestPath := createBackupFixtureSnapshot(t, sourceRoot)
	backupFile := filepath.Join(backupRoot, ".beacon", "endpoint", "logs", "runtime.jsonl")
	if err := os.MkdirAll(filepath.Dir(backupFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(sourceFile, backupFile); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}

	report, err := VerifySnapshot(manifestPath, backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "mismatch" || report.SharedFiles != 1 || report.DifferentFiles != 0 {
		t.Fatalf("hard-linked backup file was not reported: %#v", report)
	}
}

func TestPathWithinTreatsDifferentWindowsVolumesAsDisjoint(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("different-volume paths are specific to Windows")
	}
	if pathWithin(`C:\Threadforge\source`, `D:\Threadforge\backup`) ||
		pathWithin(`D:\Threadforge\backup`, `C:\Threadforge\source`) {
		t.Fatal("independent roots on different volumes were treated as overlapping")
	}
}

func TestSensitiveTreeEntriesUsePathOnlyReceiptsButStillDetectBackupDifferences(t *testing.T) {
	sourceRoot := t.TempDir()
	backupRoot := t.TempDir()
	relative := filepath.ToSlash(filepath.Join("nested", "private-secret.txt"))
	secret := []byte("credential-a")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(sourceRoot, filepath.FromSlash(relative))), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(backupRoot, filepath.FromSlash(relative))), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, filepath.FromSlash(relative)), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupRoot, filepath.FromSlash(relative)), secret, 0o600); err != nil {
		t.Fatal(err)
	}

	source, err := scanTree(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := scanTree(backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	sourceFile := source.files[relative]
	backupFile := backup.files[relative]
	secretDigest := sha256.Sum256(secret)
	if sourceFile.digest != migrate.CredentialPathCommitment(relative) ||
		sourceFile.digest == hex.EncodeToString(secretDigest[:]) || source.sha256 != backup.sha256 || differentFileCount(source, backup) != 0 {
		t.Fatalf("credential tree receipt included its content digest: source=%+v backup=%+v", sourceFile, backupFile)
	}
	credentialManifest := migrate.Manifest{
		SourceFingerprint: strings.Repeat("a", 64),
		Files: []migrate.FileEntry{{
			Path: relative, SourceSHA256: migrate.CredentialPathCommitment(relative),
			SourceBytes: int64(len(secret)), Status: "credential_path_excluded",
		}},
	}
	if mismatches := snapshotSourceMismatchCount(source, credentialManifest, credentialManifest); mismatches != 0 {
		t.Fatalf("snapshot verifier did not honor the credential path-only commitment: mismatches=%d", mismatches)
	}

	if err := os.WriteFile(filepath.Join(backupRoot, filepath.FromSlash(relative)), []byte("credential-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedBackup, err := scanTree(backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if source.sha256 != changedBackup.sha256 || differentFileCount(source, changedBackup) != 1 {
		t.Fatalf("credential content mismatch was not detected without changing the path-only receipt: source=%+v backup=%+v",
			sourceFile, changedBackup.files[relative])
	}
}

func backupFixture(t *testing.T) (string, string) {
	t.Helper()
	sourceRoot := t.TempDir()
	backupRoot := filepath.Join(t.TempDir(), "backup")
	return sourceRoot, backupRoot
}

func validBeaconEventLog(t *testing.T, message string) string {
	t.Helper()
	event := beacon.NewEvent(beacon.NewEventOptions{
		Action: "session.end", Harness: beacon.HarnessInfo{Name: "codex"}, Message: message,
	})
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded) + "\n"
}

func createBackupFixtureSnapshot(t *testing.T, sourceRoot string) string {
	t.Helper()
	dataRoot := t.TempDir()
	manifest, err := migrate.CreateSnapshot(migrate.Beacon, sourceRoot, dataRoot)
	if err != nil {
		t.Fatalf("create verified backup fixture snapshot: %v", err)
	}
	return filepath.Join(dataRoot, "migrations", "snapshots", string(manifest.Product), manifest.SourceFingerprint, "manifest.json")
}

func writeBackupFixtureFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyBackupFixtureTree(t *testing.T, sourceRoot, backupRoot string) {
	t.Helper()
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(sourceRoot, func(sourcePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return os.ErrInvalid
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		destination := filepath.Join(backupRoot, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if !info.Mode().IsRegular() {
			return os.ErrInvalid
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chmod(destination, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copy backup test tree: %v", err)
	}
}
