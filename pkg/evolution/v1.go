package evolution

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const RoundSchemaVersion = "threadforge.evolution-round.v1"

var roundIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type Evaluation struct {
	Score float64 `json:"score"`
	Cost  float64 `json:"cost"`
}

type Edit struct {
	ID                string   `json:"id,omitempty"`
	Component         string   `json:"component"`
	Hypothesis        string   `json:"hypothesis,omitempty"`
	Mechanism         string   `json:"mechanism,omitempty"`
	TargetsMode       string   `json:"targets_mode,omitempty"`
	PredictedAffected []string `json:"predicted_affected,omitempty"`
	WhyNotLowerLever  string   `json:"why_not_lower_lever,omitempty"`
	TriggerCondition  string   `json:"trigger_condition,omitempty"`
	RetroactiveCheck  string   `json:"retroactive_check,omitempty"`
	RegressionRisk    string   `json:"regression_risk,omitempty"`
}

type CandidateDecision struct {
	Variant    string   `json:"variant"`
	Admissible bool     `json:"admissible"`
	Reason     string   `json:"reason"`
	Score      *float64 `json:"S,omitempty"`
	Cost       *float64 `json:"C,omitempty"`
	DeltaScore *float64 `json:"delta_S,omitempty"`
	DeltaCost  *float64 `json:"delta_C,omitempty"`
	Novelty    int      `json:"novelty"`
	Guards     []string `json:"guards,omitempty"`
}

type Candidate struct {
	ID          string            `json:"id"`
	Diff        string            `json:"diff,omitempty"`
	Measured    bool              `json:"measured"`
	Evaluation  *Evaluation       `json:"evaluation,omitempty"`
	GateFailure string            `json:"gate_failure,omitempty"`
	Detail      string            `json:"detail,omitempty"`
	Edits       []Edit            `json:"edits"`
	Decision    CandidateDecision `json:"decision"`
	Outcome     string            `json:"outcome"`
}

// Round is a private, append-only record of an RRSI selection calculation.
// A selected candidate is only a recommendation and never authorizes changes.
type Round struct {
	SchemaVersion       string             `json:"schema_version"`
	ID                  string             `json:"id"`
	ProjectID           string             `json:"project_id"`
	Domain              string             `json:"domain"`
	CreatedAt           string             `json:"created_at"`
	Incumbent           Evaluation         `json:"incumbent"`
	SStar               float64            `json:"s_star"`
	Delta               float64            `json:"delta"`
	SelectionConfig     map[string]float64 `json:"selection_config"`
	IncumbentCounts     map[string]int     `json:"incumbent_counts"`
	Candidates          []Candidate        `json:"candidates"`
	SelectedID          *string            `json:"selected_id"`
	KeepIncumbent       bool               `json:"keep_incumbent"`
	ReviewStatus        string             `json:"review_status"`
	ExecutionAuthorized bool               `json:"execution_authorized"`
}

