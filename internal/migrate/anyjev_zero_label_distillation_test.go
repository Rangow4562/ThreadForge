package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const anyJevZeroLabelFixturePath = "bench/results_distill/2026-09-22/private__student-model.from.private__teacher-model.json"

func anyJevZeroLabelDistillationFixture() map[string]any {
	budgets := []any{float64(50), float64(100)}
	methods := anyJevZeroLabelMethodNames([]int{50, 100})
	questions := map[string]any{}
	pooledSums := make(map[string]float64, len(methods))
	for questionIndex, label := range []string{"private-question-a", "private-question-b"} {
		rows := make(map[string]any, len(methods))
		for methodIndex, method := range methods {
			accuracy := 0.55 + float64(questionIndex)*0.02 + float64(methodIndex)*0.001
			ece := 0.12 + float64(methodIndex)*0.002
			rows[method] = map[string]any{"acc": accuracy, "ece": ece}
			pooledSums[method] += accuracy
		}
		kind := []string{"choice", "score"}[questionIndex]
		questions[label] = map[string]any{
			"kind": kind, "k": float64(questionIndex + 2),
			"teacher_acc_train": 0.7 + float64(questionIndex)*0.01,
			"rows":              rows,
		}
	}
	pooled := make(map[string]any, len(methods))
	for _, method := range methods {
		pooled[method] = map[string]any{"acc": pooledSums[method] / float64(len(questions))}
	}
	return map[string]any{
		"student": "private/student-model", "teacher": "private/teacher-model",
		"budgets": budgets, "questions": questions, "pooled": pooled,
		"env": map[string]any{"device": "private-device"}, "date": "2026-09-22T15:50:00.000000",
	}
}

