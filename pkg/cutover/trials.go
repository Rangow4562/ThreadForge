package cutover

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	EvaluationTrialsSchemaVersion = "threadforge.cutover-evaluation-trials.v1"
	minimumBaselineRuns           = 8
	minimumCandidateRuns          = 4
	maximumCandidateRuns          = 8
	maximumEvaluationTasks        = 10000
)

// EvaluationTrials contains only opaque digests and measured outcomes. Task text,
// prompts, paths, model responses, and raw failure messages are not admissible.
type EvaluationTrials struct {
	SchemaVersion    string           `json:"schema_version"`
	Domain           string           `json:"domain"`
	LegacyVersion    string           `json:"legacy_version"`
	LegacyCommit     string           `json:"legacy_commit"`
	DatasetSHA256    string           `json:"dataset_sha256"`
	HeldOut          bool             `json:"held_out"`
	Evaluator        Evaluator        `json:"evaluator"`
	NoiseCalibration NoiseCalibration `json:"noise_calibration"`
	BaselineRuns     []EvaluationRun  `json:"baseline_runs"`
	CandidateRuns    []EvaluationRun  `json:"candidate_runs"`
}

type Evaluator struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type NoiseCalibration struct {
	Method             string  `json:"method"`
	Seed               uint32  `json:"seed"`
	BootstrapResamples int     `json:"bootstrap_resamples"`
	ConfidenceLevel    float64 `json:"confidence_level"`
}

type EvaluationRun struct {
	RunID string        `json:"run_id"`
	Tasks []TaskOutcome `json:"tasks"`
}

type TaskOutcome struct {
	TaskSHA256             string  `json:"task_sha256"`
	Success                bool    `json:"success"`
	FailureSignatureSHA256 string  `json:"failure_signature_sha256,omitempty"`
	CostUSD                float64 `json:"cost_usd"`
	Tokens                 int64   `json:"tokens"`
}

type EvaluationMetrics struct {
	TaskCount                  int     `json:"task_count"`
	TaskSetSHA256              string  `json:"task_set_sha256"`
	BaselineRuns               int     `json:"baseline_runs"`
	CandidateRuns              int     `json:"candidate_runs"`
	BaselineRepeatFailureRate  float64 `json:"baseline_repeat_failure_rate"`
	CandidateRepeatFailureRate float64 `json:"candidate_repeat_failure_rate"`
	NoiseFloor                 float64 `json:"noise_floor"`
	BaselineTaskSuccessRate    float64 `json:"baseline_task_success_rate"`
	CandidateTaskSuccessRate   float64 `json:"candidate_task_success_rate"`
	BaselineCostUSD            float64 `json:"baseline_cost_usd"`
	CandidateCostUSD           float64 `json:"candidate_cost_usd"`
	BaselineTokens             int64   `json:"baseline_tokens"`
	CandidateTokens            int64   `json:"candidate_tokens"`
}

func (v *artifactVerifier) readEvaluationTrials(evidence Evidence) (EvaluationTrials, error) {
	var trials EvaluationTrials
	content, actual, err := v.readEvidence(evidence)
	if err != nil {
		return trials, err
	}
	if actual != evidence.SHA256 {
		return trials, errors.New("artifact digest mismatch")
	}
	if err := decodeUniqueJSON(content, &trials); err != nil {
		return trials, fmt.Errorf("invalid evaluation trials artifact: %w", err)
	}
	return trials, nil
}

// ReadEvaluationTrialsArtifact loads a trials artifact through the same bounded,
// path-safe digest verifier used by cutover assessment.
func ReadEvaluationTrialsArtifact(evidenceRoot string, evidence Evidence) (EvaluationTrials, error) {
	var trials EvaluationTrials
	root, err := checkedEvidenceRoot(evidenceRoot)
	if err != nil {
		return trials, err
	}
	verifier := &artifactVerifier{root: root, verified: make(map[string]string)}
	return verifier.readEvaluationTrials(evidence)
}

