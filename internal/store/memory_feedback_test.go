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
	receipt := memoryfeedback.RetrievalReceipt{
		SchemaVersion: memoryfeedback.RetrievalReceiptVersion,
		ID:            "retrieval-0123456789abcdef0123456789abcdef", ProjectID: task.ProjectID, TaskID: task.ID,
		TargetKind: memoryfeedback.TargetProcedure, TargetID: "build-recovery", TargetRevision: 3,
		Tool: "threadforge_procedure_get", Freshness: "verified", GuidanceIncluded: true,
		ObservationBoundary: "mcp_handler_result_prepared", CreatedAt: stamp,
	}
	if sequence, err := database.RecordMemoryRetrievalReceipt(ctx, receipt); err != nil || sequence < 1 {
		t.Fatalf("record retrieval receipt sequence=%d err=%v", sequence, err)
	}
	feedback := memoryfeedback.Feedback{
		SchemaVersion: memoryfeedback.SchemaVersion, ID: "memory-feedback-0123456789abcdef0123456789abcdef",
		ProjectID: task.ProjectID, TaskID: task.ID, TargetKind: memoryfeedback.TargetProcedure,
		TargetID: "build-recovery", TargetRevision: 3, Exposure: memoryfeedback.ExposureUsed,
		Utility: memoryfeedback.UtilityHelpful, TaskResult: "passed", Harness: "codex-cli",
		Actor: "local-user", EvidenceClass: memoryfeedback.EvidenceSelfReported, CreatedAt: stamp,
		RetrievalReceiptID: receipt.ID,
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
		summary.RetrievalReceiptLinked != 1 ||
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
	if _, err := database.db.ExecContext(ctx, `UPDATE memory_retrieval_receipts SET target_id = 'other' WHERE id = ?`, receipt.ID); err == nil {
		t.Fatal("memory retrieval receipt update was allowed")
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM memory_retrieval_receipts WHERE id = ?`, receipt.ID); err == nil {
		t.Fatal("memory retrieval receipt deletion was allowed")
	}
}

func TestMemoryFeedbackRejectsReceiptForWithheldOrDifferentRevision(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-receipt-reject", ProjectID: "repo-receipt", Owner: "local-user",
		Title: "Check receipt binding", Objective: "Reject stale or mismatched receipt links",
		AcceptanceCriteria: []string{"Only included matching guidance can be linked"}, Status: continuity.TaskOpen,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "local-user", "create receipt-binding task"); err != nil {
		t.Fatal(err)
	}
	receipt := memoryfeedback.RetrievalReceipt{
		SchemaVersion: memoryfeedback.RetrievalReceiptVersion,
		ID:            "retrieval-1123456789abcdef0123456789abcdef", ProjectID: task.ProjectID, TaskID: task.ID,
		TargetKind: memoryfeedback.TargetProcedure, TargetID: "build-recovery", TargetRevision: 3,
		Tool: "threadforge_procedure_guide", Freshness: "stale", GuidanceIncluded: false,
		ObservationBoundary: "mcp_handler_result_prepared", CreatedAt: stamp,
	}
	if _, err := database.RecordMemoryRetrievalReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	feedback := memoryfeedback.Feedback{
		SchemaVersion: memoryfeedback.SchemaVersion, ID: "memory-feedback-2123456789abcdef0123456789abcdef",
		ProjectID: task.ProjectID, TaskID: task.ID, TargetKind: memoryfeedback.TargetProcedure,
		TargetID: "build-recovery", TargetRevision: 3, RetrievalReceiptID: receipt.ID,
		Exposure: memoryfeedback.ExposureUsed, Utility: memoryfeedback.UtilityHelpful,
		TaskResult: "passed", Harness: "codex-cli", Actor: "local-user",
		EvidenceClass: memoryfeedback.EvidenceSelfReported, CreatedAt: stamp,
	}
	if _, err := database.RecordMemoryFeedback(ctx, feedback); err == nil {
		t.Fatal("feedback linked to a stale guidance-withheld receipt was accepted")
	}
	receipt.Freshness = "verified"
	receipt.GuidanceIncluded = true
	receipt.TargetRevision++
	receipt.ID = "retrieval-2123456789abcdef0123456789abcdef"
	if _, err := database.RecordMemoryRetrievalReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	feedback.RetrievalReceiptID = receipt.ID
	if _, err := database.RecordMemoryFeedback(ctx, feedback); err == nil {
		t.Fatal("feedback linked to another approved revision was accepted")
	}
}

func TestMemoryFeedbackLinksVersion2ProjectMemoryReceipt(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-project-memory-receipt", ProjectID: "repo-project-memory", Owner: "local-user",
		Title: "Use approved project memory", Objective: "Bind reported outcome to the exact retrieved fact",
		AcceptanceCriteria: []string{"Receipt and feedback use the same task, project, and revision"}, Status: continuity.TaskOpen,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "local-user", "create project-memory receipt task"); err != nil {
		t.Fatal(err)
	}
	receipt := memoryfeedback.RetrievalReceipt{
		SchemaVersion: memoryfeedback.RetrievalReceiptVersionV2,
		ID:            "retrieval-4123456789abcdef0123456789abcdef", ProjectID: task.ProjectID, TaskID: task.ID,
		TargetKind: memoryfeedback.TargetFact, TargetID: "build-invariant", TargetRevision: 2,
		Tool: "threadforge_project_memory_read_v2", Freshness: "unverified", ResultStatus: "incomplete",
		GuidanceIncluded: true, ObservationBoundary: "mcp_handler_result_prepared", CreatedAt: stamp,
	}
	if _, err := database.RecordMemoryRetrievalReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	feedback := memoryfeedback.Feedback{
		SchemaVersion: memoryfeedback.SchemaVersion, ID: "memory-feedback-3123456789abcdef0123456789abcdef",
		ProjectID: task.ProjectID, TaskID: task.ID, RetrievalReceiptID: receipt.ID,
		TargetKind: memoryfeedback.TargetFact, TargetID: receipt.TargetID, TargetRevision: receipt.TargetRevision,
		Exposure: memoryfeedback.ExposureUsed, Utility: memoryfeedback.UtilityHelpful, TaskResult: "passed",
		Harness: "codex-cli", Actor: "local-user", EvidenceClass: memoryfeedback.EvidenceSelfReported, CreatedAt: stamp,
	}
	if _, err := database.RecordMemoryFeedback(ctx, feedback); err != nil {
		t.Fatalf("record feedback against unverified-but-included project memory: %v", err)
	}
	items, total, err := database.ListMemoryFeedback(ctx, memoryfeedback.Filter{ProjectID: task.ProjectID, Limit: 10})
	if err != nil || total != 1 || len(items) != 1 || items[0].RetrievalReceiptID != receipt.ID {
		t.Fatalf("items=%+v total=%d err=%v", items, total, err)
	}
	summary, err := database.SummarizeMemoryFeedback(ctx, task.ProjectID, 10)
	if err != nil || summary.RetrievalReceiptLinked != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
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
	foreignTask := task
	foreignTask.ID = "task-foreign-project"
	foreignTask.ProjectID = "repo-b"
	foreignTask.Title = "Foreign project task"
	foreignTask.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	foreignTask.UpdatedAt = foreignTask.CreatedAt
	if err := database.CreateTask(ctx, foreignTask, "local-user", "create foreign task"); err != nil {
		t.Fatal(err)
	}
	foreignSession := continuity.Session{
		SchemaVersion: continuity.SchemaVersion, ID: "session-foreign-project", ProjectID: foreignTask.ProjectID,
		TaskID: foreignTask.ID, Harness: "cursor", Status: continuity.SessionActive, Revision: 1,
		StartedAt: foreignTask.CreatedAt, UpdatedAt: foreignTask.UpdatedAt,
	}
	if err := database.StartSession(ctx, foreignSession, "local-user", "create foreign session"); err != nil {
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
	feedback.SessionID = foreignSession.ID
	if _, err := database.RecordMemoryFeedback(ctx, feedback); err == nil {
		t.Fatal("session from another task and project was accepted")
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
