package cutover

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const SchemaVersion = "threadforge.cutover-evidence.v4"

const (
	maxArtifactBytes = 128 << 20
	maxEvidenceFiles = 10000
	maxEvidenceBytes = 10 << 30
)

var digestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var gitCommitRE = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

type Report struct {
	SchemaVersion string              `json:"schema_version"`
	Candidate     string              `json:"candidate_commit"`
	Features      []FeatureEvidence   `json:"features"`
	Adapters      []AdapterEvidence   `json:"adapters"`
	Migrations    []MigrationEvidence `json:"migrations"`
	CrossHarness  []CrossHarnessTest  `json:"cross_harness_tests"`
	Evaluations   []Evaluation        `json:"evaluations"`
	Approvals     []Approval          `json:"approvals"`
}

type Evidence struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type FeatureEvidence struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Evidence []Evidence `json:"evidence"`
}

type AdapterEvidence struct {
	SourceID      string                `json:"source_id"`
	HostVersion   string                `json:"host_version"`
	RetrievalMode string                `json:"retrieval_mode"`
	Tests         map[string][]Evidence `json:"tests"`
}

type MigrationEvidence struct {
	Product                    string     `json:"product"`
	SourceFingerprint          string     `json:"source_fingerprint"`
	SnapshotSHA256             string     `json:"snapshot_sha256"`
	FormatCoverage             Evidence   `json:"format_coverage"`
	SourceRecords              int        `json:"source_records"`
	ImportedRecords            int        `json:"imported_records"`
	ExcludedRecords            int        `json:"excluded_records"`
	UnresolvedConflicts        int        `json:"unresolved_conflicts"`
	CredentialRecordsMigrated  int        `json:"credential_records_migrated"`
	InterruptionRecoveryTested bool       `json:"interruption_recovery_tested"`
	RollbackRehearsed          bool       `json:"rollback_rehearsed"`
	Evidence                   []Evidence `json:"evidence"`
}

type CrossHarnessTest struct {
	SourceID      string     `json:"source_id"`
	TargetID      string     `json:"target_id"`
	RetrievalMode string     `json:"retrieval_mode"`
	Passed        bool       `json:"passed"`
	Evidence      []Evidence `json:"evidence"`
}

type Evaluation struct {
	Domain                     string     `json:"domain"`
	LegacyVersion              string     `json:"legacy_version"`
	LegacyCommit               string     `json:"legacy_commit"`
	DatasetSHA256              string     `json:"dataset_sha256"`
	TaskSetSHA256              string     `json:"task_set_sha256"`
	HeldOut                    bool       `json:"held_out"`
	TaskCount                  int        `json:"task_count"`
	BaselineRuns               int        `json:"baseline_runs"`
	CandidateRuns              int        `json:"candidate_runs"`
	BaselineRepeatFailureRate  float64    `json:"baseline_repeat_failure_rate"`
	CandidateRepeatFailureRate float64    `json:"candidate_repeat_failure_rate"`
	NoiseFloor                 float64    `json:"noise_floor"`
	BaselineTaskSuccessRate    float64    `json:"baseline_task_success_rate"`
	CandidateTaskSuccessRate   float64    `json:"candidate_task_success_rate"`
	BaselineCostUSD            float64    `json:"baseline_cost_usd"`
	CandidateCostUSD           float64    `json:"candidate_cost_usd"`
	MaxCostOverheadRatio       float64    `json:"max_cost_overhead_ratio"`
	BaselineTokens             int64      `json:"baseline_tokens"`
	CandidateTokens            int64      `json:"candidate_tokens"`
	MaxTokenOverheadRatio      float64    `json:"max_token_overhead_ratio"`
	OutcomeArtifact            Evidence   `json:"outcome_artifact"`
	Attestation                Evidence   `json:"attestation"`
	Evidence                   []Evidence `json:"evidence"`
}

type Approval struct {
	Role       string     `json:"role"`
	Reviewer   string     `json:"reviewer"`
	Approved   bool       `json:"approved"`
	Reason     string     `json:"reason"`
	ApprovedAt string     `json:"approved_at"`
	Evidence   []Evidence `json:"evidence"`
}

