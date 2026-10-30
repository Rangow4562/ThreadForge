package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/migrationsources"
)

func TestMancodeSourceSetDiscoversAndBindsProjectAndCoordinationSnapshots(t *testing.T) {
	projectRoot := t.TempDir()
	ids := []string{
		"00000000000000000000000001",
		"00000000000000000000000002",
	}
	operationIDs := []string{
		"00000000000000000000000003",
		"00000000000000000000000004",
	}
	gitRoot := filepath.Join(projectRoot, ".git", "mancode", "workspaces", ids[0])
	nonGitRoot := filepath.Join(projectRoot, ".mancode", "runtime", "non-git", ids[1])
	writeMancodeCoordinationOperation(t, gitRoot, operationIDs[0])
	writeMancodeCoordinationOperation(t, nonGitRoot, operationIDs[1])

	discovered, err := DiscoverMancodeSourceRoots(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 3 {
		t.Fatalf("discovered source roots = %d, want project plus two coordination roots: %#v", len(discovered), discovered)
	}

	dataRoot := t.TempDir()
	manifests := make([]string, 0, len(discovered))
	for _, source := range discovered {
		snapshot, err := CreateSnapshot(Mancode, source.SourceRoot, dataRoot)
		if err != nil {
			t.Fatalf("snapshot %s source: %v", source.Role, err)
		}
		manifests = append(manifests, filepath.Join(dataRoot, "migrations", "snapshots", "mancode", snapshot.SourceFingerprint, "manifest.json"))
	}

	report, err := BuildMancodeSourceSet(projectRoot, manifests)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationsources.ValidateMancode(report); err != nil {
		t.Fatalf("built source set is invalid: %v", err)
	}
	roles := map[string]bool{}
	for _, component := range report.Components {
		roles[component.Role] = true
	}
	for _, role := range []string{"project", "git_coordination", "non_git_coordination"} {
		if !roles[role] {
			t.Fatalf("source-set role %q missing from %#v", role, report.Components)
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, privateValue := range append(append([]string{}, ids...), append(operationIDs, projectRoot, gitRoot, nonGitRoot)...) {
		if strings.Contains(string(encoded), privateValue) {
			t.Fatalf("shareable source-set inventory retained local identity/path %q: %s", privateValue, encoded)
		}
	}
}

func TestMancodeSourceSetRejectsMissingDuplicateAndStaleSnapshots(t *testing.T) {
	projectRoot := t.TempDir()
	workspaceID := "00000000000000000000000001"
	operationID := "00000000000000000000000003"
	coordinationRoot := filepath.Join(projectRoot, ".git", "mancode", "workspaces", workspaceID)
	operationPath := writeMancodeCoordinationOperation(t, coordinationRoot, operationID)
	discovered, err := DiscoverMancodeSourceRoots(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := t.TempDir()
	manifestPaths := make([]string, 0, len(discovered))
	for _, source := range discovered {
		snapshot, err := CreateSnapshot(Mancode, source.SourceRoot, dataRoot)
		if err != nil {
			t.Fatal(err)
		}
		manifestPaths = append(manifestPaths,
			filepath.Join(dataRoot, "migrations", "snapshots", "mancode", snapshot.SourceFingerprint, "manifest.json"))
	}
	if _, err := BuildMancodeSourceSet(projectRoot, manifestPaths[:1]); err == nil {
		t.Fatal("source set accepted a missing coordination snapshot")
	}
	if _, err := BuildMancodeSourceSet(projectRoot, []string{manifestPaths[0], manifestPaths[1], manifestPaths[1]}); err == nil {
		t.Fatal("source set accepted a duplicated component snapshot")
	}
	if err := os.WriteFile(operationPath, []byte(`{"schemaVersion":1,"operationId":"`+operationID+`","type":"claim_create","state":"committed","startedAt":"2026-10-18T10:00:00Z","updatedAt":"2026-10-18T10:01:00Z","changed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildMancodeSourceSet(projectRoot, manifestPaths); err == nil {
		t.Fatal("source set accepted a source changed after snapshot")
	}
}

func TestMancodeSourceSetSurfacesCrossRootDuplicateRecordKeys(t *testing.T) {
	projectRoot := t.TempDir()
	workspaceIDs := []string{"00000000000000000000000001", "00000000000000000000000002"}
	operationID := "00000000000000000000000003"
	roots := []string{
		filepath.Join(projectRoot, ".git", "mancode", "workspaces", workspaceIDs[0]),
		filepath.Join(projectRoot, ".mancode", "runtime", "non-git", workspaceIDs[1]),
	}
	for _, root := range roots {
		writeMancodeCoordinationOperation(t, root, operationID)
	}
	discovered, err := DiscoverMancodeSourceRoots(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := t.TempDir()
	manifests := make([]string, 0, len(discovered))
	for _, source := range discovered {
		snapshot, err := CreateSnapshot(Mancode, source.SourceRoot, dataRoot)
		if err != nil {
			t.Fatal(err)
		}
		manifests = append(manifests, filepath.Join(dataRoot, "migrations", "snapshots", "mancode", snapshot.SourceFingerprint, "manifest.json"))
	}
	report, err := BuildMancodeSourceSet(projectRoot, manifests)
	if err != nil {
		t.Fatal(err)
	}
	if report.DuplicateRecordKeys == 0 || report.DuplicateRecordKeysSHA256 == "" {
		t.Fatalf("cross-root record-key duplicate was not made visible: %#v", report)
	}
}

func TestDiscoverMancodeSourceRootsRejectsUnsupportedInventoryEntries(t *testing.T) {
	projectRoot := t.TempDir()
	workspaceRoot := filepath.Join(projectRoot, ".git", "mancode", "workspaces")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspaceRoot, "unknown-workspace"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverMancodeSourceRoots(projectRoot); err == nil {
		t.Fatal("source discovery ignored an unsupported coordination inventory entry")
	}
}

func TestDiscoverMancodeSourceRootsFollowsLinkedWorktreeCommonDirectory(t *testing.T) {
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "worktree")
	gitCommon := filepath.Join(parent, "common.git")
	gitDirectory := filepath.Join(gitCommon, "worktrees", "active")
	workspaceID := "00000000000000000000000001"
	workspaceRoot := filepath.Join(gitCommon, "mancode", "workspaces", workspaceID)
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gitDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".git"), []byte("gitdir: ../common.git/worktrees/active\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDirectory, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	roots, err := DiscoverMancodeSourceRoots(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0].Role != "git_coordination" && roots[1].Role != "git_coordination" {
		t.Fatalf("linked-worktree coordination root was not discovered: %#v", roots)
	}
}

func writeMancodeCoordinationOperation(t *testing.T, root, operationID string) string {
	t.Helper()
	path := filepath.Join(root, "operations", operationID+".json")
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join("operations", operationID+".json")), map[string]any{
		"schemaVersion": 1,
		"operationId":   operationID,
		"type":          "claim_create",
		"state":         "repair_required",
		"startedAt":     "2026-10-18T10:00:00Z",
		"updatedAt":     "2026-10-18T10:01:00Z",
	})
	return path
}
