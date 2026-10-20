package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevExitArtifactResultSchema = "threadforge.anyjev-exit-artifact-validation.v2"

const (
	anyJevExitArtifactFixedSelectionMode = "fixed_global_block"
	anyJevExitArtifactCVSelectionMode    = "per_question_cross_validation"
)

var anyJevExitArtifactRootFields = map[string]bool{
	"candidates": true, "date": true, "env": true, "model": true,
	"n_blocks": true, "summary": true, "validation": true,
}

type anyJevExitArtifactImport struct {
	SchemaVersion       string                       `json:"schema_version"`
	ModelSHA256         string                       `json:"model_sha256"`
	TransformerLayers   int                          `json:"transformer_layers"`
	CandidateLayers     []int                        `json:"candidate_layers"`
	SelectionMode       string                       `json:"selection_mode"`
	Validation          anyJevBundleValidationImport `json:"validation"`
	OmittedFields       []string                     `json:"omitted_fields"`
	HumanReviewRequired bool                         `json:"human_review_required"`
	ExecutionVerified   bool                         `json:"execution_verified"`
	CutoverEligible     bool                         `json:"cutover_evidence_eligible"`
}

func isAnyJevExitArtifactJSONPath(relative string) bool {
	_, ok := anyJevExitArtifactSelectionMode(relative)
	return ok
}

func anyJevExitArtifactSelectionMode(relative string) (string, bool) {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	if !strings.HasPrefix(path, "bench/results_exit/") {
		return "", false
	}
	switch {
	case strings.HasSuffix(path, ".artifact_cv.json"):
		return anyJevExitArtifactCVSelectionMode, true
	case strings.HasSuffix(path, ".artifact.json"):
		return anyJevExitArtifactFixedSelectionMode, true
	default:
		return "", false
	}
}

