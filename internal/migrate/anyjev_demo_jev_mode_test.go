package migrate

import (
	"encoding/json"
	"strings"
	"testing"
)

func anyJevDemoTypedLevelFixture(accuracy float64, readout string, examples float64) map[string]any {
	return map[string]any{
		"acc": accuracy, "ece": 0.1, "n": examples, "prompts_per_decision": float64(1),
		"blocks_executed": float64(24), "n_blocks": float64(36), "ms_per_decision": 3.5, "readout": readout,
	}
}

func anyJevDemoTypedQuestionFixture() map[string]any {
	return map[string]any{
		"kind": "choice", "K": float64(4), "n": float64(10), "shipped_head": false,
		"L0": anyJevDemoTypedLevelFixture(0.5, "L0", 10),
		"L2": anyJevDemoTypedLevelFixture(0.7, "head:lda", 10),
	}
}

func anyJevDemoTypedRouteFixture(question string) map[string]any {
	return map[string]any{
		"reworded_text": "Private reworded prompt", "exact": 0.7, "routed_from": question,
		"as_is":       map[string]any{"acc": 0.65, "flip_vs_exact": 0.1, "routed_from": question},
		"recentred":   map[string]any{"acc": 0.68, "flip_vs_exact": 0.05, "routed_from": question, "adapted": true, "adapt_n": float64(20)},
		"reversed":    map[string]any{"acc": 0.6, "flip_vs_exact": 0.2, "reordered": true},
		"l0_reworded": 0.55, "variant": "w2",
	}
}

func anyJevDemoTypedFitFixture() map[string]any {
	return map[string]any{
		"n_labels": float64(5), "fit_seconds": 0.5, "method": "head:lda", "layer_abs": float64(24),
		"oof_acc": 0.7, "acc": 0.75, "acc_shipped": nil, "acc_l0": 0.5,
	}
}

func anyJevDemoTypedFixture() map[string]any {
	questionNames := []string{"private.question.one", "private.question.two"}
	levels := map[string]any{}
	routing := map[string]any{}
	fit := map[string]any{}
	for _, name := range questionNames {
		levels[name] = anyJevDemoTypedQuestionFixture()
		routing[name] = anyJevDemoTypedRouteFixture(name)
		fit[name] = anyJevDemoTypedFitFixture()
	}
	levels["pooled"] = map[string]any{
		"n": float64(20), "L0": map[string]any{"acc": 0.5, "ece": 0.1},
		"L2": map[string]any{"acc": 0.7, "ece": 0.08},
	}
	routing["pooled"] = map[string]any{
		"exact": 0.7, "as_is": 0.65, "recentred": 0.68, "reversed": 0.6, "l0_reworded": 0.55,
	}
	fit["pooled"] = map[string]any{"acc": 0.75, "ece": 0.05, "acc_shipped": nil}
	return map[string]any{
		"backend": "hf", "model": "private/model-label", "device": "cuda", "dtype": "bfloat16",
		"batch_size": float64(32), "heads_file": "C:/private/heads.json", "heads_block": float64(24),
		"n_blocks": float64(36), "n_test": float64(10), "reword": "w2", "adapt_after": float64(20),
		"fit_labels": float64(5), "model_load_seconds": 2.5,
		"levels": levels, "routing": routing, "fit": fit,
		"roundtrip": map[string]any{"n_heads": float64(2), "bytes": float64(1024), "max_abs_dp": 1e-6,
			"n_decisions": float64(20), "auto_without_head": "L0"},
		"seconds": 30.0, "env": map[string]any{"gpu": "private-device"},
	}
}

func anyJevDemoLifecycleFixture() map[string]any {
	fit := map[string]any{"at": float64(30), "acc": 0.75, "ece": 0.08, "level": "L2",
		"method": "head:lda", "layer_abs": float64(24), "oof_acc": 0.72}
	return map[string]any{
		"backend": "hf", "model": "private/model-label", "mode": "lifecycle",
		"question": "private.question.identity", "seed": float64(42), "seconds": 5.5,
		"env": map[string]any{"gpu": "private-device"},
		"lifecycle": map[string]any{
			"day0":   map[string]any{"level": "L0", "acc": 0.5, "n": float64(20)},
			"stream": float64(100), "fit_at": float64(30), "fits": []any{fit},
			"wording": map[string]any{
				"text": "Private lifecycle wording", "first_n": float64(30), "acc_first_unadapted": 0.65,
				"acc_after_adapted": 0.72, "acc_after_control": 0.66,
				"routed_from": "private.question.identity", "adapt_n": float64(40),
			},
			"restart": map[string]any{
				"n_heads": float64(1), "n_observations": float64(100), "n_adaptation": float64(40),
				"bytes": float64(2048), "adapted_on_first_request": true,
				"adapt_n_first": float64(40), "max_abs_dp": 1e-6,
			},
		},
	}
}

