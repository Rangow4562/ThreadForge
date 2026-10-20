package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/decision"
)

type DecisionObservationHistoryEntry struct {
	StreamID         string `json:"stream_id"`
	Revision         int    `json:"revision"`
	Action           string `json:"action"`
	Actor            string `json:"actor"`
	Reason           string `json:"reason"`
	CreatedAt        string `json:"created_at"`
	ContentSHA256    string `json:"content_sha256"`
	ObservationCount int    `json:"observation_count"`
	TotalFits        int    `json:"total_fits"`
	ArtifactID       string `json:"artifact_id,omitempty"`
}

func (s *Store) GetDecisionObservation(ctx context.Context, streamID string) (decision.ObservationCheckpoint, error) {
	return readDecisionObservation(ctx, s.db, streamID)
}

// CommitDecisionObservation advances a private checkpoint and, when a new L2
// head was fitted, creates its review-only artifact in the same SQLite transaction.
func (s *Store) CommitDecisionObservation(ctx context.Context, checkpoint decision.ObservationCheckpoint,
	expectedRevision int, actor, reason string, candidate *decision.Artifact, at time.Time) (decision.ObservationCheckpoint, error) {
	if err := validateDecisionAudit(actor, reason); err != nil {
		return decision.ObservationCheckpoint{}, err
	}
	if expectedRevision < 0 || expectedRevision >= decision.MaxObservationCheckpointStates || checkpoint.Revision != expectedRevision+1 {
		return decision.ObservationCheckpoint{}, errors.New("observation checkpoint revision must advance expected_revision by one")
	}
	if err := decision.ValidateObservationCheckpoint(checkpoint); err != nil {
		return decision.ObservationCheckpoint{}, err
	}
	if candidate != nil {
		if candidate.Revision != 1 || candidate.Status != decision.ArtifactCandidate || candidate.Level != "L2" ||
			candidate.Model != checkpoint.Model || candidate.QuestionKey != checkpoint.QuestionKey ||
			candidate.CalibrationExamples > checkpoint.ObservationCount {
			return decision.ObservationCheckpoint{}, errors.New("observed L2 artifact must be a matching review candidate within the checkpoint sample count")
		}
		if err := decision.ValidateArtifact(*candidate); err != nil {
			return decision.ObservationCheckpoint{}, err
		}
		if expectedRevision == 0 && candidate.CalibrationExamples < 1 {
			return decision.ObservationCheckpoint{}, errors.New("initial observation candidate has no training examples")
		}
	}

	var committed decision.ObservationCheckpoint
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		action := "created"
		if expectedRevision == 0 {
			if _, err := readDecisionObservation(ctx, tx, checkpoint.StreamID); err == nil {
				return ErrDecisionObservationConflict
			} else if !errors.Is(err, ErrDecisionObservationNotFound) {
				return err
			}
			if checkpoint.Revision != 1 {
				return errors.New("new observation stream must start at revision 1")
			}
			if checkpoint.TotalFits > 0 && candidate == nil {
				return errors.New("a newly fitted observation head must be committed with its review candidate")
			}
			if checkpoint.TotalFits == 0 && candidate != nil {
				return errors.New("observation candidate cannot exist before the first head fit")
			}
			stamp := at.UTC().Format(time.RFC3339Nano)
			checkpoint.CreatedAt = stamp
			checkpoint.UpdatedAt = stamp
			if err := decision.ValidateObservationCheckpoint(checkpoint); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO decision_observation_streams
				 (id, revision, model, question_key, observation_count, total_fits, content_sha256, checkpoint_json, created_at, updated_at)
				 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				checkpoint.StreamID, checkpoint.Revision, checkpoint.Model, checkpoint.QuestionKey,
				checkpoint.ObservationCount, checkpoint.TotalFits, checkpoint.ContentSHA256,
				string(checkpoint.Content), checkpoint.CreatedAt, checkpoint.UpdatedAt); err != nil {
				return fmt.Errorf("create decision observation stream: %w", err)
			}
		} else {
			current, err := readDecisionObservation(ctx, tx, checkpoint.StreamID)
			if err != nil {
				return err
			}
			if current.Revision != expectedRevision {
				return ErrDecisionObservationConflict
			}
			if err := decision.ValidateObservationCheckpointAppend(current, checkpoint); err != nil {
				return err
			}
			if candidate != nil && candidate.CalibrationExamples <= current.ObservationCount {
				return errors.New("observed L2 candidate does not include newly appended training examples")
			}
			if checkpoint.TotalFits > current.TotalFits && candidate == nil {
				return errors.New("a newly fitted observation head must be committed with its review candidate")
			}
			if candidate != nil && checkpoint.TotalFits <= current.TotalFits {
				return errors.New("observation candidate does not correspond to a new fit")
			}
			checkpoint.CreatedAt = current.CreatedAt
			checkpoint.UpdatedAt = at.UTC().Format(time.RFC3339Nano)
			if err := decision.ValidateObservationCheckpoint(checkpoint); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx,
				`UPDATE decision_observation_streams
				 SET revision = ?, model = ?, question_key = ?, observation_count = ?, total_fits = ?,
				     content_sha256 = ?, checkpoint_json = ?, updated_at = ?
				 WHERE id = ? AND revision = ?`,
				checkpoint.Revision, checkpoint.Model, checkpoint.QuestionKey, checkpoint.ObservationCount,
				checkpoint.TotalFits, checkpoint.ContentSHA256, string(checkpoint.Content), checkpoint.UpdatedAt,
				checkpoint.StreamID, expectedRevision)
			if err != nil {
				return fmt.Errorf("advance decision observation stream: %w", err)
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if changed != 1 {
				return ErrDecisionObservationConflict
			}
			action = "updated"
		}

		artifactID := ""
		if candidate != nil {
			if err := insertNewDecisionArtifact(ctx, tx, *candidate, "observed", actor, reason); err != nil {
				return err
			}
			artifactID = candidate.ID
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO decision_observation_history
			 (stream_id, revision, action, actor, reason, created_at, content_sha256, observation_count, total_fits, artifact_id)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			checkpoint.StreamID, checkpoint.Revision, action, actor, reason, checkpoint.UpdatedAt,
			checkpoint.ContentSHA256, checkpoint.ObservationCount, checkpoint.TotalFits, artifactID); err != nil {
			return fmt.Errorf("append decision observation audit: %w", err)
		}
		committed = checkpoint
		return nil
	})
	return committed, err
}

