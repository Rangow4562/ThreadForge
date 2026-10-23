package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const mancodeOperationAuditSchema = "threadforge.mancode-operation-audit.v1"
const mancodeProjectConfigImportSchema = "threadforge.mancode-project-config.v1"
const mancodeLegacyStateImportSchema = "threadforge.mancode-legacy-state.v1"
const mancodeProjectProfileImportSchema = "threadforge.mancode-project-profile.v1"
const mancodeProjectFactImportSchema = "threadforge.mancode-project-fact.v1"
const mancodeLegacyConfigImportSchema = "threadforge.mancode-legacy-config.v1"
const mancodeGitRefAuthorityIssue = "configured git-ref team authority is not reconciled from the project-tree snapshot"

var mancodeOperationIDPattern = regexp.MustCompile(`^[0-7][0-9A-HJKMNPQRSTVWXYZ]{25}$`)

type mancodeOperationAudit struct {
	SchemaVersion            string `json:"schema_version"`
	Authority                string `json:"authority"`
	HumanReviewRequired      bool   `json:"human_review_required"`
	ExecutionAuthorized      bool   `json:"execution_authorized"`
	OperationIDSHA256        string `json:"operation_id_sha256"`
	OperationType            string `json:"operation_type"`
	SourceState              string `json:"source_state"`
	StartedAt                string `json:"started_at"`
	UpdatedAt                string `json:"updated_at"`
	RequiresReconciliation   bool   `json:"requires_reconciliation"`
	RawSourceIdentityOmitted bool   `json:"raw_source_identity_omitted"`
	SourceAuthorityOmitted   bool   `json:"source_authority_omitted"`
}

type mancodeProjectConfigImport struct {
	SchemaVersion  string `json:"schema_version"`
	Revision       int64  `json:"revision"`
	WorkspaceID    string `json:"workspace_id"`
	TransportMode  string `json:"transport_mode"`
	TransportEpoch int64  `json:"transport_epoch"`
	RemoteOmitted  bool   `json:"remote_omitted"`
	UpdatedAt      string `json:"updated_at"`
}

type mancodeProjectFactImport struct {
	SchemaVersion       string `json:"schema_version"`
	Authority           string `json:"authority"`
	HumanReviewRequired bool   `json:"human_review_required"`
	ExecutionAuthorized bool   `json:"execution_authorized"`
	Revision            int64  `json:"revision"`
	LegacyTrust         string `json:"legacy_trust"`
	DetectedAt          string `json:"detected_at"`
	ProjectKind         string `json:"project_kind"`
	LanguageCount       int    `json:"language_count"`
	FrameworkCount      int    `json:"framework_count"`
	SourceRootCount     int    `json:"source_root_count"`
	ManifestCount       int    `json:"manifest_count"`
	ValidationCount     int    `json:"validation_count"`
	UIAssets            string `json:"ui_assets"`
	BrowserAutomation   string `json:"browser_automation"`
	Confidence          string `json:"confidence"`
}

