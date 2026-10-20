package migrate

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevLatencyRowFixture(stateTokens, optionCount int, backend string, shared bool) map[string]any {
	row := map[string]any{
		"backend": backend, "state_tokens": float64(stateTokens), "k": float64(optionCount), "n": float64(32),
		"batch_raw_ms": 10.7, "batch_L0_full_ms": 42.8,
		"single_raw_ms": 48.5, "single_L0_full_ms": 54.6,
	}
	if shared {
		row["batch_L0_shared_ms"] = 22.1
		row["single_L0_shared_ms"] = 108.5
		row["shared_fallbacks"] = float64(2)
	}
	return row
}

func anyJevLatencyFixture(backend string, shared bool) map[string]any {
	return map[string]any{
		"model": "private/model-label", "backend": backend, "date": "2026-09-21T10:11:12.123456",
		"rows": []any{
			anyJevLatencyRowFixture(1000, 20, backend, shared),
			anyJevLatencyRowFixture(110, 4, backend, shared),
		},
	}
}

func TestAnyJevLatencyResultImportsSortedMeasurementsWithoutHostLabels(t *testing.T) {
	items, handled, err := normalizeAnyJevItems(
		"bench/results_latency/2026-09-21/private-model.json", anyJevLatencyFixture("hf", true),
	)
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("latency benchmark import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	if items[0].Type != "decision_label_or_evaluation" {
		t.Fatalf("unexpected record type: %s", items[0].Type)
	}
	var payload anyJevLatencyBenchmarkImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != anyJevLatencyBenchmarkResultSchema || payload.Backend != "hf" ||
		payload.ModelSHA256 != anyJevStringSHA256("private/model-label") || len(payload.Rows) != 2 ||
		payload.Rows[0].StateTokens != 110 || payload.Rows[0].OptionCount != 4 || payload.Rows[0].BatchStates != 32 ||
		payload.Rows[0].BatchL0SharedMS == nil || payload.Rows[1].SingleL0SharedMS == nil ||
		!payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEvidenceEligible {
		t.Fatalf("latency result projection is incorrect: %#v", payload)
	}
	for _, privateValue := range []string{"private/model-label", "2026-09-21T10:11:12", "shared_fallbacks\":2", "batch_L0_shared_ms\":22.1"} {
		if strings.Contains(string(items[0].Payload), privateValue) {
			t.Fatalf("latency result projection retained %q", privateValue)
		}
	}
	if !strings.Contains(string(items[0].Payload), `"batch_l0_shared_ms":22.1`) {
		t.Fatalf("latency result omitted the shared-prefix measurement: %s", items[0].Payload)
	}
}

func TestAnyJevLatencyVLLMResultImportsWithoutSharedPrefixFields(t *testing.T) {
	item, err := normalizeAnyJevLatencyBenchmarkResult(anyJevLatencyFixture("vllm", false))
	if err != nil {
		t.Fatal(err)
	}
	var payload anyJevLatencyBenchmarkImport
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Backend != "vllm" || payload.Rows[0].BatchL0SharedMS != nil || payload.Rows[0].SingleL0SharedMS != nil {
		t.Fatalf("vLLM latency result shared-prefix projection is incorrect: %#v", payload)
	}
}

func TestAnyJevLatencyRejectsUnknownInconsistentAndUnboundedData(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["env"] = map[string]any{} }},
		{name: "unknown row field", mutate: func(value map[string]any) {
			value["rows"].([]any)[0].(map[string]any)["prompt"] = "private"
		}},
		{name: "row backend mismatch", mutate: func(value map[string]any) {
			value["rows"].([]any)[0].(map[string]any)["backend"] = "vllm"
		}},
		{name: "duplicate dimensions", mutate: func(value map[string]any) {
			rows := value["rows"].([]any)
			rows[1].(map[string]any)["state_tokens"] = rows[0].(map[string]any)["state_tokens"]
			rows[1].(map[string]any)["k"] = rows[0].(map[string]any)["k"]
		}},
		{name: "batch size mismatch", mutate: func(value map[string]any) {
			value["rows"].([]any)[1].(map[string]any)["n"] = float64(16)
		}},
		{name: "unpaired shared metric", mutate: func(value map[string]any) {
			delete(value["rows"].([]any)[0].(map[string]any), "single_L0_shared_ms")
		}},
		{name: "fallback without shared metrics", mutate: func(value map[string]any) {
			delete(value["rows"].([]any)[0].(map[string]any), "batch_L0_shared_ms")
			delete(value["rows"].([]any)[0].(map[string]any), "single_L0_shared_ms")
		}},
		{name: "invalid timing", mutate: func(value map[string]any) {
			value["rows"].([]any)[0].(map[string]any)["single_raw_ms"] = math.NaN()
		}},
		{name: "invalid timestamp", mutate: func(value map[string]any) { value["date"] = "yesterday" }},
		{name: "credential-like model", mutate: func(value map[string]any) { value["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevLatencyFixture("hf", true)
			test.mutate(fixture)
			if _, err := normalizeAnyJevLatencyBenchmarkResult(fixture); err == nil {
				t.Fatal("invalid latency result was accepted")
			}
		})
	}
}

func TestAnyJevLatencyResultPathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_latency/2026-09-21/model.json",
		"bench/results_latency_vllm_cache_on/model.json",
		"bench/results_latency_vllm_cache_off/model.json",
	} {
		if !isAnyJevLatencyBenchmarkJSONPath(path) {
			t.Errorf("latency benchmark path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_latency_extra/model.json",
		"bench/results_exit/model.json",
		"bench/results_latency/model.items.json",
		"docs/results_latency.json",
	} {
		if isAnyJevLatencyBenchmarkJSONPath(path) {
			t.Errorf("path outside latency projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to a local AnyJev checkout to verify all
// supported latency-result files through inspection and snapshot readback.
func TestAnyJevLatencySourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the source-backed latency migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	for _, file := range manifest.Files {
		if isAnyJevLatencyBenchmarkJSONPath(file.Path) && file.Status == "ready" && file.ImportedCount == 1 {
			expected[file.Path] = struct{}{}
		}
	}
	if len(expected) == 0 {
		t.Fatal("source contains no importable latency-result JSON")
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
		if !isAnyJevLatencyBenchmarkJSONPath(record.SourcePath) {
			continue
		}
		var payload anyJevLatencyBenchmarkImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevLatencyBenchmarkResultSchema {
			t.Fatalf("latency result did not read back in the strict semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEvidenceEligible {
			t.Fatalf("latency result trust status changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("latency source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("latency result did not produce exactly one record (count=%d): %s", seen[path], path)
		}
	}
}
