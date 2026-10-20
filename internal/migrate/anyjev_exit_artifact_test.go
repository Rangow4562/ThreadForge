package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevExitArtifactRowFixture(k, calibrationExamples, testExamples, layer int, accuracy, nll, milliseconds float64, cacheMean *float64) map[string]any {
	row := map[string]any{
		"K": float64(k), "n_calib": float64(calibrationExamples), "n_test": float64(testExamples),
		"layer_abs": float64(layer), "relative_depth": float64(layer) / 4, "method": "head:lda",
		"oof_acc": 0.7, "oof_nll": 0.4, "temperature": 1.2, "acc": accuracy, "ece": 0.1,
		"nll": nll, "fit_seconds": 0.25, "ms_per_decision": milliseconds,
	}
	if cacheMean != nil {
		row["cache_mean_abs_dp"] = *cacheMean
		row["cache_argmax_disagreement"] = 0.1
	}
	return row
}

func anyJevExitArtifactFixture() map[string]any {
	cacheA, cacheB := 0.02, 0.04
	return map[string]any{
		"model": "private/model-label", "n_blocks": float64(4), "candidates": []any{float64(2), float64(4)},
		"date": "2026-09-28T10:11:12.123456", "env": map[string]any{"gpu": "private-device", "path": "C:/private/model"},
		"summary": map[string]any{
			"typed_pooled": map[string]any{"n": float64(4), "acc": 0.625, "ece": 0.12, "nll": 0.55},
			"all_pooled":   map[string]any{"n": float64(7), "acc": 4.5 / 7, "ece": 0.14, "nll": 0.55},
			"mean_blocks":  2.0, "mean_ms_per_decision": 10.0, "cache_mean_abs_dp": 0.03,
		},
		"validation": map[string]any{
			"typed.private-question-a": anyJevExitArtifactRowFixture(2, 8, 2, 2, 0.5, 0.7, 10, &cacheA),
			"typed.private-question-b": anyJevExitArtifactRowFixture(2, 8, 2, 2, 0.75, 0.4, 12, &cacheB),
			"bench.private-benchmark":  anyJevExitArtifactRowFixture(3, 10, 3, 2, 2.0/3, 0.55, 8, nil),
		},
	}
}

func anyJevExitArtifactCVFixture() map[string]any {
	fixture := anyJevExitArtifactFixture()
	fixture["candidates"] = []any{float64(2), float64(3), float64(4)}
	validation := fixture["validation"].(map[string]any)
	cache := 0.04
	validation["typed.private-question-b"] = anyJevExitArtifactRowFixture(2, 8, 2, 3, 0.75, 0.4, 12, &cache)
	validation["bench.private-benchmark"] = anyJevExitArtifactRowFixture(3, 10, 3, 4, 2.0/3, 0.55, 8, nil)
	fixture["summary"].(map[string]any)["mean_blocks"] = 3.0
	return fixture
}

func TestAnyJevExitArtifactReconcilesRowsAndRedactsLabels(t *testing.T) {
	items, handled, err := normalizeAnyJevItems("bench/results_exit/2026-09-28/model.artifact.json", anyJevExitArtifactFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("exit artifact import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevExitArtifactImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if items[0].Type != "decision_label_or_evaluation" || payload.SchemaVersion != anyJevExitArtifactResultSchema ||
		payload.ModelSHA256 != anyJevStringSHA256("private/model-label") || payload.TransformerLayers != 4 ||
		len(payload.CandidateLayers) != 2 || payload.SelectionMode != anyJevExitArtifactFixedSelectionMode || len(payload.Validation.PerQuestion) != 3 ||
		payload.Validation.Summary.AllPooled.N != 7 || payload.Validation.Summary.TypedPooled.N != 4 ||
		!payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("exit artifact projection is incorrect: %#v", payload)
	}
	encoded := string(items[0].Payload)
	for _, privateValue := range []string{
		"private/model-label", "2026-09-28", "private-device", "C:/private/model",
		"private-question-a", "private-question-b", "private-benchmark",
	} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("exit artifact projection retained %q", privateValue)
		}
	}
}

