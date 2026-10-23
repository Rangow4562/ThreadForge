package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	mancodeTeamGitRef            = "refs/mancode/team"
	mancodeGitRefVirtualPath     = "__threadforge_sources__/mancode/git-ref-team/manifest.json"
	mancodeGitRefSchema          = "threadforge.mancode-git-ref-authority.v1"
	maxMancodeGitRefManifest     = 1_000_000
	maxMancodeGitMetadataEntries = 100_000
	maxMancodeGitStderrBytes     = 4096
)

var (
	mancodeGitRefObjectIDPattern       = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	mancodeGitRefDigestPattern         = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	mancodeGitRefVersionPattern        = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)
	mancodeGitRefRemoteRevisionPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// InspectOptions opts into reading only the already-local Mancode team ref.
// It never fetches, updates refs, or contacts a remote.
type InspectOptions struct {
	IncludeMancodeGitRef bool
}

// MancodeGitRefSource binds the private migration snapshot to the exact local
// Git commit and manifest blob read from refs/mancode/team.
type MancodeGitRefSource struct {
	Ref            string `json:"ref"`
	Commit         string `json:"commit"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ManifestBytes  int64  `json:"manifest_bytes"`
}

type mancodeGitRefAuthorityImport struct {
	SchemaVersion             string `json:"schema_version"`
	Authority                 string `json:"authority"`
	HumanReviewRequired       bool   `json:"human_review_required"`
	ExecutionAuthorized       bool   `json:"execution_authorized"`
	SourceRef                 string `json:"source_ref"`
	SourceCommit              string `json:"source_commit"`
	SourceManifestSHA256      string `json:"source_manifest_sha256"`
	ManifestSchemaVersion     int64  `json:"manifest_schema_version"`
	WorkspaceIDSHA256         string `json:"workspace_id_sha256"`
	SchemaEpochPresent        bool   `json:"schema_epoch_present"`
	MinimumReaderVersion      string `json:"minimum_reader_version"`
	MinimumWriterVersion      string `json:"minimum_writer_version"`
	TransportEpoch            int64  `json:"transport_epoch"`
	ConfigRevision            int64  `json:"config_revision"`
	AuthorityState            string `json:"authority_state"`
	AuthorityFreezePresent    bool   `json:"authority_freeze_present"`
	AuthorityTombstonePresent bool   `json:"authority_tombstone_present"`
	Revision                  int64  `json:"revision"`
	ActorProfileCount         int    `json:"actor_profile_count"`
	OwnershipFenceCount       int    `json:"ownership_fence_count"`
	ClaimCount                int    `json:"claim_count"`
	HandoffCount              int    `json:"handoff_count"`
	TaskBundleCount           int    `json:"task_bundle_count"`
	ReceiptCount              int    `json:"receipt_count"`
	LastMutationPresent       bool   `json:"last_mutation_present"`
	PrivacyPolicyPresent      bool   `json:"privacy_policy_present"`
	UpdatedAt                 string `json:"updated_at"`
	SourceIdentityOmitted     bool   `json:"source_identity_omitted"`
	ExecutionAuthorityOmitted bool   `json:"execution_authority_omitted"`
}

type mancodeGitRefReceiptImport struct {
	SchemaVersion               string `json:"schema_version"`
	Authority                   string `json:"authority"`
	HumanReviewRequired         bool   `json:"human_review_required"`
	ExecutionAuthorized         bool   `json:"execution_authorized"`
	Kind                        string `json:"kind"`
	TaskReferencePresent        bool   `json:"task_reference_present"`
	RemoteRevision              int64  `json:"remote_revision"`
	OwnershipEpochPresent       bool   `json:"ownership_epoch_present"`
	ActorProfileDigestPresent   bool   `json:"actor_profile_digest_present"`
	OwnershipFenceDigestPresent bool   `json:"ownership_fence_digest_present"`
	ClaimDigestPresent          bool   `json:"claim_digest_present"`
	HandoffDigestPresent        bool   `json:"handoff_digest_present"`
	TaskBundleDigestPresent     bool   `json:"task_bundle_digest_present"`
	CommittedAt                 string `json:"committed_at"`
	SourceIdentityOmitted       bool   `json:"source_identity_omitted"`
	MutationDigestValuesOmitted bool   `json:"mutation_digest_values_omitted"`
}

func readMancodeGitRef(root string) (MancodeGitRefSource, []byte, string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return MancodeGitRefSource{}, nil, "", err
	}
	canonicalRoot, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode git-ref source root is unavailable or unsafe")
	}
	gitDirectory := filepath.Join(canonicalRoot, ".git")
	gitInfo, err := os.Lstat(gitDirectory)
	if err != nil || !gitInfo.IsDir() || gitInfo.Mode()&os.ModeSymlink != 0 {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode git-ref import requires a self-contained repository root")
	}
	if _, err := os.Lstat(filepath.Join(gitDirectory, "commondir")); err == nil || !errors.Is(err, os.ErrNotExist) {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode git-ref import does not support linked worktrees")
	}
	if err := rejectUnsafeGitMetadata(gitDirectory); err != nil {
		return MancodeGitRefSource{}, nil, "", err
	}
	if err := rejectGitAlternates(gitDirectory); err != nil {
		return MancodeGitRefSource{}, nil, "", err
	}
	showRoot, err := runGitRead(canonicalRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode git-ref root is not a Git worktree")
	}
	resolvedRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(showRoot)))
	if err != nil || !sameFilesystemPath(canonicalRoot, resolvedRoot) {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode git-ref source root must be the repository worktree root")
	}
	commitBytes, err := runGitRead(canonicalRoot, "rev-parse", "--verify", "--end-of-options", mancodeTeamGitRef+"^{commit}")
	if err != nil {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode team ref is not available locally; fetch it explicitly before migration")
	}
	commit := strings.TrimSpace(string(commitBytes))
	if !mancodeGitRefObjectIDPattern.MatchString(commit) {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode team ref did not resolve to a supported commit")
	}
	entries, err := runGitRead(canonicalRoot, "ls-tree", "-z", "--full-tree", commit)
	if err != nil || !singleMancodeGitRefManifestTreeEntry(entries) {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode team ref tree must contain only a regular manifest.json blob")
	}
	sizeBytes, err := runGitRead(canonicalRoot, "cat-file", "-s", commit+":manifest.json")
	if err != nil {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode team ref manifest blob is unavailable")
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeBytes)), 10, 64)
	if err != nil || size < 1 || size > maxMancodeGitRefManifest {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode team ref manifest exceeds the 1 MB import limit")
	}
	data, err := runGitRead(canonicalRoot, "show", commit+":manifest.json")
	if err != nil || int64(len(data)) != size {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode team ref manifest changed or could not be read")
	}
	currentCommit, err := runGitRead(canonicalRoot, "rev-parse", "--verify", "--end-of-options", mancodeTeamGitRef+"^{commit}")
	if err != nil || strings.TrimSpace(string(currentCommit)) != commit {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode team ref changed during inspection")
	}
	manifestValue, err := decodeStrictMancodeGitRefJSON(data)
	if err != nil {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode team ref manifest is malformed or ambiguous")
	}
	configWorkspace, configMode, err := readMancodeGitRefProjectConfig(canonicalRoot)
	if err != nil {
		return MancodeGitRefSource{}, nil, "", err
	}
	manifestObject := manifestValue.(map[string]any)
	workspaceID, ok := manifestObject["workspaceId"].(string)
	if !ok || !mancodeOperationIDPattern.MatchString(workspaceID) || !strings.EqualFold(workspaceID, configWorkspace) || configMode != "git-ref" {
		return MancodeGitRefSource{}, nil, "", errors.New("Mancode team ref workspace does not match the configured git-ref project")
	}
	sha := digest(data)
	return MancodeGitRefSource{Ref: mancodeTeamGitRef, Commit: commit, ManifestSHA256: sha, ManifestBytes: int64(len(data))}, data, workspaceID, nil
}

func readMancodeGitRefProjectConfig(root string) (string, string, error) {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", errors.New("Mancode git-ref project root is unavailable or unsafe")
	}
	if err := validateMancodeGitRefConfigParents(canonicalRoot); err != nil {
		return "", "", err
	}
	path := filepath.Join(canonicalRoot, ".mancode", "shared", "config.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > maxFileBytes {
		return "", "", errors.New("Mancode git-ref import requires a regular shared project config")
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	relativePath, relErr := filepath.Rel(canonicalRoot, resolvedPath)
	if err != nil || relErr != nil || filepath.IsAbs(relativePath) || relativePath == ".." ||
		strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", "", errors.New("Mancode shared project config resolves outside the project root")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", errors.New("Mancode shared project config could not be opened safely")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() || openedInfo.Size() != info.Size() {
		return "", "", errors.New("Mancode shared project config changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	finalInfo, statErr := file.Stat()
	if err != nil || statErr != nil || len(data) < 1 || int64(len(data)) > maxFileBytes ||
		int64(len(data)) != info.Size() || finalInfo.Size() != info.Size() || !finalInfo.ModTime().Equal(openedInfo.ModTime()) {
		return "", "", errors.New("Mancode shared project config changed during inspection")
	}
	if err := validateMancodeGitRefConfigParents(canonicalRoot); err != nil {
		return "", "", err
	}
	value, err := decodeStrictMancodeGitRefJSON(data)
	if err != nil {
		return "", "", errors.New("Mancode shared project config is malformed or ambiguous")
	}
	items, handled, err := normalizeMancodeProjectConfig(value)
	if err != nil || !handled || len(items) != 1 {
		return "", "", errors.New("Mancode shared project config is not a supported git-ref contract")
	}
	var config mancodeProjectConfigImport
	if err := json.Unmarshal(items[0].Payload, &config); err != nil || config.WorkspaceID == "" || config.TransportMode != "git-ref" {
		return "", "", errors.New("Mancode shared project config does not declare git-ref transport")
	}
	return config.WorkspaceID, config.TransportMode, nil
}

func validateMancodeGitRefConfigParents(root string) error {
	current := root
	for _, component := range []string{".mancode", "shared"} {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Mancode shared project config path contains an unsafe directory")
		}
	}
	return nil
}

func rejectGitAlternates(gitDirectory string) error {
	path := filepath.Join(gitDirectory, "objects", "info", "alternates")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 {
		return errors.New("Mancode git-ref source uses an unsafe Git object alternate")
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != "" {
		return errors.New("Mancode git-ref source must not depend on an external Git object alternate")
	}
	return nil
}

func rejectUnsafeGitMetadata(gitDirectory string) error {
	entries := 0
	err := filepath.WalkDir(gitDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maxMancodeGitMetadataEntries {
			return errors.New("Git metadata exceeds the local inspection entry limit")
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("Mancode git-ref source contains unsafe Git metadata entries")
		}
		return nil
	})
	if err != nil {
		return errors.New("Mancode git-ref source Git metadata is unsafe or unavailable")
	}
	return nil
}

func runGitRead(root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	command.Env = make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found || strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	command.Dir = root
	stdout := &boundedGitBuffer{limit: maxMancodeGitRefManifest}
	stderr := &boundedGitBuffer{limit: maxMancodeGitStderrBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if ctx.Err() != nil {
		return nil, errors.New("Git read exceeded the local inspection time limit")
	}
	if stdout.exceeded {
		return nil, errors.New("Git read exceeded the bounded output limit")
	}
	if err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

type boundedGitBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (buffer *boundedGitBuffer) Write(data []byte) (int, error) {
	remaining := buffer.limit - buffer.Len()
	if remaining <= 0 {
		buffer.exceeded = buffer.exceeded || len(data) > 0
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = buffer.Buffer.Write(data[:remaining])
		buffer.exceeded = true
		return len(data), nil
	}
	return buffer.Buffer.Write(data)
}

func singleMancodeGitRefManifestTreeEntry(data []byte) bool {
	if len(data) == 0 || data[len(data)-1] != 0 {
		return false
	}
	entries := bytes.Split(data[:len(data)-1], []byte{0})
	if len(entries) != 1 {
		return false
	}
	parts := bytes.SplitN(entries[0], []byte{'\t'}, 2)
	if len(parts) != 2 || string(parts[1]) != "manifest.json" {
		return false
	}
	metadata := strings.Fields(string(parts[0]))
	return len(metadata) == 3 && metadata[0] == "100644" && metadata[1] == "blob" &&
		mancodeGitRefObjectIDPattern.MatchString(metadata[2])
}

func sameFilesystemPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftInfo, leftErr := os.Stat(leftAbs)
	rightInfo, rightErr := os.Stat(rightAbs)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func decodeStrictMancodeGitRefJSON(data []byte) (any, error) {
	if len(data) == 0 || len(data) > maxMancodeGitRefManifest || !utf8.Valid(data) {
		return nil, errors.New("Mancode git-ref JSON size or encoding is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	value, err := decodeMancodeGitRefJSONValue(decoder, 0, &nodes)
	if err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("Mancode git-ref JSON has trailing data")
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("Mancode git-ref JSON root must be an object")
	}
	return value, nil
}

func decodeMancodeGitRefJSONValue(decoder *json.Decoder, depth int, nodes *int) (any, error) {
	if depth > 64 {
		return nil, errors.New("Mancode git-ref JSON is too deeply nested")
	}
	(*nodes)++
	if *nodes > 100_000 {
		return nil, errors.New("Mancode git-ref JSON exceeds its structural limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("Mancode git-ref JSON object key is invalid")
				}
				if _, duplicate := object[key]; duplicate {
					return nil, errors.New("Mancode git-ref JSON has duplicate object keys")
				}
				item, err := decodeMancodeGitRefJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				object[key] = item
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return nil, errors.New("Mancode git-ref JSON object is unterminated")
			}
			return object, nil
		case '[':
			items := make([]any, 0)
			for decoder.More() {
				item, err := decodeMancodeGitRefJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return nil, errors.New("Mancode git-ref JSON array is unterminated")
			}
			return items, nil
		default:
			return nil, errors.New("Mancode git-ref JSON delimiter is invalid")
		}
	case string, json.Number, bool, nil:
		return value, nil
	default:
		return nil, errors.New("Mancode git-ref JSON value is unsupported")
	}
}

func normalizeMancodeGitRefManifest(sourceRoot string, value any, source MancodeGitRefSource, workspaceID string) ([]normalizedItem, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("Mancode git-ref manifest must be an object")
	}
	version, versionOK := mancodeGitRefInt(object["schemaVersion"])
	allowed := []string{
		"schemaVersion", "workspaceId", "schemaEpoch", "minReaderVersion", "minWriterVersion",
		"transportEpoch", "configRevision", "configDigest", "authorityState", "authorityFreeze",
		"authorityTombstone", "revision", "lastOperationId", "actorProfiles", "ownershipFences",
		"claims", "handoffs", "taskBundles", "receipts", "lastMutation", "updatedAt",
	}
	if version == 2 {
		allowed = append(allowed, "privacyPolicy")
	}
	if !versionOK || (version != 1 && version != 2) || !mancodeHasAllKeys(object, allowed) || !mancodeHasOnlyKeys(object, allowed) {
		return nil, errors.New("Mancode git-ref manifest does not match a supported v1 or v2 field set")
	}
	manifestWorkspace, workspaceOK := object["workspaceId"].(string)
	schemaEpoch, schemaEpochOK := object["schemaEpoch"].(string)
	reader, readerOK := object["minReaderVersion"].(string)
	writer, writerOK := object["minWriterVersion"].(string)
	authorityState, authorityOK := object["authorityState"].(string)
	updatedAt, updatedAtOK := object["updatedAt"].(string)
	lastOperationID, lastOperationOK := object["lastOperationId"].(string)
	revision, revisionOK := mancodeGitRefPositiveInt(object["revision"])
	transportEpoch, transportOK := mancodeGitRefPositiveInt(object["transportEpoch"])
	configRevision, configRevisionOK := mancodeGitRefPositiveInt(object["configRevision"])
	configDigest, configDigestOK := object["configDigest"].(string)
	if !workspaceOK || !mancodeOperationIDPattern.MatchString(manifestWorkspace) || !strings.EqualFold(manifestWorkspace, workspaceID) ||
		!schemaEpochOK || !mancodeOperationIDPattern.MatchString(schemaEpoch) ||
		!readerOK || !mancodeGitRefVersionPattern.MatchString(reader) || !writerOK || !mancodeGitRefVersionPattern.MatchString(writer) ||
		!mancodeHasString([]string{"active", "frozen", "tombstoned"}, authorityState) || !authorityOK ||
		!mancodeRFC3339(updatedAt) || !updatedAtOK || !mancodeOperationIDPattern.MatchString(lastOperationID) || !lastOperationOK ||
		!revisionOK || !transportOK || !configRevisionOK || !configDigestOK || !mancodeGitRefDigestPattern.MatchString(configDigest) {
		return nil, errors.New("Mancode git-ref manifest header is invalid or bound to a different workspace")
	}
	collections := map[string]struct {
		limit  int
		values []any
	}{}
	for key, limit := range map[string]int{
		"actorProfiles": 256, "ownershipFences": 512, "claims": 2048,
		"handoffs": 1024, "taskBundles": 128, "receipts": 256,
	} {
		values, ok := object[key].([]any)
		if !ok || len(values) > limit {
			return nil, fmt.Errorf("Mancode git-ref manifest %s collection exceeds its schema limit", key)
		}
		for _, value := range values {
			if _, ok := value.(map[string]any); !ok {
				return nil, fmt.Errorf("Mancode git-ref manifest %s contains a non-object entry", key)
			}
		}
		collections[key] = struct {
			limit  int
			values []any
		}{limit, values}
	}
	freezePresent, err := validateMancodeGitRefOptionalObject(object, "authorityFreeze")
	if err != nil {
		return nil, err
	}
	tombstonePresent, err := validateMancodeGitRefOptionalObject(object, "authorityTombstone")
	if err != nil {
		return nil, err
	}
	privacyPolicyPresent := false
	if version == 2 {
		privacyPolicyPresent, err = validateMancodeGitRefOptionalObject(object, "privacyPolicy")
		if err != nil || !privacyPolicyPresent || !mancodeGitRefVersionAtLeast0605(reader) || !mancodeGitRefVersionAtLeast0605(writer) {
			return nil, errors.New("Mancode git-ref v2 privacy policy is missing or invalid")
		}
	}
	lastMutation, err := validateMancodeGitRefOptionalObject(object, "lastMutation")
	if err != nil {
		return nil, err
	}
	if err := validateMancodeGitRefManifestConsistency(object, collections, manifestWorkspace, revision, transportEpoch,
		freezePresent, tombstonePresent, lastMutation); err != nil {
		return nil, err
	}
	items := make([]normalizedItem, 0, 1+len(collections["actorProfiles"].values)+len(collections["claims"].values)+len(collections["handoffs"].values)+len(collections["ownershipFences"].values)+len(collections["receipts"].values))
	var privacyItems []normalizedItem
	var privacyErr error
	privacyExcludedDigests := map[string]bool{}
	if privacyPolicyPresent {
		privacyItems, privacyExcludedDigests, privacyErr = normalizeMancodeGitRefPrivacySnapshot(object["privacyPolicy"], manifestWorkspace)
	}
	summary := mancodeGitRefAuthorityImport{
		SchemaVersion: mancodeGitRefSchema, Authority: "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		SourceRef: source.Ref, SourceCommit: source.Commit, SourceManifestSHA256: source.ManifestSHA256,
		ManifestSchemaVersion: version, WorkspaceIDSHA256: mancodeCoordinationIdentityHash("workspace", manifestWorkspace),
		SchemaEpochPresent: true, MinimumReaderVersion: reader, MinimumWriterVersion: writer,
		TransportEpoch: transportEpoch, ConfigRevision: configRevision, AuthorityState: authorityState,
		AuthorityFreezePresent: freezePresent, AuthorityTombstonePresent: tombstonePresent, Revision: revision,
		ActorProfileCount: len(collections["actorProfiles"].values), OwnershipFenceCount: len(collections["ownershipFences"].values),
		ClaimCount: len(collections["claims"].values), HandoffCount: len(collections["handoffs"].values),
		TaskBundleCount: len(collections["taskBundles"].values), ReceiptCount: len(collections["receipts"].values),
		LastMutationPresent: lastMutation, PrivacyPolicyPresent: privacyPolicyPresent, UpdatedAt: updatedAt,
		SourceIdentityOmitted: true, ExecutionAuthorityOmitted: true,
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("encode Mancode git-ref authority summary: %w", err)
	}
	items = append(items, normalizedItem{Type: "mancode_git_ref_authority", Payload: payload})
	for _, profile := range collections["actorProfiles"].values {
		if mancodeGitRefPrivacyExcluded(profile, privacyExcludedDigests) {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref actor profile did not match the supported review-only contract"})
			continue
		}
		actorID, ok := profile.(map[string]any)["actorId"].(string)
		if !ok || !mancodeOperationIDPattern.MatchString(actorID) {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref actor profile did not match the supported review-only contract"})
			continue
		}
		normalized, _, err := normalizeMancodeActor(filepath.ToSlash(filepath.Join(".mancode", "shared", "team", "actors", actorID+".json")), profile)
		items = appendMancodeGitRefNormalized(items, normalized, err, "Mancode git-ref actor profile did not match the supported review-only contract")
	}
	for _, claim := range collections["claims"].values {
		if mancodeGitRefPrivacyExcluded(claim, privacyExcludedDigests) {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref claim did not match the supported review-only contract"})
			continue
		}
		claimID, ok := claim.(map[string]any)["claimId"].(string)
		if !ok || !mancodeOperationIDPattern.MatchString(claimID) {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref claim did not match the supported review-only contract"})
			continue
		}
		path := filepath.ToSlash(filepath.Join(".mancode", "shared", "team", "claims", claimID+".json"))
		normalized, err := normalizeMancodeClaimSummary(sourceRoot, path, claim)
		if err != nil {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref claim did not match the supported review-only contract"})
		} else {
			items = append(items, normalized)
		}
	}
	for _, handoff := range collections["handoffs"].values {
		if mancodeGitRefPrivacyExcluded(handoff, privacyExcludedDigests) {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref handoff did not match the supported review-only contract"})
			continue
		}
		handoffID, ok := handoff.(map[string]any)["handoffId"].(string)
		if !ok || !mancodeOperationIDPattern.MatchString(handoffID) {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref handoff did not match the supported review-only contract"})
			continue
		}
		path := filepath.ToSlash(filepath.Join(".mancode", "shared", "team", "handoffs", handoffID+".json"))
		normalized, err := normalizeMancodeHandoffSummary(path, handoff)
		if err != nil {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref handoff did not match the supported review-only contract"})
		} else {
			items = append(items, normalized)
		}
	}
	for _, fence := range collections["ownershipFences"].values {
		normalized, err := normalizeMancodeGitRefOwnershipFence(sourceRoot, fence, manifestWorkspace)
		if err != nil {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref ownership fence did not match the supported review-only contract"})
		} else {
			items = append(items, normalized)
		}
	}
	seenReceipts := make(map[string]bool, len(collections["receipts"].values)+1)
	for _, receipt := range collections["receipts"].values {
		normalized, key, err := normalizeMancodeGitRefReceipt(receipt)
		if err != nil {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref receipt did not match the supported review-only contract"})
			continue
		}
		seenReceipts[key] = true
		items = append(items, normalized)
	}
	if object["lastMutation"] != nil {
		normalized, key, err := normalizeMancodeGitRefReceipt(object["lastMutation"])
		if err != nil {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref last mutation did not match the supported review-only contract"})
		} else if !seenReceipts[key] {
			items = append(items, normalized)
		}
	}
	for _, bundle := range collections["taskBundles"].values {
		if privacyPolicyPresent && privacyErr != nil {
			items = append(items, normalizedItem{Rejected: true, Issue: "Mancode git-ref task bundle did not match the supported review-only workflow contract"})
			continue
		}
		normalized, err := normalizeMancodeGitRefTaskBundle(bundle, privacyExcludedDigests)
		items = appendMancodeGitRefNormalized(items, normalized, err, "Mancode git-ref task bundle did not match the supported review-only workflow contract")
	}
	if freezePresent || tombstonePresent {
		transitions, transitionErr := normalizeMancodeGitRefAuthorityTransitions(object, transportEpoch)
		items = appendMancodeGitRefNormalized(items, transitions, transitionErr,
			"Mancode git-ref authority transition did not match the supported review-only contract")
	}
	if privacyPolicyPresent {
		items = appendMancodeGitRefNormalized(items, privacyItems, privacyErr, "Mancode git-ref privacy policy did not match the supported review-only binding contract")
	}
	return items, nil
}

func validateMancodeGitRefManifestConsistency(object map[string]any, collections map[string]struct {
	limit  int
	values []any
}, workspaceID string, revision, transportEpoch int64, freezePresent, tombstonePresent, lastMutationPresent bool) error {
	state := mancodeString(object["authorityState"])
	switch state {
	case "active":
		if freezePresent || tombstonePresent {
			return errors.New("Mancode git-ref active authority contains a freeze or tombstone")
		}
	case "frozen":
		if !freezePresent || tombstonePresent || !validMancodeGitRefAuthorityDetail(object["authorityFreeze"], "frozenAt", transportEpoch, mancodeString(object["lastOperationId"])) {
			return errors.New("Mancode git-ref frozen authority details are inconsistent")
		}
	case "tombstoned":
		if !freezePresent || !tombstonePresent ||
			!validMancodeGitRefAuthorityDetail(object["authorityFreeze"], "frozenAt", transportEpoch, mancodeString(object["lastOperationId"])) ||
			!validMancodeGitRefAuthorityDetail(object["authorityTombstone"], "tombstonedAt", transportEpoch, mancodeString(object["lastOperationId"])) {
			return errors.New("Mancode git-ref tombstoned authority details are inconsistent")
		}
		freeze := object["authorityFreeze"].(map[string]any)
		tombstone := object["authorityTombstone"].(map[string]any)
		if freeze["successorMode"] != tombstone["successorMode"] || freeze["successorEpoch"] != tombstone["successorEpoch"] {
			return errors.New("Mancode git-ref freeze and tombstone do not agree")
		}
	default:
		return errors.New("Mancode git-ref authority state is invalid")
	}
	var lastReceipt map[string]any
	seenActorIDs := make(map[string]bool)
	for _, value := range collections["actorProfiles"].values {
		actorID := mancodeString(value.(map[string]any)["actorId"])
		if !mancodeOperationIDPattern.MatchString(actorID) || seenActorIDs[actorID] {
			return errors.New("Mancode git-ref actor profile identities are invalid or duplicated")
		}
		seenActorIDs[actorID] = true
	}
	seenFenceTasks := make(map[string]bool)
	fencesByTask := make(map[string]map[string]any)
	for _, value := range collections["ownershipFences"].values {
		fence := value.(map[string]any)
		taskKey, taskOK := mancodeGitRefTaskKey(fence["taskRef"])
		remote, ok := mancodeGitRefPositiveInt(fence["remoteRevision"])
		if !taskOK || seenFenceTasks[taskKey] || !ok || remote > revision {
			return errors.New("Mancode git-ref ownership fences are duplicated or future-dated")
		}
		seenFenceTasks[taskKey] = true
		fencesByTask[taskKey] = fence
	}
	seenClaimIDs := make(map[string]bool)
	for _, value := range collections["claims"].values {
		claim := value.(map[string]any)
		claimID := mancodeString(claim["claimId"])
		authority := mancodeMapValue(claim["authority"])
		remote, ok := mancodeGitRefOptionalRemoteRevision(authority["remoteRevision"])
		if !mancodeOperationIDPattern.MatchString(claimID) || seenClaimIDs[claimID] ||
			!strings.EqualFold(mancodeString(claim["workspaceId"]), workspaceID) || authority["mode"] != "git-ref" ||
			!ok || (remote != nil && *remote > revision) {
			return errors.New("Mancode git-ref claims do not match the configured workspace or authority")
		}
		seenClaimIDs[claimID] = true
	}
	seenHandoffIDs := make(map[string]bool)
	for _, value := range collections["handoffs"].values {
		handoff := value.(map[string]any)
		handoffID := mancodeString(handoff["handoffId"])
		transport := mancodeMapValue(handoff["transport"])
		transportRevision, ok := mancodeGitRefOptionalInt(transport["transportRevision"])
		if !mancodeOperationIDPattern.MatchString(handoffID) || seenHandoffIDs[handoffID] || transport["mode"] != "git-ref" ||
			!ok || transportRevision > revision {
			return errors.New("Mancode git-ref handoffs do not match the transport revision")
		}
		seenHandoffIDs[handoffID] = true
	}
	seenBundleTasks := make(map[string]bool)
	bundlesByTask := make(map[string]map[string]any)
	for _, value := range collections["taskBundles"].values {
		bundle := value.(map[string]any)
		taskKey, taskOK := mancodeGitRefTaskKey(bundle["taskRef"])
		bundleDigest := mancodeString(bundle["bundleDigest"])
		fence := fencesByTask[taskKey]
		if !taskOK || seenBundleTasks[taskKey] || !mancodeGitRefDigestPattern.MatchString(bundleDigest) || fence == nil ||
			bundle["taskRevision"] != fence["taskRevision"] || bundle["ownershipEpoch"] != fence["ownershipEpoch"] ||
			bundle["aggregateDigest"] != fence["aggregateDigest"] {
			return errors.New("Mancode git-ref task bundle does not reconcile with its ownership fence")
		}
		seenBundleTasks[taskKey] = true
		bundlesByTask[taskKey] = bundle
	}
	for _, value := range collections["handoffs"].values {
		handoff := value.(map[string]any)
		if handoff["state"] != "offered" {
			continue
		}
		taskKey, taskOK := mancodeGitRefTaskKey(handoff["taskRef"])
		bundle := bundlesByTask[taskKey]
		transport := mancodeMapValue(handoff["transport"])
		if !taskOK || bundle == nil || transport["taskBundleDigest"] != bundle["bundleDigest"] {
			return errors.New("Mancode git-ref offered handoff has no matching task bundle")
		}
	}
	seenReceiptRevisions := make(map[int64]bool)
	for _, value := range collections["receipts"].values {
		receipt := value.(map[string]any)
		remote, ok := mancodeGitRefPositiveInt(receipt["remoteRevision"])
		if !ok || remote > revision || seenReceiptRevisions[remote] {
			return errors.New("Mancode git-ref receipts are duplicated or future-dated")
		}
		seenReceiptRevisions[remote] = true
		if lastReceipt == nil || remote > mancodeGitRefPositiveIntValue(lastReceipt["remoteRevision"]) {
			lastReceipt = receipt
		}
	}
	if !lastMutationPresent || lastReceipt == nil {
		return errors.New("Mancode git-ref manifest is missing its committed last mutation receipt")
	}
	lastMutation := mancodeMapValue(object["lastMutation"])
	if !mancodeGitRefJSONEqual(lastReceipt, lastMutation) ||
		mancodeString(lastMutation["operationId"]) != mancodeString(object["lastOperationId"]) ||
		mancodeGitRefPositiveIntValue(lastMutation["remoteRevision"]) != revision {
		return errors.New("Mancode git-ref last mutation does not reconcile with the manifest revision")
	}
	return nil
}

func validMancodeGitRefAuthorityDetail(value any, timestampKey string, transportEpoch int64, lastOperationID string) bool {
	object, ok := value.(map[string]any)
	keys := []string{"schemaVersion", "successorMode", "successorEpoch", "operationId", timestampKey}
	version, versionOK := mancodeGitRefInt(object["schemaVersion"])
	if !ok || !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) || !versionOK || version != 1 {
		return false
	}
	epoch, epochOK := mancodeGitRefPositiveInt(object["successorEpoch"])
	mode := mancodeString(object["successorMode"])
	return epochOK && epoch > transportEpoch && (mode == "local" || mode == "git-ref") &&
		mancodeOperationIDPattern.MatchString(mancodeString(object["operationId"])) &&
		mancodeString(object["operationId"]) == lastOperationID && mancodeRFC3339(object[timestampKey])
}

func mancodeMapValue(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func mancodeGitRefOptionalRemoteRevision(value any) (*int64, bool) {
	if value == nil {
		return nil, true
	}
	text, ok := value.(string)
	if !ok || !mancodeGitRefRemoteRevisionPattern.MatchString(text) {
		return nil, false
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil, false
	}
	return &parsed, true
}

func mancodeGitRefOptionalInt(value any) (int64, bool) {
	if value == nil {
		return 0, true
	}
	return mancodeGitRefInt(value)
}

func mancodeGitRefTaskKey(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok || !mancodeHasAllKeys(object, []string{"namespace", "taskId"}) ||
		!mancodeHasOnlyKeys(object, []string{"namespace", "taskId"}) || object["namespace"] != "shared" {
		return "", false
	}
	taskID := mancodeString(object["taskId"])
	if !mancodeOperationIDPattern.MatchString(taskID) {
		return "", false
	}
	return "shared\x00" + taskID, true
}

func mancodeGitRefVersionAtLeast0605(value string) bool {
	parts := strings.SplitN(value, "-", 2)
	core := strings.Split(parts[0], ".")
	if len(core) != 3 {
		return false
	}
	parsed := [3]int{}
	for index := range parsed {
		part, err := strconv.Atoi(core[index])
		if err != nil || part < 0 {
			return false
		}
		parsed[index] = part
	}
	minimum := [3]int{0, 6, 5}
	for index := range parsed {
		if parsed[index] != minimum[index] {
			return parsed[index] > minimum[index]
		}
	}
	return len(parts) == 1
}

func mancodeGitRefPositiveIntValue(value any) int64 {
	parsed, _ := mancodeGitRefPositiveInt(value)
	return parsed
}

func mancodeGitRefJSONEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func appendMancodeGitRefNormalized(items, normalized []normalizedItem, err error, issue string) []normalizedItem {
	if err != nil || len(normalized) == 0 {
		return append(items, normalizedItem{Rejected: true, Issue: issue})
	}
	return append(items, normalized...)
}

func normalizeMancodeGitRefOwnershipFence(sourceRoot string, value any, workspaceID string) (normalizedItem, error) {
	object, ok := value.(map[string]any)
	allowed := []string{"schemaVersion", "taskRef", "ownerActorId", "ownershipEpoch", "taskRevision", "aggregateDigest", "remoteRevision", "lastOperationId", "updatedAt"}
	if !ok || !mancodeHasAllKeys(object, allowed) || !mancodeHasOnlyKeys(object, allowed) {
		return normalizedItem{}, errors.New("invalid ownership fence fields")
	}
	task, ok := object["taskRef"].(map[string]any)
	taskID, taskOK := task["taskId"].(string)
	ownerID, ownerOK := object["ownerActorId"].(string)
	operationID, operationOK := object["lastOperationId"].(string)
	remoteRevision, remoteOK := mancodeGitRefPositiveInt(object["remoteRevision"])
	if !ok || !taskOK || !mancodeOperationIDPattern.MatchString(taskID) || !ownerOK || !mancodeOperationIDPattern.MatchString(ownerID) ||
		!operationOK || !mancodeOperationIDPattern.MatchString(operationID) || !remoteOK {
		return normalizedItem{}, errors.New("invalid ownership fence identity")
	}
	projected := map[string]any{
		"schemaVersion": object["schemaVersion"], "workspaceId": workspaceID, "taskRef": object["taskRef"],
		"fenceRevision": remoteRevision, "taskRevision": object["taskRevision"], "aggregateDigest": object["aggregateDigest"],
		"ownershipEpoch": object["ownershipEpoch"], "codeRef": nil, "checkoutId": nil,
		"remoteRevision": strconv.FormatInt(remoteRevision, 10), "lastOperationId": object["lastOperationId"], "updatedAt": object["updatedAt"],
	}
	taskPath := filepath.ToSlash(filepath.Join(".mancode", "shared", "team", "task-heads", taskID+".json"))
	return normalizeMancodeTaskHeadSummary(sourceRoot, taskPath, projected)
}

func normalizeMancodeGitRefReceipt(value any) (normalizedItem, string, error) {
	object, ok := value.(map[string]any)
	allowed := []string{"schemaVersion", "kind", "operationId", "actorId", "taskRef", "remoteRevision", "ownershipEpoch", "entityDigests", "committedAt"}
	if !ok || !mancodeHasAllKeys(object, allowed) || !mancodeHasOnlyKeys(object, allowed) {
		return normalizedItem{}, "", errors.New("invalid receipt fields")
	}
	kind, kindOK := object["kind"].(string)
	if !kindOK || !mancodeHasString([]string{"actor_profile", "coordination", "authority_establish", "authority_freeze", "authority_unfreeze", "authority_tombstone", "privacy_policy"}, kind) {
		return normalizedItem{}, "", errors.New("invalid receipt kind")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	operationID, operationOK := object["operationId"].(string)
	actorID, actorOK := object["actorId"].(string)
	remoteRevision, revisionOK := mancodeGitRefPositiveInt(object["remoteRevision"])
	committedAt, timeOK := object["committedAt"].(string)
	if !versionOK || version != 1 || !operationOK || !mancodeOperationIDPattern.MatchString(operationID) ||
		!actorOK || !mancodeOperationIDPattern.MatchString(actorID) || !revisionOK || !timeOK || !mancodeRFC3339(committedAt) {
		return normalizedItem{}, "", errors.New("invalid receipt identity or timestamp")
	}
	taskPresent := object["taskRef"] != nil
	if taskPresent {
		task, ok := object["taskRef"].(map[string]any)
		if !ok || !mancodeHasAllKeys(task, []string{"namespace", "taskId"}) || !mancodeHasOnlyKeys(task, []string{"namespace", "taskId"}) ||
			task["namespace"] != "shared" || !mancodeOperationIDPattern.MatchString(mancodeString(task["taskId"])) {
			return normalizedItem{}, "", errors.New("invalid receipt task reference")
		}
	}
	epochPresent := object["ownershipEpoch"] != nil
	if (kind == "coordination") != taskPresent || (kind == "coordination") != epochPresent {
		return normalizedItem{}, "", errors.New("receipt kind and fencing fields disagree")
	}
	if epochPresent {
		if _, ok := mancodeNonNegativeInteger(object["ownershipEpoch"]); !ok {
			return normalizedItem{}, "", errors.New("invalid receipt ownership epoch")
		}
	}
	digests, ok := object["entityDigests"].(map[string]any)
	digestKeys := []string{"actorProfiles", "ownershipFence", "claims", "handoffs", "taskBundle"}
	if !ok || !mancodeHasAllKeys(digests, digestKeys) || !mancodeHasOnlyKeys(digests, digestKeys) {
		return normalizedItem{}, "", errors.New("invalid receipt entity digests")
	}
	digestPresent := make([]bool, len(digestKeys))
	for i, key := range digestKeys {
		value := digests[key]
		if value == nil {
			if key == "actorProfiles" || key == "claims" || key == "handoffs" {
				return normalizedItem{}, "", errors.New("required receipt digest is absent")
			}
			continue
		}
		text, ok := value.(string)
		if !ok || !mancodeGitRefDigestPattern.MatchString(text) {
			return normalizedItem{}, "", errors.New("receipt digest has an invalid encoding")
		}
		digestPresent[i] = true
	}
	if err := validateMancodeGitRefReceiptKind(kind, taskPresent, epochPresent, digestPresent); err != nil {
		return normalizedItem{}, "", err
	}
	payload, err := json.Marshal(mancodeGitRefReceiptImport{
		SchemaVersion: "threadforge.mancode-git-ref-receipt.v1", Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false, Kind: kind, TaskReferencePresent: taskPresent,
		RemoteRevision: remoteRevision, OwnershipEpochPresent: epochPresent,
		ActorProfileDigestPresent: digestPresent[0], OwnershipFenceDigestPresent: digestPresent[1],
		ClaimDigestPresent: digestPresent[2], HandoffDigestPresent: digestPresent[3], TaskBundleDigestPresent: digestPresent[4],
		CommittedAt: committedAt, SourceIdentityOmitted: true, MutationDigestValuesOmitted: true,
	})
	if err != nil {
		return normalizedItem{}, "", err
	}
	return normalizedItem{Type: "mancode_git_ref_receipt", Payload: payload}, digest(mustJSON(value)), nil
}

func validateMancodeGitRefReceiptKind(kind string, taskPresent, epochPresent bool, digests []bool) error {
	if kind != "coordination" && (taskPresent || epochPresent) {
		return errors.New("non-coordination receipt has task fencing fields")
	}
	if kind == "coordination" && (!taskPresent || !epochPresent || !digests[1] || !digests[4]) {
		return errors.New("coordination receipt lacks task fencing digests")
	}
	if kind == "actor_profile" && (!digests[0] || digests[1] || digests[4]) {
		return errors.New("actor profile receipt digest fields are inconsistent")
	}
	return nil
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func validateMancodeGitRefOptionalObject(object map[string]any, key string) (bool, error) {
	value, exists := object[key]
	if !exists {
		return false, fmt.Errorf("Mancode git-ref manifest is missing %s", key)
	}
	if value == nil {
		return false, nil
	}
	if _, ok := value.(map[string]any); !ok {
		return false, fmt.Errorf("Mancode git-ref manifest %s must be an object or null", key)
	}
	return true, nil
}

func mancodeGitRefInt(value any) (int64, bool) {
	parsed, ok := mancodeIntegerValue(value)
	return parsed, ok && parsed >= 0 && parsed <= 9_007_199_254_740_991
}

func mancodeGitRefPositiveInt(value any) (int64, bool) {
	parsed, ok := mancodeGitRefInt(value)
	return parsed, ok && parsed > 0
}
