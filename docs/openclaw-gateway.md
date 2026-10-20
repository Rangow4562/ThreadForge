# OpenClaw Gateway plugin adapter

Threadforge includes a local OpenClaw plugin package at `adapters/openclaw-plugin/`, a separate native OpenTelemetry setup/status path, and a bounded Gateway-history poller. These cover live agent-hook events, gateway-level OTLP signals, and visible user/assistant history. Full historical parity and live-host acceptance remain incomplete.

## Install through OpenClaw

Threadforge wraps OpenClaw's own plugin CLI, so OpenClaw remains the owner of plugin records and JSON5 config. Select the active Gateway profile explicitly; the environment/CLI profile used by this command must match the Gateway service:

```powershell
npm.cmd --prefix adapters run adapter -- openclaw install --scope active-gateway
npm.cmd --prefix adapters run adapter -- openclaw update --scope active-gateway
npm.cmd --prefix adapters run adapter -- openclaw uninstall --scope active-gateway
```

`--print` previews the exact OpenClaw CLI commands without running them. For a non-interactive install that requires OpenClaw's `--force` acknowledgement, explicitly add `--acknowledge-local-source` after reviewing the package. `--openclaw-cli <command>` selects a non-default CLI executable. The package stays in the Threadforge checkout because installation uses `--link`; update reloads the linked extension and uninstall asks OpenClaw to keep the source files. On Windows, the adapter invokes the CLI through PowerShell using a base64-encoded argument script rather than interpolating a shell command.

Restart/reload the Gateway after install. If your config has a plugin allowlist, let OpenClaw's CLI handle it; do not add or replace an allowlist manually just for Threadforge. The plugin uses no tools, gateway methods, commands, or network destinations other than loopback telemetry.

OpenClaw gates conversation hooks for non-bundled plugins. If you want prompt and assistant-output event counts/digests, explicitly enable conversation access for this plugin in your OpenClaw config after reviewing that consequence. Threadforge does not edit the JSON5 config. If conversation access stays disabled, the host may withhold `message_received` and `llm_output`; other registered hooks can still provide lifecycle/tool/compaction/subagent events.

## Native Gateway OpenTelemetry

Print a merge-safe config fragment and check whether OTLP events have arrived:

```powershell
npm.cmd --prefix adapters run adapter -- openclaw otel print-config
npm.cmd --prefix adapters run adapter -- openclaw otel status --json
npm.cmd --prefix adapters run adapter -- openclaw otel validate --since 10m --json
```

`print-config` installs nothing and never edits `openclaw.json`. It enables the `diagnostics-otel` exporter with `http/protobuf`, service name `openclaw-gateway`, traces, metrics, and logs, sampling rate 1.0, and a 60-second flush interval. It pins each signal endpoint to the local Threadforge receiver and sets `captureContent: false`. Merge the fragment into your existing JSON5; it intentionally does not emit a replacement plugin allowlist. Logs are enabled to provide the documented full signal path; set `logs: false` manually if operational logs are not wanted.

