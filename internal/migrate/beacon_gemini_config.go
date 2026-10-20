package migrate

import (
	"encoding/json"
	"strings"
)

const beaconGeminiOTelConfigSchema = "threadforge.beacon-gemini-otel-config.v1"

func normalizeBeaconGeminiSettings(root map[string]any) (json.RawMessage, error) {
	telemetry, ok := root["telemetry"].(map[string]any)
	if !ok {
		return nil, errBeaconGeminiConfig
	}
	target, ok := telemetry["target"].(string)
	if !ok || target != "local" {
		return nil, errBeaconGeminiConfig
	}
	endpoint, ok := telemetry["otlpEndpoint"].(string)
	if !ok || otelEndpointScope(endpoint) != "loopback" {
		return nil, errBeaconGeminiConfig
	}
	protocol, ok := telemetry["otlpProtocol"].(string)
	if !ok || protocol != "grpc" {
		return nil, errBeaconGeminiConfig
	}
	useCollector, ok := telemetry["useCollector"].(bool)
	if !ok || !useCollector {
		return nil, errBeaconGeminiConfig
	}

	outfileConfigured := false
	if rawOutfile, exists := telemetry["outfile"]; exists {
		outfile, ok := rawOutfile.(string)
		if !ok {
			return nil, errBeaconGeminiConfig
		}
		outfileConfigured = strings.TrimSpace(outfile) != ""
	}
	payload := map[string]any{
		"schema_version":        beaconGeminiOTelConfigSchema,
		"source_format":         "json",
		"kind":                  "gemini_otel_configuration",
		"harness":               "gemini_cli",
		"telemetry_configured":  true,
		"target":                "local",
		"endpoint_scope":        "loopback",
		"otlp_protocol":         "grpc",
		"use_collector":         true,
		"outfile_configured":    outfileConfigured,
		"omitted_fields":        []string{"credentials", "exporter_endpoint", "local_paths", "outfile_path", "unrelated_settings"},
		"human_review_required": true,
		"execution_authorized":  false,
	}
	for source, destination := range map[string]string{
		"enabled":    "enabled",
		"logPrompts": "log_prompts",
		"traces":     "traces",
	} {
		if raw, exists := telemetry[source]; exists {
			value, ok := raw.(bool)
			if !ok {
				return nil, errBeaconGeminiConfig
			}
			payload[destination] = value
		}
	}
	data, err := json.Marshal(payload)
	return data, err
}
