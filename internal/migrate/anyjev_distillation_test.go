package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevDistillationMetricFixture(tag, baseline string, accuracy, diff float64) map[string]any {
	return map[string]any{
		"acc": accuracy, "ece": 0.12, "nll": 0.55, "n": float64(40),
		"acc_ci95": []any{accuracy - 0.05, accuracy + 0.05},
		"diff_vs_" + tag + ":" + baseline + "_ci95": []any{diff - 0.05, diff + 0.05},
	}
}

func anyJevDistillationFitFixture() map[string]any {
	student := "private/student-a"
	tag := "student-a"
	methods := []string{"gold300", "synth50-hard", "synth50-soft", "gold300+synth50-hard", "gold300+synth50-soft"}
	group := map[string]any{"teacher": anyJevDistillationMetricFixture(tag, "gold300", 0.7, -0.1)}
	question := map[string]any{"teacher": 0.7}
	for index, method := range methods {
		accuracy := 0.65 + float64(index)/20
		group[tag+":"+method] = anyJevDistillationMetricFixture(tag, "gold300", accuracy, accuracy-0.68)
		question[tag+":"+method] = accuracy
	}
	return map[string]any{
		"teacher": "private/teacher-model", "students": []any{[]any{student, float64(8)}},
		"budgets": []any{float64(50)}, "questions": map[string]any{"private-question-name": question},
		"date": "2026-10-01T10:11:12.123456", "env": map[string]any{"device": "private-device"},
		"rows": map[string]any{tag: group},
	}
}

func anyJevDistillationSoftFixture() map[string]any {
	student := "private/student-b"
	tag := "student-b"
	methods := []string{"gold", "gold+teacher-soft", "teacher-argmax", "teacher-soft"}
	group := map[string]any{"teacher": anyJevDistillationMetricFixture(tag, "gold", 0.72, -0.08)}
	question := map[string]any{"teacher": 0.72, "teacher_oof_agreement_with_gold": 0.81}
	for index, method := range methods {
		accuracy := 0.6 + float64(index)/20
		group[tag+":"+method] = anyJevDistillationMetricFixture(tag, "gold", accuracy, accuracy-0.7)
		question[tag+":"+method] = accuracy
	}
	return map[string]any{
		"teacher": "private/teacher-model", "teacher_block": float64(12),
		"students":  []any{[]any{student, float64(8)}},
		"questions": map[string]any{"private-question-name": question},
		"date":      "2026-10-01T10:11:12.123456", "env": map[string]any{"device": "private-device"},
		"rows": map[string]any{tag: group},
	}
}

func TestAnyJevDistillationNormalizesFitAndSoftReportsWithoutLabels(t *testing.T) {
	tests := []struct {
		path string
		data map[string]any
		kind string
	}{
		{"bench/results_distill/2026-10-01/private.distill_heads.fit.json", anyJevDistillationFitFixture(), "head_fit"},
		{"bench/results_distill/2026-10-01/private.distill_heads.soft.json", anyJevDistillationSoftFixture(), "head_soft"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			items, handled, err := normalizeAnyJevItems(test.path, test.data)
			if err != nil || !handled || len(items) != 1 {
				t.Fatalf("distillation conversion: handled=%v items=%d err=%v", handled, len(items), err)
			}
			if items[0].Type != "decision_label_or_evaluation" {
				t.Fatalf("unexpected record type: %s", items[0].Type)
			}
			var result anyJevDistillationImport
			if err := json.Unmarshal(items[0].Payload, &result); err != nil {
				t.Fatal(err)
			}
			if result.SchemaVersion != anyJevDistillationResultSchema || result.SourceKind != test.kind ||
				result.TeacherSHA256 != anyJevStringSHA256("private/teacher-model") || result.QuestionCount != 1 ||
				result.TestExamples != 40 || len(result.Students) != 1 || len(result.Questions) != 1 ||
				!result.HumanReviewRequired || result.ExecutionVerified || result.CutoverEligible {
				t.Fatalf("distillation projection is incorrect: %#v", result)
			}
			if test.kind == "head_fit" && (result.TeacherBlock != nil || len(result.Budgets) != 1 || result.Budgets[0] != 50) {
				t.Fatalf("fit settings were not projected: %#v", result)
			}
			if test.kind == "head_soft" && (result.TeacherBlock == nil || *result.TeacherBlock != 12 || len(result.Budgets) != 0 ||
				result.Questions[0].TeacherOOFAgreement == nil || *result.Questions[0].TeacherOOFAgreement != 0.81) {
				t.Fatalf("soft-target settings were not projected: %#v", result)
			}
			for _, privateValue := range []string{
				"private/teacher-model", "private/student-a", "private/student-b",
				"private-question-name", "private-device", "2026-10-01T10:11:12",
				"gold300", "teacher-argmax", "synth50-hard",
			} {
				if strings.Contains(string(items[0].Payload), privateValue) {
					t.Fatalf("distillation projection retained %q", privateValue)
				}
			}
		})
	}
}

