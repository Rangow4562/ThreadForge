package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const anyJevSyntheticStateSummarySchema = "threadforge.anyjev-distillation-synthetic-summary.v1"

const (
	maxSyntheticStates        = 10000
	maxSyntheticAttempts      = 100000
	maxSyntheticStateDepth    = 20
	maxSyntheticStateNodes    = 200000
	maxSyntheticStringBytes   = 1 << 20
	maxSyntheticShapeVariants = 64
)

type anyJevSyntheticStateSummary struct {
	SchemaVersion           string                  `json:"schema_version"`
	WorkflowSHA256          string                  `json:"workflow_sha256"`
	GeneratorSHA256         string                  `json:"generator_sha256"`
	RequestedStates         int                     `json:"requested_states"`
	GenerationAttempts      int                     `json:"generation_attempts"`
	RejectedGenerations     int                     `json:"rejected_generations"`
	StoredStates            int                     `json:"stored_states"`
	StateCollectionSHA256   string                  `json:"state_collection_sha256"`
	StateSchemaSHA256       string                  `json:"state_schema_sha256"`
	StateSchemaVariants     int                     `json:"state_schema_variants"`
	Settings                anyJevSyntheticSettings `json:"settings"`
	OmittedFields           []string                `json:"omitted_fields"`
	HumanReviewRequired     bool                    `json:"human_review_required"`
	ExecutionVerified       bool                    `json:"execution_verified"`
	CutoverEvidenceEligible bool                    `json:"cutover_evidence_eligible"`
}

type anyJevSyntheticSettings struct {
	Temperature        float64 `json:"temperature"`
	TopP               float64 `json:"top_p"`
	Seed               int64   `json:"seed"`
	MaxNewTokens       int     `json:"max_new_tokens"`
	ExemplarsPerPrompt int     `json:"exemplars_per_prompt"`
}

func isAnyJevDistillationSyntheticJSONPath(relative string) bool {
	path := strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	const prefix = "bench/results_distill/synth/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	tail := strings.TrimPrefix(path, prefix)
	return tail != "" && !strings.Contains(tail, "/") && strings.HasSuffix(tail, ".json")
}

