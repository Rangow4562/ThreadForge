package proceduregraph

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const SchemaVersion = "threadforge.procedure-graph.v1"

const (
	StatusDraft      Status = "draft"
	StatusValidated  Status = "validated"
	StatusApproved   Status = "approved"
	StatusRejected   Status = "rejected"
	StatusSuperseded Status = "superseded"
	StatusStale      Status = "stale"
)

type Status string

type NodeKind string

const (
	NodeTrigger NodeKind = "trigger"
	NodeState   NodeKind = "state"
	NodeAction  NodeKind = "action"
	NodeCheck   NodeKind = "check"
	NodeOutput  NodeKind = "output"
)

type Relation string

const (
	RelationRequires   Relation = "requires"
	RelationEnables    Relation = "enables"
	RelationLeadsTo    Relation = "leads_to"
	RelationBranchesTo Relation = "branches_to"
	RelationLoopsTo    Relation = "loops_to"
	RelationValidates  Relation = "validates"
)

type Graph struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	ProjectID     string            `json:"project_id"`
	Title         string            `json:"title"`
	Revision      int               `json:"revision"`
	Status        Status            `json:"status"`
	Applicability Applicability     `json:"applicability"`
	Nodes         []Node            `json:"nodes"`
	Edges         []Edge            `json:"edges"`
	Evidence      []Evidence        `json:"evidence"`
	SourceFiles   []PathFingerprint `json:"source_files,omitempty"`
	Validation    *Validation       `json:"validation,omitempty"`
	Approval      *Approval         `json:"approval,omitempty"`
	SupersededBy  string            `json:"superseded_by,omitempty"`
}

type Applicability struct {
	Modules []string `json:"modules,omitempty"`
	Paths   []string `json:"paths,omitempty"`
}

type PathFingerprint struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Node struct {
	ID          string   `json:"id"`
	Kind        NodeKind `json:"kind"`
	Instruction string   `json:"instruction"`
	Module      string   `json:"module,omitempty"`
	Paths       []string `json:"paths,omitempty"`
}

type Edge struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Relation  Relation `json:"relation"`
	Condition string   `json:"condition,omitempty"`
}

// Evidence deliberately stores digests and local references, never source text.
// LocalTraceRef must be removed from team projections.
type Evidence struct {
	Origin        string `json:"origin"`
	ContentSHA256 string `json:"content_sha256"`
	Harness       string `json:"harness,omitempty"`
	ObservedAt    string `json:"observed_at,omitempty"`
	LocalTraceRef string `json:"local_trace_ref,omitempty"`
	Redacted      bool   `json:"redacted"`
}

type Validation struct {
	DatasetFingerprint string  `json:"dataset_fingerprint"`
	HeldOut            bool    `json:"held_out"`
	BaselineRuns       int     `json:"baseline_runs"`
	BaselineScore      float64 `json:"baseline_score"`
	CandidateScore     float64 `json:"candidate_score"`
	NoiseFloor         float64 `json:"noise_floor"`
	CostUnit           string  `json:"cost_unit"`
	BaselineCost       float64 `json:"baseline_cost"`
	CandidateCost      float64 `json:"candidate_cost"`
	MaxCostIncrease    float64 `json:"max_cost_increase"`
	Status             string  `json:"status"`
}

type Approval struct {
	ApprovedBy      string `json:"approved_by"`
	ApprovedAt      string `json:"approved_at"`
	Reason          string `json:"reason"`
	PrivacyReviewed bool   `json:"privacy_reviewed"`
}

type Problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationError struct {
	Problems []Problem `json:"problems"`
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		parts = append(parts, fmt.Sprintf("%s: %s", problem.Field, problem.Message))
	}
	return strings.Join(parts, "; ")
}

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

var allowedStatuses = map[Status]bool{
	StatusDraft: true, StatusValidated: true, StatusApproved: true,
	StatusRejected: true, StatusSuperseded: true, StatusStale: true,
}

