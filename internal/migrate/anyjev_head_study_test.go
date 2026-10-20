package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevHeadStudyFixture() map[string]any {
	kinds := []string{"diff_means", "lda", "ridge", "rrr"}
	methods := anyJevHeadStudyMethods(kinds)
	rows := make(map[string]any, len(methods))
	pooled := make(map[string]any, len(methods))
	for _, method := range methods {
		rows[method] = map[string]any{"acc": 0.5, "ece": 0.1, "brier": 0.4, "nll": 0.8, "flip": 0.2}
		pooled[method] = map[string]any{"n": float64(4), "acc": 0.5, "ece": 0.12, "brier": 0.4, "nll": 0.8, "flip": 0.2}
	}
	headMethods := anyJevHeadStudyHeadMethods(kinds)
	heads := make(map[string]any, len(headMethods))
	for _, method := range headMethods {
		base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(method, "head:"), "@self"), "/avg")
		parameters := map[string]any{}
		switch base {
		case "lda":
			parameters["shrinkage"] = 0.5
		case "ridge":
			parameters["lam"] = float64(1)
		case "rrr":
			parameters["lam"], parameters["rank"] = float64(1), float64(2)
		}
		heads[method] = map[string]any{
			"layer": float64(10), "params": parameters, "temperature": 1.0,
			"n_labels": float64(2), "oof_nll": 0.8, "oof_acc": 0.5,
		}
	}
	question := func() map[string]any {
		questionRows := make(map[string]any, len(rows))
		for key, value := range rows {
			questionRows[key] = value
		}
		questionHeads := make(map[string]any, len(heads))
		for key, value := range heads {
			questionHeads[key] = value
		}
		return map[string]any{
			"kind": "choice", "k": float64(2), "n_test": float64(2), "n_calib": float64(2),
			"rows": questionRows, "heads": questionHeads,
		}
	}
	return map[string]any{
		"model": "private-model-label", "layers": []any{float64(-1), float64(-5)},
		"kinds":  []any{"diff_means", "lda", "ridge", "rrr"},
		"labels": []any{"gold", "self"}, "seed": float64(7),
		"env":  map[string]any{"date": "2026-09-22T12:03:38.722660", "git_commit": "abcdef0123456789"},
		"date": "2026-09-22T12:03:38.722660",
		"typed": map[string]any{
			"questions": map[string]any{"private-question-one": question(), "private-question-two": question()},
			"pooled":    pooled,
		},
	}
}

