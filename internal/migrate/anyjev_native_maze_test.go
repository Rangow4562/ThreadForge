package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var anyJevNativeMazeTestWalls = [][]bool{
	{true, true, true, true, true},
	{true, false, false, false, true},
	{true, true, true, false, true},
	{true, false, false, false, true},
	{true, true, true, true, true},
}

var anyJevNativeMazeTestPredictions = []anyJevMazePrediction{
	{step: 0, pos: anyJevMazePosition{1, 1}},
	{step: 1, pos: anyJevMazePosition{1, 2}},
	{step: 2, pos: anyJevMazePosition{1, 3}},
	{step: 3, pos: anyJevMazePosition{2, 3}},
}

func anyJevNativeMazeFixtureRun(level string) map[string]any {
	probabilities := map[string]any{"north": 0.2, "east": 0.8, "south": 0.3, "west": 0.4}
	predictions := make([]any, 0, len(anyJevNativeMazeTestPredictions))
	for _, prediction := range anyJevNativeMazeTestPredictions {
		truth := make(map[string]any, 4)
		for _, direction := range anyJevNativeMazeDirections {
			truth[direction] = anyJevNativeMazeIsOpen(anyJevNativeMazeTestWalls, anyJevNativeMazeMove(prediction.pos, direction), 5)
		}
		predictions = append(predictions, map[string]any{
			"step": float64(prediction.step), "pos": []any{float64(prediction.pos.row), float64(prediction.pos.col)},
			"p_open": probabilities, "truth": truth,
		})
	}
	metrics := anyJevNativeMazeCalculateMetrics(func() []anyJevMazePrediction {
		out := make([]anyJevMazePrediction, 0, len(anyJevNativeMazeTestPredictions))
		for _, prediction := range anyJevNativeMazeTestPredictions {
			prediction.open = map[string]float64{"north": 0.2, "east": 0.8, "south": 0.3, "west": 0.4}
			prediction.truth = make(map[string]bool, 4)
			for _, direction := range anyJevNativeMazeDirections {
				prediction.truth[direction] = anyJevNativeMazeIsOpen(anyJevNativeMazeTestWalls, anyJevNativeMazeMove(prediction.pos, direction), 5)
			}
			out = append(out, prediction)
		}
		return out
	}())
	run := map[string]any{
		"level": level, "status": "goal", "steps": float64(4), "collisions": float64(0), "cells_seen": float64(4),
		"elapsed_seconds": 1.25, "edge_accuracy": metrics.accuracy, "edge_brier": metrics.brier,
		"mean_p_open": metrics.meanPOpen, "open_rate": metrics.openRate, "predictions": predictions,
		"moves": []any{
			map[string]any{"dir": "east", "kind": "try", "pos": []any{float64(1), float64(2)}, "hit": false, "t": float64(1)},
			map[string]any{"dir": "east", "kind": "try", "pos": []any{float64(1), float64(3)}, "hit": false, "t": float64(2)},
			map[string]any{"dir": "south", "kind": "try", "pos": []any{float64(2), float64(3)}, "hit": false, "t": float64(3)},
			map[string]any{"dir": "south", "kind": "try", "pos": []any{float64(3), float64(3)}, "hit": false, "t": float64(4)},
		},
	}
	if level == "L1" {
		run["temperatures"] = map[string]any{"north": 1.1, "east": 1.2, "south": 1.3, "west": 1.4}
	}
	return run
}

func anyJevNativeMazeFixture() map[string]any {
	questions := map[string]any{
		"north": "private question north", "east": "private question east",
		"south": "private question south", "west": "private question west",
	}
	return map[string]any{
		"game": "maze", "model": "private/model-label", "revision": "private-revision",
		"size": float64(5), "prior": "batch", "policy": "expected_cost", "wall_cost": float64(10),
		"threshold": 0.5, "calib_n": float64(200), "calib_seed0": float64(10000),
		"questions":   questions,
		"environment": map[string]any{"gpu": "private-device", "path": "C:/private/model"},
		"episodes": []any{
			map[string]any{
				"seed":  float64(7),
				"walls": []any{"#####", "#...#", "###.#", "#...#", "#####"},
				"start": []any{float64(1), float64(1)}, "goal": []any{float64(3), float64(3)},
				"runs": map[string]any{"raw": anyJevNativeMazeFixtureRun("raw"), "L1": anyJevNativeMazeFixtureRun("L1")},
			},
		},
	}
}

