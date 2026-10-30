package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/strictjson"
)

func writeMancodeWorkflowArchiveFixture(t *testing.T, root, workflowDir, archiveID string, includePlan bool) {
	t.Helper()
	requirementsPath := filepath.ToSlash(filepath.Join(workflowDir, "requirements.json"))
	requirementsBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(requirementsPath)))
	if err != nil {
		t.Fatal(err)
	}
	requirementsContent := string(requirementsBytes)
	var requirements map[string]any
	if err := strictjson.Decode(requirementsBytes, &requirements); err != nil {
		t.Fatal(err)
	}
	taskRef, ok := mancodeTaskRefFromValue(requirements["taskRef"])
	if !ok {
		t.Fatal("fixture requirements have an invalid task reference")
	}
	requirementsDigest := mancodeString(requirements["contentDigest"])
	requirementsRevision := requirements["revision"]
	requirementsFileDigest, err := mancodeCanonicalDigest(map[string]any{"content": requirementsContent})
	if err != nil {
		t.Fatal(err)
	}

	createdAt := "2026-10-30T10:00:00Z"
	var planContent *string
	var archivePlanContent any
	var sourcePlanDigest, planFileDigest any
	if includePlan {
		plan := "# Archived plan\n"
		planContent = &plan
		archivePlanContent = plan
		planDigest, err := mancodeCanonicalDigest(map[string]any{
			"artifactRef": map[string]any{"taskRef": taskRef, "kind": "plan"}, "content": plan,
		})
		if err != nil {
			t.Fatal(err)
		}
		fileDigest, err := mancodeCanonicalDigest(map[string]any{"content": plan})
		if err != nil {
			t.Fatal(err)
		}
		sourcePlanDigest, planFileDigest = planDigest, fileDigest
	}

	manifest := map[string]any{
		"schemaVersion": json.Number("1"), "archiveId": archiveID, "operationId": archiveID,
		"taskRef": taskRef, "sourceTaskRevision": json.Number("1"),
		"sourceRequirementsRevision": requirementsRevision, "sourceRequirementsDigest": requirementsDigest,
		"sourcePlanVersion": json.Number("1"), "sourcePlanDigest": sourcePlanDigest,
		"requirementsFileDigest": requirementsFileDigest, "planFileDigest": planFileDigest,
		"createdAt": createdAt,
	}
	archiveDigest, err := mancodeCanonicalDigest(map[string]any{
		"archiveId": archiveID, "operationId": archiveID, "taskRef": taskRef,
		"sourceTaskRevision":         manifest["sourceTaskRevision"],
		"sourceRequirementsRevision": requirementsRevision, "sourceRequirementsDigest": requirementsDigest,
		"sourcePlanVersion": json.Number("1"), "sourcePlanDigest": sourcePlanDigest,
		"createdAt": createdAt, "requirementsContent": requirementsContent, "planContent": archivePlanContent,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest["archiveDigest"] = archiveDigest
	archiveDir := filepath.ToSlash(filepath.Join(workflowDir, "archives", archiveID))
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(archiveDir, "archive.json")), manifest)
	writeMancodeFixture(t, root, filepath.ToSlash(filepath.Join(archiveDir, "requirements.json")), requirementsBytes)
	if planContent != nil {
		writeMancodeFixture(t, root, filepath.ToSlash(filepath.Join(archiveDir, "plan.md")), []byte(*planContent))
	}
}

func TestMancodeV3WorkflowArchiveBindingSurvivesSnapshotReadback(t *testing.T) {
	root := t.TempDir()
	taskID := strings.Repeat("0", 25) + "1"
	workflowDir := mancodeV3Fixture(t, root, "local", taskID, false)
	archiveID := strings.Repeat("0", 25) + "A"
	writeMancodeWorkflowArchiveFixture(t, root, workflowDir, archiveID, true)

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MancodeWorkflowArchiveChecks) != 1 {
		t.Fatalf("expected one source-bound workflow archive check, got %#v", manifest.MancodeWorkflowArchiveChecks)
	}
	check := manifest.MancodeWorkflowArchiveChecks[0]
	if check.Status != "verified" || !check.ArchiveDigestBindingVerified || !check.RequirementsDigestBindingVerified ||
		!check.PlanDigestBindingVerified || check.ArchiveFileCount != 3 || check.OtherFileCount != 0 ||
		check.Authority != "local_pending_review" || !check.HumanReviewRequired || check.ExecutionAuthorized {
		t.Fatalf("workflow archive integrity or authority boundary was not preserved: %#v", check)
	}
	encodedCheck, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedCheck), archiveID) || strings.Contains(string(encodedCheck), "Archived plan") {
		t.Fatalf("archive check exposed raw source identity or body: %s", encodedCheck)
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
	archivePrefix := filepath.ToSlash(filepath.Join(workflowDir, "archives", archiveID)) + "/"
	seen := map[string]bool{}
	for _, record := range records {
		if !strings.HasPrefix(record.SourcePath, archivePrefix) {
			continue
		}
		if record.Type != "workflow_document" || record.Scope != "local_pending_review" || record.SourcePath == "" {
			t.Fatalf("archive document was not retained as inert review-only material: %#v", record)
		}
		seen[filepath.Base(record.SourcePath)] = true
		if record.Payload == nil || strings.Contains(string(record.Payload), `"execution_authorized":true`) {
			t.Fatalf("archive document acquired execution authority: %#v", record)
		}
	}
	for _, name := range []string{"archive.json", "requirements.json", "plan.md"} {
		if !seen[name] {
			t.Errorf("snapshot readback omitted archive document %q", name)
		}
	}
}

