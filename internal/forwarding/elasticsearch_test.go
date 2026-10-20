package forwarding

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func TestRenderElasticsearchVectorConfigIsHTTPSAndMetadataOnly(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private events.jsonl")
	config, err := RenderElasticsearchVectorConfig(ElasticsearchForwardingOptions{
		LogPath: logPath, Endpoint: "https://elastic.example:9200/", Index: "threadforge-events", SecretsDir: filepath.Join(t.TempDir(), "secret-values"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Elasticsearch config is not valid TOML: %v\n%s", err, config)
	}
	sources := document["sources"].(map[string]any)
	source := sources["threadforge_runtime"].(map[string]any)
	include := source["include"].([]any)
	if source["read_from"] != "end" || len(include) != 1 || include[0] != logPath {
		t.Fatalf("source must tail selected local log from EOF: %#v", source)
	}
	sinks := document["sinks"].(map[string]any)
	sink := sinks["threadforge_elasticsearch"].(map[string]any)
	if sink["type"] != "elasticsearch" || sink["endpoints"].([]any)[0] != "https://elastic.example:9200" {
		t.Fatalf("unexpected Elasticsearch sink config: %#v", sink)
	}
	bulk := sink["bulk"].(map[string]any)
	if bulk["index"] != "threadforge-events" {
		t.Fatalf("unexpected bulk index: %#v", bulk)
	}
	request := sink["request"].(map[string]any)
	headers := request["headers"].(map[string]any)
	if headers["Authorization"] != "ApiKey SECRET[threadforgesecrets.elasticsearchapikey]" {
		t.Fatalf("API key must be resolved externally by Vector: %#v", headers)
	}
	transforms := document["transforms"].(map[string]any)
	transform := transforms["threadforge_runtime_metadata"].(map[string]any)
	assertMetadataOnlyProjection(t, transform["source"].(string))
	for _, forbidden := range []string{"real-api-key", "HOSTNAME", "inventory", `"action":`} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("Elasticsearch config contains secret material, host identity, or non-allowlisted content %q", forbidden)
		}
	}
}

func TestRenderSyntheticElasticsearchSampleContainsOnlyMetadata(t *testing.T) {
	data, err := RenderSyntheticElasticsearchSample()
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytesTrimNewline(data), &event); err != nil {
		t.Fatalf("sample is invalid JSON: %v", err)
	}
	if len(event) != 3 || event["severity"] != "info" || event["threadforge_export"] == nil {
		t.Fatalf("sample contains fields outside the metadata export: %#v", event)
	}
	timestamp, ok := event["timestamp"].(string)
	if !ok {
		t.Fatalf("sample timestamp is not a string: %#v", event["timestamp"])
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		t.Fatalf("sample timestamp is invalid: %v", err)
	}
	marker := event["threadforge_export"].(map[string]any)
	if len(marker) != 2 || marker["schema_version"] != "threadforge.metadata-export.v1" || marker["raw_content"] != false {
		t.Fatalf("sample export marker is invalid: %#v", marker)
	}
}

func TestElasticsearchSmokeScriptsAreExplicitSyntheticOnly(t *testing.T) {
	sample, err := RenderSyntheticElasticsearchSample()
	if err != nil {
		t.Fatal(err)
	}
	shellScript, powerShellScript := renderElasticsearchSmokeTestScripts("https://elastic.example:9200", sample)
	digest := sha256.Sum256(sample)
	for _, want := range []string{
		"--send-synthetic-event",
		"ELASTIC_API_KEY",
		"ELASTIC_USERNAME",
		"ELASTIC_PASSWORD",
		"curl --config -",
		"_data_stream/" + defaultElasticsearchDataStream,
		"pipeline=" + elasticsearchMetadataPipeline,
		hex.EncodeToString(digest[:]),
	} {
		if !strings.Contains(shellScript, want) {
			t.Fatalf("POSIX smoke script is missing safety contract %q:\n%s", want, shellScript)
		}
	}
	for _, forbidden := range []string{"runtime.jsonl", "--location", "--verbose", "curl -T"} {
		if strings.Contains(shellScript, forbidden) {
			t.Fatalf("POSIX smoke script contains forbidden behavior %q:\n%s", forbidden, shellScript)
		}
	}
	if !strings.Contains(shellScript, "printf 'url = %s\\n'") ||
		strings.Contains(shellScript, "printf 'url = %s\\\\n'") || strings.Contains(shellScript, "%!") {
		t.Fatalf("POSIX smoke script contains a malformed curl config format: %s", shellScript)
	}
	for _, want := range []string{
		"-SendSyntheticEvent",
		"ELASTIC_API_KEY",
		"ELASTIC_USERNAME",
		"ELASTIC_PASSWORD",
		"AllowAutoRedirect = $false",
		"ResponseHeadersRead",
		"_data_stream/" + defaultElasticsearchDataStream,
		"pipeline=" + elasticsearchMetadataPipeline,
		hex.EncodeToString(digest[:]),
	} {
		if !strings.Contains(powerShellScript, want) {
			t.Fatalf("PowerShell smoke script is missing safety contract %q:\n%s", want, powerShellScript)
		}
	}
	for _, forbidden := range []string{"runtime.jsonl", "Invoke-WebRequest", "-MaximumRedirection"} {
		if strings.Contains(powerShellScript, forbidden) {
			t.Fatalf("PowerShell smoke script contains forbidden behavior %q:\n%s", forbidden, powerShellScript)
		}
	}
}

