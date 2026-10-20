package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevNativeMazeResultSchema = "threadforge.anyjev-game-maze-result.v1"

var anyJevNativeMazeDirections = []string{"north", "east", "south", "west"}

var anyJevNativeMazeDelta = map[string]anyJevMazePosition{
	"north": {-1, 0}, "east": {0, 1}, "south": {1, 0}, "west": {0, -1},
}

var anyJevNativeMazeRootFields = map[string]bool{
	"game": true, "model": true, "revision": true, "size": true, "prior": true,
	"policy": true, "wall_cost": true, "threshold": true, "calib_n": true,
	"calib_seed0": true, "questions": true, "environment": true, "episodes": true,
}

type anyJevNativeMazeImport struct {
	SchemaVersion       string                           `json:"schema_version"`
	ModelSHA256         string                           `json:"model_sha256"`
	RevisionSHA256      string                           `json:"revision_sha256,omitempty"`
	MazeSize            int                              `json:"maze_size"`
	Prior               string                           `json:"prior"`
	Policy              string                           `json:"policy"`
	WallCost            int                              `json:"wall_cost"`
	Threshold           float64                          `json:"threshold"`
	CalibrationExamples int                              `json:"calibration_examples"`
	QuestionDigests     []anyJevNativeMazeQuestionDigest `json:"question_digests"`
	Levels              []string                         `json:"levels"`
	Episodes            []anyJevNativeMazeEpisode        `json:"episodes"`
	OmittedFields       []string                         `json:"omitted_fields"`
	HumanReviewRequired bool                             `json:"human_review_required"`
	ExecutionVerified   bool                             `json:"execution_verified"`
	CutoverEligible     bool                             `json:"cutover_evidence_eligible"`
}

type anyJevNativeMazeQuestionDigest struct {
	Direction string `json:"direction"`
	SHA256    string `json:"sha256"`
}

type anyJevNativeMazeEpisode struct {
	SeedSHA256 string                `json:"seed_sha256"`
	Runs       []anyJevNativeMazeRun `json:"runs"`
}

type anyJevNativeMazeRun struct {
	Level          string             `json:"level"`
	Status         string             `json:"status"`
	Steps          int                `json:"steps"`
	Collisions     int                `json:"collisions"`
	CellsSeen      int                `json:"cells_seen"`
	ElapsedSeconds float64            `json:"elapsed_seconds"`
	EdgeAccuracy   float64            `json:"edge_accuracy"`
	EdgeBrier      float64            `json:"edge_brier"`
	MeanPOpen      float64            `json:"mean_p_open"`
	OpenRate       float64            `json:"open_rate"`
	Temperatures   map[string]float64 `json:"temperatures,omitempty"`
}

type anyJevMazePosition struct {
	row int
	col int
}

type anyJevMazeMoveKey struct {
	position  anyJevMazePosition
	direction string
}

type anyJevMazePrediction struct {
	step  int
	pos   anyJevMazePosition
	open  map[string]float64
	truth map[string]bool
}

func isAnyJevNativeMazeJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepathSlash(relative), "/"))
	if !strings.HasPrefix(path, "bench/results_games/") || !strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".items.json") {
		return false
	}
	name := path[strings.LastIndex(path, "/")+1:]
	return strings.HasPrefix(name, "maze.")
}

