package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/continuity"
	"github.com/threadforge-ai/threadforge/pkg/decision"
)

func TestContinuityTaskSessionAndReviewHistory(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-one", ProjectID: "repo-a",
		Owner: "author",
		Title: "Repair test", Objective: "Fix the flaky parser test",
		AcceptanceCriteria: []string{"The parser test passes", "No unrelated behavior regresses"},
		Status:             continuity.TaskOpen, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "author", "capture requested work"); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateTask(ctx, task, "author", "duplicate task"); err == nil {
		t.Fatal("duplicate task id was accepted")
	}
	wrongOwner := task
	wrongOwner.ID = "task-two"
	wrongOwner.Owner = "someone-else"
	if err := database.CreateTask(ctx, wrongOwner, "author", "owner must match creator"); err == nil {
		t.Fatal("task creation accepted an owner different from its creator")
	}
	missingOwner := task
	missingOwner.ID = "task-three"
	missingOwner.Owner = ""
	if err := database.CreateTask(ctx, missingOwner, "author", "owner is required"); err == nil {
		t.Fatal("task creation accepted a missing owner")
	}

	task.Summary = "Located an order-dependent fixture"
	task.NextAction = "Isolate the shared fixture state"
	task.Status = continuity.TaskInProgress
	task.Revision = 2
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := database.UpdateTask(ctx, task, 1, "agent", "record progress"); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateTask(ctx, task, 1, "agent", "stale update"); !errors.Is(err, ErrContinuityConflict) {
		t.Fatalf("stale update error = %v", err)
	}

	transferred := task
	transferred.Owner = "recipient"
	transferred.Revision = 3
	transferred.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := database.UpdateTask(ctx, transferred, 2, "agent", "unilateral owner change"); err == nil || !strings.Contains(err.Error(), "immutable outside handoff") {
		t.Fatalf("ordinary task update changed owner: %v", err)
	}

	session := continuity.Session{
		SchemaVersion: continuity.SchemaVersion, ID: "session-one", ProjectID: task.ProjectID,
		TaskID: task.ID, Harness: "codex-cli", Status: continuity.SessionActive,
		Revision: 1, StartedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.StartSession(ctx, session, "agent", "begin implementation"); err != nil {
		t.Fatal(err)
	}
	entry := continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: "handoff-one", ProjectID: task.ProjectID,
		TaskID: task.ID, SessionID: session.ID, Kind: continuity.EntryHandoff,
		Actor: "agent", Summary: "Parser fix is implemented; run targeted tests next.",
		Details: "Fixture state is now reset between cases.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := database.AppendContinuityEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.ID = "review-one"
	entry.Kind = continuity.EntryReview
	entry.Result = "passed"
	entry.Actor = "reviewer"
	entry.Summary = "The patch is scoped and the fixture reset is explicit."
	entry.SessionID = ""
	entry.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.AppendContinuityEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}

	session.Summary = "Targeted tests passed; waiting for full suite."
	session.NextAction = "Run the full suite and inspect the diff."
	session.Completed = []string{"Reset shared parser fixture after every test"}
	session.Revision = 2
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	session.Status = continuity.SessionClosed
	session.EndedAt = session.UpdatedAt
	if err := database.UpdateSession(ctx, session, 1, "agent", "handoff to next harness"); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateSession(ctx, session, 1, "agent", "stale close"); !errors.Is(err, ErrContinuityConflict) {
		t.Fatalf("stale session update error = %v", err)
	}

	loaded, err := database.GetTask(ctx, task.ID)
	if err != nil || loaded.Revision != 2 || loaded.NextAction != task.NextAction {
		t.Fatalf("loaded task = %#v, error = %v", loaded, err)
	}
	sessions, err := database.ListSessions(ctx, task.ID)
	if err != nil || len(sessions) != 1 || sessions[0].Status != continuity.SessionClosed {
		t.Fatalf("sessions = %#v, error = %v", sessions, err)
	}
	entries, err := database.ContinuityEntries(ctx, task.ID, 20)
	if err != nil || len(entries) != 2 || entries[0].Kind != continuity.EntryHandoff || entries[1].Kind != continuity.EntryReview {
		t.Fatalf("entries = %#v, error = %v", entries, err)
	}
	audit, err := database.ContinuityAudit(ctx, "task", task.ID)
	if err != nil || len(audit) != 2 || audit[1].Revision != 2 {
		t.Fatalf("task audit = %#v, error = %v", audit, err)
	}
	taskHistory, err := database.TaskHistory(ctx, task.ID)
	if err != nil || len(taskHistory) != 2 || taskHistory[1].Task.NextAction != task.NextAction {
		t.Fatalf("task history = %#v, error = %v", taskHistory, err)
	}
	sessionHistory, err := database.SessionHistory(ctx, session.ID)
	if err != nil || len(sessionHistory) != 2 || sessionHistory[1].Session.Status != continuity.SessionClosed {
		t.Fatalf("session history = %#v, error = %v", sessionHistory, err)
	}
}

