package decision

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validObservationCheckpointContent() json.RawMessage {
	return json.RawMessage(`{"schema_version":"threadforge.anyjev-observation-checkpoint.v1","model":"local-model","question_key":"0123456789abcdef","fit_at":30,"refit_factor":2,"fit_options":{"layers":null,"kinds":["lda","ridge"],"n_folds":5,"seed":0,"listing":"auto"},"observation":{"question_id":"q-check","kind":"choice","text":"Which check?","options":["unit","integration"],"scale":[0,1],"ordered":false,"centers":null,"states":["safe labeled state"],"labels":[0],"fits":0,"next_fit":30}}`)
}

func TestObservationCheckpointValidatesAndBindsPrivateContent(t *testing.T) {
	content := validObservationCheckpointContent()
	checkpoint, err := NewObservationCheckpoint("parser-observations", 1, content, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.StreamID != "parser-observations" || checkpoint.Revision != 1 || checkpoint.Model != "local-model" ||
		checkpoint.QuestionKey != "0123456789abcdef" || checkpoint.ObservationCount != 1 || checkpoint.TotalFits != 0 {
		t.Fatalf("unexpected checkpoint metadata: %#v", checkpoint)
	}
	if err := ValidateObservationCheckpoint(checkpoint); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}
	checkpoint.ContentSHA256 = strings.Repeat("0", 64)
	if err := ValidateObservationCheckpoint(checkpoint); err == nil || !strings.Contains(err.Error(), "content_sha256") {
		t.Fatalf("tampered checkpoint digest error = %v", err)
	}
}

func TestObservationCheckpointRejectsSecretsUnknownFieldsAndInvalidLabels(t *testing.T) {
	secret := json.RawMessage(strings.Replace(string(validObservationCheckpointContent()), "safe labeled state", "API_TOKEN=secret-value", 1))
	if _, err := NewObservationCheckpoint("parser-observations", 1, secret, time.Now()); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("credential-bearing observation error = %v", err)
	}
	unknown := json.RawMessage(strings.Replace(string(validObservationCheckpointContent()), `"next_fit":30`, `"next_fit":30,"unexpected":true`, 1))
	if _, err := NewObservationCheckpoint("parser-observations", 1, unknown, time.Now()); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown checkpoint field error = %v", err)
	}
	badLabel := json.RawMessage(strings.Replace(string(validObservationCheckpointContent()), `"labels":[0]`, `"labels":[2]`, 1))
	if _, err := NewObservationCheckpoint("parser-observations", 1, badLabel, time.Now()); err == nil || !strings.Contains(err.Error(), "outside the option range") {
		t.Fatalf("invalid label error = %v", err)
	}
}
