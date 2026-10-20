package threatrules

import "fmt"

// CheckMaturity enforces the maturity-ladder gate for a rule's status. It assumes the
// rule has already passed Validate (so all fixtures and CEL are well-formed); it adds the
// status-dependent fixture-coverage requirements:
//
//   - experimental: >= 1 fixture (already guaranteed by Validate).
//   - stable:       >= 1 "match" fixture and >= 1 "no_match" fixture.
//   - deprecated:   no additional coverage requirement.
//
// It does not run the fixtures; the conformance harness does that separately.
func CheckMaturity(rule *Rule) error {
	switch rule.Status {
	case StatusExperimental, StatusDeprecated:
		return nil
	case StatusStable:
		var hasMatch, hasNoMatch bool
		for _, f := range rule.Tests {
			switch f.Verdict {
			case VerdictMatch:
				hasMatch = true
			case VerdictNoMatch:
				hasNoMatch = true
			}
		}
		if !hasMatch || !hasNoMatch {
			return fmt.Errorf("stable rule %q must have at least one %q and one %q fixture", rule.ID, VerdictMatch, VerdictNoMatch)
		}
		return nil
	default:
		return fmt.Errorf("rule %q has invalid status %q", rule.ID, rule.Status)
	}
}
