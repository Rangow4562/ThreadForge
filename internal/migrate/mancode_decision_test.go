package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func mancodeDecisionTestID(suffix string) string {
	return strings.Repeat("0", 25) + suffix
}

func mancodeConfirmedDecisionFixture(version int, decisionID string) map[string]any {
	actorID := mancodeDecisionTestID("2")
	value := map[string]any{
		"schemaVersion":      version,
		"decisionId":         decisionID,
		"title":              "Keep migration records review-only",
		"statement":          "Imported decisions remain advisory until a Threadforge reviewer accepts them.",
		"taskRef":            map[string]any{"namespace": "shared", "taskId": mancodeDecisionTestID("4")},
		"confirmedByActorId": actorID,
		"confirmedAt":        "2026-10-08T10:00:00Z",
		"operationId":        mancodeDecisionTestID("7"),
		"authorization": map[string]any{
			"schemaVersion": 1, "action": "confirmed_decision_publish", "actorId": actorID,
			"sessionId": mancodeDecisionTestID("3"), "trustBoundary": "repo-collaborators",
			"decisionDigest": "sha256:" + strings.Repeat("a", 64), "authorizedAt": "2026-10-08T10:00:00Z",
		},
	}
	if version == 2 {
		firstTarget, secondTarget := mancodeDecisionTestID("5"), mancodeDecisionTestID("6")
		firstDigest, secondDigest := "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64)
		value["details"] = map[string]any{
			"capability": "decision-relations:1", "recordKind": "decision",
			"rationale":    "Keep source decisions visible without inheriting their authority.",
			"alternatives": []any{map[string]any{"option": "Auto-approve", "reasonNotChosen": "The source authorization does not authorize Threadforge."}},
			"tradeoffs":    []any{"Review adds a human step."},
			"revisitWhen":  []any{"After source reconciliation."},
			"applicability": map[string]any{
				"modules": []any{"internal/migrate"}, "paths": []any{"internal/migrate/**"},
			},
			"clauses": []any{map[string]any{"id": "review-only", "statement": "Do not activate imported decisions automatically."}},
			"relations": []any{
				map[string]any{"action": "supersede", "targetId": firstTarget, "targetDigest": firstDigest, "clauses": "all"},
				map[string]any{"action": "supersede", "targetId": secondTarget, "targetDigest": secondDigest, "clauses": "all"},
			},
			"resolution": map[string]any{"records": []any{
				map[string]any{"decisionId": firstTarget, "digest": firstDigest},
				map[string]any{"decisionId": secondTarget, "digest": secondDigest},
			}},
		}
	}
	return value
}

func TestMancodeConfirmedDecisionV1SnapshotKeepsDecisionTextButDropsAuthority(t *testing.T) {
	root := t.TempDir()
	decisionID := mancodeDecisionTestID("1")
	writeMancodeJSONFixture(t, root, ".mancode/shared/memory/decisions/"+decisionID+".json", mancodeConfirmedDecisionFixture(1, decisionID))
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("confirmed decision did not inspect as supported: %#v", manifest.Summary)
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
	if len(records) != 1 || records[0].Type != "decision_record" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected confirmed-decision migration record: %#v", records)
	}
	var projection map[string]any
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["schema_version"] != mancodeConfirmedDecisionImportSchema ||
		projection["authority"] != "local_pending_review" || projection["human_review_required"] != true ||
		projection["execution_authorized"] != false || projection["legacy_confirmation_advisory"] != true ||
		projection["source_authorization_omitted"] != true || projection["source_schema_version"] != float64(1) ||
		projection["decision_id"] != decisionID || projection["title"] != "Keep migration records review-only" ||
		projection["task_reference_present"] != true || projection["details"] != nil {
		t.Fatalf("unexpected V1 confirmed-decision projection: %s", records[0].Payload)
	}
	for _, omitted := range []string{
		mancodeDecisionTestID("2"), mancodeDecisionTestID("3"), mancodeDecisionTestID("4"),
		mancodeDecisionTestID("7"), "decisionDigest", "confirmedByActorId", "operationId", "taskId",
	} {
		if strings.Contains(string(records[0].Payload), omitted) {
			t.Fatalf("confirmed-decision projection retained authority field %q: %s", omitted, records[0].Payload)
		}
	}
	if strings.Contains(string(records[0].Payload), `"authorization":`) {
		t.Fatalf("source authorization basis was retained: %s", records[0].Payload)
	}
}

