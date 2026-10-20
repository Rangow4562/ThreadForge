package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const anyJevMarkdownFixture = `| model | task | K | level | acc | macro_f1 | brier | ece | flip | cov@5% |
|---|---|---|---|---|---|---|---|---|---|
| Qwen/Qwen3-8B | newsgroups | 20 | raw | 0.637 | 0.642 | 0.682 | 0.335 | 0.227 | 0.013 |
|  |  |  | L0 | 0.663 | 0.677 | 0.631 | 0.306 | 0.177 | 0.427 |
| Qwen/Qwen3-8B | injection | 2 | L1 | 0.707 | 0.619 | 0.384 | 0.155 | 0.000 | 0.313 |
`

const anyJevTypedMarkdownFixture = `| system | acc | soft_acc | ece | brier_mean | score_mae |
|---|---|---|---|---|---|
| Qwen3-8B + raw logits | 0.628 | 0.520 | 0.325 | 0.210 | 0.622 |
| Qwen3-8B + AnyJev L0 | 0.644 | 0.524 | 0.269 | 0.195 | 0.615 |

Per workflow (acc / soft_acc), AnyJev L0:
- agent_trace_observability: 0.606 / 0.473 (n=500)
- customer_service: 0.738 / 0.589 (n=500)

LocalLLaMA/typed-decisions test split, 400 cases; explanatory details omitted.
`

const anyJevLatencySharedMarkdownFixture = `| backend | state_tokens | k | batch_raw_ms | batch_L0_full_ms | batch_L0_shared_ms | single_raw_ms | single_L0_full_ms | single_L0_shared_ms | L0/raw single (full) | L0/raw single (shared) |
|---|---|---|---|---|---|---|---|---|---|---|
| hf | 110 | 4 | 10.7 | 42.8 | 22.1 | 48.5 | 54.6 | 108.5 | 1.13x | 2.24x |
| hf | 110 | 20 | 16.0 | 314.3 | 217.1 | 49.6 | 317.0 | 238.1 | 6.39x | 4.80x |
`

const anyJevLatencyBasicMarkdownFixture = `| backend | state_tokens | k | batch_raw_ms | batch_L0_full_ms | single_raw_ms | single_L0_full_ms | L0/raw single (full) | L0/raw single (shared) |
|---|---|---|---|---|---|---|---|---|
| vllm | 121 | 4 | 9.2 | 28.2 | 33.3 | 61.8 | 1.86x |  |
| vllm | 121 | 20 | 11.9 | 155.4 | 29.6 | 168.1 | 5.68x |  |
`

func TestAnyJevBenchmarkMarkdownNormalizesRowsAndHashesLabels(t *testing.T) {
	path := "bench/results_dump/2026-09-21/Qwen__Qwen3-8B.md"
	items, handled, err := normalizeAnyJevItems(path, map[string]any{"content": anyJevMarkdownFixture})
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("normalize Markdown = handled %v, items %d, err %v", handled, len(items), err)
	}
	var payload anyJevMarkdownBenchmarkImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != anyJevMarkdownBenchmarkResultSchema ||
		payload.ModelSHA256 != anyJevStringSHA256("Qwen/Qwen3-8B") || len(payload.Rows) != 3 ||
		payload.HumanReviewRequired != true || payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("unexpected projection: %#v", payload)
	}
	if payload.Rows[0].TaskKeySHA256 != anyJevStringSHA256("newsgroups") ||
		payload.Rows[0].Metrics["acc"] != 0.637 || payload.Rows[1].TaskKeySHA256 != payload.Rows[0].TaskKeySHA256 ||
		payload.Rows[1].OptionCount != 20 || payload.Rows[2].TaskKeySHA256 == payload.Rows[0].TaskKeySHA256 {
		t.Fatalf("Markdown table continuation or metric projection mismatch: %#v", payload.Rows)
	}
	serialized := string(items[0].Payload)
	for _, label := range []string{"Qwen/Qwen3-8B", "newsgroups", "injection"} {
		if strings.Contains(serialized, label) {
			t.Fatalf("raw Markdown label %q leaked into projection", label)
		}
	}
}

