package appdata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

func writePinnedTestPolicy(t *testing.T, root string, policy teamauth.Policy) []byte {
	t.Helper()
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	policyPath := TeamTrustPolicyPath(root)
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if err := os.WriteFile(TeamTrustPolicyDigestPath(root), []byte(hex.EncodeToString(digest[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReadTeamTrustPolicyRequiresAndChecksPersistedPin(t *testing.T) {
	identity, public, err := teamauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	_ = identity
	policy := teamauth.Policy{
		SchemaVersion: teamauth.PolicySchemaVersion, OrganizationID: "test-org",
		Members: []teamauth.Member{{
			Subject: "reviewer", KeyID: public.KeyID, PublicKey: public.PublicKey,
			Projects: []string{"demo"}, Roles: []string{teamauth.RoleProcedurePublish},
		}},
	}
	root := t.TempDir()
	policyData := writePinnedTestPolicy(t, root, policy)
	loaded, loadedBytes, digest, err := ReadTeamTrustPolicy(root)
	if err != nil || loaded.SchemaVersion != teamauth.PolicySchemaVersion || string(loadedBytes) != string(policyData) {
		t.Fatalf("pinned policy read = %#v, digest=%q, error=%v", loaded, digest, err)
	}
	sum := sha256.Sum256(policyData)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("policy digest = %q, want %x", digest, sum)
	}

	modified := policy
	modified.Members = append([]teamauth.Member(nil), policy.Members...)
	modified.Members[0].Subject = "replacement"
	modifiedBytes, err := json.MarshalIndent(modified, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(TeamTrustPolicyPath(root), append(modifiedBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ReadTeamTrustPolicy(root); err == nil || !strings.Contains(err.Error(), "pinned digest") {
		t.Fatalf("modified installed policy was accepted: %v", err)
	}

	if err := os.WriteFile(TeamTrustPolicyPath(root), policyData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(TeamTrustPolicyDigestPath(root), []byte(strings.Repeat("0", 64)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ReadTeamTrustPolicy(root); err == nil || !strings.Contains(err.Error(), "pinned digest") {
		t.Fatalf("modified installed digest was accepted: %v", err)
	}
}

func TestReadTeamTrustPolicyDistinguishesMissingPolicyFromMissingPin(t *testing.T) {
	root := t.TempDir()
	if _, _, _, err := ReadTeamTrustPolicy(root); err != ErrTeamTrustPolicyNotInstalled {
		t.Fatalf("missing policy error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(TeamTrustPolicyDigestPath(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(TeamTrustPolicyDigestPath(root), []byte(strings.Repeat("0", 64)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ReadTeamTrustPolicy(root); err == nil || err == ErrTeamTrustPolicyNotInstalled {
		t.Fatalf("missing policy with retained pin was treated as local-only: %v", err)
	}
	if err := os.Remove(TeamTrustPolicyDigestPath(root)); err != nil {
		t.Fatal(err)
	}
	identity, public, err := teamauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	_ = identity
	policy := teamauth.Policy{
		SchemaVersion: teamauth.PolicySchemaVersion, OrganizationID: "test-org",
		Members: []teamauth.Member{{Subject: "reviewer", KeyID: public.KeyID, PublicKey: public.PublicKey,
			Projects: []string{"demo"}, Roles: []string{teamauth.RoleProcedurePublish}}},
	}
	policyData, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	policyPath := TeamTrustPolicyPath(root)
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, append(policyData, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ReadTeamTrustPolicy(root); err == nil || err == ErrTeamTrustPolicyNotInstalled {
		t.Fatalf("policy with missing pin was not rejected distinctly: %v", err)
	}
}
