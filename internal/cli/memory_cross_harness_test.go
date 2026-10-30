package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/mcp"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
)

func TestMemoryLearningCrossHarnessMCPRetrieval(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, "project")
	sourcePath := filepath.Join(projectRoot, "src", "agent.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("package agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	const privatePrompt = "CROSS-HARNESS-PRIVATE-PROMPT-MUST-NOT-LEAK"
	inputPath := filepath.Join(root, "codex-events.jsonl")
	var trace bytes.Buffer
	encoder := json.NewEncoder(&trace)
	for _, action := range []string{"file.edited", "test.failed"} {
		event := beacon.NewEvent(beacon.NewEventOptions{
			Action: action, Harness: beacon.HarnessInfo{Name: "codex"},
			Message: "A focused module change requires validation.",
		})
		event.File = &beacon.FileInfo{Path: sourcePath}
		event.Prompt = &beacon.PromptInfo{Text: privatePrompt}
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(inputPath, trace.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "threadforge.sqlite")
	modelResponse, err := json.Marshal(map[string]any{
		"schema_version": "threadforge.python.v1", "request_id": "memory-learn", "status": "ok",
		"result": map[string]any{
			"status": "candidate", "reason": "",
			"candidate": map[string]any{
				"schema_version": "threadforge.learning-candidate.v1",
				"title":          "Validate the changed module",
				"applicability":  map[string]any{"paths": []string{"src/**"}, "modules": []string{}},
				"steps":          []map[string]string{{"kind": "action", "instruction": "Run the focused module test."}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := func(ctx context.Context, python string, requestBytes []byte) ([]byte, error) {
		if python != "fixture-python" {
			return nil, fmt.Errorf("unexpected Python command %q", python)
		}
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("learning inference request omitted its timeout")
		}
		if bytes.Contains(requestBytes, []byte(projectRoot)) || bytes.Contains(requestBytes, []byte(sourcePath)) ||
			bytes.Contains(requestBytes, []byte(privatePrompt)) {
			return nil, errors.New("local path or raw prompt leaked into learning inference")
		}
		var request struct {
			SchemaVersion string `json:"schema_version"`
			RequestID     string `json:"request_id"`
			Operation     string `json:"operation"`
			Params        struct {
				Observations []map[string]string `json:"observations"`
				Backend      struct {
					Type string `json:"type"`
				} `json:"backend"`
			} `json:"params"`
		}
		if err := json.Unmarshal(requestBytes, &request); err != nil || request.SchemaVersion != "threadforge.python.v1" ||
			request.RequestID != "memory-learn" || request.Operation != "memory.learn" ||
			len(request.Params.Observations) != 2 || request.Params.Backend.Type != "hf" {
			return nil, errors.New("memory learning did not send the bounded local-model request")
		}
		for _, observation := range request.Params.Observations {
			if observation["harness"] != "codex" ||
				observation["summary"] != "A focused module change requires validation." {
				return nil, errors.New("Codex observations were not normalized for learning")
			}
		}
		return modelResponse, nil
	}

	var learnOut, learnErr bytes.Buffer
	if code := learnMemoryWithRunner([]string{
		"--input", inputPath, "--project-id", "repo:cross-harness", "--project-root", projectRoot,
		"--model-path", "fixture-model", "--python", "fixture-python", "--db", databasePath,
	}, &learnOut, &learnErr, runner); code != 0 {
		t.Fatalf("Codex memory learn failed: code=%d stdout=%q stderr=%q", code, learnOut.String(), learnErr.String())
	}
	var learned struct {
		Status                    string               `json:"status"`
		Graph                     proceduregraph.Graph `json:"graph"`
		HeldOutEvaluationRequired bool                 `json:"held_out_evaluation_required"`
		HumanApprovalRequired     bool                 `json:"human_approval_required"`
		ActionReady               bool                 `json:"action_ready"`
	}
	if err := json.Unmarshal(learnOut.Bytes(), &learned); err != nil || learned.Status != "proposed" ||
		learned.Graph.Status != proceduregraph.StatusDraft || learned.Graph.ProjectID != "repo:cross-harness" ||
		len(learned.Graph.SourceFiles) != 1 || learned.Graph.SourceFiles[0].Path != "src/agent.go" ||
		len(learned.Graph.Evidence) != 1 || learned.Graph.Evidence[0].Harness != "codex" ||
		!learned.HeldOutEvaluationRequired || !learned.HumanApprovalRequired || learned.ActionReady ||
		strings.Contains(learnOut.String(), privatePrompt) || strings.Contains(learnOut.String(), projectRoot) {
		t.Fatalf("Codex learning did not produce a private, source-bound draft: err=%v result=%#v stdout=%q stderr=%q",
			err, learned, learnOut.String(), learnErr.String())
	}

	// This deterministic held-out fixture exercises the approval lifecycle only;
	// it is not benchmark evidence and cannot establish efficacy or cutover.
	validation := proceduregraph.Validation{
		DatasetFingerprint: strings.Repeat("a", 64), HeldOut: true, BaselineRuns: 8,
		BaselineScore: 0.70, CandidateScore: 0.80, NoiseFloor: 0.02,
		CostUnit: "tokens", BaselineCost: 100, CandidateCost: 105, MaxCostIncrease: 10, Status: "passed",
	}
	validationPath := filepath.Join(root, "held-out-fixture.json")
	validationBytes, err := json.Marshal(validation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validationPath, validationBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	var validationOut, validationErr bytes.Buffer
	if code := recordProcedureValidation([]string{
		"--id", learned.Graph.ID, "--expected-revision", "1", "--report", validationPath, "--db", databasePath,
	}, &validationOut, &validationErr); code != 0 {
		t.Fatalf("record fixture validation failed: code=%d stdout=%q stderr=%q", code, validationOut.String(), validationErr.String())
	}
	var validated proceduregraph.Graph
	if err := json.Unmarshal(validationOut.Bytes(), &validated); err != nil || validated.Status != proceduregraph.StatusValidated ||
		validated.Revision != 2 || validated.Validation == nil || !validated.Validation.PassesGate() {
		t.Fatalf("fixture validation did not enter the validated lifecycle state: err=%v graph=%#v", err, validated)
	}
	var approvalOut, approvalErr bytes.Buffer
	if code := approveProcedure([]string{
		"--id", validated.ID, "--expected-revision", "2", "--reviewer", "fixture-human-reviewer",
		"--reason", "reviewed cross-harness fixture and held-out lifecycle contract", "--privacy-reviewed", "--db", databasePath,
	}, &approvalOut, &approvalErr); code != 0 {
		t.Fatalf("explicit procedure approval failed: code=%d stdout=%q stderr=%q", code, approvalOut.String(), approvalErr.String())
	}
	var approved proceduregraph.Graph
	if err := json.Unmarshal(approvalOut.Bytes(), &approved); err != nil || approved.Status != proceduregraph.StatusApproved ||
		approved.Revision != 3 || approved.Approval == nil || !approved.Approval.PrivacyReviewed {
		t.Fatalf("procedure approval was not persisted: err=%v graph=%#v", err, approved)
	}

	procedures, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer procedures.Close()
	server := mcp.NewWithProjectStores(procedures, nil, nil, projectRoot)
	var mcpInput bytes.Buffer
	mcpEncoder := json.NewEncoder(&mcpInput)
	requests := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
			"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
			"clientInfo": map[string]string{"name": "opencode", "version": "fixture"},
		}},
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name": "threadforge_context_get", "arguments": map[string]any{
				"project_id": "repo:cross-harness", "query": "focused module test",
				"paths": []string{"src/agent.go"}, "max_items": 8, "max_tokens": 1600,
			},
		}},
	}
	for _, request := range requests {
		if err := mcpEncoder.Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var mcpOutput bytes.Buffer
	if err := server.ServeStdio(context.Background(), &mcpInput, &mcpOutput); err != nil {
		t.Fatalf("OpenCode MCP client exchange failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(mcpOutput.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected MCP response count: %d output=%q", len(lines), mcpOutput.String())
	}
	var toolResponse struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &toolResponse); err != nil || toolResponse.Error != nil ||
		toolResponse.Result.IsError || len(toolResponse.Result.Content) != 1 || toolResponse.Result.Content[0].Type != "text" {
		t.Fatalf("OpenCode MCP retrieval returned an error: err=%v response=%q", err, lines[1])
	}
	var contextResponse struct {
		SchemaVersion string `json:"schema_version"`
		ActionReady   bool   `json:"action_ready"`
		Procedures    []struct {
			ID        string   `json:"id"`
			Revision  int      `json:"revision"`
			Title     string   `json:"title"`
			Freshness string   `json:"freshness"`
			Guidance  []string `json:"guidance"`
		} `json:"procedures"`
	}
	contextText := toolResponse.Result.Content[0].Text
	if err := json.Unmarshal([]byte(contextText), &contextResponse); err != nil ||
		contextResponse.SchemaVersion != "threadforge.context-response.v2" || contextResponse.ActionReady ||
		len(contextResponse.Procedures) != 1 {
		t.Fatalf("OpenCode did not receive one advisory context result: err=%v response=%q", err, contextText)
	}
	item := contextResponse.Procedures[0]
	joinedGuidance := strings.Join(item.Guidance, "\n")
	if item.ID != approved.ID || item.Revision != approved.Revision || item.Title != "Validate the changed module" ||
		item.Freshness != "verified" || !strings.Contains(joinedGuidance, "Run the focused module test.") ||
		strings.Contains(contextText, privatePrompt) || strings.Contains(contextText, sourcePath) {
		t.Fatalf("OpenCode received the wrong, stale, or privacy-unsafe procedure: %#v response=%q", item, contextText)
	}
}
