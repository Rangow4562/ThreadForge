package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func setGitHubOriginForTest(t *testing.T, root, repository string) {
	t.Helper()
	command := exec.Command("git", "-C", root, "remote", "add", "origin", "https://github.com/"+repository+".git")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("set fixture GitHub origin: %v: %s", err, output)
	}
}

func TestCaptureGitHubMainBranchGovernanceUsesReadOnlyBoundedRequest(t *testing.T) {
	observedAt := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/example/project/rules/branches/main" {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization header missing or incorrect")
		}
		if r.Header.Get("X-GitHub-Api-Version") != githubRulesAPIVersion {
			t.Errorf("API version = %q", r.Header.Get("X-GitHub-Api-Version"))
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`[
{"type":"pull_request","ruleset_id":1,"ruleset_source_type":"Repository","ruleset_source":"example/project","parameters":{"required_approving_review_count":0}},
{"type":"required_status_checks","ruleset_id":1,"ruleset_source_type":"Repository","ruleset_source":"example/project","parameters":{"strict_required_status_checks_policy":true,"required_status_checks":[{"context":"Required CI gate","integration_id":null}]}},
{"type":"deletion","ruleset_id":1,"ruleset_source_type":"Repository","ruleset_source":"example/project"},
{"type":"non_fast_forward","ruleset_id":1,"ruleset_source_type":"Repository","ruleset_source":"example/project"}
]`))
	}))
	defer server.Close()

	governance, err := captureGitHubMainBranchGovernance(context.Background(), server.Client(), server.URL, "example/project", "test-token", observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if governance.SchemaVersion != "threadforge.github-main-branch-governance.v1" || governance.Repository != "example/project" || governance.Branch != "main" || len(governance.Rules) != 4 {
		t.Fatalf("unexpected governance artifact: %#v", governance)
	}
	if governance.ObservedAt != observedAt.Format(time.RFC3339Nano) || governance.APIEndpoint != "/repos/example/project/rules/branches/main" {
		t.Fatalf("observation binding mismatch: %#v", governance)
	}
	encoded, err := json.Marshal(governance)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "test-token") {
		t.Fatal("captured evidence contains the API token")
	}
}

func TestCutoverCaptureMainBranchGovernanceRequiresRepositoryAndNewOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCutover([]string{"capture-main-branch-governance"}, &stdout, &stderr); code != 2 {
		t.Fatalf("missing capture arguments returned %d, want usage error 2", code)
	}
	if !strings.Contains(stderr.String(), "--repository <owner/name> --output <new-evidence.json>") {
		t.Fatalf("missing usage details: %s", stderr.String())
	}
}

func TestGitHubRepositoryFromRemoteAcceptsGitHubCloneFormsOnly(t *testing.T) {
	tests := []struct {
		remote string
		want   string
		ok     bool
	}{
		{"https://github.com/Owner/Repo.git", "Owner/Repo", true},
		{"git@github.com:Owner/Repo.git", "Owner/Repo", true},
		{"ssh://git@github.com/Owner/Repo.git", "Owner/Repo", true},
		{"https://example.invalid/Owner/Repo.git", "", false},
		{"https://token@github.com/Owner/Repo.git", "", false},
		{"https://github.com/Owner/Repo/extra.git", "", false},
		{"https://github.com/Owner/../Repo.git", "", false},
		{"git@example.invalid:Owner/Repo.git", "", false},
	}
	for _, test := range tests {
		t.Run(test.remote, func(t *testing.T) {
			got, err := gitHubRepositoryFromRemote(test.remote)
			if (err == nil) != test.ok || got != test.want {
				t.Fatalf("gitHubRepositoryFromRemote(%q) = %q, %v; want %q, ok=%v", test.remote, got, err, test.want, test.ok)
			}
			if err != nil && strings.Contains(err.Error(), "token") {
				t.Fatalf("remote parse error exposed credential details: %v", err)
			}
		})
	}
}

func TestCaptureGitHubMainBranchGovernanceRecordsEmptyRulesAndFailsClosedOnErrors(t *testing.T) {
	observedAt := time.Now().UTC()
	t.Run("empty rules are recorded for assessor", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) }))
		defer server.Close()
		result, err := captureGitHubMainBranchGovernance(context.Background(), server.Client(), server.URL, "example/project", "", observedAt)
		if err != nil || len(result.Rules) != 0 {
			t.Fatalf("empty effective rules should be recorded, got %#v, %v", result, err)
		}
	})

	t.Run("invalid repository rejected before request", func(t *testing.T) {
		result, err := captureGitHubMainBranchGovernance(context.Background(), http.DefaultClient, "https://api.github.com", "../private/repo", "secret", observedAt)
		if err == nil || !strings.Contains(err.Error(), "owner/name") || len(result.Rules) != 0 {
			t.Fatalf("invalid repository was accepted: %#v, %v", result, err)
		}
	})

	t.Run("API error body and token withheld", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("sensitive upstream message"))
		}))
		defer server.Close()
		_, err := captureGitHubMainBranchGovernance(context.Background(), server.Client(), server.URL, "example/project", "secret-token", observedAt)
		if err == nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "secret-token") || !strings.Contains(err.Error(), "HTTP 403") {
			t.Fatalf("API error leaked body/token or omitted status: %v", err)
		}
	})

	t.Run("oversized body rejected", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("[" + strings.Repeat(" ", maxGitHubRulesBody) + "]"))
		}))
		defer server.Close()
		_, err := captureGitHubMainBranchGovernance(context.Background(), server.Client(), server.URL, "example/project", "", observedAt)
		if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
			t.Fatalf("oversized API response was not rejected: %v", err)
		}
	})
}
