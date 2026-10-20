package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/decision"
)

func observationCheckpointContent(states int, fits, nextFit int) json.RawMessage {
	labels := make([]int, states)
	stateValues := make([]string, states)
	for index := range stateValues {
		stateValues[index] = fmt.Sprintf("private labeled observation %d", index)
		labels[index] = index % 2
	}
	if states > 0 {
		stateValues[0] = "safe labeled state"
	}
	checkpoint := map[string]any{
		"schema_version": decision.ObservationCheckpointSchemaVersion,
		"model":          "local-model",
		"question_key":   "0123456789abcdef",
		"fit_at":         8,
		"refit_factor":   2.0,
		"fit_options": map[string]any{
			"layers": nil, "kinds": []string{"lda", "ridge"}, "n_folds": 5, "seed": 0, "listing": "auto",
		},
		"observation": map[string]any{
			"question_id": "q-check", "kind": "choice", "text": "Which check?",
			"options": []string{"unit", "integration"}, "scale": []float64{0, 1}, "ordered": false,
			"centers": nil, "states": stateValues, "labels": labels, "fits": fits, "next_fit": nextFit,
		},
	}
	encoded, _ := json.Marshal(checkpoint)
	return encoded
}

func observedHeadArtifact(id string, examples int, at time.Time) (decision.Artifact, error) {
	content := json.RawMessage(fmt.Sprintf(
		`{"model":"local-model","question":"0123456789abcdef","question_id":"q-check","kind":"choice","text":"Which check?","options":["unit","integration"],"method":"head:ridge","layer_abs":2,"n_blocks":4,"hidden_size":2,"temperature":1.1,"n_calib":%d,"W":[[1,0],[0,1]],"b":[0,0],"mean":[0,0],"scale":[1,1],"params":{},"cv":{}}`, examples))
	return decision.NewArtifact(id, "L2", content, at)
}

func TestDecisionObservationCheckpointCASAuditAndAtomicCandidate(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	first, err := decision.NewObservationCheckpoint("parser-observations", 1, observationCheckpointContent(1, 0, 8), at)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := database.CommitDecisionObservation(ctx, first, 0, "operator", "record first labeled sample", nil, at)
	if err != nil || committed.Revision != 1 {
		t.Fatalf("create observation checkpoint=%#v error=%v", committed, err)
	}

	second, err := decision.NewObservationCheckpoint("parser-observations", 2, observationCheckpointContent(8, 1, 16), at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := observedHeadArtifact("parser-fit-1", 8, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	committed, err = database.CommitDecisionObservation(ctx, second, 1, "operator", "fit reached its reviewed threshold", &candidate, at.Add(time.Minute))
	if err != nil || committed.Revision != 2 {
		t.Fatalf("advance observation checkpoint=%#v error=%v", committed, err)
	}
	loaded, err := database.GetDecisionObservation(ctx, "parser-observations")
	if err != nil || loaded.Revision != 2 || loaded.ObservationCount != 8 || loaded.TotalFits != 1 {
		t.Fatalf("loaded checkpoint=%#v error=%v", loaded, err)
	}
	loadedArtifact, err := database.GetDecisionArtifact(ctx, candidate.ID)
	if err != nil || loadedArtifact.Status != decision.ArtifactCandidate || loadedArtifact.Level != "L2" {
		t.Fatalf("fitted head was not stored as an unapproved candidate: artifact=%#v error=%v", loadedArtifact, err)
	}
	history, err := database.DecisionObservationHistory(ctx, "parser-observations")
	if err != nil || len(history) != 2 || history[0].Action != "created" || history[1].Action != "updated" || history[1].ArtifactID != candidate.ID {
		t.Fatalf("observation history=%#v error=%v", history, err)
	}
	artifactHistory, err := database.DecisionArtifactHistory(ctx, candidate.ID)
	if err != nil || len(artifactHistory) != 1 || artifactHistory[0].Action != "observed" {
		t.Fatalf("observed candidate audit=%#v error=%v", artifactHistory, err)
	}

	stale, err := decision.NewObservationCheckpoint("parser-observations", 2, observationCheckpointContent(9, 1, 16), at.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CommitDecisionObservation(ctx, stale, 1, "operator", "stale append", nil, at.Add(2*time.Minute)); !errors.Is(err, ErrDecisionObservationConflict) {
		t.Fatalf("stale observation update error=%v", err)
	}

	third, err := decision.NewObservationCheckpoint("parser-observations", 3, observationCheckpointContent(16, 2, 32), at.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := observedHeadArtifact(candidate.ID, 16, at.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CommitDecisionObservation(ctx, third, 2, "operator", "force atomic artifact collision", &duplicate, at.Add(3*time.Minute)); err == nil {
		t.Fatal("duplicate artifact ID unexpectedly committed")
	}
	afterRollback, err := database.GetDecisionObservation(ctx, "parser-observations")
	if err != nil || afterRollback.Revision != 2 || afterRollback.ObservationCount != 8 {
		t.Fatalf("failed candidate insert did not roll back checkpoint: checkpoint=%#v error=%v", afterRollback, err)
	}
	afterHistory, err := database.DecisionObservationHistory(ctx, "parser-observations")
	if err != nil || len(afterHistory) != 2 {
		t.Fatalf("failed transaction left observation audit rows: history=%#v error=%v", afterHistory, err)
	}
}

func TestDecisionObservationCheckpointRejectsRewrittenPrefix(t *testing.T) {
	at := time.Now().UTC()
	previous, err := decision.NewObservationCheckpoint("parser-observations", 1, observationCheckpointContent(2, 0, 8), at)
	if err != nil {
		t.Fatal(err)
	}
	nextContent := observationCheckpointContent(3, 0, 8)
	var value map[string]any
	if err := json.Unmarshal(nextContent, &value); err != nil {
		t.Fatal(err)
	}
	observation := value["observation"].(map[string]any)
	states := observation["states"].([]any)
	states[0] = "rewritten prior sample"
	value["observation"] = observation
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	next, err := decision.NewObservationCheckpoint("parser-observations", 2, encoded, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := decision.ValidateObservationCheckpointAppend(previous, next); err == nil {
		t.Fatal("rewritten observation prefix was accepted")
	}
}
