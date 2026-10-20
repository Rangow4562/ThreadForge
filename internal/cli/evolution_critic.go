package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/evolution"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

type evolutionCriticRequest struct {
	SchemaVersion       string                       `json:"schema_version"`
	Domain              string                       `json:"domain"`
	Diff                string                       `json:"diff"`
	Summary             string                       `json:"summary"`
	TargetsMode         string                       `json:"targets_mode"`
	Edits               []evolution.Edit             `json:"edits"`
	StateFiles          string                       `json:"state_files,omitempty"`
	DeniedLiterals      []evolutionDenyLiteral       `json:"denied_literals,omitempty"`
	BenchmarkInventories []evolutionBenchmarkInventory `json:"benchmark_inventories,omitempty"`
	Backend             map[string]any               `json:"backend"`
}

type evolutionCriticResponse struct {
	SchemaVersion         string   `json:"schema_version"`
	Domain                string   `json:"domain"`
	Status                string   `json:"status"`
	ActionReady           bool     `json:"action_ready"`
	Findings              []string `json:"findings"`
	MatchedRuleIndices    []int    `json:"matched_rule_indices"`
	MatchedDomainRuleIDs  []string `json:"matched_domain_rule_ids"`
	Component             string   `json:"component"`
	DomainRulesLoaded     bool     `json:"domain_rules_loaded"`
	DomainRulesComplete   bool     `json:"domain_rules_complete"`
	DomainRuleSetVersion  string   `json:"domain_rule_set_version"`
	VerifiedInventorySources []string `json:"verified_inventory_sources,omitempty"`
	MissingInventorySources  []string `json:"missing_inventory_sources,omitempty"`
	Verdict               *string  `json:"verdict"`
	ReasonCode            string   `json:"reason_code"`
	ReasonCount           *int     `json:"reason_count,omitempty"`
	RiskCount             *int     `json:"risk_count,omitempty"`
	CriticReviewRequired  bool     `json:"critic_review_required"`
	HumanApprovalRequired bool     `json:"human_approval_required"`
	ExecutionAuthorized   bool     `json:"execution_authorized"`
}

