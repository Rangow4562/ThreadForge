# Claude Code task-plan deny-only gate

Threadforge can install an explicit project-level Claude Code `PreToolUse` hook for the direct `Write` and `Edit` tools. The hook is opt-in and bound to one configured task, project ID, and project root:

```text
npm run adapter -- claude-code plan-gate install --project-root <absolute-project-root> --command "<threadforge task plan guard --task-id <task-id> --project-id <project-id> --project-root <absolute-project-root> --threadforge-managed-claude-plan-gate=v1 --threadforge-plan-gate=claude-v1>"
```

The installer manages only the uniquely marked `PreToolUse` reference with matcher `Write|Edit`; it preserves existing telemetry, memory, and user-owned hooks. Use `plan-gate update` to change the command and `plan-gate uninstall` to remove only Threadforge's marked reference.

For each matched event, `threadforge task plan guard` reads a bounded hook payload and checks that the explicit task belongs to the configured project, is open or in progress, has finalized requirements, has a structurally valid `governed_execution` plan, and declares the target file within its include patterns and outside all excludes. Recursive `**` segments match zero or more path segments. The path must resolve under the configured local project root; unresolved parents and out-of-root symlink targets fail closed. The command reads task state from Threadforge's local SQLite store and does not persist or echo the hook payload or target path.

When any check fails, stdout returns a Claude `PreToolUse` `permissionDecision: "deny"` response using `threadforge.claude-plan-gate-hook-result.v1`. When checks pass, stdout is `{}`: the guard emits no `allow` decision, and Claude's normal permission flow still decides whether the tool may run. `plan_only`, unconfirmed, missing, stale/invalid, or wrong-project task state therefore blocks these direct file tools without turning stored plan confirmation into Threadforge tool authorization.

This is a deny-only host integration for `Write` and `Edit`, not a filesystem sandbox or complete execution boundary. It does not inspect shell commands, MCP tools, other agents/tools, or effects performed outside those two direct file tools; it cannot prevent path changes between the hook check and tool execution. A missing/disabled hook or unavailable local CLI also means this check is not active. Review the installed project hook and retain Claude's own permission controls. Live Claude Code activation, host-version compatibility, and task-outcome acceptance have not been verified.

Qwen Code has a corresponding project-only guard for direct `write_file` and `edit` tools, with its own response and trust boundary: [Qwen Code plan gate](qwen-code.md#deny-only-direct-file-task-plan-guard).
