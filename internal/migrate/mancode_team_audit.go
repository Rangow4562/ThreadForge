package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

const (
	mancodeActorSummarySchema     = "threadforge.mancode-actor-summary.v1"
	mancodeTeamEventSummarySchema = "threadforge.mancode-team-event-summary.v1"
)

var mancodeTeamEventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var mancodeTeamEventEntityKinds = map[string]bool{
	"workflow": true, "claim": true, "checkpoint": true, "handoff": true,
	"actor": true, "decision": true, "team_policy": true, "project_config": true,
	"transport": true,
}

var mancodeTaskScopedTeamEventKinds = map[string]bool{
	"workflow": true, "claim": true, "checkpoint": true, "handoff": true,
}

type mancodeActorSummaryImport struct {
	SchemaVersion              string  `json:"schema_version"`
	Authority                  string  `json:"authority"`
	HumanReviewRequired        bool    `json:"human_review_required"`
	ExecutionAuthorized        bool    `json:"execution_authorized"`
	ActorScope                 string  `json:"actor_scope"`
	DisplayNamePresent         bool    `json:"display_name_present"`
	SourceCreatedAt            *string `json:"source_created_at"`
	SourceJoinedAt             *string `json:"source_joined_at"`
	UpdatedAt                  string  `json:"updated_at"`
	SourceActorIdentityOmitted bool    `json:"source_actor_identity_omitted"`
}

type mancodeTeamEventSummaryImport struct {
	SchemaVersion           string `json:"schema_version"`
	Authority               string `json:"authority"`
	HumanReviewRequired     bool   `json:"human_review_required"`
	ExecutionAuthorized     bool   `json:"execution_authorized"`
	EventType               string `json:"event_type"`
	EntityKind              string `json:"entity_kind"`
	TaskReferencePresent    bool   `json:"task_reference_present"`
	TaskRevisionPresent     bool   `json:"task_revision_present"`
	CreatedAt               string `json:"created_at"`
	SourceIdentitiesOmitted bool   `json:"source_identities_omitted"`
}

