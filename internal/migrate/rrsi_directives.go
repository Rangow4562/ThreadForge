package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/strictjson"
)

const (
	rrsiDirectivesSummarySchema = "threadforge.rrsi-round-directives.v1"
	rrsiDirectivesRecordType    = "rrsi_round_directives"
	maxRRSIDirectivesIndex      = 1000000
	maxRRSIDirectivesItems      = 10000
	maxRRSIDirectivesTextBytes  = 8192
)

type rrsiDirectivesSource struct {
	domain string
	round  string
	index  int
}

type rrsiDirectivesSummary struct {
	SchemaVersion           string         `json:"schema_version"`
	Domain                  string         `json:"domain"`
	RoundIndex              int            `json:"round_index"`
	RoundSHA256             string         `json:"round_sha256"`
	ArtifactPathSHA256      string         `json:"artifact_path_sha256"`
	EditBudget              int            `json:"edit_budget"`
	StallFlag               int            `json:"stall_flag"`
	ExplorationSlots        int            `json:"exploration_slots"`
	NoiseBand               float64        `json:"noise_band"`
	IncumbentScore          float64        `json:"incumbent_score"`
	TriedComponents         map[string]int `json:"tried_components"`
	UntriedComponents       map[string]int `json:"untried_components"`
	PrunedComponents        map[string]int `json:"pruned_components"`
	AcceptedEditsInPruneSet int            `json:"accepted_edits_in_prune_set"`
	MeasuredPruneComponents int            `json:"measured_prune_components"`
	SourceSHA256            string         `json:"source_sha256"`
	SourceBytes             int64          `json:"source_bytes"`
	HumanReviewRequired     bool           `json:"human_review_required"`
	ExecutionAuthorized     bool           `json:"execution_authorized"`
	CutoverEvidenceEligible bool           `json:"cutover_evidence_eligible"`
}

func rrsiDirectivesSourceForPath(relative string) (rrsiDirectivesSource, bool) {
	if !safeRelative(relative) {
		return rrsiDirectivesSource{}, false
	}
	domain, ok := rrsiDomain(relative)
	if !ok {
		return rrsiDirectivesSource{}, false
	}
	tail, ok := rrsiRunTailOriginal(relative)
	if !ok || len(tail) != 2 || !strings.EqualFold(tail[1], "directives.json") {
		return rrsiDirectivesSource{}, false
	}
	round := strings.ToLower(tail[0])
	if !isRRSIRoundName(round) || len(round) > 2 && round[1] == '0' {
		return rrsiDirectivesSource{}, false
	}
	index, err := strconv.Atoi(round[1:])
	if err != nil || index < 0 || index > maxRRSIDirectivesIndex {
		return rrsiDirectivesSource{}, false
	}
	return rrsiDirectivesSource{domain: domain, round: round, index: index}, true
}

func isRRSIDirectivesPath(relative string) bool {
	_, ok := rrsiDirectivesSourceForPath(relative)
	return ok
}

func rrsiDirectivesIdentityHash(kind, value string) string {
	return digest([]byte("rrsi/directives/" + kind + "/v1\x00" + value))
}

func rrsiDirectivesAlias(source rrsiDirectivesSource, relative string) string {
	return filepath.ToSlash(filepath.Join("rrsi", "round-directives", source.domain,
		rrsiDirectivesIdentityHash("round", source.round),
		rrsiDirectivesIdentityHash("path", filepath.ToSlash(relative))))
}

