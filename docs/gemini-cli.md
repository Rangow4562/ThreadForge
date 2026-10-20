# Gemini CLI integrations

Threadforge's adapter configures Gemini CLI's user-level `~/.gemini/settings.json` to send OTLP/HTTP logs, traces, and metrics to the loopback service at `http://127.0.0.1:4318`. Start `threadforge serve` before expecting telemetry to be accepted.

```powershell
cd adapters
npm.cmd run adapter -- gemini install
npm.cmd run adapter -- gemini update
npm.cmd run adapter -- gemini uninstall
```

The installer enables the local target and collector, selects HTTP, sets `logPrompts` to `false`, and leaves detailed `traces` disabled. Gemini still exports trace spans when `traces` is false; the option controls detailed trace attributes. The receiver accepts all three signals. Raw telemetry remains in the local event store and is not forwarded remotely.

The installer preserves unrelated JSON settings and only adds telemetry keys that are absent. Existing conflicting values or a configured `telemetry.outfile` cause a no-change failure for review. It records only the names of keys it introduced in `~/.gemini/.threadforge-telemetry.json`; uninstall removes those keys only if their values still match Threadforge's settings. User-owned values and values changed after installation are retained. The settings and ownership files are size-limited, checked for symlinks, locked during updates, and atomically replaced.

Gemini CLI environment variables can override user settings. Review `GEMINI_TELEMETRY_*` variables if status or observed delivery disagrees with this configuration. A passing adapter test does not establish a live Gemini CLI contract; live-host and cross-harness acceptance remain pending.

## Project approved-memory injection

Gemini CLI can retrieve approved local project memory on `BeforeAgent`. The hook receives the prompt and working directory as transient stdin, queries only the configured Threadforge project, and returns bounded guidance through `hookSpecificOutput.additionalContext`. Prompt text is not persisted or forwarded by Threadforge, no model call is made for retrieval, and injected memory cannot grant permission or authorize actions. The default retrieval bounds are eight items, 1,600 approximate tokens, and 9,000 Unicode characters; the command timeout is 10 seconds.

Install the project hook separately from user-level telemetry and the project MCP server. Replace `my-project` with the project ID used by the approved records:

```powershell
npm.cmd --prefix adapters run adapter -- gemini memory install --project-root <absolute-project-path> --command 'threadforge memory inject-gemini --project-id my-project --threadforge-managed-gemini-memory=v1 --threadforge-memory=gemini-v1'
npm.cmd --prefix adapters run adapter -- gemini memory uninstall --project-root <absolute-project-path>
```

The manager updates only `.gemini/settings.json` `hooks.BeforeAgent`, shares a lock with the Gemini project MCP installer, and preserves unrelated hooks, MCP entries, telemetry settings, and user trust choices. Gemini project hooks are subject to Gemini's project-trust rules; Threadforge does not mark a project trusted or enable a global hook setting. Retrieval errors fail open with an empty hook response. Review the [Gemini CLI hook reference](https://geminicli.com/docs/hooks/reference/) for the host contract. Installed Gemini CLI `0.42.0` now has a deterministic smoke for one configured hook invocation and returned task-context markers; normal user trust/settings discovery, model consumption, and task-level usefulness remain unverified.

Task continuity remains off unless the configured command includes `--task-id <task-id>` or the separate opt-in `--discover-active-task`. Discovery selects a task only when exactly one eligible task exists in the configured `--project-id`: a task marked `in_progress`, or an `open` task with an active session. No task context is added when there are no candidates; if multiple tasks match, the hook adds only a generic selection-required note and withholds all candidate task details. For a unique task, multiple active sessions remain ambiguous and their progress entries are withheld until selected; use explicit `--task-id` plus optional `--session-id`, except a `plan_only` task omits session state altogether. `--max-task-entries <1-20>` defaults to `6`. Authorization attestations and evidence digests are omitted, stored text is labeled untrusted guidance, and plan-only snapshots retain the proposal/requirements but suppress prior progress, next actions, and handoffs. A derived plan-only reminder is advisory; host permissions remain authoritative. Since `additionalContext` becomes model context, Gemini may send task content to its configured model provider. Threadforge itself does not persist the prompt or make a remote inference request.

Smoke v28 installs this opt-in hook in a disposable project and verifies that Gemini CLI invokes it once, the Threadforge command returns the selected task objective and advisory safety markers, and the hook's output is forwarded while the host completes a fake-response turn. The smoke does not make a real model request and does not prove a model consumed or followed the injected context.

## Project task-plan gate

The optional `gemini plan-gate` installer manages only project `.gemini/settings.json` `hooks.BeforeTool`. Its matcher is `^(write_file|replace)$`; it does not install shell or MCP hooks. The Threadforge command returns Gemini's flat `{"decision":"deny","reason":"..."}` response when the selected task is missing, its requirements are not finalized, its plan is not confirmed as `governed_execution`, or a direct file target is outside the declared implementation scope. An in-scope operation returns `{}`—never an allow decision. The Go guard also checks the project root, hook working directory, and target path before evaluating scope.

Install with a real task and project ID whose approved plan includes the target paths:

```powershell
npm.cmd --prefix adapters run adapter -- gemini plan-gate install --project-root <absolute-project-path> --command 'threadforge task plan guard --host gemini-cli --task-id <task-id> --project-id <project-id> --project-root <absolute-project-path> --threadforge-managed-gemini-plan-gate=v1 --threadforge-plan-gate=gemini-v1'
npm.cmd --prefix adapters run adapter -- gemini plan-gate uninstall --project-root <absolute-project-path>
```

The installer uses a 10-second timeout, shares the lock for `.gemini/settings.json`, and preserves other `BeforeTool` groups, the separate `BeforeAgent` memory hook, MCP settings, telemetry, and trust choices. Gemini project trust remains controlled by the user. This is defense in depth, not a security boundary: a host hook failure may fail open, it does not constrain shell or MCP side effects or other tools, and filesystem changes between path validation and the host write (TOCTOU) are not prevented. Installed smoke v30 verifies one `write_file` denial and one `replace` denial against disposable file fixtures under fake responses; it makes zero real-model calls and does not prove normal trust discovery, protection of other tools, or task efficacy.

## Project memory over MCP

For project-scoped read-only memory retrieval, merge a Gemini MCP server into the project's `.gemini/settings.json` through the adapter CLI:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install --host gemini-cli --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp uninstall --host gemini-cli --project-root <project-root>
```

The installer preserves other Gemini project settings and MCP servers, adds the Threadforge ownership marker, and sets `trust` to `false` so Gemini's confirmation behavior is not bypassed. It does not mark the project trusted. Gemini project settings are subject to the host's trust rules; live retrieval and cross-harness task acceptance remain unverified. Threadforge context is guidance, not execution authorization.