func ValidateRound(round Round) error {
	if round.SchemaVersion != RoundSchemaVersion || !roundIDPattern.MatchString(round.ID) || strings.TrimSpace(round.ProjectID) == "" || len(round.ProjectID) > 256 {
		return errors.New("evolution round requires supported schema, a stable round ID, and a bounded project ID")
	}
	if round.Domain != "rrsi-coding" && round.Domain != "rrsi-agentic-workspace" && round.Domain != "rrsi-engineering-design" {
		return errors.New("evolution round domain is unsupported")
	}
	if len(round.CreatedAt) > 64 {
		return errors.New("evolution round timestamp is required and bounded")
	}
	if _, err := time.Parse(time.RFC3339Nano, round.CreatedAt); err != nil {
		return errors.New("evolution round timestamp must use RFC3339")
	}
	if round.ReviewStatus != "pending_human_review" || round.ExecutionAuthorized {
		return errors.New("evolution selection must remain pending human review and cannot authorize execution")
	}
	if !validEvaluation(round.Incumbent) || !finite(round.Delta) || round.Delta < 0 || round.Delta > 1 ||
		!finite(round.SStar) || round.SStar < 0 || round.SStar > 1 {
		return errors.New("evolution round incumbent and selection thresholds are invalid")
	}
	allowedConfig := map[string]bool{"beta0": true, "beta1": true, "w_s": true, "w_c": true, "w_n": true}
	if len(round.SelectionConfig) != len(allowedConfig) || round.IncumbentCounts == nil {
		return errors.New("evolution round requires effective selection weights and incumbent component counts")
	}
	for name, value := range round.SelectionConfig {
		if !allowedConfig[name] || !finite(value) || value < 0 {
			return errors.New("evolution round contains invalid effective selection weights")
		}
	}
	for component, count := range round.IncumbentCounts {
		if !validComponent(component) || count < 0 {
			return errors.New("evolution round contains invalid incumbent component counts")
		}
	}
	if len(round.Candidates) == 0 || len(round.Candidates) > 128 {
		return errors.New("evolution round must contain 1-128 candidates")
	}
	encoded, err := json.Marshal(round)
	if err != nil {
		return errors.New("evolution round could not be encoded")
	}
	if len(encoded) > 4<<20 {
		return errors.New("evolution round exceeds the 4 MiB limit")
	}
	if privacy.ContainsCredential(encoded) {
		return errors.New("evolution round contains credential material")
	}
	seen := make(map[string]bool, len(round.Candidates))
	selectedAdmissible := false
	bestScore := -1.0
	bestID := ""
	for _, candidate := range round.Candidates {
		if strings.TrimSpace(candidate.ID) == "" || len(candidate.ID) > 128 || seen[candidate.ID] {
			return errors.New("evolution candidate IDs must be non-empty, bounded, and unique")
		}
		seen[candidate.ID] = true
		if len(candidate.Edits) > 80 || len(candidate.Diff) > 120_000 || len(candidate.Detail) > 1000 || len(candidate.GateFailure) > 1000 {
			return errors.New("evolution candidate edit or detail limits are exceeded")
		}
		if candidate.Measured != (candidate.Evaluation != nil) || candidate.Measured && !validEvaluation(*candidate.Evaluation) {
			return errors.New("evolution candidate measurement must include valid score and positive cost")
		}
		if candidate.Decision.Variant != candidate.ID || strings.TrimSpace(candidate.Decision.Reason) == "" || len(candidate.Decision.Reason) > 2000 || candidate.Decision.Novelty < 0 || len(candidate.Decision.Guards) > 256 {
			return errors.New("evolution candidate decision does not match its candidate")
		}
		for _, guard := range candidate.Decision.Guards {
			if len(guard) > 1000 {
				return errors.New("evolution candidate guard detail exceeds limits")
			}
		}
		if candidate.Measured {
			if candidate.Decision.Score == nil || candidate.Decision.Cost == nil || candidate.Decision.DeltaScore == nil || candidate.Decision.DeltaCost == nil ||
				!finite(*candidate.Decision.Score) || !finite(*candidate.Decision.Cost) || !finite(*candidate.Decision.DeltaScore) || !finite(*candidate.Decision.DeltaCost) {
				return errors.New("measured candidate decision requires finite score, cost, and deltas")
			}
			if !closeEnough(*candidate.Decision.Score, candidate.Evaluation.Score) || !closeEnough(*candidate.Decision.Cost, candidate.Evaluation.Cost) {
				return errors.New("candidate evaluation and selection decision metrics differ")
			}
			wantDeltaScore := candidate.Evaluation.Score - round.Incumbent.Score
			wantDeltaCost := (candidate.Evaluation.Cost - round.Incumbent.Cost) / round.Incumbent.Cost
			if !closeEnough(*candidate.Decision.DeltaScore, wantDeltaScore) || !closeEnough(*candidate.Decision.DeltaCost, wantDeltaCost) {
				return errors.New("candidate score/cost deltas do not reconcile with incumbent")
			}
			if candidate.Decision.Admissible && candidate.Evaluation.Score > bestScore {
				bestScore, bestID = candidate.Evaluation.Score, candidate.ID
			}
		} else if candidate.Decision.Score != nil || candidate.Decision.Cost != nil {
			return errors.New("unmeasured candidate cannot have measured score or cost")
		} else if candidate.Decision.Admissible {
			return errors.New("unmeasured candidate cannot be admissible")
		}
		wantOutcome := "rejected"
		if !candidate.Measured {
			wantOutcome = "unmeasured"
		} else if candidate.Decision.Admissible {
			wantOutcome = "lost"
		}
		if round.SelectedID != nil && candidate.ID == *round.SelectedID {
			if !candidate.Decision.Admissible {
				return errors.New("selected candidate must be admissible")
			}
			selectedAdmissible = true
			wantOutcome = "selected_pending_review"
		}
		if candidate.Outcome != wantOutcome {
			return errors.New("evolution candidate outcome does not match selection decision")
		}
		seenEditIDs := map[string]bool{}
		for _, edit := range candidate.Edits {
			if !validComponent(edit.Component) || len(edit.ID) > 128 || len(edit.Hypothesis) > 4000 || len(edit.Mechanism) > 4000 ||
				len(edit.TargetsMode) > 2000 || len(edit.WhyNotLowerLever) > 2000 || len(edit.TriggerCondition) > 2000 ||
				len(edit.RetroactiveCheck) > 4000 || len(edit.RegressionRisk) > 2000 || len(edit.PredictedAffected) > 256 {
				return errors.New("evolution edit metadata is invalid or exceeds limits")
			}
			if edit.ID != "" {
				if seenEditIDs[edit.ID] {
					return errors.New("evolution edit IDs must be unique within a candidate")
				}
				seenEditIDs[edit.ID] = true
			}
			for _, taskID := range edit.PredictedAffected {
				if len(taskID) > 128 {
					return errors.New("predicted affected task ID exceeds limits")
				}
			}
		}
	}
	if round.SelectedID == nil {
		if !round.KeepIncumbent || bestID != "" {
			return errors.New("selection without a candidate must retain incumbent")
		}
	} else if round.KeepIncumbent || !seen[*round.SelectedID] || !selectedAdmissible || bestID != *round.SelectedID {
		return errors.New("selected candidate and incumbent-retention fields conflict")
	}
	return nil
}

func validEvaluation(value Evaluation) bool {
	return finite(value.Score) && value.Score >= 0 && value.Score <= 1 && finite(value.Cost) && value.Cost > 0
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func closeEnough(left, right float64) bool { return math.Abs(left-right) <= 1e-8 }

func validComponent(value string) bool {
	switch value {
	case "prompt", "control_flow", "config", "output_plumbing", "context_mgmt", "client_tool", "skill", "memory", "subagent":
		return true
	default:
		return false
	}
}
