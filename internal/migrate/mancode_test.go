package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMancodeFixture(t *testing.T, root, relative string, content []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mancodeV3Fixture(t *testing.T, root, namespace, taskID string, conflictReview bool) string {
	t.Helper()
	workflowMode, coordination := "man", "single"
	if namespace == "shared" {
		workflowMode, coordination = "manteam", "team"
	}
	taskRef := map[string]any{"namespace": namespace, "taskId": taskID}
	otherTaskID := strings.Repeat("0", 25) + "2"
	requirements := map[string]any{
		"schemaVersion": json.Number("1"), "canonicalizationVersion": "mancode-jcs-v1", "taskRef": taskRef,
		"revision": json.Number("1"), "status": "draft", "goal": "Keep the migration review-only",
		"functionalScope":    map[string]any{"inScope": []any{"semantic import"}, "outOfScope": []any{}},
		"technicalDecisions": []any{}, "defaults": []any{}, "coverage": []any{}, "requirements": []any{},
		"acceptanceCriteria": []any{}, "blockingUnknowns": []any{}, "legacySource": nil,
		"lastOperationId": nil, "updatedAt": "2026-09-20T10:00:00Z",
	}
	requirementsDigest, err := mancodeDigestFields(requirements, mancodeWorkflowRequirementsDigestFields)
	if err != nil {
		t.Fatal(err)
	}
	requirements["contentDigest"] = requirementsDigest

	reviewTaskRef := taskRef
	if conflictReview {
		reviewTaskRef = map[string]any{"namespace": namespace, "taskId": otherTaskID}
	}
	review := map[string]any{
		"schemaVersion": json.Number("1"), "canonicalizationVersion": "mancode-jcs-v1", "taskRef": reviewTaskRef,
		"revision": json.Number("1"), "status": "pending", "depth": "targeted",
		"requirementsDigest": requirementsDigest, "planVersion": json.Number("1"),
		"requiredDomains": []any{"quality"},
		"domains":         []any{map[string]any{"domain": "quality", "status": "pending", "reportRef": nil}},
		"blockers":        []any{}, "remediationRound": json.Number("0"),
		"skip": nil, "legacySource": nil, "lastOperationId": nil, "updatedAt": "2026-09-20T10:00:00Z",
	}
	reviewDigest, err := mancodeDigestFields(review, mancodeWorkflowReviewDigestFields)
	if err != nil {
		t.Fatal(err)
	}
	review["contentDigest"] = reviewDigest

	verification := map[string]any{
		"schemaVersion": json.Number("1"), "canonicalizationVersion": "mancode-jcs-v1", "taskRef": taskRef,
		"revision": json.Number("1"), "status": "pending", "requirementsDigest": requirementsDigest,
		"planVersion": json.Number("1"), "remediationRound": json.Number("0"), "checks": []any{},
		"legacySource": nil, "lastOperationId": nil, "updatedAt": "2026-09-20T10:00:00Z",
	}
	verificationDigest, err := mancodeDigestFields(verification, mancodeWorkflowVerificationDigestFields)
	if err != nil {
		t.Fatal(err)
	}
	verification["contentDigest"] = verificationDigest

	implementationScope := map[string]any{"source": "explicit", "include": []any{}, "exclude": []any{}, "modules": []any{}}
	scopeDigest, err := mancodeCanonicalDigest(implementationScope)
	if err != nil {
		t.Fatal(err)
	}
	implementationScope["digest"] = scopeDigest
	metadata := map[string]any{
		"schemaVersion": json.Number("3"), "taskRef": taskRef, "displaySlug": "migration-fixture",
		"task": "Verify migration semantics", "workflowMode": workflowMode, "visibility": namespace,
		"coordination": coordination, "status": "in_progress", "currentStep": json.Number("1"),
		"skippedSteps": []any{}, "blockingReason": nil, "outcome": nil, "revision": json.Number("1"),
		"transitionState": "stable", "lastOperationId": nil, "ownerActorId": taskID,
		"ownershipEpoch": json.Number("0"), "participants": []any{},
		"createdBy": map[string]any{"actorId": taskID, "client": "test", "source": "actor"},
		"base":      nil, "implementationScope": implementationScope,
		"governance": map[string]any{
			"requirementsStatus": "needs_clarification", "requirementsDigest": requirementsDigest,
			"planVersion": json.Number("1"), "planDecision": "plan_only",
			"policyVersions": map[string]any{"planning": json.Number("1"), "review": json.Number("1"), "verification": nil},
			"reviewStatus":   "pending", "reviewLedgerDigest": reviewDigest,
			"verificationStatus": "pending", "verificationLedgerDigest": verificationDigest,
		},
		"soloExecution": nil, "latestCheckpointRef": nil, "parent": nil, "successorTaskRef": nil,
		"legacyCompatibility": nil, "startedAt": "2026-09-20T10:00:00Z", "updatedAt": "2026-09-20T10:00:00Z",
	}
	directory := filepath.ToSlash(filepath.Join(".mancode", namespace, "workflows", taskID))
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(directory, "metadata.json")), metadata)
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(directory, "requirements.json")), requirements)
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(directory, "review-ledger.json")), review)
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(directory, "verification-ledger.json")), verification)
	writeMancodeFixture(t, root, filepath.ToSlash(filepath.Join(directory, "plan.md")), []byte("# Durable plan\n"))
	return directory
}

func TestMancodeV3WorkflowAggregateIsReconstructedAndSnapshotReconciles(t *testing.T) {
	root := t.TempDir()
	directory := mancodeV3Fixture(t, root, "local", strings.Repeat("0", 25)+"1", false)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	var metadataEntry FileEntry
	for _, entry := range manifest.Files {
		if entry.Path == filepath.ToSlash(filepath.Join(directory, "metadata.json")) {
			metadataEntry = entry
			break
		}
	}
	if metadataEntry.ImportedCount != 2 || metadataEntry.RecordCount != 2 ||
		manifest.Summary.RecordsImported != 6 || manifest.Summary.RecordsFound != 6 {
		t.Fatalf("Mancode aggregate counts did not reconcile: entry=%#v summary=%#v", metadataEntry, manifest.Summary)
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
	var aggregate map[string]any
	var aggregatePayload string
	for _, record := range records {
		if record.Type == "mancode_workflow_aggregate" {
			aggregatePayload = string(record.Payload)
			if record.Scope != "local_pending_review" {
				t.Fatalf("aggregate escaped pending review scope: %#v", record)
			}
			if err := json.Unmarshal(record.Payload, &aggregate); err != nil {
				t.Fatal(err)
			}
		}
	}
	if aggregate == nil || aggregate["status"] != "complete" || aggregate["activation_allowed"] != false ||
		aggregate["aggregate_digest"] == nil || aggregate["plan_digest"] == nil {
		t.Fatalf("Mancode V3 aggregate was not semantically reconstructed: %#v", aggregate)
	}
	if strings.Contains(aggregatePayload, "Durable plan") {
		t.Fatal("aggregate unexpectedly contains plan text")
	}
}

func TestMancodeV3WorkflowAggregateReportsCrossLedgerConflict(t *testing.T) {
	root := t.TempDir()
	directory := mancodeV3Fixture(t, root, "shared", strings.Repeat("0", 25)+"1", true)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
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
	found := false
	for _, record := range records {
		if record.Type != "mancode_workflow_aggregate" {
			continue
		}
		var aggregate map[string]any
		if err := json.Unmarshal(record.Payload, &aggregate); err != nil {
			t.Fatal(err)
		}
		issues, _ := aggregate["issues"].([]any)
		for _, issue := range issues {
			if issue == "review_task_ref_mismatch" {
				found = true
			}
		}
		if aggregate["status"] != "conflicting" || aggregate["aggregate_digest"] != nil {
			t.Fatalf("cross-ledger mismatch was treated as coherent: %#v", aggregate)
		}
	}
	if !found {
		t.Fatalf("cross-ledger mismatch did not appear in migration output for %s: %#v", directory, manifest.Summary)
	}
}

func TestMancodeV3MalformedNestedValuesFailClosed(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		mutate    func(map[string]any) bool
		wantIssue string
	}{
		{
			name:      "coverage entry is not an object",
			file:      "requirements.json",
			wantIssue: "invalid_requirements_schema",
			mutate: func(value map[string]any) bool {
				coverage, ok := value["coverage"].([]any)
				if !ok {
					return false
				}
				value["coverage"] = append(coverage, "not-an-object")
				return true
			},
		},
		{
			name:      "latest checkpoint reference is not an object",
			file:      "metadata.json",
			wantIssue: "invalid_latest_checkpoint_ref",
			mutate: func(value map[string]any) bool {
				value["latestCheckpointRef"] = "not-an-object"
				return true
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			directory := mancodeV3Fixture(t, root, "local", strings.Repeat("0", 25)+"4", false)
			path := filepath.Join(root, filepath.FromSlash(filepath.Join(directory, test.file)))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			if !test.mutate(value) {
				t.Fatal("fixture mutation failed")
			}
			if test.name == "coverage entry is not an object" {
				requirementsDigest, err := mancodeWorkflowLedgerDigest("requirements", value)
				if err != nil {
					t.Fatal(err)
				}
				value["contentDigest"] = requirementsDigest
				metadataPath := filepath.ToSlash(filepath.Join(directory, "metadata.json"))
				metadata := readMancodeJSONFixture(t, root, metadataPath)
				governance := metadata["governance"].(map[string]any)
				governance["requirementsDigest"] = requirementsDigest
				reviewPath := filepath.ToSlash(filepath.Join(directory, "review-ledger.json"))
				review := readMancodeJSONFixture(t, root, reviewPath)
				review["requirementsDigest"] = requirementsDigest
				reviewDigest, err := mancodeWorkflowLedgerDigest("review", review)
				if err != nil {
					t.Fatal(err)
				}
				review["contentDigest"] = reviewDigest
				governance["reviewLedgerDigest"] = reviewDigest
				verificationPath := filepath.ToSlash(filepath.Join(directory, "verification-ledger.json"))
				verification := readMancodeJSONFixture(t, root, verificationPath)
				verification["requirementsDigest"] = requirementsDigest
				verificationDigest, err := mancodeWorkflowLedgerDigest("verification", verification)
				if err != nil {
					t.Fatal(err)
				}
				verification["contentDigest"] = verificationDigest
				governance["verificationLedgerDigest"] = verificationDigest
				writeMancodeJSONFixture(t, root, reviewPath, review)
				writeMancodeJSONFixture(t, root, verificationPath, verification)
				writeMancodeJSONFixture(t, root, metadataPath, metadata)
			}
			data, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
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
			for _, record := range records {
				if record.Type != "mancode_workflow_aggregate" {
					continue
				}
				var aggregate map[string]any
				if err := json.Unmarshal(record.Payload, &aggregate); err != nil {
					t.Fatal(err)
				}
				issues, _ := aggregate["issues"].([]any)
				for _, issue := range issues {
					if issue == test.wantIssue && aggregate["status"] == "incomplete" {
						return
					}
				}
				t.Fatalf("malformed source was not reported as an incomplete aggregate: %#v", aggregate)
			}
			t.Fatal("snapshot omitted the Mancode V3 aggregate")
		})
	}
}