func normalizeAnyJevDistillationSynthetic(relative string, value any) (normalizedItem, error) {
	root, ok := value.(map[string]any)
	if !ok || !isAnyJevDistillationSyntheticJSONPath(relative) {
		return normalizedItem{}, errors.New("AnyJev synthetic-state summary source is invalid")
	}
	allowed := map[string]bool{
		"workflow": true, "generator": true, "n": true, "generated": true, "rejected": true,
		"reject_reasons": true, "settings": true, "date": true, "env": true, "states": true,
	}
	for key := range root {
		if !allowed[key] {
			return normalizedItem{}, errors.New("AnyJev synthetic-state summary has unsupported fields")
		}
	}
	workflow, workflowOK := root["workflow"].(string)
	generator, generatorOK := root["generator"].(string)
	if !workflowOK || !generatorOK || !safeAnyJevSyntheticLabel(workflow) || !safeAnyJevSyntheticLabel(generator) ||
		!strings.EqualFold(filepath.Base(relative), workflow+".json") || !knownAnyJevSyntheticWorkflow(workflow) {
		return normalizedItem{}, errors.New("AnyJev synthetic-state workflow or generator binding is invalid")
	}
	requested, requestedOK := boundedAnyJevInteger(root["n"], 1, maxSyntheticStates)
	attempts, attemptsOK := boundedAnyJevInteger(root["generated"], 1, maxSyntheticAttempts)
	rejected, rejectedOK := boundedAnyJevInteger(root["rejected"], 0, maxSyntheticAttempts)
	states, statesOK := root["states"].([]any)
	if !requestedOK || !attemptsOK || !rejectedOK || !statesOK || len(states) != requested || attempts < requested || rejected > attempts {
		return normalizedItem{}, errors.New("AnyJev synthetic-state counts do not reconcile")
	}
	settings, err := normalizeAnyJevSyntheticSettings(root["settings"])
	if err != nil {
		return normalizedItem{}, err
	}
	date, dateOK := root["date"].(string)
	if !dateOK || len(date) > 128 || !utf8.ValidString(date) {
		return normalizedItem{}, errors.New("AnyJev synthetic-state date metadata is invalid")
	}
	environment, ok := root["env"].(map[string]any)
	if !ok || len(environment) > 256 {
		return normalizedItem{}, errors.New("AnyJev synthetic-state environment metadata is invalid")
	}
	environmentNodes := 0
	if err := validateAnyJevSyntheticValue(environment, 0, &environmentNodes); err != nil {
		return normalizedItem{}, errors.New("AnyJev synthetic-state environment metadata exceeds bounds")
	}
	if err := validateAnyJevSyntheticRejectReasons(root["reject_reasons"], rejected); err != nil {
		return normalizedItem{}, err
	}

	shapeSet := make(map[string]bool)
	stateHashes := make(map[string]bool, len(states))
	totalStateNodes := 0
	for _, state := range states {
		object, ok := state.(map[string]any)
		if !ok || !anyJevSyntheticStateTopLevelMatches(workflow, object) {
			return normalizedItem{}, errors.New("AnyJev synthetic state has an unsupported workflow shape")
		}
		if err := validateAnyJevSyntheticValue(object, 0, &totalStateNodes); err != nil {
			return normalizedItem{}, err
		}
		shape, err := anyJevSyntheticValueShape(object)
		if err != nil {
			return normalizedItem{}, err
		}
		shapeSet[shape] = true
		if len(shapeSet) > maxSyntheticShapeVariants {
			return normalizedItem{}, errors.New("AnyJev synthetic dataset has too many distinct state schemas")
		}
		encoded, err := json.Marshal(object)
		if err != nil {
			return normalizedItem{}, errors.New("AnyJev synthetic state cannot be canonically encoded")
		}
		stateHash := digest(encoded)
		if stateHashes[stateHash] {
			return normalizedItem{}, errors.New("AnyJev synthetic dataset contains duplicate states")
		}
		stateHashes[stateHash] = true
	}
	stateBytes, err := json.Marshal(states)
	if err != nil {
		return normalizedItem{}, errors.New("AnyJev synthetic states cannot be canonically encoded")
	}
	shapes := make([]string, 0, len(shapeSet))
	for shape := range shapeSet {
		shapes = append(shapes, shape)
	}
	sort.Strings(shapes)
	shapeBytes, err := json.Marshal(shapes)
	if err != nil {
		return normalizedItem{}, errors.New("AnyJev synthetic state schemas cannot be canonically encoded")
	}
	payload, err := json.Marshal(anyJevSyntheticStateSummary{
		SchemaVersion: anyJevSyntheticStateSummarySchema, WorkflowSHA256: anyJevStringSHA256(workflow),
		GeneratorSHA256: anyJevStringSHA256(generator), RequestedStates: requested, GenerationAttempts: attempts,
		RejectedGenerations: rejected, StoredStates: len(states), StateCollectionSHA256: digest(stateBytes),
		StateSchemaSHA256: digest(shapeBytes), StateSchemaVariants: len(shapeSet), Settings: settings,
		OmittedFields:       []string{"state_bodies", "workflow_label", "generator_label", "generation_date", "environment", "rejection_reason_labels"},
		HumanReviewRequired: true, ExecutionVerified: false, CutoverEvidenceEligible: false,
	})
	if err != nil {
		return normalizedItem{}, fmt.Errorf("encode AnyJev synthetic-state summary: %w", err)
	}
	return normalizedItem{Type: "decision_synthetic_dataset_summary", Payload: payload}, nil
}

func normalizeAnyJevSyntheticSettings(value any) (anyJevSyntheticSettings, error) {
	settings, ok := value.(map[string]any)
	if !ok || len(settings) != 5 {
		return anyJevSyntheticSettings{}, errors.New("AnyJev synthetic-state settings are invalid")
	}
	for _, key := range []string{"temperature", "top_p", "seed", "max_new_tokens", "exemplars_per_prompt"} {
		if _, exists := settings[key]; !exists {
			return anyJevSyntheticSettings{}, errors.New("AnyJev synthetic-state settings are incomplete")
		}
	}
	temperature, temperatureOK := boundedAnyJevNumber(settings["temperature"], 0, 5)
	topP, topPOK := boundedAnyJevNumber(settings["top_p"], 0, 1)
	seed, seedOK := boundedAnyJevInteger(settings["seed"], 0, math.MaxUint32)
	maxNewTokens, tokensOK := boundedAnyJevInteger(settings["max_new_tokens"], 1, 8192)
	exemplars, exemplarsOK := boundedAnyJevInteger(settings["exemplars_per_prompt"], 3, 10)
	if !temperatureOK || !topPOK || topP == 0 || !seedOK || !tokensOK || !exemplarsOK {
		return anyJevSyntheticSettings{}, errors.New("AnyJev synthetic-state settings are out of range")
	}
	return anyJevSyntheticSettings{Temperature: temperature, TopP: topP, Seed: int64(seed),
		MaxNewTokens: maxNewTokens, ExemplarsPerPrompt: exemplars}, nil
}