func normalizeAnyJevNativeMazeResult(value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok || len(root) != len(anyJevNativeMazeRootFields) {
		return normalizedItem{}, errors.New("unsupported AnyJev native maze result shape")
	}
	for key := range root {
		if !anyJevNativeMazeRootFields[key] {
			return normalizedItem{}, fmt.Errorf("unsupported AnyJev native maze result field %q", key)
		}
	}
	game, gameOK := root["game"].(string)
	model, modelOK := boundedAnyJevBenchmarkString(root["model"], 2048)
	if !gameOK || game != "maze" || !modelOK {
		return normalizedItem{}, errors.New("AnyJev native maze game or model is invalid")
	}
	var revisionHash string
	if revisionValue := root["revision"]; revisionValue != nil {
		revision, ok := boundedAnyJevBenchmarkString(revisionValue, 2048)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev native maze revision is invalid")
		}
		revisionHash = anyJevStringSHA256(revision)
	}
	size, sizeOK := boundedAnyJevInteger(root["size"], 5, 501)
	prior, priorOK := root["prior"].(string)
	policy, policyOK := root["policy"].(string)
	wallCost, wallCostOK := boundedAnyJevInteger(root["wall_cost"], 0, 1000000)
	threshold, thresholdOK := boundedAnyJevNumber(root["threshold"], 0, 1)
	calibrationExamples, calibrationOK := boundedAnyJevInteger(root["calib_n"], 0, 10000000)
	calibrationSeed, calibrationSeedOK := boundedAnyJevInteger(root["calib_seed0"], 0, 1000000000)
	if !sizeOK || size%2 == 0 || !oneOf(prior, "batch", "content_free", "none") ||
		!priorOK || !policyOK || !oneOf(policy, "expected_cost", "threshold") || !wallCostOK ||
		!thresholdOK || !calibrationOK || !calibrationSeedOK || calibrationSeed != 10000 {
		return normalizedItem{}, errors.New("AnyJev native maze run settings are invalid")
	}
	environment, environmentOK := root["environment"].(map[string]any)
	if !environmentOK || len(environment) == 0 || len(environment) > 256 {
		return normalizedItem{}, errors.New("AnyJev native maze environment metadata is invalid")
	}
	questions, err := normalizeAnyJevNativeMazeQuestions(root["questions"])
	if err != nil {
		return normalizedItem{}, err
	}
	episodesRaw, ok := root["episodes"].([]any)
	if !ok || len(episodesRaw) == 0 || len(episodesRaw) > 1000 || len(episodesRaw)*size*size > 1000000 {
		return normalizedItem{}, errors.New("AnyJev native maze episodes are invalid or exceed bounds")
	}
	episodes := make([]anyJevNativeMazeEpisode, 0, len(episodesRaw))
	seenSeeds := make(map[int]struct{}, len(episodesRaw))
	var canonicalLevels []string
	horizon := 4 * size * size
	for _, rawEpisode := range episodesRaw {
		episode, levels, err := normalizeAnyJevNativeMazeEpisode(rawEpisode, size, wallCost, horizon)
		if err != nil {
			return normalizedItem{}, err
		}
		if canonicalLevels == nil {
			canonicalLevels = levels
		} else if !anyJevNativeMazeEqualLevels(canonicalLevels, levels) {
			return normalizedItem{}, errors.New("AnyJev native maze episodes do not use a consistent readout set")
		}
		seedValue := rawEpisode.(map[string]any)["seed"]
		seed, _ := boundedAnyJevInteger(seedValue, 0, 1000000000)
		if _, duplicate := seenSeeds[seed]; duplicate {
			return normalizedItem{}, errors.New("AnyJev native maze episode seeds contain duplicates")
		}
		seenSeeds[seed] = struct{}{}
		episodes = append(episodes, episode)
	}
	payload := anyJevNativeMazeImport{
		SchemaVersion: anyJevNativeMazeResultSchema, ModelSHA256: anyJevStringSHA256(model),
		RevisionSHA256: revisionHash, MazeSize: size, Prior: prior, Policy: policy,
		WallCost: wallCost, Threshold: threshold, CalibrationExamples: calibrationExamples,
		QuestionDigests: questions, Levels: canonicalLevels, Episodes: episodes,
		OmittedFields: []string{
			"model_label", "revision_label", "environment_details", "question_text", "calibration_seed",
			"episode_seed", "maze_walls", "start_goal_coordinates", "prediction_details", "move_trace",
		},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev native maze projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

func normalizeAnyJevNativeMazeQuestions(value any) ([]anyJevNativeMazeQuestionDigest, error) {
	questions, ok := value.(map[string]any)
	if !ok || len(questions) != len(anyJevNativeMazeDirections) {
		return nil, errors.New("AnyJev native maze question map is invalid")
	}
	digests := make([]anyJevNativeMazeQuestionDigest, 0, len(anyJevNativeMazeDirections))
	for _, direction := range anyJevNativeMazeDirections {
		text, ok := boundedAnyJevBenchmarkString(questions[direction], 4096)
		if !ok {
			return nil, errors.New("AnyJev native maze question text is invalid")
		}
		digests = append(digests, anyJevNativeMazeQuestionDigest{Direction: direction, SHA256: anyJevStringSHA256(text)})
	}
	for key := range questions {
		if !oneOf(key, anyJevNativeMazeDirections...) {
			return nil, errors.New("AnyJev native maze question map has an unknown direction")
		}
	}
	return digests, nil
}

func normalizeAnyJevNativeMazeEpisode(value any, size, wallCost, horizon int) (anyJevNativeMazeEpisode, []string, error) {
	episode, ok := value.(map[string]any)
	if !ok || len(episode) != 5 {
		return anyJevNativeMazeEpisode{}, nil, errors.New("AnyJev native maze episode shape is invalid")
	}
	allowed := map[string]bool{"seed": true, "walls": true, "start": true, "goal": true, "runs": true}
	for key := range episode {
		if !allowed[key] {
			return anyJevNativeMazeEpisode{}, nil, errors.New("AnyJev native maze episode has an unknown field")
		}
	}
	seed, seedOK := boundedAnyJevInteger(episode["seed"], 0, 1000000000)
	walls, err := normalizeAnyJevNativeMazeWalls(episode["walls"], size)
	if !seedOK || err != nil {
		return anyJevNativeMazeEpisode{}, nil, errors.New("AnyJev native maze episode seed or wall map is invalid")
	}
	start, startOK := anyJevNativeMazePositionFromJSON(episode["start"], size)
	goal, goalOK := anyJevNativeMazePositionFromJSON(episode["goal"], size)
	expectedStart := anyJevMazePosition{1, 1}
	expectedGoal := anyJevMazePosition{size - 2, size - 2}
	if !startOK || !goalOK || start != expectedStart || goal != expectedGoal || walls[start.row][start.col] || walls[goal.row][goal.col] {
		return anyJevNativeMazeEpisode{}, nil, errors.New("AnyJev native maze episode endpoints are invalid")
	}
	runs, ok := episode["runs"].(map[string]any)
	if !ok || len(runs) == 0 || len(runs) > 3 {
		return anyJevNativeMazeEpisode{}, nil, errors.New("AnyJev native maze readout set is invalid")
	}
	levels := make([]string, 0, len(runs))
	for level := range runs {
		if !oneOf(level, "raw", "L0", "L1") {
			return anyJevNativeMazeEpisode{}, nil, errors.New("AnyJev native maze readout level is unsupported")
		}
		levels = append(levels, level)
	}
	sort.Slice(levels, func(i, j int) bool {
		return anyJevNativeMazeLevelOrder(levels[i]) < anyJevNativeMazeLevelOrder(levels[j])
	})
	convertedRuns := make([]anyJevNativeMazeRun, 0, len(levels))
	for _, level := range levels {
		converted, err := normalizeAnyJevNativeMazeRun(runs[level], level, walls, start, goal, size, wallCost, horizon)
		if err != nil {
			return anyJevNativeMazeEpisode{}, nil, err
		}
		convertedRuns = append(convertedRuns, converted)
	}
	return anyJevNativeMazeEpisode{
		SeedSHA256: anyJevStringSHA256(strconv.Itoa(seed)), Runs: convertedRuns,
	}, levels, nil
}

func normalizeAnyJevNativeMazeWalls(value any, size int) ([][]bool, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) != size {
		return nil, errors.New("AnyJev native maze wall grid has invalid row count")
	}
	walls := make([][]bool, size)
	for rowIndex, raw := range rows {
		row, ok := raw.(string)
		if !ok || len(row) != size {
			return nil, errors.New("AnyJev native maze wall grid has an invalid row")
		}
		walls[rowIndex] = make([]bool, size)
		for colIndex, tile := range row {
			if tile != '#' && tile != '.' {
				return nil, errors.New("AnyJev native maze wall grid has an invalid tile")
			}
			walls[rowIndex][colIndex] = tile == '#'
			if (rowIndex == 0 || colIndex == 0 || rowIndex == size-1 || colIndex == size-1) && tile != '#' {
				return nil, errors.New("AnyJev native maze boundary contains an open tile")
			}
		}
	}
	return walls, nil
}

