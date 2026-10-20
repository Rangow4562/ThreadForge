# Qoder CLI project skills

Threadforge can install an approved portable procedure into Qoder CLI's project-local Skills directory:

```text
.qoder/skills/<generated-skill-name>/SKILL.md
```

This remains separate from Qoder's project instructions. Threadforge has a distinct digest-gated installer for `.qoder/commands/`; see [Qoder CLI project commands](qoder-commands.md). The two installers preserve one another's files.

Export and review the procedure first, then pass the export's SHA-256 digest to the installer:

```powershell
threadforge procedure export-skill --project-root <absolute-project-path> --id <procedure-id> --privacy-reviewed
npm.cmd --prefix adapters run adapter -- qoder skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd --prefix adapters run adapter -- qoder skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

Only an immutable generated artifact under `.threadforge/skills/<id>/rev-NNNNNN/SKILL.md` is accepted. The shared installer checks the expected digest, generated identity, and advisory/privacy notices; it uses exclusive writes, a host-specific ownership marker and lock, rejects symlinks and unowned conflicts, preserves neighboring files, and refuses to remove user-edited content. No trust, permissions, MCP, telemetry, credentials, project commands, or `AGENTS.md` content is changed.

Qoder may select an installed Skill based on its own model-driven behavior. The procedure remains guidance, never execution authorization. Qoder CLI version compatibility, workspace discovery, skill selection, and task outcomes remain unverified. See Qoder's official [Skills documentation](https://docs.qoder.com/cli/Skills).
