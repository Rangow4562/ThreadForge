package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestMancodeGitRefV2PrivacySnapshotImportsVerifiedHistoricalPair(t *testing.T) {
	root, _, baseBytes := mancodeGitRefFixture(t, "git-ref", false)
	manifestValue, err := decodeStrictMancodeGitRefJSON(baseBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifest := manifestValue.(map[string]any)
	workspaceID := mancodeString(manifest["workspaceId"])
	policy := mancodePrivacyPolicyFixture()
	exclusions := mancodePrivacyExclusionsFixture()
	policyDigest, err := mancodeGitRefPrivacyPolicyDigest(policy)
	if err != nil {
		t.Fatal(err)
	}
	manifest["schemaVersion"] = json.Number("2")
	manifest["minReaderVersion"] = "0.6.5"
	manifest["minWriterVersion"] = "0.6.5"
	manifest["privacyPolicy"] = map[string]any{
		"policy": policy, "exclusions": exclusions, "digest": policyDigest,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeMancodeGitRefCommit(t, root, manifestBytes)

	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshotWithOptions(Mancode, root, dataRoot, InspectOptions{IncludeMancodeGitRef: true})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary.RecordsRejected != 0 {
		t.Fatalf("valid v2 privacy pair was rejected: summary=%#v files=%#v", snapshot.Summary, snapshot.Files)
	}
	coverage, err := BuildFormatCoverage(snapshot)
	if err != nil || coverage.RejectedRecords != 0 || coverage.UnclassifiedFiles != 0 {
		t.Fatalf("verified v2 privacy pair did not reconcile: coverage=%#v err=%v", coverage, err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var policyProjection map[string]any
	var policyRecords int
	for _, record := range records {
		if record.SourcePath != mancodeGitRefVirtualPath || record.Type != "project_policy" {
			continue
		}
		policyRecords++
		if record.Scope != "local_pending_review" {
			t.Fatalf("git-ref privacy record escaped local review: %#v", record)
		}
		var projection map[string]any
		if err := json.Unmarshal(record.Payload, &projection); err != nil {
			t.Fatal(err)
		}
		if projection["policy_kind"] == "privacy" {
			policyProjection = projection
		}
		for _, private := range []string{workspaceID, mancodeDecisionTestID("2"), "workspaceId", "lastOperationId"} {
			if strings.Contains(string(record.Payload), private) {
				t.Fatalf("git-ref privacy projection retained source identity %q: %s", private, record.Payload)
			}
		}
	}
	if policyRecords != 2 || policyProjection == nil ||
		policyProjection["schema_version"] != mancodePrivacyPolicyImportSchemaV2 ||
		policyProjection["exclusions_binding_verified"] != true ||
		policyProjection["exclusions_binding_status"] != "verified" ||
		policyProjection["execution_authorized"] != false || policyProjection["human_review_required"] != true {
		t.Fatalf("v2 privacy snapshot was not projected as a verified, inert pair: count=%d projection=%#v", policyRecords, policyProjection)
	}
}

func TestMancodeGitRefV2PrivacySnapshotMismatchIsAVisibleRejection(t *testing.T) {
	root, _, baseBytes := mancodeGitRefFixture(t, "git-ref", false)
	manifestValue, err := decodeStrictMancodeGitRefJSON(baseBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifest := manifestValue.(map[string]any)
	policy := mancodePrivacyPolicyFixture()
	exclusions := mancodePrivacyExclusionsFixture()
	manifest["schemaVersion"] = json.Number("2")
	manifest["minReaderVersion"] = "0.6.5"
	manifest["minWriterVersion"] = "0.6.5"
	manifest["privacyPolicy"] = map[string]any{
		"policy": policy, "exclusions": exclusions, "digest": "sha256:" + strings.Repeat("0", 64),
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeMancodeGitRefCommit(t, root, manifestBytes)

	inspected, err := InspectWithOptions(Mancode, root, InspectOptions{IncludeMancodeGitRef: true})
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Summary.RecordsRejected != 1 {
		t.Fatalf("mismatched privacy snapshot did not add one classified rejection: %#v", inspected.Summary)
	}
	for _, file := range inspected.Files {
		if file.Path == mancodeGitRefVirtualPath && (file.Status != "partially_rejected" || file.RejectedCount != 1) {
			t.Fatalf("mismatched privacy snapshot was not surfaced on the virtual source: %#v", file)
		}
	}
}

func TestMancodeGitRefPrivacyExclusionBlocksMatchingBundleArtifact(t *testing.T) {
	root := t.TempDir()
	taskID := mancodeDecisionTestID("3")
	directory := mancodeV3Fixture(t, root, "shared", taskID, false)
	bundle := mancodeGitRefWorkflowBundleFixture(t, root, directory, taskID)
	var excludedDigest string
	for _, value := range bundle["artifacts"].([]any) {
		artifact := value.(map[string]any)
		if artifact["kind"] == "plan" {
			excludedDigest = mancodeString(artifact["contentDigest"])
		}
	}
	if excludedDigest == "" {
		t.Fatal("fixture bundle has no plan content digest")
	}

	exclusions := mancodePrivacyExclusionsFixture()
	exclusions["entries"] = []any{map[string]any{
		"kind": "checkpoint", "relativePath": "shared/workflows/" + taskID + "/checkpoints/" + mancodeDecisionTestID("6") + ".json",
		"entityDigest": excludedDigest,
	}}
	exclusionItems, handled, err := normalizeMancodePrivacyExclusions(exclusions)
	if err != nil || !handled || len(exclusionItems) != 1 || exclusionItems[0].privacyExclusions == nil {
		t.Fatalf("privacy exclusions fixture did not normalize: handled=%t err=%v", handled, err)
	}
	policy := mancodePrivacyPolicyFixture()
	policy["exclusions"] = map[string]any{
		"revision": exclusions["revision"], "digest": exclusionItems[0].privacyExclusions.CanonicalDigest,
	}
	policyDigest, err := mancodeGitRefPrivacyPolicyDigest(policy)
	if err != nil {
		t.Fatal(err)
	}
	privacyItems, blockedDigests, err := normalizeMancodeGitRefPrivacySnapshot(map[string]any{
		"policy": policy, "exclusions": exclusions, "digest": policyDigest,
	}, mancodeDecisionTestID("1"))
	if err != nil || len(privacyItems) != 2 || !blockedDigests[excludedDigest] {
		t.Fatalf("bound privacy exclusion was not retained: blocked=%v err=%v", blockedDigests, err)
	}
	if _, err := normalizeMancodeGitRefTaskBundle(bundle, blockedDigests); err == nil || !strings.Contains(err.Error(), "privacy-excluded") {
		t.Fatalf("bundle carrying an excluded artifact was accepted: %v", err)
	}
	codeRefDigest, err := mancodeCanonicalDigest(bundle["codeRef"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeMancodeGitRefTaskBundle(bundle, map[string]bool{codeRefDigest: true}); err == nil || !strings.Contains(err.Error(), "privacy-excluded") {
		t.Fatalf("bundle carrying an excluded code reference was accepted: %v", err)
	}
}
