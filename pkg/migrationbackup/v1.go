package migrationbackup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/internal/migrate"
)

const SchemaVersion = "threadforge.migration-source-backup-verification.v1"

const (
	maxFiles      = 10000
	maxEntries    = 100000
	maxFileBytes  = int64(32 << 20)
	maxTotalBytes = int64(1 << 30)
)

type Report struct {
	SchemaVersion            string `json:"schema_version"`
	Product                  string `json:"product"`
	SourceFingerprint        string `json:"source_fingerprint"`
	CurrentSourceFingerprint string `json:"current_source_fingerprint"`
	SnapshotSHA256           string `json:"snapshot_sha256"`
	SourceTreeSHA256         string `json:"source_tree_sha256"`
	BackupTreeSHA256         string `json:"backup_tree_sha256"`
	SourceFiles              int    `json:"source_files"`
	BackupFiles              int    `json:"backup_files"`
	SourceBytes              int64  `json:"source_bytes"`
	BackupBytes              int64  `json:"backup_bytes"`
	DifferentFiles           int    `json:"different_files"`
	SnapshotSourceMismatches int    `json:"snapshot_source_mismatches"`
	SharedFiles              int    `json:"shared_files"`
	Status                   string `json:"status"`
}

type fileState struct {
	size   int64
	mode   uint32
	digest string
	info   os.FileInfo
}

type treeState struct {
	root   string
	files  map[string]fileState
	sha256 string
	bytes  int64
}

// VerifySnapshot compares the entire regular-file trees selected by a verified
// migration snapshot. It emits no source or backup paths and never copies data.
func VerifySnapshot(manifestPath, backupRoot string) (Report, error) {
	manifest, err := migrate.LoadSnapshot(manifestPath)
	if err != nil {
		return Report{}, errors.New("migration snapshot is unavailable or invalid")
	}
	coverage, err := migrate.BuildFormatCoverage(manifest)
	if err != nil {
		return Report{}, errors.New("migration snapshot coverage could not be verified")
	}
	sourcePath, err := canonicalDirectory(manifest.SourceRoot)
	if err != nil {
		return Report{}, errors.New("migration source root is unavailable or unsafe")
	}
	backupPath, err := canonicalDirectory(backupRoot)
	if err != nil {
		return Report{}, errors.New("backup root is unavailable or unsafe")
	}
	if rootsOverlap(sourcePath, backupPath) {
		return Report{}, errors.New("source and backup roots must be distinct and non-overlapping")
	}

	// Interleave two complete passes so changes during either tree's scan are
	// detected without retaining file content. Per-file handles are also checked
	// before and after each hash.
	sourceFirst, err := scanTree(sourcePath)
	if err != nil {
		return Report{}, fmt.Errorf("migration source tree could not be read safely: %w", err)
	}
	backupFirst, err := scanTree(backupPath)
	if err != nil {
		return Report{}, fmt.Errorf("backup tree could not be read safely: %w", err)
	}
	currentInventory, err := migrate.Inspect(manifest.Product, sourcePath)
	if err != nil {
		return Report{}, errors.New("migration source inventory could not be revalidated")
	}
	sourceSecond, err := scanTree(sourcePath)
	if err != nil || !sameTree(sourceFirst, sourceSecond) {
		return Report{}, fmt.Errorf("migration source changed during backup verification: %v", err)
	}
	backupSecond, err := scanTree(backupPath)
	if err != nil || !sameTree(backupFirst, backupSecond) {
		return Report{}, fmt.Errorf("backup changed during verification: %v", err)
	}

	different := differentFileCount(sourceSecond, backupSecond)
	snapshotMismatches := snapshotSourceMismatchCount(sourceSecond, manifest, currentInventory)
	shared := sharedFileCount(sourceSecond, backupSecond)
	status := "mismatch"
	if different == 0 && snapshotMismatches == 0 && shared == 0 &&
		sourceSecond.sha256 == backupSecond.sha256 && sourceSecond.bytes == backupSecond.bytes &&
		len(sourceSecond.files) == len(backupSecond.files) {
		status = "verified"
	}
	report := Report{
		SchemaVersion:            SchemaVersion,
		Product:                  string(manifest.Product),
		SourceFingerprint:        manifest.SourceFingerprint,
		CurrentSourceFingerprint: currentInventory.SourceFingerprint,
		SnapshotSHA256:           coverage.SnapshotSHA256,
		SourceTreeSHA256:         sourceSecond.sha256,
		BackupTreeSHA256:         backupSecond.sha256,
		SourceFiles:              len(sourceSecond.files), BackupFiles: len(backupSecond.files),
		SourceBytes: sourceSecond.bytes, BackupBytes: backupSecond.bytes,
		DifferentFiles: different, SnapshotSourceMismatches: snapshotMismatches,
		SharedFiles: shared, Status: status,
	}
	if err := Validate(report); err != nil {
		return Report{}, errors.New("backup verification report failed integrity validation")
	}
	return report, nil
}

