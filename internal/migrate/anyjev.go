package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/decision"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	anyJevCalibrationImportSchema  = "threadforge.anyjev-calibration-import.v1"
	anyJevAdaptationImportSchema   = "threadforge.anyjev-adaptation-import.v1"
	anyJevObservationsImportSchema = "threadforge.anyjev-observations-import.v1"
	anyJevBundleMetadataSchema     = "threadforge.anyjev-bundle-metadata.v1"
)

type anyJevBundleMetadataImport struct {
	SchemaVersion    string                        `json:"schema_version"`
	ModelSHA256      string                        `json:"model_sha256"`
	AnyJevVersion    string                        `json:"anyjev_version,omitempty"`
	NBlocks          int                           `json:"n_blocks"`
	HiddenSize       int                           `json:"hidden_size"`
	CandidateLayers  []int                         `json:"candidate_layers"`
	ArtifactCount    int                           `json:"artifact_count"`
	HeadCount        int                           `json:"head_count"`
	AdaptationCount  int                           `json:"adaptation_count"`
	ObservationCount int                           `json:"observation_count"`
	QuestionBindings []anyJevQuestionBinding       `json:"question_bindings"`
	Validation       *anyJevBundleValidationImport `json:"validation,omitempty"`
	OmittedFields    []string                      `json:"omitted_fields"`
}

type anyJevQuestionBinding struct {
	QuestionKey      string `json:"question_key"`
	QuestionIDSHA256 string `json:"question_id_sha256"`
}

type anyJevBundleValidationImport struct {
	PerQuestion []anyJevQuestionValidationImport `json:"per_question"`
	Summary     anyJevValidationSummaryImport    `json:"summary"`
}

type anyJevQuestionValidationImport struct {
	RowKeySHA256            string   `json:"row_key_sha256"`
	K                       int      `json:"k"`
	CalibrationExamples     int      `json:"calibration_examples"`
	TestExamples            int      `json:"test_examples"`
	Layer                   int      `json:"layer"`
	RelativeDepth           float64  `json:"relative_depth"`
	Method                  string   `json:"method"`
	OOFAccuracy             float64  `json:"oof_accuracy"`
	OOFNLL                  float64  `json:"oof_nll"`
	Temperature             float64  `json:"temperature"`
	Accuracy                float64  `json:"accuracy"`
	ECE                     float64  `json:"ece"`
	NLL                     float64  `json:"nll"`
	FitSeconds              float64  `json:"fit_seconds"`
	MillisecondsPerDecision float64  `json:"milliseconds_per_decision"`
	CacheMeanAbsDP          *float64 `json:"cache_mean_abs_dp,omitempty"`
	CacheArgmaxDisagreement *float64 `json:"cache_argmax_disagreement,omitempty"`
}

type anyJevPooledValidationImport struct {
	N        int     `json:"n"`
	Accuracy float64 `json:"accuracy"`
	ECE      float64 `json:"ece"`
	NLL      float64 `json:"nll"`
}

type anyJevValidationSummaryImport struct {
	TypedPooled      *anyJevPooledValidationImport `json:"typed_pooled"`
	AllPooled        anyJevPooledValidationImport  `json:"all_pooled"`
	MeanBlocks       float64                       `json:"mean_blocks"`
	MeanMilliseconds float64                       `json:"mean_milliseconds_per_decision"`
	CacheMeanAbsDP   float64                       `json:"cache_mean_abs_dp"`
}

type anyJevCalibrationImport struct {
	SchemaVersion       string          `json:"schema_version"`
	Model               string          `json:"model"`
	QuestionKey         string          `json:"question_key"`
	Level               string          `json:"level"`
	CalibrationExamples int             `json:"calibration_examples"`
	ContentSHA256       string          `json:"content_sha256"`
	Content             json.RawMessage `json:"content"`
}

type anyJevAdaptationImport struct {
	SchemaVersion string `json:"schema_version"`
	Model         string `json:"model"`
	QuestionKey   string `json:"question_key"`
	Statistics    any    `json:"statistics"`
}

type anyJevObservationsImport struct {
	SchemaVersion string `json:"schema_version"`
	Model         string `json:"model"`
	QuestionKey   string `json:"question_key"`
	Observation   any    `json:"observation"`
}

