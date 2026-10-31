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
	rrsiCriticSummarySchema = "threadforge.rrsi-critic-summary.v1"
	rrsiCriticRecordType    = "rrsi_critic_summary"
	maxRRSICriticNotes      = 256
	maxRRSICriticTextBytes  = 8192
	maxRRSICriticAttempt    = 128
)

type rrsiCriticSource struct {
	domain       string
	round        string
	variant      string
	attempt      int
	artifactKind string
}

type rrsiCriticDocument struct {
	Verdict   string            `json:"verdict"`
	Reasons   []json.RawMessage `json:"reasons"`
	RiskNotes []json.RawMessage `json:"risk_notes"`
}

type rrsiCriticSummary struct {
	SchemaVersion           string `json:"schema_version"`
	Domain                  string `json:"domain"`
	ArtifactKind            string `json:"artifact_kind"`
	AttemptIndex            int    `json:"attempt_index"`
	RoundSHA256             string `json:"round_sha256"`
	VariantSHA256           string `json:"variant_sha256"`
	ArtifactPathSHA256      string `json:"artifact_path_sha256"`
	Verdict                 string `json:"verdict"`
	ReasonCount             int    `json:"reason_count"`
	RiskNoteCount           int    `json:"risk_note_count"`
	SourceSHA256            string `json:"source_sha256"`
	SourceBytes             int64  `json:"source_bytes"`
	HumanReviewRequired     bool   `json:"human_review_required"`
	SelectionAccepted       bool   `json:"selection_accepted"`
	ExecutionAuthorized     bool   `json:"execution_authorized"`
	CutoverEvidenceEligible bool   `json:"cutover_evidence_eligible"`
}

func rrsiCriticSourceForPath(relative string) (rrsiCriticSource, bool) {
	if !safeRelative(relative) {
		return rrsiCriticSource{}, false
	}
	domain, ok := rrsiDomain(relative)
	if !ok {
		return rrsiCriticSource{}, false
	}
	tail, ok := rrsiRunTailOriginal(relative)
	if !ok || len(tail) != 3 || !isRRSIRoundName(strings.ToLower(tail[0])) || len(tail[1]) != 1 {
		return rrsiCriticSource{}, false
	}
	variant := strings.ToLower(tail[1])
	if variant[0] < 'a' || variant[0] > 'h' {
		return rrsiCriticSource{}, false
	}
	base := strings.ToLower(tail[2])
	source := rrsiCriticSource{domain: domain, round: strings.ToLower(tail[0]), variant: variant}
	if base == "critic.json" {
		source.artifactKind = "final"
		return source, true
	}
	if !strings.HasPrefix(base, "critic_a") || !strings.HasSuffix(base, ".json") {
		return rrsiCriticSource{}, false
	}
	attemptText := strings.TrimSuffix(strings.TrimPrefix(base, "critic_a"), ".json")
	if attemptText == "" || len(attemptText) > 3 || (len(attemptText) > 1 && attemptText[0] == '0') {
		return rrsiCriticSource{}, false
	}
	attempt, err := strconv.Atoi(attemptText)
	if err != nil || attempt < 0 || attempt > maxRRSICriticAttempt {
		return rrsiCriticSource{}, false
	}
	source.attempt = attempt
	source.artifactKind = "attempt"
	return source, true
}

func isRRSICriticPath(relative string) bool {
	_, ok := rrsiCriticSourceForPath(relative)
	return ok
}

func rrsiCriticIdentityHash(kind, value string) string {
	return digest([]byte("rrsi/critic/" + kind + "/v1\x00" + value))
}

func rrsiCriticAlias(source rrsiCriticSource, relative string) string {
	return filepath.ToSlash(filepath.Join("rrsi", "critics", source.domain,
		rrsiCriticIdentityHash("round", source.round),
		rrsiCriticIdentityHash("variant", source.variant),
		rrsiCriticIdentityHash("path", filepath.ToSlash(relative))))
}

