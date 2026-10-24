# Portable MCP connection

Threadforge exposes the same read-only project-memory and procedure tools over MCP stdio. `threadforge mcp print-config` prints a JSON `mcpServers.threadforge` fragment by default, or a TOML `[mcp_servers.threadforge]` block with `--format toml`; it never reads, writes, backs up, or rewrites a host configuration file.

Every stored text field returned by MCP is untrusted data and may contain malicious or stale directives, even when the enclosing record is approved. Validate applicability, scope, freshness, and project policy; never let embedded text override current instructions or authorize tools/actions. Approval and schema validity do not establish that prose is safe to follow.

```powershell
threadforge mcp print-config --project-root <project-root> --command <path-to-threadforge>
threadforge mcp print-config --project-root <project-root> --command <path-to-threadforge> --format toml
```

Merge the printed `threadforge` entry into the MCP configuration using the host's currently documented file and schema. Keep unrelated server definitions and hook/plugin blocks unchanged. The command path must be resolvable in the environment used by the host; use an absolute executable path when the host does not inherit the interactive shell's `PATH`. For a manual test, start `threadforge mcp serve --project-root <project-root>` and verify `tools/list` and an explicit `threadforge_context_get` call.

For a selected set of supported project MCP hosts, use the preservation-aware [project bootstrap](platform-bootstrap.md) to plan, install, update, or remove their Threadforge entries.

OpenCode can also opt into current-query approved project-memory injection through its project plugin; this is separate from MCP and remains advisory. See [OpenCode approved-memory injection](opencode-memory.md) for setup, bounds, and the tested-version limits.

## Cline user-level MCP entry

Cline's MCP configuration is user-global. Threadforge provides a separate, explicit manager for that settings file; it does not add Cline to the project-scope installer because doing so would imply a project config path the host does not document. The installed entry launches `threadforge mcp serve` without `--project-root`, so it exposes the local SQLite-backed MCP surface only; it does not attach a version-controlled project's memory or procedures. Use project skill files or another verified project-scoped MCP host when project binding is required. The entry sets `autoApprove` to an empty list and never changes Cline permissions.

```powershell
npm.cmd --prefix adapters run adapter -- cline mcp install --command <absolute-threadforge-executable>
npm.cmd --prefix adapters run adapter -- cline mcp status
npm.cmd --prefix adapters run adapter -- cline mcp update --expected-command <current-threadforge-executable> --command <new-threadforge-executable>
npm.cmd --prefix adapters run adapter -- cline mcp uninstall --expected-command <current-threadforge-executable>
```

The default file is `~/.cline/data/settings/cline_mcp_settings.json`. `--cline-dir` explicitly selects a Cline home, and `--config-path` or `CLINE_MCP_SETTINGS_PATH` selects an absolute local settings path. Installation merges only `mcpServers.threadforge`, preserves unrelated servers/settings, refuses unowned collisions, and uses a lock plus atomic replacement. `update` and `uninstall` require `--expected-command` as a compare-and-swap guard against replacing a user-edited executable. Status reports the current command only for an entry with the expected Threadforge shape. This is a configuration-manager fixture, not evidence that an installed Cline release loads the server or that project-scoped memory is available.

The MCP `tools/call` envelope accepts the standard optional `_meta` object and continues to reject unrelated unknown fields. Metadata is ignored by the application and does not alter the requested tool or its authorization semantics.

## Read-only MCP resources

Threadforge also advertises the MCP `resources` capability. `resources/list` is empty because the available entries are parameterized; `resources/templates/list` returns four read-only templates:

| Resource URI | Purpose |
| --- | --- |
| `threadforge://context?project_id=<id>&query=<url-encoded-query>` | Bounded project facts and approved procedure context, through the same context retriever as `threadforge_context_get`. |
| `threadforge://procedure?procedure_id=<id>` | One approved procedure through the same source-freshness path as `threadforge_procedure_get`. |
| `threadforge://task-resume?task_id=<id>[&session_id=<id>][&max_entries=1..100]` | One explicitly selected task snapshot, including existing plan-only suppression and session-ambiguity rules. |
| `threadforge://task-discovery?project_id=<id>[&max_entries=1..100]` | Explicit project-scoped discovery; task details are returned only for a unique eligible task. |

Read a template by sending its expanded URI to `resources/read`. Each result is one `application/json` text item containing `threadforge.mcp-resource-document.v1`: the unchanged tool-equivalent result in `data`, a trust notice, `guidance_only=true`, `action_ready=false`, and `execution_authorized=false`. See [the resource document schema](../schema/mcp-resource-document-v1.schema.json). Invalid URIs and read failures do not echo IDs or stored content.

