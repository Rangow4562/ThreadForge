# Oh My Pi extension adapter

Threadforge provides a metadata-only Oh My Pi (OMP) extension and an ownership-aware installer. The adapter is an observational capture slice; it does not inject memory, rewrite prompts or tool arguments, or authorize actions.

## Install and remove

Choose the scope explicitly. User scope defaults to OMP's user agent extension directory. `--omp-agent-dir` can select an existing absolute agent directory, which is useful for an isolated profile. Project scope uses `<project>/.omp/extensions/threadforge.ts`.

```powershell
npm.cmd --prefix adapters run adapter -- omp install --scope user
npm.cmd --prefix adapters run adapter -- omp install --scope user --omp-agent-dir <absolute-agent-directory>
npm.cmd --prefix adapters run adapter -- omp install --scope project --project-root <absolute-project-directory>
npm.cmd --prefix adapters run adapter -- omp uninstall --scope project --project-root <absolute-project-directory>
```

The user path follows current OMP configuration rules: `OMP_PROFILE` takes precedence over `PI_PROFILE`; `PI_CONFIG_DIR` selects the config directory (default `.omp`); `PI_CODING_AGENT_DIR` is honored for the default profile, while named profiles use the profile-specific agent directory. Invalid profile/config path values fail closed. Inspect `--help` and the [upstream directory-resolution implementation](https://github.com/can1357/oh-my-pi/blob/main/packages/utils/src/dirs.ts) if your installation uses a custom profile layout.

The installer creates or updates only `threadforge.ts`, keeps user scope private, uses a content-hash ownership marker, and refuses to overwrite/remove an unowned or edited target. Updates use a lock, bounded reads, symlink checks, compare-before-replace, and atomic writes. Other extension files are left untouched. Project-scoped extensions are executable code and may be loaded automatically by OMP without a separate trust prompt; only install into a project you trust. Threadforge does not change OMP trust settings.

## Captured events and privacy

The extension observes `session_start`, `session_shutdown`, `before_agent_start`, `tool_call`, `tool_result`, `tool_approval_requested`, `tool_approval_resolved`, `user_bash`, `user_python`, and `message_end`. It digests prompt, tool input/result, approval reason, and user-entered command/code in-process, then sends only bounded metadata and SHA-256 digests to `http://127.0.0.1:4318/v1/events`. Session and tool-call identifiers are hashed. Assistant token usage is normalized when present; assistant message and reasoning text are never retained. No event content is sent remotely.

Callbacks return no decision or transformed data. Telemetry delivery is asynchronous, restricted to loopback, bounded by a 250 ms request timeout, and errors are ignored so capture cannot change the host action. The extension deliberately omits `input` (which can handle/rewrite user input), `session_stop` and other behavior-changing paths, and MCP transport notifications. Retrieved Threadforge memory is not injected by this adapter.

The host binary is unavailable in this environment. Tests validate the mapper, privacy properties, callback no-op behavior, and file-manager lifecycle, but do not establish live OMP API/version compatibility or cross-harness retrieval. Review the current [upstream extension documentation](https://github.com/can1357/oh-my-pi/blob/main/docs/extensions.md) and [extension event types](https://github.com/can1357/oh-my-pi/blob/main/packages/coding-agent/src/extensibility/extensions/types.ts) before enabling it after an OMP upgrade.
