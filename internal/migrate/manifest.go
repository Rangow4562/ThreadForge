package migrate

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/pelletier/go-toml/v2"
	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"gopkg.in/yaml.v3"
)

const SchemaVersion = "threadforge.migration.v1"

type Product string

const (
	Beacon  Product = "beacon"
	Mancode Product = "mancode"
	AnyJev  Product = "anyjev"
	RRSI    Product = "rrsi"
)

type Manifest struct {
	SchemaVersion     string                             `json:"schema_version"`
	ID                string                             `json:"id"`
	Product           Product                            `json:"product"`
	SourceRoot        string                             `json:"source_root"`
	SourceFingerprint string                             `json:"source_fingerprint"`
	MancodeGitRef     *MancodeGitRefSource               `json:"mancode_git_ref,omitempty"`
	CreatedAt         string                             `json:"created_at"`
	Files             []FileEntry                        `json:"files"`
	CrossFileChecks   []migrationcoverage.CrossFileCheck `json:"cross_file_checks,omitempty"`
	Summary           Summary                            `json:"summary"`
}

type FileEntry struct {
	Path           string `json:"path"`
	Virtual        bool   `json:"virtual,omitempty"`
	SourceSHA256   string `json:"source_sha256"`
	SourceBytes    int64  `json:"source_bytes"`
	RecordCount    int    `json:"record_count"`
	ImportedCount  int    `json:"imported_count"`
	ExcludedCount  int    `json:"excluded_count"`
	RejectedCount  int    `json:"rejected_count"`
	Status         string `json:"status"`
	Issue          string `json:"issue,omitempty"`
	SnapshotPath   string `json:"snapshot_path,omitempty"`
	SnapshotSHA256 string `json:"snapshot_sha256,omitempty"`
	SnapshotBytes  int64  `json:"snapshot_bytes,omitempty"`
	JoinedInto     string `json:"joined_into,omitempty"`
}

type Summary struct {
	FilesScanned             int   `json:"files_scanned"`
	FilesReady               int   `json:"files_ready"`
	FilesUnsupported         int   `json:"files_unsupported"`
	FilesJoined              int   `json:"files_joined"`
	FilesCredentialBlocked   int   `json:"files_credential_blocked"`
	RecordsFound             int   `json:"records_found"`
	RecordsImported          int   `json:"records_imported"`
	RecordsCredentialBlocked int   `json:"records_credential_blocked"`
	RecordsRejected          int   `json:"records_rejected"`
	TotalSourceBytes         int64 `json:"total_source_bytes"`
}

type Record struct {
	Key           string          `json:"key"`
	Type          string          `json:"type"`
	SourcePath    string          `json:"source_path"`
	ContentSHA256 string          `json:"content_sha256"`
	Scope         string          `json:"scope"`
	Payload       json.RawMessage `json:"payload"`
}

type scannedFile struct {
	entry             FileEntry
	records           []Record
	sourceData        []byte
	joinedCompanion   *FileEntry
	metadataReadBytes int64
	privacyPolicy     *mancodePrivacyPolicyBindingSource
	privacyExclusions *mancodePrivacyExclusionsBindingSource
}

const (
	maxFileBytes  = 32 << 20
	maxTotalBytes = 1 << 30
	maxFiles      = 10000
	maxRecords    = 100000
	maxSnapshot   = 1 << 30
)

func ParseProduct(value string) (Product, error) {
	p := Product(strings.ToLower(strings.TrimSpace(value)))
	switch p {
	case Beacon, Mancode, AnyJev, RRSI:
		return p, nil
	default:
		return "", fmt.Errorf("product must be beacon, mancode, anyjev, or rrsi")
	}
}

func Inspect(product Product, sourceRoot string) (Manifest, error) {
	return inspect(product, sourceRoot, nil)
}

func InspectWithOptions(product Product, sourceRoot string, options InspectOptions) (Manifest, error) {
	return inspectWithOptions(product, sourceRoot, nil, options)
}

func inspect(product Product, sourceRoot string, scannedSink *[]scannedFile) (Manifest, error) {
	return inspectWithOptions(product, sourceRoot, scannedSink, InspectOptions{})
}

