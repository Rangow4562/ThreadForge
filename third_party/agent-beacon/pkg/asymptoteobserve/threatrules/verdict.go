package threatrules

// Verdict is the two-state result an evaluator produces for a rule against an ordered
// event sequence. It is the conformance contract: any engine must produce the verdict a
// rule's fixtures declare.
type Verdict string

const (
	// VerdictMatch means the rule fired.
	VerdictMatch Verdict = "match"
	// VerdictNoMatch means the rule did not fire.
	VerdictNoMatch Verdict = "no_match"
)

func (v Verdict) valid() bool {
	return v == VerdictMatch || v == VerdictNoMatch
}
