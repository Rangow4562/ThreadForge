package cutover

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
)

func TestAssessRequiresAllParityMigrationAdaptersAndEvaluations(t *testing.T) {
	root := t.TempDir()
	report, evidence, trust := completeFixture(t, root)
	assessment, err := AssessWithTrust(report, root, trust)
	if err != nil {
		t.Fatal(err)
	}
	if !assessment.Ready || len(assessment.Blockers) != 0 {
		t.Fatalf("complete evidence was rejected: %#v", assessment)
	}
	withoutServingBenchmark := report
	withoutServingBenchmark.Features = append([]FeatureEvidence(nil), report.Features...)
	filteredFeatures := withoutServingBenchmark.Features[:0]
	for _, feature := range withoutServingBenchmark.Features {
		if feature.ID != "anyjev.serving-benchmark" {
			filteredFeatures = append(filteredFeatures, feature)
		}
	}
	withoutServingBenchmark.Features = filteredFeatures
	assessment, err = AssessWithTrust(withoutServingBenchmark, root, trust)
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "missing feature parity evidence: anyjev.serving-benchmark") {
		t.Fatalf("cutover must require a distinct AnyJev serving-benchmark gate: %#v, %v", assessment, err)
	}

	report.Evaluations[0].CandidateRepeatFailureRate = report.Evaluations[0].BaselineRepeatFailureRate
	report.Evaluations[0].CandidateTaskSuccessRate = report.Evaluations[0].BaselineTaskSuccessRate - 0.01
	report.Adapters[0].Tests["configuration-preservation"] = nil
	assessment, err = AssessWithTrust(report, root, trust)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Ready || !hasBlocker(assessment.Blockers, "repeat failures beyond") ||
		!hasBlocker(assessment.Blockers, "regresses task success") ||
		!hasBlocker(assessment.Blockers, "configuration-preservation") {
		t.Fatalf("acceptance regressions were not blocked: %#v", assessment)
	}

	if err := os.WriteFile(filepath.Join(root, evidence.Path), []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assessment, err = AssessWithTrust(report, root, trust)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Ready || !hasBlocker(assessment.Blockers, "digest mismatch") {
		t.Fatalf("tampered evidence was not blocked: %#v", assessment)
	}
}

func TestAssessRequiresAndValidatesMigrationFormatCoverage(t *testing.T) {
	root := t.TempDir()
	report, _, trust := completeFixture(t, root)

	missing := report
	missing.Migrations = append([]MigrationEvidence(nil), report.Migrations...)
	missing.Migrations[0].FormatCoverage = Evidence{}
	assessment, err := AssessWithTrust(missing, root, trust)
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "format coverage") {
		t.Fatalf("migration without format coverage was accepted: %#v, %v", assessment, err)
	}

	coveragePath := filepath.Join(root, report.Migrations[0].FormatCoverage.Path)
	content, err := os.ReadFile(coveragePath)
	if err != nil {
		t.Fatal(err)
	}
	var coverage migrationcoverage.Report
	if err := json.Unmarshal(content, &coverage); err != nil {
		t.Fatal(err)
	}
	coverage.SourceFingerprint = strings.Repeat("d", 64)
	content, err = json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coveragePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	report.Migrations[0].FormatCoverage.SHA256 = hex.EncodeToString(digest[:])
	assessment, err = AssessWithTrust(report, root, trust)
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "does not match migration fingerprints") {
		t.Fatalf("coverage with a mismatched source fingerprint was accepted: %#v, %v", assessment, err)
	}
}

