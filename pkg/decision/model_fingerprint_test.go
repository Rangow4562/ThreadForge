package decision

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprintModelDirectoryIsStableAndContentSensitive(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "weights"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"name":"fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	weightPath := filepath.Join(root, "weights", "model.safetensors")
	if err := os.WriteFile(weightPath, []byte("abcd"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := FingerprintModelDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FingerprintModelDirectory(root)
	if err != nil || first != second || first.FileCount != 2 || first.TotalBytes != 22 {
		t.Fatalf("unstable fingerprint %#v %#v error=%v", first, second, err)
	}
	if first.SHA256 != "b8719bf6410c3eb3c83acb3abe4d5adf1f584df57a20cc786a68af580c0ff955" {
		t.Fatalf("Go fingerprint differs from the Python protocol contract: %s", first.SHA256)
	}
	if err := os.WriteFile(weightPath, []byte("wxyz"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := FingerprintModelDirectory(root)
	if err != nil || changed.SHA256 == first.SHA256 {
		t.Fatalf("content change did not change the fingerprint: %#v error=%v", changed, err)
	}
}

func TestFingerprintModelDirectoryRejectsEmptyAndLinkedTrees(t *testing.T) {
	if _, err := FingerprintModelDirectory(t.TempDir()); err == nil {
		t.Fatal("accepted an empty checkpoint")
	}
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "weights.bin")
	if err := os.WriteFile(target, []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "weights.bin")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := FingerprintModelDirectory(root); err == nil {
		t.Fatal("accepted a checkpoint tree with a symlink")
	}
}
