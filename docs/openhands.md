# OpenHands hook adapter

Threadforge has a partial, project-scoped OpenHands hook adapter. It merges six asynchronous observational commands into `<project>/.openhands/hooks.json` and leaves existing hooks in place. Install/update/uninstall require an absolute `--project-root`:

```powershell
cd adapters
npm.cmd run adapter -- openhands install --project-root <absolute-path>
npm.cmd run adapter -- openhands update --project-root <absolute-path>
npm.cmd run adapter -- openhands uninstall --project-root <absolute-path>
```

Install defaults to a command that calls the local Threadforge adapter through Node 22's TypeScript stripping support. To use a command available in every project environment, pass `--command-template` with exactly one `{event}` placeholder and the ownership marker `--threadforge-openhands-hook=v1`:

```powershell
npm.cmd run adapter -- openhands install --project-root <absolute-path> --command-template 'threadforge-adapter openhands hook {event} --threadforge-openhands-hook=v1'
```

The adapter registers `session_start`, `user_prompt_submit`, `pre_tool_use`, `post_tool_use`, `stop`, and `session_end` with `async: true`, a `*` matcher, and a 10-second timeout. Its command reads at most 1 MiB of JSON from stdin, sends bounded metadata/digests only to the loopback event intake, prints no stdout decision, and fails open if parsing or delivery fails. No hook returns permission, approval, or context-injection output.

The mapper covers session starts/ends, prompts, pre-tool invocation, post-tool results, terminal command metadata, file-editor operations and `apply_patch` per-file changes, MCP results recognized by the explicit `MCPToolObservation` discriminator, generic tool failures, and stop boundaries. A nonzero terminal `exit_code` remains a command outcome unless OpenHands explicitly sets `is_error`; no operator approval is inferred. Tool-call IDs and MCP server identity are not present in the observed payload and are not synthesized.

Prompt text, tool arguments, command strings, outputs, file paths, and old/new file contents are digest-only. Per-file results keep hashed paths and before/after digests and byte counts; image payloads are ignored. Content bodies and credentials are never copied into event fields.

The manager accepts strict JSON with either a direct event map or the legacy `{"hooks": {...}}` wrapper, preserves existing snake_case/PascalCase event spelling, and refuses duplicate event spellings, unknown event keys, invalid groups, symlink paths, and oversized files. It uses an ownership marker in each command, a lock, and atomic replacement. Uninstall removes only marked references and deletes an empty hooks file.

## Approved project procedure skills

OpenHands documents project-level Agent Skills under `.agents/skills/<name>/SKILL.md`. Install a previously reviewed Threadforge export with its exact digest:

```powershell
npm.cmd run adapter -- openhands skills install --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd run adapter -- openhands skills uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

This route uses the shared `.agents/skills/` ownership contract. It accepts only an approved, privacy-reviewed Threadforge export with a matching SHA-256, refuses collisions or modified content, and preserves neighboring skills. It does not modify `.openhands/mcp.json`, hooks, trust, or credentials. The skill remains advisory guidance, not execution authorization. OpenHands may load it from the project path, but live discovery and actual task retrieval have not been verified here. See OpenHands' [Agent Skills documentation](https://docs.openhands.dev/openhands/usage/skills).

## User-global MCP server

OpenHands CLI reads MCP servers from the user-level `~/.openhands/mcp.json`; its documented CLI does not provide project-specific MCP configuration. Threadforge therefore exposes a separate, explicit user-global registration. It starts the local MCP server without `--project-root`, so it reaches the local Threadforge store rather than version-controlled project memory.

```powershell
npm.cmd --prefix adapters run adapter -- openhands mcp install --command <absolute-threadforge-executable>
npm.cmd --prefix adapters run adapter -- openhands mcp status
npm.cmd --prefix adapters run adapter -- openhands mcp update --expected-command <current-executable> --command <new-absolute-threadforge-executable>
npm.cmd --prefix adapters run adapter -- openhands mcp uninstall --expected-command <current-executable>
```

Use `--config-path <absolute-path>` to target an alternate local MCP config. Threadforge changes only `mcpServers.threadforge`, writes only `command` and bounded `args`, and preserves other entries. It does not set automatic approvals, trust, or environment credentials. This is a global local-store connection—not project-scoped memory—and OpenHands CLI discovery/tool dispatch have not been tested because the executable is unavailable here. See OpenHands' [MCP server documentation](https://docs.openhands.dev/openhands/usage/cli/mcp-servers).

OpenHands' current [hooks reference](https://docs.openhands.dev/openhands/usage/customization/hooks) describes asynchronous hooks and project hook configuration. The event payloads and generated command still require installed-version and platform verification. User-scope SDK capture, runtime discovery, packaged installation, and cross-harness retrieval are not implemented. This is not release parity or cutover evidence.
