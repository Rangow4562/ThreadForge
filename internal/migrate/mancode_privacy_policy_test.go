package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func mancodePrivacyPolicyFixture() map[string]any {
	exclusions := mancodePrivacyExclusionsFixture()
	canonical, _ := mancodeCanonicalJSON(exclusions)
	digest := sha256.Sum256(canonical)
	return map[string]any{
		"schemaVersion": 1, "revision": 3, "workspaceId": mancodeDecisionTestID("1"),
		"enabled": true, "rulesetVersion": "mancode-sensitive-text:1",
		"enabledRuleIds":  []any{"email-address", "pem-private-key"},
		"targets":         []any{"shared_write", "context_output"},
		"exclusions":      map[string]any{"revision": 2, "digest": "sha256:" + hex.EncodeToString(digest[:])},
		"lastOperationId": mancodeDecisionTestID("2"), "updatedAt": "2026-10-08T12:00:00Z",
	}
}

func mancodePrivacyExclusionsFixture() map[string]any {
	return map[string]any{
		"schemaVersion": 1, "revision": 2, "workspaceId": mancodeDecisionTestID("1"),
		"entries": []any{
			map[string]any{
				"kind": "confirmed_decision", "relativePath": "shared/memory/decisions/" + mancodeDecisionTestID("3") + ".json",
				"entityDigest": "sha256:" + strings.Repeat("b", 64),
			},
			map[string]any{
				"kind": "checkpoint", "relativePath": "shared/workflows/" + mancodeDecisionTestID("4") + "/checkpoints/" + mancodeDecisionTestID("5") + ".json",
				"entityDigest": "sha256:" + strings.Repeat("c", 64),
			},
		},
		"lastOperationId": mancodeDecisionTestID("2"), "updatedAt": "2026-10-08T12:00:00Z",
	}
}

func TestMancodePrivacyPolicyIsHistoricalAndNeverEffective(t *testing.T) {
	root := t.TempDir()
	writeMancodeJSONFixture(t, root, ".mancode/shared/context/privacy-policy.json", mancodePrivacyPolicyFixture())
	writeMancodeJSONFixture(t, root, ".mancode/shared/context/privacy-exclusions.json", mancodePrivacyExclusionsFixture())
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 2 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("privacy policy did not inspect as supported: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Mancode, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("unexpected privacy policy records: %#v", records)
	}
	var projection map[string]any
	for _, record := range records {
		if record.Type != "project_policy" || record.Scope != "local_pending_review" {
			t.Fatalf("privacy policy record escaped local review: %#v", record)
		}
		var candidate map[string]any
		if err := json.Unmarshal(record.Payload, &candidate); err != nil {
			t.Fatal(err)
		}
		if candidate["schema_version"] == mancodePrivacyPolicyImportSchema {
			projection = candidate
		}
	}
	if projection == nil {
		t.Fatalf("privacy policy record was not emitted: %#v", records)
	}
	canonicalExclusions, err := mancodeCanonicalJSON(mancodePrivacyExclusionsFixture())
	if err != nil {
		t.Fatal(err)
	}
	exclusionHash := sha256.Sum256(canonicalExclusions)
	expectedExclusionDigest := "sha256:" + hex.EncodeToString(exclusionHash[:])
	if projection["schema_version"] != mancodePrivacyPolicyImportSchema || projection["policy_kind"] != "privacy" ||
		projection["authority"] != "local_pending_review" || projection["human_review_required"] != true ||
		projection["execution_authorized"] != false || projection["source_enabled"] != true ||
		projection["exclusions_binding_verified"] != false || projection["ruleset_version"] != "mancode-sensitive-text:1" ||
		projection["exclusions_digest"] != expectedExclusionDigest {
		t.Fatalf("privacy policy was activated or lost its unverified historical reference: %s", records[0].Payload)
	}
	for _, private := range []string{mancodeDecisionTestID("1"), mancodeDecisionTestID("2"), "workspaceId", "lastOperationId"} {
		if strings.Contains(string(records[0].Payload), private) {
			t.Fatalf("privacy policy projection retained source identity %q: %s", private, records[0].Payload)
		}
	}
}