func normalizeAnyJevItems(relative string, value any) ([]normalizedItem, bool, error) {
	if isAnyJevDistillationSyntheticJSONPath(relative) {
		item, err := normalizeAnyJevDistillationSynthetic(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevHeadStudyJSONPath(relative) {
		item, err := normalizeAnyJevHeadStudyResult(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevZeroLabelDistillationJSONPath(relative) {
		item, err := normalizeAnyJevZeroLabelDistillationResult(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevDistillationJSONPath(relative) {
		item, err := normalizeAnyJevDistillationResult(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevLatencyMarkdownPath(relative) {
		item, err := normalizeAnyJevLatencyMarkdown(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevTypedMarkdownPath(relative) {
		item, err := normalizeAnyJevTypedMarkdown(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevBenchmarkMarkdownPath(relative) {
		item, err := normalizeAnyJevBenchmarkMarkdown(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevDemoJevModeJSONPath(relative) {
		item, err := normalizeAnyJevDemoJevModeResult(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevDemoGameJSONPath(relative) {
		item, err := normalizeAnyJevDemoGameResult(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevUniversalResultJSONPath(relative) {
		item, err := normalizeAnyJevUniversalResult(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevItemDumpJSONPath(relative) {
		item, err := normalizeAnyJevItemDump(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevParaphraseOrderJSONPath(relative) {
		item, err := normalizeAnyJevParaphraseOrderResult(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevExitCascadeJSONPath(relative) {
		item, err := normalizeAnyJevExitCascadeResult(value)
		return []normalizedItem{item}, true, err
	}
	if selectionMode, ok := anyJevExitArtifactSelectionMode(relative); ok {
		item, err := normalizeAnyJevExitArtifactResult(value, selectionMode)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevNativeMazeJSONPath(relative) {
		item, err := normalizeAnyJevNativeMazeResult(value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevExitLabelsJSONPath(relative) {
		item, err := normalizeAnyJevExitLabelsResult(value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevExitJevModeJSONPath(relative) {
		item, err := normalizeAnyJevExitJevModeResult(value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevExitLatencyJSONPath(relative) {
		item, err := normalizeAnyJevExitLatencyResult(relative, value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevExitDepthJSONPath(relative) {
		item, err := normalizeAnyJevExitDepthResult(value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevNanoJevMazeJSONPath(relative) {
		item, err := normalizeAnyJevNanoJevMazeResult(value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevLatencyBenchmarkJSONPath(relative) {
		item, err := normalizeAnyJevLatencyBenchmarkResult(value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevTypedBenchmarkJSONPath(relative) {
		item, err := normalizeAnyJevTypedBenchmarkResult(value)
		return []normalizedItem{item}, true, err
	}
	if isAnyJevBenchmarkJSONPath(relative) {
		item, err := normalizeAnyJevBenchmarkResult(value)
		return []normalizedItem{item}, true, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false, nil
	}
	lowerPath := "/" + strings.ToLower(strings.Trim(relative, "/")) + "/"
	inBundleDirectory := strings.Contains(lowerPath, "/anyjev-heads/")
	if inBundleDirectory {
		if _, hasMethod := object["method"]; !hasMethod {
			items, err := normalizeAnyJevBundle(object)
			return items, true, err
		}
	}
	if _, hasMethod := object["method"]; hasMethod {
		item, handled, err := normalizeAnyJevCalibration(object, "", "")
		if handled || err != nil {
			return []normalizedItem{item}, true, err
		}
		if object["model"] != nil && object["question"] != nil {
			return nil, true, errors.New("unsupported AnyJev calibration method")
		}
	}
	return nil, false, nil
}

func normalizeAnyJevBundle(bundle map[string]any) ([]normalizedItem, error) {
	allowed := map[string]bool{
		"model": true, "artifacts": true, "heads": true, "adaptation": true, "observations": true,
		"anyjev": true, "n_blocks": true, "hidden_size": true, "candidates": true,
		"questions": true, "validation": true, "date": true, "env": true,
	}
	for key := range bundle {
		if !allowed[key] {
			return nil, fmt.Errorf("unsupported AnyJev bundle field %q", key)
		}
	}
	model, ok := bundle["model"].(string)
	if !ok || strings.TrimSpace(model) == "" {
		return nil, errors.New("AnyJev artifact bundle requires a model identifier")
	}
	items := make([]normalizedItem, 0)
	for _, group := range []struct {
		name  string
		level string
	}{{"artifacts", "L1"}, {"heads", "L2"}} {
		value, exists := bundle[group.name]
		if !exists {
			continue
		}
		entries, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("AnyJev %s must be an object keyed by question hash", group.name)
		}
		keys := sortedKeys(entries)
		for _, questionKey := range keys {
			artifact, ok := entries[questionKey].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("AnyJev %s entry %q must be an object", group.name, questionKey)
			}
			item, handled, err := normalizeAnyJevCalibration(artifact, model, group.level)
			if err != nil || !handled {
				if err == nil {
					err = errors.New("unrecognized calibration artifact")
				}
				return nil, fmt.Errorf("AnyJev %s entry %q: %w", group.name, questionKey, err)
			}
			var payload anyJevCalibrationImport
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				return nil, err
			}
			if payload.QuestionKey != questionKey {
				return nil, fmt.Errorf("AnyJev %s entry key does not match its embedded question hash", group.name)
			}
			items = append(items, item)
		}
	}
	if value, exists := bundle["adaptation"]; exists {
		entries, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("AnyJev adaptation must be an object keyed by question hash")
		}
		for _, questionKey := range sortedKeys(entries) {
			if !validAnyJevQuestionKey(questionKey) {
				return nil, errors.New("AnyJev adaptation contains an invalid question key")
			}
			statistics, ok := entries[questionKey].(map[string]any)
			if !ok || statistics["sum"] == nil || statistics["sumsq"] == nil || !positiveInteger(statistics["n"]) {
				return nil, fmt.Errorf("AnyJev adaptation entry %q has an invalid shape", questionKey)
			}
			payload, err := json.Marshal(anyJevAdaptationImport{
				SchemaVersion: anyJevAdaptationImportSchema, Model: model,
				QuestionKey: questionKey, Statistics: statistics,
			})
			if err != nil {
				return nil, err
			}
			items = append(items, normalizedItem{Type: "decision_adaptation_state", Payload: payload})
		}
	}
	if value, exists := bundle["observations"]; exists {
		entries, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("AnyJev observations must be an object keyed by question hash")
		}
		for _, questionKey := range sortedKeys(entries) {
			if !validAnyJevQuestionKey(questionKey) {
				return nil, errors.New("AnyJev observations contain an invalid question key")
			}
			observation, ok := entries[questionKey].(map[string]any)
			if !ok || !validAnyJevObservation(observation) {
				return nil, fmt.Errorf("AnyJev observation entry %q has an invalid shape", questionKey)
			}
			payload, err := json.Marshal(anyJevObservationsImport{
				SchemaVersion: anyJevObservationsImportSchema, Model: model,
				QuestionKey: questionKey, Observation: observation,
			})
			if err != nil {
				return nil, err
			}
			items = append(items, normalizedItem{Type: "decision_label_or_evaluation", Payload: payload})
		}
	}
	metadata, hasMetadata, err := normalizeAnyJevBundleMetadata(bundle, model)
	if err != nil {
		return nil, err
	}
	if hasMetadata {
		items = append(items, metadata)
	}
	if len(items) == 0 {
		return nil, errors.New("AnyJev bundle contains no supported artifacts or learning state")
	}
	return items, nil
}

func normalizeAnyJevBundleMetadata(bundle map[string]any, model string) (normalizedItem, bool, error) {
	metadataKeys := []string{"anyjev", "n_blocks", "hidden_size", "candidates", "questions", "validation", "date", "env"}
	hasMetadata := false
	for _, key := range metadataKeys {
		if _, exists := bundle[key]; exists {
			hasMetadata = true
		}
	}
	if !hasMetadata {
		return normalizedItem{}, false, nil
	}
	for _, key := range metadataKeys {
		if _, exists := bundle[key]; !exists {
			return normalizedItem{}, true, errors.New("AnyJev bundle metadata is incomplete")
		}
	}
	if len(model) > 512 || privacy.ContainsCredentialText(model) {
		return normalizedItem{}, true, errors.New("AnyJev bundle model metadata is unsafe")
	}
	version, ok := bundle["anyjev"].(string)
	if !ok || len(version) == 0 || len(version) > 64 || privacy.ContainsCredentialText(version) {
		return normalizedItem{}, true, errors.New("AnyJev bundle version metadata is invalid")
	}
	nBlocks, ok := boundedAnyJevInteger(bundle["n_blocks"], 1, 4096)
	if !ok {
		return normalizedItem{}, true, errors.New("AnyJev bundle block count is invalid")
	}
	hiddenSize, ok := boundedAnyJevInteger(bundle["hidden_size"], 1, 65536)
	if !ok {
		return normalizedItem{}, true, errors.New("AnyJev bundle hidden size is invalid")
	}
	candidateValues, ok := bundle["candidates"].([]any)
	if !ok || len(candidateValues) == 0 || len(candidateValues) > 4096 {
		return normalizedItem{}, true, errors.New("AnyJev bundle candidate layers are invalid")
	}
	candidates := make([]int, 0, len(candidateValues))
	seenCandidates := make(map[int]bool, len(candidateValues))
	for _, value := range candidateValues {
		layer, valid := boundedAnyJevInteger(value, 0, nBlocks)
		if !valid || seenCandidates[layer] {
			return normalizedItem{}, true, errors.New("AnyJev bundle candidate layers are invalid")
		}
		seenCandidates[layer] = true
		candidates = append(candidates, layer)
	}
	sort.Ints(candidates)
	questionValues, ok := bundle["questions"].(map[string]any)
	if !ok || len(questionValues) > 100000 {
		return normalizedItem{}, true, errors.New("AnyJev bundle question bindings are invalid")
	}
	questionKeys := sortedKeys(questionValues)
	questionBindings := make([]anyJevQuestionBinding, 0, len(questionKeys))
	for _, questionKey := range questionKeys {
		questionID, valid := questionValues[questionKey].(string)
		if !validAnyJevQuestionKey(questionKey) || !valid || len(questionID) == 0 || len(questionID) > 256 || privacy.ContainsCredentialText(questionID) {
			return normalizedItem{}, true, errors.New("AnyJev bundle question binding is invalid")
		}
		questionIDHash := sha256.Sum256([]byte(questionID))
		questionBindings = append(questionBindings, anyJevQuestionBinding{
			QuestionKey: questionKey, QuestionIDSHA256: hex.EncodeToString(questionIDHash[:]),
		})
	}
	validation, ok := bundle["validation"].(map[string]any)
	if !ok {
		return normalizedItem{}, true, errors.New("AnyJev bundle validation metadata is invalid")
	}
	normalizedValidation, err := normalizeAnyJevValidation(validation, nBlocks)
	if err != nil {
		return normalizedItem{}, true, err
	}
	for _, optional := range []string{"date"} {
		if value, exists := bundle[optional]; exists {
			text, valid := value.(string)
			if !valid || len(text) > 128 {
				return normalizedItem{}, true, errors.New("AnyJev bundle timestamp metadata is invalid")
			}
		}
	}
	if value, exists := bundle["env"]; exists {
		env, valid := value.(map[string]any)
		if !valid || len(env) > 256 {
			return normalizedItem{}, true, errors.New("AnyJev bundle environment metadata is invalid")
		}
	}
	artifactCount, err := anyJevCollectionCount(bundle, "artifacts")
	if err != nil {
		return normalizedItem{}, true, err
	}
	headCount, err := anyJevCollectionCount(bundle, "heads")
	if err != nil {
		return normalizedItem{}, true, err
	}
	adaptationCount, err := anyJevCollectionCount(bundle, "adaptation")
	if err != nil {
		return normalizedItem{}, true, err
	}
	observationCount, err := anyJevCollectionCount(bundle, "observations")
	if err != nil {
		return normalizedItem{}, true, err
	}
	modelHash := sha256.Sum256([]byte(model))
	payload, err := json.Marshal(anyJevBundleMetadataImport{
		SchemaVersion: anyJevBundleMetadataSchema, ModelSHA256: hex.EncodeToString(modelHash[:]),
		AnyJevVersion: version, NBlocks: nBlocks, HiddenSize: hiddenSize, CandidateLayers: candidates,
		ArtifactCount: artifactCount, HeadCount: headCount, AdaptationCount: adaptationCount,
		ObservationCount: observationCount, QuestionBindings: questionBindings, Validation: normalizedValidation,
		OmittedFields: []string{"model_label", "date", "env", "question_ids", "validation_row_keys"},
	})
	if err != nil {
		return normalizedItem{}, true, err
	}
	return normalizedItem{Type: "decision_bundle_metadata", Payload: payload}, true, nil
}

func normalizeAnyJevValidation(source map[string]any, nBlocks int) (*anyJevBundleValidationImport, error) {
	if len(source) != 2 {
		return nil, errors.New("AnyJev bundle validation metadata has unsupported fields")
	}
	perQuestion, ok := source["per_question"].(map[string]any)
	if !ok || len(perQuestion) == 0 || len(perQuestion) > 100000 {
		return nil, errors.New("AnyJev per-question validation metadata is invalid")
	}
	rows := make([]anyJevQuestionValidationImport, 0, len(perQuestion))
	for _, rowKey := range sortedKeys(perQuestion) {
		row, ok := perQuestion[rowKey].(map[string]any)
		if !ok || privacy.ContainsCredentialText(rowKey) {
			return nil, errors.New("AnyJev validation row is invalid")
		}
		converted, err := normalizeAnyJevValidationRow(rowKey, row, nBlocks)
		if err != nil {
			return nil, err
		}
		rows = append(rows, converted)
	}
	summary, ok := source["summary"].(map[string]any)
	if !ok || len(summary) != 5 {
		return nil, errors.New("AnyJev validation summary is invalid")
	}
	allPooled, err := normalizeAnyJevPooled(summary["all_pooled"])
	if err != nil {
		return nil, err
	}
	var typedPooled *anyJevPooledValidationImport
	if rawTyped := summary["typed_pooled"]; rawTyped != nil {
		typed, err := normalizeAnyJevPooled(rawTyped)
		if err != nil {
			return nil, err
		}
		typedPooled = &typed
	}
	meanBlocks, ok := boundedAnyJevNumber(summary["mean_blocks"], 0, float64(nBlocks))
	if !ok {
		return nil, errors.New("AnyJev validation mean block count is invalid")
	}
	meanMilliseconds, ok := boundedAnyJevNumber(summary["mean_ms_per_decision"], 0, 1e9)
	if !ok {
		return nil, errors.New("AnyJev validation timing summary is invalid")
	}
	cacheMeanAbsDP, ok := boundedAnyJevNumber(summary["cache_mean_abs_dp"], 0, 1)
	if !ok {
		return nil, errors.New("AnyJev cache comparison summary is invalid")
	}
	return &anyJevBundleValidationImport{
		PerQuestion: rows,
		Summary: anyJevValidationSummaryImport{
			TypedPooled: typedPooled, AllPooled: allPooled, MeanBlocks: meanBlocks,
			MeanMilliseconds: meanMilliseconds, CacheMeanAbsDP: cacheMeanAbsDP,
		},
	}, nil
}

func normalizeAnyJevValidationRow(rowKey string, row map[string]any, nBlocks int) (anyJevQuestionValidationImport, error) {
	allowed := map[string]bool{
		"K": true, "n_calib": true, "n_test": true, "layer_abs": true, "relative_depth": true,
		"method": true, "oof_acc": true, "oof_nll": true, "temperature": true, "acc": true,
		"ece": true, "nll": true, "fit_seconds": true, "ms_per_decision": true,
		"cache_mean_abs_dp": true, "cache_argmax_disagreement": true,
	}
	for key := range row {
		if !allowed[key] {
			return anyJevQuestionValidationImport{}, errors.New("AnyJev validation row contains an unsupported field")
		}
	}
	k, kOK := boundedAnyJevInteger(row["K"], 2, 26)
	calibrationExamples, calibrationOK := boundedAnyJevInteger(row["n_calib"], 2, 10000000)
	testExamples, testOK := boundedAnyJevInteger(row["n_test"], 1, 10000000)
	layer, layerOK := boundedAnyJevInteger(row["layer_abs"], 0, nBlocks)
	depth, depthOK := boundedAnyJevNumber(row["relative_depth"], 0, 1)
	method, methodOK := row["method"].(string)
	if !kOK || !calibrationOK || !testOK || !layerOK || !depthOK || !methodOK || !validAnyJevHeadMethod(method) || calibrationExamples < k*2 {
		return anyJevQuestionValidationImport{}, errors.New("AnyJev validation row has invalid dimensions or method")
	}
	metric := func(key string, minimum, maximum float64) (float64, error) {
		value, valid := boundedAnyJevNumber(row[key], minimum, maximum)
		if !valid {
			return 0, fmt.Errorf("AnyJev validation metric %q is invalid", key)
		}
		return value, nil
	}
	oofAccuracy, err := metric("oof_acc", 0, 1)
	if err != nil {
		return anyJevQuestionValidationImport{}, err
	}
	oofNLL, err := metric("oof_nll", 0, 1e6)
	if err != nil {
		return anyJevQuestionValidationImport{}, err
	}
	temperature, err := metric("temperature", 1e-9, 1e6)
	if err != nil {
		return anyJevQuestionValidationImport{}, err
	}
	accuracy, err := metric("acc", 0, 1)
	if err != nil {
		return anyJevQuestionValidationImport{}, err
	}
	ece, err := metric("ece", 0, 1)
	if err != nil {
		return anyJevQuestionValidationImport{}, err
	}
	nll, err := metric("nll", 0, 1e6)
	if err != nil {
		return anyJevQuestionValidationImport{}, err
	}
	fitSeconds, err := metric("fit_seconds", 0, 1e9)
	if err != nil {
		return anyJevQuestionValidationImport{}, err
	}
	milliseconds, err := metric("ms_per_decision", 0, 1e9)
	if err != nil {
		return anyJevQuestionValidationImport{}, err
	}
	rowHash := sha256.Sum256([]byte(rowKey))
	converted := anyJevQuestionValidationImport{
		RowKeySHA256: hex.EncodeToString(rowHash[:]), K: k, CalibrationExamples: calibrationExamples,
		TestExamples: testExamples, Layer: layer, RelativeDepth: depth, Method: method,
		OOFAccuracy: oofAccuracy, OOFNLL: oofNLL, Temperature: temperature, Accuracy: accuracy,
		ECE: ece, NLL: nll, FitSeconds: fitSeconds, MillisecondsPerDecision: milliseconds,
	}
	for key, destination := range map[string]**float64{
		"cache_mean_abs_dp":         &converted.CacheMeanAbsDP,
		"cache_argmax_disagreement": &converted.CacheArgmaxDisagreement,
	} {
		if raw, exists := row[key]; exists {
			value, valid := boundedAnyJevNumber(raw, 0, 1)
			if !valid {
				return anyJevQuestionValidationImport{}, fmt.Errorf("AnyJev validation metric %q is invalid", key)
			}
			*destination = &value
		}
	}
	return converted, nil
}

func normalizeAnyJevPooled(value any) (anyJevPooledValidationImport, error) {
	pooled, ok := value.(map[string]any)
	if !ok || len(pooled) != 4 {
		return anyJevPooledValidationImport{}, errors.New("AnyJev pooled validation metrics are invalid")
	}
	n, nOK := boundedAnyJevInteger(pooled["n"], 1, 100000000)
	accuracy, accuracyOK := boundedAnyJevNumber(pooled["acc"], 0, 1)
	ece, eceOK := boundedAnyJevNumber(pooled["ece"], 0, 1)
	nll, nllOK := boundedAnyJevNumber(pooled["nll"], 0, 1e6)
	if !nOK || !accuracyOK || !eceOK || !nllOK {
		return anyJevPooledValidationImport{}, errors.New("AnyJev pooled validation metrics are invalid")
	}
	return anyJevPooledValidationImport{N: n, Accuracy: accuracy, ECE: ece, NLL: nll}, nil
}

func anyJevCollectionCount(bundle map[string]any, key string) (int, error) {
	value, exists := bundle[key]
	if !exists {
		return 0, nil
	}
	collection, ok := value.(map[string]any)
	if !ok || len(collection) > 100000 {
		return 0, fmt.Errorf("AnyJev %s collection is invalid", key)
	}
	return len(collection), nil
}

func boundedAnyJevInteger(value any, minimum, maximum int) (int, bool) {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < float64(minimum) || number > float64(maximum) || number != math.Trunc(number) {
		return 0, false
	}
	return int(number), true
}

func boundedAnyJevNumber(value any, minimum, maximum float64) (float64, bool) {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < minimum || number > maximum {
		return 0, false
	}
	return number, true
}

func validAnyJevHeadMethod(value string) bool {
	if !strings.HasPrefix(value, "head:") || len(value) > 64 || privacy.ContainsCredentialText(value) {
		return false
	}
	switch strings.TrimPrefix(value, "head:") {
	case "diff_means", "lda", "ridge", "rrr":
		return true
	default:
		return false
	}
}

func normalizeAnyJevCalibration(source map[string]any, bundleModel, expectedLevel string) (normalizedItem, bool, error) {
	method, ok := source["method"].(string)
	if !ok {
		return normalizedItem{}, false, nil
	}
	level := ""
	switch {
	case method == "temperature":
		level = "L1"
	case strings.HasPrefix(method, "head:"):
		level = "L2"
	default:
		return normalizedItem{}, false, nil
	}
	if expectedLevel != "" && expectedLevel != level {
		return normalizedItem{}, true, errors.New("artifact level does not match its AnyJev collection")
	}
	model, _ := source["model"].(string)
	questionKey, _ := source["question"].(string)
	if bundleModel != "" && model != bundleModel {
		return normalizedItem{}, true, errors.New("artifact model does not match its bundle")
	}
	content, err := json.Marshal(source)
	if err != nil {
		return normalizedItem{}, true, err
	}
	canonical, count, err := decision.ValidateCalibrationContent(content, model, questionKey, level)
	if err != nil {
		return normalizedItem{}, true, err
	}
	hash := sha256.Sum256(canonical)
	payload, err := json.Marshal(anyJevCalibrationImport{
		SchemaVersion: anyJevCalibrationImportSchema, Model: model, QuestionKey: questionKey,
		Level: level, CalibrationExamples: count, ContentSHA256: hex.EncodeToString(hash[:]), Content: canonical,
	})
	if err != nil {
		return normalizedItem{}, true, err
	}
	return normalizedItem{Type: "decision_calibration_artifact", Payload: payload}, true, nil
}

func validAnyJevObservation(observation map[string]any) bool {
	for _, key := range []string{"question_id", "kind", "text", "options", "states", "labels"} {
		if observation[key] == nil {
			return false
		}
	}
	options, ok := stringArray(observation["options"])
	if !ok || len(options) < 2 || len(options) > 26 {
		return false
	}
	states, ok := observation["states"].([]any)
	if !ok || len(states) > 100000 {
		return false
	}
	labels, ok := observation["labels"].([]any)
	if !ok || len(labels) != len(states) {
		return false
	}
	for _, label := range labels {
		if !validIntegerRange(label, len(options)) {
			return false
		}
	}
	for _, key := range []string{"question_id", "kind", "text"} {
		if value, ok := observation[key].(string); !ok || strings.TrimSpace(value) == "" || len(value) > 10000 {
			return false
		}
	}
	if len(observation["question_id"].(string)) > 128 {
		return false
	}
	if kind := observation["kind"].(string); kind != "choice" && kind != "score" && kind != "noul" {
		return false
	}
	for key, minimum := range map[string]float64{"fits": 0, "next_fit": 1} {
		if value, exists := observation[key]; exists {
			number, ok := value.(float64)
			if !ok || number < minimum || number != float64(int(number)) {
				return false
			}
		}
	}
	return true
}

func validAnyJevQuestionKey(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func stringArray(value any) ([]string, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > 1000 {
			return nil, false
		}
		result[i] = text
	}
	return result, true
}

func validIntegerRange(value any, upper int) bool {
	number, ok := value.(float64)
	return ok && number >= 0 && number < float64(upper) && number == float64(int(number))
}

func positiveInteger(value any) bool {
	number, ok := value.(float64)
	return ok && number >= 1 && number == float64(int(number))
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
