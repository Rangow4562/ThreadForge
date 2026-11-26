# Goose OTLP guidance

Threadforge supports a reversible manager for Goose's two top-level OTLP YAML settings, sanitized read-only status/validation, and a separate local session-history poller. This remains a partial integration: it does not install Goose hooks, and no installed Goose version has yet been exercised here. See [Goose local session history](goose-history.md).

## Manage the local OTLP configuration

```powershell
npm.cmd run adapter -- goose otel install --allow-content-capture
npm.cmd run adapter -- goose otel update --allow-content-capture --timeout-ms 20000
npm.cmd run adapter -- goose otel uninstall
```

`install` and `update` manage only `otel_exporter_otlp_endpoint` and `otel_exporter_otlp_timeout`; the endpoint is restricted to a loopback HTTP base URL (default `http://127.0.0.1:4318`) and timeout defaults to 10,000 ms (range 1–120,000). The manager appends a digest-marked block, preserves existing bytes, BOM and line endings, refuses duplicate or pre-existing unmanaged OTLP keys, and uses a lock plus compare-before-atomic-replace. An edited managed block is never overwritten or removed. `uninstall` removes only the intact Threadforge block; it removes the whole config file only when Threadforge created it and no other content remains. An optional `--config-path` must be absolute.

On Windows the default config path is `%APPDATA%\Block\goose\config\config.yaml`; on macOS/Linux it is `~/.config/goose/config.yaml`. Verify the path used by the installed Goose version before relying on automatic setup.

Goose GenAI spans may contain prompts, outputs, reasoning, and tool metadata. Because its OTLP config has no Threadforge-controlled content filter, `install` and `update` require the explicit `--allow-content-capture` acknowledgement. The generated endpoint is loopback-only, so captured data is directed to the local receiver by default. Goose's runtime environment can override or disable the YAML settings; an environment override can also redirect telemetry, so verify the environment that actually launches Goose. Threadforge does not configure remote forwarding through this manager.

Lifecycle commands emit the path-free `threadforge.goose-otel-config.v1` result described by [`schema/goose-otel-config-v1.schema.json`](../schema/goose-otel-config-v1.schema.json). Install/update results distinguish possible local capture from removal/absence; the schema rejects remote endpoint classes, missing consent, out-of-range timeouts, and path-bearing fields.

## Print configuration guidance

```powershell
npm.cmd run adapter -- goose otel print-config
```

The command prints the same two-key local snippet for manual use and includes the content-capture warning. A custom endpoint must be a loopback HTTP base URL. It does not edit the config.

## Read-only status and validation

```powershell
npm.cmd run adapter -- goose otel status [--config-path <config.yaml>] [--log-path <events.jsonl>] [--json]
npm.cmd run adapter -- goose otel validate [--since 10m] [--config-path <config.yaml>] [--log-path <events.jsonl>] [--json]
```

Status inspects only the two top-level OTLP YAML scalars, with a 2 MiB file cap. Duplicate/unsupported values, remote endpoints, unreadable files, and symlinks are reported without rewriting configuration. The config path follows the Windows or Unix-like default above; the event log defaults to Threadforge's existing local event log. Event inspection is bounded to the latest 16 MiB and 64 KiB per JSONL line and counts only records with `harness.name=goose` and `collection_method=otlp`.

The output never returns config or log paths, endpoint values, environment values, provider names, or event bodies. It reports whether `OTEL_EXPORTER_OTLP_ENDPOINT` differs from the config value, whether `OTEL_SDK_DISABLED` disables telemetry, and whether `OTEL_EXPORTER_OTLP_PROTOCOL` selects gRPC. These variables are inspected only in the Threadforge process environment; the Goose app may launch with a different environment. Validation requires a loopback HTTP config, no detected environment conflict, a complete local log scan, and a recent Goose OTLP event. It is evidence of local observation, not proof of complete Goose telemetry coverage.

## Deliberate hook boundary

This adapter does not generate or install Goose hook commands. Beacon's local contract describes blocking `PreToolUse` and `Stop` hooks that require a policy response; returning `allow` would be a behavior-affecting response, while an empty response may be treated as hook failure. Without pinned, installed-host verification, Threadforge leaves this path unimplemented rather than introducing a possible execution-authorization or blocking behavior. No approval decision is inferred from Goose tool hooks. Agent memory remains available through the existing MCP server and portable approved procedure files.

Goose hook mapping and installation, live Goose config-path/schema compatibility, installed-host OTLP delivery validation, cross-harness retrieval, and actual event-content review remain release-gate gaps. The isolated config tests do not prove Goose loads the managed keys or that the collector receives a live span. Do not interpret these partial integrations as Goose parity.
