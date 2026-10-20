package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/continuity"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

func TestTaskHandoffCLICompletesAtomicTransferAndReportsNoExecutionAuthority(t *testing.T) {
	root := t.TempDir()
	t.Setenv("THREADFORGE_HOME", root)
	databasePath := filepath.Join(root, "threadforge.sqlite")
	baseline := strings.Repeat("a", 64)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"task", "create", "--id", "handoff-cli-task", "--project-id", "handoff-cli-project",
		"--title", "Finish parser work", "--objective", "Transfer parser implementation safely", "--criterion", "Focused tests pass",
		"--actor", "alice", "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("task create code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"task", "claim", "acquire", "--task-id", "handoff-cli-task", "--id", "handoff-cli-claim",
		"--expected-task-revision", "1", "--baseline-sha256", baseline, "--scope", "path:pkg/parser/**",
		"--actor", "alice", "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("claim acquire code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"task", "handoff", "draft", "--id", "handoff-cli", "--task-id", "handoff-cli-task",
		"--expected-task-revision", "1", "--claim-id", "handoff-cli-claim", "--expected-claim-revision", "1",
		"--baseline-sha256", baseline, "--to-owner", "bob", "--checkpoint-entry-id", "handoff-cli-checkpoint",
		"--checkpoint-summary", "Parser is implemented; run the focused suite.", "--actor", "alice", "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("handoff draft code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var read continuity.TaskHandoffRead
	if err := json.Unmarshal(stdout.Bytes(), &read); err != nil || len(read.Handoffs) != 1 ||
		read.Handoffs[0].Status != continuity.TaskHandoffDraft || read.ExecutionAuthorized {
		t.Fatalf("draft response=%#v decode_error=%v stderr=%s", read, err, stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"task", "handoff", "offer", "--id", "handoff-cli", "--task-id", "handoff-cli-task",
		"--expected-revision", "1", "--actor", "alice", "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("handoff offer code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"task", "handoff", "accept", "--id", "handoff-cli", "--task-id", "handoff-cli-task",
		"--expected-revision", "2", "--actor", "bob", "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("handoff accept code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &read); err != nil || len(read.Handoffs) != 1 ||
		read.Handoffs[0].Status != continuity.TaskHandoffAccepted || read.ExecutionAuthorized {
		t.Fatalf("accept response=%#v decode_error=%v stderr=%s", read, err, stderr.String())
	}
	database, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task, err := database.GetTask(context.Background(), "handoff-cli-task")
	if err != nil || task.Owner != "bob" || task.Revision != 2 {
		t.Fatalf("transferred task=%#v error=%v", task, err)
	}
	claims, err := database.TaskClaims(context.Background(), task.ID)
	if err != nil || len(claims) != 1 || claims[0].Owner != "bob" || claims[0].TaskRevision != task.Revision {
		t.Fatalf("transferred claims=%#v error=%v", claims, err)
	}
}

func TestTeamTaskHandoffSignsOwnerClaimCheckpointAndTaskAudit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("THREADFORGE_HOME", root)
	projectID := "handoff-team-project"
	aliceIdentity, bobIdentity := configureTaskHandoffTeamTrust(t, root, projectID)
	databasePath := filepath.Join(root, "threadforge.sqlite")
	baseline := strings.Repeat("b", 64)
	var stdout, stderr bytes.Buffer
	create := []string{"task", "create", "--id", "handoff-team-task", "--project-id", projectID,
		"--title", "Team handoff", "--objective", "Transfer linked task ownership", "--criterion", "All transfer records are atomic",
		"--actor", "alice", "--identity-key", aliceIdentity, "--db", databasePath}
	if code := Run(create, &stdout, &stderr); code != 0 {
		t.Fatalf("team task create code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"task", "claim", "acquire", "--task-id", "handoff-team-task", "--id", "handoff-team-claim",
		"--expected-task-revision", "1", "--baseline-sha256", baseline, "--scope", "path:internal/parser/**",
		"--actor", "alice", "--identity-key", aliceIdentity, "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("team claim acquire code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"task", "handoff", "draft", "--id", "handoff-team-one", "--task-id", "handoff-team-task",
		"--expected-task-revision", "1", "--claim-id", "handoff-team-claim", "--expected-claim-revision", "1",
		"--baseline-sha256", baseline, "--to-owner", "bob", "--checkpoint-entry-id", "handoff-team-checkpoint",
		"--checkpoint-summary", "Parser changes are staged for review.", "--actor", "alice", "--identity-key", aliceIdentity, "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("team handoff draft code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"task", "handoff", "offer", "--id", "handoff-team-one", "--task-id", "handoff-team-task",
		"--expected-revision", "1", "--actor", "alice", "--identity-key", aliceIdentity, "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("team handoff offer code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"task", "handoff", "accept", "--id", "handoff-team-one", "--task-id", "handoff-team-task",
		"--expected-revision", "2", "--actor", "bob", "--identity-key", bobIdentity, "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("team handoff accept code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"team", "audit", "verify", "--db", databasePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("team audit verify code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report store.TeamContinuityAuditReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Status != "passed" || report.InvalidRecords != 0 || report.VerifiedAuthorizations < 8 {
		t.Fatalf("team audit report=%#v decode_error=%v stderr=%s", report, err, stderr.String())
	}
}

func configureTaskHandoffTeamTrust(t *testing.T, root, projectID string) (string, string) {
	t.Helper()
	type identityFiles struct {
		subject string
		private string
		public  teamauth.PublicIdentity
	}
	identities := make([]identityFiles, 0, 2)
	for _, subject := range []string{"alice", "bob"} {
		identity, public, err := teamauth.Generate()
		if err != nil {
			t.Fatal(err)
		}
		privatePath := filepath.Join(root, subject+"-identity.json")
		if err := teamauth.SaveIdentity(privatePath, filepath.Join(root, subject+"-identity.pub.json"), identity, public); err != nil {
			t.Fatal(err)
		}
		identities = append(identities, identityFiles{subject: subject, private: privatePath, public: public})
	}
	policy := teamauth.Policy{
		SchemaVersion: teamauth.PolicySchemaVersion, OrganizationID: "handoff-test-org",
		Members: []teamauth.Member{},
	}
	for _, identity := range identities {
		policy.Members = append(policy.Members, teamauth.Member{
			Subject: identity.subject, KeyID: identity.public.KeyID, PublicKey: identity.public.PublicKey,
			Projects: []string{projectID}, Roles: []string{teamauth.RoleContinuityWrite},
		})
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	policyBytes, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	policyData := append(policyBytes, '\n')
	policyPath := appdata.TeamTrustPolicyPath(root)
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, policyData, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(policyData)
	if err := os.WriteFile(appdata.TeamTrustPolicyDigestPath(root), []byte(hex.EncodeToString(digest[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return identities[0].private, identities[1].private
}
