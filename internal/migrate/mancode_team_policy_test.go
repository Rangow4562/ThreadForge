package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
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

func mancodeSharedProjectConfigFixture(workspaceID string) map[string]any {
	return map[string]any{
		"schemaVersion": 1,
		"revision":      2,
		"workspaceId":   workspaceID,
		"transport":     map[string]any{"mode": "local", "epoch": 0},
		"updatedAt":     "2026-10-08T12:00:00Z",
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

func TestMancodeTeamPolicyWorkspaceBindingVerifiesMatchingSharedConfig(t *testing.T) {
	root := t.TempDir()
	workspaceID := mancodeDecisionTestID("1")
	writeMancodeJSONFixture(t, root, ".mancode/shared/config.json", mancodeSharedProjectConfigFixture(workspaceID))
	writeMancodeJSONFixture(t, root, ".mancode/shared/team/policy.json", mancodeTeamPolicyFixture())

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MancodeTeamPolicyChecks) != 1 {
		t.Fatalf("expected one team-policy workspace check, got %#v", manifest.MancodeTeamPolicyChecks)
	}
	check := manifest.MancodeTeamPolicyChecks[0]
	if check.Status != "verified" || !check.TeamPolicyValid || !check.ProjectConfigValid || !check.WorkspaceBindingVerified ||
		check.TeamPolicyFileCount != 1 || check.ProjectConfigFileCount != 1 {
		t.Fatalf("matching shared workspace identity was not bound: %#v", check)
	}
	encodedCheck, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedCheck), workspaceID) || strings.Contains(string(encodedCheck), "shared/config.json") {
		t.Fatalf("team-policy check exposed a source identity or path: %s", encodedCheck)
	}
	if err := VerifyManifest(manifest); err != nil {
		t.Fatalf("verified team-policy manifest rejected: %v", err)
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
	var policy mancodeTeamPolicyImport
	for _, record := range records {
		if record.Type == "mancode_team_policy" {
			if err := json.Unmarshal(record.Payload, &policy); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !policy.WorkspaceBindingVerified || policy.ExecutionAuthorized || !policy.HumanReviewRequired {
		t.Fatalf("verified historical workspace binding activated source policy: %#v", policy)
	}
	coverage, err := BuildFormatCoverage(snapshot)
	if err != nil || len(coverage.MancodeTeamPolicyChecks) != 1 || coverage.MancodeTeamPolicyChecks[0].Status != "verified" {
		t.Fatalf("team-policy binding did not reconcile into coverage: checks=%#v err=%v", coverage.MancodeTeamPolicyChecks, err)
	}

	legacySnapshot := snapshot
	legacySnapshot.MancodeTeamPolicyChecks = nil
	legacySnapshot.SourceFingerprint = sourceFingerprint(
		legacySnapshot.Files, legacySnapshot.CrossFileChecks,
		legacySnapshot.MancodeLegacyWorkflowChecks, legacySnapshot.MancodeGitRef,
	)
	legacySnapshot.ID = string(Mancode) + "-" + legacySnapshot.SourceFingerprint[:20]
	if err := VerifyManifest(legacySnapshot); err != nil {
		t.Fatalf("pre-binding manifest v1 compatibility was broken: %v", err)
	}
	legacyCoverage, err := BuildFormatCoverage(legacySnapshot)
	if err != nil || len(legacyCoverage.MancodeTeamPolicyChecks) != 1 ||
		legacyCoverage.MancodeTeamPolicyChecks[0].Status != "incomplete" ||
		!containsString(legacyCoverage.MancodeTeamPolicyChecks[0].Issues, "legacy_unverified") {
		t.Fatalf("legacy manifest was not surfaced as unverified coverage: checks=%#v err=%v",
			legacyCoverage.MancodeTeamPolicyChecks, err)
	}

	tampered := snapshot
	tampered.MancodeTeamPolicyChecks = append([]migrationcoverage.MancodeTeamPolicyCheck(nil), snapshot.MancodeTeamPolicyChecks...)
	tampered.MancodeTeamPolicyChecks[0].TeamPolicySourceSetSHA256 = strings.Repeat("f", 64)
	tampered.SourceFingerprint = sourceFingerprintWithTeamPolicyChecks(
		tampered.Files, tampered.CrossFileChecks, tampered.MancodeLegacyWorkflowChecks,
		tampered.MancodeTeamPolicyChecks, tampered.MancodeGitRef,
	)
	tampered.ID = string(Mancode) + "-" + tampered.SourceFingerprint[:20]
	if VerifyManifest(tampered) == nil {
		t.Fatal("manifest accepted a tampered team-policy source-set commitment")
	}
}

func TestMancodeTeamPolicyWorkspaceBindingSurfacesMismatchAndMissingConfig(t *testing.T) {
	tests := []struct {
		name        string
		configID    string
		writeConfig bool
		status      string
		issue       string
	}{
		{name: "workspace mismatch", configID: mancodeDecisionTestID("9"), writeConfig: true, status: "conflicting", issue: "workspace_mismatch"},
		{name: "missing project config", status: "incomplete", issue: "project_config_missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.writeConfig {
				writeMancodeJSONFixture(t, root, ".mancode/shared/config.json", mancodeSharedProjectConfigFixture(test.configID))
			}
			writeMancodeJSONFixture(t, root, ".mancode/shared/team/policy.json", mancodeTeamPolicyFixture())
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.MancodeTeamPolicyChecks) != 1 {
				t.Fatalf("expected one team-policy check, got %#v", manifest.MancodeTeamPolicyChecks)
			}
			check := manifest.MancodeTeamPolicyChecks[0]
			if check.Status != test.status || !containsString(check.Issues, test.issue) || check.WorkspaceBindingVerified {
				t.Fatalf("team-policy binding state was hidden or overstated: %#v", check)
			}
			snapshot, err := CreateSnapshot(Mancode, root, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			coverage, err := BuildFormatCoverage(snapshot)
			if err != nil || len(coverage.MancodeTeamPolicyChecks) != 1 || coverage.MancodeTeamPolicyChecks[0].Status != test.status {
				t.Fatalf("nonverified binding did not remain visible in coverage: checks=%#v err=%v", coverage.MancodeTeamPolicyChecks, err)
			}
		})
	}
}

