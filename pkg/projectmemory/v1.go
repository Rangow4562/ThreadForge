package projectmemory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
)

const SchemaVersion = "threadforge.project-memory.v1"
const SchemaVersionV2 = "threadforge.project-memory.v2"

type Kind string

const (
	KindFact     Kind = "fact"
	KindDecision Kind = "decision"
	KindPolicy   Kind = "policy"
)

type Evidence struct {
	Origin        string       `json:"origin"`
	ContentSHA256 string       `json:"content_sha256"`
	Harness       string       `json:"harness,omitempty"`
	Redacted      bool         `json:"redacted"`
	SourceFiles   []SourceFile `json:"source_files,omitempty"`
}

type SourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Approval struct {
	ApprovedBy      string `json:"approved_by"`
	ApprovedAt      string `json:"approved_at"`
	Reason          string `json:"reason"`
	PrivacyReviewed bool   `json:"privacy_reviewed"`
}

type Applicability struct {
	Modules []string `json:"modules,omitempty"`
	Paths   []string `json:"paths,omitempty"`
}

// Candidate is the local input used to prepare a human-reviewed project file.
// It contains no caller-controlled revision, approval, status, or chain hash.
type Candidate struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	ProjectID     string          `json:"project_id"`
	Kind          Kind            `json:"kind"`
	Title         string          `json:"title"`
	Applicability Applicability   `json:"applicability,omitempty"`
	ConflictKey   string          `json:"conflict_key,omitempty"`
	SupersedesID  string          `json:"supersedes_id,omitempty"`
	Content       json.RawMessage `json:"content"`
	Evidence      []Evidence      `json:"evidence"`
}

