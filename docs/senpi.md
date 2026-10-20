# Senpi (standalone OMO runtime)

Threadforge supports the standalone Senpi runtime, attributed as `omo_senpi` in events to preserve Beacon's pinned telemetry identity. Current Senpi release source reviewed for this implementation was v2026.9.25 at commit `822f1d7ace2b8c1d6d36c87ec9e4b9cdac833988`; OpenCode and Codex CLI integrations remain separate.

Install for the current user (the default):

```powershell
cd adapters
npm.cmd run adapter -- senpi install
npm.cmd run adapter -- senpi status
```

Use `--scope project --project-root <absolute-path>` for a workspace extension. `update` refreshes only an intact Threadforge-owned extension, and `uninstall` removes only that file. Current Senpi loads user extensions from `<agent-directory>/extensions/threadforge.ts` (default `<home>/.senpi/agent/extensions/threadforge.ts`) and project extensions from `<project>/.senpi/extensions/threadforge.ts`. Project extensions load only in host-trusted projects; Threadforge does not change trust. Restart Senpi after install, update, or removal.

For non-default user profiles, `SENPI_CODING_AGENT_DIR` selects the user agent directory; the legacy `PI_CODING_AGENT_DIR` is used only when the Senpi-specific variable is unset. A leading `~` is expanded against the selected home directory. `--senpi-agent-dir <absolute-path>` explicitly selects a user agent directory and takes precedence over environment values. Project scope ignores these user-level overrides.

The extension observes `session_start`, `session_shutdown`, `input`, `tool_call`, `tool_result`, `user_bash`, and `message_end`. It maps lifecycle, prompts, tools, operator commands, file activity, assistant reasoning digests, token counts, and runtime-reported cost. Prompt/tool/command/file/reasoning content is not sent raw; identifiers and paths are hashed. Events go only to loopback `/v1/events`; the extension returns no callback value and does not create approval or enforcement decisions.

Install, update, status, and removal operate on a dedicated `threadforge.ts` file with a SHA-256 ownership header, bounded reads, symlink checks, locking, snapshot validation, and atomic replacement. An unrelated extension or a locally edited managed extension is left untouched and causes a refusal.

This is partial support: the current extension-loader/API and event-version compatibility, release packaging, Senpi executable discovery, and cross-harness acceptance have not been verified against an installed Senpi release. The CLI and inventory verify the extension file's ownership state, not whether Senpi is installed or running.
