package service

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/internal/procedurefiles"
	"github.com/threadforge-ai/threadforge/internal/projectfiles"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

var sha256FingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type ContextRequestError struct{ Message string }

func (e *ContextRequestError) Error() string { return e.Message }

func RetrieveContext(ctx context.Context, procedures *store.Store, request ContextRequest) (ContextResponse, error) {
	return RetrieveContextWithProjectMemory(ctx, procedures, nil, request)
}

func RetrieveContextWithProjectMemory(ctx context.Context, procedures *store.Store, memoryStore *projectfiles.Store, request ContextRequest, publishedStores ...*procedurefiles.Store) (ContextResponse, error) {
	return RetrieveContextWithProjectRoot(ctx, procedures, memoryStore, request, "", publishedStores...)
}

func RetrieveContextWithProjectRoot(
	ctx context.Context, procedures *store.Store, memoryStore *projectfiles.Store, request ContextRequest,
	projectRoot string, publishedStores ...*procedurefiles.Store,
) (ContextResponse, error) {
	if err := validateContextRequest(&request); err != nil {
		return ContextResponse{}, err
	}
	version2 := request.SchemaVersion == ContextRequestV2
	if !version2 {
		memoryStore = nil
	}
	graphs, err := procedures.ListProject(ctx, request.ProjectID)
	if err != nil {
		return ContextResponse{}, fmt.Errorf("list approved project procedures: %w", err)
	}
	var publishedProcedureError bool
	var publishedProcedureGap []string
	if version2 && len(publishedStores) > 0 && publishedStores[0] != nil {
		published, err := publishedStores[0].ListProject(request.ProjectID)
		if err != nil {
			publishedProcedureError = true
			publishedProcedureGap = append(publishedProcedureGap, "published project procedures are unavailable or failed integrity/privacy validation")
		} else {
			publishedByID := make(map[string]proceduregraph.Graph, len(published))
			for _, graph := range published {
				publishedByID[graph.ID] = graph
			}
			localByID := make(map[string]int, len(graphs))
			for index, graph := range graphs {
				localByID[graph.ID] = index
				if graph.Status != proceduregraph.StatusApproved {
					continue
				}
				publishedGraph, ok := publishedByID[graph.ID]
				if !ok || graph.Revision > publishedGraph.Revision {
					publishedProcedureGap = append(publishedProcedureGap, fmt.Sprintf("procedure %s revision %d is approved locally but not yet published to project files", graph.ID, graph.Revision))
				}
			}
			for _, graph := range published {
				if index, ok := localByID[graph.ID]; ok {
					if graphs[index].Revision <= graph.Revision {
						graphs[index] = graph
					}
					continue
				}
				graphs = append(graphs, graph)
			}
		}
	}
	responseVersion := ContextResponseV1
	if version2 {
		responseVersion = ContextResponseV2
	}
	response := ContextResponse{SchemaVersion: responseVersion, Status: "incomplete", ActionReady: false,
		Procedures: []ProcedureContextItem{}, Gaps: []string{}}
	if version2 {
		response.Memory = []MemoryContextItem{}
	}
	currentByPath := make(map[string]string, len(request.PathFingerprints))
	for _, current := range request.PathFingerprints {
		currentByPath[current.Path] = current.SHA256
	}
	localFingerprintError := false
	if strings.TrimSpace(projectRoot) != "" {
		candidates := make([]proceduregraph.Graph, 0, request.MaxItems)
		for _, graph := range graphs {
			if graph.Status == proceduregraph.StatusApproved && matchesPaths(graph, request.Paths) && matchesQuery(graph, request.Query) {
				candidates = append(candidates, graph)
				if len(candidates) >= request.MaxItems {
					break
				}
			}
		}
		localByPath, fingerprintErr := fingerprintProceduresLocally(projectRoot, candidates)
		if fingerprintErr != nil {
			localFingerprintError = true
			currentByPath = map[string]string{}
		} else {
			currentByPath = localByPath
		}
	}
	staleFound := false
	conflictFound := false
	privacyBlocked := publishedProcedureError
	response.Gaps = append(response.Gaps, publishedProcedureGap...)
	if len(request.Paths) == 0 {
		for _, graph := range graphs {
			if graph.Status == proceduregraph.StatusApproved && len(graph.Applicability.Paths) > 0 && matchesQuery(graph, request.Query) {
				response.Gaps = append(response.Gaps, fmt.Sprintf("procedure %s applicability is unverified because path context is missing", graph.ID))
			}
		}
	}
	remaining := request.MaxTokens
	for _, graph := range graphs {
		if graph.Status != proceduregraph.StatusApproved || !matchesPaths(graph, request.Paths) || !matchesQuery(graph, request.Query) {
			continue
		}
		freshness, reason := "", ""
		if localFingerprintError {
			freshness, reason = "unverified", "current source fingerprints are unavailable from the configured project root"
		} else {
			freshness, reason = freshnessFor(graph, currentByPath, request.Paths)
		}
		if freshness == "stale" {
			staleFound = true
			response.Gaps = append(response.Gaps, fmt.Sprintf("procedure %s revision %d is stale: %s", graph.ID, graph.Revision, reason))
			continue
		}
		item := ProcedureContextItem{ID: graph.ID, Revision: graph.Revision, Title: graph.Title, Freshness: freshness}
		if freshness != "verified" {
			response.Gaps = append(response.Gaps, fmt.Sprintf("procedure %s revision %d freshness is unverified: %s", graph.ID, graph.Revision, reason))
		}
		for _, node := range graph.Nodes {
			text := strings.TrimSpace(node.Instruction)
			cost := estimatedTokens(text)
			if cost > remaining {
				response.Gaps = append(response.Gaps, "context truncated at max_tokens; retrieve a narrower query")
				break
			}
			item.Guidance = append(item.Guidance, text)
			remaining -= cost
		}
		if len(item.Guidance) == 0 {
			continue
		}
		response.Procedures = append(response.Procedures, item)
		if len(response.Procedures)+len(response.Memory) >= request.MaxItems {
			response.Gaps = append(response.Gaps, "additional approved procedures may match; increase max_items or narrow the query")
			break
		}
		if remaining <= 0 {
			break
		}
	}
	if version2 && memoryStore == nil {
		response.Gaps = append(response.Gaps, "version-controlled project memory is not configured for this service")
	} else if version2 {
		records, total, err := memoryStore.List("", 200, 0)
		if err != nil {
			privacyBlocked = true
			response.Gaps = append(response.Gaps, "project memory is unavailable or failed integrity/privacy validation")
		} else {
			if total > len(records) {
				response.Gaps = append(response.Gaps, "project-memory listing reached its 200-record retrieval limit")
				records = nil
			}
			projectRecords := make([]projectmemory.Record, 0, len(records))
			superseded := make(map[string]bool)
			for _, record := range records {
				if record.ProjectID != request.ProjectID {
					continue
				}
				projectRecords = append(projectRecords, record)
				if record.SupersedesID != "" {
					superseded[record.SupersedesID] = true
				}
			}
			applicable := make([]projectmemory.Record, 0, len(projectRecords))
			scopeUnknownConflictKeys := make(map[string]bool)
			for _, record := range projectRecords {
				if (len(record.Applicability.Paths) > 0 && len(request.Paths) == 0) ||
					(len(record.Applicability.Modules) > 0 && len(request.Modules) == 0) {
					if matchesMemoryQuery(record, request.Query) {
						response.Gaps = append(response.Gaps, fmt.Sprintf("project memory %s applicability is unverified because path or module context is missing", record.ID))
						if record.ConflictKey != "" {
							scopeUnknownConflictKeys[record.ConflictKey] = true
						}
					}
					continue
				}
				if !superseded[record.ID] && projectmemory.Applicable(record, request.Paths, request.Modules) {
					applicable = append(applicable, record)
				}
			}
			conflictDigests := make(map[string]map[string]bool)
			for _, record := range applicable {
				if record.ConflictKey == "" {
					continue
				}
				if conflictDigests[record.ConflictKey] == nil {
					conflictDigests[record.ConflictKey] = map[string]bool{}
				}
				conflictDigests[record.ConflictKey][record.ContentSHA256] = true
			}
			conflicted := make(map[string]bool)
			for key, digests := range conflictDigests {
				if len(digests) > 1 {
					conflicted[key] = true
				}
			}
			for _, record := range applicable {
				if !matchesMemoryQuery(record, request.Query) {
					continue
				}
				if record.ConflictKey != "" && scopeUnknownConflictKeys[record.ConflictKey] {
					continue
				}
				if record.ConflictKey != "" && conflicted[record.ConflictKey] {
					if !conflictFound {
						response.Gaps = append(response.Gaps, "conflicting approved project-memory records match this context; review the conflict key before relying on either record")
					}
					conflictFound = true
					continue
				}
				freshness := memoryStore.CheckFreshness(record)
				if freshness == "stale" {
					staleFound = true
					response.Gaps = append(response.Gaps, fmt.Sprintf("project memory %s revision %d is stale because an evidence source file changed; content was withheld", record.ID, record.Revision))
					continue
				}
				if freshness == "incomplete" {
					response.Gaps = append(response.Gaps, fmt.Sprintf("project memory %s revision %d source freshness could not be verified safely; content was withheld", record.ID, record.Revision))
					continue
				}
				if freshness == "unverified" {
					response.Gaps = append(response.Gaps, fmt.Sprintf("project memory %s revision %d has no source-file fingerprints; approval does not establish current source freshness", record.ID, record.Revision))
				}
				if len(response.Procedures)+len(response.Memory) >= request.MaxItems {
					response.Gaps = append(response.Gaps, "additional approved project-memory records may match; retrieve a narrower query")
					break
				}
				cost := estimatedTokens(record.Title + string(record.Content))
				if cost > remaining {
					response.Gaps = append(response.Gaps, "project memory truncated at max_tokens; retrieve a narrower query")
					break
				}
				response.Memory = append(response.Memory, MemoryContextItem{
					ID: record.ID, Kind: string(record.Kind), Revision: record.Revision, Title: record.Title,
					Content: record.Content, Evidence: record.Evidence,
				})
				remaining -= cost
			}
		}
	}
	if version2 && privacyBlocked {
		response.Status = "privacy_blocked"
		response.Procedures = []ProcedureContextItem{}
		response.Memory = []MemoryContextItem{}
	} else if staleFound {
		response.Status = "stale"
	} else if version2 && conflictFound {
		response.Status = "conflicting"
	} else if len(response.Procedures)+len(response.Memory) > 0 && len(response.Gaps) == 0 {
		response.Status = "complete"
	}
	if len(response.Procedures)+len(response.Memory) == 0 && !(version2 && privacyBlocked) {
		response.Gaps = append(response.Gaps, "no approved applicable memory matched this query")
	}
	if !version2 {
		for index := range response.Procedures {
			response.Procedures[index].Freshness = ""
		}
	}
	return response, nil
}

