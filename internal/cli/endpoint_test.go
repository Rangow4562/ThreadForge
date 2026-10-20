package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEndpointS3RequiresExplicitForwardingAcknowledgement(t *testing.T) {
	output := filepath.Join(t.TempDir(), "pack")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "s3", "install-pack", "--bucket", "agent-audit-store", "--output", output,
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing forwarding acknowledgement code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("refused command created output directory: %v", err)
	}
}

func TestEndpointS3PrintConfigIsLocalAndCredentialFree(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "do-not-embed-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "do-not-embed-secret")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "s3", "print-config", "--bucket", "agent-audit-store",
		"--region", "us-west-2", "--prefix", "audit/threadforge",
		"--log-path", tracePath, "--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "read_from = \"end\"") ||
		!strings.Contains(stdout.String(), strconv.Quote(filepath.Clean(tracePath))) ||
		strings.Contains(stdout.String(), "do-not-embed") || strings.Contains(stdout.String(), "AWS_SECRET_ACCESS_KEY") {
		t.Fatalf("print-config output invalid or credential-bearing code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestEndpointS3InstallPackWritesLocalAssetsAndPreservesExistingDirectory(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "never-write-this")
	output := filepath.Join(t.TempDir(), "s3-pack")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	args := []string{
		"endpoint", "s3", "install-pack", "--bucket", "agent-audit-store",
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
	if strings.Contains(string(config), "never-write-this") || strings.Contains(string(config), "AWS_ACCESS_KEY_ID") {
		t.Fatalf("generated config contains credentials: %s", config)
	}
	sample, err := os.ReadFile(filepath.Join(output, "sample-event.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sample), "never-write-this") || strings.Contains(string(sample), "prompt") {
		t.Fatalf("generated sample contains secrets or prompt content: %s", sample)
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
		t.Fatalf("existing output was modified: got=%q err=%v", got, err)
	}
}
