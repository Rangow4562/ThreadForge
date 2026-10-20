package continuity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanOnlyResumeKeepsPlanButWithholdsExecutionProgress(t *testing.T) {
	task := Task{
		ID: "plan-only-task", ProjectID: "project-a", Title: "Prepare proposal",
		Objective: "Prepare a plan and wait for approval.", Status: TaskInProgress,
		Summary: "PRIVATE_STALE_TASK_PROGRESS", NextAction: "PRIVATE_STALE_TASK_NEXT_ACTION",
		Plan: &TaskPlan{Decision: PlanOnly, Version: 3, Markdown: "Keep the proposal; do not implement yet."},
	}
	selectedSession := &Session{
		ID: "session-a", TaskID: task.ID, ProjectID: task.ProjectID, Harness: "codex-cli",
		Status: SessionActive, Summary: "PRIVATE_SESSION_PROGRESS", NextAction: "PRIVATE_SESSION_ACTION",
		Completed: []string{"PRIVATE_SESSION_COMPLETED"},
	}
	otherSession := Session{ID: "session-b", TaskID: task.ID, ProjectID: task.ProjectID, Harness: "claude-code", Status: SessionActive}
	entries := []Entry{
		{ID: "task-handoff", TaskID: task.ID, Summary: "PRIVATE_TASK_HANDOFF"},
		{ID: "session-handoff", TaskID: task.ID, SessionID: selectedSession.ID, Summary: "PRIVATE_SESSION_HANDOFF"},
	}

	resume := NewTaskResumeContext(task, selectedSession, []Session{*selectedSession, otherSession}, entries, true, true)
	if resume.Status != TaskResumeContextAvailable || resume.Task.Plan == nil ||
		resume.Task.Plan.Decision != PlanOnly || resume.Task.Plan.Markdown != task.Plan.Markdown {
		t.Fatalf("plan-only resume lost its plan or required session selection: %#v", resume)
	}
	if resume.Task.Summary != "" || resume.Task.NextAction != "" || resume.SelectedSession != nil ||
		len(resume.SessionChoices) != 0 || resume.SessionChoicesTruncated ||
		len(resume.Entries) != 0 || resume.EntriesTruncated {
		t.Fatalf("plan-only resume retained execution progress: %#v", resume)
	}
	warnings := strings.Join(resume.Warnings, ",")
	if !strings.Contains(warnings, "plan_only_recorded") || strings.Contains(warnings, "multiple_active_sessions") ||
		resume.ActionReady || !resume.GuidanceOnly || resume.ExecutionAuthorized {
		t.Fatalf("plan-only resume changed its advisory boundary: %#v", resume)
	}
	encoded, err := json.Marshal(resume)
	if err != nil {
		t.Fatal(err)
	}
	for _, privateValue := range []string{
		"PRIVATE_STALE_TASK_PROGRESS", "PRIVATE_STALE_TASK_NEXT_ACTION", "PRIVATE_SESSION_PROGRESS",
		"PRIVATE_SESSION_ACTION", "PRIVATE_SESSION_COMPLETED", "PRIVATE_TASK_HANDOFF", "PRIVATE_SESSION_HANDOFF",
	} {
		if strings.Contains(string(encoded), privateValue) {
			t.Fatalf("plan-only task projection retained %q", privateValue)
		}
	}
	if task.Summary != "PRIVATE_STALE_TASK_PROGRESS" || task.NextAction != "PRIVATE_STALE_TASK_NEXT_ACTION" ||
		selectedSession.Summary != "PRIVATE_SESSION_PROGRESS" || len(entries) != 2 {
		t.Fatal("constructing the plan-only snapshot mutated its source records")
	}
}
