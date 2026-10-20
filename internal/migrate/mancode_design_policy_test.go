package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func mancodeDesignPolicyFixture() map[string]any {
	return map[string]any{
		"schemaVersion": 1, "revision": 3, "enabled": true, "preset": "refine",
		"iconPolicy": "existing-first", "emojiPolicy": "forbid-as-interface-icon",
		"motionPolicy": "purposeful", "browserValidation": "when-available",
		"lastOperationId": mancodeDecisionTestID("7"), "updatedAt": "2026-10-08T12:00:00Z",
	}
}

func TestMancodeDesignPolicySnapshotRetainsSettingsWithoutAuthority(t *testing.T) {
	root := t.TempDir()
	writeMancodeJSONFixture(t, root, ".mancode/shared/context/design-policy.json", mancodeDesignPolicyFixture())
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("design policy did not inspect as supported: %#v", manifest.Summary)
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
	if len(records) != 1 || records[0].Type != "project_policy" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected design-policy records: %#v", records)
	}
	var projection map[string]any
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["schema_version"] != mancodeDesignPolicyImportSchema || projection["authority"] != "local_pending_review" ||
		projection["human_review_required"] != true || projection["execution_authorized"] != false ||
		projection["revision"] != float64(3) || projection["source_enabled"] != true ||
		projection["preset"] != "refine" || projection["icon_policy"] != "existing-first" ||
		projection["emoji_policy"] != "forbid-as-interface-icon" || projection["motion_policy"] != "purposeful" ||
		projection["browser_validation"] != "when-available" {
		t.Fatalf("design policy projection lost settings or gained authority: %s", records[0].Payload)
	}
	if strings.Contains(string(records[0].Payload), mancodeDecisionTestID("7")) || strings.Contains(string(records[0].Payload), "lastOperationId") {
		t.Fatalf("design policy retained operation identity: %s", records[0].Payload)
	}
}

func TestMancodeDesignPolicyRejectsUnknownFieldsAndUnsupportedValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown field", mutate: func(value map[string]any) { value["private"] = "value" }},
		{name: "unsupported schema", mutate: func(value map[string]any) { value["schemaVersion"] = 2 }},
		{name: "revision zero", mutate: func(value map[string]any) { value["revision"] = 0 }},
		{name: "invalid preset", mutate: func(value map[string]any) { value["preset"] = "auto-execute" }},
		{name: "invalid enabled type", mutate: func(value map[string]any) { value["enabled"] = "true" }},
		{name: "invalid operation id", mutate: func(value map[string]any) { value["lastOperationId"] = "not-a-ulid" }},
		{name: "invalid timestamp", mutate: func(value map[string]any) { value["updatedAt"] = "not-a-time" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := mancodeDesignPolicyFixture()
			tc.mutate(value)
			_, handled, err := normalizeMancodeDesignPolicy(value)
			if !handled || err == nil {
				t.Fatalf("invalid design policy was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
}
