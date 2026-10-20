package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevExitLabelsMetricFixture(accuracy, ece, nll float64, withDifference bool) map[string]any {
	row := map[string]any{
		"acc": accuracy, "acc_ci95": []any{accuracy - 0.05, accuracy + 0.05}, "ece": ece, "nll": nll,
	}
	if withDifference {
		row["diff_vs_L1_ci95"] = []any{-0.02, 0.04}
	}
	return row
}

func anyJevExitLabelsFixture() map[string]any {
	return map[string]any{
		"blocks":  []any{float64(22), float64(28), float64(36)},
		"budgets": []any{float64(20), float64(50)},
		"date":    "2026-09-28T10:11:12.123456",
		"env":     map[string]any{"gpu": "private-device", "path": "C:/private/model"},
		"model":   "private/model-label",
		"rows": map[string]any{
			"raw":     anyJevExitLabelsMetricFixture(0.65, 0.12, 0.7, false),
			"L1@20":   anyJevExitLabelsMetricFixture(0.68, 0.1, 0.65, false),
			"L1@50":   anyJevExitLabelsMetricFixture(0.7, 0.09, 0.6, false),
			"head@20": anyJevExitLabelsMetricFixture(0.72, 0.08, 0.55, true),
			"head@50": anyJevExitLabelsMetricFixture(0.74, 0.07, 0.5, true),
		},
		"seeds": []any{float64(0), float64(1), float64(2)},
	}
}

func TestAnyJevExitLabelsPreservesBudgetCurvesAndRedactsEnvironment(t *testing.T) {
	items, handled, err := normalizeAnyJevItems("bench/results_exit/2026-09-28/model.labels.json", anyJevExitLabelsFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("exit labels import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevExitLabelsImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if items[0].Type != "decision_label_or_evaluation" || payload.SchemaVersion != anyJevExitLabelsResultSchema ||
		payload.ModelSHA256 != anyJevStringSHA256("private/model-label") || len(payload.Blocks) != 3 ||
		len(payload.Budgets) != 2 || len(payload.Seeds) != 3 || len(payload.Rows) != 5 ||
		payload.Rows[0].Method != "head" || payload.Rows[0].Budget == nil || *payload.Rows[0].Budget != 20 ||
		len(payload.Rows[0].DiffVsL1CI95) != 2 || !payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("exit labels projection is incorrect: %#v", payload)
	}
	encoded := string(items[0].Payload)
	for _, privateValue := range []string{"private/model-label", "2026-09-28", "private-device", "C:/private/model", "L1@20", "head@20"} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("exit labels projection retained %q", privateValue)
		}
	}
}

func TestAnyJevExitLabelsRejectsUnknownOrInconsistentRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing root field", mutate: func(value map[string]any) { delete(value, "seeds") }},
		{name: "unknown root field", mutate: func(value map[string]any) { value["extra"] = true }},
		{name: "duplicate budget", mutate: func(value map[string]any) { value["budgets"] = []any{float64(20), float64(20)} }},
		{name: "undeclared method budget", mutate: func(value map[string]any) {
			rows := value["rows"].(map[string]any)
			rows["L1@30"] = anyJevExitLabelsMetricFixture(0.7, 0.1, 0.6, false)
		}},
		{name: "missing L1 budget", mutate: func(value map[string]any) { delete(value["rows"].(map[string]any), "L1@50") }},
		{name: "head row missing paired comparison", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["head@20"] = anyJevExitLabelsMetricFixture(0.72, 0.08, 0.55, false)
		}},
		{name: "unknown metric", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["raw"].(map[string]any)["private"] = "secret"
		}},
		{name: "reversed confidence interval", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["raw"].(map[string]any)["acc_ci95"] = []any{0.9, 0.1}
		}},
		{name: "credential-like model", mutate: func(value map[string]any) { value["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevExitLabelsFixture()
			test.mutate(fixture)
			if _, err := normalizeAnyJevExitLabelsResult(fixture); err == nil {
				t.Fatal("invalid exit labels result was accepted")
			}
		})
	}
}

func TestAnyJevExitLabelsPathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_exit/2026-09-28/model.labels.json",
		"BENCH/results_exit/model.LABELS.JSON",
	} {
		if !isAnyJevExitLabelsJSONPath(path) {
			t.Errorf("exit labels JSON path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_exit_extra/model.labels.json",
		"bench/results_exit/model.labels.items.json",
		"other/results_exit/model.labels.json",
	} {
		if isAnyJevExitLabelsJSONPath(path) {
			t.Errorf("path outside exit labels projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to a local AnyJev checkout to verify every
// label-study result through inspect and temporary snapshot readback.
func TestAnyJevExitLabelsSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the exit labels migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	var matchingFiles, notReady int
	for _, file := range manifest.Files {
		if !isAnyJevExitLabelsJSONPath(file.Path) {
			continue
		}
		matchingFiles++
		if file.Status != "ready" || file.ImportedCount != 1 {
			notReady++
			continue
		}
		expected[file.Path] = struct{}{}
	}
	if matchingFiles == 0 || notReady != 0 {
		t.Fatalf("label-study source preflight mismatch: files=%d not_ready=%d", matchingFiles, notReady)
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
		if !isAnyJevExitLabelsJSONPath(record.SourcePath) {
			continue
		}
		var payload anyJevExitLabelsImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevExitLabelsResultSchema {
			t.Fatalf("label-study result did not read back in strict semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatalf("label-study trust status changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("label-study source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("label-study result did not produce exactly one record (count=%d)", seen[path])
		}
	}
}