func eligibleMancodePath(relative, sourceRoot string) bool {
	lower := mancodePath(relative, sourceRoot)
	base := strings.ToLower(filepath.Base(relative))
	isDocument := extIs(relative, ".json", ".md", ".mdx")
	if isMancodeProgressHTMLPath(relative) {
		return extIs(relative, ".html")
	}

	switch lower {
	case "/state.json", "/.mancode/state.json":
		return extIs(relative, ".json")
	case "/config.json", "/.mancode/config.json", "/.mancode/shared/config.json",
		"/.mancode/schema.json", "/.mancode/local/actor.json", "/.mancode/shared/team/policy.json":
		return extIs(relative, ".json")
	case "/project-profile.json", "/.mancode/project-profile.json":
		return extIs(relative, ".json")
	}

	for _, prefix := range []string{
		"/workflows/", "/.mancode/workflows/",
		"/memory/", "/.mancode/memory/",
		"/.mancode/local/workflows/", "/.mancode/shared/workflows/",
	} {
		if strings.HasPrefix(lower, prefix) {
			return isDocument
		}
	}
	for _, prefix := range []string{
		"/.mancode/shared/context/", "/.mancode/shared/memory/decisions/",
		"/.mancode/local/memory/decisions/",
		"/.mancode/shared/team/actors/", "/.mancode/shared/team/events/",
	} {
		if strings.HasPrefix(lower, prefix) {
			if prefix == "/.mancode/shared/context/" {
				return extIs(relative, ".json") && (strings.Contains(base, "project") || strings.Contains(base, "glossary") ||
					strings.Contains(base, "decision") || strings.Contains(base, "policy") || strings.Contains(base, "design") ||
					strings.Contains(base, "privacy"))
			}
			return extIs(relative, ".json")
		}
	}
	if strings.HasPrefix(lower, "/.mancode/local/runtime/operations/") && extIs(relative, ".json") {
		return true
	}
	if isMancodeCoordinationRoot(sourceRoot) {
		for _, directory := range []string{"claims", "handoffs", "operations", "task-heads"} {
			if strings.HasPrefix(lower, "/"+directory+"/") && extIs(relative, ".json") {
				return true
			}
		}
	}
	return false
}

func mancodePath(relative, sourceRoot string) string {
	value := strings.ToLower(filepath.ToSlash(strings.TrimPrefix(relative, "./")))
	value = "/" + strings.TrimPrefix(value, "/")
	if strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".mancode") {
		return "/.mancode" + value
	}
	return value
}

func isMancodeWorkflowCheckpointDirectory(relative, sourceRoot string) bool {
	lower := mancodePath(relative, sourceRoot)
	for _, prefix := range []string{
		"/workflows/", "/.mancode/workflows/",
		"/.mancode/local/workflows/", "/.mancode/shared/workflows/",
	} {
		if strings.HasPrefix(lower, prefix) {
			parts := strings.Split(strings.Trim(strings.TrimPrefix(lower, prefix), "/"), "/")
			return len(parts) == 2 && parts[1] == "checkpoints"
		}
	}
	return false
}

func isMancodeCoordinationRoot(sourceRoot string) bool {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(sourceRoot)))
	parts := strings.Split(strings.Trim(clean, "/"), "/")
	if len(parts) >= 3 && parts[len(parts)-3] == "mancode" && parts[len(parts)-2] == "workspaces" {
		return true
	}
	return len(parts) >= 4 && parts[len(parts)-4] == ".mancode" &&
		parts[len(parts)-3] == "runtime" && parts[len(parts)-2] == "non-git"
}

