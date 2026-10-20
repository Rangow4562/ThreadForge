package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

type fingerprintEvent struct {
	Timestamp string `json:"timestamp"`
	Harness   string `json:"harness"`
	Action    string `json:"action"`
}

type fingerprintPayload struct {
	RuleID           string             `json:"rule_id"`
	Severity         string             `json:"severity"`
	Posture          string             `json:"posture"`
	Reason           string             `json:"reason"`
	SessionReference string             `json:"session_reference,omitempty"`
	Events           []fingerprintEvent `json:"events"`
}

// FindingFingerprint creates a stable opaque identifier from sanitized
// finding metadata. It intentionally excludes raw event IDs and content.
func FindingFingerprint(finding FindingSummary) string {
	payload := fingerprintPayload{
		RuleID: finding.RuleID, Severity: string(finding.Severity), Posture: string(finding.Posture),
		Reason: finding.Reason, SessionReference: finding.SessionReference,
		Events: make([]fingerprintEvent, 0, len(finding.Events)),
	}
	for _, event := range finding.Events {
		payload.Events = append(payload.Events, fingerprintEvent{
			Timestamp: event.At, Harness: event.Harness, Action: event.Action,
		})
	}
	sort.Slice(payload.Events, func(i, j int) bool {
		left, right := payload.Events[i], payload.Events[j]
		if left.Timestamp != right.Timestamp {
			return left.Timestamp < right.Timestamp
		}
		if left.Harness != right.Harness {
			return left.Harness < right.Harness
		}
		return left.Action < right.Action
	})
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
