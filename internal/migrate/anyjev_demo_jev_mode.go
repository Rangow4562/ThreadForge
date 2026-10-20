package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevDemoJevModeResultSchema = "threadforge.anyjev-demo-jev-mode-result.v1"

type anyJevDemoJevModeImport struct {
	SchemaVersion       string                   `json:"schema_version"`
	Mode                string                   `json:"mode"`
	Backend             string                   `json:"backend"`
	ModelSHA256         string                   `json:"model_sha256"`
	RuntimeSeconds      float64                  `json:"runtime_seconds"`
	Typed               *anyJevDemoTypedMode     `json:"typed,omitempty"`
	Lifecycle           *anyJevDemoLifecycleMode `json:"lifecycle,omitempty"`
	OmittedFields       []string                 `json:"omitted_fields"`
	HumanReviewRequired bool                     `json:"human_review_required"`
	ExecutionVerified   bool                     `json:"execution_verified"`
	CutoverEligible     bool                     `json:"cutover_evidence_eligible"`
}

type anyJevDemoTypedMode struct {
	Device           string                      `json:"device"`
	DType            string                      `json:"dtype"`
	BatchSize        int                         `json:"batch_size"`
	HeadBlock        *int                        `json:"head_block,omitempty"`
	ModelBlocks      int                         `json:"model_blocks"`
	TestExamples     int                         `json:"test_examples"`
	RewordVariant    string                      `json:"reword_variant"`
	AdaptAfter       int                         `json:"adapt_after"`
	FitLabels        int                         `json:"fit_labels"`
	ModelLoadSeconds float64                     `json:"model_load_seconds"`
	Questions        []anyJevDemoTypedQuestion   `json:"questions"`
	Pooled           anyJevDemoTypedPooled       `json:"pooled"`
	Routing          []anyJevDemoTypedRoute      `json:"routing"`
	PooledRouting    *anyJevDemoTypedPooledRoute `json:"pooled_routing,omitempty"`
	Fits             []anyJevDemoTypedFit        `json:"fits"`
	PooledFit        *anyJevDemoTypedPooledFit   `json:"pooled_fit,omitempty"`
	RoundTrip        anyJevDemoTypedRoundTrip    `json:"round_trip"`
}

type anyJevDemoTypedQuestion struct {
	QuestionSHA256 string               `json:"question_sha256"`
	Kind           string               `json:"kind"`
	Options        int                  `json:"options"`
	Examples       int                  `json:"examples"`
	ShippedHead    bool                 `json:"shipped_head"`
	L0             anyJevDemoTypedLevel `json:"l0"`
	L2             anyJevDemoTypedLevel `json:"l2"`
}

type anyJevDemoTypedLevel struct {
	Accuracy                float64 `json:"accuracy"`
	ECE                     float64 `json:"ece"`
	Examples                int     `json:"examples"`
	PromptsPerDecision      int     `json:"prompts_per_decision"`
	BlocksExecuted          int     `json:"blocks_executed"`
	ModelBlocks             int     `json:"model_blocks"`
	MillisecondsPerDecision float64 `json:"milliseconds_per_decision"`
	Readout                 string  `json:"readout"`
}

type anyJevDemoTypedPooled struct {
	Examples int                        `json:"examples"`
	L0       anyJevDemoTypedPooledLevel `json:"l0"`
	L2       anyJevDemoTypedPooledLevel `json:"l2"`
}

type anyJevDemoTypedPooledLevel struct {
	Accuracy float64 `json:"accuracy"`
	ECE      float64 `json:"ece"`
}

type anyJevDemoTypedRoute struct {
	QuestionSHA256     string                  `json:"question_sha256"`
	Variant            string                  `json:"variant"`
	ExactAccuracy      float64                 `json:"exact_accuracy"`
	RoutedFromSHA256   string                  `json:"routed_from_sha256,omitempty"`
	AsIs               *anyJevDemoRouteVariant `json:"as_is,omitempty"`
	Recentred          *anyJevDemoRouteVariant `json:"recentred,omitempty"`
	Reversed           *anyJevDemoRouteVariant `json:"reversed,omitempty"`
	L0RewordedAccuracy *float64                `json:"l0_reworded_accuracy,omitempty"`
}

type anyJevDemoRouteVariant struct {
	Accuracy         float64 `json:"accuracy"`
	FlipVsExact      float64 `json:"flip_vs_exact"`
	RoutedFromSHA256 string  `json:"routed_from_sha256,omitempty"`
	Adapted          *bool   `json:"adapted,omitempty"`
	AdaptN           *int    `json:"adapt_n,omitempty"`
	Reordered        *bool   `json:"reordered,omitempty"`
}

type anyJevDemoTypedPooledRoute struct {
	ExactAccuracy      float64  `json:"exact_accuracy"`
	AsIsAccuracy       *float64 `json:"as_is_accuracy,omitempty"`
	RecentredAccuracy  *float64 `json:"recentred_accuracy,omitempty"`
	ReversedAccuracy   *float64 `json:"reversed_accuracy,omitempty"`
	L0RewordedAccuracy *float64 `json:"l0_reworded_accuracy,omitempty"`
}

type anyJevDemoTypedFit struct {
	QuestionSHA256  string   `json:"question_sha256"`
	LabelCount      int      `json:"label_count"`
	FitSeconds      float64  `json:"fit_seconds"`
	Method          string   `json:"method"`
	LayerAbsolute   int      `json:"layer_absolute"`
	OOFAccuracy     float64  `json:"oof_accuracy"`
	Accuracy        float64  `json:"accuracy"`
	ShippedAccuracy *float64 `json:"shipped_accuracy,omitempty"`
	L0Accuracy      float64  `json:"l0_accuracy"`
}

