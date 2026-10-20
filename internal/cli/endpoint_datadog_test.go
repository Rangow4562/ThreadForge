package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEndpointDatadogRequiresExplicitMetadataConsent(t *testing.T) {
	output := filepath.Join(t.TempDir(), "datadog-pack")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "datadog", "install-pack", "--endpoint", "https://datadog.example", "--output", output,
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing explicit metadata consent code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("refused command created pack: %v", err)
	}
}

func TestEndpointDatadogPrintConfigIsHTTPSMetadataOnlyAndCredentialFree(t *testing.T) {
	t.Setenv("THREADFORGE_DATADOG_API_KEY", "do-not-embed-api-key")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "datadog", "print-config", "--endpoint", "https://http-intake.logs.example/",
		"--log-path", tracePath, "--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, `type = "datadog_logs"`) ||
		!strings.Contains(config, `read_from = "end"`) || !strings.Contains(config, strconv.Quote(filepath.Clean(tracePath))) ||
		!strings.Contains(config, `endpoint = "https://http-intake.logs.example"`) ||
		!strings.Contains(config, `default_api_key = "SECRET[threadforgesecrets.datadogapikey]"`) ||
		!strings.Contains(config, "threadforge.metadata-export.v1") || strings.Contains(config, "do-not-embed-api-key") ||
		strings.Contains(config, "HOSTNAME") || strings.Contains(config, `"action":`) {
		t.Fatalf("print-config output is invalid, secret-bearing or raw code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointDatadogInstallPackWritesLocallyAndRefusesOverwrite(t *testing.T) {
	t.Setenv("THREADFORGE_DATADOG_API_KEY", "never-write-this")
	output := filepath.Join(t.TempDir(), "datadog-pack")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	args := []string{
		"endpoint", "datadog", "install-pack", "--endpoint", "https://datadog.example",
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
		t.Fatalf("generated config contains API key value: %s", config)
	}
	for _, name := range []string{"datadog-upload-smoke-test.sh", "datadog-upload-smoke-test.ps1"} {
		data, readErr := os.ReadFile(filepath.Join(output, name))
		if readErr != nil {
			t.Fatalf("missing generated Datadog smoke script %s: %v", name, readErr)
		}
		if strings.Contains(string(data), "never-write-this") || !strings.Contains(string(data), "DD_API_KEY") || !strings.Contains(string(data), "/api/v2/logs") {
			t.Fatalf("generated Datadog smoke script %s is missing its endpoint contract or contains a credential: %s", name, data)
		}
	}
	if code := Run(args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "refusing to overwrite") {
		t.Fatalf("existing output directory was not refused code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestEndpointDatadogAgentPathRequiresConsentAndSeparateMetadataFile(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "agent-pack")
	metadataPath := filepath.Join(root, "metadata.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "datadog", "install-pack", "--path", "agent", "--metadata-log-path", metadataPath, "--output", output,
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("Agent pack should require explicit remote consent code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("refused Agent command created pack: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"endpoint", "datadog", "print-config", "--path", "agent", "--log-path", filepath.Join(root, "raw-runtime.jsonl"),
		"--metadata-log-path", metadataPath, "--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, "path: "+strconv.Quote(metadataPath)) ||
		!strings.Contains(config, "service: threadforge") || !strings.Contains(config, "source: threadforge") ||
		strings.Contains(config, filepath.Join(root, "raw-runtime.jsonl")) || strings.Contains(config, "api_key") {
		t.Fatalf("Agent config must use only the separate metadata path and no credential code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointDatadogAgentInstallPackContainsLocalSanitizerAndAgentInput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "agent-pack")
	rawPath := filepath.Join(root, "raw-runtime.jsonl")
	metadataPath := filepath.Join(root, "metadata.jsonl")
	args := []string{
		"endpoint", "datadog", "install-pack", "--path", "agent", "--log-path", rawPath,
		"--metadata-log-path", metadataPath, "--output", output, "--allow-remote-metadata-forwarding",
	}
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Vector and the Agent were not installed or started") {
		t.Fatalf("Agent pack install failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	vectorConfig, err := os.ReadFile(filepath.Join(output, "vector.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(vectorConfig), `read_from = "end"`) || !strings.Contains(string(vectorConfig), "include = ["+strconv.Quote(filepath.Clean(rawPath))+"]") ||
		!strings.Contains(string(vectorConfig), "path = "+strconv.Quote(filepath.Clean(metadataPath))) || strings.Contains(string(vectorConfig), "datadog_logs") {
		t.Fatalf("Agent mode must locally sanitize new raw events to a separate file, not directly forward: %s", vectorConfig)
	}
	agentConfig, err := os.ReadFile(filepath.Join(output, "datadog-agent-conf.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentConfig), strconv.Quote(metadataPath)) || strings.Contains(string(agentConfig), rawPath) || strings.Contains(string(agentConfig), "api_key") {
		t.Fatalf("Agent config must consume only sanitized metadata without keys: %s", agentConfig)
	}
}

func TestEndpointDatadogAgentPathRejectsVectorOnlyOptions(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "datadog", "print-config", "--path", "agent", "--endpoint", "https://datadog.example",
		"--metadata-log-path", filepath.Join(t.TempDir(), "metadata.jsonl"), "--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "does not accept --endpoint") {
		t.Fatalf("Agent path accepted Vector-only endpoint code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
