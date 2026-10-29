package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/cutover"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

type cutoverAttestationResult struct {
	SchemaVersion                string           `json:"schema_version"`
	Domain                       string           `json:"domain"`
	EvaluatorID                  string           `json:"evaluator_id"`
	KeyID                        string           `json:"key_id"`
	Attestation                  cutover.Evidence `json:"attestation"`
	ReportSHA256                 string           `json:"report_sha256"`
	TrustedSignatureVerified     bool             `json:"trusted_signature_verified"`
	EvaluatorAssertionSigned     bool             `json:"evaluator_assertion_signed"`
	BenchmarkExecutionPerformed  bool             `json:"benchmark_execution_performed"`
	IndependentExecutionVerified bool             `json:"independent_execution_verified"`
	ExecutionAuthorized          bool             `json:"execution_authorized"`
	CutoverReady                 bool             `json:"cutover_ready"`
	HumanReviewRequired          bool             `json:"human_review_required"`
}

func runCutoverAttestation(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cutover attest-evaluation", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reportPath := fs.String("report", "", "existing cutover report JSON")
	domain := fs.String("domain", "", "one held-out evaluation domain in the report")
	evidenceRoot := fs.String("evidence-root", "", "existing root containing the hashed evaluation trials")
	candidateRoot := fs.String("candidate-root", "", "clean Threadforge checkout pinned by candidate_commit")
	legacyRoot := fs.String("legacy-root", "", "clean legacy checkout pinned by the selected evaluation")
	identityPath := fs.String("identity", "", "dedicated evaluator private identity JSON")
	trustedEvaluatorPath := fs.String("trusted-evaluators", "", "out-of-band trusted evaluator policy JSON")
	attestationPath := fs.String("attestation-path", "", "new repository-relative output path under evidence-root")
	outputReportPath := fs.String("output-report", "", "new output path for the updated cutover report")
	confirm := fs.Bool("confirm-evaluator-assertion", false, "assert that the evaluator personally vouches for the bound held-out evaluation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*reportPath) == "" || strings.TrimSpace(*domain) == "" ||
		strings.TrimSpace(*evidenceRoot) == "" || strings.TrimSpace(*candidateRoot) == "" || strings.TrimSpace(*legacyRoot) == "" ||
		strings.TrimSpace(*identityPath) == "" || strings.TrimSpace(*trustedEvaluatorPath) == "" ||
		strings.TrimSpace(*attestationPath) == "" || strings.TrimSpace(*outputReportPath) == "" || !*confirm {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge cutover attest-evaluation --report <report.json> --domain <domain> --evidence-root <artifacts-dir> --candidate-root <clean-checkout> --legacy-root <clean-checkout> --identity <evaluator-private-identity.json> --trusted-evaluators <external-trust.json> --attestation-path <relative-new-path.json> --output-report <new-report.json> --confirm-evaluator-assertion")
		return 2
	}

	candidate, err := canonicalDirectory(*candidateRoot, "candidate-root")
	if err != nil {
		return attestationError(stderr, err)
	}
	legacy, err := canonicalDirectory(*legacyRoot, "legacy-root")
	if err != nil {
		return attestationError(stderr, err)
	}
	evidence, err := canonicalDirectory(*evidenceRoot, "evidence-root")
	if err != nil {
		return attestationError(stderr, err)
	}
	if rootsOverlap(candidate, legacy) || rootsOverlap(candidate, evidence) || rootsOverlap(legacy, evidence) {
		return attestationError(stderr, errors.New("candidate, legacy, and evidence roots must be separate, non-nested directories"))
	}

	reportFile, err := canonicalRegularFile(*reportPath, "report", 32<<20)
	if err != nil {
		return attestationError(stderr, err)
	}
	reportContent, err := readRegularNoLinks(reportFile, 32<<20)
	if err != nil {
		return attestationError(stderr, err)
	}
	report, err := cutover.DecodeReportJSON(reportContent)
	if err != nil {
		return attestationError(stderr, fmt.Errorf("decode cutover report: %w", err))
	}
	if report.SchemaVersion != cutover.SchemaVersion {
		return attestationError(stderr, fmt.Errorf("cutover report must use schema %s", cutover.SchemaVersion))
	}
	if err := verifyPinnedCleanCheckout(candidate, report.Candidate, "candidate"); err != nil {
		return attestationError(stderr, err)
	}
	var selectedIndex = -1
	for index := range report.Evaluations {
		if report.Evaluations[index].Domain == *domain {
			if selectedIndex >= 0 {
				return attestationError(stderr, fmt.Errorf("cutover report contains duplicate evaluations for domain %q", *domain))
			}
			selectedIndex = index
		}
	}
	if selectedIndex < 0 {
		return attestationError(stderr, fmt.Errorf("cutover report has no evaluation for domain %q", *domain))
	}
	evaluation := report.Evaluations[selectedIndex]
	if !evaluation.HeldOut {
		return attestationError(stderr, errors.New("selected evaluation must declare held_out=true"))
	}
	if err := verifyPinnedCleanCheckout(legacy, evaluation.LegacyCommit, "legacy"); err != nil {
		return attestationError(stderr, err)
	}
	trials, err := cutover.ReadEvaluationTrialsArtifact(evidence, evaluation.OutcomeArtifact)
	if err != nil {
		return attestationError(stderr, fmt.Errorf("read selected outcome artifact: %w", err))
	}
	metrics, err := cutover.ValidateEvaluationTrialBinding(evaluation, trials)
	if err != nil {
		return attestationError(stderr, fmt.Errorf("validate selected outcome artifact: %w", err))
	}

	identityFile, err := canonicalRegularFile(*identityPath, "evaluator identity", 4096)
	if err != nil {
		return attestationError(stderr, err)
	}
	trustFile, err := canonicalRegularFile(*trustedEvaluatorPath, "trusted evaluator policy", 1<<20)
	if err != nil {
		return attestationError(stderr, err)
	}
	for label, path := range map[string]string{"evaluator identity": identityFile, "trusted evaluator policy": trustFile} {
		if pathIsWithin(candidate, path) || pathIsWithin(legacy, path) || pathIsWithin(evidence, path) {
			return attestationError(stderr, fmt.Errorf("%s must be outside candidate, legacy, and evidence roots", label))
		}
	}
	identity, err := teamauth.LoadIdentity(identityFile)
	if err != nil {
		return attestationError(stderr, fmt.Errorf("load evaluator identity: %w", err))
	}
	trust, trustDigest, err := cutover.ReadEvaluatorTrustStore(trustFile)
	if err != nil {
		return attestationError(stderr, err)
	}

	attestationRelative, err := validateRelativeEvidencePath(*attestationPath)
	if err != nil {
		return attestationError(stderr, fmt.Errorf("attestation-path: %w", err))
	}
	attestationAbsolute, err := validateNewEvidenceTarget(evidence, attestationRelative)
	if err != nil {
		return attestationError(stderr, err)
	}
	reportOutput, err := validateNewOutputPath(*outputReportPath)
	if err != nil {
		return attestationError(stderr, err)
	}
	if pathIsWithin(candidate, reportOutput) || pathIsWithin(legacy, reportOutput) {
		return attestationError(stderr, errors.New("output-report must be outside candidate and legacy checkouts"))
	}
	if samePath(reportOutput, reportFile) || samePath(reportOutput, identityFile) || samePath(reportOutput, trustFile) {
		return attestationError(stderr, errors.New("output-report must not overwrite an input file"))
	}
	if samePath(reportOutput, attestationAbsolute) {
		return attestationError(stderr, errors.New("output-report and attestation-path must name different files"))
	}

	issuedAt := time.Now().UTC().Format(time.RFC3339Nano)
	claims := cutover.EvaluationAttestationClaims{
		EvaluatorID: trials.Evaluator.ID, Domain: evaluation.Domain, LegacyCommit: evaluation.LegacyCommit,
		CandidateCommit: report.Candidate, DatasetSHA256: evaluation.DatasetSHA256,
		TaskSetSHA256: metrics.TaskSetSHA256, EvaluatorSHA256: trials.Evaluator.SHA256,
		OutcomeSHA256: evaluation.OutcomeArtifact.SHA256, SplitRole: "held_out", IssuedAt: issuedAt,
	}
	signed, err := cutover.SignEvaluationAttestation(claims, identity.PrivateKey)
	if err != nil {
		return attestationError(stderr, err)
	}
	if err := cutover.VerifyEvaluationAttestation(signed, trust, claims); err != nil {
		return attestationError(stderr, fmt.Errorf("signing identity is not trusted for this evaluator and domain: %w", err))
	}
	attestationBytes, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return attestationError(stderr, err)
	}
	attestationBytes = append(attestationBytes, '\n')
	attestationDigest := sha256.Sum256(attestationBytes)
	attestationEvidence := cutover.Evidence{Path: attestationRelative, SHA256: hex.EncodeToString(attestationDigest[:])}
	report.Evaluations[selectedIndex].Attestation = attestationEvidence
	updatedReport, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return attestationError(stderr, err)
	}
	updatedReport = append(updatedReport, '\n')
	reportDigest := sha256.Sum256(updatedReport)

	if _, freshTrustDigest, trustErr := cutover.ReadEvaluatorTrustStore(trustFile); trustErr != nil || freshTrustDigest != trustDigest {
		return attestationError(stderr, errors.New("trusted evaluator policy changed while preparing attestation"))
	}
	if freshIdentity, identityErr := teamauth.LoadIdentity(identityFile); identityErr != nil || freshIdentity.KeyID != identity.KeyID || !freshIdentity.PrivateKey.Equal(identity.PrivateKey) {
		return attestationError(stderr, errors.New("evaluator identity changed while preparing attestation"))
	}
	if err := verifyPinnedCleanCheckout(candidate, report.Candidate, "candidate"); err != nil {
		return attestationError(stderr, err)
	}
	if err := verifyPinnedCleanCheckout(legacy, evaluation.LegacyCommit, "legacy"); err != nil {
		return attestationError(stderr, err)
	}

	if err := writeExclusiveEvidence(evidence, attestationRelative, attestationBytes); err != nil {
		return attestationError(stderr, fmt.Errorf("write attestation: %w", err))
	}
	if err := writeExclusiveFile(reportOutput, updatedReport); err != nil {
		removeCreatedFileIfDigest(attestationAbsolute, attestationDigest[:])
		return attestationError(stderr, fmt.Errorf("write updated report: %w", err))
	}
	if err := verifyPinnedCleanCheckout(candidate, report.Candidate, "candidate"); err != nil {
		removeCreatedFileIfDigest(attestationAbsolute, attestationDigest[:])
		removeCreatedFileIfDigest(reportOutput, reportDigest[:])
		return attestationError(stderr, err)
	}
	if err := verifyPinnedCleanCheckout(legacy, evaluation.LegacyCommit, "legacy"); err != nil {
		removeCreatedFileIfDigest(attestationAbsolute, attestationDigest[:])
		removeCreatedFileIfDigest(reportOutput, reportDigest[:])
		return attestationError(stderr, err)
	}
	if _, freshTrustDigest, trustErr := cutover.ReadEvaluatorTrustStore(trustFile); trustErr != nil || freshTrustDigest != trustDigest {
		removeCreatedFileIfDigest(attestationAbsolute, attestationDigest[:])
		removeCreatedFileIfDigest(reportOutput, reportDigest[:])
		return attestationError(stderr, errors.New("trusted evaluator policy changed while writing attestation"))
	}

	result := cutoverAttestationResult{
		SchemaVersion: "threadforge.cutover-evaluation-attestation-result.v1", Domain: evaluation.Domain,
		EvaluatorID: trials.Evaluator.ID, KeyID: signed.KeyID, Attestation: attestationEvidence,
		ReportSHA256: hex.EncodeToString(reportDigest[:]), TrustedSignatureVerified: true,
		EvaluatorAssertionSigned: true, BenchmarkExecutionPerformed: false,
		IndependentExecutionVerified: false, ExecutionAuthorized: false, CutoverReady: false, HumanReviewRequired: true,
	}
	if code := writeIndentedJSON(stdout, result, stderr); code != 0 {
		return code
	}
	return 0
}

