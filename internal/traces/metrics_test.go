package traces

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

func TestMetricCatalogAndBoundedTimeSeriesOmitAttributes(t *testing.T) {
	store, err := Open(t.TempDir() + "/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	events := []beacon.Event{
		metricTestEvent("2026-10-10T10:00:00Z", "gen_ai.tokens", "codex", "{token}", "sum", map[string]any{"asInt": "11", "attributes": map[string]any{"operation": "generate", "api_key": "metric-private-secret"}}),
		metricTestEvent("2026-10-10T10:01:00Z", "gen_ai.tokens", "codex", "{token}", "sum", map[string]any{"asInt": "12"}),
		metricTestEvent("2026-10-10T10:02:00Z", "gen_ai.tokens", "codex", "{token}", "sum", map[string]any{"asDouble": 13.5}),
		metricTestEvent("2026-10-10T10:03:00Z", "runtime.memory", "claude_code", "By", "gauge", map[string]any{"asInt": "2048"}),
		metricTestEvent("2026-10-10T10:04:00Z", "sk-abcdefghijklmnopqrstuvwxyz123456", "codex", "{token}", "sum", map[string]any{"asInt": "1"}),
	}
	if err := store.Append(context.Background(), events); err != nil {
		t.Fatal(err)
	}

	catalog, err := store.MetricCatalog(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.SchemaVersion != "threadforge.metric-catalog.v1" || len(catalog.Metrics) != 2 || catalog.OlderEventsOmitted {
		t.Fatalf("unexpected catalog: %#v", catalog)
	}
	if catalog.Metrics[0].Name != "gen_ai.tokens" || catalog.Metrics[0].PointCount != 3 ||
		catalog.Metrics[0].FirstTimestamp != "2026-10-10T10:00:00Z" ||
		catalog.Metrics[0].LastTimestamp != "2026-10-10T10:02:00Z" {
		t.Fatalf("metric catalog summary mismatch: %#v", catalog.Metrics[0])
	}

	series, err := store.MetricTimeSeries(context.Background(), MetricQuery{
		Name: "gen_ai.tokens", Harness: "codex", Unit: "{token}", UnitFilter: true,
		Aggregation: "sum", Measure: "value",
		From: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC), MaxPoints: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if series.SchemaVersion != "threadforge.metric-timeseries.v1" || series.MatchedPoints != 3 ||
		!series.PointsTruncated || len(series.Points) != 2 || series.Points[0].Value != 12 ||
		series.Points[1].Value != 13.5 || series.Points[0].Timestamp >= series.Points[1].Timestamp {
		t.Fatalf("unexpected bounded time series: %#v", series)
	}

	encoded, err := json.Marshal(struct {
		Catalog MetricCatalog    `json:"catalog"`
		Series  MetricTimeSeries `json:"series"`
	}{catalog, series})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"metric-private-secret", "api_key", "attributes", "operation"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("metric query exposed %q: %s", private, encoded)
		}
	}
}

func TestMetricQueryRejectsInvalidRangesAndBounds(t *testing.T) {
	store, err := Open(t.TempDir() + "/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	base := MetricQuery{Name: "safe.metric", From: time.Now().UTC().Add(-24 * time.Hour), To: time.Now().UTC(), MaxPoints: 10}
	invalid := []MetricQuery{
		{Name: "../../private", From: base.From, To: base.To, MaxPoints: 10},
		{Name: "safe.metric", From: base.From, To: base.To.Add(31 * 24 * time.Hour), MaxPoints: 10},
		{Name: "safe.metric", From: base.From, To: base.To, MaxPoints: MetricSeriesMax + 1},
		{Name: "safe.metric", Harness: "C:/private", From: base.From, To: base.To, MaxPoints: 10},
	}
	for _, query := range invalid {
		if _, err := store.MetricTimeSeries(context.Background(), query); err == nil {
			t.Fatalf("accepted invalid query: %#v", query)
		}
	}
	if _, err := store.MetricCatalog(context.Background(), MetricCatalogMax+1); err == nil {
		t.Fatal("accepted oversized metric catalog")
	}

	if err := os.WriteFile(store.Path(), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := store.MetricCatalog(context.Background(), 10)
	if err != nil || catalog.MalformedLines != 1 {
		t.Fatalf("malformed count=%d err=%v", catalog.MalformedLines, err)
	}
}

func metricTestEvent(timestamp, name, harness, unit, aggregation string, point map[string]any) beacon.Event {
	event := beacon.NewEvent(beacon.NewEventOptions{
		Action: "otel.metric", Harness: beacon.HarnessInfo{Name: harness}, Message: name,
	})
	event.Timestamp = timestamp
	event.Event.Kind = "otel_metric"
	event.Raw = map[string]any{"otel": map[string]any{
		"signal": "metric", "name": name, "unit": unit, "aggregation": aggregation,
		"data_point": point,
	}}
	return event
}
