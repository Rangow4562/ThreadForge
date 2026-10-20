package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/continuity"
)

const maxTaskClaimEvents = 100_000

type taskClaimQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// TaskClaims returns the latest append-only local claim state for one task.
// Expired and stale claims are retained: expiry never transfers ownership.
func (s *Store) TaskClaims(ctx context.Context, taskID string) ([]continuity.TaskClaim, error) {
	task, err := readTask(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	claims, err := readTaskClaims(ctx, s.db, task.ProjectID)
	if err != nil {
		return nil, err
	}
	result := make([]continuity.TaskClaim, 0)
	for _, claim := range claims {
		if claim.TaskID == taskID {
			result = append(result, claim)
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

func readTaskClaims(ctx context.Context, q taskClaimQueryer, projectID string) (map[string]continuity.TaskClaim, error) {
	claims, _, err := readTaskClaimHistory(ctx, q, projectID)
	return claims, err
}

func readTaskClaimHistory(ctx context.Context, q taskClaimQueryer, projectID string) (map[string]continuity.TaskClaim,
	map[taskClaimTransferKey]continuity.TaskClaim, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT e.id, e.task_id, e.actor, e.created_at, e.entry_json, t.project_id
		 FROM continuity_entries e JOIN continuity_tasks t ON t.id = e.task_id
		 WHERE e.project_id = ? AND e.kind = ? ORDER BY e.sequence LIMIT ?`,
		projectID, continuity.EntryTaskClaim, maxTaskClaimEvents+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	claims := make(map[string]continuity.TaskClaim)
	transfers := make(map[taskClaimTransferKey]continuity.TaskClaim)
	count := 0
	for rows.Next() {
		count++
		if count > maxTaskClaimEvents {
			return nil, nil, errors.New("task claim history exceeds the bounded replay limit")
		}
		var entryID, taskID, actor, createdAt, payload, taskProjectID string
		if err := rows.Scan(&entryID, &taskID, &actor, &createdAt, &payload, &taskProjectID); err != nil {
			return nil, nil, err
		}
		if taskProjectID != projectID {
			return nil, nil, errors.New("stored task claim crosses its project boundary")
		}
		var entry continuity.Entry
		if err := decodeStrictJSON(payload, &entry); err != nil {
			return nil, nil, fmt.Errorf("decode task claim history: %w", err)
		}
		if err := continuity.ValidateEntry(entry); err != nil || entry.Kind != continuity.EntryTaskClaim ||
			entry.ID != entryID || entry.TaskID != taskID || entry.Actor != actor || entry.CreatedAt != createdAt || entry.TaskClaim == nil {
			return nil, nil, errors.New("stored task claim history is invalid")
		}
		if err := applyTaskClaimEvent(claims, entry); err != nil {
			return nil, nil, fmt.Errorf("stored task claim history is inconsistent: %w", err)
		}
		if entry.TaskClaim.Action == "transferred" {
			claim := entry.TaskClaim.Claim
			transfers[taskClaimTransferKey{projectID: projectID, taskID: taskID, claimID: claim.ID, revision: claim.Revision}] = claim
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return claims, transfers, nil
}

func applyTaskClaimEvent(claims map[string]continuity.TaskClaim, entry continuity.Entry) error {
	event := entry.TaskClaim
	if event == nil {
		return errors.New("task claim event is missing")
	}
	claim := event.Claim
	current, exists := claims[claim.ID]
	switch event.Action {
	case "acquired":
		if exists {
			return errors.New("claim identifier was already used")
		}
		for _, other := range claims {
			if other.Status != "active" {
				continue
			}
			if other.TaskID == claim.TaskID {
				return errors.New("task already has an active claim")
			}
			if taskClaimScopesOverlap(other.Scopes, claim.Scopes) {
				return errors.New("claim scope overlaps an existing active claim")
			}
		}
	case "renewed", "revalidated", "released", "transferred":
		if !exists || current.Status != "active" || current.Revision != event.ExpectedRevision {
			return errors.New("claim update does not match the latest active revision")
		}
		if claim.ID != current.ID || claim.ProjectID != current.ProjectID || claim.TaskID != current.TaskID ||
			claim.CreatedAt != current.CreatedAt || !sameTaskClaimScopes(claim.Scopes, current.Scopes) {
			return errors.New("claim identity, project, task, or scopes changed")
		}
		if !taskClaimLeaseExtends(claim.UpdatedAt, current.UpdatedAt) {
			return errors.New("claim event timestamp must advance monotonically")
		}
		if event.Action == "transferred" {
			leaseExpiresAt, leaseErr := time.Parse(time.RFC3339Nano, claim.LeaseExpiresAt)
			transferredAt, transferErr := time.Parse(time.RFC3339Nano, claim.UpdatedAt)
			if claim.Owner == current.Owner || claim.TaskRevision != current.TaskRevision+1 ||
				claim.BaselineSHA256 != current.BaselineSHA256 || claim.LeaseExpiresAt != current.LeaseExpiresAt ||
				leaseErr != nil || transferErr != nil || !transferredAt.Before(leaseExpiresAt) {
				return errors.New("claim transfer must change owner and advance task revision without changing baseline or lease")
			}
		} else if claim.Owner != current.Owner {
			return errors.New("only a handoff transfer may change claim owner")
		}
		switch event.Action {
		case "renewed":
			if claim.TaskRevision != current.TaskRevision || claim.BaselineSHA256 != current.BaselineSHA256 ||
				!taskClaimLeaseExtends(claim.LeaseExpiresAt, current.LeaseExpiresAt) {
				return errors.New("renewal changed the task baseline or failed to extend the lease")
			}
		case "revalidated":
			if !taskClaimLeaseExtends(claim.LeaseExpiresAt, current.LeaseExpiresAt) ||
				(claim.TaskRevision == current.TaskRevision && claim.BaselineSHA256 == current.BaselineSHA256) {
				return errors.New("revalidation must refresh the lease and change a task or code baseline")
			}
		case "released":
			if claim.TaskRevision != current.TaskRevision || claim.BaselineSHA256 != current.BaselineSHA256 ||
				claim.LeaseExpiresAt != current.LeaseExpiresAt {
				return errors.New("release changed the claim baseline or lease")
			}
		}
	default:
		return errors.New("unsupported task claim action")
	}
	claims[claim.ID] = claim
	return nil
}

func sameTaskClaimScopes(a, b []continuity.TaskClaimScope) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func taskClaimScopesOverlap(a, b []continuity.TaskClaimScope) bool {
	for _, left := range a {
		for _, right := range b {
			if left.Kind != right.Kind {
				continue
			}
			if left.Kind != continuity.TaskClaimScopePath {
				if left.Value == right.Value {
					return true
				}
				continue
			}
			if taskClaimPathsOverlap(left.Value, right.Value) {
				return true
			}
		}
	}
	return false
}

func taskClaimPathsOverlap(a, b string) bool {
	base := func(value string) (string, bool) {
		if strings.HasSuffix(value, "/**") {
			return strings.TrimSuffix(value, "/**"), true
		}
		return value, false
	}
	aBase, aTree := base(a)
	bBase, bTree := base(b)
	if aBase == bBase {
		return true
	}
	if aTree && (strings.HasPrefix(bBase, aBase+"/")) {
		return true
	}
	if bTree && strings.HasPrefix(aBase, bBase+"/") {
		return true
	}
	return false
}

func taskClaimLeaseExtends(candidate, current string) bool {
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	return candidateErr == nil && currentErr == nil && candidateTime.After(currentTime)
}

func taskClaimAction(action string) (string, bool) {
	switch action {
	case "acquired":
		return "task.claim.acquire", true
	case "renewed":
		return "task.claim.renew", true
	case "revalidated":
		return "task.claim.revalidate", true
	case "released":
		return "task.claim.release", true
	case "transferred":
		return "task.claim.transfer", true
	default:
		return "", false
	}
}

func (s *Store) validateTaskClaimMutation(ctx context.Context, tx *sql.Tx, entry continuity.Entry,
	task continuity.Task) error {
	event := entry.TaskClaim
	if event == nil {
		return errors.New("task claim event is required")
	}
	claim := event.Claim
	if event.Action == "acquired" && (task.Status == continuity.TaskCompleted || task.Status == continuity.TaskAbandoned) {
		return errors.New("terminal tasks cannot acquire a claim")
	}
	if event.Action == "transferred" {
		return errors.New("claim transfer requires the atomic handoff acceptance operation")
	}
	if claim.TaskRevision != task.Revision && event.Action != "released" {
		return errors.New("task claim must bind the current task revision; stale claims require revalidation")
	}
	claims, err := readTaskClaims(ctx, tx, task.ProjectID)
	if err != nil {
		return err
	}
	return applyTaskClaimEvent(claims, entry)
}
