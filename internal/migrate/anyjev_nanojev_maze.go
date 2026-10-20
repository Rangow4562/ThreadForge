package migrate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevNanoJevMazeResultSchema = "threadforge.anyjev-nanojev-maze-result.v1"

type anyJevNanoJevMazeImport struct {
	SchemaVersion           string                          `json:"schema_version"`
	ModelSHA256             string                          `json:"model_sha256"`
	ModelRevisionSHA256     string                          `json:"model_revision_sha256,omitempty"`
	NanoJevCommit           string                          `json:"nanojev_commit,omitempty"`
	EpisodesSHA256          string                          `json:"episodes_sha256"`
	AuditInputsSHA256       string                          `json:"audit_inputs_sha256"`
	Engine                  string                          `json:"engine"`
	Level                   string                          `json:"level"`
	Prior                   string                          `json:"prior,omitempty"`
	Protocol                anyJevNanoJevMazeProtocolImport `json:"protocol"`
	Overall                 anyJevNanoJevMazeSummaryImport  `json:"overall"`
	BySplit                 []anyJevNanoJevMazeSplitImport  `json:"by_split"`
	OmittedFields           []string                        `json:"omitted_fields"`
	HumanReviewRequired     bool                            `json:"human_review_required"`
	ExecutionVerified       bool                            `json:"execution_verified"`
	CutoverEvidenceEligible bool                            `json:"cutover_evidence_eligible"`
}

type anyJevNanoJevMazeProtocolImport struct {
	WindowSize          int     `json:"window_size"`
	MaxSteps            int     `json:"max_steps"`
	Threshold           float64 `json:"threshold"`
	Temperature         float64 `json:"temperature"`
	FullMapBFSUsed      bool    `json:"full_map_bfs_used"`
	OracleUsedForPolicy bool    `json:"oracle_used_for_policy"`
}

type anyJevNanoJevMazeSummaryImport struct {
	Episodes                    int      `json:"episodes"`
	GoalCompleted               int      `json:"goal_completed"`
	GoalCompletionRate          float64  `json:"goal_completion_rate"`
	Attempts                    int      `json:"attempts"`
	SuccessfulMoves             int      `json:"successful_moves"`
	Collisions                  int      `json:"collisions"`
	ProbeCount                  int      `json:"probe_count"`
	FallbackCount               int      `json:"fallback_count"`
	VerifiedEdgeRepositionCount int      `json:"verified_edge_reposition_count"`
	VisitedCells                int      `json:"visited_cells"`
	RevisitedCellEntries        int      `json:"revisited_cell_entries"`
	PredictionSites             int      `json:"prediction_sites"`
	AtomicQuestions             int      `json:"atomic_questions"`
	AtomicAccuracy              float64  `json:"atomic_accuracy"`
	AtomicBrier                 float64  `json:"atomic_brier"`
	AtomicNLL                   float64  `json:"atomic_nll"`
	EdgeMajority                *float64 `json:"edge_majority,omitempty"`
	MeanPTrue                   *float64 `json:"mean_p_true,omitempty"`
	MazeSizes                   []int    `json:"maze_sizes,omitempty"`
	HorizonMin                  *int     `json:"horizon_min,omitempty"`
	HorizonMax                  *int     `json:"horizon_max,omitempty"`
}

type anyJevNanoJevMazeSplitImport struct {
	Split   string                         `json:"split"`
	Summary anyJevNanoJevMazeSummaryImport `json:"summary"`
}

type anyJevNanoJevMazeEpisodeImport struct {
	ID                          string
	Split                       string
	Size                        int
	Horizon                     int
	GoalCompleted               bool
	Attempts                    int
	SuccessfulMoves             int
	Collisions                  int
	ProbeCount                  int
	FallbackCount               int
	VerifiedEdgeRepositionCount int
	VisitedCells                int
	RevisitedCellEntries        int
	PredictionSites             int
	AtomicQuestions             int
	AtomicAccuracy              float64
	AtomicBrier                 float64
	AtomicNLL                   float64
}

