package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const privateArtifactManifestSchema = "threadforge.migration-private-artifacts.v1"

const privateArtifactManifestName = "private-artifacts.json"

type PrivateArtifactReference struct {
	SourcePath string `json:"source_path"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	LocalPath  string `json:"local_path"`
}

type privateArtifactManifest struct {
	SchemaVersion     string                     `json:"schema_version"`
	MigrationID       string                     `json:"migration_id"`
	SourceFingerprint string                     `json:"source_fingerprint"`
	Artifacts         []PrivateArtifactReference `json:"artifacts"`
}

func expectedPrivateArtifacts(manifest Manifest) []PrivateArtifactReference {
	if manifest.Product != AnyJev {
		return nil
	}
	artifacts := make([]PrivateArtifactReference, 0)
	for _, file := range manifest.Files {
		if !isAnyJevDistillationNPZPath(file.Path) || file.ImportedCount == 0 || file.Status == "unsupported" || file.Status == "credential_path_excluded" {
			continue
		}
		artifacts = append(artifacts, PrivateArtifactReference{
			SourcePath: file.Path,
			SHA256:     file.SourceSHA256,
			Bytes:      file.SourceBytes,
			LocalPath:  privateArtifactLocalPath(file.SourceSHA256),
		})
	}
	return artifacts
}

func privateArtifactLocalPath(sha256 string) string {
	return filepath.ToSlash(filepath.Join("artifacts", "sha256", sha256+".npz"))
}

func ensurePrivateArtifactSnapshot(snapshotRoot, sourceRoot string, manifest Manifest) error {
	expected := expectedPrivateArtifacts(manifest)
	if len(expected) == 0 {
		return validatePrivateArtifactSnapshot(snapshotRoot, manifest)
	}
	if _, err := os.Lstat(filepath.Join(snapshotRoot, privateArtifactManifestName)); err == nil {
		return validatePrivateArtifactSnapshot(snapshotRoot, manifest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	var totalBytes int64
	for index := range expected {
		artifact := &expected[index]
		if artifact.Bytes < 0 || artifact.Bytes > maxFileBytes || totalBytes > maxSnapshot-artifact.Bytes {
			return errors.New("private AnyJev artifact snapshot exceeds bounded limits")
		}
		data, err := readAnyJevSourceArtifact(sourceRoot, artifact.SourcePath, artifact.Bytes, artifact.SHA256)
		if err != nil {
			return fmt.Errorf("verify AnyJev source artifact %s: %w", artifact.SourcePath, err)
		}
		if err := writeOrVerifyPrivateArtifact(snapshotRoot, artifact.LocalPath, data, artifact.SHA256); err != nil {
			return err
		}
		totalBytes += artifact.Bytes
	}
	return writeOrVerifyPrivateArtifactManifest(snapshotRoot, manifest, expected)
}

func readAnyJevSourceArtifact(sourceRoot, relative string, expectedBytes int64, expectedSHA256 string) ([]byte, error) {
	if !safeRelative(relative) || expectedBytes < 0 || expectedBytes > maxFileBytes || !validDigest(expectedSHA256) {
		return nil, errors.New("source artifact reference is invalid")
	}
	absRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Stat(absRoot)
	if err != nil || !rootInfo.IsDir() {
		return nil, errors.New("source artifact root is unavailable")
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(absRoot, filepath.FromSlash(relative))
	current := absRoot
	for _, part := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("source artifact path is missing or contains a symbolic link")
		}
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, err
	}
	within, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) || filepath.IsAbs(within) {
		return nil, errors.New("source artifact resolves outside the source root")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Size() != expectedBytes || info.Size() > maxFileBytes {
		return nil, errors.New("source artifact metadata changed")
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() != expectedBytes || opened.Size() > maxFileBytes {
		return nil, errors.New("source artifact changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil || int64(len(data)) != expectedBytes || digest(data) != expectedSHA256 {
		return nil, errors.New("source artifact digest or size changed")
	}
	return data, nil
}

func writeOrVerifyPrivateArtifact(snapshotRoot, relative string, data []byte, expectedSHA256 string) error {
	if !safeRelative(relative) || !validDigest(expectedSHA256) || len(data) > maxFileBytes || digest(data) != expectedSHA256 {
		return errors.New("private artifact content reference is invalid")
	}
	if existing, err := readSnapshotFile(snapshotRoot, relative, int64(len(data)), maxFileBytes); err == nil {
		if digest(existing) != expectedSHA256 {
			return errors.New("existing private artifact content conflicts with its digest")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect private artifact destination: %w", err)
	}
	name := filepath.Join(snapshotRoot, filepath.FromSlash(relative))
	tempName := name + fmt.Sprintf(".tmp-%d", time.Now().UnixNano())
	if err := writePrivateFile(tempName, data); err != nil {
		return fmt.Errorf("write private AnyJev artifact: %w", err)
	}
	if err := os.Rename(tempName, name); err != nil {
		if existing, readErr := readSnapshotFile(snapshotRoot, relative, int64(len(data)), maxFileBytes); readErr == nil && digest(existing) == expectedSHA256 {
			_ = os.Remove(tempName)
			return nil
		}
		_ = os.Remove(tempName)
		return fmt.Errorf("commit private AnyJev artifact: %w", err)
	}
	stored, err := readSnapshotFile(snapshotRoot, relative, int64(len(data)), maxFileBytes)
	if err != nil || digest(stored) != expectedSHA256 {
		return errors.New("private AnyJev artifact failed readback verification")
	}
	return nil
}

func writeOrVerifyPrivateArtifactManifest(snapshotRoot string, manifest Manifest, artifacts []PrivateArtifactReference) error {
	index := privateArtifactManifest{
		SchemaVersion: privateArtifactManifestSchema, MigrationID: manifest.ID,
		SourceFingerprint: manifest.SourceFingerprint, Artifacts: artifacts,
	}
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if existing, err := readSnapshotFile(snapshotRoot, privateArtifactManifestName, -1, 16<<20); err == nil {
		if !bytes.Equal(existing, encoded) {
			return errors.New("existing private artifact manifest conflicts with source snapshot")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect private artifact manifest: %w", err)
	}
	tempName := filepath.Join(snapshotRoot, privateArtifactManifestName+fmt.Sprintf(".tmp-%d", time.Now().UnixNano()))
	if err := writePrivateFile(tempName, encoded); err != nil {
		return fmt.Errorf("write private artifact manifest: %w", err)
	}
	if err := os.Rename(tempName, filepath.Join(snapshotRoot, privateArtifactManifestName)); err != nil {
		if existing, readErr := readSnapshotFile(snapshotRoot, privateArtifactManifestName, -1, 16<<20); readErr == nil && bytes.Equal(existing, encoded) {
			_ = os.Remove(tempName)
			return nil
		}
		_ = os.Remove(tempName)
		return fmt.Errorf("commit private artifact manifest: %w", err)
	}
	return nil
}

func validatePrivateArtifactSnapshot(snapshotRoot string, manifest Manifest) error {
	expected := expectedPrivateArtifacts(manifest)
	if len(expected) == 0 {
		if _, err := os.Lstat(filepath.Join(snapshotRoot, privateArtifactManifestName)); err == nil {
			return errors.New("unexpected private artifact manifest for migration")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := readSnapshotFile(snapshotRoot, privateArtifactManifestName, -1, 16<<20)
	if err != nil {
		return errors.New("migration snapshot is missing its private AnyJev artifact manifest")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var actual privateArtifactManifest
	if err := decoder.Decode(&actual); err != nil {
		return errors.New("private AnyJev artifact manifest is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("private AnyJev artifact manifest has trailing data")
	}
	if actual.SchemaVersion != privateArtifactManifestSchema || actual.MigrationID != manifest.ID ||
		actual.SourceFingerprint != manifest.SourceFingerprint || len(actual.Artifacts) != len(expected) {
		return errors.New("private AnyJev artifact manifest does not match migration source")
	}
	expectedByPath := make(map[string]PrivateArtifactReference, len(expected))
	for _, artifact := range expected {
		expectedByPath[artifact.SourcePath] = artifact
	}
	seen := make(map[string]bool, len(actual.Artifacts))
	var totalBytes int64
	for _, artifact := range actual.Artifacts {
		want, exists := expectedByPath[artifact.SourcePath]
		if !exists || seen[artifact.SourcePath] || want.SHA256 != artifact.SHA256 || want.Bytes != artifact.Bytes ||
			want.LocalPath != artifact.LocalPath || artifact.Bytes < 0 || artifact.Bytes > maxFileBytes ||
			totalBytes > maxSnapshot-artifact.Bytes {
			return errors.New("private AnyJev artifact entry does not reconcile with migration source")
		}
		content, err := readSnapshotFile(snapshotRoot, artifact.LocalPath, artifact.Bytes, maxFileBytes)
		if err != nil || digest(content) != artifact.SHA256 {
			return errors.New("private AnyJev artifact failed digest verification")
		}
		seen[artifact.SourcePath] = true
		totalBytes += artifact.Bytes
	}
	if len(seen) != len(expected) {
		return errors.New("private AnyJev artifact count does not reconcile")
	}
	return nil
}

func ReadSnapshotArtifact(manifestPath string, manifest Manifest, sourcePath string) ([]byte, PrivateArtifactReference, error) {
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		return nil, PrivateArtifactReference{}, err
	}
	if manifest.ID != loaded.ID || manifest.SourceFingerprint != loaded.SourceFingerprint {
		return nil, PrivateArtifactReference{}, errors.New("provided migration manifest does not match the verified snapshot")
	}
	root, err := filepath.Abs(filepath.Dir(manifestPath))
	if err != nil {
		return nil, PrivateArtifactReference{}, err
	}
	for _, artifact := range expectedPrivateArtifacts(loaded) {
		if artifact.SourcePath != sourcePath {
			continue
		}
		data, err := readSnapshotFile(root, artifact.LocalPath, artifact.Bytes, maxFileBytes)
		if err != nil || digest(data) != artifact.SHA256 {
			return nil, PrivateArtifactReference{}, errors.New("private AnyJev artifact failed digest verification")
		}
		return data, artifact, nil
	}
	return nil, PrivateArtifactReference{}, errors.New("private AnyJev artifact not found in snapshot")
}

func sortedPrivateArtifacts(artifacts []PrivateArtifactReference) []PrivateArtifactReference {
	sorted := append([]PrivateArtifactReference(nil), artifacts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SourcePath < sorted[j].SourcePath })
	return sorted
}
