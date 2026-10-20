# Goose OTLP guidance

Threadforge supports partial, read-only Goose OTLP configuration and validation plus a separate local session-history poller. It complements the existing local OTLP/HTTP receiver; it does not write Goose configuration. See [Goose local session history](goose-history.md).

## Print local configuration guidance

```powershell
npm.cmd run adapter -- goose otel print-config
```

The command prints only `otel_exporter_otlp_endpoint` and `otel_exporter_otlp_timeout` for the local collector, defaulting to `http://127.0.0.1:4318` and 10,000 milliseconds. A custom endpoint must be a loopback HTTP base URL. Merge the snippet into the existing `~/.config/goose/config.yaml`; Threadforge never edits that file and does not enable message-content capture.

## Read-only status and validation

```powershell
npm.cmd run adapter -- goose otel status [--config-path <config.yaml>] [--log-path <events.jsonl>] [--json]
npm.cmd run adapter -- goose otel validate [--since 10m] [--config-path <config.yaml>] [--log-path <events.jsonl>] [--json]
```

Status inspects only the two top-level OTLP YAML scalars, with a 2 MiB file cap. Duplicate/unsupported values, remote endpoints, unreadable files, and symlinks are reported without rewriting configuration. The default config path is `~/.config/goose/config.yaml`; the default event log is Threadforge's existing local event log. Event inspection is bounded to the latest 16 MiB and 64 KiB per JSONL line and counts only records with `harness.name=goose` and `collection_method=otlp`.

The output never returns config or log paths, endpoint values, environment values, provider names, or event bodies. It reports whether `OTEL_EXPORTER_OTLP_ENDPOINT` differs from the config value, whether `OTEL_SDK_DISABLED` disables telemetry, and whether `OTEL_EXPORTER_OTLP_PROTOCOL` selects gRPC. These variables are inspected only in the Threadforge process environment; the Goose app may launch with a different environment. Validation requires a loopback HTTP config, no detected environment conflict, a complete local log scan, and a recent Goose OTLP event. It is evidence of local observation, not proof of complete Goose telemetry coverage.

## Deliberate hook boundary

This adapter does not generate or install Goose hook commands. Beacon's local contract describes blocking `PreToolUse` and `Stop` hooks that require a policy response; returning `allow` would be a behavior-affecting response, while an empty response may be treated as hook failure. Without pinned, installed-host verification, Threadforge leaves this path unimplemented rather than introducing a possible execution-authorization or blocking behavior. No approval decision is inferred from Goose tool hooks. Agent memory remains available through the existing MCP server and portable approved procedure files.

Goose hook mapping and installation, live session-database/version validation, cross-harness retrieval, and installed-host OTLP validation remain release-gate gaps. Do not interpret these partial integrations as Goose parity.
