package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/strictjson"
)

const (
	rrsiAnalysisSummarySchema = "threadforge.rrsi-analysis-summary.v1"
	rrsiAnalysisRecordType    = "rrsi_analysis_summary"
	maxRRSIAnalysisRows       = 512
	maxRRSIAnalysisTaskRefs   = 10000
	maxRRSIAnalysisTextBytes  = 8192
)

var rrsiAnalysisLabelPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

type rrsiAnalysisSource struct {
	domain       string
	artifactKind string
	round        string
	index        int
}

type rrsiAnalysisSummary struct {
	SchemaVersion              string `json:"schema_version"`
	Domain                     string `json:"domain"`
	ArtifactKind               string `json:"artifact_kind"`
	RoundIndex                 *int   `json:"round_index"`
	RoundSHA256                string `json:"round_sha256"`
	ArtifactPathSHA256         string `json:"artifact_path_sha256"`
	FailureModeCount           int    `json:"failure_mode_count"`
	ReportedFailureTaskCount   int    `json:"reported_failure_task_count"`
	FailureEvidenceCount       int    `json:"failure_evidence_count"`
	CapabilityGapCount         int    `json:"capability_gap_count"`
	ReportedGapTaskCount       int    `json:"reported_gap_task_count"`
	GapEvidenceCount           int    `json:"gap_evidence_count"`
	SuccessHabitCount          int    `json:"success_habit_count"`
	ReportedSuccessTaskCount   int    `json:"reported_success_task_count"`
	AffectedTaskReferenceCount int    `json:"affected_task_reference_count"`
	DigestCount                int    `json:"digest_count"`
	DigestCountPresent         bool   `json:"digest_count_present"`
	AnalysisIncomplete         bool   `json:"analysis_incomplete"`
	SourceSHA256               string `json:"source_sha256"`
	SourceBytes                int64  `json:"source_bytes"`
	HumanReviewRequired        bool   `json:"human_review_required"`
	ExecutionAuthorized        bool   `json:"execution_authorized"`
	CutoverEvidenceEligible    bool   `json:"cutover_evidence_eligible"`
}

type rrsiAnalysisRowStats struct {
	rows       int
	taskCounts int
	evidence   int
	taskRefs   int
}

func rrsiAnalysisSourceForPath(relative string) (rrsiAnalysisSource, bool) {
	if !safeRelative(relative) {
		return rrsiAnalysisSource{}, false
	}
	domain, ok := rrsiDomain(relative)
	if !ok {
		return rrsiAnalysisSource{}, false
	}
	tail, ok := rrsiRunTailOriginal(relative)
	if !ok {
		return rrsiAnalysisSource{}, false
	}
	if len(tail) == 1 && strings.EqualFold(tail[0], "global_analysis.json") {
		return rrsiAnalysisSource{domain: domain, artifactKind: "global_latest", index: -1}, true
	}
	if len(tail) != 2 || !strings.EqualFold(tail[1], "analysis_report.json") {
		return rrsiAnalysisSource{}, false
	}
	round := strings.ToLower(tail[0])
	if !isRRSIRoundName(round) || len(round) > 2 && round[1] == '0' {
		return rrsiAnalysisSource{}, false
	}
	index, err := strconv.Atoi(round[1:])
	if err != nil || index > maxRRSIDirectivesIndex {
		return rrsiAnalysisSource{}, false
	}
	return rrsiAnalysisSource{domain: domain, artifactKind: "round_report", round: round, index: index}, true
}

func isRRSIAnalysisPath(relative string) bool {
	_, ok := rrsiAnalysisSourceForPath(relative)
	return ok
}

func rrsiAnalysisIdentityHash(kind, value string) string {
	return digest([]byte("rrsi/analysis/" + kind + "/v1\x00" + value))
}

func rrsiAnalysisAlias(source rrsiAnalysisSource, relative string) string {
	parts := []string{"rrsi", "analysis", source.domain, source.artifactKind}
	if source.artifactKind == "round_report" {
		parts = append(parts, rrsiAnalysisIdentityHash("round", source.round))
	}
	parts = append(parts, rrsiAnalysisIdentityHash("path", filepath.ToSlash(relative)))
	return filepath.ToSlash(filepath.Join(parts...))
}

