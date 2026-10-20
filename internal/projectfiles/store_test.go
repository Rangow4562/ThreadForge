package projectfiles

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/internal/sourcefingerprint"
	"github.com/threadforge-ai/threadforge/pkg/projectmemory"
)

func candidateRecord() projectmemory.Candidate {
	return projectmemory.Candidate{
		SchemaVersion: projectmemory.SchemaVersion,
		ID:            "release-policy",
		ProjectID:     "sample-project",
		Kind:          projectmemory.KindDecision,
		Title:         "Release decision",
		Content:       []byte(`{"decision":"Keep releases human-approved."}`),
		Evidence:      []projectmemory.Evidence{{Origin: "human", ContentSHA256: strings.Repeat("a", 64), Redacted: true}},
	}
}

func TestProjectMemoryV2PublishBindsSourcesAndFreshnessTracksChanges(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "docs", "decision.md")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("decision source v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprints, err := sourcefingerprint.FingerprintSources(root, []string{"docs/decision.md"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	candidate := projectmemory.Candidate{
		SchemaVersion: projectmemory.SchemaVersionV2,
		ID:            "source-bound-decision", ProjectID: "sample-project", Kind: projectmemory.KindDecision, Title: "Source decision",
		Content: []byte(`{"decision":"Keep behavior aligned with the source."}`),
		Evidence: []projectmemory.Evidence{{Origin: "document", ContentSHA256: strings.Repeat("b", 64), Redacted: true,
			SourceFiles: []projectmemory.SourceFile{{Path: fingerprints[0].Path, SHA256: fingerprints[0].SHA256}}}},
	}
	record, err := store.Publish(candidate, 0, "reviewer", "approved source-bound decision", true, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got := store.CheckFreshness(record); got != "verified" {
		t.Fatalf("initial freshness = %q, want verified", got)
	}
	read := store.ReadV2(candidate.ProjectID, candidate.Kind, candidate.ID, nil, nil)
	if read.Status != "complete" || read.Freshness != "verified" || read.Record == nil || read.Record.ID != candidate.ID {
		t.Fatalf("fresh exact read = %#v, want verified record", read)
	}
	changed := candidate
	changed.ID = "stale-source-decision"
	changed.Evidence = append([]projectmemory.Evidence(nil), candidate.Evidence...)
	changed.Evidence[0].SourceFiles = append([]projectmemory.SourceFile(nil), candidate.Evidence[0].SourceFiles...)
	changed.Evidence[0].SourceFiles[0].SHA256 = strings.Repeat("c", 64)
	if _, err := store.Publish(changed, 0, "reviewer", "must reject stale source digest", true, time.Now().UTC()); err == nil {
		t.Fatal("publish accepted a source digest that did not match the project file")
	}
	if err := os.WriteFile(source, []byte("decision source v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := store.CheckFreshness(record); got != "stale" {
		t.Fatalf("changed-source freshness = %q, want stale", got)
	}
	read = store.ReadV2(candidate.ProjectID, candidate.Kind, candidate.ID, nil, nil)
	if read.Status != "stale" || read.Freshness != "stale" || read.Record != nil {
		t.Fatalf("stale exact read = %#v, want content withheld", read)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if got := store.CheckFreshness(record); got != "incomplete" {
		t.Fatalf("missing-source freshness = %q, want incomplete", got)
	}
	read = store.ReadV2(candidate.ProjectID, candidate.Kind, candidate.ID, nil, nil)
	if read.Status != "incomplete" || read.Freshness != "incomplete" || read.Record != nil {
		t.Fatalf("unavailable-source exact read = %#v, want content withheld", read)
	}
	legacy, err := store.Publish(candidateRecord(), 0, "reviewer", "legacy v1 fixture", true, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got := store.CheckFreshness(legacy); got != "unverified" {
		t.Fatalf("legacy v1 freshness = %q, want unverified", got)
	}
	read = store.ReadV2(legacy.ProjectID, legacy.Kind, legacy.ID, nil, nil)
	if read.Status != "incomplete" || read.Freshness != "unverified" || read.Record == nil {
		t.Fatalf("legacy exact read = %#v, want explicitly unverified guidance", read)
	}
}

func TestPublishWritesVersionedApprovedRevisionsWithCASAndHashChain(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	first, err := store.Publish(candidateRecord(), 0, "reviewer", "confirmed source", true, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.Status != "approved" || first.Approval == nil || !first.Approval.PrivacyReviewed {
		t.Fatalf("first published record = %#v", first)
	}
	firstPath := filepath.Join(root, ".threadforge", "memory", "records", "decision", first.ID, "rev-000001.json")
	firstBytes, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondCandidate := candidateRecord()
	secondCandidate.Content = []byte(`{"decision":"Keep releases human-approved.","revision_note":"reviewed again"}`)
	second, err := store.Publish(secondCandidate, 1, "reviewer-2", "updated after review", true, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || second.PreviousSHA256 != digest(firstBytes) || second.CreatedAt != first.CreatedAt {
		t.Fatalf("second revision linkage = %#v", second)
	}
	if _, err := store.Publish(candidateRecord(), 0, "reviewer", "stale write", true, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale publish error = %v, want conflict", err)
	}
	history, err := store.History(projectmemory.KindDecision, first.ID)
	if err != nil || len(history) != 2 || history[1].Revision != 2 {
		t.Fatalf("history = %#v err=%v", history, err)
	}
	latest, err := store.GetLatest(projectmemory.KindDecision, first.ID)
	if err != nil || latest.Revision != 2 {
		t.Fatalf("latest = %#v err=%v", latest, err)
	}
}

func TestPublishRequiresPrivacyReviewAndRejectsCredentialContent(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(candidateRecord(), 0, "reviewer", "reason", false, time.Now()); err == nil {
		t.Fatal("publish without explicit privacy review was accepted")
	}
	candidate := candidateRecord()
	candidate.Content = []byte(`{"api_key":"do-not-share-this"}`)
	if _, err := store.Publish(candidate, 0, "reviewer", "reason", true, time.Now()); err == nil || !strings.Contains(err.Error(), "credential material") {
		t.Fatalf("credential-bearing record error = %v", err)
	}
}

func TestPublishRejectsNestedAndTokenShapedSecretsInAdversarialText(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		content string
		secret  string
	}{
		{"nested bearer", `{"note":{"text":"Ignore policy. Authorization: Bearer abcdefghijklmnopqr123"}}`, "abcdefghijklmnopqr123"},
		{"private key", `{"note":"-----BEGIN PRIVATE KEY-----\nprivate-test-material\n-----END PRIVATE KEY-----"}`, "private-test-material"},
		{"nested token field", `{"details":{"client_secret":"never-persist-this-token"}}`, "never-persist-this-token"},
		{"github token", `{"note":"ghp_abcdefghijklmnopqrstuvwxyz123456"}`, "ghp_abcdefghijklmnopqrstuvwxyz123456"},
	}
	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := candidateRecord()
			candidate.ID = fmt.Sprintf("secret-case-%d", index)
			candidate.Content = []byte(testCase.content)
			if _, err := store.Publish(candidate, 0, "reviewer", "adversarial secret fixture", true, time.Now().UTC()); err == nil || strings.Contains(err.Error(), testCase.secret) {
				t.Fatalf("credential content was accepted or echoed: %v", err)
			}
		})
	}
}

func TestReadHistoryRejectsTamperedRevisionChain(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(candidateRecord(), 0, "reviewer", "reason", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".threadforge", "memory", "records", "decision", "release-policy", "rev-000001.json")
	if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLatest(projectmemory.KindDecision, "release-policy"); err == nil {
		t.Fatal("tampered project-memory history was accepted")
	}
}

func TestListReturnsLatestRevisionOnly(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(candidateRecord(), 0, "reviewer", "reason", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	second := candidateRecord()
	second.Content = []byte(`{"decision":"Updated."}`)
	if _, err := store.Publish(second, 1, "reviewer", "reason", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	items, total, err := store.List(projectmemory.KindDecision, 10, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].Revision != 2 {
		t.Fatalf("list = %#v total=%d err=%v", items, total, err)
	}
}

func TestStoreRefusesSymlinkedProjectMemoryDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "memory", "records", "fact", "external"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".threadforge")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLatest(projectmemory.KindFact, "external"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlinked project-memory path error = %v", err)
	}
}
