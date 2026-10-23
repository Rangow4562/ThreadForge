package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/procedurefiles"
	"github.com/threadforge-ai/threadforge/internal/projectfiles"
	securityscan "github.com/threadforge-ai/threadforge/internal/security"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/internal/traces"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

const (
	maxRequestBytes           = 8 << 20
	maxEventBatch             = 200
	maxDashboardFindings      = 100
	maxDashboardFindingEvents = 20
	ContextRequestV1          = "threadforge.context-request.v1"
	ContextRequestV2          = "threadforge.context-request.v2"
	ContextResponseV1         = "threadforge.context-response.v1"
	ContextResponseV2         = "threadforge.context-response.v2"
)

type securityScanResponse struct {
	SchemaVersion          string                   `json:"schema_version"`
	Complete               bool                     `json:"complete"`
	EventCount             int                      `json:"event_count"`
	MalformedLines         int                      `json:"malformed_lines"`
	RuleCount              int                      `json:"rule_count"`
	FindingCount           int                      `json:"finding_count"`
	FindingsBySeverity     map[string]int           `json:"findings_by_severity"`
	FindingDetailsIncluded bool                     `json:"finding_details_included"`
	ReviewStateSaved       bool                     `json:"review_state_saved"`
	FindingsTruncated      bool                     `json:"findings_truncated,omitempty"`
	Findings               *[]securityFindingDetail `json:"findings,omitempty"`
}

type securityFindingDetail struct {
	Fingerprint      string                 `json:"fingerprint"`
	RuleID           string                 `json:"rule_id"`
	Title            string                 `json:"title"`
	Severity         beacon.Severity        `json:"severity"`
	Posture          string                 `json:"posture"`
	Reason           string                 `json:"reason"`
	SessionReference string                 `json:"session_reference,omitempty"`
	ReviewStatus     string                 `json:"review_status"`
	ReviewRevision   int                    `json:"review_revision"`
	ReviewActive     bool                   `json:"review_active"`
	Events           []securityFindingEvent `json:"events"`
	EventsTruncated  bool                   `json:"events_truncated,omitempty"`
}

type securityFindingReviewRequest struct {
	SchemaVersion    string `json:"schema_version"`
	ExpectedRevision int    `json:"expected_revision"`
	Status           string `json:"status"`
	Actor            string `json:"actor,omitempty"`
}

type securityFindingReviewsResponse struct {
	SchemaVersion string                        `json:"schema_version"`
	Total         int                           `json:"total"`
	Limit         int                           `json:"limit"`
	Offset        int                           `json:"offset"`
	Reviews       []store.SecurityFindingReview `json:"reviews"`
}

type securityFindingEvent struct {
	Timestamp string `json:"timestamp"`
	Harness   string `json:"harness"`
	Action    string `json:"action"`
}

type Handler struct {
	store                *store.Store
	traces               *traces.Store
	memory               *projectfiles.Store
	projectProcedures    *procedurefiles.Store
	ruleStoreDir         string
	securityScanGate     chan struct{}
	inventoryScanGate    chan struct{}
	inventoryHome        string
	inventoryProjectRoot string
}

func NewHandler(procedures *store.Store, traceStore *traces.Store) http.Handler {
	return NewHandlerWithProjectStores(procedures, traceStore, nil, nil)
}

func NewHandlerWithProjectMemory(procedures *store.Store, traceStore *traces.Store, memory *projectfiles.Store) http.Handler {
	return NewHandlerWithProjectStores(procedures, traceStore, memory, nil)
}

func NewHandlerWithProjectStores(procedures *store.Store, traceStore *traces.Store, memory *projectfiles.Store, projectProcedures *procedurefiles.Store) http.Handler {
	return NewHandlerWithProjectStoresAndRules(procedures, traceStore, memory, projectProcedures, "")
}

func NewHandlerWithProjectStoresAndRules(procedures *store.Store, traceStore *traces.Store, memory *projectfiles.Store, projectProcedures *procedurefiles.Store, ruleStoreDir string) http.Handler {
	return NewHandlerWithProjectStoresRulesAndInventoryRoots(procedures, traceStore, memory, projectProcedures, ruleStoreDir, "", "")
}

