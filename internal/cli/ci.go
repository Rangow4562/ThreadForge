package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/ci"
)

func runCI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge ci <exec|start|finish|validate|gate>")
		return 2
	}
	switch args[0] {
	case "exec":
		return runCIExec(args[1:], stdout, stderr)
	case "start":
		return runCIStart(args[1:], stdout, stderr)
	case "finish":
		return runCIFinish(args[1:], stdout, stderr)
	case "validate":
		return runCIValidate(args[1:], stdout, stderr)
	case "gate":
		return runCISecurityGate(args[1:], stdout, stderr)
	case "daemon":
		return runCIDaemon(args[1:], stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown ci command %q\nusage: threadforge ci <exec|start|finish|validate|gate>\n", args[0])
		return 2
	}
}

func runCIStart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	harness := fs.String("harness", "claude-code", "CI harness to configure (claude-code or codex-cli)")
	outputDir := fs.String("output-dir", "", "existing output base directory (defaults under RUNNER_TEMP or the system temp directory)")
	statePath := fs.String("state-path", "", "private session-state file path (defaults inside the artifact directory)")
	githubEnv := fs.String("github-env", "", "GitHub Actions environment file to append telemetry settings to (defaults to GITHUB_ENV)")
	minEvents := fs.Int("min-events", 1, "minimum matching harness events required for telemetry validation")
	requireTelemetry := fs.Bool("require-telemetry", true, "fail at finish when telemetry validation does not pass")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *minEvents < 1 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge ci start [--harness claude-code|codex-cli] [--output-dir <dir>] [--state-path <file>] [--github-env <file>] [--min-events n] [--require-telemetry=true|false]")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := ci.StartSession(ctx, ci.SessionOptions{
		Harness: *harness, OutputDir: *outputDir, StatePath: *statePath,
		GitHubEnvPath: *githubEnv, MinEvents: *minEvents, RequireTelemetry: *requireTelemetry,
	}, os.Environ())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = ci.FinishSession(cleanupCtx, result.StatePath, 0, nil)
		cancel()
		_, _ = fmt.Fprintln(stderr, "could not write CI session start result")
		return 1
	}
	return 0
}

func runCIFinish(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci finish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	statePath := fs.String("state-path", strings.TrimSpace(os.Getenv("THREADFORGE_CI_STATE_PATH")), "session state file (defaults to THREADFORGE_CI_STATE_PATH)")
	minEvents := fs.Int("min-events", 0, "override the minimum matching harness event count saved by ci start")
	requireTelemetry := fs.Bool("require-telemetry", false, "override the telemetry requirement saved by ci start")
	jsonOutput := fs.Bool("json", false, "print the finish result as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*statePath) == "" || *minEvents < 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge ci finish --state-path <file> [--min-events n] [--require-telemetry=true|false] [--json]")
		return 2
	}
	var required *bool
	fs.Visit(func(item *flag.Flag) {
		if item.Name == "require-telemetry" {
			required = requireTelemetry
		}
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := ci.FinishSession(ctx, *statePath, *minEvents, required)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			_, _ = fmt.Fprintln(stderr, "could not write CI session finish result")
			return 1
		}
	} else {
		printCIValidation(stdout, result.Validation)
		_, _ = fmt.Fprintf(stdout, "CI session %s; receiver stopped: %t; artifacts: %s\n", result.Status, result.Stopped, result.ArtifactDir)
	}
	if result.Status != "passed" {
		return 1
	}
	return 0
}

func runCIDaemon(args []string, stderr io.Writer) int {
	if os.Getenv("THREADFORGE_CI_DAEMON_MODE") != "1" {
		_, _ = fmt.Fprintln(stderr, "ci daemon is an internal command")
		return 2
	}
	fs := flag.NewFlagSet("ci daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	statePath := fs.String("state-path", "", "private CI session-state file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*statePath) == "" {
		_, _ = fmt.Fprintln(stderr, "ci daemon requires --state-path")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := ci.RunDaemon(ctx, *statePath); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runCIExec(args []string, stdout, stderr io.Writer) int {
	flagArgs, childArgs, separated := splitCICommand(args)
	if !separated {
		_, _ = fmt.Fprintln(stderr, "ci exec requires -- before the child command\nusage: threadforge ci exec [--harness claude-code|codex-cli] [--output-dir <dir>] [--work-dir <dir>] [--min-events n] [--require-telemetry=false] [--json] -- <command> [args...]")
		return 2
	}
	fs := flag.NewFlagSet("ci exec", flag.ContinueOnError)
	fs.SetOutput(stderr)
	harness := fs.String("harness", "claude-code", "CI harness to configure (claude-code or codex-cli)")
	outputDir := fs.String("output-dir", "", "existing output base directory (defaults under RUNNER_TEMP or the system temp directory)")
	workingDir := fs.String("work-dir", "", "child working directory (defaults to the current directory)")
	minEvents := fs.Int("min-events", 1, "minimum matching harness events required for telemetry validation")
	requireTelemetry := fs.Bool("require-telemetry", true, "fail when telemetry validation does not pass")
	jsonOutput := fs.Bool("json", false, "print the final result as JSON; child stdout is sent to stderr")
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "ci exec options must appear before --")
		return 2
	}
	if len(childArgs) == 0 || strings.TrimSpace(childArgs[0]) == "" {
		_, _ = fmt.Fprintln(stderr, "ci exec requires a child command after --")
		return 2
	}
	if *minEvents < 1 {
		_, _ = fmt.Fprintln(stderr, "min-events must be at least 1")
		return 2
	}
	childStdout := stdout
	if *jsonOutput {
		childStdout = stderr
	}
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := ci.Execute(runCtx, ci.ExecOptions{
		Harness: *harness, OutputDir: *outputDir, WorkingDir: *workingDir,
		MinEvents: *minEvents, RequireTelemetry: *requireTelemetry,
	}, childArgs, childStdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			_, _ = fmt.Fprintln(stderr, "could not write CI result JSON")
			return 1
		}
	} else {
		printCIValidation(stderr, result.Validation)
		_, _ = fmt.Fprintf(stderr, "CI artifacts: %s\n", result.ArtifactDir)
	}
	if result.ChildExitCode != 0 {
		if result.ChildExitCode < 1 || result.ChildExitCode > 255 {
			return 1
		}
		return result.ChildExitCode
	}
	if *requireTelemetry && result.Validation.Status != "passed" {
		return 1
	}
	return 0
}