func normalizeAnyJevNativeMazeRun(value any, level string, walls [][]bool, start, goal anyJevMazePosition, size, wallCost, horizon int) (anyJevNativeMazeRun, error) {
	run, ok := value.(map[string]any)
	if !ok || len(run) < 12 || len(run) > 13 {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze run shape is invalid")
	}
	allowed := map[string]bool{
		"level": true, "status": true, "steps": true, "collisions": true, "cells_seen": true,
		"elapsed_seconds": true, "edge_accuracy": true, "edge_brier": true, "mean_p_open": true,
		"open_rate": true, "predictions": true, "moves": true, "temperatures": true,
	}
	for key := range run {
		if !allowed[key] {
			return anyJevNativeMazeRun{}, errors.New("AnyJev native maze run has an unknown field")
		}
	}
	if rawLevel, ok := run["level"].(string); !ok || rawLevel != level {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze run level does not match its key")
	}
	status, statusOK := run["status"].(string)
	steps, stepsOK := boundedAnyJevInteger(run["steps"], 0, horizon+wallCost)
	collisions, collisionsOK := boundedAnyJevInteger(run["collisions"], 0, horizon)
	cellsSeen, cellsSeenOK := boundedAnyJevInteger(run["cells_seen"], 1, size*size)
	elapsed, elapsedOK := boundedAnyJevNumber(run["elapsed_seconds"], 0, 1e9)
	edgeAccuracy, accuracyOK := boundedAnyJevNumber(run["edge_accuracy"], 0, 1)
	edgeBrier, brierOK := boundedAnyJevNumber(run["edge_brier"], 0, 1)
	meanPOpen, meanPOpenOK := boundedAnyJevNumber(run["mean_p_open"], 0, 1)
	openRate, openRateOK := boundedAnyJevNumber(run["open_rate"], 0, 1)
	if !statusOK || !oneOf(status, "goal", "horizon", "stuck") || !stepsOK || !collisionsOK ||
		!cellsSeenOK || !elapsedOK || !accuracyOK || !brierOK || !meanPOpenOK || !openRateOK {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze run metrics or status are invalid")
	}
	predictions, err := normalizeAnyJevNativeMazePredictions(run["predictions"], walls, size, horizon)
	if err != nil || len(predictions) != cellsSeen {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze prediction count is invalid")
	}
	moves, err := normalizeAnyJevNativeMazeMoves(run["moves"], predictions, walls, start, goal, size, wallCost, horizon, status)
	if err != nil {
		return anyJevNativeMazeRun{}, err
	}
	if moves.steps != steps || moves.collisions != collisions || len(predictions) != cellsSeen {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze movement counts do not reconcile")
	}
	if (status == "goal") != (moves.position == goal) {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze completion status does not match its endpoint")
	}
	if status == "horizon" && (moves.position == goal || moves.steps < horizon) {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze horizon status does not reconcile")
	}
	if status == "stuck" && (moves.position == goal || moves.steps >= horizon ||
		anyJevNativeMazeHasFrontier(moves.position, moves.openEdges, moves.visited, moves.tried, moves.predictedByPosition)) {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze stuck status has remaining reachable edges")
	}
	calculated := anyJevNativeMazeCalculateMetrics(predictions)
	if absAnyJevFloat(calculated.accuracy-edgeAccuracy) > 2e-6 || absAnyJevFloat(calculated.brier-edgeBrier) > 2e-6 ||
		absAnyJevFloat(calculated.meanPOpen-meanPOpen) > 2e-6 || absAnyJevFloat(calculated.openRate-openRate) > 2e-6 {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze prediction metrics do not reconcile")
	}
	converted := anyJevNativeMazeRun{
		Level: level, Status: status, Steps: steps, Collisions: collisions, CellsSeen: cellsSeen,
		ElapsedSeconds: elapsed, EdgeAccuracy: edgeAccuracy, EdgeBrier: edgeBrier,
		MeanPOpen: meanPOpen, OpenRate: openRate,
	}
	if level == "L1" {
		temperatures, err := normalizeAnyJevNativeMazeTemperatures(run["temperatures"])
		if err != nil {
			return anyJevNativeMazeRun{}, err
		}
		converted.Temperatures = temperatures
	} else if _, exists := run["temperatures"]; exists {
		return anyJevNativeMazeRun{}, errors.New("AnyJev native maze non-L1 run has temperatures")
	}
	return converted, nil
}