func TestAssessBlocksIncompleteMancodePrivacyBindingCoverage(t *testing.T) {
	root := t.TempDir()
	report, _, trust := completeFixture(t, root)
	var mancodeMigration *MigrationEvidence
	for index := range report.Migrations {
		if report.Migrations[index].Product == "mancode" {
			mancodeMigration = &report.Migrations[index]
			break
		}
	}
	if mancodeMigration == nil {
		t.Fatal("Mancode migration fixture is missing")
	}
	coveragePath := filepath.Join(root, mancodeMigration.FormatCoverage.Path)
	content, err := os.ReadFile(coveragePath)
	if err != nil {
		t.Fatal(err)
	}
	var coverage migrationcoverage.Report
	if err := json.Unmarshal(content, &coverage); err != nil {
		t.Fatal(err)
	}
	verified := migrationcoverage.CrossFileCheck{
		SchemaVersion: migrationcoverage.MancodePrivacyBindingSchemaVersion,
		Relationship:  "mancode_privacy_policy_exclusions", PairIDSHA256: strings.Repeat("1", 64),
		PolicyFileCount: 1, PolicyPresent: true, PolicyValid: true, PolicySourceSetSHA256: strings.Repeat("2", 64),
		ExclusionsFileCount: 1, ExclusionsPresent: true, ExclusionsValid: true, ExclusionsSourceSetSHA256: strings.Repeat("3", 64),
		PolicyRevision: 3, ExpectedExclusionsRevision: 2, ActualExclusionsRevision: 2,
		WorkspaceBindingVerified: true, RevisionBindingVerified: true, DigestBindingVerified: true,
		Status: "verified", Issues: []string{}, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
	}
	for index := range coverage.Formats {
		switch coverage.Formats[index].ID {
		case "mancode.privacy-policy":
			coverage.Formats[index].Files = 1
		case "mancode.privacy-exclusions":
			coverage.Formats[index].Files = 1
		}
	}
	coverage.FilesScanned += 2
	coverage.CrossFileChecks = []migrationcoverage.CrossFileCheck{verified}
	writeCoverage := func() {
		t.Helper()
		if err := migrationcoverage.Validate(coverage); err != nil {
			t.Fatalf("invalid fixture coverage: %v", err)
		}
		updated, err := json.Marshal(coverage)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(coveragePath, updated, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(updated)
		mancodeMigration.FormatCoverage.SHA256 = hex.EncodeToString(digest[:])
	}
	writeCoverage()
	assessment, err := AssessWithTrust(report, root, trust)
	if err != nil || !assessment.Ready {
		t.Fatalf("verified Mancode privacy binding blocked a complete report: %#v, %v", assessment, err)
	}

	incomplete := verified
	incomplete.ExclusionsFileCount = 0
	incomplete.ExclusionsPresent = false
	incomplete.ExclusionsValid = false
	incomplete.ExclusionsSourceSetSHA256 = ""
	incomplete.ActualExclusionsRevision = 0
	incomplete.WorkspaceBindingVerified = false
	incomplete.RevisionBindingVerified = false
	incomplete.DigestBindingVerified = false
	incomplete.Status = "incomplete"
	incomplete.Issues = []string{"exclusions_missing"}
	for index := range coverage.Formats {
		if coverage.Formats[index].ID == "mancode.privacy-exclusions" {
			coverage.Formats[index].Files = 0
		}
	}
	coverage.FilesScanned--
	coverage.CrossFileChecks = []migrationcoverage.CrossFileCheck{incomplete}
	writeCoverage()
	assessment, err = AssessWithTrust(report, root, trust)
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "cross-file privacy binding") {
		t.Fatalf("incomplete Mancode privacy binding was not blocked: %#v, %v", assessment, err)
	}
}

func TestAssessBlocksUnclassifiedMigrationSourceFiles(t *testing.T) {
	root := t.TempDir()
	report, _, trust := completeFixture(t, root)
	migration := &report.Migrations[0]
	path := filepath.Join(root, migration.FormatCoverage.Path)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var coverage migrationcoverage.Report
	if err := json.Unmarshal(content, &coverage); err != nil {
		t.Fatal(err)
	}
	coverage.Formats = append(coverage.Formats, migrationcoverage.Format{
		ID: migrationcoverage.UnclassifiedFormat, Files: 1, FileSetSHA256: testDigest("unknown-file"),
	})
	coverage.FilesScanned++
	coverage.UnclassifiedFiles++
	content, err = json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	migration.FormatCoverage.SHA256 = hex.EncodeToString(digest[:])
	assessment, err := AssessWithTrust(report, root, trust)
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "unclassified or unsupported source formats") {
		t.Fatalf("unclassified source format was accepted: %#v, %v", assessment, err)
	}
}

