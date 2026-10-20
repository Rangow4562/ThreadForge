package ci

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/threadforge-ai/threadforge/internal/inventory"
)

func createCodexHome(artifactDir string) (string, error) {
	home := filepath.Join(artifactDir, "codex-home")
	if !pathIsInside(artifactDir, home) {
		return "", errors.New("Codex home must be inside the private CI artifact directory")
	}
	if err := os.Mkdir(home, 0o700); err != nil {
		return "", errors.New("private Codex CI home could not be created")
	}
	if _, err := inventory.ValidateScanRoot(home); err != nil {
		return "", errors.New("private Codex CI home is unavailable or contains symlinks")
	}
	return home, nil
}

func writeCodexConfig(codexHome, endpoint string) error {
	if _, err := inventory.ValidateScanRoot(codexHome); err != nil || !isLoopbackEndpoint(endpoint) {
		return errors.New("Codex CI configuration requires a private home and a loopback receiver")
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if _, err := os.Lstat(configPath); err == nil {
		return errors.New("Codex CI config path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("Codex CI config path could not be inspected")
	}
	body := strings.Join([]string{
		"[otel]",
		"environment = \"ci\"",
		"metrics_exporter = \"none\"",
		"log_user_prompt = false",
		"",
		"[otel.exporter.\"otlp-http\"]",
		"endpoint = " + strconv.Quote(endpoint+"/v1/logs"),
		"protocol = \"json\"",
		"",
		"[otel.trace_exporter.\"otlp-http\"]",
		"endpoint = " + strconv.Quote(endpoint+"/v1/traces"),
		"protocol = \"json\"",
		"",
	}, "\n")
	file, err := os.OpenFile(configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("Codex CI config could not be created")
	}
	if err := file.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		_ = file.Close()
		_ = os.Remove(configPath)
		return errors.New("Codex CI config permissions could not be restricted")
	}
	if _, err := file.WriteString(body); err != nil {
		_ = file.Close()
		_ = os.Remove(configPath)
		return errors.New("Codex CI config could not be written")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(configPath)
		return errors.New("Codex CI config could not be synced")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(configPath)
		return errors.New("Codex CI config could not be closed")
	}
	return nil
}

func codexChildEnvironment(parent []string, codexHome, endpoint, artifactDir string) []string {
	type envEntry struct{ name, value string }
	normalize := func(key string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(key)
		}
		return key
	}
	entries := make(map[string]envEntry, len(parent)+5)
	passthrough := make([]string, 0, 2)
	for _, entry := range parent {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			passthrough = append(passthrough, entry)
			continue
		}
		entries[normalize(key)] = envEntry{name: key, value: value}
	}
	blocked := map[string]bool{
		"CODEX_HOME": true, "BETA_TRACING_ENDPOINT": true,
		"OTEL_EXPORTER_OTLP_ENDPOINT": true, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": true,
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": true, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": true,
		"OTEL_EXPORTER_OTLP_PROTOCOL": true, "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": true,
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": true, "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": true,
		"OTEL_EXPORTER_OTLP_HEADERS": true, "OTEL_EXPORTER_OTLP_LOGS_HEADERS": true,
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS": true, "OTEL_EXPORTER_OTLP_TRACES_HEADERS": true,
		"OTEL_EXPORTER_OTLP_CERTIFICATE": true, "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE": true,
		"OTEL_EXPORTER_OTLP_CLIENT_KEY": true, "OTEL_LOGS_EXPORTER": true,
		"OTEL_METRICS_EXPORTER": true, "OTEL_TRACES_EXPORTER": true,
		"OTEL_RESOURCE_ATTRIBUTES": true,
	}
	for key := range blocked {
		delete(entries, normalize(key))
	}
	set := func(key, value string) { entries[normalize(key)] = envEntry{name: key, value: value} }
	set("CODEX_HOME", codexHome)
	set("OTEL_RESOURCE_ATTRIBUTES", resourceAttributesFromEnvironment(parent))
	set("THREADFORGE_EVENT_ENDPOINT", endpoint+"/v1/events")
	set("THREADFORGE_CI", "true")
	set("THREADFORGE_CI_ARTIFACT_DIR", artifactDir)
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := append([]string(nil), passthrough...)
	for _, key := range keys {
		entry := entries[key]
		out = append(out, fmt.Sprintf("%s=%s", entry.name, entry.value))
	}
	return out
}

func codexSessionExports(state SessionState, parentEnvironment []string) map[string]string {
	exports := map[string]string{
		"CODEX_HOME":                  state.CodexHome,
		"THREADFORGE_CI_STATE_PATH":   state.StatePath,
		"THREADFORGE_EVENT_ENDPOINT":  state.Endpoint + "/v1/events",
		"THREADFORGE_CI":              "true",
		"THREADFORGE_CI_ARTIFACT_DIR": state.ArtifactDir,
		"OTEL_RESOURCE_ATTRIBUTES":    resourceAttributesFromEnvironment(parentEnvironment),
	}
	for _, key := range []string{
		"BETA_TRACING_ENDPOINT",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS",
		"OTEL_EXPORTER_OTLP_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_KEY",
		"OTEL_LOGS_EXPORTER", "OTEL_METRICS_EXPORTER", "OTEL_TRACES_EXPORTER",
	} {
		exports[key] = ""
	}
	return exports
}
