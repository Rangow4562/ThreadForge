// Copyright (c) 2026 Asymptote Labs.
// Threadforge-authored; licensed under Apache-2.0. See the repository-root LICENSE.
package handoffbridge

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/endpoint/schema"
	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/handoff"
)

const (
	ReadRequestSchemaVersion  = "threadforge.native-session-read-request.v1"
	ReadResponseSchemaVersion = "threadforge.native-session-events.v1"
)

type ReadRequest struct {
	SchemaVersion string            `json:"schema_version"`
	Harness       string            `json:"harness"`
	SessionID     string            `json:"session_id"`
	StoreDirs     map[string]string `json:"store_dirs,omitempty"`
}

// NativeSession omits the private source path used internally to re-open the store record.
type NativeSession struct {
	Harness      string    `json:"harness"`
	ID           string    `json:"id"`
	Title        string    `json:"title,omitempty"`
	Directory    string    `json:"directory,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Store        string    `json:"store,omitempty"`
	Key          string    `json:"key,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
	Subagent     bool      `json:"subagent,omitempty"`
	ParentID     string    `json:"parent_id,omitempty"`
	ResumePath   string    `json:"-"`
	NativeResume bool      `json:"-"`
}

type ReadResponse struct {
	SchemaVersion string         `json:"schema_version"`
	Session       NativeSession  `json:"session"`
	Events        []schema.Event `json:"events"`
	Truncated     bool           `json:"truncated"`
}

// Read resolves one explicitly selected local session and returns bounded mapped events. Raw
// record maps and source paths are removed; the caller must still sanitize event fields before rendering.
func Read(request ReadRequest) (ReadResponse, error) {
	if request.SchemaVersion != ReadRequestSchemaVersion {
		return ReadResponse{}, errors.New("unsupported native-session read schema")
	}
	if !supportedHarness(request.Harness) || strings.TrimSpace(request.SessionID) == "" || len(request.SessionID) > 256 || strings.ContainsRune(request.SessionID, '\x00') {
		return ReadResponse{}, errors.New("native-session read requires a supported harness and bounded session id")
	}
	for harness, path := range request.StoreDirs {
		if !supportedHarness(harness) || !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsRune(path, '\x00') {
			return ReadResponse{}, errors.New("store override must use a supported harness and an absolute local path")
		}
		if runtime.GOOS == "windows" && strings.HasPrefix(path, `\\`) {
			return ReadResponse{}, errors.New("network paths are not accepted for native local-session reads")
		}
	}
	sources := handoff.DefaultSources(handoff.StoreDirs(request.StoreDirs))
	session, err := handoff.Find(sources, request.Harness, request.SessionID)
	if err != nil {
		return ReadResponse{}, err
	}
	events, err := handoff.Events(sources, session)
	truncated := errors.Is(err, handoff.ErrNativeSessionEventLimit)
	if err != nil && !truncated {
		return ReadResponse{}, err
	}
	for i := range events {
		// Raw reader maps can contain local transcript filenames and duplicated source content;
		// the brief builder consumes the normalized event fields instead.
		events[i].Raw = nil
	}
	return ReadResponse{
		SchemaVersion: ReadResponseSchemaVersion,
		Session: NativeSession{
			Harness: session.Harness, ID: session.ID, Title: session.Title, Directory: session.Directory,
			Branch: session.Branch, Store: session.Store, Key: session.Key, UpdatedAt: session.UpdatedAt,
			Subagent: session.Subagent, ParentID: session.ParentID, ResumePath: session.SourcePath,
			NativeResume: handoff.NativeResumeAvailable(session),
		},
		Events: events, Truncated: truncated,
	}, nil
}

func supportedHarness(name string) bool {
	switch name {
	case handoff.HarnessClaude, handoff.HarnessCodex, handoff.HarnessOpenCode, handoff.HarnessCline,
		handoff.HarnessPi, handoff.HarnessPrime, handoff.HarnessFactory, handoff.HarnessCopilot,
		handoff.HarnessHermes, handoff.HarnessDSH, handoff.HarnessFx, handoff.HarnessGrok,
		handoff.HarnessOpenClaw, handoff.HarnessCursor:
		return true
	default:
		return false
	}
}
