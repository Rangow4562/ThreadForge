# Antigravity CLI hooks

Threadforge provides a partial Antigravity integration: an observational hook adapter plus an opt-in project memory bootstrap. The telemetry hook manager installs at user scope by default or project scope when explicitly selected:

- User: `<home>/.gemini/config/hooks.json`
- Project: `<project>/.agents/hooks.json`

Install with the complete command that launches the adapter and includes its ownership marker:

```powershell
npm.cmd --prefix adapters run adapter -- antigravity install --command 'node --experimental-strip-types "<absolute-path-to-antigravity-hook.ts>" --threadforge-hook=antigravity'
npm.cmd --prefix adapters run adapter -- antigravity install --scope project --project-root <absolute-project-path> --command 'node --experimental-strip-types "<absolute-path-to-antigravity-hook.ts>" --threadforge-hook=antigravity'
```

Use `antigravity update` with the same scope and command to replace only marked Threadforge hooks. Use `antigravity uninstall` with the matching scope to remove them. The default scope is `user`. Review the generated file and restart Antigravity so it reloads the configuration. The source-based Node command is for development; a distributable hook executable/package remains a release gap.

The observational manager writes the `threadforge-endpoint` owner block using the direct event schema: `PreInvocation`, `PostInvocation`, and `Stop` are handler arrays; `PostToolUse` uses a `matcher: "*"` group. It installs only these four non-decision events. It deliberately does not install `PreToolUse`, whose callback can control tool authorization and requires a decision, or `UserPromptSubmit`, which is not part of the current documented event list. Updates clean only Threadforge-marked legacy refs from the former nested schema while preserving user-owned refs. The manager also preserves unrelated top-level configuration and owner blocks, refuses malformed or oversized JSON, rejects symlink paths and unowned collisions, and removes only references containing `--threadforge-hook=antigravity`. Configuration writes are lock-protected and atomic. This telemetry path does not edit MCP trust, grant permissions, inject context, or return allow/deny decisions.

## Approved project memory at invocation start

Antigravity's `PreInvocation` hook can add a transient system message. Threadforge uses it for a project-scoped bootstrap of already-approved local memory. Task-resume context is disabled by default and can be enabled only by explicitly adding `--task-id` (optionally `--session-id`) or `--discover-active-task` to the configured command. It does not capture or persist the current prompt or conversation. Install it separately from observational telemetry:

```powershell
npm.cmd --prefix adapters run adapter -- antigravity memory install --scope project --project-root <absolute-project-path> --command 'threadforge memory inject-antigravity --project-id <project-id> --threadforge-managed-antigravity-memory=v1 --threadforge-memory=antigravity-v1'
npm.cmd --prefix adapters run adapter -- antigravity memory uninstall --scope project --project-root <absolute-project-path>
```

The adapter manages only the `threadforge-approved-memory` block in `<project>/.agents/hooks.json`, uses a direct `PreInvocation` handler with a 10-second timeout, and preserves other hook owners and the separate `threadforge-endpoint` block. Install only in a project whose hook execution you trust; Threadforge does not change Antigravity trust settings or tool permissions.

The Go hook accepts only `invocationNum` 0 and exactly one real path in `workspacePaths`. It retrieves up to 8 approved items within an approximate 1,600-token budget, using `*` because `PreInvocation` provides no prompt. When task context is explicitly enabled, it adds a bounded read-only snapshot for the configured task or for a uniquely discovered eligible task in the configured project; cross-project tasks are withheld, and ambiguous discovery reports that selection is required without exposing task details. Session-specific progress is included only when a session is explicitly selected or the task has an unambiguous session. Plan-only snapshots omit prior execution progress and handoffs. The task portion is limited to 4,200 bytes and the combined context to 9,000 characters. Later model invocations, ambiguous/missing workspaces, invalid payloads, and local retrieval failures emit `{}`. A successful result is `{ "injectSteps": [{ "ephemeralMessage": "..." }] }`; the message is transient, untrusted guidance, not authorization. Conversation identifiers are validated but never stored or returned; transcript and artifact paths are ignored. No model or remote service is called.

## Optional project task-plan gate

Antigravity's documented `PreToolUse` hook is a separate, opt-in permission hook. Install it only in project scope; it manages a `threadforge-task-plan-gate` owner block in `.agents/hooks.json` and matches exactly `write_to_file`, `replace_file_content`, and `multi_replace_file_content`. The command hook has a 10-second timeout. It does not modify the `permissions` settings, trust list, telemetry, `threadforge-endpoint`, or `threadforge-approved-memory` blocks.

The command is `threadforge task plan guard --host antigravity-cli` with explicit `--task-id`, `--project-id`, `--project-root`, and optionally `--db`. The guard requires finalized task requirements, an open/in-progress task, a confirmed `governed_execution` plan, exactly one `workspacePaths` root matching the configured project, and an unambiguous `toolCall.args.TargetFile` inside implementation scope. Invalid state, scope, paths, or unsupported tools return `{ "decision": "deny", "reason": "..." }` without echoing task/file content or target paths.