type Assessment struct {
	SchemaVersion           string   `json:"schema_version"`
	Ready                   bool     `json:"ready"`
	TrustedEvaluatorsSHA256 string   `json:"trusted_evaluators_sha256,omitempty"`
	Blockers                []string `json:"blockers"`
}

type artifactVerifier struct {
	root     string
	verified map[string]string
	files    int
	bytes    int64
}

func Assess(report Report, evidenceRoot string) (Assessment, error) {
	return AssessWithTrust(report, evidenceRoot, EvaluatorTrustStore{})
}

func AssessWithTrust(report Report, evidenceRoot string, trustedEvaluators EvaluatorTrustStore) (Assessment, error) {
	assessment := Assessment{SchemaVersion: "threadforge.cutover-assessment.v2", Blockers: []string{}}
	if report.SchemaVersion != SchemaVersion {
		assessment.Blockers = append(assessment.Blockers, "unsupported cutover evidence schema")
	}
	if !gitCommitRE.MatchString(report.Candidate) {
		assessment.Blockers = append(assessment.Blockers, "candidate_commit must be a pinned lowercase git commit")
	}
	root, err := checkedEvidenceRoot(evidenceRoot)
	if err != nil {
		return assessment, err
	}
	trustDigest, trustErr := EvaluatorTrustStoreSHA256(trustedEvaluators)
	if trustErr != nil {
		assessment.Blockers = append(assessment.Blockers, "trusted evaluator policy: "+trustErr.Error())
	} else {
		assessment.TrustedEvaluatorsSHA256 = trustDigest
	}
	verifier := &artifactVerifier{root: root, verified: make(map[string]string)}

	featureByID := make(map[string]FeatureEvidence, len(report.Features))
	for _, feature := range report.Features {
		if feature.ID == "" || featureByID[feature.ID].ID != "" {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("duplicate or empty feature evidence ID %q", feature.ID))
			continue
		}
		featureByID[feature.ID] = feature
	}
	for _, required := range RequiredFeatures {
		feature, ok := featureByID[required]
		if !ok {
			assessment.Blockers = append(assessment.Blockers, "missing feature parity evidence: "+required)
			continue
		}
		if feature.Status != "complete" {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("feature %s is %q, not complete", required, feature.Status))
		}
		verifyEvidence(&assessment, verifier, "feature "+required, feature.Evidence)
	}

	adapterByID := make(map[string]AdapterEvidence, len(report.Adapters))
	for _, adapter := range report.Adapters {
		if adapter.SourceID == "" || adapterByID[adapter.SourceID].SourceID != "" {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("duplicate or empty source adapter ID %q", adapter.SourceID))
			continue
		}
		adapterByID[adapter.SourceID] = adapter
	}
	for _, source := range RequiredSources {
		adapter, ok := adapterByID[source]
		if !ok {
			assessment.Blockers = append(assessment.Blockers, "missing documented host adapter evidence: "+source)
			continue
		}
		if strings.TrimSpace(adapter.HostVersion) == "" || len(adapter.HostVersion) > 128 {
			assessment.Blockers = append(assessment.Blockers, "host "+source+" requires an exact tested host version")
		}
		if adapter.RetrievalMode != "automatic" && adapter.RetrievalMode != "mcp" && adapter.RetrievalMode != "portable" {
			assessment.Blockers = append(assessment.Blockers, "host "+source+" has an invalid retrieval mode")
		}
		for _, testName := range RequiredAdapterTests {
			artifacts := adapter.Tests[testName]
			if len(artifacts) == 0 {
				assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("host %s is missing %s test evidence", source, testName))
				continue
			}
			verifyEvidence(&assessment, verifier, "host "+source+" "+testName, artifacts)
		}
	}

	migrationByProduct := make(map[string]MigrationEvidence, len(report.Migrations))
	for _, migration := range report.Migrations {
		if migration.Product == "" || migrationByProduct[migration.Product].Product != "" {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("duplicate or empty migration product %q", migration.Product))
			continue
		}
		migrationByProduct[migration.Product] = migration
	}
	for _, product := range RequiredMigrations {
		migration, ok := migrationByProduct[product]
		if !ok {
			assessment.Blockers = append(assessment.Blockers, "missing migration reconciliation for "+product)
			continue
		}
		if !digestRE.MatchString(migration.SourceFingerprint) || !digestRE.MatchString(migration.SnapshotSHA256) {
			assessment.Blockers = append(assessment.Blockers, "migration "+product+" requires verified source and snapshot SHA-256 digests")
		}
		if migration.SourceRecords < 0 || migration.ImportedRecords < 0 || migration.ExcludedRecords < 0 ||
			migration.ImportedRecords > migration.SourceRecords ||
			migration.ExcludedRecords != migration.SourceRecords-migration.ImportedRecords {
			assessment.Blockers = append(assessment.Blockers, "migration "+product+" source/import/exclusion counts do not reconcile")
		}
		if migration.UnresolvedConflicts != 0 || migration.CredentialRecordsMigrated != 0 {
			assessment.Blockers = append(assessment.Blockers, "migration "+product+" has unresolved conflicts or migrated credentials")
		}
		if !migration.InterruptionRecoveryTested || !migration.RollbackRehearsed {
			assessment.Blockers = append(assessment.Blockers, "migration "+product+" lacks interruption-recovery or rollback evidence")
		}
		verifyEvidence(&assessment, verifier, "migration "+product+" format coverage", []Evidence{migration.FormatCoverage})
		validateMigrationFormatCoverage(&assessment, verifier, migration)
		verifyEvidence(&assessment, verifier, "migration "+product, migration.Evidence)
	}

	coveredSources := make(map[string]bool, len(RequiredSources))
	for _, test := range report.CrossHarness {
		if test.SourceID == test.TargetID || adapterByID[test.SourceID].SourceID == "" || adapterByID[test.TargetID].SourceID == "" {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("invalid cross-harness pair %s -> %s", test.SourceID, test.TargetID))
			continue
		}
		if !test.Passed {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("cross-harness test %s -> %s did not pass", test.SourceID, test.TargetID))
		}
		if test.RetrievalMode != "automatic" && test.RetrievalMode != "mcp" && test.RetrievalMode != "portable" {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("cross-harness test %s -> %s has invalid retrieval mode", test.SourceID, test.TargetID))
		}
		coveredSources[test.SourceID], coveredSources[test.TargetID] = true, true
		verifyEvidence(&assessment, verifier, "cross-harness "+test.SourceID+" -> "+test.TargetID, test.Evidence)
	}
	for _, source := range RequiredSources {
		if !coveredSources[source] {
			assessment.Blockers = append(assessment.Blockers, "cross-harness tests do not cover "+source)
		}
	}

	evaluationByDomain := make(map[string]Evaluation, len(report.Evaluations))
	for _, evaluation := range report.Evaluations {
		if evaluation.Domain == "" || evaluationByDomain[evaluation.Domain].Domain != "" {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("duplicate or empty evaluation domain %q", evaluation.Domain))
			continue
		}
		evaluationByDomain[evaluation.Domain] = evaluation
	}
	for _, domain := range RequiredEvaluationDomains {
		evaluation, ok := evaluationByDomain[domain]
		if !ok {
			assessment.Blockers = append(assessment.Blockers, "missing held-out evaluation for "+domain)
			continue
		}
		if strings.TrimSpace(evaluation.LegacyVersion) == "" || !gitCommitRE.MatchString(evaluation.LegacyCommit) {
			assessment.Blockers = append(assessment.Blockers, "evaluation "+domain+" must pin the legacy version and commit")
		}
		if !digestRE.MatchString(evaluation.DatasetSHA256) || !evaluation.HeldOut || evaluation.TaskCount < 1 || evaluation.BaselineRuns != minimumBaselineRuns || evaluation.CandidateRuns < minimumCandidateRuns || evaluation.CandidateRuns > maximumCandidateRuns {
			assessment.Blockers = append(assessment.Blockers, "evaluation "+domain+" lacks held-out dataset identity, tasks, or repeated runs")
		}
		trials, trialErr := verifier.readEvaluationTrials(evaluation.OutcomeArtifact)
		if trialErr != nil {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("evaluation %s outcome artifact: %v", domain, trialErr))
		} else if metrics, deriveErr := DeriveEvaluationMetrics(trials); deriveErr != nil {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("evaluation %s outcome artifact: %v", domain, deriveErr))
		} else {
			if trials.Domain != evaluation.Domain || trials.LegacyVersion != evaluation.LegacyVersion || trials.LegacyCommit != evaluation.LegacyCommit ||
				trials.DatasetSHA256 != evaluation.DatasetSHA256 || trials.HeldOut != evaluation.HeldOut || metrics.TaskCount != evaluation.TaskCount ||
				metrics.TaskSetSHA256 != evaluation.TaskSetSHA256 || metrics.BaselineRuns != evaluation.BaselineRuns || metrics.CandidateRuns != evaluation.CandidateRuns {
				assessment.Blockers = append(assessment.Blockers, "evaluation "+domain+" report identity/counts do not match its outcome artifact")
			}
			if !closeMetric(metrics.BaselineRepeatFailureRate, evaluation.BaselineRepeatFailureRate) ||
				!closeMetric(metrics.CandidateRepeatFailureRate, evaluation.CandidateRepeatFailureRate) ||
				!closeMetric(metrics.NoiseFloor, evaluation.NoiseFloor) ||
				!closeMetric(metrics.BaselineTaskSuccessRate, evaluation.BaselineTaskSuccessRate) ||
				!closeMetric(metrics.CandidateTaskSuccessRate, evaluation.CandidateTaskSuccessRate) ||
				!closeMetric(metrics.BaselineCostUSD, evaluation.BaselineCostUSD) ||
				!closeMetric(metrics.CandidateCostUSD, evaluation.CandidateCostUSD) ||
				metrics.BaselineTokens != evaluation.BaselineTokens || metrics.CandidateTokens != evaluation.CandidateTokens {
				assessment.Blockers = append(assessment.Blockers, "evaluation "+domain+" declared metrics do not match its outcome artifact")
			}
			if trustErr == nil {
				attestation, attestationErr := verifier.readEvaluationAttestation(evaluation.Attestation)
				if attestationErr == nil {
					expected := EvaluationAttestationClaims{
						EvaluatorID: trials.Evaluator.ID, Domain: evaluation.Domain, LegacyCommit: evaluation.LegacyCommit,
						CandidateCommit: report.Candidate, DatasetSHA256: evaluation.DatasetSHA256,
						TaskSetSHA256: metrics.TaskSetSHA256, EvaluatorSHA256: trials.Evaluator.SHA256,
						OutcomeSHA256: evaluation.OutcomeArtifact.SHA256, SplitRole: "held_out",
					}
					attestationErr = VerifyEvaluationAttestation(attestation, trustedEvaluators, expected)
				}
				if attestationErr != nil {
					assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("evaluation %s trusted attestation: %v", domain, attestationErr))
				}
			}
		}
		if !unitInterval(evaluation.BaselineRepeatFailureRate) || !unitInterval(evaluation.CandidateRepeatFailureRate) || !unitInterval(evaluation.NoiseFloor) ||
			!unitInterval(evaluation.BaselineTaskSuccessRate) || !unitInterval(evaluation.CandidateTaskSuccessRate) {
			assessment.Blockers = append(assessment.Blockers, "evaluation "+domain+" rates/noise must be within [0,1]")
		} else {
			if evaluation.BaselineRepeatFailureRate-evaluation.CandidateRepeatFailureRate <= evaluation.NoiseFloor {
				assessment.Blockers = append(assessment.Blockers, "evaluation "+domain+" does not reduce repeat failures beyond its noise floor")
			}
			if evaluation.CandidateTaskSuccessRate < evaluation.BaselineTaskSuccessRate {
				assessment.Blockers = append(assessment.Blockers, "evaluation "+domain+" regresses task success")
			}
		}
		if !positiveFinite(evaluation.BaselineCostUSD) || !nonnegativeFinite(evaluation.CandidateCostUSD) ||
			!overheadWithin(evaluation.CandidateCostUSD/float64(max(evaluation.CandidateRuns, 1)), evaluation.BaselineCostUSD/float64(max(evaluation.BaselineRuns, 1)), evaluation.MaxCostOverheadRatio) {
			assessment.Blockers = append(assessment.Blockers, "evaluation "+domain+" has missing/over-budget measured USD cost")
		}
		if evaluation.BaselineTokens <= 0 || evaluation.CandidateTokens < 0 ||
			!overheadWithin(float64(evaluation.CandidateTokens)/float64(max(evaluation.CandidateRuns, 1)), float64(evaluation.BaselineTokens)/float64(max(evaluation.BaselineRuns, 1)), evaluation.MaxTokenOverheadRatio) {
			assessment.Blockers = append(assessment.Blockers, "evaluation "+domain+" has missing/over-budget measured token usage")
		}
		verifyEvidence(&assessment, verifier, "evaluation "+domain+" outcome", []Evidence{evaluation.OutcomeArtifact})
		verifyEvidence(&assessment, verifier, "evaluation "+domain+" attestation", []Evidence{evaluation.Attestation})
		verifyEvidence(&assessment, verifier, "evaluation "+domain, evaluation.Evidence)
	}

	approvalByRole := make(map[string]Approval, len(report.Approvals))
	for _, approval := range report.Approvals {
		if approval.Role == "" || approvalByRole[approval.Role].Role != "" {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("duplicate or empty approval role %q", approval.Role))
			continue
		}
		approvalByRole[approval.Role] = approval
	}
	for _, role := range RequiredApproverRoles {
		approval, ok := approvalByRole[role]
		if !ok || !approval.Approved || strings.TrimSpace(approval.Reviewer) == "" || len(approval.Reviewer) > 128 ||
			strings.TrimSpace(approval.Reason) == "" || len(approval.Reason) > 2000 {
			assessment.Blockers = append(assessment.Blockers, "missing explicit human approval: "+role)
			continue
		}
		if privacy.ContainsCredentialText(approval.Reviewer) || privacy.ContainsCredentialText(approval.Reason) {
			assessment.Blockers = append(assessment.Blockers, "approval "+role+" contains credential material")
		}
		if _, err := time.Parse(time.RFC3339Nano, approval.ApprovedAt); err != nil {
			assessment.Blockers = append(assessment.Blockers, "approval "+role+" requires an RFC3339 timestamp")
		}
		verifyEvidence(&assessment, verifier, "approval "+role, approval.Evidence)
	}

	sort.Strings(assessment.Blockers)
	assessment.Ready = len(assessment.Blockers) == 0
	return assessment, nil
}

