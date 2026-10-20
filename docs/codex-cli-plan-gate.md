# Codex CLI task-plan gate

Threadforge offers an explicit, project-scoped, deny-only PreToolUse hook for Codex CLI apply_patch and exec_command tools. The task-plan command checks the selected task in local SQLite. Direct apply_patch calls are checked against the plan's path scope and denied when requirements are not finalized, the task or plan is invalid, the decision is plan_only, or any requested path is outside the declared implementation scope. exec_command calls are denied unless the task has finalized requirements and a valid governed_execution plan. For governed_execution shell commands the hook returns no opinion; it does not parse their effects or check their paths.

## Install and remove

Build the command with the actual task, project, local store, and absolute root:

    npm.cmd --prefix adapters run adapter -- codex plan-gate install --project-root <absolute-project-path> --command 'threadforge task plan guard --host codex-cli --task-id <task-id> --project-id <project-id> --project-root <absolute-project-path> --db <local-threadforge.sqlite> --threadforge-managed-codex-plan-gate=v1 --threadforge-plan-gate=codex-v1'
    npm.cmd --prefix adapters run adapter -- codex plan-gate uninstall --project-root <absolute-project-path>

Install/update adds a PreToolUse matcher for ^(apply_patch|exec_command)$ in the project .codex/hooks.json. It preserves unrelated event groups and hook entries and uses a five-second hook timeout. Uninstall removes only the Threadforge-owned hook. The installer does not change Codex config.toml, enable the hooks feature, or modify project trust. Enablement and hook review/trust remain the user's responsibility.

## Limits

The hook is best-effort. Codex hook launch failures, timeouts, malformed output, or disabled/untrusted hooks may fail open. plan_only and invalid/incomplete tasks deny exec_command, but a governed_execution shell command receives no opinion and its effects are not scoped. MCP, other tools, and check-to-write races are not covered. It is not an execution sandbox or security boundary. Retrieved task and plan data is guidance and does not itself authorize execution.

Installed smoke v29 used Codex CLI 0.163.0, a disposable project/home, a deterministic loopback Responses provider, hooks enabled only in the temporary config, and workspace-write sandboxing. The fixture scripted exactly one exec_command that would create a sentinel file. Codex returned the Threadforge PreToolUse denial to the fixture, the sentinel stayed absent, and real_model_invocations remained zero. The request exposed exec_command but not direct apply_patch. This verifies one specific configured host path; it does not prove normal trust discovery, real-model behavior, governed-shell scope enforcement, other versions, or complete host parity. See the Codex hooks overview and installed-host smoke documents.