func TestElasticsearchPowerShellSmokeScriptUsesLocalFixtureForAPIKeyAndBasicAuth(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		pwsh, err = exec.LookPath("powershell.exe")
	}
	if err != nil {
		t.Skip("PowerShell is unavailable")
	}
	type capturedRequest struct {
		method        string
		path          string
		query         string
		authorization string
		body          []byte
	}
	var mu sync.Mutex
	var captures []capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 4097))
		if readErr != nil {
			t.Errorf("read fixture request: %v", readErr)
		}
		mu.Lock()
		captures = append(captures, capturedRequest{
			method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
			authorization: r.Header.Get("Authorization"), body: body,
		})
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "secret-response-body-must-not-be-displayed")
	}))
	defer server.Close()

	sample, err := RenderSyntheticElasticsearchSample()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample-event.jsonl"), sample, 0o600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "elasticsearch-upload-smoke-test.ps1")
	_, script := renderElasticsearchSmokeTestScripts(server.URL, sample)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	baseEnv := withoutEnvironment(os.Environ(), "ELASTIC_API_KEY", "ELASTIC_USERNAME", "ELASTIC_PASSWORD")
	runScript := func(t *testing.T, explicit bool, credentials ...string) ([]byte, error) {
		t.Helper()
		args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File", scriptPath}
		if explicit {
			args = append(args, "-SendSyntheticEvent")
		}
		cmd := exec.Command(pwsh, args...)
		cmd.Env = append(append([]string{}, baseEnv...), credentials...)
		return cmd.CombinedOutput()
	}
	noOptIn, noOptInErr := runScript(t, false, "ELASTIC_API_KEY=must-not-send")
	var noOptInExit *exec.ExitError
	if !errors.As(noOptInErr, &noOptInExit) || noOptInExit.ExitCode() != 2 || !strings.Contains(string(noOptIn), "No request was sent") {
		t.Fatalf("PowerShell smoke script did not fail closed without its explicit switch: err=%v output=%s", noOptInErr, noOptIn)
	}
	if len(captures) != 0 {
		t.Fatalf("PowerShell script contacted the local fixture without explicit consent: %#v", captures)
	}

	apiKey := "fixture-api-key-value"
	apiOutput, apiErr := runScript(t, true, "ELASTIC_API_KEY="+apiKey)
	if apiErr != nil {
		t.Fatalf("API-key smoke test failed against local fixture: %v output=%s", apiErr, apiOutput)
	}
	if !strings.Contains(string(apiOutput), "HTTP 202") || strings.Contains(string(apiOutput), apiKey) || strings.Contains(string(apiOutput), "secret-response-body-must-not-be-displayed") {
		t.Fatalf("API-key smoke output exposed a secret or response body: %s", apiOutput)
	}

	username, password := "fixture-user", `fixture-"password\value`
	basicOutput, basicErr := runScript(t, true, "ELASTIC_USERNAME="+username, "ELASTIC_PASSWORD="+password)
	if basicErr != nil {
		t.Fatalf("Basic-auth smoke test failed against local fixture: %v output=%s", basicErr, basicOutput)
	}
	if !strings.Contains(string(basicOutput), "HTTP 202") || strings.Contains(string(basicOutput), username) || strings.Contains(string(basicOutput), password) || strings.Contains(string(basicOutput), "secret-response-body-must-not-be-displayed") {
		t.Fatalf("Basic-auth smoke output exposed a secret or response body: %s", basicOutput)
	}

	expectedBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	mu.Lock()
	got := append([]capturedRequest(nil), captures...)
	mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("expected two preflight/write pairs, got %d requests: %#v", len(got), got)
	}
	for run, expectedAuth := range []string{"ApiKey " + apiKey, expectedBasic} {
		preflight, upload := got[run*2], got[run*2+1]
		if preflight.method != http.MethodGet || preflight.path != "/_data_stream/"+defaultElasticsearchDataStream || preflight.authorization != expectedAuth || len(preflight.body) != 0 {
			t.Fatalf("bad data-stream preflight request: %#v", preflight)
		}
		if upload.method != http.MethodPost || upload.path != "/"+defaultElasticsearchDataStream+"/_doc" || upload.query != "pipeline="+elasticsearchMetadataPipeline || upload.authorization != expectedAuth || string(upload.body) != string(sample) {
			t.Fatalf("bad synthetic document upload request: %#v", upload)
		}
	}
}