type anyJevNativeMazeMetrics struct {
	accuracy  float64
	brier     float64
	meanPOpen float64
	openRate  float64
}

func normalizeAnyJevNativeMazePredictions(value any, walls [][]bool, size, horizon int) ([]anyJevMazePrediction, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) == 0 || len(rows) > size*size {
		return nil, errors.New("AnyJev native maze predictions are invalid or exceed bounds")
	}
	predictions := make([]anyJevMazePrediction, 0, len(rows))
	seenPositions := make(map[anyJevMazePosition]struct{}, len(rows))
	lastStep := -1
	for index, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok || len(row) != 4 {
			return nil, errors.New("AnyJev native maze prediction shape is invalid")
		}
		allowed := map[string]bool{"step": true, "pos": true, "p_open": true, "truth": true}
		for key := range row {
			if !allowed[key] {
				return nil, errors.New("AnyJev native maze prediction has an unknown field")
			}
		}
		step, stepOK := boundedAnyJevInteger(row["step"], 0, horizon)
		position, positionOK := anyJevNativeMazePositionFromJSON(row["pos"], size)
		if !stepOK || !positionOK || (index == 0 && (step != 0 || position != (anyJevMazePosition{1, 1}))) || step <= lastStep {
			return nil, errors.New("AnyJev native maze prediction step or position is invalid")
		}
		if walls[position.row][position.col] {
			return nil, errors.New("AnyJev native maze prediction is located on a wall")
		}
		if _, duplicate := seenPositions[position]; duplicate {
			return nil, errors.New("AnyJev native maze predicts the same cell more than once")
		}
		seenPositions[position] = struct{}{}
		probabilities, err := normalizeAnyJevNativeMazeProbabilities(row["p_open"])
		if err != nil {
			return nil, err
		}
		truth, err := normalizeAnyJevNativeMazeTruth(row["truth"], walls, position, size)
		if err != nil {
			return nil, err
		}
		predictions = append(predictions, anyJevMazePrediction{step: step, pos: position, open: probabilities, truth: truth})
		lastStep = step
	}
	return predictions, nil
}

