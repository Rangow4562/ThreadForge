package teamauth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testIdentityAndPolicy(t *testing.T, roles ...string) (Identity, Policy) {
	t.Helper()
	identity, public, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	return identity, Policy{
		SchemaVersion:  PolicySchemaVersion,
		OrganizationID: "example-org",
		Members: []Member{{
			Subject: "reviewer-1", KeyID: public.KeyID, PublicKey: public.PublicKey,
			Projects: []string{"demo-project"}, Roles: roles,
		}},
	}
}

func validTestClaims() Claims {
	return Claims{
		Action: "publish", ProjectID: "demo-project", ProcedureID: "verify-release",
		Revision: 7, GraphSHA256: strings.Repeat("a", 64), PrivacyReviewed: true,
	}
}

func completeRoles() []string {
	return []string{RoleProcedureApprove, RoleProcedurePublish, RolePrivacyReview, RoleProcedureRetire}
}

func TestSignedAttestationBindsIdentityActionProjectRevisionAndDigest(t *testing.T) {
	identity, policy := testIdentityAndPolicy(t, completeRoles()...)
	claims := validTestClaims()
	attestation, err := Sign(identity, "reviewer-1", claims, time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(attestation, policy, claims); err != nil {
		t.Fatalf("valid attestation rejected: %v", err)
	}

	for name, changed := range map[string]Claims{
		"other project":          func() Claims { c := claims; c.ProjectID = "other-project"; return c }(),
		"other procedure":        func() Claims { c := claims; c.ProcedureID = "other-procedure"; return c }(),
		"other revision":         func() Claims { c := claims; c.Revision++; return c }(),
		"other content":          func() Claims { c := claims; c.GraphSHA256 = strings.Repeat("b", 64); return c }(),
		"privacy review removed": func() Claims { c := claims; c.PrivacyReviewed = false; return c }(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := Verify(attestation, policy, changed); err == nil {
				t.Fatal("attestation was accepted for altered claims")
			}
		})
	}

	tampered := attestation
	tampered.Signature = base64.StdEncoding.EncodeToString(make([]byte, 64))
	if err := Verify(tampered, policy, claims); err == nil {
		t.Fatal("tampered signature was accepted")
	}
	tampered = attestation
	tampered.Subject = "different-reviewer"
	if err := Verify(tampered, policy, claims); err == nil {
		t.Fatal("attestation with a substituted reviewer was accepted")
	}
}

func TestTrustPolicyEnforcesProjectAndRoleGrants(t *testing.T) {
	identity, policy := testIdentityAndPolicy(t, RoleProcedurePublish, RoleProcedureApprove)
	claims := validTestClaims()
	attestation, err := Sign(identity, "reviewer-1", claims, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(attestation, policy, claims); err == nil || !strings.Contains(err.Error(), RolePrivacyReview) {
		t.Fatalf("missing privacy-review role error = %v", err)
	}
	policy.Members[0].Roles = append(policy.Members[0].Roles, RolePrivacyReview)
	policy.Members[0].Projects = []string{"different-project"}
	if err := Verify(attestation, policy, claims); err == nil {
		t.Fatal("signer authorized for another project was accepted")
	}
}

func TestPolicyRejectsUnboundKeysDuplicateKeysAndUnknownRoles(t *testing.T) {
	_, policy := testIdentityAndPolicy(t, completeRoles()...)
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := policy
	bad.Members = append(bad.Members, policy.Members[0])
	if err := bad.Validate(); err == nil {
		t.Fatal("duplicate key was accepted")
	}
	bad = policy
	bad.Members = append([]Member(nil), policy.Members...)
	bad.Members[0].KeyID = strings.Repeat("0", 64)
	if err := bad.Validate(); err == nil {
		t.Fatal("key identifier unrelated to the public key was accepted")
	}
	bad = policy
	bad.Members = append([]Member(nil), policy.Members...)
	bad.Members[0].Roles = []string{"owner"}
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown authorization role was accepted")
	}
}

