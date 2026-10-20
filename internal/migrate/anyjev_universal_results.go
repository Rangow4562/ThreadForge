package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevUniversalResultSchema = "threadforge.anyjev-universal-result.v1"

type anyJevUniversalImport struct {
	SchemaVersion       string                     `json:"schema_version"`
	StudyKind           string                     `json:"study_kind"`
	ModelSHA256         string                     `json:"model_sha256"`
	Layers              []int                      `json:"layers"`
	AbsoluteLayers      []int                      `json:"absolute_layers"`
	Forms               []string                   `json:"forms"`
	Lambdas             []float64                  `json:"lambdas"`
	TrainOrder          string                     `json:"train_order,omitempty"`
	Extraction          string                     `json:"extraction,omitempty"`
	Seed                *int                       `json:"seed,omitempty"`
	Rows                []anyJevUniversalResultRow `json:"rows"`
	OmittedFields       []string                   `json:"omitted_fields"`
	HumanReviewRequired bool                       `json:"human_review_required"`
	ExecutionVerified   bool                       `json:"execution_verified"`
	CutoverEligible     bool                       `json:"cutover_evidence_eligible"`
}

type anyJevUniversalResultRow struct {
	Scope           string                        `json:"scope"`
	Method          string                        `json:"method"`
	UnitKeySHA256   string                        `json:"unit_key_sha256,omitempty"`
	WorkflowSHA256  string                        `json:"workflow_sha256,omitempty"`
	Domain          string                        `json:"domain,omitempty"`
	Kind            string                        `json:"kind,omitempty"`
	Options         int                           `json:"options,omitempty"`
	Examples        int                           `json:"examples,omitempty"`
	Index           int                           `json:"index,omitempty"`
	FeatureVariant  string                        `json:"feature_variant,omitempty"`
	Block           int                           `json:"block,omitempty"`
	SelectionMethod string                        `json:"selection_method,omitempty"`
	HeadSelection   *anyJevUniversalHeadSelection `json:"head_selection,omitempty"`
	Metrics         map[string]float64            `json:"metrics"`
	Intervals       map[string][]float64          `json:"intervals,omitempty"`
}

type anyJevUniversalHeadSelection struct {
	Layer       int     `json:"layer"`
	LayerAbs    int     `json:"layer_absolute"`
	Lambda      float64 `json:"lambda"`
	Temperature float64 `json:"temperature"`
	OOFNLL      float64 `json:"oof_nll"`
	OOFAccuracy float64 `json:"oof_accuracy"`
	Folds       int     `json:"folds"`
}

var anyJevUniversalRootFields = []string{
	"by_domain", "date", "env", "forms", "layer_abs", "layers", "lams", "model",
	"pooled", "protocol", "questions", "ref", "seed", "train_order", "which",
}
var anyJevUniversalE0RootFields = []string{
	"date", "e01_avg", "e01_id", "e01_rand", "e02_chosen_layers", "e02_depth", "e03", "env", "layers", "model",
}

func isAnyJevUniversalResultJSONPath(relative string) bool {
	_, _, _, ok := anyJevUniversalResultPath(relative)
	return ok
}

func anyJevUniversalResultPath(relative string) (studyKind, dateDirectory, fileName string, ok bool) {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 4 || !strings.EqualFold(parts[0], "bench") || !strings.EqualFold(parts[1], "results_universal") {
		return "", "", "", false
	}
	fileName = parts[3]
	lowerName := strings.ToLower(fileName)
	if strings.EqualFold(parts[2], "e0") && strings.HasSuffix(lowerName, ".e0.json") && len(lowerName) > len(".e0.json") {
		return "phase0", parts[2], fileName, true
	}
	if _, err := parseAnyJevMigrationDate(parts[2]); err != nil {
		return "", "", "", false
	}
	if strings.HasSuffix(lowerName, ".json") && strings.Contains(lowerName, ".loqo.") {
		return "loqo", parts[2], fileName, true
	}
	if strings.HasSuffix(lowerName, ".json") && strings.Contains(lowerName, ".cross.") {
		return "cross_domain", parts[2], fileName, true
	}
	return "", "", "", false
}

