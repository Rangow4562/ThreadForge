# Claude Code project skills

Threadforge can install a locally approved, privacy-reviewed procedure export into a Claude Code project skill directory. This is a knowledge-only file operation; it does not install telemetry hooks, modify MCP settings, change project trust, or grant permission to execute the procedure.

## Install and remove

Export and review the procedure locally first:

```powershell
threadforge procedure export-skill --project-root . --id <approved-procedure-id> --privacy-reviewed
```

Then provide the exact export path and SHA-256 printed by that command:

```powershell
npm.cmd run adapter -- claude-code skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd run adapter -- claude-code skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The installer writes only `.claude/skills/<generated-skill-name>/SKILL.md` and a Threadforge ownership marker. It verifies the export digest and generated identity, uses exclusive writes and a per-skill lock, preserves neighboring skills, and refuses unowned conflicts, changed exports, edited installed content, and symlinked paths. Uninstall removes only the exact unchanged file it owns.

This adapter does not test whether a particular Claude Code version loads the file. Installed-host discovery, live procedure retrieval, cloud-session telemetry, and cross-harness acceptance remain unverified; use the file as guidance and verify its applicability against the current project.

## Prompt-time approved-memory retrieval

Claude Code can also request local approved memory on each `UserPromptSubmit`. The Threadforge hook reads the hook's `cwd` and prompt as transient input, retrieves only approved project records for the configured project ID, and returns guidance through the documented `hookSpecificOutput.additionalContext` response. It does not persist or forward the prompt, make model calls, block the prompt, or authorize actions. Retrieval caveats are included when scope or freshness cannot be verified; path-scoped memory is withheld when the hook supplies no path context.

Install the memory hook separately from the telemetry hooks. `threadforge` must be available on the host's `PATH`; replace `my-project` with the same project ID used by the approved memory records:

```powershell
npm.cmd run adapter -- claude-code memory install --project-root <absolute-project-path> --command "threadforge memory inject --project-id my-project --threadforge-managed-claude-memory=v1 --threadforge-memory=claude-v1"
```

Remove only the Threadforge memory hook with:

```powershell
npm.cmd run adapter -- claude-code memory uninstall --project-root <absolute-project-path>
```

The injector is local-first and fail-open: invalid hook input, unavailable local storage, or retrieval errors return `continue: true` without memory. Defaults are `max-items=8`, `max-tokens=1600`, and at most 9,000 Unicode characters of injected context. The hook command may add `--max-items` or `--max-tokens` within the CLI's validated bounds. Retrieved content is explicitly labeled approved guidance, not authorization; freshness and scope warnings are preserved. The project settings update uses a dedicated ownership marker and does not remove telemetry hooks or user-owned prompt hooks.

Task continuity stays off unless the hook command includes either `--task-id <task-id>` or the separate opt-in `--discover-active-task`. Discovery selects a task only when exactly one eligible task exists in the configured `--project-id`: a task marked `in_progress`, or an `open` task with an active session. No task context is added when there are no candidates; when multiple candidates exist, Threadforge adds only a generic selection-required note and withholds candidate IDs, titles, and task content. For a unique task, multiple active sessions are reported without session-specific progress until a session is selected; use explicit `--task-id` plus optional `--session-id` to choose, except plan-only tasks whose session state is omitted entirely. `--max-task-entries <1-20>` (default `6`) bounds handoff entries. The hook includes bounded task/plan/progress and handoff summaries, omits authorization attestations and evidence digests, and marks stored text as untrusted guidance. A structured plan-only task retains its proposal and requirements but omits prior progress, next actions, and handoffs; the hook adds a derived advisory reminder and still does not enforce an execution gate. Since results are added to `additionalContext`, the host may send task content to its configured model provider. Threadforge itself does not persist the incoming prompt or make a remote inference request.

An October 11, 2026 installed-host smoke ran Claude Code `2.1.296` with an explicitly supplied disposable `.claude/settings.json`, temporary `CLAUDE_CONFIG_DIR`, and loopback deterministic Messages provider. The October 19, 2026 smoke on Claude Code `2.1.303` confirms the `UserPromptSubmit` hook's freshness-verified approved fact appears in the model-bound request. Despite passing `--tools ""`, that version's request included built-in tool definitions; the deterministic provider returned text only, and the request stream contained zero tool-use/result blocks. No real model inference occurred. This is transport evidence, not a permission boundary: Threadforge does not grant tool authorization, and the host remains responsible for enforcing its own permissions. Because the smoke passes `--settings` explicitly, it does not verify ordinary project-settings discovery or trust behavior. Other host versions, real-model selection/adherence, failure behavior under normal user settings, and cross-harness task success remain unverified. See the [official Claude Code hook reference](https://code.claude.com/docs/en/hooks#userpromptsubmit) and [installed smoke evidence](installed-skill-smoke.md).

Smoke v27 uses that same explicitly configured hook with `--task-id <id>` and the isolated task database. It confirms the selected draft objective, unfinished-requirements warning, and advisory/non-authorizing safety statement are present in the model-bound request along with the approved fact. The report excludes the task ID and all task text, and the deterministic fixture makes zero real-model calls. This verifies one installed CLI/configuration path only; task selection remains explicit, and the hook does not grant or enforce execution permission.
