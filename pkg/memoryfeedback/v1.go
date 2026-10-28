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
	SchemaVersion        = "threadforge.memory-feedback.v1"
	RecordResultVersion  = "threadforge.memory-feedback-result.v1"
	ListResultVersion    = "threadforge.memory-feedback-list.v1"
	SummaryResultVersion = "threadforge.memory-feedback-summary.v1"
	EvidenceSelfReported = "self_reported"
	MaxProjectRecords    = 100000
	MaxSummaryRecords    = 10000
)

type TargetKind string

const (
	TargetProcedure TargetKind = "procedure"
	TargetFact      TargetKind = "fact"
	TargetDecision  TargetKind = "decision"
	TargetPolicy    TargetKind = "policy"
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
	TargetKind              TargetKind `json:"target_kind"`
	TargetID                string     `json:"target_id"`
	TargetRevision          int        `json:"target_revision"`
	Exposure                Exposure   `json:"exposure"`
	Utility                 Utility    `json:"utility"`
	TaskResult              string     `json:"task_result"`
	Harness                 string     `json:"harness"`
	Actor                   string     `json:"actor"`
	EvidenceClass           string     `json:"evidence_class"`
	CutoverEvidenceEligible bool       `json:"cutover_evidence_eligible"`
	ExecutionAuthorized     bool       `json:"execution_authorized"`
	CreatedAt               string     `json:"created_at"`
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
	TargetKind     TargetKind `json:"target_kind"`
	TargetID       string     `json:"target_id"`
	TargetRevision int        `json:"target_revision"`
	Harness        string     `json:"harness"`
	Exposure       Exposure   `json:"exposure"`
	Utility        Utility    `json:"utility"`
	TaskResult     string     `json:"task_result"`
	Count          int        `json:"count"`
}

type SummaryResult struct {
	SchemaVersion           string         `json:"schema_version"`
	Status                  string         `json:"status"`
	ProjectID               string         `json:"project_id"`
	Records                 int            `json:"records"`
	Groups                  []SummaryGroup `json:"groups"`
	EvidenceClass           string         `json:"evidence_class"`
	CutoverEvidenceEligible bool           `json:"cutover_evidence_eligible"`
	ExecutionAuthorized     bool           `json:"execution_authorized"`
}

var stableID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func Validate(value Feedback) error {
	var problems []string
	if value.SchemaVersion != SchemaVersion {
		problems = append(problems, "unsupported schema_version")
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
	switch value.TargetKind {
	case TargetProcedure, TargetFact, TargetDecision, TargetPolicy:
	default:
		problems = append(problems, "target_kind must be procedure, fact, decision, or policy")
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
	for _, field := range []string{value.ID, value.ProjectID, value.TaskID, value.SessionID, string(value.TargetKind), value.TargetID, string(value.Exposure), string(value.Utility), value.TaskResult, value.Harness, value.Actor} {
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

func ValidateFilter(filter Filter) error {
	if !bounded(filter.ProjectID, 256) {
		return errors.New("project_id must contain 1 to 256 characters")
	}
	if filter.TaskID != "" && !stableID.MatchString(filter.TaskID) {
		return errors.New("task_id must be a lowercase stable identifier when set")
	}
	if filter.TargetKind != "" && filter.TargetKind != TargetProcedure && filter.TargetKind != TargetFact &&
		filter.TargetKind != TargetDecision && filter.TargetKind != TargetPolicy {
		return errors.New("target_kind must be procedure, fact, decision, policy, or empty")
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

func bounded(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && len([]rune(value)) <= maximum
}
