package claudesession

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/sessionlimits"
)

const (
	MaxLineBytes = 8 * 1024 * 1024
)

type SessionRef struct {
	ID              string
	Path            string
	ProjectDir      string
	ProjectPath     string
	ModTimeUnixMS   int64
	SizeBytes       int64
	IsSidechain     bool
	ParentSessionID string
	Meta            *SubagentMeta
	Index           *IndexEntry
}

type Store struct {
	ProjectsDir string
}

type Stats struct {
	Lines       int
	Decoded     int
	Malformed   int
	PartialTail bool
	FirstError  error
}

func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("home directory is empty")
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

func NewStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) != "" {
		return &Store{ProjectsDir: dir}, nil
	}
	def, err := DefaultProjectsDir()
	if err != nil {
		return nil, err
	}
	return &Store{ProjectsDir: def}, nil
}

func (s *Store) Exists() bool {
	info, err := os.Stat(s.ProjectsDir)
	return err == nil && info.IsDir()
}

func (s *Store) List() ([]SessionRef, error) {
	budget := sessionlimits.NewBudget()
	entries, err := budget.ReadDir(s.ProjectsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Claude projects directory: %w", err)
	}

	var refs []SessionRef
	var scanErrs []error
	for _, entry := range entries {
		if budget.Truncated() {
			break
		}
		if !entry.IsDir() {
			continue
		}
		projectDir := filepath.Join(s.ProjectsDir, entry.Name())
		index, indexErr := readIndex(projectDir, budget)
		if indexErr != nil && !errors.Is(indexErr, fs.ErrNotExist) {
			scanErrs = append(scanErrs, indexErr)
		}
		if budget.Truncated() {
			break
		}
		walkErr := budget.WalkDir(projectDir, sessionlimits.MaxDirectoryDepth, func(path string, d fs.DirEntry) error {
			if d.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".jsonl" {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.IsDir() {
				return nil
			}
			ref := SessionRef{
				ID:            strings.TrimSuffix(filepath.Base(path), ".jsonl"),
				Path:          path,
				ProjectDir:    projectDir,
				ProjectPath:   projectPathFromDir(projectDir),
				ModTimeUnixMS: info.ModTime().UnixMilli(),
				SizeBytes:     info.Size(),
			}
			if idx := index[path]; idx != nil {
				ref.Index = idx
				if idx.ProjectPath != "" {
					ref.ProjectPath = idx.ProjectPath
				}
				if idx.SessionID != "" {
					ref.ID = idx.SessionID
				}
				ref.IsSidechain = idx.IsSidechain
			}
			if filepath.Base(filepath.Dir(path)) == "subagents" {
				ref.IsSidechain = true
				ref.ParentSessionID = filepath.Base(filepath.Dir(filepath.Dir(path)))
				ref.Meta, err = readSubagentMeta(strings.TrimSuffix(path, ".jsonl")+".meta.json", budget)
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					scanErrs = append(scanErrs, err)
				}
			}
			refs = append(refs, ref)
			return nil
		})
		if walkErr != nil {
			scanErrs = append(scanErrs, walkErr)
		}
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].ModTimeUnixMS != refs[j].ModTimeUnixMS {
			return refs[i].ModTimeUnixMS < refs[j].ModTimeUnixMS
		}
		return refs[i].Path < refs[j].Path
	})
	if warning := budget.Warning(); warning != nil {
		scanErrs = append(scanErrs, warning)
	}
	return refs, errors.Join(scanErrs...)
}

func (s *Store) Read(ref SessionRef) ([]Record, Stats, error) {
	f, err := os.Open(ref.Path)
	if err != nil {
		return nil, Stats{}, err
	}
	defer f.Close()
	return decodeRecords(f)
}

func decodeRecords(r io.Reader) ([]Record, Stats, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var records []Record
	var stats Stats
	lineNo := 0
	for {
		line, status, readErr := readLine(br)
		switch status {
		case lineEOF:
			return records, stats, nil
		case linePartial:
			stats.PartialTail = true
			return records, stats, nil
		case lineError:
			return records, stats, readErr
		case lineOversized:
			lineNo++
			stats.Lines = lineNo
			stats.Malformed++
			if stats.FirstError == nil {
				stats.FirstError = fmt.Errorf("claude session line exceeds %d bytes", MaxLineBytes)
			}
			continue
		}
		lineNo++
		stats.Lines = lineNo
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		entry, err := decodeEntry(line)
		if err != nil {
			stats.Malformed++
			if stats.FirstError == nil {
				stats.FirstError = err
			}
			continue
		}
		stats.Decoded++
		records = append(records, Record{Line: lineNo, Entry: *entry})
	}
}

type lineStatus int

const (
	lineOK        lineStatus = iota
	lineOversized            // consumed and discarded; too large
	linePartial              // unterminated trailing fragment
	lineEOF                  // clean end of stream
	lineError                // I/O error
)

// readLine reads one JSONL line from br, handling oversized lines (consumed and
// discarded so later lines are still reachable) and partial tails (unterminated
// trailing fragments from a file still being written).
func readLine(br *bufio.Reader) ([]byte, lineStatus, error) {
	var buf []byte
	oversized := false
	for {
		chunk, err := br.ReadSlice('\n')
		if oversized || len(buf)+len(chunk) > MaxLineBytes {
			oversized = true
			buf = nil
		} else {
			buf = append(buf, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if oversized {
				return nil, lineOversized, nil
			}
			if len(buf) > 0 {
				return nil, linePartial, nil
			}
			return nil, lineEOF, nil
		}
		if err != nil {
			return nil, lineError, err
		}
		break
	}
	if oversized {
		return nil, lineOversized, nil
	}
	return bytes.TrimSuffix(bytes.TrimSuffix(buf, []byte("\n")), []byte("\r")), lineOK, nil
}

func readIndex(projectDir string, budget *sessionlimits.Budget) (map[string]*IndexEntry, error) {
	data, err := budget.ReadFile(filepath.Join(projectDir, "sessions-index.json"))
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var index SessionIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, nil
	}
	out := make(map[string]*IndexEntry, len(index.Entries))
	for i := range index.Entries {
		entry := index.Entries[i]
		if entry.FullPath == "" {
			continue
		}
		out[entry.FullPath] = &entry
	}
	return out, nil
}

func readSubagentMeta(path string, budget *sessionlimits.Budget) (*SubagentMeta, error) {
	data, err := budget.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var meta SubagentMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, nil
	}
	return &meta, nil
}

func projectPathFromDir(projectDir string) string {
	base := filepath.Base(projectDir)
	if !strings.HasPrefix(base, "-") {
		return ""
	}
	return string(filepath.Separator) + strings.ReplaceAll(strings.TrimPrefix(base, "-"), "-", string(filepath.Separator))
}