func TestElasticsearchPosixSmokeScriptUsesMockCurlAndExternalCredentials(t *testing.T) {
	shell := sumoSmokeShell(t)
	for _, mode := range []string{"api-key", "basic"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			packDir := filepath.Join(root, "elasticsearch pack")
			fakeBin := filepath.Join(root, "bin")
			if err := os.Mkdir(fakeBin, 0o700); err != nil {
				t.Fatal(err)
			}
			sample, err := RenderSyntheticElasticsearchSample()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(packDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(packDir, "sample-event.jsonl"), sample, 0o600); err != nil {
				t.Fatal(err)
			}
			shellScript, _ := renderElasticsearchSmokeTestScripts("https://elastic.example:9200", sample)
			scriptPath := filepath.Join(packDir, "elasticsearch-upload-smoke-test.sh")
			if err := os.WriteFile(scriptPath, []byte(shellScript), 0o600); err != nil {
				t.Fatal(err)
			}
			captureConfig := filepath.Join(root, "curl-config.txt")
			captureArgs := filepath.Join(root, "curl-args.txt")
			fakeCurl := "#!/bin/sh\n" +
				"if env | grep -q '^ELASTIC_API_KEY=' || env | grep -q '^ELASTIC_USERNAME=' || env | grep -q '^ELASTIC_PASSWORD='; then exit 91; fi\n" +
				"printf 'ARGS' >> \"$THREADFORGE_TEST_CURL_ARGS\"\n" +
				"for arg in \"$@\"; do printf '<%s>' \"$arg\" >> \"$THREADFORGE_TEST_CURL_ARGS\"; done\n" +
				"printf '\\n' >> \"$THREADFORGE_TEST_CURL_ARGS\"\n" +
				"printf 'CONFIG-BEGIN\\n' >> \"$THREADFORGE_TEST_CURL_CONFIG\"\n" +
				"cat >> \"$THREADFORGE_TEST_CURL_CONFIG\"\n" +
				"printf '\\nCONFIG-END\\n' >> \"$THREADFORGE_TEST_CURL_CONFIG\"\n" +
				"case \" $* \" in *' --request POST '*) printf '201' ;; *) printf '200' ;; esac\n"
			if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(fakeCurl), 0o700); err != nil {
				t.Fatal(err)
			}
			baseEnv := withoutEnvironment(
				os.Environ(),
				"ELASTIC_API_KEY", "ELASTIC_USERNAME", "ELASTIC_PASSWORD",
				"THREADFORGE_TEST_CURL_ARGS", "THREADFORGE_TEST_CURL_CONFIG",
			)
			runScript := func(explicit bool, credentials ...string) ([]byte, error) {
				args := []string{scriptPath}
				if explicit {
					args = append(args, "--send-synthetic-event")
				}
				cmd := exec.Command(shell, args...)
				cmd.Env = append(append([]string{}, baseEnv...),
					"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
					"THREADFORGE_TEST_CURL_ARGS="+captureArgs,
					"THREADFORGE_TEST_CURL_CONFIG="+captureConfig,
				)
				cmd.Env = append(cmd.Env, credentials...)
				return cmd.CombinedOutput()
			}
			noOptIn, noOptInErr := runScript(false, "ELASTIC_API_KEY=must-not-send")
			var noOptInExit *exec.ExitError
			if !errors.As(noOptInErr, &noOptInExit) || noOptInExit.ExitCode() != 2 || !strings.Contains(string(noOptIn), "No request was sent") {
				t.Fatalf("POSIX smoke script did not fail closed without explicit flag: err=%v output=%s", noOptInErr, noOptIn)
			}
			if _, err := os.Stat(captureArgs); !os.IsNotExist(err) {
				t.Fatalf("mock curl was invoked before consent: %v", err)
			}

			credentials := []string{"ELASTIC_API_KEY=fixture-api-key"}
			wantAuthorization := "Authorization: ApiKey fixture-api-key"
			if mode == "basic" {
				credentials = []string{"ELASTIC_USERNAME=fixture-user", "ELASTIC_PASSWORD=fixture-password"}
				wantAuthorization = "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("fixture-user:fixture-password"))
			}
			output, err := runScript(true, credentials...)
			if err != nil {
				t.Fatalf("POSIX smoke script failed with mocked curl: %v output=%s", err, output)
			}
			if !strings.Contains(string(output), "HTTP 201") || strings.Contains(string(output), "fixture-api-key") || strings.Contains(string(output), "fixture-password") {
				t.Fatalf("POSIX smoke output exposed credentials or missed success: %s", output)
			}
			config, err := os.ReadFile(captureConfig)
			if err != nil {
				t.Fatalf("mock curl did not capture config stdin: %v", err)
			}
			if strings.Count(string(config), "CONFIG-BEGIN") != 2 || strings.Count(string(config), wantAuthorization) != 2 ||
				!strings.Contains(string(config), "_data_stream/"+defaultElasticsearchDataStream) ||
				!strings.Contains(string(config), "pipeline="+elasticsearchMetadataPipeline) {
				t.Fatalf("POSIX smoke script sent unexpected config requests: %s", config)
			}
			args, err := os.ReadFile(captureArgs)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(args), "fixture-api-key") || strings.Contains(string(args), "fixture-password") ||
				!strings.Contains(string(args), "sample-event.jsonl") || strings.Contains(string(args), "runtime.jsonl") ||
				strings.Count(string(args), "ARGS") != 2 {
				t.Fatalf("curl argv contained secrets/raw path or missed requests: %s", args)
			}
		})
	}
}

