package procedurefiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

var (
	ErrConflict = errors.New("published procedure revision conflict")
	ErrNotFound = errors.New("published procedure not found")
)

const maxGraphBytes = 2 << 20
const maxProcedures = 10000
const sharedRecordSchema = "threadforge.shared-procedure-record.v1"

type Store struct {
	projectRoot    string
	graphsRoot     string
	policy         *teamauth.Policy
	scopeWriteGate ProjectScopeWriteGate
}

type ProjectScopeWriteGate interface {
	WithActiveTeamProjectScope(context.Context, string, func() error) error
}

type sharedRecord struct {
	SchemaVersion string               `json:"schema_version"`
	Procedure     proceduregraph.Graph `json:"procedure"`
	Authorization teamauth.Attestation `json:"authorization"`
}

func New(projectRoot string) (*Store, error) {
	paths, err := appdata.Resolve("", "")
	if err != nil {
		return nil, err
	}
	var policy *teamauth.Policy
	loadedPolicy, _, _, err := appdata.ReadTeamTrustPolicy(paths.Root)
	if errors.Is(err, appdata.ErrTeamTrustPolicyNotInstalled) {
		err = nil
	} else if err == nil {
		policy = &loadedPolicy
	}
	if err != nil {
		return nil, err
	}
	return NewWithPolicy(projectRoot, policy)
}

func NewWithProjectScopeWriteGate(projectRoot string, gate ProjectScopeWriteGate) (*Store, error) {
	if gate == nil {
		return nil, errors.New("a team project scope write gate is required")
	}
	projectStore, err := New(projectRoot)
	if err != nil {
		return nil, err
	}
	projectStore.scopeWriteGate = gate
	return projectStore, nil
}

func NewLocal(projectRoot string) (*Store, error) {
	return NewWithPolicy(projectRoot, nil)
}

func NewWithPolicy(projectRoot string, policy *teamauth.Policy) (*Store, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("project root must be an existing directory, not a symbolic link")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if policy != nil {
		if err := policy.Validate(); err != nil {
			return nil, err
		}
		copyPolicy := *policy
		copyPolicy.Members = append([]teamauth.Member(nil), policy.Members...)
		for i := range copyPolicy.Members {
			copyPolicy.Members[i].Projects = append([]string(nil), policy.Members[i].Projects...)
			copyPolicy.Members[i].Roles = append([]string(nil), policy.Members[i].Roles...)
		}
		policy = &copyPolicy
	}
	return &Store{projectRoot: root, graphsRoot: filepath.Join(root, ".threadforge", "procedures"), policy: policy}, nil
}

func NewWithPolicyAndProjectScopeWriteGate(projectRoot string, policy *teamauth.Policy, gate ProjectScopeWriteGate) (*Store, error) {
	if gate == nil {
		return nil, errors.New("a team project scope write gate is required")
	}
	projectStore, err := NewWithPolicy(projectRoot, policy)
	if err != nil {
		return nil, err
	}
	projectStore.scopeWriteGate = gate
	return projectStore, nil
}

// Publish is disabled because shared publication requires authenticated authorization.
func (s *Store) Publish(graph proceduregraph.Graph, expectedPublishedRevision int, privacyReviewed bool) (proceduregraph.Graph, error) {
	return proceduregraph.Graph{}, errors.New("authenticated team authorization is required for shared procedure publication")
}

func (s *Store) SignPublication(graph proceduregraph.Graph, action string, identity teamauth.Identity, at time.Time, privacyReviewed bool) (teamauth.Attestation, error) {
	if s.policy == nil {
		return teamauth.Attestation{}, errors.New("local team trust policy is not configured")
	}
	shared, err := publicationProjection(graph, action)
	if err != nil {
		return teamauth.Attestation{}, err
	}
	subject, ok := s.policy.SubjectFor(identity.KeyID)
	if !ok {
		return teamauth.Attestation{}, errors.New("signing identity is not present in the local team trust policy")
	}
	if !privacyReviewed || shared.Approval == nil || !shared.Approval.PrivacyReviewed {
		return teamauth.Attestation{}, errors.New("team publication requires explicit privacy review")
	}
	graphDigest, err := digestGraph(shared)
	if err != nil {
		return teamauth.Attestation{}, err
	}
	claims := teamauth.Claims{
		Action: action, ProjectID: shared.ProjectID, ProcedureID: shared.ID,
		Revision: shared.Revision, GraphSHA256: graphDigest, PrivacyReviewed: true,
	}
	attestation, err := teamauth.Sign(identity, subject, claims, at)
	if err != nil {
		return teamauth.Attestation{}, err
	}
	if err := teamauth.Verify(attestation, *s.policy, claims); err != nil {
		return teamauth.Attestation{}, err
	}
	if action == "publish" && subject != shared.Approval.ApprovedBy {
		return teamauth.Attestation{}, errors.New("authenticated reviewer subject must match the procedure approval identity")
	}
	return attestation, nil
}

