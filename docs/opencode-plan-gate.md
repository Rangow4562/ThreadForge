# OpenCode task-plan gate

The OpenCode project plugin has an opt-in, deny-only task-plan check for direct file tools. It is off unless both `--plan-gate-task-id` and `--plan-gate-project-id` are supplied when installing or updating the Threadforge plugin.

```powershell
npm.cmd run adapter -- opencode install --project-root D:\work\project --module @threadforge/adapter-contracts/opencode-plugin --plan-gate-task-id task-parser --plan-gate-project-id parser-project
```

The two gate options must be supplied together. `opencode update` without them removes the gate configuration while retaining the owned plugin and any memory settings explicitly supplied to that update. The installer writes only its marked V1/V2 plugin entries and preserves sibling plugins.

On a supported callback, the plugin sends the tool name and arguments over stdin to the local command selected by `THREADFORGE_CLI` (default `threadforge`), using `task plan guard --host opencode`. It does not use a shell. The active OpenCode project root is bound from the host callback context. The request is capped at 1 MiB, the response at 64 KiB, and the subprocess at 5 seconds. Invalid configuration, failed startup, timeout, malformed output, or a denial raises a fixed, content-free error before the tool runs.

The covered names are `write`, `edit`, and `apply_patch`, when the host exposes those as direct tool callbacks. V1 accepts OpenCode's `filePath` arguments and V2's `path` arguments; patch/file conflicts are rejected. The task must have finalized requirements, a valid plan, and a confirmed `governed_execution` decision whose scope contains every parsed target. A positive guard result is only “no opinion”: OpenCode's normal permissions and approvals remain authoritative.

This is best-effort defense in depth, not a security boundary. It does not constrain shell/terminal commands, MCP or other external tools, subagents, unknown/custom file tools, effects outside parsed target paths, or time-of-check/time-of-use races. Use OpenCode's own permission/sandbox controls for those boundaries. The static `threadforge-plan` read-only profile is separate and does not bind itself to task state.

The implementation includes Go guard tests and adapter tests for opt-in defaults, argument bounds, failure denial, and V1/V2 callback propagation. The isolated OpenCode V2 2.3.0 smoke now makes a deterministic direct `write` call for an unconfirmed task and requires the `execute.before` rejection, a host-visible denial, and an absent sentinel file. The versioned receipt is content-free and reports zero real-model calls, zero external proxy attempts, and `execution_authorized:false`. This is fixture evidence for one pinned V2 version and one tool; it does not establish model behavior, other file tools/versions, or a security boundary. See [CI details](ci.md) and the [feature parity ledger](feature-parity.md).
