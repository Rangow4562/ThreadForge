package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const mancodeConfirmedDecisionImportSchema = "threadforge.mancode-confirmed-decision.v1"

var (
	mancodeDecisionDigestPattern  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	mancodeDecisionClausePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	mancodeDecisionURIPathPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

type mancodeDecisionAlternativeImport struct {
	Option          string `json:"option"`
	ReasonNotChosen string `json:"reason_not_chosen"`
}

type mancodeDecisionClauseImport struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}

type mancodeDecisionRelationImport struct {
	Action       string `json:"action"`
	TargetID     string `json:"target_id"`
	TargetDigest string `json:"target_digest"`
	Clauses      any    `json:"clauses"`
}

type mancodeDecisionResolutionRecordImport struct {
	DecisionID string `json:"decision_id"`
	Digest     string `json:"digest"`
}

type mancodeDecisionResolutionImport struct {
	Records []mancodeDecisionResolutionRecordImport `json:"records"`
}

type mancodeDecisionDetailsImport struct {
	RecordKind    string                             `json:"record_kind"`
	Rationale     string                             `json:"rationale"`
	Alternatives  []mancodeDecisionAlternativeImport `json:"alternatives"`
	Tradeoffs     []string                           `json:"tradeoffs"`
	RevisitWhen   []string                           `json:"revisit_when"`
	Applicability struct {
		Modules []string `json:"modules"`
		Paths   []string `json:"paths"`
	} `json:"applicability"`
	Clauses    []mancodeDecisionClauseImport    `json:"clauses"`
	Relations  []mancodeDecisionRelationImport  `json:"relations"`
	Resolution *mancodeDecisionResolutionImport `json:"resolution,omitempty"`
}

type mancodeConfirmedDecisionImport struct {
	SchemaVersion              string                        `json:"schema_version"`
	Authority                  string                        `json:"authority"`
	HumanReviewRequired        bool                          `json:"human_review_required"`
	ExecutionAuthorized        bool                          `json:"execution_authorized"`
	LegacyConfirmationAdvisory bool                          `json:"legacy_confirmation_advisory"`
	SourceAuthorizationOmitted bool                          `json:"source_authorization_omitted"`
	SourceSchemaVersion        int64                         `json:"source_schema_version"`
	DecisionID                 string                        `json:"decision_id"`
	Title                      string                        `json:"title"`
	Statement                  string                        `json:"statement"`
	ConfirmedAt                string                        `json:"confirmed_at"`
	TaskReferencePresent       bool                          `json:"task_reference_present"`
	Details                    *mancodeDecisionDetailsImport `json:"details,omitempty"`
}

func isMancodeConfirmedDecisionPath(relative string) bool {
	_, ok := mancodeConfirmedDecisionPathID(relative)
	return ok
}

func mancodeConfirmedDecisionPathID(relative string) (string, bool) {
	clean := filepath.ToSlash(strings.TrimPrefix(relative, "./"))
	clean = "/" + strings.TrimPrefix(clean, "/")
	for _, prefix := range []string{
		"/.mancode/shared/memory/decisions/",
		"/shared/memory/decisions/",
	} {
		if !strings.HasPrefix(strings.ToLower(clean), prefix) {
			continue
		}
		name := clean[len(prefix):]
		if strings.Contains(name, "/") || !strings.EqualFold(filepath.Ext(name), ".json") {
			return "", false
		}
		id := name[:len(name)-len(filepath.Ext(name))]
		return id, mancodeOperationIDPattern.MatchString(id)
	}
	return "", false
}

