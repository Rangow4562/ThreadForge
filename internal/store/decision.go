package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/decision"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

type DecisionArtifactRevision struct {
	Artifact  decision.Artifact `json:"artifact"`
	Action    string            `json:"action"`
	Actor     string            `json:"actor"`
	Reason    string            `json:"reason"`
	CreatedAt string            `json:"created_at"`
}

func (s *Store) CreateDecisionArtifact(ctx context.Context, artifact decision.Artifact, actor, reason string) error {
	return s.createDecisionArtifact(ctx, artifact, actor, reason, "calibrated")
}

func (s *Store) ImportDecisionArtifact(ctx context.Context, artifact decision.Artifact, actor, reason string) error {
	return s.createDecisionArtifact(ctx, artifact, actor, reason, "imported")
}

func (s *Store) createDecisionArtifact(ctx context.Context, artifact decision.Artifact, actor, reason, action string) error {
	if artifact.Revision != 1 || artifact.Status != decision.ArtifactCandidate {
		return errors.New("new decision artifacts must start as candidate revision 1")
	}
	if action != "calibrated" && action != "imported" && action != "observed" {
		return errors.New("new decision artifact action must be calibrated, imported, or observed")
	}
	if err := decision.ValidateArtifact(artifact); err != nil {
		return err
	}
	if err := validateDecisionAudit(actor, reason); err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return insertNewDecisionArtifact(ctx, tx, artifact, action, actor, reason)
	})
}

func insertNewDecisionArtifact(ctx context.Context, tx *sql.Tx, artifact decision.Artifact, action, actor, reason string) error {
	payload, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO decision_artifacts(id, revision, status, artifact_json, updated_at) VALUES(?, ?, ?, ?, ?)`,
		artifact.ID, artifact.Revision, artifact.Status, string(payload), artifact.UpdatedAt); err != nil {
		return fmt.Errorf("create decision artifact: %w", err)
	}
	return insertDecisionHistory(ctx, tx, artifact, action, actor, reason, artifact.UpdatedAt)
}

func (s *Store) GetDecisionArtifact(ctx context.Context, id string) (decision.Artifact, error) {
	return readDecisionArtifact(ctx, s.db, id)
}

func (s *Store) ListDecisionArtifacts(ctx context.Context, status string, limit, offset int) ([]decision.Artifact, int, error) {
	if limit < 1 || limit > maxContinuityPage || offset < 0 {
		return nil, 0, errors.New("limit must be 1-200 and offset must be non-negative")
	}
	if status != "" && status != string(decision.ArtifactCandidate) && status != string(decision.ArtifactActive) &&
		status != string(decision.ArtifactRejected) && status != string(decision.ArtifactRetired) {
		return nil, 0, errors.New("status must be candidate, active, rejected, or retired")
	}
	var total int
	var err error
	if status == "" {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_artifacts`).Scan(&total)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_artifacts WHERE status = ?`, status).Scan(&total)
	}
	if err != nil {
		return nil, 0, err
	}
	query := `SELECT artifact_json FROM decision_artifacts ORDER BY updated_at DESC, id LIMIT ? OFFSET ?`
	args := []any{limit, offset}
	if status != "" {
		query = `SELECT artifact_json FROM decision_artifacts WHERE status = ? ORDER BY updated_at DESC, id LIMIT ? OFFSET ?`
		args = []any{status, limit, offset}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]decision.Artifact, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, 0, err
		}
		artifact, err := decodeDecisionArtifact(payload)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, artifact)
	}
	return result, total, rows.Err()
}

func (s *Store) DecisionArtifactHistory(ctx context.Context, id string) ([]DecisionArtifactRevision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT revision, action, actor, reason, created_at, artifact_json FROM decision_artifact_history WHERE artifact_id = ? ORDER BY revision`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DecisionArtifactRevision, 0)
	expected := 1
	for rows.Next() {
		var revision int
		var item DecisionArtifactRevision
		var payload string
		if err := rows.Scan(&revision, &item.Action, &item.Actor, &item.Reason, &item.CreatedAt, &payload); err != nil {
			return nil, err
		}
		artifact, err := decodeDecisionArtifact(payload)
		if err != nil {
			return nil, err
		}
		if artifact.ID != id || artifact.Revision != revision || revision != expected {
			return nil, errors.New("decision artifact revision history is not contiguous")
		}
		if item.CreatedAt != artifact.UpdatedAt || !validDecisionHistoryAction(revision, item.Action, artifact.Status) {
			return nil, errors.New("decision artifact history action or timestamp does not match its snapshot")
		}
		if err := validateDecisionAudit(item.Actor, item.Reason); err != nil {
			return nil, errors.New("decision artifact history contains invalid audit metadata")
		}
		item.Artifact = artifact
		result = append(result, item)
		expected++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, ErrDecisionArtifactNotFound
	}
	return result, nil
}

func (s *Store) TransitionDecisionArtifact(ctx context.Context, id string, expectedRevision int,
	status decision.ArtifactStatus, actor, reason string, at time.Time) (decision.Artifact, error) {
	return s.TransitionDecisionArtifactWithCheckpoint(ctx, id, expectedRevision, status, actor, reason, "", at)
}