func fingerprintProceduresLocally(projectRoot string, graphs []proceduregraph.Graph) (map[string]string, error) {
	paths := make([]string, 0)
	seen := make(map[string]bool)
	for _, graph := range graphs {
		for _, source := range graph.SourceFiles {
			if seen[source.Path] {
				continue
			}
			seen[source.Path] = true
			paths = append(paths, source.Path)
			if len(paths) > 500 {
				return nil, &ContextRequestError{Message: "local context source fingerprint request exceeds 500 files"}
			}
		}
	}
	if len(paths) == 0 {
		return map[string]string{}, nil
	}
	sort.Strings(paths)
	fingerprints, err := procedurefiles.FingerprintSources(projectRoot, paths)
	if err != nil {
		return nil, err
	}
	currentByPath := make(map[string]string, len(fingerprints))
	for _, fingerprint := range fingerprints {
		currentByPath[fingerprint.Path] = fingerprint.SHA256
	}
	return currentByPath, nil
}

func ValidateContextRequest(request ContextRequest) error {
	return validateContextRequest(&request)
}

func validateContextRequest(request *ContextRequest) error {
	if (request.SchemaVersion != ContextRequestV1 && request.SchemaVersion != ContextRequestV2) ||
		strings.TrimSpace(request.ProjectID) == "" || strings.TrimSpace(request.Query) == "" {
		return &ContextRequestError{Message: "context request requires a supported schema_version, project_id, and query"}
	}
	if request.SchemaVersion == ContextRequestV1 && (len(request.Modules) > 0 || len(request.PathFingerprints) > 0) {
		return &ContextRequestError{Message: "modules and path_fingerprints require threadforge.context-request.v2"}
	}
	if len(request.Query) > 2000 || len(request.Paths) > 64 || len(request.Modules) > 64 || len(request.PathFingerprints) > 500 {
		return &ContextRequestError{Message: "query, paths, modules, or path fingerprints exceed limits"}
	}
	seenPaths := make(map[string]bool, len(request.PathFingerprints))
	for _, fingerprint := range request.PathFingerprints {
		if err := proceduregraph.ValidateRepositoryPath(fingerprint.Path); err != nil || !sha256FingerprintPattern.MatchString(fingerprint.SHA256) {
			return &ContextRequestError{Message: "path_fingerprints must contain safe repository paths and SHA-256 digests"}
		}
		if seenPaths[fingerprint.Path] {
			return &ContextRequestError{Message: "path_fingerprints must not contain duplicate paths"}
		}
		seenPaths[fingerprint.Path] = true
	}
	for _, changedPath := range request.Paths {
		if err := proceduregraph.ValidateRepositoryPath(changedPath); err != nil {
			return &ContextRequestError{Message: "paths must contain safe repository-relative paths"}
		}
	}
	for _, module := range request.Modules {
		if strings.TrimSpace(module) == "" || len(module) > 128 {
			return &ContextRequestError{Message: "modules must contain non-empty identifiers of at most 128 characters"}
		}
	}
	if request.MaxItems == 0 {
		request.MaxItems = 8
	}
	if request.MaxTokens == 0 {
		request.MaxTokens = 4000
	}
	if request.MaxItems < 1 || request.MaxItems > 32 || request.MaxTokens < 1 || request.MaxTokens > 16000 {
		return &ContextRequestError{Message: "max_items must be 1-32 and max_tokens 1-16000"}
	}
	return nil
}

