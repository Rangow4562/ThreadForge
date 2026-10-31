package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/strictjson"
)

const (
	rrsiPrepSummarySchema = "threadforge.rrsi-prep-summary.v1"
	rrsiPrepRecordType    = "rrsi_prep_summary"
	maxRRSIPrepEdits      = 80
	maxRRSIPrepTextBytes  = 8192
	maxRRSIPrepPathBytes  = 4096
)

var rrsiPrepComponents = []string{
	"prompt", "control_flow", "config", "output_plumbing", "context_mgmt",
	"client_tool", "skill", "memory", "subagent",
}

type rrsiPrepSource struct {
	domain  string
	round   string
	variant string
}

type rrsiPrepSummary struct {
	SchemaVersion           string         `json:"schema_version"`
	Domain                  string         `json:"domain"`
	GateFailure             string         `json:"gate_failure"`
	RoundSHA256             string         `json:"round_sha256"`
	VariantSHA256           string         `json:"variant_sha256"`
	ArtifactPathSHA256      string         `json:"artifact_path_sha256"`
	CommitSHA256            string         `json:"commit_sha256"`
	CommitPresent           bool           `json:"commit_present"`
	BranchMatchesIdentity   bool           `json:"branch_matches_identity"`
	EditCount               int            `json:"edit_count"`
	EditComponentCounts     map[string]int `json:"edit_component_counts"`
	DiffPathPresent         bool           `json:"diff_path_present"`
	MechanismPresent        bool           `json:"mechanism_present"`
	DiagnosticBytes         int            `json:"diagnostic_bytes"`
	SourceSHA256            string         `json:"source_sha256"`
	SourceBytes             int64          `json:"source_bytes"`
	HumanReviewRequired     bool           `json:"human_review_required"`
	SelectionAccepted       bool           `json:"selection_accepted"`
	ExecutionAuthorized     bool           `json:"execution_authorized"`
	CutoverEvidenceEligible bool           `json:"cutover_evidence_eligible"`
}

func rrsiPrepSourceForPath(relative string) (rrsiPrepSource, bool) {
	if !safeRelative(relative) {
		return rrsiPrepSource{}, false
	}
	domain, ok := rrsiDomain(relative)
	if !ok {
		return rrsiPrepSource{}, false
	}
	tail, ok := rrsiRunTailOriginal(relative)
	round := ""
	if ok && len(tail) > 0 {
		round = strings.ToLower(tail[0])
	}
	if !ok || len(tail) != 3 || !isRRSIRoundName(round) || len(round) > 2 && round[1] == '0' || len(tail[1]) != 1 ||
		strings.ToLower(tail[1])[0] < 'a' || strings.ToLower(tail[1])[0] > 'h' ||
		!strings.EqualFold(tail[2], "prep.json") {
		return rrsiPrepSource{}, false
	}
	return rrsiPrepSource{
		domain: domain, round: round, variant: strings.ToLower(tail[1]),
	}, true
}

func isRRSIPrepPath(relative string) bool {
	_, ok := rrsiPrepSourceForPath(relative)
	return ok
}

func rrsiPrepIdentityHash(kind, value string) string {
	return digest([]byte("rrsi/prep/" + kind + "/v1\x00" + value))
}

func rrsiPrepAlias(source rrsiPrepSource, relative string) string {
	return filepath.ToSlash(filepath.Join("rrsi", "preparations", source.domain,
		rrsiPrepIdentityHash("round", source.round),
		rrsiPrepIdentityHash("variant", source.variant),
		rrsiPrepIdentityHash("path", filepath.ToSlash(relative))))
}