func attestationError(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, err)
	return 1
}

func canonicalDirectory(value, label string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s must be an existing directory, not a symlink", label)
	}
	return filepath.EvalSymlinks(absolute)
}

func canonicalRegularFile(value, label string, maximum int64) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximum {
		return "", fmt.Errorf("%s must be a regular non-symlink file no larger than %d bytes", label, maximum)
	}
	return filepath.EvalSymlinks(absolute)
}

func readRegularNoLinks(filename string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximum {
		return nil, errors.New("input must be a regular non-symlink file within its size limit")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	final, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil || int64(len(data)) != info.Size() || final.Size() != info.Size() || !final.ModTime().Equal(info.ModTime()) {
		return nil, errors.New("input changed or could not be read consistently")
	}
	if int64(len(data)) > maximum {
		return nil, errors.New("input exceeds size limit")
	}
	return data, nil
}

func rootsOverlap(left, right string) bool {
	return pathIsWithin(left, right) || pathIsWithin(right, left)
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func validateRelativeEvidencePath(value string) (string, error) {
	relative := strings.ReplaceAll(value, "\\", "/")
	if relative == "" || len(relative) > 1024 || strings.HasPrefix(relative, "/") || strings.Contains(relative, ":") {
		return "", errors.New("must be a bounded repository-relative path")
	}
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("contains an unsafe path segment")
		}
	}
	return relative, nil
}

func validateNewEvidenceTarget(root, relative string) (string, error) {
	current := root
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("attestation parent directories must already exist and must not be symlinks")
		}
	}
	target := filepath.Join(current, parts[len(parts)-1])
	if _, err := os.Lstat(target); err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("attestation output already exists or cannot be checked")
	}
	return target, nil
}

func validateNewOutputPath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	parent, name := filepath.Dir(absolute), filepath.Base(absolute)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "", errors.New("output-report must name a new file")
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("output-report parent must be an existing non-symlink directory")
	}
	canonicalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	target := filepath.Join(canonicalParent, name)
	if _, err := os.Lstat(target); err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("output-report already exists or cannot be checked")
	}
	return target, nil
}

func writeExclusiveEvidence(root, relative string, content []byte) error {
	target, err := validateNewEvidenceTarget(root, relative)
	if err != nil {
		return err
	}
	return writeExclusiveFile(target, content)
}

func writeExclusiveFile(target string, content []byte) error {
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

func removeCreatedFileIfDigest(filename string, expected []byte) {
	content, err := readRegularNoLinks(filename, 128<<20)
	if err != nil {
		return
	}
	digest := sha256.Sum256(content)
	if string(digest[:]) == string(expected) {
		_ = os.Remove(filename)
	}
}
