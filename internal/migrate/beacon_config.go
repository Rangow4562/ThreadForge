package migrate

import (
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	errBeaconConfigObject   = errors.New("Beacon endpoint configuration must be an object")
	errBeaconClaudeHooks    = errors.New("Beacon Claude settings hooks must use the documented object and event-array shape")
	errBeaconCodexHooks     = errors.New("Beacon Codex hooks must use the documented object and event-array shape")
	errBeaconGeminiConfig   = errors.New("Beacon Gemini settings must contain the documented local OTEL configuration")
	errBeaconAntigravity    = errors.New("Beacon Antigravity hook block must use the documented object and event-array shape")
	errBeaconQwenHooks      = errors.New("Beacon Qwen settings hooks must use the documented object and event-array shape")
	errBeaconFactoryHooks   = errors.New("Beacon Factory settings hooks must use the documented object and event-array shape")
	errBeaconCursorHooks    = errors.New("Beacon Cursor hooks must use the documented object and event-array shape")
	errBeaconCursorVersion  = errors.New("Beacon Cursor hooks version must be 1 or omitted")
	errBeaconOpenHandsHooks = errors.New("Beacon OpenHands hooks must use the documented event-map shape")
	errBeaconKiroHooks      = errors.New("Beacon Kiro hooks must use the documented v1 command-hook shape")
	errBeaconOpenCodePlugin = errors.New("Beacon OpenCode plugin must start with its managed marker")
	errBeaconClinePlugin    = errors.New("Beacon Cline plugin must start with its managed marker")
	configLabelPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

const (
	beaconRuntimeConfigSchema    = "threadforge.beacon-endpoint-config.v1"
	beaconExporterConfigSchema   = "threadforge.beacon-exporter-config.v1"
	beaconClaudeConfigSchema     = "threadforge.beacon-harness-config.v1"
	beaconOpenCodePluginSchema   = "threadforge.beacon-opencode-plugin-config.v1"
	beaconOpenCodePluginMarker   = "// beacon-managed-opencode-plugin:v1"
	maxBeaconOpenCodePluginBytes = 1 << 20
	beaconClinePluginSchema      = "threadforge.beacon-cline-plugin-config.v1"
	beaconClinePluginMarker      = "// beacon-managed-cline-plugin:v1"
	maxBeaconClinePluginBytes    = 1 << 20
)

type beaconOpenCodePluginPresence struct {
	SchemaVersion        string   `json:"schema_version"`
	Kind                 string   `json:"kind"`
	SourceFormat         string   `json:"source_format"`
	Harness              string   `json:"harness"`
	ManagedMarkerPresent bool     `json:"managed_marker_present"`
	OmittedFields        []string `json:"omitted_fields"`
	HumanReviewRequired  bool     `json:"human_review_required"`
	ExecutionAuthorized  bool     `json:"execution_authorized"`
}

type beaconClinePluginPresence struct {
	SchemaVersion        string   `json:"schema_version"`
	Kind                 string   `json:"kind"`
	SourceFormat         string   `json:"source_format"`
	Harness              string   `json:"harness"`
	ManagedMarkerPresent bool     `json:"managed_marker_present"`
	OmittedFields        []string `json:"omitted_fields"`
	HumanReviewRequired  bool     `json:"human_review_required"`
	ExecutionAuthorized  bool     `json:"execution_authorized"`
}

// isBeaconOpenCodePluginPath accepts only Beacon's documented user/project plugin paths or
// their explicitly selected config/plugin roots. A generic beacon.ts file is not a source.
func isBeaconOpenCodePluginPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "beacon.ts") {
		return false
	}
	clean := strings.ToLower(strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relative)), "./"))
	if clean == ".opencode/plugins/beacon.ts" || strings.HasSuffix(clean, "/.opencode/plugins/beacon.ts") ||
		clean == ".config/opencode/plugins/beacon.ts" || strings.HasSuffix(clean, "/.config/opencode/plugins/beacon.ts") ||
		clean == "opencode/plugins/beacon.ts" {
		return true
	}
	if clean == "plugins/beacon.ts" {
		root := strings.ToLower(filepath.Base(filepath.Clean(sourceRoot)))
		return root == ".opencode" || root == "opencode"
	}
	if clean == "beacon.ts" {
		root := strings.ToLower(filepath.Base(filepath.Clean(sourceRoot)))
		parent := strings.ToLower(filepath.Base(filepath.Dir(filepath.Clean(sourceRoot))))
		return root == "plugins" && (parent == ".opencode" || parent == "opencode")
	}
	return false
}

