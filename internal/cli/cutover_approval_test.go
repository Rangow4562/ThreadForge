package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/cutover"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

func TestCutoverAttestApprovalSignsBoundClaimAndWritesSeparateReport(t *testing.T) {
	fixture := newCutoverApprovalFixture(t, true)
	original, err := os.ReadFile(fixture.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run(fixture.args, &stdout, &stderr); code != 0 {
		t.Fatalf("attest code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, expected := range []string{`"trusted_signature_verified": true`, `"approval_assertion_signed": true`,
		`"execution_authorized": false`, `"cutover_ready": false`, `"human_review_required": true`} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("approval result omitted %s: %s", expected, stdout.String())
		}
	}
	unchanged, err := os.ReadFile(fixture.reportPath)
	if err != nil || !bytes.Equal(unchanged, original) {
		t.Fatalf("source report changed during signing: err=%v", err)
	}
	updatedBytes, err := os.ReadFile(fixture.outputReport)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := cutover.DecodeReportJSON(updatedBytes)
	if err != nil || len(updated.Approvals) != 1 {
		t.Fatalf("updated report decode: approvals=%d err=%v", len(updated.Approvals), err)
	}
	approval := updated.Approvals[0]
	if !approval.Approved || approval.Reviewer != "fixture-release-manager" || approval.Attestation.Path != "attestations/release.json" {
		t.Fatalf("signed approval did not bind trusted reviewer and output: %#v", approval)
	}
	attestationBytes, err := os.ReadFile(filepath.Join(fixture.evidenceRoot, filepath.FromSlash(approval.Attestation.Path)))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(attestationBytes)
	if hex.EncodeToString(digest[:]) != approval.Attestation.SHA256 {
		t.Fatal("attestation digest does not match signed report")
	}
	var attestation cutover.ApprovalAttestation
	if err := json.Unmarshal(attestationBytes, &attestation); err != nil {
		t.Fatal(err)
	}
	binding, err := cutover.ApprovalBindingForReport(updated, approval)
	if err != nil {
		t.Fatal(err)
	}
	if err := cutover.VerifyApprovalAttestation(attestation, fixture.trust, binding); err != nil {
		t.Fatalf("attestation failed out-of-band verification: %v", err)
	}
	if err := verifyPinnedCleanCheckout(fixture.candidateRoot, updated.Candidate, "candidate"); err != nil {
		t.Fatal(err)
	}
}