func NewHandlerWithProjectStoresRulesAndInventoryRoots(procedures *store.Store, traceStore *traces.Store, memory *projectfiles.Store, projectProcedures *procedurefiles.Store, ruleStoreDir, inventoryHome, inventoryProjectRoot string) http.Handler {
	h := &Handler{
		store: procedures, traces: traceStore, memory: memory, projectProcedures: projectProcedures,
		ruleStoreDir: ruleStoreDir, securityScanGate: make(chan struct{}, 1), inventoryScanGate: make(chan struct{}, 1),
		inventoryHome: inventoryHome, inventoryProjectRoot: inventoryProjectRoot,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /v1/dashboard", h.dashboard)
	mux.HandleFunc("GET /v1/security/scan", h.securityScan)
	mux.HandleFunc("POST /v1/security/scan", h.persistSecurityScan)
	mux.HandleFunc("GET /v1/security/finding-reviews", h.listSecurityFindingReviews)
	mux.HandleFunc("POST /v1/security/finding-reviews/{fingerprint}", h.updateSecurityFindingReview)
	mux.HandleFunc("GET /v1/security/finding-reviews/{fingerprint}/history", h.securityFindingReviewHistory)
	mux.HandleFunc("POST /v1/inventory/scan", h.inventoryScan)
	mux.HandleFunc("POST /v2/inventory/scan", h.inventoryScanDetails)
	mux.HandleFunc("POST /v1/events", h.ingestEvents)
	mux.HandleFunc("POST /v1/logs", h.ingestOTLPLogs)
	mux.HandleFunc("POST /v1/traces", h.ingestOTLPTraces)
	mux.HandleFunc("POST /v1/metrics", h.ingestOTLPMetrics)
	mux.HandleFunc("GET /v1/metrics/catalog", h.metricCatalog)
	mux.HandleFunc("GET /v1/metrics/timeseries", h.metricTimeSeries)
	mux.HandleFunc("GET /v1/procedures", h.listProcedures)
	mux.HandleFunc("GET /v1/procedures/{id}", h.getProcedure)
	mux.HandleFunc("GET /v1/tasks", h.listTasks)
	mux.HandleFunc("GET /v1/tasks/{id}", h.getTask)
	mux.HandleFunc("GET /v1/tasks/{id}/sessions", h.listTaskSessions)
	mux.HandleFunc("GET /v1/tasks/{id}/entries", h.listTaskEntries)
	mux.HandleFunc("GET /v1/tasks/{id}/history", h.taskHistory)
	mux.HandleFunc("GET /v1/sessions/{id}", h.getSession)
	mux.HandleFunc("GET /v1/sessions/{id}/history", h.sessionHistory)
	mux.HandleFunc("POST /v1/context", h.contextV1)
	mux.HandleFunc("POST /v2/context", h.contextV2)
	dashboard, err := dashboardHandler()
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /dashboard", http.RedirectHandler("/dashboard/", http.StatusTemporaryRedirect))
	mux.Handle("GET /dashboard/", http.StripPrefix("/dashboard/", dashboard))
	return securityHeaders(mux)
}

func (h *Handler) securityScan(w http.ResponseWriter, r *http.Request) {
	h.runSecurityScan(w, r, false)
}

func (h *Handler) persistSecurityScan(w http.ResponseWriter, r *http.Request) {
	h.runSecurityScan(w, r, true)
}

func (h *Handler) runSecurityScan(w http.ResponseWriter, r *http.Request, persist bool) {
	if !sameLocalOrigin(r) {
		writeError(w, http.StatusForbidden, errors.New("security scan is restricted to same-origin requests"))
		return
	}
	includeFindings, err := requestedSecurityFindingDetails(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	select {
	case h.securityScanGate <- struct{}{}:
		defer func() { <-h.securityScanGate }()
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("a local security scan is already running"))
		return
	}
	query := r.URL.Query()
	minimumValues, minimumPresent := query["min_severity"]
	if minimumPresent && len(minimumValues) != 1 {
		writeError(w, http.StatusBadRequest, errors.New("min_severity must be specified once"))
		return
	}
	minimumValue := ""
	if minimumPresent {
		minimumValue = minimumValues[0]
	}
	minimum := beacon.Severity(strings.ToLower(strings.TrimSpace(minimumValue)))
	if minimum == "" {
		minimum = beacon.SeverityInfo
	}
	switch minimum {
	case beacon.SeverityInfo, beacon.SeverityLow, beacon.SeverityMedium, beacon.SeverityHigh, beacon.SeverityCritical:
	default:
		writeError(w, http.StatusBadRequest, errors.New("min_severity must be info, low, medium, high, or critical"))
		return
	}
	sessionValues, sessionPresent := query["session"]
	if sessionPresent && len(sessionValues) != 1 {
		writeError(w, http.StatusBadRequest, errors.New("session filter must be specified once"))
		return
	}
	sessionFilter := ""
	if sessionPresent {
		sessionFilter = strings.TrimSpace(sessionValues[0])
	}
	if len(sessionFilter) > securityscan.MaxSessionFilterBytes {
		writeError(w, http.StatusBadRequest, fmt.Errorf("session filter must not exceed %d UTF-8 bytes", securityscan.MaxSessionFilterBytes))
		return
	}
	result, err := securityscan.ScanFileWithFilters(r.Context(), h.traces.Path(), h.ruleStoreDir, "", minimum, sessionFilter, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("local security scan failed; check the local service logs"))
		return
	}
	reviewStateSaved := persist && result.Complete && minimum == beacon.SeverityInfo && sessionFilter == ""
	if reviewStateSaved {
		observations := make([]store.SecurityFindingObservation, 0, len(result.Findings))
		for _, finding := range result.Findings {
			observations = append(observations, store.SecurityFindingObservation{
				Fingerprint: securityscan.FindingFingerprint(finding), RuleID: finding.RuleID, Severity: string(finding.Severity),
			})
		}
		if err := h.store.ReconcileSecurityFindingReviews(r.Context(), observations, time.Now()); err != nil {
			writeError(w, http.StatusInternalServerError, errors.New("local security review state could not be recorded"))
			return
		}
	}
	response := summarizeSecurityScan(result, includeFindings)
	response.ReviewStateSaved = reviewStateSaved
	if includeFindings && response.Findings != nil {
		for index := range *response.Findings {
			finding := &(*response.Findings)[index]
			review, err := h.store.GetSecurityFindingReview(r.Context(), finding.Fingerprint)
			if errors.Is(err, store.ErrSecurityFindingReviewNotFound) {
				continue
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, errors.New("local security review state could not be read"))
				return
			}
			finding.ReviewStatus = review.Status
			finding.ReviewRevision = review.Revision
			finding.ReviewActive = review.Active
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func sameLocalOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	return origin == "" || origin == "http://"+r.Host
}

func (h *Handler) listSecurityFindingReviews(w http.ResponseWriter, r *http.Request) {
	if !sameLocalOrigin(r) {
		writeError(w, http.StatusForbidden, errors.New("security review reads are restricted to same-origin requests"))
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("limit must be an integer from 1 to 200"))
			return
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("offset must be a non-negative integer"))
			return
		}
		offset = parsed
	}
	reviews, total, err := h.store.ListSecurityFindingReviews(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, securityFindingReviewsResponse{
		SchemaVersion: "threadforge.security-finding-reviews.v1", Total: total,
		Limit: limit, Offset: offset, Reviews: reviews,
	})
}

