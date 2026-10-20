package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/internal/store"
)

func TestSecurityReviewCLIListsTransitionsAndShowsAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("THREADFORGE_HOME", home)
	fingerprint := fmt.Sprintf("%064x", 44)
	database, err := store.Open(filepath.Join(home, "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ReconcileSecurityFindingReviews(context.Background(), []store.SecurityFindingObservation{{
		Fingerprint: fingerprint, RuleID: "prompt-injection", Severity: "medium",
	}}, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"security", "reviews", "list"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), fingerprint) || !strings.Contains(stdout.String(), `"status": "open"`) {
		t.Fatalf("review list failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{
		"security", "reviews", "set", "--fingerprint", fingerprint,
		"--expected-revision", "0", "--status", "acknowledged", "--actor", "security-analyst",
	}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), `"revision": 1`) ||
		!strings.Contains(stdout.String(), `"actor": "security-analyst"`) {
		t.Fatalf("review transition failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"security", "reviews", "history", "--fingerprint", fingerprint}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), `"previous_status": "open"`) || !strings.Contains(stdout.String(), "security-analyst") {
		t.Fatalf("review audit retrieval failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{
		"security", "reviews", "set", "--fingerprint", fingerprint,
		"--expected-revision", "0", "--status", "resolved",
	}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "revision conflict") {
		t.Fatalf("stale review revision not rejected code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
