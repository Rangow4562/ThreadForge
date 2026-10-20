package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const rrsiProposalSummarySchema = "threadforge.rrsi-proposal-summary.v1"

const (
	maxRRSIProposalEdits = 80
	maxRRSIProposalLog   = 256
)

var rrsiProposalComponents = map[string]struct{}{
	"prompt": {}, "control_flow": {}, "config": {}, "output_plumbing": {},
	"context_mgmt": {}, "client_tool": {}, "skill": {}, "memory": {}, "subagent": {},
}

type rrsiProposalSummary struct {
	SchemaVersion       string   `json:"schema_version"`
	Domain              string   `json:"domain"`
	ProposalStatus      string   `json:"proposal_status"`
	FileEditCount       int      `json:"file_edit_count"`
	DeclaredEditCount   int      `json:"declared_edit_count"`
	EditComponents      []string `json:"edit_components"`
	Authority           string   `json:"authority"`
	HumanReviewRequired bool     `json:"human_review_required"`
	SelectionAccepted   bool     `json:"selection_accepted"`
	ExecutionAuthorized bool     `json:"execution_authorized"`
	OmittedFields       []string `json:"omitted_fields"`
}

func isRRSIProposalPath(relative string) bool {
	if _, ok := rrsiDomain(relative); !ok {
		return false
	}
	tail, ok := rrsiRunTail(relative)
	if !ok || len(tail) != 3 || !rrsiRoundDirectory(tail[0]) ||
		!rrsiProposalVariant(tail[1]) || !rrsiProposalFilename(tail[2]) {
		return false
	}
	return true
}

func rrsiProposalVariant(value string) bool {
	return len(value) == 1 && value[0] >= 'a' && value[0] <= 'h'
}

func rrsiRoundDirectory(value string) bool {
	if len(value) < 2 || value[0] != 'r' {
		return false
	}
	for _, char := range value[1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func rrsiProposalFilename(value string) bool {
	if value == "proposal.json" {
		return true
	}
	if !strings.HasPrefix(value, "proposal_r") || !strings.HasSuffix(value, ".json") {
		return false
	}
	attempt := strings.TrimSuffix(strings.TrimPrefix(value, "proposal_r"), ".json")
	if attempt == "" || len(attempt) > 9 || attempt[0] == '0' {
		return false
	}
	parsed, err := strconv.Atoi(attempt)
	return err == nil && parsed > 0
}

func scanRRSIProposalFile(relative string, data []byte) scannedFile {
	file := scannedFile{entry: FileEntry{
		Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data)), Status: "ready",
	}}
	file.entry.RecordCount = 1
	value, err := decodeStrictRRSIProposalJSON(data)
	var payload json.RawMessage
	if err == nil {
		domain, _ := rrsiDomain(relative)
		payload, err = normalizeRRSIProposal(domain, value)
	}
	if err != nil {
		file.entry.RejectedCount = 1
		file.entry.Issue = safeIssue(err)
	} else if privacy.ContainsCredential(payload) {
		file.entry.ExcludedCount = 1
	} else {
		file.records = append(file.records, Record{
			Key: relative + "#000001", Type: "evolution_proposal", SourcePath: relative,
			ContentSHA256: digest(payload), Scope: "local_pending_review", Payload: payload,
		})
	}
	finalizeScannedFile(&file)
	return file
}

func decodeStrictRRSIProposalJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("RRSI proposal is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	nodes := 0
	value, err := decodeStrictRRSIJSONValue(decoder, 0, &nodes)
	if err != nil {
		return nil, errors.New("RRSI proposal JSON is malformed or ambiguous")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("RRSI proposal JSON has trailing data")
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("RRSI proposal must be a JSON object")
	}
	return value, nil
}

func decodeStrictRRSIJSONValue(decoder *json.Decoder, depth int, nodes *int) (any, error) {
	if depth > 32 {
		return nil, errors.New("RRSI proposal JSON is too deeply nested")
	}
	*nodes = *nodes + 1
	if *nodes > 250000 {
		return nil, errors.New("RRSI proposal JSON exceeds the structural limit")
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
					return nil, errors.New("RRSI proposal JSON object key is invalid")
				}
				if _, duplicate := object[key]; duplicate {
					return nil, errors.New("RRSI proposal JSON has duplicate object keys")
				}
				item, err := decodeStrictRRSIJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				object[key] = item
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return nil, errors.New("RRSI proposal JSON object is unterminated")
			}
			return object, nil
		case '[':
			items := make([]any, 0)
			for decoder.More() {
				item, err := decodeStrictRRSIJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return nil, errors.New("RRSI proposal JSON array is unterminated")
			}
			return items, nil
		default:
			return nil, errors.New("RRSI proposal JSON delimiter is invalid")
		}
	case string, float64, bool, nil:
		return value, nil
	default:
		return nil, errors.New("RRSI proposal JSON value is unsupported")
	}
}