Antigravity requires a decision on `PreToolUse`, so an in-scope file tool returns `{ "decision": "allow" }` only as a pass-through to the next hook/default policy. Threadforge sets neither `permissionOverrides` nor `auto_approve`; Antigravity's default/user policy still governs the action. This differs from host hooks where an empty object means no opinion. The gate does not cover shell or MCP effects, sandbox execution, or time-of-check/time-of-use races. Hook startup/timeout failure semantics are not asserted as fail-closed, so this is not a security boundary. See Google's [Antigravity Hooks documentation](https://antigravity.google/docs/hooks).

```powershell
npm.cmd --prefix adapters run adapter -- antigravity plan-gate install `
  --scope project `
  --project-root <absolute-project-path> `
  --command '"<absolute-threadforge-exe>" task plan guard --host antigravity-cli --task-id <task-id> --project-id <project-id> --project-root "<absolute-project-path>" --threadforge-managed-antigravity-plan-gate=v1 --threadforge-plan-gate=antigravity-v1'
npm.cmd --prefix adapters run adapter -- antigravity plan-gate uninstall `
  --scope project --project-root <absolute-project-path>
```

Smoke v31 probes the project hook path with installed Antigravity CLI `1.4.2`, a local fake provider, an incomplete synthetic task, and one schema-valid `write_to_file` request. The observer recorded **zero hook callbacks**; the CLI returned a generic tool error and the sentinel stayed absent, so the file result cannot be attributed to Threadforge's guard. The report records `status=not_observed`, `hook_callback_invocations=0`, and `guard_decision=unknown`, with zero real model calls. Do not rely on this project gate in `agy` CLI 1.4.2. Antigravity IDE callback behavior, other CLI versions, in-scope pass-through, failure semantics, and broader permission coverage remain unverified.

The installed smoke report also distinguishes `probe_not_exercised` when the deterministic provider completes a turn without issuing the requested host tool call. That result is not equivalent to `not_observed` after a tool call and does not establish a denial. The aggregate smoke continues to later host checks for these explicit non-passing states; this does not change the unverified enforcement status above.

On October 22, 2026, Antigravity CLI `1.4.4` returned the installed v32 plan-gate subcheck as `probe_not_exercised` (two fixture requests, zero host tool calls/callbacks, unknown guard decision, no host tool error or sentinel, and zero real-model invocations). The report is now emitted, but this result still provides no evidence that the project hook denied an operation.

The mapper retains only bounded SHA-256 digests and byte counts for supported tool payloads (digests are omitted above 64 KiB), hashes conversation/call identifiers, bounds tool labels, and drops workspace paths, raw prompts, tool arguments/results, diffs, and error text. It sends events only to the local loopback `/v1/events` intake. Capture and intake failures are fail-open, and the observational hook writes no response body to stdout.

The adapter/config tests exercise payload minimization, local delivery, telemetry and memory install/update/uninstall, user and project paths, preservation, ownership conflicts, malformed/oversized config, symlink refusal, and stale locks. On October 12, 2026, installed `agy` reported version `1.3.3`; in an isolated workspace, Threadforge wrote and read back `.agents/mcp_config.json`, but `agy mcp list` returned `No MCP servers configured.` and its list help exposed no scope flags. Smoke v16 on October 15 ran on Antigravity CLI `1.3.5` and exercised the headless project-MCP path: the project entry points to the isolated SQLite DB, read-only retrieval tools carry `antigravity/alwaysLoad` metadata, and `agy --print` made one `threadforge_procedure_get` call whose result and final response contained the expected fixture guidance. The run used an isolated home, dummy Gemini API key, loopback-only provider, and a local proxy that blocked outbound attempts; `real_model_invocations` was zero. Thus `mcp list` still does not enumerate the project entry, but the tested CLI did load and dispatch it in this controlled run. This does not prove real-model selection/adherence, ordinary trust/settings discovery, hook callback behavior, or task efficacy. Hook discovery, callback payloads, timeout/response semantics, and behavior across CLI releases remain unverified. This is not a complete Beacon parity claim.

On October 19, 2026, installed Antigravity CLI `1.4.2` also passed smoke v24's explicit task-resume MCP check. The same synthetic draft task used by OpenCode was read through the project server via Antigravity's generic `call_mcp_tool` wrapper; the loopback fixture verified the objective and the `requirements_not_finalized`, `action_ready=false`, `guidance_only=true`, and `execution_authorized=false` result fields. The report excludes the task ID and content and records zero real-model invocations. The read is explicit, not automatic injection; task efficacy, normal user settings/trust discovery, host hook behavior, and full Antigravity parity remain unverified. Schema: [`antigravity-task-resume-smoke-v1`](../schema/antigravity-task-resume-smoke-v1.schema.json).
