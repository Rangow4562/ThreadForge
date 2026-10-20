# Goose local session history

Threadforge has a read-only, bounded poller for Goose's local session database. It does not install Goose hooks, alter Goose configuration, or send history anywhere except the local Threadforge OTLP/HTTP intake.

```powershell
npm.cmd run adapter -- goose sessions status [--db-path <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-500>]
npm.cmd run adapter -- goose sessions sync [--db-path <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-500>] [--max-messages <1-2000>] [--print] [--include-content]
```

`GOOSE_PATH_ROOT` selects Goose's data root when set to an absolute path; otherwise the adapter uses the platform data directory. `--db-path` overrides discovery. The default cursor is a private Threadforge state file; `--state-path` is useful for isolated installations and tests. Status and errors never return filesystem paths, session names, working directories, provider/model settings, or raw session IDs.

The reader opens only a regular, non-symlink SQLite database in read-only mode and holds a read transaction. It accepts Goose `schema_version` 17 and requires the audited `sessions` and `messages` columns; unknown versions or shapes fail closed. It reads only user sessions, visible user/assistant messages, and the fields needed for event timestamps/content digests. Tool/system roles and messages whose metadata has `userVisible=false` are skipped. Goose's upstream schema contract was inspected at commit `3bd852002903e016ff30947e973f76e2fcfcf90f` in `crates/goose/src/session/session_manager.rs`; this is schema evidence, not an installed-host compatibility claim.

Capture is digest-only by default. `--include-content` is explicit and may retain only visible text blocks from user/assistant messages, capped at 4 KiB per message. Tool payloads, reasoning blocks, paths, session names, and identifiers are never emitted. `--print` is a non-mutating preview and cannot be combined with `--include-content`. The event endpoint must be loopback HTTP.

Sync defaults to at most 500 sessions and 2,000 new messages per session, with hard caps at those values, 20,000 source messages/events per sync, 1 MiB per source message, and 64 MiB aggregate source content. Larger histories are reported incomplete instead of silently advancing a cursor. Cursor state contains SHA-256 session keys, a numeric last-row position, and the digest of the last checkpointed source row; missing or rewritten anchors fail closed. Cursors advance only after accepted local event batches (or across rows intentionally filtered from capture), and the Goose database is never updated by Threadforge. The status and sync response contracts are in `schema/goose-history-status-v1.schema.json` and `schema/goose-history-sync-v1.schema.json`.

Tests use generated schema-v17 SQLite fixtures; they cover read-only source preservation, default privacy, hidden/tool filtering, opt-in text, preview, idempotency, cursor conflicts, unsupported schema versions, and per-sync bounds. On October 11, 2026, the Goose CLI was not on PATH and no database existed at the resolved Windows default path, so installed-host compatibility was not tested. Goose prompt discovery/use, hook installation, complete historical reconciliation, packaging parity, and cross-harness task-success evidence remain open; this adapter is a partial history-capture slice, not Goose parity.
