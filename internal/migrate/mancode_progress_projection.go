package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const mancodeProgressProjectionSchema = "threadforge.mancode-progress-projection.v1"

const (
	maxMancodeProgressTasks  = 10000
	maxMancodeProgressReason = 4096
)

type mancodeProgressProjection struct {
	SchemaVersion       string                          `json:"schema_version"`
	SourceSchemaVersion int                             `json:"source_schema_version"`
	ProjectionStatus    string                          `json:"projection_status"`
	Authority           string                          `json:"authority"`
	TaskIDsMapped       bool                            `json:"task_ids_mapped"`
	HumanReviewRequired bool                            `json:"human_review_required"`
	ExecutionAuthorized bool                            `json:"execution_authorized"`
	Tasks               []mancodeProgressProjectionTask `json:"tasks"`
}

type mancodeProgressProjectionTask struct {
	TaskID string  `json:"task_id"`
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

func isMancodeProgressHTMLPath(relative string) bool {
	return strings.EqualFold(filepath.Base(filepath.Clean(relative)), "项目进度.html")
}

func normalizeMancodeProgressHTML(data []byte) (json.RawMessage, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("Mancode progress page is not valid UTF-8")
	}
	document, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("Mancode progress page is malformed HTML")
	}

	var matches []*html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		for _, attribute := range node.Attr {
			if strings.EqualFold(attribute.Key, "id") && attribute.Val == "mancode-progress-data" {
				matches = append(matches, node)
				break
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if len(matches) > 1 {
		return nil, errors.New("Mancode progress page contains duplicate data identifiers")
	}

	projection := mancodeProgressProjection{
		SchemaVersion: mancodeProgressProjectionSchema, SourceSchemaVersion: 1,
		ProjectionStatus: "absent_manual_sync", Authority: "local_pending_review",
		TaskIDsMapped: false, HumanReviewRequired: true, ExecutionAuthorized: false,
		Tasks: []mancodeProgressProjectionTask{},
	}
	if len(matches) == 1 {
		node := matches[0]
		if node.Type != html.ElementNode || !strings.EqualFold(node.Data, "script") {
			return nil, errors.New("Mancode progress data identifier is not on a script element")
		}
		var typeValue string
		idCount, typeCount := 0, 0
		for _, attribute := range node.Attr {
			switch strings.ToLower(attribute.Key) {
			case "id":
				idCount++
			case "type":
				typeCount++
				typeValue = attribute.Val
			}
		}
		if idCount != 1 || typeCount != 1 || typeValue != "application/json" {
			return nil, errors.New("Mancode progress script has an invalid type or duplicate attributes")
		}
		var scriptText string
		textNodes := 0
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.TextNode {
				return nil, errors.New("Mancode progress script contains non-text content")
			}
			textNodes++
			scriptText += child.Data
		}
		if textNodes != 1 {
			return nil, errors.New("Mancode progress script has no unique JSON body")
		}
		value, err := decodeStrictMancodeProgressJSON([]byte(strings.TrimSpace(scriptText)))
		if err != nil {
			return nil, err
		}
		tasks, err := validateMancodeProgressTasks(value)
		if err != nil {
			return nil, err
		}
		projection.ProjectionStatus = "present"
		projection.Tasks = tasks
	}

	encoded, err := json.Marshal(projection)
	if err != nil {
		return nil, errors.New("encode Mancode progress projection")
	}
	return encoded, nil
}

func decodeStrictMancodeProgressJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	value, err := decodeMancodeProgressJSONValue(decoder, 0, &nodes)
	if err != nil {
		return nil, errors.New("Mancode progress data is invalid JSON")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("Mancode progress data has trailing JSON")
	}
	return value, nil
}

func decodeMancodeProgressJSONValue(decoder *json.Decoder, depth int, nodes *int) (any, error) {
	if depth > 8 {
		return nil, errors.New("Mancode progress data is too deeply nested")
	}
	*nodes = *nodes + 1
	if *nodes > maxMancodeProgressTasks*8+32 {
		return nil, errors.New("Mancode progress data exceeds the node limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("invalid object key")
				}
				if _, duplicate := object[key]; duplicate {
					return nil, errors.New("duplicate object key")
				}
				item, err := decodeMancodeProgressJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				object[key] = item
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return nil, errors.New("invalid object end")
			}
			return object, nil
		case '[':
			items := make([]any, 0)
			for decoder.More() {
				item, err := decodeMancodeProgressJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return nil, errors.New("invalid array end")
			}
			return items, nil
		default:
			return nil, errors.New("unexpected JSON delimiter")
		}
	case string, bool, json.Number, nil:
		return value, nil
	default:
		return nil, errors.New("unsupported JSON value")
	}
}

func validateMancodeProgressTasks(value any) ([]mancodeProgressProjectionTask, error) {
	root, ok := value.(map[string]any)
	if !ok || !mancodeHasOnlyKeys(root, []string{"schemaVersion", "tasks"}) ||
		!mancodeHasAllKeys(root, []string{"schemaVersion", "tasks"}) || mancodeInteger(root["schemaVersion"]) != 1 {
		return nil, errors.New("Mancode progress data does not match schema version 1")
	}
	values, ok := root["tasks"].([]any)
	if !ok || len(values) > maxMancodeProgressTasks {
		return nil, errors.New("Mancode progress task list is invalid or too large")
	}
	tasks := make([]mancodeProgressProjectionTask, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok || !mancodeHasOnlyKeys(object, []string{"taskId", "status", "reason"}) ||
			!mancodeHasAllKeys(object, []string{"taskId", "status", "reason"}) {
			return nil, errors.New("Mancode progress task has an invalid shape")
		}
		taskID, taskIDOK := object["taskId"].(string)
		status, statusOK := object["status"].(string)
		if !taskIDOK || !validMancodeProgressText(taskID, 512, false) || !statusOK ||
			!validMancodeProgressStatus(status) {
			return nil, errors.New("Mancode progress task identity or status is invalid")
		}
		if _, duplicate := seen[taskID]; duplicate {
			return nil, errors.New("Mancode progress task identifiers are not unique")
		}
		seen[taskID] = struct{}{}
		var reason *string
		if object["reason"] != nil {
			text, ok := object["reason"].(string)
			if !ok || !validMancodeProgressText(text, maxMancodeProgressReason, true) {
				return nil, errors.New("Mancode progress task reason is invalid")
			}
			reason = &text
		}
		tasks = append(tasks, mancodeProgressProjectionTask{TaskID: taskID, Status: status, Reason: reason})
	}
	return tasks, nil
}

func validMancodeProgressStatus(value string) bool {
	switch value {
	case "未完成", "进行中", "待审核", "已完成", "阻塞":
		return true
	default:
		return false
	}
}

func validMancodeProgressText(value string, limit int, allowNewlines bool) bool {
	if len(value) > limit || !utf8.ValidString(value) || (!allowNewlines && strings.TrimSpace(value) != value) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) && !(allowNewlines && (char == '\n' || char == '\r' || char == '\t')) {
			return false
		}
	}
	return allowNewlines || strings.TrimSpace(value) != ""
}
