package migrate

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/strictjson"
)

const (
	rrsiSmokeSummarySchema  = "threadforge.rrsi-smoke-summary.v1"
	rrsiSmokeRecordType     = "rrsi_smoke_summary"
	maxRRSISmokeFields      = 512
	maxRRSISmokeTasks       = 10000
	maxRRSISmokeMetrics     = 64
	maxRRSISmokeTextBytes   = 8192
	rrsiEngSmokeMetricCount = 6
)

type rrsiSmokeSource struct {
	domain  string
	round   string
	variant string
}

type rrsiSmokeSummary struct {
	SchemaVersion           string `json:"schema_version"`
	Domain                  string `json:"domain"`
	Stage                   string `json:"stage"`
	RoundSHA256             string `json:"round_sha256"`
	VariantSHA256           string `json:"variant_sha256"`
	ArtifactPathSHA256      string `json:"artifact_path_sha256"`
	ProducerReportedOK      bool   `json:"producer_reported_ok"`
	ReportedCount           int    `json:"reported_count"`
	ReportedCountPresent    bool   `json:"reported_count_present"`
	MissingCount            int    `json:"missing_count"`
	MissingCountPresent     bool   `json:"missing_count_present"`
	SmokeIDCount            int    `json:"smoke_id_count"`
	NumericMetricCount      int    `json:"numeric_metric_count"`
	SupplementalFieldCount  int    `json:"supplemental_field_count"`
	SourceSHA256            string `json:"source_sha256"`
	SourceBytes             int64  `json:"source_bytes"`
	HumanReviewRequired     bool   `json:"human_review_required"`
	SelectionAccepted       bool   `json:"selection_accepted"`
	ExecutionAuthorized     bool   `json:"execution_authorized"`
	CutoverEvidenceEligible bool   `json:"cutover_evidence_eligible"`
}

func rrsiSmokeSourceForPath(relative string) (rrsiSmokeSource, bool) {
	if !safeRelative(relative) {
		return rrsiSmokeSource{}, false
	}
	domain, ok := rrsiDomain(relative)
	if !ok {
		return rrsiSmokeSource{}, false
	}
	tail, ok := rrsiRunTailOriginal(relative)
	if !ok || len(tail) != 3 || !isRRSIRoundName(strings.ToLower(tail[0])) || len(tail[1]) != 1 ||
		strings.ToLower(tail[1])[0] < 'a' || strings.ToLower(tail[1])[0] > 'h' ||
		!strings.EqualFold(tail[2], "smoke.json") {
		return rrsiSmokeSource{}, false
	}
	return rrsiSmokeSource{domain: domain, round: strings.ToLower(tail[0]), variant: strings.ToLower(tail[1])}, true
}

func isRRSISmokePath(relative string) bool {
	_, ok := rrsiSmokeSourceForPath(relative)
	return ok
}

func rrsiSmokeIdentityHash(kind, value string) string {
	return digest([]byte("rrsi/smoke/" + kind + "/v1\x00" + value))
}

func rrsiSmokeAlias(source rrsiSmokeSource, relative string) string {
	return filepath.ToSlash(filepath.Join("rrsi", "smokes", source.domain,
		rrsiSmokeIdentityHash("round", source.round),
		rrsiSmokeIdentityHash("variant", source.variant),
		rrsiSmokeIdentityHash("path", filepath.ToSlash(relative))))
}

