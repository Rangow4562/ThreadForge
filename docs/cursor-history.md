# Cursor local session history

Threadforge can poll both transcript files below `~/.cursor/projects/*/agent-transcripts` and local Composer conversations in Cursor's global VS Code `state.vscdb` (`cursorDiskKV` table). Transcript polling accepts observed JSON, JSONL, and simple `messages`/`events`/`entries` wrapper shapes. Composer support follows Beacon's documented local committed-store behavior; Cursor does not publish a versioned export contract, so schema compatibility remains version-sensitive. Threadforge does not edit Cursor settings or access cloud sessions/credentials.

```powershell
npm.cmd --prefix adapters run adapter -- cursor sessions status
npm.cmd --prefix adapters run adapter -- cursor sessions sync --print
npm.cmd --prefix adapters run adapter -- cursor sessions sync
# To select a non-default Cursor database:
npm.cmd --prefix adapters run adapter -- cursor sessions sync --global-db-path <absolute-state.vscdb> --print
```

`status` returns aggregate file and Composer counts only. `sync --print` previews at most 500 events without delivery or cursor writes. `--max-files` and `--max-composers` default to 1,000 and accept 1–5,000. Explicit `--projects-dir`, `--global-db-path`, and `--state-path` values must be absolute paths. Composer reads require Node.js 22.12 or later for the built-in SQLite read-only mode; Node currently marks `node:sqlite` experimental. The cursor defaults to `state/cursor-transcripts.v1.json` below `THREADFORGE_HOME` or the platform cache directory; it stores hashed relative file keys, record counts, Composer identity hashes, bubble counts, source-prefix hashes, and timestamps outside Cursor projects. Existing v1 file cursors remain readable.

Retention is metadata-only by default: prompts, assistant text, reasoning, tool arguments/results, file paths, Composer IDs, transcript IDs, and project names are not retained in Threadforge events or cursor state. `--include-content` opts in to prompt/assistant text up to 4 KiB per value. Reasoning and tool payloads remain digest-only. Composer summaries/errors are never emitted as text. All events go only to the loopback `/v1/events` receiver.

The scan is bounded to six nested directory levels, 5,000 transcript files, 5,000 composers, 4 MiB per Composer record, 8 MiB per bubble, 512 MiB of SQLite source values per sync, 64 MiB per transcript file, 8 MiB per transcript record, 100,000 bubbles per conversation, 10,000 events per transcript file, and 20,000 events per sync. The Composer database must be a regular non-symlink file and is opened read-only. Symlinked transcript paths and oversized sources are skipped and reported. JSONL rows without a terminating newline remain pending. Malformed records stop at the last valid prefix; edits to processed transcript records or Composer bubbles become conflicts instead of silent replay. Delivery is at-least-once if Threadforge crashes after receiver acceptance but before cursor persistence.

This covers local transcripts and committed Composer records only. Cloud sessions and full host/version/cross-harness acceptance remain incomplete. Both source shapes are based on Beacon-documented observed stores, not versioned public Cursor export contracts; no live Cursor database was used as a test fixture.

## Live response and reasoning hooks

The project hook installer also registers Cursor's `afterAgentResponse`, `afterAgentThought`, and observational `preCompact` callbacks. It emits `agent.message` and `agent.reasoning` events containing only a SHA-256 digest and byte count for supplied text; the response and reasoning text, generation ID, workspace paths, and transcript paths are not retained. `preCompact` emits `context.compaction` with only bounded integer `context_tokens` and `context_window_size` as `gen_ai.context.used_tokens` and `limit_tokens`; malformed or inconsistent counts are omitted, and message lists, summaries, trigger details, and raw prompts are never retained. The handler prints no callback response. It deliberately does not install `beforeSubmitPrompt`, because that hook controls whether a prompt is submitted; Cursor memory remains a project-wide `sessionStart` bootstrap, with task-specific retrieval through MCP. This hook path sends only to the loopback receiver and is not Cursor Cloud telemetry. Live host/version behavior and actual task outcomes remain unverified.
