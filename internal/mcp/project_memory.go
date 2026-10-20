package mcp

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/threadforge-ai/threadforge/internal/projectfiles"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

const (
	projectMemoryIndexSchema  = "threadforge.project-memory-index.v1"
	projectMemoryReadSchema   = "threadforge.project-memory-read.v1"
	projectMemoryReadV2Schema = projectfiles.ProjectMemoryReadV2SchemaVersion
)

var projectMemoryIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type projectMemoryIndexItem struct {
	ID            string                      `json:"id"`
	Kind          projectmemory.Kind          `json:"kind"`
	Revision      int                         `json:"revision"`
	Title         string                      `json:"title"`
	Applicability projectmemory.Applicability `json:"applicability"`
	ContentSHA256 string                      `json:"content_sha256"`
	UpdatedAt     string                      `json:"updated_at"`
	State         string                      `json:"state"`
	ConflictKey   string                      `json:"conflict_key,omitempty"`
	SupersedesID  string                      `json:"supersedes_id,omitempty"`
}

type projectMemoryIndexResponse struct {
	SchemaVersion       string                   `json:"schema_version"`
	Status              string                   `json:"status"`
	ProjectID           string                   `json:"project_id"`
	Records             []projectMemoryIndexItem `json:"records"`
	TotalCount          int                      `json:"total_count"`
	Count               int                      `json:"count"`
	Limit               int                      `json:"limit"`
	Offset              int                      `json:"offset"`
	HasMore             bool                     `json:"has_more"`
	NextOffset          int                      `json:"next_offset"`
	Gaps                []string                 `json:"gaps"`
	ActionReady         bool                     `json:"action_ready"`
	GuidanceOnly        bool                     `json:"guidance_only"`
	ExecutionAuthorized bool                     `json:"execution_authorized"`
}

type projectMemoryReadResponse = projectmemory.ReadResponseV2

func (s *Server) indexProjectMemory(projectID string, kind projectmemory.Kind, limit, offset int) (projectMemoryIndexResponse, error) {
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 || privacy.ContainsCredentialText(projectID) {
		return projectMemoryIndexResponse{}, errors.New("project_id must contain 1-256 characters")
	}
	if kind != "" && kind != projectmemory.KindFact && kind != projectmemory.KindDecision && kind != projectmemory.KindPolicy {
		return projectMemoryIndexResponse{}, errors.New("kind must be fact, decision, policy, or empty")
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return projectMemoryIndexResponse{}, errors.New("limit must be 1-100 and offset non-negative")
	}
	response := projectMemoryIndexResponse{
		SchemaVersion: projectMemoryIndexSchema, Status: "incomplete", ProjectID: projectID,
		Records: []projectMemoryIndexItem{}, Limit: limit, Offset: offset, Gaps: []string{},
		ActionReady: false, GuidanceOnly: true, ExecutionAuthorized: false,
	}
	if s.memory == nil {
		response.Gaps = append(response.Gaps, "version-controlled project memory is not configured for this MCP server")
		return response, nil
	}
	allEntries, err := s.memory.IndexProject(projectID, "")
	if err != nil {
		response.Status = "privacy_blocked"
		response.Gaps = append(response.Gaps, "project-memory index is unavailable or failed integrity/privacy validation")
		return response, nil
	}
	entries := allEntries
	if kind != "" {
		entries = make([]projectfiles.IndexEntry, 0)
		for _, entry := range allEntries {
			if entry.Kind == kind {
				entries = append(entries, entry)
			}
		}
	}
	response.Status = "complete"
	if len(entries) > 0 {
		response.Gaps = append(response.Gaps, "index state reflects approval, conflict, and supersession only; use threadforge_project_memory_read_v2 to check source freshness")
	}
	if offset > len(entries) {
		offset = len(entries)
		response.Offset = offset
	}
	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}
	response.TotalCount = len(entries)
	response.Count = end - offset
	response.HasMore = end < len(entries)
	response.NextOffset = end

	superseded := make(map[string]bool, len(allEntries))
	for _, entry := range allEntries {
		if entry.SupersedesID != "" {
			superseded[entry.SupersedesID] = true
		}
	}
	conflictDigests := make(map[string]map[string]bool)
	for _, entry := range allEntries {
		if entry.ConflictKey != "" && !superseded[entry.ID] {
			if conflictDigests[entry.ConflictKey] == nil {
				conflictDigests[entry.ConflictKey] = make(map[string]bool)
			}
			conflictDigests[entry.ConflictKey][entry.ContentSHA256] = true
		}
	}
	conflicted := make(map[string]bool)
	for _, entry := range allEntries {
		if entry.ConflictKey != "" && len(conflictDigests[entry.ConflictKey]) > 1 && !superseded[entry.ID] {
			conflicted[entry.ID] = true
		}
	}
	if len(conflicted) > 0 {
		response.Status = "conflicting"
		response.Gaps = append(response.Gaps, "one or more indexed conflict groups contain different approved content; read neither as authoritative until reviewed")
	}
	for _, entry := range entries[offset:end] {
		state := "approved"
		if superseded[entry.ID] {
			state = "superseded"
		} else if conflicted[entry.ID] {
			state = "conflicting"
		}
		response.Records = append(response.Records, projectMemoryIndexItem{
			ID: entry.ID, Kind: entry.Kind, Revision: entry.Revision, Title: entry.Title,
			Applicability: entry.Applicability, ContentSHA256: entry.ContentSHA256,
			UpdatedAt: entry.UpdatedAt, State: state, ConflictKey: entry.ConflictKey,
			SupersedesID: entry.SupersedesID,
		})
	}
	return response, nil
}

