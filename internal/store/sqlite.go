package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound                      = errors.New("procedure graph not found")
	ErrConflict                      = errors.New("procedure graph revision conflict")
	ErrTaskNotFound                  = errors.New("continuity task not found")
	ErrSessionNotFound               = errors.New("continuity session not found")
	ErrContinuityConflict            = errors.New("continuity revision conflict")
	ErrDecisionArtifactNotFound      = errors.New("decision artifact not found")
	ErrDecisionArtifactConflict      = errors.New("decision artifact revision conflict")
	ErrDecisionBundleNotFound        = errors.New("decision bundle not found")
	ErrDecisionBundleConflict        = errors.New("decision bundle revision conflict")
	ErrDecisionObservationNotFound   = errors.New("decision observation stream not found")
	ErrDecisionObservationConflict   = errors.New("decision observation stream revision conflict")
	ErrMigrationNotFound             = errors.New("migration receipt not found")
	ErrMigrationConflict             = errors.New("migration content conflict")
	ErrEvolutionRoundNotFound        = errors.New("evolution round not found")
	ErrEvolutionRoundConflict        = errors.New("evolution round content conflict")
	ErrEvolutionRepairNotFound       = errors.New("evolution repair run not found")
	ErrEvolutionRepairConflict       = errors.New("evolution repair content conflict")
	ErrSecurityFindingReviewNotFound = errors.New("security finding review not found")
	ErrSecurityFindingReviewConflict = errors.New("security finding review revision conflict")
	ErrRecoveryStaged                = errors.New("database is a staged recovery and cannot be used as an active store")
)

const CurrentSchemaVersion = 13

type Store struct {
	db             *sql.DB
	teamPolicyRoot string
}

type AuditEvent struct {
	Sequence  int64  `json:"sequence"`
	GraphID   string `json:"graph_id"`
	Revision  int    `json:"revision"`
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

func Open(path string) (*Store, error) {
	return openStore(path, false, "")
}

// OpenStagedRecovery is reserved for the explicit recovery-activation path.
// Ordinary Open continues to reject staged databases.
func OpenStagedRecovery(path, recoveryID string) (*Store, error) {
	if !validRecoveryID(recoveryID) {
		return nil, errors.New("valid staged recovery id is required")
	}
	return openStore(path, true, recoveryID)
}

func openStore(path string, allowStaged bool, recoveryID string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	paths, err := appdata.Resolve(path, "")
	if err != nil {
		return nil, err
	}
	policy, _, _, policyErr := appdata.ReadTeamTrustPolicy(paths.Root)
	if policyErr != nil && !errors.Is(policyErr, appdata.ErrTeamTrustPolicyNotInstalled) {
		return nil, fmt.Errorf("read installed team trust policy: %w", policyErr)
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create private store directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	db.SetMaxOpenConns(1)
	var stateErr error
	if allowStaged {
		stateErr = requireStagedRecovery(db, recoveryID)
	} else {
		stateErr = rejectStagedRecovery(db)
	}
	if stateErr != nil {
		_ = db.Close()
		return nil, stateErr
	}
	store := &Store{db: db, teamPolicyRoot: paths.Root}
	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if policyErr == nil {
		if err := store.registerTeamProjects(policy); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrPermission) {
			_ = db.Close()
			return nil, fmt.Errorf("restrict sqlite file permissions: %w", err)
		}
	}
	return store, nil
}

func requireStagedRecovery(db *sql.DB, expectedID string) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion {
		return errors.New("staged recovery schema version is unsupported")
	}
	var state, recoveryID string
	if err := db.QueryRow(`SELECT state, recovery_id FROM store_recovery_state WHERE singleton = 1`).Scan(&state, &recoveryID); err != nil {
		return fmt.Errorf("read staged recovery activation state: %w", err)
	}
	if state != "staged" || recoveryID != expectedID {
		return errors.New("database is not the expected staged recovery")
	}
	return nil
}

func rejectStagedRecovery(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion {
		return nil
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'store_recovery_state'`).Scan(&count); err != nil {
		return fmt.Errorf("read recovery activation schema: %w", err)
	}
	if count != 1 {
		return errors.New("database is missing its required recovery activation state")
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM store_recovery_state WHERE singleton = 1`).Scan(&state); err != nil {
		return fmt.Errorf("read recovery activation state: %w", err)
	}
	if state == "staged" {
		return ErrRecoveryStaged
	}
	if state != "active" {
		return errors.New("database recovery activation state is invalid")
	}
	return nil
}

