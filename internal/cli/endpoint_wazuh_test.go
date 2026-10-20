package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEndpointWazuhRequiresExplicitDownstreamConsentAndOutputPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"endpoint", "wazuh", "install-pack"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing Wazuh downstream consent was accepted code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"endpoint", "wazuh", "print-config", "--allow-remote-metadata-forwarding"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--metadata-log-path") {
		t.Fatalf("missing sanitized output path was accepted code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestEndpointWazuhPrintsLocalMetadataOnlyVectorConfig(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "runtime.jsonl")
	metadataPath := filepath.Join(root, "wazuh-metadata.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "wazuh", "print-config", "--log-path", logPath,
		"--metadata-log-path", metadataPath, "--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, `read_from = "end"`) ||
		!strings.Contains(config, `path = `+strconv.Quote(metadataPath)) ||
		!strings.Contains(config, "threadforge.metadata-export.v1") ||
		strings.Contains(config, "prompt") || strings.Contains(config, "HOSTNAME") {
		t.Fatalf("Wazuh print-config violated metadata-only contract code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointWazuhInstallPackWritesLocallyAndRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "wazuh-pack")
	metadataPath := filepath.Join(root, "threadforge-metadata.jsonl")
	args := []string{
		"endpoint", "wazuh", "install-pack", "--log-path", filepath.Join(root, "runtime.jsonl"),
		"--metadata-log-path", metadataPath, "--output", output, "--allow-remote-metadata-forwarding",
	}
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no network request was made") {
		t.Fatalf("install-pack failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "threadforge-rules.xml", "ossec-localfile.xml", "vector.toml"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing pack file %s: %v", name, err)
		}
	}
	if code := Run(args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "refusing to overwrite") {
		t.Fatalf("existing output directory was not refused code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