var anyJevNanoJevMazeRootFields = map[string]bool{
	"audit_inputs_sha256": true, "date": true, "elapsed_seconds": true, "engine": true,
	"episodes": true, "episodes_sha256": true, "execution": true, "level": true,
	"model": true, "model_controls": true, "model_role": true, "nanojev_commit": true,
	"nanojev_dir": true, "prior": true, "protocol": true, "question_template": true,
	"revision": true, "schema": true, "summary": true,
}

var anyJevNanoJevMazeEpisodeFields = map[string]bool{
	"atomic_metrics": true, "attempts": true, "collisions": true, "fallback_count": true,
	"goal_completion": true, "horizon": true, "id": true, "initial_state": true,
	"probe_count": true, "revisited_cell_entries": true, "size": true, "split": true,
	"status": true, "successful_moves": true, "verified_edge_reposition_count": true,
	"visited_cells": true,
}

var anyJevNanoJevMazeSummaryFields = map[string]bool{
	"atomic_accuracy": true, "atomic_brier": true, "atomic_nll": true,
	"atomic_questions": true, "attempts": true, "collisions": true, "edge_majority": true,
	"edge_questions": true, "episodes": true, "fallback_count": true,
	"goal_completed": true, "goal_completion_rate": true, "mean_p_true": true,
	"prediction_sites": true, "probe_count": true, "successful_moves": true,
	"verified_edge_reposition_count": true,
}

var anyJevNanoJevMazeAtomicFields = map[string]bool{
	"atomic_accuracy": true, "atomic_brier": true, "atomic_nll": true,
	"atomic_questions": true, "prediction_sites": true,
}

var anyJevNanoJevMazeProtocolFields = map[string]bool{
	"atomic_metric_scope": true, "brier_definition": true, "collision_rule": true,
	"default_horizon": true, "fallback_definition": true, "full_map_bfs_used": true,
	"max_steps": true, "memory_termination": true, "nll_probability_floor": true,
	"oracle_used_for_policy": true, "prediction_cache": true, "probe_rule": true,
	"reposition_rule": true, "selection_rule": true, "temperature": true,
	"threshold": true, "window_size": true,
}

func isAnyJevNanoJevMazeJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	return strings.HasPrefix(path, "bench/results_nanojev/") && strings.HasSuffix(path, ".json") &&
		!strings.HasSuffix(path, ".items.json")
}

