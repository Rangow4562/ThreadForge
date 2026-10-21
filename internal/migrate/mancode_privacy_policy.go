package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
)

const mancodePrivacyPolicyImportSchema = "threadforge.mancode-privacy-policy.v1"
const mancodePrivacyPolicyImportSchemaV2 = "threadforge.mancode-privacy-policy.v2"
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
	SchemaVersion             string    `json:"schema_version"`
	PolicyKind                string    `json:"policy_kind"`
	Authority                 string    `json:"authority"`
	HumanReviewRequired       bool      `json:"human_review_required"`
	ExecutionAuthorized       bool      `json:"execution_authorized"`
	SourceEnabled             bool      `json:"source_enabled"`
	Revision                  int64     `json:"revision"`
	RulesetVersion            string    `json:"ruleset_version"`
	EnabledRuleIDs            []string  `json:"enabled_rule_ids"`
	Targets                   []string  `json:"targets"`
	ExclusionsRevision        int64     `json:"exclusions_revision"`
	ExclusionsDigest          string    `json:"exclusions_digest"`
	ExclusionsBindingVerified bool      `json:"exclusions_binding_verified"`
	ExclusionsBindingStatus   string    `json:"exclusions_binding_status,omitempty"`
	ExclusionsBindingIssues   *[]string `json:"exclusions_binding_issues,omitempty"`
	UpdatedAt                 string    `json:"updated_at"`
}

type mancodePrivacyPolicyBindingSource struct {
	WorkspaceID        string
	PolicyRevision     int64
	ExclusionsRevision int64
	ExclusionsDigest   string
}

type mancodePrivacyExclusionsBindingSource struct {
	WorkspaceID     string
	Revision        int64
	CanonicalDigest string
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
	return []normalizedItem{{
		Type: "project_policy", Payload: payload,
		privacyPolicy: &mancodePrivacyPolicyBindingSource{
			WorkspaceID: workspaceID, PolicyRevision: revision,
			ExclusionsRevision: exclusionsRevision, ExclusionsDigest: exclusionsDigest,
		},
	}}, true, nil
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
	return []normalizedItem{{
		Type: "project_policy", Payload: payload,
		privacyExclusions: &mancodePrivacyExclusionsBindingSource{
			WorkspaceID: workspaceID, Revision: revision, CanonicalDigest: canonicalDigest,
		},
	}}, true, nil
}

func isMancodePrivacyPolicyPath(relative string) bool {
	return strings.EqualFold(filepath.Base(relative), "privacy-policy.json") && classifyMancodeRecord(relative) == "project_policy"
}

func isMancodePrivacyExclusionsPath(relative string) bool {
	return strings.EqualFold(filepath.Base(relative), "privacy-exclusions.json") && classifyMancodeRecord(relative) == "project_policy"
}

