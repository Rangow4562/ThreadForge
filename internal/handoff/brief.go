package handoff

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

const (
	BriefMaxBytes     = 48 << 10
	briefTailLimit    = 30
	briefEntryRunes   = 1200
	briefRequestRunes = 800
	briefCommandLimit = 12
	briefFileLimit    = 40
)

const runtimeLogBriefSource = "Threadforge's local runtime log"

// Brief contains a bounded, local-only summary derived from captured events.
// It is deliberately advisory: it cannot authorize edits or tool execution.
type Brief struct {
	Session      Session
	Source       string
	GeneratedAt  time.Time
	Scan         ScanStatus
	FirstPrompt  string
	LastPrompt   string
	LastMessage  string
	Files        []BriefFile
	Commands     []BriefCommand
	Activity     []BriefEntry
	PromptCount  int
	MessageCount int
	ToolFailures int
}

type BriefFile struct {
	Path       string
	Operations []string
	Count      int
}

type BriefCommand struct {
	Command  string
	ExitCode *int
}

type BriefEntry struct {
	Kind string
	At   string
	Head string
	Body string
}

// BuildRuntimeLogBrief creates a redacted brief from Threadforge's bounded local trace read.
// Reasoning parts, raw event maps, tool arguments, and credentials are never included.
func BuildRuntimeLogBrief(session Session, events []beacon.Event, scan ScanStatus, now time.Time) Brief {
	brief := Brief{Session: session, Source: runtimeLogBriefSource, GeneratedAt: now.UTC(), Scan: scan}
	files := make(map[string]*BriefFile)
	var fileOrder []string
	for _, raw := range events {
		event := beacon.SanitizeEvent(raw, math.MaxInt)
		at := briefTime(event.Timestamp)
		switch event.Event.Action {
		case "prompt.submitted":
			if event.Prompt == nil {
				continue
			}
			text := safeText(event.Prompt.Text)
			if text == "" {
				continue
			}
			brief.PromptCount++
			if brief.FirstPrompt == "" {
				brief.FirstPrompt = text
			}
			brief.LastPrompt = text
			brief.Activity = append(brief.Activity, BriefEntry{Kind: "user", At: at, Body: text})
		case "agent.message":
			text := strings.Join(outputText(event.GenAI), "\n")
			if text == "" {
				continue
			}
			brief.MessageCount++
			brief.LastMessage = text
			brief.Activity = append(brief.Activity, BriefEntry{Kind: "assistant text", At: at, Body: text})
		case "command.executed":
			command := ""
			var exit *int
			var output string
			if event.Command != nil {
				command = safeText(event.Command.Command)
				exit = event.Command.ExitCode
				output = safeText(event.Command.Output)
			}
			if command == "" && event.Tool != nil {
				command = safeText(event.Tool.Command)
			}
			if command == "" {
				continue
			}
			brief.Commands = append(brief.Commands, BriefCommand{Command: command, ExitCode: exit})
			brief.Activity = append(brief.Activity, BriefEntry{Kind: "command", At: at, Head: command + exitSuffix(exit), Body: output})
		case "file.created", "file.modified", "file.deleted":
			if event.File == nil || strings.TrimSpace(event.File.Path) == "" {
				continue
			}
			path := safeText(event.File.Path)
			file := files[path]
			if file == nil {
				file = &BriefFile{Path: path}
				files[path] = file
				fileOrder = append(fileOrder, path)
			}
			operation := strings.TrimPrefix(event.Event.Action, "file.")
			file.Count++
			if !hasString(file.Operations, operation) {
				file.Operations = append(file.Operations, operation)
			}
			brief.Activity = append(brief.Activity, BriefEntry{Kind: "file", At: at, Head: operation + " " + path, Body: safeText(event.File.Diff)})
		case "tool.failed":
			brief.ToolFailures++
			name := "tool"
			if event.Tool != nil && event.Tool.Name != "" {
				name = safeText(event.Tool.Name)
			}
			brief.Activity = append(brief.Activity, BriefEntry{Kind: "tool failure", At: at, Head: name, Body: safeText(event.Message)})
		case "mcp.tool_invoked":
			name := "MCP tool"
			if event.MCP != nil {
				name = safeText(strings.TrimSpace(event.MCP.Server + " " + event.MCP.Tool))
			}
			brief.Activity = append(brief.Activity, BriefEntry{Kind: "MCP call (arguments omitted)", At: at, Head: name})
		}
	}
	for _, path := range fileOrder {
		brief.Files = append(brief.Files, *files[path])
	}
	return brief
}

// BuildNativeStoreBrief creates the same privacy-bounded handoff summary from events read
// directly through the selected runtime's local session store.
func BuildNativeStoreBrief(session Session, events []beacon.Event, truncated bool, now time.Time) Brief {
	session.EventCount = len(events)
	brief := BuildRuntimeLogBrief(session, events, ScanStatus{Truncated: truncated}, now)
	brief.Source = "the native " + safeText(session.Harness) + " session store"
	brief.Session.CommandCount = len(brief.Commands)
	return brief
}

