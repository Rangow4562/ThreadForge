package migrate

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRRSIDomainEvaluationsAndRunHistoryAreSanitizedAndTyped(t *testing.T) {
	root := t.TempDir()
	taskResults := map[string]any{
		"coding": map[string]any{
			"job": "base", "k": 2, "S": 0.5, "C": 150.0, "n_expected": 2, "missing": 0,
			"extra": map[string]any{"total_passes": 1, "n_trials": 2, "pass_rate": 0.5},
			"per_task": map[string]any{"extract-elf": map[string]any{
				"rewards": []any{1.0, 0.0}, "weights": []any{1.0, 1.0}, "tokens": []any{100.0, 200.0}, "missing": 0,
				"extra": map[string]any{"passes": 1, "trial_dirs": []any{"C:/Users/private/raw-trial"}},
			}},
		},
		"workspace": map[string]any{
			"job": "heldout_champ", "k": 2, "S": 5.0 / 6.0, "C": 150.0, "n_expected": 2, "missing": 0,
			"extra": map[string]any{"criteria_passed": 5, "criteria_total": 6, "all_pass_tasks": 0, "criterion_rate": 5.0 / 6.0},
			"per_task": map[string]any{"law/task-01": map[string]any{
				"rewards": []any{0.5, 1.0}, "weights": []any{2.0, 4.0}, "tokens": []any{100.0, 200.0}, "missing": 0, "extra": map[string]any{},
			}},
		},
		"eng": map[string]any{
			"job": "base", "k": 2, "S": 0.5, "C": 150.0, "n_expected": 2, "missing": 0,
			"extra": map[string]any{"total_passes": 1, "n_trials": 2, "pass_rate": 0.5,
				"mean_combined_score": 0.6, "valid_rate": 0.95, "no_payload_rate": 0.0},
			"per_task": map[string]any{"eng/task-01": map[string]any{
				"rewards": []any{1.0, 0.0}, "weights": []any{1.0, 1.0}, "tokens": []any{100.0, 200.0}, "missing": 0,
				"extra": map[string]any{"combined_scores": []any{0.7, 0.5}, "valid": []any{1.0, 0.9}, "no_payload": []any{0.0, 0.0}},
			}},
		},
	}
	for domain, result := range taskResults {
		writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join("runs", domain, "jobs", "base", "eval.json")), result)
	}
	writeMancodeJSONFixture(t, root, "runs/coding/calibration.json", map[string]any{
		"delta": 0.02, "z": 2.0, "sd_null": 0.01, "sd_null_bootstrap": 0.012, "se_bootstrap": 0.008,
		"method": "repeated base evaluations", "n_evals": 2, "k": 2, "n_tasks": 1, "S_base": 0.5,
		"C_base": 150.0, "S_per_eval": []any{0.5, 0.6}, "max_abs_diff": 0.1,
		"jobs": []any{"base", "base2"},
	})
	commit := "0123456" + strings.Repeat("a", 33)
	harnessTree := strings.Repeat("b", 40)
	writeMancodeJSONFixture(t, root, "runs/coding/frontier.json", map[string]any{
		"domain": "coding", "S_star": 0.55,
		"incumbent": map[string]any{"t": 1, "commit": commit, "harness_tree": harnessTree, "job": "r0A", "S": 0.55, "C": 130.0,
			"extra": map[string]any{"total_passes": 55, "n_trials": 100, "pass_rate": 0.55}},
		"trajectory": []any{
			map[string]any{"t": 0, "S": 0.5, "C": 150.0, "commit": commit, "job": "base"},
			map[string]any{"t": 1, "S": 0.55, "C": 130.0, "commit": commit, "job": "r0A"},
		},
		"config": map[string]any{"api_key": "frontier-config-secret", "local_path": "C:/Users/private"},
	})
	writeMancodeJSONFixture(t, root, "runs/coding/r0/decisions.json", []any{map[string]any{
		"variant": "A", "admissible": true, "reason": "within calibrated noise floor", "S": 0.55, "C": 130.0,
		"delta_S": 0.05, "delta_C": -0.13, "novelty": 1, "guards": []any{},
	}})
	writeMancodeJSONFixture(t, root, "runs/coding/attribution.jsonl", map[string]any{
		"t": 0, "variant": "A", "edit_id": "E1", "component": "prompt", "hypothesis": "clarify tool return handling",
		"n_predicted": 1, "predicted_hit": []any{"extract-elf"}, "hit_rate": 1.0, "unpredicted_regressions": []any{},
	})
	writeMancodeFixture(t, root, "runs/coding/history.jsonl", []byte(fmt.Sprintf(
		`{"t":1,"variant":"A","edit_id":"E1","component":"prompt","hypothesis":"bounded retry","targets_mode":"coding","predicted_affected":["extract-elf"],"diff":"C:/Users/private/diff.patch","delta_S":0.05,"delta_C":-0.1,"accepted":true,"outcome":"ACCEPTED","S":0.55,"C":130,"bundle":1,"detail":"Bearer do-not-copy-this-secret"}`+"\n")))

	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 8 || manifest.Summary.RecordsImported != 8 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("RRSI run inspection did not reconcile: %#v files=%#v", manifest.Summary, manifest.Files)
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
	evaluated := map[string]bool{}
	gotTypes := map[string]bool{}
	for _, record := range records {
		gotTypes[record.Type] = true
		if record.Scope != "local_pending_review" {
			t.Fatalf("RRSI result became active evidence: %#v", record)
		}
		if record.Type == "rrsi_evaluation_result" {
			var payload struct {
				SchemaVersion string                     `json:"schema_version"`
				Domain        string                     `json:"domain"`
				Score         float64                    `json:"score"`
				Metrics       map[string]float64         `json:"metrics"`
				PerTask       map[string]json.RawMessage `json:"per_task"`
			}
			if err := json.Unmarshal(record.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.SchemaVersion != rrsiEvaluationImportSchema {
				t.Fatalf("RRSI evaluation schema = %q", payload.SchemaVersion)
			}
			var singleTask string
			for id := range payload.PerTask {
				singleTask = id
			}
			if strings.Contains(string(record.Payload), "trial_dirs") || strings.Contains(string(record.Payload), "C:/Users/private") {
				t.Fatalf("RRSI evaluation included a local trial path: %s", record.Payload)
			}
			switch payload.Domain {
			case "coding":
				if payload.Metrics["pass_rate"] != 0.5 || payload.Score != 0.5 {
					t.Fatalf("coding benchmark summary changed during conversion: %#v", payload)
				}
			case "workspace":
				if math.Abs(payload.Metrics["criterion_rate"]-5.0/6.0) > 1e-6 || math.Abs(payload.Score-5.0/6.0) > 1e-6 {
					t.Fatalf("workspace criterion metrics changed during conversion: %#v", payload)
				}
			case "eng":
				if payload.Metrics["valid_rate"] != 0.95 || payload.Metrics["no_payload_rate"] != 0 || payload.Score != 0.5 {
					t.Fatalf("engineering guard metrics changed during conversion: %#v", payload)
				}
			}
			evaluated[payload.Domain] = singleTask != ""
		}
		for _, secret := range []string{"frontier-config-secret", "do-not-copy-this-secret"} {
			if strings.Contains(string(record.Payload), secret) {
				t.Fatalf("RRSI artifact retained omitted secret data: %s", record.Payload)
			}
		}
	}
	for _, domain := range []string{"coding", "workspace", "eng"} {
		if !evaluated[domain] {
			t.Errorf("missing normalized RRSI evaluation for %s", domain)
		}
	}
	for _, kind := range []string{"noise_calibration", "evolution_frontier", "rrsi_edit_history", "rrsi_attribution", "rrsi_selection_decision"} {
		if !gotTypes[kind] {
			t.Errorf("RRSI snapshot is missing typed record %q", kind)
		}
	}
}

