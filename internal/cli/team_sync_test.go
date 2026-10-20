package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/procedurefiles"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

func TestTeamSyncCLIRequiresProjectScopeAndPushCAS(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "push requires expected revision", args: []string{"team", "sync", "push", "--project-root", ".", "--project-id", "demo-project"}},
		{name: "pull does not accept push revision", args: []string{"team", "sync", "pull", "--project-root", ".", "--project-id", "demo-project", "--expected-revision", "0"}},
		{name: "direction is explicit", args: []string{"team", "sync", "status"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(test.args, &stdout, &stderr); code != 2 {
				t.Fatalf("CLI exit code = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "team sync") {
				t.Fatalf("CLI error does not identify team sync usage: %q", stderr.String())
			}
		})
	}
}

func TestTeamSyncCLITransfersSignedProcedureBetweenIndependentClones(t *testing.T) {
	base := t.TempDir()
	homeA := filepath.Join(base, "home-a")
	homeB := filepath.Join(base, "home-b")
	if err := os.MkdirAll(homeA, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(homeB, 0o700); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(base, "team.git")
	teamSyncGit(t, base, "init", "--bare", "--quiet", remote)
	projectA := filepath.Join(base, "project-a")
	projectB := filepath.Join(base, "project-b")
	initTeamSyncClone(t, projectA, remote)
	initTeamSyncClone(t, projectB, remote)

	t.Setenv("THREADFORGE_HOME", homeA)
	identityPath := configureTeamTrustForTest(t, homeA, "demo")
	databasePathA := filepath.Join(homeA, "threadforge.sqlite")
	databaseA, err := store.Open(databasePathA)
	if err != nil {
		t.Fatal(err)
	}
	graph := seedApprovedCLIProcedure(t, databaseA, "synced-procedure")
	projectStoreA, err := procedurefiles.NewWithProjectScopeWriteGate(projectA, databaseA)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := teamauth.LoadIdentity(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := projectStoreA.SignPublication(graph, "publish", identity, time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectStoreA.PublishAuthorized(graph, 0, true, authorization); err != nil {
		t.Fatal(err)
	}
	if err := databaseA.Close(); err != nil {
		t.Fatal(err)
	}

	for _, pair := range [][2]string{
		{appdata.TeamTrustPolicyPath(homeA), appdata.TeamTrustPolicyPath(homeB)},
		{appdata.TeamTrustPolicyDigestPath(homeA), appdata.TeamTrustPolicyDigestPath(homeB)},
	} {
		data, err := os.ReadFile(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(pair[1]), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pair[1], data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	pushArgs := []string{"team", "sync", "push", "--project-root", projectA, "--project-id", "demo",
		"--remote", "origin", "--expected-revision", "0", "--db", databasePathA}
	if code := Run(pushArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("team sync push code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var pushed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &pushed); err != nil || pushed["status"] != "published" || pushed["remote_revision"] != float64(1) {
		t.Fatalf("push receipt = %#v, decode error = %v", pushed, err)
	}
	if strings.Contains(stdout.String(), homeA) || strings.Contains(stdout.String(), remote) || strings.Contains(stdout.String(), "private-key") {
		t.Fatal("push receipt leaked a local path, remote URL, or key path")
	}

	t.Setenv("THREADFORGE_HOME", homeB)
	databasePathB := filepath.Join(homeB, "threadforge.sqlite")
	stdout.Reset()
	stderr.Reset()
	pullArgs := []string{"team", "sync", "pull", "--project-root", projectB, "--project-id", "demo",
		"--remote", "origin", "--db", databasePathB}
	if code := Run(pullArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("team sync pull code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var pulled map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &pulled); err != nil || pulled["status"] != "pulled" || pulled["imported_records"] != float64(1) {
		t.Fatalf("pull receipt = %#v, decode error = %v", pulled, err)
	}
	policy, _, _, err := appdata.ReadTeamTrustPolicy(homeB)
	if err != nil {
		t.Fatal(err)
	}
	projectStoreB, err := procedurefiles.NewWithPolicy(projectB, &policy)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := projectStoreB.GetLatest(graph.ID)
	if err != nil || string(latest.Status) != "approved" {
		t.Fatalf("pulled procedure = %#v, err=%v", latest, err)
	}
	for _, evidence := range latest.Evidence {
		if evidence.LocalTraceRef != "" {
			t.Fatal("pulled procedure contains a local trace reference")
		}
	}
}

func initTeamSyncClone(t *testing.T, root, remote string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	teamSyncGit(t, root, "init", "--quiet", "--initial-branch=main")
	teamSyncGit(t, root, "remote", "add", "origin", remote)
}

func teamSyncGit(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}
