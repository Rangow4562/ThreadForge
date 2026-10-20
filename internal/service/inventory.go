package service

import (
	"errors"
	"net/http"
	"os"
	"strings"

	runtimeinventory "github.com/threadforge-ai/threadforge/internal/inventory"
)

func (h *Handler) inventoryScan(w http.ResponseWriter, r *http.Request) {
	result, ok := h.scanDashboardInventory(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, summarizeInventory(result))
}

func (h *Handler) inventoryScanDetails(w http.ResponseWriter, r *http.Request) {
	result, ok := h.scanDashboardInventory(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, summarizeInventoryDetails(result))
}

func (h *Handler) scanDashboardInventory(w http.ResponseWriter, r *http.Request) (runtimeinventory.Result, bool) {
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && origin != "http://"+r.Host {
		writeError(w, http.StatusForbidden, errors.New("inventory scan is restricted to same-origin requests"))
		return runtimeinventory.Result{}, false
	}
	body, err := readBoundedBody(w, r, 1024)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("inventory scan request body is too large or unreadable"))
		return runtimeinventory.Result{}, false
	}
	if len(strings.TrimSpace(string(body))) != 0 {
		writeError(w, http.StatusBadRequest, errors.New("inventory scan does not accept request options"))
		return runtimeinventory.Result{}, false
	}
	select {
	case h.inventoryScanGate <- struct{}{}:
		defer func() { <-h.inventoryScanGate }()
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("a local inventory scan is already running"))
		return runtimeinventory.Result{}, false
	}
	if r.Context().Err() != nil {
		writeError(w, http.StatusRequestTimeout, errors.New("inventory scan request was canceled"))
		return runtimeinventory.Result{}, false
	}

	home := h.inventoryHome
	if strings.TrimSpace(home) == "" {
		var homeErr error
		home, homeErr = os.UserHomeDir()
		if homeErr != nil {
			writeError(w, http.StatusInternalServerError, errors.New("could not resolve the local user scope"))
			return runtimeinventory.Result{}, false
		}
	}
	projectRoot := h.inventoryProjectRoot
	skipProjectScope := strings.TrimSpace(projectRoot) == ""
	home, err = runtimeinventory.ValidateScanRoot(home)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("local inventory roots are unavailable or contain symlinks"))
		return runtimeinventory.Result{}, false
	}
	if !skipProjectScope {
		projectRoot, err = runtimeinventory.ValidateScanRoot(projectRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, errors.New("local inventory roots are unavailable or contain symlinks"))
			return runtimeinventory.Result{}, false
		}
	}

	result := runtimeinventory.Scan(runtimeinventory.Options{HomeDir: home, WorkingDir: projectRoot, SkipProjectScope: skipProjectScope})
	if r.Context().Err() != nil {
		writeError(w, http.StatusRequestTimeout, errors.New("inventory scan request was canceled"))
		return runtimeinventory.Result{}, false
	}
	return result, true
}