func TestAnyJevDistillationRejectsUnknownOrInconsistentData(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["states"] = []any{"private"} }},
		{name: "unknown method", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["student-a"].(map[string]any)["student-a:unknown"] = anyJevDistillationMetricFixture("student-a", "gold300", 0.5, 0)
		}},
		{name: "unknown metric field", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["student-a"].(map[string]any)["teacher"].(map[string]any)["prompt"] = "private"
		}},
		{name: "metric out of range", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["student-a"].(map[string]any)["teacher"].(map[string]any)["acc"] = 1.1
		}},
		{name: "invalid interval", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["student-a"].(map[string]any)["teacher"].(map[string]any)["acc_ci95"] = []any{0.9, 0.1}
		}},
		{name: "missing question method", mutate: func(value map[string]any) {
			delete(value["questions"].(map[string]any)["private-question-name"].(map[string]any), "student-a:gold300")
		}},
		{name: "row count mismatch", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["student-a"].(map[string]any)["student-a:synth50-hard"].(map[string]any)["n"] = float64(39)
		}},
		{name: "credential-like model", mutate: func(value map[string]any) {
			value["students"].([]any)[0].([]any)[0] = "token=never-import"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevDistillationFitFixture()
			test.mutate(fixture)
			path := "bench/results_distill/2026-10-01/model.distill_heads.fit.json"
			if _, handled, err := normalizeAnyJevItems(path, fixture); !handled || err == nil {
				t.Fatalf("invalid distillation source was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
}

func TestAnyJevDistillationPathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_distill/2026-09-22/model.distill_heads.fit.json",
		"bench/results_distill/2026-09-22/model.distill_heads.soft.json",
	} {
		if !isAnyJevDistillationJSONPath(path) {
			t.Errorf("distillation report path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_distill/synth/workflow.json",
		"bench/results_distill/2026-09-22/model.from.teacher.json",
		"bench/results_distill/2026-09-22/model.distill_heads.fit.npz",
		"bench/results_distill/2026-99-99/model.distill_heads.fit.json",
		"bench/results_distill_extra/2026-09-22/model.distill_heads.fit.json",
	} {
		if isAnyJevDistillationJSONPath(path) {
			t.Errorf("unsupported or data-bearing path was recognized: %s", path)
		}
	}
}

func TestAnyJevDistillationSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the source-backed distillation migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	for _, file := range manifest.Files {
		if isAnyJevDistillationJSONPath(file.Path) && file.Status == "ready" && file.ImportedCount == 1 {
			expected[file.Path] = struct{}{}
		}
	}
	if len(expected) == 0 {
		t.Fatal("source contains no importable distillation-head summary")
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
		if !isAnyJevDistillationJSONPath(record.SourcePath) {
			continue
		}
		var payload anyJevDistillationImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevDistillationResultSchema {
			t.Fatalf("distillation summary did not read back in strict semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatalf("distillation summary trust status changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("distillation summary count mismatch: expected=%d readback=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("distillation summary did not produce exactly one record (count=%d): %s", seen[path], path)
		}
	}
}
