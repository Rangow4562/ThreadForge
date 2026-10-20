package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/continuity"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

const TeamContinuityAuditReportSchemaVersion = "threadforge.team-continuity-audit.v2"
const maxTeamAuditFindings = 200
const maxTeamAuditPolicyBytes = 1 << 20

type TeamContinuityAuditReport struct {
	SchemaVersion               string                       `json:"schema_version"`
	Status                      string                       `json:"status"`
	TeamProjects                int                          `json:"team_projects"`
	TaskRevisionsChecked        int                          `json:"task_revisions_checked"`
	SessionRevisionsChecked     int                          `json:"session_revisions_checked"`
	EntriesChecked              int                          `json:"entries_checked"`
	ScopeLifecycleEventsChecked int                          `json:"scope_lifecycle_events_checked"`
	VerifiedAuthorizations      int                          `json:"verified_authorizations"`
	UnauthenticatedBeforeScope  int                          `json:"unauthenticated_before_scope"`
	InvalidRecords              int                          `json:"invalid_records"`
	Findings                    []TeamContinuityAuditFinding `json:"findings"`
	FindingsTruncated           bool                         `json:"findings_truncated"`
}

type TeamContinuityAuditFinding struct {
	Code       string `json:"code"`
	Entity     string `json:"entity"`
	ResourceID string `json:"resource_id,omitempty"`
	Action     string `json:"action,omitempty"`
	Revision   int    `json:"revision,omitempty"`
}

type teamContinuityScope struct {
	firstSeen      time.Time
	organizationID string
}

type teamPolicySnapshot struct {
	policy teamauth.Policy
}

type continuityAuditReference struct {
	sequence    int64
	action      string
	actor       string
	reason      string
	createdAt   string
	policySHA   string
	attestation string
}

type taskAuditGroup struct {
	projectID string
	id        string
	revision  int
	action    string
	actor     string
	reason    string
	createdAt string
	payload   string
	audits    []continuityAuditReference
}

type sessionAuditGroup struct {
	projectID string
	id        string
	revision  int
	action    string
	actor     string
	reason    string
	createdAt string
	payload   string
	audits    []continuityAuditReference
}

// VerifyTeamContinuityAudit checks team-scoped task/session history and entries
// against their append-only audit evidence and retained policy snapshots. Rows
// written before the store first registered a project as team-scoped are
// reported for review rather than represented as authenticated team activity.
func (s *Store) VerifyTeamContinuityAudit(ctx context.Context) (TeamContinuityAuditReport, error) {
	report := TeamContinuityAuditReport{
		SchemaVersion: TeamContinuityAuditReportSchemaVersion,
		Status:        "passed",
		Findings:      []TeamContinuityAuditFinding{},
	}
	scopes, err := s.loadTeamAuditScopes(ctx, &report)
	if err != nil {
		return report, err
	}
	report.TeamProjects = len(scopes)
	policies, err := s.loadTeamAuditPolicies(ctx, &report)
	if err != nil {
		return report, err
	}
	if err := s.verifyTeamScopeLifecycle(ctx, scopes, policies, &report); err != nil {
		return report, err
	}
	if err := s.verifyTeamTaskHistory(ctx, scopes, policies, &report); err != nil {
		return report, err
	}
	if err := s.verifyTeamSessionHistory(ctx, scopes, policies, &report); err != nil {
		return report, err
	}
	if err := s.verifyOrphanTeamAuditEvents(ctx, &report); err != nil {
		return report, err
	}
	if err := s.verifyTeamCurrentContinuityState(ctx, &report); err != nil {
		return report, err
	}
	if err := s.verifyTeamEntries(ctx, scopes, policies, &report); err != nil {
		return report, err
	}
	switch {
	case report.InvalidRecords > 0:
		report.Status = "failed"
	case report.UnauthenticatedBeforeScope > 0:
		report.Status = "review_required"
	case report.TeamProjects == 0:
		report.Status = "no_team_projects"
	}
	return report, nil
}