func TestAnyJevExitArtifactCVRetainsPerQuestionSelectionSemantics(t *testing.T) {
	items, handled, err := normalizeAnyJevItems("bench/results_exit/2026-09-28/model.artifact_cv.json", anyJevExitArtifactCVFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("exit artifact CV import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevExitArtifactImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SelectionMode != anyJevExitArtifactCVSelectionMode || len(payload.CandidateLayers) != 3 ||
		payload.Validation.Summary.MeanBlocks != 3 || payload.CutoverEligible {
		t.Fatalf("exit artifact CV semantics were not retained: %#v", payload)
	}
}

func TestAnyJevExitArtifactRejectsUnsupportedOrUnreconciledData(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["extra"] = true }},
		{name: "unknown validation family", mutate: func(value map[string]any) {
			value["validation"].(map[string]any)["private.private-question"] = anyJevExitArtifactRowFixture(2, 8, 2, 2, 0.5, 0.7, 10, nil)
		}},
		{name: "wrong pooled example count", mutate: func(value map[string]any) {
			value["summary"].(map[string]any)["all_pooled"].(map[string]any)["n"] = float64(8)
		}},
		{name: "wrong pooled accuracy", mutate: func(value map[string]any) {
			value["summary"].(map[string]any)["all_pooled"].(map[string]any)["acc"] = 0.99
		}},
		{name: "selected layer outside candidates", mutate: func(value map[string]any) {
			value["candidates"] = []any{float64(3)}
		}},
		{name: "fixed rows select different layers", mutate: func(value map[string]any) {
			row := value["validation"].(map[string]any)["bench.private-benchmark"].(map[string]any)
			row["layer_abs"] = float64(4)
			row["relative_depth"] = float64(1)
		}},
		{name: "relative depth disagrees with selected layer", mutate: func(value map[string]any) {
			value["validation"].(map[string]any)["typed.private-question-a"].(map[string]any)["relative_depth"] = 0.75
		}},
		{name: "unpaired cache metrics", mutate: func(value map[string]any) {
			delete(value["validation"].(map[string]any)["typed.private-question-a"].(map[string]any), "cache_argmax_disagreement")
		}},
		{name: "credential-like model", mutate: func(value map[string]any) { value["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevExitArtifactFixture()
			test.mutate(fixture)
			if _, err := normalizeAnyJevExitArtifactResult(fixture, anyJevExitArtifactFixedSelectionMode); err == nil {
				t.Fatal("invalid exit artifact result was accepted")
			}
		})
	}
}

func TestAnyJevExitArtifactPathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_exit/2026-09-28/model.artifact.json",
		"BENCH/results_exit/model.ARTIFACT.JSON",
		"bench/results_exit/2026-09-28/model.artifact_cv.json",
		"BENCH/results_exit/model.ARTIFACT_CV.JSON",
	} {
		if !isAnyJevExitArtifactJSONPath(path) {
			t.Errorf("exit artifact JSON path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_exit_extra/model.artifact.json",
		"bench/results_exit/model.artifact.items.json",
		"bench/results_exit/model.artifact_cvv.json",
		"other/results_exit/model.artifact.json",
	} {
		if isAnyJevExitArtifactJSONPath(path) {
			t.Errorf("path outside exit artifact projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to a local AnyJev checkout to verify every
// exit artifact result through inspect and temporary snapshot readback.
func TestAnyJevExitArtifactSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the exit artifact migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]string)
	var matchingFiles, notReady int
	for _, file := range manifest.Files {
		if !isAnyJevExitArtifactJSONPath(file.Path) {
			continue
		}
		matchingFiles++
		if file.Status != "ready" || file.ImportedCount != 1 {
			notReady++
			continue
		}
		mode, ok := anyJevExitArtifactSelectionMode(file.Path)
		if !ok {
			t.Fatalf("unrecognized artifact selection mode for %s", file.Path)
		}
		expected[file.Path] = mode
	}
	if matchingFiles == 0 || notReady != 0 {
		t.Fatalf("exit artifact source preflight mismatch: files=%d not_ready=%d", matchingFiles, notReady)
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
	modeCounts := make(map[string]int)
	for _, record := range records {
		if !isAnyJevExitArtifactJSONPath(record.SourcePath) {
			continue
		}
		var payload anyJevExitArtifactImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevExitArtifactResultSchema {
			t.Fatal("exit artifact result did not read back in the strict semantic format")
		}
		if expected[record.SourcePath] != payload.SelectionMode {
			t.Fatalf("exit artifact selection semantics changed for %s", record.SourcePath)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatal("exit artifact trust status changed during snapshot readback")
		}
		seen[record.SourcePath]++
		modeCounts[payload.SelectionMode]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("exit artifact source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("exit artifact source row count is invalid: %d", seen[path])
		}
	}
	if modeCounts[anyJevExitArtifactFixedSelectionMode] != 5 || modeCounts[anyJevExitArtifactCVSelectionMode] != 3 {
		t.Fatalf("unexpected fixed/CV source counts: %#v", modeCounts)
	}
}
