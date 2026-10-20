# Kiro hooks

Threadforge provides partial Kiro command-hook capture for user and project scopes. It writes one dedicated v1 JSON file, `threadforge-observational.json`, under the Kiro hooks directory and leaves every sibling file untouched. User configuration is `<KIRO_HOME>/hooks/` (default `~/.kiro/hooks/`); project configuration is `<repo>/.kiro/hooks/`. The manager accepts an explicit `--kiro-home` override. Project scope is useful for repository-local Kiro sessions, but the command itself must be available in the environment where that project hook runs.

Install, update, and remove with:

```powershell
npm.cmd --prefix adapters run adapter -- kiro install --command '<adapter-command> --threadforge-hook=kiro'
npm.cmd --prefix adapters run adapter -- kiro update --command '<new-adapter-command> --threadforge-hook=kiro'
npm.cmd --prefix adapters run adapter -- kiro uninstall

npm.cmd --prefix adapters run adapter -- kiro install --scope project --project-root <absolute-project-path> --command '<adapter-command> --threadforge-hook=kiro'
npm.cmd --prefix adapters run adapter -- kiro uninstall --scope project --project-root <absolute-project-path>
```

The installer appends one `--hook-event=<trigger>` argument to each command. It registers `SessionStart`, `AgentSpawn`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, and `Stop`, with timeouts of 10 seconds for lifecycle/tool events, 30 seconds for prompt submission, and 45 seconds for `Stop`. It omits matchers so the observational hooks match all applicable events. File-change triggers are omitted because they duplicate successful write tools; spec-task triggers remain unsupported until their payload contract is sufficient to identify the task; `Manual` is not an agent event.

Prompts, assistant responses, tool arguments/results, commands, paths, diffs, and error text are never retained. Values up to 64 KiB are represented by SHA-256 digests and byte counts; session and tool-call identifiers are hashed. Known Kiro file, shell, and `@server/tool` MCP names are classified without exposing their arguments. The adapter writes no stdout because Kiro may append hook stdout to model context. It makes no permission decisions, injects no memory, and sends captured events only to the local loopback `/v1/events` endpoint; capture errors fail open.

The config manager validates strict UTF-8 JSON and Kiro's v1 `{version,hooks}` structure, caps the file at 1 MiB, rejects symbolic-link paths and credential-like commands, uses a lock and compare-before-atomic replacement, preserves unrelated hook objects in the managed file, and refuses edited Threadforge entries rather than silently replacing them. Separate JSON files in the hooks directory remain byte-for-byte untouched.

Tests cover event classification/privacy, loopback delivery with empty stdout, install/update/uninstall, both scopes, sibling and in-file hook preservation, malformed/modified config, symlinks, locks, credential rejection, and size limits. The Kiro IDE/CLI is unavailable in this environment, so installed-host payload, version, timeout, project-hook, and Kiro Web execution compatibility remain unverified; this integration is not yet accepted as parity. See the [official Kiro hooks reference](https://kiro.dev/docs/hooks/) and [configuration scopes](https://kiro.dev/docs/configuration/).