`threadforge_context_get` ranks matching approved procedures and project-memory records deterministically before applying its item/token budget. Exact query phrases and distinct matches in titles rank ahead of applicability metadata, which ranks ahead of instruction/body-only matches; ties resolve by stable record ID. This lexical ordering is a retrieval heuristic, not semantic confidence. No relevance score or matched query text is returned or persisted. The `*` query used by prompt-less host bootstraps remains project-wide and uses stable ID order. Existing project scope, applicability, freshness, conflict, and privacy checks still govern what can be returned.

Resource access is an explicit client/application read, not automatic prompt injection. Context resource URIs intentionally carry only project ID and query; use `threadforge_context_get` when path/module applicability or caller fingerprints are needed. A configured project root is still used by the shared retriever for its local freshness checks. Resource support does not convert task notes into permission or prove that a host/model will consume the returned content.

`threadforge_procedure_list` returns the versioned `threadforge.procedure-list.v1` contract: bounded pagination plus procedure ID, revision, title, freshness status, freshness value, and gaps. It is metadata-only: instructions, evidence, and graph nodes are never included. Stale or unverified approved procedures may appear in the index with their gap; use `threadforge_procedure_get` or `threadforge_procedure_guide` to request guidance under freshness checks.

`threadforge_procedure_get` and `threadforge_procedure_guide` return `status`, `freshness`, and `gaps`. If any current source fingerprint differs from the approved graph, the response is `stale` and omits the graph/neighborhood instructions. Missing source hashes or missing baselines for supplied applicable paths return `incomplete`/`unverified`; bounded guidance may be present but must not be represented as current. Matching hashes return `complete`/`verified`, with `action_ready=false` in every case. When `threadforge mcp serve --project-root <root>` is used, the server hashes the graph's recorded source files locally and ignores caller-supplied fingerprints. Without a configured project root, generate hashes locally with `threadforge procedure fingerprint --root <project-root> --path <relative-path> ...` and pass `path_fingerprints`. Caller-supplied digests are assertions, not independent acceptance evidence. See the [procedure list schema](../schema/procedure-list-v1.schema.json).

## Project memory index and exact reads

`threadforge_project_memory_index` provides a content-free index for one explicit `project_id`. Optional `kind` filters `fact`, `decision`, or `policy`; `limit` defaults to 20 (maximum 100), and `offset` defaults to 0. The `threadforge.project-memory-index.v1` response is project-scoped, sorted by kind and ID, and includes revision, title, applicability, content digest, and a state of `approved`, `superseded`, or `conflicting`. It never includes record content or evidence details and does not check source freshness; its `gaps` directs callers to use the v2 exact read. A conflict anywhere in the project is reported as `status: conflicting`, even when the current page contains only non-conflicting entries.

`threadforge_project_memory_read` remains the legacy v1 exact read. It checks project/scope/conflict/integrity but does not verify current source files; its fixed `freshness: not_tracked_by_project_memory_v1` is not a freshness guarantee.

Use `threadforge_project_memory_read_v2` for freshness-aware exact reads. Project-memory v2 evidence records bind each evidence entry to 1-64 repository-relative source files; `memory publish` recomputes the SHA-256 values before writing. On each v2 read, Threadforge hashes only those local files (maximum 64 files, 4 MiB per file, 8 MiB total; credentials and symlinks are refused). Matching files return `status: complete`, `freshness: verified`; changed files return `status: stale` with no record content; unavailable/unsafe sources return `status: incomplete` with no content. Legacy v1 or source-unbound records are `freshness: unverified`, `status: incomplete`, and explicitly labeled; the record may be returned as unverified guidance. All results remain advisory and set `action_ready: false`, `guidance_only: true`, and `execution_authorized: false`. See [v2 candidate](../schema/project-memory-candidate-v2.schema.json), [v2 record](../schema/project-memory-v2.schema.json), and [v2 read response](../schema/project-memory-read-v2.schema.json).

The equivalent local CLI is `threadforge memory read --project-root <root> --project-id <id> --kind <fact|decision|policy> --id <record-id> [--path <relative-path> ...] [--module <module-id> ...]`. This is the safe exact-read path; `memory get` is retained for raw local inspection and does not perform freshness or applicability checks.

