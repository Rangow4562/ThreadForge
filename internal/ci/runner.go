package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/inventory"
	"github.com/threadforge-ai/threadforge/internal/service"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/internal/traces"
)

const ExecSchemaVersion = "threadforge.ci-exec.v1"

type ExecOptions struct {
	Harness          string
	OutputDir        string
	WorkingDir       string
	MinEvents        int
	RequireTelemetry bool
}

type ExecResult struct {
	SchemaVersion    string           `json:"schema_version"`
	Harness          string           `json:"harness"`
	ArtifactDir      string           `json:"artifact_dir"`
	ChildStarted     bool             `json:"child_started"`
	ChildExitCode    int              `json:"child_exit_code"`
	RequireTelemetry bool             `json:"require_telemetry"`
	Validation       ValidationResult `json:"validation"`
	StartedAt        string           `json:"started_at"`
	FinishedAt       string           `json:"finished_at"`
}

// Execute wraps one child process in a temporary loopback HTTP receiver. It
// modifies only the child's telemetry environment and does not install or
// rewrite host configuration.
func Execute(ctx context.Context, opts ExecOptions, command []string, stdout, stderr io.Writer) (ExecResult, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return ExecResult{}, errors.New("a child command is required after --")
	}
	harnessValue := strings.TrimSpace(opts.Harness)
	if harnessValue == "" {
		harnessValue = "claude-code"
	}
	harness, err := normalizeHarness(harnessValue)
	if err != nil {
		return ExecResult{}, err
	}
	if harness == "" {
		return ExecResult{}, errors.New("ci exec requires one explicit harness")
	}
	if harness != "claude_code" && harness != "codex_cli" {
		return ExecResult{}, errors.New("ci exec supports claude-code and codex-cli")
	}
	if opts.MinEvents < 1 {
		opts.MinEvents = 1
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	artifactDir, err := createArtifactDir(opts.OutputDir)
	if err != nil {
		return ExecResult{}, err
	}
	logPath := filepath.Join(artifactDir, "runtime.jsonl")
	traceStore, err := traces.Open(logPath)
	if err != nil {
		return ExecResult{}, errors.New("could not create private CI runtime log")
	}
	database, err := store.Open(":memory:")
	if err != nil {
		return ExecResult{}, errors.New("could not initialize temporary CI service state")
	}
	defer database.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ExecResult{}, errors.New("could not bind the temporary CI receiver to loopback")
	}
	server := &http.Server{
		Handler:           service.NewHandler(database, traceStore),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	endpoint := "http://" + listener.Addr().String()
	codexHome := ""
	if harness == "codex_cli" {
		codexHome, err = createCodexHome(artifactDir)
		if err != nil {
			_ = server.Close()
			return ExecResult{}, err
		}
		if err := writeCodexConfig(codexHome, endpoint); err != nil {
			_ = server.Close()
			return ExecResult{}, errors.New("could not create private Codex CI configuration")
		}
	}

	started := time.Now().UTC()
	result := ExecResult{
		SchemaVersion: ExecSchemaVersion, Harness: harness, ArtifactDir: artifactDir,
		RequireTelemetry: opts.RequireTelemetry, StartedAt: started.Format(time.RFC3339Nano),
	}
	childCtx, cancelChild := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, command[0], command[1:]...)
	if opts.WorkingDir != "" {
		workingDir, err := inventory.ValidateScanRoot(opts.WorkingDir)
		if err != nil {
			cancelChild()
			_ = server.Close()
			return result, errors.New("CI child working directory is unavailable or contains symlinks")
		}
		cmd.Dir = workingDir
	}
	if harness == "codex_cli" {
		cmd.Env = codexChildEnvironment(os.Environ(), codexHome, endpoint, artifactDir)
	} else {
		cmd.Env = claudeChildEnvironment(os.Environ(), endpoint, artifactDir)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	childErr := cmd.Run()
	cancelChild()
	if childErr == nil {
		result.ChildStarted = true
	} else {
		var exitErr *exec.ExitError
		if errors.As(childErr, &exitErr) {
			result.ChildStarted = true
			result.ChildExitCode = exitErr.ExitCode()
		} else {
			_ = server.Close()
			return result, errors.New("could not start or complete CI child command")
		}
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	shutdownErr := server.Shutdown(shutdownCtx)
	cancelShutdown()
	if shutdownErr != nil {
		_ = server.Close()
		return result, errors.New("temporary CI receiver did not shut down cleanly")
	}
	if serveErrValue := <-serveErr; serveErrValue != nil && !errors.Is(serveErrValue, http.ErrServerClosed) {
		return result, errors.New("temporary CI receiver stopped unexpectedly")
	}
	result.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	result.Validation, err = ValidateLog(ValidationOptions{LogPath: logPath, Harness: harness, MinEvents: opts.MinEvents})
	if err != nil {
		return result, fmt.Errorf("CI telemetry validation could not complete: %w", err)
	}
	if err := writeResult(filepath.Join(artifactDir, "threadforge-ci-summary.json"), result); err != nil {
		return result, errors.New("CI summary artifact could not be written")
	}
	return result, nil
}

func createArtifactDir(configured string) (string, error) {
	base := strings.TrimSpace(configured)
	if base == "" {
		parent := strings.TrimSpace(os.Getenv("RUNNER_TEMP"))
		if parent == "" {
			parent = os.TempDir()
		}
		if _, err := inventory.ValidateScanRoot(parent); err != nil {
			return "", errors.New("default CI artifact parent is unavailable or contains symlinks")
		}
		base = filepath.Join(parent, "threadforge")
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return "", errors.New("CI output directory is invalid")
	}
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		if _, parentErr := inventory.ValidateScanRoot(filepath.Dir(abs)); parentErr != nil {
			return "", errors.New("CI output parent must already exist and contain no symlinks")
		}
		if err := os.Mkdir(abs, 0o700); err != nil {
			return "", errors.New("CI output directory could not be created")
		}
	} else if err != nil {
		return "", errors.New("CI output directory could not be inspected")
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("CI output path must be a real directory, not a symlink")
	} else if _, err := inventory.ValidateScanRoot(abs); err != nil {
		return "", errors.New("CI output directory path contains symlinks")
	}
	return os.MkdirTemp(abs, "ci-")
}

