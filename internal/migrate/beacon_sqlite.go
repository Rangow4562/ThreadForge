package migrate

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
	_ "modernc.org/sqlite"
)

const beaconLearningStoreSchemaVersion = 1

type beaconLearningTable struct {
	name       string
	jsonColumn string
	recordType string
}

var beaconLearningTables = [...]beaconLearningTable{
	{name: "evaluations", jsonColumn: "evaluation_json", recordType: "beacon_learning_evaluation"},
	{name: "candidates", jsonColumn: "candidate_json", recordType: "beacon_learning_candidate"},
	{name: "memories", jsonColumn: "memory_json", recordType: "beacon_learning_memory"},
}

func isBeaconLearningDBPath(relative string) bool {
	slash := "/" + strings.ToLower(filepath.ToSlash(strings.TrimPrefix(relative, "./")))
	if !strings.HasSuffix(slash, "/memory.db") {
		return false
	}
	return strings.HasSuffix(slash, "/.beacon/endpoint/memory.db") || slash == "/memory.db"
}

func ensureNoBeaconSQLiteSidecars(databasePath string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		_, err := os.Lstat(databasePath + suffix)
		if err == nil {
			return errors.New("Beacon memory.db has SQLite sidecars; stop Beacon and use a checkpointed offline copy before snapshotting")
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("could not safely check Beacon memory.db sidecars")
		}
	}
	return nil
}

func scanBeaconLearningDatabase(relative, databasePath string, sourceBytes []byte) (scannedFile, error) {
	file := scannedFile{entry: FileEntry{
		Path: relative, SourceSHA256: digest(sourceBytes), SourceBytes: int64(len(sourceBytes)), Status: "ready",
	}}
	if err := ensureNoBeaconSQLiteSidecars(databasePath); err != nil {
		return scannedFile{}, err
	}
	before, err := hashFile(databasePath)
	if err != nil || before != file.entry.SourceSHA256 {
		return scannedFile{}, errors.New("Beacon memory.db changed before it could be read")
	}
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		return scannedFile{}, errors.New("could not resolve Beacon memory.db path")
	}
	uriPath := filepath.ToSlash(absolute)
	if strings.HasPrefix(uriPath, "//") {
		return scannedFile{}, errors.New("network-path Beacon memory.db sources are not supported")
	}
	if filepath.VolumeName(absolute) != "" {
		uriPath = "/" + strings.TrimLeft(uriPath, "/")
	}
	databaseURL := url.URL{
		Scheme:   "file",
		Path:     uriPath,
		RawQuery: "immutable=1&mode=ro",
	}
	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		return scannedFile{}, errors.New("could not open Beacon memory.db read-only")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return scannedFile{}, fmt.Errorf("could not read Beacon memory.db schema version: %w", err)
	}
	if version != beaconLearningStoreSchemaVersion {
		return scannedFile{}, fmt.Errorf("unsupported Beacon memory.db schema version %d (expected %d)", version, beaconLearningStoreSchemaVersion)
	}
	var integrity string
	if err := db.QueryRow("PRAGMA quick_check(1)").Scan(&integrity); err != nil || integrity != "ok" {
		return scannedFile{}, errors.New("Beacon memory.db failed SQLite integrity check")
	}

	for _, table := range beaconLearningTables {
		query := fmt.Sprintf("SELECT id, %s FROM %s ORDER BY id", table.jsonColumn, table.name)
		rows, err := db.Query(query)
		if err != nil {
			return scannedFile{}, fmt.Errorf("Beacon memory.db is missing a supported %s table", table.name)
		}
		for rows.Next() {
			var id, raw string
			if err := rows.Scan(&id, &raw); err != nil {
				_ = rows.Close()
				return scannedFile{}, errors.New("could not read a Beacon memory.db record")
			}
			file.entry.RecordCount++
			if file.entry.RecordCount > maxRecords {
				_ = rows.Close()
				return scannedFile{}, errors.New("Beacon memory.db exceeds the 100000-record migration limit")
			}
			if !validBeaconLearningRecord(table.recordType, id, []byte(raw)) {
				file.entry.RejectedCount++
				continue
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, []byte(raw)); err != nil {
				file.entry.RejectedCount++
				continue
			}
			payload := json.RawMessage(compact.String())
			if privacy.ContainsCredential(payload) {
				file.entry.ExcludedCount++
				continue
			}
			file.records = append(file.records, Record{
				Key: fmt.Sprintf("%s#%06d", relative, file.entry.RecordCount), Type: table.recordType,
				SourcePath: relative, ContentSHA256: digest(payload), Scope: "local_pending_review", Payload: payload,
			})
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if rowsErr != nil || closeErr != nil {
			return scannedFile{}, errors.New("could not finish reading Beacon memory.db records")
		}
	}
	if err := ensureNoBeaconSQLiteSidecars(databasePath); err != nil {
		return scannedFile{}, err
	}
	after, err := hashFile(databasePath)
	if err != nil || after != before {
		return scannedFile{}, errors.New("Beacon memory.db changed while it was being read")
	}
	finalizeScannedFile(&file)
	return file, nil
}

func validBeaconLearningRecord(recordType, sqlID string, raw []byte) bool {
	if !json.Valid(raw) {
		return false
	}
	switch recordType {
	case "beacon_learning_evaluation":
		var value beacon.LearningEvaluationV1
		return json.Unmarshal(raw, &value) == nil && value.SchemaVersion == beacon.LearningSchemaVersion && value.ID == sqlID
	case "beacon_learning_candidate":
		var value beacon.LearningCandidateV1
		return json.Unmarshal(raw, &value) == nil && value.SchemaVersion == beacon.LearningSchemaVersion && value.ID == sqlID
	case "beacon_learning_memory":
		var value beacon.LearningMemoryV1
		return json.Unmarshal(raw, &value) == nil && value.SchemaVersion == beacon.LearningSchemaVersion && value.ID == sqlID
	default:
		return false
	}
}
