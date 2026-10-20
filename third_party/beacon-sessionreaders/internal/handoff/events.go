package handoff

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/claudesession"
	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/clinesession"
	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/codexsession"
	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/endpoint/schema"
	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/opencodesession"
	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/sessionlimits"
)

const MaxNativeSessionEvents = 10_000

var ErrNativeSessionEventLimit = errors.New("native session event export was truncated at its configured limit")

// EventSource is a Source that can also read one session back as endpoint events, through the
// same mapper its `beacon endpoint <runtime> sync` backfill uses.
type EventSource interface {
	Source
	Events(Session) ([]schema.Event, error)
}

// Events reads a session's events from the source for its runtime.
func Events(sources []Source, session Session) ([]schema.Event, error) {
	session.readBudget = sessionlimits.NewBudget()
	for _, source := range sources {
		if source.Harness() != session.Harness {
			continue
		}
		reader, ok := source.(EventSource)
		if !ok {
			return nil, fmt.Errorf("%s sessions cannot be read back", session.Harness)
		}
		events, err := reader.Events(session)
		if len(events) > MaxNativeSessionEvents {
			return events[:MaxNativeSessionEvents], errors.Join(err, ErrNativeSessionEventLimit)
		}
		return events, err
	}
	return nil, fmt.Errorf("no session store for %s", session.Harness)
}

// checkReadPath charges the actual source files used for one native export against a shared
// per-session budget. It rejects symlinks and special files so a path swap cannot redirect the
// read into an unrelated tree. The source-specific reader still parses its own format afterward.
func (s Session) checkReadPath(path string) error {
	if s.readBudget == nil {
		return errors.New("native session export budget is missing")
	}
	if !filepath.IsAbs(path) || len(path) > 4096 {
		return errors.New("native session source path is not a bounded absolute path")
	}
	if err := rejectSymlinkComponents(path); err != nil {
		return err
	}
	rootInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("native session source contains a symlink")
	}
	if rootInfo.Mode().IsRegular() {
		if rootInfo.Size() > sessionlimits.MaxMetadataTotalBytes || !s.readBudget.ChargeBytes(rootInfo.Size()) {
			return sessionlimits.ErrScanLimit
		}
		return nil
	}
	if !rootInfo.IsDir() {
		return errors.New("native session source is not a regular file or directory")
	}
	if err := s.readBudget.WalkDir(path, sessionlimits.MaxDirectoryDepth, func(entryPath string, entry fs.DirEntry) error {
		info, err := os.Lstat(entryPath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("native session source contains a symlink")
		}
		if entry.IsDir() {
			if !info.IsDir() {
				return errors.New("native session source directory changed during scan")
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("native session source contains a non-regular file")
		}
		if !s.readBudget.ChargeBytes(info.Size()) {
			return sessionlimits.ErrScanLimit
		}
		return nil
	}); err != nil {
		return err
	}
	return s.readBudget.Warning()
}

func (s Session) readDir(path string) ([]fs.DirEntry, error) {
	if s.readBudget == nil {
		return nil, errors.New("native session export budget is missing")
	}
	if err := rejectSymlinkComponents(path); err != nil {
		return nil, err
	}
	entries, err := s.readBudget.ReadDir(path)
	if err != nil {
		return nil, err
	}
	if err := s.readBudget.Warning(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s Session) readFile(path string) ([]byte, error) {
	if s.readBudget == nil {
		return nil, errors.New("native session export budget is missing")
	}
	if err := rejectSymlinkComponents(path); err != nil {
		return nil, err
	}
	data, err := s.readBudget.ReadFileLimit(path, sessionlimits.MaxNativeSessionReadBytes)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, sessionlimits.ErrScanLimit
	}
	return data, nil
}

func rejectSymlinkComponents(path string) error {
	if runtime.GOOS == "windows" && strings.HasPrefix(path, `\\`) {
		return errors.New("network paths are not accepted for native session reads")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	root := volume + string(filepath.Separator)
	rest := strings.TrimPrefix(filepath.Clean(abs), root)
	current := filepath.Clean(root)
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("native session source path contains a symlink")
		}
	}
	return nil
}

