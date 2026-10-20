package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const anyJevDemoGameResultSchema = "threadforge.anyjev-demo-game-result.v1"

type anyJevDemoGameImport struct {
	SchemaVersion       string                   `json:"schema_version"`
	Game                string                   `json:"game"`
	ModelSHA256         string                   `json:"model_sha256"`
	Levels              []anyJevDemoGameLevel    `json:"levels"`
	Baselines           []anyJevDemoGameBaseline `json:"baselines"`
	Episodes            int                      `json:"episodes"`
	Board               []int                    `json:"board,omitempty"`
	OracleDepth         int                      `json:"oracle_depth,omitempty"`
	Preview             *bool                    `json:"preview,omitempty"`
	OmittedFields       []string                 `json:"omitted_fields"`
	HumanReviewRequired bool                     `json:"human_review_required"`
	ExecutionVerified   bool                     `json:"execution_verified"`
	CutoverEligible     bool                     `json:"cutover_evidence_eligible"`
}

type anyJevDemoGameLevel struct {
	Level            string             `json:"level"`
	Decisions        int                `json:"decisions"`
	OracleAgreement  float64            `json:"oracle_agreement"`
	Regret           float64            `json:"regret"`
	ECE              float64            `json:"ece"`
	Brier            float64            `json:"brier"`
	FlipRate         *float64           `json:"flip_rate,omitempty"`
	IllegalMass      *float64           `json:"illegal_mass,omitempty"`
	OutcomeSummaries map[string]float64 `json:"outcome_summaries"`
}

type anyJevDemoGameBaseline struct {
	Kind    string             `json:"kind"`
	Games   int                `json:"games"`
	Metrics map[string]float64 `json:"metrics"`
}

type anyJevDemoGameOutcome struct {
	score   float64
	maxTile int
	moves   int
	won     float64
	cleared float64
	turns   int
	death   bool
}

func anyJevDemoGameJSONPath(relative string) (string, bool) {
	clean := strings.Trim(filepathSlash(relative), "/")
	parts := strings.Split(clean, "/")
	if len(parts) != 3 || !strings.EqualFold(parts[0], "demo") || !strings.EqualFold(parts[1], "results") {
		return "", false
	}
	name := strings.ToLower(parts[2])
	if !strings.HasSuffix(name, ".json") {
		return "", false
	}
	switch {
	case strings.HasPrefix(name, "twenty48_") && len(name) > len("twenty48_.json"):
		return "2048", true
	case strings.HasPrefix(name, "minesweeper_") && len(name) > len("minesweeper_.json"):
		return "minesweeper", true
	default:
		return "", false
	}
}

func isAnyJevDemoGameJSONPath(relative string) bool {
	_, ok := anyJevDemoGameJSONPath(relative)
	return ok
}

