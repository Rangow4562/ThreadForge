package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/internal/migrate"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
)

func TestBeaconOpenCodeManagedPluginMigrationReadback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	plugin := filepath.Join(root, ".opencode", "plugins", "beacon.ts")
	if err := os.MkdirAll(filepath.Dir(plugin), 0o700); err != nil {
		t.Fatal("create synthetic Beacon OpenCode plugin directory")
	}
	contents := "// beacon-managed-opencode-plugin:v1\nconst config = 'private-plugin-source';\n"
	if err := os.WriteFile(plugin, []byte(contents), 0o600); err != nil {
		t.Fatal("write synthetic Beacon OpenCode plugin")
	}

	readback := runConfiguredMigrationSourceReadback(t, migrate.Beacon, root)
	if readback.Snapshot.Summary.RecordsImported != 1 || readback.Verification.CutoverAuthorized {
		t.Fatalf("OpenCode plugin migration receipt did not reconcile as review-only: %#v", readback)
	}
	records, err := migrate.ReadSnapshotRecords(readback.ManifestPath, readback.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "beacon_opencode_plugin_presence" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected OpenCode plugin migration record: %#v", records)
	}
	if strings.Contains(string(records[0].Payload), "private-plugin-source") ||
		strings.Contains(string(records[0].Payload), "plugins/beacon.ts") {
		t.Fatalf("normalized OpenCode record retained source content or local path: %s", records[0].Payload)
	}
}

func TestBeaconClineManagedPluginMigrationReadback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	plugin := filepath.Join(root, ".cline", "plugins", "beacon.ts")
	if err := os.MkdirAll(filepath.Dir(plugin), 0o700); err != nil {
		t.Fatal("create synthetic Beacon Cline plugin directory")
	}
	contents := "// beacon-managed-cline-plugin:v1\nconst config = 'private-cline-plugin-source';\n"
	if err := os.WriteFile(plugin, []byte(contents), 0o600); err != nil {
		t.Fatal("write synthetic Beacon Cline plugin")
	}

	readback := runConfiguredMigrationSourceReadback(t, migrate.Beacon, root)
	if readback.Snapshot.Summary.RecordsImported != 1 || readback.Verification.CutoverAuthorized {
		t.Fatalf("Cline plugin migration receipt did not reconcile as review-only: %#v", readback)
	}
	clineFamilyFound := false
	for _, format := range readback.Coverage.Formats {
		if format.ID == "beacon.cline-plugin" && format.Files == 1 && format.RecordsImported == 1 {
			clineFamilyFound = true
		}
	}
	if !clineFamilyFound {
		t.Fatalf("Cline plugin was not reconciled in its distinct coverage family: %#v", readback.Coverage.Formats)
	}
	records, err := migrate.ReadSnapshotRecords(readback.ManifestPath, readback.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "beacon_cline_plugin_presence" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Cline plugin migration record: %#v", records)
	}
	if strings.Contains(string(records[0].Payload), "private-cline-plugin-source") ||
		strings.Contains(string(records[0].Payload), ".cline/plugins/beacon.ts") {
		t.Fatalf("normalized Cline record retained source content or local path: %s", records[0].Payload)
	}
}

