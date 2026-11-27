package proceduregraph

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func passingGraph() Graph {
	return Graph{
		SchemaVersion: SchemaVersion,
		ID:            "verify-build", ProjectID: "repo:sample", Title: "Verify a build before handoff",
		Revision: 1, Status: StatusApproved,
		Applicability: Applicability{Paths: []string{"src/**"}},
		Nodes: []Node{
			{ID: "inspect", Kind: NodeTrigger, Instruction: "Inspect the failing build context."},
			{ID: "run-tests", Kind: NodeAction, Instruction: "Run the narrowest relevant test suite."},
			{ID: "confirm", Kind: NodeCheck, Instruction: "Confirm the targeted tests pass before handoff."},
		},
		Edges: []Edge{
			{From: "inspect", To: "run-tests", Relation: RelationLeadsTo},
			{From: "run-tests", To: "confirm", Relation: RelationValidates},
		},
		Evidence: []Evidence{{Origin: "trace", ContentSHA256: strings.Repeat("a", 64), Redacted: true, LocalTraceRef: "trace-local-17"}},
		Validation: &Validation{
			DatasetFingerprint: strings.Repeat("b", 64), HeldOut: true, BaselineRuns: 3,
			BaselineScore: 0.80, CandidateScore: 0.80, NoiseFloor: 0.02,
			CostUnit: "tokens", BaselineCost: 1000, CandidateCost: 1050, MaxCostIncrease: 100, Status: "passed",
		},
		Approval: &Approval{ApprovedBy: "reviewer", ApprovedAt: "2026-09-24T10:00:00Z", Reason: "held-out check passed", PrivacyReviewed: true},
	}
}

func TestValidateApprovedGraph(t *testing.T) {
	if err := Validate(passingGraph()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsUnknownEdgeNode(t *testing.T) {
	g := passingGraph()
	g.Edges = append(g.Edges, Edge{From: "inspect", To: "missing", Relation: RelationLeadsTo})
	err := Validate(g)
	if err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("Validate() error = %v, want unknown node", err)
	}
}

func TestValidateRejectsUnsafeApplicabilityPath(t *testing.T) {
	g := passingGraph()
	g.Applicability.Paths = []string{"../secrets/**"}
	err := Validate(g)
	if err == nil || !strings.Contains(err.Error(), "parent-directory") {
		t.Fatalf("Validate() error = %v, want unsafe path", err)
	}
}

func TestValidateRejectsBacktrackingGlobPattern(t *testing.T) {
	g := passingGraph()
	g.Applicability.Paths = []string{"src/*a*a.go"}
	if err := Validate(g); err == nil || !strings.Contains(err.Error(), "one wildcard per path segment") {
		t.Fatalf("Validate() error = %v, want bounded glob complexity rejection", err)
	}
}

func TestValidateRejectsCredentialMaterialInGuidance(t *testing.T) {
	g := passingGraph()
	g.Nodes[0].Instruction = "Configure api_key=sk-example-secret-value-1234567890 before running."
	if err := Validate(g); err == nil || !strings.Contains(err.Error(), "credential material") {
		t.Fatalf("Validate() error = %v, want credential rejection", err)
	}
}

func TestValidateEnforcesProcedureGraphCollectionBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Graph)
		field  string
	}{
		{
			name: "applicability paths",
			mutate: func(g *Graph) {
				g.Applicability.Paths = make([]string, 65)
				for i := range g.Applicability.Paths {
					g.Applicability.Paths[i] = fmt.Sprintf("src/pkg-%d/**", i)
				}
			},
			field: "applicability.paths",
		},
		{
			name: "applicability modules",
			mutate: func(g *Graph) {
				g.Applicability.Modules = make([]string, 65)
				for i := range g.Applicability.Modules {
					g.Applicability.Modules[i] = fmt.Sprintf("module-%d", i)
				}
			},
			field: "applicability.modules",
		},
		{
			name: "node paths",
			mutate: func(g *Graph) {
				g.Nodes[0].Paths = make([]string, 17)
				for i := range g.Nodes[0].Paths {
					g.Nodes[0].Paths[i] = fmt.Sprintf("src/file-%d.go", i)
				}
			},
			field: "nodes[0].paths",
		},
		{
			name: "evidence",
			mutate: func(g *Graph) {
				g.Evidence = make([]Evidence, 101)
				for i := range g.Evidence {
					g.Evidence[i] = Evidence{Origin: "human", ContentSHA256: strings.Repeat("a", 64), Redacted: true}
				}
			},
			field: "evidence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := passingGraph()
			tt.mutate(&g)
			err := Validate(g)
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("Validate() error = %v, want a bound error for %s", err, tt.field)
			}
		})
	}
}

