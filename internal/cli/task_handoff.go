package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/continuity"
)

func runTaskHandoff(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge task handoff <draft|offer|accept|reject|cancel|show|list>")
		return 2
	}
	switch args[0] {
	case "draft", "offer", "accept", "reject", "cancel":
		return mutateTaskHandoff(args[1:], args[0], stdout, stderr)
	case "show":
		return readTaskHandoffs(args[1:], true, stdout, stderr)
	case "list":
		return readTaskHandoffs(args[1:], false, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown task handoff command %q\n", args[0])
		return 2
	}
}

func mutateTaskHandoff(args []string, action string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("task handoff "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	handoffID := fs.String("id", "", "stable handoff identifier")
	taskID := fs.String("task-id", "", "task identifier")
	expectedRevision := fs.Int("expected-revision", 0, "expected current handoff revision")
	expectedTaskRevision := fs.Int("expected-task-revision", 0, "expected current task revision (draft only)")
	claimID := fs.String("claim-id", "", "active task claim identifier (draft only)")
	expectedClaimRevision := fs.Int("expected-claim-revision", 0, "expected active claim revision (draft only)")
	baselineSHA256 := fs.String("baseline-sha256", "", "caller-supplied current code baseline digest (draft only)")
	toOwner := fs.String("to-owner", "", "handoff recipient (draft only)")
	checkpointEntryID := fs.String("checkpoint-entry-id", "", "stable checkpoint entry identifier (draft only)")
	checkpointSummary := fs.String("checkpoint-summary", "", "bounded progress checkpoint (draft only)")
	checkpointDetails := fs.String("checkpoint-details", "", "optional bounded checkpoint detail (draft only)")
	actor := fs.String("actor", "local-user", "authenticated task participant")
	identityKey := fs.String("identity-key", "", "trusted team identity for team-protected projects")
	reason := fs.String("reason", "task handoff", "audit reason for acceptance")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*handoffID) == "" || strings.TrimSpace(*taskID) == "" || fs.NArg() != 0 {
		printTaskHandoffUsage(stderr, action)
		return 2
	}
	if action == "draft" {
		if *expectedTaskRevision < 1 || *expectedClaimRevision < 1 || strings.TrimSpace(*claimID) == "" ||
			strings.TrimSpace(*toOwner) == "" || !validTaskClaimDigest(*baselineSHA256) ||
			strings.TrimSpace(*checkpointEntryID) == "" || strings.TrimSpace(*checkpointSummary) == "" || *expectedRevision != 0 {
			printTaskHandoffUsage(stderr, action)
			return 2
		}
	} else if *expectedRevision < 1 || *expectedTaskRevision != 0 || *expectedClaimRevision != 0 || *claimID != "" ||
		*baselineSHA256 != "" || *toOwner != "" || *checkpointEntryID != "" || *checkpointSummary != "" || *checkpointDetails != "" {
		printTaskHandoffUsage(stderr, action)
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	ctx := context.Background()
	task, err := databaseStore.GetTask(ctx, *taskID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	now := time.Now().UTC()
	if action == "draft" {
		return draftTaskHandoff(ctx, databaseStore, *database, task, now, *handoffID, *expectedTaskRevision,
			*claimID, *expectedClaimRevision, *baselineSHA256, *toOwner, *checkpointEntryID,
			*checkpointSummary, *checkpointDetails, *actor, *identityKey, stdout, stderr)
	}
	handoffs, err := databaseStore.TaskHandoffs(ctx, task.ID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var current continuity.TaskHandoff
	for _, candidate := range handoffs {
		if candidate.ID == *handoffID {
			current = candidate
			break
		}
	}
	if current.ID == "" {
		_, _ = fmt.Fprintln(stderr, "task handoff not found")
		return 1
	}
	if current.Revision != *expectedRevision {
		_, _ = fmt.Fprintln(stderr, store.ErrContinuityConflict)
		return 1
	}
	if action == "accept" {
		return acceptTaskHandoff(ctx, databaseStore, *database, task, current, now, *actor, *identityKey, *reason, stdout, stderr)
	}
	return appendTaskHandoffTransition(ctx, databaseStore, *database, task, current, action, now, *actor, *identityKey, stdout, stderr)
}

func draftTaskHandoff(ctx context.Context, databaseStore *store.Store, databasePath string, task continuity.Task, now time.Time,
	id string, expectedTaskRevision int, claimID string, expectedClaimRevision int, baseline, toOwner, checkpointID,
	checkpointSummary, checkpointDetails, actor, identityKey string, stdout, stderr io.Writer) int {
	if task.Revision != expectedTaskRevision {
		_, _ = fmt.Fprintln(stderr, store.ErrContinuityConflict)
		return 1
	}
	claims, err := databaseStore.TaskClaims(ctx, task.ID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var claim continuity.TaskClaim
	for _, candidate := range claims {
		if candidate.ID == claimID {
			claim = candidate
			break
		}
	}
	if claim.ID == "" || claim.Revision != expectedClaimRevision || claim.BaselineSHA256 != baseline {
		_, _ = fmt.Fprintln(stderr, store.ErrContinuityConflict)
		return 1
	}
	actorName, authorizer, err := authorizeContinuityActor(databasePath, task.ProjectID, "continuity.entry.append", actor, identityKey)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if task.Owner == "" || actorName != task.Owner {
		_, _ = fmt.Fprintln(stderr, "only the current task owner may draft a handoff")
		return 1
	}
	stamp := now.Format(time.RFC3339Nano)
	handoff := continuity.TaskHandoff{
		SchemaVersion: continuity.TaskHandoffSchemaVersion, ID: id, ProjectID: task.ProjectID, TaskID: task.ID,
		FromOwner: task.Owner, ToOwner: toOwner, TaskRevision: task.Revision, ClaimID: claim.ID,
		ClaimRevision: claim.Revision, BaselineSHA256: baseline, CheckpointEntryID: checkpointID,
		CheckpointSummary: checkpointSummary, CheckpointDetails: checkpointDetails, Status: continuity.TaskHandoffDraft,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	entry, err := newTaskHandoffEntry(handoff, "drafted", 0, actorName, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := continuity.ValidateEntry(entry); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	proof, err := authorizer.withMutationProof(context.Background(), "continuity.entry.append", "appended", "entry", entry.ID,
		task.ProjectID, 0, entry, continuity.EntryAppendAuthorizationReason, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := databaseStore.AppendContinuityEntry(proof, entry); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, continuity.TaskHandoffRead{
		SchemaVersion: continuity.TaskHandoffReadSchemaVersion, TaskID: task.ID,
		Handoffs: []continuity.TaskHandoff{handoff}, ExecutionAuthorized: false,
	}, stderr)
}

func appendTaskHandoffTransition(ctx context.Context, databaseStore *store.Store, databasePath string, task continuity.Task,
	current continuity.TaskHandoff, action string, now time.Time, actor, identityKey string, stdout, stderr io.Writer) int {
	var status continuity.TaskHandoffStatus
	wantActor := current.FromOwner
	switch action {
	case "offer":
		status = continuity.TaskHandoffOffered
		if current.Status != continuity.TaskHandoffDraft {
			_, _ = fmt.Fprintln(stderr, "only a draft handoff can be offered")
			return 1
		}
	case "reject":
		status, wantActor = continuity.TaskHandoffRejected, current.ToOwner
		if current.Status != continuity.TaskHandoffOffered {
			_, _ = fmt.Fprintln(stderr, "only an offered handoff can be rejected")
			return 1
		}
	case "cancel":
		status = continuity.TaskHandoffCancelled
		if current.Status != continuity.TaskHandoffDraft && current.Status != continuity.TaskHandoffOffered {
			_, _ = fmt.Fprintln(stderr, "only a pending handoff can be cancelled")
			return 1
		}
	default:
		_, _ = fmt.Fprintln(stderr, "unsupported task handoff transition")
		return 2
	}
	actorName, authorizer, err := authorizeContinuityActor(databasePath, task.ProjectID, "continuity.entry.append", actor, identityKey)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if actorName != wantActor {
		_, _ = fmt.Fprintln(stderr, "handoff transition actor is not authorized for this state change")
		return 1
	}
	next := current
	next.Status = status
	next.Revision++
	next.UpdatedAt = now.Format(time.RFC3339Nano)
	entry, err := newTaskHandoffEntry(next, map[string]string{"offer": "offered", "reject": "rejected", "cancel": "cancelled"}[action], current.Revision, actorName, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := continuity.ValidateEntry(entry); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	proof, err := authorizer.withMutationProof(context.Background(), "continuity.entry.append", "appended", "entry", entry.ID,
		task.ProjectID, 0, entry, continuity.EntryAppendAuthorizationReason, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := databaseStore.AppendContinuityEntry(proof, entry); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, continuity.TaskHandoffRead{
		SchemaVersion: continuity.TaskHandoffReadSchemaVersion, TaskID: task.ID,
		Handoffs: []continuity.TaskHandoff{next}, ExecutionAuthorized: false,
	}, stderr)
}

func acceptTaskHandoff(ctx context.Context, databaseStore *store.Store, databasePath string, task continuity.Task,
	offered continuity.TaskHandoff, now time.Time, actor, identityKey, reason string, stdout, stderr io.Writer) int {
	if offered.Status != continuity.TaskHandoffOffered || task.Owner != offered.FromOwner || task.Revision != offered.TaskRevision {
		_, _ = fmt.Fprintln(stderr, store.ErrContinuityConflict)
		return 1
	}
	actorName, authorizer, err := authorizeContinuityActor(databasePath, task.ProjectID, "task.handoff.accept", actor, identityKey)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if actorName != offered.ToOwner {
		_, _ = fmt.Fprintln(stderr, "only the handoff recipient may accept it")
		return 1
	}
	claims, err := databaseStore.TaskClaims(ctx, task.ID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var currentClaim continuity.TaskClaim
	for _, claim := range claims {
		if claim.ID == offered.ClaimID {
			currentClaim = claim
			break
		}
	}
	if currentClaim.ID == "" || currentClaim.Revision != offered.ClaimRevision || currentClaim.Status != "active" {
		_, _ = fmt.Fprintln(stderr, store.ErrContinuityConflict)
		return 1
	}
	updatedAt := now.Format(time.RFC3339Nano)
	updatedTask := task
	updatedTask.Owner = offered.ToOwner
	updatedTask.Revision++
	updatedTask.UpdatedAt = updatedAt
	accepted := offered
	accepted.Status = continuity.TaskHandoffAccepted
	accepted.Revision++
	accepted.UpdatedAt = updatedAt
	accepted.TransferredTaskRevision = updatedTask.Revision
	accepted.TransferredClaimRevision = currentClaim.Revision + 1
	handoffEntry, err := newTaskHandoffEntry(accepted, "accepted", offered.Revision, actorName, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	claim := currentClaim
	claim.Owner = offered.ToOwner
	claim.TaskRevision = updatedTask.Revision
	claim.Revision++
	claim.UpdatedAt = updatedAt
	claimEvent := continuity.TaskClaimEvent{
		SchemaVersion: continuity.TaskClaimEventSchemaVersion, Action: "transferred",
		ExpectedRevision: currentClaim.Revision, Claim: claim,
	}
	claimEntry, err := newTaskClaimEntry(actorName, claimEvent, "Task claim transferred by accepted handoff", now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	checkpoint := continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: offered.CheckpointEntryID, ProjectID: task.ProjectID, TaskID: task.ID,
		Kind: continuity.EntryCheckpoint, Actor: actorName, Summary: offered.CheckpointSummary,
		Details: offered.CheckpointDetails, CreatedAt: updatedAt,
	}
	for _, item := range []continuity.Entry{handoffEntry, claimEntry, checkpoint} {
		if err := continuity.ValidateEntry(item); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
	}
	entryProof, err := authorizer.withMutationProof(context.Background(), "continuity.entry.append", "appended", "entry",
		handoffEntry.ID, task.ProjectID, 0, handoffEntry, continuity.EntryAppendAuthorizationReason, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	taskProof, err := authorizer.withMutationProof(context.Background(), "task.handoff.accept", "handoff_accepted", "task",
		task.ID, task.ProjectID, task.Revision, updatedTask, reason, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	claimProof, err := authorizer.withMutationProof(context.Background(), "task.claim.transfer", "transferred", "task",
		task.ID, task.ProjectID, currentClaim.Revision, claimEntry, continuity.EntryAppendAuthorizationReason, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	checkpointProof, err := authorizer.withMutationProof(context.Background(), "continuity.entry.append", "appended", "entry",
		checkpoint.ID, task.ProjectID, 0, checkpoint, continuity.EntryAppendAuthorizationReason, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	acceptance := store.TaskHandoffAcceptance{
		Task: updatedTask, HandoffEntry: handoffEntry, ClaimEntry: claimEntry, CheckpointEntry: checkpoint,
		Actor: actorName, Reason: reason,
	}
	if err := databaseStore.AcceptTaskHandoff(entryProof, taskProof, claimProof, checkpointProof, acceptance); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, continuity.TaskHandoffRead{
		SchemaVersion: continuity.TaskHandoffReadSchemaVersion, TaskID: task.ID,
		Handoffs: []continuity.TaskHandoff{accepted}, ExecutionAuthorized: false,
	}, stderr)
}

func readTaskHandoffs(args []string, one bool, stdout, stderr io.Writer) int {
	command := "list"
	if one {
		command = "show"
	}
	fs := flag.NewFlagSet("task handoff "+command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	taskID := fs.String("task-id", "", "task identifier")
	handoffID := fs.String("id", "", "handoff identifier (required for show)")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*taskID) == "" || fs.NArg() != 0 || (one && strings.TrimSpace(*handoffID) == "") || (!one && *handoffID != "") {
		_, _ = fmt.Fprintf(stderr, "usage: threadforge task handoff %s --task-id <id>%s [--db <path>]\n",
			command, map[bool]string{true: " --id <handoff-id>", false: ""}[one])
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	handoffs, err := databaseStore.TaskHandoffs(context.Background(), *taskID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if one {
		for _, handoff := range handoffs {
			if handoff.ID == *handoffID {
				handoffs = []continuity.TaskHandoff{handoff}
				return writeIndentedJSON(stdout, continuity.TaskHandoffRead{
					SchemaVersion: continuity.TaskHandoffReadSchemaVersion, TaskID: *taskID,
					Handoffs: handoffs, ExecutionAuthorized: false,
				}, stderr)
			}
		}
		_, _ = fmt.Fprintln(stderr, "task handoff not found")
		return 1
	}
	return writeIndentedJSON(stdout, continuity.TaskHandoffRead{
		SchemaVersion: continuity.TaskHandoffReadSchemaVersion, TaskID: *taskID,
		Handoffs: handoffs, ExecutionAuthorized: false,
	}, stderr)
}

func newTaskHandoffEntry(handoff continuity.TaskHandoff, action string, expectedRevision int,
	actor string, at time.Time) (continuity.Entry, error) {
	id, err := newTaskClaimEventID()
	if err != nil {
		return continuity.Entry{}, err
	}
	event := continuity.TaskHandoffEvent{
		SchemaVersion: continuity.TaskHandoffEventSchemaVersion, Action: action,
		ExpectedRevision: expectedRevision, Handoff: handoff,
	}
	return continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: id, ProjectID: handoff.ProjectID, TaskID: handoff.TaskID,
		Kind: continuity.EntryTaskHandoff, Actor: actor, Summary: "Task handoff " + action,
		TaskHandoff: &event, CreatedAt: at.UTC().Format(time.RFC3339Nano),
	}, nil
}

func newTaskClaimEntry(actor string, event continuity.TaskClaimEvent, summary string, at time.Time) (continuity.Entry, error) {
	id, err := newTaskClaimEventID()
	if err != nil {
		return continuity.Entry{}, err
	}
	return continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: id, ProjectID: event.Claim.ProjectID, TaskID: event.Claim.TaskID,
		Kind: continuity.EntryTaskClaim, Actor: actor, Summary: summary, TaskClaim: &event,
		CreatedAt: at.UTC().Format(time.RFC3339Nano),
	}, nil
}

func printTaskHandoffUsage(stderr io.Writer, action string) {
	if action == "draft" {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge task handoff draft --id <handoff-id> --task-id <task-id> --expected-task-revision <n> --claim-id <claim-id> --expected-claim-revision <n> --baseline-sha256 <sha256> --to-owner <subject> --checkpoint-entry-id <entry-id> --checkpoint-summary <text> [--checkpoint-details <text>] [--actor local-user] [--identity-key <private-key.json>] [--db <path>]")
		return
	}
	_, _ = fmt.Fprintf(stderr, "usage: threadforge task handoff %s --id <handoff-id> --task-id <task-id> --expected-revision <n> [--actor local-user] [--identity-key <private-key.json>] [--reason <text>] [--db <path>]\n", action)
}