// isBeaconClinePluginPath accepts only Beacon's documented user/project plugin paths or
// their explicitly selected Cline config/plugin roots. A generic beacon.ts file is not a source.
func isBeaconClinePluginPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "beacon.ts") {
		return false
	}
	clean := strings.ToLower(strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relative)), "./"))
	if clean == ".cline/plugins/beacon.ts" || strings.HasSuffix(clean, "/.cline/plugins/beacon.ts") {
		return true
	}
	if clean == "plugins/beacon.ts" {
		root := strings.ToLower(filepath.Base(filepath.Clean(sourceRoot)))
		return root == ".cline"
	}
	if clean == "beacon.ts" {
		root := strings.ToLower(filepath.Base(filepath.Clean(sourceRoot)))
		parent := strings.ToLower(filepath.Base(filepath.Dir(filepath.Clean(sourceRoot))))
		return root == "plugins" && parent == ".cline"
	}
	return false
}

func isBeaconOpenCodePluginRelativePath(relative string) bool {
	clean := strings.ToLower(strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relative)), "./"))
	return clean == ".opencode/plugins/beacon.ts" || strings.HasSuffix(clean, "/.opencode/plugins/beacon.ts") ||
		clean == ".config/opencode/plugins/beacon.ts" || strings.HasSuffix(clean, "/.config/opencode/plugins/beacon.ts") ||
		clean == "opencode/plugins/beacon.ts" || clean == "plugins/beacon.ts" || clean == "beacon.ts"
}

func isBeaconClinePluginRelativePath(relative string) bool {
	clean := strings.ToLower(strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relative)), "./"))
	return clean == ".cline/plugins/beacon.ts" || strings.HasSuffix(clean, "/.cline/plugins/beacon.ts") ||
		clean == "plugins/beacon.ts" || clean == "beacon.ts"
}

// normalizeBeaconOpenCodePlugin records only the first-line ownership marker. It does not
// parse, retain, execute, or authenticate the plugin source.
func normalizeBeaconOpenCodePlugin(data []byte) (json.RawMessage, error) {
	if len(data) > maxBeaconOpenCodePluginBytes || !utf8.Valid(data) {
		return nil, errBeaconOpenCodePlugin
	}
	source := strings.TrimPrefix(string(data), "\uFEFF")
	firstLine := strings.SplitN(source, "\n", 2)[0]
	if strings.TrimSuffix(firstLine, "\r") != beaconOpenCodePluginMarker {
		return nil, errBeaconOpenCodePlugin
	}
	payload, err := json.Marshal(beaconOpenCodePluginPresence{
		SchemaVersion: beaconOpenCodePluginSchema, Kind: "managed_plugin_presence",
		SourceFormat: "typescript_plugin", Harness: "opencode", ManagedMarkerPresent: true,
		OmittedFields:       []string{"plugin_source", "commands", "credentials", "environment", "local_paths", "unrelated_configuration"},
		HumanReviewRequired: true, ExecutionAuthorized: false,
	})
	if err != nil {
		return nil, errors.New("unable to encode Beacon OpenCode plugin presence")
	}
	return payload, nil
}

// normalizeBeaconClinePlugin records only the first-line ownership marker. It does not
// parse, retain, execute, or authenticate the plugin source.
func normalizeBeaconClinePlugin(data []byte) (json.RawMessage, error) {
	if len(data) > maxBeaconClinePluginBytes || !utf8.Valid(data) {
		return nil, errBeaconClinePlugin
	}
	source := strings.TrimPrefix(string(data), "\uFEFF")
	firstLine := strings.SplitN(source, "\n", 2)[0]
	if strings.TrimSuffix(firstLine, "\r") != beaconClinePluginMarker {
		return nil, errBeaconClinePlugin
	}
	payload, err := json.Marshal(beaconClinePluginPresence{
		SchemaVersion: beaconClinePluginSchema, Kind: "managed_plugin_presence",
		SourceFormat: "typescript_plugin", Harness: "cline", ManagedMarkerPresent: true,
		OmittedFields:       []string{"plugin_source", "commands", "credentials", "environment", "local_paths", "unrelated_configuration"},
		HumanReviewRequired: true, ExecutionAuthorized: false,
	})
	if err != nil {
		return nil, errors.New("unable to encode Beacon Cline plugin presence")
	}
	return payload, nil
}

