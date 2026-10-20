package sourcefingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
)

const (
	maxFiles = 500
	maxFile  = 32 << 20
	maxTotal = 256 << 20
)

// FingerprintSources hashes explicitly named regular files relative to a real
// project root. It rejects credential paths, symlinks, oversized inputs and
// detectable changes while reading. It never returns source content.
func FingerprintSources(projectRoot string, sourcePaths []string) ([]proceduregraph.PathFingerprint, error) {
	return FingerprintSourcesWithLimits(projectRoot, sourcePaths, maxFiles, maxFile, maxTotal)
}

// FingerprintSourcesWithLimits applies stricter caller-specific bounds without
// weakening the shared credential, path, symlink, or concurrent-change checks.
func FingerprintSourcesWithLimits(projectRoot string, sourcePaths []string, fileLimit int, fileBytes, totalBytesLimit int64) ([]proceduregraph.PathFingerprint, error) {
	if fileLimit < 1 || fileLimit > maxFiles || fileBytes < 1 || fileBytes > maxFile || totalBytesLimit < 1 || totalBytesLimit > maxTotal {
		return nil, errors.New("source fingerprint limits exceed the shared safety bounds")
	}
	if len(sourcePaths) == 0 || len(sourcePaths) > fileLimit {
		return nil, fmt.Errorf("source fingerprint request must contain 1-%d paths", fileLimit)
	}
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, errors.New("project root is unavailable")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("project root must be an existing directory, not a symbolic link")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("project root is unavailable")
	}
	result := make([]proceduregraph.PathFingerprint, 0, len(sourcePaths))
	seen := make(map[string]bool, len(sourcePaths))
	var totalBytes int64
	for _, relative := range sourcePaths {
		relative = strings.ReplaceAll(relative, "\\", "/")
		if err := proceduregraph.ValidateRepositoryPath(relative); err != nil || seen[relative] {
			return nil, errors.New("source fingerprint request contains an invalid or duplicate path")
		}
		seen[relative] = true
		if privacy.ContainsCredentialPath(relative) {
			return nil, errors.New("credential-bearing source paths cannot be fingerprinted")
		}
		if hasSymlinkComponent(root, relative) {
			return nil, errors.New("source path contains a symbolic link or unavailable component")
		}
		fullPath := filepath.Join(root, filepath.FromSlash(relative))
		before, err := os.Lstat(fullPath)
		if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > fileBytes {
			return nil, errors.New("source file must be readable, regular, and within the configured size limit")
		}
		if totalBytes > totalBytesLimit-before.Size() {
			return nil, errors.New("source fingerprint request exceeds the configured total size limit")
		}
		file, err := os.Open(fullPath)
		if err != nil {
			return nil, errors.New("source file could not be opened safely")
		}
		opened, statErr := file.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() {
			_ = file.Close()
			return nil, errors.New("source file changed or could not be read consistently")
		}
		hasher := sha256.New()
		written, copyErr := io.Copy(hasher, io.LimitReader(file, before.Size()+1))
		finalInfo, finalStatErr := file.Stat()
		closeErr := file.Close()
		pathAfter, pathStatErr := os.Lstat(fullPath)
		if copyErr != nil || finalStatErr != nil || closeErr != nil || pathStatErr != nil ||
			written != before.Size() || finalInfo.Size() != before.Size() ||
			!finalInfo.ModTime().Equal(opened.ModTime()) || !os.SameFile(before, finalInfo) ||
			!os.SameFile(before, pathAfter) || pathAfter.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("source file changed or could not be read consistently")
		}
		totalBytes += written
		result = append(result, proceduregraph.PathFingerprint{Path: relative, SHA256: hex.EncodeToString(hasher.Sum(nil))})
	}
	return result, nil
}

func hasSymlinkComponent(root, relative string) bool {
	current := root
	for _, segment := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}
