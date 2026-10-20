# OpenCode project skills

OpenCode's skills documentation lists `.agents/skills/<name>/SKILL.md` as a project-level skill location ([OpenCode Skills](https://opencode.ai/docs/skills)). Threadforge can place an approved, privacy-reviewed procedure export there using the existing digest-checked project-skill lifecycle.

## Install and remove

Export and review the procedure locally first:

```powershell
threadforge procedure export-skill --project-root . --id <approved-procedure-id> --privacy-reviewed
```

Then pass the exact exported path and SHA-256:

```powershell
npm.cmd run adapter -- opencode skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd run adapter -- opencode skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The installer writes only `.agents/skills/<generated-skill-name>/SKILL.md` plus a Threadforge ownership marker. It rejects digest mismatches, unowned conflicts, symlinked paths, and edited installed content; it preserves neighboring skills and removes only the exact unchanged bytes it owns. No OpenCode settings, MCP configuration, telemetry, trust settings, or credentials are changed.

The file is advisory procedure guidance, never execution authorization. The no-model smoke (`npm run test:installed-skills` from `adapters/`) confirms the installed OpenCode CLI 1.18.31 lists the shared project skill. This verifies discovery only; model activation, procedure retrieval, and cross-harness task completion remain acceptance work. See [installed skill smoke](installed-skill-smoke.md).