func normalizeRRSIDirectivesFile(relative string, data []byte) (normalizedItem, string, error) {
	source, ok := rrsiDirectivesSourceForPath(relative)
	if !ok {
		return normalizedItem{}, "", errors.New("RRSI directives path is unsupported")
	}
	if !utf8.Valid(data) {
		return normalizedItem{}, "", errors.New("RRSI directives are not valid UTF-8")
	}
	var fields map[string]json.RawMessage
	if err := strictjson.Decode(data, &fields); err != nil || fields == nil || len(fields) != 8 {
		return normalizedItem{}, "", errors.New("RRSI directives JSON structure is invalid")
	}
	for _, name := range []string{"t", "b_t", "sigma_t", "tried", "explore", "prune_set", "delta", "S_star"} {
		if _, exists := fields[name]; !exists {
			return normalizedItem{}, "", errors.New("RRSI directives do not match the pinned producer contract")
		}
	}
	round, roundOK := rrsiDirectivesInteger(fields["t"], 0, maxRRSIDirectivesIndex)
	budget, budgetOK := rrsiDirectivesInteger(fields["b_t"], 0, maxRRSIPrepEdits)
	stall, stallOK := rrsiDirectivesInteger(fields["sigma_t"], 0, 1)
	tried, triedOK := rrsiDirectivesComponentSet(fields["tried"])
	noiseBand, noiseOK := rrsiDirectivesNumber(fields["delta"], 0, 1)
	incumbentScore, scoreOK := rrsiDirectivesNumber(fields["S_star"], 0, 1)
	if !roundOK || round != source.index || !budgetOK || !stallOK || !triedOK || !noiseOK || !scoreOK {
		return normalizedItem{}, "", errors.New("RRSI directives round or scalar values are invalid")
	}
	explorationSlots, untried, exploreOK := readRRSIDirectivesExplore(fields["explore"], stall)
	if !exploreOK || !rrsiDirectivesSetsPartition(tried, untried) {
		return normalizedItem{}, "", errors.New("RRSI directives exploration state is invalid")
	}
	pruned, acceptedCount, measuredCount, pruneOK := readRRSIDirectivesPruneSet(fields["prune_set"], tried, round)
	if !pruneOK {
		return normalizedItem{}, "", errors.New("RRSI directives prune summary is invalid")
	}
	pathHash := rrsiDirectivesIdentityHash("path", filepath.ToSlash(relative))
	payload, err := json.Marshal(rrsiDirectivesSummary{
		SchemaVersion:           rrsiDirectivesSummarySchema,
		Domain:                  source.domain,
		RoundIndex:              round,
		RoundSHA256:             rrsiDirectivesIdentityHash("round", source.round),
		ArtifactPathSHA256:      pathHash,
		EditBudget:              budget,
		StallFlag:               stall,
		ExplorationSlots:        explorationSlots,
		NoiseBand:               noiseBand,
		IncumbentScore:          incumbentScore,
		TriedComponents:         rrsiDirectivesComponentCounts(tried),
		UntriedComponents:       rrsiDirectivesComponentCounts(untried),
		PrunedComponents:        rrsiDirectivesComponentCounts(pruned),
		AcceptedEditsInPruneSet: acceptedCount,
		MeasuredPruneComponents: measuredCount,
		SourceSHA256:            digest(data),
		SourceBytes:             int64(len(data)),
		HumanReviewRequired:     true,
		ExecutionAuthorized:     false,
		CutoverEvidenceEligible: false,
	})
	if err != nil {
		return normalizedItem{}, "", err
	}
	return normalizedItem{Type: rrsiDirectivesRecordType, Payload: payload}, rrsiDirectivesAlias(source, relative), nil
}

func rrsiDirectivesInteger(raw json.RawMessage, min, max int) (int, bool) {
	if !rrsiJSONIntegerValue(raw, int64(min), int64(max)) {
		return 0, false
	}
	var value int
	if json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	return value, true
}

