package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevZeroLabelDistillationSchema = "threadforge.anyjev-zero-label-distillation-result.v1"

const anyJevZeroLabelPooledAccuracyTolerance = 0.001

type anyJevZeroLabelDistillationImport struct {
	SchemaVersion       string                                `json:"schema_version"`
	SourceKind          string                                `json:"source_kind"`
	StudentSHA256       string                                `json:"student_sha256"`
	TeacherSHA256       string                                `json:"teacher_sha256"`
	Budgets             []int                                 `json:"budgets"`
	QuestionCount       int                                   `json:"question_count"`
	Pooled              []anyJevZeroLabelPooledMethod         `json:"pooled"`
	Questions           []anyJevZeroLabelDistillationQuestion `json:"questions"`
	OmittedFields       []string                              `json:"omitted_fields"`
	HumanReviewRequired bool                                  `json:"human_review_required"`
	ExecutionVerified   bool                                  `json:"execution_verified"`
	CutoverEligible     bool                                  `json:"cutover_evidence_eligible"`
}

type anyJevZeroLabelPooledMethod struct {
	MethodSHA256 string  `json:"method_sha256"`
	Accuracy     float64 `json:"accuracy"`
}

type anyJevZeroLabelDistillationQuestion struct {
	QuestionSHA256       string                          `json:"question_sha256"`
	Kind                 string                          `json:"kind"`
	K                    int                             `json:"k"`
	TeacherTrainAccuracy float64                         `json:"teacher_train_accuracy"`
	Methods              []anyJevZeroLabelQuestionMethod `json:"methods"`
}

type anyJevZeroLabelQuestionMethod struct {
	MethodSHA256 string  `json:"method_sha256"`
	Accuracy     float64 `json:"accuracy"`
	ECE          float64 `json:"ece"`
}

func isAnyJevZeroLabelDistillationJSONPath(relative string) bool {
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
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	stem := strings.TrimSuffix(name, ".json")
	studentSlug, teacherSlug, found := strings.Cut(stem, ".from.")
	return found && validAnyJevDistillationModelSlug(studentSlug) &&
		validAnyJevDistillationModelSlug(teacherSlug) && !strings.Contains(teacherSlug, ".from.")
}

func validAnyJevDistillationModelSlug(value string) bool {
	if value == "" || len(value) > 512 || value == "." || value == ".." ||
		strings.Contains(value, "..") || privacy.ContainsCredentialText(value) {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("_-.+", char) {
			continue
		}
		return false
	}
	return true
}

