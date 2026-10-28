package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"

	"github.com/threadforge-ai/threadforge/internal/ci"
)

func TestCIChildProcessHelper(t *testing.T) {
	if os.Getenv("THREADFORGE_TEST_CI_HELPER") != "1" {
		return
	}
	if os.Getenv("CLAUDE_CODE_ENABLE_TELEMETRY") != "1" ||
		os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL") != "http/json" ||
		os.Getenv("OTEL_LOG_USER_PROMPTS") != "0" ||
		os.Getenv("OTEL_LOG_TOOL_DETAILS") != "0" ||
		os.Getenv("THREADFORGE_CI") != "true" {
		os.Exit(90)
	}
	if os.Getenv("THREADFORGE_TEST_CI_NO_EVENT") != "1" {
		endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") + "/v1/logs"
		if !strings.HasPrefix(os.Getenv("THREADFORGE_EVENT_ENDPOINT"), strings.TrimSuffix(endpoint, "/v1/logs")) {
			os.Exit(91)
		}
		attrs := make([]map[string]any, 0)
		for _, part := range strings.Split(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), ",") {
			key, value, ok := strings.Cut(part, "=")
			if !ok || key == "" {
				continue
			}
			decoded, err := url.QueryUnescape(value)
			if err != nil {
				os.Exit(91)
			}
			attrs = append(attrs, map[string]any{"key": key, "value": map[string]string{"stringValue": decoded}})
		}
		attrs = append(attrs, map[string]any{"key": "service.name", "value": map[string]string{"stringValue": "claude_code"}})
		resourceJSON, err := json.Marshal(map[string]any{"attributes": attrs})
		if err != nil {
			os.Exit(91)
		}
		bodyJSON, err := json.Marshal(map[string]any{"resourceLogs": []any{map[string]any{
			"resource": json.RawMessage(resourceJSON),
			"scopeLogs": []any{map[string]any{"scope": map[string]string{"name": "claude-code", "version": "fixture"},
				"logRecords": []any{map[string]any{
					"timeUnixNano": strconv.FormatInt(time.Now().UnixNano(), 10), "severityText": "INFO", "severityNumber": 9,
					"body": map[string]string{"stringValue": "ci fixture metadata"},
					"attributes": []any{
						map[string]any{"key": "event.action", "value": map[string]string{"stringValue": "session.started"}},
						map[string]any{"key": "event.kind", "value": map[string]string{"stringValue": "agent_runtime"}},
						map[string]any{"key": "api_key", "value": map[string]string{"stringValue": "ci-helper-secret-must-be-redacted"}},
					},
				}},
			}},
		}}})
		if err != nil {
			os.Exit(91)
		}
		body := string(bodyJSON)
		response, err := http.Post(endpoint, "application/json", strings.NewReader(body))
		if err != nil {
			os.Exit(92)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			os.Exit(93)
		}
	}
	code, _ := strconv.Atoi(os.Getenv("THREADFORGE_TEST_CI_EXIT_CODE"))
	os.Exit(code)
}

func TestCICodexChildProcessHelper(t *testing.T) {
	if os.Getenv("THREADFORGE_TEST_CI_CODEX_HELPER") != "1" {
		return
	}
	home := os.Getenv("CODEX_HOME")
	config, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || !strings.Contains(string(config), `endpoint = "http://127.0.0.1:`) ||
		!strings.Contains(string(config), `metrics_exporter = "none"`) || !strings.Contains(string(config), "log_user_prompt = false") {
		os.Exit(94)
	}
	if os.Getenv("CODEX_API_KEY") != "codex-api-key-child-only" ||
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_HEADERS") != "" ||
		strings.Contains(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), "private.secret") {
		os.Exit(95)
	}
	attrs := []map[string]any{
		{"key": "event.action", "value": map[string]string{"stringValue": "session.started"}},
		{"key": "event.kind", "value": map[string]string{"stringValue": "agent_runtime"}},
	}
	body, err := json.Marshal(map[string]any{"resourceLogs": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{
			map[string]any{"key": "service.name", "value": map[string]string{"stringValue": "codex_cli"}},
			map[string]any{"key": "threadforge.origin", "value": map[string]string{"stringValue": "ci"}},
		}},
		"scopeLogs": []any{map[string]any{"scope": map[string]string{"name": "codex-cli", "version": "test"}, "logRecords": []any{map[string]any{
			"timeUnixNano": strconv.FormatInt(time.Now().UnixNano(), 10), "severityText": "INFO", "severityNumber": 9,
			"body": map[string]string{"stringValue": "codex CI local fixture"}, "attributes": attrs,
		}}}},
	}}})
	if err != nil {
		os.Exit(96)
	}
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = os.Getenv("THREADFORGE_EVENT_ENDPOINT")
		endpoint = strings.TrimSuffix(endpoint, "/v1/events")
	}
	response, err := http.Post(endpoint+"/v1/logs", "application/json", bytes.NewReader(body))
	if err != nil {
		os.Exit(97)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		os.Exit(98)
	}
}