func inspectWithOptions(product Product, sourceRoot string, scannedSink *[]scannedFile, options InspectOptions) (Manifest, error) {
	if _, err := ParseProduct(string(product)); err != nil {
		return Manifest{}, err
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return Manifest{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return Manifest{}, fmt.Errorf("stat migration source root: %w", err)
	}
	if !info.IsDir() {
		return Manifest{}, errors.New("migration source root must be a directory")
	}
	var gitRefSource *MancodeGitRefSource
	var gitRefData []byte
	var gitRefWorkspaceID string
	if options.IncludeMancodeGitRef {
		if product != Mancode {
			return Manifest{}, errors.New("--include-mancode-git-ref is valid only for Mancode")
		}
		source, data, workspaceID, err := readMancodeGitRef(root)
		if err != nil {
			return Manifest{}, err
		}
		gitRefSource, gitRefData, gitRefWorkspaceID = &source, data, workspaceID
	}
	manifest := Manifest{SchemaVersion: SchemaVersion, Product: product, SourceRoot: root,
		MancodeGitRef: gitRefSource, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Files: []FileEntry{}}
	var scanned []scannedFile
	var totalBytes, expandedTotalBytes int64
	var rrsiTrialReadBytes int64
	joinedCompanions := make(map[string]FileEntry)
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if product == RRSI && isRRSIWorktreeDirectory(rel) {
				return filepath.SkipDir
			}
			if rel != "." && ignoredDirectory(filepath.Base(name)) &&
				!(product == Mancode && isMancodeWorkflowCheckpointDirectory(rel, root)) {
				return filepath.SkipDir
			}
			return nil
		}
		if companion, joined := joinedCompanions[rel]; joined {
			companionData, current, readErr := readSafeRRSIFile(root, rel, maxRRSITrialFileBytes)
			if readErr != nil || current.SourceBytes != companion.SourceBytes || current.SourceSHA256 != companion.SourceSHA256 ||
				int64(len(companionData)) != companion.SourceBytes {
				return fmt.Errorf("RRSI joined companion changed during inspection: %s", rel)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if !eligiblePath(product, rel, root) {
			return nil
		}
		manifest.Summary.FilesScanned++
		if manifest.Summary.FilesScanned > maxFiles {
			return errors.New("migration source exceeds 10000-file limit")
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		totalBytes += fileInfo.Size()
		if totalBytes > maxTotalBytes {
			return errors.New("migration source exceeds 1 GiB total-file limit")
		}
		manifest.Summary.TotalSourceBytes += fileInfo.Size()
		if sensitivePath(rel) {
			hash, hashErr := hashFile(name)
			if hashErr != nil {
				return hashErr
			}
			manifest.Summary.FilesCredentialBlocked++
			scanned = append(scanned, scannedFile{entry: FileEntry{
				Path: rel, SourceSHA256: hash, SourceBytes: fileInfo.Size(),
				Status: "credential_path_excluded", Issue: "credential-bearing path excluded",
			}})
			return nil
		}
		if product == RRSI && !isRRSIMetadataPath(rel) {
			hash, hashErr := hashFile(name)
			if hashErr != nil {
				return hashErr
			}
			scanned = append(scanned, scannedFile{entry: FileEntry{
				Path: rel, SourceSHA256: hash, SourceBytes: fileInfo.Size(), Status: "unsupported",
				Issue: "RRSI artifact omitted pending a source-specific privacy converter",
			}})
			manifest.Summary.FilesUnsupported++
			return nil
		}
		if product == RRSI && isRRSITrialMetadataPath(rel) &&
			(fileInfo.Size() > maxRRSITrialFileBytes || fileInfo.Size() > maxRRSITrialReadBytes-rrsiTrialReadBytes) {
			hash, hashErr := hashFile(name)
			if hashErr != nil {
				return hashErr
			}
			scanned = append(scanned, scannedFile{entry: FileEntry{
				Path: rel, SourceSHA256: hash, SourceBytes: fileInfo.Size(), Status: "unsupported",
				Issue: "RRSI trial metadata exceeds bounded conversion limits",
			}})
			manifest.Summary.FilesUnsupported++
			return nil
		}
		if fileInfo.Size() < 0 || fileInfo.Size() > maxFileBytes {
			hash, hashErr := hashFile(name)
			if hashErr != nil {
				return hashErr
			}
			scanned = append(scanned, scannedFile{entry: FileEntry{Path: rel, SourceSHA256: hash, SourceBytes: fileInfo.Size(), Status: "unsupported", Issue: "file exceeds 32 MiB limit"}})
			manifest.Summary.FilesUnsupported++
			return nil
		}
		var data []byte
		if product == RRSI && isRRSITrialMetadataPath(rel) {
			var readEntry FileEntry
			data, readEntry, err = readSafeRRSIFile(root, rel, maxRRSITrialFileBytes)
			if err != nil || readEntry.SourceBytes != fileInfo.Size() || !validDigest(readEntry.SourceSHA256) {
				return fmt.Errorf("RRSI trial metadata changed during inspection: %s", rel)
			}
		} else {
			data, err = os.ReadFile(name)
			if err != nil {
				return err
			}
		}
		file, expandedBytes, err := scanFileWithMancodeGitRef(product, root, rel, name, data,
			maxTotalBytes-expandedTotalBytes, maxRRSITrialReadBytes-rrsiTrialReadBytes-int64(len(data)), gitRefSource, gitRefWorkspaceID)
		if product == RRSI && isRRSITrialMetadataPath(rel) {
			rrsiTrialReadBytes += int64(len(data)) + file.metadataReadBytes
			if rrsiTrialReadBytes > maxRRSITrialReadBytes {
				return errors.New("RRSI trial metadata exceeds 1 GiB aggregate read limit")
			}
		}
		if expandedBytes > maxTotalBytes-expandedTotalBytes {
			return errors.New("migration source exceeds 1 GiB expanded-byte limit")
		}
		expandedTotalBytes += expandedBytes
		if file.joinedCompanion != nil {
			companion := *file.joinedCompanion
			if companion.Status != "joined_companion" || companion.JoinedInto != rel ||
				manifest.Summary.FilesScanned >= maxFiles {
				return errors.New("RRSI engineering companion manifest entry is invalid")
			}
			if companion.SourceBytes < 0 || totalBytes > maxTotalBytes-companion.SourceBytes {
				return errors.New("migration source exceeds 1 GiB total-file limit")
			}
			totalBytes += companion.SourceBytes
			manifest.Summary.TotalSourceBytes += companion.SourceBytes
			manifest.Summary.FilesScanned++
			manifest.Summary.FilesJoined++
			joinedCompanions[companion.Path] = companion
			scanned = append(scanned, scannedFile{entry: companion})
		}
		if err != nil {
			file = scannedFile{entry: FileEntry{Path: rel, SourceSHA256: digest(data), SourceBytes: int64(len(data)), Status: "unsupported", Issue: safeIssue(err)}}
		} else if file.entry.ImportedCount > 0 {
			manifest.Summary.FilesReady++
		} else if file.entry.Status == "ready" {
			file.entry.Status = "empty_or_credential_blocked"
		}
		manifest.Summary.RecordsFound += file.entry.RecordCount
		if manifest.Summary.RecordsFound > maxRecords {
			return errors.New("migration source exceeds 100000-record limit")
		}
		manifest.Summary.RecordsImported += file.entry.ImportedCount
		manifest.Summary.RecordsCredentialBlocked += file.entry.ExcludedCount
		manifest.Summary.RecordsRejected += file.entry.RejectedCount
		if file.entry.Status == "unsupported" {
			manifest.Summary.FilesUnsupported++
		}
		scanned = append(scanned, file)
		return nil
	})
	if err != nil {
		return Manifest{}, fmt.Errorf("scan migration source: %w", err)
	}
	if gitRefSource != nil {
		virtualPath := filepath.Join(root, filepath.FromSlash(mancodeGitRefVirtualPath))
		if _, err := os.Lstat(virtualPath); err == nil {
			return Manifest{}, errors.New("migration source contains a reserved Mancode git-ref virtual path")
		} else if !errors.Is(err, os.ErrNotExist) {
			return Manifest{}, errors.New("reserved Mancode git-ref virtual path could not be checked safely")
		}
		if manifest.Summary.FilesScanned >= maxFiles || int64(len(gitRefData)) > maxTotalBytes-totalBytes {
			return Manifest{}, errors.New("Mancode git-ref source exceeds migration source limits")
		}
		manifest.Summary.FilesScanned++
		manifest.Summary.TotalSourceBytes += int64(len(gitRefData))
		totalBytes += int64(len(gitRefData))
		file, expandedBytes, err := scanFileWithMancodeGitRef(Mancode, root, mancodeGitRefVirtualPath, "", gitRefData,
			maxTotalBytes-expandedTotalBytes, maxRRSITrialReadBytes-rrsiTrialReadBytes-int64(len(gitRefData)), gitRefSource, gitRefWorkspaceID)
		if err != nil {
			return Manifest{}, fmt.Errorf("inspect Mancode team ref manifest: %w", err)
		}
		if expandedBytes > maxTotalBytes-expandedTotalBytes {
			return Manifest{}, errors.New("Mancode team ref manifest exceeds expanded-byte limits")
		}
		expandedTotalBytes += expandedBytes
		file.entry.Virtual = true
		file.sourceData = append([]byte(nil), gitRefData...)
		manifest.Summary.RecordsFound += file.entry.RecordCount
		manifest.Summary.RecordsImported += file.entry.ImportedCount
		manifest.Summary.RecordsCredentialBlocked += file.entry.ExcludedCount
		manifest.Summary.RecordsRejected += file.entry.RejectedCount
		if manifest.Summary.RecordsFound > maxRecords {
			return Manifest{}, errors.New("migration source exceeds 100000-record limit")
		}
		if file.entry.ImportedCount > 0 {
			manifest.Summary.FilesReady++
		}
		if file.entry.Status == "unsupported" {
			manifest.Summary.FilesUnsupported++
		}
		scanned = append(scanned, file)
	}
	if product == Mancode {
		added, err := appendMancodeWorkflowAggregates(root, &scanned)
		if err != nil {
			return Manifest{}, fmt.Errorf("reconstruct Mancode workflow aggregates: %w", err)
		}
		manifest.Summary.RecordsFound += added
		manifest.Summary.RecordsImported += added
		if manifest.Summary.RecordsFound > maxRecords {
			return Manifest{}, errors.New("migration source exceeds 100000-record limit")
		}
		manifest.CrossFileChecks = bindMancodePrivacyPolicyExclusions(&scanned)
	}
	sort.Slice(scanned, func(i, j int) bool { return scanned[i].entry.Path < scanned[j].entry.Path })
	manifest.Files = make([]FileEntry, len(scanned))
	for i := range scanned {
		manifest.Files[i] = scanned[i].entry
	}
	manifest.SourceFingerprint = sourceFingerprint(manifest.Files, manifest.CrossFileChecks, manifest.MancodeGitRef)
	manifest.ID = string(product) + "-" + manifest.SourceFingerprint[:20]
	if scannedSink != nil {
		*scannedSink = append([]scannedFile(nil), scanned...)
	}
	return manifest, nil
}

func CreateSnapshot(product Product, sourceRoot, dataRoot string) (Manifest, error) {
	return CreateSnapshotWithOptions(product, sourceRoot, dataRoot, InspectOptions{})
}

func CreateSnapshotWithOptions(product Product, sourceRoot, dataRoot string, options InspectOptions) (Manifest, error) {
	var inspectedFiles []scannedFile
	manifest, err := inspectWithOptions(product, sourceRoot, &inspectedFiles, options)
	if err != nil {
		return Manifest{}, err
	}
	for _, entry := range manifest.Files {
		if product == Beacon && isBeaconLearningDBPath(entry.Path) {
			fullPath := filepath.Join(manifest.SourceRoot, filepath.FromSlash(entry.Path))
			if err := ensureNoBeaconSQLiteSidecars(fullPath); err != nil {
				return Manifest{}, err
			}
		}
	}
	root := filepath.Join(dataRoot, "migrations", "snapshots", string(product), manifest.SourceFingerprint)
	manifestPath := filepath.Join(root, "manifest.json")
	if existing, err := loadSnapshot(manifestPath, false); err == nil {
		if err := ensurePrivateArtifactSnapshot(root, manifest.SourceRoot, existing); err != nil {
			return Manifest{}, err
		}
		return LoadSnapshot(manifestPath)
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(errors.Unwrap(err), os.ErrNotExist) {
		if _, statErr := os.Stat(root); statErr == nil {
			return Manifest{}, fmt.Errorf("existing migration snapshot is unreadable: %w", err)
		}
	}
	temp := root + ".tmp-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		return Manifest{}, fmt.Errorf("create private migration directory: %w", err)
	}
	if err := os.Mkdir(temp, 0o700); err != nil {
		return Manifest{}, err
	}
	manifestFiles := make(map[string]FileEntry, len(manifest.Files))
	for _, entry := range manifest.Files {
		manifestFiles[entry.Path] = entry
	}
	inspectedByPath := make(map[string]scannedFile, len(inspectedFiles))
	for _, file := range inspectedFiles {
		inspectedByPath[file.entry.Path] = file
	}
	var rrsiTrialReadBytes int64
	var privateArtifacts []PrivateArtifactReference
	gitRefWorkspaceID := ""
	if manifest.MancodeGitRef != nil {
		var mode string
		gitRefWorkspaceID, mode, err = readMancodeGitRefProjectConfig(manifest.SourceRoot)
		if err != nil || mode != "git-ref" {
			return Manifest{}, errors.New("Mancode git-ref project binding changed during snapshot")
		}
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.RemoveAll(temp)
		}
	}()
	for i := range manifest.Files {
		entry := &manifest.Files[i]
		if entry.ImportedCount == 0 || entry.Status == "unsupported" {
			continue
		}
		fullPath := filepath.Join(manifest.SourceRoot, filepath.FromSlash(entry.Path))
		var data []byte
		if entry.Virtual {
			data = inspectedByPath[entry.Path].sourceData
			if int64(len(data)) != entry.SourceBytes || digest(data) != entry.SourceSHA256 {
				return Manifest{}, fmt.Errorf("virtual migration source changed during snapshot: %s", entry.Path)
			}
		} else if product == RRSI && isRRSITrialMetadataPath(entry.Path) {
			if entry.SourceBytes > maxRRSITrialFileBytes || entry.SourceBytes > maxRRSITrialReadBytes-rrsiTrialReadBytes {
				return Manifest{}, errors.New("RRSI trial metadata exceeds bounded snapshot read limits")
			}
			var current FileEntry
			data, current, err = readSafeRRSIFile(manifest.SourceRoot, entry.Path, maxRRSITrialFileBytes)
			if err == nil && (current.SourceBytes != entry.SourceBytes || current.SourceSHA256 != entry.SourceSHA256) {
				err = errors.New("source changed during snapshot")
			}
			rrsiTrialReadBytes += int64(len(data))
		} else {
			data, err = os.ReadFile(fullPath)
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("source changed during snapshot: %s", entry.Path)
		}
		if digest(data) != entry.SourceSHA256 {
			return Manifest{}, fmt.Errorf("source changed during snapshot: %s; re-run inspect", entry.Path)
		}
		parsed, _, err := scanFileWithMancodeGitRef(product, manifest.SourceRoot, entry.Path, fullPath, data,
			maxTotalBytes, maxRRSITrialReadBytes-rrsiTrialReadBytes, manifest.MancodeGitRef, gitRefWorkspaceID)
		if err != nil {
			return Manifest{}, fmt.Errorf("re-read migration source %s: %w", entry.Path, err)
		}
		rrsiTrialReadBytes += parsed.metadataReadBytes
		if rrsiTrialReadBytes > maxRRSITrialReadBytes {
			return Manifest{}, errors.New("RRSI trial metadata exceeds 1 GiB aggregate snapshot read limit")
		}
		if parsed.joinedCompanion != nil {
			companion, exists := manifestFiles[parsed.joinedCompanion.Path]
			if !exists || companion.Status != "joined_companion" || companion.JoinedInto != entry.Path ||
				companion.SourceBytes != parsed.joinedCompanion.SourceBytes ||
				companion.SourceSHA256 != parsed.joinedCompanion.SourceSHA256 {
				return Manifest{}, fmt.Errorf("joined RRSI source changed during snapshot: %s", entry.Path)
			}
		}
		var derived []Record
		if product == Mancode {
			derived = mancodeWorkflowAggregateRecords(inspectedByPath[entry.Path])
		}
		if len(parsed.records)+len(derived) != entry.ImportedCount {
			return Manifest{}, fmt.Errorf("record count changed during snapshot: %s", entry.Path)
		}
		if product == AnyJev && isAnyJevDistillationNPZPath(entry.Path) {
			artifact := PrivateArtifactReference{
				SourcePath: entry.Path, SHA256: entry.SourceSHA256, Bytes: entry.SourceBytes,
				LocalPath: privateArtifactLocalPath(entry.SourceSHA256),
			}
			if err := writeOrVerifyPrivateArtifact(temp, artifact.LocalPath, data, artifact.SHA256); err != nil {
				return Manifest{}, err
			}
			privateArtifacts = append(privateArtifacts, artifact)
		}
		records := append(append([]Record(nil), parsed.records...), derived...)
		if product == Mancode && isMancodePrivacyPolicyPath(entry.Path) {
			bound := inspectedByPath[entry.Path].records
			if len(bound) != len(parsed.records) {
				return Manifest{}, fmt.Errorf("privacy binding changed during snapshot: %s", entry.Path)
			}
			records = append([]Record(nil), bound...)
		}
		encoded, err := encodeRecordLines(records)
		if err != nil {
			return Manifest{}, err
		}
		if int64(len(encoded)) > maxSnapshot {
			return Manifest{}, errors.New("migration snapshot exceeds 1 GiB limit")
		}
		var priorBytes int64
		for _, previous := range manifest.Files[:i] {
			if previous.SnapshotBytes > maxSnapshot-priorBytes {
				return Manifest{}, errors.New("migration snapshot exceeds 1 GiB limit")
			}
			priorBytes += previous.SnapshotBytes
		}
		if int64(len(encoded)) > maxSnapshot-priorBytes {
			return Manifest{}, errors.New("migration snapshot exceeds 1 GiB limit")
		}
		rel := filepath.ToSlash(filepath.Join("records", entry.Path+".jsonl"))
		path := filepath.Join(temp, filepath.FromSlash(rel))
		if err := writePrivateFile(path, encoded); err != nil {
			return Manifest{}, err
		}
		entry.SnapshotPath = rel
		entry.SnapshotSHA256 = digest(encoded)
		entry.SnapshotBytes = int64(len(encoded))
	}
	if len(privateArtifacts) > 0 {
		privateArtifacts = sortedPrivateArtifacts(privateArtifacts)
		if err := writeOrVerifyPrivateArtifactManifest(temp, manifest, privateArtifacts); err != nil {
			return Manifest{}, err
		}
		if err := validatePrivateArtifactSnapshot(temp, manifest); err != nil {
			return Manifest{}, err
		}
	}
	current, err := InspectWithOptions(product, manifest.SourceRoot, options)
	if err != nil || current.SourceFingerprint != manifest.SourceFingerprint {
		return Manifest{}, errors.New("source changed during snapshot; re-run inspect and snapshot")
	}
	if err := VerifyManifest(manifest); err != nil {
		return Manifest{}, err
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := writePrivateFile(filepath.Join(temp, "manifest.json"), append(manifestData, '\n')); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(temp, root); err != nil {
		if existing, loadErr := LoadSnapshot(filepath.Join(root, "manifest.json")); loadErr == nil && existing.SourceFingerprint == manifest.SourceFingerprint {
			return existing, nil
		}
		return Manifest{}, fmt.Errorf("commit migration snapshot: %w", err)
	}
	keepTemp = true
	return LoadSnapshot(filepath.Join(root, "manifest.json"))
}

func LoadSnapshot(manifestPath string) (Manifest, error) {
	return loadSnapshot(manifestPath, true)
}

func loadSnapshot(manifestPath string, validatePrivateArtifacts bool) (Manifest, error) {
	if filepath.Base(manifestPath) != "manifest.json" {
		return Manifest{}, errors.New("migration manifest must be named manifest.json")
	}
	root, err := filepath.Abs(filepath.Dir(manifestPath))
	if err != nil {
		return Manifest{}, err
	}
	data, err := readSnapshotFile(root, "manifest.json", -1, 16<<20)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode migration manifest: %w", err)
	}
	if err := VerifyManifest(manifest); err != nil {
		return Manifest{}, err
	}
	var totalSnapshotBytes int64
	for _, file := range manifest.Files {
		if file.ImportedCount == 0 {
			if file.SnapshotPath != "" || file.SnapshotSHA256 != "" || file.SnapshotBytes != 0 {
				return Manifest{}, fmt.Errorf("unexpected snapshot fields in %s", file.Path)
			}
			continue
		}
		expectedPath := filepath.ToSlash(filepath.Join("records", file.Path+".jsonl"))
		if file.SnapshotPath != expectedPath {
			return Manifest{}, fmt.Errorf("snapshot path does not match source file %s", file.Path)
		}
		if file.SnapshotBytes > maxSnapshot-totalSnapshotBytes {
			return Manifest{}, errors.New("migration snapshot exceeds 1 GiB limit")
		}
		totalSnapshotBytes += file.SnapshotBytes
		snapshotData, err := readSnapshotFile(root, file.SnapshotPath, file.SnapshotBytes, maxSnapshot)
		if err != nil || digest(snapshotData) != file.SnapshotSHA256 {
			return Manifest{}, fmt.Errorf("snapshot checksum mismatch: %s", file.SnapshotPath)
		}
	}
	if validatePrivateArtifacts {
		if err := validatePrivateArtifactSnapshot(root, manifest); err != nil {
			return Manifest{}, err
		}
	}
	return manifest, nil
}