func normalizeRRSICriticFile(relative string, data []byte) (normalizedItem, string, error) {
	source, ok := rrsiCriticSourceForPath(relative)
	if !ok {
		return normalizedItem{}, "", errors.New("RRSI critic artifact path is unsupported")
	}
	if !utf8.Valid(data) {
		return normalizedItem{}, "", errors.New("RRSI critic artifact is not valid UTF-8")
	}
	var document rrsiCriticDocument
	var sourceFields map[string]json.RawMessage
	if err := strictjson.Decode(data, &document); err != nil || strictjson.Decode(data, &sourceFields) != nil ||
		len(sourceFields) != 3 ||
		(document.Verdict != "accept" && document.Verdict != "reject") ||
		document.Reasons == nil || document.RiskNotes == nil ||
		len(document.Reasons) > maxRRSICriticNotes || len(document.RiskNotes) > maxRRSICriticNotes {
		return normalizedItem{}, "", errors.New("RRSI critic artifact does not match the pinned producer contract")
	}
	for _, raw := range append(append([]json.RawMessage(nil), document.Reasons...), document.RiskNotes...) {
		var note string
		if !rrsiJSONStringValue(raw) || json.Unmarshal(raw, &note) != nil ||
			len(note) > maxRRSICriticTextBytes || strings.ContainsRune(note, '\x00') {
			return normalizedItem{}, "", errors.New("RRSI critic note is invalid or exceeds its bounded source contract")
		}
	}
	pathHash := rrsiCriticIdentityHash("path", filepath.ToSlash(relative))
	payload, err := json.Marshal(rrsiCriticSummary{
		SchemaVersion:           rrsiCriticSummarySchema,
		Domain:                  source.domain,
		ArtifactKind:            source.artifactKind,
		AttemptIndex:            source.attempt,
		RoundSHA256:             rrsiCriticIdentityHash("round", source.round),
		VariantSHA256:           rrsiCriticIdentityHash("variant", source.variant),
		ArtifactPathSHA256:      pathHash,
		Verdict:                 document.Verdict,
		ReasonCount:             len(document.Reasons),
		RiskNoteCount:           len(document.RiskNotes),
		SourceSHA256:            digest(data),
		SourceBytes:             int64(len(data)),
		HumanReviewRequired:     true,
		SelectionAccepted:       false,
		ExecutionAuthorized:     false,
		CutoverEvidenceEligible: false,
	})
	if err != nil {
		return normalizedItem{}, "", err
	}
	return normalizedItem{Type: rrsiCriticRecordType, Payload: payload}, rrsiCriticAlias(source, relative), nil
}

func scanRRSICriticFile(relative string, data []byte) scannedFile {
	file := scannedFile{entry: FileEntry{
		Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data)), Status: "ready",
	}}
	file.entry.RecordCount = 1
	item, alias, err := normalizeRRSICriticFile(relative, data)
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

func validateRRSICriticSummary(record Record, source FileEntry) error {
	var payload rrsiCriticSummary
	var fields map[string]json.RawMessage
	if err := strictjson.Decode(record.Payload, &payload); err != nil ||
		strictjson.Decode(record.Payload, &fields) != nil || len(fields) != 16 ||
		!rrsiJSONFieldTypesMatch(fields,
			[]string{"schema_version", "domain", "artifact_kind", "round_sha256", "variant_sha256", "artifact_path_sha256", "verdict", "source_sha256"},
			[]string{"human_review_required", "selection_accepted", "execution_authorized", "cutover_evidence_eligible"},
			map[string][2]int64{
				"attempt_index":   {0, maxRRSICriticAttempt},
				"reason_count":    {0, maxRRSICriticNotes},
				"risk_note_count": {0, maxRRSICriticNotes},
				"source_bytes":    {0, maxRRSITrialFileBytes},
			}) {
		return errors.New("RRSI critic summary payload is invalid")
	}
	critic, ok := rrsiCriticSourceForPath(source.Path)
	if !ok || record.Type != rrsiCriticRecordType || !validDigest(source.SourceSHA256) ||
		source.SourceBytes < 0 || source.SourceBytes > maxRRSITrialFileBytes {
		return errors.New("RRSI critic summary source is invalid")
	}
	wantAlias := rrsiCriticAlias(critic, source.Path)
	if record.SourcePath != wantAlias || payload.SchemaVersion != rrsiCriticSummarySchema ||
		payload.Domain != critic.domain || payload.ArtifactKind != critic.artifactKind ||
		payload.AttemptIndex != critic.attempt ||
		payload.RoundSHA256 != rrsiCriticIdentityHash("round", critic.round) ||
		payload.VariantSHA256 != rrsiCriticIdentityHash("variant", critic.variant) ||
		payload.ArtifactPathSHA256 != rrsiCriticIdentityHash("path", filepath.ToSlash(source.Path)) ||
		(payload.Verdict != "accept" && payload.Verdict != "reject") ||
		payload.ReasonCount < 0 || payload.ReasonCount > maxRRSICriticNotes ||
		payload.RiskNoteCount < 0 || payload.RiskNoteCount > maxRRSICriticNotes ||
		payload.SourceSHA256 != source.SourceSHA256 || payload.SourceBytes != source.SourceBytes ||
		!payload.HumanReviewRequired || payload.SelectionAccepted || payload.ExecutionAuthorized ||
		payload.CutoverEvidenceEligible {
		return errors.New("RRSI critic summary provenance or review boundary is invalid")
	}
	return nil
}
