package migrate

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"sort"
	"strings"
)

const beaconCodexOTelConfigSchema = "threadforge.beacon-codex-otel-config.v1"

var errBeaconCodexOTelConfig = errors.New("Beacon Codex telemetry config must contain a supported OTEL exporter table")

func normalizeBeaconCodexOTelConfig(root map[string]any) (json.RawMessage, error) {
	otel, ok := root["otel"].(map[string]any)
	if !ok {
		return nil, errBeaconCodexOTelConfig
	}

	logExporters, err := codexExporterProjections(otel["exporter"])
	if err != nil {
		return nil, err
	}
	traceExporters, err := codexExporterProjections(otel["trace_exporter"])
	if err != nil {
		return nil, err
	}
	if len(logExporters)+len(traceExporters) == 0 {
		return nil, errBeaconCodexOTelConfig
	}

	payload := map[string]any{
		"schema_version":        beaconCodexOTelConfigSchema,
		"source_format":         "toml",
		"kind":                  "codex_otel_configuration",
		"harness":               "codex_cli",
		"otel_configured":       true,
		"log_exporters":         logExporters,
		"trace_exporters":       traceExporters,
		"omitted_fields":        []string{"credentials", "exporter_endpoints", "headers", "local_paths", "unrelated_settings"},
		"human_review_required": true,
		"execution_authorized":  false,
	}
	if environment, ok := otel["environment"].(string); ok && (environment == "dev" || environment == "ci") {
		payload["environment"] = environment
	}
	if logUserPrompt, exists := otel["log_user_prompt"]; exists {
		value, ok := logUserPrompt.(bool)
		if !ok {
			return nil, errBeaconCodexOTelConfig
		}
		payload["log_user_prompt"] = value
	}

	data, err := json.Marshal(payload)
	return data, err
}

func codexExporterProjections(value any) ([]map[string]any, error) {
	if value == nil {
		return []map[string]any{}, nil
	}
	exporters, ok := value.(map[string]any)
	if !ok {
		return nil, errBeaconCodexOTelConfig
	}
	names := make([]string, 0, len(exporters))
	for name := range exporters {
		if name == "otlp-grpc" || name == "otlp-http" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	projected := make([]map[string]any, 0, len(names))
	for _, name := range names {
		config, ok := exporters[name].(map[string]any)
		if !ok {
			return nil, errBeaconCodexOTelConfig
		}
		endpoint, exists := config["endpoint"]
		scope := "not_configured"
		if exists {
			endpointValue, ok := endpoint.(string)
			if !ok {
				return nil, errBeaconCodexOTelConfig
			}
			if strings.TrimSpace(endpointValue) != "" {
				scope = otelEndpointScope(endpointValue)
			}
		}
		projected = append(projected, map[string]any{
			"name":           name,
			"endpoint_scope": scope,
		})
	}
	return projected, nil
}

func otelEndpointScope(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Hostname() == "" {
		return "unknown"
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" {
		return "loopback"
	}
	if address := net.ParseIP(host); address != nil && address.IsLoopback() {
		return "loopback"
	}
	return "non_loopback"
}