func (s *Store) TransitionDecisionArtifactWithCheckpoint(ctx context.Context, id string, expectedRevision int,
	status decision.ArtifactStatus, actor, reason, modelCheckpointSHA256 string, at time.Time) (decision.Artifact, error) {
	if err := validateDecisionAudit(actor, reason); err != nil {
		return decision.Artifact{}, err
	}
	if expectedRevision < 1 {
		return decision.Artifact{}, errors.New("expected decision artifact revision must be positive")
	}
	if modelCheckpointSHA256 != "" && status != decision.ArtifactActive {
		return decision.Artifact{}, errors.New("a model checkpoint fingerprint may only be bound while approving an artifact")
	}
	var updated decision.Artifact
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		current, err := readDecisionArtifact(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return ErrDecisionArtifactConflict
		}
		if !decisionArtifactTransitionAllowed(current.Status, status) {
			return fmt.Errorf("invalid decision artifact transition from %s to %s", current.Status, status)
		}
		updated = current
		updated.Revision++
		updated.Status = status
		if modelCheckpointSHA256 != "" {
			if current.Status != decision.ArtifactCandidate {
				return errors.New("a model checkpoint fingerprint may only be bound when approving a candidate")
			}
			updated.ModelCheckpointSHA256 = modelCheckpointSHA256
		}
		updated.UpdatedAt = at.UTC().Format(time.RFC3339Nano)
		if err := decision.ValidateArtifact(updated); err != nil {
			return err
		}
		payload, err := json.Marshal(updated)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE decision_artifacts SET revision = ?, status = ?, artifact_json = ?, updated_at = ? WHERE id = ? AND revision = ?`,
			updated.Revision, updated.Status, string(payload), updated.UpdatedAt, id, expectedRevision)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrDecisionArtifactConflict
		}
		action := map[decision.ArtifactStatus]string{
			decision.ArtifactActive:   "approved",
			decision.ArtifactRejected: "rejected",
			decision.ArtifactRetired:  "retired",
		}[status]
		return insertDecisionHistory(ctx, tx, updated, action, actor, reason, updated.UpdatedAt)
	})
	return updated, err
}

func decisionArtifactTransitionAllowed(current, next decision.ArtifactStatus) bool {
	return current == decision.ArtifactCandidate && (next == decision.ArtifactActive || next == decision.ArtifactRejected) ||
		current == decision.ArtifactActive && next == decision.ArtifactRetired
}

func validDecisionHistoryAction(revision int, action string, status decision.ArtifactStatus) bool {
	switch revision {
	case 1:
		return (action == "calibrated" || action == "imported" || action == "observed") && status == decision.ArtifactCandidate
	case 2:
		return action == "approved" && status == decision.ArtifactActive || action == "rejected" && status == decision.ArtifactRejected
	case 3:
		return action == "retired" && status == decision.ArtifactRetired
	default:
		return false
	}
}

func readDecisionArtifact(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (decision.Artifact, error) {
	var rowID, rowStatus, payload string
	var rowRevision int
	err := queryer.QueryRowContext(ctx, `SELECT id, revision, status, artifact_json FROM decision_artifacts WHERE id = ?`, id).
		Scan(&rowID, &rowRevision, &rowStatus, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return decision.Artifact{}, ErrDecisionArtifactNotFound
	}
	if err != nil {
		return decision.Artifact{}, err
	}
	artifact, err := decodeDecisionArtifact(payload)
	if err != nil {
		return decision.Artifact{}, err
	}
	if artifact.ID != rowID || artifact.Revision != rowRevision || string(artifact.Status) != rowStatus {
		return decision.Artifact{}, errors.New("stored decision artifact columns do not match its validated snapshot")
	}
	return artifact, nil
}

func decodeDecisionArtifact(payload string) (decision.Artifact, error) {
	var artifact decision.Artifact
	if err := json.Unmarshal([]byte(payload), &artifact); err != nil {
		return decision.Artifact{}, fmt.Errorf("decode stored decision artifact: %w", err)
	}
	if err := decision.ValidateArtifact(artifact); err != nil {
		return decision.Artifact{}, fmt.Errorf("stored decision artifact failed validation: %w", err)
	}
	return artifact, nil
}

func insertDecisionHistory(ctx context.Context, tx *sql.Tx, artifact decision.Artifact, action, actor, reason, stamp string) error {
	payload, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO decision_artifact_history(artifact_id, revision, action, actor, reason, created_at, artifact_json) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		artifact.ID, artifact.Revision, action, actor, reason, stamp, string(payload)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO decision_audit(artifact_id, revision, action, actor, reason, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		artifact.ID, artifact.Revision, action, actor, reason, stamp)
	return err
}

func validateDecisionAudit(actor, reason string) error {
	if strings.TrimSpace(actor) == "" || len(actor) > 128 || strings.TrimSpace(reason) == "" || len(reason) > 2000 {
		return errors.New("decision artifact actor and reason are required and bounded")
	}
	if privacy.ContainsCredentialText(actor) || privacy.ContainsCredentialText(reason) {
		return errors.New("decision artifact audit metadata contains credential material")
	}
	return nil
}
