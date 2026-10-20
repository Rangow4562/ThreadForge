# DeepSeek Harness adapter

Threadforge provides a partial, digest-first event adapter for the seven Claude-shaped hook events documented by the pinned Beacon DeepSeek Harness source: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `SubagentStart`, and `SubagentStop`. The mapper is `adapters/src/deepseek-hook.ts`; it reuses the existing Claude payload reader, accepts only this seven-event allowlist, identifies events as `deepseek_harness`, and hashes session/call/agent identifiers and content. It never stores prompt or tool text.

The command can be called by a manually reviewed DeepSeek hook file:

```powershell
npm.cmd run adapter -- dsh hook PreToolUse --threadforge-dsh-hook=v1
```

Use the same command shape with each listed event name. The command posts only to the shared loopback `/v1/events` endpoint, with a 250 ms timeout. It always prints `{}` as the bridge's no-opinion response, including when parsing or local delivery fails; telemetry cannot approve, deny, block, rewrite, or delay an action. The adapter does not emit `PermissionRequest` or `SessionEnd`, and does not infer a failed tool from a nonzero shell result.

## Native local history

Threadforge also provides a bounded read-only poller for committed DeepSeek session records. It reads `$DSH_HOME/sessions` (default `$DSH_HOME` or `~/.dsh`) and recognizes `session.jsonl`, `session.vN.jsonl`, and their `.zstd` variants. If a session directory contains both compressed and plain files, the compressed file is selected. The poller reads versioned Zstandard records using Node's built-in decoder; the adapter package therefore requires Node 22.15 or later. Missing/invalid/truncated compressed records are reported as incomplete/errors, never silently treated as an empty store.

```powershell
npm.cmd run adapter -- dsh sessions status [--dsh-home <absolute-path>|--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>] [--workspace <path>] [--session-id <id>]
npm.cmd run adapter -- dsh sessions sync [--dsh-home <absolute-path>|--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--workspace <path>] [--session-id <id>] [--print] [--include-content]
npm.cmd run adapter -- dsh sessions sync --watch [--interval <5s-168h>]
```

Defaults: `--max-sessions 1000` (maximum 5000), cursor `~/.threadforge/state/dsh-sessions.v1.json`, loopback intake `http://127.0.0.1:4318/v1/events`, and watch interval `1m` (minimum 5 seconds). `--dsh-home` and `--sessions-dir` are mutually exclusive, and `DSH_HOME` must be absolute. Session/workspace filters are evaluated against local metadata and are not returned. `--print` previews at most 500 events without delivery or cursor mutation and cannot be combined with `--include-content`.

Read bounds are 20,000 directories and 50,000 files per scan; 64 MiB compressed and 256 MiB expanded per session; a 128 MiB maximum Zstandard decoder window; 512 MiB compressed and 1 GiB expanded across a run; 8 MiB per JSONL line; 100,000 lines and 20,000 decoded records per session; 10,000 mapped events per session and 20,000 per sync. Truncation/bounds produce incomplete status rather than a success-shaped empty result.

Prompts, assistant messages, reasoning, tool arguments/results, session IDs, source paths, and workspace paths are digest-only by default. `--include-content` retains only user/assistant text up to 4 KiB per item; reasoning and tool content remain digest-only. Events are marked `harness.collection_method=poll`, omit approval claims, and can never authorize or affect a tool call. Cursor files contain hashed relative source keys and JSONL prefix hashes, are written privately/atomically outside the selected native source tree, and are checkpointed after accepted loopback batches. Intake/state crashes can cause at-least-once replay; modified prefixes, partial JSONL tails, malformed rows, source bounds, and delivery failures are surfaced for review.

There is deliberately no automatic hook installer in this slice. Beacon's pinned runtime document says DeepSeek requires both a hook JSON file and a Cordis patch entry mounting `@deepseek-ai/dsh-hooks-claude-code`; it also documents that the patch file is user-owned YAML and may contain comments or `!!js` tags. Threadforge does not edit either file until it has a lossless, tested merge strategy and an installed-host contract. If an operator manually configures the bridge, they remain responsible for preserving existing Cordis entries and reviewing the hook commands. Threadforge does not change DeepSeek sandbox mode, host permissions, or project hook scope.

The same pinned Beacon document warns that sandboxed sessions may be unable to write to the local telemetry destination. In that case live capture is lost but the hook still returns `{}`; native polling remains available outside the sandbox. The source exposes no approval decisions, and the hook path has no token/cost fields, tool-failure signal, or session-end event. Threadforge's poller is fixture-tested but not verified against an installed DeepSeek release or real native store; hook/Cordis behavior, packaging, and cross-harness task retrieval remain incomplete, so this adapter is not cutover evidence. Behavior was reviewed from Beacon commit `5303224c63ddb73b7c2a125278f4233d807348d9`, including `docs/runtimes/deepseek-harness.mdx`, `docs/cli/endpoint-dsh.mdx`, and `cli/beacon/internal/dshsession/`; no Beacon implementation code was copied.
