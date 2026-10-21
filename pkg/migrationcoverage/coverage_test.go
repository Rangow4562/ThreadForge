package migrationcoverage

import (
	"strings"
	"testing"
)

func TestValidateRequiresCatalogAndReconciledPerFormatTotals(t *testing.T) {
	report := validCoverageFixture("beacon")
	if err := Validate(report); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}

	missing := report
	missing.Formats = append([]Format(nil), report.Formats[1:]...)
	if err := Validate(missing); err == nil {
		t.Fatal("missing required format family was accepted")
	}

	unknown := report
	unknown.Formats = append([]Format(nil), report.Formats...)
	unknown.Formats[0].ID = "beacon.unknown"
	if err := Validate(unknown); err == nil {
		t.Fatal("unknown format family was accepted")
	}

	drift := report
	drift.Formats = append([]Format(nil), report.Formats...)
	drift.Formats[len(drift.Formats)-1].RecordsImported++
	if err := Validate(drift); err == nil {
		t.Fatal("per-format aggregate drift was accepted")
	}
}

func TestValidateRetainsUnclassifiedFilesAsBlockingEvidence(t *testing.T) {
	report := validCoverageFixture("rrsi")
	row := Format{ID: UnclassifiedFormat, Files: 1, SourceBytes: 3, FileSetSHA256: strings.Repeat("e", 64)}
	report.Formats = append(report.Formats, row)
	report.FilesScanned++
	report.SourceBytes += row.SourceBytes
	report.UnclassifiedFiles++
	if err := Validate(report); err != nil {
		t.Fatalf("unclassified source should be reportable for fail-closed assessment: %v", err)
	}
	if err := Validate(validCoverageFixture("rrsi")); err != nil {
		t.Fatalf("zero-unclassified fixture rejected: %v", err)
	}
}

func TestValidateCrossFilePrivacyBindingKeepsMismatchesVisible(t *testing.T) {
	report := validCoverageFixture("mancode")
	setMancodePrivacyFileCounts(&report, 1, 1)
	check := CrossFileCheck{
		SchemaVersion: MancodePrivacyBindingSchemaVersion, Relationship: "mancode_privacy_policy_exclusions",
		PairIDSHA256: strings.Repeat("d", 64), PolicyFileCount: 1, PolicyPresent: true, PolicyValid: true,
		PolicySourceSetSHA256: strings.Repeat("e", 64), ExclusionsPresent: true, ExclusionsValid: true,
		ExclusionsFileCount:       1,
		ExclusionsSourceSetSHA256: strings.Repeat("f", 64), PolicyRevision: 3,
		ExpectedExclusionsRevision: 2, ActualExclusionsRevision: 2,
		WorkspaceBindingVerified: true, RevisionBindingVerified: true, DigestBindingVerified: true,
		Status: "verified", Issues: []string{}, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
	}
	report.CrossFileChecks = []CrossFileCheck{check}
	if err := Validate(report); err != nil {
		t.Fatalf("verified privacy binding rejected: %v", err)
	}

	conflict := report
	conflict.CrossFileChecks = append([]CrossFileCheck(nil), report.CrossFileChecks...)
	conflict.CrossFileChecks[0].RevisionBindingVerified = false
	conflict.CrossFileChecks[0].ActualExclusionsRevision = 4
	conflict.CrossFileChecks[0].Status = "conflicting"
	conflict.CrossFileChecks[0].Issues = []string{"revision_mismatch"}
	if err := Validate(conflict); err != nil {
		t.Fatalf("explicitly conflicting privacy binding should remain reportable: %v", err)
	}

	contradictory := conflict
	contradictory.CrossFileChecks = append([]CrossFileCheck(nil), conflict.CrossFileChecks...)
	contradictory.CrossFileChecks[0].RevisionBindingVerified = true
	if err := Validate(contradictory); err == nil {
		t.Fatal("privacy binding with contradictory verified flag and mismatch issue was accepted")
	}

	wrongProduct := validCoverageFixture("beacon")
	wrongProduct.CrossFileChecks = []CrossFileCheck{check}
	if err := Validate(wrongProduct); err == nil {
		t.Fatal("Mancode privacy binding was accepted for another migration product")
	}
}

func TestValidateRequiresMancodePrivacyChecksToCoverClassifiedFiles(t *testing.T) {
	report := validCoverageFixture("mancode")
	setMancodePrivacyFileCounts(&report, 1, 0)
	if err := Validate(report); err == nil {
		t.Fatal("Mancode privacy-policy file without a cross-file check was accepted")
	}

	check := CrossFileCheck{
		SchemaVersion: MancodePrivacyBindingSchemaVersion, Relationship: "mancode_privacy_policy_exclusions",
		PairIDSHA256: strings.Repeat("d", 64), PolicyFileCount: 1, PolicyPresent: true,
		PolicySourceSetSHA256: strings.Repeat("e", 64), ExclusionsFileCount: 0,
		Authority: "local_pending_review", HumanReviewRequired: true,
		Status: "incomplete", Issues: []string{"exclusions_missing"},
	}
	report.CrossFileChecks = []CrossFileCheck{check}
	if err := Validate(report); err != nil {
		t.Fatalf("explicitly incomplete privacy-pair check should cover its source files: %v", err)
	}

	missingCount := report
	missingCount.CrossFileChecks = append([]CrossFileCheck(nil), report.CrossFileChecks...)
	missingCount.CrossFileChecks[0].PolicyFileCount = 0
	if err := Validate(missingCount); err == nil {
		t.Fatal("cross-file check omitting its policy source count was accepted")
	}
}

func setMancodePrivacyFileCounts(report *Report, policy, exclusions int) {
	for index := range report.Formats {
		row := &report.Formats[index]
		row.Files, row.SourceBytes, row.RecordsFound, row.RecordsImported = 0, 0, 0, 0
		row.CredentialExcludedRecords, row.RejectedRecords = 0, 0
		row.SnapshotFiles, row.JoinedFiles, row.UnsupportedFiles, row.UnconvertedFiles = 0, 0, 0, 0
		switch row.ID {
		case "mancode.privacy-policy":
			row.Files = policy
		case "mancode.privacy-exclusions":
			row.Files = exclusions
		}
	}
	report.FilesScanned = policy + exclusions
	report.SourceBytes, report.RecordsFound, report.RecordsImported = 0, 0, 0
	report.CredentialExcludedRecords, report.RejectedRecords = 0, 0
	report.SnapshotFiles, report.JoinedFiles, report.UnsupportedFiles = 0, 0, 0
	report.UnconvertedFiles, report.UnclassifiedFiles = 0, 0
}

func validCoverageFixture(product string) Report {
	report := Report{
		SchemaVersion: SchemaVersion, Product: product, SourceFingerprint: strings.Repeat("a", 64),
		SnapshotSHA256: strings.Repeat("b", 64), FilesScanned: 1, SourceBytes: 4,
		RecordsFound: 1, RecordsImported: 1, SnapshotFiles: 1,
	}
	for i, id := range RequiredFormats(product) {
		row := Format{ID: id, FileSetSHA256: strings.Repeat("c", 64)}
		if i == 0 {
			row.Files, row.SourceBytes, row.RecordsFound, row.RecordsImported, row.SnapshotFiles = 1, 4, 1, 1, 1
		}
		report.Formats = append(report.Formats, row)
	}
	return report
}
