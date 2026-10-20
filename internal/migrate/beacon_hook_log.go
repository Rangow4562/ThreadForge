package migrate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"
)

const (
	beaconHookLogSummarySchema = "threadforge.beacon-hook-log-summary.v1"
	maxBeaconHookLogLines      = 100_000
	maxBeaconHookLogLineBytes  = 1 << 20
)

var beaconHookLogHarnesses = map[string]string{
	"antigravity": "antigravity",
	"claude":      "claude_code",
	"cline":       "cline",
	"copilot":     "github_copilot",
	"cursor":      "cursor",
	"devin":       "devin_cli",
	"dsh":         "deepseek_harness",
	"factory":     "factory_droid",
	"grok":        "grok_build",
	"hermes":      "hermes_agent",
	"kiro":        "kiro",
	"kimi":        "kimi_code",
	"muse":        "muse_code",
	"openhands":   "openhands",
	"opencode":    "opencode",
	"qwen":        "qwen_code",
	"vscode":      "vscode_copilot",
}

type beaconHookLogSummary struct {
	SchemaVersion       string         `json:"schema_version"`
	SourceFormat        string         `json:"source_format"`
	Kind                string         `json:"kind"`
	Harness             string         `json:"harness"`
	EntryCount          int            `json:"entry_count"`
	LevelCounts         map[string]int `json:"level_counts"`
	FirstDay            *string        `json:"first_day"`
	LastDay             *string        `json:"last_day"`
	OmittedFields       []string       `json:"omitted_fields"`
	HumanReviewRequired bool           `json:"human_review_required"`
	ExecutionAuthorized bool           `json:"execution_authorized"`
}

func isBeaconHookLogPath(relative, sourceRoot string) bool {
	_, ok := beaconHookLogPlatform(relative, sourceRoot)
	return ok
}

func beaconHookLogPlatform(relative, sourceRoot string) (string, bool) {
	clean := strings.ToLower(strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relative)), "./"))
	parts := strings.Split(clean, "/")
	root := filepath.Clean(sourceRoot)
	rootBase := strings.ToLower(filepath.Base(root))
	parent := strings.ToLower(filepath.Base(filepath.Dir(root)))
	platform := ""
	switch {
	case len(parts) == 3 && parts[0] == ".beacon" && parts[2] == "hooks.log":
		platform = parts[1]
	case len(parts) == 2 && parts[1] == "hooks.log" && rootBase == ".beacon":
		platform = parts[0]
	case len(parts) == 1 && parts[0] == "hooks.log" && parent == ".beacon":
		platform = rootBase
	case len(parts) == 4 && parts[0] == ".beacon" && parts[2] == "logs" && safeBeaconSessionLogName(parts[3]):
		platform = parts[1]
	case len(parts) == 3 && parts[1] == "logs" && rootBase == ".beacon" && safeBeaconSessionLogName(parts[2]):
		platform = parts[0]
	case len(parts) == 2 && parts[0] == "logs" && parent == ".beacon" && safeBeaconSessionLogName(parts[1]):
		platform = rootBase
	case len(parts) == 1 && rootBase == "logs" && safeBeaconSessionLogName(parts[0]):
		platform = strings.ToLower(filepath.Base(filepath.Dir(root)))
		if strings.ToLower(filepath.Base(filepath.Dir(filepath.Dir(root)))) != ".beacon" {
			platform = ""
		}
	}
	_, ok := beaconHookLogHarnesses[platform]
	return platform, ok
}

func safeBeaconSessionLogName(name string) bool {
	if !strings.EqualFold(filepath.Ext(name), ".log") || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return false
	}
	identifier := strings.TrimSuffix(name, filepath.Ext(name))
	return identifier != "" && len(identifier) <= 128 && identifier != "." && identifier != ".."
}

