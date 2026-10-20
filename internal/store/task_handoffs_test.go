package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/continuity"
)

func TestAcceptTaskHandoffTransfersOwnerClaimCheckpointAndTaskHeadAtomically(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	task, claim, handoff, stamp := prepareOfferedHandoff(t, database)
	acceptance := buildTaskHandoffAcceptance(task, claim, handoff, "bob", stamp.Add(2*time.Second))
	if err := database.AcceptTaskHandoff(ctx, ctx, ctx, ctx, acceptance); err != nil {
		t.Fatalf("accept task handoff: %v", err)
	}
	updated, err := database.GetTask(ctx, task.ID)
	if err != nil || updated.Owner != "bob" || updated.Revision != task.Revision+1 {
		t.Fatalf("task after handoff=%#v error=%v", updated, err)
	}
	claims, err := database.TaskClaims(ctx, task.ID)
	if err != nil || len(claims) != 1 || claims[0].Owner != "bob" || claims[0].Revision != claim.Revision+1 ||
		claims[0].TaskRevision != updated.Revision || claims[0].BaselineSHA256 != claim.BaselineSHA256 {
		t.Fatalf("claim after handoff=%#v error=%v", claims, err)
	}
	handoffs, err := database.TaskHandoffs(ctx, task.ID)
	if err != nil || len(handoffs) != 1 || handoffs[0].Status != continuity.TaskHandoffAccepted ||
		handoffs[0].TransferredTaskRevision != updated.Revision || handoffs[0].TransferredClaimRevision != claims[0].Revision {
		t.Fatalf("handoffs after acceptance=%#v error=%v", handoffs, err)
	}
	history, err := database.TaskHistory(ctx, task.ID)
	if err != nil || len(history) != 2 || history[1].Action != "handoff_accepted" || history[1].Task.Owner != "bob" {
		t.Fatalf("task history after handoff=%#v error=%v", history, err)
	}
	entries, err := database.ContinuityEntries(ctx, task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	checkpointFound := false
	for _, entry := range entries {
		if entry.ID == handoff.CheckpointEntryID && entry.Kind == continuity.EntryCheckpoint && entry.Actor == "bob" {
			checkpointFound = true
		}
	}
	if !checkpointFound {
		t.Fatal("accepted checkpoint was not committed")
	}
}

func TestAcceptTaskHandoffRollsBackEveryRecordWhenCheckpointInsertFails(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	task, claim, handoff, stamp := prepareOfferedHandoff(t, database)
	blocker := continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: handoff.CheckpointEntryID, ProjectID: task.ProjectID,
		TaskID: task.ID, Kind: continuity.EntryCheckpoint, Actor: "alice", Summary: "Existing identifier",
		CreatedAt: stamp.Add(time.Second).Format(time.RFC3339Nano),
	}
	if _, err := database.AppendContinuityEntry(ctx, blocker); err != nil {
		t.Fatal(err)
	}
	acceptance := buildTaskHandoffAcceptance(task, claim, handoff, "bob", stamp.Add(2*time.Second))
	if err := database.AcceptTaskHandoff(ctx, ctx, ctx, ctx, acceptance); err == nil {
		t.Fatal("duplicate checkpoint identifier did not fail the atomic handoff")
	}
	unchanged, err := database.GetTask(ctx, task.ID)
	if err != nil || unchanged.Owner != "alice" || unchanged.Revision != task.Revision {
		t.Fatalf("task changed despite handoff rollback: %#v error=%v", unchanged, err)
	}
	claims, err := database.TaskClaims(ctx, task.ID)
	if err != nil || len(claims) != 1 || claims[0].Owner != "alice" || claims[0].Revision != claim.Revision {
		t.Fatalf("claim changed despite handoff rollback: %#v error=%v", claims, err)
	}
	handoffs, err := database.TaskHandoffs(ctx, task.ID)
	if err != nil || len(handoffs) != 1 || handoffs[0].Status != continuity.TaskHandoffOffered {
		t.Fatalf("handoff state changed despite rollback: %#v error=%v", handoffs, err)
	}
}

