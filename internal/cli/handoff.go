package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/handoffbridge"
	"github.com/threadforge-ai/threadforge/internal/appdata"
	handoffpkg "github.com/threadforge-ai/threadforge/internal/handoff"
)

const handoffExportSchemaVersion = "threadforge.handoff-export.v1"

type handoffExportResult struct {
	SchemaVersion string `json:"schema_version"`
	Source        string `json:"source"`
	Harness       string `json:"harness"`
	SessionID     string `json:"session_id"`
	BriefSHA256   string `json:"brief_sha256"`
	BriefBytes    int    `json:"brief_bytes"`
	OutputPath    string `json:"output_path,omitempty"`
	Warnings      struct {
		OlderEventsOmitted bool `json:"older_events_omitted,omitempty"`
		MalformedRows      int  `json:"malformed_rows,omitempty"`
		DirectoryConflict  bool `json:"directory_conflict,omitempty"`
		BranchConflict     bool `json:"branch_conflict,omitempty"`
	} `json:"warnings,omitempty"`
}

func runHandoff(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge handoff <list|export|resume> ...")
		return 2
	}
	switch args[0] {
	case "list":
		return runHandoffList(args[1:], stdout, stderr)
	case "export":
		return runHandoffExport(args[1:], stdout, stderr)
	case "resume":
		return runHandoffResume(args[1:], stdin, stdout, stderr)
	default:
		_, _ = fmt.Fprintln(stderr, "usage: threadforge handoff <list|export|resume> ...")
		return 2
	}
}

type repeatedFlag []string

func (values *repeatedFlag) String() string { return strings.Join(*values, ",") }
func (values *repeatedFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("value cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

func runHandoffList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("handoff list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	harness := fs.String("harness", "", "filter by native-session runtime")
	directory := fs.String("dir", "", "filter to this absolute workspace and descendants")
	here := fs.Bool("here", false, "filter to the current workspace and descendants")
	includeSubagents := fs.Bool("subagents", false, "include delegated child sessions")
	limit := fs.Int("limit", 25, "maximum sessions (0 lists all; maximum 10000)")
	jsonOutput := fs.Bool("json", false, "write the versioned JSON result")
	var storeOverrides repeatedFlag
	fs.Var(&storeOverrides, "store-dir", "native store override runtime=<absolute-path>; repeatable")
	claudeDir := fs.String("claude-projects-dir", "", "Claude Code projects directory override")
	codexDir := fs.String("codex-dir", "", "Codex home directory override")
	openCodeDir := fs.String("opencode-dir", "", "OpenCode data directory override")
	clineDir := fs.String("cline-dir", "", "Cline data directory override")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	if *here && *directory != "" {
		_, _ = fmt.Fprintln(stderr, "--here and --dir cannot be combined")
		return 2
	}
	if *limit < 0 || *limit > handoffbridge.MaxSessions {
		_, _ = fmt.Fprintf(stderr, "--limit must be 0..%d\n", handoffbridge.MaxSessions)
		return 2
	}
	canonicalHarness := ""
	if *harness != "" {
		var ok bool
		canonicalHarness, ok = canonicalHandoffHarness(*harness)
		if !ok {
			_, _ = fmt.Fprintln(stderr, "unsupported native-session harness")
			return 2
		}
	}
	filterDir := strings.TrimSpace(*directory)
	if *here {
		cwd, err := os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "current directory could not be resolved")
			return 1
		}
		filterDir = cwd
	}
	if filterDir != "" {
		abs, err := filepath.Abs(filterDir)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "directory filter could not be resolved")
			return 2
		}
		filterDir = abs
	}
	dirs := make(map[string]string)
	for _, item := range storeOverrides {
		runtimeName, path, found := strings.Cut(item, "=")
		canonical, ok := canonicalHandoffHarness(runtimeName)
		if !found || !ok || strings.TrimSpace(path) == "" {
			_, _ = fmt.Fprintln(stderr, "--store-dir must be runtime=<absolute-path> for a supported native-store runtime")
			return 2
		}
		if _, duplicate := dirs[canonical]; duplicate {
			_, _ = fmt.Fprintln(stderr, "duplicate --store-dir runtime override")
			return 2
		}
		abs, err := absoluteHandoffPath(path)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "native store overrides must be absolute local paths")
			return 2
		}
		dirs[canonical] = abs
	}
	for _, direct := range []struct{ harness, path string }{
		{"claude_code", *claudeDir}, {"codex_cli", *codexDir}, {"opencode", *openCodeDir}, {"cline", *clineDir},
	} {
		if strings.TrimSpace(direct.path) == "" {
			continue
		}
		if _, exists := dirs[direct.harness]; exists {
			_, _ = fmt.Fprintf(stderr, "direct directory override conflicts with --store-dir for %s\n", direct.harness)
			return 2
		}
		abs, err := absoluteHandoffPath(direct.path)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "native store overrides must be absolute local paths")
			return 2
		}
		dirs[direct.harness] = abs
	}
	response, err := handoffbridge.List(handoffbridge.Request{
		SchemaVersion: handoffbridge.RequestSchemaVersion, Harness: canonicalHarness,
		Directory: filterDir, IncludeSubagents: *includeSubagents, Limit: *limit, StoreDirs: dirs,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "native session discovery request was rejected")
		return 2
	}
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(response); err != nil {
			_, _ = fmt.Fprintln(stderr, "native session results could not be written")
			return 1
		}
	} else {
		if _, err := fmt.Fprintln(stdout, "UPDATED\tHARNESS\tSESSION\tTITLE\tDIRECTORY"); err != nil {
			return 1
		}
		for _, session := range response.Sessions {
			updated := "unknown"
			if !session.UpdatedAt.IsZero() {
				updated = session.UpdatedAt.UTC().Format(time.RFC3339)
			}
			if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", updated, safeHandoffCell(session.Harness, 64), safeHandoffCell(session.ID, 256), safeHandoffCell(session.Title, 160), safeHandoffCell(session.Directory, 512)); err != nil {
				return 1
			}
		}
		for _, warning := range response.Warnings {
			if warning.Code == "scan_truncated" {
				_, _ = fmt.Fprintf(stderr, "warning: %s session results truncated by local scan limits\n", safeHandoffCell(warning.Harness, 64))
				continue
			}
			_, _ = fmt.Fprintf(stderr, "warning: %s session store unavailable\n", safeHandoffCell(warning.Harness, 64))
		}
	}
	return 0
}