func TestAssessBlocksKnownFormatsWithoutSemanticConverters(t *testing.T) {
	root := t.TempDir()
	report, _, trust := completeFixture(t, root)
	migration := &report.Migrations[0]
	path := filepath.Join(root, migration.FormatCoverage.Path)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var coverage migrationcoverage.Report
	if err := json.Unmarshal(content, &coverage); err != nil {
		t.Fatal(err)
	}
	coverage.Formats[0].UnconvertedFiles = 1
	coverage.UnconvertedFiles = 1
	content, err = json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	migration.FormatCoverage.SHA256 = hex.EncodeToString(digest[:])
	assessment, err := AssessWithTrust(report, root, trust)
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "without semantic converters") {
		t.Fatalf("known but unconverted source format was accepted: %#v, %v", assessment, err)
	}
}

func TestAssessDerivesMetricsFromTrialsAndNormalizesOverheadPerRun(t *testing.T) {
	root := t.TempDir()
	report, _, trust := completeFixture(t, root)
	evaluation := &report.Evaluations[0]
	if evaluation.BaselineRuns != 8 || evaluation.CandidateRuns != 4 || evaluation.CandidateCostUSD >= evaluation.BaselineCostUSD || evaluation.CandidateTokens >= evaluation.BaselineTokens {
		t.Fatalf("fixture must have fewer candidate total cost/tokens because it has fewer runs: %#v", evaluation)
	}
	assessment, err := AssessWithTrust(report, root, trust)
	if err != nil || !assessment.Ready {
		t.Fatalf("run-normalized fixture should pass: %#v, %v", assessment, err)
	}
	evaluation.CandidateTokens++
	assessment, err = AssessWithTrust(report, root, trust)
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "declared metrics do not match") {
		t.Fatalf("caller-edited metrics should be rejected: %#v, %v", assessment, err)
	}

	trials := fixtureTrials(t, evaluation.Domain)
	for runIndex := range trials.CandidateRuns {
		for taskIndex := range trials.CandidateRuns[runIndex].Tasks {
			trials.CandidateRuns[runIndex].Tasks[taskIndex].CostUSD = 0.111
		}
	}
	refreshEvaluation(t, root, evaluation, trials)
	assessment, err = AssessWithTrust(report, root, trust)
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "over-budget measured USD cost") {
		t.Fatalf("candidate per-run cost regression must be blocked despite lower total cost: %#v, %v", assessment, err)
	}

	trials = fixtureTrials(t, evaluation.Domain)
	for runIndex := range trials.CandidateRuns {
		for taskIndex := range trials.CandidateRuns[runIndex].Tasks {
			trials.CandidateRuns[runIndex].Tasks[taskIndex].Tokens = 12
		}
	}
	refreshEvaluation(t, root, evaluation, trials)
	assessment, err = AssessWithTrust(report, root, trust)
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "measured token usage") {
		t.Fatalf("candidate per-run token regression must be blocked despite lower total tokens: %#v, %v", assessment, err)
	}
}

func TestAssessRejectsChangedTaskOutcomeArtifact(t *testing.T) {
	root := t.TempDir()
	report, _, trust := completeFixture(t, root)
	path := filepath.Join(root, report.Evaluations[0].OutcomeArtifact.Path)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content = append(content, ' ')
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	assessment, err := AssessWithTrust(report, root, trust)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Ready || !hasBlocker(assessment.Blockers, "outcome artifact: artifact digest mismatch") {
		t.Fatalf("modified task-level outcomes were accepted: %#v", assessment)
	}
}