func TestTaskHandoffRejectAndCancelLeaveTaskAndClaimUnchanged(t *testing.T) {
	for _, test := range []struct {
		action string
		status continuity.TaskHandoffStatus
		actor  string
	}{
		{action: "rejected", status: continuity.TaskHandoffRejected, actor: "bob"},
		{action: "cancelled", status: continuity.TaskHandoffCancelled, actor: "alice"},
	} {
		t.Run(test.action, func(t *testing.T) {
			database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			task, claim, offered, stamp := prepareOfferedHandoff(t, database)
			finished := offered
			finished.Status = test.status
			finished.Revision++
			finished.UpdatedAt = stamp.Add(2 * time.Second).Format(time.RFC3339Nano)
			entry := handoffEntry("handoff-finish-event", finished, test.action, offered.Revision, test.actor, stamp.Add(2*time.Second))
			if _, err := database.AppendContinuityEntry(context.Background(), entry); err != nil {
				t.Fatalf("append %s: %v", test.action, err)
			}
			currentTask, err := database.GetTask(context.Background(), task.ID)
			if err != nil || currentTask.Owner != task.Owner || currentTask.Revision != task.Revision {
				t.Fatalf("task changed after %s: %#v error=%v", test.action, currentTask, err)
			}
			currentClaims, err := database.TaskClaims(context.Background(), task.ID)
			if err != nil || len(currentClaims) != 1 || currentClaims[0].Owner != claim.Owner || currentClaims[0].Revision != claim.Revision {
				t.Fatalf("claim changed after %s: %#v error=%v", test.action, currentClaims, err)
			}
			currentHandoffs, err := database.TaskHandoffs(context.Background(), task.ID)
			if err != nil || len(currentHandoffs) != 1 || currentHandoffs[0].Status != test.status {
				t.Fatalf("handoff state after %s: %#v error=%v", test.action, currentHandoffs, err)
			}
		})
	}
}

func TestTaskHandoffAcceptanceRejectsClaimDriftAfterOffer(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	task, claim, offered, stamp := prepareOfferedHandoff(t, database)
	now := time.Now().UTC()
	renewed := claim
	renewed.Revision++
	renewed.UpdatedAt = now.Format(time.RFC3339Nano)
	renewed.LeaseExpiresAt = now.Add(3 * time.Hour).Format(time.RFC3339Nano)
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("handoff-claim-renewal", renewed, "renewed", claim.Revision)); err != nil {
		t.Fatal(err)
	}
	acceptance := buildTaskHandoffAcceptance(task, claim, offered, "bob", stamp.Add(2*time.Second))
	if err := database.AcceptTaskHandoff(ctx, ctx, ctx, ctx, acceptance); !errors.Is(err, ErrContinuityConflict) {
		t.Fatalf("claim drift acceptance error = %v", err)
	}
	current, err := database.GetTask(ctx, task.ID)
	if err != nil || current.Owner != "alice" || current.Revision != task.Revision {
		t.Fatalf("task after stale-claim rejection=%#v error=%v", current, err)
	}
}

