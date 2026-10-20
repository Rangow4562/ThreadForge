package devincloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const orgFixture = "org-test_1"

func pullServer(t *testing.T, sessions string, messageBodies map[string]string, messageHandler func(http.ResponseWriter, *http.Request, string)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/organizations/"+orgFixture+"/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cog_test-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(sessions))
	})
	for sessionID, body := range messageBodies {
		sid, response := sessionID, body
		mux.HandleFunc("/v3/organizations/"+orgFixture+"/sessions/"+sid+"/messages", func(w http.ResponseWriter, r *http.Request) {
			if messageHandler != nil {
				messageHandler(w, r, sid)
				return
			}
			_, _ = w.Write([]byte(response))
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func pullOptions(root string) PullOptions {
	return PullOptions{
		MaxSessions: 10, MaxMessages: 100,
		StatePath: filepath.Join(root, "state", "devin-cloud.v1.json"),
		TracePath: filepath.Join(root, "traces", "events.jsonl"),
	}
}

func TestPullOnceStoresDigestFirstLocalEventsAndIsIdempotent(t *testing.T) {
	const prompt = "private prompt phrase unique-7aa31"
	const answer = "private assistant response unique-992bb"
	sessions := `{"items":[{"session_id":"private-session-id","status":"suspended","user_id":"private-user-id","created_at":100,"updated_at":200,"acus_consumed":2.5,"title":"private title","url":"https://private.invalid/session","pull_requests":[{"url":"https://github.com/private/repo/pull/9","number":9}]}],"has_next_page":false}`
	messages := `{"items":[{"event_id":"private-event-1","source":"user","message":"` + prompt + `","created_at":110},{"event_id":"private-event-2","source":"devin","message":"` + answer + `","created_at":120}],"has_next_page":false}`
	server := pullServer(t, sessions, map[string]string{"private-session-id": messages}, nil)
	client := testClient(server)
	root := t.TempDir()
	options := pullOptions(root)

	summary, err := PullOnce(context.Background(), client, options)
	if err != nil || !summary.Complete || summary.Sessions != 1 || summary.SessionsChanged != 1 || summary.EventsEmitted != 4 {
		t.Fatalf("first pull summary=%+v err=%v", summary, err)
	}
	traceBytes, err := os.ReadFile(options.TracePath)
	if err != nil {
		t.Fatal(err)
	}
	trace := string(traceBytes)
	for _, private := range []string{prompt, answer, "private-session-id", "private-user-id", "private-event-1", "private title", "private.invalid", "github.com/private/repo"} {
		if strings.Contains(trace, private) {
			t.Fatalf("metadata-only trace leaked %q: %s", private, trace)
		}
	}
	if !strings.Contains(trace, `"action":"session.started"`) || !strings.Contains(trace, `"action":"prompt.submitted"`) ||
		!strings.Contains(trace, `"action":"agent.message"`) || !strings.Contains(trace, `"action":"session.ended"`) ||
		!strings.Contains(trace, `"retention":"metadata"`) || !strings.Contains(trace, `"collection_method":"poll"`) {
		t.Fatalf("trace omitted expected event/provenance metadata: %s", trace)
	}
	stateBytes, err := os.ReadFile(options.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-session-id", "private-user-id", "private-event-1", "cog_test-secret"} {
		if strings.Contains(string(stateBytes), private) {
			t.Fatalf("cursor state leaked %q: %s", private, stateBytes)
		}
	}

	second, err := PullOnce(context.Background(), client, options)
	if err != nil || second.EventsEmitted != 0 || second.SessionsChanged != 0 {
		t.Fatalf("repeat pull summary=%+v err=%v", second, err)
	}
	traceAfter, err := os.ReadFile(options.TracePath)
	if err != nil || !bytes.Equal(traceBytes, traceAfter) {
		t.Fatalf("idempotent pull changed trace bytes: err=%v", err)
	}
}

func TestPullOnceIncludesOnlyExplicitlyOptedInRedactedContent(t *testing.T) {
	const prompt = "deploy with token=super-secret-value-987 and keep this phrase"
	sessions := `{"items":[{"session_id":"session-1","status":"working","user_id":"user-1","created_at":10,"updated_at":20}],"has_next_page":false}`
	messages := `{"items":[{"event_id":"event-1","source":"user","message":"` + prompt + `","created_at":11}],"has_next_page":false}`
	server := pullServer(t, sessions, map[string]string{"session-1": messages}, nil)
	client := testClient(server)
	root := t.TempDir()
	options := pullOptions(root)
	options.IncludeContent = true
	if _, err := PullOnce(context.Background(), client, options); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(options.TracePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "super-secret-value-987") || !strings.Contains(string(data), "keep this phrase") ||
		!strings.Contains(string(data), `"retention":"redacted"`) || !strings.Contains(string(data), `"redacted":true`) {
		t.Fatalf("opt-in content was not bounded/redacted/marked correctly: %s", data)
	}
}

func TestPrintPreviewDoesNotWriteTraceOrCursor(t *testing.T) {
	sessions := `{"items":[{"session_id":"session-private","status":"finished","user_id":"user-private","created_at":10,"updated_at":20}],"has_next_page":false}`
	messages := `{"items":[{"event_id":"event-private","source":"user","message":"sensitive prompt text","created_at":11}],"has_next_page":false}`
	server := pullServer(t, sessions, map[string]string{"session-private": messages}, nil)
	client := testClient(server)
	root := t.TempDir()
	options := pullOptions(root)
	options.Print = true
	var preview bytes.Buffer
	options.Out = &preview
	summary, err := PullOnce(context.Background(), client, options)
	if err != nil || summary.EventsEmitted != 3 {
		t.Fatalf("preview summary=%+v err=%v", summary, err)
	}
	if strings.Contains(preview.String(), "sensitive prompt text") || strings.Contains(preview.String(), "session-private") {
		t.Fatalf("preview leaked private content or IDs: %s", preview.String())
	}
	if _, err := os.Stat(options.TracePath); !os.IsNotExist(err) {
		t.Fatalf("preview created trace path: %v", err)
	}
	if _, err := os.Stat(options.StatePath); !os.IsNotExist(err) {
		t.Fatalf("preview created cursor path: %v", err)
	}
}

func TestPullContinuesAfterOneSessionFailsAndRetriesItWithoutReemittingSuccess(t *testing.T) {
	sessions := `{"items":[{"session_id":"s1","status":"working","user_id":"u1","created_at":10,"updated_at":20},{"session_id":"s2","status":"working","user_id":"u2","created_at":10,"updated_at":20}],"has_next_page":false}`
	var secondCalls atomic.Int32
	server := pullServer(t, sessions, map[string]string{
		"s1": `{"items":[{"event_id":"e1","source":"user","message":"first","created_at":11}],"has_next_page":false}`,
		"s2": `{"items":[{"event_id":"e2","source":"user","message":"second","created_at":12}],"has_next_page":false}`,
	}, func(w http.ResponseWriter, r *http.Request, sessionID string) {
		if sessionID == "s2" && secondCalls.Add(1) == 1 {
			http.Error(w, "sensitive backend body", http.StatusInternalServerError)
			return
		}
		body := `{"items":[{"event_id":"e1","source":"user","message":"first","created_at":11}],"has_next_page":false}`
		if sessionID == "s2" {
			body = `{"items":[{"event_id":"e2","source":"user","message":"second","created_at":12}],"has_next_page":false}`
		}
		_, _ = w.Write([]byte(body))
	})
	client := testClient(server)
	client.retries = 0
	options := pullOptions(t.TempDir())
	first, err := PullOnce(context.Background(), client, options)
	if err == nil || first.Complete || first.Errors != 1 || first.EventsEmitted != 2 {
		t.Fatalf("partial first sweep=%+v err=%v", first, err)
	}
	second, err := PullOnce(context.Background(), client, options)
	if err != nil || !second.Complete || second.SessionsChanged != 1 || second.EventsEmitted != 2 {
		t.Fatalf("retry sweep=%+v err=%v", second, err)
	}
	trace, err := os.ReadFile(options.TracePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(trace), `"action":"session.started"`) != 2 || strings.Contains(string(trace), "sensitive backend body") {
		t.Fatalf("retry re-emitted a completed session or leaked API body: %s", trace)
	}
}

func TestPullEmitsASecondEndedEventAfterAnObservedResume(t *testing.T) {
	var sessionCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/organizations/"+orgFixture+"/sessions", func(w http.ResponseWriter, _ *http.Request) {
		call := sessionCalls.Add(1)
		status, updated := "suspended", 200
		if call == 2 {
			status, updated = "working", 300
		} else if call >= 3 {
			status, updated = "suspended", 400
		}
		_, _ = fmt.Fprintf(w, `{"items":[{"session_id":"s1","status":%q,"user_id":"u1","created_at":100,"updated_at":%d}],"has_next_page":false}`, status, updated)
	})
	mux.HandleFunc("/v3/organizations/"+orgFixture+"/sessions/s1/messages", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"event_id":"e1","source":"user","message":"hello","created_at":110}],"has_next_page":false}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := testClient(server)
	options := pullOptions(t.TempDir())
	first, err := PullOnce(context.Background(), client, options)
	if err != nil || first.EventsEmitted != 3 {
		t.Fatalf("first sweep=%+v err=%v", first, err)
	}
	resume, err := PullOnce(context.Background(), client, options)
	if err != nil || resume.EventsEmitted != 0 || resume.SessionsChanged != 1 {
		t.Fatalf("resume sweep=%+v err=%v", resume, err)
	}
	ended, err := PullOnce(context.Background(), client, options)
	if err != nil || ended.EventsEmitted != 1 {
		t.Fatalf("second terminal sweep=%+v err=%v", ended, err)
	}
	data, err := os.ReadFile(options.TracePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"action":"session.ended"`) != 2 {
		t.Fatalf("expected one end event for each terminal episode: %s", data)
	}
}

func TestPullFailsWhenAPIResultExceedsConfiguredSessionLimit(t *testing.T) {
	server := pullServer(t, `{"items":[{"session_id":"s1"},{"session_id":"s2"}],"has_next_page":false}`, nil, nil)
	options := pullOptions(t.TempDir())
	options.MaxSessions = 1
	_, err := PullOnce(context.Background(), testClient(server), options)
	if err == nil || !strings.Contains(err.Error(), "exceeds configured limit") {
		t.Fatalf("expected bounded session failure, got %v", err)
	}
}

func TestMappedEventsAreValidAndUseHashedIdentities(t *testing.T) {
	items := MapSession(orgFixture, Session{SessionID: "source-session", UserID: "source-user", Status: "working", CreatedAt: 10}, []Message{{EventID: "source-event", Source: "user", Message: "private"}}, false)
	if len(items) != 2 {
		t.Fatalf("mapped %d events, want 2", len(items))
	}
	for _, item := range items {
		if err := item.Event.Validate(); err != nil {
			t.Fatalf("invalid event: %v", err)
		}
		encoded, _ := json.Marshal(item.Event)
		for _, raw := range []string{"source-session", "source-user", "source-event", "private"} {
			if strings.Contains(string(encoded), raw) {
				t.Fatalf("event leaked %q: %s", raw, encoded)
			}
		}
	}
}
