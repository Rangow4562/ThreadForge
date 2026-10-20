package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const anyJevDistillationResultSchema = "threadforge.anyjev-distillation-result.v1"

type anyJevDistillationImport struct {
	SchemaVersion       string                       `json:"schema_version"`
	SourceKind          string                       `json:"source_kind"`
	TeacherSHA256       string                       `json:"teacher_sha256"`
	TeacherBlock        *int                         `json:"teacher_block,omitempty"`
	Budgets             []int                        `json:"budgets"`
	QuestionCount       int                          `json:"question_count"`
	TestExamples        int                          `json:"test_examples"`
	TeacherMetrics      anyJevDistillationMetrics    `json:"teacher_metrics"`
	Students            []anyJevDistillationStudent  `json:"students"`
	Questions           []anyJevDistillationQuestion `json:"questions"`
	OmittedFields       []string                     `json:"omitted_fields"`
	HumanReviewRequired bool                         `json:"human_review_required"`
	ExecutionVerified   bool                         `json:"execution_verified"`
	CutoverEligible     bool                         `json:"cutover_evidence_eligible"`
}

type anyJevDistillationMetrics struct {
	Accuracy     float64   `json:"accuracy"`
	ECE          float64   `json:"ece"`
	NLL          float64   `json:"nll"`
	Examples     int       `json:"examples"`
	AccuracyCI95 []float64 `json:"accuracy_ci95"`
}

type anyJevDistillationStudent struct {
	ModelSHA256           string                     `json:"model_sha256"`
	Block                 int                        `json:"block"`
	TeacherVsBaselineCI95 []float64                  `json:"teacher_vs_baseline_ci95"`
	Methods               []anyJevDistillationMethod `json:"methods"`
}

type anyJevDistillationMethod struct {
	MethodSHA256   string    `json:"method_sha256"`
	Accuracy       float64   `json:"accuracy"`
	ECE            float64   `json:"ece"`
	NLL            float64   `json:"nll"`
	Examples       int       `json:"examples"`
	AccuracyCI95   []float64 `json:"accuracy_ci95"`
	DifferenceCI95 []float64 `json:"difference_vs_baseline_ci95"`
}

type anyJevDistillationQuestion struct {
	QuestionSHA256      string                              `json:"question_sha256"`
	TeacherAccuracy     float64                             `json:"teacher_accuracy"`
	TeacherOOFAgreement *float64                            `json:"teacher_oof_agreement,omitempty"`
	Students            []anyJevDistillationQuestionStudent `json:"students"`
}

type anyJevDistillationQuestionStudent struct {
	ModelSHA256 string                             `json:"model_sha256"`
	Methods     []anyJevDistillationQuestionMethod `json:"methods"`
}

type anyJevDistillationQuestionMethod struct {
	MethodSHA256 string  `json:"method_sha256"`
	Accuracy     float64 `json:"accuracy"`
}

type anyJevDistillationStudentSpec struct {
	Model string
	Tag   string
	Block int
}

func isAnyJevDistillationJSONPath(relative string) bool {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 4 || !strings.EqualFold(parts[0], "bench") ||
		!strings.EqualFold(parts[1], "results_distill") {
		return false
	}
	if _, err := time.Parse("2006-01-02", parts[2]); err != nil {
		return false
	}
	name := strings.ToLower(parts[3])
	return strings.HasSuffix(name, ".distill_heads.fit.json") ||
		strings.HasSuffix(name, ".distill_heads.soft.json")
}

