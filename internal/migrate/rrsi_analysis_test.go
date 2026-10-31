package migrate

import (
	"encoding/json"
	"strings"
	"testing"
)

const rrsiAnalysisFailureFixture = `{"mode":"tool_state_loss","n_tasks":2,"affected_tasks":["private-task-a","private-task-b"],"description":"private failure description","needed_instead":"private correction","representative_evidence":[{"task_id":"private-task-a","where":"private/file.py:8","quote":"private evidence quote"}]}`
const rrsiAnalysisGapFixture = `{"gap":"missing_observation","n_tasks":1,"affected_tasks":["private-task-c"],"description":"private gap description","representative_evidence":[]}`
const rrsiAnalysisHabitFixture = `{"habit":"verify_before_commit","n_tasks":3,"description":"private success description","risk_if_broken":"private regression risk"}`

func rrsiAnalysisReportFixture() string {
	return `{"failure_modes":[` + rrsiAnalysisFailureFixture + `],"capability_gaps":[` + rrsiAnalysisGapFixture +
		`],"success_habits":[` + rrsiAnalysisHabitFixture + `],"n_digests":4}`
}

func rrsiGlobalAnalysisFixture() string {
	return `{"failure_modes":[` + rrsiAnalysisFailureFixture + `],"success_habits":[` + rrsiAnalysisHabitFixture + `]}`
}

func TestRRSIAnalysisPathsAndSourceShapes(t *testing.T) {
	for _, path := range []string{
		"runs/coding/r0/analysis_report.json",
		"runs/workspace/r12/analysis_report.json",
		"runs/eng/r3/analysis_report.json",
		"runs/coding/global_analysis.json",
	} {
		if !isRRSIAnalysisPath(path) || !isRRSIMetadataPath(path) || !isRRSISafelyReadPath(path) {
			t.Errorf("documented RRSI analysis path is not supported/safe-read: %s", path)
		}
	}
	for _, path := range []string{
		"runs/coding/r00/analysis_report.json",
		"runs/coding/jobs/base/analysis_report.json",
		"runs/unknown/global_analysis.json",
		"runs/eng/r1/global_analysis.json",
		"runs/workspace/global_analysis.json.bak",
	} {
		if isRRSIAnalysisPath(path) {
			t.Errorf("unsupported RRSI analysis path accepted: %s", path)
		}
	}
}

func TestRRSIAnalysisNormalizationDropsModelTextAndTaskEvidence(t *testing.T) {
	tests := []struct {
		path string
		data string
		kind string
	}{
		{"runs/coding/r0/analysis_report.json", rrsiAnalysisReportFixture(), "round_report"},
		{"runs/workspace/global_analysis.json", rrsiGlobalAnalysisFixture(), "global_latest"},
	}
	for _, test := range tests {
		item, alias, err := normalizeRRSIAnalysisFile(test.path, []byte(test.data))
		if err != nil {
			t.Fatalf("normalize %s: %v", test.path, err)
		}
		if item.Type != rrsiAnalysisRecordType || strings.Contains(alias, "runs/") || strings.Contains(alias, ".json") {
			t.Fatalf("RRSI analysis alias/type retained source identity: %s %s", item.Type, alias)
		}
		for _, private := range []string{
			"tool_state_loss", "missing_observation", "verify_before_commit", "private-task-a", "private/file.py",
			"private evidence quote", "private failure description", "private correction", "private gap description",
			"private success description", "private regression risk",
		} {
			if strings.Contains(string(item.Payload), private) || strings.Contains(alias, private) {
				t.Fatalf("RRSI analysis summary retained model/task content %q: %s", private, item.Payload)
			}
		}
		var summary rrsiAnalysisSummary
		if err := json.Unmarshal(item.Payload, &summary); err != nil {
			t.Fatal(err)
		}
		if summary.SchemaVersion != rrsiAnalysisSummarySchema || summary.ArtifactKind != test.kind ||
			summary.FailureModeCount != 1 || summary.ReportedFailureTaskCount != 2 || summary.FailureEvidenceCount != 1 ||
			summary.SuccessHabitCount != 1 || summary.ReportedSuccessTaskCount != 3 ||
			!summary.HumanReviewRequired || summary.ExecutionAuthorized || summary.CutoverEvidenceEligible {
			t.Fatalf("RRSI analysis projection lost structural counts or crossed its review boundary: %#v", summary)
		}
		if test.kind == "round_report" && (summary.RoundIndex == nil || *summary.RoundIndex != 0 ||
			!summary.DigestCountPresent || summary.DigestCount != 4 || summary.CapabilityGapCount != 1 ||
			summary.AffectedTaskReferenceCount != 3 || summary.AnalysisIncomplete) {
			t.Fatalf("RRSI round report summary is inconsistent: %#v", summary)
		}
		if test.kind == "global_latest" && (summary.RoundIndex != nil || summary.RoundSHA256 != "" ||
			summary.DigestCountPresent || summary.CapabilityGapCount != 0) {
			t.Fatalf("RRSI global analysis summary has round-only state: %#v", summary)
		}
		entry := FileEntry{Path: test.path, SourceSHA256: digest([]byte(test.data)), SourceBytes: int64(len(test.data))}
		record := Record{Type: item.Type, SourcePath: alias, Scope: "local_pending_review", Payload: item.Payload}
		if err := validateRRSIAnalysisSummary(record, entry); err != nil {
			t.Fatalf("valid RRSI analysis summary failed readback: %v", err)
		}
	}
}

