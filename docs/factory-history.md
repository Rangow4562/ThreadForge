# Factory Droid local session history

Threadforge provides a read-only, opt-in poller for the local session files observed by Beacon under `~/.factory/sessions`. It does not install hooks, modify Factory settings, or access browser/cloud sessions. Treat the directory and record shapes below as Beacon-observed inputs: Factory's live/versioned export contract has not been independently verified.

## Commands

```powershell
npm.cmd run adapter -- factory sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]
npm.cmd run adapter -- factory sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content]
```

The default source is `~/.factory/sessions`; the default cursor is `factory-sessions.v1.json` under Threadforge's private state directory (or `$THREADFORGE_HOME/state`). `status` only reports bounded file counts. `sync --print` maps records without sending events or changing the cursor. `--print` cannot be combined with `--include-content`.

## Observed file shape and mapping

The poller looks for `<encoded-project-directory>/<session-id>.jsonl` and the adjacent `<session-id>.settings.json`. It maps `session_start`, visible `message` content, and compaction records to normalized events. Todo state is not retained. Messages with `visibility` equal to `user_only` or `llm_only` are omitted. Tool calls/results, reasoning, and compaction summaries are represented by digests; tool arguments/results and raw settings are never emitted.

Settings are parsed through a narrow allowlist: `model` plus numeric fields from `tokenUsage`, falling back to `inclusiveTokenUsage` only when `tokenUsage` is absent; usage includes input, output, cache creation/read, thinking tokens, and `factoryCredits`. `providerLock`, child-session usage maps, and all unknown settings are excluded. A changed allowlisted usage projection emits one `token.usage` event; its hash is used for idempotency.

By default, prompt and assistant text are represented by a digest and byte count. `--include-content` permits only user/assistant text of at most 4 KiB per block to be sent to the explicitly selected Threadforge event endpoint. Events are restricted to loopback `/v1/events`; remote forwarding is not available through this adapter. No polling runs automatically.

## Bounds and recovery

The poller caps discovery at 5,000 sessions (default 1,000), each transcript at 64 MiB, each settings file at 1 MiB, aggregate source reads at 512 MiB, each JSONL row at 4 MiB, 100,000 rows per transcript, and 20,000 events per sync. Symlinks, oversized inputs, malformed complete rows, incomplete final rows, read races, and changed transcript prefixes are surfaced rather than followed or silently replayed. Cursor files use hashed relative paths and record-prefix hashes; state is atomically replaced and guarded by a lock. A cursor is advanced only after the corresponding local intake batch is accepted.

The parser and its fixtures cover observed shapes only. Installed Factory version compatibility, hosted sessions, native telemetry/MDM setup, package distribution, and cross-harness retrieval remain unverified. See the [source matrix](source-matrix.md) and [feature parity ledger](feature-parity.md); this partial adapter is not cutover evidence.
