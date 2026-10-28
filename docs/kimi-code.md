# Kimi Code hooks and project skills

Threadforge provides a partial user-scope Kimi Code hook adapter. Kimi Code reads one user configuration file, `<KIMI_CODE_HOME>/config.toml`; when `KIMI_CODE_HOME` is unset, Threadforge uses `~/.kimi-code/config.toml`. The adapter does not claim project-scope hook support.

Install, update, and remove Threadforge's marked hook tables with:

```powershell
npm.cmd --prefix adapters run adapter -- kimi install --command 'node --experimental-strip-types "<absolute-path-to-kimi-code-hook.ts>" --threadforge-hook=kimi'
npm.cmd --prefix adapters run adapter -- kimi update --command '<complete-new-hook-command-with-marker>'
npm.cmd --prefix adapters run adapter -- kimi uninstall
```

For an existing alternate Kimi Code data root, add `--kimi-code-home <absolute-existing-directory>`. The installer appends only `[[hooks]]` entries using Kimi's `event`, `command`, and `timeout` fields; the ownership marker is in the command because comments may be removed by Kimi's config migration. It never reserializes other TOML values, so provider keys, model settings, and comments outside Threadforge's managed tables remain byte-for-byte unchanged. Updates and removal locate and remove only entries whose command contains `--threadforge-hook=kimi`. A config created solely for the hooks is left as an empty `config.toml` after uninstall.

The 17 registered events are `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `PermissionResult`, `Stop`, `SessionEnd`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact`, `StopFailure`, `Interrupt`, `TaskStarted`, and `Notification`. Timeouts are seconds: 10 seconds for most events, 30 for prompt submission, and 45 for stop/session end. The adapter omits `SessionHeartbeat` because subscribing creates a periodic process; `UserPromptQueued` and `TurnStarted` are omitted to avoid duplicating the submitted prompt.

Prompt text, tool arguments/results, paths, diffs, task descriptions, and approval feedback are not retained. Bounded SHA-256 digests/byte counts are emitted instead; session, tool-call, agent, and task identifiers are hashed. `PermissionRequest` is recorded as a request; only `PermissionResult` records an explicit outcome. Content is sent only to the loopback `/v1/events` endpoint, and the hook writes no stdout so prompt text is never appended to model context. For Kimi's blockable hook phases, Threadforge exits successfully and never emits a deny response; Kimi remains responsible for its ordinary permission checks. Threadforge does not install policy enforcement or inject memory as authorization.

The TOML updater validates the structure it edits and fails closed on inline `hooks` values, unsupported hook fields, malformed strings, and unbalanced arrays/inline tables. It caps config at 8 MiB, rejects symlink paths, locks and atomically replaces changes, and preserves config file modes. Adapter tests cover mapper privacy, event/tool/approval behavior, loopback delivery, byte preservation, install/update/uninstall, conflicts, invalid TOML, symlinks, locks, and size limits.

## Project-scoped MCP

Kimi Code's project-level MCP config is `.kimi-code/mcp.json`, using the standard `mcpServers` object. Threadforge manages only `mcpServers.threadforge` and binds it to the selected project root:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install --host kimi-code --project-root <absolute-project-path> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <absolute-project-path>
npm.cmd --prefix adapters run adapter -- mcp update --host kimi-code --project-root <absolute-project-path> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <absolute-project-path>
npm.cmd --prefix adapters run adapter -- mcp uninstall --host kimi-code --project-root <absolute-project-path>
```

The manager writes only `command` and `args`, includes the Threadforge ownership marker, preserves sibling entries, and rejects credential-like values. It does not set workspace trust or Kimi permission options; Kimi Code may require the user to trust the project before loading its project MCP config. The Kimi CLI is unavailable in this environment, so config discovery and MCP tool retrieval are not live-verified. See Kimi Code's official [MCP documentation](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/mcp.html).

## Approved project procedure skills

Kimi Code documents project-level skills in `.kimi-code/skills/<name>/SKILL.md` (and also recognizes the shared `.agents/skills/` directory). Threadforge provides an explicit project-scoped installer for the native path:

```powershell
npm.cmd --prefix adapters run adapter -- kimi skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <reviewed-export-digest>
npm.cmd --prefix adapters run adapter -- kimi skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <reviewed-export-digest>
```

The source must be an immutable Threadforge export under `.threadforge/skills/`; the installer verifies its exact SHA-256 and Threadforge approval/privacy notices before writing. It uses exclusive writes, a per-skill lock and ownership file, refuses symlink paths, collisions and edits, and removes only unchanged Threadforge content. It does not modify `config.toml`, hook registrations, credentials, or trust settings. The export remains advisory guidance, not execution authorization. The `kimi` executable is unavailable in this environment, so installed Kimi Code skill discovery and task use remain unverified.

The current host was not available in this environment (`kimi` is not on PATH), so live event payloads, runtime version compatibility, skill discovery, and the host's exit-code behavior remain unverified. Current contracts are documented in Kimi Code's official [hooks reference](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html) and [Agent Skills guide](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/skills.html); Threadforge's host row remains partial until live acceptance succeeds.
