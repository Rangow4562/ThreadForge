package ci

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/threadforge-ai/threadforge/internal/inventory"
)

const (
	PublicSessionSummarySchemaVersion = "threadforge.ci-public-summary.v1"
	PublicSessionSummaryFileName      = "threadforge-ci-public-summary.json"
	maxPublicSessionSummaryBytes      = 16 << 10
)

// PublicSessionSummary is the only CI session receipt intended for explicit
// remote publication. It intentionally contains no paths, timestamps, run IDs,
// repository identity, or event content.
type PublicSessionSummary struct {
	SchemaVersion     string `json:"schema_version"`
	Harness           string `json:"harness"`
	Status            string `json:"status"`
	TelemetryRequired bool   `json:"telemetry_required"`
	ValidationStatus  string `json:"validation_status"`
	TotalEvents       int    `json:"total_events"`
	MatchedEvents     int    `json:"matched_events"`
	MalformedRows     int    `json:"malformed_rows"`
	MinimumEvents     int    `json:"minimum_events"`
}

func publicSessionSummary(result SessionFinishResult) (PublicSessionSummary, error) {
	if result.Status != "passed" && result.Status != "failed" {
		return PublicSessionSummary{}, errors.New("CI finish status is invalid")
	}
	if result.Validation.Harness != "claude_code" && result.Validation.Harness != "codex_cli" {
		return PublicSessionSummary{}, errors.New("CI validation harness is invalid")
	}
	if result.Validation.Status != "passed" && result.Validation.Status != "failed" {
		return PublicSessionSummary{}, errors.New("CI validation status is invalid")
	}
	if result.Validation.TotalEvents < 0 || result.Validation.MatchedEvents < 0 ||
		result.Validation.MatchedEvents > result.Validation.TotalEvents || result.Validation.MalformedRows < 0 ||
		result.Validation.MinEvents < 1 {
		return PublicSessionSummary{}, errors.New("CI validation counts are invalid")
	}
	return PublicSessionSummary{
		SchemaVersion: PublicSessionSummarySchemaVersion,
		Harness:       result.Validation.Harness, Status: result.Status,
		TelemetryRequired: result.RequireTelemetry, ValidationStatus: result.Validation.Status,
		TotalEvents: result.Validation.TotalEvents, MatchedEvents: result.Validation.MatchedEvents,
		MalformedRows: result.Validation.MalformedRows, MinimumEvents: result.Validation.MinEvents,
	}, nil
}

func writePublicSessionSummary(path string, result SessionFinishResult) error {
	if filepath.Base(path) != PublicSessionSummaryFileName || filepath.Clean(path) != path {
		return errors.New("sanitized CI summary path is invalid")
	}
	absolute, err := filepath.Abs(path)
	if err != nil || filepath.Clean(absolute) != absolute {
		return errors.New("sanitized CI summary path is invalid")
	}
	if _, err := inventory.ValidateScanRoot(filepath.Dir(absolute)); err != nil {
		return errors.New("sanitized CI summary directory is unavailable or contains symlinks")
	}
	summary, err := publicSessionSummary(result)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil || len(data) > maxPublicSessionSummaryBytes {
		return errors.New("sanitized CI summary exceeds its size limit")
	}
	data = append(data, '\n')
	if err := acceptExistingPublicSummary(absolute, data); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return acceptExistingPublicSummary(absolute, data)
	}
	if err != nil {
		return errors.New("sanitized CI summary could not be created")
	}
	info, statErr := file.Stat()
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if statErr != nil || writeErr != nil || syncErr != nil || closeErr != nil {
		current, currentErr := os.Lstat(absolute)
		if currentErr == nil && info != nil && os.SameFile(info, current) {
			_ = os.Remove(absolute)
		}
		return errors.New("sanitized CI summary could not be persisted")
	}
	return nil
}

func acceptExistingPublicSummary(path string, expected []byte) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxPublicSessionSummaryBytes {
		return errors.New("existing sanitized CI summary is unsafe")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return errors.New("existing sanitized CI summary could not be read")
	}
	after, err := os.Lstat(path)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() ||
		!os.SameFile(info, after) || after.Size() != info.Size() || !bytes.Equal(current, expected) {
		return errors.New("existing sanitized CI summary conflicts with the validated result")
	}
	return nil
}
