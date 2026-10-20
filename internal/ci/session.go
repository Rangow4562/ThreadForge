package ci

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/threadforge-ai/threadforge/internal/inventory"
	"github.com/threadforge-ai/threadforge/internal/service"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/internal/traces"
)

const (
	SessionSchemaVersion = "threadforge.ci-session.v1"
	SessionStartVersion  = "threadforge.ci-session-start.v1"
	SessionFinishVersion = "threadforge.ci-session-finish.v1"
	maxSessionStateBytes = 64 << 10
	ciStopPath           = "/_threadforge/ci/stop"
)

type SessionOptions struct {
	Harness          string
	OutputDir        string
	StatePath        string
	GitHubEnvPath    string
	MinEvents        int
	RequireTelemetry bool
	ExecutablePath   string
}

type SessionState struct {
	SchemaVersion    string `json:"schema_version"`
	Status           string `json:"status"`
	Harness          string `json:"harness"`
	ArtifactDir      string `json:"artifact_dir"`
	CodexHome        string `json:"codex_home,omitempty"`
	LogPath          string `json:"log_path"`
	StatePath        string `json:"state_path"`
	Endpoint         string `json:"endpoint,omitempty"`
	ControlToken     string `json:"control_token,omitempty"`
	DaemonPID        int    `json:"daemon_pid,omitempty"`
	MinEvents        int    `json:"min_events"`
	RequireTelemetry bool   `json:"require_telemetry"`
	StartedAt        string `json:"started_at"`
	FinishedAt       string `json:"finished_at,omitempty"`
	FailureReason    string `json:"failure_reason,omitempty"`
}

type SessionStartResult struct {
	SchemaVersion          string            `json:"schema_version"`
	Status                 string            `json:"status"`
	Harness                string            `json:"harness"`
	ArtifactDir            string            `json:"artifact_dir"`
	LogPath                string            `json:"log_path"`
	StatePath              string            `json:"state_path"`
	Endpoint               string            `json:"endpoint"`
	Exports                map[string]string `json:"exports"`
	EnvironmentFileWritten bool              `json:"environment_file_written"`
}

type SessionFinishResult struct {
	SchemaVersion    string           `json:"schema_version"`
	Status           string           `json:"status"`
	ArtifactDir      string           `json:"artifact_dir"`
	StatePath        string           `json:"state_path"`
	Stopped          bool             `json:"stopped"`
	RequireTelemetry bool             `json:"require_telemetry"`
	Validation       ValidationResult `json:"validation"`
}

