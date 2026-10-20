package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func typedMetricsFixture(n int, accuracy float64) map[string]any {
	return map[string]any{
		"n": float64(n), "acc": accuracy, "soft_acc": 0.6, "ece": 0.1,
		"brier": 0.4, "brier_mean": 0.2, "nll": 0.5,
	}
}

func anyJevTypedRunnerFixture() map[string]any {
	question := func(n int, accuracy float64, workflow, kind string) map[string]any {
		row := typedMetricsFixture(n, accuracy)
		row["workflow"] = workflow
		row["name"] = "private question display label"
		row["kind"] = kind
		row["k"] = float64(3)
		row["majority"] = 0.7
		row["label_entropy"] = 0.8
		row["score_mae"] = 0.2
		return row
	}
	return map[string]any{
		"model": "private-model-label", "dataset": "LocalLLaMA/typed-decisions", "prior": "batch",
		"prior_strength": 0.75, "limit_cases": nil, "calib_cases_per_workflow": float64(4),
		"env":     map[string]any{"device": "private-device", "path": "C:/private/model"},
		"seconds": 12.5, "n_decisions": float64(6), "n_questions": float64(2),
		"n_calib_decisions": float64(8),
		"levels": map[string]any{"L0": map[string]any{
			"overall": typedMetricsFixture(6, 0.7),
			"by_type": map[string]any{
				"choice": typedMetricsFixture(2, 0.5),
				"score":  typedMetricsFixture(4, 0.8),
			},
			"by_workflow": map[string]any{
				"private workflow one": typedMetricsFixture(2, 0.5),
				"private workflow two": typedMetricsFixture(4, 0.8),
			},
			"by_question": map[string]any{
				"private-question-one": question(2, 0.5, "private workflow one", "choice"),
				"private-question-two": question(4, 0.8, "private workflow two", "score"),
			},
		}},
	}
}

