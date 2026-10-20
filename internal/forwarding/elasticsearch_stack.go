package forwarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const elasticsearchLocalCompose = `services:
  elasticsearch:
    image: docker.elastic.co/elasticsearch/elasticsearch:9.5.4
    environment:
      discovery.type: single-node
      xpack.security.enabled: "false"
      ES_JAVA_OPTS: -Xms1g -Xmx1g
    ports:
      - "127.0.0.1:${THREADFORGE_ELASTICSEARCH_PORT:-9200}:9200"
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS 'http://localhost:9200/_cluster/health?wait_for_status=yellow&timeout=1s' >/dev/null"]
      interval: 10s
      timeout: 5s
      retries: 30
    volumes:
      - threadforge-elasticsearch-data:/usr/share/elasticsearch/data
    networks:
      - threadforge-local

  kibana:
    image: docker.elastic.co/kibana/kibana:9.5.4
    environment:
      ELASTICSEARCH_HOSTS: http://elasticsearch:9200
    ports:
      - "127.0.0.1:${THREADFORGE_KIBANA_PORT:-5601}:5601"
    depends_on:
      elasticsearch:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS http://localhost:5601/api/status >/dev/null"]
      interval: 10s
      timeout: 5s
      retries: 30
    networks:
      - threadforge-local

  setup:
    image: curlimages/curl:8.10.1
    user: "0:0"
    working_dir: /pack
    volumes:
      - ./ilm-policy.json:/pack/ilm-policy.json:ro
      - ./component-template-settings.json:/pack/component-template-settings.json:ro
      - ./component-template-mappings.json:/pack/component-template-mappings.json:ro
      - ./index-template.json:/pack/index-template.json:ro
      - ./data-stream-template.json:/pack/data-stream-template.json:ro
      - ./ingest-pipeline.json:/pack/ingest-pipeline.json:ro
      - ./kibana-assets.ndjson:/pack/kibana-assets.ndjson:ro
    depends_on:
      elasticsearch:
        condition: service_healthy
      kibana:
        condition: service_healthy
    command:
      - sh
      - -eu
      - -c
      - |
        curl -fsS -X PUT http://elasticsearch:9200/_ilm/policy/threadforge-metadata -H 'Content-Type: application/json' --data-binary @ilm-policy.json
        curl -fsS -X PUT http://elasticsearch:9200/_ingest/pipeline/threadforge-metadata -H 'Content-Type: application/json' --data-binary @ingest-pipeline.json
        curl -fsS -X PUT http://elasticsearch:9200/_component_template/threadforge-metadata-settings -H 'Content-Type: application/json' --data-binary @component-template-settings.json
        curl -fsS -X PUT http://elasticsearch:9200/_component_template/threadforge-metadata-mappings -H 'Content-Type: application/json' --data-binary @component-template-mappings.json
        curl -fsS -X PUT http://elasticsearch:9200/_index_template/threadforge-vector-index -H 'Content-Type: application/json' --data-binary @index-template.json
        curl -fsS -X PUT http://elasticsearch:9200/_index_template/threadforge-data-stream -H 'Content-Type: application/json' --data-binary @data-stream-template.json
        curl -fsS -X POST 'http://kibana:5601/api/saved_objects/_import?overwrite=true' -H 'kbn-xsrf: threadforge' --form file=@kibana-assets.ndjson
    networks:
      - threadforge-local

  filebeat:
    image: docker.elastic.co/beats/filebeat:9.5.4
    user: "0:0"
    command: ["filebeat", "-e", "--strict.perms=false"]
    volumes:
      - ./filebeat-local.yml:/usr/share/filebeat/filebeat.yml:ro
      - ./local-sample-event.jsonl:/var/lib/threadforge/sample.jsonl:ro
      - threadforge-filebeat-data:/usr/share/filebeat/data
    depends_on:
      elasticsearch:
        condition: service_healthy
      setup:
        condition: service_completed_successfully
    networks:
      - threadforge-local

networks:
  threadforge-local:
    internal: true

volumes:
  threadforge-elasticsearch-data:
  threadforge-filebeat-data:
`