func normalizeAnyJevDistillationResult(relative string, value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev distillation result shape")
	}
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	sourceKind := ""
	rootFields := map[string]bool{
		"date": true, "env": true, "rows": true, "students": true,
		"teacher": true, "teacher_block": true, "questions": true,
	}
	if strings.HasSuffix(path, ".distill_heads.fit.json") {
		sourceKind = "head_fit"
		rootFields["budgets"] = true
		delete(rootFields, "teacher_block")
	} else if strings.HasSuffix(path, ".distill_heads.soft.json") {
		sourceKind = "head_soft"
	} else {
		return normalizedItem{}, errors.New("unsupported AnyJev distillation result path")
	}
	if len(root) != len(rootFields) {
		return normalizedItem{}, errors.New("AnyJev distillation result has an incomplete or unknown root shape")
	}
	for key := range root {
		if !rootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev distillation result field %q", key)
		}
	}
	teacher, ok := boundedAnyJevBenchmarkString(root["teacher"], 2048)
	date, dateOK := root["date"].(string)
	env, envOK := root["env"].(map[string]any)
	if !ok || !dateOK || !validAnyJevLatencyTimestamp(date) || !envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev distillation teacher, timestamp, or environment shape is invalid")
	}

	students, err := normalizeAnyJevDistillationStudents(root["students"])
	if err != nil {
		return normalizedItem{}, err
	}
	studentByTag := make(map[string]anyJevDistillationStudentSpec, len(students))
	for _, student := range students {
		studentByTag[student.Tag] = student
	}
	budgets := []int{}
	var teacherBlock *int
	if sourceKind == "head_fit" {
		budgets, err = normalizeAnyJevDistillationBudgets(root["budgets"])
		if err != nil {
			return normalizedItem{}, err
		}
	} else {
		block, valid := boundedAnyJevInteger(root["teacher_block"], 1, 100000)
		if !valid {
			return normalizedItem{}, errors.New("AnyJev distillation teacher block is invalid")
		}
		teacherBlock = &block
	}
	methods := anyJevDistillationMethodNames(sourceKind, budgets)
	if len(methods)*len(students) > 4096 {
		return normalizedItem{}, errors.New("AnyJev distillation method matrix exceeds the limit")
	}

	rawRows, ok := root["rows"].(map[string]any)
	if !ok || len(rawRows) != len(students) {
		return normalizedItem{}, errors.New("AnyJev distillation pooled row groups do not match the student list")
	}
	projected := anyJevDistillationImport{
		SchemaVersion:       anyJevDistillationResultSchema,
		SourceKind:          sourceKind,
		TeacherSHA256:       anyJevStringSHA256(teacher),
		TeacherBlock:        teacherBlock,
		Budgets:             budgets,
		Students:            make([]anyJevDistillationStudent, 0, len(students)),
		OmittedFields:       []string{"teacher_label", "student_labels", "question_labels", "method_labels", "date", "environment_details", "synthetic_states", "per-example_predictions"},
		HumanReviewRequired: true,
		ExecutionVerified:   false,
		CutoverEligible:     false,
	}
	testExamples := 0
	var teacherMetrics *anyJevDistillationMetrics
	for _, student := range students {
		rawGroup, exists := rawRows[student.Tag]
		if !exists {
			return normalizedItem{}, errors.New("AnyJev distillation pooled rows omit a declared student")
		}
		group, ok := rawGroup.(map[string]any)
		if !ok || len(group) != len(methods)+1 {
			return normalizedItem{}, errors.New("AnyJev distillation pooled method set is incomplete")
		}
		baseline := anyJevDistillationBaseline(sourceKind)
		teacherRow, exists := group["teacher"]
		if !exists {
			return normalizedItem{}, errors.New("AnyJev distillation pooled rows omit teacher metrics")
		}
		teacherParsed, rowN, err := normalizeAnyJevDistillationMetricRow(teacherRow, student.Tag, baseline)
		if err != nil {
			return normalizedItem{}, fmt.Errorf("AnyJev distillation teacher metrics: %w", err)
		}
		if testExamples == 0 {
			testExamples = rowN
		} else if testExamples != rowN {
			return normalizedItem{}, errors.New("AnyJev distillation result rows disagree on test-example count")
		}
		teacherCore := metricsWithoutDifference(teacherParsed)
		if teacherMetrics == nil {
			teacherMetrics = &teacherCore
		} else if !sameAnyJevDistillationMetrics(*teacherMetrics, teacherCore) {
			return normalizedItem{}, errors.New("AnyJev distillation repeated teacher metrics disagree")
		}
		studentProjection := anyJevDistillationStudent{
			ModelSHA256:           anyJevStringSHA256(student.Model),
			Block:                 student.Block,
			TeacherVsBaselineCI95: teacherParsed.DifferenceCI95,
			Methods:               make([]anyJevDistillationMethod, 0, len(methods)),
		}
		for _, method := range methods {
			rawKey := student.Tag + ":" + method
			rawMetrics, exists := group[rawKey]
			if !exists {
				return normalizedItem{}, errors.New("AnyJev distillation pooled rows omit an expected method")
			}
			parsed, rowN, err := normalizeAnyJevDistillationMetricRow(rawMetrics, student.Tag, baseline)
			if err != nil {
				return normalizedItem{}, fmt.Errorf("AnyJev distillation method metrics: %w", err)
			}
			if rowN != testExamples {
				return normalizedItem{}, errors.New("AnyJev distillation method rows disagree on test-example count")
			}
			studentProjection.Methods = append(studentProjection.Methods, anyJevDistillationMethod{
				MethodSHA256: anyJevStringSHA256(method),
				Accuracy:     parsed.Accuracy, ECE: parsed.ECE, NLL: parsed.NLL,
				Examples: parsed.Examples, AccuracyCI95: parsed.AccuracyCI95,
				DifferenceCI95: parsed.DifferenceCI95,
			})
		}
		for rawKey := range group {
			if rawKey == "teacher" {
				continue
			}
			if _, allowed := anyJevDistillationMethodFromKey(rawKey, student.Tag, methods); !allowed {
				return normalizedItem{}, errors.New("AnyJev distillation pooled rows contain an unknown method")
			}
		}
		projected.Students = append(projected.Students, studentProjection)
	}
	if teacherMetrics == nil || testExamples == 0 {
		return normalizedItem{}, errors.New("AnyJev distillation result contains no pooled metrics")
	}
	projected.TeacherMetrics = *teacherMetrics
	projected.TestExamples = testExamples

	questions, err := normalizeAnyJevDistillationQuestions(root["questions"], students, methods, sourceKind)
	if err != nil {
		return normalizedItem{}, err
	}
	if len(questions) == 0 || len(questions)*len(students)*len(methods) > 100000 {
		return normalizedItem{}, errors.New("AnyJev distillation question result matrix exceeds the limit")
	}
	projected.Questions = questions
	projected.QuestionCount = len(questions)
	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevDistillationStudents(value any) ([]anyJevDistillationStudentSpec, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > 64 {
		return nil, errors.New("AnyJev distillation student list is invalid")
	}
	students := make([]anyJevDistillationStudentSpec, 0, len(raw))
	seenTags := make(map[string]struct{}, len(raw))
	seenModels := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		pair, ok := entry.([]any)
		if !ok || len(pair) != 2 {
			return nil, errors.New("AnyJev distillation student must contain model and block")
		}
		model, ok := boundedAnyJevBenchmarkString(pair[0], 2048)
		block, blockOK := boundedAnyJevInteger(pair[1], 1, 100000)
		if !ok || !blockOK {
			return nil, errors.New("AnyJev distillation student identity or block is invalid")
		}
		separator := strings.LastIndex(model, "/")
		tag := model[separator+1:]
		if tag == "" {
			return nil, errors.New("AnyJev distillation student identifier has no terminal component")
		}
		if _, exists := seenTags[tag]; exists {
			return nil, errors.New("AnyJev distillation student identifiers are ambiguous")
		}
		modelDigest := anyJevStringSHA256(model)
		if _, exists := seenModels[modelDigest]; exists {
			return nil, errors.New("AnyJev distillation student list contains duplicates")
		}
		seenTags[tag] = struct{}{}
		seenModels[modelDigest] = struct{}{}
		students = append(students, anyJevDistillationStudentSpec{Model: model, Tag: tag, Block: block})
	}
	return students, nil
}

