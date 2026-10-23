package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	mancodeGitRefTaskBundleSummaryType  = "mancode_git_ref_task_bundle"
	mancodeGitRefTaskBundleSchema       = "threadforge.mancode-git-ref-task-bundle.v1"
	maxMancodeGitRefBundleArtifacts     = 16
	maxMancodeGitRefBundleArtifactBytes = 256_000
)

var mancodeGitRefBundleArtifactTypes = map[string]string{
	"metadata":     "workflow_metadata",
	"checkpoint":   "workflow_checkpoint",
	"requirements": "workflow_requirements",
	"review":       "workflow_review",
	"verification": "workflow_verification",
	"plan":         "workflow_plan",
	"summary":      "workflow_document",
}

var (
	mancodeGitRefBundleEmailPattern       = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	mancodeGitRefBundleWindowsPathPattern = regexp.MustCompile("\\b[A-Z]:\\\\[^\\s'\"`]+")
	mancodeGitRefBundleUnixPathPattern    = regexp.MustCompile("/(?:Users|home|private|var/folders|tmp|etc)/[^\\s'\"`]+")
)

type mancodeGitRefTaskBundleImport struct {
	SchemaVersion        string   `json:"schema_version"`
	Authority            string   `json:"authority"`
	HumanReviewRequired  bool     `json:"human_review_required"`
	ExecutionAuthorized  bool     `json:"execution_authorized"`
	TaskIdentityOmitted  bool     `json:"task_identity_omitted"`
	CodeReferenceOmitted bool     `json:"code_reference_omitted"`
	TaskRevision         int64    `json:"task_revision"`
	OwnershipEpoch       int64    `json:"ownership_epoch"`
	WorkflowStatus       string   `json:"workflow_status"`
	RequirementsStatus   string   `json:"requirements_status"`
	ReviewStatus         string   `json:"review_status"`
	VerificationStatus   string   `json:"verification_status"`
	ArtifactCount        int      `json:"artifact_count"`
	ArtifactKinds        []string `json:"artifact_kinds"`
	BundleDigestVerified bool     `json:"bundle_digest_verified"`
	AggregateVerified    bool     `json:"aggregate_verified"`
	CreatedAt            string   `json:"created_at"`
}

