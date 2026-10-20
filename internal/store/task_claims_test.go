package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/continuity"
)

func TestTaskClaimLifecycleRevalidatesAndNeverTransfersOnExpiry(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := "2026-10-19T10:00:00Z"
	for _, id := range []string{"task-claim-a", "task-claim-b"} {
		task := continuity.Task{
			SchemaVersion: continuity.SchemaVersion, ID: id, ProjectID: "repo-claims", Owner: "author", Title: "Claim test",
			Objective: "Exercise local claim lease lifecycle", AcceptanceCriteria: []string{"No implicit reassignment"},
			Status: continuity.TaskOpen, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
		}
		if err := database.CreateTask(ctx, task, "author", "claim fixture"); err != nil {
			t.Fatal(err)
		}
	}

	claim := claimFixture("claim-primary", "task-claim-a", "2026-10-19T11:00:00Z")
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-claim-acquire", claim, "acquired", 0)); err != nil {
		t.Fatal(err)
	}

	assessment := continuity.AssessTaskClaim(claim, 1, claim.BaselineSHA256,
		mustParseClaimTime("2026-10-19T12:00:00Z"))
	if assessment.LeaseStatus != "expired" || assessment.ExecutionAuthorized {
		t.Fatalf("expired claim assessment = %#v", assessment)
	}
	conflicting := claimFixture("claim-conflict", "task-claim-b", "2026-10-19T13:00:00Z")
	conflicting.Scopes = []continuity.TaskClaimScope{{Kind: continuity.TaskClaimScopePath, Value: "src/api/v1/**"}}
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-claim-conflict", conflicting, "acquired", 0)); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("expired claim must still block overlapping acquisition, got %v", err)
	}

	task, err := database.GetTask(ctx, "task-claim-a")
	if err != nil {
		t.Fatal(err)
	}
	task.Summary = "Task changed after the claim baseline"
	task.Revision = 2
	task.UpdatedAt = "2026-10-19T12:01:00Z"
	if err := database.UpdateTask(ctx, task, 1, "agent-a", "record progress"); err != nil {
		t.Fatal(err)
	}
	staleRenewal := claim
	staleRenewal.Revision = 2
	staleRenewal.UpdatedAt = "2026-10-19T12:02:00Z"
	staleRenewal.LeaseExpiresAt = "2026-10-19T14:00:00Z"
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-claim-stale-renew", staleRenewal, "renewed", 1)); err == nil || !strings.Contains(err.Error(), "current task revision") {
		t.Fatalf("stale task revision renewal error = %v", err)
	}

	revalidated := claim
	revalidated.Revision = 2
	revalidated.TaskRevision = 2
	revalidated.BaselineSHA256 = strings.Repeat("b", 64)
	revalidated.UpdatedAt = "2026-10-19T12:03:00Z"
	revalidated.LeaseExpiresAt = "2026-10-19T14:03:00Z"
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-claim-revalidate", revalidated, "revalidated", 1)); err != nil {
		t.Fatal(err)
	}

	released := revalidated
	released.Revision = 3
	released.Status = "released"
	released.UpdatedAt = "2026-10-19T12:04:00Z"
	released.ReleasedAt = released.UpdatedAt
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-claim-release", released, "released", 2)); err != nil {
		t.Fatal(err)
	}

	claims, err := database.TaskClaims(ctx, "task-claim-a")
	if err != nil || len(claims) != 1 || claims[0].Revision != 3 || claims[0].Status != "released" {
		t.Fatalf("task claims = %#v, error = %v", claims, err)
	}
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-claim-conflict", conflicting, "acquired", 0)); err != nil {
		t.Fatalf("released claim should free its scope: %v", err)
	}
}

func TestTaskClaimRejectsOwnerMutationAndCorruptHistory(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := "2026-10-19T10:00:00Z"
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-claim-c", ProjectID: "repo-claims", Owner: "author", Title: "Claim test",
		Objective: "Reject altered claim history", AcceptanceCriteria: []string{"Reads fail closed"},
		Status: continuity.TaskOpen, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "author", "claim fixture"); err != nil {
		t.Fatal(err)
	}
	claim := claimFixture("claim-owner", task.ID, "2026-10-19T11:00:00Z")
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-claim-owner", claim, "acquired", 0)); err != nil {
		t.Fatal(err)
	}
	transferred := claim
	transferred.Owner = "agent-b"
	transferred.Revision = 2
	transferred.UpdatedAt = "2026-10-19T10:10:00Z"
	transferred.LeaseExpiresAt = "2026-10-19T12:00:00Z"
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-claim-transfer", transferred, "renewed", 1)); err == nil {
		t.Fatal("claim ownership changed through a renewal")
	}

	if _, err := database.db.Exec(`UPDATE continuity_entries SET entry_json = '{}' WHERE id = ?`, "event-claim-owner"); err == nil {
		t.Fatal("append-only claim history was mutable")
	}
	claims, err := database.TaskClaims(ctx, task.ID)
	if err != nil || len(claims) != 1 || claims[0].Owner != "agent-a" {
		t.Fatalf("claims after rejected mutation = %#v, error = %v", claims, err)
	}
}