func classifyMancodeRecord(relative string) string {
	lower := mancodePath(relative, "")
	base := strings.ToLower(filepath.Base(relative))
	if isMancodeProgressHTMLPath(relative) {
		return "mancode_progress_projection"
	}
	switch lower {
	case "/state.json", "/.mancode/state.json":
		return "mancode_legacy_state"
	case "/config.json", "/.mancode/config.json":
		return "mancode_legacy_config"
	case "/.mancode/shared/config.json", "/shared/config.json":
		return "mancode_project_config"
	case "/.mancode/schema.json", "/schema.json":
		return "mancode_schema_manifest"
	case "/.mancode/local/actor.json", "/local/actor.json":
		return "mancode_actor_identity"
	case "/.mancode/shared/team/policy.json", "/shared/team/policy.json":
		return "mancode_team_policy"
	case "/project-profile.json", "/.mancode/project-profile.json":
		return "mancode_project_profile"
	}
	if base == "project.json" && strings.Contains(lower, "/context/") {
		return "project_fact"
	}

	for _, prefix := range []string{"/.mancode/shared/team/actors/", "/shared/team/actors/"} {
		if strings.HasPrefix(lower, prefix) {
			return "mancode_actor_profile"
		}
	}
	for _, prefix := range []string{"/.mancode/shared/team/events/", "/shared/team/events/"} {
		if strings.HasPrefix(lower, prefix) {
			return "mancode_team_event"
		}
	}
	for _, prefix := range []string{"/claims/", "/.mancode/runtime/non-git/", "/mancode/workspaces/"} {
		if strings.HasPrefix(lower, prefix) && strings.Contains(lower, "/claims/") {
			return "mancode_team_claim"
		}
	}
	if strings.Contains(lower, "/handoffs/") {
		return "mancode_team_handoff"
	}
	if strings.Contains(lower, "/task-heads/") {
		return "mancode_team_head_fence"
	}
	if strings.Contains(lower, "/operations/") && base != "" {
		return "mancode_operation_audit"
	}
	if strings.Contains(lower, "/shared/memory/decisions/") || strings.Contains(lower, "/memory/decisions/") || strings.Contains(lower, "/decision") {
		return "decision_record"
	}
	if strings.Contains(lower, "/shared/context/") || strings.Contains(lower, "/context/") {
		switch base {
		case "project.json":
			return "project_fact"
		case "glossary.json":
			return "project_glossary"
		case "design-policy.json", "privacy-policy.json", "privacy-exclusions.json", "policy.json":
			return "project_policy"
		}
		return "project_record"
	}
	if strings.Contains(lower, "/workflows/") {
		if strings.Contains(lower, "/checkpoints/") {
			return "workflow_checkpoint"
		}
		switch base {
		case "metadata.json", "workflow.json", "state.json":
			return "workflow_metadata"
		case "requirements.json":
			return "workflow_requirements"
		case "review-ledger.json", "review.json":
			return "workflow_review"
		case "verification-ledger.json", "verification.json":
			return "workflow_verification"
		case "plan.md", "plan.mdx":
			return "workflow_plan"
		case "checkpoint.json":
			return "workflow_checkpoint"
		default:
			if strings.HasSuffix(base, ".md") || strings.HasSuffix(base, ".mdx") {
				return "workflow_document"
			}
			return "workflow_state"
		}
	}
	return "project_record"
}

func normalizeMancodeItems(sourceRoot, relative string, value any) ([]normalizedItem, bool, error) {
	if items, handled, err := normalizeMancodeLegacyWorkflow(relative, sourceRoot, value); handled || err != nil {
		return items, handled, err
	}
	recordType := classifyMancodeRecord(relative)
	if recordType == "mancode_legacy_state" {
		return normalizeMancodeLegacyState(value)
	}
	if recordType == "mancode_schema_manifest" {
		return normalizeMancodeSchemaManifest(value)
	}
	if recordType == "mancode_project_profile" {
		return normalizeMancodeProjectProfile(value)
	}
	if recordType == "project_fact" {
		return normalizeMancodeProjectFact(value)
	}
	if recordType == "project_glossary" {
		return normalizeMancodeProjectGlossary(value)
	}
	if recordType == "project_policy" {
		switch strings.ToLower(filepath.Base(relative)) {
		case "design-policy.json":
			return normalizeMancodeDesignPolicy(value)
		case "privacy-policy.json":
			return normalizeMancodePrivacyPolicy(value)
		case "privacy-exclusions.json":
			return normalizeMancodePrivacyExclusions(value)
		}
	}
	if recordType == "decision_record" && isMancodeConfirmedDecisionPath(relative) {
		return normalizeMancodeConfirmedDecision(relative, value)
	}
	if recordType == "mancode_legacy_config" {
		return normalizeMancodeLegacyConfig(value)
	}
	if recordType == "mancode_project_config" {
		return normalizeMancodeProjectConfig(value)
	}
	if recordType == "mancode_team_policy" {
		return normalizeMancodeTeamPolicy(value)
	}
	if recordType == "mancode_actor_identity" || recordType == "mancode_actor_profile" {
		return normalizeMancodeActor(relative, value)
	}
	if recordType == "mancode_team_event" {
		return normalizeMancodeTeamEvent(relative, value)
	}
	if isMancodeCoordinationRecordType(recordType) {
		item, err := normalizeMancodeCoordinationRecord(sourceRoot, relative, recordType, value)
		if err != nil {
			return nil, true, err
		}
		return []normalizedItem{item}, true, nil
	}
	return nil, false, nil
}

