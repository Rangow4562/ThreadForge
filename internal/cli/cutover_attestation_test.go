package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/cutover"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

type cutoverAttestationFixture struct {
	root          string
	candidateRoot string
	legacyRoot    string
	evidenceRoot  string
	reportPath    string
	outputReport  string
	args          []string
	trust         cutover.EvaluatorTrustStore
}

func TestCutoverAttestEvaluationSignsClaimAndWritesNewReport(t *testing.T) {
	fixture := newCutoverAttestationFixture(t)
	var stdout, stderr bytes.Buffer
	if code := Run(fixture.args, &stdout, &stderr); code != 0 {
		t.Fatalf("attest code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"trusted_signature_verified": true`) ||
		!strings.Contains(stdout.String(), `"evaluator_assertion_signed": true`) ||
		!strings.Contains(stdout.String(), `"benchmark_execution_performed": false`) ||
		!strings.Contains(stdout.String(), `"independent_execution_verified": false`) ||
		!strings.Contains(stdout.String(), `"execution_authorized": false`) ||
		!strings.Contains(stdout.String(), `"cutover_ready": false`) ||
		!strings.Contains(stdout.String(), `"human_review_required": true`) {
		t.Fatalf("attestation result overstates evidence: %s", stdout.String())
	}

	updatedBytes, err := os.ReadFile(fixture.outputReport)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := cutover.DecodeReportJSON(updatedBytes)
	if err != nil || len(updated.Evaluations) != 1 {
		t.Fatalf("updated report decode: evaluations=%d err=%v", len(updated.Evaluations), err)
	}
	attestationEvidence := updated.Evaluations[0].Attestation
	if attestationEvidence.Path != "attestations/beacon.json" || attestationEvidence.SHA256 == "" {
		t.Fatalf("updated report is not bound to created attestation: %#v", attestationEvidence)
	}
	attestationBytes, err := os.ReadFile(filepath.Join(fixture.evidenceRoot, filepath.FromSlash(attestationEvidence.Path)))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(attestationBytes)
	if hex.EncodeToString(digest[:]) != attestationEvidence.SHA256 {
		t.Fatal("attestation evidence hash does not match updated report")
	}
	var attestation cutover.EvaluationAttestation
	if err := json.Unmarshal(attestationBytes, &attestation); err != nil {
		t.Fatal(err)
	}
	trials, err := cutover.ReadEvaluationTrialsArtifact(fixture.evidenceRoot, updated.Evaluations[0].OutcomeArtifact)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := cutover.ValidateEvaluationTrialBinding(updated.Evaluations[0], trials)
	if err != nil {
		t.Fatal(err)
	}
	claims := cutover.EvaluationAttestationClaims{
		EvaluatorID: trials.Evaluator.ID, Domain: updated.Evaluations[0].Domain,
		LegacyCommit: updated.Evaluations[0].LegacyCommit, CandidateCommit: updated.Candidate,
		DatasetSHA256: updated.Evaluations[0].DatasetSHA256, TaskSetSHA256: metrics.TaskSetSHA256,
		EvaluatorSHA256: trials.Evaluator.SHA256, OutcomeSHA256: updated.Evaluations[0].OutcomeArtifact.SHA256,
		SplitRole: "held_out", IssuedAt: attestation.IssuedAt,
	}
	if err := cutover.VerifyEvaluationAttestation(attestation, fixture.trust, claims); err != nil {
		t.Fatalf("created signature failed external-policy verification: %v", err)
	}
	if err := verifyPinnedCleanCheckout(fixture.candidateRoot, updated.Candidate, "candidate"); err != nil {
		t.Fatal(err)
	}
	if err := verifyPinnedCleanCheckout(fixture.legacyRoot, updated.Evaluations[0].LegacyCommit, "legacy"); err != nil {
		t.Fatal(err)
	}
}

func TestCutoverAttestEvaluationRequiresAssertionAndRejectsUnsafeOrMismatchedInputs(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *cutoverAttestationFixture, *[]string)
		want   string
	}{
		{
			name: "missing explicit evaluator assertion",
			change: func(_ *testing.T, _ *cutoverAttestationFixture, args *[]string) {
				*args = (*args)[:len(*args)-1]
			},
			want: "confirm-evaluator-assertion",
		},
		{
			name: "traversing artifact output",
			change: func(_ *testing.T, _ *cutoverAttestationFixture, args *[]string) {
				for index := range *args {
					if (*args)[index] == "--attestation-path" {
						(*args)[index+1] = "../outside.json"
					}
				}
			},
			want: "unsafe path segment",
		},
		{
			name: "report metric does not match trials",
			change: func(t *testing.T, fixture *cutoverAttestationFixture, _ *[]string) {
				data, err := os.ReadFile(fixture.reportPath)
				if err != nil {
					t.Fatal(err)
				}
				var report cutover.Report
				if err := json.Unmarshal(data, &report); err != nil {
					t.Fatal(err)
				}
				report.Evaluations[0].CandidateTokens++
				data, err = json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fixture.reportPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "declared metrics do not match",
		},
		{
			name: "dirty legacy checkout",
			change: func(t *testing.T, fixture *cutoverAttestationFixture, _ *[]string) {
				if err := os.WriteFile(filepath.Join(fixture.legacyRoot, "dirty.txt"), []byte("not clean\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "clean legacy checkout",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCutoverAttestationFixture(t)
			args := append([]string(nil), fixture.args...)
			test.change(t, &fixture, &args)
			var stdout, stderr bytes.Buffer
			if code := Run(args, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("expected %q failure, code=%d stdout=%s stderr=%s", test.want, code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(fixture.outputReport); !os.IsNotExist(err) {
				t.Fatalf("failed attestation unexpectedly wrote report: %v", err)
			}
		})
	}
}

func newCutoverAttestationFixture(t *testing.T) cutoverAttestationFixture {
	t.Helper()
	root := t.TempDir()
	candidateRoot := filepath.Join(root, "candidate")
	legacyRoot := filepath.Join(root, "legacy")
	evidenceRoot := filepath.Join(root, "evidence")
	externalRoot := filepath.Join(root, "external")
	for _, directory := range []string{evidenceRoot, filepath.Join(evidenceRoot, "outcomes"), filepath.Join(evidenceRoot, "attestations"), externalRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	candidateCommit := makeCutoverAttestationGitRepo(t, candidateRoot, "candidate")
	legacyCommit := makeCutoverAttestationGitRepo(t, legacyRoot, "legacy")

	trials := cutover.EvaluationTrials{
		SchemaVersion: cutover.EvaluationTrialsSchemaVersion, Domain: "beacon", LegacyVersion: "pinned-legacy",
		LegacyCommit: legacyCommit, DatasetSHA256: strings.Repeat("d", 64), HeldOut: true,
		Evaluator:        cutover.Evaluator{ID: "fixture-evaluator", Version: "1.0.0", SHA256: strings.Repeat("e", 64)},
		NoiseCalibration: cutover.NoiseCalibration{Method: "paired_task_and_independent_run_bootstrap_null_v2", Seed: 17, BootstrapResamples: 200, ConfidenceLevel: 0.95},
	}
	for runIndex := 0; runIndex < 8; runIndex++ {
		run := cutover.EvaluationRun{RunID: cutoverAttestationDigest(fmt.Sprintf("baseline-%d", runIndex))}
		for taskIndex := 0; taskIndex < 100; taskIndex++ {
			failed := taskIndex%2 == 0
			outcome := cutover.TaskOutcome{TaskSHA256: cutoverAttestationDigest(fmt.Sprintf("task-%d", taskIndex)), Success: !failed, CostUSD: 0.1, Tokens: 10}
			if failed {
				outcome.FailureSignatureSHA256 = cutoverAttestationDigest("baseline-repeat-failure")
			}
			run.Tasks = append(run.Tasks, outcome)
		}
		trials.BaselineRuns = append(trials.BaselineRuns, run)
	}
	for runIndex := 0; runIndex < 4; runIndex++ {
		run := cutover.EvaluationRun{RunID: cutoverAttestationDigest(fmt.Sprintf("candidate-%d", runIndex))}
		for taskIndex := 0; taskIndex < 100; taskIndex++ {
			failed := runIndex == 0 && taskIndex%2 == 0
			outcome := cutover.TaskOutcome{TaskSHA256: cutoverAttestationDigest(fmt.Sprintf("task-%d", taskIndex)), Success: !failed, CostUSD: 0.105, Tokens: 11}
			if failed {
				outcome.FailureSignatureSHA256 = cutoverAttestationDigest("candidate-repeat-failure")
			}
			run.Tasks = append(run.Tasks, outcome)
		}
		trials.CandidateRuns = append(trials.CandidateRuns, run)
	}
	trialBytes, err := json.Marshal(trials)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceRoot, "outcomes", "beacon.json"), trialBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	trialHash := sha256.Sum256(trialBytes)
	evaluation := cutover.Evaluation{
		Domain: trials.Domain, LegacyVersion: trials.LegacyVersion, LegacyCommit: legacyCommit,
		DatasetSHA256: trials.DatasetSHA256, HeldOut: true, OutcomeArtifact: cutover.Evidence{Path: "outcomes/beacon.json", SHA256: hex.EncodeToString(trialHash[:])},
		MaxCostOverheadRatio: 0.2, MaxTokenOverheadRatio: 0.2,
	}
	metrics, err := cutover.DeriveEvaluationMetrics(trials)
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

	identity, public, err := teamauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(externalRoot, "evaluator-identity.json")
	if err := teamauth.SaveIdentity(identityPath, filepath.Join(externalRoot, "evaluator-public.json"), identity, public); err != nil {
		t.Fatal(err)
	}
	trust := cutover.EvaluatorTrustStore{SchemaVersion: cutover.EvaluatorTrustStoreSchemaVersion, Keys: []cutover.TrustedEvaluatorKey{{
		KeyID: public.KeyID, PublicKey: public.PublicKey, EvaluatorID: trials.Evaluator.ID, Domains: []string{trials.Domain},
		NotBefore: "2026-01-01T00:00:00Z", NotAfter: "2027-01-01T00:00:00Z",
	}}}
	trustPath := filepath.Join(externalRoot, "trusted-evaluators.json")
	trustBytes, err := json.Marshal(trust)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trustPath, trustBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	reportPath := filepath.Join(root, "cutover-report.json")
	report := cutover.Report{SchemaVersion: cutover.SchemaVersion, Candidate: candidateCommit, Evaluations: []cutover.Evaluation{evaluation}}
	reportBytes, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, reportBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	outputReport := filepath.Join(root, "cutover-report-attested.json")
	args := []string{
		"cutover", "attest-evaluation", "--report", reportPath, "--domain", trials.Domain,
		"--evidence-root", evidenceRoot, "--candidate-root", candidateRoot, "--legacy-root", legacyRoot,
		"--identity", identityPath, "--trusted-evaluators", trustPath,
		"--attestation-path", "attestations/beacon.json", "--output-report", outputReport,
		"--confirm-evaluator-assertion",
	}
	return cutoverAttestationFixture{
		root: root, candidateRoot: candidateRoot, legacyRoot: legacyRoot, evidenceRoot: evidenceRoot,
		reportPath: reportPath, outputReport: outputReport, args: args, trust: trust,
	}
}

func makeCutoverAttestationGitRepo(t *testing.T, root, name string) string {
	t.Helper()
	makeTreeWritableForCleanup(t, root)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run("init")
	run("config", "user.name", "Threadforge Test")
	run("config", "user.email", "threadforge-test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(name+" fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "--", "README.md")
	run("commit", "-m", "test "+name)
	return run("rev-parse", "HEAD")
}

func cutoverAttestationDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
