# DeepSeek Harness MCP access

DeepSeek Harness can load Cordis configuration overlays supplied with `--patch`, but it does not automatically discover Threadforge project MCP files. Threadforge therefore provides a non-mutating renderer for a standalone overlay:

```powershell
npm.cmd --prefix adapters run adapter -- dsh mcp print-patch `
  --project-root <absolute-project-root> `
  --command <threadforge-executable> `
  --arg mcp --arg serve --arg --project-root --arg <same-project-root>
```

Save the emitted YAML as `.dsh/threadforge-mcp.patch.yml`, review its executable and project path, then explicitly pass it on each DeepSeek Harness invocation, for example:

```powershell
dsh web --patch .dsh/threadforge-mcp.patch.yml
```

The overlay inserts `@deepseek-ai/dsh-mcp-client` with the server name `threadforge`, `transport: stdio`, and only the configured command/args. It contains no `env`, trust, auto-approval, or permission override. The command validates that `mcp serve --project-root` matches the selected absolute project root and adds Threadforge's reserved ownership marker. It prints YAML only; it does not write the project, install the host plugin, launch DeepSeek Harness, or make a tool call.

This is a manual, per-invocation MCP path, not automatic project integration. DeepSeek Harness documents `--patch` as an explicit configuration overlay and warns that stdio MCP commands are trusted executable processes outside the agent sandbox. Review the command and preserve DeepSeek Harness' own permission controls. The `dsh` executable is unavailable in this environment, so config loading, tool discovery, and dispatch remain unverified.

The renderer follows the official [MCP client configuration](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/mcp/mcp-client/README.md), [MCP overlay fixture](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/mcp/mcp-client/tests/fixtures/pagination-limit.patch.yml), and [CLI patch-layer reference](https://github.com/deepseek-ai/deepseek-harness/blob/master/apps/cli/reference/README.md). It is a convenience for composing a manual overlay, not DeepSeek Harness parity or cutover evidence.
