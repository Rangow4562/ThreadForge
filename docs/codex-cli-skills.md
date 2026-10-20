# Codex CLI project skills

Codex documents repository skills under `.agents/skills` ([Codex skills](https://developers.openai.com/codex/skills)). Threadforge can install a locally approved, privacy-reviewed procedure export at that project path using its digest-checked, ownership-aware installer.

## Install and remove

Export and review the procedure locally first:

```powershell
threadforge procedure export-skill --project-root . --id <approved-procedure-id> --privacy-reviewed
```

Then pass the exact exported path and SHA-256:

```powershell
npm.cmd run adapter -- codex skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd run adapter -- codex skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The installer writes only `.agents/skills/<generated-skill-name>/SKILL.md` plus a Threadforge ownership marker. It rejects digest mismatches, unowned conflicts, symlinked paths, and edited installed content; it preserves neighboring skills and removes only the exact unchanged bytes it owns. It does not change Codex trust settings, MCP configuration, telemetry, or credentials.

The procedure is advisory guidance, not authorization to execute actions. The adapter's fixture tests verify file lifecycle only; installed Codex discovery/version behavior and cross-harness retrieval remain acceptance work.

Threadforge adapters that target the shared `.agents/skills` directory use a common ownership marker and lock, so the same exact approved export can be reused across Codex CLI, Gemini CLI, Antigravity, OpenCode, Cursor, and Devin. This file-level interoperability does not itself prove that each installed host loads the skill.
