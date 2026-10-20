package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/continuity"
)

const maxTaskHandoffEvents = 100_000

type taskHandoffQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type TaskHandoffAcceptance struct {
	Task            continuity.Task
	HandoffEntry    continuity.Entry
	ClaimEntry      continuity.Entry
	CheckpointEntry continuity.Entry
	Actor           string
	Reason          string
}

// TaskHandoffs returns replayed local handoff state for one task.
func (s *Store) TaskHandoffs(ctx context.Context, taskID string) ([]continuity.TaskHandoff, error) {
	task, err := readTask(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	handoffs, err := readTaskHandoffs(ctx, s.db, task.ProjectID)
	if err != nil {
		return nil, err
	}
	result := make([]continuity.TaskHandoff, 0)
	for _, handoff := range handoffs {
		if handoff.TaskID == taskID {
			result = append(result, handoff)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt == result[j].CreatedAt {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result, nil
}

func readTaskHandoffs(ctx context.Context, q taskHandoffQueryer, projectID string) (map[string]continuity.TaskHandoff, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT e.id, e.task_id, e.actor, e.created_at, e.entry_json, t.project_id
		 FROM continuity_entries e JOIN continuity_tasks t ON t.id = e.task_id
		 WHERE e.project_id = ? AND e.kind = ? ORDER BY e.sequence LIMIT ?`,
		projectID, continuity.EntryTaskHandoff, maxTaskHandoffEvents+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	handoffs := make(map[string]continuity.TaskHandoff)
	count := 0
	for rows.Next() {
		count++
		if count > maxTaskHandoffEvents {
			return nil, errors.New("task handoff history exceeds the bounded replay limit")
		}
		var entryID, taskID, actor, createdAt, payload, taskProjectID string
		if err := rows.Scan(&entryID, &taskID, &actor, &createdAt, &payload, &taskProjectID); err != nil {
			return nil, err
		}
		if taskProjectID != projectID {
			return nil, errors.New("stored task handoff crosses its project boundary")
		}
		var entry continuity.Entry
		if err := decodeStrictJSON(payload, &entry); err != nil {
			return nil, fmt.Errorf("decode task handoff history: %w", err)
		}
		if err := continuity.ValidateEntry(entry); err != nil || entry.Kind != continuity.EntryTaskHandoff ||
			entry.ID != entryID || entry.TaskID != taskID || entry.Actor != actor || entry.CreatedAt != createdAt || entry.TaskHandoff == nil {
			return nil, errors.New("stored task handoff history is invalid")
		}
		if err := applyTaskHandoffEvent(handoffs, entry); err != nil {
			return nil, fmt.Errorf("stored task handoff history is inconsistent: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	accepted := make([]continuity.TaskHandoff, 0)
	for _, handoff := range handoffs {
		if handoff.Status == continuity.TaskHandoffAccepted {
			accepted = append(accepted, handoff)
		}
	}
	if len(accepted) > 0 {
		_, transfers, err := readTaskClaimHistory(ctx, q, projectID)
		if err != nil {
			return nil, err
		}
		for _, handoff := range accepted {
			if err := verifyTaskHandoffTransferEvidence(ctx, q, handoff, transfers); err != nil {
				return nil, err
			}
		}
	}
	return handoffs, nil
}

func verifyTaskHandoffTransferEvidence(ctx context.Context, q taskHandoffQueryer, handoff continuity.TaskHandoff,
	claimTransfers map[taskClaimTransferKey]continuity.TaskClaim) error {
	var taskPayload, taskAction, taskActor, taskCreatedAt string
	if err := q.QueryRowContext(ctx,
		`SELECT task_json, action, actor, created_at FROM continuity_task_history WHERE task_id = ? AND revision = ?`,
		handoff.TaskID, handoff.TransferredTaskRevision).Scan(&taskPayload, &taskAction, &taskActor, &taskCreatedAt); err != nil {
		return errors.New("accepted handoff has no matching task-head history")
	}
	var task continuity.Task
	if decodeStrictJSON(taskPayload, &task) != nil || continuity.ValidateTask(task) != nil || taskAction != "handoff_accepted" ||
		taskActor != handoff.ToOwner || taskCreatedAt != handoff.UpdatedAt || task.ProjectID != handoff.ProjectID ||
		task.Owner != handoff.ToOwner || task.Revision != handoff.TransferredTaskRevision {
		return errors.New("accepted handoff task-head history does not match its transfer")
	}
	var checkpointPayload string
	if err := q.QueryRowContext(ctx,
		`SELECT entry_json FROM continuity_entries WHERE id = ? AND project_id = ? AND task_id = ?`,
		handoff.CheckpointEntryID, handoff.ProjectID, handoff.TaskID).Scan(&checkpointPayload); err != nil {
		return errors.New("accepted handoff checkpoint is missing")
	}
	var checkpoint continuity.Entry
	if decodeStrictJSON(checkpointPayload, &checkpoint) != nil || continuity.ValidateEntry(checkpoint) != nil ||
		checkpoint.Kind != continuity.EntryCheckpoint || checkpoint.Actor != handoff.ToOwner || checkpoint.CreatedAt != handoff.UpdatedAt ||
		checkpoint.Summary != handoff.CheckpointSummary || checkpoint.Details != handoff.CheckpointDetails {
		return errors.New("accepted handoff checkpoint does not match the offered checkpoint")
	}
	claim, exists := claimTransfers[taskClaimTransferKey{
		projectID: handoff.ProjectID, taskID: handoff.TaskID, claimID: handoff.ClaimID, revision: handoff.TransferredClaimRevision,
	}]
	if !exists || claim.Owner != handoff.ToOwner || claim.TaskRevision != handoff.TransferredTaskRevision ||
		claim.BaselineSHA256 != handoff.BaselineSHA256 {
		return errors.New("accepted handoff has no matching transferred claim revision")
	}
	return nil
}

func applyTaskHandoffEvent(handoffs map[string]continuity.TaskHandoff, entry continuity.Entry) error {
	if entry.TaskHandoff == nil {
		return errors.New("task handoff event is missing")
	}
	event := entry.TaskHandoff
	next := event.Handoff
	current, exists := handoffs[next.ID]
	switch event.Action {
	case "drafted":
		if exists {
			return errors.New("handoff identifier was already used")
		}
		for _, other := range handoffs {
			if other.TaskID == next.TaskID && (other.Status == continuity.TaskHandoffDraft || other.Status == continuity.TaskHandoffOffered) {
				return errors.New("task already has a pending handoff")
			}
		}
	case "offered":
		if !exists || current.Status != continuity.TaskHandoffDraft || current.Revision != event.ExpectedRevision ||
			!sameTaskHandoffIdentity(current, next) || !handoffTimestampAdvances(next.UpdatedAt, current.UpdatedAt) {
			return errors.New("offer does not continue the current draft revision")
		}
	case "accepted", "rejected":
		if !exists || current.Status != continuity.TaskHandoffOffered || current.Revision != event.ExpectedRevision ||
			!sameTaskHandoffIdentity(current, next) || !handoffTimestampAdvances(next.UpdatedAt, current.UpdatedAt) {
			return errors.New("decision does not continue the current offered revision")
		}
	case "cancelled":
		if !exists || (current.Status != continuity.TaskHandoffDraft && current.Status != continuity.TaskHandoffOffered) ||
			current.Revision != event.ExpectedRevision || !sameTaskHandoffIdentity(current, next) ||
			!handoffTimestampAdvances(next.UpdatedAt, current.UpdatedAt) {
			return errors.New("cancellation does not continue a pending handoff revision")
		}
	default:
		return errors.New("unsupported task handoff action")
	}
	handoffs[next.ID] = next
	return nil
}

func sameTaskHandoffIdentity(a, b continuity.TaskHandoff) bool {
	a.Status, b.Status = "", ""
	a.Revision, b.Revision = 0, 0
	a.UpdatedAt, b.UpdatedAt = "", ""
	a.TransferredTaskRevision, b.TransferredTaskRevision = 0, 0
	a.TransferredClaimRevision, b.TransferredClaimRevision = 0, 0
	return reflect.DeepEqual(a, b)
}

func handoffTimestampAdvances(next, previous string) bool {
	nextTime, nextErr := time.Parse(time.RFC3339Nano, next)
	previousTime, previousErr := time.Parse(time.RFC3339Nano, previous)
	return nextErr == nil && previousErr == nil && nextTime.After(previousTime)
}

func (s *Store) validateTaskHandoffMutation(ctx context.Context, tx *sql.Tx, entry continuity.Entry, task continuity.Task) error {
	if entry.TaskHandoff == nil {
		return errors.New("task handoff event is required")
	}
	event := entry.TaskHandoff
	if event.Action == "accepted" {
		return errors.New("handoff acceptance requires the atomic ownership-transfer operation")
	}
	handoffs, err := readTaskHandoffs(ctx, tx, task.ProjectID)
	if err != nil {
		return err
	}
	if err := applyTaskHandoffEvent(handoffs, entry); err != nil {
		return err
	}
	if event.Action == "drafted" || event.Action == "offered" {
		handoff := event.Handoff
		if task.Owner == "" || task.Owner != handoff.FromOwner || task.Revision != handoff.TaskRevision ||
			task.Status == continuity.TaskCompleted || task.Status == continuity.TaskAbandoned {
			return errors.New("handoff requires the current non-terminal task owner and revision")
		}
		claims, err := readTaskClaims(ctx, tx, task.ProjectID)
		if err != nil {
			return err
		}
		claim, ok := claims[handoff.ClaimID]
		if !ok || claim.Status != "active" || claim.Revision != handoff.ClaimRevision || claim.TaskID != task.ID ||
			claim.Owner != handoff.FromOwner || claim.TaskRevision != task.Revision || claim.BaselineSHA256 != handoff.BaselineSHA256 {
			return errors.New("handoff must bind the current active owner claim, task revision, and baseline")
		}
		expires, parseErr := time.Parse(time.RFC3339Nano, claim.LeaseExpiresAt)
		if parseErr != nil || !time.Now().UTC().Before(expires) {
			return errors.New("expired claims cannot be offered for handoff")
		}
	}
	return nil
}

// AcceptTaskHandoff changes the task owner, transfers its active claim, records
// the accepted checkpoint, and appends the handoff decision in one SQLite transaction.
func (s *Store) AcceptTaskHandoff(handoffContext, taskContext, claimContext, checkpointContext context.Context,
	acceptance TaskHandoffAcceptance) error {
	if err := validateContinuityAudit(acceptance.Actor, acceptance.Reason); err != nil {
		return err
	}
	for _, entry := range []continuity.Entry{acceptance.HandoffEntry, acceptance.ClaimEntry, acceptance.CheckpointEntry} {
		if err := continuity.ValidateEntry(entry); err != nil {
			return err
		}
		if len(entry.Authorization) != 0 || entry.ProjectID != acceptance.Task.ProjectID || entry.TaskID != acceptance.Task.ID ||
			entry.Actor != acceptance.Actor || entry.SessionID != "" {
			return errors.New("atomic handoff entries must share the task, project, actor, and verified authorization contexts")
		}
	}
	if err := continuity.ValidateTask(acceptance.Task); err != nil {
		return err
	}
	if err := continuity.ValidateEntry(acceptance.HandoffEntry); err != nil || acceptance.HandoffEntry.Kind != continuity.EntryTaskHandoff ||
		acceptance.HandoffEntry.TaskHandoff == nil || acceptance.HandoffEntry.TaskHandoff.Action != "accepted" {
		return errors.New("accepted handoff entry is invalid")
	}
	if err := continuity.ValidateEntry(acceptance.ClaimEntry); err != nil || acceptance.ClaimEntry.Kind != continuity.EntryTaskClaim ||
		acceptance.ClaimEntry.TaskClaim == nil || acceptance.ClaimEntry.TaskClaim.Action != "transferred" {
		return errors.New("transferred claim entry is invalid")
	}
	if acceptance.CheckpointEntry.Kind != continuity.EntryCheckpoint {
		return errors.New("handoff checkpoint entry is invalid")
	}
	return s.withTx(handoffContext, func(tx *sql.Tx) error {
		currentTask, err := readTask(handoffContext, tx, acceptance.Task.ID)
		if err != nil {
			return err
		}
		handoff := acceptance.HandoffEntry.TaskHandoff.Handoff
		if currentTask.ProjectID != handoff.ProjectID || currentTask.Owner != handoff.FromOwner || currentTask.Revision != handoff.TaskRevision {
			return ErrContinuityConflict
		}
		states, err := readTaskHandoffs(handoffContext, tx, currentTask.ProjectID)
		if err != nil {
			return err
		}
		if err := applyTaskHandoffEvent(states, acceptance.HandoffEntry); err != nil {
			return err
		}
		currentHandoff, exists := states[handoff.ID]
		if !exists || currentHandoff.Status != continuity.TaskHandoffAccepted {
			return errors.New("accepted handoff state is missing")
		}
		claims, err := readTaskClaims(handoffContext, tx, currentTask.ProjectID)
		if err != nil {
			return err
		}
		currentClaim, exists := claims[handoff.ClaimID]
		if !exists || currentClaim.Status != "active" || currentClaim.Revision != handoff.ClaimRevision ||
			currentClaim.Owner != handoff.FromOwner || currentClaim.TaskID != currentTask.ID ||
			currentClaim.TaskRevision != currentTask.Revision || currentClaim.BaselineSHA256 != handoff.BaselineSHA256 {
			return ErrContinuityConflict
		}
		now := time.Now().UTC()
		leaseExpiry, parseErr := time.Parse(time.RFC3339Nano, currentClaim.LeaseExpiresAt)
		if parseErr != nil || !now.Before(leaseExpiry) {
			return errors.New("expired claims cannot be transferred")
		}
		task := acceptance.Task
		expectedTask := currentTask
		expectedTask.Owner = handoff.ToOwner
		expectedTask.Revision++
		expectedTask.UpdatedAt = handoff.UpdatedAt
		if !reflect.DeepEqual(task, expectedTask) || task.Revision != handoff.TransferredTaskRevision ||
			task.Status == continuity.TaskCompleted || task.Status == continuity.TaskAbandoned {
			return errors.New("handoff may only change owner, revision, and update time on a non-terminal task")
		}
		claimEvent := acceptance.ClaimEntry.TaskClaim
		transferredClaim := claimEvent.Claim
		expectedClaim := currentClaim
		expectedClaim.Owner = handoff.ToOwner
		expectedClaim.TaskRevision = task.Revision
		expectedClaim.Revision++
		expectedClaim.UpdatedAt = handoff.UpdatedAt
		if transferredClaim.ID != expectedClaim.ID || transferredClaim.ProjectID != expectedClaim.ProjectID ||
			transferredClaim.TaskID != expectedClaim.TaskID || transferredClaim.Owner != expectedClaim.Owner ||
			!sameTaskClaimScopes(transferredClaim.Scopes, expectedClaim.Scopes) || transferredClaim.TaskRevision != expectedClaim.TaskRevision ||
			transferredClaim.BaselineSHA256 != expectedClaim.BaselineSHA256 || transferredClaim.Status != "active" ||
			transferredClaim.Revision != expectedClaim.Revision || transferredClaim.CreatedAt != expectedClaim.CreatedAt ||
			transferredClaim.UpdatedAt != expectedClaim.UpdatedAt || transferredClaim.ReleasedAt != "" ||
			transferredClaim.LeaseExpiresAt != currentClaim.LeaseExpiresAt {
			return errors.New("claim transfer must preserve scope, baseline, and lease while advancing owner, task and claim revisions")
		}
		checkpoint := acceptance.CheckpointEntry
		if checkpoint.ID != handoff.CheckpointEntryID || checkpoint.Summary != handoff.CheckpointSummary ||
			checkpoint.Details != handoff.CheckpointDetails || checkpoint.CreatedAt != handoff.UpdatedAt ||
			checkpoint.Result != "" || len(checkpoint.Evidence) != 0 {
			return errors.New("checkpoint does not match the offered handoff snapshot")
		}
		if acceptance.HandoffEntry.CreatedAt != handoff.UpdatedAt || acceptance.ClaimEntry.CreatedAt != handoff.UpdatedAt ||
			checkpoint.CreatedAt != handoff.UpdatedAt || task.UpdatedAt != handoff.UpdatedAt {
			return errors.New("atomic handoff records must use one update timestamp")
		}
		if err := validateTaskClaimEventForTransfer(*claimEvent, currentClaim); err != nil {
			return err
		}
		handoffAuth, err := s.authorizeContinuityMutation(handoffContext, tx, "continuity.entry.append", "appended", "entry",
			acceptance.HandoffEntry.ID, handoff.ProjectID, 0, acceptance.HandoffEntry, acceptance.Actor, continuity.EntryAppendAuthorizationReason)
		if err != nil {
			return err
		}
		taskAuth, err := s.authorizeContinuityMutation(taskContext, tx, "task.handoff.accept", "handoff_accepted", "task",
			task.ID, task.ProjectID, currentTask.Revision, task, acceptance.Actor, acceptance.Reason)
		if err != nil {
			return err
		}
		claimAuth, err := s.authorizeContinuityMutation(claimContext, tx, "task.claim.transfer", "transferred", "task",
			task.ID, task.ProjectID, currentClaim.Revision, acceptance.ClaimEntry, acceptance.Actor, continuity.EntryAppendAuthorizationReason)
		if err != nil {
			return err
		}
		checkpointAuth, err := s.authorizeContinuityMutation(checkpointContext, tx, "continuity.entry.append", "appended", "entry",
			checkpoint.ID, task.ProjectID, 0, checkpoint, acceptance.Actor, continuity.EntryAppendAuthorizationReason)
		if err != nil {
			return err
		}
		if err := applyTaskClaimEvent(claims, acceptance.ClaimEntry); err != nil {
			return err
		}
		payload, err := json.Marshal(task)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(handoffContext,
			`UPDATE continuity_tasks SET revision = ?, status = ?, task_json = ?, updated_at = ? WHERE id = ? AND revision = ?`,
			task.Revision, task.Status, string(payload), task.UpdatedAt, task.ID, currentTask.Revision)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrContinuityConflict
		}
		if err := insertTaskHistory(handoffContext, tx, task, "handoff_accepted", acceptance.Actor, acceptance.Reason); err != nil {
			return err
		}
		if err := insertContinuityAudit(handoffContext, tx, "task", task.ID, task.Revision, "handoff_accepted", acceptance.Actor,
			acceptance.Reason, task.UpdatedAt, taskAuth); err != nil {
			return err
		}
		acceptance.HandoffEntry.Authorization = handoffAuth
		acceptance.ClaimEntry.Authorization = claimAuth
		checkpoint.Authorization = checkpointAuth
		for _, entry := range []continuity.Entry{acceptance.ClaimEntry, checkpoint, acceptance.HandoffEntry} {
			if err := insertContinuityEntry(handoffContext, tx, entry); err != nil {
				return err
			}
		}
		return nil
	})
}

func validateTaskClaimEventForTransfer(event continuity.TaskClaimEvent, current continuity.TaskClaim) error {
	if err := continuity.ValidateTaskClaimEvent(event); err != nil {
		return err
	}
	if event.Action != "transferred" || event.ExpectedRevision != current.Revision || event.Claim.ID != current.ID ||
		event.Claim.ProjectID != current.ProjectID || event.Claim.TaskID != current.TaskID ||
		event.Claim.Revision != current.Revision+1 {
		return errors.New("claim transfer does not match the current claim revision")
	}
	return nil
}

func insertContinuityEntry(ctx context.Context, tx *sql.Tx, entry continuity.Entry) error {
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO continuity_entries(id, project_id, task_id, session_id, kind, actor, created_at, entry_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.ProjectID, entry.TaskID, entry.SessionID, entry.Kind, entry.Actor, entry.CreatedAt, string(payload))
	if err != nil {
		return fmt.Errorf("append continuity entry: %w", err)
	}
	return nil
}
