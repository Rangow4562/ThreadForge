package migrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const mancodeWorkflowAggregateSchema = "threadforge.mancode-workflow-aggregate.v1"

var mancodeWorkflowMetadataKeys = []string{
	"schemaVersion", "taskRef", "displaySlug", "task", "workflowMode", "visibility", "coordination", "status",
	"currentStep", "skippedSteps", "blockingReason", "outcome", "revision", "transitionState", "lastOperationId",
	"ownerActorId", "ownershipEpoch", "participants", "createdBy", "base", "implementationScope", "governance",
	"soloExecution", "latestCheckpointRef", "parent", "successorTaskRef", "legacyCompatibility", "startedAt", "updatedAt",
}
var mancodeWorkflowRequirementsKeys = []string{
	"schemaVersion", "canonicalizationVersion", "taskRef", "revision", "status", "goal", "functionalScope",
	"technicalDecisions", "defaults", "coverage", "requirements", "acceptanceCriteria", "blockingUnknowns",
	"legacySource", "contentDigest", "lastOperationId", "updatedAt",
}
var mancodeWorkflowReviewKeys = []string{
	"schemaVersion", "canonicalizationVersion", "taskRef", "revision", "status", "depth", "requirementsDigest",
	"planVersion", "requiredDomains", "domains", "blockers", "remediationRound", "skip", "legacySource",
	"contentDigest", "lastOperationId", "updatedAt", "delivery",
}
var mancodeWorkflowVerificationKeys = []string{
	"schemaVersion", "canonicalizationVersion", "taskRef", "revision", "status", "requirementsDigest",
	"planVersion", "remediationRound", "checks", "legacySource", "contentDigest", "lastOperationId", "updatedAt", "execution",
}
var mancodeWorkflowCheckpointKeys = []string{
	"schemaVersion", "checkpointId", "operationId", "taskRef", "taskRevision", "ownershipEpochAtOffer", "kind",
	"git", "summary", "governance", "nextAction", "createdBy", "createdAt",
}
var mancodeWorkflowMetadataDigestFields = []string{
	"taskRef", "displaySlug", "task", "workflowMode", "visibility", "coordination", "status", "currentStep",
	"skippedSteps", "blockingReason", "outcome", "ownerActorId", "ownershipEpoch", "participants", "createdBy",
	"base", "implementationScope", "governance", "soloExecution", "parent", "successorTaskRef", "legacyCompatibility",
}
var mancodeWorkflowRequirementsDigestFields = []string{
	"schemaVersion", "canonicalizationVersion", "taskRef", "status", "goal", "functionalScope", "technicalDecisions",
	"defaults", "coverage", "requirements", "acceptanceCriteria", "blockingUnknowns", "legacySource",
}
var mancodeWorkflowReviewDigestFields = []string{
	"schemaVersion", "canonicalizationVersion", "taskRef", "status", "depth", "requirementsDigest", "planVersion",
	"requiredDomains", "domains", "blockers", "remediationRound", "skip", "legacySource",
}
var mancodeWorkflowVerificationDigestFields = []string{
	"schemaVersion", "canonicalizationVersion", "taskRef", "status", "requirementsDigest", "planVersion",
	"remediationRound", "checks", "legacySource",
}

type mancodeWorkflowLocation struct {
	directory string
	namespace string
	taskID    string
}

func decodeMancodeSourceItems(relative string, data []byte) ([]any, error) {
	switch {
	case strings.EqualFold(filepath.Ext(relative), ".json"):
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("invalid Mancode JSON file")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, errors.New("invalid Mancode JSON file")
		}
		if values, ok := value.([]any); ok {
			return values, nil
		}
		return []any{value}, nil
	case isMancodeWorkflowDocumentExtension(relative):
		if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
			return nil, errors.New("invalid UTF-8 or binary Mancode workflow document")
		}
		return []any{map[string]any{"content": string(data)}}, nil
	default:
		return nil, fmt.Errorf("unsupported Mancode file extension %q", filepath.Ext(relative))
	}
}

func appendMancodeWorkflowAggregates(sourceRoot string, files *[]scannedFile) (int, error) {
	byPath := make(map[string]scannedFile, len(*files))
	var metadataIndexes []int
	for i := range *files {
		file := (*files)[i]
		byPath[file.entry.Path] = file
		if _, ok := mancodeWorkflowMetadataLocation(file.entry.Path, sourceRoot); ok {
			metadataIndexes = append(metadataIndexes, i)
		}
	}
	added := 0
	for _, index := range metadataIndexes {
		file := &(*files)[index]
		metadata, ok := mancodeWorkflowRecordObject(*file, "workflow_metadata")
		if !ok || mancodeInteger(metadata["schemaVersion"]) != 3 {
			continue
		}
		location, _ := mancodeWorkflowMetadataLocation(file.entry.Path, sourceRoot)
		payload, err := reconstructMancodeWorkflowAggregate(location, *file, metadata, byPath)
		if err != nil {
			return added, err
		}
		file.entry.RecordCount++
		file.entry.ImportedCount++
		file.records = append(file.records, Record{
			Key: fmt.Sprintf("%s#%06d", file.entry.Path, file.entry.RecordCount),
			Type: "mancode_workflow_aggregate", SourcePath: file.entry.Path,
			ContentSHA256: digest(payload), Scope: "local_pending_review", Payload: payload,
		})
		added++
	}
	return added, nil
}

func mancodeWorkflowAggregateRecords(file scannedFile) []Record {
	var records []Record
	for _, record := range file.records {
		if record.Type == "mancode_workflow_aggregate" {
			records = append(records, record)
		}
	}
	return records
}

func mancodeWorkflowMetadataLocation(relative, sourceRoot string) (mancodeWorkflowLocation, bool) {
	standard := strings.Trim(mancodePath(relative, sourceRoot), "/")
	parts := strings.Split(standard, "/")
	if len(parts) != 5 || parts[0] != ".mancode" || parts[2] != "workflows" || parts[4] != "metadata.json" ||
		(parts[1] != "local" && parts[1] != "shared") || !mancodeOperationIDPattern.MatchString(parts[3]) {
		return mancodeWorkflowLocation{}, false
	}
	taskID := filepath.Base(filepath.Dir(relative))
	return mancodeWorkflowLocation{directory: filepath.ToSlash(filepath.Dir(relative)), namespace: parts[1], taskID: taskID}, true
}

func mancodeWorkflowComponentLocation(relative, sourceRoot string) (mancodeWorkflowLocation, bool) {
	standard := strings.Trim(mancodePath(relative, sourceRoot), "/")
	parts := strings.Split(standard, "/")
	if len(parts) < 5 || parts[0] != ".mancode" || parts[2] != "workflows" ||
		(parts[1] != "local" && parts[1] != "shared") || !mancodeOperationIDPattern.MatchString(parts[3]) {
		return mancodeWorkflowLocation{}, false
	}
	rest := parts[4:]
	valid := len(rest) == 1 && (rest[0] == "metadata.json" || rest[0] == "requirements.json" ||
		rest[0] == "review-ledger.json" || rest[0] == "verification-ledger.json" || rest[0] == "plan.md")
	valid = valid || (len(rest) == 2 && rest[0] == "checkpoints" && strings.EqualFold(filepath.Ext(rest[1]), ".json"))
	if !valid {
		return mancodeWorkflowLocation{}, false
	}
	taskDir := filepath.Dir(relative)
	if len(rest) == 2 {
		taskDir = filepath.Dir(taskDir)
	}
	return mancodeWorkflowLocation{directory: filepath.ToSlash(taskDir), namespace: parts[1], taskID: filepath.Base(taskDir)}, true
}

func reconstructMancodeWorkflowAggregate(location mancodeWorkflowLocation, metadataFile scannedFile, metadata map[string]any, files map[string]scannedFile) (json.RawMessage, error) {
	result := map[string]any{
		"schema_version": mancodeWorkflowAggregateSchema,
		"validation_profile": "mancode-v3-aggregate-integrity.v1",
		"status": "complete",
		"issues": []any{},
		"task_ref": map[string]any{"namespace": location.namespace, "task_id": location.taskID},
		"task_revision": nil, "ownership_epoch": nil,
		"workflow_status": nil, "workflow_mode": nil,
		"requirements_status": nil, "requirements_revision": nil, "requirements_digest": nil,
		"review_status": nil, "review_revision": nil, "review_digest": nil,
		"verification_status": nil, "verification_schema_version": nil, "verification_revision": nil, "verification_digest": nil,
		"metadata_digest": nil, "plan_version": nil, "plan_digest": nil, "latest_checkpoint_id": nil,
		"latest_checkpoint_digest": nil, "parent_snapshot_digest": nil, "aggregate_digest": nil,
		"sources": []any{}, "authority": "local_pending_review", "activation_allowed": false,
	}
	issues := map[string]bool{}
	conflict := false
	addIssue := func(code string, isConflict bool) { issues[code] = true; conflict = conflict || isConflict }
	sourceRefs := []any{}
	addSource := func(role, sourcePath string, file scannedFile) {
		sourceRefs = append(sourceRefs, map[string]any{"role": role, "path": sourcePath, "source_sha256": file.entry.SourceSHA256})
	}
	addSource("metadata", metadataFile.entry.Path, metadataFile)
	componentPaths := map[string]string{
		"requirements": filepath.ToSlash(filepath.Join(location.directory, "requirements.json")),
		"review": filepath.ToSlash(filepath.Join(location.directory, "review-ledger.json")),
		"verification": filepath.ToSlash(filepath.Join(location.directory, "verification-ledger.json")),
	}
	objects := map[string]map[string]any{}
	recordTypes := map[string]string{"requirements": "workflow_requirements", "review": "workflow_review", "verification": "workflow_verification"}
	for _, role := range []string{"requirements", "review", "verification"} {
		sourcePath := componentPaths[role]
		file, exists := files[sourcePath]
		if !exists {
			addIssue("missing_"+role, false)
			continue
		}
		addSource(role, sourcePath, file)
		object, ok := mancodeWorkflowRecordObject(file, recordTypes[role])
		if !ok {
			addIssue("invalid_"+role, false)
			continue
		}
		objects[role] = object
	}
	if !mancodeHasOnlyKeys(metadata, mancodeWorkflowMetadataKeys) || !mancodeHasAllKeys(metadata, mancodeWorkflowMetadataKeys) ||
		mancodeInteger(metadata["schemaVersion"]) != 3 || !mancodeWorkflowMetadataShape(metadata, location) {
		addIssue("invalid_metadata", false)
	}
	taskRef, taskRefOK := mancodeTaskRefFromValue(metadata["taskRef"])
	if !taskRefOK {
		addIssue("invalid_metadata_task_ref", false)
		taskRef = mancodeWorkflowTaskRef(location.namespace, location.taskID)
	} else if taskRef["namespace"] != location.namespace || taskRef["taskId"] != location.taskID {
		addIssue("metadata_path_task_mismatch", true)
	}
	revision, revisionOK := mancodePositiveInteger(metadata["revision"])
	epoch, epochOK := mancodeNonNegativeInteger(metadata["ownershipEpoch"])
	if !revisionOK { addIssue("invalid_metadata_revision", false) } else { result["task_revision"] = revision }
	if !epochOK { addIssue("invalid_metadata_ownership_epoch", false) } else { result["ownership_epoch"] = epoch }
	result["workflow_status"], _ = metadata["status"].(string)
	result["workflow_mode"], _ = metadata["workflowMode"].(string)
	governance, governanceOK := metadata["governance"].(map[string]any)
	if !governanceOK { addIssue("invalid_metadata_governance", false) }
	metadataDigest, metadataDigestErr := mancodeWorkflowMetadataDigest(metadata)
	if metadataDigestErr != nil { addIssue("invalid_metadata_digest_input", false) }

	for role, object := range objects {
		allowed := mancodeWorkflowRequirementsKeys
		if role == "review" { allowed = mancodeWorkflowReviewKeys }
		if role == "verification" { allowed = mancodeWorkflowVerificationKeys }
		if !mancodeHasOnlyKeys(object, allowed) || !mancodeHasAllKeys(object, requiredWorkflowLedgerKeys(role)) ||
			mancodeString(object["canonicalizationVersion"]) != "mancode-jcs-v1" || !mancodeWorkflowLedgerShape(role, object) {
			addIssue("invalid_"+role+"_schema", false)
		}
		version := mancodeInteger(object["schemaVersion"])
		if (role != "verification" && version != 1) || (role == "verification" && version != 1 && version != 2) {
			addIssue("unsupported_"+role+"_schema", false)
		}
		componentTaskRef, ok := mancodeTaskRefFromValue(object["taskRef"])
		if !ok { addIssue("invalid_"+role+"_task_ref", false) } else if !sameMancodeTaskRef(componentTaskRef, taskRef) {
			addIssue(role+"_task_ref_mismatch", true)
		}
		storedDigest := mancodeString(object["contentDigest"])
		if !validMancodeDigest(storedDigest) {
			addIssue("invalid_"+role+"_digest", false)
		} else {
			computed, err := mancodeWorkflowLedgerDigest(role, object)
			if err != nil || computed != storedDigest { addIssue(role+"_content_digest_mismatch", true) }
		}
	}
	requirements, requirementsOK := objects["requirements"]
	review, reviewOK := objects["review"]
	verification, verificationOK := objects["verification"]
	if reviewOK && requirementsOK {
		checkReviewDeliveryContext(review, requirements, addIssue)
	}
	if requirementsOK {
		result["requirements_status"] = requirements["status"]
		result["requirements_revision"], _ = mancodePositiveInteger(requirements["revision"])
		result["requirements_digest"] = requirements["contentDigest"]
	}
	if reviewOK {
		result["review_status"] = review["status"]
		result["review_revision"], _ = mancodePositiveInteger(review["revision"])
		result["review_digest"] = review["contentDigest"]
	}
	if verificationOK {
		result["verification_status"] = verification["status"]
		result["verification_schema_version"], _ = mancodePositiveInteger(verification["schemaVersion"])
		result["verification_revision"], _ = mancodePositiveInteger(verification["revision"])
		result["verification_digest"] = verification["contentDigest"]
	}
	if governanceOK {
		planVersion, planOK := mancodePositiveInteger(governance["planVersion"])
		if !planOK { addIssue("invalid_plan_version", false) } else { result["plan_version"] = planVersion }
		checkWorkflowCache(governance, "requirementsDigest", mancodeString(result["requirements_digest"]), "requirements_cache_mismatch", addIssue)
		checkWorkflowCache(governance, "reviewLedgerDigest", mancodeString(result["review_digest"]), "review_cache_mismatch", addIssue)
		checkWorkflowCache(governance, "verificationLedgerDigest", mancodeString(result["verification_digest"]), "verification_cache_mismatch", addIssue)
		checkWorkflowCache(governance, "reviewStatus", mancodeString(result["review_status"]), "review_status_cache_mismatch", addIssue)
		checkWorkflowCache(governance, "verificationStatus", mancodeString(result["verification_status"]), "verification_status_cache_mismatch", addIssue)
		policyVersions, _ := governance["policyVersions"].(map[string]any)
		verificationPolicy, policyOK := mancodeNullableInteger(policyVersions["verification"])
		verificationVersion := mancodeInteger(verification["schemaVersion"])
		if verificationOK && (!policyOK || ((verificationPolicy == 2) != (verificationVersion == 2))) {
			addIssue("verification_policy_schema_mismatch", true)
		}
		checkRequirementsStatus(governance, requirements, requirementsOK, addIssue)
		checkReviewContext(review, reviewOK, mancodeString(result["requirements_digest"]), planVersion, addIssue)
		checkVerificationContext(verification, verificationOK, requirements, requirementsOK, planVersion, review, reviewOK, addIssue)
	}

	planPath := filepath.ToSlash(filepath.Join(location.directory, "plan.md"))
	if planFile, exists := files[planPath]; exists {
		addSource("plan", planPath, planFile)
		if plan, ok := mancodeWorkflowRecordObject(planFile, "workflow_plan"); ok {
			content, contentOK := plan["content"].(string)
			if !contentOK {
				addIssue("invalid_plan", false)
			} else {
				artifactRef := map[string]any{"taskRef": map[string]any{"namespace": taskRef["namespace"], "taskId": taskRef["taskId"]}, "kind": "plan"}
				planDigest, err := mancodeCanonicalDigest(map[string]any{"artifactRef": artifactRef, "content": content})
				if err != nil { addIssue("invalid_plan_digest_input", false) } else { result["plan_digest"] = planDigest }
			}
		} else { addIssue("invalid_plan", false) }
	}
	if ref, present := metadata["latestCheckpointRef"]; present && ref != nil {
		artifactRef, ok := ref.(map[string]any)
		if !ok {
			addIssue("invalid_latest_checkpoint_ref", false)
		} else {
			checkpointID := mancodeString(artifactRef["artifactId"])
			refTask, taskOK := mancodeTaskRefFromValue(artifactRef["taskRef"])
			if mancodeString(artifactRef["kind"]) != "checkpoint" || !taskOK || !sameMancodeTaskRef(refTask, taskRef) || !mancodeOperationIDPattern.MatchString(checkpointID) {
				addIssue("invalid_latest_checkpoint_ref", false)
			} else {
				checkpointPath := filepath.ToSlash(filepath.Join(location.directory, "checkpoints", checkpointID+".json"))
				checkpointFile, exists := files[checkpointPath]
				if !exists {
					addIssue("missing_latest_checkpoint", false)
				} else {
					checkpoint, valid := mancodeWorkflowRecordObject(checkpointFile, "workflow_checkpoint")
					if !valid {
						addIssue("invalid_latest_checkpoint", false)
					} else {
						addSource("latest_checkpoint", checkpointPath, checkpointFile)
						if !mancodeHasOnlyKeys(checkpoint, mancodeWorkflowCheckpointKeys) ||
							!mancodeHasAllKeys(checkpoint, mancodeWorkflowCheckpointKeys) || !mancodeWorkflowCheckpointShape(checkpoint) {
							addIssue("invalid_latest_checkpoint_schema", false)
						}
						checkpointTaskRef, checkpointTaskOK := mancodeTaskRefFromValue(checkpoint["taskRef"])
						if !checkpointTaskOK {
							addIssue("invalid_checkpoint_task_ref", false)
						} else if !sameMancodeTaskRef(checkpointTaskRef, taskRef) {
							addIssue("checkpoint_task_ref_mismatch", true)
						}
						if checkpointID != mancodeString(checkpoint["checkpointId"]) {
							addIssue("checkpoint_id_mismatch", true)
						}
						checkpointDigest, err := mancodeCanonicalDigest(checkpoint)
						if err != nil {
							addIssue("invalid_latest_checkpoint_digest", false)
						} else {
							result["latest_checkpoint_id"] = checkpointID
							result["latest_checkpoint_digest"] = checkpointDigest
						}
					}
				}
			}
		}
	}
	if parent := metadata["parent"]; parent != nil {
		parentObject, ok := cloneMancodeJSON(parent).(map[string]any)
		if !ok {
			addIssue("invalid_parent_snapshot_digest", false)
		} else {
			participants, participantsOK := mancodeSortedStringSet(parentObject["participants"])
			if !participantsOK {
				addIssue("invalid_parent_snapshot_digest", false)
			} else {
				parentObject["participants"] = participants
				parentDigest, err := mancodeCanonicalDigest(parentObject)
				if err != nil { addIssue("invalid_parent_snapshot_digest", false) } else { result["parent_snapshot_digest"] = parentDigest }
			}
		}
	}
	if parent, ok := result["parent_snapshot_digest"].(string); ok && !validMancodeDigest(parent) {
		addIssue("invalid_parent_snapshot_digest", false)
	}

	if governanceOK && requirementsOK && reviewOK && verificationOK && metadataDigestErr == nil && len(issues) == 0 {
		result["metadata_digest"] = metadataDigest
		manifest := map[string]any{
			"taskRef": metadata["taskRef"],
			"taskRevision": json.Number(strconv.FormatInt(revision, 10)),
			"ownershipEpoch": json.Number(strconv.FormatInt(epoch, 10)),
			"metadataDigest": metadataDigest,
			"requirementsDigest": result["requirements_digest"], "reviewDigest": result["review_digest"],
			"verificationDigest": result["verification_digest"], "planVersion": json.Number(strconv.FormatInt(mancodeInteger(result["plan_version"]), 10)),
			"planDigest": result["plan_digest"], "latestCheckpointId": result["latest_checkpoint_id"],
			"latestCheckpointDigest": result["latest_checkpoint_digest"], "parentSnapshotDigest": result["parent_snapshot_digest"],
		}
		aggregateDigest, err := mancodeCanonicalDigest(manifest)
		if err != nil { addIssue("invalid_aggregate_digest_input", false) } else { result["aggregate_digest"] = aggregateDigest }
	}
	issueList := make([]string, 0, len(issues))
	for issue := range issues { issueList = append(issueList, issue) }
	sort.Strings(issueList)
	result["issues"] = issueList
	result["sources"] = sourceRefs
	if len(issueList) > 0 {
		result["aggregate_digest"] = nil
		if conflict { result["status"] = "conflicting" } else { result["status"] = "incomplete" }
	}
	sort.Slice(sourceRefs, func(i, j int) bool {
		left, _ := sourceRefs[i].(map[string]any)
		right, _ := sourceRefs[j].(map[string]any)
		if left["role"] == right["role"] { return mancodeString(left["path"]) < mancodeString(right["path"]) }
		return mancodeString(left["role"]) < mancodeString(right["role"])
	})
	return json.Marshal(result)
}