// ValidateEvaluationTrialBinding recomputes all trial metrics and checks that
// the selected report evaluation names the same held-out artifact and metrics.
// It validates artifact/report consistency only; it does not verify that any
// benchmark was actually executed.
func ValidateEvaluationTrialBinding(evaluation Evaluation, trials EvaluationTrials) (EvaluationMetrics, error) {
	metrics, err := DeriveEvaluationMetrics(trials)
	if err != nil {
		return metrics, err
	}
	if trials.Domain != evaluation.Domain || trials.LegacyVersion != evaluation.LegacyVersion ||
		trials.LegacyCommit != evaluation.LegacyCommit || trials.DatasetSHA256 != evaluation.DatasetSHA256 ||
		trials.HeldOut != evaluation.HeldOut || !evaluation.HeldOut ||
		metrics.TaskCount != evaluation.TaskCount || metrics.TaskSetSHA256 != evaluation.TaskSetSHA256 ||
		metrics.BaselineRuns != evaluation.BaselineRuns || metrics.CandidateRuns != evaluation.CandidateRuns {
		return EvaluationMetrics{}, errors.New("evaluation identity or run counts do not match its outcome artifact")
	}
	if !closeMetric(metrics.BaselineRepeatFailureRate, evaluation.BaselineRepeatFailureRate) ||
		!closeMetric(metrics.CandidateRepeatFailureRate, evaluation.CandidateRepeatFailureRate) ||
		!closeMetric(metrics.NoiseFloor, evaluation.NoiseFloor) ||
		!closeMetric(metrics.BaselineTaskSuccessRate, evaluation.BaselineTaskSuccessRate) ||
		!closeMetric(metrics.CandidateTaskSuccessRate, evaluation.CandidateTaskSuccessRate) ||
		!closeMetric(metrics.BaselineCostUSD, evaluation.BaselineCostUSD) ||
		!closeMetric(metrics.CandidateCostUSD, evaluation.CandidateCostUSD) ||
		metrics.BaselineTokens != evaluation.BaselineTokens || metrics.CandidateTokens != evaluation.CandidateTokens {
		return EvaluationMetrics{}, errors.New("declared metrics do not match its outcome artifact")
	}
	return metrics, nil
}

// DeriveEvaluationMetrics validates the task/run artifact and derives every
// acceptance metric. Baseline/candidate runs are ordered; a repeated failure is
// the same task and failure-signature digest recurring after its first occurrence
// in that treatment's run sequence. The noise floor is the configured quantile
// of a paired task bootstrap with independent baseline-run resampling.
func DeriveEvaluationMetrics(trials EvaluationTrials) (EvaluationMetrics, error) {
	var metrics EvaluationMetrics
	if trials.SchemaVersion != EvaluationTrialsSchemaVersion || strings.TrimSpace(trials.Domain) == "" || len(trials.Domain) > 128 {
		return metrics, errors.New("unsupported schema version or invalid domain")
	}
	if strings.TrimSpace(trials.LegacyVersion) == "" || len(trials.LegacyVersion) > 128 ||
		privacy.ContainsCredentialText(trials.LegacyVersion) || !gitCommitRE.MatchString(trials.LegacyCommit) {
		return metrics, errors.New("legacy version/commit identity is invalid")
	}
	if !digestRE.MatchString(trials.DatasetSHA256) || !trials.HeldOut {
		return metrics, errors.New("a pinned held-out dataset is required")
	}
	if strings.TrimSpace(trials.Evaluator.ID) == "" || len(trials.Evaluator.ID) > 128 ||
		strings.TrimSpace(trials.Evaluator.Version) == "" || len(trials.Evaluator.Version) > 128 ||
		privacy.ContainsCredentialText(trials.Evaluator.ID) || privacy.ContainsCredentialText(trials.Evaluator.Version) ||
		!digestRE.MatchString(trials.Evaluator.SHA256) {
		return metrics, errors.New("evaluator identity and digest are required")
	}
	if !validNoiseCalibrationMethod(trials.NoiseCalibration.Method) ||
		trials.NoiseCalibration.BootstrapResamples < minimumBootstrapResamples ||
		trials.NoiseCalibration.BootstrapResamples > maximumBootstrapResamples ||
		!positiveFinite(trials.NoiseCalibration.ConfidenceLevel) ||
		trials.NoiseCalibration.ConfidenceLevel < 0.95 || trials.NoiseCalibration.ConfidenceLevel > 0.99 {
		return metrics, errors.New("noise calibration must use paired task/run bootstrap, 100..10000 resamples, and confidence 0.95..0.99")
	}
	if len(trials.BaselineRuns) != minimumBaselineRuns || len(trials.CandidateRuns) < minimumCandidateRuns || len(trials.CandidateRuns) > maximumCandidateRuns {
		return metrics, fmt.Errorf("requires exactly %d baseline runs and %d..%d candidate runs", minimumBaselineRuns, minimumCandidateRuns, maximumCandidateRuns)
	}
	taskCount, err := validateRunGroup(trials.BaselineRuns, nil, "baseline")
	if err != nil {
		return metrics, err
	}
	if taskCount < 1 || taskCount > maximumEvaluationTasks {
		return metrics, fmt.Errorf("task count must be within 1..%d", maximumEvaluationTasks)
	}
	if int64(taskCount)*int64(trials.NoiseCalibration.BootstrapResamples) > maximumBootstrapWork {
		return metrics, errors.New("task_count * bootstrap_resamples exceeds 1500000")
	}
	if _, err := validateRunGroup(trials.CandidateRuns, trials.BaselineRuns, "candidate"); err != nil {
		return metrics, err
	}
	baselineRepeat, baselineSuccess, baselineCost, baselineTokens, err := summarizeRuns(trials.BaselineRuns, taskCount)
	if err != nil {
		return metrics, fmt.Errorf("baseline: %w", err)
	}
	candidateRepeat, candidateSuccess, candidateCost, candidateTokens, err := summarizeRuns(trials.CandidateRuns, taskCount)
	if err != nil {
		return metrics, fmt.Errorf("candidate: %w", err)
	}
	if baselineCost <= 0 || baselineTokens <= 0 {
		return metrics, errors.New("baseline measured cost and token totals must be positive")
	}
	metrics = EvaluationMetrics{
		TaskCount: taskCount, TaskSetSHA256: taskSetDigest(trials.BaselineRuns[0].Tasks), BaselineRuns: len(trials.BaselineRuns), CandidateRuns: len(trials.CandidateRuns),
		BaselineRepeatFailureRate: baselineRepeat, CandidateRepeatFailureRate: candidateRepeat,
		NoiseFloor:              pairedBootstrapNoiseFloor(trials.BaselineRuns, taskCount, len(trials.CandidateRuns), trials.NoiseCalibration),
		BaselineTaskSuccessRate: baselineSuccess, CandidateTaskSuccessRate: candidateSuccess,
		BaselineCostUSD: baselineCost, CandidateCostUSD: candidateCost,
		BaselineTokens: baselineTokens, CandidateTokens: candidateTokens,
	}
	return metrics, nil
}

