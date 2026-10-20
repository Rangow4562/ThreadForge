package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevParaphraseOrderResultSchema = "threadforge.anyjev-paraphrase-order-result.v1"

type anyJevParaphraseOrderImport struct {
	SchemaVersion       string                          `json:"schema_version"`
	StudyKind           string                          `json:"study_kind"`
	ModelSHA256         string                          `json:"model_sha256"`
	Block               int                             `json:"block"`
	Variants            []string                        `json:"variants"`
	AdaptationBudgets   []int                           `json:"adaptation_budgets"`
	Rows                []anyJevParaphraseOrderRow      `json:"rows"`
	Questions           []anyJevParaphraseOrderQuestion `json:"questions"`
	OmittedFields       []string                        `json:"omitted_fields"`
	HumanReviewRequired bool                            `json:"human_review_required"`
	ExecutionVerified   bool                            `json:"execution_verified"`
	CutoverEligible     bool                            `json:"cutover_evidence_eligible"`
}

type anyJevParaphraseOrderRow struct {
	MetricKey       string    `json:"metric_key"`
	Accuracy        float64   `json:"accuracy"`
	ECE             float64   `json:"ece"`
	NLL             float64   `json:"nll"`
	N               int       `json:"n"`
	AccuracyCI95    []float64 `json:"accuracy_ci95,omitempty"`
	DiffVsOrigCI95  []float64 `json:"diff_vs_orig_ci95,omitempty"`
	FlipVsOrig      *float64  `json:"flip_vs_orig,omitempty"`
	FlipVsCanonical *float64  `json:"flip_vs_canonical,omitempty"`
}

type anyJevParaphraseOrderQuestion struct {
	QuestionKeySHA256 string                                `json:"question_key_sha256"`
	K                 int                                   `json:"k"`
	Kind              string                                `json:"kind"`
	Accuracies        []anyJevParaphraseOrderQuestionMetric `json:"accuracies"`
}

type anyJevParaphraseOrderQuestionMetric struct {
	MetricKey string  `json:"metric_key"`
	Accuracy  float64 `json:"accuracy"`
}

var anyJevParaphraseOrderVariants = []string{"orig", "w1", "w2", "w3", "o1"}
var anyJevParaphraseOrderParaphraseRootFields = map[string]bool{
	"block": true, "date": true, "env": true, "model": true, "questions": true, "rows": true, "variants": true,
}
var anyJevParaphraseOrderOrderRootFields = map[string]bool{
	"block": true, "date": true, "env": true, "model": true, "rows": true,
}

func isAnyJevParaphraseOrderJSONPath(relative string) bool {
	_, _, _, ok := anyJevParaphraseOrderPath(relative)
	return ok
}

// anyJevParaphraseOrderPath recognizes only a dated, direct child of the
// documented results_paraphrase directory and returns its producer kind and
// filename-declared block (zero for order-study outputs).
func anyJevParaphraseOrderPath(relative string) (kind string, pathBlock int, dateDir string, ok bool) {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 4 || !strings.EqualFold(parts[0], "bench") ||
		!strings.EqualFold(parts[1], "results_paraphrase") {
		return "", 0, "", false
	}
	if _, err := parseAnyJevMigrationDate(parts[2]); err != nil {
		return "", 0, "", false
	}
	name := strings.ToLower(path.Base(parts[3]))
	if strings.HasSuffix(name, ".order.json") && len(name) > len(".order.json") {
		return "listing_order", 0, parts[2], true
	}
	marker := ".paraphrase.b"
	index := strings.LastIndex(name, marker)
	if index <= 0 || !strings.HasSuffix(name, ".json") {
		return "", 0, "", false
	}
	blockText := strings.TrimSuffix(name[index+len(marker):], ".json")
	block, err := strconv.Atoi(blockText)
	if err != nil || block < 1 || block > 10000 || strconv.Itoa(block) != blockText {
		return "", 0, "", false
	}
	return "paraphrase", block, parts[2], true
}

func parseAnyJevMigrationDate(value string) (string, error) {
	if len(value) != len("2006-01-02") {
		return "", errors.New("invalid date directory")
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return "", errors.New("invalid date directory")
	}
	return value, nil
}

