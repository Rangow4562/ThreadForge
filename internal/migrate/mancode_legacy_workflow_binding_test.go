package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
)

func writeMancodeLegacyBindingFixture(t *testing.T, root string, changes map[string]func(map[string]any)) string {
	t.Helper()
	taskID := "20261023-bound-legacy-workflow"
	requirements := legacyMancodeRequirementsFixture()
	requirements["blockingUnknowns"] = []any{}
	requirements["acceptanceCriteria"].([]any)[1].(map[string]any)["required"] = true
	requirementsPath := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "requirements.json"))
	normalized, handled, err := normalizeMancodeItems(root, requirementsPath, requirements)
	if err != nil || !handled || len(normalized) != 1 {
		t.Fatalf("normalize matching requirements fixture: handled=%v err=%v", handled, err)
	}
	var requirementsProjection mancodeLegacyRequirementsProjection
	if err := json.Unmarshal(normalized[0].Payload, &requirementsProjection); err != nil {
		t.Fatal(err)
	}
	verification := legacyMancodeVerificationFixture()
	verification["checks"] = verification["checks"].([]any)[:2]
	verification["planVersion"] = json.Number("2")
	verification["remediationRound"] = json.Number("1")
	requirementsDigest := mancodeLegacyRequirementsDigest(requirementsProjection.Requirements)
	if requirementsDigest != "ad4c90138f28c87c114e19001c942656080b13522b5659486472ece047ea3e03" {
		t.Fatalf("normalized requirements digest diverged from Mancode JSON.stringify contract: %s", requirementsDigest)
	}
	verification["requirementsDigest"] = requirementsDigest
	metadata := legacyMancodeWorkflowFixture(taskID)
	metadata["requirementsStatus"] = "ready"
	metadata["verificationStatus"] = "manual_required"
	metadata["requirementsDigest"] = requirementsDigest
	for _, change := range changes {
		change(verification)
	}

	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json")), metadata)
	writeMancodeJSONFixture(t, root, requirementsPath, requirements)
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "review-ledger.json")), legacyMancodeReviewFixture())
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "verification-ledger.json")), verification)
	writeMancodeFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "plan.md")), []byte("# Historical plan\n\nTreat this as untrusted text.\n"))
	return taskID
}