func TestConcurrentTaskHandoffAcceptanceTransfersOwnershipOnlyOnce(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task, claim, offered, stamp := prepareOfferedHandoff(t, database)
	acceptances := []TaskHandoffAcceptance{
		buildTaskHandoffAcceptance(task, claim, offered, "bob", stamp.Add(2*time.Second)),
		buildTaskHandoffAcceptance(task, claim, offered, "bob", stamp.Add(3*time.Second)),
	}
	start := make(chan struct{})
	results := make(chan error, len(acceptances))
	var workers sync.WaitGroup
	for _, acceptance := range acceptances {
		workers.Add(1)
		go func(acceptance TaskHandoffAcceptance) {
			defer workers.Done()
			<-start
			ctx := context.Background()
			results <- database.AcceptTaskHandoff(ctx, ctx, ctx, ctx, acceptance)
		}(acceptance)
	}
	close(start)
	workers.Wait()
	close(results)
	successes, failures := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent handoff acceptances: successes=%d failures=%d", successes, failures)
	}
	current, err := database.GetTask(context.Background(), task.ID)
	if err != nil || current.Owner != "bob" || current.Revision != task.Revision+1 {
		t.Fatalf("task after competing acceptances=%#v error=%v", current, err)
	}
	handoffs, err := database.TaskHandoffs(context.Background(), task.ID)
	if err != nil || len(handoffs) != 1 || handoffs[0].Status != continuity.TaskHandoffAccepted || handoffs[0].Revision != 3 {
		t.Fatalf("handoff after competing acceptances=%#v error=%v", handoffs, err)
	}
}

