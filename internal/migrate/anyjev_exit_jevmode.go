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

const anyJevExitJevModeResultSchema = "threadforge.anyjev-exit-jevmode-result.v1"

type anyJevExitJevModeImport struct {
	SchemaVersion       string                         `json:"schema_version"`
	ModelSHA256         string                         `json:"model_sha256"`
	TransformerLayers   int                            `json:"transformer_layers"`
	Blocks              []int                          `json:"blocks"`
	Kinds               []string                       `json:"kinds"`
	Slack               float64                        `json:"slack"`
	RecommendedBlock    int                            `json:"recommended_block"`
	OOFAccuracy         []float64                      `json:"oof_accuracy_by_block"`
	PerBlock            []anyJevExitJevModeBlockImport `json:"per_block"`
	Questions           []anyJevExitJevModeQuestion    `json:"questions"`
	Rules               []anyJevExitJevModeRule        `json:"rules"`
	References          []anyJevExitJevModeReference   `json:"references"`
	OmittedFields       []string                       `json:"omitted_fields"`
	HumanReviewRequired bool                           `json:"human_review_required"`
	ExecutionVerified   bool                           `json:"execution_verified"`
	CutoverEligible     bool                           `json:"cutover_evidence_eligible"`
}

type anyJevExitJevModeBlockImport struct {
	Block         int       `json:"block"`
	Accuracy      float64   `json:"accuracy"`
	AccuracyCI95  []float64 `json:"accuracy_ci95"`
	DiffFullCI95  []float64 `json:"diff_vs_full_ci95"`
	ECE           float64   `json:"ece"`
	Flip          float64   `json:"flip"`
	NLL           float64   `json:"nll"`
	OOFAccuracy   float64   `json:"oof_accuracy"`
	RelativeDepth float64   `json:"relative_depth"`
}

type anyJevExitJevModeQuestion struct {
	QuestionSHA256 string    `json:"question_sha256"`
	OptionCount    int       `json:"option_count"`
	CVBlock        int       `json:"cv_block"`
	OOFAccuracy    []float64 `json:"oof_accuracy_by_block"`
	OOFNLL         []float64 `json:"oof_nll_by_block"`
	HeadKind       []string  `json:"head_kind_by_block"`
	TestAccuracy   []float64 `json:"test_accuracy_by_block"`
	TestFlip       []float64 `json:"test_flip_by_block"`
}

type anyJevExitJevModeRule struct {
	Kind          string    `json:"kind"`
	Block         *int      `json:"block,omitempty"`
	Accuracy      float64   `json:"accuracy"`
	AccuracyCI95  []float64 `json:"accuracy_ci95"`
	DiffFullCI95  []float64 `json:"diff_vs_full_ci95"`
	ECE           float64   `json:"ece"`
	Flip          float64   `json:"flip"`
	MeanBlocks    float64   `json:"mean_blocks"`
	NLL           float64   `json:"nll"`
	RelativeDepth float64   `json:"relative_depth"`
}

type anyJevExitJevModeReference struct {
	Name     string  `json:"name"`
	Accuracy float64 `json:"accuracy"`
	ECE      float64 `json:"ece"`
}

var anyJevExitJevModeRootFields = map[string]bool{
	"blocks": true, "date": true, "env": true, "kinds": true, "latency": true,
	"model": true, "n_blocks": true, "oof_acc_by_block": true, "per_block": true,
	"questions": true, "recommended_block": true, "references": true, "rules": true, "slack": true,
}

var anyJevExitJevModeBlockFields = map[string]bool{
	"acc": true, "acc_ci95": true, "diff_vs_full_ci95": true, "ece": true,
	"flip": true, "nll": true, "oof_acc": true, "relative_depth": true,
}

var anyJevExitJevModeQuestionFields = map[string]bool{
	"K": true, "cv_block": true, "kind": true, "oof_acc": true, "oof_nll": true,
	"test_acc": true, "test_flip": true,
}

var anyJevExitJevModeRuleFields = map[string]bool{
	"acc": true, "acc_ci95": true, "diff_vs_full_ci95": true, "ece": true,
	"flip": true, "mean_blocks": true, "nll": true, "relative_depth": true,
}

func isAnyJevExitJevModeJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	return strings.HasPrefix(path, "bench/results_exit/") && strings.HasSuffix(path, ".jevmode.json")
}

func normalizeAnyJevExitJevModeResult(value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev Jev-mode result shape")
	}
	if len(root) != len(anyJevExitJevModeRootFields) {
		return normalizedItem{}, errors.New("AnyJev Jev-mode result is missing required fields")
	}
	for key := range root {
		if !anyJevExitJevModeRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev Jev-mode result field %q", key)
		}
	}
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	date, dateOK := boundedAnyJevBenchmarkString(root["date"], 64)
	if !modelOK || !dateOK || !validAnyJevLatencyTimestamp(date) {
		return normalizedItem{}, errors.New("AnyJev Jev-mode model or timestamp is invalid")
	}
	env, envOK := root["env"].(map[string]any)
	if !envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev Jev-mode environment metadata is invalid")
	}
	nBlocks, nBlocksOK := boundedAnyJevInteger(root["n_blocks"], 1, 10000)
	recommended, recommendedOK := boundedAnyJevInteger(root["recommended_block"], 1, 10000)
	slack, slackOK := boundedAnyJevNumber(root["slack"], 0, 1)
	if !nBlocksOK || !recommendedOK || !slackOK || recommended > nBlocks {
		return normalizedItem{}, errors.New("AnyJev Jev-mode run dimensions are invalid")
	}
	blocks, err := normalizeAnyJevExitJevModeBlocks(root["blocks"], nBlocks)
	if err != nil {
		return normalizedItem{}, err
	}
	if blocks[len(blocks)-1] != nBlocks {
		return normalizedItem{}, errors.New("AnyJev Jev-mode blocks do not include the full-depth reference")
	}
	kinds, err := normalizeAnyJevExitJevModeKinds(root["kinds"])
	if err != nil {
		return normalizedItem{}, err
	}
	oofAccuracy, err := anyJevExitFloatArray(root["oof_acc_by_block"], len(blocks), 0, 1)
	if err != nil {
		return normalizedItem{}, fmt.Errorf("AnyJev Jev-mode pooled OOF accuracy: %w", err)
	}
	perBlock, err := normalizeAnyJevExitJevModePerBlock(root["per_block"], blocks, nBlocks, oofAccuracy)
	if err != nil {
		return normalizedItem{}, err
	}
	questions, err := normalizeAnyJevExitJevModeQuestions(root["questions"], blocks, kinds)
	if err != nil {
		return normalizedItem{}, err
	}
	if len(questions) == 0 || len(questions)*len(blocks) > 100000 {
		return normalizedItem{}, errors.New("AnyJev Jev-mode question/block matrix exceeds bounds")
	}
	for i := range blocks {
		mean := 0.0
		for _, question := range questions {
			mean += question.OOFAccuracy[i]
		}
		mean /= float64(len(questions))
		if absAnyJevFloat(mean-oofAccuracy[i]) > 2e-6 {
			return normalizedItem{}, errors.New("AnyJev Jev-mode pooled OOF accuracy does not reconcile with questions")
		}
	}
	best := 0.0
	for _, accuracy := range oofAccuracy {
		if accuracy > best {
			best = accuracy
		}
	}
	derivedRecommendation := 0
	for i, accuracy := range oofAccuracy {
		if accuracy >= best-slack {
			derivedRecommendation = blocks[i]
			break
		}
	}
	if derivedRecommendation != recommended {
		return normalizedItem{}, errors.New("AnyJev Jev-mode recommended block does not follow the calibration-only slack rule")
	}
	rules, err := normalizeAnyJevExitJevModeRules(root["rules"], questions, perBlock, blocks, nBlocks, recommended)
	if err != nil {
		return normalizedItem{}, err
	}
	references, err := normalizeAnyJevExitJevModeReferences(root["references"])
	if err != nil {
		return normalizedItem{}, err
	}
	if err := validateAnyJevExitJevModeLatency(root["latency"], nBlocks); err != nil {
		return normalizedItem{}, err
	}

	payload := anyJevExitJevModeImport{
		SchemaVersion:     anyJevExitJevModeResultSchema,
		ModelSHA256:       anyJevStringSHA256(model),
		TransformerLayers: nBlocks, Blocks: blocks, Kinds: kinds, Slack: slack,
		RecommendedBlock: recommended, OOFAccuracy: oofAccuracy,
		PerBlock: perBlock, Questions: questions, Rules: rules, References: references,
		OmittedFields:       []string{"model_label", "date", "environment_details", "question_labels", "reference_paths", "embedded_latency_reference"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev Jev-mode projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevExitJevModeBlocks(value any, nBlocks int) ([]int, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > 1024 {
		return nil, errors.New("AnyJev Jev-mode block list is invalid")
	}
	blocks := make([]int, 0, len(raw))
	previous := 0
	for _, entry := range raw {
		block, ok := boundedAnyJevInteger(entry, 1, nBlocks)
		if !ok || block <= previous {
			return nil, errors.New("AnyJev Jev-mode blocks must be unique and increasing")
		}
		blocks = append(blocks, block)
		previous = block
	}
	return blocks, nil
}

func normalizeAnyJevExitJevModeKinds(value any) ([]string, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > 8 {
		return nil, errors.New("AnyJev Jev-mode head kinds are invalid")
	}
	kinds := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		kind, ok := entry.(string)
		if !ok || !oneOf(kind, "lda", "ridge") {
			return nil, errors.New("AnyJev Jev-mode head kind is unsupported")
		}
		if _, duplicate := seen[kind]; duplicate {
			return nil, errors.New("AnyJev Jev-mode head kinds contain duplicates")
		}
		seen[kind] = struct{}{}
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds, nil
}

func normalizeAnyJevExitJevModePerBlock(value any, blocks []int, nBlocks int, oofAccuracy []float64) ([]anyJevExitJevModeBlockImport, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) != len(blocks) {
		return nil, errors.New("AnyJev Jev-mode per-block map does not match the block list")
	}
	for key := range rows {
		block, err := strconv.Atoi(key)
		if err != nil || strconv.Itoa(block) != key || !containsInt(blocks, block) {
			return nil, errors.New("AnyJev Jev-mode per-block map has an unknown layer")
		}
	}
	projected := make([]anyJevExitJevModeBlockImport, 0, len(blocks))
	for i, block := range blocks {
		row, ok := rows[strconv.Itoa(block)].(map[string]any)
		if !ok || len(row) != len(anyJevExitJevModeBlockFields) {
			return nil, errors.New("AnyJev Jev-mode per-block metric shape is invalid")
		}
		for key := range row {
			if !anyJevExitJevModeBlockFields[key] {
				return nil, fmt.Errorf("unsupported AnyJev Jev-mode per-block metric %q", key)
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
		flip, err := anyJevExitNumber(row["flip"], 0, 1)
		if err != nil {
			return nil, err
		}
		nll, err := anyJevExitNumber(row["nll"], 0, 1e6)
		if err != nil {
			return nil, err
		}
		oof, err := anyJevExitNumber(row["oof_acc"], 0, 1)
		if err != nil {
			return nil, err
		}
		relative, err := anyJevExitNumber(row["relative_depth"], 0, 1)
		if err != nil || absAnyJevFloat(relative-float64(block)/float64(nBlocks)) > 1e-6 || absAnyJevFloat(oof-oofAccuracy[i]) > 2e-6 {
			return nil, errors.New("AnyJev Jev-mode per-block depth or OOF accuracy does not reconcile")
		}
		accCI, err := anyJevExitFloatArray(row["acc_ci95"], 2, 0, 1)
		if err != nil || accCI[0] > accCI[1] {
			return nil, errors.New("AnyJev Jev-mode accuracy interval is invalid")
		}
		diffCI, err := anyJevExitFloatArray(row["diff_vs_full_ci95"], 2, -1, 1)
		if err != nil || diffCI[0] > diffCI[1] {
			return nil, errors.New("AnyJev Jev-mode difference interval is invalid")
		}
		projected = append(projected, anyJevExitJevModeBlockImport{
			Block: block, Accuracy: accuracy, AccuracyCI95: accCI, DiffFullCI95: diffCI,
			ECE: ece, Flip: flip, NLL: nll, OOFAccuracy: oof, RelativeDepth: relative,
		})
	}
	return projected, nil
}

func normalizeAnyJevExitJevModeQuestions(value any, blocks []int, kinds []string) ([]anyJevExitJevModeQuestion, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) == 0 || len(rows) > 1024 || len(rows)*len(blocks) > 100000 {
		return nil, errors.New("AnyJev Jev-mode question map is invalid or exceeds bounds")
	}
	allowedKinds := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		allowedKinds[kind] = struct{}{}
	}
	questions := make([]anyJevExitJevModeQuestion, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for name, raw := range rows {
		questionName, ok := boundedAnyJevBenchmarkString(name, 512)
		question, questionOK := raw.(map[string]any)
		if !ok || !questionOK || len(question) != len(anyJevExitJevModeQuestionFields) {
			return nil, errors.New("AnyJev Jev-mode question record is invalid")
		}
		for key := range question {
			if !anyJevExitJevModeQuestionFields[key] {
				return nil, fmt.Errorf("unsupported AnyJev Jev-mode question field %q", key)
			}
		}
		digest := anyJevStringSHA256(questionName)
		if _, duplicate := seen[digest]; duplicate {
			return nil, errors.New("AnyJev Jev-mode question identifiers collide")
		}
		seen[digest] = struct{}{}
		optionCount, optionOK := boundedAnyJevInteger(question["K"], 2, 4096)
		cvBlock, cvOK := boundedAnyJevInteger(question["cv_block"], 1, 10000)
		if !optionOK || !cvOK || !containsInt(blocks, cvBlock) {
			return nil, errors.New("AnyJev Jev-mode question dimensions are invalid")
		}
		oofAccuracy, err := anyJevExitFloatArray(question["oof_acc"], len(blocks), 0, 1)
		if err != nil {
			return nil, fmt.Errorf("Jev-mode question OOF accuracy: %w", err)
		}
		oofNLL, err := anyJevExitFloatArray(question["oof_nll"], len(blocks), 0, 1e6)
		if err != nil {
			return nil, fmt.Errorf("Jev-mode question OOF NLL: %w", err)
		}
		testAccuracy, err := anyJevExitFloatArray(question["test_acc"], len(blocks), 0, 1)
		if err != nil {
			return nil, fmt.Errorf("Jev-mode question test accuracy: %w", err)
		}
		testFlip, err := anyJevExitFloatArray(question["test_flip"], len(blocks), 0, 1)
		if err != nil {
			return nil, fmt.Errorf("Jev-mode question test flip: %w", err)
		}
		rawKinds, ok := question["kind"].([]any)
		if !ok || len(rawKinds) != len(blocks) {
			return nil, errors.New("AnyJev Jev-mode per-question head-kind list is invalid")
		}
		questionKinds := make([]string, 0, len(rawKinds))
		for _, rawKind := range rawKinds {
			kind, ok := rawKind.(string)
			if !ok {
				return nil, errors.New("AnyJev Jev-mode per-question head kind is invalid")
			}
			if _, allowed := allowedKinds[kind]; !allowed {
				return nil, errors.New("AnyJev Jev-mode per-question head kind is not in the run allowlist")
			}
			questionKinds = append(questionKinds, kind)
		}
		questions = append(questions, anyJevExitJevModeQuestion{
			QuestionSHA256: digest, OptionCount: optionCount, CVBlock: cvBlock,
			OOFAccuracy: oofAccuracy, OOFNLL: oofNLL, HeadKind: questionKinds,
			TestAccuracy: testAccuracy, TestFlip: testFlip,
		})
	}
	sort.Slice(questions, func(i, j int) bool { return questions[i].QuestionSHA256 < questions[j].QuestionSHA256 })
	return questions, nil
}

func normalizeAnyJevExitJevModeRules(value any, questions []anyJevExitJevModeQuestion, perBlock []anyJevExitJevModeBlockImport, blocks []int, nBlocks, recommended int) ([]anyJevExitJevModeRule, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) < 2 || len(rows) > 4 {
		return nil, errors.New("AnyJev Jev-mode rules are invalid")
	}
	lastBlock := blocks[len(blocks)-1]
	if _, ok := rows["cv"]; !ok {
		return nil, errors.New("AnyJev Jev-mode rules omit cross-validation selection")
	}
	if _, ok := rows[fmt.Sprintf("fixed@%d", recommended)]; !ok {
		return nil, errors.New("AnyJev Jev-mode rules omit the recommended fixed block")
	}
	if _, ok := rows[fmt.Sprintf("fixed@%d", lastBlock)]; !ok {
		return nil, errors.New("AnyJev Jev-mode rules omit the full-depth fixed block")
	}
	keys := sortedKeys(rows)
	rules := make([]anyJevExitJevModeRule, 0, len(rows))
	perBlockByIndex := make(map[int]anyJevExitJevModeBlockImport, len(perBlock))
	for _, row := range perBlock {
		perBlockByIndex[row.Block] = row
	}
	for _, name := range keys {
		kind := "cv"
		block := 0
		if name != "cv" {
			if !strings.HasPrefix(name, "fixed@") {
				return nil, errors.New("AnyJev Jev-mode rule name is unsupported")
			}
			parsed, err := strconv.Atoi(strings.TrimPrefix(name, "fixed@"))
			if err != nil || strconv.Itoa(parsed) != strings.TrimPrefix(name, "fixed@") || !containsInt(blocks, parsed) {
				return nil, errors.New("AnyJev Jev-mode fixed rule block is invalid")
			}
			kind, block = "fixed", parsed
			if block != recommended && block != lastBlock {
				return nil, errors.New("AnyJev Jev-mode has an unrecognized fixed rule")
			}
		}
		row, ok := rows[name].(map[string]any)
		if !ok || len(row) != len(anyJevExitJevModeRuleFields) {
			return nil, errors.New("AnyJev Jev-mode rule metric shape is invalid")
		}
		for key := range row {
			if !anyJevExitJevModeRuleFields[key] {
				return nil, fmt.Errorf("unsupported AnyJev Jev-mode rule metric %q", key)
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
		flip, err := anyJevExitNumber(row["flip"], 0, 1)
		if err != nil {
			return nil, err
		}
		nll, err := anyJevExitNumber(row["nll"], 0, 1e6)
		if err != nil {
			return nil, err
		}
		meanBlocks, err := anyJevExitNumber(row["mean_blocks"], 1, float64(nBlocks))
		if err != nil {
			return nil, err
		}
		relative, err := anyJevExitNumber(row["relative_depth"], 0, 1)
		if err != nil {
			return nil, err
		}
		accCI, err := anyJevExitFloatArray(row["acc_ci95"], 2, 0, 1)
		if err != nil || accCI[0] > accCI[1] {
			return nil, errors.New("AnyJev Jev-mode rule accuracy interval is invalid")
		}
		diffCI, err := anyJevExitFloatArray(row["diff_vs_full_ci95"], 2, -1, 1)
		if err != nil || diffCI[0] > diffCI[1] {
			return nil, errors.New("AnyJev Jev-mode rule difference interval is invalid")
		}
		projected := anyJevExitJevModeRule{
			Kind: kind, Accuracy: accuracy, AccuracyCI95: accCI, DiffFullCI95: diffCI,
			ECE: ece, Flip: flip, MeanBlocks: meanBlocks, NLL: nll, RelativeDepth: relative,
		}
		if kind == "cv" {
			cvMean := 0.0
			for _, question := range questions {
				cvMean += float64(question.CVBlock)
			}
			cvMean /= float64(len(questions))
			if absAnyJevFloat(meanBlocks-cvMean) > 2e-6 {
				return nil, errors.New("AnyJev Jev-mode cross-validation mean depth does not reconcile")
			}
		} else {
			blockCopy := block
			projected.Block = &blockCopy
			if absAnyJevFloat(meanBlocks-float64(block)) > 1e-6 {
				return nil, errors.New("AnyJev Jev-mode fixed rule block does not reconcile")
			}
			per, exists := perBlockByIndex[block]
			if !exists || absAnyJevFloat(accuracy-per.Accuracy) > 2e-6 || absAnyJevFloat(ece-per.ECE) > 2e-6 ||
				absAnyJevFloat(flip-per.Flip) > 2e-6 || absAnyJevFloat(nll-per.NLL) > 2e-6 ||
				absAnyJevFloat(accCI[0]-per.AccuracyCI95[0]) > 2e-6 || absAnyJevFloat(accCI[1]-per.AccuracyCI95[1]) > 2e-6 ||
				absAnyJevFloat(diffCI[0]-per.DiffFullCI95[0]) > 2e-6 || absAnyJevFloat(diffCI[1]-per.DiffFullCI95[1]) > 2e-6 {
				return nil, errors.New("AnyJev Jev-mode fixed rule metrics do not reconcile with per-block metrics")
			}
		}
		if absAnyJevFloat(relative-meanBlocks/float64(nBlocks)) > 2e-6 {
			return nil, errors.New("AnyJev Jev-mode rule relative depth does not reconcile")
		}
		rules = append(rules, projected)
	}
	return rules, nil
}

func normalizeAnyJevExitJevModeReferences(value any) ([]anyJevExitJevModeReference, error) {
	if value == nil {
		return []anyJevExitJevModeReference{}, nil
	}
	rows, ok := value.(map[string]any)
	if !ok || len(rows) > 3 {
		return nil, errors.New("AnyJev Jev-mode references are invalid")
	}
	references := make([]anyJevExitJevModeReference, 0, len(rows))
	for name, raw := range rows {
		if !oneOf(name, "raw", "L0", "L1") {
			return nil, errors.New("AnyJev Jev-mode reference name is invalid")
		}
		row, ok := raw.(map[string]any)
		if !ok || len(row) != 3 {
			return nil, errors.New("AnyJev Jev-mode reference shape is invalid")
		}
		for key := range row {
			if key != "acc" && key != "ece" && key != "path" {
				return nil, errors.New("AnyJev Jev-mode reference has an unknown field")
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
		if _, ok := boundedAnyJevBenchmarkString(row["path"], 4096); !ok {
			return nil, errors.New("AnyJev Jev-mode reference path is invalid")
		}
		references = append(references, anyJevExitJevModeReference{Name: name, Accuracy: accuracy, ECE: ece})
	}
	sort.Slice(references, func(i, j int) bool { return references[i].Name < references[j].Name })
	return references, nil
}

func validateAnyJevExitJevModeLatency(value any, nBlocks int) error {
	if value == nil {
		return nil
	}
	latency, ok := value.(map[string]any)
	if !ok || len(latency) != 8 {
		return errors.New("AnyJev Jev-mode embedded latency reference is invalid")
	}
	allowed := map[string]bool{"batch_ms": true, "gflops": true, "measured_blocks": true, "path": true, "raw_batch_ms": true, "raw_gflops": true, "raw_single_ms": true, "single_ms": true}
	for key := range latency {
		if !allowed[key] {
			return errors.New("AnyJev Jev-mode latency reference has an unknown field")
		}
	}
	blocks, ok := boundedAnyJevInteger(latency["measured_blocks"], 1, nBlocks)
	if !ok {
		return errors.New("AnyJev Jev-mode embedded latency block is invalid")
	}
	_ = blocks
	if _, ok := boundedAnyJevBenchmarkString(latency["path"], 4096); !ok {
		return errors.New("AnyJev Jev-mode embedded latency path is invalid")
	}
	for _, key := range []string{"batch_ms", "single_ms", "raw_batch_ms", "raw_single_ms", "gflops", "raw_gflops"} {
		if metric, ok := boundedAnyJevNumber(latency[key], 0, 1e9); !ok || metric <= 0 {
			return fmt.Errorf("AnyJev Jev-mode embedded latency metric %q is invalid", key)
		}
	}
	return nil
}

func anyJevExitNumber(value any, min, max float64) (float64, error) {
	number, ok := boundedAnyJevNumber(value, min, max)
	if !ok {
		return 0, errors.New("AnyJev exit metric is invalid")
	}
	return number, nil
}

func anyJevExitFloatArray(value any, length int, min, max float64) ([]float64, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) != length {
		return nil, errors.New("AnyJev exit metric array has invalid length")
	}
	values := make([]float64, 0, len(raw))
	for _, entry := range raw {
		number, ok := boundedAnyJevNumber(entry, min, max)
		if !ok {
			return nil, errors.New("AnyJev exit metric array contains an invalid value")
		}
		values = append(values, number)
	}
	return values, nil
}

func containsInt(values []int, item int) bool {
	for _, value := range values {
		if value == item {
			return true
		}
	}
	return false
}
