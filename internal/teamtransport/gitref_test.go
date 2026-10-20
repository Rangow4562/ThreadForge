package teamtransport

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/internal/procedurefiles"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

type allowTeamScope struct{}

func (allowTeamScope) WithActiveTeamProjectScope(_ context.Context, _ string, write func() error) error {
	return write()
}

func TestGitRefSyncAcrossClonesUsesCASAndOnlyPublishesApprovedProcedures(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "team.git")
	runGitTest(t, root, "init", "--bare", "--quiet", remote)
	cloneA := newGitTestClone(t, root, "clone-a", remote)
	cloneB := newGitTestClone(t, root, "clone-b", remote)
	policy, identity := newTeamTrustFixture(t)
	storeA := newProcedureStoreForSyncTest(t, cloneA, policy)
	storeB := newProcedureStoreForSyncTest(t, cloneB, policy)
	publishTeamProcedure(t, storeA, identity, "procedure-one", "A reviewed shared procedure")
	publishTeamProcedure(t, storeB, identity, "procedure-two", "Another reviewed shared procedure")

	branchA := strings.TrimSpace(string(runGitTest(t, cloneA, "symbolic-ref", "--short", "HEAD")))
	statusA := string(runGitTest(t, cloneA, "status", "--porcelain"))
	stateA := filepath.Join(root, "state-a")
	stateB := filepath.Join(root, "state-b")
	first, err := Push(context.Background(), cloneA, "origin", "demo-project", 0, storeA, stateA)
	if err != nil || first.RemoteRevision != 1 || first.Status != "published" {
		t.Fatalf("first push = %#v, err=%v", first, err)
	}
	absoluteA, remoteIdentity, teamRef, err := inspectRemote(context.Background(), cloneA, "origin", "demo-project")
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot, err := readSnapshot(context.Background(), absoluteA, "origin", teamRef, remoteIdentity, "demo-project")
	if err != nil || firstSnapshot == nil || firstSnapshot.Manifest.Revision != 1 {
		t.Fatalf("read first remote revision = %#v, err=%v", firstSnapshot, err)
	}
	if got, err := Push(context.Background(), cloneB, "origin", "demo-project", 0, storeB, stateB); err == nil || !strings.Contains(err.Error(), "expected revision") {
		t.Fatalf("stale push = %#v, err=%v", got, err)
	}
	pulled, err := Pull(context.Background(), cloneB, "origin", "demo-project", storeB, stateB)
	if err != nil || pulled.ImportedRecords != 1 || pulled.RemoteRevision != 1 {
		t.Fatalf("pull into second clone = %#v, err=%v", pulled, err)
	}
	second, err := Push(context.Background(), cloneB, "origin", "demo-project", 1, storeB, stateB)
	if err != nil || second.RemoteRevision != 2 {
		t.Fatalf("second push = %#v, err=%v", second, err)
	}
	updated, err := Pull(context.Background(), cloneA, "origin", "demo-project", storeA, stateA)
	if err != nil || updated.ImportedRecords != 1 || updated.RemoteRevision != 2 {
		t.Fatalf("updated pull into first clone = %#v, err=%v", updated, err)
	}
	graphs, err := storeA.ListProject("demo-project")
	if err != nil || len(graphs) != 2 {
		t.Fatalf("clone A approved procedures = %#v, err=%v", graphs, err)
	}
	if got := strings.TrimSpace(string(runGitTest(t, cloneA, "symbolic-ref", "--short", "HEAD"))); got != branchA {
		t.Fatalf("sync changed the current branch from %q to %q", branchA, got)
	}
	if got := string(runGitTest(t, cloneA, "status", "--porcelain")); got != statusA {
		t.Fatalf("sync changed the project worktree status:\nbefore=%q\nafter=%q", statusA, got)
	}
	runGitTest(t, root, "--git-dir", remote, "update-ref", teamRef, firstSnapshot.Commit)
	if _, err := Pull(context.Background(), cloneA, "origin", "demo-project", storeA, stateA); err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("remote rollback was not rejected: %v", err)
	}
	for _, record := range mustExportTeamRecords(t, storeA) {
		if strings.Contains(string(record.Content), "local-trace-only-secret") {
			t.Fatal("local trace reference leaked into the team transport payload")
		}
	}
}