func normalizeAnyJevDemoGameResult(relative string, value any) (normalizedItem, error) {
	pathGame, recognized := anyJevDemoGameJSONPath(relative)
	if !recognized {
		return normalizedItem{}, errors.New("unsupported AnyJev demo-game result path")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return normalizedItem{}, errors.New("unsupported AnyJev demo-game result shape")
	}
	game, ok := root["game"].(string)
	if !ok || game != pathGame {
		return normalizedItem{}, errors.New("AnyJev demo-game identity does not match its path")
	}
	required := []string{"baselines", "game", "games", "levels", "model", "summary"}
	allowed := append([]string{}, required...)
	switch game {
	case "2048":
		required = append(required, "oracle_depth")
		allowed = append(allowed, "oracle_depth", "preview")
	case "minesweeper":
		required = append(required, "board")
		allowed = append(allowed, "board")
	default:
		return normalizedItem{}, errors.New("unsupported AnyJev demo game")
	}
	if err := requireAnyJevDemoFields(root, required, allowed); err != nil {
		return normalizedItem{}, err
	}
	model, ok := boundedAnyJevBenchmarkString(root["model"], 2048)
	if !ok || privacy.ContainsCredentialText(model) {
		return normalizedItem{}, errors.New("AnyJev demo-game model identity is invalid")
	}
	modelLabel := model
	if slash := strings.LastIndexAny(modelLabel, "/\\"); slash >= 0 {
		modelLabel = modelLabel[slash+1:]
	}
	if modelLabel == "" {
		return normalizedItem{}, errors.New("AnyJev demo-game model label is empty")
	}

	levelValues, ok := root["levels"].([]any)
	if !ok || len(levelValues) == 0 || len(levelValues) > 3 {
		return normalizedItem{}, errors.New("AnyJev demo-game level list is invalid")
	}
	levels := make([]string, 0, len(levelValues))
	policyLabels := make(map[string]string, len(levelValues))
	seenLevels := make(map[string]struct{}, len(levelValues))
	for _, raw := range levelValues {
		level, ok := raw.(string)
		if !ok || !oneOf(level, "raw", "L0", "L1") {
			return normalizedItem{}, errors.New("AnyJev demo-game level is unsupported")
		}
		if _, duplicate := seenLevels[level]; duplicate {
			return normalizedItem{}, errors.New("AnyJev demo-game levels contain duplicates")
		}
		seenLevels[level] = struct{}{}
		levels = append(levels, level)
		policyLabels[level] = level + " · " + modelLabel
	}

	runs, ok := root["games"].([]any)
	if !ok || len(runs) == 0 || len(runs) > 1000 {
		return normalizedItem{}, errors.New("AnyJev demo-game episode list is invalid")
	}
	perLevel := make(map[string][]anyJevDemoGameOutcome, len(levels))
	decisionTotals := make(map[string]int, len(levels))
	seeds := make(map[int]struct{}, len(runs))
	for _, level := range levels {
		perLevel[level] = make([]anyJevDemoGameOutcome, 0, len(runs))
	}
	for _, rawRun := range runs {
		run, ok := rawRun.(map[string]any)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev demo-game episode is invalid")
		}
		runRequired := append([]string{}, valuesOfMap(policyLabels)...)
		runAllowed := append([]string{}, runRequired...)
		runRequired = append(runRequired, "seed")
		runAllowed = append(runAllowed, "seed")
		if game == "2048" {
			runRequired = append(runRequired, "steps")
			runAllowed = append(runAllowed, "steps")
			steps, valid := boundedAnyJevInteger(run["steps"], 0, 1000000)
			if !valid {
				return normalizedItem{}, errors.New("AnyJev 2048 episode step count is invalid")
			}
			_ = steps
		}
		if err := requireAnyJevDemoFields(run, runRequired, runAllowed); err != nil {
			return normalizedItem{}, err
		}
		seed, valid := boundedAnyJevInteger(run["seed"], 0, 1000000000)
		if !valid {
			return normalizedItem{}, errors.New("AnyJev demo-game episode seed is invalid")
		}
		if _, duplicate := seeds[seed]; duplicate {
			return normalizedItem{}, errors.New("AnyJev demo-game episode seeds contain duplicates")
		}
		seeds[seed] = struct{}{}
		for _, level := range levels {
			outcome, err := normalizeAnyJevDemoGameOutcome(game, run[policyLabels[level]], true)
			if err != nil {
				return normalizedItem{}, err
			}
			perLevel[level] = append(perLevel[level], outcome)
			if game == "2048" {
				decisionTotals[level] += outcome.moves
			} else {
				decisionTotals[level] += outcome.turns
			}
		}
	}

	summary, ok := root["summary"].(map[string]any)
	if !ok || len(summary) != len(levels) {
		return normalizedItem{}, errors.New("AnyJev demo-game summary does not match its levels")
	}
	for _, label := range policyLabels {
		if _, exists := summary[label]; !exists {
			return normalizedItem{}, errors.New("AnyJev demo-game summary omits a declared level")
		}
	}
	for label := range summary {
		if !anyJevDemoContainsLabel(policyLabels, label) {
			return normalizedItem{}, errors.New("AnyJev demo-game summary contains an unsupported policy")
		}
	}

	projected := anyJevDemoGameImport{
		SchemaVersion: anyJevDemoGameResultSchema, Game: game, ModelSHA256: anyJevStringSHA256(model),
		Levels: []anyJevDemoGameLevel{}, Baselines: []anyJevDemoGameBaseline{}, Episodes: len(runs),
		OmittedFields: []string{
			"model_label", "episode_seeds", "environment_details", "board_state", "game_history",
			"per_action_predictions", "death_details",
		},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEligible: false,
	}
	if rawPreview, exists := root["preview"]; exists {
		preview, ok := rawPreview.(bool)
		if !ok {
			return normalizedItem{}, errors.New("AnyJev 2048 preview flag is invalid")
		}
		projected.Preview = &preview
	}
	if game == "2048" {
		depth, valid := boundedAnyJevInteger(root["oracle_depth"], 1, 16)
		if !valid {
			return normalizedItem{}, errors.New("AnyJev 2048 oracle depth is invalid")
		}
		projected.OracleDepth = depth
	} else {
		boardRaw, ok := root["board"].([]any)
		if !ok || len(boardRaw) != 3 {
			return normalizedItem{}, errors.New("AnyJev Minesweeper board dimensions are invalid")
		}
		for index, raw := range boardRaw {
			maximum := 100
			minimum := 2
			if index == 2 {
				minimum, maximum = 1, 9999
			}
			value, valid := boundedAnyJevInteger(raw, minimum, maximum)
			if !valid {
				return normalizedItem{}, errors.New("AnyJev Minesweeper board dimension is invalid")
			}
			projected.Board = append(projected.Board, value)
		}
		if projected.Board[2] >= projected.Board[0]*projected.Board[1] {
			return normalizedItem{}, errors.New("AnyJev Minesweeper mine count exceeds its board")
		}
	}

	for _, level := range levels {
		stats, err := normalizeAnyJevDemoGameSummary(game, summary[policyLabels[level]])
		if err != nil {
			return normalizedItem{}, err
		}
		if stats.decisions != decisionTotals[level] {
			return normalizedItem{}, fmt.Errorf("AnyJev demo-game decision count does not reconcile for level %s", level)
		}
		outcomes := summarizeAnyJevDemoGameOutcomes(game, perLevel[level])
		projected.Levels = append(projected.Levels, anyJevDemoGameLevel{
			Level: level, Decisions: stats.decisions, OracleAgreement: stats.oracleAgreement,
			Regret: stats.regret, ECE: stats.ece, Brier: stats.brier,
			FlipRate: stats.flipRate, IllegalMass: stats.illegalMass, OutcomeSummaries: outcomes,
		})
	}

	baselinesRaw, ok := root["baselines"].(map[string]any)
	if !ok || len(baselinesRaw) > 3 {
		return normalizedItem{}, errors.New("AnyJev demo-game baseline collection is invalid")
	}
	baselineNames := make([]string, 0, len(baselinesRaw))
	for name := range baselinesRaw {
		if (game == "2048" && !oneOf(name, "random", "first-listed", "oracle")) ||
			(game == "minesweeper" && !oneOf(name, "random", "oracle")) {
			return normalizedItem{}, errors.New("AnyJev demo-game baseline name is unsupported")
		}
		baselineNames = append(baselineNames, name)
	}
	sort.Strings(baselineNames)
	for _, name := range baselineNames {
		baselineRuns, ok := baselinesRaw[name].([]any)
		if !ok || len(baselineRuns) != len(runs) {
			return normalizedItem{}, errors.New("AnyJev demo-game baseline episode count does not reconcile")
		}
		outcomes := make([]anyJevDemoGameOutcome, 0, len(baselineRuns))
		for _, rawRun := range baselineRuns {
			outcome, err := normalizeAnyJevDemoGameOutcome(game, rawRun, false)
			if err != nil {
				return normalizedItem{}, err
			}
			outcomes = append(outcomes, outcome)
		}
		projected.Baselines = append(projected.Baselines, anyJevDemoGameBaseline{
			Kind: name, Games: len(outcomes), Metrics: summarizeAnyJevDemoGameOutcomes(game, outcomes),
		})
	}

	encoded, err := json.Marshal(projected)
	if err != nil {
		return normalizedItem{}, err
	}
	if privacy.ContainsCredential(encoded) {
		return normalizedItem{}, errors.New("AnyJev demo-game projection contains credential-like data")
	}
	return normalizedItem{Type: "decision_label_or_evaluation", Payload: encoded}, nil
}

