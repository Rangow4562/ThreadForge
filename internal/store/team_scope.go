package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

const TeamScopeStatusSchemaVersion = "threadforge.team-project-scope-status.v1"

type TeamScopeStatus struct {
	SchemaVersion  string `json:"schema_version"`
	ProjectID      string `json:"project_id"`
	OrganizationID string `json:"organization_id"`
	Status         string `json:"status"`
	Revision       int    `json:"revision"`
	LastAction     string `json:"last_action,omitempty"`
	ChangedAt      string `json:"changed_at,omitempty"`
}

type TeamScopeLifecycleMutation struct {
	ProjectID      string `json:"project_id"`
	OrganizationID string `json:"organization_id"`
	FromStatus     string `json:"from_status"`
	ToStatus       string `json:"to_status"`
	Revision       int    `json:"revision"`
}

type teamScopeEvent struct {
	projectID      string
	organizationID string
	revision       int
	action         string
	actor          string
	reason         string
	createdAt      string
	policySHA256   string
	attestation    string
}

// TeamProjectScopeStatus returns the retained scope and its current lifecycle state.
func (s *Store) TeamProjectScopeStatus(ctx context.Context, projectID string) (TeamScopeStatus, error) {
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 {
		return TeamScopeStatus{}, errors.New("project_id is required and cannot exceed 256 characters")
	}
	status := TeamScopeStatus{SchemaVersion: TeamScopeStatusSchemaVersion, ProjectID: projectID, Status: "active"}
	if err := s.db.QueryRowContext(ctx,
		`SELECT organization_id FROM team_project_scopes WHERE project_id = ?`, projectID).Scan(&status.OrganizationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TeamScopeStatus{}, errors.New("project has no registered team scope")
		}
		return TeamScopeStatus{}, err
	}
	var action, changedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT revision, action, created_at FROM team_project_scope_events WHERE project_id = ? ORDER BY revision DESC LIMIT 1`, projectID).
		Scan(&status.Revision, &action, &changedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return TeamScopeStatus{}, err
	}
	status.LastAction = action
	status.ChangedAt = changedAt
	if action == "decommissioned" {
		status.Status = "decommissioned"
	} else if action != "restored" {
		return TeamScopeStatus{}, errors.New("team project scope has an invalid lifecycle action")
	}
	return status, nil
}

// TeamProjectScopeIDs returns registered team projects in stable order.
func (s *Store) TeamProjectScopeIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id FROM team_project_scopes ORDER BY project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := make([]string, 0)
	for rows.Next() {
		var projectID string
		if err := rows.Scan(&projectID); err != nil {
			return nil, err
		}
		projects = append(projects, projectID)
		if len(projects) > MaxRecoveryTeamProjects {
			return nil, errors.New("too many team project scopes to enumerate safely")
		}
	}
	return projects, rows.Err()
}

// WithActiveTeamProjectScope serializes a project-file publication against
// decommission/restore and continuity mutations using the shared SQLite writer lock.
func (s *Store) WithActiveTeamProjectScope(ctx context.Context, projectID string, write func() error) error {
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 || write == nil {
		return errors.New("a registered project_id and write callback are required")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var organizationID string
		if err := tx.QueryRowContext(ctx,
			`SELECT organization_id FROM team_project_scopes WHERE project_id = ?`, projectID).Scan(&organizationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("project has no registered team scope")
			}
			return err
		}
		if err := acquireTeamProjectScopeWriteLock(ctx, tx, projectID); err != nil {
			return err
		}
		decommissioned, err := teamProjectScopeIsDecommissioned(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if decommissioned {
			return errors.New("team project scope is decommissioned; project publication is frozen")
		}
		return write()
	})
}

func acquireTeamProjectScopeWriteLock(ctx context.Context, tx *sql.Tx, projectID string) error {
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO team_project_scope_write_locks(project_id, generation, updated_at) VALUES(?, 1, ?)
		ON CONFLICT(project_id) DO UPDATE SET generation = team_project_scope_write_locks.generation + 1,
		updated_at = excluded.updated_at`, projectID, stamp)
	return err
}

