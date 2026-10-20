package decision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const ArtifactSchemaVersion = "threadforge.decision-artifact.v1"
const MaxArtifactBytes = 16 << 20

type ArtifactStatus string

const (
	ArtifactCandidate ArtifactStatus = "candidate"
	ArtifactActive    ArtifactStatus = "active"
	ArtifactRejected  ArtifactStatus = "rejected"
	ArtifactRetired   ArtifactStatus = "retired"
)

// Artifact contains calibration parameters only. Labeled states are never
// persisted here; the content stays in the local SQLite store.
type Artifact struct {
	SchemaVersion         string          `json:"schema_version"`
	ID                    string          `json:"id"`
	Revision              int             `json:"revision"`
	Status                ArtifactStatus  `json:"status"`
	Model                 string          `json:"model"`
	ModelCheckpointSHA256 string          `json:"model_checkpoint_sha256,omitempty"`
	QuestionKey           string          `json:"question_key"`
	Level                 string          `json:"level"`
	CalibrationExamples   int             `json:"calibration_examples"`
	ContentSHA256         string          `json:"content_sha256"`
	Content               json.RawMessage `json:"content"`
	CreatedAt             string          `json:"created_at"`
	UpdatedAt             string          `json:"updated_at"`
}

var (
	artifactIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	questionKeyPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)
)

