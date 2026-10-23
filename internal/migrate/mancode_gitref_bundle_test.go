package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestMancodeGitRefTaskBundleImportsVerifiedWorkflowArtifactsForReview(t *testing.T) {
	root, _, _ := mancodeGitRefFixture(t, "git-ref", false)
	taskID := mancodeDecisionTestID("3")
	directory := mancodeV3Fixture(t, root, "shared", taskID, false)
	bundle := mancodeGitRefWorkflowBundleFixture(t, root, directory, taskID)
	if _, err := normalizeMancodeGitRefTaskBundle(bundle); err != nil {
		t.Fatalf("fixture task bundle did not satisfy the converter: %v", err)
	}
	baseManifestBytes := []byte(runMancodeGitRefGit(t, root, "", "show", mancodeTeamGitRef+":manifest.json"))
	manifestValue, err := decodeStrictMancodeGitRefJSON(baseManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifest := manifestValue.(map[string]any)
	workspaceID := mancodeString(manifest["workspaceId"])
	manifest["taskBundles"] = []any{bundle}
	manifest["ownershipFences"] = []any{map[string]any{
		"schemaVersion": json.Number("1"), "taskRef": bundle["taskRef"], "ownerActorId": mancodeDecisionTestID("2"),
		"ownershipEpoch": json.Number("0"), "taskRevision": json.Number("1"),
		"aggregateDigest": bundle["aggregateDigest"], "remoteRevision": json.Number("1"),
		"lastOperationId": workspaceID, "updatedAt": "2026-10-08T12:00:00Z",
	}}
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
		t.Fatalf("valid workflow bundle was rejected: summary=%#v files=%#v", snapshot.Summary, snapshot.Files)
	}
	coverage, err := BuildFormatCoverage(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.RejectedRecords != 0 || coverage.UnclassifiedFiles != 0 || coverage.UnsupportedFiles != 0 {
		t.Fatalf("verified task bundle did not reconcile in format coverage: %#v", coverage)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	types := make(map[string]bool)
	var bundleSummary map[string]any
	for _, record := range records {
		if record.SourcePath != mancodeGitRefVirtualPath {
			continue
		}
		if record.Scope != "local_pending_review" {
			t.Fatalf("task bundle record is not pending review: %#v", record)
		}
		types[record.Type] = true
		if record.Type == mancodeGitRefTaskBundleSummaryType {
			if err := json.Unmarshal(record.Payload, &bundleSummary); err != nil {
				t.Fatal(err)
			}
			for _, sourceIdentity := range []string{taskID, "private-branch-marker", strings.Repeat("c", 40)} {
				if strings.Contains(string(record.Payload), sourceIdentity) {
					t.Fatalf("task bundle summary retained source identity %q: %s", sourceIdentity, record.Payload)
				}
			}
		}
	}
	for _, recordType := range []string{
		"workflow_metadata", "workflow_requirements", "workflow_review", "workflow_verification", "workflow_plan", mancodeGitRefTaskBundleSummaryType,
	} {
		if !types[recordType] {
			t.Errorf("task bundle snapshot omitted verified artifact type %q", recordType)
		}
	}
	if bundleSummary == nil || bundleSummary["execution_authorized"] != false ||
		bundleSummary["human_review_required"] != true || bundleSummary["aggregate_verified"] != true ||
		bundleSummary["task_identity_omitted"] != true || bundleSummary["code_reference_omitted"] != true {
		t.Fatalf("task bundle summary did not retain its review-only integrity boundary: %#v", bundleSummary)
	}
	for _, name := range []string{"mancode-git-ref-task-bundle-v1.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "schema", name))
		if err != nil || !json.Valid(data) {
			t.Fatalf("task bundle schema %s is missing or invalid JSON: %v", name, err)
		}
	}
}

func TestMancodeGitRefTaskBundleRejectsDigestTamperingAndSecretArtifacts(t *testing.T) {
	root := t.TempDir()
	taskID := mancodeDecisionTestID("3")
	directory := mancodeV3Fixture(t, root, "shared", taskID, false)
	bundle := mancodeGitRefWorkflowBundleFixture(t, root, directory, taskID)

	t.Run("artifact content digest mismatch", func(t *testing.T) {
		changed := cloneMancodeJSON(bundle).(map[string]any)
		artifacts := changed["artifacts"].([]any)
		artifact := artifacts[0].(map[string]any)
		artifact["contentDigest"] = "sha256:" + strings.Repeat("0", 64)
		rehashMancodeGitRefBundle(t, changed)
		if _, err := normalizeMancodeGitRefTaskBundle(changed); err == nil || !strings.Contains(err.Error(), "content digest") {
			t.Fatalf("tampered artifact digest was accepted: %v", err)
		}
	})

	for _, testCase := range []struct {
		name    string
		content string
	}{
		{name: "credential assignment", content: "token = ghp_0123456789abcdefghijklmnopqrstuvwxyz"},
		{name: "email address", content: "Contact private-reviewer@example.com for details"},
		{name: "absolute path", content: "Source was /Users/private-user/workspace"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			changed := cloneMancodeJSON(bundle).(map[string]any)
			artifacts := changed["artifacts"].([]any)
			var plan map[string]any
			for _, value := range artifacts {
				artifact := value.(map[string]any)
				if artifact["kind"] == "plan" {
					plan = artifact
				}
			}
			if plan == nil {
				t.Fatal("fixture has no plan artifact")
			}
			plan["content"] = testCase.content
			plan["contentDigest"], _ = mancodeCanonicalDigest(plan["content"])
			aggregate := changed["aggregate"].(map[string]any)
			taskRef := changed["taskRef"]
			aggregate["planDigest"], _ = mancodeCanonicalDigest(map[string]any{
				"artifactRef": map[string]any{"taskRef": taskRef, "kind": "plan"}, "content": plan["content"],
			})
			changed["aggregateDigest"], _ = mancodeCanonicalDigest(aggregate)
			rehashMancodeGitRefBundle(t, changed)
			if _, err := normalizeMancodeGitRefTaskBundle(changed); err == nil || !strings.Contains(err.Error(), "privacy-blocked") {
				t.Fatalf("credential-bearing bundle artifact was accepted: %v", err)
			}
		})
	}
}

func mancodeGitRefWorkflowBundleFixture(t *testing.T, root, directory, taskID string) map[string]any {
	t.Helper()
	taskRef := map[string]any{"namespace": "shared", "taskId": taskID}
	metadata := readMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(directory, "metadata.json")))
	requirements := readMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(directory, "requirements.json")))
	review := readMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(directory, "review-ledger.json")))
	verification := readMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(directory, "verification-ledger.json")))
	planBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(directory), "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	plan := string(planBytes)
	artifactValues := []struct {
		kind string
		path string
		body any
	}{
		{"metadata", "metadata.json", metadata},
		{"plan", "plan.md", plan},
		{"requirements", "requirements.json", requirements},
		{"review", "review-ledger.json", review},
		{"verification", "verification-ledger.json", verification},
	}
	artifacts := make([]any, 0, len(artifactValues))
	for _, item := range artifactValues {
		contentDigest, err := mancodeCanonicalDigest(item.body)
		if err != nil {
			t.Fatal(err)
		}
		artifacts = append(artifacts, map[string]any{
			"kind": item.kind, "relativePath": item.path, "content": item.body, "contentDigest": contentDigest,
		})
	}
	sort.Slice(artifacts, func(i, j int) bool {
		return mancodeString(artifacts[i].(map[string]any)["kind"]) < mancodeString(artifacts[j].(map[string]any)["kind"])
	})
	metadataDigest, err := mancodeWorkflowMetadataDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	planDigest, err := mancodeCanonicalDigest(map[string]any{
		"artifactRef": map[string]any{"taskRef": taskRef, "kind": "plan"}, "content": plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	aggregate := map[string]any{
		"taskRef": taskRef, "taskRevision": json.Number("1"), "ownershipEpoch": json.Number("0"),
		"metadataDigest": metadataDigest, "requirementsDigest": requirements["contentDigest"],
		"reviewDigest": review["contentDigest"], "verificationDigest": verification["contentDigest"],
		"planVersion": json.Number("1"), "planDigest": planDigest,
		"latestCheckpointId": nil, "latestCheckpointDigest": nil, "parentSnapshotDigest": nil,
	}
	aggregateDigest, err := mancodeCanonicalDigest(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"schemaVersion": json.Number("1"), "taskRef": taskRef, "taskRevision": json.Number("1"),
		"ownershipEpoch": json.Number("0"), "aggregate": aggregate, "aggregateDigest": aggregateDigest,
		"codeRef":   map[string]any{"branch": "private-branch-marker", "head": strings.Repeat("c", 40)},
		"artifacts": artifacts, "createdAt": "2026-10-08T12:00:00Z",
	}
	bundleDigest, err := mancodeCanonicalDigest(body)
	if err != nil {
		t.Fatal(err)
	}
	body["bundleDigest"] = bundleDigest
	return body
}

func rehashMancodeGitRefBundle(t *testing.T, bundle map[string]any) {
	t.Helper()
	delete(bundle, "bundleDigest")
	artifacts := bundle["artifacts"].([]any)
	sort.Slice(artifacts, func(i, j int) bool {
		return mancodeString(artifacts[i].(map[string]any)["kind"]) < mancodeString(artifacts[j].(map[string]any)["kind"])
	})
	bundle["artifacts"] = artifacts
	digestValue, err := mancodeCanonicalDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle["bundleDigest"] = digestValue
}
