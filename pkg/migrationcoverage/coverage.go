package migrationcoverage

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
)

const SchemaVersion = "threadforge.migration-format-coverage.v1"

const UnclassifiedFormat = "unclassified"

const MancodePrivacyBindingSchemaVersion = "threadforge.mancode-privacy-binding-check.v1"
const MancodeLegacyWorkflowBindingSchemaVersion = "threadforge.mancode-legacy-workflow-binding.v1"
const MancodeTeamPolicyBindingSchemaVersion = "threadforge.mancode-team-policy-binding-check.v1"

const (
	maxCoverageFiles   = 10000
	maxCoverageRecords = 100000
	maxCoverageBytes   = int64(1 << 30)
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Report struct {
	SchemaVersion               string                       `json:"schema_version"`
	Product                     string                       `json:"product"`
	SourceFingerprint           string                       `json:"source_fingerprint"`
	SnapshotSHA256              string                       `json:"snapshot_sha256"`
	FilesScanned                int                          `json:"files_scanned"`
	SourceBytes                 int64                        `json:"source_bytes"`
	RecordsFound                int                          `json:"records_found"`
	RecordsImported             int                          `json:"records_imported"`
	CredentialExcludedRecords   int                          `json:"credential_excluded_records"`
	RejectedRecords             int                          `json:"rejected_records"`
	SnapshotFiles               int                          `json:"snapshot_files"`
	JoinedFiles                 int                          `json:"joined_files"`
	UnsupportedFiles            int                          `json:"unsupported_files"`
	UnconvertedFiles            int                          `json:"unconverted_files"`
	UnclassifiedFiles           int                          `json:"unclassified_files"`
	CrossFileChecks             []CrossFileCheck             `json:"cross_file_checks,omitempty"`
	MancodeLegacyWorkflowChecks []MancodeLegacyWorkflowCheck `json:"mancode_legacy_workflow_checks,omitempty"`
	MancodeTeamPolicyChecks     []MancodeTeamPolicyCheck     `json:"mancode_team_policy_checks,omitempty"`
	Formats                     []Format                     `json:"formats"`
}

// MancodeLegacyWorkflowCheck records path-redacted binding evidence for the
// pre-v3 workflow files. It is continuity evidence only, never execution authority.
type MancodeLegacyWorkflowCheck struct {
	SchemaVersion                             string                           `json:"schema_version"`
	Relationship                              string                           `json:"relationship"`
	WorkflowIDSHA256                          string                           `json:"workflow_id_sha256"`
	MetadataFileCount                         int                              `json:"metadata_file_count"`
	MetadataValid                             bool                             `json:"metadata_valid"`
	MetadataSourceSetSHA256                   string                           `json:"metadata_source_set_sha256,omitempty"`
	RequirementsFileCount                     int                              `json:"requirements_file_count"`
	RequirementsValid                         bool                             `json:"requirements_valid"`
	RequirementsSourceSetSHA256               string                           `json:"requirements_source_set_sha256,omitempty"`
	ReviewFileCount                           int                              `json:"review_file_count"`
	ReviewValid                               bool                             `json:"review_valid"`
	ReviewSourceSetSHA256                     string                           `json:"review_source_set_sha256,omitempty"`
	VerificationFileCount                     int                              `json:"verification_file_count"`
	VerificationValid                         bool                             `json:"verification_valid"`
	VerificationSourceSetSHA256               string                           `json:"verification_source_set_sha256,omitempty"`
	PlanFileCount                             int                              `json:"plan_file_count"`
	PlanValid                                 bool                             `json:"plan_valid"`
	PlanSourceSetSHA256                       string                           `json:"plan_source_set_sha256,omitempty"`
	MetadataPlanVersionDefaulted              bool                             `json:"metadata_plan_version_defaulted"`
	MetadataRequirementsDigestPresent         bool                             `json:"metadata_requirements_digest_present"`
	MetadataRequirementsDigestBindingVerified bool                             `json:"metadata_requirements_digest_binding_verified"`
	MetadataRequirementsStatusPresent         bool                             `json:"metadata_requirements_status_present,omitempty"`
	MetadataRequirementsStatusBindingVerified bool                             `json:"metadata_requirements_status_binding_verified,omitempty"`
	MetadataVerificationStatusPresent         bool                             `json:"metadata_verification_status_present,omitempty"`
	MetadataVerificationStatusBindingVerified bool                             `json:"metadata_verification_status_binding_verified,omitempty"`
	WorkflowPathBindingVerified               bool                             `json:"workflow_path_binding_verified"`
	RequirementsDigestBindingVerified         bool                             `json:"requirements_digest_binding_verified"`
	AcceptanceCriteriaBindingVerified         bool                             `json:"acceptance_criteria_binding_verified"`
	PlanVersionBindingVerified                bool                             `json:"plan_version_binding_verified"`
	ReviewRemediationBindingVerified          bool                             `json:"review_remediation_binding_verified"`
	PlanPresenceVerified                      bool                             `json:"plan_presence_verified"`
	PlanContentBindingVerified                bool                             `json:"plan_content_binding_verified"`
	ArtifactReferences                        *MancodeLegacyArtifactReferences `json:"artifact_references,omitempty"`
	Status                                    string                           `json:"status"`
	Issues                                    []string                         `json:"issues"`
	Authority                                 string                           `json:"authority"`
	HumanReviewRequired                       bool                             `json:"human_review_required"`
	ExecutionAuthorized                       bool                             `json:"execution_authorized"`
}

// MancodeLegacyArtifactReferences binds review/evidence references to
// path-redacted source-file commitments. It carries no artifact text or raw path.
type MancodeLegacyArtifactReferences struct {
	ReferenceCount  int                           `json:"reference_count"`
	FoundCount      int                           `json:"found_count"`
	ImportedCount   int                           `json:"imported_count"`
	Targets         []MancodeLegacyArtifactTarget `json:"targets"`
	BindingVerified bool                          `json:"binding_verified"`
}

type MancodeLegacyArtifactTarget struct {
	PathSHA256   string `json:"path_sha256"`
	SourceSHA256 string `json:"source_sha256"`
	Imported     bool   `json:"imported"`
}

// CrossFileCheck records a bounded, path-redacted relationship check between
// source files. It is review evidence only and can never activate imported data.
type CrossFileCheck struct {
	SchemaVersion              string   `json:"schema_version"`
	Relationship               string   `json:"relationship"`
	PairIDSHA256               string   `json:"pair_id_sha256"`
	PolicyFileCount            int      `json:"policy_file_count"`
	PolicyPresent              bool     `json:"policy_present"`
	PolicyValid                bool     `json:"policy_valid"`
	PolicySourceSetSHA256      string   `json:"policy_source_set_sha256,omitempty"`
	ExclusionsFileCount        int      `json:"exclusions_file_count"`
	ExclusionsPresent          bool     `json:"exclusions_present"`
	ExclusionsValid            bool     `json:"exclusions_valid"`
	ExclusionsSourceSetSHA256  string   `json:"exclusions_source_set_sha256,omitempty"`
	PolicyRevision             int64    `json:"policy_revision"`
	ExpectedExclusionsRevision int64    `json:"expected_exclusions_revision"`
	ActualExclusionsRevision   int64    `json:"actual_exclusions_revision"`
	WorkspaceBindingVerified   bool     `json:"workspace_binding_verified"`
	RevisionBindingVerified    bool     `json:"revision_binding_verified"`
	DigestBindingVerified      bool     `json:"digest_binding_verified"`
	Status                     string   `json:"status"`
	Issues                     []string `json:"issues"`
	Authority                  string   `json:"authority"`
	HumanReviewRequired        bool     `json:"human_review_required"`
	ExecutionAuthorized        bool     `json:"execution_authorized"`
}

// MancodeTeamPolicyCheck records a path-redacted binding between a V3 team
// policy and the shared project config that owns its workspace identity.
// The result is migration evidence only and never activates source policy.
type MancodeTeamPolicyCheck struct {
	SchemaVersion                string   `json:"schema_version"`
	Relationship                 string   `json:"relationship"`
	PairIDSHA256                 string   `json:"pair_id_sha256"`
	TeamPolicyFileCount          int      `json:"team_policy_file_count"`
	TeamPolicyValid              bool     `json:"team_policy_valid"`
	TeamPolicySourceSetSHA256    string   `json:"team_policy_source_set_sha256,omitempty"`
	ProjectConfigFileCount       int      `json:"project_config_file_count"`
	ProjectConfigValid           bool     `json:"project_config_valid"`
	ProjectConfigSourceSetSHA256 string   `json:"project_config_source_set_sha256,omitempty"`
	WorkspaceBindingVerified     bool     `json:"workspace_binding_verified"`
	Status                       string   `json:"status"`
	Issues                       []string `json:"issues"`
	Authority                    string   `json:"authority"`
	HumanReviewRequired          bool     `json:"human_review_required"`
	ExecutionAuthorized          bool     `json:"execution_authorized"`
}

type Format struct {
	ID                        string `json:"id"`
	Files                     int    `json:"files"`
	SourceBytes               int64  `json:"source_bytes"`
	RecordsFound              int    `json:"records_found"`
	RecordsImported           int    `json:"records_imported"`
	CredentialExcludedRecords int    `json:"credential_excluded_records"`
	RejectedRecords           int    `json:"rejected_records"`
	SnapshotFiles             int    `json:"snapshot_files"`
	JoinedFiles               int    `json:"joined_files"`
	UnsupportedFiles          int    `json:"unsupported_files"`
	UnconvertedFiles          int    `json:"unconverted_files"`
	FileSetSHA256             string `json:"file_set_sha256"`
}

var requiredFormats = map[string][]string{
	"beacon": {
		"beacon.runtime-events", "beacon.inventory-events", "beacon.learning-database",
		"beacon.opencode-plugin", "beacon.hook-log", "beacon.endpoint-claude",
		"beacon.endpoint-qwen", "beacon.endpoint-factory", "beacon.endpoint-cursor",
		"beacon.endpoint-codex-hooks", "beacon.endpoint-codex-otel", "beacon.endpoint-gemini",
		"beacon.endpoint-antigravity", "beacon.endpoint-openhands", "beacon.endpoint-kiro",
		"beacon.endpoint-kimi-code", "beacon.endpoint-muse-hooks", "beacon.endpoint-muse-settings",
		"beacon.endpoint-generic",
	},
	"mancode": {
		"mancode.legacy-state", "mancode.legacy-config", "mancode.project-config",
		"mancode.schema-manifest", "mancode.actor-identity", "mancode.team-policy",
		"mancode.project-fact", "mancode.actor-profile", "mancode.team-event",
		"mancode.team-claim", "mancode.team-handoff", "mancode.team-head-fence",
		"mancode.operation-audit", "mancode.decision-record", "mancode.project-glossary",
		"mancode.privacy-exclusions", "mancode.privacy-policy", "mancode.project-policy",
		"mancode.git-ref-team-authority",
		"mancode.project-record", "mancode.workflow-checkpoint",
		"mancode.workflow-metadata", "mancode.workflow-requirements", "mancode.workflow-review",
		"mancode.workflow-verification", "mancode.workflow-plan", "mancode.workflow-document",
		"mancode.progress-projection",
		"mancode.workflow-state",
	},
	"anyjev": {
		"anyjev.head-study", "anyjev.zero-label-distillation", "anyjev.distillation-results",
		"anyjev.latency-markdown", "anyjev.typed-markdown", "anyjev.classification-markdown",
		"anyjev.demo-jev-mode", "anyjev.demo-games", "anyjev.universal-results",
		"anyjev.per-item-dumps", "anyjev.paraphrase-order", "anyjev.exit-cascade",
		"anyjev.exit-artifact", "anyjev.native-maze", "anyjev.exit-labels",
		"anyjev.exit-jev-mode", "anyjev.exit-latency", "anyjev.exit-depth",
		"anyjev.nanojev-maze", "anyjev.latency-benchmark-json", "anyjev.typed-benchmark-json",
		"anyjev.classification-benchmark-json", "anyjev.distillation-cache-npz",
		"anyjev.heads-artifact-json", "anyjev.typed-v01-results", "anyjev.typed-small-results",
		"anyjev.typed-diagnostic-results", "anyjev.dump-summary-results",
		"anyjev.typed-dump-summary-results", "anyjev.synthetic-state-datasets", "anyjev.unconverted-label-data",
	},
	"rrsi": {
		"rrsi.evaluation-json", "rrsi.calibration-json", "rrsi.frontier-json",
		"rrsi.history-jsonl", "rrsi.attribution-jsonl", "rrsi.decisions-json",
		"rrsi.proposal-json", "rrsi.coding-trial-result", "rrsi.workspace-scores",
		"rrsi.engineering-meta", "rrsi.engineering-verdict", "rrsi.trajectory", "rrsi.raw-trial-artifact",
	},
}

// RequiredFormats returns a copy of the format catalog used by migration and cutover checks.
func RequiredFormats(product string) []string {
	formats := append([]string(nil), requiredFormats[product]...)
	sort.Strings(formats)
	return formats
}

// Validate checks the privacy-safe format ledger and reconciles every dimension to its totals.
func Validate(report Report) error {
	if report.SchemaVersion != SchemaVersion {
		return errors.New("unsupported migration format-coverage schema")
	}
	if len(requiredFormats[report.Product]) == 0 || !digestPattern.MatchString(report.SourceFingerprint) ||
		!digestPattern.MatchString(report.SnapshotSHA256) {
		return errors.New("migration format-coverage product or root digest is invalid")
	}
	if err := ValidateCrossFileChecks(report.Product, report.CrossFileChecks); err != nil {
		return err
	}
	if err := ValidateMancodeLegacyWorkflowChecks(report.Product, report.MancodeLegacyWorkflowChecks); err != nil {
		return err
	}
	if err := ValidateMancodeTeamPolicyChecks(report.Product, report.MancodeTeamPolicyChecks); err != nil {
		return err
	}
	for name, value := range map[string]int{
		"files_scanned": report.FilesScanned, "records_found": report.RecordsFound,
		"records_imported": report.RecordsImported, "credential_excluded_records": report.CredentialExcludedRecords,
		"rejected_records": report.RejectedRecords, "snapshot_files": report.SnapshotFiles,
		"joined_files": report.JoinedFiles, "unsupported_files": report.UnsupportedFiles,
		"unconverted_files": report.UnconvertedFiles, "unclassified_files": report.UnclassifiedFiles,
	} {
		if value < 0 {
			return fmt.Errorf("migration format-coverage %s is negative", name)
		}
	}
	if report.FilesScanned > maxCoverageFiles || report.SnapshotFiles > maxCoverageFiles || report.JoinedFiles > maxCoverageFiles ||
		report.UnsupportedFiles > maxCoverageFiles || report.UnconvertedFiles > maxCoverageFiles || report.UnclassifiedFiles > maxCoverageFiles ||
		report.RecordsFound > maxCoverageRecords || report.RecordsImported > maxCoverageRecords ||
		report.CredentialExcludedRecords > maxCoverageRecords || report.RejectedRecords > maxCoverageRecords ||
		report.SourceBytes < 0 || report.SourceBytes > maxCoverageBytes || len(report.Formats) > 64 ||
		report.RecordsFound != report.RecordsImported+report.CredentialExcludedRecords+report.RejectedRecords {
		return errors.New("migration format-coverage record totals do not reconcile")
	}
	expected := make(map[string]bool, len(requiredFormats[report.Product])+1)
	optionalMancodeLegacyWorkflowFormats := map[string]bool{
		"mancode.legacy-workflow-metadata": true, "mancode.legacy-workflow-requirements": true,
		"mancode.legacy-workflow-review": true, "mancode.legacy-workflow-verification": true,
		"mancode.legacy-workflow-plan": true,
	}
	for _, id := range requiredFormats[report.Product] {
		expected[id] = true
	}
	if report.UnclassifiedFiles > 0 {
		expected[UnclassifiedFormat] = true
	}
	seen := make(map[string]bool, len(report.Formats))
	files, importedFiles, joinedFiles, unsupportedFiles, unconvertedFiles := 0, 0, 0, 0, 0
	found, imported, excluded, rejected := 0, 0, 0, 0
	var sourceBytes int64
	previous := ""
	for _, format := range report.Formats {
		if !expected[format.ID] && report.Product == "mancode" && optionalMancodeLegacyWorkflowFormats[format.ID] && format.Files > 0 {
			expected[format.ID] = true
		}
		if format.ID == "" || (previous != "" && format.ID <= previous) || seen[format.ID] || !expected[format.ID] {
			return fmt.Errorf("migration format-coverage has duplicate, unordered, or unknown format %q", format.ID)
		}
		previous, seen[format.ID] = format.ID, true
		if format.Files < 0 || format.Files > maxCoverageFiles || format.SourceBytes < 0 || format.SourceBytes > maxCoverageBytes ||
			format.RecordsFound < 0 || format.RecordsFound > maxCoverageRecords || format.RecordsImported < 0 ||
			format.RecordsImported > maxCoverageRecords ||
			format.CredentialExcludedRecords < 0 || format.RejectedRecords < 0 || format.SnapshotFiles < 0 ||
			format.JoinedFiles < 0 || format.UnsupportedFiles < 0 || format.SnapshotFiles > format.Files ||
			format.JoinedFiles > format.Files || format.UnsupportedFiles > format.Files ||
			format.UnconvertedFiles < 0 || format.UnconvertedFiles > format.Files ||
			format.CredentialExcludedRecords > maxCoverageRecords || format.RejectedRecords > maxCoverageRecords ||
			format.SnapshotFiles > maxCoverageFiles || format.JoinedFiles > maxCoverageFiles || format.UnsupportedFiles > maxCoverageFiles ||
			format.UnconvertedFiles > maxCoverageFiles ||
			format.RecordsFound != format.RecordsImported+format.CredentialExcludedRecords+format.RejectedRecords ||
			!digestPattern.MatchString(format.FileSetSHA256) {
			return fmt.Errorf("migration format-coverage entry %q is invalid", format.ID)
		}
		if format.Files == 0 && (format.SourceBytes != 0 || format.RecordsFound != 0 || format.RecordsImported != 0 ||
			format.CredentialExcludedRecords != 0 || format.RejectedRecords != 0 || format.SnapshotFiles != 0 ||
			format.JoinedFiles != 0 || format.UnsupportedFiles != 0 || format.UnconvertedFiles != 0) {
			return fmt.Errorf("empty migration format %q has nonzero totals", format.ID)
		}
		if format.ID == UnclassifiedFormat && format.Files == 0 {
			return errors.New("empty unclassified migration format is not allowed")
		}
		files += format.Files
		importedFiles += format.SnapshotFiles
		joinedFiles += format.JoinedFiles
		unsupportedFiles += format.UnsupportedFiles
		unconvertedFiles += format.UnconvertedFiles
		found += format.RecordsFound
		imported += format.RecordsImported
		excluded += format.CredentialExcludedRecords
		rejected += format.RejectedRecords
		sourceBytes += format.SourceBytes
	}
	for id := range expected {
		if !seen[id] {
			return fmt.Errorf("migration format-coverage is missing required format %q", id)
		}
	}
	if files != report.FilesScanned || importedFiles != report.SnapshotFiles || joinedFiles != report.JoinedFiles ||
		unsupportedFiles != report.UnsupportedFiles || found != report.RecordsFound || imported != report.RecordsImported ||
		unconvertedFiles != report.UnconvertedFiles ||
		excluded != report.CredentialExcludedRecords || rejected != report.RejectedRecords || sourceBytes != report.SourceBytes {
		return errors.New("migration format-coverage totals do not reconcile with format rows")
	}
	if (seen[UnclassifiedFormat] && (report.Formats[len(report.Formats)-1].ID != UnclassifiedFormat ||
		formatFiles(report.Formats, UnclassifiedFormat) != report.UnclassifiedFiles)) ||
		seen[UnclassifiedFormat] != (report.UnclassifiedFiles > 0) {
		return errors.New("migration format-coverage unclassified count does not reconcile")
	}
	if report.Product == "mancode" {
		var policyFiles, exclusionsFiles int
		for _, check := range report.CrossFileChecks {
			policyFiles += check.PolicyFileCount
			exclusionsFiles += check.ExclusionsFileCount
		}
		if policyFiles != formatFiles(report.Formats, "mancode.privacy-policy") ||
			exclusionsFiles != formatFiles(report.Formats, "mancode.privacy-exclusions") {
			return errors.New("Mancode privacy binding checks do not cover every policy and exclusions file")
		}
		var metadataFiles, requirementsFiles, reviewFiles, verificationFiles, planFiles int
		for _, check := range report.MancodeLegacyWorkflowChecks {
			metadataFiles += check.MetadataFileCount
			requirementsFiles += check.RequirementsFileCount
			reviewFiles += check.ReviewFileCount
			verificationFiles += check.VerificationFileCount
			planFiles += check.PlanFileCount
		}
		if metadataFiles != formatFiles(report.Formats, "mancode.legacy-workflow-metadata") ||
			requirementsFiles != formatFiles(report.Formats, "mancode.legacy-workflow-requirements") ||
			reviewFiles != formatFiles(report.Formats, "mancode.legacy-workflow-review") ||
			verificationFiles != formatFiles(report.Formats, "mancode.legacy-workflow-verification") ||
			planFiles != formatFiles(report.Formats, "mancode.legacy-workflow-plan") {
			return errors.New("Mancode legacy workflow checks do not cover every legacy workflow component")
		}
		var teamPolicyFiles, boundProjectConfigFiles int
		for _, check := range report.MancodeTeamPolicyChecks {
			teamPolicyFiles += check.TeamPolicyFileCount
			boundProjectConfigFiles += check.ProjectConfigFileCount
		}
		if teamPolicyFiles != formatFiles(report.Formats, "mancode.team-policy") ||
			boundProjectConfigFiles > formatFiles(report.Formats, "mancode.project-config") {
			return errors.New("Mancode team-policy checks do not reconcile with team-policy and project-config source files")
		}
	}
	return nil
}

// ValidateCrossFileChecks enforces the versioned Mancode privacy-pair check
// contract shared by migration manifests, coverage receipts, and cutover gates.
func ValidateCrossFileChecks(product string, checks []CrossFileCheck) error {
	if len(checks) > maxCoverageFiles || len(checks) > 0 && product != "mancode" {
		return errors.New("migration cross-file checks are unsupported for this product or exceed bounds")
	}
	previousPair := ""
	allowedIssues := map[string]bool{
		"policy_missing": true, "exclusions_missing": true,
		"policy_invalid": true, "exclusions_invalid": true,
		"policy_ambiguous": true, "exclusions_ambiguous": true,
		"workspace_mismatch": true, "revision_mismatch": true, "digest_mismatch": true,
		"legacy_unverified": true,
	}
	for _, check := range checks {
		if check.SchemaVersion != MancodePrivacyBindingSchemaVersion || check.Relationship != "mancode_privacy_policy_exclusions" ||
			!digestPattern.MatchString(check.PairIDSHA256) || previousPair != "" && check.PairIDSHA256 <= previousPair ||
			check.Authority != "local_pending_review" || !check.HumanReviewRequired || check.ExecutionAuthorized {
			return errors.New("migration Mancode privacy binding check identity or authority is invalid")
		}
		previousPair = check.PairIDSHA256
		if check.PolicyFileCount < 0 || check.PolicyFileCount > maxCoverageFiles ||
			check.ExclusionsFileCount < 0 || check.ExclusionsFileCount > maxCoverageFiles ||
			check.PolicyPresent != (check.PolicyFileCount > 0) ||
			check.ExclusionsPresent != (check.ExclusionsFileCount > 0) ||
			check.PolicyPresent != (check.PolicySourceSetSHA256 != "") ||
			check.ExclusionsPresent != (check.ExclusionsSourceSetSHA256 != "") ||
			check.PolicyPresent && !digestPattern.MatchString(check.PolicySourceSetSHA256) ||
			check.ExclusionsPresent && !digestPattern.MatchString(check.ExclusionsSourceSetSHA256) {
			return errors.New("migration Mancode privacy binding source commitments are invalid")
		}
		if check.PolicyValid && (!check.PolicyPresent || check.PolicyFileCount != 1 || check.PolicyRevision < 1 || check.ExpectedExclusionsRevision < 1) ||
			!check.PolicyValid && (check.PolicyRevision != 0 || check.ExpectedExclusionsRevision != 0) ||
			check.ExclusionsValid && (!check.ExclusionsPresent || check.ExclusionsFileCount != 1 || check.ActualExclusionsRevision < 1) ||
			!check.ExclusionsValid && check.ActualExclusionsRevision != 0 {
			return errors.New("migration Mancode privacy binding revisions or validity flags are inconsistent")
		}
		if (check.WorkspaceBindingVerified || check.RevisionBindingVerified || check.DigestBindingVerified) &&
			(!check.PolicyValid || !check.ExclusionsValid) {
			return errors.New("migration Mancode privacy binding claims verification without valid inputs")
		}
		if len(check.Issues) > 9 {
			return errors.New("migration Mancode privacy binding issue list exceeds bounds")
		}
		previousIssue := ""
		mismatch := false
		workspaceMismatch, revisionMismatch, digestMismatch := false, false, false
		for _, issue := range check.Issues {
			if !allowedIssues[issue] || issue <= previousIssue {
				return errors.New("migration Mancode privacy binding issue list is invalid")
			}
			previousIssue = issue
			switch issue {
			case "workspace_mismatch":
				workspaceMismatch = true
			case "revision_mismatch":
				revisionMismatch = true
			case "digest_mismatch":
				digestMismatch = true
			}
			if workspaceMismatch || revisionMismatch || digestMismatch {
				mismatch = true
			}
		}
		if check.PolicyValid && check.ExclusionsValid {
			if check.WorkspaceBindingVerified == workspaceMismatch || check.RevisionBindingVerified == revisionMismatch ||
				check.DigestBindingVerified == digestMismatch {
				return errors.New("migration Mancode privacy binding flags contradict mismatch issues")
			}
			if check.RevisionBindingVerified != (check.ExpectedExclusionsRevision == check.ActualExclusionsRevision) {
				return errors.New("migration Mancode privacy binding revision result contradicts the compared revisions")
			}
		} else if workspaceMismatch || revisionMismatch || digestMismatch || check.WorkspaceBindingVerified ||
			check.RevisionBindingVerified || check.DigestBindingVerified {
			return errors.New("migration Mancode privacy binding compares invalid inputs")
		}
		switch check.Status {
		case "verified":
			if !check.PolicyValid || !check.ExclusionsValid || !check.WorkspaceBindingVerified ||
				!check.RevisionBindingVerified || !check.DigestBindingVerified || len(check.Issues) != 0 {
				return errors.New("verified Mancode privacy binding is incomplete")
			}
		case "incomplete":
			if len(check.Issues) == 0 || mismatch {
				return errors.New("incomplete Mancode privacy binding has inconsistent issues")
			}
		case "conflicting":
			if len(check.Issues) == 0 || !mismatch {
				return errors.New("conflicting Mancode privacy binding has no binding mismatch")
			}
		default:
			return errors.New("migration Mancode privacy binding status is invalid")
		}
	}
	return nil
}

// ValidateMancodeLegacyWorkflowChecks validates the pre-v3 Mancode workflow
// binding ledger embedded in migration manifests and coverage receipts.
func ValidateMancodeLegacyWorkflowChecks(product string, checks []MancodeLegacyWorkflowCheck) error {
	if len(checks) > maxCoverageFiles || len(checks) > 0 && product != "mancode" {
		return errors.New("Mancode legacy workflow checks are unsupported for this product or exceed bounds")
	}
	allowedIssues := map[string]bool{
		"metadata_missing": true, "metadata_ambiguous": true, "metadata_invalid": true,
		"requirements_missing": true, "requirements_ambiguous": true, "requirements_invalid": true,
		"review_ambiguous": true, "review_invalid": true,
		"verification_missing": true, "verification_ambiguous": true, "verification_invalid": true,
		"plan_missing": true, "plan_ambiguous": true, "plan_invalid": true,
		"requirements_digest_mismatch": true, "acceptance_criteria_mismatch": true,
		"plan_version_mismatch": true, "remediation_round_mismatch": true,
		"metadata_requirements_digest_mismatch": true,
		"metadata_requirements_status_mismatch": true, "metadata_verification_status_mismatch": true,
		"referenced_artifact_missing": true, "referenced_artifact_unimported": true,
		"referenced_artifact_ambiguous": true, "referenced_artifact_unavailable": true,
		"legacy_unverified": true,
	}
	previousID := ""
	for _, check := range checks {
		if check.SchemaVersion != MancodeLegacyWorkflowBindingSchemaVersion ||
			check.Relationship != "mancode_legacy_workflow_ledgers" ||
			!digestPattern.MatchString(check.WorkflowIDSHA256) ||
			previousID != "" && check.WorkflowIDSHA256 <= previousID ||
			check.Authority != "local_pending_review" || !check.HumanReviewRequired || check.ExecutionAuthorized {
			return errors.New("Mancode legacy workflow check identity or authority is invalid")
		}
		previousID = check.WorkflowIDSHA256
		components := []struct {
			name   string
			count  int
			valid  bool
			digest string
		}{
			{"metadata", check.MetadataFileCount, check.MetadataValid, check.MetadataSourceSetSHA256},
			{"requirements", check.RequirementsFileCount, check.RequirementsValid, check.RequirementsSourceSetSHA256},
			{"review", check.ReviewFileCount, check.ReviewValid, check.ReviewSourceSetSHA256},
			{"verification", check.VerificationFileCount, check.VerificationValid, check.VerificationSourceSetSHA256},
			{"plan", check.PlanFileCount, check.PlanValid, check.PlanSourceSetSHA256},
		}
		for _, component := range components {
			if component.count < 0 || component.count > maxCoverageFiles ||
				(component.count == 0) != (component.digest == "") ||
				component.digest != "" && !digestPattern.MatchString(component.digest) ||
				component.valid && component.count != 1 {
				return fmt.Errorf("Mancode legacy workflow %s source commitment is invalid", component.name)
			}
		}
		if check.PlanContentBindingVerified ||
			(check.MetadataRequirementsDigestBindingVerified && (!check.MetadataRequirementsDigestPresent ||
				!check.MetadataValid || !check.RequirementsValid)) ||
			(check.MetadataRequirementsStatusPresent && !check.MetadataValid) ||
			(check.MetadataRequirementsStatusBindingVerified && (!check.MetadataRequirementsStatusPresent ||
				!check.MetadataValid || !check.RequirementsValid)) ||
			(check.MetadataVerificationStatusPresent && !check.MetadataValid) ||
			(check.MetadataVerificationStatusBindingVerified && (!check.MetadataVerificationStatusPresent ||
				!check.MetadataValid || !check.VerificationValid)) ||
			(check.RequirementsDigestBindingVerified && (!check.RequirementsValid || !check.VerificationValid)) ||
			(check.AcceptanceCriteriaBindingVerified && (!check.RequirementsValid || !check.VerificationValid)) ||
			(check.PlanVersionBindingVerified && (!check.VerificationValid || !check.PlanValid ||
				check.MetadataFileCount == 1 && !check.MetadataValid)) ||
			(check.ReviewRemediationBindingVerified && (!check.VerificationValid || check.ReviewFileCount > 0 && !check.ReviewValid)) ||
			(check.PlanPresenceVerified && (!check.PlanValid || check.PlanFileCount != 1)) {
			return errors.New("Mancode legacy workflow binding claims verification without valid inputs")
		}
		if check.ArtifactReferences != nil {
			references := check.ArtifactReferences
			if references.ReferenceCount < 1 || references.ReferenceCount > maxCoverageRecords ||
				references.FoundCount < 0 || references.FoundCount > references.ReferenceCount ||
				references.ImportedCount < 0 || references.ImportedCount > references.FoundCount ||
				len(references.Targets) != references.FoundCount || len(references.Targets) > maxCoverageFiles {
				return errors.New("Mancode legacy workflow artifact reference counts are invalid")
			}
			previousPath := ""
			importedTargets := 0
			for _, target := range references.Targets {
				if !digestPattern.MatchString(target.PathSHA256) || !digestPattern.MatchString(target.SourceSHA256) ||
					previousPath != "" && target.PathSHA256 <= previousPath {
					return errors.New("Mancode legacy workflow artifact target commitment is invalid")
				}
				previousPath = target.PathSHA256
				if target.Imported {
					importedTargets++
				}
			}
			if importedTargets != references.ImportedCount ||
				references.BindingVerified && (references.ReferenceCount != references.FoundCount ||
					references.FoundCount != references.ImportedCount) {
				return errors.New("Mancode legacy workflow artifact reference bindings are inconsistent")
			}
		} else if mancodeLegacyCheckHasIssue(check.Issues, "referenced_artifact_missing") ||
			mancodeLegacyCheckHasIssue(check.Issues, "referenced_artifact_unimported") ||
			mancodeLegacyCheckHasIssue(check.Issues, "referenced_artifact_ambiguous") ||
			mancodeLegacyCheckHasIssue(check.Issues, "referenced_artifact_unavailable") {
			return errors.New("Mancode legacy workflow artifact issue has no reference evidence")
		}
		if check.WorkflowPathBindingVerified && check.MetadataFileCount+check.RequirementsFileCount+
			check.ReviewFileCount+check.VerificationFileCount+check.PlanFileCount == 0 {
			return errors.New("Mancode legacy workflow path binding has no source files")
		}
		if len(check.Issues) > 30 {
			return errors.New("Mancode legacy workflow issue list exceeds bounds")
		}
		previousIssue := ""
		conflicting := false
		artifactMissing, artifactUnimported, artifactAmbiguous, artifactUnavailable := false, false, false, false
		for _, issue := range check.Issues {
			if !allowedIssues[issue] || issue <= previousIssue {
				return errors.New("Mancode legacy workflow issue list is invalid")
			}
			previousIssue = issue
			switch issue {
			case "referenced_artifact_missing":
				artifactMissing = true
			case "referenced_artifact_unimported":
				artifactUnimported = true
			case "referenced_artifact_ambiguous":
				artifactAmbiguous = true
				conflicting = true
			case "referenced_artifact_unavailable":
				artifactUnavailable = true
			case "requirements_digest_mismatch":
				conflicting = true
				if check.RequirementsDigestBindingVerified {
					return errors.New("Mancode legacy workflow digest mismatch contradicts its binding flag")
				}
			case "acceptance_criteria_mismatch":
				conflicting = true
				if check.AcceptanceCriteriaBindingVerified {
					return errors.New("Mancode legacy workflow criteria mismatch contradicts its binding flag")
				}
			case "plan_version_mismatch":
				conflicting = true
				if check.PlanVersionBindingVerified {
					return errors.New("Mancode legacy workflow plan mismatch contradicts its binding flag")
				}
			case "remediation_round_mismatch":
				conflicting = true
				if check.ReviewRemediationBindingVerified {
					return errors.New("Mancode legacy workflow remediation mismatch contradicts its binding flag")
				}
			case "metadata_requirements_digest_mismatch":
				conflicting = true
				if check.MetadataRequirementsDigestBindingVerified {
					return errors.New("Mancode metadata requirements digest conflict contradicts its binding flag")
				}
			case "metadata_requirements_status_mismatch":
				conflicting = true
				if !check.MetadataRequirementsStatusPresent || !check.MetadataValid || !check.RequirementsValid {
					return errors.New("Mancode metadata requirements status conflict lacks valid source inputs")
				}
				if check.MetadataRequirementsStatusBindingVerified {
					return errors.New("Mancode metadata requirements status conflict contradicts its binding flag")
				}
			case "metadata_verification_status_mismatch":
				conflicting = true
				if !check.MetadataVerificationStatusPresent || !check.MetadataValid || !check.VerificationValid {
					return errors.New("Mancode metadata verification status conflict lacks valid source inputs")
				}
				if check.MetadataVerificationStatusBindingVerified {
					return errors.New("Mancode metadata verification status conflict contradicts its binding flag")
				}
			}
		}
		if check.ArtifactReferences != nil {
			references := check.ArtifactReferences
			if (references.ReferenceCount > references.FoundCount) != (artifactMissing || artifactAmbiguous || artifactUnavailable) ||
				(references.FoundCount > references.ImportedCount) != artifactUnimported ||
				(references.BindingVerified != (references.ReferenceCount == references.FoundCount &&
					references.FoundCount == references.ImportedCount && !artifactMissing && !artifactUnimported && !artifactAmbiguous)) {
				return errors.New("Mancode legacy workflow artifact reference issues contradict their counts")
			}
		}
		switch check.Status {
		case "verified":
			if len(check.Issues) != 0 || !check.WorkflowPathBindingVerified || !check.MetadataValid ||
				!check.RequirementsValid || !check.VerificationValid || !check.PlanValid ||
				check.MetadataRequirementsDigestPresent && !check.MetadataRequirementsDigestBindingVerified ||
				check.MetadataRequirementsStatusPresent && !check.MetadataRequirementsStatusBindingVerified ||
				check.MetadataVerificationStatusPresent && !check.MetadataVerificationStatusBindingVerified ||
				!check.RequirementsDigestBindingVerified || !check.AcceptanceCriteriaBindingVerified ||
				!check.PlanVersionBindingVerified || !check.ReviewRemediationBindingVerified || !check.PlanPresenceVerified ||
				check.ArtifactReferences != nil && !check.ArtifactReferences.BindingVerified {
				return errors.New("verified Mancode legacy workflow check is incomplete")
			}
		case "incomplete":
			if len(check.Issues) == 0 || conflicting {
				return errors.New("incomplete Mancode legacy workflow check has inconsistent issues")
			}
		case "conflicting":
			if len(check.Issues) == 0 || !conflicting {
				return errors.New("conflicting Mancode legacy workflow check has no binding mismatch")
			}
		default:
			return errors.New("Mancode legacy workflow check status is invalid")
		}
	}
	return nil
}

func mancodeLegacyCheckHasIssue(issues []string, expected string) bool {
	for _, issue := range issues {
		if issue == expected {
			return true
		}
	}
	return false
}

// ValidateMancodeTeamPolicyChecks validates review-only V3 team-policy workspace
// bindings embedded in manifests and coverage receipts.
func ValidateMancodeTeamPolicyChecks(product string, checks []MancodeTeamPolicyCheck) error {
	if len(checks) > maxCoverageFiles || len(checks) > 0 && product != "mancode" {
		return errors.New("Mancode team-policy checks are unsupported for this product or exceed bounds")
	}
	allowedIssues := map[string]bool{
		"team_policy_missing": true, "team_policy_ambiguous": true, "team_policy_invalid": true,
		"project_config_missing": true, "project_config_ambiguous": true, "project_config_invalid": true,
		"workspace_mismatch": true, "legacy_unverified": true,
	}
	previousPair := ""
	for _, check := range checks {
		if check.SchemaVersion != MancodeTeamPolicyBindingSchemaVersion ||
			check.Relationship != "mancode_team_policy_project_config" ||
			!digestPattern.MatchString(check.PairIDSHA256) ||
			previousPair != "" && check.PairIDSHA256 <= previousPair ||
			check.Authority != "local_pending_review" || !check.HumanReviewRequired || check.ExecutionAuthorized {
			return errors.New("Mancode team-policy binding identity or authority is invalid")
		}
		previousPair = check.PairIDSHA256
		if check.TeamPolicyFileCount < 0 || check.TeamPolicyFileCount > maxCoverageFiles ||
			check.ProjectConfigFileCount < 0 || check.ProjectConfigFileCount > maxCoverageFiles ||
			(check.TeamPolicyFileCount == 0) != (check.TeamPolicySourceSetSHA256 == "") ||
			(check.ProjectConfigFileCount == 0) != (check.ProjectConfigSourceSetSHA256 == "") ||
			check.TeamPolicySourceSetSHA256 != "" && !digestPattern.MatchString(check.TeamPolicySourceSetSHA256) ||
			check.ProjectConfigSourceSetSHA256 != "" && !digestPattern.MatchString(check.ProjectConfigSourceSetSHA256) ||
			check.TeamPolicyValid && check.TeamPolicyFileCount != 1 ||
			check.ProjectConfigValid && check.ProjectConfigFileCount != 1 {
			return errors.New("Mancode team-policy binding source commitments are invalid")
		}
		if check.WorkspaceBindingVerified && (!check.TeamPolicyValid || !check.ProjectConfigValid) {
			return errors.New("Mancode team-policy binding claims verification without valid inputs")
		}
		if len(check.Issues) > 8 {
			return errors.New("Mancode team-policy binding issue list exceeds bounds")
		}
		previousIssue := ""
		mismatch := false
		legacyUnverified := false
		for _, issue := range check.Issues {
			if !allowedIssues[issue] || issue <= previousIssue {
				return errors.New("Mancode team-policy binding issue list is invalid")
			}
			previousIssue = issue
			if issue == "workspace_mismatch" {
				mismatch = true
			}
			if issue == "legacy_unverified" {
				legacyUnverified = true
			}
		}
		if mismatch && (!check.TeamPolicyValid || !check.ProjectConfigValid || check.WorkspaceBindingVerified) {
			return errors.New("Mancode team-policy workspace mismatch contradicts its binding inputs")
		}
		if check.TeamPolicyValid && check.ProjectConfigValid && !legacyUnverified && check.WorkspaceBindingVerified == mismatch {
			return errors.New("Mancode team-policy workspace result contradicts its mismatch issue")
		}
		if (!check.TeamPolicyValid || !check.ProjectConfigValid) && (mismatch || check.WorkspaceBindingVerified) {
			return errors.New("Mancode team-policy workspace binding compares invalid inputs")
		}
		switch check.Status {
		case "verified":
			if !check.TeamPolicyValid || !check.ProjectConfigValid || !check.WorkspaceBindingVerified || len(check.Issues) != 0 {
				return errors.New("verified Mancode team-policy binding is incomplete")
			}
		case "incomplete":
			if len(check.Issues) == 0 || mismatch {
				return errors.New("incomplete Mancode team-policy binding has inconsistent issues")
			}
		case "conflicting":
			if len(check.Issues) == 0 || !mismatch {
				return errors.New("conflicting Mancode team-policy binding has no workspace mismatch")
			}
		default:
			return errors.New("Mancode team-policy binding status is invalid")
		}
	}
	return nil
}

func formatFiles(formats []Format, id string) int {
	for _, format := range formats {
		if format.ID == id {
			return format.Files
		}
	}
	return 0
}
