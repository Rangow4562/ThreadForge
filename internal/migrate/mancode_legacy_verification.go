package migrate

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

const mancodeLegacyVerificationSchema = "threadforge.mancode-legacy-verification.v1"

const (
	maxMancodeLegacyVerificationChecks  = 256
	maxMancodeLegacyVerificationText    = 32 << 10
	maxMancodeLegacyVerificationCommand = 4 << 10
	maxMancodeLegacyEvidenceFilePath    = 1 << 10
)

var mancodeLegacyArtifactPathScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

type mancodeLegacyVerificationProjection struct {
	SchemaVersion                string                           `json:"schema_version"`
	Authority                    string                           `json:"authority"`
	HumanReviewRequired          bool                             `json:"human_review_required"`
	ExecutionAuthorized          bool                             `json:"execution_authorized"`
	TaskIDSHA256                 string                           `json:"task_id_sha256"`
	PlanVersion                  int64                            `json:"source_plan_version"`
	RequirementsDigestPresent    bool                             `json:"requirements_digest_present"`
	RequirementsBindingVerified  bool                             `json:"requirements_binding_verified"`
	PlanBindingVerified          bool                             `json:"plan_binding_verified"`
	RemediationRound             int64                            `json:"remediation_round"`
	SourceStatus                 string                           `json:"source_status"`
	Checks                       []mancodeLegacyVerificationCheck `json:"checks"`
	RawTaskIdentityOmitted       bool                             `json:"raw_task_identity_omitted"`
	RawEvidenceOmitted           bool                             `json:"raw_evidence_omitted"`
	RawCommandsOmitted           bool                             `json:"raw_commands_omitted"`
	RawEvidencePathsOmitted      bool                             `json:"raw_evidence_paths_omitted"`
	RawTimestampsOmitted         bool                             `json:"raw_timestamps_omitted"`
	RawRequirementsDigestOmitted bool                             `json:"raw_requirements_digest_omitted"`
	SourceAuthorityOmitted       bool                             `json:"source_authority_omitted"`
}

type mancodeLegacyVerificationCheck struct {
	AcceptanceID string                             `json:"acceptance_id"`
	Required     bool                               `json:"required"`
	Automated    *mancodeLegacyVerificationEvidence `json:"automated,omitempty"`
	Manual       *mancodeLegacyVerificationEvidence `json:"manual,omitempty"`
}

type mancodeLegacyVerificationEvidence struct {
	Status              string `json:"status"`
	EvidencePresent     bool   `json:"evidence_present"`
	UpdatedAtPresent    bool   `json:"updated_at_present"`
	CommandPresent      bool   `json:"command_present"`
	ExitCode            *int64 `json:"exit_code,omitempty"`
	EvidenceFilePresent bool   `json:"evidence_file_present"`
}

func isMancodeLegacyWorkflowVerificationPath(relative, sourceRoot string) (string, bool) {
	return mancodeLegacyWorkflowComponentPath(relative, sourceRoot, "verification-ledger.json")
}

func mancodeLegacyVerificationSourceAlias(relative string) string {
	return "mancode/legacy-verification/" + digest([]byte(relative))
}

func normalizeMancodeLegacyVerification(relative, sourceRoot string, value any) ([]normalizedItem, bool, error) {
	taskID, eligible := isMancodeLegacyWorkflowVerificationPath(relative, sourceRoot)
	if !eligible {
		return nil, false, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode legacy verification ledger must be an object")
	}
	keys := []string{"version", "planVersion", "requirementsDigest", "remediationRound", "status", "checks"}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode legacy verification ledger does not match the supported schema")
	}
	version, versionOK := mancodeIntegerValue(object["version"])
	planVersion, planVersionOK := mancodeIntegerValue(object["planVersion"])
	remediationRound, roundOK := mancodeIntegerValue(object["remediationRound"])
	requirementsDigest, digestOK := object["requirementsDigest"].(string)
	status, statusOK := object["status"].(string)
	if !versionOK || version != 1 || !planVersionOK || planVersion < 1 || !roundOK || remediationRound < 0 ||
		!digestOK || !validDigest(requirementsDigest) || !statusOK ||
		!containsString([]string{"pending", "passed", "failed", "manual_required", "blocked"}, status) {
		return nil, true, errors.New("Mancode legacy verification ledger has invalid binding or status fields")
	}
	rawChecks, checksOK := object["checks"].([]any)
	if !checksOK || len(rawChecks) == 0 || len(rawChecks) > maxMancodeLegacyVerificationChecks {
		return nil, true, errors.New("Mancode legacy verification check list is empty or exceeds the supported limit")
	}
	checks := make([]mancodeLegacyVerificationCheck, 0, len(rawChecks))
	seenIDs := make(map[string]bool, len(rawChecks))
	for _, rawCheck := range rawChecks {
		check, valid := rawCheck.(map[string]any)
		if !valid || !mancodeHasAllKeys(check, []string{"acceptanceId", "required"}) ||
			!mancodeHasOnlyKeys(check, []string{"acceptanceId", "required", "automated", "manual"}) {
			return nil, true, errors.New("Mancode legacy verification check has an invalid shape")
		}
		acceptanceID, idOK := check["acceptanceId"].(string)
		required, requiredOK := check["required"].(bool)
		if !idOK || !mancodeLegacyAcceptanceID.MatchString(acceptanceID) || seenIDs[acceptanceID] || !requiredOK {
			return nil, true, errors.New("Mancode legacy verification check has an invalid or duplicate acceptance ID")
		}
		seenIDs[acceptanceID] = true
		automated, automatedOK := parseMancodeLegacyVerificationEvidence(check, "automated")
		manual, manualOK := parseMancodeLegacyVerificationEvidence(check, "manual")
		if !automatedOK || !manualOK || (automated == nil && manual == nil) {
			return nil, true, errors.New("Mancode legacy verification check has invalid evidence slots")
		}
		checks = append(checks, mancodeLegacyVerificationCheck{
			AcceptanceID: acceptanceID, Required: required, Automated: automated, Manual: manual,
		})
	}
	if want := mancodeLegacyDerivedVerificationStatus(checks); status != want {
		return nil, true, errors.New("Mancode legacy verification status does not match required-check evidence")
	}
	projection := mancodeLegacyVerificationProjection{
		SchemaVersion: mancodeLegacyVerificationSchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
		TaskIDSHA256: mancodeCoordinationIdentityHash("legacy-verification-task-id", taskID),
		PlanVersion:  planVersion, RequirementsDigestPresent: true,
		RequirementsBindingVerified: false, PlanBindingVerified: false,
		RemediationRound: remediationRound, SourceStatus: status, Checks: checks,
		RawTaskIdentityOmitted: true, RawEvidenceOmitted: true, RawCommandsOmitted: true,
		RawEvidencePathsOmitted: true, RawTimestampsOmitted: true,
		RawRequirementsDigestOmitted: true, SourceAuthorityOmitted: true,
	}
	payload, err := json.Marshal(projection)
	if err != nil || len(payload) > maxMancodeLegacyRequirementsPayload {
		return nil, true, errors.New("Mancode legacy verification projection exceeds the supported payload limit")
	}
	return []normalizedItem{{Type: "mancode_legacy_verification_summary", Payload: payload}}, true, nil
}