func TestAnyJevTypedRunnerResultImportsReconciledSanitizedMetrics(t *testing.T) {
	root := t.TempDir()
	resultPath := filepath.Join(root, "bench", "results_typed", "2026-09-21", "model.json")
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(anyJevTypedRunnerFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	manifest, err := Inspect(AnyJev, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("typed AnyJev runner result did not reconcile: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(AnyJev, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(AnyJev), manifest.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "decision_label_or_evaluation" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected typed benchmark record: %#v", records)
	}
	var payload anyJevTypedBenchmarkImport
	if err := json.Unmarshal(records[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != anyJevTypedBenchmarkResultSchema || payload.SourceKind != "runner_result" ||
		payload.BackendKind != "hf" || payload.Prior != "batch" || payload.TestExamples != 6 ||
		payload.CalibrationExamples == nil || *payload.CalibrationExamples != 8 || payload.QuestionCount == nil || *payload.QuestionCount != 2 ||
		len(payload.Levels) != 1 || len(payload.Levels[0].ByWorkflow) != 2 || len(payload.Levels[0].ByQuestion) != 2 ||
		payload.ExecutionVerified || payload.CutoverEvidenceEligible || !payload.HumanReviewRequired {
		t.Fatalf("typed AnyJev run data or trust status is incorrect: %#v", payload)
	}
	encoded := string(records[0].Payload)
	for _, privateValue := range []string{"private-model-label", "LocalLLaMA/typed-decisions", "private-device", "C:/private/model", "private workflow", "private-question", "private question display label", "12.5"} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("typed AnyJev projection retained %q", privateValue)
		}
	}
	if payload.ModelSHA256 != anyJevStringSHA256("private-model-label") ||
		payload.DatasetLabelSHA256 != anyJevStringSHA256("LocalLLaMA/typed-decisions") ||
		payload.Levels[0].ByQuestion[0].QuestionKeySHA256 == "" || payload.Levels[0].ByWorkflow[0].WorkflowSHA256 == "" {
		t.Fatalf("typed AnyJev identifiers were not digest-bound: %#v", payload)
	}
	second, err := CreateSnapshot(AnyJev, root, dataRoot)
	if err != nil || second.ID != snapshot.ID {
		t.Fatalf("typed AnyJev snapshot was not idempotent: %#v err=%v", second, err)
	}
}

func TestAnyJevTypedProviderExportImportsOnlyReconciledAggregates(t *testing.T) {
	fixture := map[string]any{
		"provider": "private-provider", "checkpoint": "private-checkpoint", "limit_cases": nil,
		"date": "2026-09-21", "seconds": 30.0,
		"overall": typedMetricsFixture(10, 0.7),
		"by_type": map[string]any{
			"choice": typedMetricsFixture(3, 0.6),
			"noul":   typedMetricsFixture(3, 0.7),
			"score":  typedMetricsFixture(4, 0.8),
		},
	}
	items, handled, err := normalizeAnyJevItems("bench/results_typed/2026-09-21/provider.json", fixture)
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("provider typed benchmark import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	item := items[0]
	var payload anyJevTypedBenchmarkImport
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SourceKind != "provider_export" || payload.BackendKind != "provider" || payload.TestExamples != 10 ||
		len(payload.Levels) != 1 || payload.Levels[0].Name != "provider" || len(payload.Levels[0].ByType) != 3 ||
		payload.ModelSHA256 != anyJevStringSHA256("private-checkpoint") || payload.ProviderSHA256 != anyJevStringSHA256("private-provider") {
		t.Fatalf("provider typed benchmark projection is incorrect: %#v", payload)
	}
	for _, privateValue := range []string{"private-provider", "private-checkpoint", "2026-09-21"} {
		if strings.Contains(string(item.Payload), privateValue) {
			t.Fatalf("provider result projection retained %q", privateValue)
		}
	}
	if strings.Contains(string(item.Payload), `"seconds"`) || strings.Contains(string(item.Payload), `"date"`) {
		t.Fatalf("provider result projection retained timing or date fields: %s", item.Payload)
	}
}

func TestAnyJevTypedBenchmarkRejectsUnreconciledOrUnboundedShapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["raw_items"] = []any{} }},
		{name: "unknown metric", mutate: func(value map[string]any) {
			value["levels"].(map[string]any)["L0"].(map[string]any)["overall"].(map[string]any)["raw_prompt"] = "unsupported"
		}},
		{name: "type total mismatch", mutate: func(value map[string]any) {
			value["levels"].(map[string]any)["L0"].(map[string]any)["by_type"].(map[string]any)["choice"].(map[string]any)["n"] = float64(99)
		}},
		{name: "question dimensions mismatch", mutate: func(value map[string]any) {
			value["levels"].(map[string]any)["L0"].(map[string]any)["by_question"].(map[string]any)["private-question-one"].(map[string]any)["workflow"] = "other workflow"
		}},
		{name: "unsupported dataset", mutate: func(value map[string]any) { value["dataset"] = "other-dataset" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevTypedRunnerFixture()
			test.mutate(fixture)
			if _, handled, err := normalizeAnyJevItems("bench/results_typed/2026-09-21/model.json", fixture); !handled || err == nil {
				t.Fatalf("invalid typed result was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
	for _, path := range []string{
		"bench/results_typed/2026-09-21/model.json",
		"bench/results_typed/export.json",
		"bench/results_typed_v01/2026-09-22/model.json",
		"bench/results_typed_small/2026-09-21/model.json",
		"bench/results_typed_diag/2026-09-21/model.json",
		"bench/results_typed_dump/2026-09-21/model.json",
	} {
		if !isAnyJevTypedBenchmarkJSONPath(path) {
			t.Errorf("typed benchmark JSON path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_typed/2026-09-21/model.items.json",
		"bench/results/model.json",
		"bench/results_maze/2026-09-21/model.json",
		"bench/results_typed_future/2026-09-21/model.json",
	} {
		if isAnyJevTypedBenchmarkJSONPath(path) {
			t.Errorf("path outside typed summary projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to verify the typed runner-summary variants through
// inspection, snapshot creation, and strict semantic snapshot readback.
func TestAnyJevTypedSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the source-backed migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	for _, file := range manifest.Files {
		if isAnyJevTypedBenchmarkJSONPath(file.Path) {
			if file.Status != "ready" || file.ImportedCount != 1 || file.RejectedCount != 0 {
				t.Fatalf("typed summary source did not pass strict conversion: %s status=%s imported=%d rejected=%d",
					file.Path, file.Status, file.ImportedCount, file.RejectedCount)
			}
			expected[file.Path] = struct{}{}
		}
	}
	if len(expected) == 0 {
		t.Fatal("source contains no importable typed-decision summary JSON")
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
		if !isAnyJevTypedBenchmarkJSONPath(record.SourcePath) {
			continue
		}
		var header struct {
			SchemaVersion string `json:"schema_version"`
			SourceKind    string `json:"source_kind"`
		}
		if err := json.Unmarshal(record.Payload, &header); err != nil || header.SchemaVersion != anyJevTypedBenchmarkResultSchema ||
			!oneOf(header.SourceKind, "runner_result", "provider_export") {
			t.Fatalf("typed result did not read back in the strict semantic format: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("typed result source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("typed result did not produce exactly one record (count=%d)", seen[path])
		}
	}
}