func TestAnyJevZeroLabelDistillationNormalizesAggregateResultsWithoutLabels(t *testing.T) {
	items, handled, err := normalizeAnyJevItems(anyJevZeroLabelFixturePath, anyJevZeroLabelDistillationFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("zero-label distillation conversion: handled=%v items=%d err=%v", handled, len(items), err)
	}
	if items[0].Type != "decision_label_or_evaluation" {
		t.Fatalf("unexpected record type: %s", items[0].Type)
	}
	var result anyJevZeroLabelDistillationImport
	if err := json.Unmarshal(items[0].Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != anyJevZeroLabelDistillationSchema || result.SourceKind != "teacher_zero_label" ||
		result.StudentSHA256 != anyJevStringSHA256("private/student-model") ||
		result.TeacherSHA256 != anyJevStringSHA256("private/teacher-model") ||
		result.QuestionCount != 2 || len(result.Budgets) != 2 || len(result.Pooled) != 7 || len(result.Questions) != 2 ||
		!result.HumanReviewRequired || result.ExecutionVerified || result.CutoverEligible {
		t.Fatalf("zero-label distillation projection is incorrect: %#v", result)
	}
	if result.Questions[0].QuestionSHA256 == result.Questions[1].QuestionSHA256 ||
		result.Questions[0].TeacherTrainAccuracy == result.Questions[1].TeacherTrainAccuracy {
		t.Fatalf("per-question aggregate evidence was not retained: %#v", result.Questions)
	}
	for _, privateValue := range []string{
		"private/student-model", "private/teacher-model", "private-question-a", "private-question-b",
		"private-device", "2026-09-22T15:50", "student raw", "head@gold@50", "head@teacher@100",
	} {
		if strings.Contains(string(items[0].Payload), privateValue) {
			t.Fatalf("zero-label projection retained %q", privateValue)
		}
	}
}

func TestAnyJevZeroLabelDistillationRejectsUnknownOrInconsistentData(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		path   string
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["synthetic_states"] = []any{"private"} }},
		{name: "unknown pooled method", mutate: func(value map[string]any) {
			value["pooled"].(map[string]any)["private method"] = map[string]any{"acc": 0.5}
		}},
		{name: "unknown pooled metric", mutate: func(value map[string]any) {
			value["pooled"].(map[string]any)["student raw"].(map[string]any)["ece"] = 0.1
		}},
		{name: "pooled accuracy mismatch", mutate: func(value map[string]any) {
			value["pooled"].(map[string]any)["student raw"].(map[string]any)["acc"] = 0.99
		}},
		{name: "unknown question field", mutate: func(value map[string]any) {
			value["questions"].(map[string]any)["private-question-a"].(map[string]any)["state"] = "private"
		}},
		{name: "unknown question method", mutate: func(value map[string]any) {
			value["questions"].(map[string]any)["private-question-a"].(map[string]any)["rows"].(map[string]any)["private method"] = map[string]any{"acc": 0.5, "ece": 0.1}
		}},
		{name: "unknown question metric", mutate: func(value map[string]any) {
			value["questions"].(map[string]any)["private-question-a"].(map[string]any)["rows"].(map[string]any)["student raw"].(map[string]any)["prompt"] = "private"
		}},
		{name: "metric out of range", mutate: func(value map[string]any) {
			value["questions"].(map[string]any)["private-question-a"].(map[string]any)["rows"].(map[string]any)["student raw"].(map[string]any)["ece"] = 1.1
		}},
		{name: "unsupported question kind", mutate: func(value map[string]any) {
			value["questions"].(map[string]any)["private-question-a"].(map[string]any)["kind"] = "unrecognized"
		}},
		{name: "invalid budget order", mutate: func(value map[string]any) { value["budgets"] = []any{float64(100), float64(50)} }},
		{name: "filename identity mismatch", mutate: func(value map[string]any) { value["student"] = "private/other-model" }},
		{name: "timestamp directory mismatch", mutate: func(value map[string]any) { value["date"] = "2026-09-23T15:50:00.000000" }},
		{name: "credential-like model identity", mutate: func(value map[string]any) { value["student"] = "token=private" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevZeroLabelDistillationFixture()
			test.mutate(fixture)
			path := anyJevZeroLabelFixturePath
			if test.path != "" {
				path = test.path
			}
			if _, handled, err := normalizeAnyJevItems(path, fixture); !handled || err == nil {
				t.Fatalf("invalid zero-label result was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
}

func TestAnyJevZeroLabelDistillationPathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		anyJevZeroLabelFixturePath,
		"bench/results_distill/2026-09-22/student.from.teacher.json",
	} {
		if !isAnyJevZeroLabelDistillationJSONPath(path) {
			t.Errorf("zero-label distillation path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_distill/synth/student.from.teacher.json",
		"bench/results_distill/2026-99-99/student.from.teacher.json",
		"bench/results_distill/2026-09-22/student.from.teacher.from.other.json",
		"bench/results_distill/2026-09-22/student.distill_heads.fit.json",
		"bench/results_distill_extra/2026-09-22/student.from.teacher.json",
		"bench/results_distill/2026-09-22/../student.from.teacher.json",
		"bench/results_distill/2026-09-22/student.from.teacher.npz",
	} {
		if isAnyJevZeroLabelDistillationJSONPath(path) {
			t.Errorf("unsupported or data-bearing path was recognized: %s", path)
		}
	}
}

func TestAnyJevZeroLabelDistillationSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the source-backed zero-label distillation migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	for _, file := range manifest.Files {
		if !isAnyJevZeroLabelDistillationJSONPath(file.Path) {
			continue
		}
		if file.Status != "ready" || file.ImportedCount != 1 || file.RejectedCount != 0 || file.ExcludedCount != 0 {
			t.Fatalf("source distillation result was not strictly importable: %s status=%s imported=%d rejected=%d excluded=%d", file.Path, file.Status, file.ImportedCount, file.RejectedCount, file.ExcludedCount)
		}
		expected[file.Path] = struct{}{}
	}
	if len(expected) == 0 {
		t.Fatal("source contains no importable zero-label distillation summary")
	}

	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(AnyJev, sourceRoot, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(AnyJev), manifest.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]int, len(expected))
	for _, record := range records {
		if _, ok := expected[record.SourcePath]; !ok {
			continue
		}
		var payload anyJevZeroLabelDistillationImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevZeroLabelDistillationSchema {
			t.Fatalf("zero-label distillation summary did not read back in strict semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible || !payload.HumanReviewRequired {
			t.Fatalf("zero-label distillation trust status changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("zero-label distillation source count mismatch: expected=%d readback=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("zero-label distillation summary did not produce exactly one record (count=%d): %s", seen[path], path)
		}
	}
}