func parseMancodeLegacyVerificationEvidence(check map[string]any, component string) (*mancodeLegacyVerificationEvidence, bool) {
	raw, exists := check[component]
	if !exists {
		return nil, true
	}
	evidence, ok := raw.(map[string]any)
	if !ok || !mancodeHasAllKeys(evidence, []string{"status"}) ||
		!mancodeHasOnlyKeys(evidence, []string{"status", "evidence", "updatedAt", "command", "exitCode", "evidenceFile"}) {
		return nil, false
	}
	status, statusOK := evidence["status"].(string)
	if !statusOK || !containsString([]string{"pending", "passed", "failed", "manual_required", "blocked"}, status) ||
		(component == "automated" && status == "manual_required") {
		return nil, false
	}
	if status == "pending" {
		for _, key := range []string{"evidence", "updatedAt", "command", "exitCode", "evidenceFile"} {
			if _, exists := evidence[key]; exists {
				return nil, false
			}
		}
		return &mancodeLegacyVerificationEvidence{Status: status}, true
	}
	text, textOK := evidence["evidence"].(string)
	updatedAt, updatedAtOK := evidence["updatedAt"].(string)
	if _, parseErr := time.Parse(time.RFC3339Nano, updatedAt); !textOK || strings.TrimSpace(text) == "" ||
		!mancodeBoundedString(text, 1, maxMancodeLegacyVerificationText) || !updatedAtOK || parseErr != nil {
		return nil, false
	}
	commandPresent := false
	if rawCommand, exists := evidence["command"]; exists {
		command, valid := rawCommand.(string)
		if !valid || !mancodeBoundedString(strings.TrimSpace(command), 1, maxMancodeLegacyVerificationCommand) {
			return nil, false
		}
		commandPresent = true
	}
	var exitCode *int64
	if rawExitCode, exists := evidence["exitCode"]; exists {
		parsed, valid := mancodeIntegerValue(rawExitCode)
		if !valid {
			return nil, false
		}
		exitCode = &parsed
	}
	if component == "automated" && (status == "passed" || status == "failed") {
		if !commandPresent || exitCode == nil || (status == "passed" && *exitCode != 0) || (status == "failed" && *exitCode == 0) {
			return nil, false
		}
	}
	evidenceFilePresent := false
	if rawPath, exists := evidence["evidenceFile"]; exists {
		filePath, valid := rawPath.(string)
		if !valid || !mancodeLegacySafeArtifactPath(filePath, maxMancodeLegacyEvidenceFilePath) {
			return nil, false
		}
		evidenceFilePresent = true
	}
	return &mancodeLegacyVerificationEvidence{
		Status: status, EvidencePresent: true, UpdatedAtPresent: true,
		CommandPresent: commandPresent, ExitCode: exitCode, EvidenceFilePresent: evidenceFilePresent,
	}, true
}

// Mancode artifact references are repository-relative paths. Match its
// assertSafeSharedRelativePath contract while retaining no raw path in the
// imported projection.
func mancodeLegacySafeArtifactPath(value string, maxBytes int) bool {
	if !mancodeBoundedString(value, 1, maxBytes) || strings.TrimSpace(value) == "" || strings.ContainsRune(value, '\x00') {
		return false
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(normalized, "/") || mancodeLegacyArtifactPathScheme.MatchString(value) {
		return false
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func mancodeLegacyDerivedVerificationStatus(checks []mancodeLegacyVerificationCheck) string {
	var statuses []string
	for _, check := range checks {
		if !check.Required {
			continue
		}
		if check.Automated != nil {
			statuses = append(statuses, check.Automated.Status)
		}
		if check.Manual != nil {
			statuses = append(statuses, check.Manual.Status)
		}
	}
	for _, status := range statuses {
		if status == "blocked" {
			return "blocked"
		}
	}
	for _, status := range statuses {
		if status == "manual_required" {
			return "manual_required"
		}
	}
	for _, status := range statuses {
		if status == "failed" {
			return "failed"
		}
	}
	for _, status := range statuses {
		if status != "passed" {
			return "pending"
		}
	}
	return "passed"
}
