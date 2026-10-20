package decision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const NativeBundleSchemaVersion = "threadforge.decision-bundle.v1"
const NativeBundleValidationSchemaVersion = "threadforge.decision-bundle-validation.v1"

type NativeBundleStatus string

const (
	NativeBundleCandidate NativeBundleStatus = "candidate"
	NativeBundleActive    NativeBundleStatus = "active"
	NativeBundleRejected  NativeBundleStatus = "rejected"
	NativeBundleRetired   NativeBundleStatus = "retired"
)

// NativeBundleValidation is the bounded validation receipt returned by the
// Python AnyJev bridge. The receipt is metadata only; it never authorizes use.
type NativeBundleValidation struct {
	SchemaVersion           string `json:"schema_version"`
	Status                  string `json:"status"`
	Model                   string `json:"model"`
	ArtifactCount           int    `json:"artifact_count"`
	HeadCount               int    `json:"head_count"`
	AdaptationCount         int    `json:"adaptation_count"`
	ObservationQuestionCount int   `json:"observation_question_count"`
	ObservationStateCount   int    `json:"observation_state_count"`
	ContentSHA256           string `json:"content_sha256"`
	HumanReviewRequired     bool   `json:"human_review_required"`
	ActivationAllowed       bool   `json:"activation_allowed"`
	ExecutionAuthorized     bool   `json:"execution_authorized"`
	ModelEnvironmentVerified bool  `json:"model_environment_verified"`
	CutoverEvidenceEligible bool   `json:"cutover_evidence_eligible"`
}

// NativeBundleArtifact is a complete, private AnyJev export_artifacts bundle
// wrapped in Threadforge's local candidate/review lifecycle.
type NativeBundleArtifact struct {
	SchemaVersion            string              `json:"schema_version"`
	ID                       string              `json:"id"`
	Revision                 int                 `json:"revision"`
	Status                   NativeBundleStatus  `json:"status"`
	Model                    string              `json:"model"`
	ModelCheckpointSHA256    string              `json:"model_checkpoint_sha256,omitempty"`
	ArtifactCount            int                 `json:"artifact_count"`
	HeadCount                int                 `json:"head_count"`
	AdaptationCount          int                 `json:"adaptation_count"`
	ObservationQuestionCount int                 `json:"observation_question_count"`
	ObservationStateCount    int                 `json:"observation_state_count"`
	ContentSHA256            string              `json:"content_sha256"`
	ValidationSHA256         string              `json:"validation_sha256"`
	Content                  json.RawMessage     `json:"content"`
	CreatedAt                string              `json:"created_at"`
	UpdatedAt                string              `json:"updated_at"`
}

type NativeBundleSummary struct {
	SchemaVersion            string             `json:"schema_version"`
	ID                       string             `json:"id"`
	Revision                 int                `json:"revision"`
	Status                   NativeBundleStatus `json:"status"`
	Model                    string             `json:"model"`
	ModelCheckpointSHA256    string             `json:"model_checkpoint_sha256,omitempty"`
	ArtifactCount            int                `json:"artifact_count"`
	HeadCount                int                `json:"head_count"`
	AdaptationCount          int                `json:"adaptation_count"`
	ObservationQuestionCount int                `json:"observation_question_count"`
	ObservationStateCount    int                `json:"observation_state_count"`
	ContentSHA256            string             `json:"content_sha256"`
	ValidationSHA256         string             `json:"validation_sha256"`
	CreatedAt                string             `json:"created_at"`
	UpdatedAt                string             `json:"updated_at"`
}

type NativeBundleRevision struct {
	Bundle    NativeBundleSummary `json:"bundle"`
	Action    string              `json:"action"`
	Actor     string              `json:"actor"`
	Reason    string              `json:"reason"`
	CreatedAt string              `json:"created_at"`
}

type nativeBundleShape struct {
	Model        string                     `json:"model"`
	Artifacts    map[string]json.RawMessage `json:"artifacts"`
	Heads        map[string]json.RawMessage `json:"heads"`
	Adaptation   map[string]json.RawMessage `json:"adaptation"`
	Observations map[string]json.RawMessage `json:"observations"`
}

