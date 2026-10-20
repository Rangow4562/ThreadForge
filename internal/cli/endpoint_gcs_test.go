package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEndpointGCSRequiresExplicitMetadataConsent(t *testing.T) {
	output := filepath.Join(t.TempDir(), "gcs-pack")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "gcs", "install-pack", "--bucket", "agent-audit-store", "--output", output,
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing explicit metadata consent code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("refused command created a pack: %v", err)
	}
}

func TestEndpointGCSPrintConfigIsMetadataOnlyAndCredentialFree(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "do-not-embed-service-account.json")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "gcs", "print-config", "--bucket", "agent-audit-store",
		"--prefix", "audit/threadforge", "--log-path", tracePath,
		"--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, "type = \"gcp_cloud_storage\"") ||
		!strings.Contains(config, "read_from = \"end\"") || !strings.Contains(config, strconv.Quote(filepath.Clean(tracePath))) ||
		!strings.Contains(config, `key_prefix = "audit/threadforge/runtime/date=%F/"`) ||
		!strings.Contains(config, "threadforge.metadata-export.v1") || strings.Contains(config, "do-not-embed") ||
		strings.Contains(config, "GOOGLE_APPLICATION_CREDENTIALS") || strings.Contains(config, "inventory") ||
		strings.Contains(config, "HOSTNAME") || strings.Contains(config, `"action":`) {
		t.Fatalf("print-config output is invalid, identity-bearing or credential-bearing code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointGCSInstallPackWritesLocallyAndPreservesExistingDirectory(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "never-write-this")
	output := filepath.Join(t.TempDir(), "gcs-pack")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	args := []string{
		"endpoint", "gcs", "install-pack", "--bucket", "agent-audit-store",
		"--prefix", "audit", "--log-path", tracePath, "--output", output,
		"--allow-remote-metadata-forwarding",
	}
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no network request was made") {
		t.Fatalf("install-pack failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	config, err := os.ReadFile(filepath.Join(output, "vector.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "never-write-this") || strings.Contains(string(config), "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Fatalf("generated config contains credentials: %s", config)
	}
	marker := filepath.Join(output, "user-owned.txt")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "refusing to overwrite") {
		t.Fatalf("existing output was not refused code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
		t.Fatalf("existing pack was modified: got=%q err=%v", got, err)
	}
}
