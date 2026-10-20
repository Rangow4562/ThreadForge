package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevTypedBenchmarkResultSchema = "threadforge.anyjev-typed-benchmark-result.v1"

var anyJevTypedRunnerFields = map[string]bool{
	"model": true, "dataset": true, "prior": true, "prior_strength": true,
	"limit_cases": true, "calib_cases_per_workflow": true, "env": true,
	"levels": true, "seconds": true, "n_decisions": true,
	"n_questions": true, "n_calib_decisions": true,
}

var anyJevTypedProviderFields = map[string]bool{
	"provider": true, "checkpoint": true, "limit_cases": true,
	"date": true, "overall": true, "seconds": true, "by_type": true,
}

var anyJevTypedMetricBounds = map[string][2]float64{
	"acc": {0, 1}, "soft_acc": {0, 1}, "ece": {0, 1},
	"brier": {0, 2}, "brier_mean": {0, 2}, "nll": {0, 1e6}, "score_mae": {0, 1e6},
}

var anyJevTypedLevels = map[string]bool{
	"provider": true, "raw": true, "L0": true, "L1": true,
	"L0-bc": true, "L0-cf": true, "L0-perm": true,
	"L0-perm+bc": true, "L0-perm+cf": true,
}

var anyJevTypedQuestionKinds = map[string]bool{"choice": true, "noul": true, "score": true}

type anyJevTypedBenchmarkImport struct {
	SchemaVersion           string                      `json:"schema_version"`
	SourceKind              string                      `json:"source_kind"`
	BackendKind             string                      `json:"backend_kind"`
	ModelSHA256             string                      `json:"model_sha256"`
	ProviderSHA256          string                      `json:"provider_sha256,omitempty"`
	DatasetLabelSHA256      string                      `json:"dataset_label_sha256"`
	Prior                   string                      `json:"prior,omitempty"`
	PriorStrength           *float64                    `json:"prior_strength,omitempty"`
	TestExamples            int                         `json:"test_examples"`
	CalibrationExamples     *int                        `json:"calibration_examples,omitempty"`
	QuestionCount           *int                        `json:"question_count,omitempty"`
	TestLimit               *int                        `json:"test_limit,omitempty"`
	Levels                  []anyJevTypedBenchmarkLevel `json:"levels"`
	OmittedFields           []string                    `json:"omitted_fields"`
	HumanReviewRequired     bool                        `json:"human_review_required"`
	ExecutionVerified       bool                        `json:"execution_verified"`
	CutoverEvidenceEligible bool                        `json:"cutover_evidence_eligible"`
}

type anyJevTypedBenchmarkLevel struct {
	Name       string                       `json:"name"`
	Overall    anyJevTypedBenchmarkMetrics  `json:"overall"`
	ByType     []anyJevTypedTypeMetrics     `json:"by_type"`
	ByWorkflow []anyJevTypedWorkflowMetrics `json:"by_workflow,omitempty"`
	ByQuestion []anyJevTypedQuestionMetrics `json:"by_question,omitempty"`
}

type anyJevTypedBenchmarkMetrics struct {
	N         int      `json:"n"`
	Accuracy  float64  `json:"acc"`
	SoftAcc   *float64 `json:"soft_acc,omitempty"`
	ECE       *float64 `json:"ece,omitempty"`
	Brier     *float64 `json:"brier,omitempty"`
	BrierMean *float64 `json:"brier_mean,omitempty"`
	NLL       *float64 `json:"nll,omitempty"`
	ScoreMAE  *float64 `json:"score_mae,omitempty"`
}

type anyJevTypedTypeMetrics struct {
	Kind    string                      `json:"kind"`
	Metrics anyJevTypedBenchmarkMetrics `json:"metrics"`
}

type anyJevTypedWorkflowMetrics struct {
	WorkflowSHA256 string                      `json:"workflow_sha256"`
	Metrics        anyJevTypedBenchmarkMetrics `json:"metrics"`
}

type anyJevTypedQuestionMetrics struct {
	QuestionKeySHA256 string                      `json:"question_key_sha256"`
	WorkflowSHA256    string                      `json:"workflow_sha256"`
	Kind              string                      `json:"kind"`
	OptionCount       int                         `json:"option_count"`
	Metrics           anyJevTypedBenchmarkMetrics `json:"metrics"`
	Majority          float64                     `json:"majority"`
	LabelEntropy      float64                     `json:"label_entropy"`
}

func isAnyJevTypedBenchmarkJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	if !strings.HasPrefix(path, "bench/") || !strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".items.json") {
		return false
	}
	family := strings.SplitN(strings.TrimPrefix(path, "bench/"), "/", 2)[0]
	switch family {
	case "results_typed", "results_typed_v01", "results_typed_small", "results_typed_diag", "results_typed_dump":
		return true
	default:
		return false
	}
}

func normalizeAnyJevTypedBenchmarkResult(value any) (normalizedItem, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("typed AnyJev benchmark result must be an object")
	}
	if _, runner := object["levels"]; runner {
		return normalizeAnyJevTypedRunnerResult(object)
	}
	return normalizeAnyJevTypedProviderResult(object)
}

func normalizeAnyJevTypedRunnerResult(object map[string]any) (normalizedItem, error) {
	for key := range object {
		if !anyJevTypedRunnerFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported typed AnyJev runner field %q", key)
		}
	}
	model, ok := boundedAnyJevBenchmarkString(object["model"], 2048)
	if !ok {
		return normalizedItem{}, errors.New("typed AnyJev runner model identifier is invalid")
	}
	dataset, ok := boundedAnyJevBenchmarkString(object["dataset"], 512)
	if !ok || dataset != "LocalLLaMA/typed-decisions" {
		return normalizedItem{}, errors.New("typed AnyJev runner dataset is unsupported")
	}
	prior, ok := object["prior"].(string)
	if !ok || !oneOf(prior, "batch", "content_free", "none") {
		return normalizedItem{}, errors.New("typed AnyJev runner prior is invalid")
	}
	testCount, testOK := boundedAnyJevInteger(object["n_decisions"], 1, 1e7)
	questionCount, questionOK := boundedAnyJevInteger(object["n_questions"], 1, 1e6)
	if !testOK || !questionOK {
		return normalizedItem{}, errors.New("typed AnyJev runner totals are invalid")
	}
	payload := anyJevTypedBenchmarkImport{
		SchemaVersion: anyJevTypedBenchmarkResultSchema, SourceKind: "runner_result", BackendKind: "hf",
		ModelSHA256: anyJevStringSHA256(model), DatasetLabelSHA256: anyJevStringSHA256(dataset),
		Prior: prior, TestExamples: testCount, QuestionCount: &questionCount,
		Levels: []anyJevTypedBenchmarkLevel{}, OmittedFields: anyJevTypedOmittedFields(),
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEvidenceEligible: false,
	}
	if strength, exists := object["prior_strength"]; exists && strength != nil {
		number, valid := boundedAnyJevNumber(strength, 0, 1e6)
		if !valid {
			return normalizedItem{}, errors.New("typed AnyJev runner prior strength is invalid")
		}
		payload.PriorStrength = &number
	}
	if raw, exists := object["n_calib_decisions"]; exists {
		count, valid := boundedAnyJevInteger(raw, 0, 1e7)
		if !valid {
			return normalizedItem{}, errors.New("typed AnyJev runner calibration count is invalid")
		}
		payload.CalibrationExamples = &count
	}
	if raw, exists := object["limit_cases"]; exists && raw != nil {
		limit, valid := boundedAnyJevInteger(raw, 1, 1e7)
		if !valid {
			return normalizedItem{}, errors.New("typed AnyJev runner test limit is invalid")
		}
		payload.TestLimit = &limit
	}
	if env, exists := object["env"]; exists {
		if details, valid := env.(map[string]any); !valid || len(details) > 256 {
			return normalizedItem{}, errors.New("typed AnyJev runner environment metadata is invalid")
		}
	}
	if raw, exists := object["seconds"]; exists {
		if _, valid := boundedAnyJevNumber(raw, 0, 1e9); !valid {
			return normalizedItem{}, errors.New("typed AnyJev runner duration is invalid")
		}
	}
	if raw, exists := object["calib_cases_per_workflow"]; exists && raw != nil {
		if _, valid := boundedAnyJevInteger(raw, 0, 1e7); !valid {
			return normalizedItem{}, errors.New("typed AnyJev runner calibration limit is invalid")
		}
	}
	levels, valid := object["levels"].(map[string]any)
	if !valid || len(levels) == 0 || len(levels) > 16 {
		return normalizedItem{}, errors.New("typed AnyJev runner levels are invalid")
	}
	for _, name := range sortedKeys(levels) {
		if !anyJevTypedLevels[name] || name == "provider" {
			return normalizedItem{}, fmt.Errorf("unsupported typed AnyJev level %q", name)
		}
		level, err := normalizeAnyJevTypedLevel(name, levels[name], testCount, questionCount)
		if err != nil {
			return normalizedItem{}, fmt.Errorf("typed AnyJev level %s: %w", name, err)
		}
		payload.Levels = append(payload.Levels, level)
	}
	return typedAnyJevBenchmarkItem(payload)
}

