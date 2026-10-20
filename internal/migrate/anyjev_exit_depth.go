package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevExitDepthResultSchema = "threadforge.anyjev-exit-depth-result.v1"

type anyJevExitDepthImport struct {
	SchemaVersion       string                       `json:"schema_version"`
	ModelSHA256         string                       `json:"model_sha256"`
	Blocks              []anyJevExitDepthBlockImport `json:"blocks"`
	OmittedFields       []string                     `json:"omitted_fields"`
	HumanReviewRequired bool                         `json:"human_review_required"`
	ExecutionVerified   bool                         `json:"execution_verified"`
	CutoverEligible     bool                         `json:"cutover_evidence_eligible"`
}

type anyJevExitDepthBlockImport struct {
	Block           int                    `json:"block"`
	Lens            anyJevExitDepthMetrics `json:"lens"`
	LensTemperature anyJevExitDepthMetrics `json:"lens_temperature"`
	Head            anyJevExitDepthMetrics `json:"head"`
	HeadFlip        float64                `json:"head_flip"`
}

type anyJevExitDepthMetrics struct {
	Accuracy float64 `json:"accuracy"`
	ECE      float64 `json:"ece"`
	NLL      float64 `json:"nll"`
}

var anyJevExitDepthRootFields = map[string]bool{
	"date": true, "env": true, "layers": true, "model": true, "per_layer": true,
}

var anyJevExitDepthMetricFields = map[string]bool{
	"acc": true, "ece": true, "nll": true,
}

var anyJevExitDepthLayerFields = map[string]bool{
	"head": true, "head_flip": true, "lens": true, "lens+T": true,
}

func isAnyJevExitDepthJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	return strings.HasPrefix(path, "bench/results_exit/") && strings.HasSuffix(path, ".depth.json")
}

func normalizeAnyJevExitDepthResult(value any) (normalizedItem, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev exit-depth result shape")
	}
	for key := range object {
		if !anyJevExitDepthRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev exit-depth result field %q", key)
		}
	}
	model, modelOK := boundedAnyJevBenchmarkString(object["model"], 2048)
	date, dateOK := boundedAnyJevBenchmarkString(object["date"], 64)
	if !modelOK || !dateOK || !validAnyJevLatencyTimestamp(date) {
		return normalizedItem{}, errors.New("AnyJev exit-depth model or timestamp is invalid")
	}
	env, envOK := object["env"].(map[string]any)
	if !envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev exit-depth environment metadata is invalid")
	}
	rawLayers, ok := object["layers"].([]any)
	if !ok || len(rawLayers) == 0 || len(rawLayers) > 1024 {
		return normalizedItem{}, errors.New("AnyJev exit-depth layer list is invalid")
	}
	perLayer, ok := object["per_layer"].(map[string]any)
	if !ok || len(perLayer) != len(rawLayers) {
		return normalizedItem{}, errors.New("AnyJev exit-depth per-layer data does not match its layer list")
	}
	blocks := make([]int, 0, len(rawLayers))
	seen := make(map[int]struct{}, len(rawLayers))
	for _, raw := range rawLayers {
		block, ok := boundedAnyJevInteger(raw, 1, 100000)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev exit-depth block index is invalid")
		}
		if _, exists := seen[block]; exists {
			return normalizedItem{}, errors.New("AnyJev exit-depth layer list contains duplicates")
		}
		seen[block] = struct{}{}
		blocks = append(blocks, block)
	}
	sort.Ints(blocks)
	projected := anyJevExitDepthImport{
		SchemaVersion:       anyJevExitDepthResultSchema,
		ModelSHA256:         anyJevStringSHA256(model),
		Blocks:              make([]anyJevExitDepthBlockImport, 0, len(blocks)),
		OmittedFields:       []string{"model_label", "date", "environment_details"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	for key := range perLayer {
		block, err := strconv.Atoi(key)
		if err != nil || strconv.Itoa(block) != key {
			return normalizedItem{}, errors.New("AnyJev exit-depth block map key is invalid")
		}
		if _, exists := seen[block]; !exists {
			return normalizedItem{}, errors.New("AnyJev exit-depth map contains a block absent from its layer list")
		}
	}
	for _, block := range blocks {
		raw, exists := perLayer[strconv.Itoa(block)]
		if !exists {
			return normalizedItem{}, errors.New("AnyJev exit-depth layer list omits its per-layer metrics")
		}
		layer, ok := raw.(map[string]any)
		if !ok || len(layer) != len(anyJevExitDepthLayerFields) {
			return normalizedItem{}, errors.New("AnyJev exit-depth layer metric shape is invalid")
		}
		for key := range layer {
			if !anyJevExitDepthLayerFields[key] {
				return normalizedItem{}, fmt.Errorf("unsupported AnyJev exit-depth metric family %q", key)
			}
		}
		lens, err := normalizeAnyJevExitDepthMetrics(layer["lens"])
		if err != nil {
			return normalizedItem{}, fmt.Errorf("AnyJev exit-depth lens metrics: %w", err)
		}
		lensTemperature, err := normalizeAnyJevExitDepthMetrics(layer["lens+T"])
		if err != nil {
			return normalizedItem{}, fmt.Errorf("AnyJev exit-depth temperature-scaled metrics: %w", err)
		}
		head, err := normalizeAnyJevExitDepthMetrics(layer["head"])
		if err != nil {
			return normalizedItem{}, fmt.Errorf("AnyJev exit-depth head metrics: %w", err)
		}
		headFlip, ok := boundedAnyJevNumber(layer["head_flip"], 0, 1)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev exit-depth head-flip rate is invalid")
		}
		projected.Blocks = append(projected.Blocks, anyJevExitDepthBlockImport{
			Block: block, Lens: lens, LensTemperature: lensTemperature, Head: head, HeadFlip: headFlip,
		})
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev exit-depth projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevExitDepthMetrics(value any) (anyJevExitDepthMetrics, error) {
	metrics, ok := value.(map[string]any)
	if !ok || len(metrics) != len(anyJevExitDepthMetricFields) {
		return anyJevExitDepthMetrics{}, errors.New("metric object is incomplete")
	}
	for key := range metrics {
		if !anyJevExitDepthMetricFields[key] {
			return anyJevExitDepthMetrics{}, fmt.Errorf("unsupported metric %q", key)
		}
	}
	accuracy, accuracyOK := boundedAnyJevNumber(metrics["acc"], 0, 1)
	ece, eceOK := boundedAnyJevNumber(metrics["ece"], 0, 1)
	nll, nllOK := boundedAnyJevNumber(metrics["nll"], 0, 1e6)
	if !accuracyOK || !eceOK || !nllOK {
		return anyJevExitDepthMetrics{}, errors.New("metric values are out of range")
	}
	return anyJevExitDepthMetrics{Accuracy: accuracy, ECE: ece, NLL: nll}, nil
}
