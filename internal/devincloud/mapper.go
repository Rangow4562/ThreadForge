package devincloud

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

const (
	harnessName   = "devin"
	runProvider   = "devin_cloud"
	maxStoredText = 4096
)

var safeStatus = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

type mappedEvent struct {
	DedupeID string
	Event    beacon.Event
}

// MapSession converts one API snapshot into deterministic session and message
// events. Raw IDs are replaced with stable SHA-256 aliases. Content is digest-
// only unless includeContent was explicitly selected by the operator.
func MapSession(orgID string, session Session, messages []Message, includeContent bool) []mappedEvent {
	ordered := append([]Message(nil), messages...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].CreatedAt < ordered[j].CreatedAt })
	seen := make(map[string]bool, len(ordered))
	out := []mappedEvent{lifecycleEvent(orgID, session, "session.started", session.CreatedAt)}
	for index, message := range ordered {
		source := strings.ToLower(strings.TrimSpace(message.Source))
		if source == "" {
			continue
		}
		dedup := messageIdentity(orgID, session.SessionID, message, index, seen)
		out = append(out, messageEvent(orgID, session, message, source == "user", dedup, includeContent))
	}
	return out
}

func EndedEvent(orgID string, session Session) mappedEvent {
	return lifecycleEvent(orgID, session, "session.ended", session.UpdatedAt)
}

func IsTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "finished", "expired", "suspended":
		return true
	default:
		return false
	}
}

func IsFinal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "finished", "expired":
		return true
	default:
		return false
	}
}

func lifecycleEvent(orgID string, session Session, action string, unixSeconds int64) mappedEvent {
	identityInput := orgID + "\x00" + session.SessionID + "\x00" + action
	if action == "session.ended" {
		identityInput += fmt.Sprintf("\x00%d", session.UpdatedAt)
	}
	identity := hashedID(identityInput)
	event := baseEvent(orgID, session, action, "session", unixSeconds, identity)
	meta := map[string]interface{}{}
	if status := normalizedStatus(session.Status); status != "" {
		meta["status"] = status
	}
	if session.ACUsConsumed != nil && validNumber(*session.ACUsConsumed) {
		meta["acus_consumed"] = *session.ACUsConsumed
	}
	if action == "session.ended" && session.CreatedAt > 0 && session.UpdatedAt >= session.CreatedAt {
		meta["duration_seconds"] = session.UpdatedAt - session.CreatedAt
	}
	if session.ParentSessionID != "" {
		meta["parent_session_id"] = hashedID(orgID + "\x00" + session.ParentSessionID)
	}
	if len(session.ChildSessionIDs) > 0 {
		children := make([]string, 0, min(len(session.ChildSessionIDs), 100))
		for _, id := range session.ChildSessionIDs {
			if len(children) == 100 {
				break
			}
			if strings.TrimSpace(id) != "" {
				children = append(children, hashedID(orgID+"\x00"+id))
			}
		}
		if len(children) > 0 {
			meta["child_session_ids"] = children
		}
	}
	if len(session.PullRequests) > 0 {
		numbers := make([]int, 0, min(len(session.PullRequests), 100))
		for _, pr := range session.PullRequests {
			if len(numbers) == 100 {
				break
			}
			if pr.Number > 0 {
				numbers = append(numbers, pr.Number)
			}
		}
		if len(numbers) > 0 {
			meta["pull_request_numbers"] = numbers
		}
	}
	if len(meta) > 0 {
		event.Raw = map[string]interface{}{"devin": meta}
	}
	return mappedEvent{DedupeID: identity, Event: event}
}

func messageEvent(orgID string, session Session, message Message, userMessage bool, identity string, includeContent bool) mappedEvent {
	action, category, label := "agent.message", "session", "Devin Cloud agent message"
	if userMessage {
		action, category, label = "prompt.submitted", "prompt", "Devin Cloud user prompt"
	}
	event := baseEvent(orgID, session, action, category, message.CreatedAt, identity)
	event.Message = label
	if message.Message != "" {
		limit := beacon.DefaultRawStringLimit
		content := contentMarker(message.Message, false, limit)
		if includeContent {
			retained := beacon.CleanString(message.Message, limit, true)
			content = contentMarker(message.Message, true, limit)
			if userMessage {
				event.Prompt = &beacon.PromptInfo{Text: retained}
				event.GenAI = &beacon.GenAIInfo{Input: &beacon.GenAIInputInfo{Messages: beacon.TextInputMessages(retained)}}
			} else {
				event.GenAI = &beacon.GenAIInfo{Output: &beacon.GenAIOutputInfo{Messages: beacon.TextOutputMessages(retained)}}
			}
		}
		event.Content = content
	}
	return mappedEvent{DedupeID: identity, Event: event}
}

func baseEvent(orgID string, session Session, action, category string, unixSeconds int64, identity string) beacon.Event {
	timestamp := time.Now().UTC()
	if unixSeconds > 0 {
		timestamp = time.Unix(unixSeconds, 0).UTC()
	}
	event := beacon.NewEvent(beacon.NewEventOptions{
		Action: action, Category: category, Severity: beacon.SeverityInfo,
		Harness: beacon.HarnessInfo{Name: harnessName, CollectionMethod: beacon.CollectionMethodPoll},
		Origin:  beacon.OriginCloud, Fidelity: beacon.FidelityInferred,
		Run: &beacon.RunInfo{
			Provider: runProvider,
			RunID:    hashedID(orgID + "\x00" + session.SessionID),
		},
	})
	if session.UserID != "" {
		event.Run.Actor = hashedID(orgID + "\x00user\x00" + session.UserID)
	}
	event.Timestamp = beacon.FormatTimestamp(timestamp)
	event.Endpoint = beacon.EndpointInfo{OS: "unknown"}
	event.User = beacon.UserInfo{}
	event.Session = &beacon.SessionInfo{ID: hashedID(orgID + "\x00" + session.SessionID)}
	event.Event.ID = identity
	return event
}

func messageIdentity(orgID, sessionID string, message Message, index int, seen map[string]bool) string {
	sourceID := strings.TrimSpace(message.EventID)
	if sourceID == "" || seen[sourceID] {
		sourceID = fmt.Sprintf("fallback:%d:%d:%s", index, message.CreatedAt, hashedID(message.Message))
	}
	seen[sourceID] = true
	return hashedID(orgID + "\x00" + sessionID + "\x00message\x00" + sourceID)
}

func contentMarker(text string, included bool, limit int) *beacon.ContentInfo {
	if text == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(text))
	marker := &beacon.ContentInfo{
		Retention: beacon.ContentRetentionMetadata,
		Included:  included,
		Hash:      hex.EncodeToString(hash[:]),
		Bytes:     len(text),
	}
	if included {
		marker.Retention = beacon.ContentRetentionFull
		marker.Redacted = beacon.RedactString(text) != text
		if marker.Redacted {
			marker.Retention = beacon.ContentRetentionRedacted
		}
	}
	marker.Truncated = included && len(text) > limit
	return marker
}

func normalizedStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if safeStatus.MatchString(value) {
		return value
	}
	return ""
}

func validNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func hashedID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