func TestRRSIHistoricalTextCredentialMatchesAreExcludedBeforeSnapshot(t *testing.T) {
	root := t.TempDir()
	writeMancodeFixture(t, root, "runs/coding/history.jsonl", []byte(
		`{"t":1,"variant":"A","edit_id":"E1","component":"prompt","hypothesis":"api_key=history-fixture-secret","outcome":"ACCEPTED","bundle":1}`+"\n"))
	writeMancodeFixture(t, root, "runs/coding/attribution.jsonl", []byte(
		`{"t":1,"variant":"A","edit_id":"E1","component":"prompt","hypothesis":"Bearer attribution-fixture-secret","n_predicted":1,"predicted_hit":["task-1"],"unpredicted_regressions":[]}`+"\n"))
	writeMancodeJSONFixture(t, root, "runs/coding/r1/decisions.json", []any{map[string]any{
		"variant": "A", "admissible": false, "reason": "password=selection-fixture-secret", "guards": []any{},
	}})

	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesScanned != 3 || manifest.Summary.RecordsFound != 3 ||
		manifest.Summary.RecordsImported != 0 || manifest.Summary.RecordsCredentialBlocked != 3 ||
		manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("credential-bearing RRSI historical text was not excluded: %#v", manifest.Summary)
	}
	for _, file := range manifest.Files {
		if file.Status != "credential_excluded" || file.ExcludedCount != 1 || file.ImportedCount != 0 {
			t.Fatalf("credential-bearing RRSI record has unexpected file status: %#v", file)
		}
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
	if len(records) != 0 {
		t.Fatalf("credential-bearing RRSI history entered private snapshot records: %#v", records)
	}
}

func TestRRSIEvaluationRejectsInconsistentDomainMetrics(t *testing.T) {
	root := t.TempDir()
	bad := map[string]any{
		"job": "base", "k": 1, "S": 0.5, "C": nil, "n_expected": 1, "missing": 0,
		"extra":    map[string]any{"total_passes": 1, "n_trials": 1, "pass_rate": 0.1},
		"per_task": map[string]any{"task-1": map[string]any{"rewards": []any{0.5}, "weights": []any{1.0}, "tokens": []any{nil}, "missing": 0}},
	}
	writeMancodeJSONFixture(t, root, "runs/coding/jobs/base/eval.json", bad)
	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesUnsupported != 1 || manifest.Summary.RecordsImported != 0 || manifest.Summary.RecordsRejected != 1 {
		t.Fatalf("inconsistent RRSI result was not rejected: %#v", manifest.Summary)
	}
}

