package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
)

type migrationFileCommitment struct {
	PathSHA256       string `json:"path_sha256"`
	SourceSHA256     string `json:"source_sha256"`
	SnapshotSHA256   string `json:"snapshot_sha256,omitempty"`
	SourceBytes      int64  `json:"source_bytes"`
	RecordCount      int    `json:"record_count"`
	ImportedCount    int    `json:"imported_count"`
	ExcludedCount    int    `json:"excluded_count"`
	RejectedCount    int    `json:"rejected_count"`
	Status           string `json:"status"`
	JoinedIntoSHA256 string `json:"joined_into_path_sha256,omitempty"`
}

// BuildFormatCoverage derives a path-redacted format ledger from a verified migration snapshot
// manifest. Snapshot file bytes must already have been verified by LoadSnapshot.
func BuildFormatCoverage(manifest Manifest) (migrationcoverage.Report, error) {
	if err := VerifyManifest(manifest); err != nil {
		return migrationcoverage.Report{}, fmt.Errorf("verify migration manifest: %w", err)
	}
	legacyWorkflowChecks := manifest.MancodeLegacyWorkflowChecks
	if manifest.Product == Mancode && len(legacyWorkflowChecks) == 0 {
		legacyWorkflowChecks = legacyMancodeWorkflowBindingChecks(manifest.Files, manifest.SourceRoot)
	}
	workflowArchiveChecks := manifest.MancodeWorkflowArchiveChecks
	if manifest.Product == Mancode && len(workflowArchiveChecks) == 0 {
		workflowArchiveChecks = legacyMancodeWorkflowArchiveChecks(manifest.Files, manifest.SourceRoot)
	}
	formats := make(map[string]*migrationcoverage.Format)
	for _, id := range migrationcoverage.RequiredFormats(string(manifest.Product)) {
		formats[id] = &migrationcoverage.Format{ID: id}
	}
	allCommitments := make([]migrationFileCommitment, 0, len(manifest.Files))
	byFormat := make(map[string][]migrationFileCommitment, len(formats)+1)
	pathHashes := make(map[string]bool, len(manifest.Files))
	var report migrationcoverage.Report
	for _, file := range manifest.Files {
		pathHash := digest([]byte(filepath.ToSlash(file.Path)))
		if pathHashes[pathHash] {
			return migrationcoverage.Report{}, errors.New("migration paths produce a duplicate path commitment")
		}
		pathHashes[pathHash] = true
		if file.ImportedCount > 0 && (file.SnapshotPath == "" || !validDigest(file.SnapshotSHA256)) {
			return migrationcoverage.Report{}, fmt.Errorf("imported migration file %s has no verified snapshot binding", file.Path)
		}
		commitment := migrationFileCommitment{
			PathSHA256: pathHash, SourceSHA256: file.SourceSHA256, SnapshotSHA256: file.SnapshotSHA256,
			SourceBytes: file.SourceBytes, RecordCount: file.RecordCount, ImportedCount: file.ImportedCount,
			ExcludedCount: file.ExcludedCount, RejectedCount: file.RejectedCount, Status: file.Status,
		}
		if file.JoinedInto != "" {
			commitment.JoinedIntoSHA256 = digest([]byte(filepath.ToSlash(file.JoinedInto)))
		}
		allCommitments = append(allCommitments, commitment)
		id := migrationFormatID(manifest.Product, manifest.SourceRoot, file.Path)
		if manifest.Product == Mancode {
			if referenced, _ := isMancodeLegacyWorkflowArtifactTarget(file.Path, manifest.SourceRoot, legacyWorkflowChecks); referenced {
				id = "mancode.workflow-document"
			}
		}
		if id == "" {
			id = migrationcoverage.UnclassifiedFormat
			if formats[id] == nil {
				formats[id] = &migrationcoverage.Format{ID: id}
			}
			report.UnclassifiedFiles++
		}
		byFormat[id] = append(byFormat[id], commitment)
		row := formats[id]
		if row == nil {
			row = &migrationcoverage.Format{ID: id}
			formats[id] = row
		}
		row.Files++
		row.SourceBytes += file.SourceBytes
		row.RecordsFound += file.RecordCount
		row.RecordsImported += file.ImportedCount
		row.CredentialExcludedRecords += file.ExcludedCount
		row.RejectedRecords += file.RejectedCount
		if file.SnapshotPath != "" {
			row.SnapshotFiles++
		}
		if file.Status == "joined_companion" {
			row.JoinedFiles++
		}
		if file.Status == "unsupported" {
			row.UnsupportedFiles++
		}
		if migrationFormatUnconverted(id) {
			row.UnconvertedFiles++
			report.UnconvertedFiles++
		}
	}
	sort.Slice(allCommitments, func(i, j int) bool { return allCommitments[i].PathSHA256 < allCommitments[j].PathSHA256 })
	snapshotBytes, err := json.Marshal(allCommitments)
	if err != nil {
		return migrationcoverage.Report{}, errors.New("encode migration snapshot commitments")
	}
	report.SchemaVersion = migrationcoverage.SchemaVersion
	report.Product = string(manifest.Product)
	report.SourceFingerprint = manifest.SourceFingerprint
	report.SnapshotSHA256 = digest(snapshotBytes)
	report.FilesScanned = manifest.Summary.FilesScanned
	report.SourceBytes = manifest.Summary.TotalSourceBytes
	report.RecordsFound = manifest.Summary.RecordsFound
	report.RecordsImported = manifest.Summary.RecordsImported
	report.CredentialExcludedRecords = manifest.Summary.RecordsCredentialBlocked
	report.RejectedRecords = manifest.Summary.RecordsRejected
	report.JoinedFiles = manifest.Summary.FilesJoined
	report.UnsupportedFiles = manifest.Summary.FilesUnsupported
	crossFileChecks := manifest.CrossFileChecks
	if manifest.Product == Mancode && len(crossFileChecks) == 0 {
		crossFileChecks = legacyMancodePrivacyBindingChecks(manifest.Files)
	}
	report.CrossFileChecks = append([]migrationcoverage.CrossFileCheck(nil), crossFileChecks...)
	report.MancodeLegacyWorkflowChecks = append([]migrationcoverage.MancodeLegacyWorkflowCheck(nil), legacyWorkflowChecks...)
	teamPolicyChecks := manifest.MancodeTeamPolicyChecks
	if manifest.Product == Mancode && len(teamPolicyChecks) == 0 {
		teamPolicyChecks = legacyMancodeTeamPolicyChecks(manifest.Files, manifest.SourceRoot)
	}
	report.MancodeTeamPolicyChecks = append([]migrationcoverage.MancodeTeamPolicyCheck(nil), teamPolicyChecks...)
	report.MancodeWorkflowArchiveChecks = append([]migrationcoverage.MancodeWorkflowArchiveCheck(nil), workflowArchiveChecks...)
	for _, file := range manifest.Files {
		if file.SnapshotPath != "" {
			report.SnapshotFiles++
		}
	}
	ids := make([]string, 0, len(formats))
	for id := range formats {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		commitments := byFormat[id]
		sort.Slice(commitments, func(i, j int) bool { return commitments[i].PathSHA256 < commitments[j].PathSHA256 })
		encoded, err := json.Marshal(commitments)
		if err != nil {
			return migrationcoverage.Report{}, errors.New("encode per-format migration commitments")
		}
		formats[id].FileSetSHA256 = digest(encoded)
		report.Formats = append(report.Formats, *formats[id])
	}
	if err := migrationcoverage.Validate(report); err != nil {
		return migrationcoverage.Report{}, err
	}
	return report, nil
}