func (s *Store) verifyOrphanTeamAuditEvents(ctx context.Context, report *TeamContinuityAuditReport) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.entity, c.entity_id, c.revision, c.action
		FROM continuity_audit c
	JOIN continuity_tasks t ON c.entity = 'task' AND t.id = c.entity_id
		JOIN team_project_scopes p ON p.project_id = t.project_id
		LEFT JOIN continuity_task_history h ON h.task_id = c.entity_id AND h.revision = c.revision
		WHERE h.task_id IS NULL
		UNION ALL
		SELECT c.entity, c.entity_id, c.revision, c.action
		FROM continuity_audit c
		JOIN continuity_sessions cs ON c.entity = 'session' AND cs.id = c.entity_id
		JOIN team_project_scopes p ON p.project_id = cs.project_id
		LEFT JOIN continuity_session_history h ON h.session_id = c.entity_id AND h.revision = c.revision
		WHERE h.session_id IS NULL
		ORDER BY 1, 2, 3`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var entity, id, action string
		var revision int
		if err := rows.Scan(&entity, &id, &revision, &action); err != nil {
			return err
		}
		report.addInvalid("orphan_audit_event", entity, id, action, revision)
	}
	return rows.Err()
}

func (s *Store) verifyTeamCurrentContinuityState(ctx context.Context, report *TeamContinuityAuditReport) error {
	taskRows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.project_id, t.revision, t.task_json, COALESCE(h.revision, 0), COALESCE(h.task_json, '')
		FROM continuity_tasks t
		JOIN team_project_scopes p ON p.project_id = t.project_id
		LEFT JOIN continuity_task_history h ON h.task_id = t.id AND h.revision = t.revision
		ORDER BY t.id`)
	if err != nil {
		return err
	}
	for taskRows.Next() {
		var id, projectID, currentJSON, historyJSON string
		var revision, historyRevision int
		if err := taskRows.Scan(&id, &projectID, &revision, &currentJSON, &historyRevision, &historyJSON); err != nil {
			_ = taskRows.Close()
			return err
		}
		if revision != historyRevision || historyJSON == "" {
			report.addInvalid("current_task_missing_history", "task", id, "", revision)
			continue
		}
		currentTask, currentErr := decodeTask(currentJSON)
		historyTask, historyErr := decodeTask(historyJSON)
		if currentErr != nil || historyErr != nil || currentTask.ProjectID != projectID ||
			currentTask.ID != id || currentTask.Revision != revision || !reflect.DeepEqual(currentTask, historyTask) {
			report.addInvalid("current_task_history_mismatch", "task", id, "", revision)
		}
	}
	if err := taskRows.Err(); err != nil {
		_ = taskRows.Close()
		return err
	}
	if err := taskRows.Close(); err != nil {
		return err
	}

	sessionRows, err := s.db.QueryContext(ctx, `
		SELECT cs.id, cs.project_id, cs.revision, cs.session_json, COALESCE(h.revision, 0), COALESCE(h.session_json, '')
		FROM continuity_sessions cs
		JOIN team_project_scopes p ON p.project_id = cs.project_id
		LEFT JOIN continuity_session_history h ON h.session_id = cs.id AND h.revision = cs.revision
		ORDER BY cs.id`)
	if err != nil {
		return err
	}
	for sessionRows.Next() {
		var id, projectID, currentJSON, historyJSON string
		var revision, historyRevision int
		if err := sessionRows.Scan(&id, &projectID, &revision, &currentJSON, &historyRevision, &historyJSON); err != nil {
			_ = sessionRows.Close()
			return err
		}
		if revision != historyRevision || historyJSON == "" {
			report.addInvalid("current_session_missing_history", "session", id, "", revision)
			continue
		}
		currentSession, currentErr := decodeSession(currentJSON)
		historySession, historyErr := decodeSession(historyJSON)
		if currentErr != nil || historyErr != nil || currentSession.ProjectID != projectID ||
			currentSession.ID != id || currentSession.Revision != revision || !reflect.DeepEqual(currentSession, historySession) {
			report.addInvalid("current_session_history_mismatch", "session", id, "", revision)
		}
	}
	if err := sessionRows.Err(); err != nil {
		_ = sessionRows.Close()
		return err
	}
	return sessionRows.Close()
}

func (s *Store) loadTeamAuditScopes(ctx context.Context, report *TeamContinuityAuditReport) (map[string]teamContinuityScope, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id, organization_id, first_seen_at FROM team_project_scopes ORDER BY project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := make(map[string]teamContinuityScope)
	for rows.Next() {
		var projectID, organizationID, firstSeen string
		if err := rows.Scan(&projectID, &organizationID, &firstSeen); err != nil {
			return nil, err
		}
		stamp, parseErr := time.Parse(time.RFC3339Nano, firstSeen)
		if strings.TrimSpace(projectID) == "" || strings.TrimSpace(organizationID) == "" || parseErr != nil {
			report.addInvalid("invalid_team_scope", "project", projectID, "", 0)
			continue
		}
		scopes[projectID] = teamContinuityScope{firstSeen: stamp, organizationID: organizationID}
	}
	return scopes, rows.Err()
}

