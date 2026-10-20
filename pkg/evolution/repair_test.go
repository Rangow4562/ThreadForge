package evolution

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func acceptedRepairFixture() RepairResult {
	candidate := []byte(`{"summary":"Bound context","targets_mode":"large","edits":[{"id":"C1","component":"control_flow","hypothesis":"Use a cap."}],"diff":"+def f(): return 1","state_files":""}`)
	canonical, err := canonicalJSON(candidate)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(canonical)
	digestText := hex.EncodeToString(digest[:])
	accept := "accept"
	return RepairResult{
		SchemaVersion: RepairResultSchemaVersion, Domain: "rrsi-coding", Status: "reviewed",
		ReasonCode: "local_critic_accept", ActionReady: false, Candidate: candidate,
		CandidateSHA256: &digestText,
		AttemptHistory: []RepairAttempt{{
			Attempt: 1, CandidateSHA256: digestText, Summary: "Bound context", TargetsMode: "large",
			EditMetadata: []Edit{{ID: "C1", Component: "control_flow", Hypothesis: "Use a cap."}},
			Status:       "reviewed", Verdict: &accept, ReasonCode: "local_critic_accept",
			Findings: []string{}, MatchedDomainRuleIDs: []string{}, RepairGenerated: false,
			AnalystReview: RepairAnalystReview{
				Status: "reviewed", ReasonCode: "local_analyst_review", RiskLevel: "low",
				RiskCategories: []string{}, ValidationGaps: []string{"held_out_evaluation"},
			},
		}},
		AttemptCount: 1, RepairCount: 0,
		FinalCritic:           &RepairCritic{Status: "reviewed", Verdict: &accept, ReasonCode: "local_critic_accept", Findings: []string{}, MatchedDomainRuleIDs: []string{}},
		HumanApprovalRequired: true, EvaluationRequired: true, ExecutionAuthorized: false,
	}
}

func TestValidateRepairResultChecksCrossLanguageDigestAndAdvisoryState(t *testing.T) {
	result := acceptedRepairFixture()
	canonical, err := canonicalJSON(result.Candidate)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"diff":"+def f(): return 1","edits":[{"component":"control_flow","hypothesis":"Use a cap.","id":"C1"}],"state_files":"","summary":"Bound context","targets_mode":"large"}`
	if string(canonical) != want {
		t.Fatalf("canonical candidate = %s, want %s", canonical, want)
	}
	if err := ValidateRepairResult(result, "rrsi-coding"); err != nil {
		t.Fatalf("valid local critic acceptance rejected: %v", err)
	}

	wrongDigest := result
	bad := strings.Repeat("0", 64)
	wrongDigest.CandidateSHA256 = &bad
	if err := ValidateRepairResult(wrongDigest, "rrsi-coding"); err == nil {
		t.Fatal("candidate digest mismatch accepted")
	}
	unauthorized := result
	unauthorized.ExecutionAuthorized = true
	if err := ValidateRepairResult(unauthorized, "rrsi-coding"); err == nil {
		t.Fatal("repair result that authorizes execution accepted")
	}
	duplicateCategory := result
	duplicateCategory.AttemptHistory = append([]RepairAttempt(nil), result.AttemptHistory...)
	duplicateCategory.AttemptHistory[0].AnalystReview.RiskCategories = []string{"regression_surface", "regression_surface"}
	if err := ValidateRepairResult(duplicateCategory, "rrsi-coding"); err == nil {
		t.Fatal("analyst output with duplicate categories accepted")
	}
}

func TestValidateRepairResultV2RequiresAccuratePinnedInventoryCoverage(t *testing.T) {
	result := acceptedRepairFixture()
	complete := false
	result.SchemaVersion = RepairResultSchemaVersionV2
	result.DomainRulesComplete = &complete
	result.DomainRuleSetVersion = "rrsi-domain-rules.v3"
	result.VerifiedInventorySources = []string{"terminal-bench/terminal-bench-2-1"}
	result.MissingInventorySources = []string{"swe-bench/swe-bench-verified"}
	if err := ValidateRepairResult(result, "rrsi-coding"); err != nil {
		t.Fatalf("valid incomplete source coverage rejected: %v", err)
	}
	legacyRuleSet := result
	legacyRuleSet.DomainRuleSetVersion = "rrsi-domain-rules.v2"
	if err := ValidateRepairResult(legacyRuleSet, "rrsi-coding"); err != nil {
		t.Fatalf("previous inventory-aware rule-set version was not readable: %v", err)
	}
	unknownRuleSet := result
	unknownRuleSet.DomainRuleSetVersion = "rrsi-domain-rules.v4"
	if err := ValidateRepairResult(unknownRuleSet, "rrsi-coding"); err == nil {
		t.Fatal("unknown inventory-aware rule-set version was accepted")
	}

	forged := result
	forged.VerifiedInventorySources = []string{"swe-bench/swe-bench-verified"}
	if err := ValidateRepairResult(forged, "rrsi-coding"); err == nil {
		t.Fatal("untrusted source claimed as verified")
	}

	forged = result
	complete = true
	forged.DomainRulesComplete = &complete
	if err := ValidateRepairResult(forged, "rrsi-coding"); err == nil {
		t.Fatal("incomplete inventory claimed complete")
	}

	if err := ValidateInventoryCoverage("rrsi-agentic-workspace", false, []string{}, repairRequiredInventorySources["rrsi-agentic-workspace"]); err != nil {
		t.Fatalf("empty verified coverage with all workspace sources missing rejected: %v", err)
	}
	if err := ValidateInventoryCoverage("rrsi-engineering-design", false,
		[]string{"engdesign-open", "engdesign-v1"}, []string{"frontier-eng"}); err != nil {
		t.Fatalf("source-backed EngDesign v1 coverage rejected: %v", err)
	}
	if err := ValidateInventoryCoverage("rrsi-engineering-design", false,
		[]string{"engdesign-v1", "frontier-eng"}, []string{"engdesign-open"}); err == nil {
		t.Fatal("untrusted Frontier-Eng source claimed as verified")
	}
}