func TestAssessmentRequiresExternalTrustedEvaluatorAndValidSignature(t *testing.T) {
	root := t.TempDir()
	report, _, trust := completeFixture(t, root)
	assessment, err := Assess(report, root)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Ready || !hasBlocker(assessment.Blockers, "trusted evaluator policy") {
		t.Fatalf("assessment without a trust policy must remain blocked: %#v", assessment)
	}

	wrongScope := trust
	wrongScope.Keys = append([]TrustedEvaluatorKey(nil), trust.Keys...)
	wrongScope.Keys[0].Domains = []string{"mancode"}
	assessment, err = AssessWithTrust(report, root, wrongScope)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Ready || !hasBlocker(assessment.Blockers, "not trusted for this evaluator and domain") {
		t.Fatalf("out-of-scope evaluator key was accepted: %#v", assessment)
	}
	expiredKey := trust
	expiredKey.Keys = append([]TrustedEvaluatorKey(nil), trust.Keys...)
	expiredKey.Keys[0].NotAfter = "2026-10-01T11:00:00Z"
	assessment, err = AssessWithTrust(report, root, expiredKey)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Ready || !hasBlocker(assessment.Blockers, "outside the trusted key validity window") {
		t.Fatalf("expired evaluator key was accepted: %#v", assessment)
	}

	assessment, err = AssessWithTrust(report, root, trust)
	if err != nil || !assessment.Ready {
		t.Fatalf("correctly scoped signature was rejected: %#v, %v", assessment, err)
	}
	wrongCandidate := report
	wrongCandidate.Candidate = strings.Repeat("c", 40)
	assessment, err = AssessWithTrust(wrongCandidate, root, trust)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Ready || !hasBlocker(assessment.Blockers, "claims do not match") {
		t.Fatalf("attestation was replayed for a different candidate commit: %#v", assessment)
	}

	attestationPath := filepath.Join(root, report.Evaluations[0].Attestation.Path)
	attestationBytes, err := os.ReadFile(attestationPath)
	if err != nil {
		t.Fatal(err)
	}
	var attestation EvaluationAttestation
	if err := json.Unmarshal(attestationBytes, &attestation); err != nil {
		t.Fatal(err)
	}
	replacement := "A"
	if strings.HasPrefix(attestation.Signature, replacement) {
		replacement = "B"
	}
	attestation.Signature = replacement + attestation.Signature[1:]
	attestationBytes, err = json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attestationPath, attestationBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(attestationBytes)
	report.Evaluations[0].Attestation.SHA256 = hex.EncodeToString(digest[:])
	assessment, err = AssessWithTrust(report, root, trust)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Ready || !hasBlocker(assessment.Blockers, "signature verification failed") {
		t.Fatalf("re-signed digest must not legitimize a modified attestation: %#v", assessment)
	}
}

func TestEvaluatorTrustFingerprintIgnoresOrderingButBindsScope(t *testing.T) {
	trust := fixtureTrustStore()
	original, err := EvaluatorTrustStoreSHA256(trust)
	if err != nil {
		t.Fatal(err)
	}
	ordered := trust
	ordered.Keys = append([]TrustedEvaluatorKey(nil), trust.Keys...)
	ordered.Keys[0].Domains = append([]string(nil), trust.Keys[0].Domains...)
	for left, right := 0, len(ordered.Keys[0].Domains)-1; left < right; left, right = left+1, right-1 {
		ordered.Keys[0].Domains[left], ordered.Keys[0].Domains[right] = ordered.Keys[0].Domains[right], ordered.Keys[0].Domains[left]
	}
	permuted, err := EvaluatorTrustStoreSHA256(ordered)
	if err != nil || original != permuted {
		t.Fatalf("trust fingerprint should canonicalize key/domain order: %q != %q, %v", original, permuted, err)
	}
	ordered.Keys[0].Domains = []string{"beacon"}
	scoped, err := EvaluatorTrustStoreSHA256(ordered)
	if err != nil || scoped == original {
		t.Fatalf("trust fingerprint must bind allowed domains: %q vs %q, %v", original, scoped, err)
	}
}

