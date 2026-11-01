package memoryfeedback

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	SchemaVersion                 = "threadforge.memory-feedback.v1"
	SchemaVersionV2               = "threadforge.memory-feedback.v2"
	SchemaVersionV3               = "threadforge.memory-feedback.v3"
	RecordResultVersion           = "threadforge.memory-feedback-result.v2"
	RecordResultVersionV3         = "threadforge.memory-feedback-result.v3"
	ListResultVersion             = "threadforge.memory-feedback-list.v2"
	ListResultVersionV3           = "threadforge.memory-feedback-list.v3"
	SummaryResultVersion          = "threadforge.memory-feedback-summary.v2"
	SummaryResultVersionV3        = "threadforge.memory-feedback-summary.v3"
	FailureSummaryResultVersion   = "threadforge.memory-feedback-failures.v1"
	FailureSummaryResultVersionV2 = "threadforge.memory-feedback-failures.v2"
	RetrievalReceiptVersion       = "threadforge.memory-retrieval-receipt.v1"
	RetrievalReceiptVersionV2     = "threadforge.memory-retrieval-receipt.v2"
	RetrievalReceiptVersionV3     = "threadforge.memory-retrieval-receipt.v3"
	RetrievalReceiptVersionV4     = "threadforge.memory-retrieval-receipt.v4"
	RetrievalReceiptListVersion   = "threadforge.memory-retrieval-receipt-list.v1"
	RetrievalReceiptListVersionV2 = "threadforge.memory-retrieval-receipt-list.v2"
	EvidenceSelfReported          = "self_reported"
	MaxProjectRecords             = 100000
	MaxSummaryRecords             = 10000
	MaxRetrievalReceiptBatch      = 32
)

type TargetKind string

const (
	TargetProcedure   TargetKind = "procedure"
	TargetFact        TargetKind = "fact"
	TargetDecision    TargetKind = "decision"
	TargetPolicy      TargetKind = "policy"
	TargetTaskContext TargetKind = "task_context"
)

type Exposure string

const (
	ExposureUsed         Exposure = "used"
	ExposureShownNotUsed Exposure = "shown_not_used"
)

type Utility string

const (
	UtilityHelpful     Utility = "helpful"
	UtilityNeutral     Utility = "neutral"
	UtilityHarmful     Utility = "harmful"
	UtilityNotAssessed Utility = "not_assessed"
)

// Feedback stores only a bounded, explicitly self-reported association between
// one approved memory revision and one task outcome. It contains no memory,
// prompt, trace, task, or evidence body and is never cutover evidence.
type Feedback struct {
	SchemaVersion           string     `json:"schema_version"`
	ID                      string     `json:"id"`
	ProjectID               string     `json:"project_id"`
	TaskID                  string     `json:"task_id"`
	SessionID               string     `json:"session_id,omitempty"`
	RetrievalReceiptID      string     `json:"retrieval_receipt_id,omitempty"`
	TargetKind              TargetKind `json:"target_kind"`
	TargetID                string     `json:"target_id"`
	TargetRevision          int        `json:"target_revision"`
	Exposure                Exposure   `json:"exposure"`
	Utility                 Utility    `json:"utility"`
	TaskResult              string     `json:"task_result"`
	FailureSignatureSHA256  string     `json:"failure_signature_sha256,omitempty"`
	Harness                 string     `json:"harness"`
	Actor                   string     `json:"actor"`
	EvidenceClass           string     `json:"evidence_class"`
	CutoverEvidenceEligible bool       `json:"cutover_evidence_eligible"`
	ExecutionAuthorized     bool       `json:"execution_authorized"`
	CreatedAt               string     `json:"created_at"`
}

// RetrievalReceipt records a local observation boundary for one approved
// memory revision. V1 is procedure-only; V2 adds exact project-memory reads;
// V3 records host-hook response preparation. None proves transport delivery,
// model exposure, interpretation, or use.
type RetrievalReceipt struct {
	SchemaVersion       string     `json:"schema_version"`
	ID                  string     `json:"id"`
	ProjectID           string     `json:"project_id"`
	TaskID              string     `json:"task_id"`
	SessionID           string     `json:"session_id,omitempty"`
	TargetKind          TargetKind `json:"target_kind"`
	TargetID            string     `json:"target_id"`
	TargetRevision      int        `json:"target_revision"`
	TaskRevision        int        `json:"task_revision,omitempty"`
	Tool                string     `json:"tool"`
	Host                string     `json:"host,omitempty"`
	Freshness           string     `json:"freshness"`
	ResultStatus        string     `json:"result_status,omitempty"`
	GuidanceIncluded    bool       `json:"guidance_included"`
	ObservationBoundary string     `json:"observation_boundary"`
	CreatedAt           string     `json:"created_at"`
}