func TestTrustPolicyV2AddsContinuityRolesWithoutChangingV1Contract(t *testing.T) {
	_, policy := testIdentityAndPolicy(t, RoleContinuityWrite)
	if err := policy.Validate(); err != nil {
		t.Fatalf("v2 policy with continuity role rejected: %v", err)
	}
	policy.SchemaVersion = PolicySchemaVersionV1
	if err := policy.Validate(); err == nil {
		t.Fatal("v1 policy accepted a role added in v2")
	}
	policy.Members[0].Roles = []string{RoleProcedurePublish}
	if err := policy.Validate(); err != nil {
		t.Fatalf("v1 procedure-only policy stopped validating: %v", err)
	}
}

func TestAuthorizeIdentityRequiresTrustedKeyProjectAndEveryRole(t *testing.T) {
	identity, policy := testIdentityAndPolicy(t, RoleContinuityWrite, RoleTaskPlanConfirm)
	member, err := policy.AuthorizeIdentity(identity, "demo-project", RoleContinuityWrite)
	if err != nil || member.Subject != "reviewer-1" {
		t.Fatalf("authorized member = %#v, error = %v", member, err)
	}
	if _, err := policy.AuthorizeIdentity(identity, "demo-project", RoleContinuityWrite, RoleTaskPlanConfirm); err != nil {
		t.Fatalf("member with both roles rejected: %v", err)
	}
	if _, err := policy.AuthorizeIdentity(identity, "demo-project", RoleContinuityWrite, RoleRequirementsFinalize); err == nil {
		t.Fatal("member missing an elevated role was authorized")
	}
	if _, err := policy.AuthorizeIdentity(identity, "other-project", RoleContinuityWrite); err == nil {
		t.Fatal("identity granted access to another project")
	}
	other, _, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.AuthorizeIdentity(other, "demo-project", RoleContinuityWrite); err == nil {
		t.Fatal("untrusted key was authorized")
	}
}

func TestPrivateIdentityPersistenceIsExclusiveAndRoundTrips(t *testing.T) {
	identity, public, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	privatePath := filepath.Join(root, "identity.json")
	publicPath := filepath.Join(root, "identity.pub.json")
	if err := SaveIdentity(privatePath, publicPath, identity, public); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIdentity(privatePath)
	if err != nil || loaded.KeyID != identity.KeyID {
		t.Fatalf("loaded identity = %#v, error = %v", loaded, err)
	}
	privateInfo, err := os.Stat(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && privateInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private key permissions are too broad: %o", privateInfo.Mode().Perm())
	}
	privateBytes, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicBytes), base64.StdEncoding.EncodeToString(identity.PrivateKey)) {
		t.Fatal("public identity file contains private key bytes")
	}
	if err := SaveIdentity(privatePath, filepath.Join(root, "another.pub.json"), identity, public); err == nil {
		t.Fatal("existing private-key path was overwritten")
	}
	var stored IdentityFile
	if err := json.Unmarshal(privateBytes, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.PrivateKey == "" {
		t.Fatal("private identity record unexpectedly omitted its key")
	}
}

func TestLoadPolicyRejectsUnknownFieldsAndSymlinks(t *testing.T) {
	_, policy := testIdentityAndPolicy(t, completeRoles()...)
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	filename := filepath.Join(root, "policy.json")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(filename); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	unknown := strings.TrimSuffix(string(data), "}") + `,"unexpected":true}`
	if err := os.WriteFile(filename, []byte(unknown), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(filename); err == nil {
		t.Fatal("policy with an unknown field was accepted")
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "policy-link.json")
	if err := os.Symlink(filepath.Join(root, "missing.json"), link); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}
	if _, err := LoadPolicy(link); err == nil {
		t.Fatal("symlinked trust policy was accepted")
	}
}
