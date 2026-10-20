package service

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type otlpTracesRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpSpan struct {
	TraceID      string          `json:"traceId"`
	SpanID       string          `json:"spanId"`
	ParentSpanID string          `json:"parentSpanId"`
	Name         string          `json:"name"`
	StartTime    string          `json:"startTimeUnixNano"`
	EndTime      string          `json:"endTimeUnixNano"`
	Attributes   []otlpAttribute `json:"attributes"`
	Status       struct {
		Code json.RawMessage `json:"code"`
	} `json:"status"`
}

type otlpMetricsRequest struct {
	ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
}

type otlpResourceMetrics struct {
	Resource     otlpResource       `json:"resource"`
	ScopeMetrics []otlpScopeMetrics `json:"scopeMetrics"`
}

type otlpScopeMetrics struct {
	Scope   otlpScope    `json:"scope"`
	Metrics []otlpMetric `json:"metrics"`
}

type otlpMetric struct {
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	Unit                 string            `json:"unit"`
	Gauge                *otlpMetricPoints `json:"gauge"`
	Sum                  *otlpMetricPoints `json:"sum"`
	Histogram            *otlpMetricPoints `json:"histogram"`
	ExponentialHistogram *otlpMetricPoints `json:"exponentialHistogram"`
	Summary              *otlpMetricPoints `json:"summary"`
}

type otlpMetricPoints struct {
	DataPoints []json.RawMessage `json:"dataPoints"`
}

func decodeOTLPProtobufTraces(data []byte) ([]beacon.Event, error) {
	request := &otlptrace.TracesData{}
	if err := proto.Unmarshal(data, request); err != nil {
		return nil, err
	}
	jsonBody, err := (protojson.MarshalOptions{UseEnumNumbers: true}).Marshal(request)
	if err != nil {
		return nil, err
	}
	return decodeOTLPTraces(jsonBody)
}

func decodeOTLPProtobufMetrics(data []byte) ([]beacon.Event, error) {
	request := &otlpmetrics.MetricsData{}
	if err := proto.Unmarshal(data, request); err != nil {
		return nil, err
	}
	jsonBody, err := (protojson.MarshalOptions{UseEnumNumbers: true}).Marshal(request)
	if err != nil {
		return nil, err
	}
	return decodeOTLPMetrics(jsonBody)
}

func decodeOTLPTraces(data []byte) ([]beacon.Event, error) {
	var request otlpTracesRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, fmt.Errorf("decode OTLP/HTTP JSON traces: %w", err)
	}
	events := make([]beacon.Event, 0)
	for _, resourceSpans := range request.ResourceSpans {
		resource, err := attributeMap(resourceSpans.Resource.Attributes)
		if err != nil {
			return nil, fmt.Errorf("decode OTLP trace resource attributes: %w", err)
		}
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, span := range scopeSpans.Spans {
				if len(events) == maxEventBatch {
					return nil, fmt.Errorf("OTLP request exceeds %d spans", maxEventBatch)
				}
				event, err := convertOTLPSpan(resource, scopeSpans.Scope, span)
				if err != nil {
					return nil, err
				}
				events = append(events, event)
			}
		}
	}
	return events, nil
}