func probeLoopback(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listen unavailable in this environment: %v", err)
	}
	defer ln.Close()
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond)
	if err != nil {
		t.Skipf("loopback connections unavailable in this environment (AppContainer/sandbox socket isolation): %v", err)
	}
	_ = conn.Close()
}

func TestCIExecStartsLocalCaptureValidatesAndKeepsOnlyRedactedLog(t *testing.T) {
	probeLoopback(t)
	outputBase := t.TempDir()
	t.Setenv("THREADFORGE_TEST_CI_HELPER", "1")
	t.Setenv("THREADFORGE_TEST_CI_NO_EVENT", "")
	t.Setenv("THREADFORGE_TEST_CI_EXIT_CODE", "0")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")
	t.Setenv("GITHUB_RUN_ID", "gh-run-17")
	t.Setenv("GITHUB_RUN_ATTEMPT", "2")
	t.Setenv("GITHUB_WORKFLOW", "Threadforge Review, CI")
	t.Setenv("GITHUB_JOB", "agent-review")
	t.Setenv("GITHUB_SHA", "0123456789abcdef0123456789abcdef01234567")
	t.Setenv("GITHUB_REPOSITORY", "threadforge/example")
	t.Setenv("GITHUB_ACTOR", "review-user")
	t.Setenv("GITHUB_HEAD_REF", "feature/inventory")
	t.Setenv("GITHUB_REF", "refs/pull/42/merge")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"ci", "exec", "--output-dir", outputBase, "--harness", "claude-code", "--json", "--",
		executable, "-test.run=^TestCIChildProcessHelper$",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("ci exec code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result ci.ExecResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode result %q: %v", stdout.String(), err)
	}
	if !result.ChildStarted || result.ChildExitCode != 0 || result.Validation.Status != "passed" || result.Validation.MatchedEvents != 1 {
		t.Fatalf("unexpected ci exec result: %#v", result)
	}
	logPath := filepath.Join(result.ArtifactDir, "runtime.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ci-helper-secret-must-be-redacted") || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("CI log did not redact credential-like attributes: %s", data)
	}
	var event beacon.Event
	if err := json.Unmarshal(bytes.TrimSpace(data), &event); err != nil {
		t.Fatal(err)
	}
	if event.Origin != beacon.OriginCI || event.Run == nil || event.Run.Provider != "github_actions" ||
		event.Run.RunID != "gh-run-17" || event.Run.RunAttempt != "2" || event.Run.Workflow != "Threadforge Review, CI" ||
		event.Run.Job != "agent-review" || event.Run.Commit != "0123456789abcdef0123456789abcdef01234567" ||
		event.Run.Repository != "threadforge/example" || event.Run.Branch != "feature/inventory" ||
		event.Run.PR != "refs/pull/42/merge" || event.Run.PRNumber != "42" || event.Run.Actor != "review-user" || !event.Run.Ephemeral {
		t.Fatalf("GitHub run metadata was not normalized: %#v", event)
	}
	if _, err := os.Stat(filepath.Join(result.ArtifactDir, "threadforge-ci-summary.json")); err != nil {
		t.Fatalf("missing summary artifact: %v", err)
	}
}