func TestMancodeTeamPolicyBindingIdentityMatchesMancodeRoot(t *testing.T) {
	projectRoot := t.TempDir()
	workspaceID := mancodeDecisionTestID("1")
	writeMancodeJSONFixture(t, projectRoot, ".mancode/shared/config.json", mancodeSharedProjectConfigFixture(workspaceID))
	writeMancodeJSONFixture(t, projectRoot, ".mancode/shared/team/policy.json", mancodeTeamPolicyFixture())
	projectManifest, err := Inspect(Mancode, projectRoot)
	if err != nil {
		t.Fatal(err)
	}

	legacyRoot := t.TempDir()
	writeMancodeJSONFixture(t, legacyRoot, ".mancode/shared/config.json", mancodeSharedProjectConfigFixture(workspaceID))
	writeMancodeJSONFixture(t, legacyRoot, ".mancode/shared/team/policy.json", mancodeTeamPolicyFixture())
	legacyManifest, err := Inspect(Mancode, filepath.Join(legacyRoot, ".mancode"))
	if err != nil {
		t.Fatal(err)
	}
	if len(projectManifest.MancodeTeamPolicyChecks) != 1 || len(legacyManifest.MancodeTeamPolicyChecks) != 1 ||
		projectManifest.MancodeTeamPolicyChecks[0].PairIDSHA256 != legacyManifest.MancodeTeamPolicyChecks[0].PairIDSHA256 {
		t.Fatalf("project-root and .mancode-root workspace-binding identities differ: %#v versus %#v",
			projectManifest.MancodeTeamPolicyChecks, legacyManifest.MancodeTeamPolicyChecks)
	}
}