func (s *Store) initialize(ctx context.Context) error {
	var userVersion int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	if userVersion > CurrentSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than this Threadforge build supports (%d)", userVersion, CurrentSchemaVersion)
	}
	statements := []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS procedure_graphs (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			status TEXT NOT NULL,
			graph_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS procedure_graphs_project_status
			ON procedure_graphs(project_id, status, id)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			graph_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS audit_events_graph_sequence
			ON audit_events(graph_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS migrations (
			id TEXT PRIMARY KEY,
			product TEXT NOT NULL,
			source_fingerprint TEXT NOT NULL,
			status TEXT NOT NULL,
			expected_count INTEGER NOT NULL,
			imported_count INTEGER NOT NULL,
			excluded_count INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			rolled_back_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS imported_records (
			migration_id TEXT NOT NULL REFERENCES migrations(id),
			record_key TEXT NOT NULL,
			record_type TEXT NOT NULL,
			source_path TEXT NOT NULL,
			content_sha256 TEXT NOT NULL,
			scope TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			PRIMARY KEY(migration_id, record_key)
		)`,
		`CREATE TABLE IF NOT EXISTS migration_audit (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id TEXT NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS imported_records_migration_key ON imported_records(migration_id, record_key)`,
		`CREATE INDEX IF NOT EXISTS migration_audit_migration_sequence ON migration_audit(migration_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS continuity_tasks (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			status TEXT NOT NULL,
			task_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS continuity_tasks_project_status ON continuity_tasks(project_id, status, id)`,
		`CREATE TABLE IF NOT EXISTS continuity_task_history (
			task_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL,
			task_json TEXT NOT NULL,
			PRIMARY KEY(task_id, revision)
		)`,
		`CREATE TRIGGER IF NOT EXISTS continuity_task_history_no_update BEFORE UPDATE ON continuity_task_history BEGIN SELECT RAISE(ABORT, 'continuity task history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_task_history_no_delete BEFORE DELETE ON continuity_task_history BEGIN SELECT RAISE(ABORT, 'continuity task history is append-only'); END`,
		`CREATE TABLE IF NOT EXISTS continuity_sessions (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			status TEXT NOT NULL,
			session_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS continuity_sessions_task_status ON continuity_sessions(task_id, status, id)`,
		`CREATE TABLE IF NOT EXISTS continuity_session_history (
			session_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL,
			session_json TEXT NOT NULL,
			PRIMARY KEY(session_id, revision)
		)`,
		`CREATE TRIGGER IF NOT EXISTS continuity_session_history_no_update BEFORE UPDATE ON continuity_session_history BEGIN SELECT RAISE(ABORT, 'continuity session history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_session_history_no_delete BEFORE DELETE ON continuity_session_history BEGIN SELECT RAISE(ABORT, 'continuity session history is append-only'); END`,
		`CREATE TABLE IF NOT EXISTS continuity_entries (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL UNIQUE,
			project_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			actor TEXT NOT NULL,
			created_at TEXT NOT NULL,
			entry_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS continuity_entries_task_sequence ON continuity_entries(task_id, sequence)`,
		`CREATE TRIGGER IF NOT EXISTS continuity_entries_no_update BEFORE UPDATE ON continuity_entries BEGIN SELECT RAISE(ABORT, 'continuity entries are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_entries_no_delete BEFORE DELETE ON continuity_entries BEGIN SELECT RAISE(ABORT, 'continuity entries are append-only'); END`,
		`CREATE TABLE IF NOT EXISTS continuity_audit (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			entity TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS continuity_audit_entity_sequence ON continuity_audit(entity, entity_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS continuity_policy_snapshots (
			policy_sha256 TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			policy_json TEXT NOT NULL,
			source_file_sha256 TEXT NOT NULL,
			captured_at TEXT NOT NULL
		)`,
		`CREATE TRIGGER IF NOT EXISTS continuity_policy_snapshots_no_update BEFORE UPDATE ON continuity_policy_snapshots BEGIN SELECT RAISE(ABORT, 'continuity policy snapshots are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_policy_snapshots_no_delete BEFORE DELETE ON continuity_policy_snapshots BEGIN SELECT RAISE(ABORT, 'continuity policy snapshots are append-only'); END`,
		`CREATE TABLE IF NOT EXISTS continuity_authorizations (
			sequence INTEGER PRIMARY KEY REFERENCES continuity_audit(sequence),
			policy_sha256 TEXT NOT NULL REFERENCES continuity_policy_snapshots(policy_sha256),
			attestation_json TEXT NOT NULL
		)`,
		`CREATE TRIGGER IF NOT EXISTS continuity_audit_no_update BEFORE UPDATE ON continuity_audit BEGIN SELECT RAISE(ABORT, 'continuity audit is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_audit_no_delete BEFORE DELETE ON continuity_audit BEGIN SELECT RAISE(ABORT, 'continuity audit is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_authorizations_no_update BEFORE UPDATE ON continuity_authorizations BEGIN SELECT RAISE(ABORT, 'continuity authorizations are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS continuity_authorizations_no_delete BEFORE DELETE ON continuity_authorizations BEGIN SELECT RAISE(ABORT, 'continuity authorizations are append-only'); END`,
		`CREATE TABLE IF NOT EXISTS team_project_scopes (
			project_id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			first_seen_at TEXT NOT NULL
		)`,
		`CREATE TRIGGER IF NOT EXISTS team_project_scopes_no_update BEFORE UPDATE ON team_project_scopes BEGIN SELECT RAISE(ABORT, 'team project scopes are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS team_project_scopes_no_delete BEFORE DELETE ON team_project_scopes BEGIN SELECT RAISE(ABORT, 'team project scopes are append-only'); END`,
		`CREATE TABLE IF NOT EXISTS team_project_scope_events (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id TEXT NOT NULL REFERENCES team_project_scopes(project_id),
			organization_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL,
			policy_sha256 TEXT NOT NULL,
			attestation_json TEXT NOT NULL,
			UNIQUE(project_id, revision)
		)`,
		`CREATE INDEX IF NOT EXISTS team_project_scope_events_project_revision ON team_project_scope_events(project_id, revision)`,
		`CREATE TRIGGER IF NOT EXISTS team_project_scope_events_no_update BEFORE UPDATE ON team_project_scope_events BEGIN SELECT RAISE(ABORT, 'team project scope events are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS team_project_scope_events_no_delete BEFORE DELETE ON team_project_scope_events BEGIN SELECT RAISE(ABORT, 'team project scope events are append-only'); END`,
		`CREATE TABLE IF NOT EXISTS team_project_scope_write_locks (
			project_id TEXT PRIMARY KEY REFERENCES team_project_scopes(project_id),
			generation INTEGER NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS store_recovery_state (
			singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
			state TEXT NOT NULL CHECK(state IN ('active', 'staged')),
			recovery_id TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`INSERT OR IGNORE INTO store_recovery_state(singleton, state, recovery_id, updated_at)
			VALUES(1, 'active', '', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
		`CREATE TABLE IF NOT EXISTS decision_artifacts (
			id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL,
			status TEXT NOT NULL,
			artifact_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS decision_artifacts_status_id ON decision_artifacts(status, id)`,
		`CREATE TABLE IF NOT EXISTS decision_bundles (
			id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL,
			status TEXT NOT NULL,
			bundle_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS decision_bundles_status_id ON decision_bundles(status, id)`,
		`CREATE TABLE IF NOT EXISTS decision_bundle_history (
			bundle_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL,
			bundle_json TEXT NOT NULL,
			PRIMARY KEY(bundle_id, revision)
		)`,
		`CREATE INDEX IF NOT EXISTS decision_bundle_history_id_revision ON decision_bundle_history(bundle_id, revision)`,
		`CREATE TRIGGER IF NOT EXISTS decision_bundle_history_no_update BEFORE UPDATE ON decision_bundle_history BEGIN SELECT RAISE(ABORT, 'decision bundle history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS decision_bundle_history_no_delete BEFORE DELETE ON decision_bundle_history BEGIN SELECT RAISE(ABORT, 'decision bundle history is append-only'); END`,
		`CREATE TABLE IF NOT EXISTS decision_bundle_audit (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			bundle_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS decision_bundle_audit_id_sequence ON decision_bundle_audit(bundle_id, sequence)`,
		`CREATE TRIGGER IF NOT EXISTS decision_bundle_audit_no_update BEFORE UPDATE ON decision_bundle_audit BEGIN SELECT RAISE(ABORT, 'decision bundle audit is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS decision_bundle_audit_no_delete BEFORE DELETE ON decision_bundle_audit BEGIN SELECT RAISE(ABORT, 'decision bundle audit is append-only'); END`,
		`CREATE TABLE IF NOT EXISTS decision_artifact_history (
			artifact_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL,
			artifact_json TEXT NOT NULL,
			PRIMARY KEY(artifact_id, revision)
		)`,
		`CREATE TABLE IF NOT EXISTS decision_audit (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			artifact_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS decision_audit_artifact_sequence ON decision_audit(artifact_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS decision_observation_streams (
			id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 4096),
			model TEXT NOT NULL,
			question_key TEXT NOT NULL,
			observation_count INTEGER NOT NULL CHECK(observation_count BETWEEN 1 AND 4096),
			total_fits INTEGER NOT NULL CHECK(total_fits >= 0),
			content_sha256 TEXT NOT NULL,
			checkpoint_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS decision_observation_history (
			stream_id TEXT NOT NULL REFERENCES decision_observation_streams(id),
			revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 4096),
			action TEXT NOT NULL CHECK(action IN ('created', 'updated')),
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL,
			content_sha256 TEXT NOT NULL,
			observation_count INTEGER NOT NULL CHECK(observation_count BETWEEN 1 AND 4096),
			total_fits INTEGER NOT NULL CHECK(total_fits >= 0),
			artifact_id TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(stream_id, revision)
		)`,
		`CREATE INDEX IF NOT EXISTS decision_observation_history_stream_revision ON decision_observation_history(stream_id, revision)`,
		`CREATE TRIGGER IF NOT EXISTS decision_observation_history_no_update BEFORE UPDATE ON decision_observation_history BEGIN SELECT RAISE(ABORT, 'decision observation history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS decision_observation_history_no_delete BEFORE DELETE ON decision_observation_history BEGIN SELECT RAISE(ABORT, 'decision observation history is append-only'); END`,
		`CREATE TABLE IF NOT EXISTS evolution_rounds (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			created_at TEXT NOT NULL,
			round_json TEXT NOT NULL,
			content_sha256 TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS evolution_rounds_project_created ON evolution_rounds(project_id, created_at DESC, id)`,
		`CREATE TABLE IF NOT EXISTS evolution_round_audit (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			round_id TEXT NOT NULL REFERENCES evolution_rounds(id),
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS evolution_round_audit_id_sequence ON evolution_round_audit(round_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS evolution_candidate_history (
			round_id TEXT NOT NULL REFERENCES evolution_rounds(id),
			candidate_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			candidate_json TEXT NOT NULL,
			PRIMARY KEY(round_id, candidate_id),
			UNIQUE(round_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS evolution_edit_history (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			round_id TEXT NOT NULL,
			candidate_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			edit_json TEXT NOT NULL,
			FOREIGN KEY(round_id, candidate_id) REFERENCES evolution_candidate_history(round_id, candidate_id)
		)`,
		`CREATE INDEX IF NOT EXISTS evolution_edit_round_sequence ON evolution_edit_history(round_id, sequence)`,
		`CREATE TRIGGER IF NOT EXISTS evolution_rounds_no_update BEFORE UPDATE ON evolution_rounds BEGIN SELECT RAISE(ABORT, 'evolution round snapshots are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_rounds_no_delete BEFORE DELETE ON evolution_rounds BEGIN SELECT RAISE(ABORT, 'evolution round snapshots are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_candidates_no_update BEFORE UPDATE ON evolution_candidate_history BEGIN SELECT RAISE(ABORT, 'evolution candidate history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_candidates_no_delete BEFORE DELETE ON evolution_candidate_history BEGIN SELECT RAISE(ABORT, 'evolution candidate history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_edits_no_update BEFORE UPDATE ON evolution_edit_history BEGIN SELECT RAISE(ABORT, 'evolution edit history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_edits_no_delete BEFORE DELETE ON evolution_edit_history BEGIN SELECT RAISE(ABORT, 'evolution edit history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_audit_no_update BEFORE UPDATE ON evolution_round_audit BEGIN SELECT RAISE(ABORT, 'evolution round audit is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_audit_no_delete BEFORE DELETE ON evolution_round_audit BEGIN SELECT RAISE(ABORT, 'evolution round audit is append-only'); END`,
		`CREATE TABLE IF NOT EXISTS evolution_repair_runs (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			created_at TEXT NOT NULL,
			run_json TEXT NOT NULL,
			content_sha256 TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS evolution_repair_runs_project_created ON evolution_repair_runs(project_id, created_at DESC, id)`,
		`CREATE TABLE IF NOT EXISTS evolution_repair_audit (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES evolution_repair_runs(id),
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS evolution_repair_audit_id_sequence ON evolution_repair_audit(run_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS evolution_repair_attempts (
			run_id TEXT NOT NULL REFERENCES evolution_repair_runs(id),
			attempt_number INTEGER NOT NULL,
			attempt_json TEXT NOT NULL,
			PRIMARY KEY(run_id, attempt_number)
		)`,
		`CREATE TABLE IF NOT EXISTS evolution_repair_edits (
			run_id TEXT NOT NULL,
			attempt_number INTEGER NOT NULL,
			position INTEGER NOT NULL,
			edit_json TEXT NOT NULL,
			PRIMARY KEY(run_id, attempt_number, position),
			FOREIGN KEY(run_id, attempt_number) REFERENCES evolution_repair_attempts(run_id, attempt_number)
		)`,
		`CREATE TRIGGER IF NOT EXISTS evolution_repair_runs_no_update BEFORE UPDATE ON evolution_repair_runs BEGIN SELECT RAISE(ABORT, 'evolution repair run snapshots are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_repair_runs_no_delete BEFORE DELETE ON evolution_repair_runs BEGIN SELECT RAISE(ABORT, 'evolution repair run snapshots are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_repair_attempts_no_update BEFORE UPDATE ON evolution_repair_attempts BEGIN SELECT RAISE(ABORT, 'evolution repair attempts are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_repair_attempts_no_delete BEFORE DELETE ON evolution_repair_attempts BEGIN SELECT RAISE(ABORT, 'evolution repair attempts are append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_repair_edits_no_update BEFORE UPDATE ON evolution_repair_edits BEGIN SELECT RAISE(ABORT, 'evolution repair edit history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_repair_edits_no_delete BEFORE DELETE ON evolution_repair_edits BEGIN SELECT RAISE(ABORT, 'evolution repair edit history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_repair_audit_no_update BEFORE UPDATE ON evolution_repair_audit BEGIN SELECT RAISE(ABORT, 'evolution repair audit is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS evolution_repair_audit_no_delete BEFORE DELETE ON evolution_repair_audit BEGIN SELECT RAISE(ABORT, 'evolution repair audit is append-only'); END`,
		`CREATE TABLE IF NOT EXISTS security_finding_reviews (
			fingerprint TEXT PRIMARY KEY,
			rule_id TEXT NOT NULL,
			severity TEXT NOT NULL,
			status TEXT NOT NULL,
			revision INTEGER NOT NULL,
			first_seen TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			active INTEGER NOT NULL,
			actor TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS security_finding_reviews_active_last_seen
			ON security_finding_reviews(active DESC, last_seen DESC, fingerprint)`,
		`CREATE TABLE IF NOT EXISTS security_finding_review_history (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			fingerprint TEXT NOT NULL REFERENCES security_finding_reviews(fingerprint),
			revision INTEGER NOT NULL,
			previous_status TEXT NOT NULL,
			status TEXT NOT NULL,
			actor TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(fingerprint, revision)
		)`,
		`CREATE INDEX IF NOT EXISTS security_finding_review_history_fingerprint_revision
			ON security_finding_review_history(fingerprint, revision)`,
		`CREATE TRIGGER IF NOT EXISTS security_finding_review_history_no_update BEFORE UPDATE ON security_finding_review_history BEGIN SELECT RAISE(ABORT, 'security finding review history is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS security_finding_review_history_no_delete BEFORE DELETE ON security_finding_review_history BEGIN SELECT RAISE(ABORT, 'security finding review history is append-only'); END`,
		`CREATE TABLE IF NOT EXISTS memory_feedback_events (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL UNIQUE,
			project_id TEXT NOT NULL,
			task_id TEXT NOT NULL REFERENCES continuity_tasks(id),
			target_kind TEXT NOT NULL,
			target_id TEXT NOT NULL,
			target_revision INTEGER NOT NULL,
			harness TEXT NOT NULL,
			created_at TEXT NOT NULL,
			feedback_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS memory_feedback_project_target
			ON memory_feedback_events(project_id, target_kind, target_id, target_revision, sequence)`,
		`CREATE INDEX IF NOT EXISTS memory_feedback_project_task
			ON memory_feedback_events(project_id, task_id, sequence)`,
		`CREATE TRIGGER IF NOT EXISTS memory_feedback_events_no_update BEFORE UPDATE ON memory_feedback_events BEGIN SELECT RAISE(ABORT, 'memory feedback is append-only'); END`,
		`CREATE TRIGGER IF NOT EXISTS memory_feedback_events_no_delete BEFORE DELETE ON memory_feedback_events BEGIN SELECT RAISE(ABORT, 'memory feedback is append-only'); END`,
		`PRAGMA user_version = 13`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize sqlite store: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateProposal(ctx context.Context, graph proceduregraph.Graph, actor, reason string, at time.Time) error {
	if graph.Status != proceduregraph.StatusDraft || graph.Revision != 1 {
		return errors.New("new proposals must start as draft revision 1")
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return errors.New("proposal actor and reason are required")
	}
	if err := proceduregraph.Validate(graph); err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		payload, err := json.Marshal(graph)
		if err != nil {
			return err
		}
		stamp := at.UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO procedure_graphs(id, project_id, revision, status, graph_json, updated_at)
			 VALUES(?, ?, ?, ?, ?, ?)`, graph.ID, graph.ProjectID, graph.Revision, graph.Status, string(payload), stamp,
		); err != nil {
			return fmt.Errorf("create proposal: %w", err)
		}
		return appendAudit(ctx, tx, graph.ID, graph.Revision, "proposed", actor, reason, stamp)
	})
}

func (s *Store) RecordValidation(ctx context.Context, id string, expectedRevision int, report proceduregraph.Validation, at time.Time) (proceduregraph.Graph, error) {
	var updated proceduregraph.Graph
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		graph, err := readGraphTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if graph.Revision != expectedRevision {
			return ErrConflict
		}
		if graph.Status != proceduregraph.StatusDraft {
			return fmt.Errorf("validation requires draft status, got %s", graph.Status)
		}
		graph.Validation = &report
		if report.PassesGate() {
			graph.Status = proceduregraph.StatusValidated
		} else {
			graph.Status = proceduregraph.StatusRejected
		}
		graph.Revision++
		if err := proceduregraph.Validate(graph); err != nil {
			return err
		}
		stamp := at.UTC().Format(time.RFC3339Nano)
		if err := updateGraphTx(ctx, tx, graph, expectedRevision, stamp); err != nil {
			return err
		}
		action := "validation_failed"
		if graph.Status == proceduregraph.StatusValidated {
			action = "validation_passed"
		}
		if err := appendAudit(ctx, tx, graph.ID, graph.Revision, action, "validator", report.DatasetFingerprint, stamp); err != nil {
			return err
		}
		updated = graph
		return nil
	})
	return updated, err
}

func (s *Store) Approve(ctx context.Context, id string, expectedRevision int, reviewer, reason string, privacyReviewed bool, at time.Time) (proceduregraph.Graph, error) {
	var updated proceduregraph.Graph
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		graph, err := readGraphTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if graph.Revision != expectedRevision {
			return ErrConflict
		}
		approved, err := proceduregraph.Approve(graph, reviewer, reason, at.UTC().Format(time.RFC3339), privacyReviewed)
		if err != nil {
			return err
		}
		stamp := at.UTC().Format(time.RFC3339Nano)
		if err := updateGraphTx(ctx, tx, approved, expectedRevision, stamp); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, approved.ID, approved.Revision, "approved", reviewer, reason, stamp); err != nil {
			return err
		}
		updated = approved
		return nil
	})
	return updated, err
}

func (s *Store) Reject(ctx context.Context, id string, expectedRevision int, actor, reason string, at time.Time) (proceduregraph.Graph, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return proceduregraph.Graph{}, errors.New("rejecting actor and reason are required")
	}
	var updated proceduregraph.Graph
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		graph, err := readGraphTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if graph.Revision != expectedRevision {
			return ErrConflict
		}
		if graph.Status != proceduregraph.StatusDraft && graph.Status != proceduregraph.StatusValidated {
			return fmt.Errorf("cannot reject graph in %s state", graph.Status)
		}
		graph.Status = proceduregraph.StatusRejected
		graph.Revision++
		stamp := at.UTC().Format(time.RFC3339Nano)
		if err := updateGraphTx(ctx, tx, graph, expectedRevision, stamp); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, graph.ID, graph.Revision, "rejected", actor, reason, stamp); err != nil {
			return err
		}
		updated = graph
		return nil
	})
	return updated, err
}

func (s *Store) Get(ctx context.Context, id string) (proceduregraph.Graph, error) {
	return readGraphDB(ctx, s.db, id)
}

func (s *Store) ListProject(ctx context.Context, projectID string) ([]proceduregraph.Graph, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT graph_json FROM procedure_graphs WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var graphs []proceduregraph.Graph
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var graph proceduregraph.Graph
		if err := json.Unmarshal([]byte(payload), &graph); err != nil {
			return nil, fmt.Errorf("decode stored graph: %w", err)
		}
		graphs = append(graphs, graph)
	}
	return graphs, rows.Err()
}

func (s *Store) Audit(ctx context.Context, graphID string) ([]AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sequence, graph_id, revision, action, actor, reason, created_at
		 FROM audit_events WHERE graph_id = ? ORDER BY sequence`, graphID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		if err := rows.Scan(&event.Sequence, &event.GraphID, &event.Revision, &event.Action, &event.Actor, &event.Reason, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type ImportedRecord struct {
	Key           string          `json:"key"`
	Type          string          `json:"type"`
	SourcePath    string          `json:"source_path"`
	ContentSHA256 string          `json:"content_sha256"`
	Scope         string          `json:"scope"`
	Payload       json.RawMessage `json:"payload"`
}

type MigrationReceipt struct {
	ID                string `json:"id"`
	Product           string `json:"product"`
	SourceFingerprint string `json:"source_fingerprint"`
	Status            string `json:"status"`
	ExpectedCount     int    `json:"expected_count"`
	ImportedCount     int    `json:"imported_count"`
	ExcludedCount     int    `json:"excluded_count"`
	CreatedAt         string `json:"created_at"`
	RolledBackAt      string `json:"rolled_back_at,omitempty"`
}

type MigrationAuditEvent struct {
	Sequence    int64  `json:"sequence"`
	MigrationID string `json:"migration_id"`
	Action      string `json:"action"`
	Actor       string `json:"actor"`
	Reason      string `json:"reason"`
	CreatedAt   string `json:"created_at"`
}

type MigrationProgress struct {
	StagedRecords int `json:"staged_records"`
	TotalRecords  int `json:"total_records"`
}

func (s *Store) ApplyMigration(ctx context.Context, receipt MigrationReceipt, records []ImportedRecord, at time.Time) (MigrationReceipt, bool, error) {
	return s.applyMigrationWithProgress(ctx, receipt, records, at, nil, nil)
}

// ApplyMigrationWithProgress reports staged row counts from inside the atomic
// transaction. Returning an error from progress aborts the transaction.
func (s *Store) ApplyMigrationWithProgress(ctx context.Context, receipt MigrationReceipt, records []ImportedRecord,
	at time.Time, progress func(MigrationProgress) error) (MigrationReceipt, bool, error) {
	return s.applyMigrationWithProgress(ctx, receipt, records, at, nil, progress)
}

func (s *Store) applyMigration(ctx context.Context, receipt MigrationReceipt, records []ImportedRecord, at time.Time, beforeCommit func() error) (MigrationReceipt, bool, error) {
	return s.applyMigrationWithProgress(ctx, receipt, records, at, beforeCommit, nil)
}

func (s *Store) applyMigrationWithProgress(ctx context.Context, receipt MigrationReceipt, records []ImportedRecord,
	at time.Time, beforeCommit func() error, progress func(MigrationProgress) error) (MigrationReceipt, bool, error) {
	if receipt.ID == "" || receipt.Product == "" || !isSHA256Digest(receipt.SourceFingerprint) {
		return MigrationReceipt{}, false, errors.New("migration ID, product, and source fingerprint are required")
	}
	if receipt.ExpectedCount != len(records) || receipt.ExcludedCount < 0 {
		return MigrationReceipt{}, false, errors.New("migration record counts do not reconcile")
	}
	if receipt.Status != "" && receipt.Status != "applied" {
		return MigrationReceipt{}, false, errors.New("migration receipt status must be applied")
	}
	for _, record := range records {
		if record.Key == "" || record.Type == "" || record.SourcePath == "" || record.Scope != "local_pending_review" ||
			!isSHA256Digest(record.ContentSHA256) || !json.Valid(record.Payload) || len(record.Payload) > 4<<20 {
			return MigrationReceipt{}, false, errors.New("migration contains an invalid or non-local pending record")
		}
		if sha256Hex(record.Payload) != record.ContentSHA256 {
			return MigrationReceipt{}, false, fmt.Errorf("migration payload digest mismatch for %s", record.Key)
		}
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	receipt.CreatedAt = stamp
	receipt.Status = "applied"
	alreadyApplied := false
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var existing MigrationReceipt
		err := tx.QueryRowContext(ctx,
			`SELECT id, product, source_fingerprint, status, expected_count, imported_count, excluded_count, created_at, COALESCE(rolled_back_at, '')
			 FROM migrations WHERE id = ?`, receipt.ID,
		).Scan(&existing.ID, &existing.Product, &existing.SourceFingerprint, &existing.Status,
			&existing.ExpectedCount, &existing.ImportedCount, &existing.ExcludedCount, &existing.CreatedAt, &existing.RolledBackAt)
		if err == nil {
			if existing.Product != receipt.Product || existing.SourceFingerprint != receipt.SourceFingerprint {
				return ErrMigrationConflict
			}
			if existing.Status == "applied" {
				if existing.ExpectedCount != len(records) || existing.ImportedCount != len(records) || existing.ExcludedCount != receipt.ExcludedCount {
					return ErrMigrationConflict
				}
				for _, record := range records {
					var existingType, existingPath, existingDigest, existingScope, existingPayload string
					err := tx.QueryRowContext(ctx, `SELECT record_type, source_path, content_sha256, scope, payload_json FROM imported_records WHERE migration_id = ? AND record_key = ?`, receipt.ID, record.Key).Scan(&existingType, &existingPath, &existingDigest, &existingScope, &existingPayload)
					if err != nil || existingType != record.Type || existingPath != record.SourcePath || existingDigest != record.ContentSHA256 ||
						existingScope != record.Scope || existingPayload != string(record.Payload) || sha256Hex([]byte(existingPayload)) != record.ContentSHA256 {
						return ErrMigrationConflict
					}
				}
				var actualCount int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM imported_records WHERE migration_id = ?`, receipt.ID).Scan(&actualCount); err != nil {
					return err
				}
				if actualCount != len(records) {
					return ErrMigrationConflict
				}
				receipt = existing
				alreadyApplied = true
				return nil
			}
			if existing.Status != "rolled_back" {
				return fmt.Errorf("migration %s is in non-recoverable state %s", existing.ID, existing.Status)
			}
			receipt.CreatedAt = existing.CreatedAt
			if _, err := tx.ExecContext(ctx,
				`UPDATE migrations SET status = 'staging', expected_count = ?, imported_count = 0, excluded_count = ?, rolled_back_at = NULL WHERE id = ?`,
				receipt.ExpectedCount, receipt.ExcludedCount, receipt.ID); err != nil {
				return err
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO migrations(id, product, source_fingerprint, status, expected_count, imported_count, excluded_count, created_at)
				 VALUES(?, ?, ?, 'staging', ?, 0, ?, ?)`,
				receipt.ID, receipt.Product, receipt.SourceFingerprint, receipt.ExpectedCount, receipt.ExcludedCount, stamp); err != nil {
				return err
			}
		} else {
			return err
		}
		for index, record := range records {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO imported_records(migration_id, record_key, record_type, source_path, content_sha256, scope, payload_json)
				 VALUES(?, ?, ?, ?, ?, ?, ?)`, receipt.ID, record.Key, record.Type, record.SourcePath,
				record.ContentSHA256, record.Scope, string(record.Payload)); err != nil {
				return fmt.Errorf("stage imported record %s: %w", record.Key, err)
			}
			if progress != nil {
				if err := progress(MigrationProgress{StagedRecords: index + 1, TotalRecords: len(records)}); err != nil {
					return fmt.Errorf("report migration staging progress: %w", err)
				}
			}
		}
		var actual int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM imported_records WHERE migration_id = ?`, receipt.ID).Scan(&actual); err != nil {
			return err
		}
		if actual != receipt.ExpectedCount {
			return fmt.Errorf("migration record reconciliation failed: expected %d, stored %d", receipt.ExpectedCount, actual)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE migrations SET status = 'applied', imported_count = ? WHERE id = ?`, actual, receipt.ID); err != nil {
			return err
		}
		action := "applied"
		if existing.Status == "rolled_back" {
			action = "reapplied"
		}
		if err := appendMigrationAudit(ctx, tx, receipt.ID, action, "threadforge-cli", fmt.Sprintf("imported=%d excluded=%d fingerprint=%s", actual, receipt.ExcludedCount, receipt.SourceFingerprint), stamp); err != nil {
			return err
		}
		receipt.ImportedCount = actual
		receipt.Status = "applied"
		if beforeCommit != nil {
			if err := beforeCommit(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return MigrationReceipt{}, false, err
	}
	return receipt, alreadyApplied, nil
}

func (s *Store) RollbackMigration(ctx context.Context, migrationID, actor, reason string, at time.Time) (MigrationReceipt, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return MigrationReceipt{}, errors.New("migration rollback actor and reason are required")
	}
	var receipt MigrationReceipt
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT id, product, source_fingerprint, status, expected_count, imported_count, excluded_count, created_at, COALESCE(rolled_back_at, '')
			 FROM migrations WHERE id = ?`, migrationID,
		).Scan(&receipt.ID, &receipt.Product, &receipt.SourceFingerprint, &receipt.Status,
			&receipt.ExpectedCount, &receipt.ImportedCount, &receipt.ExcludedCount, &receipt.CreatedAt, &receipt.RolledBackAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if receipt.Status == "rolled_back" {
			return nil
		}
		if receipt.Status != "applied" {
			return fmt.Errorf("cannot roll back migration in %s state", receipt.Status)
		}
		stamp := at.UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `DELETE FROM imported_records WHERE migration_id = ?`, migrationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE migrations SET status = 'rolled_back', rolled_back_at = ? WHERE id = ?`, stamp, migrationID); err != nil {
			return err
		}
		if err := appendMigrationAudit(ctx, tx, migrationID, "rolled_back", actor, reason, stamp); err != nil {
			return err
		}
		receipt.Status = "rolled_back"
		receipt.RolledBackAt = stamp
		return nil
	})
	return receipt, err
}

func (s *Store) ListMigrations(ctx context.Context) ([]MigrationReceipt, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, product, source_fingerprint, status, expected_count, imported_count, excluded_count, created_at, COALESCE(rolled_back_at, '')
		 FROM migrations ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var receipts []MigrationReceipt
	for rows.Next() {
		var receipt MigrationReceipt
		if err := rows.Scan(&receipt.ID, &receipt.Product, &receipt.SourceFingerprint, &receipt.Status,
			&receipt.ExpectedCount, &receipt.ImportedCount, &receipt.ExcludedCount, &receipt.CreatedAt, &receipt.RolledBackAt); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}

func (s *Store) GetMigration(ctx context.Context, migrationID string) (MigrationReceipt, error) {
	var receipt MigrationReceipt
	err := s.db.QueryRowContext(ctx,
		`SELECT id, product, source_fingerprint, status, expected_count, imported_count, excluded_count, created_at, COALESCE(rolled_back_at, '')
		 FROM migrations WHERE id = ?`, migrationID,
	).Scan(&receipt.ID, &receipt.Product, &receipt.SourceFingerprint, &receipt.Status,
		&receipt.ExpectedCount, &receipt.ImportedCount, &receipt.ExcludedCount, &receipt.CreatedAt, &receipt.RolledBackAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MigrationReceipt{}, ErrMigrationNotFound
	}
	return receipt, err
}

func (s *Store) ListMigrationRecords(ctx context.Context, migrationID string, limit, offset int) ([]ImportedRecord, int, error) {
	if limit < 1 || limit > 500 || offset < 0 {
		return nil, 0, errors.New("migration record limit must be 1-500 and offset non-negative")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM imported_records WHERE migration_id = ?`, migrationID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT record_key, record_type, source_path, content_sha256, scope, payload_json
		 FROM imported_records WHERE migration_id = ? ORDER BY record_key LIMIT ? OFFSET ?`, migrationID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var records []ImportedRecord
	for rows.Next() {
		var record ImportedRecord
		var payload string
		if err := rows.Scan(&record.Key, &record.Type, &record.SourcePath, &record.ContentSHA256, &record.Scope, &payload); err != nil {
			return nil, 0, err
		}
		record.Payload = json.RawMessage(payload)
		records = append(records, record)
	}
	return records, total, rows.Err()
}

func (s *Store) MigrationAudit(ctx context.Context, migrationID string) ([]MigrationAuditEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sequence, migration_id, action, actor, reason, created_at FROM migration_audit WHERE migration_id = ? ORDER BY sequence`, migrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []MigrationAuditEvent
	for rows.Next() {
		var event MigrationAuditEvent
		if err := rows.Scan(&event.Sequence, &event.MigrationID, &event.Action, &event.Actor, &event.Reason, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func appendMigrationAudit(ctx context.Context, tx *sql.Tx, migrationID, action, actor, reason, stamp string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO migration_audit(migration_id, action, actor, reason, created_at) VALUES(?, ?, ?, ?, ?)`,
		migrationID, action, actor, reason, stamp)
	return err
}

func sha256Hex(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}

func isSHA256Digest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readGraphDB(ctx context.Context, q queryer, id string) (proceduregraph.Graph, error) {
	var payload string
	err := q.QueryRowContext(ctx, `SELECT graph_json FROM procedure_graphs WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return proceduregraph.Graph{}, ErrNotFound
	}
	if err != nil {
		return proceduregraph.Graph{}, err
	}
	var graph proceduregraph.Graph
	if err := json.Unmarshal([]byte(payload), &graph); err != nil {
		return proceduregraph.Graph{}, fmt.Errorf("decode stored graph: %w", err)
	}
	return graph, nil
}

func readGraphTx(ctx context.Context, tx *sql.Tx, id string) (proceduregraph.Graph, error) {
	return readGraphDB(ctx, tx, id)
}

func updateGraphTx(ctx context.Context, tx *sql.Tx, graph proceduregraph.Graph, expectedRevision int, stamp string) error {
	payload, err := json.Marshal(graph)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE procedure_graphs SET project_id = ?, revision = ?, status = ?, graph_json = ?, updated_at = ?
		 WHERE id = ? AND revision = ?`,
		graph.ProjectID, graph.Revision, graph.Status, string(payload), stamp, graph.ID, expectedRevision)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

func appendAudit(ctx context.Context, tx *sql.Tx, graphID string, revision int, action, actor, reason, stamp string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_events(graph_id, revision, action, actor, reason, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		graphID, revision, action, actor, reason, stamp)
	return err
}
