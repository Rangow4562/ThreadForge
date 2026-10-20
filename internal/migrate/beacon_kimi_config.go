package migrate

import (
	"encoding/json"
	"errors"
)

var errBeaconKimiCodeConfig = errors.New("Beacon Kimi Code config must contain a supported hooks array")

var beaconKimiHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"PermissionRequest",
	"PermissionResult",
	"Stop",
	"SessionEnd",
	"SubagentStart",
	"SubagentStop",
	"PreCompact",
	"PostCompact",
}

func normalizeBeaconKimiCodeConfig(root map[string]any, requireBeaconMarker bool) (json.RawMessage, error) {
	var entries []any
	if rawHooks, exists := root["hooks"]; exists {
		switch hooks := rawHooks.(type) {
		case []any:
			entries = hooks
		case []map[string]any:
			entries = make([]any, len(hooks))
			for i := range hooks {
				entries[i] = hooks[i]
			}
		default:
			return nil, errBeaconKimiCodeConfig
		}
	}
	allowed := make(map[string]struct{}, len(beaconKimiHookEvents))
	for _, event := range beaconKimiHookEvents {
		allowed[event] = struct{}{}
	}
	matched := make(map[string]struct{}, len(beaconKimiHookEvents))
	beaconMarkerSeen := false
	for _, value := range entries {
		hook, ok := value.(map[string]any)
		if !ok {
			return nil, errBeaconKimiCodeConfig
		}
		command, ok := hook["command"].(string)
		if !ok || !isBeaconHookCommand(command, "kimi") {
			continue
		}
		beaconMarkerSeen = true
		event, ok := hook["event"].(string)
		if !ok {
			return nil, errBeaconKimiCodeConfig
		}
		if _, ok := allowed[event]; !ok {
			return nil, errBeaconKimiCodeConfig
		}
		if timeout, exists := hook["timeout"]; exists {
			if _, valid := boundedConfigInt(timeout, 1, 600); !valid {
				return nil, errBeaconKimiCodeConfig
			}
		}
		if _, duplicate := matched[event]; duplicate {
			return nil, errBeaconKimiCodeConfig
		}
		matched[event] = struct{}{}
	}
	if requireBeaconMarker && !beaconMarkerSeen {
		return nil, errBeaconKimiCodeConfig
	}
	hookEvents := make([]string, 0, len(matched))
	for _, event := range beaconKimiHookEvents {
		if _, exists := matched[event]; exists {
			hookEvents = append(hookEvents, event)
		}
	}
	payload := map[string]any{
		"schema_version":          beaconClaudeConfigSchema,
		"source_format":           "toml",
		"kind":                    "host_hook_configuration",
		"harness":                 "kimi_code",
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