func Validate(g Graph) error {
	var problems []Problem
	add := func(field, message string) { problems = append(problems, Problem{Field: field, Message: message}) }
	checkPrivacy := func(field, value string) {
		if privacy.ContainsCredentialText(value) {
			add(field, "must not contain credential material")
		}
	}

	if g.SchemaVersion != SchemaVersion {
		add("schema_version", "unsupported schema version")
	}
	if !validID(g.ID) {
		add("id", "must be a lowercase stable identifier")
	}
	if strings.TrimSpace(g.ProjectID) == "" || len(g.ProjectID) > 256 {
		add("project_id", "must contain 1 to 256 characters")
	}
	if strings.TrimSpace(g.Title) == "" || len(g.Title) > 240 {
		add("title", "must contain 1 to 240 characters")
	}
	checkPrivacy("project_id", g.ProjectID)
	checkPrivacy("title", g.Title)
	if g.Revision < 1 {
		add("revision", "must be at least 1")
	}
	if !allowedStatuses[g.Status] {
		add("status", "unknown lifecycle state")
	}
	if len(g.Nodes) == 0 || len(g.Nodes) > 256 {
		add("nodes", "must contain between 1 and 256 nodes")
	}
	if len(g.Edges) > 1024 {
		add("edges", "must contain no more than 1024 edges")
	}
	if len(g.SourceFiles) > 500 {
		add("source_files", "must contain no more than 500 path fingerprints")
	}
	sourcePaths := make(map[string]bool, len(g.SourceFiles))
	for i, source := range g.SourceFiles {
		field := fmt.Sprintf("source_files[%d]", i)
		if err := validateRepositoryPath(source.Path); err != nil {
			add(field+".path", err.Error())
		}
		if isCredentialPath(source.Path) {
			add(field+".path", "credential-bearing paths cannot be fingerprinted")
		}
		checkPrivacy(field+".path", source.Path)
		if sourcePaths[source.Path] {
			add(field+".path", "duplicate source path fingerprint")
		}
		sourcePaths[source.Path] = true
		if !ApplicableToPath(g, source.Path) {
			add(field+".path", "source path is outside graph applicability")
		}
		if !digestPattern.MatchString(source.SHA256) {
			add(field+".sha256", "must be a lowercase SHA-256 digest")
		}
	}

	for i, pattern := range g.Applicability.Paths {
		checkPrivacy(fmt.Sprintf("applicability.paths[%d]", i), pattern)
		if err := validatePathPattern(pattern); err != nil {
			add(fmt.Sprintf("applicability.paths[%d]", i), err.Error())
		}
	}
	for i, module := range g.Applicability.Modules {
		checkPrivacy(fmt.Sprintf("applicability.modules[%d]", i), module)
		if strings.TrimSpace(module) == "" || len(module) > 128 {
			add(fmt.Sprintf("applicability.modules[%d]", i), "must contain 1 to 128 characters")
		}
	}

	nodeIDs := make(map[string]Node, len(g.Nodes))
	for i, node := range g.Nodes {
		field := fmt.Sprintf("nodes[%d]", i)
		if !validID(node.ID) {
			add(field+".id", "must be a lowercase stable identifier")
		}
		if _, exists := nodeIDs[node.ID]; exists {
			add(field+".id", "duplicate node identifier")
		}
		nodeIDs[node.ID] = node
		if !validNodeKind(node.Kind) {
			add(field+".kind", "unknown node kind")
		}
		if strings.TrimSpace(node.Instruction) == "" || len(node.Instruction) > 4000 {
			add(field+".instruction", "must contain 1 to 4000 characters")
		}
		checkPrivacy(field+".instruction", node.Instruction)
		checkPrivacy(field+".module", node.Module)
		for j, pattern := range node.Paths {
			checkPrivacy(fmt.Sprintf("%s.paths[%d]", field, j), pattern)
			if err := validatePathPattern(pattern); err != nil {
				add(fmt.Sprintf("%s.paths[%d]", field, j), err.Error())
			}
		}
	}

	edgeKeys := make(map[string]bool, len(g.Edges))
	for i, edge := range g.Edges {
		field := fmt.Sprintf("edges[%d]", i)
		if _, exists := nodeIDs[edge.From]; !exists {
			add(field+".from", "references an unknown node")
		}
		if _, exists := nodeIDs[edge.To]; !exists {
			add(field+".to", "references an unknown node")
		}
		if !validRelation(edge.Relation) {
			add(field+".relation", "unknown edge relation")
		}
		if len(edge.Condition) > 1000 {
			add(field+".condition", "must be no more than 1000 characters")
		}
		checkPrivacy(field+".condition", edge.Condition)
		key := edge.From + "\x00" + edge.To + "\x00" + string(edge.Relation) + "\x00" + edge.Condition
		if edgeKeys[key] {
			add(field, "duplicate edge")
		}
		edgeKeys[key] = true
	}

	for i, evidence := range g.Evidence {
		field := fmt.Sprintf("evidence[%d]", i)
		switch evidence.Origin {
		case "trace", "document", "human", "benchmark":
		default:
			add(field+".origin", "unknown evidence origin")
		}
		if !digestPattern.MatchString(evidence.ContentSHA256) {
			add(field+".content_sha256", "must be a lowercase SHA-256 digest")
		}
		if len(evidence.LocalTraceRef) > 256 {
			add(field+".local_trace_ref", "must be no more than 256 characters")
		}
		checkPrivacy(field+".harness", evidence.Harness)
		checkPrivacy(field+".local_trace_ref", evidence.LocalTraceRef)
		if evidence.ObservedAt != "" {
			if _, err := time.Parse(time.RFC3339, evidence.ObservedAt); err != nil {
				add(field+".observed_at", "must use RFC3339")
			}
		}
	}

	if g.Validation != nil {
		if !digestPattern.MatchString(g.Validation.DatasetFingerprint) {
			add("validation.dataset_fingerprint", "must be a lowercase SHA-256 digest")
		}
		if g.Validation.BaselineRuns < 1 {
			add("validation.baseline_runs", "must be at least 1")
		}
		for field, score := range map[string]float64{
			"baseline_score":  g.Validation.BaselineScore,
			"candidate_score": g.Validation.CandidateScore,
			"noise_floor":     g.Validation.NoiseFloor,
		} {
			if score < 0 || score > 1 {
				add("validation."+field, "must be between 0 and 1")
			}
		}
		if !g.Validation.HeldOut {
			add("validation.held_out", "approval requires held-out evaluation")
		}
		if g.Validation.CostUnit != "tokens" && g.Validation.CostUnit != "usd" {
			add("validation.cost_unit", "must be tokens or usd")
		}
		if g.Validation.BaselineCost < 0 || g.Validation.CandidateCost < 0 || g.Validation.MaxCostIncrease < 0 {
			add("validation.cost", "cost values cannot be negative")
		}
		if g.Validation.Status != "passed" && g.Validation.Status != "failed" {
			add("validation.status", "must be passed or failed")
		}
		if g.Validation.Status == "passed" && !g.Validation.PassesGate() {
			add("validation.status", "passed status conflicts with the no-regression or cost gate")
		}
	}

	if g.Status == StatusValidated || g.Status == StatusApproved {
		if g.Validation == nil || !g.Validation.PassesGate() {
			add("status", "validated and approved graphs require a passing held-out report")
		}
	}
	if g.Status == StatusApproved {
		if g.Approval == nil || strings.TrimSpace(g.Approval.ApprovedBy) == "" || strings.TrimSpace(g.Approval.Reason) == "" {
			add("approval", "approved graphs require a reviewer and reason")
		} else {
			checkPrivacy("approval.approved_by", g.Approval.ApprovedBy)
			checkPrivacy("approval.reason", g.Approval.Reason)
			if _, err := time.Parse(time.RFC3339, g.Approval.ApprovedAt); err != nil {
				add("approval.approved_at", "must use RFC3339")
			}
		}
	}
	if g.Status == StatusSuperseded && !validID(g.SupersededBy) {
		add("superseded_by", "superseded graphs require a replacement identifier")
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func (v Validation) PassesGate() bool {
	return v.HeldOut && v.Status == "passed" &&
		v.CandidateScore+v.NoiseFloor >= v.BaselineScore &&
		v.CandidateCost <= v.BaselineCost+v.MaxCostIncrease
}

func Approve(g Graph, reviewer, reason, at string, privacyReviewed bool) (Graph, error) {
	if g.Status != StatusValidated || g.Validation == nil || !g.Validation.PassesGate() {
		return Graph{}, errors.New("only a validated graph that passes the held-out gate can be approved")
	}
	if strings.TrimSpace(reviewer) == "" || strings.TrimSpace(reason) == "" {
		return Graph{}, errors.New("reviewer and approval reason are required")
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		return Graph{}, errors.New("approval time must use RFC3339")
	}
	g.Status = StatusApproved
	g.Revision++
	g.Approval = &Approval{ApprovedBy: reviewer, ApprovedAt: at, Reason: reason, PrivacyReviewed: privacyReviewed}
	if err := Validate(g); err != nil {
		return Graph{}, err
	}
	return g, nil
}

// Supersede withdraws an approved procedure while preserving its revision and
// evidence. The replacement must be a different stable graph identifier; the
// caller is responsible for checking that the replacement is approved in the
// same project before persisting this transition.
func Supersede(g Graph, replacementID string) (Graph, error) {
	if err := Validate(g); err != nil {
		return Graph{}, err
	}
	if g.Status != StatusApproved {
		return Graph{}, errors.New("only an approved procedure can be superseded")
	}
	if !validID(replacementID) || replacementID == g.ID {
		return Graph{}, errors.New("replacement must be a different stable procedure identifier")
	}
	g.Status = StatusSuperseded
	g.SupersededBy = replacementID
	g.Revision++
	if err := Validate(g); err != nil {
		return Graph{}, err
	}
	return g, nil
}

func SanitizeForShare(g Graph) (Graph, error) {
	if err := Validate(g); err != nil {
		return Graph{}, err
	}
	if g.Status != StatusApproved || g.Approval == nil || !g.Approval.PrivacyReviewed {
		return Graph{}, errors.New("only privacy-reviewed approved graphs can be shared")
	}
	for _, evidence := range g.Evidence {
		if !evidence.Redacted {
			return Graph{}, errors.New("unredacted evidence cannot be shared")
		}
	}
	shared := g
	shared.Nodes = append([]Node(nil), g.Nodes...)
	shared.Edges = append([]Edge(nil), g.Edges...)
	shared.Evidence = append([]Evidence(nil), g.Evidence...)
	shared.SourceFiles = append([]PathFingerprint(nil), g.SourceFiles...)
	shared.Applicability.Modules = append([]string(nil), g.Applicability.Modules...)
	shared.Applicability.Paths = append([]string(nil), g.Applicability.Paths...)
	for i := range shared.Evidence {
		shared.Evidence[i].LocalTraceRef = ""
	}
	return shared, nil
}

func ApplicableToPath(g Graph, changedPath string) bool {
	if len(g.Applicability.Paths) == 0 {
		return true
	}
	changedPath = strings.TrimPrefix(path.Clean(strings.ReplaceAll(changedPath, "\\", "/")), "./")
	for _, pattern := range g.Applicability.Paths {
		if ok, _ := path.Match(pattern, changedPath); ok {
			return true
		}
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if changedPath == prefix || strings.HasPrefix(changedPath, prefix+"/") {
				return true
			}
		}
	}
	return false
}

type Neighborhood struct {
	GraphID   string `json:"graph_id"`
	Revision  int    `json:"revision"`
	Nodes     []Node `json:"nodes"`
	Edges     []Edge `json:"edges"`
	Truncated bool   `json:"truncated"`
}

func BoundedNeighborhood(g Graph, seeds []string, hops, maxNodes int) (Neighborhood, error) {
	if err := Validate(g); err != nil {
		return Neighborhood{}, err
	}
	if g.Status != StatusApproved {
		return Neighborhood{}, errors.New("only approved procedures can be retrieved")
	}
	if hops < 0 || hops > 8 || maxNodes < 1 || maxNodes > 256 {
		return Neighborhood{}, errors.New("limits must be between 0-8 hops and 1-256 nodes")
	}
	nodeByID := make(map[string]Node, len(g.Nodes))
	for _, node := range g.Nodes {
		nodeByID[node.ID] = node
	}
	seedSet := make(map[string]bool, len(seeds))
	for _, seed := range seeds {
		if _, ok := nodeByID[seed]; !ok {
			return Neighborhood{}, fmt.Errorf("unknown seed node %q", seed)
		}
		seedSet[seed] = true
	}
	if len(seedSet) == 0 {
		return Neighborhood{}, errors.New("at least one seed node is required")
	}
	frontier := make([]string, 0, len(seedSet))
	for id := range seedSet {
		frontier = append(frontier, id)
	}
	sort.Strings(frontier)
	selected := make(map[string]bool)
	truncated := false
	for depth := 0; depth <= hops && len(frontier) > 0; depth++ {
		nextSet := make(map[string]bool)
		for _, id := range frontier {
			if selected[id] {
				continue
			}
			if len(selected) >= maxNodes {
				truncated = true
				break
			}
			selected[id] = true
			if depth == hops {
				continue
			}
			for _, edge := range g.Edges {
				if edge.From == id && !selected[edge.To] {
					nextSet[edge.To] = true
				}
			}
		}
		frontier = frontier[:0]
		for id := range nextSet {
			frontier = append(frontier, id)
		}
		sort.Strings(frontier)
	}
	if len(frontier) > 0 {
		truncated = true
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	nodes := make([]Node, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, nodeByID[id])
	}
	edges := make([]Edge, 0)
	for _, edge := range g.Edges {
		if selected[edge.From] && selected[edge.To] {
			edges = append(edges, edge)
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Relation < edges[j].Relation
	})
	return Neighborhood{GraphID: g.ID, Revision: g.Revision, Nodes: nodes, Edges: edges, Truncated: truncated}, nil
}

func validID(value string) bool { return identifierPattern.MatchString(value) }

func validNodeKind(kind NodeKind) bool {
	return kind == NodeTrigger || kind == NodeState || kind == NodeAction || kind == NodeCheck || kind == NodeOutput
}

func validRelation(relation Relation) bool {
	return relation == RelationRequires || relation == RelationEnables || relation == RelationLeadsTo ||
		relation == RelationBranchesTo || relation == RelationLoopsTo || relation == RelationValidates
}

func validatePathPattern(value string) error {
	if value == "" || strings.Contains(value, "\\") || strings.Contains(value, ":") || strings.HasPrefix(value, "/") {
		return errors.New("must be a non-empty repository-relative path pattern")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("must not contain empty, current-directory, or parent-directory segments")
		}
	}
	if _, err := path.Match(value, "probe"); err != nil {
		return errors.New("contains an invalid glob pattern")
	}
	return nil
}

func validateRepositoryPath(value string) error {
	if value == "" || strings.Contains(value, "\\") || strings.Contains(value, ":") || strings.HasPrefix(value, "/") {
		return errors.New("must be a repository-relative path")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("must not contain empty, current-directory, or parent-directory segments")
		}
	}
	return nil
}

func ValidateRepositoryPath(value string) error { return validateRepositoryPath(value) }

func isCredentialPath(value string) bool {
	for _, segment := range strings.Split(strings.ToLower(value), "/") {
		if segment == ".env" || strings.HasPrefix(segment, ".env.") || strings.Contains(segment, "credential") ||
			strings.Contains(segment, "secret") || strings.Contains(segment, "private-key") || strings.Contains(segment, "private_key") ||
			segment == "id_rsa" || segment == "id_ed25519" || strings.Contains(segment, "auth-token") || segment == ".kube" ||
			strings.HasSuffix(segment, ".pem") || strings.HasSuffix(segment, ".p12") || strings.HasSuffix(segment, ".pfx") ||
			strings.HasSuffix(segment, ".key") || strings.HasSuffix(segment, ".keystore") {
			return true
		}
	}
	return false
}