func TestElasticsearchPowerShellSmokeDoesNotWriteWhenDataStreamIsMissing(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		pwsh, err = exec.LookPath("powershell.exe")
	}
	if err != nil {
		t.Skip("PowerShell is unavailable")
	}
	methods := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods <- r.Method
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "response-body-must-not-be-shown")
	}))
	defer server.Close()
	sample, err := RenderSyntheticElasticsearchSample()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample-event.jsonl"), sample, 0o600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "elasticsearch-upload-smoke-test.ps1")
	_, script := renderElasticsearchSmokeTestScripts(server.URL, sample)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", scriptPath, "-SendSyntheticEvent")
	cmd.Env = append(withoutEnvironment(os.Environ(), "ELASTIC_API_KEY", "ELASTIC_USERNAME", "ELASTIC_PASSWORD"), "ELASTIC_API_KEY=fixture-key")
	output, runErr := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1 || strings.Contains(string(output), "response-body-must-not-be-shown") {
		t.Fatalf("missing stream did not fail closed with body suppressed: err=%v output=%s", runErr, output)
	}
	select {
	case method := <-methods:
		if method != http.MethodGet {
			t.Fatalf("unexpected request was sent before stream existence was confirmed: %s", method)
		}
	default:
		t.Fatal("expected a read-only data-stream preflight request")
	}
	select {
	case method := <-methods:
		t.Fatalf("document write was attempted despite missing data stream: %s", method)
	default:
	}
}

func withoutEnvironment(environment []string, names ...string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[strings.ToUpper(name)] = struct{}{}
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, denied := blocked[strings.ToUpper(name)]; !denied {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func TestInstallElasticsearchPackIsPrivateAndRefusesOverwrite(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "elasticsearch-pack")
	options := ElasticsearchForwardingOptions{
		LogPath: filepath.Join(parent, "runtime.jsonl"), Endpoint: "https://elastic.example:9200", SecretsDir: filepath.Join(parent, "secret-values"),
	}
	installed, err := InstallElasticsearchForwardingPack(output, options)
	if err != nil {
		t.Fatal(err)
	}
	if installed != output {
		t.Fatalf("installed path = %q, want %q", installed, output)
	}
	for _, name := range []string{
		"README.md", "sample-event.jsonl", "elasticsearch-upload-smoke-test.sh", "elasticsearch-upload-smoke-test.ps1", "vector.toml", "ilm-policy.json",
		"component-template-settings.json", "component-template-mappings.json", "index-template.json",
		"data-stream-template.json", "ingest-pipeline.json", "kibana-assets.ndjson",
		"docker-compose.yml", "docker-compose.env", "filebeat-local.yml", "local-sample-event.jsonl",
	} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing pack asset %s: %v", name, err)
		}
	}
	assertElasticsearchSmokeScriptsBindPackSample(t, output)
	readme, err := os.ReadFile(filepath.Join(output, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{
		"does not install or start Vector", "does not backfill", filepath.Join(options.SecretsDir, elasticsearchAPIKeySecretName),
		"metadata-only", "Manual cluster asset installation", "threadforge-data-stream", "--send-synthetic-event", "-SendSyntheticEvent",
	} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("README omits privacy or setup requirement %q", phrase)
		}
	}
	if _, err := InstallElasticsearchForwardingPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing output directory should be refused, got %v", err)
	}
}

func TestRenderElasticsearchRejectsUnsafeInputs(t *testing.T) {
	valid := ElasticsearchForwardingOptions{
		LogPath: filepath.Join(t.TempDir(), "events.jsonl"), Endpoint: "https://elastic.example:9200",
	}
	cases := []struct {
		name   string
		change func(*ElasticsearchForwardingOptions)
	}{
		{name: "relative log path", change: func(value *ElasticsearchForwardingOptions) { value.LogPath = "events.jsonl" }},
		{name: "http endpoint", change: func(value *ElasticsearchForwardingOptions) { value.Endpoint = "http://elastic.example:9200" }},
		{name: "endpoint userinfo", change: func(value *ElasticsearchForwardingOptions) { value.Endpoint = "https://key@elastic.example:9200" }},
		{name: "endpoint path", change: func(value *ElasticsearchForwardingOptions) { value.Endpoint = "https://elastic.example:9200/tenant" }},
		{name: "endpoint query", change: func(value *ElasticsearchForwardingOptions) {
			value.Endpoint = "https://elastic.example:9200?key=secret"
		}},
		{name: "endpoint fragment", change: func(value *ElasticsearchForwardingOptions) { value.Endpoint = "https://elastic.example:9200#secret" }},
		{name: "localhost endpoint", change: func(value *ElasticsearchForwardingOptions) { value.Endpoint = "https://localhost:9200" }},
		{name: "loopback endpoint", change: func(value *ElasticsearchForwardingOptions) { value.Endpoint = "https://127.0.0.1:9200" }},
		{name: "dynamic host interpolation", change: func(value *ElasticsearchForwardingOptions) { value.Endpoint = "https://elastic.${HOST}:9200" }},
		{name: "out-of-range port", change: func(value *ElasticsearchForwardingOptions) { value.Endpoint = "https://elastic.example:99999" }},
		{name: "index injection", change: func(value *ElasticsearchForwardingOptions) { value.Index = "index\"\n[sinks.injected]" }},
		{name: "uppercase index", change: func(value *ElasticsearchForwardingOptions) { value.Index = "Threadforge" }},
		{name: "consecutive dots", change: func(value *ElasticsearchForwardingOptions) { value.Index = "threadforge..events" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.change(&options)
			if _, err := RenderElasticsearchVectorConfig(options); err == nil {
				t.Fatal("unsafe or malformed option was accepted")
			}
		})
	}
}

