package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/cutover"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

type cutoverApprovalResult struct {
	SchemaVersion            string           `json:"schema_version"`
	Role                     string           `json:"role"`
	ReviewerID               string           `json:"reviewer_id"`
	Approved                 bool             `json:"approved"`
	KeyID                    string           `json:"key_id"`
	Attestation              cutover.Evidence `json:"attestation"`
	ReportSHA256             string           `json:"report_sha256"`
	TrustedSignatureVerified bool             `json:"trusted_signature_verified"`
	ApprovalAssertionSigned  bool             `json:"approval_assertion_signed"`
	ExecutionAuthorized      bool             `json:"execution_authorized"`
	CutoverReady             bool             `json:"cutover_ready"`
	HumanReviewRequired      bool             `json:"human_review_required"`
}

func runCutoverApproval(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cutover attest-approval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reportPath := fs.String("report", "", "existing cutover report JSON with one draft approval row")
	role := fs.String("role", "", "release-manager, security-reviewer, or license-reviewer")
	decision := fs.String("decision", "", "approve or reject the cutover claim")
	reason := fs.String("reason", "", "review rationale; no credentials or secrets")
	approvedAt := fs.String("approved-at", "", "RFC3339 time when the reviewer made the decision")
	evidenceRoot := fs.String("evidence-root", "", "directory containing the hashed approval evidence")
	candidateRoot := fs.String("candidate-root", "", "clean Threadforge checkout pinned by candidate_commit")
	identityPath := fs.String("identity", "", "dedicated reviewer private identity JSON")
	trustedApproverPath := fs.String("trusted-approvers", "", "out-of-band trusted approver policy JSON")
	attestationPath := fs.String("attestation-path", "", "new repository-relative attestation path under evidence-root")
	outputReportPath := fs.String("output-report", "", "new output path for the signed cutover report")
	confirm := fs.Bool("confirm-approval", false, "assert that the reviewer personally made the stated decision after reviewing its bound evidence")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*reportPath) == "" || strings.TrimSpace(*role) == "" ||
		(*decision != "approve" && *decision != "reject") || strings.TrimSpace(*reason) == "" || strings.TrimSpace(*approvedAt) == "" ||
		strings.TrimSpace(*evidenceRoot) == "" || strings.TrimSpace(*candidateRoot) == "" || strings.TrimSpace(*identityPath) == "" ||
		strings.TrimSpace(*trustedApproverPath) == "" || strings.TrimSpace(*attestationPath) == "" ||
		strings.TrimSpace(*outputReportPath) == "" || !*confirm {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge cutover attest-approval --report <report.json> --role <role> --decision <approve|reject> --reason <text> --approved-at <RFC3339> --evidence-root <artifacts-dir> --candidate-root <clean-checkout> --identity <reviewer-private-identity.json> --trusted-approvers <external-trust.json> --attestation-path <relative-new-path.json> --output-report <new-report.json> --confirm-approval")
		return 2
	}

	candidate, err := canonicalDirectory(*candidateRoot, "candidate-root")
	if err != nil {
		return attestationError(stderr, err)
	}
	evidence, err := canonicalDirectory(*evidenceRoot, "evidence-root")
	if err != nil {
		return attestationError(stderr, err)
	}
	if rootsOverlap(candidate, evidence) {
		return attestationError(stderr, errors.New("candidate and evidence roots must be separate, non-nested directories"))
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
	if err := verifyCandidateCheckout(candidate, report.Candidate, report.Repository); err != nil {
		return attestationError(stderr, err)
	}
	if strings.TrimSpace(*reason) != *reason || len(*reason) > 2000 || !utf8Valid(*reason) || containsCredentialText(*reason) {
		return attestationError(stderr, errors.New("approval reason is empty, malformed, too long, or contains credential material"))
	}
	approvedTime, err := time.Parse(time.RFC3339Nano, *approvedAt)
	if err != nil || approvedTime.After(time.Now().UTC().Add(time.Minute)) {
		return attestationError(stderr, errors.New("approved-at must be a valid RFC3339 timestamp no more than one minute in the future"))
	}
	if err := cutover.VerifyApprovalEvidence(evidence, findApprovalEvidence(report, *role)); err != nil {
		return attestationError(stderr, fmt.Errorf("approval supporting evidence: %w", err))
	}
	approvalIndex := -1
	for index := range report.Approvals {
		if report.Approvals[index].Role == *role {
			if approvalIndex >= 0 {
				return attestationError(stderr, fmt.Errorf("cutover report contains duplicate approvals for role %q", *role))
			}
			approvalIndex = index
		}
	}
	if approvalIndex < 0 {
		return attestationError(stderr, fmt.Errorf("cutover report has no draft approval for role %q", *role))
	}

	identityFile, err := canonicalRegularFile(*identityPath, "approver identity", 4096)
	if err != nil {
		return attestationError(stderr, err)
	}
	trustFile, err := canonicalRegularFile(*trustedApproverPath, "trusted approver policy", 1<<20)
	if err != nil {
		return attestationError(stderr, err)
	}
	for label, path := range map[string]string{"approver identity": identityFile, "trusted approver policy": trustFile} {
		if pathIsWithin(candidate, path) || pathIsWithin(evidence, path) {
			return attestationError(stderr, fmt.Errorf("%s must be outside candidate and evidence roots", label))
		}
	}
	identity, err := teamauth.LoadIdentity(identityFile)
	if err != nil {
		return attestationError(stderr, fmt.Errorf("load approver identity: %w", err))
	}
	trust, trustDigest, err := cutover.ReadApprovalTrustStore(trustFile)
	if err != nil {
		return attestationError(stderr, err)
	}
	trustedReviewer := ""
	for _, key := range trust.Keys {
		if key.KeyID == identity.KeyID {
			trustedReviewer = key.Reviewer
			break
		}
	}
	if trustedReviewer == "" {
		return attestationError(stderr, errors.New("approver identity is not present in the external trust policy"))
	}
	approval := report.Approvals[approvalIndex]
	if approval.Reviewer != trustedReviewer {
		return attestationError(stderr, errors.New("draft approval reviewer does not match the trusted identity"))
	}
	approval.Approved = *decision == "approve"
	approval.Reason = *reason
	approval.ApprovedAt = approvedTime.UTC().Format(time.RFC3339Nano)
	if err := cutover.VerifyApprovalEvidence(evidence, approval.Evidence); err != nil {
		return attestationError(stderr, fmt.Errorf("approval supporting evidence: %w", err))
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
	if pathIsWithin(candidate, reportOutput) || pathIsWithin(evidence, reportOutput) {
		return attestationError(stderr, errors.New("output-report must be outside candidate and evidence roots"))
	}
	for label, input := range map[string]string{"source report": reportFile, "approver identity": identityFile, "trusted approver policy": trustFile} {
		if samePath(reportOutput, input) || samePath(attestationAbsolute, input) {
			return attestationError(stderr, fmt.Errorf("outputs must not overwrite %s", label))
		}
	}
	if samePath(reportOutput, attestationAbsolute) {
		return attestationError(stderr, errors.New("output-report and attestation-path must name different files"))
	}

	binding, err := cutover.ApprovalBindingForReport(report, approval)
	if err != nil {
		return attestationError(stderr, err)
	}
	signed, err := cutover.SignApproval(binding, identity.PrivateKey, time.Now().UTC())
	if err != nil {
		return attestationError(stderr, err)
	}
	if err := cutover.VerifyApprovalAttestation(signed, trust, binding); err != nil {
		return attestationError(stderr, fmt.Errorf("approver identity is not trusted for this reviewer and role: %w", err))
	}
	attestationBytes, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return attestationError(stderr, err)
	}
	attestationBytes = append(attestationBytes, '\n')
	attestationHash := sha256.Sum256(attestationBytes)
	approval.Attestation = cutover.Evidence{Path: attestationRelative, SHA256: hex.EncodeToString(attestationHash[:])}
	report.Approvals[approvalIndex] = approval
	updatedReport, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return attestationError(stderr, err)
	}
	updatedReport = append(updatedReport, '\n')
	reportHash := sha256.Sum256(updatedReport)
	originalReportHash := sha256.Sum256(reportContent)

	if err := cutover.VerifyApprovalEvidence(evidence, approval.Evidence); err != nil {
		return attestationError(stderr, fmt.Errorf("approval supporting evidence changed while signing: %w", err))
	}
	if _, freshTrustDigest, trustErr := cutover.ReadApprovalTrustStore(trustFile); trustErr != nil || freshTrustDigest != trustDigest {
		return attestationError(stderr, errors.New("trusted approver policy changed while preparing attestation"))
	}
	if freshIdentity, identityErr := teamauth.LoadIdentity(identityFile); identityErr != nil || freshIdentity.KeyID != identity.KeyID || !freshIdentity.PrivateKey.Equal(identity.PrivateKey) {
		return attestationError(stderr, errors.New("approver identity changed while preparing attestation"))
	}
	latestReport, reportErr := readRegularNoLinks(reportFile, 32<<20)
	if reportErr != nil || sha256.Sum256(latestReport) != originalReportHash {
		return attestationError(stderr, errors.New("source cutover report changed while preparing attestation"))
	}
	if err := verifyCandidateCheckout(candidate, report.Candidate, report.Repository); err != nil {
		return attestationError(stderr, err)
	}
	if err := writeExclusiveEvidence(evidence, attestationRelative, attestationBytes); err != nil {
		return attestationError(stderr, fmt.Errorf("write attestation: %w", err))
	}
	if err := writeExclusiveFile(reportOutput, updatedReport); err != nil {
		removeCreatedFileIfDigest(attestationAbsolute, attestationHash[:])
		return attestationError(stderr, fmt.Errorf("write updated report: %w", err))
	}
	if err := verifyCandidateCheckout(candidate, report.Candidate, report.Repository); err != nil {
		removeCreatedFileIfDigest(attestationAbsolute, attestationHash[:])
		removeCreatedFileIfDigest(reportOutput, reportHash[:])
		return attestationError(stderr, err)
	}
	if _, freshTrustDigest, trustErr := cutover.ReadApprovalTrustStore(trustFile); trustErr != nil || freshTrustDigest != trustDigest {
		removeCreatedFileIfDigest(attestationAbsolute, attestationHash[:])
		removeCreatedFileIfDigest(reportOutput, reportHash[:])
		return attestationError(stderr, errors.New("trusted approver policy changed while writing attestation"))
	}
	result := cutoverApprovalResult{
		SchemaVersion: "threadforge.cutover-approval-attestation-result.v2", Role: *role,
		ReviewerID: trustedReviewer, Approved: approval.Approved, KeyID: signed.KeyID,
		Attestation: approval.Attestation, ReportSHA256: hex.EncodeToString(reportHash[:]),
		TrustedSignatureVerified: true, ApprovalAssertionSigned: true, ExecutionAuthorized: false,
		CutoverReady: false, HumanReviewRequired: true,
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func findApprovalEvidence(report cutover.Report, role string) []cutover.Evidence {
	for _, approval := range report.Approvals {
		if approval.Role == role {
			return approval.Evidence
		}
	}
	return nil
}

func utf8Valid(value string) bool {
	return strings.ToValidUTF8(value, "") == value
}

func containsCredentialText(value string) bool {
	return privacy.ContainsCredentialText(value)
}