func (s *Store) PublishAuthorized(graph proceduregraph.Graph, expectedPublishedRevision int, privacyReviewed bool, authorization teamauth.Attestation) (proceduregraph.Graph, error) {
	if expectedPublishedRevision < 0 || !privacyReviewed {
		return proceduregraph.Graph{}, errors.New("publish requires a non-negative expected revision and explicit privacy review")
	}
	shared, err := publicationProjection(graph, "publish")
	if err != nil {
		return proceduregraph.Graph{}, err
	}
	if shared.Approval == nil || authorization.Subject != shared.Approval.ApprovedBy {
		return proceduregraph.Graph{}, errors.New("authenticated reviewer subject must match the procedure approval identity")
	}
	return s.publishShared(shared, expectedPublishedRevision, false, authorization)
}

// PublishSupersession writes a privacy-reviewed tombstone for an approved graph.
// The replacement must already be published and approved in the same project so
// shared readers never mistake the superseded graph for current guidance.
func (s *Store) PublishSupersession(graph proceduregraph.Graph, expectedPublishedRevision int, privacyReviewed bool) (proceduregraph.Graph, error) {
	return proceduregraph.Graph{}, errors.New("authenticated team authorization is required for shared procedure supersession")
}

func (s *Store) PublishSupersessionAuthorized(graph proceduregraph.Graph, expectedPublishedRevision int, privacyReviewed bool, authorization teamauth.Attestation) (proceduregraph.Graph, error) {
	if expectedPublishedRevision < 0 || !privacyReviewed {
		return proceduregraph.Graph{}, errors.New("supersession publish requires a non-negative expected revision and explicit privacy review")
	}
	shared, err := publicationProjection(graph, "supersede")
	if err != nil {
		return proceduregraph.Graph{}, err
	}
	replacement, err := s.GetLatest(shared.SupersededBy)
	if err != nil || replacement.Status != proceduregraph.StatusApproved || replacement.ProjectID != shared.ProjectID {
		return proceduregraph.Graph{}, errors.New("replacement must already be published and approved in the same project")
	}
	return s.publishShared(shared, expectedPublishedRevision, true, authorization)
}

func (s *Store) publishShared(shared proceduregraph.Graph, expectedPublishedRevision int, supersession bool, authorization teamauth.Attestation) (proceduregraph.Graph, error) {
	if s.policy == nil {
		return proceduregraph.Graph{}, errors.New("local team trust policy is not configured")
	}
	graphDigest, err := digestGraph(shared)
	if err != nil {
		return proceduregraph.Graph{}, err
	}
	action := "publish"
	if supersession {
		action = "supersede"
	}
	claims := teamauth.Claims{
		Action: action, ProjectID: shared.ProjectID, ProcedureID: shared.ID,
		Revision: shared.Revision, GraphSHA256: graphDigest, PrivacyReviewed: true,
	}
	if err := teamauth.Verify(authorization, *s.policy, claims); err != nil {
		return proceduregraph.Graph{}, err
	}
	history, err := s.readHistory(shared.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return proceduregraph.Graph{}, err
	}
	actualRevision := 0
	if len(history) > 0 {
		actualRevision = history[len(history)-1].Revision
	}
	if expectedPublishedRevision != actualRevision || shared.Revision <= actualRevision {
		return proceduregraph.Graph{}, ErrConflict
	}
	if len(history) > 0 && history[len(history)-1].Status == proceduregraph.StatusSuperseded {
		return proceduregraph.Graph{}, errors.New("a superseded procedure ID cannot be republished; create a new procedure ID")
	}
	if supersession && shared.Status != proceduregraph.StatusSuperseded {
		return proceduregraph.Graph{}, errors.New("supersession publication requires superseded status")
	}
	if !supersession && shared.Status != proceduregraph.StatusApproved {
		return proceduregraph.Graph{}, errors.New("normal procedure publication requires approved status")
	}
	record := sharedRecord{SchemaVersion: sharedRecordSchema, Procedure: shared, Authorization: authorization}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return proceduregraph.Graph{}, err
	}
	data = append(data, '\n')
	if int64(len(data)) > maxGraphBytes {
		return proceduregraph.Graph{}, errors.New("published procedure exceeds 2 MiB")
	}
	if s.scopeWriteGate == nil {
		return proceduregraph.Graph{}, errors.New("registered active team project scope is required for shared procedure publication")
	}
	filename := filepath.Join(s.graphsRoot, shared.ID, fmt.Sprintf("rev-%06d.json", shared.Revision))
	write := func() error {
		if err := s.ensureDirectory(filepath.Dir(filename)); err != nil {
			return err
		}
		if err := writeNoReplace(filename, data); err != nil {
			if errors.Is(err, os.ErrExist) {
				return ErrConflict
			}
			return fmt.Errorf("write versioned project procedure: %w", err)
		}
		return nil
	}
	if err := s.scopeWriteGate.WithActiveTeamProjectScope(context.Background(), shared.ProjectID, write); err != nil {
		return proceduregraph.Graph{}, err
	}
	return shared, nil
}

