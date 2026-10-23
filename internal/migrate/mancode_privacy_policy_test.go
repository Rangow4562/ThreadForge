package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
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
		if candidate["schema_version"] == mancodePrivacyPolicyImportSchemaV2 {
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
	issues, issuesOK := projection["exclusions_binding_issues"].([]any)
	if projection["schema_version"] != mancodePrivacyPolicyImportSchemaV2 || projection["policy_kind"] != "privacy" ||
		projection["authority"] != "local_pending_review" || projection["human_review_required"] != true ||
		projection["execution_authorized"] != false || projection["source_enabled"] != true ||
		projection["exclusions_binding_verified"] != true || projection["exclusions_binding_status"] != "verified" ||
		!issuesOK || len(issues) != 0 || projection["ruleset_version"] != "mancode-sensitive-text:1" ||
		projection["exclusions_digest"] != expectedExclusionDigest {
		projectionBytes, _ := json.Marshal(projection)
		t.Fatalf("privacy policy pair binding was not verified as inert review data: %s", projectionBytes)
	}
	if len(snapshot.CrossFileChecks) != 1 || snapshot.CrossFileChecks[0].Status != "verified" ||
		snapshot.CrossFileChecks[0].PolicyFileCount != 1 || snapshot.CrossFileChecks[0].ExclusionsFileCount != 1 ||
		!snapshot.CrossFileChecks[0].WorkspaceBindingVerified || !snapshot.CrossFileChecks[0].RevisionBindingVerified ||
		!snapshot.CrossFileChecks[0].DigestBindingVerified || snapshot.CrossFileChecks[0].ExecutionAuthorized {
		t.Fatalf("matching privacy policy/exclusions pair was not recorded as inert binding evidence: %#v", snapshot.CrossFileChecks)
	}
	coverage, err := BuildFormatCoverage(snapshot)
	if err != nil || len(coverage.CrossFileChecks) != 1 || coverage.CrossFileChecks[0].Status != "verified" {
		t.Fatalf("Mancode privacy binding did not reach format coverage: checks=%#v err=%v", coverage.CrossFileChecks, err)
	}
	tampered := snapshot
	tampered.CrossFileChecks = append([]migrationcoverage.CrossFileCheck(nil), snapshot.CrossFileChecks...)
	tampered.CrossFileChecks[0].PolicySourceSetSHA256 = strings.Repeat("f", 64)
	tampered.SourceFingerprint = sourceFingerprint(tampered.Files, tampered.CrossFileChecks, tampered.MancodeLegacyWorkflowChecks)
	tampered.ID = string(tampered.Product) + "-" + tampered.SourceFingerprint[:20]
	if err := VerifyManifest(tampered); err == nil {
		t.Fatal("privacy binding check detached from its source-file commitment was accepted")
	}
	tampered = snapshot
	tampered.CrossFileChecks = append([]migrationcoverage.CrossFileCheck(nil), snapshot.CrossFileChecks...)
	tampered.CrossFileChecks[0].PolicyFileCount++
	tampered.SourceFingerprint = sourceFingerprint(tampered.Files, tampered.CrossFileChecks, tampered.MancodeLegacyWorkflowChecks)
	tampered.ID = string(tampered.Product) + "-" + tampered.SourceFingerprint[:20]
	if err := VerifyManifest(tampered); err == nil {
		t.Fatal("privacy source file count detached from manifest inventory was accepted")
	}
	for _, private := range []string{mancodeDecisionTestID("1"), mancodeDecisionTestID("2"), "workspaceId", "lastOperationId"} {
		if strings.Contains(string(records[0].Payload), private) {
			t.Fatalf("privacy policy projection retained source identity %q: %s", private, records[0].Payload)
		}
	}
}

func TestMancodePrivacyPolicyExclusionsBindingReportsMismatchMissingAndMalformedPairs(t *testing.T) {
	t.Run("revision and digest mismatch", func(t *testing.T) {
		policy := mancodePrivacyPolicyFixture()
		exclusions := mancodePrivacyExclusionsFixture()
		exclusions["revision"] = int64(3)
		manifest, records := readMancodePrivacyPairSnapshot(t, policy, exclusions, true, true)
		check := onlyMancodePrivacyBindingCheck(t, manifest)
		if check.Status != "conflicting" || check.RevisionBindingVerified || check.DigestBindingVerified ||
			!containsMancodePrivacyIssue(check.Issues, "revision_mismatch") || !containsMancodePrivacyIssue(check.Issues, "digest_mismatch") {
			t.Fatalf("mismatched privacy pair was not surfaced: %#v", check)
		}
		assertMancodePrivacyPolicyBindingProjection(t, records, "conflicting", false, "revision_mismatch", "digest_mismatch")
		encoded, err := json.Marshal(check)
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{mancodeDecisionTestID("1"), "workspaceId", "relativePath", "privacy-policy.json", "privacy-exclusions.json"} {
			if strings.Contains(string(encoded), private) {
				t.Fatalf("binding check leaked private source identity/path %q: %s", private, encoded)
			}
		}
	})

	t.Run("missing exclusions", func(t *testing.T) {
		manifest, records := readMancodePrivacyPairSnapshot(t, mancodePrivacyPolicyFixture(), nil, true, false)
		check := onlyMancodePrivacyBindingCheck(t, manifest)
		if check.Status != "incomplete" || check.ExclusionsPresent || !containsMancodePrivacyIssue(check.Issues, "exclusions_missing") {
			t.Fatalf("missing exclusions partner was not surfaced: %#v", check)
		}
		assertMancodePrivacyPolicyBindingProjection(t, records, "incomplete", false, "exclusions_missing")
	})

	t.Run("malformed exclusions", func(t *testing.T) {
		exclusions := mancodePrivacyExclusionsFixture()
		exclusions["unknown"] = "must be rejected"
		manifest, records := readMancodePrivacyPairSnapshot(t, mancodePrivacyPolicyFixture(), exclusions, true, true)
		check := onlyMancodePrivacyBindingCheck(t, manifest)
		if check.Status != "incomplete" || !check.ExclusionsPresent || check.ExclusionsValid ||
			!containsMancodePrivacyIssue(check.Issues, "exclusions_invalid") {
			t.Fatalf("malformed exclusions partner was not surfaced: %#v", check)
		}
		assertMancodePrivacyPolicyBindingProjection(t, records, "incomplete", false, "exclusions_invalid")
	})

	t.Run("workspace mismatch", func(t *testing.T) {
		policy := mancodePrivacyPolicyFixture()
		exclusions := mancodePrivacyExclusionsFixture()
		exclusions["workspaceId"] = mancodeDecisionTestID("6")
		manifest, records := readMancodePrivacyPairSnapshot(t, policy, exclusions, true, true)
		check := onlyMancodePrivacyBindingCheck(t, manifest)
		if check.Status != "conflicting" || check.WorkspaceBindingVerified ||
			!containsMancodePrivacyIssue(check.Issues, "workspace_mismatch") {
			t.Fatalf("workspace mismatch was not surfaced: %#v", check)
		}
		assertMancodePrivacyPolicyBindingProjection(t, records, "conflicting", false, "digest_mismatch", "workspace_mismatch")
	})
}

