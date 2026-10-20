package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	SecurityReviewOpen          = "open"
	SecurityReviewAcknowledged  = "acknowledged"
	SecurityReviewResolved      = "resolved"
	SecurityReviewFalsePositive = "false_positive"
	maxSecurityReviewPage       = 200
	maxStoredSecurityFindings   = 20_000
	maxSecurityReviewRecords    = 100_000
)

type SecurityFindingObservation struct {
	Fingerprint string
	RuleID      string
	Severity    string
}

type SecurityFindingReview struct {
	Fingerprint string `json:"fingerprint"`
	RuleID      string `json:"rule_id"`
	Severity    string `json:"severity"`
	Status      string `json:"status"`
	Revision    int    `json:"revision"`
	FirstSeen   string `json:"first_seen"`
	LastSeen    string `json:"last_seen"`
	Active      bool   `json:"active"`
	Actor       string `json:"actor"`
	UpdatedAt   string `json:"updated_at"`
}

type SecurityFindingReviewEvent struct {
	Sequence       int64  `json:"sequence"`
	Fingerprint    string `json:"fingerprint"`
	Revision       int    `json:"revision"`
	PreviousStatus string `json:"previous_status"`
	Status         string `json:"status"`
	Actor          string `json:"actor"`
	CreatedAt      string `json:"created_at"`
}

// ReconcileSecurityFindingReviews records a complete scan's bounded,
// privacy-minimized findings. Incomplete scans must not change active state.
func (s *Store) ReconcileSecurityFindingReviews(ctx context.Context, observations []SecurityFindingObservation, at time.Time) error {
	if len(observations) > maxStoredSecurityFindings {
		return fmt.Errorf("security review state is limited to %d findings per scan", maxStoredSecurityFindings)
	}
	if at.IsZero() {
		return errors.New("security scan time is required")
	}
	seen := make(map[string]struct{}, len(observations))
	for _, observation := range observations {
		if !validSecurityFingerprint(observation.Fingerprint) {
			return errors.New("security finding fingerprint must be a lowercase SHA-256 digest")
		}
		if _, exists := seen[observation.Fingerprint]; exists {
			return errors.New("security scan contains duplicate finding fingerprints")
		}
		seen[observation.Fingerprint] = struct{}{}
		if err := validateSecurityFindingIdentity(observation.RuleID, observation.Severity); err != nil {
			return err
		}
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE security_finding_reviews SET active = 0`); err != nil {
			return err
		}
		for _, observation := range observations {
			result, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO security_finding_reviews
				 (fingerprint, rule_id, severity, status, revision, first_seen, last_seen, active, actor, updated_at)
				 VALUES(?, ?, ?, ?, 0, ?, ?, 1, 'local-scan', ?)`,
				observation.Fingerprint, observation.RuleID, observation.Severity, SecurityReviewOpen, stamp, stamp, stamp)
			if err != nil {
				return fmt.Errorf("insert security finding review: %w", err)
			}
			inserted, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if inserted == 1 {
				if err := appendSecurityReviewEvent(ctx, tx, observation.Fingerprint, 0, "", SecurityReviewOpen, "local-scan", stamp); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE security_finding_reviews
				 SET rule_id = ?, severity = ?, last_seen = ?, active = 1
				 WHERE fingerprint = ?`,
				observation.RuleID, observation.Severity, stamp, observation.Fingerprint); err != nil {
				return fmt.Errorf("update security finding observation: %w", err)
			}
		}
		var recordCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_finding_reviews`).Scan(&recordCount); err != nil {
			return err
		}
		if recordCount > maxSecurityReviewRecords {
			return fmt.Errorf("security review history is limited to %d distinct findings", maxSecurityReviewRecords)
		}
		return nil
	})
}

func (s *Store) GetSecurityFindingReview(ctx context.Context, fingerprint string) (SecurityFindingReview, error) {
	if !validSecurityFingerprint(fingerprint) {
		return SecurityFindingReview{}, errors.New("security finding fingerprint must be a lowercase SHA-256 digest")
	}
	return readSecurityFindingReview(ctx, s.db, fingerprint)
}

