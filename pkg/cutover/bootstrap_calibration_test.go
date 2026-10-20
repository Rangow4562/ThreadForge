package cutover

import (
	"fmt"
	"math"
	"sort"
	"testing"
)

func TestSampledRepeatRateCountsEachRecurrenceAndPreservesSampleOrder(t *testing.T) {
	runs := make([]EvaluationRun, 8)
	failures := []string{"a", "a", "", "b", "b", "a", "", "c"}
	for i, failure := range failures {
		outcome := TaskOutcome{Success: failure == ""}
		if failure != "" {
			outcome.FailureSignatureSHA256 = failure
		}
		runs[i].Tasks = []TaskOutcome{outcome}
	}
	indices := []int{0, 1, 2, 3, 4, 5, 6, 7}
	if got, want := sampledRepeatRate(runs, indices, []int{0}), 3.0/7.0; got != want {
		t.Fatalf("repeat rate = %g, want %g", got, want)
	}
	if got, want := sampledRepeatRate(runs, indices, []int{0, 0}), 3.0/7.0; got != want {
		t.Fatalf("paired task-resample repeat rate = %g, want %g", got, want)
	}
	reversed := []int{7, 6, 5, 4, 3, 2, 1, 0}
	if got, want := sampledRepeatRate(runs, reversed, []int{0}), 3.0/7.0; got != want {
		t.Fatalf("reordered run sample repeat rate = %g, want %g", got, want)
	}
}

func TestPairedBootstrapNoiseFloorEmpiricalNullCalibration(t *testing.T) {
	const (
		simulations      = 1000
		taskCount        = 24
		bootstrapResamps = 500
		maxFalsePositive = 0.05
	)
	scenarios := []struct {
		name       string
		minFailure float64
		maxFailure float64
		runShock   float64
		seed       uint64
	}{
		{name: "heterogeneous-task-difficulty", minFailure: 0.03, maxFailure: 0.78, seed: 0x421901},
		{name: "heterogeneous-tasks-with-shared-run-shocks", minFailure: 0.03, maxFailure: 0.72, runShock: 0.40, seed: 0x9e3779},
	}
	for _, scenario := range scenarios {
		for _, candidateRuns := range []int{minimumCandidateRuns, maximumCandidateRuns} {
			t.Run(fmt.Sprintf("%s/%d-candidate-runs", scenario.name, candidateRuns), func(t *testing.T) {
				rng := splitMix64{state: scenario.seed + uint64(candidateRuns)}
				falsePositives := 0
				for simulation := 0; simulation < simulations; simulation++ {
					baseline, candidate := simulatedEvaluationRuns(&rng, taskCount, candidateRuns, scenario.minFailure, scenario.maxFailure, scenario.runShock, 0)
					baselineRate, _, _, _, err := summarizeRuns(baseline, taskCount)
					if err != nil {
						t.Fatal(err)
					}
					candidateRate, _, _, _, err := summarizeRuns(candidate, taskCount)
					if err != nil {
						t.Fatal(err)
					}
					calibration := NoiseCalibration{
						Method: noiseCalibrationMethod, Seed: uint32(rng.next()),
						BootstrapResamples: bootstrapResamps, ConfidenceLevel: 0.95,
					}
					floor := pairedBootstrapNoiseFloor(baseline, taskCount, candidateRuns, calibration)
					if baselineRate-candidateRate > floor {
						falsePositives++
					}
				}
				rate := float64(falsePositives) / simulations
				t.Logf("null false-positive rate %d/%d = %.3f (limit %.3f)", falsePositives, simulations, rate, maxFalsePositive)
				if rate > maxFalsePositive {
					t.Fatalf("paired-bootstrap threshold is too permissive under the simulated null: %.3f > %.3f", rate, maxFalsePositive)
				}
			})
		}
	}
}