func TestMancodeFormatCoverageMarksLegacyPrivacySnapshotUnverified(t *testing.T) {
	snapshot, _ := readMancodePrivacyPairSnapshot(t, mancodePrivacyPolicyFixture(), mancodePrivacyExclusionsFixture(), true, true)
	legacy := snapshot
	legacy.CrossFileChecks = nil
	legacy.SourceFingerprint = sourceFingerprint(legacy.Files, nil, nil)
	legacy.ID = string(legacy.Product) + "-" + legacy.SourceFingerprint[:20]
	coverage, err := BuildFormatCoverage(legacy)
	if err != nil {
		t.Fatal(err)
	}
	check := onlyCoverageMancodePrivacyBindingCheck(t, coverage.CrossFileChecks)
	if check.Status != "incomplete" || len(check.Issues) != 1 || check.Issues[0] != "legacy_unverified" {
		t.Fatalf("legacy snapshot was treated as pair-verified: %#v", check)
	}
}

func readMancodePrivacyPairSnapshot(t *testing.T, policy, exclusions map[string]any, includePolicy, includeExclusions bool) (Manifest, []Record) {
	t.Helper()
	root := t.TempDir()
	if includePolicy {
		writeMancodeJSONFixture(t, root, ".mancode/shared/context/privacy-policy.json", policy)
	}
	if includeExclusions {
		writeMancodeJSONFixture(t, root, ".mancode/shared/context/privacy-exclusions.json", exclusions)
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
	return snapshot, records
}

func onlyCoverageMancodePrivacyBindingCheck(t *testing.T, checks []migrationcoverage.CrossFileCheck) migrationcoverage.CrossFileCheck {
	t.Helper()
	if len(checks) != 1 {
		t.Fatalf("got %d coverage privacy binding checks, want 1: %#v", len(checks), checks)
	}
	return checks[0]
}

func onlyMancodePrivacyBindingCheck(t *testing.T, manifest Manifest) migrationcoverage.CrossFileCheck {
	t.Helper()
	if len(manifest.CrossFileChecks) != 1 {
		t.Fatalf("got %d privacy binding checks, want 1: %#v", len(manifest.CrossFileChecks), manifest.CrossFileChecks)
	}
	return manifest.CrossFileChecks[0]
}

func assertMancodePrivacyPolicyBindingProjection(t *testing.T, records []Record, status string, verified bool, issues ...string) {
	t.Helper()
	for _, record := range records {
		if record.Type != "project_policy" {
			continue
		}
		var projection map[string]any
		if json.Unmarshal(record.Payload, &projection) != nil || projection["schema_version"] != mancodePrivacyPolicyImportSchemaV2 {
			continue
		}
		actualIssues, ok := projection["exclusions_binding_issues"].([]any)
		if !ok || projection["exclusions_binding_status"] != status || projection["exclusions_binding_verified"] != verified ||
			projection["authority"] != "local_pending_review" || projection["human_review_required"] != true ||
			projection["execution_authorized"] != false || len(actualIssues) != len(issues) {
			t.Fatalf("privacy policy projection has the wrong inert binding status: %s", record.Payload)
		}
		for _, issue := range issues {
			if !containsMancodePrivacyIssueValues(actualIssues, issue) {
				t.Fatalf("privacy policy projection missing issue %q: %s", issue, record.Payload)
			}
		}
		return
	}
	t.Fatalf("versioned privacy policy binding projection not found: %#v", records)
}

func containsMancodePrivacyIssue(issues []string, wanted string) bool {
	for _, issue := range issues {
		if issue == wanted {
			return true
		}
	}
	return false
}

func containsMancodePrivacyIssueValues(issues []any, wanted string) bool {
	for _, issue := range issues {
		if issue == wanted {
			return true
		}
	}
	return false
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
