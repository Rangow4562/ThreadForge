package migrate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func legacyMancodeWorkflowFixture(taskID string) map[string]any {
	return map[string]any{
		"taskId":                    taskID,
		"task":                      "private workflow task marker",
		"mode":                      "man",
		"currentStep":               json.Number("4"),
		"skippedSteps":              []any{"private skipped step marker"},
		"startedAt":                 "2026-10-20T10:00:00Z",
		"updatedAt":                 "2026-10-20T10:05:00Z",
		"status":                    "blocked",
		"blockingReason":            "private blocker and synthetic api_key=never-copy",
		"planVersion":               json.Number("2"),
		"reviewPolicyVersion":       json.Number("2"),
		"planningPolicyVersion":     json.Number("1"),
		"verificationPolicyVersion": json.Number("1"),
		"requirementsStatus":        "ready",
		"requirementsDigest":        strings.Repeat("a", 64),
		"planDecision":              "plan_only",
		"verificationStatus":        "pending",
	}
}

func TestMancodeLegacyWorkflowMetadataCreatesPrivacyReducedReviewSummary(t *testing.T) {
	root := t.TempDir()
	taskID := "20261020-100000-private-task"
	relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json"))
	writeMancodeJSONFixture(t, root, relative, legacyMancodeWorkflowFixture(taskID))

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 ||
		len(manifest.Files) != 1 || manifest.Files[0].ImportedCount != 1 || manifest.Files[0].Status != "ready" {
		t.Fatalf("legacy workflow summary did not reconcile: summary=%#v files=%#v", manifest.Summary, manifest.Files)
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
	if len(records) != 1 || records[0].Type != "mancode_legacy_workflow_summary" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected legacy workflow snapshot records: %#v", records)
	}
	wantSourceAlias := mancodeLegacyWorkflowSourceAlias(relative)
	if records[0].SourcePath != wantSourceAlias || strings.Contains(records[0].SourcePath, taskID) || strings.Contains(records[0].Key, taskID) {
		t.Fatalf("legacy workflow record identity was not path-aliased: %#v", records[0])
	}
	payload := string(records[0].Payload)
	for _, private := range []string{taskID, "private workflow task marker", "private skipped step marker", "private blocker", "never-copy"} {
		if strings.Contains(payload, private) {
			t.Fatalf("legacy workflow summary retained source value %q: %s", private, payload)
		}
	}
	var summary map[string]any
	if err := json.Unmarshal(records[0].Payload, &summary); err != nil {
		t.Fatal(err)
	}
	if summary["schema_version"] != mancodeLegacyWorkflowSummarySchema || summary["authority"] != "local_pending_review" ||
		summary["human_review_required"] != true || summary["execution_authorized"] != false ||
		summary["workflow_mode"] != "man" || summary["workflow_status"] != "blocked" ||
		summary["current_step"] != float64(4) || summary["skipped_step_count"] != float64(1) ||
		summary["task_present"] != true || summary["blocking_reason_present"] != true ||
		summary["requirements_digest_present"] != true || summary["raw_task_text_omitted"] != true {
		t.Fatalf("legacy workflow summary lost safe lifecycle metadata: %s", payload)
	}
}

