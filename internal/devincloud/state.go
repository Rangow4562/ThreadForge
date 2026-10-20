package devincloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	stateSchemaVersion = "threadforge.devin-cloud-state.v1"
	maxStateBytes      = 32 << 20
	maxStateSessions   = 5000
	maxEmittedPerRun   = 100000
)

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type State struct {
	SchemaVersion string                   `json:"schema_version"`
	Sessions      map[string]*SessionState `json:"sessions"`
}

type SessionState struct {
	UpdatedAt    int64           `json:"updated_at"`
	SyncedAt     int64           `json:"synced_at"`
	Status       string          `json:"status"`
	Done         bool            `json:"done"`
	EndedEmitted bool            `json:"ended_emitted"`
	Emitted      map[string]bool `json:"emitted"`
}

type StateFile struct {
	path     string
	lockPath string
	lock     *os.File
	state    *State
}

func emptyState() *State {
	return &State{SchemaVersion: stateSchemaVersion, Sessions: map[string]*SessionState{}}
}

func (s *State) session(sessionKey string) *SessionState {
	entry := s.Sessions[sessionKey]
	if entry == nil {
		entry = &SessionState{Emitted: map[string]bool{}}
		s.Sessions[sessionKey] = entry
	}
	if entry.Emitted == nil {
		entry.Emitted = map[string]bool{}
	}
	return entry
}

func (s *State) validate() error {
	if s.SchemaVersion != stateSchemaVersion || s.Sessions == nil || len(s.Sessions) > maxStateSessions {
		return errors.New("Devin cloud state schema or session count is invalid")
	}
	for sessionKey, entry := range s.Sessions {
		if !sha256Pattern.MatchString(sessionKey) || entry == nil || len(entry.Emitted) > maxEmittedPerRun || len(entry.Status) > 64 ||
			(entry.Status != "" && normalizedStatus(entry.Status) != entry.Status) {
			return errors.New("Devin cloud state contains an invalid session cursor")
		}
		for eventKey, emitted := range entry.Emitted {
			if !emitted || !sha256Pattern.MatchString(eventKey) {
				return errors.New("Devin cloud state contains an invalid event cursor")
			}
		}
	}
	return nil
}

// OpenState takes an exclusive process lock, validates the private cursor file,
// and refuses symlinks, non-regular files, stale locks, and oversized state.
func OpenState(path string) (*StateFile, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("Devin cloud state path must be absolute")
	}
	path = filepath.Clean(path)
	if err := rejectSymlinkComponents(path); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.New("unable to create private Devin cloud state directory")
	}
	if err := rejectSymlinkComponents(path); err != nil {
		return nil, err
	}
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, errors.New("Devin cloud state is locked or has a stale lock; inspect it before retrying")
		}
		return nil, errors.New("unable to lock Devin cloud state")
	}
	if _, err := lock.WriteString("threadforge\n"); err != nil {
		_ = lock.Close()
		_ = os.Remove(lockPath)
		return nil, errors.New("unable to initialize Devin cloud state lock")
	}
	state := emptyState()
	file, err := os.Lstat(path)
	if err == nil {
		if file.Mode()&os.ModeSymlink != 0 || !file.Mode().IsRegular() {
			_ = lock.Close()
			_ = os.Remove(lockPath)
			return nil, errors.New("Devin cloud state must be a regular file, not a symlink")
		}
		if file.Size() > maxStateBytes {
			_ = lock.Close()
			_ = os.Remove(lockPath)
			return nil, errors.New("Devin cloud state exceeds the 32 MiB safety limit")
		}
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrPermission) {
			_ = lock.Close()
			_ = os.Remove(lockPath)
			return nil, errors.New("unable to restrict Devin cloud state permissions")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			_ = lock.Close()
			_ = os.Remove(lockPath)
			return nil, errors.New("unable to read Devin cloud state")
		}
		if err := decodeState(data, state); err != nil {
			_ = lock.Close()
			_ = os.Remove(lockPath)
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = lock.Close()
		_ = os.Remove(lockPath)
		return nil, errors.New("unable to inspect Devin cloud state")
	}
	if err := state.validate(); err != nil {
		_ = lock.Close()
		_ = os.Remove(lockPath)
		return nil, err
	}
	return &StateFile{path: path, lockPath: lockPath, lock: lock, state: state}, nil
}

func decodeState(data []byte, target *State) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("Devin cloud state is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("Devin cloud state has trailing data")
	}
	return nil
}

func (f *StateFile) State() *State { return f.state }

func (f *StateFile) Save() error {
	if f == nil || f.state == nil || f.lock == nil {
		return errors.New("Devin cloud state is not locked")
	}
	if err := f.state.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f.state, "", "  ")
	if err != nil {
		return errors.New("unable to encode Devin cloud state")
	}
	if len(data) > maxStateBytes {
		return errors.New("Devin cloud state would exceed the 32 MiB safety limit")
	}
	temporary, err := os.CreateTemp(filepath.Dir(f.path), ".devin-cloud-state-*.tmp")
	if err != nil {
		return errors.New("unable to stage Devin cloud state")
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return errors.New("unable to restrict Devin cloud state permissions")
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return errors.New("unable to write Devin cloud state")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errors.New("unable to sync Devin cloud state")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("unable to close Devin cloud state")
	}
	if err := os.Rename(name, f.path); err != nil {
		return fmt.Errorf("unable to replace Devin cloud state: %w", err)
	}
	return nil
}

func (f *StateFile) Close() error {
	if f == nil || f.lock == nil {
		return nil
	}
	err := f.lock.Close()
	f.lock = nil
	removeErr := os.Remove(f.lockPath)
	if err != nil {
		return errors.New("unable to close Devin cloud state lock")
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.New("unable to remove Devin cloud state lock")
	}
	return nil
}

func rejectSymlinkComponents(path string) error {
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(path, current)
	if volume == "" {
		current = string(filepath.Separator)
		remainder = strings.TrimPrefix(path, current)
	}
	for _, component := range strings.Split(remainder, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errors.New("unable to inspect Devin cloud state path")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Devin cloud state path contains a symlink")
		}
	}
	return nil
}

func stateSessionKey(orgID, sessionID string) string {
	return hashedID(orgID + "\x00" + sessionID)
}