func TestAnyJevHeadStudyResultProjectsHistoricalMetricsAndSelections(t *testing.T) {
	items, handled, err := normalizeAnyJevItems("bench/results_heads/2026-09-22/model.typed.json", anyJevHeadStudyFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("head-study result import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	item := items[0]
	if item.Type != "decision_label_or_evaluation" {
		t.Fatalf("unexpected head-study item type %q", item.Type)
	}
	var payload anyJevHeadStudyImport
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != anyJevHeadStudyResultSchema || payload.SourceKind != "per_question_head_study" ||
		payload.ModelSHA256 != anyJevStringSHA256("private-model-label") || payload.Seed != 7 ||
		payload.TestExamples != 4 || payload.CalibrationExamples != 4 || len(payload.Pooled) != 22 || len(payload.Questions) != 2 ||
		len(payload.Questions[0].Rows) != 22 || len(payload.Questions[0].Heads) != 16 || !payload.HumanReviewRequired ||
		payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("head-study result projection is incomplete or authorizing: %#v", payload)
	}
	parameterKinds := make(map[string]anyJevHeadStudyParameters)
	for _, head := range payload.Questions[0].Heads {
		parameterKinds[head.Method] = head.Parameters
	}
	if parameterKinds["head:lda"].Shrinkage == nil || *parameterKinds["head:lda"].Shrinkage != 0.5 ||
		parameterKinds["head:ridge"].Lambda == nil || *parameterKinds["head:ridge"].Lambda != 1 ||
		parameterKinds["head:rrr"].Rank == nil || *parameterKinds["head:rrr"].Rank != 2 {
		t.Fatalf("solver-specific head parameters were not preserved: %#v", parameterKinds)
	}
	serialized := string(item.Payload)
	for _, privateValue := range []string{"private-model-label", "private-question-one", "private-question-two", "2026-09-22", "abcdef0123456789"} {
		if strings.Contains(serialized, privateValue) {
			t.Fatalf("head-study result projection retained %q", privateValue)
		}
	}
}

func TestAnyJevHeadStudyRejectsUnreconciledAndUnsupportedShapes(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		mutate func(map[string]any)
	}{
		{name: "date path mismatch", path: "bench/results_heads/2026-09-23/model.typed.json"},
		{name: "unknown root field", mutate: func(v map[string]any) { v["raw_items"] = []any{} }},
		{name: "pooled count mismatch", mutate: func(v map[string]any) {
			v["typed"].(map[string]any)["pooled"].(map[string]any)["raw"].(map[string]any)["n"] = float64(3)
		}},
		{name: "pooled metric mismatch", mutate: func(v map[string]any) {
			v["typed"].(map[string]any)["pooled"].(map[string]any)["raw"].(map[string]any)["acc"] = 0.9
		}},
		{name: "unknown per-question method", mutate: func(v map[string]any) {
			q := v["typed"].(map[string]any)["questions"].(map[string]any)["private-question-one"].(map[string]any)
			q["rows"].(map[string]any)["unsupported"] = q["rows"].(map[string]any)["raw"]
		}},
		{name: "credential-like question key", mutate: func(v map[string]any) {
			q := v["typed"].(map[string]any)["questions"].(map[string]any)
			q["password=not-a-real-secret"] = q["private-question-one"]
			delete(q, "private-question-one")
		}},
		{name: "unsupported environment field", mutate: func(v map[string]any) {
			v["env"].(map[string]any)["private_path"] = "C:\\private"
		}},
		{name: "solver parameters do not match method", mutate: func(v map[string]any) {
			q := v["typed"].(map[string]any)["questions"].(map[string]any)["private-question-one"].(map[string]any)
			q["heads"].(map[string]any)["head:lda"].(map[string]any)["params"] = map[string]any{"lam": float64(1)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevHeadStudyFixture()
			if test.mutate != nil {
				test.mutate(fixture)
			}
			path := test.path
			if path == "" {
				path = "bench/results_heads/2026-09-22/model.typed.json"
			}
			if _, handled, err := normalizeAnyJevItems(path, fixture); !handled || err == nil {
				t.Fatalf("invalid head-study result was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
}

func TestAnyJevHeadStudyPathIsNarrowlyAllowlisted(t *testing.T) {
	for _, path := range []string{
		"bench/results_heads/2026-09-22/model.typed.json",
		"BENCH/RESULTS_HEADS/2026-09-22/model.TYPED.JSON",
	} {
		if !isAnyJevHeadStudyJSONPath(path) {
			t.Errorf("head-study JSON path was not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_heads/model.typed.json",
		"bench/results_heads/not-a-date/model.typed.json",
		"bench/results_heads/features/model.typed.json",
		"bench/results_heads/2026-09-22/model.json",
		"bench/results_heads/2026-09-22/model.typed.items.json",
		"other/results_heads/2026-09-22/model.typed.json",
	} {
		if isAnyJevHeadStudyJSONPath(path) {
			t.Errorf("path outside the per-question head-study format was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to the local AnyJev checkout to verify the
// retained head-study result through inspect, snapshot, and record readback.
func TestAnyJevHeadStudySourceFileNormalizesSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the source-backed head-study migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]bool)
	for _, file := range manifest.Files {
		if isAnyJevHeadStudyJSONPath(file.Path) {
			if file.Status != "ready" || file.ImportedCount != 1 {
				sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(file.Path))
				data, readErr := os.ReadFile(sourcePath)
				if readErr != nil {
					t.Fatalf("source head-study result was not semantically ready: status=%s imported=%d issue=%s read=%v", file.Status, file.ImportedCount, file.Issue, readErr)
				}
				var value any
				if decodeErr := json.Unmarshal(data, &value); decodeErr != nil {
					t.Fatalf("source head-study JSON could not be decoded: %v", decodeErr)
				}
				_, normalizeErr := normalizeAnyJevHeadStudyResult(file.Path, value)
				t.Fatalf("source head-study result was not semantically ready: status=%s imported=%d issue=%s normalize=%v", file.Status, file.ImportedCount, file.Issue, normalizeErr)
			}
			expected[file.Path] = true
		}
	}
	if len(expected) == 0 {
		t.Fatal("source contains no supported AnyJev head-study result")
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
	seen := make(map[string]int)
	for _, record := range records {
		if !isAnyJevHeadStudyJSONPath(record.SourcePath) {
			continue
		}
		var header struct {
			SchemaVersion string `json:"schema_version"`
			SourceKind    string `json:"source_kind"`
			QuestionCount int    `json:"question_count"`
		}
		if err := json.Unmarshal(record.Payload, &header); err != nil || header.SchemaVersion != anyJevHeadStudyResultSchema ||
			header.SourceKind != "per_question_head_study" || header.QuestionCount == 0 {
			t.Fatalf("head-study result failed semantic snapshot readback for record %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("head-study source/readback mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("head-study source did not produce exactly one record: %s count=%d", path, seen[path])
		}
	}
}
