package store

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrationApplyIsIdempotentAndRollbackIsAudited(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	payload := json.RawMessage(`{"title":"approved memory pending review"}`)
	contentHash := sha256.Sum256(payload)
	records := []ImportedRecord{{
		Key: "history.jsonl#000001", Type: "evolution_history", SourcePath: "runs/demo/history.jsonl",
		ContentSHA256: hex.EncodeToString(contentHash[:]), Scope: "local_pending_review", Payload: payload,
	}}
	receipt := MigrationReceipt{ID: "rrsi-" + strings.Repeat("a", 20), Product: "rrsi",
		SourceFingerprint: strings.Repeat("a", 64), ExpectedCount: 1, ExcludedCount: 2}
	first, already, err := store.ApplyMigration(ctx, receipt, records, time.Now())
	if err != nil || already || first.Status != "applied" || first.ImportedCount != 1 {
		t.Fatalf("first apply = %#v already=%v err=%v", first, already, err)
	}
	second, already, err := store.ApplyMigration(ctx, receipt, records, time.Now().Add(time.Minute))
	if err != nil || !already || second.CreatedAt != first.CreatedAt {
		t.Fatalf("idempotent apply = %#v already=%v err=%v", second, already, err)
	}
	storedReceipt, err := store.GetMigration(ctx, receipt.ID)
	if err != nil || storedReceipt.Status != "applied" || storedReceipt.ImportedCount != 1 {
		t.Fatalf("get applied migration = %#v err=%v", storedReceipt, err)
	}
	stored, total, err := store.ListMigrationRecords(ctx, receipt.ID, 10, 0)
	if err != nil || total != 1 || len(stored) != 1 || stored[0].Scope != "local_pending_review" {
		t.Fatalf("staged records=%#v total=%d err=%v", stored, total, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE imported_records SET payload_json = '{}' WHERE migration_id = ? AND record_key = ?`, receipt.ID, records[0].Key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApplyMigration(ctx, receipt, records, time.Now().Add(90*time.Second)); !errors.Is(err, ErrMigrationConflict) {
		t.Fatalf("idempotent apply accepted a tampered payload: %v", err)
	}
	rolled, err := store.RollbackMigration(ctx, receipt.ID, "operator", "verified source snapshot retained", time.Now().Add(2*time.Minute))
	if err != nil || rolled.Status != "rolled_back" {
		t.Fatalf("rollback = %#v err=%v", rolled, err)
	}
	_, total, err = store.ListMigrationRecords(ctx, receipt.ID, 10, 0)
	if err != nil || total != 0 {
		t.Fatalf("rollback left imported rows: total=%d err=%v", total, err)
	}
	audit, err := store.MigrationAudit(ctx, receipt.ID)
	if err != nil || len(audit) != 2 || audit[0].Action != "applied" || audit[1].Action != "rolled_back" {
		t.Fatalf("migration audit=%#v err=%v", audit, err)
	}
	reapplied, already, err := store.ApplyMigration(ctx, receipt, records, time.Now().Add(3*time.Minute))
	if err != nil || already || reapplied.Status != "applied" {
		t.Fatalf("reapply after rollback = %#v already=%v err=%v", reapplied, already, err)
	}
}

func TestMigrationApplyDatabaseFailureIsAtomicAndRetryable(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	payloads := []json.RawMessage{json.RawMessage(`{"event":"one"}`), json.RawMessage(`{"event":"two"}`)}
	records := make([]ImportedRecord, len(payloads))
	for index, payload := range payloads {
		digest := sha256.Sum256(payload)
		records[index] = ImportedRecord{
			Key: "runtime.jsonl#" + strings.Repeat("0", 5) + string(rune('1'+index)), Type: "telemetry_event",
			SourcePath: "runtime.jsonl", ContentSHA256: hex.EncodeToString(digest[:]),
			Scope: "local_pending_review", Payload: payload,
		}
	}
	receipt := MigrationReceipt{ID: "beacon-" + strings.Repeat("d", 20), Product: "beacon",
		SourceFingerprint: strings.Repeat("d", 64), ExpectedCount: len(records)}
	_, err = database.db.ExecContext(ctx, `CREATE TRIGGER fail_second_migration_record
		BEFORE INSERT ON imported_records WHEN NEW.record_key = 'runtime.jsonl#000002'
		BEGIN SELECT RAISE(ABORT, 'injected migration failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.ApplyMigration(ctx, receipt, records, time.Now()); err == nil {
		t.Fatal("injected database failure did not abort migration")
	}
	if _, err := database.GetMigration(ctx, receipt.ID); !errors.Is(err, ErrMigrationNotFound) {
		t.Fatalf("failed transaction left a migration receipt: %v", err)
	}
	if _, count, err := database.ListMigrationRecords(ctx, receipt.ID, 10, 0); err != nil || count != 0 {
		t.Fatalf("failed transaction left staged rows: count=%d err=%v", count, err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP TRIGGER fail_second_migration_record`); err != nil {
		t.Fatal(err)
	}
	if applied, already, err := database.ApplyMigration(ctx, receipt, records, time.Now().Add(time.Minute)); err != nil || already || applied.Status != "applied" {
		t.Fatalf("retry after atomic failure = %#v already=%v err=%v", applied, already, err)
	}
}

func TestMigrationApplyProcessDeathRollsBackAndCanRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration-crash.sqlite")
	cmd := exec.Command(os.Args[0], "-test.run=^TestMigrationProcessCrashChild$")
	cmd.Env = append(os.Environ(), "THREADFORGE_MIGRATION_CRASH_HELPER=1", "THREADFORGE_MIGRATION_CRASH_DB="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "MIGRATION_TRANSACTION_READY" {
				ready <- nil
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ready <- err
			return
		}
		ready <- errors.New("child exited without entering its uncommitted migration transaction")
	}()
	select {
	case err := <-ready:
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("migration child did not reach crash point: %v; stderr=%s", err, stderr.String())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("migration child did not reach crash point; stderr=%s", stderr.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		_ = cmd.Wait()
		t.Fatalf("kill importer during open transaction: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("migration child was expected to terminate by process kill")
	}

	database, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after importer process death: %v", err)
	}
	defer database.Close()
	receipt, records := migrationCrashFixture()
	if _, err := database.GetMigration(context.Background(), receipt.ID); !errors.Is(err, ErrMigrationNotFound) {
		t.Fatalf("process death left a migration receipt: %v", err)
	}
	if rows, count, err := database.ListMigrationRecords(context.Background(), receipt.ID, 10, 0); err != nil || count != 0 || len(rows) != 0 {
		t.Fatalf("process death left partial imported rows=%#v count=%d err=%v", rows, count, err)
	}
	if applied, already, err := database.ApplyMigration(context.Background(), receipt, records, time.Now()); err != nil || already || applied.Status != "applied" || applied.ImportedCount != len(records) {
		t.Fatalf("retry after process death = %#v already=%v err=%v", applied, already, err)
	}
	audit, err := database.MigrationAudit(context.Background(), receipt.ID)
	if err != nil || len(audit) != 1 || audit[0].Action != "applied" {
		t.Fatalf("retry after crash audit = %#v err=%v", audit, err)
	}
}

func TestMigrationProcessCrashChild(t *testing.T) {
	if os.Getenv("THREADFORGE_MIGRATION_CRASH_HELPER") != "1" {
		return
	}
	path := os.Getenv("THREADFORGE_MIGRATION_CRASH_DB")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt, records := migrationCrashFixture()
	_, _, err = database.applyMigration(context.Background(), receipt, records, time.Now(), func() error {
		if _, err := fmt.Fprintln(os.Stdout, "MIGRATION_TRANSACTION_READY"); err != nil {
			return err
		}
		select {}
	})
	if err != nil {
		t.Fatalf("migration failed before kill point: %v", err)
	}
	t.Fatal("migration crash child returned unexpectedly")
}

func migrationCrashFixture() (MigrationReceipt, []ImportedRecord) {
	payloads := []json.RawMessage{json.RawMessage(`{"item":"first"}`), json.RawMessage(`{"item":"second"}`)}
	records := make([]ImportedRecord, len(payloads))
	for index, payload := range payloads {
		digest := sha256.Sum256(payload)
		records[index] = ImportedRecord{
			Key: "source.jsonl#" + string(rune('1'+index)), Type: "telemetry_event",
			SourcePath: "source.jsonl", ContentSHA256: hex.EncodeToString(digest[:]),
			Scope: "local_pending_review", Payload: payload,
		}
	}
	receipt := MigrationReceipt{
		ID: "crash-recovery-" + strings.Repeat("f", 16), Product: "beacon",
		SourceFingerprint: strings.Repeat("f", 64), ExpectedCount: len(records),
	}
	return receipt, records
}

func TestMigrationRejectsMismatchedCountsAndChangedFingerprint(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	receipt := MigrationReceipt{ID: "beacon-" + strings.Repeat("b", 20), Product: "beacon",
		SourceFingerprint: strings.Repeat("b", 64), ExpectedCount: 2}
	payload := json.RawMessage(`{"event":"one"}`)
	hash := sha256.Sum256(payload)
	record := ImportedRecord{Key: "events#1", Type: "telemetry_event", SourcePath: "runtime.jsonl",
		ContentSHA256: hex.EncodeToString(hash[:]), Scope: "local_pending_review", Payload: payload}
	if _, _, err := store.ApplyMigration(context.Background(), receipt, []ImportedRecord{record}, time.Now()); err == nil {
		t.Fatal("mismatched import count was accepted")
	}
	receipt.ExpectedCount = 1
	if _, _, err := store.ApplyMigration(context.Background(), receipt, []ImportedRecord{record}, time.Now()); err != nil {
		t.Fatal(err)
	}
	receipt.SourceFingerprint = strings.Repeat("c", 64)
	if _, _, err := store.ApplyMigration(context.Background(), receipt, []ImportedRecord{record}, time.Now()); !errors.Is(err, ErrMigrationConflict) {
		t.Fatalf("changed source fingerprint error = %v, want conflict", err)
	}
}
