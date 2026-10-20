package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
)

func TestProposalValidationApprovalAndAudit(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "local", "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	g := proceduregraph.Graph{
		SchemaVersion: proceduregraph.SchemaVersion,
		ID:            "stable-build-check", ProjectID: "repo:fixture", Title: "Stable build check",
		Revision: 1, Status: proceduregraph.StatusDraft,
		Nodes:    []proceduregraph.Node{{ID: "check", Kind: proceduregraph.NodeAction, Instruction: "Run the relevant test."}},
		Evidence: []proceduregraph.Evidence{{Origin: "trace", ContentSHA256: strings.Repeat("a", 64), Redacted: true, LocalTraceRef: "local-trace"}},
	}
	if err := s.CreateProposal(ctx, g, "extractor", "explicit learning run", time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	report := proceduregraph.Validation{
		DatasetFingerprint: strings.Repeat("b", 64), HeldOut: true, BaselineRuns: 3,
		BaselineScore: 0.8, CandidateScore: 0.82, NoiseFloor: 0.02,
		CostUnit: "tokens", BaselineCost: 1000, CandidateCost: 1050, MaxCostIncrease: 100, Status: "passed",
	}
	validated, err := s.RecordValidation(ctx, g.ID, 1, report, time.Date(2026, 9, 24, 10, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if validated.Status != proceduregraph.StatusValidated || validated.Revision != 2 {
		t.Fatalf("validated graph = %#v", validated)
	}
	approved, err := s.Approve(ctx, g.ID, 2, "reviewer", "checked held-out result", true, time.Date(2026, 9, 24, 10, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != proceduregraph.StatusApproved || approved.Revision != 3 {
		t.Fatalf("approved graph = %#v", approved)
	}
	got, err := s.Get(ctx, g.ID)
	if err != nil || got.Status != proceduregraph.StatusApproved {
		t.Fatalf("Get() graph=%#v err=%v", got, err)
	}
	events, err := s.Audit(ctx, g.ID)
	if err != nil || len(events) != 3 {
		t.Fatalf("Audit() events=%#v err=%v", events, err)
	}
}

func TestRevisionConflictDoesNotOverwrite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := proceduregraph.Graph{
		SchemaVersion: proceduregraph.SchemaVersion, ID: "conflict-test", ProjectID: "repo:test",
		Title: "Conflict", Revision: 1, Status: proceduregraph.StatusDraft,
		Nodes: []proceduregraph.Node{{ID: "start", Kind: proceduregraph.NodeTrigger, Instruction: "Start."}},
	}
	if err := s.CreateProposal(ctx, g, "agent", "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	report := proceduregraph.Validation{DatasetFingerprint: strings.Repeat("c", 64), HeldOut: true, BaselineRuns: 1, BaselineScore: 0.5, CandidateScore: 0.5, NoiseFloor: 0, CostUnit: "tokens", BaselineCost: 1, CandidateCost: 1, MaxCostIncrease: 0, Status: "passed"}
	if _, err := s.RecordValidation(ctx, g.ID, 1, report, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, g.ID, 1, "reviewer", "stale write", true, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("Approve() error=%v, want ErrConflict", err)
	}
}

func TestFailedValidationIsRejected(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := proceduregraph.Graph{
		SchemaVersion: proceduregraph.SchemaVersion, ID: "regression", ProjectID: "repo:test",
		Title: "Regression", Revision: 1, Status: proceduregraph.StatusDraft,
		Nodes: []proceduregraph.Node{{ID: "start", Kind: proceduregraph.NodeTrigger, Instruction: "Start."}},
	}
	if err := s.CreateProposal(ctx, g, "agent", "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	report := proceduregraph.Validation{DatasetFingerprint: strings.Repeat("d", 64), HeldOut: true, BaselineRuns: 2, BaselineScore: 0.8, CandidateScore: 0.7, NoiseFloor: 0.02, CostUnit: "tokens", BaselineCost: 100, CandidateCost: 90, MaxCostIncrease: 0, Status: "failed"}
	updated, err := s.RecordValidation(ctx, g.ID, 1, report, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != proceduregraph.StatusRejected {
		t.Fatalf("status=%q, want rejected", updated.Status)
	}
}

func TestSupersedeRequiresApprovedSameProjectReplacementAndRecordsAudit(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := createApprovedGraphForTest(t, s, "old-procedure", "repo:supersede")
	replacement := createApprovedGraphForTest(t, s, "new-procedure", old.ProjectID)
	now := time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC)
	superseded, err := s.Supersede(ctx, old.ID, old.Revision, replacement.ID, "reviewer", "replacement fixes the failure mode", now)
	if err != nil {
		t.Fatal(err)
	}
	if superseded.Status != proceduregraph.StatusSuperseded || superseded.SupersededBy != replacement.ID || superseded.Revision != old.Revision+1 {
		t.Fatalf("superseded graph = %#v", superseded)
	}
	if _, err := s.Supersede(ctx, old.ID, old.Revision, replacement.ID, "reviewer", "stale supersession", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale supersession error = %v, want revision conflict", err)
	}
	events, err := s.Audit(ctx, old.ID)
	if err != nil || len(events) != 4 || events[3].Action != "superseded" || events[3].Reason != "replacement fixes the failure mode" {
		t.Fatalf("supersession audit = %#v, error = %v", events, err)
	}
	if _, err := s.Supersede(ctx, replacement.ID, replacement.Revision, replacement.ID, "reviewer", "self supersede", now); err == nil {
		t.Fatal("procedure was allowed to supersede itself")
	}
}

func createApprovedGraphForTest(t *testing.T, s *Store, id, projectID string) proceduregraph.Graph {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	graph := proceduregraph.Graph{
		SchemaVersion: proceduregraph.SchemaVersion, ID: id, ProjectID: projectID,
		Title: "Approved test procedure", Revision: 1, Status: proceduregraph.StatusDraft,
		Nodes: []proceduregraph.Node{{ID: "inspect", Kind: proceduregraph.NodeTrigger, Instruction: "Inspect the relevant source."}},
	}
	if err := s.CreateProposal(ctx, graph, "author", "test supersession", now); err != nil {
		t.Fatal(err)
	}
	report := proceduregraph.Validation{
		DatasetFingerprint: strings.Repeat("e", 64), HeldOut: true, BaselineRuns: 2,
		BaselineScore: 0.8, CandidateScore: 0.8, NoiseFloor: 0,
		CostUnit: "tokens", BaselineCost: 100, CandidateCost: 100, MaxCostIncrease: 0, Status: "passed",
	}
	validated, err := s.RecordValidation(ctx, graph.ID, graph.Revision, report, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.Approve(ctx, graph.ID, validated.Revision, "reviewer", "held-out result checked", true, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return approved
}
