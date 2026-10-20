package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevExitLatencyResultSchema = "threadforge.anyjev-exit-latency-result.v1"

type anyJevExitLatencyImport struct {
	SchemaVersion       string                       `json:"schema_version"`
	ModelSHA256         string                       `json:"model_sha256"`
	TransformerLayers   int                          `json:"transformer_layers"`
	HiddenSize          int                          `json:"hidden_size"`
	ParamsPerBlock      int                          `json:"params_per_block"`
	HostCondition       string                       `json:"host_condition"`
	Measurements        []anyJevExitLatencyRowImport `json:"measurements"`
	OmittedFields       []string                     `json:"omitted_fields"`
	HumanReviewRequired bool                         `json:"human_review_required"`
	ExecutionVerified   bool                         `json:"execution_verified"`
	CutoverEligible     bool                         `json:"cutover_evidence_eligible"`
}

type anyJevExitLatencyRowImport struct {
	StateTokens  int     `json:"state_tokens"`
	PromptTokens int     `json:"prompt_tokens"`
	Depth        float64 `json:"depth_fraction"`
	Blocks       int     `json:"blocks"`
	Mode         string  `json:"mode"`
	BatchMS      float64 `json:"batch_ms"`
	SingleMS     float64 `json:"single_ms"`
	GFLOPS       float64 `json:"gflops"`
}

type anyJevExitLatencyRowKey struct {
	stateTokens int
	depthBits   uint64
	mode        string
}

var anyJevExitLatencyRootFields = map[string]bool{
	"date": true, "env": true, "hidden_size": true, "model": true,
	"n_layers": true, "params_per_block": true, "rows": true,
}

var anyJevExitLatencyRowFields = map[string]bool{
	"batch_ms": true, "blocks": true, "depth": true, "gflops": true,
	"mode": true, "prompt_tokens": true, "single_ms": true, "state_tokens": true,
}

func isAnyJevExitLatencyJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	if !strings.HasPrefix(path, "bench/results_exit/") {
		return false
	}
	return strings.HasSuffix(path, ".latency.json") || strings.HasSuffix(path, ".latency_loaded.json")
}

