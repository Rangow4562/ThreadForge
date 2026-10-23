package migrate

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

const mancodeLegacyWorkflowSummarySchema = "threadforge.mancode-legacy-workflow-summary.v1"

var (
	mancodeLegacyWorkflowTaskID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)
	mancodeLegacyWorkflowKeys   = []string{
		"taskId", "task", "mode", "currentStep", "skippedSteps", "startedAt", "updatedAt", "status",
		"blockingReason", "parentTaskId", "outcome", "planVersion", "reviewPolicyVersion", "planningPolicyVersion",
		"verificationPolicyVersion", "requirementsStatus", "requirementsDigest", "planDecision", "verificationStatus",
	}
)

type mancodeLegacyWorkflowSummary struct {
	SchemaVersion             string `json:"schema_version"`
	Authority                 string `json:"authority"`
	HumanReviewRequired       bool   `json:"human_review_required"`
	ExecutionAuthorized       bool   `json:"execution_authorized"`
	TaskIDSHA256              string `json:"task_id_sha256"`
	WorkflowMode              string `json:"workflow_mode"`
	WorkflowStatus            string `json:"workflow_status"`
	CurrentStep               int64  `json:"current_step"`
	SkippedStepCount          int    `json:"skipped_step_count"`
	StartedAt                 string `json:"started_at"`
	UpdatedAt                 string `json:"updated_at"`
	TaskPresent               bool   `json:"task_present"`
	BlockingReasonPresent     bool   `json:"blocking_reason_present"`
	ParentTaskPresent         bool   `json:"parent_task_present"`
	Outcome                   string `json:"outcome,omitempty"`
	PlanVersion               *int64 `json:"plan_version,omitempty"`
	ReviewPolicyVersion       *int64 `json:"review_policy_version,omitempty"`
	PlanningPolicyVersion     *int64 `json:"planning_policy_version,omitempty"`
	VerificationPolicyVersion *int64 `json:"verification_policy_version,omitempty"`
	RequirementsStatus        string `json:"requirements_status,omitempty"`
	RequirementsDigestPresent bool   `json:"requirements_digest_present"`
	PlanDecision              string `json:"plan_decision,omitempty"`
	VerificationStatus        string `json:"verification_status,omitempty"`
	RawTaskIdentityOmitted    bool   `json:"raw_task_identity_omitted"`
	RawTaskTextOmitted        bool   `json:"raw_task_text_omitted"`
	RawBlockingReasonOmitted  bool   `json:"raw_blocking_reason_omitted"`
	RawParentTaskIDOmitted    bool   `json:"raw_parent_task_id_omitted"`
	RawSkippedStepsOmitted    bool   `json:"raw_skipped_steps_omitted"`
	SourceAuthorityOmitted    bool   `json:"source_authority_omitted"`
}

func mancodeLegacyWorkflowSourceAlias(relative string) string {
	return "mancode/legacy-workflows/" + digest([]byte(relative))
}

