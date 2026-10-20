package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSecurityFindingReviewLifecyclePreservesPrivacyAndAudit(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	firstAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	firstID := fmt.Sprintf("%064x", 1)
	secondID := fmt.Sprintf("%064x", 2)
	observations := []SecurityFindingObservation{
		{Fingerprint: firstID, RuleID: "prompt-injection", Severity: "medium"},
		{Fingerprint: secondID, RuleID: "secret-exposure", Severity: "high"},
	}
	if err := database.ReconcileSecurityFindingReviews(ctx, observations, firstAt); err != nil {
		t.Fatal(err)
	}
	first, err := database.GetSecurityFindingReview(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != SecurityReviewOpen || first.Revision != 0 || !first.Active || first.FirstSeen != first.LastSeen || first.Actor != "local-scan" {
		t.Fatalf("unexpected initial finding review: %#v", first)
	}
	updatedAt := firstAt.Add(time.Minute)
	first, err = database.UpdateSecurityFindingReview(ctx, firstID, 0, SecurityReviewAcknowledged, "analyst", updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != SecurityReviewAcknowledged || first.Revision != 1 || first.Actor != "analyst" || first.FirstSeen != first.LastSeen {
		t.Fatalf("unexpected reviewed state: %#v", first)
	}
	if _, err := database.UpdateSecurityFindingReview(ctx, firstID, 0, SecurityReviewResolved, "analyst", updatedAt.Add(time.Second)); !errors.Is(err, ErrSecurityFindingReviewConflict) {
		t.Fatalf("stale review revision returned %v", err)
	}
	history, err := database.SecurityFindingReviewHistory(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Revision != 0 || history[0].PreviousStatus != "" ||
		history[1].Revision != 1 || history[1].PreviousStatus != SecurityReviewOpen || history[1].Status != SecurityReviewAcknowledged {
		t.Fatalf("review history is not contiguous and auditable: %#v", history)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM security_finding_review_history WHERE fingerprint = ?`, firstID); err == nil {
		t.Fatal("append-only security review history allowed deletion")
	}

	secondAt := firstAt.Add(2 * time.Minute)
	if err := database.ReconcileSecurityFindingReviews(ctx, observations[:1], secondAt); err != nil {
		t.Fatal(err)
	}
	first, err = database.GetSecurityFindingReview(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.GetSecurityFindingReview(ctx, secondID)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Active || first.LastSeen != secondAt.Format(time.RFC3339Nano) || first.Status != SecurityReviewAcknowledged || first.Revision != 1 {
		t.Fatalf("complete scan failed to preserve reviewed state and refresh observation: %#v", first)
	}
	if second.Active || second.LastSeen != firstAt.Format(time.RFC3339Nano) {
		t.Fatalf("complete scan failed to mark an absent finding inactive: %#v", second)
	}
	items, total, err := database.ListSecurityFindingReviews(ctx, 10, 0)
	if err != nil || total != 2 || len(items) != 2 || !items[0].Active {
		t.Fatalf("unexpected bounded review list items=%#v total=%d err=%v", items, total, err)
	}
}

func TestSecurityFindingReviewRejectsUnsafeOrNonAtomicInputs(t *testing.T) {
	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	id := fmt.Sprintf("%064x", 3)
	valid := SecurityFindingObservation{Fingerprint: id, RuleID: "safe-rule", Severity: "low"}
	if err := database.ReconcileSecurityFindingReviews(ctx, []SecurityFindingObservation{valid}, at); err != nil {
		t.Fatal(err)
	}
	invalid := []SecurityFindingObservation{
		{Fingerprint: id, RuleID: "safe-rule", Severity: "low"},
		{Fingerprint: id, RuleID: "safe-rule", Severity: "low"},
	}
	if err := database.ReconcileSecurityFindingReviews(ctx, invalid, at.Add(time.Minute)); err == nil {
		t.Fatal("duplicate finding fingerprints were accepted")
	}
	current, err := database.GetSecurityFindingReview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Active || current.LastSeen != at.Format(time.RFC3339Nano) {
		t.Fatalf("rejected scan changed existing review state: %#v", current)
	}
	if _, err := database.UpdateSecurityFindingReview(ctx, id, 0, "ignored", "reviewer", at); err == nil {
		t.Fatal("unsupported review status was accepted")
	}
	if _, err := database.UpdateSecurityFindingReview(ctx, id, 0, SecurityReviewResolved, "api_key=secret", at); err == nil {
		t.Fatal("credential-bearing review actor was accepted")
	}
	if _, err := database.UpdateSecurityFindingReview(ctx, id, -1, SecurityReviewResolved, "reviewer", at); err == nil {
		t.Fatal("negative expected revision was accepted")
	}
	if _, _, err := database.ListSecurityFindingReviews(ctx, maxSecurityReviewPage+1, 0); err == nil {
		t.Fatal("unbounded security review page was accepted")
	}
}
