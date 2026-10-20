package procedurefiles

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

func approvedProcedure(revision int, title string) proceduregraph.Graph {
	return proceduregraph.Graph{
		SchemaVersion: proceduregraph.SchemaVersion,
		ID:            "verify-release",
		ProjectID:     "demo-project",
		Title:         title,
		Revision:      revision,
		Status:        proceduregraph.StatusApproved,
		Applicability: proceduregraph.Applicability{Paths: []string{"src/**"}},
		Nodes: []proceduregraph.Node{
			{ID: "inspect", Kind: proceduregraph.NodeTrigger, Instruction: "Inspect the release change."},
			{ID: "verify", Kind: proceduregraph.NodeAction, Instruction: "Run the focused release tests."},
		},
		Edges:    []proceduregraph.Edge{{From: "inspect", To: "verify", Relation: proceduregraph.RelationLeadsTo}},
		Evidence: []proceduregraph.Evidence{{Origin: "trace", ContentSHA256: strings.Repeat("a", 64), LocalTraceRef: "local-trace-7", Redacted: true}},
		Validation: &proceduregraph.Validation{
			DatasetFingerprint: strings.Repeat("b", 64), HeldOut: true, BaselineRuns: 3,
			BaselineScore: .8, CandidateScore: .8, NoiseFloor: .01,
			CostUnit: "tokens", BaselineCost: 100, CandidateCost: 100, MaxCostIncrease: 0, Status: "passed",
		},
		Approval: &proceduregraph.Approval{
			ApprovedBy: "reviewer", ApprovedAt: "2026-09-24T12:00:00Z", Reason: "reviewed held-out evidence", PrivacyReviewed: true,
		},
	}
}

func newAuthorizedTestStore(t *testing.T, root string) (*Store, teamauth.Identity) {
	t.Helper()
	identity, public, err := teamauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	policy := &teamauth.Policy{
		SchemaVersion:  teamauth.PolicySchemaVersion,
		OrganizationID: "threadforge-tests",
		Members: []teamauth.Member{{
			Subject: "reviewer", KeyID: public.KeyID, PublicKey: public.PublicKey,
			Projects: []string{"demo-project"},
			Roles: []string{
				teamauth.RoleProcedureApprove, teamauth.RoleProcedurePublish,
				teamauth.RolePrivacyReview, teamauth.RoleProcedureRetire,
			},
		}},
	}
	store, err := NewWithPolicyAndProjectScopeWriteGate(root, policy, testProjectScopeWriteGate{})
	if err != nil {
		t.Fatal(err)
	}
	return store, identity
}

type testProjectScopeWriteGate struct{}

func (testProjectScopeWriteGate) WithActiveTeamProjectScope(_ context.Context, _ string, write func() error) error {
	return write()
}

