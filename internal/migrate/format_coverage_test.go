package migrate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
)

func TestBuildFormatCoverageReconcilesAndRedactsManifestPaths(t *testing.T) {
	manifest := coverageTestManifest(Beacon, []FileEntry{{
		Path: ".beacon/runtime.jsonl", SourceSHA256: digest([]byte("source")), SourceBytes: 19,
		RecordCount: 1, ImportedCount: 1, Status: "ready", SnapshotPath: "records/runtime.jsonl.jsonl",
		SnapshotSHA256: digest([]byte("snapshot")), SnapshotBytes: 42,
	}})

	report, err := BuildFormatCoverage(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationcoverage.Validate(report); err != nil {
		t.Fatalf("builder produced invalid format coverage: %v", err)
	}
	if report.Product != "beacon" || report.FilesScanned != 1 || report.SnapshotFiles != 1 ||
		report.RecordsFound != 1 || report.RecordsImported != 1 || report.UnclassifiedFiles != 0 {
		t.Fatalf("unexpected coverage totals: %#v", report)
	}
	var runtimeFormat *migrationcoverage.Format
	for i := range report.Formats {
		if report.Formats[i].ID == "beacon.runtime-events" {
			runtimeFormat = &report.Formats[i]
		}
	}
	if runtimeFormat == nil || runtimeFormat.Files != 1 || runtimeFormat.RecordsImported != 1 {
		t.Fatalf("runtime event source was not attributed to its format family: %#v", runtimeFormat)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), manifest.Files[0].Path) || strings.Contains(string(encoded), manifest.SourceRoot) {
		t.Fatalf("coverage artifact exposed a local path: %s", encoded)
	}
}

func TestBuildFormatCoverageSurfacesUnclassifiedAndRejectedFiles(t *testing.T) {
	manifest := coverageTestManifest(AnyJev, []FileEntry{{
		Path: "unexpected/source.json", SourceSHA256: digest([]byte("source")), SourceBytes: 7,
		RecordCount: 1, RejectedCount: 1, Status: "unsupported",
	}})

	report, err := BuildFormatCoverage(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if report.UnclassifiedFiles != 1 || report.RejectedRecords != 1 || report.UnsupportedFiles != 1 {
		t.Fatalf("unknown source format was not surfaced: %#v", report)
	}
	if err := migrationcoverage.Validate(report); err != nil {
		t.Fatalf("explicit unclassified coverage should remain inspectable: %v", err)
	}
}

func TestMigrationFormatIDUsesDocumentedSourceSelectors(t *testing.T) {
	tests := []struct {
		product Product
		root    string
		path    string
		want    string
	}{
		{Beacon, "C:/home", ".beacon/runtime.jsonl", "beacon.runtime-events"},
		{Beacon, "C:/home", ".claude/settings.json", "beacon.endpoint-claude"},
		{Mancode, "C:/project", ".mancode/shared/memory/decisions/decision.json", "mancode.decision-record"},
		{Mancode, "C:/project", "项目进度.html", "mancode.progress-projection"},
		{AnyJev, "C:/AnyJev", "bench/results_exit/model.depth.json", "anyjev.exit-depth"},
		{RRSI, "C:/RRSI", "runs/coding/jobs/job-1/t1/result.json", "rrsi.coding-trial-result"},
		{RRSI, "C:/RRSI", "runs/coding/jobs/job-1/task__t1/agent/trajectory.json", "rrsi.trajectory"},
		{RRSI, "C:/RRSI", "runs/workspace/jobs/job-1/s0/task-1/trajectory.json", "rrsi.trajectory"},
		{RRSI, "C:/RRSI", "runs/eng/jobs/job-1/task-1/t0/traj.json", "rrsi.trajectory"},
	}
	for _, test := range tests {
		t.Run(string(test.product)+"/"+test.path, func(t *testing.T) {
			if got := migrationFormatID(test.product, test.root, test.path); got != test.want {
				t.Fatalf("migrationFormatID(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestFormatCoverageRejectsAggregateDrift(t *testing.T) {
	manifest := coverageTestManifest(Beacon, []FileEntry{{
		Path: "runtime.jsonl", SourceSHA256: digest([]byte("source")), SourceBytes: 19,
		RecordCount: 1, ImportedCount: 1, Status: "ready", SnapshotPath: "records/runtime.jsonl.jsonl",
		SnapshotSHA256: digest([]byte("snapshot")), SnapshotBytes: 42,
	}})
	report, err := BuildFormatCoverage(manifest)
	if err != nil {
		t.Fatal(err)
	}
	report.Formats[0].RecordsFound++
	if err := migrationcoverage.Validate(report); err == nil {
		t.Fatal("format row count drift was accepted")
	}
}

func coverageTestManifest(product Product, files []FileEntry) Manifest {
	manifest := Manifest{SchemaVersion: SchemaVersion, Product: product, SourceRoot: "local-source", Files: files}
	for _, file := range files {
		manifest.Summary.FilesScanned++
		manifest.Summary.TotalSourceBytes += file.SourceBytes
		manifest.Summary.RecordsFound += file.RecordCount
		manifest.Summary.RecordsImported += file.ImportedCount
		manifest.Summary.RecordsCredentialBlocked += file.ExcludedCount
		manifest.Summary.RecordsRejected += file.RejectedCount
		if file.ImportedCount > 0 {
			manifest.Summary.FilesReady++
		}
		if file.Status == "unsupported" {
			manifest.Summary.FilesUnsupported++
		}
		if file.Status == "credential_path_excluded" {
			manifest.Summary.FilesCredentialBlocked++
		}
		if file.Status == "joined_companion" {
			manifest.Summary.FilesJoined++
		}
	}
	manifest.SourceFingerprint = sourceFingerprint(manifest.Files)
	manifest.ID = string(product) + "-" + manifest.SourceFingerprint[:20]
	return manifest
}
