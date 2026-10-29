package migrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const maxMancodeLegacyArtifactReferences = 100000

type mancodeLegacyWorkflowArtifactReference struct {
	OwnerDirectory string
	Path           string
}

func mancodeLegacyWorkflowArtifactReferences(relative, sourceRoot string, value any) []mancodeLegacyWorkflowArtifactReference {
	var paths []string
	if _, ok := isMancodeLegacyWorkflowReviewPath(relative, sourceRoot); ok {
		if object, valid := value.(map[string]any); valid {
			if reports, valid := object["reports"].(map[string]any); valid {
				for _, rawPath := range reports {
					if reportPath, valid := rawPath.(string); valid && mancodeLegacyReviewReportPath(reportPath) {
						paths = append(paths, strings.ReplaceAll(reportPath, `\`, "/"))
					}
				}
			}
		}
	}
	if _, ok := isMancodeLegacyWorkflowVerificationPath(relative, sourceRoot); ok {
		if object, valid := value.(map[string]any); valid {
			if checks, valid := object["checks"].([]any); valid {
				for _, rawCheck := range checks {
					check, valid := rawCheck.(map[string]any)
					if !valid {
						continue
					}
					for _, slot := range []string{"automated", "manual"} {
						evidence, valid := check[slot].(map[string]any)
						if !valid {
							continue
						}
						if rawPath, exists := evidence["evidenceFile"]; exists {
							if evidencePath, valid := rawPath.(string); valid && mancodeLegacySafeArtifactPath(evidencePath, maxMancodeLegacyEvidenceFilePath) {
								paths = append(paths, strings.ReplaceAll(evidencePath, `\`, "/"))
							}
						}
					}
				}
			}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	ownerDirectory := filepath.ToSlash(filepath.Dir(relative))
	references := make([]mancodeLegacyWorkflowArtifactReference, 0, len(paths))
	for _, referencePath := range paths {
		references = append(references, mancodeLegacyWorkflowArtifactReference{
			OwnerDirectory: ownerDirectory,
			Path:           referencePath,
		})
	}
	sort.Slice(references, func(i, j int) bool {
		left := strings.ToLower(filepath.ToSlash(filepath.Join(references[i].OwnerDirectory, references[i].Path)))
		right := strings.ToLower(filepath.ToSlash(filepath.Join(references[j].OwnerDirectory, references[j].Path)))
		return left < right
	})
	return references
}

func reconcileMancodeLegacyArtifactReferences(sourceRoot string, references []mancodeLegacyWorkflowArtifactReference,
	scanned *[]scannedFile, summary *Summary) (*migrationcoverage.MancodeLegacyArtifactReferences, []string, error) {
	return reconcileMancodeLegacyArtifactReferencesWithHasher(sourceRoot, references, scanned, summary,
		hashSafeMancodeWorkflowSourceFile)
}

func reconcileMancodeLegacyArtifactReferencesWithHasher(sourceRoot string, references []mancodeLegacyWorkflowArtifactReference,
	scanned *[]scannedFile, summary *Summary, hashReferenced func(string, string, int64) (FileEntry, error)) (
	*migrationcoverage.MancodeLegacyArtifactReferences, []string, error) {
	if len(references) == 0 {
		return nil, nil, nil
	}
	unique := make(map[string]string, len(references))
	for _, reference := range references {
		target := filepath.ToSlash(filepath.Join(reference.OwnerDirectory, filepath.FromSlash(reference.Path)))
		if !safeRelative(target) {
			return nil, nil, errors.New("Mancode legacy artifact reference escaped its workflow directory")
		}
		unique[strings.ToLower(target)] = target
	}
	if len(unique) > maxMancodeLegacyArtifactReferences {
		return nil, nil, errors.New("Mancode legacy workflow artifact references exceed the 100000-reference limit")
	}

	paths := make([]string, 0, len(unique))
	for _, target := range unique {
		paths = append(paths, target)
	}
	sort.Slice(paths, func(i, j int) bool { return strings.ToLower(paths[i]) < strings.ToLower(paths[j]) })
	indexesByPath := make(map[string][]int, len(*scanned))
	for index := range *scanned {
		key := strings.ToLower(filepath.ToSlash((*scanned)[index].entry.Path))
		indexesByPath[key] = append(indexesByPath[key], index)
	}

	result := &migrationcoverage.MancodeLegacyArtifactReferences{
		ReferenceCount: len(paths), Targets: []migrationcoverage.MancodeLegacyArtifactTarget{},
	}
	issues := make(map[string]bool)
	for _, target := range paths {
		key := strings.ToLower(target)
		indexes := indexesByPath[key]
		if len(indexes) > 1 {
			issues["referenced_artifact_ambiguous"] = true
			continue
		}
		if len(indexes) == 1 {
			before := (*scanned)[indexes[0]]
			if before.entry.RecordCount == 0 && before.entry.ImportedCount == 0 && before.entry.ExcludedCount == 0 &&
				before.entry.RejectedCount == 0 && before.entry.Status != "credential_path_excluded" &&
				before.entry.SourceBytes <= maxFileBytes {
				document, err := readMancodeLegacyReferencedWorkflowDocument(sourceRoot, before.entry)
				if err != nil {
					return nil, nil, err
				}
				replaceScannedFileSummary(summary, before, document)
				(*scanned)[indexes[0]] = document
			}
		}
		if len(indexes) == 0 {
			if isMancodeLegacyArtifactIgnoredPath(target, sourceRoot) {
				issues["referenced_artifact_unavailable"] = true
				continue
			}
			sensitive := sensitivePath(target)
			var entry FileEntry
			var discovered scannedFile
			var err error
			if sensitive {
				entry, err = statSafeMancodeWorkflowSourceFile(sourceRoot, target, maxTotalBytes)
			} else {
				entry, err = hashReferenced(sourceRoot, target, maxTotalBytes)
			}
			if errors.Is(err, os.ErrNotExist) {
				issues["referenced_artifact_missing"] = true
				continue
			}
			if err != nil {
				issues["referenced_artifact_unavailable"] = true
				continue
			}
			if summary.FilesScanned >= maxFiles || entry.SourceBytes > maxTotalBytes-summary.TotalSourceBytes {
				return nil, nil, errors.New("Mancode referenced artifact inventory exceeds source limits")
			}
			summary.FilesScanned++
			summary.TotalSourceBytes += entry.SourceBytes
			if sensitive {
				entry.Status = "credential_path_excluded"
				entry.Issue = "credential-bearing path excluded"
				summary.FilesCredentialBlocked++
			} else {
				if entry.SourceBytes <= maxFileBytes {
					discovered, err = readMancodeLegacyReferencedWorkflowDocument(sourceRoot, entry)
					if err != nil {
						return nil, nil, err
					}
				} else {
					discovered.entry = entry
					discovered.entry.Status = "unsupported"
					discovered.entry.Issue = "referenced text artifact exceeds the 32 MiB limit"
				}
				if discovered.entry.ImportedCount > 0 {
					summary.FilesReady++
				}
				if discovered.entry.Status == "unsupported" {
					summary.FilesUnsupported++
				}
				summary.RecordsFound += discovered.entry.RecordCount
				summary.RecordsImported += discovered.entry.ImportedCount
				summary.RecordsCredentialBlocked += discovered.entry.ExcludedCount
				summary.RecordsRejected += discovered.entry.RejectedCount
				if summary.RecordsFound > maxRecords {
					return nil, nil, errors.New("Mancode migration source exceeds 100000-record limit")
				}
			}
			if sensitive {
				*scanned = append(*scanned, scannedFile{entry: entry})
			} else {
				*scanned = append(*scanned, discovered)
			}
			indexes = []int{len(*scanned) - 1}
			indexesByPath[key] = indexes
		}
		entry := (*scanned)[indexes[0]].entry
		result.FoundCount++
		imported := entry.ImportedCount > 0 && entry.ExcludedCount == 0 && entry.RejectedCount == 0 &&
			entry.Status != "unsupported" && entry.Status != "credential_path_excluded"
		if imported {
			result.ImportedCount++
		} else {
			issues["referenced_artifact_unimported"] = true
		}
		result.Targets = append(result.Targets, migrationcoverage.MancodeLegacyArtifactTarget{
			PathSHA256: mancodeLegacyArtifactPathHash(target, sourceRoot), SourceSHA256: entry.SourceSHA256, Imported: imported,
		})
	}
	sort.Slice(result.Targets, func(i, j int) bool { return result.Targets[i].PathSHA256 < result.Targets[j].PathSHA256 })
	result.BindingVerified = result.ReferenceCount == result.FoundCount && result.FoundCount == result.ImportedCount && len(issues) == 0
	orderedIssues := make([]string, 0, len(issues))
	for issue := range issues {
		orderedIssues = append(orderedIssues, issue)
	}
	sort.Strings(orderedIssues)
	return result, orderedIssues, nil
}

func mancodeLegacyArtifactPathHash(relative, sourceRoot string) string {
	canonical := strings.ToLower(strings.Trim(mancodePath(relative, sourceRoot), "/"))
	return digest([]byte("threadforge/mancode/legacy-workflow-artifact-path/v1\x00" + canonical))
}

func isMancodeLegacyWorkflowArtifactTarget(relative, sourceRoot string,
	checks []migrationcoverage.MancodeLegacyWorkflowCheck) (found, imported bool) {
	standard := filepath.ToSlash(strings.TrimPrefix(relative, "./"))
	if strings.EqualFold(filepath.Base(filepath.Clean(sourceRoot)), ".mancode") &&
		!strings.HasPrefix(strings.ToLower(standard), ".mancode/") {
		standard = filepath.ToSlash(filepath.Join(".mancode", standard))
	}
	parts := strings.Split(strings.Trim(standard, "/"), "/")
	if len(parts) < 4 || !strings.EqualFold(parts[0], ".mancode") ||
		!strings.EqualFold(parts[1], "workflows") || !mancodeLegacyWorkflowTaskID.MatchString(parts[2]) {
		return false, false
	}
	directory := strings.ToLower(strings.Join(parts[:3], "/"))
	workflowID := mancodeLegacyWorkflowPairID(directory)
	pathHash := mancodeLegacyArtifactPathHash(relative, sourceRoot)
	for _, check := range checks {
		if check.WorkflowIDSHA256 != workflowID || check.ArtifactReferences == nil {
			continue
		}
		for _, target := range check.ArtifactReferences.Targets {
			if target.PathSHA256 == pathHash {
				return true, target.Imported
			}
		}
	}
	return false, false
}

func isMancodeLegacyReferencedText(relative string, data []byte) bool {
	switch strings.ToLower(filepath.Ext(relative)) {
	case ".7z", ".avi", ".bin", ".bmp", ".class", ".dll", ".doc", ".docx", ".dylib", ".exe",
		".gif", ".gz", ".ico", ".jar", ".jpeg", ".jpg", ".mov", ".mp3", ".mp4", ".o", ".pdf",
		".png", ".ppt", ".pptx", ".rar", ".so", ".sqlite", ".sqlite3", ".tar", ".wav", ".webp",
		".xls", ".xlsx", ".zip":
		return false
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	for _, value := range string(data) {
		if unicode.IsControl(value) && value != '\n' && value != '\r' && value != '\t' && value != '\f' {
			return false
		}
	}
	return true
}

func scanMancodeLegacyReferencedWorkflowDocument(relative string, data []byte) scannedFile {
	file := scannedFile{entry: FileEntry{
		Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data)), Status: "ready",
	}}
	if !isMancodeLegacyReferencedText(relative, data) {
		file.entry.Status = "unsupported"
		file.entry.Issue = "referenced artifact is not supported UTF-8 text"
		return file
	}
	payload, _ := json.Marshal(map[string]string{"content": string(data)})
	file.entry.RecordCount = 1
	if privacy.ContainsCredential(payload) {
		file.entry.ExcludedCount = 1
	} else {
		file.records = append(file.records, Record{
			Key: relative + "#000001", Type: "workflow_document", SourcePath: relative,
			ContentSHA256: digest(payload), Scope: "local_pending_review", Payload: payload,
		})
	}
	finalizeScannedFile(&file)
	return file
}

func readMancodeLegacyReferencedWorkflowDocument(sourceRoot string, expected FileEntry) (scannedFile, error) {
	data, observed, err := readSafeMancodeReferencedText(sourceRoot, expected.Path, maxFileBytes)
	if err != nil || observed.SourceBytes != expected.SourceBytes || observed.SourceSHA256 != expected.SourceSHA256 {
		return scannedFile{}, errors.New("Mancode referenced artifact changed during bounded text read")
	}
	return scanMancodeLegacyReferencedWorkflowDocument(expected.Path, data), nil
}

func replaceScannedFileSummary(summary *Summary, before, after scannedFile) {
	if before.entry.ImportedCount > 0 {
		summary.FilesReady--
	}
	if before.entry.Status == "unsupported" {
		summary.FilesUnsupported--
	}
	summary.RecordsFound += after.entry.RecordCount - before.entry.RecordCount
	summary.RecordsImported += after.entry.ImportedCount - before.entry.ImportedCount
	summary.RecordsCredentialBlocked += after.entry.ExcludedCount - before.entry.ExcludedCount
	summary.RecordsRejected += after.entry.RejectedCount - before.entry.RejectedCount
	if after.entry.ImportedCount > 0 {
		summary.FilesReady++
	}
	if after.entry.Status == "unsupported" {
		summary.FilesUnsupported++
	}
}

func readSafeMancodeReferencedText(sourceRoot, relative string, maxBytes int64) ([]byte, FileEntry, error) {
	if !safeRelative(relative) || maxBytes < 0 {
		return nil, FileEntry{}, errors.New("unsafe Mancode referenced text path")
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return nil, FileEntry{}, err
	}
	current := root
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	var before os.FileInfo
	for index, part := range parts {
		current = filepath.Join(current, part)
		before, err = os.Lstat(current)
		if err != nil {
			return nil, FileEntry{}, err
		}
		if before.Mode()&os.ModeSymlink != 0 || index < len(parts)-1 && !before.IsDir() {
			return nil, FileEntry{}, errors.New("Mancode referenced text path has an unsafe parent")
		}
	}
	if before == nil || !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > maxBytes {
		return nil, FileEntry{}, errors.New("Mancode referenced text is not a bounded regular file")
	}
	file, err := os.Open(current)
	if err != nil {
		return nil, FileEntry{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() {
		return nil, FileEntry{}, errors.New("Mancode referenced text changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) != opened.Size() || int64(len(data)) > maxBytes {
		return nil, FileEntry{}, errors.New("Mancode referenced text changed while reading")
	}
	after, err := os.Lstat(current)
	final, statErr := file.Stat()
	if err != nil || statErr != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) ||
		!os.SameFile(opened, final) || after.Size() != opened.Size() || final.Size() != opened.Size() ||
		!after.ModTime().Equal(opened.ModTime()) || !final.ModTime().Equal(opened.ModTime()) {
		return nil, FileEntry{}, errors.New("Mancode referenced text changed after reading")
	}
	return data, FileEntry{Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data))}, nil
}

func credentialPathCommitment(relative string) string {
	canonical := strings.ToLower(filepath.ToSlash(filepath.Clean(relative)))
	return digest([]byte("threadforge/migration/credential-path-only/v1\x00" + canonical))
}

func isMancodeLegacyArtifactIgnoredPath(relative, sourceRoot string) bool {
	canonical := strings.Trim(mancodePath(relative, sourceRoot), "/")
	parts := strings.Split(canonical, "/")
	for index, part := range parts {
		if !ignoredDirectory(part) {
			continue
		}
		if strings.EqualFold(part, "checkpoints") && index == 3 && len(parts) >= 5 &&
			strings.EqualFold(parts[0], ".mancode") && strings.EqualFold(parts[1], "workflows") &&
			mancodeLegacyWorkflowTaskID.MatchString(parts[2]) {
			continue
		}
		return true
	}
	return false
}

// Hash referenced non-indexed artifacts without retaining their contents. It
// refuses symlinks/reparse points and verifies the opened file still matches
// the path observed before and after hashing.
func hashSafeMancodeWorkflowSourceFile(sourceRoot, relative string, maxBytes int64) (FileEntry, error) {
	if !safeRelative(relative) || maxBytes < 0 {
		return FileEntry{}, errors.New("unsafe Mancode referenced artifact path")
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return FileEntry{}, err
	}
	current := root
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	var before os.FileInfo
	for index, part := range parts {
		current = filepath.Join(current, part)
		before, err = os.Lstat(current)
		if err != nil {
			return FileEntry{}, err
		}
		if before.Mode()&os.ModeSymlink != 0 || index < len(parts)-1 && !before.IsDir() {
			return FileEntry{}, errors.New("Mancode referenced artifact path has an unsafe parent")
		}
	}
	if before == nil || !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > maxBytes {
		return FileEntry{}, errors.New("Mancode referenced artifact is not a bounded regular file")
	}
	file, err := os.Open(current)
	if err != nil {
		return FileEntry{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() {
		return FileEntry{}, errors.New("Mancode referenced artifact changed while opening")
	}
	hasher := sha256.New()
	read, err := io.Copy(hasher, io.LimitReader(file, maxBytes+1))
	if err != nil || read != opened.Size() || read > maxBytes {
		return FileEntry{}, errors.New("Mancode referenced artifact changed while hashing")
	}
	after, err := os.Lstat(current)
	final, statErr := file.Stat()
	if err != nil || statErr != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) ||
		!os.SameFile(opened, final) || after.Size() != opened.Size() || final.Size() != opened.Size() ||
		!after.ModTime().Equal(opened.ModTime()) || !final.ModTime().Equal(opened.ModTime()) {
		return FileEntry{}, errors.New("Mancode referenced artifact changed after hashing")
	}
	return FileEntry{Path: relative, SourceSHA256: hex.EncodeToString(hasher.Sum(nil)), SourceBytes: read}, nil
}

// Credential-path references are inventoried from filesystem metadata only;
// the referenced bytes are never opened or hashed.
func statSafeMancodeWorkflowSourceFile(sourceRoot, relative string, maxBytes int64) (FileEntry, error) {
	if !safeRelative(relative) || maxBytes < 0 {
		return FileEntry{}, errors.New("unsafe Mancode referenced artifact path")
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return FileEntry{}, err
	}
	current := root
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	var before os.FileInfo
	for index, part := range parts {
		current = filepath.Join(current, part)
		before, err = os.Lstat(current)
		if err != nil {
			return FileEntry{}, err
		}
		if before.Mode()&os.ModeSymlink != 0 || index < len(parts)-1 && !before.IsDir() {
			return FileEntry{}, errors.New("Mancode credential reference has an unsafe parent")
		}
	}
	if before == nil || !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > maxBytes {
		return FileEntry{}, errors.New("Mancode credential reference is not a bounded regular file")
	}
	after, err := os.Lstat(current)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return FileEntry{}, errors.New("Mancode credential reference changed during inspection")
	}
	return FileEntry{Path: relative, SourceSHA256: credentialPathCommitment(relative), SourceBytes: before.Size()}, nil
}
