package devincloud

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStateFileStoresOnlyHashedCursorValuesAndReportsAggregateStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "devin-cloud.v1.json")
	file, err := OpenState(path)
	if err != nil {
		t.Fatal(err)
	}
	sessionKey := stateSessionKey("org-private", "private-session-id")
	eventKey := hashedID("private-event-id")
	entry := file.State().session(sessionKey)
	entry.UpdatedAt = 100
	entry.SyncedAt = 100
	entry.Status = "suspended"
	entry.EndedEmitted = true
	entry.Emitted[eventKey] = true
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"org-private", "private-session-id", "private-event-id"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("state leaked raw source identifier %q: %s", secret, data)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("state permissions = %v, %v; want 0600", info, err)
		}
	}
	status, err := StateStatus(path)
	if err != nil || status["sessions"] != 1 || status["complete"] != 1 || status["pending"] != 0 {
		t.Fatalf("StateStatus = %v, %v", status, err)
	}
	reopened, err := OpenState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.State().Sessions[sessionKey].Emitted[eventKey] {
		t.Fatal("reopened state lost its hashed event cursor")
	}
	_ = reopened.Close()
}

func TestStateRejectsRelativeMalformedAndStaleLockInputs(t *testing.T) {
	if _, err := OpenState("relative/state.json"); err == nil {
		t.Fatal("accepted a relative state path")
	}
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"wrong","sessions":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(path); err == nil {
		t.Fatal("accepted an unsupported state schema")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lock", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(path); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("expected stale lock rejection, got %v", err)
	}
}

func TestStateAndStatusRejectSymlinkedStateFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	link := filepath.Join(root, "link.json")
	if err := os.WriteFile(target, []byte(`{"schema_version":"threadforge.devin-cloud-state.v1","sessions":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := OpenState(link); err == nil {
		t.Fatal("OpenState followed a symlink")
	}
	if _, err := StateStatus(link); err == nil {
		t.Fatal("StateStatus followed a symlink")
	}
}