func normalizeMancodeGitRefTaskBundle(value any, excludedEntityDigests ...map[string]bool) ([]normalizedItem, error) {
	blockedDigests := map[string]bool{}
	if len(excludedEntityDigests) > 1 {
		return nil, errors.New("task bundle privacy exclusion context is ambiguous")
	}
	if len(excludedEntityDigests) == 1 && excludedEntityDigests[0] != nil {
		blockedDigests = excludedEntityDigests[0]
	}
	bundle, ok := value.(map[string]any)
	allowedBundleKeys := []string{
		"schemaVersion", "taskRef", "taskRevision", "ownershipEpoch", "aggregate", "aggregateDigest",
		"codeRef", "artifacts", "bundleDigest", "createdAt",
	}
	if !ok || !mancodeHasAllKeys(bundle, allowedBundleKeys) || !mancodeHasOnlyKeys(bundle, allowedBundleKeys) {
		return nil, errors.New("task bundle fields are unsupported")
	}
	version, versionOK := mancodeIntegerValue(bundle["schemaVersion"])
	taskRef, taskRefOK := mancodeTaskRefFromValue(bundle["taskRef"])
	taskRevision, revisionOK := mancodePositiveInteger(bundle["taskRevision"])
	ownershipEpoch, epochOK := mancodeNonNegativeInteger(bundle["ownershipEpoch"])
	createdAt, createdAtOK := bundle["createdAt"].(string)
	if !versionOK || version != 1 || !taskRefOK || taskRef["namespace"] != "shared" ||
		!revisionOK || !epochOK || !createdAtOK || !mancodeRFC3339(createdAt) {
		return nil, errors.New("task bundle header is unsupported")
	}
	if !validMancodeGitRefDigest(bundle["aggregateDigest"]) || !validMancodeGitRefDigest(bundle["bundleDigest"]) {
		return nil, errors.New("task bundle digest encoding is invalid")
	}
	if err := validateMancodeGitRefBundleCodeRef(bundle["codeRef"]); err != nil {
		return nil, err
	}
	if mancodeGitRefPrivacyExcluded(bundle["codeRef"], blockedDigests) {
		return nil, errors.New("task bundle contains privacy-excluded content")
	}
	aggregate, ok := bundle["aggregate"].(map[string]any)
	aggregateKeys := []string{
		"taskRef", "taskRevision", "ownershipEpoch", "metadataDigest", "requirementsDigest", "reviewDigest",
		"verificationDigest", "planVersion", "planDigest", "latestCheckpointId", "latestCheckpointDigest", "parentSnapshotDigest",
	}
	if !ok || !mancodeHasAllKeys(aggregate, aggregateKeys) || !mancodeHasOnlyKeys(aggregate, aggregateKeys) {
		return nil, errors.New("task bundle aggregate schema is unsupported")
	}
	aggregateDigest, err := mancodeCanonicalDigest(aggregate)
	if err != nil || aggregateDigest != bundle["aggregateDigest"] {
		return nil, errors.New("task bundle aggregate digest does not match")
	}

	artifacts, ok := bundle["artifacts"].([]any)
	if !ok || len(artifacts) > maxMancodeGitRefBundleArtifacts {
		return nil, errors.New("task bundle artifact count exceeds the supported limit")
	}
	artifactByKind := make(map[string]map[string]any, len(artifacts))
	artifactPaths := make(map[string]bool, len(artifacts))
	orderedArtifacts := make([]map[string]any, 0, len(artifacts))
	for _, value := range artifacts {
		artifact, ok := value.(map[string]any)
		artifactKeys := []string{"kind", "relativePath", "content", "contentDigest"}
		if !ok || !mancodeHasAllKeys(artifact, artifactKeys) || !mancodeHasOnlyKeys(artifact, artifactKeys) {
			return nil, errors.New("task bundle artifact schema is unsupported")
		}
		kind, kindOK := artifact["kind"].(string)
		relativePath, pathOK := artifact["relativePath"].(string)
		if !kindOK || !pathOK || !validMancodeGitRefBundleArtifactPath(kind, relativePath) ||
			artifactByKind[kind] != nil || artifactPaths[relativePath] {
			return nil, errors.New("task bundle artifact kind or path is invalid")
		}
		contentBytes, err := encodeMancodeGitRefBundleJSON(artifact["content"])
		if err != nil || len(contentBytes) > maxMancodeGitRefBundleArtifactBytes {
			return nil, errors.New("task bundle artifact exceeds the supported size")
		}
		contentDigest, err := mancodeCanonicalDigest(artifact["content"])
		if err != nil || contentDigest != artifact["contentDigest"] {
			return nil, errors.New("task bundle artifact content digest does not match")
		}
		if blockedDigests[contentDigest] {
			return nil, errors.New("task bundle contains privacy-excluded content")
		}
		if kind == "plan" || kind == "summary" {
			if _, ok := artifact["content"].(string); !ok {
				return nil, errors.New("task bundle text artifact is not a string")
			}
		}
		artifactByKind[kind] = artifact
		artifactPaths[relativePath] = true
		orderedArtifacts = append(orderedArtifacts, artifact)
	}
	for _, required := range []string{"metadata", "requirements", "review", "verification"} {
		if artifactByKind[required] == nil {
			return nil, errors.New("task bundle is missing a required workflow artifact")
		}
	}
	if artifactByKind["plan"] == nil && artifactByKind["summary"] == nil {
		return nil, errors.New("task bundle is missing its plan or summary artifact")
	}
	sort.Slice(orderedArtifacts, func(i, j int) bool {
		return mancodeString(orderedArtifacts[i]["kind"]) < mancodeString(orderedArtifacts[j]["kind"])
	})

	// Mancode canonicalizes artifact order before validating the bundle digest.
	unsignedBundle := make(map[string]any, len(bundle)-1)
	for key, field := range bundle {
		if key != "bundleDigest" {
			unsignedBundle[key] = field
		}
	}
	canonicalArtifacts := make([]any, len(orderedArtifacts))
	for i, artifact := range orderedArtifacts {
		canonicalArtifacts[i] = artifact
	}
	unsignedBundle["artifacts"] = canonicalArtifacts
	bundleDigest, err := mancodeCanonicalDigest(unsignedBundle)
	if err != nil || bundleDigest != bundle["bundleDigest"] {
		return nil, errors.New("task bundle digest does not match")
	}

	// Recreate the workflow's documented source paths in memory so the existing
	// v3 validator checks ledger schemas, cross-ledger digests, checkpoint links,
	// plan digest, and task/ownership revisions before any records are emitted.
	taskID := mancodeString(taskRef["taskId"])
	directory := filepath.ToSlash(filepath.Join(".mancode", "shared", "workflows", taskID))
	files := make(map[string]scannedFile, len(artifacts))
	itemsByKind := make(map[string]normalizedItem, len(artifacts))
	kinds := make([]string, 0, len(artifacts))
	for _, artifact := range orderedArtifacts {
		kind := mancodeString(artifact["kind"])
		relativePath := mancodeString(artifact["relativePath"])
		path := filepath.ToSlash(filepath.Join(directory, filepath.FromSlash(relativePath)))
		content := artifact["content"]
		payloadValue := content
		if kind == "plan" || kind == "summary" {
			payloadValue = map[string]any{"content": content}
		}
		payload, err := json.Marshal(payloadValue)
		if err != nil {
			return nil, errors.New("task bundle artifact could not be normalized")
		}
		if privacy.ContainsCredential(payload) || mancodeGitRefBundleContainsUnsafeText(payloadValue) {
			return nil, errors.New("task bundle contains privacy-blocked content")
		}
		recordType := mancodeGitRefBundleArtifactTypes[kind]
		file := scannedFile{
			entry:   FileEntry{Path: path, SourceSHA256: digest(payload), RecordCount: 1, ImportedCount: 1},
			records: []Record{{Key: path + "#000001", Type: recordType, SourcePath: path, ContentSHA256: digest(payload), Scope: "local_pending_review", Payload: payload}},
		}
		files[path] = file
		itemsByKind[kind] = normalizedItem{Type: recordType, Payload: payload}
		kinds = append(kinds, kind)
	}
	metadataPath := filepath.ToSlash(filepath.Join(directory, "metadata.json"))
	metadata, ok := bundleArtifactObject(artifactByKind["metadata"])
	if !ok {
		return nil, errors.New("task bundle metadata artifact is not an object")
	}
	metadataFile, ok := files[metadataPath]
	if !ok {
		return nil, errors.New("task bundle metadata path is invalid")
	}
	aggregatePayload, err := reconstructMancodeWorkflowAggregate(
		mancodeWorkflowLocation{directory: directory, namespace: "shared", taskID: taskID},
		metadataFile, metadata, files,
	)
	if err != nil {
		return nil, errors.New("task bundle workflow could not be validated")
	}
	var reconstructed map[string]any
	decoder := json.NewDecoder(bytes.NewReader(aggregatePayload))
	decoder.UseNumber()
	if decoder.Decode(&reconstructed) != nil || reconstructed["status"] != "complete" {
		return nil, errors.New("task bundle workflow is incomplete or conflicting")
	}
	if err := validateMancodeGitRefBundleAggregate(bundle, taskRef, taskRevision, ownershipEpoch, reconstructed); err != nil {
		return nil, err
	}

	metadataStatus, _ := metadata["status"].(string)
	requirements, _ := bundleArtifactObject(artifactByKind["requirements"])
	review, _ := bundleArtifactObject(artifactByKind["review"])
	verification, _ := bundleArtifactObject(artifactByKind["verification"])
	importSummary := mancodeGitRefTaskBundleImport{
		SchemaVersion: mancodeGitRefTaskBundleSchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false, TaskIdentityOmitted: true, CodeReferenceOmitted: true,
		TaskRevision: taskRevision, OwnershipEpoch: ownershipEpoch, WorkflowStatus: metadataStatus,
		RequirementsStatus: mancodeString(requirements["status"]), ReviewStatus: mancodeString(review["status"]),
		VerificationStatus: mancodeString(verification["status"]), ArtifactCount: len(artifacts), ArtifactKinds: kinds,
		BundleDigestVerified: true, AggregateVerified: true, CreatedAt: createdAt,
	}
	summaryPayload, err := json.Marshal(importSummary)
	if err != nil {
		return nil, errors.New("task bundle summary could not be encoded")
	}

	items := make([]normalizedItem, 0, len(artifacts)+1)
	for _, kind := range kinds {
		items = append(items, itemsByKind[kind])
	}
	items = append(items, normalizedItem{Type: mancodeGitRefTaskBundleSummaryType, Payload: summaryPayload})
	return items, nil
}

