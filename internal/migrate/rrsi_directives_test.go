package migrate

import (
	"encoding/json"
	"strings"
	"testing"
)

const rrsiDirectivesFixture = `{"t":0,"b_t":2,"sigma_t":1,"tried":["prompt","skill"],"explore":{"sigma":1,"untried":["control_flow","config","output_plumbing","context_mgmt","client_tool","memory","subagent"],"m_draft":1,"text":"private proposer instruction"},"prune_set":[{"component":"prompt","recent_best_gain":null,"accepted_edits_in_incumbent":[{"t":0,"edit_id":"private-edit-id","hypothesis":"private-hypothesis","delta_S":null}]}],"delta":0.02,"S_star":0.5}`

func TestRRSIDirectivesPathsAndExactSourceLayout(t *testing.T) {
	for _, path := range []string{
		"runs/coding/r0/directives.json",
		"runs/workspace/r12/directives.json",
		"runs/eng/r3/directives.json",
	} {
		if !isRRSIDirectivesPath(path) || !isRRSIMetadataPath(path) || !isRRSISafelyReadPath(path) {
			t.Errorf("documented RRSI directives path is not supported/safe-read: %s", path)
		}
	}
	for _, path := range []string{
		"runs/coding/jobs/base/directives.json",
		"runs/coding/r00/directives.json",
		"runs/unknown/r0/directives.json",
		"runs/coding/r0/A/directives.json",
		"runs/coding/r1000001/directives.json",
		"runs/coding/r0/directives.json.bak",
	} {
		if isRRSIDirectivesPath(path) {
			t.Errorf("unsupported RRSI directives path accepted: %s", path)
		}
	}
}

func TestRRSIDirectivesNormalizationDropsFreeFormTextAndIdentities(t *testing.T) {
	path := "runs/coding/r0/directives.json"
	item, alias, err := normalizeRRSIDirectivesFile(path, []byte(rrsiDirectivesFixture))
	if err != nil {
		t.Fatal(err)
	}
	if item.Type != rrsiDirectivesRecordType || strings.Contains(alias, "runs/") || strings.Contains(alias, "directives.json") {
		t.Fatalf("RRSI directives alias/type retained source path: %s %s", item.Type, alias)
	}
	for _, private := range []string{"private proposer instruction", "private-edit-id", "private-hypothesis", "accepted_edits_in_incumbent"} {
		if strings.Contains(string(item.Payload), private) || strings.Contains(alias, private) {
			t.Fatalf("RRSI directives summary retained private source value %q: %s", private, item.Payload)
		}
	}
	var summary rrsiDirectivesSummary
	if err := json.Unmarshal(item.Payload, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.SchemaVersion != rrsiDirectivesSummarySchema || summary.Domain != "coding" ||
		summary.RoundIndex != 0 || summary.EditBudget != 2 || summary.StallFlag != 1 ||
		summary.ExplorationSlots != 1 || summary.TriedComponents["prompt"] != 1 ||
		summary.UntriedComponents["memory"] != 1 || summary.PrunedComponents["prompt"] != 1 ||
		summary.AcceptedEditsInPruneSet != 1 || summary.MeasuredPruneComponents != 0 ||
		!summary.HumanReviewRequired || summary.ExecutionAuthorized || summary.CutoverEvidenceEligible {
		t.Fatalf("RRSI directives summary lost bounded state or crossed review boundary: %#v", summary)
	}
	entry := FileEntry{Path: path, SourceSHA256: digest([]byte(rrsiDirectivesFixture)), SourceBytes: int64(len(rrsiDirectivesFixture))}
	record := Record{Type: item.Type, SourcePath: alias, Scope: "local_pending_review", Payload: item.Payload}
	if err := validateRRSIDirectivesSummary(record, entry); err != nil {
		t.Fatalf("valid RRSI directives summary failed readback: %v; payload=%s", err, item.Payload)
	}
}

func TestRRSIDirectivesRejectMalformedOrInconsistentProducerState(t *testing.T) {
	invalid := []string{
		strings.Replace(rrsiDirectivesFixture, `"t":0`, `"t":1`, 1),
		strings.Replace(rrsiDirectivesFixture, `"sigma":1`, `"sigma":0`, 1),
		strings.Replace(rrsiDirectivesFixture, `"private proposer instruction"`, `"`+strings.Repeat("x", maxRRSIDirectivesTextBytes+1)+`"`, 1),
		strings.Replace(rrsiDirectivesFixture, `"recent_best_gain":null`, `"recent_best_gain":0.1`, 1),
		strings.Replace(rrsiDirectivesFixture, `"component":"prompt"`, `"component":"unknown"`, 1),
		strings.Replace(rrsiDirectivesFixture, `"untried":["control_flow"`, `"untried":["prompt","control_flow"`, 1),
		strings.Replace(rrsiDirectivesFixture, `"delta_S":null`, `"delta_S":"private"`, 1),
		strings.TrimSuffix(rrsiDirectivesFixture, "}") + `,"unexpected":true}`,
		`{"t":0,"t":0,"b_t":2,"sigma_t":1,"tried":[],"explore":{},"prune_set":[],"delta":0.02,"S_star":0.5}`,
	}
	for i, value := range invalid {
		if _, _, err := normalizeRRSIDirectivesFile("runs/coding/r0/directives.json", []byte(value)); err == nil {
			t.Errorf("malformed RRSI directives fixture %d was accepted", i)
		}
	}

	item, alias, err := normalizeRRSIDirectivesFile("runs/coding/r0/directives.json", []byte(rrsiDirectivesFixture))
	if err != nil {
		t.Fatal(err)
	}
	entry := FileEntry{Path: "runs/coding/r0/directives.json", SourceSHA256: digest([]byte(rrsiDirectivesFixture)), SourceBytes: int64(len(rrsiDirectivesFixture))}
	var original map[string]any
	if err := json.Unmarshal(item.Payload, &original); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"source digest":       func(v map[string]any) { v["source_sha256"] = strings.Repeat("0", 64) },
		"execution authority": func(v map[string]any) { v["execution_authorized"] = true },
		"wrong partition":     func(v map[string]any) { v["tried_components"].(map[string]any)["skill"] = float64(0) },
		"unknown field":       func(v map[string]any) { v["hypothesis"] = "private" },
	} {
		copy := make(map[string]any, len(original))
		for key, value := range original {
			copy[key] = value
		}
		if name == "wrong partition" {
			counts := make(map[string]any, len(original["tried_components"].(map[string]any)))
			for key, value := range original["tried_components"].(map[string]any) {
				counts[key] = value
			}
			copy["tried_components"] = counts
		}
		mutate(copy)
		payload, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		record := Record{Type: item.Type, SourcePath: alias, Payload: payload}
		if err := validateRRSIDirectivesSummary(record, entry); err == nil {
			t.Errorf("tampered directives summary accepted %s", name)
		}
	}
}
