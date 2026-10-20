package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const mancodeProjectGlossaryImportSchema = "threadforge.mancode-project-glossary.v1"

const (
	mancodeGlossaryMaxEntries       = 200
	mancodeGlossaryMaxAliases       = 4096
	mancodeGlossaryMaxTermLength    = 64
	mancodeGlossaryMaxDefinitionLen = 500
)

type mancodeGlossaryEntryImport struct {
	Term                       string   `json:"term"`
	Definition                 string   `json:"definition"`
	Aliases                    []string `json:"aliases"`
	ConfirmedAt                string   `json:"confirmed_at"`
	SourceTaskReferencePresent bool     `json:"source_task_reference_present"`
}

type mancodeProjectGlossaryImport struct {
	SchemaVersion       string                       `json:"schema_version"`
	Authority           string                       `json:"authority"`
	HumanReviewRequired bool                         `json:"human_review_required"`
	ExecutionAuthorized bool                         `json:"execution_authorized"`
	Revision            int64                        `json:"revision"`
	Entries             []mancodeGlossaryEntryImport `json:"entries"`
}

func normalizeMancodeProjectGlossary(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode project glossary must be an object")
	}
	keys := []string{"schemaVersion", "revision", "entries"}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode project glossary does not match the supported schema")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	revision, revisionOK := mancodeIntegerValue(object["revision"])
	entries, entriesOK := object["entries"].([]any)
	if !versionOK || version != 1 || !revisionOK || revision < 0 || !entriesOK || len(entries) > mancodeGlossaryMaxEntries {
		return nil, true, errors.New("Mancode project glossary version, revision, or entry count is invalid")
	}
	projectEntries := make([]mancodeGlossaryEntryImport, 0, len(entries))
	seenTerms := make(map[string]bool)
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		entryKeys := []string{"term", "definition", "aliases", "sourceTaskRef", "confirmedAt"}
		if !ok || !mancodeHasAllKeys(entry, entryKeys) || !mancodeHasOnlyKeys(entry, entryKeys) {
			return nil, true, errors.New("Mancode project glossary entry does not match the supported schema")
		}
		term, termOK := mancodeGlossaryText(entry["term"], mancodeGlossaryMaxTermLength)
		definition, definitionOK := mancodeGlossaryText(entry["definition"], mancodeGlossaryMaxDefinitionLen)
		if !termOK || !definitionOK {
			return nil, true, errors.New("Mancode project glossary term or definition is invalid")
		}
		aliasesValue, aliasesOK := entry["aliases"].([]any)
		if !aliasesOK || len(aliasesValue) > mancodeGlossaryMaxAliases {
			return nil, true, errors.New("Mancode project glossary alias list is invalid or oversized")
		}
		aliases := make([]string, 0, len(aliasesValue))
		entryTerms := map[string]bool{}
		termKey := strings.ToLower(strings.TrimSpace(term))
		if seenTerms[termKey] {
			return nil, true, errors.New("Mancode project glossary contains conflicting terms or aliases")
		}
		entryTerms[termKey] = true
		for _, aliasValue := range aliasesValue {
			alias, valid := mancodeGlossaryText(aliasValue, mancodeGlossaryMaxTermLength)
			if !valid {
				return nil, true, errors.New("Mancode project glossary alias is invalid")
			}
			key := strings.ToLower(strings.TrimSpace(alias))
			if entryTerms[key] || seenTerms[key] {
				return nil, true, errors.New("Mancode project glossary contains conflicting terms or aliases")
			}
			entryTerms[key] = true
			aliases = append(aliases, alias)
		}
		for key := range entryTerms {
			seenTerms[key] = true
		}
		taskRefPresent, taskRefOK := validMancodeGlossaryTaskRef(entry["sourceTaskRef"])
		if !taskRefOK {
			return nil, true, errors.New("Mancode project glossary task reference is invalid")
		}
		confirmedAt := mancodeString(entry["confirmedAt"])
		if _, err := time.Parse(time.RFC3339Nano, confirmedAt); err != nil {
			return nil, true, errors.New("Mancode project glossary confirmation timestamp is invalid")
		}
		projectEntries = append(projectEntries, mancodeGlossaryEntryImport{
			Term: term, Definition: definition, Aliases: aliases,
			ConfirmedAt: confirmedAt, SourceTaskReferencePresent: taskRefPresent,
		})
	}
	payload, err := json.Marshal(mancodeProjectGlossaryImport{
		SchemaVersion: mancodeProjectGlossaryImportSchema,
		Authority:     "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		Revision: revision, Entries: projectEntries,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode project glossary projection: %w", err)
	}
	return []normalizedItem{{Type: "project_glossary", Payload: payload}}, true, nil
}

func mancodeGlossaryText(value any, maximum int) (string, bool) {
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) || strings.TrimSpace(text) == "" || strings.ContainsRune(text, '\x00') ||
		len(utf16.Encode([]rune(text))) > maximum || privacy.ContainsCredentialText(text) {
		return "", false
	}
	return text, true
}

func validMancodeGlossaryTaskRef(value any) (bool, bool) {
	if value == nil {
		return false, true
	}
	object, ok := value.(map[string]any)
	if !ok || !mancodeHasAllKeys(object, []string{"namespace", "taskId"}) ||
		!mancodeHasOnlyKeys(object, []string{"namespace", "taskId"}) || object["namespace"] != "shared" ||
		!mancodeOperationIDPattern.MatchString(mancodeString(object["taskId"])) {
		return false, false
	}
	return true, true
}
