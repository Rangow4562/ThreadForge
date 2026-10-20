package forwarding

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestVectorDirectorySecretsAndSinks is an opt-in runtime contract test. Set
// THREADFORGE_VECTOR_BIN to a verified Vector executable to validate generated
// TOML, native SECRET references, sink schemas, and VRL without contacting sinks.
func TestVectorDirectorySecretsAndSinks(t *testing.T) {
	vectorBin := strings.TrimSpace(os.Getenv("THREADFORGE_VECTOR_BIN"))
	if vectorBin == "" {
		t.Skip("set THREADFORGE_VECTOR_BIN to run config validation against an installed Vector release")
	}
	versionContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	versionOutput, err := exec.CommandContext(versionContext, vectorBin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("read Vector version: %v: %s", err, versionOutput)
	}
	t.Logf("validating generated configs with %s", strings.TrimSpace(string(versionOutput)))

	root := t.TempDir()
	vectorDataDir := filepath.Join(root, "vector-data")
	if err := os.Mkdir(vectorDataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "runtime.jsonl")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	secretsDir := filepath.Join(root, "secrets")
	if err := os.Mkdir(secretsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{
		splunkHECTokenSecretName:      "validation-splunk-token",
		falconTokenSecretName:         "validation-falcon-token",
		elasticsearchAPIKeySecretName: "dGVzdDp2YWxpZGF0aW9u",
		datadogAPIKeySecretName:       "validation-datadog-key",
		sumoTokenSecretName:           "validation-sumo-token",
		sentinelClientSecretName:      "validation-azure-client-secret",
	}
	for name, value := range secrets {
		if err := os.WriteFile(filepath.Join(secretsDir, name), []byte(value), 0o600); err != nil {
			t.Fatalf("write temporary validation secret %s: %v", name, err)
		}
	}

	configs := map[string]func() (string, error){
		"splunk-hec": func() (string, error) {
			return RenderSplunkVectorConfig(SplunkForwardingOptions{LogPath: logPath, Endpoint: "https://splunk.invalid:8088", SecretsDir: secretsDir})
		},
		"falcon-hec": func() (string, error) {
			return RenderFalconHECVectorConfig(FalconHECOptions{LogPath: logPath, Endpoint: "https://tenant.ingest.invalid/services/collector", SecretsDir: secretsDir})
		},
		"elasticsearch": func() (string, error) {
			return RenderElasticsearchVectorConfig(ElasticsearchForwardingOptions{LogPath: logPath, Endpoint: "https://elastic.invalid:9200", SecretsDir: secretsDir})
		},
		"elasticsearch-agent-local-metadata": func() (string, error) {
			return RenderElasticsearchAgentVectorConfig(ElasticsearchAgentForwardingOptions{
				LogPath: logPath, MetadataLogPath: filepath.Join(root, "elasticsearch-agent-metadata.jsonl"),
				Endpoint: "https://elastic.invalid:9200",
			})
		},
		"datadog": func() (string, error) {
			return RenderDatadogVectorConfig(DatadogForwardingOptions{LogPath: logPath, Endpoint: "https://http-intake.logs.invalid", SecretsDir: secretsDir})
		},
		"datadog-agent-local-metadata": func() (string, error) {
			return RenderDatadogAgentVectorConfig(DatadogAgentForwardingOptions{
				LogPath: logPath, MetadataLogPath: filepath.Join(root, "datadog-agent-metadata.jsonl"),
			})
		},
		"sumo-auth-header": func() (string, error) {
			return RenderSumoVectorConfig(SumoForwardingOptions{LogPath: logPath, Endpoint: "https://collectors.sumologic.com/receiver/v1/http", SecretsDir: secretsDir})
		},
		"rapid7": func() (string, error) {
			return RenderRapid7VectorConfig(Rapid7ForwardingOptions{LogPath: logPath})
		},
		"wazuh-metadata-file": func() (string, error) {
			return RenderWazuhVectorConfig(WazuhForwardingOptions{LogPath: logPath, MetadataLogPath: filepath.Join(root, "wazuh-metadata.jsonl")})
		},
		"sentinel-logs-ingestion": func() (string, error) {
			return RenderSentinelVectorConfig(SentinelForwardingOptions{
				LogPath:        logPath,
				Endpoint:       "https://dce.eastus.ingest.monitor.azure.com",
				DCRImmutableID: "dcr-0123456789abcdef0123456789abcdef",
				TenantID:       "01234567-89ab-cdef-0123-456789abcdef",
				ClientID:       "abcdef01-2345-6789-abcd-ef0123456789",
				SecretsDir:     secretsDir,
			})
		},
		"sentinel-ama-local-metadata": func() (string, error) {
			return RenderSentinelLocalVectorConfig(SentinelForwardingOptions{
				Path:            sentinelPathAMA,
				LogPath:         logPath,
				MetadataLogPath: filepath.Join(root, "sentinel-metadata.jsonl"),
			})
		},
	}
	for name, render := range configs {
		t.Run(name, func(t *testing.T) {
			config, err := render()
			if err != nil {
				t.Fatal(err)
			}
			config = "data_dir = " + strconv.Quote(vectorDataDir) + "\n" + config
			configPath := filepath.Join(root, name+".toml")
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			args := []string{"validate", "--no-environment"}
			if name == "rapid7" {
				args = append(args, "--dangerously-allow-env-var-interpolation")
			}
			args = append(args, "--config-toml", configPath)
			command := exec.CommandContext(ctx, vectorBin, args...)
			command.Env = append(os.Environ(), "VECTOR_LOG=trace", rapid7WebhookEnvironmentName+"=https://example.com/rapid7-test-webhook")
			output, err := command.CombinedOutput()
			if err != nil {
				var secretDiagnostics []string
				for _, line := range strings.Split(string(output), "\n") {
					lower := strings.ToLower(line)
					if strings.Contains(lower, "secret") || strings.Contains(lower, "international domain") || strings.Contains(lower, "failed to validate sink") {
						secretDiagnostics = append(secretDiagnostics, line)
					}
				}
				t.Fatalf("Vector rejected generated config: %v\n%s\nConfig:\n%s", err, strings.Join(secretDiagnostics, "\n"), config)
			}
			if len(output) > 0 {
				t.Log(fmt.Sprintf("Vector validation output: %s", strings.TrimSpace(string(output))))
			}
		})
	}
}
