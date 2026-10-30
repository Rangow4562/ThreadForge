package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeAnyJevSyntheticStateDatasetProjectsContentFreeSummary(t *testing.T) {
	path := "bench/results_distill/synth/security_incidents.json"
	fixture := anyJevSyntheticStateFixture("security_incidents")
	item, err := normalizeAnyJevDistillationSynthetic(path, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if item.Type != "decision_synthetic_dataset_summary" {
		t.Fatalf("unexpected record type %q", item.Type)
	}
	var summary anyJevSyntheticStateSummary
	if err := json.Unmarshal(item.Payload, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.SchemaVersion != anyJevSyntheticStateSummarySchema || summary.RequestedStates != 2 ||
		summary.GenerationAttempts != 3 || summary.RejectedGenerations != 1 || summary.StoredStates != 2 ||
		summary.StateSchemaVariants != 1 || !summary.HumanReviewRequired || summary.ExecutionVerified || summary.CutoverEvidenceEligible {
		t.Fatalf("synthetic dataset summary did not preserve bounded metadata and trust limits: %#v", summary)
	}
	for _, marker := range []string{"state-body-marker", "generator-label-marker", "environment-marker", "2026-09-22T19:39:01Z"} {
		if strings.Contains(string(item.Payload), marker) {
			t.Fatalf("synthetic dataset summary retained omitted source material %q", marker)
		}
	}
	if !isAnyJevDistillationSyntheticJSONPath(path) || isAnyJevDistillationSyntheticJSONPath("bench/results_distill/synth/nested/invoice_processing.json") {
		t.Fatal("synthetic state path selector accepted an unsupported location")
	}
}

func TestNormalizeAnyJevSyntheticStateDatasetRejectsUnboundOrMalformedData(t *testing.T) {
	path := "bench/results_distill/synth/security_incidents.json"
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "path workflow mismatch", mutate: func(value map[string]any) { value["workflow"] = "customer_service" }},
		{name: "unsupported workflow field", mutate: func(value map[string]any) { value["unexpected"] = true }},
		{name: "state count mismatch", mutate: func(value map[string]any) { value["states"] = []any{} }},
		{name: "unknown state root field", mutate: func(value map[string]any) {
			value["states"].([]any)[0].(map[string]any)["raw_transcript"] = "must not be persisted"
		}},
		{name: "duplicate states", mutate: func(value map[string]any) {
			states := value["states"].([]any)
			states[1] = states[0]
		}},
		{name: "unknown rejection reason", mutate: func(value map[string]any) {
			value["reject_reasons"] = map[string]any{"unrecognized": float64(1)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := anyJevSyntheticStateFixture("security_incidents")
			test.mutate(fixture)
			if _, err := normalizeAnyJevDistillationSynthetic(path, fixture); err == nil {
				t.Fatal("malformed synthetic-state dataset was accepted")
			}
		})
	}
}

func TestAnyJevSyntheticStateSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the source-backed synthetic-state conversion check")
	}
	manifest, err := Inspect(AnyJev, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]bool)
	for _, file := range manifest.Files {
		if !isAnyJevDistillationSyntheticJSONPath(file.Path) {
			continue
		}
		if file.Status != "ready" || file.ImportedCount != 1 || file.RejectedCount != 0 {
			sourceBytes, readErr := os.ReadFile(filepath.Join(sourceRoot, filepath.FromSlash(file.Path)))
			if readErr != nil {
				t.Fatalf("synthetic-state file failed strict conversion: %s status=%s imported=%d rejected=%d issue=%s",
					file.Path, file.Status, file.ImportedCount, file.RejectedCount, file.Issue)
			}
			values, decodeErr := decodeSourceItems(AnyJev, file.Path, "", sourceBytes)
			if decodeErr != nil || len(values) != 1 {
				t.Fatalf("synthetic-state source decode failed: %s issue=%s", file.Path, file.Issue)
			}
			_, normalizeErr := normalizeAnyJevDistillationSynthetic(file.Path, values[0])
			t.Fatalf("synthetic-state file failed strict conversion: %s issue=%s normalize=%v",
				file.Path, file.Issue, normalizeErr)
		}
		expected[file.Path] = true
	}
	if len(expected) == 0 {
		t.Fatal("source contains no synthetic-state datasets")
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(AnyJev, sourceRoot, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(AnyJev), manifest.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]int)
	for _, record := range records {
		if !expected[record.SourcePath] {
			continue
		}
		if record.Type != "decision_synthetic_dataset_summary" || record.Scope != "local_pending_review" {
			t.Fatalf("synthetic-state record was not staged as review-only summary: %#v", record)
		}
		var summary anyJevSyntheticStateSummary
		if err := json.Unmarshal(record.Payload, &summary); err != nil || summary.StoredStates < 1 || summary.CutoverEvidenceEligible {
			t.Fatalf("synthetic-state summary did not pass readback: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("synthetic-state source/readback count mismatch: expected=%d actual=%d", len(expected), len(seen))
	}
}

func anyJevSyntheticStateFixture(workflow string) map[string]any {
	state := func(marker string) map[string]any {
		return map[string]any{
			"alert":     map[string]any{"description": marker, "evidence": "state-body-marker", "rule": "example"},
			"context":   map[string]any{"asset_criticality": "low", "change_window_active": false, "source_on_allowlist": true},
			"history":   map[string]any{"credential_rotation_days_ago": float64(20), "distinct_countries_30d": float64(1), "logins_30d": float64(4), "prior_alerts_90d": float64(0)},
			"principal": map[string]any{"mfa_enrolled": true, "name": "fixture-user", "privileges": []any{"read"}, "type": "user"},
		}
	}
	return map[string]any{
		"workflow": workflow, "generator": "generator-label-marker", "n": float64(2), "generated": float64(3),
		"rejected": float64(1), "reject_reasons": map[string]any{"duplicate": float64(1)},
		"settings": map[string]any{"temperature": 1.0, "top_p": 0.95, "seed": float64(0),
			"max_new_tokens": float64(700), "exemplars_per_prompt": float64(3)},
		"date": "2026-09-22T19:39:01Z", "env": map[string]any{"gpu": "environment-marker"},
		"states": []any{state("first"), state("second")},
	}
}
