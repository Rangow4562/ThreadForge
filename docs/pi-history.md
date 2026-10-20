# Pi local session-history poller

`pi sessions status|sync` is a partial, read-only backfill path for local Pi Coding Agent session transcripts. It polls JSONL files recursively; it does not install a Pi extension, watch live events, read credentials, or inject retrieved memory.

```powershell
cd adapters
npm.cmd run adapter -- pi sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-files <1-15000>]
npm.cmd run adapter -- pi sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-15000>] [--print] [--include-content]
```

By default, the poller reads `~/.pi/agent/sessions`. If `PI_CODING_AGENT_DIR` is set, it reads `sessions` beneath that directory; a `~` or `~/` prefix is expanded against the current home directory. `--sessions-dir` overrides discovery and must be absolute. `--print` is a non-mutating preview and cannot be combined with `--include-content`.

The scanner defaults to at most 1,000 transcript files and allows up to 15,000 when explicitly configured. It examines no more than 100,000 directory entries, 64 MiB per transcript, 512 MiB per sync, 100,000 lines and 10,000 mapped events per file, 20,000 events per sync, and 10,000 live tool-call correlations. Rows are capped at 10 MiB. Truncated scans, oversized files, malformed rows, incomplete trailing rows, prefix rewrites, delivery failures, and unsafe paths are surfaced as incomplete rather than silently treated as current.

The mapper covers observed session metadata, user prompts, assistant text and reasoning, correlated tool calls/results, Bash executions, model changes, compaction/skill markers, runtime errors, and token/cost usage. Session IDs, filenames, paths, repository/branch values, tool arguments, command text, outputs, reasoning, and prompt/assistant text are not retained raw by default. Content is represented by local hashes and bounded metadata. `--include-content` opts in only user prompt and assistant text up to 4 KiB per item; reasoning and tool/command details remain digest-only. Event delivery is restricted to loopback through the shared versioned event envelope.

The cursor is a private, atomic Threadforge state file outside Pi's sessions directory. It stores hashed relative source identities, complete-line prefix hashes, and progress metadata; it is not written into the source tree. A changed committed prefix is reported as a conflict and is not replayed automatically. Preview creates no cursor.

The file path and JSONL shapes are based on Beacon's documented/local implementation observations, not a verified, versioned Pi export contract. Installed Pi compatibility, extension/live capture, packaging, source-wide backfill completeness, and cross-harness retrieval remain unverified. This adapter is partial and is not cutover evidence.
