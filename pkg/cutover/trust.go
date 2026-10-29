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
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	EvaluatorTrustStoreSchemaVersion   = "threadforge.cutover-evaluator-trust.v1"
	EvaluationAttestationSchemaVersion = "threadforge.cutover-evaluation-attestation.v1"
	maxEvaluatorTrustStoreBytes        = 1 << 20
)

type EvaluatorTrustStore struct {
	SchemaVersion string                `json:"schema_version"`
	Keys          []TrustedEvaluatorKey `json:"keys"`
}

type TrustedEvaluatorKey struct {
	KeyID       string   `json:"key_id"`
	PublicKey   string   `json:"public_key"`
	EvaluatorID string   `json:"evaluator_id"`
	Domains     []string `json:"domains"`
	NotBefore   string   `json:"not_before"`
	NotAfter    string   `json:"not_after"`
}

type EvaluationAttestationClaims struct {
	EvaluatorID     string `json:"evaluator_id"`
	Domain          string `json:"domain"`
	LegacyCommit    string `json:"legacy_commit"`
	CandidateCommit string `json:"candidate_commit"`
	DatasetSHA256   string `json:"dataset_sha256"`
	TaskSetSHA256   string `json:"task_set_sha256"`
	EvaluatorSHA256 string `json:"evaluator_sha256"`
	OutcomeSHA256   string `json:"outcome_sha256"`
	SplitRole       string `json:"split_role"`
	IssuedAt        string `json:"issued_at"`
}

type EvaluationAttestation struct {
	SchemaVersion string `json:"schema_version"`
	KeyID         string `json:"key_id"`
	EvaluationAttestationClaims
	Signature string `json:"signature"`
}

type signedEvaluationAttestation struct {
	SchemaVersion   string `json:"schema_version"`
	KeyID           string `json:"key_id"`
	EvaluatorID     string `json:"evaluator_id"`
	Domain          string `json:"domain"`
	LegacyCommit    string `json:"legacy_commit"`
	CandidateCommit string `json:"candidate_commit"`
	DatasetSHA256   string `json:"dataset_sha256"`
	TaskSetSHA256   string `json:"task_set_sha256"`
	EvaluatorSHA256 string `json:"evaluator_sha256"`
	OutcomeSHA256   string `json:"outcome_sha256"`
	SplitRole       string `json:"split_role"`
	IssuedAt        string `json:"issued_at"`
}

func ReadEvaluatorTrustStore(path string) (EvaluatorTrustStore, string, error) {
	var trust EvaluatorTrustStore
	absolute, err := filepath.Abs(path)
	if err != nil {
		return trust, "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxEvaluatorTrustStoreBytes {
		return trust, "", errors.New("trusted evaluator file must be a regular non-symlink file no larger than 1 MiB")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return trust, "", err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxEvaluatorTrustStoreBytes+1))
	finalInfo, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil || int64(len(content)) != info.Size() || finalInfo.Size() != info.Size() || !finalInfo.ModTime().Equal(info.ModTime()) {
		return trust, "", errors.New("trusted evaluator file changed or could not be read consistently")
	}
	if err := decodeUniqueJSON(content, &trust); err != nil {
		return trust, "", fmt.Errorf("decode trusted evaluator file: %w", err)
	}
	if err := ValidateEvaluatorTrustStore(trust); err != nil {
		return trust, "", err
	}
	digest, err := EvaluatorTrustStoreSHA256(trust)
	return trust, digest, err
}

