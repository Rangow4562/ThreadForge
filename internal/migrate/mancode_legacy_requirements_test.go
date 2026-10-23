package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func legacyMancodeRequirementsFixture() map[string]any {
	coverage := make([]any, 0, len(mancodeLegacyRequirementDimensions))
	for _, dimension := range mancodeLegacyRequirementDimensions {
		coverage = append(coverage, map[string]any{
			"dimension": dimension, "status": "confirmed", "rationale": "private rationale for " + dimension,
		})
	}
	return map[string]any{
		"version":            json.Number("1"),
		"goal":               "  private requirements goal  ",
		"confirmedScope":     []any{" in-scope marker "},
		"excludedScope":      []any{"out-of-scope marker"},
		"technicalDecisions": []any{"use a local database"},
		"defaults":           []any{"default marker"},
		"blockingUnknowns":   []any{"clarify deployment target"},
		"coverage":           coverage,
		"acceptanceCriteria": []any{
			map[string]any{
				"id": "AC-CORE-1", "description": "private acceptance criterion", "required": true,
				"method": "automated", "verificationSurfaces": map[string]any{"automated": "unit"},
			},
			map[string]any{
				"id": "AC-UX-2", "description": "manual and automated criterion", "required": false,
				"method": "hybrid", "verificationSurfaces": map[string]any{
					"automated": "browser", "manual": "manual_observation",
				},
			},
		},
	}
}

func TestMancodeLegacyRequirementsArePreservedOnlyForLocalReview(t *testing.T) {
	root := t.TempDir()
	taskID := "20261023-private-requirements-task"
	relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "requirements.json"))
	writeMancodeJSONFixture(t, root, relative, legacyMancodeRequirementsFixture())

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 ||
		len(manifest.Files) != 1 || manifest.Files[0].ImportedCount != 1 || manifest.Files[0].Status != "ready" {
		t.Fatalf("legacy requirements did not reconcile: summary=%#v files=%#v", manifest.Summary, manifest.Files)
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
	if len(records) != 1 || records[0].Type != "mancode_legacy_requirements" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected legacy requirements records: %#v", records)
	}
	wantAlias := mancodeLegacyRequirementsSourceAlias(relative)
	if records[0].SourcePath != wantAlias || strings.Contains(records[0].SourcePath, taskID) || strings.Contains(records[0].Key, taskID) {
		t.Fatalf("legacy requirements source identity was not aliased: %#v", records[0])
	}
	var projection mancodeLegacyRequirementsProjection
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.SchemaVersion != mancodeLegacyRequirementsSchema || projection.Authority != "local_pending_review" ||
		!projection.HumanReviewRequired || projection.ExecutionAuthorized || projection.Readiness != "needs_clarification" ||
		projection.Requirements.Goal != "private requirements goal" || projection.Requirements.ConfirmedScope[0] != "in-scope marker" ||
		projection.Requirements.AcceptanceCriteria[1].VerificationSurfaces.Manual != "manual_observation" ||
		!projection.RawTaskIdentityOmitted || !projection.SourceAuthorityOmitted {
		t.Fatalf("legacy requirements projection lost required context or authority limits: %#v", projection)
	}
	if strings.Contains(string(records[0].Payload), taskID) {
		t.Fatalf("legacy requirements payload retained task identity: %s", records[0].Payload)
	}
}