func ReadSnapshotRecords(manifestPath string, manifest Manifest) ([]Record, error) {
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		return nil, err
	}
	if manifest.ID != loaded.ID || manifest.SourceFingerprint != loaded.SourceFingerprint {
		return nil, errors.New("provided migration manifest does not match the verified snapshot")
	}
	manifest = loaded
	root, err := filepath.Abs(filepath.Dir(manifestPath))
	if err != nil {
		return nil, err
	}
	var records []Record
	seenKeys := make(map[string]struct{}, manifest.Summary.RecordsImported)
	fileEntries := make(map[string]FileEntry, len(manifest.Files))
	for _, file := range manifest.Files {
		fileEntries[file.Path] = file
	}
	for _, file := range manifest.Files {
		if file.SnapshotPath == "" {
			continue
		}
		data, err := readSnapshotFile(root, file.SnapshotPath, file.SnapshotBytes, maxSnapshot)
		if err != nil {
			return nil, err
		}
		if digest(data) != file.SnapshotSHA256 {
			return nil, fmt.Errorf("snapshot checksum mismatch: %s", file.SnapshotPath)
		}
		lines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
		fileRecordCount := 0
		for _, line := range lines {
			if len(line) == 0 {
				continue
			}
			var record Record
			if err := json.Unmarshal(line, &record); err != nil {
				return nil, fmt.Errorf("decode snapshot record: %w", err)
			}
			if err := validateSnapshotRecord(record, file, manifest.Product, manifest.SourceRoot); err != nil {
				return nil, fmt.Errorf("snapshot record failed integrity/privacy validation: %s", record.Key)
			}
			if manifest.Product == Mancode && record.Type == "mancode_workflow_aggregate" {
				if err := validateMancodeWorkflowAggregate(record, fileEntries); err != nil {
					return nil, fmt.Errorf("Mancode workflow aggregate failed source reconciliation: %s", record.Key)
				}
			}
			if manifest.Product == RRSI && record.Type == "rrsi_trial_artifact" {
				if err := validateRRSITrialPayload(record, file, fileEntries); err != nil {
					return nil, fmt.Errorf("RRSI trial record failed source reconciliation: %s", record.Key)
				}
			}
			if manifest.Product == RRSI && record.Type == "rrsi_trajectory_summary" {
				if err := validateRRSITrajectoryPayload(record, file); err != nil {
					return nil, fmt.Errorf("RRSI trajectory record failed source reconciliation: %s", record.Key)
				}
			}
			if _, duplicate := seenKeys[record.Key]; duplicate {
				return nil, fmt.Errorf("duplicate snapshot record key: %s", record.Key)
			}
			seenKeys[record.Key] = struct{}{}
			fileRecordCount++
			records = append(records, record)
		}
		if fileRecordCount != file.ImportedCount {
			return nil, fmt.Errorf("snapshot record count mismatch: %s", file.SnapshotPath)
		}
	}
	if len(records) != manifest.Summary.RecordsImported {
		return nil, errors.New("snapshot total record count does not reconcile with manifest")
	}
	if manifest.Product == Mancode {
		if err := validateMancodeWorkflowAggregateSet(records, manifest.Files); err != nil {
			return nil, fmt.Errorf("Mancode workflow aggregate set failed reconciliation: %w", err)
		}
	}
	return records, nil
}