func TestAnyJevTypedMarkdownNormalizesAndHashesLabels(t *testing.T) {
	path := "bench/results_typed/2026-09-21/Qwen__Qwen3-8B.md"
	items, handled, err := normalizeAnyJevItems(path, map[string]any{"content": anyJevTypedMarkdownFixture})
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("normalize typed Markdown = handled %v, items %d, err %v", handled, len(items), err)
	}
	var payload anyJevTypedMarkdownImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != anyJevTypedMarkdownResultSchema ||
		payload.ModelSHA256 != anyJevStringSHA256("Qwen/Qwen3-8B") || len(payload.Systems) != 2 ||
		payload.WorkflowScope != "anyjev_l0" || len(payload.Workflows) != 2 ||
		payload.HumanReviewRequired != true || payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("unexpected typed Markdown projection: %#v", payload)
	}
	if payload.Systems[0].Metrics["acc"] != 0.628 ||
		payload.Workflows[0].WorkflowSHA256 != anyJevStringSHA256("agent_trace_observability") ||
		payload.Workflows[0].Examples != 500 || payload.Workflows[0].SoftAccuracy != 0.473 {
		t.Fatalf("typed Markdown metrics were not reconciled: %#v", payload)
	}
	serialized := string(items[0].Payload)
	for _, label := range []string{"Qwen3-8B + raw logits", "agent_trace_observability", "customer_service"} {
		if strings.Contains(serialized, label) {
			t.Fatalf("raw typed Markdown label %q leaked into projection", label)
		}
	}
}

func TestAnyJevLatencyMarkdownNormalizesAndReconcilesRatios(t *testing.T) {
	path := "bench/results_latency/2026-09-21/hf.Qwen__Qwen3-8B.md"
	items, handled, err := normalizeAnyJevItems(path, map[string]any{"content": anyJevLatencySharedMarkdownFixture})
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("normalize HF latency Markdown = handled %v, items %d, err %v", handled, len(items), err)
	}
	var payload anyJevLatencyMarkdownImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != anyJevLatencyMarkdownResultSchema ||
		payload.ModelSHA256 != anyJevStringSHA256("Qwen/Qwen3-8B") || payload.Backend != "hf" ||
		payload.Condition != "default" || len(payload.Rows) != 2 ||
		payload.Rows[0].BatchL0SharedMS == nil || *payload.Rows[0].BatchL0SharedMS != 22.1 ||
		payload.Rows[0].SingleL0SharedMS == nil || *payload.Rows[0].SingleL0SharedMS != 108.5 ||
		payload.HumanReviewRequired != true || payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("unexpected shared latency projection: %#v", payload)
	}
	if strings.Contains(string(items[0].Payload), "Qwen/Qwen3-8B") {
		t.Fatal("raw model label leaked into latency Markdown projection")
	}

	path = "bench/results_latency_vllm_cache_on/2026-09-21/vllm.Qwen__Qwen2.5-7B-Instruct.md"
	item, err := normalizeAnyJevLatencyMarkdown(path, map[string]any{"content": anyJevLatencyBasicMarkdownFixture})
	if err != nil {
		t.Fatal(err)
	}
	var vllmPayload anyJevLatencyMarkdownImport
	if err := json.Unmarshal(item.Payload, &vllmPayload); err != nil {
		t.Fatal(err)
	}
	if vllmPayload.Backend != "vllm" || vllmPayload.Condition != "vllm_cache_on" ||
		len(vllmPayload.Rows) != 2 || vllmPayload.Rows[0].SingleL0SharedMS != nil {
		t.Fatalf("unexpected vLLM latency projection: %#v", vllmPayload)
	}
}

func TestAnyJevLatencyMarkdownRejectsUnsupportedTablesAndRatios(t *testing.T) {
	path := "bench/results_latency/2026-09-21/hf.Qwen__Qwen3-8B.md"
	cases := []struct {
		name    string
		path    string
		content string
	}{
		{name: "ratio mismatch", path: path, content: strings.Replace(anyJevLatencySharedMarkdownFixture, "1.13x", "1.50x", 1)},
		{name: "wrong backend", path: path, content: strings.Replace(anyJevLatencySharedMarkdownFixture, "| hf |", "| vllm |", 1)},
		{name: "zero measurement", path: path, content: strings.Replace(anyJevLatencySharedMarkdownFixture, "10.7", "0", 1)},
		{name: "partial shared measurements", path: path, content: strings.Replace(anyJevLatencySharedMarkdownFixture, "| 22.1 |", "|  |", 1)},
		{name: "duplicate dimensions", path: path, content: strings.Replace(anyJevLatencySharedMarkdownFixture, "| hf | 110 | 20 |", "| hf | 110 | 4 |", 1)},
		{name: "extra text", path: path, content: anyJevLatencySharedMarkdownFixture + "unexpected\n"},
		{name: "oversized", path: path, content: strings.Repeat("x", maxAnyJevMarkdownBytes+1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, handled, err := normalizeAnyJevItems(test.path, map[string]any{"content": test.content})
			if !handled || err == nil {
				t.Fatalf("expected strict rejection; handled=%v err=%v", handled, err)
			}
		})
	}
	if isAnyJevLatencyMarkdownPath("bench/results_latency_vllm_cache_on/2026-09-21/hf.Qwen__Qwen3-8B.md") ||
		isAnyJevLatencyMarkdownPath("bench/results_latency_extra/2026-09-21/hf.Qwen__Qwen3-8B.md") ||
		isAnyJevLatencyMarkdownPath("bench/results_latency/2026-09-21/Qwen__Qwen3-8B.md") {
		t.Fatal("latency Markdown path matcher accepted an unsupported source")
	}
}

