# Gemini CLI project skills

Gemini CLI documents workspace Agent Skills under `.agents/skills/<name>/SKILL.md` ([Gemini CLI Skills](https://geminicli.com/docs/cli/skills/)). Threadforge can install an approved, privacy-reviewed procedure export into that project scope.

## Install and remove

Export and review the procedure locally first:

```powershell
threadforge procedure export-skill --project-root . --id <approved-procedure-id> --privacy-reviewed
```

Then provide the exact exported path and SHA-256:

```powershell
npm.cmd run adapter -- gemini skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd run adapter -- gemini skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

Only `.agents/skills/<generated-skill-name>/SKILL.md` and its Threadforge ownership marker are written. The installer verifies the immutable export and digest, refuses unowned conflicts, edits, and symlinked paths, preserves neighboring skills, and removes only the exact unchanged content it owns. It does not write Gemini settings, change trust, configure telemetry, or handle credentials.

The procedure is advisory guidance, not execution authorization. Fixture tests verify file handling. The no-model smoke (`npm run test:installed-skills` from `adapters/`) also confirms the installed Gemini CLI 0.42.0 lists the shared project skill in an isolated temporary workspace. It uses a temporary trust override only for that disposable workspace and does not prove model activation, retrieval, or task completion; it does not change the user's trust settings. See [installed skill smoke](installed-skill-smoke.md).