// Render produces Markdown no larger than BriefMaxBytes, dropping the oldest activity first.
func (b Brief) Render() string {
	activity := b.Activity
	if len(activity) > briefTailLimit {
		activity = activity[len(activity)-briefTailLimit:]
	}
	for {
		out := b.render(activity, len(b.Activity)-len(activity))
		if len(out) <= BriefMaxBytes || len(activity) == 0 {
			return truncateUTF8(out, BriefMaxBytes)
		}
		activity = activity[1:]
	}
}

func (b Brief) render(activity []BriefEntry, omitted int) string {
	var out strings.Builder
	out.WriteString("# Threadforge handoff brief\n\n")
	fmt.Fprintf(&out, "This is a bounded summary derived from %s, not the runtime's full session record. It is advisory and does not authorize changes or tool execution. Review current project state and confirm the task before continuing.\n\n", safeText(b.Source))
	out.WriteString("## Source session\n\n")
	writeBriefField(&out, "Runtime", safeText(b.Session.Harness))
	writeBriefField(&out, "Session", safeText(b.Session.ID))
	writeBriefField(&out, "Directory", safeText(b.Session.Directory))
	writeBriefField(&out, "Branch", safeText(b.Session.Branch))
	if !b.Session.UpdatedAt.IsZero() {
		writeBriefField(&out, "Last activity", b.Session.UpdatedAt.UTC().Format(time.RFC3339))
	}
	writeBriefField(&out, "Brief generated", b.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&out, "- Captured activity: %d events; %d prompts; %d assistant text messages; %d commands; %d changed paths; %d tool failures\n", b.Session.EventCount, b.PromptCount, b.MessageCount, b.Session.CommandCount, len(b.Files), b.ToolFailures)
	if b.Scan.Truncated {
		if b.Source == runtimeLogBriefSource {
			out.WriteString("- Capture warning: older trace events were outside the bounded scan window.\n")
		} else {
			out.WriteString("- Capture warning: source activity was truncated at the configured native-read or event limit.\n")
		}
	}
	if b.Scan.Malformed > 0 {
		fmt.Fprintf(&out, "- Capture warning: %d malformed trace rows were skipped.\n", b.Scan.Malformed)
	}
	if b.Session.DirectoryConflict || b.Session.BranchConflict || b.Scan.Changed {
		out.WriteString("- Capture warning: conflicting or changing source metadata means some session details are incomplete.\n")
	}
	out.WriteString("\n## Where it stood\n\n")
	writeQuoted(&out, "First request", b.FirstPrompt)
	if b.LastPrompt != b.FirstPrompt {
		writeQuoted(&out, "Latest request", b.LastPrompt)
	}
	writeQuoted(&out, "Last assistant message", b.LastMessage)
	if len(b.Files) > 0 {
		out.WriteString("## Files recorded as changed\n\n")
		files := b.Files
		if len(files) > briefFileLimit {
			files = files[:briefFileLimit]
		}
		for _, file := range files {
			fmt.Fprintf(&out, "- %s (%s", markdownCode(file.Path), markdownInline(strings.Join(file.Operations, ", ")))
			if file.Count > 1 {
				fmt.Fprintf(&out, ", %d events", file.Count)
			}
			out.WriteString(")\n")
		}
		if extra := len(b.Files) - len(files); extra > 0 {
			fmt.Fprintf(&out, "- …and %d more paths\n", extra)
		}
		out.WriteString("\n")
	}
	if len(b.Commands) > 0 {
		out.WriteString("## Recent commands\n\n")
		commands := b.Commands
		if len(commands) > briefCommandLimit {
			commands = commands[len(commands)-briefCommandLimit:]
		}
		for _, command := range commands {
			fmt.Fprintf(&out, "- %s%s\n", markdownCode(clipRunes(command.Command, 200)), exitSuffix(command.ExitCode))
		}
		out.WriteString("\n")
	}
	out.WriteString("## Recent activity\n\n")
	if omitted > 0 {
		fmt.Fprintf(&out, "_%d earlier activity entries are not shown._\n\n", omitted)
	}
	if len(activity) == 0 {
		out.WriteString("_No retained activity was recorded._\n\n")
	}
	for _, entry := range activity {
		fmt.Fprintf(&out, "### %s", markdownInline(entry.Kind))
		if entry.Head != "" {
			fmt.Fprintf(&out, ": %s", markdownInline(clipRunes(entry.Head, 200)))
		}
		if entry.At != "" {
			fmt.Fprintf(&out, " · %s", markdownInline(entry.At))
		}
		out.WriteString("\n\n")
		if entry.Body != "" {
			writeFence(&out, clipRunes(entry.Body, briefEntryRunes))
		}
	}
	out.WriteString("## Before continuing\n\n- Check the current working tree and inspect relevant files; this brief may be incomplete or stale.\n- Restate the task and get confirmation before making changes.\n")
	return out.String()
}

