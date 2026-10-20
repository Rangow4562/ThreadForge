# Configured runtime inventory

`threadforge inventory scan` performs an explicit, read-only local scan of known runtime configuration candidates, MCP server metadata, and skill manifests. It complements the dashboard's event-derived observed activity; a configured runtime is not evidence that telemetry is installed or working.

By default, the command scans the current user's home directory and current project directory, excludes system- and managed-scope configurations, and emits JSON metadata only. Use `--home` or `--project-root` to select different roots, `--runtime <name>` to limit the scan (repeat the flag or pass comma-separated names), and `--include-system` to opt into system/managed candidates. Both roots must exist, be directories, and have no symlinked path components.

The CLI intentionally has no configuration-content capture option. It can report local paths, path/file hashes, modification times, parser status, MCP command names and argument counts, safe environment-key names, and whether a URL is present; it does not return configuration bodies, command arguments, URL values, or credentials. Config reads are capped at 1 MiB and skill manifests at 256 KiB. Symlinks and non-regular files are not followed; files that change during a read are rejected.

Senpi extension inventory distinguishes the legacy Beacon OMO `beacon.ts` paths from Threadforge's current `threadforge.ts` paths under `~/.senpi/agent/extensions` and project `.senpi/extensions`. Separate `beacon_managed` and `threadforge_managed` metadata identifies ownership; default inventory does not read or return extension bodies.

The dashboard scan is separately user-triggered with a button and `POST /v2/inventory/scan`; page load and routine dashboard refresh do not scan configuration. It scans the current home and, only when the service was started with an explicit `--project-root`, that project root; otherwise project scope is skipped (the service process working directory is never substituted). The v2 response returns aggregate totals plus per-runtime counts for configuration files, MCP servers, skill manifests, incomplete sources, and user/project/other scope counts. It does not return paths, hashes, server names, definitions, or configuration values, and results are not persisted or team-shared. Both versions enforce same-origin requests, reject request options, and share a single-scan gate. The compatible `POST /v1/inventory/scan` endpoint remains aggregate-only and retains its v1 schema.

Examples:

```powershell
threadforge inventory scan
threadforge inventory scan --project-root D:\work\my-project --runtime claude_code,codex_cli
threadforge inventory scan --include-system
```

This is partial Beacon inventory parity: forwarding/export, detailed inventory views, and live host verification remain separate work.