type RetrievalReceiptFilter struct {
	ProjectID string
	TaskID    string
	TargetID  string
	Limit     int
	Offset    int
}

type RetrievalReceiptListResult struct {
	SchemaVersion string             `json:"schema_version"`
	ProjectID     string             `json:"project_id"`
	TaskID        string             `json:"task_id"`
	Total         int                `json:"total"`
	Limit         int                `json:"limit"`
	Offset        int                `json:"offset"`
	Receipts      []RetrievalReceipt `json:"receipts"`
}

type Filter struct {
	ProjectID  string
	TaskID     string
	TargetKind TargetKind
	TargetID   string
	Harness    string
	Limit      int
	Offset     int
}

type RecordResult struct {
	SchemaVersion string   `json:"schema_version"`
	Status        string   `json:"status"`
	Sequence      int64    `json:"sequence"`
	Feedback      Feedback `json:"feedback"`
}

type ListResult struct {
	SchemaVersion           string     `json:"schema_version"`
	ProjectID               string     `json:"project_id"`
	Total                   int        `json:"total"`
	Limit                   int        `json:"limit"`
	Offset                  int        `json:"offset"`
	Feedback                []Feedback `json:"feedback"`
	EvidenceClass           string     `json:"evidence_class"`
	CutoverEvidenceEligible bool       `json:"cutover_evidence_eligible"`
	ExecutionAuthorized     bool       `json:"execution_authorized"`
}

type SummaryGroup struct {
	TargetKind        TargetKind `json:"target_kind"`
	TargetID          string     `json:"target_id"`
	TargetRevision    int        `json:"target_revision"`
	Harness           string     `json:"harness"`
	Exposure          Exposure   `json:"exposure"`
	Utility           Utility    `json:"utility"`
	TaskResult        string     `json:"task_result"`
	Count             int        `json:"count"`
	DistinctTaskCount int        `json:"distinct_task_count"`
}

type SummaryResult struct {
	SchemaVersion           string         `json:"schema_version"`
	Status                  string         `json:"status"`
	ProjectID               string         `json:"project_id"`
	Records                 int            `json:"records"`
	RetrievalReceiptLinked  int            `json:"retrieval_receipt_linked"`
	Groups                  []SummaryGroup `json:"groups"`
	EvidenceClass           string         `json:"evidence_class"`
	CutoverEvidenceEligible bool           `json:"cutover_evidence_eligible"`
	ExecutionAuthorized     bool           `json:"execution_authorized"`
}

// FailureGroup reports repeated opaque failure signatures from local,
// self-reported feedback. Task identifiers and failure text are never returned.
type FailureGroup struct {
	TargetKind                  TargetKind `json:"target_kind"`
	TargetID                    string     `json:"target_id"`
	TargetRevision              int        `json:"target_revision"`
	Harness                     string     `json:"harness"`
	Exposure                    Exposure   `json:"exposure"`
	Utility                     Utility    `json:"utility"`
	FailureSignatureSHA256      string     `json:"failure_signature_sha256"`
	FeedbackCount               int        `json:"feedback_count"`
	DistinctTaskCount           int        `json:"distinct_task_count"`
	RepeatedAfterFirstTaskCount int        `json:"repeated_after_first_task_count"`
}

type FailureSummaryResult struct {
	SchemaVersion           string         `json:"schema_version"`
	Status                  string         `json:"status"`
	ProjectID               string         `json:"project_id"`
	RecordsScanned          int            `json:"records_scanned"`
	SignatureRecords        int            `json:"signature_records"`
	Groups                  []FailureGroup `json:"groups"`
	EvidenceClass           string         `json:"evidence_class"`
	CutoverEvidenceEligible bool           `json:"cutover_evidence_eligible"`
	ExecutionAuthorized     bool           `json:"execution_authorized"`
}

