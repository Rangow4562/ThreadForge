# Claude Code telemetry

Threadforge can configure Claude Code's user-level `~/.claude/settings.json` to send OTLP/HTTP JSON logs, metrics, and beta traces to the local service at `http://127.0.0.1:4318`. Start `threadforge serve` before expecting telemetry to be accepted.

```powershell
npm.cmd --prefix adapters run adapter -- telemetry install --host claude-code
npm.cmd --prefix adapters run adapter -- telemetry update --host claude-code
npm.cmd --prefix adapters run adapter -- telemetry uninstall --host claude-code
```

The installer is an explicit opt-in and affects all Claude Code sessions using that user profile. It enables the three OTLP exporters, HTTP/JSON, enhanced beta tracing, and the loopback endpoint. It sets `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_DETAILS`, `OTEL_LOG_TOOL_CONTENT`, and `OTEL_LOG_RAW_API_BODIES` to `0`. It also clears inherited beta/per-signal endpoints, protocols, and OTLP headers so the configured signals use the same local destination without credentials.

Only unset environment keys are added. Existing conflicting values cause a no-change refusal for review; unrelated settings are preserved. The ownership file `~/.claude/.threadforge-telemetry.json` contains only the names of keys Threadforge introduced. Uninstall removes those keys only if their values still match Threadforge's configuration, retaining user edits. The settings and ownership files are size-limited, checked for symlinks, locked during updates, and atomically replaced.

Claude Code ignores the OpenTelemetry exporter variables in repository `.claude/settings.json` and `.claude/settings.local.json`; the installer therefore writes only user settings and does not touch project hook settings or project trust. Organization-managed settings and explicit `--settings` files can take precedence. Restart Claude Code after install and inspect its debug output if no events arrive. Raw telemetry is stored only in Threadforge's local JSONL; Threadforge does not forward it remotely.

Unit/config tests do not launch Claude Code. A separate bounded local transcript poller is available; its observed JSONL format/version compatibility, live runtime, package deployment, Cloud Agents/GCS, and cross-harness acceptance remain pending. See [Claude Code history](claude-history.md).
