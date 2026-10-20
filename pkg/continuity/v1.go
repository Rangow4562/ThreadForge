package continuity

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const SchemaVersion = "threadforge.continuity.v1"

type TaskStatus string

const (
	TaskOpen       TaskStatus = "open"
	TaskInProgress TaskStatus = "in_progress"
	TaskBlocked    TaskStatus = "blocked"
	TaskCompleted  TaskStatus = "completed"
	TaskAbandoned  TaskStatus = "abandoned"
)

type SessionStatus string

const (
	SessionActive SessionStatus = "active"
	SessionClosed SessionStatus = "closed"
)

type EntryKind string

const (
	EntryCheckpoint   EntryKind = "checkpoint"
	EntryHandoff      EntryKind = "handoff"
	EntryReview       EntryKind = "review"
	EntryVerification EntryKind = "verification"
	EntryTaskClaim    EntryKind = "task_claim"
	EntryTaskHandoff  EntryKind = "task_handoff"
)

const TaskClaimSchemaVersion = "threadforge.task-claim.v1"
const TaskClaimEventSchemaVersion = "threadforge.task-claim-event.v1"
const TaskClaimReadSchemaVersion = "threadforge.task-claim-read.v1"
const TaskHandoffSchemaVersion = "threadforge.task-handoff.v1"
const TaskHandoffEventSchemaVersion = "threadforge.task-handoff-event.v1"
const TaskHandoffReadSchemaVersion = "threadforge.task-handoff-read.v1"

type TaskHandoffStatus string

const (
	TaskHandoffDraft     TaskHandoffStatus = "draft"
	TaskHandoffOffered   TaskHandoffStatus = "offered"
	TaskHandoffAccepted  TaskHandoffStatus = "accepted"
	TaskHandoffRejected  TaskHandoffStatus = "rejected"
	TaskHandoffCancelled TaskHandoffStatus = "cancelled"
)

type TaskHandoff struct {
	SchemaVersion            string            `json:"schema_version"`
	ID                       string            `json:"id"`
	ProjectID                string            `json:"project_id"`
	TaskID                   string            `json:"task_id"`
	FromOwner                string            `json:"from_owner"`
	ToOwner                  string            `json:"to_owner"`
	TaskRevision             int               `json:"task_revision"`
	ClaimID                  string            `json:"claim_id"`
	ClaimRevision            int               `json:"claim_revision"`
	BaselineSHA256           string            `json:"baseline_sha256"`
	CheckpointEntryID        string            `json:"checkpoint_entry_id"`
	CheckpointSummary        string            `json:"checkpoint_summary"`
	CheckpointDetails        string            `json:"checkpoint_details,omitempty"`
	Status                   TaskHandoffStatus `json:"status"`
	Revision                 int               `json:"revision"`
	CreatedAt                string            `json:"created_at"`
	UpdatedAt                string            `json:"updated_at"`
	TransferredTaskRevision  int               `json:"transferred_task_revision,omitempty"`
	TransferredClaimRevision int               `json:"transferred_claim_revision,omitempty"`
}

type TaskHandoffEvent struct {
	SchemaVersion    string      `json:"schema_version"`
	Action           string      `json:"action"`
	ExpectedRevision int         `json:"expected_revision"`
	Handoff          TaskHandoff `json:"handoff"`
}

type TaskHandoffRead struct {
	SchemaVersion       string        `json:"schema_version"`
	TaskID              string        `json:"task_id"`
	Handoffs            []TaskHandoff `json:"handoffs"`
	ExecutionAuthorized bool          `json:"execution_authorized"`
}

type TaskClaimScopeKind string

const (
	TaskClaimScopePath   TaskClaimScopeKind = "path"
	TaskClaimScopeModule TaskClaimScopeKind = "module"
	TaskClaimScopeAPI    TaskClaimScopeKind = "api"
	TaskClaimScopeSchema TaskClaimScopeKind = "schema"
)

type TaskClaimScope struct {
	Kind  TaskClaimScopeKind `json:"kind"`
	Value string             `json:"value"`
}

type TaskClaim struct {
	SchemaVersion  string           `json:"schema_version"`
	ID             string           `json:"id"`
	ProjectID      string           `json:"project_id"`
	TaskID         string           `json:"task_id"`
	Owner          string           `json:"owner"`
	Scopes         []TaskClaimScope `json:"scopes"`
	TaskRevision   int              `json:"task_revision"`
	BaselineSHA256 string           `json:"baseline_sha256"`
	LeaseExpiresAt string           `json:"lease_expires_at"`
	Status         string           `json:"status"`
	Revision       int              `json:"revision"`
	CreatedAt      string           `json:"created_at"`
	UpdatedAt      string           `json:"updated_at"`
	ReleasedAt     string           `json:"released_at,omitempty"`
}

type TaskClaimEvent struct {
	SchemaVersion    string    `json:"schema_version"`
	Action           string    `json:"action"`
	ExpectedRevision int       `json:"expected_revision"`
	Claim            TaskClaim `json:"claim"`
}

