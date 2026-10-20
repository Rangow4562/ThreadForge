package continuity

import (
	"strings"
	"testing"
)

func TestTaskPlanValidationRequiresSafeExplicitExecutionScope(t *testing.T) {
	plan := TaskPlan{
		SchemaVersion: PlanSchemaVersion, Version: 1,
		Markdown: "Implement the parser fix and add regression coverage.",
		Decision: PlanUnconfirmed,
	}
	if err := ValidateTaskPlan(plan); err != nil {
		t.Fatalf("valid unconfirmed plan rejected: %v", err)
	}
	plan.Decision = PlanGovernedExecution
	plan.ConfirmedBy = "reviewer"
	plan.ConfirmedAt = "2026-09-24T12:00:00Z"
	plan.ConfirmReason = "The implementation plan and tests are accepted."
	if err := ValidateTaskPlan(plan); err == nil || !strings.Contains(err.Error(), "non-empty implementation_scope.include") {
		t.Fatalf("execution plan without scope error = %v", err)
	}
	plan.Scope = &ImplementationScope{Include: []string{"src/parser/**"}, Exclude: []string{"src/parser/generated/**"}, Modules: []string{"parser"}}
	if err := ValidateTaskPlan(plan); err != nil {
		t.Fatalf("valid scoped plan rejected: %v", err)
	}
	for _, invalid := range []string{"../outside/**", "/absolute/**", `src\\parser\\**`, ".env", "keys/private.pem", "src/[broken"} {
		scope := ImplementationScope{Include: []string{invalid}}
		if err := ValidateImplementationScope(scope); err == nil {
			t.Errorf("unsafe scope %q accepted", invalid)
		}
	}
	plan.Markdown = "Use API_TOKEN=secret-value"
	if err := ValidateTaskPlan(plan); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("credential-bearing plan error = %v", err)
	}
}

func TestImplementationScopeMatchingUsesRecursivePatternsAndExclusionsWin(t *testing.T) {
	scope := ImplementationScope{
		Include: []string{"src/**", "README.md"},
		Exclude: []string{"src/generated/**", "src/private/**"},
	}
	for _, repositoryPath := range []string{"src/main.go", "src/nested/worker.go", "README.md"} {
		if !PathInImplementationScope(scope, repositoryPath) {
			t.Errorf("path %q should match the declared scope", repositoryPath)
		}
	}
	for _, repositoryPath := range []string{
		"src/generated/build.go", "src/private/token.go", "docs/README.md",
		"../outside.go", "src/../outside.go", "src\\windows.go", "",
	} {
		if PathInImplementationScope(scope, repositoryPath) {
			t.Errorf("path %q should not match the declared scope", repositoryPath)
		}
	}
}

func TestTaskPlanValidationRequiresConfirmationMetadataOnlyAfterDecision(t *testing.T) {
	plan := TaskPlan{
		SchemaVersion: PlanSchemaVersion, Version: 1, Markdown: "Plan text",
		Decision: PlanUnconfirmed, ConfirmedBy: "reviewer",
	}
	if err := ValidateTaskPlan(plan); err == nil || !strings.Contains(err.Error(), "unconfirmed plans") {
		t.Fatalf("unconfirmed metadata error = %v", err)
	}
	plan.ConfirmedBy = ""
	plan.Decision = PlanOnly
	if err := ValidateTaskPlan(plan); err == nil || !strings.Contains(err.Error(), "require a bounded confirmer") {
		t.Fatalf("missing confirmation metadata error = %v", err)
	}
}

func TestTaskRequirementsValidationEnforcesClarificationAndPrivacyStates(t *testing.T) {
	requirements := TaskRequirements{
		SchemaVersion: RequirementsSchemaVersion,
		Status:        RequirementsDraft,
		Questions:     []RequirementQuestion{},
	}
	if err := ValidateTaskRequirements(requirements); err != nil {
		t.Fatalf("empty draft requirements rejected: %v", err)
	}
	question := RequirementQuestion{
		ID: "source-of-truth", Question: "Which source wins when records differ?",
		AskedBy: "operator", AskedAt: "2026-09-24T12:00:00Z",
	}
	requirements.Status = RequirementsClarifying
	requirements.Questions = []RequirementQuestion{question}
	if err := ValidateTaskRequirements(requirements); err != nil {
		t.Fatalf("valid clarification state rejected: %v", err)
	}
	requirements.Status = RequirementsFinalized
	requirements.FinalizedBy = "owner"
	requirements.FinalizedAt = "2026-09-24T12:01:00Z"
	requirements.FinalizeReason = "Reviewed the migration conflict policy."
	if err := ValidateTaskRequirements(requirements); err == nil || !strings.Contains(err.Error(), "questions remain unanswered") {
		t.Fatalf("unanswered requirements finalization error = %v", err)
	}
	question.Answer = "Conflicting records remain pending for human review."
	question.AnsweredBy = "owner"
	question.AnsweredAt = "2026-09-24T12:02:00Z"
	requirements.Questions[0] = question
	requirements.Status = RequirementsFinalized
	if err := ValidateTaskRequirements(requirements); err != nil {
		t.Fatalf("answered finalized requirements rejected: %v", err)
	}
	question.Question = "Set API_TOKEN=secret-value before continuing."
	requirements.Questions[0] = question
	if err := ValidateTaskRequirements(requirements); err == nil || !strings.Contains(err.Error(), "credential material") {
		t.Fatalf("credential-bearing clarification error = %v", err)
	}
}