func TestAnyJevDemoJevModeResultsNormalizeAndRedact(t *testing.T) {
	tests := []struct {
		name string
		path string
		root map[string]any
		mode string
	}{
		{"typed", "demo/results/jev_mode_private_model.json", anyJevDemoTypedFixture(), "typed"},
		{"lifecycle", "demo/results/jev_mode_lifecycle_private_model.json", anyJevDemoLifecycleFixture(), "lifecycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item, err := normalizeAnyJevDemoJevModeResult(test.path, test.root)
			if err != nil {
				t.Fatal(err)
			}
			var payload anyJevDemoJevModeImport
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if item.Type != "decision_label_or_evaluation" || payload.Mode != test.mode ||
				payload.ModelSHA256 != anyJevStringSHA256("private/model-label") ||
				!payload.HumanReviewRequired || payload.ExecutionVerified || payload.CutoverEligible {
				t.Fatalf("unexpected Jev-mode projection: %#v", payload)
			}
			encoded := string(item.Payload)
			for _, privateValue := range []string{
				"private/model-label", "private.question", "private reworded prompt", "Private lifecycle wording",
				"private-device", "C:/private/heads.json", "private.question.identity",
			} {
				if strings.Contains(encoded, privateValue) {
					t.Fatalf("Jev-mode projection retained %q", privateValue)
				}
			}
			if test.mode == "typed" {
				if payload.Typed == nil || payload.Lifecycle != nil || len(payload.Typed.Questions) != 2 ||
					len(payload.Typed.Routing) != 2 || len(payload.Typed.Fits) != 2 || payload.Typed.RoundTrip.DecisionCount != 20 {
					t.Fatalf("typed Jev-mode projection did not reconcile: %#v", payload.Typed)
				}
			} else if payload.Lifecycle == nil || payload.Typed != nil || payload.Lifecycle.FitAt != 30 ||
				payload.Lifecycle.Wording.AdaptExamples != payload.Lifecycle.Restart.AdaptExamplesOnFirst {
				t.Fatalf("lifecycle Jev-mode projection did not reconcile: %#v", payload.Lifecycle)
			}
		})
	}
}

func TestAnyJevDemoJevModeRejectsUnknownAndInconsistentData(t *testing.T) {
	tests := map[string]func(map[string]any){
		"typed unknown root field": func(root map[string]any) { root["private_path"] = "C:/secret" },
		"typed roundtrip count mismatch": func(root map[string]any) {
			root["roundtrip"].(map[string]any)["n_decisions"] = float64(21)
		},
		"typed L0 metric mismatch": func(root map[string]any) {
			root["fit"].(map[string]any)["private.question.one"].(map[string]any)["acc_l0"] = 0.9
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			root := anyJevDemoTypedFixture()
			mutate(root)
			if _, err := normalizeAnyJevDemoJevModeResult("demo/results/jev_mode_fixture.json", root); err == nil {
				t.Fatal("invalid typed Jev-mode data was accepted")
			}
		})
	}
	root := anyJevDemoLifecycleFixture()
	root["lifecycle"].(map[string]any)["fit_at"] = float64(31)
	if _, err := normalizeAnyJevDemoJevModeResult("demo/results/jev_mode_lifecycle_fixture.json", root); err == nil {
		t.Fatal("lifecycle result without the configured fit checkpoint was accepted")
	}
}

func TestAnyJevDemoJevModePathIsNarrow(t *testing.T) {
	for _, path := range []string{
		"demo/results/jev_mode_model.json",
		"demo/results/jev_mode_lifecycle_model.json",
	} {
		if !isAnyJevDemoJevModeJSONPath(path) {
			t.Errorf("supported path was not recognized: %s", path)
		}
	}
	for _, path := range []string{
		"demo/results/jev_mode_.json",
		"demo/results/nested/jev_mode_model.json",
		"bench/results/jev_mode_model.json",
	} {
		if isAnyJevDemoJevModeJSONPath(path) {
			t.Errorf("unsupported path was recognized: %s", path)
		}
	}
}