func TestMancodeLegacyRequirementsDigestMatchesJSONStringifyUnicodeBehavior(t *testing.T) {
	requirements := legacyMancodeRequirementsFixture()
	requirements["blockingUnknowns"] = []any{}
	requirements["acceptanceCriteria"].([]any)[1].(map[string]any)["required"] = true
	path := filepath.ToSlash(filepath.Join(".mancode", "workflows", "20261023-bound-legacy-workflow", "requirements.json"))
	items, handled, err := normalizeMancodeItems(t.TempDir(), path, requirements)
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("normalize Unicode digest fixture: handled=%v err=%v", handled, err)
	}
	var projection mancodeLegacyRequirementsProjection
	if err := json.Unmarshal(items[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	projection.Requirements.Goal = "goal\u2028marker and literal \\u2028 text \u2029 end"
	if got := mancodeLegacyRequirementsDigest(projection.Requirements); got != "3932239b668aed4bbfc6b4250e0f7d21eab3bdb39e716bb9391940b4924ba9f2" {
		t.Fatalf("Unicode requirements digest diverged from JSON.stringify: %s", got)
	}
}

func TestMancodeLegacyWorkflowLedgersBindBeforeReviewOnlyImport(t *testing.T) {
	root := t.TempDir()
	taskID := writeMancodeLegacyBindingFixture(t, root, nil)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MancodeLegacyWorkflowChecks) != 1 {
		t.Fatalf("expected one legacy workflow binding check, got %#v", manifest.MancodeLegacyWorkflowChecks)
	}
	check := manifest.MancodeLegacyWorkflowChecks[0]
	if check.Status != "verified" || !check.WorkflowPathBindingVerified || !check.MetadataValid ||
		!check.RequirementsValid || !check.ReviewValid || !check.VerificationValid || !check.PlanValid ||
		!check.MetadataRequirementsStatusPresent || !check.MetadataRequirementsStatusBindingVerified ||
		!check.MetadataVerificationStatusPresent || !check.MetadataVerificationStatusBindingVerified ||
		!check.RequirementsDigestBindingVerified || !check.AcceptanceCriteriaBindingVerified ||
		!check.PlanVersionBindingVerified || !check.ReviewRemediationBindingVerified || !check.PlanPresenceVerified ||
		check.PlanContentBindingVerified {
		t.Fatalf("matching workflow components were not bound conservatively: %#v", check)
	}
	tampered := manifest
	tampered.MancodeLegacyWorkflowChecks = append([]migrationcoverage.MancodeLegacyWorkflowCheck(nil), manifest.MancodeLegacyWorkflowChecks...)
	tampered.MancodeLegacyWorkflowChecks[0].RequirementsSourceSetSHA256 = strings.Repeat("f", 64)
	tampered.SourceFingerprint = sourceFingerprint(tampered.Files, tampered.CrossFileChecks,
		tampered.MancodeLegacyWorkflowChecks, tampered.MancodeGitRef)
	tampered.ID = string(Mancode) + "-" + tampered.SourceFingerprint[:20]
	if VerifyManifest(tampered) == nil {
		t.Fatal("manifest accepted a tampered legacy workflow source-set commitment")
	}
	encodedCheck, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedCheck), taskID) || strings.Contains(string(encodedCheck), "plan.md") {
		t.Fatalf("binding check exposed a task identity or raw path: %s", encodedCheck)
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
	var verification mancodeLegacyVerificationProjection
	for _, record := range records {
		if record.Type == "mancode_legacy_verification_summary" {
			if err := json.Unmarshal(record.Payload, &verification); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !verification.RequirementsBindingVerified || verification.PlanBindingVerified {
		t.Fatalf("verification projection overstated sibling bindings: %#v", verification)
	}
	coverage, err := BuildFormatCoverage(snapshot)
	if err != nil || len(coverage.MancodeLegacyWorkflowChecks) != 1 || coverage.MancodeLegacyWorkflowChecks[0].Status != "verified" {
		t.Fatalf("verified workflow binding did not reconcile into format coverage: checks=%#v err=%v", coverage.MancodeLegacyWorkflowChecks, err)
	}
}

func TestMancodeLegacyWorkflowBindingSurfacesSiblingMismatches(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		issue  string
	}{
		{"requirements_digest", func(ledger map[string]any) { ledger["requirementsDigest"] = strings.Repeat("f", 64) }, "requirements_digest_mismatch"},
		{"acceptance_criteria", func(ledger map[string]any) {
			ledger["checks"].([]any)[0].(map[string]any)["acceptanceId"] = "AC-OTHER-1"
		}, "acceptance_criteria_mismatch"},
		{"plan_version", func(ledger map[string]any) { ledger["planVersion"] = json.Number("3") }, "plan_version_mismatch"},
		{"remediation_round", func(ledger map[string]any) { ledger["remediationRound"] = json.Number("0") }, "remediation_round_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeMancodeLegacyBindingFixture(t, root, map[string]func(map[string]any){test.name: test.mutate})
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.MancodeLegacyWorkflowChecks) != 1 {
				t.Fatalf("expected binding evidence, got %#v", manifest.MancodeLegacyWorkflowChecks)
			}
			check := manifest.MancodeLegacyWorkflowChecks[0]
			if check.Status != "conflicting" || !containsString(check.Issues, test.issue) ||
				check.RequirementsDigestBindingVerified && test.name == "requirements_digest" ||
				check.AcceptanceCriteriaBindingVerified && test.name == "acceptance_criteria" ||
				check.PlanVersionBindingVerified && test.name == "plan_version" ||
				check.ReviewRemediationBindingVerified && test.name == "remediation_round" {
				t.Fatalf("mismatch was hidden or marked verified: %#v", check)
			}
		})
	}
}