type nativeBundleObservation struct {
	QuestionID string    `json:"question_id"`
	Kind       string    `json:"kind"`
	Text       string    `json:"text"`
	Options    []string  `json:"options"`
	States     []string  `json:"states"`
	Labels     []int     `json:"labels"`
	Fits       int       `json:"fits"`
	NextFit    int       `json:"next_fit"`
	Scale      []float64 `json:"scale"`
	Ordered    *bool     `json:"ordered"`
	Centers    []float64 `json:"centers"`
}

func NewNativeBundleArtifact(id string, content json.RawMessage, validation NativeBundleValidation, at time.Time) (NativeBundleArtifact, error) {
	canonical, shape, err := validateNativeBundleContent(content)
	if err != nil {
		return NativeBundleArtifact{}, err
	}
	counts, err := nativeBundleCounts(shape)
	if err != nil {
		return NativeBundleArtifact{}, err
	}
	if err := validateNativeBundleReceipt(validation, shape.Model, counts); err != nil {
		return NativeBundleArtifact{}, err
	}
	digest := sha256.Sum256(canonical)
	stamp := at.UTC().Format(time.RFC3339Nano)
	artifact := NativeBundleArtifact{
		SchemaVersion: NativeBundleSchemaVersion,
		ID: id, Revision: 1, Status: NativeBundleCandidate,
		Model: shape.Model,
		ArtifactCount: counts.artifacts,
		HeadCount: counts.heads,
		AdaptationCount: counts.adaptation,
		ObservationQuestionCount: counts.observationQuestions,
		ObservationStateCount: counts.observationStates,
		ContentSHA256: hex.EncodeToString(digest[:]),
		ValidationSHA256: validation.ContentSHA256,
		Content: canonical,
		CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := ValidateNativeBundleArtifact(artifact); err != nil {
		return NativeBundleArtifact{}, err
	}
	return artifact, nil
}

func ValidateNativeBundleArtifact(artifact NativeBundleArtifact) error {
	if artifact.SchemaVersion != NativeBundleSchemaVersion || !artifactIDPattern.MatchString(artifact.ID) {
		return errors.New("native bundle schema version or identifier is invalid")
	}
	if !validNativeBundleRevisionStatus(artifact.Revision, artifact.Status) {
		return errors.New("native bundle status and revision are inconsistent")
	}
	if artifact.ModelCheckpointSHA256 != "" && !validNativeBundleSHA256(artifact.ModelCheckpointSHA256) {
		return errors.New("native bundle model checkpoint fingerprint is invalid")
	}
	if (artifact.Status == NativeBundleCandidate || artifact.Status == NativeBundleRejected) && artifact.ModelCheckpointSHA256 != "" {
		return errors.New("model checkpoint fingerprints may only be bound to approved bundles")
	}
	canonical, shape, err := validateNativeBundleContent(artifact.Content)
	if err != nil {
		return err
	}
	counts, err := nativeBundleCounts(shape)
	if err != nil {
		return err
	}
	if err := validateNativeBundleCounts(artifact, shape.Model, counts); err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	if artifact.ContentSHA256 != hex.EncodeToString(digest[:]) || !validNativeBundleSHA256(artifact.ValidationSHA256) {
		return errors.New("native bundle digest metadata does not match the stored content")
	}
	created, createdOK := parseTimestamp(artifact.CreatedAt)
	updated, updatedOK := parseTimestamp(artifact.UpdatedAt)
	if !createdOK || !updatedOK || updated.Before(created) {
		return errors.New("native bundle timestamps are invalid")
	}
	return nil
}

func SummarizeNativeBundle(artifact NativeBundleArtifact) NativeBundleSummary {
	return NativeBundleSummary{
		SchemaVersion: artifact.SchemaVersion,
		ID: artifact.ID, Revision: artifact.Revision, Status: artifact.Status,
		Model: artifact.Model, ModelCheckpointSHA256: artifact.ModelCheckpointSHA256,
		ArtifactCount: artifact.ArtifactCount,
		HeadCount: artifact.HeadCount,
		AdaptationCount: artifact.AdaptationCount,
		ObservationQuestionCount: artifact.ObservationQuestionCount,
		ObservationStateCount: artifact.ObservationStateCount,
		ContentSHA256: artifact.ContentSHA256,
		ValidationSHA256: artifact.ValidationSHA256,
		CreatedAt: artifact.CreatedAt,
		UpdatedAt: artifact.UpdatedAt,
	}
}

type nativeBundleCountSet struct {
	artifacts           int
	heads               int
	adaptation          int
	observationQuestions int
	observationStates   int
}

func validateNativeBundleContent(content []byte) (json.RawMessage, nativeBundleShape, error) {
	canonical, err := CanonicalJSON(content)
	if err != nil {
		return nil, nativeBundleShape{}, err
	}
	if privacy.ContainsCredential(canonical) {
		return nil, nativeBundleShape{}, errors.New("native bundle contains credential material")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil || fields == nil {
		return nil, nativeBundleShape{}, errors.New("native bundle must be an object")
	}
	for key := range fields {
		if key != "model" && key != "artifacts" && key != "heads" && key != "adaptation" && key != "observations" {
			return nil, nativeBundleShape{}, fmt.Errorf("native bundle contains unsupported field %q", key)
		}
	}
	if fields["model"] == nil || fields["artifacts"] == nil || fields["heads"] == nil {
		return nil, nativeBundleShape{}, errors.New("native bundle requires model, artifacts, and heads")
	}
	for _, key := range []string{"adaptation", "observations"} {
		if raw, exists := fields[key]; exists && string(raw) == "null" {
			return nil, nativeBundleShape{}, fmt.Errorf("native bundle %s must be an object when present", key)
		}
	}
	var shape nativeBundleShape
	if err := json.Unmarshal(canonical, &shape); err != nil || shape.Artifacts == nil || shape.Heads == nil {
		return nil, nativeBundleShape{}, errors.New("native bundle maps are invalid")
	}
	if shape.Adaptation == nil {
		shape.Adaptation = map[string]json.RawMessage{}
	}
	if shape.Observations == nil {
		shape.Observations = map[string]json.RawMessage{}
	}
	if strings.TrimSpace(shape.Model) == "" || len(shape.Model) > 512 || privacy.ContainsCredentialText(shape.Model) {
		return nil, nativeBundleShape{}, errors.New("native bundle model is invalid")
	}
	if len(shape.Artifacts) > 256 || len(shape.Heads) > 256 || len(shape.Adaptation) > 256 || len(shape.Observations) > 256 {
		return nil, nativeBundleShape{}, errors.New("native bundle contains too many entries")
	}
	return canonical, shape, nil
}

func nativeBundleCounts(shape nativeBundleShape) (nativeBundleCountSet, error) {
	counts := nativeBundleCountSet{
		artifacts: len(shape.Artifacts),
		heads: len(shape.Heads),
		adaptation: len(shape.Adaptation),
		observationQuestions: len(shape.Observations),
	}
	for key := range shape.Artifacts {
		if !questionKeyPattern.MatchString(key) {
			return nativeBundleCountSet{}, errors.New("native bundle calibration question key is invalid")
		}
	}
	for key := range shape.Heads {
		if !questionKeyPattern.MatchString(key) {
			return nativeBundleCountSet{}, errors.New("native bundle head question key is invalid")
		}
	}
	for key := range shape.Adaptation {
		if !questionKeyPattern.MatchString(key) {
			return nativeBundleCountSet{}, errors.New("native bundle adaptation question key is invalid")
		}
	}
	for key, raw := range shape.Observations {
		if !questionKeyPattern.MatchString(key) {
			return nativeBundleCountSet{}, errors.New("native bundle observation question key is invalid")
		}
		states, err := validateNativeBundleObservation(raw)
		if err != nil {
			return nativeBundleCountSet{}, err
		}
		counts.observationStates += states
		if counts.observationStates > 4096 {
			return nativeBundleCountSet{}, errors.New("native bundle contains too many labeled observations")
		}
	}
	return counts, nil
}

func validateNativeBundleObservation(raw []byte) (int, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return 0, errors.New("native bundle observation must be an object")
	}
	allowed := map[string]bool{
		"question_id": true, "kind": true, "text": true, "options": true,
		"states": true, "labels": true, "fits": true, "next_fit": true,
		"scale": true, "ordered": true, "centers": true,
	}
	for key := range fields {
		if !allowed[key] {
			return 0, fmt.Errorf("native bundle observation contains unsupported field %q", key)
		}
	}
	for _, key := range []string{"question_id", "kind", "text", "options", "states", "labels", "fits", "next_fit"} {
		if fields[key] == nil {
			return 0, fmt.Errorf("native bundle observation is missing %s", key)
		}
	}
	var observation nativeBundleObservation
	if err := json.Unmarshal(raw, &observation); err != nil {
		return 0, errors.New("native bundle observation fields are invalid")
	}
	if strings.TrimSpace(observation.QuestionID) == "" || len(observation.QuestionID) > 128 ||
		len(observation.Text) < 1 || len(observation.Text) > 10000 ||
		(observation.Kind != "choice" && observation.Kind != "score" && observation.Kind != "noul") ||
		len(observation.Options) < 2 || len(observation.Options) > 26 ||
		len(observation.States) < 1 || len(observation.States) > 4096 || len(observation.Labels) != len(observation.States) ||
		observation.Fits < 0 || observation.Fits > len(observation.States) || observation.NextFit < 1 || observation.NextFit > 100000 {
		return 0, errors.New("native bundle observation metadata or paired state/label arrays are invalid")
	}
	seen := make(map[string]bool, len(observation.Options))
	for _, option := range observation.Options {
		if strings.TrimSpace(option) == "" || seen[option] {
			return 0, errors.New("native bundle observation options must be unique non-empty strings")
		}
		seen[option] = true
	}
	if observation.Kind == "noul" && (len(observation.Options) != 2 || observation.Options[0] != "Yes" || observation.Options[1] != "No") {
		return 0, errors.New("native bundle no-uncertainty options must be Yes and No")
	}
	for _, label := range observation.Labels {
		if label < 0 || label >= len(observation.Options) {
			return 0, errors.New("native bundle observation label is outside its option layout")
		}
	}
	if fields["scale"] != nil {
		if len(observation.Scale) != 2 || math.IsNaN(observation.Scale[0]) || math.IsInf(observation.Scale[0], 0) ||
			math.IsNaN(observation.Scale[1]) || math.IsInf(observation.Scale[1], 0) || observation.Scale[1] <= observation.Scale[0] {
			return 0, errors.New("native bundle observation scale is invalid")
		}
	}
	if fields["ordered"] != nil && observation.Ordered == nil {
		return 0, errors.New("native bundle observation ordered flag is invalid")
	}
	if fields["centers"] != nil && string(fields["centers"]) != "null" {
		if len(observation.Centers) != len(observation.Options) {
			return 0, errors.New("native bundle observation centers do not match the option layout")
		}
		for index, center := range observation.Centers {
			if math.IsNaN(center) || math.IsInf(center, 0) || index > 0 && observation.Centers[index-1] >= center {
				return 0, errors.New("native bundle observation centers must be finite and strictly increasing")
			}
		}
	}
	return len(observation.States), nil
}

func validateNativeBundleReceipt(receipt NativeBundleValidation, model string, counts nativeBundleCountSet) error {
	if receipt.SchemaVersion != NativeBundleValidationSchemaVersion || receipt.Status != "valid" ||
		receipt.Model != model || receipt.ArtifactCount != counts.artifacts || receipt.HeadCount != counts.heads ||
		receipt.AdaptationCount != counts.adaptation || receipt.ObservationQuestionCount != counts.observationQuestions ||
		receipt.ObservationStateCount != counts.observationStates || !validNativeBundleSHA256(receipt.ContentSHA256) ||
		!receipt.HumanReviewRequired || receipt.ActivationAllowed || receipt.ExecutionAuthorized ||
		receipt.ModelEnvironmentVerified || receipt.CutoverEvidenceEligible {
		return errors.New("native bundle validation receipt does not match the submitted bundle or review-only contract")
	}
	return nil
}

func validateNativeBundleCounts(artifact NativeBundleArtifact, model string, counts nativeBundleCountSet) error {
	if artifact.Model != model || artifact.ArtifactCount != counts.artifacts || artifact.HeadCount != counts.heads ||
		artifact.AdaptationCount != counts.adaptation || artifact.ObservationQuestionCount != counts.observationQuestions ||
		artifact.ObservationStateCount != counts.observationStates {
		return errors.New("native bundle metadata does not match its content")
	}
	return nil
}

func validNativeBundleRevisionStatus(revision int, status NativeBundleStatus) bool {
	switch status {
	case NativeBundleCandidate:
		return revision == 1
	case NativeBundleActive, NativeBundleRejected:
		return revision == 2
	case NativeBundleRetired:
		return revision == 3
	default:
		return false
	}
}

func validNativeBundleSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
