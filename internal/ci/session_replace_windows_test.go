//go:build windows

package ci

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReplaceSessionStateFileRetriesTransientWindowsLockErrors(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "state.tmp")
	destination := filepath.Join(dir, "state.json")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	attempts, sleeps := 0, 0
	rename := func(oldPath, newPath string) error {
		attempts++
		if attempts < 4 {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.Errno(32)}
		}
		return os.Rename(oldPath, newPath)
	}
	sleep := func(duration time.Duration) {
		if duration != 25*time.Millisecond {
			t.Errorf("retry delay = %s, want 25ms", duration)
		}
		sleeps++
	}
	if err := replaceSessionStateFileWith(source, destination, rename, sleep); err != nil {
		t.Fatal(err)
	}
	if attempts != 4 || sleeps != 3 {
		t.Fatalf("replace attempts=%d sleeps=%d, want 4 and 3", attempts, sleeps)
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "new" {
		t.Fatalf("replacement content=%q err=%v", content, err)
	}
}

func TestReplaceSessionStateFileDoesNotRetryPermanentErrors(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "state.tmp")
	destination := filepath.Join(dir, "state.json")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	attempts, sleeps := 0, 0
	wantErr := syscall.Errno(87)
	err := replaceSessionStateFileWith(source, destination, func(string, string) error {
		attempts++
		return &os.LinkError{Op: "rename", Old: source, New: destination, Err: wantErr}
	}, func(time.Duration) { sleeps++ })
	if err == nil || attempts != 1 || sleeps != 0 {
		t.Fatalf("permanent error handling err=%v attempts=%d sleeps=%d", err, attempts, sleeps)
	}
}

func TestSessionStateReplacementRecognizesWindowsTransientCodes(t *testing.T) {
	for _, code := range []syscall.Errno{syscall.Errno(5), syscall.Errno(32), syscall.Errno(33)} {
		err := &os.LinkError{Op: "rename", Old: "source", New: "target", Err: code}
		if !isTransientSessionStateReplaceError(err) {
			t.Errorf("Windows error %d was not classified as transient", code)
		}
	}
	if isTransientSessionStateReplaceError(&os.LinkError{Op: "rename", Old: "source", New: "target", Err: syscall.Errno(87)}) {
		t.Fatal("invalid-parameter error was classified as transient")
	}
}