func checkOpenCodeLegacyInputs(session Session, store *opencodesession.Store, ref opencodesession.TraceRef) error {
	messageDir := filepath.Join(store.LegacyStorageDir, "message", ref.ID)
	messages, err := session.readDir(messageDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range messages {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		messagePath := filepath.Join(messageDir, entry.Name())
		data, err := session.readFile(messagePath)
		if err != nil {
			return err
		}
		var message struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(data, &message)
		messageID := strings.TrimSuffix(entry.Name(), ".json")
		if strings.TrimSpace(message.ID) != "" {
			messageID = message.ID
		}
		partDir := filepath.Join(store.LegacyStorageDir, "part", messageID)
		parts, err := session.readDir(partDir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, part := range parts {
			if part.IsDir() || !strings.HasSuffix(part.Name(), ".json") {
				continue
			}
			if _, err := session.readFile(filepath.Join(partDir, part.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func errSessionGone(session Session) error {
	return fmt.Errorf("%w: %s session %s is no longer in its store", ErrNotFound, session.Harness, session.ID)
}

func (s *claudeSource) Events(session Session) ([]schema.Event, error) {
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	refs, err := store.List()
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if ref.ID != session.ID || ref.Path != session.SourcePath {
			continue
		}
		if err := session.checkReadPath(ref.Path); err != nil {
			return nil, err
		}
		records, _, err := store.Read(ref)
		if err != nil {
			return nil, err
		}
		return claudeEvents(claudesession.MapSession(ref, records, claudesession.MapOptions{})), nil
	}
	return nil, errSessionGone(session)
}

func claudeEvents(mapped []claudesession.MappedEvent) []schema.Event {
	events := make([]schema.Event, 0, len(mapped))
	for _, m := range mapped {
		events = append(events, m.Event)
	}
	return events
}

// Events reads every rollout file of the thread, oldest first, so a thread resumed into a new file
// keeps its earlier turns.
func (s *codexSource) Events(session Session) ([]schema.Event, error) {
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	refs, err := store.List()
	if err != nil {
		return nil, err
	}
	var thread []codexsession.SessionRef
	for _, ref := range refs {
		if ref.ID == session.ID {
			thread = append(thread, ref)
		}
	}
	if len(thread) == 0 {
		return nil, errSessionGone(session)
	}
	sort.SliceStable(thread, func(i, j int) bool { return thread[i].ModTimeUnixMS < thread[j].ModTimeUnixMS })
	var events []schema.Event
	for i, ref := range thread {
		if err := session.checkReadPath(ref.Path); err != nil {
			return nil, err
		}
		records, _, err := store.Read(ref)
		if err != nil {
			return nil, err
		}
		// A later rollout file repeats the thread's session_meta; only the first starts the session.
		for _, m := range codexsession.MapSession(ref, records, codexsession.MapOptions{SkipSessionStarted: i > 0}) {
			events = append(events, m.Event)
		}
	}
	return events, nil
}

func (s *openCodeSource) Events(session Session) ([]schema.Event, error) {
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	refs, listErr := store.List()
	for _, ref := range refs {
		if ref.ID != session.ID {
			continue
		}
		if err := session.checkReadPath(ref.SourcePath); err != nil {
			return nil, err
		}
		if ref.Kind == opencodesession.SourceLegacy {
			if err := checkOpenCodeLegacyInputs(session, store, ref); err != nil {
				return nil, err
			}
		}
		records, err := store.Read(ref)
		if err != nil {
			return nil, err
		}
		mapped := opencodesession.MapTrace(ref, records, opencodesession.MapOptions{})
		events := make([]schema.Event, 0, len(mapped))
		for _, m := range mapped {
			events = append(events, m.Event)
		}
		return events, nil
	}
	if listErr != nil {
		return nil, listErr
	}
	return nil, errSessionGone(session)
}

func (s *clineSource) Events(session Session) ([]schema.Event, error) {
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	refs, err := store.List()
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if ref.ID != session.ID || ref.Kind != session.Store {
			continue
		}
		if err := session.checkReadPath(ref.SourcePath); err != nil {
			return nil, err
		}
		records, err := store.Read(ref)
		if err != nil {
			return nil, err
		}
		mapped := clinesession.MapTrace(ref, records, clinesession.MapOptions{})
		events := make([]schema.Event, 0, len(mapped))
		for _, m := range mapped {
			events = append(events, m.Event)
		}
		return events, nil
	}
	return nil, errSessionGone(session)
}