func bindMancodePrivacyPolicyExclusions(scanned *[]scannedFile) []migrationcoverage.CrossFileCheck {
	type pairFiles struct {
		policies   []int
		exclusions []int
	}
	pairs := make(map[string]*pairFiles)
	for index := range *scanned {
		file := &(*scanned)[index]
		kind := ""
		switch {
		case isMancodePrivacyPolicyPath(file.entry.Path):
			kind = "policy"
		case isMancodePrivacyExclusionsPath(file.entry.Path):
			kind = "exclusions"
		default:
			continue
		}
		directory := strings.ToLower(filepath.ToSlash(filepath.Dir(filepath.Clean(file.entry.Path))))
		pair := pairs[directory]
		if pair == nil {
			pair = &pairFiles{}
			pairs[directory] = pair
		}
		if kind == "policy" {
			pair.policies = append(pair.policies, index)
		} else {
			pair.exclusions = append(pair.exclusions, index)
		}
	}
	directories := make([]string, 0, len(pairs))
	for directory := range pairs {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	checks := make([]migrationcoverage.CrossFileCheck, 0, len(directories))
	for _, directory := range directories {
		pair := pairs[directory]
		check := migrationcoverage.CrossFileCheck{
			SchemaVersion:   migrationcoverage.MancodePrivacyBindingSchemaVersion,
			Relationship:    "mancode_privacy_policy_exclusions",
			PairIDSHA256:    mancodePrivacyPairID(directory),
			PolicyFileCount: len(pair.policies), ExclusionsFileCount: len(pair.exclusions),
			PolicyPresent: len(pair.policies) > 0, ExclusionsPresent: len(pair.exclusions) > 0,
			Authority: "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
			Issues: []string{},
		}
		if len(pair.policies) > 0 {
			check.PolicySourceSetSHA256 = mancodePrivacySourceSetDigest(*scanned, pair.policies)
		}
		if len(pair.exclusions) > 0 {
			check.ExclusionsSourceSetSHA256 = mancodePrivacySourceSetDigest(*scanned, pair.exclusions)
		}
		if len(pair.policies) == 0 {
			check.Issues = append(check.Issues, "policy_missing")
		} else if len(pair.policies) > 1 {
			check.Issues = append(check.Issues, "policy_ambiguous")
		} else {
			file := &(*scanned)[pair.policies[0]]
			check.PolicyValid = file.privacyPolicy != nil && file.entry.ImportedCount > 0
			if !check.PolicyValid {
				check.Issues = append(check.Issues, "policy_invalid")
			} else {
				check.PolicyRevision = file.privacyPolicy.PolicyRevision
				check.ExpectedExclusionsRevision = file.privacyPolicy.ExclusionsRevision
			}
		}
		if len(pair.exclusions) == 0 {
			check.Issues = append(check.Issues, "exclusions_missing")
		} else if len(pair.exclusions) > 1 {
			check.Issues = append(check.Issues, "exclusions_ambiguous")
		} else {
			file := &(*scanned)[pair.exclusions[0]]
			check.ExclusionsValid = file.privacyExclusions != nil && file.entry.ImportedCount > 0
			if !check.ExclusionsValid {
				check.Issues = append(check.Issues, "exclusions_invalid")
			} else {
				check.ActualExclusionsRevision = file.privacyExclusions.Revision
			}
		}
		if check.PolicyValid && check.ExclusionsValid {
			policy := (*scanned)[pair.policies[0]].privacyPolicy
			exclusions := (*scanned)[pair.exclusions[0]].privacyExclusions
			check.WorkspaceBindingVerified = policy.WorkspaceID == exclusions.WorkspaceID
			check.RevisionBindingVerified = policy.ExclusionsRevision == exclusions.Revision
			check.DigestBindingVerified = policy.ExclusionsDigest == exclusions.CanonicalDigest
			if !check.WorkspaceBindingVerified {
				check.Issues = append(check.Issues, "workspace_mismatch")
			}
			if !check.RevisionBindingVerified {
				check.Issues = append(check.Issues, "revision_mismatch")
			}
			if !check.DigestBindingVerified {
				check.Issues = append(check.Issues, "digest_mismatch")
			}
		}
		sort.Strings(check.Issues)
		switch {
		case len(check.Issues) == 0:
			check.Status = "verified"
		case hasMancodePrivacyBindingMismatch(check.Issues):
			check.Status = "conflicting"
		default:
			check.Status = "incomplete"
		}
		for _, index := range pair.policies {
			upgradeMancodePrivacyPolicyBinding(&(*scanned)[index], check)
		}
		checks = append(checks, check)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].PairIDSHA256 < checks[j].PairIDSHA256 })
	return checks
}

func mancodePrivacySourceSetDigest(scanned []scannedFile, indices []int) string {
	hashes := make([]string, 0, len(indices))
	for _, index := range indices {
		hashes = append(hashes, scanned[index].entry.SourceSHA256)
	}
	return mancodePrivacySourceHashSetDigest(hashes)
}

func mancodePrivacySourceHashSetDigest(hashes []string) string {
	hashes = append([]string(nil), hashes...)
	sort.Strings(hashes)
	encoded, _ := json.Marshal(hashes)
	return digest(encoded)
}

func mancodePrivacyPairID(directory string) string {
	return digest([]byte("threadforge/mancode/privacy-binding/v1\x00" + strings.ToLower(filepath.ToSlash(directory))))
}

type mancodePrivacyPairSourceFiles struct {
	policies   []FileEntry
	exclusions []FileEntry
}

func mancodePrivacyPairFiles(files []FileEntry) map[string]*mancodePrivacyPairSourceFiles {
	pairs := make(map[string]*mancodePrivacyPairSourceFiles)
	for _, file := range files {
		kind := ""
		switch {
		case isMancodePrivacyPolicyPath(file.Path):
			kind = "policy"
		case isMancodePrivacyExclusionsPath(file.Path):
			kind = "exclusions"
		default:
			continue
		}
		directory := strings.ToLower(filepath.ToSlash(filepath.Dir(filepath.Clean(file.Path))))
		pair := pairs[directory]
		if pair == nil {
			pair = &mancodePrivacyPairSourceFiles{}
			pairs[directory] = pair
		}
		if kind == "policy" {
			pair.policies = append(pair.policies, file)
		} else {
			pair.exclusions = append(pair.exclusions, file)
		}
	}
	return pairs
}

