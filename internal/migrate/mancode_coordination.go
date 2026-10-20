package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	mancodeClaimSummarySchema     = "threadforge.mancode-claim-summary.v1"
	mancodeHandoffSummarySchema   = "threadforge.mancode-handoff-summary.v1"
	mancodeHeadFenceSummarySchema = "threadforge.mancode-task-head-summary.v1"
)

type mancodeClaimSummary struct {
	SchemaVersion             string `json:"schema_version"`
	Authority                 string `json:"authority"`
	HumanReviewRequired       bool   `json:"human_review_required"`
	ExecutionAuthorized       bool   `json:"execution_authorized"`
	SourceIDSHA256            string `json:"source_id_sha256"`
	WorkspaceIDSHA256         string `json:"workspace_id_sha256"`
	TaskReferenceSHA256       string `json:"task_reference_sha256"`
	SourceState               string `json:"source_state"`
	Revision                  int64  `json:"revision"`
	TaskRevisionAtAcquire     int64  `json:"task_revision_at_acquire"`
	LastValidatedTaskRevision int64  `json:"last_validated_task_revision"`
	OwnershipEpochAtAcquire   int64  `json:"ownership_epoch_at_acquire"`
	PathScopeCount            int    `json:"path_scope_count"`
	ModuleScopeCount          int    `json:"module_scope_count"`
	APIScopeCount             int    `json:"api_scope_count"`
	SchemaScopeCount          int    `json:"schema_scope_count"`
	ScopeDigestPresent        bool   `json:"scope_digest_present"`
	CodeBaselinePresent       bool   `json:"code_baseline_present"`
	ExpiresAt                 string `json:"expires_at"`
	CreatedAt                 string `json:"created_at"`
	UpdatedAt                 string `json:"updated_at"`
	RawActorIdentityOmitted   bool   `json:"raw_actor_identity_omitted"`
	RawScopeValuesOmitted     bool   `json:"raw_scope_values_omitted"`
	SourceAuthorityOmitted    bool   `json:"source_authority_omitted"`
}

type mancodeHandoffSummary struct {
	SchemaVersion             string  `json:"schema_version"`
	Authority                 string  `json:"authority"`
	HumanReviewRequired       bool    `json:"human_review_required"`
	ExecutionAuthorized       bool    `json:"execution_authorized"`
	SourceIDSHA256            string  `json:"source_id_sha256"`
	TaskReferenceSHA256       string  `json:"task_reference_sha256"`
	SourceState               string  `json:"source_state"`
	Revision                  int64   `json:"revision"`
	TaskRevision              int64   `json:"task_revision"`
	OwnershipEpochAtOffer     int64   `json:"ownership_epoch_at_offer"`
	ClaimReferenceCount       int     `json:"claim_reference_count"`
	CheckpointPresent         bool    `json:"checkpoint_present"`
	CompletedCount            int     `json:"completed_count"`
	InProgressCount           int     `json:"in_progress_count"`
	NotStartedCount           int     `json:"not_started_count"`
	ChangedFileCount          int     `json:"changed_file_count"`
	VerificationItemCount     int     `json:"verification_item_count"`
	BlockerCount              int     `json:"blocker_count"`
	RiskCount                 int     `json:"risk_count"`
	NextActionPresent         bool    `json:"next_action_present"`
	TransportMode             string  `json:"transport_mode"`
	TransportReceiptPresent   bool    `json:"transport_receipt_present"`
	CodeReferencePresent      bool    `json:"code_reference_present"`
	ResolutionPresent         bool    `json:"resolution_present"`
	OfferedAt                 *string `json:"offered_at"`
	CreatedAt                 string  `json:"created_at"`
	UpdatedAt                 string  `json:"updated_at"`
	RawActorIdentitiesOmitted bool    `json:"raw_actor_identities_omitted"`
	RawSummaryTextOmitted     bool    `json:"raw_summary_text_omitted"`
	SourceAuthorityOmitted    bool    `json:"source_authority_omitted"`
}