type TaskClaimAssessment struct {
	Claim               TaskClaim `json:"claim"`
	LeaseStatus         string    `json:"lease_status"`
	TaskRevisionStatus  string    `json:"task_revision_status"`
	BaselineStatus      string    `json:"baseline_status"`
	ExecutionAuthorized bool      `json:"execution_authorized"`
}

type TaskClaimRead struct {
	SchemaVersion       string                `json:"schema_version"`
	TaskID              string                `json:"task_id"`
	Claims              []TaskClaimAssessment `json:"claims"`
	ExecutionAuthorized bool                  `json:"execution_authorized"`
}

func AssessTaskClaim(claim TaskClaim, currentTaskRevision int, currentBaselineSHA256 string, now time.Time) TaskClaimAssessment {
	assessment := TaskClaimAssessment{
		Claim: claim, LeaseStatus: "active", TaskRevisionStatus: "current",
		BaselineStatus: "unverified", ExecutionAuthorized: false,
	}
	if claim.Status == "released" {
		assessment.LeaseStatus = "released"
	} else if expiresAt, err := time.Parse(time.RFC3339Nano, claim.LeaseExpiresAt); err != nil {
		assessment.LeaseStatus = "invalid"
	} else if !now.Before(expiresAt) {
		assessment.LeaseStatus = "expired"
	}
	if currentTaskRevision != claim.TaskRevision {
		assessment.TaskRevisionStatus = "stale"
	}
	if currentBaselineSHA256 != "" {
		assessment.BaselineStatus = "match"
		if currentBaselineSHA256 != claim.BaselineSHA256 {
			assessment.BaselineStatus = "mismatch"
		}
	}
	return assessment
}

type PlanDecision string

const (
	PlanUnconfirmed       PlanDecision = "unconfirmed"
	PlanOnly              PlanDecision = "plan_only"
	PlanGovernedExecution PlanDecision = "governed_execution"
)

const PlanSchemaVersion = "threadforge.task-plan.v1"
const RequirementsSchemaVersion = "threadforge.task-requirements.v1"
const EntryAppendAuthorizationReason = "continuity entry append"

type RequirementsStatus string

const (
	RequirementsDraft      RequirementsStatus = "draft"
	RequirementsClarifying RequirementsStatus = "clarifying"
	RequirementsFinalized  RequirementsStatus = "finalized"
)

type RequirementQuestion struct {
	ID         string `json:"id"`
	Question   string `json:"question"`
	AskedBy    string `json:"asked_by"`
	AskedAt    string `json:"asked_at"`
	Answer     string `json:"answer,omitempty"`
	AnsweredBy string `json:"answered_by,omitempty"`
	AnsweredAt string `json:"answered_at,omitempty"`
}

type TaskRequirements struct {
	SchemaVersion  string                `json:"schema_version"`
	Status         RequirementsStatus    `json:"status"`
	Questions      []RequirementQuestion `json:"questions"`
	FinalizedBy    string                `json:"finalized_by,omitempty"`
	FinalizedAt    string                `json:"finalized_at,omitempty"`
	FinalizeReason string                `json:"finalize_reason,omitempty"`
}

// ImplementationScope is a declared planning boundary, not a filesystem
// sandbox or a grant of tool permissions. Exclusions always take precedence.
type ImplementationScope struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude,omitempty"`
	Modules []string `json:"modules,omitempty"`
}

// TaskPlan is an explicitly authored, revisioned plan attached to a task.
// Confirmed decisions are durable records, never execution authorization.
type TaskPlan struct {
	SchemaVersion string               `json:"schema_version"`
	Version       int                  `json:"version"`
	Markdown      string               `json:"markdown"`
	Scope         *ImplementationScope `json:"implementation_scope,omitempty"`
	Decision      PlanDecision         `json:"decision"`
	ConfirmedBy   string               `json:"confirmed_by,omitempty"`
	ConfirmedAt   string               `json:"confirmed_at,omitempty"`
	ConfirmReason string               `json:"confirmation_reason,omitempty"`
}

type Task struct {
	SchemaVersion      string            `json:"schema_version"`
	ID                 string            `json:"id"`
	ProjectID          string            `json:"project_id"`
	Owner              string            `json:"owner,omitempty"`
	Title              string            `json:"title"`
	Objective          string            `json:"objective"`
	AcceptanceCriteria []string          `json:"acceptance_criteria"`
	Requirements       *TaskRequirements `json:"requirements,omitempty"`
	Plan               *TaskPlan         `json:"plan,omitempty"`
	Status             TaskStatus        `json:"status"`
	Summary            string            `json:"summary,omitempty"`
	NextAction         string            `json:"next_action,omitempty"`
	Revision           int               `json:"revision"`
	CreatedAt          string            `json:"created_at"`
	UpdatedAt          string            `json:"updated_at"`
}

type Session struct {
	SchemaVersion string        `json:"schema_version"`
	ID            string        `json:"id"`
	ProjectID     string        `json:"project_id"`
	TaskID        string        `json:"task_id"`
	Harness       string        `json:"harness"`
	Status        SessionStatus `json:"status"`
	Summary       string        `json:"summary,omitempty"`
	Completed     []string      `json:"completed,omitempty"`
	NextAction    string        `json:"next_action,omitempty"`
	Revision      int           `json:"revision"`
	StartedAt     string        `json:"started_at"`
	UpdatedAt     string        `json:"updated_at"`
	EndedAt       string        `json:"ended_at,omitempty"`
}

