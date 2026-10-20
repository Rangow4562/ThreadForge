package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

type continuityAuthorizer struct {
	identity     teamauth.Identity
	policy       teamauth.Policy
	policySHA256 string
	subject      string
}

func continuityMutationRoles(action string) []string {
	roles := []string{teamauth.RoleContinuityWrite}
	switch action {
	case "task.plan.confirm":
		roles = append(roles, teamauth.RoleTaskPlanConfirm)
	case "task.requirements.finalize", "task.requirements.reopen":
		roles = append(roles, teamauth.RoleRequirementsFinalize)
	case "continuity.review.append":
		roles = append(roles, teamauth.RoleContinuityReview)
	case "team.scope.decommission", "team.scope.restore":
		return []string{teamauth.RoleTeamScopeManage}
	}
	return roles
}

// authorizeContinuityActor preserves local-only projects. A project explicitly
// covered by the installed team policy requires a private identity with all
// operation roles; its policy subject replaces, rather than trusts, --actor.
func authorizeContinuityActor(database, projectID, action, actor, identityPath string) (string, *continuityAuthorizer, error) {
	paths, err := appdata.Resolve(database, "")
	if err != nil {
		return "", nil, err
	}
	policy, _, _, err := appdata.ReadTeamTrustPolicy(paths.Root)
	if errors.Is(err, appdata.ErrTeamTrustPolicyNotInstalled) {
		if strings.TrimSpace(identityPath) != "" {
			return "", nil, errors.New("--identity-key requires an installed team trust policy")
		}
		return actor, nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("load installed team trust policy: %w", err)
	}
	projectGoverned := false
	for _, member := range policy.Members {
		for _, project := range member.Projects {
			if project == projectID {
				projectGoverned = true
				break
			}
		}
		if projectGoverned {
			break
		}
	}
	if !projectGoverned {
		if strings.TrimSpace(identityPath) != "" {
			return "", nil, errors.New("project is not covered by the installed team trust policy")
		}
		return actor, nil, nil
	}
	if strings.TrimSpace(identityPath) == "" {
		return "", nil, errors.New("team-protected project mutation requires --identity-key")
	}
	identity, err := teamauth.LoadIdentity(identityPath)
	if err != nil {
		return "", nil, fmt.Errorf("load team identity: %w", err)
	}
	member, err := policy.AuthorizeIdentity(identity, projectID, continuityMutationRoles(action)...)
	if err != nil {
		return "", nil, err
	}
	if len(member.Subject) > 128 {
		return "", nil, errors.New("team subject exceeds the continuity audit actor limit")
	}
	if strings.TrimSpace(actor) != "" && actor != "local-user" && actor != member.Subject {
		return "", nil, errors.New("--actor must match the authenticated team subject")
	}
	policySHA256, err := teamauth.CanonicalPolicySHA256(policy)
	if err != nil {
		return "", nil, err
	}
	return member.Subject, &continuityAuthorizer{
		identity: identity, policy: policy, policySHA256: policySHA256, subject: member.Subject,
	}, nil
}

func (a *continuityAuthorizer) withMutationProof(ctx context.Context, action, auditAction, resourceType, resourceID, projectID string,
	expectedRevision int, entity any, reason string, at time.Time) (context.Context, error) {
	if a == nil {
		return ctx, nil
	}
	payloadSHA256, err := teamauth.MutationPayloadSHA256(entity, reason)
	if err != nil {
		return nil, err
	}
	claims := teamauth.MutationClaims{
		Action: action, AuditAction: auditAction, ProjectID: projectID, ResourceType: resourceType,
		ResourceID: resourceID, ExpectedRevision: expectedRevision, PayloadSHA256: payloadSHA256,
	}
	attestation, err := teamauth.SignMutation(a.identity, a.subject, a.policySHA256, claims, at)
	if err != nil {
		return nil, err
	}
	if err := teamauth.VerifyMutation(attestation, a.policy, a.policySHA256, claims, at); err != nil {
		return nil, err
	}
	return teamauth.WithMutationProof(ctx, teamauth.MutationProof{
		Attestation: attestation, Policy: a.policy, PolicySHA256: a.policySHA256,
	}), nil
}
