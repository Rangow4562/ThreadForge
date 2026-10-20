package teamtransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const syncStateSchema = "threadforge.team-sync-highwater.v1"

var syncStateFilename = regexp.MustCompile(`^rev-([0-9]{20})\.json$`)

type syncHighwater struct {
	SchemaVersion        string `json:"schema_version"`
	ProjectID            string `json:"project_id"`
	RemoteIdentitySHA256 string `json:"remote_identity_sha256"`
	Revision             int64  `json:"revision"`
	ManifestSHA256       string `json:"manifest_sha256"`
	Commit               string `json:"commit"`
}

func readHighwater(stateRoot, projectID, identity string) (*syncHighwater, error) {
	directory, err := syncStateDirectory(stateRoot, projectID, identity, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	if len(entries) > 100000 {
		return nil, errors.New("local team-sync high-water history exceeds its safety limit")
	}
	var latest *syncHighwater
	for _, entry := range entries {
		match := syncStateFilename.FindStringSubmatch(entry.Name())
		if match == nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("local team-sync state contains an unsupported entry")
		}
		filename := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(filename)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 {
			return nil, errors.New("local team-sync state is not a bounded regular file")
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		state, err := decodeHighwater(data)
		if err != nil {
			return nil, err
		}
		revision, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || state.Revision != revision || state.ProjectID != projectID || state.RemoteIdentitySHA256 != identity {
			return nil, errors.New("local team-sync state does not match its project, remote, or revision path")
		}
		if latest == nil || state.Revision > latest.Revision {
			copyState := state
			latest = &copyState
		}
	}
	return latest, nil
}

func saveHighwater(stateRoot string, state syncHighwater) error {
	if state.SchemaVersion != syncStateSchema || state.Revision < 1 || strings.TrimSpace(state.ProjectID) == "" ||
		!digestPattern.MatchString(state.RemoteIdentitySHA256) || !digestPattern.MatchString(state.ManifestSHA256) ||
		!objectIDPattern.MatchString(state.Commit) {
		return errors.New("refusing to persist an invalid team-sync high-water mark")
	}
	directory, err := syncStateDirectory(stateRoot, state.ProjectID, state.RemoteIdentitySHA256, true)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	filename := filepath.Join(directory, fmt.Sprintf("rev-%020d.json", state.Revision))
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := os.Lstat(filename)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 {
			return errors.New("existing local team-sync revision pin is not a safe regular file")
		}
		existing, readErr := os.ReadFile(filename)
		if readErr == nil && bytes.Equal(existing, data) {
			return nil
		}
		return errors.New("local team-sync revision is already pinned to different remote evidence")
	}
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(filename)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(filename)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(filename)
		return err
	}
	return nil
}

func decodeHighwater(data []byte) (syncHighwater, error) {
	var state syncHighwater
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return syncHighwater{}, fmt.Errorf("decode local team-sync state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return syncHighwater{}, errors.New("local team-sync state contains trailing data")
	}
	if state.SchemaVersion != syncStateSchema || strings.TrimSpace(state.ProjectID) == "" || state.Revision < 1 ||
		!digestPattern.MatchString(state.RemoteIdentitySHA256) || !digestPattern.MatchString(state.ManifestSHA256) ||
		!objectIDPattern.MatchString(state.Commit) {
		return syncHighwater{}, errors.New("local team-sync state has an invalid schema or identity")
	}
	return state, nil
}

func syncStateDirectory(root, projectID, identity string, create bool) (string, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(projectID) == "" || !digestPattern.MatchString(identity) {
		return "", errors.New("local team-sync state root or identity is invalid")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if create {
		if err := ensureDirectoryChain(absolute); err != nil {
			return "", err
		}
	} else {
		if err := verifyExistingDirectoryChain(absolute); err != nil {
			return "", err
		}
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	projectSum := sha256.Sum256([]byte("threadforge-team-project-ref-v1\x00" + projectID))
	projectKey := hex.EncodeToString(projectSum[:])
	directory := filepath.Join(resolved, "team-sync", identity, projectKey)
	if create {
		if err := ensureDirectoryChain(directory); err != nil {
			return "", err
		}
		return directory, nil
	}
	if err := verifyDirectoryChain(resolved, directory); err != nil {
		return "", err
	}
	return directory, nil
}

func ensureDirectoryChain(target string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	current := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(absolute, current)
	for _, part := range strings.Split(remainder, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("local team-sync state directory has an unsafe path component")
		}
	}
	return nil
}

func verifyExistingDirectoryChain(target string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	current := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(absolute, current)
	for _, part := range strings.Split(remainder, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("local team-sync state root is unavailable or unsafe")
		}
	}
	return nil
}

func verifyDirectoryChain(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("local team-sync state path escapes its configured root")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("local team-sync state directory is unavailable or unsafe")
		}
	}
	return nil
}

func checkHighwater(ctx context.Context, root string, high *syncHighwater, snapshot *remoteSnapshot) error {
	if high == nil {
		return nil
	}
	if snapshot == nil || snapshot.Manifest.Revision < high.Revision {
		return errors.New("remote team ref is behind the locally pinned revision; refusing rollback")
	}
	if snapshot.Manifest.Revision == high.Revision {
		if snapshot.ManifestSHA256 != high.ManifestSHA256 || snapshot.Commit != high.Commit {
			return errors.New("remote team ref changed at a pinned revision; refusing rewritten history")
		}
		return nil
	}
	if _, err := git(ctx, root, nil, 64, "merge-base", "--is-ancestor", high.Commit, snapshot.Commit); err != nil {
		return errors.New("remote team ref is not a descendant of the locally pinned commit; refusing rewritten history")
	}
	return nil
}
