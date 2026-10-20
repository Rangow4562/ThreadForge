package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevLatencyBenchmarkResultSchema = "threadforge.anyjev-latency-benchmark-result.v1"

type anyJevLatencyBenchmarkImport struct {
	SchemaVersion           string                            `json:"schema_version"`
	ModelSHA256             string                            `json:"model_sha256"`
	Backend                 string                            `json:"backend"`
	Rows                    []anyJevLatencyBenchmarkRowImport `json:"rows"`
	OmittedFields           []string                          `json:"omitted_fields"`
	HumanReviewRequired     bool                              `json:"human_review_required"`
	ExecutionVerified       bool                              `json:"execution_verified"`
	CutoverEvidenceEligible bool                              `json:"cutover_evidence_eligible"`
}

type anyJevLatencyBenchmarkRowImport struct {
	StateTokens      int      `json:"state_tokens"`
	OptionCount      int      `json:"option_count"`
	BatchStates      int      `json:"batch_states"`
	BatchRawMS       float64  `json:"batch_raw_ms"`
	BatchL0FullMS    float64  `json:"batch_l0_full_ms"`
	BatchL0SharedMS  *float64 `json:"batch_l0_shared_ms,omitempty"`
	SingleRawMS      float64  `json:"single_raw_ms"`
	SingleL0FullMS   float64  `json:"single_l0_full_ms"`
	SingleL0SharedMS *float64 `json:"single_l0_shared_ms,omitempty"`
}

var anyJevLatencyRootFields = map[string]bool{
	"backend": true, "date": true, "model": true, "rows": true,
}

var anyJevLatencyRowFields = map[string]bool{
	"backend": true, "batch_L0_full_ms": true, "batch_L0_shared_ms": true,
	"batch_raw_ms": true, "k": true, "n": true, "shared_fallbacks": true,
	"single_L0_full_ms": true, "single_L0_shared_ms": true, "single_raw_ms": true,
	"state_tokens": true,
}

func isAnyJevLatencyBenchmarkJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	if !strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".items.json") {
		return false
	}
	for _, family := range []string{
		"bench/results_latency/",
		"bench/results_latency_vllm_cache_on/",
		"bench/results_latency_vllm_cache_off/",
	} {
		if strings.HasPrefix(path, family) {
			return true
		}
	}
	return false
}

