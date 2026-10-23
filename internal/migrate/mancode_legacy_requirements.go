package migrate

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

const mancodeLegacyRequirementsSchema = "threadforge.mancode-legacy-requirements.v1"

const (
	maxMancodeLegacyRequirementTextBytes = 32 << 10
	maxMancodeLegacyRequirementItems     = 128
	maxMancodeLegacyAcceptanceCriteria   = 256
	maxMancodeLegacyRequirementsPayload  = 1 << 20
)

var (
	mancodeLegacyAcceptanceID          = regexp.MustCompile(`^AC-[A-Z0-9][A-Z0-9-]{0,27}$`)
	mancodeLegacyRequirementDimensions = []string{
		"platform", "core_scope", "technical_stack", "data_and_persistence",
		"performance", "compatibility", "security",
	}
	mancodeLegacyAllowedVerificationSurfaces = []string{
		"unit", "component", "handler", "real_http", "browser", "device",
		"external_service", "manual_observation",
	}
)

type mancodeLegacyRequirementsProjection struct {
	SchemaVersion          string                          `json:"schema_version"`
	Authority              string                          `json:"authority"`
	HumanReviewRequired    bool                            `json:"human_review_required"`
	ExecutionAuthorized    bool                            `json:"execution_authorized"`
	TaskIDSHA256           string                          `json:"task_id_sha256"`
	Readiness              string                          `json:"readiness"`
	Requirements           mancodeLegacyRequirementsLedger `json:"requirements"`
	RawTaskIdentityOmitted bool                            `json:"raw_task_identity_omitted"`
	SourceAuthorityOmitted bool                            `json:"source_authority_omitted"`
}

type mancodeLegacyRequirementsLedger struct {
	Version            int64                              `json:"version"`
	Goal               string                             `json:"goal"`
	ConfirmedScope     []string                           `json:"confirmedScope"`
	ExcludedScope      []string                           `json:"excludedScope"`
	TechnicalDecisions []string                           `json:"technicalDecisions"`
	Defaults           []string                           `json:"defaults"`
	BlockingUnknowns   []string                           `json:"blockingUnknowns"`
	Coverage           []mancodeLegacyRequirementCoverage `json:"coverage"`
	AcceptanceCriteria []mancodeLegacyAcceptanceCriterion `json:"acceptanceCriteria"`
}

type mancodeLegacyRequirementCoverage struct {
	Dimension string `json:"dimension"`
	Status    string `json:"status"`
	Rationale string `json:"rationale"`
}

type mancodeLegacyAcceptanceCriterion struct {
	ID                   string                             `json:"id"`
	Description          string                             `json:"description"`
	Required             bool                               `json:"required"`
	Method               string                             `json:"method"`
	VerificationSurfaces *mancodeLegacyVerificationSurfaces `json:"verificationSurfaces,omitempty"`
}

type mancodeLegacyVerificationSurfaces struct {
	Automated string `json:"automated,omitempty"`
	Manual    string `json:"manual,omitempty"`
}

func isMancodeLegacyWorkflowMetadataPath(relative, sourceRoot string) (string, bool) {
	return mancodeLegacyWorkflowComponentPath(relative, sourceRoot, "metadata.json")
}

func isMancodeLegacyWorkflowRequirementsPath(relative, sourceRoot string) (string, bool) {
	return mancodeLegacyWorkflowComponentPath(relative, sourceRoot, "requirements.json")
}

func mancodeLegacyWorkflowComponentPath(relative, sourceRoot, filename string) (string, bool) {
	standard := filepath.ToSlash(strings.TrimPrefix(relative, "./"))
	if strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".mancode") &&
		!strings.HasPrefix(strings.ToLower(standard), ".mancode/") {
		standard = filepath.ToSlash(filepath.Join(".mancode", standard))
	}
	parts := strings.Split(strings.Trim(standard, "/"), "/")
	if len(parts) != 4 || !strings.EqualFold(parts[0], ".mancode") || !strings.EqualFold(parts[1], "workflows") ||
		!strings.EqualFold(parts[3], filename) || !mancodeLegacyWorkflowTaskID.MatchString(parts[2]) {
		return "", false
	}
	return parts[2], true
}

