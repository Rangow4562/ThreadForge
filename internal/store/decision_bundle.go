package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/decision"
)

func (s *Store) ImportDecisionBundle(ctx context.Context, bundle decision.NativeBundleArtifact, actor, reason string) error {
	if bundle.Revision != 1 || bundle.Status != decision.NativeBundleCandidate {
		return errors.New("new decision bundles must start as candidate revision 1")
	}
	if err := decision.ValidateNativeBundleArtifact(bundle); err != nil {
		return err
	}
	if err := validateDecisionAudit(actor, reason); err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return insertNewDecisionBundle(ctx, tx, bundle, actor, reason)
	})
}

func insertNewDecisionBundle(ctx context.Context, tx *sql.Tx, bundle decision.NativeBundleArtifact, actor, reason string) error {
	payload, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO decision_bundles(id, revision, status, bundle_json, updated_at) VALUES(?, ?, ?, ?, ?)`,
		bundle.ID, bundle.Revision, bundle.Status, string(payload), bundle.UpdatedAt); err != nil {
		return fmt.Errorf("create decision bundle: %w", err)
	}
	return insertDecisionBundleHistory(ctx, tx, bundle, "imported", actor, reason, bundle.UpdatedAt)
}

func (s *Store) GetDecisionBundle(ctx context.Context, id string) (decision.NativeBundleArtifact, error) {
	return readDecisionBundle(ctx, s.db, id)
}

func (s *Store) ListDecisionBundles(ctx context.Context, status string, limit, offset int) ([]decision.NativeBundleSummary, int, error) {
	if limit < 1 || limit > maxContinuityPage || offset < 0 {
		return nil, 0, errors.New("limit must be 1-200 and offset must be non-negative")
	}
	if status != "" && status != string(decision.NativeBundleCandidate) && status != string(decision.NativeBundleActive) &&
		status != string(decision.NativeBundleRejected) && status != string(decision.NativeBundleRetired) {
		return nil, 0, errors.New("status must be candidate, active, rejected, or retired")
	}
	var total int
	var err error
	if status == "" {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_bundles`).Scan(&total)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_bundles WHERE status = ?`, status).Scan(&total)
	}
	if err != nil {
		return nil, 0, err
	}
	query := `SELECT id FROM decision_bundles ORDER BY updated_at DESC, id LIMIT ? OFFSET ?`
	args := []any{limit, offset}
	if status != "" {
		query = `SELECT id FROM decision_bundles WHERE status = ? ORDER BY updated_at DESC, id LIMIT ? OFFSET ?`
		args = []any{status, limit, offset}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
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
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if rowsErr != nil {
		return nil, 0, rowsErr
	}
	if closeErr != nil {
		return nil, 0, closeErr
	}
	result := make([]decision.NativeBundleSummary, 0, len(ids))
	for _, id := range ids {
		bundle, err := readDecisionBundle(ctx, s.db, id)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, decision.SummarizeNativeBundle(bundle))
	}
	return result, total, nil
}

func (s *Store) DecisionBundleHistory(ctx context.Context, id string) ([]decision.NativeBundleRevision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT revision, action, actor, reason, created_at, bundle_json FROM decision_bundle_history WHERE bundle_id = ? ORDER BY revision`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]decision.NativeBundleRevision, 0)
	expected := 1
	for rows.Next() {
		var revision int
		var item decision.NativeBundleRevision
		var payload string
		if err := rows.Scan(&revision, &item.Action, &item.Actor, &item.Reason, &item.CreatedAt, &payload); err != nil {
			return nil, err
		}
		bundle, err := decodeDecisionBundle(payload)
		if err != nil {
			return nil, err
		}
		if bundle.ID != id || bundle.Revision != revision || revision != expected {
			return nil, errors.New("decision bundle revision history is not contiguous")
		}
		if item.CreatedAt != bundle.UpdatedAt || !validDecisionBundleHistoryAction(revision, item.Action, bundle.Status) {
			return nil, errors.New("decision bundle history action or timestamp does not match its snapshot")
		}
		if err := validateDecisionAudit(item.Actor, item.Reason); err != nil {
			return nil, errors.New("decision bundle history contains invalid audit metadata")
		}
		item.Bundle = decision.SummarizeNativeBundle(bundle)
		result = append(result, item)
		expected++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, ErrDecisionBundleNotFound
	}
	return result, nil
}

