package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/procedurefiles"
	"github.com/threadforge-ai/threadforge/internal/projectfiles"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/memoryfeedback"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

func runMemoryFeedback(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge memory feedback <record|list|summary|receipts>")
		return 2
	}
	switch args[0] {
	case "record":
		return recordMemoryFeedback(args[1:], stdout, stderr)
	case "list":
		return listMemoryFeedback(args[1:], stdout, stderr)
	case "summary":
		return summarizeMemoryFeedback(args[1:], stdout, stderr)
	case "receipts":
		return listMemoryRetrievalReceipts(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown memory feedback command %q\n", args[0])
		return 2
	}
}

func listMemoryRetrievalReceipts(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory feedback receipts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := fs.String("project-id", "", "exact project scope to list")
	taskID := fs.String("task-id", "", "exact task scope to list")
	targetID := fs.String("target-id", "", "optional memory identifier filter")
	limit := fs.Int("limit", 50, "maximum content-free receipts (1-500)")
	offset := fs.Int("offset", 0, "number of newest receipts to skip")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	filter := memoryfeedback.RetrievalReceiptFilter{
		ProjectID: *projectID, TaskID: *taskID, TargetID: *targetID, Limit: *limit, Offset: *offset,
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "memory feedback receipts does not accept positional arguments")
		return 2
	}
	if err := memoryfeedback.ValidateRetrievalReceiptFilter(filter); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	items, total, err := databaseStore.ListMemoryRetrievalReceipts(context.Background(), filter)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	result := memoryfeedback.RetrievalReceiptListResult{
		SchemaVersion: memoryfeedback.RetrievalReceiptListVersion,
		ProjectID:     filter.ProjectID, TaskID: filter.TaskID, Total: total,
		Limit: filter.Limit, Offset: filter.Offset, Receipts: items,
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func recordMemoryFeedback(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory feedback record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectRoot := fs.String("project-root", "", "project root containing the approved memory revision")
	projectID := fs.String("project-id", "", "exact project scope shared with the task and memory")
	taskID := fs.String("task-id", "", "existing Threadforge task associated with this report")
	sessionID := fs.String("session-id", "", "optional session belonging to the task")
	retrievalReceiptID := fs.String("retrieval-receipt-id", "", "optional local MCP handler-result receipt returned with memory guidance")
	targetKind := fs.String("target-kind", "", "approved memory kind: procedure, fact, decision, or policy")
	targetID := fs.String("target-id", "", "stable identifier of the approved memory revision")
	targetRevision := fs.Int("target-revision", 0, "exact approved memory revision referenced by the report")
	exposure := fs.String("exposure", "", "reported exposure: used or shown_not_used")
	utility := fs.String("utility", "", "reported utility: helpful, neutral, harmful, or not_assessed")
	taskResult := fs.String("task-result", "", "self-reported result: passed, failed, or inconclusive")
	harness := fs.String("harness", "", "lowercase harness identifier, for example codex-cli")
	actor := fs.String("actor", "local-user", "local reporter label; not identity proof")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !filepath.IsAbs(*projectRoot) || strings.TrimSpace(*projectID) == "" || strings.TrimSpace(*taskID) == "" ||
		strings.TrimSpace(*targetKind) == "" || strings.TrimSpace(*targetID) == "" || *targetRevision < 1 ||
		strings.TrimSpace(*exposure) == "" || strings.TrimSpace(*utility) == "" || strings.TrimSpace(*taskResult) == "" ||
		strings.TrimSpace(*harness) == "" || strings.TrimSpace(*actor) == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge memory feedback record --project-root <absolute-root> --project-id <id> --task-id <id> [--session-id <id>] [--retrieval-receipt-id <id>] --target-kind <procedure|fact|decision|policy> --target-id <id> --target-revision <n> --exposure <used|shown_not_used> --utility <helpful|neutral|harmful|not_assessed> --task-result <passed|failed|inconclusive> --harness <id> [--actor local-user] [--db <path>]")
		return 2
	}
	feedbackID, err := newMemoryFeedbackID()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	feedback := memoryfeedback.Feedback{
		SchemaVersion: memoryfeedback.SchemaVersion,
		ID:            feedbackID, ProjectID: *projectID, TaskID: *taskID, SessionID: *sessionID,
		RetrievalReceiptID: *retrievalReceiptID,
		TargetKind:         memoryfeedback.TargetKind(*targetKind), TargetID: *targetID, TargetRevision: *targetRevision,
		Exposure: memoryfeedback.Exposure(*exposure), Utility: memoryfeedback.Utility(*utility), TaskResult: *taskResult,
		Harness: *harness, Actor: *actor, EvidenceClass: memoryfeedback.EvidenceSelfReported,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := memoryfeedback.Validate(feedback); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	if err := verifyMemoryFeedbackTarget(context.Background(), databaseStore, *projectRoot, feedback); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	sequence, err := databaseStore.RecordMemoryFeedback(context.Background(), feedback)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	result := memoryfeedback.RecordResult{
		SchemaVersion: memoryfeedback.RecordResultVersion,
		Status:        "recorded", Sequence: sequence, Feedback: feedback,
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func verifyMemoryFeedbackTarget(ctx context.Context, databaseStore *store.Store, projectRoot string, feedback memoryfeedback.Feedback) error {
	switch feedback.TargetKind {
	case memoryfeedback.TargetProcedure:
		graph, err := databaseStore.Get(ctx, feedback.TargetID)
		if err == nil {
			if graph.ProjectID != feedback.ProjectID || graph.Revision != feedback.TargetRevision || graph.Status != proceduregraph.StatusApproved {
				return errors.New("target procedure is not the requested approved revision in this project")
			}
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("read local procedure target: %w", err)
		}
		procedures, err := procedurefiles.New(projectRoot)
		if err != nil {
			return err
		}
		graph, err = procedures.GetLatest(feedback.TargetID)
		if err != nil {
			return fmt.Errorf("read approved project procedure target: %w", err)
		}
		if graph.ProjectID != feedback.ProjectID || graph.Revision != feedback.TargetRevision || graph.Status != proceduregraph.StatusApproved {
			return errors.New("target procedure is not the requested approved revision in this project")
		}
		return nil
	case memoryfeedback.TargetFact, memoryfeedback.TargetDecision, memoryfeedback.TargetPolicy:
		memories, err := projectfiles.New(projectRoot)
		if err != nil {
			return err
		}
		record, err := memories.GetLatest(projectmemory.Kind(feedback.TargetKind), feedback.TargetID)
		if err != nil {
			return fmt.Errorf("read approved project-memory target: %w", err)
		}
		if record.ProjectID != feedback.ProjectID || record.Revision != feedback.TargetRevision || record.Status != "approved" {
			return errors.New("target project memory is not the requested approved revision in this project")
		}
		return nil
	default:
		return errors.New("unsupported memory target kind")
	}
}

func listMemoryFeedback(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory feedback list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := fs.String("project-id", "", "exact project scope to list")
	taskID := fs.String("task-id", "", "optional task filter")
	targetKind := fs.String("target-kind", "", "optional memory kind filter")
	targetID := fs.String("target-id", "", "optional memory identifier filter")
	harness := fs.String("harness", "", "optional harness identifier filter")
	limit := fs.Int("limit", 50, "maximum feedback records (1-500)")
	offset := fs.Int("offset", 0, "number of newest records to skip")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	filter := memoryfeedback.Filter{
		ProjectID: *projectID, TaskID: *taskID, TargetKind: memoryfeedback.TargetKind(*targetKind),
		TargetID: *targetID, Harness: *harness, Limit: *limit, Offset: *offset,
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "memory feedback list does not accept positional arguments")
		return 2
	}
	if err := memoryfeedback.ValidateFilter(filter); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	items, total, err := databaseStore.ListMemoryFeedback(context.Background(), filter)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	result := memoryfeedback.ListResult{
		SchemaVersion: memoryfeedback.ListResultVersion, ProjectID: filter.ProjectID,
		Total: total, Limit: filter.Limit, Offset: filter.Offset, Feedback: items,
		EvidenceClass: memoryfeedback.EvidenceSelfReported,
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func summarizeMemoryFeedback(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory feedback summary", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := fs.String("project-id", "", "exact project scope to summarize")
	maxRecords := fs.Int("max-records", memoryfeedback.MaxSummaryRecords, "maximum newest feedback records to aggregate (1-10000)")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*projectID) == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge memory feedback summary --project-id <id> [--max-records 10000] [--db <path>]")
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	result, err := databaseStore.SummarizeMemoryFeedback(context.Background(), *projectID, *maxRecords)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func newMemoryFeedbackID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate memory feedback id: %w", err)
	}
	return "memory-feedback-" + hex.EncodeToString(random[:]), nil
}