type mancodeLegacyStateImport struct {
	SchemaVersion         string `json:"schema_version"`
	Authority             string `json:"authority"`
	HumanReviewRequired   bool   `json:"human_review_required"`
	ExecutionAuthorized   bool   `json:"execution_authorized"`
	CurrentMode           string `json:"current_mode"`
	LastMode              string `json:"last_mode"`
	CurrentWorkflowMode   any    `json:"current_workflow_mode"`
	TaskPresent           bool   `json:"task_present"`
	SkippedStepCount      int    `json:"skipped_step_count"`
	ActiveSoloPlanPresent bool   `json:"active_solo_plan_present"`
	ActiveSoloPlanVersion *int64 `json:"active_solo_plan_version,omitempty"`
	TeamModeAutoDetected  bool   `json:"team_mode_auto_detected"`
	ContributorCount      int64  `json:"contributor_count"`
	ProjectMode           string `json:"project_mode,omitempty"`
	InitializedAt         string `json:"initialized_at"`
}

func normalizeMancodeLegacyState(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode legacy state must be an object")
	}
	requiredKeys := []string{
		"version", "currentMode", "lastMode", "platform", "initializedAt", "techStack", "uiLibrary",
		"currentTask", "currentWorkflowMode", "skippedSteps", "activeSoloPlan", "teamModeAutoDetected", "contributors",
	}
	allowedKeys := append(append([]string(nil), requiredKeys...), "projectMode")
	if !mancodeHasAllKeys(object, requiredKeys) || !mancodeHasOnlyKeys(object, allowedKeys) {
		return nil, true, errors.New("Mancode legacy state does not match the supported schema")
	}
	version, versionOK := object["version"].(string)
	platform, platformOK := object["platform"].(string)
	techStack, techStackOK := object["techStack"].(string)
	uiLibrary, uiLibraryOK := object["uiLibrary"].(string)
	initializedAt, initializedAtOK := object["initializedAt"].(string)
	currentMode := mancodeString(object["currentMode"])
	lastMode := mancodeString(object["lastMode"])
	if !versionOK || !mancodeBoundedString(version, 1, 128) ||
		!platformOK || !mancodeLegacyPlatform(platform) ||
		!techStackOK || !mancodeBoundedString(techStack, 0, 4096) ||
		!uiLibraryOK || !mancodeBoundedString(uiLibrary, 0, 4096) ||
		!mancodeHasString([]string{"solo", "man", "mamba", "manteam", "manps"}, currentMode) ||
		!mancodeHasString([]string{"solo", "man", "mamba", "manteam", "manps"}, lastMode) ||
		!initializedAtOK {
		return nil, true, errors.New("Mancode legacy state has invalid metadata")
	}
	if _, err := time.Parse(time.RFC3339Nano, initializedAt); err != nil {
		return nil, true, errors.New("Mancode legacy state initialization time is invalid")
	}

	currentTask, taskOK := object["currentTask"]
	taskPresent := false
	if currentTask != nil {
		taskText, isString := currentTask.(string)
		if !isString || !mancodeBoundedString(taskText, 0, 16384) {
			return nil, true, errors.New("Mancode legacy state task is invalid")
		}
		taskPresent = strings.TrimSpace(taskText) != ""
	}
	if !taskOK {
		return nil, true, errors.New("Mancode legacy state task is missing")
	}

	workflowMode := object["currentWorkflowMode"]
	var projectedWorkflowMode any
	if workflowMode != nil {
		mode, isString := workflowMode.(string)
		if !isString || !mancodeHasString([]string{"man", "mamba", "manteam"}, mode) {
			return nil, true, errors.New("Mancode legacy state workflow mode is invalid")
		}
		projectedWorkflowMode = mode
	}

	skippedSteps, skippedOK := object["skippedSteps"].([]any)
	if !skippedOK || len(skippedSteps) > 128 {
		return nil, true, errors.New("Mancode legacy state skipped steps are invalid")
	}
	for _, step := range skippedSteps {
		text, isString := step.(string)
		if !isString || !mancodeBoundedString(text, 0, 256) {
			return nil, true, errors.New("Mancode legacy state skipped steps are invalid")
		}
	}

	var activeSoloPlanVersion *int64
	activeSoloPlanPresent := false
	if activePlan := object["activeSoloPlan"]; activePlan != nil {
		plan, isObject := activePlan.(map[string]any)
		if !isObject || !mancodeHasOnlyKeys(plan, []string{"taskId", "planVersion"}) ||
			!mancodeHasAllKeys(plan, []string{"taskId", "planVersion"}) {
			return nil, true, errors.New("Mancode legacy state active solo plan is invalid")
		}
		taskID, taskIDOK := plan["taskId"].(string)
		planVersion, versionOK := mancodeIntegerValue(plan["planVersion"])
		if !taskIDOK || !mancodeBoundedString(taskID, 1, 512) || !versionOK || planVersion < 1 {
			return nil, true, errors.New("Mancode legacy state active solo plan is invalid")
		}
		activeSoloPlanPresent = true
		activeSoloPlanVersion = &planVersion
	}

	autoDetected, autoDetectedOK := object["teamModeAutoDetected"].(bool)
	contributors, contributorsOK := mancodeIntegerValue(object["contributors"])
	if !autoDetectedOK || !contributorsOK || contributors < 0 || contributors > 1000000 {
		return nil, true, errors.New("Mancode legacy state team metadata is invalid")
	}
	projectMode := ""
	if rawProjectMode, exists := object["projectMode"]; exists {
		projectMode, ok = rawProjectMode.(string)
		if !ok || !mancodeHasString([]string{"generic", "detected"}, projectMode) {
			return nil, true, errors.New("Mancode legacy state project mode is invalid")
		}
	}

	imported := mancodeLegacyStateImport{
		SchemaVersion: mancodeLegacyStateImportSchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
		CurrentMode: currentMode, LastMode: lastMode, CurrentWorkflowMode: projectedWorkflowMode,
		TaskPresent: taskPresent, SkippedStepCount: len(skippedSteps),
		ActiveSoloPlanPresent: activeSoloPlanPresent, ActiveSoloPlanVersion: activeSoloPlanVersion,
		TeamModeAutoDetected: autoDetected, ContributorCount: contributors,
		ProjectMode: projectMode, InitializedAt: initializedAt,
	}
	payload, err := json.Marshal(imported)
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode legacy state projection: %w", err)
	}
	return []normalizedItem{{Type: "mancode_legacy_state", Payload: payload}}, true, nil
}

