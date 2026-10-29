# GitHub Copilot CLI project memory

Threadforge does not install automatic prompt-memory injection for Copilot CLI. The CLI's documented command-hook events do not provide a supported way for a command hook to modify the prompt; `userPromptTransformed` belongs to the Copilot SDK callback contract. Accordingly, `copilot memory install|update` now fails before reading or changing project configuration. `copilot memory uninstall --project-root <absolute-project-root>` remains available to remove only Threadforge-marked entries written by the earlier unsupported installer, preserving unrelated hooks.

Use the project MCP server for explicit, read-only memory retrieval instead. Install it through the project `.mcp.json` manager:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install `
  --host github-copilot `
  --project-root <absolute-project-root> `
  --command <absolute-threadforge-executable> `
  --arg mcp --arg serve --arg --project-root --arg <absolute-project-root>
```

After installing, Copilot can explicitly call `threadforge_project_memory_read_v2` with `project_id`, `kind`, and `id`. Results are freshness-checked when the MCP server has the project root, and remain untrusted guidance with `action_ready=false`, `guidance_only=true`, and `execution_authorized=false`. This is an explicit tool read, not automatic prompt injection. See [project MCP setup](mcp.md#github-copilot-cli-project-mcp).

The low-level `threadforge memory inject-copilot` formatter remains for a separately implemented Copilot SDK integration. It consumes camelCase `cwd`, `prompt`, and `transformedPrompt`, uses at most 2,000 UTF-8 bytes of prompt text as the retrieval query, and returns `modifiedTransformedPrompt`. Its task options remain opt-in: `--task-id <id>` with optional `--session-id <id>`, or `--discover-active-task`; `--max-task-entries` defaults to 6 (range 1-20) when a selector is enabled. Do not connect this SDK response formatter to a Copilot CLI command hook.

The installed Copilot CLI 1.0.64 smoke verifies exactly one explicitly selected, read-only project-memory MCP call through a deterministic loopback provider. The report contains no memory text or record IDs and records zero real-model invocations. This is selected-version MCP transport evidence only—not automatic injection, model adherence, fewer repeated failures, or cutover evidence. See [GitHub's Copilot CLI hooks guide](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-hooks), [hooks reference](https://docs.github.com/en/copilot/reference/hooks-reference), and [Copilot SDK documentation](https://docs.github.com/en/copilot/concepts/agents/about-copilot-sdk) for their distinct event contracts.
