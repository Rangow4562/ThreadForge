package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/internal/migrate"
	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
	"github.com/threadforge-ai/threadforge/pkg/migrationreview"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

func TestMigrateReviewPlanSignsOnlyReconciledMancodeSnapshot(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("THREADFORGE_HOME", dataRoot)
	sourceRoot := t.TempDir()
	taskID := "20261027-review-cli-test"
	workflowRoot := filepath.Join(sourceRoot, ".mancode", "workflows", taskID)
	if err := os.MkdirAll(workflowRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	requirements := cliLegacyRequirementsFixture()
	requirements["blockingUnknowns"] = []any{}
	requirements["acceptanceCriteria"].([]any)[1].(map[string]any)["required"] = true
	const requirementsDigest = "ad4c90138f28c87c114e19001c942656080b13522b5659486472ece047ea3e03"
	metadata := map[string]any{
		"taskId": taskID, "task": "reviewed legacy plan", "mode": "man", "currentStep": 4,
		"skippedSteps": []any{}, "startedAt": "2026-10-27T10:00:00Z", "updatedAt": "2026-10-27T10:05:00Z",
		"status": "blocked", "blockingReason": "waiting for verification", "planVersion": 2, "reviewPolicyVersion": 2, "planningPolicyVersion": 1,
		"verificationPolicyVersion": 1, "requirementsStatus": "ready", "requirementsDigest": requirementsDigest,
		"planDecision": "plan_only", "verificationStatus": "manual_required",
	}
	review := map[string]any{
		"version": "1.0", "depth": "full", "requiredDomains": []any{"quality", "security"},
		"completedDomains": []any{"quality"}, "reports": map[string]any{"quality": "reports/quality-review.md"},
		"blockers":          []any{map[string]any{"id": "QUALITY-1", "domain": "quality", "status": "resolved"}},
		"remediationRounds": 1,
	}
	verification := map[string]any{
		"version": 1, "planVersion": 2, "requirementsDigest": requirementsDigest, "remediationRound": 1,
		"status": "manual_required", "checks": []any{
			map[string]any{"acceptanceId": "AC-CORE-1", "required": true, "automated": map[string]any{
				"status": "passed", "evidence": "passed", "updatedAt": "2026-10-27T11:00:00Z",
				"command": "go test ./...", "exitCode": 0, "evidenceFile": "reports/verification.txt",
			}},
			map[string]any{"acceptanceId": "AC-UX-2", "required": true, "automated": map[string]any{
				"status": "failed", "evidence": "failed", "updatedAt": "2026-10-27T11:01:00Z",
				"command": "npm test", "exitCode": 2,
			}, "manual": map[string]any{
				"status": "manual_required", "evidence": "awaiting human verification", "updatedAt": "2026-10-27T11:02:00Z",
			}},
		},
	}
	for name, value := range map[string]any{
		"metadata.json": metadata, "requirements.json": requirements,
		"review-ledger.json": review, "verification-ledger.json": verification,
	} {
		writeMancodeReviewCLIJSON(t, filepath.Join(workflowRoot, name), value)
	}
	if err := os.WriteFile(filepath.Join(workflowRoot, "plan.md"), []byte("# Legacy plan\n\nReviewed as inert history.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workflowRoot, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"quality-review.md", "verification.txt"} {
		if err := os.WriteFile(filepath.Join(workflowRoot, "reports", name), []byte("historical evidence\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	secondTaskID := "20261027-review-cli-second"
	secondWorkflowRoot := filepath.Join(sourceRoot, ".mancode", "workflows", secondTaskID)
	copyMigrationSourceTree(t, workflowRoot, secondWorkflowRoot)
	metadataPath := filepath.Join(secondWorkflowRoot, "metadata.json")
	var secondMetadata map[string]any
	if err := decodeJSONFile(metadataPath, 1<<20, &secondMetadata); err != nil {
		t.Fatal(err)
	}
	secondMetadata["taskId"] = secondTaskID
	writeMancodeReviewCLIJSON(t, metadataPath, secondMetadata)
	manifest, err := migrate.CreateSnapshot(migrate.Mancode, sourceRoot, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(migrate.Mancode), manifest.SourceFingerprint, "manifest.json")
	backupRoot := filepath.Join(t.TempDir(), "independent-copy")
	copyMigrationSourceTree(t, sourceRoot, backupRoot)
	coverage, err := migrate.BuildFormatCoverage(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage.MancodeLegacyWorkflowChecks) != 2 || coverage.MancodeLegacyWorkflowChecks[0].Status != "verified" ||
		coverage.MancodeLegacyWorkflowChecks[1].Status != "verified" || coverage.MancodeLegacyWorkflowChecks[0].MetadataPlanVersion != 2 {
		t.Fatalf("fixture did not produce a verified plan binding: %#v", coverage.MancodeLegacyWorkflowChecks)
	}
	workflowID := coverage.MancodeLegacyWorkflowChecks[0].WorkflowIDSHA256
	secondWorkflowID := coverage.MancodeLegacyWorkflowChecks[1].WorkflowIDSHA256
	identity, public, err := teamauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(t.TempDir(), "reviewer-private.json")
	publicPath := filepath.Join(t.TempDir(), "reviewer-public.json")
	if err := teamauth.SaveIdentity(identityPath, publicPath, identity, public); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	trust := migrationreview.TrustStore{SchemaVersion: migrationreview.TrustStoreSchemaVersion, Keys: []migrationreview.ReviewerKey{{
		KeyID: identity.KeyID, PublicKey: public.PublicKey, Reviewer: "legacy-plan-reviewer", Products: []string{"mancode"},
		NotBefore: now.Add(-time.Hour).Format(time.RFC3339Nano), NotAfter: now.Add(time.Hour).Format(time.RFC3339Nano),
	}}}
	trustPath := filepath.Join(t.TempDir(), "reviewer-trust.json")
	writeMancodeReviewCLIJSON(t, trustPath, trust)
	outputPath := filepath.Join(t.TempDir(), "reviewed-coverage.json")
	args := []string{"migrate", "review-plan", "--manifest", manifestPath, "--backup-root", backupRoot,
		"--output", outputPath, "--workflow-id-sha256", workflowID, "--identity-key", identityPath,
		"--trusted-reviewers", trustPath, "--confirm-reviewed-pairing"}
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("review-plan code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var updated migrationcoverage.Report
	if err := decodeJSONFile(outputPath, 32<<20, &updated); err != nil {
		t.Fatal(err)
	}
	if err := migrationcoverage.Validate(updated); err != nil {
		t.Fatalf("signed CLI coverage is invalid: %v", err)
	}
	attestation := updated.MancodeLegacyWorkflowChecks[0].PlanReview
	if attestation == nil || updated.MancodeLegacyWorkflowChecks[0].PlanContentBindingVerified || attestation.MetadataPlanVersion != 2 {
		t.Fatalf("review did not remain separate from historical binding: %#v", attestation)
	}
	binding := attestation.Binding
	if err := migrationreview.Verify(*attestation, trust, binding); err != nil {
		t.Fatalf("CLI emitted an unverifiable reviewer signature: %v", err)
	}
	firstWorkflowOutput := outputPath
	secondOutput := filepath.Join(t.TempDir(), "second-reviewed-coverage.json")
	secondArgs := append(append([]string(nil), args...), "--previous-coverage", firstWorkflowOutput)
	secondArgs[7] = secondOutput
	secondArgs[9] = secondWorkflowID
	stdout.Reset()
	stderr.Reset()
	if code := Run(secondArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("review-plan did not append a second verified workflow signature: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var fullyReviewed migrationcoverage.Report
	if err := decodeJSONFile(secondOutput, 32<<20, &fullyReviewed); err != nil {
		t.Fatal(err)
	}
	if err := migrationcoverage.Validate(fullyReviewed); err != nil {
		t.Fatalf("multi-review coverage is invalid: %v", err)
	}
	signedChecks := 0
	for _, check := range fullyReviewed.MancodeLegacyWorkflowChecks {
		if check.PlanReview != nil {
			signedChecks++
		}
	}
	if signedChecks != 2 {
		t.Fatalf("multi-workflow review only signed %d of 2 workflows", signedChecks)
	}
	before, err := os.ReadFile(secondOutput)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(secondArgs, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "file exists") {
		t.Fatalf("review command overwrote existing coverage: code=%d stderr=%s", code, stderr.String())
	}
	after, err := os.ReadFile(secondOutput)
	if err != nil || string(before) != string(after) {
		t.Fatalf("existing reviewed coverage changed after rejected overwrite: %v", err)
	}
	badBackup := filepath.Join(backupRoot, ".mancode", "workflows", taskID, "plan.md")
	if err := os.WriteFile(badBackup, []byte("tampered backup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockedOutput := filepath.Join(t.TempDir(), "must-not-exist.json")
	badBackupArgs := append([]string(nil), secondArgs...)
	badBackupArgs[7] = blockedOutput
	stdout.Reset()
	stderr.Reset()
	if code := Run(badBackupArgs, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "source backup must verify") {
		t.Fatalf("review accepted a mismatched backup: code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(blockedOutput); !os.IsNotExist(err) {
		t.Fatalf("blocked review created output file: %v", err)
	}
}

func cliLegacyRequirementsFixture() map[string]any {
	dimensions := []string{"platform", "core_scope", "technical_stack", "data_and_persistence", "performance", "compatibility", "security"}
	coverage := make([]any, 0, len(dimensions))
	for _, dimension := range dimensions {
		coverage = append(coverage, map[string]any{"dimension": dimension, "status": "confirmed", "rationale": "private rationale for " + dimension})
	}
	return map[string]any{
		"version": 1, "goal": "  private requirements goal  ", "confirmedScope": []any{" in-scope marker "},
		"excludedScope": []any{"out-of-scope marker"}, "technicalDecisions": []any{"use a local database"}, "defaults": []any{"default marker"},
		"blockingUnknowns": []any{}, "coverage": coverage, "acceptanceCriteria": []any{
			map[string]any{"id": "AC-CORE-1", "description": "private acceptance criterion", "required": true,
				"method": "automated", "verificationSurfaces": map[string]any{"automated": "unit"}},
			map[string]any{"id": "AC-UX-2", "description": "manual and automated criterion", "required": true,
				"method": "hybrid", "verificationSurfaces": map[string]any{"automated": "browser", "manual": "manual_observation"}},
		},
	}
}

func writeMancodeReviewCLIJSON(t *testing.T, filename string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
