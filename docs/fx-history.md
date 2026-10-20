# Vercel fx local session history

Threadforge provides a read-only local poller for the Beacon-observed Vercel fx session store. It does not install hooks, configure fx, inspect MCP credentials, or access a private database. fx's installed/versioned file contract has not been independently verified; treat this integration as partial until a live-host contract test is available.

## Commands

```powershell
npm.cmd run adapter -- fx sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]
npm.cmd run adapter -- fx sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content]
```

The default source is `~/.fx/sessions`; the default cursor is `fx-session-cursor.v1.json` under Threadforge's private state directory (or `$THREADFORGE_HOME/state`). `sync` is explicit; schedule it externally for periodic backfill. `--print` maps committed events without sending them or mutating the cursor. It cannot be combined with `--include-content`.

## Observed store and event mapping

Each session directory contains `events.jsonl` and, when available, `session.json`. The poller accepts the observed manifest version 3 with `storage_format: event_log_v1`, and reads only up to `event_log_bytes`; a manifest that advances beyond the log, a malformed manifest, or an unsupported schema is surfaced as incomplete. A missing manifest falls back to the available log bytes. Only newline-terminated, bounded event envelopes with schema version 1 are considered; unknown event kinds are skipped and counted, while malformed/unsupported committed frames stop the safe prefix.

Mapped record kinds are `session_started`, `preferences_changed`, `workspace_rebound`, `history_turn_committed`, and `usage_checkpointed`. They produce session/context, prompt and assistant, tool/file/command/MCP, compaction, and token-usage events. Poll-derived events are marked `harness.collection_method=poll`; this is after-the-fact collection, not enforcement or approval telemetry. Permission feedback is digested and never interpreted as an allow/deny decision. fx does not provide a reliable session-end signal, so Threadforge does not invent one.

The default stores digests and byte counts for prompt/assistant text, tool arguments/results, command text/output, diffs, and workspace/file paths. `--include-content` permits only prompt and assistant text up to 4 KiB per message to be sent to the selected local event endpoint; tool, command, path, and diff data remain digest-only. Event IDs combine hashed session identity, hashed log generation, sequence, and event position, making re-reads deterministic without exposing source identifiers.

Turn input/output usage comes from `turn_summary.token_progress` when present, otherwise from positive differences in the session totals. Cumulative usage checkpoints emit cache-token and cost deltas, not cumulative totals, to avoid double-counting. Provider/model labels are retained only when they pass bounded safe-label checks.

## Bounds and recovery

Discovery defaults to 1,000 sessions and caps at 5,000; each event log is limited to 64 MiB, each manifest to 1 MiB, each JSONL frame to 8 MiB, and each session log to 100,000 lines/10,000 mapped events. Aggregate reads are capped at 512 MiB and one sync at 20,000 events. Symlinks are skipped or rejected. The cursor stores a hashed session key, hashed generation, sequence, prefix hash, and bounded status metadata under Threadforge state, with a private atomic write and single-writer lock; placing it inside the fx source tree is refused.

The cursor advances only after each mapped source-record batch is accepted by loopback intake. Generation changes reset the sequence cursor but preserve the already-reported session-start state, avoiding duplicate `session.started` events after compaction. A same-generation prefix rewrite is reported as a conflict. Partial rows and malformed/unsupported committed frames are surfaced rather than silently treated as caught up. An interrupted process between intake acceptance and cursor persistence may replay a batch; stable event IDs identify those repeats.

The feature-parity ledger and [source matrix](source-matrix.md) retain the unresolved installed-host/export compatibility, packaging, and cross-harness retrieval gates. This slice is not cutover evidence.
