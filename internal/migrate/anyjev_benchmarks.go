package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevBenchmarkResultSchema = "threadforge.anyjev-benchmark-result.v1"

type anyJevBenchmarkResultImport struct {
	SchemaVersion           string                      `json:"schema_version"`
	ModelSHA256             string                      `json:"model_sha256"`
	Backend                 string                      `json:"backend"`
	Prior                   string                      `json:"prior"`
	Combine                 string                      `json:"combine"`
	TestExamples            int                         `json:"test_examples"`
	CalibrationExamples     int                         `json:"calibration_examples"`
	Seed                    int                         `json:"seed"`
	Adaptive                *bool                       `json:"adaptive,omitempty"`
	AdaptiveMargin          *float64                    `json:"adaptive_margin,omitempty"`
	MaxPermutations         *int                        `json:"max_permutations,omitempty"`
	PriorStrength           *float64                    `json:"prior_strength,omitempty"`
	Tasks                   []anyJevBenchmarkTaskImport `json:"tasks"`
	OmittedFields           []string                    `json:"omitted_fields"`
	HumanReviewRequired     bool                        `json:"human_review_required"`
	ExecutionVerified       bool                        `json:"execution_verified"`
	CutoverEvidenceEligible bool                        `json:"cutover_evidence_eligible"`
}

type anyJevBenchmarkTaskImport struct {
	TaskKeySHA256       string                       `json:"task_key_sha256"`
	QuestionKeySHA256   string                       `json:"question_key_sha256"`
	K                   int                          `json:"k"`
	TestExamples        int                          `json:"test_examples"`
	CalibrationExamples int                          `json:"calibration_examples"`
	Levels              []anyJevBenchmarkLevelImport `json:"levels"`
}

type anyJevBenchmarkLevelImport struct {
	Name         string             `json:"name"`
	Metrics      map[string]float64 `json:"metrics"`
	MethodSHA256 map[string]string  `json:"method_sha256,omitempty"`
	Flags        map[string]bool    `json:"flags,omitempty"`
}

var anyJevBenchmarkRootFields = map[string]bool{
	"adaptive": true, "adaptive_margin": true, "backend": true, "calib": true,
	"combine": true, "env": true, "max_permutations": true, "model": true,
	"n": true, "prior": true, "prior_strength": true, "seed": true, "tasks": true,
}

var anyJevBenchmarkTaskFields = map[string]bool{
	"answer_mass": true, "artifact": true, "k": true, "levels": true,
	"n_calib": true, "n_test": true, "question": true,
	"seconds_l0_pass": true, "seconds_per_decision_l0": true, "task": true,
}

var anyJevBenchmarkMetricBounds = map[string][2]float64{
	"acc": {0, 1}, "aurc": {0, 1}, "brier": {0, 2}, "cov@5%": {0, 1},
	"cyclic_flip_l0": {0, 1}, "cyclic_flip_raw": {0, 1}, "ece": {0, 1},
	"flip": {0, 1}, "macro_f1": {0, 1}, "mean_shifts": {0, 1e6},
	"n": {1, 1e7}, "nll": {0, 1e6}, "prior_strength": {0, 1e6},
	"temperature": {0, 1e6}, "temperature_reversed": {0, 1e6},
}

var anyJevBenchmarkLevels = map[string]bool{
	"raw": true, "L0": true, "L0-bc": true, "L0-cf": true,
	"L0-perm": true, "L0-perm+bc": true, "L0-perm+cf": true, "L1": true,
}

func isAnyJevBenchmarkJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	if !strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".items.json") {
		return false
	}
	if strings.HasPrefix(path, "bench/results/") {
		return true
	}
	if !strings.HasPrefix(path, "bench/results_") {
		return false
	}
	family := strings.SplitN(strings.TrimPrefix(path, "bench/results_"), "/", 2)[0]
	return strings.HasPrefix(family, "adaptive_") || oneOf(family, "batchprior_v0", "dump", "small", "v01")
}

func filepathSlash(value string) string { return strings.ReplaceAll(value, "\\", "/") }

