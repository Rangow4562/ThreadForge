package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/evolution"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

type evolutionRepairRequest struct {
	SchemaVersion       string                       `json:"schema_version"`
	Domain              string                       `json:"domain"`
	Candidate           evolution.RepairCandidate    `json:"candidate"`
	MaxRepairs          *int                         `json:"max_repairs,omitempty"`
	DeniedLiterals     []evolutionDenyLiteral       `json:"denied_literals,omitempty"`
	BenchmarkInventories []evolutionBenchmarkInventory `json:"benchmark_inventories,omitempty"`
	Backend             localHFBackend               `json:"backend"`
}

type localHFBackend struct {
	Type      string `json:"type"`
	ModelPath string `json:"model_path"`
	Device    string `json:"device,omitempty"`
	DType     string `json:"dtype,omitempty"`
}

type evolutionRepairRunSummary struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	Domain       string `json:"domain"`
	CreatedAt    string `json:"created_at"`
	Status       string `json:"status"`
	AttemptCount int    `json:"attempt_count"`
	RepairCount  int    `json:"repair_count"`
}

func runEvolutionRepair(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution repair", stderr)
	file := fs.String("file", "", "versioned local RRSI repair request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned local-only repair module")
	recordID := fs.String("record-id", "", "append this repair result to local history with this stable ID")
	projectID := fs.String("project-id", "", "local project ID required with --record-id")
	actor := fs.String("actor", "", "local audit actor required with --record-id")
	reason := fs.String("reason", "", "audit reason required with --record-id")
	database := fs.String("db", "", "Threadforge SQLite database path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution repair --file <repair-request.json> [--python python] [--record-id <id> --project-id <id> --actor <name> --reason <text> [--db <path>]]")
		return 2
	}
	input, err := readBoundedText(*file, 768<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing repair request rejected before inference")
		return 1
	}
	var request evolutionRepairRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		(request.SchemaVersion != "threadforge.evolution-repair-request.v1" && request.SchemaVersion != "threadforge.evolution-repair-request.v2") ||
		strings.TrimSpace(request.Domain) == "" || strings.TrimSpace(request.Candidate.Summary) == "" ||
		strings.TrimSpace(request.Candidate.TargetsMode) == "" || strings.TrimSpace(request.Candidate.Diff) == "" ||
		len(request.Candidate.Edits) == 0 || request.Backend.Type != "hf" || strings.TrimSpace(request.Backend.ModelPath) == "" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned evolution repair request")
		return 1
	}
	inventoryAware := request.SchemaVersion == "threadforge.evolution-repair-request.v2"
	if !inventoryAware && request.BenchmarkInventories != nil {
		_, _ = fmt.Fprintln(stderr, "benchmark inventories require repair request v2")
		return 1
	}
	if !inventoryAware && len(input) > 256<<10 {
		_, _ = fmt.Fprintln(stderr, "legacy repair request exceeds its size limit")
		return 1
	}
	if request.MaxRepairs != nil && (*request.MaxRepairs < 0 || *request.MaxRepairs > 2) {
		_, _ = fmt.Fprintln(stderr, "max_repairs must be an integer from 0 to 2")
		return 1
	}
	deniedLiterals := request.DeniedLiterals
	if deniedLiterals == nil {
		deniedLiterals = []evolutionDenyLiteral{}
	}
	params := map[string]any{
		"domain": request.Domain, "candidate": request.Candidate, "backend": request.Backend,
		"denied_literals": deniedLiterals,
	}
	operation := "evolution.repair"
	maxParams := 240 << 10
	if inventoryAware {
		operation = "evolution.repair.v2"
		maxParams = 768 << 10
		if request.BenchmarkInventories == nil {
			request.BenchmarkInventories = []evolutionBenchmarkInventory{}
		}
		params["benchmark_inventories"] = request.BenchmarkInventories
	}
	if request.MaxRepairs != nil {
		params["max_repairs"] = *request.MaxRepairs
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil || len(paramsJSON) > maxParams || privacy.ContainsCredential(paramsJSON) {
		_, _ = fmt.Fprintln(stderr, "repair request is invalid or credential-bearing")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-repair", operation, paramsJSON)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "local evolution repair failed safely")
		return 1
	}
	var result evolution.RepairResult
	if err := decodeJSONBytes(response.Result, &result); err != nil || evolution.ValidateRepairResult(result, request.Domain) != nil ||
		request.MaxRepairs != nil && result.RepairCount > *request.MaxRepairs {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe evolution repair response")
		return 1
	}
	recording := strings.TrimSpace(*recordID) != ""
	if !recording && (strings.TrimSpace(*projectID) != "" || strings.TrimSpace(*actor) != "" || strings.TrimSpace(*reason) != "" || strings.TrimSpace(*database) != "") {
		_, _ = fmt.Fprintln(stderr, "--project-id, --actor, --reason, and --db require --record-id")
		return 2
	}
	if recording {
		if strings.TrimSpace(*projectID) == "" || strings.TrimSpace(*actor) == "" || strings.TrimSpace(*reason) == "" {
			_, _ = fmt.Fprintln(stderr, "--record-id requires --project-id, --actor, and --reason")
			return 2
		}
		paths, err := appdata.Resolve(*database, "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "could not resolve local Threadforge database")
			return 1
		}
		databaseStore, err := store.Open(paths.SQLite)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "could not open local Threadforge database")
			return 1
		}
		defer databaseStore.Close()
		run := evolution.RepairRun{
			ID: *recordID, ProjectID: *projectID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Actor: *actor, Reason: *reason, Result: result,
		}
		if err := databaseStore.RecordEvolutionRepairRun(ctx, run); err != nil {
			_, _ = fmt.Fprintf(stderr, "could not record evolution repair history: %v\n", err)
			return 1
		}
		return writeIndentedJSON(stdout, map[string]any{"result": result, "recorded_run_id": run.ID, "project_id": run.ProjectID}, stderr)
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func runEvolutionRepairHistory(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution repair-history", stderr)
	projectID := fs.String("project-id", "", "local Threadforge project ID")
	database := fs.String("db", "", "Threadforge SQLite database path")
	limit := fs.Int("limit", 50, "maximum repair runs to return (1-200)")
	offset := fs.Int("offset", 0, "repair run offset")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*projectID) == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution repair-history --project-id <id> [--limit 50] [--offset 0] [--db <path>]")
		return 2
	}
	paths, err := appdata.Resolve(*database, "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not resolve local Threadforge database")
		return 1
	}
	databaseStore, err := store.Open(paths.SQLite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not open local Threadforge database")
		return 1
	}
	defer databaseStore.Close()
	runs, total, err := databaseStore.ListEvolutionRepairRuns(context.Background(), *projectID, *limit, *offset)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	items := make([]evolutionRepairRunSummary, 0, len(runs))
	for _, run := range runs {
		items = append(items, evolutionRepairRunSummary{
			ID: run.ID, ProjectID: run.ProjectID, Domain: run.Result.Domain, CreatedAt: run.CreatedAt,
			Status: run.Result.Status, AttemptCount: run.Result.AttemptCount, RepairCount: run.Result.RepairCount,
		})
	}
	return writeIndentedJSON(stdout, map[string]any{"runs": items, "total": total, "limit": *limit, "offset": *offset}, stderr)
}

func runEvolutionRepairShow(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution repair-show", stderr)
	id := fs.String("id", "", "stable repair run ID")
	database := fs.String("db", "", "Threadforge SQLite database path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*id) == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution repair-show --id <run-id> [--db <path>]")
		return 2
	}
	paths, err := appdata.Resolve(*database, "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not resolve local Threadforge database")
		return 1
	}
	databaseStore, err := store.Open(paths.SQLite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not open local Threadforge database")
		return 1
	}
	defer databaseStore.Close()
	run, err := databaseStore.GetEvolutionRepairRun(context.Background(), *id)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	audit, err := databaseStore.EvolutionRepairHistory(context.Background(), *id)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, map[string]any{"run": run, "audit": audit}, stderr)
}