type Entry struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	ProjectID     string            `json:"project_id"`
	TaskID        string            `json:"task_id"`
	SessionID     string            `json:"session_id,omitempty"`
	Kind          EntryKind         `json:"kind"`
	Actor         string            `json:"actor"`
	Summary       string            `json:"summary"`
	Details       string            `json:"details,omitempty"`
	Result        string            `json:"result,omitempty"`
	Evidence      []string          `json:"evidence_sha256,omitempty"`
	Authorization json.RawMessage   `json:"authorization,omitempty"`
	TaskClaim     *TaskClaimEvent   `json:"task_claim,omitempty"`
	TaskHandoff   *TaskHandoffEvent `json:"task_handoff,omitempty"`
	CreatedAt     string            `json:"created_at"`
}

type AuditEvent struct {
	Sequence      int64           `json:"sequence"`
	Entity        string          `json:"entity"`
	EntityID      string          `json:"entity_id"`
	Revision      int             `json:"revision"`
	Action        string          `json:"action"`
	Actor         string          `json:"actor"`
	Reason        string          `json:"reason"`
	CreatedAt     string          `json:"created_at"`
	Authorization json.RawMessage `json:"authorization,omitempty"`
}

var stableID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidateTask(task Task) error {
	var problems []string
	if task.SchemaVersion != SchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	if !stableID.MatchString(task.ID) {
		problems = append(problems, "id must be a lowercase stable identifier")
	}
	if !boundedText(task.ProjectID, 256) {
		problems = append(problems, "project_id must contain 1 to 256 characters")
	}
	if task.Owner != "" && !boundedText(task.Owner, 128) {
		problems = append(problems, "owner must contain 1 to 128 characters when set")
	}
	if !boundedText(task.Title, 240) {
		problems = append(problems, "title must contain 1 to 240 characters")
	}
	if !boundedText(task.Objective, 8000) {
		problems = append(problems, "objective must contain 1 to 8000 characters")
	}
	if len(task.AcceptanceCriteria) == 0 || len(task.AcceptanceCriteria) > 64 {
		problems = append(problems, "acceptance_criteria must contain 1 to 64 items")
	}
	for i, criterion := range task.AcceptanceCriteria {
		if !boundedText(criterion, 1000) {
			problems = append(problems, fmt.Sprintf("acceptance_criteria[%d] must contain 1 to 1000 characters", i))
		}
	}
	switch task.Status {
	case TaskOpen, TaskInProgress, TaskBlocked, TaskCompleted, TaskAbandoned:
	default:
		problems = append(problems, "status must be open, in_progress, blocked, completed, or abandoned")
	}
	if task.Summary != "" && !boundedText(task.Summary, 8000) {
		problems = append(problems, "summary exceeds 8000 characters")
	}
	if task.NextAction != "" && !boundedText(task.NextAction, 2000) {
		problems = append(problems, "next_action exceeds 2000 characters")
	}
	if task.Plan != nil {
		if err := ValidateTaskPlan(*task.Plan); err != nil {
			problems = append(problems, "plan: "+err.Error())
		}
	}
	if task.Requirements != nil {
		if err := ValidateTaskRequirements(*task.Requirements); err != nil {
			problems = append(problems, "requirements: "+err.Error())
		}
	}
	if task.Revision < 1 {
		problems = append(problems, "revision must be at least 1")
	}
	if !validTimestamp(task.CreatedAt) || !validTimestamp(task.UpdatedAt) {
		problems = append(problems, "created_at and updated_at must use RFC3339")
	}
	if task.CreatedAt != "" && task.UpdatedAt != "" {
		created, _ := time.Parse(time.RFC3339Nano, task.CreatedAt)
		updated, _ := time.Parse(time.RFC3339Nano, task.UpdatedAt)
		if updated.Before(created) {
			problems = append(problems, "updated_at cannot precede created_at")
		}
	}
	if hasCredential(task.ID, task.ProjectID, task.Owner, task.Title, task.Objective, task.Summary, task.NextAction) {
		problems = append(problems, "task contains credential material")
	}
	for _, criterion := range task.AcceptanceCriteria {
		if privacy.ContainsCredentialText(criterion) {
			problems = append(problems, "acceptance criteria contain credential material")
			break
		}
	}
	return joinProblems(problems)
}