func canonicalHandoffHarness(value string) (string, bool) {
	aliases := map[string]string{
		"claude": "claude_code", "claude-code": "claude_code", "claude_code": "claude_code",
		"codex": "codex_cli", "codex-cli": "codex_cli", "codex_cli": "codex_cli",
		"opencode": "opencode", "cline": "cline", "pi": "pi_cli", "pi-cli": "pi_cli", "pi_cli": "pi_cli",
		"prime": "prime_agent", "prime-agent": "prime_agent", "prime_agent": "prime_agent", "factory": "factory", "droid": "factory", "factory-droid": "factory",
		"copilot": "copilot_cli", "copilot-cli": "copilot_cli", "copilot_cli": "copilot_cli",
		"hermes": "hermes", "hermes-agent": "hermes", "dsh": "deepseek_harness", "deepseek": "deepseek_harness", "deepseek-harness": "deepseek_harness", "deepseek_harness": "deepseek_harness",
		"fx": "vercel_fx", "vercel-fx": "vercel_fx", "vercel_fx": "vercel_fx", "grok": "grok", "grok-build": "grok", "openclaw": "openclaw_gateway", "openclaw-gateway": "openclaw_gateway",
		"cursor": "cursor",
		"omp":    "omp", "oh-my-pi": "omp", "gemini": "gemini_cli", "gemini-cli": "gemini_cli", "gemini_cli": "gemini_cli",
		"qwen": "qwen_code", "qwen-code": "qwen_code", "qwen_code": "qwen_code", "kiro": "kiro", "kiro-cli": "kiro",
		"agy": "antigravity_cli", "antigravity": "antigravity_cli", "antigravity-cli": "antigravity_cli", "antigravity_cli": "antigravity_cli",
		"devin": "devin-cli", "devin-cli": "devin-cli", "muse": "muse_code", "muse-code": "muse_code", "muse_code": "muse_code",
		"openhands": "openhands", "open-hands": "openhands", "goose": "goose",
	}
	canonical, ok := aliases[strings.ToLower(strings.TrimSpace(value))]
	return canonical, ok
}

func absoluteHandoffPath(path string) (string, error) {
	if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsRune(path, '\x00') ||
		(runtime.GOOS == "windows" && strings.HasPrefix(path, `\\`)) {
		return "", fmt.Errorf("invalid local path")
	}
	return filepath.Clean(path), nil
}

func safeHandoffCell(value string, maxRunes int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > maxRunes {
		value = string(runes[:maxRunes-1]) + "…"
	}
	return value
}