func normalizeAnyJevExitArtifactResult(value any, selectionMode string) (normalizedItem, error) {
	if selectionMode != anyJevExitArtifactFixedSelectionMode && selectionMode != anyJevExitArtifactCVSelectionMode {
		return normalizedItem{}, errors.New("unsupported AnyJev exit artifact selection mode")
	}
	root, ok := value.(map[string]any)
	if !ok || len(root) != len(anyJevExitArtifactRootFields) {
		return normalizedItem{}, errors.New("unsupported AnyJev exit artifact result shape")
	}
	for key := range root {
		if !anyJevExitArtifactRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev exit artifact field %q", key)
		}
	}
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	date, dateOK := boundedAnyJevBenchmarkString(root["date"], 64)
	if !modelOK || !dateOK || !validAnyJevLatencyTimestamp(date) {
		return normalizedItem{}, errors.New("AnyJev exit artifact model or timestamp is invalid")
	}
	env, envOK := root["env"].(map[string]any)
	if !envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev exit artifact environment metadata is invalid")
	}
	nBlocks, blocksOK := boundedAnyJevInteger(root["n_blocks"], 1, 4096)
	if !blocksOK {
		return normalizedItem{}, errors.New("AnyJev exit artifact transformer block count is invalid")
	}
	candidates, err := normalizeAnyJevExitArtifactCandidates(root["candidates"], nBlocks)
	if err != nil {
		return normalizedItem{}, err
	}
	if selectionMode == anyJevExitArtifactCVSelectionMode && len(candidates) < 2 {
		return normalizedItem{}, errors.New("AnyJev per-question cross-validation requires multiple candidate layers")
	}
	validationRows, ok := root["validation"].(map[string]any)
	if !ok || len(validationRows) == 0 || len(validationRows) > 100000 {
		return normalizedItem{}, errors.New("AnyJev exit artifact validation rows are invalid")
	}
	keys := sortedKeys(validationRows)
	for _, key := range keys {
		if !strings.HasPrefix(key, "typed.") && !strings.HasPrefix(key, "bench.") {
			return normalizedItem{}, errors.New("AnyJev exit artifact validation row family is unsupported")
		}
	}
	validationValue := map[string]any{"per_question": validationRows, "summary": root["summary"]}
	validation, err := normalizeAnyJevValidation(validationValue, nBlocks)
	if err != nil {
		return normalizedItem{}, fmt.Errorf("AnyJev exit artifact validation: %w", err)
	}
	if err := reconcileAnyJevExitArtifactSummary(validation, keys); err != nil {
		return normalizedItem{}, err
	}
	candidateSet := make(map[int]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateSet[candidate] = struct{}{}
	}
	selectedLayers := make(map[int]struct{}, len(validation.PerQuestion))
	for _, row := range validation.PerQuestion {
		if _, exists := candidateSet[row.Layer]; !exists {
			return normalizedItem{}, errors.New("AnyJev exit artifact selected layer is outside its candidate set")
		}
		if absAnyJevFloat(row.RelativeDepth-float64(row.Layer)/float64(nBlocks)) > 2e-6 {
			return normalizedItem{}, errors.New("AnyJev exit artifact relative depth does not match its selected layer")
		}
		selectedLayers[row.Layer] = struct{}{}
	}
	if selectionMode == anyJevExitArtifactFixedSelectionMode && len(selectedLayers) != 1 {
		return normalizedItem{}, errors.New("AnyJev fixed-block validation rows do not share one selected layer")
	}
	if selectionMode == anyJevExitArtifactCVSelectionMode && len(selectedLayers) < 2 {
		return normalizedItem{}, errors.New("AnyJev per-question cross-validation did not select multiple candidate layers")
	}
	payload := anyJevExitArtifactImport{
		SchemaVersion: anyJevExitArtifactResultSchema, ModelSHA256: anyJevStringSHA256(model),
		TransformerLayers: nBlocks, CandidateLayers: candidates, SelectionMode: selectionMode, Validation: *validation,
		OmittedFields:       []string{"model_label", "date", "environment_details", "validation_row_labels"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev exit artifact projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevExitArtifactCandidates(value any, nBlocks int) ([]int, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > nBlocks+1 || len(raw) > 4097 {
		return nil, errors.New("AnyJev exit artifact candidate layers are invalid")
	}
	candidates := make([]int, 0, len(raw))
	seen := make(map[int]struct{}, len(raw))
	for _, entry := range raw {
		layer, ok := boundedAnyJevInteger(entry, 0, nBlocks)
		if !ok {
			return nil, errors.New("AnyJev exit artifact candidate layer is invalid")
		}
		if _, duplicate := seen[layer]; duplicate {
			return nil, errors.New("AnyJev exit artifact candidate layers contain duplicates")
		}
		seen[layer] = struct{}{}
		candidates = append(candidates, layer)
	}
	sort.Ints(candidates)
	return candidates, nil
}

func reconcileAnyJevExitArtifactSummary(validation *anyJevBundleValidationImport, sourceKeys []string) error {
	if validation == nil || len(sourceKeys) != len(validation.PerQuestion) || len(sourceKeys) == 0 {
		return errors.New("AnyJev exit artifact validation summary has no rows")
	}
	var totalN, typedN int
	var accuracySum, nllSum, typedAccuracySum, typedNLLSum float64
	var meanBlocks, meanMS float64
	var cacheSum float64
	var cacheCount int
	typedRows := 0
	for index, row := range validation.PerQuestion {
		key := sourceKeys[index]
		if row.TestExamples <= 0 {
			return errors.New("AnyJev exit artifact validation row has no test examples")
		}
		totalN += row.TestExamples
		accuracySum += row.Accuracy * float64(row.TestExamples)
		nllSum += row.NLL * float64(row.TestExamples)
		meanBlocks += float64(row.Layer)
		meanMS += row.MillisecondsPerDecision
		if strings.HasPrefix(key, "typed.") {
			typedRows++
			typedN += row.TestExamples
			typedAccuracySum += row.Accuracy * float64(row.TestExamples)
			typedNLLSum += row.NLL * float64(row.TestExamples)
		}
		if row.CacheMeanAbsDP != nil {
			if row.CacheArgmaxDisagreement == nil {
				return errors.New("AnyJev exit artifact cache metrics are not paired")
			}
			cacheSum += *row.CacheMeanAbsDP
			cacheCount++
		} else if row.CacheArgmaxDisagreement != nil {
			return errors.New("AnyJev exit artifact cache metrics are not paired")
		}
	}
	summary := validation.Summary
	if totalN != summary.AllPooled.N || typedRows != 0 && summary.TypedPooled == nil || typedRows == 0 && summary.TypedPooled != nil {
		return errors.New("AnyJev exit artifact pooled example counts do not reconcile")
	}
	if absAnyJevFloat(summary.AllPooled.Accuracy-accuracySum/float64(totalN)) > 2e-6 ||
		absAnyJevFloat(summary.AllPooled.NLL-nllSum/float64(totalN)) > 2e-6 {
		return errors.New("AnyJev exit artifact pooled accuracy or NLL does not reconcile")
	}
	if summary.TypedPooled != nil {
		if typedN != summary.TypedPooled.N ||
			absAnyJevFloat(summary.TypedPooled.Accuracy-typedAccuracySum/float64(typedN)) > 2e-6 ||
			absAnyJevFloat(summary.TypedPooled.NLL-typedNLLSum/float64(typedN)) > 2e-6 {
			return errors.New("AnyJev exit artifact typed pooled metrics do not reconcile")
		}
	}
	count := float64(len(validation.PerQuestion))
	if absAnyJevFloat(summary.MeanBlocks-meanBlocks/count) > 2e-6 || absAnyJevFloat(summary.MeanMilliseconds-meanMS/count) > 2e-6 {
		return errors.New("AnyJev exit artifact mean layer or latency does not reconcile")
	}
	if cacheCount == 0 || summary.CacheMeanAbsDP == 0 && cacheSum != 0 ||
		absAnyJevFloat(summary.CacheMeanAbsDP-cacheSum/float64(cacheCount)) > 2e-6 {
		return errors.New("AnyJev exit artifact cache comparison summary does not reconcile")
	}
	return nil
}
