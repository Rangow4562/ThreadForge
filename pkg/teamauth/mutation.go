package teamauth

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

const MutationAttestationSchemaVersion = "threadforge.continuity-attestation.v1"

const (
	MutationMaxPayloadBytes = 1 << 20
	MutationMaxAge          = 5 * time.Minute
	MutationFutureSkew      = 1 * time.Minute
)

var mutationResourceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var mutationActionPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
var mutationAuditActionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type MutationClaims struct {
	Action           string `json:"action"`
	AuditAction      string `json:"audit_action"`
	ProjectID        string `json:"project_id"`
	ResourceType     string `json:"resource_type"`
	ResourceID       string `json:"resource_id"`
	ExpectedRevision int    `json:"expected_revision"`
	PayloadSHA256    string `json:"payload_sha256"`
}

type MutationAttestation struct {
	SchemaVersion string         `json:"schema_version"`
	Subject       string         `json:"subject"`
	KeyID         string         `json:"key_id"`
	PolicySHA256  string         `json:"policy_sha256"`
	Claims        MutationClaims `json:"claims"`
	IssuedAt      string         `json:"issued_at"`
	Signature     string         `json:"signature"`
}

type mutationSignedPayload struct {
	SchemaVersion string         `json:"schema_version"`
	Subject       string         `json:"subject"`
	KeyID         string         `json:"key_id"`
	PolicySHA256  string         `json:"policy_sha256"`
	Claims        MutationClaims `json:"claims"`
	IssuedAt      string         `json:"issued_at"`
}

type mutationContent struct {
	Entity json.RawMessage `json:"entity"`
	Reason string          `json:"reason"`
}

type MutationProof struct {
	Attestation  MutationAttestation
	Policy       Policy
	PolicySHA256 string
}

type mutationProofContextKey struct{}

func WithMutationProof(ctx context.Context, proof MutationProof) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	proof.Policy.Members = append([]Member(nil), proof.Policy.Members...)
	for i := range proof.Policy.Members {
		proof.Policy.Members[i].Projects = append([]string(nil), proof.Policy.Members[i].Projects...)
		proof.Policy.Members[i].Roles = append([]string(nil), proof.Policy.Members[i].Roles...)
	}
	return context.WithValue(ctx, mutationProofContextKey{}, proof)
}

func MutationProofFromContext(ctx context.Context) (MutationProof, bool) {
	if ctx == nil {
		return MutationProof{}, false
	}
	proof, ok := ctx.Value(mutationProofContextKey{}).(MutationProof)
	return proof, ok
}