func TestElasticsearchNativeShipperConfigsReadOnlySanitizedMetadata(t *testing.T) {
	root := t.TempDir()
	rawPath := filepath.Join(root, "private-runtime.jsonl")
	metadataPath := filepath.Join(root, "sanitized-metadata.jsonl")
	options := ElasticsearchAgentForwardingOptions{
		LogPath: rawPath, MetadataLogPath: metadataPath,
		Endpoint: "https://elastic.example:9200/",
	}

	filebeatConfig, err := RenderElasticsearchFilebeatConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	var filebeat map[string]any
	if err := yaml.Unmarshal([]byte(filebeatConfig), &filebeat); err != nil {
		t.Fatalf("generated Filebeat YAML is invalid: %v\n%s", err, filebeatConfig)
	}
	inputs := filebeat["filebeat.inputs"].([]any)
	input := inputs[0].(map[string]any)
	paths := input["paths"].([]any)
	if len(paths) != 1 || paths[0] != metadataPath || strings.Contains(filebeatConfig, rawPath) {
		t.Fatalf("Filebeat must read only the separate sanitized file: %#v", input)
	}
	parsers := input["parsers"].([]any)
	if parsers[0].(map[string]any)["ndjson"] == nil {
		t.Fatalf("Filebeat must parse one JSON document per line: %#v", parsers)
	}
	output := filebeat["output.elasticsearch"].(map[string]any)
	if output["hosts"].([]any)[0] != "https://elastic.example:9200" || output["index"] != defaultElasticsearchDataStream || output["ssl.verification_mode"] != "full" {
		t.Fatalf("Filebeat output is not pinned to the normalized HTTPS destination and index: %#v", output)
	}

	agentConfig, err := RenderElasticsearchStandaloneAgentConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	var agent map[string]any
	if err := yaml.Unmarshal([]byte(agentConfig), &agent); err != nil {
		t.Fatalf("generated standalone Elastic Agent YAML is invalid: %v\n%s", err, agentConfig)
	}
	agentInputs := agent["inputs"].([]any)
	streams := agentInputs[0].(map[string]any)["streams"].([]any)
	agentPaths := streams[0].(map[string]any)["paths"].([]any)
	if len(agentPaths) != 1 || agentPaths[0] != metadataPath || strings.Contains(agentConfig, rawPath) {
		t.Fatalf("standalone Elastic Agent must read only the separate sanitized file: %#v", streams[0])
	}
	outputs := agent["outputs"].(map[string]any)
	defaultOutput := outputs["default"].(map[string]any)
	if defaultOutput["hosts"].([]any)[0] != "https://elastic.example:9200" || defaultOutput["ssl.verification_mode"] != "full" {
		t.Fatalf("standalone Elastic Agent output must use verified HTTPS: %#v", defaultOutput)
	}
	for _, config := range []string{filebeatConfig, agentConfig} {
		for _, forbidden := range []string{"private-runtime.jsonl", "API_KEY_VALUE", "password:", "username:"} {
			if strings.Contains(config, forbidden) {
				t.Fatalf("native shipper config contains raw path or credentials %q: %s", forbidden, config)
			}
		}
	}

	vectorConfig, err := RenderElasticsearchAgentVectorConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	var vector map[string]any
	if err := toml.Unmarshal([]byte(vectorConfig), &vector); err != nil {
		t.Fatalf("generated local sanitizer TOML is invalid: %v\n%s", err, vectorConfig)
	}
	sources := vector["sources"].(map[string]any)
	if sources["threadforge_runtime"].(map[string]any)["read_from"] != "end" {
		t.Fatalf("local sanitizer must start at EOF: %#v", sources)
	}
	sinks := vector["sinks"].(map[string]any)
	if sinks["threadforge_elasticsearch_metadata"].(map[string]any)["path"] != metadataPath || strings.Contains(vectorConfig, "type = \"elasticsearch\"") {
		t.Fatalf("Vector bridge must write only a local metadata file: %#v", sinks)
	}
	transforms := vector["transforms"].(map[string]any)
	assertMetadataOnlyProjection(t, transforms["threadforge_runtime_metadata"].(map[string]any)["source"].(string))
}

