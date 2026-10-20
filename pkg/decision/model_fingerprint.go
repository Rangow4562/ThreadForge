package decision

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const ModelCheckpointFingerprintSchemaVersion = "threadforge.model-checkpoint-fingerprint.v1"
const maxModelCheckpointFiles = 20000
const maxModelCheckpointBytes int64 = 1 << 40

type ModelCheckpointFingerprint struct {
	SchemaVersion string `json:"schema_version"`
	SHA256        string `json:"sha256"`
	FileCount     int    `json:"file_count"`
	TotalBytes    int64  `json:"total_bytes"`
}

type modelCheckpointFile struct {
	path string
	rel  string
	info os.FileInfo
}

type modelCheckpointDirectory struct {
	path string
	info os.FileInfo
}

// FingerprintModelDirectory hashes a materialized local checkpoint without
// returning its path or file names. Symlinks and special files are rejected so
// the digest has stable, bounded meaning across approval and inference.
func FingerprintModelDirectory(root string) (ModelCheckpointFingerprint, error) {
	if strings.TrimSpace(root) == "" {
		return ModelCheckpointFingerprint{}, errors.New("model checkpoint path is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return ModelCheckpointFingerprint{}, errors.New("model checkpoint path is invalid")
	}
	rootInfo, err := os.Lstat(absolute)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ModelCheckpointFingerprint{}, errors.New("model checkpoint must be a real local directory")
	}
	files := make([]modelCheckpointFile, 0)
	directories := []modelCheckpointDirectory{{path: absolute, info: rootInfo}}
	var total int64
	err = filepath.WalkDir(absolute, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("model checkpoint could not be read consistently")
		}
		if path == absolute {
			return nil
		}
		info, infoErr := os.Lstat(path)
		if infoErr != nil || entry.Type()&os.ModeSymlink != 0 || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("model checkpoint contains an unsupported link or unreadable entry")
		}
		if info.IsDir() {
			directories = append(directories, modelCheckpointDirectory{path: path, info: info})
			return nil
		}
		if !info.Mode().IsRegular() || !utf8.ValidString(path) {
			return errors.New("model checkpoint contains an unsupported file")
		}
		rel, relErr := filepath.Rel(absolute, path)
		if relErr != nil {
			return errors.New("model checkpoint path is inconsistent")
		}
		rel = filepath.ToSlash(rel)
		if !utf8.ValidString(rel) || len(rel) > 4096 || len(files) >= maxModelCheckpointFiles ||
			info.Size() < 0 || info.Size() > maxModelCheckpointBytes-total {
			return errors.New("model checkpoint exceeds file-count or byte limits")
		}
		total += info.Size()
		files = append(files, modelCheckpointFile{path: path, rel: rel, info: info})
		return nil
	})
	if err != nil {
		return ModelCheckpointFingerprint{}, err
	}
	if len(files) == 0 {
		return ModelCheckpointFingerprint{}, errors.New("model checkpoint directory contains no files")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	manifest := sha256.New()
	for _, item := range files {
		file, openErr := os.Open(item.path)
		if openErr != nil {
			return ModelCheckpointFingerprint{}, errors.New("model checkpoint changed while fingerprinting")
		}
		before, statErr := file.Stat()
		pathBefore, pathStatErr := os.Lstat(item.path)
		if statErr != nil || pathStatErr != nil || !before.Mode().IsRegular() || !pathBefore.Mode().IsRegular() ||
			before.Size() != item.info.Size() || pathBefore.Size() != item.info.Size() ||
			!before.ModTime().Equal(item.info.ModTime()) || !pathBefore.ModTime().Equal(item.info.ModTime()) {
			_ = file.Close()
			return ModelCheckpointFingerprint{}, errors.New("model checkpoint changed while fingerprinting")
		}
		contentHash := sha256.New()
		readBytes, copyErr := io.Copy(contentHash, file)
		after, afterErr := file.Stat()
		closeErr := file.Close()
		pathAfter, pathAfterErr := os.Lstat(item.path)
		if copyErr != nil || afterErr != nil || closeErr != nil || pathAfterErr != nil ||
			!after.Mode().IsRegular() || !pathAfter.Mode().IsRegular() || readBytes != item.info.Size() ||
			after.Size() != item.info.Size() || pathAfter.Size() != item.info.Size() ||
			!after.ModTime().Equal(item.info.ModTime()) || !pathAfter.ModTime().Equal(item.info.ModTime()) {
			return ModelCheckpointFingerprint{}, errors.New("model checkpoint changed while fingerprinting")
		}
		_, _ = manifest.Write([]byte(item.rel))
		_, _ = manifest.Write([]byte{0})
		_, _ = manifest.Write([]byte(strconv.FormatInt(readBytes, 10)))
		_, _ = manifest.Write([]byte{0})
		_, _ = manifest.Write([]byte(hex.EncodeToString(contentHash.Sum(nil))))
		_, _ = manifest.Write([]byte{'\n'})
	}
	for _, directory := range directories {
		current, statErr := os.Lstat(directory.path)
		if statErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 ||
			!current.ModTime().Equal(directory.info.ModTime()) {
			return ModelCheckpointFingerprint{}, errors.New("model checkpoint changed while fingerprinting")
		}
	}
	return ModelCheckpointFingerprint{
		SchemaVersion: ModelCheckpointFingerprintSchemaVersion,
		SHA256:        hex.EncodeToString(manifest.Sum(nil)),
		FileCount:     len(files),
		TotalBytes:    total,
	}, nil
}