func TestGitRefSyncRejectsTamperedSnapshotBeforeMaterialization(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "team.git")
	runGitTest(t, root, "init", "--bare", "--quiet", remote)
	cloneA := newGitTestClone(t, root, "clone-a", remote)
	cloneB := newGitTestClone(t, root, "clone-b", remote)
	policy, identity := newTeamTrustFixture(t)
	storeA := newProcedureStoreForSyncTest(t, cloneA, policy)
	storeB := newProcedureStoreForSyncTest(t, cloneB, policy)
	publishTeamProcedure(t, storeA, identity, "procedure-one", "A reviewed shared procedure")
	if _, err := Push(context.Background(), cloneA, "origin", "demo-project", 0, storeA, filepath.Join(root, "state-a")); err != nil {
		t.Fatal(err)
	}

	_, remoteIdentity, ref, err := inspectRemote(context.Background(), cloneA, "origin", "demo-project")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := readSnapshot(context.Background(), cloneA, "origin", ref, remoteIdentity, "demo-project")
	if err != nil || snapshot == nil {
		t.Fatalf("read published snapshot = %#v, err=%v", snapshot, err)
	}
	bad := makeManifest("demo-project", remoteIdentity, 2, snapshot.ManifestSHA256, snapshot.Records)
	bad.Records[0].SHA256 = strings.Repeat("0", 64)
	manifestBytes, err := encodeManifest(bad)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := createSnapshotCommit(context.Background(), cloneA, snapshot.Commit, ref, snapshot.Records, manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := pushWithLease(context.Background(), cloneA, "origin", ref, snapshot.Commit, commit); err != nil {
		t.Fatal(err)
	}
	if _, err := Pull(context.Background(), cloneB, "origin", "demo-project", storeB, filepath.Join(root, "state-b")); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered remote pull error = %v", err)
	}
	if _, err := storeB.GetLatest("procedure-one"); !errors.Is(err, procedurefiles.ErrNotFound) {
		t.Fatalf("tampered procedure became visible locally: %v", err)
	}
}

func TestRemoteIdentityNormalizationNeverRetainsCredentials(t *testing.T) {
	first, err := canonicalRemoteIdentity(t.TempDir(), "https://writer:secret@example.test/org/team.git?token=hidden")
	if err != nil {
		t.Fatal(err)
	}
	second, err := canonicalRemoteIdentity(t.TempDir(), "ssh://git@example.test/org/team.git")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || strings.Contains(first, "secret") || strings.Contains(first, "hidden") || strings.Contains(first, "writer") {
		t.Fatalf("remote identities are not safely normalized: %q != %q", first, second)
	}
	if _, err := canonicalRemoteIdentity(t.TempDir(), "ext::sh -c whoami"); err == nil {
		t.Fatal("unsupported external remote transport was accepted")
	}
}

func newGitTestClone(t *testing.T, root, name, remote string) string {
	t.Helper()
	clone := filepath.Join(root, name)
	if err := os.MkdirAll(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, clone, "init", "--quiet", "--initial-branch=main")
	runGitTest(t, clone, "remote", "add", "origin", remote)
	return clone
}

func runGitTest(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

func newTeamTrustFixture(t *testing.T) (teamauth.Policy, teamauth.Identity) {
	t.Helper()
	identity, public, err := teamauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	policy := teamauth.Policy{
		SchemaVersion: teamauth.PolicySchemaVersion, OrganizationID: "threadforge-tests",
		Members: []teamauth.Member{{
			Subject: "reviewer", KeyID: public.KeyID, PublicKey: public.PublicKey,
			Projects: []string{"demo-project"},
			Roles: []string{teamauth.RoleProcedureApprove, teamauth.RoleProcedurePublish,
				teamauth.RolePrivacyReview, teamauth.RoleProcedureRetire},
		}},
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	return policy, identity
}

func newProcedureStoreForSyncTest(t *testing.T, root string, policy teamauth.Policy) *procedurefiles.Store {
	t.Helper()
	store, err := procedurefiles.NewWithPolicyAndProjectScopeWriteGate(root, &policy, allowTeamScope{})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func publishTeamProcedure(t *testing.T, store *procedurefiles.Store, identity teamauth.Identity, id, title string) {
	t.Helper()
	graph := proceduregraph.Graph{
		SchemaVersion: proceduregraph.SchemaVersion, ID: id, ProjectID: "demo-project", Title: title,
		Revision: 1, Status: proceduregraph.StatusApproved,
		Applicability: proceduregraph.Applicability{Paths: []string{"src/**"}},
		Nodes: []proceduregraph.Node{
			{ID: "inspect", Kind: proceduregraph.NodeTrigger, Instruction: "Inspect the proposed change."},
			{ID: "verify", Kind: proceduregraph.NodeCheck, Instruction: "Run the focused checks."},
		},
		Edges: []proceduregraph.Edge{{From: "inspect", To: "verify", Relation: proceduregraph.RelationLeadsTo}},
		Evidence: []proceduregraph.Evidence{{Origin: "trace", ContentSHA256: strings.Repeat("a", 64),
			LocalTraceRef: "local-trace-only-secret", Redacted: true}},
		Validation: &proceduregraph.Validation{
			DatasetFingerprint: strings.Repeat("b", 64), HeldOut: true, BaselineRuns: 3,
			BaselineScore: .8, CandidateScore: .8, NoiseFloor: .01, CostUnit: "tokens",
			BaselineCost: 100, CandidateCost: 100, MaxCostIncrease: 0, Status: "passed",
		},
		Approval: &proceduregraph.Approval{ApprovedBy: "reviewer", ApprovedAt: "2026-10-10T00:00:00Z",
			Reason: "reviewed held-out evidence", PrivacyReviewed: true},
	}
	authorization, err := store.SignPublication(graph, "publish", identity, time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAuthorized(graph, 0, true, authorization); err != nil {
		t.Fatal(err)
	}
}

func mustExportTeamRecords(t *testing.T, store *procedurefiles.Store) []procedurefiles.SharedRecordFile {
	t.Helper()
	records, err := store.ExportTeamProcedures("demo-project")
	if err != nil {
		t.Fatal(err)
	}
	return records
}
