package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func mancodeSchemaManifestFixture(version int64) map[string]any {
	manifest := map[string]any{
		"manifestVersion":  version,
		"layoutVersion":    3,
		"epoch":            mancodeDecisionTestID("1"),
		"activationState":  "initializing",
		"minReaderVersion": "0.3.0",
		"minWriterVersion": "0.3.0",
		"activatedAt":      nil,
		"legacyBaseline":   nil,
		"managedAdapters":  map[string]any{"codex": "0.1.0"},
		"lastOperationId":  nil,
	}
	if version >= 2 {
		manifest["activationState"] = "dual_read"
		manifest["minReaderVersion"] = "0.4.0"
		manifest["minWriterVersion"] = "0.4.0"
		manifest["legacyBaseline"] = map[string]any{
			"stateDigest":         "sha256:" + strings.Repeat("a", 64),
			"workflowIndexDigest": "sha256:" + strings.Repeat("b", 64),
		}
		manifest["workflowPolicyDefaults"] = map[string]any{"planning": 2}
	}
	if version == 3 {
		manifest["activationState"] = "v3_active"
		manifest["minReaderVersion"] = "0.6.5"
		manifest["minWriterVersion"] = "0.6.5"
		manifest["activatedAt"] = "2026-10-08T10:00:00Z"
		manifest["legacyBaseline"] = nil
		manifest["lastOperationId"] = mancodeDecisionTestID("2")
		manifest["workflowPolicyDefaults"] = map[string]any{"planning": 1}
		manifest["privacyPolicy"] = map[string]any{
			"revision": 2, "digest": "sha256:" + strings.Repeat("c", 64),
		}
	}
	return manifest
}

func TestMancodeSchemaManifestVersionsBecomeHistoricalSummary(t *testing.T) {
	for _, version := range []int64{1, 2, 3} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			root := t.TempDir()
			fixture := mancodeSchemaManifestFixture(version)
			writeMancodeJSONFixture(t, root, ".mancode/schema.json", fixture)
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 {
				t.Fatalf("valid schema manifest V%d did not convert: %#v", version, manifest.Summary)
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
			if len(records) != 1 || records[0].Type != "mancode_schema_manifest" || records[0].Scope != "local_pending_review" {
				t.Fatalf("schema manifest did not stay historical: %#v", records)
			}
			var projection map[string]any
			if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
				t.Fatal(err)
			}
			if projection["schema_version"] != mancodeSchemaManifestImportSchema ||
				projection["source_manifest_version"] != float64(version) || projection["source_activation_is_authority"] != false ||
				projection["authority"] != "local_pending_review" || projection["execution_authorized"] != false ||
				projection["source_epoch_omitted"] != true || projection["source_operation_id_omitted"] != true {
				t.Fatalf("schema manifest activated source authority or lost version: %s", records[0].Payload)
			}
			for _, private := range []string{
				mancodeDecisionTestID("1"), mancodeDecisionTestID("2"),
				"sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("b", 64), "sha256:" + strings.Repeat("c", 64),
				"lastOperationId", "privacyPolicy.digest", "stateDigest",
			} {
				if strings.Contains(string(records[0].Payload), private) {
					t.Fatalf("schema-manifest summary retained source identity/digest %q: %s", private, records[0].Payload)
				}
			}
		})
	}
}

func TestMancodeSchemaManifestRejectsUnsupportedContracts(t *testing.T) {
	cases := []struct {
		name    string
		version int64
		mutate  func(map[string]any)
	}{
		{name: "unknown root field", version: 3, mutate: func(value map[string]any) { value["private"] = "value" }},
		{name: "unsupported manifest version", version: 3, mutate: func(value map[string]any) { value["manifestVersion"] = 4 }},
		{name: "wrong layout version", version: 3, mutate: func(value map[string]any) { value["layoutVersion"] = 2 }},
		{name: "invalid epoch", version: 3, mutate: func(value map[string]any) { value["epoch"] = "invalid" }},
		{name: "invalid activation state", version: 3, mutate: func(value map[string]any) { value["activationState"] = "active" }},
		{name: "invalid semver", version: 3, mutate: func(value map[string]any) { value["minReaderVersion"] = "0.6" }},
		{name: "v3 reader floor", version: 3, mutate: func(value map[string]any) { value["minReaderVersion"] = "0.6.4" }},
		{name: "v2 defaults unsupported", version: 2, mutate: func(value map[string]any) { value["workflowPolicyDefaults"].(map[string]any)["planning"] = 1 }},
		{name: "v3 defaults unsupported", version: 3, mutate: func(value map[string]any) { value["workflowPolicyDefaults"].(map[string]any)["planning"] = 3 }},
		{name: "invalid adapter key", version: 3, mutate: func(value map[string]any) { value["managedAdapters"].(map[string]any)["other-agent"] = "1.0.0" }},
		{name: "empty adapter version", version: 3, mutate: func(value map[string]any) { value["managedAdapters"].(map[string]any)["codex"] = " " }},
		{name: "v3 missing privacy reference", version: 3, mutate: func(value map[string]any) { delete(value, "privacyPolicy") }},
		{name: "invalid privacy digest", version: 3, mutate: func(value map[string]any) { value["privacyPolicy"].(map[string]any)["digest"] = "not-a-digest" }},
		{name: "initializing cannot have baseline", version: 1, mutate: func(value map[string]any) {
			value["legacyBaseline"] = map[string]any{"stateDigest": "sha256:" + strings.Repeat("a", 64), "workflowIndexDigest": "sha256:" + strings.Repeat("b", 64)}
		}},
		{name: "dual read requires baseline", version: 2, mutate: func(value map[string]any) { value["legacyBaseline"] = nil }},
		{name: "v3 active requires timestamp", version: 3, mutate: func(value map[string]any) { value["activatedAt"] = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := mancodeSchemaManifestFixture(tc.version)
			tc.mutate(value)
			_, handled, err := normalizeMancodeSchemaManifest(value)
			if !handled || err == nil {
				t.Fatalf("unsupported schema manifest was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
}
