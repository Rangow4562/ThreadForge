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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestRenderSumoVectorConfigIsMetadataOnlyAndUsesExternalSecrets(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private events.jsonl")
	secretsDir := filepath.Join(t.TempDir(), "secret-values")
	endpoint := "https://collectors.sumologic.com/receiver/v1/http"
	config, err := RenderSumoVectorConfig(SumoForwardingOptions{LogPath: logPath, Endpoint: endpoint, SecretsDir: secretsDir})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(config), &document); err != nil {
		t.Fatalf("generated Sumo config is not valid TOML: %v\n%s", err, config)
	}
	source := document["sources"].(map[string]any)["threadforge_runtime"].(map[string]any)
	include := source["include"].([]any)
	if source["read_from"] != "end" || len(include) != 1 || include[0] != logPath {
		t.Fatalf("source must tail selected local log from EOF: %#v", source)
	}
	sink := document["sinks"].(map[string]any)["threadforge_sumo"].(map[string]any)
	if sink["type"] != "http" || sink["uri"] != endpoint || sink["method"] != "post" {
		t.Fatalf("unexpected Sumo HTTP sink config: %#v", sink)
	}
	framing := sink["framing"].(map[string]any)
	if framing["method"] != "newline_delimited" {
		t.Fatalf("HTTP sink must preserve one JSON event per line: %#v", framing)
	}
	headers := sink["request"].(map[string]any)["headers"].(map[string]any)
	if headers["x-sumo-token"] != "SECRET[threadforgesecrets.sumotoken]" {
		t.Fatalf("Sumo Auth Header token must come from the Vector secret backend: %#v", headers)
	}
	if document["secret"].(map[string]any)["threadforgesecrets"].(map[string]any)["path"] != secretsDir {
		t.Fatal("generated config must use the selected Vector directory-secrets backend")
	}
	transform := document["transforms"].(map[string]any)["threadforge_runtime_metadata"].(map[string]any)
	assertMetadataOnlyProjection(t, transform["source"].(string))
	for _, forbidden := range []string{"secret-source-url", "secret-sumo-token", "HOSTNAME", "inventory", `"action":`} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("Sumo config contains secret material, host identity, or non-allowlisted content %q", forbidden)
		}
	}
}

func TestRenderSyntheticSumoSampleIsMetadataOnly(t *testing.T) {
	data, err := RenderSyntheticSumoSample()
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

func TestSumoSmokeScriptIsExplicitSyntheticOnlyAndKeepsTokenOffArgv(t *testing.T) {
	options := SumoForwardingOptions{
		LogPath:    filepath.Join(t.TempDir(), "private runtime.jsonl"),
		Endpoint:   "https://collectors.sumologic.com/receiver/v1/http",
		SecretsDir: filepath.Join(t.TempDir(), "vector secrets"),
	}
	sample, err := RenderSyntheticSumoSample()
	if err != nil {
		t.Fatal(err)
	}
	script := renderSumoSmokeTestScript(options.Endpoint, sample)
	powershellScript := renderSumoPowerShellSmokeTestScript(options.Endpoint, sample)
	digest := sha256.Sum256(sample)
	for _, want := range []string{
		"--send-synthetic-event",
		"${SUMO_TOKEN:-}",
		"unset SUMO_TOKEN",
		"curl --config -",
		"--data-binary \"@$sample_file\"",
		"--output /dev/null",
		hex.EncodeToString(digest[:]),
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("smoke script is missing safety contract %q:\n%s", want, script)
		}
	}
	for _, forbidden := range []string{
		options.LogPath,
		options.SecretsDir,
		"BEACON_LOG",
		"curl -T",
		"--location",
		"--verbose",
		"$SUMO_TOKEN\" --",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("smoke script contains forbidden runtime/secret handling %q:\n%s", forbidden, script)
		}
	}
	if strings.Contains(script, "%!") || strings.Contains(script, `printf 'url = %s\\n'`) {
		t.Fatalf("smoke script contains unexpanded formatting or a literal backslash-n: %s", script)
	}
	for _, want := range []string{
		"-SendSyntheticEvent",
		"SUMO_TOKEN",
		"AllowAutoRedirect = $false",
		"ResponseHeadersRead",
		"ReadAllBytes($samplePath)",
		hex.EncodeToString(digest[:]),
	} {
		if !strings.Contains(powershellScript, want) {
			t.Fatalf("PowerShell smoke script is missing safety contract %q:\n%s", want, powershellScript)
		}
	}
	for _, forbidden := range []string{options.LogPath, options.SecretsDir, "BEACON_LOG", "Invoke-WebRequest", "-MaximumRedirection"} {
		if strings.Contains(powershellScript, forbidden) {
			t.Fatalf("PowerShell smoke script contains forbidden runtime/redirect handling %q:\n%s", forbidden, powershellScript)
		}
	}
}

