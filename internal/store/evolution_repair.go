package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/evolution"
)

type EvolutionRepairAudit struct {
	Sequence  int64  `json:"sequence"`
	RunID     string `json:"run_id"`
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

var evolutionRepairIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func (s *Store) RecordEvolutionRepairRun(ctx context.Context, run evolution.RepairRun) error {
	if err := validateEvolutionRepairRun(run); err != nil {
		return err
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	digest := sha256Hex(payload)
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var existingPayload, existingDigest, existingActor, existingReason string
		err := tx.QueryRowContext(ctx,
			`SELECT run_json, content_sha256, actor, reason FROM evolution_repair_runs WHERE id = ?`, run.ID).
			Scan(&existingPayload, &existingDigest, &existingActor, &existingReason)
		if err == nil {
			if existingDigest == digest && existingPayload == string(payload) && existingActor == run.Actor && existingReason == run.Reason {
				return nil
			}
			return ErrEvolutionRepairConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO evolution_repair_runs(id, project_id, domain, created_at, run_json, content_sha256, actor, reason)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, run.ProjectID, run.Result.Domain, run.CreatedAt, string(payload), digest, run.Actor, run.Reason); err != nil {
			return fmt.Errorf("record evolution repair run: %w", err)
		}
		for _, attempt := range run.Result.AttemptHistory {
			attemptJSON, err := json.Marshal(attempt)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO evolution_repair_attempts(run_id, attempt_number, attempt_json) VALUES(?, ?, ?)`,
				run.ID, attempt.Attempt, string(attemptJSON)); err != nil {
				return err
			}
			for position, edit := range attempt.EditMetadata {
				editJSON, err := json.Marshal(edit)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO evolution_repair_edits(run_id, attempt_number, position, edit_json) VALUES(?, ?, ?, ?)`,
					run.ID, attempt.Attempt, position, string(editJSON)); err != nil {
					return err
				}
			}
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO evolution_repair_audit(run_id, action, actor, reason, created_at) VALUES(?, 'repair_recorded', ?, ?, ?)`,
			run.ID, run.Actor, run.Reason, run.CreatedAt)
		return err
	})
}

func (s *Store) GetEvolutionRepairRun(ctx context.Context, id string) (evolution.RepairRun, error) {
	var rowID, projectID, domain, createdAt, payload, digest, actor, reason string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, domain, created_at, run_json, content_sha256, actor, reason FROM evolution_repair_runs WHERE id = ?`, id).
		Scan(&rowID, &projectID, &domain, &createdAt, &payload, &digest, &actor, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return evolution.RepairRun{}, ErrEvolutionRepairNotFound
	}
	if err != nil {
		return evolution.RepairRun{}, err
	}
	if !isSHA256Digest(digest) || sha256Hex([]byte(payload)) != digest {
		return evolution.RepairRun{}, errors.New("stored evolution repair digest mismatch")
	}
	var run evolution.RepairRun
	if err := json.Unmarshal([]byte(payload), &run); err != nil {
		return evolution.RepairRun{}, fmt.Errorf("decode stored evolution repair run: %w", err)
	}
	if run.ID != rowID || run.ProjectID != projectID || run.Result.Domain != domain || run.CreatedAt != createdAt || run.Actor != actor || run.Reason != reason {
		return evolution.RepairRun{}, errors.New("stored evolution repair columns do not match their snapshot")
	}
	if err := validateEvolutionRepairRun(run); err != nil {
		return evolution.RepairRun{}, fmt.Errorf("stored evolution repair run failed validation: %w", err)
	}
	if err := s.validateEvolutionRepairChildren(ctx, run); err != nil {
		return evolution.RepairRun{}, err
	}
	var auditCount, totalAuditCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM evolution_repair_audit WHERE run_id = ? AND action = 'repair_recorded' AND actor = ? AND reason = ? AND created_at = ?`,
		id, actor, reason, run.CreatedAt).Scan(&auditCount); err != nil {
		return evolution.RepairRun{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM evolution_repair_audit WHERE run_id = ?`, id).Scan(&totalAuditCount); err != nil {
		return evolution.RepairRun{}, err
	}
	if auditCount != 1 || totalAuditCount != 1 {
		return evolution.RepairRun{}, errors.New("stored evolution repair audit does not reconcile")
	}
	return run, nil
}

