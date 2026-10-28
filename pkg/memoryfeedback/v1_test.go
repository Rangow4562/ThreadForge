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

func TestValidateRetrievalReceiptSeparatesHandlerResultFromExposure(t *testing.T) {
	receipt := RetrievalReceipt{
		SchemaVersion: RetrievalReceiptVersion, ID: "retrieval-0123456789abcdef0123456789abcdef",
		ProjectID: "repo:example", TaskID: "task-1", TargetKind: TargetProcedure,
		TargetID: "build-recovery", TargetRevision: 2, Tool: "threadforge_procedure_get",
		Freshness: "verified", GuidanceIncluded: true, ObservationBoundary: "mcp_handler_result_prepared",
		CreatedAt: "2025-01-02T12:00:00Z",
	}
	if err := ValidateRetrievalReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Freshness = "stale"
	if err := ValidateRetrievalReceipt(receipt); err == nil {
		t.Fatal("stale receipt claiming included guidance was accepted")
	}
	receipt.Freshness = "verified"
	receipt.GuidanceIncluded = false
	if err := ValidateRetrievalReceipt(receipt); err == nil {
		t.Fatal("verified receipt claiming withheld guidance was accepted")
	}
	receipt.GuidanceIncluded = true
	receipt.ID = "not-a-retrieval-receipt"
	if err := ValidateRetrievalReceipt(receipt); err == nil {
		t.Fatal("non-random receipt identifier was accepted")
	}
}

func TestValidateProjectMemoryRetrievalReceiptV2CarriesFreshnessAndReadStatus(t *testing.T) {
	receipt := RetrievalReceipt{
		SchemaVersion: RetrievalReceiptVersionV2, ID: "retrieval-3123456789abcdef0123456789abcdef",
		ProjectID: "repo:example", TaskID: "task-1", TargetKind: TargetFact,
		TargetID: "build-invariant", TargetRevision: 2, Tool: "threadforge_project_memory_read_v2",
		Freshness: "verified", ResultStatus: "complete", GuidanceIncluded: true,
		ObservationBoundary: "mcp_handler_result_prepared", CreatedAt: "2025-01-02T12:00:00Z",
	}
	if err := ValidateRetrievalReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	receipt.ResultStatus = "incomplete"
	receipt.Freshness = "unverified"
	if err := ValidateRetrievalReceipt(receipt); err != nil {
		t.Fatalf("legacy record returned with an unverified caveat should remain valid: %v", err)
	}
	receipt.ResultStatus = "stale"
	receipt.Freshness = "stale"
	if err := ValidateRetrievalReceipt(receipt); err == nil {
		t.Fatal("stale project-memory receipt with included guidance was accepted")
	}
	receipt.GuidanceIncluded = false
	if err := ValidateRetrievalReceipt(receipt); err != nil {
		t.Fatalf("stale project-memory receipt withholding guidance was rejected: %v", err)
	}
}

func TestValidateHookRetrievalReceiptV3UsesHostPreparationBoundary(t *testing.T) {
	receipt := RetrievalReceipt{
		SchemaVersion: RetrievalReceiptVersionV3, ID: "retrieval-4123456789abcdef0123456789abcdef",
		ProjectID: "repo:example", TaskID: "task-1", SessionID: "session-1",
		TargetKind: TargetFact, TargetID: "build-invariant", TargetRevision: 2,
		Tool: "threadforge_memory_inject", Host: "codex-cli", Freshness: "verified",
		GuidanceIncluded: true, ObservationBoundary: "hook_response_prepared", CreatedAt: "2026-10-28T12:00:00Z",
	}
	if err := ValidateRetrievalReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Freshness = "stale"
	if err := ValidateRetrievalReceipt(receipt); err == nil {
		t.Fatal("stale hook receipt claiming included guidance was accepted")
	}
	receipt.Freshness = "verified"
	receipt.GuidanceIncluded = false
	if err := ValidateRetrievalReceipt(receipt); err != nil {
		t.Fatalf("hook response that withheld guidance should remain auditable: %v", err)
	}
	receipt.Host = "unknown-host"
	if err := ValidateRetrievalReceipt(receipt); err == nil {
		t.Fatal("unsupported hook host was accepted")
	}
}

func TestValidateRetrievalReceiptFilterRequiresExactTaskScope(t *testing.T) {
	filter := RetrievalReceiptFilter{ProjectID: "repo:example", TaskID: "task-1", Limit: 50}
	if err := ValidateRetrievalReceiptFilter(filter); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []RetrievalReceiptFilter{
		{ProjectID: "repo:example", Limit: 50},
		{ProjectID: "repo:example", TaskID: "task-1", Limit: 501},
		{ProjectID: "repo:example", TaskID: "task-1", Limit: 50, Offset: MaxProjectRecords + 1},
	} {
		if err := ValidateRetrievalReceiptFilter(invalid); err == nil {
			t.Fatalf("expected invalid receipt filter to fail: %+v", invalid)
		}
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
