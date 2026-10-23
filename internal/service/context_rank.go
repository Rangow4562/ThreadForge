package service

import (
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

type rankedProcedure struct {
	graph proceduregraph.Graph
	score int
}

type rankedMemory struct {
	record projectmemory.Record
	score  int
}

// rankProcedureGraphs makes context selection deterministic and relevance-first.
// The score is internal: the API returns no confidence estimate or matched text.
func rankProcedureGraphs(graphs []proceduregraph.Graph, query string) []proceduregraph.Graph {
	ranked := make([]rankedProcedure, 0, len(graphs))
	for _, graph := range graphs {
		if graph.Status != proceduregraph.StatusApproved {
			continue
		}
		score, matched := procedureContextRelevanceScore(graph, query)
		if matched {
			ranked = append(ranked, rankedProcedure{graph: graph, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].graph.ID != ranked[j].graph.ID {
			return ranked[i].graph.ID < ranked[j].graph.ID
		}
		return ranked[i].graph.Revision > ranked[j].graph.Revision
	})
	result := make([]proceduregraph.Graph, len(ranked))
	for index := range ranked {
		result[index] = ranked[index].graph
	}
	return result
}

// rankProjectMemories applies the same relevance policy as procedure retrieval.
func rankProjectMemories(records []projectmemory.Record, query string) []projectmemory.Record {
	ranked := make([]rankedMemory, 0, len(records))
	for _, record := range records {
		score, matched := projectMemoryContextRelevanceScore(record, query)
		if matched {
			ranked = append(ranked, rankedMemory{record: record, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].record.ID != ranked[j].record.ID {
			return ranked[i].record.ID < ranked[j].record.ID
		}
		return ranked[i].record.Revision > ranked[j].record.Revision
	})
	result := make([]projectmemory.Record, len(ranked))
	for index := range ranked {
		result[index] = ranked[index].record
	}
	return result
}

func procedureContextRelevanceScore(graph proceduregraph.Graph, query string) (int, bool) {
	var metadata, instructions strings.Builder
	metadata.WriteString(graph.ID)
	writeSearchFields(&metadata, graph.Applicability.Modules)
	writeSearchFields(&metadata, graph.Applicability.Paths)
	for _, node := range graph.Nodes {
		writeSearchFields(&metadata, []string{node.ID, node.Module})
		writeSearchFields(&metadata, node.Paths)
		instructions.WriteByte(' ')
		instructions.WriteString(node.Instruction)
	}
	return contextRelevanceScore(query, graph.Title, metadata.String(), instructions.String())
}

func projectMemoryContextRelevanceScore(record projectmemory.Record, query string) (int, bool) {
	var metadata strings.Builder
	writeSearchFields(&metadata, []string{record.ID, string(record.Kind)})
	writeSearchFields(&metadata, record.Applicability.Modules)
	writeSearchFields(&metadata, record.Applicability.Paths)
	return contextRelevanceScore(query, record.Title, metadata.String(), string(record.Content))
}

func writeSearchFields(builder *strings.Builder, values []string) {
	for _, value := range values {
		builder.WriteByte(' ')
		builder.WriteString(value)
	}
}

// contextRelevanceScore rewards exact query phrases and distinct term coverage
// in more identifying fields. Repetition never increases a score. A wildcard
// retains project-wide bootstrap behavior and is ordered by stable record ID.
func contextRelevanceScore(query, title, metadata, content string) (int, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "*" {
		return 0, true
	}
	if query == "" {
		return 0, false
	}

	title = strings.ToLower(title)
	metadata = strings.ToLower(metadata)
	content = strings.ToLower(content)
	score := 0
	if strings.Contains(title, query) {
		score += 24
	}
	if strings.Contains(metadata, query) {
		score += 12
	}
	if strings.Contains(content, query) {
		score += 4
	}

	seen := make(map[string]struct{})
	for _, term := range queryTerms(query) {
		if len(term) <= 2 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		switch {
		case strings.Contains(title, term):
			score += 8
		case strings.Contains(metadata, term):
			score += 3
		case strings.Contains(content, term):
			score++
		}
	}
	return score, score > 0
}
