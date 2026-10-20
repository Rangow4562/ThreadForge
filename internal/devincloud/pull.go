package devincloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/traces"
)

type PullOptions struct {
	Print          bool
	IncludeContent bool
	ForceRefresh   bool
	MaxSessions    int
	MaxMessages    int
	StatePath      string
	TracePath      string
	Out            io.Writer
}

type Summary struct {
	Sessions        int  `json:"sessions"`
	SessionsChanged int  `json:"sessions_changed"`
	EventsEmitted   int  `json:"events_emitted"`
	Errors          int  `json:"errors"`
	Complete        bool `json:"complete"`
}

// PullOnce performs one bounded read-only API sweep. Normal runs persist only
// hashed cursors and append sanitized events to the private local trace store.
// Print mode is an explicitly non-mutating preview.
func PullOnce(ctx context.Context, client *Client, options PullOptions) (summary Summary, resultErr error) {
	if client == nil {
		return summary, errors.New("Devin API client is required")
	}
	if options.MaxSessions < 1 || options.MaxSessions > maxSessionLimit {
		return summary, fmt.Errorf("max sessions must be 1-%d", maxSessionLimit)
	}
	if options.MaxMessages < 1 || options.MaxMessages > maxMessageLimit {
		return summary, fmt.Errorf("max messages must be 1-%d", maxMessageLimit)
	}
	if options.Print {
		if options.Out == nil {
			return summary, errors.New("print mode requires an output writer")
		}
	} else {
		if !filepath.IsAbs(options.StatePath) || !filepath.IsAbs(options.TracePath) {
			return summary, errors.New("Devin state and trace paths must be absolute")
		}
	}

	var stateFile *StateFile
	var traceStore *traces.Store
	if !options.Print {
		var err error
		stateFile, err = OpenState(options.StatePath)
		if err != nil {
			return summary, err
		}
		defer func() {
			if closeErr := stateFile.Close(); closeErr != nil && resultErr == nil {
				resultErr = closeErr
			}
		}()
		if err := rejectSymlinkComponents(filepath.Clean(options.TracePath)); err != nil {
			return summary, err
		}
		if info, statErr := os.Lstat(options.TracePath); statErr == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return summary, errors.New("Devin trace path must be a regular file, not a symlink")
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return summary, errors.New("unable to inspect Devin trace path")
		}
		traceStore, err = traces.Open(options.TracePath)
		if err != nil {
			return summary, err
		}
		if err := rejectSymlinkComponents(filepath.Clean(options.TracePath)); err != nil {
			return summary, err
		}
	}

	sessions, err := client.ListSessions(ctx, options.MaxSessions)
	if err != nil {
		return summary, err
	}
	summary.Sessions = len(sessions)
	summary.Complete = true
	var sessionErrors []error
	for _, session := range sessions {
		if !sessionIDPattern.MatchString(session.SessionID) || session.SessionID == "." || session.SessionID == ".." ||
			session.CreatedAt <= 0 || session.UpdatedAt < session.CreatedAt {
			summary.Errors++
			summary.Complete = false
			sessionErrors = append(sessionErrors, errors.New("Devin API returned a session with an invalid ID or timestamp"))
			continue
		}
		var cursor *SessionState
		var cursorKey string
		if stateFile != nil {
			cursorKey = stateSessionKey(client.orgID, session.SessionID)
			cursor = stateFile.State().session(cursorKey)
			if !options.ForceRefresh && (cursor.Done || (cursor.SyncedAt != 0 && cursor.SyncedAt == session.UpdatedAt && cursor.Status == normalizedStatus(session.Status))) {
				continue
			}
		}
		if err := processSession(ctx, client, session, cursor, stateFile, traceStore, options, &summary); err != nil {
			summary.Errors++
			summary.Complete = false
			sessionErrors = append(sessionErrors, fmt.Errorf("Devin session %s: %w", hashedID(session.SessionID)[:12], err))
		}
	}
	if len(sessionErrors) > 0 {
		return summary, errors.Join(sessionErrors...)
	}
	return summary, nil
}