func validateSnapshotRecord(record Record, file FileEntry, product Product, sourceRoot string) error {
	aliasedRRSIRecord := product == RRSI && (record.Type == "rrsi_trial_artifact" || record.Type == "rrsi_trajectory_summary")
	aliasedMancodeCoordinationRecord := product == Mancode && isMancodeCoordinationRecordType(record.Type)
	aliasedMancodeLegacyWorkflowRecord := product == Mancode && record.Type == "mancode_legacy_workflow_summary"
	aliasedMancodeLegacyRequirementsRecord := product == Mancode && record.Type == "mancode_legacy_requirements"
	aliasedMancodeLegacyReviewRecord := product == Mancode && record.Type == "mancode_legacy_review_summary"
	aliasedMancodeLegacyVerificationRecord := product == Mancode && record.Type == "mancode_legacy_verification_summary"
	aliasedMancodeLegacyPlanRecord := product == Mancode && record.Type == "mancode_legacy_workflow_plan"
	wantMancodeAlias := ""
	if aliasedMancodeCoordinationRecord {
		wantMancodeAlias = mancodeCoordinationSourceAlias(file.Path, record.Type)
	}
	wantLegacyWorkflowAlias := ""
	if aliasedMancodeLegacyWorkflowRecord {
		wantLegacyWorkflowAlias = mancodeLegacyWorkflowSourceAlias(file.Path)
	}
	wantLegacyRequirementsAlias := ""
	if aliasedMancodeLegacyRequirementsRecord {
		wantLegacyRequirementsAlias = mancodeLegacyRequirementsSourceAlias(file.Path)
	}
	wantLegacyReviewAlias := ""
	if aliasedMancodeLegacyReviewRecord {
		wantLegacyReviewAlias = mancodeLegacyReviewSourceAlias(file.Path)
	}
	wantLegacyVerificationAlias := ""
	if aliasedMancodeLegacyVerificationRecord {
		wantLegacyVerificationAlias = mancodeLegacyVerificationSourceAlias(file.Path)
	}
	wantLegacyPlanAlias := ""
	if aliasedMancodeLegacyPlanRecord {
		wantLegacyPlanAlias = mancodeLegacyPlanSourceAlias(file.Path)
	}
	if !safeRelative(record.SourcePath) ||
		(!aliasedRRSIRecord && !aliasedMancodeCoordinationRecord && !aliasedMancodeLegacyWorkflowRecord && !aliasedMancodeLegacyRequirementsRecord && !aliasedMancodeLegacyReviewRecord && !aliasedMancodeLegacyVerificationRecord && !aliasedMancodeLegacyPlanRecord && record.SourcePath != file.Path) ||
		(aliasedMancodeCoordinationRecord && record.SourcePath != wantMancodeAlias) ||
		(aliasedMancodeLegacyWorkflowRecord && record.SourcePath != wantLegacyWorkflowAlias) ||
		(aliasedMancodeLegacyRequirementsRecord && record.SourcePath != wantLegacyRequirementsAlias) ||
		(aliasedMancodeLegacyReviewRecord && record.SourcePath != wantLegacyReviewAlias) ||
		(aliasedMancodeLegacyVerificationRecord && record.SourcePath != wantLegacyVerificationAlias) ||
		(aliasedMancodeLegacyPlanRecord && record.SourcePath != wantLegacyPlanAlias) ||
		!json.Valid(record.Payload) || digest(record.Payload) != record.ContentSHA256 || privacy.ContainsCredential(record.Payload) ||
		record.Scope != "local_pending_review" || !knownRecordType(record.Type) || !recordTypeMatchesSource(product, file.Path, record.Type, sourceRoot) {
		return errors.New("record fields are not valid for this migration")
	}
	prefix := file.Path + "#"
	if aliasedRRSIRecord || aliasedMancodeCoordinationRecord || aliasedMancodeLegacyWorkflowRecord || aliasedMancodeLegacyRequirementsRecord || aliasedMancodeLegacyReviewRecord || aliasedMancodeLegacyVerificationRecord || aliasedMancodeLegacyPlanRecord {
		prefix = record.SourcePath + "#"
	}
	if !strings.HasPrefix(record.Key, prefix) {
		return errors.New("record key does not match source file")
	}
	indexText := strings.TrimPrefix(record.Key, prefix)
	if len(indexText) != 6 {
		return errors.New("record key has an invalid source index")
	}
	index, err := strconv.Atoi(indexText)
	if err != nil || index < 1 || index > file.RecordCount {
		return errors.New("record key source index is out of range")
	}
	return nil
}

