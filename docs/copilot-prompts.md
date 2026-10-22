# GitHub Copilot IDE prompt files

Threadforge can project one exact, digest-reviewed portable procedure export into a repository prompt file at `.github/prompts/<procedure-name>.prompt.md`:

```powershell
npm.cmd --prefix adapters run adapter -- copilot prompts install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd --prefix adapters run adapter -- copilot prompts uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The installer accepts only a generated Threadforge export under `.threadforge/skills/<procedure-id>/rev-NNNNNN/SKILL.md`, verifies the supplied SHA-256 and approval/privacy notices, and writes one Markdown prompt plus a private ownership marker. It adds an advisory preamble, reports only relative paths and digests, refuses collisions and symlinked paths, and removes only exact unchanged content. Existing `.github/copilot-instructions.md`, `.github/skills`, other prompts, trust, permissions, and IDE settings are not changed. The export digest verifies the copied artifact; it does not establish current source freshness or applicability.

These are **manually invoked prompts**, not always-on instructions or automatic memory retrieval. The generated content says not to infer authorization from the procedure. Select it only for a matching user request, and independently check the current project state and policies. For relevant approved skills that a host may select when useful, use `copilot skills install` instead; project MCP offers a separate explicit retrieval path.

GitHub documents repository prompt files as Markdown files ending in `.prompt.md` under `.github/prompts/`; availability and setup vary by IDE and version, and the feature is in preview. Threadforge does not enable IDE settings or claim that a generated file was discovered or used. GitHub's current customization matrix does not list prompt files as supported in Copilot CLI, so this installer targets IDE prompt-file workflows only. Verify the selected IDE's current documentation and settings before relying on discovery or invocation:

- [Prompt files for GitHub Copilot](https://docs.github.com/en/copilot/concepts/prompting-and-context/prompt-files)
- [GitHub Copilot customization cheat sheet and support matrix](https://docs.github.com/en/copilot/reference/customization-cheat-sheet)
