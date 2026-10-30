package migrate

import (
	"encoding/json"
	"sort"
	"strings"
)

const (
	maxBeaconDevinDesktopHooksBytes = 1 << 20
	maxBeaconDevinDesktopHookRefs   = 4096
)

var beaconDevinDesktopActions = map[string]string{
	"pre_user_prompt":   "prompt-submit",
	"post_write_code":   "post-tool",
	"post_run_command":  "post-tool",
	"post_mcp_tool_use": "post-tool",
	"post_read_code":    "post-tool",
}

// normalizeBeaconDevinDesktopHooks projects only the five event names emitted
// by the pinned Beacon Cascade adapter. Hook commands and all other settings
// are deliberately discarded.
func normalizeBeaconDevinDesktopHooks(root map[string]any) (json.RawMessage, error) {
	hookEvents := make([]string, 0, len(beaconDevinDesktopActions))
	rawHooks, exists := root["hooks"]
	if exists {
		hooks, ok := rawHooks.(map[string]any)
		if !ok {
			return nil, errBeaconDevinHooks
		}
		seenRefs := 0
		for eventName, rawRefs := range hooks {
			refs, ok := rawRefs.([]any)
			if !ok {
				return nil, errBeaconDevinHooks
			}
			seenRefs += len(refs)
			if seenRefs > maxBeaconDevinDesktopHookRefs {
				return nil, errBeaconDevinHooks
			}
			action := beaconDevinDesktopActions[eventName]
			for _, rawRef := range refs {
				ref, ok := rawRef.(map[string]any)
				if !ok {
					return nil, errBeaconDevinHooks
				}
				command := ""
				if rawCommand, exists := ref["command"]; exists && rawCommand != nil {
					var valid bool
					command, valid = rawCommand.(string)
					if !valid {
						return nil, errBeaconDevinHooks
					}
				}
				if showOutput, exists := ref["show_output"]; exists && showOutput != nil {
					if _, ok := showOutput.(bool); !ok {
						return nil, errBeaconDevinHooks
					}
				}
				if action != "" && isBeaconDevinDesktopCommand(command, action) {
					hookEvents = append(hookEvents, eventName)
					break
				}
			}
		}
	}
	sort.Strings(hookEvents)
	payload := map[string]any{
		"schema_version":          beaconClaudeConfigSchema,
		"source_format":           "json",
		"kind":                    "host_hook_configuration",
		"harness":                 "devin_desktop",
		"beacon_hooks_configured": len(hookEvents) > 0,
		"hook_events":             hookEvents,
		"omitted_fields": []string{
			"credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
		},
		"human_review_required": true,
		"execution_authorized":  false,
	}
	data, err := json.Marshal(payload)
	return data, err
}

// isBeaconDevinDesktopCommand recognizes only the current pinned emitter's
// direct command form. It never evaluates or executes shell text.
func isBeaconDevinDesktopCommand(command, action string) bool {
	fields, ok := splitBeaconCommandFields(command)
	if !ok || len(fields) != 8 {
		return false
	}
	binary := strings.ReplaceAll(fields[0], `\`, "/")
	binary = binary[strings.LastIndex(binary, "/")+1:]
	if !strings.EqualFold(binary, "beacon-hooks") && !strings.EqualFold(binary, "beacon-hooks.exe") {
		return false
	}
	return fields[1] == "--platform" && fields[2] == "devin-desktop" &&
		fields[3] == "--log" && fields[4] != "" &&
		fields[5] == "--config" && fields[6] != "" && fields[7] == action
}

// splitBeaconCommandFields tokenizes only enough POSIX single-quoted argv
// syntax to compare against the pinned emitter contract; shell operators,
// expansion syntax, double quotes, and line breaks are rejected.
func splitBeaconCommandFields(command string) ([]string, bool) {
	if len(command) == 0 || len(command) > 8192 {
		return nil, false
	}
	fields := make([]string, 0, 8)
	var field strings.Builder
	inSingleQuote := false
	started := false
	flush := func() {
		if started {
			fields = append(fields, field.String())
			field.Reset()
			started = false
		}
	}
	for index := 0; index < len(command); index++ {
		char := command[index]
		if char == 0 || char == '\r' || char == '\n' {
			return nil, false
		}
		if inSingleQuote {
			if char == '\'' {
				inSingleQuote = false
				continue
			}
			field.WriteByte(char)
			started = true
			continue
		}
		switch char {
		case '\'', ' ':
			if char == '\'' {
				inSingleQuote = true
				started = true
			} else {
				flush()
			}
		case '\t':
			flush()
		case '\\':
			if index+1 >= len(command) || command[index+1] != '\'' {
				return nil, false
			}
			field.WriteByte('\'')
			started = true
			index++
		case '"', ';', '|', '&', '<', '>', '$', '`', '(', ')', '{', '}', '*', '?', '!':
			return nil, false
		default:
			field.WriteByte(char)
			started = true
		}
	}
	if inSingleQuote {
		return nil, false
	}
	flush()
	return fields, true
}