func validateMancodeGitRefBundleCodeRef(value any) error {
	object, ok := value.(map[string]any)
	keys := []string{"branch", "head"}
	if !ok || !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return errors.New("task bundle code reference schema is unsupported")
	}
	branch, branchOK := object["branch"].(string)
	head, headOK := object["head"].(string)
	if !branchOK || strings.TrimSpace(branch) == "" || strings.ContainsRune(branch, 0) ||
		mancodeGitRefBundleTextIsSensitive(branch) || !headOK || !mancodeGitRefObjectIDPattern.MatchString(head) {
		return errors.New("task bundle code reference is invalid")
	}
	return nil
}

func validMancodeGitRefBundleArtifactPath(kind, path string) bool {
	fixed := map[string]string{
		"metadata": "metadata.json", "requirements": "requirements.json", "review": "review-ledger.json",
		"verification": "verification-ledger.json", "plan": "plan.md", "summary": "summary.md",
	}
	if expected, ok := fixed[kind]; ok {
		return path == expected
	}
	if kind != "checkpoint" || !strings.HasPrefix(path, "checkpoints/") || !strings.HasSuffix(path, ".json") {
		return false
	}
	checkpointID := strings.TrimSuffix(strings.TrimPrefix(path, "checkpoints/"), ".json")
	return mancodeOperationIDPattern.MatchString(checkpointID)
}