func normalizeAnyJevParaphraseOrderResult(relative string, value any) (normalizedItem, error) {
	kind, filenameBlock, dateDir, recognized := anyJevParaphraseOrderPath(relative)
	if !recognized {
		return normalizedItem{}, errors.New("unsupported AnyJev paraphrase/order result path")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev paraphrase/order result shape")
	}
	allowed := anyJevParaphraseOrderParaphraseRootFields
	if kind == "listing_order" {
		allowed = anyJevParaphraseOrderOrderRootFields
	}
	if len(root) != len(allowed) {
		return normalizedItem{}, errors.New("AnyJev paraphrase/order result is missing required fields")
	}
	for key := range root {
		if !allowed[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev paraphrase/order result field %q", key)
		}
	}
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	date, dateOK := boundedAnyJevBenchmarkString(root["date"], 64)
	if !modelOK || privacy.ContainsCredentialText(model) || !dateOK || !validAnyJevLatencyTimestamp(date) {
		return normalizedItem{}, errors.New("AnyJev paraphrase/order model or timestamp is invalid")
	}
	if _, err := parseAnyJevMigrationDate(dateDir); err != nil {
		return normalizedItem{}, err
	}
	env, envOK := root["env"].(map[string]any)
	if !envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev paraphrase/order environment metadata is invalid")
	}
	block, blockOK := boundedAnyJevInteger(root["block"], 1, 10000)
	if !blockOK || (filenameBlock != 0 && filenameBlock != block) {
		return normalizedItem{}, errors.New("AnyJev paraphrase/order block does not match its filename")
	}
	filename := strings.ToLower(path.Base(filepathSlash(relative)))
	modelFilename := strings.ToLower(strings.ReplaceAll(model, "/", "__"))
	if kind == "listing_order" {
		if filename != modelFilename+".order.json" {
			return normalizedItem{}, errors.New("AnyJev listing-order filename does not match its model")
		}
	} else if filename != modelFilename+".paraphrase.b"+strconv.Itoa(block)+".json" {
		return normalizedItem{}, errors.New("AnyJev paraphrase filename does not match its model or block")
	}

	projected := anyJevParaphraseOrderImport{
		SchemaVersion: anyJevParaphraseOrderResultSchema, StudyKind: kind,
		ModelSHA256: anyJevStringSHA256(model), Block: block,
		Variants: []string{}, AdaptationBudgets: []int{}, Rows: []anyJevParaphraseOrderRow{},
		Questions:           []anyJevParaphraseOrderQuestion{},
		OmittedFields:       []string{"model_label", "date", "environment_details", "question_labels"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	if kind == "paraphrase" {
		variants, err := normalizeAnyJevParaphraseOrderVariantList(root["variants"])
		if err != nil {
			return normalizedItem{}, err
		}
		projected.Variants = variants
		projected.Rows, projected.AdaptationBudgets, err = normalizeAnyJevParaphraseRows(root["rows"])
		if err != nil {
			return normalizedItem{}, err
		}
		projected.Questions, err = normalizeAnyJevParaphraseQuestions(root["questions"], projected.Rows, projected.AdaptationBudgets)
		if err != nil {
			return normalizedItem{}, err
		}
	} else {
		rows, _, err := normalizeAnyJevOrderRows(root["rows"])
		if err != nil {
			return normalizedItem{}, err
		}
		projected.Rows = rows
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev paraphrase/order projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevParaphraseOrderVariantList(value any) ([]string, error) {
	variants, ok := value.([]any)
	if !ok || len(variants) != len(anyJevParaphraseOrderVariants) {
		return nil, errors.New("AnyJev paraphrase variants are invalid")
	}
	result := make([]string, 0, len(variants))
	for index, raw := range variants {
		variant, valid := raw.(string)
		if !valid || variant != anyJevParaphraseOrderVariants[index] {
			return nil, errors.New("AnyJev paraphrase variants are unsupported or out of order")
		}
		result = append(result, variant)
	}
	return result, nil
}

func normalizeAnyJevParaphraseRows(value any) ([]anyJevParaphraseOrderRow, []int, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) < 36 || len(rows) > 1000 {
		return nil, nil, errors.New("AnyJev paraphrase metric rows are invalid")
	}
	parsed := make(map[string]anyJevParaphraseOrderRow, len(rows))
	budgetSet := make(map[int]struct{})
	var sampleCount int
	for rawKey, rawValue := range rows {
		key, budget, valid := normalizeAnyJevParaphraseMetricKey(rawKey)
		if !valid {
			return nil, nil, errors.New("AnyJev paraphrase metric key is unsupported")
		}
		row, ok := rawValue.(map[string]any)
		if !ok {
			return nil, nil, errors.New("AnyJev paraphrase metric row is invalid")
		}
		for field := range row {
			if field != "acc" && field != "ece" && field != "nll" && field != "n" &&
				field != "acc_ci95" && field != "diff_vs_orig_ci95" && field != "flip_vs_orig" {
				return nil, nil, fmt.Errorf("unsupported AnyJev paraphrase metric field %q", field)
			}
		}
		accuracy, accOK := boundedAnyJevNumber(row["acc"], 0, 1)
		ece, eceOK := boundedAnyJevNumber(row["ece"], 0, 1)
		nll, nllOK := boundedAnyJevNumber(row["nll"], 0, 1e6)
		n, nOK := boundedAnyJevInteger(row["n"], 1, 1e8)
		accCI, accCIOK := normalizeAnyJevParaphraseInterval(row["acc_ci95"], 0, 1)
		diffCI, diffCIOK := normalizeAnyJevParaphraseInterval(row["diff_vs_orig_ci95"], -1, 1)
		flip, flipOK := boundedAnyJevNumber(row["flip_vs_orig"], 0, 1)
		if !accOK || !eceOK || !nllOK || !nOK || !accCIOK || !diffCIOK || !flipOK ||
			len(row) != 7 {
			return nil, nil, errors.New("AnyJev paraphrase metrics or confidence intervals are invalid")
		}
		if budget == 0 {
			if sampleCount == 0 {
				sampleCount = n
			} else if sampleCount != n {
				return nil, nil, errors.New("AnyJev paraphrase base rows have inconsistent sample counts")
			}
		}
		flipCopy := flip
		parsed[key] = anyJevParaphraseOrderRow{
			MetricKey: key, Accuracy: accuracy, ECE: ece, NLL: nll, N: n,
			AccuracyCI95: accCI, DiffVsOrigCI95: diffCI, FlipVsOrig: &flipCopy,
		}
		if budget > 0 {
			budgetSet[budget] = struct{}{}
		}
	}
	if sampleCount == 0 {
		return nil, nil, errors.New("AnyJev paraphrase result has no base sample-count reference")
	}
	for key, row := range parsed {
		_, budget, _ := normalizeAnyJevParaphraseMetricKey(key)
		expectedCount := sampleCount
		if budget > 0 {
			// The producer pools three independently sampled adaptation draws.
			expectedCount *= 3
		}
		if row.N != expectedCount {
			return nil, nil, errors.New("AnyJev paraphrase sample counts do not match the producer aggregation")
		}
	}
	for _, variant := range anyJevParaphraseOrderVariants {
		prefixes := []string{"raw", "L0", "orig-head"}
		if variant == "orig" {
			prefixes = append(prefixes, "tt-head")
		} else {
			prefixes = append(prefixes, "own-head", "mix-head", "all-head", "ta-head", "tt-head")
		}
		for _, prefix := range prefixes {
			if _, exists := parsed[prefix+"@"+variant]; !exists {
				return nil, nil, errors.New("AnyJev paraphrase result is missing a required metric row")
			}
		}
	}
	budgets := make([]int, 0, len(budgetSet))
	for budget := range budgetSet {
		budgets = append(budgets, budget)
	}
	sort.Ints(budgets)
	for _, budget := range budgets {
		for _, variant := range anyJevParaphraseOrderVariants[1:] {
			for _, prefix := range []string{"ta", "ta-mean"} {
				key := prefix + "@" + strconv.Itoa(budget) + "-head@" + variant
				if _, exists := parsed[key]; !exists {
					return nil, nil, errors.New("AnyJev paraphrase adaptation budget rows are incomplete")
				}
			}
		}
	}
	if len(parsed) != 36+8*len(budgets) {
		return nil, nil, errors.New("AnyJev paraphrase result contains an unexpected or incomplete metric matrix")
	}
	result := make([]anyJevParaphraseOrderRow, 0, len(parsed))
	for _, row := range parsed {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].MetricKey < result[j].MetricKey })
	return result, budgets, nil
}