func normalizeAnyJevDistillationBudgets(value any) ([]int, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > 16 {
		return nil, errors.New("AnyJev distillation budgets are invalid")
	}
	budgets := make([]int, 0, len(raw))
	seen := make(map[int]struct{}, len(raw))
	for _, entry := range raw {
		budget, ok := boundedAnyJevInteger(entry, 1, 1000000)
		if !ok {
			return nil, errors.New("AnyJev distillation budget is invalid")
		}
		if _, exists := seen[budget]; exists {
			return nil, errors.New("AnyJev distillation budgets contain duplicates")
		}
		seen[budget] = struct{}{}
		budgets = append(budgets, budget)
	}
	return budgets, nil
}

func anyJevDistillationMethodNames(sourceKind string, budgets []int) []string {
	if sourceKind == "head_soft" {
		return []string{"gold", "gold+teacher-soft", "teacher-argmax", "teacher-soft"}
	}
	methods := []string{"gold300"}
	for _, budget := range budgets {
		methods = append(methods,
			fmt.Sprintf("synth%d-hard", budget), fmt.Sprintf("synth%d-soft", budget),
			fmt.Sprintf("gold300+synth%d-hard", budget), fmt.Sprintf("gold300+synth%d-soft", budget))
	}
	sort.Strings(methods)
	return methods
}

