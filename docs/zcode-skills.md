# ZCode project skills

Threadforge can install an approved portable procedure as a project Agent Skill at:

```text
.agents/skills/<generated-skill-name>/SKILL.md
```

ZCode uses the shared `.agents/skills/` directory, so the same installed file may be visible to other compatible project-skill hosts. Threadforge records shared ownership as `agents-skills`; uninstalling the ZCode route removes only the exact, unchanged Threadforge-owned export and leaves neighboring skills in place.

Export and review a procedure first, then install the exported artifact using its printed digest:

```powershell
threadforge procedure export-skill --project-root <absolute-project-path> --id <procedure-id> --privacy-reviewed
npm.cmd --prefix adapters run adapter -- zcode skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd --prefix adapters run adapter -- zcode skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The installer accepts only a generated export beneath `.threadforge/skills/<id>/rev-NNNNNN/SKILL.md`, checks its SHA-256 and required advisory/privacy frontmatter, and refuses unowned conflicts or edited installed content. It does not change ZCode trust, settings, MCP, telemetry, or credentials. The ZCode target is documented in Mancode's preserved platform matrix as provisional; installed ZCode discovery, command behavior, and session propagation have not been verified. A successful file install is not evidence that ZCode loaded or followed the procedure.
