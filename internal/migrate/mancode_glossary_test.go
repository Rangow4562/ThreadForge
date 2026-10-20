package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func mancodeProjectGlossaryFixture() map[string]any {
	return map[string]any{
		"schemaVersion": 1,
		"revision":      5,
		"entries": []any{
			map[string]any{
				"term": "Shared Widget", "definition": "The reusable project interface shared across harnesses.",
				"aliases":       []any{"Widget", "Component"},
				"sourceTaskRef": map[string]any{"namespace": "shared", "taskId": mancodeDecisionTestID("4")},
				"confirmedAt":   "2026-10-08T10:00:00Z",
			},
			map[string]any{
				"term": "Review gate", "definition": "A human must accept imported knowledge before it is used.",
				"aliases": []any{}, "sourceTaskRef": nil, "confirmedAt": "2026-10-08T11:00:00Z",
			},
		},
	}
}

func TestMancodeProjectGlossarySnapshotPreservesReviewableTermsWithoutTaskIdentity(t *testing.T) {
	root := t.TempDir()
	writeMancodeJSONFixture(t, root, ".mancode/shared/context/glossary.json", mancodeProjectGlossaryFixture())
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("project glossary did not inspect as supported: %#v", manifest.Summary)
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
	if len(records) != 1 || records[0].Type != "project_glossary" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected project glossary migration records: %#v", records)
	}
	var projection struct {
		SchemaVersion       string `json:"schema_version"`
		Authority           string `json:"authority"`
		HumanReviewRequired bool   `json:"human_review_required"`
		ExecutionAuthorized bool   `json:"execution_authorized"`
		Revision            int    `json:"revision"`
		Entries             []struct {
			Term                       string   `json:"term"`
			Definition                 string   `json:"definition"`
			Aliases                    []string `json:"aliases"`
			ConfirmedAt                string   `json:"confirmed_at"`
			SourceTaskReferencePresent bool     `json:"source_task_reference_present"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.SchemaVersion != mancodeProjectGlossaryImportSchema || projection.Authority != "local_pending_review" ||
		!projection.HumanReviewRequired || projection.ExecutionAuthorized || projection.Revision != 5 || len(projection.Entries) != 2 ||
		projection.Entries[0].Term != "Shared Widget" || len(projection.Entries[0].Aliases) != 2 ||
		!projection.Entries[0].SourceTaskReferencePresent || projection.Entries[1].SourceTaskReferencePresent {
		t.Fatalf("project glossary lost safe continuity or gained authority: %s", records[0].Payload)
	}
	if strings.Contains(string(records[0].Payload), mancodeDecisionTestID("4")) || strings.Contains(string(records[0].Payload), "sourceTaskRef") {
		t.Fatalf("project glossary retained the source task identity: %s", records[0].Payload)
	}
}

func TestMancodeProjectGlossaryRejectsMalformedOrUnsafeRecords(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["projectRoot"] = "C:/private" }},
		{name: "unsupported version", mutate: func(value map[string]any) { value["schemaVersion"] = 2 }},
		{name: "negative revision", mutate: func(value map[string]any) { value["revision"] = -1 }},
		{name: "duplicate alias across terms", mutate: func(value map[string]any) {
			value["entries"].([]any)[1].(map[string]any)["term"] = "widget"
		}},
		{name: "local task reference", mutate: func(value map[string]any) {
			value["entries"].([]any)[0].(map[string]any)["sourceTaskRef"].(map[string]any)["namespace"] = "local"
		}},
		{name: "credential-like definition", mutate: func(value map[string]any) {
			value["entries"].([]any)[0].(map[string]any)["definition"] = "API_TOKEN=private-value"
		}},
		{name: "unknown entry field", mutate: func(value map[string]any) {
			value["entries"].([]any)[0].(map[string]any)["operationId"] = mancodeDecisionTestID("7")
		}},
		{name: "invalid confirmation timestamp", mutate: func(value map[string]any) {
			value["entries"].([]any)[0].(map[string]any)["confirmedAt"] = "not-a-time"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := mancodeProjectGlossaryFixture()
			tc.mutate(value)
			_, handled, err := normalizeMancodeProjectGlossary(value)
			if !handled || err == nil {
				t.Fatalf("invalid Mancode glossary was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
}

func TestMancodeProjectGlossaryAcceptsEmptyInitializedState(t *testing.T) {
	items, handled, err := normalizeMancodeProjectGlossary(map[string]any{
		"schemaVersion": 1, "revision": 0, "entries": []any{},
	})
	if !handled || err != nil || len(items) != 1 {
		t.Fatalf("empty initialized project glossary was not preserved: handled=%v err=%v items=%#v", handled, err, items)
	}
}
