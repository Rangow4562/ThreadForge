package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func legacyMancodeVerificationFixture() map[string]any {
	return map[string]any{
		"version":            json.Number("1"),
		"planVersion":        json.Number("3"),
		"requirementsDigest": strings.Repeat("a", 64),
		"remediationRound":   json.Number("1"),
		"status":             "manual_required",
		"checks": []any{
			map[string]any{
				"acceptanceId": "AC-CORE-1", "required": true,
				"automated": map[string]any{
					"status": "passed", "evidence": "private api_key=never-copy verification details",
					"updatedAt": "2026-10-23T11:00:00.000Z", "command": "go test ./private/path",
					"exitCode": json.Number("0"), "evidenceFile": "reports/private-output.txt",
				},
			},
			map[string]any{
				"acceptanceId": "AC-UX-2", "required": true,
				"automated": map[string]any{
					"status": "failed", "evidence": "private failing output", "updatedAt": "2026-10-23T11:01:00Z",
					"command": "npm run private-check", "exitCode": json.Number("2"),
				},
				"manual": map[string]any{
					"status": "manual_required", "evidence": "private pending human test",
					"updatedAt": "2026-10-23T11:02:00Z",
				},
			},
			map[string]any{
				"acceptanceId": "AC-OPTIONAL-3", "required": false,
				"manual": map[string]any{
					"status": "failed", "evidence": "optional private note", "updatedAt": "2026-10-23T11:03:00Z",
				},
			},
		},
	}
}

func TestMancodeLegacyVerificationCreatesRedactedLocalSummary(t *testing.T) {
	root := t.TempDir()
	taskID := "20261023-private-verification-task"
	relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "verification-ledger.json"))
	writeMancodeJSONFixture(t, root, relative, legacyMancodeVerificationFixture())
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 ||
		len(manifest.Files) != 1 || manifest.Files[0].Status != "ready" {
		t.Fatalf("legacy verification did not reconcile: summary=%#v files=%#v", manifest.Summary, manifest.Files)
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
	if len(records) != 1 || records[0].Type != "mancode_legacy_verification_summary" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected legacy verification snapshot records: %#v", records)
	}
	wantAlias := mancodeLegacyVerificationSourceAlias(relative)
	if records[0].SourcePath != wantAlias || strings.Contains(records[0].SourcePath, taskID) || strings.Contains(records[0].Key, taskID) {
		t.Fatalf("legacy verification source identity was not aliased: %#v", records[0])
	}
	var projection mancodeLegacyVerificationProjection
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.SchemaVersion != mancodeLegacyVerificationSchema || projection.Authority != "local_pending_review" ||
		!projection.HumanReviewRequired || projection.ExecutionAuthorized || projection.SourceStatus != "manual_required" ||
		projection.PlanVersion != 3 || !projection.RequirementsDigestPresent || projection.RequirementsBindingVerified ||
		projection.PlanBindingVerified || projection.RemediationRound != 1 || len(projection.Checks) != 3 ||
		projection.Checks[0].AcceptanceID != "AC-CORE-1" || projection.Checks[0].Automated.ExitCode == nil ||
		*projection.Checks[0].Automated.ExitCode != 0 || !projection.Checks[0].Automated.EvidenceFilePresent ||
		projection.Checks[1].Automated.Status != "failed" || projection.Checks[1].Manual.Status != "manual_required" ||
		!projection.RawEvidenceOmitted || !projection.RawCommandsOmitted || !projection.RawEvidencePathsOmitted ||
		!projection.RawTimestampsOmitted || !projection.RawRequirementsDigestOmitted {
		t.Fatalf("legacy verification summary lost typed state or authority limits: %#v", projection)
	}
	payload := string(records[0].Payload)
	for _, private := range []string{
		taskID, "private api_key=never-copy", "private failing output", "private pending human test",
		"optional private note", "go test ./private/path", "npm run private-check", "reports/private-output.txt",
		strings.Repeat("a", 64), "2026-10-23T11:00:00.000Z",
	} {
		if strings.Contains(payload, private) {
			t.Fatalf("legacy verification projection retained omitted value %q: %s", private, payload)
		}
	}
}

func TestMancodeLegacyVerificationSupportsDirectorySourceRoot(t *testing.T) {
	root := t.TempDir()
	taskID := "20261023-verification-directory-root"
	relative := filepath.ToSlash(filepath.Join("workflows", taskID, "verification-ledger.json"))
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", relative)), legacyMancodeVerificationFixture())
	manifest, err := Inspect(Mancode, filepath.Join(root, ".mancode"))
	if err != nil || manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Mancode directory source did not import verification ledger: manifest=%#v err=%v", manifest, err)
	}
}

func TestMancodeLegacyVerificationRejectsInvalidStatusBindingsAndShapes(t *testing.T) {
	taskID := "20261023-invalid-verification"
	path := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "verification-ledger.json"))
	invalid := make([]map[string]any, 0)
	add := func(change func(map[string]any)) {
		value := legacyMancodeVerificationFixture()
		change(value)
		invalid = append(invalid, value)
	}
	add(func(value map[string]any) { value["unknown"] = "reject unknown fields" })
	add(func(value map[string]any) { value["version"] = json.Number("2") })
	add(func(value map[string]any) { value["planVersion"] = json.Number("0") })
	add(func(value map[string]any) { value["requirementsDigest"] = "not-a-sha256" })
	add(func(value map[string]any) { value["status"] = "passed" })
	add(func(value map[string]any) {
		checks := value["checks"].([]any)
		checks[2].(map[string]any)["acceptanceId"] = "AC-CORE-1"
	})
	add(func(value map[string]any) {
		checks := value["checks"].([]any)
		checks[0].(map[string]any)["automated"].(map[string]any)["exitCode"] = json.Number("1")
	})
	add(func(value map[string]any) {
		checks := value["checks"].([]any)
		checks[0].(map[string]any)["automated"].(map[string]any)["status"] = "manual_required"
	})
	add(func(value map[string]any) {
		checks := value["checks"].([]any)
		checks[0].(map[string]any)["automated"].(map[string]any)["updatedAt"] = "invalid-time"
	})
	add(func(value map[string]any) {
		checks := value["checks"].([]any)
		checks[0].(map[string]any)["automated"].(map[string]any)["evidence"] = strings.Repeat("x", maxMancodeLegacyVerificationText+1)
	})
	add(func(value map[string]any) {
		checks := value["checks"].([]any)
		checks[0].(map[string]any)["automated"].(map[string]any)["evidence"] = ""
	})
	for index, value := range invalid {
		if _, handled, err := normalizeMancodeItems("", path, value); !handled || err == nil {
			t.Errorf("invalid legacy verification fixture %d was not rejected: handled=%v err=%v", index, handled, err)
		}
	}
	for _, unsafePath := range []string{
		".mancode/workflows/not_valid!/verification-ledger.json",
		".mancode/local/workflows/01ARZ3NDEKTSV4RRFFQ69G5FAV/verification-ledger.json",
		".mancode/workflows/20261023-invalid-verification/nested/verification-ledger.json",
	} {
		if _, handled, err := normalizeMancodeItems("", unsafePath, legacyMancodeVerificationFixture()); handled || err != nil {
			t.Errorf("non-legacy verification path entered converter: path=%q handled=%v err=%v", unsafePath, handled, err)
		}
	}
}