// isBeaconEndpointConfigPath accepts only Beacon's documented endpoint state root or a
// checkpoint made from it. A random repository config.json is not a Beacon migration source.
func isBeaconEndpointConfigPath(relative, sourceRoot string) bool {
	base := strings.ToLower(filepath.Base(relative))
	switch base {
	case "config.json", "config.yaml", "config.yml", "otelcol.yaml", "otelcol.yml":
	default:
		return false
	}
	lower := "/" + strings.ToLower(strings.TrimPrefix(filepath.ToSlash(relative), "./"))
	if strings.Contains(lower, "/.beacon/endpoint/") || strings.Contains(lower, "/beacon/endpoint/") {
		return true
	}
	if filepath.Base(relative) != relative && relative != "." {
		return false
	}
	rootBase := strings.ToLower(filepath.Base(filepath.Clean(sourceRoot)))
	parentBase := strings.ToLower(filepath.Base(filepath.Dir(filepath.Clean(sourceRoot))))
	return rootBase == "endpoint" && (parentBase == "beacon" || parentBase == ".beacon")
}

// isBeaconClaudeSettingsPath accepts only Claude Code's documented user/project
// settings.json paths, or settings.json when the selected source root is .claude.
func isBeaconClaudeSettingsPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "settings.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.claude/settings.json") {
		return true
	}
	return relative == filepath.Base(relative) && strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".claude")
}

// isBeaconQwenSettingsPath accepts only Qwen Code's documented user/project
// settings.json paths, or settings.json when the selected source root is .qwen.
func isBeaconQwenSettingsPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "settings.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.qwen/settings.json") {
		return true
	}
	return relative == filepath.Base(relative) && strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".qwen")
}

// isBeaconFactorySettingsPath accepts only Factory Droid's documented user/project
// settings.json paths, or settings.json when the selected source root is .factory.
func isBeaconFactorySettingsPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "settings.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.factory/settings.json") {
		return true
	}
	return relative == filepath.Base(relative) && strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".factory")
}

// isBeaconCursorHooksPath accepts only Cursor's documented user/project hooks.json
// paths, or hooks.json when the selected source root itself is .cursor.
func isBeaconCursorHooksPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "hooks.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.cursor/hooks.json") {
		return true
	}
	return relative == filepath.Base(relative) && strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".cursor")
}

// isBeaconCodexHooksPath accepts only Codex's documented user/project hooks.json
// paths, or hooks.json when the selected source root itself is .codex.
func isBeaconCodexHooksPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "hooks.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.codex/hooks.json") {
		return true
	}
	return relative == filepath.Base(relative) && strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".codex")
}

// isBeaconCodexOTelConfigPath accepts only Codex's documented config.toml path,
// or config.toml when the selected source root itself is .codex.
func isBeaconCodexOTelConfigPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "config.toml") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.codex/config.toml") {
		return true
	}
	return relative == filepath.Base(relative) && strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".codex")
}

// isBeaconGeminiSettingsPath accepts only Gemini's documented user settings path,
// or settings.json when the selected source root itself is .gemini.
func isBeaconGeminiSettingsPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "settings.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.gemini/settings.json") {
		return true
	}
	return relative == filepath.Base(relative) && strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".gemini")
}

// isBeaconAntigravityHooksPath accepts only Antigravity's user/project hook files,
// including those files when their configuration directory is the selected root.
func isBeaconAntigravityHooksPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "hooks.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.gemini/config/hooks.json") || strings.Contains(lower, "/.agents/hooks.json") {
		return true
	}
	if strings.EqualFold(filepath.ToSlash(filepath.Clean(relative)), "config/hooks.json") &&
		strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".gemini") {
		return true
	}
	if filepath.Base(relative) != relative {
		return false
	}
	rootBase := strings.ToLower(filepath.Base(filepath.Clean(sourceRoot)))
	parentBase := strings.ToLower(filepath.Base(filepath.Dir(filepath.Clean(sourceRoot))))
	return rootBase == ".agents" || (rootBase == "config" && parentBase == ".gemini")
}

// isBeaconOpenHandsHooksPath accepts OpenHands' documented user/project hooks.json locations,
// the selected .openhands root, or hooks.json directly inside an explicitly selected source root.
// The last form is needed for OH_PERSISTENCE_DIR, whose directory name is user-defined; its
// normalizer requires at least one exact Beacon OpenHands command marker.
func isBeaconOpenHandsHooksPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "hooks.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.openhands/hooks.json") {
		return true
	}
	return relative == filepath.Base(relative)
}