func mancodeWorkflowTaskRef(namespace, taskID string) map[string]any {
	return map[string]any{"namespace": namespace, "taskId": taskID}
}

func sameMancodeTaskRef(left, right map[string]any) bool {
	return mancodeString(left["namespace"]) == mancodeString(right["namespace"]) && mancodeString(left["taskId"]) == mancodeString(right["taskId"])
}

func mancodeWorkflowRecordObject(file scannedFile, recordType string) (map[string]any, bool) {
	if len(file.records) != file.entry.ImportedCount { return nil, false }
	var payload json.RawMessage
	for _, record := range file.records {
		if record.Type != recordType { continue }
		if payload != nil { return nil, false }
		payload = record.Payload
	}
	if payload == nil { return nil, false }
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil { return nil, false }
	object, ok := value.(map[string]any)
	return object, ok
}

func mancodeHasOnlyKeys(object map[string]any, allowed []string) bool {
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed { set[key] = true }
	for key := range object { if !set[key] { return false } }
	return true
}

func mancodeHasAllKeys(object map[string]any, required []string) bool {
	for _, key := range required {
		if _, exists := object[key]; !exists { return false }
	}
	return true
}

func requiredWorkflowLedgerKeys(role string) []string {
	switch role {
	case "requirements":
		return mancodeWorkflowRequirementsKeys
	case "review":
		keys := append([]string(nil), mancodeWorkflowReviewKeys...)
		return keys[:len(keys)-1] // delivery is the only optional review field.
	case "verification":
		keys := append([]string(nil), mancodeWorkflowVerificationKeys...)
		return keys[:len(keys)-1] // execution is present only for schema version 2.
	default:
		return nil
	}
}

func mancodeWorkflowMetadataShape(metadata map[string]any, location mancodeWorkflowLocation) bool {
	mode, visibility := mancodeString(metadata["workflowMode"]), mancodeString(metadata["visibility"])
	coordination, status := mancodeString(metadata["coordination"]), mancodeString(metadata["status"])
	if (mode != "man" && mode != "manba" && mode != "manteam") ||
		(visibility != "local" && visibility != "shared") || visibility != location.namespace ||
		(coordination != "single" && coordination != "team") ||
		!containsString([]string{"in_progress", "planned", "blocked", "completed", "abandoned", "superseded"}, status) ||
		!mancodeIsPositiveInteger(metadata["revision"]) ||
		!mancodeIsNonNegativeInteger(metadata["ownershipEpoch"]) ||
		!mancodeIsNonNegativeInteger(metadata["currentStep"]) ||
		!mancodeStringList(metadata["skippedSteps"]) ||
		!mancodeNullableULID(metadata["lastOperationId"]) || !mancodeNullableULID(metadata["ownerActorId"]) ||
		!mancodeULIDSet(metadata["participants"]) ||
		!mancodeRFC3339(metadata["startedAt"]) || !mancodeRFC3339(metadata["updatedAt"]) {
		return false
	}
	ref, refOK := mancodeTaskRefFromValue(metadata["taskRef"])
	if !refOK || ref["namespace"] != location.namespace || ref["taskId"] != location.taskID { return false }
	if !mancodeNonEmptyString(metadata["displaySlug"]) || !mancodeNonEmptyString(metadata["task"]) { return false }
	if mode == "man" && (coordination != "single" || metadata["parent"] != nil) { return false }
	if mode == "manteam" && (visibility != "shared" || coordination != "team" || metadata["parent"] != nil) { return false }
	if mode == "manba" && ((visibility == "local" && coordination != "single") || (visibility == "shared" && metadata["parent"] == nil)) { return false }

	outcome := metadata["outcome"]
	if outcome != nil && !containsString([]string{"fixed", "verified", "no_repro", "manual_test_required"}, mancodeString(outcome)) { return false }
	if mode != "manba" && outcome != nil { return false }
	if mode == "manba" && status == "completed" && outcome == nil { return false }
	if mode == "manba" && status != "completed" && outcome != nil { return false }
	blockingReason := metadata["blockingReason"]
	if (status == "blocked") != (blockingReason != nil) || (blockingReason != nil && !mancodeNonEmptyString(blockingReason)) { return false }
	transition := mancodeString(metadata["transitionState"])
	if !containsString([]string{"stable", "operation_pending", "pending_repair"}, transition) ||
		(transition != "stable" && metadata["lastOperationId"] == nil) { return false }
	if (status == "superseded") != (metadata["successorTaskRef"] != nil) { return false }
	if successor := metadata["successorTaskRef"]; successor != nil {
		next, ok := mancodeTaskRefFromValue(successor)
		if !ok || next["namespace"] != "shared" || ref["namespace"] != "local" || next["taskId"] == ref["taskId"] { return false }
	}
	createdBy, ok := metadata["createdBy"].(map[string]any)
	if !ok || !mancodeHasOnlyKeys(createdBy, []string{"actorId", "client", "source"}) ||
		!mancodeHasAllKeys(createdBy, []string{"actorId", "client", "source"}) ||
		!mancodeNullableULID(createdBy["actorId"]) || !mancodeNonEmptyString(createdBy["client"]) { return false }
	createdSource := mancodeString(createdBy["source"])
	if createdSource != "actor" && createdSource != "legacy_migration" { return false }
	if createdSource == "actor" && createdBy["actorId"] == nil { return false }
	if metadata["legacyCompatibility"] == nil && (metadata["ownerActorId"] == nil || createdSource != "actor") { return false }
	if metadata["legacyCompatibility"] != nil {
		legacy, ok := metadata["legacyCompatibility"].(map[string]any)
		if !ok || !mancodeHasOnlyKeys(legacy, []string{"legacyTaskId", "sourceMetadataDigest", "fieldMapVersion"}) ||
			!mancodeHasAllKeys(legacy, []string{"legacyTaskId", "sourceMetadataDigest", "fieldMapVersion"}) ||
			!mancodeNonEmptyString(legacy["legacyTaskId"]) || !validMancodeDigest(mancodeString(legacy["sourceMetadataDigest"])) ||
			mancodeInteger(legacy["fieldMapVersion"]) != 1 { return false }
	}
	if base := metadata["base"]; base != nil {
		value, ok := base.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(value, []string{"branch", "head", "upstream"}) ||
			!mancodeHasAllKeys(value, []string{"branch", "head", "upstream"}) ||
			!mancodeNonEmptyString(value["branch"]) || !mancodeNonEmptyString(value["head"]) ||
			(value["upstream"] != nil && !mancodeNonEmptyString(value["upstream"])) { return false }
	}
	scope, ok := metadata["implementationScope"].(map[string]any)
	if !ok || !mancodeHasOnlyKeys(scope, []string{"source", "include", "exclude", "modules", "digest"}) ||
		!mancodeHasAllKeys(scope, []string{"source", "include", "exclude", "modules", "digest"}) { return false }
	governance, ok := metadata["governance"].(map[string]any)
	if !ok || !mancodeHasOnlyKeys(governance, []string{
		"requirementsStatus", "requirementsDigest", "planVersion", "planDecision", "policyVersions",
		"reviewStatus", "reviewLedgerDigest", "verificationStatus", "verificationLedgerDigest",
	}) || !mancodeHasAllKeys(governance, []string{
		"requirementsStatus", "requirementsDigest", "planVersion", "planDecision", "policyVersions",
		"reviewStatus", "reviewLedgerDigest", "verificationStatus", "verificationLedgerDigest",
	}) { return false }
	if !containsString([]string{"ready", "needs_clarification"}, mancodeString(governance["requirementsStatus"])) ||
		!validMancodeDigest(mancodeString(governance["requirementsDigest"])) ||
		!mancodeIsPositiveInteger(governance["planVersion"]) ||
		(governance["planDecision"] != nil && !containsString([]string{"plan_only", "solo_handoff", "governed_execution"}, mancodeString(governance["planDecision"]))) ||
		!containsString([]string{"pending", "in_review", "passed", "blocked", "skipped", "stale"}, mancodeString(governance["reviewStatus"])) ||
		!validMancodeDigest(mancodeString(governance["reviewLedgerDigest"])) ||
		!containsString([]string{"pending", "passed", "failed", "stale", "manual_required", "blocked"}, mancodeString(governance["verificationStatus"])) ||
		!validMancodeDigest(mancodeString(governance["verificationLedgerDigest"])) { return false }
	policies, ok := governance["policyVersions"].(map[string]any)
	if !ok || !mancodeHasOnlyKeys(policies, []string{"planning", "review", "verification"}) ||
		!mancodeHasAllKeys(policies, []string{"planning", "review", "verification"}) { return false }
	for name, versions := range map[string][]int64{"planning": {1, 2, 3}, "review": {1, 2}, "verification": {1, 2}} {
		version, valid := mancodeNullableInteger(policies[name])
		if !valid || (policies[name] != nil && !containsInt64(versions, version)) { return false }
	}
	if mancodeInteger(policies["planning"]) == 3 && mode != "man" { return false }
	if solo := metadata["soloExecution"]; solo != nil {
		value, ok := solo.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(value, []string{"state", "planVersion", "assignedSessionId", "startedAt", "completedAt"}) ||
			!mancodeHasAllKeys(value, []string{"state", "planVersion", "assignedSessionId", "startedAt", "completedAt"}) ||
			!containsString([]string{"active", "completed"}, mancodeString(value["state"])) ||
			!mancodeIsPositiveInteger(value["planVersion"]) || !mancodeNullableULID(value["assignedSessionId"]) { return false }
	}
	if parent := metadata["parent"]; parent != nil {
		value, ok := parent.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(value, []string{"taskRef", "revisionAtCreate", "planVersionAtCreate", "requirementsDigestAtCreate", "implementationScopeDigestAtCreate", "visibility", "coordination", "participants"}) ||
			!mancodeHasAllKeys(value, []string{"taskRef", "revisionAtCreate", "planVersionAtCreate", "requirementsDigestAtCreate", "implementationScopeDigestAtCreate", "visibility", "coordination", "participants"}) { return false }
		parentRef, ok := mancodeTaskRefFromValue(value["taskRef"])
		if !ok || parentRef["namespace"] != visibility || value["visibility"] != visibility || value["coordination"] != coordination ||
			!mancodeIsPositiveInteger(value["revisionAtCreate"]) || !mancodeIsPositiveInteger(value["planVersionAtCreate"]) ||
			!validMancodeDigest(mancodeString(value["requirementsDigestAtCreate"])) ||
			!validMancodeDigest(mancodeString(value["implementationScopeDigestAtCreate"])) || !mancodeULIDSet(value["participants"]) { return false }
	}
	return true
}

