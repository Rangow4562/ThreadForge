package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rrsiProposalFixture(status string) map[string]any {
	edits := []any{map[string]any{
		"id": "C1", "component": "memory", "hypothesis": "private hypothesis with api_key=hidden-value",
		"targets_mode": "private target", "why_not_lower_lever": "private rationale",
		"trigger_condition": "private trigger", "predicted_affected": []any{"private-task-id"},
		"retroactive_check": "private check", "regression_risk": "private risk",
		"mechanism": "private mechanism",
	}}
	if status != "done" {
		edits = []any{}
	}
	return map[string]any{
		"status": status, "summary": "private summary", "edits": edits,
		"mechanism": "private top-level mechanism", "targets_mode": "private top-level target",
		"n_edits": float64(1), "reason": "private rejection reason",
		"log": []any{map[string]any{"action": "read_trace", "prompt": "private interaction text"}},
	}
}

func TestNormalizeRRSIProposalKeepsOnlyBoundedReviewMetadata(t *testing.T) {
	path := "runs/workspace/r0/a/proposal.json"
	items, handled, err := normalizeRRSIItems(path, rrsiProposalFixture("done"))
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("normalize RRSI proposal: handled=%t items=%d err=%v", handled, len(items), err)
	}
	if items[0].Type != "evolution_proposal" {
		t.Fatalf("unexpected proposal record type: %q", items[0].Type)
	}
	var summary map[string]any
	if err := json.Unmarshal(items[0].Payload, &summary); err != nil {
		t.Fatal(err)
	}
	if summary["schema_version"] != rrsiProposalSummarySchema || summary["domain"] != "workspace" ||
		summary["proposal_status"] != "done" || summary["file_edit_count"] != float64(1) ||
		summary["declared_edit_count"] != float64(1) || summary["authority"] != "local_pending_review" ||
		summary["human_review_required"] != true || summary["selection_accepted"] != false ||
		summary["execution_authorized"] != false {
		t.Fatalf("proposal was not kept review-only: %#v", summary)
	}
	components, ok := summary["edit_components"].([]any)
	if !ok || len(components) != 1 || components[0] != "memory" {
		t.Fatalf("allowlisted component summary is missing: %#v", summary["edit_components"])
	}
	for _, private := range []string{
		"private", "api_key", "hidden-value", "private-task-id", "interaction text",
	} {
		if strings.Contains(string(items[0].Payload), private) {
			t.Fatalf("proposal payload retained omitted source material %q: %s", private, items[0].Payload)
		}
	}
}

func TestRRSIProposalSchemaStatusesAndPathBoundRepairFiles(t *testing.T) {
	for _, status := range []string{"abort", "max_turns"} {
		if _, err := normalizeRRSIProposal("coding", rrsiProposalFixture(status)); err != nil {
			t.Errorf("valid %s proposal status rejected: %v", status, err)
		}
	}
	validPaths := []string{
		"runs/workspace/r0/a/proposal.json",
		"runs/eng/r7/h/proposal_r2.json",
	}
	for _, path := range validPaths {
		if !isRRSIProposalPath(path) {
			t.Errorf("documented RRSI proposal path was not recognized: %s", path)
		}
	}
	invalidPaths := []string{
		"runs/workspace/r0/proposal.json",
		"runs/workspace/jobs/base/proposal.json",
		"runs/workspace/r0/proposal_r0.json",
		"runs/workspace/r0/proposal_r2-extra.json",
		"runs/workspace/r0/v0/proposal.json",
		"runs/unknown/r0/v0/proposal.json",
		"runs/workspace/r0/v0/proposal.txt",
	}
	for _, path := range invalidPaths {
		if isRRSIProposalPath(path) {
			t.Errorf("unowned or malformed path was recognized as an RRSI proposal: %s", path)
		}
	}
}

