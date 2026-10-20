# Command Code integration

Threadforge has a partial, project-scoped Command Code integration for approved procedure skills, MCP configuration, and advisory project instructions. It does not yet provide Command Code telemetry capture, hooks, session-history import, or end-to-end task-efficacy evidence.

## Approved procedure skills

Export a privacy-reviewed and approved procedure into the same project that will consume it, then install the exported file using its SHA-256 digest:

```powershell
threadforge procedure export-skill --project-root <project-root> --id <procedure-id> --privacy-reviewed
npm.cmd --prefix adapters run adapter -- command-code skills install `
  --project-root <absolute-project-root> `
  --skill-file <project-root>/.threadforge/skills/<exported-skill>/SKILL.md `
  --sha256 <export-digest>
```

The host-specific path is `.commandcode/skills/<skill-name>/SKILL.md`. The installer validates the exported skill and digest, uses Threadforge ownership metadata, preserves neighboring files, and refuses to overwrite unowned or modified content. Remove only the matching Threadforge-managed file with `command-code skills uninstall` and the same project/file/digest arguments. Skills are advisory guidance, not permission grants or execution authorization.

## Project MCP

The `command-code` host ID manages the `threadforge` entry in the project's standard `.mcp.json` file. It shares that file format/manager with Claude Code; use separate review of the merged config when both clients use the project. The manager preserves unrelated server entries and does not change project trust or host permissions.

```powershell
npm.cmd --prefix adapters run adapter -- mcp install `
  --host command-code `
  --project-root <absolute-project-root> `
  --command <threadforge-executable> `
  --arg mcp --arg serve --arg --project-root --arg <same-project-root>

npm.cmd --prefix adapters run adapter -- mcp uninstall `
  --host command-code `
  --project-root <absolute-project-root>
```

For the reversible advisory instruction block, `bootstrap instructions plan|install|update|uninstall --hosts command-code --project-root <absolute-project-root>` manages a marked block in `AGENTS.md`. It does not install Command Code-native commands, skills, MCP, hooks, permissions, or trust.

## Verification and limits

On October 12, 2026, the installed Command Code CLI `1.79.2` was tested in a disposable project with an isolated home, `CMD_LOCAL_ONLY=1`, and `--no-auto-update`. On Windows, the v15 smoke resolved `cmdc.ps1` and invoked the installed `node_modules/command-code/dist/index.mjs` entrypoint directly with Node to avoid a timed-out PowerShell shim leaving a child process attached to the test directory. The CLI's `mcp list` displayed the project-scoped `threadforge` entry, and `skills list` discovered the approved exported procedure under `.commandcode/skills/`. The smoke reports `real_model_invocations: 0`; the Command Code project was isolated from Claude's `.mcp.json` smoke project.

On October 15, 2026, smoke v17 additionally wrote a keyless custom provider under the disposable `~/.commandcode/providers.json`, pointed it at a loopback OpenAI-compatible fixture, and confirmed `cmdc --local-only --list-models` listed the fixture model. The subsequent isolated headless run returned `Not authenticated` before making a provider request, so the smoke records `agent_run.status=skipped` and `provider_requests=0`. It does not borrow the user's account, use the hosted Command Code provider, or claim MCP tool dispatch.

This verifies local config discovery/listing and skill discovery for that CLI version. Agent-run MCP dispatch remains unverified because the isolated CLI requires login before beginning a headless run; the smoke intentionally does not copy user credentials. Model-driven procedure selection, instruction adherence, task success, telemetry, hooks, project trust behavior outside the disposable test, and compatibility with other releases also remain unverified. See the [installed-host smoke details](installed-skill-smoke.md), [project MCP bootstrap](platform-bootstrap.md), and [feature-parity ledger](feature-parity.md).
