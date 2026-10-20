# Muse Code hooks

Threadforge provides a partial observational adapter for Muse Code. The current Meta documentation describes project hooks in `<project>/.muse/hooks.json`, user hooks in `~/.config/muse/settings.json`, and a separate managed-hook file selected by `managed_hooks_path`. Threadforge exposes only explicit user or project installation; it never claims or overwrites the managed-hook slot.

Choose a scope explicitly. Project hooks are loaded only after the project is trusted; user hooks apply machine-wide. Both invoke shell commands outside Muse Code's sandbox, so review the exact adapter command and the generated hook file before starting Muse Code:

```bash
npm --prefix adapters run adapter -- muse install --scope user --command '<adapter-command> --threadforge-hook=muse'
npm --prefix adapters run adapter -- muse update --scope user --command '<new-adapter-command> --threadforge-hook=muse'
npm --prefix adapters run adapter -- muse uninstall --scope user

npm --prefix adapters run adapter -- muse install --scope project --project-root <absolute-project-path> --command '<adapter-command> --threadforge-hook=muse'
npm --prefix adapters run adapter -- muse uninstall --scope project --project-root <absolute-project-path>
```

User settings require `schema_version: 1`. The installer preserves unrelated settings, existing user hooks, and any existing `managed_hooks_path`; project installation edits only `.muse/hooks.json`. It does not change project trust or managed policy. `XDG_CONFIG_HOME` selects the user settings root; pass `--xdg-config-home` when an explicit location is needed. Restart Muse Code or start a new session after changing hooks.

The adapter registers `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, `PreCompact`, `PostCompact`, `SubagentStart`, `SubagentStop`, `Notification`, `Stop`, and `SessionEnd`. `PreLLMCall` and `PostLLMCall` are deliberately excluded to avoid capturing whole message arrays at model-call frequency. Permission requests remain observations, never approvals. Hook input is capped at 1 MiB; prompts, tool input/results, and final assistant text are digest-only up to 64 KiB; session, turn, tool, and child-session IDs are hashed. Events go only to local loopback `/v1/events`; the command emits an empty no-decision object and never emits model context or allow/deny policy.

The current official reference documents hook sources and lifecycle names, but not the complete handler and payload schemas. The group/handler layout and payload envelope are implemented against Beacon's MIT-licensed documentation and fixtures as behavior references; no Beacon Muse implementation code was copied. The `muse` executable is unavailable in this environment, so file loading, response handling, event payloads, timeout semantics, and cross-harness retrieval remain unverified. The source row remains partial and is not acceptance evidence.

See Meta's [Muse Code hooks](https://dev.meta.ai/docs/muse-code/extending#hooks), [settings file](https://dev.meta.ai/docs/muse-code/configuration#settings-file), and [permissions](https://dev.meta.ai/docs/muse-code/permissions).
