package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/inventory"
	"github.com/threadforge-ai/threadforge/internal/traces"
)

func appendWazuhValidationEvent(tracePath string) (string, error) {
	tracePath = strings.TrimSpace(tracePath)
	if tracePath == "" || len(tracePath) > 4096 || strings.ContainsAny(tracePath, "\x00\r\n") {
		return "", errors.New("Wazuh validation trace path must be nonempty, bounded, and contain no control separators")
	}
	absolute, err := filepath.Abs(tracePath)
	if err != nil {
		return "", errors.New("Wazuh validation trace path is invalid")
	}
	if err := validateWazuhValidationParent(filepath.Dir(absolute)); err != nil {
		return "", err
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", errors.New("Wazuh validation trace must be a regular, non-symlink file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("Wazuh validation trace could not be inspected")
	}

	store, err := traces.Open(absolute)
	if err != nil {
		return "", fmt.Errorf("open local Threadforge trace: %w", err)
	}
	event := beacon.Event{
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Vendor:        beacon.Vendor,
		Product:       beacon.Product,
		SchemaVersion: beacon.SchemaVersion,
		Event: beacon.EventInfo{
			Kind: "agent_runtime", Action: "agent.detected", Category: "validation",
		},
		Severity: beacon.SeverityInfo,
		Endpoint: beacon.EndpointInfo{OS: runtime.GOOS},
		Harness:  beacon.HarnessInfo{Name: "threadforge"},
		Message:  "Threadforge Wazuh validation event",
	}
	if err := store.Append(context.Background(), []beacon.Event{event}); err != nil {
		return "", fmt.Errorf("append local Wazuh validation event: %w", err)
	}
	return absolute, nil
}

func validateWazuhValidationParent(parent string) error {
	current := filepath.Clean(parent)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("Wazuh validation trace parent must be a real directory")
			}
			if _, err := inventory.ValidateScanRoot(current); err != nil {
				return errors.New("Wazuh validation trace parent contains an unavailable or linked path")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("Wazuh validation trace parent could not be inspected")
		}
		next := filepath.Dir(current)
		if next == current {
			return errors.New("Wazuh validation trace parent has no existing filesystem root")
		}
		current = next
	}
}
