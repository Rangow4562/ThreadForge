package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOmoEmptyPrimaryOverrideUsesDefaultWithoutFallingThrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_CODING_AGENT_DIR", "")
	t.Setenv("SENPI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "legacy"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "older"))

	got, err := OmoExtensionPathForHome(home, LevelUser)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".omo", "agent", "extensions", "beacon.ts")
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}

func TestOmoUnsetPrimaryOverrideUsesFirstDefinedLegacyOverride(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(t.TempDir(), "senpi-agent")
	t.Setenv("OMO_CODING_AGENT_DIR", "unused")
	if err := os.Unsetenv("OMO_CODING_AGENT_DIR"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENPI_CODING_AGENT_DIR", legacy)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "older"))

	got, err := OmoExtensionPathForHome(home, LevelUser)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(legacy, "extensions", "beacon.ts")
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}
}

func TestSenpiThreadforgePathUsesTheCurrentRuntimeDirectoryOverridesAndOwnFilename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SENPI_CODING_AGENT_DIR", "~/senpi-agent")
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "ignored-legacy"))

	got, err := SenpiExtensionPathForHome(home, LevelUser)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "senpi-agent", "extensions", "threadforge.ts")
	if got != want {
		t.Fatalf("path=%q, want %q", got, want)
	}

	t.Setenv("SENPI_CODING_AGENT_DIR", "")
	got, err = SenpiExtensionPathForHome(home, LevelUser)
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(home, ".senpi", "agent", "extensions", "threadforge.ts")
	if got != want {
		t.Fatalf("empty primary path=%q, want default %q", got, want)
	}
}