func mancodeLegacyRequirementsSourceAlias(relative string) string {
	return "mancode/legacy-requirements/" + digest([]byte(relative))
}

func normalizeMancodeLegacyRequirements(relative, sourceRoot string, value any) ([]normalizedItem, bool, error) {
	taskID, eligible := isMancodeLegacyWorkflowRequirementsPath(relative, sourceRoot)
	if !eligible {
		return nil, false, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode legacy requirements must be an object")
	}
	keys := []string{
		"version", "goal", "confirmedScope", "excludedScope", "technicalDecisions", "defaults",
		"blockingUnknowns", "coverage", "acceptanceCriteria",
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode legacy requirements do not match the supported schema")
	}
	version, validVersion := mancodeIntegerValue(object["version"])
	goal, validGoal := mancodeLegacyRequirementText(object["goal"], true)
	if !validVersion || version != 1 || !validGoal {
		return nil, true, errors.New("Mancode legacy requirements have invalid version or goal")
	}
	confirmed, okConfirmed := mancodeLegacyRequirementStrings(object["confirmedScope"])
	excluded, okExcluded := mancodeLegacyRequirementStrings(object["excludedScope"])
	decisions, okDecisions := mancodeLegacyRequirementStrings(object["technicalDecisions"])
	defaults, okDefaults := mancodeLegacyRequirementStrings(object["defaults"])
	unknowns, okUnknowns := mancodeLegacyRequirementStrings(object["blockingUnknowns"])
	if !okConfirmed || !okExcluded || !okDecisions || !okDefaults || !okUnknowns || len(confirmed) == 0 {
		return nil, true, errors.New("Mancode legacy requirements have invalid or incomplete scope fields")
	}

	coverageValues, okCoverage := object["coverage"].([]any)
	if !okCoverage || len(coverageValues) != len(mancodeLegacyRequirementDimensions) {
		return nil, true, errors.New("Mancode legacy requirements coverage is incomplete")
	}
	coverage := make([]mancodeLegacyRequirementCoverage, 0, len(coverageValues))
	seenDimensions := make(map[string]bool, len(coverageValues))
	technicalStackApplicable := true
	for _, raw := range coverageValues {
		item, ok := raw.(map[string]any)
		if !ok || !mancodeHasAllKeys(item, []string{"dimension", "status", "rationale"}) ||
			!mancodeHasOnlyKeys(item, []string{"dimension", "status", "rationale"}) {
			return nil, true, errors.New("Mancode legacy requirements contain invalid coverage")
		}
		dimension, okDimension := item["dimension"].(string)
		status, okStatus := item["status"].(string)
		rationale, okRationale := mancodeLegacyRequirementText(item["rationale"], true)
		if !okDimension || !containsString(mancodeLegacyRequirementDimensions, dimension) || seenDimensions[dimension] ||
			!okStatus || !containsString([]string{"confirmed", "defaulted", "not_applicable"}, status) || !okRationale {
			return nil, true, errors.New("Mancode legacy requirements contain invalid coverage")
		}
		seenDimensions[dimension] = true
		if dimension == "technical_stack" && status == "not_applicable" {
			technicalStackApplicable = false
		}
		coverage = append(coverage, mancodeLegacyRequirementCoverage{
			Dimension: dimension, Status: status, Rationale: rationale,
		})
	}
	if technicalStackApplicable && len(decisions) == 0 {
		return nil, true, errors.New("Mancode legacy requirements need technical decisions when technical_stack applies")
	}

	criteriaValues, okCriteria := object["acceptanceCriteria"].([]any)
	if !okCriteria || len(criteriaValues) == 0 || len(criteriaValues) > maxMancodeLegacyAcceptanceCriteria {
		return nil, true, errors.New("Mancode legacy requirements need a bounded acceptance-criteria list")
	}
	criteria := make([]mancodeLegacyAcceptanceCriterion, 0, len(criteriaValues))
	seenIDs := make(map[string]bool, len(criteriaValues))
	hasRequiredCriterion := false
	for _, raw := range criteriaValues {
		item, ok := raw.(map[string]any)
		if !ok || !mancodeHasAllKeys(item, []string{"id", "description", "required", "method"}) ||
			!mancodeHasOnlyKeys(item, []string{"id", "description", "required", "method", "verificationSurfaces"}) {
			return nil, true, errors.New("Mancode legacy requirements contain an invalid acceptance criterion")
		}
		id, okID := item["id"].(string)
		description, okDescription := mancodeLegacyRequirementText(item["description"], true)
		required, okRequired := item["required"].(bool)
		method, okMethod := item["method"].(string)
		if !okID || !mancodeLegacyAcceptanceID.MatchString(id) || seenIDs[id] || !okDescription || !okRequired ||
			!okMethod || !containsString([]string{"automated", "manual", "hybrid"}, method) {
			return nil, true, errors.New("Mancode legacy requirements contain an invalid acceptance criterion")
		}
		seenIDs[id] = true
		surfaces, okSurfaces := mancodeLegacyParseVerificationSurfaces(item, method)
		if !okSurfaces {
			return nil, true, errors.New("Mancode legacy requirements contain invalid verification surfaces")
		}
		criteria = append(criteria, mancodeLegacyAcceptanceCriterion{
			ID: id, Description: description, Required: required, Method: method, VerificationSurfaces: surfaces,
		})
		hasRequiredCriterion = hasRequiredCriterion || required
	}
	if !hasRequiredCriterion {
		return nil, true, errors.New("Mancode legacy requirements need at least one required acceptance criterion")
	}

	ledger := mancodeLegacyRequirementsLedger{
		Version: version, Goal: goal, ConfirmedScope: confirmed, ExcludedScope: excluded,
		TechnicalDecisions: decisions, Defaults: defaults, BlockingUnknowns: unknowns,
		Coverage: coverage, AcceptanceCriteria: criteria,
	}
	readiness := "ready"
	if len(unknowns) > 0 {
		readiness = "needs_clarification"
	}
	projection := mancodeLegacyRequirementsProjection{
		SchemaVersion: mancodeLegacyRequirementsSchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
		TaskIDSHA256: mancodeCoordinationIdentityHash("legacy-requirements-task-id", taskID),
		Readiness:    readiness, Requirements: ledger,
		RawTaskIdentityOmitted: true, SourceAuthorityOmitted: true,
	}
	payload, err := json.Marshal(projection)
	if err != nil || len(payload) > maxMancodeLegacyRequirementsPayload {
		return nil, true, errors.New("Mancode legacy requirements exceed the supported payload limit")
	}
	return []normalizedItem{{Type: "mancode_legacy_requirements", Payload: payload}}, true, nil
}

