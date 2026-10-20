package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevExitJevModeRuleFixture(block int, accuracy, ece, flip, nll, relative float64, accuracyCI, diffCI []any) map[string]any {
	return map[string]any{
		"acc": accuracy, "acc_ci95": accuracyCI, "diff_vs_full_ci95": diffCI,
		"ece": ece, "flip": flip, "mean_blocks": float64(block), "nll": nll, "relative_depth": relative,
	}
}

func anyJevExitJevModeFixture() map[string]any {
	return map[string]any{
		"blocks": []any{float64(1), float64(2)},
		"date":   "2026-09-28T10:11:12.123456",
		"env":    map[string]any{"gpu": "private-device", "path": "C:/private/model"},
		"kinds":  []any{"lda", "ridge"},
		"latency": map[string]any{
			"batch_ms": 2.5, "gflops": 4.5, "measured_blocks": float64(2), "path": "private/latency.json",
			"raw_batch_ms": 3.5, "raw_gflops": 5.5, "raw_single_ms": 4.5, "single_ms": 3.0,
		},
		"model": "private/model-label", "n_blocks": float64(2),
		"oof_acc_by_block": []any{0.55, 0.8},
		"per_block": map[string]any{
			"1": map[string]any{
				"acc": 0.7, "acc_ci95": []any{0.5, 0.8}, "diff_vs_full_ci95": []any{-0.2, 0.0},
				"ece": 0.1, "flip": 0.25, "nll": 0.5, "oof_acc": 0.55, "relative_depth": 0.5,
			},
			"2": map[string]any{
				"acc": 0.8, "acc_ci95": []any{0.7, 0.9}, "diff_vs_full_ci95": []any{0.0, 0.0},
				"ece": 0.08, "flip": 0.1, "nll": 0.3, "oof_acc": 0.8, "relative_depth": 1.0,
			},
		},
		"questions": map[string]any{
			"private-question-a": map[string]any{
				"K": float64(4), "cv_block": float64(1), "kind": []any{"lda", "ridge"},
				"oof_acc": []any{0.5, 0.8}, "oof_nll": []any{0.8, 0.3},
				"test_acc": []any{0.7, 0.8}, "test_flip": []any{0.2, 0.1},
			},
			"private-question-b": map[string]any{
				"K": float64(3), "cv_block": float64(2), "kind": []any{"ridge", "lda"},
				"oof_acc": []any{0.6, 0.8}, "oof_nll": []any{0.7, 0.4},
				"test_acc": []any{0.6, 0.9}, "test_flip": []any{0.3, 0.0},
			},
		},
		"recommended_block": float64(1),
		"references": map[string]any{
			"L0":  map[string]any{"acc": 0.75, "ece": 0.11, "path": "private/L0.json"},
			"L1":  map[string]any{"acc": 0.78, "ece": 0.09, "path": "private/L1.json"},
			"raw": map[string]any{"acc": 0.8, "ece": 0.08, "path": "private/raw.json"},
		},
		"rules": map[string]any{
			"cv": map[string]any{
				"acc": 0.75, "acc_ci95": []any{0.65, 0.85}, "diff_vs_full_ci95": []any{-0.1, 0.1},
				"ece": 0.12, "flip": 0.15, "mean_blocks": 1.5, "nll": 0.4, "relative_depth": 0.75,
			},
			"fixed@1": anyJevExitJevModeRuleFixture(1, 0.7, 0.1, 0.25, 0.5, 0.5, []any{0.5, 0.8}, []any{-0.2, 0.0}),
			"fixed@2": anyJevExitJevModeRuleFixture(2, 0.8, 0.08, 0.1, 0.3, 1.0, []any{0.7, 0.9}, []any{0.0, 0.0}),
		},
		"slack": 0.3,
	}
}

