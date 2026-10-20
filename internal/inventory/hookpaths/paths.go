// Package hooks contains read-only path resolution used by the Beacon-derived inventory scanner.
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Level string

const (
	LevelUser    Level = "user"
	LevelProject Level = "project"

	PiManagedExtensionMarker    = "beacon-managed-pi-extension:v1"
	OmpManagedExtensionMarker   = "beacon-managed-omp-extension:v1"
	OpenClawManagedPluginMarker = "beacon-managed-openclaw-plugin:v1"
	PrimeManagedExtensionMarker = "beacon-managed-prime-extension:v1"
	OmoManagedExtensionMarker   = "beacon-managed-omo-extension:v1"
	SenpiManagedExtensionMarker = "threadforge-managed-senpi-extension:v1"
)

func OmpExtensionPathForHome(home string, level Level) (string, error) {
	switch level {
	case "", LevelUser:
		if agentDir := os.Getenv("PI_CODING_AGENT_DIR"); agentDir != "" {
			return filepath.Join(agentDir, "extensions", "beacon.ts"), nil
		}
		if home == "" {
			return "", fmt.Errorf("home directory is required")
		}
		configDir := ".omp"
		if override := os.Getenv("PI_CONFIG_DIR"); override != "" {
			configDir = override
		}
		return filepath.Join(home, configDir, "agent", "extensions", "beacon.ts"), nil
	case LevelProject:
		workingDir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(workingDir, ".omp", "extensions", "beacon.ts"), nil
	default:
		return "", fmt.Errorf("unknown inventory scope %q", level)
	}
}

func OpenClawEntryPathForHome(home string, level Level) (string, error) {
	switch level {
	case "", LevelUser:
		stateDir := strings.TrimSpace(os.Getenv("OPENCLAW_STATE_DIR"))
		if stateDir == "" {
			if home == "" {
				return "", fmt.Errorf("home directory is required")
			}
			stateDir = filepath.Join(home, ".openclaw")
		}
		return filepath.Join(stateDir, "extensions", "beacon-endpoint", "beacon.js"), nil
	case LevelProject:
		workingDir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(workingDir, ".openclaw", "extensions", "beacon-endpoint", "beacon.js"), nil
	default:
		return "", fmt.Errorf("unknown inventory scope %q", level)
	}
}

func PrimeExtensionPathForHome(home string, level Level) (string, error) {
	switch level {
	case "", LevelUser:
		if override := os.Getenv("PRIME_AGENT_CODING_AGENT_DIR"); override != "" {
			dir, err := expandHomePrefix(override, home)
			if err != nil {
				return "", err
			}
			return filepath.Join(dir, "extensions", "beacon.ts"), nil
		}
		if home == "" {
			return "", fmt.Errorf("home directory is required")
		}
		return filepath.Join(home, ".prime", "agent", "extensions", "beacon.ts"), nil
	case LevelProject:
		workingDir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(workingDir, ".prime", "agent", "extensions", "beacon.ts"), nil
	default:
		return "", fmt.Errorf("unknown inventory scope %q", level)
	}
}

func OmoExtensionPathForHome(home string, level Level) (string, error) {
	switch level {
	case "", LevelUser:
		for _, name := range []string{"OMO_CODING_AGENT_DIR", "SENPI_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
			if override, ok := os.LookupEnv(name); ok {
				if override != "" {
					dir, err := expandHomePrefix(override, home)
					if err != nil {
						return "", err
					}
					return filepath.Join(dir, "extensions", "beacon.ts"), nil
				}
				// Match the runtime: an explicitly empty first variable prevents
				// fallback to legacy variables, then uses the default directory.
				break
			}
		}
		if home == "" {
			return "", fmt.Errorf("home directory is required")
		}
		return filepath.Join(home, ".omo", "agent", "extensions", "beacon.ts"), nil
	case LevelProject:
		workingDir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(workingDir, ".omo", "agent", "extensions", "beacon.ts"), nil
	default:
		return "", fmt.Errorf("unknown inventory scope %q", level)
	}
}

// SenpiExtensionPathForHome resolves Threadforge's distinct Senpi extension alongside Beacon's
// legacy OMO path. Current Senpi uses SENPI_CODING_AGENT_DIR then the legacy PI override; the
// project extension path is fixed at .senpi/extensions.
func SenpiExtensionPathForHome(home string, level Level) (string, error) {
	switch level {
	case "", LevelUser:
		for _, name := range []string{"SENPI_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
			if override, ok := os.LookupEnv(name); ok {
				if override != "" {
					dir, err := expandHomePrefix(override, home)
					if err != nil {
						return "", err
					}
					return filepath.Join(dir, "extensions", "threadforge.ts"), nil
				}
				break
			}
		}
		if home == "" {
			return "", fmt.Errorf("home directory is required")
		}
		return filepath.Join(home, ".senpi", "agent", "extensions", "threadforge.ts"), nil
	case LevelProject:
		workingDir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(workingDir, ".senpi", "extensions", "threadforge.ts"), nil
	default:
		return "", fmt.Errorf("unknown inventory scope %q", level)
	}
}

func expandHomePrefix(path, home string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}
	if home == "" {
		return "", fmt.Errorf("home directory is required to expand %q", path)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, filepath.FromSlash(path[2:])), nil
}