func TestRRSISyntheticTrialMigrationReadback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rrsi")
	resultPath := filepath.Join(root, "runs", "coding", "jobs", "base", "task-1__trialhash", "result.json")
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
		t.Fatal("create synthetic RRSI source directory")
	}
	result := `{"task_name":"terminal-bench/task-1","verifier_result":{"rewards":{"reward":0.5}},"agent_result":{"n_input_tokens":11,"n_output_tokens":7,"metadata":{"n_episodes":2,"private_note":"raw-trial-content-marker"}},"exception_info":null,"transcript":"raw-trial-content-marker","local_path":"C:/Users/private/trial"}`
	if err := os.WriteFile(resultPath, []byte(result), 0o600); err != nil {
		t.Fatal("write synthetic RRSI trial source")
	}
	trajectoryPath := filepath.Join(root, "runs", "coding", "jobs", "base", "task-1__trialhash", "agent", "trajectory.json")
	if err := os.MkdirAll(filepath.Dir(trajectoryPath), 0o700); err != nil {
		t.Fatal("create synthetic RRSI trajectory directory")
	}
	trajectory := `{"steps":[{"source":"assistant","message":"raw-trajectory-private-marker","tool_calls":[{"name":"private-tool","arguments":{"secret":"private-argument"}}],"observation":{"results":[{"content":"private-result"}]}}]}`
	if err := os.WriteFile(trajectoryPath, []byte(trajectory), 0o600); err != nil {
		t.Fatal("write synthetic RRSI trajectory source")
	}
	deliverables := map[string]string{
		"runs/coding/jobs/base/task-1__trialhash/agent/recording.cast": "{\"version\":2}\n[0.1,\"o\",\"private-terminal-output\"]\n",
		"runs/workspace/jobs/work/s0/area__task/output/report.md":      "private-workspace-deliverable",
		"runs/eng/jobs/candidate/task-a/t2/payload.py":                 "PAYLOAD = 'private-engineering-payload'\n",
	}
	for relative, content := range deliverables {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create synthetic RRSI deliverable directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write synthetic RRSI deliverable: %v", err)
		}
	}
	readback := runConfiguredMigrationSourceReadback(t, migrate.RRSI, root)
	if readback.Snapshot.Summary.FilesScanned != 5 || readback.Snapshot.Summary.FilesUnsupported != 0 ||
		readback.Snapshot.Summary.RecordsImported != 5 ||
		readback.Verification.Status != "reconciled" || readback.Verification.CutoverAuthorized {
		t.Fatalf("RRSI trial, trajectory, and deliverable CLI migration receipt did not reconcile as review-only: %#v", readback)
	}
	records, err := migrate.ReadSnapshotRecords(readback.ManifestPath, readback.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 5 {
		t.Fatalf("RRSI trial, trajectory, and deliverable CLI record count = %d, want 5", len(records))
	}
	expectedDeliverableFamilies := map[string]bool{
		"rrsi.coding-terminal-recording": false,
		"rrsi.workspace-deliverable":     false,
		"rrsi.engineering-payload":       false,
	}
	for _, format := range readback.Coverage.Formats {
		if _, expected := expectedDeliverableFamilies[format.ID]; expected {
			if format.Files != 1 || format.RecordsImported != 1 {
				t.Errorf("RRSI deliverable coverage did not reconcile for %s: %#v", format.ID, format)
			}
			expectedDeliverableFamilies[format.ID] = true
		}
	}
	for family, found := range expectedDeliverableFamilies {
		if !found {
			t.Errorf("RRSI migration coverage omitted deliverable family %s", family)
		}
	}
	foundTrajectory := false
	deliverablesFound := map[string]bool{}
	for _, record := range records {
		if record.Type == "rrsi_trajectory_summary" {
			foundTrajectory = true
			if strings.Contains(string(record.Payload), "raw-trajectory-private-marker") ||
				strings.Contains(string(record.Payload), "private-tool") || strings.Contains(string(record.Payload), "private-result") {
				t.Fatalf("RRSI trajectory CLI snapshot retained trace content: %s", record.Payload)
			}
		}
		if record.Type == "rrsi_deliverable_summary" {
			var summary struct {
				RecordKind              string `json:"record_kind"`
				ContentImported         bool   `json:"content_imported"`
				HumanReviewRequired     bool   `json:"human_review_required"`
				ExecutionAuthorized     bool   `json:"execution_authorized"`
				CutoverEvidenceEligible bool   `json:"cutover_evidence_eligible"`
			}
			if err := json.Unmarshal(record.Payload, &summary); err != nil {
				t.Fatal(err)
			}
			if summary.ContentImported || !summary.HumanReviewRequired || summary.ExecutionAuthorized || summary.CutoverEvidenceEligible {
				t.Fatalf("RRSI CLI deliverable receipt violated review-only boundary: %#v", summary)
			}
			deliverablesFound[summary.RecordKind] = true
			for _, private := range []string{"private-terminal-output", "private-workspace-deliverable", "private-engineering-payload", "recording.cast", "report.md", "payload.py"} {
				if strings.Contains(string(record.Payload), private) || strings.Contains(record.SourcePath, private) {
					t.Fatalf("RRSI CLI deliverable receipt retained private content or source identity %q: %#v", private, record)
				}
			}
		}
	}
	if !foundTrajectory {
		t.Fatal("RRSI trajectory CLI snapshot omitted its structure-only record")
	}
	for _, kind := range []string{"coding_terminal_recording", "workspace_deliverable", "engineering_payload"} {
		if !deliverablesFound[kind] {
			t.Errorf("RRSI CLI snapshot omitted deliverable receipt %q", kind)
		}
	}
}