func normalizeRRSISmokeFile(relative string, data []byte) (normalizedItem, string, error) {
	source, ok := rrsiSmokeSourceForPath(relative)
	if !ok {
		return normalizedItem{}, "", errors.New("RRSI smoke artifact path is unsupported")
	}
	if !utf8.Valid(data) {
		return normalizedItem{}, "", errors.New("RRSI smoke artifact is not valid UTF-8")
	}
	var fields map[string]json.RawMessage
	if err := strictjson.Decode(data, &fields); err != nil || fields == nil || len(fields) < 2 || len(fields) > maxRRSISmokeFields {
		return normalizedItem{}, "", errors.New("RRSI smoke artifact JSON structure is invalid")
	}
	okRaw, hasOK := fields["ok"]
	stageRaw, hasStage := fields["stage"]
	var producerOK bool
	var stage string
	if !hasOK || !rrsiJSONBoolValue(okRaw) || json.Unmarshal(okRaw, &producerOK) != nil ||
		!hasStage || !rrsiJSONStringValue(stageRaw) || json.Unmarshal(stageRaw, &stage) != nil ||
		!rrsiSmokeStageSupported(source.domain, stage) {
		return normalizedItem{}, "", errors.New("RRSI smoke artifact result fields are invalid")
	}
	if producerOK && (stage == "compile" || stage == "ctor") {
		return normalizedItem{}, "", errors.New("RRSI smoke stage/result combination is unsupported")
	}
	if producerOK && stage != "smoke_run" && stage != "run" {
		return normalizedItem{}, "", errors.New("RRSI smoke success stage is unsupported")
	}
	summary := rrsiSmokeSummary{
		SchemaVersion:           rrsiSmokeSummarySchema,
		Domain:                  source.domain,
		Stage:                   stage,
		ProducerReportedOK:      producerOK,
		HumanReviewRequired:     true,
		SelectionAccepted:       false,
		ExecutionAuthorized:     false,
		CutoverEvidenceEligible: false,
	}
	for key, raw := range fields {
		if key == "ok" || key == "stage" {
			continue
		}
		summary.SupplementalFieldCount++
		switch key {
		case "detail", "err":
			if !rrsiSmokeTextFieldAllowed(source.domain, stage, producerOK, key) || !validRRSISmokeText(raw) {
				return normalizedItem{}, "", errors.New("RRSI smoke diagnostic field is invalid")
			}
		case "n":
			if (source.domain != "coding" && source.domain != "workspace") || stage != "smoke_run" || !producerOK ||
				!readRRSISmokeBoundedInt(raw, maxRRSISmokeTasks, &summary.ReportedCount) {
				return normalizedItem{}, "", errors.New("RRSI smoke reported count is invalid")
			}
			summary.ReportedCountPresent = true
		case "missing":
			if source.domain != "eng" || stage != "run" ||
				!readRRSISmokeBoundedInt(raw, maxRRSISmokeTasks, &summary.MissingCount) {
				return normalizedItem{}, "", errors.New("RRSI smoke missing count is invalid")
			}
			summary.MissingCountPresent = true
		case "smoke_ids":
			if source.domain != "eng" || stage != "run" || !readRRSISmokeIDCount(raw, &summary.SmokeIDCount) {
				return normalizedItem{}, "", errors.New("RRSI smoke task list is invalid")
			}
		default:
			if source.domain == "eng" && stage == "run" && isRRSISmokeMetric(key) && readRRSISmokeMetric(key, raw) {
				summary.NumericMetricCount++
				continue
			}
			if source.domain == "coding" && stage == "smoke_run" && validRRSISmokeText(raw) {
				continue
			}
			return normalizedItem{}, "", errors.New("RRSI smoke artifact contains an unsupported field")
		}
	}
	if !rrsiSmokeSourceShapeMatches(source, stage, producerOK, fields, summary) {
		return normalizedItem{}, "", errors.New("RRSI smoke artifact is incomplete for its producer stage")
	}
	if summary.SupplementalFieldCount > maxRRSISmokeFields-2 || summary.NumericMetricCount > maxRRSISmokeMetrics {
		return normalizedItem{}, "", errors.New("RRSI smoke artifact exceeds structural limits")
	}
	summary.RoundSHA256 = rrsiSmokeIdentityHash("round", source.round)
	summary.VariantSHA256 = rrsiSmokeIdentityHash("variant", source.variant)
	summary.ArtifactPathSHA256 = rrsiSmokeIdentityHash("path", filepath.ToSlash(relative))
	summary.SourceSHA256 = digest(data)
	summary.SourceBytes = int64(len(data))
	payload, err := json.Marshal(summary)
	if err != nil {
		return normalizedItem{}, "", err
	}
	return normalizedItem{Type: rrsiSmokeRecordType, Payload: payload}, rrsiSmokeAlias(source, relative), nil
}

func rrsiSmokeSourceShapeMatches(source rrsiSmokeSource, stage string, producerOK bool,
	fields map[string]json.RawMessage, summary rrsiSmokeSummary) bool {
	switch source.domain {
	case "coding":
		if stage == "compile" || stage == "ctor" {
			return !producerOK && len(fields) == 3 && hasRRSISmokeField(fields, "detail")
		}
		if producerOK {
			return stage == "smoke_run" && summary.ReportedCountPresent && len(fields) == 3
		}
		return stage == "smoke_run" && !summary.ReportedCountPresent && len(fields) >= 3
	case "workspace":
		if stage == "compile" {
			return !producerOK && len(fields) == 3 && hasRRSISmokeField(fields, "detail")
		}
		if stage == "smoke_run" && producerOK {
			return summary.ReportedCountPresent && len(fields) == 3 && hasRRSISmokeField(fields, "n")
		}
		if stage == "smoke_run" {
			return !producerOK && len(fields) == 3 && hasRRSISmokeField(fields, "detail")
		}
	case "eng":
		if stage == "compile" || stage == "ctor" {
			return !producerOK && len(fields) == 3 && hasRRSISmokeField(fields, "err")
		}
		if stage == "run" {
			return len(fields) == 2+2+rrsiEngSmokeMetricCount && summary.MissingCountPresent &&
				summary.NumericMetricCount == rrsiEngSmokeMetricCount &&
				hasRRSISmokeField(fields, "smoke_ids") && hasRRSISmokeField(fields, "missing")
		}
	}
	return false
}