func anyJevDistillationModelSlug(model string) (string, bool) {
	if strings.TrimSpace(model) != model || strings.ContainsAny(model, "\\\r\n:") {
		return "", false
	}
	for _, part := range strings.Split(model, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	slug := strings.ReplaceAll(model, "/", "__")
	return slug, validAnyJevDistillationModelSlug(slug)
}

func normalizeAnyJevZeroLabelDistillationResult(relative string, value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev zero-label distillation result shape")
	}
	allowedRoot := map[string]bool{
		"student": true, "teacher": true, "budgets": true, "questions": true,
		"pooled": true, "env": true, "date": true,
	}
	if len(root) != len(allowedRoot) {
		return normalizedItem{}, errors.New("AnyJev zero-label distillation result has an incomplete or unknown root shape")
	}
	for key := range root {
		if !allowedRoot[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev zero-label distillation result field %q", key)
		}
	}

	student, studentOK := boundedAnyJevBenchmarkString(root["student"], 2048)
	teacher, teacherOK := boundedAnyJevBenchmarkString(root["teacher"], 2048)
	date, dateOK := root["date"].(string)
	env, envOK := root["env"].(map[string]any)
	parts := strings.Split(strings.Trim(filepathSlash(relative), "/"), "/")
	studentSlug, studentSlugOK := anyJevDistillationModelSlug(student)
	teacherSlug, teacherSlugOK := anyJevDistillationModelSlug(teacher)
	if !studentOK || !teacherOK || !studentSlugOK || !teacherSlugOK || !dateOK ||
		!validAnyJevLatencyTimestamp(date) || len(date) < 10 || parts[2] != date[:10] ||
		!envOK || len(env) == 0 || len(env) > 256 {
		return normalizedItem{}, errors.New("AnyJev zero-label distillation identity, timestamp, or environment shape is invalid")
	}
	expectedName := studentSlug + ".from." + teacherSlug + ".json"
	if !strings.EqualFold(parts[3], expectedName) {
		return normalizedItem{}, errors.New("AnyJev zero-label distillation filename does not match model identities")
	}

	budgets, err := normalizeAnyJevZeroLabelBudgets(root["budgets"])
	if err != nil {
		return normalizedItem{}, err
	}
	methods := anyJevZeroLabelMethodNames(budgets)
	questions, ok := root["questions"].(map[string]any)
	if !ok || len(questions) == 0 || len(questions) > 256 || len(methods)*len(questions) > 100000 {
		return normalizedItem{}, errors.New("AnyJev zero-label distillation question matrix is invalid")
	}
	pooledRows, ok := root["pooled"].(map[string]any)
	if !ok || len(pooledRows) != len(methods) {
		return normalizedItem{}, errors.New("AnyJev zero-label distillation pooled methods do not match budgets")
	}
	pooledAccuracy := make(map[string]float64, len(methods))
	projected := anyJevZeroLabelDistillationImport{
		SchemaVersion:       anyJevZeroLabelDistillationSchema,
		SourceKind:          "teacher_zero_label",
		StudentSHA256:       anyJevStringSHA256(student),
		TeacherSHA256:       anyJevStringSHA256(teacher),
		Budgets:             budgets,
		QuestionCount:       len(questions),
		Pooled:              make([]anyJevZeroLabelPooledMethod, 0, len(methods)),
		Questions:           make([]anyJevZeroLabelDistillationQuestion, 0, len(questions)),
		OmittedFields:       []string{"student_label", "teacher_label", "question_labels", "method_labels", "date", "environment_details", "synthetic_states", "per-example_predictions"},
		HumanReviewRequired: true,
		ExecutionVerified:   false,
		CutoverEligible:     false,
	}
	for _, method := range methods {
		rawMetric, exists := pooledRows[method]
		metric, valid := rawMetric.(map[string]any)
		accuracy, accuracyOK := 0.0, false
		if valid && len(metric) == 1 {
			accuracy, accuracyOK = boundedAnyJevNumber(metric["acc"], 0, 1)
		}
		if !exists || !valid || len(metric) != 1 || !accuracyOK {
			return normalizedItem{}, errors.New("AnyJev zero-label distillation pooled metric is invalid")
		}
		pooledAccuracy[method] = accuracy
		projected.Pooled = append(projected.Pooled, anyJevZeroLabelPooledMethod{
			MethodSHA256: anyJevStringSHA256(method), Accuracy: accuracy,
		})
	}
	for method := range pooledRows {
		if _, exists := pooledAccuracy[method]; !exists {
			return normalizedItem{}, errors.New("AnyJev zero-label distillation pooled rows contain an unknown method")
		}
	}

	questionLabels := make([]string, 0, len(questions))
	for label := range questions {
		if _, valid := boundedAnyJevBenchmarkString(label, 2048); !valid {
			return normalizedItem{}, errors.New("AnyJev zero-label distillation question identifier is invalid")
		}
		questionLabels = append(questionLabels, label)
	}
	sort.Strings(questionLabels)
	questionAccuracySums := make(map[string]float64, len(methods))
	for _, label := range questionLabels {
		question, ok := questions[label].(map[string]any)
		if !ok || len(question) != 4 || !hasExactAnyJevKeys(question, "kind", "k", "teacher_acc_train", "rows") {
			return normalizedItem{}, errors.New("AnyJev zero-label distillation question record has an unknown or incomplete shape")
		}
		kind, kindOK := question["kind"].(string)
		k, kOK := boundedAnyJevInteger(question["k"], 2, 64)
		teacherAccuracy, teacherAccuracyOK := boundedAnyJevNumber(question["teacher_acc_train"], 0, 1)
		rows, rowsOK := question["rows"].(map[string]any)
		if !kindOK || !oneOf(kind, "choice", "noul", "score") || !kOK || !teacherAccuracyOK ||
			!rowsOK || len(rows) != len(methods) {
			return normalizedItem{}, errors.New("AnyJev zero-label distillation question dimensions are invalid")
		}
		projectedQuestion := anyJevZeroLabelDistillationQuestion{
			QuestionSHA256:       anyJevStringSHA256(label),
			Kind:                 kind,
			K:                    k,
			TeacherTrainAccuracy: teacherAccuracy,
			Methods:              make([]anyJevZeroLabelQuestionMethod, 0, len(methods)),
		}
		for _, method := range methods {
			rawMetric, exists := rows[method]
			metric, valid := rawMetric.(map[string]any)
			if !exists || !valid || len(metric) != 2 || !hasExactAnyJevKeys(metric, "acc", "ece") {
				return normalizedItem{}, errors.New("AnyJev zero-label distillation per-question metric has an unknown or incomplete shape")
			}
			accuracy, accuracyOK := boundedAnyJevNumber(metric["acc"], 0, 1)
			ece, eceOK := boundedAnyJevNumber(metric["ece"], 0, 1)
			if !accuracyOK || !eceOK {
				return normalizedItem{}, errors.New("AnyJev zero-label distillation per-question metric is invalid")
			}
			questionAccuracySums[method] += accuracy
			projectedQuestion.Methods = append(projectedQuestion.Methods, anyJevZeroLabelQuestionMethod{
				MethodSHA256: anyJevStringSHA256(method), Accuracy: accuracy, ECE: ece,
			})
		}
		for method := range rows {
			if _, exists := pooledAccuracy[method]; !exists {
				return normalizedItem{}, errors.New("AnyJev zero-label distillation question rows contain an unknown method")
			}
		}
		projected.Questions = append(projected.Questions, projectedQuestion)
	}
	for _, method := range methods {
		mean := questionAccuracySums[method] / float64(len(questions))
		if math.Abs(mean-pooledAccuracy[method]) > anyJevZeroLabelPooledAccuracyTolerance {
			return normalizedItem{}, errors.New("AnyJev zero-label distillation pooled accuracy does not reconcile to question results")
		}
	}

	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev zero-label distillation projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevZeroLabelBudgets(value any) ([]int, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 || len(raw) > 16 {
		return nil, errors.New("AnyJev zero-label distillation budgets are invalid")
	}
	budgets := make([]int, 0, len(raw))
	previous := 0
	for _, entry := range raw {
		budget, valid := boundedAnyJevInteger(entry, 1, 1000000)
		if !valid || budget <= previous {
			return nil, errors.New("AnyJev zero-label distillation budgets must be unique and increasing")
		}
		budgets = append(budgets, budget)
		previous = budget
	}
	return budgets, nil
}

func anyJevZeroLabelMethodNames(budgets []int) []string {
	methods := make([]string, 0, 1+3*len(budgets))
	methods = append(methods, "student raw")
	for _, budget := range budgets {
		methods = append(methods,
			fmt.Sprintf("head@gold@%d", budget),
			fmt.Sprintf("head@teacher@%d", budget),
			fmt.Sprintf("head@soft@%d", budget),
		)
	}
	return methods
}

func hasExactAnyJevKeys(value map[string]any, expected ...string) bool {
	if len(value) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, exists := value[key]; !exists {
			return false
		}
	}
	return true
}
