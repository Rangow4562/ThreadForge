// Copyright (c) 2026 Asymptote Labs.
// Threadforge-authored test; licensed under Apache-2.0. See the repository-root LICENSE.
package handoff

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/endpoint/schema"
	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/sessionlimits"
)

func TestCheckReadPathRejectsOversizedNativeSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, sessionlimits.MaxMetadataTotalBytes+1); err != nil {
		t.Fatal(err)
	}
	session := Session{readBudget: sessionlimits.NewBudget()}
	if err := session.checkReadPath(path); !errors.Is(err, sessionlimits.ErrScanLimit) {
		t.Fatalf("checkReadPath error = %v, want scan limit", err)
	}
}

func TestCheckReadPathRejectsSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, "session.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	session := Session{readBudget: sessionlimits.NewBudget()}
	if err := session.checkReadPath(filepath.Join(link, "session.jsonl")); err == nil {
		t.Fatal("read through a symlinked parent was accepted")
	}
}

func TestEventsCapsMappedNativeSessionEvents(t *testing.T) {
	source := cappedEventSource{events: make([]schema.Event, MaxNativeSessionEvents+1)}
	events, err := Events([]Source{source}, Session{Harness: source.Harness()})
	if !errors.Is(err, ErrNativeSessionEventLimit) {
		t.Fatalf("Events error = %v, want event-limit warning", err)
	}
	if len(events) != MaxNativeSessionEvents {
		t.Fatalf("events = %d, want %d", len(events), MaxNativeSessionEvents)
	}
}

type cappedEventSource struct {
	events []schema.Event
}

func (s cappedEventSource) Harness() string                        { return "test" }
func (s cappedEventSource) List() ([]Session, error)               { return nil, nil }
func (s cappedEventSource) Events(Session) ([]schema.Event, error) { return s.events, nil }