func TestRRSIAggregateEvaluationReportsMigrationReadback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rrsi-evaluations")
	fixtures := map[string]string{
		"coding":    `{"job":"base","k":2,"S":0.5,"C":150,"n_expected":2,"missing":0,"extra":{"total_passes":1,"n_trials":2,"pass_rate":0.5},"per_task":{"task-01":{"rewards":[1,0],"weights":[1,1],"tokens":[100,200],"missing":0,"extra":{"passes":1,"trial_dirs":["C:/Users/private/raw-trial"]}}}}`,
		"workspace": `{"job":"base","k":2,"S":0.8333333333333334,"C":150,"n_expected":2,"missing":0,"extra":{"criteria_passed":5,"criteria_total":6,"all_pass_tasks":0,"criterion_rate":0.8333333333333334},"per_task":{"task-01":{"rewards":[0.5,1],"weights":[2,4],"tokens":[100,200],"missing":0,"extra":{}}}}`,
		"eng":       `{"job":"base","k":2,"S":0.5,"C":150,"n_expected":2,"missing":0,"extra":{"total_passes":1,"n_trials":2,"pass_rate":0.5,"mean_combined_score":0.6,"valid_rate":0.95,"no_payload_rate":0},"per_task":{"task-01":{"rewards":[1,0],"weights":[1,1],"tokens":[100,200],"missing":0,"extra":{"combined_scores":[0.7,0.5],"valid":[1,0.9],"no_payload":[0,0]}}}}`,
	}
	for domain, contents := range fixtures {
		file := filepath.Join(root, "runs", domain, "jobs", "base", "eval.json")
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal("create synthetic RRSI evaluation directory")
		}
		if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
			t.Fatal("write synthetic RRSI evaluation report")
		}
	}

	readback := runConfiguredMigrationSourceReadback(t, migrate.RRSI, root)
	if readback.Snapshot.Summary.FilesScanned != 3 || readback.Snapshot.Summary.RecordsImported != 3 ||
		readback.Verification.Status != "reconciled" || readback.Verification.CutoverAuthorized {
		t.Fatalf("aggregate RRSI migration receipt did not reconcile: %#v", readback)
	}
	records, err := migrate.ReadSnapshotRecords(readback.ManifestPath, readback.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("normalized RRSI evaluation record count = %d, want 3", len(records))
	}
	type normalizedEvaluation struct {
		SchemaVersion string             `json:"schema_version"`
		Domain        string             `json:"domain"`
		Score         float64            `json:"score"`
		MeanTokens    float64            `json:"mean_tokens"`
		Expected      int                `json:"expected_trials"`
		Missing       int                `json:"missing_trials"`
		Metrics       map[string]float64 `json:"metrics"`
	}
	want := map[string]struct {
		score   float64
		metrics map[string]float64
	}{
		"coding":    {0.5, map[string]float64{"pass_rate": 0.5, "total_passes": 1}},
		"workspace": {5.0 / 6.0, map[string]float64{"criterion_rate": 5.0 / 6.0, "criteria_passed": 5, "criteria_total": 6}},
		"eng":       {0.5, map[string]float64{"pass_rate": 0.5, "mean_combined_score": 0.6, "valid_rate": 0.95, "no_payload_rate": 0}},
	}
	seen := map[string]bool{}
	for _, record := range records {
		if record.Type != "rrsi_evaluation_result" || record.Scope != "local_pending_review" {
			t.Fatalf("aggregate result became active or changed type: %#v", record)
		}
		var payload normalizedEvaluation
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		expected, ok := want[payload.Domain]
		if !ok || seen[payload.Domain] || payload.SchemaVersion != "threadforge.rrsi-evaluation.v1" ||
			math.Abs(payload.Score-expected.score) > 1e-9 || payload.Expected != 2 || payload.Missing != 0 || payload.MeanTokens != 150 {
			t.Fatalf("RRSI evaluation identity/aggregate did not replay: %#v", payload)
		}
		for key, value := range expected.metrics {
			if math.Abs(payload.Metrics[key]-value) > 1e-9 {
				t.Fatalf("RRSI %s metric %s = %v, want %v", payload.Domain, key, payload.Metrics[key], value)
			}
		}
		if strings.Contains(string(record.Payload), "C:/Users/private") || strings.Contains(string(record.Payload), "trial_dirs") {
			t.Fatalf("RRSI aggregate snapshot retained local trial paths: %s", record.Payload)
		}
		seen[payload.Domain] = true
	}
	if len(seen) != 3 {
		t.Fatalf("RRSI aggregate domains did not reconcile: %#v", seen)
	}
}