type mancodeTaskHeadSummary struct {
	SchemaVersion          string `json:"schema_version"`
	Authority              string `json:"authority"`
	HumanReviewRequired    bool   `json:"human_review_required"`
	ExecutionAuthorized    bool   `json:"execution_authorized"`
	WorkspaceIDSHA256      string `json:"workspace_id_sha256"`
	TaskReferenceSHA256    string `json:"task_reference_sha256"`
	FenceRevision          int64  `json:"fence_revision"`
	TaskRevision           int64  `json:"task_revision"`
	OwnershipEpoch         int64  `json:"ownership_epoch"`
	AggregateDigestPresent bool   `json:"aggregate_digest_present"`
	CodeReferencePresent   bool   `json:"code_reference_present"`
	CheckoutBindingPresent bool   `json:"checkout_binding_present"`
	RemoteRevisionPresent  bool   `json:"remote_revision_present"`
	UpdatedAt              string `json:"updated_at"`
	RawTaskIdentityOmitted bool   `json:"raw_task_identity_omitted"`
	SourceAuthorityOmitted bool   `json:"source_authority_omitted"`
}

func isMancodeCoordinationRecordType(recordType string) bool {
	switch recordType {
	case "mancode_team_claim", "mancode_team_handoff", "mancode_team_head_fence", "mancode_operation_audit":
		return true
	default:
		return false
	}
}

// mancodeCoordinationSourceAlias keeps source identifiers embedded in coordination
// filenames out of imported record keys. The original relative path remains only
// in the private migration manifest used for source reconciliation.
func mancodeCoordinationSourceAlias(relative, recordType string) string {
	return "mancode/coordination/" + strings.TrimPrefix(recordType, "mancode_") + "/" + digest([]byte(relative))
}

func mancodeCoordinationIdentityHash(kind, value string) string {
	return digest([]byte("threadforge/mancode/coordination/v1\x00" + kind + "\x00" + value))
}

func mancodeCoordinationTaskHash(taskRef map[string]any) string {
	namespace := mancodeString(taskRef["namespace"])
	taskID := mancodeString(taskRef["taskId"])
	return mancodeCoordinationIdentityHash("task-reference", namespace+"\x00"+taskID)
}

func mancodeCoordinationTime(value any) (string, bool) {
	text, ok := value.(string)
	if !ok || !mancodeBoundedString(text, 1, 64) {
		return "", false
	}
	_, err := time.Parse(time.RFC3339Nano, text)
	return text, err == nil
}

func mancodeCoordinationMode(value any) (string, bool) {
	mode, ok := value.(string)
	if !ok {
		return "", false
	}
	switch mode {
	case "local":
		return mode, true
	case "git_ref", "git-ref":
		return "git_ref", true
	default:
		return "", false
	}
}

func mancodeCoordinationOptionalTime(value any) bool {
	return value == nil || func() bool { _, ok := mancodeCoordinationTime(value); return ok }()
}

func mancodeCoordinationOptionalULID(object map[string]any, key string) bool {
	value, exists := object[key]
	if !exists || value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && mancodeOperationIDPattern.MatchString(text)
}

func mancodeCoordinationCodeReference(value any, allowedKeys []string) bool {
	if value == nil {
		return true
	}
	object, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(object, allowedKeys) || len(object) == 0 {
		return false
	}
	for _, item := range object {
		text, ok := item.(string)
		if !ok || !mancodeBoundedString(text, 1, 2048) {
			return false
		}
	}
	return true
}

func mancodeCoordinationObject(value any, required, allowed []string) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	if !ok || !mancodeHasAllKeys(object, required) || !mancodeHasOnlyKeys(object, allowed) {
		return nil, false
	}
	return object, true
}

func mancodeCoordinationScopeCounts(value any) (paths, modules, apis, schemas int, ok bool) {
	keys := []string{"paths", "modules", "apis", "schemas"}
	scope, valid := mancodeCoordinationObject(value, keys, keys)
	if !valid {
		return 0, 0, 0, 0, false
	}
	counts := make([]int, len(keys))
	for index, key := range keys {
		items, valid := scope[key].([]any)
		if !valid || len(items) > 4096 {
			return 0, 0, 0, 0, false
		}
		for _, item := range items {
			text, isString := item.(string)
			if !isString || !mancodeBoundedString(text, 1, 4096) || strings.TrimSpace(text) == "" {
				return 0, 0, 0, 0, false
			}
		}
		counts[index] = len(items)
	}
	return counts[0], counts[1], counts[2], counts[3], true
}