func TestRRSIAnalysisRejectsMalformedProducerShapeAndIncompleteReports(t *testing.T) {
	path := "runs/coding/r1/analysis_report.json"
	incomplete := `{"failure_modes":[],"capability_gaps":[],"success_habits":[],"n_digests":2,"error":"analyst hit max turns"}`
	item, alias, err := normalizeRRSIAnalysisFile(path, []byte(incomplete))
	if err != nil {
		t.Fatalf("pinned max-turn fallback should remain migratable: %v", err)
	}
	entry := FileEntry{Path: path, SourceSHA256: digest([]byte(incomplete)), SourceBytes: int64(len(incomplete))}
	if err := validateRRSIAnalysisSummary(Record{Type: item.Type, SourcePath: alias, Payload: item.Payload}, entry); err != nil {
		t.Fatalf("valid incomplete report failed readback: %v", err)
	}
	var summary rrsiAnalysisSummary
	if err := json.Unmarshal(item.Payload, &summary); err != nil || !summary.AnalysisIncomplete {
		t.Fatalf("max-turn fallback was not marked incomplete: %#v err=%v", summary, err)
	}
	invalid := []string{
		strings.Replace(rrsiAnalysisReportFixture(), `"n_digests":4`, `"n_digests":"4"`, 1),
		strings.Replace(rrsiAnalysisReportFixture(), `"mode":"tool_state_loss"`, `"mode":"Private Label"`, 1),
		strings.Replace(rrsiAnalysisReportFixture(), `"representative_evidence":[]`, `"unknown":true,"representative_evidence":[]`, 1),
		strings.Replace(rrsiAnalysisReportFixture(), `"affected_tasks":["private-task-a","private-task-b"]`, `"affected_tasks":[null]`, 1),
		strings.Replace(rrsiAnalysisReportFixture(), `"n_digests":4`, `"n_digests":4,"unexpected":true`, 1),
		`{"failure_modes":[],"failure_modes":[],"capability_gaps":[],"success_habits":[],"n_digests":0}`,
		`{"failure_modes":[],"success_habits":[],"unexpected":true}`,
		`{"failure_modes":[],"capability_gaps":[],"success_habits":[],"n_digests":0,"error":"private runtime error"}`,
	}
	for i, value := range invalid {
		if _, _, err := normalizeRRSIAnalysisFile("runs/coding/r0/analysis_report.json", []byte(value)); err == nil {
			t.Errorf("invalid RRSI analysis producer fixture %d was accepted", i)
		}
	}
	globalInvalid := strings.Replace(rrsiGlobalAnalysisFixture(), `"success_habits":[`, `"capability_gaps":[],"success_habits":[`, 1)
	if _, _, err := normalizeRRSIAnalysisFile("runs/coding/global_analysis.json", []byte(globalInvalid)); err == nil {
		t.Fatal("global-analysis importer accepted unsupported fields")
	}
}