func normalizeAnyJevNativeMazeProbabilities(value any) (map[string]float64, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) != len(anyJevNativeMazeDirections) {
		return nil, errors.New("AnyJev native maze probability map is invalid")
	}
	probabilities := make(map[string]float64, len(rows))
	for _, direction := range anyJevNativeMazeDirections {
		probability, ok := boundedAnyJevNumber(rows[direction], 0, 1)
		if !ok {
			return nil, errors.New("AnyJev native maze probability is invalid")
		}
		probabilities[direction] = probability
	}
	for key := range rows {
		if !oneOf(key, anyJevNativeMazeDirections...) {
			return nil, errors.New("AnyJev native maze probability map has an unknown direction")
		}
	}
	return probabilities, nil
}

func normalizeAnyJevNativeMazeTruth(value any, walls [][]bool, position anyJevMazePosition, size int) (map[string]bool, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) != len(anyJevNativeMazeDirections) {
		return nil, errors.New("AnyJev native maze truth map is invalid")
	}
	truth := make(map[string]bool, len(rows))
	for _, direction := range anyJevNativeMazeDirections {
		observed, ok := rows[direction].(bool)
		if !ok || observed != anyJevNativeMazeIsOpen(walls, anyJevNativeMazeMove(position, direction), size) {
			return nil, errors.New("AnyJev native maze truth map does not match the source grid")
		}
		truth[direction] = observed
	}
	for key := range rows {
		if !oneOf(key, anyJevNativeMazeDirections...) {
			return nil, errors.New("AnyJev native maze truth map has an unknown direction")
		}
	}
	return truth, nil
}

type anyJevNativeMazeReplay struct {
	position            anyJevMazePosition
	steps               int
	collisions          int
	visited             map[anyJevMazePosition]bool
	tried               map[anyJevMazeMoveKey]bool
	openEdges           map[anyJevMazePosition]map[string]bool
	predictedByPosition map[anyJevMazePosition]anyJevMazePrediction
	consumedPredictions int
}

