package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/cutover"
	"github.com/threadforge-ai/threadforge/pkg/strictjson"
)

const (
	githubRulesAPIVersion = "2026-03-10"
	maxGitHubRulesBody    = 1 << 20
)

func runCutoverCaptureGitHubGovernance(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cutover capture-main-branch-governance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repository := fs.String("repository", "", "GitHub repository in owner/name form")
	outputPath := fs.String("output", "", "new path for the read-only governance evidence JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *repository == "" || *outputPath == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge cutover capture-main-branch-governance --repository <owner/name> --output <new-evidence.json>")
		return 2
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	governance, err := captureGitHubMainBranchGovernance(context.Background(), client, "https://api.github.com", *repository, token, time.Now())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	output, err := validateNewOutputPath(*outputPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "governance output:", err)
		return 1
	}
	content, err := json.MarshalIndent(governance, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "encode governance evidence:", err)
		return 1
	}
	content = append(content, '\n')
	if err := writeExclusiveFile(output, content); err != nil {
		_, _ = fmt.Fprintln(stderr, "write governance evidence:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "captured read-only main-branch governance for %s\n", *repository)
	return 0
}

func captureGitHubMainBranchGovernance(ctx context.Context, client *http.Client, apiBase, repository, token string, observedAt time.Time) (cutover.GitHubMainBranchGovernance, error) {
	var result cutover.GitHubMainBranchGovernance
	if !cutover.GitHubRepositorySlugValid(repository) {
		return result, errors.New("GitHub repository must be owner/name")
	}
	if client == nil {
		return result, errors.New("GitHub API client is required")
	}
	endpoint := strings.TrimRight(apiBase, "/") + "/repos/" + repository + "/rules/branches/main"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, errors.New("could not create GitHub main-branch rules request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubRulesAPIVersion)
	request.Header.Set("User-Agent", "threadforge-cutover-governance-capture")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return result, errors.New("GitHub main-branch rules request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("GitHub main-branch rules request returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGitHubRulesBody+1))
	if err != nil || len(body) > maxGitHubRulesBody {
		return result, errors.New("GitHub main-branch rules response is unreadable or exceeds 1 MiB")
	}
	var rules []map[string]json.RawMessage
	if err := strictjson.Decode(body, &rules); err != nil {
		return result, errors.New("GitHub main-branch rules response is invalid JSON")
	}
	result = cutover.GitHubMainBranchGovernance{
		SchemaVersion: cutover.GitHubMainBranchGovernanceSchemaVersion,
		Repository:    repository,
		Branch:        "main",
		APIEndpoint:   fmt.Sprintf(cutover.GitHubMainBranchRulesEndpointTemplate, repository),
		ObservedAt:    observedAt.UTC().Format(time.RFC3339Nano),
		Rules:         rules,
	}
	if err := cutover.ValidateGitHubMainBranchGovernanceEnvelope(result); err != nil {
		return cutover.GitHubMainBranchGovernance{}, fmt.Errorf("GitHub main-branch rules response cannot be recorded: %w", err)
	}
	return result, nil
}
