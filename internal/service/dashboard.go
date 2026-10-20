package service

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	runtimeinventory "github.com/threadforge-ai/threadforge/internal/inventory"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/internal/traces"
	"github.com/threadforge-ai/threadforge/pkg/continuity"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
)

const dashboardTaskLimit = 8

//go:embed dashboard/*
var dashboardAssets embed.FS

type DashboardResponse struct {
	SchemaVersion     string                      `json:"schema_version"`
	ProjectID         string                      `json:"project_id,omitempty"`
	Traces            traces.DashboardStats       `json:"traces"`
	Procedures        *ProcedureCounts            `json:"procedures,omitempty"`
	Tasks             *store.ContinuityTaskCounts `json:"tasks,omitempty"`
	RecentTasks       []TaskProgressItem          `json:"recent_tasks,omitempty"`
	MigrationsByState map[string]int              `json:"migrations_by_state"`
}

type TaskProgressItem struct {
	ID           string                `json:"id"`
	Title        string                `json:"title"`
	Status       continuity.TaskStatus `json:"status"`
	Requirements string                `json:"requirements_state"`
	UpdatedAt    string                `json:"updated_at"`
	NextAction   string                `json:"next_action,omitempty"`
}

type ProcedureCounts struct {
	Total     int `json:"total"`
	Draft     int `json:"draft"`
	Validated int `json:"validated"`
	Approved  int `json:"approved"`
	Rejected  int `json:"rejected"`
	Stale     int `json:"stale"`
}

// InventorySummary deliberately contains counts and scan health only. Local
// configuration paths, runtime names, hashes, and MCP definitions stay out of
// the dashboard API response.
type InventorySummary struct {
	SchemaVersion          string `json:"schema_version"`
	GeneratedAt            string `json:"generated_at"`
	Complete               bool   `json:"complete"`
	ConfiguredRuntimeCount int    `json:"configured_runtime_count"`
	ConfigFileCount        int    `json:"config_file_count"`
	MCPServerCount         int    `json:"mcp_server_count"`
	SkillManifestCount     int    `json:"skill_manifest_count"`
	IncompleteSourceCount  int    `json:"incomplete_source_count"`
}

type InventoryScopeCount struct {
	Scope string `json:"scope"`
	Count int    `json:"count"`
}

type RuntimeInventorySummary struct {
	Runtime               string                `json:"runtime"`
	ConfigFileCount       int                   `json:"config_file_count"`
	ConfigFilesByScope    []InventoryScopeCount `json:"config_files_by_scope"`
	MCPServerCount        int                   `json:"mcp_server_count"`
	SkillManifestCount    int                   `json:"skill_manifest_count"`
	SkillManifestsByScope []InventoryScopeCount `json:"skill_manifests_by_scope"`
	IncompleteSourceCount int                   `json:"incomplete_source_count"`
}

type InventoryDetailsSummary struct {
	InventorySummary
	Runtimes []RuntimeInventorySummary `json:"runtimes"`
}

type runtimeInventoryCounts struct {
	configScopes    map[string]int
	skillScopes     map[string]int
	mcpServers      int
	incompleteCount int
}

func summarizeInventory(result runtimeinventory.Result) InventorySummary {
	runtimes := map[string]bool{}
	configFiles, incomplete := 0, 0
	for _, config := range result.Configs {
		if config.Exists {
			configFiles++
			runtimes[config.Runtime] = true
		}
		if inventorySourceIncomplete(config.ParserStatus) {
			incomplete++
		}
	}
	skillCount := 0
	for _, skill := range result.Skills {
		if skill.Exists && skill.SkillNameHash != "" {
			skillCount++
		}
		if inventorySourceIncomplete(skill.ParserStatus) {
			incomplete++
		}
	}
	return InventorySummary{
		SchemaVersion: "threadforge.inventory-summary.v1", GeneratedAt: result.GeneratedAt,
		Complete: incomplete == 0, ConfiguredRuntimeCount: len(runtimes), ConfigFileCount: configFiles,
		MCPServerCount: len(result.MCPServers), SkillManifestCount: skillCount, IncompleteSourceCount: incomplete,
	}
}