func TestMancodeV3ReviewDeliveryNestedSchemaAndAcceptanceBinding(t *testing.T) {
	validDelivery := mancodeReviewDeliveryFixture(nil)
	if !mancodeWorkflowReviewDeliveryShape(validDelivery) {
		t.Fatal("valid review delivery evidence was rejected")
	}
	withUnknownField := mancodeReviewDeliveryFixture(nil)
	withUnknownField["unreviewed"] = "must not be silently accepted"
	if mancodeWorkflowReviewDeliveryShape(withUnknownField) {
		t.Fatal("review delivery accepted an unknown nested field")
	}
	withDuplicateCoverage := mancodeReviewDeliveryFixture([]any{
		map[string]any{"acceptanceId": "AC-1", "status": "met", "evidence": "checked"},
		map[string]any{"acceptanceId": "AC-1", "status": "met", "evidence": "checked again"},
	})
	if mancodeWorkflowReviewDeliveryShape(withDuplicateCoverage) {
		t.Fatal("review delivery accepted duplicate acceptance coverage")
	}

	requirements := map[string]any{"acceptanceCriteria": []any{
		map[string]any{"displayId": "AC-1", "required": true},
	}}
	review := map[string]any{"status": "passed", "delivery": mancodeReviewDeliveryFixture([]any{
		map[string]any{"acceptanceId": "AC-1", "status": "missing", "evidence": "not verified"},
	})}
	issues := map[string]bool{}
	checkReviewDeliveryContext(review, requirements, func(code string, conflict bool) {
		if conflict {
			issues[code] = true
		}
	})
	if !issues["review_delivery_acceptance_unmet"] {
		t.Fatalf("passed review with unmet required acceptance was not reported: %#v", issues)
	}

	review["delivery"] = mancodeReviewDeliveryFixture([]any{
		map[string]any{"acceptanceId": "AC-1", "status": "met", "evidence": "verified"},
	})
	issues = map[string]bool{}
	checkReviewDeliveryContext(review, requirements, func(code string, conflict bool) {
		if conflict {
			issues[code] = true
		}
	})
	if len(issues) != 0 {
		t.Fatalf("passed review with met required acceptance was rejected: %#v", issues)
	}

	review["delivery"] = mancodeReviewDeliveryFixture([]any{
		map[string]any{"acceptanceId": "AC-UNKNOWN", "status": "unverified", "evidence": "unlinked"},
	})
	issues = map[string]bool{}
	checkReviewDeliveryContext(review, requirements, func(code string, conflict bool) {
		if conflict {
			issues[code] = true
		}
	})
	if !issues["review_delivery_unknown_acceptance"] {
		t.Fatalf("review coverage for an unknown acceptance criterion was not reported: %#v", issues)
	}
}

func TestMancodeV3ReviewDeliveryImportIsReconciledAndAggregateStaysDigestOnly(t *testing.T) {
	root := t.TempDir()
	directory := mancodeV3Fixture(t, root, "local", strings.Repeat("0", 25)+"5", false)
	reviewPath := filepath.Join(root, filepath.FromSlash(filepath.Join(directory, "review-ledger.json")))
	data, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	items, err := decodeMancodeSourceItems("review-ledger.json", data)
	if err != nil || len(items) != 1 {
		t.Fatalf("decode review fixture: items=%d err=%v", len(items), err)
	}
	review, ok := items[0].(map[string]any)
	if !ok {
		t.Fatal("review fixture is not an object")
	}
	review["delivery"] = mancodeReviewDeliveryFixture([]any{
		map[string]any{"acceptanceId": "AC-UNKNOWN", "status": "unverified", "evidence": "private review note"},
	})
	review["contentDigest"], err = mancodeWorkflowLedgerDigest("review", review)
	if err != nil {
		t.Fatal(err)
	}
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(directory, "review-ledger.json")), review)

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
	for _, record := range records {
		if record.Type != "mancode_workflow_aggregate" {
			continue
		}
		var aggregate map[string]any
		if err := json.Unmarshal(record.Payload, &aggregate); err != nil {
			t.Fatal(err)
		}
		issues, _ := aggregate["issues"].([]any)
		found := false
		for _, issue := range issues {
			found = found || issue == "review_delivery_unknown_acceptance"
		}
		if aggregate["status"] != "conflicting" || !found || strings.Contains(string(record.Payload), "private review note") {
			t.Fatalf("review evidence was not safely reconciled: aggregate=%s", record.Payload)
		}
		return
	}
	t.Fatal("snapshot omitted the Mancode V3 aggregate")
}

