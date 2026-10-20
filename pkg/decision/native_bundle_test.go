package decision

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validNativeBundleFixture() (json.RawMessage, NativeBundleValidation) {
	content := json.RawMessage(`{"model":"local-model","artifacts":{},"heads":{},"adaptation":{"0123456789abcdef":{"sum":[1.0,2.0],"sumsq":[1.0,4.0],"n":2}},"observations":{"0123456789abcdef":{"question_id":"review-choice","kind":"choice","text":"Which review?","options":["accept","reject"],"states":["private labeled state"],"labels":[0],"fits":0,"next_fit":30}}}`)
	receipt := NativeBundleValidation{
		SchemaVersion: NativeBundleValidationSchemaVersion,
		Status: "valid", Model: "local-model",
		AdaptationCount: 1, ObservationQuestionCount: 1, ObservationStateCount: 1,
		ContentSHA256: strings.Repeat("a", 64),
		HumanReviewRequired: true,
	}
	return content, receipt
}

func TestNativeBundleCandidateBindsReceiptAndKeepsWholePayload(t *testing.T) {
	content, receipt := validNativeBundleFixture()
	artifact, err := NewNativeBundleArtifact("bundle-one", content, receipt, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != NativeBundleCandidate || artifact.Revision != 1 || artifact.Model != "local-model" ||
		artifact.AdaptationCount != 1 || artifact.ObservationStateCount != 1 || !json.Valid(artifact.Content) {
		t.Fatalf("unexpected native bundle candidate: %#v", artifact)
	}
	if !strings.Contains(string(artifact.Content), "private labeled state") {
		t.Fatal("private observations were not retained in the full local bundle")
	}
	if err := ValidateNativeBundleArtifact(artifact); err != nil {
		t.Fatalf("candidate failed validation: %v", err)
	}
	if strings.Contains(string(mustMarshalNativeBundleSummary(t, artifact)), "private labeled state") {
		t.Fatal("native bundle summary exposed labeled state")
	}
}

func TestNativeBundleRejectsReceiptMismatchSecretsAndTampering(t *testing.T) {
	content, receipt := validNativeBundleFixture()
	wrongCount := receipt
	wrongCount.ObservationStateCount++
	if _, err := NewNativeBundleArtifact("bundle-one", content, wrongCount, time.Now().UTC()); err == nil {
		t.Fatal("accepted validation receipt with mismatched observation count")
	}

	secret := json.RawMessage(`{"model":"local-model","artifacts":{},"heads":{},"adaptation":{},"observations":{"0123456789abcdef":{"states":["API_TOKEN=secret-value"]}}}`)
	if _, err := NewNativeBundleArtifact("bundle-one", secret, receipt, time.Now().UTC()); err == nil {
		t.Fatal("accepted a credential-bearing native bundle")
	}

	artifact, err := NewNativeBundleArtifact("bundle-one", content, receipt, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	artifact.Content = json.RawMessage(`{"model":"local-model","artifacts":{},"heads":{},"adaptation":{},"observations":{}}`)
	if err := ValidateNativeBundleArtifact(artifact); err == nil {
		t.Fatal("accepted content modified after its digest was recorded")
	}
}

func TestNativeBundleSummaryDoesNotContainBundleContent(t *testing.T) {
	content, receipt := validNativeBundleFixture()
	artifact, err := NewNativeBundleArtifact("bundle-one", content, receipt, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(SummarizeNativeBundle(artifact))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private labeled state") || strings.Contains(string(encoded), `"content"`) {
		t.Fatalf("summary leaked the bundle payload: %s", encoded)
	}
}

func TestNativeBundleRejectsMalformedCheckpointFingerprint(t *testing.T) {
	content, receipt := validNativeBundleFixture()
	artifact, err := NewNativeBundleArtifact("bundle-one", content, receipt, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	artifact.Status = NativeBundleActive
	artifact.Revision = 2
	artifact.ModelCheckpointSHA256 = "not-a-digest"
	if err := ValidateNativeBundleArtifact(artifact); err == nil {
		t.Fatal("accepted a malformed approved model checkpoint fingerprint")
	}
}

func mustMarshalNativeBundleSummary(t *testing.T, artifact NativeBundleArtifact) []byte {
	t.Helper()
	encoded, err := json.Marshal(SummarizeNativeBundle(artifact))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