// Validate checks the path-free versioned receipt. A mismatch is structurally
// valid evidence but is never acceptable to the cutover assessor.
func Validate(report Report) error {
	if report.SchemaVersion != SchemaVersion {
		return errors.New("unsupported migration backup verification schema")
	}
	if _, err := migrate.ParseProduct(report.Product); err != nil {
		return errors.New("invalid migration product")
	}
	for _, value := range []string{report.SourceFingerprint, report.CurrentSourceFingerprint, report.SnapshotSHA256, report.SourceTreeSHA256, report.BackupTreeSHA256} {
		if !validDigest(value) {
			return errors.New("invalid migration backup digest")
		}
	}
	if report.SourceFiles < 0 || report.SourceFiles > maxFiles || report.BackupFiles < 0 || report.BackupFiles > maxFiles ||
		report.SourceBytes < 0 || report.SourceBytes > maxTotalBytes || report.BackupBytes < 0 || report.BackupBytes > maxTotalBytes ||
		report.DifferentFiles < 0 || report.DifferentFiles > maxFiles ||
		report.SnapshotSourceMismatches < 0 || report.SnapshotSourceMismatches > maxFiles ||
		report.SharedFiles < 0 || report.SharedFiles > maxFiles {
		return errors.New("migration backup counts exceed their bounds")
	}
	if report.Status != "verified" && report.Status != "mismatch" {
		return errors.New("invalid migration backup status")
	}
	if report.Status == "verified" && (report.SourceTreeSHA256 != report.BackupTreeSHA256 ||
		report.SourceFingerprint != report.CurrentSourceFingerprint ||
		report.SourceFiles != report.BackupFiles || report.SourceBytes != report.BackupBytes ||
		report.DifferentFiles != 0 || report.SnapshotSourceMismatches != 0 || report.SharedFiles != 0) {
		return errors.New("verified migration backup receipt contains mismatches")
	}
	return nil
}

