package migrate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRRSICriticSourcePathsAreStrictlyBounded(t *testing.T) {
	for _, supported := range []string{
		"runs/coding/r0/A/critic.json",
		"runs/workspace/r12/b/critic_a0.json",
		"runs/eng/r2/H/critic_a2.json",
		"runs/eng/r3/H/critic_a128.json",
	} {
		if !isRRSICriticPath(supported) {
			t.Errorf("documented RRSI critic artifact path was rejected: %s", supported)
		}
	}
	for _, unsupported := range []string{
		"runs/coding/jobs/base/task__trial/critic.json",
		"runs/coding/r0/I/critic.json",
		"runs/coding/r0/A/critic_a01.json",
		"runs/coding/r0/A/critic_a129.json",
		"runs/coding/r0/A/critic_aone.json",
		"runs/coding/r0/A/critic.json.bak",
		"runs/coding/r0/A/analysis_report.json",
		"runs/coding/r0/A/subdir/critic.json",
		"runs/unknown/r0/A/critic.json",
	} {
		if isRRSICriticPath(unsupported) {
			t.Errorf("unsupported RRSI critic artifact path was accepted: %s", unsupported)
		}
	}
}

func TestRRSICriticSummaryOmitsProseAndBindsReviewOnlyReceipt(t *testing.T) {
	path := "runs/workspace/r12/B/critic_a2.json"
	data := []byte(`{"verdict":"reject","reasons":["private-critic-prose"],"risk_notes":["private-risk-details"]}`)
	item, alias, err := normalizeRRSICriticFile(path, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-critic-prose", "private-risk-details", "critic_a2.json", "runs/workspace"} {
		if strings.Contains(string(item.Payload), private) || strings.Contains(alias, private) {
			t.Fatalf("RRSI critic summary retained private source data %q: %s %s", private, item.Payload, alias)
		}
	}
	entry := FileEntry{Path: path, SourceSHA256: digest(data), SourceBytes: int64(len(data))}
	record := Record{
		Key: alias + "#000001", Type: item.Type, SourcePath: alias,
		ContentSHA256: digest(item.Payload), Scope: "local_pending_review", Payload: item.Payload,
	}
	if err := validateRRSICriticSummary(record, entry); err != nil {
		t.Fatalf("valid RRSI critic summary failed source readback: %v", err)
	}
	var summary rrsiCriticSummary
	if err := json.Unmarshal(item.Payload, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.SchemaVersion != rrsiCriticSummarySchema || summary.Verdict != "reject" ||
		summary.ReasonCount != 1 || summary.RiskNoteCount != 1 || summary.ArtifactKind != "attempt" ||
		summary.AttemptIndex != 2 || !summary.HumanReviewRequired || summary.SelectionAccepted ||
		summary.ExecutionAuthorized || summary.CutoverEvidenceEligible {
		t.Fatalf("RRSI critic summary violated typed review-only contract: %#v", summary)
	}
}

func TestRRSICriticReadbackRejectsTamperingAndStrictParserRejectsAmbiguity(t *testing.T) {
	path := "runs/coding/r0/A/critic.json"
	data := []byte(`{"verdict":"accept","reasons":[],"risk_notes":[]}`)
	item, alias, err := normalizeRRSICriticFile(path, data)
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
		"source digest":          func(value map[string]any) { value["source_sha256"] = strings.Repeat("0", 64) },
		"round identity":         func(value map[string]any) { value["round_sha256"] = strings.Repeat("0", 64) },
		"selection authority":    func(value map[string]any) { value["selection_accepted"] = true },
		"execution authority":    func(value map[string]any) { value["execution_authorized"] = true },
		"cutover eligibility":    func(value map[string]any) { value["cutover_evidence_eligible"] = true },
		"unexpected prose field": func(value map[string]any) { value["reason"] = "must-not-be-retained" },
		"false flag type":        func(value map[string]any) { value["selection_accepted"] = nil },
		"count type":             func(value map[string]any) { value["reason_count"] = nil },
	} {
		payload := make(map[string]any, len(original))
		for key, value := range original {
			payload[key] = value
		}
		mutate(payload)
		record.Payload, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRRSICriticSummary(record, entry); err == nil {
			t.Errorf("readback accepted tampered %s", name)
		}
	}

	for _, invalid := range []string{
		`{"verdict":"accept","reasons":[],"reasons":[],"risk_notes":[]}`,
		`{"verdict":"accept","reasons":[],"risk_notes":[],"extra":"unknown"}`,
		`{"verdict":"maybe","reasons":[],"risk_notes":[]}`,
		`{"verdict":"reject","reasons":null,"risk_notes":[]}`,
		`{"verdict":"reject","reasons":[null],"risk_notes":[]}`,
		`{"verdict":"reject","reasons":["` + strings.Repeat("x", maxRRSICriticTextBytes+1) + `"],"risk_notes":[]}`,
	} {
		if _, _, err := normalizeRRSICriticFile(path, []byte(invalid)); err == nil {
			t.Errorf("critic normalizer accepted malformed/ambiguous producer JSON: %s", invalid[:min(len(invalid), 80)])
		}
	}
}
