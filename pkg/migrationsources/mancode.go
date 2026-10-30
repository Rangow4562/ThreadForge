package migrationsources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

const MancodeSchemaVersion = "threadforge.mancode-source-set.v1"

const (
	maxMancodeSourceComponents = 1024
	maxMancodeSourceRecords    = 100000
	maxMancodeSourceFiles      = 10000
)

var mancCodeSourceIDPattern = regexp.MustCompile(`^(?:project|git_coordination-[a-f0-9]{64}|non_git_coordination-[a-f0-9]{64})$`)
var mancCodeDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// MancodeReport inventories the project snapshot together with every discovered
// external coordination snapshot. It intentionally contains no source paths or
// workspace identifiers; Snapshot and backup evidence remain component-bound.
type MancodeReport struct {
	SchemaVersion             string             `json:"schema_version"`
	Product                   string             `json:"product"`
	ProjectSourceFingerprint  string             `json:"project_source_fingerprint"`
	ProjectSnapshotSHA256     string             `json:"project_snapshot_sha256"`
	Components                []MancodeComponent `json:"components"`
	DuplicateRecordKeys       int                `json:"duplicate_record_keys"`
	DuplicateRecordKeysSHA256 string             `json:"duplicate_record_keys_sha256,omitempty"`
	InventorySHA256           string             `json:"inventory_sha256"`
}

type MancodeComponent struct {
	SourceID             string `json:"source_id"`
	Role                 string `json:"role"`
	SourceFingerprint    string `json:"source_fingerprint"`
	SnapshotSHA256       string `json:"snapshot_sha256"`
	FilesScanned         int    `json:"files_scanned"`
	SnapshotFiles        int    `json:"snapshot_files"`
	SourceRecords        int    `json:"source_records"`
	ImportedRecords      int    `json:"imported_records"`
	CredentialExclusions int    `json:"credential_exclusions"`
	RejectedRecords      int    `json:"rejected_records"`
}

type mancCodeInventoryPayload struct {
	SchemaVersion             string             `json:"schema_version"`
	Product                   string             `json:"product"`
	ProjectSourceFingerprint  string             `json:"project_source_fingerprint"`
	ProjectSnapshotSHA256     string             `json:"project_snapshot_sha256"`
	Components                []MancodeComponent `json:"components"`
	DuplicateRecordKeys       int                `json:"duplicate_record_keys"`
	DuplicateRecordKeysSHA256 string             `json:"duplicate_record_keys_sha256,omitempty"`
}

// SealMancode computes the deterministic inventory commitment after Components
// and duplicate-key evidence have been populated.
func SealMancode(report *MancodeReport) error {
	if report == nil {
		return errors.New("Mancode source-set report is required")
	}
	report.SchemaVersion = MancodeSchemaVersion
	report.Product = "mancode"
	sort.Slice(report.Components, func(i, j int) bool {
		return report.Components[i].SourceID < report.Components[j].SourceID
	})
	report.InventorySHA256 = mancCodeInventoryDigest(*report)
	return ValidateMancode(*report)
}