func TestInstallElasticsearchNativeShipperPackContainsNoCredentials(t *testing.T) {
	for _, shipper := range []string{"filebeat", "elastic-agent"} {
		t.Run(shipper, func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, "pack")
			options := ElasticsearchAgentForwardingOptions{
				LogPath: filepath.Join(root, "runtime.jsonl"), MetadataLogPath: filepath.Join(root, "metadata.jsonl"),
				Endpoint: "https://elastic.example:9200",
			}
			installed, err := InstallElasticsearchAgentForwardingPack(output, shipper, options)
			if err != nil {
				t.Fatal(err)
			}
			if installed != output {
				t.Fatalf("installed path = %q, want %q", installed, output)
			}
			shipperFile := "filebeat.yml"
			if shipper == "elastic-agent" {
				shipperFile = "elastic-agent-standalone.yml"
			}
			for _, name := range []string{
				"README.md", "sample-event.jsonl", "elasticsearch-upload-smoke-test.sh", "elasticsearch-upload-smoke-test.ps1", "vector.toml", shipperFile,
				"ilm-policy.json", "component-template-settings.json", "component-template-mappings.json",
				"index-template.json", "data-stream-template.json", "ingest-pipeline.json", "kibana-assets.ndjson",
				"docker-compose.yml", "docker-compose.env", "filebeat-local.yml", "local-sample-event.jsonl",
			} {
				if _, err := os.Stat(filepath.Join(output, name)); err != nil {
					t.Fatalf("missing pack asset %s: %v", name, err)
				}
			}
			assertElasticsearchSmokeScriptsBindPackSample(t, output)
			config, err := os.ReadFile(filepath.Join(output, shipperFile))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(config), "runtime.jsonl") || strings.Contains(string(config), "API_KEY_VALUE") {
				t.Fatalf("shipper config includes raw source or credentials: %s", config)
			}
			if _, err := InstallElasticsearchAgentForwardingPack(output, shipper, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
				t.Fatalf("existing output directory should be refused, got %v", err)
			}
		})
	}
}