func normalizeAnyJevTypedProviderResult(object map[string]any) (normalizedItem, error) {
	for key := range object {
		if !anyJevTypedProviderFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported typed AnyJev provider field %q", key)
		}
	}
	provider, providerOK := boundedAnyJevBenchmarkString(object["provider"], 2048)
	checkpoint, checkpointOK := boundedAnyJevBenchmarkString(object["checkpoint"], 2048)
	if !providerOK && !checkpointOK {
		return normalizedItem{}, errors.New("typed AnyJev provider result has no model identity")
	}
	modelIdentity := checkpoint
	if modelIdentity == "" {
		modelIdentity = provider
	}
	payload := anyJevTypedBenchmarkImport{
		SchemaVersion: anyJevTypedBenchmarkResultSchema, SourceKind: "provider_export", BackendKind: "provider",
		ModelSHA256: anyJevStringSHA256(modelIdentity), DatasetLabelSHA256: anyJevStringSHA256("LocalLLaMA/typed-decisions"),
		Levels: []anyJevTypedBenchmarkLevel{}, OmittedFields: anyJevTypedOmittedFields(),
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEvidenceEligible: false,
	}
	if providerOK {
		payload.ProviderSHA256 = anyJevStringSHA256(provider)
	}
	if raw, exists := object["limit_cases"]; exists && raw != nil {
		limit, valid := boundedAnyJevInteger(raw, 1, 1e7)
		if !valid {
			return normalizedItem{}, errors.New("typed AnyJev provider test limit is invalid")
		}
		payload.TestLimit = &limit
	}
	if raw, exists := object["date"]; exists {
		if _, valid := raw.(string); !valid {
			return normalizedItem{}, errors.New("typed AnyJev provider date metadata is invalid")
		}
	}
	if raw, exists := object["seconds"]; exists {
		if _, valid := boundedAnyJevNumber(raw, 0, 1e9); !valid {
			return normalizedItem{}, errors.New("typed AnyJev provider duration is invalid")
		}
	}
	overall, err := normalizeAnyJevTypedMetrics(object["overall"])
	if err != nil {
		return normalizedItem{}, fmt.Errorf("typed AnyJev provider overall metrics: %w", err)
	}
	payload.TestExamples = overall.N
	byType, total, err := normalizeAnyJevTypedByType(object["by_type"])
	if err != nil {
		return normalizedItem{}, err
	}
	if total != overall.N {
		return normalizedItem{}, errors.New("typed AnyJev provider type counts do not match overall count")
	}
	payload.Levels = append(payload.Levels, anyJevTypedBenchmarkLevel{
		Name: "provider", Overall: overall, ByType: byType,
	})
	return typedAnyJevBenchmarkItem(payload)
}

func normalizeAnyJevTypedLevel(name string, value any, total, questionCount int) (anyJevTypedBenchmarkLevel, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) == 0 || len(object) > 4 {
		return anyJevTypedBenchmarkLevel{}, errors.New("level aggregate is invalid")
	}
	for key := range object {
		if !oneOf(key, "overall", "by_type", "by_workflow", "by_question") {
			return anyJevTypedBenchmarkLevel{}, fmt.Errorf("unsupported level field %q", key)
		}
	}
	overall, err := normalizeAnyJevTypedMetrics(object["overall"])
	if err != nil || overall.N != total {
		return anyJevTypedBenchmarkLevel{}, errors.New("level overall count does not match run count")
	}
	byType, typeTotal, err := normalizeAnyJevTypedByType(object["by_type"])
	if err != nil || typeTotal != total {
		return anyJevTypedBenchmarkLevel{}, errors.New("level type counts do not match run count")
	}
	level := anyJevTypedBenchmarkLevel{Name: name, Overall: overall, ByType: byType}
	var byWorkflow []anyJevTypedWorkflowMetrics
	if raw, exists := object["by_workflow"]; exists {
		var workflowTotal int
		byWorkflow, workflowTotal, err = normalizeAnyJevTypedByWorkflow(raw)
		if err != nil || workflowTotal != total {
			return anyJevTypedBenchmarkLevel{}, errors.New("level workflow counts do not match run count")
		}
		level.ByWorkflow = byWorkflow
	}
	if raw, exists := object["by_question"]; exists {
		byQuestion, questionTotal, err := normalizeAnyJevTypedByQuestion(raw)
		if err != nil || questionTotal != total || len(byQuestion) != questionCount {
			return anyJevTypedBenchmarkLevel{}, errors.New("level question counts do not match run totals")
		}
		if !anyJevTypedQuestionDimensionsReconcile(byType, byWorkflow, byQuestion) {
			return anyJevTypedBenchmarkLevel{}, errors.New("level per-question counts do not reconcile with type or workflow aggregates")
		}
		level.ByQuestion = byQuestion
	}
	return level, nil
}