func TestPairedBootstrapDetectsClearRepeatFailureReduction(t *testing.T) {
	const (
		simulations        = 500
		taskCount          = 24
		bootstrapResamps   = 300
		candidateReduction = 0.30
		minimumPower       = 0.80
	)
	for _, candidateRuns := range []int{minimumCandidateRuns, maximumCandidateRuns} {
		t.Run(fmt.Sprintf("%d-candidate-runs", candidateRuns), func(t *testing.T) {
			rng := splitMix64{state: 0x6a09e667f3bcc909 + uint64(candidateRuns)}
			detections := 0
			for simulation := 0; simulation < simulations; simulation++ {
				baseline, candidate := simulatedEvaluationRuns(&rng, taskCount, candidateRuns, 0.10, 0.82, 0.30, candidateReduction)
				baselineRepeat, baselineSuccess, _, _, err := summarizeRuns(baseline, taskCount)
				if err != nil {
					t.Fatal(err)
				}
				candidateRepeat, candidateSuccess, _, _, err := summarizeRuns(candidate, taskCount)
				if err != nil {
					t.Fatal(err)
				}
				calibration := NoiseCalibration{
					Method: noiseCalibrationMethod, Seed: uint32(rng.next()),
					BootstrapResamples: bootstrapResamps, ConfidenceLevel: 0.95,
				}
				floor := pairedBootstrapNoiseFloor(baseline, taskCount, candidateRuns, calibration)
				if baselineRepeat-candidateRepeat > floor && candidateSuccess >= baselineSuccess {
					detections++
				}
			}
			power := float64(detections) / simulations
			t.Logf("detection rate for a %.2f absolute per-task failure-probability reduction = %d/%d = %.3f", candidateReduction, detections, simulations, power)
			if power < minimumPower {
				t.Fatalf("paired-bootstrap gate missed too many clear improvements: power %.3f < %.3f", power, minimumPower)
			}
		})
	}
}

func TestLegacyNoiseCalibrationMethodRetainsOriginalBootstrapSemantics(t *testing.T) {
	trials := fixtureTrials(t, "legacy-bootstrap-method")
	trials.NoiseCalibration.Method = legacyNoiseCalibrationMethod
	metrics, err := DeriveEvaluationMetrics(trials)
	if err != nil {
		t.Fatal(err)
	}
	want := legacyEqualRunNoiseFloor(trials.BaselineRuns, len(trials.BaselineRuns[0].Tasks), len(trials.CandidateRuns), trials.NoiseCalibration)
	if metrics.NoiseFloor != want {
		t.Fatalf("legacy method noise floor = %.12f, want original equal-sized bootstrap result %.12f", metrics.NoiseFloor, want)
	}
}

func legacyEqualRunNoiseFloor(runs []EvaluationRun, taskCount, comparisonRuns int, calibration NoiseCalibration) float64 {
	rng := splitMix64{state: uint64(calibration.Seed)}
	differences := make([]float64, calibration.BootstrapResamples)
	taskSample := make([]int, taskCount)
	runSampleA := make([]int, comparisonRuns)
	runSampleB := make([]int, comparisonRuns)
	for sample := range differences {
		for i := range taskSample {
			taskSample[i] = int(rng.next() % uint64(taskCount))
		}
		for i := range runSampleA {
			runSampleA[i] = int(rng.next() % uint64(len(runs)))
			runSampleB[i] = int(rng.next() % uint64(len(runs)))
		}
		differences[sample] = math.Abs(sampledRepeatRate(runs, runSampleA, taskSample) - sampledRepeatRate(runs, runSampleB, taskSample))
	}
	sort.Float64s(differences)
	index := int(math.Ceil(calibration.ConfidenceLevel*float64(len(differences)))) - 1
	return differences[index]
}

func simulatedEvaluationRuns(rng *splitMix64, taskCount, candidateRuns int, minimumFailure, maximumFailure, runShock, candidateReduction float64) ([]EvaluationRun, []EvaluationRun) {
	probabilities := make([]float64, taskCount)
	for task := range probabilities {
		probabilities[task] = minimumFailure + (maximumFailure-minimumFailure)*unitFloat(rng)
	}
	allRuns := make([]EvaluationRun, minimumBaselineRuns+candidateRuns)
	for runIndex := range allRuns {
		shock := (unitFloat(rng) - 0.5) * runShock
		run := EvaluationRun{Tasks: make([]TaskOutcome, taskCount)}
		for taskIndex, probability := range probabilities {
			adjusted := probability + shock
			if runIndex >= minimumBaselineRuns {
				adjusted -= candidateReduction
			}
			if adjusted < 0 {
				adjusted = 0
			} else if adjusted > 1 {
				adjusted = 1
			}
			failureDigest := fmt.Sprintf("%064x", taskIndex+1)
			failed := unitFloat(rng) < adjusted
			outcome := TaskOutcome{TaskSHA256: fmt.Sprintf("%064x", taskIndex+1), Success: !failed, CostUSD: 0.1, Tokens: 10}
			if failed {
				outcome.FailureSignatureSHA256 = failureDigest
			}
			run.Tasks[taskIndex] = outcome
		}
		allRuns[runIndex] = run
	}
	return allRuns[:minimumBaselineRuns], allRuns[minimumBaselineRuns:]
}

func unitFloat(rng *splitMix64) float64 {
	return float64(rng.next()>>11) / (1 << 53)
}