func TestMancodeV3ExecutionV2PolicyAndRunHistoryValidation(t *testing.T) {
	state := mancodeExecutionStateFixture(t)
	if !mancodeWorkflowExecutionStateShape(state) {
		t.Fatal("valid local execution state was rejected")
	}
	withChecks := cloneMancodeJSON(state).(map[string]any)
	policy := withChecks["policy"].(map[string]any)
	policy["checks"] = []any{map[string]any{
		"id": "unit-check", "acceptanceIds": []any{"AC-1"}, "argv": []any{"go", "test"},
		"cwd": ".", "surface": "unit",
	}}
	policy["scenarios"] = []any{map[string]any{
		"id": "unit-scenario", "acceptanceId": "AC-1", "mode": "required", "rationale": "Exercise the scoped unit behavior",
		"alternativeCheckIds": []any{}, "testInputs": []any{"internal"}, "configInputs": []any{},
		"targets": []any{map[string]any{"file": "internal/example_test.go", "name": "Example"}},
	}}
	rehashMancodeExecutionPolicy(t, withChecks)
	if !mancodeWorkflowExecutionStateShape(withChecks) {
		t.Fatal("valid execution check and required test scenario were rejected")
	}
	pathBoundary := cloneMancodeJSON(withChecks).(map[string]any)
	pathBoundaryScenario := pathBoundary["policy"].(map[string]any)["scenarios"].([]any)[0].(map[string]any)
	pathBoundaryScenario["targets"].([]any)[0].(map[string]any)["file"] = "internal-other/example_test.go"
	rehashMancodeExecutionPolicy(t, pathBoundary)
	if mancodeWorkflowExecutionStateShape(pathBoundary) {
		t.Fatal("required test input directory was treated as a string prefix for a sibling path")
	}
	remotePolicy := cloneMancodeJSON(state).(map[string]any)
	remotePolicy["policy"].(map[string]any)["delivery"] = "remote_required"
	remotePolicy["policy"].(map[string]any)["ci"] = map[string]any{
		"repository": "org/repo", "event": "pull_request", "testedBinding": "unverified",
		"workflows": []any{map[string]any{
			"id": json.Number("1"), "path": ".github/workflows/test.yml", "configurationSha": strings.Repeat("a", 40),
			"requiredJobs": []any{"test"},
		}},
	}
	rehashMancodeExecutionPolicy(t, remotePolicy)
	if !mancodeWorkflowExecutionStateShape(remotePolicy) {
		t.Fatal("valid remote-required CI policy was rejected")
	}

	badPolicy := cloneMancodeJSON(state).(map[string]any)
	badPolicy["policy"].(map[string]any)["budget"].(map[string]any)["hidden"] = true
	rehashMancodeExecutionPolicy(t, badPolicy)
	if mancodeWorkflowExecutionStateShape(badPolicy) {
		t.Fatal("execution policy accepted an unknown nested budget field")
	}

	withRun := cloneMancodeJSON(state).(map[string]any)
	validRun := mancodeExecutionRunFixture(state)
	withRun["runs"] = []any{validRun}
	if !mancodeWorkflowExecutionStateShape(withRun) {
		t.Fatal("valid reserved verification run was rejected")
	}
	withRetry := cloneMancodeJSON(withRun).(map[string]any)
	firstRunID := mancodeString(withRetry["runs"].([]any)[0].(map[string]any)["runId"])
	secondRun := mancodeExecutionRunFixture(state)
	secondRun["runId"] = strings.Repeat("0", 25) + "A"
	secondRun["infrastructureRetryOf"] = firstRunID
	withRetry["runs"] = append(withRetry["runs"].([]any), secondRun)
	if !mancodeWorkflowExecutionStateShape(withRetry) {
		t.Fatal("valid retry reference was rejected")
	}
	secondRun["infrastructureRetryOf"] = strings.Repeat("0", 25) + "B"
	if mancodeWorkflowExecutionStateShape(withRetry) {
		t.Fatal("run accepted an infrastructure retry reference to a missing run")
	}
	finishedRun := mancodeExecutionRunFixture(state)
	runID := mancodeString(finishedRun["runId"])
	executorID := strings.Repeat("0", 25) + "9"
	identity := mancodeExecutionIdentityFixture(runID, executorID)
	finishedRun["identity"] = identity
	finishedRun["state"] = "succeeded"
	finishedRun["result"] = map[string]any{
		"identity": identity, "status": "succeeded", "started": true, "exitCode": json.Number("0"),
		"signal": nil, "reason": nil, "startedAt": "2026-09-26T10:00:00Z", "finishedAt": "2026-09-26T10:00:01Z",
		"durationMs": json.Number("1000"), "outputTruncated": false, "cleanupConfirmed": true,
		"outputArtifactRef": nil, "summary": "check passed",
	}
	if _, _, _, valid := mancodeWorkflowExecutionRunShape(finishedRun); !valid {
		t.Fatal("valid finished run result and matching identity were rejected")
	}
	finishedRun["result"].(map[string]any)["identity"].(map[string]any)["runId"] = strings.Repeat("0", 25) + "A"
	if _, _, _, valid := mancodeWorkflowExecutionRunShape(finishedRun); valid {
		t.Fatal("run accepted a result identity belonging to another run")
	}
	vitestRun := mancodeExecutionRunFixture(state)
	vitestRun["testIdentity"] = "sha256:" + strings.Repeat("c", 64)
	vitestRun["configurationIdentity"] = "sha256:" + strings.Repeat("d", 64)
	vitestIdentity := mancodeExecutionIdentityFixture(mancodeString(vitestRun["runId"]), executorID)
	vitestRun["identity"] = vitestIdentity
	vitestRun["vitest"] = map[string]any{
		"adapter": "vitest", "adapterVersion": json.Number("1"), "runId": vitestRun["runId"], "executorId": executorID,
		"testIdentity": vitestRun["testIdentity"], "configurationIdentity": vitestRun["configurationIdentity"],
		"assessment": "passed", "targets": []any{}, "collectionErrors": json.Number("0"),
		"unhandledErrors": json.Number("0"), "reasons": []any{},
	}
	if _, _, _, valid := mancodeWorkflowExecutionRunShape(vitestRun); !valid {
		t.Fatal("Vitest assessment with matching run/executor/test identity was rejected")
	}
	vitestRun["vitest"].(map[string]any)["executorId"] = strings.Repeat("0", 25) + "A"
	if _, _, _, valid := mancodeWorkflowExecutionRunShape(vitestRun); valid {
		t.Fatal("run accepted Vitest evidence bound to a different executor")
	}
	badRun := cloneMancodeJSON(withRun).(map[string]any)
	badRun["runs"].([]any)[0].(map[string]any)["unrecognized"] = true
	if mancodeWorkflowExecutionStateShape(badRun) {
		t.Fatal("execution run accepted an unknown nested field")
	}
	withAttempt := cloneMancodeJSON(state).(map[string]any)
	attemptRun := mancodeExecutionRunFixture(state)
	attemptID := strings.Repeat("0", 25) + "B"
	attemptRun["attemptId"] = attemptID
	withAttempt["runs"] = []any{attemptRun}
	withAttempt["attempts"] = []any{mancodeExecutionAttemptFixture(attemptID, mancodeString(attemptRun["runId"]))}
	if !mancodeWorkflowExecutionStateShape(withAttempt) {
		t.Fatal("valid repair attempt and linked run were rejected")
	}
	brokenAttempt := cloneMancodeJSON(withAttempt).(map[string]any)
	brokenAttempt["attempts"].([]any)[0].(map[string]any)["runIds"] = []any{strings.Repeat("0", 25) + "C"}
	if mancodeWorkflowExecutionStateShape(brokenAttempt) {
		t.Fatal("attempt accepted a run reference that does not point back to the attempt")
	}
	withDecision := cloneMancodeJSON(state).(map[string]any)
	withDecision["decisions"] = []any{mancodeExecutionDecisionFixture(state)}
	if !mancodeWorkflowExecutionStateShape(withDecision) {
		t.Fatal("valid approved decision history was rejected")
	}
	badDecision := cloneMancodeJSON(withDecision).(map[string]any)
	badDecision["decisions"].([]any)[0].(map[string]any)["approval"].(map[string]any)["unexpected"] = "ignored?"
	if mancodeWorkflowExecutionStateShape(badDecision) {
		t.Fatal("execution decision accepted an unknown nested approval field")
	}

	withCI := cloneMancodeJSON(state).(map[string]any)
	ciRun, ciObservation := mancodeExecutionCIObservationFixture(t, withCI)
	withCI["runs"] = []any{ciRun}
	withCI["ciObservations"] = []any{ciObservation}
	if !mancodeWorkflowExecutionStateShape(withCI) {
		t.Fatal("valid CI observation linked to a successful applicable CI run was rejected")
	}
	for name, mutate := range map[string]func(map[string]any){
		"target_digest":        func(item map[string]any) { item["targetDigest"] = "sha256:" + strings.Repeat("f", 64) },
		"policy_digest_format": func(item map[string]any) { item["policyDigest"] = "not-a-digest" },
		"target_observation_mismatch": func(item map[string]any) {
			item["observation"].(map[string]any)["candidateSha"] = strings.Repeat("d", 40)
		},
		"unknown_run_field": func(item map[string]any) {
			item["observation"].(map[string]any)["runs"].([]any)[0].(map[string]any)["surprise"] = true
		},
		"unknown_target_field": func(item map[string]any) { item["target"].(map[string]any)["surprise"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			broken := cloneMancodeJSON(withCI).(map[string]any)
			mutate(broken["ciObservations"].([]any)[0].(map[string]any))
			if mancodeWorkflowExecutionStateShape(broken) {
				t.Fatal("invalid CI observation history was accepted")
			}
		})
	}
	duplicateCI := cloneMancodeJSON(withCI).(map[string]any)
	duplicateCI["ciObservations"] = append(duplicateCI["ciObservations"].([]any), cloneMancodeJSON(ciObservation))
	if mancodeWorkflowExecutionStateShape(duplicateCI) {
		t.Fatal("duplicate CI observation and execution-run references were accepted")
	}
	for name, mutate := range map[string]func(map[string]any){
		"wrong_purpose":  func(run map[string]any) { run["purpose"] = "verification" },
		"not_successful": func(run map[string]any) { run["state"] = "failed" },
		"not_applicable": func(run map[string]any) { run["applicable"] = false },
	} {
		t.Run(name, func(t *testing.T) {
			broken := cloneMancodeJSON(withCI).(map[string]any)
			mutate(broken["runs"].([]any)[0].(map[string]any))
			if mancodeWorkflowExecutionStateShape(broken) {
				t.Fatal("CI observation accepted an ineligible execution run")
			}
		})
	}

	for _, historyKind := range []string{"empty", "run", "attempt", "decision", "ci"} {
		t.Run(historyKind, func(t *testing.T) {
			root := t.TempDir()
			directory := mancodeV3Fixture(t, root, "local", strings.Repeat("0", 25)+"6", false)
			fixtureState := mancodeExecutionStateFixture(t)
			if historyKind == "run" {
				fixtureState["runs"] = []any{mancodeExecutionRunFixture(fixtureState)}
			} else if historyKind == "attempt" {
				run := mancodeExecutionRunFixture(fixtureState)
				attemptID := strings.Repeat("0", 25) + "B"
				run["attemptId"] = attemptID
				fixtureState["runs"] = []any{run}
				fixtureState["attempts"] = []any{mancodeExecutionAttemptFixture(attemptID, mancodeString(run["runId"]))}
			} else if historyKind == "decision" {
				fixtureState["decisions"] = []any{mancodeExecutionDecisionFixture(fixtureState)}
			} else if historyKind == "ci" {
				run, observation := mancodeExecutionCIObservationFixture(t, fixtureState)
				fixtureState["runs"] = []any{run}
				fixtureState["ciObservations"] = []any{observation}
			}
			writeMancodeV2VerificationFixture(t, root, directory, fixtureState)
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
			for _, record := range records {
				if record.Type != "mancode_workflow_aggregate" {
					continue
				}
				var aggregate map[string]any
				if err := json.Unmarshal(record.Payload, &aggregate); err != nil {
					t.Fatal(err)
				}
				wantStatus := "complete"
				if aggregate["status"] != wantStatus {
					t.Fatalf("execution history status=%v, want %s: %#v", aggregate["status"], wantStatus, aggregate)
				}
				return
			}
			t.Fatal("snapshot omitted the Mancode V3 aggregate")
		})
	}
}