func ValidateMancode(report MancodeReport) error {
	if report.SchemaVersion != MancodeSchemaVersion || report.Product != "mancode" ||
		!mancCodeDigestPattern.MatchString(report.ProjectSourceFingerprint) ||
		!mancCodeDigestPattern.MatchString(report.ProjectSnapshotSHA256) ||
		!mancCodeDigestPattern.MatchString(report.InventorySHA256) ||
		len(report.Components) == 0 || len(report.Components) > maxMancodeSourceComponents ||
		report.DuplicateRecordKeys < 0 || report.DuplicateRecordKeys > maxMancodeSourceRecords {
		return errors.New("invalid Mancode source-set header")
	}
	if report.DuplicateRecordKeys == 0 {
		if report.DuplicateRecordKeysSHA256 != "" {
			return errors.New("empty duplicate-key set has an unexpected digest")
		}
	} else if !mancCodeDigestPattern.MatchString(report.DuplicateRecordKeysSHA256) {
		return errors.New("duplicate Mancode record keys require a digest")
	}

	seen := make(map[string]bool, len(report.Components))
	projectCount := 0
	totalRecords := 0
	previousID := ""
	for _, component := range report.Components {
		if component.SourceID == "" || !mancCodeSourceIDPattern.MatchString(component.SourceID) || seen[component.SourceID] ||
			(previousID != "" && component.SourceID <= previousID) ||
			!mancCodeDigestPattern.MatchString(component.SourceFingerprint) ||
			!mancCodeDigestPattern.MatchString(component.SnapshotSHA256) ||
			component.FilesScanned < 0 || component.FilesScanned > maxMancodeSourceFiles ||
			component.SnapshotFiles < 0 || component.SnapshotFiles > component.FilesScanned ||
			component.SourceRecords < 0 || component.SourceRecords > maxMancodeSourceRecords ||
			component.ImportedRecords < 0 || component.ImportedRecords > component.SourceRecords ||
			component.CredentialExclusions < 0 || component.RejectedRecords < 0 ||
			component.SourceRecords != component.ImportedRecords+component.CredentialExclusions+component.RejectedRecords {
			return errors.New("invalid Mancode source-set component")
		}
		switch component.Role {
		case "project":
			projectCount++
			if component.SourceID != "project" {
				return errors.New("project source must use the project source ID")
			}
		case "git_coordination":
			if len(component.SourceID) != len("git_coordination-")+64 || component.SourceID[:len("git_coordination-")] != "git_coordination-" {
				return errors.New("Git coordination source ID does not match its role")
			}
		case "non_git_coordination":
			if len(component.SourceID) != len("non_git_coordination-")+64 || component.SourceID[:len("non_git_coordination-")] != "non_git_coordination-" {
				return errors.New("non-Git coordination source ID does not match its role")
			}
		default:
			return errors.New("unknown Mancode source-set role")
		}
		seen[component.SourceID] = true
		previousID = component.SourceID
		totalRecords += component.SourceRecords
		if totalRecords > maxMancodeSourceRecords {
			return errors.New("Mancode source-set record total exceeds its bound")
		}
		if component.Role == "project" && (component.SourceFingerprint != report.ProjectSourceFingerprint ||
			component.SnapshotSHA256 != report.ProjectSnapshotSHA256) {
			return errors.New("Mancode project component does not match project bindings")
		}
	}
	if projectCount != 1 || mancCodeInventoryDigest(report) != report.InventorySHA256 {
		return errors.New("Mancode source-set inventory commitment does not reconcile")
	}
	return nil
}

func MancodeInventoryDigest(report MancodeReport) string {
	return mancCodeInventoryDigest(report)
}

func mancCodeInventoryDigest(report MancodeReport) string {
	payload := mancCodeInventoryPayload{
		SchemaVersion: report.SchemaVersion, Product: report.Product,
		ProjectSourceFingerprint: report.ProjectSourceFingerprint,
		ProjectSnapshotSHA256:    report.ProjectSnapshotSHA256,
		Components:               report.Components, DuplicateRecordKeys: report.DuplicateRecordKeys,
		DuplicateRecordKeysSHA256: report.DuplicateRecordKeysSHA256,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte("threadforge.mancode-source-set-inventory.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:])
}

func ValidateMancodeComponentsMatch(report MancodeReport, components []MancodeComponent) error {
	if err := ValidateMancode(report); err != nil {
		return err
	}
	if len(components) != len(report.Components) {
		return fmt.Errorf("Mancode source-set component count mismatch")
	}
	for index := range components {
		if components[index] != report.Components[index] {
			return fmt.Errorf("Mancode source-set component %q does not match the inventory", components[index].SourceID)
		}
	}
	return nil
}
