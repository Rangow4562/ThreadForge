package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"
)

const mancodePrivacyPolicyImportSchema = "threadforge.mancode-privacy-policy.v1"
const mancodePrivacyExclusionsImportSchema = "threadforge.mancode-privacy-exclusions-summary.v1"

var mancodePrivacyExclusionDecisionPath = regexp.MustCompile(`^shared/memory/decisions/([0-7][0-9A-HJKMNPQRSTVWXYZ]{25})\.json$`)
var mancodePrivacyExclusionCheckpointPath = regexp.MustCompile(`^shared/workflows/([0-7][0-9A-HJKMNPQRSTVWXYZ]{25})/checkpoints/([0-7][0-9A-HJKMNPQRSTVWXYZ]{25})\.json$`)

var mancodePrivacyRuleIDs = map[string]bool{
	"pem-private-key": true, "authorization-header": true, "cookie-header": true,
	"named-secret": true, "absolute-local-path": true, "email-address": true,
	"vendor-api-key": true, "cloud-access-key": true, "connection-password": true,
	"bearer-token": true, "jwt-token": true, "cn-mobile-phone": true,
	"cn-identity-card-18": true, "cn-identity-card-15": true, "payment-card": true,
}

type mancodePrivacyPolicyImport struct {
	SchemaVersion             string   `json:"schema_version"`
	PolicyKind                string   `json:"policy_kind"`
	Authority                 string   `json:"authority"`
	HumanReviewRequired       bool     `json:"human_review_required"`
	ExecutionAuthorized       bool     `json:"execution_authorized"`
	SourceEnabled             bool     `json:"source_enabled"`
	Revision                  int64    `json:"revision"`
	RulesetVersion            string   `json:"ruleset_version"`
	EnabledRuleIDs            []string `json:"enabled_rule_ids"`
	Targets                   []string `json:"targets"`
	ExclusionsRevision        int64    `json:"exclusions_revision"`
	ExclusionsDigest          string   `json:"exclusions_digest"`
	ExclusionsBindingVerified bool     `json:"exclusions_binding_verified"`
	UpdatedAt                 string   `json:"updated_at"`
}

type mancodePrivacyExclusionsImport struct {
	SchemaVersion            string `json:"schema_version"`
	PolicyKind               string `json:"policy_kind"`
	Authority                string `json:"authority"`
	HumanReviewRequired      bool   `json:"human_review_required"`
	ExecutionAuthorized      bool   `json:"execution_authorized"`
	Revision                 int64  `json:"revision"`
	EntryCount               int    `json:"entry_count"`
	DecisionExclusionCount   int    `json:"decision_exclusion_count"`
	CheckpointExclusionCount int    `json:"checkpoint_exclusion_count"`
	SourceCanonicalDigest    string `json:"source_canonical_digest"`
	UpdatedAt                string `json:"updated_at"`
}

type mancodePrivacyExclusionEntry struct {
	Kind         string `json:"kind"`
	RelativePath string `json:"relativePath"`
	EntityDigest string `json:"entityDigest"`
}

