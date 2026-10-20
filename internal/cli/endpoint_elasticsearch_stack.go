package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/forwarding"
)

const elasticsearchLocalStackProject = "threadforge-elasticsearch-local"

func runEndpointElasticsearchStack(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "up" && args[0] != "down" && args[0] != "status") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint elasticsearch <up|down|status> [--pack-dir <dir>] [--docker-bin docker] [--elasticsearch-port 9200] [--kibana-port 5601] [--allow-image-pulls (up only)]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint elasticsearch "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	packDir := fs.String("pack-dir", "threadforge-elasticsearch-pack", "generated Threadforge Elasticsearch pack directory")
	dockerBin := fs.String("docker-bin", "docker", "Docker CLI executable")
	elasticsearchPort := fs.Int("elasticsearch-port", 9200, "loopback host port for local Elasticsearch")
	kibanaPort := fs.Int("kibana-port", 5601, "loopback host port for local Kibana")
	allowImagePulls := fs.Bool("allow-image-pulls", false, "allow Docker to pull pinned local-test images from their registries")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch stack command does not accept positional arguments")
		return 2
	}
	if operation == "up" && !*allowImagePulls {
		_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch up requires --allow-image-pulls; missing pinned images may be downloaded from Docker registries")
		return 2
	}
	if operation != "up" && *allowImagePulls {
		_, _ = fmt.Fprintln(stderr, "--allow-image-pulls is accepted only by endpoint elasticsearch up")
		return 2
	}
	if *elasticsearchPort < 1024 || *elasticsearchPort > 65535 || *kibanaPort < 1024 || *kibanaPort > 65535 || *elasticsearchPort == *kibanaPort {
		_, _ = fmt.Fprintln(stderr, "local Elasticsearch and Kibana ports must be distinct integers in 1024..65535")
		return 2
	}
	absolutePackDir, err := filepath.Abs(strings.TrimSpace(*packDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve Elasticsearch pack directory:", err)
		return 2
	}
	validatePack := forwarding.ValidateElasticsearchLocalStackPack
	if operation != "up" {
		validatePack = forwarding.ValidateElasticsearchLocalStackControlPack
	}
	if err := validatePack(absolutePackDir); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	dockerPath, err := exec.LookPath(strings.TrimSpace(*dockerBin))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "find Docker CLI:", err)
		return 1
	}
	contextName, err := resolveLocalDockerContext(dockerPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	baseArgs := []string{
		"--context", contextName,
		"compose",
		"--project-name", elasticsearchLocalStackProjectForPackDir(absolutePackDir),
		"--project-directory", absolutePackDir,
		"--env-file", "docker-compose.env",
		"--file", "docker-compose.yml",
	}
	env := localElasticsearchComposeEnv(*elasticsearchPort, *kibanaPort)
	if err := runDockerCompose(dockerPath, baseArgs, absolutePackDir, env, []string{"config", "--quiet"}, stdout, stderr, 30*time.Second); err != nil {
		_, _ = fmt.Fprintln(stderr, "validate generated local Compose configuration:", err)
		return 1
	}
	switch operation {
	case "up":
		if err := runDockerCompose(dockerPath, baseArgs, absolutePackDir, env, []string{"up", "--detach", "--wait", "--wait-timeout", "600", "--pull", "missing"}, stdout, stderr, 15*time.Minute); err != nil {
			_, _ = fmt.Fprintln(stderr, "start local Elasticsearch stack:", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Local synthetic-only Elasticsearch stack is up at http://127.0.0.1:%d and http://127.0.0.1:%d. Security is disabled inside the loopback-only test stack; no Threadforge trace was mounted or sent. Stop it with endpoint elasticsearch down; the named data volume is preserved.\n", *elasticsearchPort, *kibanaPort)
	case "down":
		if err := runDockerCompose(dockerPath, baseArgs, absolutePackDir, env, []string{"down"}, stdout, stderr, 90*time.Second); err != nil {
			_, _ = fmt.Fprintln(stderr, "stop local Elasticsearch stack:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "Local Elasticsearch stack stopped; its named data volume was preserved.")
	case "status":
		if err := runDockerCompose(dockerPath, baseArgs, absolutePackDir, env, []string{"ps", "--all"}, stdout, stderr, 30*time.Second); err != nil {
			_, _ = fmt.Fprintln(stderr, "read local Elasticsearch stack status:", err)
			return 1
		}
	}
	return 0
}

func elasticsearchLocalStackProjectForPackDir(packDir string) string {
	cleanPath := filepath.Clean(packDir)
	defaultPath, err := filepath.Abs("threadforge-elasticsearch-pack")
	if err == nil && sameElasticsearchPackPath(cleanPath, filepath.Clean(defaultPath)) {
		// Keep the original project identity for the documented default path so
		// existing `up`/`down` stacks continue to be managed across upgrades.
		return elasticsearchLocalStackProject
	}
	identity := cleanPath
	if runtime.GOOS == "windows" {
		identity = strings.ToLower(identity)
	}
	digest := sha256.Sum256([]byte(identity))
	return "threadforge-elasticsearch-" + hex.EncodeToString(digest[:8])
}

func sameElasticsearchPackPath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func resolveLocalDockerContext(dockerBin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	show := exec.CommandContext(ctx, dockerBin, "context", "show")
	nameBytes, err := show.Output()
	if err != nil {
		return "", fmt.Errorf("resolve current Docker context: %w", err)
	}
	name := strings.TrimSpace(string(nameBytes))
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "\x00\r\n\t") {
		return "", errors.New("Docker returned an invalid current context name")
	}
	inspect := exec.CommandContext(ctx, dockerBin, "context", "inspect", "--format", "{{json .Endpoints.docker}}", name)
	endpointBytes, err := inspect.Output()
	if err != nil {
		return "", fmt.Errorf("inspect Docker context endpoint: %w", err)
	}
	var endpoint struct {
		Host string `json:"Host"`
	}
	if err := json.Unmarshal(endpointBytes, &endpoint); err != nil {
		return "", fmt.Errorf("decode Docker context endpoint: %w", err)
	}
	host := strings.ToLower(strings.TrimSpace(endpoint.Host))
	if !isLocalDockerEndpoint(host) {
		return "", errors.New("local Elasticsearch stack refuses non-local Docker contexts; select a Docker Engine using a local npipe or Unix socket")
	}
	return name, nil
}

func isLocalDockerEndpoint(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.HasPrefix(host, "npipe://") || strings.HasPrefix(host, "unix://")
}

func localElasticsearchComposeEnv(elasticsearchPort, kibanaPort int) []string {
	blocked := map[string]bool{
		"DOCKER_HOST": true, "DOCKER_CONTEXT": true, "DOCKER_TLS_VERIFY": true, "DOCKER_CERT_PATH": true,
		"COMPOSE_FILE": true, "COMPOSE_PROJECT_NAME": true, "COMPOSE_PROFILES": true, "COMPOSE_ENV_FILES": true,
		"COMPOSE_PATH_SEPARATOR": true, "COMPOSE_DISABLE_ENV_FILE": true,
	}
	values := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok || blocked[strings.ToUpper(key)] {
			continue
		}
		values[key] = value
	}
	values["THREADFORGE_ELASTICSEARCH_PORT"] = strconv.Itoa(elasticsearchPort)
	values["THREADFORGE_KIBANA_PORT"] = strconv.Itoa(kibanaPort)
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func runDockerCompose(dockerBin string, baseArgs []string, workingDir string, env []string, composeArgs []string, stdout, stderr io.Writer, timeout time.Duration) error {
	args := append(append([]string(nil), baseArgs...), composeArgs...)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, dockerBin, args...)
	cmd.Dir = workingDir
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("Docker Compose exceeded %s", timeout)
		}
		return err
	}
	return nil
}
