package memoryfeedback

import (
	"testing"
)

func validFeedback() Feedback {
	return Feedback{
		SchemaVersion: SchemaVersion, ID: "memory-feedback-0123456789abcdef0123456789abcdef",
		ProjectID: "repo:example", TaskID: "task-1", TargetKind: TargetProcedure,
		TargetID: "build-recovery", TargetRevision: 2, Exposure: ExposureUsed,
		Utility: UtilityHelpful, TaskResult: "passed", Harness: "codex-cli", Actor: "local-user",
		EvidenceClass: EvidenceSelfReported, CreatedAt: "2025-01-02T12:00:00Z",
	}
}

func TestValidateAcceptsSelfReportedFeedback(t *testing.T) {
	if err := Validate(validFeedback()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequiresAdvisorySelfReportedSemantics(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Feedback)
	}{
		{"unknown target", func(v *Feedback) { v.TargetKind = "trace" }},
		{"invalid exposure", func(v *Feedback) { v.Exposure = "inferred" }},
		{"not assessed after use", func(v *Feedback) { v.Utility = UtilityNotAssessed }},
		{"rating without exposure", func(v *Feedback) { v.Exposure = ExposureShownNotUsed }},
		{"invalid task result", func(v *Feedback) { v.TaskResult = "success" }},
		{"not self reported", func(v *Feedback) { v.EvidenceClass = "verified" }},
		{"cutover claim", func(v *Feedback) { v.CutoverEvidenceEligible = true }},
		{"authorization claim", func(v *Feedback) { v.ExecutionAuthorized = true }},
		{"credential material", func(v *Feedback) { v.Actor = "ghp_123456789012345678901234567890" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validFeedback()
			test.change(&value)
			if err := Validate(value); err == nil {
				t.Fatal("expected feedback validation to fail")
			}
		})
	}
}

func TestValidateShownMemoryCanBeMarkedNotUsed(t *testing.T) {
	value := validFeedback()
	value.Exposure = ExposureShownNotUsed
	value.Utility = UtilityNotAssessed
	if err := Validate(value); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFilterIsBoundedAndProjectScoped(t *testing.T) {
	if err := ValidateFilter(Filter{ProjectID: "repo:example", Limit: 50}); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []Filter{
		{ProjectID: "", Limit: 50},
		{ProjectID: "repo:example", Limit: 501},
		{ProjectID: "repo:example", Limit: 50, Offset: MaxProjectRecords + 1},
		{ProjectID: "repo:example", Limit: 50, TargetKind: "unknown"},
	} {
		if err := ValidateFilter(filter); err == nil {
			t.Fatalf("expected invalid filter to fail: %+v", filter)
		}
	}
}
