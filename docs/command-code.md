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

The same installer accepts the fixed portable memory continuity skill. It contains no project data and guides explicit freshness-aware MCP retrieval; configure project MCP separately:

```powershell
threadforge memory export-skill --project-root <project-root>
npm.cmd --prefix adapters run adapter -- command-code skills install `
  --project-root <absolute-project-root> `
  --skill-file <project-root>/.threadforge/skills/threadforge-memory-continuity/rev-000002/SKILL.md `
  --sha256 <export-sha256>
```

Smoke v43 on November 1, 2026 verified the revision-2 export→digest-gated install→`cmdc skills list` path with Command Code `1.88.2`, alongside an approved procedure. The isolated agent run remains skipped because Command Code requires login; no agent use or real-model behavior is claimed.

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

## Optional local telemetry observer

The adapter package includes a project-scoped Command Code mod at `command-code-mod/`. It subscribes to the documented `AgentEvent` stream with `cmd.on(...)`; it does not register behavior-changing `cmd.hooks(...)`. Install only after reviewing the mod source because host mods execute code inside Command Code:

```powershell
cmdc mods add <path-to-threadforge-adapter-package>/command-code-mod
cmdc mods list
cmdc mods inspect <path-to-threadforge-adapter-package>/command-code-mod

# Remove the project-scoped telemetry mod when no longer wanted.
cmdc mods remove <path-to-threadforge-adapter-package>/command-code-mod
```

The observer allowlists session/run/turn/model/tool/subagent/compaction lifecycle events. It ignores each event payload and sends only normalized event type/action, category, observed timestamp, OS, and a process-generated session correlation ID to the loopback-only `http://127.0.0.1:4318/v1/events` endpoint. Prompt text, tool inputs/results, paths, model and tool names, host IDs, errors, permission/config values, and arbitrary event fields are not read or forwarded. The queue is capped at 64 in-flight requests, requests time out after 1.5 seconds by default, delivery failures are ignored, and removal disposes event subscriptions. This is opt-in local telemetry, not a policy hook or security boundary.

`npm run test:command-code-mod-host` uses an isolated home and disposable Git project. On Command Code CLI `1.89.1`, the host accepted the local mod source, recorded it in project `.commandcode/settings.json`, and `cmdc mods inspect` reported `registers: event subscription` and `hooks: none`; removing the same local source cleared the mod setting. A one-shot `cmdc mods list` outside an agent session reported zero loaded mods, so active TUI discovery and callback execution remain unverified. The smoke does not log in or start an agent run.

This integration follows Command Code's [Mods API](https://commandcode.ai/docs/mods), which distinguishes observer subscriptions from behavior-changing hooks. Command Code currently labels the ModApi experimental and warns that surfaces/signatures may change; treat CLI mod inspection and installed-version smoke as required when upgrading Command Code.

## Verification and limits

Installed-host refresh (November 15, 2026): the Windows installed-skill smoke v48 ran against Command Code CLI `1.89.1`. It verified project `.mcp.json` discovery with `cmdc mcp list` and discovery of the approved procedure plus portable memory-continuity skills. The isolated agent MCP probe returned `skipped` with reason `isolated_command_code_cli_requires_login_for_agent_run`; `provider_requests=0` and `real_model_invocations=0`. A separate help probe under a temporary home showed `cmdc mcp` exposes server management only, not a direct tool-call command. Thus host-mediated `threadforge_procedure_get` dispatch remains unverified; no user credentials or hosted model were used.

On October 12, 2026, the installed Command Code CLI `1.79.2` was tested in a disposable project with an isolated home, `CMD_LOCAL_ONLY=1`, and `--no-auto-update`. On Windows, the v15 smoke resolved `cmdc.ps1` and invoked the installed `node_modules/command-code/dist/index.mjs` entrypoint directly with Node to avoid a timed-out PowerShell shim leaving a child process attached to the test directory. The CLI's `mcp list` displayed the project-scoped `threadforge` entry, and `skills list` discovered the approved exported procedure under `.commandcode/skills/`. The smoke reports `real_model_invocations: 0`; the Command Code project was isolated from Claude's `.mcp.json` smoke project.

On October 15, 2026, smoke v17 additionally wrote a keyless custom provider under the disposable `~/.commandcode/providers.json`, pointed it at a loopback OpenAI-compatible fixture, and confirmed `cmdc --local-only --list-models` listed the fixture model. The subsequent isolated headless run returned `Not authenticated` before making a provider request, so the smoke records `agent_run.status=skipped` and `provider_requests=0`. It does not borrow the user's account, use the hosted Command Code provider, or claim MCP tool dispatch.

This verifies local config discovery/listing and skill discovery for that CLI version. Agent-run MCP dispatch remains unverified because the isolated CLI requires login before beginning a headless run; the smoke intentionally does not copy user credentials. Model-driven procedure selection, instruction adherence, task success, telemetry, hooks, project trust behavior outside the disposable test, and compatibility with other releases also remain unverified. See the [installed-host smoke details](installed-skill-smoke.md), [project MCP bootstrap](platform-bootstrap.md), and [feature-parity ledger](feature-parity.md).