The default endpoint is `http://127.0.0.1:4318`. A custom remote endpoint is refused unless `--allow-remote` is explicit and the URL uses HTTPS. `--capture-content` is a separate explicit opt-in; OpenClaw may then export bounded/redacted prompt, response, tool input/output, and tool-definition content, while system-prompt capture remains excluded by OpenClaw. Review current [OpenClaw OpenTelemetry settings](https://docs.openclaw.ai/gateway/opentelemetry) before enabling content or changing endpoints.

`status` scans only the bounded local Threadforge JSONL tail (16 MiB maximum) and reports aggregate counts and the latest timestamp, never bodies, identities, or the source path. It resolves the same default trace path as the core: `THREADFORGE_TRACE_FILE`, then `THREADFORGE_HOME/traces/events.jsonl`, then the platform cache directory. `validate` exits nonzero unless an OpenClaw OTLP event is newer than `--since` (default `10m`). Malformed rows and omitted older tails are reported as incomplete.

## Session-history polling

Threadforge delegates session discovery and transcript reads to OpenClaw's supported CLI and Gateway RPC. It does not inspect OpenClaw's SQLite files or copy its credentials:

```powershell
npm.cmd --prefix adapters run adapter -- openclaw sessions status
npm.cmd --prefix adapters run adapter -- openclaw sessions sync --print
npm.cmd --prefix adapters run adapter -- openclaw sessions sync
npm.cmd --prefix adapters run adapter -- openclaw sessions sync --max-sessions 100 --max-messages 1000
```

The defaults are 50 sessions and 200 messages per session; each `chat.history` page is capped at 200 messages. `--max-sessions` accepts 1–200 and `--max-messages` accepts 1–2000. `--print` previews events without sending or writing cursors. Normal sync emits only to Threadforge's loopback `/v1/events` endpoint and stores a private cursor under Threadforge state (or `--state-path`). The cursor contains hashes, not session keys, message IDs, or transcript text. Multi-agent calls include the `agentId` returned by OpenClaw's session index.

History is metadata-only by default: user/assistant role, timestamp, UTF-8 byte count, and a content digest are retained, while reasoning/tool blocks are omitted. `--include-content` is an explicit opt-in for bounded text capture (up to 4 KiB per message); oversized messages remain metadata-only. This flag cannot be combined with `--print`.

The poller follows Gateway `hasMore`/`nextOffset` pages within the configured message limit and a 32 MiB per-session response budget. A visible message without a stable identifier is rejected for that session. If the session index or history still reports more data at the bound, output is marked `complete: false` and the command exits nonzero. A missing saved message anchor is reported as a cursor conflict and is not silently replayed or advanced. This bounded importer does not yet prove complete export of every historical branch or source format; it is not cutover evidence. If a sync is incomplete, preserve its JSON report and resolve the limit/conflict before relying on the imported history.

## Events, privacy, and behavior

The package registers these ten hooks: `session_start`, `session_end`, `message_received`, `before_tool_call`, `after_tool_call`, `llm_output`, `before_compaction`, `after_compaction`, `subagent_spawned`, and `subagent_ended`. It deliberately does not register prompt-injection, message-claim, install-gate, approval-voting, or synchronous persistence hooks.

Prompt, assistant output, tool input/output, and patch data are digested in-process (maximum 64 KiB input per digest). Session, conversation, run, sender, child-session, tool-call, and file-path identities are hashed. The plugin does not store raw chat text, tool arguments/results, command output, paths, reasoning, or error messages. It reports only an explicit tool error as a failure; an in-progress `exec` is reported as `command.started`, not as a successful command. OpenClaw's model-backed `approvalReviewOutcome` is not treated as human approval, and the plugin does not collect approval decisions.

All callbacks return synchronously with `undefined`, including `before_tool_call`, so Threadforge does not hold a policy or authorization role and its delivery latency cannot delay tool execution. Events queue in-process up to 128; excess events are dropped, and loopback delivery has a 250 ms timeout. There is no remote forwarding or memory/context injection.

The local OpenClaw executable is unavailable in this environment. Unit tests exercise plugin mapping, privacy, hook registration, bounded queuing, command planning, native-config rendering, local status validation, and the fake-CLI history poller; live plugin install/load, actual CLI/RPC payload compatibility, and cross-harness retrieval are unverified. The preserved Beacon guide's JSONL session format is not assumed to match current OpenClaw storage; see the current [OpenClaw session documentation](https://docs.openclaw.ai/concepts/session), [sessions CLI](https://docs.openclaw.ai/cli/sessions), and [Gateway call CLI](https://docs.openclaw.ai/cli/gateway/query).

## References

- [OpenClaw plugin management](https://docs.openclaw.ai/plugins/management)
- [OpenClaw plugin hooks](https://docs.openclaw.ai/plugins/hooks)
- [OpenClaw Gateway OpenTelemetry](https://docs.openclaw.ai/gateway/opentelemetry)
- [OpenClaw Gateway session control](https://docs.openclaw.ai/gateway/protocol/rpc-session-control)
- [Beacon OpenClaw Gateway behavior reference](../../agent-beacon/docs/runtimes/openclaw-gateway.mdx)