func mancodeGitRefBundleContainsUnsafeText(value any) bool {
	switch typed := value.(type) {
	case string:
		return mancodeGitRefBundleTextIsSensitive(typed)
	case []any:
		for _, item := range typed {
			if mancodeGitRefBundleContainsUnsafeText(item) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if mancodeGitRefBundleTextIsSensitive(key) || mancodeGitRefBundleContainsUnsafeText(item) {
				return true
			}
		}
	}
	return false
}

func mancodeGitRefBundleTextIsSensitive(value string) bool {
	return privacy.ContainsCredentialText(value) || mancodeGitRefBundleEmailPattern.MatchString(value) ||
		mancodeGitRefBundleWindowsPathPattern.MatchString(value) || mancodeGitRefBundleUnixPathPattern.MatchString(value)
}

func validateMancodeGitRefBundleAggregate(bundle map[string]any, taskRef map[string]any, taskRevision, ownershipEpoch int64, reconstructed map[string]any) error {
	reconstructedTaskRef, ok := reconstructed["task_ref"].(map[string]any)
	if reconstructed["aggregate_digest"] == nil || !ok {
		return errors.New("task bundle aggregate reconstruction is incomplete")
	}
	canonicalTaskRef := map[string]any{"namespace": reconstructedTaskRef["namespace"], "taskId": reconstructedTaskRef["task_id"]}
	if !mancodeGitRefJSONEqual(canonicalTaskRef, taskRef) ||
		mancodeInteger(reconstructed["task_revision"]) != taskRevision ||
		mancodeInteger(reconstructed["ownership_epoch"]) != ownershipEpoch {
		return errors.New("task bundle aggregate identity does not match its workflow")
	}
	aggregate := bundle["aggregate"].(map[string]any)
	expected := map[string]any{
		"taskRef": taskRef, "taskRevision": reconstructed["task_revision"], "ownershipEpoch": reconstructed["ownership_epoch"],
		"metadataDigest": reconstructed["metadata_digest"], "requirementsDigest": reconstructed["requirements_digest"],
		"reviewDigest": reconstructed["review_digest"], "verificationDigest": reconstructed["verification_digest"],
		"planVersion": reconstructed["plan_version"], "planDigest": reconstructed["plan_digest"],
		"latestCheckpointId": reconstructed["latest_checkpoint_id"], "latestCheckpointDigest": reconstructed["latest_checkpoint_digest"],
		"parentSnapshotDigest": reconstructed["parent_snapshot_digest"],
	}
	if !mancodeGitRefJSONEqual(aggregate, expected) {
		return errors.New("task bundle aggregate does not reconcile with its workflow artifacts")
	}
	if reconstructed["aggregate_digest"] != bundle["aggregateDigest"] {
		return errors.New("task bundle aggregate digest does not match its workflow artifacts")
	}
	checkpointArtifact := false
	for _, artifact := range bundle["artifacts"].([]any) {
		if mancodeString(artifact.(map[string]any)["kind"]) == "checkpoint" {
			checkpointArtifact = true
			break
		}
	}
	checkpointID := aggregate["latestCheckpointId"]
	if (checkpointID == nil && checkpointArtifact) || (checkpointID != nil && !checkpointArtifact) {
		return errors.New("task bundle checkpoint artifact does not match its aggregate")
	}
	if checkpointID != nil {
		for _, artifactValue := range bundle["artifacts"].([]any) {
			artifact := artifactValue.(map[string]any)
			if artifact["kind"] == "checkpoint" && artifact["relativePath"] != "checkpoints/"+mancodeString(checkpointID)+".json" {
				return errors.New("task bundle checkpoint path does not match its aggregate")
			}
		}
	}
	return nil
}

func bundleArtifactObject(artifact map[string]any) (map[string]any, bool) {
	if artifact == nil {
		return nil, false
	}
	object, ok := artifact["content"].(map[string]any)
	return object, ok
}

func validMancodeGitRefDigest(value any) bool {
	digest, ok := value.(string)
	return ok && mancodeGitRefDigestPattern.MatchString(digest)
}

func encodeMancodeGitRefBundleJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode bundle JSON: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}