func normalizeMancodeLegacyWorkflow(relative, sourceRoot string, value any) ([]normalizedItem, bool, error) {
	taskID, eligible := isMancodeLegacyWorkflowMetadataPath(relative, sourceRoot)
	if !eligible {
		return nil, false, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode legacy workflow metadata must be an object")
	}
	required := []string{"task", "mode", "currentStep", "skippedSteps", "startedAt", "updatedAt", "status"}
	if !mancodeHasAllKeys(object, required) || !mancodeHasOnlyKeys(object, mancodeLegacyWorkflowKeys) {
		return nil, true, errors.New("Mancode legacy workflow metadata does not match the supported schema")
	}
	if sourceTaskID, exists := object["taskId"]; exists {
		id, valid := sourceTaskID.(string)
		if !valid || id != taskID {
			return nil, true, errors.New("Mancode legacy workflow path and task ID do not match")
		}
	}
	task, taskOK := object["task"].(string)
	mode, modeOK := object["mode"].(string)
	status, statusOK := object["status"].(string)
	step, stepOK := mancodeIntegerValue(object["currentStep"])
	if !taskOK || !mancodeBoundedString(task, 0, 32<<10) || !modeOK || !statusOK || !stepOK {
		return nil, true, errors.New("Mancode legacy workflow metadata has invalid required fields")
	}
	maxStep := int64(9)
	if mode == "mamba" {
		maxStep = 5
	} else if mode != "man" && mode != "manteam" {
		return nil, true, errors.New("Mancode legacy workflow mode is unsupported")
	}
	if step < 1 || step > maxStep || !containsString([]string{"in_progress", "planned", "completed", "blocked", "abandoned"}, status) ||
		(mode == "mamba" && status == "planned") {
		return nil, true, errors.New("Mancode legacy workflow step or status is invalid")
	}
	skipped, skippedOK := object["skippedSteps"].([]any)
	if !skippedOK || len(skipped) > 64 {
		return nil, true, errors.New("Mancode legacy workflow skipped-step list is invalid")
	}
	for _, item := range skipped {
		label, valid := item.(string)
		if !valid || !mancodeBoundedString(label, 0, 256) {
			return nil, true, errors.New("Mancode legacy workflow skipped-step list is invalid")
		}
	}
	if version, exists := object["reviewPolicyVersion"]; exists {
		value, valid := mancodeIntegerValue(version)
		if !valid || (value != 1 && value != 2) || mode == "mamba" {
			return nil, true, errors.New("Mancode legacy workflow review policy version is invalid")
		}
	}
	if version, exists := object["planningPolicyVersion"]; exists {
		value, valid := mancodeIntegerValue(version)
		if !valid || (value != 1 && value != 2) || mode == "mamba" {
			return nil, true, errors.New("Mancode legacy workflow planning policy version is invalid")
		}
		if value == 2 {
			for _, item := range skipped {
				if item != "clarification" && item != "review" {
					return nil, true, errors.New("Mancode legacy workflow skipped steps violate planning policy")
				}
			}
		}
	}
	planVersion, planVersionOK := legacyWorkflowOptionalInteger(object, "planVersion", 1, int64(^uint64(0)>>1))
	verificationPolicyVersion, verificationPolicyOK := legacyWorkflowOptionalInteger(object, "verificationPolicyVersion", 1, 1)
	if !planVersionOK || !verificationPolicyOK || (mode == "mamba" && (planVersion != nil || verificationPolicyVersion != nil)) {
		return nil, true, errors.New("Mancode legacy workflow version field is invalid")
	}
	reviewPolicyVersion, _ := legacyWorkflowOptionalInteger(object, "reviewPolicyVersion", 1, 2)
	planningPolicyVersion, _ := legacyWorkflowOptionalInteger(object, "planningPolicyVersion", 1, 2)
	if digestValue, exists := object["requirementsDigest"]; exists {
		digestText, valid := digestValue.(string)
		if !valid || !mancodeBoundedString(digestText, 0, 128) || mode == "mamba" {
			return nil, true, errors.New("Mancode legacy workflow requirements digest is invalid")
		}
	}
	requirementsStatus, requirementsStatusOK := legacyWorkflowOptionalString(object, "requirementsStatus", []string{"ready", "needs_clarification"})
	planDecision, planDecisionOK := legacyWorkflowOptionalString(object, "planDecision", []string{"plan_only", "solo_handoff", "governed_execution"})
	verificationStatus, verificationStatusOK := legacyWorkflowOptionalString(object, "verificationStatus", []string{"pending", "passed", "failed", "manual_required", "blocked"})
	if !requirementsStatusOK || !planDecisionOK || !verificationStatusOK ||
		(mode == "mamba" && (requirementsStatus != "" || planDecision != "" || verificationStatus != "" || legacyWorkflowHasKey(object, "requirementsDigest"))) {
		return nil, true, errors.New("Mancode legacy workflow governance state is invalid")
	}
	outcome, outcomeOK := legacyWorkflowOptionalString(object, "outcome", []string{"fixed", "verified", "no_repro", "manual_test_required"})
	if !outcomeOK || (mode != "mamba" && outcome != "") || (mode == "mamba" && status != "completed" && outcome != "") ||
		(mode == "mamba" && status == "completed" && outcome == "") {
		return nil, true, errors.New("Mancode legacy workflow outcome is invalid")
	}
	parentTaskID, parentPresent := object["parentTaskId"]
	if parentPresent {
		parentID, valid := parentTaskID.(string)
		if mode != "mamba" || !valid || !mancodeLegacyWorkflowTaskID.MatchString(parentID) {
			return nil, true, errors.New("Mancode legacy workflow parent task is invalid")
		}
	}
	blockingReason, blockingPresent := object["blockingReason"]
	if blockingPresent {
		reason, valid := blockingReason.(string)
		if status != "blocked" || !valid || !mancodeBoundedString(reason, 1, 32<<10) || strings.TrimSpace(reason) == "" {
			return nil, true, errors.New("Mancode legacy workflow blocking reason is invalid")
		}
	} else if status == "blocked" {
		return nil, true, errors.New("blocked Mancode legacy workflows require a reason")
	}
	startedAt, startedOK := mancodeCoordinationTime(object["startedAt"])
	updatedAt, updatedOK := mancodeCoordinationTime(object["updatedAt"])
	if !startedOK || !updatedOK {
		return nil, true, errors.New("Mancode legacy workflow timestamps are invalid")
	}
	started, _ := time.Parse(time.RFC3339Nano, startedAt)
	updated, _ := time.Parse(time.RFC3339Nano, updatedAt)
	if updated.Before(started) {
		return nil, true, errors.New("Mancode legacy workflow timestamps are out of order")
	}
	summary := mancodeLegacyWorkflowSummary{
		SchemaVersion: mancodeLegacyWorkflowSummarySchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
		TaskIDSHA256: mancodeCoordinationIdentityHash("legacy-workflow-task-id", taskID),
		WorkflowMode: mode, WorkflowStatus: status, CurrentStep: step, SkippedStepCount: len(skipped),
		StartedAt: startedAt, UpdatedAt: updatedAt, TaskPresent: true, BlockingReasonPresent: blockingPresent,
		ParentTaskPresent: parentPresent, Outcome: outcome, PlanVersion: planVersion,
		ReviewPolicyVersion: reviewPolicyVersion, PlanningPolicyVersion: planningPolicyVersion,
		VerificationPolicyVersion: verificationPolicyVersion, RequirementsStatus: requirementsStatus,
		RequirementsDigestPresent: legacyWorkflowHasKey(object, "requirementsDigest"), PlanDecision: planDecision,
		VerificationStatus: verificationStatus, RawTaskIdentityOmitted: true, RawTaskTextOmitted: true,
		RawBlockingReasonOmitted: true, RawParentTaskIDOmitted: true, RawSkippedStepsOmitted: true,
		SourceAuthorityOmitted: true,
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		return nil, true, errors.New("unable to serialize Mancode legacy workflow summary")
	}
	return []normalizedItem{{Type: "mancode_legacy_workflow_summary", Payload: payload}}, true, nil
}

func legacyWorkflowOptionalInteger(object map[string]any, key string, minimum, maximum int64) (*int64, bool) {
	value, exists := object[key]
	if !exists {
		return nil, true
	}
	integer, ok := mancodeIntegerValue(value)
	if !ok || integer < minimum || integer > maximum {
		return nil, false
	}
	return &integer, true
}

func legacyWorkflowOptionalString(object map[string]any, key string, allowed []string) (string, bool) {
	value, exists := object[key]
	if !exists {
		return "", true
	}
	text, ok := value.(string)
	if !ok || !containsString(allowed, text) {
		return "", false
	}
	return text, true
}

func legacyWorkflowHasKey(object map[string]any, key string) bool {
	_, exists := object[key]
	return exists
}
