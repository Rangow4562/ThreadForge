package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const MaxLocalRecoveryDatabaseBytes int64 = 16 << 30
const CurrentLocalRecoverySchemaVersion = CurrentSchemaVersion
const MaxRecoveryTeamProjects = 10000

type LocalRecoveryDatabaseInfo struct {
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
	UserVersion   int    `json:"user_version"`
	RecoveryState string `json:"recovery_state"`
	RecoveryID    string `json:"recovery_id,omitempty"`
}

type onlineBackupConnection interface {
	NewBackup(string) (*sqlite.Backup, error)
	NewRestore(string) (*sqlite.Backup, error)
}

// CreateLocalRecoverySnapshot writes a transaction-consistent SQLite copy to
// an already-created empty regular file. It never publishes or shares the file.
func (s *Store) CreateLocalRecoverySnapshot(ctx context.Context, target string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != 0 {
		return errors.New("recovery snapshot destination must be an existing empty regular file")
	}
	connection, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Raw(func(raw any) error {
		backuper, ok := raw.(onlineBackupConnection)
		if !ok {
			return errors.New("SQLite driver does not expose the required online-backup API")
		}
		backup, err := backuper.NewBackup(absolute)
		if err != nil {
			return fmt.Errorf("start local SQLite snapshot: %w", err)
		}
		finished := false
		finish := func() error {
			if finished {
				return nil
			}
			finished = true
			return backup.Finish()
		}
		defer func() { _ = finish() }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if err := ctx.Err(); err != nil {
				_ = finish()
				return err
			}
			more, stepErr := backup.Step(128)
			if stepErr != nil {
				if isSQLiteBusy(stepErr) && time.Now().Before(deadline) {
					select {
					case <-ctx.Done():
						_ = finish()
						return ctx.Err()
					case <-time.After(25 * time.Millisecond):
						continue
					}
				}
				finishErr := finish()
				if finishErr != nil {
					return fmt.Errorf("snapshot failed: %v; close snapshot: %w", stepErr, finishErr)
				}
				return fmt.Errorf("copy SQLite snapshot pages: %w", stepErr)
			}
			pages := int64(backup.PageCount())
			if pages > MaxLocalRecoveryDatabaseBytes/(64<<10) {
				_ = finish()
				return errors.New("local SQLite snapshot exceeds the 16 GiB safety limit")
			}
			if !more {
				break
			}
		}
		if err := finish(); err != nil {
			return fmt.Errorf("finish local SQLite snapshot: %w", err)
		}
		return nil
	})
}

