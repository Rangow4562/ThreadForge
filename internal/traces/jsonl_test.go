package traces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

func TestAppendRedactsSecretsAndKeepsTraceLocal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local", "events.jsonl")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	event := beacon.NewEvent(beacon.NewEventOptions{
		Action: "tool.invoked", Harness: beacon.HarnessInfo{Name: "test-harness"},
		Message: "authorization: Bearer abcdefghijklmnop",
	})
	if err := store.Append(context.Background(), []beacon.Event{event}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "abcdefghijklmnop") || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("trace secret was not redacted: %s", data)
	}
	var saved beacon.Event
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Harness.Name != "test-harness" {
		t.Fatalf("unexpected saved harness %q", saved.Harness.Name)
	}
}

func TestAppendRejectsInvalidBatchWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	good := beacon.NewEvent(beacon.NewEventOptions{Action: "tool.invoked", Harness: beacon.HarnessInfo{Name: "ok"}})
	bad := good
	bad.Harness.Name = ""
	if err := store.Append(context.Background(), []beacon.Event{good, bad}); err == nil {
		t.Fatal("expected invalid event to reject the full batch")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("invalid batch wrote partial trace data: %s", data)
	}
}

func TestAppendRejectsBatchAboveHandoffExpandedLimitWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	event := beacon.NewEvent(beacon.NewEventOptions{Action: "tool.invoked", Harness: beacon.HarnessInfo{Name: "test-harness"}})
	events := make([]beacon.Event, maxTraceBatchEvents+1)
	for i := range events {
		events[i] = event
	}
	if err := store.Append(context.Background(), events); err == nil {
		t.Fatal("expected oversized trace event count to be rejected")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("oversized batch wrote trace data: %s", data)
	}
}

func TestDashboardStatsReportsLatestObservedTimestampPerHarness(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	earlier := beacon.NewEvent(beacon.NewEventOptions{
		Action: "tool.invoked", Harness: beacon.HarnessInfo{Name: "codex"},
	})
	earlier.Timestamp = "2026-09-25T11:00:00Z"
	later := beacon.NewEvent(beacon.NewEventOptions{
		Action: "tool.completed", Harness: beacon.HarnessInfo{Name: "codex"},
	})
	later.Timestamp = "2026-09-25T12:00:00Z"
	other := beacon.NewEvent(beacon.NewEventOptions{
		Action: "session.started", Harness: beacon.HarnessInfo{Name: "claude-code"},
	})
	other.Timestamp = "2026-09-25T10:30:00Z"
	if err := store.Append(context.Background(), []beacon.Event{earlier, later, other}); err != nil {
		t.Fatal(err)
	}
	stats, err := store.DashboardStats(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SampledEvents != 3 || stats.ByHarness["codex"] != 2 ||
		stats.LastSeenByHarness["codex"] != "2026-09-25T12:00:00Z" ||
		stats.LastSeenByHarness["claude-code"] != "2026-09-25T10:30:00Z" {
		t.Fatalf("dashboard stats have incorrect runtime activity: %#v", stats)
	}
}
