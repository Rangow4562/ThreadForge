package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func mancodeSharedActorFixture() map[string]any {
	return map[string]any{
		"schemaVersion": 1, "actorId": mancodeDecisionTestID("1"), "displayName": "Private Reviewer",
		"joinedAt": "2026-10-08T10:00:00Z", "updatedAt": "2026-10-08T12:00:00Z",
	}
}

func mancodeLocalActorFixture() map[string]any {
	return map[string]any{
		"schemaVersion": 1, "actorId": mancodeDecisionTestID("2"), "displayName": "Local Reviewer",
		"createdAt": "2026-10-07T10:00:00Z", "updatedAt": "2026-10-08T12:00:00Z",
	}
}

func mancodeTeamEventFixture(eventID string) map[string]any {
	return map[string]any{
		"schemaVersion": 1, "eventId": eventID, "eventType": "handoff_accepted",
		"operationId": mancodeDecisionTestID("2"), "entityRef": map[string]any{
			"kind": "handoff", "id": mancodeDecisionTestID("3"),
		},
		"taskRef": map[string]any{"namespace": "shared", "taskId": mancodeDecisionTestID("4")},
		"actorId": mancodeDecisionTestID("5"), "taskRevision": 8,
		"createdAt": "2026-10-08T12:00:00Z",
	}
}

func TestMancodeActorProfilesBecomeIdentityFreeReviewSummaries(t *testing.T) {
	root := t.TempDir()
	sharedID := mancodeDecisionTestID("1")
	writeMancodeJSONFixture(t, root, ".mancode/shared/team/actors/"+sharedID+".json", mancodeSharedActorFixture())
	writeMancodeJSONFixture(t, root, ".mancode/local/actor.json", mancodeLocalActorFixture())
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 2 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("valid Mancode actor records did not reconcile: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Mancode, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("unexpected Mancode actor summaries: %#v", records)
	}
	for _, record := range records {
		if record.Scope != "local_pending_review" ||
			(record.Type != "mancode_actor_identity" && record.Type != "mancode_actor_profile") {
			t.Fatalf("Mancode actor summary escaped local review or changed type: %#v", record)
		}
		var projection map[string]any
		if err := json.Unmarshal(record.Payload, &projection); err != nil {
			t.Fatal(err)
		}
		if projection["schema_version"] != mancodeActorSummarySchema || projection["authority"] != "local_pending_review" ||
			projection["human_review_required"] != true || projection["execution_authorized"] != false ||
			projection["display_name_present"] != true || projection["source_actor_identity_omitted"] != true {
			t.Fatalf("actor identity became authority-bearing: %s", record.Payload)
		}
		for _, private := range []string{sharedID, mancodeDecisionTestID("2"), "Private Reviewer", "Local Reviewer", "actorId", "displayName"} {
			if strings.Contains(string(record.Payload), private) {
				t.Fatalf("Mancode actor summary retained identity data %q: %s", private, record.Payload)
			}
		}
	}
}

func TestMancodeTeamEventBecomesContentFreeReviewSummary(t *testing.T) {
	root := t.TempDir()
	eventID := mancodeDecisionTestID("1")
	writeMancodeJSONFixture(t, root, ".mancode/shared/team/events/"+eventID+".json", mancodeTeamEventFixture(eventID))
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("valid Mancode team event did not inspect: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Mancode, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "mancode_team_event" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Mancode event record: %#v", records)
	}
	var projection map[string]any
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["schema_version"] != mancodeTeamEventSummarySchema || projection["authority"] != "local_pending_review" ||
		projection["human_review_required"] != true || projection["execution_authorized"] != false ||
		projection["event_type"] != "handoff_accepted" || projection["entity_kind"] != "handoff" ||
		projection["task_reference_present"] != true || projection["task_revision_present"] != true ||
		projection["source_identities_omitted"] != true {
		t.Fatalf("Mancode team event projection changed meaning or gained authority: %s", records[0].Payload)
	}
	for _, private := range []string{eventID, mancodeDecisionTestID("2"), mancodeDecisionTestID("3"), mancodeDecisionTestID("4"), mancodeDecisionTestID("5"), "taskId", "operationId", "actorId"} {
		if strings.Contains(string(records[0].Payload), private) {
			t.Fatalf("Mancode team event summary retained identity %q: %s", private, records[0].Payload)
		}
	}
}

func TestMancodeActorAndTeamEventConvertersRejectSchemaDrift(t *testing.T) {
	actorCases := []struct {
		name   string
		path   string
		value  map[string]any
		mutate func(map[string]any)
	}{
		{name: "actor unknown field", path: ".mancode/local/actor.json", value: mancodeLocalActorFixture(), mutate: func(value map[string]any) { value["email"] = "person@example.invalid" }},
		{name: "actor invalid ID", path: ".mancode/local/actor.json", value: mancodeLocalActorFixture(), mutate: func(value map[string]any) { value["actorId"] = "invalid" }},
		{name: "actor invalid time", path: ".mancode/local/actor.json", value: mancodeLocalActorFixture(), mutate: func(value map[string]any) { value["createdAt"] = "yesterday" }},
		{name: "shared actor filename binding", path: ".mancode/shared/team/actors/" + mancodeDecisionTestID("3") + ".json", value: mancodeSharedActorFixture(), mutate: func(map[string]any) {}},
		{name: "shared actor nested path", path: ".mancode/shared/team/actors/nested/" + mancodeDecisionTestID("1") + ".json", value: mancodeSharedActorFixture(), mutate: func(map[string]any) {}},
	}
	for _, tc := range actorCases {
		t.Run("actor/"+tc.name, func(t *testing.T) {
			tc.mutate(tc.value)
			_, handled, err := normalizeMancodeActor(tc.path, tc.value)
			if !handled || err == nil {
				t.Fatalf("invalid actor was accepted: handled=%v err=%v", handled, err)
			}
		})
	}

	eventID := mancodeDecisionTestID("1")
	eventCases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown event field", mutate: func(value map[string]any) { value["summary"] = "raw event content" }},
		{name: "event ID differs from filename", mutate: func(value map[string]any) { value["eventId"] = mancodeDecisionTestID("6") }},
		{name: "event type grammar", mutate: func(value map[string]any) { value["eventType"] = "Workflow Updated" }},
		{name: "entity kind", mutate: func(value map[string]any) { value["entityRef"].(map[string]any)["kind"] = "private_kind" }},
		{name: "task reference must be shared", mutate: func(value map[string]any) { value["taskRef"].(map[string]any)["namespace"] = "local" }},
		{name: "task revision required", mutate: func(value map[string]any) { value["taskRevision"] = nil }},
		{name: "invalid operation ID", mutate: func(value map[string]any) { value["operationId"] = "invalid" }},
		{name: "non-task event rejects task ref", mutate: func(value map[string]any) {
			value["entityRef"].(map[string]any)["kind"] = "actor"
		}},
	}
	for _, tc := range eventCases {
		t.Run("event/"+tc.name, func(t *testing.T) {
			value := mancodeTeamEventFixture(eventID)
			tc.mutate(value)
			_, handled, err := normalizeMancodeTeamEvent(".mancode/shared/team/events/"+eventID+".json", value)
			if !handled || err == nil {
				t.Fatalf("invalid team event was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
	_, handled, err := normalizeMancodeTeamEvent(
		".mancode/shared/team/events/nested/"+eventID+".json", mancodeTeamEventFixture(eventID))
	if !handled || err == nil {
		t.Fatalf("nested team-event path was accepted: handled=%v err=%v", handled, err)
	}
}
