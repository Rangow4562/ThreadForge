package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMancodeGitRefSnapshotImportsOnlyReviewSummaries(t *testing.T) {
	root, commit, manifestBytes := mancodeGitRefFixture(t, "git-ref", false)
	withoutOptIn, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if withoutOptIn.Summary.RecordsRejected == 0 {
		t.Fatal("git-ref project authority was accepted without explicit opt-in")
	}

	manifest, err := InspectWithOptions(Mancode, root, InspectOptions{IncludeMancodeGitRef: true})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.MancodeGitRef == nil || manifest.MancodeGitRef.Ref != mancodeTeamGitRef ||
		manifest.MancodeGitRef.Commit != commit || manifest.MancodeGitRef.ManifestSHA256 != digest(manifestBytes) ||
		manifest.MancodeGitRef.ManifestBytes != int64(len(manifestBytes)) || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("local Mancode ref was not bound and reconciled: %#v", manifest)
	}
	var virtual *FileEntry
	for i := range manifest.Files {
		if manifest.Files[i].Path == mancodeGitRefVirtualPath {
			virtual = &manifest.Files[i]
		}
	}
	if virtual == nil || !virtual.Virtual || virtual.ImportedCount != 3 || virtual.RejectedCount != 0 {
		t.Fatalf("git-ref source did not produce authority, actor, and receipt summaries: %#v", virtual)
	}
	if err := VerifyManifest(manifest); err != nil {
		t.Fatalf("valid git-ref migration manifest failed verification: %v", err)
	}

	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshotWithOptions(Mancode, root, dataRoot, InspectOptions{IncludeMancodeGitRef: true})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	coverage, err := BuildFormatCoverage(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.UnclassifiedFiles != 0 || coverage.UnsupportedFiles != 0 || coverage.RejectedRecords != 0 {
		t.Fatalf("valid, fully converted git-ref fixture did not reconcile: %#v", coverage)
	}
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("expected project config plus three git-ref summaries, got %d records", len(records))
	}
	types := make(map[string]bool)
	for _, record := range records {
		types[record.Type] = true
		if record.Scope != "local_pending_review" {
			t.Fatalf("git-ref record escaped local review: %#v", record)
		}
		if record.SourcePath != mancodeGitRefVirtualPath {
			continue
		}
		for _, private := range []string{mancodeDecisionTestID("1"), mancodeDecisionTestID("2"), "Private Reviewer", "private-actor-marker"} {
			if strings.Contains(string(record.Payload), private) {
				t.Fatalf("git-ref record retained source identity %q: %s", private, record.Payload)
			}
		}
	}
	if !types["mancode_git_ref_authority"] || !types["mancode_actor_profile"] ||
		!types["mancode_git_ref_receipt"] || !types["mancode_project_config"] {
		t.Fatalf("snapshot omitted a required review summary: %#v", types)
	}
	if strings.Contains(string(manifestBytes), "private-actor-marker") == false {
		t.Fatal("test fixture did not include its private data marker")
	}
	for _, name := range []string{"mancode-git-ref-authority-v1.schema.json", "mancode-git-ref-receipt-v1.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "schema", name))
		if err != nil || !json.Valid(data) {
			t.Fatalf("new migration record schema %s is missing or invalid JSON: %v", name, err)
		}
	}
}

func TestMancodeGitRefUnsupportedTaskBundleBlocksCoverageWithoutCopyingBody(t *testing.T) {
	root, _, _ := mancodeGitRefFixture(t, "git-ref", true)
	dataRoot := t.TempDir()
	manifest, err := CreateSnapshotWithOptions(Mancode, root, dataRoot, InspectOptions{IncludeMancodeGitRef: true})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsRejected == 0 {
		t.Fatal("unsupported team task bundle did not block migration")
	}
	coverage, err := BuildFormatCoverage(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.RejectedRecords == 0 || coverage.UnclassifiedFiles != 0 {
		t.Fatalf("unsupported task bundle was not classified as a rejection: %#v", coverage)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), manifest.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, manifest)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-task-bundle-marker") {
		t.Fatal("unsupported task bundle body was copied into normalized snapshot records")
	}
}

