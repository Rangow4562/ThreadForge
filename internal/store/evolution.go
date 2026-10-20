package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/evolution"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

type EvolutionRoundAudit struct {
	Sequence  int64  `json:"sequence"`
	RoundID   string `json:"round_id"`
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) RecordEvolutionRound(ctx context.Context, round evolution.Round, actor, reason string) error {
	if err := evolution.ValidateRound(round); err != nil {
		return err
	}
	if err := validateEvolutionAudit(actor, reason); err != nil {
		return err
	}
	payload, err := json.Marshal(round)
	if err != nil {
		return err
	}
	digest := sha256Hex(payload)
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var existingPayload, existingDigest, existingActor, existingReason string
		err := tx.QueryRowContext(ctx,
			`SELECT round_json, content_sha256, actor, reason FROM evolution_rounds WHERE id = ?`, round.ID).
			Scan(&existingPayload, &existingDigest, &existingActor, &existingReason)
		if err == nil {
			if existingDigest == digest && existingPayload == string(payload) && existingActor == actor && existingReason == reason {
				return nil
			}
			return ErrEvolutionRoundConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO evolution_rounds(id, project_id, domain, created_at, round_json, content_sha256, actor, reason)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			round.ID, round.ProjectID, round.Domain, round.CreatedAt, string(payload), digest, actor, reason); err != nil {
			return fmt.Errorf("record evolution round: %w", err)
		}
		for position, candidate := range round.Candidates {
			candidateJSON, err := json.Marshal(candidate)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO evolution_candidate_history(round_id, candidate_id, position, candidate_json) VALUES(?, ?, ?, ?)`,
				round.ID, candidate.ID, position, string(candidateJSON)); err != nil {
				return err
			}
			for editPosition, edit := range candidate.Edits {
				editJSON, err := json.Marshal(edit)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO evolution_edit_history(round_id, candidate_id, position, edit_json) VALUES(?, ?, ?, ?)`,
					round.ID, candidate.ID, editPosition, string(editJSON)); err != nil {
					return err
				}
			}
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO evolution_round_audit(round_id, action, actor, reason, created_at) VALUES(?, 'selection_recorded', ?, ?, ?)`,
			round.ID, actor, reason, round.CreatedAt)
		return err
	})
}

func (s *Store) GetEvolutionRound(ctx context.Context, id string) (evolution.Round, error) {
	return readEvolutionRound(ctx, s.db, id)
}

func (s *Store) ListEvolutionRounds(ctx context.Context, projectID string, limit, offset int) ([]evolution.Round, int, error) {
	if limit < 1 || limit > maxContinuityPage || offset < 0 {
		return nil, 0, errors.New("limit must be 1-200 and offset must be non-negative")
	}
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 {
		return nil, 0, errors.New("project ID is required and bounded")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM evolution_rounds WHERE project_id = ?`, projectID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM evolution_rounds WHERE project_id = ? ORDER BY created_at DESC, id LIMIT ? OFFSET ?`,
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
	result := make([]evolution.Round, 0, len(ids))
	for _, id := range ids {
		round, err := readEvolutionRound(ctx, s.db, id)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, round)
	}
	return result, total, nil
}

func (s *Store) EvolutionRoundHistory(ctx context.Context, id string) ([]EvolutionRoundAudit, error) {
	if _, err := readEvolutionRound(ctx, s.db, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT sequence, round_id, action, actor, reason, created_at FROM evolution_round_audit WHERE round_id = ? ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]EvolutionRoundAudit, 0, 1)
	var previous int64
	for rows.Next() {
		var item EvolutionRoundAudit
		if err := rows.Scan(&item.Sequence, &item.RoundID, &item.Action, &item.Actor, &item.Reason, &item.CreatedAt); err != nil {
			return nil, err
		}
		if item.Sequence <= previous || item.RoundID != id || item.Action != "selection_recorded" || item.CreatedAt == "" {
			return nil, errors.New("evolution round audit is inconsistent")
		}
		previous = item.Sequence
		if err := validateEvolutionAudit(item.Actor, item.Reason); err != nil {
			return nil, errors.New("evolution round audit contains invalid metadata")
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) != 1 {
		return nil, ErrEvolutionRoundNotFound
	}
	return result, nil
}

func readEvolutionRound(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) (evolution.Round, error) {
	var rowID, projectID, domain, createdAt, payload, digest, actor, reason string
	err := queryer.QueryRowContext(ctx,
		`SELECT id, project_id, domain, created_at, round_json, content_sha256, actor, reason FROM evolution_rounds WHERE id = ?`, id).
		Scan(&rowID, &projectID, &domain, &createdAt, &payload, &digest, &actor, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return evolution.Round{}, ErrEvolutionRoundNotFound
	}
	if err != nil {
		return evolution.Round{}, err
	}
	if !isSHA256Digest(digest) || sha256Hex([]byte(payload)) != digest {
		return evolution.Round{}, errors.New("stored evolution round digest mismatch")
	}
	var round evolution.Round
	if err := json.Unmarshal([]byte(payload), &round); err != nil {
		return evolution.Round{}, fmt.Errorf("decode stored evolution round: %w", err)
	}
	if round.ID != rowID || round.ProjectID != projectID || round.Domain != domain || round.CreatedAt != createdAt {
		return evolution.Round{}, errors.New("stored evolution round columns do not match its snapshot")
	}
	if err := evolution.ValidateRound(round); err != nil {
		return evolution.Round{}, fmt.Errorf("stored evolution round failed validation: %w", err)
	}
	if err := validateEvolutionAudit(actor, reason); err != nil {
		return evolution.Round{}, errors.New("stored evolution round has invalid audit metadata")
	}
	var auditCount, totalAuditCount int
	if err := queryer.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM evolution_round_audit WHERE round_id = ? AND action = 'selection_recorded' AND actor = ? AND reason = ? AND created_at = ?`,
		id, actor, reason, round.CreatedAt).Scan(&auditCount); err != nil {
		return evolution.Round{}, err
	}
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM evolution_round_audit WHERE round_id = ?`, id).Scan(&totalAuditCount); err != nil {
		return evolution.Round{}, err
	}
	if auditCount != 1 || totalAuditCount != 1 {
		return evolution.Round{}, errors.New("stored evolution round audit does not reconcile")
	}
	if err := validateEvolutionChildren(ctx, queryer, round); err != nil {
		return evolution.Round{}, err
	}
	return round, nil
}

func validateEvolutionChildren(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, round evolution.Round) error {
	rows, err := queryer.QueryContext(ctx,
		`SELECT candidate_id, position, candidate_json FROM evolution_candidate_history WHERE round_id = ? ORDER BY position`, round.ID)
	if err != nil {
		return err
	}
	position := 0
	for rows.Next() {
		var id, payload string
		var storedPosition int
		if err := rows.Scan(&id, &storedPosition, &payload); err != nil {
			_ = rows.Close()
			return err
		}
		if position >= len(round.Candidates) || storedPosition != position || id != round.Candidates[position].ID {
			_ = rows.Close()
			return errors.New("stored evolution candidate history does not reconcile")
		}
		expected, err := json.Marshal(round.Candidates[position])
		if err != nil || payload != string(expected) {
			_ = rows.Close()
			return errors.New("stored evolution candidate snapshot differs from round")
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
	if position != len(round.Candidates) {
		return errors.New("stored evolution candidate history is incomplete")
	}
	for _, candidate := range round.Candidates {
		rows, err := queryer.QueryContext(ctx,
			`SELECT position, edit_json FROM evolution_edit_history WHERE round_id = ? AND candidate_id = ? ORDER BY position`,
			round.ID, candidate.ID)
		if err != nil {
			return err
		}
		editPosition := 0
		for rows.Next() {
			var storedPosition int
			var payload string
			if err := rows.Scan(&storedPosition, &payload); err != nil {
				_ = rows.Close()
				return err
			}
			if editPosition >= len(candidate.Edits) || storedPosition != editPosition {
				_ = rows.Close()
				return errors.New("stored evolution edit history does not reconcile")
			}
			expected, err := json.Marshal(candidate.Edits[editPosition])
			if err != nil || payload != string(expected) {
				_ = rows.Close()
				return errors.New("stored evolution edit snapshot differs from round")
			}
			editPosition++
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if editPosition != len(candidate.Edits) {
			return errors.New("stored evolution edit history is incomplete")
		}
	}
	return nil
}

func validateEvolutionAudit(actor, reason string) error {
	if strings.TrimSpace(actor) == "" || len(actor) > 128 || strings.TrimSpace(reason) == "" || len(reason) > 2000 {
		return errors.New("evolution audit requires an actor and reason within size limits")
	}
	if privacy.ContainsCredentialText(actor) || privacy.ContainsCredentialText(reason) {
		return errors.New("evolution audit metadata contains credential material")
	}
	return nil
}
