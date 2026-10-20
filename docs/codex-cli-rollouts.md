# Codex CLI rollout backfill

Codex CLI writes local JSONL session rollouts under `$CODEX_HOME/sessions` (default `~/.codex/sessions`). Threadforge can explicitly scan those files and recover session lifecycle, prompt/assistant message markers, tool actions, task completion, and token usage. This is a poll/backfill path, separate from hooks and live OTLP; it does not run during normal Codex use.

Start the local service, preview the events, then sync them:

```powershell
threadforge serve --project-root <project-root>
npm.cmd --prefix adapters run adapter -- codex status
npm.cmd --prefix adapters run adapter -- codex sync --print
npm.cmd --prefix adapters run adapter -- codex sync
```

`codex status` reports hashed source identifiers, file sizes, cursor lines, and whether file metadata still matches the saved checkpoint; it does not read rollout bodies or change state. `--codex-home <absolute-path>` selects another Codex home. `--state-path <absolute-path>` overrides the local cursor file; it must remain outside `CODEX_HOME`. `--endpoint <loopback-url>` overrides the local `/v1/events` endpoint. Remote URLs are rejected. `--print` returns a JSON preview and never creates or advances cursor state; it shows at most 500 events and marks truncation in the summary.

By default, prompts, assistant text, tool arguments, and tool output are not retained or sent. Threadforge stores only SHA-256 digests and byte counts for those fields, along with bounded event metadata; raw session IDs and workspace paths are omitted. `--include-content` is a separate explicit opt-in for local loopback delivery. Retained text is capped at 4 KiB per source content field, with the local event store applying its own credential redaction and field limits. Treat rollout files and Threadforge's local trace directory as sensitive. `--include-content` cannot be combined with `--print` so full content is not written to the terminal by this command. Threadforge never reads `auth.json` or the Codex session index.

The cursor defaults to `<THREADFORGE_HOME>/state/codex-rollouts.v1.json`; without `THREADFORGE_HOME`, it uses the operating system's local cache directory. The state directory is private, the file is written with mode `0600` where supported, and a sibling lock prevents concurrent writers. Checkpoints include complete-line number and a SHA-256 of the consumed prefix so truncation or edits rewind the affected rollout. Incomplete final lines wait for a later sync. A rollout is capped at 64 MiB, each line at 8 MiB, the walk at 10,000 files and 10 directory levels; oversize files, symlinks, malformed rows, partial tails, and processing errors are reported in the result.

Delivery is resumable and at-least-once at the receiver/checkpoint boundary: Threadforge checkpoints after each accepted batch, but a process failure after the local service appends a batch and before the cursor is atomically saved may replay that batch. Event IDs are deterministic for a source path, line, and row fingerprint, but the current event store does not globally deduplicate them. Do not use this poller as migration reconciliation or exactly-once audit evidence.

The parser follows Beacon's documented Codex rollout event shapes and uses synthetic fixtures. No live rollout files or cross-version schemas have yet been validated, so compatibility remains unverified. Token-attribution reconciliation and end-to-end acceptance also remain open. The implementation is a new TypeScript adapter, not copied Beacon source. See [Codex telemetry configuration](codex-cli-telemetry.md), [source matrix](source-matrix.md), and [feature parity](feature-parity.md).
