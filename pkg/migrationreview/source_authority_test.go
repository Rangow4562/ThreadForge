package migrationreview

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestSourceAuthorityAttestationBindsExactSourceSetAndRequiresExplicitPurpose(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	binding := SourceAuthorityBinding{
		Product: "mancode", SourceSetArtifactSHA256: strings.Repeat("a", 64), InventorySHA256: strings.Repeat("b", 64),
		ProjectSourceFingerprint: strings.Repeat("c", 64), ProjectSnapshotSHA256: strings.Repeat("d", 64), ComponentCount: 3,
	}
	attestation, err := SignSourceAuthority(binding, "source-authority-reviewer", privateKey, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	trust := fixtureTrust(publicKey, issuedAt)
	trust.Keys[0].Reviewer = "source-authority-reviewer"
	if err := VerifySourceAuthority(attestation, trust, binding); err == nil || !strings.Contains(err.Error(), "purpose") {
		t.Fatalf("legacy plan-review-only key authorized source-root completeness: %v", err)
	}
	trust.Keys[0].Purposes = []string{PurposeSourceAuthority}
	if err := VerifySourceAuthority(attestation, trust, binding); err != nil {
		t.Fatalf("explicitly trusted source-authority attestation rejected: %v", err)
	}
	planAttestation, err := Sign(fixtureBinding(), "source-authority-reviewer", privateKey, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(planAttestation, trust, fixtureBinding()); err == nil {
		t.Fatal("source-authority-only key was allowed to approve legacy plan pairing")
	}
	changed := binding
	changed.SourceSetArtifactSHA256 = strings.Repeat("e", 64)
	if err := VerifySourceAuthority(attestation, trust, changed); err == nil {
		t.Fatal("source-authority attestation replayed against a different source-set artifact")
	}
	changed = binding
	changed.ComponentCount++
	if err := VerifySourceAuthority(attestation, trust, changed); err == nil {
		t.Fatal("source-authority attestation replayed against a different root count")
	}
	attestation.Decision = "confirmed"
	if err := VerifySourceAuthority(attestation, trust, binding); err == nil {
		t.Fatal("modified source-authority decision was accepted")
	}
}

func TestSourceAuthorityTrustPurposeValidationAndBindingBounds(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	trust := fixtureTrust(publicKey, now)
	trust.Keys[0].Purposes = []string{PurposeSourceAuthority, PurposePlanReview}
	if err := ValidateTrustStore(trust); err != nil {
		t.Fatalf("valid scoped purpose list rejected: %v", err)
	}
	firstDigest, err := TrustStoreSHA256(trust)
	if err != nil {
		t.Fatal(err)
	}
	trust.Keys[0].Purposes = []string{PurposePlanReview, PurposeSourceAuthority}
	secondDigest, err := TrustStoreSHA256(trust)
	if err != nil || firstDigest != secondDigest {
		t.Fatalf("trust digest depends on purpose ordering: first=%s second=%s err=%v", firstDigest, secondDigest, err)
	}
	trust.Keys[0].Purposes = []string{}
	if err := ValidateTrustStore(trust); err == nil {
		t.Fatal("explicit empty purposes list was treated as legacy plan-review scope")
	}
	trust.Keys[0].Purposes = []string{PurposeSourceAuthority, PurposeSourceAuthority}
	if err := ValidateTrustStore(trust); err == nil {
		t.Fatal("duplicate trust purposes were accepted")
	}
	trust.Keys[0].Purposes = []string{"mancode_release_approval"}
	if err := ValidateTrustStore(trust); err == nil {
		t.Fatal("unknown trust purpose was accepted")
	}
	if err := ValidateSourceAuthorityBinding(SourceAuthorityBinding{
		Product: "mancode", SourceSetArtifactSHA256: strings.Repeat("a", 64), InventorySHA256: strings.Repeat("b", 64),
		ProjectSourceFingerprint: strings.Repeat("c", 64), ProjectSnapshotSHA256: strings.Repeat("d", 64), ComponentCount: 1025,
	}); err == nil {
		t.Fatal("source-set root count above the discovery bound was accepted")
	}
}

func TestSourceAuthorityBindingHashesExactArtifactBytes(t *testing.T) {
	artifact := []byte("{\"fixture\":true}\n")
	binding, err := SourceAuthorityBindingForArtifact(artifact, strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64), 1)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(artifact)
	if binding.SourceSetArtifactSHA256 != hex.EncodeToString(expected[:]) || binding.ComponentCount != 1 {
		t.Fatalf("unexpected source-set artifact binding: %+v", binding)
	}
	if _, err := SourceAuthorityBindingForArtifact(artifact, "bad", strings.Repeat("c", 64), strings.Repeat("d", 64), 1); err == nil {
		t.Fatal("invalid source-set digest was accepted")
	}
}

func TestSourceAuthoritySignatureRejectsUnknownKeyAndInvalidEncoding(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	binding := SourceAuthorityBinding{
		Product: "mancode", SourceSetArtifactSHA256: strings.Repeat("a", 64), InventorySHA256: strings.Repeat("b", 64),
		ProjectSourceFingerprint: strings.Repeat("c", 64), ProjectSnapshotSHA256: strings.Repeat("d", 64), ComponentCount: 1,
	}
	attestation, err := SignSourceAuthority(binding, "source-authority-reviewer", privateKey, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	trust := fixtureTrust(publicKey, issuedAt)
	trust.Keys[0].Purposes = []string{PurposeSourceAuthority}
	trust.Keys[0].PublicKey = base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	if err := VerifySourceAuthority(attestation, trust, binding); err == nil {
		t.Fatal("malformed trust public key was accepted")
	}
	trust = fixtureTrust(publicKey, issuedAt)
	trust.Keys[0].Reviewer = "source-authority-reviewer"
	trust.Keys[0].Purposes = []string{PurposeSourceAuthority}
	attestation.Signature = "not-base64"
	if err := VerifySourceAuthority(attestation, trust, binding); err == nil {
		t.Fatal("invalid signature encoding was accepted")
	}
}
