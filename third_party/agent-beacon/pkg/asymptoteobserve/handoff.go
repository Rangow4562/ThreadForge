package asymptoteobserve

import (
	"fmt"
	"regexp"
)

// HandoffInfo links a new session to the session whose brief it was given.
// A marker is an observed claim, not verification of session identity.
type HandoffInfo struct {
	SourceHarness   string `json:"source_harness,omitempty"`
	SourceSessionID string `json:"source_session_id,omitempty"`
}

var (
	handoffMarkerPattern = regexp.MustCompile(`\[beacon-handoff from=([a-z0-9_-]{1,64}) session=([A-Za-z0-9._:\-]{1,200})\]`)
	handoffHarnessChars  = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
	handoffSessionChars  = regexp.MustCompile(`^[A-Za-z0-9._:\-]{1,200}$`)
)

// HandoffMarker returns a round-trippable marker, or empty for unsafe values.
func HandoffMarker(harness, sessionID string) string {
	harness = NormalizeHarnessName(harness)
	if !handoffHarnessChars.MatchString(harness) || !handoffSessionChars.MatchString(sessionID) {
		return ""
	}
	return fmt.Sprintf("[beacon-handoff from=%s session=%s]", harness, sessionID)
}

// ParseHandoffMarker reads the first explicit handoff marker in a prompt.
func ParseHandoffMarker(text string) (HandoffInfo, bool) {
	match := handoffMarkerPattern.FindStringSubmatch(text)
	if match == nil {
		return HandoffInfo{}, false
	}
	return HandoffInfo{SourceHarness: NormalizeHarnessName(match[1]), SourceSessionID: match[2]}, true
}