func TestValidateBoundsExpensiveApplicabilityComparisons(t *testing.T) {
	allowed := passingGraph()
	allowed.Applicability.Paths = []string{"src/pkg0/*.go", "src/pkg1/*.go"}
	allowed.SourceFiles = make([]PathFingerprint, 64)
	for i := range allowed.SourceFiles {
		module := i % 2
		allowed.SourceFiles[i] = PathFingerprint{
			Path:   fmt.Sprintf("src/pkg%d/file-%d.go", module, i),
			SHA256: strings.Repeat("a", 64),
		}
	}
	if err := Validate(allowed); err != nil {
		t.Fatalf("Validate() rejected the 128-comparison boundary: %v", err)
	}

	g := passingGraph()
	g.Applicability.Paths = []string{"src/pkg0/*.go", "src/pkg1/*.go", "src/pkg2/*.go"}
	g.SourceFiles = make([]PathFingerprint, 43)
	for i := range g.SourceFiles {
		module := i % 3
		g.SourceFiles[i] = PathFingerprint{
			Path:   fmt.Sprintf("src/pkg%d/file-%d.go", module, i),
			SHA256: strings.Repeat("a", 64),
		}
	}
	if err := Validate(g); err == nil || !strings.Contains(err.Error(), "wildcard-to-source comparisons") {
		t.Fatalf("Validate() error = %v, want comparison-budget rejection", err)
	}
}

func TestValidateAcceptsBoundedGraphAtCollectionLimits(t *testing.T) {
	g := passingGraph()
	g.Applicability.Paths = make([]string, 64)
	for i := range g.Applicability.Paths {
		g.Applicability.Paths[i] = fmt.Sprintf("src/pkg-%d/**", i)
	}
	g.SourceFiles = make([]PathFingerprint, 500)
	for i := range g.SourceFiles {
		module := i % len(g.Applicability.Paths)
		g.SourceFiles[i] = PathFingerprint{
			Path:   fmt.Sprintf("src/pkg-%d/file-%d.go", module, i),
			SHA256: strings.Repeat("a", 64),
		}
	}
	if err := Validate(g); err != nil {
		t.Fatalf("Validate() rejected a bounded maximum-collection graph: %v", err)
	}
}

func TestValidateEnforcesProcedureGraphSchemaFieldBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Graph)
		field  string
	}{
		{"applicability path length", func(g *Graph) { g.Applicability.Paths = []string{strings.Repeat("a", 257)} }, "applicability.paths[0]"},
		{"source path length", func(g *Graph) {
			g.SourceFiles = []PathFingerprint{{Path: "src/" + strings.Repeat("a", 1021), SHA256: strings.Repeat("a", 64)}}
		}, "source_files[0].path"},
		{"node module length", func(g *Graph) { g.Nodes[0].Module = strings.Repeat("m", 129) }, "nodes[0].module"},
		{"node path length", func(g *Graph) { g.Nodes[0].Paths = []string{strings.Repeat("a", 257)} }, "nodes[0].paths[0]"},
		{"evidence harness length", func(g *Graph) { g.Evidence[0].Harness = strings.Repeat("h", 129) }, "evidence[0].harness"},
		{"evidence timestamp length", func(g *Graph) { g.Evidence[0].ObservedAt = strings.Repeat("t", 65) }, "evidence[0].observed_at"},
		{"approval reviewer length", func(g *Graph) { g.Approval.ApprovedBy = strings.Repeat("r", 129) }, "approval.approved_by"},
		{"approval reason length", func(g *Graph) { g.Approval.Reason = strings.Repeat("r", 1001) }, "approval.reason"},
		{"superseded by identifier", func(g *Graph) { g.SupersededBy = strings.Repeat("x", 65) }, "superseded_by"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := passingGraph()
			tt.mutate(&g)
			err := Validate(g)
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("Validate() error = %v, want a schema-bound error for %s", err, tt.field)
			}
		})
	}
}