type anyJevDemoTypedPooledFit struct {
	Accuracy        float64  `json:"accuracy"`
	ECE             float64  `json:"ece"`
	ShippedAccuracy *float64 `json:"shipped_accuracy,omitempty"`
}

type anyJevDemoTypedRoundTrip struct {
	HeadCount           int     `json:"head_count"`
	ArtifactBytes       int     `json:"artifact_bytes"`
	MaxProbabilityDelta float64 `json:"max_probability_delta"`
	DecisionCount       int     `json:"decision_count"`
	AutoWithoutHead     string  `json:"auto_without_head"`
}

type anyJevDemoLifecycleMode struct {
	QuestionSHA256 string                     `json:"question_sha256"`
	Day0           anyJevDemoLifecycleDay0    `json:"day0"`
	StreamExamples int                        `json:"stream_examples"`
	FitAt          int                        `json:"fit_at"`
	Fits           []anyJevDemoLifecycleFit   `json:"fits"`
	Wording        anyJevDemoLifecycleWording `json:"wording"`
	Restart        anyJevDemoLifecycleRestart `json:"restart"`
}

type anyJevDemoLifecycleDay0 struct {
	Level    string  `json:"level"`
	Accuracy float64 `json:"accuracy"`
	Examples int     `json:"examples"`
}

type anyJevDemoLifecycleFit struct {
	At            int     `json:"at"`
	Accuracy      float64 `json:"accuracy"`
	ECE           float64 `json:"ece"`
	Level         string  `json:"level"`
	Method        string  `json:"method"`
	LayerAbsolute int     `json:"layer_absolute"`
	OOFAccuracy   float64 `json:"oof_accuracy"`
}

type anyJevDemoLifecycleWording struct {
	FirstExamples     int     `json:"first_examples"`
	AccuracyUnadapted float64 `json:"accuracy_unadapted"`
	AccuracyAdapted   float64 `json:"accuracy_adapted"`
	AccuracyControl   float64 `json:"accuracy_control"`
	RoutedFromSHA256  string  `json:"routed_from_sha256"`
	AdaptExamples     int     `json:"adapt_examples"`
}

type anyJevDemoLifecycleRestart struct {
	HeadCount             int     `json:"head_count"`
	ObservationCount      int     `json:"observation_count"`
	AdaptationCount       int     `json:"adaptation_count"`
	ArtifactBytes         int     `json:"artifact_bytes"`
	AdaptedOnFirstRequest bool    `json:"adapted_on_first_request"`
	AdaptExamplesOnFirst  int     `json:"adapt_examples_on_first"`
	MaxProbabilityDelta   float64 `json:"max_probability_delta"`
}

func anyJevDemoJevModeJSONPath(relative string) (string, bool) {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 3 || !strings.EqualFold(parts[0], "demo") || !strings.EqualFold(parts[1], "results") {
		return "", false
	}
	name := strings.ToLower(parts[2])
	if !strings.HasPrefix(name, "jev_mode_") || !strings.HasSuffix(name, ".json") {
		return "", false
	}
	if strings.HasPrefix(name, "jev_mode_lifecycle_") && len(name) > len("jev_mode_lifecycle_.json") {
		return "lifecycle", true
	}
	if len(name) > len("jev_mode_.json") {
		return "typed", true
	}
	return "", false
}

func isAnyJevDemoJevModeJSONPath(relative string) bool {
	_, ok := anyJevDemoJevModeJSONPath(relative)
	return ok
}

