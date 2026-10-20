package forwarding

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const vectorSecretBackendName = "threadforgesecrets"

// DefaultVectorSecretsDir returns the local user-config location used when a
// pack command does not receive an explicit --secrets-dir override.
func DefaultVectorSecretsDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory for Vector secrets: %w", err)
	}
	return filepath.Abs(filepath.Join(configDir, "threadforge", "vector-secrets"))
}

func normalizeVectorSecretsDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("Vector secrets directory must not contain control characters")
	}
	if path == "" {
		return DefaultVectorSecretsDir()
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Vector secrets directory: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func renderVectorSecretDirectory(secretsDir string) string {
	return fmt.Sprintf(`[secret.%s]
type = "directory"
path = %s
remove_trailing_whitespace = true
`, vectorSecretBackendName, strconv.Quote(secretsDir))
}

func vectorSecretReference(name string) string {
	return "SECRET[" + vectorSecretBackendName + "." + name + "]"
}
