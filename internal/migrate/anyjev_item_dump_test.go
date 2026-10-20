package migrate

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevItemDumpBaseUnit() map[string]any {
	return map[string]any{
		"kind": "choice", "k": float64(2),
		"perms": []any{[]any{float64(0), float64(1)}, []any{float64(1), float64(0)}},
		"gold":  []any{float64(0), float64(1)},
		"p_pos_raw": []any{
			[]any{[]any{0.7, 0.3}, []any{0.2, 0.8}},
			[]any{[]any{0.6, 0.4}, []any{0.8, 0.2}},
		},
		"cf_prior": nil,
	}
}

func anyJevItemDumpTaskFixture() map[string]any {
	return map[string]any{
		"model": "private/model",
		"tasks": map[string]any{"private.task": anyJevItemDumpBaseUnit()},
	}
}

func anyJevTypedItemDumpFixture() map[string]any {
	unit := anyJevItemDumpBaseUnit()
	unit["name"] = "Private display name"
	unit["workflow"] = "private.workflow"
	unit["gold_probs"] = []any{[]any{1.0, 0.0}, []any{0.0, 1.0}}
	return map[string]any{
		"model":     "private/model",
		"questions": map[string]any{"question-id-1": unit},
	}
}

func TestAnyJevItemDumpProjectsMetricsWithoutRetainingPerItemData(t *testing.T) {
	path := "bench/results_dump/2026-09-30/private__model.items.json.gz"
	items, handled, err := normalizeAnyJevItems(path, anyJevItemDumpTaskFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("task dump import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevItemDumpImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if items[0].Type != "decision_label_or_evaluation" || payload.SchemaVersion != anyJevItemDumpSchema ||
		payload.DatasetKind != "task_dump" || payload.ModelSHA256 != anyJevStringSHA256("private/model") ||
		len(payload.Units) != 1 || payload.Units[0].UnitKeySHA256 != anyJevStringSHA256("private.task") ||
		payload.Units[0].Examples != 2 || payload.Units[0].Options != 2 ||
		payload.Units[0].RawFirstLayoutAccuracy != 0.5 || payload.Units[0].PermutationMeanAccuracy != 1 ||
		payload.Units[0].PermutationLogMeanAccuracy != 1 || payload.Units[0].PermutationFlipRate != 0.5 ||
		payload.Units[0].ContentFreePriorAvailable || payload.Units[0].PermutationLayoutSHA256 == "" ||
		!payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("task dump projection is incorrect: %#v", payload)
	}
	encoded := string(items[0].Payload)
	for _, privateValue := range []string{"private/model", "private.task", "p_pos_raw", "[0.7,0.3]"} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("item-dump projection retained %q", privateValue)
		}
	}
}

