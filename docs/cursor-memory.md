# Cursor session-start project memory

Threadforge can add a bounded project-memory bootstrap to Cursor's project
`sessionStart` hook. Cursor's hook input contains session metadata and its
documented `CURSOR_PROJECT_DIR` environment variable, but no user prompt. The
hook therefore retrieves a small, project-wide approved context packet at
session start; it does not pretend to provide per-prompt relevance. Use the
Threadforge MCP context tool for task-specific retrieval during the session,
or explicitly opt in to a bounded task-resume snapshot at startup below.

The command reads only the configured Threadforge project ID and the local
project memory/procedure stores. It uses the bounded wildcard query with the
existing defaults of eight items, 1,600 approximate tokens, and 9,000 Unicode
characters. Path- or module-scoped entries are withheld when Cursor supplies no
matching scope, stale procedures are withheld, and retrieval caveats remain in
the returned context. No Cursor session identifier is persisted, no prompt is
read, no model call is made, and no remote service is contacted. Missing project
context or local-store errors return an empty JSON object and do not block the
Cursor session.

Cursor's `beforeSubmitPrompt` hook is a submission gate whose output decides
whether a prompt continues; Threadforge does not install it or use it to inject
memory. This integration stays on the non-blocking `sessionStart` path.
Task-resume context remains off by default and can be enabled only by adding
`--task-id` (optionally `--session-id`) or `--discover-active-task` to the
configured command. The hook's `session_id` belongs to Cursor and is never
treated as a Threadforge session ID; use `--session-id` only with an explicit
Threadforge task ID when selecting a Threadforge session.

Install this separately from telemetry hooks. Replace `my-project` with the
project ID used when the approved memory was published, and ensure `threadforge`
is on Cursor's host `PATH`:

```powershell
npm.cmd --prefix adapters run adapter -- cursor memory install `
  --project-root <absolute-project-path> `
  --command 'threadforge memory inject-cursor --project-id my-project --threadforge-managed-cursor-memory=v1 --threadforge-memory=cursor-v1'
```

The adapter merges only a marked command into `.cursor/hooks.json` under
`sessionStart`, preserves user hooks and the independent telemetry hook, and
sets a 10-second timeout. To remove only the Threadforge memory hook:

```powershell
npm.cmd --prefix adapters run adapter -- cursor memory uninstall --project-root <absolute-project-path>
```

Cursor receives `{ "additional_context": "..." }`, or `{}` when nothing
approved and applicable is available. Task context is project-checked; discovery
injects it only for one eligible task and withholds task details if multiple
tasks match. Plan-only snapshots omit prior progress and handoffs. The task
portion is capped at 4,200 bytes and combined context at 9,000 characters.
Returned content is labeled untrusted advisory guidance, never authorization.
Cursor's official [Hooks documentation](https://cursor.com/docs/hooks)
describes `sessionStart` context injection and its output field. Threadforge's
local payload, retrieval, and configuration-preservation tests do not establish
that a particular installed Cursor version loads the hook or uses the context as
intended; verify this in Cursor's Hooks output channel before relying on it.
