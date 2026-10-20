package decision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const ObservationCheckpointSchemaVersion = "threadforge.anyjev-observation-checkpoint.v1"
const MaxObservationCheckpointBytes = 8 << 20
const MaxObservationCheckpointStates = 4096

var observationStreamIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

// ObservationCheckpoint is a private, revisioned snapshot of AnyJev's labeled
// observations. Content contains raw local states and must never be exported to
// project/team scopes or printed by the CLI.
type ObservationCheckpoint struct {
	StreamID         string          `json:"stream_id"`
	Revision         int             `json:"revision"`
	Model            string          `json:"model"`
	QuestionKey      string          `json:"question_key"`
	ObservationCount int             `json:"observation_count"`
	TotalFits        int             `json:"total_fits"`
	ContentSHA256    string          `json:"content_sha256"`
	Content          json.RawMessage `json:"content"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
}

type anyJevObservationCheckpoint struct {
	SchemaVersion string                       `json:"schema_version"`
	Model         string                       `json:"model"`
	QuestionKey   string                       `json:"question_key"`
	FitAt         int                          `json:"fit_at"`
	RefitFactor   float64                      `json:"refit_factor"`
	FitOptions    anyJevObservationFitOptions `json:"fit_options"`
	Observation   anyJevObservationRecord     `json:"observation"`
}

type anyJevObservationFitOptions struct {
	Layers  []int    `json:"layers"`
	Kinds   []string `json:"kinds"`
	NFolds  int      `json:"n_folds"`
	Seed    uint64   `json:"seed"`
	Listing string   `json:"listing"`
}

type anyJevObservationRecord struct {
	QuestionID string            `json:"question_id"`
	Kind       string            `json:"kind"`
	Text       string            `json:"text"`
	Options    []string          `json:"options"`
	Scale      []float64         `json:"scale"`
	Ordered    bool              `json:"ordered"`
	Centers    []float64         `json:"centers"`
	States     []json.RawMessage `json:"states"`
	Labels     []int             `json:"labels"`
	Fits       int               `json:"fits"`
	NextFit    int               `json:"next_fit"`
}

func NewObservationCheckpoint(streamID string, revision int, content json.RawMessage, at time.Time) (ObservationCheckpoint, error) {
	if !observationStreamIDPattern.MatchString(streamID) {
		return ObservationCheckpoint{}, errors.New("observation stream ID must be a lowercase stable identifier of at most 48 characters")
	}
	if revision < 1 || revision > MaxObservationCheckpointStates {
		return ObservationCheckpoint{}, errors.New("observation checkpoint revision must be between 1 and 4096")
	}
	payload, err := validateObservationCheckpointContent(content)
	if err != nil {
		return ObservationCheckpoint{}, err
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	hash := sha256.Sum256(content)
	checkpoint := ObservationCheckpoint{
		StreamID: streamID, Revision: revision, Model: payload.Model, QuestionKey: payload.QuestionKey,
		ObservationCount: len(payload.Observation.States), TotalFits: payload.Observation.Fits,
		ContentSHA256: hex.EncodeToString(hash[:]), Content: bytes.Clone(content), CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := ValidateObservationCheckpoint(checkpoint); err != nil {
		return ObservationCheckpoint{}, err
	}
	return checkpoint, nil
}

func ValidateObservationCheckpoint(checkpoint ObservationCheckpoint) error {
	var problems []string
	if !observationStreamIDPattern.MatchString(checkpoint.StreamID) {
		problems = append(problems, "stream_id must be a lowercase stable identifier")
	}
	if checkpoint.Revision < 1 || checkpoint.Revision > MaxObservationCheckpointStates {
		problems = append(problems, "revision must be between 1 and 4096")
	}
	if len(checkpoint.Content) == 0 || len(checkpoint.Content) > MaxObservationCheckpointBytes || !json.Valid(checkpoint.Content) {
		problems = append(problems, "checkpoint content must be valid JSON no larger than 8 MiB")
	} else {
		payload, err := validateObservationCheckpointContent(checkpoint.Content)
		if err != nil {
			problems = append(problems, "content: "+err.Error())
		} else {
			if checkpoint.Model != payload.Model || checkpoint.QuestionKey != payload.QuestionKey {
				problems = append(problems, "outer model/question binding does not match content")
			}
			if checkpoint.ObservationCount != len(payload.Observation.States) || checkpoint.TotalFits != payload.Observation.Fits {
				problems = append(problems, "outer counters do not match content")
			}
		}
		hash := sha256.Sum256(checkpoint.Content)
		if checkpoint.ContentSHA256 != hex.EncodeToString(hash[:]) {
			problems = append(problems, "content_sha256 does not match checkpoint content")
		}
		if privacy.ContainsCredential(checkpoint.Content) {
			problems = append(problems, "checkpoint content contains credential material")
		}
	}
	created, createdOK := parseTimestamp(checkpoint.CreatedAt)
	updated, updatedOK := parseTimestamp(checkpoint.UpdatedAt)
	if !createdOK || !updatedOK {
		problems = append(problems, "created_at and updated_at must use RFC3339")
	} else if updated.Before(created) {
		problems = append(problems, "updated_at cannot precede created_at")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// ValidateObservationCheckpointAppend ensures a new revision strictly extends
// the previous labeled history without changing its model, question, fit
// schedule, solver configuration, or already-recorded state/label pairs.
func ValidateObservationCheckpointAppend(previous, next ObservationCheckpoint) error {
	if err := ValidateObservationCheckpoint(previous); err != nil {
		return fmt.Errorf("previous checkpoint is invalid: %w", err)
	}
	if err := ValidateObservationCheckpoint(next); err != nil {
		return fmt.Errorf("next checkpoint is invalid: %w", err)
	}
	oldPayload, err := validateObservationCheckpointContent(previous.Content)
	if err != nil {
		return err
	}
	newPayload, err := validateObservationCheckpointContent(next.Content)
	if err != nil {
		return err
	}
	if previous.StreamID != next.StreamID || previous.Model != next.Model || previous.QuestionKey != next.QuestionKey ||
		oldPayload.FitAt != newPayload.FitAt || oldPayload.RefitFactor != newPayload.RefitFactor ||
		!reflect.DeepEqual(oldPayload.FitOptions, newPayload.FitOptions) ||
		oldPayload.Observation.QuestionID != newPayload.Observation.QuestionID ||
		oldPayload.Observation.Kind != newPayload.Observation.Kind ||
		oldPayload.Observation.Text != newPayload.Observation.Text ||
		oldPayload.Observation.Ordered != newPayload.Observation.Ordered ||
		!reflect.DeepEqual(oldPayload.Observation.Options, newPayload.Observation.Options) ||
		!reflect.DeepEqual(oldPayload.Observation.Scale, newPayload.Observation.Scale) ||
		!reflect.DeepEqual(oldPayload.Observation.Centers, newPayload.Observation.Centers) {
		return errors.New("observation checkpoint binding or fitting configuration changed")
	}
	addedStates := len(newPayload.Observation.States) - len(oldPayload.Observation.States)
	newFits := newPayload.Observation.Fits - oldPayload.Observation.Fits
	if addedStates < 1 || newFits < 0 || newFits > addedStates {
		return errors.New("observation checkpoint must add states and cannot decrease its fit count")
	}
	for index := range oldPayload.Observation.States {
		if !equalJSON(oldPayload.Observation.States[index], newPayload.Observation.States[index]) ||
			oldPayload.Observation.Labels[index] != newPayload.Observation.Labels[index] {
			return fmt.Errorf("observation checkpoint rewrote existing sample %d", index)
		}
	}
	return nil
}

func validateObservationCheckpointContent(content []byte) (anyJevObservationCheckpoint, error) {
	if len(content) == 0 || len(content) > MaxObservationCheckpointBytes || !json.Valid(content) {
		return anyJevObservationCheckpoint{}, errors.New("content must be valid JSON no larger than 8 MiB")
	}
	if privacy.ContainsCredential(content) {
		return anyJevObservationCheckpoint{}, errors.New("content contains credential material")
	}
	if err := requireObjectFields(content,
		"schema_version", "model", "question_key", "fit_at", "refit_factor", "fit_options", "observation"); err != nil {
		return anyJevObservationCheckpoint{}, fmt.Errorf("checkpoint fields are invalid: %w", err)
	}
	var objects map[string]json.RawMessage
	if err := json.Unmarshal(content, &objects); err != nil {
		return anyJevObservationCheckpoint{}, errors.New("checkpoint must be a JSON object")
	}
	if err := requireObjectFields(objects["fit_options"], "layers", "kinds", "n_folds", "seed", "listing"); err != nil {
		return anyJevObservationCheckpoint{}, fmt.Errorf("fit_options fields are invalid: %w", err)
	}
	if err := requireObjectFields(objects["observation"],
		"question_id", "kind", "text", "options", "scale", "ordered", "centers", "states", "labels", "fits", "next_fit"); err != nil {
		return anyJevObservationCheckpoint{}, fmt.Errorf("observation fields are invalid: %w", err)
	}
	var payload anyJevObservationCheckpoint
	if err := decodeStrictJSON(content, &payload); err != nil {
		return anyJevObservationCheckpoint{}, fmt.Errorf("decode checkpoint: %w", err)
	}
	if payload.SchemaVersion != ObservationCheckpointSchemaVersion {
		return anyJevObservationCheckpoint{}, errors.New("unsupported checkpoint schema_version")
	}
	if strings.TrimSpace(payload.Model) == "" || len(payload.Model) > 4096 || privacy.ContainsCredentialText(payload.Model) {
		return anyJevObservationCheckpoint{}, errors.New("model must be bounded and credential-free")
	}
	if !questionKeyPattern.MatchString(payload.QuestionKey) {
		return anyJevObservationCheckpoint{}, errors.New("question_key must be a 16-character lowercase hex digest")
	}
	if payload.FitAt < 1 || payload.FitAt > 10000 || math.IsNaN(payload.RefitFactor) || math.IsInf(payload.RefitFactor, 0) ||
		payload.RefitFactor <= 1 || payload.RefitFactor > 10 {
		return anyJevObservationCheckpoint{}, errors.New("fit schedule is outside its bounds")
	}
	if err := validateObservationFitOptions(payload.FitOptions); err != nil {
		return anyJevObservationCheckpoint{}, err
	}
	observation := payload.Observation
	if observation.QuestionID == "" || len(observation.QuestionID) > 128 || len(observation.Text) == 0 || len(observation.Text) > 10000 {
		return anyJevObservationCheckpoint{}, errors.New("observation question metadata is invalid")
	}
	if observation.Kind != "choice" && observation.Kind != "score" && observation.Kind != "noul" {
		return anyJevObservationCheckpoint{}, errors.New("observation question kind is unsupported")
	}
	if len(observation.Options) < 2 || len(observation.Options) > 26 || len(observation.Scale) != 2 ||
		math.IsNaN(observation.Scale[0]) || math.IsNaN(observation.Scale[1]) || math.IsInf(observation.Scale[0], 0) ||
		math.IsInf(observation.Scale[1], 0) || observation.Scale[1] <= observation.Scale[0] {
		return anyJevObservationCheckpoint{}, errors.New("observation option or scale metadata is invalid")
	}
	if (observation.Kind == "score") != observation.Ordered {
		return anyJevObservationCheckpoint{}, errors.New("observation ordering flag does not match its question kind")
	}
	seenOptions := make(map[string]struct{}, len(observation.Options))
	for _, option := range observation.Options {
		if strings.TrimSpace(option) == "" {
			return anyJevObservationCheckpoint{}, errors.New("observation options must be non-empty")
		}
		if _, exists := seenOptions[option]; exists {
			return anyJevObservationCheckpoint{}, errors.New("observation options must be unique")
		}
		seenOptions[option] = struct{}{}
	}
	if observation.Kind == "noul" && (len(observation.Options) != 2 || observation.Options[0] != "Yes" || observation.Options[1] != "No") {
		return anyJevObservationCheckpoint{}, errors.New("noul observations must use the AnyJev Yes/No option layout")
	}
	if observation.Centers != nil {
		if len(observation.Centers) != len(observation.Options) {
			return anyJevObservationCheckpoint{}, errors.New("score centers must match the option count")
		}
		for index, center := range observation.Centers {
			if math.IsNaN(center) || math.IsInf(center, 0) || (index > 0 && observation.Centers[index-1] >= center) {
				return anyJevObservationCheckpoint{}, errors.New("score centers must be finite and strictly increasing")
			}
		}
	}
	count := len(observation.States)
	if count < 1 || count > MaxObservationCheckpointStates || len(observation.Labels) != count ||
		observation.Fits < 0 || observation.Fits > count || observation.NextFit < 1 || observation.NextFit > 100000 {
		return anyJevObservationCheckpoint{}, errors.New("observation state, labels, or fit counters are invalid")
	}
	for _, label := range observation.Labels {
		if label < 0 || label >= len(observation.Options) {
			return anyJevObservationCheckpoint{}, errors.New("observation label is outside the option range")
		}
	}
	return payload, nil
}

func validateObservationFitOptions(options anyJevObservationFitOptions) error {
	if options.Layers != nil {
		if len(options.Layers) < 1 || len(options.Layers) > 16 {
			return errors.New("fit_options.layers must contain 1 to 16 indexes")
		}
		for _, layer := range options.Layers {
			if layer == 0 || layer < -1024 || layer > 1024 {
				return errors.New("fit_options.layers contains an unsupported index")
			}
		}
	}
	if len(options.Kinds) < 1 || len(options.Kinds) > 4 {
		return errors.New("fit_options.kinds must contain supported head kinds")
	}
	seenKinds := make(map[string]struct{}, len(options.Kinds))
	for _, kind := range options.Kinds {
		if kind != "diff_means" && kind != "lda" && kind != "ridge" && kind != "rrr" {
			return errors.New("fit_options.kinds contains an unsupported method")
		}
		if _, exists := seenKinds[kind]; exists {
			return errors.New("fit_options.kinds contains a duplicate method")
		}
		seenKinds[kind] = struct{}{}
	}
	if options.NFolds < 2 || options.NFolds > 10 || options.Seed > 0xFFFFFFFF {
		return errors.New("fit_options fold count or seed is outside its bounds")
	}
	if options.Listing != "auto" && options.Listing != "random" && options.Listing != "canonical" {
		return errors.New("fit_options.listing is unsupported")
	}
	return nil
}

func requireObjectFields(payload []byte, expected ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return errors.New("expected a JSON object")
	}
	wanted := make(map[string]struct{}, len(expected))
	for _, field := range expected {
		wanted[field] = struct{}{}
		if _, ok := object[field]; !ok {
			return fmt.Errorf("missing field %q", field)
		}
	}
	for field := range object {
		if _, ok := wanted[field]; !ok {
			return fmt.Errorf("unknown field %q", field)
		}
	}
	return nil
}

func decodeStrictJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func equalJSON(left, right []byte) bool {
	decode := func(payload []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	a, errA := decode(left)
	b, errB := decode(right)
	return errA == nil && errB == nil && reflect.DeepEqual(a, b)
}