type anyJevDemoGameStats struct {
	decisions       int
	oracleAgreement float64
	regret          float64
	ece             float64
	brier           float64
	flipRate        *float64
	illegalMass     *float64
}

func normalizeAnyJevDemoGameSummary(game string, value any) (anyJevDemoGameStats, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return anyJevDemoGameStats{}, errors.New("AnyJev demo-game policy summary is invalid")
	}
	expected := []string{"agree", "brier", "decisions", "ece", "regret"}
	if game == "2048" {
		expected = append(expected, "flip", "illegal_mass")
	}
	if err := requireAnyJevDemoFields(object, expected, expected); err != nil {
		return anyJevDemoGameStats{}, err
	}
	decisions, ok := boundedAnyJevInteger(object["decisions"], 1, 10000000)
	if !ok {
		return anyJevDemoGameStats{}, errors.New("AnyJev demo-game policy decision count is invalid")
	}
	agree, agreeOK := boundedAnyJevNumber(object["agree"], 0, 1)
	regret, regretOK := boundedAnyJevNumber(object["regret"], 0, 1)
	ece, eceOK := boundedAnyJevNumber(object["ece"], 0, 1)
	brier, brierOK := boundedAnyJevNumber(object["brier"], 0, 2)
	if !agreeOK || !regretOK || !eceOK || !brierOK {
		return anyJevDemoGameStats{}, errors.New("AnyJev demo-game policy metric is invalid")
	}
	stats := anyJevDemoGameStats{decisions: decisions, oracleAgreement: agree, regret: regret, ece: ece, brier: brier}
	if game == "2048" {
		flip, flipOK := boundedAnyJevNumber(object["flip"], 0, 1)
		illegalMass, illegalOK := boundedAnyJevNumber(object["illegal_mass"], 0, 1)
		if !flipOK || !illegalOK {
			return anyJevDemoGameStats{}, errors.New("AnyJev 2048 policy metric is invalid")
		}
		stats.flipRate, stats.illegalMass = &flip, &illegalMass
	}
	return stats, nil
}