func normalizeAnyJevParaphraseMetricKey(value string) (string, int, bool) {
	parts := strings.Split(value, "@")
	if len(parts) == 2 && anyJevParaphraseVariant(parts[1]) {
		prefix := parts[0]
		allowed := map[string]bool{
			"raw": true, "L0": true, "orig-head": true, "own-head": true,
			"mix-head": true, "all-head": true, "ta-head": true, "tt-head": true,
		}
		if allowed[prefix] {
			if parts[1] == "orig" && (prefix == "own-head" || prefix == "mix-head" || prefix == "all-head" || prefix == "ta-head") {
				return "", 0, false
			}
			return value, 0, true
		}
	}
	if len(parts) == 3 && anyJevParaphraseVariant(parts[2]) && (parts[0] == "ta" || parts[0] == "ta-mean") {
		if !strings.HasSuffix(parts[1], "-head") {
			return "", 0, false
		}
		budgetText := strings.TrimSuffix(parts[1], "-head")
		budget, err := strconv.Atoi(budgetText)
		if err == nil && budget >= 1 && budget <= 1000000 && strconv.Itoa(budget) == budgetText && parts[2] != "orig" {
			return value, budget, true
		}
	}
	return "", 0, false
}

func anyJevParaphraseVariant(value string) bool {
	for _, variant := range anyJevParaphraseOrderVariants {
		if value == variant {
			return true
		}
	}
	return false
}

