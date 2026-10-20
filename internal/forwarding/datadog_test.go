package forwarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func TestRenderDatadogVectorConfigIsHTTPSAndMetadataOnly(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private events.jsonl")
	config, err := RenderDatadogVectorConfig(DatadogForwardingOptions{
		LogPath: logPath, Endpoint: "https://http-intake.logs.example/", SecretsDir: filepath.Join(t.TempDir(), "secret-values"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Datadog config is not valid TOML: %v\n%s", err, config)
	}
	source := document["sources"].(map[string]any)["threadforge_runtime"].(map[string]any)
	include := source["include"].([]any)
	if source["read_from"] != "end" || len(include) != 1 || include[0] != logPath {
		t.Fatalf("source must tail selected local log from EOF: %#v", source)
	}
	sink := document["sinks"].(map[string]any)["threadforge_datadog"].(map[string]any)
	if sink["type"] != "datadog_logs" || sink["endpoint"] != "https://http-intake.logs.example" ||
		sink["default_api_key"] != "SECRET[threadforgesecrets.datadogapikey]" {
		t.Fatalf("unexpected Datadog sink config: %#v", sink)
	}
	transform := document["transforms"].(map[string]any)["threadforge_runtime_metadata"].(map[string]any)
	assertMetadataOnlyProjection(t, transform["source"].(string))
	for _, forbidden := range []string{"real-api-key", "Authorization", "HOSTNAME", "inventory", `"action":`} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("Datadog config contains secret material, host identity, or non-allowlisted content %q", forbidden)
		}
	}
}

func TestRenderSyntheticDatadogSampleIsMetadataOnly(t *testing.T) {
	data, err := RenderSyntheticDatadogSample()
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytesTrimNewline(data), &event); err != nil {
		t.Fatalf("sample is invalid JSON: %v", err)
	}
	if len(event) != 3 || event["severity"] != "info" {
		t.Fatalf("sample contains fields outside the metadata export: %#v", event)
	}
	if _, err := time.Parse(time.RFC3339Nano, event["timestamp"].(string)); err != nil {
		t.Fatalf("sample timestamp is invalid: %v", err)
	}
	marker := event["threadforge_export"].(map[string]any)
	if len(marker) != 2 || marker["schema_version"] != "threadforge.metadata-export.v1" || marker["raw_content"] != false {
		t.Fatalf("sample export marker is invalid: %#v", marker)
	}
}

func TestDatadogSmokeScriptsAreExplicitSyntheticOnlyAndSampleBound(t *testing.T) {
	options := DatadogForwardingOptions{
		LogPath: filepath.Join(t.TempDir(), "private-runtime.jsonl"), Endpoint: "https://http-intake.logs.example",
		SecretsDir: filepath.Join(t.TempDir(), "vector-secrets"),
	}
	sample, err := RenderSyntheticDatadogSample()
	if err != nil {
		t.Fatal(err)
	}
	script, powershell := renderDatadogSmokeTestScripts(options.Endpoint, sample)
	digest := sha256.Sum256(sample)
	for _, want := range []string{
		"--send-synthetic-event", "${DD_API_KEY:-}", "unset DD_API_KEY", "curl --config -",
		"DD-API-KEY", "Content-Type: application/json", "/api/v2/logs", "--data-binary \"@$sample_file\"",
		"--output /dev/null", "status\" = 202", hex.EncodeToString(digest[:]),
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("POSIX smoke script omits safety contract %q:\n%s", want, script)
		}
	}
	for _, forbidden := range []string{options.LogPath, options.SecretsDir, "THREADFORGE_DATADOG_API_KEY", "--location", "--verbose", "$DD_API_KEY\" --"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("POSIX smoke script contains forbidden runtime/secret handling %q:\n%s", forbidden, script)
		}
	}
	for _, want := range []string{
		"-SendSyntheticEvent", "DD_API_KEY", "DD-API-KEY", "AllowAutoRedirect = $false", "ResponseHeadersRead",
		"ReadAllBytes($samplePath)", "-ne 202", "/api/v2/logs", hex.EncodeToString(digest[:]),
	} {
		if !strings.Contains(powershell, want) {
			t.Fatalf("PowerShell smoke script omits safety contract %q:\n%s", want, powershell)
		}
	}
	for _, forbidden := range []string{options.LogPath, options.SecretsDir, "THREADFORGE_DATADOG_API_KEY", "Invoke-WebRequest", "-MaximumRedirection"} {
		if strings.Contains(powershell, forbidden) {
			t.Fatalf("PowerShell smoke script contains forbidden runtime/redirect handling %q:\n%s", forbidden, powershell)
		}
	}
}