func normalizeAnyJevDemoGameOutcome(game string, value any, includeDeath bool) (anyJevDemoGameOutcome, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return anyJevDemoGameOutcome{}, errors.New("AnyJev demo-game outcome row is invalid")
	}
	if game == "2048" {
		if err := requireAnyJevDemoFields(object, []string{"max_tile", "moves", "score"}, []string{"max_tile", "moves", "score"}); err != nil {
			return anyJevDemoGameOutcome{}, err
		}
		score, scoreOK := boundedAnyJevNumber(object["score"], 0, 1000000000)
		maxTile, tileOK := boundedAnyJevInteger(object["max_tile"], 0, 1000000000)
		moves, movesOK := boundedAnyJevInteger(object["moves"], 0, 1000000)
		if !scoreOK || !tileOK || !movesOK {
			return anyJevDemoGameOutcome{}, errors.New("AnyJev 2048 outcome metric is invalid")
		}
		return anyJevDemoGameOutcome{score: score, maxTile: maxTile, moves: moves}, nil
	}
	required := []string{"cleared", "turns", "won"}
	allowed := append([]string{}, required...)
	if includeDeath {
		required = append(required, "death")
		allowed = append(allowed, "death")
	}
	if err := requireAnyJevDemoFields(object, required, allowed); err != nil {
		return anyJevDemoGameOutcome{}, err
	}
	wonValue, ok := object["won"].(bool)
	if !ok {
		return anyJevDemoGameOutcome{}, errors.New("AnyJev Minesweeper win outcome is invalid")
	}
	cleared, clearedOK := boundedAnyJevNumber(object["cleared"], 0, 1)
	turns, turnsOK := boundedAnyJevInteger(object["turns"], 0, 1000000)
	if !clearedOK || !turnsOK {
		return anyJevDemoGameOutcome{}, errors.New("AnyJev Minesweeper outcome metric is invalid")
	}
	death := false
	if includeDeath {
		deathValue := object["death"]
		if deathValue != nil {
			deathMap, ok := deathValue.(map[string]any)
			if !ok || len(deathMap) > 16 {
				return anyJevDemoGameOutcome{}, errors.New("AnyJev Minesweeper death record is invalid")
			}
			death = true
		}
	}
	win := 0.0
	if wonValue {
		win = 1
	}
	return anyJevDemoGameOutcome{won: win, cleared: cleared, turns: turns, death: death}, nil
}

func summarizeAnyJevDemoGameOutcomes(game string, outcomes []anyJevDemoGameOutcome) map[string]float64 {
	metrics := make(map[string]float64)
	if len(outcomes) == 0 {
		return metrics
	}
	if game == "2048" {
		scores := make([]float64, 0, len(outcomes))
		var scoreTotal, moveTotal float64
		maxTile := 0
		for _, outcome := range outcomes {
			scores = append(scores, outcome.score)
			scoreTotal += outcome.score
			moveTotal += float64(outcome.moves)
			if outcome.maxTile > maxTile {
				maxTile = outcome.maxTile
			}
		}
		mean := scoreTotal / float64(len(scores))
		var squared float64
		for _, score := range scores {
			delta := score - mean
			squared += delta * delta
		}
		metrics["mean_score"] = mean
		metrics["score_population_stddev"] = math.Sqrt(squared / float64(len(scores)))
		metrics["maximum_tile"] = float64(maxTile)
		metrics["mean_moves"] = moveTotal / float64(len(outcomes))
		return metrics
	}
	var wins, cleared, deaths, turns float64
	for _, outcome := range outcomes {
		wins += outcome.won
		cleared += outcome.cleared
		turns += float64(outcome.turns)
		if outcome.death {
			deaths++
		}
	}
	count := float64(len(outcomes))
	metrics["win_rate"] = wins / count
	metrics["clear_rate"] = cleared / count
	metrics["mean_turns"] = turns / count
	metrics["deaths"] = deaths
	return metrics
}

func requireAnyJevDemoFields(object map[string]any, required, allowed []string) error {
	allowedFields := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowedFields[field] = struct{}{}
	}
	for _, field := range required {
		if _, ok := object[field]; !ok {
			return fmt.Errorf("AnyJev demo-game object is missing %q", field)
		}
	}
	for field := range object {
		if _, ok := allowedFields[field]; !ok {
			return fmt.Errorf("unsupported AnyJev demo-game field %q", field)
		}
	}
	return nil
}

func valuesOfMap(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for _, value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

func anyJevDemoContainsLabel(labels map[string]string, value string) bool {
	for _, label := range labels {
		if label == value {
			return true
		}
	}
	return false
}