func TestSumoPowerShellSmokeScriptPostsOnlyPinnedSampleToLocalFixture(t *testing.T) {
	pwsh := testPowerShell(t)
	type requestCapture struct {
		method string
		path   string
		token  string
		body   []byte
	}
	captures := make(chan requestCapture, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 4097))
		if readErr != nil {
			t.Errorf("read fixture request: %v", readErr)
		}
		captures <- requestCapture{method: r.Method, path: r.URL.Path, token: r.Header.Get("x-sumo-token"), body: body}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "secret-response-body-must-not-be-displayed")
	}))
	defer server.Close()

	sample, err := RenderSyntheticSumoSample()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample-event.jsonl"), sample, 0o600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "sumo-upload-smoke-test.ps1")
	script := renderSumoPowerShellSmokeTestScript(server.URL+sumoAuthPath, sample)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := `unit-token-"\\safe`
	noOptIn := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", scriptPath)
	noOptIn.Env = append(os.Environ(), "SUMO_TOKEN="+secret)
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
	cmd.Env = append(os.Environ(), "SUMO_TOKEN="+secret)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell smoke script failed against local fixture: %v output=%s", err, output)
	}
	if !strings.Contains(string(output), "HTTP 202") || strings.Contains(string(output), "secret-response-body-must-not-be-displayed") || strings.Contains(string(output), secret) {
		t.Fatalf("PowerShell smoke script exposed data or did not report the status: %s", output)
	}
	select {
	case captured := <-captures:
		if captured.method != http.MethodPost || captured.path != sumoAuthPath || captured.token != secret || string(captured.body) != string(sample) {
			t.Fatalf("unexpected fixture request: method=%q path=%q token=%q body=%s", captured.method, captured.path, captured.token, captured.body)
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

func TestSumoSmokeScriptDoesNothingWithoutExplicitSendFlag(t *testing.T) {
	shell := sumoSmokeShell(t)
	sample, err := RenderSyntheticSumoSample()
	if err != nil {
		t.Fatal(err)
	}
	script := renderSumoSmokeTestScript("https://collectors.sumologic.com/receiver/v1/http", sample)
	scriptPath := filepath.Join(t.TempDir(), "sumo-upload-smoke-test.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, scriptPath)
	cmd.Env = append(os.Environ(), "SUMO_TOKEN=must-not-be-used")
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 || !strings.Contains(string(output), "No request was sent") {
		t.Fatalf("smoke script did not fail closed without explicit send flag: err=%v output=%s", err, output)
	}
}

func TestSumoSmokeScriptPostsPinnedSampleThroughCurlConfigStdin(t *testing.T) {
	shell := sumoSmokeShell(t)
	root := t.TempDir()
	packDir := filepath.Join(root, "sumo pack")
	options := SumoForwardingOptions{
		LogPath:  filepath.Join(root, "private runtime.jsonl"),
		Endpoint: "https://collectors.sumologic.com/receiver/v1/http",
	}
	if _, err := InstallSumoForwardingPack(packDir, options); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(root, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(root, "curl-args.txt")
	configPath := filepath.Join(root, "curl-config.txt")
	fakeCurl := `#!/bin/sh
if [ "${SUMO_TOKEN+x}" = x ]; then
  exit 91
fi
printf '%s\n' "$@" > "$THREADFORGE_TEST_CURL_ARGS"
cat > "$THREADFORGE_TEST_CURL_CONFIG"
printf '202'
`
	fakeCurlPath := filepath.Join(fakeBin, "curl")
	if err := os.WriteFile(fakeCurlPath, []byte(fakeCurl), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, filepath.Join(packDir, "sumo-upload-smoke-test.sh"), "--send-synthetic-event")
	token := `unit-token"\suffix`
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SUMO_TOKEN="+token,
		"THREADFORGE_TEST_CURL_ARGS="+argsPath,
		"THREADFORGE_TEST_CURL_CONFIG="+configPath,
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
	if strings.Contains(string(args), token) || !strings.Contains(string(args), "sample-event.jsonl") || strings.Contains(string(args), options.LogPath) {
		t.Fatalf("curl argv included a token or runtime path, or missed the synthetic sample: %s", args)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("mock curl did not receive configuration on stdin: %v", err)
	}
	if !strings.Contains(string(config), `url = "`+options.Endpoint+`"`) ||
		!strings.Contains(string(config), `header = "x-sumo-token: unit-token\"\\suffix"`) {
		t.Fatalf("curl config stdin did not contain escaped endpoint/token header: %s", config)
	}
	sample, err := os.ReadFile(filepath.Join(packDir, "sample-event.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytesTrimNewline(sample), &event); err != nil || len(event) != 3 {
		t.Fatalf("uploaded pack sample is not the expected synthetic metadata event: err=%v event=%#v", err, event)
	}
}

func sumoSmokeShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX script execution is not configured for native Windows paths")
	}
	for _, name := range []string{"sh", "bash"} {
		if shell, err := exec.LookPath(name); err == nil {
			return shell
		}
	}
	t.Skip("POSIX shell is unavailable")
	return ""
}

func TestInstallSumoPackRefusesOverwriteAndWritesPrivateFiles(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "sumo-pack")
	options := SumoForwardingOptions{LogPath: filepath.Join(parent, "runtime.jsonl"), Endpoint: "https://collectors.sumologic.com/receiver/v1/http", SecretsDir: filepath.Join(parent, "secret-values")}
	installed, err := InstallSumoForwardingPack(output, options)
	if err != nil {
		t.Fatal(err)
	}
	if installed != output {
		t.Fatalf("installed path = %q, want %q", installed, output)
	}
	for _, name := range []string{"README.md", "sample-event.jsonl", "sumo-upload-smoke-test.sh", "sumo-upload-smoke-test.ps1", "vector.toml"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing pack asset %s: %v", name, err)
		}
	}
	sample, err := os.ReadFile(filepath.Join(output, "sample-event.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	smokeScript, err := os.ReadFile(filepath.Join(output, "sumo-upload-smoke-test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(sample)
	if !strings.Contains(string(smokeScript), hex.EncodeToString(digest[:])) {
		t.Fatal("smoke script is not pinned to the exact sample-event.jsonl written in the pack")
	}
	powerShellSmokeScript, err := os.ReadFile(filepath.Join(output, "sumo-upload-smoke-test.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(powerShellSmokeScript), hex.EncodeToString(digest[:])) {
		t.Fatal("PowerShell smoke script is not pinned to the exact sample-event.jsonl written in the pack")
	}
	readme, err := os.ReadFile(filepath.Join(output, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"does not install or start Vector", "does not backfill", "--send-synthetic-event", filepath.Join(options.SecretsDir, sumoTokenSecretName), "metadata-only", options.Endpoint} {
		if !strings.Contains(string(readme), phrase) {
			t.Fatalf("README omits privacy or setup requirement %q", phrase)
		}
	}
	if _, err := InstallSumoForwardingPack(output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("existing output directory should be refused, got %v", err)
	}
}

func TestRenderSumoRejectsInvalidLogPath(t *testing.T) {
	for _, path := range []string{"", "relative/runtime.jsonl", "runtime\nsecret.jsonl"} {
		t.Run(path, func(t *testing.T) {
			if _, err := RenderSumoVectorConfig(SumoForwardingOptions{LogPath: path}); err == nil {
				t.Fatal("invalid runtime log path was accepted")
			}
		})
	}
}

func TestRenderSumoRejectsPresignedOrInvalidEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"",
		"https://collectors.sumologic.com/receiver/v1/http/bearer-secret",
		"http://collectors.sumologic.com/receiver/v1/http",
		"https://user:secret@collectors.sumologic.com/receiver/v1/http",
		"https://collectors.sumologic.com/receiver/v1/http?token=secret",
		"https://localhost/receiver/v1/http",
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := RenderSumoVectorConfig(SumoForwardingOptions{LogPath: filepath.Join(t.TempDir(), "runtime.jsonl"), Endpoint: endpoint}); err == nil {
				t.Fatal("invalid or bearer-credential endpoint was accepted")
			}
		})
	}
}
