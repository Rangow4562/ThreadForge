# Qwen Code hooks

Threadforge provides a partial, observational Qwen Code hook adapter. User scope is the default; project scope is available when explicitly selected:

- User: `<home>/.qwen/settings.json`
- Project: `<project>/.qwen/settings.json`

Install or remove the managed hook references with:

```powershell
npm.cmd --prefix adapters run adapter -- qwen install --command 'node --experimental-strip-types "<absolute-path-to-qwen-hook.ts>" --threadforge-hook=qwen'
npm.cmd --prefix adapters run adapter -- qwen install --scope project --project-root <absolute-project-path> --command 'node --experimental-strip-types "<absolute-path-to-qwen-hook.ts>" --threadforge-hook=qwen'
npm.cmd --prefix adapters run adapter -- qwen update --command '<complete-new-hook-command-with-marker>'
npm.cmd --prefix adapters run adapter -- qwen uninstall
npm.cmd --prefix adapters run adapter -- qwen uninstall --scope project --project-root <absolute-project-path>
```

The manager preserves unrelated settings and non-Threadforge hook refs, adds the `--threadforge-hook=qwen` ownership marker, and refuses malformed/oversized JSON, symlink paths, unsafe commands, and invalid event groups. It does not set `disableAllHooks`, modify trust state, or grant tool permissions. If `disableAllHooks` is already `true`, installation refuses. Project hooks require Qwen's normal trusted-folder decision; the CLI warns about this and never changes trust. Qwen must be restarted to reload settings.

The adapter registers `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `Stop`, `SubagentStart`, `SubagentStop`, and `SessionEnd`. Tool hooks use matcher `*`; every command ref declares `shell: "bash"`. Qwen timeouts are milliseconds: prompt 30,000; tool and permission events 10,000; stop 45,000. Session/subagent hooks omit timeout and use the host default. On Windows, Bash must be available to Qwen; Threadforge does not install a shell.

The mapper keeps bounded SHA-256 digests and byte counts for prompts and tool arguments (values over 64 KiB are omitted), hashes session/tool/subagent identifiers, classifies Qwen's closed built-in tool set, and omits raw prompt/tool content, paths, results, diffs, and error text. `PermissionRequest` remains an observed request, not an inferred decision. `PreToolUse` replies with `{}` and never emits `permissionDecision`, so the host's normal approval flow remains in control. Context-window counts are reported as context utilization, not token spend. Delivery is local-only to `/v1/events`, with a 250 ms timeout and fail-open behavior.

Tests cover mapper privacy, tool taxonomy and failure precedence, response shape, user/project configuration, millisecond timeouts, preservation, idempotency, conflicts, malformed/oversized input, symlink refusal, and trusted-folder warnings. No Qwen executable is available in the current environment, so payload/version behavior, host response parsing, Bash launch, and live project-trust behavior remain unverified. Beacon parity is not complete.

## Project-scoped MCP access

Qwen Code project MCP servers are configured in `.qwen/settings.json`. Threadforge manages only `mcpServers.threadforge` with a local command and bounded arguments:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install --host qwen-code --project-root <absolute-project-path> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <same-project-path>
npm.cmd --prefix adapters run adapter -- mcp update --host qwen-code --project-root <absolute-project-path> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <same-project-path>
npm.cmd --prefix adapters run adapter -- mcp uninstall --host qwen-code --project-root <absolute-project-path>
```