func convertOTLPSpan(resource map[string]any, scope otlpScope, span otlpSpan) (beacon.Event, error) {
	traceID, err := normalizeOTLPID(span.TraceID, 16, false)
	if err != nil {
		return beacon.Event{}, fmt.Errorf("invalid OTLP traceId: %w", err)
	}
	spanID, err := normalizeOTLPID(span.SpanID, 8, false)
	if err != nil {
		return beacon.Event{}, fmt.Errorf("invalid OTLP spanId: %w", err)
	}
	parentID, err := normalizeOTLPID(span.ParentSpanID, 8, true)
	if err != nil {
		return beacon.Event{}, fmt.Errorf("invalid OTLP parentSpanId: %w", err)
	}
	startNanos, startErr := strconv.ParseInt(span.StartTime, 10, 64)
	endNanos, endErr := strconv.ParseInt(span.EndTime, 10, 64)
	timestamp, timestampErr := otlpTimestamp(span.StartTime)
	if startErr != nil || endErr != nil || timestampErr != nil || startNanos <= 0 || endNanos < startNanos || timestamp == "" {
		return beacon.Event{}, errors.New("OTLP span startTimeUnixNano must be a valid non-zero timestamp")
	}
	attrs, err := attributeMap(span.Attributes)
	if err != nil {
		return beacon.Event{}, fmt.Errorf("decode OTLP span attributes: %w", err)
	}
	spanRaw, err := normalizeOTLPRaw(span)
	if err != nil {
		return beacon.Event{}, fmt.Errorf("normalize OTLP span metadata: %w", err)
	}
	severity := beacon.SeverityInfo
	if otlpSpanHasErrorStatus(span.Status.Code) {
		severity = beacon.SeverityHigh
	}
	return makeOTLPSignalEvent(resource, scope, attrs, timestamp, "otel.span", "otel_span", span.Name, severity,
		map[string]any{"signal": "trace", "span": spanRaw}, &beacon.TraceInfo{ID: traceID, SpanID: spanID, ParentSpanID: parentID}), nil
}

func decodeOTLPMetrics(data []byte) ([]beacon.Event, error) {
	var request otlpMetricsRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, fmt.Errorf("decode OTLP/HTTP JSON metrics: %w", err)
	}
	events := make([]beacon.Event, 0)
	for _, resourceMetrics := range request.ResourceMetrics {
		resource, err := attributeMap(resourceMetrics.Resource.Attributes)
		if err != nil {
			return nil, fmt.Errorf("decode OTLP metric resource attributes: %w", err)
		}
		for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
			for _, metric := range scopeMetrics.Metrics {
				aggregation, points, err := metric.dataPoints()
				if err != nil {
					return nil, err
				}
				for _, pointRaw := range points {
					if len(events) == maxEventBatch {
						return nil, fmt.Errorf("OTLP request exceeds %d metric data points", maxEventBatch)
					}
					var point struct {
						Attributes []otlpAttribute `json:"attributes"`
						Time       string          `json:"timeUnixNano"`
					}
					if err := json.Unmarshal(pointRaw, &point); err != nil {
						return nil, fmt.Errorf("decode OTLP metric data point: %w", err)
					}
					timestamp, err := otlpTimestamp(point.Time)
					if err != nil || timestamp == "" {
						return nil, errors.New("OTLP metric data point timeUnixNano must be a valid non-zero timestamp")
					}
					attrs, err := attributeMap(point.Attributes)
					if err != nil {
						return nil, fmt.Errorf("decode OTLP metric data point attributes: %w", err)
					}
					var rawPoint any
					decoder := json.NewDecoder(bytes.NewReader(pointRaw))
					decoder.UseNumber()
					if err := decoder.Decode(&rawPoint); err != nil {
						return nil, fmt.Errorf("decode OTLP metric data point value: %w", err)
					}
					rawPoint = normalizeOTLPAttributes(rawPoint)
					metricRaw := map[string]any{
						"signal": "metric", "name": metric.Name, "description": metric.Description,
						"unit": metric.Unit, "aggregation": aggregation, "data_point": rawPoint,
					}
					event := makeOTLPSignalEvent(resource, scopeMetrics.Scope, attrs, timestamp, "otel.metric", "otel_metric", metric.Name, beacon.SeverityInfo, metricRaw, nil)
					events = append(events, event)
				}
			}
		}
	}
	return events, nil
}

func (metric otlpMetric) dataPoints() (string, []json.RawMessage, error) {
	type candidate struct {
		name   string
		points *otlpMetricPoints
	}
	all := []candidate{
		{"gauge", metric.Gauge}, {"sum", metric.Sum}, {"histogram", metric.Histogram},
		{"exponentialHistogram", metric.ExponentialHistogram}, {"summary", metric.Summary},
	}
	var selected *candidate
	for index := range all {
		if all[index].points == nil {
			continue
		}
		if selected != nil {
			return "", nil, errors.New("OTLP metric must contain exactly one aggregation")
		}
		selected = &all[index]
	}
	if selected == nil {
		return "", nil, errors.New("OTLP metric aggregation is required")
	}
	return selected.name, selected.points.DataPoints, nil
}

