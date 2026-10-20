package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/decision"
)

func TestNativeDecisionBundleLifecycleIsAudited(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	bundle := nativeDecisionBundleFixture(t)
	if err := database.ImportDecisionBundle(ctx, bundle, "operator", "import complete AnyJev bundle for local review"); err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetDecisionBundle(ctx, bundle.ID)
	if err != nil || loaded.Status != decision.NativeBundleCandidate || loaded.Revision != 1 {
		t.Fatalf("candidate=%#v error=%v", loaded, err)
	}
	listed, total, err := database.ListDecisionBundles(ctx, string(decision.NativeBundleCandidate), 10, 0)
	if err != nil || total != 1 || len(listed) != 1 || listed[0].ID != bundle.ID {
		t.Fatalf("list=%#v total=%d error=%v", listed, total, err)
	}
	active, err := database.TransitionDecisionBundle(ctx, bundle.ID, 1, decision.NativeBundleActive, "reviewer", "reviewed bundle metadata and opted in", "", time.Now().UTC())
	if err != nil || active.Revision != 2 || active.Status != decision.NativeBundleActive {
		t.Fatalf("active=%#v error=%v", active, err)
	}
	if _, err := database.TransitionDecisionBundle(ctx, bundle.ID, 1, decision.NativeBundleRetired, "owner", "stale transition", "", time.Now().UTC()); !errors.Is(err, ErrDecisionBundleConflict) {
		t.Fatalf("stale transition error=%v", err)
	}
	retired, err := database.TransitionDecisionBundle(ctx, bundle.ID, 2, decision.NativeBundleRetired, "owner", "replace reviewed bundle", "", time.Now().UTC())
	if err != nil || retired.Revision != 3 || retired.Status != decision.NativeBundleRetired {
		t.Fatalf("retired=%#v error=%v", retired, err)
	}
	if _, err := database.TransitionDecisionBundle(ctx, bundle.ID, 3, decision.NativeBundleActive, "owner", "attempt revival", "", time.Now().UTC()); err == nil {
		t.Fatal("retired bundle was revived")
	}
	history, err := database.DecisionBundleHistory(ctx, bundle.ID)
	if err != nil || len(history) != 3 || history[0].Action != "imported" || history[1].Action != "approved" || history[2].Action != "retired" {
		t.Fatalf("history=%#v error=%v", history, err)
	}
	for _, item := range history {
		if item.Bundle.ID != bundle.ID || item.Bundle.ContentSHA256 != bundle.ContentSHA256 {
			t.Fatalf("history metadata mismatch: %#v", item)
		}
	}
	if _, err := database.db.Exec(`DELETE FROM decision_bundle_history WHERE bundle_id = ?`, bundle.ID); err == nil {
		t.Fatal("append-only decision bundle history allowed deletion")
	}
	if _, err := database.db.Exec(`DELETE FROM decision_bundle_audit WHERE bundle_id = ?`, bundle.ID); err == nil {
		t.Fatal("append-only decision bundle audit allowed deletion")
	}
}

func TestNativeDecisionBundleImportRejectsInvalidAuditAndDuplicate(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	bundle := nativeDecisionBundleFixture(t)
	if err := database.ImportDecisionBundle(context.Background(), bundle, "operator", "API_TOKEN=must-not-be-audit-text"); err == nil {
		t.Fatal("accepted credential-bearing audit metadata")
	}
	if err := database.ImportDecisionBundle(context.Background(), bundle, "operator", "first import"); err != nil {
		t.Fatal(err)
	}
	if err := database.ImportDecisionBundle(context.Background(), bundle, "operator", "duplicate import"); err == nil {
		t.Fatal("accepted duplicate bundle ID")
	}
	if _, err := database.GetDecisionBundle(context.Background(), "missing-bundle"); !errors.Is(err, ErrDecisionBundleNotFound) {
		t.Fatalf("missing bundle error=%v", err)
	}
}

func TestNativeDecisionBundleApprovalPersistsAndRetainsCheckpointFingerprint(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	bundle := nativeDecisionBundleFixture(t)
	if err := database.ImportDecisionBundle(ctx, bundle, "operator", "import for reviewed checkpoint binding"); err != nil {
		t.Fatal(err)
	}
	checkpointSHA256 := strings.Repeat("b", 64)
	active, err := database.TransitionDecisionBundle(ctx, bundle.ID, 1, decision.NativeBundleActive,
		"reviewer", "approved exact local checkpoint", checkpointSHA256, time.Now().UTC())
	if err != nil || active.ModelCheckpointSHA256 != checkpointSHA256 {
		t.Fatalf("approved bundle fingerprint=%q error=%v", active.ModelCheckpointSHA256, err)
	}
	loaded, err := database.GetDecisionBundle(ctx, bundle.ID)
	if err != nil || loaded.ModelCheckpointSHA256 != checkpointSHA256 {
		t.Fatalf("stored bundle fingerprint=%q error=%v", loaded.ModelCheckpointSHA256, err)
	}
	history, err := database.DecisionBundleHistory(ctx, bundle.ID)
	if err != nil || len(history) != 2 || history[1].Bundle.ModelCheckpointSHA256 != checkpointSHA256 {
		t.Fatalf("approval history=%#v error=%v", history, err)
	}
	retired, err := database.TransitionDecisionBundle(ctx, bundle.ID, 2, decision.NativeBundleRetired,
		"owner", "retire checkpoint-bound bundle", "", time.Now().UTC())
	if err != nil || retired.ModelCheckpointSHA256 != checkpointSHA256 {
		t.Fatalf("retired bundle lost checkpoint binding: %#v error=%v", retired, err)
	}
}

func TestNativeDecisionBundleReadRejectsChangedCheckpointBinding(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	bundle := nativeDecisionBundleFixture(t)
	if err := database.ImportDecisionBundle(ctx, bundle, "operator", "import for model binding integrity check"); err != nil {
		t.Fatal(err)
	}
	checkpointSHA256 := strings.Repeat("b", 64)
	if _, err := database.TransitionDecisionBundle(ctx, bundle.ID, 1, decision.NativeBundleActive,
		"reviewer", "approve model checkpoint", checkpointSHA256, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := database.db.QueryRow(`SELECT bundle_json FROM decision_bundles WHERE id = ?`, bundle.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(payload, checkpointSHA256, strings.Repeat("c", 64), 1)
	if tampered == payload {
		t.Fatal("test fixture did not contain the bound checkpoint fingerprint")
	}
	if _, err := database.db.Exec(`UPDATE decision_bundles SET bundle_json = ? WHERE id = ?`, tampered, bundle.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetDecisionBundle(ctx, bundle.ID); err == nil {
		t.Fatal("accepted a checkpoint binding changed outside its immutable approval history")
	}
	if _, _, err := database.ListDecisionBundles(ctx, string(decision.NativeBundleActive), 10, 0); err == nil {
		t.Fatal("listed a checkpoint binding changed outside its immutable approval history")
	}
}

func nativeDecisionBundleFixture(t *testing.T) decision.NativeBundleArtifact {
	t.Helper()
	content := []byte(`{"model":"local-model","artifacts":{},"heads":{},"adaptation":{},"observations":{}}`)
	receipt := decision.NativeBundleValidation{
		SchemaVersion: decision.NativeBundleValidationSchemaVersion,
		Status: "valid", Model: "local-model", ContentSHA256: strings.Repeat("a", 64),
		HumanReviewRequired: true,
	}
	bundle, err := decision.NewNativeBundleArtifact("native-bundle-one", content, receipt, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}