func taskSetDigest(tasks []TaskOutcome) string {
	ids := make([]string, len(tasks))
	for index, task := range tasks {
		ids[index] = task.TaskSHA256
	}
	sort.Strings(ids)
	hash := sha256.Sum256([]byte(strings.Join(ids, "\n") + "\n"))
	return hex.EncodeToString(hash[:])
}

func validateRunGroup(runs []EvaluationRun, other []EvaluationRun, label string) (int, error) {
	var taskCount int
	seenRunIDs := make(map[string]bool, len(runs)+len(other))
	for _, run := range other {
		seenRunIDs[run.RunID] = true
	}
	var expectedTasks map[string]bool
	for runIndex, run := range runs {
		if !digestRE.MatchString(run.RunID) || seenRunIDs[run.RunID] {
			return 0, fmt.Errorf("%s run %d has an invalid or duplicate run_id", label, runIndex)
		}
		seenRunIDs[run.RunID] = true
		if runIndex == 0 {
			taskCount = len(run.Tasks)
			if taskCount == 0 || taskCount > maximumEvaluationTasks {
				return 0, fmt.Errorf("%s task count must be within 1..%d", label, maximumEvaluationTasks)
			}
			expectedTasks = make(map[string]bool, taskCount)
		}
		if len(run.Tasks) != taskCount {
			return 0, fmt.Errorf("%s run %d has a different task count", label, runIndex)
		}
		currentTasks := make(map[string]bool, taskCount)
		for taskIndex, outcome := range run.Tasks {
			if !digestRE.MatchString(outcome.TaskSHA256) || currentTasks[outcome.TaskSHA256] {
				return 0, fmt.Errorf("%s run %d task %d has an invalid or duplicate task digest", label, runIndex, taskIndex)
			}
			currentTasks[outcome.TaskSHA256] = true
			if !nonnegativeFinite(outcome.CostUSD) || outcome.CostUSD > 1e12 || outcome.Tokens < 0 {
				return 0, fmt.Errorf("%s run %d task %d has invalid cost or token usage", label, runIndex, taskIndex)
			}
			if outcome.Success {
				if outcome.FailureSignatureSHA256 != "" {
					return 0, fmt.Errorf("%s run %d successful task contains a failure signature", label, runIndex)
				}
			} else if !digestRE.MatchString(outcome.FailureSignatureSHA256) {
				return 0, fmt.Errorf("%s run %d failed task lacks an opaque failure-signature digest", label, runIndex)
			}
		}
		if runIndex == 0 {
			expectedTasks = currentTasks
		} else if !sameSet(expectedTasks, currentTasks) {
			return 0, fmt.Errorf("%s runs do not use the same task set", label)
		}
	}
	if len(other) > 0 && taskCount > 0 {
		otherTaskCount := len(other[0].Tasks)
		if taskCount != otherTaskCount {
			return 0, errors.New("baseline and candidate task counts differ")
		}
		otherTasks := make(map[string]bool, otherTaskCount)
		for _, task := range other[0].Tasks {
			otherTasks[task.TaskSHA256] = true
		}
		if !sameSet(expectedTasks, otherTasks) {
			return 0, errors.New("baseline and candidate task sets differ")
		}
	}
	return taskCount, nil
}

func sameSet(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if !right[key] {
			return false
		}
	}
	return true
}