func normalizeAnyJevNativeMazeMoves(value any, predictions []anyJevMazePrediction, walls [][]bool, start, goal anyJevMazePosition, size, wallCost, horizon int, status string) (anyJevNativeMazeReplay, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) == 0 || len(rows) > horizon {
		return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze movement trace is invalid or exceeds bounds")
	}
	replay := anyJevNativeMazeReplay{
		position: start, visited: map[anyJevMazePosition]bool{start: true},
		tried: make(map[anyJevMazeMoveKey]bool), openEdges: make(map[anyJevMazePosition]map[string]bool),
		predictedByPosition: make(map[anyJevMazePosition]anyJevMazePrediction, len(predictions)),
	}
	predictionByPosition := make(map[anyJevMazePosition]anyJevMazePrediction, len(predictions))
	for _, prediction := range predictions {
		predictionByPosition[prediction.pos] = prediction
	}
	for _, raw := range rows {
		if _, predicted := replay.predictedByPosition[replay.position]; !predicted {
			prediction, exists := predictionByPosition[replay.position]
			if !exists || prediction.step != replay.steps || replay.consumedPredictions >= len(predictions) ||
				predictions[replay.consumedPredictions].pos != replay.position {
				return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze prediction ordering does not match movement")
			}
			replay.predictedByPosition[replay.position] = prediction
			replay.consumedPredictions++
		}
		if replay.steps >= horizon {
			return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze continues after its horizon")
		}
		move, ok := raw.(map[string]any)
		if !ok || len(move) != 5 {
			return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze movement event shape is invalid")
		}
		for key := range move {
			if key != "dir" && key != "kind" && key != "pos" && key != "hit" && key != "t" {
				return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze movement event has an unknown field")
			}
		}
		direction, directionOK := move["dir"].(string)
		kind, kindOK := move["kind"].(string)
		hit, hitOK := move["hit"].(bool)
		timestamp, timestampOK := boundedAnyJevInteger(move["t"], 1, horizon+wallCost)
		position, positionOK := anyJevNativeMazePositionFromJSON(move["pos"], size)
		if !directionOK || !oneOf(direction, anyJevNativeMazeDirections...) || !kindOK || !oneOf(kind, "try", "walk") ||
			!hitOK || !timestampOK || !positionOK {
			return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze movement event is invalid")
		}
		key := anyJevMazeMoveKey{position: replay.position, direction: direction}
		replay.tried[key] = true
		target := anyJevNativeMazeMove(replay.position, direction)
		targetOpen := anyJevNativeMazeIsOpen(walls, target, size)
		if hit == targetOpen || (hit && kind != "try") {
			return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze movement hit flag or kind does not match the source grid")
		}
		replay.steps++
		if hit {
			replay.collisions++
			replay.steps += wallCost
			if position != replay.position {
				return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze collision changed position")
			}
		} else {
			if position != target {
				return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze open move ended at the wrong position")
			}
			if replay.openEdges[replay.position] == nil {
				replay.openEdges[replay.position] = make(map[string]bool)
			}
			if replay.openEdges[target] == nil {
				replay.openEdges[target] = make(map[string]bool)
			}
			replay.openEdges[replay.position][direction] = true
			back := anyJevNativeMazeOpposite(direction)
			replay.openEdges[target][back] = true
			replay.tried[anyJevMazeMoveKey{position: target, direction: back}] = true
			replay.position = target
			replay.visited[target] = true
		}
		if timestamp != replay.steps {
			return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze movement timestamp does not reconcile")
		}
	}
	if status == "stuck" {
		if _, predicted := replay.predictedByPosition[replay.position]; !predicted {
			prediction, exists := predictionByPosition[replay.position]
			if !exists || prediction.step != replay.steps || replay.consumedPredictions >= len(predictions) ||
				predictions[replay.consumedPredictions].pos != replay.position {
				return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze terminal stuck prediction does not reconcile")
			}
			replay.predictedByPosition[replay.position] = prediction
			replay.consumedPredictions++
		}
	}
	if replay.consumedPredictions != len(predictions) {
		return anyJevNativeMazeReplay{}, errors.New("AnyJev native maze has unused prediction records")
	}
	return replay, nil
}