func runEvolutionCritic(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution critic", stderr)
	file := fs.String("file", "", "versioned local RRSI critic request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned local-only JSON-stdio critic")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution critic --file <critic-request.json> [--python python]")
		return 2
	}
	input, err := readBoundedText(*file, 768<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing critic request rejected before inference")
		return 1
	}
	var request evolutionCriticRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		(request.SchemaVersion != "threadforge.evolution-critic-request.v1" && request.SchemaVersion != "threadforge.evolution-critic-request.v2") ||
		strings.TrimSpace(request.Domain) == "" || strings.TrimSpace(request.Diff) == "" || strings.TrimSpace(request.Summary) == "" ||
		strings.TrimSpace(request.TargetsMode) == "" || len(request.Edits) == 0 || request.Backend == nil {
		_, _ = fmt.Fprintln(stderr, "invalid versioned evolution critic request")
		return 1
	}
	inventoryAware := request.SchemaVersion == "threadforge.evolution-critic-request.v2"
	if !inventoryAware && request.BenchmarkInventories != nil {
		_, _ = fmt.Fprintln(stderr, "benchmark inventories require critic request v2")
		return 1
	}
	if !inventoryAware && len(input) > 256<<10 {
		_, _ = fmt.Fprintln(stderr, "legacy critic request exceeds its size limit")
		return 1
	}
	deniedLiterals := request.DeniedLiterals
	if deniedLiterals == nil {
		deniedLiterals = []evolutionDenyLiteral{}
	}
	params := map[string]any{
		"domain": request.Domain, "diff": request.Diff, "summary": request.Summary,
		"targets_mode": request.TargetsMode, "edits": request.Edits, "state_files": request.StateFiles,
		"denied_literals": deniedLiterals, "backend": request.Backend,
	}
	operation := "evolution.critic"
	maxParams := 240 << 10
	if inventoryAware {
		operation = "evolution.critic.v2"
		maxParams = 768 << 10
		if request.BenchmarkInventories == nil {
			request.BenchmarkInventories = []evolutionBenchmarkInventory{}
		}
		params["benchmark_inventories"] = request.BenchmarkInventories
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil || len(paramsJSON) > maxParams || privacy.ContainsCredential(paramsJSON) {
		_, _ = fmt.Fprintln(stderr, "critic request is invalid or credential-bearing")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-critic", operation, paramsJSON)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "local evolution critic failed safely")
		return 1
	}
	var result evolutionCriticResponse
	if err := decodeJSONBytes(response.Result, &result); err != nil || !validEvolutionCriticResponse(result, request.Domain, inventoryAware) {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe evolution critic response")
		return 1
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func validEvolutionCriticResponse(result evolutionCriticResponse, expectedDomain string, inventoryAware bool) bool {
	wantSchema := "threadforge.evolution-critic.v1"
	wantRules := "rrsi-domain-rules.v1"
	if inventoryAware {
		wantSchema = "threadforge.evolution-critic.v2"
		wantRules = "rrsi-domain-rules.v3"
	}
	if result.SchemaVersion != wantSchema || result.Domain != expectedDomain || result.ActionReady || !result.DomainRulesLoaded ||
		result.DomainRuleSetVersion != wantRules || !result.HumanApprovalRequired || result.ExecutionAuthorized {
		return false
	}
	if inventoryAware {
		if !validEvolutionInventoryCoverage(expectedDomain, result.DomainRulesComplete, result.VerifiedInventorySources, result.MissingInventorySources) {
			return false
		}
	} else if result.DomainRulesComplete || len(result.VerifiedInventorySources) != 0 || len(result.MissingInventorySources) != 0 {
		return false
	}
	validReasonCodes := map[string]bool{
		"deterministic_screen_block": true, "credential_material": true, "local_hf_backend_required": true,
		"local_model_unavailable": true, "unsafe_model_output": true, "invalid_critic_output": true,
		"local_critic_accept": true, "local_critic_reject": true,
	}
	if !validReasonCodes[result.ReasonCode] || len(result.Findings) > 8 || len(result.MatchedRuleIndices) > 512 || len(result.MatchedDomainRuleIDs) > 32 {
		return false
	}
	for _, finding := range result.Findings {
		if len(finding) == 0 || len(finding) > 128 {
			return false
		}
	}
	for _, index := range result.MatchedRuleIndices {
		if index < 0 {
			return false
		}
	}
	seenRules := make(map[string]bool, len(result.MatchedDomainRuleIDs))
	for _, ruleID := range result.MatchedDomainRuleIDs {
		if len(ruleID) == 0 || len(ruleID) > 64 || seenRules[ruleID] {
			return false
		}
		seenRules[ruleID] = true
	}
	if result.ReasonCount != nil && (*result.ReasonCount < 0 || *result.ReasonCount > 8) ||
		result.RiskCount != nil && (*result.RiskCount < 0 || *result.RiskCount > 8) {
		return false
	}
	switch result.Status {
	case "reviewed":
		return result.Verdict != nil && *result.Verdict == "accept" && result.ReasonCode == "local_critic_accept" &&
			!result.CriticReviewRequired && result.ReasonCount != nil && result.RiskCount != nil && *result.ReasonCount <= 8 && *result.RiskCount <= 8
	case "blocked":
		return result.Verdict != nil && *result.Verdict == "reject" && !result.CriticReviewRequired &&
			(result.ReasonCode == "deterministic_screen_block" || result.ReasonCode == "local_critic_reject")
	case "privacy_blocked", "incomplete":
		if result.Verdict != nil || !result.CriticReviewRequired {
			return false
		}
		if result.Status == "privacy_blocked" {
			return result.ReasonCode == "credential_material" || result.ReasonCode == "unsafe_model_output"
		}
		return result.ReasonCode == "local_hf_backend_required" || result.ReasonCode == "local_model_unavailable" || result.ReasonCode == "invalid_critic_output"
	default:
		return false
	}
}
