package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevHeadStudyResultSchema = "threadforge.anyjev-head-study-result.v1"

type anyJevHeadStudyImport struct {
	SchemaVersion       string                    `json:"schema_version"`
	SourceKind          string                    `json:"source_kind"`
	ModelSHA256         string                    `json:"model_sha256"`
	Seed                int                       `json:"seed"`
	Layers              []int                     `json:"layers"`
	Kinds               []string                  `json:"kinds"`
	Labels              []string                  `json:"labels"`
	QuestionCount       int                       `json:"question_count"`
	TestExamples        int                       `json:"test_examples"`
	CalibrationExamples int                       `json:"calibration_examples"`
	Pooled              []anyJevHeadStudyPooled   `json:"pooled"`
	Questions           []anyJevHeadStudyQuestion `json:"questions"`
	OmittedFields       []string                  `json:"omitted_fields"`
	HumanReviewRequired bool                      `json:"human_review_required"`
	ExecutionVerified   bool                      `json:"execution_verified"`
	CutoverEligible     bool                      `json:"cutover_evidence_eligible"`
}

type anyJevHeadStudyPooled struct {
	Method string                 `json:"method"`
	Stats  anyJevHeadStudyMetrics `json:"metrics"`
}

type anyJevHeadStudyQuestion struct {
	QuestionSHA256      string                `json:"question_sha256"`
	Kind                string                `json:"kind"`
	OptionCount         int                   `json:"option_count"`
	TestExamples        int                   `json:"test_examples"`
	CalibrationExamples int                   `json:"calibration_examples"`
	Rows                []anyJevHeadStudyRow  `json:"rows"`
	Heads               []anyJevHeadStudyHead `json:"heads"`
}

type anyJevHeadStudyRow struct {
	Method  string                 `json:"method"`
	Metrics anyJevHeadStudyMetrics `json:"metrics"`
}

type anyJevHeadStudyHead struct {
	Method      string                    `json:"method"`
	Layer       int                       `json:"layer"`
	Parameters  anyJevHeadStudyParameters `json:"parameters"`
	Temperature float64                   `json:"temperature"`
	LabelCount  int                       `json:"label_count"`
	OOFNLL      float64                   `json:"oof_nll"`
	OOFAccuracy float64                   `json:"oof_accuracy"`
}

type anyJevHeadStudyParameters struct {
	Shrinkage *float64 `json:"shrinkage,omitempty"`
	Lambda    *float64 `json:"lambda,omitempty"`
	Rank      *int     `json:"rank,omitempty"`
}

type anyJevHeadStudyMetrics struct {
	N        int     `json:"n,omitempty"`
	Accuracy float64 `json:"acc"`
	ECE      float64 `json:"ece"`
	Brier    float64 `json:"brier"`
	NLL      float64 `json:"nll"`
	Flip     float64 `json:"flip"`
}

var anyJevHeadStudyRootFields = []string{"model", "layers", "kinds", "labels", "seed", "env", "date", "typed"}
var anyJevHeadStudyEnvFields = map[string]bool{
	"python": true, "date": true, "anyjev": true, "batch_size": true,
	"git_commit": true, "torch": true, "transformers": true, "gpu": true, "nvidia_smi": true,
}
var anyJevHeadStudyKinds = map[string]bool{"diff_means": true, "lda": true, "ridge": true, "rrr": true}
var anyJevHeadStudyLabels = map[string]bool{"gold": true, "self": true}

func isAnyJevHeadStudyJSONPath(relative string) bool {
	_, ok := anyJevHeadStudyDateFromPath(relative)
	return ok
}

func anyJevHeadStudyDateFromPath(relative string) (string, bool) {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 4 || !strings.EqualFold(parts[0], "bench") || !strings.EqualFold(parts[1], "results_heads") ||
		!strings.HasSuffix(strings.ToLower(parts[3]), ".typed.json") || len(parts[3]) <= len(".typed.json") {
		return "", false
	}
	date, err := parseAnyJevMigrationDate(parts[2])
	if err != nil {
		return "", false
	}
	return date, true
}