func TestAnyJevTypedItemDumpHashesWorkflowAndRetainsOnlySoftMetric(t *testing.T) {
	items, handled, err := normalizeAnyJevItems(
		"bench/results_typed_dump/2026-09-30/private__model.items.json", anyJevTypedItemDumpFixture(),
	)
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("typed item dump import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevItemDumpImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	unit := payload.Units[0]
	if payload.DatasetKind != "typed_question_dump" || unit.WorkflowSHA256 != anyJevStringSHA256("private.workflow") ||
		unit.SoftLabelLogMeanAccuracy == nil || *unit.SoftLabelLogMeanAccuracy <= 0.5 ||
		strings.Contains(string(items[0].Payload), "Private display name") || strings.Contains(string(items[0].Payload), "private.workflow") {
		t.Fatalf("typed dump projection is incorrect or retained source labels: %#v", payload)
	}
}

func TestAnyJevItemDumpRejectsMalformedProducerInputs(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", path: "bench/results_dump/2026-09-30/private__model.items.json", mutate: func(v map[string]any) { v["env"] = map[string]any{} }},
		{name: "model filename mismatch", path: "bench/results_dump/2026-09-30/other.items.json", mutate: func(map[string]any) {}},
		{name: "unknown group field", path: "bench/results_dump/2026-09-30/private__model.items.json", mutate: func(v map[string]any) {
			v["tasks"].(map[string]any)["private.task"].(map[string]any)["prompt"] = "private"
		}},
		{name: "wrong canonical permutation", path: "bench/results_dump/2026-09-30/private__model.items.json", mutate: func(v map[string]any) {
			unit := v["tasks"].(map[string]any)["private.task"].(map[string]any)
			unit["perms"] = []any{[]any{float64(1), float64(0)}, []any{float64(0), float64(1)}}
		}},
		{name: "duplicate option permutation", path: "bench/results_dump/2026-09-30/private__model.items.json", mutate: func(v map[string]any) {
			unit := v["tasks"].(map[string]any)["private.task"].(map[string]any)
			unit["perms"] = []any{[]any{float64(0), float64(1)}, []any{float64(0), float64(1)}}
		}},
		{name: "non-normalized probabilities", path: "bench/results_dump/2026-09-30/private__model.items.json", mutate: func(v map[string]any) {
			unit := v["tasks"].(map[string]any)["private.task"].(map[string]any)
			unit["p_pos_raw"].([]any)[0].([]any)[0] = []any{0.9, 0.9}
		}},
		{name: "gold index out of range", path: "bench/results_dump/2026-09-30/private__model.items.json", mutate: func(v map[string]any) {
			unit := v["tasks"].(map[string]any)["private.task"].(map[string]any)
			unit["gold"].([]any)[0] = float64(2)
		}},
		{name: "typed soft-label count mismatch", path: "bench/results_typed_dump/2026-09-30/private__model.items.json", mutate: func(v map[string]any) {
			unit := v["questions"].(map[string]any)["question-id-1"].(map[string]any)
			unit["gold_probs"] = []any{[]any{1.0, 0.0}}
		}},
		{name: "credential-like model", path: "bench/results_dump/2026-09-30/api_key=secret.items.json", mutate: func(v map[string]any) { v["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevItemDumpTaskFixture()
			if strings.Contains(test.path, "results_typed_dump") {
				fixture = anyJevTypedItemDumpFixture()
			}
			test.mutate(fixture)
			if _, _, err := normalizeAnyJevItems(test.path, fixture); err == nil {
				t.Fatal("malformed AnyJev item dump was accepted")
			}
		})
	}
}

func TestAnyJevItemDumpPathsAreNarrowAndGzipExpansionIsBounded(t *testing.T) {
	for _, path := range []string{
		"bench/results_dump/2026-09-21/model.items.json",
		"bench/results_dump/2026-09-21/model.items.json.gz",
		"bench/results_typed_dump/2026-09-21/model.items.json.gz",
	} {
		if !isAnyJevItemDumpJSONPath(path) {
			t.Errorf("supported item-dump path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results/2026-09-21/model.items.json.gz",
		"bench/results_dump/invalid-date/model.items.json.gz",
		"bench/results_typed_dump/2026-09-21/nested/model.items.json.gz",
		"bench/results_dump/2026-09-21/model.json.gz",
	} {
		if isAnyJevItemDumpJSONPath(path) {
			t.Errorf("unsupported item-dump path recognized: %s", path)
		}
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(bytes.Repeat([]byte("x"), 128)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := "bench/results_dump/2026-09-21/model.items.json.gz"
	if _, _, err := decodeAnyJevItemDumpGzipJSON(path, compressed.Bytes(), 32); err == nil {
		t.Fatal("compressed item dump exceeded the expanded budget without rejection")
	}
	if _, _, err := decodeAnyJevItemDumpGzipJSON(path, []byte("not gzip"), 1024); err == nil {
		t.Fatal("invalid gzip item dump was accepted")
	}
}

type anyJevItemDumpReferenceMetric struct {
	examples int
	accuracy float64
}

func anyJevItemDumpReferenceMetrics(t *testing.T, sourceRoot, itemPath string) map[string]anyJevItemDumpReferenceMetric {
	t.Helper()
	summaryRelative := strings.TrimSuffix(itemPath, ".items.json.gz")
	if summaryRelative == itemPath {
		summaryRelative = strings.TrimSuffix(itemPath, ".items.json")
	}
	summaryPath := filepath.Join(sourceRoot, filepath.FromSlash(summaryRelative+".json"))
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read sibling source result %s: %v", summaryRelative+".json", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("decode sibling source result %s: %v", summaryRelative+".json", err)
	}
	metrics := make(map[string]anyJevItemDumpReferenceMetric)
	if tasks, ok := root["tasks"].([]any); ok {
		for _, rawTask := range tasks {
			task, ok := rawTask.(map[string]any)
			if !ok {
				t.Fatalf("invalid task row in sibling result %s", summaryRelative+".json")
			}
			name, nameOK := task["task"].(string)
			levels, levelsOK := task["levels"].(map[string]any)
			raw, rawOK := levels["raw"].(map[string]any)
			accuracy, accOK := boundedAnyJevNumber(raw["acc"], 0, 1)
			examples, nOK := boundedAnyJevInteger(raw["n"], 1, maxAnyJevDumpItemsPerUnit)
			if !nameOK || !levelsOK || !rawOK || !accOK || !nOK {
				t.Fatalf("invalid raw task metrics in sibling result %s", summaryRelative+".json")
			}
			metrics[anyJevStringSHA256(name)] = anyJevItemDumpReferenceMetric{examples: examples, accuracy: accuracy}
		}
		return metrics
	}
	levels, levelsOK := root["levels"].(map[string]any)
	raw, rawOK := levels["raw"].(map[string]any)
	byQuestion, questionsOK := raw["by_question"].(map[string]any)
	if !levelsOK || !rawOK || !questionsOK {
		t.Fatalf("sibling typed result has no raw per-question metrics: %s", summaryRelative+".json")
	}
	for key, rawValue := range byQuestion {
		row, ok := rawValue.(map[string]any)
		if !ok {
			t.Fatalf("invalid per-question result row in %s", summaryRelative+".json")
		}
		accuracy, accOK := boundedAnyJevNumber(row["acc"], 0, 1)
		examples, nOK := boundedAnyJevInteger(row["n"], 1, maxAnyJevDumpItemsPerUnit)
		if !accOK || !nOK {
			t.Fatalf("invalid raw per-question metrics in %s", summaryRelative+".json")
		}
		metrics[anyJevStringSHA256(key)] = anyJevItemDumpReferenceMetric{examples: examples, accuracy: accuracy}
	}
	return metrics
}

func TestAnyJevItemDumpSourceMetricsReconcile(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to reconcile item dumps with sibling AnyJev summaries")
	}
	paths := make([]string, 0, 20)
	for _, family := range []string{"results_dump", "results_typed_dump"} {
		matches, err := filepath.Glob(filepath.Join(sourceRoot, "bench", family, "*", "*.items.json.gz"))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) != 20 {
		t.Fatalf("expected 20 local compressed item dumps, found %d", len(paths))
	}
	for _, path := range paths {
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			t.Fatal(err)
		}
		relative = filepath.ToSlash(relative)
		compressed, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		decoded, _, err := decodeAnyJevItemDumpGzipJSON(relative, compressed, maxTotalBytes)
		if err != nil {
			t.Fatalf("decompress %s: %v", relative, err)
		}
		var source any
		if err := json.Unmarshal(decoded, &source); err != nil {
			t.Fatalf("decode %s: %v", relative, err)
		}
		item, err := normalizeAnyJevItemDump(relative, source)
		if err != nil {
			t.Fatalf("normalize %s: %v", relative, err)
		}
		var payload anyJevItemDumpImport
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode normalized %s: %v", relative, err)
		}
		reference := anyJevItemDumpReferenceMetrics(t, sourceRoot, relative)
		if len(reference) != len(payload.Units) {
			t.Fatalf("dump/summary unit count mismatch for %s: dump=%d summary=%d", relative, len(payload.Units), len(reference))
		}
		for _, unit := range payload.Units {
			metric, exists := reference[unit.UnitKeySHA256]
			if !exists || unit.Examples != metric.examples || math.Abs(unit.RawFirstLayoutAccuracy-metric.accuracy) > 1/float64(metric.examples)+1e-8 {
				t.Fatalf("projected raw metric does not reconcile to sibling source summary: %s unit=%s got_n=%d ref_n=%d got_acc=%0.8f ref_acc=%0.8f",
					relative, unit.UnitKeySHA256, unit.Examples, metric.examples, unit.RawFirstLayoutAccuracy, metric.accuracy)
			}
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to verify every local compressed item dump
// through inspection, bounded expansion, snapshot, and strict readback.
func TestAnyJevItemDumpSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the AnyJev item-dump migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]int)
	for _, file := range manifest.Files {
		if !isAnyJevItemDumpJSONPath(file.Path) {
			continue
		}
		if file.Status != "ready" || file.ImportedCount <= 0 || file.ImportedCount != file.RecordCount {
			t.Fatalf("source item dump was not completely normalized: %s status=%s imported=%d found=%d issue=%s",
				file.Path, file.Status, file.ImportedCount, file.RecordCount, file.Issue)
		}
		expected[file.Path] = file.ImportedCount
	}
	if len(expected) != 20 {
		t.Fatalf("expected 20 local compressed item dumps, found %d", len(expected))
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
		_, ok := expected[record.SourcePath]
		if !ok {
			continue
		}
		var payload anyJevItemDumpImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevItemDumpSchema {
			t.Fatalf("item dump did not read back in its semantic contract: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible || !payload.HumanReviewRequired || len(payload.Units) == 0 {
			t.Fatalf("item dump review status or unit count changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("item-dump source/readback file count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path, count := range expected {
		if seen[path] != count {
			t.Fatalf("item-dump unit readback mismatch for %s: expected=%d actual=%d", path, count, seen[path])
		}
	}
}
