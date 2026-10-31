package migrate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRRSISmokePathsAndDomainContractsAreStrict(t *testing.T) {
	for _, supported := range []string{
		"runs/coding/r0/A/smoke.json",
		"runs/workspace/r12/b/smoke.json",
		"runs/eng/r3/H/smoke.json",
	} {
		if !isRRSISmokePath(supported) {
			t.Errorf("documented RRSI smoke path was rejected: %s", supported)
		}
		if !isRRSISafelyReadPath(supported) {
			t.Errorf("documented RRSI smoke path is not on the safe-read allowlist: %s", supported)
		}
	}
	for _, unsupported := range []string{
		"runs/coding/jobs/base/task__trial/smoke.json",
		"runs/coding/r0/I/smoke.json",
		"runs/coding/r0/A/smoke-report.json",
		"runs/unknown/r0/A/smoke.json",
	} {
		if isRRSISmokePath(unsupported) {
			t.Errorf("unsupported RRSI smoke path was accepted: %s", unsupported)
		}
	}

	fixtures := []struct {
		path string
		data string
	}{
		{
			"runs/coding/r0/A/smoke.json",
			`{"ok":false,"stage":"smoke_run","private-task-id":"private-error-text","missing_trials":"0/1"}`,
		},
		{
			"runs/workspace/r1/B/smoke.json",
			`{"ok":true,"stage":"smoke_run","n":2}`,
		},
		{
			"runs/eng/r2/C/smoke.json",
			`{"ok":false,"stage":"run","smoke_ids":["private-eng-task"],"missing":1,"total_passes":2,"n_trials":4,"pass_rate":0.5,"mean_combined_score":0.25,"valid_rate":0.75,"no_payload_rate":0.25}`,
		},
	}
	for _, fixture := range fixtures {
		item, alias, err := normalizeRRSISmokeFile(fixture.path, []byte(fixture.data))
		if err != nil {
			t.Fatalf("normalize %s: %v", fixture.path, err)
		}
		for _, forbidden := range []string{"private-task-id", "private-error-text", "missing_trials", "private-eng-task", "runs/", "smoke.json"} {
			if strings.Contains(string(item.Payload), forbidden) || strings.Contains(alias, forbidden) {
				t.Fatalf("RRSI smoke receipt retained private item %q: %s %s", forbidden, item.Payload, alias)
			}
		}
		entry := FileEntry{Path: fixture.path, SourceSHA256: digest([]byte(fixture.data)), SourceBytes: int64(len(fixture.data))}
		record := Record{Type: item.Type, SourcePath: alias, Payload: item.Payload}
		if err := validateRRSISmokeSummary(record, entry); err != nil {
			t.Fatalf("valid RRSI smoke receipt failed readback: %v", err)
		}
		var summary rrsiSmokeSummary
		if err := json.Unmarshal(item.Payload, &summary); err != nil {
			t.Fatal(err)
		}
		if summary.SchemaVersion != rrsiSmokeSummarySchema || summary.Domain != rrsiSmokeSourceDomain(fixture.path) ||
			!summary.HumanReviewRequired || summary.SelectionAccepted || summary.ExecutionAuthorized ||
			summary.CutoverEvidenceEligible {
			t.Fatalf("RRSI smoke receipt crossed its review-only boundary: %#v", summary)
		}
	}
}

func TestRRSISmokeRejectsMalformedAndTamperedRecords(t *testing.T) {
	path := "runs/workspace/r0/A/smoke.json"
	data := []byte(`{"ok":true,"stage":"smoke_run","n":1}`)
	item, alias, err := normalizeRRSISmokeFile(path, data)
	if err != nil {
		t.Fatal(err)
	}
	entry := FileEntry{Path: path, SourceSHA256: digest(data), SourceBytes: int64(len(data))}
	record := Record{Type: item.Type, SourcePath: alias, Payload: item.Payload}
	var original map[string]any
	if err := json.Unmarshal(item.Payload, &original); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"source binding":      func(value map[string]any) { value["source_sha256"] = strings.Repeat("0", 64) },
		"reported outcome":    func(value map[string]any) { value["producer_reported_ok"] = false },
		"execution authority": func(value map[string]any) { value["execution_authorized"] = true },
		"cutover eligibility": func(value map[string]any) { value["cutover_evidence_eligible"] = true },
		"boolean field type":  func(value map[string]any) { value["producer_reported_ok"] = nil },
		"count field type":    func(value map[string]any) { value["reported_count"] = nil },
		"unexpected content":  func(value map[string]any) { value["task_id"] = "private-task" },
	} {
		copy := make(map[string]any, len(original))
		for key, value := range original {
			copy[key] = value
		}
		mutate(copy)
		record.Payload, err = json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRRSISmokeSummary(record, entry); err == nil {
			t.Errorf("readback accepted tampered %s", name)
		}
	}

	for _, invalid := range []string{
		`{"ok":true,"ok":false,"stage":"smoke_run","n":1}`,
		`{"ok":null,"stage":"smoke_run","n":1}`,
		`{"ok":true,"stage":"smoke_run","unknown":{"text":"private"}}`,
		`{"ok":true,"stage":"compile","detail":"unexpected-success"}`,
		`{"ok":true,"stage":"smoke_run","n":null}`,
		`{"ok":true,"stage":"smoke_run"}`,
		`{"ok":false,"stage":"smoke_run","detail":null}`,
		`{"ok":false,"stage":"smoke_run"}`,
		`{"ok":true,"stage":"smoke_run","n":10001}`,
		`{"ok":false,"stage":"smoke_run","detail":"` + strings.Repeat("x", maxRRSISmokeTextBytes+1) + `"}`,
	} {
		if _, _, err := normalizeRRSISmokeFile(path, []byte(invalid)); err == nil {
			t.Errorf("smoke normalizer accepted invalid producer shape: %s", invalid[:min(len(invalid), 80)])
		}
	}
	engPath := "runs/eng/r0/A/smoke.json"
	for _, invalid := range []string{
		`{"ok":true,"stage":"run","smoke_ids":null,"missing":0}`,
		`{"ok":true,"stage":"run","smoke_ids":[null],"missing":0}`,
		`{"ok":true,"stage":"run","smoke_ids":[],"missing":null}`,
		`{"ok":true,"stage":"run","smoke_ids":[],"missing":0,"pass_rate":null}`,
		`{"ok":true,"stage":"run","smoke_ids":[],"missing":0,"total_passes":2,"n_trials":4,"pass_rate":2,"mean_combined_score":0.25,"valid_rate":0.75,"no_payload_rate":0.25}`,
		`{"ok":true,"stage":"run","smoke_ids":[],"missing":0,"total_passes":2,"n_trials":4,"pass_rate":0.5,"mean_combined_score":0.25,"valid_rate":0.75}`,
		`{"ok":false,"stage":"compile","err":null}`,
	} {
		if _, _, err := normalizeRRSISmokeFile(engPath, []byte(invalid)); err == nil {
			t.Errorf("engineering smoke normalizer accepted a null/mistyped value: %s", invalid)
		}
	}
}

func rrsiSmokeSourceDomain(relative string) string {
	domain, _ := rrsiDomain(relative)
	return domain
}