func normalizeAnyJevBenchmarkResult(value any) (normalizedItem, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev benchmark result shape")
	}
	for key := range object {
		if !anyJevBenchmarkRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev benchmark result field %q", key)
		}
	}
	model, ok := boundedAnyJevBenchmarkString(object["model"], 2048)
	if !ok {
		return normalizedItem{}, errors.New("AnyJev benchmark result model identifier is invalid")
	}
	backend, backendOK := object["backend"].(string)
	prior, priorOK := object["prior"].(string)
	combine, combineOK := object["combine"].(string)
	if !backendOK || backend != "hf" || !priorOK || !oneOf(prior, "batch", "content_free", "none") ||
		!combineOK || !oneOf(combine, "logmean", "mean") {
		return normalizedItem{}, errors.New("AnyJev benchmark result has an unsupported runtime mode")
	}
	testExamples, testOK := boundedAnyJevInteger(object["n"], 1, 1e7)
	calibrationExamples, calibrationOK := boundedAnyJevInteger(object["calib"], 0, 1e7)
	seed, seedOK := boundedAnyJevInteger(object["seed"], -1e9, 1e9)
	if !testOK || !calibrationOK || !seedOK {
		return normalizedItem{}, errors.New("AnyJev benchmark run counts or seed are invalid")
	}
	if env, exists := object["env"]; exists {
		if envMap, ok := env.(map[string]any); !ok || len(envMap) > 256 {
			return normalizedItem{}, errors.New("AnyJev benchmark environment metadata is invalid")
		}
	}

	payload := anyJevBenchmarkResultImport{
		SchemaVersion: anyJevBenchmarkResultSchema,
		ModelSHA256:   anyJevStringSHA256(model), Backend: backend, Prior: prior, Combine: combine,
		TestExamples: testExamples, CalibrationExamples: calibrationExamples, Seed: seed,
		OmittedFields:       []string{"model_label", "environment_details", "task_labels", "question_labels", "artifacts", "answer_mass", "raw_item_distributions", "timing_metrics"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEvidenceEligible: false,
	}
	if raw, exists := object["adaptive"]; exists {
		adaptive, ok := raw.(bool)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev benchmark adaptive flag is invalid")
		}
		payload.Adaptive = &adaptive
	}
	if margin, exists := object["adaptive_margin"]; exists && margin != nil {
		value, ok := boundedAnyJevNumber(margin, 0, 1e6)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev benchmark adaptive margin is invalid")
		}
		payload.AdaptiveMargin = &value
	}
	if strength, exists := object["prior_strength"]; exists && strength != nil {
		value, ok := boundedAnyJevNumber(strength, 0, 1e6)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev benchmark prior strength is invalid")
		}
		payload.PriorStrength = &value
	}
	if maximum, exists := object["max_permutations"]; exists && maximum != nil {
		value, ok := boundedAnyJevInteger(maximum, 1, 1e6)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev benchmark permutation limit is invalid")
		}
		payload.MaxPermutations = &value
	}

	rawTasks, ok := object["tasks"].([]any)
	if !ok || len(rawTasks) == 0 || len(rawTasks) > 10000 {
		return normalizedItem{}, errors.New("AnyJev benchmark task list is invalid")
	}
	payload.Tasks = make([]anyJevBenchmarkTaskImport, 0, len(rawTasks))
	seenTasks := make(map[string]struct{}, len(rawTasks))
	for _, rawTask := range rawTasks {
		task, ok := rawTask.(map[string]any)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev benchmark task entry is invalid")
		}
		for key := range task {
			if !anyJevBenchmarkTaskFields[key] {
				return normalizedItem{}, fmt.Errorf("unsupported AnyJev benchmark task field %q", key)
			}
		}
		taskName, taskOK := boundedAnyJevBenchmarkString(task["task"], 256)
		question, questionOK := boundedAnyJevBenchmarkString(task["question"], 256)
		k, kOK := boundedAnyJevInteger(task["k"], 2, 26)
		taskTestExamples, taskTestOK := boundedAnyJevInteger(task["n_test"], 1, testExamples)
		taskCalibrationExamples, taskCalibrationOK := boundedAnyJevInteger(task["n_calib"], 0, calibrationExamples)
		if !taskOK || !questionOK || !kOK || !taskTestOK || !taskCalibrationOK {
			return normalizedItem{}, errors.New("AnyJev benchmark task identity or counts are invalid")
		}
		taskHash := anyJevStringSHA256(taskName)
		if _, duplicate := seenTasks[taskHash]; duplicate {
			return normalizedItem{}, errors.New("AnyJev benchmark contains duplicate task identifiers")
		}
		seenTasks[taskHash] = struct{}{}
		levels, ok := task["levels"].(map[string]any)
		if !ok || len(levels) == 0 || len(levels) > 16 {
			return normalizedItem{}, errors.New("AnyJev benchmark task levels are invalid")
		}
		projected := anyJevBenchmarkTaskImport{
			TaskKeySHA256: taskHash, QuestionKeySHA256: anyJevStringSHA256(question), K: k,
			TestExamples: taskTestExamples, CalibrationExamples: taskCalibrationExamples,
			Levels: make([]anyJevBenchmarkLevelImport, 0, len(levels)),
		}
		for _, name := range sortedKeys(levels) {
			if !anyJevBenchmarkLevels[name] {
				return normalizedItem{}, fmt.Errorf("unsupported AnyJev benchmark level %q", name)
			}
			metrics, ok := levels[name].(map[string]any)
			if !ok || len(metrics) == 0 || len(metrics) > 32 {
				return normalizedItem{}, errors.New("AnyJev benchmark metric set is invalid")
			}
			level := anyJevBenchmarkLevelImport{Name: name, Metrics: make(map[string]float64), MethodSHA256: make(map[string]string), Flags: make(map[string]bool)}
			for metricName, raw := range metrics {
				switch metricName {
				case "adaptive":
					flag, ok := raw.(bool)
					if !ok {
						return normalizedItem{}, errors.New("AnyJev benchmark metric flag is invalid")
					}
					level.Flags[metricName] = flag
				case "prior_method":
					if raw == nil {
						continue
					}
					method, ok := boundedAnyJevBenchmarkString(raw, 128)
					if !ok {
						return normalizedItem{}, errors.New("AnyJev benchmark method label is invalid")
					}
					level.MethodSHA256[metricName] = anyJevStringSHA256(method)
				default:
					bounds, known := anyJevBenchmarkMetricBounds[metricName]
					if !known {
						return normalizedItem{}, fmt.Errorf("unsupported AnyJev benchmark metric %q", metricName)
					}
					if raw == nil && (metricName == "temperature_reversed" || metricName == "prior_strength") {
						continue
					}
					number, ok := boundedAnyJevNumber(raw, bounds[0], bounds[1])
					if !ok || (strings.HasPrefix(metricName, "temperature") && number == 0) {
						return normalizedItem{}, fmt.Errorf("AnyJev benchmark metric %q is invalid", metricName)
					}
					if metricName == "n" && number != float64(taskTestExamples) {
						return normalizedItem{}, errors.New("AnyJev benchmark level count does not match task test count")
					}
					level.Metrics[metricName] = number
				}
			}
			if _, exists := level.Metrics["n"]; !exists {
				return normalizedItem{}, errors.New("AnyJev benchmark level omits its evaluated example count")
			}
			if len(level.MethodSHA256) == 0 {
				level.MethodSHA256 = nil
			}
			if len(level.Flags) == 0 {
				level.Flags = nil
			}
			projected.Levels = append(projected.Levels, level)
		}
		payload.Tasks = append(payload.Tasks, projected)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev benchmark projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func boundedAnyJevBenchmarkString(value any, maximum int) (string, bool) {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" || len(text) > maximum || privacy.ContainsCredentialText(text) {
		return "", false
	}
	return text, true
}

func anyJevStringSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
