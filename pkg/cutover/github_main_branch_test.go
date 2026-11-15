package cutover

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validGitHubMainBranchGovernance(now time.Time) GitHubMainBranchGovernance {
	repository := "threadforge-fixture/Threadforge"
	return GitHubMainBranchGovernance{
		SchemaVersion: GitHubMainBranchGovernanceSchemaVersion,
		Repository:    repository,
		Branch:        "main",
		APIEndpoint:   "/repos/" + repository + "/rules/branches/main",
		ObservedAt:    now.UTC().Format(time.RFC3339Nano),
		Rules: []map[string]json.RawMessage{
			{"type": json.RawMessage(`"pull_request"`)},
			{"type": json.RawMessage(`"deletion"`)},
			{"type": json.RawMessage(`"non_fast_forward"`)},
			{
				"type":       json.RawMessage(`"required_status_checks"`),
				"parameters": json.RawMessage(`{"strict_required_status_checks_policy":true,"required_status_checks":[{"context":"Required CI gate","integration_id":null}]}`),
			},
		},
	}
}

func writeMainBranchGovernanceFixture(t *testing.T, root string, report *Report) Evidence {
	t.Helper()
	content, err := json.Marshal(validGitHubMainBranchGovernance(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	path := "github-main-branch-governance.json"
	if err := os.WriteFile(filepath.Join(root, path), content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	evidence := Evidence{Path: path, SHA256: hex.EncodeToString(digest[:])}
	for index := range report.Approvals {
		if report.Approvals[index].Role == "security-reviewer" {
			report.Approvals[index].Evidence = append(report.Approvals[index].Evidence, evidence)
		}
	}
	return evidence
}

func TestValidateGitHubMainBranchGovernanceRequiresFreshEffectiveRules(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	valid := validGitHubMainBranchGovernance(now)
	if err := ValidateGitHubMainBranchGovernance(valid, now); err != nil {
		t.Fatalf("valid governance report rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*GitHubMainBranchGovernance)
		want   string
	}{
		{"empty rules", func(value *GitHubMainBranchGovernance) { value.Rules = nil }, "pull-request-only changes"},
		{"missing CI checks", func(value *GitHubMainBranchGovernance) { value.Rules = value.Rules[:3] }, "strict required status check"},
		{"missing force-push protection", func(value *GitHubMainBranchGovernance) {
			value.Rules = value.Rules[:3]
			value.Rules = append(value.Rules, valid.Rules[3])
			value.Rules = append(value.Rules[:2], value.Rules[3:]...)
		}, "non-fast-forward protection"},
		{"missing deletion protection", func(value *GitHubMainBranchGovernance) {
			value.Rules = []map[string]json.RawMessage{valid.Rules[0], valid.Rules[2], valid.Rules[3]}
		}, "branch-deletion protection"},
		{"non-strict CI", func(value *GitHubMainBranchGovernance) {
			value.Rules[3]["parameters"] = json.RawMessage(`{"strict_required_status_checks_policy":false,"required_status_checks":[{"context":"Required CI gate"}]}`)
		}, "strict_required_status_checks_policy must be true"},
		{"stale observation", func(value *GitHubMainBranchGovernance) {
			value.ObservedAt = now.Add(-25 * time.Hour).Format(time.RFC3339Nano)
		}, "older than 24 hours"},
		{"future observation", func(value *GitHubMainBranchGovernance) {
			value.ObservedAt = now.Add(6 * time.Minute).Format(time.RFC3339Nano)
		}, "from the future"},
		{"wrong endpoint", func(value *GitHubMainBranchGovernance) { value.APIEndpoint = "/repos/other/repo/rules/branches/main" }, "bind to the main branch rules endpoint"},
		{"wrong branch", func(value *GitHubMainBranchGovernance) { value.Branch = "develop" }, "bind to the main branch rules endpoint"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validGitHubMainBranchGovernance(now)
			test.mutate(&value)
			err := ValidateGitHubMainBranchGovernance(value, now)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestDecodeGitHubMainBranchGovernanceRejectsDuplicateKeysAndOversize(t *testing.T) {
	if _, err := DecodeGitHubMainBranchGovernance([]byte(`{"schema_version":"threadforge.github-main-branch-governance.v1","schema_version":"x"}`)); err == nil {
		t.Fatal("duplicate schema_version key was accepted")
	}
	if _, err := DecodeGitHubMainBranchGovernance(make([]byte, maxGitHubBranchGovernanceBytes+1)); err == nil {
		t.Fatal("oversize governance evidence was accepted")
	}
}

func TestAssessmentRequiresEffectiveMainBranchGovernanceInSecurityApproval(t *testing.T) {
	t.Run("repository is bound to the candidate report", func(t *testing.T) {
		root := t.TempDir()
		report, _, evaluatorTrust := completeFixture(t, root)
		var evidence Evidence
		for index := range report.Approvals {
			if report.Approvals[index].Role == "security-reviewer" {
				evidence = report.Approvals[index].Evidence[len(report.Approvals[index].Evidence)-1]
			}
		}
		governance := validGitHubMainBranchGovernance(time.Now())
		governance.Repository = "another-org/other-repository"
		governance.APIEndpoint = "/repos/another-org/other-repository/rules/branches/main"
		content, err := json.Marshal(governance)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, evidence.Path), content, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(content)
		for index := range report.Approvals {
			if report.Approvals[index].Role == "security-reviewer" {
				report.Approvals[index].Evidence[len(report.Approvals[index].Evidence)-1].SHA256 = hex.EncodeToString(digest[:])
			}
		}
		sealFixtureApprovals(t, root, &report)
		assessment, err := AssessWithTrustAndMigrationReviewersAndApprovers(report, root, evaluatorTrust, fixtureMigrationAuthorityTrustStore(), fixtureApprovalTrustStore())
		if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "does not match the candidate repository") {
			t.Fatalf("governance snapshot for another repository was accepted: %#v, %v", assessment, err)
		}
	})

	t.Run("missing artifact", func(t *testing.T) {
		root := t.TempDir()
		report, _, evaluatorTrust := completeFixture(t, root)
		for index := range report.Approvals {
			if report.Approvals[index].Role == "security-reviewer" {
				report.Approvals[index].Evidence = report.Approvals[index].Evidence[:1]
			}
		}
		sealFixtureApprovals(t, root, &report)
		assessment, err := AssessWithTrustAndMigrationReviewersAndApprovers(report, root, evaluatorTrust, fixtureMigrationAuthorityTrustStore(), fixtureApprovalTrustStore())
		if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "must include one hash-bound GitHub main-branch governance artifact") {
			t.Fatalf("missing branch governance evidence was accepted: %#v, %v", assessment, err)
		}
	})

	t.Run("unprotected main", func(t *testing.T) {
		root := t.TempDir()
		report, _, evaluatorTrust := completeFixture(t, root)
		var evidence Evidence
		for index := range report.Approvals {
			if report.Approvals[index].Role == "security-reviewer" {
				evidence = report.Approvals[index].Evidence[len(report.Approvals[index].Evidence)-1]
			}
		}
		unprotected := validGitHubMainBranchGovernance(time.Now())
		unprotected.Rules = nil
		content, err := json.Marshal(unprotected)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, evidence.Path), content, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(content)
		for index := range report.Approvals {
			if report.Approvals[index].Role == "security-reviewer" {
				report.Approvals[index].Evidence[len(report.Approvals[index].Evidence)-1].SHA256 = hex.EncodeToString(digest[:])
			}
		}
		sealFixtureApprovals(t, root, &report)
		assessment, err := AssessWithTrustAndMigrationReviewersAndApprovers(report, root, evaluatorTrust, fixtureMigrationAuthorityTrustStore(), fixtureApprovalTrustStore())
		if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "GitHub main branch is missing required governance rules") {
			t.Fatalf("empty rules for main were accepted: %#v, %v", assessment, err)
		}
	})
}