func knownRecordType(value string) bool {
	switch value {
	case "telemetry_event", "endpoint_configuration", "project_fact", "project_glossary", "project_policy",
		"workflow_state", "workflow_document", "decision_record", "project_record", "decision_label_or_evaluation",
		"decision_calibration_artifact", "decision_adaptation_state", "decision_bundle_metadata", "evolution_history", "noise_calibration", "evolution_frontier",
		"evaluation_result", "evolution_proposal", "selection_decision", "evolution_artifact", "decision_synthetic_dataset_summary",
		"beacon_learning_evaluation", "beacon_learning_candidate", "beacon_learning_memory", "beacon_opencode_plugin_presence", "beacon_hook_log_summary",
		"mancode_legacy_state", "mancode_project_profile", "mancode_legacy_config", "mancode_project_config", "mancode_schema_manifest",
		"mancode_actor_identity", "mancode_actor_profile", "mancode_team_policy", "mancode_team_event",
		"mancode_team_claim", "mancode_team_handoff", "mancode_team_head_fence", "mancode_operation_audit",
		"mancode_git_ref_authority", "mancode_git_ref_receipt", "mancode_git_ref_task_bundle", "mancode_git_ref_authority_transition",
		"mancode_progress_projection", "workflow_metadata", "workflow_requirements", "workflow_review",
		"workflow_verification", "workflow_plan", "workflow_checkpoint",
		"mancode_workflow_aggregate", "mancode_legacy_workflow_summary", "mancode_legacy_requirements", "mancode_legacy_review_summary", "mancode_legacy_verification_summary", "mancode_legacy_workflow_plan",
		"rrsi_evaluation_result", "rrsi_edit_history", "rrsi_attribution", "rrsi_selection_decision", "rrsi_trial_artifact",
		"rrsi_trajectory_summary":
		return true
	default:
		return false
	}
}

func recordTypeMatchesSource(product Product, relative, recordType, sourceRoot string) bool {
	if product == Mancode && recordType == "mancode_legacy_workflow_summary" {
		_, ok := isMancodeLegacyWorkflowMetadataPath(relative, sourceRoot)
		return ok
	}
	if product == Mancode && recordType == "mancode_legacy_requirements" {
		_, ok := isMancodeLegacyWorkflowRequirementsPath(relative, sourceRoot)
		return ok
	}
	if product == Mancode && recordType == "mancode_legacy_review_summary" {
		_, ok := isMancodeLegacyWorkflowReviewPath(relative, sourceRoot)
		return ok
	}
	if product == Mancode && recordType == "mancode_legacy_verification_summary" {
		_, ok := isMancodeLegacyWorkflowVerificationPath(relative, sourceRoot)
		return ok
	}
	if product == Mancode && recordType == "mancode_legacy_workflow_plan" {
		_, ok := isMancodeLegacyWorkflowPlanPath(relative, sourceRoot)
		return ok
	}
	if product == Mancode && relative == mancodeGitRefVirtualPath {
		switch recordType {
		case "mancode_actor_identity", "mancode_actor_profile", "mancode_team_claim", "mancode_team_handoff",
			"mancode_team_head_fence", "mancode_git_ref_authority", "mancode_git_ref_receipt", "mancode_git_ref_task_bundle",
			"mancode_git_ref_authority_transition",
			"workflow_metadata", "workflow_requirements", "workflow_review", "workflow_verification", "workflow_plan",
			"workflow_checkpoint", "workflow_document", "project_policy":
			return true
		default:
			return false
		}
	}
	if product == Beacon && recordType == "beacon_hook_log_summary" {
		return isBeaconHookLogPath(relative, sourceRoot)
	}
	if classifyRecord(product, relative) == recordType {
		return true
	}
	if product == Mancode && recordType == "mancode_workflow_aggregate" {
		_, ok := mancodeWorkflowMetadataLocation(relative, "")
		if !ok {
			_, ok = mancodeWorkflowMetadataLocation(relative, ".mancode")
		}
		return ok
	}
	if product == Beacon && isBeaconLearningDBPath(relative) {
		switch recordType {
		case "beacon_learning_evaluation", "beacon_learning_candidate", "beacon_learning_memory":
			return true
		}
	}
	if product == AnyJev && strings.Contains("/"+strings.ToLower(relative)+"/", "/anyjev-heads/") {
		switch recordType {
		case "decision_calibration_artifact", "decision_adaptation_state", "decision_label_or_evaluation", "decision_bundle_metadata":
			return true
		}
	}
	if product == AnyJev && recordType == "decision_synthetic_dataset_summary" {
		return isAnyJevDistillationSyntheticJSONPath(relative)
	}
	return false
}