func normalizeMancodeCoordinationRecord(sourceRoot, relative, recordType string, value any) (normalizedItem, error) {
	switch recordType {
	case "mancode_team_claim":
		return normalizeMancodeClaimSummary(sourceRoot, relative, value)
	case "mancode_team_handoff":
		return normalizeMancodeHandoffSummary(relative, value)
	case "mancode_team_head_fence":
		return normalizeMancodeTaskHeadSummary(sourceRoot, relative, value)
	case "mancode_operation_audit":
		return normalizeMancodeOperationAudit(relative, value)
	default:
		return normalizedItem{}, errors.New("unsupported Mancode coordination record type")
	}
}

func normalizeMancodeClaimSummary(sourceRoot, relative string, value any) (normalizedItem, error) {
	allowed := []string{
		"schemaVersion", "claimId", "workspaceId", "coordinationDomainId", "authority", "taskRef",
		"taskRevisionAtAcquire", "lastValidatedTaskRevision", "implementationScopeDigest", "ownershipEpochAtAcquire",
		"ownerActorId", "state", "revision", "scope", "scopeDigest", "codeRefAtAcquire", "lastValidatedCodeRef",
		"acquisitionEnforcement", "writeGuard", "expiresAt", "predecessorClaimId", "successorClaimId",
		"lastOperationId", "createdAt", "updatedAt",
	}
	object, ok := mancodeCoordinationObject(value, allowed, allowed)
	if !ok {
		return normalizedItem{}, errors.New("Mancode claim does not match the supported v1 contract")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	claimID := mancodeString(object["claimId"])
	filenameID := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	workspaceID, workspaceOK := object["workspaceId"].(string)
	coordinationDomainID, domainOK := object["coordinationDomainId"].(string)
	ownerActorID, actorOK := object["ownerActorId"].(string)
	taskRef, taskOK := mancodeTaskRefFromValue(object["taskRef"])
	state := mancodeString(object["state"])
	revision, revisionOK := mancodePositiveInteger(object["revision"])
	taskRevision, taskRevisionOK := mancodePositiveInteger(object["taskRevisionAtAcquire"])
	validatedRevision, validatedRevisionOK := mancodeNonNegativeInteger(object["lastValidatedTaskRevision"])
	epoch, epochOK := mancodeNonNegativeInteger(object["ownershipEpochAtAcquire"])
	paths, modules, apis, schemas, scopeOK := mancodeCoordinationScopeCounts(object["scope"])
	expiresAt, expiresOK := mancodeCoordinationTime(object["expiresAt"])
	createdAt, createdOK := mancodeCoordinationTime(object["createdAt"])
	updatedAt, updatedOK := mancodeCoordinationTime(object["updatedAt"])
	authority, authorityOK := mancodeCoordinationObject(object["authority"], []string{"mode", "remoteRevision"}, []string{"mode", "remoteRevision"})
	_, modeOK := mancodeCoordinationMode(authority["mode"])
	if !versionOK || version != 1 || !mancodeOperationIDPattern.MatchString(claimID) || claimID != filenameID ||
		!workspaceOK || !mancodeOperationIDPattern.MatchString(workspaceID) ||
		!mancodeCoordinationWorkspaceBinding(sourceRoot, relative, workspaceID) ||
		!domainOK || !mancodeOperationIDPattern.MatchString(coordinationDomainID) ||
		!actorOK || !mancodeOperationIDPattern.MatchString(ownerActorID) || !taskOK ||
		!mancodeHasString([]string{"pending", "active", "released", "expired"}, state) || !revisionOK || !taskRevisionOK ||
		!validatedRevisionOK || !epochOK || !scopeOK ||
		!expiresOK || !createdOK || !updatedOK || !authorityOK || !modeOK ||
		!mancodeCoordinationOptionalULID(object, "predecessorClaimId") || !mancodeCoordinationOptionalULID(object, "successorClaimId") ||
		!mancodeCoordinationOptionalULID(object, "lastOperationId") ||
		!mancodeCoordinationCodeReference(object["codeRefAtAcquire"], []string{"branch", "head", "base"}) ||
		!mancodeCoordinationCodeReference(object["lastValidatedCodeRef"], []string{"branch", "head", "base"}) {
		return normalizedItem{}, errors.New("Mancode claim contains an invalid identity, scope, revision, or timestamp")
	}
	implementationDigest, implementationDigestOK := object["implementationScopeDigest"].(string)
	if !implementationDigestOK || !mancodeBoundedString(implementationDigest, 1, 256) {
		return normalizedItem{}, errors.New("Mancode claim implementation scope digest is invalid")
	}
	scopeDigest, scopeDigestOK := object["scopeDigest"].(string)
	if !scopeDigestOK || !mancodeBoundedString(scopeDigest, 1, 256) {
		return normalizedItem{}, errors.New("Mancode claim scope digest is invalid")
	}
	if object["acquisitionEnforcement"] != nil {
		if _, ok := object["acquisitionEnforcement"].(map[string]any); !ok {
			return normalizedItem{}, errors.New("Mancode claim enforcement metadata is invalid")
		}
	}
	if object["writeGuard"] != nil {
		if _, ok := object["writeGuard"].(map[string]any); !ok {
			return normalizedItem{}, errors.New("Mancode claim write-guard metadata is invalid")
		}
	}
	if remoteRevision := authority["remoteRevision"]; remoteRevision != nil {
		text, ok := remoteRevision.(string)
		if !ok || !mancodeBoundedString(text, 1, 256) {
			return normalizedItem{}, errors.New("Mancode claim remote revision is invalid")
		}
	}
	created, _ := time.Parse(time.RFC3339Nano, createdAt)
	updated, _ := time.Parse(time.RFC3339Nano, updatedAt)
	expires, _ := time.Parse(time.RFC3339Nano, expiresAt)
	if updated.Before(created) || expires.Before(created) {
		return normalizedItem{}, errors.New("Mancode claim timestamps are out of order")
	}
	payload, err := json.Marshal(mancodeClaimSummary{
		SchemaVersion: mancodeClaimSummarySchema, Authority: "local_pending_review", HumanReviewRequired: true,
		ExecutionAuthorized: false, SourceIDSHA256: mancodeCoordinationIdentityHash("claim", claimID),
		WorkspaceIDSHA256:   mancodeCoordinationIdentityHash("workspace", workspaceID),
		TaskReferenceSHA256: mancodeCoordinationTaskHash(taskRef), SourceState: state, Revision: revision,
		TaskRevisionAtAcquire: taskRevision, LastValidatedTaskRevision: validatedRevision,
		OwnershipEpochAtAcquire: epoch, PathScopeCount: paths, ModuleScopeCount: modules, APIScopeCount: apis,
		SchemaScopeCount: schemas, ScopeDigestPresent: true,
		CodeBaselinePresent: object["codeRefAtAcquire"] != nil, ExpiresAt: expiresAt,
		CreatedAt: createdAt, UpdatedAt: updatedAt, RawActorIdentityOmitted: true,
		RawScopeValuesOmitted: true, SourceAuthorityOmitted: true,
	})
	if err != nil {
		return normalizedItem{}, fmt.Errorf("encode Mancode claim summary: %w", err)
	}
	return normalizedItem{Type: "mancode_team_claim", Payload: payload}, nil
}

func mancodeCoordinationStringListCount(value any, maximumItems, maximumBytes int) (int, bool) {
	items, ok := value.([]any)
	if !ok || len(items) > maximumItems {
		return 0, false
	}
	for _, item := range items {
		text, ok := item.(string)
		if !ok || !mancodeBoundedString(text, 1, maximumBytes) || strings.TrimSpace(text) == "" {
			return 0, false
		}
	}
	return len(items), true
}

func mancodeCoordinationTextPresenceAndCount(value any, maximumItems, maximumBytes int) (int, bool) {
	if text, ok := value.(string); ok {
		if !mancodeBoundedString(text, 0, maximumBytes) {
			return 0, false
		}
		if strings.TrimSpace(text) == "" {
			return 0, true
		}
		return 1, true
	}
	return mancodeCoordinationStringListCount(value, maximumItems, maximumBytes)
}

func normalizeMancodeHandoffSummary(relative string, value any) (normalizedItem, error) {
	allowed := []string{
		"schemaVersion", "handoffId", "taskRef", "taskRevision", "ownershipEpochAtOffer", "state", "revision",
		"fromActorId", "toActorId", "claimIds", "checkpointRef", "summary", "transport", "lastOperationId",
		"offeredAt", "resolution", "createdAt", "updatedAt",
	}
	required := []string{
		"schemaVersion", "handoffId", "taskRef", "taskRevision", "ownershipEpochAtOffer", "state", "revision",
		"fromActorId", "toActorId", "claimIds", "checkpointRef", "summary", "transport", "offeredAt", "createdAt", "updatedAt",
	}
	object, ok := mancodeCoordinationObject(value, required, allowed)
	if !ok {
		return normalizedItem{}, errors.New("Mancode handoff does not match the supported v1 contract")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	handoffID := mancodeString(object["handoffId"])
	filenameID := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	taskRef, taskOK := mancodeTaskRefFromValue(object["taskRef"])
	taskRevision, taskRevisionOK := mancodePositiveInteger(object["taskRevision"])
	epoch, epochOK := mancodeNonNegativeInteger(object["ownershipEpochAtOffer"])
	revision, revisionOK := mancodePositiveInteger(object["revision"])
	state := mancodeString(object["state"])
	fromActor := mancodeString(object["fromActorId"])
	toActor := mancodeString(object["toActorId"])
	offeredAtText, offeredAtPresent := object["offeredAt"].(string)
	_, offeredOK := mancodeCoordinationTime(object["offeredAt"])
	if object["offeredAt"] == nil && state == "draft" {
		offeredOK = true
	}
	createdAt, createdOK := mancodeCoordinationTime(object["createdAt"])
	updatedAt, updatedOK := mancodeCoordinationTime(object["updatedAt"])
	claimIDs, claimsOK := object["claimIds"].([]any)
	if len(claimIDs) > 1024 {
		claimsOK = false
	}
	for _, claimID := range claimIDs {
		text, isString := claimID.(string)
		if !isString || !mancodeOperationIDPattern.MatchString(text) {
			claimsOK = false
		}
	}
	checkpoint, checkpointOK := mancodeCoordinationObject(object["checkpointRef"], []string{"taskRef", "kind", "artifactId"}, []string{"taskRef", "kind", "artifactId"})
	checkpointTask, checkpointTaskOK := mancodeTaskRefFromValue(checkpoint["taskRef"])
	checkpointKind, checkpointKindOK := checkpoint["kind"].(string)
	checkpointArtifactID, checkpointArtifactOK := checkpoint["artifactId"].(string)
	summary, summaryOK := mancodeCoordinationObject(object["summary"], []string{
		"completed", "inProgress", "notStarted", "changedFiles", "verification", "blockers", "risks", "nextAction",
	}, []string{"completed", "inProgress", "notStarted", "changedFiles", "verification", "blockers", "risks", "nextAction"})
	completed, completedOK := mancodeCoordinationStringListCount(summary["completed"], 4096, 4096)
	inProgress, inProgressOK := mancodeCoordinationStringListCount(summary["inProgress"], 4096, 4096)
	notStarted, notStartedOK := mancodeCoordinationStringListCount(summary["notStarted"], 4096, 4096)
	changedFiles, changedFilesOK := mancodeCoordinationStringListCount(summary["changedFiles"], 4096, 4096)
	verification, verificationOK := mancodeCoordinationTextPresenceAndCount(summary["verification"], 4096, 4096)
	blockers, blockersOK := mancodeCoordinationStringListCount(summary["blockers"], 4096, 4096)
	risks, risksOK := mancodeCoordinationStringListCount(summary["risks"], 4096, 4096)
	nextAction, nextActionOK := summary["nextAction"].(string)
	if nextActionOK {
		nextActionOK = mancodeBoundedString(nextAction, 0, 4096)
	}
	transport, transportOK := mancodeCoordinationObject(object["transport"], []string{
		"mode", "state", "transportRevision", "publishedAt", "fetchedAt", "taskBundleDigest", "codeRef", "codeReachable", "receipt",
	}, []string{"mode", "state", "transportRevision", "publishedAt", "fetchedAt", "taskBundleDigest", "codeRef", "codeReachable", "receipt"})
	transportMode, transportModeOK := mancodeCoordinationMode(transport["mode"])
	transportState := mancodeString(transport["state"])
	transportRevision, transportRevisionOK := mancodeNonNegativeInteger(transport["transportRevision"])
	_, _ = transportRevision, transportRevisionOK
	codeReachable, codeReachableOK := transport["codeReachable"].(bool)
	_ = codeReachable
	if transport["receipt"] != nil {
		if _, ok := transport["receipt"].(map[string]any); !ok {
			transportOK = false
		}
	}
	if transport["taskBundleDigest"] != nil {
		text, ok := transport["taskBundleDigest"].(string)
		if !ok || !mancodeBoundedString(text, 1, 256) {
			transportOK = false
		}
	}
	if !mancodeCoordinationOptionalTime(transport["publishedAt"]) || !mancodeCoordinationOptionalTime(transport["fetchedAt"]) ||
		!mancodeCoordinationCodeReference(transport["codeRef"], []string{"branch", "head"}) {
		transportOK = false
	}
	if !mancodeBoundedString(transportState, 1, 64) {
		transportOK = false
	}
	if _, ok := object["resolution"]; ok && object["resolution"] != nil {
		if _, isObject := object["resolution"].(map[string]any); !isObject {
			return normalizedItem{}, errors.New("Mancode handoff resolution metadata is invalid")
		}
	}
	if !versionOK || version != 1 || !mancodeOperationIDPattern.MatchString(handoffID) || handoffID != filenameID || !taskOK ||
		!taskRevisionOK || !epochOK || !revisionOK || !mancodeHasString([]string{"draft", "offered", "accepted", "rejected", "cancelled"}, state) ||
		!mancodeOperationIDPattern.MatchString(fromActor) || !mancodeOperationIDPattern.MatchString(toActor) || !claimsOK ||
		!checkpointOK || !checkpointTaskOK || checkpointTask["namespace"] != taskRef["namespace"] || checkpointTask["taskId"] != taskRef["taskId"] ||
		!checkpointKindOK || !mancodeBoundedString(checkpointKind, 1, 64) || !checkpointArtifactOK || !mancodeOperationIDPattern.MatchString(checkpointArtifactID) ||
		!summaryOK || !completedOK || !inProgressOK || !notStartedOK || !changedFilesOK || !verificationOK || !blockersOK || !risksOK || !nextActionOK ||
		!transportOK || !transportModeOK || !codeReachableOK || !transportRevisionOK ||
		!offeredAtPresent && !(state == "draft" && object["offeredAt"] == nil) || !offeredOK ||
		(state != "draft" && object["offeredAt"] == nil) || !createdOK || !updatedOK || !mancodeCoordinationOptionalULID(object, "lastOperationId") {
		return normalizedItem{}, errors.New("Mancode handoff contains an invalid identity, transition, summary, or transport contract")
	}
	var offeredAtPointer *string
	if object["offeredAt"] != nil {
		offeredAtPointer = &offeredAtText
	}
	payload, err := json.Marshal(mancodeHandoffSummary{
		SchemaVersion: mancodeHandoffSummarySchema, Authority: "local_pending_review", HumanReviewRequired: true,
		ExecutionAuthorized: false, SourceIDSHA256: mancodeCoordinationIdentityHash("handoff", handoffID),
		TaskReferenceSHA256: mancodeCoordinationTaskHash(taskRef),
		SourceState:         state, Revision: revision, TaskRevision: taskRevision, OwnershipEpochAtOffer: epoch,
		ClaimReferenceCount: len(claimIDs), CheckpointPresent: true, CompletedCount: completed,
		InProgressCount: inProgress, NotStartedCount: notStarted, ChangedFileCount: changedFiles,
		VerificationItemCount: verification, BlockerCount: blockers, RiskCount: risks,
		NextActionPresent: strings.TrimSpace(nextAction) != "", TransportMode: transportMode,
		TransportReceiptPresent: transport["receipt"] != nil, CodeReferencePresent: transport["codeRef"] != nil,
		ResolutionPresent: object["resolution"] != nil, OfferedAt: offeredAtPointer, CreatedAt: createdAt, UpdatedAt: updatedAt,
		RawActorIdentitiesOmitted: true, RawSummaryTextOmitted: true, SourceAuthorityOmitted: true,
	})
	if err != nil {
		return normalizedItem{}, fmt.Errorf("encode Mancode handoff summary: %w", err)
	}
	return normalizedItem{Type: "mancode_team_handoff", Payload: payload}, nil
}

func normalizeMancodeTaskHeadSummary(sourceRoot, relative string, value any) (normalizedItem, error) {
	allowed := []string{"schemaVersion", "workspaceId", "taskRef", "fenceRevision", "taskRevision", "aggregateDigest", "ownershipEpoch", "codeRef", "checkoutId", "remoteRevision", "lastOperationId", "updatedAt"}
	required := []string{"schemaVersion", "workspaceId", "taskRef", "fenceRevision", "taskRevision", "aggregateDigest", "ownershipEpoch", "updatedAt"}
	object, ok := mancodeCoordinationObject(value, required, allowed)
	if !ok {
		return normalizedItem{}, errors.New("Mancode task-head fence does not match the supported v1 contract")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	workspaceID, workspaceOK := object["workspaceId"].(string)
	taskRef, taskOK := mancodeTaskRefFromValue(object["taskRef"])
	filenameID := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	fenceRevision, fenceOK := mancodePositiveInteger(object["fenceRevision"])
	taskRevision, taskRevisionOK := mancodePositiveInteger(object["taskRevision"])
	epoch, epochOK := mancodeNonNegativeInteger(object["ownershipEpoch"])
	aggregateDigest, digestOK := object["aggregateDigest"].(string)
	updatedAt, updatedOK := mancodeCoordinationTime(object["updatedAt"])
	if _, exists := object["checkoutId"]; exists && object["checkoutId"] != nil {
		checkoutID, ok := object["checkoutId"].(string)
		if !ok || !mancodeBoundedString(checkoutID, 1, 256) {
			return normalizedItem{}, errors.New("Mancode task-head checkout binding is invalid")
		}
	}
	if _, exists := object["remoteRevision"]; exists && object["remoteRevision"] != nil {
		remoteRevision, ok := object["remoteRevision"].(string)
		if !ok || !mancodeBoundedString(remoteRevision, 1, 256) {
			return normalizedItem{}, errors.New("Mancode task-head remote revision is invalid")
		}
	}
	if !versionOK || version != 1 || !workspaceOK || !mancodeOperationIDPattern.MatchString(workspaceID) ||
		!mancodeCoordinationWorkspaceBinding(sourceRoot, relative, workspaceID) || !taskOK ||
		mancodeString(taskRef["taskId"]) != filenameID || !fenceOK || !taskRevisionOK || !epochOK || !digestOK ||
		!mancodeBoundedString(aggregateDigest, 1, 256) || !updatedOK ||
		!mancodeCoordinationOptionalULID(object, "lastOperationId") ||
		!mancodeCoordinationCodeReference(object["codeRef"], []string{"head"}) {
		return normalizedItem{}, errors.New("Mancode task-head fence contains an invalid identity, revision, or digest")
	}
	payload, err := json.Marshal(mancodeTaskHeadSummary{
		SchemaVersion: mancodeHeadFenceSummarySchema, Authority: "local_pending_review", HumanReviewRequired: true,
		ExecutionAuthorized: false, WorkspaceIDSHA256: mancodeCoordinationIdentityHash("workspace", workspaceID),
		TaskReferenceSHA256: mancodeCoordinationTaskHash(taskRef), FenceRevision: fenceRevision,
		TaskRevision: taskRevision, OwnershipEpoch: epoch, AggregateDigestPresent: aggregateDigest != "",
		CodeReferencePresent: object["codeRef"] != nil, CheckoutBindingPresent: object["checkoutId"] != nil,
		RemoteRevisionPresent: object["remoteRevision"] != nil, UpdatedAt: updatedAt,
		RawTaskIdentityOmitted: true, SourceAuthorityOmitted: true,
	})
	if err != nil {
		return normalizedItem{}, fmt.Errorf("encode Mancode task-head summary: %w", err)
	}
	return normalizedItem{Type: "mancode_team_head_fence", Payload: payload}, nil
}

func mancodeCoordinationWorkspaceBinding(sourceRoot, relative, workspaceID string) bool {
	rootParts := strings.Split(strings.Trim(filepath.ToSlash(filepath.Clean(sourceRoot)), "/"), "/")
	for index := 0; index+2 < len(rootParts); index++ {
		if strings.EqualFold(rootParts[index], "mancode") && strings.EqualFold(rootParts[index+1], "workspaces") {
			if index+2 == len(rootParts)-1 && mancodeOperationIDPattern.MatchString(rootParts[index+2]) {
				return strings.EqualFold(rootParts[index+2], workspaceID)
			}
		}
	}
	pathParts := strings.Split(filepath.ToSlash(relative), "/")
	for index := 0; index+2 < len(pathParts); index++ {
		if strings.EqualFold(pathParts[index], "mancode") && strings.EqualFold(pathParts[index+1], "workspaces") &&
			mancodeOperationIDPattern.MatchString(pathParts[index+2]) {
			return strings.EqualFold(pathParts[index+2], workspaceID)
		}
	}
	return true
}

func normalizeMancodeOperationAudit(relative string, value any) (normalizedItem, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("Mancode operation journal must be an object")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	operationID := mancodeString(object["operationId"])
	operationType := mancodeString(object["type"])
	state := mancodeString(object["state"])
	startedAt, startedOK := mancodeCoordinationTime(object["startedAt"])
	updatedAt, updatedOK := mancodeCoordinationTime(object["updatedAt"])
	filenameID := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	if !versionOK || version != 1 || !mancodeOperationIDPattern.MatchString(operationID) || operationID != filenameID ||
		!validMancodeOperationType(operationType) || !startedOK || !updatedOK {
		return normalizedItem{}, errors.New("Mancode operation journal identity, type, or timestamps are invalid")
	}
	switch state {
	case "prepared", "applying", "committed", "repair_required", "aborted":
	default:
		return normalizedItem{}, errors.New("Mancode operation journal state is invalid")
	}
	started, _ := time.Parse(time.RFC3339Nano, startedAt)
	updated, _ := time.Parse(time.RFC3339Nano, updatedAt)
	if updated.Before(started) {
		return normalizedItem{}, errors.New("Mancode operation journal timestamps are out of order")
	}
	payload, err := json.Marshal(mancodeOperationAudit{
		SchemaVersion: mancodeOperationAuditSchema, Authority: "local_pending_review", HumanReviewRequired: true,
		ExecutionAuthorized: false, OperationIDSHA256: mancodeCoordinationIdentityHash("operation", operationID),
		OperationType: operationType, SourceState: state, StartedAt: startedAt, UpdatedAt: updatedAt,
		RequiresReconciliation:   state == "prepared" || state == "applying" || state == "repair_required",
		RawSourceIdentityOmitted: true, SourceAuthorityOmitted: true,
	})
	if err != nil {
		return normalizedItem{}, fmt.Errorf("encode Mancode operation audit: %w", err)
	}
	return normalizedItem{Type: "mancode_operation_audit", Payload: payload}, nil
}
