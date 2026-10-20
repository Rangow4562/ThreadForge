# Claude Code local transcript history

Threadforge can poll Claude Code JSONL transcripts below `~/.claude/projects` (or `<CLAUDE_CONFIG_DIR>/projects`) and submit recovered events to the local `/v1/events` intake. This is a local-file adapter; it does not use Claude Cloud Agents, fetch cloud artifacts, or access credentials.

```powershell
npm.cmd --prefix adapters run adapter -- claude sessions status
npm.cmd --prefix adapters run adapter -- claude sessions sync --print
npm.cmd --prefix adapters run adapter -- claude sessions sync
```

Use `--projects-dir <absolute-path>` when the Claude profile is elsewhere. `status` reports scan/checkpoint counts without reading or returning transcript content. `sync --print` previews at most 500 mapped events without sending or writing a cursor. `--max-files` defaults to 1,000 and accepts 1–5,000. The sync cursor defaults to `state/claude-sessions.v1.json` below `THREADFORGE_HOME`; without that variable it uses the platform cache location. Use `--state-path <absolute-path>` to select another local cursor location. Cursors are private, atomic, and contain only hashed relative file keys plus line/prefix checkpoints.

Content retention is metadata-only by default: prompts, assistant text, reasoning, tool arguments/results, paths, session IDs, and message IDs are not stored in Threadforge events or the cursor. Prompt/assistant text can be opted into with `--include-content`; it is limited to 4 KiB per text value and goes only to the loopback intake. Reasoning and tool arguments/results remain digest-only. Do not enable content capture if a transcript may contain material you do not want retained locally. Remote endpoints are rejected.

The reader skips symbolic links and bounds scans to 5,000 files, 64 MiB per file, 512 MiB per sync, 8 MiB per JSONL row, 100,000 rows per file, and 20,000 events per sync. Partial final rows remain pending until completed. A malformed complete row stops that file at the last valid checkpoint and makes the result incomplete; repair the row and rerun to continue. Rewritten prefixes are reported as conflicts rather than silently replayed. Delivery is at-least-once across a crash between intake acceptance and cursor persistence, so consumers should deduplicate using stable event IDs.

The file location and observed JSONL row shapes are Beacon-documented compatibility inputs, not a verified, versioned Claude Code export contract. The current Claude Code installation/version and live transcript compatibility have not been verified in this environment. Local transcript polling is therefore a partial parity slice, not evidence of full Claude Code or Beacon parity.