func mancodeLegacyPlatform(value string) bool {
	return mancodeHasString([]string{"claude-code", "cursor", "codex", "copilot", "zcode", "kimi-code", "qoder", "dsh"}, value)
}

func mancodeBoundedString(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum && !strings.ContainsRune(value, '\x00')
}

type mancodeProjectProfileImport struct {
	SchemaVersion       string `json:"schema_version"`
	Authority           string `json:"authority"`
	HumanReviewRequired bool   `json:"human_review_required"`
	ExecutionAuthorized bool   `json:"execution_authorized"`
	ProjectKind         string `json:"project_kind"`
	LanguageCount       int    `json:"language_count"`
	FrameworkCount      int    `json:"framework_count"`
	SourceRootCount     int    `json:"source_root_count"`
	ManifestCount       int    `json:"manifest_count"`
	ValidationCount     int    `json:"validation_count"`
	UIAssets            string `json:"ui_assets"`
	BrowserAutomation   string `json:"browser_automation"`
	Confidence          string `json:"confidence"`
}

func normalizeMancodeProjectProfile(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode project profile must be an object")
	}
	keys := []string{
		"version", "projectKind", "languages", "frameworks", "sourceRoots", "manifests",
		"availableValidation", "uiAssets", "browserAutomation", "confidence",
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) || object["version"] != "1.0" {
		return nil, true, errors.New("Mancode project profile does not match the supported schema")
	}
	projectKind := mancodeString(object["projectKind"])
	uiAssets := mancodeString(object["uiAssets"])
	browserAutomation := mancodeString(object["browserAutomation"])
	confidence := mancodeString(object["confidence"])
	if !mancodeHasString([]string{"backend", "web", "mobile", "desktop", "cli", "library", "data", "mixed", "unknown"}, projectKind) ||
		!mancodeHasString([]string{"none", "detected"}, uiAssets) ||
		!mancodeHasString([]string{"available", "unavailable", "unknown"}, browserAutomation) ||
		!mancodeHasString([]string{"high", "medium", "low"}, confidence) {
		return nil, true, errors.New("Mancode project profile contains an invalid categorical value")
	}
	languages, languagesOK := mancodeStringArrayCount(object["languages"], 128, 256)
	frameworks, frameworksOK := mancodeStringArrayCount(object["frameworks"], 256, 512)
	sourceRoots, rootsOK := mancodeStringArrayCount(object["sourceRoots"], 128, 256)
	manifests, manifestsOK := mancodeStringArrayCount(object["manifests"], 64, 256)
	validations, validationsOK := mancodeStringArrayCount(object["availableValidation"], 256, 512)
	if !languagesOK || !frameworksOK || !rootsOK || !manifestsOK || !validationsOK {
		return nil, true, errors.New("Mancode project profile contains an invalid or oversized value list")
	}
	payload, err := json.Marshal(mancodeProjectProfileImport{
		SchemaVersion: mancodeProjectProfileImportSchema, Authority: "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		ProjectKind: projectKind, LanguageCount: languages, FrameworkCount: frameworks,
		SourceRootCount: sourceRoots, ManifestCount: manifests, ValidationCount: validations,
		UIAssets: uiAssets, BrowserAutomation: browserAutomation, Confidence: confidence,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode project profile projection: %w", err)
	}
	return []normalizedItem{{Type: "mancode_project_profile", Payload: payload}}, true, nil
}