func MutationPayloadSHA256(entity any, reason string) (string, error) {
	if len(reason) == 0 || len(reason) > 2000 {
		return "", errors.New("mutation reason must contain 1-2000 bytes")
	}
	entityJSON, err := json.Marshal(entity)
	if err != nil {
		return "", fmt.Errorf("encode mutation entity: %w", err)
	}
	if len(entityJSON) == 0 || len(entityJSON) > MutationMaxPayloadBytes {
		return "", errors.New("mutation entity exceeds the attestation payload limit")
	}
	content, err := json.Marshal(mutationContent{Entity: entityJSON, Reason: reason})
	if err != nil {
		return "", fmt.Errorf("encode mutation payload: %w", err)
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func CanonicalPolicySHA256(policy Policy) (string, error) {
	if err := policy.Validate(); err != nil {
		return "", fmt.Errorf("invalid team trust policy: %w", err)
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("encode team trust policy: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func SignMutation(identity Identity, subject, policySHA256 string, claims MutationClaims, at time.Time) (MutationAttestation, error) {
	if at.IsZero() || len(identity.PrivateKey) != ed25519.PrivateKeySize ||
		!keyIDPattern.MatchString(identity.KeyID) || KeyID(identity.PrivateKey.Public().(ed25519.PublicKey)) != identity.KeyID {
		return MutationAttestation{}, errors.New("invalid mutation signing identity or time")
	}
	if !namePattern.MatchString(subject) || !keyIDPattern.MatchString(policySHA256) || !validMutationClaims(claims) {
		return MutationAttestation{}, errors.New("invalid continuity mutation claims")
	}
	if _, ok := requiredMutationRoles(claims.Action); !ok {
		return MutationAttestation{}, errors.New("unsupported continuity mutation action")
	}
	issuedAt := at.UTC().Format(time.RFC3339Nano)
	payload := mutationSignedPayload{
		SchemaVersion: MutationAttestationSchemaVersion, Subject: subject, KeyID: identity.KeyID,
		PolicySHA256: policySHA256, Claims: claims, IssuedAt: issuedAt,
	}
	message, err := json.Marshal(payload)
	if err != nil {
		return MutationAttestation{}, err
	}
	signature := ed25519.Sign(identity.PrivateKey, message)
	return MutationAttestation{
		SchemaVersion: payload.SchemaVersion, Subject: payload.Subject, KeyID: payload.KeyID,
		PolicySHA256: payload.PolicySHA256, Claims: payload.Claims, IssuedAt: payload.IssuedAt,
		Signature: base64.StdEncoding.EncodeToString(signature),
	}, nil
}

func VerifyMutation(attestation MutationAttestation, policy Policy, policySHA256 string, expected MutationClaims, now time.Time) error {
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("invalid team trust policy: %w", err)
	}
	canonicalPolicySHA256, err := CanonicalPolicySHA256(policy)
	if err != nil || canonicalPolicySHA256 != policySHA256 {
		return errors.New("continuity attestation policy digest does not match the supplied policy")
	}
	if attestation.SchemaVersion != MutationAttestationSchemaVersion || !namePattern.MatchString(attestation.Subject) ||
		!keyIDPattern.MatchString(attestation.KeyID) || !keyIDPattern.MatchString(attestation.PolicySHA256) ||
		!validMutationClaims(attestation.Claims) || attestation.Claims != expected ||
		attestation.PolicySHA256 != policySHA256 {
		return errors.New("continuity attestation does not bind the expected mutation and trust policy")
	}
	if _, ok := requiredMutationRoles(expected.Action); !ok {
		return errors.New("unsupported continuity mutation action")
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, attestation.IssuedAt)
	if err != nil {
		return errors.New("continuity attestation issued_at must use RFC3339")
	}
	if !now.IsZero() {
		age := now.UTC().Sub(issuedAt)
		if age < -MutationFutureSkew || age > MutationMaxAge {
			return errors.New("continuity attestation is expired or not yet valid")
		}
	}
	var member *Member
	for i := range policy.Members {
		if policy.Members[i].KeyID == attestation.KeyID {
			member = &policy.Members[i]
			break
		}
	}
	if member == nil || member.Subject != attestation.Subject || !contains(member.Projects, expected.ProjectID) {
		return errors.New("continuity attestation signer is not trusted for this project")
	}
	roles, _ := requiredMutationRoles(expected.Action)
	for _, role := range roles {
		if !contains(member.Roles, role) {
			return fmt.Errorf("continuity attestation signer lacks required role %q", role)
		}
	}
	publicKeyBytes, _ := base64.StdEncoding.DecodeString(member.PublicKey)
	signature, err := base64.StdEncoding.DecodeString(attestation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("continuity attestation signature is malformed")
	}
	payload := mutationSignedPayload{
		SchemaVersion: attestation.SchemaVersion, Subject: attestation.Subject, KeyID: attestation.KeyID,
		PolicySHA256: attestation.PolicySHA256, Claims: attestation.Claims, IssuedAt: attestation.IssuedAt,
	}
	message, err := json.Marshal(payload)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKeyBytes), message, signature) {
		return errors.New("continuity attestation signature verification failed")
	}
	return nil
}

func validMutationClaims(claims MutationClaims) bool {
	_, knownAction := requiredMutationRoles(claims.Action)
	return knownAction && mutationActionPattern.MatchString(claims.Action) && mutationAuditActionPattern.MatchString(claims.AuditAction) &&
		namePattern.MatchString(claims.ProjectID) &&
		validMutationResource(claims.Action, claims.ResourceType) && validMutationAuditAction(claims.Action, claims.AuditAction) &&
		mutationResourceIDPattern.MatchString(claims.ResourceID) && claims.ExpectedRevision >= 0 &&
		keyIDPattern.MatchString(claims.PayloadSHA256)
}

func validMutationResource(action, resourceType string) bool {
	switch {
	case action == "team.scope.decommission" || action == "team.scope.restore":
		return resourceType == "project_scope"
	case len(action) >= 5 && action[:5] == "task.":
		return resourceType == "task"
	case len(action) >= 8 && action[:8] == "session.":
		return resourceType == "session"
	case len(action) >= 11 && action[:11] == "continuity.":
		return resourceType == "entry"
	default:
		return false
	}
}

func validMutationAuditAction(action, auditAction string) bool {
	switch action {
	case "team.scope.decommission":
		return auditAction == "decommissioned"
	case "team.scope.restore":
		return auditAction == "restored"
	case "task.create":
		return auditAction == "created"
	case "task.update":
		return auditAction == "updated"
	case "task.plan.revise":
		return auditAction == "plan_revised"
	case "task.plan.confirm":
		return auditAction == "plan_confirmed"
	case "task.requirements.update":
		return auditAction == "requirements_revised" || auditAction == "requirements_question_added" || auditAction == "requirements_question_answered"
	case "task.requirements.finalize":
		return auditAction == "requirements_finalized"
	case "task.requirements.reopen":
		return auditAction == "requirements_reopened"
	case "task.claim.acquire":
		return auditAction == "acquired"
	case "task.claim.renew":
		return auditAction == "renewed"
	case "task.claim.revalidate":
		return auditAction == "revalidated"
	case "task.claim.release":
		return auditAction == "released"
	case "task.claim.transfer":
		return auditAction == "transferred"
	case "task.handoff.accept":
		return auditAction == "handoff_accepted"
	case "session.start":
		return auditAction == "started"
	case "session.update":
		return auditAction == "updated" || auditAction == "closed"
	case "continuity.entry.append", "continuity.review.append":
		return auditAction == "appended"
	default:
		return false
	}
}

func requiredMutationRoles(action string) ([]string, bool) {
	if action == "team.scope.decommission" || action == "team.scope.restore" {
		return []string{RoleTeamScopeManage}, true
	}
	roles := []string{RoleContinuityWrite}
	switch action {
	case "task.create", "task.update", "task.requirements.update", "task.plan.revise", "task.claim.acquire",
		"task.claim.renew", "task.claim.revalidate", "task.claim.release", "task.claim.transfer", "task.handoff.accept",
		"session.start", "session.update", "continuity.entry.append":
	case "task.plan.confirm":
		roles = append(roles, RoleTaskPlanConfirm)
	case "task.requirements.finalize", "task.requirements.reopen":
		roles = append(roles, RoleRequirementsFinalize)
	case "continuity.review.append":
		roles = append(roles, RoleContinuityReview)
	default:
		return nil, false
	}
	return roles, true
}
