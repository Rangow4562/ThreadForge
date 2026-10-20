# Antigravity project skills

Google's [Antigravity Agent Skills documentation](https://antigravity.google/docs/skills) says Antigravity loads available skill names/descriptions when a conversation starts and defaults to `.agents/skills` (while retaining `.agent/skills` compatibility). The [Skills codelab](https://codelabs.developers.google.com/getting-started-with-antigravity-skills) also documents project Agent Skills in `.agents/skills/<name>/SKILL.md`. Threadforge can install an approved, privacy-reviewed procedure export into that project scope.

## Install and remove

Export and review the procedure locally first:

```powershell
threadforge procedure export-skill --project-root . --id <approved-procedure-id> --privacy-reviewed
```

Then provide the exact exported path and SHA-256:

```powershell
npm.cmd run adapter -- antigravity skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd run adapter -- antigravity skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

Only `.agents/skills/<generated-skill-name>/SKILL.md` and its Threadforge ownership marker are written. The installer verifies the immutable export and digest, refuses unowned conflicts, edits, and symlinked paths, preserves neighboring skills, and removes only exact unchanged content. It does not alter Antigravity hook files, trust settings, telemetry, or credentials.

The procedure is advisory guidance, never execution authorization. On October 8, 2026, the locally installed `agy` CLI reported version `1.3.1`; its help exposes no no-model skill-list/discovery command. Running `agy --print` would start an agent prompt and may invoke a configured model, so the installed-skill smoke intentionally does not use it. Fixture tests verify file handling and the official project path, not live Antigravity loading, retrieval, or cross-harness task success; those remain acceptance work.
