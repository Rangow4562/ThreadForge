package continuity

import (
	"strings"
	"testing"
	"time"
)

func validTaskClaimFixture() TaskClaim {
	stamp := "2026-10-19T10:00:00Z"
	return TaskClaim{
		SchemaVersion: TaskClaimSchemaVersion, ID: "claim-one", ProjectID: "repo-a", TaskID: "task-one",
		Owner: "agent-a", Scopes: []TaskClaimScope{{Kind: TaskClaimScopePath, Value: "src/api/**"}},
		TaskRevision: 1, BaselineSHA256: strings.Repeat("a", 64),
		LeaseExpiresAt: "2026-10-19T11:00:00Z", Status: "active", Revision: 1,
		CreatedAt: stamp, UpdatedAt: stamp,
	}
}

func TestTaskClaimValidationAndAssessment(t *testing.T) {
	claim := validTaskClaimFixture()
	if err := ValidateTaskClaim(claim); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTaskClaimEvent(TaskClaimEvent{
		SchemaVersion: TaskClaimEventSchemaVersion, Action: "acquired", Claim: claim,
	}); err != nil {
		t.Fatal(err)
	}

	assessment := AssessTaskClaim(claim, 2, strings.Repeat("b", 64), time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	if assessment.LeaseStatus != "expired" || assessment.TaskRevisionStatus != "stale" ||
		assessment.BaselineStatus != "mismatch" || assessment.ExecutionAuthorized {
		t.Fatalf("claim assessment = %#v", assessment)
	}
	assessment = AssessTaskClaim(claim, 1, "", time.Date(2026, 10, 19, 10, 30, 0, 0, time.UTC))
	if assessment.LeaseStatus != "active" || assessment.TaskRevisionStatus != "current" || assessment.BaselineStatus != "unverified" {
		t.Fatalf("unverified baseline assessment = %#v", assessment)
	}
}

func TestTaskClaimRejectsUnsafeOrAmbiguousScopes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TaskClaim)
		want   string
	}{
		{"absolute path", func(c *TaskClaim) { c.Scopes[0].Value = "/src/api" }, "path scope"},
		{"parent traversal", func(c *TaskClaim) { c.Scopes[0].Value = "src/../secrets" }, "path scope"},
		{"unsupported glob", func(c *TaskClaim) { c.Scopes[0].Value = "src/*/api" }, "path scope"},
		{"credential path", func(c *TaskClaim) { c.Scopes[0].Value = "config/private-key.pem" }, "path scope"},
		{"unsorted duplicate scope", func(c *TaskClaim) {
			c.Scopes = []TaskClaimScope{
				{Kind: TaskClaimScopePath, Value: "src/api"}, {Kind: TaskClaimScopeModule, Value: "core"},
			}
		}, "sorted"},
		{"credential label", func(c *TaskClaim) {
			c.Scopes[0] = TaskClaimScope{Kind: TaskClaimScopeAPI, Value: "Bearer abcdefghijklmnopqrstuvwxyz0123456789"}
		}, "scope"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claim := validTaskClaimFixture()
			test.mutate(&claim)
			if err := ValidateTaskClaim(claim); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestTaskClaimEventRequiresMonotonicRevisionAndTerminalState(t *testing.T) {
	claim := validTaskClaimFixture()
	claim.Revision = 2
	claim.UpdatedAt = "2026-10-19T10:10:00Z"
	claim.LeaseExpiresAt = "2026-10-19T11:10:00Z"
	if err := ValidateTaskClaimEvent(TaskClaimEvent{
		SchemaVersion: TaskClaimEventSchemaVersion, Action: "renewed", ExpectedRevision: 1, Claim: claim,
	}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTaskClaimEvent(TaskClaimEvent{
		SchemaVersion: TaskClaimEventSchemaVersion, Action: "renewed", ExpectedRevision: 0, Claim: claim,
	}); err == nil {
		t.Fatal("claim renewal with a non-monotonic revision was accepted")
	}
	claim.Status = "released"
	claim.ReleasedAt = claim.UpdatedAt
	if err := ValidateTaskClaimEvent(TaskClaimEvent{
		SchemaVersion: TaskClaimEventSchemaVersion, Action: "released", ExpectedRevision: 1, Claim: claim,
	}); err != nil {
		t.Fatal(err)
	}
}