// isBeaconKiroHooksPath accepts Beacon's dedicated user/project hook file, including a selected
// .kiro, .kiro/hooks, or custom KIRO_HOME root. Custom-root forms are safe to recognize because
// the basename is Beacon-owned and the normalizer still requires a matching --platform kiro hook.
func isBeaconKiroHooksPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "beacon-endpoint.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.kiro/hooks/beacon-endpoint.json") {
		return true
	}
	if strings.EqualFold(filepath.ToSlash(filepath.Clean(relative)), "hooks/beacon-endpoint.json") &&
		strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".kiro") {
		return true
	}
	if strings.EqualFold(filepath.ToSlash(filepath.Clean(relative)), "hooks/beacon-endpoint.json") {
		return true
	}
	if filepath.Base(relative) != relative {
		return false
	}
	rootBase := strings.ToLower(filepath.Base(filepath.Clean(sourceRoot)))
	parentBase := strings.ToLower(filepath.Base(filepath.Dir(filepath.Clean(sourceRoot))))
	return rootBase == "hooks" && parentBase == ".kiro" || rootBase == ".kiro/hooks" || rootBase != ".kiro"
}

// isBeaconKimiCodeConfigPath accepts the documented ~/.kimi-code/config.toml path, a selected
// .kimi-code root, or config.toml directly inside an explicitly selected KIMI_CODE_HOME root.
// The custom-root normalizer requires a Beacon hook marker because config.toml is a generic name.
func isBeaconKimiCodeConfigPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "config.toml") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.kimi-code/config.toml") {
		return true
	}
	if filepath.Base(relative) != relative {
		return false
	}
	return true
}

func isBeaconKimiCodeCustomRootPath(relative, sourceRoot string) bool {
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.kimi-code/config.toml") {
		return false
	}
	return filepath.Base(relative) == relative && !strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".kimi-code")
}