func mancodeIsPositiveInteger(value any) bool {
	_, ok := mancodePositiveInteger(value)
	return ok
}

func mancodeIsNonNegativeInteger(value any) bool {
	_, ok := mancodeNonNegativeInteger(value)
	return ok
}

func mancodeWorkflowLedgerShape(role string, object map[string]any) bool {
	taskRef, taskRefOK := mancodeTaskRefFromValue(object["taskRef"])
	if !taskRefOK || mancodeString(object["canonicalizationVersion"]) != "mancode-jcs-v1" ||
		!mancodeIsPositiveInteger(object["revision"]) || !mancodeNullableULID(object["lastOperationId"]) ||
		!mancodeRFC3339(object["updatedAt"]) || !validMancodeDigest(mancodeString(object["contentDigest"])) {
		return false
	}
	switch role {
	case "requirements":
		if mancodeInteger(object["schemaVersion"]) != 1 ||
			!containsString([]string{"draft", "confirmed"}, mancodeString(object["status"])) ||
			!mancodeNonEmptyString(object["goal"]) || !mancodeValidLegacySource("requirements", object["legacySource"]) {
			return false
		}
		scope, ok := object["functionalScope"].(map[string]any)
		if !ok || !mancodeHasOnlyKeys(scope, []string{"inScope", "outOfScope"}) ||
			!mancodeHasAllKeys(scope, []string{"inScope", "outOfScope"}) ||
			!mancodeStringList(scope["inScope"]) || !mancodeStringList(scope["outOfScope"]) {
			return false
		}
		for _, field := range []string{"technicalDecisions", "defaults", "coverage", "requirements", "acceptanceCriteria", "blockingUnknowns"} {
			if _, ok := object[field].([]any); !ok { return false }
		}
		displayIDs := map[string]bool{}
		if _, valid := mancodeValidateRequirementItemGroup(object["technicalDecisions"], "technicalDecisions", displayIDs); !valid { return false }
		if _, valid := mancodeValidateRequirementItemGroup(object["defaults"], "defaults", displayIDs); !valid { return false }
		requirementIDs, valid := mancodeValidateRequirementItemGroup(object["requirements"], "requirements", displayIDs)
		if !valid { return false }
		if _, valid := mancodeValidateRequirementItemGroup(object["blockingUnknowns"], "blockingUnknowns", displayIDs); !valid { return false }
		dimensions := map[string]bool{}
		coverageIDs := map[string]bool{}
		coverage, _ := object["coverage"].([]any)
		for _, item := range coverage {
			entry, ok := item.(map[string]any)
			if !ok {
				return false
			}
			dimension := mancodeString(entry["dimension"])
			if !mancodeHasOnlyKeys(entry, []string{"coverageId", "dimension", "status", "rationale"}) ||
				!mancodeHasAllKeys(entry, []string{"coverageId", "dimension", "status", "rationale"}) ||
				!mancodeOperationIDPattern.MatchString(mancodeString(entry["coverageId"])) ||
				coverageIDs[mancodeString(entry["coverageId"])] ||
				!containsString([]string{"platform", "core_scope", "technical_stack", "data_and_persistence", "performance", "compatibility", "security"}, dimension) ||
				!containsString([]string{"confirmed", "defaulted", "not_applicable"}, mancodeString(entry["status"])) ||
				!mancodeNonEmptyString(entry["rationale"]) || dimensions[dimension] {
				return false
			}
			dimensions[dimension] = true
			coverageIDs[mancodeString(entry["coverageId"])] = true
		}
		criteria, _ := object["acceptanceCriteria"].([]any)
		for _, item := range criteria {
			entry, ok := item.(map[string]any)
			if !ok || !mancodeHasOnlyKeys(entry, []string{"displayId", "legacyId", "criterionId", "requirementIds", "statement", "required", "verificationRequirement", "verificationSurfaces"}) ||
				!mancodeHasAllKeys(entry, []string{"displayId", "legacyId", "criterionId", "requirementIds", "statement", "required", "verificationRequirement"}) ||
				!mancodeItemIdentity(entry) || !mancodeOperationIDPattern.MatchString(mancodeString(entry["criterionId"])) ||
				!mancodeNonEmptyString(entry["statement"]) || !containsString([]string{"automated", "manual", "hybrid"}, mancodeString(entry["verificationRequirement"])) ||
				displayIDs[mancodeString(entry["displayId"])] {
				return false
			}
			if _, ok := entry["required"].(bool); !ok { return false }
			displayIDs[mancodeString(entry["displayId"])] = true
			references, ok := mancodeSortedStringSet(entry["requirementIds"])
			if !ok { return false }
			if surfaces, exists := entry["verificationSurfaces"]; exists {
				value, ok := surfaces.(map[string]any)
				if !ok || !mancodeHasOnlyKeys(value, []string{"automated", "manual"}) { return false }
				automated, hasAutomated := value["automated"]
				manual, hasManual := value["manual"]
				switch mancodeString(entry["verificationRequirement"]) {
				case "automated":
					if !hasAutomated || hasManual || automated == nil { return false }
				case "manual":
					if hasAutomated || !hasManual || manual == nil { return false }
				case "hybrid":
					if !hasAutomated || !hasManual || automated == nil || manual == nil { return false }
				}
				if (hasAutomated && !mancodeNonEmptyString(automated)) || (hasManual && !mancodeNonEmptyString(manual)) { return false }
			}
			for _, ref := range references {
				id := mancodeString(ref)
				if !mancodeOperationIDPattern.MatchString(id) || !requirementIDs[id] { return false }
			}
		}
		if mancodeString(object["status"]) == "confirmed" && !mancodeRequirementsReady(object) { return false }
		return true
	case "review":
		if mancodeInteger(object["schemaVersion"]) != 1 ||
			!containsString([]string{"pending", "in_review", "passed", "blocked", "skipped", "stale"}, mancodeString(object["status"])) ||
			!containsString([]string{"targeted", "full"}, mancodeString(object["depth"])) ||
			!mancodeIsNonNegativeInteger(object["remediationRound"]) ||
			!mancodeValidLegacySource("review", object["legacySource"]) {
			return false
		}
		if object["legacySource"] == nil && (object["requirementsDigest"] == nil || object["planVersion"] == nil) { return false }
		if object["requirementsDigest"] != nil && !validMancodeDigest(mancodeString(object["requirementsDigest"])) { return false }
		if object["planVersion"] != nil && !mancodeIsPositiveInteger(object["planVersion"]) { return false }
		required, ok := object["requiredDomains"].([]any)
		domains, domainsOK := object["domains"].([]any)
		blockers, blockersOK := object["blockers"].([]any)
		if !ok || !domainsOK || !blockersOK { return false }
		expectedCount := 2
		if mancodeString(object["depth"]) == "targeted" { expectedCount = 1 }
		if object["skip"] != nil {
			if len(required) != 0 || len(domains) != 0 || len(blockers) != 0 || mancodeInteger(object["remediationRound"]) != 0 { return false }
			if !mancodeValidSkip(object["skip"]) { return false }
		} else {
			if len(required) != expectedCount || len(domains) != len(required) { return false }
			requiredSet, ok := mancodeSortedStringSet(required)
			if !ok { return false }
			requiredNames := map[string]bool{}
			for _, value := range requiredSet {
				name := mancodeString(value)
				if !containsString([]string{"quality", "security"}, name) { return false }
				requiredNames[name] = true
			}
			found := map[string]bool{}
			for _, item := range domains {
				entry, ok := item.(map[string]any)
				if !ok || !mancodeHasOnlyKeys(entry, []string{"domain", "status", "reportRef"}) ||
					!mancodeHasAllKeys(entry, []string{"domain", "status", "reportRef"}) { return false }
				name, state := mancodeString(entry["domain"]), mancodeString(entry["status"])
				if !requiredNames[name] || found[name] || !containsString([]string{"pending", "passed", "blocked", "manual_required", "skipped"}, state) { return false }
				if state == "pending" && entry["reportRef"] != nil { return false }
				if entry["reportRef"] != nil {
					ref, ok := entry["reportRef"].(map[string]any)
					if !ok || !mancodeHasOnlyKeys(ref, []string{"taskRef", "kind", "artifactId"}) ||
						!mancodeHasAllKeys(ref, []string{"taskRef", "kind"}) ||
						mancodeString(ref["kind"]) != "review_report" { return false }
					refTask, ok := mancodeTaskRefFromValue(ref["taskRef"])
					if !ok || !sameMancodeTaskRef(refTask, taskRef) { return false }
					if artifactID, exists := ref["artifactId"]; exists && !mancodeOperationIDPattern.MatchString(mancodeString(artifactID)) { return false }
				}
				found[name] = true
			}
			if len(found) != len(requiredNames) { return false }
			blockerIDs, blockerDisplays := map[string]bool{}, map[string]bool{}
			for _, item := range blockers {
				entry, ok := item.(map[string]any)
				if !ok || !mancodeItemIdentity(entry) ||
					!mancodeHasAllKeys(entry, []string{"displayId", "legacyId", "blockerId", "domain", "severity", "status", "summary", "waiver"}) ||
					!mancodeOperationIDPattern.MatchString(mancodeString(entry["blockerId"])) ||
					!requiredNames[mancodeString(entry["domain"])] ||
					!containsString([]string{"p0", "p1", "p2", "legacy_unknown"}, mancodeString(entry["severity"])) ||
					!containsString([]string{"open", "resolved", "waived"}, mancodeString(entry["status"])) {
					return false
				}
				blockerID, displayID := mancodeString(entry["blockerId"]), mancodeString(entry["displayId"])
				if blockerIDs[blockerID] || blockerDisplays[displayID] || !mancodeStringOrNull(entry["summary"]) { return false }
				blockerIDs[blockerID], blockerDisplays[displayID] = true, true
				if entry["status"] == "waived" {
					waiver, ok := entry["waiver"].(map[string]any)
					if !ok || mancodeString(entry["severity"]) == "p0" || mancodeString(entry["severity"]) == "legacy_unknown" ||
						!mancodeHasOnlyKeys(waiver, []string{"reason", "approvedByActorId", "approvedAt"}) ||
						!mancodeHasAllKeys(waiver, []string{"reason", "approvedByActorId", "approvedAt"}) ||
						!mancodeNonEmptyString(waiver["reason"]) ||
						!mancodeOperationIDPattern.MatchString(mancodeString(waiver["approvedByActorId"])) ||
						!mancodeRFC3339(waiver["approvedAt"]) { return false }
				} else if entry["waiver"] != nil { return false }
			}
		}
		if delivery, exists := object["delivery"]; exists {
			if !mancodeWorkflowReviewDeliveryShape(delivery) { return false }
		}
		return true
	case "verification":
		version := mancodeInteger(object["schemaVersion"])
		if (version != 1 && version != 2) ||
			!containsString([]string{"pending", "passed", "failed", "stale", "manual_required", "blocked"}, mancodeString(object["status"])) ||
			!validMancodeDigest(mancodeString(object["requirementsDigest"])) ||
			!mancodeIsPositiveInteger(object["planVersion"]) || !mancodeIsNonNegativeInteger(object["remediationRound"]) ||
			!mancodeValidLegacySource("verification", object["legacySource"]) {
			return false
		}
		if version == 2 {
			if !mancodeWorkflowExecutionStateShape(object["execution"]) { return false }
		} else if _, exists := object["execution"]; exists { return false }
		checks, ok := object["checks"].([]any)
		if !ok { return false }
		checkIDs, criterionIDs, displayIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, item := range checks {
			check, ok := item.(map[string]any)
			if !ok || !mancodeHasOnlyKeys(check, []string{"displayId", "legacyId", "checkId", "criterionId", "required", "verificationRequirement", "automated", "manual"}) ||
				!mancodeHasAllKeys(check, []string{"displayId", "legacyId", "checkId", "criterionId", "required", "verificationRequirement", "automated", "manual"}) ||
				!mancodeItemIdentity(check) || !mancodeOperationIDPattern.MatchString(mancodeString(check["checkId"])) ||
				!mancodeOperationIDPattern.MatchString(mancodeString(check["criterionId"])) ||
				!containsString([]string{"automated", "manual", "hybrid"}, mancodeString(check["verificationRequirement"])) {
				return false
			}
			if _, ok := check["required"].(bool); !ok { return false }
			checkID, criterionID, displayID := mancodeString(check["checkId"]), mancodeString(check["criterionId"]), mancodeString(check["displayId"])
			if checkIDs[checkID] || criterionIDs[criterionID] || displayIDs[displayID] { return false }
			checkIDs[checkID], criterionIDs[criterionID], displayIDs[displayID] = true, true, true
			requirement := mancodeString(check["verificationRequirement"])
			if !mancodeVerificationEvidence(check["automated"], "automated") || !mancodeVerificationEvidence(check["manual"], "manual") { return false }
			if requirement == "automated" && (check["automated"] == nil || check["manual"] != nil) { return false }
			if requirement == "manual" && (check["automated"] != nil || check["manual"] == nil) { return false }
			if requirement == "hybrid" && (check["automated"] == nil || check["manual"] == nil) { return false }
		}
		return true
	default:
		_ = taskRef
		return false
	}
}