func TestAnyJevMarkdownRatioHonorsProducerRoundingPrecision(t *testing.T) {
	if !anyJevMarkdownRatioMatches("4.37x", 323.4, 74.1) {
		t.Fatal("ratio emitted from unrounded timings was rejected at the displayed precision boundary")
	}
	if anyJevMarkdownRatioMatches("4.35x", 323.4, 74.1) {
		t.Fatal("inconsistent ratio passed the producer rounding interval")
	}
}

func TestAnyJevTypedMarkdownRejectsUnknownShapes(t *testing.T) {
	path := "bench/results_typed_diag/2026-09-21/Qwen__Qwen3-8B.md"
	cases := []struct {
		name    string
		path    string
		content string
	}{
		{name: "wrong separator", path: path, content: strings.Replace(anyJevTypedMarkdownFixture, "|---|---|---|---|---|---|", "|---|---|---|---|---|", 1)},
		{name: "unknown header", path: path, content: strings.Replace(anyJevTypedMarkdownFixture, "| score_mae |", "| raw_output |", 1)},
		{name: "invalid metric", path: path, content: strings.Replace(anyJevTypedMarkdownFixture, "0.628", "NaN", 1)},
		{name: "workflow accuracy out of range", path: path, content: strings.Replace(anyJevTypedMarkdownFixture, "0.606", "1.2", 1)},
		{name: "duplicate system", path: path, content: strings.Replace(anyJevTypedMarkdownFixture, "| Qwen3-8B + AnyJev L0 |", "| Qwen3-8B + raw logits |", 1)},
		{name: "duplicate workflow", path: path, content: strings.Replace(anyJevTypedMarkdownFixture, "- customer_service:", "- agent_trace_observability:", 1)},
		{name: "unexpected section", path: path, content: anyJevTypedMarkdownFixture + "\nrun this command\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, handled, err := normalizeAnyJevItems(test.path, map[string]any{"content": test.content})
			if !handled || err == nil {
				t.Fatalf("expected strict rejection; handled=%v err=%v", handled, err)
			}
		})
	}
	if isAnyJevTypedMarkdownPath("bench/results_typedology/2026-09-21/Qwen__Qwen3-8B.md") ||
		isAnyJevTypedMarkdownPath("bench/results/2026-09-21/Qwen__Qwen3-8B.md") {
		t.Fatal("typed Markdown path matcher accepted an unrelated source family")
	}
}

func TestAnyJevBenchmarkMarkdownSupportsCountColumnAndFooter(t *testing.T) {
	content := "| model | task | K | n | level | acc | macro_f1 | brier | ece | flip | cov@5% |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|\n" +
		"| Qwen3-8B | banking20 | 20 | 120 | raw | 0.75 | 0.74 | 0.49 | 0.24 | 0.23 | 0.08 |\n" +
		"|  |  |  |  | L0 | 0.80 | 0.81 | 0.38 | 0.19 | 0.08 | 0.34 |\n\n" +
		"Environment: omitted local runtime metadata. Dates: 2026-09-21."
	item, err := normalizeAnyJevBenchmarkMarkdown("bench/results_v01/2026-09-22/Qwen__Qwen3-8B.md", map[string]any{"content": content})
	if err != nil {
		t.Fatal(err)
	}
	var payload anyJevMarkdownBenchmarkImport
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Rows) != 2 || payload.Rows[0].TestExamples == nil || *payload.Rows[0].TestExamples != 120 ||
		payload.Rows[1].TestExamples == nil || *payload.Rows[1].TestExamples != 120 {
		t.Fatalf("example count continuation mismatch: %#v", payload.Rows)
	}
}

