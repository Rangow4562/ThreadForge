package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevUniversalQuestionFixture(domain string, protocol string) map[string]any {
	methods := []string{"raw", "L1", "per-question head", "universal:probe", "universal:pos"}
	if protocol == "loqo" || (protocol == "cross" && domain == "typed") {
		methods = append(methods, "L0-perm")
	}
	rows := make(map[string]any, len(methods))
	for _, method := range methods {
		rows[method] = map[string]any{
			"acc": 0.8, "ece": 0.1, "brier": 0.3, "nll": 0.4, "cov5": 0.9,
		}
	}
	heads := map[string]any{}
	for _, form := range []string{"probe", "pos"} {
		heads[form] = map[string]any{
			"layer": float64(0), "layer_abs": float64(36), "lam": 1.0, "temperature": 1.0,
			"oof_nll": 0.4, "oof_acc": 0.8, "folds": float64(5),
		}
	}
	return map[string]any{
		"domain": domain, "heads": heads, "k": float64(4), "kind": "choice", "n_test": float64(2),
		"rows": rows, "workflow": "private.workflow",
	}
}

func anyJevUniversalAggregateFixture(n int) map[string]any {
	return map[string]any{"acc": 0.8, "ece": 0.1, "brier": 0.3, "nll": 0.4, "n": float64(n)}
}

func anyJevUniversalProtocolFixture(protocol string) map[string]any {
	questions := map[string]any{
		"private.question.one": anyJevUniversalQuestionFixture("typed", protocol),
		"private.question.two": anyJevUniversalQuestionFixture("typed", protocol),
	}
	pooledMethods := []string{"raw", "L1", "L0-perm", "per-question head", "universal:probe", "universal:pos"}
	pooled := make(map[string]any, len(pooledMethods))
	for _, method := range pooledMethods {
		n := 4
		if protocol == "cross" {
			n = 6
			if method == "L0-perm" {
				n = 2
			}
		}
		pooled[method] = anyJevUniversalAggregateFixture(n)
	}
	byDomain := map[string]any{}
	if protocol == "cross" {
		questions = map[string]any{
			"private.question.typed": anyJevUniversalQuestionFixture("typed", protocol),
			"private.question.extra": anyJevUniversalQuestionFixture("extra", protocol),
			"private.question.games": anyJevUniversalQuestionFixture("games", protocol),
		}
		pooled = make(map[string]any, len(pooledMethods))
		for _, method := range pooledMethods {
			n := 6
			if method == "L0-perm" {
				n = 2
			}
			pooled[method] = anyJevUniversalAggregateFixture(n)
		}
		domainMethods := []string{"raw", "L1", "per-question head", "universal:probe", "universal:pos"}
		for _, domain := range []string{"typed", "extra", "games"} {
			availableMethods := domainMethods
			if domain == "typed" {
				availableMethods = append(append([]string{}, domainMethods...), "L0-perm")
			}
			methods := make(map[string]any, len(availableMethods))
			for _, method := range availableMethods {
				methods[method] = anyJevUniversalAggregateFixture(2)
			}
			byDomain[domain] = methods
		}
	}
	return map[string]any{
		"model": "Qwen/Qwen3-8B", "date": "2026-09-22T10:11:12.123456",
		"env":      map[string]any{"device": "private-device"},
		"protocol": protocol, "train_order": "rand", "which": "last", "seed": float64(42),
		"layers": []any{float64(-1), float64(-5)}, "layer_abs": []any{float64(36), float64(32)}, "forms": "probe,pos", "lams": "1,10",
		"ref":       "private/reference.json",
		"questions": questions, "pooled": pooled, "by_domain": byDomain,
	}
}

func TestAnyJevUniversalProtocolResultNormalizesAndRedacts(t *testing.T) {
	for _, protocol := range []string{"loqo", "cross"} {
		study := protocol
		if protocol == "cross" {
			study = "cross_domain"
		}
		path := "bench/results_universal/2026-09-22/qwen__qwen3-8b." + protocol + ".rand.last.json"
		item, err := normalizeAnyJevUniversalResult(path, anyJevUniversalProtocolFixture(protocol))
		if err != nil {
			t.Fatalf("%s normalization: %v", protocol, err)
		}
		var payload anyJevUniversalImport
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if item.Type != "decision_label_or_evaluation" || payload.StudyKind != study ||
			payload.ModelSHA256 != anyJevStringSHA256("Qwen/Qwen3-8B") || len(payload.Rows) == 0 ||
			!payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatalf("unexpected %s projection: %#v", protocol, payload)
		}
		encoded := string(item.Payload)
		for _, privateValue := range []string{"Qwen/Qwen3-8B", "private.question", "private.workflow", "private-device", "2026-09-22"} {
			if strings.Contains(encoded, privateValue) {
				t.Fatalf("%s projection retained %q", protocol, privateValue)
			}
		}
	}
}

