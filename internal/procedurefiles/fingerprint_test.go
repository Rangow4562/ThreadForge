package procedurefiles

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprintSourcesHashesOnlyExplicitProjectFiles(t *testing.T) {
	root := t.TempDir()
	content := []byte("package example\n")
	path := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := FingerprintSources(root, []string{"src/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(content)
	if len(result) != 1 || result[0].Path != "src/main.go" || result[0].SHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("unexpected source fingerprints: %#v", result)
	}
}

func TestFingerprintSourcesRejectsUnsafeAndAmbiguousInputs(t *testing.T) {
	root := t.TempDir()
	if _, err := FingerprintSources(root, nil); err == nil {
		t.Fatal("empty source list was accepted")
	}
	if _, err := FingerprintSources(root, []string{".env"}); err == nil {
		t.Fatal("credential-bearing source path was accepted")
	}
	if _, err := FingerprintSources(root, []string{"src/main.go", "src/main.go"}); err == nil {
		t.Fatal("duplicate source path was accepted")
	}
	if _, err := FingerprintSources(root, []string{"../outside.go"}); err == nil {
		t.Fatal("path traversal was accepted")
	}
}

func TestFingerprintSourcesRejectsSymbolicLinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symbolic links are unavailable in this environment")
	}
	if _, err := FingerprintSources(root, []string{"linked.go"}); err == nil {
		t.Fatal("symbolic source link was accepted")
	}
}