func (s *Store) ListEvolutionRepairRuns(ctx context.Context, projectID string, limit, offset int) ([]evolution.RepairRun, int, error) {
	if limit < 1 || limit > maxContinuityPage || offset < 0 {
		return nil, 0, errors.New("limit must be 1-200 and offset must be non-negative")
	}
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 {
		return nil, 0, errors.New("project ID is required and bounded")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM evolution_repair_runs WHERE project_id = ?`, projectID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM evolution_repair_runs WHERE project_id = ? ORDER BY created_at DESC, id LIMIT ? OFFSET ?`,
		projectID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	result := make([]evolution.RepairRun, 0, len(ids))
	for _, id := range ids {
		run, err := s.GetEvolutionRepairRun(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, run)
	}
	return result, total, nil
}

func (s *Store) EvolutionRepairHistory(ctx context.Context, id string) ([]EvolutionRepairAudit, error) {
	if _, err := s.GetEvolutionRepairRun(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT sequence, run_id, action, actor, reason, created_at FROM evolution_repair_audit WHERE run_id = ? ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]EvolutionRepairAudit, 0, 1)
	var previous int64
	for rows.Next() {
		var item EvolutionRepairAudit
		if err := rows.Scan(&item.Sequence, &item.RunID, &item.Action, &item.Actor, &item.Reason, &item.CreatedAt); err != nil {
			return nil, err
		}
		if item.Sequence <= previous || item.RunID != id || item.Action != "repair_recorded" || item.CreatedAt == "" {
			return nil, errors.New("evolution repair audit is inconsistent")
		}
		previous = item.Sequence
		if err := validateEvolutionAudit(item.Actor, item.Reason); err != nil {
			return nil, errors.New("evolution repair audit contains invalid metadata")
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) != 1 {
		return nil, ErrEvolutionRepairNotFound
	}
	return result, nil
}

func (s *Store) validateEvolutionRepairChildren(ctx context.Context, run evolution.RepairRun) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT attempt_number, attempt_json FROM evolution_repair_attempts WHERE run_id = ? ORDER BY attempt_number`, run.ID)
	if err != nil {
		return err
	}
	index := 0
	for rows.Next() {
		var number int
		var payload string
		if err := rows.Scan(&number, &payload); err != nil {
			_ = rows.Close()
			return err
		}
		if index >= len(run.Result.AttemptHistory) || number != index+1 {
			_ = rows.Close()
			return errors.New("stored evolution repair attempt history does not reconcile")
		}
		expected, err := json.Marshal(run.Result.AttemptHistory[index])
		if err != nil || payload != string(expected) {
			_ = rows.Close()
			return errors.New("stored evolution repair attempt differs from run snapshot")
		}
		index++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if index != len(run.Result.AttemptHistory) {
		return errors.New("stored evolution repair attempt history is incomplete")
	}
	for _, attempt := range run.Result.AttemptHistory {
		rows, err := s.db.QueryContext(ctx,
			`SELECT position, edit_json FROM evolution_repair_edits WHERE run_id = ? AND attempt_number = ? ORDER BY position`, run.ID, attempt.Attempt)
		if err != nil {
			return err
		}
		position := 0
		for rows.Next() {
			var storedPosition int
			var payload string
			if err := rows.Scan(&storedPosition, &payload); err != nil {
				_ = rows.Close()
				return err
			}
			if position >= len(attempt.EditMetadata) || storedPosition != position {
				_ = rows.Close()
				return errors.New("stored evolution repair edit history does not reconcile")
			}
			expected, err := json.Marshal(attempt.EditMetadata[position])
			if err != nil || payload != string(expected) {
				_ = rows.Close()
				return errors.New("stored evolution repair edit differs from run snapshot")
			}
			position++
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if position != len(attempt.EditMetadata) {
			return errors.New("stored evolution repair edit history is incomplete")
		}
	}
	return nil
}

func validateEvolutionRepairRun(run evolution.RepairRun) error {
	if !evolutionRepairIDPattern.MatchString(run.ID) || strings.TrimSpace(run.ProjectID) == "" || len(run.ProjectID) > 256 || len(run.CreatedAt) > 64 {
		return errors.New("evolution repair run requires stable ID, bounded project ID, and timestamp")
	}
	if _, err := time.Parse(time.RFC3339Nano, run.CreatedAt); err != nil {
		return errors.New("evolution repair timestamp must use RFC3339")
	}
	if err := validateEvolutionAudit(run.Actor, run.Reason); err != nil {
		return err
	}
	if err := evolution.ValidateRepairResult(run.Result, run.Result.Domain); err != nil {
		return err
	}
	encoded, err := json.Marshal(run)
	if err != nil || len(encoded) > 512<<10 {
		return errors.New("evolution repair run exceeds the 512 KiB limit")
	}
	return nil
}