func normalizeAnyJevExitLatencyResult(relative string, value any) (normalizedItem, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev exit-latency result shape")
	}
	for key := range object {
		if !anyJevExitLatencyRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev exit-latency result field %q", key)
		}
	}
	model, modelOK := boundedAnyJevBenchmarkString(object["model"], 2048)
	date, dateOK := boundedAnyJevBenchmarkString(object["date"], 64)
	if !modelOK || !dateOK || !validAnyJevLatencyTimestamp(date) {
		return normalizedItem{}, errors.New("AnyJev exit-latency model or timestamp is invalid")
	}
	env, envOK := object["env"].(map[string]any)
	if !envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev exit-latency environment metadata is invalid")
	}
	layers, layersOK := boundedAnyJevInteger(object["n_layers"], 1, 10000)
	hiddenSize, hiddenOK := boundedAnyJevInteger(object["hidden_size"], 1, 1e7)
	paramsPerBlock, paramsOK := boundedAnyJevInteger(object["params_per_block"], 1, 1e9)
	if !layersOK || !hiddenOK || !paramsOK {
		return normalizedItem{}, errors.New("AnyJev exit-latency model dimensions are invalid")
	}
	rawRows, ok := object["rows"].([]any)
	if !ok || len(rawRows) == 0 || len(rawRows) > 10000 {
		return normalizedItem{}, errors.New("AnyJev exit-latency rows are invalid")
	}
	condition := "quiet_gpu"
	if strings.HasSuffix(strings.ToLower(filepathSlash(relative)), ".latency_loaded.json") {
		condition = "contended_gpu"
	}
	payload := anyJevExitLatencyImport{
		SchemaVersion:     anyJevExitLatencyResultSchema,
		ModelSHA256:       anyJevStringSHA256(model),
		TransformerLayers: layers, HiddenSize: hiddenSize, ParamsPerBlock: paramsPerBlock,
		HostCondition:       condition,
		Measurements:        make([]anyJevExitLatencyRowImport, 0, len(rawRows)),
		OmittedFields:       []string{"model_label", "date", "environment_details"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	seen := make(map[anyJevExitLatencyRowKey]struct{}, len(rawRows))
	for _, raw := range rawRows {
		row, ok := raw.(map[string]any)
		if !ok || len(row) != len(anyJevExitLatencyRowFields) {
			return normalizedItem{}, errors.New("AnyJev exit-latency measurement shape is invalid")
		}
		for key := range row {
			if !anyJevExitLatencyRowFields[key] {
				return normalizedItem{}, fmt.Errorf("unsupported AnyJev exit-latency row field %q", key)
			}
		}
		stateTokens, stateOK := boundedAnyJevInteger(row["state_tokens"], 1, 1e7)
		promptTokens, promptOK := boundedAnyJevInteger(row["prompt_tokens"], 1, 1e7)
		blocks, blocksOK := boundedAnyJevInteger(row["blocks"], 1, layers)
		depth, depthOK := boundedAnyJevNumber(row["depth"], 0, 1)
		mode, modeOK := row["mode"].(string)
		if !stateOK || !promptOK || !blocksOK || !depthOK || depth == 0 || !modeOK {
			return normalizedItem{}, errors.New("AnyJev exit-latency dimensions are invalid")
		}
		modeValue := ""
		switch mode {
		case "raw (plain forward)":
			if blocks != layers || depth != 1 {
				return normalizedItem{}, errors.New("AnyJev raw exit-latency dimensions do not match full depth")
			}
			modeValue = "raw"
		case "truncated forward (head at this depth)":
			if math.Abs(float64(blocks)-depth*float64(layers)) > 0.500001 {
				return normalizedItem{}, errors.New("AnyJev truncated exit-latency depth and block count do not reconcile")
			}
			modeValue = "truncated_head"
		default:
			return normalizedItem{}, errors.New("AnyJev exit-latency mode is unsupported")
		}
		key := anyJevExitLatencyRowKey{stateTokens: stateTokens, depthBits: math.Float64bits(depth), mode: modeValue}
		if _, duplicate := seen[key]; duplicate {
			return normalizedItem{}, errors.New("AnyJev exit-latency result contains duplicate dimensions")
		}
		seen[key] = struct{}{}
		batchMS, batchOK := boundedAnyJevNumber(row["batch_ms"], 0, 1e9)
		singleMS, singleOK := boundedAnyJevNumber(row["single_ms"], 0, 1e9)
		gflops, gflopsOK := boundedAnyJevNumber(row["gflops"], 0, 1e9)
		if !batchOK || !singleOK || !gflopsOK || batchMS == 0 || singleMS == 0 || gflops == 0 {
			return normalizedItem{}, errors.New("AnyJev exit-latency measurement values are invalid")
		}
		expectedGFLOPS := 2 * float64(paramsPerBlock) * float64(promptTokens) * float64(blocks) / 1e9
		if math.Abs(gflops-expectedGFLOPS) > math.Max(1e-6, expectedGFLOPS*1e-6) {
			return normalizedItem{}, errors.New("AnyJev exit-latency FLOPs do not reconcile with model dimensions")
		}
		payload.Measurements = append(payload.Measurements, anyJevExitLatencyRowImport{
			StateTokens: stateTokens, PromptTokens: promptTokens, Depth: depth, Blocks: blocks,
			Mode: modeValue, BatchMS: batchMS, SingleMS: singleMS, GFLOPS: gflops,
		})
	}
	sort.Slice(payload.Measurements, func(i, j int) bool {
		a, b := payload.Measurements[i], payload.Measurements[j]
		if a.StateTokens != b.StateTokens {
			return a.StateTokens < b.StateTokens
		}
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		return a.Mode < b.Mode
	})
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev exit-latency projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}