func readSnapshotFile(root, relative string, expectedBytes, maxBytes int64) ([]byte, error) {
	if !safeRelative(relative) || expectedBytes < -1 || maxBytes < 0 || maxBytes > maxSnapshot || expectedBytes > maxBytes {
		return nil, errors.New("unsafe or oversized snapshot path")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	for _, candidate := range append([]string{root}, snapshotPathParts(root, relative)...) {
		info, err := os.Lstat(candidate)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("snapshot path contains a symbolic link")
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	within, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) || filepath.IsAbs(within) {
		return nil, errors.New("snapshot path resolves outside the snapshot root")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxBytes ||
		(expectedBytes >= 0 && info.Size() != expectedBytes) {
		return nil, errors.New("snapshot file metadata mismatch")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() > maxBytes ||
		(expectedBytes >= 0 && opened.Size() != expectedBytes) {
		return nil, errors.New("snapshot file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes || (expectedBytes >= 0 && int64(len(data)) != expectedBytes) {
		return nil, errors.New("snapshot file size changed while reading")
	}
	return data, nil
}

func snapshotPathParts(root, relative string) []string {
	current := root
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	return paths
}

func VerifyManifest(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion {
		return errors.New("unsupported migration manifest schema")
	}
	if _, err := ParseProduct(string(manifest.Product)); err != nil {
		return err
	}
	if !validDigest(manifest.SourceFingerprint) || len(manifest.Files) > maxFiles ||
		migrationcoverage.ValidateCrossFileChecks(string(manifest.Product), manifest.CrossFileChecks) != nil ||
		validateMancodePrivacyManifestChecks(manifest) != nil ||
		manifest.SourceFingerprint != sourceFingerprint(manifest.Files, manifest.CrossFileChecks, manifest.MancodeGitRef) {
		return errors.New("migration source fingerprint is invalid")
	}
	if manifest.ID != string(manifest.Product)+"-"+manifest.SourceFingerprint[:20] {
		return errors.New("migration ID does not match product and source fingerprint")
	}
	imported, found, blocked, rejected := 0, 0, 0, 0
	filesReady, filesUnsupported, filesCredentialBlocked, filesJoined := 0, 0, 0, 0
	var totalBytes int64
	seenPaths := make(map[string]bool, len(manifest.Files))
	fileByPath := make(map[string]FileEntry, len(manifest.Files))
	virtualGitRefFiles := 0
	for _, file := range manifest.Files {
		if !safeRelative(file.Path) || !validDigest(file.SourceSHA256) || seenPaths[file.Path] {
			return fmt.Errorf("invalid source manifest entry %q", file.Path)
		}
		if file.Virtual {
			if manifest.Product != Mancode || manifest.MancodeGitRef == nil || file.Path != mancodeGitRefVirtualPath {
				return errors.New("migration manifest contains an unsupported virtual source file")
			}
			virtualGitRefFiles++
		} else if file.Path == mancodeGitRefVirtualPath {
			return errors.New("reserved Mancode git-ref virtual path is not marked virtual")
		}
		seenPaths[file.Path] = true
		fileByPath[file.Path] = file
		if file.SourceBytes < 0 || file.SourceBytes > maxTotalBytes || file.ImportedCount < 0 || file.ExcludedCount < 0 || file.RejectedCount < 0 ||
			file.RecordCount != file.ImportedCount+file.ExcludedCount+file.RejectedCount {
			return fmt.Errorf("invalid record counts in %s", file.Path)
		}
		if file.SnapshotPath != "" && (!safeRelative(file.SnapshotPath) || !validDigest(file.SnapshotSHA256) || file.SnapshotBytes < 0) {
			return fmt.Errorf("invalid snapshot fields in %s", file.Path)
		}
		if file.Status != "ready" && file.Status != "partially_excluded" && file.Status != "partially_rejected" &&
			file.Status != "credential_excluded" && file.Status != "credential_path_excluded" &&
			file.Status != "unsupported" && file.Status != "empty_or_credential_blocked" && file.Status != "joined_companion" {
			return fmt.Errorf("invalid migration status %q in %s", file.Status, file.Path)
		}
		if file.Status == "joined_companion" {
			expectedPath, valid := rrsiEngineeringVerdictPath(file.JoinedInto)
			if manifest.Product != RRSI || !safeRelative(file.JoinedInto) || !valid || expectedPath != file.Path || file.RecordCount != 0 ||
				file.SnapshotPath != "" || file.SnapshotSHA256 != "" || file.SnapshotBytes != 0 {
				return fmt.Errorf("invalid joined RRSI companion entry %q", file.Path)
			}
			filesJoined++
		} else if file.JoinedInto != "" {
			return fmt.Errorf("unexpected joined target on migration entry %q", file.Path)
		}
		if found > maxRecords-file.RecordCount || totalBytes > maxTotalBytes-file.SourceBytes {
			return errors.New("migration manifest exceeds source limits")
		}
		imported += file.ImportedCount
		found += file.RecordCount
		blocked += file.ExcludedCount
		rejected += file.RejectedCount
		totalBytes += file.SourceBytes
		if file.ImportedCount > 0 {
			filesReady++
		}
		if file.Status == "unsupported" {
			filesUnsupported++
		}
		if file.Status == "credential_path_excluded" {
			filesCredentialBlocked++
		}
	}
	if manifest.MancodeGitRef != nil {
		if manifest.Product != Mancode || manifest.MancodeGitRef.Ref != mancodeTeamGitRef ||
			!mancodeGitRefObjectIDPattern.MatchString(manifest.MancodeGitRef.Commit) ||
			!validDigest(manifest.MancodeGitRef.ManifestSHA256) || manifest.MancodeGitRef.ManifestBytes < 1 ||
			manifest.MancodeGitRef.ManifestBytes > maxMancodeGitRefManifest || virtualGitRefFiles != 1 {
			return errors.New("migration manifest Mancode git-ref binding is invalid")
		}
		virtual, ok := fileByPath[mancodeGitRefVirtualPath]
		if !ok || !virtual.Virtual || virtual.SourceSHA256 != manifest.MancodeGitRef.ManifestSHA256 ||
			virtual.SourceBytes != manifest.MancodeGitRef.ManifestBytes {
			return errors.New("migration manifest Mancode git-ref blob does not match its source binding")
		}
	} else if virtualGitRefFiles != 0 {
		return errors.New("migration manifest virtual source is missing its Mancode git-ref binding")
	}
	for _, file := range manifest.Files {
		if file.Status != "joined_companion" {
			continue
		}
		primary, exists := fileByPath[file.JoinedInto]
		if !exists || primary.Status != "ready" || primary.ImportedCount != 1 || !isRRSIEngineeringMetaPath(primary.Path) {
			return fmt.Errorf("joined RRSI companion %q has no imported metadata record", file.Path)
		}
	}
	if len(manifest.Files) != manifest.Summary.FilesScanned || filesReady != manifest.Summary.FilesReady ||
		filesUnsupported != manifest.Summary.FilesUnsupported || filesCredentialBlocked != manifest.Summary.FilesCredentialBlocked ||
		filesJoined != manifest.Summary.FilesJoined ||
		imported != manifest.Summary.RecordsImported || found != manifest.Summary.RecordsFound ||
		blocked != manifest.Summary.RecordsCredentialBlocked || rejected != manifest.Summary.RecordsRejected ||
		totalBytes != manifest.Summary.TotalSourceBytes {
		return errors.New("migration record totals do not reconcile with file entries")
	}
	return nil
}

func scanFile(product Product, sourceRoot, relative, fullPath string, data []byte, expandedBudget, rrsiReadBudget int64) (scannedFile, int64, error) {
	return scanFileWithMancodeGitRef(product, sourceRoot, relative, fullPath, data, expandedBudget, rrsiReadBudget, nil, "")
}

func scanFileWithMancodeGitRef(product Product, sourceRoot, relative, fullPath string, data []byte,
	expandedBudget, rrsiReadBudget int64, gitRefSource *MancodeGitRefSource, gitRefWorkspaceID string) (scannedFile, int64, error) {
	entry := FileEntry{Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data)), Status: "ready"}
	if product == Beacon && isBeaconLearningDBPath(relative) {
		file, err := scanBeaconLearningDatabase(relative, fullPath, data)
		return file, int64(len(data)), err
	}
	if product == Mancode && isMancodeProgressHTMLPath(relative) {
		file := scannedFile{entry: entry}
		file.entry.RecordCount = 1
		payload, err := normalizeMancodeProgressHTML(data)
		if err != nil {
			file.entry.RejectedCount = 1
			file.entry.Issue = safeIssue(err)
		} else if privacy.ContainsCredential(payload) {
			file.entry.ExcludedCount = 1
		} else {
			file.records = append(file.records, Record{
				Key: relative + "#000001", Type: "mancode_progress_projection", SourcePath: relative,
				ContentSHA256: digest(payload), Scope: "local_pending_review", Payload: payload,
			})
		}
		finalizeScannedFile(&file)
		return file, int64(len(data)), nil
	}
	if product == RRSI && isRRSIProposalPath(relative) {
		file := scanRRSIProposalFile(relative, data)
		return file, int64(len(data)), nil
	}
	if product == Beacon && isBeaconOpenCodePluginPath(relative, sourceRoot) {
		file := scannedFile{entry: entry}
		file.entry.RecordCount = 1
		payload, err := normalizeBeaconOpenCodePlugin(data)
		if err != nil {
			file.entry.RejectedCount = 1
			file.entry.Issue = safeIssue(err)
		} else {
			file.records = append(file.records, Record{
				Key: relative + "#000001", Type: "beacon_opencode_plugin_presence", SourcePath: relative,
				ContentSHA256: digest(payload), Scope: "local_pending_review", Payload: payload,
			})
		}
		finalizeScannedFile(&file)
		return file, int64(len(data)), nil
	}
	if product == Beacon && isBeaconHookLogPath(relative, sourceRoot) {
		file := scannedFile{entry: entry}
		file.entry.RecordCount = 1
		payload, err := normalizeBeaconHookLog(relative, sourceRoot, data)
		if err != nil {
			file.entry.RejectedCount = 1
			file.entry.Issue = safeIssue(err)
		} else {
			file.records = append(file.records, Record{
				Key: relative + "#000001", Type: "beacon_hook_log_summary", SourcePath: relative,
				ContentSHA256: digest(payload), Scope: "local_pending_review", Payload: payload,
			})
		}
		finalizeScannedFile(&file)
		return file, int64(len(data)), nil
	}
	if product == RRSI && isRRSITrialMetadataPath(relative) {
		item, companion, companionBytes, err := normalizeRRSITrialFile(sourceRoot, relative, data, rrsiReadBudget)
		file := scannedFile{entry: entry, joinedCompanion: companion, metadataReadBytes: companionBytes}
		if err != nil {
			return file, int64(len(data)), err
		}
		file.entry.RecordCount = 1
		if privacy.ContainsCredential(item.Payload) {
			file.entry.ExcludedCount = 1
		} else {
			var payload map[string]any
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				return file, int64(len(data)), errors.New("invalid normalized RRSI trial record")
			}
			alias := rrsiTrialSourceAlias(stringValue(payload["domain"]), stringValue(payload["job_id_sha256"]),
				stringValue(payload["task_id_sha256"]), stringValue(payload["trial_key_sha256"]))
			if item.Type == "rrsi_trajectory_summary" {
				alias = rrsiTrajectorySourceAlias(stringValue(payload["domain"]), stringValue(payload["job_id_sha256"]),
					stringValue(payload["task_id_sha256"]), stringValue(payload["trial_key_sha256"]))
			}
			file.records = append(file.records, Record{
				Key: alias + "#000001", Type: item.Type, SourcePath: alias,
				ContentSHA256: digest(item.Payload), Scope: "local_pending_review", Payload: item.Payload,
			})
		}
		finalizeScannedFile(&file)
		return file, int64(len(data)), nil
	}
	if product == AnyJev && isAnyJevDistillationNPZPath(relative) {
		file := scannedFile{entry: entry}
		file.entry.RecordCount = 1
		item, err := normalizeAnyJevDistillationNPZ(relative, data)
		if err != nil {
			file.entry.RejectedCount = 1
			file.entry.Issue = safeIssue(err)
		} else if privacy.ContainsCredential(item.Payload) {
			file.entry.ExcludedCount = 1
		} else {
			file.records = append(file.records, Record{
				Key: relative + "#000001", Type: item.Type, SourcePath: relative,
				ContentSHA256: digest(item.Payload), Scope: "local_pending_review", Payload: item.Payload,
			})
		}
		finalizeScannedFile(&file)
		return file, int64(len(data)), nil
	}
	decoded := data
	expandedBytes := int64(len(data))
	var err error
	if product == Beacon {
		decoded, expandedBytes, err = decodeBeaconGzipJSONL(relative, data, expandedBudget)
		if err != nil {
			return scannedFile{}, expandedBytes, err
		}
	} else if product == AnyJev && isAnyJevItemDumpGzipPath(relative) {
		decoded, expandedBytes, err = decodeAnyJevItemDumpGzipJSON(relative, data, expandedBudget)
		if err != nil {
			return scannedFile{}, expandedBytes, err
		}
	}
	var items []any
	if product == Mancode {
		items, err = decodeMancodeSourceItems(relative, decoded)
	} else if product == AnyJev && isAnyJevItemDumpJSONPath(relative) {
		decodePath := relative
		if strings.HasSuffix(strings.ToLower(relative), ".items.json.gz") {
			decodePath = strings.TrimSuffix(relative, ".gz")
		}
		items, err = decodeSourceItems(decodePath, decoded)
	} else {
		items, err = decodeSourceItems(relative, decoded)
	}
	if err != nil {
		return scannedFile{}, expandedBytes, err
	}
	file := scannedFile{entry: entry}
	recordIndex := 0
	unreconciledMancodeGitRef := false
	for _, value := range items {
		var normalized []normalizedItem
		var err error
		if product == Mancode && relative == mancodeGitRefVirtualPath {
			if gitRefSource == nil || gitRefWorkspaceID == "" {
				return scannedFile{}, expandedBytes, errors.New("Mancode git-ref manifest requires an explicitly bound local ref")
			}
			normalized, err = normalizeMancodeGitRefManifest(sourceRoot, value, *gitRefSource, gitRefWorkspaceID)
		} else {
			normalized, err = normalizeItems(product, sourceRoot, relative, value)
		}
		if err != nil {
			file.entry.RecordCount++
			recordIndex++
			file.entry.RejectedCount++
			continue
		}
		if product == Mancode && gitRefSource == nil && mancodeGitRefAuthorityNeedsReconciliation(sourceRoot, relative, value) {
			unreconciledMancodeGitRef = true
		}
		for _, item := range normalized {
			if item.Rejected {
				file.entry.RecordCount++
				recordIndex++
				file.entry.RejectedCount++
				if file.entry.Issue == "" && item.Issue != "" {
					file.entry.Issue = item.Issue
				}
				continue
			}
			if item.privacyPolicy != nil {
				if file.privacyPolicy != nil {
					return scannedFile{}, expandedBytes, errors.New("Mancode privacy policy source has multiple policy objects")
				}
				file.privacyPolicy = item.privacyPolicy
			}
			if item.privacyExclusions != nil {
				if file.privacyExclusions != nil {
					return scannedFile{}, expandedBytes, errors.New("Mancode privacy exclusions source has multiple exclusion objects")
				}
				file.privacyExclusions = item.privacyExclusions
			}
			file.entry.RecordCount++
			recordIndex++
			if privacy.ContainsCredential(item.Payload) {
				file.entry.ExcludedCount++
				continue
			}
			hash := digest(item.Payload)
			recordPath := relative
			if product == Mancode && isMancodeCoordinationRecordType(item.Type) {
				recordPath = mancodeCoordinationSourceAlias(relative, item.Type)
			} else if product == Mancode && item.Type == "mancode_legacy_workflow_summary" {
				recordPath = mancodeLegacyWorkflowSourceAlias(relative)
			} else if product == Mancode && item.Type == "mancode_legacy_requirements" {
				recordPath = mancodeLegacyRequirementsSourceAlias(relative)
			} else if product == Mancode && item.Type == "mancode_legacy_review_summary" {
				recordPath = mancodeLegacyReviewSourceAlias(relative)
			} else if product == Mancode && item.Type == "mancode_legacy_verification_summary" {
				recordPath = mancodeLegacyVerificationSourceAlias(relative)
			} else if product == Mancode && item.Type == "mancode_legacy_workflow_plan" {
				recordPath = mancodeLegacyPlanSourceAlias(relative)
			}
			file.records = append(file.records, Record{
				Key: fmt.Sprintf("%s#%06d", recordPath, recordIndex), Type: item.Type, SourcePath: recordPath,
				ContentSHA256: hash, Scope: "local_pending_review", Payload: item.Payload,
			})
		}
	}
	if unreconciledMancodeGitRef {
		file.entry.RecordCount++
		file.entry.RejectedCount++
		file.entry.Issue = mancodeGitRefAuthorityIssue
	}
	finalizeScannedFile(&file)
	return file, expandedBytes, nil
}

func finalizeScannedFile(file *scannedFile) {
	file.entry.ImportedCount = len(file.records)
	if file.entry.ExcludedCount > 0 {
		file.entry.Status = "partially_excluded"
	}
	if file.entry.RejectedCount > 0 && file.entry.ImportedCount > 0 {
		file.entry.Status = "partially_rejected"
	}
	if file.entry.ImportedCount == 0 && file.entry.ExcludedCount > 0 {
		file.entry.Status = "credential_excluded"
	}
	if file.entry.ImportedCount == 0 && file.entry.RejectedCount > 0 {
		file.entry.Status = "unsupported"
		file.entry.Issue = "all source records failed format or contract validation"
	}
}

type normalizedItem struct {
	Type              string
	Payload           json.RawMessage
	Rejected          bool
	Issue             string
	privacyPolicy     *mancodePrivacyPolicyBindingSource
	privacyExclusions *mancodePrivacyExclusionsBindingSource
}

func normalizeItems(product Product, sourceRoot, relative string, value any) ([]normalizedItem, error) {
	if product == Beacon && classifyRecord(product, relative) == "endpoint_configuration" {
		payload, err := normalizeBeaconEndpointConfig(relative, sourceRoot, value)
		if err != nil {
			return nil, err
		}
		return []normalizedItem{{Type: "endpoint_configuration", Payload: payload}}, nil
	}
	if product == Mancode {
		items, handled, err := normalizeMancodeItems(sourceRoot, relative, value)
		if handled || err != nil {
			return items, err
		}
	}
	if product == AnyJev {
		items, handled, err := normalizeAnyJevItems(relative, value)
		if handled || err != nil {
			return items, err
		}
	}
	if product == RRSI {
		items, handled, err := normalizeRRSIItems(relative, value)
		if handled || err != nil {
			return items, err
		}
	}
	kind, payload, err := normalizeItem(product, relative, value)
	if err != nil {
		return nil, err
	}
	return []normalizedItem{{Type: kind, Payload: payload}}, nil
}

func decodeBeaconGzipJSONL(relative string, data []byte, expandedBudget int64) ([]byte, int64, error) {
	base := strings.ToLower(filepath.Base(relative))
	if !strings.HasSuffix(base, ".gz") ||
		!(strings.HasPrefix(base, "runtime.jsonl.") || strings.HasPrefix(base, "inventory_state.jsonl.")) {
		return data, int64(len(data)), nil
	}
	if expandedBudget < 0 {
		return nil, 0, errors.New("migration source exceeds expanded-byte limit")
	}
	limit := int64(maxFileBytes)
	if expandedBudget < limit {
		limit = expandedBudget
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, 0, errors.New("invalid gzip-compressed Beacon JSONL")
	}
	decoded, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := reader.Close()
	expandedBytes := int64(len(decoded))
	if expandedBytes > limit {
		if expandedBudget < maxFileBytes {
			return nil, expandedBytes, errors.New("migration source exceeds 1 GiB expanded-byte limit")
		}
		return nil, expandedBytes, errors.New("expanded Beacon JSONL exceeds 32 MiB per-file limit")
	}
	if readErr != nil || closeErr != nil {
		return nil, expandedBytes, errors.New("invalid gzip-compressed Beacon JSONL")
	}
	return decoded, expandedBytes, nil
}

func decodeSourceItems(relative string, data []byte) ([]any, error) {
	ext := strings.ToLower(filepath.Ext(relative))
	base := strings.ToLower(filepath.Base(relative))
	if strings.HasPrefix(base, "runtime.jsonl.") || strings.HasPrefix(base, "inventory_state.jsonl.") {
		ext = ".jsonl"
	}
	switch ext {
	case ".jsonl":
		var items []any
		for lineNumber, line := range bytes.Split(data, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var value any
			if err := json.Unmarshal(line, &value); err != nil {
				return nil, fmt.Errorf("invalid JSONL at line %d", lineNumber+1)
			}
			items = append(items, value)
		}
		return items, nil
	case ".json":
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, errors.New("invalid JSON file")
		}
		if values, ok := value.([]any); ok {
			return values, nil
		}
		return []any{value}, nil
	case ".yaml", ".yml":
		var value any
		if err := yaml.Unmarshal(data, &value); err != nil {
			return nil, errors.New("invalid YAML file")
		}
		normalized, err := normalizeYAML(value)
		if err != nil {
			return nil, err
		}
		return []any{normalized}, nil
	case ".toml":
		var value map[string]any
		if err := toml.Unmarshal(data, &value); err != nil {
			return nil, errors.New("invalid TOML file")
		}
		return []any{value}, nil
	case ".md", ".mdx", ".txt":
		return []any{map[string]any{"content": string(data)}}, nil
	case ".csv":
		reader := csv.NewReader(bytes.NewReader(data))
		reader.FieldsPerRecord = -1
		records, err := reader.ReadAll()
		if err != nil || len(records) < 2 {
			return nil, errors.New("CSV requires a header and at least one data row")
		}
		var items []any
		for _, row := range records[1:] {
			item := make(map[string]any, len(records[0]))
			for i, key := range records[0] {
				if i < len(row) && strings.TrimSpace(key) != "" {
					item[strings.TrimSpace(key)] = row[i]
				}
			}
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported file extension %q", ext)
	}
}

func normalizeYAML(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func normalizeItem(product Product, relative string, value any) (string, json.RawMessage, error) {
	kind := classifyRecord(product, relative)
	if product == Beacon && kind == "telemetry_event" {
		data, err := json.Marshal(value)
		if err != nil {
			return "", nil, err
		}
		var event beacon.Event
		if err := json.Unmarshal(data, &event); err != nil {
			return "", nil, errors.New("invalid Beacon event")
		}
		event = beacon.SanitizeEvent(event, beacon.DefaultMaxEventBytes)
		if err := event.Validate(); err != nil {
			return "", nil, errors.New("invalid Beacon event contract")
		}
		data, err = json.Marshal(event)
		return kind, data, err
	}
	data, err := json.Marshal(value)
	return kind, data, err
}

func classifyRecord(product Product, relative string) string {
	base := strings.ToLower(filepath.Base(relative))
	switch product {
	case Beacon:
		if isBeaconOpenCodePluginRelativePath(relative) {
			return "beacon_opencode_plugin_presence"
		}
		if strings.HasPrefix(base, "runtime.jsonl") || strings.HasPrefix(base, "inventory_state.jsonl") {
			return "telemetry_event"
		}
		return "endpoint_configuration"
	case Mancode:
		return classifyMancodeRecord(relative)
	case AnyJev:
		lower := "/" + strings.ToLower(strings.Trim(relative, "/")) + "/"
		if strings.Contains(lower, "/labels/") || strings.Contains(lower, "/bench/results") || strings.Contains(lower, "/demo/results/") {
			return "decision_label_or_evaluation"
		}
		return "decision_calibration_artifact"
	case RRSI:
		if isRRSIProposalPath(relative) {
			return "evolution_proposal"
		}
		if isRRSITrialMetadataPath(relative) {
			if source, ok := rrsiTrialSourceForPath(relative); ok && isRRSITrajectoryKind(source.kind) {
				return "rrsi_trajectory_summary"
			}
			return "rrsi_trial_artifact"
		}
		switch base {
		case "history.jsonl":
			return "rrsi_edit_history"
		case "attribution.jsonl":
			return "rrsi_attribution"
		case "calibration.json":
			return "noise_calibration"
		case "frontier.json":
			return "evolution_frontier"
		case "eval.json":
			return "rrsi_evaluation_result"
		case "decisions.json":
			return "rrsi_selection_decision"
		default:
			return "evolution_artifact"
		}
	default:
		return "legacy_record"
	}
}

func eligiblePath(product Product, relative, sourceRoot string) bool {
	lower := "/" + strings.ToLower(strings.TrimPrefix(relative, "./"))
	base := strings.ToLower(filepath.Base(relative))
	switch product {
	case Beacon:
		return strings.HasPrefix(base, "runtime.jsonl") || strings.HasPrefix(base, "inventory_state.jsonl") ||
			isBeaconOpenCodePluginPath(relative, sourceRoot) || isBeaconHookLogPath(relative, sourceRoot) ||
			isBeaconEndpointConfigPath(relative, sourceRoot) || isBeaconClaudeSettingsPath(relative, sourceRoot) ||
			isBeaconQwenSettingsPath(relative, sourceRoot) || isBeaconFactorySettingsPath(relative, sourceRoot) ||
			isBeaconCursorHooksPath(relative, sourceRoot) || isBeaconCodexHooksPath(relative, sourceRoot) ||
			isBeaconCodexOTelConfigPath(relative, sourceRoot) || isBeaconGeminiSettingsPath(relative, sourceRoot) ||
			isBeaconKimiCodeConfigPath(relative, sourceRoot) ||
			isBeaconMuseManagedHooksPath(relative, sourceRoot) || isBeaconMuseSettingsPath(relative, sourceRoot) ||
			isBeaconAntigravityHooksPath(relative, sourceRoot) || isBeaconOpenHandsHooksPath(relative, sourceRoot) ||
			isBeaconKiroHooksPath(relative, sourceRoot) ||
			(isBeaconLearningDBPath(relative) && (strings.HasSuffix(lower, "/.beacon/endpoint/memory.db") ||
				(relative == "memory.db" && strings.EqualFold(filepath.Base(sourceRoot), "endpoint") &&
					strings.EqualFold(filepath.Base(filepath.Dir(sourceRoot)), ".beacon"))))
	case Mancode:
		return eligibleMancodePath(relative, sourceRoot)
	case AnyJev:
		return (strings.Contains(lower, "/anyjev-heads/") || strings.Contains(lower, "/artifacts/") ||
			strings.Contains(lower, "/labels/") || strings.Contains(lower, "/bench/results") ||
			strings.Contains(lower, "/demo/results/")) &&
			(extIs(relative, ".json", ".jsonl", ".csv", ".md") || isAnyJevItemDumpJSONPath(relative) ||
				isAnyJevDistillationNPZPath(relative))
	case RRSI:
		_, supportedDomain := rrsiDomain(relative)
		return supportedDomain && (isRRSIRawTrialArtifactPath(relative) || extIs(relative, ".json", ".jsonl", ".md"))
	default:
		return false
	}
}

func extIs(path string, extensions ...string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, extension := range extensions {
		if ext == extension {
			return true
		}
	}
	return false
}

func ignoredDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "vendor", "__pycache__", ".venv", "venv", "models", "checkpoints", "cache", "caches":
		return true
	default:
		return false
	}
}

