package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEndpointCloudWatchRequiresExplicitMetadataConsent(t *testing.T) {
	output := filepath.Join(t.TempDir(), "cloudwatch-pack")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "cloudwatch", "install-pack", "--log-group", "/threadforge/runtime", "--output", output,
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing explicit metadata consent code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("refused command created a pack: %v", err)
	}
}

func TestEndpointCloudWatchPrintConfigIsMetadataOnlyAndCredentialFree(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "do-not-embed-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "do-not-embed-secret")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "cloudwatch", "print-config", "--log-group", "/threadforge/runtime",
		"--region", "us-west-2", "--log-path", tracePath,
		"--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, "type = \"aws_cloudwatch_logs\"") ||
		!strings.Contains(config, "read_from = \"end\"") || !strings.Contains(config, strconv.Quote(filepath.Clean(tracePath))) ||
		!strings.Contains(config, "threadforge.metadata-export.v1") || strings.Contains(config, "do-not-embed") ||
		strings.Contains(config, "AWS_SECRET_ACCESS_KEY") || strings.Contains(config, "HOSTNAME") ||
		strings.Contains(config, `"action":`) {
		t.Fatalf("print-config output is invalid, identity-bearing or credential-bearing code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointCloudWatchInstallPackWritesLocallyAndPreservesExistingDirectory(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "never-write-this")
	output := filepath.Join(t.TempDir(), "cloudwatch-pack")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	args := []string{
		"endpoint", "cloudwatch", "install-pack", "--log-group", "/threadforge/runtime",
		"--log-path", tracePath, "--output", output, "--allow-remote-metadata-forwarding",
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