func hasRRSISmokeField(fields map[string]json.RawMessage, key string) bool {
	_, ok := fields[key]
	return ok
}

func rrsiSmokeStageSupported(domain, stage string) bool {
	switch domain {
	case "coding":
		return stage == "compile" || stage == "ctor" || stage == "smoke_run"
	case "workspace":
		return stage == "compile" || stage == "smoke_run"
	case "eng":
		return stage == "compile" || stage == "ctor" || stage == "run"
	default:
		return false
	}
}

func rrsiSmokeTextFieldAllowed(domain, stage string, producerOK bool, key string) bool {
	if domain == "eng" {
		return key == "err" && !producerOK && (stage == "compile" || stage == "ctor")
	}
	return key == "detail" && !producerOK && (stage == "compile" || stage == "ctor" ||
		(domain == "workspace" && stage == "smoke_run"))
}

func validRRSISmokeText(raw json.RawMessage) bool {
	var value string
	return rrsiJSONStringValue(raw) && json.Unmarshal(raw, &value) == nil &&
		len(value) <= maxRRSISmokeTextBytes && !strings.ContainsRune(value, '\x00')
}

func readRRSISmokeBoundedInt(raw json.RawMessage, max int, destination *int) bool {
	if !rrsiJSONIntegerValue(raw, 0, int64(max)) {
		return false
	}
	value, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	*destination = value
	return true
}

func readRRSISmokeIDCount(raw json.RawMessage, destination *int) bool {
	if len(raw) == 0 || raw[0] != '[' {
		return false
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) > maxRRSISmokeTasks {
		return false
	}
	for _, rawValue := range values {
		var value string
		if !rrsiJSONStringValue(rawValue) || json.Unmarshal(rawValue, &value) != nil ||
			len(value) > 512 || strings.ContainsRune(value, '\x00') {
			return false
		}
	}
	*destination = len(values)
	return true
}

func isRRSISmokeMetric(key string) bool {
	switch key {
	case "total_passes", "n_trials", "pass_rate", "mean_combined_score", "valid_rate", "no_payload_rate":
		return true
	default:
		return false
	}
}

func readRRSISmokeMetric(key string, raw json.RawMessage) bool {
	switch key {
	case "total_passes", "n_trials":
		return rrsiJSONIntegerValue(raw, 0, maxRRSISmokeTasks)
	case "pass_rate", "valid_rate", "no_payload_rate":
		return rrsiJSONNumberValue(raw, 0, 1)
	case "mean_combined_score":
		return rrsiJSONNumberValue(raw, -1_000_000_000, 1_000_000_000)
	default:
		return false
	}
}

func scanRRSISmokeFile(relative string, data []byte) scannedFile {
	file := scannedFile{entry: FileEntry{
		Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data)), Status: "ready",
	}}
	file.entry.RecordCount = 1
	item, alias, err := normalizeRRSISmokeFile(relative, data)
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

