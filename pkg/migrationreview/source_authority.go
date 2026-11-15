package migrationreview

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	SourceAuthorityAttestationSchemaVersion = "threadforge.mancode-source-authority-attestation.v1"
	SourceAuthorityDecision                 = "authoritative_source_inventory_confirmed"
)

// SourceAuthorityBinding commits a trusted migration reviewer to the exact
// path-free source-set artifact that was independently checked for authority
// and completeness. The signature is a human claim, not machine proof that the
// roots are authoritative.
type SourceAuthorityBinding struct {
	Product                  string `json:"product"`
	SourceSetArtifactSHA256  string `json:"source_set_artifact_sha256"`
	InventorySHA256          string `json:"inventory_sha256"`
	ProjectSourceFingerprint string `json:"project_source_fingerprint"`
	ProjectSnapshotSHA256    string `json:"project_snapshot_sha256"`
	ComponentCount           int    `json:"component_count"`
}

type SourceAuthorityAttestation struct {
	SchemaVersion string `json:"schema_version"`
	Reviewer      string `json:"reviewer_id"`
	KeyID         string `json:"key_id"`
	SourceAuthorityBinding
	Decision  string `json:"decision"`
	IssuedAt  string `json:"issued_at"`
	Signature string `json:"signature"`
}

type signedSourceAuthorityPayload struct {
	SchemaVersion string `json:"schema_version"`
	Reviewer      string `json:"reviewer_id"`
	KeyID         string `json:"key_id"`
	SourceAuthorityBinding
	Decision string `json:"decision"`
	IssuedAt string `json:"issued_at"`
}

func ValidateSourceAuthorityBinding(binding SourceAuthorityBinding) error {
	if binding.Product != "mancode" || !digestPattern.MatchString(binding.SourceSetArtifactSHA256) ||
		!digestPattern.MatchString(binding.InventorySHA256) || !digestPattern.MatchString(binding.ProjectSourceFingerprint) ||
		!digestPattern.MatchString(binding.ProjectSnapshotSHA256) || binding.ComponentCount < 1 || binding.ComponentCount > 1024 {
		return errors.New("Mancode source-authority binding is incomplete or invalid")
	}
	return nil
}

func SignSourceAuthority(binding SourceAuthorityBinding, reviewer string, privateKey ed25519.PrivateKey, issuedAt time.Time) (SourceAuthorityAttestation, error) {
	if err := ValidateSourceAuthorityBinding(binding); err != nil {
		return SourceAuthorityAttestation{}, err
	}
	if !reviewerPattern.MatchString(reviewer) || len(privateKey) != ed25519.PrivateKeySize || issuedAt.IsZero() {
		return SourceAuthorityAttestation{}, errors.New("Mancode source-authority signer identity or timestamp is invalid")
	}
	attestation := SourceAuthorityAttestation{
		SchemaVersion:          SourceAuthorityAttestationSchemaVersion,
		Reviewer:               reviewer,
		KeyID:                  KeyID(privateKey.Public().(ed25519.PublicKey)),
		SourceAuthorityBinding: binding,
		Decision:               SourceAuthorityDecision,
		IssuedAt:               issuedAt.UTC().Format(time.RFC3339Nano),
	}
	message, err := sourceAuthoritySigningBytes(attestation)
	if err != nil {
		return SourceAuthorityAttestation{}, err
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message))
	return attestation, nil
}

func ValidateSourceAuthorityAttestation(attestation SourceAuthorityAttestation) error {
	if attestation.SchemaVersion != SourceAuthorityAttestationSchemaVersion || attestation.Decision != SourceAuthorityDecision ||
		!reviewerPattern.MatchString(attestation.Reviewer) || !digestPattern.MatchString(attestation.KeyID) {
		return errors.New("Mancode source-authority attestation identity or decision is invalid")
	}
	if err := ValidateSourceAuthorityBinding(attestation.SourceAuthorityBinding); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, attestation.IssuedAt); err != nil {
		return errors.New("Mancode source-authority attestation issued_at must use RFC3339")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(attestation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("Mancode source-authority signature is malformed")
	}
	return nil
}

func VerifySourceAuthority(attestation SourceAuthorityAttestation, trust TrustStore, expected SourceAuthorityBinding) error {
	if err := ValidateTrustStore(trust); err != nil {
		return err
	}
	if err := ValidateSourceAuthorityBinding(expected); err != nil {
		return err
	}
	if err := ValidateSourceAuthorityAttestation(attestation); err != nil {
		return err
	}
	if attestation.SourceAuthorityBinding != expected {
		return errors.New("Mancode source-authority attestation does not match the expected source inventory")
	}
	issuedAt, _ := time.Parse(time.RFC3339Nano, attestation.IssuedAt)
	var trusted *ReviewerKey
	for index := range trust.Keys {
		if trust.Keys[index].KeyID == attestation.KeyID {
			trusted = &trust.Keys[index]
			break
		}
	}
	if trusted == nil || trusted.Reviewer != attestation.Reviewer || !contains(trusted.Products, expected.Product) || !hasPurpose(*trusted, PurposeSourceAuthority) {
		return errors.New("Mancode source-authority signing key is not trusted for this reviewer, product, and purpose")
	}
	notBefore, _ := time.Parse(time.RFC3339Nano, trusted.NotBefore)
	notAfter, _ := time.Parse(time.RFC3339Nano, trusted.NotAfter)
	if issuedAt.Before(notBefore) || issuedAt.After(notAfter) {
		return errors.New("Mancode source-authority attestation was issued outside the trusted key validity window")
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(trusted.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("Mancode source-authority trust key is malformed")
	}
	signature, _ := base64.StdEncoding.Strict().DecodeString(attestation.Signature)
	message, err := sourceAuthoritySigningBytes(attestation)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return errors.New("Mancode source-authority signature verification failed")
	}
	return nil
}

func SourceAuthorityBindingForArtifact(artifact []byte, inventorySHA256, projectSourceFingerprint, projectSnapshotSHA256 string, componentCount int) (SourceAuthorityBinding, error) {
	digest := sha256.Sum256(artifact)
	binding := SourceAuthorityBinding{
		Product:                  "mancode",
		SourceSetArtifactSHA256:  hex.EncodeToString(digest[:]),
		InventorySHA256:          inventorySHA256,
		ProjectSourceFingerprint: projectSourceFingerprint,
		ProjectSnapshotSHA256:    projectSnapshotSHA256,
		ComponentCount:           componentCount,
	}
	if err := ValidateSourceAuthorityBinding(binding); err != nil {
		return SourceAuthorityBinding{}, fmt.Errorf("build Mancode source-authority binding: %w", err)
	}
	return binding, nil
}

func sourceAuthoritySigningBytes(attestation SourceAuthorityAttestation) ([]byte, error) {
	return json.Marshal(signedSourceAuthorityPayload{
		SchemaVersion:          attestation.SchemaVersion,
		Reviewer:               attestation.Reviewer,
		KeyID:                  attestation.KeyID,
		SourceAuthorityBinding: attestation.SourceAuthorityBinding,
		Decision:               attestation.Decision,
		IssuedAt:               attestation.IssuedAt,
	})
}
