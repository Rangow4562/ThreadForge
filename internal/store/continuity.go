package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/pkg/continuity"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

const maxContinuityPage = 200

type ContinuityTaskCounts struct {
	Total      int `json:"total"`
	Open       int `json:"open"`
	InProgress int `json:"in_progress"`
	Blocked    int `json:"blocked"`
	Completed  int `json:"completed"`
	Abandoned  int `json:"abandoned"`
}

type TaskRevision struct {
	Task          continuity.Task `json:"task"`
	Action        string          `json:"action"`
	Actor         string          `json:"actor"`
	Reason        string          `json:"reason"`
	CreatedAt     string          `json:"created_at"`
	Authorization json.RawMessage `json:"authorization,omitempty"`
}

type SessionRevision struct {
	Session       continuity.Session `json:"session"`
	Action        string             `json:"action"`
	Actor         string             `json:"actor"`
	Reason        string             `json:"reason"`
	CreatedAt     string             `json:"created_at"`
	Authorization json.RawMessage    `json:"authorization,omitempty"`
}

func (s *Store) authorizeContinuityMutation(ctx context.Context, tx *sql.Tx, action, auditAction, resourceType, resourceID,
	projectID string, expectedRevision int, entity any, actor, reason string) (json.RawMessage, error) {
	payloadSHA256, err := teamauth.MutationPayloadSHA256(entity, reason)
	if err != nil {
		return nil, err
	}
	claims := teamauth.MutationClaims{
		Action: action, AuditAction: auditAction, ProjectID: projectID, ResourceType: resourceType,
		ResourceID: resourceID, ExpectedRevision: expectedRevision, PayloadSHA256: payloadSHA256,
	}
	proof, hasProof := teamauth.MutationProofFromContext(ctx)
	var organizationID string
	err = tx.QueryRowContext(ctx, `SELECT organization_id FROM team_project_scopes WHERE project_id = ?`, projectID).Scan(&organizationID)
	if errors.Is(err, sql.ErrNoRows) {
		if hasProof {
			return nil, errors.New("team authorization was supplied for a project without a registered team scope")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := acquireTeamProjectScopeWriteLock(ctx, tx, projectID); err != nil {
		return nil, err
	}
	decommissioned, err := teamProjectScopeIsDecommissioned(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	if decommissioned {
		return nil, errors.New("team project scope is decommissioned; ordinary continuity writes are frozen")
	}
	policy, _, sourceFileSHA256, err := appdata.ReadTeamTrustPolicy(s.teamPolicyRoot)
	if err != nil {
		return nil, fmt.Errorf("team-protected project requires its pinned trust policy: %w", err)
	}
	if policy.OrganizationID != organizationID {
		return nil, errors.New("installed team trust policy organization does not match the registered project scope")
	}
	policySHA256, err := teamauth.CanonicalPolicySHA256(policy)
	if err != nil {
		return nil, err
	}
	if !hasProof {
		return nil, errors.New("team-protected project mutation requires signed identity authorization")
	}
	if proof.PolicySHA256 != policySHA256 {
		return nil, errors.New("team authorization was created under a stale or different trust policy")
	}
	if actor != proof.Attestation.Subject {
		return nil, errors.New("team mutation audit actor does not match the signed subject")
	}
	if err := teamauth.VerifyMutation(proof.Attestation, policy, policySHA256, claims, time.Now().UTC()); err != nil {
		return nil, err
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO continuity_policy_snapshots(policy_sha256, organization_id, policy_json, source_file_sha256, captured_at)
		 VALUES(?, ?, ?, ?, ?)`, policySHA256, policy.OrganizationID, string(policyJSON), sourceFileSHA256, stamp); err != nil {
		return nil, fmt.Errorf("retain verified team policy snapshot: %w", err)
	}
	var storedOrganization, storedPolicyJSON, storedFileSHA256 string
	if err := tx.QueryRowContext(ctx,
		`SELECT organization_id, policy_json, source_file_sha256 FROM continuity_policy_snapshots WHERE policy_sha256 = ?`, policySHA256).
		Scan(&storedOrganization, &storedPolicyJSON, &storedFileSHA256); err != nil {
		return nil, err
	}
	if storedOrganization != policy.OrganizationID || storedPolicyJSON != string(policyJSON) || !isSHA256Digest(storedFileSHA256) {
		return nil, errors.New("retained team policy snapshot conflicts with the canonical verified policy")
	}
	return json.Marshal(proof.Attestation)
}

func (s *Store) TaskCounts(ctx context.Context, projectID string) (ContinuityTaskCounts, error) {
	var counts ContinuityTaskCounts
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 {
		return counts, errors.New("project_id is required and cannot exceed 256 characters")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM continuity_tasks WHERE project_id = ? GROUP BY status`, projectID)
	if err != nil {
		return counts, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var total int
		if err := rows.Scan(&status, &total); err != nil {
			return counts, err
		}
		counts.Total += total
		switch continuity.TaskStatus(status) {
		case continuity.TaskOpen:
			counts.Open = total
		case continuity.TaskInProgress:
			counts.InProgress = total
		case continuity.TaskBlocked:
			counts.Blocked = total
		case continuity.TaskCompleted:
			counts.Completed = total
		case continuity.TaskAbandoned:
			counts.Abandoned = total
		default:
			return counts, fmt.Errorf("stored task has unknown status %q", status)
		}
	}
	return counts, rows.Err()
}

func (s *Store) CreateTask(ctx context.Context, task continuity.Task, actor, reason string) error {
	if task.Owner == "" || task.Owner != actor {
		return errors.New("new task owner must match its authenticated creator")
	}
	if task.Revision != 1 || task.Status == continuity.TaskCompleted || task.Status == continuity.TaskAbandoned {
		return errors.New("new tasks must start at revision 1 in a non-terminal status")
	}
	if task.Plan != nil {
		return errors.New("new tasks cannot skip the explicit plan revision and confirmation lifecycle")
	}
	if err := continuity.ValidateTask(task); err != nil {
		return err
	}
	if err := validateContinuityAudit(actor, reason); err != nil {
		return err
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	stamp := task.UpdatedAt
	return s.withTx(ctx, func(tx *sql.Tx) error {
		authorization, err := s.authorizeContinuityMutation(ctx, tx, "task.create", "created", "task", task.ID,
			task.ProjectID, 0, task, actor, reason)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO continuity_tasks(id, project_id, revision, status, task_json, updated_at) VALUES(?, ?, ?, ?, ?, ?)`,
			task.ID, task.ProjectID, task.Revision, task.Status, string(payload), stamp); err != nil {
			return fmt.Errorf("create task: %w", err)
		}
		if err := insertTaskHistory(ctx, tx, task, "created", actor, reason); err != nil {
			return err
		}
		return insertContinuityAudit(ctx, tx, "task", task.ID, task.Revision, "created", actor, reason, stamp, authorization)
	})
}

func (s *Store) GetTask(ctx context.Context, id string) (continuity.Task, error) {
	return readTask(ctx, s.db, id)
}

func (s *Store) TaskHistory(ctx context.Context, id string) ([]TaskRevision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT h.revision, h.action, h.actor, h.reason, h.created_at, h.task_json, COALESCE(a.attestation_json, '')
		 FROM continuity_task_history h
		 LEFT JOIN continuity_audit c ON c.entity = 'task' AND c.entity_id = h.task_id AND c.revision = h.revision
		 LEFT JOIN continuity_authorizations a ON a.sequence = c.sequence
		 WHERE h.task_id = ? ORDER BY h.revision`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TaskRevision, 0)
	expectedRevision := 1
	projectID := ""
	for rows.Next() {
		var revision int
		var item TaskRevision
		var payload string
		var authorization string
		if err := rows.Scan(&revision, &item.Action, &item.Actor, &item.Reason, &item.CreatedAt, &payload, &authorization); err != nil {
			return nil, err
		}
		item.Authorization = json.RawMessage(authorization)
		task, err := decodeTask(payload)
		if err != nil {
			return nil, err
		}
		if task.ID != id || task.Revision != revision || revision != expectedRevision {
			return nil, errors.New("task revision history is not contiguous")
		}
		if projectID != "" && projectID != task.ProjectID {
			return nil, errors.New("task project scope changed in revision history")
		}
		projectID = task.ProjectID
		item.Task = task
		result = append(result, item)
		expectedRevision++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, ErrTaskNotFound
	}
	return result, nil
}

func (s *Store) UpdateTask(ctx context.Context, task continuity.Task, expectedRevision int, actor, reason string) error {
	return s.updateTask(ctx, task, expectedRevision, actor, reason, "updated")
}

func (s *Store) ReviseTaskPlan(ctx context.Context, task continuity.Task, expectedRevision int, actor, reason string) error {
	return s.updateTask(ctx, task, expectedRevision, actor, reason, "plan_revised")
}

func (s *Store) ConfirmTaskPlan(ctx context.Context, task continuity.Task, expectedRevision int, actor, reason string) error {
	return s.updateTask(ctx, task, expectedRevision, actor, reason, "plan_confirmed")
}

func (s *Store) ChangeTaskRequirements(ctx context.Context, task continuity.Task, expectedRevision int, action, actor, reason string) error {
	switch action {
	case "requirements_revised", "requirements_question_added", "requirements_question_answered", "requirements_finalized", "requirements_reopened":
		return s.updateTask(ctx, task, expectedRevision, actor, reason, action)
	default:
		return errors.New("unsupported task requirements action")
	}
}

func (s *Store) updateTask(ctx context.Context, task continuity.Task, expectedRevision int, actor, reason, action string) error {
	if err := continuity.ValidateTask(task); err != nil {
		return err
	}
	if err := validateContinuityAudit(actor, reason); err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		current, err := readTask(ctx, tx, task.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision || task.Revision != expectedRevision+1 {
			return ErrContinuityConflict
		}
		if current.ProjectID != task.ProjectID || current.CreatedAt != task.CreatedAt || current.Owner != task.Owner {
			return errors.New("task project_id, owner, and created_at are immutable outside handoff")
		}
		if action == "updated" {
			if task.Title != current.Title || task.Objective != current.Objective ||
				!reflect.DeepEqual(task.AcceptanceCriteria, current.AcceptanceCriteria) ||
				!reflect.DeepEqual(task.Plan, current.Plan) || !reflect.DeepEqual(task.Requirements, current.Requirements) {
				return errors.New("task definition, plan, and requirements require their dedicated revision workflows")
			}
		} else if action == "requirements_revised" {
			expectedTask := current
			expectedTask.Plan = taskPlanAfterRequirementsChange(current.Plan)
			unchanged := task
			unchanged.Objective = current.Objective
			unchanged.AcceptanceCriteria = current.AcceptanceCriteria
			unchanged.Requirements = current.Requirements
			unchanged.Revision = current.Revision
			unchanged.UpdatedAt = current.UpdatedAt
			if !reflect.DeepEqual(expectedTask, unchanged) {
				return errors.New("requirements revisions may only change the objective and acceptance criteria")
			}
			if current.Objective == task.Objective && reflect.DeepEqual(current.AcceptanceCriteria, task.AcceptanceCriteria) {
				return errors.New("requirements revision must change the objective or acceptance criteria")
			}
		} else if strings.HasPrefix(action, "requirements_") {
			expectedTask := current
			expectedTask.Plan = taskPlanAfterRequirementsChange(current.Plan)
			unchanged := task
			unchanged.Requirements = current.Requirements
			unchanged.Revision = current.Revision
			unchanged.UpdatedAt = current.UpdatedAt
			if !reflect.DeepEqual(expectedTask, unchanged) {
				return errors.New("requirements operations may not change task, plan, or progress fields")
			}
		} else {
			unchanged := task
			unchanged.Plan = current.Plan
			unchanged.Revision = current.Revision
			unchanged.UpdatedAt = current.UpdatedAt
			if !reflect.DeepEqual(current, unchanged) {
				return errors.New("plan operations may not change task status or progress fields")
			}
		}
		if action != "updated" && (current.Status == continuity.TaskCompleted || current.Status == continuity.TaskAbandoned) {
			return errors.New("terminal tasks cannot revise plans or requirements")
		}
		if strings.HasPrefix(action, "requirements_") {
			if err := validateTaskRequirementsMutation(current.Requirements, task.Requirements, action, actor, reason); err != nil {
				return err
			}
		} else {
			if action == "plan_confirmed" && task.Plan != nil && task.Plan.Decision == continuity.PlanGovernedExecution &&
				(task.Requirements == nil || task.Requirements.Status != continuity.RequirementsFinalized) {
				return errors.New("governed execution requires finalized task requirements")
			}
			if err := validateTaskPlanMutation(current.Plan, task.Plan, action, actor, reason); err != nil {
				return err
			}
		}
		if !taskStatusTransitionAllowed(current.Status, task.Status) {
			return fmt.Errorf("invalid task status transition from %s to %s", current.Status, task.Status)
		}
		mutationAction, err := taskMutationAction(action)
		if err != nil {
			return err
		}
		authorization, err := s.authorizeContinuityMutation(ctx, tx, mutationAction, action, "task", task.ID,
			task.ProjectID, expectedRevision, task, actor, reason)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(task)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE continuity_tasks SET revision = ?, status = ?, task_json = ?, updated_at = ? WHERE id = ? AND revision = ?`,
			task.Revision, task.Status, string(payload), task.UpdatedAt, task.ID, expectedRevision)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrContinuityConflict
		}
		if err := insertTaskHistory(ctx, tx, task, action, actor, reason); err != nil {
			return err
		}
		return insertContinuityAudit(ctx, tx, "task", task.ID, task.Revision, action, actor, reason, task.UpdatedAt, authorization)
	})
}

func taskPlanAfterRequirementsChange(current *continuity.TaskPlan) *continuity.TaskPlan {
	if current == nil {
		return nil
	}
	next := *current
	if next.Decision != continuity.PlanUnconfirmed {
		next.Decision = continuity.PlanUnconfirmed
		next.ConfirmedBy = ""
		next.ConfirmedAt = ""
		next.ConfirmReason = ""
	}
	return &next
}

func taskMutationAction(auditAction string) (string, error) {
	switch auditAction {
	case "created":
		return "task.create", nil
	case "updated":
		return "task.update", nil
	case "plan_revised":
		return "task.plan.revise", nil
	case "plan_confirmed":
		return "task.plan.confirm", nil
	case "requirements_revised", "requirements_question_added", "requirements_question_answered":
		return "task.requirements.update", nil
	case "requirements_finalized":
		return "task.requirements.finalize", nil
	case "requirements_reopened":
		return "task.requirements.reopen", nil
	default:
		return "", errors.New("continuity task audit action has no authorization mapping")
	}
}

func validateTaskRequirementsMutation(current, next *continuity.TaskRequirements, action, actor, reason string) error {
	defaultRequirements := continuity.TaskRequirements{
		SchemaVersion: continuity.RequirementsSchemaVersion,
		Status:        continuity.RequirementsDraft,
		Questions:     []continuity.RequirementQuestion{},
	}
	currentValue := defaultRequirements
	if current != nil {
		currentValue = *current
	}
	if next == nil {
		return errors.New("task requirements state cannot be removed")
	}
	if err := continuity.ValidateTaskRequirements(*next); err != nil {
		return err
	}
	if currentValue.Status == continuity.RequirementsFinalized && action != "requirements_reopened" {
		return errors.New("finalized task requirements must be explicitly reopened before changes")
	}
	switch action {
	case "requirements_revised":
		if !reflect.DeepEqual(currentValue, *next) {
			return errors.New("requirements content revisions cannot alter clarification or finalization state")
		}
	case "requirements_question_added":
		if len(next.Questions) != len(currentValue.Questions)+1 || next.Status != continuity.RequirementsClarifying ||
			next.FinalizedBy != "" || next.FinalizedAt != "" || next.FinalizeReason != "" {
			return errors.New("adding a clarification must append one question and enter clarifying state")
		}
		for index, question := range currentValue.Questions {
			if !reflect.DeepEqual(question, next.Questions[index]) {
				return errors.New("adding a clarification cannot change existing questions")
			}
		}
		added := next.Questions[len(next.Questions)-1]
		if added.AskedBy != actor || added.Answer != "" || added.AnsweredBy != "" || added.AnsweredAt != "" {
			return errors.New("new clarification metadata must match the audited actor and begin unanswered")
		}
	case "requirements_question_answered":
		if len(next.Questions) != len(currentValue.Questions) || next.Status == continuity.RequirementsFinalized ||
			next.FinalizedBy != "" || next.FinalizedAt != "" || next.FinalizeReason != "" {
			return errors.New("answering a clarification cannot change the question set or finalize requirements")
		}
		changed := 0
		for index, old := range currentValue.Questions {
			updated := next.Questions[index]
			if reflect.DeepEqual(old, updated) {
				continue
			}
			changed++
			if old.Answer != "" || old.ID != updated.ID || old.Question != updated.Question ||
				old.AskedBy != updated.AskedBy || old.AskedAt != updated.AskedAt ||
				updated.Answer == "" || updated.AnsweredBy != actor || updated.AnsweredAt == "" {
				return errors.New("clarification answers are append-only and must identify the answering actor")
			}
		}
		if changed != 1 {
			return errors.New("answering a clarification must resolve exactly one unanswered question")
		}
		open := false
		for _, question := range next.Questions {
			open = open || question.Answer == ""
		}
		expectedStatus := continuity.RequirementsDraft
		if open {
			expectedStatus = continuity.RequirementsClarifying
		}
		if next.Status != expectedStatus {
			return errors.New("requirements status must reflect whether unanswered questions remain")
		}
	case "requirements_finalized":
		if !reflect.DeepEqual(currentValue.Questions, next.Questions) || next.Status != continuity.RequirementsFinalized ||
			next.FinalizedBy != actor || next.FinalizeReason != reason {
			return errors.New("finalization must preserve clarification history and match the audited reviewer and reason")
		}
		for _, question := range next.Questions {
			if question.Answer == "" {
				return errors.New("requirements cannot be finalized while questions remain unanswered")
			}
		}
	case "requirements_reopened":
		if currentValue.Status != continuity.RequirementsFinalized || !reflect.DeepEqual(currentValue.Questions, next.Questions) ||
			next.Status != continuity.RequirementsDraft || next.FinalizedBy != "" || next.FinalizedAt != "" || next.FinalizeReason != "" {
			return errors.New("reopening must preserve clarification history and clear finalization metadata")
		}
	default:
		return errors.New("unsupported task requirements mutation action")
	}
	return nil
}

func validateTaskPlanMutation(current, next *continuity.TaskPlan, action, actor, reason string) error {
	switch action {
	case "updated":
		if !reflect.DeepEqual(current, next) {
			return errors.New("task plan changes require the dedicated revise or confirm operation")
		}
	case "plan_revised":
		if next == nil || next.Decision != continuity.PlanUnconfirmed || next.ConfirmedBy != "" || next.ConfirmedAt != "" || next.ConfirmReason != "" {
			return errors.New("revised task plans must be unconfirmed and contain no confirmation metadata")
		}
		if current == nil {
			if next.Version != 1 {
				return errors.New("the first task plan must have version 1")
			}
		} else if next.Version != current.Version+1 {
			return errors.New("task plan revision must increment the plan version by exactly one")
		}
	case "plan_confirmed":
		if current == nil || next == nil || current.Version != next.Version {
			return errors.New("confirming a task requires the current plan without revising its version")
		}
		if next.ConfirmedBy != actor || next.ConfirmReason != reason {
			return errors.New("plan confirmation metadata must match the audited actor and reason")
		}
		unchanged := *next
		unchanged.Decision = current.Decision
		unchanged.ConfirmedBy = current.ConfirmedBy
		unchanged.ConfirmedAt = current.ConfirmedAt
		unchanged.ConfirmReason = current.ConfirmReason
		if !reflect.DeepEqual(current, &unchanged) {
			return errors.New("plan confirmation cannot change plan content or implementation scope")
		}
		allowed := current.Decision == continuity.PlanUnconfirmed &&
			(next.Decision == continuity.PlanOnly || next.Decision == continuity.PlanGovernedExecution)
		allowed = allowed || current.Decision == continuity.PlanOnly && next.Decision == continuity.PlanGovernedExecution
		if !allowed {
			return errors.New("task plan decision cannot be changed through confirmation; revise the plan first")
		}
	default:
		return errors.New("unsupported task plan mutation action")
	}
	return nil
}

func (s *Store) ListTasks(ctx context.Context, projectID string, limit, offset int) ([]continuity.Task, int, error) {
	if strings.TrimSpace(projectID) == "" || limit < 1 || limit > maxContinuityPage || offset < 0 {
		return nil, 0, errors.New("project_id is required, limit must be 1-200, and offset must be non-negative")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_tasks WHERE project_id = ?`, projectID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_json FROM continuity_tasks WHERE project_id = ? ORDER BY updated_at DESC, id LIMIT ? OFFSET ?`,
		projectID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]continuity.Task, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, 0, err
		}
		task, err := decodeTask(payload)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, task)
	}
	return result, total, rows.Err()
}

func (s *Store) StartSession(ctx context.Context, session continuity.Session, actor, reason string) error {
	if session.Revision != 1 || session.Status != continuity.SessionActive || session.EndedAt != "" {
		return errors.New("new sessions must start active at revision 1")
	}
	if err := continuity.ValidateSession(session); err != nil {
		return err
	}
	if err := validateContinuityAudit(actor, reason); err != nil {
		return err
	}
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		task, err := readTask(ctx, tx, session.TaskID)
		if err != nil {
			return err
		}
		if task.ProjectID != session.ProjectID {
			return errors.New("session project_id does not match its task")
		}
		if task.Status == continuity.TaskCompleted || task.Status == continuity.TaskAbandoned {
			return errors.New("cannot start a session for a terminal task")
		}
		authorization, err := s.authorizeContinuityMutation(ctx, tx, "session.start", "started", "session", session.ID,
			session.ProjectID, 0, session, actor, reason)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO continuity_sessions(id, task_id, project_id, revision, status, session_json, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
			session.ID, session.TaskID, session.ProjectID, session.Revision, session.Status, string(payload), session.UpdatedAt); err != nil {
			return fmt.Errorf("start session: %w", err)
		}
		if err := insertSessionHistory(ctx, tx, session, "started", actor, reason); err != nil {
			return err
		}
		return insertContinuityAudit(ctx, tx, "session", session.ID, session.Revision, "started", actor, reason, session.UpdatedAt, authorization)
	})
}

func (s *Store) GetSession(ctx context.Context, id string) (continuity.Session, error) {
	return readSession(ctx, s.db, id)
}

func (s *Store) SessionHistory(ctx context.Context, id string) ([]SessionRevision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT h.revision, h.action, h.actor, h.reason, h.created_at, h.session_json, COALESCE(a.attestation_json, '')
		 FROM continuity_session_history h
		 LEFT JOIN continuity_audit c ON c.entity = 'session' AND c.entity_id = h.session_id AND c.revision = h.revision
		 LEFT JOIN continuity_authorizations a ON a.sequence = c.sequence
		 WHERE h.session_id = ? ORDER BY h.revision`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SessionRevision, 0)
	expectedRevision := 1
	taskID := ""
	for rows.Next() {
		var revision int
		var item SessionRevision
		var payload string
		var authorization string
		if err := rows.Scan(&revision, &item.Action, &item.Actor, &item.Reason, &item.CreatedAt, &payload, &authorization); err != nil {
			return nil, err
		}
		item.Authorization = json.RawMessage(authorization)
		session, err := decodeSession(payload)
		if err != nil {
			return nil, err
		}
		if session.ID != id || session.Revision != revision || revision != expectedRevision {
			return nil, errors.New("session revision history is not contiguous")
		}
		if taskID != "" && taskID != session.TaskID {
			return nil, errors.New("session task scope changed in revision history")
		}
		taskID = session.TaskID
		item.Session = session
		result = append(result, item)
		expectedRevision++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, ErrSessionNotFound
	}
	return result, nil
}

func (s *Store) UpdateSession(ctx context.Context, session continuity.Session, expectedRevision int, actor, reason string) error {
	if err := continuity.ValidateSession(session); err != nil {
		return err
	}
	if err := validateContinuityAudit(actor, reason); err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		current, err := readSession(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision || session.Revision != expectedRevision+1 {
			return ErrContinuityConflict
		}
		if current.TaskID != session.TaskID || current.ProjectID != session.ProjectID || current.Harness != session.Harness || current.StartedAt != session.StartedAt {
			return errors.New("session identity, scope, harness, and started_at are immutable")
		}
		if current.Status == continuity.SessionClosed && session.Status != continuity.SessionClosed {
			return errors.New("closed sessions cannot be reopened")
		}
		if current.Status == continuity.SessionClosed && session.EndedAt != current.EndedAt {
			return errors.New("session ended_at is immutable after closure")
		}
		payload, err := json.Marshal(session)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE continuity_sessions SET revision = ?, status = ?, session_json = ?, updated_at = ? WHERE id = ? AND revision = ?`,
			session.Revision, session.Status, string(payload), session.UpdatedAt, session.ID, expectedRevision)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrContinuityConflict
		}
		action := "updated"
		if current.Status != continuity.SessionClosed && session.Status == continuity.SessionClosed {
			action = "closed"
		}
		authorization, err := s.authorizeContinuityMutation(ctx, tx, "session.update", action, "session", session.ID,
			session.ProjectID, expectedRevision, session, actor, reason)
		if err != nil {
			return err
		}
		if err := insertSessionHistory(ctx, tx, session, action, actor, reason); err != nil {
			return err
		}
		return insertContinuityAudit(ctx, tx, "session", session.ID, session.Revision, action, actor, reason, session.UpdatedAt, authorization)
	})
}

func (s *Store) ListSessions(ctx context.Context, taskID string) ([]continuity.Session, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("task_id is required")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_json FROM continuity_sessions WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]continuity.Session, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		session, err := decodeSession(payload)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAt != result[j].StartedAt {
			return result[i].StartedAt < result[j].StartedAt
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

// TaskResumeContext returns one consistent read snapshot for explicit cross-session
// continuity. It never writes task/session state or grants execution authority.
func (s *Store) TaskResumeContext(ctx context.Context, taskID, sessionID string, maxEntries int) (continuity.TaskResumeContext, error) {
	if strings.TrimSpace(taskID) == "" || maxEntries < 1 || maxEntries > 100 {
		return continuity.TaskResumeContext{}, errors.New("task_id is required and max_entries must be 1-100")
	}
	var snapshot continuity.TaskResumeContext
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		snapshot, err = taskResumeContextInTx(ctx, tx, taskID, sessionID, maxEntries)
		return err
	})
	return snapshot, err
}

// DiscoverTaskResumeContext selects a project task only when exactly one task
// is explicitly in progress or has an active session. Blocked and terminal tasks
// are never automatically selected. The count and resulting snapshot share one
// database transaction so a concurrent task mutation cannot change the selection.
func (s *Store) DiscoverTaskResumeContext(ctx context.Context, projectID string, maxEntries int) (continuity.TaskResumeDiscovery, error) {
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 || maxEntries < 1 || maxEntries > 100 {
		return continuity.TaskResumeDiscovery{}, errors.New("project_id is required, project_id cannot exceed 256 characters, and max_entries must be 1-100")
	}
	discovery := continuity.TaskResumeDiscovery{
		SchemaVersion: continuity.TaskResumeDiscoverySchemaVersion,
		Status:        continuity.TaskResumeDiscoveryNoActiveTask,
		ActionReady:   false, GuidanceOnly: true, ExecutionAuthorized: false,
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		const eligible = `(t.status = ? OR (t.status = ? AND EXISTS (
			SELECT 1 FROM continuity_sessions s
			WHERE s.task_id = t.id AND s.project_id = t.project_id AND s.status = ?
		)))`
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM continuity_tasks t WHERE t.project_id = ? AND `+eligible,
			projectID, continuity.TaskInProgress, continuity.TaskOpen, continuity.SessionActive).Scan(&discovery.CandidateCount); err != nil {
			return err
		}
		if discovery.CandidateCount == 0 {
			return nil
		}
		if discovery.CandidateCount > 1 {
			discovery.Status = continuity.TaskResumeDiscoverySelectionRequired
			return nil
		}
		var indexedStatus, payload string
		if err := tx.QueryRowContext(ctx,
			`SELECT t.status, t.task_json FROM continuity_tasks t WHERE t.project_id = ? AND `+eligible+
				` ORDER BY t.updated_at DESC, t.id LIMIT 1`,
			projectID, continuity.TaskInProgress, continuity.TaskOpen, continuity.SessionActive).Scan(&indexedStatus, &payload); err != nil {
			return err
		}
		task, err := decodeTask(payload)
		if err != nil {
			return err
		}
		if task.ProjectID != projectID || string(task.Status) != indexedStatus ||
			(task.Status != continuity.TaskInProgress && task.Status != continuity.TaskOpen) {
			return errors.New("stored task index does not match project-scoped discovery eligibility")
		}
		resume, err := taskResumeContextInTx(ctx, tx, task.ID, "", maxEntries)
		if err != nil {
			return err
		}
		discovery.Status = continuity.TaskResumeDiscoveryAvailable
		discovery.Context = &resume
		return nil
	})
	return discovery, err
}

func taskResumeContextInTx(ctx context.Context, tx *sql.Tx, taskID, sessionID string, maxEntries int) (continuity.TaskResumeContext, error) {
	task, err := readTask(ctx, tx, taskID)
	if err != nil {
		return continuity.TaskResumeContext{}, err
	}
	if task.Plan != nil && task.Plan.Decision == continuity.PlanOnly {
		// Preserve explicit session-reference validation, but avoid reading or
		// returning any session progress or handoff records in plan-only mode.
		if strings.TrimSpace(sessionID) != "" {
			session, err := readSession(ctx, tx, sessionID)
			if err != nil {
				return continuity.TaskResumeContext{}, err
			}
			if session.TaskID != task.ID || session.ProjectID != task.ProjectID {
				return continuity.TaskResumeContext{}, errors.New("session_id does not belong to task_id")
			}
		}
		return continuity.NewTaskResumeContext(task, nil, []continuity.Session{}, []continuity.Entry{}, false, false), nil
	}
	var selected *continuity.Session
	choices := []continuity.Session{}
	if strings.TrimSpace(sessionID) != "" {
		session, err := readSession(ctx, tx, sessionID)
		if err != nil {
			return continuity.TaskResumeContext{}, err
		}
		if session.TaskID != task.ID || session.ProjectID != task.ProjectID {
			return continuity.TaskResumeContext{}, errors.New("session_id does not belong to task_id")
		}
		selected = &session
	} else {
		active, err := taskSessionsByStatus(ctx, tx, task.ID, continuity.SessionActive, 3)
		if err != nil {
			return continuity.TaskResumeContext{}, err
		}
		if len(active) > 1 {
			choicesTruncated := len(active) > 2
			if choicesTruncated {
				active = active[:2]
			}
			return continuity.NewTaskResumeContext(task, nil, active, nil, false, choicesTruncated), nil
		}
		if len(active) == 1 {
			selected = &active[0]
		} else {
			latest, err := latestTaskSession(ctx, tx, task.ID)
			if err != nil && !errors.Is(err, ErrSessionNotFound) {
				return continuity.TaskResumeContext{}, err
			}
			if err == nil {
				selected = &latest
			}
		}
	}
	selectedSessionID := ""
	if selected != nil {
		selectedSessionID = selected.ID
	}
	entries, truncated, err := taskResumeEntries(ctx, tx, task.ID, selectedSessionID, maxEntries)
	if err != nil {
		return continuity.TaskResumeContext{}, err
	}
	return continuity.NewTaskResumeContext(task, selected, choices, entries, truncated, false), nil
}

func taskSessionsByStatus(ctx context.Context, tx *sql.Tx, taskID string, status continuity.SessionStatus, limit int) ([]continuity.Session, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT session_json FROM continuity_sessions WHERE task_id = ? AND status = ? ORDER BY updated_at DESC, id DESC LIMIT ?`,
		taskID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]continuity.Session, 0, limit)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		session, err := decodeSession(payload)
		if err != nil {
			return nil, err
		}
		if session.TaskID != taskID || session.Status != status {
			return nil, errors.New("stored session index does not match its payload")
		}
		result = append(result, session)
	}
	return result, rows.Err()
}

func latestTaskSession(ctx context.Context, tx *sql.Tx, taskID string) (continuity.Session, error) {
	var payload string
	err := tx.QueryRowContext(ctx,
		`SELECT session_json FROM continuity_sessions WHERE task_id = ? ORDER BY updated_at DESC, id DESC LIMIT 1`,
		taskID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return continuity.Session{}, ErrSessionNotFound
	}
	if err != nil {
		return continuity.Session{}, err
	}
	session, err := decodeSession(payload)
	if err != nil {
		return continuity.Session{}, err
	}
	if session.TaskID != taskID {
		return continuity.Session{}, errors.New("stored session index does not match its payload")
	}
	return session, nil
}

func taskResumeEntries(ctx context.Context, tx *sql.Tx, taskID, sessionID string, limit int) ([]continuity.Entry, bool, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT entry_json FROM continuity_entries
		 WHERE task_id = ? AND (COALESCE(session_id, '') = '' OR COALESCE(session_id, '') = ?)
		 ORDER BY sequence DESC LIMIT ?`, taskID, sessionID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]continuity.Entry, 0, limit+1)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, false, err
		}
		var entry continuity.Entry
		if err := decodeStrictJSON(payload, &entry); err != nil {
			return nil, false, fmt.Errorf("decode stored continuity entry: %w", err)
		}
		if err := continuity.ValidateEntry(entry); err != nil || entry.TaskID != taskID || (entry.SessionID != "" && entry.SessionID != sessionID) {
			return nil, false, errors.New("stored continuity entry does not match the resume context scope")
		}
		// The resume context is a reader projection; omit authorization attestations.
		entry.Authorization = nil
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(result) > limit
	if truncated {
		result = result[:limit]
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, truncated, nil
}

func (s *Store) AppendContinuityEntry(ctx context.Context, entry continuity.Entry) (int64, error) {
	if err := continuity.ValidateEntry(entry); err != nil {
		return 0, err
	}
	if len(entry.Authorization) != 0 {
		return 0, errors.New("continuity entry authorization must be supplied by the verified team context")
	}
	var sequence int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		task, err := readTask(ctx, tx, entry.TaskID)
		if err != nil {
			return err
		}
		if task.ProjectID != entry.ProjectID {
			return errors.New("continuity entry project_id does not match its task")
		}
		if entry.SessionID != "" {
			session, err := readSession(ctx, tx, entry.SessionID)
			if err != nil {
				return err
			}
			if session.TaskID != entry.TaskID || session.ProjectID != entry.ProjectID {
				return errors.New("continuity entry session does not belong to its task and project")
			}
		}
		mutationAction, auditAction, resourceType, resourceID, expectedRevision :=
			"continuity.entry.append", "appended", "entry", entry.ID, 0
		if entry.Kind == continuity.EntryReview || entry.Kind == continuity.EntryVerification {
			mutationAction = "continuity.review.append"
		}
		if entry.Kind == continuity.EntryTaskClaim {
			if err := s.validateTaskClaimMutation(ctx, tx, entry, task); err != nil {
				return err
			}
			var ok bool
			mutationAction, ok = taskClaimAction(entry.TaskClaim.Action)
			if !ok {
				return errors.New("unsupported task claim action")
			}
			auditAction, resourceType, resourceID, expectedRevision = entry.TaskClaim.Action, "task", task.ID, entry.TaskClaim.ExpectedRevision
		}
		if entry.Kind == continuity.EntryTaskHandoff {
			if err := s.validateTaskHandoffMutation(ctx, tx, entry, task); err != nil {
				return err
			}
		}
		authorization, err := s.authorizeContinuityMutation(ctx, tx, mutationAction, auditAction, resourceType, resourceID,
			entry.ProjectID, expectedRevision, entry, entry.Actor, continuity.EntryAppendAuthorizationReason)
		if err != nil {
			return err
		}
		entry.Authorization = authorization
		payload, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			`INSERT INTO continuity_entries(id, project_id, task_id, session_id, kind, actor, created_at, entry_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			entry.ID, entry.ProjectID, entry.TaskID, entry.SessionID, entry.Kind, entry.Actor, entry.CreatedAt, string(payload))
		if err != nil {
			return fmt.Errorf("append continuity entry: %w", err)
		}
		sequence, err = result.LastInsertId()
		if err != nil {
			return err
		}
		return nil
	})
	return sequence, err
}

func (s *Store) ContinuityEntries(ctx context.Context, taskID string, limit int) ([]continuity.Entry, error) {
	if strings.TrimSpace(taskID) == "" || limit < 1 || limit > 1000 {
		return nil, errors.New("task_id is required and limit must be 1-1000")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_json FROM continuity_entries WHERE task_id = ? ORDER BY sequence DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]continuity.Entry, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var entry continuity.Entry
		if err := json.Unmarshal([]byte(payload), &entry); err != nil {
			return nil, fmt.Errorf("decode continuity entry: %w", err)
		}
		if err := continuity.ValidateEntry(entry); err != nil {
			return nil, fmt.Errorf("stored continuity entry is invalid: %w", err)
		}
		result = append(result, entry)
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, rows.Err()
}

func (s *Store) ContinuityAudit(ctx context.Context, entity, id string) ([]continuity.AuditEvent, error) {
	if (entity != "task" && entity != "session") || strings.TrimSpace(id) == "" {
		return nil, errors.New("entity must be task or session and id is required")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.sequence, c.entity, c.entity_id, c.revision, c.action, c.actor, c.reason, c.created_at, COALESCE(a.attestation_json, '')
		 FROM continuity_audit c LEFT JOIN continuity_authorizations a ON a.sequence = c.sequence
		 WHERE c.entity = ? AND c.entity_id = ? ORDER BY c.sequence`, entity, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]continuity.AuditEvent, 0)
	for rows.Next() {
		var event continuity.AuditEvent
		var authorization string
		if err := rows.Scan(&event.Sequence, &event.Entity, &event.EntityID, &event.Revision, &event.Action, &event.Actor, &event.Reason, &event.CreatedAt, &authorization); err != nil {
			return nil, err
		}
		event.Authorization = json.RawMessage(authorization)
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) VerifyContinuityAuthorization(ctx context.Context, attestation teamauth.MutationAttestation,
	claims teamauth.MutationClaims) error {
	if !isSHA256Digest(attestation.PolicySHA256) {
		return errors.New("continuity attestation has an invalid policy digest")
	}
	var organizationID, policyJSON, sourceFileSHA256 string
	err := s.db.QueryRowContext(ctx,
		`SELECT organization_id, policy_json, source_file_sha256 FROM continuity_policy_snapshots WHERE policy_sha256 = ?`,
		attestation.PolicySHA256).Scan(&organizationID, &policyJSON, &sourceFileSHA256)
	if err != nil {
		return fmt.Errorf("load retained continuity trust snapshot: %w", err)
	}
	if !isSHA256Digest(sourceFileSHA256) {
		return errors.New("retained continuity trust snapshot has an invalid source-file digest")
	}
	var policy teamauth.Policy
	if err := decodeStrictJSON(policyJSON, &policy); err != nil {
		return fmt.Errorf("decode retained continuity trust snapshot: %w", err)
	}
	if policy.OrganizationID != organizationID {
		return errors.New("retained continuity trust snapshot organization is inconsistent")
	}
	return teamauth.VerifyMutation(attestation, policy, attestation.PolicySHA256, claims, time.Time{})
}

