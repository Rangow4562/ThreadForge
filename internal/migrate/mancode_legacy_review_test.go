package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func legacyMancodeReviewFixture() map[string]any {
	return map[string]any{
		"version":          "1.0",
		"depth":            "full",
		"requiredDomains":  []any{"quality", "security"},
		"completedDomains": []any{"quality"},
		"reports":          map[string]any{"quality": "reports/quality-review.md"},
		"blockers": []any{
			map[string]any{"id": "QUALITY-1", "domain": "quality", "status": "open"},
			map[string]any{"id": "QUALITY-2", "domain": "quality", "status": "resolved"},
		},
		"remediationRounds": json.Number("1"),
	}
}

func TestMancodeLegacyReviewCreatesRedactedLocalReviewSummary(t *testing.T) {
	root := t.TempDir()
	taskID := "20261023-private-review-task"
	relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "review-ledger.json"))
	writeMancodeJSONFixture(t, root, relative, legacyMancodeReviewFixture())
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 ||
		len(manifest.Files) != 1 || manifest.Files[0].Status != "ready" {
		t.Fatalf("legacy review did not reconcile: summary=%#v files=%#v", manifest.Summary, manifest.Files)
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
	if len(records) != 1 || records[0].Type != "mancode_legacy_review_summary" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected legacy review snapshot records: %#v", records)
	}
	wantAlias := mancodeLegacyReviewSourceAlias(relative)
	if records[0].SourcePath != wantAlias || strings.Contains(records[0].SourcePath, taskID) || strings.Contains(records[0].Key, taskID) {
		t.Fatalf("legacy review source identity was not aliased: %#v", records[0])
	}
	var projection mancodeLegacyReviewProjection
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.SchemaVersion != mancodeLegacyReviewSchema || projection.Authority != "local_pending_review" ||
		!projection.HumanReviewRequired || projection.ExecutionAuthorized || projection.Depth != "full" ||
		len(projection.Domains) != 2 || projection.Domains[0].Domain != "quality" || !projection.Domains[0].Completed ||
		!projection.Domains[0].ReportPresent || projection.Domains[0].OpenBlockerCount != 1 ||
		projection.Domains[0].ResolvedBlockerCount != 1 || projection.Domains[1].Domain != "security" ||
		projection.Domains[1].Completed || projection.Domains[1].ReportPresent || projection.RemediationRounds != 1 ||
		!projection.BlockerIDsOmitted || !projection.ReportPathsOmitted || !projection.RawTaskIdentityOmitted {
		t.Fatalf("legacy review summary lost safe lifecycle details or authority limits: %#v", projection)
	}
	payload := string(records[0].Payload)
	for _, private := range []string{taskID, "reports/quality-review.md", "QUALITY-1", "QUALITY-2"} {
		if strings.Contains(payload, private) {
			t.Fatalf("legacy review summary retained omitted value %q: %s", private, payload)
		}
	}
}

func TestMancodeLegacyReviewSupportsSkippedLedgerAndMancodeRoot(t *testing.T) {
	root := t.TempDir()
	taskID := "20261023-skipped-review"
	relative := filepath.ToSlash(filepath.Join("workflows", taskID, "review-ledger.json"))
	value := map[string]any{
		"version": "1.0", "depth": "targeted", "requiredDomains": []any{}, "completedDomains": []any{},
		"reports": map[string]any{}, "blockers": []any{}, "remediationRounds": json.Number("0"),
		"skipped": map[string]any{
			"reason": "private skip reason api_key=must-not-copy", "recordedAt": "2026-10-23T11:30:00.000Z",
		},
	}
	items, handled, err := normalizeMancodeItems(".mancode", relative, value)
	if err != nil || !handled || len(items) != 1 || items[0].Type != "mancode_legacy_review_summary" {
		t.Fatalf("skipped Mancode-root review was not converted: handled=%v items=%#v err=%v", handled, items, err)
	}
	var projection mancodeLegacyReviewProjection
	if err := json.Unmarshal(items[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if !projection.Skipped || !projection.SkipReasonPresent || len(projection.Domains) != 0 ||
		projection.SkipRecordedAt != "2026-10-23T11:30:00Z" || strings.Contains(string(items[0].Payload), "must-not-copy") ||
		projection.ExecutionAuthorized {
		t.Fatalf("skipped review exposed its reason or gained authority: %#v", projection)
	}
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", relative)), value)
	manifest, err := Inspect(Mancode, filepath.Join(root, ".mancode"))
	if err != nil || manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Mancode directory source did not import the skipped review: manifest=%#v err=%v", manifest, err)
	}
}

func TestMancodeLegacyReviewRejectsMalformedAndUnsafeLedgers(t *testing.T) {
	taskID := "20261023-invalid-review"
	path := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "review-ledger.json"))
	invalid := []map[string]any{}
	add := func(change func(map[string]any)) {
		value := legacyMancodeReviewFixture()
		change(value)
		invalid = append(invalid, value)
	}
	add(func(value map[string]any) { value["unexpected"] = "unknown field" })
	add(func(value map[string]any) { value["version"] = "2.0" })
	add(func(value map[string]any) { value["requiredDomains"] = []any{"quality", "quality"} })
	add(func(value map[string]any) { value["completedDomains"] = []any{"security"} })
	add(func(value map[string]any) { value["reports"] = map[string]any{} })
	add(func(value map[string]any) { value["reports"] = map[string]any{"quality": "../outside/report.md"} })
	add(func(value map[string]any) { value["reports"] = map[string]any{"quality": "C:\\private\\review.md"} })
	add(func(value map[string]any) { value["remediationRounds"] = json.Number("2") })
	add(func(value map[string]any) {
		value["blockers"] = []any{
			map[string]any{"id": "DUPLICATE", "domain": "quality", "status": "open"},
			map[string]any{"id": "DUPLICATE", "domain": "quality", "status": "resolved"},
		}
	})
	add(func(value map[string]any) {
		value["skipped"] = map[string]any{"reason": "skip", "recordedAt": "not-a-time"}
	})
	add(func(value map[string]any) {
		value["skipped"] = map[string]any{"reason": "skip", "recordedAt": "2026-10-23T11:30:00Z"}
	})
	for index, value := range invalid {
		if _, handled, err := normalizeMancodeItems("", path, value); !handled || err == nil {
			t.Errorf("invalid legacy review fixture %d was not rejected: handled=%v err=%v", index, handled, err)
		}
	}
	for _, unsafePath := range []string{
		".mancode/workflows/not_valid!/review-ledger.json",
		".mancode/local/workflows/01ARZ3NDEKTSV4RRFFQ69G5FAV/review-ledger.json",
		".mancode/workflows/20261023-invalid-review/nested/review-ledger.json",
	} {
		if _, handled, err := normalizeMancodeItems("", unsafePath, legacyMancodeReviewFixture()); handled || err != nil {
			t.Errorf("non-legacy review path entered converter: path=%q handled=%v err=%v", unsafePath, handled, err)
		}
	}
}
