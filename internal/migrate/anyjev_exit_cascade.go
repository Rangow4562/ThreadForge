package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevExitCascadeResultSchema = "threadforge.anyjev-exit-cascade-result.v1"

var anyJevExitCascadeRootFields = map[string]bool{
	"date": true, "env": true, "exits": true, "model": true, "n_blocks": true, "rows": true,
}

var anyJevExitCascadeFixedFields = map[string]bool{
	"acc": true, "ece": true, "mean_blocks": true, "nll": true, "relative_depth": true,
}

var anyJevExitCascadeRuleFields = map[string]bool{
	"acc": true, "ece": true, "excess_error": true, "exit_shares": true,
	"mean_blocks": true, "nll": true, "relative_depth": true,
}

var anyJevExitCascadeEpsilonKeys = []string{"0.0", "0.005", "0.01", "0.02"}

type anyJevExitCascadeImport struct {
	SchemaVersion       string                 `json:"schema_version"`
	ModelSHA256         string                 `json:"model_sha256"`
	TransformerLayers   int                    `json:"transformer_layers"`
	Exits               []int                  `json:"exits"`
	Rows                []anyJevExitCascadeRow `json:"rows"`
	OmittedFields       []string               `json:"omitted_fields"`
	HumanReviewRequired bool                   `json:"human_review_required"`
	ExecutionVerified   bool                   `json:"execution_verified"`
	CutoverEligible     bool                   `json:"cutover_evidence_eligible"`
}

type anyJevExitCascadeRow struct {
	Kind          string                   `json:"kind"`
	Block         *int                     `json:"block,omitempty"`
	Rule          string                   `json:"rule,omitempty"`
	Epsilon       *float64                 `json:"epsilon,omitempty"`
	Scope         string                   `json:"scope,omitempty"`
	Accuracy      float64                  `json:"accuracy"`
	ECE           float64                  `json:"ece"`
	NLL           float64                  `json:"nll"`
	MeanBlocks    float64                  `json:"mean_blocks"`
	RelativeDepth float64                  `json:"relative_depth"`
	ExcessError   *float64                 `json:"excess_error,omitempty"`
	ExitShares    []anyJevExitCascadeShare `json:"exit_shares,omitempty"`
}

type anyJevExitCascadeShare struct {
	Block int     `json:"block"`
	Share float64 `json:"share"`
}

func isAnyJevExitCascadeJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	return strings.HasPrefix(path, "bench/results_exit/") && strings.HasSuffix(path, ".cascade.json")
}