func validateRRSISmokeSummary(record Record, source FileEntry) error {
	var payload rrsiSmokeSummary
	var fields map[string]json.RawMessage
	if err := strictjson.Decode(record.Payload, &payload); err != nil ||
		strictjson.Decode(record.Payload, &fields) != nil || len(fields) != 20 ||
		!rrsiJSONFieldTypesMatch(fields,
			[]string{"schema_version", "domain", "stage", "round_sha256", "variant_sha256", "artifact_path_sha256", "source_sha256"},
			[]string{"producer_reported_ok", "reported_count_present", "missing_count_present", "human_review_required", "selection_accepted", "execution_authorized", "cutover_evidence_eligible"},
			map[string][2]int64{
				"reported_count":           {0, maxRRSISmokeTasks},
				"missing_count":            {0, maxRRSISmokeTasks},
				"smoke_id_count":           {0, maxRRSISmokeTasks},
				"numeric_metric_count":     {0, maxRRSISmokeMetrics},
				"supplemental_field_count": {0, maxRRSISmokeFields - 2},
				"source_bytes":             {1, maxRRSITrialFileBytes},
			}) {
		return errors.New("RRSI smoke summary payload is invalid")
	}
	smoke, ok := rrsiSmokeSourceForPath(source.Path)
	if !ok || record.Type != rrsiSmokeRecordType || !validDigest(source.SourceSHA256) ||
		source.SourceBytes < 1 || source.SourceBytes > maxRRSITrialFileBytes {
		return errors.New("RRSI smoke summary source is invalid")
	}
	wantAlias := rrsiSmokeAlias(smoke, source.Path)
	if record.SourcePath != wantAlias || payload.SchemaVersion != rrsiSmokeSummarySchema ||
		payload.Domain != smoke.domain || !rrsiSmokeStageSupported(smoke.domain, payload.Stage) ||
		!rrsiSmokeSummaryShapeMatches(smoke.domain, payload) ||
		payload.ProducerReportedOK && (payload.Stage == "compile" || payload.Stage == "ctor") ||
		payload.ProducerReportedOK && payload.Stage != "smoke_run" && payload.Stage != "run" ||
		payload.ReportedCountPresent && (!payload.ProducerReportedOK || payload.Stage != "smoke_run" || smoke.domain == "eng") ||
		!payload.ReportedCountPresent && payload.ReportedCount != 0 ||
		payload.MissingCountPresent && (payload.Stage != "run" || smoke.domain != "eng") ||
		!payload.MissingCountPresent && payload.MissingCount != 0 ||
		smoke.domain != "eng" && (payload.MissingCountPresent || payload.MissingCount != 0 ||
			payload.SmokeIDCount != 0 || payload.NumericMetricCount != 0) ||
		smoke.domain == "eng" && payload.Stage != "run" &&
			(payload.MissingCountPresent || payload.SmokeIDCount != 0 || payload.NumericMetricCount != 0) ||
		payload.RoundSHA256 != rrsiSmokeIdentityHash("round", smoke.round) ||
		payload.VariantSHA256 != rrsiSmokeIdentityHash("variant", smoke.variant) ||
		payload.ArtifactPathSHA256 != rrsiSmokeIdentityHash("path", filepath.ToSlash(source.Path)) ||
		payload.ReportedCount < 0 || payload.ReportedCount > maxRRSISmokeTasks ||
		payload.MissingCount < 0 || payload.MissingCount > maxRRSISmokeTasks ||
		payload.SmokeIDCount < 0 || payload.SmokeIDCount > maxRRSISmokeTasks ||
		payload.NumericMetricCount < 0 || payload.NumericMetricCount > maxRRSISmokeMetrics ||
		payload.SupplementalFieldCount < 0 || payload.SupplementalFieldCount > maxRRSISmokeFields-2 ||
		payload.SourceSHA256 != source.SourceSHA256 || payload.SourceBytes != source.SourceBytes ||
		!payload.HumanReviewRequired || payload.SelectionAccepted || payload.ExecutionAuthorized ||
		payload.CutoverEvidenceEligible {
		return errors.New("RRSI smoke summary provenance or review boundary is invalid")
	}
	return nil
}

func rrsiSmokeSummaryShapeMatches(domain string, summary rrsiSmokeSummary) bool {
	switch domain {
	case "coding":
		if summary.Stage == "compile" || summary.Stage == "ctor" {
			return !summary.ProducerReportedOK && !summary.ReportedCountPresent && summary.SupplementalFieldCount == 1
		}
		if summary.ProducerReportedOK {
			return summary.Stage == "smoke_run" && summary.ReportedCountPresent && summary.SupplementalFieldCount == 1
		}
		return summary.Stage == "smoke_run" && !summary.ReportedCountPresent && summary.SupplementalFieldCount >= 1
	case "workspace":
		if summary.Stage == "compile" {
			return !summary.ProducerReportedOK && !summary.ReportedCountPresent && summary.SupplementalFieldCount == 1
		}
		if summary.Stage == "smoke_run" && summary.ProducerReportedOK {
			return summary.ReportedCountPresent && summary.SupplementalFieldCount == 1
		}
		return summary.Stage == "smoke_run" && !summary.ProducerReportedOK &&
			!summary.ReportedCountPresent && summary.SupplementalFieldCount == 1
	case "eng":
		if summary.Stage == "compile" || summary.Stage == "ctor" {
			return !summary.ProducerReportedOK && !summary.MissingCountPresent && summary.SupplementalFieldCount == 1
		}
		return summary.Stage == "run" && summary.MissingCountPresent &&
			summary.NumericMetricCount == rrsiEngSmokeMetricCount && summary.SupplementalFieldCount == 2+rrsiEngSmokeMetricCount
	default:
		return false
	}
}
