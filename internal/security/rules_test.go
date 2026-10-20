package security

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve/rulestore"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
	beaconrules "github.com/threadforge-ai/threadforge/third_party/agent-beacon/rules"
)

func TestManagedRuleLifecycleInstallsListsReplacesAndRemoves(t *testing.T) {
	source := baselineRuleFixture(t)
	storeDir := filepath.Join(t.TempDir(), "rules")
	ids, err := InstallManagedRules(storeDir, source, false)
	if err != nil || len(ids) != 1 || ids[0] != "curl-pipe-to-shell" {
		t.Fatalf("install IDs=%v error=%v", ids, err)
	}
	rules, err := ListManagedRules(storeDir)
	if err != nil || len(rules) != 1 || rules[0].ID != ids[0] || rules[0].Source != rulestore.SourceStore {
		t.Fatalf("installed rules=%#v error=%v", rules, err)
	}
	if _, err := InstallManagedRules(storeDir, source, false); err == nil {
		t.Fatal("duplicate rule install without force was accepted")
	}
	if ids, err := InstallManagedRules(storeDir, source, true); err != nil || len(ids) != 1 {
		t.Fatalf("validated replacement IDs=%v error=%v", ids, err)
	}
	if err := RemoveManagedRule(storeDir, "curl-pipe-to-shell"); err != nil {
		t.Fatal(err)
	}
	rules, err = ListManagedRules(storeDir)
	if err != nil || len(rules) == 0 || rules[0].Source != rulestore.SourceBaseline {
		t.Fatalf("removing the last managed rule did not restore the embedded baseline: rules=%#v error=%v", rules, err)
	}
}

func TestManagedRuleInstallBlocksCredentialsAndInvalidBatchAtomically(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "rules")
	baseline, err := os.ReadFile(baselineRuleFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	secretSource := filepath.Join(t.TempDir(), "secret.rule.yaml")
	secretRule := strings.Replace(string(baseline), "title: Remote script", "title: API_TOKEN=secret-value", 1)
	if err := os.WriteFile(secretSource, []byte(secretRule), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallManagedRules(storeDir, secretSource, false); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("credential-bearing rule error = %v", err)
	}
	if _, err := os.Lstat(storeDir); !os.IsNotExist(err) {
		t.Fatalf("credential rejection created managed store unexpectedly: %v", err)
	}

	batch := t.TempDir()
	if err := os.WriteFile(filepath.Join(batch, "valid.rule.yaml"), baseline, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "invalid.rule.yaml"), []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallManagedRules(storeDir, batch, false); err == nil {
		t.Fatal("batch containing an invalid rule was partially installed")
	}
	if rulestore.HasRuleFiles(storeDir) {
		t.Fatal("failed all-or-nothing rule batch left an installed rule")
	}
}

func TestBuiltinBeaconThreatRulePackInstallsFullValidatedCorpus(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "rules")
	baseline, err := ListManagedRules(storeDir)
	if err != nil || len(baseline) != 6 || baseline[0].Source != rulestore.SourceBaseline {
		t.Fatalf("default baseline rules=%#v error=%v", baseline, err)
	}

	ids, err := InstallBuiltinRulePack(storeDir, BeaconThreatRulePackV1, false)
	if err != nil {
		t.Fatalf("install full Beacon rule pack: %v", err)
	}
	if len(ids) != 75 {
		t.Fatalf("installed rules=%d, want 75", len(ids))
	}

	installed, err := ListManagedRules(storeDir)
	if err != nil || len(installed) != 75 {
		t.Fatalf("active rules=%d error=%v, want 75", len(installed), err)
	}
	seen := make(map[string]bool, len(installed))
	for _, rule := range installed {
		if rule.Source != rulestore.SourceStore || seen[rule.ID] {
			t.Fatalf("unexpected source or duplicate active rule: %#v", rule)
		}
		seen[rule.ID] = true
	}
	for _, id := range []string{"agent-permission-bypass-spawn", "ignore-previous-instructions", "git-remote-tampering"} {
		if !seen[id] {
			t.Errorf("full rule pack is missing %q", id)
		}
	}

	if _, err := InstallBuiltinRulePack(storeDir, BeaconThreatRulePackV1, false); err == nil {
		t.Fatal("duplicate full-pack installation succeeded without --force")
	}
	unchanged, err := ListManagedRules(storeDir)
	if err != nil || len(unchanged) != 75 {
		t.Fatalf("failed duplicate install changed active corpus: rules=%d error=%v", len(unchanged), err)
	}
}

func TestBuiltinThreatRulePackRejectsUnknownIDBeforeStoreCreation(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "rules")
	if _, err := InstallBuiltinRulePack(storeDir, "unknown-pack", false); err == nil {
		t.Fatal("unknown built-in rule pack was accepted")
	}
	if _, err := os.Lstat(storeDir); !os.IsNotExist(err) {
		t.Fatalf("unknown pack created the managed store: %v", err)
	}
}

func TestBuiltinBeaconRulePackContainsOnlyPinnedCredentialLikeFixtureFiles(t *testing.T) {
	paths, err := builtinRulePaths(BeaconThreatRulePackV1)
	if err != nil {
		t.Fatal(err)
	}
	var flagged []string
	for _, name := range paths {
		data, err := beaconrules.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if privacy.ContainsCredential(data) {
			flagged = append(flagged, name)
		}
	}
	want := []string{
		"agent-control/handoff-to-second-agent-with-credentials.rule.yaml",
		"agent-control/permission-scope-expansion-via-env.rule.yaml",
		"risky-command/sudoers-nopasswd-tampering.rule.yaml",
	}
	if !slices.Equal(flagged, want) {
		t.Fatalf("credential-like content in built-in pack=%v, want only fixed upstream fixtures %v", flagged, want)
	}
}

func baselineRuleFixture(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate security package fixture")
	}
	return filepath.Join(filepath.Dir(currentFile), "..", "..", "third_party", "agent-beacon", "pkg", "asymptoteobserve", "rulestore", "baseline", "curl-pipe-to-shell.rule.yaml")
}