func TestMancodeSyntheticProjectTeamMigrationReadback(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".mancode")
	actorID := strings.Repeat("0", 25) + "1"
	privacyExclusions := map[string]any{
		"schemaVersion": 1,
		"revision":      2,
		"workspaceId":   actorID,
		"entries": []any{map[string]any{
			"kind": "confirmed_decision", "relativePath": "shared/memory/decisions/" + actorID + ".json",
			"entityDigest": "sha256:" + strings.Repeat("b", 64),
		}},
		"lastOperationId": actorID,
		"updatedAt":       "2026-10-08T10:00:00Z",
	}
	canonicalExclusions, err := json.Marshal(privacyExclusions)
	if err != nil {
		t.Fatal("canonicalize synthetic Mancode privacy exclusions")
	}
	privacyExclusionsDigest := sha256.Sum256(canonicalExclusions)
	privacyExclusionsDigestText := "sha256:" + hex.EncodeToString(privacyExclusionsDigest[:])
	privacyExclusionsJSON, err := json.Marshal(privacyExclusions)
	if err != nil {
		t.Fatal("encode synthetic Mancode privacy exclusions")
	}
	fixtures := []struct {
		path string
		json string
	}{
		{"schema.json", `{"manifestVersion":3,"layoutVersion":3,"epoch":"` + actorID + `","activationState":"v3_active","minReaderVersion":"0.6.5","minWriterVersion":"0.6.5","activatedAt":"2026-10-08T10:00:00Z","legacyBaseline":null,"managedAdapters":{"codex":"0.1.0"},"lastOperationId":"` + actorID + `","workflowPolicyDefaults":{"planning":1},"privacyPolicy":{"revision":1,"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`},
		{"shared/context/project.json", `{"schemaVersion":1,"revision":2,"trust":"detected","profile":{"version":"1.0","projectKind":"web","languages":["TypeScript"],"frameworks":["private-framework"],"sourceRoots":["src"],"manifests":["package.json"],"availableValidation":["private-command"],"uiAssets":"detected","browserAutomation":"available","confidence":"high"},"uiLibrary":"private-ui","detectedAt":"2026-10-08T10:00:00Z","lastOperationId":"` + actorID + `"}`},
		{"shared/context/glossary.json", `{"schemaVersion":1,"revision":1,"entries":[{"term":"Migration record","definition":"An imported item awaiting Threadforge review.","aliases":["review record"],"sourceTaskRef":{"namespace":"shared","taskId":"` + actorID + `"},"confirmedAt":"2026-10-08T10:00:00Z"}]}`},
		{"shared/context/design-policy.json", `{"schemaVersion":1,"revision":1,"enabled":true,"preset":"preserve","iconPolicy":"existing-first","emojiPolicy":"forbid-as-interface-icon","motionPolicy":"minimal","browserValidation":"when-available","lastOperationId":"` + actorID + `","updatedAt":"2026-10-08T10:00:00Z"}`},
		{"shared/context/privacy-policy.json", `{"schemaVersion":1,"revision":1,"workspaceId":"` + actorID + `","enabled":true,"rulesetVersion":"mancode-sensitive-text:1","enabledRuleIds":["email-address"],"targets":["shared_write","context_output"],"exclusions":{"revision":2,"digest":"` + privacyExclusionsDigestText + `"},"lastOperationId":"` + actorID + `","updatedAt":"2026-10-08T10:00:00Z"}`},
		{"shared/context/privacy-exclusions.json", string(privacyExclusionsJSON)},
		{"shared/config.json", `{"schemaVersion":1,"revision":1,"workspaceId":"` + actorID + `","transport":{"mode":"local","epoch":2},"updatedAt":"2026-10-08T10:00:00Z"}`},
		{"shared/team/policy.json", `{"schemaVersion":1,"revision":4,"workspaceId":"` + actorID + `","policy":"auto","recentDays":30,"defaultVisibility":"local","shareConfirmedDecisions":false,"retention":{"localRawArtifactDays":0,"localCacheDays":14,"completedSessionDays":30},"lastOperationId":"` + actorID + `","updatedAt":"2026-10-08T10:00:00Z"}`},
		{"shared/team/actors/" + actorID + ".json", `{"schemaVersion":1,"actorId":"` + actorID + `","displayName":"Fixture reviewer","joinedAt":"2026-10-08T10:00:00Z","updatedAt":"2026-10-08T12:00:00Z"}`},
		{"shared/team/events/" + actorID + ".json", `{"schemaVersion":1,"eventId":"` + actorID + `","eventType":"team_policy_updated","operationId":"` + actorID + `","entityRef":{"kind":"team_policy","id":"` + actorID + `"},"taskRef":null,"actorId":"` + actorID + `","taskRevision":null,"createdAt":"2026-10-08T12:00:00Z"}`},
		{"local/actor.json", `{"schemaVersion":1,"actorId":"` + actorID + `","displayName":"Local reviewer","createdAt":"2026-10-07T10:00:00Z","updatedAt":"2026-10-08T12:00:00Z"}`},
		{"shared/memory/decisions/" + actorID + ".json", `{"schemaVersion":1,"decisionId":"` + actorID + `","title":"Keep migration review-only","statement":"Imported decisions remain advisory until reviewed by Threadforge.","taskRef":null,"confirmedByActorId":"` + actorID + `","confirmedAt":"2026-10-08T10:00:00Z","operationId":"` + actorID + `","authorization":{"schemaVersion":1,"action":"confirmed_decision_publish","actorId":"` + actorID + `","sessionId":"` + actorID + `","trustBoundary":"repo-collaborators","decisionDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","authorizedAt":"2026-10-08T10:00:00Z"}}`},
	}
	for _, fixture := range fixtures {
		path := filepath.Join(root, filepath.FromSlash(fixture.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal("create synthetic Mancode source directory")
		}
		if err := os.WriteFile(path, []byte(fixture.json), 0o600); err != nil {
			t.Fatal("write synthetic Mancode source record")
		}
	}
	runConfiguredMigrationSourceReadback(t, migrate.Mancode, root)
}