func TestMancodeConfirmedDecisionV2SnapshotKeepsStructuredContinuity(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".mancode")
	decisionID := mancodeDecisionTestID("1")
	relative := "shared/memory/decisions/" + decisionID + ".json"
	fixture := mancodeConfirmedDecisionFixture(2, decisionID)
	if _, handled, err := normalizeMancodeConfirmedDecision(relative, fixture); !handled || err != nil {
		t.Fatalf("valid V2 confirmed-decision fixture was rejected: handled=%v err=%v", handled, err)
	}
	writeMancodeJSONFixture(t, root, relative, fixture)
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
	if len(records) != 1 || records[0].Type != "decision_record" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected V2 decision records: %#v", records)
	}
	var projection struct {
		SchemaVersion string `json:"schema_version"`
		SourceVersion int    `json:"source_schema_version"`
		Details       struct {
			RecordKind    string `json:"record_kind"`
			Rationale     string `json:"rationale"`
			Alternatives  []any  `json:"alternatives"`
			Applicability struct {
				Modules []string `json:"modules"`
				Paths   []string `json:"paths"`
			} `json:"applicability"`
			Clauses    []any `json:"clauses"`
			Relations  []any `json:"relations"`
			Resolution struct {
				Records []any `json:"records"`
			} `json:"resolution"`
		} `json:"details"`
	}
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.SchemaVersion != mancodeConfirmedDecisionImportSchema || projection.SourceVersion != 2 ||
		projection.Details.RecordKind != "decision" || projection.Details.Rationale == "" ||
		len(projection.Details.Alternatives) != 1 || len(projection.Details.Applicability.Modules) != 1 ||
		projection.Details.Applicability.Paths[0] != "internal/migrate/**" ||
		len(projection.Details.Clauses) != 1 || len(projection.Details.Relations) != 2 ||
		len(projection.Details.Resolution.Records) != 2 {
		t.Fatalf("V2 structured decision data did not survive review-only projection: %s", records[0].Payload)
	}
	for _, private := range []string{mancodeDecisionTestID("2"), mancodeDecisionTestID("3"), mancodeDecisionTestID("4"), mancodeDecisionTestID("7"), "decisionDigest"} {
		if strings.Contains(string(records[0].Payload), private) {
			t.Fatalf("V2 decision projection retained authority field %q: %s", private, records[0].Payload)
		}
	}
	if strings.Contains(string(records[0].Payload), `"authorization":`) {
		t.Fatalf("source authorization basis was retained in V2 projection: %s", records[0].Payload)
	}
}

func TestMancodeConfirmedDecisionV2RevocationRemainsReviewOnly(t *testing.T) {
	decisionID := mancodeDecisionTestID("1")
	value := mancodeConfirmedDecisionFixture(2, decisionID)
	details := value["details"].(map[string]any)
	details["recordKind"] = "revocation"
	details["clauses"] = []any{}
	for _, item := range details["relations"].([]any) {
		item.(map[string]any)["action"] = "revoke"
	}
	payloads, handled, err := normalizeMancodeConfirmedDecision("shared/memory/decisions/"+decisionID+".json", value)
	if !handled || err != nil || len(payloads) != 1 {
		t.Fatalf("valid V2 revocation did not normalize as one review record: handled=%v err=%v", handled, err)
	}
	var projection struct {
		Authority           string `json:"authority"`
		ExecutionAuthorized bool   `json:"execution_authorized"`
		Details             struct {
			RecordKind string `json:"record_kind"`
			Clauses    []any  `json:"clauses"`
			Relations  []struct {
				Action string `json:"action"`
			} `json:"relations"`
		} `json:"details"`
	}
	if err := json.Unmarshal(payloads[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Authority != "local_pending_review" || projection.ExecutionAuthorized ||
		projection.Details.RecordKind != "revocation" || len(projection.Details.Clauses) != 0 ||
		len(projection.Details.Relations) != 2 || projection.Details.Relations[0].Action != "revoke" {
		t.Fatalf("revocation authority or meaning changed during projection: %s", payloads[0].Payload)
	}
}

func TestMancodeConfirmedDecisionRejectsSchemaDriftAndUnsafeSemantics(t *testing.T) {
	cases := []struct {
		name    string
		version int
		mutate  func(map[string]any)
	}{
		{name: "unknown root field", version: 1, mutate: func(value map[string]any) { value["private"] = "value" }},
		{name: "filename mismatch", version: 1, mutate: func(value map[string]any) { value["decisionId"] = mancodeDecisionTestID("8") }},
		{name: "authorization actor mismatch", version: 1, mutate: func(value map[string]any) {
			value["authorization"].(map[string]any)["actorId"] = mancodeDecisionTestID("9")
		}},
		{name: "local task reference", version: 1, mutate: func(value map[string]any) { value["taskRef"].(map[string]any)["namespace"] = "local" }},
		{name: "credential-bearing statement", version: 1, mutate: func(value map[string]any) { value["statement"] = "API_TOKEN=private-token-value" }},
		{name: "unknown V2 details field", version: 2, mutate: func(value map[string]any) { value["details"].(map[string]any)["unknown"] = true }},
		{name: "unsafe applicability path", version: 2, mutate: func(value map[string]any) {
			value["details"].(map[string]any)["applicability"].(map[string]any)["paths"] = []any{"../outside"}
		}},
		{name: "inconsistent resolution", version: 2, mutate: func(value map[string]any) {
			value["details"].(map[string]any)["resolution"].(map[string]any)["records"].([]any)[0].(map[string]any)["digest"] = "sha256:" + strings.Repeat("d", 64)
		}},
	}
	decisionID := mancodeDecisionTestID("1")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := mancodeConfirmedDecisionFixture(tc.version, decisionID)
			tc.mutate(value)
			_, handled, err := normalizeMancodeConfirmedDecision(".mancode/shared/memory/decisions/"+decisionID+".json", value)
			if !handled || err == nil {
				t.Fatalf("unsupported confirmed decision was accepted: handled=%v err=%v", handled, err)
			}
		})
	}
}
