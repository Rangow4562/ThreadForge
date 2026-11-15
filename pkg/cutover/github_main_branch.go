package cutover

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const GitHubMainBranchGovernanceSchemaVersion = "threadforge.github-main-branch-governance.v1"

const GitHubMainBranchRulesEndpointTemplate = "/repos/%s/rules/branches/main"

const (
	maxGitHubBranchGovernanceBytes = 1 << 20
	maxGitHubBranchGovernanceAge   = 24 * time.Hour
)

var githubRepositorySlugRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)

// GitHubRepositorySlugValid reports whether value is a bounded owner/name
// repository slug suitable for the fixed github.com REST API endpoints.
func GitHubRepositorySlugValid(value string) bool {
	return githubRepositorySlugRE.MatchString(value) && !strings.Contains(value, "..")
}

// GitHubMainBranchGovernance is a path-free, read-only snapshot of the rules
// GitHub reports as effective for the repository's main branch. The artifact
// is evidence for the signed security-review approval, not a GitHub signature.
type GitHubMainBranchGovernance struct {
	SchemaVersion string                       `json:"schema_version"`
	Repository    string                       `json:"repository"`
	Branch        string                       `json:"branch"`
	APIEndpoint   string                       `json:"api_endpoint"`
	ObservedAt    string                       `json:"observed_at"`
	Rules         []map[string]json.RawMessage `json:"rules"`
}

// DecodeGitHubMainBranchGovernance strictly decodes a captured GitHub rules
// response envelope while allowing forward-compatible fields inside API rule
// objects. Duplicate keys remain rejected at every nesting level.
func DecodeGitHubMainBranchGovernance(data []byte) (GitHubMainBranchGovernance, error) {
	var result GitHubMainBranchGovernance
	if len(data) > maxGitHubBranchGovernanceBytes {
		return result, errors.New("GitHub main-branch governance artifact exceeds 1 MiB")
	}
	if err := decodeUniqueJSON(data, &result); err != nil {
		return result, err
	}
	return result, nil
}

// ValidateGitHubMainBranchGovernanceEnvelope validates the artifact identity
// and shape without requiring protective rules to be present. Capture must be
// able to emit an honest empty-rules artifact so the assessor can report the
// actual blocker instead of suppressing evidence.
func ValidateGitHubMainBranchGovernanceEnvelope(report GitHubMainBranchGovernance) error {
	if report.SchemaVersion != GitHubMainBranchGovernanceSchemaVersion {
		return errors.New("unsupported GitHub main-branch governance schema")
	}
	if !GitHubRepositorySlugValid(report.Repository) {
		return errors.New("GitHub main-branch governance repository must be owner/name")
	}
	if report.Branch != "main" || report.APIEndpoint != fmt.Sprintf(GitHubMainBranchRulesEndpointTemplate, report.Repository) {
		return errors.New("GitHub main-branch governance must bind to the main branch rules endpoint")
	}
	if _, err := time.Parse(time.RFC3339Nano, report.ObservedAt); err != nil {
		return errors.New("GitHub main-branch governance requires an RFC3339 observed_at")
	}
	if len(report.Rules) > 256 {
		return errors.New("GitHub main-branch governance contains too many rules")
	}
	for index, rule := range report.Rules {
		var ruleType string
		if raw, ok := rule["type"]; !ok || json.Unmarshal(raw, &ruleType) != nil || strings.TrimSpace(ruleType) == "" {
			return fmt.Errorf("GitHub main-branch rule %d is missing a valid type", index)
		}
	}
	return nil
}

// ValidateGitHubMainBranchGovernance checks freshness and the minimum
// repository safeguards required for cutover: main changes through pull
// requests, at least one strict required status check, no branch deletion, and
// no non-fast-forward updates. It deliberately does not prescribe the review
// count; that remains a repository-owner policy choice.
func ValidateGitHubMainBranchGovernance(report GitHubMainBranchGovernance, now time.Time) error {
	if err := ValidateGitHubMainBranchGovernanceEnvelope(report); err != nil {
		return err
	}
	observedAt, err := time.Parse(time.RFC3339Nano, report.ObservedAt)
	if err != nil {
		return err
	}
	if observedAt.After(now.Add(5 * time.Minute)) {
		return errors.New("GitHub main-branch governance observation is from the future")
	}
	if now.Sub(observedAt) > maxGitHubBranchGovernanceAge {
		return errors.New("GitHub main-branch governance observation is older than 24 hours")
	}
	var hasPullRequest, hasDeletion, hasNonFastForward, hasStrictChecks bool
	for index, rule := range report.Rules {
		var ruleType string
		_ = json.Unmarshal(rule["type"], &ruleType)
		switch ruleType {
		case "pull_request":
			hasPullRequest = true
		case "deletion":
			hasDeletion = true
		case "non_fast_forward":
			hasNonFastForward = true
		case "required_status_checks":
			valid, err := hasStrictRequiredStatusChecks(rule["parameters"])
			if err != nil {
				return fmt.Errorf("GitHub main-branch required status-check rule %d: %w", index, err)
			}
			hasStrictChecks = hasStrictChecks || valid
		}
	}
	missing := make([]string, 0, 4)
	if !hasPullRequest {
		missing = append(missing, "pull-request-only changes")
	}
	if !hasStrictChecks {
		missing = append(missing, "at least one strict required status check")
	}
	if !hasDeletion {
		missing = append(missing, "branch-deletion protection")
	}
	if !hasNonFastForward {
		missing = append(missing, "non-fast-forward protection")
	}
	if len(missing) > 0 {
		return fmt.Errorf("GitHub main branch is missing required governance rules: %s", strings.Join(missing, ", "))
	}
	return nil
}

func hasStrictRequiredStatusChecks(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, errors.New("parameters are missing")
	}
	var parameters map[string]json.RawMessage
	if err := decodeUniqueJSON(raw, &parameters); err != nil {
		return false, errors.New("parameters are invalid")
	}
	var strict bool
	if err := json.Unmarshal(parameters["strict_required_status_checks_policy"], &strict); err != nil || !strict {
		return false, errors.New("strict_required_status_checks_policy must be true")
	}
	var checks []map[string]json.RawMessage
	if err := json.Unmarshal(parameters["required_status_checks"], &checks); err != nil || len(checks) == 0 {
		return false, errors.New("at least one required status check is required")
	}
	for index, check := range checks {
		var context string
		if err := json.Unmarshal(check["context"], &context); err != nil || strings.TrimSpace(context) == "" {
			return false, fmt.Errorf("required status check %d has no context", index)
		}
	}
	return true, nil
}