func splitCICommand(args []string) (flagArgs, commandArgs []string, found bool) {
	for index, arg := range args {
		if arg == "--" {
			return args[:index], args[index+1:], true
		}
	}
	return args, nil, false
}

func runCIValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	logPath := fs.String("log-path", "", "runtime.jsonl produced by a local CI capture")
	harness := fs.String("harness", "claude-code", "required harness (claude-code, codex-cli, or all)")
	minEvents := fs.Int("min-events", 1, "minimum matching harness events required")
	jsonOutput := fs.Bool("json", false, "print validation result as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*logPath) == "" || *minEvents < 1 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge ci validate --log-path <runtime.jsonl> [--harness claude-code|codex-cli|all] [--min-events n] [--json]")
		return 2
	}
	result, err := ci.ValidateLog(ci.ValidationOptions{LogPath: *logPath, Harness: *harness, MinEvents: *minEvents})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			_, _ = fmt.Fprintln(stderr, "could not write CI validation JSON")
			return 1
		}
	} else {
		printCIValidation(stdout, result)
	}
	if result.Status != "passed" {
		return 1
	}
	return 0
}

func runCISecurityGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	logPath := fs.String("log-path", "", "local runtime.jsonl produced by a CI capture")
	harness := fs.String("harness", "all", "required harness (claude-code, codex-cli, or all)")
	minEvents := fs.Int("min-events", 1, "minimum matching harness events required")
	failOn := fs.String("fail-on", "", "fail the CI gate for findings at or above this severity")
	rulesDirectory := fs.String("rules-dir", "", "explicit threat-rule directory for this scan")
	rulesStore := fs.String("rules-store", "", "local installed threat-rule directory")
	jsonOutput := fs.Bool("json", false, "print the versioned aggregate gate result as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*logPath) == "" || *minEvents < 1 || !validSeverityName(beacon.Severity(strings.ToLower(strings.TrimSpace(*failOn)))) {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge ci gate --log-path <runtime.jsonl> --fail-on <info|low|medium|high|critical> [--harness claude-code|codex-cli|all] [--min-events n] [--rules-dir <path>] [--rules-store <path>] [--json]")
		return 2
	}
	paths, err := appdata.Resolve("", *logPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "CI gate log path is unavailable")
		return 1
	}
	storeDir := strings.TrimSpace(*rulesStore)
	if storeDir == "" {
		storeDir = filepath.Join(paths.Root, "rules")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := ci.EvaluateSecurityGate(ctx, ci.SecurityGateOptions{
		Validation:    ci.ValidationOptions{LogPath: *logPath, Harness: *harness, MinEvents: *minEvents},
		RulesStoreDir: storeDir, RulesDir: strings.TrimSpace(*rulesDirectory),
		FailOn: beacon.Severity(strings.ToLower(strings.TrimSpace(*failOn))),
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "CI security gate could not complete; review local runner diagnostics")
		return 1
	}
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			_, _ = fmt.Fprintln(stderr, "could not write CI security gate JSON")
			return 1
		}
	} else {
		printCIValidation(stdout, result.Validation)
		_, _ = fmt.Fprintf(stdout, "Threadforge CI security gate: %s; findings=%d; fail-on=%s; threshold matched=%t\n",
			result.Status, result.Security.FindingCount, result.Security.FailOn, result.Security.ThresholdMatched)
	}
	if !result.Security.Complete {
		return 2
	}
	if result.Security.ThresholdMatched {
		return 3
	}
	if result.Validation.Status != "passed" {
		return 1
	}
	return 0
}

func printCIValidation(output io.Writer, result ci.ValidationResult) {
	_, _ = fmt.Fprintf(output, "Threadforge CI telemetry validation: %s\n", result.Status)
	_, _ = fmt.Fprintf(output, "Harness events: %d (minimum %d); total valid events: %d; malformed rows: %d\n",
		result.MatchedEvents, result.MinEvents, result.TotalEvents, result.MalformedRows)
}
