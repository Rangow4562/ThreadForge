package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/traces"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
)

func TestMemoryLearnFromCapturedSessionUsesBoundedPrivateObservations(t *testing.T) {
	root := t.TempDir()
	tracePath := filepath.Join(root, "private", "events.jsonl")
	trace, err := traces.Open(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "captured-session-private-id"
	const privatePrompt = "CAPTURED-SESSION-RAW-PROMPT-MUST-NOT-LEAK"
	const privatePath = "C:\\users\\private\\repo\\src\\agent.go"
	events := make([]beacon.Event, 40)
	for i := range events {
		event := beacon.NewEvent(beacon.NewEventOptions{
			Action: "test.failed", Harness: beacon.HarnessInfo{Name: "codex"},
			Message: fmt.Sprintf("captured-observation-%02d", i),
		})
		event.Session = &beacon.SessionInfo{ID: sessionID, WorkingDirectory: filepath.Dir(privatePath)}
		event.File = &beacon.FileInfo{Path: privatePath}
		event.Prompt = &beacon.PromptInfo{Text: privatePrompt}
		events[i] = event
	}
	if err := trace.Append(context.Background(), events); err != nil {
		t.Fatal(err)
	}

	modelResponse, err := json.Marshal(map[string]any{
		"schema_version": "threadforge.python.v1", "request_id": "memory-learn", "status": "ok",
		"result": map[string]any{
			"status": "candidate",
			"candidate": map[string]any{
				"schema_version": "threadforge.learning-candidate.v1",
				"title":          "Verify a failed test before retrying",
				"applicability":  map[string]any{"paths": []string{}, "modules": []string{}},
				"steps":          []map[string]string{{"kind": "action", "instruction": "Inspect the failing assertion."}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "threadforge.sqlite")
	runner := func(ctx context.Context, python string, requestBytes []byte) ([]byte, error) {
		if python != "fixture-python" {
			return nil, fmt.Errorf("unexpected python command %q", python)
		}
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("learning inference request omitted its timeout")
		}
		for _, secret := range []string{sessionID, privatePrompt, privatePath, root} {
			if bytes.Contains(requestBytes, []byte(secret)) {
				return nil, fmt.Errorf("private session data leaked to model: %q", secret)
			}
		}
		var request struct {
			Params struct {
				Observations []map[string]string `json:"observations"`
			} `json:"params"`
		}
		if err := json.Unmarshal(requestBytes, &request); err != nil || len(request.Params.Observations) != 32 {
			return nil, fmt.Errorf("expected the bounded 32-event model request: observations=%d err=%v", len(request.Params.Observations), err)
		}
		if request.Params.Observations[0]["summary"] != "captured-observation-08" ||
			request.Params.Observations[31]["summary"] != "captured-observation-39" {
			return nil, errors.New("learning did not use the most recent 32 session events")
		}
		for _, observation := range request.Params.Observations {
			if len(observation) != 4 || observation["harness"] != "codex" {
				return nil, errors.New("session observation was not normalized")
			}
		}
		return modelResponse, nil
	}

	var stdout, stderr bytes.Buffer
	code := learnMemoryWithRunner([]string{
		"--session-id", sessionID, "--harness", "codex", "--trace-path", tracePath,
		"--project-id", "repo:captured", "--model-path", "fixture-model",
		"--python", "fixture-python", "--db", databasePath,
	}, &stdout, &stderr, runner)
	var result struct {
		Status string               `json:"status"`
		Graph  proceduregraph.Graph `json:"graph"`
		Source struct {
			Kind               string `json:"kind"`
			Harness            string `json:"harness"`
			SessionSHA256      string `json:"session_sha256"`
			EventsObserved     int    `json:"events_observed"`
			EventsUsed         int    `json:"events_used"`
			OlderEventsOmitted bool   `json:"older_events_omitted"`
		} `json:"source"`
	}
	wantSessionDigest := digestBytes([]byte("codex_cli\x00" + sessionID))
	if code != 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || result.Status != "proposed" ||
		result.Source.Kind != "captured_session" || result.Source.Harness != "codex_cli" ||
		result.Source.SessionSHA256 != wantSessionDigest || result.Source.EventsObserved != 40 ||
		result.Source.EventsUsed != 32 || !result.Source.OlderEventsOmitted ||
		len(result.Graph.Evidence) != 1 || result.Graph.Evidence[0].LocalTraceRef != "session-sha256:"+wantSessionDigest ||
		proceduregraph.Validate(result.Graph) != nil || strings.Contains(stdout.String(), sessionID) ||
		strings.Contains(stdout.String(), privatePrompt) || strings.Contains(stdout.String(), privatePath) ||
		strings.Contains(stdout.String(), root) || strings.Contains(stderr.String(), tracePath) {
		t.Fatalf("captured-session learning code=%d stdout=%q stderr=%q result=%#v", code, stdout.String(), stderr.String(), result)
	}
}

func TestMemoryLearnFromCapturedSessionFailsClosedForAmbiguousOrMalformedTrace(t *testing.T) {
	for _, test := range []struct {
		name      string
		malformed bool
	}{
		{name: "ambiguous session"},
		{name: "malformed trace", malformed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			tracePath := filepath.Join(root, "events.jsonl")
			trace, err := traces.Open(tracePath)
			if err != nil {
				t.Fatal(err)
			}
			first := beacon.NewEvent(beacon.NewEventOptions{Action: "test.failed", Harness: beacon.HarnessInfo{Name: "codex"}})
			first.Session = &beacon.SessionInfo{ID: "same-private-session-id"}
			events := []beacon.Event{first}
			if !test.malformed {
				second := beacon.NewEvent(beacon.NewEventOptions{Action: "test.failed", Harness: beacon.HarnessInfo{Name: "opencode"}})
				second.Session = &beacon.SessionInfo{ID: "same-private-session-id"}
				events = append(events, second)
			}
			if err := trace.Append(context.Background(), events); err != nil {
				t.Fatal(err)
			}
			if test.malformed {
				file, err := os.OpenFile(tracePath, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.WriteString("not-json\n")
				closeErr := file.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatalf("append malformed fixture: write=%v close=%v", writeErr, closeErr)
				}
			}
			runnerCalled := false
			runner := func(context.Context, string, []byte) ([]byte, error) {
				runnerCalled = true
				return nil, errors.New("must not invoke model for unsafe source")
			}
			var stdout, stderr bytes.Buffer
			code := learnMemoryWithRunner([]string{
				"--session-id", "same-private-session-id", "--trace-path", tracePath,
				"--project-id", "repo:captured", "--model-path", "fixture-model",
			}, &stdout, &stderr, runner)
			if code != 1 || runnerCalled || strings.Contains(stderr.String(), "same-private-session-id") ||
				strings.Contains(stderr.String(), tracePath) || strings.Contains(stdout.String(), "same-private-session-id") {
				t.Fatalf("unsafe captured trace was not hidden/fail-closed: code=%d runner=%v stdout=%q stderr=%q",
					code, runnerCalled, stdout.String(), stderr.String())
			}
		})
	}
}