func claudeChildEnvironment(parent []string, endpoint, artifactDir string) []string {
	type envEntry struct{ name, value string }
	entries := map[string]envEntry{}
	passthrough := make([]string, 0, 2)
	normalizeKey := func(key string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(key)
		}
		return key
	}
	for _, item := range parent {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			passthrough = append(passthrough, item)
			continue
		}
		entries[normalizeKey(key)] = envEntry{name: key, value: value}
	}
	for _, key := range []string{
		"BETA_TRACING_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS",
		"OTEL_EXPORTER_OTLP_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_KEY",
	} {
		delete(entries, normalizeKey(key))
	}
	set := func(key, value string) { entries[normalizeKey(key)] = envEntry{name: key, value: value} }
	if attributes := resourceAttributesFromEnvironment(parent); attributes != "" {
		resourceKey := normalizeKey("OTEL_RESOURCE_ATTRIBUTES")
		existing := strings.TrimSpace(entries[resourceKey].value)
		if existing != "" {
			attributes = existing + "," + attributes
		}
		set("OTEL_RESOURCE_ATTRIBUTES", attributes)
	}
	for key, value := range map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY":                      "1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA":               "1",
		"OTEL_LOGS_EXPORTER":                                "otlp",
		"OTEL_METRICS_EXPORTER":                             "otlp",
		"OTEL_TRACES_EXPORTER":                              "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":                       "http/json",
		"OTEL_EXPORTER_OTLP_ENDPOINT":                       endpoint,
		"OTEL_EXPORTER_OTLP_TIMEOUT":                        "5000",
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": "delta",
		"OTEL_METRIC_EXPORT_INTERVAL":                       "5000",
		"OTEL_BLRP_SCHEDULE_DELAY":                          "1000",
		"OTEL_BSP_SCHEDULE_DELAY":                           "1000",
		"OTEL_LOG_USER_PROMPTS":                             "0",
		"OTEL_LOG_ASSISTANT_RESPONSES":                      "0",
		"OTEL_LOG_TOOL_DETAILS":                             "0",
		"OTEL_LOG_TOOL_CONTENT":                             "0",
		"OTEL_LOG_RAW_API_BODIES":                           "0",
		"THREADFORGE_EVENT_ENDPOINT":                        endpoint + "/v1/events",
		"THREADFORGE_CI":                                    "true",
		"THREADFORGE_CI_ARTIFACT_DIR":                       artifactDir,
	} {
		set(key, value)
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := append([]string(nil), passthrough...)
	for _, key := range keys {
		entry := entries[key]
		out = append(out, entry.name+"="+entry.value)
	}
	return out
}

func writeResult(path string, result ExecResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}
