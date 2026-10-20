package traces

import (
	"bufio"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	metricScanTailBytes = 64 << 20
	MetricCatalogMax    = 250
	MetricSeriesMax     = 1000
)

var (
	metricNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,255}$`)
	metricUnitPattern  = regexp.MustCompile(`^[A-Za-z0-9%{}./_-]{1,32}$`)
	metricLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
)

type MetricDescriptor struct {
	Name           string `json:"name"`
	Harness        string `json:"harness"`
	Unit           string `json:"unit"`
	Aggregation    string `json:"aggregation"`
	PointCount     int    `json:"point_count"`
	FirstTimestamp string `json:"first_timestamp"`
	LastTimestamp  string `json:"last_timestamp"`
}

type MetricCatalog struct {
	SchemaVersion      string             `json:"schema_version"`
	Metrics            []MetricDescriptor `json:"metrics"`
	MetricsTruncated   bool               `json:"metrics_truncated"`
	OlderEventsOmitted bool               `json:"older_events_omitted"`
	MalformedLines     int                `json:"malformed_lines"`
}

type MetricQuery struct {
	Name        string
	Harness     string
	Unit        string
	UnitFilter  bool
	Aggregation string
	Measure     string
	From        time.Time
	To          time.Time
	MaxPoints   int
}

type MetricPoint struct {
	Timestamp   string  `json:"timestamp"`
	Value       float64 `json:"value"`
	Harness     string  `json:"harness"`
	Unit        string  `json:"unit"`
	Aggregation string  `json:"aggregation"`
}

type MetricTimeSeries struct {
	SchemaVersion      string        `json:"schema_version"`
	Name               string        `json:"name"`
	Measure            string        `json:"measure"`
	From               string        `json:"from"`
	To                 string        `json:"to"`
	MatchedPoints      int           `json:"matched_points"`
	PointsTruncated    bool          `json:"points_truncated"`
	OlderEventsOmitted bool          `json:"older_events_omitted"`
	MalformedLines     int           `json:"malformed_lines"`
	Points             []MetricPoint `json:"points"`
}

type metricRecord struct {
	name, harness, unit, aggregation, timestamp string
	time                                        time.Time
	dataPoint                                   map[string]any
}

func (s *Store) MetricCatalog(ctx context.Context, maxMetrics int) (MetricCatalog, error) {
	if maxMetrics < 1 || maxMetrics > MetricCatalogMax {
		return MetricCatalog{}, errors.New("metric catalog limit must be 1-250")
	}
	type key struct{ name, harness, unit, aggregation string }
	items := make(map[key]*MetricDescriptor)
	malformed, omitted, err := s.scanMetrics(ctx, func(record metricRecord) {
		id := key{record.name, record.harness, record.unit, record.aggregation}
		item := items[id]
		if item == nil {
			item = &MetricDescriptor{
				Name: record.name, Harness: record.harness, Unit: record.unit,
				Aggregation: record.aggregation, FirstTimestamp: record.timestamp,
				LastTimestamp: record.timestamp,
			}
			items[id] = item
		}
		item.PointCount++
		if record.time.Before(parseMetricTime(item.FirstTimestamp)) {
			item.FirstTimestamp = record.timestamp
		}
		if record.time.After(parseMetricTime(item.LastTimestamp)) {
			item.LastTimestamp = record.timestamp
		}
	})
	if err != nil {
		return MetricCatalog{}, err
	}
	keys := make([]key, 0, len(items))
	for id := range items {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := keys[i], keys[j]
		if left.name != right.name {
			return left.name < right.name
		}
		if left.harness != right.harness {
			return left.harness < right.harness
		}
		if left.unit != right.unit {
			return left.unit < right.unit
		}
		return left.aggregation < right.aggregation
	})
	truncated := len(keys) > maxMetrics
	if truncated {
		keys = keys[:maxMetrics]
	}
	metrics := make([]MetricDescriptor, 0, len(keys))
	for _, id := range keys {
		metrics = append(metrics, *items[id])
	}
	return MetricCatalog{
		SchemaVersion: "threadforge.metric-catalog.v1", Metrics: metrics,
		MetricsTruncated: truncated, OlderEventsOmitted: omitted, MalformedLines: malformed,
	}, nil
}

func (s *Store) MetricTimeSeries(ctx context.Context, query MetricQuery) (MetricTimeSeries, error) {
	if !metricNamePattern.MatchString(query.Name) {
		return MetricTimeSeries{}, errors.New("metric name is invalid")
	}
	if query.Harness != "" && !metricLabelPattern.MatchString(query.Harness) {
		return MetricTimeSeries{}, errors.New("metric harness filter is invalid")
	}
	if query.UnitFilter && query.Unit != "" && !metricUnitPattern.MatchString(query.Unit) {
		return MetricTimeSeries{}, errors.New("metric unit filter is invalid")
	}
	if query.Aggregation != "" && !validMetricAggregation(query.Aggregation) {
		return MetricTimeSeries{}, errors.New("metric aggregation filter is invalid")
	}
	if query.Measure == "" {
		query.Measure = "value"
	}
	switch query.Measure {
	case "value", "count", "sum", "min", "max":
	default:
		return MetricTimeSeries{}, errors.New("metric measure must be value, count, sum, min, or max")
	}
	if query.MaxPoints < 1 || query.MaxPoints > MetricSeriesMax {
		return MetricTimeSeries{}, errors.New("metric point limit must be 1-1000")
	}
	if query.From.IsZero() || query.To.IsZero() || !query.From.Before(query.To) || query.To.Sub(query.From) > 30*24*time.Hour {
		return MetricTimeSeries{}, errors.New("metric time range must be positive and at most 30 days")
	}
	if query.From.Location() != time.UTC || query.To.Location() != time.UTC {
		query.From, query.To = query.From.UTC(), query.To.UTC()
	}
	points := &metricPointHeap{}
	heap.Init(points)
	matched := 0
	malformed, omitted, err := s.scanMetrics(ctx, func(record metricRecord) {
		if record.name != query.Name || (query.Harness != "" && record.harness != query.Harness) ||
			(query.UnitFilter && record.unit != query.Unit) ||
			(query.Aggregation != "" && record.aggregation != query.Aggregation) ||
			record.time.Before(query.From) || record.time.After(query.To) {
			return
		}
		value, ok := metricValue(record.dataPoint, query.Measure)
		if !ok {
			return
		}
		matched++
		point := metricHeapPoint{time: record.time, point: MetricPoint{
			Timestamp: record.timestamp, Value: value, Harness: record.harness,
			Unit: record.unit, Aggregation: record.aggregation,
		}}
		if points.Len() < query.MaxPoints {
			heap.Push(points, point)
			return
		}
		if metricPointLess((*points)[0], point) {
			heap.Pop(points)
			heap.Push(points, point)
		}
	})
	if err != nil {
		return MetricTimeSeries{}, err
	}
	result := make([]MetricPoint, 0, points.Len())
	for points.Len() > 0 {
		result = append(result, heap.Pop(points).(metricHeapPoint).point)
	}
	return MetricTimeSeries{
		SchemaVersion: "threadforge.metric-timeseries.v1", Name: query.Name, Measure: query.Measure,
		From: query.From.Format(time.RFC3339Nano), To: query.To.Format(time.RFC3339Nano),
		MatchedPoints: matched, PointsTruncated: matched > query.MaxPoints,
		OlderEventsOmitted: omitted, MalformedLines: malformed, Points: result,
	}, nil
}

func (s *Store) scanMetrics(ctx context.Context, visit func(metricRecord)) (malformed int, olderOmitted bool, err error) {
	file, err := os.Open(s.path)
	if err != nil {
		return 0, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, false, err
	}
	start := info.Size() - metricScanTailBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return 0, false, err
	}
	reader := bufio.NewReader(io.LimitReader(file, info.Size()-start))
	if start > 0 {
		if _, err := reader.ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
			return 0, false, err
		}
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), MaxEventBytes+1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return 0, false, ctx.Err()
		default:
		}
		var event beacon.Event
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			malformed++
			continue
		}
		record, ok := metricFromEvent(event)
		if ok {
			visit(record)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, false, err
	}
	return malformed, start > 0, nil
}

func metricFromEvent(event beacon.Event) (metricRecord, bool) {
	if event.Event.Kind != "otel_metric" {
		return metricRecord{}, false
	}
	otel, ok := event.Raw["otel"].(map[string]any)
	if !ok {
		return metricRecord{}, false
	}
	name, ok := otel["name"].(string)
	if !ok || !metricNamePattern.MatchString(name) || privacy.ContainsCredentialText(name) {
		return metricRecord{}, false
	}
	aggregation, ok := otel["aggregation"].(string)
	if !ok || !validMetricAggregation(aggregation) {
		return metricRecord{}, false
	}
	dataPoint, ok := otel["data_point"].(map[string]any)
	if !ok {
		return metricRecord{}, false
	}
	timestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil {
		return metricRecord{}, false
	}
	harness := event.Harness.Name
	if !metricLabelPattern.MatchString(harness) || privacy.ContainsCredentialText(harness) {
		harness = "other"
	}
	unit, _ := otel["unit"].(string)
	if !metricUnitPattern.MatchString(unit) || privacy.ContainsCredentialText(unit) {
		unit = ""
	}
	return metricRecord{name: name, harness: harness, unit: unit, aggregation: aggregation,
		timestamp: timestamp.UTC().Format(time.RFC3339Nano), time: timestamp.UTC(), dataPoint: dataPoint}, true
}

func validMetricAggregation(value string) bool {
	switch value {
	case "gauge", "sum", "histogram", "exponentialHistogram", "summary":
		return true
	}
	return false
}

func metricValue(point map[string]any, measure string) (float64, bool) {
	if measure == "value" {
		for _, key := range []string{"asDouble", "asInt"} {
			if value, ok := finiteMetricNumber(point[key]); ok {
				return value, true
			}
		}
		return 0, false
	}
	value, ok := finiteMetricNumber(point[measure])
	return value, ok
}

func finiteMetricNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func parseMetricTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

type metricHeapPoint struct {
	time  time.Time
	point MetricPoint
}
type metricPointHeap []metricHeapPoint

func (h metricPointHeap) Len() int           { return len(h) }
func (h metricPointHeap) Less(i, j int) bool { return metricPointLess(h[i], h[j]) }
func (h metricPointHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *metricPointHeap) Push(value any)    { *h = append(*h, value.(metricHeapPoint)) }
func (h *metricPointHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}
func metricPointLess(left, right metricHeapPoint) bool {
	if left.time.Equal(right.time) {
		if left.point.Harness != right.point.Harness {
			return left.point.Harness < right.point.Harness
		}
		if left.point.Unit != right.point.Unit {
			return left.point.Unit < right.point.Unit
		}
		return left.point.Aggregation < right.point.Aggregation
	}
	return left.time.Before(right.time)
}