func TestMancodePrivacyExclusionsBecomePathFreeReconciliationSummary(t *testing.T) {
	root := t.TempDir()
	fixture := mancodePrivacyExclusionsFixture()
	writeMancodeJSONFixture(t, root, ".mancode/shared/context/privacy-exclusions.json", fixture)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("privacy exclusions did not inspect as supported: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Mancode, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "project_policy" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected privacy exclusions records: %#v", records)
	}
	var projection struct {
		SchemaVersion            string `json:"schema_version"`
		PolicyKind               string `json:"policy_kind"`
		Authority                string `json:"authority"`
		HumanReviewRequired      bool   `json:"human_review_required"`
		ExecutionAuthorized      bool   `json:"execution_authorized"`
		EntryCount               int    `json:"entry_count"`
		DecisionExclusionCount   int    `json:"decision_exclusion_count"`
		CheckpointExclusionCount int    `json:"checkpoint_exclusion_count"`
		SourceCanonicalDigest    string `json:"source_canonical_digest"`
	}
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	canonical, err := mancodeCanonicalJSON(fixture)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	expectedDigest := "sha256:" + hex.EncodeToString(digest[:])
	if projection.SchemaVersion != mancodePrivacyExclusionsImportSchema || projection.PolicyKind != "privacy_exclusions" ||
		projection.Authority != "local_pending_review" || !projection.HumanReviewRequired || projection.ExecutionAuthorized ||
		projection.EntryCount != 2 || projection.DecisionExclusionCount != 1 || projection.CheckpointExclusionCount != 1 ||
		projection.SourceCanonicalDigest != expectedDigest {
		t.Fatalf("privacy-exclusion summary lost counts/digest or became active: %s", records[0].Payload)
	}
	for _, private := range []string{mancodeDecisionTestID("1"), mancodeDecisionTestID("2"), mancodeDecisionTestID("3"), mancodeDecisionTestID("4"), mancodeDecisionTestID("5"), "relativePath", "entityDigest", "workspaceId", "lastOperationId"} {
		if strings.Contains(string(records[0].Payload), private) {
			t.Fatalf("privacy-exclusion summary retained source identity or path %q: %s", private, records[0].Payload)
		}
	}
}

func TestMancodePrivacyPolicyAndExclusionsRejectUnsupportedContracts(t *testing.T) {
	policyCases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown policy key", mutate: func(value map[string]any) { value["private"] = "value" }},
		{name: "invalid ULID alphabet", mutate: func(value map[string]any) {
			invalid := []rune(mancodeDecisionTestID("1"))
			invalid[10] = 'O'
			value["workspaceId"] = string(invalid)
		}},
		{name: "unknown rule", mutate: func(value map[string]any) { value["enabledRuleIds"] = []any{"invented-rule"} }},
		{name: "enabled with no rules", mutate: func(value map[string]any) { value["enabledRuleIds"] = []any{} }},
		{name: "unsupported target", mutate: func(value map[string]any) { value["targets"] = []any{"shared_write", "remote_forward"} }},
		{name: "invalid exclusions digest", mutate: func(value map[string]any) { value["exclusions"].(map[string]any)["digest"] = "not-a-digest" }},
	}
	for _, tc := range policyCases {
		t.Run("policy/"+tc.name, func(t *testing.T) {
			value := mancodePrivacyPolicyFixture()
			tc.mutate(value)
			_, handled, err := normalizeMancodePrivacyPolicy(value)
			if !handled || err == nil {
				t.Fatalf("invalid privacy policy was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
	exclusionCases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown exclusion key", mutate: func(value map[string]any) { value["private"] = "value" }},
		{name: "absolute path", mutate: func(value map[string]any) {
			value["entries"].([]any)[0].(map[string]any)["relativePath"] = "C:/private/decision.json"
		}},
		{name: "invalid path ULID alphabet", mutate: func(value map[string]any) {
			invalid := []rune(mancodeDecisionTestID("3"))
			invalid[10] = 'O'
			value["entries"].([]any)[0].(map[string]any)["relativePath"] = "shared/memory/decisions/" + string(invalid) + ".json"
		}},
		{name: "path-kind mismatch", mutate: func(value map[string]any) { value["entries"].([]any)[0].(map[string]any)["kind"] = "checkpoint" }},
		{name: "duplicate path", mutate: func(value map[string]any) {
			value["entries"].([]any)[1].(map[string]any)["relativePath"] = value["entries"].([]any)[0].(map[string]any)["relativePath"]
		}},
		{name: "invalid entity digest", mutate: func(value map[string]any) { value["entries"].([]any)[0].(map[string]any)["entityDigest"] = "bad" }},
	}
	for _, tc := range exclusionCases {
		t.Run("exclusions/"+tc.name, func(t *testing.T) {
			value := mancodePrivacyExclusionsFixture()
			tc.mutate(value)
			_, handled, err := normalizeMancodePrivacyExclusions(value)
			if !handled || err == nil {
				t.Fatalf("invalid privacy exclusions were accepted: handled=%v err=%v", handled, err)
			}
		})
	}
}
