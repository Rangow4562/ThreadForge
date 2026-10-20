# Prime Agent local session-history poller

`prime-agent sessions status|sync` (also accepted as `prime sessions status|sync`) is a partial, read-only backfill path for local Prime Agent JSONL transcripts. It scans both session transcripts and subagent artifacts. It does not install Prime Agent's live extension, watch for changes, read credentials, or inject retrieved memory.

```powershell
cd adapters
npm.cmd run adapter -- prime-agent sessions status [--sessions-dir <absolute-path>] [--artifacts-dir <absolute-path>] [--state-path <absolute-path>] [--max-files <1-15000>]
npm.cmd run adapter -- prime-agent sessions sync [--sessions-dir <absolute-path>] [--artifacts-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-15000>] [--print] [--include-content]
```

By default, source roots are `~/.prime/agent/sessions` and `~/.prime/agent/session-artifacts`. `PRIME_AGENT_CODING_AGENT_DIR` replaces `~/.prime/agent`; a leading `~` or `~/` is expanded. The Pi-specific `PI_CODING_AGENT_DIR` is deliberately ignored. Each explicit directory must be absolute, real, and non-overlapping with the other Prime data root. `--max-files` defaults to 1,000 files per root and can be raised to 15,000 per root. `--print` performs a non-mutating preview and cannot be combined with `--include-content`.

The scanner caps traversal at 100,000 directory entries across both roots, 64 MiB per transcript, 512 MiB total per sync, 10 MiB per row, 100,000 rows and 10,000 mapped events per file, 20,000 events per sync, and 10,000 in-memory tool-call correlations. Symlinks, oversized files, truncated traversal, malformed rows, incomplete trailing rows, rewritten prefixes, and rejected event batches are reported as incomplete; data is not treated as verified or silently replayed.

The mapper covers observed session starts, prompts, assistant text and reasoning, tool calls/results, persistent-IPython and Bash command activity, context attachments, model changes, compaction/refinement markers, runtime errors, and numeric token/cost usage when present in a transcript. Subagent artifact events carry only a hashed parent-session relationship. It does not invent session-end or operator-approval events and does not separately attribute MCP calls.

Session IDs, parent IDs, filenames, working directories, repository/branch values, tool-call IDs/arguments, command text, outputs, attachments, reasoning, and prompt/assistant text are not retained raw by default. `--include-content` opts in only user prompt and assistant text up to 4 KiB per item; tool, command, output, and reasoning content remain digest-only. Event delivery is loopback-only through the shared versioned event envelope.

The private atomic Threadforge cursor stores hashed root-relative source identities, complete-line prefix hashes, and numeric progress metadata outside both Prime data roots. Preview does not create it. Prefix rewrites fail closed rather than triggering automatic replay.

The roots and transcript shapes are based on Beacon's documented/local implementation observations, not a verified, versioned Prime Agent export contract. Live extension capture and its installer, runtime discovery, packaging, and cross-harness retrieval remain incomplete. This poller is partial parity and is not cutover evidence.
