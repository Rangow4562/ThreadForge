package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevExitDepthMetricsFixture(acc, ece, nll float64) map[string]any {
	return map[string]any{"acc": acc, "ece": ece, "nll": nll}
}

func anyJevExitDepthLayerFixture(offset float64) map[string]any {
	return map[string]any{
		"head":      anyJevExitDepthMetricsFixture(0.7+offset, 0.1, 0.5),
		"head_flip": 0.04 + offset,
		"lens":      anyJevExitDepthMetricsFixture(0.6+offset, 0.2, 0.8),
		"lens+T":    anyJevExitDepthMetricsFixture(0.65+offset, 0.12, 0.7),
	}
}

func anyJevExitDepthFixture() map[string]any {
	return map[string]any{
		"date":   "2026-09-22T10:11:12.123456",
		"env":    map[string]any{"gpu": "private-device", "path": "C:/private/model"},
		"layers": []any{float64(12), float64(8)},
		"model":  "private/model-label",
		"per_layer": map[string]any{
			"8":  anyJevExitDepthLayerFixture(0),
			"12": anyJevExitDepthLayerFixture(0.03),
		},
	}
}

func TestAnyJevExitDepthResultProjectsSortedAllowlistedMetrics(t *testing.T) {
	items, handled, err := normalizeAnyJevItems("bench/results_exit/2026-09-22/model.depth.json", anyJevExitDepthFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("exit-depth import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	if items[0].Type != "decision_label_or_evaluation" {
		t.Fatalf("unexpected record type: %s", items[0].Type)
	}
	var payload anyJevExitDepthImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != anyJevExitDepthResultSchema || payload.ModelSHA256 != anyJevStringSHA256("private/model-label") ||
		len(payload.Blocks) != 2 || payload.Blocks[0].Block != 8 || payload.Blocks[1].Block != 12 ||
		payload.Blocks[1].LensTemperature.Accuracy != 0.68 || !payload.HumanReviewRequired ||
		payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("exit-depth projection is incorrect: %#v", payload)
	}
	for _, privateValue := range []string{"private/model-label", "2026-09-22", "private-device", "C:/private/model"} {
		if strings.Contains(string(items[0].Payload), privateValue) {
			t.Fatalf("exit-depth projection retained %q", privateValue)
		}
	}
}

func TestAnyJevExitDepthRejectsUnknownOrInconsistentMetrics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["raw_rows"] = []any{} }},
		{name: "unknown environment-free metadata is not accepted", mutate: func(value map[string]any) { value["n_blocks"] = float64(36) }},
		{name: "duplicate layer", mutate: func(value map[string]any) { value["layers"].([]any)[1] = float64(12) }},
		{name: "layer map mismatch", mutate: func(value map[string]any) { delete(value["per_layer"].(map[string]any), "12") }},
		{name: "unknown metric family", mutate: func(value map[string]any) {
			value["per_layer"].(map[string]any)["8"].(map[string]any)["raw"] = anyJevExitDepthMetricsFixture(0.5, 0.5, 0.5)
		}},
		{name: "unknown metric field", mutate: func(value map[string]any) {
			value["per_layer"].(map[string]any)["8"].(map[string]any)["head"].(map[string]any)["prompt"] = "secret"
		}},
		{name: "metric out of range", mutate: func(value map[string]any) {
			value["per_layer"].(map[string]any)["8"].(map[string]any)["lens"].(map[string]any)["acc"] = 2.0
		}},
		{name: "invalid date", mutate: func(value map[string]any) { value["date"] = "yesterday" }},
		{name: "credential-like model", mutate: func(value map[string]any) { value["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevExitDepthFixture()
			test.mutate(fixture)
			if _, err := normalizeAnyJevExitDepthResult(fixture); err == nil {
				t.Fatal("invalid exit-depth result was accepted")
			}
		})
	}
}

func TestAnyJevExitDepthPathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_exit/2026-09-22/model.depth.json",
		"bench/results_exit/model.depth.json",
	} {
		if !isAnyJevExitDepthJSONPath(path) {
			t.Errorf("exit-depth JSON path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_exit/model.jevmode.json",
		"bench/results_exit/model.latency.json",
		"bench/results_exit_extra/model.depth.json",
		"bench/results_exit/model.depth.items.json",
	} {
		if isAnyJevExitDepthJSONPath(path) {
			t.Errorf("path outside exit-depth projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to a local AnyJev checkout to verify every
// supported depth result through inspect and temporary snapshot readback.
func TestAnyJevExitDepthSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the source-backed exit-depth migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	for _, file := range manifest.Files {
		if isAnyJevExitDepthJSONPath(file.Path) && file.Status == "ready" && file.ImportedCount == 1 {
			expected[file.Path] = struct{}{}
		}
	}
	if len(expected) == 0 {
		t.Fatal("source contains no importable exit-depth JSON")
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
		if !isAnyJevExitDepthJSONPath(record.SourcePath) {
			continue
		}
		var payload anyJevExitDepthImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevExitDepthResultSchema {
			t.Fatalf("exit-depth result did not read back in the strict semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatalf("exit-depth result trust status changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("exit-depth source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("exit-depth result did not produce exactly one record (count=%d): %s", seen[path], path)
		}
	}
}