To create source fingerprints without exposing file contents, run `threadforge memory fingerprint --project-root <root> --path <relative-file> [--path <relative-file> ...]`. The output is a versioned, path-and-digest-only receipt; add those pairs to `evidence[].source_files` before explicit human/privacy approval. See the [v1 index](../schema/project-memory-index-v1.schema.json) and [legacy v1 read](../schema/project-memory-read-v1.schema.json) contracts as well.

For Codex in the ChatGPT desktop app, Codex CLI, and the IDE extension, the TOML output remains available as a manual fallback. Threadforge also has an explicit project-scoped installer for `.codex/config.toml`:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install --host chatgpt-desktop-codex --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp update --host codex-ide --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp uninstall --host codex-cli --project-root <project-root>
```

The `chatgpt-desktop-codex`, `codex-cli`, and `codex-ide` IDs manage the same hash-owned `[mcp_servers.threadforge]` block in `.codex/config.toml`; they preserve unrelated TOML, refuse unowned name collisions or altered owned blocks, and do not set project trust or enable any hook. OpenAI Docs describes these local Codex clients as sharing MCP configuration ([MCP setup](https://learn.chatgpt.com/docs/extend/mcp)). Codex project config remains trust-gated by the host; trust projects only through Codex's own UI/CLI when appropriate. On October 11, 2026, Codex CLI `0.162.1` loaded the generated project table in an isolated smoke and completed one scripted read through a loopback fixture provider; the smoke trusted only its disposable workspace under temporary `CODEX_HOME` and used `--sandbox read-only`. This proves project config loading and MCP dispatch/result transport, not real-model retrieval or task success. ChatGPT desktop/IDE project loading and live host task use remain unverified.

## Command Code project MCP

The `command-code` host ID manages the project `threadforge` entry in `.mcp.json`, using the same standard JSON config manager as Claude Code. The installer preserves unrelated entries and does not change trust or permissions:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install --host command-code --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp update --host command-code --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp uninstall --host command-code --project-root <project-root>
```

On October 12, 2026, Command Code CLI `1.79.2` listed the project server from an isolated `.mcp.json` with `cmdc mcp list`. This establishes config discovery/listing only; an actual Command Code agent-run MCP call has not been exercised. See the [Command Code integration notes](command-code.md).

## GitHub Copilot CLI project MCP