func summarizeRuns(runs []EvaluationRun, taskCount int) (float64, float64, float64, int64, error) {
	seenFailures := make(map[string]map[string]bool)
	var repeatCount, successes int64
	var totalCost float64
	var totalTokens int64
	var repeatedObservations int64
	for runIndex, run := range runs {
		for _, outcome := range run.Tasks {
			if outcome.Success {
				successes++
			} else {
				failures := seenFailures[outcome.TaskSHA256]
				if failures != nil && failures[outcome.FailureSignatureSHA256] {
					repeatCount++
				}
				if failures == nil {
					failures = make(map[string]bool)
					seenFailures[outcome.TaskSHA256] = failures
				}
				failures[outcome.FailureSignatureSHA256] = true
			}
			if totalTokens > math.MaxInt64-outcome.Tokens {
				return 0, 0, 0, 0, errors.New("token total overflows int64")
			}
			totalTokens += outcome.Tokens
			totalCost += outcome.CostUSD
			if !positiveOrZeroFinite(totalCost) {
				return 0, 0, 0, 0, errors.New("cost total is not finite")
			}
		}
		if runIndex > 0 {
			repeatedObservations += int64(taskCount)
		}
	}
	var rate float64
	if repeatedObservations > 0 {
		rate = float64(repeatCount) / float64(repeatedObservations)
	}
	observations := int64(len(runs)) * int64(taskCount)
	successRate := float64(successes) / float64(observations)
	return rate, successRate, totalCost, totalTokens, nil
}

const (
	legacyNoiseCalibrationMethod       = "paired_task_and_independent_run_bootstrap_null"
	noiseCalibrationMethod             = "paired_task_and_independent_run_bootstrap_null_v2"
	minimumBootstrapResamples          = 100
	maximumBootstrapResamples          = 10000
	maximumBootstrapWork         int64 = 1500000
)

func pairedBootstrapNoiseFloor(runs []EvaluationRun, taskCount, comparisonRuns int, calibration NoiseCalibration) float64 {
	rng := splitMix64{state: uint64(calibration.Seed)}
	differences := make([]float64, calibration.BootstrapResamples)
	taskSample := make([]int, taskCount)
	baselineSampleCount := comparisonRuns
	if calibration.Method == noiseCalibrationMethod {
		// V2 preserves the estimator's actual unequal run counts: the baseline
		// metric uses every baseline run, while the null candidate has comparisonRuns.
		baselineSampleCount = len(runs)
	}
	runSampleA := make([]int, baselineSampleCount)
	runSampleB := make([]int, comparisonRuns)
	for sample := range differences {
		for i := range taskSample {
			taskSample[i] = int(rng.next() % uint64(taskCount))
		}
		for i := range runSampleA {
			runSampleA[i] = int(rng.next() % uint64(len(runs)))
		}
		for i := range runSampleB {
			runSampleB[i] = int(rng.next() % uint64(len(runs)))
		}
		rateA := sampledRepeatRate(runs, runSampleA, taskSample)
		rateB := sampledRepeatRate(runs, runSampleB, taskSample)
		differences[sample] = math.Abs(rateA - rateB)
	}
	sort.Float64s(differences)
	index := int(math.Ceil(calibration.ConfidenceLevel*float64(len(differences)))) - 1
	return differences[index]
}

func validNoiseCalibrationMethod(method string) bool {
	return method == legacyNoiseCalibrationMethod || method == noiseCalibrationMethod
}

func sampledRepeatRate(runs []EvaluationRun, sampledRuns, sampledTasks []int) float64 {
	if len(sampledRuns) < 2 || len(sampledTasks) == 0 {
		return 0
	}
	var repeated int64
	for _, taskIndex := range sampledTasks {
		// DeriveEvaluationMetrics bounds both the comparison-run sample and the
		// baseline run set to eight. Keep the seen failures in a small stack array
		// rather than allocating a map for every sampled task in every resample.
		var seen [minimumBaselineRuns]string
		seenCount := 0
		for _, runIndex := range sampledRuns {
			outcome := runs[runIndex].Tasks[taskIndex]
			if outcome.Success {
				continue
			}
			found := false
			for i := 0; i < seenCount; i++ {
				if seen[i] == outcome.FailureSignatureSHA256 {
					found = true
					break
				}
			}
			if found {
				repeated++
			} else {
				seen[seenCount] = outcome.FailureSignatureSHA256
				seenCount++
			}
		}
	}
	denominator := int64(len(sampledTasks)) * int64(len(sampledRuns)-1)
	return float64(repeated) / float64(denominator)
}

type splitMix64 struct{ state uint64 }

func (r *splitMix64) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	value := r.state
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func closeMetric(left, right float64) bool {
	if !positiveOrZeroFinite(left) || !positiveOrZeroFinite(right) {
		return false
	}
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-9*scale
}

func positiveOrZeroFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
