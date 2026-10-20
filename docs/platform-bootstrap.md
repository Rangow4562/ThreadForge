# Project MCP bootstrap

The adapter CLI can configure Threadforge's project-scoped MCP server for an explicitly selected set of hosts. It composes the existing preservation-aware installers for Claude Code, Command Code, Cursor, Antigravity, Gemini CLI, Qwen Code, Codex in ChatGPT desktop, Codex CLI, the Codex IDE extension, and OpenCode. Claude Code and Command Code share the standard project `.mcp.json` manager; the three Codex host IDs share one `.codex/config.toml`. Selecting aliases for a shared file is idempotent, with the first operation changing the file and later operations reporting `unchanged` or `absent`.

Run the read-only plan first:

```powershell
npm.cmd --prefix adapters run adapter -- bootstrap mcp plan `
  --project-root <absolute-project-root> `
  --hosts claude-code,command-code,cursor,codex-cli,gemini-cli,qwen-code `
  --command <threadforge-executable> `
  --arg mcp --arg serve --arg --project-root --arg <same-project-root>
```

The host list is required, comma-separated, and must use exact supported IDs. `plan` checks the project root and command arguments but does not inspect or modify host configuration files; it is a supported-target preview, not a conflict/diff preflight. The plan contract is [`threadforge.project-bootstrap-plan.v1`](../schema/project-bootstrap-plan-v1.schema.json).

After reviewing the plan, install or update the selected hosts:

```powershell
npm.cmd --prefix adapters run adapter -- bootstrap mcp install `
  --project-root <absolute-project-root> `
  --hosts claude-code,cursor,codex-cli,gemini-cli,qwen-code `
  --command <threadforge-executable> `
  --arg mcp --arg serve --arg --project-root --arg <same-project-root>

npm.cmd --prefix adapters run adapter -- bootstrap mcp update `
  --project-root <absolute-project-root> `
  --hosts claude-code,cursor,codex-cli,gemini-cli,qwen-code `
  --command <threadforge-executable> `
  --arg mcp --arg serve --arg --project-root --arg <same-project-root>
```

Each host update is independently atomic, but the multi-host operation is not a transaction. The command continues across selected hosts, returns `status: partial` and exit code 1 if any host refuses its change, and leaves successful host changes in place for review/retry. It never overwrites an unowned collision. `install` is idempotent for an identical Threadforge-managed entry; `update` changes only an entry carrying the Threadforge marker. Inspect the returned `threadforge.project-bootstrap-result.v1` receipt before retrying.

Uninstall only the selected Threadforge-managed entries:

```powershell
npm.cmd --prefix adapters run adapter -- bootstrap mcp uninstall `
  --project-root <absolute-project-root> `
  --hosts claude-code,cursor,codex-cli,gemini-cli,qwen-code
```

MCP bootstrap does not create instruction blocks, install telemetry/hooks, change host trust or permissions, or activate any procedure. Gemini's project MCP entry keeps `trust: false`. Qwen's entry is limited to `command` and `args` in `.qwen/settings.json`; its manager shares the Qwen settings lock with hook/memory updates and leaves Qwen trust unchanged. Generated project configs contain the executable command; review machine-specific paths before committing them. For the separate advisory project-instruction block, see [instruction bootstrap](instruction-bootstrap.md).

Other harnesses can use `threadforge mcp print-config --project-root <root> --command <executable>` for a manually merged stdio MCP entry, or the portable approved-skill export/install path where MCP configuration is unsupported. This bootstrap accepts eleven host IDs across seven underlying config adapters; it is not full platform-bootstrap parity or live-host acceptance. Command Code CLI 1.79.2 listed the isolated project Threadforge entry in `cmdc mcp list` on October 12, 2026. GitHub Copilot CLI 1.0.64 lists the isolated workspace entry using `copilot mcp list`; neither check exercises an agent-run tool call. Codex desktop/IDE and Qwen project loading and live memory-tool retrieval also remain unverified. See [MCP setup and retrieval contracts](mcp.md), [Command Code](command-code.md), and the [host source matrix](source-matrix.md).