func TestExpiredTaskClaimCanBeExplicitlyReleased(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := "2026-10-19T10:00:00Z"
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-claim-expire", ProjectID: "repo-claims", Owner: "author", Title: "Expired claim",
		Objective: "Allow explicit cleanup without implicit ownership transfer", AcceptanceCriteria: []string{"Expired claim can be released"},
		Status: continuity.TaskOpen, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "author", "expired claim fixture"); err != nil {
		t.Fatal(err)
	}
	claim := claimFixture("claim-expiring", task.ID, "2026-10-19T11:00:00Z")
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-expiring-acquire", claim, "acquired", 0)); err != nil {
		t.Fatal(err)
	}
	released := claim
	released.Revision = 2
	released.Status = "released"
	released.UpdatedAt = "2026-10-19T12:00:00Z"
	released.ReleasedAt = released.UpdatedAt
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("event-expiring-release", released, "released", 1)); err != nil {
		t.Fatalf("explicit release after lease expiry failed: %v", err)
	}
	claims, err := database.TaskClaims(ctx, task.ID)
	if err != nil || len(claims) != 1 || claims[0].Status != "released" {
		t.Fatalf("released expired claim = %#v, error=%v", claims, err)
	}
}

func TestConcurrentOverlappingTaskClaimsOnlyOneAcquires(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := "2026-10-19T10:00:00Z"
	for _, taskID := range []string{"task-claim-concurrent-a", "task-claim-concurrent-b"} {
		task := continuity.Task{
			SchemaVersion: continuity.SchemaVersion, ID: taskID, ProjectID: "repo-claims", Owner: "author", Title: "Concurrent claim",
			Objective: "Serialize overlapping claims", AcceptanceCriteria: []string{"At most one claim succeeds"},
			Status: continuity.TaskOpen, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
		}
		if err := database.CreateTask(ctx, task, "author", "concurrent claim fixture"); err != nil {
			t.Fatal(err)
		}
	}
	entries := []continuity.Entry{
		claimEntry("event-concurrent-a", claimFixture("claim-concurrent-a", "task-claim-concurrent-a", "2026-10-19T11:00:00Z"), "acquired", 0),
		claimEntry("event-concurrent-b", claimFixture("claim-concurrent-b", "task-claim-concurrent-b", "2026-10-19T11:00:00Z"), "acquired", 0),
	}
	start := make(chan struct{})
	errorsSeen := make(chan error, len(entries))
	for _, entry := range entries {
		entry := entry
		go func() {
			<-start
			_, err := database.AppendContinuityEntry(ctx, entry)
			errorsSeen <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range entries {
		err := <-errorsSeen
		if err == nil {
			successes++
		} else if strings.Contains(err.Error(), "overlaps") {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent claim error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("claim race successes=%d conflicts=%d", successes, conflicts)
	}
}

func claimFixture(id, taskID, leaseExpiresAt string) continuity.TaskClaim {
	stamp := "2026-10-19T10:00:00Z"
	return continuity.TaskClaim{
		SchemaVersion: continuity.TaskClaimSchemaVersion, ID: id, ProjectID: "repo-claims", TaskID: taskID,
		Owner: "agent-a", Scopes: []continuity.TaskClaimScope{{Kind: continuity.TaskClaimScopePath, Value: "src/api/**"}},
		TaskRevision: 1, BaselineSHA256: strings.Repeat("a", 64), LeaseExpiresAt: leaseExpiresAt,
		Status: "active", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
}

func claimEntry(id string, claim continuity.TaskClaim, action string, expectedRevision int) continuity.Entry {
	claimEvent := continuity.TaskClaimEvent{
		SchemaVersion: continuity.TaskClaimEventSchemaVersion, Action: action,
		ExpectedRevision: expectedRevision, Claim: claim,
	}
	return continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: id, ProjectID: claim.ProjectID, TaskID: claim.TaskID,
		Kind: continuity.EntryTaskClaim, Actor: claim.Owner, Summary: "Task claim " + action,
		TaskClaim: &claimEvent, CreatedAt: claim.UpdatedAt,
	}
}

func mustParseClaimTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
