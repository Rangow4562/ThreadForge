package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

type otlpLogsRequest struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

type otlpResourceLogs struct {
	Resource  otlpResource    `json:"resource"`
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpScopeLogs struct {
	Scope      otlpScope       `json:"scope"`
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type otlpLogRecord struct {
	TimeUnixNano         string          `json:"timeUnixNano"`
	ObservedTimeUnixNano string          `json:"observedTimeUnixNano"`
	SeverityText         string          `json:"severityText"`
	SeverityNumber       int             `json:"severityNumber"`
	Body                 json.RawMessage `json:"body"`
	Attributes           []otlpAttribute `json:"attributes"`
	TraceID              string          `json:"traceId"`
	SpanID               string          `json:"spanId"`
}

type otlpAttribute struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

func decodeOTLPLogs(data []byte) ([]beacon.Event, error) {
	var request otlpLogsRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, fmt.Errorf("decode OTLP/HTTP JSON logs: %w", err)
	}
	var events []beacon.Event
	for _, resourceLogs := range request.ResourceLogs {
		resourceAttrs, err := attributeMap(resourceLogs.Resource.Attributes)
		if err != nil {
			return nil, fmt.Errorf("decode OTLP resource attributes: %w", err)
		}
		for _, scopeLogs := range resourceLogs.ScopeLogs {
			for _, record := range scopeLogs.LogRecords {
				event, err := convertOTLPRecord(resourceAttrs, scopeLogs.Scope, record)
				if err != nil {
					return nil, err
				}
				events = append(events, event)
				if len(events) > maxEventBatch {
					return nil, fmt.Errorf("OTLP request exceeds %d log records", maxEventBatch)
				}
			}
		}
	}
	return events, nil
}

func convertOTLPRecord(resource map[string]any, scope otlpScope, record otlpLogRecord) (beacon.Event, error) {
	attrs, err := attributeMap(record.Attributes)
	if err != nil {
		return beacon.Event{}, fmt.Errorf("decode OTLP record attributes: %w", err)
	}
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
	harness := value("threadforge.harness", "agent.harness", "beacon.harness.name", "service.name")
	if harness == "" {
		harness = scope.Name
	}
	if harness == "" {
		harness = "otel"
	}
	action := value("threadforge.action", "event.action", "event.name", "beacon.event.action")
	if action == "" {
		action = "otel.log"
	}
	timestamp, err := otlpTimestamp(record.TimeUnixNano)
	if err != nil {
		return beacon.Event{}, fmt.Errorf("invalid OTLP timeUnixNano: %w", err)
	}
	if timestamp == "" {
		timestamp, err = otlpTimestamp(record.ObservedTimeUnixNano)
		if err != nil {
			return beacon.Event{}, fmt.Errorf("invalid OTLP observedTimeUnixNano: %w", err)
		}
	}
	if timestamp == "" {
		timestamp = beacon.FormatTimestamp(time.Now())
	}
	message, err := otlpAnyValue(record.Body)
	if err != nil {
		return beacon.Event{}, fmt.Errorf("decode OTLP body: %w", err)
	}
	messageText := ""
	if message != nil {
		if text, ok := message.(string); ok {
			messageText = text
		} else {
			encoded, marshalErr := json.Marshal(message)
			if marshalErr != nil {
				return beacon.Event{}, fmt.Errorf("encode OTLP body: %w", marshalErr)
			}
			messageText = string(encoded)
		}
	}
	combined := make(map[string]any, len(resource)+len(attrs))
	for key, value := range resource {
		combined["resource."+key] = value
	}
	for key, value := range attrs {
		combined[key] = value
	}
	combined["scope.name"] = scope.Name
	combined["scope.version"] = scope.Version
	if message != nil {
		combined["body"] = message
	}
	origin := beacon.Origin(value(beacon.AttributeOrigin))
	switch origin {
	case beacon.OriginLocal, beacon.OriginCloud, beacon.OriginCI:
	default:
		origin = beacon.OriginLocal
	}
	event := beacon.NewEvent(beacon.NewEventOptions{
		Action: action, Category: value("event.category", "threadforge.category", "beacon.event.category"),
		Severity: mapOTLPSeverity(record.SeverityText, record.SeverityNumber),
		Harness:  beacon.HarnessInfo{Name: harness, Version: scope.Version, CollectionMethod: "otlp"},
		Origin:   origin, Message: messageText,
	})
	event.Timestamp = timestamp
	event.Event.Kind = value("event.kind", "threadforge.kind", "beacon.event.kind")
	if event.Event.Kind == "" {
		if value("beacon.event.action") != "" {
			event.Event.Kind = "agent_runtime"
		} else {
			event.Event.Kind = "otel_log"
		}
	}
	switch value("threadforge.fidelity", "event.fidelity", "beacon.event.fidelity") {
	case string(beacon.FidelityObserved):
		event.Event.Fidelity = beacon.FidelityObserved
	case string(beacon.FidelityInferred):
		event.Event.Fidelity = beacon.FidelityInferred
	}
	if session := value("threadforge.session.id", "session.id", "beacon.session.id", "gen_ai.conversation.id"); session != "" {
		event.Session = &beacon.SessionInfo{ID: session}
	}
	applyOTLPGenAIAttributes(&event, attrs, resource)
	if record.TraceID != "" || record.SpanID != "" {
		event.Trace = &beacon.TraceInfo{ID: record.TraceID, SpanID: record.SpanID}
	}
	event.Run = runInfoFromResource(resource)
	event.Raw = combined
	return event, nil
}

// applyOTLPGenAIAttributes lifts the browser extension's Beacon-prefixed event
// contract and OpenTelemetry GenAI attributes into canonical typed fields.
// convertOTLPRecord continues to preserve the normalized attributes in Raw.
func applyOTLPGenAIAttributes(event *beacon.Event, attrs, resource map[string]any) {
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
	attribute := func(names ...string) any {
		for _, name := range names {
			if candidate, ok := attrs[name]; ok {
				return candidate
			}
			if candidate, ok := resource[name]; ok {
				return candidate
			}
		}
		return nil
	}

	if name := value("user_agent.name"); name != "" {
		event.UserAgent = &beacon.UserAgentInfo{Name: name, Version: value("user_agent.version")}
	}
	if prompt := value("beacon.prompt.text"); prompt != "" {
		event.Prompt = &beacon.PromptInfo{Text: prompt}
	}
	if model := value("gen_ai.response.model", "gen_ai.request.model"); model != "" {
		event.Model = model
	}
	if retention := value("beacon.content.retention"); retention == beacon.ContentRetentionMetadata ||
		retention == beacon.ContentRetentionRedacted || retention == beacon.ContentRetentionFull {
		included := false
		for _, key := range []string{"beacon.prompt.text", "gen_ai.input.messages", "gen_ai.output.messages", "gen_ai.tool.call.arguments", "gen_ai.tool.call.result"} {
			if candidate, ok := attrs[key].(string); ok && candidate != "" {
				included = true
			}
		}
		truncated, _ := attribute("beacon.field_truncated").(bool)
		event.Content = &beacon.ContentInfo{
			Retention: retention,
			Included:  included,
			Redacted:  retention == beacon.ContentRetentionRedacted,
			Truncated: truncated,
		}
		event.Truncated = truncated
	}

	genAI := event.GenAI
	if genAI == nil {
		genAI = &beacon.GenAIInfo{}
	}
	if provider := value("gen_ai.provider.name"); provider != "" {
		genAI.Provider = &beacon.GenAIProviderInfo{Name: provider}
	}
	if conversation := value("gen_ai.conversation.id", "beacon.session.id"); conversation != "" {
		genAI.Conversation = &beacon.GenAIConversationInfo{ID: conversation}
	}
	if model := value("gen_ai.request.model"); model != "" {
		genAI.Request = &beacon.GenAIRequestInfo{Model: model}
	}
	if model := value("gen_ai.response.model"); model != "" {
		genAI.Response = &beacon.GenAIResponseInfo{Model: model}
	}
	if raw := attribute("gen_ai.input.messages"); raw != nil {
		genAI.Input = &beacon.GenAIInputInfo{Messages: decodeGenAIValue(raw)}
	}
	if raw := attribute("gen_ai.output.messages"); raw != nil {
		genAI.Output = &beacon.GenAIOutputInfo{Messages: decodeGenAIValue(raw)}
	}

	usage := &beacon.GenAIUsageInfo{
		InputTokens:  otlpInt64Attribute(attribute("gen_ai.usage.input_tokens")),
		OutputTokens: otlpInt64Attribute(attribute("gen_ai.usage.output_tokens")),
	}
	if count := otlpInt64Attribute(attribute("gen_ai.usage.cache_creation.input_tokens")); count != nil {
		usage.CacheCreation = &beacon.GenAIUsageCacheCreationInfo{InputTokens: count}
	}
	if count := otlpInt64Attribute(attribute("gen_ai.usage.cache_read.input_tokens")); count != nil {
		usage.CacheRead = &beacon.GenAIUsageCacheReadInfo{InputTokens: count}
	}
	if usage.InputTokens != nil || usage.OutputTokens != nil || usage.CacheCreation != nil || usage.CacheRead != nil {
		genAI.Usage = usage
	}

	if name := value("tool.name", "gen_ai.tool.call.name"); name != "" {
		event.Tool = &beacon.ToolInfo{Name: name}
		call := &beacon.GenAIToolCallInfo{ID: value("gen_ai.tool.call.id")}
		if raw := attribute("gen_ai.tool.call.arguments"); raw != nil {
			call.Arguments = decodeGenAIValue(raw)
		}
		if raw := attribute("gen_ai.tool.call.result"); raw != nil {
			call.Result = decodeGenAIValue(raw)
		}
		genAI.Tool = &beacon.GenAIToolInfo{Name: name, Call: call}
	}
	if genAI.Provider != nil || genAI.Conversation != nil || genAI.Request != nil || genAI.Response != nil ||
		genAI.Input != nil || genAI.Output != nil || genAI.Usage != nil || genAI.Tool != nil {
		event.GenAI = genAI
	}
}

func decodeGenAIValue(value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	var decoded any
	if json.Unmarshal([]byte(text), &decoded) == nil {
		return decoded
	}
	return text
}

func otlpInt64Attribute(value any) *int64 {
	var parsed int64
	switch number := value.(type) {
	case string:
		candidate, err := strconv.ParseInt(number, 10, 64)
		if err != nil || candidate < 0 {
			return nil
		}
		parsed = candidate
	case json.Number:
		candidate, err := number.Int64()
		if err != nil || candidate < 0 {
			return nil
		}
		parsed = candidate
	default:
		return nil
	}
	return &parsed
}

func runInfoFromResource(resource map[string]any) *beacon.RunInfo {
	value := func(key string) string {
		if text, ok := resource[key].(string); ok {
			return text
		}
		return ""
	}
	run := &beacon.RunInfo{
		Provider: value(beacon.AttributeRunProvider), RunID: value(beacon.AttributeRunID),
		RunAttempt: value(beacon.AttributeRunAttempt), Workflow: value(beacon.AttributeRunWorkflow),
		Job: value(beacon.AttributeRunJob), EventName: value(beacon.AttributeRunEventName),
		Commit: value(beacon.AttributeRunCommit), Repository: value(beacon.AttributeRunRepository),
		Branch: value(beacon.AttributeRunBranch), PR: value(beacon.AttributeRunPR),
		PRNumber: value(beacon.AttributeRunPRNumber), Actor: value(beacon.AttributeRunActor),
	}
	if ephemeral, ok := resource[beacon.AttributeRunEphemeral].(bool); ok {
		run.Ephemeral = ephemeral
	} else {
		run.Ephemeral = strings.EqualFold(value(beacon.AttributeRunEphemeral), "true")
	}
	if run.Provider == "" && run.RunID == "" && run.RunAttempt == "" && run.Workflow == "" && run.Job == "" &&
		run.EventName == "" && run.Commit == "" && run.Repository == "" && run.Branch == "" && run.PR == "" &&
		run.PRNumber == "" && run.Actor == "" && !run.Ephemeral {
		return nil
	}
	return run
}

func attributeMap(attributes []otlpAttribute) (map[string]any, error) {
	result := make(map[string]any, len(attributes))
	for _, attribute := range attributes {
		if attribute.Key == "" {
			return nil, errors.New("OTLP attribute key is required")
		}
		if len(attribute.Value) == 0 {
			continue
		}
		value, err := otlpAnyValue(attribute.Value)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", attribute.Key, err)
		}
		result[attribute.Key] = value
	}
	return result, nil
}

func otlpAnyValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("expected OTLP AnyValue object: %w", err)
	}
	for _, key := range []string{"stringValue", "boolValue", "intValue", "doubleValue", "bytesValue"} {
		if encoded, ok := fields[key]; ok {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return nil, err
			}
			return value, nil
		}
	}
	if encoded, ok := fields["arrayValue"]; ok {
		var value struct {
			Values []json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, err
		}
		items := make([]any, 0, len(value.Values))
		for _, item := range value.Values {
			decoded, err := otlpAnyValue(item)
			if err != nil {
				return nil, err
			}
			items = append(items, decoded)
		}
		return items, nil
	}
	if encoded, ok := fields["kvlistValue"]; ok {
		var value struct {
			Values []otlpAttribute `json:"values"`
		}
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, err
		}
		return attributeMap(value.Values)
	}
	return nil, errors.New("unsupported OTLP AnyValue variant")
}

func otlpTimestamp(value string) (string, error) {
	if value == "" || value == "0" {
		return "", nil
	}
	nanos, err := strconv.ParseInt(value, 10, 64)
	if err != nil || nanos < 0 {
		return "", errors.New("must be a non-negative nanosecond timestamp string")
	}
	return time.Unix(0, nanos).UTC().Format(time.RFC3339Nano), nil
}

func mapOTLPSeverity(text string, number int) beacon.Severity {
	switch strings.ToLower(text) {
	case "trace", "debug":
		return beacon.SeverityLow
	case "info", "informational":
		return beacon.SeverityInfo
	case "warn", "warning":
		return beacon.SeverityMedium
	case "error":
		return beacon.SeverityHigh
	case "fatal":
		return beacon.SeverityCritical
	}
	switch {
	case number >= 21:
		return beacon.SeverityCritical
	case number >= 17:
		return beacon.SeverityHigh
	case number >= 13:
		return beacon.SeverityMedium
	case number >= 9:
		return beacon.SeverityInfo
	case number >= 5:
		return beacon.SeverityLow
	case number > 0:
		return beacon.SeverityLow
	default:
		return beacon.SeverityInfo
	}
}