func assertElasticsearchSmokeScriptsBindPackSample(t *testing.T, packDir string) {
	t.Helper()
	sample, err := os.ReadFile(filepath.Join(packDir, "sample-event.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(sample)
	want := hex.EncodeToString(digest[:])
	for _, name := range []string{"elasticsearch-upload-smoke-test.sh", "elasticsearch-upload-smoke-test.ps1"} {
		script, err := os.ReadFile(filepath.Join(packDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(script), want) {
			t.Fatalf("%s is not bound to the exact pack sample", name)
		}
	}
}

func TestElasticsearchAssetsEnforceMetadataOnlySchemaAndTargets(t *testing.T) {
	files, err := elasticsearchPackAssets("threadforge-metadata")
	if err != nil {
		t.Fatal(err)
	}
	contents := make(map[string][]byte, len(files))
	for _, file := range files {
		contents[file.name] = file.content
	}
	var mappings map[string]any
	if err := json.Unmarshal(contents["component-template-mappings.json"], &mappings); err != nil {
		t.Fatal(err)
	}
	template := mappings["template"].(map[string]any)
	rootMappings := template["mappings"].(map[string]any)
	if rootMappings["dynamic"] != "strict" {
		t.Fatalf("mappings must reject fields outside the metadata contract: %#v", rootMappings)
	}
	properties := rootMappings["properties"].(map[string]any)
	for _, key := range []string{"@timestamp", "severity", "threadforge_export"} {
		if properties[key] == nil {
			t.Fatalf("strict mapping missing approved field %q", key)
		}
	}
	if len(properties) != 3 {
		t.Fatalf("strict mapping unexpectedly contains additional fields: %#v", properties)
	}

	var indexTemplate map[string]any
	if err := json.Unmarshal(contents["index-template.json"], &indexTemplate); err != nil {
		t.Fatal(err)
	}
	if indexTemplate["index_patterns"].([]any)[0] != "threadforge-metadata" {
		t.Fatalf("vector index template does not target configured index: %#v", indexTemplate)
	}
	indexTemplateSettings := indexTemplate["template"].(map[string]any)["settings"].(map[string]any)
	if indexTemplateSettings["index.default_pipeline"] != elasticsearchMetadataPipeline {
		t.Fatalf("Vector index template does not enforce the strict ingest pipeline: %#v", indexTemplateSettings)
	}
	var dataStreamTemplate map[string]any
	if err := json.Unmarshal(contents["data-stream-template.json"], &dataStreamTemplate); err != nil {
		t.Fatal(err)
	}
	if dataStreamTemplate["index_patterns"].([]any)[0] != "logs-threadforge.endpoint-*" || dataStreamTemplate["data_stream"] == nil {
		t.Fatalf("native shipper data-stream template target is invalid: %#v", dataStreamTemplate)
	}
	dataStreamSettings := dataStreamTemplate["template"].(map[string]any)["settings"].(map[string]any)
	if dataStreamSettings["index.default_pipeline"] != elasticsearchMetadataPipeline || dataStreamSettings["index.lifecycle.name"] != elasticsearchMetadataPipeline {
		t.Fatalf("data stream template must enforce metadata pipeline and retention policy: %#v", dataStreamSettings)
	}
	var ilm map[string]any
	if err := json.Unmarshal(contents["ilm-policy.json"], &ilm); err != nil {
		t.Fatal(err)
	}
	deletePhase := ilm["policy"].(map[string]any)["phases"].(map[string]any)["delete"].(map[string]any)
	if deletePhase["min_age"] != "90d" {
		t.Fatalf("data-stream retention policy unexpectedly changed: %#v", deletePhase)
	}

	var pipeline map[string]any
	if err := json.Unmarshal(contents["ingest-pipeline.json"], &pipeline); err != nil {
		t.Fatal(err)
	}
	processors := pipeline["processors"].([]any)
	var script string
	var keepFields []any
	for _, processor := range processors {
		processorMap := processor.(map[string]any)
		if value, ok := processorMap["script"]; ok {
			script = value.(map[string]any)["source"].(string)
		}
		if value, ok := processorMap["remove"]; ok {
			keepFields = value.(map[string]any)["keep"].([]any)
		}
	}
	if len(keepFields) != 3 || keepFields[0] != "@timestamp" || keepFields[1] != "severity" || keepFields[2] != "threadforge_export" ||
		strings.Contains(script, "ctx.clear()") ||
		!strings.Contains(script, "threadforge.metadata-export.v1") ||
		!strings.Contains(script, "ctx.put('raw_content', false)") && !strings.Contains(script, "exportMarker.put('raw_content', false)") {
		t.Fatalf("ingest pipeline does not use the fixed source-field allowlist and privacy marker: keep=%#v script=%s", keepFields, script)
	}

	for _, line := range bytes.Split(bytes.TrimSpace(contents["kibana-assets.ndjson"]), []byte{'\n'}) {
		var object map[string]any
		if err := json.Unmarshal(line, &object); err != nil {
			t.Fatalf("Kibana NDJSON object is invalid: %v", err)
		}
		encoded := string(line)
		if strings.Contains(strings.ToLower(encoded), "beacon") || strings.Contains(encoded, "prompt.text") || strings.Contains(encoded, "host.name") {
			t.Fatalf("Kibana asset contains legacy/raw-field content: %s", encoded)
		}
		if object["type"] == "search" {
			attributes := object["attributes"].(map[string]any)
			columns := attributes["columns"].([]any)
			for _, column := range columns {
				if column != "@timestamp" && column != "severity" &&
					column != "threadforge_export.schema_version" && column != "threadforge_export.raw_content" {
					t.Fatalf("Kibana saved search exposes a non-metadata column %q", column)
				}
			}
			meta := attributes["kibanaSavedObjectMeta"].(map[string]any)
			searchSource := meta["searchSourceJSON"].(string)
			var parsedSearchSource map[string]any
			if err := json.Unmarshal([]byte(searchSource), &parsedSearchSource); err != nil {
				t.Fatalf("Kibana searchSourceJSON is invalid JSON: %v: %s", err, searchSource)
			}
		}
	}
}

func TestNormalizeElasticsearchAgentOptionsRejectsRawOrPreexistingMetadataDestination(t *testing.T) {
	root := t.TempDir()
	rawPath := filepath.Join(root, "runtime.jsonl")
	options := ElasticsearchAgentForwardingOptions{
		LogPath: rawPath, MetadataLogPath: rawPath, Endpoint: "https://elastic.example:9200",
	}
	if _, err := normalizeElasticsearchAgentOptions(options); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("raw trace destination was accepted: %v", err)
	}
	metadataPath := filepath.Join(root, "metadata.jsonl")
	if err := os.WriteFile(metadataPath, []byte("preexisting content"), 0o600); err != nil {
		t.Fatal(err)
	}
	options.MetadataLogPath = metadataPath
	if _, err := normalizeElasticsearchAgentOptions(options); err == nil || !strings.Contains(err.Error(), "absent or empty") {
		t.Fatalf("preexisting metadata destination was accepted: %v", err)
	}
}

func TestNormalizeElasticsearchAgentOptionsRejectsHardLinkToRawTrace(t *testing.T) {
	root := t.TempDir()
	rawPath := filepath.Join(root, "runtime.jsonl")
	metadataPath := filepath.Join(root, "metadata.jsonl")
	if err := os.WriteFile(rawPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(rawPath, metadataPath); err != nil {
		t.Skipf("hard links are unavailable in this test environment: %v", err)
	}
	_, err := normalizeElasticsearchAgentOptions(ElasticsearchAgentForwardingOptions{
		LogPath: rawPath, MetadataLogPath: metadataPath, Endpoint: "https://elastic.example:9200",
	})
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("hard link to raw trace was accepted as metadata destination: %v", err)
	}
}

func TestElasticsearchLocalStackIsSyntheticOnlyAndPackValidated(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, "pack")
	installed, err := InstallElasticsearchForwardingPack(packDir, ElasticsearchForwardingOptions{
		LogPath: filepath.Join(root, "private-runtime.jsonl"), Endpoint: "https://elastic.example:9200", Index: "threadforge-local-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateElasticsearchLocalStackPack(installed); err != nil {
		t.Fatalf("generated local stack pack failed validation: %v", err)
	}

	composeBytes, err := os.ReadFile(filepath.Join(installed, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var compose map[string]any
	if err := yaml.Unmarshal(composeBytes, &compose); err != nil {
		t.Fatalf("generated Compose YAML is invalid: %v", err)
	}
	services := compose["services"].(map[string]any)
	if len(services) != 4 {
		t.Fatalf("local stack should contain only Elasticsearch, Kibana, asset setup, and synthetic Filebeat: %#v", services)
	}
	if services["elasticsearch"].(map[string]any)["image"] != "docker.elastic.co/elasticsearch/elasticsearch:9.5.4" ||
		services["kibana"].(map[string]any)["image"] != "docker.elastic.co/kibana/kibana:9.5.4" ||
		services["filebeat"].(map[string]any)["image"] != "docker.elastic.co/beats/filebeat:9.5.4" {
		t.Fatalf("Elastic stack image versions are not consistently pinned: %#v", services)
	}
	networks := compose["networks"].(map[string]any)
	if networks["threadforge-local"].(map[string]any)["internal"] != true {
		t.Fatalf("local validation stack network must not have external egress: %#v", networks)
	}

	localFilebeat, err := os.ReadFile(filepath.Join(installed, "filebeat-local.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(localFilebeat), "private-runtime.jsonl") || strings.Contains(string(composeBytes), "private-runtime.jsonl") ||
		!strings.Contains(string(localFilebeat), "/var/lib/threadforge/sample.jsonl") {
		t.Fatalf("local stack must ship only the synthetic fixture, never the raw runtime log")
	}
	localSample, err := os.ReadFile(filepath.Join(installed, "local-sample-event.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var sample map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(localSample), &sample); err != nil {
		t.Fatalf("local synthetic fixture is invalid JSON: %v", err)
	}
	if len(sample) != 3 || sample["severity"] != "info" || sample["threadforge_export"] == nil {
		t.Fatalf("local fixture must contain only approved metadata: %#v", sample)
	}
	if err := validateElasticsearchLocalSample(localSample); err != nil {
		t.Fatalf("generated local synthetic fixture failed strict validation: %v", err)
	}

	if err := os.WriteFile(filepath.Join(installed, "docker-compose.yml"), append(composeBytes, []byte("# user-edited\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateElasticsearchLocalStackPack(installed); err == nil || !strings.Contains(err.Error(), "was modified") {
		t.Fatalf("modified Compose file must fail before Docker invocation: %v", err)
	}
}

func TestElasticsearchLocalStackControlValidationAllowsOlderAssetsButProtectsCompose(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, "pack")
	installed, err := InstallElasticsearchForwardingPack(packDir, ElasticsearchForwardingOptions{
		LogPath: filepath.Join(root, "private-runtime.jsonl"), Endpoint: "https://elastic.example:9200",
	})
	if err != nil {
		t.Fatal(err)
	}
	pipelinePath := filepath.Join(installed, "ingest-pipeline.json")
	pipeline, err := os.ReadFile(pipelinePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pipelinePath, []byte(`{"processors":[{"script":{"source":"legacy pipeline"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateElasticsearchLocalStackControlPack(installed); err != nil {
		t.Fatalf("status/down should accept an intact Compose pack with an older data pipeline: %v", err)
	}
	if err := ValidateElasticsearchLocalStackPack(installed); err == nil {
		t.Fatal("up validation must still reject the old pipeline asset")
	}
	if err := os.WriteFile(pipelinePath, pipeline, 0o600); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(installed, "docker-compose.env")
	if err := os.WriteFile(envPath, []byte("THREADFORGE_ELASTICSEARCH_PORT=9400\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateElasticsearchLocalStackControlPack(installed); err == nil {
		t.Fatal("status/down must reject a populated Compose env file")
	}
	if err := os.WriteFile(envPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(installed, "docker-compose.yml")
	compose, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(composePath, append(compose, []byte("# modified\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateElasticsearchLocalStackControlPack(installed); err == nil {
		t.Fatal("status/down must reject a modified Compose file")
	}
}

func TestElasticsearchComposeConfigWithInstalledDockerCLI(t *testing.T) {
	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker CLI is unavailable; generated Compose YAML is still structurally tested")
	}
	if output, err := exec.Command(dockerBin, "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("Docker Compose plugin is unavailable: %v: %s", err, output)
	}
	root := t.TempDir()
	packDir := filepath.Join(root, "pack")
	if _, err := InstallElasticsearchForwardingPack(packDir, ElasticsearchForwardingOptions{
		LogPath: filepath.Join(root, "runtime.jsonl"), Endpoint: "https://elastic.example:9200",
	}); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(dockerBin, "compose", "--project-directory", packDir, "--env-file", "docker-compose.env", "--file", "docker-compose.yml", "config", "--quiet")
	command.Dir = packDir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Docker Compose rejected the generated local stack config: %v\n%s", err, output)
	}
}
