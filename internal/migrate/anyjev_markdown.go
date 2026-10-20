package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	anyJevMarkdownBenchmarkResultSchema = "threadforge.anyjev-markdown-benchmark-result.v1"
	anyJevTypedMarkdownResultSchema     = "threadforge.anyjev-typed-markdown-result.v1"
	anyJevLatencyMarkdownResultSchema   = "threadforge.anyjev-latency-markdown-result.v1"
	maxAnyJevMarkdownBytes              = 1 << 20
	maxAnyJevMarkdownRows               = 20000
)

type anyJevMarkdownBenchmarkImport struct {
	SchemaVersion       string                       `json:"schema_version"`
	ModelSHA256         string                       `json:"model_sha256"`
	Rows                []anyJevMarkdownBenchmarkRow `json:"rows"`
	OmittedFields       []string                     `json:"omitted_fields"`
	HumanReviewRequired bool                         `json:"human_review_required"`
	ExecutionVerified   bool                         `json:"execution_verified"`
	CutoverEligible     bool                         `json:"cutover_evidence_eligible"`
}

type anyJevMarkdownBenchmarkRow struct {
	TaskKeySHA256 string             `json:"task_key_sha256"`
	OptionCount   int                `json:"option_count"`
	TestExamples  *int               `json:"test_examples,omitempty"`
	Level         string             `json:"level"`
	Metrics       map[string]float64 `json:"metrics"`
}

type anyJevTypedMarkdownImport struct {
	SchemaVersion       string                        `json:"schema_version"`
	ModelSHA256         string                        `json:"model_sha256"`
	Systems             []anyJevTypedMarkdownSystem   `json:"systems"`
	Workflows           []anyJevTypedMarkdownWorkflow `json:"workflows,omitempty"`
	WorkflowScope       string                        `json:"workflow_scope,omitempty"`
	OmittedFields       []string                      `json:"omitted_fields"`
	HumanReviewRequired bool                          `json:"human_review_required"`
	ExecutionVerified   bool                          `json:"execution_verified"`
	CutoverEligible     bool                          `json:"cutover_evidence_eligible"`
}

type anyJevTypedMarkdownSystem struct {
	SystemSHA256 string             `json:"system_sha256"`
	Metrics      map[string]float64 `json:"metrics"`
}

type anyJevTypedMarkdownWorkflow struct {
	WorkflowSHA256 string  `json:"workflow_sha256"`
	Accuracy       float64 `json:"acc"`
	SoftAccuracy   float64 `json:"soft_acc"`
	Examples       int     `json:"n"`
}

type anyJevLatencyMarkdownImport struct {
	SchemaVersion       string                     `json:"schema_version"`
	ModelSHA256         string                     `json:"model_sha256"`
	Backend             string                     `json:"backend"`
	Condition           string                     `json:"condition"`
	Rows                []anyJevLatencyMarkdownRow `json:"rows"`
	OmittedFields       []string                   `json:"omitted_fields"`
	HumanReviewRequired bool                       `json:"human_review_required"`
	ExecutionVerified   bool                       `json:"execution_verified"`
	CutoverEligible     bool                       `json:"cutover_evidence_eligible"`
}

type anyJevLatencyMarkdownRow struct {
	StateTokens      int      `json:"state_tokens"`
	OptionCount      int      `json:"option_count"`
	BatchRawMS       float64  `json:"batch_raw_ms"`
	BatchL0FullMS    float64  `json:"batch_l0_full_ms"`
	BatchL0SharedMS  *float64 `json:"batch_l0_shared_ms,omitempty"`
	SingleRawMS      float64  `json:"single_raw_ms"`
	SingleL0FullMS   float64  `json:"single_l0_full_ms"`
	SingleL0SharedMS *float64 `json:"single_l0_shared_ms,omitempty"`
}

