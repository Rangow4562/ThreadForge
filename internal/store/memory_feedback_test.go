package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/continuity"
	"github.com/threadforge-ai/threadforge/pkg/memoryfeedback"
)

func TestMemoryFeedbackIsTaskScopedAppendOnlyAndSummarizedAsSelfReported(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-memory-feedback", ProjectID: "repo-a", Owner: "local-user",
		Title: "Fix an intermittent build", Objective: "Make the build deterministic",
		AcceptanceCriteria: []string{"The regression test passes"}, Status: continuity.TaskOpen,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "local-user", "record task for outcome feedback"); err != nil {
		t.Fatal(err)
	}
	feedback := memoryfeedback.Feedback{
		SchemaVersion: memoryfeedback.SchemaVersion, ID: "memory-feedback-0123456789abcdef0123456789abcdef",
		ProjectID: task.ProjectID, TaskID: task.ID, TargetKind: memoryfeedback.TargetProcedure,
		TargetID: "build-recovery", TargetRevision: 3, Exposure: memoryfeedback.ExposureUsed,
		Utility: memoryfeedback.UtilityHelpful, TaskResult: "passed", Harness: "codex-cli",
		Actor: "local-user", EvidenceClass: memoryfeedback.EvidenceSelfReported, CreatedAt: stamp,
	}
	sequence, err := database.RecordMemoryFeedback(ctx, feedback)
	if err != nil || sequence < 1 {
		t.Fatalf("record sequence=%d err=%v", sequence, err)
	}
	items, total, err := database.ListMemoryFeedback(ctx, memoryfeedback.Filter{ProjectID: task.ProjectID, Limit: 50})
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != feedback.ID || items[0].TaskID != task.ID {
		t.Fatalf("list items=%+v total=%d err=%v", items, total, err)
	}
	summary, err := database.SummarizeMemoryFeedback(ctx, task.ProjectID, 50)
	if err != nil || summary.Status != "complete" || summary.Records != 1 || len(summary.Groups) != 1 ||
		summary.Groups[0].Count != 1 || summary.EvidenceClass != memoryfeedback.EvidenceSelfReported ||
		summary.CutoverEvidenceEligible || summary.ExecutionAuthorized {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE memory_feedback_events SET harness = 'other' WHERE id = ?`, feedback.ID); err == nil {
		t.Fatal("memory feedback update was allowed")
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM memory_feedback_events WHERE id = ?`, feedback.ID); err == nil {
		t.Fatal("memory feedback deletion was allowed")
	}
}

func TestMemoryFeedbackRejectsTaskAndSessionScopeMismatch(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-scope-check", ProjectID: "repo-a", Owner: "local-user",
		Title: "Check task scope", Objective: "Verify local scope checks",
		AcceptanceCriteria: []string{"Wrong scope is rejected"}, Status: continuity.TaskOpen,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "local-user", "create local test task"); err != nil {
		t.Fatal(err)
	}
	feedback := memoryfeedback.Feedback{
		SchemaVersion: memoryfeedback.SchemaVersion, ID: "memory-feedback-1123456789abcdef0123456789abcdef",
		ProjectID: "repo-b", TaskID: task.ID, TargetKind: memoryfeedback.TargetFact,
		TargetID: "build-fact", TargetRevision: 1, Exposure: memoryfeedback.ExposureShownNotUsed,
		Utility: memoryfeedback.UtilityNotAssessed, TaskResult: "inconclusive", Harness: "cursor",
		Actor: "local-user", EvidenceClass: memoryfeedback.EvidenceSelfReported, CreatedAt: stamp,
	}
	if _, err := database.RecordMemoryFeedback(ctx, feedback); err == nil {
		t.Fatal("project mismatch was accepted")
	}
	feedback.ProjectID = task.ProjectID
	feedback.SessionID = "session-other-project"
	if _, err := database.RecordMemoryFeedback(ctx, feedback); err == nil {
		t.Fatal("missing session was accepted")
	}
}

func TestMemoryFeedbackListDoesNotCrossProjectScope(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	items, total, err := database.ListMemoryFeedback(ctx, memoryfeedback.Filter{ProjectID: "repo-other", Limit: 50})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("items=%+v total=%d err=%v", items, total, err)
	}
}