func (s *Store) verifyTeamScopeLifecycle(ctx context.Context, scopes map[string]teamContinuityScope,
	policies map[string]teamPolicySnapshot, report *TeamContinuityAuditReport) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, organization_id, revision, action, actor, reason, created_at, policy_sha256, attestation_json
		FROM team_project_scope_events ORDER BY project_id, revision`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type lifecycleState struct {
		revision             int
		status               string
		decommissionedPolicy string
		decommissionedKeyID  string
	}
	states := make(map[string]lifecycleState)
	for rows.Next() {
		var event teamScopeEvent
		if err := rows.Scan(&event.projectID, &event.organizationID, &event.revision, &event.action, &event.actor,
			&event.reason, &event.createdAt, &event.policySHA256, &event.attestation); err != nil {
			return err
		}
		report.ScopeLifecycleEventsChecked++
		scope, exists := scopes[event.projectID]
		if !exists || scope.organizationID != event.organizationID {
			report.addInvalid("invalid_team_scope", "project", event.projectID, event.action, event.revision)
			continue
		}
		state, exists := states[event.projectID]
		if !exists {
			state.status = "active"
		}
		if event.revision != state.revision+1 || !isSHA256Digest(event.policySHA256) ||
			validateContinuityAudit(event.actor, event.reason) != nil || parseRFC3339(event.createdAt) == false {
			report.addInvalid("invalid_scope_lifecycle_event", "project", event.projectID, event.action, event.revision)
			continue
		}
		fromStatus, toStatus := "active", "decommissioned"
		mutationAction := "team.scope.decommission"
		if event.action == "decommissioned" {
			if state.status != "active" {
				report.addInvalid("invalid_scope_lifecycle_transition", "project", event.projectID, event.action, event.revision)
				continue
			}
		} else if event.action == "restored" {
			fromStatus, toStatus = "decommissioned", "active"
			mutationAction = "team.scope.restore"
			if state.status != "decommissioned" || state.decommissionedPolicy == "" || event.policySHA256 == state.decommissionedPolicy {
				report.addInvalid("invalid_scope_lifecycle_transition", "project", event.projectID, event.action, event.revision)
				continue
			}
		} else {
			report.addInvalid("invalid_scope_lifecycle_event", "project", event.projectID, event.action, event.revision)
			continue
		}
		policySnapshot, exists := policies[event.policySHA256]
		if !exists || policySnapshot.policy.OrganizationID != event.organizationID {
			report.addInvalid("missing_scope_lifecycle_policy", "project", event.projectID, event.action, event.revision)
			continue
		}
		entity := TeamScopeLifecycleMutation{ProjectID: event.projectID, OrganizationID: event.organizationID,
			FromStatus: fromStatus, ToStatus: toStatus, Revision: event.revision}
		payloadSHA, payloadErr := teamauth.MutationPayloadSHA256(entity, event.reason)
		claims := teamauth.MutationClaims{Action: mutationAction, AuditAction: event.action, ProjectID: event.projectID,
			ResourceType: "project_scope", ResourceID: TeamScopeResourceID(event.projectID),
			ExpectedRevision: event.revision - 1, PayloadSHA256: payloadSHA}
		var attestation teamauth.MutationAttestation
		decodeErr := decodeStrictJSON(event.attestation, &attestation)
		if payloadErr != nil || decodeErr != nil || attestation.Subject != event.actor || attestation.PolicySHA256 != event.policySHA256 ||
			teamauth.VerifyMutation(attestation, policySnapshot.policy, event.policySHA256, claims, time.Time{}) != nil {
			report.addInvalid("invalid_scope_lifecycle_authorization", "project", event.projectID, event.action, event.revision)
			continue
		}
		if event.action == "restored" && attestation.KeyID == state.decommissionedKeyID {
			report.addInvalid("invalid_scope_lifecycle_transition", "project", event.projectID, event.action, event.revision)
			continue
		}
		state.revision = event.revision
		state.status = toStatus
		report.VerifiedAuthorizations++
		if event.action == "decommissioned" {
			state.decommissionedPolicy = event.policySHA256
			state.decommissionedKeyID = attestation.KeyID
		}
		states[event.projectID] = state
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

func parseRFC3339(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func (s *Store) loadTeamAuditPolicies(ctx context.Context, report *TeamContinuityAuditReport) (map[string]teamPolicySnapshot, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT policy_sha256, organization_id, policy_json, source_file_sha256 FROM continuity_policy_snapshots ORDER BY policy_sha256`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := make(map[string]teamPolicySnapshot)
	for rows.Next() {
		var policySHA, organizationID, policyJSON, sourceFileSHA string
		if err := rows.Scan(&policySHA, &organizationID, &policyJSON, &sourceFileSHA); err != nil {
			return nil, err
		}
		var policy teamauth.Policy
		if !isSHA256Digest(policySHA) || !isSHA256Digest(sourceFileSHA) || len(policyJSON) > maxTeamAuditPolicyBytes ||
			decodeStrictJSON(policyJSON, &policy) != nil || policy.OrganizationID != organizationID {
			report.addInvalid("invalid_policy_snapshot", "policy", policySHA, "snapshot", 0)
			continue
		}
		canonicalSHA, err := teamauth.CanonicalPolicySHA256(policy)
		if err != nil || canonicalSHA != policySHA {
			report.addInvalid("invalid_policy_snapshot", "policy", policySHA, "snapshot", 0)
			continue
		}
		policies[policySHA] = teamPolicySnapshot{policy: policy}
	}
	return policies, rows.Err()
}