func ValidateEvaluatorTrustStore(trust EvaluatorTrustStore) error {
	if trust.SchemaVersion != EvaluatorTrustStoreSchemaVersion || len(trust.Keys) == 0 || len(trust.Keys) > 256 {
		return errors.New("trusted evaluator key store schema or key count is invalid")
	}
	domains := make(map[string]bool, len(RequiredEvaluationDomains))
	for _, domain := range RequiredEvaluationDomains {
		domains[domain] = true
	}
	seen := make(map[string]bool, len(trust.Keys))
	for index, key := range trust.Keys {
		publicKey, err := base64.StdEncoding.Strict().DecodeString(key.PublicKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize || evaluatorKeyID(ed25519.PublicKey(publicKey)) != key.KeyID || seen[key.KeyID] {
			return fmt.Errorf("trusted evaluator key %d has an invalid or duplicate key identity", index)
		}
		seen[key.KeyID] = true
		if strings.TrimSpace(key.EvaluatorID) == "" || len(key.EvaluatorID) > 128 || privacy.ContainsCredentialText(key.EvaluatorID) || len(key.Domains) == 0 {
			return fmt.Errorf("trusted evaluator key %s has invalid scope", key.KeyID)
		}
		notBefore, beforeErr := time.Parse(time.RFC3339Nano, key.NotBefore)
		notAfter, afterErr := time.Parse(time.RFC3339Nano, key.NotAfter)
		if beforeErr != nil || afterErr != nil || !notBefore.Before(notAfter) {
			return fmt.Errorf("trusted evaluator key %s requires an increasing RFC3339 validity window", key.KeyID)
		}
		seenDomain := make(map[string]bool, len(key.Domains))
		for _, domain := range key.Domains {
			if !domains[domain] || seenDomain[domain] {
				return fmt.Errorf("trusted evaluator key %s has unknown or duplicate domain scope", key.KeyID)
			}
			seenDomain[domain] = true
		}
	}
	return nil
}

func EvaluatorTrustStoreSHA256(trust EvaluatorTrustStore) (string, error) {
	if err := ValidateEvaluatorTrustStore(trust); err != nil {
		return "", err
	}
	canonical := EvaluatorTrustStore{SchemaVersion: trust.SchemaVersion, Keys: append([]TrustedEvaluatorKey(nil), trust.Keys...)}
	for index := range canonical.Keys {
		canonical.Keys[index].Domains = append([]string(nil), canonical.Keys[index].Domains...)
		sort.Strings(canonical.Keys[index].Domains)
	}
	sort.Slice(canonical.Keys, func(i, j int) bool { return canonical.Keys[i].KeyID < canonical.Keys[j].KeyID })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func SignEvaluationAttestation(claims EvaluationAttestationClaims, privateKey ed25519.PrivateKey) (EvaluationAttestation, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return EvaluationAttestation{}, errors.New("evaluation signing key is invalid")
	}
	if err := validateEvaluationAttestationClaims(claims); err != nil {
		return EvaluationAttestation{}, err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	attestation := EvaluationAttestation{SchemaVersion: EvaluationAttestationSchemaVersion, KeyID: evaluatorKeyID(publicKey), EvaluationAttestationClaims: claims}
	message, err := evaluationAttestationSigningBytes(attestation)
	if err != nil {
		return EvaluationAttestation{}, err
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message))
	return attestation, nil
}

func VerifyEvaluationAttestation(attestation EvaluationAttestation, trust EvaluatorTrustStore, expected EvaluationAttestationClaims) error {
	if err := ValidateEvaluatorTrustStore(trust); err != nil {
		return err
	}
	if attestation.SchemaVersion != EvaluationAttestationSchemaVersion || attestation.KeyID == "" {
		return errors.New("unsupported or incomplete evaluator attestation")
	}
	if err := validateEvaluationAttestationClaims(attestation.EvaluationAttestationClaims); err != nil {
		return err
	}
	actual := attestation.EvaluationAttestationClaims
	if actual.EvaluatorID != expected.EvaluatorID || actual.Domain != expected.Domain || actual.LegacyCommit != expected.LegacyCommit ||
		actual.CandidateCommit != expected.CandidateCommit || actual.DatasetSHA256 != expected.DatasetSHA256 ||
		actual.TaskSetSHA256 != expected.TaskSetSHA256 || actual.EvaluatorSHA256 != expected.EvaluatorSHA256 ||
		actual.OutcomeSHA256 != expected.OutcomeSHA256 || actual.SplitRole != "held_out" {
		return errors.New("evaluator attestation claims do not match the report and outcome artifact")
	}
	var trusted *TrustedEvaluatorKey
	for index := range trust.Keys {
		if trust.Keys[index].KeyID == attestation.KeyID {
			trusted = &trust.Keys[index]
			break
		}
	}
	if trusted == nil || trusted.EvaluatorID != actual.EvaluatorID || !containsString(trusted.Domains, actual.Domain) {
		return errors.New("evaluator signing key is not trusted for this evaluator and domain")
	}
	issuedAt, _ := time.Parse(time.RFC3339Nano, actual.IssuedAt)
	notBefore, _ := time.Parse(time.RFC3339Nano, trusted.NotBefore)
	notAfter, _ := time.Parse(time.RFC3339Nano, trusted.NotAfter)
	if issuedAt.Before(notBefore) || issuedAt.After(notAfter) {
		return errors.New("evaluator attestation was issued outside the trusted key validity window")
	}
	publicKey, _ := base64.StdEncoding.Strict().DecodeString(trusted.PublicKey)
	signature, err := base64.StdEncoding.Strict().DecodeString(attestation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("evaluator attestation signature is malformed")
	}
	message, err := evaluationAttestationSigningBytes(attestation)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return errors.New("evaluator attestation signature verification failed")
	}
	return nil
}