func mancodeWorkflowExecutionStateShape(value any) bool {
	state, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(state, []string{"policy", "policyDigest", "runs", "attempts", "decisions", "ciObservations"}) ||
		!mancodeHasAllKeys(state, []string{"policy", "policyDigest", "runs", "attempts", "decisions", "ciObservations"}) {
		return false
	}
	policy, ok := state["policy"].(map[string]any)
	if !ok || !mancodeWorkflowExecutionPolicyShape(policy) || !validMancodeDigest(mancodeString(state["policyDigest"])) {
		return false
	}
	computed, err := mancodeCanonicalDigest(policy)
	if err != nil || computed != mancodeString(state["policyDigest"]) {
		return false
	}
	var runs []any
	var attempts []any
	var decisions []any
	var ciObservations []any
	for _, key := range []string{"runs", "attempts", "decisions", "ciObservations"} {
		items, ok := state[key].([]any)
		if !ok || len(items) > 10000 {
			return false
		}
		if key == "runs" {
			runs = items
		} else if key == "attempts" {
			attempts = items
		} else if key == "decisions" {
			decisions = items
		} else {
			ciObservations = items
		}
	}
	runIDs := map[string]bool{}
	runsByID := map[string]map[string]any{}
	parsedRuns := make([]map[string]any, 0, len(runs))
	for _, item := range runs {
		runID, _, _, valid := mancodeWorkflowExecutionRunShape(item)
		if !valid || runIDs[runID] {
			return false
		}
		runIDs[runID] = true
		run, _ := item.(map[string]any)
		runsByID[runID] = run
		parsedRuns = append(parsedRuns, run)
	}
	attemptIDs := map[string]bool{}
	attemptRunIDs := map[string][]string{}
	parsedAttempts := make([]map[string]any, 0, len(attempts))
	for _, item := range attempts {
		attemptID, runIDsForAttempt, valid := mancodeWorkflowExecutionAttemptShape(item)
		if !valid || attemptIDs[attemptID] {
			return false
		}
		attemptIDs[attemptID] = true
		attemptRunIDs[attemptID] = runIDsForAttempt
		attempt, _ := item.(map[string]any)
		parsedAttempts = append(parsedAttempts, attempt)
	}
	decisionIDs := map[string]bool{}
	for _, item := range decisions {
		decisionID, valid := mancodeWorkflowExecutionDecisionShape(item)
		if !valid || decisionIDs[decisionID] {
			return false
		}
		decisionIDs[decisionID] = true
	}
	for _, run := range parsedRuns {
		retryOf := run["infrastructureRetryOf"]
		if retryOf != nil && !runIDs[mancodeString(retryOf)] {
			return false
		}
		attemptID := run["attemptId"]
		if attemptID != nil && !attemptIDs[mancodeString(attemptID)] {
			return false
		}
	}
	for _, attempt := range parsedAttempts {
		attemptID := mancodeString(attempt["attemptId"])
		for _, runID := range attemptRunIDs[attemptID] {
			run, exists := runsByID[runID]
			if !exists || mancodeString(run["attemptId"]) != attemptID {
				return false
			}
		}
	}
	ciObservationIDs := map[string]bool{}
	ciObservationRunIDs := map[string]bool{}
	for _, item := range ciObservations {
		observationID, runID, valid := mancodeWorkflowStoredCIObservationShape(item)
		if !valid || ciObservationIDs[observationID] || ciObservationRunIDs[runID] {
			return false
		}
		run, exists := runsByID[runID]
		if !exists || mancodeString(run["purpose"]) != "ci_observation" ||
			mancodeString(run["state"]) != "succeeded" || run["applicable"] != true {
			return false
		}
		ciObservationIDs[observationID] = true
		ciObservationRunIDs[runID] = true
	}
	return true
}

func mancodeWorkflowStoredCIObservationShape(value any) (string, string, bool) {
	stored, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(stored, []string{"observationId", "runId", "policyDigest", "targetDigest", "target", "observation"}) ||
		!mancodeHasAllKeys(stored, []string{"observationId", "runId", "policyDigest", "targetDigest", "target", "observation"}) {
		return "", "", false
	}
	observationID, runID := mancodeString(stored["observationId"]), mancodeString(stored["runId"])
	if !mancodeOperationIDPattern.MatchString(observationID) || !mancodeOperationIDPattern.MatchString(runID) ||
		!validMancodeDigest(mancodeString(stored["policyDigest"])) || !validMancodeDigest(mancodeString(stored["targetDigest"])) ||
		!mancodeWorkflowCIContractShape(stored["target"]) || !mancodeWorkflowCIObservationShape(stored["observation"]) {
		return "", "", false
	}
	target, _ := stored["target"].(map[string]any)
	computed, err := mancodeCanonicalDigest(target)
	if err != nil || computed != mancodeString(stored["targetDigest"]) {
		return "", "", false
	}
	observation, _ := stored["observation"].(map[string]any)
	if mancodeString(observation["repository"]) != mancodeString(target["repository"]) ||
		mancodeString(observation["candidateSha"]) != mancodeString(target["candidateSha"]) ||
		mancodeString(observation["event"]) != mancodeString(target["event"]) {
		return "", "", false
	}
	return observationID, runID, true
}

func mancodeWorkflowCIContractShape(value any) bool {
	target, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(target, []string{"repository", "event", "candidateSha", "testedSha", "testedBinding", "workflows", "pr"}) ||
		!mancodeHasAllKeys(target, []string{"repository", "event", "candidateSha", "testedSha", "testedBinding", "workflows"}) {
		return false
	}
	repository := mancodeString(target["repository"])
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !mancodeExecutionText(target["repository"]) ||
		!mancodeExecutionRepositoryPart(parts[0]) || !mancodeExecutionRepositoryPart(parts[1]) ||
		!containsString([]string{"push", "pull_request"}, mancodeString(target["event"])) ||
		!containsString([]string{"approved_workflow_head", "unverified"}, mancodeString(target["testedBinding"])) ||
		!mancodeExecutionSHA(target["candidateSha"]) ||
		(target["testedSha"] != nil && !mancodeExecutionSHA(target["testedSha"])) {
		return false
	}
	workflows, ok := target["workflows"].([]any)
	if !ok || len(workflows) == 0 || len(workflows) > 10000 {
		return false
	}
	workflowIDs := map[int64]bool{}
	for _, item := range workflows {
		workflow, ok := item.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(workflow, []string{"id", "path", "configurationSha", "requiredJobs", "runId"}) ||
			!mancodeHasAllKeys(workflow, []string{"id", "path", "configurationSha", "requiredJobs"}) {
			return false
		}
		id, idOK := mancodePositiveInteger(workflow["id"])
		jobs, jobsOK := mancodeExecutionTextList(workflow["requiredJobs"], true)
		if !idOK || workflowIDs[id] || !mancodeExecutionRelativePath(workflow["path"]) || !jobsOK || !mancodeUniqueStrings(jobs) ||
			!mancodeExecutionSHA(workflow["configurationSha"]) {
			return false
		}
		if runID, exists := workflow["runId"]; exists && !mancodeIsPositiveInteger(runID) {
			return false
		}
		workflowIDs[id] = true
	}
	if prValue, exists := target["pr"]; exists {
		pr, ok := prValue.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(pr, []string{"number", "headSha", "baseSha", "mergeSha"}) ||
			!mancodeHasAllKeys(pr, []string{"number", "headSha", "baseSha", "mergeSha"}) ||
			!mancodeIsPositiveInteger(pr["number"]) || !mancodeExecutionSHA(pr["headSha"]) ||
			!mancodeExecutionSHA(pr["baseSha"]) || !mancodeExecutionSHA(pr["mergeSha"]) {
			return false
		}
	}
	return true
}

func mancodeWorkflowCIObservationShape(value any) bool {
	observation, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(observation, []string{"provider", "repository", "candidateSha", "testedSha", "event", "observedAt", "status", "runs", "reasons"}) ||
		!mancodeHasAllKeys(observation, []string{"provider", "repository", "candidateSha", "testedSha", "event", "observedAt", "status", "runs", "reasons"}) ||
		mancodeString(observation["provider"]) != "github" || !mancodeExecutionText(observation["repository"]) ||
		!mancodeExecutionSHA(observation["candidateSha"]) ||
		(observation["testedSha"] != nil && !mancodeExecutionSHA(observation["testedSha"])) ||
		!mancodeExecutionText(observation["event"]) || !mancodeRFC3339(observation["observedAt"]) ||
		!containsString([]string{"passed", "failed", "pending", "unverified"}, mancodeString(observation["status"])) {
		return false
	}
	if _, ok := mancodeExecutionTextList(observation["reasons"], false); !ok {
		return false
	}
	runs, ok := observation["runs"].([]any)
	if !ok || len(runs) > 10000 {
		return false
	}
	for _, item := range runs {
		run, ok := item.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(run, []string{"runId", "attempt", "workflowId", "workflowPath", "configurationSha", "headSha", "status", "conclusion", "jobs"}) ||
			!mancodeHasAllKeys(run, []string{"runId", "attempt", "workflowId", "workflowPath", "configurationSha", "headSha", "status", "conclusion", "jobs"}) ||
			!mancodeIsPositiveInteger(run["runId"]) || !mancodeIsPositiveInteger(run["attempt"]) ||
			!mancodeIsPositiveInteger(run["workflowId"]) || !mancodeExecutionRelativePath(run["workflowPath"]) ||
			!mancodeExecutionSHA(run["configurationSha"]) || !mancodeExecutionSHA(run["headSha"]) ||
			!mancodeExecutionText(run["status"]) || !mancodeExecutionNullableText(run["conclusion"]) {
			return false
		}
		jobs, ok := run["jobs"].([]any)
		if !ok || len(jobs) > 10000 {
			return false
		}
		for _, jobValue := range jobs {
			job, ok := jobValue.(map[string]any)
			if !ok || !mancodeHasOnlyKeys(job, []string{"id", "name", "status", "conclusion"}) ||
				!mancodeHasAllKeys(job, []string{"id", "name", "status", "conclusion"}) ||
				!mancodeIsPositiveInteger(job["id"]) || !mancodeExecutionText(job["name"]) ||
				!mancodeExecutionText(job["status"]) || !mancodeExecutionNullableText(job["conclusion"]) {
				return false
			}
		}
	}
	return true
}