func sensitivePath(relative string) bool {
	for _, segment := range strings.Split(strings.ToLower(relative), "/") {
		if segment == ".env" || strings.Contains(segment, "credential") || strings.Contains(segment, "secret") ||
			strings.Contains(segment, "private-key") || strings.Contains(segment, "private_key") ||
			segment == "id_rsa" || segment == "id_ed25519" || strings.Contains(segment, "auth-token") {
			return true
		}
	}
	return false
}

func encodeRecordLines(records []Record) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return nil, err
		}
	}
	return output.Bytes(), nil
}

func writePrivateFile(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func sourceFingerprint(files []FileEntry, checks []migrationcoverage.CrossFileCheck, gitRefs ...*MancodeGitRefSource) string {
	var digestInput strings.Builder
	for _, file := range files {
		digestInput.WriteString(file.Path)
		digestInput.WriteByte(0)
		digestInput.WriteString(file.SourceSHA256)
		digestInput.WriteByte(0)
		digestInput.WriteString(fmt.Sprintf("%d", file.SourceBytes))
		digestInput.WriteByte('\n')
	}
	if len(checks) > 0 {
		encoded, _ := json.Marshal(checks)
		digestInput.WriteString("cross-file-checks-v1\x00")
		digestInput.Write(encoded)
		digestInput.WriteByte('\n')
	}
	if len(gitRefs) > 0 && gitRefs[0] != nil {
		encoded, _ := json.Marshal(gitRefs[0])
		digestInput.WriteString("mancode-git-ref-source-v1\x00")
		digestInput.Write(encoded)
		digestInput.WriteByte('\n')
	}
	return digest([]byte(digestInput.String()))
}

func digest(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}

func hashFile(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func safeRelative(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	return clean == value && clean != "." && !strings.HasPrefix(clean, "../") && clean != ".."
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func safeIssue(err error) string {
	if err == nil {
		return "unsupported_source_format"
	}
	return "source_validation_failed"
}