func (h *Handler) updateSecurityFindingReview(w http.ResponseWriter, r *http.Request) {
	if !sameLocalOrigin(r) {
		writeError(w, http.StatusForbidden, errors.New("security review updates are restricted to same-origin requests"))
		return
	}
	var request securityFindingReviewRequest
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.SchemaVersion != "threadforge.security-review-request.v1" {
		writeError(w, http.StatusBadRequest, errors.New("unsupported security review request schema"))
		return
	}
	actor := strings.TrimSpace(request.Actor)
	if actor == "" {
		actor = "local-user"
	}
	updated, err := h.store.UpdateSecurityFindingReview(
		r.Context(), r.PathValue("fingerprint"), request.ExpectedRevision, request.Status, actor, time.Now(),
	)
	switch {
	case errors.Is(err, store.ErrSecurityFindingReviewNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrSecurityFindingReviewConflict):
		writeError(w, http.StatusConflict, err)
	case err != nil:
		writeError(w, http.StatusBadRequest, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": "threadforge.security-review-response.v1", "review": updated,
		})
	}
}

func (h *Handler) securityFindingReviewHistory(w http.ResponseWriter, r *http.Request) {
	if !sameLocalOrigin(r) {
		writeError(w, http.StatusForbidden, errors.New("security review history is restricted to same-origin requests"))
		return
	}
	history, err := h.store.SecurityFindingReviewHistory(r.Context(), r.PathValue("fingerprint"))
	if errors.Is(err, store.ErrSecurityFindingReviewNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "threadforge.security-finding-review-history.v1", "events": history,
	})
}

