package teamtransport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/procedurefiles"
)

const (
	manifestSchema  = "threadforge.team-procedure-manifest.v1"
	maxManifestSize = 16 << 20
	maxRecordSize   = 2 << 20
	maxSnapshotSize = 128 << 20
	maxRecordCount  = 10000
	gitTimeout      = 3 * time.Minute
)

var (
	remoteNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	procedurePath     = regexp.MustCompile(`^procedures/([a-z][a-z0-9-]{0,63})/rev-([0-9]{6,})\.json$`)
	digestPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	objectIDPattern   = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)
	scpRemotePattern  = regexp.MustCompile(`^(?:[^/@:]+@)?([^/:]+):(.+)$`)
	windowsDrivePath  = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

type manifest struct {
	SchemaVersion        string         `json:"schema_version"`
	ProjectID            string         `json:"project_id"`
	RemoteIdentitySHA256 string         `json:"remote_identity_sha256"`
	Revision             int64          `json:"revision"`
	ParentManifestSHA256 string         `json:"parent_manifest_sha256,omitempty"`
	RecordCount          int            `json:"record_count"`
	TotalBytes           int64          `json:"total_bytes"`
	Records              []recordDigest `json:"records"`
}

type recordDigest struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type remoteSnapshot struct {
	Manifest       manifest
	ManifestSHA256 string
	Commit         string
	Records        []procedurefiles.SharedRecordFile
}

type Result struct {
	SchemaVersion   string `json:"schema_version"`
	Direction       string `json:"direction"`
	ProjectID       string `json:"project_id"`
	RemoteRevision  int64  `json:"remote_revision"`
	RecordCount     int    `json:"record_count"`
	ImportedRecords int    `json:"imported_records"`
	AlreadyPresent  int    `json:"already_present_records"`
	ManifestSHA256  string `json:"manifest_sha256,omitempty"`
	Status          string `json:"status"`
}

// Pull validates the complete remote snapshot under the local pinned team
// trust policy, then adds only immutable approved procedure records.
func Pull(ctx context.Context, projectRoot, remoteName, projectID string, files *procedurefiles.Store, stateRoot string) (Result, error) {
	if files == nil {
		return Result{}, errors.New("procedure store is required")
	}
	absoluteRoot, identity, ref, err := inspectRemote(ctx, projectRoot, remoteName, projectID)
	if err != nil {
		return Result{}, err
	}
	snapshot, err := readSnapshot(ctx, absoluteRoot, remoteName, ref, identity, projectID)
	if err != nil {
		return Result{}, err
	}
	highwater, err := readHighwater(stateRoot, projectID, identity)
	if err != nil {
		return Result{}, err
	}
	if err := checkHighwater(ctx, absoluteRoot, highwater, snapshot); err != nil {
		return Result{}, err
	}
	result := Result{SchemaVersion: "threadforge.team-sync-receipt.v1", Direction: "pull", ProjectID: projectID}
	if snapshot == nil {
		result.Status = "empty"
		return result, nil
	}
	if err := files.ValidateTeamProcedures(projectID, snapshot.Records); err != nil {
		return Result{}, fmt.Errorf("remote team procedures were withheld: %w", err)
	}
	imported, err := files.ImportTeamProcedures(projectID, snapshot.Records)
	if err != nil {
		return Result{}, fmt.Errorf("materialize remote team procedures: %w", err)
	}
	state := syncHighwater{SchemaVersion: syncStateSchema, ProjectID: projectID, RemoteIdentitySHA256: identity,
		Revision: snapshot.Manifest.Revision, ManifestSHA256: snapshot.ManifestSHA256, Commit: snapshot.Commit}
	if err := saveHighwater(stateRoot, state); err != nil {
		return Result{}, fmt.Errorf("procedures were imported but the local anti-rollback pin could not be saved: %w", err)
	}
	result.RemoteRevision = snapshot.Manifest.Revision
	result.RecordCount = len(snapshot.Records)
	result.ImportedRecords = imported.Imported
	result.AlreadyPresent = imported.AlreadyExists
	result.ManifestSHA256 = snapshot.ManifestSHA256
	result.Status = "pulled"
	if imported.Imported == 0 {
		result.Status = "already_current"
	}
	return result, nil
}

