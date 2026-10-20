package forwarding

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const (
	elasticsearchMetadataPipeline = "threadforge-metadata"
	elasticsearchMetadataDataset  = "logs-threadforge.endpoint-default"
)

func elasticsearchPackAssets(vectorIndex string) ([]privatePackFile, error) {
	assets := []struct {
		name string
		data any
	}{
		{name: "ilm-policy.json", data: elasticsearchMetadataILMPolicy()},
		{name: "component-template-settings.json", data: elasticsearchMetadataSettingsComponent()},
		{name: "component-template-mappings.json", data: elasticsearchMetadataMappingsComponent()},
		{name: "index-template.json", data: elasticsearchMetadataIndexTemplate(vectorIndex)},
		{name: "data-stream-template.json", data: elasticsearchMetadataDataStreamTemplate()},
		{name: "ingest-pipeline.json", data: elasticsearchMetadataIngestPipeline()},
	}
	files := make([]privatePackFile, 0, len(assets)+1)
	for _, asset := range assets {
		data, err := json.MarshalIndent(asset.data, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode Elasticsearch asset %s: %w", asset.name, err)
		}
		files = append(files, privatePackFile{name: asset.name, content: append(data, '\n')})
	}
	kibana, err := elasticsearchMetadataKibanaAssets(vectorIndex)
	if err != nil {
		return nil, err
	}
	files = append(files, privatePackFile{name: "kibana-assets.ndjson", content: kibana})
	return files, nil
}

func elasticsearchMetadataILMPolicy() map[string]any {
	return map[string]any{
		"policy": map[string]any{
			"phases": map[string]any{
				"hot":    map[string]any{"actions": map[string]any{"rollover": map[string]any{"max_age": "30d", "max_primary_shard_size": "50gb"}}},
				"delete": map[string]any{"min_age": "90d", "actions": map[string]any{"delete": map[string]any{}}},
			},
		},
	}
}

func elasticsearchMetadataSettingsComponent() map[string]any {
	return map[string]any{
		"template": map[string]any{"settings": map[string]any{
			"index.refresh_interval":   "5s",
			"index.number_of_shards":   1,
			"index.number_of_replicas": 0,
			"index.default_pipeline":   elasticsearchMetadataPipeline,
		}},
		"_meta": map[string]any{"description": "Threadforge metadata-only index settings"},
	}
}

func elasticsearchMetadataMappingsComponent() map[string]any {
	return map[string]any{
		"template": map[string]any{"mappings": map[string]any{
			"dynamic": "strict",
			"properties": map[string]any{
				"@timestamp": map[string]any{"type": "date"},
				"severity":   map[string]any{"type": "keyword"},
				"threadforge_export": map[string]any{"properties": map[string]any{
					"schema_version": map[string]any{"type": "constant_keyword", "value": "threadforge.metadata-export.v1"},
					"raw_content":    map[string]any{"type": "boolean"},
				}},
			},
		}},
		"_meta": map[string]any{"description": "Strict allowlist for Threadforge metadata-only exports"},
	}
}

func elasticsearchMetadataIndexTemplate(index string) map[string]any {
	return map[string]any{
		"index_patterns": []string{index},
		"priority":       500,
		"composed_of":    []string{"threadforge-metadata-mappings", "threadforge-metadata-settings"},
		"template": map[string]any{"settings": map[string]any{
			"index.default_pipeline": elasticsearchMetadataPipeline,
		}},
		"_meta": map[string]any{"schema_version": "threadforge.metadata-export.v1", "purpose": "metadata-only forwarding"},
	}
}

func elasticsearchMetadataDataStreamTemplate() map[string]any {
	return map[string]any{
		"index_patterns": []string{"logs-threadforge.endpoint-*"},
		"priority":       501,
		"data_stream":    map[string]any{},
		"composed_of":    []string{"threadforge-metadata-mappings", "threadforge-metadata-settings"},
		"template": map[string]any{"settings": map[string]any{
			"index.lifecycle.name":   elasticsearchMetadataPipeline,
			"index.default_pipeline": elasticsearchMetadataPipeline,
		}},
		"_meta": map[string]any{"schema_version": "threadforge.metadata-export.v1", "purpose": "metadata-only forwarding"},
	}
}

