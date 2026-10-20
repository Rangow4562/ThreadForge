package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEndpointSplunkRequiresExplicitMetadataConsent(t *testing.T) {
	output := filepath.Join(t.TempDir(), "splunk-pack")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "splunk-hec", "install-pack", "--endpoint", "https://splunk.example:8088", "--output", output,
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing explicit metadata consent code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("refused command created pack: %v", err)
	}
}

func TestEndpointSplunkPrintConfigIsHTTPSMetadataOnlyAndCredentialFree(t *testing.T) {
	t.Setenv("THREADFORGE_SPLUNK_HEC_TOKEN", "do-not-embed-token")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "splunk-hec", "print-config", "--endpoint", "https://splunk.example:8088/",
		"--index", "security_ops", "--log-path", tracePath,
		"--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, "type = \"splunk_hec_logs\"") ||
		!strings.Contains(config, "read_from = \"end\"") || !strings.Contains(config, strconv.Quote(filepath.Clean(tracePath))) ||
		!strings.Contains(config, "endpoint = \"https://splunk.example:8088\"") ||
		!strings.Contains(config, "default_token = \"SECRET[threadforgesecrets.splunkhectoken]\"") ||
		!strings.Contains(config, "threadforge.metadata-export.v1") || strings.Contains(config, "do-not-embed-token") ||
		strings.Contains(config, "HOSTNAME") || strings.Contains(config, `"action":`) {
		t.Fatalf("print-config output is invalid, secret-bearing or raw code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointSplunkInstallPackWritesLocallyAndPreservesExistingDirectory(t *testing.T) {
	t.Setenv("THREADFORGE_SPLUNK_HEC_TOKEN", "never-write-this")
	output := filepath.Join(t.TempDir(), "splunk-pack")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	args := []string{
		"endpoint", "splunk-hec", "install-pack", "--endpoint", "https://splunk.example:8088",
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
	if strings.Contains(string(config), "never-write-this") {
		t.Fatalf("generated config contains token value: %s", config)
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
