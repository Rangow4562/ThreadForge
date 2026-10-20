# Qoder CLI project commands

Threadforge installs an explicitly invoked, advisory procedure command at:

```text
.qoder/commands/<generated-procedure-name>.md
```

Export and review the procedure first. Installation accepts only the immutable Threadforge export and its exact SHA-256 digest:

```powershell
threadforge procedure export-skill --project-root <absolute-project-path> --id <procedure-id> --privacy-reviewed
npm.cmd --prefix adapters run adapter -- qoder commands install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd --prefix adapters run adapter -- qoder commands uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The installed basename is the generated procedure name, so Qoder CLI exposes it as `/threadforge-...`. Threadforge emits Qoder command frontmatter (`name` and `description`) and preserves the reviewed procedure body. It does not turn graph transitions into executable commands or add shell/tool instructions.

The installer writes only its generated `.md` and a hidden ownership record under `.qoder/commands/`. It uses exclusive writes and a per-command lock, refuses symlinks, unowned collisions, edited files, and a directory containing `SKILL.md` (which Qoder treats as a single Skill rather than a sibling command collection). Uninstall removes only the exact unchanged command it owns and preserves neighboring prompts. It does not alter `.qoder/skills/`, `AGENTS.md`, Qoder settings, trust, permissions, MCP, telemetry, credentials, or other project files.

The command body is approved guidance, not execution authorization. Qoder CLI discovery and invocation are not verified in this environment because the executable is unavailable; this installer test proves only the generated project-file contract. See Qoder's official [CLI Commands](https://docs.qoder.com/cli/commands) and [Custom Commands](https://docs.qoder.com/user-guide/custom-commands) documentation.

The versioned installer response is `threadforge.qoder-project-command.v1`, defined by `schema/qoder-project-command-v1.schema.json`; it reports the command path and separate source/installed-content SHA-256 digests, never the procedure body.