func readTask(ctx context.Context, q queryer, id string) (continuity.Task, error) {
	var payload string
	err := q.QueryRowContext(ctx, `SELECT task_json FROM continuity_tasks WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return continuity.Task{}, ErrTaskNotFound
	}
	if err != nil {
		return continuity.Task{}, err
	}
	return decodeTask(payload)
}

func readSession(ctx context.Context, q queryer, id string) (continuity.Session, error) {
	var payload string
	err := q.QueryRowContext(ctx, `SELECT session_json FROM continuity_sessions WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return continuity.Session{}, ErrSessionNotFound
	}
	if err != nil {
		return continuity.Session{}, err
	}
	return decodeSession(payload)
}

func decodeTask(payload string) (continuity.Task, error) {
	var task continuity.Task
	if err := decodeStrictJSON(payload, &task); err != nil {
		return continuity.Task{}, fmt.Errorf("decode stored task: %w", err)
	}
	if err := continuity.ValidateTask(task); err != nil {
		return continuity.Task{}, fmt.Errorf("stored task is invalid: %w", err)
	}
	return task, nil
}

func decodeSession(payload string) (continuity.Session, error) {
	var session continuity.Session
	if err := decodeStrictJSON(payload, &session); err != nil {
		return continuity.Session{}, fmt.Errorf("decode stored session: %w", err)
	}
	if err := continuity.ValidateSession(session); err != nil {
		return continuity.Session{}, fmt.Errorf("stored session is invalid: %w", err)
	}
	return session, nil
}

