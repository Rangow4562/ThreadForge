package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
)

const mancodeTeamPolicyImportSchema = "threadforge.mancode-team-policy.v1"

type mancodeTeamPolicyImport struct {
	SchemaVersion            string `json:"schema_version"`
	PolicyKind               string `json:"policy_kind"`
	Authority                string `json:"authority"`
	HumanReviewRequired      bool   `json:"human_review_required"`
	ExecutionAuthorized      bool   `json:"execution_authorized"`
	WorkspaceBindingVerified bool   `json:"workspace_binding_verified"`
	Revision                 int64  `json:"revision"`
	SourcePolicy             string `json:"source_policy"`
	RecentDays               int64  `json:"recent_days"`
	DefaultVisibility        string `json:"default_visibility"`
	ShareConfirmedDecisions  bool   `json:"share_confirmed_decisions"`
	Retention                struct {
		LocalRawArtifactDays int64 `json:"local_raw_artifact_days"`
		LocalCacheDays       int64 `json:"local_cache_days"`
		CompletedSessionDays int64 `json:"completed_session_days"`
	} `json:"retention"`
	UpdatedAt string `json:"updated_at"`
}

func normalizeMancodeTeamPolicy(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode team policy must be an object")
	}
	keys := []string{
		"schemaVersion", "revision", "workspaceId", "policy", "recentDays", "defaultVisibility",
		"shareConfirmedDecisions", "retention", "lastOperationId", "updatedAt",
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode team policy does not match the supported schema")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	revision, revisionOK := mancodeIntegerValue(object["revision"])
	workspaceID := mancodeString(object["workspaceId"])
	operationValue := object["lastOperationId"]
	policy := mancodeString(object["policy"])
	recentDays, recentOK := mancodeIntegerValue(object["recentDays"])
	visibility := mancodeString(object["defaultVisibility"])
	shareConfirmed, shareOK := object["shareConfirmedDecisions"].(bool)
	updatedAt := mancodeString(object["updatedAt"])
	if !versionOK || version != 1 || !revisionOK || revision < 1 ||
		!mancodeOperationIDPattern.MatchString(workspaceID) || !mancodeNullableULID(operationValue) ||
		!oneOf(policy, "on", "off", "auto") || !recentOK || recentDays < 0 ||
		!oneOf(visibility, "local", "shared") || !shareOK || !mancodeRFC3339(updatedAt) {
		return nil, true, errors.New("Mancode team policy version or fields are invalid")
	}
	retention, ok := object["retention"].(map[string]any)
	retentionKeys := []string{"localRawArtifactDays", "localCacheDays", "completedSessionDays"}
	if !ok || !mancodeHasAllKeys(retention, retentionKeys) || !mancodeHasOnlyKeys(retention, retentionKeys) {
		return nil, true, errors.New("Mancode team policy retention does not match the supported schema")
	}
	localRawDays, rawOK := mancodeIntegerValue(retention["localRawArtifactDays"])
	localCacheDays, cacheOK := mancodeIntegerValue(retention["localCacheDays"])
	completedDays, completedOK := mancodeIntegerValue(retention["completedSessionDays"])
	if !rawOK || localRawDays < 0 || !cacheOK || localCacheDays < 0 || !completedOK || completedDays < 0 {
		return nil, true, errors.New("Mancode team policy retention values are invalid")
	}
	payload := mancodeTeamPolicyImport{
		SchemaVersion:            mancodeTeamPolicyImportSchema,
		PolicyKind:               "team_sharing",
		Authority:                "local_pending_review",
		HumanReviewRequired:      true,
		ExecutionAuthorized:      false,
		WorkspaceBindingVerified: false,
		Revision:                 revision,
		SourcePolicy:             policy,
		RecentDays:               recentDays,
		DefaultVisibility:        visibility,
		ShareConfirmedDecisions:  shareConfirmed,
		UpdatedAt:                updatedAt,
	}
	payload.Retention.LocalRawArtifactDays = localRawDays
	payload.Retention.LocalCacheDays = localCacheDays
	payload.Retention.CompletedSessionDays = completedDays
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode team policy projection: %w", err)
	}
	return []normalizedItem{{Type: "mancode_team_policy", Payload: encoded}}, true, nil
}
