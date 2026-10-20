package teamauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMutationAttestationBindsProjectOperationRevisionPayloadAndPolicy(t *testing.T) {
	identity, policy := testIdentityAndPolicy(t, RoleContinuityWrite)
	policyBytes, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	policyDigestBytes := sha256.Sum256(policyBytes)
	policyDigest := hex.EncodeToString(policyDigestBytes[:])
	entity := map[string]any{"status": "completed", "summary": "verified"}
	payloadDigest, err := MutationPayloadSHA256(entity, "reviewed evidence")
	if err != nil {
		t.Fatal(err)
	}
	claims := MutationClaims{
		Action: "task.update", AuditAction: "updated", ProjectID: "demo-project",
		ResourceType: "task", ResourceID: "task-1", ExpectedRevision: 4, PayloadSHA256: payloadDigest,
	}
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	attestation, err := SignMutation(identity, "reviewer-1", policyDigest, claims, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMutation(attestation, policy, policyDigest, claims, now); err != nil {
		t.Fatalf("valid mutation attestation rejected: %v", err)
	}

	changed := claims
	changed.ProjectID = "other-project"
	if err := VerifyMutation(attestation, policy, policyDigest, changed, now); err == nil {
		t.Fatal("attestation was accepted for a different project")
	}
	changed = claims
	changed.ExpectedRevision++
	if err := VerifyMutation(attestation, policy, policyDigest, changed, now); err == nil {
		t.Fatal("attestation was accepted for a different revision")
	}
	changed = claims
	changed.PayloadSHA256 = strings.Repeat("b", 64)
	if err := VerifyMutation(attestation, policy, policyDigest, changed, now); err == nil {
		t.Fatal("attestation was accepted for different mutation content")
	}
	if err := VerifyMutation(attestation, policy, strings.Repeat("c", 64), claims, now); err == nil {
		t.Fatal("attestation was accepted under another policy digest")
	}

	tampered := attestation
	tampered.Signature = strings.Repeat("A", 88)
	if err := VerifyMutation(tampered, policy, policyDigest, claims, now); err == nil {
		t.Fatal("tampered mutation signature was accepted")
	}
	if err := VerifyMutation(attestation, policy, policyDigest, claims, now.Add(MutationMaxAge+time.Second)); err == nil {
		t.Fatal("expired mutation attestation was accepted")
	}
}

func TestMutationAttestationRequiresGrantedElevatedRoleAndValidScope(t *testing.T) {
	identity, policy := testIdentityAndPolicy(t, RoleContinuityWrite)
	policyBytes, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(policyBytes)
	policyDigest := hex.EncodeToString(digestBytes[:])
	payloadDigest, err := MutationPayloadSHA256(map[string]string{"decision": "plan_only"}, "reviewed")
	if err != nil {
		t.Fatal(err)
	}
	claims := MutationClaims{
		Action: "task.plan.confirm", AuditAction: "plan_confirmed", ProjectID: "demo-project",
		ResourceType: "task", ResourceID: "task-2", ExpectedRevision: 1, PayloadSHA256: payloadDigest,
	}
	now := time.Now().UTC()
	attestation, err := SignMutation(identity, "reviewer-1", policyDigest, claims, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMutation(attestation, policy, policyDigest, claims, now); err == nil || !strings.Contains(err.Error(), RoleTaskPlanConfirm) {
		t.Fatalf("missing elevated role error = %v", err)
	}
	policy.Members[0].Roles = append(policy.Members[0].Roles, RoleTaskPlanConfirm)
	if err := VerifyMutation(attestation, policy, policyDigest, claims, now); err == nil {
		t.Fatal("role change without corresponding policy digest was accepted")
	}
	invalid := claims
	invalid.ResourceType = "session"
	if _, err := SignMutation(identity, "reviewer-1", policyDigest, invalid, now); err == nil {
		t.Fatal("task mutation with session resource type was signed")
	}
}

func TestMutationPayloadDigestBindsReasonAndEntity(t *testing.T) {
	entity := map[string]string{"status": "blocked"}
	digest, err := MutationPayloadSHA256(entity, "network unavailable")
	if err != nil {
		t.Fatal(err)
	}
	otherReason, err := MutationPayloadSHA256(entity, "provider rejected request")
	if err != nil {
		t.Fatal(err)
	}
	otherEntity, err := MutationPayloadSHA256(map[string]string{"status": "completed"}, "network unavailable")
	if err != nil {
		t.Fatal(err)
	}
	if digest == otherReason || digest == otherEntity {
		t.Fatal("mutation digest did not bind both entity and audit reason")
	}
}