func (s *Store) verifyTeamTaskHistory(ctx context.Context, scopes map[string]teamContinuityScope,
	policies map[string]teamPolicySnapshot, report *TeamContinuityAuditReport) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.project_id, h.task_id, h.revision, h.action, h.actor, h.reason, h.created_at, h.task_json,
		       COALESCE(c.sequence, 0), COALESCE(c.action, ''), COALESCE(c.actor, ''), COALESCE(c.reason, ''), COALESCE(c.created_at, ''),
		       COALESCE(a.policy_sha256, ''), COALESCE(a.attestation_json, '')
		FROM continuity_task_history h
		JOIN continuity_tasks t ON t.id = h.task_id
		JOIN team_project_scopes p ON p.project_id = t.project_id
		LEFT JOIN continuity_audit c ON c.entity = 'task' AND c.entity_id = h.task_id AND c.revision = h.revision
		LEFT JOIN continuity_authorizations a ON a.sequence = c.sequence
		ORDER BY h.task_id, h.revision, c.sequence`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var current *taskAuditGroup
	flush := func() {
		if current != nil {
			report.verifyTaskAuditGroup(*current, scopes, policies)
		}
	}
	for rows.Next() {
		var group taskAuditGroup
		var sequence int64
		var audit continuityAuditReference
		if err := rows.Scan(&group.projectID, &group.id, &group.revision, &group.action, &group.actor,
			&group.reason, &group.createdAt, &group.payload, &sequence, &audit.action, &audit.actor, &audit.reason,
			&audit.createdAt, &audit.policySHA, &audit.attestation); err != nil {
			return err
		}
		if current == nil || current.id != group.id || current.revision != group.revision {
			flush()
			current = &group
		} else if current.projectID != group.projectID || current.action != group.action || current.actor != group.actor ||
			current.reason != group.reason || current.createdAt != group.createdAt || current.payload != group.payload {
			report.addInvalid("conflicting_task_history_join", "task", group.id, group.action, group.revision)
		}
		if sequence > 0 {
			audit.sequence = sequence
			current.audits = append(current.audits, audit)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

func (s *Store) verifyTeamSessionHistory(ctx context.Context, scopes map[string]teamContinuityScope,
	policies map[string]teamPolicySnapshot, report *TeamContinuityAuditReport) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT cs.project_id, h.session_id, h.revision, h.action, h.actor, h.reason, h.created_at, h.session_json,
		       COALESCE(c.sequence, 0), COALESCE(c.action, ''), COALESCE(c.actor, ''), COALESCE(c.reason, ''), COALESCE(c.created_at, ''),
	       COALESCE(a.policy_sha256, ''), COALESCE(a.attestation_json, '')
		FROM continuity_session_history h
		JOIN continuity_sessions cs ON cs.id = h.session_id
		JOIN team_project_scopes p ON p.project_id = cs.project_id
		LEFT JOIN continuity_audit c ON c.entity = 'session' AND c.entity_id = h.session_id AND c.revision = h.revision
		LEFT JOIN continuity_authorizations a ON a.sequence = c.sequence
		ORDER BY h.session_id, h.revision, c.sequence`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var current *sessionAuditGroup
	flush := func() {
		if current != nil {
			report.verifySessionAuditGroup(*current, scopes, policies)
		}
	}
	for rows.Next() {
		var group sessionAuditGroup
		var sequence int64
		var audit continuityAuditReference
		if err := rows.Scan(&group.projectID, &group.id, &group.revision, &group.action, &group.actor,
			&group.reason, &group.createdAt, &group.payload, &sequence, &audit.action, &audit.actor, &audit.reason,
			&audit.createdAt, &audit.policySHA, &audit.attestation); err != nil {
			return err
		}
		if current == nil || current.id != group.id || current.revision != group.revision {
			flush()
			current = &group
		} else if current.projectID != group.projectID || current.action != group.action || current.actor != group.actor ||
			current.reason != group.reason || current.createdAt != group.createdAt || current.payload != group.payload {
			report.addInvalid("conflicting_session_history_join", "session", group.id, group.action, group.revision)
		}
		if sequence > 0 {
			audit.sequence = sequence
			current.audits = append(current.audits, audit)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

func (s *Store) verifyTeamEntries(ctx context.Context, scopes map[string]teamContinuityScope,
	policies map[string]teamPolicySnapshot, report *TeamContinuityAuditReport) error {
	claimStates := make(map[string]map[string]continuity.TaskClaim)
	handoffStates := make(map[string]map[string]continuity.TaskHandoff)
	claimTransfers := make(map[taskClaimTransferKey]continuity.TaskClaim)
	checkpoints := make(map[string]continuity.Entry)
	acceptedHandoffs := make([]continuity.TaskHandoff, 0)
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.project_id, p.first_seen_at, e.id, e.task_id, e.session_id, e.actor, e.created_at, e.entry_json,
		       COALESCE(t.project_id, ''), COALESCE(cs.project_id, ''), COALESCE(cs.task_id, '')
		FROM continuity_entries e
		JOIN team_project_scopes p ON p.project_id = e.project_id
		LEFT JOIN continuity_tasks t ON t.id = e.task_id
		LEFT JOIN continuity_sessions cs ON cs.id = e.session_id
		ORDER BY e.sequence`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var projectID, firstSeen, id, taskID, sessionID, actor, createdAt, payload string
		var taskProjectID, sessionProjectID, sessionTaskID string
		if err := rows.Scan(&projectID, &firstSeen, &id, &taskID, &sessionID, &actor, &createdAt, &payload,
			&taskProjectID, &sessionProjectID, &sessionTaskID); err != nil {
			return err
		}
		report.EntriesChecked++
		var entry continuity.Entry
		if decodeStrictJSON(payload, &entry) != nil || continuity.ValidateEntry(entry) != nil || entry.ID != id ||
			entry.ProjectID != projectID || entry.TaskID != taskID || entry.SessionID != sessionID || entry.Actor != actor || entry.CreatedAt != createdAt {
			report.addInvalid("invalid_entry_record", "entry", id, "appended", 0)
			continue
		}
		if taskProjectID != projectID || (sessionID != "" && (sessionProjectID != projectID || sessionTaskID != taskID)) ||
			(sessionID == "" && sessionProjectID != "") {
			report.addInvalid("entry_scope_mismatch", "entry", id, "appended", 0)
			continue
		}
		scope, exists := scopes[projectID]
		if !exists {
			report.addInvalid("missing_team_scope", "entry", id, "appended", 0)
			continue
		}
		stamp, parseErr := time.Parse(time.RFC3339Nano, firstSeen)
		if parseErr != nil || !stamp.Equal(scope.firstSeen) {
			report.addInvalid("invalid_team_scope", "project", projectID, "", 0)
			continue
		}
		if len(entry.Authorization) == 0 {
			if auditStampBeforeScope(createdAt, scope.firstSeen) {
				report.UnauthenticatedBeforeScope++
				report.addFinding("unsigned_before_team_scope", "entry", id, "appended", 0)
			} else {
				report.addInvalid("missing_authorization", "entry", id, "appended", 0)
			}
			continue
		}
		action, auditAction, resourceType, resourceID, expectedRevision :=
			"continuity.entry.append", "appended", "entry", id, 0
		if entry.Kind == continuity.EntryReview || entry.Kind == continuity.EntryVerification {
			action = "continuity.review.append"
		}
		if entry.Kind == continuity.EntryTaskClaim {
			if entry.TaskClaim == nil {
				report.addInvalid("invalid_task_claim_event", "entry", id, "", 0)
				continue
			}
			var ok bool
			action, ok = taskClaimAction(entry.TaskClaim.Action)
			if !ok {
				report.addInvalid("invalid_task_claim_event", "entry", id, entry.TaskClaim.Action, entry.TaskClaim.Claim.Revision)
				continue
			}
			auditAction, resourceType, resourceID, expectedRevision = entry.TaskClaim.Action, "task", taskID, entry.TaskClaim.ExpectedRevision
			state := claimStates[projectID]
			if state == nil {
				state = make(map[string]continuity.TaskClaim)
				claimStates[projectID] = state
			}
			if err := applyTaskClaimEvent(state, entry); err != nil {
				report.addInvalid("invalid_task_claim_transition", "entry", id, entry.TaskClaim.Action, entry.TaskClaim.Claim.Revision)
				continue
			}
		}
		unsigned := entry
		unsigned.Authorization = nil
		if err := verifyTeamMutationEvidence(entry.Authorization, "", action, auditAction, resourceType, resourceID,
			projectID, expectedRevision, unsigned, entry.Actor, continuity.EntryAppendAuthorizationReason, createdAt, policies); err != nil {
			report.addInvalid("invalid_authorization", "entry", id, "appended", 0)
			continue
		}
		report.VerifiedAuthorizations++
		if entry.Kind == continuity.EntryTaskClaim && entry.TaskClaim.Action == "transferred" {
			claim := entry.TaskClaim.Claim
			claimTransfers[taskClaimTransferKey{projectID: projectID, taskID: taskID, claimID: claim.ID, revision: claim.Revision}] = claim
		}
		if entry.Kind == continuity.EntryCheckpoint {
			checkpoints[entry.ID] = entry
		}
		if entry.Kind == continuity.EntryTaskHandoff {
			state := handoffStates[projectID]
			if state == nil {
				state = make(map[string]continuity.TaskHandoff)
				handoffStates[projectID] = state
			}
			if err := applyTaskHandoffEvent(state, entry); err != nil {
				report.addInvalid("invalid_task_handoff_transition", "entry", id, entry.TaskHandoff.Action, entry.TaskHandoff.Handoff.Revision)
				continue
			}
			if entry.TaskHandoff.Action == "accepted" {
				acceptedHandoffs = append(acceptedHandoffs, entry.TaskHandoff.Handoff)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return s.verifyAcceptedTaskHandoffEvidence(ctx, acceptedHandoffs, claimTransfers, checkpoints, report)
}

type taskClaimTransferKey struct {
	projectID string
	taskID    string
	claimID   string
	revision  int
}

func (s *Store) verifyAcceptedTaskHandoffEvidence(ctx context.Context, handoffs []continuity.TaskHandoff,
	claimTransfers map[taskClaimTransferKey]continuity.TaskClaim, checkpoints map[string]continuity.Entry,
	report *TeamContinuityAuditReport) error {
	for _, handoff := range handoffs {
		var payload, action, actor, createdAt string
		err := s.db.QueryRowContext(ctx,
			`SELECT task_json, action, actor, created_at FROM continuity_task_history WHERE task_id = ? AND revision = ?`,
			handoff.TaskID, handoff.TransferredTaskRevision).Scan(&payload, &action, &actor, &createdAt)
		var task continuity.Task
		if err != nil || decodeStrictJSON(payload, &task) != nil || continuity.ValidateTask(task) != nil ||
			action != "handoff_accepted" || actor != handoff.ToOwner || createdAt != handoff.UpdatedAt ||
			task.ProjectID != handoff.ProjectID || task.Owner != handoff.ToOwner || task.Revision != handoff.TransferredTaskRevision {
			report.addInvalid("handoff_task_transfer_evidence_mismatch", "task", handoff.TaskID, "handoff_accepted", handoff.TransferredTaskRevision)
		}
		claim, exists := claimTransfers[taskClaimTransferKey{
			projectID: handoff.ProjectID, taskID: handoff.TaskID, claimID: handoff.ClaimID, revision: handoff.TransferredClaimRevision,
		}]
		if !exists || claim.Owner != handoff.ToOwner || claim.TaskRevision != handoff.TransferredTaskRevision ||
			claim.BaselineSHA256 != handoff.BaselineSHA256 {
			report.addInvalid("handoff_claim_transfer_evidence_mismatch", "task", handoff.TaskID, "transferred", handoff.TransferredClaimRevision)
		}
		checkpoint, exists := checkpoints[handoff.CheckpointEntryID]
		if !exists || checkpoint.Kind != continuity.EntryCheckpoint || checkpoint.ProjectID != handoff.ProjectID ||
			checkpoint.TaskID != handoff.TaskID || checkpoint.Actor != handoff.ToOwner || checkpoint.CreatedAt != handoff.UpdatedAt ||
			checkpoint.Summary != handoff.CheckpointSummary || checkpoint.Details != handoff.CheckpointDetails {
			report.addInvalid("handoff_checkpoint_evidence_mismatch", "entry", handoff.CheckpointEntryID, "appended", 0)
		}
	}
	return nil
}

func (report *TeamContinuityAuditReport) verifyTaskAuditGroup(group taskAuditGroup,
	scopes map[string]teamContinuityScope, policies map[string]teamPolicySnapshot) {
	report.TaskRevisionsChecked++
	var task continuity.Task
	if decodeStrictJSON(group.payload, &task) != nil || continuity.ValidateTask(task) != nil || task.ID != group.id ||
		task.ProjectID != group.projectID || task.Revision != group.revision || task.UpdatedAt != group.createdAt {
		report.addInvalid("invalid_task_revision", "task", group.id, group.action, group.revision)
		return
	}
	if len(group.audits) != 1 {
		code := "missing_audit_event"
		if len(group.audits) > 1 {
			code = "duplicate_audit_events"
		}
		report.addInvalid(code, "task", group.id, group.action, group.revision)
		return
	}
	audit := group.audits[0]
	if audit.action != group.action || audit.actor != group.actor || audit.reason != group.reason || audit.createdAt != group.createdAt {
		report.addInvalid("audit_history_mismatch", "task", group.id, group.action, group.revision)
		return
	}
	scope, exists := scopes[group.projectID]
	if !exists {
		report.addInvalid("missing_team_scope", "task", group.id, group.action, group.revision)
		return
	}
	if audit.attestation == "" {
		if auditStampBeforeScope(group.createdAt, scope.firstSeen) {
			report.UnauthenticatedBeforeScope++
			report.addFinding("unsigned_before_team_scope", "task", group.id, group.action, group.revision)
		} else {
			report.addInvalid("missing_authorization", "task", group.id, group.action, group.revision)
		}
		return
	}
	expectedAction, ok := teamAuditTaskMutationAction(group.action)
	if !ok {
		report.addInvalid("unknown_task_audit_action", "task", group.id, group.action, group.revision)
		return
	}
	if err := verifyTeamMutationEvidence(json.RawMessage(audit.attestation), audit.policySHA, expectedAction,
		group.action, "task", group.id, group.projectID, group.revision-1, task, group.actor, group.reason, group.createdAt, policies); err != nil {
		report.addInvalid("invalid_authorization", "task", group.id, group.action, group.revision)
		return
	}
	report.VerifiedAuthorizations++
}

func (report *TeamContinuityAuditReport) verifySessionAuditGroup(group sessionAuditGroup,
	scopes map[string]teamContinuityScope, policies map[string]teamPolicySnapshot) {
	report.SessionRevisionsChecked++
	var session continuity.Session
	if decodeStrictJSON(group.payload, &session) != nil || continuity.ValidateSession(session) != nil || session.ID != group.id ||
		session.ProjectID != group.projectID || session.Revision != group.revision || session.UpdatedAt != group.createdAt {
		report.addInvalid("invalid_session_revision", "session", group.id, group.action, group.revision)
		return
	}
	if len(group.audits) != 1 {
		code := "missing_audit_event"
		if len(group.audits) > 1 {
			code = "duplicate_audit_events"
		}
		report.addInvalid(code, "session", group.id, group.action, group.revision)
		return
	}
	audit := group.audits[0]
	if audit.action != group.action || audit.actor != group.actor || audit.reason != group.reason || audit.createdAt != group.createdAt {
		report.addInvalid("audit_history_mismatch", "session", group.id, group.action, group.revision)
		return
	}
	scope, exists := scopes[group.projectID]
	if !exists {
		report.addInvalid("missing_team_scope", "session", group.id, group.action, group.revision)
		return
	}
	if audit.attestation == "" {
		if auditStampBeforeScope(group.createdAt, scope.firstSeen) {
			report.UnauthenticatedBeforeScope++
			report.addFinding("unsigned_before_team_scope", "session", group.id, group.action, group.revision)
		} else {
			report.addInvalid("missing_authorization", "session", group.id, group.action, group.revision)
		}
		return
	}
	expectedAction, ok := teamAuditSessionMutationAction(group.action)
	if !ok {
		report.addInvalid("unknown_session_audit_action", "session", group.id, group.action, group.revision)
		return
	}
	if err := verifyTeamMutationEvidence(json.RawMessage(audit.attestation), audit.policySHA, expectedAction,
		group.action, "session", group.id, group.projectID, group.revision-1, session, group.actor, group.reason, group.createdAt, policies); err != nil {
		report.addInvalid("invalid_authorization", "session", group.id, group.action, group.revision)
		return
	}
	report.VerifiedAuthorizations++
}

func verifyTeamMutationEvidence(raw json.RawMessage, storedPolicySHA, action, auditAction, resourceType, resourceID,
	projectID string, expectedRevision int, entity any, actor, reason, eventStamp string,
	policies map[string]teamPolicySnapshot) error {
	if len(raw) == 0 || len(raw) > 16<<10 {
		return errors.New("authorization evidence is missing or oversized")
	}
	var attestation teamauth.MutationAttestation
	if err := decodeStrictJSON(string(raw), &attestation); err != nil {
		return fmt.Errorf("decode authorization evidence: %w", err)
	}
	if storedPolicySHA == "" {
		storedPolicySHA = attestation.PolicySHA256
	}
	if attestation.Subject != actor || attestation.PolicySHA256 != storedPolicySHA || !isSHA256Digest(storedPolicySHA) {
		return errors.New("authorization identity or retained policy reference does not match the audit record")
	}
	snapshot, ok := policies[storedPolicySHA]
	if !ok {
		return errors.New("authorization references a missing or invalid retained policy snapshot")
	}
	signedAt, err := time.Parse(time.RFC3339Nano, attestation.IssuedAt)
	if err != nil {
		return errors.New("authorization issued_at is invalid")
	}
	eventAt, err := time.Parse(time.RFC3339Nano, eventStamp)
	if err != nil {
		return errors.New("audit event timestamp is invalid")
	}
	issuanceDelta := signedAt.Sub(eventAt)
	if issuanceDelta < -teamauth.MutationFutureSkew || issuanceDelta > teamauth.MutationMaxAge {
		return errors.New("authorization signature was not issued near the recorded mutation time")
	}
	payloadSHA, err := teamauth.MutationPayloadSHA256(entity, reason)
	if err != nil {
		return err
	}
	claims := teamauth.MutationClaims{
		Action: action, AuditAction: auditAction, ProjectID: projectID, ResourceType: resourceType,
		ResourceID: resourceID, ExpectedRevision: expectedRevision, PayloadSHA256: payloadSHA,
	}
	return teamauth.VerifyMutation(attestation, snapshot.policy, storedPolicySHA, claims, time.Time{})
}

func teamAuditTaskMutationAction(auditAction string) (string, bool) {
	switch auditAction {
	case "created":
		return "task.create", true
	case "updated":
		return "task.update", true
	case "plan_revised":
		return "task.plan.revise", true
	case "plan_confirmed":
		return "task.plan.confirm", true
	case "requirements_revised", "requirements_question_added", "requirements_question_answered":
		return "task.requirements.update", true
	case "requirements_finalized":
		return "task.requirements.finalize", true
	case "requirements_reopened":
		return "task.requirements.reopen", true
	case "handoff_accepted":
		return "task.handoff.accept", true
	default:
		return "", false
	}
}

func teamAuditSessionMutationAction(auditAction string) (string, bool) {
	switch auditAction {
	case "started":
		return "session.start", true
	case "updated", "closed":
		return "session.update", true
	default:
		return "", false
	}
}

func auditStampBeforeScope(eventStamp string, scopeStamp time.Time) bool {
	eventTime, err := time.Parse(time.RFC3339Nano, eventStamp)
	return err == nil && eventTime.Before(scopeStamp)
}

func (report *TeamContinuityAuditReport) addInvalid(code, entity, resourceID, action string, revision int) {
	report.InvalidRecords++
	report.addFinding(code, entity, resourceID, action, revision)
}

func (report *TeamContinuityAuditReport) addFinding(code, entity, resourceID, action string, revision int) {
	if len(report.Findings) >= maxTeamAuditFindings {
		report.FindingsTruncated = true
		return
	}
	report.Findings = append(report.Findings, TeamContinuityAuditFinding{
		Code: code, Entity: entity, ResourceID: resourceID, Action: action, Revision: revision,
	})
}