The manager preserves other Qwen settings and MCP servers, requires the embedded project root to match, rejects inline credentials, and writes no `env`, `cwd`, or trust field. Qwen MCP updates share `.threadforge-qwen-hooks.lock` with hook and approved-memory changes, avoiding concurrent lost updates. Threadforge does not modify project trust or Qwen permissions. Live Qwen project-config loading and memory-tool retrieval remain unverified. See the [Qwen MCP documentation](https://qwenlm.github.io/qwen-code-docs/en/users/features/mcp/) and [MCP setup](mcp.md).

## Approved project-memory retrieval

Qwen Code's `UserPromptExpansion` event can append `hookSpecificOutput.additionalContext` to the expanded model prompt. Threadforge provides a separate, opt-in project memory hook for this event; it is not part of the observational telemetry installer:

```powershell
npm.cmd --prefix adapters run adapter -- qwen memory install `
  --scope project `
  --project-root <absolute-project-path> `
  --command 'threadforge memory inject-qwen --project-id my-project --threadforge-managed-qwen-memory=v1 --threadforge-memory=qwen-v1'
```

Task-resume context is separately opt-in. The installer can add an explicit task/session selection to the marked command:

```powershell
npm.cmd --prefix adapters run adapter -- qwen memory update `
  --scope project `
  --project-root <absolute-project-path> `
  --command 'threadforge memory inject-qwen --project-id my-project --threadforge-managed-qwen-memory=v1 --threadforge-memory=qwen-v1' `
  --task-id <task-id> `
  --session-id <session-id> `
  --max-task-entries 6
```

Alternatively, use `--discover-active-task` instead of `--task-id` and `--session-id`; discovery returns details only for one eligible task. Do not combine installer selection flags with task-resume flags already present in `--command`. If no selector is supplied, the hook stays memory-only. The installer accepts only lowercase stable task/session IDs and appends bounded selection arguments to the marked command. Explicit task IDs and optional session IDs are stored in project `.qwen/settings.json`; review the file before committing it.

Configured explicit tasks are checked against the hook command's `--project-id`; discovery returns task details only when exactly one eligible task exists in that project. Ambiguity is surfaced without task details. `--max-task-entries` defaults to 6 (range 1-20); task text is bounded to 4,200 bytes and the combined hook context is capped at 9,000 characters. Plan-only tasks omit prior session progress and handoffs. Task snapshots are untrusted, advisory context and never grant permissions or authorize execution. `UserPromptExpansion` can also run for model-generated prompt expansion, so the hook does not treat the prompt as proof of user intent; the prompt is used only as the transient memory-retrieval query and is not stored. Since returned memory/task context is model-bound, treat it as potentially retained in Qwen's session history and sent to the provider configured for that Qwen session; Threadforge does not control host retention or provider settings.

The injector requires `hook_event_name=UserPromptExpansion`, `cwd`, and a bounded `prompt`. Prompt text is used transiently as the retrieval query (maximum 2,000 bytes); Threadforge does not persist or forward it, call a model, or contact a remote service. Retrieval defaults to eight items and 1,600 approximate tokens, with a 9,000-character output ceiling. Only approved, project-scoped memory and procedures are returned; missing path/module applicability is surfaced and inapplicable material is withheld. The returned context is explicitly advisory and never grants tool permission or authorizes execution. Model-authored commands may also trigger this event, so its prompt is treated as untrusted query text rather than proof of human intent.

The installer edits only a marked `UserPromptExpansion` command in project `.qwen/settings.json`, sets the Qwen timeout to 30,000 ms, and preserves telemetry hooks, user hooks, MCP settings, `disableAllHooks`, and trust configuration. The project must already be trusted by Qwen; Threadforge does not grant trust. Remove only the memory hook with:

```powershell
npm.cmd --prefix adapters run adapter -- qwen memory uninstall --scope project --project-root <absolute-project-path>
```

This uses Qwen's documented `UserPromptExpansion` response shape. Local contract and round-trip tests do not verify a live Qwen executable, version compatibility, hook response parsing, or actual context use. See the [Qwen Code hook reference](https://qwenlm.github.io/qwen-code-docs/en/users/features/hooks/).

For a portable advisory block in Qwen Code's project instruction file, use the reversible `bootstrap instructions` command targeting `QWEN.md`; it does not modify Qwen settings or enable hooks. See [project instruction bootstrap](instruction-bootstrap.md).

## Deny-only direct-file task-plan guard

An optional project-only `PreToolUse` hook can deny Qwen's direct `write_file` and `edit` tools when the explicitly selected Threadforge task lacks finalized requirements, an active governed-execution plan, or a matching in-scope target:

```powershell
npm.cmd --prefix adapters run adapter -- qwen plan-gate install `
  --scope project `
  --project-root <absolute-project-path> `
  --command 'threadforge task plan guard --host qwen-code --task-id <task-id> --project-id <project-id> --project-root <absolute-project-path> --threadforge-managed-qwen-plan-gate=v1 --threadforge-plan-gate=qwen-v1'
```

The hook uses the anchored matcher `^(write_file|edit)$` and a 10-second timeout. A denied edit returns Qwen's `PreToolUse` `hookSpecificOutput.permissionDecision: "deny"`; a valid in-scope request returns `{}` and leaves Qwen's normal permission decision authoritative. It never emits `allow`. The output contract is [qwen-plan-gate-hook-result-v1](../schema/qwen-plan-gate-hook-result-v1.schema.json). Install/update/uninstall manage only the marked plan-gate ref and preserve telemetry, memory, user hooks, MCP settings, `disableAllHooks`, and trust. `disableAllHooks=true` causes installation to fail, and project hooks still require Qwen's normal trusted-folder approval; Threadforge does not grant trust.

Important limitation: this is best-effort denial, not a fail-closed security boundary. Qwen's current command-hook contract treats most hook errors as non-blocking, so missing executables, timeouts, malformed output, or host/runtime issues may let the tool call continue. Keep Qwen's native permissions and sandbox enabled; do not rely on this hook as the sole control for sensitive operations. See the [Qwen hook contract](https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/hooks.md#exit-code-behavior-command-hooks).

This is not a sandbox: it does not intercept shell, MCP, `replace`, `notebook_edit`, other tools, or filesystem changes between check and use. Local Go/config contract tests do not verify a live Qwen executable, installed-version tool-input fields, Bash launch behavior, or end-to-end enforcement. See also the [Claude Code deny-only plan gate](claude-code-plan-gate.md).
