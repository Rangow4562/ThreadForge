package openclawsession

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/sessionlimits"
)

const (
	maxMetadataLines = 128
	maxLineBytes     = 4 << 20
)

type Store struct {
	OpenClawDir string
}

func NewStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		def, err := DefaultOpenClawDir()
		if err != nil {
			return nil, err
		}
		dir = def
	}
	return &Store{OpenClawDir: filepath.Clean(dir)}, nil
}

func DefaultOpenClawDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv("OPENCLAW_STATE_DIR")); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("home directory is empty")
	}
	return filepath.Join(home, ".openclaw"), nil
}

func (s *Store) Exists() bool {
	info, err := os.Stat(filepath.Join(s.OpenClawDir, "agents"))
	return err == nil && info.IsDir()
}

func (s *Store) List() ([]TraceRef, error) {
	budget := sessionlimits.NewBudget()
	roots, err := s.sessionRoots(budget)
	if err != nil {
		return nil, errors.Join(err, budget.Warning())
	}
	seen := map[string]bool{}
	var refs []TraceRef
	for _, root := range roots {
		indexed := s.readSessionsIndex(root, budget)
		for _, ref := range indexed {
			refs = append(refs, ref)
			seen[ref.SourcePath] = true
		}
		files, err := collectSessionFilesFlat(root, budget)
		if err != nil {
			return refs, errors.Join(err, budget.Warning())
		}
		for _, path := range files {
			if seen[path] {
				continue
			}
			if !budget.ChargeEntry() {
				break
			}
			if ref, ok := s.readTraceMetadata(path, 0, "", budget); ok {
				refs = append(refs, ref)
				seen[path] = true
			}
		}
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].UpdatedAtUnixMS != refs[j].UpdatedAtUnixMS {
			return refs[i].UpdatedAtUnixMS < refs[j].UpdatedAtUnixMS
		}
		if refs[i].Profile != refs[j].Profile {
			return refs[i].Profile < refs[j].Profile
		}
		return refs[i].ID < refs[j].ID
	})
	return refs, budget.Warning()
}

func (s *Store) Read(ref TraceRef) ([]Record, error) {
	entries, err := readEntries(ref.SourcePath)
	if err != nil {
		return nil, err
	}
	return recordsFromEntries(entries, ref.UpdatedAtUnixMS), nil
}

func (s *Store) sessionRoots(budget *sessionlimits.Budget) ([]string, error) {
	agentsDir := filepath.Join(s.OpenClawDir, "agents")
	entries, err := budget.ReadDir(agentsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read OpenClaw agents directory: %w", err)
	}
	var roots []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(agentsDir, entry.Name(), "sessions")
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	return roots, nil
}

func (s *Store) readSessionsIndex(root string, budget *sessionlimits.Budget) []TraceRef {
	data, err := budget.ReadFile(filepath.Join(root, "sessions.json"))
	if err != nil {
		return nil
	}
	var raw map[string]SessionIndexEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var refs []TraceRef
	for _, key := range keys {
		entry := raw[key]
		sourcePath := resolveIndexedSourcePath(entry.SessionFile, root)
		if entry.SessionID == "" || sourcePath == "" || !pathWithinRoot(sourcePath, root) {
			continue
		}
		if !budget.ChargeEntry() {
			break
		}
		if _, err := os.Lstat(sourcePath); err != nil {
			continue
		}
		updated := parseTimestamp(entry.UpdatedAt, 0)
		dir := firstNonEmpty(entry.WorkspaceDir, directoryFromAny(entry.SystemPromptReport))
		if ref, ok := s.readTraceMetadata(sourcePath, updated, dir, budget); ok {
			ref.Profile = profileFromSessionRoot(root)
			ref.SessionKey = key
			refs = append(refs, ref)
		}
	}
	return refs
}

func (s *Store) readTraceMetadata(path string, updatedHint int64, dirHint string, budget *sessionlimits.Budget) (TraceRef, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return TraceRef{}, false
	}
	updated := updatedHint
	if updated <= 0 {
		updated = info.ModTime().UnixMilli()
	}
	lines, err := budget.ReadLines(path, maxMetadataLines, sessionlimits.MaxMetadataFileBytes)
	if err != nil || len(lines) == 0 {
		return TraceRef{}, false
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	title := ""
	dir := dirHint
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry RawEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err == nil {
			entry.Raw = raw
		}
		ts := parseTimestamp(entry.Timestamp, updated)
		if ts > updated {
			updated = ts
		}
		switch strings.ToLower(entry.Type) {
		case "session", "session.started":
			if entry.ID != "" {
				id = entry.ID
			}
			dir = chooseDirectory(dir, entry.CWD)
		case "model_change":
			// Model switches are mapped from the full record later. Metadata only needs the
			// session's identity, title and workspace.
		case "message":
			if title == "" && entry.Message != nil && strings.EqualFold(entry.Message.Role, "user") {
				title = normalizeTitle(extractText(entry.Message.Content))
			}
		}
		dir = chooseDirectory(dir, directoryFromAny(entry.Raw))
	}
	if title == "" {
		title = "(No preview)"
	}
	return TraceRef{
		ID:              id,
		Profile:         profileFromSourcePath(path),
		SourcePath:      path,
		Title:           title,
		Preview:         title,
		Directory:       dir,
		UpdatedAtUnixMS: updated,
		SizeBytes:       info.Size(),
	}, true
}

func collectSessionFilesFlat(root string, budget *sessionlimits.Budget) ([]string, error) {
	entries, err := budget.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") || entry.Name() == "config-audit.jsonl" {
			continue
		}
		out = append(out, filepath.Join(root, entry.Name()))
	}
	return out, nil
}

func readEntries(path string) ([]RawEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	var out []RawEntry
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		var entry RawEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entry.Raw = raw
		out = append(out, entry)
	}
	return out, scanner.Err()
}

func resolveIndexedSourcePath(path, root string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func pathWithinRoot(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func profileFromSessionRoot(root string) string {
	if filepath.Base(root) != "sessions" {
		return "unknown"
	}
	return filepath.Base(filepath.Dir(root))
}

func profileFromSourcePath(path string) string {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "agents" && parts[i+2] == "sessions" {
			return parts[i+1]
		}
	}
	return "unknown"
}

func parseTimestamp(value any, fallback int64) int64 {
	switch v := value.(type) {
	case float64:
		if v > 0 && v < 1000000000000 {
			return int64(v * 1000)
		}
		return int64(v)
	case int64:
		if v > 0 && v < 1000000000000 {
			return v * 1000
		}
		return v
	case string:
		if v == "" {
			return fallback
		}
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t.UnixMilli()
		}
	}
	return fallback
}

func normalizeTitle(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "[[reply_to_current]]")
	text = strings.TrimSpace(text)
	if len(text) > 100 {
		return text[:100]
	}
	return text
}

func chooseDirectory(current, candidate string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return strings.TrimSpace(candidate)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