func TestAnyJevNativeMazeReconcilesTrajectoryAndOmitsRawTraces(t *testing.T) {
	items, handled, err := normalizeAnyJevItems("bench/results_games/2026-09-28/maze.private__model.json", anyJevNativeMazeFixture())
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("native maze import: handled=%v items=%d err=%v", handled, len(items), err)
	}
	var payload anyJevNativeMazeImport
	if err := json.Unmarshal(items[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if items[0].Type != "decision_label_or_evaluation" || payload.SchemaVersion != anyJevNativeMazeResultSchema ||
		payload.ModelSHA256 != anyJevStringSHA256("private/model-label") || payload.RevisionSHA256 != anyJevStringSHA256("private-revision") ||
		payload.MazeSize != 5 || payload.CalibrationExamples != 200 || len(payload.QuestionDigests) != 4 ||
		len(payload.Levels) != 2 || payload.Levels[0] != "raw" || payload.Levels[1] != "L1" || len(payload.Episodes) != 1 ||
		len(payload.Episodes[0].Runs) != 2 || payload.Episodes[0].Runs[1].Temperatures["west"] != 1.4 ||
		!payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
		t.Fatalf("native maze projection is incorrect: %#v", payload)
	}
	encoded := string(items[0].Payload)
	for _, privateValue := range []string{
		"private/model-label", "private-revision", "private-device", "C:/private/model", "2026-09-28",
		"private question", "#####", "#...#", "predictions", "moves",
	} {
		if strings.Contains(encoded, privateValue) {
			t.Fatalf("native maze projection retained %q", privateValue)
		}
	}
}

func TestAnyJevNativeMazeRejectsTamperedGridTraceOrMetrics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["extra"] = true }},
		{name: "unsupported question direction", mutate: func(value map[string]any) {
			value["questions"].(map[string]any)["center"] = "private"
		}},
		{name: "changed truth vector", mutate: func(value map[string]any) {
			run := value["episodes"].([]any)[0].(map[string]any)["runs"].(map[string]any)["raw"].(map[string]any)
			run["predictions"].([]any)[0].(map[string]any)["truth"].(map[string]any)["east"] = false
		}},
		{name: "changed move destination", mutate: func(value map[string]any) {
			run := value["episodes"].([]any)[0].(map[string]any)["runs"].(map[string]any)["raw"].(map[string]any)
			run["moves"].([]any)[0].(map[string]any)["pos"] = []any{float64(1), float64(1)}
		}},
		{name: "incorrect aggregate accuracy", mutate: func(value map[string]any) {
			run := value["episodes"].([]any)[0].(map[string]any)["runs"].(map[string]any)["raw"].(map[string]any)
			run["edge_accuracy"] = 0.99
		}},
		{name: "duplicate seed", mutate: func(value map[string]any) {
			first := value["episodes"].([]any)[0].(map[string]any)
			second := map[string]any{}
			for key, entry := range first {
				second[key] = entry
			}
			value["episodes"] = append(value["episodes"].([]any), second)
		}},
		{name: "credential-like model", mutate: func(value map[string]any) { value["model"] = "api_key=secret" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevNativeMazeFixture()
			test.mutate(fixture)
			if _, err := normalizeAnyJevNativeMazeResult(fixture); err == nil {
				t.Fatal("invalid native maze result was accepted")
			}
		})
	}
}

func TestAnyJevNativeMazePathsAreNarrowlyRecognized(t *testing.T) {
	for _, path := range []string{
		"bench/results_games/2026-09-28/maze.model.json",
		"BENCH/results_games/maze.Model.JSON",
	} {
		if !isAnyJevNativeMazeJSONPath(path) {
			t.Errorf("native maze result path not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"bench/results_games_extra/maze.model.json",
		"bench/results_games/2026-09-28/other.json",
		"bench/results_games/maze.model.items.json",
	} {
		if isAnyJevNativeMazeJSONPath(path) {
			t.Errorf("path outside native maze result projection was recognized: %s", path)
		}
	}
}

// Set THREADFORGE_ANYJEV_SOURCE to a local AnyJev checkout to verify native
// game-maze outputs through inspect and temporary snapshot readback.
func TestAnyJevNativeMazeSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the native maze migration check")
	}
	sourceDirectory := filepath.Join(sourceRoot, "bench", "results_games")
	if _, err := os.Stat(sourceDirectory); os.IsNotExist(err) {
		t.Skip("source checkout has no native maze result directory")
	}
	var matchingFiles int
	err := filepath.WalkDir(sourceDirectory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if isAnyJevNativeMazeJSONPath(relative) {
			matchingFiles++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if matchingFiles == 0 {
		t.Skip("source checkout has no native maze result JSON files")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	ready := 0
	for _, file := range manifest.Files {
		if isAnyJevNativeMazeJSONPath(file.Path) && file.Status == "ready" && file.ImportedCount == 1 {
			ready++
		}
	}
	if ready != matchingFiles {
		t.Fatalf("native maze source preflight mismatch: files=%d ready=%d", matchingFiles, ready)
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
	seen := 0
	for _, record := range records {
		if !isAnyJevNativeMazeJSONPath(record.SourcePath) {
			continue
		}
		var payload anyJevNativeMazeImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevNativeMazeResultSchema {
			t.Fatal("native maze source record did not read back in the strict semantic format")
		}
		if record.Type != "decision_label_or_evaluation" || record.Scope != "local_pending_review" ||
			payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatal("native maze source record did not preserve review-only trust status")
		}
		seen++
	}
	if seen != matchingFiles {
		t.Fatalf("native maze source/readback count mismatch: expected=%d actual=%d", matchingFiles, seen)
	}
}