func StartSession(ctx context.Context, opts SessionOptions, parentEnvironment []string) (SessionStartResult, error) {
	harnessValue := strings.TrimSpace(opts.Harness)
	if harnessValue == "" {
		harnessValue = "claude-code"
	}
	harness, err := normalizeHarness(harnessValue)
	if err != nil {
		return SessionStartResult{}, err
	}
	if harness != "claude_code" && harness != "codex_cli" {
		return SessionStartResult{}, errors.New("detached CI sessions support claude-code and codex-cli")
	}
	if opts.MinEvents < 1 {
		opts.MinEvents = 1
	}
	artifactDir, err := createArtifactDir(opts.OutputDir)
	if err != nil {
		return SessionStartResult{}, err
	}
	logPath := filepath.Join(artifactDir, "runtime.jsonl")
	if _, err := traces.Open(logPath); err != nil {
		return SessionStartResult{}, errors.New("could not create private CI runtime log")
	}
	statePath, err := createSessionStatePath(artifactDir, opts.StatePath)
	if err != nil {
		return SessionStartResult{}, err
	}
	codexHome := ""
	if harness == "codex_cli" {
		codexHome, err = createCodexHome(artifactDir)
		if err != nil {
			return SessionStartResult{}, err
		}
	}
	token, err := newControlToken()
	if err != nil {
		return SessionStartResult{}, errors.New("could not create CI session control token")
	}
	state := SessionState{
		SchemaVersion: SessionSchemaVersion, Status: "starting", Harness: harness,
		ArtifactDir: artifactDir, CodexHome: codexHome, LogPath: logPath, StatePath: statePath,
		ControlToken: token, MinEvents: opts.MinEvents, RequireTelemetry: opts.RequireTelemetry,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeSessionState(state, true); err != nil {
		return SessionStartResult{}, err
	}
	executable := strings.TrimSpace(opts.ExecutablePath)
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			markSessionFailed(state, "could not resolve Threadforge executable")
			return SessionStartResult{}, errors.New("could not resolve Threadforge executable")
		}
	}
	logFile, err := os.OpenFile(filepath.Join(artifactDir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		markSessionFailed(state, "could not create private daemon log")
		return SessionStartResult{}, errors.New("could not create private CI daemon log")
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		_ = logFile.Close()
		markSessionFailed(state, "could not open null input")
		return SessionStartResult{}, errors.New("could not initialize detached CI daemon")
	}
	cmd := exec.Command(executable, "ci", "daemon", "--state-path", statePath)
	cmd.Dir = artifactDir
	cmd.Env = daemonEnvironment(parentEnvironment)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, logFile, logFile
	configureDetachedProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = logFile.Close()
		markSessionFailed(state, "daemon process could not be started")
		return SessionStartResult{}, errors.New("could not start detached CI receiver")
	}
	_ = stdin.Close()
	_ = logFile.Close()
	readyState, err := waitForDaemonReady(ctx, statePath)
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		markSessionFailed(state, "detached receiver did not become ready")
		return SessionStartResult{}, err
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	if readyState.Harness == "codex_cli" {
		if err := writeCodexConfig(readyState.CodexHome, readyState.Endpoint); err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			stopErr := stopSessionDaemon(stopCtx, readyState)
			var waitErr error
			if stopErr == nil {
				_, waitErr = waitForDaemonStopped(stopCtx, readyState.StatePath)
			}
			cancel()
			if stopErr != nil || waitErr != nil {
				return SessionStartResult{}, fmt.Errorf("could not configure Codex CI; session may still be running, state file: %s", readyState.StatePath)
			}
			return SessionStartResult{}, errors.New("could not create private Codex CI configuration")
		}
	}
	exports := sessionExports(readyState, parentEnvironment)
	environmentFile := strings.TrimSpace(opts.GitHubEnvPath)
	if environmentFile == "" {
		environmentFile = environmentValue(parentEnvironment, "GITHUB_ENV")
	}
	wroteEnvironmentFile := false
	if environmentFile != "" {
		if err := appendEnvironmentFile(environmentFile, exports); err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			stopErr := stopSessionDaemon(stopCtx, readyState)
			var waitErr error
			if stopErr == nil {
				_, waitErr = waitForDaemonStopped(stopCtx, readyState.StatePath)
			}
			cancel()
			if stopErr != nil || waitErr != nil {
				return SessionStartResult{}, fmt.Errorf("could not append CI telemetry exports; session may still be running, state file: %s", readyState.StatePath)
			}
			return SessionStartResult{}, errors.New("could not append CI telemetry exports to the environment file")
		}
		wroteEnvironmentFile = true
	}
	return SessionStartResult{
		SchemaVersion: SessionStartVersion, Status: "running", Harness: readyState.Harness,
		ArtifactDir: readyState.ArtifactDir, LogPath: readyState.LogPath, StatePath: readyState.StatePath,
		Endpoint: readyState.Endpoint, Exports: exports, EnvironmentFileWritten: wroteEnvironmentFile,
	}, nil
}

func FinishSession(ctx context.Context, statePath string, minEvents int, requireTelemetry *bool) (SessionFinishResult, error) {
	state, err := readSessionState(statePath)
	if err != nil {
		return SessionFinishResult{}, err
	}
	if state.Status == "running" || state.Status == "stopping" {
		if state.Status == "running" {
			if err := stopSessionDaemon(ctx, state); err != nil {
				return SessionFinishResult{}, err
			}
		}
		state, err = waitForDaemonStopped(ctx, state.StatePath)
		if err != nil {
			return SessionFinishResult{}, err
		}
	} else if state.Status != "stopped" {
		return SessionFinishResult{}, errors.New("CI receiver session is not running or cleanly stopped")
	}
	if minEvents < 1 {
		minEvents = state.MinEvents
	}
	required := state.RequireTelemetry
	if requireTelemetry != nil {
		required = *requireTelemetry
	}
	validation, err := ValidateLog(ValidationOptions{LogPath: state.LogPath, Harness: state.Harness, MinEvents: minEvents})
	if err != nil {
		return SessionFinishResult{}, fmt.Errorf("CI telemetry validation could not complete: %w", err)
	}
	status := "passed"
	if required && validation.Status != "passed" {
		status = "failed"
	}
	result := SessionFinishResult{
		SchemaVersion: SessionFinishVersion, Status: status, ArtifactDir: state.ArtifactDir,
		StatePath: state.StatePath, Stopped: true, RequireTelemetry: required, Validation: validation,
	}
	if err := writeSessionSummary(filepath.Join(state.ArtifactDir, "threadforge-ci-summary.json"), result); err != nil {
		return result, errors.New("CI session summary could not be written")
	}
	if err := writePublicSessionSummary(filepath.Join(state.ArtifactDir, PublicSessionSummaryFileName), result); err != nil {
		return result, errors.New("sanitized CI summary could not be written")
	}
	return result, nil
}