func TestMancodeGitRefCoordinationSummariesAreBoundAndIdentityReduced(t *testing.T) {
	root, _, baseBytes := mancodeGitRefFixture(t, "git-ref", false)
	value, err := decodeStrictMancodeGitRefJSON(baseBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifest := value.(map[string]any)
	workspaceID := mancodeDecisionTestID("1")
	ids := mancodeCoordinationFixtureIDs()
	ids["workspace"] = workspaceID
	claim := mancodeCoordinationClaimFixture(ids)
	claim["authority"] = map[string]any{"mode": "git-ref", "remoteRevision": "1"}
	handoff := mancodeCoordinationHandoffFixture(ids)
	handoff["transport"].(map[string]any)["transportRevision"] = json.Number("1")
	bundleDigest := "sha256:" + strings.Repeat("e", 64)
	handoff["transport"].(map[string]any)["taskBundleDigest"] = bundleDigest
	aggregateDigest := "sha256:" + strings.Repeat("f", 64)
	fence := map[string]any{
		"schemaVersion": json.Number("1"), "taskRef": map[string]any{"namespace": "shared", "taskId": ids["task"]},
		"ownerActorId": ids["fromActor"], "ownershipEpoch": json.Number("2"), "taskRevision": json.Number("3"),
		"aggregateDigest": aggregateDigest, "remoteRevision": json.Number("1"),
		"lastOperationId": ids["operation"], "updatedAt": "2026-10-18T10:02:00Z",
	}
	bundle := map[string]any{
		"schemaVersion": json.Number("1"), "taskRef": map[string]any{"namespace": "shared", "taskId": ids["task"]},
		"taskRevision": json.Number("3"), "ownershipEpoch": json.Number("2"),
		"aggregateDigest": aggregateDigest, "bundleDigest": bundleDigest,
	}
	manifest["claims"] = []any{claim}
	manifest["handoffs"] = []any{handoff}
	manifest["ownershipFences"] = []any{fence}
	manifest["taskBundles"] = []any{bundle}
	items, err := normalizeMancodeGitRefManifest(root, manifest,
		MancodeGitRefSource{Ref: mancodeTeamGitRef, Commit: strings.Repeat("a", 40), ManifestSHA256: digest(baseBytes), ManifestBytes: int64(len(baseBytes))},
		workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	rejected := 0
	for _, item := range items {
		if item.Rejected {
			rejected++
			continue
		}
		seen[item.Type] = true
		for _, private := range []string{workspaceID, ids["claim"], ids["handoff"], ids["task"], ids["workspace"], ids["fromActor"], ids["toActor"], "private completed detail", "private next task"} {
			if strings.Contains(string(item.Payload), private) {
				t.Fatalf("Mancode git-ref %s summary retained private source value %q: %s", item.Type, private, item.Payload)
			}
		}
	}
	for _, recordType := range []string{"mancode_git_ref_authority", "mancode_actor_profile", "mancode_git_ref_receipt",
		"mancode_team_claim", "mancode_team_handoff", "mancode_team_head_fence"} {
		if !seen[recordType] {
			t.Errorf("valid git-ref source omitted review summary %q", recordType)
		}
	}
	if rejected != 1 {
		t.Fatalf("unsupported bundle should be the sole blocked item, got %d: %#v", rejected, items)
	}
	invalid := make(map[string]any, len(manifest))
	for key, value := range manifest {
		invalid[key] = value
	}
	wrongWorkspaceClaim := make(map[string]any, len(claim))
	for key, value := range claim {
		wrongWorkspaceClaim[key] = value
	}
	wrongWorkspaceClaim["workspaceId"] = mancodeDecisionTestID("8")
	invalid["claims"] = []any{wrongWorkspaceClaim}
	if _, err := normalizeMancodeGitRefManifest(root, invalid,
		MancodeGitRefSource{Ref: mancodeTeamGitRef, Commit: strings.Repeat("a", 40), ManifestSHA256: digest(baseBytes), ManifestBytes: int64(len(baseBytes))},
		workspaceID); err == nil {
		t.Fatal("claim from a different workspace was accepted under the git-ref authority summary")
	}
}

func TestMancodeGitRefFailsClosedForMissingRefWrongTransportAndDuplicateKeys(t *testing.T) {
	t.Run("missing local ref", func(t *testing.T) {
		root, _, _ := mancodeGitRefFixtureWithoutRef(t, "git-ref")
		if _, err := InspectWithOptions(Mancode, root, InspectOptions{IncludeMancodeGitRef: true}); err == nil || !strings.Contains(err.Error(), "available locally") {
			t.Fatalf("missing local team ref was not reported safely: %v", err)
		}
	})
	t.Run("wrong transport", func(t *testing.T) {
		root, _, _ := mancodeGitRefFixture(t, "local", false)
		if _, err := InspectWithOptions(Mancode, root, InspectOptions{IncludeMancodeGitRef: true}); err == nil {
			t.Fatal("git-ref opt-in accepted a project configured for local transport")
		}
	})
	t.Run("symlinked project config directory", func(t *testing.T) {
		root, _, _ := mancodeGitRefFixture(t, "git-ref", false)
		sharedPath := filepath.Join(root, ".mancode", "shared")
		configPath := filepath.Join(sharedPath, "config.json")
		config, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		externalShared := filepath.Join(t.TempDir(), "shared")
		if err := os.MkdirAll(externalShared, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(externalShared, "config.json"), config, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(configPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(sharedPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(externalShared, sharedPath); err != nil {
			t.Skipf("directory symlinks are unavailable: %v", err)
		}
		if _, err := InspectWithOptions(Mancode, root, InspectOptions{IncludeMancodeGitRef: true}); err == nil {
			t.Fatal("git-ref import read a project config through an external symlink")
		}
	})
	t.Run("duplicate manifest keys", func(t *testing.T) {
		root, _, _ := mancodeGitRefFixture(t, "git-ref", false)
		writeMancodeGitRefCommit(t, root, []byte(`{"workspaceId":"duplicate","workspaceId":"duplicate"}`))
		if _, err := InspectWithOptions(Mancode, root, InspectOptions{IncludeMancodeGitRef: true}); err == nil {
			t.Fatal("ambiguous duplicate-key team manifest was accepted")
		}
	})
	t.Run("zero committed revision", func(t *testing.T) {
		root, _, source := mancodeGitRefFixture(t, "git-ref", false)
		value, err := decodeStrictMancodeGitRefJSON(source)
		if err != nil {
			t.Fatal(err)
		}
		value.(map[string]any)["revision"] = 0
		invalid, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		writeMancodeGitRefCommit(t, root, invalid)
		inspected, err := CreateSnapshotWithOptions(Mancode, root, t.TempDir(), InspectOptions{IncludeMancodeGitRef: true})
		if err != nil {
			t.Fatal(err)
		}
		coverage, err := BuildFormatCoverage(inspected)
		if err != nil {
			t.Fatal(err)
		}
		var rejectedVirtual bool
		for _, file := range inspected.Files {
			if file.Path == mancodeGitRefVirtualPath && file.Status == "unsupported" && file.RejectedCount > 0 {
				rejectedVirtual = true
			}
		}
		if !rejectedVirtual || inspected.Summary.RecordsRejected == 0 || coverage.RejectedRecords == 0 || coverage.UnclassifiedFiles != 0 {
			t.Fatalf("invalid committed revision did not become a cutover-blocking classified rejection: manifest=%#v coverage=%#v", inspected.Summary, coverage)
		}
	})
	t.Run("option restricted to Mancode", func(t *testing.T) {
		if _, err := InspectWithOptions(Beacon, t.TempDir(), InspectOptions{IncludeMancodeGitRef: true}); err == nil {
			t.Fatal("Mancode git-ref option was accepted for another product")
		}
	})
}

func mancodeGitRefFixture(t *testing.T, transportMode string, includeUnsupportedTaskBundle bool) (string, string, []byte) {
	t.Helper()
	root, _, _ := mancodeGitRefFixtureWithoutRef(t, transportMode)
	workspaceID := mancodeDecisionTestID("1")
	profile := map[string]any{
		"schemaVersion": 1, "actorId": mancodeDecisionTestID("2"), "displayName": "private-actor-marker",
		"joinedAt": "2026-10-08T10:00:00Z", "updatedAt": "2026-10-08T12:00:00Z",
	}
	profiles := []any{profile}
	profileDigest, err := mancodeCanonicalDigest(profiles)
	if err != nil {
		t.Fatal(err)
	}
	emptyDigest, err := mancodeCanonicalDigest([]any{})
	if err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{
		"schemaVersion": 1, "kind": "actor_profile", "operationId": workspaceID, "actorId": mancodeDecisionTestID("2"),
		"taskRef": nil, "remoteRevision": 1, "ownershipEpoch": nil,
		"entityDigests": map[string]any{
			"actorProfiles": profileDigest, "ownershipFence": nil, "claims": emptyDigest, "handoffs": emptyDigest, "taskBundle": nil,
		},
		"committedAt": "2026-10-08T12:00:00Z",
	}
	manifest := map[string]any{
		"schemaVersion": 1, "workspaceId": workspaceID, "schemaEpoch": workspaceID,
		"minReaderVersion": "0.0.0", "minWriterVersion": "0.0.0", "transportEpoch": 1,
		"configRevision": 1, "configDigest": "sha256:" + strings.Repeat("a", 64),
		"authorityState": "active", "authorityFreeze": nil, "authorityTombstone": nil,
		"revision": 1, "lastOperationId": workspaceID,
		"actorProfiles":   profiles,
		"ownershipFences": []any{}, "claims": []any{}, "handoffs": []any{},
		"taskBundles": []any{}, "receipts": []any{receipt}, "lastMutation": receipt,
		"updatedAt": "2026-10-08T12:00:00Z",
	}
	if includeUnsupportedTaskBundle {
		manifest["taskBundles"] = []any{map[string]any{"private": "private-task-bundle-marker"}}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	commit := writeMancodeGitRefCommit(t, root, data)
	return root, commit, data
}

func mancodeGitRefFixtureWithoutRef(t *testing.T, transportMode string) (string, string, []byte) {
	t.Helper()
	root := t.TempDir()
	runMancodeGitRefGit(t, root, "", "init", "--quiet")
	runMancodeGitRefGit(t, root, "", "config", "user.name", "Threadforge Test")
	runMancodeGitRefGit(t, root, "", "config", "user.email", "threadforge-test@example.invalid")
	workspaceID := mancodeDecisionTestID("1")
	writeMancodeJSONFixture(t, root, ".mancode/shared/config.json", map[string]any{
		"schemaVersion": 1, "revision": 1, "workspaceId": workspaceID,
		"transport":       map[string]any{"mode": transportMode, "epoch": 1},
		"lastOperationId": workspaceID, "updatedAt": "2026-10-08T12:00:00Z",
	})
	return root, "", nil
}

func writeMancodeGitRefCommit(t *testing.T, root string, manifest []byte) string {
	t.Helper()
	blob := runMancodeGitRefGit(t, root, string(manifest), "hash-object", "-w", "--stdin")
	tree := runMancodeGitRefGit(t, root, fmt.Sprintf("100644 blob %s\tmanifest.json\n", strings.TrimSpace(blob)), "mktree")
	commit := runMancodeGitRefGit(t, root, "", "commit-tree", strings.TrimSpace(tree), "-m", "fixture")
	commit = strings.TrimSpace(commit)
	runMancodeGitRefGit(t, root, "", "update-ref", mancodeTeamGitRef, commit)
	return commit
}

func runMancodeGitRefGit(t *testing.T, root, stdin string, args ...string) string {
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