func TestValidateRejectsDuplicateProcedureGraphPatterns(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Graph)
		field  string
	}{
		{"applicability modules", func(g *Graph) { g.Applicability.Modules = []string{"core", "core"} }, "applicability.modules[1]"},
		{"applicability paths", func(g *Graph) { g.Applicability.Paths = []string{"src/**", "src/**"} }, "applicability.paths[1]"},
		{"node paths", func(g *Graph) { g.Nodes[0].Paths = []string{"src/main.go", "src/main.go"} }, "nodes[0].paths[1]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := passingGraph()
			tt.mutate(&g)
			err := Validate(g)
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("Validate() error = %v, want duplicate error for %s", err, tt.field)
			}
		})
	}
}

func TestValidateEnforcesMaximumEncodedGraphSize(t *testing.T) {
	g := Graph{
		SchemaVersion: SchemaVersion,
		ID:            "large-graph",
		ProjectID:     "repo:sample",
		Title:         "Large bounded graph",
		Revision:      1,
		Status:        StatusDraft,
		Nodes:         make([]Node, 256),
		Edges:         make([]Edge, 1024),
	}
	for i := range g.Nodes {
		g.Nodes[i] = Node{ID: fmt.Sprintf("node-%03d", i), Kind: NodeAction, Instruction: strings.Repeat("x", 4000)}
	}
	for i := range g.Edges {
		g.Edges[i] = Edge{
			From:      g.Nodes[0].ID,
			To:        g.Nodes[1].ID,
			Relation:  RelationLeadsTo,
			Condition: strings.Repeat("x", 990) + fmt.Sprintf("%010d", i),
		}
	}

	err := Validate(g)
	if err == nil || !strings.Contains(err.Error(), "encoded graph") {
		t.Fatalf("Validate() error = %v, want encoded-size rejection above %d bytes", err, MaxGraphBytes)
	}
}

func TestValidateBindsSourceFingerprintsToApplicablePaths(t *testing.T) {
	g := passingGraph()
	g.SourceFiles = []PathFingerprint{{Path: "src/main.go", SHA256: strings.Repeat("c", 64)}}
	if err := Validate(g); err != nil {
		t.Fatalf("valid source fingerprint was rejected: %v", err)
	}
	g.SourceFiles[0].Path = "docs/other.md"
	if err := Validate(g); err == nil || !strings.Contains(err.Error(), "outside graph applicability") {
		t.Fatalf("Validate() error = %v, want applicability mismatch", err)
	}
}

func TestApprovalRequiresHeldOutGate(t *testing.T) {
	g := passingGraph()
	g.Status = StatusValidated
	g.Approval = nil
	g.Validation.CandidateScore = 0.70
	g.Validation.NoiseFloor = 0.02
	if _, err := Approve(g, "reviewer", "looks good", "2026-09-24T10:00:00Z", true); err == nil {
		t.Fatal("Approve() accepted a regressing candidate")
	}
}

func TestSanitizeForShareRemovesLocalTraceReference(t *testing.T) {
	shared, err := SanitizeForShare(passingGraph())
	if err != nil {
		t.Fatalf("SanitizeForShare() error = %v", err)
	}
	if shared.Evidence[0].LocalTraceRef != "" {
		t.Fatalf("local trace ref leaked: %q", shared.Evidence[0].LocalTraceRef)
	}
	if shared.Evidence[0].ContentSHA256 != strings.Repeat("a", 64) {
		t.Fatal("share projection must retain evidence digest")
	}
}

func TestSanitizeForAgentRemovesLocalTraceReferenceWithoutMutatingGraph(t *testing.T) {
	local := passingGraph()
	agent, err := SanitizeForAgent(local)
	if err != nil {
		t.Fatalf("SanitizeForAgent() error = %v", err)
	}
	if agent.Evidence[0].LocalTraceRef != "" {
		t.Fatalf("agent projection retained local trace reference: %q", agent.Evidence[0].LocalTraceRef)
	}
	if local.Evidence[0].LocalTraceRef != "trace-local-17" {
		t.Fatalf("SanitizeForAgent() mutated stored graph: %#v", local.Evidence[0])
	}
	if agent.Evidence[0].ContentSHA256 != local.Evidence[0].ContentSHA256 {
		t.Fatal("agent projection should retain the redacted evidence digest")
	}
	agent.Evidence[0].LocalTraceRef = "changed-agent-copy"
	if local.Evidence[0].LocalTraceRef != "trace-local-17" {
		t.Fatal("agent projection shares its evidence slice with the local graph")
	}
}