func RunDaemon(ctx context.Context, statePath string) error {
	state, err := readSessionState(statePath)
	if err != nil {
		return err
	}
	if state.Status != "starting" || state.Endpoint != "" || state.ControlToken == "" {
		return errors.New("CI daemon received an invalid startup state")
	}
	if _, err := inventory.ValidateScanRoot(state.ArtifactDir); err != nil || !pathIsInside(state.ArtifactDir, state.LogPath) {
		markSessionFailed(state, "artifact scope is invalid")
		return errors.New("CI daemon artifact scope is invalid")
	}
	database, err := store.Open(":memory:")
	if err != nil {
		markSessionFailed(state, "temporary database could not be initialized")
		return errors.New("CI daemon could not initialize service state")
	}
	defer database.Close()
	traceStore, err := traces.Open(state.LogPath)
	if err != nil {
		markSessionFailed(state, "runtime log could not be opened")
		return errors.New("CI daemon could not open runtime log")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		markSessionFailed(state, "loopback listener could not be opened")
		return errors.New("CI daemon could not bind a loopback receiver")
	}
	state.Endpoint = "http://" + listener.Addr().String()
	state.DaemonPID = os.Getpid()
	stopRequests := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+ciStopPath, func(w http.ResponseWriter, r *http.Request) {
		if !requestHostIsLoopback(r.Host) {
			http.Error(w, "loopback only", http.StatusForbidden)
			return
		}
		supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(state.ControlToken), []byte(supplied)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		select {
		case stopRequests <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.Handle("/", service.NewHandler(database, traceStore))
	var connectionStateMu sync.Mutex
	connectionStates := make(map[net.Conn]http.ConnState)
	server := &http.Server{
		Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
		ConnState: func(connection net.Conn, state http.ConnState) {
			connectionStateMu.Lock()
			defer connectionStateMu.Unlock()
			if state == http.StateClosed || state == http.StateHijacked {
				delete(connectionStates, connection)
				return
			}
			connectionStates[connection] = state
		},
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	state.Status = "running"
	if err := writeSessionState(state, false); err != nil {
		_ = server.Close()
		markSessionFailed(state, "ready state could not be saved")
		return errors.New("CI daemon could not publish its ready state")
	}
	select {
	case <-stopRequests:
		state.Status = "stopping"
		if err := writeSessionState(state, false); err != nil {
			_ = server.Close()
			markSessionFailed(state, "receiver stop state could not be saved")
			return errors.New("CI daemon could not record stop request")
		}
		err = shutdownHTTPServer(server, 5*time.Second)
		if err != nil {
			connectionStateMu.Lock()
			activeConnections, idleConnections, newConnections := 0, 0, 0
			for _, connectionState := range connectionStates {
				switch connectionState {
				case http.StateActive:
					activeConnections++
				case http.StateIdle:
					idleConnections++
				case http.StateNew:
					newConnections++
				}
			}
			connectionStateMu.Unlock()
			_ = server.Close()
			markSessionFailed(state, "receiver shutdown failed")
			return fmt.Errorf("CI daemon receiver did not shut down cleanly (active=%d idle=%d new=%d): %w", activeConnections, idleConnections, newConnections, err)
		}
		state.Status, state.Endpoint, state.ControlToken = "stopped", "", ""
		state.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := writeSessionState(state, false); err != nil {
			markSessionFailed(state, "receiver stopped state could not be saved")
			return errors.New("CI daemon could not record stopped state")
		}
		return nil
	case <-serverErrors:
		markSessionFailed(state, "receiver server stopped unexpectedly")
		return errors.New("CI daemon receiver stopped unexpectedly")
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = server.Shutdown(shutdownCtx)
		cancel()
		markSessionFailed(state, "daemon interrupted before finish")
		return nil
	}
}

func shutdownHTTPServer(server *http.Server, gracePeriod time.Duration) error {
	shutdown := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), gracePeriod)
		defer cancel()
		return server.Shutdown(ctx)
	}
	err := shutdown()
	if errors.Is(err, context.DeadlineExceeded) {
		// On Windows, a transiently slow ConnState/keep-alive drain can outlive
		// one grace window. Shutdown is safe to call again after its deadline;
		// keep the retry bounded and continue to fail on listener errors.
		err = shutdown()
	}
	return err
}