func TestCIExecPreservesChildExitAndAllowsWarningMode(t *testing.T) {
	t.Setenv("THREADFORGE_TEST_CI_HELPER", "1")
	t.Setenv("THREADFORGE_TEST_CI_NO_EVENT", "1")
	t.Setenv("THREADFORGE_TEST_CI_EXIT_CODE", "7")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"ci", "exec", "--output-dir", t.TempDir(), "--require-telemetry=false", "--json", "--",
		executable, "-test.run=^TestCIChildProcessHelper$",
	}, &stdout, &stderr)
	if code != 7 {
		t.Fatalf("ci exec returned %d, want original child status 7; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result ci.ExecResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode result %q: %v", stdout.String(), err)
	}
	if result.ChildExitCode != 7 || result.Validation.Status != "failed" || result.RequireTelemetry {
		t.Fatalf("unexpected warning-mode result: %#v", result)
	}
}

func TestCIValidateCLIReadsExistingLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"ci", "validate", "--log-path", path, "--json"}, &stdout, &stderr); code == 0 {
		t.Fatalf("ci validate passed an invalid event: %s", stdout.String())
	}
}

func TestCISecurityGateCLIReportsOnlyAggregateOutcome(t *testing.T) {
	t.Setenv("THREADFORGE_HOME", t.TempDir())
	logPath := filepath.Join(t.TempDir(), "runtime.jsonl")
	event := beacon.NewEvent(beacon.NewEventOptions{
		Action: "prompt.submitted", Harness: beacon.HarnessInfo{Name: "claude-code"},
	})
	event.Prompt = &beacon.PromptInfo{Text: "Ignore all previous instructions. ci-gate-private-content"}
	event.Session = &beacon.SessionInfo{ID: "ci-gate-private-session"}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"ci", "gate", "--log-path", logPath, "--harness", "claude-code", "--fail-on", "medium", "--json"}, &stdout, &stderr)
	var failed ci.SecurityGateResult
	if err := json.Unmarshal(stdout.Bytes(), &failed); err != nil {
		t.Fatalf("decode gate result %q: %v", stdout.String(), err)
	}
	if code != 3 || failed.Status != "failed" || failed.Validation.Status != "passed" ||
		failed.Security.FindingCount != 1 || !failed.Security.ThresholdMatched ||
		strings.Contains(stdout.String(), "ci-gate-private-content") ||
		strings.Contains(stdout.String(), "ci-gate-private-session") || strings.Contains(stdout.String(), logPath) {
		t.Fatalf("security threshold did not fail with an aggregate-only result: code=%d result=%#v stdout=%s stderr=%s", code, failed, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"ci", "gate", "--log-path", logPath, "--harness", "claude-code", "--fail-on", "critical", "--json"}, &stdout, &stderr)
	var passed ci.SecurityGateResult
	if err := json.Unmarshal(stdout.Bytes(), &passed); err != nil {
		t.Fatalf("decode passing gate result %q: %v", stdout.String(), err)
	}
	if code != 0 || passed.Status != "passed" || passed.Security.ThresholdMatched {
		t.Fatalf("non-matching security threshold failed: code=%d result=%#v stderr=%s", code, passed, stderr.String())
	}
}