func (s *Store) TransitionDecisionBundle(ctx context.Context, id string, expectedRevision int,
	status decision.NativeBundleStatus, actor, reason, modelCheckpointSHA256 string, at time.Time) (decision.NativeBundleArtifact, error) {
	if err := validateDecisionAudit(actor, reason); err != nil {
		return decision.NativeBundleArtifact{}, err
	}
	if status != decision.NativeBundleActive && modelCheckpointSHA256 != "" {
		return decision.NativeBundleArtifact{}, errors.New("model checkpoint fingerprints may only be bound during approval")
	}
	if expectedRevision < 1 {
		return decision.NativeBundleArtifact{}, errors.New("expected decision bundle revision must be positive")
	}
	var updated decision.NativeBundleArtifact
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		current, err := readDecisionBundle(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return ErrDecisionBundleConflict
		}
		if !decisionBundleTransitionAllowed(current.Status, status) {
			return fmt.Errorf("invalid decision bundle transition from %s to %s", current.Status, status)
		}
		updated = current
		updated.Revision++
		updated.Status = status
		if status == decision.NativeBundleActive {
			updated.ModelCheckpointSHA256 = modelCheckpointSHA256
		}
		updated.UpdatedAt = at.UTC().Format(time.RFC3339Nano)
		if err := decision.ValidateNativeBundleArtifact(updated); err != nil {
			return err
		}
		payload, err := json.Marshal(updated)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE decision_bundles SET revision = ?, status = ?, bundle_json = ?, updated_at = ? WHERE id = ? AND revision = ?`,
			updated.Revision, updated.Status, string(payload), updated.UpdatedAt, id, expectedRevision)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrDecisionBundleConflict
		}
		action := map[decision.NativeBundleStatus]string{
			decision.NativeBundleActive: "approved",
			decision.NativeBundleRejected: "rejected",
			decision.NativeBundleRetired: "retired",
		}[status]
		return insertDecisionBundleHistory(ctx, tx, updated, action, actor, reason, updated.UpdatedAt)
	})
	return updated, err
}

func decisionBundleTransitionAllowed(current, next decision.NativeBundleStatus) bool {
	return current == decision.NativeBundleCandidate && (next == decision.NativeBundleActive || next == decision.NativeBundleRejected) ||
		current == decision.NativeBundleActive && next == decision.NativeBundleRetired
}

func validDecisionBundleHistoryAction(revision int, action string, status decision.NativeBundleStatus) bool {
	switch revision {
	case 1:
		return action == "imported" && status == decision.NativeBundleCandidate
	case 2:
		return action == "approved" && status == decision.NativeBundleActive || action == "rejected" && status == decision.NativeBundleRejected
	case 3:
		return action == "retired" && status == decision.NativeBundleRetired
	default:
		return false
	}
}

func readDecisionBundle(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (decision.NativeBundleArtifact, error) {
	var rowID, rowStatus, rowUpdatedAt, payload string
	var rowRevision int
	err := queryer.QueryRowContext(ctx, `SELECT id, revision, status, updated_at, bundle_json FROM decision_bundles WHERE id = ?`, id).
		Scan(&rowID, &rowRevision, &rowStatus, &rowUpdatedAt, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return decision.NativeBundleArtifact{}, ErrDecisionBundleNotFound
	}
	if err != nil {
		return decision.NativeBundleArtifact{}, err
	}
	bundle, err := decodeDecisionBundle(payload)
	if err != nil {
		return decision.NativeBundleArtifact{}, err
	}
	if bundle.ID != rowID || bundle.Revision != rowRevision || string(bundle.Status) != rowStatus || bundle.UpdatedAt != rowUpdatedAt {
		return decision.NativeBundleArtifact{}, errors.New("stored decision bundle columns do not match its validated snapshot")
	}
	var historyRevision int
	var historyAction, historyActor, historyReason, historyCreatedAt, historyPayload string
	if err := queryer.QueryRowContext(ctx,
		`SELECT revision, action, actor, reason, created_at, bundle_json FROM decision_bundle_history WHERE bundle_id = ? ORDER BY revision DESC LIMIT 1`, id).
		Scan(&historyRevision, &historyAction, &historyActor, &historyReason, &historyCreatedAt, &historyPayload); err != nil {
		return decision.NativeBundleArtifact{}, errors.New("stored decision bundle is missing its immutable history snapshot")
	}
	historyBundle, err := decodeDecisionBundle(historyPayload)
	if err != nil || !reflect.DeepEqual(bundle, historyBundle) || historyRevision != bundle.Revision ||
		historyCreatedAt != bundle.UpdatedAt || !validDecisionBundleHistoryAction(historyRevision, historyAction, bundle.Status) ||
		validateDecisionAudit(historyActor, historyReason) != nil {
		return decision.NativeBundleArtifact{}, errors.New("stored decision bundle differs from its immutable history snapshot")
	}
	var auditAction, auditActor, auditReason, auditCreatedAt string
	if err := queryer.QueryRowContext(ctx,
		`SELECT action, actor, reason, created_at FROM decision_bundle_audit WHERE bundle_id = ? AND revision = ?`, id, bundle.Revision).
		Scan(&auditAction, &auditActor, &auditReason, &auditCreatedAt); err != nil ||
		auditAction != historyAction || auditActor != historyActor || auditReason != historyReason || auditCreatedAt != historyCreatedAt {
		return decision.NativeBundleArtifact{}, errors.New("stored decision bundle audit does not match its immutable history snapshot")
	}
	return bundle, nil
}

func decodeDecisionBundle(payload string) (decision.NativeBundleArtifact, error) {
	var bundle decision.NativeBundleArtifact
	if err := json.Unmarshal([]byte(payload), &bundle); err != nil {
		return decision.NativeBundleArtifact{}, fmt.Errorf("decode stored decision bundle: %w", err)
	}
	if err := decision.ValidateNativeBundleArtifact(bundle); err != nil {
		return decision.NativeBundleArtifact{}, fmt.Errorf("stored decision bundle failed validation: %w", err)
	}
	return bundle, nil
}

func insertDecisionBundleHistory(ctx context.Context, tx *sql.Tx, bundle decision.NativeBundleArtifact, action, actor, reason, stamp string) error {
	payload, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO decision_bundle_history(bundle_id, revision, action, actor, reason, created_at, bundle_json) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		bundle.ID, bundle.Revision, action, actor, reason, stamp, string(payload)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO decision_bundle_audit(bundle_id, revision, action, actor, reason, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		bundle.ID, bundle.Revision, action, actor, reason, stamp)
	return err
}
