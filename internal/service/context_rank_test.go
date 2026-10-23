package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/internal/projectfiles"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

func TestContextRelevanceScoreWeightsFieldsAndDeduplicatesQueryTerms(t *testing.T) {
	titleScore, titleMatches := contextRelevanceScore(
		"build migration procedure", "Build migration procedure", "", "",
	)
	bodyScore, bodyMatches := contextRelevanceScore(
		"build migration procedure", "General notes", "", "build build build",
	)
	if !titleMatches || !bodyMatches || titleScore <= bodyScore {
		t.Fatalf("title/body relevance = (%d,%t)/(%d,%t), want title first", titleScore, titleMatches, bodyScore, bodyMatches)
	}
	first, _ := contextRelevanceScore("build other", "", "", "build")
	repeated, _ := contextRelevanceScore("build build other", "", "", "build")
	if first != repeated {
		t.Fatalf("repeated query terms changed relevance: single=%d repeated=%d", first, repeated)
	}
	if score, matched := contextRelevanceScore("*", "", "", ""); !matched || score != 0 {
		t.Fatalf("project-wide wildcard semantics changed: score=%d matched=%t", score, matched)
	}
	if _, matched := contextRelevanceScore("nonexistent", "Build", "", "compile"); matched {
		t.Fatal("unmatched query unexpectedly matched context")
	}
}

func TestRankProcedureGraphsUsesStableIDOrderForWildcard(t *testing.T) {
	graphs := []proceduregraph.Graph{
		{ID: "z-last", Status: proceduregraph.StatusApproved, Title: "Build notes"},
		{ID: "a-first", Status: proceduregraph.StatusApproved, Title: "Deploy notes"},
		{ID: "draft", Status: proceduregraph.StatusDraft, Title: "Build migration procedure"},
	}
	got := rankProcedureGraphs(graphs, "*")
	if len(got) != 2 || got[0].ID != "a-first" || got[1].ID != "z-last" {
		t.Fatalf("wildcard ordering or status filter = %#v", got)
	}
}

func TestContextPrioritizesProcedureTitleMatchesBeforeIncidentalTerms(t *testing.T) {
	handler, database, _ := testHandler(t)
	addRankedProcedure(t, database, "a-incidental", "Project helper", "Use the build checks while handling an unrelated project task.")
	addRankedProcedure(t, database, "z-relevant", "Build migration procedure", "Verify the migration before publishing the build.")

	body := `{"schema_version":"threadforge.context-request.v1","project_id":"demo","query":"build migration procedure","paths":["pkg/main.go"],"max_items":1,"max_tokens":1000}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, localRequest(http.MethodPost, "/v1/context", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("context request returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var response ContextResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Procedures) != 1 || response.Procedures[0].ID != "z-relevant" {
		t.Fatalf("context budget selected weak lexical hit instead of strongest procedure: %#v", response.Procedures)
	}
}

func TestContextFiltersProcedureModulesAndReportsMissingModuleContext(t *testing.T) {
	handler, database, _ := testHandler(t)
	addRankedProcedure(t, database, "api-procedure", "Build API procedure", "Check API behavior before changes.", "api")
	addRankedProcedure(t, database, "worker-procedure", "Build worker procedure", "Check worker behavior before changes.", "worker")

	read := func(body string) ContextResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, localRequest(http.MethodPost, "/v2/context", strings.NewReader(body)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("context request returned %d: %s", recorder.Code, recorder.Body.String())
		}
		var response ContextResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	matched := read(`{"schema_version":"threadforge.context-request.v2","project_id":"demo","query":"build procedure","paths":["pkg/main.go"],"modules":["api"],"max_items":4,"max_tokens":1000}`)
	if len(matched.Procedures) != 1 || matched.Procedures[0].ID != "api-procedure" {
		t.Fatalf("procedure outside the requested module was returned: %#v", matched.Procedures)
	}

	unverified := read(`{"schema_version":"threadforge.context-request.v2","project_id":"demo","query":"build procedure","paths":["pkg/main.go"],"max_items":4,"max_tokens":1000}`)
	if len(unverified.Procedures) != 0 || !strings.Contains(strings.Join(unverified.Gaps, " "), "module context is missing") {
		t.Fatalf("module-scoped procedures were returned without module context: %#v", unverified)
	}
}

func TestContextPrioritizesProjectMemoryTitleMatchesBeforeBodyIncidentalTerms(t *testing.T) {
	_, database, traces := testHandler(t)
	memoryStore, err := projectfiles.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeApprovedMemory(t, memoryStore, projectmemory.Candidate{
		SchemaVersion: projectmemory.SchemaVersion, ID: "a-incidental", ProjectID: "demo",
		Kind: projectmemory.KindFact, Title: "Old build note",
		Content: []byte(`{"note":"The migration should be staged carefully."}`),
	})
	writeApprovedMemory(t, memoryStore, projectmemory.Candidate{
		SchemaVersion: projectmemory.SchemaVersion, ID: "z-relevant", ProjectID: "demo",
		Kind: projectmemory.KindDecision, Title: "Build migration procedure",
		Content: []byte(`{"decision":"Verify the schema before publishing."}`),
	})
	handler := NewHandlerWithProjectMemory(database, traces, memoryStore)
	body := `{"schema_version":"threadforge.context-request.v2","project_id":"demo","query":"build migration procedure","max_items":1,"max_tokens":1000}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, localRequest(http.MethodPost, "/v2/context", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("context request returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var response ContextResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Memory) != 1 || response.Memory[0].ID != "z-relevant" {
		t.Fatalf("context budget selected weak memory hit instead of strongest title match: %#v", response.Memory)
	}
}

func addRankedProcedure(t *testing.T, database *store.Store, id, title, instruction string, modules ...string) {
	t.Helper()
	now := time.Now().UTC()
	applicability := proceduregraph.Applicability{Paths: []string{"pkg/**"}, Modules: modules}
	graph := proceduregraph.Graph{
		SchemaVersion: proceduregraph.SchemaVersion,
		ID:            id, ProjectID: "demo", Title: title, Revision: 1,
		Status:        proceduregraph.StatusDraft,
		Applicability: applicability,
		Nodes:         []proceduregraph.Node{{ID: "check", Kind: proceduregraph.NodeAction, Instruction: instruction}},
	}
	if err := database.CreateProposal(context.Background(), graph, "tester", "relevance ranking fixture", now); err != nil {
		t.Fatal(err)
	}
	report := proceduregraph.Validation{
		DatasetFingerprint: strings.Repeat("a", 64), HeldOut: true, BaselineRuns: 5,
		BaselineScore: .7, CandidateScore: .72, NoiseFloor: .02,
		CostUnit: "tokens", BaselineCost: 100, CandidateCost: 105, MaxCostIncrease: 10, Status: "passed",
	}
	validated, err := database.RecordValidation(context.Background(), id, graph.Revision, report, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Approve(context.Background(), id, validated.Revision, "reviewer", "reviewed ranking fixture", true, now); err != nil {
		t.Fatal(err)
	}
}
