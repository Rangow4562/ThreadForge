package forwarding

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func renderLocalMetadataVectorConfig(logPath, metadataLogPath, sinkName, projectionVRL string) string {
	return fmt.Sprintf(`# Threadforge runtime JSONL -> local metadata-only JSONL.
# This file is inert until you install and start Vector yourself.

[sources.threadforge_runtime]
type = "file"
include = [%s]
read_from = "end"

[transforms.threadforge_runtime_metadata]
type = "remap"
inputs = ["threadforge_runtime"]
source = '''
%s
'''

[sinks.%s]
type = "file"
inputs = ["threadforge_runtime_metadata"]
path = %s

[sinks.%s.encoding]
codec = "json"

[sinks.%s.framing]
method = "newline_delimited"
`, strconv.Quote(logPath), projectionVRL, sinkName, strconv.Quote(metadataLogPath), sinkName, sinkName)
}

// normalizeMetadataOutputLogPath validates a separate local JSONL destination
// used by a metadata-only Vector handoff. It refuses paths that could overwrite
// the raw trace, follow a link, or backfill pre-existing non-metadata content.
func normalizeMetadataOutputLogPath(path, consumer string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n\t") {
		return "", fmt.Errorf("%s metadata log path must be nonempty, bounded, and contain no control separators", consumer)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s metadata log path must be absolute", consumer)
	}
	path = filepath.Clean(path)
	if err := validateRealDirectoryChain(filepath.Dir(path)); err != nil {
		return "", fmt.Errorf("validate %s metadata output directory: %w", consumer, err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s metadata output must be a regular file, not a link or special file", consumer)
		}
		if info.Size() != 0 {
			return "", fmt.Errorf("%s metadata output must be absent or empty; refusing to forward pre-existing file content", consumer)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect %s metadata output: %w", consumer, err)
	}
	return path, nil
}
