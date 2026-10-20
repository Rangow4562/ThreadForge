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