func normalizeAnyJevHeadStudyResult(relative string, value any) (normalizedItem, error) {
	pathDate, validPath := anyJevHeadStudyDateFromPath(relative)
	if !validPath {
		return normalizedItem{}, errors.New("AnyJev head-study result path is invalid")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("AnyJev head-study result must be an object")
	}
	if err := requireAnyJevHeadStudyFields(root, anyJevHeadStudyRootFields); err != nil {
		return normalizedItem{}, err
	}
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	date, dateOK := boundedAnyJevBenchmarkString(root["date"], 64)
	if !modelOK {
		return normalizedItem{}, errors.New("AnyJev head-study model identifier is invalid")
	}
	if !dateOK {
		return normalizedItem{}, errors.New("AnyJev head-study date is invalid")
	}
	dateDirectory, timestampOK := anyJevHeadStudyTimestampDate(date)
	if !timestampOK {
		return normalizedItem{}, errors.New("AnyJev head-study timestamp is invalid")
	}
	if dateDirectory != pathDate {
		return normalizedItem{}, errors.New("AnyJev head-study date does not match its source path")
	}
	seed, seedOK := boundedAnyJevInteger(root["seed"], 0, 1<<32-1)
	if !seedOK {
		return normalizedItem{}, errors.New("AnyJev head-study seed is invalid")
	}
	if err := validateAnyJevHeadStudyEnv(root["env"], pathDate); err != nil {
		return normalizedItem{}, err
	}
	layers, err := parseAnyJevHeadStudyLayers(root["layers"])
	if err != nil {
		return normalizedItem{}, err
	}
	kinds, err := parseAnyJevHeadStudyLabels(root["kinds"], anyJevHeadStudyKinds, 4, "kinds")
	if err != nil || len(kinds) != len(anyJevHeadStudyKinds) {
		return normalizedItem{}, errors.New("AnyJev head-study solver kinds are invalid")
	}
	labels, err := parseAnyJevHeadStudyLabels(root["labels"], anyJevHeadStudyLabels, 2, "labels")
	if err != nil || len(labels) != len(anyJevHeadStudyLabels) {
		return normalizedItem{}, errors.New("AnyJev head-study training labels are invalid")
	}

	typed, ok := root["typed"].(map[string]any)
	if !ok || requireAnyJevHeadStudyFields(typed, []string{"questions", "pooled"}) != nil {
		return normalizedItem{}, errors.New("AnyJev head-study typed results have an unsupported shape")
	}
	questions, ok := typed["questions"].(map[string]any)
	if !ok || len(questions) == 0 || len(questions) > 10000 {
		return normalizedItem{}, errors.New("AnyJev head-study questions are invalid")
	}
	methods := anyJevHeadStudyMethods(kinds)
	headMethods := anyJevHeadStudyHeadMethods(kinds)
	pooledSource, ok := typed["pooled"].(map[string]any)
	if !ok || requireAnyJevHeadStudyMapKeys(pooledSource, methods, "pooled methods") != nil {
		return normalizedItem{}, errors.New("AnyJev head-study pooled method matrix is invalid")
	}

	payload := anyJevHeadStudyImport{
		SchemaVersion: anyJevHeadStudyResultSchema, SourceKind: "per_question_head_study",
		ModelSHA256: anyJevStringSHA256(model), Seed: seed, Layers: layers, Kinds: kinds, Labels: labels,
		QuestionCount: len(questions),
		Pooled:        []anyJevHeadStudyPooled{}, Questions: []anyJevHeadStudyQuestion{},
		OmittedFields: []string{
			"model_label", "question_keys", "study_date", "environment_details", "hardware_details",
			"git_commit", "source_paths", "raw_question_content",
		},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	pooledByMethod := make(map[string]anyJevHeadStudyMetrics, len(methods))
	for _, method := range methods {
		stats, err := parseAnyJevHeadStudyMetrics(pooledSource[method], true)
		if err != nil {
			return normalizedItem{}, fmt.Errorf("AnyJev head-study pooled method %s: %w", method, err)
		}
		pooledByMethod[method] = stats
		payload.Pooled = append(payload.Pooled, anyJevHeadStudyPooled{Method: method, Stats: stats})
	}

	questionKeys := sortedKeys(questions)
	totalTest, totalCalibration := 0, 0
	weighted := make(map[string]map[string]float64, len(methods))
	for _, method := range methods {
		weighted[method] = map[string]float64{"acc": 0, "brier": 0, "nll": 0, "flip": 0}
	}
	for _, key := range questionKeys {
		if _, valid := boundedAnyJevBenchmarkString(key, 2048); !valid || privacy.ContainsCredentialText(key) {
			return normalizedItem{}, errors.New("AnyJev head-study question key is invalid")
		}
		question, ok := questions[key].(map[string]any)
		if !ok || requireAnyJevHeadStudyFields(question, []string{"kind", "k", "n_test", "n_calib", "rows", "heads"}) != nil {
			return normalizedItem{}, errors.New("AnyJev head-study question has an unsupported shape")
		}
		kind, kindOK := question["kind"].(string)
		optionCount, optionOK := boundedAnyJevInteger(question["k"], 2, 100)
		testCount, testOK := boundedAnyJevInteger(question["n_test"], 1, 1e7)
		calibrationCount, calibrationOK := boundedAnyJevInteger(question["n_calib"], 0, 1e7)
		if !kindOK || !anyJevTypedQuestionKinds[kind] || !optionOK || !testOK || !calibrationOK {
			return normalizedItem{}, errors.New("AnyJev head-study question metadata is invalid")
		}
		totalTest += testCount
		totalCalibration += calibrationCount
		if totalTest > 1e7 || totalCalibration > 1e7 {
			return normalizedItem{}, errors.New("AnyJev head-study example totals exceed bounds")
		}

		rowsSource, ok := question["rows"].(map[string]any)
		if !ok || requireAnyJevHeadStudyMapKeys(rowsSource, methods, "question method rows") != nil {
			return normalizedItem{}, errors.New("AnyJev head-study question method matrix is invalid")
		}
		projectedQuestion := anyJevHeadStudyQuestion{
			QuestionSHA256: anyJevStringSHA256(key), Kind: kind, OptionCount: optionCount,
			TestExamples: testCount, CalibrationExamples: calibrationCount,
			Rows: []anyJevHeadStudyRow{}, Heads: []anyJevHeadStudyHead{},
		}
		for _, method := range methods {
			stats, err := parseAnyJevHeadStudyMetrics(rowsSource[method], false)
			if err != nil {
				return normalizedItem{}, fmt.Errorf("AnyJev head-study question method %s: %w", method, err)
			}
			projectedQuestion.Rows = append(projectedQuestion.Rows, anyJevHeadStudyRow{Method: method, Metrics: stats})
			weighted[method]["acc"] += stats.Accuracy * float64(testCount)
			weighted[method]["brier"] += stats.Brier * float64(testCount)
			weighted[method]["nll"] += stats.NLL * float64(testCount)
			weighted[method]["flip"] += stats.Flip * float64(testCount)
		}
		headsSource, ok := question["heads"].(map[string]any)
		if !ok || requireAnyJevHeadStudyMapKeys(headsSource, headMethods, "head selections") != nil {
			return normalizedItem{}, errors.New("AnyJev head-study head-selection matrix is invalid")
		}
		for _, method := range headMethods {
			head, err := parseAnyJevHeadStudyHead(method, headsSource[method], calibrationCount)
			if err != nil {
				return normalizedItem{}, fmt.Errorf("AnyJev head-study %s: %w", method, err)
			}
			projectedQuestion.Heads = append(projectedQuestion.Heads, head)
		}
		payload.Questions = append(payload.Questions, projectedQuestion)
	}
	if totalTest == 0 {
		return normalizedItem{}, errors.New("AnyJev head-study test example count is zero")
	}
	payload.TestExamples, payload.CalibrationExamples = totalTest, totalCalibration
	for _, method := range methods {
		stats := pooledByMethod[method]
		if stats.N != totalTest {
			return normalizedItem{}, fmt.Errorf("AnyJev head-study pooled count mismatch for %s", method)
		}
		for _, metric := range []string{"acc", "brier", "nll", "flip"} {
			pooledValue := anyJevHeadStudyMetricValue(stats, metric)
			questionMean := weighted[method][metric] / float64(totalTest)
			if math.Abs(pooledValue-questionMean) > 0.001 {
				return normalizedItem{}, fmt.Errorf("AnyJev head-study pooled %s does not reconcile for %s", metric, method)
			}
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev head-study projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func anyJevHeadStudyMethods(kinds []string) []string {
	methods := []string{"raw", "L1", "L1@self", "L0-perm", "L0-perm+T", "L0-perm+T@self"}
	for _, kind := range kinds {
		for _, average := range []string{"", "/avg"} {
			for _, label := range []string{"", "@self"} {
				methods = append(methods, "head:"+kind+average+label)
			}
		}
	}
	sort.Strings(methods)
	return methods
}

func anyJevHeadStudyHeadMethods(kinds []string) []string {
	methods := make([]string, 0, len(kinds)*4)
	for _, method := range anyJevHeadStudyMethods(kinds) {
		if strings.HasPrefix(method, "head:") {
			methods = append(methods, method)
		}
	}
	return methods
}

func parseAnyJevHeadStudyLayers(value any) ([]int, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > 128 {
		return nil, errors.New("AnyJev head-study layer list is invalid")
	}
	seen := make(map[int]bool, len(raw))
	layers := make([]int, 0, len(raw))
	for _, item := range raw {
		layer, valid := boundedAnyJevInteger(item, -4096, 4096)
		if !valid || seen[layer] {
			return nil, errors.New("AnyJev head-study layers must be bounded and unique")
		}
		seen[layer] = true
		layers = append(layers, layer)
	}
	return layers, nil
}

func parseAnyJevHeadStudyLabels(value any, allowed map[string]bool, maximum int, name string) ([]string, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > maximum {
		return nil, fmt.Errorf("AnyJev head-study %s are invalid", name)
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		label, valid := item.(string)
		if !valid || !allowed[label] || seen[label] {
			return nil, fmt.Errorf("AnyJev head-study %s contain an unsupported value", name)
		}
		seen[label] = true
		out = append(out, label)
	}
	for label := range allowed {
		if !seen[label] {
			return nil, fmt.Errorf("AnyJev head-study %s are incomplete", name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func validateAnyJevHeadStudyEnv(value any, date string) error {
	env, ok := value.(map[string]any)
	if !ok || len(env) > len(anyJevHeadStudyEnvFields) {
		return errors.New("AnyJev head-study environment metadata is invalid")
	}
	for key, raw := range env {
		if !anyJevHeadStudyEnvFields[key] {
			return fmt.Errorf("unsupported AnyJev head-study environment field %q", key)
		}
		if raw == nil {
			continue
		}
		if key == "batch_size" {
			if _, valid := boundedAnyJevInteger(raw, 1, 1e6); !valid {
				return errors.New("AnyJev head-study environment batch size is invalid")
			}
			continue
		}
		if key == "date" {
			text, valid := boundedAnyJevBenchmarkString(raw, 64)
			dateDirectory, timestampOK := anyJevHeadStudyTimestampDate(text)
			if !valid || !timestampOK || dateDirectory != date {
				return errors.New("AnyJev head-study environment date is invalid")
			}
			continue
		}
		if _, valid := boundedAnyJevBenchmarkString(raw, 8192); !valid {
			return fmt.Errorf("AnyJev head-study environment field %s is invalid", key)
		}
	}
	return nil
}

func anyJevHeadStudyTimestampDate(value string) (string, bool) {
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.Format("2006-01-02"), true
		}
	}
	return "", false
}

func requireAnyJevHeadStudyFields(object map[string]any, fields []string) error {
	if len(object) != len(fields) {
		return errors.New("AnyJev head-study object has an unknown or missing field")
	}
	for _, field := range fields {
		if _, exists := object[field]; !exists {
			return fmt.Errorf("AnyJev head-study object omits %s", field)
		}
	}
	return nil
}

func requireAnyJevHeadStudyMapKeys(object map[string]any, expected []string, label string) error {
	if len(object) != len(expected) {
		return fmt.Errorf("AnyJev head-study %s have an unexpected count", label)
	}
	for _, key := range expected {
		if _, exists := object[key]; !exists {
			return fmt.Errorf("AnyJev head-study %s omit %s", label, key)
		}
	}
	return nil
}

func parseAnyJevHeadStudyMetrics(value any, pooled bool) (anyJevHeadStudyMetrics, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return anyJevHeadStudyMetrics{}, errors.New("metrics must be an object")
	}
	fields := []string{"acc", "ece", "brier", "nll", "flip"}
	if pooled {
		fields = append(fields, "n")
	}
	if err := requireAnyJevHeadStudyFields(object, fields); err != nil {
		return anyJevHeadStudyMetrics{}, err
	}
	accuracy, accuracyOK := boundedAnyJevNumber(object["acc"], 0, 1)
	ece, eceOK := boundedAnyJevNumber(object["ece"], 0, 1)
	brier, brierOK := boundedAnyJevNumber(object["brier"], 0, 2)
	nll, nllOK := boundedAnyJevNumber(object["nll"], 0, 1e6)
	flip, flipOK := boundedAnyJevNumber(object["flip"], 0, 1)
	if !accuracyOK || !eceOK || !brierOK || !nllOK || !flipOK {
		return anyJevHeadStudyMetrics{}, errors.New("metric values are outside bounds")
	}
	metrics := anyJevHeadStudyMetrics{Accuracy: accuracy, ECE: ece, Brier: brier, NLL: nll, Flip: flip}
	if pooled {
		n, valid := boundedAnyJevInteger(object["n"], 1, 1e7)
		if !valid {
			return anyJevHeadStudyMetrics{}, errors.New("pooled metric count is invalid")
		}
		metrics.N = n
	}
	return metrics, nil
}

func parseAnyJevHeadStudyHead(method string, value any, calibrationCount int) (anyJevHeadStudyHead, error) {
	object, ok := value.(map[string]any)
	if !ok || requireAnyJevHeadStudyFields(object, []string{"layer", "params", "temperature", "n_labels", "oof_nll", "oof_acc"}) != nil {
		return anyJevHeadStudyHead{}, errors.New("head metadata has an unsupported shape")
	}
	layer, layerOK := boundedAnyJevInteger(object["layer"], 0, 4096)
	temperature, temperatureOK := boundedAnyJevNumber(object["temperature"], 0, 1e3)
	labels, labelsOK := boundedAnyJevInteger(object["n_labels"], 1, 1e7)
	oofNLL, nllOK := boundedAnyJevNumber(object["oof_nll"], 0, 1e6)
	oofAccuracy, accuracyOK := boundedAnyJevNumber(object["oof_acc"], 0, 1)
	if !layerOK {
		return anyJevHeadStudyHead{}, errors.New("head layer is outside bounds")
	}
	parameters, err := parseAnyJevHeadStudyParameters(method, object["params"])
	if err != nil {
		return anyJevHeadStudyHead{}, err
	}
	if !temperatureOK {
		return anyJevHeadStudyHead{}, errors.New("head temperature is outside bounds")
	}
	if !labelsOK || labels > calibrationCount {
		return anyJevHeadStudyHead{}, errors.New("head label count is invalid for calibration data")
	}
	if !nllOK || !accuracyOK {
		return anyJevHeadStudyHead{}, errors.New("head out-of-fold metrics are outside bounds")
	}
	return anyJevHeadStudyHead{
		Method: method, Layer: layer, Parameters: parameters, Temperature: temperature,
		LabelCount: labels, OOFNLL: oofNLL, OOFAccuracy: oofAccuracy,
	}, nil
}

func parseAnyJevHeadStudyParameters(method string, value any) (anyJevHeadStudyParameters, error) {
	params, ok := value.(map[string]any)
	if !ok {
		return anyJevHeadStudyParameters{}, errors.New("head solver parameters must be an object")
	}
	base := strings.TrimPrefix(method, "head:")
	base = strings.TrimSuffix(base, "@self")
	base = strings.TrimSuffix(base, "/avg")
	result := anyJevHeadStudyParameters{}
	switch base {
	case "diff_means":
		if len(params) != 0 {
			return anyJevHeadStudyParameters{}, errors.New("diff_means parameters are unsupported")
		}
	case "lda":
		if err := requireAnyJevHeadStudyFields(params, []string{"shrinkage"}); err != nil {
			return anyJevHeadStudyParameters{}, errors.New("lda parameters are invalid")
		}
		value, valid := boundedAnyJevNumber(params["shrinkage"], 0, 1)
		if !valid {
			return anyJevHeadStudyParameters{}, errors.New("lda shrinkage is outside bounds")
		}
		result.Shrinkage = &value
	case "ridge":
		if err := requireAnyJevHeadStudyFields(params, []string{"lam"}); err != nil {
			return anyJevHeadStudyParameters{}, errors.New("ridge parameters are invalid")
		}
		value, valid := boundedAnyJevNumber(params["lam"], 0, 1e6)
		if !valid {
			return anyJevHeadStudyParameters{}, errors.New("ridge lambda is outside bounds")
		}
		result.Lambda = &value
	case "rrr":
		if err := requireAnyJevHeadStudyFields(params, []string{"lam", "rank"}); err != nil {
			return anyJevHeadStudyParameters{}, errors.New("rrr parameters are invalid")
		}
		lambda, lambdaOK := boundedAnyJevNumber(params["lam"], 0, 1e6)
		rank, rankOK := boundedAnyJevInteger(params["rank"], 1, 4096)
		if !lambdaOK || !rankOK {
			return anyJevHeadStudyParameters{}, errors.New("rrr lambda or rank is outside bounds")
		}
		result.Lambda, result.Rank = &lambda, &rank
	default:
		return anyJevHeadStudyParameters{}, errors.New("head solver kind is unsupported")
	}
	return result, nil
}

func anyJevHeadStudyMetricValue(metrics anyJevHeadStudyMetrics, name string) float64 {
	switch name {
	case "acc":
		return metrics.Accuracy
	case "brier":
		return metrics.Brier
	case "nll":
		return metrics.NLL
	case "flip":
		return metrics.Flip
	default:
		return math.NaN()
	}
}