func requestedSecurityFindingDetails(r *http.Request) (bool, error) {
	values, present := r.URL.Query()["include_findings"]
	if !present {
		return false, nil
	}
	if len(values) != 1 {
		return false, errors.New("include_findings must be specified once as true or false")
	}
	switch strings.ToLower(strings.TrimSpace(values[0])) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("include_findings must be true or false")
	}
}

func summarizeSecurityScan(result securityscan.ScanResult, includeFindings bool) securityScanResponse {
	counts := make(map[string]int)
	for _, finding := range result.Findings {
		counts[string(finding.Severity)]++
	}
	response := securityScanResponse{
		SchemaVersion: "threadforge.security-summary.v1", Complete: result.Complete,
		EventCount: result.EventCount, MalformedLines: result.MalformedLines,
		RuleCount: result.RuleCount, FindingCount: len(result.Findings),
		FindingsBySeverity: counts, FindingDetailsIncluded: includeFindings,
	}
	if !includeFindings {
		return response
	}
	limit := min(len(result.Findings), maxDashboardFindings)
	details := make([]securityFindingDetail, 0, limit)
	for _, finding := range result.Findings[:limit] {
		eventLimit := min(len(finding.Events), maxDashboardFindingEvents)
		item := securityFindingDetail{
			Fingerprint: securityscan.FindingFingerprint(finding),
			RuleID:      finding.RuleID, Title: finding.Title, Severity: finding.Severity,
			Posture: string(finding.Posture), Reason: finding.Reason,
			ReviewStatus:     store.SecurityReviewOpen,
			SessionReference: finding.SessionReference,
			Events:           make([]securityFindingEvent, 0, eventLimit),
			EventsTruncated:  len(finding.Events) > maxDashboardFindingEvents,
		}
		for _, event := range finding.Events[:eventLimit] {
			item.Events = append(item.Events, securityFindingEvent{
				Timestamp: event.At, Harness: event.Harness, Action: event.Action,
			})
		}
		details = append(details, item)
	}
	response.FindingsTruncated = len(result.Findings) > maxDashboardFindings
	response.Findings = &details
	return response
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if !isLoopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, errors.New("local service accepts loopback Host headers only"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(value string) bool {
	hostname, _, err := net.SplitHostPort(value)
	if err != nil {
		hostname = strings.Trim(value, "[]")
	}
	if strings.EqualFold(hostname, "localhost") || strings.HasSuffix(strings.ToLower(hostname), ".localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "api_version": "threadforge.local-api.v1",
		"procedure_schema": proceduregraph.SchemaVersion,
		"trace_schema":     beacon.SchemaVersion,
	})
}

func (h *Handler) ingestEvents(w http.ResponseWriter, r *http.Request) {
	data, err := readBoundedBody(w, r, maxRequestBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var events []beacon.Event
	if len(strings.TrimSpace(string(data))) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("request body is empty"))
		return
	}
	if strings.HasPrefix(strings.TrimSpace(string(data)), "[") {
		if err := decodeStrict(data, &events); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("decode event batch: %w", err))
			return
		}
	} else {
		var event beacon.Event
		if err := decodeStrict(data, &event); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("decode event: %w", err))
			return
		}
		events = []beacon.Event{event}
	}
	if len(events) == 0 || len(events) > maxEventBatch {
		writeError(w, http.StatusBadRequest, fmt.Errorf("event batch must contain 1 to %d events", maxEventBatch))
		return
	}
	if err := h.traces.Append(r.Context(), events); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": len(events), "schema_version": beacon.SchemaVersion})
}

func (h *Handler) ingestOTLPLogs(w http.ResponseWriter, r *http.Request) {
	h.ingestOTLP(w, r, decodeOTLPLogs, decodeOTLPProtobufLogs, "log-record")
}

func (h *Handler) ingestOTLPTraces(w http.ResponseWriter, r *http.Request) {
	h.ingestOTLP(w, r, decodeOTLPTraces, decodeOTLPProtobufTraces, "span")
}

func (h *Handler) ingestOTLPMetrics(w http.ResponseWriter, r *http.Request) {
	h.ingestOTLP(w, r, decodeOTLPMetrics, decodeOTLPProtobufMetrics, "metric data point")
}

