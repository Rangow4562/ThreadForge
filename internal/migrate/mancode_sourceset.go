package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
	"github.com/threadforge-ai/threadforge/pkg/migrationsources"
)

const maxMancodeSourceRoots = 1024

type MancodeSourceRoot struct {
	SourceID   string `json:"source_id"`
	Role       string `json:"role"`
	SourceRoot string `json:"source_root"`
}

// DiscoverMancodeSourceRoots returns the project root plus every workspace
// store under the Git common directory and Mancode's non-Git runtime layout.
// The returned paths are local operator data and must not be copied into a
// shared source-set report.
func DiscoverMancodeSourceRoots(projectRoot string) ([]MancodeSourceRoot, error) {
	project, err := canonicalMancodeSourceDirectory(projectRoot)
	if err != nil {
		return nil, errors.New("Mancode project source root is unavailable or unsafe")
	}
	if isMancodeCoordinationRoot(project) {
		return nil, errors.New("Mancode source-set root must be the project root, not a coordination workspace")
	}
	roots := []MancodeSourceRoot{{SourceID: "project", Role: "project", SourceRoot: project}}
	gitCommon, err := mancodeGitCommonDirectory(project)
	if err != nil {
		return nil, err
	}
	if gitCommon != "" {
		found, err := discoverMancodeWorkspaceRoots(filepath.Join(gitCommon, "mancode", "workspaces"), "git_coordination", project, gitCommon)
		if err != nil {
			return nil, err
		}
		roots = append(roots, found...)
	}
	found, err := discoverMancodeWorkspaceRoots(
		filepath.Join(project, ".mancode", "runtime", "non-git"), "non_git_coordination", project, project)
	if err != nil {
		return nil, err
	}
	roots = append(roots, found...)
	if len(roots) > maxMancodeSourceRoots {
		return nil, errors.New("Mancode source set exceeds its workspace-root limit")
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].SourceID < roots[j].SourceID })
	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		key := mancodeSourcePathKey(root.SourceRoot)
		if seen[key] {
			return nil, errors.New("Mancode source discovery found overlapping workspace roots")
		}
		seen[key] = true
	}
	return roots, nil
}