// ChangeTeamProjectScopeLifecycle appends a signed decommission or restore event.
// Restoration requires a different canonical digest from the policy that decommissioned the scope.
func (s *Store) ChangeTeamProjectScopeLifecycle(ctx context.Context, projectID, action, actor, reason string) error {
	if action != "decommissioned" && action != "restored" {
		return errors.New("team scope lifecycle action must be decommissioned or restored")
	}
	if err := validateContinuityAudit(actor, reason); err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var organizationID string
		if err := tx.QueryRowContext(ctx,
			`SELECT organization_id FROM team_project_scopes WHERE project_id = ?`, projectID).Scan(&organizationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("project has no registered team scope")
			}
			return err
		}
		if err := acquireTeamProjectScopeWriteLock(ctx, tx, projectID); err != nil {
			return err
		}
		var revision int
		var previousAction, previousPolicy, previousAttestationJSON string
		err := tx.QueryRowContext(ctx,
			`SELECT revision, action, policy_sha256, attestation_json FROM team_project_scope_events WHERE project_id = ? ORDER BY revision DESC LIMIT 1`, projectID).
			Scan(&revision, &previousAction, &previousPolicy, &previousAttestationJSON)
		if errors.Is(err, sql.ErrNoRows) {
			revision = 0
		} else if err != nil {
			return err
		}
		fromStatus, toStatus := "active", "decommissioned"
		if action == "restored" {
			fromStatus, toStatus = "decommissioned", "active"
		}
		if (action == "decommissioned" && revision > 0 && previousAction != "restored") ||
			(action == "restored" && (revision == 0 || previousAction != "decommissioned")) {
			return fmt.Errorf("cannot %s team project scope from its current lifecycle state", action)
		}
		proof, ok := teamauth.MutationProofFromContext(ctx)
		if !ok {
			return errors.New("team scope lifecycle mutation requires signed authorization")
		}
		policy, _, sourceFileSHA256, err := appdata.ReadTeamTrustPolicy(s.teamPolicyRoot)
		if err != nil {
			return fmt.Errorf("read pinned team trust policy for scope lifecycle: %w", err)
		}
		if policy.OrganizationID != organizationID {
			return errors.New("installed team trust policy organization does not match the registered project scope")
		}
		policySHA256, err := teamauth.CanonicalPolicySHA256(policy)
		if err != nil {
			return err
		}
		if proof.PolicySHA256 != policySHA256 {
			return errors.New("team scope authorization was created under a stale or different trust policy")
		}
		if action == "restored" && policySHA256 == previousPolicy {
			return errors.New("restoring a team scope requires a newly pinned trust policy")
		}
		var decommissionAttestation teamauth.MutationAttestation
		if action == "restored" {
			if err := decodeStrictJSON(previousAttestationJSON, &decommissionAttestation); err != nil || decommissionAttestation.KeyID == "" {
				return errors.New("cannot restore a team scope with invalid decommission authorization history")
			}
			if proof.Attestation.KeyID == decommissionAttestation.KeyID {
				return errors.New("restoring a team scope requires an identity key different from the decommissioning signer")
			}
		}
		entity := TeamScopeLifecycleMutation{ProjectID: projectID, OrganizationID: organizationID,
			FromStatus: fromStatus, ToStatus: toStatus, Revision: revision + 1}
		claims := teamauth.MutationClaims{
			Action:      "team.scope." + map[string]string{"decommissioned": "decommission", "restored": "restore"}[action],
			AuditAction: action, ProjectID: projectID, ResourceType: "project_scope",
			ResourceID: TeamScopeResourceID(projectID), ExpectedRevision: revision,
		}
		claims.PayloadSHA256, err = teamauth.MutationPayloadSHA256(entity, reason)
		if err != nil {
			return err
		}
		if proof.Attestation.Subject != actor || proof.Policy.OrganizationID != organizationID {
			return errors.New("team scope audit actor or authorization organization is inconsistent")
		}
		if err := teamauth.VerifyMutation(proof.Attestation, policy, policySHA256, claims, time.Now().UTC()); err != nil {
			return fmt.Errorf("verify team scope authorization: %w", err)
		}
		policyJSON, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO continuity_policy_snapshots(policy_sha256, organization_id, policy_json, source_file_sha256, captured_at)
			 VALUES(?, ?, ?, ?, ?)`, policySHA256, organizationID, string(policyJSON), sourceFileSHA256, stamp); err != nil {
			return fmt.Errorf("retain team scope trust snapshot: %w", err)
		}
		attestationJSON, err := json.Marshal(proof.Attestation)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO team_project_scope_events(project_id, organization_id, revision, action, actor, reason, created_at, policy_sha256, attestation_json)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, projectID, organizationID, revision+1, action, actor, reason, stamp, policySHA256, string(attestationJSON))
		if err != nil {
			return fmt.Errorf("append team scope lifecycle event: %w", err)
		}
		return nil
	})
}

func TeamScopeResourceID(projectID string) string {
	digest := sha256.Sum256([]byte(projectID))
	return "scope-" + hex.EncodeToString(digest[:])[:58]
}

func teamProjectScopeIsDecommissioned(ctx context.Context, q queryer, projectID string) (bool, error) {
	var action string
	err := q.QueryRowContext(ctx,
		`SELECT action FROM team_project_scope_events WHERE project_id = ? ORDER BY revision DESC LIMIT 1`, projectID).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if action != "decommissioned" && action != "restored" {
		return false, errors.New("team project scope has an invalid lifecycle action")
	}
	return action == "decommissioned", nil
}

func (s *Store) registerTeamProjects(policy teamauth.Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	return s.withTx(context.Background(), func(tx *sql.Tx) error {
		for _, member := range policy.Members {
			for _, projectID := range member.Projects {
				var organizationID string
				err := tx.QueryRowContext(context.Background(),
					`SELECT organization_id FROM team_project_scopes WHERE project_id = ?`, projectID).Scan(&organizationID)
				if errors.Is(err, sql.ErrNoRows) {
					if _, err := tx.ExecContext(context.Background(),
						`INSERT INTO team_project_scopes(project_id, organization_id, first_seen_at) VALUES(?, ?, ?)`,
						projectID, policy.OrganizationID, stamp); err != nil {
						return fmt.Errorf("register trusted team project scope: %w", err)
					}
					continue
				}
				if err != nil {
					return err
				}
				if organizationID != policy.OrganizationID {
					return fmt.Errorf("project %q is already pinned to another team organization", projectID)
				}
			}
		}
		return nil
	})
}