func mancodeExecutionSHA(value any) bool {
	sha, ok := value.(string)
	if !ok || (len(sha) != 40 && len(sha) != 64) || sha != strings.ToLower(sha) {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func mancodeWorkflowExecutionAttemptShape(value any) (string, []string, bool) {
	attempt, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(attempt, []string{
		"attemptId", "problemId", "classification", "hypothesis", "evidence", "subject", "reservedAt", "finishedAt",
		"state", "runIds", "summary", "interruptions",
	}) || !mancodeHasAllKeys(attempt, []string{
		"attemptId", "problemId", "classification", "hypothesis", "evidence", "subject", "reservedAt", "finishedAt",
		"state", "runIds", "summary", "interruptions",
	}) {
		return "", nil, false
	}
	attemptID := mancodeString(attempt["attemptId"])
	if !mancodeOperationIDPattern.MatchString(attemptID) || !mancodeExecutionText(attempt["problemId"]) ||
		!containsString([]string{"implementation", "test_or_ci", "infrastructure", "flaky", "pre_existing", "contract_conflict", "unknown"}, mancodeString(attempt["classification"])) ||
		!mancodeExecutionText(attempt["hypothesis"]) || !mancodeExecutionText(attempt["evidence"]) ||
		!mancodeWorkflowEvidenceSubjectShape(attempt["subject"]) || !mancodeRFC3339(attempt["reservedAt"]) ||
		(attempt["finishedAt"] != nil && !mancodeRFC3339(attempt["finishedAt"])) ||
		!containsString([]string{"reserved", "succeeded", "failed", "interrupted", "cancelled"}, mancodeString(attempt["state"])) ||
		!mancodeExecutionNullableText(attempt["summary"]) {
		return "", nil, false
	}
	runIDs, ok := mancodeExecutionTextList(attempt["runIds"], false)
	if !ok {
		return "", nil, false
	}
	for _, runID := range runIDs {
		if !mancodeOperationIDPattern.MatchString(runID) {
			return "", nil, false
		}
	}
	interruptions, ok := attempt["interruptions"].([]any)
	if !ok || len(interruptions) > 10000 {
		return "", nil, false
	}
	for _, item := range interruptions {
		interruption, ok := item.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(interruption, []string{"finishedAt", "runIds", "summary"}) ||
			!mancodeHasAllKeys(interruption, []string{"finishedAt", "runIds", "summary"}) ||
			!mancodeRFC3339(interruption["finishedAt"]) || !mancodeExecutionText(interruption["summary"]) {
			return "", nil, false
		}
		interruptedRunIDs, ok := mancodeExecutionTextList(interruption["runIds"], false)
		if !ok {
			return "", nil, false
		}
		for _, runID := range interruptedRunIDs {
			if !mancodeOperationIDPattern.MatchString(runID) {
				return "", nil, false
			}
		}
	}
	return attemptID, runIDs, true
}

func mancodeWorkflowExecutionDecisionShape(value any) (string, bool) {
	decision, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(decision, []string{
		"decisionId", "kind", "actorId", "createdAt", "approval", "problemId", "sourceProblemIds", "scenarioId", "runId",
		"requirementsDigest", "policyDigest", "expiresAt", "delta", "previousPolicy",
	}) || !mancodeHasAllKeys(decision, []string{
		"decisionId", "kind", "actorId", "createdAt", "approval", "problemId", "sourceProblemIds", "scenarioId", "runId",
		"requirementsDigest", "policyDigest", "expiresAt", "delta", "previousPolicy",
	}) {
		return "", false
	}
	decisionID := mancodeString(decision["decisionId"])
	if !mancodeOperationIDPattern.MatchString(decisionID) ||
		!containsString([]string{"budget_extension", "scenario_exception", "contract_revision", "problem_merge", "run_reconciliation"}, mancodeString(decision["kind"])) ||
		!mancodeOperationIDPattern.MatchString(mancodeString(decision["actorId"])) || !mancodeRFC3339(decision["createdAt"]) ||
		!mancodeWorkflowExecutionApprovalShape(decision["approval"]) || !mancodeExecutionNullableText(decision["problemId"]) {
		return "", false
	}
	sourceProblemIDs, ok := mancodeExecutionTextList(decision["sourceProblemIds"], false)
	if !ok || !mancodeExecutionNullableText(decision["scenarioId"]) || !mancodeNullableULID(decision["runId"]) ||
		!validMancodeDigest(mancodeString(decision["requirementsDigest"])) || !validMancodeDigest(mancodeString(decision["policyDigest"])) ||
		(decision["expiresAt"] != nil && !mancodeRFC3339(decision["expiresAt"])) {
		return "", false
	}
	_ = sourceProblemIDs
	if decision["delta"] != nil {
		delta, ok := decision["delta"].(map[string]any)
		if !ok || !mancodeHasOnlyKeys(delta, []string{"runs", "executionMs", "repairAttempts", "problemFailures"}) ||
			!mancodeHasAllKeys(delta, []string{"runs", "executionMs", "repairAttempts", "problemFailures"}) {
			return "", false
		}
		for _, key := range []string{"runs", "executionMs", "repairAttempts", "problemFailures"} {
			if !mancodeIsNonNegativeInteger(delta[key]) {
				return "", false
			}
		}
	}
	if decision["previousPolicy"] != nil {
		previousPolicy, ok := decision["previousPolicy"].(map[string]any)
		if !ok || !mancodeWorkflowExecutionPolicyShape(previousPolicy) {
			return "", false
		}
	}
	return decisionID, true
}

func mancodeWorkflowExecutionApprovalShape(value any) bool {
	approval, ok := value.(map[string]any)
	return ok && mancodeHasOnlyKeys(approval, []string{"confirmed", "source", "reason", "evidence"}) &&
		mancodeHasAllKeys(approval, []string{"confirmed", "source", "reason", "evidence"}) &&
		approval["confirmed"] == true && mancodeExecutionText(approval["source"]) &&
		mancodeExecutionText(approval["reason"]) && mancodeExecutionText(approval["evidence"])
}

func mancodeWorkflowExecutionRunShape(value any) (string, string, string, bool) {
	run, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(run, []string{
		"runId", "purpose", "checkId", "scenarioId", "problemId", "attemptId", "infrastructureRetryOf", "hypothesis",
		"argv", "runnerArgv", "cwd", "timeoutMs", "subject", "testIdentity", "configurationIdentity",
		"requirementsDigest", "planVersion", "policyDigest", "actorId", "checkoutId", "reservedAt", "state",
		"identity", "result", "interruptions", "vitest", "applicable",
	}) || !mancodeHasAllKeys(run, []string{
		"runId", "purpose", "checkId", "scenarioId", "problemId", "attemptId", "infrastructureRetryOf", "hypothesis",
		"argv", "runnerArgv", "cwd", "timeoutMs", "subject", "testIdentity", "configurationIdentity",
		"requirementsDigest", "planVersion", "policyDigest", "actorId", "checkoutId", "reservedAt", "state",
		"identity", "result", "interruptions", "vitest", "applicable",
	}) {
		return "", "", "", false
	}
	runID := mancodeString(run["runId"])
	attemptID := mancodeString(run["attemptId"])
	retryID := mancodeString(run["infrastructureRetryOf"])
	if !mancodeOperationIDPattern.MatchString(runID) ||
		!containsString([]string{"verification", "tdd_red", "tdd_green", "regression_replay", "diagnosis", "ci_observation"}, mancodeString(run["purpose"])) ||
		!mancodeExecutionNullableText(run["checkId"]) || !mancodeExecutionNullableText(run["scenarioId"]) ||
		!mancodeExecutionNullableText(run["problemId"]) || !mancodeNullableULID(run["attemptId"]) ||
		!mancodeNullableULID(run["infrastructureRetryOf"]) || !mancodeExecutionNullableText(run["hypothesis"]) {
		return "", "", "", false
	}
	argv, argvOK := mancodeExecutionTextList(run["argv"], true)
	runnerArgv, runnerArgvOK := mancodeExecutionTextList(run["runnerArgv"], true)
	_ = argv
	_ = runnerArgv
	timeoutMS, timeoutOK := mancodePositiveInteger(run["timeoutMs"])
	if !argvOK || !runnerArgvOK || !mancodeExecutionRelativePath(run["cwd"]) || !timeoutOK || timeoutMS > 2147480000 ||
		!mancodeWorkflowEvidenceSubjectShape(run["subject"]) ||
		!mancodeExecutionNullableDigest(run["testIdentity"]) || !mancodeExecutionNullableDigest(run["configurationIdentity"]) ||
		!validMancodeDigest(mancodeString(run["requirementsDigest"])) || !mancodeIsPositiveInteger(run["planVersion"]) ||
		!validMancodeDigest(mancodeString(run["policyDigest"])) || !mancodeOperationIDPattern.MatchString(mancodeString(run["actorId"])) ||
		!mancodeExecutionText(run["checkoutId"]) || !mancodeRFC3339(run["reservedAt"]) {
		return "", "", "", false
	}
	runState := mancodeString(run["state"])
	if !containsString([]string{"reserved", "running", "succeeded", "failed", "timed_out", "cancelled", "interrupted"}, runState) ||
		!mancodeExecutionBoolean(run["applicable"]) {
		return "", "", "", false
	}
	var identity map[string]any
	if run["identity"] != nil {
		identity, ok = mancodeWorkflowExecutionIdentityShape(run["identity"])
		if !ok || mancodeString(identity["runId"]) != runID {
			return "", "", "", false
		}
	}
	var resultIdentity map[string]any
	if run["result"] != nil {
		resultIdentity, ok = mancodeWorkflowPersistedRunResultShape(run["result"])
		if !ok || runState == "reserved" || runState == "running" {
			return "", "", "", false
		}
		result, _ := run["result"].(map[string]any)
		if mancodeString(result["status"]) != runState {
			return "", "", "", false
		}
		if resultIdentity != nil {
			if identity == nil {
				return "", "", "", false
			}
			left, leftErr := mancodeCanonicalDigest(resultIdentity)
			right, rightErr := mancodeCanonicalDigest(identity)
			if leftErr != nil || rightErr != nil || left != right {
				return "", "", "", false
			}
		}
	} else if runState != "reserved" && runState != "running" {
		return "", "", "", false
	}
	if runState == "running" && identity == nil {
		return "", "", "", false
	}
	interruptions, ok := run["interruptions"].([]any)
	if !ok || len(interruptions) > 10000 {
		return "", "", "", false
	}
	for _, interruption := range interruptions {
		if _, valid := mancodeWorkflowPersistedRunResultShape(interruption); !valid {
			return "", "", "", false
		}
	}
	var vitestRunID, vitestExecutorID, vitestTestIdentity, vitestConfigIdentity string
	if run["vitest"] != nil {
		vitestRunID, vitestExecutorID, vitestTestIdentity, vitestConfigIdentity, ok = mancodeWorkflowVitestAssessmentShape(run["vitest"])
		if !ok || identity == nil || vitestRunID != runID || vitestExecutorID != mancodeString(identity["executorId"]) ||
			vitestTestIdentity != mancodeString(run["testIdentity"]) || vitestConfigIdentity != mancodeString(run["configurationIdentity"]) {
			return "", "", "", false
		}
	}
	return runID, attemptID, retryID, true
}

func mancodeWorkflowEvidenceSubjectShape(value any) bool {
	subject, ok := value.(map[string]any)
	return ok && mancodeHasOnlyKeys(subject, []string{"contentDigest", "environment"}) &&
		mancodeHasAllKeys(subject, []string{"contentDigest", "environment"}) &&
		validMancodeDigest(mancodeString(subject["contentDigest"])) && mancodeExecutionText(subject["environment"])
}

func mancodeWorkflowExecutionIdentityShape(value any) (map[string]any, bool) {
	identity, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(identity, []string{"protocolVersion", "runId", "executorId", "commandDigest", "startedAt", "pid", "platform"}) ||
		!mancodeHasAllKeys(identity, []string{"protocolVersion", "runId", "executorId", "commandDigest", "startedAt", "pid", "platform"}) ||
		mancodeInteger(identity["protocolVersion"]) != 1 || !mancodeOperationIDPattern.MatchString(mancodeString(identity["runId"])) ||
		!mancodeOperationIDPattern.MatchString(mancodeString(identity["executorId"])) || !validMancodeDigest(mancodeString(identity["commandDigest"])) ||
		!mancodeRFC3339(identity["startedAt"]) || !mancodeIsPositiveInteger(identity["pid"]) || !mancodeExecutionText(identity["platform"]) {
		return nil, false
	}
	return identity, true
}

func mancodeWorkflowPersistedRunResultShape(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(result, []string{
		"identity", "status", "started", "exitCode", "signal", "reason", "startedAt", "finishedAt", "durationMs",
		"outputTruncated", "cleanupConfirmed", "outputArtifactRef", "summary",
	}) || !mancodeHasAllKeys(result, []string{
		"identity", "status", "started", "exitCode", "signal", "reason", "startedAt", "finishedAt", "durationMs",
		"outputTruncated", "cleanupConfirmed", "outputArtifactRef", "summary",
	}) || !containsString([]string{"succeeded", "failed", "timed_out", "cancelled", "interrupted"}, mancodeString(result["status"])) ||
		!mancodeExecutionBoolean(result["started"]) || !mancodeExecutionNullableInteger(result["exitCode"]) ||
		!mancodeExecutionNullableText(result["signal"]) || !mancodeExecutionNullableText(result["reason"]) ||
		(result["startedAt"] != nil && !mancodeRFC3339(result["startedAt"])) || !mancodeRFC3339(result["finishedAt"]) ||
		!mancodeIsNonNegativeInteger(result["durationMs"]) || !mancodeExecutionBoolean(result["outputTruncated"]) ||
		!mancodeExecutionBoolean(result["cleanupConfirmed"]) || !mancodeExecutionNullableText(result["summary"]) {
		return nil, false
	}
	var identity map[string]any
	if result["identity"] != nil {
		identity, ok = mancodeWorkflowExecutionIdentityShape(result["identity"])
		if !ok {
			return nil, false
		}
	}
	if result["outputArtifactRef"] != nil && !mancodeWorkflowArtifactRefShape(result["outputArtifactRef"]) {
		return nil, false
	}
	startedAt := result["startedAt"]
	if result["started"] == true && (identity == nil || startedAt == nil) {
		return nil, false
	}
	if result["cleanupConfirmed"] == false && result["status"] != "interrupted" {
		return nil, false
	}
	if result["status"] == "succeeded" {
		exitCode, exitCodeOK := mancodeIntegerValue(result["exitCode"])
		if result["started"] != true || !exitCodeOK || exitCode != 0 || result["signal"] != nil || result["outputTruncated"] == true {
			return nil, false
		}
	}
	if startedAt != nil {
		startedTime, err1 := time.Parse(time.RFC3339Nano, mancodeString(startedAt))
		finishedTime, err2 := time.Parse(time.RFC3339Nano, mancodeString(result["finishedAt"]))
		if err1 != nil || err2 != nil || finishedTime.Before(startedTime) {
			return nil, false
		}
	}
	return identity, true
}

func mancodeWorkflowArtifactRefShape(value any) bool {
	ref, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(ref, []string{"taskRef", "kind", "artifactId"}) || !mancodeHasAllKeys(ref, []string{"taskRef", "kind"}) {
		return false
	}
	if _, ok := mancodeTaskRefFromValue(ref["taskRef"]); !ok ||
		!containsString([]string{"requirements", "requirements_markdown", "plan", "review_ledger", "verification_ledger", "checkpoint", "handoff", "summary", "review_report", "evidence_summary"}, mancodeString(ref["kind"])) {
		return false
	}
	if artifactID, exists := ref["artifactId"]; exists && !mancodeOperationIDPattern.MatchString(mancodeString(artifactID)) {
		return false
	}
	return true
}

func mancodeWorkflowVitestAssessmentShape(value any) (string, string, string, string, bool) {
	assessment, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(assessment, []string{
		"adapter", "adapterVersion", "runId", "executorId", "testIdentity", "configurationIdentity", "assessment", "targets",
		"collectionErrors", "unhandledErrors", "reasons",
	}) || !mancodeHasAllKeys(assessment, []string{
		"adapter", "adapterVersion", "runId", "executorId", "testIdentity", "configurationIdentity", "assessment", "targets",
		"collectionErrors", "unhandledErrors", "reasons",
	}) || assessment["adapter"] != "vitest" || mancodeInteger(assessment["adapterVersion"]) != 1 ||
		!mancodeOperationIDPattern.MatchString(mancodeString(assessment["runId"])) ||
		!mancodeOperationIDPattern.MatchString(mancodeString(assessment["executorId"])) ||
		!validMancodeDigest(mancodeString(assessment["testIdentity"])) || !validMancodeDigest(mancodeString(assessment["configurationIdentity"])) ||
		!containsString([]string{"assertion_failure", "passed", "unverified"}, mancodeString(assessment["assessment"])) ||
		!mancodeIsNonNegativeInteger(assessment["collectionErrors"]) || !mancodeIsNonNegativeInteger(assessment["unhandledErrors"]) {
		return "", "", "", "", false
	}
	targets, ok := assessment["targets"].([]any)
	if !ok || len(targets) > 10000 {
		return "", "", "", "", false
	}
	seenTargets := map[string]bool{}
	for _, item := range targets {
		target, ok := item.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(target, []string{"file", "name", "status", "errorNames"}) ||
			!mancodeHasAllKeys(target, []string{"file", "name", "status", "errorNames"}) ||
			!mancodeExecutionRelativePath(target["file"]) || !mancodeExecutionText(target["name"]) ||
			!containsString([]string{"passed", "failed", "skipped", "pending"}, mancodeString(target["status"])) {
			return "", "", "", "", false
		}
		errorNames, valid := mancodeExecutionTextList(target["errorNames"], false)
		if !valid {
			return "", "", "", "", false
		}
		_ = errorNames
		key := mancodeString(target["file"]) + "\x00" + mancodeString(target["name"])
		if seenTargets[key] {
			return "", "", "", "", false
		}
		seenTargets[key] = true
	}
	if _, ok := mancodeExecutionTextList(assessment["reasons"], false); !ok {
		return "", "", "", "", false
	}
	return mancodeString(assessment["runId"]), mancodeString(assessment["executorId"]),
		mancodeString(assessment["testIdentity"]), mancodeString(assessment["configurationIdentity"]), true
}

func mancodeExecutionNullableText(value any) bool {
	return value == nil || mancodeExecutionText(value)
}

func mancodeExecutionNullableDigest(value any) bool {
	return value == nil || validMancodeDigest(mancodeString(value))
}

func mancodeExecutionNullableInteger(value any) bool {
	if value == nil {
		return true
	}
	_, ok := mancodeIntegerValue(value)
	return ok
}

func mancodeExecutionBoolean(value any) bool {
	_, ok := value.(bool)
	return ok
}

func mancodeWorkflowExecutionPolicyShape(policy map[string]any) bool {
	if !mancodeHasOnlyKeys(policy, []string{"version", "budget", "checks", "scenarios", "delivery", "ci"}) ||
		!mancodeHasAllKeys(policy, []string{"version", "budget", "checks", "scenarios", "delivery", "ci"}) ||
		mancodeInteger(policy["version"]) != 1 {
		return false
	}
	budget, ok := policy["budget"].(map[string]any)
	if !ok || !mancodeHasOnlyKeys(budget, []string{"maxRuns", "maxExecutionMs", "maxRepairAttempts", "commandTimeoutMs", "ciTimeoutMs"}) ||
		!mancodeHasAllKeys(budget, []string{"maxRuns", "maxExecutionMs", "maxRepairAttempts", "commandTimeoutMs", "ciTimeoutMs"}) {
		return false
	}
	_, maxRunsOK := mancodePositiveInteger(budget["maxRuns"])
	maxExecutionMS, maxExecutionOK := mancodePositiveInteger(budget["maxExecutionMs"])
	_, repairAttemptsOK := mancodeNonNegativeInteger(budget["maxRepairAttempts"])
	commandTimeout, commandTimeoutOK := mancodePositiveInteger(budget["commandTimeoutMs"])
	ciTimeout, ciTimeoutOK := mancodePositiveInteger(budget["ciTimeoutMs"])
	if !maxRunsOK || !maxExecutionOK || !repairAttemptsOK || !commandTimeoutOK || !ciTimeoutOK ||
		commandTimeout > maxExecutionMS || ciTimeout > maxExecutionMS ||
		commandTimeout > 2147480000 || ciTimeout > 2147480000 {
		return false
	}
	checks, checksOK := policy["checks"].([]any)
	scenarios, scenariosOK := policy["scenarios"].([]any)
	if !checksOK || !scenariosOK || len(checks) > 10000 || len(scenarios) > 10000 {
		return false
	}
	checkIDs := map[string]bool{}
	for _, item := range checks {
		check, ok := item.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(check, []string{"id", "acceptanceIds", "argv", "cwd", "surface"}) ||
			!mancodeHasAllKeys(check, []string{"id", "acceptanceIds", "argv", "cwd", "surface"}) {
			return false
		}
		id := mancodeString(check["id"])
		acceptanceIDs, acceptanceOK := mancodeExecutionTextList(check["acceptanceIds"], true)
		_, argvOK := mancodeExecutionTextList(check["argv"], true)
		if !mancodeExecutionText(check["id"]) || checkIDs[id] || !acceptanceOK || !mancodeUniqueStrings(acceptanceIDs) ||
			!argvOK || !mancodeExecutionRelativePath(check["cwd"]) ||
			!containsString([]string{"unit", "component", "handler", "real_http", "browser", "device", "external_service", "manual_observation"}, mancodeString(check["surface"])) {
			return false
		}
		checkIDs[id] = true
	}
	scenarioIDs := map[string]bool{}
	for _, item := range scenarios {
		scenario, ok := item.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(scenario, []string{"id", "acceptanceId", "mode", "rationale", "alternativeCheckIds", "testInputs", "configInputs", "targets"}) ||
			!mancodeHasAllKeys(scenario, []string{"id", "acceptanceId", "mode", "rationale", "alternativeCheckIds", "testInputs", "configInputs", "targets"}) {
			return false
		}
		id := mancodeString(scenario["id"])
		mode := mancodeString(scenario["mode"])
		alternatives, alternativesOK := mancodeExecutionTextList(scenario["alternativeCheckIds"], false)
		testInputs, testInputsOK := mancodeExecutionRelativePathList(scenario["testInputs"])
		_, configInputsOK := mancodeExecutionRelativePathList(scenario["configInputs"])
		targets, targetsOK := scenario["targets"].([]any)
		if !mancodeExecutionText(scenario["id"]) || scenarioIDs[id] || !mancodeExecutionText(scenario["acceptanceId"]) ||
			!containsString([]string{"required", "alternative", "not_applicable"}, mode) || !mancodeExecutionText(scenario["rationale"]) ||
			!alternativesOK || !testInputsOK || !configInputsOK || !targetsOK || len(targets) > 10000 {
			return false
		}
		scenarioIDs[id] = true
		targetFiles := map[string]bool{}
		targetNames := map[string]bool{}
		for _, targetValue := range targets {
			target, ok := targetValue.(map[string]any)
			if !ok || !mancodeHasOnlyKeys(target, []string{"file", "name"}) ||
				!mancodeHasAllKeys(target, []string{"file", "name"}) || !mancodeExecutionRelativePath(target["file"]) ||
				!mancodeExecutionText(target["name"]) {
				return false
			}
			file, name := mancodeString(target["file"]), mancodeString(target["name"])
			if targetFiles[file+"\x00"+name] {
				return false
			}
			targetFiles[file+"\x00"+name] = true
			targetNames[file] = true
		}
		if mode == "required" {
			if len(targets) == 0 {
				return false
			}
			for file := range targetNames {
				if !mancodeExecutionInputsContain(testInputs, file) {
					return false
				}
			}
		}
		if mode == "alternative" && len(alternatives) == 0 {
			return false
		}
		for _, alternative := range alternatives {
			if !checkIDs[alternative] {
				return false
			}
		}
	}
	delivery := mancodeString(policy["delivery"])
	if delivery != "local" && delivery != "remote_required" {
		return false
	}
	if policy["ci"] == nil {
		return delivery == "local"
	}
	if delivery != "remote_required" {
		return false
	}
	ci, ok := policy["ci"].(map[string]any)
	if !ok || !mancodeHasOnlyKeys(ci, []string{"repository", "event", "testedBinding", "workflows"}) ||
		!mancodeHasAllKeys(ci, []string{"repository", "event", "testedBinding", "workflows"}) {
		return false
	}
	repository := mancodeString(ci["repository"])
	repositoryParts := strings.Split(repository, "/")
	if !mancodeExecutionText(ci["repository"]) || len(repositoryParts) != 2 ||
		!mancodeExecutionRepositoryPart(repositoryParts[0]) || !mancodeExecutionRepositoryPart(repositoryParts[1]) ||
		!containsString([]string{"push", "pull_request"}, mancodeString(ci["event"])) ||
		!containsString([]string{"approved_workflow_head", "unverified"}, mancodeString(ci["testedBinding"])) {
		return false
	}
	workflows, ok := ci["workflows"].([]any)
	if !ok || len(workflows) == 0 || len(workflows) > 10000 {
		return false
	}
	workflowIDs := map[int64]bool{}
	for _, item := range workflows {
		workflow, ok := item.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(workflow, []string{"id", "path", "configurationSha", "requiredJobs"}) ||
			!mancodeHasAllKeys(workflow, []string{"id", "path", "configurationSha", "requiredJobs"}) {
			return false
		}
		id, idOK := mancodePositiveInteger(workflow["id"])
		jobs, jobsOK := mancodeExecutionTextList(workflow["requiredJobs"], true)
		configurationSHA := mancodeString(workflow["configurationSha"])
		if !idOK || workflowIDs[id] || !mancodeExecutionRelativePath(workflow["path"]) || !jobsOK || !mancodeUniqueStrings(jobs) ||
			(len(configurationSHA) != 40 && len(configurationSHA) != 64) {
			return false
		}
		if _, err := hex.DecodeString(configurationSHA); err != nil || configurationSHA != strings.ToLower(configurationSHA) {
			return false
		}
		workflowIDs[id] = true
	}
	return true
}

func mancodeExecutionInputsContain(inputs []string, target string) bool {
	for _, input := range inputs {
		if input == "." || target == input || strings.HasPrefix(target, input+"/") {
			return true
		}
	}
	return false
}

func mancodeExecutionRepositoryPart(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '.' || character == '-') {
			return false
		}
	}
	return true
}

