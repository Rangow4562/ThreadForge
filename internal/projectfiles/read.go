package projectfiles

import (
	"reflect"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

const ProjectMemoryReadV2SchemaVersion = "threadforge.project-memory-read.v2"

// ReadV2 returns one exact project-memory record with server-side scope,
// conflict, integrity, and source-freshness checks.
func (s *Store) ReadV2(projectID string, kind projectmemory.Kind, id string, paths, modules []string) projectmemory.ReadResponseV2 {
	response := projectmemory.ReadResponseV2{
		SchemaVersion: ProjectMemoryReadV2SchemaVersion,
		Status:        "incomplete", Freshness: "unverified", ProjectID: projectID, ID: id, Kind: kind,
		Gaps: []string{}, ActionReady: false, GuidanceOnly: true, ExecutionAuthorized: false,
	}
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 || privacy.ContainsCredentialText(projectID) || !validID(id) {
		response.ProjectID = ""
		response.ID = ""
		response.Gaps = append(response.Gaps, "a valid project_id and stable record id are required")
		return response
	}
	if kind != projectmemory.KindFact && kind != projectmemory.KindDecision && kind != projectmemory.KindPolicy {
		response.Kind = ""
		response.Gaps = append(response.Gaps, "kind must be fact, decision, or policy")
		return response
	}
	if len(paths) > 64 || len(modules) > 64 {
		response.Gaps = append(response.Gaps, "paths and modules must contain no more than 64 entries")
		return response
	}
	for _, currentPath := range paths {
		if proceduregraph.ValidateRepositoryPath(currentPath) != nil || privacy.ContainsCredentialPath(currentPath) {
			response.Gaps = append(response.Gaps, "paths must be safe repository-relative paths")
			return response
		}
	}
	for _, module := range modules {
		if strings.TrimSpace(module) == "" || len(module) > 128 || privacy.ContainsCredentialText(module) {
			response.Gaps = append(response.Gaps, "modules must contain safe non-empty identifiers of at most 128 characters")
			return response
		}
	}

	entries, err := s.IndexProject(projectID, "")
	if err != nil {
		response.Status = "privacy_blocked"
		response.Gaps = append(response.Gaps, "project memory is unavailable or failed integrity/privacy validation")
		return response
	}
	var indexed *IndexEntry
	for index := range entries {
		if entries[index].ID == id && entries[index].Kind == kind {
			indexed = &entries[index]
			break
		}
	}
	if indexed == nil {
		response.Gaps = append(response.Gaps, "requested project-memory record is unavailable in this project")
		return response
	}
	response.Revision = indexed.Revision
	if recordSuperseded(*indexed, entries) {
		response.Gaps = append(response.Gaps, "requested project-memory record is superseded; use its approved replacement")
		return response
	}
	if recordConflicting(*indexed, entries) {
		response.Status = "conflicting"
		response.Gaps = append(response.Gaps, "requested project-memory record conflicts with another approved record; neither is returned as authoritative")
		return response
	}
	if missingScope(indexed.Applicability, paths, modules) {
		response.Gaps = append(response.Gaps, "path or module context required by this record is missing")
		return response
	}
	if !projectmemory.Applicable(projectmemory.Record{Applicability: indexed.Applicability}, paths, modules) {
		response.Gaps = append(response.Gaps, "requested project-memory record does not apply to the supplied path or module scope")
		return response
	}
	record, err := s.GetLatest(kind, id)
	if err != nil || record.ProjectID != projectID {
		response.Status = "privacy_blocked"
		response.Gaps = append(response.Gaps, "project memory changed or failed integrity/privacy validation during read")
		return response
	}
	if !sameIndex(record, *indexed) {
		response.Gaps = append(response.Gaps, "project-memory revision changed during read; retry with a fresh index")
		return response
	}
	response.Freshness = s.CheckFreshness(record)
	switch response.Freshness {
	case "verified":
		response.Status = "complete"
		response.Record = &record
	case "stale":
		response.Status = "stale"
		response.Gaps = append(response.Gaps, "one or more evidence source files changed since this memory revision was approved; content was withheld")
	case "incomplete":
		response.Gaps = append(response.Gaps, "evidence source files could not be safely fingerprinted from this project; content was withheld")
	default:
		response.Gaps = append(response.Gaps, "this memory revision has no source-file fingerprints; approval is not evidence of current source freshness")
		response.Record = &record
	}
	return response
}

func sameIndex(record projectmemory.Record, entry IndexEntry) bool {
	return record.ID == entry.ID && record.ProjectID == entry.ProjectID && record.Kind == entry.Kind &&
		record.Title == entry.Title && record.Revision == entry.Revision &&
		reflect.DeepEqual(record.Applicability, entry.Applicability) && record.ConflictKey == entry.ConflictKey &&
		record.SupersedesID == entry.SupersedesID && record.ContentSHA256 == entry.ContentSHA256 && record.UpdatedAt == entry.UpdatedAt
}

func missingScope(scope projectmemory.Applicability, paths, modules []string) bool {
	return (len(scope.Paths) > 0 && len(paths) == 0) || (len(scope.Modules) > 0 && len(modules) == 0)
}

func recordSuperseded(entry IndexEntry, records []IndexEntry) bool {
	for _, candidate := range records {
		if candidate.Kind == entry.Kind && candidate.SupersedesID == entry.ID {
			return true
		}
	}
	return false
}

func recordConflicting(entry IndexEntry, records []IndexEntry) bool {
	if entry.ConflictKey == "" {
		return false
	}
	digests := map[string]bool{}
	for _, candidate := range records {
		if candidate.ConflictKey == entry.ConflictKey && !recordSuperseded(candidate, records) {
			digests[candidate.ContentSHA256] = true
		}
	}
	return len(digests) > 1
}