func normalizeMancodeProjectFact(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode project facts must be an object")
	}
	keys := []string{"schemaVersion", "revision", "trust", "profile", "uiLibrary", "detectedAt", "lastOperationId"}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode project facts do not match the supported schema")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	revision, revisionOK := mancodeIntegerValue(object["revision"])
	trust := mancodeString(object["trust"])
	detectedAt := mancodeString(object["detectedAt"])
	if !versionOK || version != 1 || !revisionOK || revision < 1 || trust != "detected" {
		return nil, true, errors.New("Mancode project facts contain an unsupported version, revision, or trust value")
	}
	if _, err := time.Parse(time.RFC3339Nano, detectedAt); err != nil {
		return nil, true, errors.New("Mancode project facts detection time is invalid")
	}
	if uiLibrary := object["uiLibrary"]; uiLibrary != nil {
		text, isString := uiLibrary.(string)
		if !isString || !mancodeBoundedString(text, 1, 256) || strings.TrimSpace(text) == "" {
			return nil, true, errors.New("Mancode project facts UI library value is invalid")
		}
	}
	if operationID := object["lastOperationId"]; operationID != nil {
		text, isString := operationID.(string)
		if !isString || !mancodeOperationIDPattern.MatchString(text) {
			return nil, true, errors.New("Mancode project facts operation reference is invalid")
		}
	}
	profileItems, _, err := normalizeMancodeProjectProfile(object["profile"])
	if err != nil {
		return nil, true, fmt.Errorf("Mancode project facts profile is invalid: %w", err)
	}
	var profile mancodeProjectProfileImport
	if err := json.Unmarshal(profileItems[0].Payload, &profile); err != nil {
		return nil, true, errors.New("Mancode project facts profile projection is invalid")
	}
	payload, err := json.Marshal(mancodeProjectFactImport{
		SchemaVersion: mancodeProjectFactImportSchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false, Revision: revision,
		LegacyTrust: trust, DetectedAt: detectedAt, ProjectKind: profile.ProjectKind,
		LanguageCount: profile.LanguageCount, FrameworkCount: profile.FrameworkCount,
		SourceRootCount: profile.SourceRootCount, ManifestCount: profile.ManifestCount,
		ValidationCount: profile.ValidationCount, UIAssets: profile.UIAssets,
		BrowserAutomation: profile.BrowserAutomation, Confidence: profile.Confidence,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode project facts projection: %w", err)
	}
	return []normalizedItem{{Type: "project_fact", Payload: payload}}, true, nil
}