func normalizeRRSIProposal(domain string, value any) (json.RawMessage, error) {
	if domain != "coding" && domain != "workspace" && domain != "eng" {
		return nil, errors.New("RRSI proposal is outside a supported domain")
	}
	source, ok := rrsiObject(value)
	allowed := []string{"status", "summary", "edits", "mechanism", "targets_mode", "n_edits", "log", "reason"}
	if !ok || !mancodeHasOnlyKeys(source, allowed) ||
		!mancodeHasAllKeys(source, []string{"status", "edits", "n_edits", "log"}) {
		return nil, errors.New("RRSI proposal fields do not match the supported producer contract")
	}
	status, ok := source["status"].(string)
	if !ok || (status != "done" && status != "abort" && status != "max_turns") {
		return nil, errors.New("RRSI proposal status is invalid")
	}
	fileEditCount, ok := rrsiInteger(source["n_edits"])
	if !ok || fileEditCount < 0 || fileEditCount > maxRRSIProposalEdits {
		return nil, errors.New("RRSI proposal file-edit count is invalid")
	}
	if _, ok := source["log"].([]any); !ok || len(source["log"].([]any)) > maxRRSIProposalLog {
		return nil, errors.New("RRSI proposal interaction log is invalid or too large")
	}
	for _, key := range []string{"summary", "mechanism", "targets_mode", "reason"} {
		if field, exists := source[key]; exists && field != nil {
			text, ok := field.(string)
			if !ok || len(text) > 8192 || strings.ContainsRune(text, '\x00') {
				return nil, errors.New("RRSI proposal text metadata is invalid")
			}
		}
	}
	rawEdits, ok := source["edits"].([]any)
	if !ok || len(rawEdits) > maxRRSIProposalEdits || (status != "done" && len(rawEdits) != 0) {
		return nil, errors.New("RRSI proposal edit list is invalid")
	}
	components := make([]string, 0, len(rawEdits))
	for _, raw := range rawEdits {
		edit, ok := rrsiObject(raw)
		editAllowed := []string{
			"id", "component", "hypothesis", "targets_mode", "why_not_lower_lever", "trigger_condition",
			"predicted_affected", "retroactive_check", "regression_risk", "mechanism",
		}
		editRequired := []string{"id", "component", "hypothesis", "targets_mode", "predicted_affected", "retroactive_check"}
		if !ok || !mancodeHasOnlyKeys(edit, editAllowed) || !mancodeHasAllKeys(edit, editRequired) {
			return nil, errors.New("RRSI proposal edit does not match the supported producer contract")
		}
		id, idOK := edit["id"].(string)
		component, componentOK := edit["component"].(string)
		if !idOK || id == "" || len(id) > 128 || !componentOK || component != strings.ToLower(component) {
			return nil, errors.New("RRSI proposal edit identity is invalid")
		}
		if _, ok := rrsiProposalComponents[component]; !ok {
			return nil, errors.New("RRSI proposal edit component is unsupported")
		}
		for _, key := range []string{"hypothesis", "targets_mode", "why_not_lower_lever", "trigger_condition", "retroactive_check", "regression_risk", "mechanism"} {
			if field, exists := edit[key]; exists {
				text, ok := field.(string)
				if !ok || len(text) > 8192 || strings.ContainsRune(text, '\x00') {
					return nil, errors.New("RRSI proposal edit text is invalid")
				}
			}
		}
		predicted, ok := edit["predicted_affected"].([]any)
		if !ok || len(predicted) > 512 {
			return nil, errors.New("RRSI proposal predicted-task list is invalid")
		}
		for _, rawTaskID := range predicted {
			taskID, ok := rawTaskID.(string)
			if !ok || len(taskID) > 512 || strings.ContainsRune(taskID, '\x00') {
				return nil, errors.New("RRSI proposal predicted-task identifier is invalid")
			}
		}
		components = append(components, component)
	}
	return rrsiMarshal(rrsiProposalSummary{
		SchemaVersion: rrsiProposalSummarySchema, Domain: domain, ProposalStatus: status,
		FileEditCount: fileEditCount, DeclaredEditCount: len(rawEdits), EditComponents: components,
		Authority: "local_pending_review", HumanReviewRequired: true,
		SelectionAccepted: false, ExecutionAuthorized: false,
		OmittedFields: []string{
			"summary", "reason", "mechanism", "targets_mode", "interaction_log", "edit_ids",
			"hypotheses", "predicted_task_ids", "review_text", "diffs",
		},
	})
}
