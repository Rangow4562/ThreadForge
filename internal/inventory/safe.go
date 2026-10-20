package inventory

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	MaxInventoryConfigBytes = 1 << 20
	MaxSkillManifestBytes   = 256 << 10
)

var (
	errInventoryUnsupported = errors.New("unsupported inventory file")
	errInventorySymlink     = errors.New("symlinked inventory path is not followed")
	errInventoryTooLarge    = errors.New("inventory file exceeds size limit")
	errInventoryChanged     = errors.New("inventory file changed while being read")
)

// ValidateScanRoot rejects symlinks in the supplied root or any parent and
// returns its absolute path. Inventory is intentionally a read-only local scan.
func ValidateScanRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := lstatNoSymlink(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("scan root is not a directory")
	}
	return absolute, nil
}

func readInventoryFile(path string, limit int) ([]byte, os.FileInfo, error) {
	if limit < 1 {
		return nil, nil, fmt.Errorf("invalid inventory file size limit")
	}
	initial, err := lstatNoSymlink(path)
	if err != nil {
		return nil, nil, err
	}
	if !initial.Mode().IsRegular() {
		return nil, initial, fmt.Errorf("%w: not a regular file", errInventoryUnsupported)
	}
	if initial.Size() > int64(limit) {
		return nil, initial, fmt.Errorf("%w: maximum is %d bytes", errInventoryTooLarge, limit)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, initial, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, initial, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(initial, opened) {
		return nil, initial, errInventoryChanged
	}
	if opened.Size() > int64(limit) {
		return nil, opened, fmt.Errorf("%w: maximum is %d bytes", errInventoryTooLarge, limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, opened, err
	}
	if len(data) > limit {
		return nil, opened, fmt.Errorf("%w: maximum is %d bytes", errInventoryTooLarge, limit)
	}

	finalHandle, err := file.Stat()
	if err != nil {
		return nil, opened, err
	}
	finalPath, err := lstatNoSymlink(path)
	if err != nil || !os.SameFile(opened, finalHandle) || !os.SameFile(opened, finalPath) ||
		opened.Size() != finalHandle.Size() || opened.Size() != finalPath.Size() ||
		!opened.ModTime().Equal(finalHandle.ModTime()) || !opened.ModTime().Equal(finalPath.ModTime()) {
		return nil, opened, errInventoryChanged
	}
	return data, finalPath, nil
}

func lstatNoSymlink(path string) (os.FileInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	abs := filepath.Clean(absolute)
	volume := filepath.VolumeName(abs)
	root := volume + string(os.PathSeparator)
	relative, err := filepath.Rel(root, abs)
	if err != nil {
		return nil, err
	}
	if relative == "." {
		return os.Lstat(abs)
	}
	current := root
	parts := strings.Split(relative, string(os.PathSeparator))
	var info os.FileInfo
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errInventorySymlink
		}
		if index < len(parts)-1 && !info.IsDir() {
			return nil, fmt.Errorf("%w: parent component is not a directory", errInventoryUnsupported)
		}
	}
	if info == nil {
		return os.Lstat(abs)
	}
	return info, nil
}

func inventoryErrorStatus(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return StatusNotFound
	case errors.Is(err, errInventoryUnsupported), errors.Is(err, errInventorySymlink), errors.Is(err, errInventoryTooLarge):
		return StatusUnsupported
	default:
		return StatusUnreadable
	}
}