func (s *Store) DecisionObservationHistory(ctx context.Context, streamID string) ([]DecisionObservationHistoryEntry, error) {
	current, err := s.GetDecisionObservation(ctx, streamID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT stream_id, revision, action, actor, reason, created_at, content_sha256, observation_count, total_fits, artifact_id
		 FROM decision_observation_history WHERE stream_id = ? ORDER BY revision`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := make([]DecisionObservationHistoryEntry, 0)
	expected := 1
	previousCount := 0
	previousFits := 0
	for rows.Next() {
		var item DecisionObservationHistoryEntry
		if err := rows.Scan(&item.StreamID, &item.Revision, &item.Action, &item.Actor, &item.Reason,
			&item.CreatedAt, &item.ContentSHA256, &item.ObservationCount, &item.TotalFits, &item.ArtifactID); err != nil {
			return nil, err
		}
		if item.StreamID != streamID || item.Revision != expected || (expected == 1 && item.Action != "created") ||
			(expected > 1 && item.Action != "updated") || item.ObservationCount < 1 ||
			item.ObservationCount > decision.MaxObservationCheckpointStates || item.TotalFits < 0 ||
			item.TotalFits > item.ObservationCount || item.ObservationCount <= previousCount ||
			item.TotalFits < previousFits || item.TotalFits-previousFits > item.ObservationCount-previousCount ||
			!validSHA256(item.ContentSHA256) ||
			(item.TotalFits > previousFits) != (item.ArtifactID != "") {
			return nil, errors.New("decision observation history is not a valid contiguous audit")
		}
		if err := validateDecisionAudit(item.Actor, item.Reason); err != nil {
			return nil, err
		}
		history = append(history, item)
		previousCount = item.ObservationCount
		previousFits = item.TotalFits
		expected++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index, item := range history {
		if item.ArtifactID == "" {
			continue
		}
		artifactHistory, err := s.DecisionArtifactHistory(ctx, item.ArtifactID)
		if err != nil || len(artifactHistory) == 0 {
			return nil, errors.New("decision observation audit references a missing candidate artifact")
		}
		artifact := artifactHistory[0].Artifact
		previousObservationCount := 0
		if index > 0 {
			previousObservationCount = history[index-1].ObservationCount
		}
		if artifactHistory[0].Action != "observed" || artifact.Status != decision.ArtifactCandidate ||
			artifact.Level != "L2" || artifact.Model != current.Model || artifact.QuestionKey != current.QuestionKey ||
			artifact.CalibrationExamples <= previousObservationCount || artifact.CalibrationExamples > item.ObservationCount {
			return nil, errors.New("decision observation audit references a missing or invalid candidate artifact")
		}
	}
	if len(history) != current.Revision || len(history) == 0 ||
		history[len(history)-1].ContentSHA256 != current.ContentSHA256 ||
		history[len(history)-1].ObservationCount != current.ObservationCount ||
		history[len(history)-1].TotalFits != current.TotalFits {
		return nil, errors.New("decision observation audit does not reconcile with its current checkpoint")
	}
	return history, nil
}

func readDecisionObservation(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, streamID string) (decision.ObservationCheckpoint, error) {
	var checkpoint decision.ObservationCheckpoint
	var content string
	err := queryer.QueryRowContext(ctx,
		`SELECT id, revision, model, question_key, observation_count, total_fits, content_sha256, checkpoint_json, created_at, updated_at
		 FROM decision_observation_streams WHERE id = ?`, streamID).Scan(
		&checkpoint.StreamID, &checkpoint.Revision, &checkpoint.Model, &checkpoint.QuestionKey,
		&checkpoint.ObservationCount, &checkpoint.TotalFits, &checkpoint.ContentSHA256, &content,
		&checkpoint.CreatedAt, &checkpoint.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return decision.ObservationCheckpoint{}, ErrDecisionObservationNotFound
	}
	if err != nil {
		return decision.ObservationCheckpoint{}, err
	}
	checkpoint.Content = json.RawMessage(content)
	if checkpoint.StreamID != streamID {
		return decision.ObservationCheckpoint{}, errors.New("stored decision observation ID does not match its query key")
	}
	if err := decision.ValidateObservationCheckpoint(checkpoint); err != nil {
		return decision.ObservationCheckpoint{}, fmt.Errorf("stored decision observation checkpoint failed validation: %w", err)
	}
	return checkpoint, nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}