func TestMancodeV3WorkflowArchiveBindingReportsDigestConflict(t *testing.T) {
	root := t.TempDir()
	taskID := strings.Repeat("0", 25) + "1"
	workflowDir := mancodeV3Fixture(t, root, "local", taskID, false)
	archiveID := strings.Repeat("0", 25) + "B"
	writeMancodeWorkflowArchiveFixture(t, root, workflowDir, archiveID, false)
	manifestPath := filepath.Join(root, filepath.FromSlash(filepath.Join(workflowDir, "archives", archiveID, "archive.json")))
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var archiveManifest map[string]any
	if err := strictjson.Decode(data, &archiveManifest); err != nil {
		t.Fatal(err)
	}
	archiveManifest["requirementsFileDigest"] = "sha256:" + strings.Repeat("f", 64)
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(workflowDir, "archives", archiveID, "archive.json")), archiveManifest)

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MancodeWorkflowArchiveChecks) != 1 || manifest.MancodeWorkflowArchiveChecks[0].Status != "conflicting" {
		t.Fatalf("mismatched archive digest did not remain visible as a conflict: %#v", manifest.MancodeWorkflowArchiveChecks)
	}
}

func TestMancodeV3WorkflowArchiveWithoutPlanBindsExplicitNullDigests(t *testing.T) {
	root := t.TempDir()
	workflowDir := mancodeV3Fixture(t, root, "local", strings.Repeat("0", 25)+"1", false)
	writeMancodeWorkflowArchiveFixture(t, root, workflowDir, strings.Repeat("0", 25)+"C", false)

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MancodeWorkflowArchiveChecks) != 1 || manifest.MancodeWorkflowArchiveChecks[0].Status != "verified" ||
		manifest.MancodeWorkflowArchiveChecks[0].PlanFileCount != 0 || !manifest.MancodeWorkflowArchiveChecks[0].PlanDigestBindingVerified {
		t.Fatalf("archive without a plan did not bind the explicit null plan digests: %#v", manifest.MancodeWorkflowArchiveChecks)
	}
}

func TestMancodeWorkflowArchiveRejectsUnsupportedArchivePaths(t *testing.T) {
	root := t.TempDir()
	taskID := strings.Repeat("0", 25) + "1"
	workflowDir := filepath.ToSlash(filepath.Join(".mancode", "local", "workflows", taskID))
	archivePath := filepath.ToSlash(filepath.Join(workflowDir, "archives", "not-a-ulid", "archive.json"))
	writeMancodeFixture(t, root, archivePath, []byte(`{"schemaVersion":1}`))

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MancodeWorkflowArchiveChecks) != 1 || manifest.MancodeWorkflowArchiveChecks[0].Status != "incomplete" ||
		!mancodeArchiveIssuePresent(manifest.MancodeWorkflowArchiveChecks[0].Issues, "invalid_path") {
		t.Fatalf("unsupported archive path was not visibly blocked: %#v", manifest.MancodeWorkflowArchiveChecks)
	}
	for _, file := range manifest.Files {
		if file.Path == archivePath && file.RejectedCount != 1 {
			t.Fatalf("unsupported archive document was not rejected: %#v", file)
		}
	}
}

func TestMancodeWorkflowArchiveSurfacesCredentialExclusion(t *testing.T) {
	root := t.TempDir()
	taskID := strings.Repeat("0", 25) + "1"
	archiveID := strings.Repeat("0", 25) + "D"
	planPath := filepath.ToSlash(filepath.Join(".mancode", "local", "workflows", taskID, "archives", archiveID, "plan.md"))
	writeMancodeFixture(t, root, planPath, []byte("TOKEN=ghp_123456789012345678901234567890123456\n"))

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MancodeWorkflowArchiveChecks) != 1 ||
		!mancodeArchiveIssuePresent(manifest.MancodeWorkflowArchiveChecks[0].Issues, "credential_excluded") {
		t.Fatalf("credential exclusion was not surfaced in archive evidence: %#v", manifest.MancodeWorkflowArchiveChecks)
	}
	for _, file := range manifest.Files {
		if file.Path == planPath && (file.ExcludedCount != 1 || file.ImportedCount != 0) {
			t.Fatalf("credential-bearing archive body was not excluded: %#v", file)
		}
	}
}

func mancodeArchiveIssuePresent(issues []string, wanted string) bool {
	for _, issue := range issues {
		if issue == wanted {
			return true
		}
	}
	return false
}
