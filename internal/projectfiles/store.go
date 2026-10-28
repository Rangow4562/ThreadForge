package projectfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/sourcefingerprint"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

var (
	ErrConflict = errors.New("project memory revision conflict")
	ErrNotFound = errors.New("project memory record not found")
)

const (
	maxRecordBytes    = 128 << 10
	maxHistoryEntries = 1000
	maxProjectRecords = 10000
)

type Store struct {
	projectRoot string
	recordsRoot string
}

// IndexEntry is the content-free projection used to browse one project's
// approved memory without returning record content or evidence metadata.
type IndexEntry struct {
	ID            string
	ProjectID     string
	Kind          projectmemory.Kind
	Title         string
	Revision      int
	Applicability projectmemory.Applicability
	ConflictKey   string
	SupersedesID  string
	ContentSHA256 string
	UpdatedAt     string
}

func New(projectRoot string) (*Store, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("project root must be an existing directory, not a symbolic link")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	recordsRoot := filepath.Join(root, ".threadforge", "memory", "records")
	return &Store{projectRoot: root, recordsRoot: recordsRoot}, nil
}

// Publish creates a new immutable, approved project-file revision. It requires
// an explicit privacy-reviewed human approval and compare-and-swap revision.
func (s *Store) Publish(candidate projectmemory.Candidate, expectedRevision int, reviewer, reason string, privacyReviewed bool, at time.Time) (projectmemory.Record, error) {
	if expectedRevision < 0 || strings.TrimSpace(reviewer) == "" || strings.TrimSpace(reason) == "" || !privacyReviewed {
		return projectmemory.Record{}, errors.New("publish requires a non-negative expected revision, reviewer, reason, and explicit privacy review")
	}
	if candidate.SchemaVersion != projectmemory.SchemaVersion && candidate.SchemaVersion != projectmemory.SchemaVersionV2 {
		return projectmemory.Record{}, errors.New("unsupported project memory schema")
	}
	if candidate.Kind != projectmemory.KindFact && candidate.Kind != projectmemory.KindDecision && candidate.Kind != projectmemory.KindPolicy {
		return projectmemory.Record{}, errors.New("project memory kind must be fact, decision, or policy")
	}
	if !json.Valid(candidate.Content) {
		return projectmemory.Record{}, errors.New("project memory content must be valid JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(candidate.Content, &object); err != nil || object == nil {
		return projectmemory.Record{}, errors.New("project memory content must be a JSON object")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, candidate.Content); err != nil {
		return projectmemory.Record{}, errors.New("project memory content must be valid JSON")
	}
	record := projectmemory.Record{
		SchemaVersion: candidate.SchemaVersion,
		ID:            candidate.ID,
		ProjectID:     candidate.ProjectID,
		Kind:          candidate.Kind,
		Title:         candidate.Title,
		Applicability: candidate.Applicability,
		ConflictKey:   candidate.ConflictKey,
		SupersedesID:  candidate.SupersedesID,
		Content:       compact.Bytes(),
		Evidence:      append([]projectmemory.Evidence(nil), candidate.Evidence...),
	}
	record.ContentSHA256 = digest(record.Content)
	if record.SupersedesID != "" {
		superseded, err := s.GetLatest(record.Kind, record.SupersedesID)
		if err != nil {
			return projectmemory.Record{}, fmt.Errorf("superseded project-memory record is unavailable: %w", err)
		}
		if superseded.ProjectID != record.ProjectID {
			return projectmemory.Record{}, errors.New("a record may supersede only another record in the same project")
		}
	}

	history, previousDigest, err := s.readHistory(record.Kind, record.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return projectmemory.Record{}, err
	}
	actualRevision := len(history)
	if expectedRevision != actualRevision {
		return projectmemory.Record{}, ErrConflict
	}
	if actualRevision >= maxHistoryEntries {
		return projectmemory.Record{}, errors.New("project memory revision history reached the 1000-revision limit")
	}

	stamp := at.UTC().Format(time.RFC3339Nano)
	record.Revision = actualRevision + 1
	record.Status = "approved"
	record.PreviousSHA256 = ""
	if actualRevision > 0 {
		record.PreviousSHA256 = previousDigest
		record.CreatedAt = history[0].CreatedAt
	}
	if record.CreatedAt == "" {
		record.CreatedAt = stamp
	}
	record.UpdatedAt = stamp
	record.Approval = &projectmemory.Approval{
		ApprovedBy: strings.TrimSpace(reviewer), ApprovedAt: stamp,
		Reason: strings.TrimSpace(reason), PrivacyReviewed: true,
	}
	if err := projectmemory.Validate(record); err != nil {
		return projectmemory.Record{}, err
	}
	if record.SchemaVersion == projectmemory.SchemaVersionV2 && s.CheckFreshness(record) != "verified" {
		return projectmemory.Record{}, errors.New("project-memory v2 evidence source files do not match their approved fingerprints")
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return projectmemory.Record{}, err
	}
	data = append(data, '\n')
	directory := filepath.Join(s.recordsRoot, string(record.Kind), record.ID)
	if err := s.ensureDirectory(directory); err != nil {
		return projectmemory.Record{}, err
	}
	filename := filepath.Join(directory, fmt.Sprintf("rev-%06d.json", record.Revision))
	if err := writeNoReplace(filename, data); err != nil {
		if errors.Is(err, os.ErrExist) {
			return projectmemory.Record{}, ErrConflict
		}
		return projectmemory.Record{}, fmt.Errorf("write approved project memory: %w", err)
	}
	return record, nil
}

// CheckFreshness recomputes locally bound evidence source hashes. Legacy v1
// records without source-path commitments remain explicitly unverified.
func (s *Store) CheckFreshness(record projectmemory.Record) string {
	if record.SchemaVersion != projectmemory.SchemaVersionV2 {
		return "unverified"
	}
	paths := make([]string, 0)
	expected := make(map[string]string)
	for _, evidence := range record.Evidence {
		for _, source := range evidence.SourceFiles {
			paths = append(paths, source.Path)
			expected[source.Path] = source.SHA256
		}
	}
	if len(paths) == 0 {
		return "unverified"
	}
	current, err := sourcefingerprint.FingerprintSourcesWithLimits(s.projectRoot, paths, 64, 4<<20, 8<<20)
	if err != nil {
		return "incomplete"
	}
	for _, source := range current {
		if expected[source.Path] != source.SHA256 {
			return "stale"
		}
	}
	return "verified"
}

func (s *Store) GetLatest(kind projectmemory.Kind, id string) (projectmemory.Record, error) {
	history, _, err := s.readHistory(kind, id)
	if err != nil {
		return projectmemory.Record{}, err
	}
	return history[len(history)-1], nil
}

func (s *Store) History(kind projectmemory.Kind, id string) ([]projectmemory.Record, error) {
	history, _, err := s.readHistory(kind, id)
	return history, err
}

func (s *Store) List(kind projectmemory.Kind, limit, offset int) ([]projectmemory.Record, int, error) {
	if kind != "" && kind != projectmemory.KindFact && kind != projectmemory.KindDecision && kind != projectmemory.KindPolicy {
		return nil, 0, errors.New("project memory kind must be fact, decision, policy, or empty")
	}
	if limit < 1 || limit > 200 || offset < 0 {
		return nil, 0, errors.New("project memory limit must be 1-200 and offset non-negative")
	}
	if err := s.checkNoSymlinkComponents(s.recordsRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []projectmemory.Record{}, 0, nil
		}
		return nil, 0, err
	}
	kinds := []projectmemory.Kind{projectmemory.KindFact, projectmemory.KindDecision, projectmemory.KindPolicy}
	if kind != "" {
		kinds = []projectmemory.Kind{kind}
	}
	var latest []projectmemory.Record
	for _, itemKind := range kinds {
		entries, err := os.ReadDir(filepath.Join(s.recordsRoot, string(itemKind)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !validID(entry.Name()) {
				return nil, 0, fmt.Errorf("invalid project-memory entry %q", entry.Name())
			}
			record, err := s.GetLatest(itemKind, entry.Name())
			if err != nil {
				return nil, 0, err
			}
			latest = append(latest, record)
			if len(latest) > maxProjectRecords {
				return nil, 0, errors.New("project memory exceeds 10000-record listing limit")
			}
		}
	}
	sort.Slice(latest, func(i, j int) bool {
		if latest[i].Kind != latest[j].Kind {
			return latest[i].Kind < latest[j].Kind
		}
		return latest[i].ID < latest[j].ID
	})
	total := len(latest)
	if offset >= total {
		return []projectmemory.Record{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return latest[offset:end], total, nil
}

// IndexProject returns content-free metadata for the latest records belonging
// to one project. The directory traversal and record histories are validated
// by the same strict readers used by List and GetLatest.
func (s *Store) IndexProject(projectID string, kind projectmemory.Kind) ([]IndexEntry, error) {
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 || privacy.ContainsCredentialText(projectID) {
		return nil, errors.New("project_id must be a non-empty, credential-free identifier of at most 256 characters")
	}
	if kind != "" && kind != projectmemory.KindFact && kind != projectmemory.KindDecision && kind != projectmemory.KindPolicy {
		return nil, errors.New("project memory kind must be fact, decision, policy, or empty")
	}
	if err := s.checkNoSymlinkComponents(s.recordsRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []IndexEntry{}, nil
		}
		return nil, err
	}
	kinds := []projectmemory.Kind{projectmemory.KindFact, projectmemory.KindDecision, projectmemory.KindPolicy}
	if kind != "" {
		kinds = []projectmemory.Kind{kind}
	}
	entries := make([]IndexEntry, 0)
	visited := 0
	for _, itemKind := range kinds {
		kindRoot := filepath.Join(s.recordsRoot, string(itemKind))
		info, err := os.Lstat(kindRoot)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("project-memory kind path is not a regular directory")
		}
		children, err := os.ReadDir(kindRoot)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if !child.IsDir() || child.Type()&os.ModeSymlink != 0 || !validID(child.Name()) {
				return nil, fmt.Errorf("invalid project-memory entry %q", child.Name())
			}
			visited++
			if visited > maxProjectRecords {
				return nil, errors.New("project memory exceeds 10000-record index limit")
			}
			record, err := s.GetLatest(itemKind, child.Name())
			if err != nil {
				return nil, err
			}
			if record.ProjectID != projectID {
				continue
			}
			entries = append(entries, IndexEntry{
				ID: record.ID, ProjectID: record.ProjectID, Kind: record.Kind, Title: record.Title,
				Revision: record.Revision, Applicability: record.Applicability, ConflictKey: record.ConflictKey,
				SupersedesID: record.SupersedesID, ContentSHA256: record.ContentSHA256, UpdatedAt: record.UpdatedAt,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}

func (s *Store) readHistory(kind projectmemory.Kind, id string) ([]projectmemory.Record, string, error) {
	if kind != projectmemory.KindFact && kind != projectmemory.KindDecision && kind != projectmemory.KindPolicy {
		return nil, "", errors.New("unknown project-memory kind")
	}
	if !validID(id) {
		return nil, "", errors.New("invalid project-memory identifier")
	}
	directory := filepath.Join(s.recordsRoot, string(kind), id)
	if err := s.checkNoSymlinkComponents(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("project-memory history path is not a regular directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, "", err
	}
	if len(entries) == 0 || len(entries) > maxHistoryEntries {
		return nil, "", errors.New("project-memory history is empty or exceeds the revision limit")
	}
	history := make([]projectmemory.Record, 0, len(entries))
	previousDigest := ""
	projectID := ""
	for index, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, "", fmt.Errorf("invalid project-memory revision entry %q", entry.Name())
		}
		revision, err := parseRevisionFilename(entry.Name())
		if err != nil || revision != index+1 {
			return nil, "", fmt.Errorf("project-memory revision sequence has a gap at %q", entry.Name())
		}
		filename := filepath.Join(directory, entry.Name())
		fileInfo, err := os.Lstat(filename)
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 || fileInfo.Size() > maxRecordBytes {
			return nil, "", fmt.Errorf("invalid project-memory revision file %q", entry.Name())
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, "", err
		}
		var record projectmemory.Record
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, "", fmt.Errorf("decode project-memory revision %s: %w", entry.Name(), err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, "", fmt.Errorf("project-memory revision %s contains trailing JSON", entry.Name())
		}
		if record.ID != id || record.Kind != kind || record.Revision != revision || record.PreviousSHA256 != previousDigest {
			return nil, "", fmt.Errorf("project-memory revision linkage mismatch at %s", entry.Name())
		}
		if projectID == "" {
			projectID = record.ProjectID
		} else if projectID != record.ProjectID {
			return nil, "", fmt.Errorf("project-memory project scope changed at %s", entry.Name())
		}
		if err := projectmemory.Validate(record); err != nil {
			return nil, "", fmt.Errorf("invalid project-memory revision %s: %w", entry.Name(), err)
		}
		history = append(history, record)
		previousDigest = digest(data)
	}
	return history, previousDigest, nil
}

func (s *Store) ensureDirectory(target string) error {
	relative, err := filepath.Rel(s.projectRoot, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("project-memory directory resolves outside the project")
	}
	current := s.projectRoot
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("project-memory directory component is unavailable or unsafe: %s", current)
		}
	}
	return nil
}

func (s *Store) checkNoSymlinkComponents(target string) error {
	relative, err := filepath.Rel(s.projectRoot, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("project-memory path resolves outside the project")
	}
	current := s.projectRoot
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("project-memory path contains a symbolic link: %s", current)
		}
	}
	return nil
}

func writeNoReplace(filename string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".pending-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryName, filename); err == nil {
		return nil
	}
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		_ = os.Remove(filename)
		return err
	}
	return file.Sync()
}

func parseRevisionFilename(name string) (int, error) {
	if !strings.HasPrefix(name, "rev-") || !strings.HasSuffix(name, ".json") {
		return 0, errors.New("invalid revision filename")
	}
	value := strings.TrimSuffix(strings.TrimPrefix(name, "rev-"), ".json")
	revision, err := strconv.Atoi(value)
	if err != nil || revision < 1 || fmt.Sprintf("%06d", revision) != value {
		return 0, errors.New("invalid revision number")
	}
	return revision, nil
}

func validID(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' || len(value) > 64 {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func digest(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}
