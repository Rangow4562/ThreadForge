# GitHub Copilot project skills

Threadforge can install a locally approved, privacy-reviewed procedure as a project Agent Skill for GitHub Copilot. GitHub documents project skills in `.github/skills/<skill-name>/SKILL.md`; the same open Agent Skills format is also used by other supported hosts.

```powershell
npm.cmd --prefix adapters run adapter -- copilot skills install --project-root D:\path\to\project --skill-file "D:\path\to\project\.threadforge\skills\approved-procedure\rev-000001\SKILL.md" --sha256 "<export-digest>"
npm.cmd --prefix adapters run adapter -- copilot skills uninstall --project-root D:\path\to\project --skill-file "D:\path\to\project\.threadforge\skills\approved-procedure\rev-000001\SKILL.md" --sha256 "<export-digest>"
```

Installation accepts only an immutable Threadforge skill export and its exact lowercase SHA-256 digest. The shared installer validates the generated identity, revision, and advisory/privacy notices; writes exclusively under `.github/skills/`; records ownership; refuses symlinks, unowned conflicts, and edited content; and removes only an unchanged file it owns. Existing `.github/copilot-instructions.md` and neighboring skills are not modified. A later approved procedure revision is installed as a separate immutable skill rather than overwriting the previous export.

This is an explicit file installation, not proof that a particular Copilot client loaded or applied the skill. The installer does not configure telemetry, credentials, MCP, trust, or execution permissions. Retrieved procedures remain advisory. Host discovery, live task use, and cross-harness outcome evidence remain unverified.

See [GitHub's Copilot CLI Agent Skills documentation](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-skills) for the host's supported locations and loading behavior.
