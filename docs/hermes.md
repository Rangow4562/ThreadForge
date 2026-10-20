# Hermes Agent adapter

Threadforge has a partial, observational adapter for Beacon-documented Hermes Agent shell-hook events. The payload/event contract is based on the local Beacon documentation and synthetic fixtures; installed Hermes versions have not been validated here.

## Print a hook configuration fragment

```powershell
npm.cmd run adapter -- hermes hooks print-config
```

The command prints a YAML `hooks:` fragment for these events: `on_session_start`, `pre_llm_call`, `pre_tool_call`, `post_tool_call`, `pre_approval_request`, `post_approval_response`, `subagent_stop`, `on_session_end`, and `on_session_finalize`. Tool/approval hooks use a 10-second timeout, `pre_llm_call` uses 30 seconds, and tool hooks use the `.*` matcher. Supply `--command-template` to print a command appropriate for the installed Threadforge executable; the value must contain exactly one `{event}` slot and one `--threadforge-hermes-hook=v1` ownership marker.

This is a print-only command. It does not read or edit `~/.hermes/config.yaml`, install hooks, set `hooks_auto_accept`, or accept Hermes hook consent. Review and merge the printed fragment yourself, preserving existing settings. The default command is a local Node 22 development launcher and should not be treated as a portable installed command.

## Event and privacy behavior

The mapper emits lifecycle, prompt, tool, approval, and subagent observations to the existing loopback `/v1/events` intake. Session/call identifiers and working paths are hashed; prompt, tool input, command, and output bodies are reduced to SHA-256 digests and byte counts. Tool labels and model labels are bounded, and credential-like labels are omitted. An approval request remains `requested`; an approval outcome is recorded only from an explicit response field. A nonzero command exit status is an outcome, not by itself a tool failure; failure requires an explicit error signal.

The hook runner accepts at most 1 MiB of JSON input, emits no stdout response, uses a 250 ms intake timeout, rejects non-loopback destinations, and fails open on malformed payloads or unavailable intake. Telemetry is evidence only: it cannot approve tools, alter hook behavior, inject memory, or authorize execution. The current command hooks are not verified as asynchronous, so telemetry is observational but may still consume the host's configured hook timeout.

## Local session-history polling

```powershell
npm.cmd run adapter -- hermes sessions status
npm.cmd run adapter -- hermes sessions sync --print
npm.cmd run adapter -- hermes sessions sync
```

The poller opens `~/.hermes/state.db` read-only (override with `--db-path`) and reads only active messages from unarchived sessions. It uses bounded session/message/source-byte limits and a private Threadforge cursor under the local Threadforge state directory (override with `--state-path`). Session identifiers are stored as SHA-256 hashes; event bodies are represented by a SHA-256 digest and byte count by default. `--include-content` explicitly permits bounded message text for normal sync; it cannot be combined with `--print`. Reasoning fields, tool arguments, titles, working paths, and raw Hermes identifiers are not emitted. Polling reads committed history and cannot approve, deny, or delay tools. The source SQLite database is never written by this adapter.

The SQLite fixture contract is based on the Beacon-pinned Hermes collector schema. A synthetic schema/read-only/idempotency test passes, but an installed Hermes state database and current Hermes versions have not been verified; unsupported schema or cursor conflicts are surfaced rather than guessed through.

## Remaining acceptance work

Hermes executable/version and event payload compatibility, config merge behavior, hook-consent flow, installed state-database compatibility, distribution packaging, and cross-harness retrieval are not verified. No installed-host or full parity claim should be made until those gates pass.
