package service

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/threadforge-ai/threadforge/internal/traces"
)

func (h *Handler) metricCatalog(w http.ResponseWriter, r *http.Request) {
	limit, err := metricIntegerParam(r, "limit", 100, 1, traces.MetricCatalogMax)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.traces.MetricCatalog(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) metricTimeSeries(w http.ResponseWriter, r *http.Request) {
	name, err := metricStringParam(r, "name", true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	harness, err := metricStringParam(r, "harness", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	unit, unitFilter, err := metricOptionalParam(r, "unit")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	aggregation, err := metricStringParam(r, "aggregation", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	measure, err := metricStringParam(r, "measure", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if measure == "" {
		measure = "value"
	}
	limit, err := metricIntegerParam(r, "limit", 500, 1, traces.MetricSeriesMax)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fromValue, err := metricStringParam(r, "from", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	toValue, err := metricStringParam(r, "to", false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if (fromValue == "") != (toValue == "") {
		writeError(w, http.StatusBadRequest, errors.New("from and to must be supplied together"))
		return
	}
	to := time.Now().UTC()
	from := to.Add(-24 * time.Hour)
	if fromValue != "" {
		from, err = time.Parse(time.RFC3339Nano, fromValue)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("from must be RFC3339Nano"))
			return
		}
		to, err = time.Parse(time.RFC3339Nano, toValue)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("to must be RFC3339Nano"))
			return
		}
		from, to = from.UTC(), to.UTC()
	}
	result, err := h.traces.MetricTimeSeries(r.Context(), traces.MetricQuery{
		Name: name, Harness: harness, Unit: unit, UnitFilter: unitFilter, Aggregation: aggregation,
		Measure: measure, From: from, To: to, MaxPoints: limit,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func metricOptionalParam(r *http.Request, key string) (string, bool, error) {
	values, present := r.URL.Query()[key]
	if !present {
		return "", false, nil
	}
	if len(values) != 1 || len(values[0]) > 32 {
		return "", true, errors.New(key + " must be specified once and be at most 32 characters")
	}
	return values[0], true, nil
}

func metricStringParam(r *http.Request, key string, required bool) (string, error) {
	values, present := r.URL.Query()[key]
	if !present {
		if required {
			return "", errors.New(key + " is required")
		}
		return "", nil
	}
	if len(values) != 1 || len(values[0]) > 256 {
		return "", errors.New(key + " must be specified once and be at most 256 characters")
	}
	if required && values[0] == "" {
		return "", errors.New(key + " is required")
	}
	return values[0], nil
}

func metricIntegerParam(r *http.Request, key string, defaultValue, minimum, maximum int) (int, error) {
	values, present := r.URL.Query()[key]
	if !present {
		return defaultValue, nil
	}
	if len(values) != 1 {
		return 0, errors.New(key + " must be specified once")
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New(key + " is outside its allowed range")
	}
	return value, nil
}
