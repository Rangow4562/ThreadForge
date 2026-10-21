# Devin Desktop (Cascade/Windsurf hooks)

Threadforge provides a partial, observation-only Devin Desktop adapter through the Cascade/Windsurf hook files. The host's current payload and response contract has not been verified against an installed Devin Desktop release.

Install user-level hooks (the default scope):

```powershell
cd adapters
npm.cmd run adapter -- devin-desktop install
```

For project-level hooks, pass both `--scope project` and an absolute `--project-root`. Use `update` to replace only Threadforge-owned commands, and `uninstall` to remove those commands while keeping unrelated settings and hooks. Threadforge edits `<home>/.codeium/windsurf/hooks.json` for user scope or `<project>/.windsurf/hooks.json` for project scope. Restart Devin Desktop after installing, updating, or removing hooks.

The five configured hook events are `pre_user_prompt`, `post_write_code`, `post_run_command`, `post_mcp_tool_use`, and `post_read_code`. Payload input is limited to 1 MiB. Session and execution identifiers and workspace/file paths are hashed; prompt, command, file, edit, and MCP-input content is represented by a digest and byte count (with digest computation capped at 64 KiB). Events are sent only to the local loopback `/v1/events` endpoint, with a 250 ms timeout. Hook failures do not block Devin Desktop, and the hook emits no stdout response or authorization decision.

The default installed command points at the local TypeScript CLI and requires Node.js 22.6 or later with type stripping; it is a development launcher, not a portable release installer. `--command-template` can supply a deployment-specific command and must contain exactly one `{event}` placeholder and the ownership marker `--threadforge-devin-desktop-hook=v1`.

This integration does not configure cloud agents, credentials, host trust, or permission behavior. Installed-host validation, package distribution, full migration parity, and cross-harness retrieval acceptance remain open; see the [source matrix](source-matrix.md) and [cutover criteria](cutover.md).

## Approved project procedures

Windsurf/Cascade can also receive reviewed Threadforge procedures as workspace Agent Skills under the shared `.agents/skills/` path. Install and uninstall are digest-gated and preserve neighboring skills; they do not modify Windsurf configuration or trust. Live skill discovery and invocation remain unverified. See [Windsurf/Cascade approved project skills](windsurf-skills.md).
