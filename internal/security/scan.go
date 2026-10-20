package security

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve/rulestore"
	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve/threatrules"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	MaxScanBytes          = 128 << 20
	MaxScanEvents         = 100_000
	MaxFindings           = 100_000
	MaxRules              = 500
	MaxRuleBytes          = 1 << 20
	MaxRuleCorpus         = 64 << 20
	MaxSessionFilterBytes = 256
	maxEventLine          = 64 << 10
)

type RuleSummary struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Severity beacon.Severity     `json:"severity"`
	Posture  threatrules.Posture `json:"posture"`
	Source   rulestore.Source    `json:"source"`
}

type EventReference struct {
	EventID string `json:"event_id,omitempty"`
	At      string `json:"timestamp"`
	Harness string `json:"harness"`
	Action  string `json:"action"`
}

type FindingSummary struct {
	RuleID           string              `json:"rule_id"`
	Title            string              `json:"title"`
	Severity         beacon.Severity     `json:"severity"`
	Posture          threatrules.Posture `json:"posture"`
	Reason           string              `json:"reason"`
	SessionReference string              `json:"session_reference,omitempty"`
	Events           []EventReference    `json:"events"`
}

type ScanResult struct {
	SchemaVersion        string           `json:"schema_version"`
	Complete             bool             `json:"complete"`
	EventCount           int              `json:"event_count"`
	MalformedLines       int              `json:"malformed_lines"`
	RuleCount            int              `json:"rule_count"`
	Rules                []RuleSummary    `json:"rules"`
	Findings             []FindingSummary `json:"findings"`
	FailThresholdMatched bool             `json:"-"`
}

func ScanFile(ctx context.Context, logPath, ruleStoreDir, ruleOverride string, minimumSeverity beacon.Severity) (ScanResult, error) {
	return ScanFileWithFilters(ctx, logPath, ruleStoreDir, ruleOverride, minimumSeverity, "", "")
}

// ScanFileWithFailThreshold filters returned findings by minimumSeverity while
// independently evaluating failOnSeverity against every valid finding.
func ScanFileWithFailThreshold(ctx context.Context, logPath, ruleStoreDir, ruleOverride string, minimumSeverity, failOnSeverity beacon.Severity) (ScanResult, error) {
	return ScanFileWithFilters(ctx, logPath, ruleStoreDir, ruleOverride, minimumSeverity, "", failOnSeverity)
}

// ScanFileWithFilters scopes rule evaluation to events whose session ID contains
// sessionContains, filters returned findings by minimumSeverity, and evaluates
// failOnSeverity independently from the display filter.
func ScanFileWithFilters(ctx context.Context, logPath, ruleStoreDir, ruleOverride string, minimumSeverity beacon.Severity, sessionContains string, failOnSeverity beacon.Severity) (ScanResult, error) {
	if !validSeverity(minimumSeverity) {
		return ScanResult{}, errors.New("minimum severity must be info, low, medium, high, or critical")
	}
	sessionContains = strings.TrimSpace(sessionContains)
	if len(sessionContains) > MaxSessionFilterBytes {
		return ScanResult{}, fmt.Errorf("session filter must not exceed %d UTF-8 bytes", MaxSessionFilterBytes)
	}
	if failOnSeverity != "" && !validSeverity(failOnSeverity) {
		return ScanResult{}, errors.New("fail threshold must be empty or info, low, medium, high, or critical")
	}
	if ruleOverride != "" {
		if err := validateRuleDirectory(ruleOverride); err != nil {
			return ScanResult{}, fmt.Errorf("validate rule directory: %w", err)
		}
	} else if rulestore.HasRuleFiles(ruleStoreDir) {
		if err := validateRuleDirectory(ruleStoreDir); err != nil {
			return ScanResult{}, fmt.Errorf("validate installed rule directory: %w", err)
		}
	}
	loaded, err := rulestore.LoadActive(ruleStoreDir, ruleOverride)
	if err != nil {
		return ScanResult{}, fmt.Errorf("load threat rules: %w", err)
	}
	if len(loaded) == 0 || len(loaded) > MaxRules {
		return ScanResult{}, fmt.Errorf("active rule set must contain 1 to %d rules", MaxRules)
	}
	compiled := make([]*threatrules.CompiledRule, 0, len(loaded))
	ruleSummaries := make([]RuleSummary, 0, len(loaded))
	for _, item := range loaded {
		rule, err := threatrules.Compile(item.Rule)
		if err != nil {
			return ScanResult{}, fmt.Errorf("compile rule %s: %w", item.Rule.ID, err)
		}
		compiled = append(compiled, rule)
		ruleSummaries = append(ruleSummaries, RuleSummary{
			ID: cleanRuleText(item.Rule.ID, 128), Title: cleanRuleText(item.Rule.Title, 240),
			Severity: item.Rule.Severity, Posture: item.Rule.Posture, Source: item.Source,
		})
	}
	result := ScanResult{
		SchemaVersion: "threadforge.security-scan.v1", Complete: true,
		RuleCount: len(compiled), Rules: ruleSummaries, Findings: []FindingSummary{},
	}
	events, malformed, err := readEvents(ctx, logPath)
	if err != nil {
		return ScanResult{}, err
	}
	result.EventCount = len(events)
	result.MalformedLines = malformed
	result.Complete = malformed == 0
	if malformed > 0 {
		return result, nil
	}
	if sessionContains != "" {
		needle := strings.ToLower(sessionContains)
		filtered := make([]beacon.Event, 0, len(events))
		for _, event := range events {
			if event.Session != nil && strings.Contains(strings.ToLower(event.Session.ID), needle) {
				filtered = append(filtered, event)
			}
		}
		events = filtered
		result.EventCount = len(events)
	}
	threatrules.SortEvents(events)
	findings := make([]threatrules.Finding, 0)
	minimumRank := threatrules.SeverityRank(minimumSeverity)
	failThresholdRank := threatrules.SeverityRank(failOnSeverity)
	for _, rule := range compiled {
		matches, err := rule.Findings(events)
		if err != nil {
			return ScanResult{}, fmt.Errorf("evaluate rule %s: %w", rule.Rule().ID, err)
		}
		for _, finding := range matches {
			findingRank := threatrules.SeverityRank(finding.Severity)
			if failOnSeverity != "" && findingRank >= failThresholdRank {
				result.FailThresholdMatched = true
			}
			if findingRank < minimumRank {
				continue
			}
			findings = append(findings, finding)
			if len(findings) > MaxFindings {
				return ScanResult{}, fmt.Errorf("security scan exceeds %d reported-finding limit", MaxFindings)
			}
		}
	}
	threatrules.SortFindings(findings)
	for _, finding := range findings {
		item := FindingSummary{
			RuleID: cleanRuleText(finding.RuleID, 128), Title: cleanRuleText(finding.Title, 240),
			Severity: finding.Severity, Posture: finding.Posture, Reason: cleanRuleText(finding.Reason, 1000),
			Events: make([]EventReference, 0, len(finding.Events)),
		}
		if finding.SessionID != "" {
			hash := sha256.Sum256([]byte(finding.SessionID))
			item.SessionReference = hex.EncodeToString(hash[:16])
		}
		for _, event := range finding.Events {
			reference := EventReference{
				At: event.Timestamp, Harness: cleanRuleText(event.Harness.Name, 128),
				Action: cleanRuleText(event.Event.Action, 256),
			}
			if event.Event.ID != "" && !privacy.ContainsCredentialText(event.Event.ID) {
				reference.EventID = cleanRuleText(event.Event.ID, 128)
			}
			item.Events = append(item.Events, reference)
		}
		result.Findings = append(result.Findings, item)
	}
	return result, nil
}

