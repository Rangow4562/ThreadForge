package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanReturnsMetadataWithoutConfigurationBodiesOrSecrets(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"apiKey":"TOP_LEVEL_SECRET","mcpServers":{"demo":{"command":"node","args":["server.js"],"env":{"API_TOKEN":"MCP_SECRET","VISIBLE_MODE":"local"}}}}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	result := Scan(Options{HomeDir: home, WorkingDir: project, Runtimes: []string{"claude_code"}})
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "TOP_LEVEL_SECRET") || strings.Contains(string(encoded), "MCP_SECRET") ||
		strings.Contains(string(encoded), "server.js") || strings.Contains(string(encoded), "local") {
		t.Fatalf("inventory leaked config values: %s", encoded)
	}
	var foundConfig bool
	for _, config := range result.Configs {
		if config.PathHash == hashString(configPath) {
			foundConfig = true
			if config.Content != nil || !config.Readable || config.MCPServerCount != 1 {
				t.Fatalf("unexpected config metadata: %+v", config)
			}
		}
	}
	if !foundConfig {
		t.Fatalf("Claude configuration was not inventoried: %+v", result.Configs)
	}
	if len(result.MCPServers) != 1 || result.MCPServers[0].Definition != nil || len(result.MCPServers[0].EnvKeys) != 1 || result.MCPServers[0].EnvKeys[0] != "VISIBLE_MODE" {
		t.Fatalf("unexpected MCP metadata: %+v", result.MCPServers)
	}
}

func TestScanRejectsOversizedConfigAndSkillManifest(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Repeat(" ", MaxInventoryConfigBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(home, ".claude", "skills", "large", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(strings.Repeat("x", MaxSkillManifestBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}

	result := Scan(Options{HomeDir: home, WorkingDir: project, Runtimes: []string{"claude_code"}})
	for _, config := range result.Configs {
		if config.PathHash == hashString(configPath) {
			if config.ParserStatus != StatusUnsupported || config.Readable || config.MCPServerCount != 0 {
				t.Fatalf("oversized config was not rejected: %+v", config)
			}
		}
	}
	var oversizedSkillFound bool
	for _, skill := range result.Skills {
		if skill.SkillNameHash == hashString("large") && skill.SourceScope == ScopeUser {
			oversizedSkillFound = skill.ParserStatus == StatusUnsupported && !skill.Readable
		}
	}
	if !oversizedSkillFound {
		t.Fatalf("oversized skill manifest was not rejected: %+v", result.Skills)
	}
}

func TestSenpiInventoryDistinguishesLegacyAndThreadforgeManagedExtensions(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SENPI_CODING_AGENT_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	legacyPath := filepath.Join(home, ".omo", "agent", "extensions", "beacon.ts")
	threadforgeProjectPath := filepath.Join(project, ".senpi", "extensions", "threadforge.ts")
	for _, entry := range []struct{ path, marker string }{
		{legacyPath, "// beacon-managed-omo-extension:v1"},
		{threadforgeProjectPath, "// threadforge-managed-senpi-extension:v1"},
	} {
		if err := os.MkdirAll(filepath.Dir(entry.path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(entry.path, []byte(entry.marker+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	result := Scan(Options{HomeDir: home, WorkingDir: project, Runtimes: []string{"omo_senpi"}})
	found := map[string]Config{}
	for _, config := range result.Configs {
		if config.PathHash == hashString(legacyPath) || config.PathHash == hashString(threadforgeProjectPath) {
			found[config.PathHash] = config
		}
	}
	legacy, ok := found[hashString(legacyPath)]
	if !ok || !legacy.Exists || !legacy.BeaconManaged || legacy.ThreadforgeManaged {
		t.Fatalf("legacy Senpi extension was misclassified: %+v", legacy)
	}
	threadforge, ok := found[hashString(threadforgeProjectPath)]
	if !ok || !threadforge.Exists || threadforge.BeaconManaged || !threadforge.ThreadforgeManaged || threadforge.Scope != ScopeProject {
		t.Fatalf("Threadforge Senpi extension was misclassified: %+v", threadforge)
	}
}

func TestScanRefusesSymlinkedConfigAndSkillRoot(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()
	secretConfig := filepath.Join(outside, "settings.json")
	if err := os.WriteFile(secretConfig, []byte(`{"mcpServers":{"hidden":{"command":"secret-command"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secretConfig, configPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	skillsLink := filepath.Join(home, ".claude", "skills")
	if err := os.Symlink(outside, skillsLink); err != nil {
		t.Skipf("directory symlink creation is unavailable: %v", err)
	}

	result := Scan(Options{HomeDir: home, WorkingDir: project, Runtimes: []string{"claude_code"}})
	for _, config := range result.Configs {
		if config.PathHash == hashString(configPath) {
			if config.ParserStatus != StatusUnsupported || config.Readable {
				t.Fatalf("symlinked config was not rejected: %+v", config)
			}
		}
	}
	if len(result.MCPServers) != 0 {
		t.Fatalf("MCP metadata escaped a refused config: %+v", result.MCPServers)
	}
	var symlinkSkillRootFound bool
	for _, skill := range result.Skills {
		if skill.RootPathHash == hashString(skillsLink) && skill.SourceScope == ScopeUser {
			symlinkSkillRootFound = skill.ParserStatus == StatusUnsupported
		}
	}
	if !symlinkSkillRootFound {
		t.Fatalf("symlinked skill root was not rejected: %+v", result.Skills)
	}
}

func TestDefaultInventoryExcludesSystemAndManagedScopes(t *testing.T) {
	items := []candidate{
		{runtime: "user", scope: ScopeUser},
		{runtime: "managed", scope: ScopeManaged},
		{runtime: "system", scope: ScopeSystem},
	}
	filtered := withoutSystemScope(items)
	if len(filtered) != 1 || filtered[0].scope != ScopeUser {
		t.Fatalf("system and managed candidates were not excluded: %+v", filtered)
	}
}

func TestValidateScanRootRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if _, err := ValidateScanRoot(link); err == nil {
		t.Fatal("expected symlinked scan root to be rejected")
	}
}