func mancodeExecutionText(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != "" && !strings.ContainsRune(text, 0) && len(utf16.Encode([]rune(text))) <= 16000
}

func mancodeExecutionTextList(value any, nonEmpty bool) ([]string, bool) {
	items, ok := value.([]any)
	if !ok || len(items) > 1000 || (nonEmpty && len(items) == 0) {
		return nil, false
	}
	result := make([]string, len(items))
	for index, item := range items {
		if !mancodeExecutionText(item) {
			return nil, false
		}
		result[index] = mancodeString(item)
	}
	return result, true
}

func mancodeExecutionRelativePath(value any) bool {
	if !mancodeExecutionText(value) {
		return false
	}
	path := mancodeString(value)
	if path == "." {
		return true
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, "\\") ||
		(len(path) >= 2 && ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) && path[1] == ':') {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == ".." {
			return false
		}
	}
	return true
}

func mancodeExecutionRelativePathList(value any) ([]string, bool) {
	items, ok := mancodeExecutionTextList(value, false)
	if !ok {
		return nil, false
	}
	for _, item := range items {
		if !mancodeExecutionRelativePath(item) {
			return nil, false
		}
	}
	return items, true
}

func mancodeUniqueStrings(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func mancodeWorkflowReviewDeliveryShape(value any) bool {
	delivery, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(delivery, []string{
		"subject", "reviewer", "direction", "correctness", "proportionality", "nextAction", "coverage",
	}) || !mancodeHasAllKeys(delivery, []string{
		"subject", "reviewer", "direction", "correctness", "proportionality", "nextAction", "coverage",
	}) {
		return false
	}
	subject, ok := delivery["subject"].(map[string]any)
	if !ok || !mancodeHasOnlyKeys(subject, []string{"contentDigest", "environment"}) ||
		!mancodeHasAllKeys(subject, []string{"contentDigest", "environment"}) ||
		!validMancodeDigest(mancodeString(subject["contentDigest"])) || !mancodeNonEmptyString(subject["environment"]) {
		return false
	}
	if !containsString([]string{"independent", "self"}, mancodeString(delivery["reviewer"])) ||
		!mancodeNonEmptyString(delivery["direction"]) || !mancodeNonEmptyString(delivery["correctness"]) ||
		!mancodeNonEmptyString(delivery["proportionality"]) || !mancodeNonEmptyString(delivery["nextAction"]) {
		return false
	}
	coverage, ok := delivery["coverage"].([]any)
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for _, item := range coverage {
		entry, ok := item.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(entry, []string{"acceptanceId", "status", "evidence"}) ||
			!mancodeHasAllKeys(entry, []string{"acceptanceId", "status", "evidence"}) ||
			!mancodeNonEmptyString(entry["acceptanceId"]) || !mancodeNonEmptyString(entry["evidence"]) ||
			!containsString([]string{"met", "missing", "unverified"}, mancodeString(entry["status"])) {
			return false
		}
		acceptanceID := mancodeString(entry["acceptanceId"])
		if seen[acceptanceID] {
			return false
		}
		seen[acceptanceID] = true
	}
	return true
}

func checkReviewDeliveryContext(review, requirements map[string]any, add func(string, bool)) {
	delivery, exists := review["delivery"].(map[string]any)
	if !exists {
		return
	}
	coverage, _ := delivery["coverage"].([]any)
	criteria, _ := requirements["acceptanceCriteria"].([]any)
	criteriaByDisplayID := make(map[string]map[string]any, len(criteria))
	for _, item := range criteria {
		criterion, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if displayID := mancodeString(criterion["displayId"]); displayID != "" {
			criteriaByDisplayID[displayID] = criterion
		}
	}
	coverageByID := make(map[string]map[string]any, len(coverage))
	for _, item := range coverage {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		acceptanceID := mancodeString(entry["acceptanceId"])
		if criteriaByDisplayID[acceptanceID] == nil {
			add("review_delivery_unknown_acceptance", true)
			continue
		}
		coverageByID[acceptanceID] = entry
	}
	if mancodeString(review["status"]) != "passed" {
		return
	}
	for displayID, criterion := range criteriaByDisplayID {
		if criterion["required"] != true {
			continue
		}
		entry := coverageByID[displayID]
		if entry == nil || mancodeString(entry["status"]) != "met" {
			add("review_delivery_acceptance_unmet", true)
		}
	}
}

func mancodeValidateRequirementItemGroup(value any, kind string, displayIDs map[string]bool) (map[string]bool, bool) {
	items, ok := value.([]any)
	if !ok { return nil, false }
	idKey, idKeys := "", []string{}
	switch kind {
	case "technicalDecisions":
		idKey, idKeys = "decisionId", []string{"displayId", "legacyId", "decisionId", "statement"}
	case "defaults":
		idKey, idKeys = "defaultId", []string{"displayId", "legacyId", "defaultId", "statement"}
	case "requirements":
		idKey, idKeys = "requirementId", []string{"displayId", "legacyId", "requirementId", "statement", "priority"}
	case "blockingUnknowns":
		idKey, idKeys = "unknownId", []string{"displayId", "legacyId", "unknownId", "statement", "status"}
	default:
		return nil, false
	}
	ids := map[string]bool{}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(entry, idKeys) || !mancodeHasAllKeys(entry, idKeys) ||
			!mancodeItemIdentity(entry) || !mancodeOperationIDPattern.MatchString(mancodeString(entry[idKey])) ||
			!mancodeNonEmptyString(entry["statement"]) || displayIDs[mancodeString(entry["displayId"])] {
			return nil, false
		}
		id := mancodeString(entry[idKey])
		if ids[id] { return nil, false }
		if kind == "requirements" && !containsString([]string{"must", "should", "could"}, mancodeString(entry["priority"])) { return nil, false }
		if kind == "blockingUnknowns" && !containsString([]string{"open", "resolved", "accepted_risk"}, mancodeString(entry["status"])) { return nil, false }
		ids[id], displayIDs[mancodeString(entry["displayId"])] = true, true
	}
	return ids, true
}

