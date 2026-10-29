package migrationreview

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadTrustStoreRejectsDuplicateJSONKeys(t *testing.T) {
	for _, input := range []string{
		`{"schema_version":"one","schema_version":"two"}`,
		`{"schema_version":"one","keys":[{"key_id":"first","key_id":"second"}]}`,
	} {
		path := filepath.Join(t.TempDir(), "reviewers.json")
		if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadTrustStore(path); err == nil || !strings.Contains(err.Error(), "duplicate object key") {
			t.Fatalf("duplicate reviewer trust key was not rejected for %s: %v", input, err)
		}
	}
}

func TestPlanReviewAttestationBindsSnapshotWorkflowMetadataPlanAndVersion(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	binding := fixtureBinding()
	attestation, err := Sign(binding, "migration-reviewer", privateKey, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	trust := fixtureTrust(publicKey, issuedAt)
	if err := Verify(attestation, trust, binding); err != nil {
		t.Fatalf("valid review rejected: %v", err)
	}
	changed := binding
	changed.PlanSourceSetSHA256 = strings.Repeat("f", 64)
	if err := Verify(attestation, trust, changed); err == nil {
		t.Fatal("review was replayed for a different plan source")
	}
	changed = binding
	changed.MetadataPlanVersion++
	if err := Verify(attestation, trust, changed); err == nil {
		t.Fatal("review was replayed for a different declared plan version")
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	if err := Verify(attestation, trust, binding); err == nil {
		t.Fatal("modified signature was accepted")
	}
}

func TestPlanReviewTrustRequiresMatchingReviewerProductAndValidityWindow(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	attestation, err := Sign(fixtureBinding(), "migration-reviewer", privateKey, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	trust := fixtureTrust(publicKey, issuedAt)
	trust.Keys[0].Reviewer = "another-reviewer"
	if err := Verify(attestation, trust, fixtureBinding()); err == nil {
		t.Fatal("attestation accepted under another reviewer identity")
	}
	trust = fixtureTrust(publicKey, issuedAt)
	trust.Keys[0].Products = []string{"beacon"}
	if err := Verify(attestation, trust, fixtureBinding()); err == nil {
		t.Fatal("attestation accepted outside Mancode scope")
	}
	trust = fixtureTrust(publicKey, issuedAt)
	trust.Keys[0].NotAfter = issuedAt.Add(-time.Second).Format(time.RFC3339)
	if err := Verify(attestation, trust, fixtureBinding()); err == nil {
		t.Fatal("attestation accepted outside the trust validity window")
	}
}

func fixtureBinding() Binding {
	return Binding{
		Product: "mancode", SourceFingerprint: strings.Repeat("a", 64), SnapshotSHA256: strings.Repeat("b", 64),
		WorkflowIDSHA256: strings.Repeat("c", 64), MetadataSourceSetSHA256: strings.Repeat("d", 64),
		PlanSourceSetSHA256: strings.Repeat("e", 64), MetadataPlanVersion: 7,
	}
}

func fixtureTrust(publicKey ed25519.PublicKey, now time.Time) TrustStore {
	return TrustStore{SchemaVersion: TrustStoreSchemaVersion, Keys: []ReviewerKey{{
		KeyID: KeyID(publicKey), PublicKey: base64.StdEncoding.EncodeToString(publicKey), Reviewer: "migration-reviewer",
		Products: []string{"mancode"}, NotBefore: now.Add(-time.Hour).Format(time.RFC3339),
		NotAfter: now.Add(time.Hour).Format(time.RFC3339),
	}}}
}