func normalizeMancodeActor(relative string, value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode actor record must be an object")
	}
	recordType := classifyMancodeRecord(relative)
	keys := []string{"schemaVersion", "actorId", "displayName", "updatedAt"}
	scope := "shared"
	var createdAt, joinedAt *string
	switch recordType {
	case "mancode_actor_identity":
		scope = "local"
		keys = []string{"schemaVersion", "actorId", "displayName", "createdAt", "updatedAt"}
	case "mancode_actor_profile":
		keys = []string{"schemaVersion", "actorId", "displayName", "joinedAt", "updatedAt"}
	default:
		return nil, true, errors.New("Mancode actor record path is unsupported")
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode actor record does not match the supported schema")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	actorID := mancodeString(object["actorId"])
	displayName, nameOK := object["displayName"].(string)
	updatedAt := mancodeString(object["updatedAt"])
	if !versionOK || version != 1 || !mancodeOperationIDPattern.MatchString(actorID) || !nameOK ||
		strings.ContainsRune(displayName, '\x00') || len(utf16.Encode([]rune(strings.TrimSpace(displayName)))) == 0 ||
		len(utf16.Encode([]rune(strings.TrimSpace(displayName)))) > 128 || !mancodeRFC3339(updatedAt) {
		return nil, true, errors.New("Mancode actor record fields are invalid")
	}
	if scope == "local" {
		created := mancodeString(object["createdAt"])
		if !mancodeRFC3339(created) {
			return nil, true, errors.New("Mancode local actor creation time is invalid")
		}
		createdAt = &created
	} else {
		pathID, pathOK := mancodeSharedTeamEntityID(relative, "actors")
		joined := mancodeString(object["joinedAt"])
		if !pathOK || actorID != pathID || !mancodeRFC3339(joined) {
			return nil, true, errors.New("Mancode shared actor profile identity or join time is invalid")
		}
		joinedAt = &joined
	}
	payload, err := json.Marshal(mancodeActorSummaryImport{
		SchemaVersion: mancodeActorSummarySchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
		ActorScope: scope, DisplayNamePresent: true,
		SourceCreatedAt: createdAt, SourceJoinedAt: joinedAt, UpdatedAt: updatedAt,
		SourceActorIdentityOmitted: true,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode actor summary: %w", err)
	}
	return []normalizedItem{{Type: recordType, Payload: payload}}, true, nil
}

func normalizeMancodeTeamEvent(relative string, value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode team event must be an object")
	}
	keys := []string{
		"schemaVersion", "eventId", "eventType", "operationId", "entityRef", "taskRef",
		"actorId", "taskRevision", "createdAt",
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode team event does not match the supported schema")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	eventID := mancodeString(object["eventId"])
	operationID := mancodeString(object["operationId"])
	actorID := mancodeString(object["actorId"])
	eventType := mancodeString(object["eventType"])
	createdAt := mancodeString(object["createdAt"])
	pathID, pathOK := mancodeSharedTeamEntityID(relative, "events")
	if !versionOK || version != 1 || !mancodeOperationIDPattern.MatchString(eventID) || eventID != pathID ||
		!pathOK || !mancodeOperationIDPattern.MatchString(operationID) || !mancodeOperationIDPattern.MatchString(actorID) ||
		!mancodeTeamEventTypePattern.MatchString(eventType) || !mancodeRFC3339(createdAt) {
		return nil, true, errors.New("Mancode team event identity or fields are invalid")
	}
	entity, ok := object["entityRef"].(map[string]any)
	if !ok || !mancodeHasAllKeys(entity, []string{"kind", "id"}) || !mancodeHasOnlyKeys(entity, []string{"kind", "id"}) {
		return nil, true, errors.New("Mancode team event entity reference is invalid")
	}
	entityKind := mancodeString(entity["kind"])
	if !mancodeTeamEventEntityKinds[entityKind] || !mancodeOperationIDPattern.MatchString(mancodeString(entity["id"])) {
		return nil, true, errors.New("Mancode team event entity identity is invalid")
	}
	taskReferencePresent := object["taskRef"] != nil
	taskRevisionPresent := object["taskRevision"] != nil
	if mancodeTaskScopedTeamEventKinds[entityKind] {
		task, taskOK := object["taskRef"].(map[string]any)
		revision, revisionOK := mancodeIntegerValue(object["taskRevision"])
		if !taskReferencePresent || !taskOK || !mancodeHasAllKeys(task, []string{"namespace", "taskId"}) ||
			!mancodeHasOnlyKeys(task, []string{"namespace", "taskId"}) || mancodeString(task["namespace"]) != "shared" ||
			!mancodeOperationIDPattern.MatchString(mancodeString(task["taskId"])) || !revisionOK || revision < 1 {
			return nil, true, errors.New("Mancode task-scoped event lacks a valid shared TaskRef and revision")
		}
	} else if taskReferencePresent || taskRevisionPresent {
		return nil, true, errors.New("Mancode non-task event cannot contain a TaskRef or task revision")
	}
	payload, err := json.Marshal(mancodeTeamEventSummaryImport{
		SchemaVersion: mancodeTeamEventSummarySchema, Authority: "local_pending_review",
		HumanReviewRequired: true, ExecutionAuthorized: false,
		EventType: eventType, EntityKind: entityKind,
		TaskReferencePresent: taskReferencePresent, TaskRevisionPresent: taskRevisionPresent,
		CreatedAt: createdAt, SourceIdentitiesOmitted: true,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode team-event summary: %w", err)
	}
	return []normalizedItem{{Type: "mancode_team_event", Payload: payload}}, true, nil
}

func mancodeSharedTeamEntityID(relative, collection string) (string, bool) {
	parts := strings.Split(strings.Trim(filepath.ToSlash(relative), "/"), "/")
	start := 0
	if len(parts) == 5 && strings.EqualFold(parts[0], ".mancode") {
		start = 1
	} else if len(parts) != 4 {
		return "", false
	}
	if len(parts) != start+4 || !strings.EqualFold(parts[start], "shared") ||
		!strings.EqualFold(parts[start+1], "team") || !strings.EqualFold(parts[start+2], collection) {
		return "", false
	}
	filename := parts[start+3]
	if !strings.EqualFold(filepath.Ext(filename), ".json") {
		return "", false
	}
	id := strings.TrimSuffix(filename, filepath.Ext(filename))
	return id, mancodeOperationIDPattern.MatchString(id)
}
