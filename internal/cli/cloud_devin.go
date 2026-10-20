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

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/devincloud"
)

func runCloud(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "devin" {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge cloud devin <pull|status>")
		return 2
	}
	if len(args) < 2 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge cloud devin <pull|status>")
		return 2
	}
	switch args[1] {
	case "pull":
		return runCloudDevinPull(args[2:], stdout, stderr)
	case "status":
		return runCloudDevinStatus(args[2:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown Devin cloud command %q\nusage: threadforge cloud devin <pull|status>\n", args[1])
		return 2
	}
}

func runCloudDevinPull(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cloud devin pull", flag.ContinueOnError)
	fs.SetOutput(stderr)
	printOnly := fs.Bool("print", false, "print mapped events without storing events or cursor state")
	includeContent := fs.Bool("include-content", false, "retain redacted prompt/response text locally (explicit privacy opt-in)")
	forceRefresh := fs.Bool("full-resync", false, "fetch all sessions again; event IDs still prevent ordinary re-emission")
	watch := fs.Bool("watch", false, "repeat read-only API sweeps until interrupted")
	interval := fs.Duration("interval", 30*time.Second, "watch interval (5s-24h)")
	maxSessions := fs.Int("max-sessions", 1000, "maximum sessions per sweep (1-5000)")
	maxMessages := fs.Int("max-messages", 10000, "maximum messages per session (1-50000)")
	stateOverride := fs.String("state-path", "", "absolute path for the private hashed cursor file")
	traceOverride := fs.String("trace-path", "", "absolute path for the local trace JSONL file")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || (*printOnly && *watch) || *interval < 5*time.Second || *interval > 24*time.Hour ||
		*maxSessions < 1 || *maxSessions > 5000 || *maxMessages < 1 || *maxMessages > 50000 {
		_, _ = fmt.Fprintln(stderr, "invalid Devin pull options; see `threadforge cloud devin pull --help`")
		return 2
	}
	apiKey := strings.TrimSpace(os.Getenv("DEVIN_API_KEY"))
	orgID := strings.TrimSpace(os.Getenv("DEVIN_ORG_ID"))
	if apiKey == "" || orgID == "" {
		_, _ = fmt.Fprintln(stderr, "set DEVIN_API_KEY and DEVIN_ORG_ID to reauthorize this local read-only connector; credentials are never migrated or written to project files")
		return 1
	}
	if *traceOverride != "" && !filepath.IsAbs(*traceOverride) {
		_, _ = fmt.Fprintln(stderr, "--trace-path must be absolute")
		return 2
	}
	if *stateOverride != "" && !filepath.IsAbs(*stateOverride) {
		_, _ = fmt.Fprintln(stderr, "--state-path must be absolute")
		return 2
	}
	paths, err := appdata.Resolve("", *traceOverride)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	tracePath, err := filepath.Abs(paths.Traces)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "unable to resolve local trace path")
		return 1
	}
	statePath, tracePath, err := devincloud.ResolvePaths(paths.Root, *stateOverride, tracePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if *includeContent {
		_, _ = fmt.Fprintln(stderr, "content capture is enabled: redacted prompt and response text will be retained locally")
	}
	client := devincloud.NewClient(orgID, apiKey)
	options := devincloud.PullOptions{
		Print: *printOnly, IncludeContent: *includeContent, ForceRefresh: *forceRefresh,
		MaxSessions: *maxSessions, MaxMessages: *maxMessages,
		StatePath: statePath, TracePath: tracePath, Out: stdout,
	}
	ctx := context.Background()
	stop := func() {}
	if *watch {
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	}
	defer stop()
	for {
		summary, pullErr := devincloud.PullOnce(ctx, client, options)
		if *printOnly {
			_ = json.NewEncoder(stderr).Encode(summary)
		} else {
			if err := json.NewEncoder(stdout).Encode(summary); err != nil {
				_, _ = fmt.Fprintln(stderr, "unable to write Devin pull summary")
				return 1
			}
		}
		if pullErr != nil {
			if *watch && ctx.Err() != nil {
				return 0
			}
			_, _ = fmt.Fprintf(stderr, "Devin pull incomplete: %v\n", pullErr)
			if !*watch {
				return 1
			}
		}
		if !*watch {
			return 0
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0
		case <-timer.C:
		}
	}
}

func runCloudDevinStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cloud devin status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateOverride := fs.String("state-path", "", "absolute path for the private hashed cursor file")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || (*stateOverride != "" && !filepath.IsAbs(*stateOverride)) {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge cloud devin status [--state-path <absolute-path>]")
		return 2
	}
	paths, err := appdata.Resolve("", "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	root, err := filepath.Abs(paths.Root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "unable to resolve Threadforge data directory")
		return 1
	}
	statePath, _, err := devincloud.ResolvePaths(root, *stateOverride, filepath.Join(root, "traces", "events.jsonl"))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	counts, err := devincloud.StateStatus(statePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	output := struct {
		SchemaVersion string         `json:"schema_version"`
		State         map[string]int `json:"state"`
	}{SchemaVersion: "threadforge.devin-cloud-status.v1", State: counts}
	if err := json.NewEncoder(stdout).Encode(output); err != nil {
		_, _ = fmt.Fprintln(stderr, "unable to write Devin status")
		return 1
	}
	return 0
}
