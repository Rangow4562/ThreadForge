package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/continuity"
)

const (
	defaultTaskClaimLeaseSeconds = 3600
	minTaskClaimLeaseSeconds     = 60
	maxTaskClaimLeaseSeconds     = 86400
)

type claimScopeFlags []string

func (f *claimScopeFlags) String() string { return strings.Join(*f, ",") }
func (f *claimScopeFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func runTaskClaim(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge task claim <acquire|renew|revalidate|release|show|list>")
		return 2
	}
	switch args[0] {
	case "acquire", "renew", "revalidate", "release":
		return mutateTaskClaim(args[1:], args[0], stdout, stderr)
	case "show":
		return readTaskClaims(args[1:], true, stdout, stderr)
	case "list":
		return readTaskClaims(args[1:], false, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown task claim command %q\n", args[0])
		return 2
	}
}

func mutateTaskClaim(args []string, action string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("task claim "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	taskID := fs.String("task-id", "", "task identifier")
	claimID := fs.String("id", "", "stable claim identifier")
	expectedTaskRevision := fs.Int("expected-task-revision", 0, "current task revision for compare-and-swap and baseline binding")
	expectedClaimRevision := fs.Int("expected-claim-revision", 0, "current claim revision for compare-and-swap")
	baselineSHA256 := fs.String("baseline-sha256", "", "SHA-256 commitment supplied by the caller for the code baseline")
	leaseSeconds := fs.Int("lease-seconds", defaultTaskClaimLeaseSeconds, "lease duration in seconds (60-86400)")
	var rawScopes claimScopeFlags
	fs.Var(&rawScopes, "scope", "claim scope as path:<relative-path-or-prefix/**>, module:<label>, api:<label>, or schema:<label> (repeatable)")
	actor := fs.String("actor", "local-user", "claim owner (must match authenticated team subject)")
	identityKey := fs.String("identity-key", "", "trusted team identity for team-protected projects")
	reason := fs.String("reason", "task claim updated", "audit reason")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*taskID) == "" || strings.TrimSpace(*claimID) == "" || strings.TrimSpace(*reason) == "" || fs.NArg() != 0 {
		printTaskClaimMutationUsage(stderr, action)
		return 2
	}
	if *leaseSeconds < minTaskClaimLeaseSeconds || *leaseSeconds > maxTaskClaimLeaseSeconds {
		_, _ = fmt.Fprintf(stderr, "--lease-seconds must be between %d and %d\n", minTaskClaimLeaseSeconds, maxTaskClaimLeaseSeconds)
		return 2
	}
	if action != "release" && !validTaskClaimDigest(*baselineSHA256) {
		_, _ = fmt.Fprintln(stderr, "--baseline-sha256 must be a lowercase SHA-256 digest")
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	ctx := context.Background()
	task, err := databaseStore.GetTask(ctx, *taskID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	var claim continuity.TaskClaim
	var expectedRevision int
	switch action {
	case "acquire":
		if *expectedTaskRevision < 1 || *expectedClaimRevision != 0 || len(rawScopes) == 0 {
			printTaskClaimMutationUsage(stderr, action)
			return 2
		}
		if task.Revision != *expectedTaskRevision {
			_, _ = fmt.Fprintln(stderr, store.ErrContinuityConflict)
			return 1
		}
		scopes, scopeErr := parseTaskClaimScopes(rawScopes)
		if scopeErr != nil {
			_, _ = fmt.Fprintln(stderr, scopeErr)
			return 2
		}
		claim = continuity.TaskClaim{
			SchemaVersion: continuity.TaskClaimSchemaVersion, ID: *claimID, ProjectID: task.ProjectID,
			TaskID: task.ID, Owner: *actor, Scopes: scopes, TaskRevision: task.Revision,
			BaselineSHA256: *baselineSHA256, LeaseExpiresAt: now.Add(time.Duration(*leaseSeconds) * time.Second).Format(time.RFC3339Nano),
			Status: "active", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
		}
		expectedRevision = 0
	case "renew", "revalidate", "release":
		if *expectedClaimRevision < 1 || *expectedTaskRevision < 0 || len(rawScopes) != 0 ||
			(action != "release" && *expectedTaskRevision < 1) || (action == "release" && *expectedTaskRevision != 0) {
			printTaskClaimMutationUsage(stderr, action)
			return 2
		}
		claims, listErr := databaseStore.TaskClaims(ctx, task.ID)
		if listErr != nil {
			_, _ = fmt.Fprintln(stderr, listErr)
			return 1
		}
		found := false
		for _, existing := range claims {
			if existing.ID == *claimID {
				claim, found = existing, true
				break
			}
		}
		if !found || claim.Revision != *expectedClaimRevision || claim.Status != "active" {
			_, _ = fmt.Fprintln(stderr, store.ErrContinuityConflict)
			return 1
		}
		expectedRevision = claim.Revision
		previousTaskRevision := claim.TaskRevision
		previousBaselineSHA256 := claim.BaselineSHA256
		claim.Revision++
		claim.UpdatedAt = stamp
		switch action {
		case "renew":
			if *expectedTaskRevision < 1 || task.Revision != *expectedTaskRevision || claim.TaskRevision != task.Revision ||
				*baselineSHA256 != claim.BaselineSHA256 {
				_, _ = fmt.Fprintln(stderr, "claim is stale or the supplied baseline changed; use revalidate with the current task revision and baseline")
				return 1
			}
			claim.LeaseExpiresAt = now.Add(time.Duration(*leaseSeconds) * time.Second).Format(time.RFC3339Nano)
		case "revalidate":
			if task.Status == continuity.TaskCompleted || task.Status == continuity.TaskAbandoned {
				_, _ = fmt.Fprintln(stderr, "terminal tasks cannot revalidate a claim")
				return 1
			}
			if task.Revision != *expectedTaskRevision {
				_, _ = fmt.Fprintln(stderr, store.ErrContinuityConflict)
				return 1
			}
			claim.TaskRevision = task.Revision
			claim.BaselineSHA256 = *baselineSHA256
			claim.LeaseExpiresAt = now.Add(time.Duration(*leaseSeconds) * time.Second).Format(time.RFC3339Nano)
			if claim.TaskRevision == previousTaskRevision && *baselineSHA256 == previousBaselineSHA256 {
				_, _ = fmt.Fprintln(stderr, "revalidation requires a changed task revision or code baseline")
				return 1
			}
		case "release":
			claim.Status = "released"
			claim.ReleasedAt = stamp
		}
	}
	if action != "acquire" {
		resolvedActor, _, authErr := authorizeContinuityActor(*database, task.ProjectID, "task.claim."+action, *actor, *identityKey)
		if authErr != nil {
			_, _ = fmt.Fprintln(stderr, authErr)
			return 1
		}
		*actor = resolvedActor
		if claim.Owner != *actor {
			_, _ = fmt.Fprintln(stderr, "only the current claim owner may renew, revalidate, or release this local claim")
			return 1
		}
	} else {
		resolvedActor, _, authErr := authorizeContinuityActor(*database, task.ProjectID, "task.claim.acquire", *actor, *identityKey)
		if authErr != nil {
			_, _ = fmt.Fprintln(stderr, authErr)
			return 1
		}
		*actor = resolvedActor
		claim.Owner = resolvedActor
	}
	if claim.Status == "active" && (task.Status == continuity.TaskCompleted || task.Status == continuity.TaskAbandoned) {
		_, _ = fmt.Fprintln(stderr, "terminal tasks cannot acquire, renew, or revalidate a claim")
		return 1
	}
	if action == "release" {
		claim.ReleasedAt = stamp
	}
	eventAction := action
	switch action {
	case "acquire":
		eventAction = "acquired"
	case "renew":
		eventAction = "renewed"
	case "revalidate":
		eventAction = "revalidated"
	case "release":
		eventAction = "released"
	}
	event := continuity.TaskClaimEvent{
		SchemaVersion: continuity.TaskClaimEventSchemaVersion, Action: eventAction,
		ExpectedRevision: expectedRevision, Claim: claim,
	}
	entryID, err := newTaskClaimEventID()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	entry := continuity.Entry{
		SchemaVersion: continuity.SchemaVersion, ID: entryID, ProjectID: task.ProjectID, TaskID: task.ID,
		Kind: continuity.EntryTaskClaim, Actor: *actor, Summary: "Task claim " + action,
		TaskClaim: &event, CreatedAt: stamp,
	}
	if err := continuity.ValidateEntry(entry); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	authorizationAction, _ := storeTaskClaimMutationAction(eventAction)
	proofContext, err := func() (context.Context, error) {
		resolvedActor, authorizer, authErr := authorizeContinuityActor(*database, task.ProjectID, authorizationAction, *actor, *identityKey)
		if authErr != nil {
			return nil, authErr
		}
		if resolvedActor != *actor {
			return nil, errors.New("claim owner does not match the authenticated team subject")
		}
		return authorizer.withMutationProof(ctx, authorizationAction, eventAction, "task", task.ID,
			task.ProjectID, expectedRevision, entry, continuity.EntryAppendAuthorizationReason, now)
	}()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := databaseStore.AppendContinuityEntry(proofContext, entry); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	assessment := continuity.AssessTaskClaim(claim, task.Revision, *baselineSHA256, now)
	return writeIndentedJSON(stdout, continuity.TaskClaimRead{
		SchemaVersion: continuity.TaskClaimReadSchemaVersion, TaskID: task.ID,
		Claims: []continuity.TaskClaimAssessment{assessment}, ExecutionAuthorized: false,
	}, stderr)
}

func readTaskClaims(args []string, one bool, stdout, stderr io.Writer) int {
	command := "list"
	if one {
		command = "show"
	}
	fs := flag.NewFlagSet("task claim "+command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	taskID := fs.String("task-id", "", "task identifier")
	claimID := fs.String("id", "", "claim identifier (required for show)")
	baselineSHA256 := fs.String("baseline-sha256", "", "current caller-supplied code baseline digest for comparison")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*taskID) == "" || fs.NArg() != 0 || (one && strings.TrimSpace(*claimID) == "") || (!one && *claimID != "") ||
		(*baselineSHA256 != "" && !validTaskClaimDigest(*baselineSHA256)) {
		_, _ = fmt.Fprintf(stderr, "usage: threadforge task claim %s --task-id <id>%s [--baseline-sha256 <sha256>] [--db <path>]\n",
			command, map[bool]string{true: " --id <claim-id>", false: ""}[one])
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	task, err := databaseStore.GetTask(context.Background(), *taskID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	claims, err := databaseStore.TaskClaims(context.Background(), task.ID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if one {
		for _, claim := range claims {
			if claim.ID == *claimID {
				return writeIndentedJSON(stdout, continuity.TaskClaimRead{
					SchemaVersion: continuity.TaskClaimReadSchemaVersion, TaskID: task.ID,
					Claims:              []continuity.TaskClaimAssessment{continuity.AssessTaskClaim(claim, task.Revision, *baselineSHA256, time.Now().UTC())},
					ExecutionAuthorized: false,
				}, stderr)
			}
		}
		_, _ = fmt.Fprintln(stderr, "task claim not found")
		return 1
	}
	assessments := make([]continuity.TaskClaimAssessment, 0, len(claims))
	for _, claim := range claims {
		assessments = append(assessments, continuity.AssessTaskClaim(claim, task.Revision, *baselineSHA256, time.Now().UTC()))
	}
	return writeIndentedJSON(stdout, continuity.TaskClaimRead{
		SchemaVersion: continuity.TaskClaimReadSchemaVersion, TaskID: task.ID,
		Claims: assessments, ExecutionAuthorized: false,
	}, stderr)
}

func parseTaskClaimScopes(raw []string) ([]continuity.TaskClaimScope, error) {
	if len(raw) == 0 || len(raw) > 32 {
		return nil, errors.New("--scope must be provided 1 to 32 times")
	}
	scopes := make([]continuity.TaskClaimScope, 0, len(raw))
	for _, item := range raw {
		kind, value, ok := strings.Cut(item, ":")
		if !ok || strings.TrimSpace(kind) != kind || strings.TrimSpace(value) != value || value == "" {
			return nil, errors.New("--scope must use kind:value syntax")
		}
		scopes = append(scopes, continuity.TaskClaimScope{Kind: continuity.TaskClaimScopeKind(kind), Value: value})
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].Kind == scopes[j].Kind {
			return scopes[i].Value < scopes[j].Value
		}
		return scopes[i].Kind < scopes[j].Kind
	})
	for i := 1; i < len(scopes); i++ {
		if scopes[i] == scopes[i-1] {
			return nil, errors.New("duplicate --scope values are not allowed")
		}
	}
	return scopes, nil
}

func storeTaskClaimMutationAction(action string) (string, bool) {
	switch action {
	case "acquire", "acquired":
		return "task.claim.acquire", true
	case "renew", "renewed":
		return "task.claim.renew", true
	case "revalidate", "revalidated":
		return "task.claim.revalidate", true
	case "release", "released":
		return "task.claim.release", true
	default:
		return "", false
	}
}

func newTaskClaimEventID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate claim event identifier: %w", err)
	}
	return "claim-event-" + hex.EncodeToString(random[:]), nil
}

func validTaskClaimDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func printTaskClaimMutationUsage(stderr io.Writer, action string) {
	switch action {
	case "acquire":
		_, _ = fmt.Fprintln(stderr, "usage: threadforge task claim acquire --task-id <id> --id <claim-id> --expected-task-revision <n> --baseline-sha256 <digest> --scope <kind:value> [--scope ...] [--lease-seconds 3600] [--actor <subject>] [--identity-key <key>] [--reason <text>] [--db <path>]")
	case "renew", "revalidate":
		_, _ = fmt.Fprintf(stderr, "usage: threadforge task claim %s --task-id <id> --id <claim-id> --expected-claim-revision <n> --expected-task-revision <n> --baseline-sha256 <digest> [--lease-seconds 3600] [--actor <subject>] [--identity-key <key>] [--reason <text>] [--db <path>]\n", action)
	case "release":
		_, _ = fmt.Fprintln(stderr, "usage: threadforge task claim release --task-id <id> --id <claim-id> --expected-claim-revision <n> --baseline-sha256 <digest> [--actor <subject>] [--identity-key <key>] [--reason <text>] [--db <path>]")
	}
}