func TestNormalizeRRSIProposalRejectsUnsupportedProducerShapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown top-level field", mutate: func(value map[string]any) { value["accepted"] = true }},
		{name: "unsupported status", mutate: func(value map[string]any) { value["status"] = "selected" }},
		{name: "unbounded file edit count", mutate: func(value map[string]any) { value["n_edits"] = float64(81) }},
		{name: "unknown component", mutate: func(value map[string]any) {
			value["edits"].([]any)[0].(map[string]any)["component"] = "arbitrary"
		}},
		{name: "unknown nested field", mutate: func(value map[string]any) {
			value["edits"].([]any)[0].(map[string]any)["patch"] = "must not migrate"
		}},
		{name: "non-done status with edits", mutate: func(value map[string]any) { value["status"] = "abort"; value["edits"] = []any{map[string]any{}} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := rrsiProposalFixture("done")
			test.mutate(value)
			if _, err := normalizeRRSIProposal("workspace", value); err == nil {
				t.Fatal("invalid RRSI proposal was accepted")
			}
		})
	}
	if _, err := decodeStrictRRSIProposalJSON([]byte(`{"status":"done","status":"abort"}`)); err == nil {
		t.Fatal("duplicate JSON keys were accepted")
	}
}

func TestRRSIProposalMigrationSnapshotReadbackAndCoverage(t *testing.T) {
	root := t.TempDir()
	for _, fixture := range []struct {
		path   string
		status string
	}{
		{"runs/workspace/r0/a/proposal.json", "done"},
		{"runs/workspace/r0/a/proposal_r2.json", "abort"},
	} {
		data, err := json.Marshal(rrsiProposalFixture(fixture.status))
		if err != nil {
			t.Fatal(err)
		}
		writeMancodeFixture(t, root, fixture.path, data)
	}
	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesScanned != 2 || manifest.Summary.RecordsFound != 2 ||
		manifest.Summary.RecordsImported != 2 || manifest.Summary.RecordsRejected != 0 ||
		manifest.Summary.RecordsCredentialBlocked != 0 {
		t.Fatalf("RRSI proposal files did not reconcile: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(RRSI, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(RRSI), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("proposal snapshot produced %d records, want 2", len(records))
	}
	for _, record := range records {
		if record.Type != "evolution_proposal" || record.Scope != "local_pending_review" {
			t.Fatalf("proposal migration granted authority or wrong record type: %#v", record)
		}
		for _, private := range []string{"private", "api_key", "hidden-value", "private-task-id", "interaction text"} {
			if strings.Contains(string(record.Payload), private) {
				t.Fatalf("snapshot retained raw proposal material %q", private)
			}
		}
	}
	report, err := BuildFormatCoverage(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if report.UnconvertedFiles != 0 || report.RejectedRecords != 0 {
		t.Fatalf("proposal converter remains unconverted or rejected: %#v", report)
	}
	for _, format := range report.Formats {
		if format.ID == "rrsi.proposal-json" {
			if format.Files != 2 || format.RecordsImported != 2 || format.UnconvertedFiles != 0 {
				t.Fatalf("proposal format coverage did not reconcile: %#v", format)
			}
			return
		}
	}
	t.Fatal("proposal format is missing from the fixed RRSI coverage catalog")
}

func TestRRSIProposalMalformedFileIsRejectedWithoutSnapshotContent(t *testing.T) {
	root := t.TempDir()
	writeMancodeFixture(t, root, "runs/coding/r0/a/proposal.json", []byte(`{"status":"done","status":"abort"}`))
	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsRejected != 1 || manifest.Summary.RecordsImported != 0 {
		t.Fatalf("malformed proposal was not explicitly rejected: %#v", manifest.Summary)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Status != "unsupported" || manifest.Files[0].SnapshotPath != "" {
		t.Fatalf("malformed proposal content was staged: %#v", manifest.Files)
	}
	if _, err := os.Stat(filepath.Join(root, "runs/coding/r0/a/proposal.json")); err != nil {
		t.Fatalf("read-only import altered the source fixture: %v", err)
	}
}