func normalizeAnyJevParaphraseInterval(value any, minimum, maximum float64) ([]float64, bool) {
	values, ok := value.([]any)
	if !ok || len(values) != 2 {
		return nil, false
	}
	low, lowOK := boundedAnyJevNumber(values[0], minimum, maximum)
	high, highOK := boundedAnyJevNumber(values[1], minimum, maximum)
	if !lowOK || !highOK || low > high {
		return nil, false
	}
	return []float64{low, high}, true
}

func normalizeAnyJevParaphraseQuestions(value any, rows []anyJevParaphraseOrderRow, budgets []int) ([]anyJevParaphraseOrderQuestion, error) {
	questions, ok := value.(map[string]any)
	if !ok || len(questions) == 0 || len(questions) > 256 {
		return nil, errors.New("AnyJev paraphrase question summaries are invalid")
	}
	rowKeys := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		rowKeys[row.MetricKey] = struct{}{}
	}
	expected := make(map[string]struct{})
	for _, variant := range anyJevParaphraseOrderVariants {
		expected["L0@"+variant] = struct{}{}
		expected["orig-head@"+variant] = struct{}{}
	}
	expected["tt-head@orig"] = struct{}{}
	for _, variant := range anyJevParaphraseOrderVariants[1:] {
		for _, prefix := range []string{"own-head", "mix-head", "all-head", "ta-head", "tt-head"} {
			expected[prefix+"@"+variant] = struct{}{}
		}
	}
	_ = budgets // Budgets are pooled-only in this producer's per-question output.
	result := make([]anyJevParaphraseOrderQuestion, 0, len(questions))
	seenHashes := make(map[string]struct{}, len(questions))
	for name, raw := range questions {
		if len(name) == 0 || len(name) > 256 {
			return nil, errors.New("AnyJev paraphrase question key is invalid")
		}
		question, ok := raw.(map[string]any)
		if !ok || len(question) != 3 {
			return nil, errors.New("AnyJev paraphrase question summary has an invalid shape")
		}
		for key := range question {
			if key != "K" && key != "kind" && key != "acc" {
				return nil, fmt.Errorf("unsupported AnyJev paraphrase question field %q", key)
			}
		}
		k, kOK := boundedAnyJevInteger(question["K"], 2, 26)
		kind, kindOK := question["kind"].(string)
		if !kOK || !kindOK || (kind != "choice" && kind != "noul" && kind != "score") {
			return nil, errors.New("AnyJev paraphrase question dimensions are invalid")
		}
		accuracies, ok := question["acc"].(map[string]any)
		if !ok || len(accuracies) != len(expected) {
			return nil, errors.New("AnyJev paraphrase per-question accuracy set is incomplete")
		}
		converted := make([]anyJevParaphraseOrderQuestionMetric, 0, len(accuracies))
		for key, rawAccuracy := range accuracies {
			if _, valid := expected[key]; !valid {
				return nil, errors.New("AnyJev paraphrase per-question metric key is unsupported")
			}
			if _, exists := rowKeys[key]; !exists {
				return nil, errors.New("AnyJev paraphrase question metric has no matching pooled row")
			}
			accuracy, valid := boundedAnyJevNumber(rawAccuracy, 0, 1)
			if !valid {
				return nil, errors.New("AnyJev paraphrase per-question accuracy is invalid")
			}
			converted = append(converted, anyJevParaphraseOrderQuestionMetric{MetricKey: key, Accuracy: accuracy})
		}
		sort.Slice(converted, func(i, j int) bool { return converted[i].MetricKey < converted[j].MetricKey })
		hash := anyJevStringSHA256(name)
		if _, exists := seenHashes[hash]; exists {
			return nil, errors.New("AnyJev paraphrase question keys collide after hashing")
		}
		seenHashes[hash] = struct{}{}
		result = append(result, anyJevParaphraseOrderQuestion{
			QuestionKeySHA256: hash, K: k, Kind: kind, Accuracies: converted,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].QuestionKeySHA256 < result[j].QuestionKeySHA256 })
	return result, nil
}

