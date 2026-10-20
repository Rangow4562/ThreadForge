package ci

import (
	"strings"
	"testing"
)

func TestResourceAttributesFromGitHubAllowlistAndEncodeValues(t *testing.T) {
	attrs := resourceAttributesFromEnvironment([]string{
		"GITHUB_ACTIONS=true",
		"GITHUB_EVENT_NAME=pull_request",
		"GITHUB_RUN_ID=run-123",
		"GITHUB_RUN_ATTEMPT=2",
		"GITHUB_WORKFLOW=Agent Review, CI",
		"GITHUB_JOB=review",
		"GITHUB_SHA=0123456789abcdef",
		"GITHUB_REPOSITORY=org/private-repo",
		"GITHUB_ACTOR=sample-user",
		"GITHUB_HEAD_REF=feature/safe",
		"GITHUB_REF=refs/pull/42/merge",
		"AWS_SECRET_ACCESS_KEY=must-not-appear",
	})
	for _, expected := range []string{
		"beacon.origin=ci", "beacon.run.provider=github_actions", "beacon.run.run_id=run-123",
		"beacon.run.workflow=Agent%20Review%2C%20CI", "beacon.run.branch=feature%2Fsafe",
		"beacon.run.pr_number=42", "beacon.run.ephemeral=true",
	} {
		if !strings.Contains(attrs, expected) {
			t.Fatalf("resource attrs missing %q: %s", expected, attrs)
		}
	}
	if strings.Contains(attrs, "AWS_SECRET_ACCESS_KEY") || strings.Contains(attrs, "must-not-appear") {
		t.Fatalf("non-allowlisted environment was exported: %s", attrs)
	}
}

func TestResourceAttributesUseGenericCIWithoutGitHubIdentity(t *testing.T) {
	attrs := resourceAttributesFromEnvironment([]string{"CI=true", "GITHUB_ACTIONS=false", "GITHUB_ACTOR=person"})
	if attrs != "beacon.origin=ci,beacon.run.provider=ci,beacon.run.ephemeral=true" {
		t.Fatalf("generic CI attrs=%q", attrs)
	}
}

func TestResourceAttributesIgnoreMalformedPullRequestRefsAndStripControls(t *testing.T) {
	attrs := resourceAttributesFromEnvironment([]string{
		"GITHUB_ACTIONS=true", "GITHUB_EVENT_NAME=pull_request", "GITHUB_REF=refs/pull/not-a-number/merge",
		"GITHUB_WORKFLOW=workflow\nprivate", "GITHUB_RUN_ID=run-1",
	})
	if strings.Contains(attrs, "beacon.run.pr=") || strings.Contains(attrs, "pr_number") || strings.Contains(attrs, "\n") {
		t.Fatalf("malformed ref or control character was retained: %q", attrs)
	}
}
