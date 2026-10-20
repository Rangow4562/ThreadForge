package migrate

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	anyJevItemDumpSchema      = "threadforge.anyjev-item-dump.v1"
	maxAnyJevDumpUnits        = 512
	maxAnyJevDumpItemsPerUnit = 10000
	maxAnyJevDumpItems        = 100000
	maxAnyJevDumpCells        = 4000000
)

type anyJevItemDumpImport struct {
	SchemaVersion       string               `json:"schema_version"`
	DatasetKind         string               `json:"dataset_kind"`
	ModelSHA256         string               `json:"model_sha256"`
	Units               []anyJevItemDumpUnit `json:"units"`
	OmittedFields       []string             `json:"omitted_fields"`
	HumanReviewRequired bool                 `json:"human_review_required"`
	ExecutionVerified   bool                 `json:"execution_verified"`
	CutoverEligible     bool                 `json:"cutover_evidence_eligible"`
}

type anyJevItemDumpUnit struct {
	UnitKeySHA256              string   `json:"unit_key_sha256"`
	WorkflowSHA256             string   `json:"workflow_sha256,omitempty"`
	Kind                       string   `json:"kind"`
	Options                    int      `json:"options"`
	Examples                   int      `json:"examples"`
	PermutationCount           int      `json:"permutation_count"`
	PermutationLayoutSHA256    string   `json:"permutation_layout_sha256"`
	RawFirstLayoutAccuracy     float64  `json:"raw_first_layout_accuracy"`
	PermutationMeanAccuracy    float64  `json:"permutation_mean_accuracy"`
	PermutationLogMeanAccuracy float64  `json:"permutation_logmean_accuracy"`
	PermutationLogMeanNLL      float64  `json:"permutation_logmean_nll"`
	PermutationLogMeanBrier    float64  `json:"permutation_logmean_brier"`
	PermutationFlipRate        float64  `json:"permutation_flip_rate"`
	SoftLabelLogMeanAccuracy   *float64 `json:"soft_label_logmean_accuracy,omitempty"`
	ContentFreePriorAvailable  bool     `json:"content_free_prior_available"`
}

func anyJevItemDumpPath(relative string) (datasetKind string, compressed bool, ok bool) {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 4 || !strings.EqualFold(parts[0], "bench") {
		return "", false, false
	}
	if _, err := parseAnyJevMigrationDate(parts[2]); err != nil {
		return "", false, false
	}
	switch strings.ToLower(parts[1]) {
	case "results_dump":
		datasetKind = "task_dump"
	case "results_typed_dump":
		datasetKind = "typed_question_dump"
	default:
		return "", false, false
	}
	name := strings.ToLower(parts[3])
	if strings.HasSuffix(name, ".items.json.gz") && len(name) > len(".items.json.gz") {
		return datasetKind, true, true
	}
	if strings.HasSuffix(name, ".items.json") && len(name) > len(".items.json") {
		return datasetKind, false, true
	}
	return "", false, false
}

func isAnyJevItemDumpJSONPath(relative string) bool {
	_, _, ok := anyJevItemDumpPath(relative)
	return ok
}

func isAnyJevItemDumpGzipPath(relative string) bool {
	_, compressed, ok := anyJevItemDumpPath(relative)
	return ok && compressed
}

func decodeAnyJevItemDumpGzipJSON(relative string, data []byte, expandedBudget int64) ([]byte, int64, error) {
	if !isAnyJevItemDumpGzipPath(relative) {
		return data, int64(len(data)), nil
	}
	if expandedBudget < 0 {
		return nil, 0, errors.New("migration source exceeds expanded-byte limit")
	}
	limit := int64(maxFileBytes)
	if expandedBudget < limit {
		limit = expandedBudget
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, 0, errors.New("invalid compressed AnyJev item-dump JSON")
	}
	decoded, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := reader.Close()
	expandedBytes := int64(len(decoded))
	if expandedBytes > limit {
		if expandedBudget < maxFileBytes {
			return nil, expandedBytes, errors.New("migration source exceeds 1 GiB expanded-byte limit")
		}
		return nil, expandedBytes, errors.New("expanded AnyJev item dump exceeds 32 MiB per-file limit")
	}
	if readErr != nil || closeErr != nil {
		return nil, expandedBytes, errors.New("invalid compressed AnyJev item-dump JSON")
	}
	return decoded, expandedBytes, nil
}