func mancodeStringArrayCount(value any, maximumItems, maximumItemBytes int) (int, bool) {
	items, ok := value.([]any)
	if !ok || len(items) > maximumItems {
		return 0, false
	}
	for _, item := range items {
		text, isString := item.(string)
		if !isString || !mancodeBoundedString(text, 1, maximumItemBytes) || strings.TrimSpace(text) == "" {
			return 0, false
		}
	}
	return len(items), true
}

type mancodeLegacyConfigImport struct {
	SchemaVersion          string   `json:"schema_version"`
	Authority              string   `json:"authority"`
	HumanReviewRequired    bool     `json:"human_review_required"`
	ExecutionAuthorized    bool     `json:"execution_authorized"`
	Platforms              []string `json:"platforms"`
	ForceTeamMode          *bool    `json:"force_team_mode,omitempty"`
	TeamMode               *string  `json:"team_mode,omitempty"`
	DefaultStyleConfigured bool     `json:"default_style_configured"`
	MinimalPlatforms       []string `json:"minimal_platforms"`
}

func normalizeMancodeLegacyConfig(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode legacy config must be an object")
	}
	allowedKeys := []string{"platforms", "forceTeamMode", "teamMode", "defaultStyle", "platformOptions"}
	if !mancodeHasOnlyKeys(object, allowedKeys) {
		return nil, true, errors.New("Mancode legacy config contains unsupported fields")
	}
	platforms := []string{}
	if rawPlatforms, exists := object["platforms"]; exists {
		var platformsOK bool
		platforms, platformsOK = mancodeLegacyPlatforms(rawPlatforms)
		if !platformsOK {
			return nil, true, errors.New("Mancode legacy config platform list is invalid")
		}
	}
	var forceTeamMode *bool
	if rawForceTeamMode, exists := object["forceTeamMode"]; exists {
		value, valid := rawForceTeamMode.(bool)
		if !valid {
			return nil, true, errors.New("Mancode legacy config team-mode flag is invalid")
		}
		forceTeamMode = &value
	}
	var teamMode *string
	if rawTeamMode, exists := object["teamMode"]; exists {
		value, valid := rawTeamMode.(string)
		if !valid || !mancodeHasString([]string{"auto", "on", "off"}, value) {
			return nil, true, errors.New("Mancode legacy config team mode is invalid")
		}
		teamMode = &value
	}
	defaultStyleConfigured := false
	if rawStyle, exists := object["defaultStyle"]; exists && rawStyle != nil {
		style, valid := rawStyle.(string)
		if !valid || !mancodeBoundedString(style, 0, 512) {
			return nil, true, errors.New("Mancode legacy config default style is invalid")
		}
		defaultStyleConfigured = true
	}
	minimalPlatforms := []string{}
	if rawOptions, exists := object["platformOptions"]; exists {
		options, valid := rawOptions.(map[string]any)
		if !valid || len(options) > 8 {
			return nil, true, errors.New("Mancode legacy config platform options are invalid")
		}
		for platform, rawOption := range options {
			if !mancodeLegacyPlatform(platform) {
				return nil, true, errors.New("Mancode legacy config platform options are invalid")
			}
			option, isObject := rawOption.(map[string]any)
			if !isObject || !mancodeHasOnlyKeys(option, []string{"minimal"}) || !mancodeHasAllKeys(option, []string{"minimal"}) {
				return nil, true, errors.New("Mancode legacy config platform options are invalid")
			}
			minimal, isBool := option["minimal"].(bool)
			if !isBool {
				return nil, true, errors.New("Mancode legacy config platform options are invalid")
			}
			if minimal {
				minimalPlatforms = append(minimalPlatforms, platform)
			}
		}
	}
	sort.Strings(platforms)
	sort.Strings(minimalPlatforms)
	payload, err := json.Marshal(mancodeLegacyConfigImport{
		SchemaVersion: mancodeLegacyConfigImportSchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false, Platforms: platforms,
		ForceTeamMode: forceTeamMode, TeamMode: teamMode,
		DefaultStyleConfigured: defaultStyleConfigured, MinimalPlatforms: minimalPlatforms,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode legacy config projection: %w", err)
	}
	return []normalizedItem{{Type: "mancode_legacy_config", Payload: payload}}, true, nil
}