func TestReadEvaluatorTrustStoreReturnsValidatedSemanticFingerprint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "trusted-evaluators.json")
	encoded, err := json.Marshal(fixtureTrustStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	trust, fingerprint, err := ReadEvaluatorTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := EvaluatorTrustStoreSHA256(fixtureTrustStore())
	if err != nil || fingerprint != expected || len(trust.Keys) != 1 {
		t.Fatalf("trust policy load/fingerprint mismatch: %s != %s, %v", fingerprint, expected, err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":"threadforge.cutover-evaluator-trust.v1","keys":[],"private_key":"forbidden"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadEvaluatorTrustStore(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("trust loader accepted an unknown/private key field: %v", err)
	}
}

func TestDeriveEvaluationMetricsRejectsUnmatchedOrRawTrialData(t *testing.T) {
	trials := fixtureTrials(t, "beacon")
	trials.CandidateRuns[0].Tasks[0].TaskSHA256 = strings.Repeat("f", 64)
	if _, err := DeriveEvaluationMetrics(trials); err == nil || !strings.Contains(err.Error(), "task set") {
		t.Fatalf("different candidate task set was accepted: %v", err)
	}
	trials = fixtureTrials(t, "beacon")
	trials.BaselineRuns[0].Tasks[0].FailureSignatureSHA256 = "raw error text"
	if _, err := DeriveEvaluationMetrics(trials); err == nil || !strings.Contains(err.Error(), "failure-signature digest") {
		t.Fatalf("raw failure text was accepted: %v", err)
	}
}

func TestNoiseFloorBootstrapIsSeededAndCapturesRunVariation(t *testing.T) {
	trials := fixtureTrials(t, "beacon")
	for runIndex := range trials.BaselineRuns {
		for taskIndex := range trials.BaselineRuns[runIndex].Tasks {
			outcome := &trials.BaselineRuns[runIndex].Tasks[taskIndex]
			outcome.Success = runIndex%2 == 1
			outcome.FailureSignatureSHA256 = ""
			if !outcome.Success {
				outcome.FailureSignatureSHA256 = testDigest("alternating-baseline-failure")
			}
		}
	}
	first, err := DeriveEvaluationMetrics(trials)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeriveEvaluationMetrics(trials)
	if err != nil {
		t.Fatal(err)
	}
	if first.NoiseFloor <= 0 || first.NoiseFloor != second.NoiseFloor {
		t.Fatalf("seeded bootstrap should be reproducible and reflect baseline run variation: %g vs %g", first.NoiseFloor, second.NoiseFloor)
	}
}

func TestAssessFailsClosedForUnsafeEvidenceAndUnresolvedMigration(t *testing.T) {
	root := t.TempDir()
	report, _, trust := completeFixture(t, root)
	report.Migrations[0].UnresolvedConflicts = 1
	report.Migrations[0].CredentialRecordsMigrated = 1
	report.Features[0].Evidence[0].Path = "../outside.json"
	assessment, err := AssessWithTrust(report, root, trust)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Ready || !hasBlocker(assessment.Blockers, "unresolved conflicts or migrated credentials") ||
		!hasBlocker(assessment.Blockers, "unsafe segment") {
		t.Fatalf("unsafe or unreconciled report was not blocked: %#v", assessment)
	}
}

func TestRequiredSourceInventoryMatchesDocumentedMatrix(t *testing.T) {
	matrix, err := os.ReadFile(filepath.Join("..", "..", "docs", "source-matrix.md"))
	if err != nil {
		t.Fatal(err)
	}
	document := string(matrix)
	beaconTable := strings.SplitN(document, "The source tree also documents", 2)[0]
	linePattern := regexp.MustCompile("`([^`]+\\.mdx)`")
	documentedBeacon := map[string]bool{}
	for _, match := range linePattern.FindAllStringSubmatch(beaconTable, -1) {
		documentedBeacon["beacon/"+strings.TrimSuffix(match[1], ".mdx")] = true
	}
	requiredBeacon := map[string]bool{}
	for _, source := range RequiredSources {
		if strings.HasPrefix(source, "beacon/") {
			requiredBeacon[source] = true
		}
	}
	if len(documentedBeacon) != len(requiredBeacon) {
		t.Fatalf("Beacon cutover source count %d does not match docs count %d", len(requiredBeacon), len(documentedBeacon))
	}
	for source := range documentedBeacon {
		if !requiredBeacon[source] {
			t.Errorf("documented Beacon source %q is absent from cutover requirements", source)
		}
	}
	for source := range requiredBeacon {
		if !documentedBeacon[source] {
			t.Errorf("cutover source %q is absent from Beacon source matrix", source)
		}
	}
	for _, platform := range []string{"Claude Code", "Cursor", "ChatGPT desktop Codex", "Codex CLI", "GitHub Copilot", "ZCode", "Kimi Code", "Qoder", "DeepSeek Harness"} {
		if !strings.Contains(document, platform) {
			t.Errorf("Mancode bootstrap target %q was removed from source matrix without updating cutover requirements", platform)
		}
	}
}

func completeFixture(t *testing.T, root string) (Report, Evidence, EvaluatorTrustStore) {
	t.Helper()
	content := []byte("{\"test_suite\":\"acceptance\",\"passed\":true}\n")
	if err := os.WriteFile(filepath.Join(root, "evidence.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	evidence := Evidence{Path: "evidence.json", SHA256: hex.EncodeToString(digest[:])}
	report := completeReportWithoutWriting(evidence)
	for i := range report.Migrations {
		report.Migrations[i].FormatCoverage = writeMigrationCoverageFixture(t, root, report.Migrations[i])
	}
	trust := fixtureTrustStore()
	for i := range report.Evaluations {
		trials := fixtureTrials(t, report.Evaluations[i].Domain)
		path := fmt.Sprintf("trials-%s.json", report.Evaluations[i].Domain)
		report.Evaluations[i].OutcomeArtifact = Evidence{Path: path}
		report.Evaluations[i].Attestation = Evidence{Path: fmt.Sprintf("attestation-%s.json", report.Evaluations[i].Domain)}
		refreshEvaluation(t, root, &report.Evaluations[i], trials)
	}
	return report, evidence, trust
}

func writeMigrationCoverageFixture(t *testing.T, root string, migration MigrationEvidence) Evidence {
	t.Helper()
	coverage := migrationcoverage.Report{
		SchemaVersion: migrationcoverage.SchemaVersion, Product: migration.Product,
		SourceFingerprint: migration.SourceFingerprint, SnapshotSHA256: migration.SnapshotSHA256,
		FilesScanned: 1, SourceBytes: 4, RecordsFound: 4, RecordsImported: 3,
		CredentialExcludedRecords: 1, SnapshotFiles: 1,
	}
	for i, id := range migrationcoverage.RequiredFormats(migration.Product) {
		row := migrationcoverage.Format{ID: id, FileSetSHA256: testDigest(migration.Product + "/" + id)}
		if i == 0 {
			row.Files, row.SourceBytes, row.RecordsFound, row.RecordsImported = 1, 4, 4, 3
			row.CredentialExcludedRecords, row.SnapshotFiles = 1, 1
		}
		coverage.Formats = append(coverage.Formats, row)
	}
	if err := migrationcoverage.Validate(coverage); err != nil {
		t.Fatalf("invalid migration coverage fixture: %v", err)
	}
	content, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	path := "migration-format-coverage-" + migration.Product + ".json"
	if err := os.WriteFile(filepath.Join(root, path), content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	return Evidence{Path: path, SHA256: hex.EncodeToString(digest[:])}
}

func completeReportWithoutWriting(evidence Evidence) Report {
	report := Report{SchemaVersion: SchemaVersion, Candidate: strings.Repeat("b", 40)}
	for _, id := range RequiredFeatures {
		report.Features = append(report.Features, FeatureEvidence{ID: id, Status: "complete", Evidence: []Evidence{evidence}})
	}
	for _, id := range RequiredSources {
		tests := make(map[string][]Evidence, len(RequiredAdapterTests))
		for _, testName := range RequiredAdapterTests {
			tests[testName] = []Evidence{evidence}
		}
		report.Adapters = append(report.Adapters, AdapterEvidence{
			SourceID: id, HostVersion: "1.0.0", RetrievalMode: "mcp", Tests: tests,
		})
	}
	for _, product := range RequiredMigrations {
		report.Migrations = append(report.Migrations, MigrationEvidence{
			Product: product, SourceFingerprint: strings.Repeat("a", 64), SnapshotSHA256: strings.Repeat("c", 64),
			SourceRecords: 4, ImportedRecords: 3, ExcludedRecords: 1, InterruptionRecoveryTested: true,
			RollbackRehearsed: true, Evidence: []Evidence{evidence},
		})
	}
	for i, id := range RequiredSources {
		report.CrossHarness = append(report.CrossHarness, CrossHarnessTest{
			SourceID: id, TargetID: RequiredSources[(i+1)%len(RequiredSources)],
			RetrievalMode: "mcp", Passed: true, Evidence: []Evidence{evidence},
		})
	}
	for _, domain := range RequiredEvaluationDomains {
		report.Evaluations = append(report.Evaluations, Evaluation{
			Domain: domain, LegacyVersion: "pinned-legacy", LegacyCommit: strings.Repeat("a", 40),
			DatasetSHA256: strings.Repeat("d", 64), TaskSetSHA256: strings.Repeat("f", 64), HeldOut: true, TaskCount: 100, BaselineRuns: minimumBaselineRuns, CandidateRuns: minimumCandidateRuns,
			BaselineRepeatFailureRate: 0.5, CandidateRepeatFailureRate: 0, NoiseFloor: 0,
			BaselineTaskSuccessRate: 0.5, CandidateTaskSuccessRate: 0.875,
			BaselineCostUSD: 80, CandidateCostUSD: 42, MaxCostOverheadRatio: 0.10,
			BaselineTokens: 8000, CandidateTokens: 4400, MaxTokenOverheadRatio: 0.10,
			Evidence: []Evidence{evidence},
		})
	}
	for _, role := range RequiredApproverRoles {
		report.Approvals = append(report.Approvals, Approval{
			Role: role, Reviewer: "reviewer", Approved: true, Reason: "acceptance evidence reviewed",
			ApprovedAt: "2026-09-24T12:00:00Z", Evidence: []Evidence{evidence},
		})
	}
	return report
}

func fixtureTrials(t *testing.T, domain string) EvaluationTrials {
	t.Helper()
	trials := EvaluationTrials{
		SchemaVersion: EvaluationTrialsSchemaVersion, Domain: domain, LegacyVersion: "pinned-legacy",
		LegacyCommit: strings.Repeat("a", 40), DatasetSHA256: strings.Repeat("d", 64), HeldOut: true,
		Evaluator:        Evaluator{ID: "fixture-evaluator", Version: "1.0.0", SHA256: strings.Repeat("e", 64)},
		NoiseCalibration: NoiseCalibration{Method: noiseCalibrationMethod, Seed: 17, BootstrapResamples: 200, ConfidenceLevel: 0.95},
	}
	for runIndex := 0; runIndex < minimumBaselineRuns; runIndex++ {
		run := EvaluationRun{RunID: testDigest(fmt.Sprintf("baseline-%s-%d", domain, runIndex))}
		for taskIndex := 0; taskIndex < 100; taskIndex++ {
			failed := taskIndex%2 == 0
			outcome := TaskOutcome{TaskSHA256: testDigest(fmt.Sprintf("%s-task-%d", domain, taskIndex)), Success: !failed, CostUSD: 0.1, Tokens: 10}
			if failed {
				outcome.FailureSignatureSHA256 = testDigest("same-baseline-failure")
			}
			run.Tasks = append(run.Tasks, outcome)
		}
		trials.BaselineRuns = append(trials.BaselineRuns, run)
	}
	for runIndex := 0; runIndex < minimumCandidateRuns; runIndex++ {
		run := EvaluationRun{RunID: testDigest(fmt.Sprintf("candidate-%s-%d", domain, runIndex))}
		for taskIndex := 0; taskIndex < 100; taskIndex++ {
			failed := runIndex == 0 && taskIndex%2 == 0
			outcome := TaskOutcome{TaskSHA256: testDigest(fmt.Sprintf("%s-task-%d", domain, taskIndex)), Success: !failed, CostUSD: 0.105, Tokens: 11}
			if failed {
				outcome.FailureSignatureSHA256 = testDigest("same-candidate-failure")
			}
			run.Tasks = append(run.Tasks, outcome)
		}
		trials.CandidateRuns = append(trials.CandidateRuns, run)
	}
	return trials
}

func refreshEvaluation(t *testing.T, root string, evaluation *Evaluation, trials EvaluationTrials) {
	t.Helper()
	metrics, err := DeriveEvaluationMetrics(trials)
	if err != nil {
		t.Fatal(err)
	}
	evaluation.TaskCount = metrics.TaskCount
	evaluation.TaskSetSHA256 = metrics.TaskSetSHA256
	evaluation.BaselineRuns = metrics.BaselineRuns
	evaluation.CandidateRuns = metrics.CandidateRuns
	evaluation.BaselineRepeatFailureRate = metrics.BaselineRepeatFailureRate
	evaluation.CandidateRepeatFailureRate = metrics.CandidateRepeatFailureRate
	evaluation.NoiseFloor = metrics.NoiseFloor
	evaluation.BaselineTaskSuccessRate = metrics.BaselineTaskSuccessRate
	evaluation.CandidateTaskSuccessRate = metrics.CandidateTaskSuccessRate
	evaluation.BaselineCostUSD = metrics.BaselineCostUSD
	evaluation.CandidateCostUSD = metrics.CandidateCostUSD
	evaluation.BaselineTokens = metrics.BaselineTokens
	evaluation.CandidateTokens = metrics.CandidateTokens
	encoded, err := json.Marshal(trials)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, evaluation.OutcomeArtifact.Path), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	evaluation.OutcomeArtifact.SHA256 = hex.EncodeToString(digest[:])
	attestation, err := SignEvaluationAttestation(EvaluationAttestationClaims{
		EvaluatorID: trials.Evaluator.ID, Domain: trials.Domain, LegacyCommit: trials.LegacyCommit,
		CandidateCommit: strings.Repeat("b", 40), DatasetSHA256: trials.DatasetSHA256,
		TaskSetSHA256: metrics.TaskSetSHA256, EvaluatorSHA256: trials.Evaluator.SHA256,
		OutcomeSHA256: evaluation.OutcomeArtifact.SHA256, SplitRole: "held_out", IssuedAt: "2026-10-01T12:00:00Z",
	}, fixturePrivateKey())
	if err != nil {
		t.Fatal(err)
	}
	attestationBytes, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, evaluation.Attestation.Path), attestationBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	attestationDigest := sha256.Sum256(attestationBytes)
	evaluation.Attestation.SHA256 = hex.EncodeToString(attestationDigest[:])
}

func fixtureTrustStore() EvaluatorTrustStore {
	privateKey := fixturePrivateKey()
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return EvaluatorTrustStore{SchemaVersion: EvaluatorTrustStoreSchemaVersion, Keys: []TrustedEvaluatorKey{{
		KeyID: evaluatorKeyID(publicKey), PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		EvaluatorID: "fixture-evaluator", Domains: append([]string(nil), RequiredEvaluationDomains...),
		NotBefore: "2026-01-01T00:00:00Z", NotAfter: "2027-01-01T00:00:00Z",
	}}}
}

func fixturePrivateKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("Threadforge cutover test evaluator signing key"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func testDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func hasBlocker(blockers []string, fragment string) bool {
	for _, blocker := range blockers {
		if strings.Contains(blocker, fragment) {
			return true
		}
	}
	return false
}