func (s *Server) readProjectMemory(projectID string, kind projectmemory.Kind, id string, paths, modules []string, freshnessAware bool) (projectMemoryReadResponse, error) {
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 || privacy.ContainsCredentialText(projectID) ||
		!projectMemoryIDPattern.MatchString(id) {
		return projectMemoryReadResponse{}, errors.New("project_id and a stable record id are required")
	}
	if kind != projectmemory.KindFact && kind != projectmemory.KindDecision && kind != projectmemory.KindPolicy {
		return projectMemoryReadResponse{}, errors.New("kind must be fact, decision, or policy")
	}
	if len(paths) > 64 || len(modules) > 64 {
		return projectMemoryReadResponse{}, errors.New("paths and modules must contain no more than 64 entries")
	}
	for _, path := range paths {
		if err := proceduregraph.ValidateRepositoryPath(path); err != nil {
			return projectMemoryReadResponse{}, errors.New("paths must be safe repository-relative paths")
		}
	}
	for _, module := range modules {
		if strings.TrimSpace(module) == "" || len(module) > 128 {
			return projectMemoryReadResponse{}, errors.New("modules must contain non-empty identifiers of at most 128 characters")
		}
	}
	response := projectMemoryReadResponse{
		SchemaVersion: projectMemoryReadSchema, Status: "incomplete", Freshness: "not_tracked_by_project_memory_v1",
		ProjectID: projectID, ID: id, Kind: kind, Gaps: []string{},
		ActionReady: false, GuidanceOnly: true, ExecutionAuthorized: false,
	}
	if freshnessAware {
		response.SchemaVersion = projectfiles.ProjectMemoryReadV2SchemaVersion
		response.Freshness = "unverified"
	}
	if s.memory == nil {
		response.Gaps = append(response.Gaps, "version-controlled project memory is not configured for this MCP server")
		return response, nil
	}
	if freshnessAware {
		return s.memory.ReadV2(projectID, kind, id, paths, modules), nil
	}
	index, err := s.memory.IndexProject(projectID, "")
	if err != nil {
		response.Status = "privacy_blocked"
		response.Gaps = append(response.Gaps, "project memory is unavailable or failed integrity/privacy validation")
		return response, nil
	}
	var indexed *projectfiles.IndexEntry
	for i := range index {
		if index[i].ID == id && index[i].Kind == kind {
			indexed = &index[i]
			break
		}
	}
	if indexed == nil {
		response.Gaps = append(response.Gaps, "requested project-memory record is unavailable in this project")
		return response, nil
	}
	response.Revision = indexed.Revision
	if memoryRecordSuperseded(*indexed, index) {
		response.Gaps = append(response.Gaps, "requested project-memory record is superseded; use its approved replacement")
		return response, nil
	}
	if memoryConflict(*indexed, index) {
		response.Status = "conflicting"
		response.Gaps = append(response.Gaps, "requested project-memory record conflicts with another approved record; neither is returned as authoritative")
		return response, nil
	}
	if missingMemoryScope(indexed.Applicability, paths, modules) {
		response.Gaps = append(response.Gaps, "path or module context required by this record is missing")
		return response, nil
	}
	if !projectmemory.Applicable(projectmemory.Record{Applicability: indexed.Applicability}, paths, modules) {
		response.Gaps = append(response.Gaps, "requested project-memory record does not apply to the supplied path or module scope")
		return response, nil
	}
	record, err := s.memory.GetLatest(kind, id)
	if err != nil || record.ProjectID != projectID {
		response.Status = "privacy_blocked"
		response.Gaps = append(response.Gaps, "project memory changed or failed integrity/privacy validation during read")
		return response, nil
	}
	if !sameProjectMemoryIndex(record, *indexed) {
		response.Gaps = append(response.Gaps, "project-memory revision changed during read; retry with a fresh index")
		return response, nil
	}
	response.Status = "complete"
	response.Record = &record
	return response, nil
}