// validateMancodePrivacyManifestChecks binds every newly generated check back
// to the exact source file set. Older manifests without checks remain readable;
// BuildFormatCoverage marks their privacy pairs legacy_unverified and cutover
// rejects them.
func validateMancodePrivacyManifestChecks(manifest Manifest) error {
	if manifest.Product != Mancode || len(manifest.CrossFileChecks) == 0 {
		return nil
	}
	pairs := mancodePrivacyPairFiles(manifest.Files)
	if len(pairs) != len(manifest.CrossFileChecks) {
		return errors.New("Mancode privacy binding checks do not cover every policy/exclusions source directory")
	}
	checksByPairID := make(map[string]migrationcoverage.CrossFileCheck, len(manifest.CrossFileChecks))
	for _, check := range manifest.CrossFileChecks {
		checksByPairID[check.PairIDSHA256] = check
	}
	for directory, pair := range pairs {
		check, ok := checksByPairID[mancodePrivacyPairID(directory)]
		if !ok {
			return errors.New("Mancode privacy binding check is missing for a source directory")
		}
		policyHashes := make([]string, 0, len(pair.policies))
		for _, file := range pair.policies {
			policyHashes = append(policyHashes, file.SourceSHA256)
		}
		exclusionsHashes := make([]string, 0, len(pair.exclusions))
		for _, file := range pair.exclusions {
			exclusionsHashes = append(exclusionsHashes, file.SourceSHA256)
		}
		if check.PolicyFileCount != len(pair.policies) || check.ExclusionsFileCount != len(pair.exclusions) ||
			check.PolicyPresent != (len(pair.policies) > 0) || check.ExclusionsPresent != (len(pair.exclusions) > 0) ||
			check.PolicyPresent && check.PolicySourceSetSHA256 != mancodePrivacySourceHashSetDigest(policyHashes) ||
			check.ExclusionsPresent && check.ExclusionsSourceSetSHA256 != mancodePrivacySourceHashSetDigest(exclusionsHashes) {
			return errors.New("Mancode privacy binding source commitment does not match manifest file inventory")
		}
		if check.PolicyValid != (len(pair.policies) == 1 && pair.policies[0].ImportedCount == 1) ||
			check.ExclusionsValid != (len(pair.exclusions) == 1 && pair.exclusions[0].ImportedCount == 1) {
			return errors.New("Mancode privacy binding validity does not match manifest record counts")
		}
	}
	return nil
}

func legacyMancodePrivacyBindingChecks(files []FileEntry) []migrationcoverage.CrossFileCheck {
	pairs := mancodePrivacyPairFiles(files)
	directories := make([]string, 0, len(pairs))
	for directory := range pairs {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	checks := make([]migrationcoverage.CrossFileCheck, 0, len(directories))
	for _, directory := range directories {
		pair := pairs[directory]
		check := migrationcoverage.CrossFileCheck{
			SchemaVersion:   migrationcoverage.MancodePrivacyBindingSchemaVersion,
			Relationship:    "mancode_privacy_policy_exclusions",
			PairIDSHA256:    mancodePrivacyPairID(directory),
			PolicyFileCount: len(pair.policies), ExclusionsFileCount: len(pair.exclusions),
			PolicyPresent: len(pair.policies) > 0, ExclusionsPresent: len(pair.exclusions) > 0,
			Authority: "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
			Status: "incomplete", Issues: []string{"legacy_unverified"},
		}
		if check.PolicyPresent {
			hashes := make([]string, 0, len(pair.policies))
			for _, file := range pair.policies {
				hashes = append(hashes, file.SourceSHA256)
			}
			check.PolicySourceSetSHA256 = mancodePrivacySourceHashSetDigest(hashes)
		}
		if check.ExclusionsPresent {
			hashes := make([]string, 0, len(pair.exclusions))
			for _, file := range pair.exclusions {
				hashes = append(hashes, file.SourceSHA256)
			}
			check.ExclusionsSourceSetSHA256 = mancodePrivacySourceHashSetDigest(hashes)
		}
		checks = append(checks, check)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].PairIDSHA256 < checks[j].PairIDSHA256 })
	return checks
}

func hasMancodePrivacyBindingMismatch(issues []string) bool {
	for _, issue := range issues {
		if issue == "workspace_mismatch" || issue == "revision_mismatch" || issue == "digest_mismatch" {
			return true
		}
	}
	return false
}

func upgradeMancodePrivacyPolicyBinding(file *scannedFile, check migrationcoverage.CrossFileCheck) {
	for index := range file.records {
		record := &file.records[index]
		if record.Type != "project_policy" {
			continue
		}
		var policy mancodePrivacyPolicyImport
		if json.Unmarshal(record.Payload, &policy) != nil || policy.SchemaVersion != mancodePrivacyPolicyImportSchema {
			continue
		}
		policy.SchemaVersion = mancodePrivacyPolicyImportSchemaV2
		policy.ExclusionsBindingVerified = check.Status == "verified"
		policy.ExclusionsBindingStatus = check.Status
		issues := append([]string{}, check.Issues...)
		policy.ExclusionsBindingIssues = &issues
		payload, err := json.Marshal(policy)
		if err != nil {
			continue
		}
		record.Payload = payload
		record.ContentSHA256 = digest(payload)
	}
}