func mancodeExecutionStateFixture(t *testing.T) map[string]any {
	t.Helper()
	policy := map[string]any{
		"version": json.Number("1"),
		"budget": map[string]any{
			"maxRuns": json.Number("1"), "maxExecutionMs": json.Number("10000"),
			"maxRepairAttempts": json.Number("0"), "commandTimeoutMs": json.Number("1000"), "ciTimeoutMs": json.Number("1000"),
		},
		"checks": []any{}, "scenarios": []any{}, "delivery": "local", "ci": nil,
	}
	digest, err := mancodeCanonicalDigest(policy)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"policy": policy, "policyDigest": digest, "runs": []any{}, "attempts": []any{},
		"decisions": []any{}, "ciObservations": []any{},
	}
}

func mancodeExecutionCIObservationFixture(t *testing.T, state map[string]any) (map[string]any, map[string]any) {
	t.Helper()
	candidateSHA := strings.Repeat("c", 40)
	configurationSHA := strings.Repeat("a", 40)
	policy := state["policy"].(map[string]any)
	policy["delivery"] = "remote_required"
	policy["ci"] = map[string]any{
		"repository": "org/repo", "event": "pull_request", "testedBinding": "unverified",
		"workflows": []any{map[string]any{
			"id": json.Number("1"), "path": ".github/workflows/test.yml", "configurationSha": configurationSHA,
			"requiredJobs": []any{"test"},
		}},
	}
	rehashMancodeExecutionPolicy(t, state)
	target := map[string]any{
		"repository": "org/repo", "event": "pull_request", "candidateSha": candidateSHA,
		"testedSha": candidateSHA, "testedBinding": "unverified",
		"workflows": []any{map[string]any{
			"id": json.Number("1"), "path": ".github/workflows/test.yml", "configurationSha": configurationSHA,
			"requiredJobs": []any{"test"}, "runId": json.Number("101"),
		}},
	}
	run := mancodeExecutionRunFixture(state)
	run["purpose"] = "ci_observation"
	runID := mancodeString(run["runId"])
	identity := mancodeExecutionIdentityFixture(runID, strings.Repeat("0", 25)+"9")
	run["identity"] = identity
	run["state"] = "succeeded"
	run["result"] = map[string]any{
		"identity": identity, "status": "succeeded", "started": true, "exitCode": json.Number("0"),
		"signal": nil, "reason": nil, "startedAt": "2026-09-26T10:00:00Z", "finishedAt": "2026-09-26T10:00:01Z",
		"durationMs": json.Number("1000"), "outputTruncated": false, "cleanupConfirmed": true,
		"outputArtifactRef": nil, "summary": "CI observation collected",
	}
	observation := map[string]any{
		"provider": "github", "repository": "org/repo", "candidateSha": candidateSHA,
		"testedSha": candidateSHA, "event": "pull_request", "observedAt": "2026-09-26T10:00:02Z",
		"status": "passed", "reasons": []any{}, "runs": []any{map[string]any{
			"runId": json.Number("101"), "attempt": json.Number("1"), "workflowId": json.Number("1"),
			"workflowPath": ".github/workflows/test.yml", "configurationSha": configurationSHA,
			"headSha": candidateSHA, "status": "completed", "conclusion": "success",
			"jobs": []any{map[string]any{"id": json.Number("501"), "name": "test", "status": "completed", "conclusion": "success"}},
		}},
	}
	targetDigest, err := mancodeCanonicalDigest(target)
	if err != nil {
		t.Fatal(err)
	}
	return run, map[string]any{
		"observationId": strings.Repeat("0", 25) + "A", "runId": runID,
		"policyDigest": state["policyDigest"], "targetDigest": targetDigest,
		"target": target, "observation": observation,
	}
}

func mancodeExecutionRunFixture(state map[string]any) map[string]any {
	return map[string]any{
		"runId": strings.Repeat("0", 25) + "7", "purpose": "verification", "checkId": nil, "scenarioId": nil,
		"problemId": nil, "attemptId": nil, "infrastructureRetryOf": nil, "hypothesis": nil,
		"argv": []any{"go", "test"}, "runnerArgv": []any{"go", "test"}, "cwd": ".", "timeoutMs": json.Number("1000"),
		"subject":      map[string]any{"contentDigest": "sha256:" + strings.Repeat("a", 64), "environment": "fixture-runtime"},
		"testIdentity": nil, "configurationIdentity": nil, "requirementsDigest": "sha256:" + strings.Repeat("b", 64),
		"planVersion": json.Number("1"), "policyDigest": state["policyDigest"], "actorId": strings.Repeat("0", 25) + "8",
		"checkoutId": "fixture-checkout", "reservedAt": "2026-09-26T10:00:00Z", "state": "reserved",
		"identity": nil, "result": nil, "interruptions": []any{}, "vitest": nil, "applicable": true,
	}
}

func mancodeExecutionAttemptFixture(attemptID, runID string) map[string]any {
	return map[string]any{
		"attemptId": attemptID, "problemId": "problem-1", "classification": "implementation",
		"hypothesis": "Verify the repair attempt history", "evidence": "Recorded from a local verification run",
		"subject":    map[string]any{"contentDigest": "sha256:" + strings.Repeat("a", 64), "environment": "fixture-runtime"},
		"reservedAt": "2026-09-26T10:00:00Z", "finishedAt": nil, "state": "reserved",
		"runIds": []any{runID}, "summary": nil, "interruptions": []any{},
	}
}

func mancodeExecutionDecisionFixture(state map[string]any) map[string]any {
	return map[string]any{
		"decisionId": strings.Repeat("0", 25) + "D", "kind": "budget_extension",
		"actorId": strings.Repeat("0", 25) + "E", "createdAt": "2026-09-26T10:00:00Z",
		"approval":  map[string]any{"confirmed": true, "source": "actor", "reason": "Review the budget change", "evidence": "Local review record"},
		"problemId": nil, "sourceProblemIds": []any{"problem-1"}, "scenarioId": nil, "runId": nil,
		"requirementsDigest": "sha256:" + strings.Repeat("a", 64), "policyDigest": state["policyDigest"],
		"expiresAt": nil,
		"delta": map[string]any{
			"runs": json.Number("1"), "executionMs": json.Number("1000"),
			"repairAttempts": json.Number("0"), "problemFailures": json.Number("0"),
		},
		"previousPolicy": nil,
	}
}

func mancodeExecutionIdentityFixture(runID, executorID string) map[string]any {
	return map[string]any{
		"protocolVersion": json.Number("1"), "runId": runID, "executorId": executorID,
		"commandDigest": "sha256:" + strings.Repeat("e", 64), "startedAt": "2026-09-26T10:00:00Z",
		"pid": json.Number("1200"), "platform": "windows/amd64",
	}
}

func rehashMancodeExecutionPolicy(t *testing.T, state map[string]any) {
	t.Helper()
	digest, err := mancodeCanonicalDigest(state["policy"])
	if err != nil {
		t.Fatal(err)
	}
	state["policyDigest"] = digest
}

