package cutover

import "github.com/threadforge-ai/threadforge/pkg/strictjson"

const (
	maxCutoverJSONDepth  = strictjson.MaxDepth
	maxCutoverJSONTokens = strictjson.MaxTokens
)

// DecodeReportJSON decodes a top-level cutover report using the same strict
// duplicate-key, field, and resource checks as the report's evidence artifacts.
func DecodeReportJSON(data []byte) (Report, error) {
	var report Report
	if err := decodeUniqueJSON(data, &report); err != nil {
		return report, err
	}
	return report, nil
}

// decodeUniqueJSON applies the shared bounded strict JSON decoder to evidence.
func decodeUniqueJSON(data []byte, target any) error {
	return strictjson.Decode(data, target)
}
