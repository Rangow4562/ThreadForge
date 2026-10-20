package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func anyJevParaphraseRowFixture(accuracy float64) map[string]any {
	return map[string]any{
		"acc": accuracy, "ece": 0.08, "nll": 0.42, "n": float64(200),
		"acc_ci95":          []any{accuracy - 0.04, accuracy + 0.04},
		"diff_vs_orig_ci95": []any{-0.03, 0.02}, "flip_vs_orig": 0.12,
	}
}

func anyJevParaphraseFixture() map[string]any {
	rows := make(map[string]any)
	questionAcc := make(map[string]any)
	for _, variant := range anyJevParaphraseOrderVariants {
		prefixes := []string{"raw", "L0", "orig-head"}
		questionAcc["L0@"+variant] = 0.7
		questionAcc["orig-head@"+variant] = 0.74
		if variant == "orig" {
			prefixes = append(prefixes, "tt-head")
			questionAcc["tt-head@orig"] = 0.72
		} else {
			prefixes = append(prefixes, "own-head", "mix-head", "all-head", "ta-head", "tt-head")
			for _, prefix := range []string{"own-head", "mix-head", "all-head", "ta-head", "tt-head"} {
				questionAcc[prefix+"@"+variant] = 0.71
			}
		}
		for _, prefix := range prefixes {
			rows[prefix+"@"+variant] = anyJevParaphraseRowFixture(0.73)
		}
	}
	for _, budget := range []int{10, 30} {
		for _, variant := range anyJevParaphraseOrderVariants[1:] {
			for _, prefix := range []string{"ta", "ta-mean"} {
				row := anyJevParaphraseRowFixture(0.72)
				row["n"] = float64(600)
				rows[prefix+"@"+strconv.Itoa(budget)+"-head@"+variant] = row
			}
		}
	}
	questions := map[string]any{}
	for _, key := range []string{"private.question.one", "private.question.two"} {
		questions[key] = map[string]any{"K": float64(4), "kind": "choice", "acc": questionAcc}
	}
	return map[string]any{
		"model": "private/model-label", "block": float64(24),
		"variants": []any{"orig", "w1", "w2", "w3", "o1"}, "rows": rows,
		"questions": questions, "date": "2026-09-28T10:11:12.123456",
		"env": map[string]any{"gpu": "private-device", "path": "C:/private/model"},
	}
}

func anyJevOrderFixture() map[string]any {
	rows := map[string]any{}
	for _, key := range []string{
		"fit-id@canonical", "fit-id@reversed", "fit-id@reversed+tt",
		"fit-rand@canonical", "fit-rand@reversed", "fit-rand@reversed+tt",
	} {
		flip := 0.13
		if key == "fit-id@canonical" {
			flip = 0
		}
		rows[key] = map[string]any{
			"acc": 0.75, "ece": 0.06, "nll": 0.43, "n": float64(200), "flip_vs_canonical": flip,
		}
	}
	return map[string]any{
		"model": "private/model-label", "block": float64(24), "rows": rows,
		"date": "2026-09-28T10:11:12.123456", "env": map[string]any{"gpu": "private-device"},
	}
}

func TestAnyJevParaphraseResultNormalizesAndRedacts(t *testing.T) {
	items, handled, err := normalizeAnyJevItems(
		"bench/results_paraphrase/2026-09-28/private__model-label.paraphrase.b24.json",
		anyJevParaphraseFixture(),
	)
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("paraphrase import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevParaphraseOrderImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if items[0].Type != "decision_label_or_evaluation" || payload.SchemaVersion != anyJevParaphraseOrderResultSchema ||
		payload.StudyKind != "paraphrase" || payload.ModelSHA256 != anyJevStringSHA256("private/model-label") ||
		payload.Block != 24 || len(payload.Variants) != 5 || len(payload.AdaptationBudgets) != 2 ||
		len(payload.Rows) != 52 || len(payload.Questions) != 2 || !payload.HumanReviewRequired ||
		payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("paraphrase projection is incorrect: %#v", payload)
	}
	encoded := string(items[0].Payload)
	for _, privateValue := range []string{
		"private/model-label", "private.question.one", "2026-09-28", "private-device", "C:/private/model",
	} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("paraphrase projection retained %q", privateValue)
		}
	}
}

func TestAnyJevListingOrderResultNormalizesAndRedacts(t *testing.T) {
	items, handled, err := normalizeAnyJevItems(
		"bench/results_paraphrase/2026-09-28/private__model-label.order.json", anyJevOrderFixture(),
	)
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("listing-order import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevParaphraseOrderImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.StudyKind != "listing_order" || payload.Block != 24 || len(payload.Rows) != 6 ||
		len(payload.Questions) != 0 || len(payload.Variants) != 0 || len(payload.AdaptationBudgets) != 0 ||
		payload.Rows[0].FlipVsCanonical == nil || !payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("listing-order projection is incorrect: %#v", payload)
	}
	encoded := string(items[0].Payload)
	for _, privateValue := range []string{"private/model-label", "2026-09-28", "private-device"} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("listing-order projection retained %q", privateValue)
		}
	}
}