const elasticsearchLocalFilebeatConfig = `filebeat.inputs:
  - type: filestream
    id: threadforge-local-synthetic-validation
    enabled: true
    paths:
      - /var/lib/threadforge/sample.jsonl
    parsers:
      - ndjson:
          keys_under_root: true
          add_error_key: true
          overwrite_keys: true
    fields_under_root: true
    fields:
      data_stream.type: logs
      data_stream.dataset: threadforge.endpoint
      data_stream.namespace: default

output.elasticsearch:
  hosts: ["http://elasticsearch:9200"]
  index: "logs-threadforge.endpoint-default"

setup.template.enabled: false
setup.ilm.enabled: false
`

func elasticsearchLocalStackConfigFiles() []privatePackFile {
	return []privatePackFile{
		{name: "docker-compose.yml", content: []byte(elasticsearchLocalCompose)},
		{name: "docker-compose.env", content: nil},
		{name: "filebeat-local.yml", content: []byte(elasticsearchLocalFilebeatConfig)},
	}
}

func elasticsearchLocalStackFiles(sample []byte) []privatePackFile {
	files := elasticsearchLocalStackConfigFiles()
	files = append(files, privatePackFile{name: "local-sample-event.jsonl", content: append([]byte(nil), sample...)})
	return files
}

func elasticsearchLocalStackInstructions() string {
	return `

## Local Elasticsearch/Kibana validation stack

This optional test stack uses pinned Elasticsearch/Kibana/Filebeat images,
disables Elasticsearch security, binds its HTTP ports to loopback, and places
containers on an internal-only network. Its Filebeat input reads only the
synthetic local-sample-event.jsonl fixture; it never mounts or reads the raw
Threadforge runtime log or the user's metadata export. It is for local tests,
not production use.

Starting the stack may pull the pinned images from Docker registries, so the
command requires an explicit acknowledgement:

~~~powershell
threadforge endpoint elasticsearch up --pack-dir .\\threadforge-elasticsearch-pack --allow-image-pulls
threadforge endpoint elasticsearch status --pack-dir .\\threadforge-elasticsearch-pack
threadforge endpoint elasticsearch down --pack-dir .\\threadforge-elasticsearch-pack
~~~

The CLI validates the generated Compose and mounted assets before use and
refuses non-local Docker contexts. Reimporting Kibana views overwrites only the
fixed Threadforge-owned saved-object IDs in this local test cluster.
Elasticsearch is available at
http://127.0.0.1:9200 and Kibana at http://127.0.0.1:5601 by default; pass
--elasticsearch-port and --kibana-port to choose different loopback ports.
Stopping the stack preserves its named Elasticsearch data volume.`
}

// ValidateElasticsearchLocalStackPack ensures the Compose file and every file
// it mounts are exactly the generated local-only stack. This prevents the CLI
// from executing a user-edited Compose project or mounting a raw trace.
func ValidateElasticsearchLocalStackPack(packDir string) error {
	abs, err := validateElasticsearchPackDirectory(packDir)
	if err != nil {
		return err
	}
	indexTemplateBytes, err := readElasticsearchPackFile(abs, "index-template.json")
	if err != nil {
		return err
	}
	var indexTemplate struct {
		IndexPatterns []string `json:"index_patterns"`
	}
	if err := json.Unmarshal(indexTemplateBytes, &indexTemplate); err != nil || len(indexTemplate.IndexPatterns) != 1 {
		return errors.New("Elasticsearch pack index-template.json is malformed")
	}
	index := indexTemplate.IndexPatterns[0]
	if !elasticsearchIndexPattern.MatchString(index) || strings.Contains(index, "..") {
		return errors.New("Elasticsearch pack index template has an unsafe index target")
	}

	assets, err := elasticsearchPackAssets(index)
	if err != nil {
		return fmt.Errorf("rebuild expected Elasticsearch pack assets: %w", err)
	}
	expected := append(elasticsearchLocalStackConfigFiles(), assets...)
	for _, file := range expected {
		actual, err := readElasticsearchPackFile(abs, file.name)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, file.content) {
			return fmt.Errorf("Elasticsearch local stack asset %s was modified; regenerate the pack before starting Docker Compose", file.name)
		}
	}
	sample, err := readElasticsearchPackFile(abs, "local-sample-event.jsonl")
	if err != nil {
		return err
	}
	if err := validateElasticsearchLocalSample(sample); err != nil {
		return err
	}
	return nil
}