func decodeStrictJSON(payload string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func taskStatusTransitionAllowed(previous, next continuity.TaskStatus) bool {
	if previous == next {
		return true
	}
	if previous == continuity.TaskCompleted || previous == continuity.TaskAbandoned {
		return false
	}
	return true
}

func insertTaskHistory(ctx context.Context, tx *sql.Tx, task continuity.Task, action, actor, reason string) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO continuity_task_history(task_id, revision, action, actor, reason, created_at, task_json) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Revision, action, actor, reason, task.UpdatedAt, string(payload))
	return err
}

func insertSessionHistory(ctx context.Context, tx *sql.Tx, session continuity.Session, action, actor, reason string) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO continuity_session_history(session_id, revision, action, actor, reason, created_at, session_json) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.Revision, action, actor, reason, session.UpdatedAt, string(payload))
	return err
}

func insertContinuityAudit(ctx context.Context, tx *sql.Tx, entity, id string, revision int, action, actor, reason, stamp string,
	authorization json.RawMessage) error {
	result, err := tx.ExecContext(ctx,
		`INSERT INTO continuity_audit(entity, entity_id, revision, action, actor, reason, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		entity, id, revision, action, actor, reason, stamp)
	if err != nil {
		return err
	}
	if len(authorization) == 0 {
		return nil
	}
	if len(authorization) > 16<<10 || !json.Valid(authorization) {
		return errors.New("continuity authorization evidence is malformed or oversized")
	}
	var attestation teamauth.MutationAttestation
	if err := decodeStrictJSON(string(authorization), &attestation); err != nil || attestation.PolicySHA256 == "" {
		return errors.New("continuity authorization evidence has an invalid attestation envelope")
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO continuity_authorizations(sequence, policy_sha256, attestation_json) VALUES(?, ?, ?)`,
		sequence, attestation.PolicySHA256, string(authorization))
	return err
}

func validateContinuityAudit(actor, reason string) error {
	if strings.TrimSpace(actor) == "" || len(actor) > 128 || strings.TrimSpace(reason) == "" || len(reason) > 2000 {
		return errors.New("continuity audit requires an actor and reason within size limits")
	}
	if privacy.ContainsCredentialText(actor) || privacy.ContainsCredentialText(reason) {
		return errors.New("continuity audit metadata contains credential material")
	}
	return nil
}