func rrsiDirectivesNumber(raw json.RawMessage, min, max float64) (float64, bool) {
	if !rrsiJSONNumberValue(raw, min, max) {
		return 0, false
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func rrsiDirectivesComponentSet(raw json.RawMessage) (map[string]bool, bool) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '[' {
		return nil, false
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) > len(rrsiPrepComponents) {
		return nil, false
	}
	set := make(map[string]bool, len(values))
	for _, rawValue := range values {
		var component string
		if !rrsiJSONStringValue(rawValue) || json.Unmarshal(rawValue, &component) != nil ||
			!rrsiPrepComponentSupported(component) || set[component] {
			return nil, false
		}
		set[component] = true
	}
	return set, true
}

func readRRSIDirectivesExplore(raw json.RawMessage, stall int) (int, map[string]bool, bool) {
	var fields map[string]json.RawMessage
	if strictjson.Decode(raw, &fields) != nil || fields == nil || len(fields) != 4 {
		return 0, nil, false
	}
	for _, name := range []string{"sigma", "untried", "m_draft", "text"} {
		if _, exists := fields[name]; !exists {
			return 0, nil, false
		}
	}
	sigma, sigmaOK := rrsiDirectivesInteger(fields["sigma"], 0, 1)
	slots, slotsOK := rrsiDirectivesInteger(fields["m_draft"], 0, 8)
	untried, untriedOK := rrsiDirectivesComponentSet(fields["untried"])
	var text string
	textOK := rrsiJSONStringValue(fields["text"]) && json.Unmarshal(fields["text"], &text) == nil &&
		len(text) <= maxRRSIDirectivesTextBytes && !strings.ContainsRune(text, '\x00')
	return slots, untried, sigmaOK && sigma == stall && slotsOK && untriedOK && textOK
}

func rrsiDirectivesSetsPartition(tried, untried map[string]bool) bool {
	count := 0
	for _, component := range rrsiPrepComponents {
		if tried[component] == untried[component] {
			return false
		}
		count++
	}
	return count == len(rrsiPrepComponents) && len(tried) <= len(rrsiPrepComponents) && len(untried) <= len(rrsiPrepComponents)
}

func readRRSIDirectivesPruneSet(raw json.RawMessage, tried map[string]bool, round int) (map[string]bool, int, int, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, 0, 0, false
	}
	var entries []json.RawMessage
	if json.Unmarshal(trimmed, &entries) != nil || len(entries) > len(rrsiPrepComponents) {
		return nil, 0, 0, false
	}
	pruned := make(map[string]bool, len(entries))
	acceptedCount, measuredCount := 0, 0
	for _, entry := range entries {
		var fields map[string]json.RawMessage
		if strictjson.Decode(entry, &fields) != nil || fields == nil || len(fields) != 3 {
			return nil, 0, 0, false
		}
		for _, name := range []string{"component", "recent_best_gain", "accepted_edits_in_incumbent"} {
			if _, exists := fields[name]; !exists {
				return nil, 0, 0, false
			}
		}
		var component string
		if !rrsiJSONStringValue(fields["component"]) || json.Unmarshal(fields["component"], &component) != nil ||
			!rrsiPrepComponentSupported(component) || !tried[component] || pruned[component] {
			return nil, 0, 0, false
		}
		gain := bytes.TrimSpace(fields["recent_best_gain"])
		if !bytes.Equal(gain, []byte("null")) {
			value, valid := rrsiDirectivesNumber(gain, -1, 0)
			if !valid || value > 0 {
				return nil, 0, 0, false
			}
			measuredCount++
		}
		var accepted []json.RawMessage
		if len(bytes.TrimSpace(fields["accepted_edits_in_incumbent"])) == 0 ||
			bytes.TrimSpace(fields["accepted_edits_in_incumbent"])[0] != '[' ||
			json.Unmarshal(fields["accepted_edits_in_incumbent"], &accepted) != nil ||
			len(accepted) > maxRRSIDirectivesItems-acceptedCount {
			return nil, 0, 0, false
		}
		for _, edit := range accepted {
			if !validRRSIDirectiveAcceptedEdit(edit, round) {
				return nil, 0, 0, false
			}
		}
		acceptedCount += len(accepted)
		pruned[component] = true
	}
	return pruned, acceptedCount, measuredCount, true
}

func validRRSIDirectiveAcceptedEdit(raw json.RawMessage, round int) bool {
	var fields map[string]json.RawMessage
	if strictjson.Decode(raw, &fields) != nil || fields == nil || len(fields) != 4 {
		return false
	}
	for _, name := range []string{"t", "edit_id", "hypothesis", "delta_S"} {
		if _, exists := fields[name]; !exists {
			return false
		}
	}
	if _, ok := rrsiDirectivesInteger(fields["t"], 0, round); !ok {
		return false
	}
	for _, name := range []string{"edit_id", "hypothesis"} {
		value := bytes.TrimSpace(fields[name])
		if bytes.Equal(value, []byte("null")) {
			continue
		}
		var text string
		if !rrsiJSONStringValue(value) || json.Unmarshal(value, &text) != nil ||
			len(text) > maxRRSIDirectivesTextBytes || strings.ContainsRune(text, '\x00') {
			return false
		}
	}
	value := bytes.TrimSpace(fields["delta_S"])
	return bytes.Equal(value, []byte("null")) || func() bool {
		_, ok := rrsiDirectivesNumber(value, -1, 1)
		return ok
	}()
}

func rrsiDirectivesComponentCounts(set map[string]bool) map[string]int {
	counts := make(map[string]int, len(rrsiPrepComponents))
	for _, component := range rrsiPrepComponents {
		if set[component] {
			counts[component] = 1
		} else {
			counts[component] = 0
		}
	}
	return counts
}

func scanRRSIDirectivesFile(relative string, data []byte) scannedFile {
	file := scannedFile{entry: FileEntry{
		Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data)), Status: "ready",
	}}
	file.entry.RecordCount = 1
	item, alias, err := normalizeRRSIDirectivesFile(relative, data)
	if err != nil {
		file.entry.RejectedCount = 1
		file.entry.Issue = safeIssue(err)
	} else if privacy.ContainsCredential(item.Payload) {
		file.entry.ExcludedCount = 1
	} else {
		file.records = append(file.records, Record{
			Key: alias + "#000001", Type: item.Type, SourcePath: alias,
			ContentSHA256: digest(item.Payload), Scope: "local_pending_review", Payload: item.Payload,
		})
	}
	finalizeScannedFile(&file)
	return file
}

