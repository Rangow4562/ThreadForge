package migrate

import (
	"encoding/json"
	"strings"
	"testing"
)

func anyJevDemoGamePolicySummary(decisions float64) map[string]any {
	return map[string]any{
		"decisions": decisions, "agree": 0.6, "regret": 0.2, "ece": 0.1, "brier": 0.4,
		"flip": 0.05, "illegal_mass": 0.01,
	}
}

func anyJev2048Fixture() map[string]any {
	levels := []string{"raw", "L0"}
	policyLabels := []string{"raw · Qwen3-8B", "L0 · Qwen3-8B"}
	runs := []any{
		map[string]any{
			"seed": float64(1), "steps": float64(5),
			policyLabels[0]: map[string]any{"score": float64(100), "max_tile": float64(16), "moves": float64(3)},
			policyLabels[1]: map[string]any{"score": float64(200), "max_tile": float64(32), "moves": float64(4)},
		},
		map[string]any{
			"seed": float64(2), "steps": float64(6),
			policyLabels[0]: map[string]any{"score": float64(200), "max_tile": float64(32), "moves": float64(2)},
			policyLabels[1]: map[string]any{"score": float64(300), "max_tile": float64(64), "moves": float64(4)},
		},
	}
	baselines := map[string]any{}
	for index, name := range []string{"random", "first-listed", "oracle"} {
		baselines[name] = []any{
			map[string]any{"score": float64(100 + index), "max_tile": float64(16), "moves": float64(5)},
			map[string]any{"score": float64(200 + index), "max_tile": float64(32), "moves": float64(6)},
		}
	}
	return map[string]any{
		"game": "2048", "model": "Qwen/Qwen3-8B", "levels": []any{levels[0], levels[1]},
		"preview": true, "oracle_depth": float64(2), "games": runs, "baselines": baselines,
		"summary": map[string]any{
			policyLabels[0]: anyJevDemoGamePolicySummary(5),
			policyLabels[1]: anyJevDemoGamePolicySummary(8),
		},
	}
}

func anyJevMinesweeperFixture() map[string]any {
	levels := []string{"raw", "L1"}
	policyLabels := []string{"raw · Qwen3-8B", "L1 · Qwen3-8B"}
	runs := []any{
		map[string]any{
			"seed":          float64(11),
			policyLabels[0]: map[string]any{"won": true, "cleared": 1.0, "turns": float64(3), "death": nil},
			policyLabels[1]: map[string]any{"won": false, "cleared": 0.5, "turns": float64(4), "death": map[string]any{"turn": 4, "private_cell": "A4"}},
		},
		map[string]any{
			"seed":          float64(12),
			policyLabels[0]: map[string]any{"won": false, "cleared": 0.5, "turns": float64(2), "death": nil},
			policyLabels[1]: map[string]any{"won": true, "cleared": 1.0, "turns": float64(5), "death": nil},
		},
	}
	return map[string]any{
		"game": "minesweeper", "model": "Qwen/Qwen3-8B", "levels": []any{levels[0], levels[1]},
		"board": []any{float64(8), float64(8), float64(10)}, "games": runs,
		"baselines": map[string]any{
			"random": []any{
				map[string]any{"won": false, "cleared": 0.25, "turns": float64(2)},
				map[string]any{"won": true, "cleared": 1.0, "turns": float64(4)},
			},
			"oracle": []any{
				map[string]any{"won": true, "cleared": 1.0, "turns": float64(3)},
				map[string]any{"won": true, "cleared": 1.0, "turns": float64(5)},
			},
		},
		"summary": map[string]any{
			policyLabels[0]: map[string]any{"decisions": float64(5), "agree": 0.8, "regret": 0.1, "ece": 0.2, "brier": 0.3},
			policyLabels[1]: map[string]any{"decisions": float64(9), "agree": 0.7, "regret": 0.2, "ece": 0.25, "brier": 0.4},
		},
	}
}

func TestAnyJevDemoGameResultsNormalizeAndRedact(t *testing.T) {
	tests := []struct {
		path string
		root map[string]any
		game string
	}{
		{"demo/results/twenty48_private.json", anyJev2048Fixture(), "2048"},
		{"demo/results/minesweeper_private.json", anyJevMinesweeperFixture(), "minesweeper"},
	}
	for _, test := range tests {
		t.Run(test.game, func(t *testing.T) {
			item, err := normalizeAnyJevDemoGameResult(test.path, test.root)
			if err != nil {
				t.Fatal(err)
			}
			var payload anyJevDemoGameImport
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if item.Type != "decision_label_or_evaluation" || payload.Game != test.game ||
				payload.ModelSHA256 != anyJevStringSHA256("Qwen/Qwen3-8B") || len(payload.Levels) != 2 ||
				payload.Episodes != 2 || !payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
				t.Fatalf("unexpected demo-game projection: %#v", payload)
			}
			encoded := string(item.Payload)
			for _, privateValue := range []string{"Qwen/Qwen3-8B", "Qwen3-8B", "private_cell", "A4"} {
				if strings.Contains(encoded, privateValue) {
					t.Fatalf("demo-game projection retained %q", privateValue)
				}
			}
			if test.game == "2048" {
				if payload.Levels[0].Decisions != 5 || payload.Levels[0].OutcomeSummaries["mean_score"] != 150 ||
					payload.Levels[0].OutcomeSummaries["score_population_stddev"] != 50 || payload.OracleDepth != 2 ||
					payload.Preview == nil || !*payload.Preview || len(payload.Baselines) != 3 {
					t.Fatalf("2048 projection did not reconcile: %#v", payload)
				}
			} else if payload.Levels[0].OutcomeSummaries["win_rate"] != 0.5 ||
				payload.Levels[0].OutcomeSummaries["deaths"] != 0 || len(payload.Board) != 3 || len(payload.Baselines) != 2 {
				t.Fatalf("Minesweeper projection did not reconcile: %#v", payload)
			}
		})
	}
}

func TestAnyJevDemoGameRejectsUnknownOrInconsistentSourceData(t *testing.T) {
	tests := map[string]func(map[string]any){
		"unknown root field": func(root map[string]any) { root["private_path"] = "C:/secret" },
		"decision count mismatch": func(root map[string]any) {
			root["summary"].(map[string]any)["raw · Qwen3-8B"].(map[string]any)["decisions"] = float64(999)
		},
		"unexpected episode policy": func(root map[string]any) {
			root["games"].([]any)[0].(map[string]any)["unexpected · Qwen3-8B"] = map[string]any{"score": 1.0, "max_tile": float64(2), "moves": float64(1)}
		},
		"unknown outcome value": func(root map[string]any) {
			root["games"].([]any)[0].(map[string]any)["raw · Qwen3-8B"].(map[string]any)["prompt"] = "secret"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			root := anyJev2048Fixture()
			mutate(root)
			if _, err := normalizeAnyJevDemoGameResult("demo/results/twenty48_fixture.json", root); err == nil {
				t.Fatal("invalid demo-game result was accepted")
			}
		})
	}
}

func TestAnyJevDemoGamePathIsNarrow(t *testing.T) {
	for _, path := range []string{
		"demo/results/twenty48_model.json",
		"demo/results/minesweeper_model.json",
	} {
		if !isAnyJevDemoGameJSONPath(path) {
			t.Errorf("supported path was not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"demo/results/jev_mode_model.json",
		"demo/results/nested/twenty48_model.json",
		"bench/results/twenty48_model.json",
		"demo/results/twenty48_.json",
	} {
		if isAnyJevDemoGameJSONPath(path) {
			t.Errorf("unsupported path was recognized: %s", path)
		}
	}
}
