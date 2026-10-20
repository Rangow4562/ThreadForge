package ci

import (
	"context"
	"errors"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	securityscan "github.com/threadforge-ai/threadforge/internal/security"
)

const SecurityGateSchemaVersion = "threadforge.ci-security-gate.v1"

type SecurityGateOptions struct {
	Validation    ValidationOptions
	RulesStoreDir string
	RulesDir      string
	FailOn        beacon.Severity
}

type SecurityGateScanResult struct {
	Complete           bool           `json:"complete"`
	EventCount         int            `json:"event_count"`
	MalformedLines     int            `json:"malformed_lines"`
	RuleCount          int            `json:"rule_count"`
	FindingCount       int            `json:"finding_count"`
	FindingsBySeverity map[string]int `json:"findings_by_severity"`
	FailOn             string         `json:"fail_on"`
	ThresholdMatched   bool           `json:"threshold_matched"`
}

type SecurityGateResult struct {
	SchemaVersion string                 `json:"schema_version"`
	Status        string                 `json:"status"`
	Validation    ValidationResult       `json:"validation"`
	Security      SecurityGateScanResult `json:"security"`
}

func EvaluateSecurityGate(ctx context.Context, opts SecurityGateOptions) (SecurityGateResult, error) {
	if !validSecurityGateSeverity(opts.FailOn) {
		return SecurityGateResult{}, errors.New("security gate requires fail-on info, low, medium, high, or critical")
	}
	validation, err := ValidateLog(opts.Validation)
	if err != nil {
		return SecurityGateResult{}, errors.New("CI telemetry validation could not complete")
	}
	scan, err := securityscan.ScanFileWithFailThreshold(
		ctx,
		opts.Validation.LogPath,
		opts.RulesStoreDir,
		opts.RulesDir,
		beacon.SeverityInfo,
		opts.FailOn,
	)
	if err != nil {
		return SecurityGateResult{}, errors.New("local CI threat scan could not complete")
	}
	bySeverity := make(map[string]int)
	for _, finding := range scan.Findings {
		bySeverity[string(finding.Severity)]++
	}
	status := "passed"
	if !scan.Complete {
		status = "incomplete"
	} else if validation.Status != "passed" || scan.FailThresholdMatched {
		status = "failed"
	}
	return SecurityGateResult{
		SchemaVersion: SecurityGateSchemaVersion,
		Status:        status,
		Validation:    validation,
		Security: SecurityGateScanResult{
			Complete: scan.Complete, EventCount: scan.EventCount,
			MalformedLines: scan.MalformedLines, RuleCount: scan.RuleCount,
			FindingCount: len(scan.Findings), FindingsBySeverity: bySeverity,
			FailOn: string(opts.FailOn), ThresholdMatched: scan.FailThresholdMatched,
		},
	}, nil
}

func validSecurityGateSeverity(value beacon.Severity) bool {
	switch value {
	case beacon.SeverityInfo, beacon.SeverityLow, beacon.SeverityMedium, beacon.SeverityHigh, beacon.SeverityCritical:
		return true
	default:
		return false
	}
}
