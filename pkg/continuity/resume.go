package continuity

const TaskResumeContextSchemaVersion = "threadforge.task-resume-context.v1"
const TaskResumeDiscoverySchemaVersion = "threadforge.task-resume-discovery.v1"

const (
	TaskResumeContextAvailable                = "available"
	TaskResumeContextSessionSelectionRequired = "session_selection_required"
	TaskResumeDiscoveryAvailable              = "available"
	TaskResumeDiscoveryNoActiveTask           = "no_active_task"
	TaskResumeDiscoverySelectionRequired      = "task_selection_required"
)

// TaskResumeContext is a bounded, read-only continuity snapshot for a harness
// explicitly resuming one task. It records workflow state without authorizing
// tools, commands, or implementation.
type TaskResumeContext struct {
	SchemaVersion           string    `json:"schema_version"`
	Status                  string    `json:"status"`
	Task                    Task      `json:"task"`
	SelectedSession         *Session  `json:"selected_session,omitempty"`
	SessionChoices          []Session `json:"session_choices"`
	SessionChoicesTruncated bool      `json:"session_choices_truncated"`
	Entries                 []Entry   `json:"entries"`
	EntriesTruncated        bool      `json:"entries_truncated"`
	Warnings                []string  `json:"warnings"`
	ActionReady             bool      `json:"action_ready"`
	GuidanceOnly            bool      `json:"guidance_only"`
	ExecutionAuthorized     bool      `json:"execution_authorized"`
}

// TaskResumeDiscovery is a project-scoped, read-only result. It intentionally
// withholds task details unless exactly one open task has an active session or
// exactly one task is marked in progress.
type TaskResumeDiscovery struct {
	SchemaVersion       string             `json:"schema_version"`
	Status              string             `json:"status"`
	CandidateCount      int                `json:"candidate_count"`
	Context             *TaskResumeContext `json:"context,omitempty"`
	ActionReady         bool               `json:"action_ready"`
	GuidanceOnly        bool               `json:"guidance_only"`
	ExecutionAuthorized bool               `json:"execution_authorized"`
}

// NewTaskResumeContext constructs the advisory MCP continuity payload. Session
// selection is handled by the store so ambiguity is surfaced rather than
// guessed; this function derives warning codes from the selected task snapshot.
func NewTaskResumeContext(task Task, selectedSession *Session, sessionChoices []Session, entries []Entry, entriesTruncated, sessionChoicesTruncated bool) TaskResumeContext {
	planOnly := task.Plan != nil && task.Plan.Decision == PlanOnly
	if planOnly {
		// A plan-only task can resume its requirements and proposal, but prior
		// progress, session state, and handoffs must not nudge implementation.
		task.Summary = ""
		task.NextAction = ""
		selectedSession = nil
		sessionChoices = []Session{}
		sessionChoicesTruncated = false
		entries = []Entry{}
		entriesTruncated = false
	}
	status := TaskResumeContextAvailable
	if !planOnly && len(sessionChoices) > 1 && selectedSession == nil {
		status = TaskResumeContextSessionSelectionRequired
	}
	if sessionChoices == nil {
		sessionChoices = []Session{}
	}
	if entries == nil {
		entries = []Entry{}
	}
	warnings := make([]string, 0, 6)
	if task.Requirements == nil || task.Requirements.Status != RequirementsFinalized {
		warnings = append(warnings, "requirements_not_finalized")
	}
	if task.Requirements != nil {
		for _, question := range task.Requirements.Questions {
			if question.Answer == "" {
				warnings = append(warnings, "unanswered_clarifications")
				break
			}
		}
	}
	if task.Plan == nil {
		warnings = append(warnings, "plan_missing")
	} else {
		switch task.Plan.Decision {
		case PlanUnconfirmed:
			warnings = append(warnings, "plan_unconfirmed")
		case PlanOnly:
			warnings = append(warnings, "plan_only_recorded")
		}
		if task.Plan.Decision == PlanGovernedExecution &&
			(task.Plan.Scope == nil || len(task.Plan.Scope.Include) == 0) {
			warnings = append(warnings, "implementation_scope_missing")
		}
	}
	if task.Status == TaskBlocked {
		warnings = append(warnings, "task_blocked")
	} else if task.Status == TaskCompleted || task.Status == TaskAbandoned {
		warnings = append(warnings, "task_terminal")
	}
	if status == TaskResumeContextSessionSelectionRequired {
		warnings = append(warnings, "multiple_active_sessions")
	}
	return TaskResumeContext{
		SchemaVersion:           TaskResumeContextSchemaVersion,
		Status:                  status,
		Task:                    task,
		SelectedSession:         selectedSession,
		SessionChoices:          sessionChoices,
		SessionChoicesTruncated: sessionChoicesTruncated,
		Entries:                 entries,
		EntriesTruncated:        entriesTruncated,
		Warnings:                warnings,
		ActionReady:             false,
		GuidanceOnly:            true,
		ExecutionAuthorized:     false,
	}
}