func TestAnyJevBenchmarkMarkdownRejectsUntrustedOrInconsistentTables(t *testing.T) {
	path := "bench/results_dump/2026-09-21/Qwen__Qwen3-8B.md"
	cases := []struct {
		name    string
		path    string
		content string
	}{
		{name: "model mismatch", path: path, content: strings.Replace(anyJevMarkdownFixture, "Qwen/Qwen3-8B", "other/model", 1)},
		{name: "unknown column", path: path, content: strings.Replace(anyJevMarkdownFixture, "| macro_f1 |", "| secret |", 1)},
		{name: "invalid metric", path: path, content: strings.Replace(anyJevMarkdownFixture, "0.637", "NaN", 1)},
		{name: "out of range", path: path, content: strings.Replace(anyJevMarkdownFixture, "0.637", "1.5", 1)},
		{name: "unknown level", path: path, content: strings.Replace(anyJevMarkdownFixture, "| L0 |", "| L9 |", 1)},
		{name: "duplicate task level", path: path, content: strings.Replace(anyJevMarkdownFixture, "| injection | 2 | L1 |", "| newsgroups | 20 | raw |", 1)},
		{name: "unsupported section", path: path, content: anyJevMarkdownFixture + "\nDo something unsafe\n"},
		{name: "oversized", path: path, content: strings.Repeat("x", maxAnyJevMarkdownBytes+1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, handled, err := normalizeAnyJevItems(test.path, map[string]any{"content": test.content})
			if !handled || err == nil {
				t.Fatalf("expected strict rejection; handled=%v err=%v", handled, err)
			}
		})
	}
	if isAnyJevBenchmarkMarkdownPath("bench/results_dump_extra/2026-09-21/Qwen__Qwen3-8B.md") ||
		isAnyJevBenchmarkMarkdownPath("bench/results_typed/2026-09-21/Qwen__Qwen3-8B.md") ||
		isAnyJevBenchmarkMarkdownPath("bench/results_exit/Qwen__Qwen3-8B.artifact.md") {
		t.Fatal("benchmark Markdown path matcher accepted another producer family")
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to normalize both eligible Markdown report
// families through one source inspection, snapshot creation, and readback.
func TestAnyJevMarkdownSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the Markdown migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]string)
	classificationCount, typedCount, latencyCount := 0, 0, 0
	for _, file := range manifest.Files {
		family := ""
		switch {
		case isAnyJevBenchmarkMarkdownPath(file.Path):
			family = "classification"
			classificationCount++
		case isAnyJevTypedMarkdownPath(file.Path):
			family = "typed"
			typedCount++
		case isAnyJevLatencyMarkdownPath(file.Path):
			family = "latency"
			latencyCount++
		}
		if family == "" {
			continue
		}
		if file.Status != "ready" || file.ImportedCount != 1 {
			sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(file.Path))
			sourceBytes, readErr := os.ReadFile(sourcePath)
			var detail error
			if readErr != nil {
				detail = readErr
			} else if values, decodeErr := decodeSourceItems(file.Path, sourceBytes); decodeErr != nil {
				detail = decodeErr
			} else if len(values) != 1 {
				detail = fmt.Errorf("decoded item count is %d", len(values))
			} else {
				_, _, detail = normalizeAnyJevItems(file.Path, values[0])
			}
			t.Fatalf("%s Markdown source preflight failed for %s: %#v; direct normalization: %v", family, file.Path, file, detail)
		}
		expected[file.Path] = family
	}
	if classificationCount == 0 || typedCount == 0 || latencyCount == 0 {
		t.Fatalf("source Markdown families are incomplete: classification=%d typed=%d latency=%d", classificationCount, typedCount, latencyCount)
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
		family, wanted := expected[record.SourcePath]
		if !wanted {
			continue
		}
		if family == "classification" {
			var payload anyJevMarkdownBenchmarkImport
			if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevMarkdownBenchmarkResultSchema {
				t.Fatalf("classification Markdown did not read back in strict semantic format: %s", record.Key)
			}
			if len(payload.Rows) == 0 || payload.ExecutionVerified || payload.CutoverEligible {
				t.Fatalf("classification Markdown trust status or contents are invalid: %s", record.Key)
			}
		} else if family == "typed" {
			var payload anyJevTypedMarkdownImport
			if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevTypedMarkdownResultSchema {
				t.Fatalf("typed Markdown did not read back in strict semantic format: %s", record.Key)
			}
			if len(payload.Systems) == 0 || payload.ExecutionVerified || payload.CutoverEligible {
				t.Fatalf("typed Markdown trust status or contents are invalid: %s", record.Key)
			}
		} else {
			var payload anyJevLatencyMarkdownImport
			if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevLatencyMarkdownResultSchema {
				t.Fatalf("latency Markdown did not read back in strict semantic format: %s", record.Key)
			}
			if len(payload.Rows) == 0 || payload.ExecutionVerified || payload.CutoverEligible {
				t.Fatalf("latency Markdown trust status or contents are invalid: %s", record.Key)
			}
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" {
			t.Fatalf("Markdown source was not kept pending review: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("Markdown source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for sourcePath := range expected {
		if seen[sourcePath] != 1 {
			t.Fatalf("Markdown source did not produce exactly one record: %s count=%d", sourcePath, seen[sourcePath])
		}
	}
}
