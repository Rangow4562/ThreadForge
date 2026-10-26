package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
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
		if len(indexes) == 0 {
			if isMancodeLegacyArtifactIgnoredPath(target, sourceRoot) {
				issues["referenced_artifact_unavailable"] = true
				continue
			}
			sensitive := sensitivePath(target)
			var entry FileEntry
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
				entry.Status = "unsupported"
				entry.Issue = "referenced artifact requires a source-specific converter"
				summary.FilesUnsupported++
			}
			*scanned = append(*scanned, scannedFile{entry: entry})
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
