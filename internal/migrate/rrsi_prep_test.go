package migrate

import (
	"encoding/json"
	"strings"
	"testing"
)

const rrsiPrepTestEdit = `{"id":"C1","component":"skill","hypothesis":"private-hypothesis","targets_mode":"private-target","predicted_affected":["private-task-id"],"retroactive_check":"private-counterfactual","mechanism":"private-mechanism"}`

func TestRRSIPrepPathsAndExactSourceLayout(t *testing.T) {
	for _, supported := range []string{
		"runs/coding/r0/A/prep.json",
		"runs/workspace/r12/b/prep.json",
		"runs/eng/r3/H/prep.json",
	} {
		if !isRRSIPrepPath(supported) {
			t.Errorf("documented RRSI prep path was rejected: %s", supported)
		}
		if !isRRSISafelyReadPath(supported) {
			t.Errorf("documented RRSI prep path is not safe-read allowlisted: %s", supported)
		}
	}
	for _, unsupported := range []string{
		"runs/coding/jobs/base/task__trial/prep.json",
		"runs/coding/r0/I/prep.json",
		"runs/coding/r0/A/prep.json.bak",
		"runs/coding/r00/A/prep.json",
		"runs/unknown/r0/A/prep.json",
		"runs/coding/r0/A/subdir/prep.json",
	} {
		if isRRSIPrepPath(unsupported) {
			t.Errorf("unsupported RRSI prep path was accepted: %s", unsupported)
		}
	}
}

func TestRRSIPrepNormalizationDropsEditTextAndLocalIdentities(t *testing.T) {
	fixtures := []struct {
		path string
		data string
	}{
		{
			"runs/coding/r0/A/prep.json",
			`{"commit":"a1b2c3d","branch":"coding/r0A","edits":[` + rrsiPrepTestEdit + `],"diff_path":"C:\\private\\repo\\runs\\coding\\r0\\A\\diff.patch","mechanism":"private-mechanism"}`,
		},
		{
			"runs/workspace/r1/B/prep.json",
			`{"gate_failure":"no_proposal","detail":"private-proposal-diagnostic","edits":[],"diff_path":null}`,
		},
		{
			"runs/eng/r2/C/prep.json",
			`{"gate_failure":"critic_reject","detail":"private-critic-diagnostic","edits":[` + rrsiPrepTestEdit + `],"diff_path":"/private/repo/diff.patch"}`,
		},
		{
			"runs/coding/r3/D/prep.json",
			`{"gate_failure":"smoke_fail","detail":"private-smoke-diagnostic","edits":[` + rrsiPrepTestEdit + `],"diff_path":"/private/repo/diff.patch"}`,
		},
	}
	for _, fixture := range fixtures {
		item, alias, err := normalizeRRSIPrepFile(fixture.path, []byte(fixture.data))
		if err != nil {
			t.Fatalf("normalize %s: %v", fixture.path, err)
		}
		if item.Type != rrsiPrepRecordType || strings.Contains(alias, "runs/") || strings.Contains(alias, "prep.json") {
			t.Fatalf("RRSI prep alias/type retained source paths: %s %s", item.Type, alias)
		}
		for _, private := range []string{
			"a1b2c3d", "coding/r0A", "private/repo", "private-hypothesis", "private-target",
			"private-task-id", "private-counterfactual", "private-mechanism", "private-proposal-diagnostic",
			"private-critic-diagnostic", "private-smoke-diagnostic", "diff.patch",
		} {
			if strings.Contains(string(item.Payload), private) || strings.Contains(alias, private) {
				t.Fatalf("RRSI prep summary retained private source value %q: %s", private, item.Payload)
			}
		}
		entry := FileEntry{Path: fixture.path, SourceSHA256: digest([]byte(fixture.data)), SourceBytes: int64(len(fixture.data))}
		record := Record{Type: item.Type, SourcePath: alias, Scope: "local_pending_review", Payload: item.Payload}
		if err := validateRRSIPrepSummary(record, entry); err != nil {
			t.Fatalf("valid RRSI prep summary failed readback: %v", err)
		}
		var summary rrsiPrepSummary
		if err := json.Unmarshal(item.Payload, &summary); err != nil {
			t.Fatal(err)
		}
		if summary.SchemaVersion != rrsiPrepSummarySchema || summary.Domain == "" ||
			!summary.HumanReviewRequired || summary.SelectionAccepted || summary.ExecutionAuthorized ||
			summary.CutoverEvidenceEligible {
			t.Fatalf("RRSI prep summary crossed its review-only boundary: %#v", summary)
		}
		if fixture.path == "runs/coding/r0/A/prep.json" &&
			(!summary.CommitPresent || !summary.BranchMatchesIdentity || summary.EditCount != 1 ||
				summary.EditComponentCounts["skill"] != 1 || summary.DiagnosticBytes != 0) {
			t.Fatalf("committed candidate summary lost bounded preparation evidence: %#v", summary)
		}
	}
	nullOptionalMechanism := strings.Replace(rrsiPrepTestEdit, `"mechanism":"private-mechanism"`, `"mechanism":null`, 1)
	if _, _, err := normalizeRRSIPrepFile("runs/coding/r0/A/prep.json", []byte(
		`{"commit":"a1b2c3d","branch":"coding/r0A","edits":[`+nullOptionalMechanism+`],"diff_path":"/private/diff.patch","mechanism":"candidate"}`)); err != nil {
		t.Fatalf("nullable optional source mechanism should remain convertible when omitted: %v", err)
	}
}

