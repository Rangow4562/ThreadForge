# Windsurf / Cascade approved project skills

Threadforge can install an already reviewed portable procedure as a workspace Agent Skill:

```powershell
npm.cmd --prefix adapters run adapter -- windsurf skills install --project-root <absolute-project-root> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd --prefix adapters run adapter -- windsurf skills uninstall --project-root <absolute-project-root> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The installer writes to `.agents/skills/<generated-name>/SKILL.md`, the shared Agent Skills location that the current [Windsurf/Cascade skills documentation](https://docs.windsurf.com/windsurf/cascade/skills) lists for cross-agent compatibility. It uses Threadforge's existing portable-export validator: the source must be an approved Threadforge export, its SHA-256 must match, and its generated identity and advisory/privacy notices must be intact. Writes are exclusive and ownership-marked; collisions, symbolic links, and modified installed content are refused. Uninstall removes only the unchanged Threadforge-owned skill and preserves neighboring files.

This is a project-file memory path, not a global MCP connection. It does not change host settings, trust, permissions, telemetry, credentials, or execution policy. The skill remains advisory. Windsurf's live discovery, model selection, and actual use of a Threadforge procedure have not been verified; installation alone does not establish context injection or task efficacy. The `devin-desktop skills` CLI spelling is also accepted for the Cascade/Windsurf integration.