func TestSanitizeForAgentDeepCopiesNestedGraphData(t *testing.T) {
	local := passingGraph()
	local.Nodes[0].Paths = []string{"src/main.go"}
	agent, err := SanitizeForAgent(local)
	if err != nil {
		t.Fatalf("SanitizeForAgent() error = %v", err)
	}
	agent.Nodes[0].Paths[0] = "changed/path.go"
	agent.Validation.BaselineRuns = 99
	agent.Approval.Reason = "changed projection"
	if local.Nodes[0].Paths[0] != "src/main.go" || local.Validation.BaselineRuns != 3 || local.Approval.Reason != "held-out check passed" {
		t.Fatal("agent projection shares nested or pointer-backed data with the stored graph")
	}
}

func TestSanitizeForShareRejectsUnredactedEvidence(t *testing.T) {
	g := passingGraph()
	g.Evidence[0].Redacted = false
	if _, err := SanitizeForShare(g); err == nil {
		t.Fatal("SanitizeForShare() accepted unredacted evidence")
	}
}

func TestBoundedNeighborhoodIsDeterministicAndStopsAtLimit(t *testing.T) {
	g := passingGraph()
	result, err := BoundedNeighborhood(g, []string{"inspect"}, 4, 2)
	if err != nil {
		t.Fatalf("BoundedNeighborhood() error = %v", err)
	}
	if !result.Truncated || len(result.Nodes) != 2 {
		t.Fatalf("result = %#v, want 2 nodes and truncated", result)
	}
	if result.Nodes[0].ID != "inspect" || result.Nodes[1].ID != "run-tests" {
		t.Fatalf("node order = %#v", result.Nodes)
	}
}

func TestBoundedNeighborhoodDeepCopiesNodePaths(t *testing.T) {
	g := passingGraph()
	g.Nodes[0].Paths = []string{"src/main.go"}
	result, err := BoundedNeighborhood(g, []string{"inspect"}, 0, 1)
	if err != nil {
		t.Fatalf("BoundedNeighborhood() error = %v", err)
	}
	result.Nodes[0].Paths[0] = "changed/path.go"
	if g.Nodes[0].Paths[0] != "src/main.go" {
		t.Fatal("bounded neighborhood shares nested node paths with the source graph")
	}
}

func TestBoundedNeighborhoodRejectsUnapprovedGraph(t *testing.T) {
	g := passingGraph()
	g.Status = StatusDraft
	g.Approval = nil
	if _, err := BoundedNeighborhood(g, []string{"inspect"}, 2, 8); err == nil {
		t.Fatal("draft graph was retrieved")
	}
}

func TestApplicableToPath(t *testing.T) {
	g := passingGraph()
	if !ApplicableToPath(g, `src\pkg\main.go`) {
		t.Fatal("expected Windows path to normalize and match src/**")
	}
	if ApplicableToPath(g, "docs/readme.md") {
		t.Fatal("unrelated path matched")
	}
}

func FuzzProcedureGraphValidationAndProjection(f *testing.F) {
	valid, err := json.Marshal(passingGraph())
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{
		valid,
		[]byte(`{}`),
		[]byte(`{"schema_version":"threadforge.procedure-graph.v1","status":"approved","nodes":[{"id":"step","kind":"action","instruction":"run safely"}],"edges":[{"from":"step","to":"step","relation":"loops_to"}]}`),
		[]byte(`{"schema_version":"threadforge.procedure-graph.v1","nodes":[{"id":"step","kind":"action","instruction":"api_key=sk-example-secret-material-123456"}]}`),
		[]byte(`{"schema_version":"threadforge.procedure-graph.v1","nodes":[{"id":"step","kind":"action","instruction":"ok"}],"source_files":[{"path":"../secret.pem","sha256":"bad"}]}`),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64*1024 {
			t.Skip()
		}

		var graph Graph
		if err := json.Unmarshal(data, &graph); err != nil {
			return
		}
		if err := Validate(graph); err != nil {
			return
		}

		projected, err := SanitizeForAgent(graph)
		if err != nil {
			t.Fatalf("valid graph failed agent projection: %v", err)
		}
		for i := range graph.Evidence {
			if graph.Evidence[i].LocalTraceRef != "" && projected.Evidence[i].LocalTraceRef != "" {
				t.Fatal("agent projection retained a local trace reference")
			}
		}
		if graph.Status != StatusApproved {
			return
		}

		neighborhood, err := BoundedNeighborhood(graph, []string{graph.Nodes[0].ID}, 8, 256)
		if err != nil {
			t.Fatalf("valid approved graph failed bounded traversal: %v", err)
		}
		if len(neighborhood.Nodes) > 256 {
			t.Fatalf("bounded traversal returned %d nodes", len(neighborhood.Nodes))
		}
	})
}