func TestContinuityRejectsCredentialMaterialAndCrossScopeEntries(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-safe", ProjectID: "repo-a", Owner: "author",
		Title: "Safe task", Objective: "No secrets enter continuity records",
		AcceptanceCriteria: []string{"Reject token-like credentials"}, Status: continuity.TaskOpen,
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "author", "test safety"); err != nil {
		t.Fatal(err)
	}
	task.Objective = "Bearer abcdefghijklmnopqrstuvwxyz0123456789"
	if err := continuity.ValidateTask(task); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("credential objective validation error = %v", err)
	}
	entry := continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: "cross-scope", ProjectID: "repo-b",
		TaskID: task.ID, Kind: continuity.EntryCheckpoint, Actor: "agent",
		Summary: "A wrong-project record must not attach.", CreatedAt: stamp,
	}
	if _, err := database.AppendContinuityEntry(ctx, entry); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("cross-scope entry error = %v", err)
	}
}

func TestTaskPlanRevisionConfirmationAndHistoryAreGoverned(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	task := continuity.Task{
		SchemaVersion: continuity.SchemaVersion, ID: "task-planning", ProjectID: "repo-plan", Owner: "author",
		Title: "Add planning records", Objective: "Keep a durable plan and explicit planning choice",
		AcceptanceCriteria: []string{"Stale revisions are rejected"}, Status: continuity.TaskOpen,
		Requirements: &continuity.TaskRequirements{
			SchemaVersion: continuity.RequirementsSchemaVersion, Status: continuity.RequirementsFinalized,
			Questions: []continuity.RequirementQuestion{}, FinalizedBy: "owner",
			FinalizedAt: stamp, FinalizeReason: "Planning requirements are ready.",
		},
		Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := database.CreateTask(ctx, task, "author", "create plan fixture"); err != nil {
		t.Fatal(err)
	}
	task.Plan = &continuity.TaskPlan{
		SchemaVersion: continuity.PlanSchemaVersion, Version: 1,
		Markdown: "Add a durable plan before implementation.", Decision: continuity.PlanUnconfirmed,
	}
	task.Revision = 2
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := database.ReviseTaskPlan(ctx, task, 1, "planner", "record first plan"); err != nil {
		t.Fatal(err)
	}
	if err := database.ReviseTaskPlan(ctx, task, 1, "planner", "stale plan"); !errors.Is(err, ErrContinuityConflict) {
		t.Fatalf("stale plan revision error = %v", err)
	}
	task.Plan.Decision = continuity.PlanOnly
	task.Plan.ConfirmedBy = "owner"
	task.Plan.ConfirmedAt = time.Now().UTC().Format(time.RFC3339Nano)
	task.Plan.ConfirmReason = "Save the plan without starting implementation."
	task.Revision = 3
	task.UpdatedAt = task.Plan.ConfirmedAt
	if err := database.ConfirmTaskPlan(ctx, task, 2, "owner", task.Plan.ConfirmReason); err != nil {
		t.Fatal(err)
	}
	task.Plan.Decision = continuity.PlanGovernedExecution
	task.Plan.Scope = &continuity.ImplementationScope{Include: []string{"src/**"}}
	task.Revision = 4
	task.Plan.ConfirmedAt = time.Now().UTC().Format(time.RFC3339Nano)
	task.UpdatedAt = task.Plan.ConfirmedAt
	if err := database.ConfirmTaskPlan(ctx, task, 3, "owner", "start the approved bounded work"); err == nil {
		t.Fatal("confirmation changed plan content and scope without a plan revision")
	}
	task.Plan.Scope = &continuity.ImplementationScope{Include: []string{"src/**"}}
	task.Plan.Markdown = "Revised plan with an explicit file boundary."
	task.Plan.Decision = continuity.PlanUnconfirmed
	task.Plan.ConfirmedBy = ""
	task.Plan.ConfirmedAt = ""
	task.Plan.ConfirmReason = ""
	task.Plan.Version = 2
	task.Revision = 4
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := database.ReviseTaskPlan(ctx, task, 3, "planner", "add the implementation boundary"); err != nil {
		t.Fatal(err)
	}
	task.Plan.Decision = continuity.PlanGovernedExecution
	task.Plan.ConfirmedBy = "owner"
	task.Plan.ConfirmedAt = time.Now().UTC().Format(time.RFC3339Nano)
	task.Plan.ConfirmReason = "The explicit scope is approved."
	task.Revision = 5
	task.UpdatedAt = task.Plan.ConfirmedAt
	if err := database.ConfirmTaskPlan(ctx, task, 4, "owner", task.Plan.ConfirmReason); err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetTask(ctx, task.ID)
	if err != nil || loaded.Plan == nil || loaded.Plan.Decision != continuity.PlanGovernedExecution || loaded.Plan.Version != 2 {
		t.Fatalf("loaded planned task = %#v, error = %v", loaded, err)
	}
	history, err := database.TaskHistory(ctx, task.ID)
	if err != nil || len(history) != 5 || history[1].Action != "plan_revised" || history[2].Action != "plan_confirmed" || history[3].Action != "plan_revised" || history[4].Action != "plan_confirmed" {
		t.Fatalf("plan audit history = %#v, error = %v", history, err)
	}
	if history[2].Task.Plan.Decision != continuity.PlanOnly || history[3].Task.Plan.Decision != continuity.PlanUnconfirmed {
		t.Fatalf("plan decision snapshots lost from history: %#v", history)
	}
}

func TestSchemaUpgradeFromV1AndRejectsNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-v1.sqlite")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := database.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion {
		t.Fatalf("upgraded schema version=%d, want %d", version, CurrentSchemaVersion)
	}
	for _, table := range []string{"decision_observation_streams", "decision_observation_history", "decision_bundles", "decision_bundle_history", "decision_bundle_audit", "continuity_authorizations", "continuity_policy_snapshots", "team_project_scopes", "team_project_scope_events", "team_project_scope_write_locks", "store_recovery_state", "security_finding_reviews", "security_finding_review_history"} {
		if err := database.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&version); err != nil || version != 1 {
			t.Fatalf("observation schema table %s count=%d error=%v", table, version, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	newerPath := filepath.Join(t.TempDir(), "future.sqlite")
	raw, err = sql.Open("sqlite", newerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 13`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if database, err := Open(newerPath); err == nil {
		_ = database.Close()
		t.Fatal("opened a database with a newer unsupported schema")
	}

	v2Path := filepath.Join(t.TempDir(), "legacy-v2.sqlite")
	raw, err = sql.Open("sqlite", v2Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(v2Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'decision_artifact_history'`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("decision artifact history migration count=%d error=%v", version, err)
	}
	_ = database.Close()
}

func TestImportedDecisionArtifactRemainsCandidateAndRecordsImportAction(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	content := []byte(`{"model":"local-model","question":"0123456789abcdef","method":"temperature","temperature":1.2,"n_calib":8}`)
	artifact, err := decision.NewImportedArtifact("imported-calibration", content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ImportDecisionArtifact(context.Background(), artifact, "operator", "import upstream calibration for review"); err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetDecisionArtifact(context.Background(), artifact.ID)
	if err != nil || loaded.Status != decision.ArtifactCandidate || loaded.Revision != 1 {
		t.Fatalf("import activated an artifact or failed to load: artifact=%#v error=%v", loaded, err)
	}
	history, err := database.DecisionArtifactHistory(context.Background(), artifact.ID)
	if err != nil || len(history) != 1 || history[0].Action != "imported" || history[0].Artifact.Status != decision.ArtifactCandidate {
		t.Fatalf("import audit history=%#v error=%v", history, err)
	}
}

func TestDecisionArtifactLifecycleIsVersionedAndAppendOnly(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	content := []byte(`{"model":"local-model","question":"0123456789abcdef","method":"temperature","temperature":1.2,"prior_method":"batch","prior_strength":0.75,"n_calib":8}`)
	artifact, err := decision.NewArtifact("calibration-one", "L1", content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := database.CreateDecisionArtifact(ctx, artifact, "operator", "calibration produced a candidate"); err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetDecisionArtifact(ctx, artifact.ID)
	if err != nil || loaded.Status != decision.ArtifactCandidate || loaded.ContentSHA256 != artifact.ContentSHA256 {
		t.Fatalf("loaded candidate=%#v error=%v", loaded, err)
	}
	if _, err := database.TransitionDecisionArtifact(ctx, artifact.ID, 1, decision.ArtifactRetired, "reviewer", "invalid direct transition", time.Now().UTC()); err == nil {
		t.Fatal("candidate skipped review and was retired")
	}
	active, err := database.TransitionDecisionArtifact(ctx, artifact.ID, 1, decision.ArtifactActive, "reviewer", "calibration artifact reviewed", time.Now().UTC())
	if err != nil || active.Revision != 2 || active.Status != decision.ArtifactActive {
		t.Fatalf("approve artifact=%#v error=%v", active, err)
	}
	if _, err := database.TransitionDecisionArtifact(ctx, artifact.ID, 1, decision.ArtifactRetired, "reviewer", "stale retirement", time.Now().UTC()); !errors.Is(err, ErrDecisionArtifactConflict) {
		t.Fatalf("stale artifact transition error=%v", err)
	}
	retired, err := database.TransitionDecisionArtifact(ctx, artifact.ID, 2, decision.ArtifactRetired, "owner", "replace the calibration", time.Now().UTC())
	if err != nil || retired.Revision != 3 || retired.Status != decision.ArtifactRetired {
		t.Fatalf("retire artifact=%#v error=%v", retired, err)
	}
	if _, err := database.TransitionDecisionArtifact(ctx, artifact.ID, 3, decision.ArtifactActive, "owner", "attempt to revive", time.Now().UTC()); err == nil {
		t.Fatal("retired artifact was revived")
	}
	items, total, err := database.ListDecisionArtifacts(ctx, string(decision.ArtifactRetired), 10, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != artifact.ID {
		t.Fatalf("retired artifact list=%#v total=%d error=%v", items, total, err)
	}
	history, err := database.DecisionArtifactHistory(ctx, artifact.ID)
	if err != nil || len(history) != 3 || history[0].Action != "calibrated" || history[1].Action != "approved" || history[2].Action != "retired" {
		t.Fatalf("artifact lifecycle history=%#v error=%v", history, err)
	}
	for _, revision := range history {
		if revision.Artifact.ContentSHA256 != artifact.ContentSHA256 {
			t.Fatalf("artifact content changed across lifecycle at revision %d", revision.Artifact.Revision)
		}
	}
}

func TestDecisionArtifactCheckpointBindingIsAtomicAndRetainedInHistory(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	content := []byte(`{"model":"local-model","question":"0123456789abcdef","method":"temperature","temperature":1.2,"n_calib":8}`)
	artifact, err := decision.NewArtifact("checkpoint-bound", "L1", content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := database.CreateDecisionArtifact(ctx, artifact, "operator", "create pending checkpoint-bound candidate"); err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("a", 64)
	active, err := database.TransitionDecisionArtifactWithCheckpoint(ctx, artifact.ID, 1,
		decision.ArtifactActive, "reviewer", "approve against the local checkpoint", fingerprint, time.Now().UTC())
	if err != nil || active.Status != decision.ArtifactActive || active.ModelCheckpointSHA256 != fingerprint {
		t.Fatalf("approved checkpoint-bound artifact=%#v error=%v", active, err)
	}
	loaded, err := database.GetDecisionArtifact(ctx, artifact.ID)
	if err != nil || loaded.ModelCheckpointSHA256 != fingerprint {
		t.Fatalf("loaded checkpoint-bound artifact=%#v error=%v", loaded, err)
	}
	history, err := database.DecisionArtifactHistory(ctx, artifact.ID)
	if err != nil || len(history) != 2 || history[1].Artifact.ModelCheckpointSHA256 != fingerprint {
		t.Fatalf("checkpoint binding missing from approval history=%#v error=%v", history, err)
	}
	if _, err := database.TransitionDecisionArtifactWithCheckpoint(ctx, artifact.ID, 2,
		decision.ArtifactRetired, "owner", "retire bound artifact", fingerprint, time.Now().UTC()); err == nil {
		t.Fatal("checkpoint binding must not be newly supplied during retirement")
	}
	retired, err := database.TransitionDecisionArtifact(ctx, artifact.ID, 2,
		decision.ArtifactRetired, "owner", "retire bound artifact", time.Now().UTC())
	if err != nil || retired.ModelCheckpointSHA256 != fingerprint {
		t.Fatalf("retirement did not preserve checkpoint binding: artifact=%#v error=%v", retired, err)
	}
}
