# Cline local session history

Threadforge provides a read-only poller for Cline history already committed to local storage. It complements the digest-first Cline plugin callbacks; it does not configure Cline, read hosted prompt storage, or make tool-authorization decisions.

The default Cline root is `~/.cline` (or `CLINE_HOME`). The poller checks these documented local stores:

- `data/tasks/<task-id>/api_conversation_history.json`
- `tasks/<task-id>/api_conversation_history.json`
- `data/sessions/<lead-id>/<id>.messages.json`, including subagent message files
- `kanban/workspaces/<workspace-id>/sessions.json` with its adjacent `board.json`
- The VS Code Cline extension task store under the platform's `globalStorage/saoudrizwan.claude-dev/tasks/`

Use `--cline-dir` or `--vscode-storage-dir` to select isolated roots. Explicit roots must be absolute, real directories; symlinked roots and entries are not followed.

```sh
npm run adapter -- cline sessions status [--cline-dir <absolute-path>] [--vscode-storage-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]
npm run adapter -- cline sessions sync [--cline-dir <absolute-path>] [--vscode-storage-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content] [--watch --interval <5s-168h>]
```

Status returns counts and hashed source references, not native task IDs, paths, or message text. `sync --print` is a non-mutating preview and never sends events or advances the cursor. Sync sends historical events only to the local loopback `/v1/events` endpoint and marks them `harness.collection_method=poll`.

Prompts, assistant messages, reasoning, tool calls/results, command outcomes, file activity, MCP calls, and available model/token/cost metadata are mapped. By default, content-bearing values are represented by a SHA-256 digest and byte count. `--include-content` is explicit and retains only user/assistant text up to 4 KiB per event; reasoning and tool arguments/results stay digest-only. Workspace and file paths, Cline task/message IDs, and tool-call IDs are omitted or hashed. No approval decision is inferred from a historical tool result.

The private cursor defaults to `~/.threadforge/state/cline-sessions.v1.json`, outside Cline's stores. It uses a lock and atomic replacement. For task-history/message files, it records a complete-record count and a digest of the committed prefix; an edited or truncated prefix is reported for review and withheld rather than silently replayed. Kanban is mutable, so the poller emits only fields whose content digest changed. Accepted event batches are checkpointed; an interruption after intake acceptance but before cursor persistence can cause at-least-once replay, so event IDs are stable but the server is not assumed to deduplicate them.

Resource limits are deliberate: 1,000 sessions by default (hard cap 5,000), 10,000 task/workspace directories per root, 20,000 discovered files, 25 MiB per source file, 512 MiB aggregate source reads, 100,000 records and 10,000 mapped events per session, 20,000 events per sync, batches of 100, and an 8 MiB cursor. Partial, malformed, changed-during-read, oversized, symlinked, truncated, and intake-rejected sources are reported as incomplete; a successful status does not establish that every source file belongs to a supported Cline version.

Fixture tests cover the pinned Beacon-described layouts. On October 9, 2026, a read-only `cline sessions status` against the local `~/.cline` and VS Code extension-store roots found 2 pending sessions, 0 incomplete sessions, no skipped symlinks/oversized sources, and no errors; status sent no events and wrote no cursor. This is source-backed parsing evidence for those local files, not a pinned Cline version or a live plugin/task test. The Cline executable is absent; JetBrains compatibility, broader source-format coverage, plugin packaging, and cross-harness task outcomes remain unverified. See the [source matrix](source-matrix.md) and [feature parity ledger](feature-parity.md).
