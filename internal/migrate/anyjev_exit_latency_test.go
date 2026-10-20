package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevExitLatencyRowFixture(stateTokens, promptTokens, blocks int, depth float64, mode string, gflops float64) map[string]any {
	return map[string]any{
		"batch_ms": 42.5, "blocks": float64(blocks), "depth": depth, "gflops": gflops,
		"mode": mode, "prompt_tokens": float64(promptTokens), "single_ms": 45.5,
		"state_tokens": float64(stateTokens),
	}
}

func anyJevExitLatencyFixture() map[string]any {
	return map[string]any{
		"date":        "2026-09-22T10:11:12.123456",
		"env":         map[string]any{"gpu": "private-device", "path": "C:/private/model"},
		"hidden_size": float64(4096), "model": "private/model-label",
		"n_layers": float64(32), "params_per_block": float64(100000000),
		"rows": []any{
			anyJevExitLatencyRowFixture(110, 120, 32, 1, "raw (plain forward)", 768),
			anyJevExitLatencyRowFixture(110, 120, 16, 0.5, "truncated forward (head at this depth)", 384),
		},
	}
}

func TestAnyJevExitLatencyResultKeepsConditionsAndReconciledCosts(t *testing.T) {
	for _, test := range []struct {
		path      string
		condition string
	}{
		{"bench/results_exit/2026-09-22/model.latency.json", "quiet_gpu"},
		{"bench/results_exit/2026-09-22/model.latency_loaded.json", "contended_gpu"},
	} {
		items, handled, err := normalizeAnyJevItems(test.path, anyJevExitLatencyFixture())
		if err != nil || !handled || len(items) != 1 {
			t.Fatalf("exit-latency import: handled=%v items=%d err=%v", handled, len(items), err)
		}
		var payload anyJevExitLatencyImport
		if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.SchemaVersion != anyJevExitLatencyResultSchema || payload.ModelSHA256 != anyJevStringSHA256("private/model-label") ||
			payload.TransformerLayers != 32 || payload.HiddenSize != 4096 || payload.ParamsPerBlock != 100000000 ||
			payload.HostCondition != test.condition || len(payload.Measurements) != 2 ||
			payload.Measurements[0].Mode != "truncated_head" || payload.Measurements[1].Mode != "raw" ||
			payload.Measurements[1].GFLOPS != 768 || !payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatalf("exit-latency projection is incorrect: %#v", payload)
		}
		for _, privateValue := range []string{"private/model-label", "2026-09-22", "private-device", "C:/private/model"} {
			if strings.Contains(string(items[0].Payload), privateValue) {
				t.Fatalf("exit-latency projection retained %q", privateValue)
			}
		}
	}
}

func TestAnyJevExitLatencyRejectsMalformedDimensionsOrCosts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["extra"] = true }},
		{name: "unknown row field", mutate: func(value map[string]any) {
			value["rows"].([]any)[0].(map[string]any)["prompt"] = "secret"
		}},
		{name: "duplicate dimensions", mutate: func(value map[string]any) {
			value["rows"].([]any)[1].(map[string]any)["depth"] = float64(1)
			value["rows"].([]any)[1].(map[string]any)["mode"] = "raw (plain forward)"
			value["rows"].([]any)[1].(map[string]any)["blocks"] = float64(32)
		}},
		{name: "incorrect raw full depth", mutate: func(value map[string]any) {
			value["rows"].([]any)[0].(map[string]any)["blocks"] = float64(16)
		}},
		{name: "depth block mismatch", mutate: func(value map[string]any) {
			value["rows"].([]any)[1].(map[string]any)["blocks"] = float64(10)
		}},
		{name: "gflops mismatch", mutate: func(value map[string]any) {
			value["rows"].([]any)[0].(map[string]any)["gflops"] = 12.0
		}},
		{name: "unsupported mode", mutate: func(value map[string]any) {
			value["rows"].([]any)[0].(map[string]any)["mode"] = "unknown"
		}},
		{name: "invalid depth", mutate: func(value map[string]any) {
			value["rows"].([]any)[1].(map[string]any)["depth"] = 0.0
		}},
		{name: "credential-like model", mutate: func(value map[string]any) { value["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevExitLatencyFixture()
			test.mutate(fixture)
			if _, err := normalizeAnyJevExitLatencyResult("bench/results_exit/model.latency.json", fixture); err == nil {
				t.Fatal("invalid exit-latency result was accepted")
			}
		})
	}
}

func TestAnyJevExitLatencyPathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_exit/2026-09-22/model.latency.json",
		"bench/results_exit/2026-09-22/model.latency_loaded.json",
	} {
		if !isAnyJevExitLatencyJSONPath(path) {
			t.Errorf("exit-latency JSON path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_exit/model.jevmode.json",
		"bench/results_exit_extra/model.latency.json",
		"bench/results_exit/model.latency.items.json",
	} {
		if isAnyJevExitLatencyJSONPath(path) {
			t.Errorf("path outside exit-latency projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to a local AnyJev checkout to verify all
// quiet and contended exit-latency results through inspect and snapshot readback.
func TestAnyJevExitLatencySourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the exit-latency migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	for _, file := range manifest.Files {
		if isAnyJevExitLatencyJSONPath(file.Path) && file.Status == "ready" && file.ImportedCount == 1 {
			expected[file.Path] = struct{}{}
		}
	}
	if len(expected) == 0 {
		t.Fatal("source contains no importable exit-latency JSON")
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
		if !isAnyJevExitLatencyJSONPath(record.SourcePath) {
			continue
		}
		var payload anyJevExitLatencyImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevExitLatencyResultSchema {
			t.Fatalf("exit-latency result did not read back in strict semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatalf("exit-latency trust status changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("exit-latency source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("exit-latency result did not produce exactly one record (count=%d): %s", seen[path], path)
		}
	}
}