func normalizeMancodeConfirmedDecision(relative string, value any) ([]normalizedItem, bool, error) {
	pathID, pathOK := mancodeConfirmedDecisionPathID(relative)
	if !pathOK {
		return nil, true, errors.New("Mancode confirmed decision path is invalid")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode confirmed decision must be an object")
	}
	sourceVersion, versionOK := mancodeIntegerValue(object["schemaVersion"])
	if !versionOK || (sourceVersion != 1 && sourceVersion != 2) {
		return nil, true, errors.New("Mancode confirmed decision schema version is unsupported")
	}
	keys := []string{
		"schemaVersion", "decisionId", "title", "statement", "taskRef", "confirmedByActorId",
		"confirmedAt", "operationId", "authorization",
	}
	if sourceVersion == 2 {
		keys = append(keys, "details")
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode confirmed decision does not match its supported schema")
	}
	decisionID := mancodeString(object["decisionId"])
	actorID := mancodeString(object["confirmedByActorId"])
	operationID := mancodeString(object["operationId"])
	if !mancodeOperationIDPattern.MatchString(decisionID) || decisionID != pathID ||
		!mancodeOperationIDPattern.MatchString(actorID) || !mancodeOperationIDPattern.MatchString(operationID) {
		return nil, true, errors.New("Mancode confirmed decision identity is invalid")
	}
	title, titleOK := mancodeDecisionText(object["title"], 256)
	statement, statementOK := mancodeDecisionText(object["statement"], 12_000)
	confirmedAt := mancodeString(object["confirmedAt"])
	if !titleOK || !statementOK {
		return nil, true, errors.New("Mancode confirmed decision text is invalid or credential-bearing")
	}
	if _, err := time.Parse(time.RFC3339Nano, confirmedAt); err != nil {
		return nil, true, errors.New("Mancode confirmed decision timestamp is invalid")
	}
	if !validMancodeDecisionAuthorization(object["authorization"], actorID) {
		return nil, true, errors.New("Mancode confirmed decision authorization basis is invalid")
	}
	taskReferencePresent, taskOK := validMancodeDecisionTaskRef(object["taskRef"])
	if !taskOK {
		return nil, true, errors.New("Mancode confirmed decision task reference is invalid")
	}
	var details *mancodeDecisionDetailsImport
	var err error
	if sourceVersion == 2 {
		details, err = normalizeMancodeDecisionDetails(object["details"])
		if err != nil {
			return nil, true, fmt.Errorf("Mancode confirmed decision details are invalid: %w", err)
		}
	}
	payload, err := json.Marshal(mancodeConfirmedDecisionImport{
		SchemaVersion: mancodeConfirmedDecisionImportSchema,
		Authority:     "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		LegacyConfirmationAdvisory: true, SourceAuthorizationOmitted: true,
		SourceSchemaVersion: sourceVersion, DecisionID: decisionID, Title: title,
		Statement: statement, ConfirmedAt: confirmedAt, TaskReferencePresent: taskReferencePresent,
		Details: details,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode confirmed decision projection: %w", err)
	}
	return []normalizedItem{{Type: "decision_record", Payload: payload}}, true, nil
}

func validMancodeDecisionAuthorization(value any, actorID string) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	keys := []string{"schemaVersion", "action", "actorId", "sessionId", "trustBoundary", "decisionDigest", "authorizedAt"}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	authorizedAt := mancodeString(object["authorizedAt"])
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) ||
		!versionOK || version != 1 || object["action"] != "confirmed_decision_publish" ||
		mancodeString(object["actorId"]) != actorID || !mancodeOperationIDPattern.MatchString(mancodeString(object["sessionId"])) ||
		object["trustBoundary"] != "repo-collaborators" ||
		!mancodeDecisionDigestPattern.MatchString(mancodeString(object["decisionDigest"])) {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, authorizedAt)
	return err == nil
}