func TestAnyJevParaphraseOrderRejectsUnsupportedOrInconsistentResults(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", path: "bench/results_paraphrase/2026-09-28/private__model-label.paraphrase.b24.json", mutate: func(v map[string]any) { v["extra"] = true }},
		{name: "path block mismatch", path: "bench/results_paraphrase/2026-09-28/private__model-label.paraphrase.b36.json", mutate: func(map[string]any) {}},
		{name: "variant order", path: "bench/results_paraphrase/2026-09-28/private__model-label.paraphrase.b24.json", mutate: func(v map[string]any) { v["variants"] = []any{"orig", "w2", "w1", "w3", "o1"} }},
		{name: "incomplete paired adaptation", path: "bench/results_paraphrase/2026-09-28/private__model-label.paraphrase.b24.json", mutate: func(v map[string]any) { delete(v["rows"].(map[string]any), "ta-mean@10-head@w1") }},
		{name: "malformed adaptation label", path: "bench/results_paraphrase/2026-09-28/private__model-label.paraphrase.b24.json", mutate: func(v map[string]any) {
			rows := v["rows"].(map[string]any)
			rows["ta@10@w1"] = rows["ta@10-head@w1"]
			delete(rows, "ta@10-head@w1")
		}},
		{name: "reversed confidence interval", path: "bench/results_paraphrase/2026-09-28/private__model-label.paraphrase.b24.json", mutate: func(v map[string]any) {
			v["rows"].(map[string]any)["raw@orig"].(map[string]any)["acc_ci95"] = []any{0.9, 0.1}
		}},
		{name: "missing question metric", path: "bench/results_paraphrase/2026-09-28/private__model-label.paraphrase.b24.json", mutate: func(v map[string]any) {
			delete(v["questions"].(map[string]any)["private.question.one"].(map[string]any)["acc"].(map[string]any), "orig-head@orig")
		}},
		{name: "credential-like model", path: "bench/results_paraphrase/2026-09-28/api_key=secret.paraphrase.b24.json", mutate: func(v map[string]any) { v["model"] = "api_key=secret" }},
		{name: "unknown order row", path: "bench/results_paraphrase/2026-09-28/private__model-label.order.json", mutate: func(v map[string]any) { v["rows"].(map[string]any)["fit-secret@canonical"] = map[string]any{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var fixture map[string]any
			if strings.HasSuffix(test.path, ".order.json") {
				fixture = anyJevOrderFixture()
			} else {
				fixture = anyJevParaphraseFixture()
			}
			test.mutate(fixture)
			if _, _, err := normalizeAnyJevItems(test.path, fixture); err == nil {
				t.Fatal("invalid AnyJev paraphrase/order result was accepted")
			}
		})
	}
}

func TestAnyJevParaphraseOrderPathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_paraphrase/2026-09-22/model.paraphrase.b24.json",
		"bench/results_paraphrase/2026-09-22/model.paraphrase.b36.json",
		"bench/results_paraphrase/2026-09-22/model.order.json",
		"BENCH/RESULTS_PARAPHRASE/2026-09-22/model.ORDER.JSON",
	} {
		if !isAnyJevParaphraseOrderJSONPath(path) {
			t.Errorf("supported path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_paraphrase_extra/2026-09-22/model.order.json",
		"bench/results_paraphrase/2026-09-22/nested/model.order.json",
		"bench/results_paraphrase/not-a-date/model.order.json",
		"other/results_paraphrase/2026-09-22/model.paraphrase.b24.json",
		"bench/results_paraphrase/2026-09-22/model.paraphrase.b0.json",
	} {
		if isAnyJevParaphraseOrderJSONPath(path) {
			t.Errorf("unsupported path was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to verify all local paraphrase and order
// results through inspect, snapshot creation, and semantic readback.
func TestAnyJevParaphraseOrderSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the paraphrase/order migration check")
	}
	paths, err := filepath.Glob(filepath.Join(sourceRoot, "bench", "results_paraphrase", "*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var directlyNormalized int
	for _, sourcePath := range paths {
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil || !isAnyJevParaphraseOrderJSONPath(filepath.ToSlash(relative)) {
			continue
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		var source any
		if err := json.Unmarshal(data, &source); err != nil {
			t.Fatal(err)
		}
		if _, err := normalizeAnyJevParaphraseOrderResult(filepath.ToSlash(relative), source); err != nil {
			t.Fatalf("source result failed semantic normalization: %s: %v", relative, err)
		}
		directlyNormalized++
	}
	if directlyNormalized != 6 {
		t.Fatalf("expected six direct source normalizations, got %d", directlyNormalized)
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	for _, file := range manifest.Files {
		if !isAnyJevParaphraseOrderJSONPath(file.Path) {
			continue
		}
		if file.Status != "ready" || file.ImportedCount != 1 {
			t.Fatalf("paraphrase/order file did not produce one semantic record: %s (%s: %s)", file.Path, file.Status, file.Issue)
		}
		expected[file.Path] = struct{}{}
	}
	if len(expected) != 6 {
		t.Fatalf("expected six local paraphrase/order results, got %d", len(expected))
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
		if _, ok := expected[record.SourcePath]; !ok {
			continue
		}
		var payload anyJevParaphraseOrderImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevParaphraseOrderResultSchema {
			t.Fatalf("result did not read back in semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible || !payload.HumanReviewRequired {
			t.Fatalf("historical result trust state changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("source/readback file count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for sourcePath := range expected {
		if seen[sourcePath] != 1 {
			t.Fatalf("source file produced %d records: %s", seen[sourcePath], sourcePath)
		}
	}
}