func NewArtifact(id, level string, content json.RawMessage, at time.Time) (Artifact, error) {
	canonical, err := CanonicalJSON(content)
	if err != nil {
		return Artifact{}, err
	}
	var envelope struct {
		Model    string `json:"model"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal(canonical, &envelope); err != nil {
		return Artifact{}, err
	}
	calibrationExamples, err := calibrationCount(canonical)
	if err != nil {
		return Artifact{}, err
	}
	hash := sha256.Sum256(canonical)
	stamp := at.UTC().Format(time.RFC3339Nano)
	artifact := Artifact{
		SchemaVersion: ArtifactSchemaVersion, ID: id, Revision: 1, Status: ArtifactCandidate,
		Model: envelope.Model, QuestionKey: envelope.Question, Level: level,
		CalibrationExamples: calibrationExamples, ContentSHA256: hex.EncodeToString(hash[:]),
		Content: canonical, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := ValidateArtifact(artifact); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// NewImportedArtifact wraps one upstream AnyJev per-question calibration or
// head payload as a local review candidate. Bundle-level adaptation statistics
// and observations are intentionally not accepted by this import path.
func NewImportedArtifact(id string, content []byte, at time.Time) (Artifact, error) {
	canonical, err := CanonicalJSON(content)
	if err != nil {
		return Artifact{}, err
	}
	var envelope struct {
		Model    string `json:"model"`
		Question string `json:"question"`
		Method   string `json:"method"`
	}
	if err := json.Unmarshal(canonical, &envelope); err != nil {
		return Artifact{}, err
	}
	level := ""
	switch {
	case envelope.Method == "temperature":
		level = "L1"
	case strings.HasPrefix(envelope.Method, "head:"):
		level = "L2"
	default:
		return Artifact{}, errors.New("import content is not an AnyJev per-question calibration or head")
	}
	validated, _, err := ValidateCalibrationContent(canonical, envelope.Model, envelope.Question, level)
	if err != nil {
		return Artifact{}, err
	}
	return NewArtifact(id, level, validated, at)
}

func CanonicalJSON(content []byte) (json.RawMessage, error) {
	if len(content) == 0 || len(content) > MaxArtifactBytes || !json.Valid(content) {
		return nil, errors.New("artifact content must be valid JSON no larger than 16 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode artifact content: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, errors.New("artifact content must be a JSON object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("canonicalize artifact content: %w", err)
	}
	return canonical, nil
}

func ValidateArtifact(artifact Artifact) error {
	var problems []string
	if artifact.SchemaVersion != ArtifactSchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	if !artifactIDPattern.MatchString(artifact.ID) {
		problems = append(problems, "id must be a lowercase stable identifier")
	}
	if artifact.Revision < 1 {
		problems = append(problems, "revision must be at least 1")
	}
	switch artifact.Status {
	case ArtifactCandidate:
		if artifact.Revision != 1 {
			problems = append(problems, "candidate artifacts must remain at revision 1")
		}
	case ArtifactActive, ArtifactRejected:
		if artifact.Revision != 2 {
			problems = append(problems, "approved or rejected artifacts must be at revision 2")
		}
	case ArtifactRetired:
		if artifact.Revision != 3 {
			problems = append(problems, "retired artifacts must be at revision 3")
		}
	default:
		problems = append(problems, "status must be candidate, active, rejected, or retired")
	}
	if artifact.ModelCheckpointSHA256 != "" && !validNativeBundleSHA256(artifact.ModelCheckpointSHA256) {
		problems = append(problems, "model checkpoint fingerprint must be a lowercase SHA-256 digest")
	}
	if (artifact.Status == ArtifactCandidate || artifact.Status == ArtifactRejected) && artifact.ModelCheckpointSHA256 != "" {
		problems = append(problems, "model checkpoint fingerprints may only be bound to approved artifacts")
	}
	if !bounded(artifact.Model, 512) || privacy.ContainsCredentialText(artifact.Model) {
		problems = append(problems, "model must be bounded and credential-free")
	}
	if !questionKeyPattern.MatchString(artifact.QuestionKey) {
		problems = append(problems, "question_key must be a 16-character lowercase hex digest")
	}
	if artifact.Level != "L1" && artifact.Level != "L2" {
		problems = append(problems, "level must be L1 or L2")
	}
	if artifact.CalibrationExamples < 2 || artifact.CalibrationExamples > 100000 {
		problems = append(problems, "calibration_examples must be in [2, 100000]")
	}
	canonical, err := CanonicalJSON(artifact.Content)
	if err != nil {
		problems = append(problems, "content: "+err.Error())
	} else {
		hash := sha256.Sum256(canonical)
		if artifact.ContentSHA256 != hex.EncodeToString(hash[:]) {
			problems = append(problems, "content_sha256 does not match content")
		}
		if privacy.ContainsCredential(canonical) {
			problems = append(problems, "content contains credential material")
		}
		if err := validateContent(canonical, artifact); err != nil {
			problems = append(problems, "content: "+err.Error())
		}
	}
	created, createdOK := parseTimestamp(artifact.CreatedAt)
	updated, updatedOK := parseTimestamp(artifact.UpdatedAt)
	if !createdOK || !updatedOK {
		problems = append(problems, "created_at and updated_at must use RFC3339")
	} else if updated.Before(created) {
		problems = append(problems, "updated_at cannot precede created_at")
	}
	if len(problems) != 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// ValidateCalibrationContent validates the upstream AnyJev per-question L1/L2
// payload without adding Threadforge storage lifecycle metadata.
func ValidateCalibrationContent(content []byte, model, questionKey, level string) (json.RawMessage, int, error) {
	canonical, err := CanonicalJSON(content)
	if err != nil {
		return nil, 0, err
	}
	if !bounded(model, 512) || privacy.ContainsCredentialText(model) || !questionKeyPattern.MatchString(questionKey) {
		return nil, 0, errors.New("calibration import model or question binding is invalid")
	}
	if level != "L1" && level != "L2" {
		return nil, 0, errors.New("calibration import level must be L1 or L2")
	}
	if privacy.ContainsCredential(canonical) {
		return nil, 0, errors.New("calibration import contains credential material")
	}
	count, err := calibrationCount(canonical)
	if err != nil {
		return nil, 0, err
	}
	artifact := Artifact{Model: model, QuestionKey: questionKey, Level: level, CalibrationExamples: count}
	if err := validateContent(canonical, artifact); err != nil {
		return nil, 0, err
	}
	return canonical, count, nil
}

func validateContent(content []byte, artifact Artifact) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil || fields == nil {
		return errors.New("must be an object")
	}
	var envelope struct {
		Method   string `json:"method"`
		Model    string `json:"model"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil {
		return err
	}
	if envelope.Model != artifact.Model || envelope.Question != artifact.QuestionKey {
		return errors.New("model or question binding differs from artifact metadata")
	}
	if artifact.Level == "L1" && envelope.Method != "temperature" {
		return errors.New("L1 artifacts must use the temperature method")
	}
	if artifact.Level == "L2" && !strings.HasPrefix(envelope.Method, "head:") {
		return errors.New("L2 artifacts must use a head method")
	}
	if artifact.Level == "L1" {
		var fields struct {
			Temperature float64 `json:"temperature"`
		}
		if err := json.Unmarshal(content, &fields); err != nil || math.IsNaN(fields.Temperature) || math.IsInf(fields.Temperature, 0) || fields.Temperature <= 0 {
			return errors.New("L1 temperature must be finite and positive")
		}
	}
	if artifact.Level == "L2" {
		for _, key := range []string{"W", "b", "mean", "scale", "layer_abs", "hidden_size", "options", "text", "kind"} {
			if _, ok := fields[key]; !ok {
				return fmt.Errorf("L2 artifact is missing %s", key)
			}
		}
		kind := strings.TrimPrefix(envelope.Method, "head:")
		if kind != "diff_means" && kind != "lda" && kind != "ridge" && kind != "rrr" {
			return errors.New("L2 head method is unsupported")
		}
		var options []json.RawMessage
		if err := json.Unmarshal(fields["options"], &options); err != nil || len(options) < 2 || len(options) > 26 {
			return errors.New("L2 artifact options must contain 2 to 26 values")
		}
		minimum := 2 * len(options)
		if minimum < 8 {
			minimum = 8
		}
		if artifact.CalibrationExamples < minimum {
			return fmt.Errorf("L2 artifact needs at least %d calibration examples for its option count", minimum)
		}
	}
	count, err := calibrationCount(content)
	if err != nil {
		return err
	}
	if count != artifact.CalibrationExamples {
		return errors.New("n_calib differs from calibration_examples metadata")
	}
	return nil
}

func calibrationCount(content []byte) (int, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil || fields == nil {
		return 0, errors.New("artifact content must be an object")
	}
	var count int
	if err := json.Unmarshal(fields["n_calib"], &count); err != nil || count < 1 {
		return 0, errors.New("artifact n_calib must be a positive integer")
	}
	return count, nil
}

func bounded(value string, max int) bool { return strings.TrimSpace(value) != "" && len(value) <= max }

func parseTimestamp(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}
