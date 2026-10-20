package traces

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

const MaxEventBytes = 64 * 1024
const maxTraceBatchEvents = 200
const dashboardTailBytes = 16 << 20

type RecentEvent struct {
	Timestamp string `json:"timestamp"`
	Harness   string `json:"harness"`
	Action    string `json:"action"`
	Severity  string `json:"severity"`
}

type DashboardStats struct {
	SampledEvents     int               `json:"sampled_events"`
	Malformed         int               `json:"malformed_lines"`
	Truncated         bool              `json:"older_events_omitted"`
	ByHarness         map[string]int    `json:"by_harness"`
	LastSeenByHarness map[string]string `json:"last_seen_by_harness"`
	ByAction          map[string]int    `json:"by_action"`
	Recent            []RecentEvent     `json:"recent"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("trace JSONL path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create private trace directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open local trace JSONL: %w", err)
	}
	if err := file.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		_ = file.Close()
		return nil, fmt.Errorf("restrict trace file permissions: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return &Store{path: path}, nil
}

// Append stores only validated canonical events after Beacon's secret redaction
// and field-size limits. Raw trace material never leaves this local JSONL file.
func (s *Store) Append(ctx context.Context, events []beacon.Event) error {
	if len(events) == 0 {
		return errors.New("at least one trace event is required")
	}
	if len(events) > maxTraceBatchEvents {
		return fmt.Errorf("trace batch exceeds %d-event storage limit", maxTraceBatchEvents)
	}
	var payload bytes.Buffer
	encoder := json.NewEncoder(&payload)
	for i, input := range events {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		input.Raw = redactMap(input.Raw)
		event := beacon.SanitizeEvent(input, MaxEventBytes)
		if err := event.Validate(); err != nil {
			return fmt.Errorf("event %d: %w", i, err)
		}
		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("encode event %d: %w", i, err)
		}
		if payload.Len() > MaxEventBytes*maxTraceBatchEvents {
			return fmt.Errorf("trace batch exceeds %d-event storage limit", maxTraceBatchEvents)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open local trace JSONL: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(payload.Bytes()); err != nil {
		return fmt.Errorf("append local trace JSONL: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync local trace JSONL: %w", err)
	}
	return nil
}

func (s *Store) Path() string { return s.path }

// DashboardStats scans only the bounded tail of the local event file and omits
// message bodies, raw attributes, user identities, and project paths.
func (s *Store) DashboardStats(ctx context.Context, maxRecent int) (DashboardStats, error) {
	if maxRecent < 1 || maxRecent > 500 {
		return DashboardStats{}, errors.New("dashboard recent-event limit must be 1-500")
	}
	file, err := os.Open(s.path)
	if err != nil {
		return DashboardStats{}, fmt.Errorf("open local trace JSONL: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return DashboardStats{}, err
	}
	start := info.Size() - dashboardTailBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return DashboardStats{}, err
	}
	limited := io.LimitReader(file, info.Size()-start)
	reader := bufio.NewReader(limited)
	if start > 0 {
		if _, err := reader.ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
			return DashboardStats{}, err
		}
	}
	stats := DashboardStats{
		Truncated: start > 0, ByHarness: map[string]int{}, LastSeenByHarness: map[string]string{},
		ByAction: map[string]int{}, Recent: []RecentEvent{},
	}
	lastSeen := map[string]time.Time{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), MaxEventBytes+1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return DashboardStats{}, ctx.Err()
		default:
		}
		var event beacon.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			stats.Malformed++
			continue
		}
		stats.SampledEvents++
		stats.ByHarness[event.Harness.Name]++
		stats.ByAction[event.Event.Action]++
		if timestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp); err == nil && timestamp.After(lastSeen[event.Harness.Name]) {
			lastSeen[event.Harness.Name] = timestamp
			stats.LastSeenByHarness[event.Harness.Name] = timestamp.UTC().Format(time.RFC3339Nano)
		}
		stats.Recent = append(stats.Recent, RecentEvent{
			Timestamp: event.Timestamp, Harness: event.Harness.Name,
			Action: event.Event.Action, Severity: string(event.Severity),
		})
		if len(stats.Recent) > maxRecent {
			stats.Recent = stats.Recent[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return DashboardStats{}, fmt.Errorf("scan local trace JSONL: %w", err)
	}
	return stats, nil
}

func redactMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	output := make(map[string]interface{}, len(input))
	for key, value := range input {
		if sensitiveKey(key) {
			output[key] = "[REDACTED]"
			continue
		}
		output[key] = redactValue(value)
	}
	return output
}

func redactValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return redactMap(typed)
	case []interface{}:
		output := make([]interface{}, len(typed))
		for index, item := range typed {
			output[index] = redactValue(item)
		}
		return output
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	compact := strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == '.' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToLower(key))
	for _, marker := range []string{"apikey", "token", "secret", "password", "authorization", "credential", "privatekey", "cookie"} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}
