package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/evolution"
)

func evolutionRepairRunFixture() evolution.RepairRun {
	reject := "reject"
	return evolution.RepairRun{
		ID: "repair-run-01", ProjectID: "project-demo", CreatedAt: "2026-09-24T10:00:00Z",
		Actor: "local-author", Reason: "retain bounded local repair history",
		Result: evolution.RepairResult{
			SchemaVersion: evolution.RepairResultSchemaVersion, Domain: "rrsi-coding", Status: "blocked",
			ReasonCode: "local_critic_reject", ActionReady: false, Candidate: []byte("null"), CandidateSHA256: nil,
			AttemptHistory: []evolution.RepairAttempt{{
				Attempt: 1, CandidateSHA256: strings.Repeat("a", 64), Summary: "Bound context",
				TargetsMode: "large context", EditMetadata: []evolution.Edit{{ID: "C1", Component: "control_flow", Hypothesis: "Bound retained context."}},
				Status: "blocked", Verdict: &reject, ReasonCode: "local_critic_reject",
				Findings: []string{}, MatchedDomainRuleIDs: []string{}, RepairGenerated: false,
				AnalystReview: evolution.RepairAnalystReview{
					Status: "reviewed", ReasonCode: "local_analyst_review", RiskLevel: "medium",
					RiskCategories: []string{"regression_surface"}, ValidationGaps: []string{"held_out_evaluation"},
				},
			}},
			AttemptCount: 1, RepairCount: 0,
			FinalCritic:           &evolution.RepairCritic{Status: "blocked", Verdict: &reject, ReasonCode: "local_critic_reject", Findings: []string{}, MatchedDomainRuleIDs: []string{}},
			HumanApprovalRequired: true, EvaluationRequired: true, ExecutionAuthorized: false,
		},
	}
}

func TestEvolutionRepairHistoryIsValidatedPrivateAndAppendOnly(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	run := evolutionRepairRunFixture()
	if err := database.RecordEvolutionRepairRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := database.RecordEvolutionRepairRun(ctx, run); err != nil {
		t.Fatalf("identical repair record retry should be idempotent: %v", err)
	}
	conflict := run
	conflict.Reason = "different valid audit rationale"
	if err := database.RecordEvolutionRepairRun(ctx, conflict); !errors.Is(err, ErrEvolutionRepairConflict) {
		t.Fatalf("conflicting repair ID error = %v", err)
	}
	private := run
	private.Reason = "API_TOKEN=credential-value"
	if err := database.RecordEvolutionRepairRun(ctx, private); err == nil {
		t.Fatal("credential-bearing repair audit metadata accepted")
	}

	got, err := database.GetEvolutionRepairRun(ctx, run.ID)
	if err != nil || got.Result.AttemptCount != 1 || got.Result.AttemptHistory[0].EditMetadata[0].Hypothesis == "" {
		t.Fatalf("stored repair run = %#v err=%v", got, err)
	}
	page, total, err := database.ListEvolutionRepairRuns(ctx, run.ProjectID, 10, 0)
	if err != nil || total != 1 || len(page) != 1 || page[0].ID != run.ID {
		t.Fatalf("repair list = %#v total=%d err=%v", page, total, err)
	}
	audit, err := database.EvolutionRepairHistory(ctx, run.ID)
	if err != nil || len(audit) != 1 || audit[0].Action != "repair_recorded" {
		t.Fatalf("repair audit = %#v err=%v", audit, err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE evolution_repair_runs SET reason = 'tampered' WHERE id = ?`, run.ID); err == nil {
		t.Fatal("repair run snapshot update was not blocked")
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM evolution_repair_edits WHERE run_id = ?`, run.ID); err == nil {
		t.Fatal("repair edit history deletion was not blocked")
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM evolution_repair_audit WHERE run_id = ?`, run.ID); err == nil {
		t.Fatal("repair audit deletion was not blocked")
	}
}

func TestEvolutionRepairHistoryPersistsSourceCoverageV2(t *testing.T) {
	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	run := evolutionRepairRunFixture()
	run.ID = "repair-run-v2"
	complete := false
	run.Result.SchemaVersion = evolution.RepairResultSchemaVersionV2
	run.Result.DomainRulesComplete = &complete
	run.Result.DomainRuleSetVersion = "rrsi-domain-rules.v3"
	run.Result.VerifiedInventorySources = []string{"terminal-bench/terminal-bench-2-1"}
	run.Result.MissingInventorySources = []string{"swe-bench/swe-bench-verified"}
	if err := database.RecordEvolutionRepairRun(ctx, run); err != nil {
		t.Fatalf("record v2 repair evidence: %v", err)
	}
	got, err := database.GetEvolutionRepairRun(ctx, run.ID)
	if err != nil || got.Result.DomainRulesComplete == nil || *got.Result.DomainRulesComplete ||
		len(got.Result.VerifiedInventorySources) != 1 || got.Result.VerifiedInventorySources[0] != "terminal-bench/terminal-bench-2-1" ||
		len(got.Result.MissingInventorySources) != 1 || got.Result.MissingInventorySources[0] != "swe-bench/swe-bench-verified" {
		t.Fatalf("stored v2 inventory evidence = %#v err=%v", got.Result, err)
	}
}