// Push publishes an append-only snapshot using a single Git-ref compare-and-
// swap. expectedRevision is mandatory: pass 0 for a new ref.
func Push(ctx context.Context, projectRoot, remoteName, projectID string, expectedRevision int64, files *procedurefiles.Store, stateRoot string) (Result, error) {
	if files == nil || expectedRevision < 0 {
		return Result{}, errors.New("procedure store and a non-negative expected remote revision are required")
	}
	localRecords, err := files.ExportTeamProcedures(projectID)
	if err != nil {
		return Result{}, fmt.Errorf("validate local published procedures: %w", err)
	}
	if len(localRecords) == 0 {
		return Result{}, errors.New("there are no published team procedures to synchronize")
	}
	if err := files.ValidateTeamProcedures(projectID, localRecords); err != nil {
		return Result{}, fmt.Errorf("local team procedures were withheld: %w", err)
	}
	absoluteRoot, identity, ref, err := inspectRemote(ctx, projectRoot, remoteName, projectID)
	if err != nil {
		return Result{}, err
	}
	remote, err := readSnapshot(ctx, absoluteRoot, remoteName, ref, identity, projectID)
	if err != nil {
		return Result{}, err
	}
	highwater, err := readHighwater(stateRoot, projectID, identity)
	if err != nil {
		return Result{}, err
	}
	if err := checkHighwater(ctx, absoluteRoot, highwater, remote); err != nil {
		return Result{}, err
	}
	remoteRevision := int64(0)
	remoteManifestDigest := ""
	remoteCommit := ""
	var remoteRecords []procedurefiles.SharedRecordFile
	if remote != nil {
		remoteRevision = remote.Manifest.Revision
		remoteManifestDigest = remote.ManifestSHA256
		remoteCommit = remote.Commit
		remoteRecords = remote.Records
		if err := files.ValidateTeamProcedures(projectID, remoteRecords); err != nil {
			return Result{}, fmt.Errorf("remote team procedures were withheld: %w", err)
		}
	}
	if remoteRevision != expectedRevision {
		return Result{}, fmt.Errorf("remote revision is %d, not expected revision %d; pull and review the current snapshot", remoteRevision, expectedRevision)
	}
	if err := requireRemoteRecordsIncluded(localRecords, remoteRecords); err != nil {
		return Result{}, err
	}
	if sameRecords(localRecords, remoteRecords) {
		if remote != nil {
			state := syncHighwater{SchemaVersion: syncStateSchema, ProjectID: projectID, RemoteIdentitySHA256: identity,
				Revision: remote.Manifest.Revision, ManifestSHA256: remote.ManifestSHA256, Commit: remote.Commit}
			if err := files.WithActiveTeamProjectScope(ctx, projectID, func() error { return saveHighwater(stateRoot, state) }); err != nil {
				return Result{}, err
			}
		}
		return Result{SchemaVersion: "threadforge.team-sync-receipt.v1", Direction: "push", ProjectID: projectID,
			RemoteRevision: remoteRevision, RecordCount: len(localRecords), ManifestSHA256: remoteManifestDigest,
			Status: "already_current"}, nil
	}
	if remoteRevision == int64(^uint64(0)>>1) {
		return Result{}, errors.New("remote team manifest revision is exhausted")
	}
	next := makeManifest(projectID, identity, remoteRevision+1, remoteManifestDigest, localRecords)
	manifestBytes, err := encodeManifest(next)
	if err != nil {
		return Result{}, err
	}
	manifestSum := sha256.Sum256(manifestBytes)
	manifestDigest := hex.EncodeToString(manifestSum[:])
	commit, err := createSnapshotCommit(ctx, absoluteRoot, remoteCommit, ref, localRecords, manifestBytes)
	if err != nil {
		return Result{}, err
	}
	err = files.WithActiveTeamProjectScope(ctx, projectID, func() error {
		latestLocal, err := files.ExportTeamProcedures(projectID)
		if err != nil {
			return fmt.Errorf("recheck local procedures before publication: %w", err)
		}
		if !sameRecords(localRecords, latestLocal) {
			return errors.New("local published procedures changed during sync; retry after reviewing the current records")
		}
		if err := pushWithLease(ctx, absoluteRoot, remoteName, ref, remoteCommit, commit); err != nil {
			return err
		}
		state := syncHighwater{SchemaVersion: syncStateSchema, ProjectID: projectID, RemoteIdentitySHA256: identity,
			Revision: next.Revision, ManifestSHA256: manifestDigest, Commit: commit}
		if err := saveHighwater(stateRoot, state); err != nil {
			return fmt.Errorf("remote push succeeded but the local anti-rollback pin could not be saved: %w", err)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{SchemaVersion: "threadforge.team-sync-receipt.v1", Direction: "push", ProjectID: projectID,
		RemoteRevision: next.Revision, RecordCount: len(localRecords), ManifestSHA256: manifestDigest, Status: "published"}, nil
}

func inspectRemote(ctx context.Context, root, remoteName, projectID string) (string, string, string, error) {
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 || !remoteNamePattern.MatchString(remoteName) || remoteName == "." || remoteName == ".." {
		return "", "", "", errors.New("a valid project ID and Git remote name are required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", "", "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", "", errors.New("project root must be an existing non-symlink directory")
	}
	if _, err := git(ctx, absolute, nil, 1<<20, "rev-parse", "--show-toplevel"); err != nil {
		return "", "", "", errors.New("project root is not an available Git working tree")
	}
	fetchURLs, err := git(ctx, absolute, nil, 64<<10, "remote", "get-url", "--all", remoteName)
	if err != nil {
		return "", "", "", fmt.Errorf("configured Git remote %q is unavailable", remoteName)
	}
	pushURLs, err := git(ctx, absolute, nil, 64<<10, "remote", "get-url", "--push", "--all", remoteName)
	if err != nil {
		return "", "", "", fmt.Errorf("configured Git push remote %q is unavailable", remoteName)
	}
	fetchIdentity, err := oneRemoteIdentity(absolute, fetchURLs)
	if err != nil {
		return "", "", "", err
	}
	pushIdentity, err := oneRemoteIdentity(absolute, pushURLs)
	if err != nil || fetchIdentity != pushIdentity {
		return "", "", "", errors.New("Git fetch and push remotes must identify the same repository")
	}
	identitySum := sha256.Sum256([]byte("threadforge-team-remote-v1\x00" + fetchIdentity))
	identityDigest := hex.EncodeToString(identitySum[:])
	projectSum := sha256.Sum256([]byte("threadforge-team-project-ref-v1\x00" + projectID))
	ref := "refs/threadforge/team/" + hex.EncodeToString(projectSum[:])
	return absolute, identityDigest, ref, nil
}

func oneRemoteIdentity(root string, output []byte) (string, error) {
	lines := strings.Fields(string(output))
	if len(lines) != 1 {
		return "", errors.New("Git remote must resolve to exactly one fetch and push URL")
	}
	return canonicalRemoteIdentity(root, lines[0])
}

func canonicalRemoteIdentity(root, raw string) (string, error) {
	if runtime.GOOS == "windows" && windowsDrivePath.MatchString(raw) {
		return canonicalLocalRemotePath(root, raw)
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		scheme := strings.ToLower(parsed.Scheme)
		if scheme != "https" && scheme != "http" && scheme != "ssh" && scheme != "git" && scheme != "file" {
			return "", errors.New("Git remote uses an unsupported transport scheme")
		}
		host := strings.ToLower(parsed.Hostname())
		port := parsed.Port()
		path := strings.TrimSuffix(strings.TrimSuffix(parsed.EscapedPath(), "/"), ".git")
		if scheme == "file" {
			local := filepath.FromSlash(parsed.Path)
			if runtime.GOOS == "windows" && len(local) >= 3 && local[0] == '\\' && local[2] == ':' {
				local = local[1:]
			}
			absolute, err := filepath.Abs(local)
			if err != nil {
				return "", errors.New("Git file remote identity cannot be resolved")
			}
			if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
				absolute = resolved
			}
			return canonicalLocalIdentity(absolute), nil
		}
		if host == "" || path == "" {
			return "", errors.New("Git remote URL has no stable host and repository path")
		}
		if (scheme == "https" && port == "443") || ((scheme == "ssh" || scheme == "git") && port == "22") {
			port = ""
		}
		if port != "" {
			host += ":" + port
		}
		return "network:" + host + path, nil
	}
	if match := scpRemotePattern.FindStringSubmatch(raw); match != nil && !strings.Contains(raw, "\\") {
		path := strings.TrimSuffix(strings.TrimSuffix(match[2], "/"), ".git")
		if path == "" {
			return "", errors.New("Git SSH remote has no repository path")
		}
		return "network:" + strings.ToLower(match[1]) + "/" + strings.TrimPrefix(path, "/"), nil
	}
	return canonicalLocalRemotePath(root, raw)
}

func canonicalLocalRemotePath(root, raw string) (string, error) {
	local := raw
	if !filepath.IsAbs(local) {
		local = filepath.Join(root, local)
	}
	absolute, err := filepath.Abs(local)
	if err != nil {
		return "", errors.New("local Git remote identity cannot be resolved")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return canonicalLocalIdentity(absolute), nil
}

func canonicalLocalIdentity(path string) string {
	clean := filepath.ToSlash(filepath.Clean(path))
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return "local:" + clean
}

func readSnapshot(ctx context.Context, root, remoteName, ref, identity, projectID string) (*remoteSnapshot, error) {
	listing, err := git(ctx, root, nil, 64<<10, "ls-remote", "--refs", remoteName, ref)
	if err != nil {
		return nil, fmt.Errorf("read remote team ref failed: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(listing)), "\n")
	if len(listing) == 0 || strings.TrimSpace(string(listing)) == "" {
		return nil, nil
	}
	if len(lines) != 1 {
		return nil, errors.New("remote team ref returned an ambiguous response")
	}
	fields := strings.Split(strings.TrimSpace(lines[0]), "\t")
	if len(fields) != 2 || fields[1] != ref || !objectIDPattern.MatchString(fields[0]) {
		return nil, errors.New("remote team ref returned an invalid object identity")
	}
	tempRef, err := temporaryRef()
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = git(cleanupCtx, root, nil, 64<<10, "update-ref", "-d", tempRef)
	}()
	if _, err := git(ctx, root, nil, 64<<10, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", remoteName, "+"+ref+":"+tempRef); err != nil {
		return nil, fmt.Errorf("fetch remote team snapshot failed: %w", err)
	}
	commitBytes, err := git(ctx, root, nil, 1024, "rev-parse", "--verify", tempRef+"^{commit}")
	if err != nil {
		return nil, errors.New("remote team ref does not point to a commit")
	}
	commit := strings.TrimSpace(string(commitBytes))
	if !objectIDPattern.MatchString(commit) {
		return nil, errors.New("remote team ref commit ID is invalid")
	}
	if commit != fields[0] {
		return nil, errors.New("remote team ref changed while the snapshot was being fetched; retry the read")
	}
	return readCommitSnapshot(ctx, root, commit, identity, projectID)
}

func readCommitSnapshot(ctx context.Context, root, commit, identity, projectID string) (*remoteSnapshot, error) {
	listing, err := git(ctx, root, nil, 4<<20, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return nil, fmt.Errorf("inspect remote team snapshot failed: %w", err)
	}
	entries := make(map[string]string)
	for _, raw := range bytes.Split(listing, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		parts := bytes.SplitN(raw, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return nil, errors.New("remote team snapshot contains an invalid tree entry")
		}
		metadata := strings.Fields(string(parts[0]))
		path := string(parts[1])
		if len(metadata) != 3 || metadata[0] != "100644" || metadata[1] != "blob" || !objectIDPattern.MatchString(metadata[2]) {
			return nil, errors.New("remote team snapshot contains a non-regular or non-blob entry")
		}
		if path != "manifest.json" && procedurePath.FindStringSubmatch(path) == nil {
			return nil, errors.New("remote team snapshot contains an unsupported path")
		}
		if _, exists := entries[path]; exists {
			return nil, errors.New("remote team snapshot contains duplicate paths")
		}
		entries[path] = metadata[2]
		if len(entries) > maxRecordCount+1 {
			return nil, errors.New("remote team snapshot exceeds its file count limit")
		}
	}
	manifestOID, ok := entries["manifest.json"]
	if !ok {
		return nil, errors.New("remote team snapshot has no manifest")
	}
	manifestBytes, err := readBlob(ctx, root, manifestOID, maxManifestSize)
	if err != nil {
		return nil, fmt.Errorf("read remote team manifest: %w", err)
	}
	current, err := decodeManifest(manifestBytes)
	if err != nil {
		return nil, err
	}
	if current.ProjectID != projectID || current.RemoteIdentitySHA256 != identity {
		return nil, errors.New("remote team manifest belongs to a different project or Git remote")
	}
	if current.RecordCount != len(entries)-1 || current.RecordCount != len(current.Records) || current.RecordCount < 1 || current.RecordCount > maxRecordCount {
		return nil, errors.New("remote team manifest record count does not match its Git tree")
	}
	result := &remoteSnapshot{Manifest: current, Commit: commit}
	manifestSum := sha256.Sum256(manifestBytes)
	result.ManifestSHA256 = hex.EncodeToString(manifestSum[:])
	result.Records = make([]procedurefiles.SharedRecordFile, 0, current.RecordCount)
	totalBytes := int64(0)
	lastPath := ""
	seen := make(map[string]bool, current.RecordCount)
	for _, item := range current.Records {
		match := procedurePath.FindStringSubmatch(item.Path)
		if match == nil || (lastPath != "" && item.Path <= lastPath) || seen[item.Path] ||
			!digestPattern.MatchString(item.SHA256) || item.SizeBytes < 1 || item.SizeBytes > maxRecordSize {
			return nil, errors.New("remote team manifest has an invalid or unordered procedure record")
		}
		lastPath = item.Path
		seen[item.Path] = true
		oid, exists := entries[item.Path]
		if !exists {
			return nil, errors.New("remote team manifest references a missing procedure blob")
		}
		data, err := readBlob(ctx, root, oid, maxRecordSize)
		if err != nil {
			return nil, fmt.Errorf("read remote procedure record: %w", err)
		}
		if int64(len(data)) != item.SizeBytes || digest(data) != item.SHA256 {
			return nil, errors.New("remote procedure record size or digest does not match its manifest")
		}
		revision, parseErr := strconv.Atoi(match[2])
		if parseErr != nil || revision < 1 {
			return nil, errors.New("remote team manifest contains an invalid procedure revision")
		}
		result.Records = append(result.Records, procedurefiles.SharedRecordFile{ID: match[1], Revision: revision, Content: data})
		totalBytes += int64(len(data))
		if totalBytes > maxSnapshotSize {
			return nil, errors.New("remote team procedure snapshot exceeds its byte limit")
		}
	}
	if int64(current.TotalBytes) != totalBytes || len(seen) != len(entries)-1 {
		return nil, errors.New("remote team manifest byte count or file set does not match its Git tree")
	}
	if err := verifyCommitParent(ctx, root, commit, current, identity, projectID); err != nil {
		return nil, err
	}
	return result, nil
}

func verifyCommitParent(ctx context.Context, root, commit string, current manifest, identity, projectID string) error {
	line, err := git(ctx, root, nil, 256, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return errors.New("verify remote team commit ancestry failed")
	}
	fields := strings.Fields(string(line))
	if len(fields) == 0 || fields[0] != commit || len(fields) > 2 {
		return errors.New("remote team history must be a single-parent linear history")
	}
	if current.Revision == 1 {
		if len(fields) != 1 || current.ParentManifestSHA256 != "" {
			return errors.New("first remote team revision has an unexpected parent")
		}
		return nil
	}
	if len(fields) != 2 || current.ParentManifestSHA256 == "" || !digestPattern.MatchString(current.ParentManifestSHA256) {
		return errors.New("remote team revision has no valid parent manifest binding")
	}
	parentOID, err := git(ctx, root, nil, 1024, "rev-parse", "--verify", fields[1]+":manifest.json")
	if err != nil {
		return errors.New("remote team parent manifest is unavailable")
	}
	parentBytes, err := readBlob(ctx, root, strings.TrimSpace(string(parentOID)), maxManifestSize)
	if err != nil {
		return errors.New("remote team parent manifest cannot be read")
	}
	parent, err := decodeManifest(parentBytes)
	if err != nil || parent.ProjectID != projectID || parent.RemoteIdentitySHA256 != identity || parent.Revision+1 != current.Revision {
		return errors.New("remote team revision does not extend the expected project history")
	}
	if digest(parentBytes) != current.ParentManifestSHA256 {
		return errors.New("remote team revision does not bind its parent manifest")
	}
	return nil
}

func decodeManifest(data []byte) (manifest, error) {
	if len(data) == 0 || len(data) > maxManifestSize {
		return manifest{}, errors.New("remote team manifest is empty or exceeds its size limit")
	}
	var value manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return manifest{}, fmt.Errorf("decode remote team manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return manifest{}, errors.New("remote team manifest has trailing data")
	}
	if value.SchemaVersion != manifestSchema || strings.TrimSpace(value.ProjectID) == "" || len(value.ProjectID) > 256 ||
		!digestPattern.MatchString(value.RemoteIdentitySHA256) || value.Revision < 1 || value.RecordCount < 1 ||
		value.RecordCount > maxRecordCount || value.TotalBytes < 1 || value.TotalBytes > maxSnapshotSize ||
		len(value.Records) != value.RecordCount {
		return manifest{}, errors.New("remote team manifest has an unsupported schema or invalid bounds")
	}
	if value.Revision == 1 && value.ParentManifestSHA256 != "" ||
		value.Revision > 1 && !digestPattern.MatchString(value.ParentManifestSHA256) {
		return manifest{}, errors.New("remote team manifest has an invalid parent digest")
	}
	return value, nil
}

func makeManifest(projectID, identity string, revision int64, parentDigest string, records []procedurefiles.SharedRecordFile) manifest {
	ordered := append([]procedurefiles.SharedRecordFile(nil), records...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].ID != ordered[j].ID {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Revision < ordered[j].Revision
	})
	value := manifest{SchemaVersion: manifestSchema, ProjectID: projectID, RemoteIdentitySHA256: identity,
		Revision: revision, ParentManifestSHA256: parentDigest, RecordCount: len(ordered), Records: make([]recordDigest, 0, len(ordered))}
	for _, record := range ordered {
		path := recordPath(record.ID, record.Revision)
		value.Records = append(value.Records, recordDigest{Path: path, SHA256: digest(record.Content), SizeBytes: int64(len(record.Content))})
		value.TotalBytes += int64(len(record.Content))
	}
	return value
}

func encodeManifest(value manifest) ([]byte, error) {
	if _, err := decodeManifestUnchecked(value); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func decodeManifestUnchecked(value manifest) (manifest, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return manifest{}, err
	}
	return decodeManifest(data)
}

func createSnapshotCommit(ctx context.Context, root, parentCommit, ref string, records []procedurefiles.SharedRecordFile, manifestBytes []byte) (string, error) {
	indexName, err := randomHex(16)
	if err != nil {
		return "", err
	}
	indexPath := filepath.Join(os.TempDir(), "threadforge-team-index-"+indexName)
	defer os.Remove(indexPath)
	env := map[string]string{"GIT_INDEX_FILE": indexPath}
	readTreeArgs := []string{"read-tree", "--empty"}
	if parentCommit != "" {
		readTreeArgs = []string{"read-tree", parentCommit}
	}
	if _, err := gitWithEnv(ctx, root, nil, 1024, env, readTreeArgs...); err != nil {
		return "", fmt.Errorf("prepare isolated team snapshot index: %w", err)
	}
	for _, record := range records {
		if len(record.Content) == 0 || len(record.Content) > maxRecordSize {
			return "", errors.New("local published procedure has an invalid size")
		}
		if err := addBlobToIndex(ctx, root, env, recordPath(record.ID, record.Revision), record.Content); err != nil {
			return "", err
		}
	}
	if err := addBlobToIndex(ctx, root, env, "manifest.json", manifestBytes); err != nil {
		return "", err
	}
	tree, err := gitWithEnv(ctx, root, nil, 1024, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write team snapshot tree: %w", err)
	}
	identityEnv := map[string]string{
		"GIT_AUTHOR_NAME":     "Threadforge Team Transport",
		"GIT_AUTHOR_EMAIL":    "noreply@threadforge.invalid",
		"GIT_COMMITTER_NAME":  "Threadforge Team Transport",
		"GIT_COMMITTER_EMAIL": "noreply@threadforge.invalid",
		"GIT_AUTHOR_DATE":     "2000-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE":  "2000-01-01T00:00:00Z",
	}
	commitArgs := []string{"commit-tree", strings.TrimSpace(string(tree)), "-m", "Threadforge approved procedure snapshot v1"}
	if parentCommit != "" {
		commitArgs = append(commitArgs[:2], append([]string{"-p", parentCommit}, commitArgs[2:]...)...)
	}
	commit, err := gitWithEnv(ctx, root, nil, 1024, identityEnv, commitArgs...)
	if err != nil {
		return "", fmt.Errorf("create team snapshot commit: %w", err)
	}
	commitID := strings.TrimSpace(string(commit))
	if !objectIDPattern.MatchString(commitID) {
		return "", errors.New("Git returned an invalid team snapshot commit ID")
	}
	return commitID, nil
}

func addBlobToIndex(ctx context.Context, root string, env map[string]string, path string, content []byte) error {
	if len(content) > maxRecordSize && path != "manifest.json" {
		return errors.New("team snapshot file exceeds its size limit")
	}
	if path == "manifest.json" && len(content) > maxManifestSize {
		return errors.New("team manifest exceeds its size limit")
	}
	oid, err := gitWithEnv(ctx, root, content, 1024, nil, "hash-object", "-w", "--stdin")
	if err != nil {
		return fmt.Errorf("store team snapshot object: %w", err)
	}
	objectID := strings.TrimSpace(string(oid))
	if !objectIDPattern.MatchString(objectID) {
		return errors.New("Git returned an invalid team snapshot blob ID")
	}
	indexEntry := "100644," + objectID + "," + path
	if _, err := gitWithEnv(ctx, root, nil, 1024, env, "update-index", "--add", "--cacheinfo", indexEntry); err != nil {
		return fmt.Errorf("add team snapshot file to isolated index: %w", err)
	}
	return nil
}

func pushWithLease(ctx context.Context, root, remote, ref, previousCommit, nextCommit string) error {
	lease := "--force-with-lease=" + ref + ":" + previousCommit
	if _, err := git(ctx, root, nil, 1<<20, "push", "--porcelain", lease, remote, nextCommit+":"+ref); err != nil {
		return fmt.Errorf("remote compare-and-swap push failed; pull and review before retrying: %w", err)
	}
	return nil
}

func requireRemoteRecordsIncluded(local, remote []procedurefiles.SharedRecordFile) error {
	localByPath := make(map[string][]byte, len(local))
	for _, record := range local {
		localByPath[recordPath(record.ID, record.Revision)] = record.Content
	}
	for _, record := range remote {
		path := recordPath(record.ID, record.Revision)
		content, ok := localByPath[path]
		if !ok {
			return errors.New("remote contains published procedures missing locally; pull and review before pushing")
		}
		if !bytes.Equal(content, record.Content) {
			return fmt.Errorf("local and remote procedure revision conflict at %s; review before resolving", path)
		}
	}
	return nil
}

func sameRecords(left, right []procedurefiles.SharedRecordFile) bool {
	if len(left) != len(right) {
		return false
	}
	leftByPath := make(map[string][]byte, len(left))
	for _, record := range left {
		leftByPath[recordPath(record.ID, record.Revision)] = record.Content
	}
	for _, record := range right {
		if !bytes.Equal(leftByPath[recordPath(record.ID, record.Revision)], record.Content) {
			return false
		}
	}
	return true
}

func recordPath(id string, revision int) string {
	return fmt.Sprintf("procedures/%s/rev-%06d.json", id, revision)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readBlob(ctx context.Context, root, objectID string, maxBytes int) ([]byte, error) {
	if !objectIDPattern.MatchString(objectID) {
		return nil, errors.New("Git blob object ID is invalid")
	}
	sizeText, err := git(ctx, root, nil, 64, "cat-file", "-s", objectID)
	if err != nil {
		return nil, errors.New("Git blob size cannot be read")
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(sizeText)))
	if err != nil || size < 0 || size > maxBytes {
		return nil, errors.New("Git blob exceeds its size limit")
	}
	return git(ctx, root, nil, maxBytes, "cat-file", "blob", objectID)
}

func temporaryRef() (string, error) {
	value, err := randomHex(16)
	if err != nil {
		return "", err
	}
	return "refs/threadforge/tmp-sync-" + value, nil
}

func randomHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > b.limit {
		return 0, errors.New("Git output exceeds its limit")
	}
	return b.Buffer.Write(data)
}

func git(ctx context.Context, root string, stdin []byte, maxOutput int, args ...string) ([]byte, error) {
	return gitWithEnv(ctx, root, stdin, maxOutput, nil, args...)
}

func gitWithEnv(ctx context.Context, root string, stdin []byte, maxOutput int, extraEnv map[string]string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	bounded, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	command := exec.CommandContext(bounded, "git", args...)
	command.Dir = root
	command.Env = sanitizedGitEnvironment(extraEnv)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	stdout := &cappedBuffer{limit: maxOutput}
	command.Stdout = stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git operation failed with exit code %d", exitErr.ExitCode())
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(bounded.Err(), context.DeadlineExceeded) {
			return nil, errors.New("git operation timed out")
		}
		return nil, errors.New("git operation could not be started")
	}
	return stdout.Bytes(), nil
}

func sanitizedGitEnvironment(extra map[string]string) []string {
	blocked := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true, "GIT_COMMON_DIR": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_CEILING_DIRECTORIES": true,
	}
	values := make(map[string]string, len(extra)+2)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		upper := strings.ToUpper(key)
		if ok && !blocked[upper] {
			values[upper] = key + "=" + value
		}
	}
	values["GIT_TERMINAL_PROMPT"] = "GIT_TERMINAL_PROMPT=0"
	values["GCM_INTERACTIVE"] = "GCM_INTERACTIVE=never"
	for key, value := range extra {
		values[strings.ToUpper(key)] = key + "=" + value
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