func anyJevDistillationBaseline(sourceKind string) string {
	if sourceKind == "head_soft" {
		return "gold"
	}
	return "gold300"
}

func anyJevDistillationMethodFromKey(key, tag string, methods []string) (string, bool) {
	prefix := tag + ":"
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	method := strings.TrimPrefix(key, prefix)
	for _, allowed := range methods {
		if method == allowed {
			return method, true
		}
	}
	return "", false
}

func normalizeAnyJevDistillationMetricRow(value any, tag, baseline string) (anyJevDistillationMethod, int, error) {
	row, ok := value.(map[string]any)
	if !ok {
		return anyJevDistillationMethod{}, 0, errors.New("metric row is not an object")
	}
	differenceField := "diff_vs_" + tag + ":" + baseline + "_ci95"
	if len(row) != 6 {
		return anyJevDistillationMethod{}, 0, errors.New("metric row has an unknown or missing field")
	}
	for _, key := range []string{"acc", "ece", "nll", "n", "acc_ci95", differenceField} {
		if _, exists := row[key]; !exists {
			return anyJevDistillationMethod{}, 0, errors.New("metric row has an unknown or missing field")
		}
	}
	accuracy, accOK := boundedAnyJevNumber(row["acc"], 0, 1)
	ece, eceOK := boundedAnyJevNumber(row["ece"], 0, 1)
	nll, nllOK := boundedAnyJevNumber(row["nll"], 0, 1000000)
	examples, examplesOK := boundedAnyJevInteger(row["n"], 1, 10000000)
	accuracyCI, accuracyCIOK := normalizeAnyJevParaphraseInterval(row["acc_ci95"], 0, 1)
	differenceCI, differenceCIOK := normalizeAnyJevParaphraseInterval(row[differenceField], -1, 1)
	if !accOK || !eceOK || !nllOK || !examplesOK || !accuracyCIOK || !differenceCIOK {
		return anyJevDistillationMethod{}, 0, errors.New("metric values or confidence intervals are invalid")
	}
	return anyJevDistillationMethod{
		Accuracy: accuracy, ECE: ece, NLL: nll, Examples: examples,
		AccuracyCI95: accuracyCI, DifferenceCI95: differenceCI,
	}, examples, nil
}