func (s *Store) ListProject(projectID string) ([]proceduregraph.Graph, error) {
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 {
		return nil, errors.New("project_id must contain 1 to 256 characters")
	}
	if err := s.checkNoSymlinkComponents(s.graphsRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []proceduregraph.Graph{}, nil
		}
		return nil, err
	}
	directories, err := os.ReadDir(s.graphsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []proceduregraph.Graph{}, nil
	}
	if err != nil {
		return nil, err
	}
	var graphs []proceduregraph.Graph
	for _, entry := range directories {
		if !entry.IsDir() || !validID(entry.Name()) {
			return nil, fmt.Errorf("invalid published procedure entry %q", entry.Name())
		}
		history, err := s.readHistory(entry.Name())
		if err != nil {
			return nil, err
		}
		latest := history[len(history)-1]
		if latest.ProjectID == projectID {
			graphs = append(graphs, latest)
		}
		if len(graphs) > maxProcedures {
			return nil, errors.New("published procedure listing exceeds 10000-record limit")
		}
	}
	sort.Slice(graphs, func(i, j int) bool { return graphs[i].ID < graphs[j].ID })
	return graphs, nil
}

func (s *Store) GetLatest(id string) (proceduregraph.Graph, error) {
	history, err := s.readHistory(id)
	if err != nil {
		return proceduregraph.Graph{}, err
	}
	return history[len(history)-1], nil
}