func normalizeAnyJevNanoJevMazeResult(value any) (normalizedItem, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev NanoJev maze result shape")
	}
	for key := range object {
		if !anyJevNanoJevMazeRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev NanoJev maze result field %q", key)
		}
	}
	model, modelOK := boundedAnyJevBenchmarkString(object["model"], 2048)
	engine, engineOK := object["engine"].(string)
	level, levelOK := object["level"].(string)
	schema, schemaOK := object["schema"].(string)
	role, roleOK := object["model_role"].(string)
	if !modelOK || !engineOK || !oneOf(engine, "anyjev", "nanojev_native") ||
		!levelOK || !oneOf(level, "raw", "L0", "native_AB_readout") ||
		!schemaOK || schema != "nanojev-model-edges-maze-v1" ||
		!roleOK || role != "model_guided_local_edge_exploration" {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze identity or schema is unsupported")
	}
	prior := ""
	if engine == "anyjev" {
		if level == "native_AB_readout" {
			return normalizedItem{}, errors.New("AnyJev engine has an invalid maze readout level")
		}
		var exists bool
		prior, exists = object["prior"].(string)
		if !exists || !oneOf(prior, "batch", "content_free", "none") {
			return normalizedItem{}, errors.New("AnyJev maze prior is unsupported")
		}
	} else if level != "native_AB_readout" {
		return normalizedItem{}, errors.New("NanoJev native maze result has an invalid readout level")
	} else if _, exists := object["prior"]; exists {
		return normalizedItem{}, errors.New("NanoJev native maze result unexpectedly declares an AnyJev prior")
	}
	date, dateOK := boundedAnyJevBenchmarkString(object["date"], 64)
	if !dateOK || !validAnyJevLatencyTimestamp(date) {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze timestamp is invalid")
	}
	if _, ok := boundedAnyJevNumber(object["elapsed_seconds"], 0, 1e9); !ok {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze elapsed time is invalid")
	}
	if !validAnyJevSHA256(object["episodes_sha256"]) || !validAnyJevSHA256(object["audit_inputs_sha256"]) {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze input digest is invalid")
	}
	modelControls, controlsExist := object["model_controls"]
	if controlsExist {
		if modelControls != nil {
			if _, ok := boundedAnyJevBenchmarkString(modelControls, 2048); !ok {
				return normalizedItem{}, errors.New("AnyJev NanoJev maze model controls are invalid")
			}
		}
	}
	if directory, ok := boundedAnyJevBenchmarkString(object["nanojev_dir"], 2048); !ok || directory == "" {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze checkout label is invalid")
	}
	commit, hasCommit := object["nanojev_commit"]
	if !hasCommit {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze result omits its source commit field")
	}
	commitValue := ""
	if commit != nil {
		commitValue, ok = commit.(string)
		if !ok || !validAnyJevHex(commitValue, 40) {
			return normalizedItem{}, errors.New("AnyJev NanoJev maze source commit is invalid")
		}
	}
	revisionHash := ""
	if rawRevision, exists := object["revision"]; !exists {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze result omits its model revision field")
	} else if rawRevision != nil {
		revision, ok := boundedAnyJevBenchmarkString(rawRevision, 2048)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev NanoJev maze model revision is invalid")
		}
		revisionHash = anyJevStringSHA256(revision)
	}
	if err := validateAnyJevNanoJevMazeProtocol(object["protocol"]); err != nil {
		return normalizedItem{}, err
	}
	if err := validateAnyJevNanoJevMazeQuestionTemplate(object["question_template"]); err != nil {
		return normalizedItem{}, err
	}
	if err := validateAnyJevNanoJevMazeExecution(object["execution"]); err != nil {
		return normalizedItem{}, err
	}

	rawEpisodes, ok := object["episodes"].([]any)
	if !ok || len(rawEpisodes) == 0 || len(rawEpisodes) > 10000 {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze episodes are invalid")
	}
	episodes := make([]anyJevNanoJevMazeEpisodeImport, 0, len(rawEpisodes))
	seenIDs := make(map[string]struct{}, len(rawEpisodes))
	for _, rawEpisode := range rawEpisodes {
		episode, ok := rawEpisode.(map[string]any)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev NanoJev maze episode is invalid")
		}
		for key := range episode {
			if !anyJevNanoJevMazeEpisodeFields[key] {
				return normalizedItem{}, fmt.Errorf("unsupported AnyJev NanoJev maze episode field %q", key)
			}
		}
		id, idOK := boundedAnyJevBenchmarkString(episode["id"], 512)
		split, splitOK := episode["split"].(string)
		status, statusOK := episode["status"].(string)
		goalCompletion, goalOK := episode["goal_completion"].(bool)
		size, sizeOK := boundedAnyJevInteger(episode["size"], 1, 100000)
		horizon, horizonOK := boundedAnyJevInteger(episode["horizon"], 1, 10000000)
		if !idOK || !splitOK || !oneOf(split, "test", "ood") || !statusOK ||
			!oneOf(status, "goal", "horizon_exhausted") || !goalOK || (status == "goal") != goalCompletion ||
			!sizeOK || !horizonOK {
			return normalizedItem{}, errors.New("AnyJev NanoJev maze episode identity or dimensions are invalid")
		}
		if _, duplicate := seenIDs[id]; duplicate {
			return normalizedItem{}, errors.New("AnyJev NanoJev maze contains duplicate episode identifiers")
		}
		seenIDs[id] = struct{}{}
		if err := validateAnyJevNanoJevMazeInitialState(episode["initial_state"], size); err != nil {
			return normalizedItem{}, err
		}
		attempts, attemptsOK := boundedAnyJevInteger(episode["attempts"], 0, 1e9)
		successful, successfulOK := boundedAnyJevInteger(episode["successful_moves"], 0, 1e9)
		collisions, collisionsOK := boundedAnyJevInteger(episode["collisions"], 0, 1e9)
		probes, probesOK := boundedAnyJevInteger(episode["probe_count"], 0, 1e9)
		fallbacks, fallbacksOK := boundedAnyJevInteger(episode["fallback_count"], 0, 1e9)
		repositions, repositionsOK := boundedAnyJevInteger(episode["verified_edge_reposition_count"], 0, 1e9)
		visited, visitedOK := boundedAnyJevInteger(episode["visited_cells"], 0, 1e9)
		revisited, revisitedOK := boundedAnyJevInteger(episode["revisited_cell_entries"], 0, 1e9)
		if !attemptsOK || !successfulOK || !collisionsOK || !probesOK || !fallbacksOK ||
			!repositionsOK || !visitedOK || !revisitedOK || attempts != successful+collisions {
			return normalizedItem{}, errors.New("AnyJev NanoJev maze episode counters are invalid or inconsistent")
		}
		atomic, ok := episode["atomic_metrics"].(map[string]any)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev NanoJev maze atomic metrics are invalid")
		}
		for key := range atomic {
			if !anyJevNanoJevMazeAtomicFields[key] {
				return normalizedItem{}, fmt.Errorf("unsupported AnyJev NanoJev maze atomic metric %q", key)
			}
		}
		atomicQuestions, atomicQuestionsOK := boundedAnyJevInteger(atomic["atomic_questions"], 1, 1e9)
		predictionSites, predictionSitesOK := boundedAnyJevInteger(atomic["prediction_sites"], 1, 1e9)
		accuracy, accuracyOK := boundedAnyJevNumber(atomic["atomic_accuracy"], 0, 1)
		brier, brierOK := boundedAnyJevNumber(atomic["atomic_brier"], 0, 1)
		nll, nllOK := boundedAnyJevNumber(atomic["atomic_nll"], 0, 1e6)
		if !atomicQuestionsOK || !predictionSitesOK || !accuracyOK || !brierOK || !nllOK ||
			atomicQuestions != predictionSites*4 {
			return normalizedItem{}, errors.New("AnyJev NanoJev maze atomic metrics are inconsistent")
		}
		episodes = append(episodes, anyJevNanoJevMazeEpisodeImport{
			ID: id, Split: split, Size: size, Horizon: horizon, GoalCompleted: goalCompletion,
			Attempts: attempts, SuccessfulMoves: successful, Collisions: collisions,
			ProbeCount: probes, FallbackCount: fallbacks,
			VerifiedEdgeRepositionCount: repositions, VisitedCells: visited,
			RevisitedCellEntries: revisited, PredictionSites: predictionSites,
			AtomicQuestions: atomicQuestions, AtomicAccuracy: accuracy,
			AtomicBrier: brier, AtomicNLL: nll,
		})
	}

	overall, bySplit, err := aggregateAnyJevNanoJevMazeEpisodes(episodes)
	if err != nil {
		return normalizedItem{}, err
	}
	if err := reconcileAnyJevNanoJevMazeSummary(object["summary"], overall); err != nil {
		return normalizedItem{}, err
	}
	summary, ok := object["summary"].(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze summary is invalid")
	}
	edgeMajority, edgeMajorityOK := boundedAnyJevNumber(summary["edge_majority"], 0, 1)
	meanPTrue, meanPTrueOK := boundedAnyJevNumber(summary["mean_p_true"], 0, 1)
	if !edgeMajorityOK || !meanPTrueOK {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze edge summary is invalid")
	}
	overall.EdgeMajority = &edgeMajority
	overall.MeanPTrue = &meanPTrue

	protocol, _ := object["protocol"].(map[string]any)
	protocolProjection := anyJevNanoJevMazeProtocolImport{}
	protocolProjection.WindowSize, _ = boundedAnyJevInteger(protocol["window_size"], 1, 10000)
	protocolProjection.MaxSteps, _ = boundedAnyJevInteger(protocol["max_steps"], 0, 1e7)
	protocolProjection.Threshold, _ = boundedAnyJevNumber(protocol["threshold"], 0, 1)
	protocolProjection.Temperature, _ = boundedAnyJevNumber(protocol["temperature"], 0, 1e6)
	protocolProjection.FullMapBFSUsed, _ = protocol["full_map_bfs_used"].(bool)
	protocolProjection.OracleUsedForPolicy, _ = protocol["oracle_used_for_policy"].(bool)

	payload := anyJevNanoJevMazeImport{
		SchemaVersion:     anyJevNanoJevMazeResultSchema,
		ModelSHA256:       anyJevStringSHA256(model),
		NanoJevCommit:     commitValue,
		EpisodesSHA256:    object["episodes_sha256"].(string),
		AuditInputsSHA256: object["audit_inputs_sha256"].(string),
		Engine:            engine, Level: level, Prior: prior,
		Protocol: protocolProjection, Overall: overall, BySplit: bySplit,
		OmittedFields: []string{
			"date", "elapsed_seconds", "model_label", "model_revision_label", "nanojev_directory",
			"model_controls", "model_role", "question_templates", "protocol_descriptions",
			"execution_counters", "episode_ids", "initial_states", "per_step_observations",
		},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEvidenceEligible: false,
	}
	if revisionHash != "" {
		payload.ModelRevisionSHA256 = revisionHash
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev NanoJev maze projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func validateAnyJevNanoJevMazeProtocol(value any) error {
	protocol, ok := value.(map[string]any)
	if !ok || len(protocol) != len(anyJevNanoJevMazeProtocolFields) {
		return errors.New("AnyJev NanoJev maze protocol is incomplete")
	}
	for key := range protocol {
		if !anyJevNanoJevMazeProtocolFields[key] {
			return fmt.Errorf("unsupported AnyJev NanoJev maze protocol field %q", key)
		}
	}
	for _, key := range []string{
		"atomic_metric_scope", "brier_definition", "collision_rule", "default_horizon",
		"fallback_definition", "memory_termination", "prediction_cache", "probe_rule",
		"reposition_rule", "selection_rule",
	} {
		if _, ok := boundedAnyJevBenchmarkString(protocol[key], 4096); !ok {
			return fmt.Errorf("AnyJev NanoJev maze protocol description %q is invalid", key)
		}
	}
	_, windowOK := boundedAnyJevInteger(protocol["window_size"], 1, 10000)
	_, stepsOK := boundedAnyJevInteger(protocol["max_steps"], 0, 1e7)
	_, thresholdOK := boundedAnyJevNumber(protocol["threshold"], 0, 1)
	temperature, temperatureOK := boundedAnyJevNumber(protocol["temperature"], 0, 1e6)
	probabilityFloor, floorOK := boundedAnyJevNumber(protocol["nll_probability_floor"], 0, 1)
	fullMap, fullMapOK := protocol["full_map_bfs_used"].(bool)
	oracle, oracleOK := protocol["oracle_used_for_policy"].(bool)
	if !windowOK || !stepsOK || !thresholdOK || !temperatureOK || temperature == 0 ||
		!floorOK || probabilityFloor == 0 || !fullMapOK || !oracleOK || fullMap || oracle {
		return errors.New("AnyJev NanoJev maze protocol contains invalid dimensions or oracle use")
	}
	return nil
}

func validateAnyJevNanoJevMazeQuestionTemplate(value any) error {
	template, ok := value.(map[string]any)
	if !ok || len(template) != 4 {
		return errors.New("AnyJev NanoJev maze question template is incomplete")
	}
	for _, direction := range []string{"clear_north", "clear_east", "clear_south", "clear_west"} {
		question, ok := template[direction].(map[string]any)
		if !ok || len(question) != 3 {
			return fmt.Errorf("AnyJev NanoJev maze question template %q is invalid", direction)
		}
		for key := range question {
			if key != "criteria" && key != "instructions" && key != "type" {
				return fmt.Errorf("unsupported AnyJev NanoJev maze question field %q", key)
			}
		}
		typeName, typeOK := question["type"].(string)
		if !typeOK || typeName != "boolean" {
			return fmt.Errorf("AnyJev NanoJev maze question type %q is invalid", direction)
		}
		if _, ok := boundedAnyJevBenchmarkString(question["instructions"], 4096); !ok {
			return fmt.Errorf("AnyJev NanoJev maze question instructions %q are invalid", direction)
		}
		criteria, ok := question["criteria"].(map[string]any)
		if !ok || len(criteria) != 2 {
			return fmt.Errorf("AnyJev NanoJev maze question criteria %q are invalid", direction)
		}
		for key := range criteria {
			if key != "true" && key != "false" {
				return fmt.Errorf("unsupported AnyJev NanoJev maze question criterion %q", key)
			}
			if _, ok := boundedAnyJevBenchmarkString(criteria[key], 4096); !ok {
				return fmt.Errorf("AnyJev NanoJev maze question criterion %q is invalid", key)
			}
		}
	}
	return nil
}

func validateAnyJevNanoJevMazeExecution(value any) error {
	execution, ok := value.(map[string]any)
	if !ok || len(execution) != 3 {
		return errors.New("AnyJev NanoJev maze execution metadata is incomplete")
	}
	allowed := map[string]bool{"predict_calls": true, "model_forward_passes": true, "network_model_calls": true}
	for key := range execution {
		if !allowed[key] {
			return fmt.Errorf("unsupported AnyJev NanoJev maze execution field %q", key)
		}
	}
	for _, key := range []string{"predict_calls", "network_model_calls"} {
		if _, ok := boundedAnyJevInteger(execution[key], 0, 1e9); !ok {
			return fmt.Errorf("AnyJev NanoJev maze execution counter %q is invalid", key)
		}
	}
	if value := execution["model_forward_passes"]; value != nil {
		if _, ok := boundedAnyJevInteger(value, 0, 1e12); !ok {
			return errors.New("AnyJev NanoJev maze forward-pass count is invalid")
		}
	}
	return nil
}

func validateAnyJevNanoJevMazeInitialState(value any, size int) error {
	state, ok := value.(map[string]any)
	if !ok || len(state) != 7 {
		return errors.New("AnyJev NanoJev maze initial state has an unsupported shape")
	}
	allowed := map[string]bool{"game": true, "goal": true, "position": true, "seed": true, "size": true, "topology": true, "walls": true}
	for key := range state {
		if !allowed[key] {
			return fmt.Errorf("unsupported AnyJev NanoJev maze initial-state field %q", key)
		}
	}
	game, gameOK := state["game"].(string)
	stateSize, sizeOK := boundedAnyJevInteger(state["size"], 1, 100000)
	if !gameOK || game != "scaled_maze" || !sizeOK || stateSize != size {
		return errors.New("AnyJev NanoJev maze initial-state identity is invalid")
	}
	if _, ok := boundedAnyJevInteger(state["seed"], -1e9, 1e9); !ok {
		return errors.New("AnyJev NanoJev maze initial-state seed is invalid")
	}
	for _, key := range []string{"position", "goal"} {
		coordinates, ok := state[key].([]any)
		if !ok || len(coordinates) != 2 {
			return fmt.Errorf("AnyJev NanoJev maze initial-state %q is invalid", key)
		}
		for _, coordinate := range coordinates {
			if _, ok := boundedAnyJevInteger(coordinate, -1e7, 1e7); !ok {
				return fmt.Errorf("AnyJev NanoJev maze initial-state %q coordinate is invalid", key)
			}
		}
	}
	if _, ok := boundedAnyJevBenchmarkString(state["topology"], 4096); !ok {
		return errors.New("AnyJev NanoJev maze initial-state topology is invalid")
	}
	if _, ok := state["walls"].([]any); !ok {
		return errors.New("AnyJev NanoJev maze initial-state walls are invalid")
	}
	return nil
}

func aggregateAnyJevNanoJevMazeEpisodes(episodes []anyJevNanoJevMazeEpisodeImport) (anyJevNanoJevMazeSummaryImport, []anyJevNanoJevMazeSplitImport, error) {
	bySplit := make(map[string]*anyJevNanoJevMazeSummaryImport)
	bySizes := make(map[string]map[int]struct{})
	for _, episode := range episodes {
		summary := bySplit[episode.Split]
		if summary == nil {
			summary = &anyJevNanoJevMazeSummaryImport{}
			bySplit[episode.Split] = summary
			bySizes[episode.Split] = make(map[int]struct{})
		}
		summary.Episodes++
		if episode.GoalCompleted {
			summary.GoalCompleted++
		}
		summary.Attempts += episode.Attempts
		summary.SuccessfulMoves += episode.SuccessfulMoves
		summary.Collisions += episode.Collisions
		summary.ProbeCount += episode.ProbeCount
		summary.FallbackCount += episode.FallbackCount
		summary.VerifiedEdgeRepositionCount += episode.VerifiedEdgeRepositionCount
		summary.VisitedCells += episode.VisitedCells
		summary.RevisitedCellEntries += episode.RevisitedCellEntries
		summary.PredictionSites += episode.PredictionSites
		summary.AtomicQuestions += episode.AtomicQuestions
		summary.AtomicAccuracy += episode.AtomicAccuracy * float64(episode.AtomicQuestions)
		summary.AtomicBrier += episode.AtomicBrier * float64(episode.AtomicQuestions)
		summary.AtomicNLL += episode.AtomicNLL * float64(episode.AtomicQuestions)
		bySizes[episode.Split][episode.Size] = struct{}{}
		if summary.HorizonMin == nil || episode.Horizon < *summary.HorizonMin {
			value := episode.Horizon
			summary.HorizonMin = &value
		}
		if summary.HorizonMax == nil || episode.Horizon > *summary.HorizonMax {
			value := episode.Horizon
			summary.HorizonMax = &value
		}
	}
	if len(bySplit) == 0 {
		return anyJevNanoJevMazeSummaryImport{}, nil, errors.New("AnyJev NanoJev maze result has no split groups")
	}
	all := anyJevNanoJevMazeSummaryImport{}
	splits := make([]anyJevNanoJevMazeSplitImport, 0, len(bySplit))
	for split, summary := range bySplit {
		finishAnyJevNanoJevMazeSummary(summary, bySizes[split])
		mergeAnyJevNanoJevMazeSummary(&all, *summary)
		splits = append(splits, anyJevNanoJevMazeSplitImport{Split: split, Summary: *summary})
	}
	finishAnyJevNanoJevMazeSummary(&all, mergeAnyJevNanoJevMazeSizes(bySizes))
	sort.Slice(splits, func(i, j int) bool { return splits[i].Split < splits[j].Split })
	return all, splits, nil
}

func finishAnyJevNanoJevMazeSummary(summary *anyJevNanoJevMazeSummaryImport, sizes map[int]struct{}) {
	if summary.Episodes > 0 {
		summary.GoalCompletionRate = float64(summary.GoalCompleted) / float64(summary.Episodes)
	}
	if summary.AtomicQuestions > 0 {
		denominator := float64(summary.AtomicQuestions)
		summary.AtomicAccuracy /= denominator
		summary.AtomicBrier /= denominator
		summary.AtomicNLL /= denominator
	}
	for size := range sizes {
		summary.MazeSizes = append(summary.MazeSizes, size)
	}
	sort.Ints(summary.MazeSizes)
}

func mergeAnyJevNanoJevMazeSummary(into *anyJevNanoJevMazeSummaryImport, value anyJevNanoJevMazeSummaryImport) {
	into.Episodes += value.Episodes
	into.GoalCompleted += value.GoalCompleted
	into.Attempts += value.Attempts
	into.SuccessfulMoves += value.SuccessfulMoves
	into.Collisions += value.Collisions
	into.ProbeCount += value.ProbeCount
	into.FallbackCount += value.FallbackCount
	into.VerifiedEdgeRepositionCount += value.VerifiedEdgeRepositionCount
	into.VisitedCells += value.VisitedCells
	into.RevisitedCellEntries += value.RevisitedCellEntries
	into.PredictionSites += value.PredictionSites
	into.AtomicQuestions += value.AtomicQuestions
	into.AtomicAccuracy += value.AtomicAccuracy * float64(value.AtomicQuestions)
	into.AtomicBrier += value.AtomicBrier * float64(value.AtomicQuestions)
	into.AtomicNLL += value.AtomicNLL * float64(value.AtomicQuestions)
	if value.HorizonMin != nil && (into.HorizonMin == nil || *value.HorizonMin < *into.HorizonMin) {
		v := *value.HorizonMin
		into.HorizonMin = &v
	}
	if value.HorizonMax != nil && (into.HorizonMax == nil || *value.HorizonMax > *into.HorizonMax) {
		v := *value.HorizonMax
		into.HorizonMax = &v
	}
}

func mergeAnyJevNanoJevMazeSizes(groups map[string]map[int]struct{}) map[int]struct{} {
	all := make(map[int]struct{})
	for _, sizes := range groups {
		for size := range sizes {
			all[size] = struct{}{}
		}
	}
	return all
}

func reconcileAnyJevNanoJevMazeSummary(value any, computed anyJevNanoJevMazeSummaryImport) error {
	summary, ok := value.(map[string]any)
	if !ok || len(summary) != len(anyJevNanoJevMazeSummaryFields) {
		return errors.New("AnyJev NanoJev maze source summary is incomplete")
	}
	for key := range summary {
		if !anyJevNanoJevMazeSummaryFields[key] {
			return fmt.Errorf("unsupported AnyJev NanoJev maze summary field %q", key)
		}
	}
	intFields := []struct {
		name string
		got  int
	}{
		{"episodes", computed.Episodes}, {"goal_completed", computed.GoalCompleted},
		{"attempts", computed.Attempts}, {"successful_moves", computed.SuccessfulMoves},
		{"collisions", computed.Collisions}, {"probe_count", computed.ProbeCount},
		{"fallback_count", computed.FallbackCount},
		{"verified_edge_reposition_count", computed.VerifiedEdgeRepositionCount},
		{"atomic_questions", computed.AtomicQuestions}, {"prediction_sites", computed.PredictionSites},
	}
	for _, field := range intFields {
		got, ok := boundedAnyJevInteger(summary[field.name], 0, 1e12)
		if !ok || got != field.got {
			return fmt.Errorf("AnyJev NanoJev maze summary %q does not reconcile with episodes", field.name)
		}
	}
	if computed.Attempts != computed.SuccessfulMoves+computed.Collisions ||
		computed.AtomicQuestions != computed.PredictionSites*4 {
		return errors.New("AnyJev NanoJev maze aggregate counters are inconsistent")
	}
	goalRate, ok := boundedAnyJevNumber(summary["goal_completion_rate"], 0, 1)
	if !ok || absAnyJevFloat(goalRate-computed.GoalCompletionRate) > 1e-6 {
		return errors.New("AnyJev NanoJev maze goal-completion rate does not reconcile")
	}
	metrics := []struct {
		name string
		got  float64
		min  float64
		max  float64
	}{
		{"atomic_accuracy", computed.AtomicAccuracy, 0, 1},
		{"atomic_brier", computed.AtomicBrier, 0, 1},
		{"atomic_nll", computed.AtomicNLL, 0, 1e6},
	}
	for _, metric := range metrics {
		got, ok := boundedAnyJevNumber(summary[metric.name], metric.min, metric.max)
		if !ok || absAnyJevFloat(got-metric.got) > 2e-6 {
			return fmt.Errorf("AnyJev NanoJev maze summary %q does not reconcile with episode-weighted metrics", metric.name)
		}
	}
	questions, questionsOK := boundedAnyJevInteger(summary["edge_questions"], 1, 1e12)
	if !questionsOK || questions != computed.AtomicQuestions {
		return errors.New("AnyJev NanoJev maze edge-question count does not reconcile")
	}
	return nil
}

func validAnyJevSHA256(value any) bool {
	text, ok := value.(string)
	return ok && validAnyJevHex(text, 64)
}

func validAnyJevHex(value string, length int) bool {
	if len(value) != length || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func absAnyJevFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
