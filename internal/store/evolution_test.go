package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/evolution"
)

func evolutionRoundFixture() evolution.Round {
	score, cost := 0.8, 105.0
	guardScore, guardCost := 0.85, 104.0
	deltaScore, deltaCost := 0.1, 0.05
	guardDeltaScore, guardDeltaCost := 0.15, 0.04
	selected := "candidate-gain"
	return evolution.Round{
		SchemaVersion: evolution.RoundSchemaVersion,
		ID:            "evolution-round-1", ProjectID: "project-demo", Domain: "rrsi-agentic-workspace",
		CreatedAt: "2026-09-24T10:00:00Z", Incumbent: evolution.Evaluation{Score: 0.7, Cost: 100},
		SStar: 0.7, Delta: 0.02,
		SelectionConfig: map[string]float64{"beta0": 0.10, "beta1": 40, "w_s": 100, "w_c": 15, "w_n": 0.5},
		IncumbentCounts: map[string]int{"skill": 1},
		SelectedID:      &selected, ReviewStatus: "pending_human_review",
		Candidates: []evolution.Candidate{
			{
				ID: "candidate-gain", Diff: "+def retrieve_procedure(): return bounded\n", Measured: true, Evaluation: &evolution.Evaluation{Score: score, Cost: cost},
				Edits: []evolution.Edit{{ID: "C1", Component: "skill", Hypothesis: "Use bounded retrieval for relevant procedures."}},
				Decision: evolution.CandidateDecision{Variant: "candidate-gain", Admissible: true, Reason: "admissible after cost and noise gates",
					Score: &score, Cost: &cost, DeltaScore: &deltaScore, DeltaCost: &deltaCost},
				Outcome: "selected_pending_review",
			},
			{
				ID: "candidate-guarded", Measured: true, Evaluation: &evolution.Evaluation{Score: guardScore, Cost: guardCost},
				Decision: evolution.CandidateDecision{Variant: "candidate-guarded", Reason: "domain guard violated", Score: &guardScore, Cost: &guardCost,
					DeltaScore: &guardDeltaScore, DeltaCost: &guardDeltaCost, Guards: []string{"privacy guard"}},
				Outcome: "rejected",
			},
			{
				ID: "candidate-unmeasured", Measured: false, GateFailure: "held-out evaluation unavailable",
				Decision: evolution.CandidateDecision{Variant: "candidate-unmeasured", Reason: "held-out evaluation unavailable"},
				Outcome:  "unmeasured",
			},
		},
	}
}

func TestEvolutionRoundHistoryIsValidatedPrivateAndAppendOnly(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	round := evolutionRoundFixture()
	if err := db.RecordEvolutionRound(ctx, round, "local-author", "record RRSI selection evidence"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordEvolutionRound(ctx, round, "local-author", "record RRSI selection evidence"); err != nil {
		t.Fatalf("identical record retry should be idempotent: %v", err)
	}
	conflict := round
	conflict.Delta = 0.03
	if err := db.RecordEvolutionRound(ctx, conflict, "local-author", "record conflicting result"); !errors.Is(err, ErrEvolutionRoundConflict) {
		t.Fatalf("conflicting round re-use error = %v", err)
	}
	if err := db.RecordEvolutionRound(ctx, round, "local-author", "API_TOKEN=credential-value"); err == nil {
		t.Fatal("credential-bearing audit metadata accepted")
	}

	got, err := db.GetEvolutionRound(ctx, round.ID)
	if err != nil || got.SelectedID == nil || *got.SelectedID != "candidate-gain" || len(got.Candidates) != 3 || got.Candidates[0].Edits[0].Hypothesis == "" {
		t.Fatalf("stored round = %#v err=%v", got, err)
	}
	page, total, err := db.ListEvolutionRounds(ctx, round.ProjectID, 10, 0)
	if err != nil || total != 1 || len(page) != 1 || page[0].ID != round.ID {
		t.Fatalf("round list = %#v total=%d err=%v", page, total, err)
	}
	audit, err := db.EvolutionRoundHistory(ctx, round.ID)
	if err != nil || len(audit) != 1 || audit[0].Action != "selection_recorded" {
		t.Fatalf("round audit = %#v err=%v", audit, err)
	}

	if _, err := db.db.ExecContext(ctx, `UPDATE evolution_rounds SET reason = 'tampered' WHERE id = ?`, round.ID); err == nil {
		t.Fatal("round snapshot update was not blocked")
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM evolution_edit_history WHERE round_id = ?`, round.ID); err == nil {
		t.Fatal("edit history deletion was not blocked")
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM evolution_round_audit WHERE round_id = ?`, round.ID); err == nil {
		t.Fatal("audit deletion was not blocked")
	}
	if _, err := db.db.ExecContext(ctx, `DROP TRIGGER evolution_edits_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE evolution_edit_history SET edit_json = '{}' WHERE round_id = ?`, round.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetEvolutionRound(ctx, round.ID); err == nil {
		t.Fatal("tampered edit history was not detected")
	}
}

func TestEvolutionRoundRejectsInconsistentSelectionAndCredentialEdits(t *testing.T) {
	round := evolutionRoundFixture()
	round.Candidates[0].Outcome = "lost"
	if err := evolution.ValidateRound(round); err == nil {
		t.Fatal("selected candidate with inconsistent outcome was accepted")
	}
	round = evolutionRoundFixture()
	round.Candidates[0].Edits[0].Hypothesis = "API_TOKEN=credential-value"
	if err := evolution.ValidateRound(round); err == nil || !strings.Contains(strings.ToLower(err.Error()), "credential") {
		t.Fatalf("credential-bearing edit validation error = %v", err)
	}
}