func matchesMemoryQuery(record projectmemory.Record, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "*" {
		return true
	}
	corpus := strings.ToLower(record.Title + " " + string(record.Content))
	if strings.Contains(corpus, query) {
		return true
	}
	for _, term := range queryTerms(query) {
		if len(term) > 2 && strings.Contains(corpus, term) {
			return true
		}
	}
	return false
}

// AssessProcedureFreshness applies the same fingerprint and changed-path rules
// used by unified context retrieval to direct MCP procedure reads.
func AssessProcedureFreshness(
	graph proceduregraph.Graph, fingerprints []proceduregraph.PathFingerprint, changedPaths []string,
) (string, string, error) {
	if len(fingerprints) > 500 || len(changedPaths) > 64 {
		return "", "", &ContextRequestError{Message: "procedure freshness inputs exceed limits"}
	}
	currentByPath := make(map[string]string, len(fingerprints))
	for _, fingerprint := range fingerprints {
		if err := proceduregraph.ValidateRepositoryPath(fingerprint.Path); err != nil ||
			!sha256FingerprintPattern.MatchString(fingerprint.SHA256) {
			return "", "", &ContextRequestError{Message: "path_fingerprints must contain safe repository paths and SHA-256 digests"}
		}
		if _, exists := currentByPath[fingerprint.Path]; exists {
			return "", "", &ContextRequestError{Message: "path_fingerprints must not contain duplicate paths"}
		}
		currentByPath[fingerprint.Path] = fingerprint.SHA256
	}
	for _, changedPath := range changedPaths {
		if err := proceduregraph.ValidateRepositoryPath(changedPath); err != nil {
			return "", "", &ContextRequestError{Message: "paths must contain safe repository-relative paths"}
		}
	}
	freshness, reason := freshnessFor(graph, currentByPath, changedPaths)
	return freshness, reason, nil
}

func freshnessFor(graph proceduregraph.Graph, currentByPath map[string]string, changedPaths []string) (string, string) {
	if len(graph.SourceFiles) == 0 {
		return "unverified", "procedure has no recorded source-file fingerprints"
	}
	known := make(map[string]string, len(graph.SourceFiles))
	for _, source := range graph.SourceFiles {
		known[source.Path] = source.SHA256
		current, ok := currentByPath[source.Path]
		if !ok {
			return "unverified", "current fingerprint is missing for " + source.Path
		}
		if current != source.SHA256 {
			return "stale", "source file changed: " + source.Path
		}
	}
	for _, changed := range changedPaths {
		if !proceduregraph.ApplicableToPath(graph, changed) {
			continue
		}
		if _, ok := known[changed]; !ok {
			return "unverified", "no baseline fingerprint exists for applicable path " + changed
		}
	}
	return "verified", "all recorded source-file fingerprints match"
}