func ValidateTaskRequirements(requirements TaskRequirements) error {
	var problems []string
	if requirements.SchemaVersion != RequirementsSchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	if len(requirements.Questions) > 64 {
		problems = append(problems, "questions must contain no more than 64 items")
	}
	seen := make(map[string]bool, len(requirements.Questions))
	openQuestions := 0
	for index, question := range requirements.Questions {
		prefix := fmt.Sprintf("questions[%d]", index)
		if !stableID.MatchString(question.ID) || seen[question.ID] {
			problems = append(problems, prefix+".id must be unique and use a lowercase stable identifier")
		}
		seen[question.ID] = true
		if !boundedText(question.Question, 2000) || !boundedText(question.AskedBy, 128) || !validTimestamp(question.AskedAt) {
			problems = append(problems, prefix+" must contain a bounded question, asker, and RFC3339 asked_at")
		}
		if question.Answer == "" {
			openQuestions++
			if question.AnsweredBy != "" || question.AnsweredAt != "" {
				problems = append(problems, prefix+" unanswered state cannot contain answer metadata")
			}
		} else if !boundedText(question.Answer, 4000) || !boundedText(question.AnsweredBy, 128) || !validTimestamp(question.AnsweredAt) {
			problems = append(problems, prefix+" answered state requires a bounded answer, responder, and RFC3339 answered_at")
		} else {
			askedAt, _ := time.Parse(time.RFC3339Nano, question.AskedAt)
			answeredAt, _ := time.Parse(time.RFC3339Nano, question.AnsweredAt)
			if answeredAt.Before(askedAt) {
				problems = append(problems, prefix+".answered_at cannot precede asked_at")
			}
		}
		if hasCredential(question.ID, question.Question, question.AskedBy, question.Answer, question.AnsweredBy) {
			problems = append(problems, prefix+" contains credential material")
		}
	}
	switch requirements.Status {
	case RequirementsDraft:
		if openQuestions != 0 {
			problems = append(problems, "draft requirements cannot contain unanswered questions")
		}
	case RequirementsClarifying:
		if openQuestions == 0 {
			problems = append(problems, "clarifying requirements must contain an unanswered question")
		}
	case RequirementsFinalized:
		if openQuestions != 0 {
			problems = append(problems, "requirements cannot be finalized while questions remain unanswered")
		}
		if !boundedText(requirements.FinalizedBy, 128) || !boundedText(requirements.FinalizeReason, 2000) || !validTimestamp(requirements.FinalizedAt) {
			problems = append(problems, "finalized requirements require a reviewer, reason, and RFC3339 timestamp")
		}
	default:
		problems = append(problems, "status must be draft, clarifying, or finalized")
	}
	if requirements.Status != RequirementsFinalized && (requirements.FinalizedBy != "" || requirements.FinalizedAt != "" || requirements.FinalizeReason != "") {
		problems = append(problems, "non-finalized requirements cannot contain finalization metadata")
	}
	if hasCredential(requirements.FinalizedBy, requirements.FinalizeReason) {
		problems = append(problems, "requirements finalization contains credential material")
	}
	return joinProblems(problems)
}

func ValidateTaskPlan(plan TaskPlan) error {
	var problems []string
	if plan.SchemaVersion != PlanSchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	if plan.Version < 1 {
		problems = append(problems, "version must be at least 1")
	}
	if !utf8.ValidString(plan.Markdown) || !boundedText(plan.Markdown, 65536) {
		problems = append(problems, "markdown must contain 1 to 65536 valid UTF-8 bytes")
	}
	switch plan.Decision {
	case PlanUnconfirmed, PlanOnly, PlanGovernedExecution:
	default:
		problems = append(problems, "decision must be unconfirmed, plan_only, or governed_execution")
	}
	if plan.Scope != nil {
		if err := ValidateImplementationScope(*plan.Scope); err != nil {
			problems = append(problems, "implementation_scope: "+err.Error())
		}
	}
	if plan.Decision == PlanGovernedExecution && (plan.Scope == nil || len(plan.Scope.Include) == 0) {
		problems = append(problems, "governed_execution requires a non-empty implementation_scope.include")
	}
	if plan.Decision == PlanUnconfirmed {
		if plan.ConfirmedBy != "" || plan.ConfirmedAt != "" || plan.ConfirmReason != "" {
			problems = append(problems, "unconfirmed plans cannot contain confirmation metadata")
		}
	} else {
		if !boundedText(plan.ConfirmedBy, 128) || !boundedText(plan.ConfirmReason, 2000) || !validTimestamp(plan.ConfirmedAt) {
			problems = append(problems, "confirmed plans require a bounded confirmer, RFC3339 timestamp, and reason")
		}
	}
	if privacy.ContainsCredentialText(plan.Markdown) || privacy.ContainsCredentialText(plan.ConfirmedBy) || privacy.ContainsCredentialText(plan.ConfirmReason) {
		problems = append(problems, "plan contains credential material")
	}
	return joinProblems(problems)
}

func ValidateImplementationScope(scope ImplementationScope) error {
	var problems []string
	if len(scope.Include) == 0 || len(scope.Include) > 128 {
		problems = append(problems, "include must contain 1 to 128 repository-relative patterns")
	}
	if len(scope.Exclude) > 128 || len(scope.Modules) > 128 {
		problems = append(problems, "exclude and modules may contain no more than 128 items each")
	}
	for _, field := range []struct {
		label  string
		values []string
	}{{"include", scope.Include}, {"exclude", scope.Exclude}, {"modules", scope.Modules}} {
		for index, value := range field.values {
			if !validScopeItem(value, field.label == "modules") {
				problems = append(problems, fmt.Sprintf("%s[%d] must be a bounded, relative, credential-free path pattern or module name", field.label, index))
			}
		}
	}
	return joinProblems(problems)
}

