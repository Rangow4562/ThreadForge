package proceduregraph

import (
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

func TestValidateRejectsCredentialMaterialInGuidance(t *testing.T) {
	g := passingGraph()
	g.Nodes[0].Instruction = "Configure api_key=sk-example-secret-value-1234567890 before running."
	if err := Validate(g); err == nil || !strings.Contains(err.Error(), "credential material") {
		t.Fatalf("Validate() error = %v, want credential rejection", err)
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