func validateMigrationFormatCoverage(assessment *Assessment, verifier *artifactVerifier, migration MigrationEvidence) {
	content, actual, err := verifier.readEvidence(migration.FormatCoverage)
	if err != nil {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" format coverage: "+err.Error())
		return
	}
	if actual != migration.FormatCoverage.SHA256 {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" format coverage digest mismatch")
		return
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	var coverage migrationcoverage.Report
	if err := decoder.Decode(&coverage); err != nil {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" format coverage is invalid JSON: "+err.Error())
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" format coverage has trailing data")
		return
	}
	if err := migrationcoverage.Validate(coverage); err != nil {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" format coverage is invalid: "+err.Error())
		return
	}
	if coverage.Product != migration.Product || coverage.SourceFingerprint != migration.SourceFingerprint ||
		coverage.SnapshotSHA256 != migration.SnapshotSHA256 || coverage.RecordsFound != migration.SourceRecords ||
		coverage.RecordsImported != migration.ImportedRecords ||
		coverage.CredentialExcludedRecords+coverage.RejectedRecords != migration.ExcludedRecords {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" format coverage does not match migration fingerprints or aggregate counts")
	}
	if coverage.FilesScanned == 0 {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" has no scanned source files")
	}
	if coverage.UnclassifiedFiles != 0 || coverage.UnsupportedFiles != 0 {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" contains unclassified or unsupported source formats")
	}
	if coverage.UnconvertedFiles != 0 {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" contains source formats without semantic converters")
	}
	if coverage.RejectedRecords != 0 {
		assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" contains rejected source records")
	}
	for _, check := range coverage.CrossFileChecks {
		if check.Status != "verified" {
			assessment.Blockers = append(assessment.Blockers, "migration "+migration.Product+" has an incomplete or conflicting cross-file privacy binding")
		}
	}
}

