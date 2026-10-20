package threatrules

import (
	"testing"

	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

func TestSingleEventEvaluate(t *testing.T) {
	c, err := Compile(validSingleEventRule())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	match, err := c.Evaluate([]asymptoteobserve.Event{
		{Event: asymptoteobserve.EventInfo{Action: "tool.invoked"}},
		{Event: asymptoteobserve.EventInfo{Action: "file.read"}},
	})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if match != VerdictMatch {
		t.Fatalf("want match (one event satisfies), got %s", match)
	}

	noMatch, err := c.Evaluate([]asymptoteobserve.Event{
		{Event: asymptoteobserve.EventInfo{Action: "tool.invoked"}},
	})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if noMatch != VerdictNoMatch {
		t.Fatalf("want no_match, got %s", noMatch)
	}
}

func TestCompiledRuleReusable(t *testing.T) {
	// The same compiled rule must give stable results across repeated calls (no leaked
	// per-event state).
	c, err := Compile(validSingleEventRule())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	match := []asymptoteobserve.Event{{Event: asymptoteobserve.EventInfo{Action: "file.read"}}}
	for i := 0; i < 3; i++ {
		v, err := c.Evaluate(match)
		if err != nil || v != VerdictMatch {
			t.Fatalf("call %d: v=%s err=%v", i, v, err)
		}
	}
}

func TestCompileRejectsInvalidRule(t *testing.T) {
	r := validSingleEventRule()
	r.Match = `e.bogus.field == 1`
	if _, err := Compile(r); err == nil {
		t.Fatalf("expected compile to reject invalid rule")
	}
}
