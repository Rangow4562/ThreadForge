package cutover

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func TestCutoverApprovalAttestationBindsRoleReviewerAndEvidence(t *testing.T) {
	privateKey := approvalTestPrivateKey("security-reviewer")
	publicKey := privateKey.Public().(ed25519.PublicKey)
	trust := ApprovalTrustStore{SchemaVersion: ApprovalTrustSchemaVersion, Keys: []TrustedApprovalKey{{
		KeyID: approvalKeyID(publicKey), PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		Reviewer: "security-reviewer-1", Roles: []string{"security-reviewer"},
		NotBefore: "2026-01-01T00:00:00Z", NotAfter: "2027-01-01T00:00:00Z",
	}}}
	report := Report{
		SchemaVersion: SchemaVersion, Candidate: "0123456789abcdef0123456789abcdef01234567",
		Features: []FeatureEvidence{{ID: "capture", Status: "complete", Evidence: []Evidence{{Path: "capture.json", SHA256: testDigest("capture")}}}},
	}
	approval := Approval{
		Role: "security-reviewer", Reviewer: "security-reviewer-1", Approved: true,
		Reason: "Reviewed the security evidence.", ApprovedAt: "2026-06-01T12:00:00Z",
		Evidence: []Evidence{{Path: "review.json", SHA256: testDigest("review")}},
	}
	binding, err := ApprovalBindingForReport(report, approval)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignApproval(binding, privateKey, time.Date(2026, 6, 1, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyApprovalAttestation(signed, trust, binding); err != nil {
		t.Fatalf("valid approval signature failed: %v", err)
	}

	changedReport := report
	changedReport.Features = append([]FeatureEvidence(nil), report.Features...)
	changedReport.Features[0].Status = "partial"
	changedBinding, err := ApprovalBindingForReport(changedReport, approval)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyApprovalAttestation(signed, trust, changedBinding); err == nil {
		t.Fatal("edited non-approval report data retained the old approval signature")
	}
	changedApproval := approval
	changedApproval.Reason = "Different reviewer rationale."
	changedBinding, err = ApprovalBindingForReport(report, changedApproval)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyApprovalAttestation(signed, trust, changedBinding); err == nil {
		t.Fatal("edited reviewer rationale retained the old approval signature")
	}
	changedApproval = approval
	changedApproval.Evidence = []Evidence{{Path: "different.json", SHA256: testDigest("different")}}
	changedBinding, err = ApprovalBindingForReport(report, changedApproval)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyApprovalAttestation(signed, trust, changedBinding); err == nil {
		t.Fatal("substituted approval evidence retained the old signature")
	}

	wrongScope := trust
	wrongScope.Keys = append([]TrustedApprovalKey(nil), trust.Keys...)
	wrongScope.Keys[0].Roles = []string{"release-manager"}
	if err := VerifyApprovalAttestation(signed, wrongScope, binding); err == nil {
		t.Fatal("role-scoped trust policy authorized a different approval role")
	}
	if _, err := SignApproval(binding, privateKey, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("attestation signed before the claimed approval time")
	}
}

func TestApprovalEvidenceFingerprintExcludesOnlyApprovalRows(t *testing.T) {
	report := Report{
		SchemaVersion: SchemaVersion, Candidate: "0123456789abcdef0123456789abcdef01234567",
		Approvals: []Approval{{Role: "release-manager", Reviewer: "reviewer", Reason: "first"}},
	}
	first, err := ApprovalEvidenceFingerprint(report)
	if err != nil {
		t.Fatal(err)
	}
	report.Approvals[0].Reason = "second"
	second, err := ApprovalEvidenceFingerprint(report)
	if err != nil || first != second {
		t.Fatalf("approval bundle should not recursively invalidate signatures: %s %s %v", first, second, err)
	}
	report.Candidate = "abcdef0123456789abcdef0123456789abcdef01"
	third, err := ApprovalEvidenceFingerprint(report)
	if err != nil || first == third {
		t.Fatalf("candidate change did not change report evidence binding: %s %s %v", first, third, err)
	}
}

func TestAssessmentRequiresDistinctTrustedCutoverReviewers(t *testing.T) {
	root := t.TempDir()
	report, _, evaluatorTrust := completeFixture(t, root)
	assessment, err := AssessWithTrustAndMigrationReviewersAndApprovers(
		report, root, evaluatorTrust, fixtureMigrationAuthorityTrustStore(), fixtureApprovalTrustStore())
	if err != nil || !assessment.Ready {
		t.Fatalf("signed independent reviewer fixture should pass: %#v, %v", assessment, err)
	}
	duplicateReviewer := report
	duplicateReviewer.Approvals = append([]Approval(nil), report.Approvals...)
	duplicateReviewer.Approvals[1].Reviewer = duplicateReviewer.Approvals[0].Reviewer
	assessment, err = AssessWithTrustAndMigrationReviewersAndApprovers(
		duplicateReviewer, root, evaluatorTrust, fixtureMigrationAuthorityTrustStore(), fixtureApprovalTrustStore())
	if err != nil || assessment.Ready || !hasBlocker(assessment.Blockers, "distinct trusted reviewers") {
		t.Fatalf("one reviewer was allowed to satisfy multiple roles: %#v, %v", assessment, err)
	}
}

func approvalTestPrivateKey(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("Threadforge approval unit test:" + label))
	return ed25519.NewKeyFromSeed(seed[:])
}
