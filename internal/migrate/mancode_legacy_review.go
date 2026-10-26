package migrate

import (
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"strings"
	"time"
)

const mancodeLegacyReviewSchema = "threadforge.mancode-legacy-review.v1"

const (
	maxMancodeLegacyReviewTextBytes = 32 << 10
	maxMancodeLegacyReviewBlockers  = 128
)

var (
	mancodeLegacyReviewBlockerID = regexp.MustCompile(`^[A-Z][A-Z0-9-]{0,31}$`)
)

type mancodeLegacyReviewProjection struct {
	SchemaVersion          string                      `json:"schema_version"`
	Authority              string                      `json:"authority"`
	HumanReviewRequired    bool                        `json:"human_review_required"`
	ExecutionAuthorized    bool                        `json:"execution_authorized"`
	TaskIDSHA256           string                      `json:"task_id_sha256"`
	Depth                  string                      `json:"depth"`
	Domains                []mancodeLegacyReviewDomain `json:"domains"`
	RemediationRounds      int64                       `json:"remediation_rounds"`
	Skipped                bool                        `json:"skipped"`
	SkipRecordedAt         string                      `json:"skip_recorded_at,omitempty"`
	SkipReasonPresent      bool                        `json:"skip_reason_present"`
	BlockerIDsOmitted      bool                        `json:"blocker_ids_omitted"`
	ReportPathsOmitted     bool                        `json:"report_paths_omitted"`
	RawTaskIdentityOmitted bool                        `json:"raw_task_identity_omitted"`
	SourceAuthorityOmitted bool                        `json:"source_authority_omitted"`
}

type mancodeLegacyReviewDomain struct {
	Domain               string `json:"domain"`
	Required             bool   `json:"required"`
	Completed            bool   `json:"completed"`
	ReportPresent        bool   `json:"report_present"`
	OpenBlockerCount     int    `json:"open_blocker_count"`
	ResolvedBlockerCount int    `json:"resolved_blocker_count"`
}

func isMancodeLegacyWorkflowReviewPath(relative, sourceRoot string) (string, bool) {
	return mancodeLegacyWorkflowComponentPath(relative, sourceRoot, "review-ledger.json")
}

func mancodeLegacyReviewSourceAlias(relative string) string {
	return "mancode/legacy-reviews/" + digest([]byte(relative))
}