func validateRRSIDirectivesSummary(record Record, source FileEntry) error {
	var payload rrsiDirectivesSummary
	var fields map[string]json.RawMessage
	if strictjson.Decode(record.Payload, &payload) != nil || strictjson.Decode(record.Payload, &fields) != nil ||
		fields == nil || len(fields) != 20 ||
		!rrsiJSONFieldTypesMatch(fields,
			[]string{"schema_version", "domain", "round_sha256", "artifact_path_sha256", "source_sha256"},
			[]string{"human_review_required", "execution_authorized", "cutover_evidence_eligible"},
			map[string][2]int64{
				"round_index": {0, maxRRSIDirectivesIndex}, "edit_budget": {0, maxRRSIPrepEdits},
				"stall_flag": {0, 1}, "exploration_slots": {0, 8},
				"accepted_edits_in_prune_set": {0, maxRRSIDirectivesItems},
				"measured_prune_components":   {0, int64(len(rrsiPrepComponents))},
				"source_bytes":                {1, maxRRSITrialFileBytes},
			}) || !rrsiJSONNumberValue(fields["noise_band"], 0, 1) ||
		!rrsiJSONNumberValue(fields["incumbent_score"], 0, 1) {
		return errors.New("RRSI directives summary payload is invalid")
	}
	sourceInfo, ok := rrsiDirectivesSourceForPath(source.Path)
	if !ok || record.Type != rrsiDirectivesRecordType || !validDigest(source.SourceSHA256) ||
		source.SourceBytes < 1 || source.SourceBytes > maxRRSITrialFileBytes {
		return errors.New("RRSI directives summary source is invalid")
	}
	tried, triedOK := readRRSIDirectiveSummaryCounts(fields["tried_components"])
	untried, untriedOK := readRRSIDirectiveSummaryCounts(fields["untried_components"])
	pruned, prunedOK := readRRSIDirectiveSummaryCounts(fields["pruned_components"])
	if !triedOK || !untriedOK || !prunedOK {
		return errors.New("RRSI directives component summaries are invalid")
	}
	triedSet, untriedSet, prunedSet := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	for _, component := range rrsiPrepComponents {
		triedSet[component] = tried[component] == 1
		untriedSet[component] = untried[component] == 1
		prunedSet[component] = pruned[component] == 1
		if prunedSet[component] && !triedSet[component] {
			return errors.New("RRSI directives prune components are not a subset of tried components")
		}
	}
	_, noiseOK := rrsiDirectivesNumber(fields["noise_band"], 0, 1)
	_, scoreOK := rrsiDirectivesNumber(fields["incumbent_score"], 0, 1)
	aliasOK := record.SourcePath == rrsiDirectivesAlias(sourceInfo, source.Path)
	identityOK := payload.SchemaVersion == rrsiDirectivesSummarySchema && payload.Domain == sourceInfo.domain &&
		payload.RoundIndex == sourceInfo.index && payload.RoundSHA256 == rrsiDirectivesIdentityHash("round", sourceInfo.round) &&
		payload.ArtifactPathSHA256 == rrsiDirectivesIdentityHash("path", filepath.ToSlash(source.Path))
	provenanceOK := payload.SourceSHA256 == source.SourceSHA256 && payload.SourceBytes == source.SourceBytes
	partitionOK := rrsiDirectivesSetsPartition(triedSet, untriedSet)
	countOK := payload.MeasuredPruneComponents <= sumRRSIDirectiveCounts(pruned)
	boundaryOK := payload.HumanReviewRequired && !payload.ExecutionAuthorized && !payload.CutoverEvidenceEligible
	if !aliasOK || !identityOK || !provenanceOK ||
		!partitionOK || !noiseOK || !scoreOK || !countOK || !boundaryOK {
		return errors.New("RRSI directives provenance or review boundary is invalid")
	}
	return nil
}

func readRRSIDirectiveSummaryCounts(raw json.RawMessage) (map[string]int, bool) {
	var fields map[string]json.RawMessage
	if strictjson.Decode(raw, &fields) != nil || fields == nil || len(fields) != len(rrsiPrepComponents) {
		return nil, false
	}
	counts := make(map[string]int, len(rrsiPrepComponents))
	for _, component := range rrsiPrepComponents {
		value, exists := fields[component]
		if !exists || !rrsiJSONIntegerValue(value, 0, 1) {
			return nil, false
		}
		var count int
		if json.Unmarshal(value, &count) != nil {
			return nil, false
		}
		counts[component] = count
	}
	return counts, true
}

func sumRRSIDirectiveCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}