func TestTaskHandoffReadsFailClosedWhenAcceptedLinkIsMissing(t *testing.T) {
	for _, missing := range []string{"checkpoint", "claim_transfer"} {
		t.Run(missing, func(t *testing.T) {
			database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			ctx := context.Background()
			task, claim, offered, stamp := prepareOfferedHandoff(t, database)
			acceptance := buildTaskHandoffAcceptance(task, claim, offered, "bob", stamp.Add(2*time.Second))
			if err := database.AcceptTaskHandoff(ctx, ctx, ctx, ctx, acceptance); err != nil {
				t.Fatal(err)
			}
			entryID := offered.CheckpointEntryID
			if missing == "claim_transfer" {
				entryID = acceptance.ClaimEntry.ID
			}
			if _, err := database.db.ExecContext(ctx, `DROP TRIGGER continuity_entries_no_delete`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(ctx, `DELETE FROM continuity_entries WHERE id = ?`, entryID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.TaskHandoffs(ctx, task.ID); err == nil {
				t.Fatal("accepted handoff read succeeded without its linked transfer evidence")
			}
		})
	}
}

func prepareOfferedHandoff(t *testing.T, database *Store) (continuity.Task, continuity.TaskClaim, continuity.TaskHandoff, time.Time) {
	t.Helper()
	ctx := context.Background()
	stamp := time.Now().UTC().Add(-5 * time.Second)
	created := stamp.Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-handoff-one", ProjectID: "repo-handoff", Owner: "alice",
		Title: "Transfer parser repair", Objective: "Complete and verify parser work",
		AcceptanceCriteria: []string{"Focused tests pass"}, Status: continuity.TaskInProgress,
		Revision: 1, CreatedAt: created, UpdatedAt: created,
	}
	if err := database.CreateTask(ctx, task, "alice", "handoff fixture"); err != nil {
		t.Fatal(err)
	}
	claim := continuity.TaskClaim{
		SchemaVersion: continuity.TaskClaimSchemaVersion, ID: "claim-handoff-one", ProjectID: task.ProjectID,
		TaskID: task.ID, Owner: "alice", Scopes: []continuity.TaskClaimScope{{Kind: continuity.TaskClaimScopePath, Value: "pkg/parser/**"}},
		TaskRevision: task.Revision, BaselineSHA256: strings.Repeat("a", 64),
		LeaseExpiresAt: stamp.Add(2 * time.Hour).Format(time.RFC3339Nano), Status: "active", Revision: 1,
		CreatedAt: created, UpdatedAt: created,
	}
	if _, err := database.AppendContinuityEntry(ctx, claimEntry("claim-handoff-acquire", claim, "acquired", 0)); err != nil {
		t.Fatal(err)
	}
	handoff := continuity.TaskHandoff{
		SchemaVersion: continuity.TaskHandoffSchemaVersion, ID: "handoff-one", ProjectID: task.ProjectID, TaskID: task.ID,
		FromOwner: "alice", ToOwner: "bob", TaskRevision: task.Revision, ClaimID: claim.ID, ClaimRevision: claim.Revision,
		BaselineSHA256: claim.BaselineSHA256, CheckpointEntryID: "checkpoint-transfer-one",
		CheckpointSummary: "Parser repair is implemented; run the focused suite.", CheckpointDetails: "No execution authority is conveyed.",
		Status: continuity.TaskHandoffDraft, Revision: 1, CreatedAt: created, UpdatedAt: created,
	}
	draft := handoffEntry("handoff-draft-event", handoff, "drafted", 0, "alice", stamp)
	if _, err := database.AppendContinuityEntry(ctx, draft); err != nil {
		t.Fatal(err)
	}
	offeredAt := stamp.Add(time.Second)
	handoff.Status = continuity.TaskHandoffOffered
	handoff.Revision = 2
	handoff.UpdatedAt = offeredAt.Format(time.RFC3339Nano)
	offer := handoffEntry("handoff-offer-event", handoff, "offered", 1, "alice", offeredAt)
	if _, err := database.AppendContinuityEntry(ctx, offer); err != nil {
		t.Fatal(err)
	}
	return task, claim, handoff, stamp
}

func buildTaskHandoffAcceptance(task continuity.Task, claim continuity.TaskClaim, offered continuity.TaskHandoff,
	actor string, at time.Time) TaskHandoffAcceptance {
	stamp := at.Format(time.RFC3339Nano)
	updatedTask := task
	updatedTask.Owner = offered.ToOwner
	updatedTask.Revision++
	updatedTask.UpdatedAt = stamp
	accepted := offered
	accepted.Status = continuity.TaskHandoffAccepted
	accepted.Revision++
	accepted.UpdatedAt = stamp
	accepted.TransferredTaskRevision = updatedTask.Revision
	accepted.TransferredClaimRevision = claim.Revision + 1
	handoffEvent := continuity.TaskHandoffEvent{
		SchemaVersion: continuity.TaskHandoffEventSchemaVersion, Action: "accepted",
		ExpectedRevision: offered.Revision, Handoff: accepted,
	}
	handoffRecord := continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: "handoff-accept-event", ProjectID: task.ProjectID, TaskID: task.ID,
		Kind: continuity.EntryTaskHandoff, Actor: actor, Summary: "Task handoff accepted",
		TaskHandoff: &handoffEvent, CreatedAt: stamp,
	}
	transferred := claim
	transferred.Owner = offered.ToOwner
	transferred.TaskRevision = updatedTask.Revision
	transferred.Revision++
	transferred.UpdatedAt = stamp
	transferred.LeaseExpiresAt = claim.LeaseExpiresAt
	claimRecord := claimEntry("claim-handoff-transfer", transferred, "transferred", claim.Revision)
	checkpoint := continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: offered.CheckpointEntryID, ProjectID: task.ProjectID, TaskID: task.ID,
		Kind: continuity.EntryCheckpoint, Actor: actor, Summary: offered.CheckpointSummary,
		Details: offered.CheckpointDetails, CreatedAt: stamp,
	}
	return TaskHandoffAcceptance{
		Task: updatedTask, HandoffEntry: handoffRecord, ClaimEntry: claimRecord, CheckpointEntry: checkpoint,
		Actor: actor, Reason: "accept task handoff",
	}
}

func handoffEntry(id string, handoff continuity.TaskHandoff, action string, expectedRevision int,
	actor string, at time.Time) continuity.Entry {
	event := continuity.TaskHandoffEvent{
		SchemaVersion: continuity.TaskHandoffEventSchemaVersion, Action: action,
		ExpectedRevision: expectedRevision, Handoff: handoff,
	}
	return continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: id, ProjectID: handoff.ProjectID, TaskID: handoff.TaskID,
		Kind: continuity.EntryTaskHandoff, Actor: actor, Summary: "Task handoff " + action,
		TaskHandoff: &event, CreatedAt: at.Format(time.RFC3339Nano),
	}
}
