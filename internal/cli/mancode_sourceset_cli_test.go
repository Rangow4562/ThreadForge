package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/migrationreview"
	"github.com/threadforge-ai/threadforge/pkg/migrationsources"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

func TestMancodeSourceSetCLIRequiresAndEmitsCompleteDiscoveredInventory(t *testing.T) {
	const workspaceID = "00000000000000000000000001"
	const operationID = "00000000000000000000000003"
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "project")
	coordinationRoot := filepath.Join(projectRoot, ".git", "mancode", "workspaces", workspaceID)
	operationPath := filepath.Join(coordinationRoot, "operations", operationID+".json")
	if err := os.MkdirAll(filepath.Dir(operationPath), 0o700); err != nil {
		t.Fatal(err)
	}
	operation := `{"schemaVersion":1,"operationId":"` + operationID + `","type":"claim_create","state":"repair_required","startedAt":"2026-10-18T10:00:00Z","updatedAt":"2026-10-18T10:01:00Z","sessionId":"source-session-private"}`
	if err := os.WriteFile(operationPath, []byte(operation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(parent, "threadforge-home")
	t.Setenv("THREADFORGE_HOME", home)
	run := func(wantCode int, args ...string) ([]byte, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run(args, &stdout, &stderr)
		if code != wantCode {
			t.Fatalf("threadforge %s returned %d, want %d: stdout=%q stderr=%q", strings.Join(args, " "), code, wantCode, stdout.String(), stderr.String())
		}
		return append([]byte(nil), stdout.Bytes()...), stderr.String()
	}

	localRootsJSON, _ := run(0, "migrate", "sources", "--root", projectRoot)
	var localRoots struct {
		Sources []struct {
			Role       string `json:"role"`
			SourceRoot string `json:"source_root"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(localRootsJSON, &localRoots); err != nil {
		t.Fatal(err)
	}
	if len(localRoots.Sources) != 2 {
		t.Fatalf("operator inventory omitted the coordination workspace: %s", localRootsJSON)
	}

	manifestPaths := make(map[string]string)
	for _, root := range []string{projectRoot, coordinationRoot} {
		snapshotJSON, _ := run(0, "migrate", "snapshot", "--product", "mancode", "--root", root)
		var snapshot struct {
			SourceFingerprint string `json:"source_fingerprint"`
		}
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		manifestPaths[root] = filepath.Join(home, "migrations", "snapshots", "mancode", snapshot.SourceFingerprint, "manifest.json")
	}

	args := []string{"migrate", "source-set", "--root", projectRoot,
		"--manifest", manifestPaths[coordinationRoot], "--manifest", manifestPaths[projectRoot]}
	reportJSON, _ := run(0, args...)
	var report migrationsources.MancodeReport
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		t.Fatal(err)
	}
	if err := migrationsources.ValidateMancode(report); err != nil {
		t.Fatalf("CLI emitted an invalid source-set report: %v", err)
	}
	if len(report.Components) != 2 || report.DuplicateRecordKeys != 0 {
		t.Fatalf("source-set report did not reconcile all component roots: %#v", report)
	}
	for _, privateValue := range []string{projectRoot, coordinationRoot, workspaceID, operationID, "source-session-private"} {
		if strings.Contains(string(reportJSON), privateValue) {
			t.Fatalf("shareable CLI inventory retained private value %q: %s", privateValue, reportJSON)
		}
	}

	missingArgs := []string{"migrate", "source-set", "--root", projectRoot, "--manifest", manifestPaths[projectRoot]}
	missingJSON, missingErr := run(1, missingArgs...)
	if len(missingJSON) != 0 || !strings.Contains(missingErr, "one snapshot for each of 2 discovered source roots") {
		t.Fatalf("source-set CLI did not fail visibly when a discovered root was omitted: stdout=%q stderr=%q", missingJSON, missingErr)
	}

	sourceSetPath := filepath.Join(parent, "source-set.json")
	if err := os.WriteFile(sourceSetPath, reportJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	evidenceRoot := t.TempDir()
	identity, public, err := teamauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	identityDir := t.TempDir()
	identityPath := filepath.Join(identityDir, "source-authority-private.json")
	publicPath := filepath.Join(identityDir, "source-authority-public.json")
	if err := teamauth.SaveIdentity(identityPath, publicPath, identity, public); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	trust := migrationreview.TrustStore{SchemaVersion: migrationreview.TrustStoreSchemaVersion, Keys: []migrationreview.ReviewerKey{{
		KeyID: identity.KeyID, PublicKey: public.PublicKey, Reviewer: "source-authority-reviewer", Products: []string{"mancode"},
		Purposes:  []string{migrationreview.PurposeSourceAuthority},
		NotBefore: now.Add(-time.Hour).Format(time.RFC3339Nano), NotAfter: now.Add(time.Hour).Format(time.RFC3339Nano),
	}}}
	trustPath := filepath.Join(identityDir, "source-authority-trust.json")
	trustBytes, err := json.Marshal(trust)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trustPath, trustBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	attestationArgs := []string{"migrate", "attest-source-set", "--root", projectRoot,
		"--manifest", manifestPaths[coordinationRoot], "--manifest", manifestPaths[projectRoot],
		"--source-set", sourceSetPath, "--evidence-root", evidenceRoot, "--attestation-path", "mancode-source-authority.json",
		"--identity-key", identityPath, "--trusted-reviewers", trustPath}
	missingConfirmation, missingConfirmationErr := run(2, attestationArgs...)
	if len(missingConfirmation) != 0 || !strings.Contains(missingConfirmationErr, "confirm-authoritative-root-inventory") {
		t.Fatalf("authority assertion did not require explicit confirmation: stdout=%q stderr=%q", missingConfirmation, missingConfirmationErr)
	}
	attestationArgs = append(attestationArgs, "--confirm-authoritative-root-inventory")
	resultBytes, _ := run(0, attestationArgs...)
	var result mancodeSourceAuthorityResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != "threadforge.mancode-source-authority-result.v1" || result.Product != "mancode" ||
		result.ReviewerID != "source-authority-reviewer" || result.KeyID != identity.KeyID || !result.SourceSetRecomputed ||
		!result.TrustedSignatureVerified || !result.ReviewerAssertionSigned || !result.HumanReviewRequired ||
		result.ExecutionAuthorized || result.CutoverReady || result.ComponentCount != 2 {
		t.Fatalf("authority result overstated or omitted review safeguards: %#v", result)
	}
	attestationBytes, err := os.ReadFile(filepath.Join(evidenceRoot, "mancode-source-authority.json"))
	if err != nil {
		t.Fatal(err)
	}
	var attestation migrationreview.SourceAuthorityAttestation
	if err := json.Unmarshal(attestationBytes, &attestation); err != nil {
		t.Fatal(err)
	}
	binding, err := migrationreview.SourceAuthorityBindingForArtifact(reportJSON, report.InventorySHA256,
		report.ProjectSourceFingerprint, report.ProjectSnapshotSHA256, len(report.Components))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationreview.VerifySourceAuthority(attestation, trust, binding); err != nil {
		t.Fatalf("CLI emitted invalid source-authority signature: %v", err)
	}
	for _, privateValue := range []string{projectRoot, coordinationRoot, workspaceID, operationID, "source-session-private"} {
		if strings.Contains(string(resultBytes), privateValue) || strings.Contains(string(attestationBytes), privateValue) {
			t.Fatalf("source-authority evidence exposed local/private value %q", privateValue)
		}
	}

	trust.Keys[0].Purposes = []string{migrationreview.PurposePlanReview}
	trustBytes, err = json.Marshal(trust)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trustPath, trustBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	wrongPurposeArgs := append([]string(nil), attestationArgs...)
	wrongPurposeArgs[13] = "mancode-authority-wrong-purpose.json"
	wrongPurposeJSON, wrongPurposeErr := run(1, wrongPurposeArgs...)
	if len(wrongPurposeJSON) != 0 || !strings.Contains(wrongPurposeErr, "source-authority purpose") {
		t.Fatalf("plan-review-only key was allowed to attest source authority: stdout=%q stderr=%q", wrongPurposeJSON, wrongPurposeErr)
	}
	if err := os.WriteFile(operationPath, []byte(operation+"\n \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleArgs := append([]string(nil), attestationArgs...)
	staleArgs[13] = "mancode-source-authority-stale.json"
	staleJSON, staleErr := run(1, staleArgs...)
	if len(staleJSON) != 0 || !strings.Contains(staleErr, "source changed after its component snapshot") {
		t.Fatalf("stale source root was eligible for authority attestation: stdout=%q stderr=%q", staleJSON, staleErr)
	}
}