func validateAnyJevSyntheticRejectReasons(value any, rejected int) error {
	if value == nil {
		return nil
	}
	reasons, ok := value.(map[string]any)
	if !ok || len(reasons) > 16 {
		return errors.New("AnyJev synthetic-state rejection metadata is invalid")
	}
	allowed := map[string]bool{"truncated": true, "no_json": true, "invalid_json": true, "not_object": true, "keys": true, "duplicate": true}
	total := 0
	for reason, value := range reasons {
		count, valid := boundedAnyJevInteger(value, 0, maxSyntheticAttempts)
		if !allowed[reason] || !valid || count > maxSyntheticAttempts-total {
			return errors.New("AnyJev synthetic-state rejection metadata is unsupported")
		}
		total += count
	}
	if total != rejected {
		return errors.New("AnyJev synthetic-state rejection counts do not reconcile")
	}
	return nil
}

func knownAnyJevSyntheticWorkflow(workflow string) bool {
	switch workflow {
	case "agent_trace_observability", "customer_service", "invoice_processing", "security_incidents":
		return true
	default:
		return false
	}
}

func safeAnyJevSyntheticLabel(value string) bool {
	return len(value) > 0 && len(value) <= 512 && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\\")
}

func anyJevSyntheticStateTopLevelMatches(workflow string, state map[string]any) bool {
	want := map[string][][]string{
		"agent_trace_observability": {{"agent", "constraints", "task", "trace_summary"}},
		"customer_service":          {{"account", "orders", "thread"}, {"account", "thread"}},
		"invoice_processing":        {{"delivery", "invoice", "payment", "purchase_order", "vendor_history"}},
		"security_incidents":        {{"alert", "context", "history", "principal"}},
	}[workflow]
	for _, variant := range want {
		if len(state) != len(variant) {
			continue
		}
		matches := true
		for _, key := range variant {
			if _, exists := state[key]; !exists {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func validateAnyJevSyntheticValue(value any, depth int, nodes *int) error {
	*nodes++
	if depth > maxSyntheticStateDepth || *nodes > maxSyntheticStateNodes {
		return errors.New("AnyJev synthetic state exceeds structural bounds")
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > 128 {
			return errors.New("AnyJev synthetic state object exceeds field bounds")
		}
		for key, child := range typed {
			if key == "" || len(key) > 128 || !utf8.ValidString(key) {
				return errors.New("AnyJev synthetic state has an invalid field name")
			}
			if err := validateAnyJevSyntheticValue(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > 10000 {
			return errors.New("AnyJev synthetic state list exceeds item bounds")
		}
		for _, child := range typed {
			if err := validateAnyJevSyntheticValue(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case string:
		if len(typed) > maxSyntheticStringBytes || !utf8.ValidString(typed) {
			return errors.New("AnyJev synthetic state string exceeds bounds")
		}
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return errors.New("AnyJev synthetic state number is not finite")
		}
	case bool, nil:
	default:
		return errors.New("AnyJev synthetic state has an unsupported JSON value")
	}
	return nil
}

func anyJevSyntheticValueShape(value any) (string, error) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			shape, err := anyJevSyntheticValueShape(typed[key])
			if err != nil {
				return "", err
			}
			parts = append(parts, key+":"+shape)
		}
		encoded, err := json.Marshal(parts)
		return "object:" + string(encoded), err
	case []any:
		shapes := make(map[string]bool)
		for _, child := range typed {
			shape, err := anyJevSyntheticValueShape(child)
			if err != nil {
				return "", err
			}
			shapes[shape] = true
		}
		parts := make([]string, 0, len(shapes))
		for shape := range shapes {
			parts = append(parts, shape)
		}
		sort.Strings(parts)
		encoded, err := json.Marshal(parts)
		return "array:" + string(encoded), err
	case string:
		return "string", nil
	case float64:
		return "number", nil
	case bool:
		return "boolean", nil
	case nil:
		return "null", nil
	default:
		return "", errors.New("AnyJev synthetic state shape is unsupported")
	}
}