func metricsWithoutDifference(value anyJevDistillationMethod) anyJevDistillationMetrics {
	return anyJevDistillationMetrics{
		Accuracy: value.Accuracy, ECE: value.ECE, NLL: value.NLL,
		Examples: value.Examples, AccuracyCI95: value.AccuracyCI95,
	}
}

func sameAnyJevDistillationMetrics(left, right anyJevDistillationMetrics) bool {
	return math.Abs(left.Accuracy-right.Accuracy) < 1e-12 &&
		math.Abs(left.ECE-right.ECE) < 1e-12 && math.Abs(left.NLL-right.NLL) < 1e-12 &&
		left.Examples == right.Examples && len(left.AccuracyCI95) == len(right.AccuracyCI95) &&
		math.Abs(left.AccuracyCI95[0]-right.AccuracyCI95[0]) < 1e-12 &&
		math.Abs(left.AccuracyCI95[1]-right.AccuracyCI95[1]) < 1e-12
}

func normalizeAnyJevDistillationQuestions(value any, students []anyJevDistillationStudentSpec,
	methods []string, sourceKind string) ([]anyJevDistillationQuestion, error) {
	questions, ok := value.(map[string]any)
	if !ok || len(questions) == 0 || len(questions) > 256 {
		return nil, errors.New("AnyJev distillation per-question result map is invalid")
	}
	labels := make([]string, 0, len(questions))
	for label := range questions {
		if _, ok := boundedAnyJevBenchmarkString(label, 2048); !ok {
			return nil, errors.New("AnyJev distillation question identifier is invalid")
		}
		labels = append(labels, label)
	}
	sort.Strings(labels)
	result := make([]anyJevDistillationQuestion, 0, len(labels))
	for _, label := range labels {
		row, ok := questions[label].(map[string]any)
		expectedFields := 1 + len(students)*len(methods)
		if sourceKind == "head_soft" {
			expectedFields += 1
		}
		if !ok || len(row) != expectedFields {
			return nil, errors.New("AnyJev distillation per-question method set is incomplete")
		}
		teacherAccuracy, teacherOK := boundedAnyJevNumber(row["teacher"], 0, 1)
		if !teacherOK {
			return nil, errors.New("AnyJev distillation per-question teacher accuracy is invalid")
		}
		question := anyJevDistillationQuestion{
			QuestionSHA256:  anyJevStringSHA256(label),
			TeacherAccuracy: teacherAccuracy,
			Students:        make([]anyJevDistillationQuestionStudent, 0, len(students)),
		}
		if sourceKind == "head_soft" {
			agreement, valid := boundedAnyJevNumber(row["teacher_oof_agreement_with_gold"], 0, 1)
			if !valid {
				return nil, errors.New("AnyJev distillation teacher agreement metric is invalid")
			}
			question.TeacherOOFAgreement = &agreement
		}
		for _, student := range students {
			projectedStudent := anyJevDistillationQuestionStudent{
				ModelSHA256: anyJevStringSHA256(student.Model),
				Methods:     make([]anyJevDistillationQuestionMethod, 0, len(methods)),
			}
			for _, method := range methods {
				key := student.Tag + ":" + method
				accuracy, valid := boundedAnyJevNumber(row[key], 0, 1)
				if !valid {
					return nil, errors.New("AnyJev distillation per-question method accuracy is invalid")
				}
				projectedStudent.Methods = append(projectedStudent.Methods, anyJevDistillationQuestionMethod{
					MethodSHA256: anyJevStringSHA256(method), Accuracy: accuracy,
				})
			}
			question.Students = append(question.Students, projectedStudent)
		}
		for key := range row {
			if key == "teacher" || (sourceKind == "head_soft" && key == "teacher_oof_agreement_with_gold") {
				continue
			}
			allowed := false
			for _, student := range students {
				if _, ok := anyJevDistillationMethodFromKey(key, student.Tag, methods); ok {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, errors.New("AnyJev distillation per-question row contains an unknown method")
			}
		}
		result = append(result, question)
	}
	return result, nil
}