func validMancodeDecisionTaskRef(value any) (bool, bool) {
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

func normalizeMancodeDecisionDetails(value any) (*mancodeDecisionDetailsImport, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("details must be an object")
	}
	keys := []string{"capability", "recordKind", "rationale", "alternatives", "tradeoffs", "revisitWhen", "applicability", "clauses", "relations"}
	if _, exists := object["resolution"]; exists {
		keys = append(keys, "resolution")
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) || object["capability"] != "decision-relations:1" {
		return nil, errors.New("details do not match the supported schema")
	}
	recordKind := mancodeString(object["recordKind"])
	if recordKind != "decision" && recordKind != "revocation" {
		return nil, errors.New("record kind is invalid")
	}
	rationale, rationaleOK := mancodeDecisionText(object["rationale"], 4000)
	if !rationaleOK {
		return nil, errors.New("rationale is invalid")
	}
	tradeoffs, tradeoffsOK := mancodeDecisionStrings(object["tradeoffs"], 32, 4000)
	revisitWhen, revisitOK := mancodeDecisionStrings(object["revisitWhen"], 32, 4000)
	if !tradeoffsOK || !revisitOK {
		return nil, errors.New("tradeoff or revisit conditions are invalid")
	}
	applicability, ok := object["applicability"].(map[string]any)
	if !ok || !mancodeHasAllKeys(applicability, []string{"modules", "paths"}) || !mancodeHasOnlyKeys(applicability, []string{"modules", "paths"}) {
		return nil, errors.New("applicability does not match the supported schema")
	}
	modules, modulesOK := mancodeDecisionStrings(applicability["modules"], 32, 128)
	paths, pathsOK := mancodeDecisionStrings(applicability["paths"], 32, 256)
	if !modulesOK || !pathsOK {
		return nil, errors.New("applicability values are invalid")
	}
	for _, path := range paths {
		if !safeMancodeDecisionPath(path) {
			return nil, errors.New("applicability path is unsafe")
		}
	}
	alternativesValue, alternativesOK := object["alternatives"].([]any)
	if !alternativesOK || len(alternativesValue) > 32 {
		return nil, errors.New("alternatives are invalid")
	}
	alternatives := make([]mancodeDecisionAlternativeImport, 0, len(alternativesValue))
	for _, item := range alternativesValue {
		alternative, ok := item.(map[string]any)
		if !ok || !mancodeHasAllKeys(alternative, []string{"option", "reasonNotChosen"}) || !mancodeHasOnlyKeys(alternative, []string{"option", "reasonNotChosen"}) {
			return nil, errors.New("alternative does not match the supported schema")
		}
		option, optionOK := mancodeDecisionText(alternative["option"], 4000)
		reason, reasonOK := mancodeDecisionText(alternative["reasonNotChosen"], 4000)
		if !optionOK || !reasonOK {
			return nil, errors.New("alternative text is invalid")
		}
		alternatives = append(alternatives, mancodeDecisionAlternativeImport{Option: option, ReasonNotChosen: reason})
	}
	clausesValue, clausesOK := object["clauses"].([]any)
	if !clausesOK || len(clausesValue) > 64 {
		return nil, errors.New("clauses are invalid")
	}
	clauses := make([]mancodeDecisionClauseImport, 0, len(clausesValue))
	clauseIDs := map[string]bool{}
	for _, item := range clausesValue {
		clause, ok := item.(map[string]any)
		if !ok || !mancodeHasAllKeys(clause, []string{"id", "statement"}) || !mancodeHasOnlyKeys(clause, []string{"id", "statement"}) {
			return nil, errors.New("clause does not match the supported schema")
		}
		id, idOK := mancodeDecisionText(clause["id"], 64)
		statement, statementOK := mancodeDecisionText(clause["statement"], 4000)
		if !idOK || !mancodeDecisionClausePattern.MatchString(id) || !statementOK || clauseIDs[id] {
			return nil, errors.New("clause identity or text is invalid")
		}
		clauseIDs[id] = true
		clauses = append(clauses, mancodeDecisionClauseImport{ID: id, Statement: statement})
	}
	relationsValue, relationsOK := object["relations"].([]any)
	if !relationsOK || len(relationsValue) > 32 {
		return nil, errors.New("relations are invalid")
	}
	relations := make([]mancodeDecisionRelationImport, 0, len(relationsValue))
	relationIDs := map[string]bool{}
	for _, item := range relationsValue {
		relation, ok := item.(map[string]any)
		if !ok || !mancodeHasAllKeys(relation, []string{"action", "targetId", "targetDigest", "clauses"}) || !mancodeHasOnlyKeys(relation, []string{"action", "targetId", "targetDigest", "clauses"}) {
			return nil, errors.New("relation does not match the supported schema")
		}
		action := mancodeString(relation["action"])
		targetID := mancodeString(relation["targetId"])
		targetDigest := mancodeString(relation["targetDigest"])
		if (action != "supersede" && action != "revoke") || !mancodeOperationIDPattern.MatchString(targetID) ||
			!mancodeDecisionDigestPattern.MatchString(targetDigest) || relationIDs[targetID] {
			return nil, errors.New("relation identity or digest is invalid")
		}
		relationIDs[targetID] = true
		selected, selectedOK := mancodeDecisionRelationClauses(relation["clauses"])
		if !selectedOK {
			return nil, errors.New("relation clause references are invalid")
		}
		relations = append(relations, mancodeDecisionRelationImport{
			Action: action, TargetID: targetID, TargetDigest: targetDigest, Clauses: selected,
		})
	}
	if recordKind == "decision" {
		if len(clauses) == 0 || (len(modules)+len(paths) == 0) {
			return nil, errors.New("decision requires clauses and applicability")
		}
		for _, relation := range relations {
			if relation.Action != "supersede" {
				return nil, errors.New("decision relation action is invalid")
			}
		}
	} else {
		if len(clauses) != 0 || len(relations) == 0 {
			return nil, errors.New("revocation requires relations and no clauses")
		}
		for _, relation := range relations {
			if relation.Action != "revoke" {
				return nil, errors.New("revocation relation action is invalid")
			}
		}
	}
	details := &mancodeDecisionDetailsImport{
		RecordKind: recordKind, Rationale: rationale, Alternatives: alternatives,
		Tradeoffs: tradeoffs, RevisitWhen: revisitWhen,
		Applicability: struct {
			Modules []string `json:"modules"`
			Paths   []string `json:"paths"`
		}{Modules: modules, Paths: paths},
		Clauses: clauses, Relations: relations,
	}
	if rawResolution, exists := object["resolution"]; exists {
		resolution, err := normalizeMancodeDecisionResolution(rawResolution, relations)
		if err != nil {
			return nil, err
		}
		details.Resolution = resolution
	}
	return details, nil
}

