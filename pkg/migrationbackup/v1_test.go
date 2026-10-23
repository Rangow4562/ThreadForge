package migrationbackup

import (
	"encoding/json"
	"os"
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
