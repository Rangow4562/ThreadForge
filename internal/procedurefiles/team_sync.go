package procedurefiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxTeamSyncRecords = 10000
	maxTeamSyncBytes   = 128 << 20
)

// SharedRecordFile is one immutable, signed project procedure revision. It is
// deliberately narrower than the local SQLite store: tasks, sessions, traces,
// decisions, preferences, and credentials are never part of this transport.
type SharedRecordFile struct {
	ID       string
	Revision int
	Content  []byte
}

// TeamSyncImportResult reports idempotent materialization of a remote snapshot.
type TeamSyncImportResult struct {
	Imported      int `json:"imported_records"`
	AlreadyExists int `json:"already_present_records"`
}

// WithActiveTeamProjectScope serializes an operation with scope decommission
// and refuses work if the project has no registered active team scope.
func (s *Store) WithActiveTeamProjectScope(ctx context.Context, projectID string, action func() error) error {
	if s.scopeWriteGate == nil || action == nil {
		return errors.New("registered active team project scope is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.scopeWriteGate.WithActiveTeamProjectScope(ctx, projectID, action)
}

// ExportTeamProcedures returns only the authenticated, privacy-reviewed
// published history for projectID, with deterministic ordering.
func (s *Store) ExportTeamProcedures(projectID string) ([]SharedRecordFile, error) {
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 {
		return nil, errors.New("project_id must contain 1 to 256 characters")
	}
	if _, err := s.ListProject(projectID); err != nil {
		return nil, err
	}
	directories, err := os.ReadDir(s.graphsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []SharedRecordFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records []SharedRecordFile
	totalBytes := 0
	for _, directory := range directories {
		if !directory.IsDir() || !validID(directory.Name()) {
			return nil, fmt.Errorf("invalid published procedure entry %q", directory.Name())
		}
		history, err := s.readHistory(directory.Name())
		if err != nil {
			return nil, err
		}
		if history[0].ProjectID != projectID {
			continue
		}
		for _, graph := range history {
			filename := filepath.Join(s.graphsRoot, graph.ID, fmt.Sprintf("rev-%06d.json", graph.Revision))
			info, err := os.Lstat(filename)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxGraphBytes {
				return nil, fmt.Errorf("published procedure revision %s/%d is not a bounded regular file", graph.ID, graph.Revision)
			}
			content, err := os.ReadFile(filename)
			if err != nil {
				return nil, err
			}
			totalBytes += len(content)
			if totalBytes > maxTeamSyncBytes || len(records) >= maxTeamSyncRecords {
				return nil, errors.New("published team procedure snapshot exceeds its count or size limit")
			}
			records = append(records, SharedRecordFile{ID: graph.ID, Revision: graph.Revision, Content: content})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].ID != records[j].ID {
			return records[i].ID < records[j].ID
		}
		return records[i].Revision < records[j].Revision
	})
	return records, nil
}

// ValidateTeamProcedures validates an untrusted transport snapshot in an
// isolated project tree. Every graph must pass the current pinned team trust
// policy and the normal published-history checks before it can be imported.
func (s *Store) ValidateTeamProcedures(projectID string, records []SharedRecordFile) error {
	if s.policy == nil {
		return errors.New("local team trust policy is not configured; remote procedures are withheld")
	}
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 || len(records) == 0 || len(records) > maxTeamSyncRecords {
		return errors.New("remote team procedure snapshot has an invalid project or record count")
	}
	stage, err := os.MkdirTemp("", ".threadforge-team-procedures-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		return err
	}
	stageStore, err := NewWithPolicy(stage, s.policy)
	if err != nil {
		return err
	}
	if err := stageStore.writeValidatedTeamProcedures(projectID, records); err != nil {
		return err
	}
	graphs, err := stageStore.ListProject(projectID)
	if err != nil {
		return fmt.Errorf("validate remote team procedures: %w", err)
	}
	if len(graphs) == 0 {
		return errors.New("remote snapshot has no procedures for the requested project")
	}
	return nil
}

// ImportTeamProcedures adds an authenticated remote snapshot without replacing
// local history. Identical records are idempotent; divergent same-revision
// records fail for review. The active team-scope gate serializes materialization
// against scope decommissioning.
func (s *Store) ImportTeamProcedures(projectID string, records []SharedRecordFile) (TeamSyncImportResult, error) {
	if s.scopeWriteGate == nil {
		return TeamSyncImportResult{}, errors.New("registered active team project scope is required for remote procedure materialization")
	}
	if err := s.ValidateTeamProcedures(projectID, records); err != nil {
		return TeamSyncImportResult{}, err
	}
	var result TeamSyncImportResult
	err := s.scopeWriteGate.WithActiveTeamProjectScope(context.Background(), projectID, func() error {
		// Preflight every destination before writing any file. Existing published
		// revisions are immutable and must match byte-for-byte.
		for _, record := range records {
			filename := s.teamProcedureFilename(record.ID, record.Revision)
			if err := s.checkNoSymlinkComponents(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			info, err := os.Lstat(filename)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxGraphBytes {
				return fmt.Errorf("existing published procedure revision %s/%d is unsafe", record.ID, record.Revision)
			}
			existing, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			if !bytes.Equal(existing, record.Content) {
				return fmt.Errorf("published procedure revision conflict for %s/%d; review before resolving", record.ID, record.Revision)
			}
		}
		for _, record := range records {
			filename := s.teamProcedureFilename(record.ID, record.Revision)
			if err := s.ensureDirectory(filepath.Dir(filename)); err != nil {
				return err
			}
			err := writeNoReplace(filename, record.Content)
			if errors.Is(err, os.ErrExist) {
				existing, readErr := os.ReadFile(filename)
				if readErr != nil || !bytes.Equal(existing, record.Content) {
					return fmt.Errorf("published procedure revision conflict for %s/%d; review before resolving", record.ID, record.Revision)
				}
				result.AlreadyExists++
				continue
			}
			if err != nil {
				return fmt.Errorf("materialize published procedure %s/%d: %w", record.ID, record.Revision, err)
			}
			result.Imported++
		}
		return nil
	})
	if err != nil {
		return TeamSyncImportResult{}, err
	}
	return result, nil
}

func (s *Store) writeValidatedTeamProcedures(projectID string, records []SharedRecordFile) error {
	seen := make(map[string]struct{}, len(records))
	totalBytes := 0
	for _, record := range records {
		if !validID(record.ID) || record.Revision < 1 || len(record.Content) == 0 || len(record.Content) > maxGraphBytes {
			return errors.New("remote team procedure record has an invalid identity or size")
		}
		key := fmt.Sprintf("%s/rev-%06d.json", record.ID, record.Revision)
		if _, duplicate := seen[key]; duplicate {
			return errors.New("remote team procedure snapshot contains a duplicate revision")
		}
		seen[key] = struct{}{}
		totalBytes += len(record.Content)
		if totalBytes > maxTeamSyncBytes {
			return errors.New("remote team procedure snapshot exceeds its byte limit")
		}
		var header struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal(record.Content, &header); err != nil || header.SchemaVersion != sharedRecordSchema {
			return fmt.Errorf("remote procedure %s/%d has no authenticated record envelope", record.ID, record.Revision)
		}
		var shared sharedRecord
		decoder := json.NewDecoder(bytes.NewReader(record.Content))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&shared); err != nil {
			return fmt.Errorf("decode remote procedure %s/%d: %w", record.ID, record.Revision, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return fmt.Errorf("remote procedure %s/%d contains trailing JSON", record.ID, record.Revision)
		}
		if shared.SchemaVersion != sharedRecordSchema || shared.Procedure.ID != record.ID ||
			shared.Procedure.Revision != record.Revision || shared.Procedure.ProjectID != projectID {
			return fmt.Errorf("remote procedure %s/%d does not match its project-scoped path", record.ID, record.Revision)
		}
		filename := s.teamProcedureFilename(record.ID, record.Revision)
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			return err
		}
		file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(record.Content)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (s *Store) teamProcedureFilename(id string, revision int) string {
	return filepath.Join(s.graphsRoot, id, fmt.Sprintf("rev-%06d.json", revision))
}