func summarizeInventoryDetails(result runtimeinventory.Result) InventoryDetailsSummary {
	base := summarizeInventory(result)
	base.SchemaVersion = "threadforge.inventory-summary.v2"
	byRuntime := map[string]*runtimeInventoryCounts{}
	get := func(runtime string) *runtimeInventoryCounts {
		if !safeInventoryRuntime(runtime) {
			return nil
		}
		counts := byRuntime[runtime]
		if counts == nil {
			counts = &runtimeInventoryCounts{configScopes: map[string]int{}, skillScopes: map[string]int{}}
			byRuntime[runtime] = counts
		}
		return counts
	}
	for _, config := range result.Configs {
		counts := get(config.Runtime)
		if counts == nil {
			continue
		}
		if config.Exists {
			counts.configScopes[safeInventoryScope(config.Scope)]++
		}
		if inventorySourceIncomplete(config.ParserStatus) {
			counts.incompleteCount++
		}
	}
	for _, server := range result.MCPServers {
		if counts := get(server.Runtime); counts != nil {
			counts.mcpServers++
		}
	}
	for _, skill := range result.Skills {
		counts := get(skill.Runtime)
		if counts == nil {
			continue
		}
		if skill.Exists && skill.SkillNameHash != "" {
			counts.skillScopes[safeInventoryScope(skill.SourceScope)]++
		}
		if inventorySourceIncomplete(skill.ParserStatus) {
			counts.incompleteCount++
		}
	}

	runtimeNames := make([]string, 0, len(byRuntime))
	for runtime := range byRuntime {
		runtimeNames = append(runtimeNames, runtime)
	}
	sort.Strings(runtimeNames)
	runtimes := make([]RuntimeInventorySummary, 0, len(runtimeNames))
	for _, runtime := range runtimeNames {
		counts := byRuntime[runtime]
		configScopes := inventoryScopeCounts(counts.configScopes)
		skillScopes := inventoryScopeCounts(counts.skillScopes)
		configTotal, skillTotal := 0, 0
		for _, item := range configScopes {
			configTotal += item.Count
		}
		for _, item := range skillScopes {
			skillTotal += item.Count
		}
		if configTotal == 0 && skillTotal == 0 && counts.mcpServers == 0 && counts.incompleteCount == 0 {
			continue
		}
		runtimes = append(runtimes, RuntimeInventorySummary{
			Runtime: runtime, ConfigFileCount: configTotal, ConfigFilesByScope: configScopes,
			MCPServerCount: counts.mcpServers, SkillManifestCount: skillTotal,
			SkillManifestsByScope: skillScopes, IncompleteSourceCount: counts.incompleteCount,
		})
	}
	return InventoryDetailsSummary{InventorySummary: base, Runtimes: runtimes}
}

func inventoryScopeCounts(counts map[string]int) []InventoryScopeCount {
	scopes := make([]string, 0, len(counts))
	for scope, count := range counts {
		if count > 0 {
			scopes = append(scopes, scope)
		}
	}
	sort.Strings(scopes)
	items := make([]InventoryScopeCount, 0, len(scopes))
	for _, scope := range scopes {
		items = append(items, InventoryScopeCount{Scope: scope, Count: counts[scope]})
	}
	return items
}

func safeInventoryRuntime(runtime string) bool {
	if len(runtime) == 0 || len(runtime) > 64 {
		return false
	}
	for _, char := range runtime {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func safeInventoryScope(scope string) string {
	switch scope {
	case runtimeinventory.ScopeUser, runtimeinventory.ScopeProject, runtimeinventory.ScopeManaged,
		runtimeinventory.ScopeSystem, runtimeinventory.ScopeWorkspace:
		return scope
	default:
		return runtimeinventory.ScopeUnknown
	}
}

func inventorySourceIncomplete(status string) bool {
	switch status {
	case runtimeinventory.StatusPartial, runtimeinventory.StatusParseFailed, runtimeinventory.StatusUnreadable, runtimeinventory.StatusUnsupported:
		return true
	default:
		return false
	}
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := h.traces.DashboardStats(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	response := DashboardResponse{
		SchemaVersion: "threadforge.dashboard.v1", Traces: stats,
		MigrationsByState: map[string]int{},
	}
	migrations, err := h.store.ListMigrations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, migration := range migrations {
		response.MigrationsByState[migration.Status]++
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if len(projectID) > 256 {
		writeError(w, http.StatusBadRequest, errors.New("project_id exceeds 256 characters"))
		return
	}
	if projectID != "" {
		taskCounts, err := h.store.TaskCounts(r.Context(), projectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		tasks, _, err := h.store.ListTasks(r.Context(), projectID, dashboardTaskLimit, 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		recentTasks := make([]TaskProgressItem, 0, len(tasks))
		for _, task := range tasks {
			requirementsState := "not_recorded"
			if task.Requirements != nil {
				requirementsState = string(task.Requirements.Status)
			}
			recentTasks = append(recentTasks, TaskProgressItem{
				ID: task.ID, Title: task.Title, Status: task.Status,
				Requirements: requirementsState, UpdatedAt: task.UpdatedAt,
				NextAction: task.NextAction,
			})
		}
		graphs, err := h.store.ListProject(r.Context(), projectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		counts := &ProcedureCounts{Total: len(graphs)}
		for _, graph := range graphs {
			switch graph.Status {
			case proceduregraph.StatusDraft:
				counts.Draft++
			case proceduregraph.StatusValidated:
				counts.Validated++
			case proceduregraph.StatusApproved:
				counts.Approved++
			case proceduregraph.StatusRejected:
				counts.Rejected++
			case proceduregraph.StatusStale:
				counts.Stale++
			}
		}
		response.ProjectID = projectID
		response.Procedures = counts
		response.Tasks = &taskCounts
		response.RecentTasks = recentTasks
	}
	writeJSON(w, http.StatusOK, response)
}

func dashboardHandler() (http.Handler, error) {
	assets, err := fs.Sub(dashboardAssets, "dashboard")
	if err != nil {
		return nil, err
	}
	return http.FileServer(http.FS(assets)), nil
}