func anyJevTypedQuestionDimensionsReconcile(types []anyJevTypedTypeMetrics, workflows []anyJevTypedWorkflowMetrics, questions []anyJevTypedQuestionMetrics) bool {
	typeCounts := make(map[string]int, len(types))
	for _, group := range types {
		typeCounts[group.Kind] = group.Metrics.N
	}
	workflowCounts := make(map[string]int, len(workflows))
	for _, group := range workflows {
		workflowCounts[group.WorkflowSHA256] = group.Metrics.N
	}
	questionTypeCounts := make(map[string]int, len(types))
	questionWorkflowCounts := make(map[string]int, len(workflows))
	for _, question := range questions {
		questionTypeCounts[question.Kind] += question.Metrics.N
		questionWorkflowCounts[question.WorkflowSHA256] += question.Metrics.N
	}
	if len(typeCounts) != len(questionTypeCounts) {
		return false
	}
	for kind, count := range typeCounts {
		if questionTypeCounts[kind] != count {
			return false
		}
	}
	if len(workflowCounts) > 0 {
		if len(workflowCounts) != len(questionWorkflowCounts) {
			return false
		}
		for workflow, count := range workflowCounts {
			if questionWorkflowCounts[workflow] != count {
				return false
			}
		}
	}
	return true
}

func normalizeAnyJevTypedMetrics(value any) (anyJevTypedBenchmarkMetrics, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) == 0 || len(object) > len(anyJevTypedMetricBounds)+1 {
		return anyJevTypedBenchmarkMetrics{}, errors.New("metric set is invalid")
	}
	count, ok := boundedAnyJevInteger(object["n"], 1, 1e7)
	if !ok {
		return anyJevTypedBenchmarkMetrics{}, errors.New("metric example count is invalid")
	}
	out := anyJevTypedBenchmarkMetrics{N: count}
	for name, raw := range object {
		if name == "n" {
			continue
		}
		bounds, known := anyJevTypedMetricBounds[name]
		if !known {
			return anyJevTypedBenchmarkMetrics{}, fmt.Errorf("unsupported typed AnyJev metric %q", name)
		}
		number, valid := boundedAnyJevNumber(raw, bounds[0], bounds[1])
		if !valid {
			return anyJevTypedBenchmarkMetrics{}, fmt.Errorf("typed AnyJev metric %q is invalid", name)
		}
		switch name {
		case "acc":
			out.Accuracy = number
		case "soft_acc":
			out.SoftAcc = &number
		case "ece":
			out.ECE = &number
		case "brier":
			out.Brier = &number
		case "brier_mean":
			out.BrierMean = &number
		case "nll":
			out.NLL = &number
		case "score_mae":
			out.ScoreMAE = &number
		}
	}
	if _, exists := object["acc"]; !exists {
		return anyJevTypedBenchmarkMetrics{}, errors.New("metric set omits accuracy")
	}
	return out, nil
}

func normalizeAnyJevTypedByType(value any) ([]anyJevTypedTypeMetrics, int, error) {
	groups, ok := value.(map[string]any)
	if !ok || len(groups) == 0 || len(groups) > len(anyJevTypedQuestionKinds) {
		return nil, 0, errors.New("typed AnyJev question-type aggregates are invalid")
	}
	out := make([]anyJevTypedTypeMetrics, 0, len(groups))
	total := 0
	for _, kind := range sortedKeys(groups) {
		if !anyJevTypedQuestionKinds[kind] {
			return nil, 0, fmt.Errorf("unsupported typed AnyJev question kind %q", kind)
		}
		metrics, err := normalizeAnyJevTypedMetrics(groups[kind])
		if err != nil {
			return nil, 0, err
		}
		total += metrics.N
		out = append(out, anyJevTypedTypeMetrics{Kind: kind, Metrics: metrics})
	}
	return out, total, nil
}