func TestDatadogPowerShellSmokePostsOnlyPinnedSampleToLocalFixture(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell Core is unavailable")
	}
	type requestCapture struct {
		method      string
		path        string
		apiKey      string
		contentType string
		body        []byte
	}
	captures := make(chan requestCapture, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 4097))
		if readErr != nil {
			t.Errorf("read fixture request: %v", readErr)
		}
		captures <- requestCapture{method: r.Method, path: r.URL.Path, apiKey: r.Header.Get("DD-API-KEY"), contentType: r.Header.Get("Content-Type"), body: body}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "secret-response-body-must-not-be-displayed")
	}))
	defer server.Close()

	sample, err := RenderSyntheticDatadogSample()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample-event.jsonl"), sample, 0o600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "datadog-upload-smoke-test.ps1")
	_, script := renderDatadogSmokeTestScripts(server.URL, sample)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := `unit-api-key-"\\safe`
	noOptIn := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", scriptPath)
	noOptIn.Env = append(os.Environ(), "DD_API_KEY="+secret)
	noOptInOutput, noOptInErr := noOptIn.CombinedOutput()
	var noOptInExit *exec.ExitError
	if !errors.As(noOptInErr, &noOptInExit) || noOptInExit.ExitCode() != 2 || !strings.Contains(string(noOptInOutput), "No request was sent") {
		t.Fatalf("PowerShell script did not fail closed without explicit send switch: err=%v output=%s", noOptInErr, noOptInOutput)
	}
	select {
	case unexpected := <-captures:
		t.Fatalf("PowerShell script sent a request without explicit send switch: %#v", unexpected)
	default:
	}
	cmd := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", scriptPath, "-SendSyntheticEvent")
	cmd.Env = append(os.Environ(), "DD_API_KEY="+secret)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell smoke script failed against local fixture: %v output=%s", err, output)
	}
	if !strings.Contains(string(output), "HTTP 202") || strings.Contains(string(output), "secret-response-body-must-not-be-displayed") || strings.Contains(string(output), secret) {
		t.Fatalf("PowerShell smoke script exposed data or did not report HTTP 202: %s", output)
	}
	select {
	case captured := <-captures:
		if captured.method != http.MethodPost || captured.path != "/api/v2/logs" || captured.apiKey != secret || captured.contentType != "application/json" || string(captured.body) != string(sample) {
			t.Fatalf("unexpected fixture request: method=%q path=%q api-key=%q content-type=%q body=%s", captured.method, captured.path, captured.apiKey, captured.contentType, captured.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PowerShell smoke script did not send the synthetic event to the local fixture")
	}
	select {
	case duplicate := <-captures:
		t.Fatalf("PowerShell smoke script sent more than one request: %#v", duplicate)
	default:
	}
}

func TestDatadogPowerShellSmokeRejectsNon202WithoutExposingResponseBody(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell Core is unavailable")
	}
	type requestCapture struct {
		method string
		path   string
		apiKey string
		body   []byte
	}
	captures := make(chan requestCapture, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 4097))
		if readErr != nil {
			t.Errorf("read fixture request: %v", readErr)
		}
		captures <- requestCapture{method: r.Method, path: r.URL.Path, apiKey: r.Header.Get("DD-API-KEY"), body: body}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "secret-response-body-must-not-be-displayed")
	}))
	defer server.Close()

	sample, err := RenderSyntheticDatadogSample()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample-event.jsonl"), sample, 0o600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "datadog-upload-smoke-test.ps1")
	_, script := renderDatadogSmokeTestScripts(server.URL, sample)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := "fixture-api-key"
	cmd := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", scriptPath, "-SendSyntheticEvent")
	cmd.Env = append(os.Environ(), "DD_API_KEY="+secret)
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("PowerShell smoke script must reject HTTP 200: err=%v output=%s", err, output)
	}
	if strings.Contains(string(output), "secret-response-body-must-not-be-displayed") || strings.Contains(string(output), secret) {
		t.Fatalf("failed Datadog response exposed credentials or response body: %s", output)
	}
	select {
	case captured := <-captures:
		if captured.method != http.MethodPost || captured.path != "/api/v2/logs" || captured.apiKey != secret || string(captured.body) != string(sample) {
			t.Fatalf("unexpected fixture request: method=%q path=%q api-key=%q body=%s", captured.method, captured.path, captured.apiKey, captured.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PowerShell smoke script did not contact the local fixture")
	}
}

func TestDatadogSmokeScriptPostsPinnedSampleThroughCurlConfigStdin(t *testing.T) {
	shell := sumoSmokeShell(t)
	root := t.TempDir()
	packDir := filepath.Join(root, "datadog pack")
	options := DatadogForwardingOptions{
		LogPath: filepath.Join(root, "private runtime.jsonl"), Endpoint: "https://http-intake.logs.example",
		SecretsDir: filepath.Join(root, "vector secrets"),
	}
	if _, err := InstallDatadogForwardingPack(packDir, options); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(root, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(root, "curl-args.txt")
	configPath := filepath.Join(root, "curl-config.txt")
	payloadPath := filepath.Join(root, "curl-payload.jsonl")
	fakeCurl := `#!/bin/sh
if [ "${DD_API_KEY+x}" = x ]; then exit 91; fi
printf '%s\n' "$@" > "$THREADFORGE_TEST_CURL_ARGS"
cat > "$THREADFORGE_TEST_CURL_CONFIG"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--data-binary" ]; then
    payload=${2#@}
    cat "$payload" > "$THREADFORGE_TEST_CURL_PAYLOAD"
    break
  fi
  shift
done
printf '202'
`
	fakeCurlPath := filepath.Join(fakeBin, "curl")
	if err := os.WriteFile(fakeCurlPath, []byte(fakeCurl), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, filepath.Join(packDir, "datadog-upload-smoke-test.sh"), "--send-synthetic-event")
	secret := `unit-api-key-"\\safe`
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DD_API_KEY="+secret,
		"THREADFORGE_TEST_CURL_ARGS="+argsPath,
		"THREADFORGE_TEST_CURL_CONFIG="+configPath,
		"THREADFORGE_TEST_CURL_PAYLOAD="+payloadPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke script failed with mocked curl: %v output=%s", err, output)
	}
	if !strings.Contains(string(output), "HTTP 202") {
		t.Fatalf("smoke script did not report mocked success status: %s", output)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("mock curl did not capture arguments: %v", err)
	}
	if strings.Contains(string(args), secret) || !strings.Contains(string(args), "sample-event.jsonl") || strings.Contains(string(args), options.LogPath) {
		t.Fatalf("curl argv included an API key or runtime path, or missed the synthetic sample: %s", args)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("mock curl did not receive configuration on stdin: %v", err)
	}
	if !strings.Contains(string(config), `url = "`+options.Endpoint+`/api/v2/logs"`) ||
		!strings.Contains(string(config), `header = "DD-API-KEY: unit-api-key-\"\\\\safe"`) {
		t.Fatalf("curl config stdin did not contain expected endpoint and escaped API key: %s", config)
	}
	sample, err := os.ReadFile(filepath.Join(packDir, "sample-event.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := os.ReadFile(payloadPath)
	if err != nil || string(uploaded) != string(sample) {
		t.Fatalf("curl did not receive the exact generated synthetic sample: err=%v payload=%s sample=%s", err, uploaded, sample)
	}
}

func TestInstallDatadogPackRefusesOverwriteAndWritesPrivateFiles(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "datadog-pack")
	options := DatadogForwardingOptions{LogPath: filepath.Join(parent, "runtime.jsonl"), Endpoint: "https://datadog.example", SecretsDir: filepath.Join(parent, "secret-values")}
	installed, err := InstallDatadogForwardingPack(output, options)
	if err != nil {
		t.Fatal(err)
	}
	if installed != output {
		t.Fatalf("installed path = %q, want %q", installed, output)
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "datadog-upload-smoke-test.sh", "datadog-upload-smoke-test.ps1", "vector.toml"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing pack asset %s: %v", name, err)
		}
	}
	readme, err := os.ReadFile(filepath.Join(output, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"does not install or start Vector", "does not backfill", filepath.Join(options.SecretsDir, datadogAPIKeySecretName), "metadata-only", "DD_API_KEY", "HTTP 202 Accepted", "response bodies"} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("README omits privacy or setup requirement %q", phrase)
		}
	}
	if _, err := InstallDatadogForwardingPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing output directory should be refused, got %v", err)
	}
}

