package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

const (
	RRSIDeliverableSummarySchema = "threadforge.rrsi-deliverable-summary.v1"
	rrsiDeliverableRecordType    = "rrsi_deliverable_summary"
)

type rrsiDeliverableSource struct {
	domain       string
	recordKind   string
	jobID        string
	taskID       string
	trialKeyPath string
}

type rrsiDeliverableSummary struct {
	SchemaVersion           string `json:"schema_version"`
	Domain                  string `json:"domain"`
	RecordKind              string `json:"record_kind"`
	JobIDSHA256             string `json:"job_id_sha256"`
	TaskIDSHA256            string `json:"task_id_sha256"`
	TrialKeySHA256          string `json:"trial_key_sha256"`
	ArtifactPathSHA256      string `json:"artifact_path_sha256"`
	SourceSHA256            string `json:"source_sha256"`
	SourceBytes             int64  `json:"source_bytes"`
	ContentImported         bool   `json:"content_imported"`
	HumanReviewRequired     bool   `json:"human_review_required"`
	ExecutionAuthorized     bool   `json:"execution_authorized"`
	CutoverEvidenceEligible bool   `json:"cutover_evidence_eligible"`
}

func rrsiDeliverableSourceForPath(relative string) (rrsiDeliverableSource, bool) {
	if !safeRelative(relative) {
		return rrsiDeliverableSource{}, false
	}
	domain, supported := rrsiDomain(relative)
	if !supported {
		return rrsiDeliverableSource{}, false
	}
	tail, ok := rrsiRunTailOriginal(relative)
	if !ok || len(tail) < 3 || !strings.EqualFold(tail[0], "jobs") || tail[1] == "" {
		return rrsiDeliverableSource{}, false
	}
	switch domain {
	case "coding":
		if len(tail) != 5 || !strings.EqualFold(tail[3], "agent") || !strings.EqualFold(tail[4], "recording.cast") {
			return rrsiDeliverableSource{}, false
		}
		separator := strings.LastIndex(tail[2], "__")
		if separator < 1 || separator+2 >= len(tail[2]) {
			return rrsiDeliverableSource{}, false
		}
		return rrsiDeliverableSource{
			domain: domain, recordKind: "coding_terminal_recording", jobID: tail[1],
			taskID: tail[2][:separator], trialKeyPath: strings.Join(tail[:3], "/"),
		}, true
	case "workspace":
		if len(tail) < 6 || !isRRSISampleName(tail[2]) || tail[3] == "" || !strings.EqualFold(tail[4], "output") {
			return rrsiDeliverableSource{}, false
		}
		return rrsiDeliverableSource{
			domain: domain, recordKind: "workspace_deliverable", jobID: tail[1],
			taskID: tail[3], trialKeyPath: strings.Join(tail[:4], "/"),
		}, true
	case "eng":
		if len(tail) != 5 || tail[2] == "" || !isRRSITrialName(tail[3]) || !strings.EqualFold(tail[4], "payload.py") {
			return rrsiDeliverableSource{}, false
		}
		return rrsiDeliverableSource{
			domain: domain, recordKind: "engineering_payload", jobID: tail[1],
			taskID: tail[2], trialKeyPath: strings.Join(tail[:4], "/"),
		}, true
	default:
		return rrsiDeliverableSource{}, false
	}
}

func isRRSIDeliverablePath(relative string) bool {
	_, ok := rrsiDeliverableSourceForPath(relative)
	return ok
}

func isRRSISafelyReadPath(relative string) bool {
	return isRRSITrialMetadataPath(relative) || isRRSIDeliverablePath(relative) ||
		isRRSICriticPath(relative) || isRRSISmokePath(relative) || isRRSIPrepPath(relative)
}

func normalizeRRSIDeliverableFile(relative string, data []byte) (normalizedItem, string, error) {
	source, ok := rrsiDeliverableSourceForPath(relative)
	if !ok {
		return normalizedItem{}, "", errors.New("RRSI deliverable path is unsupported")
	}
	jobHash := rrsiDeliverableIdentityHash("job", source.jobID)
	taskHash := rrsiDeliverableIdentityHash("task", source.taskID)
	trialHash := rrsiDeliverableIdentityHash("trial", source.trialKeyPath)
	pathHash := rrsiDeliverableIdentityHash("path", filepath.ToSlash(relative))
	alias := filepath.ToSlash(filepath.Join("rrsi", "deliverables", source.domain, jobHash, taskHash, trialHash, pathHash))
	payload, err := json.Marshal(rrsiDeliverableSummary{
		SchemaVersion:           RRSIDeliverableSummarySchema,
		Domain:                  source.domain,
		RecordKind:              source.recordKind,
		JobIDSHA256:             jobHash,
		TaskIDSHA256:            taskHash,
		TrialKeySHA256:          trialHash,
		ArtifactPathSHA256:      pathHash,
		SourceSHA256:            digest(data),
		SourceBytes:             int64(len(data)),
		ContentImported:         false,
		HumanReviewRequired:     true,
		ExecutionAuthorized:     false,
		CutoverEvidenceEligible: false,
	})
	if err != nil {
		return normalizedItem{}, "", err
	}
	return normalizedItem{Type: rrsiDeliverableRecordType, Payload: payload}, alias, nil
}

func rrsiDeliverableIdentityHash(kind, value string) string {
	return digest([]byte("rrsi/deliverable/" + kind + "/v1\x00" + value))
}

func validateRRSIDeliverableSummary(record Record, source FileEntry) error {
	decoder := json.NewDecoder(bytes.NewReader(record.Payload))
	decoder.DisallowUnknownFields()
	var payload rrsiDeliverableSummary
	if err := decoder.Decode(&payload); err != nil {
		return errors.New("RRSI deliverable summary payload is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("RRSI deliverable summary payload contains trailing data")
	}
	deliverable, ok := rrsiDeliverableSourceForPath(source.Path)
	if !ok || record.Type != rrsiDeliverableRecordType || !validDigest(source.SourceSHA256) ||
		source.SourceBytes < 0 || source.SourceBytes > maxFileBytes {
		return errors.New("RRSI deliverable summary source is invalid")
	}
	jobHash := rrsiDeliverableIdentityHash("job", deliverable.jobID)
	taskHash := rrsiDeliverableIdentityHash("task", deliverable.taskID)
	trialHash := rrsiDeliverableIdentityHash("trial", deliverable.trialKeyPath)
	pathHash := rrsiDeliverableIdentityHash("path", filepath.ToSlash(source.Path))
	wantAlias := filepath.ToSlash(filepath.Join("rrsi", "deliverables", deliverable.domain, jobHash, taskHash, trialHash, pathHash))
	if record.SourcePath != wantAlias || payload.SchemaVersion != RRSIDeliverableSummarySchema ||
		payload.Domain != deliverable.domain || payload.RecordKind != deliverable.recordKind ||
		payload.JobIDSHA256 != jobHash || payload.TaskIDSHA256 != taskHash || payload.TrialKeySHA256 != trialHash ||
		payload.ArtifactPathSHA256 != pathHash || payload.SourceSHA256 != source.SourceSHA256 ||
		payload.SourceBytes != source.SourceBytes || payload.ContentImported || !payload.HumanReviewRequired ||
		payload.ExecutionAuthorized || payload.CutoverEvidenceEligible {
		return errors.New("RRSI deliverable summary provenance or advisory boundary is invalid")
	}
	return nil
}