func TestAnyJevExitJevModeKeepsValidatedSelectionAndRedactsIdentifiers(t *testing.T) {
	items, handled, err := normalizeAnyJevItems("bench/results_exit/2026-09-28/model.jevmode.json", anyJevExitJevModeFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("exit Jev-mode import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevExitJevModeImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if items[0].Type != "decision_label_or_evaluation" || payload.SchemaVersion != anyJevExitJevModeResultSchema ||
		payload.ModelSHA256 != anyJevStringSHA256("private/model-label") || payload.TransformerLayers != 2 ||
		payload.RecommendedBlock != 1 || len(payload.Questions) != 2 || len(payload.Rules) != 3 ||
		len(payload.References) != 3 || !payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("exit Jev-mode projection is incorrect: %#v", payload)
	}
	encoded := string(items[0].Payload)
	for _, privateValue := range []string{
		"private/model-label", "2026-09-28", "private-device", "C:/private/model",
		"private-question-a", "private-question-b", "private/L0.json", "private/latency.json",
	} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("exit Jev-mode projection retained %q", privateValue)
		}
	}
}

func TestAnyJevExitJevModeRejectsInconsistentOrUnsupportedResults(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing required root field", mutate: func(value map[string]any) { delete(value, "references") }},
		{name: "unknown root field", mutate: func(value map[string]any) { value["private_extra"] = true }},
		{name: "non-monotonic blocks", mutate: func(value map[string]any) { value["blocks"] = []any{float64(2), float64(1)} }},
		{name: "wrong calibration aggregate", mutate: func(value map[string]any) { value["oof_acc_by_block"].([]any)[0] = 0.7 }},
		{name: "recommendation violates slack rule", mutate: func(value map[string]any) { value["recommended_block"] = float64(2) }},
		{name: "question head outside allowlist", mutate: func(value map[string]any) {
			value["questions"].(map[string]any)["private-question-a"].(map[string]any)["kind"].([]any)[0] = "secret-head"
		}},
		{name: "fixed rule differs from per-block metrics", mutate: func(value map[string]any) {
			value["rules"].(map[string]any)["fixed@1"].(map[string]any)["acc"] = 0.99
		}},
		{name: "invalid embedded latency", mutate: func(value map[string]any) {
			value["latency"].(map[string]any)["single_ms"] = 0.0
		}},
		{name: "credential-like model", mutate: func(value map[string]any) { value["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevExitJevModeFixture()
			test.mutate(fixture)
			if _, err := normalizeAnyJevExitJevModeResult(fixture); err == nil {
				t.Fatal("invalid exit Jev-mode result was accepted")
			}
		})
	}
}

func TestAnyJevExitJevModePathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_exit/2026-09-28/model.jevmode.json",
		"BENCH/results_exit/model.JEVMODE.JSON",
	} {
		if !isAnyJevExitJevModeJSONPath(path) {
			t.Errorf("exit Jev-mode JSON path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_exit_extra/model.jevmode.json",
		"bench/results_exit/model.jevmode.items.json",
		"other/results_exit/model.jevmode.json",
	} {
		if isAnyJevExitJevModeJSONPath(path) {
			t.Errorf("path outside exit Jev-mode projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to a local AnyJev checkout to verify every
// Jev-mode result through inspect and temporary snapshot readback.
func TestAnyJevExitJevModeSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the exit Jev-mode migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	var matchingFiles, notReady int
	for _, file := range manifest.Files {
		if !isAnyJevExitJevModeJSONPath(file.Path) {
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
		t.Fatalf("Jev-mode source preflight mismatch: files=%d not_ready=%d", matchingFiles, notReady)
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
		if !isAnyJevExitJevModeJSONPath(record.SourcePath) {
			continue
		}
		var payload anyJevExitJevModeImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevExitJevModeResultSchema {
			t.Fatalf("Jev-mode result did not read back in strict semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatalf("Jev-mode trust status changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("Jev-mode source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("Jev-mode result did not produce exactly one record (count=%d)", seen[path])
		}
	}
}
