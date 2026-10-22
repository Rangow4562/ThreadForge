# GitHub Copilot CLI task-plan gate

Threadforge can install an opt-in, project-only `preToolUse` hook for the GitHub Copilot CLI. The hook uses Copilot CLI's repository hook file format and flat response contract: it returns `{}` when it has no denial to add, or `{"permissionDecision":"deny", ...}`. It never returns `allow`.

```powershell
npm.cmd --prefix adapters run adapter -- copilot plan-gate install `
  --project-root D:\work\my-project `
  --bash-command 'threadforge task plan guard --host github-copilot --task-id <task-id> --project-id <project-id> --project-root /d/work/my-project --threadforge-managed-copilot-plan-gate=v1 --threadforge-plan-gate=copilot-v1' `
  --powershell-command 'threadforge task plan guard --host github-copilot --task-id <task-id> --project-id <project-id> --project-root D:\work\my-project --threadforge-managed-copilot-plan-gate=v1 --threadforge-plan-gate=copilot-v1'
```

The installer writes only `.github/hooks/threadforge-plan-gate.json`; other hook files and unrelated hook entries are preserved. It adds a `preToolUse` matcher for Copilot CLI's direct `create`, `edit`, `apply_patch`, and `str_replace_editor` tools, with a 10-second timeout. Platform-specific `--bash-command` and `--powershell-command` values are both required together; the shorthand `--command` writes the same shell-compatible command to both fields. Replace the example project paths with the corresponding absolute path in each shell. `update` replaces only the marked Threadforge entry; `uninstall` removes only that entry. The manager does not change `disableAllHooks`, repository trust, or any tool-permission setting.

For a write, the Go guard checks the configured task and project, finalized requirements, the confirmed `governed_execution` decision, and every resolved target path against the task's declared implementation scope. Multi-file `apply_patch` requests are accepted only when every `Update File`, `Add File`, `Delete File`, and optional `Move to` path is parsed and in scope. Unknown or malformed write shapes are denied. `str_replace_editor`'s `view` operation returns no opinion because it is read-only.

This is defense in depth, not a sandbox or a security boundary. It does not govern shell commands, MCP tools, or other hosts, and it cannot prevent a path changing between check and write. GitHub documents command-hook timeouts as always fail-open, including `preToolUse`: on timeout the hook's denial is not applied and the call proceeds through Copilot's normal permission flow. Keep Copilot's native permissions and other safety controls enabled. See GitHub's [Copilot CLI hooks reference](https://docs.github.com/en/copilot/reference/hooks-reference).

Installed-host evidence: on October 22, 2026, smoke v33 exercises Copilot CLI `1.0.64` in a temporary Git repository, with trust configured only in disposable `COPILOT_HOME`, a loopback-only fake provider, and a deliberately missing task ID. The host exposed `create` and `edit`; both `preToolUse` denials were observed in tool results, the create target remained absent, the edit target remained byte-for-byte unchanged, and no real model was invoked. This installed CLI did not expose `apply_patch` or `str_replace_editor`, so those matcher entries remain unverified on this host. The result is partial and does not establish behavior for other task/scope states, timeout enforcement, other CLI versions, model-driven adherence, efficacy, or cutover.
