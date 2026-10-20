package threatrules

import (
	"testing"

	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

// These negative-control tests assert that the conformance machinery FAILS when it
// should — so a green pack-conformance run is meaningful. They use synthetic in-memory
// rules and never touch the real pack.

func TestCheckRuleRejectsBogusField(t *testing.T) {
	r := validSingleEventRule()
	r.Match = `e.totally.not.a.field == 1`
	if _, err := CheckRule(r); err == nil {
		t.Fatalf("expected CheckRule to reject a rule referencing an unknown field")
	}
}

func TestCheckRuleEnforcesMaturityGate(t *testing.T) {
	r := validSingleEventRule()
	r.Status = StatusStable
	// Stable but only a match fixture (no no_match) — maturity gate must fail.
	r.Tests = []Fixture{matchFixture()}
	if _, err := CheckRule(r); err == nil {
		t.Fatalf("expected CheckRule to fail the maturity gate for a stable rule lacking a no_match fixture")
	}
}

func TestCheckRuleDetectsFixtureMismatch(t *testing.T) {
	r := validSingleEventRule()
	// Fixture claims match, but the event cannot satisfy the rule's expression.
	r.Tests = []Fixture{
		{Name: "wrong", Verdict: VerdictMatch, Events: []FixtureEvent{
			{Event: asymptoteobserve.Event{Event: asymptoteobserve.EventInfo{Action: "tool.invoked"}}},
		}},
	}
	results, err := CheckRule(r)
	if err != nil {
		t.Fatalf("rule itself is valid, expected no rule-level error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].OK() {
		t.Fatalf("expected fixture to fail (declared match but event does not satisfy rule)")
	}
}