The `github-copilot` host ID manages Threadforge in workspace `.mcp.json`, a configuration source recognized by Copilot CLI. This deliberately shares the standard JSON manager used by Claude Code and Command Code; installing the same server for multiple of these hosts is idempotent. Unrelated MCP servers and repository instructions are preserved, and Threadforge does not change Copilot permissions, authentication, or trust:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install --host github-copilot --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp update --host github-copilot --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp uninstall --host github-copilot --project-root <project-root>
```

Copilot CLI `mcp list` can inspect the project registration without model inference. Installed-host smoke v26 additionally makes one explicit task-resume call through the project server, using a deterministic loopback provider and restricting the model-visible tools to `threadforge-threadforge_task_resume_context`. This proves selected-version transport only; real-model interpretation, task outcomes, and permission behavior beyond the tool-availability filter remain unverified. See [Copilot project skills](copilot-skills.md) for the separate approved-skill path.

## Explicit task resume context

`threadforge_task_discover` provides explicit project-scoped discovery for MCP-only harnesses. Call it with required `project_id` and optional `max_entries` (default `12`, range `1-100`). Only `in_progress` tasks and `open` tasks with an active session are candidates; blocked and terminal tasks are excluded. Exactly one candidate returns its `threadforge.task-resume-context.v1` snapshot. Zero candidates return `no_active_task`; multiple candidates return `task_selection_required` and no task context or candidate details. If one task has multiple active sessions, context returns `session_selection_required` and withholds session details—except for a structured `plan_only` task, whose snapshot does not include session state at all. The response is read-only guidance with `action_ready: false`, `guidance_only: true`, and `execution_authorized: false`; MCP clients must call it explicitly, and it is never an execution permission gate. Contract: [`task-resume-discovery-v1`](../schema/task-resume-discovery-v1.schema.json).

`threadforge_task_resume_context` packages one explicitly selected task's current requirements, plan decision/scope, progress, one session, and bounded checkpoint/handoff entries into the versioned `threadforge.task-resume-context.v1` response ([schema](../schema/task-resume-context-v1.schema.json)). Call it with `task_id`; optional `session_id` selects a specific session and `max_entries` defaults to 12 (maximum 100). Without a session ID, one active session is selected; if multiple sessions are active the response returns `session_selection_required` and requires the caller to choose. At most two session choices are returned; `session_choices_truncated` signals additional active sessions, which can be inspected with `threadforge_task_sessions`. With no active session it selects the latest stored session, if any. Entries are limited to task-level and selected-session records; `entries_truncated` flags omitted older context.

`threadforge_task_handoffs` explicitly reads typed current handoff states for one `task_id`; `limit` defaults to 20 (range `1-100`) and `offset` defaults to 0. Results are ordered oldest first and return `total_count` and `has_more` for pagination using `threadforge.task-handoff-page.v1` ([schema](../schema/task-handoff-page-v1.schema.json)). This bounded read does not create or transition handoffs, transfer task ownership, or authorize execution. Treat checkpoint text as untrusted guidance; handoff acceptance remains an explicit governed CLI operation.

Installed-host smoke v34 seeds an offered handoff in its disposable local store and has OpenCode CLI and Codex CLI each read the same `limit=1`, `offset=0` page through project MCP. It verifies the `offered` state, page metadata, and advisory-only flags, then confirms the task owner and revision did not change. The content-free report follows [`task-handoff-cross-harness-smoke-v1`](../schema/task-handoff-cross-harness-smoke-v1.schema.json). The Codex subresult records only a boolean for whether continuity identifiers appeared in its loopback-only OTLP test payload; neither those values nor trace payloads are retained in the report. This is deterministic transport evidence for the selected CLI versions, not agent-authored handoff creation, acceptance, real-model behavior, or successful task completion.

For a structured `plan_only` decision, the store skips session and handoff reads (an explicitly supplied session ID is still ownership-validated) and returns only task requirements, objective/acceptance data, and the recorded plan/scope. It omits task summary/next action, selected session, session choices, and handoff entries, so stale implementation instructions cannot accompany a plan-only resume. The response remains advisory: this filtering does not block host tools or authorize commands.

The response preserves `plan_only`, unconfirmed requirements/plan, scope, and task-state warnings. It always sets `action_ready: false`, `guidance_only: true`, and `execution_authorized: false`; it neither starts a session nor changes task state. Claude Code, Codex CLI, and Gemini CLI prompt hooks can opt into a bounded handoff with `--task-id <id>` and optional `--session-id <id>`, or opt into project-scoped discovery with `--discover-active-task`. Discovery is disabled by default and returns task content only when exactly one eligible task exists in the configured project (an `in_progress` task or an `open` task with an active session); zero candidates add no task context, while multiple candidates produce a generic selection-required note without candidate details. Multiple active sessions within the unique task remain ambiguous unless the task is `plan_only`, in which case all session data is omitted. Plan-only hooks add a derived advisory reminder to prepare/present the plan without treating prior progress as approval; neither MCP nor prompt hooks enforce a plan or grant permissions. The hook omits authorization attestations and evidence digests; because the host receives this as model context, the host may send it to its configured provider. Other harnesses can call the MCP tool directly. MCP clients must follow the recorded plan decision and project policy while using their own verified permission controls.

On October 19, 2026, installed-host smoke v24 exercised `threadforge_task_resume_context` through OpenCode CLI `1.18.31` using a synthetic task in a disposable local database. It verified the MCP result reached the host's local provider and retained the draft-requirements warning and non-authorizing fields. This is deterministic transport evidence only: the provider scripted the call, no real model ran, and it does not demonstrate real task success.

Smoke v24 also exercised an explicit `threadforge_task_resume_context` read through Antigravity CLI `1.4.2`. Because its direct always-loaded tool set remains limited to other read-only project tools, this check uses Antigravity's generic `call_mcp_tool` wrapper and explicitly supplies `task_id`; it verifies the returned draft-requirements warning and non-authorizing flags using a loopback provider. This does not enable automatic task injection or prove real-model behavior, task success, or permission enforcement.

On October 19, 2026, installed-host smoke v25 also exercised the same explicit task-resume read through Codex CLI `0.163.0`. Codex's project MCP server returned the synthetic draft task's objective and non-authorizing status to a deterministic loopback provider. The same run observed Codex OTLP delivery at the local collector; prompt text and the fixture key were absent, but approved guidance text may appear in local traces. The result proves one selected-version transport path only, with zero real model calls; it does not demonstrate model-led retrieval, task success, or execution authorization.

Smoke v26 exercises that explicit read through GitHub Copilot CLI `1.0.64` as well. The disposable workspace exposes exactly one model-visible tool, the read-only task-resume MCP tool, and the local fixture verifies the objective plus advisory-only result fields. This remains scripted transport evidence with zero real model calls; it does not prove real-model retrieval quality or task success.

On October 19, 2026, smoke v27 verifies Claude Code `2.1.303` can receive the same selected task through its project `UserPromptSubmit` memory hook using explicit `--task-id` configuration. A direct hook preflight and the installed CLI's loopback Messages request both contain the task objective, `requirements_not_finalized` warning text, and the advisory/no-authorization statement, together with the approved project fact. The hook remains guidance only; the provider is deterministic, no real model ran, and explicit temporary settings do not prove ordinary trust/settings discovery or host permission behavior.

## TypeScript project-config installer primitive

`@threadforge/adapter-contracts/mcp-config` provides project-scoped JSON merge operations for Claude Code and Command Code (`.mcp.json`), Cursor (`.cursor/mcp.json`), Antigravity (`.agents/mcp_config.json`), and Gemini CLI (`.gemini/settings.json`). Qwen Code's `mcpServers` map lives in `.qwen/settings.json` and is managed through the Qwen settings lock shared with hook and memory updates. Codex CLI has a separate hash-owned TOML block manager for `.codex/config.toml`; OpenCode has a separate manager for its local MCP server schema in project `opencode.json`. Installed OpenCode and Codex CLIs have fixture-backed checks for one read-only procedure call. Antigravity CLI `1.3.5` also completed one isolated headless `threadforge_procedure_get` call under smoke v16 and returned the fixture guidance; its project entry was used even though `agy mcp list` reports no configured servers. The run used a local-only provider and zero real-model calls. Command Code `1.79.2` passed project config/skill discovery, but smoke v17's isolated headless run was skipped after `Not authenticated`; its local model fixture received no requests. These checks establish selected config/host dispatch transport only; model-driven context use, real task outcomes, and the other hosts' live contracts remain outstanding.

The adapter CLI can manage a Threadforge MCP server in each supported project config. It requires `mcp serve --project-root <same-root>` arguments and adds `--threadforge-managed-mcp=v1`; the Go CLI accepts this reserved no-op ownership marker. Gemini entries explicitly set `trust: false`; the installer never changes Gemini's project trust state. Install is idempotent and refuses to overwrite an existing unowned entry. Update and uninstall operate only on entries carrying the marker. The underlying APIs also support compare-and-swap changes for callers that already have the expected prior value. Writes use a same-directory temporary file and rename; unrelated JSON keys are retained. The module accepts strict JSON only: comments/JSONC, malformed or oversized documents, symbolic-link config paths, and invalid `mcpServers` values are rejected without rewriting the file. Server entries intentionally do not accept `env`; callers must still avoid putting credentials directly in the command or arguments.

For example, after obtaining an absolute Threadforge executable path and project root, invoke `npm.cmd --prefix adapters run adapter -- mcp install --host cursor --project-root <root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <root>` in Windows PowerShell, or replace `npm.cmd` with `npm` in a POSIX shell. Replace `cursor` with `claude-code`, `antigravity`, or `gemini-cli` as needed. Use `mcp update` with the complete new argument list, or `mcp uninstall` to remove only the marked entry. The CLI does not start the host, trust the project, or claim live injection compatibility.

The adapter tests exercise path selection, preservation of unrelated servers/settings, idempotency, conflict refusal, Gemini trust=false behavior, Qwen settings-lock sharing, compare-and-swap update/removal, malformed-file refusal, and symbolic-link refusal. Codex TOML tests additionally cover line-ending/byte restoration, existing sibling servers, inline-table and same-name conflicts, ownership hashes, and project-root binding. They do not launch Qwen or most other host applications; the narrow Codex `mcp list` smoke validates the user-config table parser, not project trust or live retrieval. JSON installers preserve data, not original whitespace or key ordering when they write a changed file; Codex's managed TOML block preserves surrounding bytes.

## OpenCode project MCP

OpenCode uses a different shape: `opencode.json` has a top-level `mcp` map; local servers use `type: "local"`, an argv `command` array, and `enabled: true`. Install a project-scoped Threadforge server with:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install --host opencode --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp uninstall --host opencode --project-root <project-root>
```

