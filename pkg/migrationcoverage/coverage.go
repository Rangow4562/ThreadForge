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

const (
	maxCoverageFiles   = 10000
	maxCoverageRecords = 100000
	maxCoverageBytes   = int64(1 << 30)
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Report struct {
	SchemaVersion             string           `json:"schema_version"`
	Product                   string           `json:"product"`
	SourceFingerprint         string           `json:"source_fingerprint"`
	SnapshotSHA256            string           `json:"snapshot_sha256"`
	FilesScanned              int              `json:"files_scanned"`
	SourceBytes               int64            `json:"source_bytes"`
	RecordsFound              int              `json:"records_found"`
	RecordsImported           int              `json:"records_imported"`
	CredentialExcludedRecords int              `json:"credential_excluded_records"`
	RejectedRecords           int              `json:"rejected_records"`
	SnapshotFiles             int              `json:"snapshot_files"`
	JoinedFiles               int              `json:"joined_files"`
	UnsupportedFiles          int              `json:"unsupported_files"`
	UnconvertedFiles          int              `json:"unconverted_files"`
	UnclassifiedFiles         int              `json:"unclassified_files"`
	CrossFileChecks           []CrossFileCheck `json:"cross_file_checks,omitempty"`
	Formats                   []Format         `json:"formats"`
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

func formatFiles(formats []Format, id string) int {
	for _, format := range formats {
		if format.ID == id {
			return format.Files
		}
	}
	return 0
}