func normalizeAnyJevOrderRows(value any) ([]anyJevParaphraseOrderRow, []int, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) != 6 {
		return nil, nil, errors.New("AnyJev listing-order metric rows are invalid")
	}
	expected := map[string]bool{
		"fit-id@canonical": true, "fit-id@reversed": true, "fit-id@reversed+tt": true,
		"fit-rand@canonical": true, "fit-rand@reversed": true, "fit-rand@reversed+tt": true,
	}
	result := make([]anyJevParaphraseOrderRow, 0, len(rows))
	sampleCount := 0
	for key, raw := range rows {
		if !expected[key] {
			return nil, nil, errors.New("AnyJev listing-order metric key is unsupported")
		}
		row, ok := raw.(map[string]any)
		if !ok || len(row) != 5 {
			return nil, nil, errors.New("AnyJev listing-order metric row is invalid")
		}
		for field := range row {
			if field != "acc" && field != "ece" && field != "nll" && field != "n" && field != "flip_vs_canonical" {
				return nil, nil, fmt.Errorf("unsupported AnyJev listing-order metric field %q", field)
			}
		}
		accuracy, accOK := boundedAnyJevNumber(row["acc"], 0, 1)
		ece, eceOK := boundedAnyJevNumber(row["ece"], 0, 1)
		nll, nllOK := boundedAnyJevNumber(row["nll"], 0, 1e6)
		n, nOK := boundedAnyJevInteger(row["n"], 1, 1e8)
		flip, flipOK := boundedAnyJevNumber(row["flip_vs_canonical"], 0, 1)
		if !accOK || !eceOK || !nllOK || !nOK || !flipOK {
			return nil, nil, errors.New("AnyJev listing-order metrics are invalid")
		}
		if sampleCount == 0 {
			sampleCount = n
		} else if sampleCount != n {
			return nil, nil, errors.New("AnyJev listing-order rows have inconsistent sample counts")
		}
		if key == "fit-id@canonical" && flip > 1e-12 {
			return nil, nil, errors.New("AnyJev listing-order canonical baseline has a nonzero self-flip")
		}
		flipCopy := flip
		result = append(result, anyJevParaphraseOrderRow{
			MetricKey: key, Accuracy: accuracy, ECE: ece, NLL: nll, N: n, FlipVsCanonical: &flipCopy,
		})
	}
	if len(result) != len(expected) {
		return nil, nil, errors.New("AnyJev listing-order metric matrix is incomplete")
	}
	sort.Slice(result, func(i, j int) bool { return result[i].MetricKey < result[j].MetricKey })
	return result, []int{}, nil
}
