package handoff

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeResumeAvailableRequiresCursorCLIChatStore(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	config := filepath.Join(root, "cursor-config")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_CONFIG_DIR", config)

	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum([]byte(resolvedWorkspace))
	storePath := filepath.Join(config, "chats", hex.EncodeToString(digest[:]), "cursor-session-123", "store.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	eligible := Session{Harness: HarnessCursor, ID: "cursor-session-123", Directory: workspace, Store: "agent_transcript"}
	if !NativeResumeAvailable(eligible) {
		t.Fatal("Cursor CLI transcript with a regular chat store should be natively resumable")
	}

	cases := []struct {
		name    string
		session Session
	}{
		{name: "composer record", session: Session{Harness: HarnessCursor, ID: eligible.ID, Directory: workspace, Store: "global_storage"}},
		{name: "subagent", session: Session{Harness: HarnessCursor, ID: eligible.ID, Directory: workspace, Store: "agent_transcript", Subagent: true}},
		{name: "wrong harness", session: Session{Harness: HarnessClaude, ID: eligible.ID, Directory: workspace, Store: "agent_transcript"}},
		{name: "missing id", session: Session{Harness: HarnessCursor, Directory: workspace, Store: "agent_transcript"}},
		{name: "missing directory", session: Session{Harness: HarnessCursor, ID: eligible.ID, Store: "agent_transcript"}},
		{name: "missing chat database", session: Session{Harness: HarnessCursor, ID: "other-session", Directory: workspace, Store: "agent_transcript"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if NativeResumeAvailable(tc.session) {
				t.Fatal("session without a matching top-level Cursor CLI store was marked resumable")
			}
		})
	}
}

func TestNativeResumeAvailableRejectsSymlinkedChatStore(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	config := filepath.Join(root, "cursor-config")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_CONFIG_DIR", config)
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum([]byte(resolvedWorkspace))
	storePath := filepath.Join(config, "chats", hex.EncodeToString(digest[:]), "cursor-session-link", "store.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside.db")
	if err := os.WriteFile(target, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, storePath); err != nil {
		t.Skipf("file symlink creation is unavailable: %v", err)
	}
	if NativeResumeAvailable(Session{Harness: HarnessCursor, ID: "cursor-session-link", Directory: workspace, Store: "agent_transcript"}) {
		t.Fatal("symlinked Cursor chat database must not establish native-resume eligibility")
	}
}