var stableID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var retrievalReceiptID = regexp.MustCompile(`^retrieval-[a-f0-9]{32}$`)
var sha256Digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Validate(value Feedback) error {
	var problems []string
	isV1 := value.SchemaVersion == SchemaVersion
	isV2 := value.SchemaVersion == SchemaVersionV2
	isV3 := value.SchemaVersion == SchemaVersionV3
	if !isV1 && !isV2 && !isV3 {
		problems = append(problems, "unsupported schema_version")
	}
	if isV1 && value.FailureSignatureSHA256 != "" {
		problems = append(problems, "v1 feedback does not include failure_signature_sha256")
	}
	if !stableID.MatchString(value.ID) {
		problems = append(problems, "id must be a lowercase stable identifier")
	}
	if !bounded(value.ProjectID, 256) {
		problems = append(problems, "project_id must contain 1 to 256 characters")
	}
	if !stableID.MatchString(value.TaskID) {
		problems = append(problems, "task_id must be a lowercase stable identifier")
	}
	if value.SessionID != "" && !stableID.MatchString(value.SessionID) {
		problems = append(problems, "session_id must be a lowercase stable identifier when set")
	}
	if value.RetrievalReceiptID != "" && !retrievalReceiptID.MatchString(value.RetrievalReceiptID) {
		problems = append(problems, "retrieval_receipt_id must be a valid local receipt ID when set")
	}
	switch value.TargetKind {
	case TargetProcedure, TargetFact, TargetDecision, TargetPolicy:
	case TargetTaskContext:
		if !isV3 {
			problems = append(problems, "task_context feedback requires v3")
		}
	default:
		problems = append(problems, "target_kind must be procedure, fact, decision, policy, or task_context")
	}
	if value.TargetKind == TargetTaskContext {
		if value.TargetID != "task-resume-context" {
			problems = append(problems, "task_context target_id must be task-resume-context")
		}
		if value.TargetRevision != 1 {
			problems = append(problems, "task_context target_revision must be 1")
		}
		if value.RetrievalReceiptID == "" {
			problems = append(problems, "task_context feedback requires a matching retrieval receipt")
		}
	}
	if !stableID.MatchString(value.TargetID) {
		problems = append(problems, "target_id must be a lowercase stable identifier")
	}
	if value.TargetRevision < 1 {
		problems = append(problems, "target_revision must be at least 1")
	}
	switch value.Exposure {
	case ExposureUsed, ExposureShownNotUsed:
	default:
		problems = append(problems, "exposure must be used or shown_not_used")
	}
	switch value.Utility {
	case UtilityHelpful, UtilityNeutral, UtilityHarmful:
		if value.Exposure != ExposureUsed {
			problems = append(problems, "shown_not_used exposure requires utility not_assessed")
		}
	case UtilityNotAssessed:
		if value.Exposure != ExposureShownNotUsed {
			problems = append(problems, "used exposure requires an assessed utility")
		}
	default:
		problems = append(problems, "utility must be helpful, neutral, harmful, or not_assessed")
	}
	if value.TaskResult != "passed" && value.TaskResult != "failed" && value.TaskResult != "inconclusive" {
		problems = append(problems, "task_result must be passed, failed, or inconclusive")
	}
	if value.FailureSignatureSHA256 != "" {
		if (!isV2 && !isV3) || !sha256Digest.MatchString(value.FailureSignatureSHA256) {
			problems = append(problems, "failure_signature_sha256 must be a lowercase SHA-256 digest in v2 or v3 feedback")
		}
		if value.TaskResult != "failed" {
			problems = append(problems, "failure_signature_sha256 is allowed only for failed tasks")
		}
	}
	if !stableID.MatchString(value.Harness) {
		problems = append(problems, "harness must be a lowercase stable identifier")
	}
	if !bounded(value.Actor, 128) {
		problems = append(problems, "actor must contain 1 to 128 characters")
	}
	if value.EvidenceClass != EvidenceSelfReported {
		problems = append(problems, "evidence_class must be self_reported")
	}
	if value.CutoverEvidenceEligible {
		problems = append(problems, "self-reported feedback is not cutover evidence")
	}
	if value.ExecutionAuthorized {
		problems = append(problems, "memory feedback never authorizes execution")
	}
	if _, err := time.Parse(time.RFC3339Nano, value.CreatedAt); err != nil {
		problems = append(problems, "created_at must use RFC3339")
	}
	for _, field := range []string{value.ID, value.ProjectID, value.TaskID, value.SessionID, value.RetrievalReceiptID, string(value.TargetKind), value.TargetID, string(value.Exposure), string(value.Utility), value.TaskResult, value.Harness, value.Actor} {
		if privacy.ContainsCredentialText(field) {
			problems = append(problems, "feedback contains credential material")
			break
		}
	}
	if len(problems) != 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func ValidateRetrievalReceipt(value RetrievalReceipt) error {
	var problems []string
	isV1 := value.SchemaVersion == RetrievalReceiptVersion
	isV2 := value.SchemaVersion == RetrievalReceiptVersionV2
	isV3 := value.SchemaVersion == RetrievalReceiptVersionV3
	isV4 := value.SchemaVersion == RetrievalReceiptVersionV4
	if !isV1 && !isV2 && !isV3 && !isV4 {
		problems = append(problems, "unsupported schema_version")
	}
	if !retrievalReceiptID.MatchString(value.ID) {
		problems = append(problems, "id must be a valid local receipt ID")
	}
	if !bounded(value.ProjectID, 256) {
		problems = append(problems, "project_id must contain 1 to 256 characters")
	}
	if !stableID.MatchString(value.TaskID) {
		problems = append(problems, "task_id must be a lowercase stable identifier")
	}
	if value.SessionID != "" && !stableID.MatchString(value.SessionID) {
		problems = append(problems, "session_id must be a lowercase stable identifier when set")
	}
	if !stableID.MatchString(value.TargetID) || value.TargetRevision < 1 {
		problems = append(problems, "target must identify a versioned memory record")
	}
	if isV1 {
		if value.TargetKind != TargetProcedure ||
			(value.Tool != "threadforge_procedure_get" && value.Tool != "threadforge_procedure_guide" && value.Tool != "threadforge_context_get") {
			problems = append(problems, "v1 receipts are limited to procedure_get, procedure_guide, or procedure entries from context_get")
		}
		if value.ResultStatus != "" {
			problems = append(problems, "v1 receipts do not include result_status")
		}
		if value.Freshness != "verified" && value.Freshness != "unverified" && value.Freshness != "stale" {
			problems = append(problems, "v1 freshness must be verified, unverified, or stale")
		}
		if value.GuidanceIncluded == (value.Freshness == "stale") {
			problems = append(problems, "v1 guidance_included must be false only for stale procedures")
		}
	}
	if isV2 {
		if value.TargetKind != TargetFact && value.TargetKind != TargetDecision && value.TargetKind != TargetPolicy {
			problems = append(problems, "v2 receipts must identify a fact, decision, or policy")
		}
		if value.Tool != "threadforge_project_memory_read" && value.Tool != "threadforge_project_memory_read_v2" && value.Tool != "threadforge_context_get" {
			problems = append(problems, "v2 receipts are limited to project-memory reads or project-memory entries from context_get")
		}
		if value.Freshness != "verified" && value.Freshness != "stale" && value.Freshness != "incomplete" &&
			value.Freshness != "unverified" && value.Freshness != "not_tracked" {
			problems = append(problems, "v2 freshness must be verified, stale, incomplete, unverified, or not_tracked")
		}
		switch value.ResultStatus {
		case "complete", "incomplete", "stale", "conflicting", "privacy_blocked":
		default:
			problems = append(problems, "v2 result_status must be complete, incomplete, stale, conflicting, or privacy_blocked")
		}
		if value.ResultStatus == "complete" && !value.GuidanceIncluded {
			problems = append(problems, "complete project-memory results must include guidance")
		}
		if value.ResultStatus == "stale" && (value.Freshness != "stale" || value.GuidanceIncluded) {
			problems = append(problems, "stale results must withhold guidance and report stale freshness")
		}
		if (value.ResultStatus == "conflicting" || value.ResultStatus == "privacy_blocked") && value.GuidanceIncluded {
			problems = append(problems, "conflicting or privacy-blocked results must withhold guidance")
		}
		if value.Freshness == "stale" && value.ResultStatus != "stale" {
			problems = append(problems, "stale freshness requires stale result_status")
		}
		if value.Freshness == "incomplete" && value.GuidanceIncluded {
			problems = append(problems, "incomplete freshness must withhold guidance")
		}
	}
	if isV3 {
		if value.TargetKind != TargetProcedure && value.TargetKind != TargetFact && value.TargetKind != TargetDecision && value.TargetKind != TargetPolicy {
			problems = append(problems, "v3 receipts must identify a procedure, fact, decision, or policy")
		}
		if value.Tool != "threadforge_memory_inject" {
			problems = append(problems, "v3 receipts must use threadforge_memory_inject")
		}
		switch value.Host {
		case "claude-code", "codex-cli", "gemini-cli", "cursor", "qwen-code", "antigravity", "opencode", "copilot-cli":
		default:
			problems = append(problems, "v3 host must identify a supported prompt-memory host")
		}
		if value.ResultStatus != "" {
			problems = append(problems, "v3 hook receipts do not include result_status")
		}
		if value.Freshness != "verified" && value.Freshness != "unverified" && value.Freshness != "stale" &&
			value.Freshness != "incomplete" && value.Freshness != "not_tracked" {
			problems = append(problems, "v3 freshness must be verified, unverified, stale, incomplete, or not_tracked")
		}
		if value.Freshness == "stale" && value.GuidanceIncluded {
			problems = append(problems, "stale hook guidance must not be marked included")
		}
	}
	if isV4 {
		if value.TargetKind != TargetTaskContext || value.TargetID != "task-resume-context" || value.TargetRevision != 1 {
			problems = append(problems, "v4 receipts must identify task-resume-context contract revision 1")
		}
		if value.TaskRevision < 1 {
			problems = append(problems, "v4 receipts must bind a positive task_revision")
		}
		if value.Tool != "threadforge_task_resume_context_with_receipt" {
			problems = append(problems, "v4 receipts must use threadforge_task_resume_context_with_receipt")
		}
		if value.Freshness != "snapshot" {
			problems = append(problems, "v4 freshness must be snapshot")
		}
		switch value.ResultStatus {
		case "available":
			if !value.GuidanceIncluded {
				problems = append(problems, "available task context must be marked included")
			}
		case "session_selection_required":
			if value.GuidanceIncluded {
				problems = append(problems, "ambiguous task context must be marked withheld")
			}
		default:
			problems = append(problems, "v4 result_status must be available or session_selection_required")
		}
	}
	if !isV4 && value.TaskRevision != 0 {
		problems = append(problems, "task_revision is only supported by v4 task-context receipts")
	}
	if !isV3 && value.Host != "" {
		problems = append(problems, "non-hook receipts do not include host")
	}
	if isV3 && value.ObservationBoundary != "hook_response_prepared" {
		problems = append(problems, "v3 observation_boundary must be hook_response_prepared")
	} else if !isV3 && value.ObservationBoundary != "mcp_handler_result_prepared" {
		problems = append(problems, "observation_boundary must be mcp_handler_result_prepared")
	}
	if _, err := time.Parse(time.RFC3339Nano, value.CreatedAt); err != nil {
		problems = append(problems, "created_at must use RFC3339")
	}
	for _, field := range []string{value.ID, value.ProjectID, value.TaskID, value.SessionID, string(value.TargetKind), value.TargetID, value.Tool, value.Host, value.Freshness, value.ObservationBoundary} {
		if privacy.ContainsCredentialText(field) {
			problems = append(problems, "retrieval receipt contains credential material")
			break
		}
	}
	if len(problems) != 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func ValidateFilter(filter Filter) error {
	if !bounded(filter.ProjectID, 256) {
		return errors.New("project_id must contain 1 to 256 characters")
	}
	if filter.TaskID != "" && !stableID.MatchString(filter.TaskID) {
		return errors.New("task_id must be a lowercase stable identifier when set")
	}
	if filter.TargetKind != "" && filter.TargetKind != TargetProcedure && filter.TargetKind != TargetFact &&
		filter.TargetKind != TargetDecision && filter.TargetKind != TargetPolicy && filter.TargetKind != TargetTaskContext {
		return errors.New("target_kind must be procedure, fact, decision, policy, task_context, or empty")
	}
	if filter.TargetID != "" && !stableID.MatchString(filter.TargetID) {
		return errors.New("target_id must be a lowercase stable identifier when set")
	}
	if filter.Harness != "" && !stableID.MatchString(filter.Harness) {
		return errors.New("harness must be a lowercase stable identifier when set")
	}
	if filter.Limit < 1 || filter.Limit > 500 || filter.Offset < 0 || filter.Offset > MaxProjectRecords {
		return fmt.Errorf("limit must be 1-500 and offset must be 0-%d", MaxProjectRecords)
	}
	for _, field := range []string{filter.ProjectID, filter.TaskID, string(filter.TargetKind), filter.TargetID, filter.Harness} {
		if privacy.ContainsCredentialText(field) {
			return errors.New("feedback filter contains credential material")
		}
	}
	return nil
}

func ValidateRetrievalReceiptFilter(filter RetrievalReceiptFilter) error {
	if !bounded(filter.ProjectID, 256) {
		return errors.New("project_id must contain 1 to 256 characters")
	}
	if !stableID.MatchString(filter.TaskID) {
		return errors.New("task_id must be a lowercase stable identifier")
	}
	if filter.TargetID != "" && !stableID.MatchString(filter.TargetID) {
		return errors.New("target_id must be a lowercase stable identifier when set")
	}
	if filter.Limit < 1 || filter.Limit > 500 || filter.Offset < 0 || filter.Offset > MaxProjectRecords {
		return fmt.Errorf("limit must be 1-500 and offset must be 0-%d", MaxProjectRecords)
	}
	for _, field := range []string{filter.ProjectID, filter.TaskID, filter.TargetID} {
		if privacy.ContainsCredentialText(field) {
			return errors.New("memory retrieval receipt filter contains credential material")
		}
	}
	return nil
}

func bounded(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && len([]rune(value)) <= maximum
}