func normalizeAnyJevLatencyBenchmarkResult(value any) (normalizedItem, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev latency benchmark result shape")
	}
	for key := range object {
		if !anyJevLatencyRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev latency benchmark result field %q", key)
		}
	}
	model, modelOK := boundedAnyJevBenchmarkString(object["model"], 2048)
	backend, backendOK := object["backend"].(string)
	if !modelOK || !backendOK || !oneOf(backend, "hf", "vllm") {
		return normalizedItem{}, errors.New("AnyJev latency benchmark model or backend is invalid")
	}
	date, dateOK := boundedAnyJevBenchmarkString(object["date"], 64)
	if !dateOK || !validAnyJevLatencyTimestamp(date) {
		return normalizedItem{}, errors.New("AnyJev latency benchmark timestamp is invalid")
	}

	rawRows, ok := object["rows"].([]any)
	if !ok || len(rawRows) == 0 || len(rawRows) > 10000 {
		return normalizedItem{}, errors.New("AnyJev latency benchmark rows are invalid")
	}
	payload := anyJevLatencyBenchmarkImport{
		SchemaVersion:       anyJevLatencyBenchmarkResultSchema,
		ModelSHA256:         anyJevStringSHA256(model),
		Backend:             backend,
		Rows:                make([]anyJevLatencyBenchmarkRowImport, 0, len(rawRows)),
		OmittedFields:       []string{"model_label", "date", "row_backend", "shared_fallbacks_counter"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEvidenceEligible: false,
	}
	seenRows := make(map[[2]int]struct{}, len(rawRows))
	batchStates := 0
	for _, raw := range rawRows {
		row, ok := raw.(map[string]any)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev latency benchmark row is invalid")
		}
		for key := range row {
			if !anyJevLatencyRowFields[key] {
				return normalizedItem{}, fmt.Errorf("unsupported AnyJev latency benchmark row field %q", key)
			}
		}
		rowBackend, ok := row["backend"].(string)
		if !ok || rowBackend != backend {
			return normalizedItem{}, errors.New("AnyJev latency row backend does not match its result")
		}
		stateTokens, stateOK := boundedAnyJevInteger(row["state_tokens"], 1, 1e7)
		optionCount, optionOK := boundedAnyJevInteger(row["k"], 2, 4096)
		states, statesOK := boundedAnyJevInteger(row["n"], 1, 1e6)
		if !stateOK || !optionOK || !statesOK {
			return normalizedItem{}, errors.New("AnyJev latency benchmark dimensions are invalid")
		}
		if batchStates == 0 {
			batchStates = states
		} else if batchStates != states {
			return normalizedItem{}, errors.New("AnyJev latency rows disagree on batch size")
		}
		key := [2]int{stateTokens, optionCount}
		if _, duplicate := seenRows[key]; duplicate {
			return normalizedItem{}, errors.New("AnyJev latency benchmark contains duplicate state/option rows")
		}
		seenRows[key] = struct{}{}

		batchRaw, batchRawOK := boundedAnyJevNumber(row["batch_raw_ms"], 0, 1e9)
		batchFull, batchFullOK := boundedAnyJevNumber(row["batch_L0_full_ms"], 0, 1e9)
		singleRaw, singleRawOK := boundedAnyJevNumber(row["single_raw_ms"], 0, 1e9)
		singleFull, singleFullOK := boundedAnyJevNumber(row["single_L0_full_ms"], 0, 1e9)
		if !batchRawOK || !batchFullOK || !singleRawOK || !singleFullOK ||
			batchRaw == 0 || batchFull == 0 || singleRaw == 0 || singleFull == 0 {
			return normalizedItem{}, errors.New("AnyJev latency benchmark timings are invalid")
		}
		projected := anyJevLatencyBenchmarkRowImport{
			StateTokens: stateTokens, OptionCount: optionCount, BatchStates: states,
			BatchRawMS: batchRaw, BatchL0FullMS: batchFull,
			SingleRawMS: singleRaw, SingleL0FullMS: singleFull,
		}
		sharedBatch, hasSharedBatch := row["batch_L0_shared_ms"]
		sharedSingle, hasSharedSingle := row["single_L0_shared_ms"]
		if hasSharedBatch != hasSharedSingle {
			return normalizedItem{}, errors.New("AnyJev latency shared-prefix measurements must be paired")
		}
		if hasSharedBatch {
			batchValue, batchOK := boundedAnyJevNumber(sharedBatch, 0, 1e9)
			singleValue, singleOK := boundedAnyJevNumber(sharedSingle, 0, 1e9)
			if !batchOK || !singleOK || batchValue == 0 || singleValue == 0 {
				return normalizedItem{}, errors.New("AnyJev shared-prefix latency measurements are invalid")
			}
			projected.BatchL0SharedMS = &batchValue
			projected.SingleL0SharedMS = &singleValue
		}
		if fallback, exists := row["shared_fallbacks"]; exists {
			if !hasSharedBatch {
				return normalizedItem{}, errors.New("AnyJev shared-prefix fallback counter lacks shared measurements")
			}
			if _, ok := boundedAnyJevInteger(fallback, 0, 1e9); !ok {
				return normalizedItem{}, errors.New("AnyJev shared-prefix fallback counter is invalid")
			}
		}
		payload.Rows = append(payload.Rows, projected)
	}
	sort.Slice(payload.Rows, func(i, j int) bool {
		if payload.Rows[i].StateTokens != payload.Rows[j].StateTokens {
			return payload.Rows[i].StateTokens < payload.Rows[j].StateTokens
		}
		return payload.Rows[i].OptionCount < payload.Rows[j].OptionCount
	})
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev latency benchmark projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func validAnyJevLatencyTimestamp(value string) bool {
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return true
	}
	_, err := time.Parse("2006-01-02T15:04:05.999999999", value)
	return err == nil
}
