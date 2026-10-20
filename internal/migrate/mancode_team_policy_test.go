package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func mancodeTeamPolicyFixture() map[string]any {
	return map[string]any{
		"schemaVersion":           1,
		"revision":                4,
		"workspaceId":             mancodeDecisionTestID("1"),
		"policy":                  "auto",
		"recentDays":              30,
		"defaultVisibility":       "local",
		"shareConfirmedDecisions": false,
		"retention": map[string]any{
			"localRawArtifactDays": 0,
			"localCacheDays":       14,
			"completedSessionDays": 30,
		},
		"lastOperationId": mancodeDecisionTestID("2"),
		"updatedAt":       "2026-10-08T12:00:00Z",
	}
}

func TestMancodeTeamPolicyIsHistoricalAndWorkspaceUnbound(t *testing.T) {
	root := t.TempDir()
	writeMancodeJSONFixture(t, root, ".mancode/shared/team/policy.json", mancodeTeamPolicyFixture())
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("valid Mancode team policy was not converted: %#v", manifest.Summary)
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
	if len(records) != 1 || records[0].Type != "mancode_team_policy" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected Mancode team-policy staging: %#v", records)
	}
	var projection map[string]any
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["schema_version"] != mancodeTeamPolicyImportSchema || projection["policy_kind"] != "team_sharing" ||
		projection["authority"] != "local_pending_review" || projection["human_review_required"] != true ||
		projection["execution_authorized"] != false || projection["workspace_binding_verified"] != false ||
		projection["source_policy"] != "auto" || projection["default_visibility"] != "local" ||
		projection["share_confirmed_decisions"] != false || projection["recent_days"] != float64(30) {
		t.Fatalf("Mancode team policy became active or lost historical meaning: %s", records[0].Payload)
	}
	for _, private := range []string{mancodeDecisionTestID("1"), mancodeDecisionTestID("2"), "workspaceId", "lastOperationId"} {
		if strings.Contains(string(records[0].Payload), private) {
			t.Fatalf("Mancode team policy retained source identity %q: %s", private, records[0].Payload)
		}
	}
}

func TestMancodeTeamPolicyRejectsUnsupportedContracts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["secret"] = "not-copied" }},
		{name: "unsupported policy", mutate: func(value map[string]any) { value["policy"] = "always-share" }},
		{name: "invalid workspace ULID", mutate: func(value map[string]any) { value["workspaceId"] = "invalid" }},
		{name: "negative recent window", mutate: func(value map[string]any) { value["recentDays"] = -1 }},
		{name: "unsupported visibility", mutate: func(value map[string]any) { value["defaultVisibility"] = "remote" }},
		{name: "nonboolean sharing flag", mutate: func(value map[string]any) { value["shareConfirmedDecisions"] = "yes" }},
		{name: "unknown retention key", mutate: func(value map[string]any) { value["retention"].(map[string]any)["remoteDays"] = 7 }},
		{name: "negative retention", mutate: func(value map[string]any) { value["retention"].(map[string]any)["localCacheDays"] = -1 }},
		{name: "invalid operation ULID", mutate: func(value map[string]any) { value["lastOperationId"] = "invalid" }},
		{name: "invalid timestamp", mutate: func(value map[string]any) { value["updatedAt"] = "yesterday" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := mancodeTeamPolicyFixture()
			tc.mutate(value)
			_, handled, err := normalizeMancodeTeamPolicy(value)
			if !handled || err == nil {
				t.Fatalf("unsupported team policy was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
}
