package migrationsources

import (
	"strings"
	"testing"
)

func TestSealAndValidateMancodeSourceSetInventory(t *testing.T) {
	report := MancodeReport{
		ProjectSourceFingerprint: strings.Repeat("a", 64),
		ProjectSnapshotSHA256:    strings.Repeat("b", 64),
		Components: []MancodeComponent{
			{
				SourceID: "project", Role: "project", SourceFingerprint: strings.Repeat("a", 64),
				SnapshotSHA256: strings.Repeat("b", 64), FilesScanned: 0, SnapshotFiles: 0,
			},
			{
				SourceID: "git_coordination-" + strings.Repeat("c", 64), Role: "git_coordination",
				SourceFingerprint: strings.Repeat("d", 64), SnapshotSHA256: strings.Repeat("e", 64),
				FilesScanned: 1, SnapshotFiles: 1, SourceRecords: 2, ImportedRecords: 1, CredentialExclusions: 1,
			},
		},
	}
	if err := SealMancode(&report); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMancode(report); err != nil {
		t.Fatalf("sealed source-set report is invalid: %v", err)
	}
	if report.InventorySHA256 == "" || report.SchemaVersion != MancodeSchemaVersion {
		t.Fatalf("source-set inventory was not sealed: %#v", report)
	}
}

func TestValidateMancodeSourceSetRequiresCompleteUniqueRolesAndDigestBinding(t *testing.T) {
	base := validMancodeSourceSetForTest(t)
	missingProject := base
	missingProject.Components = nil
	for _, component := range base.Components {
		if component.Role != "project" {
			missingProject.Components = append(missingProject.Components, component)
		}
	}
	missingProject.InventorySHA256 = MancodeInventoryDigest(missingProject)
	if err := ValidateMancode(missingProject); err == nil {
		t.Fatal("source set without project root was accepted")
	}

	duplicate := base
	duplicate.Components = append(append([]MancodeComponent(nil), base.Components...), base.Components[1])
	duplicate.InventorySHA256 = MancodeInventoryDigest(duplicate)
	if err := ValidateMancode(duplicate); err == nil {
		t.Fatal("duplicate source ID was accepted")
	}

	tampered := base
	tampered.Components = append([]MancodeComponent(nil), base.Components...)
	tampered.Components[0].FilesScanned++
	if err := ValidateMancode(tampered); err == nil {
		t.Fatal("source-set content changed without updating the inventory digest")
	}
}

func TestValidateMancodeSourceSetRequiresDigestForDuplicateRecordKeys(t *testing.T) {
	report := validMancodeSourceSetForTest(t)
	report.DuplicateRecordKeys = 1
	report.DuplicateRecordKeysSHA256 = ""
	report.InventorySHA256 = MancodeInventoryDigest(report)
	if err := ValidateMancode(report); err == nil {
		t.Fatal("duplicate record keys without a path-free commitment were accepted")
	}
	report.DuplicateRecordKeysSHA256 = strings.Repeat("f", 64)
	report.InventorySHA256 = MancodeInventoryDigest(report)
	if err := ValidateMancode(report); err != nil {
		t.Fatalf("valid duplicate record-key commitment was rejected: %v", err)
	}
}

func validMancodeSourceSetForTest(t *testing.T) MancodeReport {
	t.Helper()
	report := MancodeReport{
		ProjectSourceFingerprint: strings.Repeat("a", 64),
		ProjectSnapshotSHA256:    strings.Repeat("b", 64),
		Components: []MancodeComponent{
			{SourceID: "project", Role: "project", SourceFingerprint: strings.Repeat("a", 64), SnapshotSHA256: strings.Repeat("b", 64)},
			{SourceID: "non_git_coordination-" + strings.Repeat("c", 64), Role: "non_git_coordination", SourceFingerprint: strings.Repeat("d", 64), SnapshotSHA256: strings.Repeat("e", 64)},
		},
	}
	if err := SealMancode(&report); err != nil {
		t.Fatal(err)
	}
	return report
}
