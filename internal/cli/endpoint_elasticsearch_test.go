package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEndpointElasticsearchRequiresExplicitMetadataConsent(t *testing.T) {
	output := filepath.Join(t.TempDir(), "elasticsearch-pack")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "elasticsearch", "install-pack", "--endpoint", "https://elastic.example:9200", "--output", output,
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--allow-remote-metadata-forwarding") {
		t.Fatalf("missing explicit metadata consent code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("refused command created pack: %v", err)
	}
}

func TestEndpointElasticsearchPrintConfigIsHTTPSMetadataOnlyAndCredentialFree(t *testing.T) {
	t.Setenv("THREADFORGE_ELASTICSEARCH_API_KEY", "do-not-embed-api-key")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"endpoint", "elasticsearch", "print-config", "--endpoint", "https://elastic.example:9200/",
		"--index", "threadforge-events", "--log-path", tracePath, "--allow-remote-metadata-forwarding",
	}, &stdout, &stderr)
	config := stdout.String()
	if code != 0 || !strings.Contains(config, `type = "elasticsearch"`) ||
		!strings.Contains(config, `read_from = "end"`) || !strings.Contains(config, strconv.Quote(filepath.Clean(tracePath))) ||
		!strings.Contains(config, `endpoints = ["https://elastic.example:9200"]`) ||
		!strings.Contains(config, `Authorization = "ApiKey SECRET[threadforgesecrets.elasticsearchapikey]"`) ||
		!strings.Contains(config, "threadforge.metadata-export.v1") || strings.Contains(config, "do-not-embed-api-key") ||
		strings.Contains(config, "HOSTNAME") || strings.Contains(config, `"action":`) {
		t.Fatalf("print-config output is invalid, secret-bearing or raw code=%d stdout=%s stderr=%s", code, config, stderr.String())
	}
}