func verifyEvidence(assessment *Assessment, verifier *artifactVerifier, label string, evidence []Evidence) {
	if len(evidence) == 0 {
		assessment.Blockers = append(assessment.Blockers, label+" has no evidence artifact")
		return
	}
	for _, item := range evidence {
		if !digestRE.MatchString(item.SHA256) {
			assessment.Blockers = append(assessment.Blockers, label+" has an invalid artifact digest")
			continue
		}
		actual, err := verifier.verify(item)
		if err != nil {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("%s evidence %q: %v", label, item.Path, err))
		} else if actual != item.SHA256 {
			assessment.Blockers = append(assessment.Blockers, fmt.Sprintf("%s evidence digest mismatch for %q", label, item.Path))
		}
	}
}

func (v *artifactVerifier) verify(evidence Evidence) (string, error) {
	relative := strings.ReplaceAll(evidence.Path, "\\", "/")
	if relative == "" || len(relative) > 1024 || strings.HasPrefix(relative, "/") || strings.Contains(relative, ":") {
		return "", errors.New("artifact path must be repository-relative")
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("artifact path contains an unsafe segment")
		}
	}
	if digest, ok := v.verified[relative]; ok {
		return digest, nil
	}
	_, actual, err := v.readEvidence(evidence)
	if err != nil {
		return "", err
	}
	return actual, nil
}

