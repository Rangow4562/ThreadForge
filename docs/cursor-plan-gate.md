# Cursor task plan gate

Threadforge can install an opt-in project `preToolUse` gate in `.cursor/hooks.json`.
The gate uses two fail-closed matchers: `Write` for direct file edits, and
`Shell|MCP:.*` for shell and MCP calls. It preserves other hooks and settings
and removes only Threadforge-owned commands on uninstall. This is separate
from observational telemetry and approved-memory hooks.

For direct writes, the guard requires a selected Threadforge task with finalized
requirements, an open or in-progress status, a confirmed `governed_execution`
plan, and one unambiguous `tool_input.path` or `file_path` inside the declared
implementation scope. Conflicting or missing paths, invalid input, unsupported
operations, or failed task/scope checks return a flat deny response.

Shell calls and MCP tools not on the exact read-only Threadforge allowlist
(`threadforge_context_get`, `threadforge_project_memory_read_v2`,
`threadforge_procedure_list`, `threadforge_procedure_get`,
`threadforge_procedure_guide`, `threadforge_task_resume_context`, and
`threadforge_task_discover`) require the same finalized, open/in-progress,
`governed_execution` task and a working directory inside the configured project.
Unknown MCP tools are treated as potentially mutating. When checks pass,
Threadforge returns `{}` and leaves the decision to Cursor's normal permission
flow. Threadforge never returns `allow`. Denial messages do not echo commands,
tool arguments, paths, or stored task data.

The shell and MCP checks only verify task/plan state and the project working
directory; they cannot prove command effects, MCP effects, or file targets stay
inside the task's implementation scope. Only direct `Write` paths are
scope-checked. Other tools and writes outside these matched callbacks are not
covered, and the hook does not sandbox execution or close time-of-check/time-of-
use races. This is an additional guard, not a security boundary or replacement
for Cursor's own permissions. Both matchers use a 10-second timeout and
`failClosed: true`; unavailable or malformed hook responses block the matched
action according to Cursor's hook behavior.

Install it separately from Cursor telemetry and memory hooks. Provide the same
task/project IDs and absolute project root in the command:

```powershell
npm.cmd --prefix adapters run adapter -- cursor plan-gate install `
  --project-root <absolute-project-path> `
  --command 'threadforge task plan guard --host cursor --task-id <task-id> --project-id <project-id> --project-root <absolute-project-path> --threadforge-managed-cursor-plan-gate=v1 --threadforge-plan-gate=cursor-v1'
```

Remove only this gate with:

```powershell
npm.cmd --prefix adapters run adapter -- cursor plan-gate uninstall --project-root <absolute-project-path>
```

The Go contract tests exercise in-scope no-opinion, out-of-scope denial,
plan-only shell/MCP denial, the exact read-only MCP allowlist, governed
shell/MCP pass-through, unsupported-tool denial, and private response shape.
Adapter tests cover install/update/uninstall, marker ownership, fail-closed
configuration, and preservation of unrelated settings. The response contract is
defined in `schema/cursor-plan-gate-hook-result-v1.schema.json`. Tests do not establish
that an installed Cursor version loads the project hook or enforces the
response; perform a host smoke before relying on it.
