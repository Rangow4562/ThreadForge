package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
)

const (
	rrsiEvaluationImportSchema  = "threadforge.rrsi-evaluation.v1"
	rrsiFrontierImportSchema    = "threadforge.rrsi-frontier.v1"
	rrsiCalibrationImportSchema = "threadforge.rrsi-noise-calibration.v1"
	rrsiHistoryImportSchema     = "threadforge.rrsi-edit-history.v1"
	rrsiAttributionImportSchema = "threadforge.rrsi-attribution.v1"
	rrsiDecisionImportSchema    = "threadforge.rrsi-selection-decision.v1"
)

func normalizeRRSIItems(relative string, value any) ([]normalizedItem, bool, error) {
	if isRRSIProposalPath(relative) {
		domain, _ := rrsiDomain(relative)
		payload, err := normalizeRRSIProposal(domain, value)
		return []normalizedItem{{Type: "evolution_proposal", Payload: payload}}, true, err
	}
	base := strings.ToLower(filepath.Base(relative))
	domain, supportedDomain := rrsiDomain(relative)
	switch base {
	case "eval.json":
		if !supportedDomain {
			return nil, true, errors.New("RRSI evaluation is outside a supported domain directory")
		}
		payload, err := normalizeRRSIEvaluation(domain, value)
		return []normalizedItem{{Type: "rrsi_evaluation_result", Payload: payload}}, true, err
	case "calibration.json":
		if !supportedDomain {
			return nil, true, errors.New("RRSI calibration is outside a supported domain directory")
		}
		payload, err := normalizeRRSICalibration(domain, value)
		return []normalizedItem{{Type: "noise_calibration", Payload: payload}}, true, err
	case "frontier.json":
		if !supportedDomain {
			return nil, true, errors.New("RRSI frontier is outside a supported domain directory")
		}
		payload, err := normalizeRRSIFrontier(domain, value)
		return []normalizedItem{{Type: "evolution_frontier", Payload: payload}}, true, err
	case "history.jsonl":
		if !supportedDomain {
			return nil, true, errors.New("RRSI history is outside a supported domain directory")
		}
		payload, err := normalizeRRSIHistory(domain, value)
		return []normalizedItem{{Type: "rrsi_edit_history", Payload: payload}}, true, err
	case "attribution.jsonl":
		if !supportedDomain {
			return nil, true, errors.New("RRSI attribution is outside a supported domain directory")
		}
		payload, err := normalizeRRSIAttribution(domain, value)
		return []normalizedItem{{Type: "rrsi_attribution", Payload: payload}}, true, err
	case "decisions.json":
		if !supportedDomain {
			return nil, true, errors.New("RRSI selection decision is outside a supported domain directory")
		}
		payload, err := normalizeRRSIDecision(domain, value)
		return []normalizedItem{{Type: "rrsi_selection_decision", Payload: payload}}, true, err
	default:
		return nil, false, nil
	}
}

func rrsiDomain(relative string) (string, bool) {
	parts := strings.Split(strings.ToLower(filepath.ToSlash(relative)), "/")
	for i, part := range parts {
		if part != "runs" || i+1 >= len(parts) {
			continue
		}
		domain := parts[i+1]
		switch domain {
		case "coding", "workspace", "eng":
			return domain, true
		default:
			return "", false
		}
	}
	return "", false
}

func rrsiRunTail(relative string) ([]string, bool) {
	parts := strings.Split(strings.ToLower(filepath.ToSlash(relative)), "/")
	for i, part := range parts {
		if part != "runs" || i+2 > len(parts) {
			continue
		}
		domain := parts[i+1]
		if domain != "coding" && domain != "workspace" && domain != "eng" {
			return nil, false
		}
		return parts[i+2:], true
	}
	return nil, false
}

func rrsiRunTailOriginal(relative string) ([]string, bool) {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for i, part := range parts {
		if !strings.EqualFold(part, "runs") || i+2 > len(parts) {
			continue
		}
		domain := strings.ToLower(parts[i+1])
		if domain != "coding" && domain != "workspace" && domain != "eng" {
			return nil, false
		}
		return parts[i+2:], true
	}
	return nil, false
}