func normalizeMancodeLegacyReview(relative, sourceRoot string, value any) ([]normalizedItem, bool, error) {
	taskID, eligible := isMancodeLegacyWorkflowReviewPath(relative, sourceRoot)
	if !eligible {
		return nil, false, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode legacy review ledger must be an object")
	}
	keys := []string{"version", "depth", "requiredDomains", "completedDomains", "reports", "blockers", "remediationRounds", "skipped"}
	if !mancodeHasAllKeys(object, keys[:len(keys)-1]) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode legacy review ledger does not match the supported schema")
	}
	version, versionOK := object["version"].(string)
	depth, depthOK := object["depth"].(string)
	if !versionOK || version != "1.0" || !depthOK || !containsString([]string{"targeted", "full"}, depth) {
		return nil, true, errors.New("Mancode legacy review ledger has an unsupported version or depth")
	}
	required, requiredOK := mancodeLegacyReviewDomains(object["requiredDomains"])
	completed, completedOK := mancodeLegacyReviewDomains(object["completedDomains"])
	if !requiredOK || !completedOK {
		return nil, true, errors.New("Mancode legacy review ledger domain lists are invalid")
	}
	remediationRounds, roundsOK := mancodeIntegerValue(object["remediationRounds"])
	if !roundsOK || remediationRounds < 0 || remediationRounds > 1 {
		return nil, true, errors.New("Mancode legacy review remediation round is invalid")
	}

	reports, reportsOK := object["reports"].(map[string]any)
	if !reportsOK || len(reports) > 2 {
		return nil, true, errors.New("Mancode legacy review reports are invalid")
	}
	reportPresence := make(map[string]bool, len(reports))
	for domain, rawPath := range reports {
		reportPath, valid := rawPath.(string)
		if !containsString([]string{"quality", "security"}, domain) || !containsString(required, domain) ||
			!valid || !mancodeLegacyReviewReportPath(reportPath) {
			return nil, true, errors.New("Mancode legacy review report reference is invalid")
		}
		reportPresence[domain] = true
	}
	for _, domain := range completed {
		if !reportPresence[domain] {
			return nil, true, errors.New("completed Mancode legacy review domains require a report")
		}
	}

	blockerValues, blockersOK := object["blockers"].([]any)
	if !blockersOK || len(blockerValues) > maxMancodeLegacyReviewBlockers {
		return nil, true, errors.New("Mancode legacy review blocker list is invalid")
	}
	openBlockers := make(map[string]int)
	resolvedBlockers := make(map[string]int)
	seenBlockers := make(map[string]bool, len(blockerValues))
	for _, raw := range blockerValues {
		blocker, valid := raw.(map[string]any)
		if !valid || !mancodeHasAllKeys(blocker, []string{"id", "domain", "status"}) ||
			!mancodeHasOnlyKeys(blocker, []string{"id", "domain", "status"}) {
			return nil, true, errors.New("Mancode legacy review blocker is invalid")
		}
		id, idOK := blocker["id"].(string)
		domain, domainOK := blocker["domain"].(string)
		status, statusOK := blocker["status"].(string)
		if !idOK || !mancodeLegacyReviewBlockerID.MatchString(id) || seenBlockers[id] || !domainOK ||
			!containsString(required, domain) || !containsString(completed, domain) || !statusOK ||
			!containsString([]string{"open", "resolved"}, status) {
			return nil, true, errors.New("Mancode legacy review blocker has invalid identity, domain, or status")
		}
		seenBlockers[id] = true
		if status == "open" {
			openBlockers[domain]++
		} else {
			resolvedBlockers[domain]++
		}
	}

	skipped := false
	skipReasonPresent := false
	skipRecordedAt := ""
	if rawSkip, exists := object["skipped"]; exists {
		skip, valid := rawSkip.(map[string]any)
		if !valid || !mancodeHasAllKeys(skip, []string{"reason", "recordedAt"}) ||
			!mancodeHasOnlyKeys(skip, []string{"reason", "recordedAt"}) {
			return nil, true, errors.New("Mancode legacy skipped-review record is invalid")
		}
		_, reasonOK := mancodeLegacyReviewText(skip["reason"])
		recordedAt, timeOK := skip["recordedAt"].(string)
		parsedTime, parseErr := time.Parse(time.RFC3339Nano, recordedAt)
		if !reasonOK || !timeOK || parseErr != nil || depth != "targeted" || len(required) != 0 || len(completed) != 0 ||
			len(reports) != 0 || len(blockerValues) != 0 || remediationRounds != 0 {
			return nil, true, errors.New("Mancode skipped-review state is inconsistent")
		}
		skipped = true
		skipReasonPresent = true
		skipRecordedAt = parsedTime.UTC().Format(time.RFC3339Nano)
	}
	if !skipped && ((depth == "targeted" && len(required) != 1) ||
		(depth == "full" && (len(required) != 2 || !containsString(required, "quality") || !containsString(required, "security")))) {
		return nil, true, errors.New("Mancode legacy review required domains do not match the declared depth")
	}
	if !skipped {
		for _, domain := range completed {
			if !containsString(required, domain) {
				return nil, true, errors.New("Mancode legacy review completed a domain that was not required")
			}
		}
	}

	domains := make([]mancodeLegacyReviewDomain, 0, len(required))
	for _, domain := range required {
		domains = append(domains, mancodeLegacyReviewDomain{
			Domain: domain, Required: true, Completed: containsString(completed, domain),
			ReportPresent: reportPresence[domain], OpenBlockerCount: openBlockers[domain],
			ResolvedBlockerCount: resolvedBlockers[domain],
		})
	}
	projection := mancodeLegacyReviewProjection{
		SchemaVersion: mancodeLegacyReviewSchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
		TaskIDSHA256: mancodeCoordinationIdentityHash("legacy-review-task-id", taskID),
		Depth:        depth, Domains: domains, RemediationRounds: remediationRounds, Skipped: skipped,
		SkipRecordedAt: skipRecordedAt, SkipReasonPresent: skipReasonPresent,
		BlockerIDsOmitted: true, ReportPathsOmitted: true, RawTaskIdentityOmitted: true,
		SourceAuthorityOmitted: true,
	}
	payload, err := json.Marshal(projection)
	if err != nil {
		return nil, true, errors.New("unable to serialize Mancode legacy review summary")
	}
	return []normalizedItem{{Type: "mancode_legacy_review_summary", Payload: payload}}, true, nil
}

func mancodeLegacyReviewDomains(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok || len(items) > 2 {
		return nil, false
	}
	result := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		domain, valid := item.(string)
		if !valid || !containsString([]string{"quality", "security"}, domain) || seen[domain] {
			return nil, false
		}
		seen[domain] = true
		result = append(result, domain)
	}
	return result, true
}

func mancodeLegacyReviewReportPath(value string) bool {
	normalized := strings.ReplaceAll(value, "\\", "/")
	return mancodeLegacySafeArtifactPath(value, 512) && strings.EqualFold(path.Ext(normalized), ".md")
}

func mancodeLegacyReviewText(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, mancodeBoundedString(text, 1, maxMancodeLegacyReviewTextBytes)
}