func normalizeRRSIAnalysisFile(relative string, data []byte) (normalizedItem, string, error) {
	source, ok := rrsiAnalysisSourceForPath(relative)
	if !ok {
		return normalizedItem{}, "", errors.New("RRSI analysis path is unsupported")
	}
	if !utf8.Valid(data) {
		return normalizedItem{}, "", errors.New("RRSI analysis is not valid UTF-8")
	}
	var fields map[string]json.RawMessage
	if strictjson.Decode(data, &fields) != nil || fields == nil {
		return normalizedItem{}, "", errors.New("RRSI analysis JSON structure is invalid")
	}
	var failureRows, gapRows, habitRows rrsiAnalysisRowStats
	var digestCount int
	digestPresent, incomplete := false, false
	if source.artifactKind == "round_report" {
		if !rrsiAnalysisHasKeys(fields, []string{"failure_modes", "capability_gaps", "success_habits", "n_digests"}, []string{"error"}) {
			return normalizedItem{}, "", errors.New("RRSI analysis report does not match the pinned producer contract")
		}
		failureRows, ok = readRRSIAnalysisRows(fields["failure_modes"], "failure")
		if !ok {
			return normalizedItem{}, "", errors.New("RRSI failure-mode report is invalid")
		}
		gapRows, ok = readRRSIAnalysisRows(fields["capability_gaps"], "gap")
		if !ok {
			return normalizedItem{}, "", errors.New("RRSI capability-gap report is invalid")
		}
		habitRows, ok = readRRSIAnalysisRows(fields["success_habits"], "habit")
		if !ok {
			return normalizedItem{}, "", errors.New("RRSI success-habit report is invalid")
		}
		digestCount, ok = rrsiDirectivesInteger(fields["n_digests"], 0, maxRRSIAnalysisTaskRefs)
		if !ok {
			return normalizedItem{}, "", errors.New("RRSI analysis digest count is invalid")
		}
		digestPresent = true
		if rawError, exists := fields["error"]; exists {
			var text string
			if !rrsiJSONStringValue(rawError) || json.Unmarshal(rawError, &text) != nil || text != "analyst hit max turns" ||
				failureRows.rows != 0 || gapRows.rows != 0 || habitRows.rows != 0 {
				return normalizedItem{}, "", errors.New("RRSI incomplete analysis state is invalid")
			}
			incomplete = true
		}
	} else {
		if len(fields) != 2 {
			return normalizedItem{}, "", errors.New("RRSI global analysis does not match the pinned producer contract")
		}
		for _, name := range []string{"failure_modes", "success_habits"} {
			if _, exists := fields[name]; !exists {
				return normalizedItem{}, "", errors.New("RRSI global analysis is missing a required field")
			}
		}
		failureRows, ok = readRRSIAnalysisRows(fields["failure_modes"], "failure")
		if !ok {
			return normalizedItem{}, "", errors.New("RRSI global failure-mode state is invalid")
		}
		habitRows, ok = readRRSIAnalysisRows(fields["success_habits"], "habit")
		if !ok {
			return normalizedItem{}, "", errors.New("RRSI global success-habit state is invalid")
		}
	}
	if failureRows.taskRefs+gapRows.taskRefs+habitRows.taskRefs > maxRRSIAnalysisTaskRefs {
		return normalizedItem{}, "", errors.New("RRSI analysis task-reference count exceeds the source bound")
	}
	pathHash := rrsiAnalysisIdentityHash("path", filepath.ToSlash(relative))
	roundHash := ""
	var roundIndex *int
	if source.artifactKind == "round_report" {
		roundHash = rrsiAnalysisIdentityHash("round", source.round)
		index := source.index
		roundIndex = &index
	}
	payload, err := json.Marshal(rrsiAnalysisSummary{
		SchemaVersion:              rrsiAnalysisSummarySchema,
		Domain:                     source.domain,
		ArtifactKind:               source.artifactKind,
		RoundIndex:                 roundIndex,
		RoundSHA256:                roundHash,
		ArtifactPathSHA256:         pathHash,
		FailureModeCount:           failureRows.rows,
		ReportedFailureTaskCount:   failureRows.taskCounts,
		FailureEvidenceCount:       failureRows.evidence,
		CapabilityGapCount:         gapRows.rows,
		ReportedGapTaskCount:       gapRows.taskCounts,
		GapEvidenceCount:           gapRows.evidence,
		SuccessHabitCount:          habitRows.rows,
		ReportedSuccessTaskCount:   habitRows.taskCounts,
		AffectedTaskReferenceCount: failureRows.taskRefs + gapRows.taskRefs + habitRows.taskRefs,
		DigestCount:                digestCount,
		DigestCountPresent:         digestPresent,
		AnalysisIncomplete:         incomplete,
		SourceSHA256:               digest(data),
		SourceBytes:                int64(len(data)),
		HumanReviewRequired:        true,
		ExecutionAuthorized:        false,
		CutoverEvidenceEligible:    false,
	})
	if err != nil {
		return normalizedItem{}, "", err
	}
	return normalizedItem{Type: rrsiAnalysisRecordType, Payload: payload}, rrsiAnalysisAlias(source, relative), nil
}