func TestDatadogAgentPackTailsOnlyNewSanitizedMetadata(t *testing.T) {
	root := t.TempDir()
	options := DatadogAgentForwardingOptions{
		LogPath:         filepath.Join(root, "private-runtime.jsonl"),
		MetadataLogPath: filepath.Join(root, "datadog-metadata.jsonl"),
	}
	agentConfig, err := RenderDatadogAgentConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	var agent struct {
		Logs []struct {
			Type    string   `yaml:"type"`
			Path    string   `yaml:"path"`
			Service string   `yaml:"service"`
			Source  string   `yaml:"source"`
			Tags    []string `yaml:"tags"`
		} `yaml:"logs"`
	}
	if err := yaml.Unmarshal([]byte(agentConfig), &agent); err != nil {
		t.Fatalf("generated Datadog Agent config is not valid YAML: %v\n%s", err, agentConfig)
	}
	if len(agent.Logs) != 1 || agent.Logs[0].Type != "file" || agent.Logs[0].Path != options.MetadataLogPath ||
		agent.Logs[0].Service != "threadforge" || agent.Logs[0].Source != "threadforge" ||
		!slices.Contains(agent.Logs[0].Tags, "threadforge_export:metadata-only") {
		t.Fatalf("Agent config is not bound to the sanitized metadata path: %#v", agent)
	}
	if strings.Contains(agentConfig, options.LogPath) || strings.Contains(agentConfig, "api_key") || strings.Contains(agentConfig, "datadog.example") {
		t.Fatalf("Agent config includes raw trace path or credentials: %s", agentConfig)
	}

	vectorConfig, err := RenderDatadogAgentVectorConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	var vector map[string]any
	if err := toml.Unmarshal([]byte(vectorConfig), &vector); err != nil {
		t.Fatalf("generated local sanitizer config is not valid TOML: %v\n%s", err, vectorConfig)
	}
	source := vector["sources"].(map[string]any)["threadforge_runtime"].(map[string]any)
	if source["read_from"] != "end" || source["include"].([]any)[0] != options.LogPath {
		t.Fatalf("raw source must start at EOF and remain local: %#v", source)
	}
	sink := vector["sinks"].(map[string]any)["threadforge_datadog_metadata"].(map[string]any)
	if sink["type"] != "file" || sink["path"] != options.MetadataLogPath {
		t.Fatalf("local transform must write to the separate metadata file: %#v", sink)
	}
	transform := vector["transforms"].(map[string]any)["threadforge_runtime_metadata"].(map[string]any)
	assertMetadataOnlyProjection(t, transform["source"].(string))
	if strings.Contains(vectorConfig, "datadog_logs") || strings.Contains(vectorConfig, "api_key") {
		t.Fatalf("local sanitizer config must not directly forward or contain credentials: %s", vectorConfig)
	}

	output := filepath.Join(root, "agent-pack")
	installed, err := InstallDatadogAgentForwardingPack(output, options)
	if err != nil {
		t.Fatal(err)
	}
	if installed != output {
		t.Fatalf("installed pack path = %q, want %q", installed, output)
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "datadog-agent-conf.yaml", "vector.toml"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing Datadog Agent pack asset %s: %v", name, err)
		}
	}
	readme, err := os.ReadFile(filepath.Join(output, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"metadata-only", "does not install or start", "host metadata", options.MetadataLogPath} {
		if !strings.Contains(string(readme), want) {
			t.Fatalf("Agent pack README omits %q", want)
		}
	}
}

