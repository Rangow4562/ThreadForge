package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndpointRapid7RequiresExplicitMetadataConsent(t *testing.T) {
	output := filepath.Join(t.TempDir(), "rapid7-pack")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"endpoint", "rapid7", "install-pack", "--output", output}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing explicit metadata consent code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("refused command created pack: %v", err)
	}
}

func TestEndpointRapid7PrintConfigUsesExternalBearerEnvironment(t *testing.T) {
	t.Setenv("THREADFORGE_RAPID7_WEBHOOK_URL", "https://rapid7.example/secret-bearer-webhook")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "rapid7", "print-config", "--log-path", tracePath,
		"--allow-remote-metadata-forwarding", "--allow-vector-env-interpolation",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, `type = "http"`) ||
		!strings.Contains(config, `uri = "${THREADFORGE_RAPID7_WEBHOOK_URL}"`) ||
		!strings.Contains(config, `"Content-Type" = "application/x-ndjson"`) ||
		!strings.Contains(config, `read_from = "end"`) ||
		!strings.Contains(config, "newline_delimited") ||
		!strings.Contains(config, "threadforge.metadata-export.v1") ||
		strings.Contains(config, "secret-bearer-webhook") {
		t.Fatalf("print-config leaked a secret or violated the forwarding contract code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointRapid7InstallPackWritesLocallyAndRefusesOverwrite(t *testing.T) {
	t.Setenv("THREADFORGE_RAPID7_WEBHOOK_URL", "https://rapid7.example/never-write-this")
	output := filepath.Join(t.TempDir(), "rapid7-pack")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	args := []string{
		"endpoint", "rapid7", "install-pack", "--log-path", tracePath,
		"--output", output, "--allow-remote-metadata-forwarding", "--allow-vector-env-interpolation",
	}
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no network request was made") {
		t.Fatalf("install-pack failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "vector.toml"} {
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatalf("read pack file %s: %v", name, err)
		}
		if strings.Contains(string(data), "secret-bearer-webhook") || strings.Contains(string(data), "never-write-this") {
			t.Fatalf("generated %s contains an environment secret: %s", name, data)
		}
	}
	if code := Run(args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "refusing to overwrite") {
		t.Fatalf("existing output directory was not refused code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