func TestCIExecRequiresSeparatorBeforeChildCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ci", "exec", "--harness", "claude-code", "not-a-command"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "before the child command") {
		t.Fatalf("missing separator code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCIExecRejectsAllHarnessMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ci", "exec", "--harness", "all", "--", "not-a-command"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "one explicit harness") {
		t.Fatalf("all-harness exec code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCIExecCodexUsesPrivateOTLPHomeWithoutCopyingAuth(t *testing.T) {
	probeLoopback(t)
	userHome := filepath.Join(t.TempDir(), "user-codex")
	if err := os.Mkdir(userHome, 0o700); err != nil {
		t.Fatal(err)
	}
	userConfig := filepath.Join(userHome, "config.toml")
	if err := os.WriteFile(userConfig, []byte("model = \"user-profile\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(userHome, "auth.json")
	if err := os.WriteFile(authFile, []byte(`{"access_token":"do-not-copy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THREADFORGE_TEST_CI_CODEX_HELPER", "1")
	t.Setenv("CODEX_HOME", userHome)
	t.Setenv("CODEX_API_KEY", "codex-api-key-child-only")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://remote.invalid/v1")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer-not-forwarded")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "private.secret=must-not-be-exported")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"ci", "exec", "--harness", "codex-cli", "--output-dir", t.TempDir(), "--json", "--",
		executable, "-test.run=^TestCICodexChildProcessHelper$",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Codex ci exec code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result ci.ExecResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode Codex CI result %q: %v", stdout.String(), err)
	}
	if result.Harness != "codex_cli" || result.Validation.Status != "passed" || result.Validation.MatchedEvents != 1 {
		t.Fatalf("unexpected Codex CI result: %#v", result)
	}
	logData, err := os.ReadFile(filepath.Join(result.ArtifactDir, "runtime.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "codex-api-key-child-only") || strings.Contains(string(logData), "do-not-copy") ||
		strings.Contains(string(logData), "private.secret") {
		t.Fatalf("Codex runtime log contains private auth/config data: %s", logData)
	}
	if original, err := os.ReadFile(userConfig); err != nil || string(original) != "model = \"user-profile\"\n" {
		t.Fatalf("user Codex config changed: %q, %v", original, err)
	}
	if original, err := os.ReadFile(authFile); err != nil || !strings.Contains(string(original), "do-not-copy") {
		t.Fatalf("user Codex auth file changed: %q, %v", original, err)
	}
}

func TestCIDetachedSessionSpansSeparateCLIProcesses(t *testing.T) {
	probeLoopback(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "threadforge")
	if os.PathSeparator == '\\' {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/threadforge")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Threadforge CLI: %v\n%s", err, output)
	}

	outputBase := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(outputBase, 0o700); err != nil {
		t.Fatal(err)
	}
	githubEnv := filepath.Join(t.TempDir(), "github-env")
	if err := os.WriteFile(githubEnv, []byte("EXISTING_SETTING=preserve\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStart()
	start := exec.CommandContext(startCtx, binary, "ci", "start", "--output-dir", outputBase)
	start.Env = append(os.Environ(),
		"GITHUB_ENV="+githubEnv,
		"THREADFORGE_TEST_PARENT_SECRET=must-not-enter-daemon",
		"GITHUB_ACTIONS=true", "GITHUB_RUN_ID=workflow-55",
		"BETA_TRACING_ENDPOINT=https://remote.invalid/trace",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=https://remote.invalid/logs",
		"OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer-do-not-forward",
		"OTEL_RESOURCE_ATTRIBUTES=internal.secret=must-not-export,service.name=user-private-value",
	)
	startOutput, err := start.CombinedOutput()
	if err != nil {
		t.Fatalf("ci start: %v\n%s", err, startOutput)
	}
	var started ci.SessionStartResult
	if err := json.Unmarshal(startOutput, &started); err != nil {
		t.Fatalf("decode ci start result %q: %v", startOutput, err)
	}
	if started.Status != "running" || started.Endpoint == "" || !started.EnvironmentFileWritten || started.Exports["THREADFORGE_CI_STATE_PATH"] != started.StatePath {
		t.Fatalf("unexpected session start result: %#v", started)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cleanupCancel()
		cleanup := exec.CommandContext(cleanupCtx, binary, "ci", "finish", "--state-path", started.StatePath, "--require-telemetry=false")
		_ = cleanup.Run()
	})
	var state ci.SessionState
	stateBytes, err := os.ReadFile(started.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	if state.ControlToken == "" || strings.Contains(string(startOutput), state.ControlToken) {
		t.Fatal("session control token was empty or exposed in start output")
	}
	if !strings.Contains(started.Exports["OTEL_RESOURCE_ATTRIBUTES"], "beacon.run.run_id=workflow-55") ||
		strings.Contains(started.Exports["OTEL_RESOURCE_ATTRIBUTES"], "internal.secret") ||
		started.Exports["OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"] != "" ||
		started.Exports["OTEL_EXPORTER_OTLP_HEADERS"] != "" || started.Exports["BETA_TRACING_ENDPOINT"] != "" {
		t.Fatalf("session exports did not isolate telemetry to local capture: %#v", started.Exports)
	}
	if runtime.GOOS != "windows" {
		stateInfo, err := os.Stat(started.StatePath)
		if err != nil {
			t.Fatal(err)
		}
		if stateInfo.Mode().Perm()&0o077 != 0 {
			t.Fatalf("session state permissions are too broad: %o", stateInfo.Mode().Perm())
		}
	}
	envBytes, err := os.ReadFile(githubEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(envBytes), "EXISTING_SETTING=preserve\n") ||
		!strings.Contains(string(envBytes), "THREADFORGE_CI_STATE_PATH="+started.StatePath+"\n") ||
		strings.Contains(string(envBytes), state.ControlToken) ||
		strings.Contains(string(envBytes), "THREADFORGE_TEST_PARENT_SECRET") ||
		strings.Contains(string(envBytes), "must-not-export") ||
		!strings.Contains(string(envBytes), "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=\n") ||
		!strings.Contains(string(envBytes), "BETA_TRACING_ENDPOINT=\n") {
		t.Fatalf("unsafe or incomplete GitHub environment append: %q", envBytes)
	}
	if runtime.GOOS != "windows" {
		envInfo, err := os.Stat(githubEnv)
		if err != nil {
			t.Fatal(err)
		}
		if envInfo.Mode().Perm() != 0o640 {
			t.Fatalf("ci start changed existing GITHUB_ENV permissions: %o", envInfo.Mode().Perm())
		}
	}
	unauthorized, err := http.Post(started.Endpoint+"/_threadforge/ci/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("send unauthenticated stop request: %v", err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthenticated receiver stop status=%d, want %d", unauthorized.StatusCode, http.StatusForbidden)
	}
	badAuth, err := http.NewRequest(http.MethodPost, started.Endpoint+"/_threadforge/ci/stop", nil)
	if err != nil {
		t.Fatal(err)
	}
	badAuth.Header.Set("Authorization", "Bearer incorrect-token")
	badAuthResponse, err := http.DefaultClient.Do(badAuth)
	if err != nil {
		t.Fatalf("send invalid-token stop request: %v", err)
	}
	_ = badAuthResponse.Body.Close()
	if badAuthResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid-token receiver stop status=%d, want %d", badAuthResponse.StatusCode, http.StatusForbidden)
	}

	event := beacon.NewEvent(beacon.NewEventOptions{
		Action: "session.started", Category: "session", Harness: beacon.HarnessInfo{Name: "claude_code", CollectionMethod: "otlp"}, Origin: beacon.OriginCI,
	})
	payload, err := json.Marshal([]beacon.Event{event})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(started.Endpoint+"/v1/events", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("post event to detached receiver: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("event intake status=%d, want %d", response.StatusCode, http.StatusAccepted)
	}

	finishCtx, cancelFinish := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelFinish()
	finish := exec.CommandContext(finishCtx, binary, "ci", "finish", "--state-path", started.StatePath, "--json")
	finishOutput, err := finish.CombinedOutput()
	if err != nil {
		stateBytes, _ := os.ReadFile(started.StatePath)
		var failureState ci.SessionState
		_ = json.Unmarshal(stateBytes, &failureState)
		daemonLog, _ := os.ReadFile(filepath.Join(started.ArtifactDir, "daemon.log"))
		t.Fatalf("ci finish: %v\n%s\nreceiver status=%q failure=%q daemon log=%s", err, finishOutput, failureState.Status, failureState.FailureReason, daemonLog)
	}
	var completed ci.SessionFinishResult
	if err := json.Unmarshal(finishOutput, &completed); err != nil {
		t.Fatalf("decode ci finish result %q: %v", finishOutput, err)
	}
	if completed.Status != "passed" || !completed.Stopped || completed.Validation.MatchedEvents != 1 {
		t.Fatalf("unexpected session finish result: %#v", completed)
	}
	publicSummaryPath := filepath.Join(started.ArtifactDir, ci.PublicSessionSummaryFileName)
	publicSummaryBytes, err := os.ReadFile(publicSummaryPath)
	if err != nil {
		t.Fatalf("missing sanitized CI summary: %v", err)
	}
	var publicSummary ci.PublicSessionSummary
	if err := json.Unmarshal(publicSummaryBytes, &publicSummary); err != nil {
		t.Fatalf("decode sanitized CI summary %q: %v", publicSummaryBytes, err)
	}
	if publicSummary.SchemaVersion != ci.PublicSessionSummarySchemaVersion || publicSummary.Harness != "claude_code" ||
		publicSummary.Status != "passed" || publicSummary.ValidationStatus != "passed" || publicSummary.MatchedEvents != 1 {
		t.Fatalf("unexpected sanitized CI summary: %#v", publicSummary)
	}
	for _, privateValue := range []string{started.ArtifactDir, started.StatePath, "workflow-55", "runtime.jsonl", "control_token"} {
		if strings.Contains(string(publicSummaryBytes), privateValue) {
			t.Fatalf("sanitized CI summary retained %q: %s", privateValue, publicSummaryBytes)
		}
	}
	stateBytes, err = os.ReadFile(started.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	state = ci.SessionState{}
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != "stopped" || state.ControlToken != "" || state.Endpoint != "" {
		t.Fatalf("stopped session retained active control state: %#v", state)
	}

	missingStart := exec.CommandContext(startCtx, binary, "ci", "start", "--output-dir", outputBase)
	missingStart.Env = append(os.Environ(), "GITHUB_ENV="+githubEnv)
	missingStartOutput, err := missingStart.CombinedOutput()
	if err != nil {
		t.Fatalf("second ci start: %v\n%s", err, missingStartOutput)
	}
	var missingStarted ci.SessionStartResult
	if err := json.Unmarshal(missingStartOutput, &missingStarted); err != nil {
		t.Fatalf("decode second ci start result %q: %v", missingStartOutput, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cleanupCancel()
		cleanup := exec.CommandContext(cleanupCtx, binary, "ci", "finish", "--state-path", missingStarted.StatePath, "--require-telemetry=false")
		_ = cleanup.Run()
	})
	missingFinish := exec.CommandContext(finishCtx, binary, "ci", "finish", "--state-path", missingStarted.StatePath, "--json")
	missingFinishOutput, err := missingFinish.CombinedOutput()
	if err == nil {
		t.Fatalf("ci finish accepted a session with no telemetry: %s", missingFinishOutput)
	}
	var missingCompleted ci.SessionFinishResult
	if decodeErr := json.Unmarshal(missingFinishOutput, &missingCompleted); decodeErr != nil {
		stateBytes, _ := os.ReadFile(missingStarted.StatePath)
		var failureState ci.SessionState
		_ = json.Unmarshal(stateBytes, &failureState)
		daemonLog, _ := os.ReadFile(filepath.Join(missingStarted.ArtifactDir, "daemon.log"))
		t.Fatalf("decode telemetry-required finish result %q: %v; receiver status=%q failure=%q daemon log=%s", missingFinishOutput, decodeErr, failureState.Status, failureState.FailureReason, daemonLog)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 || missingCompleted.Status != "failed" || !missingCompleted.Stopped {
		t.Fatalf("missing telemetry did not fail after a clean stop: err=%v result=%#v", err, missingCompleted)
	}
	missingPublicSummaryBytes, err := os.ReadFile(filepath.Join(missingStarted.ArtifactDir, ci.PublicSessionSummaryFileName))
	if err != nil {
		t.Fatalf("missing telemetry should still produce a local diagnostic summary: %v", err)
	}
	var missingPublicSummary ci.PublicSessionSummary
	if err := json.Unmarshal(missingPublicSummaryBytes, &missingPublicSummary); err != nil ||
		missingPublicSummary.Status != "failed" || missingPublicSummary.ValidationStatus != "failed" {
		t.Fatalf("unexpected failed-session public summary: %#v, err=%v", missingPublicSummary, err)
	}

	userCodexHome := filepath.Join(t.TempDir(), "user-codex")
	if err := os.Mkdir(userCodexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	userCodexConfig := filepath.Join(userCodexHome, "config.toml")
	if err := os.WriteFile(userCodexConfig, []byte("model = \"user-profile\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userCodexAuth := filepath.Join(userCodexHome, "auth.json")
	if err := os.WriteFile(userCodexAuth, []byte(`{"access_token":"codex-start-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	codexStart := exec.CommandContext(startCtx, binary, "ci", "start", "--harness", "codex-cli", "--output-dir", outputBase)
	codexStart.Env = append(os.Environ(), "GITHUB_ENV="+githubEnv, "CODEX_HOME="+userCodexHome,
		"CODEX_API_KEY=codex-start-api-key", "GITHUB_ACTIONS=true", "GITHUB_RUN_ID=codex-workflow-72")
	codexStartOutput, err := codexStart.CombinedOutput()
	if err != nil {
		t.Fatalf("Codex ci start: %v\n%s", err, codexStartOutput)
	}
	var codexStarted ci.SessionStartResult
	if err := json.Unmarshal(codexStartOutput, &codexStarted); err != nil {
		t.Fatalf("decode Codex ci start result %q: %v", codexStartOutput, err)
	}
	codexHome := codexStarted.Exports["CODEX_HOME"]
	if codexStarted.Harness != "codex_cli" || codexHome == "" {
		t.Fatalf("Codex CI start did not return an isolated home: %#v", codexStarted)
	}
	configBytes, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configBytes), "metrics_exporter = \"none\"") ||
		!strings.Contains(string(configBytes), "log_user_prompt = false") || strings.Contains(string(configBytes), "codex-start-api-key") {
		t.Fatalf("Codex session config/export is incorrect or contains credentials: %#v\n%s", codexStarted.Exports, configBytes)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("Codex CI home copied user auth material: %v", err)
	}
	if strings.Contains(string(codexStartOutput), "codex-start-api-key") {
		t.Fatal("Codex CI start output contains API key")
	}
	envBytes, err = os.ReadFile(githubEnv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(envBytes), "codex-start-api-key") || strings.Contains(string(envBytes), "codex-start-secret") {
		t.Fatal("Codex CI session exported credential material to GITHUB_ENV")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cleanupCancel()
		cleanup := exec.CommandContext(cleanupCtx, binary, "ci", "finish", "--state-path", codexStarted.StatePath, "--require-telemetry=false")
		_ = cleanup.Run()
	})
	codexEvent := beacon.NewEvent(beacon.NewEventOptions{
		Action: "session.started", Category: "session", Harness: beacon.HarnessInfo{Name: "codex_cli", CollectionMethod: "otlp"}, Origin: beacon.OriginCI,
	})
	codexPayload, err := json.Marshal([]beacon.Event{codexEvent})
	if err != nil {
		t.Fatal(err)
	}
	codexResponse, err := http.Post(codexStarted.Endpoint+"/v1/events", "application/json", bytes.NewReader(codexPayload))
	if err != nil {
		t.Fatalf("post event to Codex CI receiver: %v", err)
	}
	_ = codexResponse.Body.Close()
	if codexResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("Codex event intake status=%d, want %d", codexResponse.StatusCode, http.StatusAccepted)
	}
	codexFinish := exec.CommandContext(finishCtx, binary, "ci", "finish", "--state-path", codexStarted.StatePath, "--json")
	codexFinishOutput, err := codexFinish.CombinedOutput()
	if err != nil {
		stateBytes, _ := os.ReadFile(codexStarted.StatePath)
		var failureState ci.SessionState
		_ = json.Unmarshal(stateBytes, &failureState)
		daemonLog, _ := os.ReadFile(filepath.Join(codexStarted.ArtifactDir, "daemon.log"))
		t.Fatalf("Codex ci finish: %v\n%s\nreceiver status=%q failure=%q daemon log=%s", err, codexFinishOutput, failureState.Status, failureState.FailureReason, daemonLog)
	}
	var codexCompleted ci.SessionFinishResult
	if err := json.Unmarshal(codexFinishOutput, &codexCompleted); err != nil {
		t.Fatalf("decode Codex ci finish result %q: %v", codexFinishOutput, err)
	}
	if codexCompleted.Status != "passed" || codexCompleted.Validation.Harness != "codex_cli" || codexCompleted.Validation.MatchedEvents != 1 {
		t.Fatalf("unexpected Codex session result: %#v", codexCompleted)
	}
	if original, err := os.ReadFile(userCodexConfig); err != nil || string(original) != "model = \"user-profile\"\n" {
		t.Fatalf("Codex session changed user config: %q, %v", original, err)
	}
	if original, err := os.ReadFile(userCodexAuth); err != nil || !strings.Contains(string(original), "codex-start-secret") {
		t.Fatalf("Codex session changed user auth data: %q, %v", original, err)
	}
}