func mancodeValidSkip(value any) bool {
	object, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(object, []string{"reason", "approvedByActorId", "approvedAt", "source"}) ||
		!mancodeHasAllKeys(object, []string{"reason", "approvedByActorId", "approvedAt", "source"}) ||
		!mancodeNonEmptyString(object["reason"]) || !mancodeNullableULID(object["approvedByActorId"]) ||
		!mancodeRFC3339(object["approvedAt"]) { return false }
	source := mancodeString(object["source"])
	if source != "actor" && source != "legacy_migration" { return false }
	return source != "actor" || object["approvedByActorId"] != nil
}

func mancodeWorkflowCheckpointShape(checkpoint map[string]any) bool {
	if mancodeInteger(checkpoint["schemaVersion"]) != 1 ||
		!mancodeOperationIDPattern.MatchString(mancodeString(checkpoint["checkpointId"])) ||
		!mancodeOperationIDPattern.MatchString(mancodeString(checkpoint["operationId"])) ||
			!mancodeIsPositiveInteger(checkpoint["taskRevision"]) ||
		!mancodeIsNonNegativeInteger(checkpoint["ownershipEpochAtOffer"]) ||
		!containsString([]string{"plan_confirmed", "scope_changed", "requirements_reframed", "diagnostic_started", "base_changed", "verification_completed", "blocked", "handoff_offered", "completed"}, mancodeString(checkpoint["kind"])) ||
		!mancodeNonEmptyString(checkpoint["summary"]) || !mancodeNonEmptyString(checkpoint["nextAction"]) ||
		!mancodeRFC3339(checkpoint["createdAt"]) {
		return false
	}
	git, ok := checkpoint["git"].(map[string]any)
	if !ok || !mancodeHasOnlyKeys(git, []string{"branch", "head", "base"}) ||
		!mancodeHasAllKeys(git, []string{"branch", "head", "base"}) { return false }
	for _, key := range []string{"branch", "head", "base"} {
		value := git[key]
		if value != nil && !mancodeNonEmptyString(value) { return false }
	}
	governance, ok := checkpoint["governance"].(map[string]any)
	if !ok || !mancodeHasOnlyKeys(governance, []string{"requirementsDigest", "planVersion", "reviewLedgerDigest", "verificationLedgerDigest"}) ||
		!mancodeHasAllKeys(governance, []string{"requirementsDigest", "planVersion", "reviewLedgerDigest", "verificationLedgerDigest"}) ||
		!validMancodeDigest(mancodeString(governance["requirementsDigest"])) ||
		!mancodeIsPositiveInteger(governance["planVersion"]) ||
		!validMancodeDigest(mancodeString(governance["reviewLedgerDigest"])) ||
		!validMancodeDigest(mancodeString(governance["verificationLedgerDigest"])) { return false }
	createdBy, ok := checkpoint["createdBy"].(map[string]any)
	return ok && mancodeHasOnlyKeys(createdBy, []string{"actorId", "client"}) &&
		mancodeHasAllKeys(createdBy, []string{"actorId", "client"}) &&
		mancodeOperationIDPattern.MatchString(mancodeString(createdBy["actorId"])) &&
		mancodeNonEmptyString(createdBy["client"])
}

func mancodeDigestFields(object map[string]any, fields []string) (string, error) {
	projection := make(map[string]any, len(fields))
	for _, field := range fields {
		value, ok := object[field]
		if !ok { return "", fmt.Errorf("missing digest field %s", field) }
		projection[field] = value
	}
	return mancodeCanonicalDigest(projection)
}

func mancodeWorkflowMetadataDigest(metadata map[string]any) (string, error) {
	projection := make(map[string]any, len(mancodeWorkflowMetadataDigestFields))
	for _, field := range mancodeWorkflowMetadataDigestFields {
		value, exists := metadata[field]
		if !exists { return "", fmt.Errorf("missing metadata digest field %s", field) }
		projection[field] = cloneMancodeJSON(value)
	}
	participants, ok := mancodeSortedStringSet(projection["participants"])
	if !ok { return "", errors.New("metadata participants are not a valid unique set") }
	projection["participants"] = participants
	scope, ok := projection["implementationScope"].(map[string]any)
	if !ok { return "", errors.New("metadata implementationScope is invalid") }
	scopeDigestObject := map[string]any{}
	for _, field := range []string{"source", "include", "exclude", "modules"} {
		value, exists := scope[field]
		if !exists { return "", fmt.Errorf("missing implementationScope field %s", field) }
		if field != "source" {
			value, ok = mancodeSortedStringSet(value)
			if !ok { return "", fmt.Errorf("implementationScope %s is not a valid unique set", field) }
			scope[field] = value
		}
		scopeDigestObject[field] = value
	}
	computedScopeDigest, err := mancodeCanonicalDigest(scopeDigestObject)
	if err != nil || computedScopeDigest != mancodeString(scope["digest"]) {
		return "", errors.New("implementationScope digest does not match its normalized contents")
	}
	if parent, ok := projection["parent"].(map[string]any); ok {
		normalized, ok := mancodeSortedStringSet(parent["participants"])
		if !ok { return "", errors.New("parent snapshot participants are invalid") }
		parent["participants"] = normalized
		projection["parent"] = parent
	}
	return mancodeCanonicalDigest(projection)
}

func mancodeWorkflowLedgerDigest(role string, object map[string]any) (string, error) {
	var fields []string
	switch role {
	case "requirements":
		fields = mancodeWorkflowRequirementsDigestFields
	case "review":
		fields = append([]string(nil), mancodeWorkflowReviewDigestFields...)
		if _, exists := object["delivery"]; exists { fields = append(fields, "delivery") }
	case "verification":
		fields = append([]string(nil), mancodeWorkflowVerificationDigestFields...)
		if mancodeInteger(object["schemaVersion"]) == 2 { fields = append(fields, "execution") }
	default:
		return "", errors.New("unknown Mancode workflow ledger role")
	}
	projection := make(map[string]any, len(fields))
	for _, field := range fields {
		value, exists := object[field]
		if !exists { return "", fmt.Errorf("missing %s digest field %s", role, field) }
		projection[field] = cloneMancodeJSON(value)
	}
	switch role {
	case "requirements":
		criteria, ok := projection["acceptanceCriteria"].([]any)
		if !ok { return "", errors.New("acceptanceCriteria is invalid") }
		for _, item := range criteria {
			criterion, ok := item.(map[string]any)
			if !ok { return "", errors.New("acceptance criterion is invalid") }
			ids, ok := mancodeSortedStringSet(criterion["requirementIds"])
			if !ok { return "", errors.New("acceptance criterion requirementIds are invalid") }
			criterion["requirementIds"] = ids
		}
	case "review":
		required, ok := mancodeSortedStringSet(projection["requiredDomains"])
		if !ok { return "", errors.New("review requiredDomains are invalid") }
		projection["requiredDomains"] = required
	}
	return mancodeCanonicalDigest(projection)
}

func cloneMancodeJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(typed))
		for key, item := range typed { copy[key] = cloneMancodeJSON(item) }
		return copy
	case []any:
		copy := make([]any, len(typed))
		for i, item := range typed { copy[i] = cloneMancodeJSON(item) }
		return copy
	default:
		return typed
	}
}

func mancodeSortedStringSet(value any) ([]any, bool) {
	items, ok := value.([]any)
	if !ok { return nil, false }
	normalized := make([]string, len(items))
	for i, item := range items {
		value, ok := item.(string)
		if !ok || strings.TrimSpace(value) == "" { return nil, false }
		normalized[i] = value
	}
	sort.Strings(normalized)
	result := make([]any, len(normalized))
	for i, item := range normalized {
		if i > 0 && item == normalized[i-1] { return nil, false }
		result[i] = item
	}
	return result, true
}

func mancodeCanonicalDigest(value any) (string, error) {
	encoded, err := mancodeCanonicalJSON(value)
	if err != nil { return "", err }
	hash := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func mancodeCanonicalJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	if err := appendMancodeCanonicalValue(&output, value); err != nil { return nil, err }
	return output.Bytes(), nil
}

func appendMancodeCanonicalValue(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if typed { output.WriteString("true") } else { output.WriteString("false") }
	case string:
		return appendMancodeCanonicalString(output, typed)
	case json.Number:
		number, err := strconv.ParseFloat(string(typed), 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
			math.Abs(number) > 9007199254740991 || (number == 0 && strings.HasPrefix(string(typed), "-")) {
			return errors.New("Mancode canonical number is not a safe integer")
		}
		output.WriteString(strconv.FormatInt(int64(number), 10))
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || math.Abs(typed) > 9007199254740991 ||
			(typed == 0 && math.Signbit(typed)) { return errors.New("Mancode canonical number is not a safe integer") }
		output.WriteString(strconv.FormatInt(int64(typed), 10))
	case int:
		if math.Abs(float64(typed)) > 9007199254740991 { return errors.New("Mancode canonical number is not a safe integer") }
		output.WriteString(strconv.FormatInt(int64(typed), 10))
	case int64:
		if math.Abs(float64(typed)) > 9007199254740991 { return errors.New("Mancode canonical number is not a safe integer") }
		output.WriteString(strconv.FormatInt(typed, 10))
	case []any:
		output.WriteByte('[')
		for i, item := range typed {
			if i > 0 { output.WriteByte(',') }
			if err := appendMancodeCanonicalValue(output, item); err != nil { return err }
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed { keys = append(keys, key) }
		sort.Slice(keys, func(i, j int) bool { return mancodeUTF16Less(keys[i], keys[j]) })
		output.WriteByte('{')
		for i, key := range keys {
			if i > 0 { output.WriteByte(',') }
			if err := appendMancodeCanonicalString(output, key); err != nil { return err }
			output.WriteByte(':')
			if err := appendMancodeCanonicalValue(output, typed[key]); err != nil { return err }
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unsupported Mancode canonical JSON value %T", value)
	}
	return nil
}

func appendMancodeCanonicalString(output *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) { return errors.New("Mancode canonical string is invalid") }
	output.WriteByte('"')
	for _, char := range value {
		switch char {
		case '"': output.WriteString("\\\"")
		case '\\': output.WriteString("\\\\")
		case '\b': output.WriteString("\\b")
		case '\t': output.WriteString("\\t")
		case '\n': output.WriteString("\\n")
		case '\f': output.WriteString("\\f")
		case '\r': output.WriteString("\\r")
		default:
			if char < 0x20 {
				output.WriteString("\\u00")
				output.WriteByte("0123456789abcdef"[byte(char)>>4])
				output.WriteByte("0123456789abcdef"[byte(char)&0x0f])
			} else { output.WriteRune(char) }
		}
	}
	output.WriteByte('"')
	return nil
}

func mancodeUTF16Less(left, right string) bool {
	a, b := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	for i := 0; i < len(a) && i < len(b); i++ { if a[i] != b[i] { return a[i] < b[i] } }
	return len(a) < len(b)
}

func mancodeTaskRefFromValue(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(object, []string{"namespace", "taskId"}) { return nil, false }
	namespace, namespaceOK := object["namespace"].(string)
	taskID, taskOK := object["taskId"].(string)
	if !namespaceOK || (namespace != "local" && namespace != "shared") || !taskOK || !mancodeOperationIDPattern.MatchString(taskID) { return nil, false }
	return map[string]any{"namespace": namespace, "taskId": taskID}, true
}

func mancodeInteger(value any) int64 {
	integer, ok := mancodeIntegerValue(value)
	if !ok { return math.MinInt64 }
	return integer
}

func mancodeIntegerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := strconv.ParseFloat(string(typed), 64)
		if err != nil || math.Trunc(number) != number || math.Abs(number) > 9007199254740991 ||
			(number == 0 && strings.HasPrefix(string(typed), "-")) { return 0, false }
		return int64(number), true
	case float64:
		if math.Trunc(typed) != typed || math.Abs(typed) > 9007199254740991 || (typed == 0 && math.Signbit(typed)) { return 0, false }
		return int64(typed), true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	default:
		return 0, false
	}
}