func TestMancodeLegacyRequirementsSupportsDirectorySourceRootAndReadyLedger(t *testing.T) {
	root := t.TempDir()
	taskID := "20261023-ready-requirements"
	path := filepath.ToSlash(filepath.Join("workflows", taskID, "requirements.json"))
	value := legacyMancodeRequirementsFixture()
	value["blockingUnknowns"] = []any{}
	items, handled, err := normalizeMancodeItems(".mancode", path, value)
	if err != nil || !handled || len(items) != 1 || items[0].Type != "mancode_legacy_requirements" {
		t.Fatalf("directory-root requirements were not converted: handled=%v items=%#v err=%v", handled, items, err)
	}
	var projection mancodeLegacyRequirementsProjection
	if err := json.Unmarshal(items[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Readiness != "ready" || projection.ExecutionAuthorized || projection.TaskIDSHA256 != mancodeCoordinationIdentityHash("legacy-requirements-task-id", taskID) {
		t.Fatalf("ready requirements gained authority or incorrect task binding: %#v", projection)
	}
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", path)), value)
	manifest, err := Inspect(Mancode, filepath.Join(root, ".mancode"))
	if err != nil || manifest.Summary.RecordsImported != 1 {
		t.Fatalf("Mancode directory source did not import requirements: manifest=%#v err=%v", manifest, err)
	}
}

func TestMancodeLegacyRequirementsRejectsMalformedOrUnsupportedContracts(t *testing.T) {
	taskID := "20261023-invalid-requirements"
	path := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "requirements.json"))
	base := legacyMancodeRequirementsFixture()
	invalid := make([]map[string]any, 0)
	add := func(change func(map[string]any)) {
		value := make(map[string]any, len(base)+1)
		for key, item := range base {
			value[key] = item
		}
		change(value)
		invalid = append(invalid, value)
	}
	add(func(value map[string]any) { value["unexpected"] = "unknown field" })
	add(func(value map[string]any) { value["version"] = json.Number("2") })
	add(func(value map[string]any) { value["confirmedScope"] = []any{} })
	add(func(value map[string]any) {
		coverage := value["coverage"].([]any)
		value["coverage"] = coverage[:len(coverage)-1]
	})
	add(func(value map[string]any) {
		coverage := value["coverage"].([]any)
		coverage[1] = map[string]any{"dimension": "platform", "status": "confirmed", "rationale": "duplicate"}
	})
	add(func(value map[string]any) {
		coverage := value["coverage"].([]any)
		for _, raw := range coverage {
			item := raw.(map[string]any)
			if item["dimension"] == "technical_stack" {
				item["status"] = "defaulted"
			}
		}
		value["technicalDecisions"] = []any{}
	})
	add(func(value map[string]any) {
		criteria := value["acceptanceCriteria"].([]any)
		criteria[1].(map[string]any)["id"] = "AC-CORE-1"
	})
	add(func(value map[string]any) {
		criteria := value["acceptanceCriteria"].([]any)
		criteria[0].(map[string]any)["verificationSurfaces"] = map[string]any{"manual": "unit"}
	})
	add(func(value map[string]any) {
		criteria := value["acceptanceCriteria"].([]any)
		criteria[0].(map[string]any)["verificationSurfaces"] = map[string]any{"automated": "unit", "unknown": "device"}
	})
	add(func(value map[string]any) {
		criteria := value["acceptanceCriteria"].([]any)
		criteria[0].(map[string]any)["description"] = strings.Repeat("x", maxMancodeLegacyRequirementTextBytes+1)
	})
	for index, value := range invalid {
		if _, handled, err := normalizeMancodeItems("", path, value); !handled || err == nil {
			t.Errorf("invalid legacy requirements fixture %d was not rejected: handled=%v err=%v", index, handled, err)
		}
	}
	for _, unsafePath := range []string{
		".mancode/workflows/not_valid!/requirements.json",
		".mancode/local/workflows/20261023-invalid-requirements/requirements.json",
		".mancode/workflows/20261023-invalid-requirements/nested/requirements.json",
	} {
		if _, handled, err := normalizeMancodeItems("", unsafePath, base); handled || err != nil {
			t.Errorf("non-legacy requirements path entered converter: path=%q handled=%v err=%v", unsafePath, handled, err)
		}
	}
}

func TestMancodeLegacyRequirementsCredentialsAreExcludedBeforeSnapshot(t *testing.T) {
	root := t.TempDir()
	taskID := "20261023-private-requirements"
	relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "requirements.json"))
	value := legacyMancodeRequirementsFixture()
	criteria := value["acceptanceCriteria"].([]any)
	criteria[0].(map[string]any)["description"] = "use api_key=never-copy for the integration"
	writeMancodeJSONFixture(t, root, relative, value)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 0 || manifest.Summary.RecordsCredentialBlocked != 1 ||
		len(manifest.Files) != 1 || manifest.Files[0].Status != "credential_excluded" {
		t.Fatalf("credential-bearing requirements were not excluded: summary=%#v files=%#v", manifest.Summary, manifest.Files)
	}
}