func normalizeAnyJevItemDump(relative string, value any) (normalizedItem, error) {
	datasetKind, _, recognized := anyJevItemDumpPath(relative)
	if !recognized {
		return normalizedItem{}, errors.New("unsupported AnyJev item-dump path")
	}
	root, ok := value.(map[string]any)
	if !ok || len(root) != 2 {
		return normalizedItem{}, errors.New("unsupported AnyJev item-dump root shape")
	}
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	if !modelOK || privacy.ContainsCredentialText(model) {
		return normalizedItem{}, errors.New("AnyJev item-dump model identifier is invalid")
	}
	filename := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	filename = filename[strings.LastIndex(filename, "/")+1:]
	modelSlug := strings.ToLower(strings.ReplaceAll(model, "/", "__"))
	if filename != modelSlug+".items.json" && filename != modelSlug+".items.json.gz" {
		return normalizedItem{}, errors.New("AnyJev item-dump filename does not match its model")
	}
	groupField := "tasks"
	if datasetKind == "typed_question_dump" {
		groupField = "questions"
	}
	for field := range root {
		if field != "model" && field != groupField {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev item-dump root field %q", field)
		}
	}
	groups, ok := root[groupField].(map[string]any)
	if !ok || len(groups) == 0 || len(groups) > maxAnyJevDumpUnits {
		return normalizedItem{}, errors.New("AnyJev item-dump groups are invalid")
	}
	projected := anyJevItemDumpImport{
		SchemaVersion: anyJevItemDumpSchema, DatasetKind: datasetKind,
		ModelSHA256: anyJevStringSHA256(model), Units: make([]anyJevItemDumpUnit, 0, len(groups)),
		OmittedFields: []string{
			"model_label", "task_and_question_labels", "display_names", "raw_probability_vectors",
			"gold_label_vectors", "soft_gold_vectors", "content_free_prior_vectors",
		},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	seenKeys := make(map[string]struct{}, len(groups))
	seenExamples := 0
	seenCells := int64(0)
	for groupKey, rawGroup := range groups {
		unit, err := normalizeAnyJevItemDumpUnit(groupKey, rawGroup, datasetKind == "typed_question_dump",
			maxAnyJevDumpItems-seenExamples, int64(maxAnyJevDumpCells)-seenCells)
		if err != nil {
			return normalizedItem{}, fmt.Errorf("invalid AnyJev item-dump unit: %w", err)
		}
		seenExamples += unit.Examples
		seenCells += anyJevItemDumpCellCount(unit)
		if seenExamples > maxAnyJevDumpItems || seenCells > maxAnyJevDumpCells {
			return normalizedItem{}, errors.New("AnyJev item dump exceeds aggregate item or probability-cell bounds")
		}
		if _, exists := seenKeys[unit.UnitKeySHA256]; exists {
			return normalizedItem{}, errors.New("AnyJev item-dump unit keys collide after hashing")
		}
		seenKeys[unit.UnitKeySHA256] = struct{}{}
		projected.Units = append(projected.Units, unit)
	}
	sort.Slice(projected.Units, func(i, j int) bool {
		return projected.Units[i].UnitKeySHA256 < projected.Units[j].UnitKeySHA256
	})
	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev item-dump projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevItemDumpUnit(groupKey string, value any, typed bool, remainingItems int, remainingCells int64) (anyJevItemDumpUnit, error) {
	if groupKey == "" || len(groupKey) > 256 {
		return anyJevItemDumpUnit{}, errors.New("unit identifier is invalid")
	}
	group, ok := value.(map[string]any)
	if !ok {
		return anyJevItemDumpUnit{}, errors.New("unit must be an object")
	}
	allowed := map[string]bool{
		"cf_prior": true, "gold": true, "k": true, "kind": true, "p_pos_raw": true, "perms": true,
	}
	if typed {
		allowed["gold_probs"], allowed["name"], allowed["workflow"] = true, true, true
	}
	if len(group) != len(allowed) {
		return anyJevItemDumpUnit{}, errors.New("unit is missing or has unexpected producer fields")
	}
	for field := range group {
		if !allowed[field] {
			return anyJevItemDumpUnit{}, fmt.Errorf("unsupported unit field %q", field)
		}
	}
	k, kOK := boundedAnyJevInteger(group["k"], 2, 26)
	kind, kindOK := group["kind"].(string)
	if !kOK || !kindOK || (kind != "choice" && kind != "noul" && kind != "score") {
		return anyJevItemDumpUnit{}, errors.New("unit kind or option count is invalid")
	}
	if typed {
		_, nameOK := boundedAnyJevBenchmarkString(group["name"], 256)
		_, workflowOK := boundedAnyJevBenchmarkString(group["workflow"], 256)
		if !nameOK || !workflowOK {
			return anyJevItemDumpUnit{}, errors.New("typed unit display metadata is invalid")
		}
	}
	perms, err := normalizeAnyJevItemDumpPermutations(group["perms"], k)
	if err != nil {
		return anyJevItemDumpUnit{}, err
	}
	permutationBytes, err := json.Marshal(perms)
	if err != nil {
		return anyJevItemDumpUnit{}, err
	}
	golds, ok := group["gold"].([]any)
	if !ok || len(golds) == 0 || len(golds) > maxAnyJevDumpItemsPerUnit {
		return anyJevItemDumpUnit{}, errors.New("gold label vector is invalid")
	}
	if len(golds) > remainingItems {
		return anyJevItemDumpUnit{}, errors.New("AnyJev item-dump unit exceeds remaining item or probability-cell bounds")
	}
	probabilityItems, ok := group["p_pos_raw"].([]any)
	if !ok || len(probabilityItems) != len(golds) {
		return anyJevItemDumpUnit{}, errors.New("raw probability item count does not match gold labels")
	}
	var softItems []any
	if typed && group["gold_probs"] != nil {
		var softOK bool
		softItems, softOK = group["gold_probs"].([]any)
		if !softOK || len(softItems) != len(golds) {
			return anyJevItemDumpUnit{}, errors.New("soft gold-label item count is invalid")
		}
	}
	contentFreeAvailable := group["cf_prior"] != nil
	if contentFreeAvailable {
		if err := validateAnyJevItemDumpPrior(group["cf_prior"], len(perms), k); err != nil {
			return anyJevItemDumpUnit{}, err
		}
	}
	estimatedCells := int64(len(golds)) * int64(len(perms)) * int64(k)
	if typed && group["gold_probs"] != nil {
		estimatedCells += int64(len(golds)) * int64(k)
	}
	if contentFreeAvailable {
		estimatedCells += int64(len(perms)) * int64(k)
	}
	if estimatedCells > remainingCells {
		return anyJevItemDumpUnit{}, errors.New("AnyJev item-dump unit exceeds remaining probability-cell bound")
	}
	unit := anyJevItemDumpUnit{
		UnitKeySHA256: anyJevStringSHA256(groupKey), Kind: kind, Options: k,
		Examples: len(golds), PermutationCount: len(perms),
		PermutationLayoutSHA256:   anyJevStringSHA256(string(permutationBytes)),
		ContentFreePriorAvailable: contentFreeAvailable,
	}
	if typed {
		workflow := group["workflow"].(string)
		unit.WorkflowSHA256 = anyJevStringSHA256(workflow)
	}
	var rawCorrect, meanCorrect, logMeanCorrect int
	var flipCount, flipComparisons int64
	var logMeanNLL, logMeanBrier, softAccuracy float64
	for index, rawGold := range golds {
		gold, valid := boundedAnyJevInteger(rawGold, 0, k-1)
		if !valid {
			return anyJevItemDumpUnit{}, errors.New("gold label is outside the option range")
		}
		item, ok := probabilityItems[index].([]any)
		if !ok || len(item) != len(perms) {
			return anyJevItemDumpUnit{}, errors.New("raw probability permutation count is invalid")
		}
		meanByOption := make([]float64, k)
		logByOption := make([]float64, k)
		firstWinner := -1
		for permutationIndex, rawVector := range item {
			vector, err := normalizeAnyJevItemDumpProbabilityVector(rawVector, k)
			if err != nil {
				return anyJevItemDumpUnit{}, fmt.Errorf("invalid raw probability vector: %w", err)
			}
			permutation := perms[permutationIndex]
			positionWinner := anyJevItemDumpArgmax(vector)
			winner := permutation[positionWinner]
			if permutationIndex == 0 {
				firstWinner = winner
				if winner == gold {
					rawCorrect++
				}
			} else {
				flipComparisons++
				if winner != firstWinner {
					flipCount++
				}
			}
			for position, option := range permutation {
				probability := vector[position]
				meanByOption[option] += probability / float64(len(perms))
				logByOption[option] += math.Log(math.Max(probability, 1e-12)) / float64(len(perms))
			}
		}
		meanByOption = normalizeAnyJevItemDumpVector(meanByOption)
		logMeanByOption := normalizeAnyJevItemDumpLogMean(logByOption)
		if anyJevItemDumpArgmax(meanByOption) == gold {
			meanCorrect++
		}
		if anyJevItemDumpArgmax(logMeanByOption) == gold {
			logMeanCorrect++
		}
		probability := math.Max(logMeanByOption[gold], 1e-12)
		logMeanNLL -= math.Log(probability)
		for option, predicted := range logMeanByOption {
			goldValue := 0.0
			if option == gold {
				goldValue = 1
			}
			logMeanBrier += (predicted - goldValue) * (predicted - goldValue)
		}
		if len(softItems) > 0 {
			soft, err := normalizeAnyJevItemDumpProbabilityVector(softItems[index], k)
			if err != nil {
				return anyJevItemDumpUnit{}, fmt.Errorf("invalid soft gold-label vector: %w", err)
			}
			for option := 0; option < k; option++ {
				softAccuracy += logMeanByOption[option] * soft[option]
			}
		}
	}
	n := float64(len(golds))
	unit.RawFirstLayoutAccuracy = float64(rawCorrect) / n
	unit.PermutationMeanAccuracy = float64(meanCorrect) / n
	unit.PermutationLogMeanAccuracy = float64(logMeanCorrect) / n
	unit.PermutationLogMeanNLL = logMeanNLL / n
	unit.PermutationLogMeanBrier = logMeanBrier / n
	if flipComparisons > 0 {
		unit.PermutationFlipRate = float64(flipCount) / float64(flipComparisons)
	}
	if len(softItems) > 0 {
		softMean := softAccuracy / n
		unit.SoftLabelLogMeanAccuracy = &softMean
	}
	return unit, nil
}

func anyJevItemDumpCellCount(unit anyJevItemDumpUnit) int64 {
	cells := int64(unit.Examples) * int64(unit.PermutationCount) * int64(unit.Options)
	if unit.SoftLabelLogMeanAccuracy != nil {
		cells += int64(unit.Examples) * int64(unit.Options)
	}
	if unit.ContentFreePriorAvailable {
		cells += int64(unit.PermutationCount) * int64(unit.Options)
	}
	return cells
}

func normalizeAnyJevItemDumpPermutations(value any, k int) ([][]int, error) {
	rawPermutations, ok := value.([]any)
	if !ok || len(rawPermutations) == 0 || len(rawPermutations) > k {
		return nil, errors.New("option permutations are invalid")
	}
	permutations := make([][]int, 0, len(rawPermutations))
	seen := make(map[string]struct{}, len(rawPermutations))
	for permutationIndex, rawPermutation := range rawPermutations {
		entries, ok := rawPermutation.([]any)
		if !ok || len(entries) != k {
			return nil, errors.New("option permutation width is invalid")
		}
		permutation := make([]int, k)
		seenOptions := make(map[int]struct{}, k)
		for position, rawOption := range entries {
			option, valid := boundedAnyJevInteger(rawOption, 0, k-1)
			if !valid {
				return nil, errors.New("option permutation index is invalid")
			}
			if _, duplicate := seenOptions[option]; duplicate {
				return nil, errors.New("option permutation repeats an option")
			}
			seenOptions[option] = struct{}{}
			permutation[position] = option
		}
		encodedPermutation, err := json.Marshal(permutation)
		if err != nil {
			return nil, err
		}
		key := string(encodedPermutation)
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("option permutation list contains a duplicate")
		}
		seen[key] = struct{}{}
		if permutationIndex == 0 {
			for index, option := range permutation {
				if index != option {
					return nil, errors.New("first option permutation is not the canonical identity layout")
				}
			}
		}
		permutations = append(permutations, permutation)
	}
	return permutations, nil
}

func normalizeAnyJevItemDumpProbabilityVector(value any, k int) ([]float64, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) != k {
		return nil, errors.New("probability vector width is invalid")
	}
	probabilities := make([]float64, k)
	sum := 0.0
	for index, rawProbability := range raw {
		probability, valid := boundedAnyJevNumber(rawProbability, 0, 1)
		if !valid {
			return nil, errors.New("probability value is invalid")
		}
		probabilities[index] = probability
		sum += probability
	}
	if math.Abs(sum-1) > 0.001 {
		return nil, errors.New("probability vector is not normalized")
	}
	return probabilities, nil
}

func validateAnyJevItemDumpPrior(value any, permutations, k int) error {
	prior, ok := value.([]any)
	if !ok || len(prior) != permutations {
		return errors.New("content-free prior permutation count is invalid")
	}
	for _, vector := range prior {
		if _, err := normalizeAnyJevItemDumpProbabilityVector(vector, k); err != nil {
			return fmt.Errorf("content-free prior is invalid: %w", err)
		}
	}
	return nil
}

func anyJevItemDumpArgmax(values []float64) int {
	best := 0
	for index := 1; index < len(values); index++ {
		if values[index] > values[best] {
			best = index
		}
	}
	return best
}

func normalizeAnyJevItemDumpVector(values []float64) []float64 {
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	if sum <= 0 {
		return values
	}
	for index := range values {
		values[index] /= sum
	}
	return values
}

func normalizeAnyJevItemDumpLogMean(logValues []float64) []float64 {
	maximum := logValues[0]
	for _, value := range logValues[1:] {
		if value > maximum {
			maximum = value
		}
	}
	probabilities := make([]float64, len(logValues))
	for index, value := range logValues {
		probabilities[index] = math.Exp(value - maximum)
	}
	return normalizeAnyJevItemDumpVector(probabilities)
}
