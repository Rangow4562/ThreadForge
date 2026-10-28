# Claude Code approved project memory

Claude Code's `UserPromptSubmit` hook can add approved local project memory through `hookSpecificOutput.additionalContext`. The Threadforge hook retrieves only the configured project, reads prompt text transiently for ranking, and returns bounded guidance. It does not persist the prompt, make a model call, or authorize tools/actions. Since the returned context enters the model conversation, Claude Code may send it to its configured provider.

Install the project hook with the project ID used by approved memory records:

```powershell
npm.cmd --prefix adapters run adapter -- claude-code memory install --project-root <absolute-project-path> --command 'threadforge memory inject --project-id <project-id> --threadforge-managed-claude-memory=v1 --threadforge-memory=claude-v1'
npm.cmd --prefix adapters run adapter -- claude-code memory uninstall --project-root <absolute-project-path>
```

Task continuity is opt-in. Add `--task-id <task-id>` and optional `--session-id <session-id>`, or use `--discover-active-task`; `--max-task-entries <1-20>` defaults to 6 when task selection is enabled. The installer validates stable IDs, inserts selectors before the single unquoted `--threadforge-memory=claude-v1` runtime marker, and rejects invalid options or manually duplicated selectors before changing `.claude/settings.json`. Without a task selector, the hook remains memory-only.

Discovery selects only one eligible task in the configured project. Ambiguous task or session selection withholds task details; plan-only snapshots suppress prior progress and handoffs. Task context is untrusted guidance, omits authorization attestations, and does not override current instructions or host permissions. Retrieval failures return an empty no-opinion response. Review and trust project hooks in Claude Code; Threadforge does not change hook trust or permissions.

Installed Claude Code 2.1.303 smoke verifies one configured task-aware hook path with an isolated settings file and loopback fake provider, with zero real model calls. That smoke configures injector flags directly rather than exercising the new adapter installer selectors; ordinary project discovery/trust, model adherence, held-out task efficacy, and cross-harness acceptance remain unverified.
