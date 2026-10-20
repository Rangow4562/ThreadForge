# GitHub Copilot CLI local session history

Threadforge can poll committed session event files below `<copilot-dir>/session-state/<session-id>/events.jsonl`. The default Copilot directory is `~/.copilot`; override it explicitly when needed. This adapter reads only these event files and does not inspect Copilot configuration, credentials, or shell profiles; event delivery is limited to Threadforge's loopback receiver.

```powershell
npm.cmd --prefix adapters run adapter -- copilot sessions status
npm.cmd --prefix adapters run adapter -- copilot sessions sync --print
npm.cmd --prefix adapters run adapter -- copilot sessions sync
```

`status` returns aggregate session/checkpoint counts only. `sync --print` previews up to 500 mapped events without sending or writing a cursor. `--max-sessions` defaults to 1,000 and accepts 1–5,000. `--copilot-dir`, `--state-path`, and `--endpoint` are optional; explicit paths must be absolute, and event intake is loopback-only. The cursor defaults to `state/copilot-sessions.v1.json` under `THREADFORGE_HOME` or the platform cache directory. It stores hashed relative file keys and validated prefix checkpoints, not session IDs or transcript content.

Retention defaults to metadata-only: prompts, assistant text, reasoning, tool arguments/results, paths, repository names, and record IDs are not retained in Threadforge events or cursor state. `--include-content` opts in to prompt and assistant text up to 4 KiB per value; reasoning and tool payloads remain digest-only. Captured event text is sent only to the local `/v1/events` receiver. Do not enable text capture when source transcripts may contain material you do not want retained locally.

Scanning is bounded to 5,000 sessions, 64 MiB per file, 512 MiB per sync, 8 MiB per JSONL row, 100,000 rows per file, 10,000 events per file, and 20,000 events per sync. Symlinks and oversized files are skipped and reported. Partial trailing rows wait for the next sweep. A malformed complete row blocks that file at the preceding valid checkpoint; repair it and rerun. Rewritten prefixes are conflicts and are not silently replayed. Delivery is at-least-once if Threadforge crashes after the local receiver accepts a batch but before cursor persistence.

The `events.jsonl` location and event shapes are documented by the preserved Beacon source as observed Copilot CLI records; this is not a verified, versioned public export contract. Live Copilot CLI compatibility and cross-harness acceptance remain unverified. This slice does not configure MDM-managed OTLP export or claim full GitHub Copilot parity.
