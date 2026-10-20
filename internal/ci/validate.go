package ci

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/inventory"
)

const (
	ValidationSchemaVersion = "threadforge.ci-validation.v1"
	MaxValidationLogBytes   = 256 << 20
	maxValidationLineBytes  = 1 << 20
)

type ValidationOptions struct {
	LogPath   string
	Harness   string
	MinEvents int
}

type ValidationResult struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	Harness       string `json:"harness,omitempty"`
	TotalEvents   int    `json:"total_events"`
	MatchedEvents int    `json:"matched_events"`
	MinEvents     int    `json:"min_events"`
	MalformedRows int    `json:"malformed_rows"`
	LogBytes      int64  `json:"log_bytes"`
	GeneratedAt   string `json:"generated_at"`
}

func ValidateLog(opts ValidationOptions) (ValidationResult, error) {
	if opts.MinEvents < 1 {
		opts.MinEvents = 1
	}
	harness, err := normalizeHarness(opts.Harness)
	if err != nil {
		return ValidationResult{}, err
	}
	result := ValidationResult{
		SchemaVersion: ValidationSchemaVersion,
		Status:        "failed",
		Harness:       harness,
		MinEvents:     opts.MinEvents,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	absolute, err := filepath.Abs(opts.LogPath)
	if err != nil {
		return result, errors.New("runtime log path is invalid")
	}
	if _, err := inventory.ValidateScanRoot(filepath.Dir(absolute)); err != nil {
		return result, errors.New("runtime log directory is unavailable or contains symlinks")
	}
	initial, err := os.Lstat(absolute)
	if err != nil {
		return result, errors.New("runtime log is unavailable")
	}
	if initial.Mode()&os.ModeSymlink != 0 || !initial.Mode().IsRegular() {
		return result, errors.New("runtime log must be a regular, non-symlink file")
	}
	if initial.Size() > MaxValidationLogBytes {
		return result, fmt.Errorf("runtime log exceeds the %d byte validation limit", MaxValidationLogBytes)
	}
	result.LogBytes = initial.Size()
	file, err := os.Open(absolute)
	if err != nil {
		return result, errors.New("runtime log could not be opened")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(initial, opened) {
		return result, errors.New("runtime log changed before validation")
	}
	scanner := bufio.NewScanner(io.LimitReader(file, MaxValidationLogBytes+1))
	scanner.Buffer(make([]byte, 64*1024), maxValidationLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			result.MalformedRows++
			continue
		}
		var event beacon.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil || event.Validate() != nil {
			result.MalformedRows++
			continue
		}
		result.TotalEvents++
		if harness == "" || matchesHarness(event.Harness.Name, harness) {
			result.MatchedEvents++
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("runtime log could not be read completely or contains a line larger than %d bytes", maxValidationLineBytes)
	}
	finalHandle, err := file.Stat()
	if err != nil {
		return result, errors.New("runtime log could not be restated after validation")
	}
	finalPath, err := os.Lstat(absolute)
	if err != nil || finalPath.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, finalHandle) || !os.SameFile(opened, finalPath) ||
		opened.Size() != finalHandle.Size() || opened.Size() != finalPath.Size() ||
		!opened.ModTime().Equal(finalHandle.ModTime()) || !opened.ModTime().Equal(finalPath.ModTime()) {
		return result, errors.New("runtime log changed during validation")
	}
	if result.MalformedRows == 0 && result.MatchedEvents >= result.MinEvents {
		result.Status = "passed"
	}
	return result, nil
}

func normalizeHarness(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all":
		return "", nil
	case "claude", "claude-code", "claude_code":
		return "claude_code", nil
	case "codex", "codex-cli", "codex_cli":
		return "codex_cli", nil
	default:
		return "", errors.New("unsupported CI harness; supported values are claude-code and codex-cli")
	}
}

func matchesHarness(got, want string) bool {
	got = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(got)), "-", "_")
	if got == want {
		return true
	}
	return (want == "claude_code" && got == "claude") || (want == "codex_cli" && got == "codex")
}
