package threatrules

import (
	"strings"
	"testing"

	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

// validSingleEventRule returns a minimal, valid single-event rule that tests mutate to
// exercise individual validation failures.
func validSingleEventRule() *Rule {
	return &Rule{
		ID:       "test-rule",
		Version:  1,
		Title:    "Test rule",
		Severity: asymptoteobserve.SeverityMedium,
		Status:   StatusExperimental,
		Posture:  PostureDetect,
		Match:    `e.event.action == "file.read"`,
		Emit:     Emit{Reason: "test"},
		Tests: []Fixture{
			{Name: "pos", Verdict: VerdictMatch, Events: []FixtureEvent{
				{Event: asymptoteobserve.Event{Event: asymptoteobserve.EventInfo{Action: "file.read"}}},
			}},
		},
	}
}

func validCorrelationRule() *Rule {
	r := validSingleEventRule()
	r.Match = ""
	r.Correlation = &Correlation{
		Scope:  ScopeSession,
		Window: "120s",
		Steps: []CorrelationStep{
			{ID: "a", Match: `e.event.action == "file.read"`},
			{ID: "b", Match: `e.event.action == "command.executed"`},
		},
	}
	return r
}

func TestValidateAcceptsValidRules(t *testing.T) {
	if err := validSingleEventRule().Validate(); err != nil {
		t.Fatalf("single-event rule should be valid: %v", err)
	}
	if err := validCorrelationRule().Validate(); err != nil {
		t.Fatalf("correlation rule should be valid: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Rule)
		wantErr string
	}{
		{"bad id", func(r *Rule) { r.ID = "Bad_ID" }, "must match"},
		{"zero version", func(r *Rule) { r.Version = 0 }, "version must be"},
		{"missing title", func(r *Rule) { r.Title = "" }, "title is required"},
		{"bad severity", func(r *Rule) { r.Severity = "huge" }, "invalid severity"},
		{"bad status", func(r *Rule) { r.Status = "ga" }, "invalid status"},
		{"bad posture", func(r *Rule) { r.Posture = "block" }, "invalid posture"},
		{"empty reason", func(r *Rule) { r.Emit.Reason = "" }, "emit.reason is required"},
		{"no fixtures", func(r *Rule) { r.Tests = nil }, "at least one test fixture"},
		{"both match and correlation", func(r *Rule) {
			r.Correlation = validCorrelationRule().Correlation
		}, "both are present"},
		{"neither match nor correlation", func(r *Rule) { r.Match = "" }, "neither is present"},
		{"bad match expr", func(r *Rule) { r.Match = `e.nope.field == 1` }, "match:"},
		{"fixture missing name", func(r *Rule) { r.Tests[0].Name = "" }, "missing name"},
		{"fixture bad verdict", func(r *Rule) { r.Tests[0].Verdict = "maybe" }, "invalid verdict"},
		{"fixture no events", func(r *Rule) { r.Tests[0].Events = nil }, "at least one event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validSingleEventRule()
			tt.mutate(r)
			err := r.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateCorrelationRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Correlation)
		wantErr string
	}{
		{"bad scope", func(c *Correlation) { c.Scope = "global" }, "scope"},
		{"bad window", func(c *Correlation) { c.Window = "soon" }, "window"},
		{"too few steps", func(c *Correlation) { c.Steps = c.Steps[:1] }, ">= 2 steps"},
		{"step missing id", func(c *Correlation) { c.Steps[0].ID = "" }, "missing id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validCorrelationRule()
			tt.mutate(r.Correlation)
			err := r.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestDecodeRuleStrict(t *testing.T) {
	valid := `
id: decode-ok
version: 1
title: Decode OK
severity: low
status: experimental
posture: detect
match: 'e.event.action == "file.read"'
emit:
  reason: ok
tests:
  - name: p
    verdict: match
    events:
      - event: { action: file.read }
`
	rule, err := DecodeRule([]byte(valid))
	if err != nil {
		t.Fatalf("decode valid: %v", err)
	}
	if err := rule.Validate(); err != nil {
		t.Fatalf("validate decoded: %v", err)
	}
	if len(rule.Tests) != 1 || rule.Tests[0].Events[0].Event.Event.Action != "file.read" {
		t.Fatalf("fixture event decoded via JSON-tag names incorrectly: %+v", rule.Tests[0])
	}

	// Unknown top-level key must be rejected by strict decode.
	bad := valid + "\nunknown_key: oops\n"
	if _, err := DecodeRule([]byte(bad)); err == nil {
		t.Fatalf("expected strict-decode error for unknown key")
	}
}

func TestFixtureEventJSONFieldNames(t *testing.T) {
	// gen_ai is GenAI in Go; only the JSON-tag bridge maps it correctly.
	y := `
id: genai-rule
version: 1
title: GenAI
severity: info
status: experimental
posture: detect
match: 'e.gen_ai.usage.input_tokens > 100'
emit:
  reason: ok
tests:
  - name: p
    verdict: match
    events:
      - event: { action: gen_ai.call }
        gen_ai: { usage: { input_tokens: 500 } }
`
	rule, err := DecodeRule([]byte(y))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	ev := rule.Tests[0].Events[0].Event
	if ev.GenAI == nil || ev.GenAI.Usage == nil || ev.GenAI.Usage.InputTokens == nil {
		t.Fatalf("gen_ai.usage.input_tokens not decoded via json tags: %+v", ev.GenAI)
	}
	if *ev.GenAI.Usage.InputTokens != 500 {
		t.Fatalf("expected input_tokens 500, got %d", *ev.GenAI.Usage.InputTokens)
	}
}