// BuildMancodeSourceSet verifies a snapshot for the project and for every
// discovered coordination workspace. Missing, extra, stale, and duplicate
// component snapshots are rejected. Record-key overlaps are surfaced in the
// path-free report for cutover to block rather than silently merging them.
func BuildMancodeSourceSet(projectRoot string, manifestPaths []string) (migrationsources.MancodeReport, error) {
	roots, err := DiscoverMancodeSourceRoots(projectRoot)
	if err != nil {
		return migrationsources.MancodeReport{}, err
	}
	if len(manifestPaths) != len(roots) {
		return migrationsources.MancodeReport{}, fmt.Errorf("Mancode source set requires one snapshot for each of %d discovered source roots", len(roots))
	}
	rootByPath := make(map[string]MancodeSourceRoot, len(roots))
	for _, root := range roots {
		rootByPath[mancodeSourcePathKey(root.SourceRoot)] = root
	}
	seenRoots := make(map[string]bool, len(manifestPaths))
	report := migrationsources.MancodeReport{Components: make([]migrationsources.MancodeComponent, 0, len(roots))}
	seenRecordKeys := make(map[string]string)
	var duplicateCommitments []string

	for _, manifestPath := range manifestPaths {
		manifest, err := LoadSnapshot(manifestPath)
		if err != nil || manifest.Product != Mancode {
			return migrationsources.MancodeReport{}, errors.New("Mancode source set contains an invalid component snapshot")
		}
		sourceRoot, err := canonicalMancodeSourceDirectory(manifest.SourceRoot)
		if err != nil {
			return migrationsources.MancodeReport{}, errors.New("Mancode source-set component root is unavailable or unsafe")
		}
		pathKey := mancodeSourcePathKey(sourceRoot)
		root, exists := rootByPath[pathKey]
		if !exists || seenRoots[pathKey] {
			return migrationsources.MancodeReport{}, errors.New("Mancode component snapshot is extra or duplicated")
		}
		seenRoots[pathKey] = true
		options := InspectOptions{IncludeMancodeGitRef: manifest.MancodeGitRef != nil}
		if root.Role != "project" && manifest.MancodeGitRef != nil {
			return migrationsources.MancodeReport{}, errors.New("Mancode Git-ref authority may be included only in the project component")
		}
		current, err := InspectWithOptions(Mancode, sourceRoot, options)
		if err != nil || current.SourceFingerprint != manifest.SourceFingerprint {
			return migrationsources.MancodeReport{}, errors.New("Mancode source changed after its component snapshot")
		}
		coverage, err := BuildFormatCoverage(manifest)
		if err != nil || migrationcoverage.Validate(coverage) != nil {
			return migrationsources.MancodeReport{}, errors.New("Mancode component coverage could not be verified")
		}
		records, err := ReadSnapshotRecords(manifestPath, manifest)
		if err != nil {
			return migrationsources.MancodeReport{}, errors.New("Mancode component records could not be verified")
		}
		for _, record := range records {
			if prior, duplicate := seenRecordKeys[record.Key]; duplicate {
				pair := []string{prior, root.SourceID}
				sort.Strings(pair)
				keyDigest := sha256.Sum256([]byte(record.Key))
				duplicateCommitments = append(duplicateCommitments,
					pair[0]+"\x00"+pair[1]+"\x00"+hex.EncodeToString(keyDigest[:]))
			} else {
				seenRecordKeys[record.Key] = root.SourceID
			}
		}
		component := migrationsources.MancodeComponent{
			SourceID: root.SourceID, Role: root.Role,
			SourceFingerprint: manifest.SourceFingerprint, SnapshotSHA256: coverage.SnapshotSHA256,
			FilesScanned: coverage.FilesScanned, SnapshotFiles: coverage.SnapshotFiles,
			SourceRecords: coverage.RecordsFound, ImportedRecords: coverage.RecordsImported,
			CredentialExclusions: coverage.CredentialExcludedRecords, RejectedRecords: coverage.RejectedRecords,
		}
		report.Components = append(report.Components, component)
		if root.Role == "project" {
			report.ProjectSourceFingerprint = component.SourceFingerprint
			report.ProjectSnapshotSHA256 = component.SnapshotSHA256
		}
	}
	if len(seenRoots) != len(roots) {
		return migrationsources.MancodeReport{}, errors.New("Mancode source set is missing one or more discovered workspace snapshots")
	}
	currentRoots, err := DiscoverMancodeSourceRoots(projectRoot)
	if err != nil || len(currentRoots) != len(roots) {
		return migrationsources.MancodeReport{}, errors.New("Mancode source-root inventory changed while the source set was built")
	}
	for index := range roots {
		if roots[index].SourceID != currentRoots[index].SourceID || roots[index].Role != currentRoots[index].Role ||
			mancodeSourcePathKey(roots[index].SourceRoot) != mancodeSourcePathKey(currentRoots[index].SourceRoot) {
			return migrationsources.MancodeReport{}, errors.New("Mancode source-root inventory changed while the source set was built")
		}
	}
	if len(duplicateCommitments) > 0 {
		sort.Strings(duplicateCommitments)
		report.DuplicateRecordKeys = len(duplicateCommitments)
		joined := strings.Join(duplicateCommitments, "\n")
		digest := sha256.Sum256([]byte(joined))
		report.DuplicateRecordKeysSHA256 = hex.EncodeToString(digest[:])
	}
	if err := migrationsources.SealMancode(&report); err != nil {
		return migrationsources.MancodeReport{}, fmt.Errorf("seal Mancode source-set inventory: %w", err)
	}
	return report, nil
}

