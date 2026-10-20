package decision

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestL1ArtifactBindingAndDigest(t *testing.T) {
	content := json.RawMessage(`{"model":"local-model","question":"0123456789abcdef","method":"temperature","temperature":1.2,"prior_method":"batch","prior_strength":0.75,"n_calib":8,"prior":[[0.5,0.5]]}`)
	artifact, err := NewArtifact("decision-calibration", "L1", content, time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != ArtifactCandidate || artifact.Revision != 1 || artifact.QuestionKey != "0123456789abcdef" || artifact.CalibrationExamples != 8 {
		t.Fatalf("unexpected artifact metadata: %#v", artifact)
	}
	if err := ValidateArtifact(artifact); err != nil {
		t.Fatalf("valid L1 artifact rejected: %v", err)
	}
	artifact.ContentSHA256 = strings.Repeat("0", 64)
	if err := ValidateArtifact(artifact); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered artifact digest error = %v", err)
	}
}

func TestL2ArtifactBoundsAndRejectsCredentialContent(t *testing.T) {
	content := json.RawMessage(`{"model":"local-model","question":"0123456789abcdef","question_id":"q-one","kind":"choice","text":"Pick an option","options":["a","b"],"method":"head:ridge","layer_abs":2,"n_blocks":4,"hidden_size":2,"temperature":1.1,"n_calib":8,"W":[[1,0],[0,1]],"b":[0,0],"mean":[0,0],"scale":[1,1],"params":{},"cv":{}}`)
	artifact, err := NewArtifact("decision-head", "L2", content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateArtifact(artifact); err != nil {
		t.Fatalf("valid L2 artifact rejected: %v", err)
	}
	artifact.CalibrationExamples = 7
	if err := ValidateArtifact(artifact); err == nil || !strings.Contains(err.Error(), "calibration examples") {
		t.Fatalf("under-sampled L2 artifact error = %v", err)
	}
	secret := json.RawMessage(`{"model":"local-model","question":"0123456789abcdef","method":"temperature","temperature":1.1,"n_calib":8,"api_key":"unsafe"}`)
	if _, err := NewArtifact("decision-secret", "L1", secret, time.Now()); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("credential-bearing artifact error = %v", err)
	}
}

func TestImportedAnyJevCalibrationBecomesUnapprovedThreadforgeCandidate(t *testing.T) {
	content := []byte(`{"model":"local-model","question":"0123456789abcdef","method":"temperature","temperature":1.2,"prior_method":"batch","prior_strength":0.75,"n_calib":8}`)
	artifact, err := NewImportedArtifact("imported-calibration", content, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != ArtifactCandidate || artifact.Revision != 1 || artifact.Level != "L1" ||
		artifact.Model != "local-model" || artifact.QuestionKey != "0123456789abcdef" || artifact.CalibrationExamples != 8 {
		t.Fatalf("imported payload did not become a bound candidate: %#v", artifact)
	}
	if err := ValidateArtifact(artifact); err != nil {
		t.Fatalf("imported candidate failed storage validation: %v", err)
	}
}

func TestImportedAnyJevHeadAndRejectsBundlesOrSecrets(t *testing.T) {
	head := []byte(`{"model":"local-model","question":"0123456789abcdef","method":"head:ridge","question_id":"q-one","kind":"choice","text":"Pick an option","options":["a","b"],"layer_abs":2,"n_blocks":4,"hidden_size":2,"temperature":1.1,"n_calib":8,"W":[[1,0],[0,1]],"b":[0,0],"mean":[0,0],"scale":[1,1],"params":{},"cv":{}}`)
	artifact, err := NewImportedArtifact("imported-head", head, time.Now().UTC())
	if err != nil || artifact.Level != "L2" {
		t.Fatalf("valid upstream head import artifact=%#v error=%v", artifact, err)
	}
	if _, err := NewImportedArtifact("imported-bundle", []byte(`{"model":"local-model","artifacts":{},"heads":{}}`), time.Now().UTC()); err == nil {
		t.Fatal("bundle-level content must not be misclassified as one per-question artifact")
	}
	secret := []byte(`{"model":"local-model","question":"0123456789abcdef","method":"temperature","temperature":1.2,"n_calib":8,"api_key":"secret-value"}`)
	if _, err := NewImportedArtifact("imported-secret", secret, time.Now().UTC()); err == nil {
		t.Fatal("credential-bearing calibration payload must be rejected")
	}
}

func TestArtifactCheckpointFingerprintIsApprovalBoundAndValidated(t *testing.T) {
	content := json.RawMessage(`{"model":"local-model","question":"0123456789abcdef","method":"temperature","temperature":1.2,"n_calib":8}`)
	artifact, err := NewArtifact("checkpoint-bound", "L1", content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	artifact.Status = ArtifactActive
	artifact.Revision = 2
	artifact.ModelCheckpointSHA256 = strings.Repeat("a", 64)
	if err := ValidateArtifact(artifact); err != nil {
		t.Fatalf("approved artifact with a valid checkpoint fingerprint rejected: %v", err)
	}

	artifact.ModelCheckpointSHA256 = "not-a-digest"
	if err := ValidateArtifact(artifact); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("invalid checkpoint fingerprint error = %v", err)
	}
	artifact.Status = ArtifactCandidate
	artifact.Revision = 1
	artifact.ModelCheckpointSHA256 = strings.Repeat("a", 64)
	if err := ValidateArtifact(artifact); err == nil || !strings.Contains(err.Error(), "approved artifacts") {
		t.Fatalf("candidate checkpoint fingerprint error = %v", err)
	}
	artifact.Status = ArtifactRejected
	artifact.Revision = 2
	if err := ValidateArtifact(artifact); err == nil || !strings.Contains(err.Error(), "approved artifacts") {
		t.Fatalf("rejected checkpoint fingerprint error = %v", err)
	}
}