func processSession(
	ctx context.Context,
	client *Client,
	session Session,
	cursor *SessionState,
	stateFile *StateFile,
	traceStore *traces.Store,
	options PullOptions,
	summary *Summary,
) error {
	messages, err := client.SessionMessages(ctx, session.SessionID, options.MaxMessages)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if message.CreatedAt <= 0 || strings.TrimSpace(message.Source) == "" {
			return errors.New("Devin API returned a message with an invalid source or timestamp")
		}
	}
	mapped := MapSession(client.orgID, session, messages, options.IncludeContent)
	if cursor != nil && IsTerminal(session.Status) && !cursor.EndedEmitted {
		mapped = append(mapped, EndedEvent(client.orgID, session))
	}
	if cursor == nil && IsTerminal(session.Status) {
		mapped = append(mapped, EndedEvent(client.orgID, session))
	}

	var pending []mappedEvent
	for _, item := range mapped {
		if cursor == nil || !cursor.Emitted[item.DedupeID] {
			pending = append(pending, item)
		}
	}
	for start := 0; start < len(pending); start += 100 {
		end := min(start+100, len(pending))
		batch := pending[start:end]
		if options.Print {
			encoder := json.NewEncoder(options.Out)
			for _, item := range batch {
				if err := encoder.Encode(item.Event); err != nil {
					return errors.New("unable to write Devin event preview")
				}
			}
		} else {
			events := make([]beacon.Event, 0, len(batch))
			for _, item := range batch {
				events = append(events, item.Event)
			}
			if err := traceStore.Append(ctx, events); err != nil {
				return err
			}
			for _, item := range batch {
				cursor.Emitted[item.DedupeID] = true
			}
			if err := stateFile.Save(); err != nil {
				return err
			}
		}
		summary.EventsEmitted += len(batch)
	}

	if cursor != nil {
		cursor.UpdatedAt = session.UpdatedAt
		cursor.SyncedAt = session.UpdatedAt
		cursor.Status = normalizedStatus(session.Status)
		cursor.Done = IsFinal(session.Status)
		cursor.EndedEmitted = IsTerminal(session.Status)
		if !IsTerminal(session.Status) {
			cursor.EndedEmitted = false
		}
		if err := stateFile.Save(); err != nil {
			return err
		}
	}
	summary.SessionsChanged++
	return nil
}

// ResolvePaths returns a bounded local state file path and trace path under the
// Threadforge data root. Explicit overrides must be absolute paths.
func ResolvePaths(root, stateOverride, tracePath string) (string, string, error) {
	if strings.TrimSpace(root) == "" {
		return "", "", errors.New("Threadforge data root is required")
	}
	if !filepath.IsAbs(root) {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return "", "", errors.New("Threadforge data root must resolve to an absolute path")
		}
	}
	statePath := stateOverride
	if statePath == "" {
		statePath = filepath.Join(root, "state", "devin-cloud.v1.json")
	} else if !filepath.IsAbs(statePath) {
		return "", "", errors.New("--state-path must be absolute")
	}
	if !filepath.IsAbs(tracePath) {
		return "", "", errors.New("trace path must be absolute")
	}
	return filepath.Clean(statePath), filepath.Clean(tracePath), nil
}

// StateStatus reports only aggregate cursor metadata; raw organization, user,
// session, and event IDs are never returned.
func StateStatus(path string) (map[string]int, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("Devin cloud state path must be absolute")
	}
	path = filepath.Clean(path)
	if err := rejectSymlinkComponents(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]int{"sessions": 0, "pending": 0, "complete": 0}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Devin cloud state must be a regular file, not a symlink")
	}
	if info.Size() > maxStateBytes {
		return nil, errors.New("Devin cloud state exceeds the 32 MiB safety limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("unable to read Devin cloud state")
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, after) || int64(len(data)) != info.Size() {
		return nil, errors.New("Devin cloud state changed while it was being read")
	}
	state := emptyState()
	if err := decodeState(data, state); err != nil {
		return nil, err
	}
	if err := state.validate(); err != nil {
		return nil, err
	}
	complete, pending := 0, 0
	for _, session := range state.Sessions {
		if session.Done || (session.SyncedAt != 0 && session.SyncedAt == session.UpdatedAt) {
			complete++
		} else {
			pending++
		}
	}
	return map[string]int{"sessions": len(state.Sessions), "pending": pending, "complete": complete}, nil
}