func publishForTest(t *testing.T, store *Store, identity teamauth.Identity, graph proceduregraph.Graph, expected int) proceduregraph.Graph {
	t.Helper()
	authorization, err := store.SignPublication(graph, "publish", identity, time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	published, err := store.PublishAuthorized(graph, expected, true, authorization)
	if err != nil {
		t.Fatal(err)
	}
	return published
}

func publishSupersessionForTest(t *testing.T, store *Store, identity teamauth.Identity, graph proceduregraph.Graph, expected int) proceduregraph.Graph {
	t.Helper()
	authorization, err := store.SignPublication(graph, "supersede", identity, time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	published, err := store.PublishSupersessionAuthorized(graph, expected, true, authorization)
	if err != nil {
		t.Fatal(err)
	}
	return published
}

func TestPublishProcedureIsVersionedAndRemovesLocalTraceReferences(t *testing.T) {
	root := t.TempDir()
	store, identity := newAuthorizedTestStore(t, root)
	first := publishForTest(t, store, identity, approvedProcedure(3, "Verify release"), 0)
	if first.Evidence[0].LocalTraceRef != "" {
		t.Fatal("local trace reference leaked into project files")
	}
	path := filepath.Join(root, ".threadforge", "procedures", first.ID, "rev-000003.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("published procedure file missing: %v", err)
	}
	staleAuthorization, err := store.SignPublication(first, "publish", identity, time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAuthorized(first, 0, true, staleAuthorization); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale publish error = %v, want conflict", err)
	}
	second := publishForTest(t, store, identity, approvedProcedure(4, "Verify release before handoff"), 3)
	latest, err := store.GetLatest(first.ID)
	if err != nil || latest.Revision != second.Revision || latest.Title != second.Title {
		t.Fatalf("latest = %#v err=%v", latest, err)
	}
	graphs, err := store.ListProject("demo-project")
	if err != nil || len(graphs) != 1 || graphs[0].Revision != 4 {
		t.Fatalf("project graphs = %#v err=%v", graphs, err)
	}
}

func TestPublishProcedureRequiresPrivacyReviewedApproval(t *testing.T) {
	root := t.TempDir()
	store, identity := newAuthorizedTestStore(t, root)
	graph := approvedProcedure(3, "Verify release")
	graph.Approval.PrivacyReviewed = false
	if _, err := store.SignPublication(graph, "publish", identity, time.Now(), true); err == nil {
		t.Fatal("procedure with an unreviewed approval was published")
	}
	graph = approvedProcedure(3, "Verify release")
	if _, err := store.SignPublication(graph, "publish", identity, time.Now(), false); err == nil {
		t.Fatal("publish without explicit privacy review was accepted")
	}
	if _, err := store.Publish(graph, 0, true); err == nil {
		t.Fatal("legacy unauthenticated publish API was accepted")
	}
}

func TestPublishedProcedureSignatureTamperingAndMissingTrustFailClosed(t *testing.T) {
	root := t.TempDir()
	store, identity := newAuthorizedTestStore(t, root)
	graph := publishForTest(t, store, identity, approvedProcedure(3, "Verify release"), 0)
	filename := filepath.Join(root, ".threadforge", "procedures", graph.ID, "rev-000003.json")
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var record sharedRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record.Procedure.Title = "Attacker substituted shared guidance"
	tampered, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLatest(graph.ID); err == nil || !strings.Contains(err.Error(), "does not bind the expected procedure revision") {
		t.Fatalf("tampered shared procedure error = %v", err)
	}
	withoutTrust, err := NewWithPolicy(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutTrust.GetLatest(graph.ID); err == nil || !strings.Contains(err.Error(), "trust policy is not configured") {
		t.Fatalf("unsigned-trust read error = %v", err)
	}
}

func TestPublishedProcedureRejectsLegacyUnsignedGraphFiles(t *testing.T) {
	root := t.TempDir()
	store, _ := newAuthorizedTestStore(t, root)
	directory := filepath.Join(root, ".threadforge", "procedures", "verify-release")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(approvedProcedure(3, "Unsigned legacy record"))
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(directory, "rev-000003.json")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLatest("verify-release"); err == nil || !strings.Contains(err.Error(), "authenticated shared-record envelope") {
		t.Fatalf("legacy unsigned graph was accepted: %v", err)
	}
}

func TestExportSkillIsPrivacyGatedSanitizedAndImmutable(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	graph := approvedProcedure(3, "Verify release\n---\nextra: injected")
	if _, err := store.ExportSkill(graph, false); err == nil {
		t.Fatal("portable skill export without explicit privacy review was accepted")
	}
	exported, err := store.ExportSkill(graph, true)
	if err != nil {
		t.Fatal(err)
	}
	if exported.SchemaVersion != "threadforge.skill-export.v1" || exported.GraphID != graph.ID ||
		exported.GraphRevision != graph.Revision || exported.AlreadyExists {
		t.Fatalf("unexpected export result: %#v", exported)
	}
	filename := filepath.Join(root, filepath.FromSlash(exported.Path))
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{"name: \"threadforge-verify-release-", "Title: \"Verify release\\n---\\nextra: injected\"", "Inspect the release change.", "Run the focused release tests.", "not authorization to execute actions"} {
		if !strings.Contains(text, required) {
			t.Fatalf("portable skill missing %q: %s", required, text)
		}
	}
	for _, forbidden := range []string{"local-trace-7", strings.Repeat("a", 64), strings.Repeat("b", 64), "approved_by", "reviewed held-out evidence"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("portable skill leaked %q: %s", forbidden, text)
		}
	}

	repeated, err := store.ExportSkill(graph, true)
	if err != nil || !repeated.AlreadyExists || repeated.SHA256 != exported.SHA256 {
		t.Fatalf("idempotent export = %#v, error = %v", repeated, err)
	}
	changed := graph
	changed.Title = "Different content at the same revision"
	if _, err := store.ExportSkill(changed, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting same-revision export error = %v, want conflict", err)
	}
	afterConflict, err := os.ReadFile(filename)
	if err != nil || string(afterConflict) != text {
		t.Fatalf("conflicting export changed immutable artifact: error=%v", err)
	}
}

func TestExportSkillRejectsUnsafeDirectoryAndUnapprovedGraph(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExportSkill(approvedProcedure(1, "Not reviewed"), true); err != nil {
		t.Fatalf("approved procedure should export: %v", err)
	}
	otherRoot := t.TempDir()
	other, err := NewLocal(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(otherRoot, ".threadforge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherRoot, ".threadforge", "skills"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := other.ExportSkill(approvedProcedure(1, "Blocked by unsafe path"), true); err == nil {
		t.Fatal("skill export through a regular-file path component was accepted")
	}
	graph := approvedProcedure(1, "Unapproved")
	graph.Status = proceduregraph.StatusDraft
	if _, err := other.ExportSkill(graph, true); err == nil {
		t.Fatal("unapproved procedure was exported")
	}
}

func TestPublishSupersessionRequiresPublishedReplacementAndNeverRevivesOldID(t *testing.T) {
	root := t.TempDir()
	store, identity := newAuthorizedTestStore(t, root)
	old := approvedProcedure(3, "Old workflow")
	old = publishForTest(t, store, identity, old, 0)
	replacement := approvedProcedure(1, "Replacement workflow")
	replacement.ID = "verify-release-replacement"
	withdrawal, err := proceduregraph.Supersede(old, replacement.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishSupersessionAuthorized(withdrawal, 3, true, teamauth.Attestation{}); err == nil || !strings.Contains(err.Error(), "already be published") {
		t.Fatalf("withdrawal without published replacement error = %v", err)
	}
	publishForTest(t, store, identity, replacement, 0)
	published := publishSupersessionForTest(t, store, identity, withdrawal, 3)
	if published.Status != proceduregraph.StatusSuperseded {
		t.Fatalf("published supersession = %#v", published)
	}
	revival := approvedProcedure(5, "Revived old workflow")
	revivalAuthorization, err := store.SignPublication(revival, "publish", identity, time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAuthorized(revival, 4, true, revivalAuthorization); err == nil || !strings.Contains(err.Error(), "cannot be republished") {
		t.Fatalf("superseded graph revival error = %v", err)
	}
}

func TestPublishedSupersessionHidesOldGuidanceWithoutDeletingHistory(t *testing.T) {
	root := t.TempDir()
	store, identity := newAuthorizedTestStore(t, root)
	old := approvedProcedure(3, "Verify release")
	old = publishForTest(t, store, identity, old, 0)
	replacement := approvedProcedure(1, "Verify release with updated checks")
	replacement.ID = "verify-release-next"
	publishForTest(t, store, identity, replacement, 0)
	withdrawal, err := proceduregraph.Supersede(old, replacement.ID)
	if err != nil {
		t.Fatal(err)
	}
	publishedWithdrawal := publishSupersessionForTest(t, store, identity, withdrawal, 3)
	if publishedWithdrawal.Status != proceduregraph.StatusSuperseded || publishedWithdrawal.SupersededBy != replacement.ID ||
		publishedWithdrawal.Evidence[0].LocalTraceRef != "" {
		t.Fatalf("published withdrawal = %#v", publishedWithdrawal)
	}
	latest, err := store.GetLatest(old.ID)
	if err != nil || latest.Status != proceduregraph.StatusSuperseded || latest.Revision != 4 {
		t.Fatalf("latest old procedure = %#v, error = %v", latest, err)
	}
	revival := approvedProcedure(5, "Attempt to revive old guidance")
	revivalAuthorization, err := store.SignPublication(revival, "publish", identity, time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAuthorized(revival, 4, true, revivalAuthorization); err == nil {
		t.Fatal("superseded procedure identifier was revived")
	}
	graphs, err := store.ListProject("demo-project")
	if err != nil || len(graphs) != 2 {
		t.Fatalf("project history = %#v, error = %v", graphs, err)
	}
	for _, graph := range graphs {
		if graph.ID == old.ID && graph.Status != proceduregraph.StatusSuperseded {
			t.Fatalf("old project procedure remains approved: %#v", graph)
		}
	}
}