func normalizeRRSIPrepFile(relative string, data []byte) (normalizedItem, string, error) {
	source, ok := rrsiPrepSourceForPath(relative)
	if !ok {
		return normalizedItem{}, "", errors.New("RRSI prep artifact path is unsupported")
	}
	if len(data) == 0 || int64(len(data)) > maxRRSITrialFileBytes || !utf8.Valid(data) {
		return normalizedItem{}, "", errors.New("RRSI prep artifact size or UTF-8 encoding is invalid")
	}
	var fields map[string]json.RawMessage
	if err := strictjson.Decode(data, &fields); err != nil || fields == nil {
		return normalizedItem{}, "", errors.New("RRSI prep artifact JSON object is invalid")
	}
	summary := rrsiPrepSummary{
		SchemaVersion:           rrsiPrepSummarySchema,
		Domain:                  source.domain,
		EditComponentCounts:     emptyRRSIPrepComponentCounts(),
		HumanReviewRequired:     true,
		SelectionAccepted:       false,
		ExecutionAuthorized:     false,
		CutoverEvidenceEligible: false,
	}

	if gateRaw, failed := fields["gate_failure"]; failed {
		if len(fields) != 4 || !rrsiJSONStringValue(gateRaw) || json.Unmarshal(gateRaw, &summary.GateFailure) != nil ||
			!rrsiPrepGateFailureSupported(summary.GateFailure) {
			return normalizedItem{}, "", errors.New("RRSI prep failure state is invalid")
		}
		detail, detailOK := rrsiPrepReadText(fields["detail"], maxRRSIPrepTextBytes)
		if !detailOK || !hasRRSIPrepField(fields, "detail") {
			return normalizedItem{}, "", errors.New("RRSI prep diagnostic field is invalid")
		}
		summary.DiagnosticBytes = len(detail)
		diffPresent, diffOK := rrsiPrepReadNullablePath(fields["diff_path"])
		if !diffOK || !hasRRSIPrepField(fields, "diff_path") {
			return normalizedItem{}, "", errors.New("RRSI prep diff-path field is invalid")
		}
		summary.DiffPathPresent = diffPresent
	} else {
		if len(fields) != 5 {
			return normalizedItem{}, "", errors.New("RRSI committed prep fields are invalid")
		}
		commit, commitOK := rrsiPrepReadText(fields["commit"], 64)
		if !commitOK || !isRRSIPrepCommitID(commit) {
			return normalizedItem{}, "", errors.New("RRSI prep commit identity is invalid")
		}
		branch, branchOK := rrsiPrepReadText(fields["branch"], 256)
		if !branchOK || !strings.EqualFold(branch, rrsiPrepExpectedBranch(source)) {
			return normalizedItem{}, "", errors.New("RRSI prep branch does not match its source identity")
		}
		mechanism, mechanismOK := rrsiPrepReadText(fields["mechanism"], 2048)
		if !mechanismOK || mechanism == "" {
			return normalizedItem{}, "", errors.New("RRSI prep mechanism field is invalid")
		}
		diffPresent, diffOK := rrsiPrepReadRequiredPath(fields["diff_path"])
		if !diffOK || !hasRRSIPrepField(fields, "diff_path") {
			return normalizedItem{}, "", errors.New("RRSI prep diff path is invalid")
		}
		editCount, counts, editsOK := readRRSIPrepEdits(fields["edits"])
		if !editsOK || editCount == 0 {
			return normalizedItem{}, "", errors.New("RRSI committed prep edit list is invalid")
		}
		summary.CommitSHA256 = rrsiPrepIdentityHash("commit", commit)
		summary.CommitPresent = true
		summary.BranchMatchesIdentity = true
		summary.EditCount = editCount
		summary.EditComponentCounts = counts
		summary.DiffPathPresent = diffPresent
		summary.MechanismPresent = true
	}

	if hasRRSIPrepField(fields, "edits") {
		editCount, counts, editsOK := readRRSIPrepEdits(fields["edits"])
		if !editsOK || summary.EditCount != 0 && summary.EditCount != editCount {
			return normalizedItem{}, "", errors.New("RRSI prep edit list is invalid")
		}
		summary.EditCount = editCount
		summary.EditComponentCounts = counts
	} else {
		return normalizedItem{}, "", errors.New("RRSI prep edit list is missing")
	}
	if !rrsiPrepSummaryShapeMatches(summary) {
		return normalizedItem{}, "", errors.New("RRSI prep source shape is incomplete or inconsistent")
	}

	summary.RoundSHA256 = rrsiPrepIdentityHash("round", source.round)
	summary.VariantSHA256 = rrsiPrepIdentityHash("variant", source.variant)
	summary.ArtifactPathSHA256 = rrsiPrepIdentityHash("path", filepath.ToSlash(relative))
	summary.SourceSHA256 = digest(data)
	summary.SourceBytes = int64(len(data))
	payload, err := json.Marshal(summary)
	if err != nil {
		return normalizedItem{}, "", err
	}
	return normalizedItem{Type: rrsiPrepRecordType, Payload: payload}, rrsiPrepAlias(source, relative), nil
}

func rrsiPrepReadText(raw json.RawMessage, maxBytes int) (string, bool) {
	if !rrsiJSONStringValue(raw) {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) || len(value) > maxBytes || strings.ContainsRune(value, '\x00') {
		return "", false
	}
	return value, true
}

