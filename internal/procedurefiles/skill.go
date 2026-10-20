package procedurefiles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
)

const maxSkillBytes = 256 << 10

// SkillExport describes an immutable, portable projection of an approved graph.
type SkillExport struct {
	SchemaVersion string `json:"schema_version"`
	GraphID       string `json:"graph_id"`
	GraphRevision int    `json:"graph_revision"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	AlreadyExists bool   `json:"already_exists"`
}

// ExportSkill writes an immutable SKILL.md projection under the project root.
// It is an explicit file export only: it does not install or inject a host skill.
func (s *Store) ExportSkill(graph proceduregraph.Graph, privacyReviewed bool) (SkillExport, error) {
	if !privacyReviewed {
		return SkillExport{}, errors.New("skill export requires explicit privacy review")
	}
	shared, err := proceduregraph.SanitizeForShare(graph)
	if err != nil {
		return SkillExport{}, err
	}
	for _, evidence := range shared.Evidence {
		if evidence.LocalTraceRef != "" || !evidence.Redacted {
			return SkillExport{}, errors.New("portable skills may contain only redacted evidence digests")
		}
	}

	data, err := renderSkill(shared)
	if err != nil {
		return SkillExport{}, err
	}
	if len(data) > maxSkillBytes {
		return SkillExport{}, errors.New("portable skill exceeds 256 KiB")
	}
	if privacy.ContainsCredentialText(string(data)) {
		return SkillExport{}, errors.New("portable skill contains credential material")
	}

	relative := filepath.Join(".threadforge", "skills", shared.ID, fmt.Sprintf("rev-%06d", shared.Revision), "SKILL.md")
	target := filepath.Join(s.projectRoot, relative)
	if err := s.ensureDirectory(filepath.Dir(target)); err != nil {
		return SkillExport{}, err
	}
	digest := sha256.Sum256(data)
	result := SkillExport{
		SchemaVersion: "threadforge.skill-export.v1",
		GraphID:       shared.ID,
		GraphRevision: shared.Revision,
		Path:          filepath.ToSlash(relative),
		SHA256:        hex.EncodeToString(digest[:]),
	}
	if err := writeNoReplace(target, data); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return SkillExport{}, fmt.Errorf("write immutable portable skill: %w", err)
		}
		if err := s.checkNoSymlinkComponents(target); err != nil {
			return SkillExport{}, err
		}
		info, statErr := os.Lstat(target)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxSkillBytes {
			return SkillExport{}, errors.New("existing portable skill path is not a safe regular file")
		}
		existing, readErr := os.ReadFile(target)
		if readErr != nil {
			return SkillExport{}, readErr
		}
		if string(existing) != string(data) {
			return SkillExport{}, ErrConflict
		}
		result.AlreadyExists = true
	}
	return result, nil
}

func renderSkill(graph proceduregraph.Graph) ([]byte, error) {
	quoted := func(value string) (string, error) {
		data, err := json.Marshal(value)
		return string(data), err
	}
	title, err := quoted(graph.Title)
	if err != nil {
		return nil, err
	}
	revision := fmt.Sprintf("%d", graph.Revision)
	idDigest := sha256.Sum256([]byte(graph.ID))
	nameSuffix := "-" + hex.EncodeToString(idDigest[:4]) + "-r" + revision
	namePrefix := "threadforge-"
	idBudget := 64 - len(namePrefix) - len(nameSuffix)
	shortID := graph.ID
	if len(shortID) > idBudget {
		shortID = strings.TrimRight(shortID[:idBudget], "-")
	}
	name, err := quoted(namePrefix + shortID + nameSuffix)
	if err != nil {
		return nil, err
	}
	description, err := quoted("Approved Threadforge procedure: " + graph.Title + ". Advisory only; not execution authorization.")
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	out.WriteString("---\nname: ")
	out.WriteString(name)
	out.WriteString("\ndescription: ")
	out.WriteString(description)
	out.WriteString("\n---\n\n# Threadforge procedure\n\n> Title: ")
	out.WriteString(title)
	out.WriteString("\n> Source procedure: ")
	out.WriteString(graph.ID)
	out.WriteString(" revision ")
	out.WriteString(revision)
	out.WriteString("\n\n> This portable procedure is approved guidance, not authorization to execute actions. Verify current project state and applicable policy before use.\n")

	if len(graph.Applicability.Modules) > 0 || len(graph.Applicability.Paths) > 0 {
		out.WriteString("\n## Applicability\n")
		for _, module := range graph.Applicability.Modules {
			out.WriteString("\n- Module: ")
			value, err := quoted(module)
			if err != nil {
				return nil, err
			}
			out.WriteString(value)
		}
		for _, path := range graph.Applicability.Paths {
			out.WriteString("\n- Path: ")
			value, err := quoted(path)
			if err != nil {
				return nil, err
			}
			out.WriteString(value)
		}
		out.WriteByte('\n')
	}

	out.WriteString("\n## Procedure\n")
	for _, node := range graph.Nodes {
		out.WriteString("\n### ")
		out.WriteString(string(node.Kind))
		out.WriteString(": ")
		out.WriteString(node.ID)
		out.WriteString("\n\n")
		writeQuotedLines(&out, node.Instruction)
		if node.Module != "" {
			out.WriteString("\n> Module: ")
			value, err := quoted(node.Module)
			if err != nil {
				return nil, err
			}
			out.WriteString(value)
		}
		for _, path := range node.Paths {
			out.WriteString("\n> Path: ")
			value, err := quoted(path)
			if err != nil {
				return nil, err
			}
			out.WriteString(value)
		}
		out.WriteByte('\n')
	}
	if len(graph.Edges) > 0 {
		out.WriteString("\n## Transitions\n")
		for _, edge := range graph.Edges {
			out.WriteString("\n> ")
			out.WriteString(edge.From)
			out.WriteString(" --")
			out.WriteString(string(edge.Relation))
			out.WriteString("--> ")
			out.WriteString(edge.To)
			if edge.Condition != "" {
				out.WriteString(" when ")
				condition, err := quoted(edge.Condition)
				if err != nil {
					return nil, err
				}
				out.WriteString(condition)
			}
			out.WriteByte('\n')
		}
	}
	out.WriteString("\nEvidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.\n")
	return []byte(out.String()), nil
}

func writeQuotedLines(out *strings.Builder, value string) {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for index, line := range lines {
		if index > 0 {
			out.WriteByte('\n')
		}
		out.WriteString("> ")
		out.WriteString(line)
	}
}
