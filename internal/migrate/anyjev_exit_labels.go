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

const anyJevExitLabelsResultSchema = "threadforge.anyjev-exit-labels-result.v1"

type anyJevExitLabelsImport struct {
	SchemaVersion       string                `json:"schema_version"`
	ModelSHA256         string                `json:"model_sha256"`
	Blocks              []int                 `json:"blocks"`
	Budgets             []int                 `json:"label_budgets"`
	Seeds               []int                 `json:"seeds"`
	Rows                []anyJevExitLabelsRow `json:"rows"`
	OmittedFields       []string              `json:"omitted_fields"`
	HumanReviewRequired bool                  `json:"human_review_required"`
	ExecutionVerified   bool                  `json:"execution_verified"`
	CutoverEligible     bool                  `json:"cutover_evidence_eligible"`
}

type anyJevExitLabelsRow struct {
	Method       string    `json:"method"`
	Budget       *int      `json:"budget,omitempty"`
	Accuracy     float64   `json:"accuracy"`
	AccuracyCI95 []float64 `json:"accuracy_ci95"`
	ECE          float64   `json:"ece"`
	NLL          float64   `json:"nll"`
	DiffVsL1CI95 []float64 `json:"diff_vs_l1_ci95,omitempty"`
}

var anyJevExitLabelsRootFields = map[string]bool{
	"blocks": true, "budgets": true, "date": true, "env": true, "model": true, "rows": true, "seeds": true,
}

var anyJevExitLabelsMetricFields = map[string]bool{
	"acc": true, "acc_ci95": true, "ece": true, "nll": true, "diff_vs_L1_ci95": true,
}

func isAnyJevExitLabelsJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	return strings.HasPrefix(path, "bench/results_exit/") && strings.HasSuffix(path, ".labels.json")
}