func validateRuleDirectory(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("rule root must be an existing directory, not a symlink")
	}
	count := 0
	var total int64
	err = filepath.WalkDir(abs, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link is not allowed in rule corpus: %s", filepath.Base(path))
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".rule.yaml") || strings.HasPrefix(entry.Name(), "._") {
			return nil
		}
		if !entryInfo.Mode().IsRegular() || entryInfo.Size() > MaxRuleBytes {
			return fmt.Errorf("rule file %s must be regular and no larger than 1 MiB", filepath.Base(path))
		}
		count++
		if count > MaxRules || total > MaxRuleCorpus-entryInfo.Size() {
			return fmt.Errorf("rule corpus exceeds %d files or 64 MiB", MaxRules)
		}
		total += entryInfo.Size()
		return nil
	})
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("rule directory contains no *.rule.yaml files")
	}
	return nil
}

func readEvents(ctx context.Context, path string) ([]beacon.Event, int, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, errors.New("trace input must be a regular file, not a symlink")
	}
	if info.Size() > MaxScanBytes {
		return nil, 0, fmt.Errorf("trace input exceeds the %d MiB read-only scan limit", MaxScanBytes>>20)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, info.Size()+1))
	scanner.Buffer(make([]byte, 64*1024), maxEventLine)
	events := make([]beacon.Event, 0)
	malformed := 0
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var event beacon.Event
		if err := json.Unmarshal(line, &event); err != nil || event.Validate() != nil || !validTimestamp(event.Timestamp) {
			malformed++
			continue
		}
		events = append(events, event)
		if len(events) > MaxScanEvents {
			return nil, 0, fmt.Errorf("trace input exceeds %d-event scan limit", MaxScanEvents)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("scan trace JSONL: %w", err)
	}
	finalInfo, err := file.Stat()
	if err != nil || finalInfo.Size() != info.Size() || !finalInfo.ModTime().Equal(info.ModTime()) {
		return nil, 0, errors.New("trace input changed during scan; retry against a stable snapshot")
	}
	return events, malformed, nil
}

func validSeverity(value beacon.Severity) bool {
	switch value {
	case beacon.SeverityInfo, beacon.SeverityLow, beacon.SeverityMedium, beacon.SeverityHigh, beacon.SeverityCritical:
		return true
	default:
		return false
	}
}

func validTimestamp(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func cleanRuleText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if privacy.ContainsCredentialText(value) {
		return "[REDACTED]"
	}
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}