func normalizeAnyJevDemoJevModeResult(relative string, value any) (normalizedItem, error) {
	mode, recognized := anyJevDemoJevModeJSONPath(relative)
	if !recognized {
		return normalizedItem{}, errors.New("unsupported AnyJev Jev-mode demo path")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev Jev-mode demo result shape")
	}
	backend, backendOK := root["backend"].(string)
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	seconds, secondsOK := boundedAnyJevNumber(root["seconds"], 0, 1e6)
	env, envOK := root["env"].(map[string]any)
	if !backendOK || !oneOf(backend, "hf", "fake") || !modelOK || privacy.ContainsCredentialText(model) ||
		!secondsOK || !envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev Jev-mode demo provenance is invalid")
	}
	projected := anyJevDemoJevModeImport{
		SchemaVersion: anyJevDemoJevModeResultSchema, Mode: mode, Backend: backend,
		ModelSHA256: anyJevStringSHA256(model), RuntimeSeconds: seconds,
		OmittedFields:       []string{"model_label", "question_labels", "question_text", "reworded_text", "seed", "head_file_path", "environment_details"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	var err error
	if mode == "typed" {
		projected.Typed, err = normalizeAnyJevDemoTypedMode(root)
	} else {
		projected.Lifecycle, err = normalizeAnyJevDemoLifecycleMode(root)
	}
	if err != nil {
		return normalizedItem{}, err
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev Jev-mode demo projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevDemoTypedMode(root map[string]any) (*anyJevDemoTypedMode, error) {
	required := []string{
		"adapt_after", "backend", "batch_size", "device", "dtype", "env", "fit", "fit_labels", "heads_block",
		"heads_file", "levels", "model", "model_load_seconds", "n_blocks", "n_test", "reword", "roundtrip",
		"routing", "seconds",
	}
	if err := requireAnyJevJevModeFields(root, required, required); err != nil {
		return nil, err
	}
	device, deviceOK := root["device"].(string)
	dtype, dtypeOK := root["dtype"].(string)
	reword, rewordOK := root["reword"].(string)
	if !deviceOK || !oneOf(device, "cpu", "cuda", "mps") || !dtypeOK || !oneOf(dtype, "float16", "float32", "float64", "bfloat16") ||
		!rewordOK || !oneOf(reword, "none", "w1", "w2", "w3", "o1") {
		return nil, errors.New("AnyJev typed demo runtime settings are invalid")
	}
	batchSize, batchOK := boundedAnyJevInteger(root["batch_size"], 1, 4096)
	modelBlocks, blocksOK := boundedAnyJevInteger(root["n_blocks"], 1, 1024)
	testExamples, testOK := boundedAnyJevInteger(root["n_test"], 1, 1000000)
	adaptAfter, adaptOK := boundedAnyJevInteger(root["adapt_after"], 0, 10000000)
	fitLabels, fitLabelsOK := boundedAnyJevInteger(root["fit_labels"], 0, 10000000)
	modelLoadSeconds, loadOK := boundedAnyJevNumber(root["model_load_seconds"], 0, 1e6)
	if !batchOK || !blocksOK || !testOK || !adaptOK || !fitLabelsOK || !loadOK {
		return nil, errors.New("AnyJev typed demo numeric settings are invalid")
	}
	var headBlock *int
	if raw := root["heads_block"]; raw != nil {
		value, ok := boundedAnyJevInteger(raw, 1, 10000)
		if !ok {
			return nil, errors.New("AnyJev typed demo head block is invalid")
		}
		headBlock = &value
	}
	if headFile := root["heads_file"]; headFile != nil {
		_, ok := boundedAnyJevBenchmarkString(headFile, 4096)
		if !ok {
			return nil, errors.New("AnyJev typed demo head-file reference is invalid")
		}
	}

	levelsRaw, ok := root["levels"].(map[string]any)
	if !ok || len(levelsRaw) < 2 || len(levelsRaw) > 257 {
		return nil, errors.New("AnyJev typed demo level map is invalid")
	}
	pooledRaw, hasPooled := levelsRaw["pooled"]
	if !hasPooled {
		return nil, errors.New("AnyJev typed demo levels omit the pooled row")
	}
	questionNames := make([]string, 0, len(levelsRaw)-1)
	for name := range levelsRaw {
		if name == "pooled" {
			continue
		}
		if name == "" || len(name) > 512 {
			return nil, errors.New("AnyJev typed demo question key is invalid")
		}
		questionNames = append(questionNames, name)
	}
	sort.Strings(questionNames)
	projected := &anyJevDemoTypedMode{
		Device: device, DType: dtype, BatchSize: batchSize, HeadBlock: headBlock,
		ModelBlocks: modelBlocks, TestExamples: testExamples, RewordVariant: reword,
		AdaptAfter: adaptAfter, FitLabels: fitLabels, ModelLoadSeconds: modelLoadSeconds,
		Questions: []anyJevDemoTypedQuestion{}, Routing: []anyJevDemoTypedRoute{}, Fits: []anyJevDemoTypedFit{},
	}
	questionByName := make(map[string]anyJevDemoTypedQuestion, len(questionNames))
	var totalExamples int
	for _, name := range questionNames {
		question, ok := levelsRaw[name].(map[string]any)
		if !ok {
			return nil, errors.New("AnyJev typed demo question level row is invalid")
		}
		if err := requireAnyJevJevModeFields(question,
			[]string{"K", "L0", "L2", "kind", "n", "shipped_head"},
			[]string{"K", "L0", "L2", "kind", "n", "shipped_head"}); err != nil {
			return nil, err
		}
		kind, kindOK := question["kind"].(string)
		options, optionsOK := boundedAnyJevInteger(question["K"], 2, 26)
		examples, examplesOK := boundedAnyJevInteger(question["n"], 1, 1000000)
		shipped, shippedOK := question["shipped_head"].(bool)
		if !kindOK || !oneOf(kind, "choice", "noul", "score") || !optionsOK || !examplesOK || !shippedOK {
			return nil, errors.New("AnyJev typed demo question metadata is invalid")
		}
		l0, err := normalizeAnyJevDemoTypedLevel(question["L0"])
		if err != nil {
			return nil, err
		}
		l2, err := normalizeAnyJevDemoTypedLevel(question["L2"])
		if err != nil {
			return nil, err
		}
		if l0.Examples != examples || l2.Examples != examples || l0.ModelBlocks != modelBlocks || l2.ModelBlocks != modelBlocks {
			return nil, errors.New("AnyJev typed demo question counts or model depth do not reconcile")
		}
		row := anyJevDemoTypedQuestion{
			QuestionSHA256: anyJevStringSHA256(name), Kind: kind, Options: options,
			Examples: examples, ShippedHead: shipped, L0: l0, L2: l2,
		}
		projected.Questions = append(projected.Questions, row)
		questionByName[name] = row
		totalExamples += examples
		if totalExamples > 10000000 {
			return nil, errors.New("AnyJev typed demo example count exceeds bounds")
		}
	}
	pooledExamples, err := normalizeAnyJevDemoTypedPooled(pooledRaw, totalExamples)
	if err != nil {
		return nil, err
	}
	projected.Pooled = pooledExamples

	if err := normalizeAnyJevDemoTypedRouteMap(root["routing"], questionByName, projected.Pooled.L2.Accuracy,
		&projected.Routing, &projected.PooledRouting); err != nil {
		return nil, err
	}
	if err := normalizeAnyJevDemoTypedFitMap(root["fit"], questionByName, fitLabels, &projected.Fits, &projected.PooledFit); err != nil {
		return nil, err
	}
	roundTrip, err := normalizeAnyJevDemoTypedRoundTrip(root["roundtrip"], totalExamples)
	if err != nil {
		return nil, err
	}
	projected.RoundTrip = roundTrip
	return projected, nil
}

func normalizeAnyJevDemoTypedLevel(value any) (anyJevDemoTypedLevel, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return anyJevDemoTypedLevel{}, errors.New("AnyJev typed demo level metric is invalid")
	}
	if err := requireAnyJevJevModeFields(object,
		[]string{"acc", "blocks_executed", "ece", "ms_per_decision", "n", "n_blocks", "prompts_per_decision", "readout"},
		[]string{"acc", "blocks_executed", "ece", "ms_per_decision", "n", "n_blocks", "prompts_per_decision", "readout"}); err != nil {
		return anyJevDemoTypedLevel{}, err
	}
	accuracy, accOK := boundedAnyJevNumber(object["acc"], 0, 1)
	ece, eceOK := boundedAnyJevNumber(object["ece"], 0, 1)
	examples, examplesOK := boundedAnyJevInteger(object["n"], 1, 1000000)
	prompts, promptsOK := boundedAnyJevInteger(object["prompts_per_decision"], 1, 100)
	blocks, blocksOK := boundedAnyJevInteger(object["blocks_executed"], 0, 1024)
	modelBlocks, totalBlocksOK := boundedAnyJevInteger(object["n_blocks"], 1, 1024)
	milliseconds, msOK := boundedAnyJevNumber(object["ms_per_decision"], 0, 1e6)
	readout, readoutOK := object["readout"].(string)
	if !accOK || !eceOK || !examplesOK || !promptsOK || !blocksOK || !totalBlocksOK || !msOK || !readoutOK ||
		!oneOf(readout, "L0", "head:lda", "head:ridge", "head:rrr", "head:diff_means") {
		return anyJevDemoTypedLevel{}, errors.New("AnyJev typed demo level metric values are invalid")
	}
	return anyJevDemoTypedLevel{
		Accuracy: accuracy, ECE: ece, Examples: examples, PromptsPerDecision: prompts,
		BlocksExecuted: blocks, ModelBlocks: modelBlocks, MillisecondsPerDecision: milliseconds, Readout: readout,
	}, nil
}

func normalizeAnyJevDemoTypedPooled(value any, expectedExamples int) (anyJevDemoTypedPooled, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 3 {
		return anyJevDemoTypedPooled{}, errors.New("AnyJev typed demo pooled level row is invalid")
	}
	if err := requireAnyJevJevModeFields(object, []string{"L0", "L2", "n"}, []string{"L0", "L2", "n"}); err != nil {
		return anyJevDemoTypedPooled{}, err
	}
	examples, ok := boundedAnyJevInteger(object["n"], 1, 10000000)
	if !ok || examples != expectedExamples {
		return anyJevDemoTypedPooled{}, errors.New("AnyJev typed demo pooled example count does not match question rows")
	}
	parse := func(value any) (anyJevDemoTypedPooledLevel, error) {
		metric, ok := value.(map[string]any)
		if !ok {
			return anyJevDemoTypedPooledLevel{}, errors.New("AnyJev typed demo pooled metric is invalid")
		}
		if err := requireAnyJevJevModeFields(metric, []string{"acc", "ece"}, []string{"acc", "ece"}); err != nil {
			return anyJevDemoTypedPooledLevel{}, err
		}
		acc, aok := boundedAnyJevNumber(metric["acc"], 0, 1)
		ece, eok := boundedAnyJevNumber(metric["ece"], 0, 1)
		if !aok || !eok {
			return anyJevDemoTypedPooledLevel{}, errors.New("AnyJev typed demo pooled metrics are invalid")
		}
		return anyJevDemoTypedPooledLevel{Accuracy: acc, ECE: ece}, nil
	}
	l0, err := parse(object["L0"])
	if err != nil {
		return anyJevDemoTypedPooled{}, err
	}
	l2, err := parse(object["L2"])
	if err != nil {
		return anyJevDemoTypedPooled{}, err
	}
	return anyJevDemoTypedPooled{Examples: examples, L0: l0, L2: l2}, nil
}

func normalizeAnyJevDemoTypedRouteMap(value any, questionByName map[string]anyJevDemoTypedQuestion, expectedPooledAccuracy float64,
	routes *[]anyJevDemoTypedRoute, pooledRoute **anyJevDemoTypedPooledRoute) error {
	object, ok := value.(map[string]any)
	if !ok || len(object) == 0 {
		if ok && len(object) == 0 {
			return nil
		}
		return errors.New("AnyJev typed demo routing map is invalid")
	}
	if len(object) != len(questionByName)+1 {
		return errors.New("AnyJev typed demo routing coverage does not match questions")
	}
	pooledRaw, exists := object["pooled"]
	if !exists {
		return errors.New("AnyJev typed demo routing omits its pooled row")
	}
	pooled, ok := pooledRaw.(map[string]any)
	if !ok {
		return errors.New("AnyJev typed demo pooled routing row is invalid")
	}
	if err := requireAnyJevJevModeFields(pooled, []string{"as_is", "exact", "l0_reworded", "recentred", "reversed"},
		[]string{"as_is", "exact", "l0_reworded", "recentred", "reversed"}); err != nil {
		return err
	}
	exact, exactOK := boundedAnyJevNumber(pooled["exact"], 0, 1)
	if !exactOK {
		return errors.New("AnyJev typed demo pooled routing accuracy is invalid")
	}
	pooledValue := &anyJevDemoTypedPooledRoute{ExactAccuracy: exact}
	for key, target := range map[string]**float64{
		"as_is": &pooledValue.AsIsAccuracy, "recentred": &pooledValue.RecentredAccuracy,
		"reversed": &pooledValue.ReversedAccuracy, "l0_reworded": &pooledValue.L0RewordedAccuracy,
	} {
		parsed, err := normalizeAnyJevDemoNullableNumber(pooled[key], 0, 1)
		if err != nil {
			return err
		}
		*target = parsed
	}
	*pooledRoute = pooledValue

	for name, raw := range object {
		if name == "pooled" {
			continue
		}
		question, exists := questionByName[name]
		if !exists {
			return errors.New("AnyJev typed demo routing contains an unknown question")
		}
		row, ok := raw.(map[string]any)
		if !ok || len(row) != 8 {
			return errors.New("AnyJev typed demo routing question row is invalid")
		}
		if err := requireAnyJevJevModeFields(row,
			[]string{"as_is", "exact", "l0_reworded", "recentred", "reversed", "reworded_text", "routed_from", "variant"},
			[]string{"as_is", "exact", "l0_reworded", "recentred", "reversed", "reworded_text", "routed_from", "variant"}); err != nil {
			return err
		}
		variant, ok := row["variant"].(string)
		if !ok || !oneOf(variant, "w1", "w2", "w3", "o1", "custom") {
			return errors.New("AnyJev typed demo routing variant is invalid")
		}
		if text := row["reworded_text"]; text != nil {
			if _, ok := boundedAnyJevBenchmarkString(text, 100000); !ok {
				return errors.New("AnyJev typed demo reworded prompt is invalid")
			}
		}
		accuracy, ok := boundedAnyJevNumber(row["exact"], 0, 1)
		if !ok || math.Abs(accuracy-question.L2.Accuracy) > 1e-9 {
			return errors.New("AnyJev typed demo exact routing accuracy does not match L2")
		}
		routedFrom, err := normalizeAnyJevDemoOptionalHash(row["routed_from"])
		if err != nil {
			return err
		}
		asIs, err := normalizeAnyJevDemoRouteVariant(row["as_is"], "as_is")
		if err != nil {
			return err
		}
		recentred, err := normalizeAnyJevDemoRouteVariant(row["recentred"], "recentred")
		if err != nil {
			return err
		}
		reversed, err := normalizeAnyJevDemoRouteVariant(row["reversed"], "reversed")
		if err != nil {
			return err
		}
		l0Reworded, err := normalizeAnyJevDemoNullableNumber(row["l0_reworded"], 0, 1)
		if err != nil {
			return err
		}
		*routes = append(*routes, anyJevDemoTypedRoute{
			QuestionSHA256: anyJevStringSHA256(name), Variant: variant, ExactAccuracy: accuracy,
			RoutedFromSHA256: routedFrom, AsIs: asIs, Recentred: recentred,
			Reversed: reversed, L0RewordedAccuracy: l0Reworded,
		})
	}
	sort.Slice(*routes, func(i, j int) bool { return (*routes)[i].QuestionSHA256 < (*routes)[j].QuestionSHA256 })
	if math.Abs(pooledValue.ExactAccuracy-expectedPooledAccuracy) > 1e-9 {
		return errors.New("AnyJev typed demo pooled routing accuracy does not match pooled L2")
	}
	return nil
}

func normalizeAnyJevDemoRouteVariant(value any, kind string) (*anyJevDemoRouteVariant, error) {
	if value == nil {
		return nil, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("AnyJev typed demo route variant is invalid")
	}
	expected := []string{"acc", "flip_vs_exact"}
	if kind == "recentred" {
		expected = append(expected, "adapted", "adapt_n", "routed_from")
	} else if kind == "reversed" {
		expected = append(expected, "reordered")
	} else {
		expected = append(expected, "routed_from")
	}
	if err := requireAnyJevJevModeFields(object, expected, expected); err != nil {
		return nil, err
	}
	accuracy, accOK := boundedAnyJevNumber(object["acc"], 0, 1)
	flip, flipOK := boundedAnyJevNumber(object["flip_vs_exact"], 0, 1)
	routedFrom, err := normalizeAnyJevDemoOptionalHash(object["routed_from"])
	if !accOK || !flipOK || err != nil {
		return nil, errors.New("AnyJev typed demo route variant metrics are invalid")
	}
	result := &anyJevDemoRouteVariant{Accuracy: accuracy, FlipVsExact: flip, RoutedFromSHA256: routedFrom}
	if kind == "recentred" {
		adapted, adaptedOK := object["adapted"].(bool)
		adaptN, adaptNOK := boundedAnyJevInteger(object["adapt_n"], 0, 10000000)
		if !adaptedOK || !adaptNOK {
			return nil, errors.New("AnyJev typed demo recentring metadata is invalid")
		}
		result.Adapted, result.AdaptN = &adapted, &adaptN
	} else if kind == "reversed" {
		reordered, reorderedOK := object["reordered"].(bool)
		if !reorderedOK {
			return nil, errors.New("AnyJev typed demo order metadata is invalid")
		}
		result.Reordered = &reordered
	}
	return result, nil
}

func normalizeAnyJevDemoTypedFitMap(value any, questionByName map[string]anyJevDemoTypedQuestion, labelCount int,
	fits *[]anyJevDemoTypedFit, pooledFit **anyJevDemoTypedPooledFit) error {
	object, ok := value.(map[string]any)
	if !ok || len(object) > len(questionByName)+1 {
		return errors.New("AnyJev typed demo fitting map is invalid")
	}
	if len(object) == 0 {
		if labelCount != 0 {
			return errors.New("AnyJev typed demo fitting records are missing despite nonzero labels")
		}
		return nil
	}
	if labelCount == 0 || len(object) != len(questionByName)+1 {
		return errors.New("AnyJev typed demo fitting coverage does not match label settings")
	}
	pooledRaw, exists := object["pooled"]
	if !exists {
		return errors.New("AnyJev typed demo fitting map omits its pooled row")
	}
	pooled, ok := pooledRaw.(map[string]any)
	if !ok {
		return errors.New("AnyJev typed demo pooled fit row is invalid")
	}
	if err := requireAnyJevJevModeFields(pooled, []string{"acc", "acc_shipped", "ece"}, []string{"acc", "acc_shipped", "ece"}); err != nil {
		return err
	}
	accuracy, accOK := boundedAnyJevNumber(pooled["acc"], 0, 1)
	ece, eceOK := boundedAnyJevNumber(pooled["ece"], 0, 1)
	shipped, err := normalizeAnyJevDemoNullableNumber(pooled["acc_shipped"], 0, 1)
	if !accOK || !eceOK || err != nil {
		return errors.New("AnyJev typed demo pooled fit metrics are invalid")
	}
	*pooledFit = &anyJevDemoTypedPooledFit{Accuracy: accuracy, ECE: ece, ShippedAccuracy: shipped}
	for name, question := range questionByName {
		raw, exists := object[name]
		if !exists {
			return errors.New("AnyJev typed demo fitting row is missing for a question")
		}
		fit, ok := raw.(map[string]any)
		if !ok {
			return errors.New("AnyJev typed demo question fit row is invalid")
		}
		if err := requireAnyJevJevModeFields(fit,
			[]string{"acc", "acc_l0", "acc_shipped", "fit_seconds", "layer_abs", "method", "n_labels", "oof_acc"},
			[]string{"acc", "acc_l0", "acc_shipped", "fit_seconds", "layer_abs", "method", "n_labels", "oof_acc"}); err != nil {
			return err
		}
		fitAcc, fitAccOK := boundedAnyJevNumber(fit["acc"], 0, 1)
		l0Acc, l0OK := boundedAnyJevNumber(fit["acc_l0"], 0, 1)
		fitSeconds, secondsOK := boundedAnyJevNumber(fit["fit_seconds"], 0, 1e6)
		layer, layerOK := boundedAnyJevInteger(fit["layer_abs"], 1, 10000)
		labels, labelsOK := boundedAnyJevInteger(fit["n_labels"], 1, 10000000)
		oof, oofOK := boundedAnyJevNumber(fit["oof_acc"], 0, 1)
		method, methodOK := fit["method"].(string)
		shippedAcc, err := normalizeAnyJevDemoNullableNumber(fit["acc_shipped"], 0, 1)
		if !fitAccOK || !l0OK || !secondsOK || !layerOK || !labelsOK || !oofOK || !methodOK ||
			!oneOf(method, "head:lda", "head:ridge", "head:rrr", "head:diff_means") || err != nil || labels != labelCount ||
			math.Abs(l0Acc-question.L0.Accuracy) > 1e-9 || (question.ShippedHead && shippedAcc == nil) ||
			(!question.ShippedHead && shippedAcc != nil) {
			return errors.New("AnyJev typed demo fit metrics do not reconcile with question metadata")
		}
		if shippedAcc != nil && math.Abs(*shippedAcc-question.L2.Accuracy) > 1e-9 {
			return errors.New("AnyJev typed demo shipped-head accuracy does not reconcile with L2")
		}
		*fits = append(*fits, anyJevDemoTypedFit{
			QuestionSHA256: anyJevStringSHA256(name), LabelCount: labels, FitSeconds: fitSeconds,
			Method: method, LayerAbsolute: layer, OOFAccuracy: oof, Accuracy: fitAcc,
			ShippedAccuracy: shippedAcc, L0Accuracy: l0Acc,
		})
	}
	sort.Slice(*fits, func(i, j int) bool { return (*fits)[i].QuestionSHA256 < (*fits)[j].QuestionSHA256 })
	return nil
}

func normalizeAnyJevDemoTypedRoundTrip(value any, totalExamples int) (anyJevDemoTypedRoundTrip, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 5 {
		return anyJevDemoTypedRoundTrip{}, errors.New("AnyJev typed demo artifact round-trip is invalid")
	}
	if err := requireAnyJevJevModeFields(object, []string{"auto_without_head", "bytes", "max_abs_dp", "n_decisions", "n_heads"},
		[]string{"auto_without_head", "bytes", "max_abs_dp", "n_decisions", "n_heads"}); err != nil {
		return anyJevDemoTypedRoundTrip{}, err
	}
	heads, headsOK := boundedAnyJevInteger(object["n_heads"], 1, 100000)
	bytes, bytesOK := boundedAnyJevInteger(object["bytes"], 1, 1<<30)
	delta, deltaOK := boundedAnyJevNumber(object["max_abs_dp"], 0, 1)
	decisions, decisionsOK := boundedAnyJevInteger(object["n_decisions"], 1, 10000000)
	auto, autoOK := object["auto_without_head"].(string)
	if !headsOK || !bytesOK || !deltaOK || !decisionsOK || !autoOK || auto != "L0" || decisions != totalExamples {
		return anyJevDemoTypedRoundTrip{}, errors.New("AnyJev typed demo artifact round-trip metrics do not reconcile")
	}
	return anyJevDemoTypedRoundTrip{HeadCount: heads, ArtifactBytes: bytes, MaxProbabilityDelta: delta,
		DecisionCount: decisions, AutoWithoutHead: auto}, nil
}

func normalizeAnyJevDemoLifecycleMode(root map[string]any) (*anyJevDemoLifecycleMode, error) {
	required := []string{"backend", "env", "lifecycle", "mode", "model", "question", "seconds", "seed"}
	if err := requireAnyJevJevModeFields(root, required, required); err != nil {
		return nil, err
	}
	mode, ok := root["mode"].(string)
	if !ok || mode != "lifecycle" {
		return nil, errors.New("AnyJev lifecycle demo mode does not match its path")
	}
	question, ok := boundedAnyJevBenchmarkString(root["question"], 512)
	if !ok || privacy.ContainsCredentialText(question) {
		return nil, errors.New("AnyJev lifecycle question identity is invalid")
	}
	if _, ok := boundedAnyJevInteger(root["seed"], 0, 1000000); !ok {
		return nil, errors.New("AnyJev lifecycle seed is invalid")
	}
	lifecycle, ok := root["lifecycle"].(map[string]any)
	if !ok {
		return nil, errors.New("AnyJev lifecycle state is invalid")
	}
	if err := requireAnyJevJevModeFields(lifecycle, []string{"day0", "fit_at", "fits", "restart", "stream", "wording"},
		[]string{"day0", "fit_at", "fits", "restart", "stream", "wording"}); err != nil {
		return nil, err
	}
	stream, streamOK := boundedAnyJevInteger(lifecycle["stream"], 1, 10000000)
	fitAt, fitAtOK := boundedAnyJevInteger(lifecycle["fit_at"], 1, stream)
	if !streamOK || !fitAtOK {
		return nil, errors.New("AnyJev lifecycle stream or fit threshold is invalid")
	}
	day0Raw, ok := lifecycle["day0"].(map[string]any)
	if !ok {
		return nil, errors.New("AnyJev lifecycle day-0 result is invalid")
	}
	if err := requireAnyJevJevModeFields(day0Raw, []string{"acc", "level", "n"}, []string{"acc", "level", "n"}); err != nil {
		return nil, err
	}
	day0Level, levelOK := day0Raw["level"].(string)
	day0Accuracy, accuracyOK := boundedAnyJevNumber(day0Raw["acc"], 0, 1)
	day0Examples, examplesOK := boundedAnyJevInteger(day0Raw["n"], 1, 1000000)
	if !levelOK || !oneOf(day0Level, "raw", "L0", "L1") || !accuracyOK || !examplesOK {
		return nil, errors.New("AnyJev lifecycle day-0 values are invalid")
	}
	day0 := anyJevDemoLifecycleDay0{Level: day0Level, Accuracy: day0Accuracy, Examples: day0Examples}

	fitsRaw, ok := lifecycle["fits"].([]any)
	if !ok || len(fitsRaw) == 0 || len(fitsRaw) > 128 {
		return nil, errors.New("AnyJev lifecycle fit history is invalid")
	}
	fits := make([]anyJevDemoLifecycleFit, 0, len(fitsRaw))
	seenAt := make(map[int]struct{}, len(fitsRaw))
	fitThresholdObserved := false
	for _, rawFit := range fitsRaw {
		fit, ok := rawFit.(map[string]any)
		if !ok || len(fit) != 7 {
			return nil, errors.New("AnyJev lifecycle fit row is invalid")
		}
		if err := requireAnyJevJevModeFields(fit, []string{"acc", "at", "ece", "layer_abs", "level", "method", "oof_acc"},
			[]string{"acc", "at", "ece", "layer_abs", "level", "method", "oof_acc"}); err != nil {
			return nil, err
		}
		at, atOK := boundedAnyJevInteger(fit["at"], 0, stream)
		accuracy, accOK := boundedAnyJevNumber(fit["acc"], 0, 1)
		ece, eceOK := boundedAnyJevNumber(fit["ece"], 0, 1)
		layer, layerOK := boundedAnyJevInteger(fit["layer_abs"], 1, 10000)
		oof, oofOK := boundedAnyJevNumber(fit["oof_acc"], 0, 1)
		fitLevel, fitLevelOK := fit["level"].(string)
		method, methodOK := fit["method"].(string)
		if !atOK || !accOK || !eceOK || !layerOK || !oofOK || !fitLevelOK || fitLevel != "L2" || !methodOK ||
			!oneOf(method, "head:lda", "head:ridge", "head:rrr", "head:diff_means") {
			return nil, errors.New("AnyJev lifecycle fit values are invalid")
		}
		if _, duplicate := seenAt[at]; duplicate {
			return nil, errors.New("AnyJev lifecycle fit history contains duplicate checkpoints")
		}
		seenAt[at] = struct{}{}
		if at == fitAt {
			fitThresholdObserved = true
		}
		fits = append(fits, anyJevDemoLifecycleFit{At: at, Accuracy: accuracy, ECE: ece,
			Level: fitLevel, Method: method, LayerAbsolute: layer, OOFAccuracy: oof})
	}
	if !fitThresholdObserved {
		return nil, errors.New("AnyJev lifecycle history omits its configured fit threshold")
	}
	sort.Slice(fits, func(i, j int) bool { return fits[i].At < fits[j].At })

	wordingRaw, ok := lifecycle["wording"].(map[string]any)
	if !ok || len(wordingRaw) != 7 {
		return nil, errors.New("AnyJev lifecycle wording result is invalid")
	}
	if err := requireAnyJevJevModeFields(wordingRaw,
		[]string{"acc_after_adapted", "acc_after_control", "acc_first_unadapted", "adapt_n", "first_n", "routed_from", "text"},
		[]string{"acc_after_adapted", "acc_after_control", "acc_first_unadapted", "adapt_n", "first_n", "routed_from", "text"}); err != nil {
		return nil, err
	}
	if rawText := wordingRaw["text"]; rawText != nil {
		if _, ok := boundedAnyJevBenchmarkString(rawText, 100000); !ok {
			return nil, errors.New("AnyJev lifecycle wording text is invalid")
		}
	}
	firstN, firstNOK := boundedAnyJevInteger(wordingRaw["first_n"], 1, stream)
	accFirst, accFirstOK := boundedAnyJevNumber(wordingRaw["acc_first_unadapted"], 0, 1)
	accAdapted, accAdaptedOK := boundedAnyJevNumber(wordingRaw["acc_after_adapted"], 0, 1)
	accControl, accControlOK := boundedAnyJevNumber(wordingRaw["acc_after_control"], 0, 1)
	adaptN, adaptNOK := boundedAnyJevInteger(wordingRaw["adapt_n"], 0, stream)
	routedFrom, routedFromOK := boundedAnyJevBenchmarkString(wordingRaw["routed_from"], 512)
	if !firstNOK || !accFirstOK || !accAdaptedOK || !accControlOK || !adaptNOK || !routedFromOK || routedFrom == "" ||
		privacy.ContainsCredentialText(routedFrom) {
		return nil, errors.New("AnyJev lifecycle wording metrics are invalid")
	}
	wording := anyJevDemoLifecycleWording{
		FirstExamples: firstN, AccuracyUnadapted: accFirst, AccuracyAdapted: accAdapted,
		AccuracyControl: accControl, RoutedFromSHA256: anyJevStringSHA256(routedFrom), AdaptExamples: adaptN,
	}

	restartRaw, ok := lifecycle["restart"].(map[string]any)
	if !ok || len(restartRaw) != 7 {
		return nil, errors.New("AnyJev lifecycle restart result is invalid")
	}
	if err := requireAnyJevJevModeFields(restartRaw,
		[]string{"adapt_n_first", "adapted_on_first_request", "bytes", "max_abs_dp", "n_adaptation", "n_heads", "n_observations"},
		[]string{"adapt_n_first", "adapted_on_first_request", "bytes", "max_abs_dp", "n_adaptation", "n_heads", "n_observations"}); err != nil {
		return nil, err
	}
	headCount, headsOK := boundedAnyJevInteger(restartRaw["n_heads"], 0, 100000)
	observations, observationsOK := boundedAnyJevInteger(restartRaw["n_observations"], 0, 10000000)
	adaptations, adaptationsOK := boundedAnyJevInteger(restartRaw["n_adaptation"], 0, 10000000)
	artifactBytes, bytesOK := boundedAnyJevInteger(restartRaw["bytes"], 0, 1<<30)
	adaptedFirst, adaptedFirstOK := restartRaw["adapted_on_first_request"].(bool)
	adaptFirstCount, adaptFirstOK := boundedAnyJevInteger(restartRaw["adapt_n_first"], 0, 10000000)
	maxDelta, deltaOK := boundedAnyJevNumber(restartRaw["max_abs_dp"], 0, 1)
	if !headsOK || !observationsOK || !adaptationsOK || !bytesOK || !adaptedFirstOK || !adaptFirstOK || !deltaOK ||
		adaptFirstCount > observations || (adaptedFirst && adaptFirstCount == 0) {
		return nil, errors.New("AnyJev lifecycle restart metrics are invalid")
	}
	restart := anyJevDemoLifecycleRestart{
		HeadCount: headCount, ObservationCount: observations, AdaptationCount: adaptations,
		ArtifactBytes: artifactBytes, AdaptedOnFirstRequest: adaptedFirst,
		AdaptExamplesOnFirst: adaptFirstCount, MaxProbabilityDelta: maxDelta,
	}
	return &anyJevDemoLifecycleMode{
		QuestionSHA256: anyJevStringSHA256(question), Day0: day0, StreamExamples: stream,
		FitAt: fitAt, Fits: fits, Wording: wording, Restart: restart,
	}, nil
}

func normalizeAnyJevDemoNullableNumber(value any, minimum, maximum float64) (*float64, error) {
	if value == nil {
		return nil, nil
	}
	number, ok := boundedAnyJevNumber(value, minimum, maximum)
	if !ok {
		return nil, errors.New("AnyJev demo nullable metric is invalid")
	}
	return &number, nil
}

func normalizeAnyJevDemoOptionalHash(value any) (string, error) {
	if value == nil || value == "" {
		return "", nil
	}
	text, ok := boundedAnyJevBenchmarkString(value, 512)
	if !ok {
		return "", errors.New("AnyJev demo routed identity is invalid")
	}
	return anyJevStringSHA256(text), nil
}

func requireAnyJevJevModeFields(object map[string]any, required, allowed []string) error {
	allowedFields := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowedFields[field] = struct{}{}
	}
	for _, field := range required {
		if _, exists := object[field]; !exists {
			return fmt.Errorf("AnyJev Jev-mode result is missing %q", field)
		}
	}
	for field := range object {
		if _, exists := allowedFields[field]; !exists {
			return fmt.Errorf("unsupported AnyJev Jev-mode field %q", field)
		}
	}
	return nil
}