func (v *artifactVerifier) readEvidence(evidence Evidence) ([]byte, string, error) {
	relative := strings.ReplaceAll(evidence.Path, "\\", "/")
	if relative == "" || len(relative) > 1024 || strings.HasPrefix(relative, "/") || strings.Contains(relative, ":") {
		return nil, "", errors.New("artifact path must be repository-relative")
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return nil, "", errors.New("artifact path contains an unsafe segment")
		}
	}
	if v.files >= maxEvidenceFiles && v.verified[relative] == "" {
		return nil, "", errors.New("evidence file count exceeds 10000")
	}
	current := v.root
	for _, segment := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, "", errors.New("symlinked evidence paths are not allowed")
		}
	}
	info, err := os.Stat(current)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxArtifactBytes {
		return nil, "", errors.New("artifact must be a regular file no larger than 128 MiB")
	}
	if v.verified[relative] == "" && v.bytes > maxEvidenceBytes-info.Size() {
		return nil, "", errors.New("total evidence exceeds 10 GiB")
	}
	file, err := os.Open(current)
	if err != nil {
		return nil, "", err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxArtifactBytes+1))
	finalInfo, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil || int64(len(content)) != info.Size() || finalInfo.Size() != info.Size() || !finalInfo.ModTime().Equal(info.ModTime()) {
		return nil, "", errors.New("artifact changed or could not be read consistently")
	}
	digest := sha256.Sum256(content)
	actual := hex.EncodeToString(digest[:])
	if prior := v.verified[relative]; prior != "" && prior != actual {
		return nil, "", errors.New("artifact changed after verification")
	}
	if v.verified[relative] == "" {
		v.files++
		v.bytes += int64(len(content))
		v.verified[relative] = actual
	}
	return content, actual, nil
}

func checkedEvidenceRoot(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("evidence root is required")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("evidence root must be an existing directory, not a symlink")
	}
	return filepath.EvalSymlinks(abs)
}

func unitInterval(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
func positiveFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
func nonnegativeFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func overheadWithin(candidate, baseline, maximumRatio float64) bool {
	return nonnegativeFinite(maximumRatio) && maximumRatio <= 10 && nonnegativeFinite(candidate) && positiveFinite(baseline) &&
		candidate <= baseline*(1+maximumRatio)
}