// ValidateElasticsearchLocalStackControlPack allows status/down to use an
// intact local Compose definition from an older Threadforge pack version.
// These operations never start services; keeping the Compose file exact and
// the env file empty prevents it from selecting an alternate project or
// expanding its configuration while still allowing old stacks to be stopped.
func ValidateElasticsearchLocalStackControlPack(packDir string) error {
	abs, err := validateElasticsearchPackDirectory(packDir)
	if err != nil {
		return err
	}
	compose, err := readElasticsearchPackFile(abs, "docker-compose.yml")
	if err != nil {
		return err
	}
	if !bytes.Equal(compose, []byte(elasticsearchLocalCompose)) {
		return errors.New("Elasticsearch local stack Compose file was modified; refusing status or teardown")
	}
	env, err := readElasticsearchPackFile(abs, "docker-compose.env")
	if err != nil {
		return err
	}
	if len(env) != 0 {
		return errors.New("Elasticsearch local stack env file must remain empty for status or teardown")
	}
	return nil
}

func validateElasticsearchPackDirectory(packDir string) (string, error) {
	packDir = strings.TrimSpace(packDir)
	if packDir == "" || len(packDir) > 4096 || strings.ContainsAny(packDir, "\x00\r\n\t") {
		return "", errors.New("Elasticsearch pack directory must be nonempty, bounded, and contain no control separators")
	}
	abs, err := filepath.Abs(packDir)
	if err != nil {
		return "", fmt.Errorf("resolve Elasticsearch pack directory: %w", err)
	}
	abs = filepath.Clean(abs)
	if err := validateRealDirectoryChain(abs); err != nil {
		return "", fmt.Errorf("validate Elasticsearch pack directory: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("inspect Elasticsearch pack directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("Elasticsearch pack path must be a real directory, not a link")
	}
	return abs, nil
}

func validateElasticsearchLocalSample(data []byte) error {
	if len(data) == 0 || len(data) > 64<<10 {
		return errors.New("local Elasticsearch sample must be a nonempty JSONL record no larger than 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var sample struct {
		Timestamp string `json:"timestamp"`
		Severity  string `json:"severity"`
		Export    struct {
			SchemaVersion string `json:"schema_version"`
			RawContent    *bool  `json:"raw_content"`
		} `json:"threadforge_export"`
	}
	if err := decoder.Decode(&sample); err != nil {
		return fmt.Errorf("decode local Elasticsearch synthetic sample: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("local Elasticsearch sample must contain exactly one JSON object")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode local Elasticsearch sample trailer: %w", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, sample.Timestamp); err != nil {
		return errors.New("local Elasticsearch sample timestamp must be RFC3339")
	}
	if sample.Severity != "info" || sample.Export.SchemaVersion != "threadforge.metadata-export.v1" || sample.Export.RawContent == nil || *sample.Export.RawContent {
		return errors.New("local Elasticsearch sample must be the fixed metadata-only fixture")
	}
	return nil
}

func readElasticsearchPackFile(packDir, name string) ([]byte, error) {
	path := filepath.Join(packDir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect Elasticsearch pack file %s: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("Elasticsearch pack file %s must be a regular file no larger than 1 MiB", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Elasticsearch pack file %s: %w", name, err)
	}
	return data, nil
}
