package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func anyJevNanoJevMazeProtocolFixture() map[string]any {
	return map[string]any{
		"atomic_metric_scope":    "one value per four directional questions",
		"brier_definition":       "mean squared probability error",
		"collision_rule":         "wall collision",
		"default_horizon":        "bounded by evaluator",
		"fallback_definition":    "stuck fallback behavior",
		"full_map_bfs_used":      false,
		"max_steps":              float64(0),
		"memory_termination":     "stop on goal or horizon",
		"nll_probability_floor":  1e-9,
		"oracle_used_for_policy": false,
		"prediction_cache":       "cache only within current run",
		"probe_rule":             "bounded local probe",
		"reposition_rule":        "verified edge reposition",
		"selection_rule":         "engine selected from local probabilities",
		"temperature":            1.0,
		"threshold":              0.5,
		"window_size":            float64(5),
	}
}

func anyJevNanoJevMazeInitialStateFixture(size int, seed int) map[string]any {
	return map[string]any{
		"game": "scaled_maze", "goal": []any{float64(9), float64(9)},
		"position": []any{float64(1), float64(1)}, "seed": float64(seed),
		"size": float64(size), "topology": "private-topology-label",
		"walls": []any{"###########", "#.........#"},
	}
}

func anyJevNanoJevMazeEpisodeFixture(id, split, status string, completed bool, sites int, accuracy, brier, nll float64) map[string]any {
	questions := sites * 4
	return map[string]any{
		"atomic_metrics": map[string]any{
			"atomic_accuracy": accuracy, "atomic_brier": brier, "atomic_nll": nll,
			"atomic_questions": float64(questions), "prediction_sites": float64(sites),
		},
		"attempts": float64(3), "collisions": float64(1), "fallback_count": float64(1),
		"goal_completion": completed, "horizon": float64(20), "id": id,
		"initial_state": anyJevNanoJevMazeInitialStateFixture(11, 7),
		"probe_count":   float64(1), "revisited_cell_entries": float64(1),
		"size": float64(11), "split": split, "status": status,
		"successful_moves": float64(2), "verified_edge_reposition_count": float64(1),
		"visited_cells": float64(2),
	}
}

func anyJevNanoJevMazeSummaryFixture() map[string]any {
	return map[string]any{
		"atomic_accuracy":  (0.75*8 + 0.5*4) / 12,
		"atomic_brier":     (0.2*8 + 0.4*4) / 12,
		"atomic_nll":       (0.3*8 + 0.8*4) / 12,
		"atomic_questions": float64(12), "attempts": float64(6), "collisions": float64(2),
		"edge_majority": 0.6, "edge_questions": float64(12), "episodes": float64(2),
		"fallback_count": float64(2), "goal_completed": float64(1),
		"goal_completion_rate": 0.5, "mean_p_true": 0.45,
		"prediction_sites": float64(3), "probe_count": float64(2),
		"successful_moves": float64(4), "verified_edge_reposition_count": float64(2),
	}
}

func anyJevNanoJevMazeFixture() map[string]any {
	return map[string]any{
		"audit_inputs_sha256": strings.Repeat("b", 64),
		"date":                "2026-09-21T10:11:12.123456",
		"elapsed_seconds":     12.5,
		"engine":              "anyjev",
		"episodes": []any{
			anyJevNanoJevMazeEpisodeFixture("private-episode-test", "test", "goal", true, 2, 0.75, 0.2, 0.3),
			anyJevNanoJevMazeEpisodeFixture("private-episode-ood", "ood", "horizon_exhausted", false, 1, 0.5, 0.4, 0.8),
		},
		"episodes_sha256": strings.Repeat("a", 64),
		"execution": map[string]any{
			"model_forward_passes": nil, "network_model_calls": float64(0), "predict_calls": float64(10),
		},
		"level": "L0", "model": "private-model-label", "model_controls": "private controls",
		"model_role":     "model_guided_local_edge_exploration",
		"nanojev_commit": strings.Repeat("c", 40), "nanojev_dir": "private-local-directory",
		"prior": "batch", "protocol": anyJevNanoJevMazeProtocolFixture(),
		"question_template": map[string]any{
			"clear_north": map[string]any{"criteria": map[string]any{"true": "private north true", "false": "private north false"}, "instructions": "private question north", "type": "boolean"},
			"clear_east":  map[string]any{"criteria": map[string]any{"true": "private east true", "false": "private east false"}, "instructions": "private question east", "type": "boolean"},
			"clear_south": map[string]any{"criteria": map[string]any{"true": "private south true", "false": "private south false"}, "instructions": "private question south", "type": "boolean"},
			"clear_west":  map[string]any{"criteria": map[string]any{"true": "private west true", "false": "private west false"}, "instructions": "private question west", "type": "boolean"},
		},
		"revision": "private-model-revision",
		"schema":   "nanojev-model-edges-maze-v1",
		"summary":  anyJevNanoJevMazeSummaryFixture(),
	}
}