func rrsiAnalysisHasKeys(fields map[string]json.RawMessage, required, optional []string) bool {
	if len(fields) < len(required) || len(fields) > len(required)+len(optional) {
		return false
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		allowed[key] = true
		if _, exists := fields[key]; !exists {
			return false
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range fields {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func readRRSIAnalysisRows(raw json.RawMessage, kind string) (rrsiAnalysisRowStats, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return rrsiAnalysisRowStats{}, false
	}
	var rows []json.RawMessage
	if json.Unmarshal(trimmed, &rows) != nil || len(rows) > maxRRSIAnalysisRows {
		return rrsiAnalysisRowStats{}, false
	}
	stats := rrsiAnalysisRowStats{rows: len(rows)}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if strictjson.Decode(row, &fields) != nil || fields == nil {
			return rrsiAnalysisRowStats{}, false
		}
		labelKey := kind
		if kind == "failure" {
			labelKey = "mode"
		}
		required := []string{labelKey, "n_tasks", "description"}
		var optional []string
		switch kind {
		case "failure":
			required = append(required, "affected_tasks", "needed_instead", "representative_evidence")
			optional = nil
		case "gap":
			required = append(required, "affected_tasks", "representative_evidence")
			optional = nil
		case "habit":
			required = append(required, "risk_if_broken")
			optional = nil
		default:
			return rrsiAnalysisRowStats{}, false
		}
		if !rrsiAnalysisHasKeys(fields, required, optional) {
			return rrsiAnalysisRowStats{}, false
		}
		var label string
		if !rrsiJSONStringValue(fields[labelKey]) || json.Unmarshal(fields[labelKey], &label) != nil ||
			!rrsiAnalysisLabelPattern.MatchString(label) {
			return rrsiAnalysisRowStats{}, false
		}
		tasks, ok := rrsiDirectivesInteger(fields["n_tasks"], 0, maxRRSIAnalysisTaskRefs)
		if !ok {
			return rrsiAnalysisRowStats{}, false
		}
		refs := 0
		if kind == "failure" || kind == "gap" {
			var refsOK bool
			refs, refsOK = readRRSIAnalysisTaskRefs(fields["affected_tasks"])
			if !refsOK || refs > maxRRSIAnalysisTaskRefs-stats.taskRefs {
				return rrsiAnalysisRowStats{}, false
			}
		}
		for _, name := range []string{"description", "needed_instead", "risk_if_broken"} {
			if rawText, exists := fields[name]; exists && !rrsiAnalysisTextValid(rawText, maxRRSIAnalysisTextBytes) {
				return rrsiAnalysisRowStats{}, false
			}
		}
		if kind == "failure" || kind == "gap" {
			evidenceCount, ok := readRRSIAnalysisEvidence(fields["representative_evidence"])
			if !ok || evidenceCount > maxRRSIAnalysisTaskRefs-stats.evidence {
				return rrsiAnalysisRowStats{}, false
			}
			stats.evidence += evidenceCount
		}
		stats.taskCounts += tasks
		stats.taskRefs += refs
	}
	return stats, true
}

func readRRSIAnalysisTaskRefs(raw json.RawMessage) (int, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return 0, false
	}
	var values []json.RawMessage
	if json.Unmarshal(trimmed, &values) != nil || len(values) > maxRRSIAnalysisTaskRefs {
		return 0, false
	}
	for _, value := range values {
		if !rrsiAnalysisTextValid(value, 512) {
			return 0, false
		}
	}
	return len(values), true
}

func readRRSIAnalysisEvidence(raw json.RawMessage) (int, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return 0, false
	}
	var rows []json.RawMessage
	if json.Unmarshal(trimmed, &rows) != nil || len(rows) > maxRRSIAnalysisRows {
		return 0, false
	}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if strictjson.Decode(row, &fields) != nil || fields == nil || len(fields) != 3 ||
			!rrsiAnalysisHasKeys(fields, []string{"task_id", "where", "quote"}, nil) ||
			!rrsiAnalysisTextValid(fields["task_id"], 512) || !rrsiAnalysisTextValid(fields["where"], 512) ||
			!rrsiAnalysisTextValid(fields["quote"], maxRRSIAnalysisTextBytes) {
			return 0, false
		}
	}
	return len(rows), true
}

func rrsiAnalysisTextValid(raw json.RawMessage, maxBytes int) bool {
	var value string
	return rrsiJSONStringValue(raw) && json.Unmarshal(raw, &value) == nil && utf8.ValidString(value) &&
		len(value) <= maxBytes && !strings.ContainsRune(value, '\x00')
}