func TestAnyJevUniversalProtocolRejectsInconsistentAndUnexpectedData(t *testing.T) {
	tests := map[string]func(map[string]any){
		"wrong filename identity": func(root map[string]any) {},
		"unknown root field":      func(root map[string]any) { root["private_path"] = "C:/secret" },
		"pooled accuracy mismatch": func(root map[string]any) {
			root["pooled"].(map[string]any)["raw"].(map[string]any)["acc"] = 0.2
		},
		"unknown question metric": func(root map[string]any) {
			root["questions"].(map[string]any)["private.question.one"].(map[string]any)["rows"].(map[string]any)["raw"].(map[string]any)["secret"] = 1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			root := anyJevUniversalProtocolFixture("loqo")
			mutate(root)
			path := "bench/results_universal/2026-09-22/qwen__qwen3-8b.loqo.rand.last.json"
			if name == "wrong filename identity" {
				path = "bench/results_universal/2026-09-22/other.loqo.rand.last.json"
			}
			if _, err := normalizeAnyJevUniversalResult(path, root); err == nil {
				t.Fatal("invalid universal result was accepted")
			}
		})
	}
}

func TestAnyJevUniversalResultPathIsNarrow(t *testing.T) {
	for _, path := range []string{
		"bench/results_universal/2026-09-22/model.loqo.rand.last.json",
		"bench/results_universal/2026-09-22/model.cross.rand.last.json",
		"bench/results_universal/e0/model.e0.json",
	} {
		if !isAnyJevUniversalResultJSONPath(path) {
			t.Errorf("supported path was not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_universal/model.loqo.rand.last.json",
		"bench/results_universal/not-a-date/model.loqo.rand.last.json",
		"bench/results_universal/2026-09-22/nested/model.loqo.rand.last.json",
		"bench/results_universal/e0/nested/model.e0.json",
		"other/results_universal/2026-09-22/model.cross.rand.last.json",
		"bench/results_universal/2026-09-22/model.unknown.rand.last.json",
	} {
		if isAnyJevUniversalResultJSONPath(path) {
			t.Errorf("unsupported path was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to verify local universal-study and demo-game
// results through semantic normalization, migration snapshot creation, and readback.
func TestAnyJevUniversalSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the universal-study migration check")
	}
	var paths []string
	for _, pattern := range []string{
		filepath.Join(sourceRoot, "bench", "results_universal", "*", "*.json"),
		filepath.Join(sourceRoot, "demo", "results", "twenty48_*.json"),
		filepath.Join(sourceRoot, "demo", "results", "minesweeper_*.json"),
		filepath.Join(sourceRoot, "demo", "results", "jev_mode_*.json"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	var directlyNormalized int
	var demoGamesNormalized int
	var jevModeNormalized int
	expected := make(map[string]struct{})
	for _, sourcePath := range paths {
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		relative = filepath.ToSlash(relative)
		if !isAnyJevUniversalResultJSONPath(relative) && !isAnyJevDemoGameJSONPath(relative) && !isAnyJevDemoJevModeJSONPath(relative) {
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
		items, handled, err := normalizeAnyJevItems(relative, source)
		if err != nil || !handled || len(items) != 1 {
			t.Fatalf("source result failed semantic normalization: %s: handled=%v records=%d err=%v", relative, handled, len(items), err)
		}
		if strings.Contains(string(items[0].Payload), "Qwen/Qwen3-8B") || strings.Contains(string(items[0].Payload), "Qwen3-8B") ||
			strings.Contains(string(items[0].Payload), "2026-09-22") {
			t.Fatalf("source result retained identifying provenance: %s", relative)
		}
		if isAnyJevUniversalResultJSONPath(relative) {
			directlyNormalized++
		} else if isAnyJevDemoGameJSONPath(relative) {
			demoGamesNormalized++
		} else {
			jevModeNormalized++
		}
		expected[relative] = struct{}{}
	}
	if directlyNormalized != 6 {
		t.Fatalf("expected six direct source normalizations, got %d", directlyNormalized)
	}
	if demoGamesNormalized != 5 {
		t.Fatalf("expected five direct demo-game source normalizations, got %d", demoGamesNormalized)
	}
	if jevModeNormalized != 4 {
		t.Fatalf("expected four direct Jev-mode source normalizations, got %d", jevModeNormalized)
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range manifest.Files {
		if _, ok := expected[file.Path]; !ok {
			continue
		}
		if file.Status != "ready" || file.ImportedCount != 1 {
			t.Fatalf("semantic result did not produce one record: %s (%s: %s)", file.Path, file.Status, file.Issue)
		}
	}
	if len(expected) != 15 {
		t.Fatalf("expected fifteen staged semantic results, got %d", len(expected))
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
		var payload struct {
			SchemaVersion       string `json:"schema_version"`
			HumanReviewRequired bool   `json:"human_review_required"`
			ExecutionVerified   bool   `json:"execution_verified"`
			CutoverEligible     bool   `json:"cutover_evidence_eligible"`
		}
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatalf("result did not read back in the semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible || !payload.HumanReviewRequired {
			t.Fatalf("historical result trust state changed: %s", record.Key)
		}
		wantSchema := anyJevUniversalResultSchema
		if isAnyJevDemoGameJSONPath(record.SourcePath) {
			wantSchema = anyJevDemoGameResultSchema
		} else if isAnyJevDemoJevModeJSONPath(record.SourcePath) {
			wantSchema = anyJevDemoJevModeResultSchema
		}
		if payload.SchemaVersion != wantSchema {
			t.Fatalf("result read back under schema %q, expected %q: %s", payload.SchemaVersion, wantSchema, record.Key)
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