func normalizeAnyJevUniversalResult(relative string, value any) (normalizedItem, error) {
	studyKind, _, name, recognized := anyJevUniversalResultPath(relative)
	if !recognized {
		return normalizedItem{}, errors.New("unsupported AnyJev universal-result path")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev universal-result shape")
	}
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	date, dateOK := boundedAnyJevBenchmarkString(root["date"], 64)
	env, envOK := root["env"].(map[string]any)
	if !modelOK || privacy.ContainsCredentialText(model) || !dateOK || !validAnyJevLatencyTimestamp(date) ||
		!envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev universal-result provenance fields are invalid")
	}
	filename := strings.ToLower(name)
	modelSlug := strings.ToLower(strings.ReplaceAll(model, "/", "__"))
	projected := anyJevUniversalImport{
		SchemaVersion: anyJevUniversalResultSchema, StudyKind: studyKind,
		ModelSHA256: anyJevStringSHA256(model), Layers: []int{}, AbsoluteLayers: []int{},
		Forms: []string{}, Lambdas: []float64{}, Rows: []anyJevUniversalResultRow{},
		OmittedFields:       []string{"model_label", "date", "environment_details", "reference_paths", "question_and_workflow_labels"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	var err error
	if studyKind == "phase0" {
		if err := requireAnyJevUniversalFields(root, anyJevUniversalE0RootFields); err != nil {
			return normalizedItem{}, err
		}
		if filename != modelSlug+".e0.json" || !strings.EqualFold(filepathSlash(relative), "bench/results_universal/e0/"+name) {
			return normalizedItem{}, errors.New("AnyJev phase-0 filename or location does not match its model")
		}
		projected.Layers, err = normalizeAnyJevUniversalIntegerList(root["layers"], 1, 256, 1, 10000, "phase-0 layers")
		if err != nil {
			return normalizedItem{}, err
		}
		projected.Rows, err = normalizeAnyJevUniversalE0(root, projected.Layers)
	} else {
		if err := requireAnyJevUniversalFields(root, anyJevUniversalRootFields); err != nil {
			return normalizedItem{}, err
		}
		projected.Rows, projected.Layers, projected.AbsoluteLayers, projected.Forms, projected.Lambdas,
			projected.TrainOrder, projected.Extraction, projected.Seed, err = normalizeAnyJevUniversalProtocol(root, filename, modelSlug, studyKind)
	}
	if err != nil {
		return normalizedItem{}, err
	}
	sort.Slice(projected.Rows, func(i, j int) bool {
		a, b := projected.Rows[i], projected.Rows[j]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.Domain != b.Domain {
			return a.Domain < b.Domain
		}
		if a.UnitKeySHA256 != b.UnitKeySHA256 {
			return a.UnitKeySHA256 < b.UnitKeySHA256
		}
		if a.Index != b.Index {
			return a.Index < b.Index
		}
		if a.FeatureVariant != b.FeatureVariant {
			return a.FeatureVariant < b.FeatureVariant
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Block < b.Block
	})
	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev universal-result projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevUniversalProtocol(root map[string]any, filename, modelSlug, studyKind string) (
	rows []anyJevUniversalResultRow, layers, absoluteLayers []int, forms []string, lambdas []float64,
	trainOrder, extraction string, seed *int, err error,
) {
	protocol, ok := root["protocol"].(string)
	if !ok || (protocol != "loqo" && protocol != "cross") ||
		(protocol == "loqo" && studyKind != "loqo") || (protocol == "cross" && studyKind != "cross_domain") {
		return nil, nil, nil, nil, nil, "", "", nil, errors.New("AnyJev universal-result protocol is invalid")
	}
	trainOrder, ok = root["train_order"].(string)
	if !ok || (trainOrder != "id" && trainOrder != "rand") {
		return nil, nil, nil, nil, nil, "", "", nil, errors.New("AnyJev universal-result training order is invalid")
	}
	extraction, ok = root["which"].(string)
	if !ok || (extraction != "last" && extraction != "newline") {
		return nil, nil, nil, nil, nil, "", "", nil, errors.New("AnyJev universal-result feature extraction is invalid")
	}
	seedValue, seedOK := boundedAnyJevInteger(root["seed"], 0, 1000000)
	if !seedOK {
		return nil, nil, nil, nil, nil, "", "", nil, errors.New("AnyJev universal-result seed is invalid")
	}
	seed = &seedValue
	layers, err = normalizeAnyJevUniversalIntegerList(root["layers"], 1, 128, -10000, 10000, "relative layer list")
	if err != nil {
		return
	}
	absRaw, ok := root["layer_abs"].([]any)
	if !ok || len(absRaw) != len(layers) {
		err = errors.New("AnyJev universal-result absolute layers do not match the relative list")
		return
	}
	absSeen := make(map[int]struct{}, len(absRaw))
	for _, raw := range absRaw {
		block, valid := boundedAnyJevInteger(raw, 1, 10000)
		if !valid {
			err = errors.New("AnyJev universal-result absolute layer index is invalid")
			return
		}
		if _, duplicate := absSeen[block]; duplicate {
			err = errors.New("AnyJev universal-result absolute layer list contains duplicates")
			return
		}
		absSeen[block] = struct{}{}
		absoluteLayers = append(absoluteLayers, block)
	}
	forms, err = normalizeAnyJevUniversalCSV(root["forms"], map[string]bool{
		"probe": true, "clogit": true, "residual": true, "pos": true, "prod": true,
	}, "forms")
	if err != nil {
		return
	}
	if len(forms) == 0 || (protocol == "loqo" && len(forms) > 3) || (protocol == "cross" && len(forms) > 3) {
		err = errors.New("AnyJev universal-result form list is invalid")
		return
	}
	expectedName := modelSlug + "." + protocol + "." + trainOrder + "." + extraction + ".json"
	if filename != expectedName {
		posName := modelSlug + "." + protocol + "." + trainOrder + "." + extraction + ".pos.json"
		if protocol != "loqo" || filename != posName || len(forms) != 1 || forms[0] != "pos" {
			err = errors.New("AnyJev universal-result filename does not match its study settings")
			return
		}
	}
	lambdaText, ok := root["lams"].(string)
	if !ok {
		err = errors.New("AnyJev universal-result regularization values are invalid")
		return
	}
	for _, part := range strings.Split(lambdaText, ",") {
		part = strings.TrimSpace(part)
		value, parseErr := strconv.ParseFloat(part, 64)
		if parseErr != nil || value <= 0 || value > 1e6 {
			err = errors.New("AnyJev universal-result regularization value is outside bounds")
			return
		}
		for _, existing := range lambdas {
			if existing == value {
				err = errors.New("AnyJev universal-result regularization values contain duplicates")
				return
			}
		}
		lambdas = append(lambdas, value)
	}
	if len(lambdas) == 0 || len(lambdas) > 16 {
		err = errors.New("AnyJev universal-result regularization list is invalid")
		return
	}

	questions, ok := root["questions"].(map[string]any)
	if !ok || len(questions) == 0 || len(questions) > 256 {
		err = errors.New("AnyJev universal-result question summaries are invalid")
		return
	}
	questionMethods := []string{"raw", "L1", "per-question head"}
	pooledMethods := []string{"raw", "L1", "L0-perm", "per-question head"}
	if protocol == "loqo" {
		questionMethods = append(questionMethods, "L0-perm")
	}
	for _, form := range forms {
		method := "universal:" + form
		questionMethods = append(questionMethods, method)
		pooledMethods = append(pooledMethods, method)
	}
	questionMethodSet := anyJevUniversalSet(questionMethods)
	pooledMethodSet := anyJevUniversalSet(pooledMethods)
	questionRows := make(map[string]map[string]anyJevUniversalResultRow, len(questions))
	questionN := make(map[string]int, len(questions))
	questionDomain := make(map[string]string, len(questions))
	questionAccuracy := make(map[string]map[string]float64, len(questions))
	questionMethodExamples := make(map[string]int, len(questionMethodSet))
	var totalExamples int
	for rawKey, rawQuestion := range questions {
		if rawKey == "" || len(rawKey) > 512 {
			err = errors.New("AnyJev universal-result question key is invalid")
			return
		}
		question, ok := rawQuestion.(map[string]any)
		if !ok {
			err = errors.New("AnyJev universal-result question entry is invalid")
			return
		}
		if err = requireAnyJevUniversalFields(question, []string{"domain", "heads", "k", "kind", "n_test", "rows", "workflow"}); err != nil {
			return
		}
		kind, kindOK := question["kind"].(string)
		workflow, workflowOK := boundedAnyJevBenchmarkString(question["workflow"], 256)
		domain, domainOK := question["domain"].(string)
		k, kOK := boundedAnyJevInteger(question["k"], 2, 26)
		nTest, nOK := boundedAnyJevInteger(question["n_test"], 1, 100000)
		if !kindOK || (kind != "choice" && kind != "noul" && kind != "score") || !workflowOK ||
			!domainOK || !oneOf(domain, "typed", "extra", "games") || !kOK || !nOK ||
			(protocol == "loqo" && domain != "typed") {
			err = errors.New("AnyJev universal-result question metadata is invalid")
			return
		}
		heads, ok := question["heads"].(map[string]any)
		if !ok || len(heads) != len(forms) {
			err = errors.New("AnyJev universal-result question head selections are incomplete")
			return
		}
		for _, form := range forms {
			if _, exists := heads[form]; !exists {
				err = errors.New("AnyJev universal-result question is missing a declared head selection")
				return
			}
		}
		expectedQuestionMethods := questionMethodSet
		if protocol == "cross" {
			expectedQuestionMethods = anyJevUniversalSet([]string{"raw", "L1", "per-question head"})
			if domain == "typed" {
				expectedQuestionMethods["L0-perm"] = struct{}{}
			}
			for _, form := range forms {
				expectedQuestionMethods["universal:"+form] = struct{}{}
			}
		}
		rowsRaw, ok := question["rows"].(map[string]any)
		if !ok || len(rowsRaw) != len(expectedQuestionMethods) {
			err = errors.New("AnyJev universal-result per-question metric matrix is invalid")
			return
		}
		unitHash := anyJevStringSHA256(rawKey)
		workflowHash := anyJevStringSHA256(workflow)
		convertedRows := make(map[string]anyJevUniversalResultRow, len(rowsRaw))
		accuracies := make(map[string]float64, len(rowsRaw))
		for method, rawMetrics := range rowsRaw {
			if _, expected := expectedQuestionMethods[method]; !expected {
				err = errors.New("AnyJev universal-result question has an unsupported method row")
				return
			}
			metrics, intervals, _, metricErr := normalizeAnyJevUniversalMetrics(rawMetrics, "question")
			if metricErr != nil {
				err = metricErr
				return
			}
			row := anyJevUniversalResultRow{
				Scope: "question", Method: method, UnitKeySHA256: unitHash, WorkflowSHA256: workflowHash,
				Domain: domain, Kind: kind, Options: k, Examples: nTest, Metrics: metrics, Intervals: intervals,
			}
			if strings.HasPrefix(method, "universal:") {
				form := strings.TrimPrefix(method, "universal:")
				head, headErr := normalizeAnyJevUniversalHeadSelection(heads[form], layers, absoluteLayers, lambdas)
				if headErr != nil {
					err = headErr
					return
				}
				row.HeadSelection = &head
			}
			convertedRows[method] = row
			accuracies[method] = metrics["acc"]
			questionMethodExamples[method] += nTest
			rows = append(rows, row)
		}
		questionRows[unitHash] = convertedRows
		questionAccuracy[unitHash] = accuracies
		questionN[unitHash] = nTest
		questionDomain[unitHash] = domain
		totalExamples += nTest
		if totalExamples > 10000000 {
			err = errors.New("AnyJev universal-result test-example count exceeds bounds")
			return
		}
	}
	pooledRaw, ok := root["pooled"].(map[string]any)
	if !ok || len(pooledRaw) != len(pooledMethodSet) {
		err = errors.New("AnyJev universal-result pooled method matrix is invalid")
		return
	}
	pooledValues := make(map[string]normalizedAnyJevUniversalMetrics, len(pooledRaw))
	for method, rawMetrics := range pooledRaw {
		if _, expected := pooledMethodSet[method]; !expected {
			err = errors.New("AnyJev universal-result pooled method is unsupported")
			return
		}
		metrics, intervals, n, metricErr := normalizeAnyJevUniversalMetrics(rawMetrics, "pooled")
		if metricErr != nil {
			err = metricErr
			return
		}
		if n != questionMethodExamples[method] {
			err = errors.New("AnyJev universal-result pooled count does not match question counts")
			return
		}
		pooledValues[method] = normalizedAnyJevUniversalMetrics{metrics: metrics, intervals: intervals, examples: n}
		rows = append(rows, anyJevUniversalResultRow{Scope: "pooled", Method: method, Examples: n, Metrics: metrics, Intervals: intervals})
		if _, exists := questionMethodSet[method]; exists && questionMethodExamples[method] == totalExamples &&
			anyJevUniversalRowsCoverMethod(questionAccuracy, method) {
			if !anyJevUniversalQuestionRowsReconcile(questionAccuracy, questionN, method, metrics["acc"], totalExamples) {
				err = errors.New("AnyJev universal-result pooled accuracy does not reconcile to per-question rows")
				return
			}
		}
	}
	byDomain, ok := root["by_domain"].(map[string]any)
	if !ok {
		err = errors.New("AnyJev universal-result domain summaries are invalid")
		return
	}
	if protocol == "loqo" {
		if len(byDomain) != 0 {
			err = errors.New("LOQO result unexpectedly contains domain aggregates")
			return
		}
	} else {
		if len(byDomain) != 3 || !hasAllStringKeys(byDomain, []string{"extra", "games", "typed"}) {
			err = errors.New("cross-domain result must contain exactly typed, extra, and games aggregates")
			return
		}
		domainBaseMethods := append([]string{"raw", "L1", "per-question head"}, universalMethodNames(forms)...)
		for domain, rawMethods := range byDomain {
			methods, ok := rawMethods.(map[string]any)
			if !ok {
				err = errors.New("cross-domain aggregate method matrix is invalid")
				return
			}
			domainExamples := 0
			domainMethodExamples := make(map[string]int)
			for unitHash, value := range questionDomain {
				if value == domain {
					domainExamples += questionN[unitHash]
					for method := range questionAccuracy[unitHash] {
						domainMethodExamples[method] += questionN[unitHash]
					}
				}
			}
			if domainExamples == 0 {
				err = errors.New("cross-domain result contains an empty domain")
				return
			}
			domainMethodSet := anyJevUniversalSet(domainBaseMethods)
			if domainMethodExamples["L0-perm"] > 0 {
				domainMethodSet["L0-perm"] = struct{}{}
			}
			if len(methods) != len(domainMethodSet) {
				err = errors.New("cross-domain aggregate method matrix is invalid")
				return
			}
			for method, rawMetrics := range methods {
				if _, expected := domainMethodSet[method]; !expected {
					err = errors.New("cross-domain aggregate has an unsupported method")
					return
				}
				metrics, intervals, n, metricErr := normalizeAnyJevUniversalMetrics(rawMetrics, "domain")
				if metricErr != nil {
					err = metricErr
					return
				}
				if n != domainMethodExamples[method] || (anyJevUniversalDomainRowsCoverMethod(questionAccuracy, questionDomain, method, domain) &&
					!anyJevUniversalQuestionRowsReconcileDomain(questionAccuracy, questionN, questionDomain, method, domain, metrics["acc"], domainExamples)) {
					err = errors.New("cross-domain aggregate does not reconcile to its question rows")
					return
				}
				rows = append(rows, anyJevUniversalResultRow{Scope: "domain", Method: method, Domain: domain, Examples: n, Metrics: metrics, Intervals: intervals})
			}
		}
	}
	return
}

type normalizedAnyJevUniversalMetrics struct {
	metrics   map[string]float64
	intervals map[string][]float64
	examples  int
}

func normalizeAnyJevUniversalMetrics(value any, scope string) (map[string]float64, map[string][]float64, int, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, nil, 0, errors.New("AnyJev universal-result metric row is invalid")
	}
	allowed := map[string]bool{"acc": true, "ece": true, "brier": true, "nll": true, "cov5": true, "flip": true}
	required := []string{"acc", "ece", "brier", "nll", "cov5"}
	if scope != "question" {
		allowed = map[string]bool{
			"acc": true, "ece": true, "brier": true, "nll": true, "n": true,
			"acc_ci95": true, "diff_vs_L0-perm_ci95": true, "diff_vs_raw_ci95": true, "flip": true,
		}
		required = []string{"acc", "ece", "brier", "nll", "n"}
	}
	for key := range object {
		if !allowed[key] {
			return nil, nil, 0, fmt.Errorf("unsupported AnyJev universal-result metric field %q", key)
		}
	}
	for _, key := range required {
		if _, exists := object[key]; !exists {
			return nil, nil, 0, fmt.Errorf("AnyJev universal-result metric row is missing %q", key)
		}
	}
	metrics := make(map[string]float64)
	intervals := make(map[string][]float64)
	examples := 0
	for key, raw := range object {
		if key == "n" {
			var valid bool
			examples, valid = boundedAnyJevInteger(raw, 1, 10000000)
			if !valid {
				return nil, nil, 0, errors.New("AnyJev universal-result example count is invalid")
			}
			continue
		}
		if key == "acc_ci95" || key == "diff_vs_L0-perm_ci95" || key == "diff_vs_raw_ci95" {
			minimum, maximum := 0.0, 1.0
			if key != "acc_ci95" {
				minimum, maximum = -1, 1
			}
			interval, ok := normalizeAnyJevParaphraseInterval(raw, minimum, maximum)
			if !ok {
				return nil, nil, 0, errors.New("AnyJev universal-result confidence interval is invalid")
			}
			intervals[key] = interval
			continue
		}
		minimum, maximum := 0.0, 1.0
		if key == "brier" {
			maximum = 2
		} else if key == "nll" {
			maximum = 1e6
		}
		number, valid := boundedAnyJevNumber(raw, minimum, maximum)
		if !valid {
			return nil, nil, 0, fmt.Errorf("AnyJev universal-result metric %q is invalid", key)
		}
		metrics[key] = number
	}
	return metrics, intervals, examples, nil
}

func normalizeAnyJevUniversalHeadSelection(value any, layers, absoluteLayers []int, lambdas []float64) (anyJevUniversalHeadSelection, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 7 {
		return anyJevUniversalHeadSelection{}, errors.New("AnyJev universal-result head selection is invalid")
	}
	if err := requireAnyJevUniversalFields(object, []string{"folds", "lam", "layer", "layer_abs", "oof_acc", "oof_nll", "temperature"}); err != nil {
		return anyJevUniversalHeadSelection{}, err
	}
	layer, layerOK := boundedAnyJevInteger(object["layer"], 0, len(layers)-1)
	layerAbs, absOK := boundedAnyJevInteger(object["layer_abs"], 1, 10000)
	lam, lamOK := boundedAnyJevNumber(object["lam"], 0, 1e6)
	temperature, tempOK := boundedAnyJevNumber(object["temperature"], 1e-12, 1e6)
	oofNLL, nllOK := boundedAnyJevNumber(object["oof_nll"], 0, 1e6)
	oofAcc, accOK := boundedAnyJevNumber(object["oof_acc"], 0, 1)
	folds, foldsOK := boundedAnyJevInteger(object["folds"], 2, 20)
	if !layerOK || !absOK || !lamOK || !tempOK || !nllOK || !accOK || !foldsOK ||
		layerAbs != absoluteLayers[layer] {
		return anyJevUniversalHeadSelection{}, errors.New("AnyJev universal-result head selection dimensions or metrics are invalid")
	}
	if !anyJevUniversalFloatInList(lam, lambdas) {
		return anyJevUniversalHeadSelection{}, errors.New("AnyJev universal-result head lambda is not declared")
	}
	return anyJevUniversalHeadSelection{Layer: layer, LayerAbs: layerAbs, Lambda: lam,
		Temperature: temperature, OOFNLL: oofNLL, OOFAccuracy: oofAcc, Folds: folds}, nil
}

func normalizeAnyJevUniversalE0(root map[string]any, layers []int) ([]anyJevUniversalResultRow, error) {
	rows := make([]anyJevUniversalResultRow, 0, 128)
	e01Fields := []string{"feat", "mean", "per_question"}
	var baselineKeys map[string]struct{}
	for _, feature := range []string{"id", "rand", "avg"} {
		section, ok := root["e01_"+feature].(map[string]any)
		if !ok {
			return nil, errors.New("AnyJev phase-0 E01 section is invalid")
		}
		if err := requireAnyJevUniversalFields(section, e01Fields); err != nil {
			return nil, err
		}
		featureName, ok := section["feat"].(string)
		if !ok || featureName != feature {
			return nil, errors.New("AnyJev phase-0 feature variant does not match its section")
		}
		questions, ok := section["per_question"].(map[string]any)
		if !ok || len(questions) == 0 || len(questions) > 256 {
			return nil, errors.New("AnyJev phase-0 per-question summaries are invalid")
		}
		if baselineKeys == nil {
			baselineKeys = make(map[string]struct{}, len(questions))
			for key := range questions {
				baselineKeys[key] = struct{}{}
			}
		} else if len(questions) != len(baselineKeys) {
			return nil, errors.New("AnyJev phase-0 feature variants have different question coverage")
		}
		for key, value := range questions {
			if _, exists := baselineKeys[key]; !exists {
				return nil, errors.New("AnyJev phase-0 feature variants have different question identities")
			}
			metrics, err := normalizeAnyJevUniversalE0Stats(value, true, layers)
			if err != nil {
				return nil, err
			}
			rows = append(rows, anyJevUniversalResultRow{Scope: "phase0_question", Method: "feature_summary",
				UnitKeySHA256: anyJevStringSHA256(key), FeatureVariant: feature, Metrics: metrics})
		}
		mean, err := normalizeAnyJevUniversalE0Stats(section["mean"], false, layers)
		if err != nil {
			return nil, err
		}
		rows = append(rows, anyJevUniversalResultRow{Scope: "phase0_mean", Method: "feature_summary",
			FeatureVariant: feature, Metrics: mean})
	}
	depth, ok := root["e02_depth"].(map[string]any)
	if !ok || len(depth) != len(layers) {
		return nil, errors.New("AnyJev phase-0 depth summary is invalid")
	}
	for key, raw := range depth {
		block, err := strconv.Atoi(key)
		if err != nil || strconv.Itoa(block) != key || !anyJevUniversalIntInList(block, layers) {
			return nil, errors.New("AnyJev phase-0 depth block is invalid")
		}
		object, ok := raw.(map[string]any)
		if !ok || len(object) != 3 {
			return nil, errors.New("AnyJev phase-0 depth metrics are invalid")
		}
		if err := requireAnyJevUniversalFields(object, []string{"acc", "ece_pooled", "nll"}); err != nil {
			return nil, err
		}
		metrics, err := normalizeAnyJevUniversalNumericMetrics(object, map[string][2]float64{
			"acc": {0, 1}, "ece_pooled": {0, 1}, "nll": {0, 1e6},
		})
		if err != nil {
			return nil, err
		}
		rows = append(rows, anyJevUniversalResultRow{Scope: "phase0_depth", Method: "per_question_head", Block: block, Metrics: metrics})
	}
	chosen, ok := root["e02_chosen_layers"].(map[string]any)
	if !ok || len(chosen) != 2 {
		return nil, errors.New("AnyJev phase-0 selected-layer summary is invalid")
	}
	if err := requireAnyJevUniversalFields(chosen, []string{"histogram", "per_question"}); err != nil {
		return nil, err
	}
	histogram, ok := chosen["histogram"].(map[string]any)
	if !ok || len(histogram) == 0 || len(histogram) > 64 {
		return nil, errors.New("AnyJev phase-0 selected-layer histogram is invalid")
	}
	selectedQuestions, ok := chosen["per_question"].(map[string]any)
	if !ok || len(selectedQuestions) == 0 || len(selectedQuestions) > 256 {
		return nil, errors.New("AnyJev phase-0 per-question layer choices are invalid")
	}
	histogramCount := 0
	for key, rawCount := range histogram {
		block, parseErr := strconv.Atoi(key)
		count, countOK := boundedAnyJevInteger(rawCount, 0, len(selectedQuestions))
		if parseErr != nil || strconv.Itoa(block) != key || !anyJevUniversalIntInList(block, layers) || !countOK {
			return nil, errors.New("AnyJev phase-0 layer histogram contains an invalid entry")
		}
		histogramCount += count
		rows = append(rows, anyJevUniversalResultRow{Scope: "phase0_layer_histogram", Method: "selected_layer_count",
			Block: block, Metrics: map[string]float64{"count": float64(count)}})
	}
	if histogramCount != len(selectedQuestions) {
		return nil, errors.New("AnyJev phase-0 layer histogram does not reconcile to selected questions")
	}
	selectionMethods := map[string]bool{
		"head:lda": true, "head:lda/avg": true, "head:lda/rand": true,
		"head:ridge": true, "head:ridge/avg": true, "head:ridge/rand": true,
	}
	for key, raw := range selectedQuestions {
		object, ok := raw.(map[string]any)
		if !ok || len(object) != 2 {
			return nil, errors.New("AnyJev phase-0 selected-layer question row is invalid")
		}
		if err := requireAnyJevUniversalFields(object, []string{"layer", "method"}); err != nil {
			return nil, err
		}
		method, methodOK := object["method"].(string)
		block, blockOK := boundedAnyJevInteger(object["layer"], 1, 10000)
		if !methodOK || !selectionMethods[method] || !blockOK || !anyJevUniversalIntInList(block, layers) {
			return nil, errors.New("AnyJev phase-0 selected-layer method or block is invalid")
		}
		rows = append(rows, anyJevUniversalResultRow{Scope: "phase0_selected_layer", Method: "selected_head",
			UnitKeySHA256: anyJevStringSHA256(key), Block: block, SelectionMethod: method, Metrics: map[string]float64{}})
	}
	e03, ok := root["e03"].(map[string]any)
	if !ok || len(e03) != 7 {
		return nil, errors.New("AnyJev phase-0 E03 comparison table is invalid")
	}
	methods := []string{"raw", "L0-perm", "L1", "head", "head/rand", "logreg"}
	for _, method := range methods {
		value, exists := e03[method]
		if !exists {
			return nil, errors.New("AnyJev phase-0 E03 table omits a comparison method")
		}
		metrics, intervals, examples, err := normalizeAnyJevUniversalE03Metrics(value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, anyJevUniversalResultRow{Scope: "phase0_comparison", Method: method,
			Examples: examples, Metrics: metrics, Intervals: intervals})
	}
	gradient, ok := e03["_gradient_gap"].(map[string]any)
	if !ok || len(gradient) != 2 {
		return nil, errors.New("AnyJev phase-0 gradient reference is invalid")
	}
	if err := requireAnyJevUniversalFields(gradient, []string{"mean_logreg_minus_head", "per_question"}); err != nil {
		return nil, err
	}
	meanGap, meanOK := boundedAnyJevNumber(gradient["mean_logreg_minus_head"], -1, 1)
	gapByQuestion, qOK := gradient["per_question"].([]any)
	if !meanOK || !qOK || len(gapByQuestion) == 0 || len(gapByQuestion) > 256 {
		return nil, errors.New("AnyJev phase-0 gradient gap metrics are invalid")
	}
	rows = append(rows, anyJevUniversalResultRow{Scope: "phase0_gradient_summary", Method: "logreg_minus_head",
		Metrics: map[string]float64{"mean_logreg_minus_head": meanGap}})
	var gapTotal float64
	for index, raw := range gapByQuestion {
		gap, valid := boundedAnyJevNumber(raw, -1, 1)
		if !valid {
			return nil, errors.New("AnyJev phase-0 per-question gradient gap is invalid")
		}
		gapTotal += gap
		rows = append(rows, anyJevUniversalResultRow{Scope: "phase0_gradient_question", Method: "logreg_minus_head",
			Index: index + 1, Metrics: map[string]float64{"logreg_minus_head": gap}})
	}
	if math.Abs(gapTotal/float64(len(gapByQuestion))-meanGap) > 1e-9 {
		return nil, errors.New("AnyJev phase-0 gradient gap mean does not reconcile to its question values")
	}
	return rows, nil
}

func normalizeAnyJevUniversalE0Stats(value any, perQuestion bool, layers []int) (map[string]float64, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("AnyJev phase-0 feature metric map is invalid")
	}
	allowed := map[string]bool{
		"raw": true, "L0-perm": true, "per-question:lda": true,
		"pooled:diff_means": true, "pooled:diff_means:ece": true,
		"pooled:lda": true, "pooled:lda:ece": true,
		"pooled:ridge": true, "pooled:ridge:ece": true,
	}
	if perQuestion {
		allowed["pooled:diff_means:layer"] = true
		allowed["pooled:lda:layer"] = true
		allowed["pooled:ridge:layer"] = true
	}
	if len(object) != len(allowed) {
		return nil, errors.New("AnyJev phase-0 feature metric set is incomplete")
	}
	metrics := make(map[string]float64, len(object))
	maxLayer := 0.0
	for _, layer := range layers {
		if float64(layer) > maxLayer {
			maxLayer = float64(layer)
		}
	}
	for key, raw := range object {
		if !allowed[key] {
			return nil, fmt.Errorf("unsupported AnyJev phase-0 feature metric %q", key)
		}
		minimum, maximum := 0.0, 1.0
		if strings.HasSuffix(key, ":ece") {
			maximum = 1
		} else if strings.HasSuffix(key, ":layer") {
			maximum = maxLayer
		}
		metric, valid := boundedAnyJevNumber(raw, minimum, maximum)
		if !valid {
			return nil, errors.New("AnyJev phase-0 feature metric value is invalid")
		}
		metrics[key] = metric
	}
	return metrics, nil
}

func normalizeAnyJevUniversalE03Metrics(value any) (map[string]float64, map[string][]float64, int, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 10 {
		return nil, nil, 0, errors.New("AnyJev phase-0 E03 method metrics are invalid")
	}
	if err := requireAnyJevUniversalFields(object, []string{
		"acc", "acc_ci95", "brier", "brier_mean", "diff_vs_L0_ci95", "ece", "n", "nll", "score_mae", "soft_acc",
	}); err != nil {
		return nil, nil, 0, err
	}
	n, nOK := boundedAnyJevInteger(object["n"], 1, 10000000)
	if !nOK {
		return nil, nil, 0, errors.New("AnyJev phase-0 E03 sample count is invalid")
	}
	metrics := make(map[string]float64)
	for key, limits := range map[string][2]float64{
		"acc": {0, 1}, "soft_acc": {0, 1}, "ece": {0, 1}, "brier": {0, 2},
		"brier_mean": {0, 2}, "nll": {0, 1e6}, "score_mae": {0, 1e6},
	} {
		metric, valid := boundedAnyJevNumber(object[key], limits[0], limits[1])
		if !valid {
			return nil, nil, 0, fmt.Errorf("AnyJev phase-0 E03 metric %q is invalid", key)
		}
		metrics[key] = metric
	}
	accuracyCI, ok := normalizeAnyJevParaphraseInterval(object["acc_ci95"], 0, 1)
	if !ok {
		return nil, nil, 0, errors.New("AnyJev phase-0 E03 accuracy interval is invalid")
	}
	differenceCI, ok := normalizeAnyJevParaphraseInterval(object["diff_vs_L0_ci95"], -1, 1)
	if !ok {
		return nil, nil, 0, errors.New("AnyJev phase-0 E03 paired interval is invalid")
	}
	return metrics, map[string][]float64{"acc_ci95": accuracyCI, "diff_vs_L0_ci95": differenceCI}, n, nil
}

func normalizeAnyJevUniversalNumericMetrics(value any, allowed map[string][2]float64) (map[string]float64, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != len(allowed) {
		return nil, errors.New("AnyJev universal result metric object is invalid")
	}
	metrics := make(map[string]float64, len(object))
	for key, raw := range object {
		limits, exists := allowed[key]
		if !exists {
			return nil, fmt.Errorf("unsupported AnyJev universal result metric %q", key)
		}
		metric, valid := boundedAnyJevNumber(raw, limits[0], limits[1])
		if !valid {
			return nil, fmt.Errorf("AnyJev universal result metric %q is invalid", key)
		}
		metrics[key] = metric
	}
	return metrics, nil
}

func normalizeAnyJevUniversalIntegerList(value any, minimumCount, maximumCount, minimum, maximum int, label string) ([]int, error) {
	items, ok := value.([]any)
	if !ok || len(items) < minimumCount || len(items) > maximumCount {
		return nil, fmt.Errorf("AnyJev universal-result %s are invalid", label)
	}
	result := make([]int, 0, len(items))
	seen := make(map[int]struct{}, len(items))
	for _, raw := range items {
		item, valid := boundedAnyJevInteger(raw, minimum, maximum)
		if !valid {
			return nil, fmt.Errorf("AnyJev universal-result %s contain an invalid value", label)
		}
		if _, duplicate := seen[item]; duplicate {
			return nil, fmt.Errorf("AnyJev universal-result %s contain duplicates", label)
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result, nil
}

func normalizeAnyJevUniversalCSV(value any, allowed map[string]bool, label string) ([]string, error) {
	text, ok := value.(string)
	if !ok || len(text) == 0 || len(text) > 1024 {
		return nil, fmt.Errorf("AnyJev universal-result %s are invalid", label)
	}
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(text, ",") {
		item := strings.TrimSpace(raw)
		if !allowed[item] {
			return nil, fmt.Errorf("AnyJev universal-result %s contain an unsupported value", label)
		}
		if _, duplicate := seen[item]; duplicate {
			return nil, fmt.Errorf("AnyJev universal-result %s contain duplicates", label)
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result, nil
}

func requireAnyJevUniversalFields(object map[string]any, expected []string) error {
	fields := make(map[string]struct{}, len(expected))
	for _, key := range expected {
		fields[key] = struct{}{}
	}
	if len(object) != len(fields) {
		return errors.New("AnyJev universal-result object has missing or unexpected fields")
	}
	for key := range object {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("unsupported AnyJev universal-result field %q", key)
		}
	}
	return nil
}

func anyJevUniversalSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func universalMethodNames(forms []string) []string {
	methods := make([]string, 0, len(forms))
	for _, form := range forms {
		methods = append(methods, "universal:"+form)
	}
	return methods
}

func anyJevUniversalFloatInList(value float64, list []float64) bool {
	for _, candidate := range list {
		if value == candidate {
			return true
		}
	}
	return false
}

func anyJevUniversalIntInList(value int, list []int) bool {
	for _, candidate := range list {
		if value == candidate {
			return true
		}
	}
	return false
}

func hasAllStringKeys(object map[string]any, expected []string) bool {
	if len(object) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func anyJevUniversalQuestionRowsReconcile(accuracy map[string]map[string]float64, examples map[string]int, method string, pooledAccuracy float64, totalExamples int) bool {
	correct := 0.0
	for unit, rows := range accuracy {
		value, ok := rows[method]
		if !ok {
			return false
		}
		correct += value * float64(examples[unit])
	}
	return math.Abs(correct/float64(totalExamples)-pooledAccuracy) <= 1e-6
}

func anyJevUniversalRowsCoverMethod(accuracy map[string]map[string]float64, method string) bool {
	if len(accuracy) == 0 {
		return false
	}
	for _, rows := range accuracy {
		if _, ok := rows[method]; !ok {
			return false
		}
	}
	return true
}

func anyJevUniversalDomainRowsCoverMethod(accuracy map[string]map[string]float64, domains map[string]string, method, domain string) bool {
	count := 0
	for unit, unitDomain := range domains {
		if unitDomain != domain {
			continue
		}
		count++
		if _, ok := accuracy[unit][method]; !ok {
			return false
		}
	}
	return count > 0
}

func anyJevUniversalQuestionRowsReconcileDomain(accuracy map[string]map[string]float64, examples map[string]int, domains map[string]string, method, domain string, aggregateAccuracy float64, domainExamples int) bool {
	correct := 0.0
	for unit, unitDomain := range domains {
		if unitDomain != domain {
			continue
		}
		value, ok := accuracy[unit][method]
		if !ok {
			return false
		}
		correct += value * float64(examples[unit])
	}
	return math.Abs(correct/float64(domainExamples)-aggregateAccuracy) <= 1e-6
}
