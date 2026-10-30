package migrationcoverage

import (
	"encoding/json"
	"sort"
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

func TestValidateMancodeLegacyWorkflowBindingsReconcileAndSurfaceConflicts(t *testing.T) {
	report := validCoverageFixture("mancode")
	setMancodePrivacyFileCounts(&report, 0, 0)
	componentFormats := []string{
		"mancode.legacy-workflow-metadata", "mancode.legacy-workflow-requirements",
		"mancode.legacy-workflow-review", "mancode.legacy-workflow-verification", "mancode.legacy-workflow-plan",
	}
	check := MancodeLegacyWorkflowCheck{
		SchemaVersion: MancodeLegacyWorkflowBindingSchemaVersion, Relationship: "mancode_legacy_workflow_ledgers",
		WorkflowIDSHA256:  strings.Repeat("d", 64),
		MetadataFileCount: 1, MetadataValid: true, MetadataSourceSetSHA256: strings.Repeat("e", 64),
		RequirementsFileCount: 1, RequirementsValid: true, RequirementsSourceSetSHA256: strings.Repeat("f", 64),
		ReviewFileCount: 1, ReviewValid: true, ReviewSourceSetSHA256: strings.Repeat("a", 64),
		VerificationFileCount: 1, VerificationValid: true, VerificationSourceSetSHA256: strings.Repeat("b", 64),
		PlanFileCount: 1, PlanValid: true, PlanSourceSetSHA256: strings.Repeat("c", 64),
		MetadataRequirementsDigestPresent: true, MetadataRequirementsDigestBindingVerified: true,
		WorkflowPathBindingVerified: true, RequirementsDigestBindingVerified: true,
		AcceptanceCriteriaBindingVerified: true, PlanVersionBindingVerified: true,
		ReviewRemediationBindingVerified: true, PlanPresenceVerified: true,
		Status: "verified", Issues: []string{}, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
	}
	for _, id := range componentFormats {
		report.Formats = append(report.Formats, Format{ID: id, Files: 1, FileSetSHA256: strings.Repeat("c", 64)})
	}
	sort.Slice(report.Formats, func(i, j int) bool { return report.Formats[i].ID < report.Formats[j].ID })
	report.FilesScanned = len(componentFormats)
	report.MancodeLegacyWorkflowChecks = []MancodeLegacyWorkflowCheck{check}
	if err := Validate(report); err != nil {
		t.Fatalf("fully bound legacy workflow check rejected: %v", err)
	}
	legacyJSON, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	for _, optionalField := range []string{
		"metadata_requirements_status_present", "metadata_requirements_status_binding_verified",
		"metadata_verification_status_present", "metadata_verification_status_binding_verified",
	} {
		if strings.Contains(string(legacyJSON), optionalField) {
			t.Fatalf("absent optional status field changed the legacy v1 JSON shape: %s", legacyJSON)
		}
	}
	var oldCheck MancodeLegacyWorkflowCheck
	if err := json.Unmarshal(legacyJSON, &oldCheck); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMancodeLegacyWorkflowChecks("mancode", []MancodeLegacyWorkflowCheck{oldCheck}); err != nil {
		t.Fatalf("previously verified v1 binding without optional status fields was rejected: %v", err)
	}

	conflict := report
	conflict.MancodeLegacyWorkflowChecks = append([]MancodeLegacyWorkflowCheck(nil), report.MancodeLegacyWorkflowChecks...)
	conflict.MancodeLegacyWorkflowChecks[0].Status = "conflicting"
	conflict.MancodeLegacyWorkflowChecks[0].RequirementsDigestBindingVerified = false
	conflict.MancodeLegacyWorkflowChecks[0].Issues = []string{"requirements_digest_mismatch"}
	if err := Validate(conflict); err != nil {
		t.Fatalf("explicitly conflicting workflow binding should remain reportable: %v", err)
	}

	contradictory := conflict
	contradictory.MancodeLegacyWorkflowChecks = append([]MancodeLegacyWorkflowCheck(nil), conflict.MancodeLegacyWorkflowChecks...)
	contradictory.MancodeLegacyWorkflowChecks[0].RequirementsDigestBindingVerified = true
	if err := Validate(contradictory); err == nil {
		t.Fatal("legacy workflow digest conflict with a verified flag was accepted")
	}

	uncovered := report
	for index := range uncovered.Formats {
		if uncovered.Formats[index].ID == componentFormats[0] {
			uncovered.Formats[index].Files = 0
		}
	}
	if err := Validate(uncovered); err == nil {
		t.Fatal("legacy workflow check omitting its metadata source file was accepted")
	}
}

func TestValidateMancodeTeamPolicyWorkspaceBindingsReconcileAndSurfaceConflicts(t *testing.T) {
	report := validCoverageFixture("mancode")
	setMancodeTeamPolicyFileCounts(&report, 1, 1)
	check := MancodeTeamPolicyCheck{
		SchemaVersion: MancodeTeamPolicyBindingSchemaVersion, Relationship: "mancode_team_policy_project_config",
		PairIDSHA256:        strings.Repeat("d", 64),
		TeamPolicyFileCount: 1, TeamPolicyValid: true, TeamPolicySourceSetSHA256: strings.Repeat("e", 64),
		ProjectConfigFileCount: 1, ProjectConfigValid: true, ProjectConfigSourceSetSHA256: strings.Repeat("f", 64),
		WorkspaceBindingVerified: true, Status: "verified", Issues: []string{}, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
	}
	report.MancodeTeamPolicyChecks = []MancodeTeamPolicyCheck{check}
	if err := Validate(report); err != nil {
		t.Fatalf("verified team-policy workspace binding rejected: %v", err)
	}

	conflict := report
	conflict.MancodeTeamPolicyChecks = append([]MancodeTeamPolicyCheck(nil), report.MancodeTeamPolicyChecks...)
	conflict.MancodeTeamPolicyChecks[0].WorkspaceBindingVerified = false
	conflict.MancodeTeamPolicyChecks[0].Status = "conflicting"
	conflict.MancodeTeamPolicyChecks[0].Issues = []string{"workspace_mismatch"}
	if err := Validate(conflict); err != nil {
		t.Fatalf("explicit team-policy workspace mismatch should remain reportable: %v", err)
	}

	contradictory := conflict
	contradictory.MancodeTeamPolicyChecks = append([]MancodeTeamPolicyCheck(nil), conflict.MancodeTeamPolicyChecks...)
	contradictory.MancodeTeamPolicyChecks[0].WorkspaceBindingVerified = true
	if err := Validate(contradictory); err == nil {
		t.Fatal("team-policy workspace mismatch with a verified flag was accepted")
	}

	incomplete := report
	setMancodeTeamPolicyFileCounts(&incomplete, 1, 0)
	incomplete.MancodeTeamPolicyChecks = []MancodeTeamPolicyCheck{{
		SchemaVersion: MancodeTeamPolicyBindingSchemaVersion, Relationship: "mancode_team_policy_project_config",
		PairIDSHA256:        strings.Repeat("d", 64),
		TeamPolicyFileCount: 1, TeamPolicyValid: true, TeamPolicySourceSetSHA256: strings.Repeat("e", 64),
		Status: "incomplete", Issues: []string{"project_config_missing"}, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
	}}
	if err := Validate(incomplete); err != nil {
		t.Fatalf("missing sibling project config should remain an explicit incomplete binding: %v", err)
	}

	uncovered := report
	setMancodeTeamPolicyFileCounts(&uncovered, 1, 0)
	uncovered.MancodeTeamPolicyChecks = nil
	if err := Validate(uncovered); err == nil {
		t.Fatal("coverage omitted a team-policy binding check for a classified policy file")
	}
}

func TestValidateMancodeWorkflowArchiveBindingsReconcileAndSurfaceConflicts(t *testing.T) {
	report := validCoverageFixture("mancode")
	setMancodePrivacyFileCounts(&report, 0, 0)
	report.Formats = append(report.Formats, Format{
		ID: "mancode.workflow-archive", Files: 2, RecordsFound: 2, RecordsImported: 2, SnapshotFiles: 2,
		FileSetSHA256: strings.Repeat("c", 64),
	})
	sort.Slice(report.Formats, func(i, j int) bool { return report.Formats[i].ID < report.Formats[j].ID })
	report.FilesScanned, report.RecordsFound, report.RecordsImported, report.SnapshotFiles = 2, 2, 2, 2
	check := MancodeWorkflowArchiveCheck{
		SchemaVersion: MancodeWorkflowArchiveBindingSchemaVersion, Relationship: "mancode_workflow_archive_artifacts",
		WorkflowIDSHA256: strings.Repeat("d", 64), ArchiveIDSHA256: strings.Repeat("e", 64),
		ArchiveFileCount: 2, ArchiveSourceSetSHA256: strings.Repeat("a", 64),
		ManifestFileCount: 1, ManifestValid: true, ManifestSourceSetSHA256: strings.Repeat("b", 64),
		RequirementsFileCount: 1, RequirementsValid: true, RequirementsSourceSetSHA256: strings.Repeat("f", 64),
		PlanFileCount: 0, PlanValid: false, OtherFileCount: 0,
		ArchiveDigestBindingVerified: true, RequirementsDigestBindingVerified: true, PlanDigestBindingVerified: true,
		Status: "verified", Issues: []string{}, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
	}
	report.MancodeWorkflowArchiveChecks = []MancodeWorkflowArchiveCheck{check}
	if err := Validate(report); err != nil {
		t.Fatalf("verified archive binding rejected: %v", err)
	}

	conflict := report
	conflict.MancodeWorkflowArchiveChecks = append([]MancodeWorkflowArchiveCheck(nil), report.MancodeWorkflowArchiveChecks...)
	conflict.MancodeWorkflowArchiveChecks[0].ArchiveDigestBindingVerified = false
	conflict.MancodeWorkflowArchiveChecks[0].Status = "conflicting"
	conflict.MancodeWorkflowArchiveChecks[0].Issues = []string{"archive_digest_mismatch"}
	if err := Validate(conflict); err != nil {
		t.Fatalf("explicit archive digest conflict should remain reportable: %v", err)
	}

	contradictory := conflict
	contradictory.MancodeWorkflowArchiveChecks = append([]MancodeWorkflowArchiveCheck(nil), conflict.MancodeWorkflowArchiveChecks...)
	contradictory.MancodeWorkflowArchiveChecks[0].ArchiveDigestBindingVerified = true
	if err := Validate(contradictory); err == nil {
		t.Fatal("archive digest conflict with a verified flag was accepted")
	}

	uncovered := report
	uncovered.MancodeWorkflowArchiveChecks = nil
	if err := Validate(uncovered); err == nil {
		t.Fatal("coverage omitted a check for classified workflow archive files")
	}

	unsafe := report
	unsafe.MancodeWorkflowArchiveChecks = append([]MancodeWorkflowArchiveCheck(nil), report.MancodeWorkflowArchiveChecks...)
	unsafe.MancodeWorkflowArchiveChecks[0].ExecutionAuthorized = true
	if err := Validate(unsafe); err == nil {
		t.Fatal("workflow archive migration check granted execution authority")
	}
}

func setMancodeTeamPolicyFileCounts(report *Report, teamPolicy, projectConfig int) {
	for index := range report.Formats {
		row := &report.Formats[index]
		row.Files, row.SourceBytes, row.RecordsFound, row.RecordsImported = 0, 0, 0, 0
		row.CredentialExcludedRecords, row.RejectedRecords = 0, 0
		row.SnapshotFiles, row.JoinedFiles, row.UnsupportedFiles, row.UnconvertedFiles = 0, 0, 0, 0
		switch row.ID {
		case "mancode.team-policy":
			row.Files, row.RecordsFound, row.RecordsImported, row.SnapshotFiles = teamPolicy, teamPolicy, teamPolicy, teamPolicy
		case "mancode.project-config":
			row.Files, row.RecordsFound, row.RecordsImported, row.SnapshotFiles = projectConfig, projectConfig, projectConfig, projectConfig
		}
	}
	files := teamPolicy + projectConfig
	report.FilesScanned, report.RecordsFound, report.RecordsImported, report.SnapshotFiles = files, files, files, files
	report.SourceBytes, report.CredentialExcludedRecords, report.RejectedRecords = 0, 0, 0
	report.JoinedFiles, report.UnsupportedFiles, report.UnconvertedFiles, report.UnclassifiedFiles = 0, 0, 0, 0
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