func TestCutoverAttestApprovalCanSignRejectionButNeverAuthorizeCutover(t *testing.T) {
	fixture := newCutoverApprovalFixture(t, true)
	for index := range fixture.args {
		if fixture.args[index] == "--decision" {
			fixture.args[index+1] = "reject"
		}
	}
	var stdout, stderr bytes.Buffer
	if code := Run(fixture.args, &stdout, &stderr); code != 0 {
		t.Fatalf("signed rejection code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result cutoverApprovalResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Approved || !result.TrustedSignatureVerified || !result.ApprovalAssertionSigned ||
		result.ExecutionAuthorized || result.CutoverReady || !result.HumanReviewRequired {
		t.Fatalf("rejected approval result overstates readiness: %#v", result)
	}
	updatedBytes, err := os.ReadFile(fixture.outputReport)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := cutover.DecodeReportJSON(updatedBytes)
	if err != nil || len(updated.Approvals) != 1 || updated.Approvals[0].Approved {
		t.Fatalf("signed rejection missing from output report: %#v, %v", updated.Approvals, err)
	}
}

func TestCutoverAttestApprovalRejectsMissingConfirmationAndWrongRoleTrust(t *testing.T) {
	tests := []struct {
		name   string
		change func(*cutoverApprovalFixture)
		want   string
	}{
		{
			name: "missing confirmation",
			change: func(fixture *cutoverApprovalFixture) {
				fixture.args = fixture.args[:len(fixture.args)-1]
			},
			want: "confirm-approval",
		},
		{
			name: "key lacks role scope",
			change: func(fixture *cutoverApprovalFixture) {
				fixture.trust.Keys[0].Roles = []string{"security-reviewer"}
				data, err := json.Marshal(fixture.trust)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fixture.trustPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "not trusted for this reviewer and role",
		},
		{
			name: "changed evidence",
			change: func(fixture *cutoverApprovalFixture) {
				if err := os.WriteFile(filepath.Join(fixture.evidenceRoot, "review.txt"), []byte("changed\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "digest mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCutoverApprovalFixture(t, true)
			test.change(&fixture)
			var stdout, stderr bytes.Buffer
			if code := Run(fixture.args, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("expected %q failure, code=%d stdout=%s stderr=%s", test.want, code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(fixture.outputReport); !os.IsNotExist(err) {
				t.Fatalf("failed signing unexpectedly wrote report: %v", err)
			}
		})
	}
}

type cutoverApprovalFixture struct {
	root          string
	candidateRoot string
	evidenceRoot  string
	identityPath  string
	trustPath     string
	reportPath    string
	outputReport  string
	args          []string
	trust         cutover.ApprovalTrustStore
}

func newCutoverApprovalFixture(t *testing.T, confirm bool) cutoverApprovalFixture {
	t.Helper()
	root := t.TempDir()
	candidateRoot := filepath.Join(root, "candidate")
	evidenceRoot := filepath.Join(root, "evidence")
	externalRoot := filepath.Join(root, "external")
	for _, directory := range []string{evidenceRoot, filepath.Join(evidenceRoot, "attestations"), externalRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	candidateCommit := makeCutoverAttestationGitRepo(t, candidateRoot, "candidate")
	setGitHubOriginForTest(t, candidateRoot, "threadforge-fixture/Threadforge")
	evidenceBytes := []byte("independently reviewed release evidence\n")
	if err := os.WriteFile(filepath.Join(evidenceRoot, "review.txt"), evidenceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	evidenceHash := sha256.Sum256(evidenceBytes)
	identity, public, err := teamauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(externalRoot, "release-manager-identity.json")
	if err := teamauth.SaveIdentity(identityPath, filepath.Join(externalRoot, "release-manager-public.json"), identity, public); err != nil {
		t.Fatal(err)
	}
	trust := cutover.ApprovalTrustStore{SchemaVersion: cutover.ApprovalTrustSchemaVersion, Keys: []cutover.TrustedApprovalKey{{
		KeyID: public.KeyID, PublicKey: public.PublicKey, Reviewer: "fixture-release-manager", Roles: []string{"release-manager"},
		NotBefore: "2000-01-01T00:00:00Z", NotAfter: "9999-01-01T00:00:00Z",
	}}}
	trustPath := filepath.Join(externalRoot, "trusted-approvers.json")
	trustBytes, err := json.Marshal(trust)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trustPath, trustBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(root, "cutover-report.json")
	report := cutover.Report{
		SchemaVersion: cutover.SchemaVersion,
		Repository:    "threadforge-fixture/Threadforge",
		Candidate:     candidateCommit,
		Approvals: []cutover.Approval{{
			Role: "release-manager", Reviewer: "fixture-release-manager", Evidence: []cutover.Evidence{{
				Path: "review.txt", SHA256: hex.EncodeToString(evidenceHash[:]),
			}},
		}},
	}
	reportBytes, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, reportBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	outputReport := filepath.Join(root, "cutover-report-approved.json")
	args := []string{
		"cutover", "attest-approval", "--report", reportPath, "--role", "release-manager", "--decision", "approve",
		"--reason", "Reviewed the release evidence and its acceptance criteria.", "--approved-at", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
		"--evidence-root", evidenceRoot, "--candidate-root", candidateRoot, "--identity", identityPath,
		"--trusted-approvers", trustPath, "--attestation-path", "attestations/release.json", "--output-report", outputReport,
	}
	if confirm {
		args = append(args, "--confirm-approval")
	}
	return cutoverApprovalFixture{
		root: root, candidateRoot: candidateRoot, evidenceRoot: evidenceRoot, identityPath: identityPath,
		trustPath: trustPath, reportPath: reportPath, outputReport: outputReport, args: args, trust: trust,
	}
}