func (h *Handler) ingestOTLP(w http.ResponseWriter, r *http.Request, decodeJSON, decodeProtobuf func([]byte) ([]beacon.Event, error), recordName string) {
	encoding, err := parseOTLPEncoding(r.Header.Get("Content-Type"))
	if err != nil {
		writeOTLPResponse(w, otlpJSON, http.StatusUnsupportedMediaType, "unsupported OTLP content type")
		return
	}
	data, err := readBoundedOTLPBody(r)
	if err != nil {
		writeOTLPResponse(w, encoding, http.StatusBadRequest, "invalid or oversized OTLP request body")
		return
	}
	decode := decodeJSON
	if encoding == otlpProtobuf {
		decode = decodeProtobuf
	}
	events, err := decode(data)
	if err != nil {
		writeOTLPResponse(w, encoding, http.StatusBadRequest, "invalid OTLP "+recordName+" export request")
		return
	}
	if len(events) > maxEventBatch {
		writeOTLPResponse(w, encoding, http.StatusBadRequest, fmt.Sprintf("OTLP request exceeds the %d %s limit", maxEventBatch, recordName))
		return
	}
	if len(events) > 0 {
		if err := h.traces.Append(r.Context(), events); err != nil {
			writeOTLPResponse(w, encoding, http.StatusInternalServerError, "Threadforge could not accept the OTLP export")
			return
		}
	}
	writeOTLPResponse(w, encoding, http.StatusOK, "")
}

func (h *Handler) listProcedures(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		writeError(w, http.StatusBadRequest, errors.New("project_id is required"))
		return
	}
	graphs, err := h.store.ListProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	approved := make([]proceduregraph.Graph, 0, len(graphs))
	for _, graph := range graphs {
		if graph.Status == proceduregraph.StatusApproved {
			agentGraph, err := proceduregraph.SanitizeForAgent(graph)
			if err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Errorf("sanitize approved procedure response: %w", err))
				return
			}
			approved = append(approved, agentGraph)
		}
	}
	writeJSON(w, http.StatusOK, approved)
}