func TestRRSITrajectoriesAndDocumentedDeliverablesImportContentFreeSummaries(t *testing.T) {
	root := t.TempDir()
	rawArtifacts := map[string]string{
		"runs/coding/jobs/base/task__a1/agent/trajectory.json": `{"steps":[{"source":"assistant","message":"raw-trial-content-marker","tool_calls":[{"name":"private-tool","arguments":{"token":"private-argument-marker"}}],"observation":{"results":[{"content":"private-observation-marker"}]}},{"source":"tool","message":"private-tool-result"}]}`,
		"runs/workspace/jobs/base/s0/task-1/trajectory.json":   `{"messages":[{"role":"system","content":"private-system-marker"},{"role":"user","content":"raw-trial-content-marker"},{"role":"assistant","content":"private-answer-marker","tool_calls":[{"name":"private-tool","args":{"secret":"private-argument-marker"}}]},{"role":"tool","content":"private-result-marker"}]}`,
		"runs/eng/jobs/base/task-1/t0/traj.json":               `{"messages":[{"role":"system","content":"private-system-marker"},{"role":"user","content":"raw-trial-content-marker"},{"role":"assistant","content":"private-answer-marker","tool_calls":[{"name":"private-tool","args":{"secret":"private-argument-marker"}}]},{"role":"tool","content":"private-result-marker"}]}`,
		"runs/eng/jobs/base/task-1/t0/payload.py":              "PAYLOAD = 'raw-trial-content-marker'\n",
		"runs/workspace/jobs/base/s0/task-1/output/report.md":  "raw-trial-content-marker\n",
		"runs/coding/jobs/base/task__a1/agent/recording.cast":  "{\"version\":2}\n[0.1,\"o\",\"private-terminal-recording-marker\"]\n",
		"runs/eng/jobs/base/task-1/t0/unknown.log":             "unknown-raw-artifact-marker\n",
	}
	for relative, content := range rawArtifacts {
		writeMancodeFixture(t, root, relative, []byte(content))
	}
	binaryOutputPath := "runs/workspace/jobs/base/s0/task-1/output/nested/report.pdf"
	writeMancodeFixture(t, root, binaryOutputPath, []byte{0x00, 0xff, 0x81, 0x10})
	unknownArtifactPath := "runs/eng/jobs/base/task-1/t0/unknown.log"
	writeMancodeJSONFixture(t, root, "runs/coding/jobs/base/eval.json", map[string]any{
		"job": "base", "k": 1, "S": 1.0, "C": nil, "n_expected": 1, "missing": 0,
		"extra": map[string]any{"total_passes": 1, "n_trials": 1, "pass_rate": 1.0},
		"per_task": map[string]any{"task-1": map[string]any{
			"rewards": []any{1.0}, "weights": []any{1.0}, "tokens": []any{nil}, "missing": 0,
			"extra": map[string]any{"passes": 1, "trial_dirs": []any{"C:/Users/private/raw-trial"}},
		}},
	})

	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesScanned != len(rawArtifacts)+2 || manifest.Summary.FilesUnsupported != 1 ||
		manifest.Summary.RecordsImported != 8 || manifest.Summary.RecordsFound != 8 {
		t.Fatalf("RRSI trial inventory did not reconcile: %#v", manifest.Summary)
	}
	deliverablesByKind := map[string]int{}
	for _, file := range manifest.Files {
		if file.Path == unknownArtifactPath {
			if file.Status != "unsupported" || file.SnapshotPath != "" || file.RecordCount != 0 || !validDigest(file.SourceSHA256) {
				t.Fatalf("unknown RRSI artifact was treated as supported: %#v", file)
			}
			continue
		}
		trajectory := strings.HasSuffix(file.Path, "trajectory.json") || strings.HasSuffix(file.Path, "traj.json")
		if trajectory {
			if file.Status != "ready" || file.ImportedCount != 1 || file.RecordCount != 1 {
				t.Fatalf("RRSI trajectory structure was not summarized: %#v", file)
			}
			continue
		}
		if source, deliverable := rrsiDeliverableSourceForPath(file.Path); deliverable {
			if file.Status != "ready" || file.ImportedCount != 1 || file.RecordCount != 1 {
				t.Fatalf("documented RRSI deliverable was not summarized: %#v", file)
			}
			deliverablesByKind[source.recordKind]++
		}
	}
	if deliverablesByKind["coding_terminal_recording"] != 1 || deliverablesByKind["workspace_deliverable"] != 2 ||
		deliverablesByKind["engineering_payload"] != 1 {
		t.Fatalf("documented RRSI deliverables were not all covered: %#v", deliverablesByKind)
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
	trajectorySummaries := 0
	deliverableSummaries := 0
	for _, record := range records {
		encoded := string(record.Payload)
		for _, forbidden := range []string{
			"private-", "raw-trial-content-marker", "private-terminal-recording-marker", "unknown-raw-artifact-marker",
			"recording.cast", "report.md", "report.pdf", "payload.py",
		} {
			if strings.Contains(encoded, forbidden) || strings.Contains(record.SourcePath, forbidden) {
				t.Fatalf("raw RRSI content or identity entered the normalized snapshot (%q): %#v", forbidden, record)
			}
		}
		if record.Type == "rrsi_trajectory_summary" {
			trajectorySummaries++
		} else if record.Type == rrsiDeliverableRecordType {
			deliverableSummaries++
			var payload rrsiDeliverableSummary
			if err := json.Unmarshal(record.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.ContentImported || !payload.HumanReviewRequired || payload.ExecutionAuthorized || payload.CutoverEvidenceEligible ||
				!validDigest(payload.SourceSHA256) || payload.SourceBytes < 0 {
				t.Fatalf("deliverable receipt violated its content-free review-only contract: %#v", payload)
			}
			if strings.Contains(record.SourcePath, "task-1") || strings.Contains(string(record.Payload), "task-1") {
				t.Fatalf("deliverable receipt retained a raw task identity: %#v", record)
			}
		}
	}
	if len(records) != 8 || trajectorySummaries != 3 || deliverableSummaries != 4 {
		t.Fatalf("expected one evaluation, three trajectories, and four content-free deliverable summaries, got %#v", records)
	}
}

func TestRRSIDeliverableReceiptReadbackRejectsTamperingAndUnknownLayouts(t *testing.T) {
	relative := "runs/workspace/jobs/job-1/s2/task-1/output/nested/report.pdf"
	data := []byte{0x00, 0xff, 0x81, 0x10}
	item, alias, err := normalizeRRSIDeliverableFile(relative, data)
	if err != nil {
		t.Fatal(err)
	}
	entry := FileEntry{Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data))}
	record := Record{
		Key: alias + "#000001", Type: item.Type, SourcePath: alias,
		ContentSHA256: digest(item.Payload), Scope: "local_pending_review", Payload: item.Payload,
	}
	if err := validateRRSIDeliverableSummary(record, entry); err != nil {
		t.Fatalf("valid content-free deliverable receipt failed readback validation: %v", err)
	}

	var original map[string]any
	if err := json.Unmarshal(item.Payload, &original); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"source byte count":       func(value map[string]any) { value["source_bytes"] = float64(len(data) + 1) },
		"content import":          func(value map[string]any) { value["content_imported"] = true },
		"path commitment":         func(value map[string]any) { value["artifact_path_sha256"] = strings.Repeat("0", 64) },
		"unexpected raw path":     func(value map[string]any) { value["private_path"] = "runs/private/report.pdf" },
		"cutover evidence status": func(value map[string]any) { value["cutover_evidence_eligible"] = true },
	} {
		payload := make(map[string]any, len(original))
		for key, value := range original {
			payload[key] = value
		}
		mutate(payload)
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		record.Payload = encoded
		if err := validateRRSIDeliverableSummary(record, entry); err == nil {
			t.Errorf("readback accepted mutated %s", name)
		}
	}
	record.Payload = item.Payload
	record.SourcePath = "rrsi/deliverables/workspace/private-report"
	if err := validateRRSIDeliverableSummary(record, entry); err == nil {
		t.Fatal("readback accepted a source alias that does not bind the artifact path")
	}

	for _, unsupported := range []string{
		"runs/workspace/jobs/job-1/s2/task-1/outputs/report.pdf",
		"runs/coding/jobs/job-1/task__hash/agent/session.cast",
		"runs/eng/jobs/job-1/task-1/t2/payload.txt",
	} {
		if isRRSIDeliverablePath(unsupported) {
			t.Errorf("unsupported RRSI layout was accepted: %s", unsupported)
		}
	}
}