func mancodeLegacyPlatforms(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok || len(items) > 8 {
		return nil, false
	}
	seen := make(map[string]bool, len(items))
	platforms := make([]string, 0, len(items))
	for _, item := range items {
		platform, isString := item.(string)
		if !isString || !mancodeLegacyPlatform(platform) || seen[platform] {
			return nil, false
		}
		seen[platform] = true
		platforms = append(platforms, platform)
	}
	return platforms, true
}

func normalizeMancodeProjectConfig(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode project config must be an object")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	revision, revisionOK := mancodeIntegerValue(object["revision"])
	workspaceID, workspaceOK := object["workspaceId"].(string)
	updatedAt, updatedAtOK := object["updatedAt"].(string)
	transport, transportOK := object["transport"].(map[string]any)
	mode, modeOK := transport["mode"].(string)
	epoch, epochOK := mancodeIntegerValue(transport["epoch"])
	if !versionOK || version != 1 || !revisionOK || revision < 1 ||
		!workspaceOK || !mancodeOperationIDPattern.MatchString(workspaceID) || !updatedAtOK ||
		!transportOK || !modeOK || (mode != "local" && mode != "git-ref") || !epochOK || epoch < 0 {
		return nil, true, errors.New("Mancode project config does not match the supported schema")
	}
	if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, true, errors.New("Mancode project config update time is invalid")
	}
	payload, err := json.Marshal(mancodeProjectConfigImport{
		SchemaVersion: mancodeProjectConfigImportSchema, Revision: revision, WorkspaceID: workspaceID,
		TransportMode: mode, TransportEpoch: epoch, RemoteOmitted: true, UpdatedAt: updatedAt,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode project config: %w", err)
	}
	return []normalizedItem{{Type: "mancode_project_config", Payload: payload}}, true, nil
}

func mancodeGitRefAuthorityNeedsReconciliation(sourceRoot, relative string, value any) bool {
	path := mancodePath(relative, sourceRoot)
	if path != "/.mancode/shared/config.json" && path != "/shared/config.json" {
		return false
	}
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	transport, ok := object["transport"].(map[string]any)
	return ok && transport["mode"] == "git-ref"
}

func validMancodeOperationType(value string) bool {
	if len(value) < 1 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return true
}
