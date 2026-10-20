package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func sentinelFlags(root string) []string {
	return []string{
		"--endpoint", "https://dce.eastus.ingest.monitor.azure.com",
		"--dcr-immutable-id", "dcr-0123456789abcdef0123456789abcdef",
		"--tenant-id", "01234567-89ab-cdef-0123-456789abcdef",
		"--client-id", "abcdef01-2345-6789-abcd-ef0123456789",
		"--log-path", filepath.Join(root, "runtime.jsonl"),
		"--secrets-dir", filepath.Join(root, "secrets"),
		"--allow-remote-metadata-forwarding",
	}
}

func TestEndpointSentinelRequiresExplicitMetadataConsent(t *testing.T) {
	root := t.TempDir()
	args := append([]string{"endpoint", "sentinel", "install-pack"}, sentinelFlags(root)...)
	args = args[:len(args)-1]
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing Sentinel metadata consent was accepted code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestEndpointSentinelPrintConfigUsesExternalClientSecret(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AZURE_CLIENT_SECRET", "never-write-this-secret")
	args := append([]string{"endpoint", "sentinel", "print-config"}, sentinelFlags(root)...)
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, `type = "azure_logs_ingestion"`) ||
		!strings.Contains(config, `azure_client_secret = "SECRET[threadforgesecrets.azureclientsecret]"`) ||
		!strings.Contains(config, `read_from = "end"`) ||
		!strings.Contains(config, "threadforge.metadata-export.v1") ||
		strings.Contains(config, "never-write-this-secret") || strings.Contains(config, "AZURE_CLIENT_SECRET") ||
		strings.Contains(config, "RawData") || strings.Contains(config, "prompt") {
		t.Fatalf("Sentinel print-config violated privacy/config contract code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointSentinelInstallPackWritesLocallyAndRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "sentinel-pack")
	args := append([]string{"endpoint", "sentinel", "install-pack"}, sentinelFlags(root)...)
	args = append(args, "--output", output)
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no network request was made") {
		t.Fatalf("install-pack failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, name := range []string{"README.md", "dcr-logs-ingestion-template.json", "dcr-transform.kql", "table-schema.json", "vector.toml", "queries.kql", "detections.kql", "sample-event.jsonl"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing Sentinel pack file %s: %v", name, err)
		}
	}
	if code := Run(args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "refusing to overwrite") {
		t.Fatalf("existing pack directory was not refused code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestEndpointSentinelAMAPrintsOnlyLocalSanitizerConfig(t *testing.T) {
	root := t.TempDir()
	metadataPath := filepath.Join(root, "sentinel-metadata.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "sentinel", "print-config", "--path", "ama",
		"--log-path", filepath.Join(root, "runtime.jsonl"),
		"--metadata-log-path", metadataPath,
		"--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, `type = "file"`) ||
		!strings.Contains(config, `path = `+strconv.Quote(metadataPath)) ||
		!strings.Contains(config, `read_from = "end"`) ||
		strings.Contains(config, "azure_logs_ingestion") || strings.Contains(config, "SECRET[") {
		t.Fatalf("AMA print-config must emit only local metadata sanitizer code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointSentinelAMAPackAndBothPrintConfigContracts(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "sentinel-ama-pack")
	metadataPath := filepath.Join(root, "sentinel-metadata.jsonl")
	args := []string{
		"endpoint", "sentinel", "install-pack", "--path", "ama",
		"--log-path", filepath.Join(root, "runtime.jsonl"),
		"--metadata-log-path", metadataPath,
		"--allow-remote-metadata-forwarding", "--output", output,
	}
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no network request was made") {
		t.Fatalf("AMA install-pack failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, name := range []string{"README.md", "dcr-template.json", "dcr-ama-transform.kql", "table-schema.json", "vector-local.toml", "queries.kql", "detections.kql", "sample-event.jsonl"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing AMA pack file %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(output, "vector.toml")); !os.IsNotExist(err) {
		t.Fatalf("AMA-only pack must not include direct API config: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"endpoint", "sentinel", "print-config", "--path", "both", "--allow-remote-metadata-forwarding"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "use install-pack") {
		t.Fatalf("both-path print-config should require pack output code=%d stderr=%s", code, stderr.String())
	}
}
