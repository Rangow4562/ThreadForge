package handoffbridge

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/handoff"
)

func TestReadMarksOnlyVerifiedCursorCLITranscriptsAsNativelyResumable(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	projectsDir := filepath.Join(root, "projects")
	configDir := filepath.Join(root, "cursor-config")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(key, root)
	}
	t.Setenv("APPDATA", filepath.Join(root, "appdata"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	t.Setenv("CURSOR_CONFIG_DIR", configDir)

	projectName := cursorEncodedProjectName(workspace)
	transcriptDir := filepath.Join(projectsDir, projectName, "agent-transcripts")
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const sessionID = "cursor-session-bridge-123"
	transcriptPath := filepath.Join(transcriptDir, sessionID+".jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"user_message","id":"u1","text":"fixture"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum([]byte(resolvedWorkspace))
	chatStore := filepath.Join(configDir, "chats", hex.EncodeToString(digest[:]), sessionID, "store.db")
	if err := os.MkdirAll(filepath.Dir(chatStore), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chatStore, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := Read(ReadRequest{
		SchemaVersion: ReadRequestSchemaVersion,
		Harness:       handoff.HarnessCursor,
		SessionID:     sessionID,
		StoreDirs:     map[string]string{handoff.HarnessCursor: projectsDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Session.Store != "agent_transcript" || !response.Session.NativeResume {
		t.Fatalf("Cursor transcript resume eligibility = store %q, resume %t", response.Session.Store, response.Session.NativeResume)
	}
	if len(response.Events) == 0 {
		t.Fatal("expected the native transcript to remain readable")
	}

	encoded, err := json.Marshal(response.Session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "native_resume") || strings.Contains(string(encoded), transcriptPath) || strings.Contains(string(encoded), chatStore) {
		t.Fatalf("private resume eligibility or local paths escaped the internal bridge result: %s", encoded)
	}
}

func cursorEncodedProjectName(path string) string {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	if volume != "" && runtime.GOOS == "windows" {
		rest := strings.TrimLeft(strings.TrimPrefix(clean, volume), `\\/`)
		return strings.ToLower(volume[:1]) + "-" + strings.ReplaceAll(filepath.ToSlash(rest), "/", "-")
	}
	return strings.ReplaceAll(filepath.ToSlash(clean), "/", "-")
}