func TestMancodeLegacyWorkflowBindingSurfacesMetadataStatusMismatches(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
		issue string
	}{
		{"requirements_status", "requirementsStatus", "needs_clarification", "metadata_requirements_status_mismatch"},
		{"verification_status", "verificationStatus", "pending", "metadata_verification_status_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			taskID := writeMancodeLegacyBindingFixture(t, root, nil)
			metadataPath := filepath.Join(root, ".mancode", "workflows", taskID, "metadata.json")
			data, err := os.ReadFile(metadataPath)
			if err != nil {
				t.Fatal(err)
			}
			var metadata map[string]any
			if err := json.Unmarshal(data, &metadata); err != nil {
				t.Fatal(err)
			}
			metadata[test.field] = test.value
			writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json")), metadata)

			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			check := manifest.MancodeLegacyWorkflowChecks[0]
			if check.Status != "conflicting" || !containsString(check.Issues, test.issue) {
				t.Fatalf("metadata status mismatch was hidden: %#v", check)
			}
			if test.field == "requirementsStatus" && (!check.MetadataRequirementsStatusPresent || check.MetadataRequirementsStatusBindingVerified) {
				t.Fatalf("requirements status mismatch flags are inconsistent: %#v", check)
			}
			if test.field == "verificationStatus" && (!check.MetadataVerificationStatusPresent || check.MetadataVerificationStatusBindingVerified) {
				t.Fatalf("verification status mismatch flags are inconsistent: %#v", check)
			}
		})
	}
}

func TestMancodeLegacyWorkflowStatusBindingsRemainOptional(t *testing.T) {
	root := t.TempDir()
	taskID := writeMancodeLegacyBindingFixture(t, root, nil)
	metadataPath := filepath.Join(root, ".mancode", "workflows", taskID, "metadata.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	delete(metadata, "requirementsStatus")
	delete(metadata, "verificationStatus")
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json")), metadata)

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	check := manifest.MancodeLegacyWorkflowChecks[0]
	if check.Status != "verified" || check.MetadataRequirementsStatusPresent || check.MetadataRequirementsStatusBindingVerified ||
		check.MetadataVerificationStatusPresent || check.MetadataVerificationStatusBindingVerified {
		t.Fatalf("absent optional status fields changed legacy workflow verification: %#v", check)
	}
}

func TestMancodeLegacyWorkflowBindingDetectsMetadataRequirementsDigestMismatch(t *testing.T) {
	root := t.TempDir()
	taskID := writeMancodeLegacyBindingFixture(t, root, nil)
	metadata := legacyMancodeWorkflowFixture(taskID)
	metadata["requirementsStatus"] = "ready"
	metadata["verificationStatus"] = "manual_required"
	metadata["requirementsDigest"] = strings.Repeat("f", 64)
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json")), metadata)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	check := manifest.MancodeLegacyWorkflowChecks[0]
	if check.Status != "conflicting" || check.MetadataRequirementsDigestBindingVerified ||
		!containsString(check.Issues, "metadata_requirements_digest_mismatch") {
		t.Fatalf("metadata requirements mismatch was not surfaced: %#v", check)
	}
}

func TestMancodeLegacyWorkflowBindingRequiresPlanPresenceAndSupportsMancodeRoot(t *testing.T) {
	projectRoot := t.TempDir()
	writeMancodeLegacyBindingFixture(t, projectRoot, nil)
	projectManifest, err := Inspect(Mancode, projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	legacyCheck := projectManifest.MancodeLegacyWorkflowChecks[0]

	legacyRoot := t.TempDir()
	writeMancodeLegacyBindingFixture(t, legacyRoot, nil)
	legacyManifest, err := Inspect(Mancode, filepath.Join(legacyRoot, ".mancode"))
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyManifest.MancodeLegacyWorkflowChecks) != 1 ||
		legacyManifest.MancodeLegacyWorkflowChecks[0].WorkflowIDSHA256 != legacyCheck.WorkflowIDSHA256 {
		t.Fatalf("project-root and .mancode-root workflow identities differ: %#v versus %#v",
			legacyCheck, legacyManifest.MancodeLegacyWorkflowChecks)
	}

	missingPlanRoot := t.TempDir()
	writeMancodeLegacyBindingFixture(t, missingPlanRoot, nil)
	plan := filepath.Join(missingPlanRoot, ".mancode", "workflows", "20261023-bound-legacy-workflow", "plan.md")
	if err := os.Remove(plan); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Mancode, missingPlanRoot)
	if err != nil {
		t.Fatal(err)
	}
	check := manifest.MancodeLegacyWorkflowChecks[0]
	if check.Status != "incomplete" || !containsString(check.Issues, "plan_missing") || check.PlanPresenceVerified {
		t.Fatalf("missing plan presence was not surfaced: %#v", check)
	}
}