// PathInImplementationScope checks one normalized repository-relative path
// against a declared plan scope. Exclusions take precedence. The result is a
// planning-boundary check, not a filesystem sandbox or tool authorization.
func PathInImplementationScope(scope ImplementationScope, repositoryPath string) bool {
	if ValidateImplementationScope(scope) != nil || repositoryPath == "" ||
		strings.Contains(repositoryPath, "\\") || strings.Contains(repositoryPath, ":") ||
		path.IsAbs(repositoryPath) || path.Clean(repositoryPath) != repositoryPath ||
		repositoryPath == "." || repositoryPath == ".." || strings.HasPrefix(repositoryPath, "../") ||
		len(repositoryPath) > 4096 {
		return false
	}
	if len(strings.Split(repositoryPath, "/")) > 256 {
		return false
	}
	included := false
	for _, pattern := range scope.Include {
		if scopeGlobMatch(pattern, repositoryPath) {
			included = true
			break
		}
	}
	if !included {
		return false
	}
	for _, pattern := range scope.Exclude {
		if scopeGlobMatch(pattern, repositoryPath) {
			return false
		}
	}
	return true
}

func scopeGlobMatch(pattern, candidate string) bool {
	patternParts := strings.Split(pattern, "/")
	candidateParts := strings.Split(candidate, "/")
	memo := make([][]int8, len(patternParts)+1)
	for index := range memo {
		memo[index] = make([]int8, len(candidateParts)+1)
		for candidateIndex := range memo[index] {
			memo[index][candidateIndex] = -1
		}
	}
	var match func(int, int) bool
	match = func(patternIndex, candidateIndex int) bool {
		if memo[patternIndex][candidateIndex] >= 0 {
			return memo[patternIndex][candidateIndex] == 1
		}
		result := false
		if patternIndex == len(patternParts) {
			result = candidateIndex == len(candidateParts)
		} else if patternParts[patternIndex] == "**" {
			for index := candidateIndex; index <= len(candidateParts); index++ {
				if match(patternIndex+1, index) {
					result = true
					break
				}
			}
		} else if candidateIndex < len(candidateParts) {
			matches, err := path.Match(patternParts[patternIndex], candidateParts[candidateIndex])
			result = err == nil && matches && match(patternIndex+1, candidateIndex+1)
		}
		if result {
			memo[patternIndex][candidateIndex] = 1
		} else {
			memo[patternIndex][candidateIndex] = 0
		}
		return result
	}
	return match(0, 0)
}