func (s *Store) ListSecurityFindingReviews(ctx context.Context, limit, offset int) ([]SecurityFindingReview, int, error) {
	if limit < 1 || limit > maxSecurityReviewPage || offset < 0 {
		return nil, 0, errors.New("security review limit must be 1-200 and offset must be non-negative")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_finding_reviews`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT fingerprint, rule_id, severity, status, revision, first_seen, last_seen, active, actor, updated_at
		 FROM security_finding_reviews ORDER BY active DESC, last_seen DESC, fingerprint LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]SecurityFindingReview, 0)
	for rows.Next() {
		var item SecurityFindingReview
		var active int
		if err := rows.Scan(&item.Fingerprint, &item.RuleID, &item.Severity, &item.Status, &item.Revision,
			&item.FirstSeen, &item.LastSeen, &active, &item.Actor, &item.UpdatedAt); err != nil {
			return nil, 0, err
		}
		item.Active = active != 0
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Store) UpdateSecurityFindingReview(ctx context.Context, fingerprint string, expectedRevision int, status, actor string, at time.Time) (SecurityFindingReview, error) {
	if !validSecurityFingerprint(fingerprint) {
		return SecurityFindingReview{}, errors.New("security finding fingerprint must be a lowercase SHA-256 digest")
	}
	if expectedRevision < 0 {
		return SecurityFindingReview{}, errors.New("expected security review revision must not be negative")
	}
	if !validSecurityReviewStatus(status) {
		return SecurityFindingReview{}, errors.New("security review status must be open, acknowledged, resolved, or false_positive")
	}
	if err := validateSecurityReviewActor(actor); err != nil {
		return SecurityFindingReview{}, err
	}
	if at.IsZero() {
		return SecurityFindingReview{}, errors.New("security review time is required")
	}
	var updated SecurityFindingReview
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		current, err := readSecurityFindingReview(ctx, tx, fingerprint)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return ErrSecurityFindingReviewConflict
		}
		if current.Status == status {
			return errors.New("security finding already has the requested review status")
		}
		updated = current
		updated.Status = status
		updated.Revision++
		updated.Actor = actor
		updated.UpdatedAt = at.UTC().Format(time.RFC3339Nano)
		result, err := tx.ExecContext(ctx,
			`UPDATE security_finding_reviews SET status = ?, revision = ?, actor = ?, updated_at = ?
			 WHERE fingerprint = ? AND revision = ?`,
			updated.Status, updated.Revision, updated.Actor, updated.UpdatedAt, fingerprint, expectedRevision)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrSecurityFindingReviewConflict
		}
		return appendSecurityReviewEvent(ctx, tx, fingerprint, updated.Revision, current.Status, status, actor, updated.UpdatedAt)
	})
	return updated, err
}

func (s *Store) SecurityFindingReviewHistory(ctx context.Context, fingerprint string) ([]SecurityFindingReviewEvent, error) {
	if !validSecurityFingerprint(fingerprint) {
		return nil, errors.New("security finding fingerprint must be a lowercase SHA-256 digest")
	}
	current, err := readSecurityFindingReview(ctx, s.db, fingerprint)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT sequence, fingerprint, revision, previous_status, status, actor, created_at
		 FROM security_finding_review_history WHERE fingerprint = ? ORDER BY revision`, fingerprint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]SecurityFindingReviewEvent, 0)
	expectedRevision := 0
	previous := ""
	for rows.Next() {
		var event SecurityFindingReviewEvent
		if err := rows.Scan(&event.Sequence, &event.Fingerprint, &event.Revision, &event.PreviousStatus,
			&event.Status, &event.Actor, &event.CreatedAt); err != nil {
			return nil, err
		}
		if event.Revision != expectedRevision || event.PreviousStatus != previous || !validSecurityReviewStatus(event.Status) {
			return nil, errors.New("security finding review history is not contiguous")
		}
		events = append(events, event)
		expectedRevision++
		previous = event.Status
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 || expectedRevision-1 != current.Revision || previous != current.Status {
		return nil, errors.New("security finding review history does not match current state")
	}
	return events, nil
}

func readSecurityFindingReview(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, fingerprint string) (SecurityFindingReview, error) {
	var item SecurityFindingReview
	var active int
	err := q.QueryRowContext(ctx,
		`SELECT fingerprint, rule_id, severity, status, revision, first_seen, last_seen, active, actor, updated_at
		 FROM security_finding_reviews WHERE fingerprint = ?`, fingerprint).Scan(
		&item.Fingerprint, &item.RuleID, &item.Severity, &item.Status, &item.Revision,
		&item.FirstSeen, &item.LastSeen, &active, &item.Actor, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SecurityFindingReview{}, ErrSecurityFindingReviewNotFound
	}
	if err != nil {
		return SecurityFindingReview{}, err
	}
	item.Active = active != 0
	return item, nil
}

func appendSecurityReviewEvent(ctx context.Context, tx *sql.Tx, fingerprint string, revision int, previous, status, actor, at string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO security_finding_review_history
		 (fingerprint, revision, previous_status, status, actor, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		fingerprint, revision, previous, status, actor, at)
	return err
}

func validSecurityFingerprint(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validateSecurityFindingIdentity(ruleID, severity string) error {
	if strings.TrimSpace(ruleID) != ruleID || len(ruleID) == 0 || len(ruleID) > 128 || privacy.ContainsCredentialText(ruleID) {
		return errors.New("security rule identifier is invalid or credential-like")
	}
	for _, char := range ruleID {
		if unicode.IsControl(char) {
			return errors.New("security rule identifier contains control characters")
		}
	}
	switch severity {
	case "info", "low", "medium", "high", "critical":
		return nil
	default:
		return errors.New("security finding severity is invalid")
	}
}

func validateSecurityReviewActor(actor string) error {
	if strings.TrimSpace(actor) != actor || len(actor) == 0 || len(actor) > 64 || privacy.ContainsCredentialText(actor) {
		return errors.New("security review actor is invalid or credential-like")
	}
	for _, char := range actor {
		if unicode.IsControl(char) {
			return errors.New("security review actor contains control characters")
		}
	}
	return nil
}

func validSecurityReviewStatus(status string) bool {
	switch status {
	case SecurityReviewOpen, SecurityReviewAcknowledged, SecurityReviewResolved, SecurityReviewFalsePositive:
		return true
	default:
		return false
	}
}