func rrsiPrepReadNullablePath(raw json.RawMessage) (bool, bool) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return false, true
	}
	value, ok := rrsiPrepReadText(raw, maxRRSIPrepPathBytes)
	return ok && value != "", ok
}

func rrsiPrepReadRequiredPath(raw json.RawMessage) (bool, bool) {
	present, ok := rrsiPrepReadNullablePath(raw)
	return present, ok && present
}

func rrsiPrepExpectedBranch(source rrsiPrepSource) string {
	return source.domain + "/" + source.round + strings.ToUpper(source.variant)
}

func isRRSIPrepCommitID(value string) bool {
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func rrsiPrepGateFailureSupported(value string) bool {
	switch value {
	case "no_proposal", "critic_reject", "smoke_fail":
		return true
	default:
		return false
	}
}

func emptyRRSIPrepComponentCounts() map[string]int {
	counts := make(map[string]int, len(rrsiPrepComponents))
	for _, component := range rrsiPrepComponents {
		counts[component] = 0
	}
	return counts
}

func readRRSIPrepEdits(raw json.RawMessage) (int, map[string]int, bool) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '[' {
		return 0, nil, false
	}
	var edits []json.RawMessage
	if json.Unmarshal(raw, &edits) != nil || len(edits) > maxRRSIPrepEdits {
		return 0, nil, false
	}
	counts := emptyRRSIPrepComponentCounts()
	for _, editRaw := range edits {
		var fields map[string]json.RawMessage
		if strictjson.Decode(editRaw, &fields) != nil || fields == nil {
			return 0, nil, false
		}
		for key := range fields {
			if !isRRSIPrepEditField(key) {
				return 0, nil, false
			}
		}
		for _, key := range []string{"id", "component", "hypothesis", "targets_mode", "predicted_affected", "retroactive_check"} {
			if !hasRRSIPrepField(fields, key) {
				return 0, nil, false
			}
		}
		id, idOK := rrsiPrepReadText(fields["id"], 128)
		component, componentOK := rrsiPrepReadText(fields["component"], 64)
		_, hypothesisOK := rrsiPrepReadText(fields["hypothesis"], maxRRSIPrepTextBytes)
		_, targetOK := rrsiPrepReadText(fields["targets_mode"], maxRRSIPrepTextBytes)
		_, retroactiveOK := rrsiPrepReadText(fields["retroactive_check"], maxRRSIPrepTextBytes)
		if !idOK || id == "" || !componentOK || !rrsiPrepComponentSupported(component) ||
			!hypothesisOK || !targetOK || !retroactiveOK || !rrsiPrepPredictedAffectedValid(fields["predicted_affected"]) {
			return 0, nil, false
		}
		for _, key := range []string{"why_not_lower_lever", "trigger_condition", "mechanism"} {
			if rawValue, exists := fields[key]; exists {
				if !rrsiPrepOptionalTextValid(rawValue) {
					return 0, nil, false
				}
			}
		}
		counts[component]++
	}
	return len(edits), counts, true
}

func rrsiPrepOptionalTextValid(raw json.RawMessage) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	_, ok := rrsiPrepReadText(raw, maxRRSIPrepTextBytes)
	return ok
}

func rrsiPrepPredictedAffectedValid(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return false
	}
	var values []json.RawMessage
	if json.Unmarshal(trimmed, &values) != nil || len(values) == 0 || len(values) > maxRRSISmokeTasks {
		return false
	}
	for _, value := range values {
		id, ok := rrsiPrepReadText(value, 512)
		if !ok || id == "" {
			return false
		}
	}
	return true
}

func isRRSIPrepEditField(value string) bool {
	switch value {
	case "id", "component", "hypothesis", "targets_mode", "predicted_affected", "retroactive_check",
		"why_not_lower_lever", "trigger_condition", "mechanism":
		return true
	default:
		return false
	}
}

func rrsiPrepComponentSupported(value string) bool {
	for _, component := range rrsiPrepComponents {
		if value == component {
			return true
		}
	}
	return false
}

func hasRRSIPrepField(fields map[string]json.RawMessage, key string) bool {
	_, ok := fields[key]
	return ok
}

