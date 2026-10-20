package security

import (
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve/threatrules"
)

func TestFindingFingerprintIsStableAndExcludesEventIdentifiers(t *testing.T) {
	first := FindingSummary{
		RuleID: "fixture-prompt", Severity: beacon.SeverityMedium,
		Posture: threatrules.PostureDetect, Reason: "sanitized reason", SessionReference: "safe-session-digest",
		Events: []EventReference{
			{EventID: "raw-event-id-a", At: "2026-10-01T00:00:00Z", Harness: "claude-code", Action: "prompt.submitted"},
			{EventID: "raw-event-id-b", At: "2026-10-01T00:00:01Z", Harness: "claude-code", Action: "tool.failed"},
		},
	}
	second := first
	second.Events = []EventReference{
		{EventID: "different-private-id", At: first.Events[1].At, Harness: first.Events[1].Harness, Action: first.Events[1].Action},
		{EventID: "another-private-id", At: first.Events[0].At, Harness: first.Events[0].Harness, Action: first.Events[0].Action},
	}
	got := FindingFingerprint(first)
	if len(got) != 64 || got != FindingFingerprint(second) {
		t.Fatalf("fingerprint must be a stable SHA-256 over sanitized evidence, got %q and %q", got, FindingFingerprint(second))
	}
	second.Reason = "changed reason"
	if got == FindingFingerprint(second) {
		t.Fatal("fingerprint did not change when the finding reason changed")
	}
}
