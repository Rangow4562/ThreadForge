// Copyright (c) 2026 Asymptote Labs.
// Threadforge-authored; licensed under Apache-2.0. See the repository-root LICENSE.
package sessionlimits

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReadDirAndWalkRespectSharedEntryBudget(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < MaxEntriesPerStore+2; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%05d.jsonl", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	budget := NewBudget()
	var visited int
	if err := budget.WalkDir(root, 2, func(string, os.DirEntry) error {
		visited++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visited != MaxEntriesPerStore {
		t.Fatalf("visited %d entries, want %d", visited, MaxEntriesPerStore)
	}
	if !errors.Is(budget.Warning(), ErrScanLimit) {
		t.Fatalf("warning = %v, want ErrScanLimit", budget.Warning())
	}
}

func TestReadDirBudgetIsSharedAcrossDirectories(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < MaxEntriesPerStore/2+1; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%05d", i)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	budget := NewBudget()
	entries, err := budget.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for _, entry := range entries {
		children, err := budget.ReadDir(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		count += len(children)
	}
	if count != MaxEntriesPerStore-2 {
		t.Fatalf("read %d child entries, want %d", count, MaxEntriesPerStore-2)
	}
	if !errors.Is(budget.Warning(), ErrScanLimit) {
		t.Fatalf("warning = %v, want ErrScanLimit", budget.Warning())
	}
}

func TestReadFileCapsPerFileAndAggregateBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "metadata.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	budget := NewBudget()
	data, err := budget.ReadFile(path)
	if err != nil || string(data) != `{"ok":true}` {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	if err := os.WriteFile(path, make([]byte, MaxMetadataFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := budget.ReadFile(path); err != nil || data != nil {
		t.Fatalf("oversized ReadFile = %d bytes, %v; want omitted metadata", len(data), err)
	}
	if !errors.Is(budget.Warning(), ErrScanLimit) {
		t.Fatalf("warning = %v, want ErrScanLimit", budget.Warning())
	}
}

func TestReadFirstLineDoesNotReadTranscriptTail(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"session_meta\"}\n"+string(make([]byte, MaxMetadataFileBytes+10))), 0o600); err != nil {
		t.Fatal(err)
	}
	budget := NewBudget()
	line, err := budget.ReadFirstLine(path, 64)
	if err != nil || string(line) != `{"type":"session_meta"}` {
		t.Fatalf("ReadFirstLine = %q, %v", line, err)
	}
	if budget.Truncated() {
		t.Fatal("reading a bounded first line should not mark an unused transcript tail truncated")
	}
}

func TestReadLinesStopsAtConfiguredPrefix(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	contents := "one\ntwo\nthree\n" + string(make([]byte, MaxMetadataFileBytes))
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	budget := NewBudget()
	lines, err := budget.ReadLines(path, 2, 64)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || string(lines[0]) != "one" || string(lines[1]) != "two" {
		t.Fatalf("lines = %q, want the first two rows", lines)
	}
	if budget.Truncated() {
		t.Fatal("stopping after the requested prefix should not report truncation")
	}
}