func TestEndpointElasticsearchInstallPackWritesLocallyAndPreservesExistingDirectory(t *testing.T) {
	t.Setenv("THREADFORGE_ELASTICSEARCH_API_KEY", "never-write-this")
	t.Setenv("ELASTIC_API_KEY", "must-not-embed-api-key")
	t.Setenv("ELASTIC_USERNAME", "must-not-embed-user")
	t.Setenv("ELASTIC_PASSWORD", "must-not-embed-password")
	output := filepath.Join(t.TempDir(), "elasticsearch-pack")
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	args := []string{
		"endpoint", "elasticsearch", "install-pack", "--endpoint", "https://elastic.example:9200",
		"--log-path", tracePath, "--output", output, "--allow-remote-metadata-forwarding",
	}
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no network request was made") {
		t.Fatalf("install-pack failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, name := range []string{"vector.toml", "elasticsearch-upload-smoke-test.sh", "elasticsearch-upload-smoke-test.ps1"} {
		data, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"never-write-this", "must-not-embed-api-key", "must-not-embed-user", "must-not-embed-password"} {
			if strings.Contains(string(data), secret) {
				t.Fatalf("generated %s contains secret environment data %q", name, secret)
			}
		}
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

func TestEndpointElasticsearchAgentPathsRequireConsentAndSeparateMetadata(t *testing.T) {
	root := t.TempDir()
	rawPath := filepath.Join(root, "runtime.jsonl")
	metadataPath := filepath.Join(root, "metadata.jsonl")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "consent",
			args: []string{"endpoint", "elasticsearch", "print-config", "--path", "filebeat", "--endpoint", "https://elastic.example:9200", "--metadata-log-path", metadataPath},
			want: "--allow-remote-metadata-forwarding",
		},
		{
			name: "metadata path",
			args: []string{"endpoint", "elasticsearch", "print-config", "--path", "elastic-agent", "--endpoint", "https://elastic.example:9200", "--log-path", rawPath, "--allow-remote-metadata-forwarding"},
			want: "requires --metadata-log-path",
		},
		{
			name: "absolute metadata path",
			args: []string{"endpoint", "elasticsearch", "print-config", "--path", "filebeat", "--endpoint", "https://elastic.example:9200", "--metadata-log-path", "relative-metadata.jsonl", "--allow-remote-metadata-forwarding"},
			want: "must be absolute",
		},
		{
			name: "vector-only secrets option",
			args: []string{"endpoint", "elasticsearch", "print-config", "--path", "filebeat", "--endpoint", "https://elastic.example:9200", "--metadata-log-path", metadataPath, "--secrets-dir", filepath.Join(root, "secrets"), "--allow-remote-metadata-forwarding"},
			want: "does not accept --secrets-dir",
		},
		{
			name: "vector-only index override",
			args: []string{"endpoint", "elasticsearch", "print-config", "--path", "filebeat", "--endpoint", "https://elastic.example:9200", "--metadata-log-path", metadataPath, "--index", "custom-index", "--allow-remote-metadata-forwarding"},
			want: "--index applies only to --path vector",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(test.args, &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("unsafe/missing option accepted code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestEndpointElasticsearchPrintsCredentialFreeNativeConfigs(t *testing.T) {
	t.Setenv("ELASTICSEARCH_API_KEY", "must-not-appear")
	root := t.TempDir()
	rawPath := filepath.Join(root, "raw-runtime.jsonl")
	metadataPath := filepath.Join(root, "metadata-only.jsonl")
	for _, path := range []string{"filebeat", "elastic-agent"} {
		t.Run(path, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			streamMarker := "logs-threadforge.endpoint-default"
			if path == "elastic-agent" {
				streamMarker = "dataset: threadforge.endpoint"
			}
			code := Run([]string{
				"endpoint", "elasticsearch", "print-config", "--path", path,
				"--endpoint", "https://elastic.example:9200",
				"--log-path", rawPath, "--metadata-log-path", metadataPath,
				"--allow-remote-metadata-forwarding",
			}, &stdout, &stderr)
			config := stdout.String()
			if code != 0 || !strings.Contains(config, strconv.Quote(metadataPath)) || strings.Contains(config, strconv.Quote(rawPath)) ||
				strings.Contains(config, "must-not-appear") || strings.Contains(config, "username:") || strings.Contains(config, "password:") ||
				!strings.Contains(config, "https://elastic.example:9200") || !strings.Contains(config, "ssl.verification_mode: full") ||
				!strings.Contains(config, streamMarker) {
				t.Fatalf("native config is invalid, points at raw traces, or contains credentials code=%d stdout=%s stderr=%s", code, config, stderr.String())
			}
		})
	}
}

func TestEndpointElasticsearchInstallsNativePacksLocally(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"filebeat", "elastic-agent"} {
		t.Run(path, func(t *testing.T) {
			output := filepath.Join(root, path+"-pack")
			var stdout, stderr bytes.Buffer
			code := Run([]string{
				"endpoint", "elasticsearch", "install-pack", "--path", path,
				"--endpoint", "https://elastic.example:9200", "--log-path", filepath.Join(root, "runtime.jsonl"),
				"--metadata-log-path", filepath.Join(root, path+"-metadata.jsonl"), "--output", output,
				"--allow-remote-metadata-forwarding",
			}, &stdout, &stderr)
			if code != 0 || !strings.Contains(stdout.String(), "no network request was made") {
				t.Fatalf("local install failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			shipperFile := "filebeat.yml"
			if path == "elastic-agent" {
				shipperFile = "elastic-agent-standalone.yml"
			}
			for _, name := range []string{"README.md", "sample-event.jsonl", "elasticsearch-upload-smoke-test.sh", "elasticsearch-upload-smoke-test.ps1", "vector.toml", shipperFile} {
				if _, err := os.Stat(filepath.Join(output, name)); err != nil {
					t.Fatalf("missing installed pack asset %s: %v", name, err)
				}
			}
		})
	}
}

func TestEndpointElasticsearchStackUpRequiresImagePullAcknowledgement(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"endpoint", "elasticsearch", "up", "--pack-dir", filepath.Join(t.TempDir(), "missing-pack")}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "requires --allow-image-pulls") {
		t.Fatalf("up must require explicit registry-pull acknowledgement code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestElasticsearchLocalStackProjectIsStableAndScopedToPack(t *testing.T) {
	root := t.TempDir()
	firstPack := filepath.Join(root, "first-pack")
	secondPack := filepath.Join(root, "second-pack")
	firstProject := elasticsearchLocalStackProjectForPackDir(firstPack)
	if firstProject != elasticsearchLocalStackProjectForPackDir(firstPack) {
		t.Fatalf("project name changed for the same pack path: %q", firstProject)
	}
	secondProject := elasticsearchLocalStackProjectForPackDir(secondPack)
	if firstProject == secondProject {
		t.Fatalf("different pack directories must own isolated Compose projects: %q", firstProject)
	}
	if firstProject == elasticsearchLocalStackProject || secondProject == elasticsearchLocalStackProject {
		t.Fatal("custom pack directories must not share the legacy default project")
	}
	if len(firstProject) > 63 || firstProject[:len("threadforge-elasticsearch-")] != "threadforge-elasticsearch-" {
		t.Fatalf("unexpected Compose project name %q", firstProject)
	}
}

func TestElasticsearchLocalStackKeepsLegacyProjectForDefaultPackPath(t *testing.T) {
	defaultPath, err := filepath.Abs("threadforge-elasticsearch-pack")
	if err != nil {
		t.Fatal(err)
	}
	if got := elasticsearchLocalStackProjectForPackDir(defaultPath); got != elasticsearchLocalStackProject {
		t.Fatalf("default pack path should keep legacy project name %q, got %q", elasticsearchLocalStackProject, got)
	}
}

func TestEndpointElasticsearchStackRejectsModifiedComposeBeforeDocker(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, "pack")
	var stdout, stderr bytes.Buffer
	install := []string{
		"endpoint", "elasticsearch", "install-pack", "--endpoint", "https://elastic.example:9200",
		"--output", packDir, "--allow-remote-metadata-forwarding",
	}
	if code := Run(install, &stdout, &stderr); code != 0 {
		t.Fatalf("install test pack code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	composePath := filepath.Join(packDir, "docker-compose.yml")
	compose, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(composePath, append(compose, []byte("# changed by test\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code := Run([]string{
		"endpoint", "elasticsearch", "up", "--pack-dir", packDir,
		"--docker-bin", filepath.Join(root, "docker-must-not-run.exe"), "--allow-image-pulls",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "was modified") {
		t.Fatalf("modified Compose file should fail before Docker lookup/invocation code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestElasticsearchLocalStackRejectsRemoteDockerEndpoints(t *testing.T) {
	for _, test := range []struct {
		host  string
		local bool
	}{
		{host: "npipe:////./pipe/docker_engine", local: true},
		{host: "unix:///var/run/docker.sock", local: true},
		{host: "tcp://127.0.0.1:2375", local: false},
		{host: "ssh://builder.example", local: false},
	} {
		if got := isLocalDockerEndpoint(test.host); got != test.local {
			t.Errorf("isLocalDockerEndpoint(%q) = %t, want %t", test.host, got, test.local)
		}
	}
}
