// Package handoffbridge exposes a small public, versioned boundary around the
// selected Beacon native session readers. Threadforge does not import Beacon's
// internal packages directly.
package handoffbridge

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/handoff"
	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/sessionlimits"
)

const (
	RequestSchemaVersion  = "threadforge.native-session-list-request.v1"
	ResponseSchemaVersion = "threadforge.native-session-list.v1"
	MaxSessions           = 10000
)

type Request struct {
	SchemaVersion    string            `json:"schema_version"`
	Harness          string            `json:"harness,omitempty"`
	Directory        string            `json:"directory,omitempty"`
	IncludeSubagents bool              `json:"include_subagents,omitempty"`
	Limit            int               `json:"limit"`
	StoreDirs        map[string]string `json:"store_dirs,omitempty"`
}

type Warning struct {
	Harness string `json:"harness"`
	Code    string `json:"code"`
}

type Response struct {
	SchemaVersion string            `json:"schema_version"`
	Sessions      []handoff.Session `json:"sessions"`
	Warnings      []Warning         `json:"warnings"`
}

// List reads only the selected native stores. Store read failures become
// path-free per-harness warnings; other stores can still return results.
func List(request Request) (Response, error) {
	if request.SchemaVersion != RequestSchemaVersion {
		return Response{}, errors.New("unsupported native-session request schema")
	}
	if request.Limit < 0 || request.Limit > MaxSessions {
		return Response{}, fmt.Errorf("session limit must be 0..%d", MaxSessions)
	}
	if request.Directory != "" {
		if !filepath.IsAbs(request.Directory) || len(request.Directory) > 4096 || strings.ContainsRune(request.Directory, '\x00') {
			return Response{}, errors.New("directory filter must be a bounded absolute local path")
		}
	}
	if runtime.GOOS == "windows" && (strings.HasPrefix(request.Directory, `\\`) || hasNetworkStore(request.StoreDirs)) {
		return Response{}, errors.New("network paths are not accepted for native local-session discovery")
	}
	allowed := map[string]bool{
		handoff.HarnessClaude: true, handoff.HarnessCodex: true, handoff.HarnessOpenCode: true,
		handoff.HarnessCline: true, handoff.HarnessPi: true, handoff.HarnessPrime: true,
		handoff.HarnessFactory: true, handoff.HarnessCopilot: true, handoff.HarnessHermes: true,
		handoff.HarnessDSH: true, handoff.HarnessFx: true, handoff.HarnessGrok: true,
		handoff.HarnessOpenClaw: true, handoff.HarnessCursor: true,
	}
	if request.Harness != "" && !allowed[request.Harness] {
		return Response{}, errors.New("unsupported native-session harness")
	}
	for harness, path := range request.StoreDirs {
		if !allowed[harness] || !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsRune(path, '\x00') {
			return Response{}, errors.New("store override must use a supported harness and an absolute local path")
		}
	}
	sessions, err := handoff.List(handoff.DefaultSources(handoff.StoreDirs(request.StoreDirs)), handoff.Filter{
		Harness: request.Harness, Directory: request.Directory,
		IncludeSubagents: request.IncludeSubagents, Limit: request.Limit,
	})
	response := Response{SchemaVersion: ResponseSchemaVersion, Sessions: sessions, Warnings: warnings(err)}
	if response.Sessions == nil {
		response.Sessions = []handoff.Session{}
	}
	if response.Warnings == nil {
		response.Warnings = []Warning{}
	}
	return response, nil
}

func warnings(err error) []Warning {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var result []Warning
		for _, child := range joined.Unwrap() {
			result = append(result, warnings(child)...)
		}
		return result
	}
	var source *handoff.SourceError
	if errors.As(err, &source) {
		if errors.Is(source.Err, sessionlimits.ErrScanLimit) {
			return []Warning{{Harness: source.Harness, Code: "scan_truncated"}}
		}
		return []Warning{{Harness: source.Harness, Code: "store_unavailable"}}
	}
	if errors.Is(err, sessionlimits.ErrScanLimit) {
		return []Warning{{Code: "scan_truncated"}}
	}
	return []Warning{{Code: "store_unavailable"}}
}

func hasNetworkStore(dirs map[string]string) bool {
	for _, path := range dirs {
		if strings.HasPrefix(path, `\\`) {
			return true
		}
	}
	return false
}
