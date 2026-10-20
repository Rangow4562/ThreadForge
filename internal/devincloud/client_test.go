package devincloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testClient(server *httptest.Server) *Client {
	client := NewClient("org-test_1", "cog_test-secret")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	client.retries = 0
	client.retryWait = 0
	return client
}

func TestListSessionsAndMessagesFollowV3CursorPages(t *testing.T) {
	var sessionPages, messagePages atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/organizations/org-test_1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cog_test-secret" || r.Header.Get("Accept") != "application/json" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			sessionPages.Add(1)
			_, _ = w.Write([]byte(`{"items":[{"session_id":"s_1","status":"working","user_id":"u_1","created_at":10,"updated_at":20}],"end_cursor":"sessions-page-2","has_next_page":true}`))
			return
		}
		sessionPages.Add(1)
		_, _ = w.Write([]byte(`{"items":[{"session_id":"s_2","status":"finished","user_id":"u_2","created_at":30,"updated_at":40}],"end_cursor":null,"has_next_page":false}`))
	})
	mux.HandleFunc("/v3/organizations/org-test_1/sessions/s_1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			messagePages.Add(1)
			_, _ = w.Write([]byte(`{"items":[{"event_id":"e_1","source":"user","message":"hello","created_at":11}],"end_cursor":"messages-page-2","has_next_page":true}`))
			return
		}
		messagePages.Add(1)
		_, _ = w.Write([]byte(`{"items":[{"event_id":"e_2","source":"devin","message":"done","created_at":12}],"end_cursor":null,"has_next_page":false}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := testClient(server)

	sessions, err := client.ListSessions(context.Background(), 2)
	if err != nil || len(sessions) != 2 || sessions[1].SessionID != "s_2" {
		t.Fatalf("ListSessions = %+v, %v", sessions, err)
	}
	messages, err := client.SessionMessages(context.Background(), "s_1", 2)
	if err != nil || len(messages) != 2 || messages[1].EventID != "e_2" {
		t.Fatalf("SessionMessages = %+v, %v", messages, err)
	}
	if sessionPages.Load() != 2 || messagePages.Load() != 2 {
		t.Fatalf("pagination calls = sessions %d, messages %d", sessionPages.Load(), messagePages.Load())
	}
}

func TestClientFailsClosedOnUnsafePaginationAndLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[],"end_cursor":"same","has_next_page":true}`))
	}))
	defer server.Close()
	client := testClient(server)
	if _, err := client.ListSessions(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "advancing cursor") {
		t.Fatalf("expected repeated cursor rejection, got %v", err)
	}
	if _, err := client.ListSessions(context.Background(), maxSessionLimit+1); err == nil {
		t.Fatal("accepted a session limit above the hard cap")
	}
	if _, err := client.SessionMessages(context.Background(), "../other", 10); err == nil {
		t.Fatal("accepted path-like session ID")
	}
}

func TestClientErrorDoesNotEchoCredentialOrResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"private message token=leaked-response-value"}`, http.StatusForbidden)
	}))
	defer server.Close()
	client := testClient(server)
	_, err := client.ListSessions(context.Background(), 10)
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "cog_test-secret") || strings.Contains(err.Error(), "leaked-response-value") {
		t.Fatalf("unsafe API error: %v", err)
	}
}

func TestClientDoesNotFollowRedirectsWithOrganizationCredential(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()
	client := NewClient("org-test_1", "cog_test-secret")
	client.baseURL = source.URL
	client.retries = 0
	if _, err := client.ListSessions(context.Background(), 10); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("expected redirect rejection, got %v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatal("followed cross-host API redirect")
	}
}

func TestClientRequiresSafeOrganizationAndCredentialValues(t *testing.T) {
	if _, err := NewClient("../org", "key").ListSessions(context.Background(), 1); err == nil {
		t.Fatal("accepted an unsafe organization ID")
	}
	if _, err := NewClient("org-valid", "bad\nkey").ListSessions(context.Background(), 1); err == nil {
		t.Fatal("accepted a header-injection credential")
	}
}
