package devincloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL      = "https://api.devin.ai"
	maxResponseBytes    = 16 << 20
	maxSessionListBytes = 64 << 20
	maxMessageBytes     = 128 << 20
	maxPages            = 100
	maxSessionLimit     = 5000
	maxMessageLimit     = 50000
	maxOrgIDBytes       = 132
	maxSessionIDBytes   = 512
)

var orgIDPattern = regexp.MustCompile(`^org-[A-Za-z0-9_-]{1,128}$`)
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,512}$`)
var apiCredentialPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)

// Client reads session metadata and messages from Devin's organization API.
// It has no write methods, and production callers cannot override the API host.
type Client struct {
	baseURL    string
	orgID      string
	apiKey     string
	httpClient *http.Client
	retries    int
	retryWait  time.Duration
}

// Session is the bounded subset used by Threadforge. Sensitive free-text
// fields such as title, status detail, URLs, tags, and repository paths are not
// decoded or persisted.
type Session struct {
	SessionID       string        `json:"session_id"`
	Status          string        `json:"status"`
	UserID          string        `json:"user_id"`
	CreatedAt       int64         `json:"created_at"`
	UpdatedAt       int64         `json:"updated_at"`
	ACUsConsumed    *float64      `json:"acus_consumed"`
	PullRequests    []PullRequest `json:"pull_requests"`
	ParentSessionID string        `json:"parent_session_id"`
	ChildSessionIDs []string      `json:"child_session_ids"`
}

type PullRequest struct {
	Number int `json:"number"`
}

type Message struct {
	EventID   string `json:"event_id"`
	Source    string `json:"source"`
	Message   string `json:"message"`
	CreatedAt int64  `json:"created_at"`
}

type page[T any] struct {
	Items       []T     `json:"items"`
	EndCursor   *string `json:"end_cursor"`
	HasNextPage bool    `json:"has_next_page"`
}

// NewClient constructs a read-only Devin v3 organization API client.
func NewClient(orgID, apiKey string) *Client {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// Do not forward an organization credential through redirects.
			return http.ErrUseLastResponse
		},
	}
	return &Client{
		baseURL: defaultBaseURL, orgID: strings.TrimSpace(orgID), apiKey: strings.TrimSpace(apiKey),
		httpClient: httpClient, retries: 3, retryWait: time.Second,
	}
}

func (c *Client) validate() error {
	if len(c.orgID) > maxOrgIDBytes || !orgIDPattern.MatchString(c.orgID) {
		return errors.New("a valid Devin organization ID is required")
	}
	if len(c.apiKey) == 0 || len(c.apiKey) > 16*1024 || !apiCredentialPattern.MatchString(c.apiKey) {
		return errors.New("DEVIN_API_KEY is missing or invalid")
	}
	return nil
}

// ListSessions reads all visible organization sessions, failing rather than
// silently truncating when maxSessions is reached.
func (c *Client) ListSessions(ctx context.Context, maxSessions int) ([]Session, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if maxSessions < 1 || maxSessions > maxSessionLimit {
		return nil, fmt.Errorf("max sessions must be 1-%d", maxSessionLimit)
	}
	var all []Session
	aggregateBytes := 0
	cursor := ""
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		var response page[Session]
		if err := c.getJSON(ctx, c.sessionsPath(), cursor, &response); err != nil {
			return nil, err
		}
		if len(response.Items) > maxSessions-len(all) {
			return nil, fmt.Errorf("Devin session result exceeds configured limit %d", maxSessions)
		}
		for _, item := range response.Items {
			if !sessionIDPattern.MatchString(item.SessionID) || item.SessionID == "." || item.SessionID == ".." ||
				len(item.Status) > 64 || len(item.UserID) > 512 || len(item.ChildSessionIDs) > 100 || len(item.PullRequests) > 100 {
				return nil, errors.New("Devin API session page contains an invalid or oversized field")
			}
			itemBytes := len(item.SessionID) + len(item.Status) + len(item.UserID) + 64
			for _, childID := range item.ChildSessionIDs {
				if len(childID) > maxSessionIDBytes {
					return nil, errors.New("Devin API session page contains an oversized child session ID")
				}
				itemBytes += len(childID)
			}
			aggregateBytes += itemBytes
			if aggregateBytes > maxSessionListBytes {
				return nil, errors.New("Devin session metadata exceeds the 64 MiB safety limit")
			}
		}
		all = append(all, response.Items...)
		next, done, err := nextCursor(cursor, response.EndCursor, response.HasNextPage)
		if err != nil {
			return nil, err
		}
		if done {
			return all, nil
		}
		cursor = next
	}
	return nil, errors.New("Devin session pagination exceeded the 100-page safety limit")
}

// SessionMessages reads all messages for one session, failing on a configured
// limit or non-advancing pagination rather than reporting an incomplete import
// as complete.
func (c *Client) SessionMessages(ctx context.Context, sessionID string, maxMessages int) ([]Message, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if !sessionIDPattern.MatchString(sessionID) || sessionID == "." || sessionID == ".." || len(sessionID) > maxSessionIDBytes {
		return nil, errors.New("Devin session ID is invalid")
	}
	if maxMessages < 1 || maxMessages > maxMessageLimit {
		return nil, fmt.Errorf("max messages must be 1-%d", maxMessageLimit)
	}
	var all []Message
	aggregateBytes := 0
	cursor := ""
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		var response page[Message]
		endpoint := c.sessionsPath() + "/" + url.PathEscape(sessionID) + "/messages"
		if err := c.getJSON(ctx, endpoint, cursor, &response); err != nil {
			return nil, err
		}
		if len(response.Items) > maxMessages-len(all) {
			return nil, fmt.Errorf("Devin message result exceeds configured limit %d", maxMessages)
		}
		for _, item := range response.Items {
			if len(item.EventID) > 512 || len(item.Source) > 64 {
				return nil, errors.New("Devin API message page contains an oversized identifier or source")
			}
			aggregateBytes += len(item.EventID) + len(item.Source) + len(item.Message) + 64
			if aggregateBytes > maxMessageBytes {
				return nil, errors.New("Devin session messages exceed the 128 MiB safety limit")
			}
		}
		all = append(all, response.Items...)
		next, done, err := nextCursor(cursor, response.EndCursor, response.HasNextPage)
		if err != nil {
			return nil, err
		}
		if done {
			return all, nil
		}
		cursor = next
	}
	return nil, errors.New("Devin message pagination exceeded the 100-page safety limit")
}

func (c *Client) sessionsPath() string {
	return "/v3/organizations/" + url.PathEscape(c.orgID) + "/sessions"
}

func nextCursor(current string, end *string, hasNext bool) (string, bool, error) {
	if !hasNext {
		return "", true, nil
	}
	if end == nil || strings.TrimSpace(*end) == "" || *end == current || len(*end) > 4096 {
		return "", false, errors.New("Devin API pagination did not provide a safe advancing cursor")
	}
	return *end, false, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint, cursor string, target any) error {
	parsed, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return errors.New("Devin API URL could not be constructed")
	}
	if cursor != "" {
		query := parsed.Query()
		query.Set("cursor", cursor)
		parsed.RawQuery = query.Encode()
	}
	var lastErr error
	waitOverride := time.Duration(0)
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			wait := c.retryWait * time.Duration(attempt)
			if waitOverride > wait {
				wait = waitOverride
			}
			if wait > 30*time.Second {
				wait = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return errors.New("Devin API request could not be constructed")
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Accept", "application/json")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = errors.New("Devin API transport request failed")
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = errors.New("Devin API response could not be read")
			continue
		}
		if len(body) > maxResponseBytes {
			return errors.New("Devin API response exceeded the 16 MiB safety limit")
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("Devin API temporarily returned HTTP %d", resp.StatusCode)
			if resp.StatusCode == http.StatusTooManyRequests {
				if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); retryAfter > waitOverride {
					if retryAfter > 30*time.Second {
						retryAfter = 30 * time.Second
					}
					waitOverride = retryAfter
				}
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("Devin API returned HTTP %d", resp.StatusCode)
		}
		if err := json.Unmarshal(body, target); err != nil {
			return errors.New("Devin API response was not valid JSON for the expected page schema")
		}
		return nil
	}
	return lastErr
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		if seconds > 30 {
			seconds = 30
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if wait := time.Until(at); wait > 0 {
			return wait
		}
	}
	return 0
}