func normalizeBeaconHookLog(relative, sourceRoot string, data []byte) (json.RawMessage, error) {
	harness, ok := beaconHookLogHarness(relative, sourceRoot)
	if !ok {
		return nil, errors.New("Beacon hook log is outside the documented state roots")
	}
	counts := map[string]int{"debug": 0, "error": 0, "info": 0, "other": 0, "warn": 0}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), maxBeaconHookLogLineBytes)
	var count int
	var first, last time.Time
	for scanner.Scan() {
		if count >= maxBeaconHookLogLines {
			return nil, errors.New("Beacon hook log exceeds entry limit")
		}
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			return nil, errors.New("Beacon hook log contains an empty entry")
		}
		fields, err := decodeBeaconHookLogEntry(line)
		if err != nil {
			return nil, err
		}
		hook, err := beaconHookLogString(fields, "hook", true)
		if err != nil || len(hook) > 128 {
			return nil, errors.New("Beacon hook log entry has an invalid hook label")
		}
		level, err := beaconHookLogString(fields, "level", true)
		if err != nil || len(level) > 32 {
			return nil, errors.New("Beacon hook log entry has an invalid level")
		}
		if _, err := beaconHookLogString(fields, "message", false); err != nil {
			return nil, errors.New("Beacon hook log entry has an invalid message field")
		}
		timestamp, err := beaconHookLogString(fields, "timestamp", true)
		if err != nil {
			return nil, errors.New("Beacon hook log entry has an invalid timestamp")
		}
		observedAt, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return nil, errors.New("Beacon hook log entry has an invalid timestamp")
		}
		levelKey := strings.ToLower(level)
		switch levelKey {
		case "debug", "error", "info":
		case "warn", "warning":
			levelKey = "warn"
		default:
			levelKey = "other"
		}
		counts[levelKey]++
		observedAt = observedAt.UTC()
		if count == 0 || observedAt.Before(first) {
			first = observedAt
		}
		if count == 0 || observedAt.After(last) {
			last = observedAt
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("Beacon hook log contains an oversized or unreadable entry")
	}
	var firstDay, lastDay *string
	if count > 0 {
		firstValue := first.Format("2006-01-02")
		lastValue := last.Format("2006-01-02")
		firstDay, lastDay = &firstValue, &lastValue
	}
	payload, err := json.Marshal(beaconHookLogSummary{
		SchemaVersion:       beaconHookLogSummarySchema,
		SourceFormat:        "jsonl",
		Kind:                "hook_log_summary",
		Harness:             harness,
		EntryCount:          count,
		LevelCounts:         counts,
		FirstDay:            firstDay,
		LastDay:             lastDay,
		OmittedFields:       []string{"arbitrary_fields", "hook_names", "message", "raw_source_lines", "session_id"},
		HumanReviewRequired: true,
		ExecutionAuthorized: false,
	})
	return payload, err
}

func beaconHookLogHarness(relative, sourceRoot string) (string, bool) {
	platform, ok := beaconHookLogPlatform(relative, sourceRoot)
	if !ok {
		return "", false
	}
	harness, ok := beaconHookLogHarnesses[platform]
	return harness, ok
}

func decodeBeaconHookLogEntry(line []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("Beacon hook log entry must be a JSON object")
	}
	fields := make(map[string]json.RawMessage, 6)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, errors.New("Beacon hook log entry is malformed")
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("Beacon hook log entry has an invalid field name")
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, errors.New("Beacon hook log entry contains a duplicate field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("Beacon hook log entry is malformed")
		}
		fields[key] = value
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, errors.New("Beacon hook log entry is malformed")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("Beacon hook log entry has trailing data")
	}
	return fields, nil
}

func beaconHookLogString(fields map[string]json.RawMessage, key string, nonempty bool) (string, error) {
	raw, ok := fields[key]
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", errors.New("missing or non-string field")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || (nonempty && strings.TrimSpace(value) == "") {
		return "", errors.New("invalid string field")
	}
	return value, nil
}
