package forwarding

import (
	"encoding/json"
	"time"
)

type metadataExportV1 struct {
	Timestamp         string `json:"timestamp"`
	Severity          string `json:"severity"`
	ThreadforgeExport struct {
		SchemaVersion string `json:"schema_version"`
		RawContent    bool   `json:"raw_content"`
	} `json:"threadforge_export"`
}

func renderSyntheticMetadataExport() ([]byte, error) {
	event := metadataExportV1{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Severity:  "info",
	}
	event.ThreadforgeExport.SchemaVersion = "threadforge.metadata-export.v1"
	event.ThreadforgeExport.RawContent = false
	data, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
