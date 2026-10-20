# Cursor task plan gate

Threadforge can install an opt-in project `preToolUse` hook for Cursor's direct
`Write` tool. The adapter adds a `Write` matcher with `failClosed: true` to
`.cursor/hooks.json`; it preserves other hooks and settings and removes only the
Threadforge-owned command on uninstall. This is separate from observational
telemetry and approved-memory hooks.

The guard requires a selected Threadforge task with finalized requirements, an
open or in-progress status, a confirmed `governed_execution` plan, and a target
inside that plan's implementation scope. Cursor's `tool_input` must identify one
unambiguous path through `path` or `file_path`; conflicting or missing paths,
invalid hook input, unsupported operations, and failed task/scope checks produce
a flat `{"permission":"deny"}` response. An in-scope operation returns `{}`
so Threadforge does not issue an allow. Denial messages do not echo target paths,
file contents, or stored task data.

The gate covers only Cursor's direct `Write` tool. It does not gate shell, MCP,
other tool classes, or writes performed outside that tool, and it does not
sandbox execution or close time-of-check/time-of-use races. It is an additional
task-scope guard, not a security boundary or replacement for Cursor's own
permissions. The configured project hook uses a 10-second timeout and
`failClosed: true`; operators should account for blocked writes if Threadforge
cannot start or respond.

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

The Go contract tests exercise an in-scope no-opinion result, out-of-scope
denial, unsupported-tool denial, and private response shape. Adapter tests cover
install/update/uninstall, marker ownership, fail-closed configuration, and
preservation of unrelated settings. These tests do not establish that an
installed Cursor version loads the project hook or enforces the documented
response; perform a host smoke before relying on it.