func writeMancodeV2VerificationFixture(t *testing.T, root, directory string, execution map[string]any) {
	t.Helper()
	verificationPath := filepath.ToSlash(filepath.Join(directory, "verification-ledger.json"))
	verification := readMancodeJSONFixture(t, root, verificationPath)
	verification["schemaVersion"] = json.Number("2")
	verification["execution"] = execution
	verificationDigestValue, err := mancodeWorkflowLedgerDigest("verification", verification)
	if err != nil {
		t.Fatal(err)
	}
	verification["contentDigest"] = verificationDigestValue
	verificationDigest := mancodeString(verification["contentDigest"])
	metadataPath := filepath.ToSlash(filepath.Join(directory, "metadata.json"))
	metadata := readMancodeJSONFixture(t, root, metadataPath)
	governance := metadata["governance"].(map[string]any)
	governance["policyVersions"].(map[string]any)["verification"] = json.Number("2")
	governance["verificationLedgerDigest"] = verificationDigest
	writeMancodeJSONFixture(t, root, verificationPath, verification)
	writeMancodeJSONFixture(t, root, metadataPath, metadata)
}

func readMancodeJSONFixture(t *testing.T, root, relative string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	items, err := decodeMancodeSourceItems(relative, data)
	if err != nil || len(items) != 1 {
		t.Fatalf("decode Mancode fixture %s: items=%d err=%v", relative, len(items), err)
	}
	object, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("Mancode fixture %s is not an object", relative)
	}
	return object
}

func mancodeReviewDeliveryFixture(coverage []any) map[string]any {
	if coverage == nil {
		coverage = []any{}
	}
	return map[string]any{
		"subject": map[string]any{
			"contentDigest": "sha256:" + strings.Repeat("a", 64),
			"environment":   "fixture-runtime",
		},
		"reviewer": "independent", "direction": "Review the accepted scope",
		"correctness": "Check linked evidence", "proportionality": "Use focused review",
		"nextAction": "Resolve any missing evidence", "coverage": coverage,
	}
}

func TestMancodeV3WorkflowAggregateSupportsMancodeDirectorySourceRoot(t *testing.T) {
	root := t.TempDir()
	mancodeRoot := filepath.Join(root, ".mancode")
	directory := mancodeV3Fixture(t, root, "local", strings.Repeat("0", 25)+"3", false)
	relativeMetadata := strings.TrimPrefix(filepath.ToSlash(filepath.Join(directory, "metadata.json")), ".mancode/")
	manifest, err := Inspect(Mancode, mancodeRoot)
	if err != nil {
		t.Fatal(err)
	}
	foundMetadata := false
	for _, entry := range manifest.Files {
		foundMetadata = foundMetadata || entry.Path == relativeMetadata
	}
	if !foundMetadata {
		t.Fatalf("Mancode-root scan did not retain relative workflow path %q", relativeMetadata)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Mancode, mancodeRoot, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Type == "mancode_workflow_aggregate" {
			return
		}
	}
	t.Fatal("Mancode-root snapshot did not contain a reconstructed workflow aggregate")
}

func TestMancodeCanonicalJSONUsesSafeJCSOrderingAndEscaping(t *testing.T) {
	value := map[string]any{
		"\uE000":     json.Number("1.0"),
		"\U00010000": "<>&\n",
	}
	encoded, err := mancodeCanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	expected := "{\"\U00010000\":\"<>&\\n\",\"\uE000\":1}"
	if string(encoded) != expected {
		t.Fatalf("Mancode canonical JSON mismatch:\n got %s\nwant %s", encoded, expected)
	}
	if _, err := mancodeCanonicalJSON(json.Number("-0")); err == nil {
		t.Fatal("negative zero must not be accepted by the Mancode JCS profile")
	}
	if _, err := mancodeCanonicalJSON(json.Number("9007199254740992")); err == nil {
		t.Fatal("integers beyond the safe-integer boundary must be rejected")
	}
}

func writeMancodeJSONFixture(t *testing.T, root, relative string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeMancodeFixture(t, root, relative, data)
}

func mancodeProjectFactsFixture() map[string]any {
	return map[string]any{
		"schemaVersion": 1,
		"revision":      4,
		"trust":         "detected",
		"profile": map[string]any{
			"version": "1.0", "projectKind": "web",
			"languages":           []any{"TypeScript", "private-language-marker"},
			"frameworks":          []any{"private-framework-marker"},
			"sourceRoots":         []any{"src/private-root-marker"},
			"manifests":           []any{"package.json"},
			"availableValidation": []any{"private-validation-command-marker"},
			"uiAssets":            "detected", "browserAutomation": "available", "confidence": "high",
		},
		"uiLibrary": "private-ui-marker", "detectedAt": "2026-09-20T10:00:00Z",
		"lastOperationId": strings.Repeat("0", 25) + "1",
	}
}