func validScopeItem(value string, module bool) bool {
	if !boundedText(value, 512) || strings.Contains(value, `\`) || strings.Contains(value, ":") || strings.HasPrefix(value, "/") {
		return false
	}
	if privacy.ContainsCredentialText(value) {
		return false
	}
	if module {
		return !strings.Contains(value, "..")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || segment == "~" {
			return false
		}
		lower := strings.ToLower(segment)
		if lower == ".env" || strings.HasPrefix(lower, ".env.") || strings.Contains(lower, "credential") ||
			strings.Contains(lower, "secret") || strings.Contains(lower, "private-key") || strings.Contains(lower, "private_key") ||
			lower == "id_rsa" || lower == "id_ed25519" || strings.Contains(lower, "auth-token") ||
			strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx") ||
			strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".keystore") {
			return false
		}
	}
	_, err := path.Match(value, "scope-validation-probe")
	if err != nil {
		return false
	}
	return true
}

func ValidateSession(session Session) error {
	var problems []string
	if session.SchemaVersion != SchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	for name, value := range map[string]string{"id": session.ID, "task_id": session.TaskID} {
		if !stableID.MatchString(value) {
			problems = append(problems, name+" must be a lowercase stable identifier")
		}
	}
	if !boundedText(session.ProjectID, 256) || !boundedText(session.Harness, 128) {
		problems = append(problems, "project_id and harness are required and bounded")
	}
	if session.Status != SessionActive && session.Status != SessionClosed {
		problems = append(problems, "status must be active or closed")
	}
	if session.Revision < 1 {
		problems = append(problems, "revision must be at least 1")
	}
	if !validTimestamp(session.StartedAt) || !validTimestamp(session.UpdatedAt) {
		problems = append(problems, "started_at and updated_at must use RFC3339")
	}
	if session.Status == SessionClosed && !validTimestamp(session.EndedAt) {
		problems = append(problems, "closed sessions require ended_at")
	}
	if session.Status == SessionActive && session.EndedAt != "" {
		problems = append(problems, "active sessions cannot have ended_at")
	}
	if len(session.Completed) > 64 {
		problems = append(problems, "completed must contain no more than 64 items")
	}
	for _, completed := range session.Completed {
		if !boundedText(completed, 1000) {
			problems = append(problems, "completed items must contain 1 to 1000 characters")
			break
		}
	}
	for _, value := range []string{session.ID, session.ProjectID, session.TaskID, session.Harness, session.Summary, session.NextAction} {
		if privacy.ContainsCredentialText(value) {
			problems = append(problems, "session contains credential material")
			break
		}
	}
	for _, completed := range session.Completed {
		if privacy.ContainsCredentialText(completed) {
			problems = append(problems, "session completion notes contain credential material")
			break
		}
	}
	return joinProblems(problems)
}

func ValidateEntry(entry Entry) error {
	var problems []string
	if entry.SchemaVersion != SchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	for name, value := range map[string]string{"id": entry.ID, "task_id": entry.TaskID} {
		if !stableID.MatchString(value) {
			problems = append(problems, name+" must be a lowercase stable identifier")
		}
	}
	if !boundedText(entry.ProjectID, 256) || !boundedText(entry.Actor, 128) || !boundedText(entry.Summary, 4000) {
		problems = append(problems, "project_id, actor, and summary are required and bounded")
	}
	if entry.SessionID != "" && !stableID.MatchString(entry.SessionID) {
		problems = append(problems, "session_id must be a lowercase stable identifier")
	}
	switch entry.Kind {
	case EntryCheckpoint, EntryHandoff, EntryReview, EntryVerification:
		if entry.TaskClaim != nil || entry.TaskHandoff != nil {
			problems = append(problems, "typed task events are only valid for their matching entry kinds")
		}
	case EntryTaskClaim:
		if entry.TaskHandoff != nil {
			problems = append(problems, "task_handoff is only valid for task_handoff entries")
		} else if entry.TaskClaim == nil {
			problems = append(problems, "task_claim entries require a task_claim event")
		} else if err := ValidateTaskClaimEvent(*entry.TaskClaim); err != nil {
			problems = append(problems, "task_claim event is invalid: "+err.Error())
		} else if entry.TaskClaim.Claim.ProjectID != entry.ProjectID || entry.TaskClaim.Claim.TaskID != entry.TaskID ||
			entry.TaskClaim.Claim.Owner != entry.Actor || entry.TaskClaim.Claim.UpdatedAt != entry.CreatedAt || entry.SessionID != "" {
			problems = append(problems, "task_claim event scope, owner, or session binding is invalid")
		}
	case EntryTaskHandoff:
		if entry.TaskClaim != nil {
			problems = append(problems, "task_claim is only valid for task_claim entries")
		} else if entry.TaskHandoff == nil {
			problems = append(problems, "task_handoff entries require a task_handoff event")
		} else if err := ValidateTaskHandoffEvent(*entry.TaskHandoff); err != nil {
			problems = append(problems, "task_handoff event is invalid: "+err.Error())
		} else if entry.TaskHandoff.Handoff.ProjectID != entry.ProjectID || entry.TaskHandoff.Handoff.TaskID != entry.TaskID ||
			entry.TaskHandoff.Handoff.UpdatedAt != entry.CreatedAt || entry.SessionID != "" ||
			!taskHandoffActorMatches(entry.TaskHandoff.Action, entry.Actor, entry.TaskHandoff.Handoff) {
			problems = append(problems, "task_handoff event scope, actor, or session binding is invalid")
		}
	default:
		problems = append(problems, "kind must be checkpoint, handoff, review, verification, task_claim, or task_handoff")
	}
	if entry.Details != "" && !boundedText(entry.Details, 8000) {
		problems = append(problems, "details exceeds 8000 characters")
	}
	if entry.Result != "" && entry.Result != "passed" && entry.Result != "failed" && entry.Result != "inconclusive" {
		problems = append(problems, "result must be passed, failed, or inconclusive")
	}
	if (entry.Kind == EntryReview || entry.Kind == EntryVerification) && entry.Result == "" {
		problems = append(problems, "reviews and verifications require a result")
	}
	if len(entry.Evidence) > 100 {
		problems = append(problems, "evidence_sha256 must contain no more than 100 digests")
	}
	if len(entry.Authorization) > 16<<10 || (len(entry.Authorization) > 0 && !json.Valid(entry.Authorization)) {
		problems = append(problems, "authorization must be bounded valid JSON")
	}
	for _, digest := range entry.Evidence {
		if !digestPattern.MatchString(digest) {
			problems = append(problems, "evidence_sha256 values must be lowercase SHA-256 digests")
			break
		}
	}
	if !validTimestamp(entry.CreatedAt) {
		problems = append(problems, "created_at must use RFC3339")
	}
	for _, value := range []string{entry.ID, entry.ProjectID, entry.TaskID, entry.SessionID, entry.Actor, entry.Summary, entry.Details} {
		if privacy.ContainsCredentialText(value) {
			problems = append(problems, "continuity entry contains credential material")
			break
		}
	}
	return joinProblems(problems)
}

func taskHandoffActorMatches(action, actor string, handoff TaskHandoff) bool {
	switch action {
	case "drafted", "offered", "cancelled":
		return actor == handoff.FromOwner
	case "accepted", "rejected":
		return actor == handoff.ToOwner
	default:
		return false
	}
}

func ValidateTaskHandoffEvent(event TaskHandoffEvent) error {
	var problems []string
	if event.SchemaVersion != TaskHandoffEventSchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	if err := ValidateTaskHandoff(event.Handoff); err != nil {
		problems = append(problems, err.Error())
	}
	handoff := event.Handoff
	switch event.Action {
	case "drafted":
		if event.ExpectedRevision != 0 || handoff.Revision != 1 || handoff.Status != TaskHandoffDraft || handoff.CreatedAt != handoff.UpdatedAt {
			problems = append(problems, "drafted requires revision 1, expected_revision 0, and draft status")
		}
	case "offered":
		if event.ExpectedRevision < 1 || handoff.Revision != event.ExpectedRevision+1 || handoff.Status != TaskHandoffOffered ||
			handoff.TransferredTaskRevision != 0 || handoff.TransferredClaimRevision != 0 {
			problems = append(problems, "offered requires a revision increment and offered status")
		}
	case "accepted":
		if event.ExpectedRevision < 1 || handoff.Revision != event.ExpectedRevision+1 || handoff.Status != TaskHandoffAccepted ||
			handoff.TransferredTaskRevision != handoff.TaskRevision+1 || handoff.TransferredClaimRevision != handoff.ClaimRevision+1 {
			problems = append(problems, "accepted requires a revision increment and matching transferred task/claim revisions")
		}
	case "rejected":
		if event.ExpectedRevision < 1 || handoff.Revision != event.ExpectedRevision+1 || handoff.Status != TaskHandoffRejected ||
			handoff.TransferredTaskRevision != 0 || handoff.TransferredClaimRevision != 0 {
			problems = append(problems, "rejected requires a revision increment and rejected status")
		}
	case "cancelled":
		if event.ExpectedRevision < 1 || handoff.Revision != event.ExpectedRevision+1 || handoff.Status != TaskHandoffCancelled ||
			handoff.TransferredTaskRevision != 0 || handoff.TransferredClaimRevision != 0 {
			problems = append(problems, "cancelled requires a revision increment and cancelled status")
		}
	default:
		problems = append(problems, "action must be drafted, offered, accepted, rejected, or cancelled")
	}
	return joinProblems(problems)
}

func ValidateTaskHandoff(handoff TaskHandoff) error {
	var problems []string
	if handoff.SchemaVersion != TaskHandoffSchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	for name, value := range map[string]string{"id": handoff.ID, "task_id": handoff.TaskID,
		"claim_id": handoff.ClaimID, "checkpoint_entry_id": handoff.CheckpointEntryID} {
		if !stableID.MatchString(value) {
			problems = append(problems, name+" must be a lowercase stable identifier")
		}
	}
	if !boundedText(handoff.ProjectID, 256) || !boundedText(handoff.FromOwner, 128) || !boundedText(handoff.ToOwner, 128) ||
		handoff.FromOwner == handoff.ToOwner {
		problems = append(problems, "project_id and distinct bounded owners are required")
	}
	if handoff.TaskRevision < 1 || handoff.ClaimRevision < 1 || handoff.Revision < 1 {
		problems = append(problems, "task_revision, claim_revision, and revision must be at least 1")
	}
	if !digestPattern.MatchString(handoff.BaselineSHA256) {
		problems = append(problems, "baseline_sha256 must be a lowercase SHA-256 digest")
	}
	if !boundedText(handoff.CheckpointSummary, 4000) ||
		(handoff.CheckpointDetails != "" && !boundedText(handoff.CheckpointDetails, 8000)) {
		problems = append(problems, "checkpoint summary/details exceed their size limits")
	}
	switch handoff.Status {
	case TaskHandoffDraft, TaskHandoffOffered, TaskHandoffAccepted, TaskHandoffRejected, TaskHandoffCancelled:
	default:
		problems = append(problems, "status must be draft, offered, accepted, rejected, or cancelled")
	}
	if !validTimestamp(handoff.CreatedAt) || !validTimestamp(handoff.UpdatedAt) {
		problems = append(problems, "created_at and updated_at must use RFC3339")
	} else {
		created, _ := time.Parse(time.RFC3339Nano, handoff.CreatedAt)
		updated, _ := time.Parse(time.RFC3339Nano, handoff.UpdatedAt)
		if updated.Before(created) {
			problems = append(problems, "updated_at cannot precede created_at")
		}
	}
	if privacy.ContainsCredentialText(handoff.ProjectID) || privacy.ContainsCredentialText(handoff.FromOwner) ||
		privacy.ContainsCredentialText(handoff.ToOwner) || privacy.ContainsCredentialText(handoff.CheckpointSummary) ||
		privacy.ContainsCredentialText(handoff.CheckpointDetails) {
		problems = append(problems, "task handoff contains credential material")
	}
	return joinProblems(problems)
}

func ValidateTaskClaimEvent(event TaskClaimEvent) error {
	var problems []string
	if event.SchemaVersion != TaskClaimEventSchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	if err := ValidateTaskClaim(event.Claim); err != nil {
		problems = append(problems, err.Error())
	}
	switch event.Action {
	case "acquired":
		if event.ExpectedRevision != 0 || event.Claim.Revision != 1 || event.Claim.Status != "active" ||
			event.Claim.CreatedAt != event.Claim.UpdatedAt {
			problems = append(problems, "acquired requires revision 1, expected_revision 0, and active status")
		}
	case "renewed", "revalidated", "transferred":
		if event.ExpectedRevision < 1 || event.Claim.Revision != event.ExpectedRevision+1 || event.Claim.Status != "active" || event.Claim.ReleasedAt != "" {
			problems = append(problems, "renewed, revalidated, or transferred claims require a revision increment and active status")
		}
	case "released":
		if event.ExpectedRevision < 1 || event.Claim.Revision != event.ExpectedRevision+1 || event.Claim.Status != "released" ||
			!validTimestamp(event.Claim.ReleasedAt) || event.Claim.ReleasedAt != event.Claim.UpdatedAt {
			problems = append(problems, "released claims require a revision increment and matching released_at/updated_at")
		}
	default:
		problems = append(problems, "action must be acquired, renewed, revalidated, or released")
	}
	return joinProblems(problems)
}

func ValidateTaskClaim(claim TaskClaim) error {
	var problems []string
	if claim.SchemaVersion != TaskClaimSchemaVersion {
		problems = append(problems, "unsupported schema_version")
	}
	for name, value := range map[string]string{"id": claim.ID, "task_id": claim.TaskID} {
		if !stableID.MatchString(value) {
			problems = append(problems, name+" must be a lowercase stable identifier")
		}
	}
	if !boundedText(claim.ProjectID, 256) || !boundedText(claim.Owner, 128) {
		problems = append(problems, "project_id and owner are required and bounded")
	}
	if len(claim.Scopes) == 0 || len(claim.Scopes) > 32 {
		problems = append(problems, "scopes must contain 1 to 32 entries")
	}
	previous := ""
	for _, scope := range claim.Scopes {
		key := string(scope.Kind) + "\x00" + scope.Value
		if previous != "" && key <= previous {
			problems = append(problems, "scopes must be unique and sorted by kind and value")
			break
		}
		previous = key
		switch scope.Kind {
		case TaskClaimScopePath:
			if !validTaskClaimPath(scope.Value) {
				problems = append(problems, "path scope must be a safe repository-relative path or trailing /** subtree")
			}
		case TaskClaimScopeModule, TaskClaimScopeAPI, TaskClaimScopeSchema:
			if !validTaskClaimLabel(scope.Value) {
				problems = append(problems, "module, api, and schema scopes must be bounded labels without control characters or credentials")
			}
		default:
			problems = append(problems, "scope kind must be path, module, api, or schema")
		}
	}
	if claim.TaskRevision < 1 || claim.Revision < 1 {
		problems = append(problems, "task_revision and revision must be at least 1")
	}
	if !digestPattern.MatchString(claim.BaselineSHA256) {
		problems = append(problems, "baseline_sha256 must be a lowercase SHA-256 digest")
	}
	if claim.Status != "active" && claim.Status != "released" {
		problems = append(problems, "status must be active or released")
	}
	if !validTimestamp(claim.CreatedAt) || !validTimestamp(claim.UpdatedAt) || !validTimestamp(claim.LeaseExpiresAt) {
		problems = append(problems, "created_at, updated_at, and lease_expires_at must use RFC3339")
	} else if expiry, err := time.Parse(time.RFC3339Nano, claim.LeaseExpiresAt); err == nil && claim.Status == "active" {
		updated, _ := time.Parse(time.RFC3339Nano, claim.UpdatedAt)
		if !expiry.After(updated) {
			problems = append(problems, "lease_expires_at must be after updated_at")
		}
	}
	if claim.Status == "active" && claim.ReleasedAt != "" {
		problems = append(problems, "active claims cannot have released_at")
	}
	if claim.Status == "released" && (!validTimestamp(claim.ReleasedAt) || claim.ReleasedAt != claim.UpdatedAt) {
		problems = append(problems, "released claims require released_at equal to updated_at")
	}
	for _, value := range []string{claim.ID, claim.ProjectID, claim.TaskID, claim.Owner} {
		if privacy.ContainsCredentialText(value) {
			problems = append(problems, "task claim contains credential material")
			break
		}
	}
	return joinProblems(problems)
}

func validTaskClaimPath(value string) bool {
	base := strings.TrimSuffix(value, "/**")
	if strings.Contains(value, "**") && value != "**" && !strings.HasSuffix(value, "/**") {
		return false
	}
	if value == "**" || strings.Contains(strings.TrimSuffix(value, "/**"), "*") ||
		strings.Contains(strings.TrimSuffix(value, "/**"), "?") || strings.Contains(strings.TrimSuffix(value, "/**"), "[") {
		return false
	}
	return base != "" && validScopeItem(value, false)
}

func validTaskClaimLabel(value string) bool {
	if !boundedText(value, 256) || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") || privacy.ContainsCredentialText(value) {
		return false
	}
	return true
}

func boundedText(value string, max int) bool {
	trimmed := strings.TrimSpace(value)
	return len(trimmed) > 0 && len(trimmed) <= max
}

func validTimestamp(value string) bool {
	if value == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func hasCredential(values ...string) bool {
	for _, value := range values {
		if privacy.ContainsCredentialText(value) {
			return true
		}
	}
	return false
}

func joinProblems(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}
