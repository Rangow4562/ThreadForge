package procedurefiles

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportMemorySkillIsImmutableAndContainsBoundedRetrievalGuidance(t *testing.T) {
	root := t.TempDir()
	project, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}

	first, err := project.ExportMemorySkill()
	if err != nil {
		t.Fatal(err)
	}
	if first.SchemaVersion != "threadforge.memory-skill-export.v2" || first.SkillID != memoryContinuitySkillID ||
		first.SkillRevision != memoryContinuitySkillRevision || first.AlreadyExists ||
		first.Path != ".threadforge/skills/threadforge-memory-continuity/rev-000002/SKILL.md" || len(first.SHA256) != 64 {
		t.Fatalf("unexpected portable memory-skill export: %#v", first)
	}
	filename := filepath.Join(root, filepath.FromSlash(first.Path))
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if first.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("memory-skill digest does not match bytes: %#v", first)
	}
	for _, required := range []string{
		"threadforge_context_get", "threadforge_project_memory_read_v2", "threadforge_task_resume_context",
		"threadforge_task_discover", "project_id", "unverified", "privacy_blocked",
		"separately trusted host configuration", "Source memory continuity skill: threadforge-memory-continuity revision 2",
		"portable memory skill provides advisory guidance", "not authorization to execute actions", "Never send raw traces",
	} {
		if !strings.Contains(string(content), required) {
			t.Errorf("portable memory skill is missing %q", required)
		}
	}
	if strings.Contains(string(content), "> Source procedure:") || strings.Contains(string(content), "portable procedure is approved guidance") {
		t.Fatalf("memory continuity artifact is mislabeled as a procedure: %s", content)
	}
	if strings.Contains(string(content), root) || strings.Contains(string(content), "example.invalid") {
		t.Fatalf("portable memory skill contains project-specific data: %s", content)
	}

	second, err := project.ExportMemorySkill()
	if err != nil {
		t.Fatal(err)
	}
	if !second.AlreadyExists || second.SHA256 != first.SHA256 {
		t.Fatalf("identical export was not idempotent: first=%#v second=%#v", first, second)
	}
	if err := os.WriteFile(filename, []byte("user edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := project.ExportMemorySkill(); !errors.Is(err, ErrConflict) {
		t.Fatalf("modified artifact was overwritten instead of conflicting: %v", err)
	}
}
