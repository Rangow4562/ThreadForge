// Package handoff provides local, read-only views over sessions already
// captured in Threadforge's private event log.
package handoff

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/traces"
)

const (
	MaxTraceScanBytes   = 64 << 20
	MaxTraceSessions    = 10000
	MaxEventsPerSession = 10000
	MinSessionIDPrefix  = 6
)

var (
	ErrSessionNotFound  = errors.New("captured session not found")
	ErrSessionAmbiguous = errors.New("session id is ambiguous")
)

type Session struct {
	ID                string    `json:"id"`
	Harness           string    `json:"harness"`
	Directory         string    `json:"directory,omitempty"`
	DirectoryConflict bool      `json:"directory_conflict,omitempty"`
	Branch            string    `json:"branch,omitempty"`
	BranchConflict    bool      `json:"branch_conflict,omitempty"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
	EventCount        int       `json:"event_count"`
	PromptCount       int       `json:"prompt_count"`
	CommandCount      int       `json:"command_count"`
	FileChangeCount   int       `json:"file_change_count"`
	ToolEventCount    int       `json:"tool_event_count"`
}

type Filter struct {
	Harness   string
	Directory string
	Limit     int // zero means all, bounded by MaxTraceSessions
}

type ScanStatus struct {
	TraceMissing bool `json:"trace_missing,omitempty"`
	Truncated    bool `json:"older_events_omitted"`
	Malformed    int  `json:"malformed_lines,omitempty"`
	Changed      bool `json:"source_changed_during_read,omitempty"`
}

type Listing struct {
	Sessions []Session  `json:"sessions"`
	Scan     ScanStatus `json:"scan"`
}

type AmbiguousSessionError struct {
	ID         string
	Candidates []Session
}

func (e *AmbiguousSessionError) Error() string {
	items := make([]string, 0, len(e.Candidates))
	for _, candidate := range e.Candidates {
		items = append(items, candidate.Harness+":"+candidate.ID)
	}
	return fmt.Sprintf("%v: %q matches %d captured sessions (%s); pass --harness or the full id", ErrSessionAmbiguous, e.ID, len(e.Candidates), strings.Join(items, ", "))
}

func (e *AmbiguousSessionError) Is(target error) bool { return target == ErrSessionAmbiguous }

type aggregate struct {
	session           Session
	events            []beacon.Event
	directoryConflict bool
	branchConflict    bool
}

func List(tracePath string, filter Filter) (Listing, error) {
	if filter.Limit < 0 || filter.Limit > MaxTraceSessions {
		return Listing{}, fmt.Errorf("session limit must be 0..%d", MaxTraceSessions)
	}
	harness := normalizeHarnessFilter(filter.Harness)
	within := directoryMatcher(filter.Directory)
	groups := make(map[string]*aggregate)
	status, err := scanTrace(tracePath, func(event beacon.Event) error {
		session, ok := sessionFromEvent(event)
		if !ok || (harness != "" && session.Harness != harness) || !within(session.Directory) {
			return nil
		}
		key := sessionKey(session.Harness, session.ID)
		group := groups[key]
		if group == nil {
			if len(groups) >= MaxTraceSessions {
				return fmt.Errorf("trace contains more than %d distinct sessions in the scan window", MaxTraceSessions)
			}
			group = &aggregate{session: session}
			groups[key] = group
		}
		addEvent(group, event)
		return nil
	})
	if err != nil {
		return Listing{}, err
	}
	sessions := make([]Session, 0, len(groups))
	for _, group := range groups {
		sessions = append(sessions, group.session)
	}
	sortSessions(sessions)
	if filter.Limit > 0 && len(sessions) > filter.Limit {
		sessions = sessions[:filter.Limit]
	}
	return Listing{Sessions: sessions, Scan: status}, nil
}

func ReadSession(tracePath, id, harness string) (Session, []beacon.Event, ScanStatus, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n\t") {
		return Session{}, nil, ScanStatus{}, errors.New("session id must be nonempty, bounded, and contain no control separators")
	}
	harness = normalizeHarnessFilter(harness)
	groups := make(map[string]*aggregate)
	status, err := scanTrace(tracePath, func(event beacon.Event) error {
		session, ok := sessionFromEvent(event)
		if !ok || (harness != "" && session.Harness != harness) || (session.ID != id && (len(id) < MinSessionIDPrefix || !strings.HasPrefix(session.ID, id))) {
			return nil
		}
		key := sessionKey(session.Harness, session.ID)
		group := groups[key]
		if group == nil {
			if len(groups) >= MaxTraceSessions {
				return fmt.Errorf("session prefix matches more than %d captured sessions; pass a longer id or --harness", MaxTraceSessions)
			}
			group = &aggregate{session: session}
			groups[key] = group
		}
		addEvent(group, event)
		if len(group.events) >= MaxEventsPerSession {
			group.events = group.events[1:]
		}
		group.events = append(group.events, event)
		return nil
	})
	if err != nil {
		return Session{}, nil, status, err
	}
	if exact, ok := groups[sessionKey(harness, id)]; ok {
		return exact.session, exact.events, status, nil
	}
	matches := make([]*aggregate, 0, len(groups))
	for _, group := range groups {
		matches = append(matches, group)
	}
	switch len(matches) {
	case 0:
		return Session{}, nil, status, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	case 1:
		return matches[0].session, matches[0].events, status, nil
	default:
		candidates := make([]Session, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, match.session)
		}
		sortSessions(candidates)
		return Session{}, nil, status, &AmbiguousSessionError{ID: id, Candidates: candidates}
	}
}

func scanTrace(path string, visit func(beacon.Event) error) (ScanStatus, error) {
	status := ScanStatus{}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		status.TraceMissing = true
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("inspect local trace log: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return status, errors.New("local trace log must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return status, fmt.Errorf("open local trace log: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return status, errors.New("local trace log changed while opening")
	}
	start := opened.Size() - MaxTraceScanBytes
	if start < 0 {
		start = 0
	}
	status.Truncated = start > 0
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return status, fmt.Errorf("seek local trace log: %w", err)
	}
	reader := bufio.NewReader(io.LimitReader(file, opened.Size()-start))
	if start > 0 {
		if _, err := reader.ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
			return status, fmt.Errorf("skip partial trace line: %w", err)
		}
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), traces.MaxEventBytes+4096)
	for scanner.Scan() {
		var event beacon.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Validate() != nil {
			status.Malformed++
			continue
		}
		if err := visit(event); err != nil {
			return status, err
		}
	}
	if err := scanner.Err(); err != nil {
		return status, fmt.Errorf("scan local trace log: %w", err)
	}
	final, err := file.Stat()
	if err != nil || !os.SameFile(opened, final) {
		return status, errors.New("local trace log changed during read")
	}
	status.Changed = final.Size() != opened.Size() || !final.ModTime().Equal(opened.ModTime())
	return status, nil
}

func sessionFromEvent(event beacon.Event) (Session, bool) {
	if event.Session == nil || event.Session.ID == "" || event.Harness.Name == "" {
		return Session{}, false
	}
	id := strings.TrimSpace(event.Session.ID)
	if id == "" || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n\t") {
		return Session{}, false
	}
	timestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil {
		return Session{}, false
	}
	harness := beacon.NormalizeHarnessName(event.Harness.Name)
	if harness == "" || len(harness) > 64 || strings.ContainsAny(harness, "\x00\r\n\t") {
		return Session{}, false
	}
	return Session{ID: id, Harness: harness, Directory: event.Session.WorkingDirectory,
		Branch: event.Branch, StartedAt: timestamp.UTC(), UpdatedAt: timestamp.UTC()}, true
}

func addEvent(group *aggregate, event beacon.Event) {
	session := &group.session
	timestamp, _ := time.Parse(time.RFC3339Nano, event.Timestamp)
	timestamp = timestamp.UTC()
	if session.StartedAt.IsZero() || timestamp.Before(session.StartedAt) {
		session.StartedAt = timestamp
	}
	if timestamp.After(session.UpdatedAt) {
		session.UpdatedAt = timestamp
	}
	if !group.directoryConflict {
		if session.Directory == "" {
			session.Directory = event.Session.WorkingDirectory
		} else if event.Session.WorkingDirectory != "" && !samePath(session.Directory, event.Session.WorkingDirectory) {
			session.Directory = ""
			session.DirectoryConflict = true
			group.directoryConflict = true
		}
	}
	if !group.branchConflict {
		if session.Branch == "" {
			session.Branch = event.Branch
		} else if event.Branch != "" && event.Branch != session.Branch {
			session.Branch = ""
			session.BranchConflict = true
			group.branchConflict = true
		}
	}
	session.EventCount++
	switch event.Event.Action {
	case "prompt.submitted", "user_prompt_submit":
		session.PromptCount++
	case "command.executed", "command.completed":
		session.CommandCount++
	case "file.changed", "file.edited", "file.created", "file.deleted":
		session.FileChangeCount++
	case "tool.invoked", "tool.completed", "tool.failed":
		session.ToolEventCount++
	}
}

func normalizeHarnessFilter(value string) string {
	return beacon.NormalizeHarnessName(strings.TrimSpace(value))
}

func sessionKey(harness, id string) string { return harness + "\x00" + id }

func sortSessions(sessions []Session) {
	sort.Slice(sessions, func(i, j int) bool {
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		if sessions[i].Harness != sessions[j].Harness {
			return sessions[i].Harness < sessions[j].Harness
		}
		return sessions[i].ID < sessions[j].ID
	})
}

func samePath(a, b string) bool {
	left, right := filepath.Clean(a), filepath.Clean(b)
	if left == right {
		return true
	}
	resolvedLeft, leftErr := filepath.EvalSymlinks(left)
	resolvedRight, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && resolvedLeft == resolvedRight
}

func directoryMatcher(root string) func(string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return func(string) bool { return true }
	}
	return func(value string) bool {
		if value == "" {
			return false
		}
		path, err := filepath.Abs(value)
		if err != nil {
			return false
		}
		base, err := filepath.Abs(root)
		if err != nil {
			return false
		}
		return within(path, base) || samePath(path, base)
	}
}

func within(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}
