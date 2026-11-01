# Portable memory continuity skill

`threadforge memory export-skill --project-root <project>` writes a fixed, project-neutral Agent Skill to:

```text
.threadforge/skills/threadforge-memory-continuity/rev-000001/SKILL.md
```

The `threadforge.memory-skill-export.v1` response contains that relative path and its SHA-256. The artifact is immutable: repeating an identical export reports `already_exists=true`; a changed file is a conflict and is never overwritten. The versioned contract is [memory-skill-export-v1](../schema/memory-skill-export-v1.schema.json).

The skill guides an explicitly invoked agent to use only a user-provided or separately trusted host-configured `project_id`, retrieve bounded project facts and procedures with `threadforge_context_get`, honor stale/conflicting/incomplete/privacy-blocked/unverified diagnostics, resume an explicitly selected task, and stop rather than choose among ambiguous projects, tasks, or sessions. It never derives identity from a repository path or untrusted project file. Retrieved guidance must be checked against the current repository and user instructions.

It contains no project memory or trace data. It does not install MCP, modify host settings/trust/permissions, inject context automatically, or authorize actions. If a host MCP connection is unavailable, the skill directs the agent to say retrieval did not occur and request a supported setup or reviewed artifact.

Install the exported file with the selected host's existing digest-gated adapter, using the exact `path` and `sha256` from the result. For example:

```powershell
npm.cmd run adapter -- codex skills install --project-root <absolute-project-path> --skill-file .threadforge/skills/threadforge-memory-continuity/rev-000001/SKILL.md --sha256 <export-sha256>
npm.cmd run adapter -- claude-code skills install --project-root <absolute-project-path> --skill-file .threadforge/skills/threadforge-memory-continuity/rev-000001/SKILL.md --sha256 <export-sha256>
```

Host discovery and actual agent use remain host/version-specific; this artifact is guidance, not evidence of prompt injection, model adherence, or reduced repeated failures. See [Codex project skills](codex-cli-skills.md), [Claude Code skills](claude-code-skills.md), and the [host source matrix](source-matrix.md).