func TestAnyJevNanoJevMazeResultProjectsReconciledHistoricalSplits(t *testing.T) {
	items, handled, err := normalizeAnyJevItems("bench/results_nanojev/2026-09-22/model.json", anyJevNanoJevMazeFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("NanoJev maze import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	if items[0].Type != "decision_label_or_evaluation" {
		t.Fatalf("unexpected record type: %s", items[0].Type)
	}
	var payload anyJevNanoJevMazeImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != anyJevNanoJevMazeResultSchema ||
		payload.ModelSHA256 != anyJevStringSHA256("private-model-label") ||
		payload.ModelRevisionSHA256 != anyJevStringSHA256("private-model-revision") ||
		payload.Engine != "anyjev" || payload.Level != "L0" || payload.Prior != "batch" ||
		payload.Overall.Episodes != 2 || payload.Overall.GoalCompleted != 1 ||
		payload.Overall.AtomicQuestions != 12 || payload.Overall.PredictionSites != 3 ||
		len(payload.Overall.MazeSizes) != 1 || payload.Overall.MazeSizes[0] != 11 ||
		len(payload.BySplit) != 2 || payload.BySplit[0].Split != "ood" || payload.BySplit[0].Summary.GoalCompleted != 0 ||
		payload.BySplit[1].Split != "test" || payload.BySplit[1].Summary.GoalCompleted != 1 ||
		payload.Protocol.OracleUsedForPolicy || payload.Protocol.FullMapBFSUsed ||
		!payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEvidenceEligible {
		t.Fatalf("NanoJev maze result projection is incorrect: %#v", payload)
	}
	for _, privateValue := range []string{
		"private-model-label", "private-model-revision", "private controls", "private-local-directory",
		"private-episode-test", "private-episode-ood", "private question", "private-topology-label",
	} {
		if strings.Contains(string(items[0].Payload), privateValue) {
			t.Fatalf("NanoJev maze projection retained %q", privateValue)
		}
	}
	for _, omittedKey := range []string{"initial_state", "question_templates", "execution_counters"} {
		if strings.Contains(string(items[0].Payload), `"`+omittedKey+`":`) {
			t.Fatalf("NanoJev maze projection retained the omitted field %q", omittedKey)
		}
	}
	if !strings.Contains(string(items[0].Payload), strings.Repeat("a", 64)) ||
		!strings.Contains(string(items[0].Payload), strings.Repeat("c", 40)) {
		t.Fatal("NanoJev dataset or source-commit bindings were not preserved")
	}
}

func TestAnyJevNanoJevNativeMazeResultAllowsAbsentPriorAndRevision(t *testing.T) {
	fixture := anyJevNanoJevMazeFixture()
	fixture["engine"] = "nanojev_native"
	fixture["level"] = "native_AB_readout"
	delete(fixture, "prior")
	fixture["revision"] = nil
	item, err := normalizeAnyJevNanoJevMazeResult(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var payload anyJevNanoJevMazeImport
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Engine != "nanojev_native" || payload.Level != "native_AB_readout" || payload.Prior != "" || payload.ModelRevisionSHA256 != "" {
		t.Fatalf("native maze variant is not normalized: %#v", payload)
	}
}

func TestAnyJevNanoJevMazeRejectsUnknownOrInconsistentInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["environment"] = map[string]any{} }},
		{name: "unknown protocol field", mutate: func(value map[string]any) {
			value["protocol"].(map[string]any)["extra"] = true
		}},
		{name: "oracle use", mutate: func(value map[string]any) {
			value["protocol"].(map[string]any)["oracle_used_for_policy"] = true
		}},
		{name: "unknown episode field", mutate: func(value map[string]any) {
			value["episodes"].([]any)[0].(map[string]any)["observations"] = []any{}
		}},
		{name: "duplicate episode id", mutate: func(value map[string]any) {
			value["episodes"].([]any)[1].(map[string]any)["id"] = "private-episode-test"
		}},
		{name: "invalid attempt count", mutate: func(value map[string]any) {
			value["episodes"].([]any)[0].(map[string]any)["attempts"] = float64(100)
		}},
		{name: "status and goal mismatch", mutate: func(value map[string]any) {
			value["episodes"].([]any)[0].(map[string]any)["status"] = "horizon_exhausted"
		}},
		{name: "atomic metric count mismatch", mutate: func(value map[string]any) {
			value["episodes"].([]any)[0].(map[string]any)["atomic_metrics"].(map[string]any)["atomic_questions"] = float64(7)
		}},
		{name: "summary count mismatch", mutate: func(value map[string]any) {
			value["summary"].(map[string]any)["attempts"] = float64(99)
		}},
		{name: "weighted metric mismatch", mutate: func(value map[string]any) {
			value["summary"].(map[string]any)["atomic_accuracy"] = float64(0.1)
		}},
		{name: "unsupported split", mutate: func(value map[string]any) {
			value["episodes"].([]any)[1].(map[string]any)["split"] = "train"
		}},
		{name: "bad source digest", mutate: func(value map[string]any) { value["episodes_sha256"] = "bad" }},
		{name: "credential-like model", mutate: func(value map[string]any) { value["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevNanoJevMazeFixture()
			test.mutate(fixture)
			if _, err := normalizeAnyJevNanoJevMazeResult(fixture); err == nil {
				t.Fatal("invalid NanoJev maze result was accepted")
			}
		})
	}
}

func TestAnyJevNanoJevMazePathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_nanojev/2026-09-22/model.json",
		"bench/results_nanojev/model.json",
	} {
		if !isAnyJevNanoJevMazeJSONPath(path) {
			t.Errorf("NanoJev maze JSON path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_nanojev_extra/model.json",
		"bench/results_nanojev/model.items.json",
		"bench/results_games/model.json",
		"docs/results_maze.json",
	} {
		if isAnyJevNanoJevMazeJSONPath(path) {
			t.Errorf("path outside NanoJev maze projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to a local AnyJev checkout to verify every
// supported NanoJev maze summary through inspection and temporary readback.
func TestAnyJevNanoJevMazeSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the source-backed maze migration check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	var rejectedSourceFiles int
	for _, file := range manifest.Files {
		if isAnyJevNanoJevMazeJSONPath(file.Path) {
			if file.Status == "ready" && file.ImportedCount == 1 {
				expected[file.Path] = struct{}{}
			} else {
				rejectedSourceFiles++
				t.Logf("maze source classification: status=%s imported=%d rejected=%d issue=%s", file.Status, file.ImportedCount, file.RejectedCount, file.Issue)
				path := filepath.Join(sourceRoot, filepath.FromSlash(file.Path))
				info, statErr := os.Lstat(path)
				if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
					t.Fatalf("refusing to diagnose non-regular maze source file: %v", statErr)
				}
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatalf("read maze source for converter diagnosis: %v", readErr)
				}
				var sourceValue any
				if err := json.Unmarshal(data, &sourceValue); err != nil {
					t.Fatalf("decode maze source for converter diagnosis: %v", err)
				}
				_, normalizeErr := normalizeAnyJevNanoJevMazeResult(sourceValue)
				t.Logf("maze semantic projection rejection: %v", normalizeErr)
			}
		}
	}
	if len(expected) == 0 {
		t.Fatalf("source contains no importable NanoJev maze summary JSON; rejected=%d", rejectedSourceFiles)
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
		if !isAnyJevNanoJevMazeJSONPath(record.SourcePath) {
			continue
		}
		var payload anyJevNanoJevMazeImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevNanoJevMazeResultSchema {
			t.Fatalf("NanoJev maze result did not read back in the strict semantic format: %s", record.Key)
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEvidenceEligible {
			t.Fatalf("NanoJev maze result trust status changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("NanoJev maze source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
	for path := range expected {
		if seen[path] != 1 {
			t.Fatalf("NanoJev maze result did not produce exactly one record (count=%d): %s", seen[path], path)
		}
	}
}