func isRRSIRawTrialArtifactPath(relative string) bool {
	tail, ok := rrsiRunTail(relative)
	if !ok || len(tail) < 3 || tail[0] != "jobs" {
		return false
	}
	return !(len(tail) == 3 && tail[2] == "eval.json")
}

func isRRSIMetadataPath(relative string) bool {
	if isRRSITrialMetadataPath(relative) || isRRSIProposalPath(relative) ||
		isRRSIDeliverablePath(relative) || isRRSICriticPath(relative) || isRRSISmokePath(relative) {
		return true
	}
	tail, ok := rrsiRunTail(relative)
	if !ok {
		return false
	}
	base := strings.ToLower(filepath.Base(relative))
	if len(tail) == 1 {
		switch base {
		case "calibration.json", "frontier.json", "history.jsonl", "attribution.jsonl":
			return true
		}
	}
	if len(tail) == 2 && isRRSIRoundName(tail[0]) && base == "decisions.json" {
		return true
	}
	if len(tail) == 3 && base == "eval.json" {
		if tail[0] == "jobs" || isRRSIRoundName(tail[0]) {
			return true
		}
	}
	return false
}

func isRRSIRoundName(value string) bool {
	if len(value) < 2 || value[0] != 'r' {
		return false
	}
	for _, char := range value[1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isRRSIWorktreeDirectory(relative string) bool {
	tail, ok := rrsiRunTail(relative)
	return ok && len(tail) == 1 && tail[0] == "wt"
}

func normalizeRRSIEvaluation(domain string, value any) (json.RawMessage, error) {
	source, ok := rrsiObject(value)
	if !ok {
		return nil, errors.New("RRSI evaluation must be an object")
	}
	job, jobOK := source["job"].(string)
	k, kOK := rrsiInteger(source["k"])
	score, scoreOK := rrsiNumber(source["S"])
	missing, missingOK := rrsiInteger(source["missing"])
	expected, expectedOK := rrsiInteger(source["n_expected"])
	perTask, perTaskOK := rrsiObject(source["per_task"])
	if !jobOK || strings.TrimSpace(job) == "" || len(job) > 160 || !kOK || k < 1 || k > 100 ||
		!scoreOK || score < 0 || score > 1 || !missingOK || missing < 0 ||
		!expectedOK || expected < 0 || !perTaskOK || len(perTask) == 0 ||
		len(perTask) > maxRecords || expected != len(perTask)*k {
		return nil, errors.New("RRSI evaluation summary is invalid")
	}
	if _, exists := source["C"]; !exists {
		return nil, errors.New("RRSI evaluation token-cost field is missing")
	}
	meanTokens, hasMeanTokens, validCost := rrsiOptionalNumber(source["C"])
	if !validCost || (hasMeanTokens && meanTokens < 0) {
		return nil, errors.New("RRSI evaluation token-cost field is invalid")
	}
	sourceMetrics, metricsOK := rrsiObject(source["extra"])
	if !metricsOK {
		return nil, errors.New("RRSI evaluation domain metrics are invalid")
	}
	taskIDs := make([]string, 0, len(perTask))
	tasks := make(map[string]any, len(perTask))
	totalMissing := 0
	weightedScore, totalWeight := 0.0, 0.0
	observedTokens, totalTokens := 0, 0.0
	for taskID, rawTask := range perTask {
		if strings.TrimSpace(taskID) == "" || len(taskID) > 512 || strings.ContainsRune(taskID, '\x00') {
			return nil, errors.New("RRSI evaluation task identifier is invalid")
		}
		task, ok := rrsiObject(rawTask)
		if !ok {
			return nil, errors.New("RRSI per-task evaluation is invalid")
		}
		rewards, ok := rrsiNumberArray(task["rewards"])
		if !ok || len(rewards) != k {
			return nil, errors.New("RRSI per-task rewards do not match k")
		}
		for _, reward := range rewards {
			if reward < 0 || reward > 1 {
				return nil, errors.New("RRSI per-task reward is outside [0,1]")
			}
		}
		weights := []float64{}
		if rawWeights, exists := task["weights"]; !exists || rawWeights == nil {
			weights = make([]float64, k)
			for i := range weights {
				weights[i] = 1
			}
		} else {
			var valid bool
			weights, valid = rrsiNumberArray(rawWeights)
			if !valid {
				return nil, errors.New("RRSI per-task weights are invalid")
			}
			if len(weights) == 0 {
				weights = make([]float64, k)
				for i := range weights {
					weights[i] = 1
				}
			} else if len(weights) != k {
				return nil, errors.New("RRSI per-task weights do not match k")
			}
		}
		for i, weight := range weights {
			if weight <= 0 {
				return nil, errors.New("RRSI per-task weight must be positive")
			}
			weightedScore += rewards[i] * weight
			totalWeight += weight
		}
		tokens := []any{}
		if rawTokens, exists := task["tokens"]; !exists || rawTokens == nil {
			tokens = make([]any, k)
		} else {
			var valid bool
			tokens, valid = rrsiNullableNumberArray(rawTokens)
			if !valid {
				return nil, errors.New("RRSI per-task token counts are invalid")
			}
			if len(tokens) == 0 {
				tokens = make([]any, k)
			} else if len(tokens) != k {
				return nil, errors.New("RRSI per-task token counts do not match k")
			}
		}
		for _, rawToken := range tokens {
			if rawToken == nil {
				continue
			}
			tokenCount, ok := rrsiNumber(rawToken)
			if !ok || tokenCount < 0 {
				return nil, errors.New("RRSI per-task token count is invalid")
			}
			if tokenCount > 0 {
				totalTokens += tokenCount
				observedTokens++
			}
		}
		taskMissing, ok := rrsiInteger(task["missing"])
		if !ok || taskMissing < 0 || taskMissing > k {
			return nil, errors.New("RRSI per-task missing count is invalid")
		}
		totalMissing += taskMissing
		taskMetrics := map[string]any{}
		switch domain {
		case "coding":
			if rawExtra, exists := task["extra"]; exists {
				extra, ok := rrsiObject(rawExtra)
				if !ok {
					return nil, errors.New("RRSI coding task metrics are invalid")
				}
				if passes, ok := rrsiInteger(extra["passes"]); ok {
					if passes < 0 || passes > k {
						return nil, errors.New("RRSI coding pass count is invalid")
					}
					taskMetrics["passes"] = passes
				}
			}
		case "eng":
			extra, ok := rrsiObject(task["extra"])
			if !ok {
				return nil, errors.New("RRSI engineering task metrics are invalid")
			}
			for _, key := range []string{"combined_scores", "valid", "no_payload"} {
				values, ok := rrsiNumberArray(extra[key])
				if !ok || len(values) != k {
					return nil, fmt.Errorf("RRSI engineering task metric %s does not match k", key)
				}
				for _, metric := range values {
					if metric < 0 || (key != "combined_scores" && metric > 1) {
						return nil, fmt.Errorf("RRSI engineering task metric %s is out of range", key)
					}
				}
				taskMetrics[key] = values
			}
		}
		taskIDs = append(taskIDs, taskID)
		tasks[taskID] = map[string]any{
			"rewards": rewards, "weights": weights, "tokens": tokens,
			"missing": taskMissing, "metrics": taskMetrics,
		}
	}
	if totalMissing != missing || totalWeight == 0 || !rrsiClose(score, weightedScore/totalWeight) {
		return nil, errors.New("RRSI evaluation aggregate does not reconcile with per-task data")
	}
	if observedTokens == 0 {
		if hasMeanTokens {
			return nil, errors.New("RRSI evaluation reports token cost without token counts")
		}
	} else if !hasMeanTokens || !rrsiClose(meanTokens, totalTokens/float64(observedTokens)) {
		return nil, errors.New("RRSI evaluation token cost does not reconcile")
	}
	metrics, err := normalizeRRSIDomainMetrics(domain, sourceMetrics, expected)
	if err != nil {
		return nil, err
	}
	return rrsiMarshal(map[string]any{
		"schema_version": rrsiEvaluationImportSchema, "domain": domain, "job": job,
		"trials_per_task": k, "score": score, "mean_tokens": nullableRRsiNumber(meanTokens, hasMeanTokens),
		"expected_trials": expected, "missing_trials": missing, "metrics": metrics,
		"per_task": tasks,
	})
}

func normalizeRRSIDomainMetrics(domain string, source map[string]any, expected int) (map[string]any, error) {
	number := func(key string, min, max float64) (float64, error) {
		value, ok := rrsiNumber(source[key])
		if !ok || value < min || value > max {
			return 0, fmt.Errorf("RRSI %s metric %s is invalid", domain, key)
		}
		return value, nil
	}
	integer := func(key string, max int) (int, error) {
		value, ok := rrsiInteger(source[key])
		if !ok || value < 0 || value > max {
			return 0, fmt.Errorf("RRSI %s metric %s is invalid", domain, key)
		}
		return value, nil
	}
	switch domain {
	case "coding":
		passes, err := integer("total_passes", expected)
		if err != nil {
			return nil, err
		}
		trials, err := integer("n_trials", expected)
		if err != nil || trials != expected {
			return nil, errors.New("RRSI coding trial count does not match evaluation")
		}
		rate, err := number("pass_rate", 0, 1)
		if err != nil || !rrsiClose(rate, float64(passes)/float64(maxInt(expected, 1))) {
			return nil, errors.New("RRSI coding pass rate does not reconcile")
		}
		return map[string]any{"total_passes": passes, "n_trials": trials, "pass_rate": rate}, nil
	case "workspace":
		passed, err := integer("criteria_passed", math.MaxInt)
		if err != nil {
			return nil, err
		}
		total, err := integer("criteria_total", math.MaxInt)
		if err != nil || total == 0 || passed > total {
			return nil, errors.New("RRSI workspace criterion totals are invalid")
		}
		allPass, err := integer("all_pass_tasks", expected)
		if err != nil || allPass > expected {
			return nil, errors.New("RRSI workspace all-pass task count is invalid")
		}
		rate, err := number("criterion_rate", 0, 1)
		if err != nil || !rrsiClose(rate, float64(passed)/float64(total)) {
			return nil, errors.New("RRSI workspace criterion rate does not reconcile")
		}
		return map[string]any{"criteria_passed": passed, "criteria_total": total,
			"all_pass_tasks": allPass, "criterion_rate": rate}, nil
	case "eng":
		passes, err := integer("total_passes", expected)
		if err != nil {
			return nil, err
		}
		trials, err := integer("n_trials", expected)
		if err != nil || trials != expected {
			return nil, errors.New("RRSI engineering trial count does not match evaluation")
		}
		passRate, err := number("pass_rate", 0, 1)
		if err != nil || !rrsiClose(passRate, float64(passes)/float64(maxInt(expected, 1))) {
			return nil, errors.New("RRSI engineering pass rate does not reconcile")
		}
		meanScore, err := number("mean_combined_score", 0, math.MaxFloat64)
		if err != nil {
			return nil, err
		}
		validRate, err := number("valid_rate", 0, 1)
		if err != nil {
			return nil, err
		}
		noPayloadRate, err := number("no_payload_rate", 0, 1)
		if err != nil {
			return nil, err
		}
		return map[string]any{"total_passes": passes, "n_trials": trials, "pass_rate": passRate,
			"mean_combined_score": meanScore, "valid_rate": validRate, "no_payload_rate": noPayloadRate}, nil
	default:
		return nil, errors.New("unsupported RRSI domain metrics")
	}
}

func normalizeRRSICalibration(domain string, value any) (json.RawMessage, error) {
	source, ok := rrsiObject(value)
	if !ok {
		return nil, errors.New("RRSI calibration must be an object")
	}
	delta, deltaOK := rrsiNumber(source["delta"])
	z, zOK := rrsiNumber(source["z"])
	sd, sdOK := rrsiNumber(source["sd_null"])
	sdBootstrap, sdBootstrapOK := rrsiNumber(source["sd_null_bootstrap"])
	se, seOK := rrsiNumber(source["se_bootstrap"])
	evaluations, evaluationsOK := rrsiInteger(source["n_evals"])
	k, kOK := rrsiInteger(source["k"])
	tasks, tasksOK := rrsiInteger(source["n_tasks"])
	baseScore, scoreOK := rrsiNumber(source["S_base"])
	method, methodOK := source["method"].(string)
	baseCost, hasBaseCost, costOK := rrsiOptionalNumber(source["C_base"])
	if !deltaOK || delta < 0 || !zOK || z <= 0 || !sdOK || sd < 0 || !sdBootstrapOK || sdBootstrap < 0 ||
		!seOK || se < 0 || !evaluationsOK || evaluations < 1 || !kOK || k < 1 || !tasksOK || tasks < 1 ||
		!scoreOK || baseScore < 0 || baseScore > 1 || !methodOK || strings.TrimSpace(method) == "" || !costOK ||
		(hasBaseCost && baseCost < 0) {
		return nil, errors.New("RRSI noise calibration fields are invalid")
	}
	out := map[string]any{
		"schema_version": rrsiCalibrationImportSchema, "domain": domain, "delta": delta,
		"z": z, "sd_null": sd, "sd_null_bootstrap": sdBootstrap, "se_bootstrap": se,
		"method": method, "n_evals": evaluations, "k": k, "n_tasks": tasks, "S_base": baseScore,
		"C_base": nullableRRsiNumber(baseCost, hasBaseCost),
	}
	if values, exists := source["S_per_eval"]; exists {
		scores, ok := rrsiNumberArray(values)
		if !ok || len(scores) != evaluations {
			return nil, errors.New("RRSI calibration score samples are invalid")
		}
		for _, score := range scores {
			if score < 0 || score > 1 {
				return nil, errors.New("RRSI calibration score sample is outside [0,1]")
			}
		}
		out["S_per_eval"] = scores
	}
	if value, exists := source["max_abs_diff"]; exists {
		maximum, ok := rrsiNumber(value)
		if !ok || maximum < 0 || maximum > 1 {
			return nil, errors.New("RRSI calibration maximum score difference is invalid")
		}
		out["max_abs_diff"] = maximum
	}
	return rrsiMarshal(out)
}

func normalizeRRSIFrontier(domain string, value any) (json.RawMessage, error) {
	source, ok := rrsiObject(value)
	if !ok || source["domain"] != domain {
		return nil, errors.New("RRSI frontier domain is missing or mismatched")
	}
	sStar, starOK := rrsiNumber(source["S_star"])
	incumbent, incumbentOK := rrsiObject(source["incumbent"])
	trajectory, trajectoryOK := rrsiArray(source["trajectory"])
	if !starOK || sStar < 0 || sStar > 1 || !incumbentOK || !trajectoryOK || len(trajectory) == 0 || len(trajectory) > maxRecords {
		return nil, errors.New("RRSI frontier summary is invalid")
	}
	incumbentOut, incumbentScore, incumbentRound, err := normalizeRRSIFrontierEntry(incumbent, true)
	if err != nil {
		return nil, err
	}
	entries := make([]any, 0, len(trajectory))
	maxScore := 0.0
	for index, raw := range trajectory {
		entry, ok := rrsiObject(raw)
		if !ok {
			return nil, errors.New("RRSI frontier trajectory entry is invalid")
		}
		normalized, score, round, err := normalizeRRSIFrontierEntry(entry, false)
		if err != nil || round != index {
			return nil, errors.New("RRSI frontier trajectory is not sequential")
		}
		if score > maxScore {
			maxScore = score
		}
		entries = append(entries, normalized)
	}
	last, _ := rrsiObject(trajectory[len(trajectory)-1])
	lastCommit, _ := last["commit"].(string)
	incCommit, _ := incumbent["commit"].(string)
	if incumbentRound != len(trajectory)-1 || !rrsiClose(incumbentScore, func() float64 { v, _ := rrsiNumber(last["S"]); return v }()) ||
		incCommit != lastCommit || !rrsiClose(sStar, maxScore) {
		return nil, errors.New("RRSI frontier incumbent does not reconcile with its trajectory")
	}
	return rrsiMarshal(map[string]any{
		"schema_version": rrsiFrontierImportSchema, "domain": domain, "S_star": sStar,
		"incumbent": incumbentOut, "trajectory": entries,
	})
}

func normalizeRRSIFrontierEntry(source map[string]any, incumbent bool) (map[string]any, float64, int, error) {
	round, roundOK := rrsiInteger(source["t"])
	score, scoreOK := rrsiNumber(source["S"])
	commit, commitOK := source["commit"].(string)
	job, jobOK := source["job"].(string)
	cost, hasCost, costOK := rrsiOptionalNumber(source["C"])
	if !roundOK || round < 0 || !scoreOK || score < 0 || score > 1 || !commitOK || !validGitObjectID(commit) ||
		!jobOK || strings.TrimSpace(job) == "" || !costOK || (hasCost && cost < 0) {
		return nil, 0, 0, errors.New("RRSI frontier trajectory fields are invalid")
	}
	out := map[string]any{"t": round, "S": score, "C": nullableRRsiNumber(cost, hasCost), "commit": commit, "job": job}
	if incumbent {
		harnessTree, ok := source["harness_tree"].(string)
		if !ok || !validGitObjectID(harnessTree) {
			return nil, 0, 0, errors.New("RRSI frontier harness tree is invalid")
		}
		out["harness_tree"] = harnessTree
		if rawExtra, exists := source["extra"]; exists {
			extra, ok := rrsiObject(rawExtra)
			if !ok {
				return nil, 0, 0, errors.New("RRSI frontier incumbent metrics are invalid")
			}
			out["metrics"] = extra
		}
		if variant, ok := source["variant"].(string); ok {
			out["variant"] = variant
		}
	}
	return out, score, round, nil
}

func normalizeRRSIHistory(domain string, value any) (json.RawMessage, error) {
	source, ok := rrsiObject(value)
	if !ok {
		return nil, errors.New("RRSI edit-history record must be an object")
	}
	round, roundOK := rrsiInteger(source["t"])
	variant, variantOK := source["variant"].(string)
	outcome, outcomeOK := source["outcome"].(string)
	bundle, bundleOK := rrsiInteger(source["bundle"])
	if !roundOK || round < 0 || !variantOK || len(variant) > 64 || !outcomeOK || len(outcome) > 128 ||
		!bundleOK || bundle < 0 || bundle > 100 {
		return nil, errors.New("RRSI edit-history record fields are invalid")
	}
	out := map[string]any{"schema_version": rrsiHistoryImportSchema, "domain": domain,
		"round": round, "variant": variant, "outcome": outcome, "bundle": bundle}
	for _, key := range []string{"edit_id", "component", "hypothesis", "targets_mode"} {
		if value, exists := source[key]; exists && value != nil {
			text, ok := value.(string)
			if !ok || len(text) > 2000 {
				return nil, errors.New("RRSI edit-history text field is invalid")
			}
			out[key] = text
		}
	}
	for _, key := range []string{"delta_S", "delta_C", "S", "C"} {
		if value, exists := source[key]; exists {
			number, hasValue, ok := rrsiOptionalNumber(value)
			if !ok {
				return nil, errors.New("RRSI edit-history metric is invalid")
			}
			if hasValue {
				out[key] = number
			}
		}
	}
	if accepted, exists := source["accepted"]; exists {
		value, ok := accepted.(bool)
		if !ok {
			return nil, errors.New("RRSI edit-history accepted flag is invalid")
		}
		out["accepted"] = value
	}
	if predicted, exists := source["predicted_affected"]; exists {
		values, ok := rrsiStringArray(predicted, 512)
		if !ok {
			return nil, errors.New("RRSI edit-history predicted task list is invalid")
		}
		out["predicted_affected"] = values
	}
	return rrsiMarshal(out)
}

func normalizeRRSIAttribution(domain string, value any) (json.RawMessage, error) {
	source, ok := rrsiObject(value)
	if !ok {
		return nil, errors.New("RRSI attribution record must be an object")
	}
	round, roundOK := rrsiInteger(source["t"])
	variant, variantOK := source["variant"].(string)
	editID, editOK := source["edit_id"].(string)
	component, componentOK := source["component"].(string)
	if !roundOK || round < 0 || !variantOK || len(variant) > 64 || !editOK || len(editID) > 128 ||
		!componentOK || len(component) > 128 {
		return nil, errors.New("RRSI attribution identity is invalid")
	}
	predictedCount, predictedOK := rrsiInteger(source["n_predicted"])
	predicted, predictedListOK := rrsiStringArray(source["predicted_hit"], 512)
	regressions, regressionsOK := rrsiStringArray(source["unpredicted_regressions"], 512)
	if !predictedOK || predictedCount < 0 || !predictedListOK || len(predicted) > predictedCount || !regressionsOK {
		return nil, errors.New("RRSI attribution task evidence is invalid")
	}
	out := map[string]any{"schema_version": rrsiAttributionImportSchema, "domain": domain,
		"round": round, "variant": variant, "edit_id": editID, "component": component,
		"n_predicted": predictedCount, "predicted_hit": predicted, "unpredicted_regressions": regressions}
	if hypothesis, exists := source["hypothesis"]; exists {
		text, ok := hypothesis.(string)
		if !ok || len(text) > 2000 {
			return nil, errors.New("RRSI attribution hypothesis is invalid")
		}
		out["hypothesis"] = text
	}
	if hitRate, exists := source["hit_rate"]; exists && hitRate != nil {
		number, ok := rrsiNumber(hitRate)
		if !ok || number < 0 || number > 1 {
			return nil, errors.New("RRSI attribution hit rate is invalid")
		}
		out["hit_rate"] = number
	}
	return rrsiMarshal(out)
}

func normalizeRRSIDecision(domain string, value any) (json.RawMessage, error) {
	source, ok := rrsiObject(value)
	if !ok {
		return nil, errors.New("RRSI selection decision must be an object")
	}
	variant, variantOK := source["variant"].(string)
	admissible, admissibleOK := source["admissible"].(bool)
	reason, reasonOK := source["reason"].(string)
	if !variantOK || len(variant) > 64 || !admissibleOK || !reasonOK || len(reason) > 2000 {
		return nil, errors.New("RRSI selection decision fields are invalid")
	}
	out := map[string]any{"schema_version": rrsiDecisionImportSchema, "domain": domain,
		"variant": variant, "admissible": admissible, "reason": reason}
	for _, key := range []string{"S", "C", "delta_S", "delta_C"} {
		if value, exists := source[key]; exists {
			number, hasValue, ok := rrsiOptionalNumber(value)
			if !ok {
				return nil, errors.New("RRSI selection decision metric is invalid")
			}
			if hasValue {
				out[key] = number
			}
		}
	}
	if novelty, exists := source["novelty"]; exists {
		value, ok := rrsiInteger(novelty)
		if !ok || value < 0 {
			return nil, errors.New("RRSI selection decision novelty is invalid")
		}
		out["novelty"] = value
	}
	if guards, exists := source["guards"]; exists {
		values, ok := rrsiStringArray(guards, 512)
		if !ok {
			return nil, errors.New("RRSI selection decision guards are invalid")
		}
		out["guards"] = values
	}
	return rrsiMarshal(out)
}

func rrsiObject(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok && result != nil
}

func rrsiArray(value any) ([]any, bool) {
	result, ok := value.([]any)
	return result, ok
}

func rrsiNumber(value any) (float64, bool) {
	result, ok := value.(float64)
	return result, ok && !math.IsNaN(result) && !math.IsInf(result, 0)
}

func rrsiOptionalNumber(value any) (float64, bool, bool) {
	if value == nil {
		return 0, false, true
	}
	number, ok := rrsiNumber(value)
	return number, ok, ok
}

func nullableRRsiNumber(value float64, exists bool) any {
	if !exists {
		return nil
	}
	return value
}

func rrsiInteger(value any) (int, bool) {
	number, ok := rrsiNumber(value)
	if !ok || number < 0 || number > float64(math.MaxInt) || number != math.Trunc(number) {
		return 0, false
	}
	return int(number), true
}

func rrsiNumberArray(value any) ([]float64, bool) {
	items, ok := rrsiArray(value)
	if !ok {
		return nil, false
	}
	values := make([]float64, len(items))
	for i, item := range items {
		number, ok := rrsiNumber(item)
		if !ok {
			return nil, false
		}
		values[i] = number
	}
	return values, true
}

func rrsiNullableNumberArray(value any) ([]any, bool) {
	items, ok := rrsiArray(value)
	if !ok {
		return nil, false
	}
	values := make([]any, len(items))
	for i, item := range items {
		if item == nil {
			continue
		}
		number, ok := rrsiNumber(item)
		if !ok {
			return nil, false
		}
		values[i] = number
	}
	return values, true
}

func rrsiStringArray(value any, maxLength int) ([]string, bool) {
	items, ok := rrsiArray(value)
	if !ok || len(items) > maxLength {
		return nil, false
	}
	values := make([]string, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok || len(text) > 512 || strings.ContainsRune(text, '\x00') {
			return nil, false
		}
		values[i] = text
	}
	return values, true
}

func rrsiClose(left, right float64) bool {
	return math.Abs(left-right) <= 1e-6
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func validGitObjectID(value string) bool {
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func rrsiMarshal(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	return json.RawMessage(data), err
}