func makeOTLPSignalEvent(resource map[string]any, scope otlpScope, attrs map[string]any, timestamp, action, kind, message string, severity beacon.Severity, signal map[string]any, trace *beacon.TraceInfo) beacon.Event {
	value := func(names ...string) string {
		for _, name := range names {
			if candidate, ok := attrs[name].(string); ok && candidate != "" {
				return candidate
			}
			if candidate, ok := resource[name].(string); ok && candidate != "" {
				return candidate
			}
		}
		return ""
	}
	harness := value("threadforge.harness", "agent.harness", "service.name")
	if harness == "" {
		harness = scope.Name
	}
	if harness == "" {
		harness = "otel"
	}
	if override := value("threadforge.action", "event.action", "event.name"); override != "" {
		action = override
	}
	combined := make(map[string]any, len(resource)+len(attrs)+5)
	for key, item := range resource {
		combined["resource."+key] = item
	}
	for key, item := range attrs {
		combined[key] = item
	}
	combined["scope.name"] = scope.Name
	combined["scope.version"] = scope.Version
	combined["otel"] = signal
	event := beacon.NewEvent(beacon.NewEventOptions{
		Action: action, Category: "otel", Severity: severity,
		Harness: beacon.HarnessInfo{Name: harness, Version: scope.Version, CollectionMethod: "otlp"},
		Origin:  beacon.OriginLocal, Message: message,
	})
	event.Timestamp = timestamp
	event.Event.Kind = kind
	switch value("threadforge.fidelity", "event.fidelity") {
	case string(beacon.FidelityObserved):
		event.Event.Fidelity = beacon.FidelityObserved
	case string(beacon.FidelityInferred):
		event.Event.Fidelity = beacon.FidelityInferred
	}
	if session := value("threadforge.session.id", "session.id"); session != "" {
		event.Session = &beacon.SessionInfo{ID: session}
	}
	event.Trace = trace
	event.Raw = combined
	return event
}

func normalizeOTLPID(value string, byteCount int, optional bool) (string, error) {
	if value == "" && optional {
		return "", nil
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == byteCount {
		if allZeroBytes(decoded) {
			return "", errors.New("all-zero identifiers are invalid")
		}
		return strings.ToLower(value), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != byteCount {
		return "", fmt.Errorf("must encode exactly %d bytes", byteCount)
	}
	if allZeroBytes(decoded) {
		return "", errors.New("all-zero identifiers are invalid")
	}
	return hex.EncodeToString(decoded), nil
}

func allZeroBytes(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return len(value) > 0
}

func otlpSpanHasErrorStatus(code json.RawMessage) bool {
	var number int
	if json.Unmarshal(code, &number) == nil && number == 2 {
		return true
	}
	var name string
	return json.Unmarshal(code, &name) == nil && name == "STATUS_CODE_ERROR"
}

func normalizeOTLPRaw(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return normalizeOTLPAttributes(normalized), nil
}

func normalizeOTLPAttributes(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		output := make(map[string]any, len(typed))
		for key, item := range typed {
			if key == "attributes" {
				if list, ok := item.([]any); ok {
					attributes := make(map[string]any, len(list))
					valid := true
					for _, entry := range list {
						attribute, ok := entry.(map[string]any)
						attributeKey, keyOK := attribute["key"].(string)
						attributeValue, valueOK := attribute["value"]
						if !ok || !keyOK || attributeKey == "" || !valueOK {
							valid = false
							break
						}
						valueJSON, marshalErr := json.Marshal(attributeValue)
						if marshalErr != nil {
							valid = false
							break
						}
						decoded, decodeErr := otlpAnyValue(valueJSON)
						if decodeErr != nil {
							valid = false
							break
						}
						attributes[attributeKey] = normalizeOTLPAttributes(decoded)
					}
					if valid {
						output[key] = attributes
						continue
					}
					output[key] = "[OMITTED_INVALID_OTLP_ATTRIBUTE_LIST]"
					continue
				}
			}
			output[key] = normalizeOTLPAttributes(item)
		}
		return output
	case []any:
		output := make([]any, len(typed))
		for index, item := range typed {
			output[index] = normalizeOTLPAttributes(item)
		}
		return output
	default:
		return value
	}
}
