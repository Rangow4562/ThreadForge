package evolution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const RepairResultSchemaVersion = "threadforge.evolution-repair.v1"
const RepairResultSchemaVersionV2 = "threadforge.evolution-repair.v2"

var repairDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var repairRequiredInventorySources = map[string][]string{
	"rrsi-coding": {"terminal-bench/terminal-bench-2-1", "swe-bench/swe-bench-verified"},
	"rrsi-agentic-workspace": {"harvey-lab/evolve", "harvey-lab/held-out", "jobbench", "gdpval", "apex-agents"},
	"rrsi-engineering-design": {"engdesign-open", "engdesign-v1", "frontier-eng"},
}
var repairTrustedInventorySources = map[string]map[string]bool{
	"rrsi-coding": {"terminal-bench/terminal-bench-2-1": true},
	"rrsi-engineering-design": {"engdesign-open": true, "engdesign-v1": true},
}

func ValidateInventoryCoverage(domain string, complete bool, verified, missing []string) error {
	required, ok := repairRequiredInventorySources[domain]
	if !ok || len(verified)+len(missing) != len(required) || complete != (len(missing) == 0) {
		return errors.New("source-inventory coverage does not match the required domain sources")
	}
	want := make(map[string]bool, len(required))
	for _, source := range required {
		want[source] = true
	}
	seen := make(map[string]bool, len(required))
	for _, source := range verified {
		if !want[source] || seen[source] || !repairTrustedInventorySources[domain][source] {
			return errors.New("verified source inventory is untrusted or duplicated")
		}
		seen[source] = true
	}
	for _, source := range missing {
		if !want[source] || seen[source] {
			return errors.New("missing source inventory set is invalid")
		}
		seen[source] = true
	}
	if len(seen) != len(required) {
		return errors.New("source-inventory coverage omits required sources")
	}
	return nil
}

type RepairCandidate struct {
	Summary     string `json:"summary"`
	TargetsMode string `json:"targets_mode"`
	Edits       []Edit `json:"edits"`
	Diff        string `json:"diff"`
	StateFiles  string `json:"state_files"`
}

type RepairAttempt struct {
	Attempt              int                 `json:"attempt"`
	CandidateSHA256      string              `json:"candidate_sha256"`
	Summary              string              `json:"summary"`
	TargetsMode          string              `json:"targets_mode"`
	EditMetadata         []Edit              `json:"edit_metadata"`
	Status               string              `json:"status"`
	Verdict              *string             `json:"verdict"`
	ReasonCode           string              `json:"reason_code"`
	Findings             []string            `json:"findings"`
	MatchedDomainRuleIDs []string            `json:"matched_domain_rule_ids"`
	AnalystReview        RepairAnalystReview `json:"analyst_review"`
	RepairGenerated      bool                `json:"repair_generated"`
}

type RepairAnalystReview struct {
	Status         string   `json:"status"`
	ReasonCode     string   `json:"reason_code"`
	RiskLevel      string   `json:"risk_level"`
	RiskCategories []string `json:"risk_categories"`
	ValidationGaps []string `json:"validation_gaps"`
}

type RepairCritic struct {
	Status               string   `json:"status"`
	Verdict              *string  `json:"verdict"`
	ReasonCode           string   `json:"reason_code"`
	Findings             []string `json:"findings"`
	MatchedDomainRuleIDs []string `json:"matched_domain_rule_ids"`
}

type RepairResult struct {
	SchemaVersion         string          `json:"schema_version"`
	Domain                string          `json:"domain"`
	Status                string          `json:"status"`
	ReasonCode            string          `json:"reason_code"`
	ActionReady           bool            `json:"action_ready"`
	Candidate             json.RawMessage `json:"candidate"`
	CandidateSHA256       *string         `json:"candidate_sha256"`
	AttemptHistory        []RepairAttempt `json:"attempt_history"`
	AttemptCount          int             `json:"attempt_count"`
	RepairCount           int             `json:"repair_count"`
	FinalCritic           *RepairCritic   `json:"final_critic"`
	HumanApprovalRequired bool            `json:"human_approval_required"`
	EvaluationRequired    bool            `json:"evaluation_required"`
	ExecutionAuthorized   bool            `json:"execution_authorized"`
	DomainRulesComplete   *bool           `json:"domain_rules_complete,omitempty"`
	DomainRuleSetVersion  string          `json:"domain_rule_set_version,omitempty"`
	VerifiedInventorySources []string     `json:"verified_inventory_sources,omitempty"`
	MissingInventorySources  []string     `json:"missing_inventory_sources,omitempty"`
}