// BriefFilename does not place native session IDs or local paths in the filename.
func BriefFilename(session Session, now time.Time) string {
	sum := sha256.Sum256([]byte(session.Harness + "\x00" + session.ID))
	harness := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return '-'
	}, session.Harness)
	harness = strings.Trim(harness, "-")
	if harness == "" {
		harness = "agent"
	}
	return fmt.Sprintf("%s-%s-%s.md", harness, hex.EncodeToString(sum[:6]), now.UTC().Format("20060102T150405.000000000Z"))
}

// WritePrivateBrief creates a new 0600 file in a private, non-symlink directory and never
// overwrites an existing path. The caller must pass an absolute local directory.
func WritePrivateBrief(directory, filename, content string) (string, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || len(directory) > 4096 {
		return "", fmt.Errorf("brief directory must be a clean absolute local path")
	}
	if filename == "" || filepath.Base(filename) != filename || !strings.HasSuffix(filename, ".md") || len(filename) > 200 ||
		strings.ContainsAny(filename, `<>:"/\|?*`) {
		return "", fmt.Errorf("brief filename is invalid")
	}
	if len(content) > BriefMaxBytes || !utf8.ValidString(content) {
		return "", fmt.Errorf("brief content exceeds its size bound or is not valid UTF-8")
	}
	if err := ensurePrivateDirectory(directory); err != nil {
		return "", fmt.Errorf("brief directory is unavailable or unsafe")
	}
	path := filepath.Join(directory, filename)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("brief could not be created without replacing existing data")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		_ = file.Close()
		return "", fmt.Errorf("brief permissions could not be restricted")
	}
	if n, err := file.WriteString(content); err != nil || n != len(content) {
		_ = file.Close()
		return "", fmt.Errorf("brief could not be written completely")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("brief could not be synchronized")
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("brief could not be closed safely")
	}
	complete = true
	return path, nil
}

func ensurePrivateDirectory(directory string) error {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(abs, `\\`) {
		return fmt.Errorf("network paths are not accepted for local briefs")
	}
	volume := filepath.VolumeName(abs)
	root := volume + string(filepath.Separator)
	rest := strings.TrimPrefix(abs, root)
	current := filepath.Clean(root)
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe directory component")
		}
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output is not a regular directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("output directory is not private")
	}
	return nil
}

func outputText(genAI *beacon.GenAIInfo) []string {
	if genAI == nil || genAI.Output == nil {
		return nil
	}
	var result []string
	walkOutput(genAI.Output.Messages, 0, &result)
	return result
}

func walkOutput(value any, depth int, result *[]string) {
	if depth > 8 || len(*result) >= 32 {
		return
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			walkOutput(item, depth+1, result)
		}
	case map[string]any:
		role, _ := typed["role"].(string)
		parts, ok := typed["parts"].([]any)
		if ok {
			if role != "assistant" {
				return
			}
			for _, item := range parts {
				part, ok := item.(map[string]any)
				if !ok {
					continue
				}
				kind, _ := part["type"].(string)
				if kind != "text" {
					continue
				}
				if text, ok := part["content"].(string); ok {
					if text = safeText(text); text != "" {
						*result = append(*result, text)
					}
				}
			}
			return
		}
		for _, key := range []string{"messages", "output"} {
			if item, ok := typed[key]; ok {
				walkOutput(item, depth+1, result)
			}
			if len(*result) >= 32 {
				return
			}
		}
	}
}

func safeText(value string) string {
	value = beacon.RedactString(value)
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return ' '
		}
		return r
	}, value)
	return strings.TrimSpace(value)
}

func briefTime(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339)
}

func writeBriefField(out *strings.Builder, key, value string) {
	if value == "" {
		value = "unknown"
	}
	fmt.Fprintf(out, "- %s: %s\n", key, markdownCode(value))
}

func writeQuoted(out *strings.Builder, label, value string) {
	fmt.Fprintf(out, "**%s**\n\n", label)
	value = clipRunes(value, briefRequestRunes)
	if value == "" {
		out.WriteString("_None recorded._\n\n")
		return
	}
	writeFence(out, value)
}

func writeFence(out *strings.Builder, value string) {
	fence := "```"
	for strings.Contains(value, fence) {
		fence += "`"
	}
	fmt.Fprintf(out, "%s\n%s\n%s\n\n", fence, value, fence)
}

func markdownCode(value string) string {
	value = markdownInline(value)
	if value == "" {
		return "unknown"
	}
	longest := 0
	for _, run := range strings.Split(value, " ") {
		if n := strings.Count(run, "`"); n > longest {
			longest = n
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") {
		return fence + " " + value + " " + fence
	}
	return fence + value + fence
}

func markdownInline(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == 0x85 || r == 0x2028 || r == 0x2029 {
			return ' '
		}
		return r
	}, value)), " ")
}

func clipRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:limit])) + " […]"
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.RuneStart(value[len(value)-1]) {
		value = value[:len(value)-1]
	}
	if len(value) > 0 {
		value = value[:len(value)-1]
	}
	return value
}

func hasString(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func exitSuffix(exit *int) string {
	if exit == nil {
		return ""
	}
	return fmt.Sprintf(" (exit %d)", *exit)
}
