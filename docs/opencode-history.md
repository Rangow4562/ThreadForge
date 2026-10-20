# OpenCode session-history poller

Threadforge can poll visible OpenCode session records through OpenCode's own CLI. It does not read `opencode.db` or the legacy session directory directly.

```powershell
npm.cmd --prefix adapters run adapter -- opencode sessions status
npm.cmd --prefix adapters run adapter -- opencode sessions sync --project-root D:\path\to\project --print
npm.cmd --prefix adapters run adapter -- opencode sessions sync --project-root D:\path\to\project
npm.cmd --prefix adapters run adapter -- opencode sessions sync --project-root D:\path\to\project --max-sessions 100
```

The poller runs `opencode session list --format json --max-count <N+1> --pure` from the selected project root and exports each selected session with `opencode export <session-id> --sanitize --pure`. `--pure` prevents external OpenCode plugins from running during these read-only collection calls. The project root is explicit; sessions outside the selected project listing are not scanned. Session IDs, titles, directories, raw messages, and credentials from OpenCode output are not copied into Threadforge state.

Defaults are 50 sessions (maximum 200). The extra list row detects overflow; a truncated session list produces `complete: false` and a nonzero exit. Each export is limited to 16 MiB, and one sync is limited to 128 MiB of combined CLI output. Per-export bounds are 20,000 messages and 50,000 parts; the sync processes at most 20,000 events. A CLI invocation has a 45-second timeout. These are safety ceilings, not a claim that every OpenCode session was exported.

User and assistant text are represented by SHA-256 and byte count by default. `--include-content` is an explicit opt-in that retains up to 4 KiB of user/assistant text per part. Reasoning is always digest-only; tool inputs/results are digested only; file paths, diffs, session titles, and workspace directories are omitted. OpenCode's `--sanitize` flag is used as an additional source-side measure, not as Threadforge's privacy boundary.

The private `threadforge.opencode-history-cursor.v1` file stores SHA-256 session, message/part, and content-state fingerprints under Threadforge state (or an explicit `--state-path` outside the selected project root). It does not contain raw OpenCode IDs or transcript text. Repeated unchanged exports emit no new events. A tool part that changes from pending/running to completed/error produces a new result event. Cursors advance only after loopback intake accepts a batch; a crash between intake and cursor write can still result in an at-least-once retry. `--print` previews without sending events or changing cursors. Remote event endpoints are rejected.

Only supported text, reasoning metadata, tool, compaction, error, and usage parts are mapped. Missing stable IDs or an unknown tool state makes that session incomplete rather than silently checkpointing it. Poll events are separate from the live plugin path and may overlap with live-captured events; there is no cross-collector exactly-once guarantee.

On October 19, 2026, `npm run test:opencode-history-host` passed against the installed OpenCode CLI 1.18.31. In a disposable home/project, the smoke directs a synthetic prompt to a loopback-only fixture provider, then uses the production history adapter to list and export the resulting session and preview metadata-only events. The observed run had one session, three preview events, two local provider requests, zero real-model invocations, no retained prompt/response/session ID, and no cursor write. The smoke is versioned as `threadforge.opencode-history-host-smoke.v1`. This verifies one synthetic CLI/export path only; older/newer versions, full historical coverage, cross-project history, cross-collector deduplication, real task outcomes, and cross-harness retrieval remain unverified and are not cutover evidence.