func anyJevNativeMazeHasFrontier(current anyJevMazePosition, edges map[anyJevMazePosition]map[string]bool, visited map[anyJevMazePosition]bool, tried map[anyJevMazeMoveKey]bool, predictions map[anyJevMazePosition]anyJevMazePrediction) bool {
	reachable := map[anyJevMazePosition]bool{current: true}
	queue := []anyJevMazePosition{current}
	for len(queue) > 0 {
		position := queue[0]
		queue = queue[1:]
		for direction := range edges[position] {
			next := anyJevNativeMazeMove(position, direction)
			if !reachable[next] {
				reachable[next] = true
				queue = append(queue, next)
			}
		}
	}
	for position := range reachable {
		if _, predicted := predictions[position]; !predicted {
			continue
		}
		for _, direction := range anyJevNativeMazeDirections {
			if tried[anyJevMazeMoveKey{position: position, direction: direction}] || visited[anyJevNativeMazeMove(position, direction)] {
				continue
			}
			return true
		}
	}
	return false
}

func anyJevNativeMazeCalculateMetrics(predictions []anyJevMazePrediction) anyJevNativeMazeMetrics {
	var correct, brier, probabilities, open float64
	count := float64(len(predictions) * len(anyJevNativeMazeDirections))
	for _, prediction := range predictions {
		for _, direction := range anyJevNativeMazeDirections {
			probability := prediction.open[direction]
			truth := prediction.truth[direction]
			predictedOpen := probability >= 0.5
			if predictedOpen == truth {
				correct++
			}
			target := 0.0
			if truth {
				target = 1
				open++
			}
			delta := probability - target
			brier += delta * delta
			probabilities += probability
		}
	}
	return anyJevNativeMazeMetrics{accuracy: correct / count, brier: brier / count, meanPOpen: probabilities / count, openRate: open / count}
}

func normalizeAnyJevNativeMazeTemperatures(value any) (map[string]float64, error) {
	rows, ok := value.(map[string]any)
	if !ok || len(rows) != len(anyJevNativeMazeDirections) {
		return nil, errors.New("AnyJev native maze L1 temperatures are invalid")
	}
	converted := make(map[string]float64, len(rows))
	for _, direction := range anyJevNativeMazeDirections {
		temperature, ok := boundedAnyJevNumber(rows[direction], 1e-9, 1e6)
		if !ok {
			return nil, errors.New("AnyJev native maze L1 temperature is invalid")
		}
		converted[direction] = temperature
	}
	for key := range rows {
		if !oneOf(key, anyJevNativeMazeDirections...) {
			return nil, errors.New("AnyJev native maze temperature map has an unknown direction")
		}
	}
	return converted, nil
}

func anyJevNativeMazePositionFromJSON(value any, size int) (anyJevMazePosition, bool) {
	values, ok := value.([]any)
	if !ok || len(values) != 2 {
		return anyJevMazePosition{}, false
	}
	row, rowOK := boundedAnyJevInteger(values[0], 0, size-1)
	col, colOK := boundedAnyJevInteger(values[1], 0, size-1)
	return anyJevMazePosition{row, col}, rowOK && colOK
}

func anyJevNativeMazeMove(position anyJevMazePosition, direction string) anyJevMazePosition {
	delta := anyJevNativeMazeDelta[direction]
	return anyJevMazePosition{position.row + delta.row, position.col + delta.col}
}

func anyJevNativeMazeIsOpen(walls [][]bool, position anyJevMazePosition, size int) bool {
	return position.row >= 0 && position.col >= 0 && position.row < size && position.col < size && !walls[position.row][position.col]
}

func anyJevNativeMazeOpposite(direction string) string {
	switch direction {
	case "north":
		return "south"
	case "east":
		return "west"
	case "south":
		return "north"
	default:
		return "east"
	}
}

func anyJevNativeMazeLevelOrder(level string) int {
	switch level {
	case "raw":
		return 0
	case "L0":
		return 1
	default:
		return 2
	}
}

func anyJevNativeMazeEqualLevels(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