func TestDatadogAgentPackRejectsRawOrUnsafeMetadataTargets(t *testing.T) {
	root := t.TempDir()
	valid := DatadogAgentForwardingOptions{
		LogPath:         filepath.Join(root, "runtime.jsonl"),
		MetadataLogPath: filepath.Join(root, "metadata.jsonl"),
	}
	cases := []struct {
		name   string
		change func(*DatadogAgentForwardingOptions)
	}{
		{name: "relative metadata path", change: func(value *DatadogAgentForwardingOptions) { value.MetadataLogPath = "metadata.jsonl" }},
		{name: "raw path reused", change: func(value *DatadogAgentForwardingOptions) { value.MetadataLogPath = value.LogPath }},
		{name: "tab in metadata path", change: func(value *DatadogAgentForwardingOptions) {
			value.MetadataLogPath = filepath.Join(root, "bad\tname.jsonl")
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			options := valid
			tt.change(&options)
			if _, err := RenderDatadogAgentConfig(options); err == nil {
				t.Fatal("unsafe Datadog Agent metadata path was accepted")
			}
		})
	}

	preexisting := filepath.Join(root, "preexisting.jsonl")
	if err := os.WriteFile(preexisting, []byte(`{"prompt":"must not be tailed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	options := valid
	options.MetadataLogPath = preexisting
	if _, err := RenderDatadogAgentConfig(options); err == nil || !strings.Contains(err.Error(), "pre-existing file content") {
		t.Fatalf("nonempty existing output was accepted: %v", err)
	}
}

func TestRenderDatadogRejectsUnsafeInputs(t *testing.T) {
	valid := DatadogForwardingOptions{LogPath: filepath.Join(t.TempDir(), "events.jsonl"), Endpoint: "https://datadog.example"}
	cases := []struct {
		name   string
		change func(*DatadogForwardingOptions)
	}{
		{name: "relative log path", change: func(value *DatadogForwardingOptions) { value.LogPath = "events.jsonl" }},
		{name: "http endpoint", change: func(value *DatadogForwardingOptions) { value.Endpoint = "http://datadog.example" }},
		{name: "endpoint userinfo", change: func(value *DatadogForwardingOptions) { value.Endpoint = "https://key@datadog.example" }},
		{name: "endpoint path", change: func(value *DatadogForwardingOptions) { value.Endpoint = "https://datadog.example/api/v2/logs" }},
		{name: "endpoint query", change: func(value *DatadogForwardingOptions) { value.Endpoint = "https://datadog.example?key=secret" }},
		{name: "endpoint fragment", change: func(value *DatadogForwardingOptions) { value.Endpoint = "https://datadog.example#secret" }},
		{name: "localhost endpoint", change: func(value *DatadogForwardingOptions) { value.Endpoint = "https://localhost" }},
		{name: "loopback endpoint", change: func(value *DatadogForwardingOptions) { value.Endpoint = "https://127.0.0.1" }},
		{name: "dynamic host interpolation", change: func(value *DatadogForwardingOptions) { value.Endpoint = "https://logs.${DD_SITE}" }},
		{name: "out-of-range port", change: func(value *DatadogForwardingOptions) { value.Endpoint = "https://datadog.example:99999" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.change(&options)
			if _, err := RenderDatadogVectorConfig(options); err == nil {
				t.Fatal("unsafe or malformed option was accepted")
			}
		})
	}
}
