package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndpointSumoRequiresExplicitMetadataConsent(t *testing.T) {
	output := filepath.Join(t.TempDir(), "sumo-pack")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"endpoint", "sumo", "install-pack", "--output", output}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing explicit metadata consent code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("refused command created pack: %v", err)
	}
}

func TestEndpointSumoPrintConfigUsesStaticAuthHeaderURLAndExternalToken(t *testing.T) {
	t.Setenv("THREADFORGE_SUMO_URL", "https://collectors.sumologic.example/secret-source-url")
	t.Setenv("THREADFORGE_SUMO_TOKEN", "secret-sumo-token")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	endpoint := "https://collectors.sumologic.com/receiver/v1/http"
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "sumo", "print-config", "--endpoint", endpoint, "--auth-header", "--log-path", tracePath,
		"--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, `type = "http"`) ||
		!strings.Contains(config, `uri = "`+endpoint+`"`) ||
		!strings.Contains(config, `"x-sumo-token" = "SECRET[threadforgesecrets.sumotoken]"`) ||
		!strings.Contains(config, `read_from = "end"`) ||
		!strings.Contains(config, "newline_delimited") ||
		!strings.Contains(config, "threadforge.metadata-export.v1") ||
		strings.Contains(config, "collectors.sumologic.example") || strings.Contains(config, "secret-sumo-token") {
		t.Fatalf("print-config leaked a secret or violated the forwarding contract code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointSumoRequiresAuthHeaderMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "sumo", "print-config", "--endpoint", "https://collectors.sumologic.com/receiver/v1/http",
		"--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--auth-header") {
		t.Fatalf("Sumo without Auth Header confirmation was accepted code=%d stderr=%s", code, stderr.String())
	}
}

func TestEndpointSumoInstallPackWritesLocallyAndRefusesOverwrite(t *testing.T) {
	t.Setenv("THREADFORGE_SUMO_URL", "https://collectors.sumologic.example/secret-source-url")
	t.Setenv("THREADFORGE_SUMO_TOKEN", "never-write-this")
	output := filepath.Join(t.TempDir(), "sumo-pack")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	args := []string{
		"endpoint", "sumo", "install-pack", "--endpoint", "https://collectors.sumologic.com/receiver/v1/http", "--auth-header", "--log-path", tracePath,
		"--output", output, "--allow-remote-metadata-forwarding",
	}
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no network request was made") {
		t.Fatalf("install-pack failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "sumo-upload-smoke-test.sh", "sumo-upload-smoke-test.ps1", "vector.toml"} {
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatalf("read pack file %s: %v", name, err)
		}
		if strings.Contains(string(data), "collectors.sumologic.example") || strings.Contains(string(data), "never-write-this") {
			t.Fatalf("generated %s contains an environment secret: %s", name, data)
		}
		if name == "sumo-upload-smoke-test.sh" &&
			(!strings.Contains(string(data), "--send-synthetic-event") || strings.Contains(string(data), "BEACON_LOG") || strings.Contains(string(data), "curl -T")) {
			t.Fatalf("generated smoke script lacks explicit synthetic-only behavior: %s", data)
		}
		if name == "sumo-upload-smoke-test.ps1" &&
			(!strings.Contains(string(data), "-SendSyntheticEvent") || strings.Contains(string(data), "BEACON_LOG") || strings.Contains(string(data), "Invoke-WebRequest")) {
			t.Fatalf("generated PowerShell smoke script lacks explicit synthetic-only behavior: %s", data)
		}
	}
	if code := Run(args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "refusing to overwrite") {
		t.Fatalf("existing output directory was not refused code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