func discoverMancodeWorkspaceRoots(parent, role, projectRoot, storageRoot string) ([]MancodeSourceRoot, error) {
	exists, err := validateMancodeDirectoryPath(storageRoot, parent)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	info, err := os.Lstat(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("Mancode coordination workspace inventory is not a regular directory")
	}
	directory, err := os.Open(parent)
	if err != nil {
		return nil, errors.New("Mancode coordination workspace inventory cannot be opened")
	}
	entries, readErr := directory.ReadDir(maxMancodeSourceRoots + 1)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(entries) > maxMancodeSourceRoots {
		return nil, errors.New("Mancode coordination workspace inventory exceeds its bounds")
	}
	roots := make([]MancodeSourceRoot, 0, len(entries))
	for _, entry := range entries {
		if !mancodeOperationIDPattern.MatchString(entry.Name()) || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return nil, errors.New("Mancode coordination inventory contains an unsupported entry")
		}
		rootPath := filepath.Join(parent, entry.Name())
		canonical, err := canonicalMancodeSourceDirectory(rootPath)
		if err != nil || !isMancodeCoordinationRoot(canonical) {
			return nil, errors.New("Mancode coordination workspace root is unavailable or unsafe")
		}
		relativeToStore, err := filepath.Rel(parent, canonical)
		if err != nil || relativeToStore == ".." || strings.HasPrefix(relativeToStore, ".."+string(filepath.Separator)) {
			return nil, errors.New("Mancode coordination workspace escaped its inventory directory")
		}
		relative, err := filepath.Rel(projectRoot, canonical)
		if err != nil {
			return nil, errors.New("Mancode coordination root identity cannot be derived")
		}
		relative = strings.ToLower(filepath.ToSlash(filepath.Clean(relative)))
		identity := sha256.Sum256([]byte(role + "\x00" + relative))
		sourceID := role + "-" + hex.EncodeToString(identity[:])
		roots = append(roots, MancodeSourceRoot{SourceID: sourceID, Role: role, SourceRoot: canonical})
	}
	return roots, nil
}

func validateMancodeDirectoryPath(root, target string) (bool, error) {
	canonicalRoot, err := filepath.Abs(root)
	if err != nil {
		return false, errors.New("Mancode source inventory root is invalid")
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return false, errors.New("Mancode source inventory path is invalid")
	}
	relative, err := filepath.Rel(canonicalRoot, absoluteTarget)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false, errors.New("Mancode coordination inventory is outside its storage root")
	}
	current := canonicalRoot
	if relative == "." {
		return true, nil
	}
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return false, errors.New("Mancode coordination inventory traverses an unsafe path")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return false, errors.New("Mancode coordination inventory parent is not a directory")
		}
	}
	return true, nil
}

func mancodeGitCommonDirectory(projectRoot string) (string, error) {
	gitEntry := filepath.Join(projectRoot, ".git")
	info, err := os.Lstat(gitEntry)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Mancode Git metadata path is unavailable or unsafe")
	}
	if info.IsDir() {
		return canonicalMancodeSourceDirectory(gitEntry)
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 4096 {
		return "", errors.New("Mancode Git worktree pointer is invalid")
	}
	data, err := os.ReadFile(gitEntry)
	if err != nil || len(data) > 4096 {
		return "", errors.New("Mancode Git worktree pointer cannot be read")
	}
	value := strings.TrimSpace(string(data))
	if !strings.HasPrefix(value, "gitdir: ") || strings.ContainsAny(strings.TrimPrefix(value, "gitdir: "), "\r\n") {
		return "", errors.New("Mancode Git worktree pointer has an unsupported format")
	}
	gitDirectory := strings.TrimSpace(strings.TrimPrefix(value, "gitdir: "))
	if !filepath.IsAbs(gitDirectory) {
		gitDirectory = filepath.Join(projectRoot, gitDirectory)
	}
	gitDirectory, err = canonicalMancodeSourceDirectory(gitDirectory)
	if err != nil {
		return "", errors.New("Mancode Git worktree metadata directory is unavailable or unsafe")
	}
	commonPath := filepath.Join(gitDirectory, "commondir")
	commonInfo, err := os.Lstat(commonPath)
	if errors.Is(err, os.ErrNotExist) {
		return gitDirectory, nil
	}
	if err != nil || commonInfo.Mode()&os.ModeSymlink != 0 || !commonInfo.Mode().IsRegular() || commonInfo.Size() > 4096 {
		return "", errors.New("Mancode Git common-directory pointer is invalid")
	}
	commonData, err := os.ReadFile(commonPath)
	if err != nil || len(commonData) > 4096 {
		return "", errors.New("Mancode Git common-directory pointer cannot be read")
	}
	commonValue := strings.TrimSpace(string(commonData))
	if commonValue == "" || strings.ContainsAny(commonValue, "\r\n") {
		return "", errors.New("Mancode Git common-directory pointer has an unsupported format")
	}
	if !filepath.IsAbs(commonValue) {
		commonValue = filepath.Join(gitDirectory, commonValue)
	}
	commonValue, err = canonicalMancodeSourceDirectory(commonValue)
	if err != nil {
		return "", errors.New("Mancode Git common directory is unavailable or unsafe")
	}
	return commonValue, nil
}

func canonicalMancodeSourceDirectory(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("source is not a non-symlink directory")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}

func mancodeSourcePathKey(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}