func mancodeLegacyRequirementText(value any, nonEmpty bool) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if !mancodeBoundedString(text, 0, maxMancodeLegacyRequirementTextBytes) || (nonEmpty && text == "") {
		return "", false
	}
	return text, true
}

func mancodeLegacyRequirementStrings(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok || len(items) > maxMancodeLegacyRequirementItems {
		return nil, false
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, valid := mancodeLegacyRequirementText(item, true)
		if !valid {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

func mancodeLegacyParseVerificationSurfaces(
	item map[string]any,
	method string,
) (*mancodeLegacyVerificationSurfaces, bool) {
	raw, exists := item["verificationSurfaces"]
	if !exists {
		return nil, true
	}
	value, ok := raw.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(value, []string{"automated", "manual"}) {
		return nil, false
	}
	automated, hasAutomated, validAutomated := mancodeLegacyVerificationSurface(value, "automated")
	manual, hasManual, validManual := mancodeLegacyVerificationSurface(value, "manual")
	if !validAutomated || !validManual ||
		(method == "automated" && (!hasAutomated || hasManual)) ||
		(method == "manual" && (hasAutomated || !hasManual)) ||
		(method == "hybrid" && (!hasAutomated || !hasManual)) {
		return nil, false
	}
	return &mancodeLegacyVerificationSurfaces{Automated: automated, Manual: manual}, true
}

func mancodeLegacyVerificationSurface(value map[string]any, key string) (string, bool, bool) {
	raw, exists := value[key]
	if !exists {
		return "", false, true
	}
	surface, ok := raw.(string)
	if !ok || !containsString(mancodeLegacyAllowedVerificationSurfaces, surface) {
		return "", true, false
	}
	return surface, true, true
}