func normalizeAnyJevExitCascadeResult(value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok || len(root) != len(anyJevExitCascadeRootFields) {
		return normalizedItem{}, errors.New("unsupported AnyJev exit cascade result shape")
	}
	for key := range root {
		if !anyJevExitCascadeRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev exit cascade field %q", key)
		}
	}
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	date, dateOK := boundedAnyJevBenchmarkString(root["date"], 64)
	if !modelOK || !dateOK || !validAnyJevLatencyTimestamp(date) {
		return normalizedItem{}, errors.New("AnyJev exit cascade model or timestamp is invalid")
	}
	env, envOK := root["env"].(map[string]any)
	if !envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev exit cascade environment metadata is invalid")
	}
	nBlocks, nBlocksOK := boundedAnyJevInteger(root["n_blocks"], 1, 10000)
	if !nBlocksOK {
		return normalizedItem{}, errors.New("AnyJev exit cascade transformer block count is invalid")
	}
	exits, err := normalizeAnyJevExitCascadeExits(root["exits"], nBlocks)
	if err != nil {
		return normalizedItem{}, err
	}
	sourceRows, ok := root["rows"].(map[string]any)
	if !ok || len(sourceRows) != len(exits)+16 {
		return normalizedItem{}, errors.New("AnyJev exit cascade rows are invalid or incomplete")
	}
	for key := range sourceRows {
		if !anyJevExitCascadeIsExpectedKey(key, exits) {
			return normalizedItem{}, errors.New("AnyJev exit cascade row key is unsupported")
		}
	}
	fullDepthValue, exists := sourceRows[fmt.Sprintf("fixed@%d", nBlocks)]
	fullDepthRow, fullDepthOK := fullDepthValue.(map[string]any)
	if !exists || !fullDepthOK {
		return normalizedItem{}, errors.New("AnyJev exit cascade omits its full-depth reference")
	}
	fullAccuracy, err := anyJevExitNumber(fullDepthRow["acc"], 0, 1)
	if err != nil {
		return normalizedItem{}, errors.New("AnyJev exit cascade full-depth accuracy is invalid")
	}
	rows := make([]anyJevExitCascadeRow, 0, len(sourceRows))
	for _, key := range sortedKeys(sourceRows) {
		if strings.HasPrefix(key, "fixed@") {
			block, _ := strconv.Atoi(strings.TrimPrefix(key, "fixed@"))
			row, err := normalizeAnyJevExitCascadeFixedRow(sourceRows[key], block, nBlocks)
			if err != nil {
				return normalizedItem{}, err
			}
			rows = append(rows, row)
			continue
		}
		row, err := normalizeAnyJevExitCascadeRuleRow(key, sourceRows[key], exits, nBlocks, fullAccuracy)
		if err != nil {
			return normalizedItem{}, err
		}
		rows = append(rows, row)
	}
	payload := anyJevExitCascadeImport{
		SchemaVersion: anyJevExitCascadeResultSchema, ModelSHA256: anyJevStringSHA256(model),
		TransformerLayers: nBlocks, Exits: exits, Rows: rows,
		OmittedFields:       []string{"model_label", "date", "environment_details", "source_row_keys"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev exit cascade projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevExitCascadeExits(value any, nBlocks int) ([]int, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > 1024 {
		return nil, errors.New("AnyJev exit cascade block list is invalid")
	}
	exits := make([]int, 0, len(raw))
	previous := 0
	for _, entry := range raw {
		block, ok := boundedAnyJevInteger(entry, 1, nBlocks)
		if !ok || block <= previous {
			return nil, errors.New("AnyJev exit cascade blocks must be unique and increasing")
		}
		exits = append(exits, block)
		previous = block
	}
	if exits[len(exits)-1] != nBlocks {
		return nil, errors.New("AnyJev exit cascade blocks omit the full-depth reference")
	}
	return exits, nil
}

func anyJevExitCascadeIsExpectedKey(key string, exits []int) bool {
	if strings.HasPrefix(key, "fixed@") {
		blockText := strings.TrimPrefix(key, "fixed@")
		block, err := strconv.Atoi(blockText)
		return err == nil && strconv.Itoa(block) == blockText && containsInt(exits, block)
	}
	parts := strings.Split(key, ":")
	if len(parts) != 4 || parts[0] != "cascade" || !oneOf(parts[1], "quantile", "ltt") ||
		!strings.HasPrefix(parts[2], "eps=") || !oneOf(parts[3], "per-question", "pooled") {
		return false
	}
	epsilon := strings.TrimPrefix(parts[2], "eps=")
	for _, candidate := range anyJevExitCascadeEpsilonKeys {
		if epsilon == candidate {
			return true
		}
	}
	return false
}

func normalizeAnyJevExitCascadeFixedRow(value any, block, nBlocks int) (anyJevExitCascadeRow, error) {
	row, ok := value.(map[string]any)
	if !ok || len(row) != len(anyJevExitCascadeFixedFields) {
		return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade fixed row has an invalid shape")
	}
	for key := range row {
		if !anyJevExitCascadeFixedFields[key] {
			return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade fixed row has an unknown metric")
		}
	}
	accuracy, err := anyJevExitNumber(row["acc"], 0, 1)
	if err != nil {
		return anyJevExitCascadeRow{}, err
	}
	ece, err := anyJevExitNumber(row["ece"], 0, 1)
	if err != nil {
		return anyJevExitCascadeRow{}, err
	}
	nll, err := anyJevExitNumber(row["nll"], 0, 1e6)
	if err != nil {
		return anyJevExitCascadeRow{}, err
	}
	meanBlocks, err := anyJevExitNumber(row["mean_blocks"], 1, float64(nBlocks))
	if err != nil || absAnyJevFloat(meanBlocks-float64(block)) > 2e-6 {
		return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade fixed mean block count does not match its row key")
	}
	relative, err := anyJevExitNumber(row["relative_depth"], 0, 1)
	if err != nil || absAnyJevFloat(relative-float64(block)/float64(nBlocks)) > 2e-6 {
		return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade fixed depth does not reconcile")
	}
	blockCopy := block
	return anyJevExitCascadeRow{
		Kind: "fixed", Block: &blockCopy, Accuracy: accuracy, ECE: ece, NLL: nll,
		MeanBlocks: meanBlocks, RelativeDepth: relative,
	}, nil
}

func normalizeAnyJevExitCascadeRuleRow(key string, value any, exits []int, nBlocks int, fullAccuracy float64) (anyJevExitCascadeRow, error) {
	if !anyJevExitCascadeIsExpectedKey(key, exits) || strings.HasPrefix(key, "fixed@") {
		return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade rule key is invalid")
	}
	parts := strings.Split(key, ":")
	epsilonText := strings.TrimPrefix(parts[2], "eps=")
	epsilon, _ := strconv.ParseFloat(epsilonText, 64)
	row, ok := value.(map[string]any)
	if !ok || len(row) != len(anyJevExitCascadeRuleFields) {
		return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade policy row has an invalid shape")
	}
	for field := range row {
		if !anyJevExitCascadeRuleFields[field] {
			return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade policy row has an unknown metric")
		}
	}
	accuracy, err := anyJevExitNumber(row["acc"], 0, 1)
	if err != nil {
		return anyJevExitCascadeRow{}, err
	}
	ece, err := anyJevExitNumber(row["ece"], 0, 1)
	if err != nil {
		return anyJevExitCascadeRow{}, err
	}
	nll, err := anyJevExitNumber(row["nll"], 0, 1e6)
	if err != nil {
		return anyJevExitCascadeRow{}, err
	}
	excess, err := anyJevExitNumber(row["excess_error"], -1, 1)
	if err != nil || absAnyJevFloat(excess-(fullAccuracy-accuracy)) > 2e-6 {
		return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade excess error does not reconcile with the full-depth reference")
	}
	meanBlocks, err := anyJevExitNumber(row["mean_blocks"], 1, float64(nBlocks))
	if err != nil {
		return anyJevExitCascadeRow{}, err
	}
	relative, err := anyJevExitNumber(row["relative_depth"], 0, 1)
	if err != nil || absAnyJevFloat(relative-meanBlocks/float64(nBlocks)) > 2e-6 {
		return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade relative depth does not reconcile")
	}
	shares, err := normalizeAnyJevExitCascadeShares(row["exit_shares"], exits)
	if err != nil {
		return anyJevExitCascadeRow{}, err
	}
	weightedBlocks := 0.0
	for _, share := range shares {
		weightedBlocks += float64(share.Block) * share.Share
	}
	if absAnyJevFloat(meanBlocks-weightedBlocks) > 2e-6 {
		return anyJevExitCascadeRow{}, errors.New("AnyJev exit cascade mean block count does not reconcile with exit shares")
	}
	excessCopy, epsilonCopy := excess, epsilon
	return anyJevExitCascadeRow{
		Kind: "cascade", Rule: parts[1], Epsilon: &epsilonCopy, Scope: strings.ReplaceAll(parts[3], "-", "_"),
		Accuracy: accuracy, ECE: ece, NLL: nll, MeanBlocks: meanBlocks, RelativeDepth: relative,
		ExcessError: &excessCopy, ExitShares: shares,
	}, nil
}

func normalizeAnyJevExitCascadeShares(value any, exits []int) ([]anyJevExitCascadeShare, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) != len(exits) {
		return nil, errors.New("AnyJev exit cascade share map does not match exit blocks")
	}
	shares := make([]anyJevExitCascadeShare, 0, len(exits))
	sum := 0.0
	for _, block := range exits {
		key := strconv.Itoa(block)
		share, valid := boundedAnyJevNumber(rows[key], 0, 1)
		if !valid {
			return nil, errors.New("AnyJev exit cascade share is invalid")
		}
		shares = append(shares, anyJevExitCascadeShare{Block: block, Share: share})
		sum += share
	}
	for key := range rows {
		block, err := strconv.Atoi(key)
		if err != nil || strconv.Itoa(block) != key || !containsInt(exits, block) {
			return nil, errors.New("AnyJev exit cascade share map has an unknown exit")
		}
	}
	if absAnyJevFloat(sum-1) > 2e-6 {
		return nil, errors.New("AnyJev exit cascade shares do not sum to one")
	}
	return shares, nil
}
