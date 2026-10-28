# ZCode project MCP

Threadforge manages its ZCode project MCP entry in `.zcode/config.json`, under `mcp.servers.threadforge`:

```json
{
  "mcp": {
    "servers": {
      "threadforge": {
        "type": "stdio",
        "command": "<threadforge-executable>",
        "args": ["mcp", "serve", "--project-root", "<project-root>", "--threadforge-managed-mcp=v1"]
      }
    }
  }
}
```

Install, update, or remove only the Threadforge-owned server:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install --host zcode --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp update --host zcode --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp uninstall --host zcode --project-root <project-root>
```

The preservation-aware manager changes only `mcp.servers.threadforge`, requires `type: "stdio"`, and stores only `type`, `command`, and `args`. It rejects credential-like command/argument values, `cwd`, environment, trust, and other server fields; refuses unowned collisions—including a same-name entry in the alternate root `zcode.json`—or malformed config; and preserves other project settings and MCP servers. Writes use the existing bounded, lock-protected, symlink-aware atomic JSON path. The project entry does not grant trust, start MCP, or authorize tool calls.

ZCode also discovers project config from `zcode.json`; this adapter intentionally uses `.zcode/config.json` and does not modify the alternate file. ZCode's MCP design specifies that project-origin servers remain untrusted until a user explicitly connects them. Threadforge never writes trust state; because the `zcode` executable is unavailable in this environment, that host behavior, project-config discovery, MCP listing/connection, and tool dispatch remain unverified.

This implementation follows ZCode's [configuration guide](https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/docs/usage/03-config.md), [MCP design](https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/docs/design/v2/mcp.md), and [MCP server config types](https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/packages/contracts/src/interfaces/mcp.port.ts). The adapter test proves the file mutation contract only, not host behavior or Threadforge's broader parity/cutover acceptance.
