package cutover

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	ApprovalTrustSchemaVersion       = "threadforge.cutover-approval-trust.v2"
	ApprovalAttestationSchemaVersion = "threadforge.cutover-approval-attestation.v2"
	maxApprovalTrustBytes            = 1 << 20
)

var approvalReviewerRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._@/-]{0,127}$`)

type ApprovalTrustStore struct {
	SchemaVersion string               `json:"schema_version"`
	Keys          []TrustedApprovalKey `json:"keys"`
}

type TrustedApprovalKey struct {
	KeyID     string   `json:"key_id"`
	PublicKey string   `json:"public_key"`
	Reviewer  string   `json:"reviewer_id"`
	Roles     []string `json:"roles"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
}

type ApprovalBinding struct {
	Role                     string `json:"role"`
	Reviewer                 string `json:"reviewer_id"`
	Approved                 bool   `json:"approved"`
	ReasonSHA256             string `json:"reason_sha256"`
	ApprovedAt               string `json:"approved_at"`
	CandidateCommit          string `json:"candidate_commit"`
	ReportEvidenceSHA256     string `json:"report_evidence_sha256"`
	SupportingEvidenceSHA256 string `json:"supporting_evidence_sha256"`
}

type ApprovalAttestation struct {
	SchemaVersion string `json:"schema_version"`
	KeyID         string `json:"key_id"`
	ApprovalBinding
	IssuedAt  string `json:"issued_at"`
	Signature string `json:"signature"`
}

type signedApprovalPayload struct {
	SchemaVersion string `json:"schema_version"`
	KeyID         string `json:"key_id"`
	ApprovalBinding
	IssuedAt string `json:"issued_at"`
}

func ReadApprovalTrustStore(path string) (ApprovalTrustStore, string, error) {
	var trust ApprovalTrustStore
	absolute, err := filepath.Abs(path)
	if err != nil {
		return trust, "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxApprovalTrustBytes {
		return trust, "", errors.New("trusted approver file must be a regular non-symlink file no larger than 1 MiB")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return trust, "", err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxApprovalTrustBytes+1))
	finalInfo, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil || int64(len(content)) != info.Size() ||
		finalInfo.Size() != info.Size() || !finalInfo.ModTime().Equal(info.ModTime()) {
		return trust, "", errors.New("trusted approver policy changed or could not be read consistently")
	}
	if err := decodeUniqueJSON(content, &trust); err != nil {
		return trust, "", fmt.Errorf("decode trusted approver policy: %w", err)
	}
	if err := ValidateApprovalTrustStore(trust); err != nil {
		return trust, "", err
	}
	digest, err := ApprovalTrustStoreSHA256(trust)
	return trust, digest, err
}