func canonicalDirectory(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("directory is required")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("path is not a real directory")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func rootsOverlap(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil || os.SameFile(leftInfo, rightInfo) {
		return true
	}
	return pathWithin(left, right) || pathWithin(right, left)
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		if runtime.GOOS == "windows" && !strings.EqualFold(filepath.VolumeName(root), filepath.VolumeName(candidate)) {
			return false
		}
		return true
	}
	if runtime.GOOS == "windows" {
		relative = strings.ToLower(relative)
	}
	return relative == "." || (relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func scanTree(root string) (treeState, error) {
	state := treeState{root: root, files: make(map[string]fileState)}
	directories := make(map[string]struct{})
	casePaths := make(map[string]struct{})
	entries := 0
	err := filepath.WalkDir(root, func(fullPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("tree traversal failed")
		}
		entries++
		if entries > maxEntries {
			return errors.New("tree exceeds the entry-count limit")
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("tree contains an unreadable or linked entry")
		}
		relative, err := filepath.Rel(root, fullPath)
		if err != nil {
			return errors.New("tree path is invalid")
		}
		if relative == "." {
			directories[relative] = struct{}{}
			return nil
		}
		relative = filepath.ToSlash(relative)
		if relative == "" || !utf8.ValidString(relative) || strings.HasPrefix(relative, "../") || strings.Contains(relative, ":") {
			return errors.New("tree contains an unsafe relative path")
		}
		if runtime.GOOS == "windows" {
			folded := strings.ToLower(relative)
			if _, exists := casePaths[folded]; exists {
				return errors.New("tree contains case-colliding paths")
			}
			casePaths[folded] = struct{}{}
		}
		if info.IsDir() {
			directories[relative] = struct{}{}
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxFileBytes || len(state.files) >= maxFiles ||
			state.bytes > maxTotalBytes-info.Size() {
			return errors.New("tree contains an unsupported or over-limit file")
		}
		file, err := os.Open(fullPath)
		if err != nil {
			return errors.New("tree file could not be opened")
		}
		opened, statErr := file.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != info.Size() ||
			!opened.ModTime().Equal(info.ModTime()) || uint32(opened.Mode().Perm()) != uint32(info.Mode().Perm()) {
			_ = file.Close()
			return errors.New("tree file changed before reading")
		}
		hasher := sha256.New()
		written, copyErr := io.Copy(hasher, io.LimitReader(file, info.Size()+1))
		finalInfo, finalStatErr := file.Stat()
		closeErr := file.Close()
		pathInfo, pathErr := os.Lstat(fullPath)
		if copyErr != nil || finalStatErr != nil || closeErr != nil || pathErr != nil ||
			written != info.Size() || finalInfo.Size() != info.Size() || !opened.ModTime().Equal(finalInfo.ModTime()) ||
			!os.SameFile(info, finalInfo) || !os.SameFile(info, pathInfo) || pathInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("tree file changed during reading")
		}
		if len(state.files)+1 > maxFiles || state.bytes > maxTotalBytes-written {
			return errors.New("tree exceeds configured bounds")
		}
		state.bytes += written
		state.files[relative] = fileState{
			size: written, mode: uint32(info.Mode().Perm()), digest: hex.EncodeToString(hasher.Sum(nil)), info: info,
		}
		return nil
	})
	if err != nil {
		return treeState{}, err
	}
	for relative := range directories {
		fullPath := root
		if relative != "." {
			fullPath = filepath.Join(root, filepath.FromSlash(relative))
		}
		after, err := os.Lstat(fullPath)
		if err != nil || !after.IsDir() || after.Mode()&os.ModeSymlink != 0 {
			return treeState{}, errors.New("tree directory changed during reading")
		}
	}
	paths := make([]string, 0, len(state.files))
	for relative := range state.files {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	commitments := make([]treeCommitment, 0, len(paths))
	for _, relative := range paths {
		file := state.files[relative]
		current, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(file.info, current) ||
			current.Size() != file.size || uint32(current.Mode().Perm()) != file.mode || !current.ModTime().Equal(file.info.ModTime()) {
			return treeState{}, errors.New("tree files changed during traversal")
		}
		commitments = append(commitments, treeCommitment{Path: relative, Size: file.size, Mode: file.mode, SHA256: file.digest})
	}
	encoded, err := json.Marshal(commitments)
	if err != nil {
		return treeState{}, errors.New("tree commitment could not be encoded")
	}
	digest := sha256.Sum256(encoded)
	state.sha256 = hex.EncodeToString(digest[:])
	return state, nil
}

type treeCommitment struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256"`
}

func sameTree(left, right treeState) bool {
	if left.sha256 != right.sha256 || left.bytes != right.bytes || len(left.files) != len(right.files) {
		return false
	}
	for path, a := range left.files {
		b, ok := right.files[path]
		if !ok || a.size != b.size || a.mode != b.mode || a.digest != b.digest {
			return false
		}
	}
	return true
}

func differentFileCount(source, backup treeState) int {
	paths := make(map[string]struct{}, len(source.files)+len(backup.files))
	for path := range source.files {
		paths[path] = struct{}{}
	}
	for path := range backup.files {
		paths[path] = struct{}{}
	}
	different := 0
	for path := range paths {
		a, sourceOK := source.files[path]
		b, backupOK := backup.files[path]
		if !sourceOK || !backupOK || a.size != b.size || a.mode != b.mode || a.digest != b.digest {
			different++
		}
	}
	return different
}

func snapshotSourceMismatchCount(source treeState, snapshot, current migrate.Manifest) int {
	oldEntries := make(map[string]migrate.FileEntry, len(snapshot.Files))
	newEntries := make(map[string]migrate.FileEntry, len(current.Files))
	paths := make(map[string]struct{}, len(snapshot.Files)+len(current.Files))
	for _, entry := range snapshot.Files {
		path := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry.Path)))
		oldEntries[path] = entry
		paths[path] = struct{}{}
	}
	for _, entry := range current.Files {
		path := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry.Path)))
		newEntries[path] = entry
		paths[path] = struct{}{}
	}
	mismatched := make(map[string]struct{})
	for path := range paths {
		old, oldOK := oldEntries[path]
		currentEntry, currentOK := newEntries[path]
		if !oldOK || !currentOK || !sameSourceEntry(old, currentEntry) {
			mismatched[path] = struct{}{}
		}
		expected, expectedOK := currentEntry, currentOK
		if !expectedOK {
			expected, expectedOK = old, oldOK
		}
		if expectedOK {
			file, exists := source.files[path]
			if !exists || file.size != expected.SourceBytes || file.digest != expected.SourceSHA256 {
				mismatched[path] = struct{}{}
			}
		}
	}
	if snapshot.SourceFingerprint != current.SourceFingerprint && len(mismatched) == 0 {
		return 1
	}
	return len(mismatched)
}

func sameSourceEntry(left, right migrate.FileEntry) bool {
	return left.Path == right.Path && left.SourceSHA256 == right.SourceSHA256 && left.SourceBytes == right.SourceBytes &&
		left.RecordCount == right.RecordCount && left.ImportedCount == right.ImportedCount &&
		left.ExcludedCount == right.ExcludedCount && left.RejectedCount == right.RejectedCount &&
		left.Status == right.Status && left.Issue == right.Issue && left.JoinedInto == right.JoinedInto
}

func sharedFileCount(source, backup treeState) int {
	shared := 0
	for path, sourceFile := range source.files {
		backupFile, ok := backup.files[path]
		if ok && os.SameFile(sourceFile.info, backupFile.info) {
			shared++
		}
	}
	return shared
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