// Record is an approved, shareable project-memory revision. Raw traces, local
// paths, credentials, and personal preferences have no fields in this schema.
type Record struct {
	SchemaVersion  string          `json:"schema_version"`
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	Kind           Kind            `json:"kind"`
	Title          string          `json:"title"`
	Applicability  Applicability   `json:"applicability,omitempty"`
	ConflictKey    string          `json:"conflict_key,omitempty"`
	SupersedesID   string          `json:"supersedes_id,omitempty"`
	Revision       int             `json:"revision"`
	Status         string          `json:"status"`
	Content        json.RawMessage `json:"content"`
	ContentSHA256  string          `json:"content_sha256"`
	PreviousSHA256 string          `json:"previous_sha256,omitempty"`
	Evidence       []Evidence      `json:"evidence"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	Approval       *Approval       `json:"approval"`
}

// ReadResponseV2 is the exact local/MCP project-memory read contract. An
// unverified legacy record may be returned only with status=incomplete;
// stale or unreadable source-bound records have no Record.
type ReadResponseV2 struct {
	SchemaVersion       string   `json:"schema_version"`
	Status              string   `json:"status"`
	Freshness           string   `json:"freshness"`
	ProjectID           string   `json:"project_id"`
	ID                  string   `json:"id"`
	Kind                Kind     `json:"kind"`
	Revision            int      `json:"revision,omitempty"`
	Record              *Record  `json:"record,omitempty"`
	Gaps                []string `json:"gaps"`
	ActionReady         bool     `json:"action_ready"`
	GuidanceOnly        bool     `json:"guidance_only"`
	ExecutionAuthorized bool     `json:"execution_authorized"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Validate(record Record) error {
	var problems []string
	if record.SchemaVersion != SchemaVersion && record.SchemaVersion != SchemaVersionV2 {
		problems = append(problems, "unsupported schema_version")
	}
	if !idPattern.MatchString(record.ID) {
		problems = append(problems, "id must be a lowercase stable identifier")
	}
	if strings.TrimSpace(record.ProjectID) == "" || len(record.ProjectID) > 256 {
		problems = append(problems, "project_id must contain 1 to 256 characters")
	}
	if record.Kind != KindFact && record.Kind != KindDecision && record.Kind != KindPolicy {
		problems = append(problems, "kind must be fact, decision, or policy")
	}
	if strings.TrimSpace(record.Title) == "" || len(record.Title) > 240 {
		problems = append(problems, "title must contain 1 to 240 characters")
	}
	if len(record.Applicability.Modules) > 64 || len(record.Applicability.Paths) > 64 {
		problems = append(problems, "applicability must contain no more than 64 modules and 64 path patterns")
	}
	for index, module := range record.Applicability.Modules {
		if strings.TrimSpace(module) == "" || len(module) > 128 {
			problems = append(problems, fmt.Sprintf("applicability.modules[%d] is invalid", index))
		}
	}
	for index, pattern := range record.Applicability.Paths {
		if !validPathPattern(pattern) {
			problems = append(problems, fmt.Sprintf("applicability.paths[%d] is invalid", index))
		}
	}
	if record.ConflictKey != "" && !idPattern.MatchString(record.ConflictKey) {
		problems = append(problems, "conflict_key must be a lowercase stable identifier")
	}
	if record.SupersedesID != "" && (!idPattern.MatchString(record.SupersedesID) || record.SupersedesID == record.ID) {
		problems = append(problems, "supersedes_id must identify a different stable record")
	}
	if record.Revision < 1 {
		problems = append(problems, "revision must be at least 1")
	}
	if record.Status != "approved" {
		problems = append(problems, "only approved records may be stored as project memory")
	}
	if !json.Valid(record.Content) || len(record.Content) == 0 || record.Content[0] != '{' || len(record.Content) > 64<<10 {
		problems = append(problems, "content must be a JSON object no larger than 64 KiB")
	} else if contentDigest(record.Content) != record.ContentSHA256 {
		problems = append(problems, "content_sha256 does not match content")
	}
	if record.Revision == 1 && record.PreviousSHA256 != "" {
		problems = append(problems, "revision 1 cannot have previous_sha256")
	}
	if record.Revision > 1 && !sha256Pattern.MatchString(record.PreviousSHA256) {
		problems = append(problems, "revisions after 1 require a previous_sha256 digest")
	}
	if len(record.Evidence) == 0 || len(record.Evidence) > 100 {
		problems = append(problems, "evidence must contain between 1 and 100 entries")
	}
	seenSourceFiles := make(map[string]bool)
	totalSourceFiles := 0
	for index, evidence := range record.Evidence {
		switch evidence.Origin {
		case "trace", "document", "human", "benchmark":
		default:
			problems = append(problems, fmt.Sprintf("evidence[%d].origin is unknown", index))
		}
		if !sha256Pattern.MatchString(evidence.ContentSHA256) {
			problems = append(problems, fmt.Sprintf("evidence[%d].content_sha256 must be a lowercase SHA-256 digest", index))
		}
		if !evidence.Redacted {
			problems = append(problems, fmt.Sprintf("evidence[%d] must be marked redacted", index))
		}
		if privacy.ContainsCredentialText(evidence.Harness) {
			problems = append(problems, fmt.Sprintf("evidence[%d].harness contains credential material", index))
		}
		if record.SchemaVersion == SchemaVersion && len(evidence.SourceFiles) != 0 {
			problems = append(problems, fmt.Sprintf("evidence[%d].source_files requires project-memory v2", index))
		}
		if record.SchemaVersion == SchemaVersionV2 && len(evidence.SourceFiles) == 0 {
			problems = append(problems, fmt.Sprintf("evidence[%d].source_files must bind v2 evidence to at least one source file", index))
		}
		for sourceIndex, source := range evidence.SourceFiles {
			totalSourceFiles++
			if totalSourceFiles > 64 {
				problems = append(problems, "evidence source_files must contain no more than 64 files in total")
				break
			}
			if err := proceduregraph.ValidateRepositoryPath(source.Path); err != nil || privacy.ContainsCredentialPath(source.Path) {
				problems = append(problems, fmt.Sprintf("evidence[%d].source_files[%d].path is unsafe", index, sourceIndex))
			}
			if !sha256Pattern.MatchString(source.SHA256) {
				problems = append(problems, fmt.Sprintf("evidence[%d].source_files[%d].sha256 must be a lowercase SHA-256 digest", index, sourceIndex))
			}
			pathKey := strings.ToLower(source.Path)
			if pathKey == "" || seenSourceFiles[pathKey] {
				problems = append(problems, fmt.Sprintf("evidence[%d].source_files[%d].path is duplicated", index, sourceIndex))
			}
			if strings.HasPrefix(pathKey, ".threadforge/memory/records/") {
				problems = append(problems, fmt.Sprintf("evidence[%d].source_files[%d].path cannot refer to project-memory history", index, sourceIndex))
			}
			seenSourceFiles[pathKey] = true
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, record.CreatedAt); err != nil {
		problems = append(problems, "created_at must use RFC3339")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.UpdatedAt); err != nil {
		problems = append(problems, "updated_at must use RFC3339")
	}
	if record.Approval == nil || strings.TrimSpace(record.Approval.ApprovedBy) == "" || strings.TrimSpace(record.Approval.Reason) == "" {
		problems = append(problems, "approved records require a reviewer and reason")
	} else {
		if _, err := time.Parse(time.RFC3339Nano, record.Approval.ApprovedAt); err != nil {
			problems = append(problems, "approval.approved_at must use RFC3339")
		}
		if !record.Approval.PrivacyReviewed {
			problems = append(problems, "project memory requires an explicit privacy review")
		}
		if privacy.ContainsCredentialText(record.Approval.ApprovedBy) || privacy.ContainsCredentialText(record.Approval.Reason) {
			problems = append(problems, "approval metadata contains credential material")
		}
	}
	if privacy.ContainsCredentialText(record.ProjectID) || privacy.ContainsCredentialText(record.Title) || privacy.ContainsCredential(record.Content) {
		problems = append(problems, "project memory contains credential material")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func sha256Digest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func contentDigest(value []byte) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		return ""
	}
	return sha256Digest(compact.Bytes())
}

func Applicable(record Record, paths, modules []string) bool {
	if len(record.Applicability.Modules) > 0 {
		if len(modules) == 0 {
			return false
		}
		moduleMatches := false
		for _, required := range record.Applicability.Modules {
			for _, current := range modules {
				if required == current {
					moduleMatches = true
				}
			}
		}
		if !moduleMatches {
			return false
		}
	}
	if len(record.Applicability.Paths) > 0 && len(paths) == 0 {
		return false
	}
	if len(record.Applicability.Paths) == 0 {
		return true
	}
	for _, current := range paths {
		for _, pattern := range record.Applicability.Paths {
			if matchPath(pattern, current) {
				return true
			}
		}
	}
	return false
}

func validPathPattern(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.Contains(value, ":") || strings.HasPrefix(value, "/") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	_, err := path.Match(value, "probe")
	return err == nil
}

func matchPath(pattern, value string) bool {
	if ok, _ := path.Match(pattern, value); ok {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return value == prefix || strings.HasPrefix(value, prefix+"/")
	}
	return false
}