func normalizeAnyJevExitLabelsResult(value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev label-study result shape")
	}
	if len(root) != len(anyJevExitLabelsRootFields) {
		return normalizedItem{}, errors.New("AnyJev label-study result is missing required fields")
	}
	for key := range root {
		if !anyJevExitLabelsRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev label-study result field %q", key)
		}
	}
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	date, dateOK := boundedAnyJevBenchmarkString(root["date"], 64)
	if !modelOK || !dateOK || !validAnyJevLatencyTimestamp(date) {
		return normalizedItem{}, errors.New("AnyJev label-study model or timestamp is invalid")
	}
	env, envOK := root["env"].(map[string]any)
	if !envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev label-study environment metadata is invalid")
	}
	blocks, err := normalizeAnyJevExitLabelsIntegerList(root["blocks"], 1, 10000, "blocks")
	if err != nil {
		return normalizedItem{}, err
	}
	budgets, err := normalizeAnyJevExitLabelsIntegerList(root["budgets"], 1, 1000000, "label budgets")
	if err != nil {
		return normalizedItem{}, err
	}
	seeds, err := normalizeAnyJevExitLabelsIntegerList(root["seeds"], 0, 1000000, "seeds")
	if err != nil {
		return normalizedItem{}, err
	}
	rows, err := normalizeAnyJevExitLabelsRows(root["rows"], budgets)
	if err != nil {
		return normalizedItem{}, err
	}
	payload := anyJevExitLabelsImport{
		SchemaVersion: anyJevExitLabelsResultSchema, ModelSHA256: anyJevStringSHA256(model),
		Blocks: blocks, Budgets: budgets, Seeds: seeds, Rows: rows,
		OmittedFields:       []string{"model_label", "date", "environment_details", "method_labels"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev label-study projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevExitLabelsIntegerList(value any, minimum, maximum int, name string) ([]int, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > 256 {
		return nil, fmt.Errorf("AnyJev label-study %s are invalid", name)
	}
	values := make([]int, 0, len(raw))
	seen := make(map[int]struct{}, len(raw))
	for _, entry := range raw {
		integer, valid := boundedAnyJevInteger(entry, minimum, maximum)
		if !valid {
			return nil, fmt.Errorf("AnyJev label-study %s contain an invalid value", name)
		}
		if _, duplicate := seen[integer]; duplicate {
			return nil, fmt.Errorf("AnyJev label-study %s contain duplicates", name)
		}
		seen[integer] = struct{}{}
		values = append(values, integer)
	}
	return values, nil
}

func normalizeAnyJevExitLabelsRows(value any, budgets []int) ([]anyJevExitLabelsRow, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) == 0 || len(rows) > 1+2*len(budgets) {
		return nil, errors.New("AnyJev label-study metric rows are invalid or exceed bounds")
	}
	allowedBudgets := make(map[int]struct{}, len(budgets))
	for _, budget := range budgets {
		allowedBudgets[budget] = struct{}{}
	}
	projected := make([]anyJevExitLabelsRow, 0, len(rows))
	seenL1 := make(map[int]struct{}, len(budgets))
	seenHead := make(map[int]struct{}, len(budgets))
	for name, raw := range rows {
		method := "raw"
		budget := 0
		if name != "raw" {
			separator := strings.IndexByte(name, '@')
			if separator < 1 || separator == len(name)-1 || (name[:separator] != "L1" && name[:separator] != "head") {
				return nil, errors.New("AnyJev label-study method key is unsupported")
			}
			budgetText := name[separator+1:]
			parsed, err := strconv.Atoi(budgetText)
			if err != nil || strconv.Itoa(parsed) != budgetText {
				return nil, errors.New("AnyJev label-study method budget is invalid")
			}
			if _, exists := allowedBudgets[parsed]; !exists {
				return nil, errors.New("AnyJev label-study row refers to an undeclared budget")
			}
			budget, method = parsed, strings.ToLower(name[:separator])
		}
		row, ok := raw.(map[string]any)
		if !ok || len(row) < 4 || len(row) > 5 {
			return nil, errors.New("AnyJev label-study metric row has an invalid shape")
		}
		for key := range row {
			if !anyJevExitLabelsMetricFields[key] {
				return nil, fmt.Errorf("unsupported AnyJev label-study metric %q", key)
			}
		}
		accuracy, err := anyJevExitNumber(row["acc"], 0, 1)
		if err != nil {
			return nil, err
		}
		ece, err := anyJevExitNumber(row["ece"], 0, 1)
		if err != nil {
			return nil, err
		}
		nll, err := anyJevExitNumber(row["nll"], 0, 1e6)
		if err != nil {
			return nil, err
		}
		accuracyCI, err := anyJevExitFloatArray(row["acc_ci95"], 2, 0, 1)
		if err != nil || accuracyCI[0] > accuracyCI[1] {
			return nil, errors.New("AnyJev label-study accuracy interval is invalid")
		}
		converted := anyJevExitLabelsRow{Method: method, Accuracy: accuracy, AccuracyCI95: accuracyCI, ECE: ece, NLL: nll}
		if method == "raw" {
			if len(row) != 4 {
				return nil, errors.New("AnyJev label-study raw row has unsupported metrics")
			}
		} else {
			budgetCopy := budget
			converted.Budget = &budgetCopy
			if method == "l1" {
				if len(row) != 4 {
					return nil, errors.New("AnyJev label-study L1 row has unsupported metrics")
				}
				seenL1[budget] = struct{}{}
			} else {
				if len(row) != 5 {
					return nil, errors.New("AnyJev label-study head row is missing its paired comparison")
				}
				diff, err := anyJevExitFloatArray(row["diff_vs_L1_ci95"], 2, -1, 1)
				if err != nil || diff[0] > diff[1] {
					return nil, errors.New("AnyJev label-study head-versus-L1 interval is invalid")
				}
				converted.DiffVsL1CI95 = diff
				seenHead[budget] = struct{}{}
			}
		}
		projected = append(projected, converted)
	}
	if _, exists := rows["raw"]; !exists || len(seenL1) != len(budgets) {
		return nil, errors.New("AnyJev label-study rows omit raw or a declared L1 budget")
	}
	for budget := range seenHead {
		if _, exists := seenL1[budget]; !exists {
			return nil, errors.New("AnyJev label-study head row has no same-budget L1 comparison")
		}
	}
	sort.Slice(projected, func(i, j int) bool {
		if projected[i].Method != projected[j].Method {
			return projected[i].Method < projected[j].Method
		}
		if projected[i].Budget == nil {
			return true
		}
		if projected[j].Budget == nil {
			return false
		}
		return *projected[i].Budget < *projected[j].Budget
	})
	return projected, nil
}