func createSessionStatePath(artifactDir, configured string) (string, error) {
	path := strings.TrimSpace(configured)
	if path == "" {
		path = filepath.Join(artifactDir, "ci-session.json")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("CI session state path is invalid")
	}
	if _, err := inventory.ValidateScanRoot(filepath.Dir(abs)); err != nil {
		return "", errors.New("CI session state directory must exist and contain no symlinks")
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", errors.New("CI session state file already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("CI session state file could not be inspected")
	}
	return abs, nil
}

func newControlToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func writeSessionState(state SessionState, create bool) error {
	if state.StatePath == "" {
		return errors.New("CI state path is required")
	}
	absolute, err := filepath.Abs(state.StatePath)
	if err != nil || filepath.Clean(absolute) != filepath.Clean(state.StatePath) {
		return errors.New("CI state path must be absolute and canonical")
	}
	if _, err := inventory.ValidateScanRoot(filepath.Dir(absolute)); err != nil {
		return errors.New("CI state directory is unavailable or contains symlinks")
	}
	if info, err := os.Lstat(absolute); err == nil {
		if create || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("CI state path already exists or is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("CI state path could not be inspected")
	} else if !create {
		return errors.New("CI state file disappeared")
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxSessionStateBytes {
		return errors.New("CI state exceeds size limit")
	}
	if create {
		file, err := os.OpenFile(absolute, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if err := file.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
			_ = file.Close()
			_ = os.Remove(absolute)
			return err
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			_ = file.Close()
			_ = os.Remove(absolute)
			return err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = os.Remove(absolute)
			return err
		}
		return file.Close()
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".threadforge-ci-state-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return replaceSessionStateFile(tempPath, absolute)
}

func readSessionState(path string) (SessionState, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return SessionState{}, errors.New("CI session state path is invalid")
	}
	if _, err := inventory.ValidateScanRoot(filepath.Dir(absolute)); err != nil {
		return SessionState{}, errors.New("CI session state directory is unavailable or contains symlinks")
	}
	initial, err := os.Lstat(absolute)
	if err != nil || initial.Mode()&os.ModeSymlink != 0 || !initial.Mode().IsRegular() || initial.Size() > maxSessionStateBytes {
		return SessionState{}, errors.New("CI session state must be a bounded regular file")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return SessionState{}, errors.New("CI session state could not be opened")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(initial, opened) {
		return SessionState{}, errors.New("CI session state changed before reading")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxSessionStateBytes+1))
	decoder.DisallowUnknownFields()
	var state SessionState
	if err := decoder.Decode(&state); err != nil {
		return SessionState{}, errors.New("CI session state is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return SessionState{}, errors.New("CI session state has trailing data")
	}
	if err := validateSessionState(state, absolute); err != nil {
		return SessionState{}, err
	}
	return state, nil
}

func validateSessionState(state SessionState, absolutePath string) error {
	if state.SchemaVersion != SessionSchemaVersion || state.StatePath != absolutePath || state.MinEvents < 1 {
		return errors.New("unsupported or inconsistent CI session state")
	}
	switch state.Harness {
	case "claude_code":
		if state.CodexHome != "" {
			return errors.New("Claude CI session unexpectedly contains a Codex home")
		}
	case "codex_cli":
		expectedHome := filepath.Join(state.ArtifactDir, "codex-home")
		if state.CodexHome != expectedHome || !pathIsInside(state.ArtifactDir, state.CodexHome) {
			return errors.New("Codex CI home is outside the private artifact directory")
		}
		if _, err := inventory.ValidateScanRoot(state.CodexHome); err != nil {
			return errors.New("Codex CI home is unavailable or contains symlinks")
		}
	default:
		return errors.New("unsupported CI session harness")
	}
	if _, err := time.Parse(time.RFC3339Nano, state.StartedAt); err != nil {
		return errors.New("CI session start time is invalid")
	}
	artifactDir, err := filepath.Abs(state.ArtifactDir)
	if err != nil || filepath.Clean(artifactDir) != state.ArtifactDir || filepath.Clean(state.LogPath) != state.LogPath ||
		state.LogPath != filepath.Join(state.ArtifactDir, "runtime.jsonl") {
		return errors.New("CI session artifact paths are invalid")
	}
	if _, err := inventory.ValidateScanRoot(state.ArtifactDir); err != nil || !pathIsInside(state.ArtifactDir, state.LogPath) {
		return errors.New("CI session artifact paths are invalid")
	}
	switch state.Status {
	case "starting":
		if state.ControlToken == "" || state.Endpoint != "" {
			return errors.New("CI session startup state is inconsistent")
		}
	case "running", "stopping":
		if state.ControlToken == "" || !isLoopbackEndpoint(state.Endpoint) || state.DaemonPID < 1 {
			return errors.New("CI session receiver state is inconsistent")
		}
	case "stopped":
		if state.ControlToken != "" || state.Endpoint != "" || state.FinishedAt == "" {
			return errors.New("stopped CI session retains control credentials")
		}
	case "failed":
		if state.ControlToken != "" || state.Endpoint != "" || state.FailureReason == "" || state.FinishedAt == "" {
			return errors.New("failed CI session state is inconsistent")
		}
	default:
		return errors.New("unknown CI session state")
	}
	return nil
}

func waitForDaemonReady(ctx context.Context, statePath string) (SessionState, error) {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := readSessionState(statePath)
		if err == nil {
			switch state.Status {
			case "running":
				if healthErr := probeDaemon(ctx, state.Endpoint); healthErr == nil {
					return state, nil
				}
			case "failed":
				return SessionState{}, errors.New("detached CI receiver failed during startup")
			}
		}
		select {
		case <-ctx.Done():
			return SessionState{}, errors.New("CI session startup was canceled")
		case <-deadline.C:
			return SessionState{}, errors.New("detached CI receiver startup timed out")
		case <-ticker.C:
		}
	}
}

func probeDaemon(ctx context.Context, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/healthz", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("receiver health check failed")
	}
	return nil
}

func stopSessionDaemon(ctx context.Context, state SessionState) error {
	if state.Status != "running" && state.Status != "stopping" {
		return errors.New("CI receiver is not in a stoppable state")
	}
	if !isLoopbackEndpoint(state.Endpoint) || state.ControlToken == "" {
		return errors.New("CI receiver control state is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, state.Endpoint+ciStopPath, nil)
	if err != nil {
		return errors.New("CI receiver stop request could not be created")
	}
	request.Header.Set("Authorization", "Bearer "+state.ControlToken)
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("CI receiver could not be reached for shutdown")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return errors.New("CI receiver rejected the authenticated shutdown request")
	}
	return nil
}

func waitForDaemonStopped(ctx context.Context, statePath string) (SessionState, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		state, err := readSessionState(statePath)
		if err == nil {
			if state.Status == "stopped" {
				return state, nil
			}
			if state.Status == "failed" {
				return SessionState{}, errors.New("CI receiver failed before clean shutdown")
			}
		}
		select {
		case <-ctx.Done():
			return SessionState{}, errors.New("CI receiver shutdown wait was canceled")
		case <-deadline.C:
			return SessionState{}, errors.New("CI receiver shutdown timed out")
		case <-ticker.C:
		}
	}
}

func markSessionFailed(state SessionState, reason string) {
	state.Status, state.Endpoint, state.ControlToken = "failed", "", ""
	state.FailureReason = reason
	state.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	_ = writeSessionState(state, false)
}

func sessionExports(state SessionState, parentEnvironment []string) map[string]string {
	if state.Harness == "codex_cli" {
		return codexSessionExports(state, parentEnvironment)
	}
	values := claudeChildEnvironment(parentEnvironment, state.Endpoint, state.ArtifactDir)
	allowed := map[string]bool{
		"CLAUDE_CODE_ENABLE_TELEMETRY": true, "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": true,
		"OTEL_LOGS_EXPORTER": true, "OTEL_METRICS_EXPORTER": true, "OTEL_TRACES_EXPORTER": true,
		"OTEL_EXPORTER_OTLP_PROTOCOL": true, "OTEL_EXPORTER_OTLP_ENDPOINT": true, "OTEL_EXPORTER_OTLP_TIMEOUT": true,
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": true, "OTEL_METRIC_EXPORT_INTERVAL": true,
		"OTEL_BLRP_SCHEDULE_DELAY": true, "OTEL_BSP_SCHEDULE_DELAY": true,
		"OTEL_LOG_USER_PROMPTS": true, "OTEL_LOG_ASSISTANT_RESPONSES": true,
		"OTEL_LOG_TOOL_DETAILS": true, "OTEL_LOG_TOOL_CONTENT": true, "OTEL_LOG_RAW_API_BODIES": true,
		"OTEL_RESOURCE_ATTRIBUTES": true, "THREADFORGE_EVENT_ENDPOINT": true,
		"THREADFORGE_CI": true, "THREADFORGE_CI_ARTIFACT_DIR": true,
	}
	exports := map[string]string{
		"THREADFORGE_CI_STATE_PATH": state.StatePath,
		// Replace arbitrary inherited resource attributes in later workflow steps
		// with only the bounded, allowlisted run metadata projection.
		"OTEL_RESOURCE_ATTRIBUTES": resourceAttributesFromEnvironment(parentEnvironment),
	}
	for _, key := range []string{
		"BETA_TRACING_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS",
		"OTEL_EXPORTER_OTLP_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_KEY",
	} {
		exports[key] = ""
	}
	for _, entry := range values {
		key, value, ok := strings.Cut(entry, "=")
		if ok && allowed[key] && key != "OTEL_RESOURCE_ATTRIBUTES" {
			exports[key] = value
		}
	}
	return exports
}

func appendEnvironmentFile(path string, values map[string]string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return errors.New("environment file path is invalid")
	}
	parent := filepath.Dir(abs)
	if _, err := inventory.ValidateScanRoot(parent); err != nil {
		return errors.New("environment file parent is unavailable or contains symlinks")
	}
	if info, err := os.Lstat(abs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("environment file must be a regular, non-symlink file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("environment file could not be inspected")
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if key == "" || strings.ContainsAny(key, "=\r\n") || strings.ContainsAny(value, "\r\n") {
			return errors.New("CI environment exports contain an invalid key or multiline value")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("environment file could not be opened")
	}
	defer file.Close()
	opened, statErr := file.Stat()
	current, pathErr := os.Lstat(abs)
	if statErr != nil || pathErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return errors.New("environment file changed while it was being opened")
	}
	for _, key := range keys {
		if _, err := fmt.Fprintf(file, "%s=%s\n", key, values[key]); err != nil {
			return errors.New("environment exports could not be appended")
		}
	}
	return file.Sync()
}

func daemonEnvironment(parent []string) []string {
	allowed := map[string]bool{
		"PATH": true, "SYSTEMROOT": true, "WINDIR": true, "TEMP": true, "TMP": true,
		"HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true,
	}
	values := map[string]string{"THREADFORGE_CI_DAEMON_MODE": "1"}
	for _, entry := range parent {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		check := key
		if runtime.GOOS == "windows" {
			check = strings.ToUpper(key)
		}
		if allowed[check] {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func isLoopbackEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	ip := net.ParseIP(parsed.Hostname())
	port := parsed.Port()
	if ip == nil || !ip.IsLoopback() || port == "" {
		return false
	}
	portNumber, err := strconv.Atoi(port)
	return err == nil && portNumber > 0 && portNumber <= 65535
}

func requestHostIsLoopback(host string) bool {
	name, _, err := net.SplitHostPort(host)
	if err != nil {
		name = strings.Trim(host, "[]")
	}
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

func pathIsInside(root, target string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}

func writeSessionSummary(path string, result SessionFinishResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxSessionStateBytes {
		return errors.New("CI session summary exceeds size limit")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func environmentValue(values []string, name string) string {
	for _, entry := range values {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if runtime.GOOS == "windows" {
			if strings.EqualFold(key, name) {
				return value
			}
		} else if key == name {
			return value
		}
	}
	return ""
}