// ActivateStagedRecovery makes a previously restored copy usable only after
// its team ledger verifies and every registered team scope is frozen.
func (s *Store) ActivateStagedRecovery(ctx context.Context, recoveryID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !validRecoveryID(recoveryID) {
		return errors.New("valid staged recovery id is required")
	}
	report, err := s.VerifyTeamContinuityAudit(ctx)
	if err != nil {
		return fmt.Errorf("verify staged team ledger before activation: %w", err)
	}
	if (report.TeamProjects == 0 && report.Status != "no_team_projects") ||
		(report.TeamProjects > 0 && report.Status != "passed") {
		return fmt.Errorf("staged team ledger status %q does not permit activation", report.Status)
	}
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		var state, storedID string
		if err := tx.QueryRowContext(ctx,
			`SELECT state, recovery_id FROM store_recovery_state WHERE singleton = 1`).Scan(&state, &storedID); err != nil {
			return err
		}
		if state != "staged" || storedID != recoveryID {
			return errors.New("database is not the expected staged recovery")
		}
		rows, err := tx.QueryContext(ctx, `SELECT project_id FROM team_project_scopes ORDER BY project_id`)
		if err != nil {
			return err
		}
		projects := make([]string, 0)
		for rows.Next() {
			var projectID string
			if err := rows.Scan(&projectID); err != nil {
				_ = rows.Close()
				return err
			}
			projects = append(projects, projectID)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(projects) > MaxRecoveryTeamProjects {
			return errors.New("staged recovery has too many team scopes for bounded activation")
		}
		for _, projectID := range projects {
			decommissioned, err := teamProjectScopeIsDecommissioned(ctx, tx, projectID)
			if err != nil {
				return err
			}
			if !decommissioned {
				return fmt.Errorf("team project scope %q is not frozen; staged recovery remains inactive", projectID)
			}
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE store_recovery_state SET state = 'active', updated_at = ? WHERE singleton = 1 AND state = 'staged' AND recovery_id = ?`,
			time.Now().UTC().Format(time.RFC3339Nano), recoveryID)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return errors.New("staged recovery activation state changed concurrently")
		}
		return nil
	}); err != nil {
		return err
	}
	var busy, logFrames, checkpointed int
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("checkpoint activated recovery database: %w", err)
	}
	if busy != 0 {
		return errors.New("activated recovery database has a busy WAL checkpoint")
	}
	return nil
}

// RestoreLocalRecoverySnapshot restores a verified SQLite file into an
// already-created empty destination database. Callers should use a new staged
// destination, never replace the active database in place.
func RestoreLocalRecoverySnapshot(ctx context.Context, source, target, recoveryID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !validRecoveryID(recoveryID) {
		return errors.New("recovery id must contain 1 to 64 lowercase letters, digits, or hyphens and start with a letter or digit")
	}
	sourceInfo, err := InspectLocalRecoveryDatabase(ctx, source)
	if err != nil {
		return fmt.Errorf("verify recovery source before restore: %w", err)
	}
	sourceAbsolute, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	targetAbsolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if filepath.Clean(sourceAbsolute) == filepath.Clean(targetAbsolute) {
		return errors.New("recovery source and destination must be different files")
	}
	targetInfo, err := os.Lstat(targetAbsolute)
	if err != nil || !targetInfo.Mode().IsRegular() || targetInfo.Mode()&os.ModeSymlink != 0 || targetInfo.Size() != 0 {
		return errors.New("recovery restore destination must be an existing empty regular file")
	}
	database, err := sql.Open("sqlite", targetAbsolute)
	if err != nil {
		return fmt.Errorf("open staged recovery destination: %w", err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	connection, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	sourceURI, err := sqliteReadOnlyURI(sourceAbsolute)
	if err != nil {
		return err
	}
	err = connection.Raw(func(raw any) error {
		backuper, ok := raw.(onlineBackupConnection)
		if !ok {
			return errors.New("SQLite driver does not expose the required online-restore API")
		}
		backup, err := backuper.NewRestore(sourceURI)
		if err != nil {
			return fmt.Errorf("start staged SQLite restore: %w", err)
		}
		finished := false
		finish := func() error {
			if finished {
				return nil
			}
			finished = true
			return backup.Finish()
		}
		defer func() { _ = finish() }()
		for {
			if err := ctx.Err(); err != nil {
				_ = finish()
				return err
			}
			more, stepErr := backup.Step(128)
			if stepErr != nil {
				finishErr := finish()
				if finishErr != nil {
					return fmt.Errorf("restore failed: %v; close restore: %w", stepErr, finishErr)
				}
				return fmt.Errorf("copy recovery database pages: %w", stepErr)
			}
			if !more {
				return finish()
			}
		}
	})
	if err != nil {
		return err
	}
	latestSourceInfo, err := InspectLocalRecoveryDatabase(ctx, sourceAbsolute)
	if err != nil || latestSourceInfo != sourceInfo {
		return errors.New("recovery source changed while it was being restored")
	}
	if _, err := connection.ExecContext(ctx,
		`UPDATE store_recovery_state SET state = 'staged', recovery_id = ?, updated_at = ? WHERE singleton = 1`,
		recoveryID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("mark restored database as staged: %w", err)
	}
	if err := connection.Close(); err != nil {
		return err
	}
	if _, err := InspectLocalRecoveryDatabase(ctx, targetAbsolute); err != nil {
		return fmt.Errorf("verify staged recovery after restore: %w", err)
	}
	return nil
}

// InspectLocalRecoveryDatabase checks a bounded regular SQLite file without
// allowing SQLite to create or mutate it.
func InspectLocalRecoveryDatabase(ctx context.Context, filename string) (LocalRecoveryDatabaseInfo, error) {
	var result LocalRecoveryDatabaseInfo
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return result, err
	}
	before, err := os.Lstat(absolute)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() < 1 || before.Size() > MaxLocalRecoveryDatabaseBytes {
		return result, errors.New("recovery database must be a bounded regular file")
	}
	uri, err := sqliteReadOnlyURI(absolute)
	if err != nil {
		return result, err
	}
	database, err := sql.Open("sqlite", uri)
	if err != nil {
		return result, errors.New("open recovery database read-only")
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	if ctx == nil {
		ctx = context.Background()
	}
	var integrity string
	if err := database.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&integrity); err != nil || integrity != "ok" {
		return result, errors.New("recovery database failed SQLite integrity check")
	}
	if err := database.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&result.UserVersion); err != nil ||
		result.UserVersion != CurrentLocalRecoverySchemaVersion {
		return LocalRecoveryDatabaseInfo{}, errors.New("recovery database schema version is unsupported")
	}
	for _, table := range []string{"procedure_graphs", "continuity_tasks", "team_project_scopes", "team_project_scope_events", "store_recovery_state"} {
		var count int
		if err := database.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 1 {
			return LocalRecoveryDatabaseInfo{}, errors.New("recovery database does not have the expected Threadforge schema")
		}
	}
	if err := database.QueryRowContext(ctx,
		`SELECT state, recovery_id FROM store_recovery_state WHERE singleton = 1`).Scan(&result.RecoveryState, &result.RecoveryID); err != nil ||
		(result.RecoveryState != "active" && result.RecoveryState != "staged") ||
		(result.RecoveryState == "staged" && !validRecoveryID(result.RecoveryID)) {
		return LocalRecoveryDatabaseInfo{}, errors.New("recovery database activation state is invalid")
	}
	foreignKeys, err := database.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return LocalRecoveryDatabaseInfo{}, errors.New("recovery database foreign-key check failed")
	}
	if foreignKeys.Next() {
		_ = foreignKeys.Close()
		return LocalRecoveryDatabaseInfo{}, errors.New("recovery database contains a foreign-key violation")
	}
	if err := foreignKeys.Close(); err != nil {
		return LocalRecoveryDatabaseInfo{}, errors.New("recovery database foreign-key check could not finish")
	}
	if err := database.Close(); err != nil {
		return LocalRecoveryDatabaseInfo{}, err
	}
	after, err := os.Lstat(absolute)
	if err != nil || !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return LocalRecoveryDatabaseInfo{}, errors.New("recovery database changed during verification")
	}
	hash, size, err := hashRecoveryFile(absolute)
	if err != nil || size != after.Size() {
		return LocalRecoveryDatabaseInfo{}, errors.New("recovery database could not be hashed consistently")
	}
	result.SizeBytes = size
	result.SHA256 = hash
	return result, nil
}

func validRecoveryID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	first := id[0]
	if (first < 'a' || first > 'z') && (first < '0' || first > '9') {
		return false
	}
	for _, char := range id {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func sqliteReadOnlyURI(absolute string) (string, error) {
	uriPath := filepath.ToSlash(absolute)
	if strings.HasPrefix(uriPath, "//") {
		return "", errors.New("network-path recovery databases are not supported")
	}
	if filepath.VolumeName(absolute) != "" {
		uriPath = "/" + strings.TrimLeft(uriPath, "/")
	}
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "immutable=1&mode=ro"}).String(), nil
}

func hashRecoveryFile(filename string) (string, int64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hasher := sha256.New()
	limited := &io.LimitedReader{R: file, N: MaxLocalRecoveryDatabaseBytes + 1}
	size, err := io.Copy(hasher, limited)
	if err != nil || size > MaxLocalRecoveryDatabaseBytes {
		return "", 0, errors.New("recovery database exceeds the 16 GiB safety limit")
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func isSQLiteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code() & 0xff
	return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
}