func TestRRSIDeliverableReadLimitsAndSymlinkProtection(t *testing.T) {
	root := t.TempDir()
	oversizedPath := "runs/eng/jobs/job-1/task-1/t0/payload.py"
	writeMancodeFixture(t, root, oversizedPath, []byte(strings.Repeat("x", int(maxRRSITrialFileBytes+1))))
	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesUnsupported != 1 || manifest.Summary.RecordsImported != 0 || manifest.Files[0].Status != "unsupported" {
		t.Fatalf("oversized RRSI deliverable bypassed the 32 MiB source-file bound: %#v", manifest.Summary)
	}

	root = t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.bin")
	if err := os.WriteFile(outside, []byte("private-outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := "runs/workspace/jobs/job-1/s0/task-1/output/report.pdf"
	fullSymlinkPath := filepath.Join(root, filepath.FromSlash(symlinkPath))
	if err := os.MkdirAll(filepath.Dir(fullSymlinkPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, fullSymlinkPath); err != nil {
		t.Skipf("file symlink creation unavailable: %v", err)
	}
	if _, _, err := readSafeRRSIFile(root, symlinkPath, maxRRSITrialFileBytes); err == nil {
		t.Fatal("RRSI deliverable safe reader followed a symlink")
	}
}

func TestRRSITrialImportSanitizesThreeDomainsAndJoinsEngineeringPair(t *testing.T) {
	root := t.TempDir()
	writeMancodeJSONFixture(t, root, "runs/coding/jobs/base/task-1__trialhash/result.json", map[string]any{
		"task_name":       "terminal-bench/task-1",
		"verifier_result": map[string]any{"rewards": map[string]any{"reward": 0.5}},
		"agent_result": map[string]any{
			"n_input_tokens": 11, "n_output_tokens": 7,
			"metadata": map[string]any{"n_episodes": 2, "private_note": "raw-trial-content-marker"},
		},
		"exception_info": nil,
		"transcript":     "raw-trial-content-marker",
		"local_path":     "C:/Users/private/trial",
	})
	writeMancodeFixture(t, root, "runs/coding/jobs/base/task-1__trialhash/agent/trajectory.json",
		[]byte(`{"steps":[{"source":"assistant","message":"raw-trial-content-marker","tool_calls":[{"name":"private-tool","arguments":{"token":"private-argument-marker"}}],"observation":{"results":[{"content":"private-observation-marker"}]}},{"source":"tool","message":"private-result-marker"}]}`))
	writeMancodeFixture(t, root, "runs/coding/jobs/base/task-1__trialhash/agent/recording.cast",
		[]byte("{\"version\":2}\n[0.1,\"o\",\"private-terminal-recording-marker\"]\n"))
	writeMancodeJSONFixture(t, root, "runs/workspace/jobs/work/s0/area__task/scores.json", map[string]any{
		"task": "area__task", "all_pass": 0, "score": 0.5, "passed": 1, "total": 2,
		"criteria": []any{map[string]any{"verdict": "pass", "reason": "raw-trial-content-marker"}},
	})
	writeMancodeFixture(t, root, "runs/workspace/jobs/work/s0/area__task/trajectory.json",
		[]byte(`{"messages":[{"role":"system","content":"private-system-marker"},{"role":"user","content":"raw-trial-content-marker"},{"role":"assistant","content":"private-answer-marker","tool_calls":[{"name":"private-tool","args":{"secret":"private-argument-marker"}}]},{"role":"tool","content":"private-result-marker"}]}`))
	writeMancodeFixture(t, root, "runs/workspace/jobs/work/s0/area__task/output/deliverable.md",
		[]byte("raw-trial-content-marker\n"))
	writeMancodeJSONFixture(t, root, "runs/eng/jobs/candidate/task-a/t2/meta.json", map[string]any{
		"id": "task-a", "trial": 2, "status": "done", "domain": "engineering",
		"wall_s": 12.5, "model": "private-model-label", "vertex_project": "private-cloud-project",
		"n_messages": 9, "usage": map[string]any{"private": "raw-trial-content-marker"},
		"total_tokens": 99, "payload_chars": 42, "no_payload": false,
	})
	writeMancodeJSONFixture(t, root, "runs/eng/jobs/candidate/task-a/t2/verdict.json", map[string]any{
		"combined_score": 0.7, "valid": 0.5, "passed": false, "wall_s": 3.25,
		"details": map[string]any{"criteria": "raw-trial-content-marker"}, "error": "private-error-message",
	})
	writeMancodeFixture(t, root, "runs/eng/jobs/candidate/task-a/t2/traj.json",
		[]byte(`{"messages":[{"role":"system","content":"private-system-marker"},{"role":"user","content":"raw-trial-content-marker"},{"role":"assistant","content":"private-answer-marker","tool_calls":[{"name":"private-tool","args":{"secret":"private-argument-marker"}}]},{"role":"tool","content":"private-result-marker"}]}`))
	writeMancodeFixture(t, root, "runs/eng/jobs/candidate/task-a/t2/payload.py",
		[]byte("PAYLOAD = 'raw-trial-content-marker'\n"))

	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 9 || manifest.Summary.FilesJoined != 1 ||
		manifest.Summary.FilesUnsupported != 0 || manifest.Summary.FilesScanned != 10 {
		t.Fatalf("RRSI trial migration counts do not reconcile: %#v", manifest.Summary)
	}
	engMetaPath := "runs/eng/jobs/candidate/task-a/t2/meta.json"
	engVerdictPath := "runs/eng/jobs/candidate/task-a/t2/verdict.json"
	foundJoined := false
	for _, file := range manifest.Files {
		if file.Path == engVerdictPath {
			foundJoined = file.Status == "joined_companion" && file.JoinedInto == engMetaPath && file.RecordCount == 0
		}
	}
	if !foundJoined {
		t.Fatalf("engineering verdict was not linked to its metadata record: %#v", manifest.Files)
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
	if len(records) != 9 {
		t.Fatalf("typed RRSI trial, trajectory, and deliverable record count = %d, want 9", len(records))
	}
	seenDomains := map[string]bool{}
	seenTrajectoryDomains := map[string]bool{}
	seenDeliverables := map[string]bool{}
	for _, record := range records {
		validAlias := record.Type == "rrsi_trial_artifact" && strings.HasPrefix(record.SourcePath, "rrsi/trials/") ||
			record.Type == "rrsi_trajectory_summary" && strings.HasPrefix(record.SourcePath, "rrsi/trajectories/") ||
			record.Type == rrsiDeliverableRecordType && strings.HasPrefix(record.SourcePath, "rrsi/deliverables/")
		if !validAlias || record.Scope != "local_pending_review" {
			t.Fatalf("RRSI trial record has an unsafe identity/scope: %#v", record)
		}
		var payload map[string]any
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if record.Type == "rrsi_trial_artifact" && payload["schema_version"] != RRSITrialImportSchema {
			t.Fatalf("trial schema version = %v", payload["schema_version"])
		}
		if record.Type == "rrsi_trajectory_summary" && payload["schema_version"] != RRSITrajectorySummarySchema {
			t.Fatalf("trajectory schema version = %v", payload["schema_version"])
		}
		domain, _ := payload["domain"].(string)
		if record.Type == rrsiDeliverableRecordType {
			if payload["schema_version"] != RRSIDeliverableSummarySchema || payload["content_imported"] != false ||
				payload["human_review_required"] != true || payload["execution_authorized"] != false ||
				payload["cutover_evidence_eligible"] != false {
				t.Fatalf("RRSI deliverable receipt violated its advisory-only schema: %#v", payload)
			}
			kind, _ := payload["record_kind"].(string)
			seenDeliverables[kind] = true
			continue
		} else if record.Type == "rrsi_trial_artifact" {
			seenDomains[domain] = true
		} else if record.Type == "rrsi_trajectory_summary" {
			seenTrajectoryDomains[domain] = true
		} else {
			t.Fatalf("unexpected RRSI record type %q", record.Type)
		}
		encoded := string(record.Payload)
		for _, forbidden := range []string{"raw-trial-content-marker", "private-system-marker", "private-answer-marker", "private-tool", "private-argument-marker", "private-observation-marker", "private-result-marker", "private-terminal-recording-marker", "C:/Users/private", "private-model-label", "private-cloud-project", "private-error-message", "terminal-bench/task-1", "area__task", "task-a", "payload.py", "deliverable.md", "recording.cast"} {
			if strings.Contains(encoded, forbidden) || strings.Contains(record.SourcePath, forbidden) {
				t.Fatalf("RRSI trial snapshot retained %q: %s %s", forbidden, record.SourcePath, encoded)
			}
		}
		if record.Type == "rrsi_trajectory_summary" {
			switch domain {
			case "coding":
				if payload["step_count"] != float64(2) || payload["tool_call_count"] != float64(1) || payload["observation_result_count"] != float64(1) {
					t.Fatalf("coding trajectory structure counts changed: %#v", payload)
				}
				hashes, ok := rrsiArray(payload["tool_call_name_hashes"])
				wantHash, hashErr := rrsiTrajectoryToolNameHash(map[string]any{"name": "private-tool"})
				if !ok || len(hashes) != 1 || hashErr != nil || hashes[0] != wantHash || payload["tool_sequence_truncated"] != false {
					t.Fatalf("coding trajectory tool sequence was not hashed and retained: %#v", payload)
				}
			case "workspace", "eng":
				if payload["message_count"] != float64(4) || payload["system_message_count"] != float64(1) ||
					payload["user_message_count"] != float64(1) || payload["assistant_message_count"] != float64(1) ||
					payload["tool_message_count"] != float64(1) || payload["other_message_count"] != float64(0) ||
					payload["tool_call_count"] != float64(1) {
					t.Fatalf("message trajectory structure counts changed: %#v", payload)
				}
				hashes, ok := rrsiArray(payload["tool_call_name_hashes"])
				wantHash, hashErr := rrsiTrajectoryToolNameHash(map[string]any{"name": "private-tool"})
				if !ok || len(hashes) != 1 || hashErr != nil || hashes[0] != wantHash || payload["tool_sequence_truncated"] != false {
					t.Fatalf("message trajectory tool sequence was not hashed and retained in order: %#v", payload)
				}
			}
			continue
		}
		switch domain {
		case "coding":
			if payload["reward"] != 0.5 || payload["input_tokens"] != float64(11) || payload["output_tokens"] != float64(7) || payload["episodes"] != float64(2) {
				t.Fatalf("coding trial metrics changed: %#v", payload)
			}
		case "workspace":
			if payload["sample_index"] != float64(0) || payload["score"] != 0.5 || payload["passed_criteria"] != float64(1) || payload["total_criteria"] != float64(2) {
				t.Fatalf("workspace trial metrics changed: %#v", payload)
			}
		case "eng":
			if payload["trial_index"] != float64(2) || payload["combined_score"] != 0.7 || payload["valid_rate"] != 0.5 || payload["total_tokens"] != float64(99) || payload["verdict_source_sha256"] == nil {
				t.Fatalf("engineering joined trial metrics changed: %#v", payload)
			}
		}
	}
	for _, domain := range []string{"coding", "workspace", "eng"} {
		if !seenDomains[domain] {
			t.Errorf("missing sanitized RRSI trial record for %s", domain)
		}
		if !seenTrajectoryDomains[domain] {
			t.Errorf("missing structure-only RRSI trajectory record for %s", domain)
		}
	}
	for _, kind := range []string{"coding_terminal_recording", "workspace_deliverable", "engineering_payload"} {
		if !seenDeliverables[kind] {
			t.Errorf("missing content-free RRSI deliverable summary for %s", kind)
		}
	}
}

func TestRRSITrajectoryToolSequenceIsOrderedHashedAndBounded(t *testing.T) {
	source := rrsiTrialSource{
		domain: "workspace", kind: "workspace_trajectory",
		tail: []string{"jobs", "job", "s0", "task", "trajectory.json"},
	}
	sourcePath := "runs/workspace/jobs/job/s0/task/trajectory.json"
	sourceDigest := digest([]byte("trajectory-source"))
	calls := []any{
		map[string]any{"type": "function", "function": map[string]any{
			"name": "file_read", "arguments": `{"path":"/private/input.pdf"}`,
		}},
		map[string]any{"name": "file_write", "args": map[string]any{"content": "private-deliverable"}},
	}
	value := map[string]any{"messages": []any{map[string]any{"role": "assistant", "tool_calls": calls}}}
	payloadBytes, err := normalizeRRSITrajectory(source, value, sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatal(err)
	}
	hashes, ok := rrsiArray(payload["tool_call_name_hashes"])
	wantRead, _ := rrsiTrajectoryToolNameHash(calls[0])
	wantWrite, _ := rrsiTrajectoryToolNameHash(calls[1])
	if !ok || len(hashes) != 2 || hashes[0] != wantRead || hashes[1] != wantWrite ||
		payload["tool_call_count"] != float64(2) || payload["tool_sequence_truncated"] != false {
		t.Fatalf("tool call sequence order/count was not preserved: %#v", payload)
	}
	for _, forbidden := range []string{"file_read", "file_write", "/private/input.pdf", "private-deliverable"} {
		if strings.Contains(string(payloadBytes), forbidden) {
			t.Fatalf("normalized trajectory retained raw tool or argument data %q: %s", forbidden, payloadBytes)
		}
	}
	jobHash, taskHash, trialHash := rrsiTrialHashes(source, "task")
	entry := FileEntry{Path: sourcePath, SourceSHA256: sourceDigest}
	record := Record{
		Type: "rrsi_trajectory_summary", Scope: "local_pending_review",
		SourcePath: rrsiTrajectorySourceAlias("workspace", jobHash, taskHash, trialHash), Payload: payloadBytes,
	}
	if err := validateRRSITrajectoryPayload(record, entry); err != nil {
		t.Fatalf("valid tool-sequence summary did not pass readback validation: %v", err)
	}
	var mutated map[string]any
	if err := json.Unmarshal(payloadBytes, &mutated); err != nil {
		t.Fatal(err)
	}
	mutated["tool_call_name_hashes"] = []any{"not-a-sha256"}
	mutatedBytes, err := json.Marshal(mutated)
	if err != nil {
		t.Fatal(err)
	}
	record.Payload = mutatedBytes
	if err := validateRRSITrajectoryPayload(record, entry); err == nil {
		t.Fatal("readback accepted a malformed tool-name digest")
	}

	tooManyCalls := make([]any, maxRRSIToolSequenceHashes+1)
	for index := range tooManyCalls {
		tooManyCalls[index] = map[string]any{"name": "custom_tool"}
	}
	truncatedBytes, err := normalizeRRSITrajectory(source, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "tool_calls": tooManyCalls}},
	}, sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	var truncated map[string]any
	if err := json.Unmarshal(truncatedBytes, &truncated); err != nil {
		t.Fatal(err)
	}
	truncatedHashes, ok := rrsiArray(truncated["tool_call_name_hashes"])
	if !ok || len(truncatedHashes) != maxRRSIToolSequenceHashes ||
		truncated["tool_call_count"] != float64(maxRRSIToolSequenceHashes+1) || truncated["tool_sequence_truncated"] != true {
		t.Fatalf("tool sequence truncation was not explicit and bounded: %#v", truncated)
	}
}

func TestRRSICodingTrajectoryUsesProducerFunctionNameAndPreservesOptionalCompatibility(t *testing.T) {
	source := rrsiTrialSource{
		domain: "coding", kind: "coding_trajectory",
		tail: []string{"jobs", "base", "task__trial-1", "agent", "trajectory.json"},
	}
	sourcePath := "runs/coding/jobs/base/task__trial-1/agent/trajectory.json"
	sourceDigest := digest([]byte("coding-trajectory-source"))
	calls := []any{
		map[string]any{"tool_call_id": "call-1", "function_name": "bash_command", "arguments": map[string]any{"keystrokes": "private-command"}},
		map[string]any{"tool_call_id": "call-2", "function_name": "mark_task_complete", "arguments": map[string]any{}},
	}
	payloadBytes, err := normalizeRRSITrajectory(source, map[string]any{
		"steps": []any{map[string]any{
			"source": "agent", "tool_calls": calls,
			"observation": map[string]any{"results": []any{map[string]any{"content": "private-terminal-output"}}},
		}},
	}, sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatal(err)
	}
	hashes, ok := rrsiArray(payload["tool_call_name_hashes"])
	wantCommand, _ := rrsiTrajectoryToolNameHash(map[string]any{"function_name": "bash_command"})
	wantComplete, _ := rrsiTrajectoryToolNameHash(map[string]any{"function_name": "mark_task_complete"})
	if !ok || len(hashes) != 2 || hashes[0] != wantCommand || hashes[1] != wantComplete ||
		payload["tool_call_count"] != float64(2) || payload["tool_sequence_truncated"] != false {
		t.Fatalf("coding tool sequence was not ordered, hashed, and reconciled: %#v", payload)
	}
	for _, forbidden := range []string{"bash_command", "mark_task_complete", "private-command", "private-terminal-output", "call-1"} {
		if strings.Contains(string(payloadBytes), forbidden) {
			t.Fatalf("coding trajectory retained raw tool or trace content %q: %s", forbidden, payloadBytes)
		}
	}
	jobHash, taskHash, trialHash := rrsiTrialHashes(source, "task")
	entry := FileEntry{Path: sourcePath, SourceSHA256: sourceDigest}
	record := Record{
		Type: "rrsi_trajectory_summary", Scope: "local_pending_review",
		SourcePath: rrsiTrajectorySourceAlias("coding", jobHash, taskHash, trialHash), Payload: payloadBytes,
	}
	if err := validateRRSITrajectoryPayload(record, entry); err != nil {
		t.Fatalf("valid coding tool sequence did not pass readback validation: %v", err)
	}

	legacyPayload := make(map[string]any, len(payload))
	for key, value := range payload {
		legacyPayload[key] = value
	}
	delete(legacyPayload, "tool_call_name_hashes")
	delete(legacyPayload, "tool_sequence_truncated")
	legacyBytes, err := json.Marshal(legacyPayload)
	if err != nil {
		t.Fatal(err)
	}
	record.Payload = legacyBytes
	if err := validateRRSITrajectoryPayload(record, entry); err != nil {
		t.Fatalf("older v1 coding trajectory without optional sequence was rejected: %v", err)
	}

	tooManyCalls := make([]any, maxRRSIToolSequenceHashes+1)
	for index := range tooManyCalls {
		tooManyCalls[index] = map[string]any{"function_name": "bash_command"}
	}
	truncatedBytes, err := normalizeRRSITrajectory(source, map[string]any{
		"steps": []any{map[string]any{"source": "agent", "tool_calls": tooManyCalls}},
	}, sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	var truncated map[string]any
	if err := json.Unmarshal(truncatedBytes, &truncated); err != nil {
		t.Fatal(err)
	}
	truncatedHashes, ok := rrsiArray(truncated["tool_call_name_hashes"])
	if !ok || len(truncatedHashes) != maxRRSIToolSequenceHashes ||
		truncated["tool_call_count"] != float64(maxRRSIToolSequenceHashes+1) || truncated["tool_sequence_truncated"] != true {
		t.Fatalf("coding tool sequence truncation was not explicit and bounded: %#v", truncated)
	}
	record.Payload = truncatedBytes
	if err := validateRRSITrajectoryPayload(record, entry); err != nil {
		t.Fatalf("valid truncated coding tool sequence failed readback validation: %v", err)
	}
}

func TestRRSITrajectoryRejectsCredentialLikeOrAmbiguousToolNames(t *testing.T) {
	source := rrsiTrialSource{
		domain: "workspace", kind: "workspace_trajectory",
		tail: []string{"jobs", "job", "s0", "task", "trajectory.json"},
	}
	for name, call := range map[string]any{
		"credential-like":                 map[string]any{"name": "token=private-value"},
		"coding producer credential-like": map[string]any{"function_name": "token=private-value"},
		"ambiguous":                       map[string]any{"name": "read_file", "function": map[string]any{"name": "write_file"}},
		"missing":                         map[string]any{"type": "function", "function": map[string]any{"arguments": "{}"}},
	} {
		_, err := normalizeRRSITrajectory(source, map[string]any{
			"messages": []any{map[string]any{"role": "assistant", "tool_calls": []any{call}}},
		}, digest([]byte("trajectory-source")))
		if err == nil {
			t.Errorf("trajectory accepted %s tool identity", name)
		}
	}
}

func TestRRSIEngineeringTrialWithoutVerdictIsUnsupported(t *testing.T) {
	root := t.TempDir()
	writeMancodeJSONFixture(t, root, "runs/eng/jobs/candidate/task-a/t0/meta.json", map[string]any{
		"id": "task-a", "trial": 0, "status": "done", "wall_s": 1.0,
		"total_tokens": 10, "payload_chars": 20, "no_payload": false,
	})
	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesUnsupported != 1 || manifest.Summary.RecordsImported != 0 ||
		manifest.Files[0].Status != "unsupported" {
		t.Fatalf("incomplete RRSI engineering pair was not rejected: %#v", manifest)
	}
}

func TestRRSITrajectoryRejectsAmbiguousOrMalformedStructure(t *testing.T) {
	root := t.TempDir()
	fixtures := map[string]string{
		"runs/coding/jobs/base/task__t1/agent/trajectory.json": `{"steps":[],"steps":[]}`,
		"runs/workspace/jobs/base/s0/task/trajectory.json":     `{"messages":"private trace contents"}`,
		"runs/eng/jobs/base/task/t0/traj.json":                 `[{"role":"assistant"}]`,
	}
	for path, data := range fixtures {
		writeMancodeFixture(t, root, path, []byte(data))
	}
	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesScanned != len(fixtures) || manifest.Summary.FilesUnsupported != len(fixtures) ||
		manifest.Summary.RecordsImported != 0 {
		t.Fatalf("ambiguous or malformed trajectories were imported: %#v", manifest.Summary)
	}
	for _, file := range manifest.Files {
		if file.Status != "unsupported" || file.SnapshotPath != "" ||
			strings.Contains(file.Issue, "private") || strings.Contains(file.Issue, "trace contents") {
			t.Fatalf("unsafe RRSI trajectory issue or snapshot was recorded: %#v", file)
		}
	}
}

func TestRRSITrialMetadataRejectsOversizedAndCredentialBearingRawFields(t *testing.T) {
	root := t.TempDir()
	large := `{"task_name":"bench/task","padding":"` + strings.Repeat("x", maxRRSITrialFileBytes) + `"}`
	writeMancodeFixture(t, root, "runs/coding/jobs/base/task__large/result.json", []byte(large))
	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.FilesUnsupported != 1 || manifest.Summary.RecordsImported != 0 || manifest.Files[0].Status != "unsupported" {
		t.Fatalf("oversized trial metadata was read/imported: %#v", manifest)
	}

	root = t.TempDir()
	writeMancodeJSONFixture(t, root, "runs/coding/jobs/base/task__failure/result.json", map[string]any{
		"task_name": "bench/task", "exception_info": "do-not-store-error-with-sk-abcdefghijklmnopqrstuvwxyz",
		"agent_result": map[string]any{"n_input_tokens": 99, "n_output_tokens": 101},
	})
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
	if len(records) != 1 || strings.Contains(string(records[0].Payload), "do-not-store-error") ||
		!strings.Contains(string(records[0].Payload), `"exception_reported":true`) ||
		!strings.Contains(string(records[0].Payload), `"input_tokens":null`) {
		t.Fatalf("RRSI coding exception content or token fields were mishandled: %#v", records)
	}
}

func TestRRSIEngineeringPairReadBudgetAndSymlinkProtection(t *testing.T) {
	root := t.TempDir()
	metaPath := "runs/eng/jobs/candidate/task-a/t0/meta.json"
	verdictPath := "runs/eng/jobs/candidate/task-a/t0/verdict.json"
	meta := []byte(`{"id":"task-a","trial":0,"status":"done","wall_s":1,"payload_chars":2,"no_payload":false}`)
	verdict := []byte(`{"combined_score":0.5,"valid":1,"passed":false}`)
	writeMancodeFixture(t, root, metaPath, meta)
	writeMancodeFixture(t, root, verdictPath, verdict)
	if _, _, bytesRead, err := normalizeRRSITrialFile(root, metaPath, meta, 1); err == nil || bytesRead != 0 {
		t.Fatalf("engineering companion exceeded the caller read budget: read=%d err=%v", bytesRead, err)
	}

	outside := filepath.Join(t.TempDir(), "verdict.json")
	if err := os.WriteFile(outside, verdict, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(verdictPath))); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, filepath.FromSlash(verdictPath))); err != nil {
		t.Skipf("file symlink creation unavailable: %v", err)
	}
	manifest, err := Inspect(RRSI, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 0 || manifest.Summary.FilesUnsupported != 1 || manifest.Summary.FilesJoined != 0 {
		t.Fatalf("symlinked engineering companion entered migration: %#v", manifest)
	}
}
