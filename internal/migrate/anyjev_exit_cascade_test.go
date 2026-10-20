package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func anyJevExitCascadeFixture() map[string]any {
	exits := []any{float64(18), float64(22), float64(26), float64(30), float64(36)}
	rows := make(map[string]any, 21)
	fixedAccuracy := 0.78
	for index, exit := range []int{18, 22, 26, 30, 36} {
		block := float64(exit)
		rows["fixed@"+strconv.Itoa(exit)] = map[string]any{
			"acc": 0.70 + 0.02*float64(index), "ece": 0.10 - 0.01*float64(index),
			"mean_blocks": block, "nll": 0.8 - 0.1*float64(index), "relative_depth": block / 36,
		}
	}
	shares := map[string]any{"18": 0.0, "22": 0.5, "26": 0.0, "30": 0.0, "36": 0.5}
	for _, rule := range []string{"quantile", "ltt"} {
		for _, epsilon := range anyJevExitCascadeEpsilonKeys {
			for _, scope := range []string{"per-question", "pooled"} {
				key := "cascade:" + rule + ":eps=" + epsilon + ":" + scope
				rows[key] = map[string]any{
					"acc": 0.76, "ece": 0.05, "excess_error": fixedAccuracy - 0.76,
					"exit_shares": shares, "mean_blocks": 29.0, "nll": 0.4, "relative_depth": 29.0 / 36,
				}
			}
		}
	}
	return map[string]any{
		"date":  "2026-09-28T10:11:12.123456",
		"env":   map[string]any{"gpu": "private-device", "path": "C:/private/model"},
		"exits": exits, "model": "private/model-label", "n_blocks": float64(36), "rows": rows,
	}
}

func TestAnyJevExitCascadeReconcilesPoliciesAndRedactsLabels(t *testing.T) {
	items, handled, err := normalizeAnyJevItems("bench/results_exit/2026-09-28/model.cascade.json", anyJevExitCascadeFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("exit cascade import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevExitCascadeImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if items[0].Type != "decision_label_or_evaluation" || payload.SchemaVersion != anyJevExitCascadeResultSchema ||
		payload.ModelSHA256 != anyJevStringSHA256("private/model-label") || payload.TransformerLayers != 36 ||
		len(payload.Exits) != 5 || len(payload.Rows) != 21 || !payload.HumanReviewRequired ||
		payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("exit cascade projection is incorrect: %#v", payload)
	}
	cascadeRows, fixedRows := 0, 0
	for _, row := range payload.Rows {
		if row.Kind == "cascade" {
			cascadeRows++
			if row.Epsilon == nil || row.Scope == "" || row.ExcessError == nil || len(row.ExitShares) != 5 {
				t.Fatalf("cascade rule fields were not preserved: %#v", row)
			}
		} else if row.Kind == "fixed" {
			fixedRows++
			if row.Block == nil || row.ExcessError != nil || len(row.ExitShares) != 0 {
				t.Fatalf("fixed baseline row shape is incorrect: %#v", row)
			}
		}
	}
	if cascadeRows != 16 || fixedRows != 5 {
		t.Fatalf("unexpected cascade/fixed row counts: cascade=%d fixed=%d", cascadeRows, fixedRows)
	}
	encoded := string(items[0].Payload)
	for _, privateValue := range []string{
		"private/model-label", "2026-09-28", "private-device", "C:/private/model",
		"cascade:quantile:eps=0.0:pooled", "fixed@18",
	} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("exit cascade projection retained %q", privateValue)
		}
	}
}