The installer adds the reserved Threadforge ownership argument, verifies that the embedded project root matches the selected directory, preserves unrelated config, and refuses collisions or user-edited managed entries. It writes strict `opencode.json` only; if `opencode.jsonc` exists, it refuses rather than rewriting comments or risking merge/precedence ambiguity. No environment credentials or automatic permissions are added. Use an absolute executable path, and review the resulting project file before committing machine-specific paths. On October 11, 2026, the no-model installed-host smoke used OpenCode CLI `1.18.31` in an isolated temporary home, confirmed `opencode mcp list --pure` reported the local Threadforge server connected, and verified one scripted `threadforge_procedure_get` call returned its approved fixture procedure. This proves installed-host tool dispatch/result transport with a deterministic local provider, not model-driven retrieval, memory interpretation, or real-task success.

```json
{
  "mcp": {
    "threadforge": {
      "type": "local",
      "command": ["<threadforge-executable>", "mcp", "serve", "--project-root", "<project-root>", "--threadforge-managed-mcp=v1"],
      "enabled": true
    }
  }
}
```

## Qwen Code project MCP

Qwen Code uses the project `.qwen/settings.json` file for MCP server entries. Install a Threadforge server explicitly for one project:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install --host qwen-code --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp update --host qwen-code --project-root <project-root> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <project-root>
npm.cmd --prefix adapters run adapter -- mcp uninstall --host qwen-code --project-root <project-root>
```

The manager changes only `mcpServers.threadforge`, stores only `command` and bounded `args`, and requires the reserved Threadforge marker plus the same canonical project root. It rejects environment, `cwd`, trust fields, inline credential-like values, unowned collisions, malformed settings, symlink paths, and entries whose ownership marker has been removed. Operations share `.threadforge-qwen-hooks.lock` with Qwen hook and project-memory settings updates to prevent lost updates; no trust or permission setting is written. The Qwen executable is unavailable in this environment, so live project-config loading, tool discovery, and memory retrieval remain unverified.

## Claude Code command hooks

`@threadforge/adapter-contracts/claude-hook` accepts Claude Code command-hook JSON on stdin and maps supported lifecycle events to the local `/v1/events` intake. By default, prompt and tool payloads are not forwarded: only a bounded SHA-256 digest and byte count are retained, and digests are omitted for values larger than 64 KiB. Session and call identifiers are hashed before logging; tool/agent labels are bounded and credential-like labels are omitted. Working directories, transcript paths, raw prompts, tool arguments, and tool outputs are not retained. Permission requests remain requests, and the mapper does not invent approval decisions or process exit codes. Capture errors are fail-open so this observational hook cannot block or authorize an agent action.

`@threadforge/adapter-contracts/claude-hooks-config` merges the supported event groups into project `.claude/settings.json`, retaining unrelated settings, hook events, hook types, and user hook references. Only command hook refs whose command includes `--threadforge-hook=claude` are treated as Threadforge-owned for replacement/removal. Supply the full command string for the installed adapter, including that marker. The config API requires strict JSON and refuses malformed/oversized settings or symbolic-link settings directories. Its lock file is removed after normal completion; after a forced process termination, inspect and remove a stale `.threadforge-settings.lock` manually before retrying.

Both modules have local unit/loopback tests, but no Claude Code binary is launched in the test suite. This is therefore a tested adapter implementation, not a claim of current live-host contract verification or full Claude Code telemetry parity. Beacon documents additional paths not implemented here, including OTLP metric/log configuration, historical transcript sync, cloud-agent forwarding, packaging, and deployment modes.

## Antigravity CLI observational hooks

`@threadforge/adapter-contracts/antigravity-hook` maps the documented Antigravity hook payloads into metadata-only events. The corresponding config manager supports user-level `~/.gemini/config/hooks.json` (default) and project-level `.agents/hooks.json`. It installs only an owned `threadforge-endpoint` block and preserves unrelated top-level configuration and hook refs:

```powershell
npm.cmd --prefix adapters run adapter -- antigravity install --command '<adapter-command> --threadforge-hook=antigravity'
npm.cmd --prefix adapters run adapter -- antigravity install --scope project --project-root <absolute-project-path> --command '<adapter-command> --threadforge-hook=antigravity'
npm.cmd --prefix adapters run adapter -- antigravity uninstall [--scope user|project] [--project-root <absolute-project-path>]
```

The observational manager appends event selectors to its configured hook commands; prompts, tool args/results, paths, diffs, and error text are omitted, and bounded digests are retained locally. No host decision is emitted. A separate opt-in `antigravity memory install|uninstall --scope project` manages a `PreInvocation` hook that injects approved project memory as a transient system message on invocation 0 only. Because that event has no prompt, retrieval is bounded project-wide context and requires exactly one workspace path; no transcript is read. Both hook paths remain unverified in a live Antigravity host. See [Antigravity CLI integration](antigravity-cli.md) for configuration, privacy, and verification limits.

## Cursor observational hooks

`@threadforge/adapter-contracts/cursor-hook` handles project hook events for `sessionStart`, `postToolUse`, `postToolUseFailure`, `afterFileEdit`, and `sessionEnd`. It stores only bounded content hashes/byte counts and hashed conversation identifiers. File paths, diffs, commands, tool input, output, and error text are not retained. The telemetry config manager uses `.cursor/hooks.json` version 1 and installs only those after/observation events; it never installs policy hooks or returns an allow/deny response. Separately, the opt-in `cursor plan-gate` manager can install a fail-closed `preToolUse` hook matched only to direct `Write` tools. It returns deny or no opinion, never allow, and does not gate shell/MCP or close path races. See [Cursor task plan gate](cursor-plan-gate.md).

```powershell
npm.cmd --prefix adapters run adapter -- cursor install --project-root <absolute-project-path> --command 'node --experimental-strip-types "<absolute-path-to-cursor-hook.ts>" --threadforge-hook=cursor'
npm.cmd --prefix adapters run adapter -- cursor uninstall --project-root <absolute-project-path>
```

The installer updates/removes only commands carrying the ownership marker and preserves unrelated hook objects. It refuses comments/JSONC, malformed or unsupported config versions, and symbolic-link paths; after a forced termination, inspect and remove `.threadforge-cursor-hooks.lock` before retrying. Adapter and config fixtures are tested locally, but the Cursor application is not launched in tests. Cursor Cloud hooks, prompt/reasoning capture, approval decisions, local session-store backfill, and live-host contract validation remain out of scope.

## OpenCode plugin event adapter

`@threadforge/adapter-contracts/opencode-plugin` exports `ThreadforgeOpenCodePlugin` and a testable `createOpenCodeAdapter` factory. The adapter maps chat, tool, command, session, permission, and diff-count lifecycle events into the same local event intake. It maps `message.updated` only when `info.role` is `assistant` and `info.time.completed` is a finite positive timestamp; the event contains no response text or raw message ID. Duplicate completions are suppressed using a SHA-256 identity held only in a bounded 2,048-entry in-memory cache. Content is represented only by bounded metadata digests; identifiers are hashed; credential-like labels are dropped. Delivery is queued with a fixed capacity, non-blocking for host callbacks, and fail-open when local intake is unavailable. Queue overflow drops the newest telemetry event rather than blocking the agent. `THREADFORGE_EVENT_ENDPOINT` can select a custom endpoint only when the shared emitter validates it as loopback.

The adapter CLI can create/update/remove only the owned `.opencode/plugins/threadforge.ts` file while leaving sibling plugins untouched:

```powershell
npm.cmd --prefix adapters run adapter -- opencode install --project-root <absolute-project-path> --module <Threadforge-package-export-or-file-url>
npm.cmd --prefix adapters run adapter -- opencode uninstall --project-root <absolute-project-path>
```

OpenCode CLI `1.18.31` has now passed an isolated installed-host smoke that observes project plugin callbacks and confirms loopback receipt of `assistant.message.completed`, `prompt.submitted`, and tool lifecycle events while performing one deterministic MCP read. The fixture retained no prompt text and made zero real-model calls; this verifies callback delivery for this pinned version, not model behavior or task success. The poller uses OpenCode's CLI export rather than direct database reads; complete older/cross-project history, a distributable package, broader CLI/plugin-version compatibility, duplicate reconciliation between history and live events, and real-task end-to-end verification remain release blockers. A local `file:` module URL is machine-specific and should not be committed as a portable project integration. See [OpenCode session history](opencode-history.md) and [installed smoke evidence](installed-skill-smoke.md).

## Kimi Code hook adapter

Threadforge manages user-scope Kimi Code `[[hooks]]` in `KIMI_CODE_HOME/config.toml` through the `kimi install|update|uninstall` adapter CLI. It appends only valid hook fields, identifies its tables through the command marker, and does not reserialize provider/model configuration. The mapper is metadata-only and writes no stdout, so prompt content is not returned to Kimi as context. Kimi Code has blockable hook phases: Threadforge emits no deny response or policy action; normal host permissions remain authoritative. The host binary is unavailable in this environment, so live event/exit-code compatibility is unverified. See [Kimi Code hooks](kimi-code.md).

## Kiro hooks

`@threadforge/adapter-contracts/kiro-hook` maps Kiro v1 events to privacy-minimized local events. `kiro install|update|uninstall` manages a dedicated `threadforge-observational.json` under `<KIRO_HOME>/hooks/` or `<project>/.kiro/hooks/`; it preserves other hook objects and never edits sibling JSON files. It registers lifecycle, prompt, tool, and stop events with bounded timeouts. Prompt, tool, and response content are digested only; stdout remains empty to avoid adding text to the model context. See [Kiro hooks](kiro.md) for scope, safety details, and unverified host behavior.

## Muse Code hooks

`@threadforge/adapter-contracts/muse-hook` maps Muse Code's lifecycle, prompt, tool, permission-request, compaction, subagent, notification, and stop hooks. `muse install|update|uninstall` requires an explicit user/project scope. User scope updates `settings.json` only when it declares `schema_version: 1`; project scope updates `.muse/hooks.json` and does not change project trust. Neither path claims or changes `managed_hooks_path`. Prompt/tool/response bodies are digested locally, pre/post model-call hooks are omitted, and the command emits only an empty no-decision object. Muse hook commands run outside the agent sandbox, so review them before installation. Current handler/payload/response compatibility is not live-verified; see [Muse Code hooks](muse-code.md).

## Oh My Pi extension

`@threadforge/adapter-contracts/omp-extension` is a self-contained observational extension for lifecycle, prompt, tool, approval, operator Bash/Python, and token-usage events. `omp install|update|uninstall` requires `--scope user|project`; user scope follows OMP's profile/config-directory rules or accepts `--omp-agent-dir`, while project scope manages only `.omp/extensions/threadforge.ts`. The file manager preserves sibling extensions and refuses unowned or edited targets. Content is digested locally and asynchronously sent only to loopback; callbacks return no blocking, transformed, or injected result. OMP is unavailable here, so live extension API/version and end-to-end compatibility remain unverified. Project extensions are executable code and may load automatically; install only in a trusted project. See [Oh My Pi adapter](oh-my-pi.md).

## OpenClaw Gateway plugin

The source package is `adapters/openclaw-plugin/` (exported as `@threadforge/adapter-contracts/openclaw-plugin`). `openclaw install|update|uninstall --scope active-gateway` delegates registration and lifecycle to the matching OpenClaw CLI profile; `--print` previews commands, and `--acknowledge-local-source` explicitly forwards OpenClaw's non-interactive local-install acknowledgement. `openclaw otel print-config|status|validate` prints a JSON5 fragment without editing config and checks recent local OTLP intake. Threadforge does not rewrite OpenClaw's JSON5 config. The plugin registers ten observational hooks, hashes content and identity locally, sends only to loopback, returns synchronously without decisions, and bounds its queue at 128. OpenClaw is unavailable here, so live plugin/export behavior and current session-history import remain unverified/incomplete.

This is the portable MCP fallback when host-specific installation is unavailable. Threadforge has project MCP config managers for Claude Code, Command Code, Cursor, Antigravity, Gemini CLI, Codex in ChatGPT desktop/CLI/IDE, and OpenCode. Claude Code and Codex CLI have opt-in local `UserPromptSubmit` memory hooks; Gemini CLI has a project `BeforeAgent` memory hook; Qwen Code has a project-only `UserPromptExpansion` hook that uses the current model-bound prompt only as a transient retrieval query. Cursor has a separate opt-in `sessionStart` bootstrap; task-resume context is included only when the configured command opts in with `--task-id`/`--session-id` or `--discover-active-task`. Antigravity has a project-only `PreInvocation` bootstrap that injects bounded project-wide approved memory and similarly opts into task-resume context only when explicitly configured. Use the MCP tools in this document for task-specific context when hooks are not configured for it. Installed smoke v28 verifies one configured Gemini CLI `BeforeAgent` hook invocation returns explicit task-resume context during a fake-response turn; this does not prove model consumption, normal trust discovery, or efficacy. The installed OpenCode, Codex, and Antigravity smoke checks each exercise one deterministic procedure read, but do not prove real-model selection, host instruction adherence, or cross-harness task success. Command Code CLI `1.79.2` lists its project MCP entry, but an agent-run call remains unverified. Antigravity CLI `1.3.5` dispatched and returned the fixture result in v16 even though its `mcp list` view is empty; real-model behavior, normal trust/settings discovery, hook activation, and efficacy remain unverified. Other hosts' live host/version and trust behavior also remain outstanding. Gemini CLI and Codex CLI have separate user-level telemetry settings, while Claude Code, Cursor, Codex, Antigravity, Qwen, Kimi Code, Kiro, Muse, Oh My Pi, and OpenClaw have partial telemetry-hook/extension slices. Other harnesses do not claim automatic context injection until their versioned host contracts and preservation tests are recorded in [the source matrix](source-matrix.md).