func sameProjectMemoryIndex(record projectmemory.Record, entry projectfiles.IndexEntry) bool {
	return record.ID == entry.ID && record.ProjectID == entry.ProjectID && record.Kind == entry.Kind &&
		record.Title == entry.Title && record.Revision == entry.Revision &&
		reflect.DeepEqual(record.Applicability, entry.Applicability) && record.ConflictKey == entry.ConflictKey &&
		record.SupersedesID == entry.SupersedesID && record.ContentSHA256 == entry.ContentSHA256 && record.UpdatedAt == entry.UpdatedAt
}

func missingMemoryScope(scope projectmemory.Applicability, paths, modules []string) bool {
	return (len(scope.Paths) > 0 && len(paths) == 0) || (len(scope.Modules) > 0 && len(modules) == 0)
}

func memoryRecordSuperseded(entry projectfiles.IndexEntry, records []projectfiles.IndexEntry) bool {
	for _, candidate := range records {
		if candidate.Kind == entry.Kind && candidate.SupersedesID == entry.ID {
			return true
		}
	}
	return false
}

func memoryConflict(entry projectfiles.IndexEntry, records []projectfiles.IndexEntry) bool {
	if entry.ConflictKey == "" {
		return false
	}
	digests := map[string]bool{}
	for _, candidate := range records {
		if candidate.ConflictKey == entry.ConflictKey && !memoryRecordSuperseded(candidate, records) {
			digests[candidate.ContentSHA256] = true
		}
	}
	return len(digests) > 1
}

func validateProjectMemoryListArguments(kind string, limit, offset int) (projectmemory.Kind, int, int, error) {
	parsed := projectmemory.Kind(strings.ToLower(strings.TrimSpace(kind)))
	if parsed != "" && parsed != projectmemory.KindFact && parsed != projectmemory.KindDecision && parsed != projectmemory.KindPolicy {
		return "", 0, 0, fmt.Errorf("kind must be fact, decision, policy, or empty")
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return "", 0, 0, errors.New("limit must be 1-100 and offset non-negative")
	}
	return parsed, limit, offset, nil
}