func normalizeMancodePrivacyPolicy(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode privacy policy must be an object")
	}
	keys := []string{
		"schemaVersion", "revision", "workspaceId", "enabled", "rulesetVersion", "enabledRuleIds",
		"targets", "exclusions", "lastOperationId", "updatedAt",
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode privacy policy does not match the supported schema")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	revision, revisionOK := mancodeIntegerValue(object["revision"])
	enabled, enabledOK := object["enabled"].(bool)
	workspaceID := mancodeString(object["workspaceId"])
	operationID := mancodeString(object["lastOperationId"])
	rulesetVersion := mancodeString(object["rulesetVersion"])
	updatedAt := mancodeString(object["updatedAt"])
	if !versionOK || version != 1 || !revisionOK || revision < 1 || !enabledOK ||
		!mancodeOperationIDPattern.MatchString(workspaceID) || !mancodeOperationIDPattern.MatchString(operationID) ||
		rulesetVersion != "mancode-sensitive-text:1" {
		return nil, true, errors.New("Mancode privacy policy version, identity, or ruleset is unsupported")
	}
	ruleIDsValue, ok := object["enabledRuleIds"].([]any)
	if !ok || len(ruleIDsValue) > len(mancodePrivacyRuleIDs) {
		return nil, true, errors.New("Mancode privacy policy rule list is invalid")
	}
	ruleIDs := make([]string, 0, len(ruleIDsValue))
	seenRules := map[string]bool{}
	for _, value := range ruleIDsValue {
		id, isString := value.(string)
		if !isString || !mancodePrivacyRuleIDs[id] || seenRules[id] {
			return nil, true, errors.New("Mancode privacy policy contains an unsupported or duplicate rule")
		}
		seenRules[id] = true
		ruleIDs = append(ruleIDs, id)
	}
	if enabled && len(ruleIDs) == 0 {
		return nil, true, errors.New("enabled Mancode privacy policy has no rules")
	}
	sort.Strings(ruleIDs)
	targetsValue, ok := object["targets"].([]any)
	if !ok || len(targetsValue) != 2 || targetsValue[0] != "shared_write" || targetsValue[1] != "context_output" {
		return nil, true, errors.New("Mancode privacy policy targets are invalid")
	}
	exclusions, ok := object["exclusions"].(map[string]any)
	if !ok || !mancodeHasAllKeys(exclusions, []string{"revision", "digest"}) || !mancodeHasOnlyKeys(exclusions, []string{"revision", "digest"}) {
		return nil, true, errors.New("Mancode privacy exclusions reference is invalid")
	}
	exclusionsRevision, revisionOK := mancodeIntegerValue(exclusions["revision"])
	exclusionsDigest := mancodeString(exclusions["digest"])
	if !revisionOK || exclusionsRevision < 1 || !mancodeDecisionDigestPattern.MatchString(exclusionsDigest) {
		return nil, true, errors.New("Mancode privacy exclusions reference revision or digest is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, true, errors.New("Mancode privacy policy update time is invalid")
	}
	payload, err := json.Marshal(mancodePrivacyPolicyImport{
		SchemaVersion: mancodePrivacyPolicyImportSchema,
		PolicyKind:    "privacy",
		Authority:     "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		SourceEnabled: enabled, Revision: revision, RulesetVersion: rulesetVersion,
		EnabledRuleIDs: ruleIDs, Targets: []string{"shared_write", "context_output"},
		ExclusionsRevision: exclusionsRevision, ExclusionsDigest: exclusionsDigest,
		ExclusionsBindingVerified: false, UpdatedAt: updatedAt,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode privacy policy projection: %w", err)
	}
	return []normalizedItem{{Type: "project_policy", Payload: payload}}, true, nil
}

func normalizeMancodePrivacyExclusions(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode privacy exclusions must be an object")
	}
	keys := []string{"schemaVersion", "revision", "workspaceId", "entries", "lastOperationId", "updatedAt"}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode privacy exclusions do not match the supported schema")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	revision, revisionOK := mancodeIntegerValue(object["revision"])
	workspaceID := mancodeString(object["workspaceId"])
	operationID := mancodeString(object["lastOperationId"])
	updatedAt := mancodeString(object["updatedAt"])
	entriesValue, entriesOK := object["entries"].([]any)
	if !versionOK || version != 1 || !revisionOK || revision < 1 || !entriesOK || len(entriesValue) > 10000 ||
		!mancodeOperationIDPattern.MatchString(workspaceID) || !mancodeOperationIDPattern.MatchString(operationID) {
		return nil, true, errors.New("Mancode privacy exclusions version, revision, identity, or entry count is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, true, errors.New("Mancode privacy exclusions update time is invalid")
	}
	entries := make([]mancodePrivacyExclusionEntry, 0, len(entriesValue))
	seenPaths := map[string]bool{}
	decisionCount, checkpointCount := 0, 0
	for _, value := range entriesValue {
		entry, ok := value.(map[string]any)
		entryKeys := []string{"kind", "relativePath", "entityDigest"}
		if !ok || !mancodeHasAllKeys(entry, entryKeys) || !mancodeHasOnlyKeys(entry, entryKeys) {
			return nil, true, errors.New("Mancode privacy exclusion entry does not match the supported schema")
		}
		kind := mancodeString(entry["kind"])
		relativePath := mancodeString(entry["relativePath"])
		entityDigest := mancodeString(entry["entityDigest"])
		pathKind := ""
		switch {
		case mancodePrivacyExclusionDecisionPath.MatchString(relativePath):
			pathKind = "confirmed_decision"
		case mancodePrivacyExclusionCheckpointPath.MatchString(relativePath):
			pathKind = "checkpoint"
		}
		if pathKind == "" || kind != pathKind || !mancodeDecisionDigestPattern.MatchString(entityDigest) || seenPaths[relativePath] {
			return nil, true, errors.New("Mancode privacy exclusion path, kind, or digest is invalid")
		}
		seenPaths[relativePath] = true
		if kind == "confirmed_decision" {
			decisionCount++
		} else {
			checkpointCount++
		}
		entries = append(entries, mancodePrivacyExclusionEntry{Kind: kind, RelativePath: relativePath, EntityDigest: entityDigest})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].RelativePath < entries[j].RelativePath })
	canonicalEntries := make([]any, 0, len(entries))
	for _, entry := range entries {
		canonicalEntries = append(canonicalEntries, map[string]any{
			"kind": entry.Kind, "relativePath": entry.RelativePath, "entityDigest": entry.EntityDigest,
		})
	}
	canonicalSource, err := mancodeCanonicalJSON(map[string]any{
		"schemaVersion": version, "revision": revision, "workspaceId": workspaceID,
		"entries": canonicalEntries, "lastOperationId": operationID, "updatedAt": updatedAt,
	})
	if err != nil {
		return nil, true, fmt.Errorf("canonicalize Mancode privacy exclusions: %w", err)
	}
	sum := sha256.Sum256(canonicalSource)
	canonicalDigest := "sha256:" + hex.EncodeToString(sum[:])
	payload, err := json.Marshal(mancodePrivacyExclusionsImport{
		SchemaVersion: mancodePrivacyExclusionsImportSchema,
		PolicyKind:    "privacy_exclusions",
		Authority:     "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		Revision: revision, EntryCount: len(entries), DecisionExclusionCount: decisionCount,
		CheckpointExclusionCount: checkpointCount, SourceCanonicalDigest: canonicalDigest, UpdatedAt: updatedAt,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode privacy exclusions summary: %w", err)
	}
	return []normalizedItem{{Type: "project_policy", Payload: payload}}, true, nil
}
