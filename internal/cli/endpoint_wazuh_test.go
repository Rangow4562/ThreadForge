package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
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

func TestEndpointWazuhValidationRequiresConsentAndAppendsContentFreeLocalEvent(t *testing.T) {
	root := t.TempDir()
	tracePath := filepath.Join(root, "events.jsonl")
	if err := os.WriteFile(tracePath, []byte("{\"existing\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"endpoint", "wazuh", "validate", "--log-path", tracePath}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("Wazuh validation ran without downstream-consent acknowledgement code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	before, err := os.ReadFile(tracePath)
	if err != nil || string(before) != "{\"existing\":true}\n" {
		t.Fatalf("refused validation changed the local trace: data=%q err=%v", before, err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"endpoint", "wazuh", "validate", "--log-path", tracePath,
		"--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "no network request was made") || !strings.Contains(stdout.String(), "119510") {
		t.Fatalf("Wazuh validation failed or omitted its downstream warning code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(rows) != 2 || rows[0] != "{\"existing\":true}" {
		t.Fatalf("Wazuh validation did not append exactly one event while preserving prior content: %s", data)
	}
	var event beacon.Event
	if err := json.Unmarshal([]byte(rows[1]), &event); err != nil {
		t.Fatalf("validation event is not JSON: %v", err)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("validation event violates the canonical trace contract: %v", err)
	}
	if event.Event.Action != "agent.detected" || event.Event.Category != "validation" ||
		event.Severity != beacon.SeverityInfo || event.Harness.Name != "threadforge" ||
		event.Endpoint.OS != runtime.GOOS || event.Message != "Threadforge Wazuh validation event" ||
		event.Endpoint.Hostname != "" || event.User != (beacon.UserInfo{}) || event.Prompt != nil || event.Raw != nil {
		t.Fatalf("validation event contains unexpected or identity-bearing data: %#v", event)
	}
}

func TestEndpointWazuhValidationRejectsSymlinkedTrace(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.jsonl")
	link := filepath.Join(root, "events.jsonl")
	if err := os.WriteFile(target, []byte("leave unchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "wazuh", "validate", "--log-path", link,
		"--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "regular, non-symlink") {
		t.Fatalf("Wazuh validation accepted a linked trace code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "leave unchanged\n" {
		t.Fatalf("refused validation modified the symlink target: data=%q err=%v", data, err)
	}
}
