package forwarding

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type privatePackFile struct {
	name    string
	content []byte
}

func writePrivatePack(outputDir string, files []privatePackFile) (string, error) {
	abs, err := newPackDirectory(outputDir)
	if err != nil {
		return "", err
	}
	for _, file := range files {
		path := filepath.Join(abs, file.name)
		output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return abs, fmt.Errorf("create forwarding pack file %s: %w; partial output was preserved", file.name, err)
		}
		written, writeErr := output.Write(file.content)
		closeErr := output.Close()
		if writeErr != nil || written != len(file.content) {
			if writeErr == nil {
				writeErr = errors.New("short write")
			}
			return abs, fmt.Errorf("write forwarding pack file %s: %w; partial output was preserved", file.name, writeErr)
		}
		if closeErr != nil {
			return abs, fmt.Errorf("close forwarding pack file %s: %w; partial output was preserved", file.name, closeErr)
		}
	}
	return abs, nil
}
