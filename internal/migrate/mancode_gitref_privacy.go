package migrate

import (
	"encoding/json"
	"errors"
	"sort"
)

func normalizeMancodeGitRefPrivacySnapshot(value any, workspaceID string) ([]normalizedItem, map[string]bool, error) {
	snapshot, ok := value.(map[string]any)
	snapshotKeys := []string{"policy", "exclusions", "digest"}
	if !ok || !mancodeHasAllKeys(snapshot, snapshotKeys) || !mancodeHasOnlyKeys(snapshot, snapshotKeys) {
		return nil, nil, errors.New("Mancode git-ref privacy snapshot fields are unsupported")
	}
	policy, policyOK := snapshot["policy"].(map[string]any)
	exclusions, exclusionsOK := snapshot["exclusions"].(map[string]any)
	snapshotDigest, digestOK := snapshot["digest"].(string)
	if !policyOK || !exclusionsOK || !digestOK || !mancodeGitRefDigestPattern.MatchString(snapshotDigest) {
		return nil, nil, errors.New("Mancode git-ref privacy snapshot is incomplete")
	}
	policyItems, policyHandled, err := normalizeMancodePrivacyPolicy(policy)
	if err != nil || !policyHandled || len(policyItems) != 1 || policyItems[0].privacyPolicy == nil {
		return nil, nil, errors.New("Mancode git-ref privacy policy does not match its supported v1 contract")
	}
	exclusionItems, exclusionsHandled, err := normalizeMancodePrivacyExclusions(exclusions)
	if err != nil || !exclusionsHandled || len(exclusionItems) != 1 || exclusionItems[0].privacyExclusions == nil {
		return nil, nil, errors.New("Mancode git-ref privacy exclusions do not match their supported v1 contract")
	}
	policyBinding := policyItems[0].privacyPolicy
	exclusionBinding := exclusionItems[0].privacyExclusions
	if policyBinding.WorkspaceID != workspaceID || exclusionBinding.WorkspaceID != workspaceID ||
		policyBinding.WorkspaceID != exclusionBinding.WorkspaceID ||
		policyBinding.ExclusionsRevision != exclusionBinding.Revision ||
		policyBinding.ExclusionsDigest != exclusionBinding.CanonicalDigest {
		return nil, nil, errors.New("Mancode git-ref privacy policy and exclusions bindings conflict")
	}
	policyDigest, err := mancodeGitRefPrivacyPolicyDigest(policy)
	if err != nil || policyDigest != snapshotDigest {
		return nil, nil, errors.New("Mancode git-ref privacy policy digest does not match its normalized policy")
	}

	var projection mancodePrivacyPolicyImport
	if err := json.Unmarshal(policyItems[0].Payload, &projection); err != nil {
		return nil, nil, errors.New("Mancode git-ref privacy policy projection could not be decoded")
	}
	projection.SchemaVersion = mancodePrivacyPolicyImportSchemaV2
	projection.ExclusionsBindingVerified = true
	projection.ExclusionsBindingStatus = "verified"
	issues := []string{}
	projection.ExclusionsBindingIssues = &issues
	policyPayload, err := json.Marshal(projection)
	if err != nil {
		return nil, nil, errors.New("Mancode git-ref privacy policy projection could not be encoded")
	}
	policyItems[0].Payload = policyPayload
	blockedDigests := make(map[string]bool)
	for _, value := range exclusions["entries"].([]any) {
		entry := value.(map[string]any)
		blockedDigests[mancodeString(entry["entityDigest"])] = true
	}
	return append(policyItems, exclusionItems...), blockedDigests, nil
}

func mancodeGitRefPrivacyPolicyDigest(policy map[string]any) (string, error) {
	keys := []string{
		"schemaVersion", "revision", "workspaceId", "enabled", "rulesetVersion", "enabledRuleIds",
		"targets", "exclusions", "lastOperationId", "updatedAt",
	}
	if !mancodeHasAllKeys(policy, keys) || !mancodeHasOnlyKeys(policy, keys) {
		return "", errors.New("Mancode privacy policy fields are unsupported")
	}
	ruleIDs, ok := policy["enabledRuleIds"].([]any)
	if !ok {
		return "", errors.New("Mancode privacy policy rule list is invalid")
	}
	ruleStrings := make([]string, len(ruleIDs))
	for i, value := range ruleIDs {
		text, ok := value.(string)
		if !ok {
			return "", errors.New("Mancode privacy policy rule identifier is invalid")
		}
		ruleStrings[i] = text
	}
	sort.Strings(ruleStrings)
	normalizedRules := make([]any, len(ruleStrings))
	for i := range ruleStrings {
		normalizedRules[i] = ruleStrings[i]
	}
	canonicalPolicy := map[string]any{
		"schemaVersion": policy["schemaVersion"], "revision": policy["revision"], "workspaceId": policy["workspaceId"],
		"enabled": policy["enabled"], "rulesetVersion": policy["rulesetVersion"], "enabledRuleIds": normalizedRules,
		"targets": policy["targets"], "exclusions": policy["exclusions"],
		"lastOperationId": policy["lastOperationId"], "updatedAt": policy["updatedAt"],
	}
	return mancodeCanonicalDigest(canonicalPolicy)
}

func mancodeGitRefPrivacyExcluded(value any, excludedDigests map[string]bool) bool {
	if len(excludedDigests) == 0 {
		return false
	}
	digest, err := mancodeCanonicalDigest(value)
	return err == nil && excludedDigests[digest]
}