func rrsiPrepSummaryShapeMatches(summary rrsiPrepSummary) bool {
	if summary.GateFailure == "" {
		return summary.CommitPresent && summary.BranchMatchesIdentity && summary.DiffPathPresent &&
			summary.MechanismPresent && summary.DiagnosticBytes == 0 && summary.EditCount >= 1
	}
	if !rrsiPrepGateFailureSupported(summary.GateFailure) || summary.CommitPresent || summary.CommitSHA256 != "" ||
		summary.BranchMatchesIdentity || summary.MechanismPresent || summary.DiagnosticBytes < 1 ||
		summary.DiagnosticBytes > maxRRSIPrepTextBytes {
		return false
	}
	switch summary.GateFailure {
	case "no_proposal":
		return !summary.DiffPathPresent && summary.EditCount == 0
	case "critic_reject":
		return summary.DiffPathPresent
	case "smoke_fail":
		return summary.DiffPathPresent && summary.EditCount >= 1
	default:
		return false
	}
}

func scanRRSIPrepFile(relative string, data []byte) scannedFile {
	file := scannedFile{entry: FileEntry{
		Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data)), Status: "ready",
	}}
	file.entry.RecordCount = 1
	item, alias, err := normalizeRRSIPrepFile(relative, data)
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

func validateRRSIPrepSummary(record Record, source FileEntry) error {
	var payload rrsiPrepSummary
	var fields map[string]json.RawMessage
	if err := strictjson.Decode(record.Payload, &payload); err != nil ||
		strictjson.Decode(record.Payload, &fields) != nil || len(fields) != 20 ||
		!rrsiJSONFieldTypesMatch(fields,
			[]string{"schema_version", "domain", "gate_failure", "round_sha256", "variant_sha256", "artifact_path_sha256", "commit_sha256", "source_sha256"},
			[]string{"commit_present", "branch_matches_identity", "diff_path_present", "mechanism_present", "human_review_required", "selection_accepted", "execution_authorized", "cutover_evidence_eligible"},
			map[string][2]int64{
				"edit_count":       {0, maxRRSIPrepEdits},
				"diagnostic_bytes": {0, maxRRSIPrepTextBytes},
				"source_bytes":     {1, maxRRSITrialFileBytes},
			}) {
		return errors.New("RRSI prep summary payload is invalid")
	}
	prep, ok := rrsiPrepSourceForPath(source.Path)
	if !ok || record.Type != rrsiPrepRecordType || !validDigest(source.SourceSHA256) ||
		source.SourceBytes < 1 || source.SourceBytes > maxRRSITrialFileBytes {
		return errors.New("RRSI prep summary source is invalid")
	}
	counts, countsOK := readRRSIPrepSummaryComponentCounts(fields["edit_component_counts"])
	if !countsOK {
		return errors.New("RRSI prep component counts are invalid")
	}
	countSum := 0
	for _, count := range counts {
		countSum += count
	}
	wantAlias := rrsiPrepAlias(prep, source.Path)
	if record.SourcePath != wantAlias || payload.SchemaVersion != rrsiPrepSummarySchema || payload.Domain != prep.domain ||
		payload.RoundSHA256 != rrsiPrepIdentityHash("round", prep.round) ||
		payload.VariantSHA256 != rrsiPrepIdentityHash("variant", prep.variant) ||
		payload.ArtifactPathSHA256 != rrsiPrepIdentityHash("path", filepath.ToSlash(source.Path)) ||
		payload.SourceSHA256 != source.SourceSHA256 || payload.SourceBytes != source.SourceBytes ||
		payload.EditCount < 0 || payload.EditCount > maxRRSIPrepEdits || countSum != payload.EditCount ||
		!rrsiPrepSummaryShapeMatches(payload) ||
		payload.CommitPresent && (!validDigest(payload.CommitSHA256) || payload.CommitSHA256 == "") ||
		!payload.CommitPresent && payload.CommitSHA256 != "" ||
		!payload.HumanReviewRequired || payload.SelectionAccepted || payload.ExecutionAuthorized ||
		payload.CutoverEvidenceEligible {
		return errors.New("RRSI prep provenance or review boundary is invalid")
	}
	for key, count := range payload.EditComponentCounts {
		if counts[key] != count {
			return errors.New("RRSI prep component-count reconciliation failed")
		}
	}
	return nil
}

func readRRSIPrepSummaryComponentCounts(raw json.RawMessage) (map[string]int, bool) {
	var fields map[string]json.RawMessage
	if strictjson.Decode(raw, &fields) != nil || fields == nil || len(fields) != len(rrsiPrepComponents) {
		return nil, false
	}
	counts := make(map[string]int, len(rrsiPrepComponents))
	for _, component := range rrsiPrepComponents {
		value, exists := fields[component]
		if !exists || !rrsiJSONIntegerValue(value, 0, maxRRSIPrepEdits) {
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
