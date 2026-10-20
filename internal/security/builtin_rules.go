package security

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve/rulestore"
	beaconrules "github.com/threadforge-ai/threadforge/third_party/agent-beacon/rules"
)

const BeaconThreatRulePackV1 = "beacon-threat-rules-v1"

const (
	builtinBeaconRuleCount  = 75
	builtinBeaconRuleSHA256 = "fc5b9df22a8531200defb6ecabd3f10629d4730494c2ae2880a3488a17c18f21"
)

// The full Beacon corpus is an opt-in data pack. Scans continue to use the
// upstream frozen baseline until an operator explicitly installs this pack.
//
// InstallBuiltinRulePack stages one known embedded pack and sends it through the
// same schema, fixture, and atomic-install checks as external rules. It verifies
// the exact pinned asset digest first; this corpus has three synthetic fixture files
// that intentionally resemble credentials and therefore fail the user-source scanner.
func InstallBuiltinRulePack(storeDir, packID string, force bool) ([]string, error) {
	rulePaths, err := builtinRulePaths(packID)
	if err != nil {
		return nil, err
	}
	if err := validateManagedRuleStore(storeDir, false); err != nil {
		return nil, err
	}

	staging, err := os.MkdirTemp("", "threadforge-builtin-rules-")
	if err != nil {
		return nil, fmt.Errorf("create temporary built-in rule staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	for _, name := range rulePaths {
		data, err := beaconrules.Files.ReadFile(name)
		if err != nil {
			return nil, err
		}
		relative := filepath.ToSlash(name)
		if !safeRuleRelative(relative) {
			return nil, fmt.Errorf("built-in threat-rule pack %q contains an unsafe path", packID)
		}
		destination := filepath.Join(staging, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			return nil, err
		}
	}
	if err := validateManagedRuleStore(storeDir, true); err != nil {
		return nil, err
	}
	installed, err := rulestore.InstallFiles(storeDir, staging, force)
	if err != nil {
		return nil, fmt.Errorf("install validated built-in threat-rule pack: %w", err)
	}
	ids := make([]string, 0, len(installed))
	for _, item := range installed {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func builtinRulePaths(packID string) ([]string, error) {
	if packID != BeaconThreatRulePackV1 {
		return nil, fmt.Errorf("unknown built-in threat-rule pack %q", packID)
	}
	paths, err := fs.Glob(beaconrules.Files, "*/*"+rulestore.RuleFileSuffix)
	if err != nil {
		return nil, fmt.Errorf("read built-in threat-rule pack: %w", err)
	}
	sort.Strings(paths)
	if len(paths) != builtinBeaconRuleCount {
		return nil, fmt.Errorf("built-in threat-rule pack %q has %d rules, want %d", packID, len(paths), builtinBeaconRuleCount)
	}
	manifest := sha256.New()
	for _, name := range paths {
		data, err := beaconrules.Files.ReadFile(name)
		if err != nil {
			return nil, err
		}
		_, _ = manifest.Write([]byte(name))
		_, _ = manifest.Write([]byte{0})
		_, _ = manifest.Write(data)
		_, _ = manifest.Write([]byte{0})
	}
	if got := hex.EncodeToString(manifest.Sum(nil)); got != builtinBeaconRuleSHA256 {
		return nil, fmt.Errorf("built-in threat-rule pack %q does not match its pinned content digest", packID)
	}
	return paths, nil
}