func elasticsearchMetadataIngestPipeline() map[string]any {
	return map[string]any{
		"description": "Normalize Threadforge metadata export and reconstruct only its allowlisted fields",
		"processors": []any{
			map[string]any{"date": map[string]any{
				"field": "timestamp", "target_field": "@timestamp", "formats": []string{"ISO8601"},
			}},
			map[string]any{"remove": map[string]any{
				"description": "Keep only normalized timestamp, allowlisted severity, and the fixed privacy marker",
				"keep":        []string{"@timestamp", "severity", "threadforge_export"},
			}},
			map[string]any{"script": map[string]any{
				"description": "Clamp severity and rebuild the fixed privacy marker after source-field allowlisting",
				"lang":        "painless",
				"source": `def eventTime = ctx.get('@timestamp');
def severity = ctx.get('severity');
ctx.put('@timestamp', eventTime);
Set allowed = new HashSet();
allowed.add('info');
allowed.add('low');
allowed.add('medium');
allowed.add('high');
allowed.add('critical');
allowed.add('unknown');
if (severity instanceof String && allowed.contains(severity)) {
  ctx.put('severity', severity);
} else {
  ctx.put('severity', 'unknown');
}
Map exportMarker = new HashMap();
exportMarker.put('schema_version', 'threadforge.metadata-export.v1');
exportMarker.put('raw_content', false);
ctx.put('threadforge_export', exportMarker);`,
			}},
		},
		"_meta": map[string]any{"schema_version": "threadforge.metadata-export.v1", "raw_content_forwarded": false},
	}
}

func elasticsearchMetadataKibanaAssets(vectorIndex string) ([]byte, error) {
	views := []struct {
		id    string
		title string
		name  string
	}{
		{id: "threadforge-metadata-index", title: vectorIndex, name: "Threadforge Metadata Events"},
		{id: "threadforge-metadata-stream", title: "logs-threadforge.endpoint-*", name: "Threadforge Metadata Data Stream"},
	}
	var output bytes.Buffer
	for _, view := range views {
		objects := []map[string]any{
			{
				"type": "index-pattern", "id": view.id,
				"attributes": map[string]any{"title": view.title, "timeFieldName": "@timestamp", "name": view.name},
				"references": []any{}, "coreMigrationVersion": "8.8.0", "typeMigrationVersion": "8.0.0",
			},
			{
				"type": "search", "id": view.id + "-search",
				"attributes": map[string]any{
					"title": view.name, "description": "Sanitized Threadforge event metadata only",
					"columns":               []string{"@timestamp", "severity", "threadforge_export.schema_version", "threadforge_export.raw_content"},
					"sort":                  [][]string{{"@timestamp", "desc"}},
					"kibanaSavedObjectMeta": map[string]any{"searchSourceJSON": fmt.Sprintf(`{"query":{"language":"kuery","query":"threadforge_export.schema_version: \\\"threadforge.metadata-export.v1\\\""},"filter":[],"indexRefName":"kibanaSavedObjectMeta.searchSourceJSON.index"}`)},
				},
				"references":           []any{map[string]any{"name": "kibanaSavedObjectMeta.searchSourceJSON.index", "type": "index-pattern", "id": view.id}},
				"coreMigrationVersion": "8.8.0", "typeMigrationVersion": "8.0.0",
			},
		}
		for _, object := range objects {
			data, err := json.Marshal(object)
			if err != nil {
				return nil, fmt.Errorf("encode Threadforge Kibana asset: %w", err)
			}
			output.Write(data)
			output.WriteByte('\n')
		}
	}
	return output.Bytes(), nil
}

func elasticsearchAssetInstallInstructions() string {
	return `

## Manual cluster asset installation

The files in this pack are inert. After review, use an authorized HTTPS
Elasticsearch/Kibana administration client configured through your approved
external credential mechanism. The following commands are examples; Threadforge
does not execute them or store cluster credentials.

~~~sh
curl -fsS -X PUT "$ES_HOSTS/_ilm/policy/threadforge-metadata" \
  -H 'Content-Type: application/json' --data-binary @ilm-policy.json
curl -fsS -X PUT "$ES_HOSTS/_ingest/pipeline/threadforge-metadata" \
  -H 'Content-Type: application/json' --data-binary @ingest-pipeline.json
curl -fsS -X PUT "$ES_HOSTS/_component_template/threadforge-metadata-settings" \
  -H 'Content-Type: application/json' --data-binary @component-template-settings.json
curl -fsS -X PUT "$ES_HOSTS/_component_template/threadforge-metadata-mappings" \
  -H 'Content-Type: application/json' --data-binary @component-template-mappings.json
curl -fsS -X PUT "$ES_HOSTS/_index_template/threadforge-vector-index" \
  -H 'Content-Type: application/json' --data-binary @index-template.json
curl -fsS -X PUT "$ES_HOSTS/_index_template/threadforge-data-stream" \
  -H 'Content-Type: application/json' --data-binary @data-stream-template.json
curl -fsS -X POST "$KIBANA_HOST/api/saved_objects/_import?overwrite=false" \
  -H 'kbn-xsrf: threadforge' --form file=@kibana-assets.ndjson
~~~

Review privileges, retention, TLS trust, Kibana object conflicts, and the
destination before use. Vector's direct index uses index-template.json; the
Filebeat and standalone Agent paths use the logs-threadforge.endpoint-default
data stream and its lifecycle policy.`
}