func TestRRSIPrepRejectsMalformedProducerShapesAndTamperedSummaries(t *testing.T) {
	path := "runs/coding/r0/A/prep.json"
	valid := `{"commit":"a1b2c3d","branch":"coding/r0A","edits":[` + rrsiPrepTestEdit + `],"diff_path":"/private/diff.patch","mechanism":"private mechanism"}`
	item, alias, err := normalizeRRSIPrepFile(path, []byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	entry := FileEntry{Path: path, SourceSHA256: digest([]byte(valid)), SourceBytes: int64(len(valid))}
	record := Record{Type: item.Type, SourcePath: alias, Payload: item.Payload}
	var original map[string]any
	if err := json.Unmarshal(item.Payload, &original); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"source digest":             func(value map[string]any) { value["source_sha256"] = strings.Repeat("0", 64) },
		"acceptance flag":           func(value map[string]any) { value["selection_accepted"] = true },
		"execution authority":       func(value map[string]any) { value["execution_authorized"] = true },
		"cutover eligibility":       func(value map[string]any) { value["cutover_evidence_eligible"] = true },
		"nullable component counts": func(value map[string]any) { value["edit_component_counts"] = nil },
		"component sum":             func(value map[string]any) { value["edit_component_counts"].(map[string]any)["skill"] = float64(0) },
		"commit marker":             func(value map[string]any) { value["commit_present"] = false },
		"branch marker":             func(value map[string]any) { value["branch_matches_identity"] = false },
		"unexpected field":          func(value map[string]any) { value["hypothesis"] = "private" },
	} {
		copy := make(map[string]any, len(original))
		for key, value := range original {
			copy[key] = value
		}
		if name == "nullable component counts" || name == "component sum" {
			componentCounts := make(map[string]any, len(original["edit_component_counts"].(map[string]any)))
			for key, value := range original["edit_component_counts"].(map[string]any) {
				componentCounts[key] = value
			}
			copy["edit_component_counts"] = componentCounts
		}
		mutate(copy)
		record.Payload, err = json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRRSIPrepSummary(record, entry); err == nil {
			t.Errorf("RRSI prep readback accepted tampered %s", name)
		}
	}

	invalid := []struct{ path, data string }{
		{path, `{"commit":"a1b2c3d","branch":"other/r0A","edits":[` + rrsiPrepTestEdit + `],"diff_path":"/private/diff.patch","mechanism":"x"}`},
		{path, `{"commit":"not-a-commit","branch":"coding/r0A","edits":[` + rrsiPrepTestEdit + `],"diff_path":"/private/diff.patch","mechanism":"x"}`},
		{path, `{"commit":"a1b2c3d","commit":"f1e2d3c","branch":"coding/r0A","edits":[` + rrsiPrepTestEdit + `],"diff_path":"/private/diff.patch","mechanism":"x"}`},
		{path, `{"commit":"a1b2c3d","branch":"coding/r0A","edits":[` + strings.Replace(rrsiPrepTestEdit, `"skill"`, `"unknown_component"`, 1) + `],"diff_path":"/private/diff.patch","mechanism":"x"}`},
		{path, `{"commit":"a1b2c3d","branch":"coding/r0A","edits":[{"id":"C1","component":"skill","hypothesis":"h","targets_mode":"t","predicted_affected":[null],"retroactive_check":"r"}],"diff_path":"/private/diff.patch","mechanism":"x"}`},
		{path, `{"commit":"a1b2c3d","branch":"coding/r0A","edits":[{"id":"C1","component":"skill","hypothesis":"h","targets_mode":"t","predicted_affected":[],"retroactive_check":"r"}],"diff_path":"/private/diff.patch","mechanism":"x"}`},
		{path, `{"commit":"a1b2c3d","branch":"coding/r0A","edits":[` + rrsiPrepTestEdit + `],"diff_path":null,"mechanism":"x"}`},
		{path, `{"gate_failure":"unknown","detail":"x","edits":[],"diff_path":null}`},
		{path, `{"gate_failure":"no_proposal","detail":null,"edits":[],"diff_path":null}`},
		{path, `{"gate_failure":"no_proposal","detail":"x","edits":[` + rrsiPrepTestEdit + `],"diff_path":null}`},
		{path, `{"gate_failure":"no_proposal","detail":"x","edits":[],"diff_path":"/private/diff.patch"}`},
		{path, `{"gate_failure":"smoke_fail","detail":"x","edits":[],"diff_path":"/private/diff.patch"}`},
		{path, `{"commit":"a1b2c3d","branch":"coding/r0A","edits":[` + rrsiPrepTestEdit + `],"diff_path":"/private/diff.patch","mechanism":"x","unknown":"field"}`},
		{"runs/coding/r0/I/prep.json", valid},
	}
	for _, test := range invalid {
		if _, _, err := normalizeRRSIPrepFile(test.path, []byte(test.data)); err == nil {
			t.Errorf("RRSI prep normalizer accepted invalid shape for %s: %s", test.path, test.data)
		}
	}
}