func normalizeMancodeDecisionResolution(value any, relations []mancodeDecisionRelationImport) (*mancodeDecisionResolutionImport, error) {
	object, ok := value.(map[string]any)
	if !ok || !mancodeHasAllKeys(object, []string{"records"}) || !mancodeHasOnlyKeys(object, []string{"records"}) {
		return nil, errors.New("resolution does not match the supported schema")
	}
	items, ok := object["records"].([]any)
	if !ok || len(items) < 2 || len(items) > 32 || len(items) != len(relations) {
		return nil, errors.New("resolution records are invalid")
	}
	records := make([]mancodeDecisionResolutionRecordImport, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok || !mancodeHasAllKeys(record, []string{"decisionId", "digest"}) || !mancodeHasOnlyKeys(record, []string{"decisionId", "digest"}) {
			return nil, errors.New("resolution record does not match the supported schema")
		}
		id := mancodeString(record["decisionId"])
		digest := mancodeString(record["digest"])
		if !mancodeOperationIDPattern.MatchString(id) || !mancodeDecisionDigestPattern.MatchString(digest) || seen[id] {
			return nil, errors.New("resolution record identity or digest is invalid")
		}
		seen[id] = true
		matched := false
		for _, relation := range relations {
			if relation.TargetID == id && relation.TargetDigest == digest && relation.Clauses == "all" {
				matched = true
				break
			}
		}
		if !matched {
			return nil, errors.New("resolution record does not match a complete relation")
		}
		records = append(records, mancodeDecisionResolutionRecordImport{DecisionID: id, Digest: digest})
	}
	return &mancodeDecisionResolutionImport{Records: records}, nil
}

func mancodeDecisionRelationClauses(value any) (any, bool) {
	if value == "all" {
		return "all", true
	}
	items, ok := value.([]any)
	if !ok || len(items) == 0 || len(items) > 64 {
		return nil, false
	}
	clauses := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		id, valid := mancodeDecisionText(item, 64)
		if !valid || !mancodeDecisionClausePattern.MatchString(id) || seen[id] {
			return nil, false
		}
		seen[id] = true
		clauses = append(clauses, id)
	}
	return clauses, true
}

func mancodeDecisionStrings(value any, maximumItems, maximumText int) ([]string, bool) {
	items, ok := value.([]any)
	if !ok || len(items) > maximumItems {
		return nil, false
	}
	result := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		text, valid := mancodeDecisionText(item, maximumText)
		if !valid || seen[text] {
			return nil, false
		}
		seen[text] = true
		result = append(result, text)
	}
	return result, true
}

func mancodeDecisionText(value any, maximum int) (string, bool) {
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) || strings.TrimSpace(text) == "" || strings.ContainsRune(text, '\x00') ||
		len(utf16.Encode([]rune(text))) > maximum || privacy.ContainsCredentialText(text) {
		return "", false
	}
	return text, true
}

func safeMancodeDecisionPath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") ||
		mancodeDecisionURIPathPattern.MatchString(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	if strings.Contains(value, "*") && !strings.HasSuffix(value, "/**") {
		return false
	}
	if strings.Contains(strings.TrimSuffix(value, "/**"), "*") {
		return false
	}
	return true
}