func migrationFormatID(product Product, sourceRoot, relative string) string {
	base := strings.ToLower(filepath.Base(relative))
	switch product {
	case Beacon:
		if strings.HasPrefix(base, "runtime.jsonl") {
			return "beacon.runtime-events"
		}
		if strings.HasPrefix(base, "inventory_state.jsonl") {
			return "beacon.inventory-events"
		}
		if isBeaconLearningDBPath(relative) {
			return "beacon.learning-database"
		}
		if isBeaconOpenCodePluginPath(relative, sourceRoot) {
			return "beacon.opencode-plugin"
		}
		if isBeaconClinePluginPath(relative, sourceRoot) {
			return "beacon.cline-plugin"
		}
		if isBeaconHookLogPath(relative, sourceRoot) {
			return "beacon.hook-log"
		}
		for _, host := range []struct {
			id      string
			matches bool
		}{
			{"claude", isBeaconClaudeSettingsPath(relative, sourceRoot)},
			{"qwen", isBeaconQwenSettingsPath(relative, sourceRoot)},
			{"factory", isBeaconFactorySettingsPath(relative, sourceRoot)},
			{"cursor", isBeaconCursorHooksPath(relative, sourceRoot)},
			{"codex-hooks", isBeaconCodexHooksPath(relative, sourceRoot)},
			{"codex-otel", isBeaconCodexOTelConfigPath(relative, sourceRoot)},
			{"gemini", isBeaconGeminiSettingsPath(relative, sourceRoot)},
			{"antigravity", isBeaconAntigravityHooksPath(relative, sourceRoot)},
			{"openhands", isBeaconOpenHandsHooksPath(relative, sourceRoot)},
			{"kiro", isBeaconKiroHooksPath(relative, sourceRoot)},
			{"kimi-code", isBeaconKimiCodeConfigPath(relative, sourceRoot)},
			{"muse-hooks", isBeaconMuseManagedHooksPath(relative, sourceRoot)},
			{"muse-settings", isBeaconMuseSettingsPath(relative, sourceRoot)},
		} {
			if host.matches {
				return "beacon.endpoint-" + host.id
			}
		}
		if isBeaconEndpointConfigPath(relative, sourceRoot) {
			return "beacon.endpoint-generic"
		}
	case Mancode:
		if isMancodeWorkflowArchivePath(relative, sourceRoot) {
			return "mancode.workflow-archive"
		}
		for _, workflow := range []struct {
			name string
			path func(string, string) (string, bool)
		}{
			{"metadata", isMancodeLegacyWorkflowMetadataPath},
			{"requirements", isMancodeLegacyWorkflowRequirementsPath},
			{"review", isMancodeLegacyWorkflowReviewPath},
			{"verification", isMancodeLegacyWorkflowVerificationPath},
			{"plan", isMancodeLegacyWorkflowPlanPath},
		} {
			if _, ok := workflow.path(relative, sourceRoot); ok {
				return "mancode.legacy-workflow-" + workflow.name
			}
		}
		if relative == mancodeGitRefVirtualPath {
			return "mancode.git-ref-team-authority"
		}
		if isMancodePrivacyPolicyPath(relative) {
			return "mancode.privacy-policy"
		}
		if isMancodePrivacyExclusionsPath(relative) {
			return "mancode.privacy-exclusions"
		}
		if isMancodeProgressHTMLPath(relative) {
			return "mancode.progress-projection"
		}
		kind := classifyMancodeRecord(relative)
		kind = strings.TrimPrefix(kind, "mancode_")
		return "mancode." + strings.ReplaceAll(kind, "_", "-")
	case AnyJev:
		switch {
		case isAnyJevDistillationSyntheticJSONPath(relative):
			return "anyjev.synthetic-state-datasets"
		case isAnyJevDistillationNPZPath(relative):
			return "anyjev.distillation-cache-npz"
		case isAnyJevHeadStudyJSONPath(relative):
			return "anyjev.head-study"
		case isAnyJevZeroLabelDistillationJSONPath(relative):
			return "anyjev.zero-label-distillation"
		case isAnyJevDistillationJSONPath(relative):
			return "anyjev.distillation-results"
		case isAnyJevLatencyMarkdownPath(relative):
			return "anyjev.latency-markdown"
		case isAnyJevTypedMarkdownPath(relative):
			return "anyjev.typed-markdown"
		case isAnyJevBenchmarkMarkdownPath(relative):
			return "anyjev.classification-markdown"
		case isAnyJevDemoJevModeJSONPath(relative):
			return "anyjev.demo-jev-mode"
		case isAnyJevDemoGameJSONPath(relative):
			return "anyjev.demo-games"
		case isAnyJevUniversalResultJSONPath(relative):
			return "anyjev.universal-results"
		case isAnyJevItemDumpJSONPath(relative) || isAnyJevItemDumpGzipPath(relative):
			return "anyjev.per-item-dumps"
		case isAnyJevParaphraseOrderJSONPath(relative):
			return "anyjev.paraphrase-order"
		case isAnyJevExitCascadeJSONPath(relative):
			return "anyjev.exit-cascade"
		case isAnyJevExitArtifactJSONPath(relative):
			return "anyjev.exit-artifact"
		case isAnyJevNativeMazeJSONPath(relative):
			return "anyjev.native-maze"
		case isAnyJevExitLabelsJSONPath(relative):
			return "anyjev.exit-labels"
		case isAnyJevExitJevModeJSONPath(relative):
			return "anyjev.exit-jev-mode"
		case isAnyJevExitLatencyJSONPath(relative):
			return "anyjev.exit-latency"
		case isAnyJevExitDepthJSONPath(relative):
			return "anyjev.exit-depth"
		case isAnyJevNanoJevMazeJSONPath(relative):
			return "anyjev.nanojev-maze"
		case isAnyJevLatencyBenchmarkJSONPath(relative):
			return "anyjev.latency-benchmark-json"
		case isAnyJevTypedBenchmarkJSONPath(relative):
			return "anyjev.typed-benchmark-json"
		case isAnyJevBenchmarkJSONPath(relative):
			return "anyjev.classification-benchmark-json"
		}
		lower := "/" + strings.ToLower(strings.Trim(relative, "/")) + "/"
		if strings.Contains(lower, "/anyjev-heads/") || strings.Contains(lower, "/artifacts/") {
			return "anyjev.heads-artifact-json"
		}
		if strings.Contains(lower, "/labels/") {
			return "anyjev.unconverted-label-data"
		}
		if strings.Contains(lower, "/bench/results") {
			return anyJevUnconvertedResultsFormat(relative)
		}
	case RRSI:
		if isRRSIProposalPath(relative) {
			return "rrsi.proposal-json"
		}
		if source, ok := rrsiTrialSourceForPath(relative); ok {
			switch source.kind {
			case "coding_trajectory", "workspace_trajectory", "engineering_trajectory":
				return "rrsi.trajectory"
			case "coding_result":
				return "rrsi.coding-trial-result"
			case "workspace_scores":
				return "rrsi.workspace-scores"
			case "engineering_meta":
				return "rrsi.engineering-meta"
			case "engineering_verdict":
				return "rrsi.engineering-verdict"
			}
		}
		switch base {
		case "eval.json":
			return "rrsi.evaluation-json"
		case "calibration.json":
			return "rrsi.calibration-json"
		case "frontier.json":
			return "rrsi.frontier-json"
		case "history.jsonl":
			return "rrsi.history-jsonl"
		case "attribution.jsonl":
			return "rrsi.attribution-jsonl"
		case "decisions.json":
			return "rrsi.decisions-json"
		}
		if isRRSIRawTrialArtifactPath(relative) {
			return "rrsi.raw-trial-artifact"
		}
	}
	return ""
}

func anyJevUnconvertedResultsFormat(relative string) string {
	parts := strings.Split(strings.ToLower(filepath.ToSlash(relative)), "/")
	for _, part := range parts {
		switch part {
		case "results_distill":
			if strings.Contains(strings.ToLower(filepath.ToSlash(relative)), "/results_distill/synth/") {
				return "anyjev.synthetic-state-datasets"
			}
		case "results_typed_v01":
			return "anyjev.typed-v01-results"
		case "results_typed_small":
			return "anyjev.typed-small-results"
		case "results_typed_diag":
			return "anyjev.typed-diagnostic-results"
		case "results_dump":
			return "anyjev.dump-summary-results"
		case "results_typed_dump":
			return "anyjev.typed-dump-summary-results"
		}
	}
	return ""
}

func migrationFormatUnconverted(formatID string) bool {
	switch formatID {
	case "mancode.project-record", "mancode.workflow-state", "anyjev.typed-v01-results",
		"anyjev.typed-small-results", "anyjev.typed-diagnostic-results", "anyjev.dump-summary-results",
		"anyjev.typed-dump-summary-results",
		"anyjev.unconverted-label-data":
		return true
	default:
		return false
	}
}