// isBeaconMuseManagedHooksPath accepts Muse's Beacon-owned managed hook file under the default
// ~/.config/muse tree, an XDG_CONFIG_HOME/muse tree, or a selected muse config directory.
func isBeaconMuseManagedHooksPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "beacon-endpoint-hooks.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.config/muse/beacon-endpoint-hooks.json") ||
		strings.Contains(lower, "/muse/beacon-endpoint-hooks.json") {
		return true
	}
	return relative == filepath.Base(relative) && strings.EqualFold(path.Base(strings.ReplaceAll(sourceRoot, `\`, "/")), "muse")
}

// isBeaconMuseSettingsPath accepts Muse's user settings file only in its Muse config directory.
func isBeaconMuseSettingsPath(relative, sourceRoot string) bool {
	if !strings.EqualFold(filepath.Base(relative), "settings.json") {
		return false
	}
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.config/muse/settings.json") || strings.Contains(lower, "/muse/settings.json") {
		return true
	}
	return relative == filepath.Base(relative) && strings.EqualFold(path.Base(strings.ReplaceAll(sourceRoot, `\`, "/")), "muse")
}

func normalizeBeaconEndpointConfig(relative, sourceRoot string, value any) (json.RawMessage, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return nil, errBeaconConfigObject
	}
	if isBeaconClaudeSettingsPath(relative, sourceRoot) {
		return normalizeBeaconClaudeSettings(root)
	}
	if isBeaconQwenSettingsPath(relative, sourceRoot) {
		return normalizeBeaconQwenSettings(root)
	}
	if isBeaconFactorySettingsPath(relative, sourceRoot) {
		return normalizeBeaconFactorySettings(root)
	}
	if isBeaconCursorHooksPath(relative, sourceRoot) {
		return normalizeBeaconCursorHooks(root)
	}
	if isBeaconCodexHooksPath(relative, sourceRoot) {
		return normalizeBeaconCodexHooks(root)
	}
	if isBeaconCodexOTelConfigPath(relative, sourceRoot) {
		return normalizeBeaconCodexOTelConfig(root)
	}
	if isBeaconKimiCodeConfigPath(relative, sourceRoot) {
		return normalizeBeaconKimiCodeConfig(root, isBeaconKimiCodeCustomRootPath(relative, sourceRoot))
	}
	if isBeaconMuseManagedHooksPath(relative, sourceRoot) {
		return normalizeBeaconMuseManagedHooks(root)
	}
	if isBeaconMuseSettingsPath(relative, sourceRoot) {
		return normalizeBeaconMuseSettings(root)
	}
	if isBeaconGeminiSettingsPath(relative, sourceRoot) {
		return normalizeBeaconGeminiSettings(root)
	}
	if isBeaconAntigravityHooksPath(relative, sourceRoot) {
		return normalizeBeaconAntigravityHooks(root)
	}
	if isBeaconOpenHandsHooksPath(relative, sourceRoot) {
		if relative == filepath.Base(relative) && !strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".openhands") {
			return normalizeBeaconOpenHandsHooksAtExplicitRoot(root)
		}
		return normalizeBeaconOpenHandsHooks(root)
	}
	if isBeaconKiroHooksPath(relative, sourceRoot) {
		return normalizeBeaconKiroHooks(root, isBeaconKiroCustomRootPath(relative, sourceRoot))
	}
	base := strings.ToLower(filepath.Base(relative))
	if base == "otelcol.yaml" || base == "otelcol.yml" || hasMap(root["exporters"]) || hasMap(root["service"]) {
		return normalizeBeaconExporterConfig(root)
	}
	return normalizeBeaconRuntimeConfig(root, filepath.Ext(base))
}

func normalizeBeaconClaudeSettings(root map[string]any) (json.RawMessage, error) {
	return normalizeBeaconHookSettings(root, "claude_code", "claude", []string{
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
		"Stop", "SubagentStart", "SubagentStop", "PermissionRequest", "SessionEnd",
	}, errBeaconClaudeHooks)
}

func normalizeBeaconQwenSettings(root map[string]any) (json.RawMessage, error) {
	return normalizeBeaconHookSettings(root, "qwen_code", "qwen", []string{
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
		"PermissionRequest", "Stop", "SubagentStart", "SubagentStop", "SessionEnd",
	}, errBeaconQwenHooks)
}

func normalizeBeaconFactorySettings(root map[string]any) (json.RawMessage, error) {
	return normalizeBeaconHookSettings(root, "factory", "factory", []string{
		"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd",
	}, errBeaconFactoryHooks)
}

func normalizeBeaconCursorHooks(root map[string]any) (json.RawMessage, error) {
	if version, exists := root["version"]; exists {
		if _, valid := boundedConfigInt(version, 0, 1); !valid {
			return nil, errBeaconCursorVersion
		}
	}
	return normalizeBeaconHookSettings(root, "cursor", "cursor", []string{
		"sessionStart", "beforeSubmitPrompt", "preToolUse", "afterAgentThought", "afterFileEdit",
		"postToolUse", "postToolUseFailure", "stop", "sessionEnd",
	}, errBeaconCursorHooks)
}

func normalizeBeaconCodexHooks(root map[string]any) (json.RawMessage, error) {
	return normalizeBeaconHookSettings(root, "codex_cli", "codex", []string{
		"SessionStart", "Stop",
	}, errBeaconCodexHooks)
}

func normalizeBeaconAntigravityHooks(root map[string]any) (json.RawMessage, error) {
	block, exists := root["beacon-endpoint"]
	if !exists {
		block = map[string]any{}
	}
	return normalizeBeaconHookSettings(map[string]any{"hooks": block}, "antigravity", "antigravity", []string{
		"PreInvocation", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostInvocation", "Stop",
	}, errBeaconAntigravity)
}

func normalizeBeaconOpenHandsHooks(root map[string]any) (json.RawMessage, error) {
	return normalizeBeaconOpenHandsHooksWithMarker(root, false)
}

func normalizeBeaconOpenHandsHooksAtExplicitRoot(root map[string]any) (json.RawMessage, error) {
	return normalizeBeaconOpenHandsHooksWithMarker(root, true)
}

func normalizeBeaconOpenHandsHooksWithMarker(root map[string]any, requireBeaconMarker bool) (json.RawMessage, error) {
	eventsRoot := root
	if wrapper, exists := root["hooks"]; exists {
		var ok bool
		eventsRoot, ok = wrapper.(map[string]any)
		if !ok {
			return nil, errBeaconOpenHandsHooks
		}
	}

	type eventName struct {
		snake  string
		pascal string
	}
	documented := []eventName{
		{snake: "session_start", pascal: "SessionStart"},
		{snake: "user_prompt_submit", pascal: "UserPromptSubmit"},
		{snake: "pre_tool_use", pascal: "PreToolUse"},
		{snake: "post_tool_use", pascal: "PostToolUse"},
		{snake: "stop", pascal: "Stop"},
		{snake: "session_end", pascal: "SessionEnd"},
	}
	allowed := make(map[string]struct{}, len(documented)*2)
	hookEvents := make([]string, 0, len(documented))
	for _, event := range documented {
		allowed[event.snake] = struct{}{}
		allowed[event.pascal] = struct{}{}
		_, hasSnake := eventsRoot[event.snake]
		_, hasPascal := eventsRoot[event.pascal]
		if hasSnake && hasPascal {
			return nil, errBeaconOpenHandsHooks
		}
		key := event.snake
		if hasPascal {
			key = event.pascal
		}
		groups, exists := eventsRoot[key]
		if !exists {
			continue
		}
		groupList, ok := groups.([]any)
		if !ok {
			return nil, errBeaconOpenHandsHooks
		}
		for _, group := range groupList {
			if beaconHookGroupContainsPlatform(group, "openhands", 0) {
				hookEvents = append(hookEvents, key)
				break
			}
		}
	}
	for key := range eventsRoot {
		if _, ok := allowed[key]; !ok {
			return nil, errBeaconOpenHandsHooks
		}
	}
	if requireBeaconMarker && len(hookEvents) == 0 {
		return nil, errBeaconOpenHandsHooks
	}

	payload := map[string]any{
		"schema_version":          beaconClaudeConfigSchema,
		"source_format":           "json",
		"kind":                    "host_hook_configuration",
		"harness":                 "openhands",
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

func isBeaconKiroCustomRootPath(relative, sourceRoot string) bool {
	lower := "/" + strings.ToLower(strings.Trim(filepath.ToSlash(relative), "/"))
	if strings.Contains(lower, "/.kiro/hooks/beacon-endpoint.json") {
		return false
	}
	if strings.EqualFold(filepath.ToSlash(filepath.Clean(relative)), "hooks/beacon-endpoint.json") &&
		strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".kiro") {
		return false
	}
	if filepath.Base(relative) == relative && strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), "hooks") &&
		strings.EqualFold(filepath.Base(filepath.Dir(filepath.Clean(sourceRoot))), ".kiro") {
		return false
	}
	return true
}

func normalizeBeaconKiroHooks(root map[string]any, requireBeaconMarker bool) (json.RawMessage, error) {
	for key := range root {
		if key != "version" && key != "hooks" {
			return nil, errBeaconKiroHooks
		}
	}
	if version, ok := root["version"].(string); !ok || version != "v1" {
		return nil, errBeaconKiroHooks
	}
	rawHooks, ok := root["hooks"].([]any)
	if !ok {
		return nil, errBeaconKiroHooks
	}
	allowed := map[string]struct{}{
		"SessionStart": {}, "UserPromptSubmit": {}, "PreToolUse": {}, "PostToolUse": {}, "Stop": {},
	}
	hookEvents := make([]string, 0, len(allowed))
	seen := make(map[string]struct{}, len(rawHooks))
	beaconMarkerSeen := false
	for _, value := range rawHooks {
		hook, ok := value.(map[string]any)
		if !ok {
			return nil, errBeaconKiroHooks
		}
		trigger, ok := hook["trigger"].(string)
		if !ok {
			return nil, errBeaconKiroHooks
		}
		if _, ok := allowed[trigger]; !ok {
			return nil, errBeaconKiroHooks
		}
		if _, duplicate := seen[trigger]; duplicate {
			return nil, errBeaconKiroHooks
		}
		seen[trigger] = struct{}{}
		action, ok := hook["action"].(map[string]any)
		if !ok || action["type"] != "command" {
			return nil, errBeaconKiroHooks
		}
		command, ok := action["command"].(string)
		if !ok || !isBeaconHookCommand(command, "kiro") {
			return nil, errBeaconKiroHooks
		}
		beaconMarkerSeen = true
		enabled, ok := hook["enabled"].(bool)
		if !ok {
			return nil, errBeaconKiroHooks
		}
		if timeout, exists := hook["timeout"]; exists {
			if _, valid := boundedConfigInt(timeout, 1, 600); !valid {
				return nil, errBeaconKiroHooks
			}
		}
		if enabled {
			hookEvents = append(hookEvents, trigger)
		}
	}
	if requireBeaconMarker && !beaconMarkerSeen {
		return nil, errBeaconKiroHooks
	}
	payload := map[string]any{
		"schema_version":          beaconClaudeConfigSchema,
		"source_format":           "json",
		"kind":                    "host_hook_configuration",
		"harness":                 "kiro",
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

func normalizeBeaconHookSettings(root map[string]any, harness, platform string, events []string, shapeError error) (json.RawMessage, error) {
	hookEvents := make([]string, 0, len(events))
	if rawHooks, exists := root["hooks"]; exists {
		hooks, ok := rawHooks.(map[string]any)
		if !ok {
			return nil, shapeError
		}
		for _, event := range events {
			groups, exists := hooks[event]
			if !exists {
				continue
			}
			groupList, ok := groups.([]any)
			if !ok {
				return nil, shapeError
			}
			for _, group := range groupList {
				if beaconHookGroupContainsPlatform(group, platform, 0) {
					hookEvents = append(hookEvents, event)
					break
				}
			}
		}
	}
	payload := map[string]any{
		"schema_version":          beaconClaudeConfigSchema,
		"source_format":           "json",
		"kind":                    "host_hook_configuration",
		"harness":                 harness,
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

func beaconHookGroupContainsPlatform(value any, platform string, depth int) bool {
	if depth > 12 {
		return false
	}
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if strings.EqualFold(key, "command") {
				if command, ok := child.(string); ok && isBeaconHookCommand(command, platform) {
					return true
				}
			}
			if beaconHookGroupContainsPlatform(child, platform, depth+1) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if beaconHookGroupContainsPlatform(child, platform, depth+1) {
				return true
			}
		}
	}
	return false
}

func isBeaconClaudeHookCommand(command string) bool {
	return isBeaconHookCommand(command, "claude")
}

func isBeaconHookCommand(command, platform string) bool {
	if !strings.Contains(strings.ToLower(command), "beacon-hooks") {
		return false
	}
	fields := strings.Fields(command)
	for index, field := range fields {
		field = strings.Trim(field, "\"'")
		if strings.EqualFold(field, "--platform="+platform) {
			return true
		}
		if strings.EqualFold(field, "--platform") && index+1 < len(fields) && strings.EqualFold(strings.Trim(fields[index+1], "\"'"), platform) {
			return true
		}
	}
	return false
}

func hasMap(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

func normalizeBeaconRuntimeConfig(root map[string]any, extension string) (json.RawMessage, error) {
	payload := map[string]any{
		"schema_version": beaconRuntimeConfigSchema,
		"source_format":  strings.TrimPrefix(extension, "."),
		"kind":           "endpoint_runtime",
	}
	if containsSensitiveField(root, 0) {
		payload["sensitive_values_omitted"] = true
	}
	if value, ok := root["user_mode"].(bool); ok {
		payload["user_mode"] = value
	}
	if collector, ok := root["collector"].(map[string]any); ok {
		projected := map[string]any{}
		for _, key := range []string{"grpc_port", "http_port", "health_port"} {
			if port, valid := boundedConfigInt(collector[key], 1, 65535); valid {
				projected[key] = port
			}
		}
		for _, key := range []string{"include_runtime_metrics", "include_codex_spans"} {
			if value, valid := collector[key].(bool); valid {
				projected[key] = value
			}
		}
		if len(projected) > 0 {
			payload["collector"] = projected
		}
	}
	if values := safeConfigLabels(root["harnesses"]); len(values) > 0 {
		payload["harnesses"] = values
	}
	if values := safeConfigLabels(root["event_categories"]); len(values) > 0 {
		payload["event_categories"] = values
	}
	if inventory, ok := root["inventory_heartbeat"].(map[string]any); ok {
		projected := map[string]any{}
		for _, key := range []string{"enabled", "include_contents"} {
			if value, valid := inventory[key].(bool); valid {
				projected[key] = value
			}
		}
		if value, valid := boundedConfigInt(inventory["max_content_bytes"], 0, 1<<30); valid {
			projected["max_content_bytes"] = value
		}
		if values := safeConfigLabels(inventory["runtimes"]); len(values) > 0 {
			projected["runtimes"] = values
		}
		if len(projected) > 0 {
			payload["inventory"] = projected
		}
	}
	if managed, ok := root["managed_ingest"].(map[string]any); ok {
		projected := map[string]any{}
		if enabled, valid := managed["enabled"].(bool); valid {
			projected["enabled"] = enabled
		}
		if mode := safeConfigLabel(managed["privacy_mode"]); mode != "" {
			projected["privacy_mode"] = mode
		}
		if len(projected) > 0 {
			projected["sensitive_values_omitted"] = true
			payload["managed_ingest"] = projected
		}
	}
	if update, ok := root["auto_update"].(map[string]any); ok {
		if mode := safeConfigLabel(update["mode"]); mode != "" {
			payload["auto_update"] = map[string]any{"mode": mode}
		}
	}
	if destinations, ok := root["destinations"].(map[string]any); ok {
		forwarders := make([]map[string]any, 0, 2)
		for _, name := range []string{"splunk_hec", "falcon_hec"} {
			if destination, exists := destinations[name].(map[string]any); exists {
				forwarders = append(forwarders, projectBeaconDestination(name, destination))
			}
		}
		if len(forwarders) > 0 {
			payload["forwarders"] = forwarders
		}
	}
	data, err := json.Marshal(payload)
	return data, err
}

func projectBeaconDestination(name string, value map[string]any) map[string]any {
	projected := map[string]any{"name": name, "sensitive_values_omitted": true}
	if enabled, ok := value["enabled"].(bool); ok {
		projected["enabled"] = enabled
	}
	if endpoint, ok := value["endpoint"].(string); ok && strings.TrimSpace(endpoint) != "" {
		projected["endpoint_configured"] = true
	}
	if token, ok := value["token"].(string); ok && strings.TrimSpace(token) != "" {
		projected["sensitive_values_omitted"] = true
	}
	for _, key := range []string{"index", "source", "sourcetype"} {
		if label, ok := value[key].(string); ok && strings.TrimSpace(label) != "" {
			projected[key+"_configured"] = true
		}
	}
	if insecure, ok := value["insecure_skip_verify"].(bool); ok {
		projected["insecure_skip_verify"] = insecure
	}
	return projected
}

func normalizeBeaconExporterConfig(root map[string]any) (json.RawMessage, error) {
	used := make(map[string]bool)
	pipelines := map[string]any{}
	if service, ok := root["service"].(map[string]any); ok {
		if configured, ok := service["pipelines"].(map[string]any); ok {
			signals := make([]string, 0, len(configured))
			for signal := range configured {
				if safe := safeConfigLabel(signal); safe != "" {
					signals = append(signals, safe)
				}
			}
			sort.Strings(signals)
			for _, signal := range signals {
				pipeline, ok := configured[signal].(map[string]any)
				if !ok {
					continue
				}
				projected := map[string]any{}
				for _, key := range []string{"receivers", "processors", "exporters"} {
					labels := safeConfigLabels(pipeline[key])
					if len(labels) > 0 {
						projected[key] = labels
					}
					if key == "exporters" {
						for _, name := range labels {
							used[name] = true
						}
					}
				}
				pipelines[signal] = projected
			}
		}
	}
	configuredExporters, _ := root["exporters"].(map[string]any)
	rawNames := make([]string, 0, len(configuredExporters))
	for name := range configuredExporters {
		rawNames = append(rawNames, name)
	}
	sort.Strings(rawNames)
	exporters := make([]map[string]any, 0, len(rawNames))
	seen := make(map[string]bool, len(rawNames))
	for _, rawName := range rawNames {
		name := safeConfigLabel(rawName)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		config := mapAt(configuredExporters, rawName)
		exporters = append(exporters, map[string]any{
			"name": name, "enabled": used[name], "endpoint_configured": containsEndpointField(config, 0),
			"sensitive_values_omitted": containsSensitiveField(config, 0),
		})
	}
	payload := map[string]any{
		"schema_version": beaconExporterConfigSchema,
		"source_format":  "yaml",
		"kind":           "collector_exporters",
		"pipelines":      pipelines,
		"exporters":      exporters,
	}
	data, err := json.Marshal(payload)
	return data, err
}

func containsEndpointField(value any, depth int) bool {
	if depth > 12 {
		return false
	}
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if strings.EqualFold(key, "endpoint") || strings.EqualFold(key, "endpoints") || strings.EqualFold(key, "url") {
				return true
			}
			if containsEndpointField(child, depth+1) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if containsEndpointField(child, depth+1) {
				return true
			}
		}
	}
	return false
}

func containsSensitiveField(value any, depth int) bool {
	if depth > 12 {
		return false
	}
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") ||
				strings.Contains(lower, "api_key") || strings.Contains(lower, "authorization") || strings.Contains(lower, "credential") ||
				strings.Contains(lower, "headers") {
				return true
			}
			if containsSensitiveField(child, depth+1) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if containsSensitiveField(child, depth+1) {
				return true
			}
		}
	}
	return false
}

func mapAt(value map[string]any, key string) map[string]any {
	if item, ok := value[key].(map[string]any); ok {
		return item
	}
	return map[string]any{}
}

func safeConfigLabels(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	labels := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if label := safeConfigLabel(item); label != "" && !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
	}
	sort.Strings(labels)
	return labels
}

func safeConfigLabel(value any) string {
	label, ok := value.(string)
	label = strings.TrimSpace(label)
	if !ok || len(label) == 0 || len(label) > 128 || !configLabelPattern.MatchString(label) {
		return ""
	}
	return label
}

func boundedConfigInt(value any, minimum, maximum int) (int, bool) {
	var number int
	switch item := value.(type) {
	case float64:
		if item != float64(int(item)) {
			return 0, false
		}
		number = int(item)
	case int:
		number = item
	case int64:
		if item < int64(minimum) || item > int64(maximum) {
			return 0, false
		}
		number = int(item)
	default:
		return 0, false
	}
	return number, number >= minimum && number <= maximum
}
