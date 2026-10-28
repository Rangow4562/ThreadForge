package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/internal/projectfiles"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/continuity"
	"github.com/threadforge-ai/threadforge/pkg/memoryfeedback"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

func TestMemoryFeedbackCLIRecordsAndSummarizesScopedSelfReports(t *testing.T) {
	projectRoot := t.TempDir()
	databasePath := filepath.Join(t.TempDir(), "threadforge.sqlite")
	projectStore, err := projectfiles.New(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = projectStore.Publish(projectmemory.Candidate{
		SchemaVersion: projectmemory.SchemaVersion, ID: "memory-fact", ProjectID: "repo-feedback",
		Kind: projectmemory.KindFact, Title: "Build invariant", Content: json.RawMessage(`{"statement":"Use the repository wrapper"}`),
		Evidence: []projectmemory.Evidence{{Origin: "human", ContentSHA256: strings.Repeat("a", 64), Redacted: true}},
	}, 0, "reviewer", "verified the local build note", true, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-feedback", ProjectID: "repo-feedback", Owner: "local-user",
		Title: "Run the build", Objective: "Verify the repository build with approved guidance",
		AcceptanceCriteria: []string{"The build completes"}, Status: continuity.TaskOpen,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(context.Background(), task, "local-user", "create feedback test task"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"memory", "feedback", "record", "--project-root", projectRoot, "--project-id", "repo-feedback",
		"--task-id", "task-feedback", "--target-kind", "fact", "--target-id", "memory-fact",
		"--target-revision", "1", "--exposure", "used", "--utility", "helpful", "--task-result", "passed",
		"--harness", "codex-cli", "--db", databasePath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("record exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var recorded memoryfeedback.RecordResult
	if err := json.Unmarshal(stdout.Bytes(), &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.SchemaVersion != memoryfeedback.RecordResultVersion || recorded.Status != "recorded" ||
		recorded.Feedback.EvidenceClass != memoryfeedback.EvidenceSelfReported || recorded.Feedback.CutoverEvidenceEligible ||
		recorded.Feedback.ExecutionAuthorized || recorded.Feedback.TargetRevision != 1 ||
		strings.Contains(stdout.String(), "Use the repository wrapper") {
		t.Fatalf("unexpected feedback result: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"memory", "feedback", "list", "--project-id", "repo-feedback", "--target-kind", "fact", "--target-id", "memory-fact", "--db", databasePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("list exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var listed memoryfeedback.ListResult
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Total != 1 || len(listed.Feedback) != 1 || listed.Feedback[0].ID != recorded.Feedback.ID ||
		listed.CutoverEvidenceEligible || listed.ExecutionAuthorized {
		t.Fatalf("unexpected list result: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"memory", "feedback", "summary", "--project-id", "repo-feedback", "--db", databasePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("summary exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var summary memoryfeedback.SummaryResult
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Status != "complete" || summary.Records != 1 || len(summary.Groups) != 1 ||
		summary.Groups[0].TargetID != "memory-fact" || summary.Groups[0].Utility != memoryfeedback.UtilityHelpful ||
		summary.Groups[0].TaskResult != "passed" || summary.Groups[0].Count != 1 || summary.CutoverEvidenceEligible {
		t.Fatalf("unexpected summary: %s", stdout.String())
	}
}

func TestMemoryFeedbackCLIRejectsWrongProjectAndUnapprovedTargets(t *testing.T) {
	projectRoot := t.TempDir()
	databasePath := filepath.Join(t.TempDir(), "threadforge.sqlite")
	projectStore, err := projectfiles.New(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = projectStore.Publish(projectmemory.Candidate{
		SchemaVersion: projectmemory.SchemaVersion, ID: "memory-fact", ProjectID: "repo-a",
		Kind: projectmemory.KindFact, Title: "Build invariant", Content: json.RawMessage(`{"statement":"Use a deterministic build"}`),
		Evidence: []projectmemory.Evidence{{Origin: "human", ContentSHA256: strings.Repeat("b", 64), Redacted: true}},
	}, 0, "reviewer", "verified source note", true, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-feedback-scope", ProjectID: "repo-feedback", Owner: "local-user",
		Title: "Check memory feedback scope", Objective: "Reject mismatched and unapproved targets",
		AcceptanceCriteria: []string{"Invalid feedback is not stored"}, Status: continuity.TaskOpen,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(context.Background(), task, "local-user", "create scope test task"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	baseArgs := []string{
		"memory", "feedback", "record", "--project-root", projectRoot, "--project-id", "repo-feedback",
		"--task-id", "task-feedback-scope", "--target-kind", "fact", "--target-id", "memory-fact",
		"--target-revision", "1", "--exposure", "shown_not_used", "--utility", "not_assessed",
		"--task-result", "failed", "--harness", "cursor", "--db", databasePath,
	}
	var stdout, stderr bytes.Buffer
	code := Run(baseArgs, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "target project memory is not the requested approved revision") {
		t.Fatalf("missing target exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	baseArgs[12] = "missing-memory"
	code = Run(baseArgs, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "read approved project-memory target") {
		t.Fatalf("unapproved target exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	database, err = store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	items, total, err := database.ListMemoryFeedback(context.Background(), memoryfeedback.Filter{ProjectID: "repo-feedback", Limit: 10})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("invalid feedback was stored: items=%+v total=%d err=%v", items, total, err)
	}
}

func TestMemoryFeedbackCLIRecordsApprovedSQLiteProcedureTarget(t *testing.T) {
	projectRoot := t.TempDir()
	databasePath := filepath.Join(t.TempDir(), "threadforge.sqlite")
	database, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-procedure-feedback", ProjectID: "repo-feedback", Owner: "local-user",
		Title: "Apply the approved repair", Objective: "Check feedback for a local approved procedure",
		AcceptanceCriteria: []string{"The procedure target is revision checked"}, Status: continuity.TaskOpen,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "local-user", "create procedure feedback task"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	graph := proceduregraph.Graph{
		SchemaVersion: proceduregraph.SchemaVersion, ID: "approved-build-repair", ProjectID: "repo-feedback",
		Title: "Approved build repair", Revision: 1, Status: proceduregraph.StatusDraft,
		Nodes: []proceduregraph.Node{{ID: "inspect", Kind: proceduregraph.NodeTrigger, Instruction: "Inspect the build failure."}},
	}
	if err := database.CreateProposal(ctx, graph, "local-user", "seed feedback test procedure", now); err != nil {
		database.Close()
		t.Fatal(err)
	}
	validated, err := database.RecordValidation(ctx, graph.ID, graph.Revision, proceduregraph.Validation{
		DatasetFingerprint: strings.Repeat("c", 64), HeldOut: true, BaselineRuns: 2,
		BaselineScore: 0.8, CandidateScore: 0.8, NoiseFloor: 0,
		CostUnit: "tokens", BaselineCost: 100, CandidateCost: 100, MaxCostIncrease: 0, Status: "passed",
	}, now.Add(time.Minute))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	approved, err := database.Approve(ctx, graph.ID, validated.Revision, "reviewer", "held-out validation passed", true, now.Add(2*time.Minute))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	receipt := memoryfeedback.RetrievalReceipt{
		SchemaVersion: memoryfeedback.RetrievalReceiptVersion, ID: "retrieval-0123456789abcdef0123456789abcdef",
		ProjectID: task.ProjectID, TaskID: task.ID, TargetKind: memoryfeedback.TargetProcedure,
		TargetID: approved.ID, TargetRevision: approved.Revision, Tool: "threadforge_procedure_get",
		Freshness: "verified", GuidanceIncluded: true, ObservationBoundary: "mcp_handler_result_prepared", CreatedAt: stamp,
	}
	if _, err := database.RecordMemoryRetrievalReceipt(ctx, receipt); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"memory", "feedback", "record", "--project-root", projectRoot, "--project-id", task.ProjectID,
		"--task-id", task.ID, "--target-kind", "procedure", "--target-id", approved.ID,
		"--target-revision", fmt.Sprint(approved.Revision), "--exposure", "used", "--utility", "harmful",
		"--task-result", "failed", "--harness", "claude-code", "--retrieval-receipt-id", receipt.ID, "--db", databasePath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("record exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result memoryfeedback.RecordResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Feedback.TargetKind != memoryfeedback.TargetProcedure || result.Feedback.TargetRevision != approved.Revision ||
		result.Feedback.Utility != memoryfeedback.UtilityHarmful || result.Feedback.TaskResult != "failed" ||
		result.Feedback.RetrievalReceiptID != receipt.ID {
		t.Fatalf("recorded feedback does not bind the approved procedure: %+v", result)
	}
}

func TestMemoryFeedbackCLIRequiresAbsoluteProjectRootAndTypedOutcome(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"memory", "feedback", "record", "--project-root", ".", "--project-id", "repo-a", "--task-id", "task-a"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "usage: threadforge memory feedback record") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