func (s *Store) readHistory(id string) ([]proceduregraph.Graph, error) {
	if !validID(id) {
		return nil, errors.New("invalid published procedure identifier")
	}
	directory := filepath.Join(s.graphsRoot, id)
	if err := s.checkNoSymlinkComponents(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("published procedure history path is not a regular directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 || len(entries) > 1000 {
		return nil, errors.New("published procedure history is empty or exceeds the 1000-version limit")
	}
	graphs := make([]proceduregraph.Graph, 0, len(entries))
	lastRevision := 0
	projectID := ""
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("invalid published procedure revision entry %q", entry.Name())
		}
		revision, err := parseRevisionFilename(entry.Name())
		if err != nil || revision <= lastRevision {
			return nil, fmt.Errorf("invalid or unordered published procedure revision %q", entry.Name())
		}
		filename := filepath.Join(directory, entry.Name())
		fileInfo, err := os.Lstat(filename)
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 || fileInfo.Size() > maxGraphBytes {
			return nil, fmt.Errorf("invalid published procedure file %q", entry.Name())
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		var header struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal(data, &header); err != nil || header.SchemaVersion != sharedRecordSchema {
			return nil, fmt.Errorf("published procedure %s has no authenticated shared-record envelope", entry.Name())
		}
		var record sharedRecord
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("decode published procedure %s: %w", entry.Name(), err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("published procedure %s contains trailing JSON", entry.Name())
		}
		if record.SchemaVersion != sharedRecordSchema {
			return nil, fmt.Errorf("published procedure %s has no authenticated shared-record envelope", entry.Name())
		}
		graph := record.Procedure
		if graph.ID != id || graph.Revision != revision ||
			(graph.Status != proceduregraph.StatusApproved && graph.Status != proceduregraph.StatusSuperseded) {
			return nil, fmt.Errorf("published procedure identity/status mismatch in %s", entry.Name())
		}
		if len(graphs) > 0 && graphs[len(graphs)-1].Status == proceduregraph.StatusSuperseded {
			return nil, fmt.Errorf("published procedure %s has revisions after its supersession", id)
		}
		if projectID == "" {
			projectID = graph.ProjectID
		} else if projectID != graph.ProjectID {
			return nil, fmt.Errorf("published procedure project scope changed in %s", entry.Name())
		}
		if err := proceduregraph.Validate(graph); err != nil {
			return nil, fmt.Errorf("invalid published procedure %s: %w", entry.Name(), err)
		}
		if graph.Approval == nil || !graph.Approval.PrivacyReviewed {
			return nil, fmt.Errorf("published procedure %s lacks privacy-reviewed approval", entry.Name())
		}
		if s.policy == nil {
			return nil, errors.New("local team trust policy is not configured; shared procedures are withheld")
		}
		graphDigest, err := digestGraph(graph)
		if err != nil {
			return nil, err
		}
		action := "publish"
		if graph.Status == proceduregraph.StatusSuperseded {
			action = "supersede"
		}
		claims := teamauth.Claims{
			Action: action, ProjectID: graph.ProjectID, ProcedureID: graph.ID,
			Revision: graph.Revision, GraphSHA256: graphDigest, PrivacyReviewed: true,
		}
		if err := teamauth.Verify(record.Authorization, *s.policy, claims); err != nil {
			return nil, fmt.Errorf("published procedure %s has invalid team authorization: %w", id, err)
		}
		if action == "publish" && record.Authorization.Subject != graph.Approval.ApprovedBy {
			return nil, fmt.Errorf("published procedure %s approval identity does not match its authenticated reviewer", id)
		}
		for _, evidence := range graph.Evidence {
			if evidence.LocalTraceRef != "" || !evidence.Redacted {
				return nil, fmt.Errorf("published procedure %s contains local or unredacted evidence", entry.Name())
			}
		}
		graphs = append(graphs, graph)
		lastRevision = revision
	}
	return graphs, nil
}

func publicationProjection(graph proceduregraph.Graph, action string) (proceduregraph.Graph, error) {
	if action == "publish" {
		return proceduregraph.SanitizeForShare(graph)
	}
	if action != "supersede" || graph.Status != proceduregraph.StatusSuperseded || graph.Approval == nil || !graph.Approval.PrivacyReviewed {
		return proceduregraph.Graph{}, errors.New("only a privacy-reviewed superseded procedure may be published")
	}
	if err := proceduregraph.Validate(graph); err != nil {
		return proceduregraph.Graph{}, err
	}
	shareInput := graph
	shareInput.Status = proceduregraph.StatusApproved
	shared, err := proceduregraph.SanitizeForShare(shareInput)
	if err != nil {
		return proceduregraph.Graph{}, err
	}
	shared.Status = proceduregraph.StatusSuperseded
	shared.SupersededBy = graph.SupersededBy
	if err := proceduregraph.Validate(shared); err != nil {
		return proceduregraph.Graph{}, err
	}
	return shared, nil
}

func digestGraph(graph proceduregraph.Graph) (string, error) {
	data, err := json.Marshal(graph)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Store) ensureDirectory(target string) error {
	relative, err := filepath.Rel(s.projectRoot, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("published procedure path resolves outside the project")
	}
	current := s.projectRoot
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("published procedure directory component is unavailable or unsafe: %s", current)
		}
	}
	return nil
}

func (s *Store) checkNoSymlinkComponents(target string) error {
	relative, err := filepath.Rel(s.projectRoot, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("published procedure path resolves outside the project")
	}
	current := s.projectRoot
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("published procedure path contains a symbolic link: %s", current)
		}
	}
	return nil
}

func writeNoReplace(filename string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".pending-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Link(temporaryName, filename)
}

func parseRevisionFilename(name string) (int, error) {
	if !strings.HasPrefix(name, "rev-") || !strings.HasSuffix(name, ".json") {
		return 0, errors.New("invalid revision filename")
	}
	value := strings.TrimSuffix(strings.TrimPrefix(name, "rev-"), ".json")
	revision, err := strconv.Atoi(value)
	if err != nil || revision < 1 || fmt.Sprintf("%06d", revision) != value {
		return 0, errors.New("invalid revision number")
	}
	return revision, nil
}

func validID(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' || len(value) > 64 {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}