func runHandoffExport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge handoff export <session-id> [--source runtime-log|native] [--harness <name>] [--traces <events.jsonl>] [--store-dir <runtime>=<absolute-path>] [--output-dir <private-dir>] [--print|--json]")
		return 2
	}
	sessionID := args[0]
	fs := flag.NewFlagSet("handoff export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "runtime-log", "brief source: runtime-log or native")
	harness := fs.String("harness", "", "disambiguate sessions from different runtimes")
	tracePath := fs.String("traces", "", "local Threadforge trace JSONL override")
	outputDirectory := fs.String("output-dir", "", "private absolute directory for the new brief")
	printBrief := fs.Bool("print", false, "write the brief to stdout instead of a file")
	jsonOutput := fs.Bool("json", false, "print a path-local metadata receipt without brief content")
	var storeOverrides repeatedFlag
	fs.Var(&storeOverrides, "store-dir", "native store override runtime=<absolute-path>; repeatable")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return 2
	}
	if *source != "runtime-log" && *source != "native" {
		_, _ = fmt.Fprintln(stderr, "--source must be runtime-log or native")
		return 2
	}
	if *printBrief && *jsonOutput {
		_, _ = fmt.Fprintln(stderr, "--print and --json cannot be combined")
		return 2
	}
	if *tracePath != "" && !filepath.IsAbs(*tracePath) {
		_, _ = fmt.Fprintln(stderr, "--traces must be an absolute local file path")
		return 2
	}
	if *source == "native" && *tracePath != "" {
		_, _ = fmt.Fprintln(stderr, "--traces cannot be combined with --source native")
		return 2
	}
	if *source == "runtime-log" && len(storeOverrides) > 0 {
		_, _ = fmt.Fprintln(stderr, "--store-dir requires --source native")
		return 2
	}
	if *outputDirectory != "" && (!filepath.IsAbs(*outputDirectory) || filepath.Clean(*outputDirectory) != *outputDirectory) {
		_, _ = fmt.Fprintln(stderr, "--output-dir must be a clean absolute local directory")
		return 2
	}
	paths, err := appdata.Resolve("", *tracePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "local Threadforge paths could not be resolved")
		return 1
	}
	var session handoffpkg.Session
	var brief handoffpkg.Brief
	var exportSource string
	var scan handoffpkg.ScanStatus
	if *source == "native" {
		canonical, ok := canonicalHandoffHarness(*harness)
		if !ok {
			_, _ = fmt.Fprintln(stderr, "--source native requires --harness for one supported runtime")
			return 2
		}
		dirs := make(map[string]string, len(storeOverrides))
		for _, item := range storeOverrides {
			runtimeName, path, found := strings.Cut(item, "=")
			nativeHarness, supported := canonicalHandoffHarness(runtimeName)
			if !found || !supported || strings.TrimSpace(path) == "" {
				_, _ = fmt.Fprintln(stderr, "--store-dir must be runtime=<absolute-path> for a supported native-store runtime")
				return 2
			}
			if _, duplicate := dirs[nativeHarness]; duplicate {
				_, _ = fmt.Fprintln(stderr, "duplicate --store-dir runtime override")
				return 2
			}
			absolute, err := absoluteHandoffPath(path)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "native store overrides must be absolute local paths")
				return 2
			}
			dirs[nativeHarness] = absolute
		}
		response, err := handoffbridge.Read(handoffbridge.ReadRequest{
			SchemaVersion: handoffbridge.ReadRequestSchemaVersion,
			Harness:       canonical, SessionID: sessionID, StoreDirs: dirs,
		})
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "native session could not be read within local export limits")
			return 1
		}
		session = handoffpkg.Session{
			ID: response.Session.ID, Harness: response.Session.Harness, Directory: response.Session.Directory,
			Branch: response.Session.Branch, UpdatedAt: response.Session.UpdatedAt,
			EventCount: len(response.Events),
		}
		brief = handoffpkg.BuildNativeStoreBrief(session, response.Events, response.Truncated, time.Now())
		scan.Truncated = response.Truncated
		exportSource = "native_session_store"
	} else {
		traceSession, events, traceScan, readErr := handoffpkg.ReadSession(paths.Traces, sessionID, *harness)
		if readErr != nil {
			_, _ = fmt.Fprintln(stderr, readErr)
			return 1
		}
		session = traceSession
		if traceScan.Changed {
			_, _ = fmt.Fprintln(stderr, "local trace changed while it was being read; no handoff brief was created")
			return 1
		}
		scan = traceScan
		brief = handoffpkg.BuildRuntimeLogBrief(session, events, scan, time.Now())
		exportSource = "threadforge_runtime_log"
	}
	content := brief.Render()
	digest := sha256.Sum256([]byte(content))
	result := handoffExportResult{
		SchemaVersion: handoffExportSchemaVersion,
		Source:        exportSource,
		Harness:       session.Harness,
		SessionID:     session.ID,
		BriefSHA256:   hex.EncodeToString(digest[:]),
		BriefBytes:    len(content),
	}
	result.Warnings.OlderEventsOmitted = scan.Truncated
	result.Warnings.MalformedRows = scan.Malformed
	result.Warnings.DirectoryConflict = session.DirectoryConflict
	result.Warnings.BranchConflict = session.BranchConflict
	if *printBrief {
		if _, err := io.WriteString(stdout, content); err != nil {
			_, _ = fmt.Fprintln(stderr, "handoff brief could not be written to stdout")
			return 1
		}
		return 0
	}
	if *outputDirectory == "" {
		*outputDirectory = filepath.Join(paths.Root, "handoffs")
	}
	filename := handoffpkg.BriefFilename(session, brief.GeneratedAt)
	path, err := handoffpkg.WritePrivateBrief(*outputDirectory, filename, content)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	result.OutputPath = path
	if !*jsonOutput {
		_, err = fmt.Fprintln(stdout, path)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "handoff result could not be written")
			return 1
		}
		return 0
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		_, _ = fmt.Fprintln(stderr, "handoff result could not be written")
		return 1
	}
	return 0
}