func TestMancodeLegacyWorkflowMetadataSupportsMambaAndDirectorySourceRoot(t *testing.T) {
	root := t.TempDir()
	taskID := "20261020-110000-debug-child"
	parentID := "20261020-100000-parent"
	value := map[string]any{
		"taskId": taskID, "task": "private diagnostic task", "mode": "mamba", "currentStep": json.Number("5"),
		"skippedSteps": []any{}, "startedAt": "2026-10-20T11:00:00Z", "updatedAt": "2026-10-20T11:01:00Z",
		"status": "completed", "parentTaskId": parentID, "outcome": "verified",
	}
	relative := filepath.ToSlash(filepath.Join("workflows", taskID, "metadata.json"))
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", relative)), value)

	manifest, err := Inspect(Mancode, filepath.Join(root, ".mancode"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || len(manifest.Files) != 1 {
		t.Fatalf("Mancode directory source did not import legacy workflow: %#v", manifest)
	}
	items, handled, err := normalizeMancodeItems(".mancode", relative, value)
	if err != nil || !handled || len(items) != 1 || items[0].Type != "mancode_legacy_workflow_summary" {
		t.Fatalf("Mancode directory path was not converted: handled=%v items=%#v err=%v", handled, items, err)
	}
	var summary mancodeLegacyWorkflowSummary
	if err := json.Unmarshal(items[0].Payload, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.WorkflowMode != "mamba" || summary.Outcome != "verified" || !summary.ParentTaskPresent || summary.ExecutionAuthorized {
		t.Fatalf("Mamba workflow summary has invalid state: %#v", summary)
	}
}

func TestMancodeLegacyWorkflowSkippedStepSummaryHasBoundedCount(t *testing.T) {
	taskID := "20261020-115000-bounded-skips"
	path := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json"))
	value := legacyMancodeWorkflowFixture(taskID)
	value["skippedSteps"] = make([]any, 64)
	for index := range value["skippedSteps"].([]any) {
		value["skippedSteps"].([]any)[index] = "historical step"
	}
	items, handled, err := normalizeMancodeItems("", path, value)
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("bounded legacy workflow was not summarized: handled=%v items=%#v err=%v", handled, items, err)
	}
	var summary mancodeLegacyWorkflowSummary
	if err := json.Unmarshal(items[0].Payload, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.SkippedStepCount != 64 {
		t.Fatalf("bounded skipped-step count was not retained: %#v", summary)
	}
	value["skippedSteps"] = append(value["skippedSteps"].([]any), "overflow")
	if _, handled, err := normalizeMancodeItems("", path, value); !handled || err == nil {
		t.Fatalf("oversized legacy skipped-step list was not rejected: handled=%v err=%v", handled, err)
	}
}

func TestMancodeLegacyWorkflowMetadataRejectsSchemaDriftAndUnsafeState(t *testing.T) {
	taskID := "20261020-120000-legacy-workflow"
	path := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json"))
	base := legacyMancodeWorkflowFixture(taskID)
	invalid := []map[string]any{}
	with := func(change func(map[string]any)) {
		value := make(map[string]any, len(base)+1)
		for key, item := range base {
			value[key] = item
		}
		change(value)
		invalid = append(invalid, value)
	}
	with(func(value map[string]any) { value["taskId"] = "different-id" })
	with(func(value map[string]any) { value["unmappedPrivateField"] = "must reject" })
	with(func(value map[string]any) { value["currentStep"] = json.Number("10") })
	with(func(value map[string]any) {
		value["mode"], value["status"] = "mamba", "planned"
		for _, key := range []string{"blockingReason", "planVersion", "reviewPolicyVersion", "planningPolicyVersion", "verificationPolicyVersion", "requirementsStatus", "requirementsDigest", "planDecision", "verificationStatus"} {
			delete(value, key)
		}
	})
	with(func(value map[string]any) { value["status"] = "blocked"; delete(value, "blockingReason") })
	with(func(value map[string]any) { value["updatedAt"] = "2026-10-19T10:00:00Z" })
	with(func(value map[string]any) {
		value["mode"], value["status"], value["outcome"] = "mamba", "completed", "verified"
		value["planVersion"] = json.Number("1")
	})
	with(func(value map[string]any) {
		value["mode"], value["status"] = "mamba", "completed"
		delete(value, "outcome")
		for _, key := range []string{"planVersion", "reviewPolicyVersion", "planningPolicyVersion", "verificationPolicyVersion", "requirementsStatus", "requirementsDigest", "planDecision", "verificationStatus", "blockingReason"} {
			delete(value, key)
		}
	})
	with(func(value map[string]any) {
		value["planningPolicyVersion"] = json.Number("2")
		value["skippedSteps"] = []any{"implementation"}
	})

	for index, value := range invalid {
		if _, handled, err := normalizeMancodeItems("", path, value); !handled || err == nil {
			t.Errorf("invalid legacy workflow fixture %d was not rejected: handled=%v err=%v", index, handled, err)
		}
	}
	for _, unsafePath := range []string{
		".mancode/workflows/not_valid!/metadata.json",
		".mancode/local/workflows/20261020-120000-legacy-workflow/metadata.json",
		".mancode/workflows/20261020-120000-legacy-workflow/requirements.json",
	} {
		if _, handled, err := normalizeMancodeItems("", unsafePath, base); handled || err != nil {
			t.Errorf("non-legacy metadata path unexpectedly entered legacy converter: path=%q handled=%v err=%v", unsafePath, handled, err)
		}
	}
}