func TestMancodeProjectAndTeamRecordsAreImportedAsLocalReview(t *testing.T) {
	root := t.TempDir()
	actorID := strings.Repeat("0", 25) + "1"
	fixtures := []struct {
		path  string
		value any
	}{
		{".mancode/shared/context/project.json", mancodeProjectFactsFixture()},
		{".mancode/shared/context/glossary.json", mancodeProjectGlossaryFixture()},
		{".mancode/shared/context/design-policy.json", mancodeDesignPolicyFixture()},
		{".mancode/shared/context/privacy-policy.json", mancodePrivacyPolicyFixture()},
		{".mancode/shared/context/privacy-exclusions.json", mancodePrivacyExclusionsFixture()},
		{".mancode/schema.json", mancodeSchemaManifestFixture(3)},
		{".mancode/shared/config.json", map[string]any{
			"schemaVersion": 1, "revision": 1, "workspaceId": actorID,
			"transport":       map[string]any{"mode": "git-ref", "epoch": 2, "remote": "https://migration-user:private-pass@example.invalid/repo.git"},
			"lastOperationId": actorID, "updatedAt": "2026-09-20T10:00:00Z",
		}},
		{".mancode/shared/team/policy.json", mancodeTeamPolicyFixture()},
		{".mancode/shared/team/actors/" + actorID + ".json", mancodeSharedActorFixture()},
		{".mancode/shared/team/events/" + actorID + ".json", mancodeTeamEventFixture(actorID)},
		{".mancode/shared/memory/decisions/" + actorID + ".json", mancodeConfirmedDecisionFixture(1, actorID)},
		{".mancode/shared/workflows/" + actorID + "/metadata.json", map[string]any{"schemaVersion": 1, "status": "in_progress"}},
		{".mancode/shared/workflows/" + actorID + "/requirements.json", map[string]any{"schemaVersion": 1, "goal": "Keep migration review-only"}},
		{".mancode/shared/workflows/" + actorID + "/plan.md", map[string]any{"unused": true}},
		{".mancode/shared/workflows/" + actorID + "/checkpoints/" + actorID + ".json", map[string]any{"schemaVersion": 1, "kind": "verification_completed"}},
		{".mancode/local/workflows/" + actorID + "/verification-ledger.json", map[string]any{"schemaVersion": 1, "status": "pending"}},
		{".mancode/local/actor.json", mancodeLocalActorFixture()},
		{"state.json", map[string]any{
			"version": "0.9.0", "currentMode": "man", "lastMode": "solo", "platform": "claude-code",
			"initializedAt": "2026-09-20T10:00:00Z", "techStack": "TypeScript API_TOKEN=do-not-copy-this-secret",
			"uiLibrary": "", "currentTask": "private-task-marker", "currentWorkflowMode": "man",
			"skippedSteps": []any{"private-step-marker"}, "activeSoloPlan": map[string]any{"taskId": "private-plan-marker", "planVersion": 2},
			"teamModeAutoDetected": true, "contributors": 3, "projectMode": "detected",
		}},
		{"workflows/legacy-task/metadata.json", map[string]any{"status": "completed"}},
	}
	for _, fixture := range fixtures {
		if strings.HasSuffix(fixture.path, ".md") {
			writeMancodeFixture(t, root, fixture.path, []byte("# Confirmed plan\n"))
			continue
		}
		writeMancodeJSONFixture(t, root, fixture.path, fixture.value)
	}

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != len(fixtures)+1 || manifest.Summary.RecordsImported != len(fixtures) ||
		manifest.Summary.RecordsCredentialBlocked != 0 || manifest.Summary.RecordsRejected != 1 {
		t.Fatalf("Mancode project/team records did not reconcile: summary=%#v files=%#v", manifest.Summary, manifest.Files)
	}
	var transportConfig *FileEntry
	for i := range manifest.Files {
		if manifest.Files[i].Path == ".mancode/shared/config.json" {
			transportConfig = &manifest.Files[i]
			break
		}
	}
	if transportConfig == nil || transportConfig.Status != "partially_rejected" ||
		transportConfig.RejectedCount != 1 || transportConfig.Issue != mancodeGitRefAuthorityIssue {
		t.Fatalf("unreconciled git-ref authority was not made visible: %#v", transportConfig)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Mancode, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	coverage, err := BuildFormatCoverage(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.RejectedRecords != 1 || coverage.UnclassifiedFiles != 0 || coverage.UnsupportedFiles != 0 {
		t.Fatalf("configured git-ref authority did not remain a classified cutover blocker: %#v", coverage)
	}
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, record := range records {
		types[record.Type] = true
		if record.Scope != "local_pending_review" {
			t.Fatalf("Mancode authority was imported outside local review: %#v", record)
		}
		if strings.Contains(string(record.Payload), "do-not-copy-this-secret") {
			t.Fatal("credential-bearing Mancode state entered the snapshot")
		}
		if record.Type == "mancode_project_config" {
			if strings.Contains(string(record.Payload), "private-pass") || strings.Contains(string(record.Payload), "lastOperationId") {
				t.Fatalf("Mancode project config carried a remote credential or live operation pointer: %s", record.Payload)
			}
			if !strings.Contains(string(record.Payload), `"remote_omitted":true`) {
				t.Fatalf("Mancode project config did not disclose omitted transport remote: %s", record.Payload)
			}
		}
	}
	for _, kind := range []string{
		"mancode_legacy_state", "mancode_schema_manifest", "project_fact", "project_glossary", "project_policy", "mancode_project_config", "mancode_team_policy", "mancode_actor_profile",
		"mancode_team_event", "mancode_actor_identity", "decision_record", "workflow_metadata",
		"workflow_requirements", "workflow_plan", "workflow_checkpoint", "workflow_verification",
	} {
		if !types[kind] {
			t.Errorf("Mancode snapshot is missing record type %q", kind)
		}
	}
}

func TestMancodeLegacyStateProjectionIsStrictAndOmitsFreeformSourceValues(t *testing.T) {
	root := t.TempDir()
	state := map[string]any{
		"version": "0.9.0", "currentMode": "solo", "lastMode": "man", "platform": "codex",
		"initializedAt": "2026-09-20T10:00:00Z", "techStack": "private-stack-marker",
		"uiLibrary": "private-ui-marker", "currentTask": "private-task-marker",
		"currentWorkflowMode": "man", "skippedSteps": []any{"private-step-marker"},
		"activeSoloPlan":       map[string]any{"taskId": "private-plan-marker", "planVersion": 4},
		"teamModeAutoDetected": true, "contributors": 3, "projectMode": "detected",
	}
	writeMancodeJSONFixture(t, root, ".mancode/state.json", state)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 ||
		len(manifest.Files) != 1 || manifest.Files[0].Status != "ready" {
		t.Fatalf("legacy state did not reconcile: summary=%#v files=%#v", manifest.Summary, manifest.Files)
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
	if len(records) != 1 || records[0].Type != "mancode_legacy_state" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected legacy-state snapshot records: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, private := range []string{"private-stack-marker", "private-ui-marker", "private-task-marker", "private-step-marker", "private-plan-marker"} {
		if strings.Contains(payload, private) {
			t.Fatalf("legacy state projection retained source value %q: %s", private, payload)
		}
	}
	var projection map[string]any
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["schema_version"] != "threadforge.mancode-legacy-state.v1" ||
		projection["authority"] != "local_pending_review" || projection["human_review_required"] != true ||
		projection["execution_authorized"] != false || projection["current_mode"] != "solo" ||
		projection["current_workflow_mode"] != "man" || projection["task_present"] != true ||
		projection["skipped_step_count"] != float64(1) || projection["active_solo_plan_present"] != true ||
		projection["active_solo_plan_version"] != float64(4) || projection["contributor_count"] != float64(3) {
		t.Fatalf("legacy state projection lost safe continuity metadata: %s", payload)
	}
}

func TestMancodeLegacyStateRejectsUnknownOrMalformedFields(t *testing.T) {
	base := map[string]any{
		"version": "0.9.0", "currentMode": "solo", "lastMode": "solo", "platform": "claude-code",
		"initializedAt": "2026-09-20T10:00:00Z", "techStack": "", "uiLibrary": "",
		"currentTask": nil, "currentWorkflowMode": nil, "skippedSteps": []any{}, "activeSoloPlan": nil,
		"teamModeAutoDetected": false, "contributors": 0,
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown key", mutate: func(value map[string]any) { value["api_key"] = "must-not-be-accepted" }},
		{name: "unknown mode", mutate: func(value map[string]any) { value["currentMode"] = "shell" }},
		{name: "wrong workflow mode", mutate: func(value map[string]any) { value["currentWorkflowMode"] = "unknown" }},
		{name: "malformed skipped step", mutate: func(value map[string]any) { value["skippedSteps"] = []any{42} }},
		{name: "malformed active plan", mutate: func(value map[string]any) { value["activeSoloPlan"] = map[string]any{"taskId": "x", "planVersion": 0} }},
		{name: "negative contributors", mutate: func(value map[string]any) { value["contributors"] = -1 }},
		{name: "invalid timestamp", mutate: func(value map[string]any) { value["initializedAt"] = "not-a-time" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			state := map[string]any{}
			for key, value := range base {
				state[key] = value
			}
			test.mutate(state)
			writeMancodeJSONFixture(t, root, "state.json", state)
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 0 || manifest.Summary.RecordsRejected != 1 ||
				len(manifest.Files) != 1 || manifest.Files[0].Status != "unsupported" {
				t.Fatalf("malformed legacy state was not rejected: summary=%#v files=%#v", manifest.Summary, manifest.Files)
			}
		})
	}
}

func TestMancodeProjectProfileSnapshotRetainsOnlyBoundedCategoriesAndCounts(t *testing.T) {
	root := t.TempDir()
	profile := map[string]any{
		"version": "1.0", "projectKind": "web",
		"languages":           []any{"TypeScript", "private-language-marker"},
		"frameworks":          []any{"private-framework-marker"},
		"sourceRoots":         []any{"src/private-root-marker"},
		"manifests":           []any{"package.json"},
		"availableValidation": []any{"private-validation-command-marker"},
		"uiAssets":            "detected", "browserAutomation": "unknown", "confidence": "high",
	}
	writeMancodeJSONFixture(t, root, ".mancode/project-profile.json", profile)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 ||
		len(manifest.Files) != 1 || manifest.Files[0].Status != "ready" {
		t.Fatalf("project profile did not reconcile: summary=%#v files=%#v", manifest.Summary, manifest.Files)
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
	if len(records) != 1 || records[0].Type != "mancode_project_profile" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected project-profile snapshot records: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, private := range []string{"TypeScript", "private-language-marker", "private-framework-marker", "private-root-marker", "package.json", "private-validation-command-marker"} {
		if strings.Contains(payload, private) {
			t.Fatalf("project profile projection retained source label %q: %s", private, payload)
		}
	}
	var projection map[string]any
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["schema_version"] != "threadforge.mancode-project-profile.v1" ||
		projection["authority"] != "local_pending_review" || projection["human_review_required"] != true || projection["execution_authorized"] != false ||
		projection["project_kind"] != "web" || projection["language_count"] != float64(2) ||
		projection["framework_count"] != float64(1) || projection["source_root_count"] != float64(1) ||
		projection["manifest_count"] != float64(1) || projection["validation_count"] != float64(1) ||
		projection["ui_assets"] != "detected" || projection["browser_automation"] != "unknown" || projection["confidence"] != "high" {
		t.Fatalf("project profile projection lost bounded metadata: %s", payload)
	}
}

func TestMancodeProjectProfileRejectsSchemaDriftAndOversizedLists(t *testing.T) {
	base := map[string]any{
		"version": "1.0", "projectKind": "unknown", "languages": []any{}, "frameworks": []any{},
		"sourceRoots": []any{}, "manifests": []any{}, "availableValidation": []any{},
		"uiAssets": "none", "browserAutomation": "unknown", "confidence": "low",
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown key", mutate: func(value map[string]any) { value["projectRoot"] = "C:/private" }},
		{name: "unknown kind", mutate: func(value map[string]any) { value["projectKind"] = "private-kind" }},
		{name: "invalid array member", mutate: func(value map[string]any) { value["languages"] = []any{42} }},
		{name: "oversized array", mutate: func(value map[string]any) {
			items := make([]any, 129)
			for i := range items {
				items[i] = "language"
			}
			value["languages"] = items
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			profile := map[string]any{}
			for key, value := range base {
				profile[key] = value
			}
			test.mutate(profile)
			writeMancodeJSONFixture(t, root, "project-profile.json", profile)
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 0 || manifest.Summary.RecordsRejected != 1 ||
				len(manifest.Files) != 1 || manifest.Files[0].Status != "unsupported" {
				t.Fatalf("invalid project profile was not rejected: summary=%#v files=%#v", manifest.Summary, manifest.Files)
			}
		})
	}
}

func TestMancodeProjectFactsSnapshotRetainsOnlyBoundedReviewMetadata(t *testing.T) {
	root := t.TempDir()
	writeMancodeJSONFixture(t, root, ".mancode/shared/context/project.json", mancodeProjectFactsFixture())
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 ||
		len(manifest.Files) != 1 || manifest.Files[0].Status != "ready" {
		t.Fatalf("project facts did not reconcile: summary=%#v files=%#v", manifest.Summary, manifest.Files)
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
	if len(records) != 1 || records[0].Type != "project_fact" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected project-facts snapshot records: %#v", records)
	}
	for _, private := range []string{
		"TypeScript", "private-language-marker", "private-framework-marker", "private-root-marker",
		"package.json", "private-validation-command-marker", "private-ui-marker", strings.Repeat("0", 25) + "1",
	} {
		if strings.Contains(string(records[0].Payload), private) {
			t.Fatalf("project facts projection retained source value %q: %s", private, records[0].Payload)
		}
	}
	var projection map[string]any
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["schema_version"] != "threadforge.mancode-project-fact.v1" ||
		projection["authority"] != "local_pending_review" || projection["human_review_required"] != true ||
		projection["execution_authorized"] != false || projection["revision"] != float64(4) ||
		projection["legacy_trust"] != "detected" || projection["project_kind"] != "web" ||
		projection["language_count"] != float64(2) || projection["framework_count"] != float64(1) ||
		projection["source_root_count"] != float64(1) || projection["manifest_count"] != float64(1) ||
		projection["validation_count"] != float64(1) || projection["detected_at"] != "2026-09-20T10:00:00Z" {
		t.Fatalf("project facts projection lost bounded metadata: %s", records[0].Payload)
	}
}

func TestMancodeProjectFactsRejectSchemaDriftAndInvalidAuthorityFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown root field", mutate: func(value map[string]any) { value["projectRoot"] = "C:/private" }},
		{name: "unsupported version", mutate: func(value map[string]any) { value["schemaVersion"] = 2 }},
		{name: "unsupported trust", mutate: func(value map[string]any) { value["trust"] = "approved" }},
		{name: "invalid operation reference", mutate: func(value map[string]any) { value["lastOperationId"] = "private-operation" }},
		{name: "unknown profile field", mutate: func(value map[string]any) {
			value["profile"].(map[string]any)["projectRoot"] = "C:/private"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			value := mancodeProjectFactsFixture()
			tc.mutate(value)
			writeMancodeJSONFixture(t, root, ".mancode/shared/context/project.json", value)
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Summary.RecordsRejected != 1 || manifest.Summary.RecordsImported != 0 {
				t.Fatalf("invalid Mancode project facts were not rejected: %#v", manifest.Summary)
			}
		})
	}
}

func TestMancodeLegacyConfigSnapshotKeepsOnlyKnownPreferences(t *testing.T) {
	root := t.TempDir()
	config := map[string]any{
		"platforms": []any{"codex", "claude-code"}, "forceTeamMode": true, "teamMode": "on",
		"defaultStyle": "private-style-marker API_TOKEN=do-not-copy-config-secret",
		"platformOptions": map[string]any{
			"codex": map[string]any{"minimal": true}, "claude-code": map[string]any{"minimal": false},
		},
	}
	writeMancodeJSONFixture(t, root, ".mancode/config.json", config)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 ||
		len(manifest.Files) != 1 || manifest.Files[0].Status != "ready" {
		t.Fatalf("legacy config did not reconcile: summary=%#v files=%#v", manifest.Summary, manifest.Files)
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
	if len(records) != 1 || records[0].Type != "mancode_legacy_config" || records[0].Scope != "local_pending_review" {
		t.Fatalf("unexpected legacy-config records: %#v", records)
	}
	payload := string(records[0].Payload)
	for _, private := range []string{"private-style-marker", "do-not-copy-config-secret"} {
		if strings.Contains(payload, private) {
			t.Fatalf("legacy config projection retained private value %q: %s", private, payload)
		}
	}
	var projection map[string]any
	if err := json.Unmarshal(records[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	platforms, ok := projection["platforms"].([]any)
	minimalPlatforms, minimalOK := projection["minimal_platforms"].([]any)
	if projection["schema_version"] != "threadforge.mancode-legacy-config.v1" ||
		projection["authority"] != "local_pending_review" || projection["human_review_required"] != true ||
		projection["execution_authorized"] != false || projection["force_team_mode"] != true ||
		projection["team_mode"] != "on" || projection["default_style_configured"] != true ||
		!ok || len(platforms) != 2 || platforms[0] != "claude-code" || platforms[1] != "codex" ||
		!minimalOK || len(minimalPlatforms) != 1 || minimalPlatforms[0] != "codex" {
		t.Fatalf("legacy config projection lost safe settings: %s", payload)
	}
}

func TestMancodeLegacyConfigRejectsUnknownOrMalformedSettings(t *testing.T) {
	base := map[string]any{
		"platforms": []any{"codex"}, "forceTeamMode": false, "teamMode": "auto",
		"defaultStyle": nil, "platformOptions": map[string]any{"codex": map[string]any{"minimal": false}},
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown secret key", mutate: func(value map[string]any) { value["api_key"] = "must-not-be-accepted" }},
		{name: "unsupported platform", mutate: func(value map[string]any) { value["platforms"] = []any{"private-host"} }},
		{name: "invalid team mode", mutate: func(value map[string]any) { value["teamMode"] = "maybe" }},
		{name: "malformed style", mutate: func(value map[string]any) { value["defaultStyle"] = 42 }},
		{name: "unknown platform option", mutate: func(value map[string]any) {
			value["platformOptions"] = map[string]any{"codex": map[string]any{"minimal": true, "path": "C:/private"}}
		}},
		{name: "unknown platform option host", mutate: func(value map[string]any) {
			value["platformOptions"] = map[string]any{"private-host": map[string]any{"minimal": true}}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			config := map[string]any{}
			for key, value := range base {
				config[key] = value
			}
			test.mutate(config)
			writeMancodeJSONFixture(t, root, "config.json", config)
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 0 || manifest.Summary.RecordsRejected != 1 ||
				len(manifest.Files) != 1 || manifest.Files[0].Status != "unsupported" {
				t.Fatalf("invalid legacy config was not rejected: summary=%#v files=%#v", manifest.Summary, manifest.Files)
			}
		})
	}
}

func mancodeCoordinationFixtureIDs() map[string]string {
	makeID := func(last string) string { return strings.Repeat("0", 25) + last }
	return map[string]string{
		"claim": makeID("1"), "handoff": makeID("2"), "task": makeID("3"), "workspace": makeID("4"),
		"domain": makeID("5"), "fromActor": makeID("6"), "toActor": makeID("7"), "checkpoint": makeID("8"),
		"operation": makeID("9"),
	}
}

func mancodeCoordinationClaimFixture(ids map[string]string) map[string]any {
	return map[string]any{
		"schemaVersion": 1, "claimId": ids["claim"], "workspaceId": ids["workspace"],
		"coordinationDomainId": ids["domain"], "authority": map[string]any{"mode": "local", "remoteRevision": nil},
		"taskRef":               map[string]any{"namespace": "shared", "taskId": ids["task"]},
		"taskRevisionAtAcquire": 3, "lastValidatedTaskRevision": 4,
		"implementationScopeDigest": strings.Repeat("a", 64), "ownershipEpochAtAcquire": 2,
		"ownerActorId": ids["fromActor"], "state": "active", "revision": 4,
		"scope": map[string]any{
			"paths": []any{"internal/private.go"}, "modules": []any{"private-module"},
			"apis": []any{}, "schemas": []any{"private-schema"},
		},
		"scopeDigest":            strings.Repeat("b", 64),
		"codeRefAtAcquire":       map[string]any{"branch": "private-branch-name", "head": strings.Repeat("c", 40), "base": strings.Repeat("d", 40)},
		"lastValidatedCodeRef":   map[string]any{"branch": "private-branch-name", "head": strings.Repeat("c", 40), "base": strings.Repeat("d", 40)},
		"acquisitionEnforcement": map[string]any{"mode": "strict", "authorization": "private-proof"},
		"writeGuard":             map[string]any{"checkoutId": ids["workspace"], "lock": "private-lock"},
		"expiresAt":              "2026-10-21T10:00:00Z", "predecessorClaimId": nil, "successorClaimId": nil,
		"lastOperationId": ids["operation"], "createdAt": "2026-10-18T10:00:00Z", "updatedAt": "2026-10-18T10:01:00Z",
	}
}

func mancodeCoordinationHandoffFixture(ids map[string]string) map[string]any {
	return map[string]any{
		"schemaVersion": 1, "handoffId": ids["handoff"],
		"taskRef":      map[string]any{"namespace": "shared", "taskId": ids["task"]},
		"taskRevision": 3, "ownershipEpochAtOffer": 2, "state": "offered", "revision": 1,
		"fromActorId": ids["fromActor"], "toActorId": ids["toActor"], "claimIds": []any{ids["claim"]},
		"checkpointRef": map[string]any{
			"taskRef": map[string]any{"namespace": "shared", "taskId": ids["task"]},
			"kind":    "handoff", "artifactId": ids["checkpoint"],
		},
		"summary": map[string]any{
			"completed": []any{"private completed detail"}, "inProgress": []any{}, "notStarted": []any{"private next task"},
			"changedFiles": []any{"internal/private.go"}, "verification": "private test command and output",
			"blockers": []any{"private blocker"}, "risks": []any{"private risk"}, "nextAction": "private next action",
		},
		"transport": map[string]any{
			"mode": "git-ref", "state": "published", "transportRevision": 5,
			"publishedAt": "2026-10-18T10:01:00Z", "fetchedAt": nil,
			"taskBundleDigest": strings.Repeat("e", 64),
			"codeRef":          map[string]any{"branch": "private-branch-name", "head": strings.Repeat("c", 40)},
			"codeReachable":    true, "receipt": map[string]any{"sessionId": "private-transport-session", "proof": "private-receipt"},
		},
		"lastOperationId": ids["operation"], "offeredAt": "2026-10-18T10:00:00Z",
		"resolution": nil, "createdAt": "2026-10-18T10:00:00Z", "updatedAt": "2026-10-18T10:01:00Z",
	}
}

func mancodeCoordinationTaskHeadFixture(ids map[string]string) map[string]any {
	return map[string]any{
		"schemaVersion": 1, "workspaceId": ids["workspace"],
		"taskRef":       map[string]any{"namespace": "shared", "taskId": ids["task"]},
		"fenceRevision": 6, "taskRevision": 3, "aggregateDigest": strings.Repeat("f", 64),
		"ownershipEpoch": 2, "codeRef": map[string]any{"head": strings.Repeat("c", 40)},
		"checkoutId": ids["workspace"], "remoteRevision": "private-remote-revision",
		"lastOperationId": ids["operation"], "updatedAt": "2026-10-18T10:02:00Z",
	}
}

func TestMancodeCoordinationRootImportsInertRecordsAndSanitizedOperationAudit(t *testing.T) {
	ids := mancodeCoordinationFixtureIDs()
	root := filepath.Join(t.TempDir(), ".git", "mancode", "workspaces", ids["workspace"])
	writeMancodeJSONFixture(t, root, "claims/"+ids["claim"]+".json", mancodeCoordinationClaimFixture(ids))
	writeMancodeJSONFixture(t, root, "handoffs/"+ids["handoff"]+".json", mancodeCoordinationHandoffFixture(ids))
	writeMancodeJSONFixture(t, root, "task-heads/"+ids["task"]+".json", mancodeCoordinationTaskHeadFixture(ids))
	writeMancodeJSONFixture(t, root, "operations/"+ids["operation"]+".json", map[string]any{
		"schemaVersion": 1, "operationId": ids["operation"], "type": "claim_create", "state": "repair_required",
		"startedAt": "2026-10-18T10:00:00Z", "updatedAt": "2026-10-18T10:01:00Z",
		"sessionId": "source-session-must-not-migrate", "authorizationBasis": map[string]any{"approval": "source-only"},
		"recoveryPayload": map[string]any{"command": "must-not-be-copied", "api_key": "sensitive"},
	})
	writeMancodeJSONFixture(t, root, "operations/not-a-ulid.json", map[string]any{
		"schemaVersion": 1, "operationId": ids["operation"], "type": "claim_create", "state": "committed",
		"startedAt": "2026-10-18T10:00:00Z", "updatedAt": "2026-10-18T10:01:00Z",
	})

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 5 || manifest.Summary.RecordsImported != 4 || manifest.Summary.RecordsRejected != 1 {
		t.Fatalf("Mancode coordination records did not reconcile: %#v", manifest.Summary)
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
	var operation Record
	forbidden := append([]string{}, ids["claim"], ids["handoff"], ids["task"], ids["workspace"], ids["domain"], ids["fromActor"], ids["toActor"], ids["checkpoint"], ids["operation"])
	forbidden = append(forbidden, "private-branch-name", "internal/private.go", "private completed detail", "private next task",
		"private test command and output", "private blocker", "private risk", "private next action", "private-transport-session",
		"private-receipt", "private-remote-revision", "source-session-must-not-migrate", "authorizationBasis", "recoveryPayload", "must-not-be-copied", "api_key")
	seenTypes := map[string]bool{}
	for _, record := range records {
		if record.Type == "mancode_operation_audit" {
			operation = record
		}
		seenTypes[record.Type] = true
		if record.Scope != "local_pending_review" {
			t.Fatalf("Mancode coordination record became live authority: %#v", record)
		}
		if !strings.HasPrefix(record.SourcePath, "mancode/coordination/") || strings.Contains(record.Key, ids["workspace"]) {
			t.Fatalf("coordination record key/path retained source identity: %#v", record)
		}
		encoded := string(record.Payload)
		for _, value := range forbidden {
			if strings.Contains(encoded, value) {
				t.Fatalf("%s projection retained omitted coordination data %q: %s", record.Type, value, encoded)
			}
		}
	}
	for _, recordType := range []string{"mancode_team_claim", "mancode_team_handoff", "mancode_team_head_fence", "mancode_operation_audit"} {
		if !seenTypes[recordType] {
			t.Fatalf("typed coordination projection %q was not imported: %#v", recordType, records)
		}
	}
	if operation.Key == "" || !strings.Contains(string(operation.Payload), `"requires_reconciliation":true`) ||
		!strings.Contains(string(operation.Payload), `"raw_source_identity_omitted":true`) {
		t.Fatalf("incomplete operation was not represented as requiring reconciliation: %s", operation.Payload)
	}
}

func TestMancodeCoordinationProjectionRejectsUnknownFieldsAndPathMismatches(t *testing.T) {
	ids := mancodeCoordinationFixtureIDs()
	root := filepath.Join(t.TempDir(), ".git", "mancode", "workspaces", ids["workspace"])
	claim := mancodeCoordinationClaimFixture(ids)
	claim["unexpectedAuthorityPayload"] = map[string]any{"secret": "must never fall through to generic storage"}
	writeMancodeJSONFixture(t, root, "claims/"+ids["claim"]+".json", claim)
	handoff := mancodeCoordinationHandoffFixture(ids)
	handoff["handoffId"] = ids["claim"]
	writeMancodeJSONFixture(t, root, "handoffs/"+ids["handoff"]+".json", handoff)
	wrongWorkspaceClaim := mancodeCoordinationClaimFixture(ids)
	wrongWorkspaceClaim["claimId"] = ids["operation"]
	wrongWorkspaceClaim["workspaceId"] = ids["operation"]
	writeMancodeJSONFixture(t, root, "claims/"+ids["operation"]+".json", wrongWorkspaceClaim)

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 3 || manifest.Summary.RecordsImported != 0 || manifest.Summary.RecordsRejected != 3 {
		t.Fatalf("malformed coordination records were not rejected fail-closed: %#v", manifest.Summary)
	}
	for _, file := range manifest.Files {
		if file.Status != "unsupported" {
			t.Fatalf("malformed coordination source was accepted: %#v", file)
		}
	}
}

func TestMancodeDraftHandoffProjectionDoesNotInventOfferTime(t *testing.T) {
	ids := mancodeCoordinationFixtureIDs()
	root := filepath.Join(t.TempDir(), ".git", "mancode", "workspaces", ids["workspace"])
	handoff := mancodeCoordinationHandoffFixture(ids)
	handoff["state"] = "draft"
	handoff["offeredAt"] = nil
	item, err := normalizeMancodeCoordinationRecord(root, "handoffs/"+ids["handoff"]+".json", "mancode_team_handoff", handoff)
	if err != nil {
		t.Fatalf("draft handoff without an offer timestamp was rejected: %v", err)
	}
	var projected map[string]any
	if err := json.Unmarshal(item.Payload, &projected); err != nil {
		t.Fatal(err)
	}
	if offeredAt, exists := projected["offered_at"]; !exists || offeredAt != nil {
		t.Fatalf("draft handoff was assigned an offer timestamp: %#v", projected)
	}
}

func TestMancodeImporterAcceptsMancodeDirectoryAsSourceRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".mancode")
	writeMancodeJSONFixture(t, root, "shared/context/project.json", mancodeProjectFactsFixture())
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsImported != 1 || manifest.Files[0].Path != "shared/context/project.json" || manifest.Files[0].Status != "ready" {
		t.Fatalf("Mancode-root inspection did not include project facts: %#v", manifest)
	}
}
