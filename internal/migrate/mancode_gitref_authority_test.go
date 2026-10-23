package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMancodeGitRefFreezeAndTombstoneImportIdentityReducedTransitions(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		state       string
		transitions []string
	}{
		{name: "frozen", state: "frozen", transitions: []string{"freeze"}},
		{name: "tombstoned", state: "tombstoned", transitions: []string{"freeze", "tombstone"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root, workspaceID, operationID := mancodeGitRefAuthorityTransitionFixture(t, testCase.state)
			dataRoot := t.TempDir()
			snapshot, err := CreateSnapshotWithOptions(Mancode, root, dataRoot, InspectOptions{IncludeMancodeGitRef: true})
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Summary.RecordsRejected != 0 {
				t.Fatalf("valid %s authority transition was rejected: %#v", testCase.state, snapshot.Summary)
			}
			coverage, err := BuildFormatCoverage(snapshot)
			if err != nil || coverage.RejectedRecords != 0 || coverage.UnclassifiedFiles != 0 {
				t.Fatalf("%s authority transition did not reconcile in format coverage: coverage=%#v err=%v", testCase.state, coverage, err)
			}
			manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
			records, err := ReadSnapshotRecords(manifestPath, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var transitions []map[string]any
			for _, record := range records {
				if record.SourcePath != mancodeGitRefVirtualPath || record.Type != mancodeGitRefAuthorityTransitionType {
					continue
				}
				if record.Scope != "local_pending_review" {
					t.Fatalf("authority transition escaped local review: %#v", record)
				}
				var transition map[string]any
				if err := json.Unmarshal(record.Payload, &transition); err != nil {
					t.Fatal(err)
				}
				if transition["execution_authorized"] != false || transition["human_review_required"] != true ||
					transition["operation_reference_omitted"] != true || transition["workspace_identity_omitted"] != true {
					t.Fatalf("transition projection lost its non-authorizing identity boundary: %#v", transition)
				}
				for _, sourceID := range []string{operationID, workspaceID} {
					if strings.Contains(string(record.Payload), sourceID) {
						t.Fatalf("authority transition retained source identity %q: %s", sourceID, record.Payload)
					}
				}
				transitions = append(transitions, transition)
			}
			if len(transitions) != len(testCase.transitions) {
				t.Fatalf("expected %d reduced transition records, got %#v", len(testCase.transitions), transitions)
			}
			for i, want := range testCase.transitions {
				if transitions[i]["transition"] != want || transitions[i]["successor_epoch"] != float64(2) ||
					transitions[i]["transport_epoch"] != float64(1) || transitions[i]["epoch_advance"] != float64(1) {
					t.Fatalf("transition %d does not match its validated authority detail: %#v", i, transitions[i])
				}
			}
			data, err := os.ReadFile(filepath.Join("..", "..", "schema", "mancode-git-ref-authority-transition-v1.schema.json"))
			if err != nil || !json.Valid(data) {
				t.Fatalf("authority transition schema is missing or invalid JSON: %v", err)
			}
		})
	}
}

func mancodeGitRefAuthorityTransitionFixture(t *testing.T, state string) (string, string, string) {
	t.Helper()
	root, _, baseBytes := mancodeGitRefFixture(t, "git-ref", false)
	value, err := decodeStrictMancodeGitRefJSON(baseBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifest := value.(map[string]any)
	workspaceID := mancodeString(manifest["workspaceId"])
	operationID := mancodeDecisionTestID("7")
	actorID := mancodeDecisionTestID("2")
	freezeTime := "2026-10-08T13:00:00Z"
	tombstoneTime := "2026-10-08T14:00:00Z"
	freeze := map[string]any{
		"schemaVersion": json.Number("1"), "successorMode": "local", "successorEpoch": json.Number("2"),
		"operationId": operationID, "frozenAt": freezeTime,
	}
	manifest["authorityFreeze"] = freeze
	manifest["authorityState"] = state
	manifest["lastOperationId"] = operationID
	manifest["updatedAt"] = freezeTime
	freezeReceipt := mancodeGitRefAuthorityReceiptFixture(t, manifest, "authority_freeze", operationID, actorID, 2, freezeTime)
	receipts := append(append([]any(nil), manifest["receipts"].([]any)...), freezeReceipt)
	lastMutation := freezeReceipt
	if state == "frozen" {
		manifest["revision"] = json.Number("2")
	} else {
		manifest["authorityTombstone"] = map[string]any{
			"schemaVersion": json.Number("1"), "successorMode": "local", "successorEpoch": json.Number("2"),
			"operationId": operationID, "tombstonedAt": tombstoneTime,
		}
		manifest["revision"] = json.Number("3")
		manifest["updatedAt"] = tombstoneTime
		tombstoneReceipt := mancodeGitRefAuthorityReceiptFixture(t, manifest, "authority_tombstone", operationID, actorID, 3, tombstoneTime)
		receipts = append(receipts, tombstoneReceipt)
		lastMutation = tombstoneReceipt
	}
	manifest["receipts"] = receipts
	manifest["lastMutation"] = lastMutation
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeMancodeGitRefCommit(t, root, manifestBytes)
	return root, workspaceID, operationID
}

func mancodeGitRefAuthorityReceiptFixture(t *testing.T, manifest map[string]any, kind, operationID, actorID string, remoteRevision int64, committedAt string) map[string]any {
	t.Helper()
	actorDigest, err := mancodeCanonicalDigest(manifest["actorProfiles"])
	if err != nil {
		t.Fatal(err)
	}
	emptyDigest, err := mancodeCanonicalDigest([]any{})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"schemaVersion": json.Number("1"), "kind": kind, "operationId": operationID, "actorId": actorID,
		"taskRef": nil, "remoteRevision": json.Number(strconv.FormatInt(remoteRevision, 10)), "ownershipEpoch": nil,
		"entityDigests": map[string]any{
			"actorProfiles": actorDigest, "ownershipFence": emptyDigest, "claims": emptyDigest,
			"handoffs": emptyDigest, "taskBundle": emptyDigest,
		},
		"committedAt": committedAt,
	}
}