func (v *artifactVerifier) readEvaluationAttestation(evidence Evidence) (EvaluationAttestation, error) {
	var attestation EvaluationAttestation
	content, actual, err := v.readEvidence(evidence)
	if err != nil {
		return attestation, err
	}
	if actual != evidence.SHA256 {
		return attestation, errors.New("attestation artifact digest mismatch")
	}
	if err := decodeUniqueJSON(content, &attestation); err != nil {
		return attestation, fmt.Errorf("invalid attestation JSON: %w", err)
	}
	return attestation, nil
}

func validateEvaluationAttestationClaims(claims EvaluationAttestationClaims) error {
	validDomain := false
	for _, domain := range RequiredEvaluationDomains {
		validDomain = validDomain || claims.Domain == domain
	}
	if strings.TrimSpace(claims.EvaluatorID) == "" || len(claims.EvaluatorID) > 128 || privacy.ContainsCredentialText(claims.EvaluatorID) ||
		!validDomain || !gitCommitRE.MatchString(claims.LegacyCommit) || !gitCommitRE.MatchString(claims.CandidateCommit) ||
		!digestRE.MatchString(claims.DatasetSHA256) || !digestRE.MatchString(claims.TaskSetSHA256) ||
		!digestRE.MatchString(claims.EvaluatorSHA256) || !digestRE.MatchString(claims.OutcomeSHA256) || claims.SplitRole != "held_out" {
		return errors.New("evaluator attestation claims are invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, claims.IssuedAt); err != nil {
		return errors.New("evaluator attestation requires an RFC3339 issued_at timestamp")
	}
	return nil
}

func evaluationAttestationSigningBytes(attestation EvaluationAttestation) ([]byte, error) {
	payload := signedEvaluationAttestation{
		SchemaVersion: attestation.SchemaVersion, KeyID: attestation.KeyID,
		EvaluatorID: attestation.EvaluatorID, Domain: attestation.Domain, LegacyCommit: attestation.LegacyCommit,
		CandidateCommit: attestation.CandidateCommit, DatasetSHA256: attestation.DatasetSHA256,
		TaskSetSHA256: attestation.TaskSetSHA256, EvaluatorSHA256: attestation.EvaluatorSHA256,
		OutcomeSHA256: attestation.OutcomeSHA256, SplitRole: attestation.SplitRole, IssuedAt: attestation.IssuedAt,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return append([]byte("THREADFORGE-CUTOVER-EVALUATION-ATTESTATION-V1\x00"), encoded...), nil
}

func evaluatorKeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:])
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