var anyJevMarkdownBenchmarkHeaders = [][]string{
	{"model", "task", "k", "level", "acc", "macro_f1", "brier", "ece", "flip", "cov@5%"},
	{"model", "task", "k", "n", "level", "acc", "macro_f1", "brier", "ece", "flip", "cov@5%"},
}

var anyJevMarkdownMetricBounds = map[string][2]float64{
	"acc": {0, 1}, "macro_f1": {0, 1}, "brier": {0, 2},
	"ece": {0, 1}, "flip": {0, 1}, "cov@5%": {0, 1},
}

var anyJevTypedMarkdownHeaders = []string{"system", "acc", "soft_acc", "ece", "brier_mean", "score_mae"}

var anyJevTypedMarkdownMetricBounds = map[string][2]float64{
	"acc": {0, 1}, "soft_acc": {0, 1}, "ece": {0, 1},
	"brier_mean": {0, 2}, "score_mae": {0, 1e6},
}

var anyJevLatencyMarkdownSharedHeaders = []string{
	"backend", "state_tokens", "k", "batch_raw_ms", "batch_l0_full_ms", "batch_l0_shared_ms",
	"single_raw_ms", "single_l0_full_ms", "single_l0_shared_ms", "l0/raw single (full)", "l0/raw single (shared)",
}

var anyJevLatencyMarkdownBasicHeaders = []string{
	"backend", "state_tokens", "k", "batch_raw_ms", "batch_l0_full_ms",
	"single_raw_ms", "single_l0_full_ms", "l0/raw single (full)", "l0/raw single (shared)",
}

const anyJevTypedMarkdownWorkflowHeading = "Per workflow (acc / soft_acc), AnyJev L0:"

func isAnyJevLatencyMarkdownPath(relative string) bool {
	_, _, _, _, ok := anyJevLatencyMarkdownIdentity(relative)
	return ok
}

func anyJevLatencyMarkdownIdentity(relative string) (model, backend, condition string, shared bool, ok bool) {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 4 || !strings.EqualFold(parts[0], "bench") || !strings.HasSuffix(strings.ToLower(parts[3]), ".md") {
		return "", "", "", false, false
	}
	if _, err := parseAnyJevMigrationDate(parts[2]); err != nil {
		return "", "", "", false, false
	}
	family := strings.ToLower(parts[1])
	switch family {
	case "results_latency":
		condition = "default"
		shared = true
	case "results_latency_vllm_cache_on":
		condition = "vllm_cache_on"
	case "results_latency_vllm_cache_off":
		condition = "vllm_cache_off"
	default:
		return "", "", "", false, false
	}
	filename := parts[3]
	stem := filename[:len(filename)-len(".md")]
	dot := strings.IndexByte(stem, '.')
	if dot < 1 || dot+1 >= len(stem) {
		return "", "", "", false, false
	}
	backend = strings.ToLower(stem[:dot])
	if (family == "results_latency" && backend != "hf") ||
		(family != "results_latency" && backend != "vllm") {
		return "", "", "", false, false
	}
	modelSlug := stem[dot+1:]
	separator := strings.Index(modelSlug, "__")
	if separator <= 0 || separator+2 >= len(modelSlug) {
		return "", "", "", false, false
	}
	model = modelSlug[:separator] + "/" + modelSlug[separator+2:]
	if _, valid := boundedAnyJevBenchmarkString(model, 2048); !valid {
		return "", "", "", false, false
	}
	return model, backend, condition, shared, true
}