func (h *Handler) getProcedure(w http.ResponseWriter, r *http.Request) {
	graph, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	if graph.Status != proceduregraph.StatusApproved {
		writeError(w, http.StatusNotFound, store.ErrNotFound)
		return
	}
	agentGraph, err := proceduregraph.SanitizeForAgent(graph)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("sanitize approved procedure response: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, agentGraph)
}

func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	projectID := strings.TrimSpace(query.Get("project_id"))
	limit, err := queryInt(query.Get("limit"), 50, 1, 200)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	offset, err := queryInt(query.Get("offset"), 0, 0, 1_000_000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if projectID == "" {
		writeError(w, http.StatusBadRequest, errors.New("project_id is required"))
		return
	}
	tasks, total, err := h.store.ListTasks(r.Context(), projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": "threadforge.continuity.v1", "total": total, "tasks": tasks})
}

func (h *Handler) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := h.store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) listTaskSessions(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, err := h.store.GetTask(r.Context(), taskID); err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	sessions, err := h.store.ListSessions(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": "threadforge.continuity.v1", "sessions": sessions})
}

func (h *Handler) listTaskEntries(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, err := h.store.GetTask(r.Context(), taskID); err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	limit, err := queryInt(r.URL.Query().Get("limit"), 100, 1, 1000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entries, err := h.store.ContinuityEntries(r.Context(), taskID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": "threadforge.continuity.v1", "entries": entries})
}

func (h *Handler) taskHistory(w http.ResponseWriter, r *http.Request) {
	revisions, err := h.store.TaskHistory(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": "threadforge.continuity.v1", "revisions": revisions})
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := h.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *Handler) sessionHistory(w http.ResponseWriter, r *http.Request) {
	revisions, err := h.store.SessionHistory(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": "threadforge.continuity.v1", "revisions": revisions})
}

func queryInt(raw string, fallback, minimum, maximum int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("query value must be an integer between %d and %d", minimum, maximum)
	}
	return value, nil
}

type ContextRequest struct {
	SchemaVersion    string                           `json:"schema_version"`
	ProjectID        string                           `json:"project_id"`
	Query            string                           `json:"query"`
	Paths            []string                         `json:"paths"`
	Modules          []string                         `json:"modules,omitempty"`
	PathFingerprints []proceduregraph.PathFingerprint `json:"path_fingerprints,omitempty"`
	MaxItems         int                              `json:"max_items"`
	MaxTokens        int                              `json:"max_tokens"`
}

type ContextResponse struct {
	SchemaVersion string                 `json:"schema_version"`
	Status        string                 `json:"status"`
	ActionReady   bool                   `json:"action_ready"`
	Procedures    []ProcedureContextItem `json:"procedures"`
	Memory        []MemoryContextItem    `json:"memory,omitempty"`
	Gaps          []string               `json:"gaps"`
}

func (response ContextResponse) MarshalJSON() ([]byte, error) {
	type v1Response struct {
		SchemaVersion string                 `json:"schema_version"`
		Status        string                 `json:"status"`
		ActionReady   bool                   `json:"action_ready"`
		Procedures    []ProcedureContextItem `json:"procedures"`
		Gaps          []string               `json:"gaps"`
	}
	type v2Response struct {
		SchemaVersion string                 `json:"schema_version"`
		Status        string                 `json:"status"`
		ActionReady   bool                   `json:"action_ready"`
		Procedures    []ProcedureContextItem `json:"procedures"`
		Memory        []MemoryContextItem    `json:"memory"`
		Gaps          []string               `json:"gaps"`
	}
	if response.SchemaVersion == ContextResponseV1 {
		return json.Marshal(v1Response{
			SchemaVersion: response.SchemaVersion, Status: response.Status, ActionReady: response.ActionReady,
			Procedures: response.Procedures, Gaps: response.Gaps,
		})
	}
	if response.Memory == nil {
		response.Memory = []MemoryContextItem{}
	}
	return json.Marshal(v2Response{
		SchemaVersion: response.SchemaVersion, Status: response.Status, ActionReady: response.ActionReady,
		Procedures: response.Procedures, Memory: response.Memory, Gaps: response.Gaps,
	})
}

type MemoryContextItem struct {
	ID       string                   `json:"id"`
	Kind     string                   `json:"kind"`
	Revision int                      `json:"revision"`
	Title    string                   `json:"title"`
	Content  json.RawMessage          `json:"content"`
	Evidence []projectmemory.Evidence `json:"evidence"`
}

type ProcedureContextItem struct {
	ID        string   `json:"id"`
	Revision  int      `json:"revision"`
	Title     string   `json:"title"`
	Freshness string   `json:"freshness,omitempty"`
	Guidance  []string `json:"guidance"`
}

func (h *Handler) contextV1(w http.ResponseWriter, r *http.Request) {
	h.serveContext(w, r, ContextRequestV1)
}

func (h *Handler) contextV2(w http.ResponseWriter, r *http.Request) {
	h.serveContext(w, r, ContextRequestV2)
}

func (h *Handler) serveContext(w http.ResponseWriter, r *http.Request, expectedVersion string) {
	var request ContextRequest
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.SchemaVersion != expectedVersion {
		writeError(w, http.StatusBadRequest, errors.New("context request schema does not match endpoint version"))
		return
	}
	response, err := RetrieveContextWithProjectRoot(
		r.Context(), h.store, h.memory, request, h.inventoryProjectRoot, h.projectProcedures,
	)
	if err != nil {
		var requestErr *ContextRequestError
		if errors.As(err, &requestErr) {
			writeError(w, http.StatusBadRequest, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func matchesPaths(graph proceduregraph.Graph, paths []string) bool {
	if len(graph.Applicability.Paths) > 0 && len(paths) == 0 {
		return false
	}
	if len(graph.Applicability.Paths) == 0 {
		return true
	}
	for _, changed := range paths {
		if proceduregraph.ApplicableToPath(graph, changed) {
			return true
		}
	}
	return false
}

func matchesProcedureApplicability(graph proceduregraph.Graph, paths, modules []string) bool {
	return matchesProcedureModules(graph, modules) && matchesPaths(graph, paths)
}

func matchesProcedureModules(graph proceduregraph.Graph, modules []string) bool {
	if len(graph.Applicability.Modules) > 0 {
		if len(modules) == 0 {
			return false
		}
		matched := false
		for _, required := range graph.Applicability.Modules {
			for _, current := range modules {
				if required == current {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func queryTerms(query string) []string {
	return strings.FieldsFunc(strings.ToLower(query), func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsNumber(char)
	})
}

func estimatedTokens(value string) int { return (len(value) + 3) / 4 }

func readBoundedBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	return data, nil
}

func decodeRequest(w http.ResponseWriter, r *http.Request, out any) error {
	data, err := readBoundedBody(w, r, maxRequestBytes)
	if err != nil {
		return err
	}
	return decodeStrict(data, out)
}

func decodeStrict(data []byte, out any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
