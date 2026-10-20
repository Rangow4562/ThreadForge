# Grok Build hooks

Threadforge has partial local-only Grok Build hooks and a local session-history poller. The hook adapter observes `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `Stop`, and `SessionEnd` through a dedicated file at `.grok/hooks/threadforge.json` (user scope: `~/.grok/hooks/threadforge.json`). The poller reads committed local session files; neither path uploads cloud telemetry.

Install a user-level hook set by supplying the command template that resolves to the Threadforge adapter CLI:

```powershell
npm --prefix <absolute-adapters-directory> run adapter -- grok hooks install --scope user --command-template "npm --prefix <absolute-adapters-directory> run adapter -- grok hook {event} --threadforge-grok-hook=v1"
```

Project scope requires an explicit absolute root:

```powershell
npm --prefix <absolute-adapters-directory> run adapter -- grok hooks install --scope project --project-root <absolute-project-root> --command-template "npm --prefix <absolute-adapters-directory> run adapter -- grok hook {event} --threadforge-grok-hook=v1"
```

Grok requires users to trust project hooks separately (for example, with its `/hooks-trust` command). Threadforge does not change that trust state. Restart Grok after installation or removal so new sessions load the hook file.

The manager owns only `threadforge.json`; other files in `.grok/hooks/` are left untouched. Its ownership digest detects edits and causes update/removal to refuse rather than overwrite user changes. Writes use a lock, bounded strict-JSON reads, symlink checks, and compare-before-atomic replacement. User files request owner-only permissions where supported; project files request project-readable permissions. Uninstall removes only the unchanged owned file.

The hook mapper hashes session/workspace identifiers and prompt, tool input/output, command, and error content. It emits only digest/byte-count metadata to the loopback `/v1/events` endpoint. Oversized or structurally excessive content is omitted. `PreToolUse` is observational: no allow/deny decision is inferred or returned. Intake or parse failures are fail-open, produce only a generic stderr message, and do not expose payload text.

## Local session history

The poller scans `~/.grok/sessions/<url-escaped-workspace>/<session-id>/` (or an absolute `--sessions-dir` override) for `summary.json` and `chat_history.jsonl`, with optional `events.jsonl` and matching `terminal/*.log` output. It maps prompts, assistant/reasoning digests, tool invocations/results, approvals, turn-end context, and session start. A turn end is not treated as a session end.

```powershell
npm --prefix <absolute-adapters-directory> run adapter -- grok sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]
npm --prefix <absolute-adapters-directory> run adapter -- grok sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-url>] [--max-sessions <1-5000>] [--print] [--include-content]
```

`--max-sessions` defaults to 1,000 and is capped at 5,000. Each chat/events file is capped at 64 MiB, `summary.json` at 1 MiB, each terminal log at 8 MiB, terminal logs per session at 64 MiB, and total source bytes per run at 512 MiB. JSONL rows are capped at 4 MiB and 100,000 lines per file; one session maps at most 10,000 events and one sync at most 20,000. Preview returns at most 500 events and performs no delivery or cursor writes. `--print` cannot be combined with `--include-content`.

By default, prompt, assistant, reasoning, tool, command, and terminal-output text is not retained; content is represented by bounded hashes and byte counts. `--include-content` explicitly allows only user/assistant text up to 4 KiB per item; reasoning and tool data remain digest-only. State is stored outside Grok's session tree at `~/.threadforge/state/grok-sessions.v2.json`, keyed by hashed relative session identities with independent chat/events physical-line checkpoints and prefix digests. Partial tails wait for completion, malformed rows and changed prefixes are reported, and changed terminal-log sets after checkpoint are surfaced as conflicts rather than silently replayed. Delivery is loopback-only and at-least-once around the receiver/checkpoint boundary.

The implementation is tested against pinned Beacon-observed payload and file fixtures; it does not read `prompt_context.json` or arbitrary attachments. Installed Grok Build payload/version behavior, actual project trust, live hooks/history compatibility, packaging, and cross-harness retrieval remain unverified. The host row is still partial and this is not cutover evidence.