func mancodePositiveInteger(value any) (int64, bool) {
	integer, ok := mancodeIntegerValue(value)
	return integer, ok && integer > 0
}
func mancodeNonNegativeInteger(value any) (int64, bool) {
	integer, ok := mancodeIntegerValue(value)
	return integer, ok && integer >= 0
}
func mancodeNullableInteger(value any) (int64, bool) {
	if value == nil { return 0, true }
	return mancodeIntegerValue(value)
}
func mancodeString(value any) string { result, _ := value.(string); return result }
func mancodeNonEmptyString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}
func mancodeStringOrNull(value any) bool {
	return value == nil || mancodeNonEmptyString(value)
}
func mancodeHasString(values []string, expected string) bool {
	for _, value := range values { if value == expected { return true } }
	return false
}
func mancodeHasInt64(values []int64, expected int64) bool {
	for _, value := range values { if value == expected { return true } }
	return false
}
func containsString(values []string, expected string) bool { return mancodeHasString(values, expected) }
func containsInt64(values []int64, expected int64) bool { return mancodeHasInt64(values, expected) }
func mancodeStringList(value any) bool {
	items, ok := value.([]any)
	if !ok { return false }
	seen := map[string]bool{}
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" || seen[text] { return false }
		seen[text] = true
	}
	return true
}
func mancodeULIDSet(value any) bool {
	items, ok := mancodeSortedStringSet(value)
	if !ok { return false }
	for _, item := range items { if !mancodeOperationIDPattern.MatchString(mancodeString(item)) { return false } }
	return true
}
func mancodeNullableULID(value any) bool {
	return value == nil || mancodeOperationIDPattern.MatchString(mancodeString(value))
}
func mancodeRFC3339(value any) bool {
	text, ok := value.(string)
	if !ok { return false }
	_, err := time.Parse(time.RFC3339Nano, text)
	return err == nil
}
func mancodeItemIdentity(value map[string]any) bool {
	if !mancodeNonEmptyString(value["displayId"]) { return false }
	return value["legacyId"] == nil || mancodeNonEmptyString(value["legacyId"])
}
func mancodeValidLegacySource(role string, value any) bool {
	if value == nil { return true }
	object, ok := value.(map[string]any)
	if !ok { return false }
	keys := []string{"sourceSchema", "sourceDigest", "fieldMapVersion"}
	schema := ""
	switch role {
	case "requirements": schema = "requirements-v1"
	case "review":
		schema = "review-ledger-1.0"
		keys = append(keys, "sourceRequirementsDigest")
	case "verification":
		schema = "verification-v1"
		keys = append(keys, "sourceRequirementsDigest")
	default: return false
	}
	if !mancodeHasOnlyKeys(object, keys) || !mancodeHasAllKeys(object, keys) ||
		mancodeString(object["sourceSchema"]) != schema ||
		!validMancodeDigest(mancodeString(object["sourceDigest"])) ||
		mancodeInteger(object["fieldMapVersion"]) != 1 { return false }
	if role == "review" && object["sourceRequirementsDigest"] != nil &&
		!validMancodeDigest(mancodeString(object["sourceRequirementsDigest"])) { return false }
	if role == "verification" && !validMancodeDigest(mancodeString(object["sourceRequirementsDigest"])) { return false }
	return true
}
func mancodeVerificationEvidence(value any, kind string) bool {
	if value == nil { return true }
	object, ok := value.(map[string]any)
	allowed := []string{"evidenceId", "status", "summary", "command", "exitCode", "artifactRef", "confirmedByActorId", "confirmationSource", "updatedAt", "subject", "surface"}
	required := []string{"evidenceId", "status", "summary", "command", "exitCode", "artifactRef", "confirmedByActorId", "confirmationSource", "updatedAt"}
	if !ok || !mancodeHasOnlyKeys(object, allowed) || !mancodeHasAllKeys(object, required) ||
		!mancodeOperationIDPattern.MatchString(mancodeString(object["evidenceId"])) ||
		!mancodeHasString([]string{"pending", "passed", "failed", "manual_required", "blocked"}, mancodeString(object["status"])) ||
		!mancodeStringOrNull(object["summary"]) || !mancodeStringOrNull(object["command"]) ||
		!mancodeNullableULID(object["confirmedByActorId"]) { return false }
		if kind == "automated" && mancodeString(object["status"]) == "manual_required" { return false }
	if object["exitCode"] != nil {
		if _, ok := mancodeIntegerValue(object["exitCode"]); !ok { return false }
	}
	if object["artifactRef"] != nil {
		if _, ok := object["artifactRef"].(map[string]any); !ok { return false }
	}
	source := object["confirmationSource"]
	if source != nil && source != "actor" && source != "legacy_migration" { return false }
	actorID := object["confirmedByActorId"]
	if (source == "actor") != (actorID != nil) || (source == "legacy_migration" && actorID != nil) { return false }
	if kind == "automated" && actorID != nil { return false }
	if kind == "manual" && object["status"] == "passed" && source == nil { return false }
	if kind == "manual" && object["status"] != "passed" && source != nil { return false }
	if object["updatedAt"] != nil && !mancodeRFC3339(object["updatedAt"]) { return false }
	for _, key := range []string{"subject", "surface"} {
		if object[key] != nil {
			if _, ok := object[key].(map[string]any); !ok { return false }
		}
	}
	return true
}
func validMancodeDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") { return false }
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && value == strings.ToLower(value)
}

func checkWorkflowCache(governance map[string]any, key, expected, issue string, add func(string, bool)) {
	if mancodeString(governance[key]) != expected || expected == "" { add(issue, true) }
}
func checkRequirementsStatus(governance, requirements map[string]any, ok bool, add func(string, bool)) {
	if !ok { return }
	expected := "needs_clarification"
	if mancodeString(requirements["status"]) == "confirmed" && mancodeRequirementsReady(requirements) { expected = "ready" }
	if mancodeString(governance["requirementsStatus"]) != expected { add("requirements_status_cache_mismatch", true) }
}

func mancodeRequirementsReady(requirements map[string]any) bool {
	if strings.TrimSpace(mancodeString(requirements["goal"])) == "" { return false }
	scope, _ := requirements["functionalScope"].(map[string]any)
	inScope, _ := scope["inScope"].([]any)
	if len(inScope) == 0 { return false }
	coverage, _ := requirements["coverage"].([]any)
	if len(coverage) != 7 { return false }
	dimensions := map[string]map[string]any{}
	for _, item := range coverage {
		entry, _ := item.(map[string]any)
		dimension := mancodeString(entry["dimension"])
		if dimension == "" || dimensions[dimension] != nil { return false }
		dimensions[dimension] = entry
	}
	for _, dimension := range []string{"platform", "core_scope", "technical_stack", "data_and_persistence", "performance", "compatibility", "security"} {
		if dimensions[dimension] == nil { return false }
	}
	decisions, _ := requirements["technicalDecisions"].([]any)
	if mancodeString(dimensions["technical_stack"]["status"]) != "not_applicable" && len(decisions) == 0 { return false }
	criteria, _ := requirements["acceptanceCriteria"].([]any)
	if len(criteria) == 0 { return false }
	hasRequired := false
	for _, item := range criteria { entry, _ := item.(map[string]any); hasRequired = hasRequired || entry["required"] == true }
	if !hasRequired { return false }
	unknowns, _ := requirements["blockingUnknowns"].([]any)
	for _, item := range unknowns { entry, _ := item.(map[string]any); if mancodeString(entry["status"]) == "open" { return false } }
	return true
}

func checkReviewContext(review map[string]any, ok bool, requirementsDigest string, planVersion int64, add func(string, bool)) {
	if !ok { return }
	digestValue := mancodeString(review["requirementsDigest"])
	version, versionOK := mancodeNullableInteger(review["planVersion"])
	legacyGap := review["legacySource"] != nil && (digestValue == "" || version == 0)
	if legacyGap { return }
	stale := digestValue != requirementsDigest || !versionOK || version != planVersion
	if stale {
		if mancodeString(review["status"]) != "stale" { add("review_context_mismatch", true) }
		return
	}
	status := mancodeString(review["status"])
	expected := "in_review"
	if review["skip"] != nil {
		expected = "skipped"
	} else {
		domains, _ := review["domains"].([]any)
		blockers, _ := review["blockers"].([]any)
		blocked := false
		for _, item := range blockers { blocker, _ := item.(map[string]any); blocked = blocked || mancodeString(blocker["status"]) == "open" }
		for _, item := range domains { domain, _ := item.(map[string]any); blocked = blocked || mancodeString(domain["status"]) == "blocked" }
		allPassed, allPending := true, true
		for _, item := range domains {
			domain, _ := item.(map[string]any)
			allPassed = allPassed && mancodeString(domain["status"]) == "passed"
			allPending = allPending && mancodeString(domain["status"]) == "pending"
		}
		switch {
		case blocked: expected = "blocked"
		case allPassed: expected = "passed"
		case allPending: expected = "pending"
		}
	}
	if status != expected { add("review_status_inconsistent", true) }
}

func checkVerificationContext(verification map[string]any, verificationOK bool, requirements map[string]any, requirementsOK bool, planVersion int64, review map[string]any, reviewOK bool, add func(string, bool)) {
	if !verificationOK || !requirementsOK || !reviewOK { return }
	remediation, remediationOK := mancodeNonNegativeInteger(review["remediationRound"])
	stale := !remediationOK || mancodeString(verification["requirementsDigest"]) != mancodeString(requirements["contentDigest"]) ||
		mancodeInteger(verification["planVersion"]) != planVersion || mancodeInteger(verification["remediationRound"]) != remediation
	if stale {
		if mancodeString(verification["status"]) != "stale" { add("verification_context_mismatch", true) }
		return
	}
	if mancodeString(verification["status"]) == "stale" {
		add("verification_status_inconsistent", true)
		return
	}
	criteria, _ := requirements["acceptanceCriteria"].([]any)
	checks, _ := verification["checks"].([]any)
	if mancodeString(verification["requirementsDigest"]) != mancodeString(requirements["contentDigest"]) {
		add("verification_context_mismatch", true)
	}
	if len(criteria) != len(checks) { add("verification_criteria_mismatch", true); return }
	criteriaByID := make(map[string]map[string]any, len(criteria))
	for _, item := range criteria {
		criterion, _ := item.(map[string]any)
		id := mancodeString(criterion["criterionId"])
		if id == "" || criteriaByID[id] != nil { add("invalid_acceptance_criteria_identity", false); return }
		criteriaByID[id] = criterion
	}
	for _, item := range checks {
		check, _ := item.(map[string]any)
		id := mancodeString(check["criterionId"])
		criterion := criteriaByID[id]
		if criterion == nil || check["displayId"] != criterion["displayId"] || check["legacyId"] != criterion["legacyId"] ||
			check["required"] != criterion["required"] || check["verificationRequirement"] != criterion["verificationRequirement"] {
			add("verification_criteria_mismatch", true); return
		}
		delete(criteriaByID, id)
	}
	if len(criteriaByID) != 0 { add("verification_criteria_mismatch", true) }
	statuses := []string{}
	for _, item := range checks {
		check, _ := item.(map[string]any)
		if check["required"] != true { continue }
		for _, key := range []string{"automated", "manual"} {
			evidence, exists := check[key]
			if !exists || evidence == nil { continue }
			entry, _ := evidence.(map[string]any)
			statuses = append(statuses, mancodeString(entry["status"]))
		}
	}
	expected := "pending"
	if len(statuses) > 0 {
		expected = "passed"
		for _, value := range statuses {
			switch value {
			case "blocked": expected = "blocked"
			case "manual_required": if expected != "blocked" { expected = "manual_required" }
			case "failed": if expected != "blocked" && expected != "manual_required" { expected = "failed" }
			case "pending": if expected != "blocked" && expected != "manual_required" && expected != "failed" { expected = "pending" }
			}
		}
	}
	if mancodeString(verification["status"]) != expected { add("verification_status_inconsistent", true) }
}

func validateMancodeWorkflowAggregate(record Record, entries map[string]FileEntry) error {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(record.Payload))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil { return errors.New("invalid Mancode workflow aggregate payload") }
	if value["schema_version"] != mancodeWorkflowAggregateSchema || value["authority"] != "local_pending_review" || value["activation_allowed"] != false {
		return errors.New("Mancode workflow aggregate is not inert")
	}
	status := mancodeString(value["status"])
	if status != "complete" && status != "incomplete" && status != "conflicting" { return errors.New("Mancode workflow aggregate status is invalid") }
	issues, ok := value["issues"].([]any)
	if !ok || (status == "complete") != (len(issues) == 0) { return errors.New("Mancode workflow aggregate issue state is inconsistent") }
	location, ok := mancodeWorkflowMetadataLocation(record.SourcePath, "")
	if !ok { location, ok = mancodeWorkflowMetadataLocation(record.SourcePath, ".mancode") }
	if !ok { return errors.New("Mancode workflow aggregate source path is invalid") }
	ref, ok := value["task_ref"].(map[string]any)
	if !ok || mancodeString(ref["namespace"]) != location.namespace || mancodeString(ref["task_id"]) != location.taskID { return errors.New("Mancode workflow aggregate task reference is inconsistent") }
	sources, ok := value["sources"].([]any)
	if !ok { return errors.New("Mancode workflow aggregate sources are invalid") }
	seenRoles, seenPaths := map[string]bool{}, map[string]bool{}
	for _, item := range sources {
		source, ok := item.(map[string]any)
		if !ok { return errors.New("Mancode workflow aggregate source entry is invalid") }
		role, sourcePath, sourceHash := mancodeString(source["role"]), mancodeString(source["path"]), mancodeString(source["source_sha256"])
		entry, exists := entries[sourcePath]
		if !exists || entry.SourceSHA256 != sourceHash || !validDigest(sourceHash) || seenRoles[role] || seenPaths[sourcePath] { return errors.New("Mancode workflow aggregate source reconciliation failed") }
		sourceLocation, sourceOK := mancodeWorkflowComponentLocation(sourcePath, "")
		if !sourceOK { sourceLocation, sourceOK = mancodeWorkflowComponentLocation(sourcePath, ".mancode") }
		if !sourceOK || sourceLocation.directory != location.directory || sourceLocation.namespace != location.namespace || sourceLocation.taskID != location.taskID {
			return errors.New("Mancode workflow aggregate source escaped its task directory")
		}
		seenRoles[role], seenPaths[sourcePath] = true, true
	}
	if status == "complete" {
		for _, role := range []string{"metadata", "requirements", "review", "verification"} { if !seenRoles[role] { return errors.New("complete Mancode workflow aggregate lacks a required source") } }
		if !validMancodeDigest(mancodeString(value["aggregate_digest"])) { return errors.New("complete Mancode workflow aggregate lacks its digest") }
	} else if value["aggregate_digest"] != nil {
		return errors.New("incomplete Mancode workflow aggregate must not claim an aggregate digest")
	}
	return nil
}

func validateMancodeWorkflowAggregateSet(records []Record, entries []FileEntry) error {
	entryByPath := make(map[string]FileEntry, len(entries))
	filesByPath := make(map[string]scannedFile, len(entries))
	for _, entry := range entries {
		entryByPath[entry.Path] = entry
		filesByPath[entry.Path] = scannedFile{entry: entry}
	}
	for _, record := range records {
		file, exists := filesByPath[record.SourcePath]
		if !exists {
			continue
		}
		file.records = append(file.records, record)
		filesByPath[record.SourcePath] = file
	}
	for _, record := range records {
		if record.Type != "mancode_workflow_aggregate" {
			continue
		}
		if err := validateMancodeWorkflowAggregate(record, entryByPath); err != nil {
			return err
		}
		location, ok := mancodeWorkflowMetadataLocation(record.SourcePath, "")
		if !ok {
			location, ok = mancodeWorkflowMetadataLocation(record.SourcePath, ".mancode")
		}
		if !ok {
			return errors.New("Mancode workflow aggregate metadata path is invalid")
		}
		metadataFile := filesByPath[record.SourcePath]
		metadata, ok := mancodeWorkflowRecordObject(metadataFile, "workflow_metadata")
		if !ok {
			return errors.New("Mancode workflow aggregate metadata record is missing")
		}
		reconstructed, err := reconstructMancodeWorkflowAggregate(location, metadataFile, metadata, filesByPath)
		if err != nil || !bytes.Equal(reconstructed, record.Payload) {
			return errors.New("Mancode workflow aggregate does not reconcile with staged component records")
		}
	}
	return nil
}
