package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevBenchmarkFixture() map[string]any {
	return map[string]any{
		"model": "private-model-label", "backend": "hf", "prior": "batch", "combine": "logmean",
		"n": float64(10), "calib": float64(5), "seed": float64(7), "adaptive": true,
		"adaptive_margin": 0.1, "max_permutations": nil, "prior_strength": 0.75,
		"env": map[string]any{"gpu": "private-hardware-id", "date": "2026-09-21"},
		"tasks": []any{map[string]any{
			"task": "private-task-label", "question": "private-question-id", "k": float64(3),
			"n_test": float64(3), "n_calib": float64(2), "answer_mass": 0.77,
			"artifact":        map[string]any{"raw": "private-artifact-marker"},
			"seconds_l0_pass": 0.25, "seconds_per_decision_l0": 0.08,
			"levels": map[string]any{
				"L0": map[string]any{
					"n": float64(3), "acc": 0.66, "macro_f1": 0.6, "brier": 0.5, "nll": 0.9,
					"ece": 0.2, "cov@5%": 0.3, "aurc": 0.4, "flip": 0.1,
					"cyclic_flip_l0": 0.05, "cyclic_flip_raw": 0.15, "mean_shifts": 2.0,
					"prior_method": "private-prior-method", "prior_strength": 0.75, "adaptive": true,
				},
				"L1": map[string]any{
					"n": float64(3), "acc": 0.7, "macro_f1": 0.65, "brier": 0.4, "nll": 0.7,
					"ece": 0.1, "cov@5%": 0.4, "aurc": 0.3, "flip": 0.0,
					"temperature": 1.2, "temperature_reversed": nil,
				},
			},
		}},
	}
}

func TestAnyJevBenchmarkResultImportsSanitizedTypedMetrics(t *testing.T) {
	root := t.TempDir()
	resultPath := filepath.Join(root, "bench", "results_adaptive_m0.1", "2026-09-21", "model.json")
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(anyJevBenchmarkFixture())
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
		t.Fatalf("typed AnyJev benchmark result did not reconcile: %#v", manifest.Summary)
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
		t.Fatalf("unexpected AnyJev benchmark snapshot: %#v", records)
	}
	var payload map[string]any
	if err := json.Unmarshal(records[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	encoded := string(records[0].Payload)
	for _, privateValue := range []string{
		"private-model-label", "private-task-label", "private-question-id", "private-hardware-id",
		"private-artifact-marker", "private-prior-method", "seconds_l0_pass",
	} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("benchmark projection retained %q", privateValue)
		}
	}
	if payload["schema_version"] != anyJevBenchmarkResultSchema || payload["execution_verified"] != false || payload["cutover_evidence_eligible"] != false || payload["human_review_required"] != true {
		t.Fatalf("benchmark history was given the wrong trust status: %#v", payload)
	}
	if payload["model_sha256"] == "" {
		t.Fatal("benchmark model identifier was not bound by hash")
	}
	tasks, ok := payload["tasks"].([]any)
	if !ok || len(tasks) != 1 {
		t.Fatalf("typed task metrics missing: %#v", payload["tasks"])
	}
	task := tasks[0].(map[string]any)
	if task["task_key_sha256"] == "" || task["question_key_sha256"] == "" || task["test_examples"] != float64(3) {
		t.Fatalf("hashed task bindings or test count missing: %#v", task)
	}
	if _, err := os.ReadFile(filepath.Join("..", "..", "schema", "anyjev-benchmark-result-v1.schema.json")); err != nil {
		t.Fatalf("versioned benchmark-result schema missing: %v", err)
	}
	second, err := CreateSnapshot(AnyJev, root, dataRoot)
	if err != nil || second.ID != snapshot.ID {
		t.Fatalf("AnyJev benchmark snapshot was not idempotent: %#v err=%v", second, err)
	}
}

func TestAnyJevBenchmarkResultRejectsUnknownOrInvalidShapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["new_field"] = "unsupported" }},
		{name: "unknown metric", mutate: func(value map[string]any) {
			value["tasks"].([]any)[0].(map[string]any)["levels"].(map[string]any)["L0"].(map[string]any)["raw_prompt"] = "do not import"
		}},
		{name: "out of range metric", mutate: func(value map[string]any) {
			value["tasks"].([]any)[0].(map[string]any)["levels"].(map[string]any)["L0"].(map[string]any)["acc"] = 1.1
		}},
		{name: "metric count mismatch", mutate: func(value map[string]any) {
			value["tasks"].([]any)[0].(map[string]any)["levels"].(map[string]any)["L0"].(map[string]any)["n"] = float64(2)
		}},
		{name: "unknown level", mutate: func(value map[string]any) {
			task := value["tasks"].([]any)[0].(map[string]any)
			task["levels"].(map[string]any)["private-level"] = task["levels"].(map[string]any)["L0"]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := anyJevBenchmarkFixture()
			test.mutate(value)
			_, handled, err := normalizeAnyJevItems("bench/results/model.json", value)
			if !handled || err == nil {
				t.Fatalf("invalid benchmark result was not rejected: handled=%v err=%v", handled, err)
			}
		})
	}
	if _, handled, err := normalizeAnyJevItems("bench/results/model.json", []any{"raw"}); !handled || err == nil {
		t.Fatalf("non-object benchmark result was not rejected: handled=%v err=%v", handled, err)
	}
}

func TestAnyJevBenchmarkProjectionIsScopedToKnownRunSummaryFamilies(t *testing.T) {
	for _, path := range []string{
		"bench/results/model.json",
		"bench/results_adaptive_m0.1/2026-09-21/model.json",
		"bench/results_adaptive_spread_m0.1/2026-09-21/model.json",
		"bench/results_batchprior_v0/2026-09-20/model.json",
		"bench/results_dump/2026-09-20/model.json",
		"bench/results_small/2026-09-20/model.json",
		"bench/results_v01/2026-09-20/model.json",
	} {
		if !isAnyJevBenchmarkJSONPath(path) {
			t.Errorf("known run-summary path was not selected: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_typed/2026-09-22/model.json",
		"bench/results_distill/2026-09-22/model.json",
		"bench/results_universal/2026-09-22/model.json",
		"demo/results/model.json",
		"labels/questions.csv",
		"bench/results_small/2026-09-20/model.items.json",
	} {
		if isAnyJevBenchmarkJSONPath(path) {
			t.Errorf("unconverted result path was selected for the strict converter: %s", path)
		}
	}
}