func TestConfiguredMigrationSourceReadback(t *testing.T) {
	sources := []struct {
		product migrate.Product
		env     string
	}{
		{product: migrate.Beacon, env: "THREADFORGE_BEACON_SOURCE"},
		{product: migrate.Mancode, env: "THREADFORGE_MANCODE_SOURCE"},
		{product: migrate.AnyJev, env: "THREADFORGE_ANYJEV_SOURCE"},
		{product: migrate.RRSI, env: "THREADFORGE_RRSI_SOURCE"},
	}
	configured := false
	for _, source := range sources {
		root := os.Getenv(source.env)
		if root == "" {
			continue
		}
		configured = true
		t.Run(string(source.product), func(t *testing.T) {
			runConfiguredMigrationSourceReadback(t, source.product, root)
		})
	}
	if !configured {
		t.Skip("set a THREADFORGE_<PRODUCT>_SOURCE variable to run an actual-source migration round trip")
	}
}

type migrationSourceReadbackResult struct {
	Snapshot     migrate.Manifest
	ManifestPath string
	Verification migrationVerification
	Coverage     migrationcoverage.Report
}

func runConfiguredMigrationSourceReadback(t *testing.T, product migrate.Product, sourceRoot string) migrationSourceReadbackResult {
	t.Helper()
	absoluteRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		t.Fatal("resolve configured migration source")
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil || !info.IsDir() {
		t.Fatalf("configured %s migration source is not a readable directory", product)
	}
	home := t.TempDir()
	t.Setenv("THREADFORGE_HOME", home)
	t.Setenv("THREADFORGE_DB", "")
	t.Setenv("THREADFORGE_TRACE_FILE", "")
	databasePath := filepath.Join(home, "threadforge.sqlite")

	var inspected migrate.Manifest
	runMigrationJSON(t, &inspected, "migrate", "inspect", "--product", string(product), "--root", absoluteRoot)
	if inspected.Summary.FilesScanned == 0 || inspected.Summary.RecordsImported == 0 {
		t.Fatalf("configured %s source has no eligible migration records", product)
	}
	if inspected.Summary.FilesUnsupported != 0 || inspected.Summary.RecordsRejected != 0 {
		t.Fatalf("configured %s source has unsupported or rejected data: files=%d records=%d",
			product, inspected.Summary.FilesUnsupported, inspected.Summary.RecordsRejected)
	}

	var snapshot migrate.Manifest
	runMigrationJSON(t, &snapshot, "migrate", "snapshot", "--product", string(product), "--root", absoluteRoot)
	if snapshot.SourceFingerprint != inspected.SourceFingerprint || snapshot.Summary != inspected.Summary ||
		snapshot.ID != inspected.ID {
		t.Fatalf("configured %s source changed between inspect and snapshot", product)
	}
	manifestPath := filepath.Join(home, "migrations", "snapshots", string(product), snapshot.SourceFingerprint, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("private %s migration snapshot was not created", product)
	}
	backupRoot := filepath.Join(home, "source-backup", string(product))
	copyMigrationSourceTree(t, absoluteRoot, backupRoot)
	var coverage migrationcoverage.Report
	runMigrationJSON(t, &coverage, "migrate", "coverage", "--manifest", manifestPath)
	if err := migrationcoverage.Validate(coverage); err != nil {
		t.Fatalf("configured %s source produced invalid format coverage: %v", product, err)
	}
	if coverage.SourceFingerprint != snapshot.SourceFingerprint || coverage.FilesScanned != snapshot.Summary.FilesScanned ||
		coverage.RecordsFound != snapshot.Summary.RecordsFound || coverage.RecordsImported != snapshot.Summary.RecordsImported ||
		coverage.CredentialExcludedRecords != snapshot.Summary.RecordsCredentialBlocked ||
		coverage.RejectedRecords != snapshot.Summary.RecordsRejected || coverage.UnclassifiedFiles != 0 ||
		coverage.UnsupportedFiles != 0 || coverage.UnconvertedFiles != 0 || coverage.RejectedRecords != 0 {
		t.Fatalf("configured %s source format coverage failed source reconciliation: %#v", product, coverage)
	}
	populatedFormats := make([]string, 0, len(coverage.Formats))
	unconvertedFormats := make([]string, 0)
	for _, format := range coverage.Formats {
		if format.Files > 0 {
			populatedFormats = append(populatedFormats, fmt.Sprintf("%s=%d", format.ID, format.Files))
		}
		if format.UnconvertedFiles > 0 {
			unconvertedFormats = append(unconvertedFormats, fmt.Sprintf("%s=%d", format.ID, format.UnconvertedFiles))
		}
	}
	t.Logf("source format coverage: product=%s files=%d unconverted_files=%d unconverted_families=%s formats=%s",
		product, coverage.FilesScanned, coverage.UnconvertedFiles, strings.Join(unconvertedFormats, ","), strings.Join(populatedFormats, ","))

	var applied struct {
		Receipt        store.MigrationReceipt `json:"receipt"`
		AlreadyApplied bool                   `json:"already_applied"`
		Scope          string                 `json:"scope"`
		Backup         struct {
			Status string `json:"status"`
		} `json:"source_backup_verification"`
	}
	runMigrationJSON(t, &applied, "migrate", "apply", "--manifest", manifestPath, "--backup-root", backupRoot, "--db", databasePath)
	if applied.AlreadyApplied || applied.Scope != "local_pending_review" || applied.Receipt.Status != "applied" ||
		applied.Backup.Status != "verified" ||
		applied.Receipt.ImportedCount != snapshot.Summary.RecordsImported ||
		applied.Receipt.ExcludedCount != snapshot.Summary.RecordsCredentialBlocked+snapshot.Summary.RecordsRejected {
		t.Fatalf("configured %s migration receipt did not reconcile to its snapshot", product)
	}
	var reapplied struct {
		Receipt        store.MigrationReceipt `json:"receipt"`
		AlreadyApplied bool                   `json:"already_applied"`
		Scope          string                 `json:"scope"`
	}
	runMigrationJSON(t, &reapplied, "migrate", "apply", "--manifest", manifestPath, "--backup-root", backupRoot, "--db", databasePath)
	if !reapplied.AlreadyApplied || reapplied.Receipt.ImportedCount != applied.Receipt.ImportedCount {
		t.Fatalf("configured %s migration apply is not idempotent", product)
	}

	var verification migrationVerification
	runMigrationJSON(t, &verification, "migrate", "verify", "--manifest", manifestPath, "--db", databasePath)
	if verification.Status != "reconciled" || verification.ActualState != "applied" ||
		verification.SnapshotRecordCount != snapshot.Summary.RecordsImported ||
		verification.StagedRecordCount != snapshot.Summary.RecordsImported ||
		verification.ExcludedRecordCount != snapshot.Summary.RecordsCredentialBlocked+snapshot.Summary.RecordsRejected ||
		verification.CutoverAuthorized || len(verification.Mismatches) != 0 {
		t.Fatalf("configured %s migration readback did not reconcile: status=%q state=%q snapshot=%d staged=%d excluded=%d cutover=%t mismatches=%v",
			product, verification.Status, verification.ActualState, verification.SnapshotRecordCount,
			verification.StagedRecordCount, verification.ExcludedRecordCount, verification.CutoverAuthorized, verification.Mismatches)
	}
	var after migrate.Manifest
	runMigrationJSON(t, &after, "migrate", "inspect", "--product", string(product), "--root", absoluteRoot)
	if after.SourceFingerprint != snapshot.SourceFingerprint {
		t.Fatalf("configured %s migration source changed during readback", product)
	}
	t.Logf("reconciled migration round-trip: product=%s files=%d ready=%d credential_path_exclusions=%d records=%d source_bytes=%d",
		product, snapshot.Summary.FilesScanned, snapshot.Summary.FilesReady,
		snapshot.Summary.FilesCredentialBlocked, snapshot.Summary.RecordsImported, snapshot.Summary.TotalSourceBytes)
	return migrationSourceReadbackResult{Snapshot: snapshot, ManifestPath: manifestPath, Verification: verification, Coverage: coverage}
}

func copyMigrationSourceTree(t *testing.T, sourceRoot, backupRoot string) {
	t.Helper()
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		t.Fatalf("create isolated migration backup: %v", err)
	}
	err := filepath.WalkDir(sourceRoot, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("test source tree contains an unreadable or linked path")
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		destination := filepath.Join(backupRoot, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("test source tree contains a non-regular path")
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		input, err := os.Open(sourcePath)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		if outputErr != nil {
			return outputErr
		}
		return os.Chmod(destination, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("make isolated migration backup fixture: %v", err)
	}
}

func runMigrationJSON(t *testing.T, target any, args ...string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 {
		if len(args) >= 2 && args[0] == "migrate" && (args[1] == "apply" || args[1] == "backup-verify") {
			t.Fatalf("migration command %q failed with exit code %d: %s", args[:2], code, strings.TrimSpace(stderr.String()))
		}
		t.Fatalf("migration command %q failed with exit code %d", args[:2], code)
	}
	if err := json.Unmarshal(stdout.Bytes(), target); err != nil {
		t.Fatalf("migration command %q returned an invalid receipt", args[:2])
	}
}