func normalizeAnyJevLatencyMarkdown(relative string, value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok || len(root) != 1 {
		return normalizedItem{}, errors.New("AnyJev latency Markdown must contain only bounded document content")
	}
	content, ok := root["content"].(string)
	if !ok || content == "" || len(content) > maxAnyJevMarkdownBytes || !utf8.ValidString(content) {
		return normalizedItem{}, errors.New("AnyJev latency Markdown content is empty, invalid, or oversized")
	}
	model, backend, condition, shared, ok := anyJevLatencyMarkdownIdentity(relative)
	if !ok {
		return normalizedItem{}, errors.New("AnyJev latency Markdown path or model identity is unsupported")
	}
	lines := strings.Split(content, "\n")
	headerIndex := nextAnyJevNonBlankLine(lines, 0)
	if headerIndex < 0 {
		return normalizedItem{}, errors.New("AnyJev latency Markdown table is empty")
	}
	header, tableRow := splitAnyJevMarkdownTableRow(lines[headerIndex])
	wantedHeader := anyJevLatencyMarkdownBasicHeaders
	if shared {
		wantedHeader = anyJevLatencyMarkdownSharedHeaders
	}
	if !tableRow || len(header) != len(wantedHeader) {
		return normalizedItem{}, errors.New("AnyJev latency Markdown header is unsupported")
	}
	columns := make(map[string]int, len(header))
	for index, name := range header {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != wantedHeader[index] {
			return normalizedItem{}, errors.New("AnyJev latency Markdown columns are not in the documented order")
		}
		columns[name] = index
	}
	separatorIndex := nextAnyJevNonBlankLine(lines, headerIndex+1)
	if separatorIndex < 0 || !anyJevMarkdownSeparator(lines[separatorIndex], len(header)) {
		return normalizedItem{}, errors.New("AnyJev latency Markdown separator is invalid")
	}
	projected := anyJevLatencyMarkdownImport{
		SchemaVersion: anyJevLatencyMarkdownResultSchema,
		ModelSHA256:   anyJevStringSHA256(model), Backend: backend, Condition: condition,
		Rows:                make([]anyJevLatencyMarkdownRow, 0),
		OmittedFields:       []string{"model_label", "dates", "reported_speedup_ratios"},
		HumanReviewRequired: true,
	}
	seen := make(map[[2]int]struct{})
	for index := separatorIndex + 1; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		cells, row := splitAnyJevMarkdownTableRow(line)
		if !row || len(cells) != len(header) || anyJevMarkdownSeparator(line, len(header)) {
			return normalizedItem{}, errors.New("AnyJev latency Markdown contains an unsupported or malformed row")
		}
		if len(projected.Rows) >= maxAnyJevMarkdownRows {
			return normalizedItem{}, errors.New("AnyJev latency Markdown has too many rows")
		}
		if !strings.EqualFold(cells[columns["backend"]], backend) {
			return normalizedItem{}, errors.New("AnyJev latency Markdown backend does not match its filename")
		}
		stateTokens, stateOK := parseAnyJevMarkdownInteger(cells[columns["state_tokens"]], 1, 1e7)
		options, optionsOK := parseAnyJevMarkdownInteger(cells[columns["k"]], 2, 26)
		if !stateOK || !optionsOK {
			return normalizedItem{}, errors.New("AnyJev latency Markdown dimensions are invalid")
		}
		key := [2]int{stateTokens, options}
		if _, duplicate := seen[key]; duplicate {
			return normalizedItem{}, errors.New("AnyJev latency Markdown contains duplicate dimensions")
		}
		seen[key] = struct{}{}
		readMS := func(name string) (float64, bool) {
			return parseAnyJevMarkdownNumber(cells[columns[name]], math.SmallestNonzeroFloat64, 1e9)
		}
		batchRaw, a := readMS("batch_raw_ms")
		batchFull, b := readMS("batch_l0_full_ms")
		singleRaw, c := readMS("single_raw_ms")
		singleFull, d := readMS("single_l0_full_ms")
		if !a || !b || !c || !d {
			return normalizedItem{}, errors.New("AnyJev latency Markdown full-path measurements are invalid")
		}
		rowValue := anyJevLatencyMarkdownRow{
			StateTokens: stateTokens, OptionCount: options,
			BatchRawMS: batchRaw, BatchL0FullMS: batchFull,
			SingleRawMS: singleRaw, SingleL0FullMS: singleFull,
		}
		if !anyJevMarkdownRatioMatches(cells[columns["l0/raw single (full)"]], singleFull, singleRaw) {
			return normalizedItem{}, errors.New("AnyJev latency Markdown full-path ratio does not reconcile")
		}
		if shared {
			batchSharedText := cells[columns["batch_l0_shared_ms"]]
			singleSharedText := cells[columns["single_l0_shared_ms"]]
			if (batchSharedText == "") != (singleSharedText == "") {
				return normalizedItem{}, errors.New("AnyJev latency Markdown shared measurements are incomplete")
			}
			sharedRatio := cells[columns["l0/raw single (shared)"]]
			if batchSharedText == "" {
				if sharedRatio != "" {
					return normalizedItem{}, errors.New("AnyJev latency Markdown shared ratio has no measurements")
				}
			} else {
				batchShared, batchOK := parseAnyJevMarkdownNumber(batchSharedText, math.SmallestNonzeroFloat64, 1e9)
				singleShared, singleOK := parseAnyJevMarkdownNumber(singleSharedText, math.SmallestNonzeroFloat64, 1e9)
				if !batchOK || !singleOK || !anyJevMarkdownRatioMatches(sharedRatio, singleShared, singleRaw) {
					return normalizedItem{}, errors.New("AnyJev latency Markdown shared measurements or ratio are invalid")
				}
				rowValue.BatchL0SharedMS = &batchShared
				rowValue.SingleL0SharedMS = &singleShared
			}
		} else {
			if cells[columns["l0/raw single (shared)"]] != "" {
				return normalizedItem{}, errors.New("AnyJev latency Markdown has a shared ratio without shared measurements")
			}
		}
		projected.Rows = append(projected.Rows, rowValue)
	}
	if len(projected.Rows) == 0 {
		return normalizedItem{}, errors.New("AnyJev latency Markdown contains no measurements")
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev latency Markdown projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func anyJevMarkdownRatioMatches(value string, numerator, denominator float64) bool {
	if !strings.HasSuffix(value, "x") {
		return false
	}
	reported, ok := parseAnyJevMarkdownNumber(strings.TrimSuffix(value, "x"), 0, 1e6)
	if !ok || numerator <= 0.05 || denominator <= 0.05 {
		return false
	}
	// The source table prints timings to one decimal place and ratios to two.
	// Reconcile the rounded ratio against the possible quotient interval implied
	// by the displayed timing precision, rather than dividing already-rounded
	// cells and rejecting legitimate source output at a rounding boundary.
	ratioMinimum := (numerator - 0.05) / (denominator + 0.05)
	ratioMaximum := (numerator + 0.05) / (denominator - 0.05)
	reportedMinimum, reportedMaximum := reported-0.005-1e-9, reported+0.005+1e-9
	return reportedMaximum >= ratioMinimum && reportedMinimum <= ratioMaximum
}

func isAnyJevTypedMarkdownPath(relative string) bool {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 4 || !strings.EqualFold(parts[0], "bench") || !strings.HasSuffix(strings.ToLower(parts[3]), ".md") {
		return false
	}
	family := strings.ToLower(parts[1])
	if family != "results_typed" && !strings.HasPrefix(family, "results_typed_") {
		return false
	}
	if _, err := parseAnyJevMigrationDate(parts[2]); err != nil {
		return false
	}
	_, _, ok := anyJevBenchmarkModelFromMarkdownPath(clean)
	return ok
}

func normalizeAnyJevTypedMarkdown(relative string, value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok || len(root) != 1 {
		return normalizedItem{}, errors.New("typed AnyJev Markdown must contain only bounded document content")
	}
	content, ok := root["content"].(string)
	if !ok || content == "" || len(content) > maxAnyJevMarkdownBytes || !utf8.ValidString(content) {
		return normalizedItem{}, errors.New("typed AnyJev Markdown content is empty, invalid, or oversized")
	}
	model, _, ok := anyJevBenchmarkModelFromMarkdownPath(relative)
	if !ok {
		return normalizedItem{}, errors.New("typed AnyJev Markdown filename has no supported model identity")
	}
	lines := strings.Split(content, "\n")
	headerIndex := -1
	var header []string
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells, tableRow := splitAnyJevMarkdownTableRow(line)
		if !tableRow {
			return normalizedItem{}, errors.New("typed AnyJev Markdown must begin with a supported table")
		}
		headerIndex, header = index, cells
		break
	}
	if headerIndex < 0 || !anyJevTypedMarkdownHeaderAllowed(header) {
		return normalizedItem{}, errors.New("typed AnyJev Markdown table header is unsupported")
	}
	separatorIndex := nextAnyJevNonBlankLine(lines, headerIndex+1)
	if separatorIndex < 0 || !anyJevMarkdownSeparator(lines[separatorIndex], len(header)) {
		return normalizedItem{}, errors.New("typed AnyJev Markdown table separator is invalid")
	}
	projected := anyJevTypedMarkdownImport{
		SchemaVersion:       anyJevTypedMarkdownResultSchema,
		ModelSHA256:         anyJevStringSHA256(model),
		Systems:             make([]anyJevTypedMarkdownSystem, 0),
		Workflows:           make([]anyJevTypedMarkdownWorkflow, 0),
		OmittedFields:       []string{"model_label", "system_labels", "workflow_labels", "dates", "explanatory_text"},
		HumanReviewRequired: true,
	}
	seenSystems := make(map[string]struct{})
	index := separatorIndex + 1
	for ; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			break
		}
		cells, tableRow := splitAnyJevMarkdownTableRow(line)
		if !tableRow {
			return normalizedItem{}, errors.New("typed AnyJev Markdown contains a non-table row before the table ends")
		}
		if len(cells) != len(header) || anyJevMarkdownSeparator(line, len(header)) {
			return normalizedItem{}, errors.New("typed AnyJev Markdown row width is inconsistent")
		}
		if len(projected.Systems) >= maxAnyJevMarkdownRows {
			return normalizedItem{}, errors.New("typed AnyJev Markdown has too many systems")
		}
		system, valid := boundedAnyJevBenchmarkString(cells[0], 512)
		if !valid {
			return normalizedItem{}, errors.New("typed AnyJev Markdown system label is invalid")
		}
		systemSHA := anyJevStringSHA256(system)
		if _, duplicate := seenSystems[systemSHA]; duplicate {
			return normalizedItem{}, errors.New("typed AnyJev Markdown contains duplicate system rows")
		}
		seenSystems[systemSHA] = struct{}{}
		metrics := make(map[string]float64, len(anyJevTypedMarkdownMetricBounds))
		for column, name := range anyJevTypedMarkdownHeaders[1:] {
			cell := cells[column+1]
			if cell == "" {
				continue
			}
			bounds := anyJevTypedMarkdownMetricBounds[name]
			number, valid := parseAnyJevMarkdownNumber(cell, bounds[0], bounds[1])
			if !valid {
				return normalizedItem{}, fmt.Errorf("typed AnyJev Markdown metric %q is invalid", name)
			}
			metrics[name] = number
		}
		if _, exists := metrics["acc"]; !exists {
			return normalizedItem{}, errors.New("typed AnyJev Markdown system omits accuracy")
		}
		projected.Systems = append(projected.Systems, anyJevTypedMarkdownSystem{SystemSHA256: systemSHA, Metrics: metrics})
	}
	if len(projected.Systems) == 0 {
		return normalizedItem{}, errors.New("typed AnyJev Markdown contains no systems")
	}

	footerSeen := false
	workflowSectionSeen := false
	for ; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if line == anyJevTypedMarkdownWorkflowHeading && !workflowSectionSeen && !footerSeen {
			workflowSectionSeen = true
			projected.WorkflowScope = "anyjev_l0"
			continue
		}
		if workflowSectionSeen && !footerSeen && strings.HasPrefix(line, "- ") {
			workflow, accuracy, softAccuracy, examples, valid := parseAnyJevTypedMarkdownWorkflow(line)
			if !valid {
				return normalizedItem{}, errors.New("typed AnyJev Markdown workflow row is invalid")
			}
			workflowSHA := anyJevStringSHA256(workflow)
			for _, existing := range projected.Workflows {
				if existing.WorkflowSHA256 == workflowSHA {
					return normalizedItem{}, errors.New("typed AnyJev Markdown contains duplicate workflow rows")
				}
			}
			projected.Workflows = append(projected.Workflows, anyJevTypedMarkdownWorkflow{
				WorkflowSHA256: workflowSHA, Accuracy: accuracy, SoftAccuracy: softAccuracy, Examples: examples,
			})
			continue
		}
		if strings.HasPrefix(line, "LocalLLaMA/typed-decisions test split,") && !footerSeen && len(line) <= 8192 {
			footerSeen = true
			continue
		}
		return normalizedItem{}, errors.New("typed AnyJev Markdown contains an unsupported section")
	}
	if workflowSectionSeen && len(projected.Workflows) == 0 {
		return normalizedItem{}, errors.New("typed AnyJev Markdown workflow section is empty")
	}
	if len(projected.Workflows) == 0 {
		projected.Workflows = nil
		projected.WorkflowScope = ""
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("typed AnyJev Markdown projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func anyJevTypedMarkdownHeaderAllowed(header []string) bool {
	if len(header) != len(anyJevTypedMarkdownHeaders) {
		return false
	}
	for index, name := range anyJevTypedMarkdownHeaders {
		if !strings.EqualFold(strings.TrimSpace(header[index]), name) {
			return false
		}
	}
	return true
}

func parseAnyJevTypedMarkdownWorkflow(line string) (string, float64, float64, int, bool) {
	if !strings.HasPrefix(line, "- ") || !strings.HasSuffix(line, ")") {
		return "", 0, 0, 0, false
	}
	line = strings.TrimSuffix(strings.TrimPrefix(line, "- "), ")")
	countMarker := strings.LastIndex(line, " (n=")
	if countMarker < 1 {
		return "", 0, 0, 0, false
	}
	countText := line[countMarker+4:]
	examples, valid := parseAnyJevMarkdownInteger(countText, 1, 1e7)
	if !valid {
		return "", 0, 0, 0, false
	}
	metricsAndName := line[:countMarker]
	separator := strings.LastIndex(metricsAndName, ": ")
	if separator < 1 {
		return "", 0, 0, 0, false
	}
	name, metricText := metricsAndName[:separator], metricsAndName[separator+2:]
	if len(name) > 512 || privacy.ContainsCredentialText(name) {
		return "", 0, 0, 0, false
	}
	values := strings.Split(metricText, " / ")
	if len(values) != 2 {
		return "", 0, 0, 0, false
	}
	accuracy, accuracyOK := parseAnyJevMarkdownNumber(values[0], 0, 1)
	softAccuracy, softOK := parseAnyJevMarkdownNumber(values[1], 0, 1)
	return name, accuracy, softAccuracy, examples, accuracyOK && softOK
}

func isAnyJevBenchmarkMarkdownPath(relative string) bool {
	path := strings.Trim(filepathSlash(relative), "/")
	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		return false
	}
	jsonPath := path[:len(path)-len(".md")] + ".json"
	return isAnyJevBenchmarkJSONPath(jsonPath)
}