func normalizeAnyJevTypedByWorkflow(value any) ([]anyJevTypedWorkflowMetrics, int, error) {
	groups, ok := value.(map[string]any)
	if !ok || len(groups) == 0 || len(groups) > 128 {
		return nil, 0, errors.New("typed AnyJev workflow aggregates are invalid")
	}
	out := make([]anyJevTypedWorkflowMetrics, 0, len(groups))
	total := 0
	seen := make(map[string]struct{}, len(groups))
	for _, label := range sortedKeys(groups) {
		workflow, ok := boundedAnyJevBenchmarkString(label, 256)
		if !ok {
			return nil, 0, errors.New("typed AnyJev workflow label is invalid")
		}
		hash := anyJevStringSHA256(workflow)
		if _, exists := seen[hash]; exists {
			return nil, 0, errors.New("typed AnyJev workflow labels collide")
		}
		seen[hash] = struct{}{}
		metrics, err := normalizeAnyJevTypedMetrics(groups[label])
		if err != nil {
			return nil, 0, err
		}
		total += metrics.N
		out = append(out, anyJevTypedWorkflowMetrics{WorkflowSHA256: hash, Metrics: metrics})
	}
	return out, total, nil
}

func normalizeAnyJevTypedByQuestion(value any) ([]anyJevTypedQuestionMetrics, int, error) {
	groups, ok := value.(map[string]any)
	if !ok || len(groups) == 0 || len(groups) > 10000 {
		return nil, 0, errors.New("typed AnyJev per-question aggregates are invalid")
	}
	out := make([]anyJevTypedQuestionMetrics, 0, len(groups))
	total := 0
	seen := make(map[string]struct{}, len(groups))
	for _, key := range sortedKeys(groups) {
		questionKey, valid := boundedAnyJevBenchmarkString(key, 512)
		if !valid {
			return nil, 0, errors.New("typed AnyJev question key is invalid")
		}
		hash := anyJevStringSHA256(questionKey)
		if _, exists := seen[hash]; exists {
			return nil, 0, errors.New("typed AnyJev question keys collide")
		}
		seen[hash] = struct{}{}
		question, valid := groups[key].(map[string]any)
		if !valid || len(question) == 0 || len(question) > 16 {
			return nil, 0, errors.New("typed AnyJev per-question row is invalid")
		}
		for field := range question {
			if !oneOf(field, "n", "acc", "soft_acc", "ece", "brier", "brier_mean", "nll", "score_mae", "workflow", "name", "kind", "k", "majority", "label_entropy") {
				return nil, 0, fmt.Errorf("unsupported typed AnyJev question field %q", field)
			}
		}
		kind, kindOK := question["kind"].(string)
		workflow, workflowOK := boundedAnyJevBenchmarkString(question["workflow"], 256)
		optionCount, optionOK := boundedAnyJevInteger(question["k"], 2, 26)
		majority, majorityOK := boundedAnyJevNumber(question["majority"], 0, 1)
		entropy, entropyOK := boundedAnyJevNumber(question["label_entropy"], 0, 1)
		if !kindOK || !anyJevTypedQuestionKinds[kind] || !workflowOK || !optionOK || !majorityOK || !entropyOK {
			return nil, 0, errors.New("typed AnyJev per-question metadata is invalid")
		}
		if name, exists := question["name"]; exists {
			if _, valid := boundedAnyJevBenchmarkString(name, 512); !valid {
				return nil, 0, errors.New("typed AnyJev question name is invalid")
			}
		}
		metricFields := make(map[string]any)
		for name := range anyJevTypedMetricBounds {
			if raw, exists := question[name]; exists {
				metricFields[name] = raw
			}
		}
		metricFields["n"] = question["n"]
		metrics, err := normalizeAnyJevTypedMetrics(metricFields)
		if err != nil {
			return nil, 0, err
		}
		total += metrics.N
		out = append(out, anyJevTypedQuestionMetrics{
			QuestionKeySHA256: hash, WorkflowSHA256: anyJevStringSHA256(workflow),
			Kind: kind, OptionCount: optionCount, Metrics: metrics,
			Majority: majority, LabelEntropy: entropy,
		})
	}
	return out, total, nil
}

func anyJevTypedOmittedFields() []string {
	return []string{
		"model_label", "provider_label", "checkpoint_label", "dataset_label",
		"workflow_labels", "question_labels", "environment_details", "timestamps",
		"timing_metrics", "raw_item_distributions",
	}
}

func typedAnyJevBenchmarkItem(payload anyJevTypedBenchmarkImport) (normalizedItem, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("typed AnyJev benchmark projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}