func ValidateApprovalTrustStore(trust ApprovalTrustStore) error {
	if trust.SchemaVersion != ApprovalTrustSchemaVersion || len(trust.Keys) == 0 || len(trust.Keys) > 256 {
		return errors.New("trusted approver policy schema or key count is invalid")
	}
	seen := make(map[string]bool, len(trust.Keys))
	for index, key := range trust.Keys {
		publicKey, err := base64.StdEncoding.Strict().DecodeString(key.PublicKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize || approvalKeyID(ed25519.PublicKey(publicKey)) != key.KeyID || seen[key.KeyID] {
			return fmt.Errorf("approver key %d has an invalid or duplicate key identity", index)
		}
		seen[key.KeyID] = true
		if !approvalReviewerRE.MatchString(key.Reviewer) || privacy.ContainsCredentialText(key.Reviewer) || len(key.Roles) == 0 || len(key.Roles) > len(RequiredApproverRoles) {
			return fmt.Errorf("approver key %s has invalid reviewer or role scope", key.KeyID)
		}
		seenRoles := make(map[string]bool, len(key.Roles))
		for _, role := range key.Roles {
			if !containsString(RequiredApproverRoles, role) || seenRoles[role] {
				return fmt.Errorf("approver key %s has unknown or duplicate role scope", key.KeyID)
			}
			seenRoles[role] = true
		}
		notBefore, beforeErr := time.Parse(time.RFC3339Nano, key.NotBefore)
		notAfter, afterErr := time.Parse(time.RFC3339Nano, key.NotAfter)
		if beforeErr != nil || afterErr != nil || !notBefore.Before(notAfter) {
			return fmt.Errorf("approver key %s requires an increasing RFC3339 validity window", key.KeyID)
		}
	}
	return nil
}

func ApprovalTrustStoreSHA256(trust ApprovalTrustStore) (string, error) {
	if err := ValidateApprovalTrustStore(trust); err != nil {
		return "", err
	}
	canonical := ApprovalTrustStore{SchemaVersion: trust.SchemaVersion, Keys: append([]TrustedApprovalKey(nil), trust.Keys...)}
	for index := range canonical.Keys {
		canonical.Keys[index].Roles = append([]string(nil), canonical.Keys[index].Roles...)
		sort.Strings(canonical.Keys[index].Roles)
	}
	sort.Slice(canonical.Keys, func(i, j int) bool { return canonical.Keys[i].KeyID < canonical.Keys[j].KeyID })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func ApprovalEvidenceFingerprint(report Report) (string, error) {
	canonical := report
	canonical.Approvals = nil
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func ApprovalBindingForReport(report Report, approval Approval) (ApprovalBinding, error) {
	if !containsString(RequiredApproverRoles, approval.Role) || !approvalReviewerRE.MatchString(approval.Reviewer) ||
		privacy.ContainsCredentialText(approval.Reviewer) || strings.TrimSpace(approval.Reason) == "" || len(approval.Reason) > 2000 ||
		privacy.ContainsCredentialText(approval.Reason) || !gitCommitRE.MatchString(report.Candidate) ||
		!GitHubRepositorySlugValid(report.Repository) {
		return ApprovalBinding{}, errors.New("cutover approval binding is incomplete or invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, approval.ApprovedAt); err != nil {
		return ApprovalBinding{}, errors.New("cutover approval approved_at must use RFC3339")
	}
	if len(approval.Evidence) == 0 {
		return ApprovalBinding{}, errors.New("cutover approval requires supporting evidence")
	}
	reportEvidenceSHA256, err := ApprovalEvidenceFingerprint(report)
	if err != nil {
		return ApprovalBinding{}, err
	}
	reasonDigest := sha256.Sum256([]byte(approval.Reason))
	supportingEvidence, err := json.Marshal(approval.Evidence)
	if err != nil {
		return ApprovalBinding{}, err
	}
	supportingDigest := sha256.Sum256(supportingEvidence)
	return ApprovalBinding{
		Role: approval.Role, Reviewer: approval.Reviewer, Approved: approval.Approved,
		ReasonSHA256: hex.EncodeToString(reasonDigest[:]), ApprovedAt: approval.ApprovedAt,
		CandidateCommit: report.Candidate, ReportEvidenceSHA256: reportEvidenceSHA256,
		SupportingEvidenceSHA256: hex.EncodeToString(supportingDigest[:]),
	}, nil
}

func SignApproval(binding ApprovalBinding, privateKey ed25519.PrivateKey, issuedAt time.Time) (ApprovalAttestation, error) {
	if err := validateApprovalBinding(binding); err != nil {
		return ApprovalAttestation{}, err
	}
	approvedAt, _ := time.Parse(time.RFC3339Nano, binding.ApprovedAt)
	if len(privateKey) != ed25519.PrivateKeySize || issuedAt.IsZero() || issuedAt.Before(approvedAt) {
		return ApprovalAttestation{}, errors.New("cutover approver identity or timestamp is invalid")
	}
	attestation := ApprovalAttestation{
		SchemaVersion:   ApprovalAttestationSchemaVersion,
		KeyID:           approvalKeyID(privateKey.Public().(ed25519.PublicKey)),
		ApprovalBinding: binding,
		IssuedAt:        issuedAt.UTC().Format(time.RFC3339Nano),
	}
	message, err := approvalSigningBytes(attestation)
	if err != nil {
		return ApprovalAttestation{}, err
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message))
	return attestation, nil
}

func ValidateApprovalAttestation(attestation ApprovalAttestation) error {
	if attestation.SchemaVersion != ApprovalAttestationSchemaVersion || !digestRE.MatchString(attestation.KeyID) {
		return errors.New("cutover approval attestation identity or schema is invalid")
	}
	if err := validateApprovalBinding(attestation.ApprovalBinding); err != nil {
		return err
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, attestation.IssuedAt)
	if err != nil {
		return errors.New("cutover approval attestation issued_at must use RFC3339")
	}
	approvedAt, _ := time.Parse(time.RFC3339Nano, attestation.ApprovedAt)
	if issuedAt.Before(approvedAt) {
		return errors.New("cutover approval attestation predates the approval claim")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(attestation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("cutover approval signature is malformed")
	}
	return nil
}

// VerifyApprovalEvidence checks the approval's supporting artifacts before a
// reviewer signs. Assessment repeats these checks later, so evidence modified
// after signing still blocks cutover.
func VerifyApprovalEvidence(evidenceRoot string, evidence []Evidence) error {
	root, err := checkedEvidenceRoot(evidenceRoot)
	if err != nil {
		return err
	}
	if len(evidence) == 0 {
		return errors.New("cutover approval requires supporting evidence")
	}
	verifier := &artifactVerifier{root: root, verified: make(map[string]string)}
	for _, item := range evidence {
		_, actual, err := verifier.readEvidence(item)
		if err != nil {
			return err
		}
		if actual != item.SHA256 {
			return errors.New("cutover approval supporting evidence digest mismatch")
		}
	}
	return nil
}

func VerifyApprovalAttestation(attestation ApprovalAttestation, trust ApprovalTrustStore, expected ApprovalBinding) error {
	if err := ValidateApprovalTrustStore(trust); err != nil {
		return err
	}
	if err := validateApprovalBinding(expected); err != nil {
		return err
	}
	if err := ValidateApprovalAttestation(attestation); err != nil {
		return err
	}
	if attestation.ApprovalBinding != expected {
		return errors.New("cutover approval attestation does not match the report approval and candidate evidence")
	}
	issuedAt, _ := time.Parse(time.RFC3339Nano, attestation.IssuedAt)
	var trusted *TrustedApprovalKey
	for index := range trust.Keys {
		if trust.Keys[index].KeyID == attestation.KeyID {
			trusted = &trust.Keys[index]
			break
		}
	}
	if trusted == nil || trusted.Reviewer != expected.Reviewer || !containsString(trusted.Roles, expected.Role) {
		return errors.New("cutover approval key is not trusted for this reviewer and role")
	}
	notBefore, _ := time.Parse(time.RFC3339Nano, trusted.NotBefore)
	notAfter, _ := time.Parse(time.RFC3339Nano, trusted.NotAfter)
	approvedAt, _ := time.Parse(time.RFC3339Nano, expected.ApprovedAt)
	if issuedAt.Before(notBefore) || issuedAt.After(notAfter) || approvedAt.Before(notBefore) || approvedAt.After(notAfter) {
		return errors.New("cutover approval or its signature is outside the trusted key validity window")
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(trusted.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("cutover approval trust key is malformed")
	}
	signature, _ := base64.StdEncoding.Strict().DecodeString(attestation.Signature)
	message, err := approvalSigningBytes(attestation)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return errors.New("cutover approval signature verification failed")
	}
	return nil
}

func validateApprovalBinding(binding ApprovalBinding) error {
	if !containsString(RequiredApproverRoles, binding.Role) || !approvalReviewerRE.MatchString(binding.Reviewer) ||
		privacy.ContainsCredentialText(binding.Reviewer) || !digestRE.MatchString(binding.ReasonSHA256) ||
		!gitCommitRE.MatchString(binding.CandidateCommit) || !digestRE.MatchString(binding.ReportEvidenceSHA256) ||
		!digestRE.MatchString(binding.SupportingEvidenceSHA256) {
		return errors.New("cutover approval binding identity or digest is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, binding.ApprovedAt); err != nil {
		return errors.New("cutover approval binding approved_at must use RFC3339")
	}
	return nil
}

func approvalSigningBytes(attestation ApprovalAttestation) ([]byte, error) {
	return json.Marshal(signedApprovalPayload{
		SchemaVersion:   attestation.SchemaVersion,
		KeyID:           attestation.KeyID,
		ApprovalBinding: attestation.ApprovalBinding,
		IssuedAt:        attestation.IssuedAt,
	})
}

func approvalKeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:])
}