func scanRRSIAnalysisFile(relative string, data []byte) scannedFile {
	file := scannedFile{entry: FileEntry{
		Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data)), Status: "ready",
	}}
	file.entry.RecordCount = 1
	item, alias, err := normalizeRRSIAnalysisFile(relative, data)
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

func validateRRSIAnalysisSummary(record Record, source FileEntry) error {
	var payload rrsiAnalysisSummary
	var fields map[string]json.RawMessage
	if strictjson.Decode(record.Payload, &payload) != nil || strictjson.Decode(record.Payload, &fields) != nil ||
		fields == nil || len(fields) != 23 ||
		!rrsiJSONFieldTypesMatch(fields,
			[]string{"schema_version", "domain", "artifact_kind", "round_sha256", "artifact_path_sha256", "source_sha256"},
			[]string{"digest_count_present", "analysis_incomplete", "human_review_required", "execution_authorized", "cutover_evidence_eligible"},
			map[string][2]int64{
				"failure_mode_count": {0, maxRRSIAnalysisRows}, "reported_failure_task_count": {0, maxRRSIAnalysisRows * maxRRSIAnalysisTaskRefs},
				"failure_evidence_count": {0, maxRRSIAnalysisTaskRefs}, "capability_gap_count": {0, maxRRSIAnalysisRows},
				"reported_gap_task_count": {0, maxRRSIAnalysisRows * maxRRSIAnalysisTaskRefs},
				"gap_evidence_count":      {0, maxRRSIAnalysisTaskRefs}, "success_habit_count": {0, maxRRSIAnalysisRows},
				"reported_success_task_count":   {0, maxRRSIAnalysisRows * maxRRSIAnalysisTaskRefs},
				"affected_task_reference_count": {0, maxRRSIAnalysisTaskRefs},
				"digest_count":                  {0, maxRRSIAnalysisTaskRefs}, "source_bytes": {1, maxRRSITrialFileBytes},
			}) {
		return errors.New("RRSI analysis summary payload is invalid")
	}
	if _, exists := fields["round_index"]; !exists {
		return errors.New("RRSI analysis round index is missing")
	}
	sourceInfo, ok := rrsiAnalysisSourceForPath(source.Path)
	if !ok || record.Type != rrsiAnalysisRecordType || !validDigest(source.SourceSHA256) ||
		source.SourceBytes < 1 || source.SourceBytes > maxRRSITrialFileBytes {
		return errors.New("RRSI analysis summary source is invalid")
	}
	if sourceInfo.artifactKind == "round_report" {
		if payload.RoundIndex == nil || *payload.RoundIndex != sourceInfo.index ||
			payload.RoundSHA256 != rrsiAnalysisIdentityHash("round", sourceInfo.round) || !payload.DigestCountPresent {
			return errors.New("RRSI round-analysis identity is invalid")
		}
	} else if payload.RoundIndex != nil || payload.RoundSHA256 != "" || payload.DigestCountPresent ||
		payload.DigestCount != 0 || payload.AnalysisIncomplete || payload.CapabilityGapCount != 0 ||
		payload.ReportedGapTaskCount != 0 || payload.GapEvidenceCount != 0 {
		return errors.New("RRSI global-analysis shape is invalid")
	}
	artifactKindRaw := fields["artifact_kind"]
	if !rrsiJSONStringValue(artifactKindRaw) || payload.ArtifactKind != sourceInfo.artifactKind ||
		payload.SchemaVersion != rrsiAnalysisSummarySchema || payload.Domain != sourceInfo.domain ||
		payload.ArtifactPathSHA256 != rrsiAnalysisIdentityHash("path", filepath.ToSlash(source.Path)) ||
		payload.SourceSHA256 != source.SourceSHA256 || payload.SourceBytes != source.SourceBytes ||
		payload.FailureModeCount > maxRRSIAnalysisRows || payload.CapabilityGapCount > maxRRSIAnalysisRows ||
		payload.SuccessHabitCount > maxRRSIAnalysisRows || payload.AffectedTaskReferenceCount > maxRRSIAnalysisTaskRefs ||
		payload.FailureEvidenceCount+payload.GapEvidenceCount > maxRRSIAnalysisTaskRefs ||
		!payload.HumanReviewRequired || payload.ExecutionAuthorized || payload.CutoverEvidenceEligible ||
		record.SourcePath != rrsiAnalysisAlias(sourceInfo, source.Path) {
		return errors.New("RRSI analysis summary provenance or review boundary is invalid")
	}
	if payload.RoundIndex == nil && !bytes.Equal(bytes.TrimSpace(fields["round_index"]), []byte("null")) {
		return errors.New("RRSI global analysis round index is invalid")
	}
	return nil
}