func normalizeAnyJevBenchmarkMarkdown(relative string, value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok || len(root) != 1 {
		return normalizedItem{}, errors.New("AnyJev Markdown benchmark must contain only bounded document content")
	}
	content, ok := root["content"].(string)
	if !ok || content == "" || len(content) > maxAnyJevMarkdownBytes || !utf8.ValidString(content) {
		return normalizedItem{}, errors.New("AnyJev Markdown benchmark content is empty, invalid, or oversized")
	}
	model, leaf, ok := anyJevBenchmarkModelFromMarkdownPath(relative)
	if !ok {
		return normalizedItem{}, errors.New("AnyJev Markdown benchmark filename has no supported model identity")
	}

	lines := strings.Split(content, "\n")
	headerIndex := -1
	var header []string
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells, tableRow := splitAnyJevMarkdownTableRow(line)
		if !tableRow {
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark must begin with a supported table")
		}
		headerIndex, header = index, cells
		break
	}
	if headerIndex < 0 || !anyJevMarkdownHeaderAllowed(header) {
		return normalizedItem{}, errors.New("AnyJev Markdown benchmark table header is unsupported")
	}
	separatorIndex := nextAnyJevNonBlankLine(lines, headerIndex+1)
	if separatorIndex < 0 || !anyJevMarkdownSeparator(lines[separatorIndex], len(header)) {
		return normalizedItem{}, errors.New("AnyJev Markdown benchmark table separator is invalid")
	}
	columns := make(map[string]int, len(header))
	for index, name := range header {
		columns[strings.ToLower(strings.TrimSpace(name))] = index
	}

	modelSHA := anyJevStringSHA256(model)
	projected := anyJevMarkdownBenchmarkImport{
		SchemaVersion:       anyJevMarkdownBenchmarkResultSchema,
		ModelSHA256:         modelSHA,
		Rows:                make([]anyJevMarkdownBenchmarkRow, 0),
		OmittedFields:       []string{"model_label", "task_labels", "environment_footer", "dates"},
		HumanReviewRequired: true,
	}
	var currentModel, currentTask string
	currentK, currentExamples := 0, 0
	seen := make(map[string]struct{})
	footerSeen := false
	for index := separatorIndex + 1; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		cells, tableRow := splitAnyJevMarkdownTableRow(line)
		if !tableRow {
			if strings.HasPrefix(line, "Environment: ") && !footerSeen && len(line) <= 8192 {
				footerSeen = true
				continue
			}
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark contains an unsupported non-table section")
		}
		if footerSeen {
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark table resumes after its footer")
		}
		if len(cells) != len(header) || anyJevMarkdownSeparator(line, len(header)) {
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark row width is inconsistent")
		}
		if len(projected.Rows) >= maxAnyJevMarkdownRows {
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark has too many rows")
		}

		rowModel := cells[columns["model"]]
		if rowModel != "" {
			currentModel = rowModel
		}
		if currentModel != model && currentModel != leaf {
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark model does not match its filename")
		}
		previousTask := currentTask
		rowTask := cells[columns["task"]]
		if rowTask != "" {
			currentTask = rowTask
			if currentTask != previousTask {
				currentK = 0
				currentExamples = 0
			}
		}
		if currentTask == "" || privacy.ContainsCredentialText(currentTask) {
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark task identity is invalid")
		}
		kCell := cells[columns["k"]]
		if kCell != "" {
			parsed, valid := parseAnyJevMarkdownInteger(kCell, 2, 26)
			if !valid {
				return normalizedItem{}, errors.New("AnyJev Markdown benchmark option count is invalid")
			}
			currentK = parsed
		}
		if currentK == 0 {
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark row has no option count")
		}
		if examplesIndex, exists := columns["n"]; exists {
			examplesCell := cells[examplesIndex]
			if examplesCell != "" {
				parsed, valid := parseAnyJevMarkdownInteger(examplesCell, 1, 1e7)
				if !valid {
					return normalizedItem{}, errors.New("AnyJev Markdown benchmark example count is invalid")
				}
				currentExamples = parsed
			}
			if currentExamples == 0 {
				return normalizedItem{}, errors.New("AnyJev Markdown benchmark row has no example count")
			}
		}
		level := cells[columns["level"]]
		if !anyJevBenchmarkLevels[level] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev Markdown benchmark level %q", level)
		}
		metrics := make(map[string]float64, len(anyJevMarkdownMetricBounds))
		for name, bounds := range anyJevMarkdownMetricBounds {
			cell := cells[columns[name]]
			if cell == "" {
				continue
			}
			number, valid := parseAnyJevMarkdownNumber(cell, bounds[0], bounds[1])
			if !valid {
				return normalizedItem{}, fmt.Errorf("AnyJev Markdown benchmark metric %q is invalid", name)
			}
			metrics[name] = number
		}
		if _, hasAccuracy := metrics["acc"]; !hasAccuracy {
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark row omits accuracy")
		}
		taskSHA := anyJevStringSHA256(currentTask)
		key := fmt.Sprintf("%s/%d/%s", taskSHA, currentK, level)
		if _, duplicate := seen[key]; duplicate {
			return normalizedItem{}, errors.New("AnyJev Markdown benchmark contains duplicate task/level rows")
		}
		seen[key] = struct{}{}
		row := anyJevMarkdownBenchmarkRow{
			TaskKeySHA256: taskSHA, OptionCount: currentK, Level: level, Metrics: metrics,
		}
		if currentExamples > 0 {
			examples := currentExamples
			row.TestExamples = &examples
		}
		projected.Rows = append(projected.Rows, row)
	}
	if len(projected.Rows) == 0 {
		return normalizedItem{}, errors.New("AnyJev Markdown benchmark contains no data rows")
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev Markdown benchmark projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func anyJevBenchmarkModelFromMarkdownPath(relative string) (canonical, leaf string, ok bool) {
	clean := strings.Trim(filepathSlash(relative), "/")
	filename := clean[strings.LastIndex(clean, "/")+1:]
	if !strings.HasSuffix(strings.ToLower(filename), ".md") {
		return "", "", false
	}
	stem := filename[:len(filename)-len(".md")]
	separator := strings.Index(stem, "__")
	if separator <= 0 || separator+2 >= len(stem) {
		return "", "", false
	}
	canonical = stem[:separator] + "/" + stem[separator+2:]
	leaf = stem[separator+2:]
	if _, valid := boundedAnyJevBenchmarkString(canonical, 2048); !valid {
		return "", "", false
	}
	return canonical, leaf, true
}

func anyJevMarkdownHeaderAllowed(header []string) bool {
	for _, expected := range anyJevMarkdownBenchmarkHeaders {
		if len(header) != len(expected) {
			continue
		}
		match := true
		for index := range header {
			if strings.ToLower(strings.TrimSpace(header[index])) != expected[index] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func splitAnyJevMarkdownTableRow(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 || trimmed[0] != '|' || trimmed[len(trimmed)-1] != '|' {
		return nil, false
	}
	parts := strings.Split(trimmed[1:len(trimmed)-1], "|")
	cells := make([]string, len(parts))
	for index, part := range parts {
		cells[index] = strings.TrimSpace(part)
		if len(cells[index]) > 4096 {
			return nil, false
		}
	}
	return cells, true
}

func nextAnyJevNonBlankLine(lines []string, start int) int {
	for index := start; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) != "" {
			return index
		}
	}
	return -1
}

func anyJevMarkdownSeparator(line string, columns int) bool {
	cells, ok := splitAnyJevMarkdownTableRow(line)
	if !ok || len(cells) != columns {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if strings.HasPrefix(cell, ":") {
			cell = cell[1:]
		}
		if strings.HasSuffix(cell, ":") {
			cell = cell[:len(cell)-1]
		}
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func parseAnyJevMarkdownNumber(value string, minimum, maximum float64) (float64, bool) {
	if strings.TrimSpace(value) != value || value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed >= minimum && parsed <= maximum
}

func parseAnyJevMarkdownInteger(value string, minimum, maximum int) (int, bool) {
	if strings.TrimSpace(value) != value || value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil && parsed >= minimum && parsed <= maximum
}