func TestAnyJevExitCascadeRejectsIncompleteOrInconsistentRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["extra"] = true }},
		{name: "missing fixed baseline", mutate: func(value map[string]any) {
			delete(value["rows"].(map[string]any), "fixed@18")
		}},
		{name: "unsupported epsilon", mutate: func(value map[string]any) {
			rows := value["rows"].(map[string]any)
			delete(rows, "cascade:quantile:eps=0.0:pooled")
			rows["cascade:quantile:eps=0.03:pooled"] = map[string]any{
				"acc": 0.76, "ece": 0.05, "excess_error": 0.02,
				"exit_shares": map[string]any{"18": 0.0, "22": 0.5, "26": 0.0, "30": 0.0, "36": 0.5},
				"mean_blocks": 29.0, "nll": 0.4, "relative_depth": 29.0 / 36,
			}
		}},
		{name: "shares do not sum to one", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["cascade:ltt:eps=0.0:pooled"].(map[string]any)["exit_shares"].(map[string]any)["22"] = 0.1
		}},
		{name: "mean does not match shares", mutate: func(value map[string]any) {
			row := value["rows"].(map[string]any)["cascade:ltt:eps=0.0:pooled"].(map[string]any)
			row["mean_blocks"] = 28.0
			row["relative_depth"] = 28.0 / 36
		}},
		{name: "excess error does not match full-depth reference", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["cascade:ltt:eps=0.0:pooled"].(map[string]any)["excess_error"] = 0.5
		}},
		{name: "fixed metric does not match block", mutate: func(value map[string]any) {
			value["rows"].(map[string]any)["fixed@22"].(map[string]any)["mean_blocks"] = 21.0
		}},
		{name: "credential-like model", mutate: func(value map[string]any) { value["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevExitCascadeFixture()
			test.mutate(fixture)
			if _, err := normalizeAnyJevExitCascadeResult(fixture); err == nil {
				t.Fatal("invalid exit cascade result was accepted")
			}
		})
	}
}

func TestAnyJevExitCascadePathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_exit/2026-09-28/model.cascade.json",
		"BENCH/results_exit/model.CASCADE.JSON",
	} {
		if !isAnyJevExitCascadeJSONPath(path) {
			t.Errorf("exit cascade JSON path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_exit_extra/model.cascade.json",
		"bench/results_exit/model.cascade.items.json",
		"other/results_exit/model.cascade.json",
	} {
		if isAnyJevExitCascadeJSONPath(path) {
			t.Errorf("path outside exit cascade projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to a local AnyJev checkout to verify every
// cascade output and its fixed-depth references through snapshot readback.
func TestAnyJevExitCascadeSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the exit cascade migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := 0
	for _, file := range manifest.Files {
		if !isAnyJevExitCascadeJSONPath(file.Path) {
			continue
		}
		expected++
		if file.Status != "ready" || file.ImportedCount != 1 {
			t.Fatal("cascade source file was not prepared as exactly one semantic record")
		}
	}
	if expected == 0 {
		t.Skip("source checkout has no cascade result JSON files")
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
	depthByModel := make(map[string]map[int]anyJevExitDepthMetrics)
	cascades := make([]anyJevExitCascadeImport, 0, expected)
	seenCascade := 0
	for _, record := range records {
		if isAnyJevExitDepthJSONPath(record.SourcePath) {
			var depth anyJevExitDepthImport
			if err := json.Unmarshal(record.Payload, &depth); err != nil {
				t.Fatal(err)
			}
			blocks := make(map[int]anyJevExitDepthMetrics, len(depth.Blocks))
			for _, block := range depth.Blocks {
				blocks[block.Block] = block.Head
			}
			depthByModel[depth.ModelSHA256] = blocks
			continue
		}
		if !isAnyJevExitCascadeJSONPath(record.SourcePath) {
			continue
		}
		var cascade anyJevExitCascadeImport
		if err := json.Unmarshal(record.Payload, &cascade); err != nil || cascade.SchemaVersion != anyJevExitCascadeResultSchema {
			t.Fatal("cascade result did not read back in the strict semantic format")
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			cascade.ExecutionVerified || cascade.CutoverEligible {
			t.Fatal("cascade trust status changed during snapshot readback")
		}
		cascades = append(cascades, cascade)
		seenCascade++
	}
	if seenCascade != expected {
		t.Fatalf("cascade source/readback count mismatch: expected=%d actual=%d", expected, seenCascade)
	}
	for _, cascade := range cascades {
		depth, exists := depthByModel[cascade.ModelSHA256]
		if !exists {
			t.Fatal("cascade record has no same-model exit-depth source result")
		}
		for _, row := range cascade.Rows {
			if row.Kind != "fixed" || row.Block == nil {
				continue
			}
			fixed, exists := depth[*row.Block]
			if !exists || absAnyJevFloat(fixed.Accuracy-row.Accuracy) > 2e-6 ||
				absAnyJevFloat(fixed.ECE-row.ECE) > 2e-6 || absAnyJevFloat(fixed.NLL-row.NLL) > 2e-6 {
				t.Fatal("cascade fixed-depth reference does not match exit-depth head metrics")
			}
		}
	}
}
