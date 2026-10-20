package threatrules

import (
	"testing"

	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

func matchFixture() Fixture {
	return Fixture{Name: "m", Verdict: VerdictMatch, Events: []FixtureEvent{
		{Event: asymptoteobserve.Event{Event: asymptoteobserve.EventInfo{Action: "file.read"}}},
	}}
}

func noMatchFixture() Fixture {
	return Fixture{Name: "n", Verdict: VerdictNoMatch, Events: []FixtureEvent{
		{Event: asymptoteobserve.Event{Event: asymptoteobserve.EventInfo{Action: "tool.invoked"}}},
	}}
}

func TestCheckMaturity(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		fixtures []Fixture
		wantErr  bool
	}{
		{"experimental one fixture", StatusExperimental, []Fixture{matchFixture()}, false},
		{"deprecated no extra coverage", StatusDeprecated, []Fixture{matchFixture()}, false},
		{"stable with both", StatusStable, []Fixture{matchFixture(), noMatchFixture()}, false},
		{"stable missing no_match", StatusStable, []Fixture{matchFixture()}, true},
		{"stable missing match", StatusStable, []Fixture{noMatchFixture()}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validSingleEventRule()
			r.Status = tt.status
			r.Tests = tt.fixtures
			err := CheckMaturity(r)
			if tt.wantErr && err == nil {
				t.Fatalf("expected maturity error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected maturity error: %v", err)
			}
		})
	}
}
