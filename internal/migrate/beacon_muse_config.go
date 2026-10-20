package migrate

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
)

const (
	beaconMuseHooksMarker = "beacon-managed-muse-hooks:v1"
	beaconMuseHooksFile   = "beacon-endpoint-hooks.json"
	beaconMuseSettingsV1  = "threadforge.beacon-muse-settings.v1"
)

var (
	errBeaconMuseHooks    = errors.New("Beacon Muse managed hooks must use the documented event-group shape")
	errBeaconMuseSettings = errors.New("Beacon Muse settings must use the documented managed_hooks_path value")
	beaconMuseHookEvents  = []string{
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse",
		"PreCompact", "PostCompact", "SubagentStart", "SubagentStop", "Stop",
	}
)

func normalizeBeaconMuseManagedHooks(root map[string]any) (json.RawMessage, error) {
	if root["beacon"] != beaconMuseHooksMarker {
		return nil, errBeaconMuseHooks
	}
	if version, exists := root["schema_version"]; exists {
		if value, valid := boundedConfigInt(version, 1, 1); !valid || value != 1 {
			return nil, errBeaconMuseHooks
		}
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return nil, errBeaconMuseHooks
	}
	allowed := make(map[string]struct{}, len(beaconMuseHookEvents))
	for _, event := range beaconMuseHookEvents {
		allowed[event] = struct{}{}
	}
	for event, groups := range hooks {
		if _, ok := allowed[event]; !ok && beaconHookGroupContainsPlatform(groups, "muse", 0) {
			return nil, errBeaconMuseHooks
		}
	}
	configured := make(map[string]struct{}, len(beaconMuseHookEvents))
	for _, event := range beaconMuseHookEvents {
		rawGroups, exists := hooks[event]
		if !exists {
			continue
		}
		groups, ok := rawGroups.([]any)
		if !ok {
			return nil, errBeaconMuseHooks
		}
		for _, rawGroup := range groups {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				return nil, errBeaconMuseHooks
			}
			for key := range group {
				if key != "matcher" && key != "hooks" {
					return nil, errBeaconMuseHooks
				}
			}
			if matcher, exists := group["matcher"]; exists {
				if _, ok := matcher.(string); !ok {
					return nil, errBeaconMuseHooks
				}
			}
			handlers, ok := group["hooks"].([]any)
			if !ok {
				return nil, errBeaconMuseHooks
			}
			for _, rawHandler := range handlers {
				handler, ok := rawHandler.(map[string]any)
				if !ok {
					return nil, errBeaconMuseHooks
				}
				command, ok := handler["command"].(string)
				if !ok || !isBeaconHookCommand(command, "muse") {
					continue
				}
				for key := range handler {
					switch key {
					case "type", "command", "timeout", "statusMessage", "commandWindows", "silent", "async":
					default:
						return nil, errBeaconMuseHooks
					}
				}
				if handler["type"] != "command" {
					return nil, errBeaconMuseHooks
				}
				if message, exists := handler["statusMessage"]; exists {
					if _, ok := message.(string); !ok {
						return nil, errBeaconMuseHooks
					}
				}
				configured[event] = struct{}{}
			}
		}
	}
	hookEvents := make([]string, 0, len(configured))
	for _, event := range beaconMuseHookEvents {
		if _, exists := configured[event]; exists {
			hookEvents = append(hookEvents, event)
		}
	}
	payload := map[string]any{
		"schema_version":          beaconClaudeConfigSchema,
		"source_format":           "json",
		"kind":                    "host_hook_configuration",
		"harness":                 "muse_code",
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

func normalizeBeaconMuseSettings(root map[string]any) (json.RawMessage, error) {
	target, exists := root["managed_hooks_path"]
	configured := false
	beaconName := false
	if exists {
		value, ok := target.(string)
		if !ok {
			return nil, errBeaconMuseSettings
		}
		value = strings.TrimSpace(value)
		configured = value != ""
		if configured {
			beaconName = strings.EqualFold(path.Base(strings.ReplaceAll(value, "\\", "/")), beaconMuseHooksFile)
		}
	}
	payload := map[string]any{
		"schema_version":                      beaconMuseSettingsV1,
		"source_format":                       "json",
		"kind":                                "muse_managed_hooks_pointer",
		"harness":                             "muse_code",
		"managed_hooks_path_configured":       configured,
		"target_basename_matches_beacon_file": beaconName,
		"effective_registration_verified":     false,
		"omitted_fields": []string{
			"credentials", "managed_hooks_path", "local_paths", "unrelated_settings",
		},
		"human_review_required": true,
		"execution_authorized":  false,
	}
	data, err := json.Marshal(payload)
	return data, err
}
