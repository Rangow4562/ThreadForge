package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/continuity"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

func TestTaskClaimCLITracksLeaseStalenessAndExplicitRelease(t *testing.T) {
	database := filepath.Join(t.TempDir(), "claims.sqlite")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"task", "create", "--id", "claim-task", "--project-id", "claim-project",
		"--title", "Scoped change", "--objective", "Coordinate changes to the API", "--criterion", "No overlapping active claims", "--db", database}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("task create code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	baselineA := strings.Repeat("a", 64)
	baselineB := strings.Repeat("b", 64)
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"task", "claim", "acquire", "--task-id", "claim-task", "--id", "claim-api",
		"--expected-task-revision", "1", "--baseline-sha256", baselineA, "--scope", "path:src/api/**",
		"--lease-seconds", "60", "--db", database}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("claim acquire code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result continuity.TaskClaimRead
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.SchemaVersion != continuity.TaskClaimReadSchemaVersion ||
		result.TaskID != "claim-task" || len(result.Claims) != 1 || result.Claims[0].Claim.ID != "claim-api" ||
		result.Claims[0].LeaseStatus != "active" || result.Claims[0].BaselineStatus != "match" || result.ExecutionAuthorized {
		t.Fatalf("acquire result=%#v, decode error=%v, stderr=%s", result, err, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"task", "update", "--id", "claim-task", "--expected-revision", "1", "--summary", "Implementation started", "--db", database}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("task update code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"task", "claim", "renew", "--task-id", "claim-task", "--id", "claim-api",
		"--expected-claim-revision", "1", "--expected-task-revision", "2", "--baseline-sha256", baselineA,
		"--lease-seconds", "60", "--db", database}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "use revalidate") {
		t.Fatalf("stale claim renewal code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"task", "claim", "revalidate", "--task-id", "claim-task", "--id", "claim-api",
		"--expected-claim-revision", "1", "--expected-task-revision", "2", "--baseline-sha256", baselineB,
		"--lease-seconds", "120", "--db", database}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("claim revalidate code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result.Claims) != 1 ||
		result.Claims[0].Claim.Revision != 2 || result.Claims[0].Claim.TaskRevision != 2 ||
		result.Claims[0].BaselineStatus != "match" || result.Claims[0].ExecutionAuthorized {
		t.Fatalf("revalidate result=%#v decode_error=%v stderr=%s", result, err, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"task", "claim", "release", "--task-id", "claim-task", "--id", "claim-api",
		"--expected-claim-revision", "2", "--db", database}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("claim release code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result.Claims) != 1 ||
		result.Claims[0].Claim.Status != "released" || result.Claims[0].LeaseStatus != "released" || result.ExecutionAuthorized {
		t.Fatalf("release result=%#v decode_error=%v stderr=%s", result, err, stderr.String())
	}
}

func TestTaskClaimCLIRejectsInvalidBaselineAndScopeBeforeMutation(t *testing.T) {
	database := filepath.Join(t.TempDir(), "claims-invalid.sqlite")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"task", "create", "--id", "claim-task", "--project-id", "claim-project",
		"--title", "Scoped change", "--objective", "Keep claims bounded", "--criterion", "Invalid claims are rejected", "--db", database}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("task create code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, args := range [][]string{
		{"task", "claim", "acquire", "--task-id", "claim-task", "--id", "claim-invalid", "--expected-task-revision", "1", "--baseline-sha256", "not-a-digest", "--scope", "path:src/api/**", "--db", database},
		{"task", "claim", "acquire", "--task-id", "claim-task", "--id", "claim-invalid", "--expected-task-revision", "1", "--baseline-sha256", strings.Repeat("a", 64), "--scope", "path:../private", "--db", database},
	} {
		stdout.Reset()
		stderr.Reset()
		if code = Run(args, &stdout, &stderr); code == 0 || stdout.Len() != 0 {
			t.Fatalf("invalid claim accepted: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"task", "claim", "list", "--task-id", "claim-task", "--db", database}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), `"claims": []`) {
		t.Fatalf("invalid claim mutated store: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestTeamTaskClaimRequiresSignedWriteAndPassesLedgerAudit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("THREADFORGE_HOME", root)
	identityPath := configureContinuityTrustForTest(t, root, "claim-team", "claim-owner", teamauth.RoleContinuityWrite)
	database := filepath.Join(root, "claims.sqlite")
	var stdout, stderr bytes.Buffer
	create := []string{"task", "create", "--id", "team-claim-task", "--project-id", "claim-team",
		"--title", "Team claim", "--objective", "Authenticate claim mutations", "--criterion", "Audit the complete claim history", "--db", database}
	if code := Run(create, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "requires --identity-key") {
		t.Fatalf("unsigned team task creation code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	create = append(create, "--identity-key", identityPath)
	if code := Run(create, &stdout, &stderr); code != 0 {
		t.Fatalf("signed team task creation code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	claimArgs := []string{"task", "claim", "acquire", "--task-id", "team-claim-task", "--id", "team-api-claim",
		"--expected-task-revision", "1", "--baseline-sha256", strings.Repeat("c", 64),
		"--scope", "api:POST /v1/orders", "--lease-seconds", "60", "--identity-key", identityPath, "--db", database}
	stdout.Reset()
	stderr.Reset()
	if code := Run(claimArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("signed team claim code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"task", "claim", "renew", "--task-id", "team-claim-task", "--id", "team-api-claim",
		"--expected-claim-revision", "1", "--expected-task-revision", "1", "--baseline-sha256", strings.Repeat("c", 64),
		"--lease-seconds", "120", "--identity-key", identityPath, "--db", database}, &stdout, &stderr); code != 0 {
		t.Fatalf("signed team claim renewal code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"team", "audit", "verify", "--db", database}, &stdout, &stderr); code != 0 {
		t.Fatalf("team claim audit code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report struct {
		Status                 string `json:"status"`
		EntriesChecked         int    `json:"entries_checked"`
		InvalidRecords         int    `json:"invalid_records"`
		VerifiedAuthorizations int    `json:"verified_authorizations"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Status != "passed" ||
		report.EntriesChecked != 2 || report.InvalidRecords != 0 || report.VerifiedAuthorizations != 3 {
		t.Fatalf("team claim audit report=%#v decode_error=%v stderr=%s", report, err, stderr.String())
	}
}