type RepairRun struct {
	ID        string       `json:"id"`
	ProjectID string       `json:"project_id"`
	CreatedAt string       `json:"created_at"`
	Actor     string       `json:"actor"`
	Reason    string       `json:"reason"`
	Result    RepairResult `json:"result"`
}

func ValidateRepairResult(result RepairResult, expectedDomain string) error {
	if (result.SchemaVersion != RepairResultSchemaVersion && result.SchemaVersion != RepairResultSchemaVersionV2) || result.Domain != expectedDomain || !validDomain(result.Domain) {
		return errors.New("evolution repair schema version or domain is invalid")
	}
	if result.SchemaVersion == RepairResultSchemaVersionV2 {
		if result.DomainRulesComplete == nil ||
			(result.DomainRuleSetVersion != "rrsi-domain-rules.v2" && result.DomainRuleSetVersion != "rrsi-domain-rules.v3") ||
			ValidateInventoryCoverage(result.Domain, *result.DomainRulesComplete, result.VerifiedInventorySources, result.MissingInventorySources) != nil {
			return errors.New("evolution repair source-inventory coverage is invalid")
		}
	} else if result.DomainRulesComplete != nil || result.DomainRuleSetVersion != "" ||
		len(result.VerifiedInventorySources) != 0 || len(result.MissingInventorySources) != 0 {
		return errors.New("legacy evolution repair result must not claim source-inventory coverage")
	}
	if result.ActionReady || !result.HumanApprovalRequired || !result.EvaluationRequired || result.ExecutionAuthorized {
		return errors.New("evolution repair must remain advisory and require human approval and evaluation")
	}
	if len(result.AttemptHistory) > 3 || result.AttemptCount != len(result.AttemptHistory) || result.RepairCount < 0 || result.RepairCount > 2 || result.RepairCount > result.AttemptCount {
		return errors.New("evolution repair attempt counters are inconsistent")
	}
	if err := validateRepairReason(result.Status, result.ReasonCode); err != nil {
		return err
	}
	if len(result.Candidate) == 0 {
		return errors.New("evolution repair candidate field is required")
	}
	var candidate *RepairCandidate
	if !bytes.Equal(bytes.TrimSpace(result.Candidate), []byte("null")) {
		var decoded RepairCandidate
		if err := decodeStrictJSON(result.Candidate, &decoded); err != nil {
			return errors.New("evolution repair candidate is invalid")
		}
		if err := validateRepairCandidate(decoded); err != nil {
			return err
		}
		candidate = &decoded
		canonical, err := canonicalJSON(result.Candidate)
		if err != nil {
			return errors.New("evolution repair candidate could not be canonicalized")
		}
		digest := sha256.Sum256(canonical)
		want := hex.EncodeToString(digest[:])
		if result.CandidateSHA256 == nil || *result.CandidateSHA256 != want {
			return errors.New("evolution repair candidate digest does not match its content")
		}
	} else if result.CandidateSHA256 != nil {
		return errors.New("rejected evolution repair candidate must not have a digest")
	}
	if result.Status == "reviewed" {
		if candidate == nil || result.FinalCritic == nil || result.FinalCritic.Status != "reviewed" || result.FinalCritic.Verdict == nil || *result.FinalCritic.Verdict != "accept" || result.ReasonCode != "local_critic_accept" {
			return errors.New("reviewed evolution repair lacks a matching local critic acceptance")
		}
	} else if candidate != nil {
		return errors.New("unaccepted evolution repair must not return a candidate")
	}
	if len(result.AttemptHistory) == 0 {
		if result.FinalCritic != nil || result.RepairCount != 0 || result.ReasonCode != "credential_material" && result.ReasonCode != "local_hf_backend_required" {
			return errors.New("evolution repair without critic attempts has inconsistent metadata")
		}
	} else if result.FinalCritic == nil {
		return errors.New("evolution repair attempts require final critic metadata")
	}
	generated := 0
	for index, attempt := range result.AttemptHistory {
		if attempt.Attempt != index+1 || !repairDigestPattern.MatchString(attempt.CandidateSHA256) || len(attempt.Summary) > 2000 || len(attempt.TargetsMode) > 2000 || strings.TrimSpace(attempt.Summary) == "" {
			return errors.New("evolution repair attempt metadata is invalid")
		}
		if attempt.RepairGenerated {
			generated++
			if index == len(result.AttemptHistory)-1 || attempt.Status != "blocked" || attempt.ReasonCode != "local_critic_reject" {
				return errors.New("last evolution repair attempt cannot claim an unreviewed generated repair")
			}
		}
		if err := validateRepairReason(attempt.Status, attempt.ReasonCode); err != nil {
			return errors.New("evolution repair attempt has an unsupported critic result")
		}
		if len(attempt.EditMetadata) == 0 || len(attempt.EditMetadata) > 80 || len(attempt.Findings) > 8 || len(attempt.MatchedDomainRuleIDs) > 32 {
			return errors.New("evolution repair attempt exceeds metadata limits")
		}
		if err := validateRepairAnalystReview(attempt.AnalystReview); err != nil {
			return err
		}
		for _, edit := range attempt.EditMetadata {
			if err := validateRepairEdit(edit); err != nil {
				return err
			}
		}
		for _, finding := range attempt.Findings {
			if finding == "" || len(finding) > 128 {
				return errors.New("evolution repair attempt finding is invalid")
			}
		}
		for _, ruleID := range attempt.MatchedDomainRuleIDs {
			if !regexp.MustCompile(`^[a-z0-9-]{1,64}$`).MatchString(ruleID) {
				return errors.New("evolution repair domain rule ID is invalid")
			}
		}
	}
	if generated != result.RepairCount {
		return errors.New("evolution repair count does not match attempt history")
	}
	if len(result.AttemptHistory) > 0 {
		last := result.AttemptHistory[len(result.AttemptHistory)-1]
		if result.FinalCritic.Status != last.Status || result.FinalCritic.ReasonCode != last.ReasonCode ||
			!equalOptionalString(result.FinalCritic.Verdict, last.Verdict) ||
			!equalStrings(result.FinalCritic.Findings, last.Findings) ||
			!equalStrings(result.FinalCritic.MatchedDomainRuleIDs, last.MatchedDomainRuleIDs) {
			return errors.New("final critic metadata does not match the last repair attempt")
		}
	}
	if candidate != nil {
		last := result.AttemptHistory[len(result.AttemptHistory)-1]
		if last.CandidateSHA256 != *result.CandidateSHA256 {
			return errors.New("accepted candidate digest does not match its final critic attempt")
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 256<<10 || privacy.ContainsCredential(encoded) {
		return errors.New("evolution repair result is oversized or contains credential material")
	}
	return nil
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateRepairCandidate(candidate RepairCandidate) error {
	if strings.TrimSpace(candidate.Summary) == "" || len(candidate.Summary) > 2000 || strings.TrimSpace(candidate.TargetsMode) == "" || len(candidate.TargetsMode) > 2000 ||
		strings.TrimSpace(candidate.Diff) == "" || len(candidate.Diff) > 16<<10 || len(candidate.StateFiles) > 4000 || len(candidate.Edits) == 0 || len(candidate.Edits) > 80 {
		return errors.New("evolution repair candidate exceeds limits")
	}
	for _, edit := range candidate.Edits {
		if err := validateRepairEdit(edit); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(candidate)
	if err != nil || len(encoded) > 64<<10 || privacy.ContainsCredential(encoded) {
		return errors.New("evolution repair candidate is oversized or contains credential material")
	}
	return nil
}

func validateRepairEdit(edit Edit) error {
	if !validComponent(edit.Component) || len(edit.ID) > 128 || len(edit.Hypothesis) > 4000 || len(edit.Mechanism) > 4000 ||
		len(edit.TargetsMode) > 2000 || len(edit.WhyNotLowerLever) > 2000 || len(edit.TriggerCondition) > 2000 ||
		len(edit.RetroactiveCheck) > 4000 || len(edit.RegressionRisk) > 2000 || len(edit.PredictedAffected) > 256 {
		return errors.New("evolution repair edit metadata is invalid")
	}
	for _, affected := range edit.PredictedAffected {
		if len(affected) > 128 {
			return errors.New("evolution repair affected item exceeds limits")
		}
	}
	return nil
}

func validateRepairReason(status, reason string) error {
	allowed := map[string]map[string]bool{
		"reviewed":        {"local_critic_accept": true},
		"blocked":         {"local_critic_reject": true, "deterministic_screen_block": true, "repair_loop_exhausted": true},
		"incomplete":      {"local_hf_backend_required": true, "local_model_unavailable": true, "invalid_repair_output": true, "no_safe_repair": true, "repair_loop_exhausted": true, "invalid_critic_output": true, "invalid_analyst_output": true},
		"privacy_blocked": {"credential_material": true, "unsafe_repair_output": true, "unsafe_model_output": true, "unsafe_analyst_output": true},
	}
	if !allowed[status][reason] {
		return fmt.Errorf("unsupported evolution repair status/reason pair %q/%q", status, reason)
	}
	return nil
}

func validateRepairAnalystReview(review RepairAnalystReview) error {
	if len(review.RiskCategories) > 8 || len(review.ValidationGaps) > 5 {
		return errors.New("evolution analyst review exceeds categorical limits")
	}
	risks := map[string]bool{
		"benchmark_specialization": true, "grader_access": true, "secret_handling": true,
		"authority_change": true, "unbounded_work": true, "state_leakage": true,
		"regression_surface": true, "unclear_mechanism": true,
	}
	gaps := map[string]bool{
		"structural_tests": true, "held_out_evaluation": true, "noise_calibration": true,
		"cost_token_measurement": true, "cross_harness_test": true,
	}
	for _, category := range review.RiskCategories {
		if !risks[category] {
			return errors.New("evolution analyst risk category is invalid")
		}
	}
	if hasDuplicate(review.RiskCategories) {
		return errors.New("evolution analyst risk categories must be unique")
	}
	for _, gap := range review.ValidationGaps {
		if !gaps[gap] {
			return errors.New("evolution analyst validation gap is invalid")
		}
	}
	if hasDuplicate(review.ValidationGaps) {
		return errors.New("evolution analyst validation gaps must be unique")
	}
	if review.Status == "reviewed" && review.ReasonCode == "local_analyst_review" &&
		(review.RiskLevel == "low" || review.RiskLevel == "medium" || review.RiskLevel == "high") {
		return nil
	}
	if review.Status == "skipped" && review.ReasonCode == "deterministic_screen_block" && review.RiskLevel == "unknown" &&
		len(review.RiskCategories) == 0 && len(review.ValidationGaps) == 0 {
		return nil
	}
	if review.Status == "incomplete" && (review.ReasonCode == "local_model_unavailable" || review.ReasonCode == "invalid_analyst_output") &&
		review.RiskLevel == "unknown" && len(review.RiskCategories) == 0 && len(review.ValidationGaps) == 0 {
		return nil
	}
	if review.Status == "privacy_blocked" && review.ReasonCode == "unsafe_analyst_output" &&
		review.RiskLevel == "unknown" && len(review.RiskCategories) == 0 && len(review.ValidationGaps) == 0 {
		return nil
	}
	return errors.New("evolution analyst review status and reason are inconsistent")
}

func hasDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func validDomain(domain string) bool {
	return domain == "rrsi-coding" || domain == "rrsi-agentic-workspace" || domain == "rrsi-engineering-design"
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func canonicalJSON(raw []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte("\n")), nil
}
