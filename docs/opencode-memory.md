# OpenCode approved-memory injection

OpenCode project memory is opt-in. The normal OpenCode plugin install remains telemetry-only; prompt-time retrieval is enabled only when `--memory-project-id` is supplied.

```powershell
npm.cmd --prefix adapters run adapter -- opencode install `
  --project-root <absolute-project-root> `
  --module @threadforge/adapter-contracts/opencode-plugin `
  --memory-project-id <threadforge-project-id> `
  --memory-max-items 8 `
  --memory-max-tokens 1600
```

Task-resume snapshots are a separate opt-in. Select one task explicitly:

```powershell
npm.cmd --prefix adapters run adapter -- opencode update `
  --project-root <absolute-project-root> `
  --module @threadforge/adapter-contracts/opencode-plugin `
  --memory-project-id <threadforge-project-id> `
  --memory-task-id <threadforge-task-id> `
  --memory-session-id <threadforge-session-id> `
  --memory-max-task-entries 6
```

Or request project-scoped discovery only when exactly one eligible active task exists:

```powershell
npm.cmd --prefix adapters run adapter -- opencode update `
  --project-root <absolute-project-root> `
  --module @threadforge/adapter-contracts/opencode-plugin `
  --memory-project-id <threadforge-project-id> `
  --memory-discover-active-task `
  --memory-max-task-entries 6
```

Do not combine explicit task selection with discovery; `--memory-session-id` requires `--memory-task-id`. Task IDs, optional session IDs, and selection settings are written to the managed `.opencode/plugins/threadforge.ts`; review that file before committing it. With neither task option, the plugin remains memory-only. Discovery with multiple eligible tasks withholds their details and reports that selection is needed. Explicit task snapshots are checked against the configured project ID. `--memory-max-task-entries` is bounded to 1-20 and defaults to 6 when task selection is enabled.

Update with the same options to keep memory enabled. Remove the Threadforge plugin with `npm.cmd --prefix adapters run adapter -- opencode uninstall --project-root <absolute-project-root>`; this removes only the managed `.opencode/plugins/threadforge.ts` file. The installer preserves the existing ownership checks and does not modify OpenCode trust, permissions, MCP configuration, or credentials.

At runtime the plugin uses OpenCode's active `context.directory` as the project root, with `context.worktree` only as fallback. This keeps retrieval bound to the directory selected for the current OpenCode run when it differs from the repository worktree root.

The opt-in plugin uses `experimental.chat.messages.transform`, which exposes the model-bound message list before provider dispatch. It finds the latest user message, extracts only bounded text parts (maximum 2,000 UTF-8 bytes), and sends that transient query through stdin to `threadforge memory inject-opencode`. Returned approved context is appended to the existing user text with an explicit untrusted-memory boundary; the plugin never uses assistant text, image payloads, or tool arguments as a retrieval query. If there is no valid user text, the message shape is malformed, or retrieval fails, the hook leaves the messages unchanged. An existing Threadforge memory marker prevents duplicate insertion when OpenCode rebuilds a request. The transform does not provide verified file-path context, so path-scoped procedures remain withheld or surfaced as a retrieval gap; use Threadforge MCP tools for explicit path- or task-specific reads.

The plugin does not retain a prompt cache. Each transform handles only the latest user text in the current model-bound request. The local CLI child is limited to 2 seconds and 96 KiB of output. It receives only allowlisted OS and Threadforge store-path environment variables, not the OpenCode provider's API credentials or proxy settings. Retrieval failures and timeouts are fail-open: OpenCode receives its unmodified messages.

Only approved project memory and procedures are eligible for retrieval. Threadforge uses its normal applicability, freshness, conflict, privacy, and token-budget checks. An explicitly configured task snapshot is read-only, project-checked, bounded, and carries finalized-requirements/plan warnings; plan-only snapshots omit prior execution progress and handoffs. Unique-task discovery withholds details when selection is ambiguous. Raw query text is held in process memory only long enough for local retrieval and is not written to Threadforge telemetry or files. Injected memory and task data are explicitly untrusted guidance; neither authorizes tools, commands, or actions. The installed-host smoke uses a deterministic loopback provider and zero real-model calls; it verifies query-matched memory and explicit task-context transport only, not model adherence or fewer task failures. The messages-transform hook is experimental and must be reverified for each supported OpenCode version.

Set `THREADFORGE_CLI` to an absolute local CLI path when `threadforge` is not on OpenCode's `PATH`. `THREADFORGE_HOME`/`THREADFORGE_DB` select the local store in the same way as the regular Threadforge CLI. No remote inference is performed by this adapter.
