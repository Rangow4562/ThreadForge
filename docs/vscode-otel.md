# VS Code Copilot OTLP guidance

Threadforge provides a partial, print-only OpenTelemetry setup and read-only local validation path for GitHub Copilot Chat in VS Code. OTLP events are identified as `harness.name=vscode_copilot`; VS Code preview hook events are a separate harness path and are not installed by these commands.

## Print settings guidance

```powershell
npm.cmd run adapter -- vscode otel print-config
```

The command prints a mergeable JSON settings fragment for the local OTLP/HTTP receiver, defaulting to `http://127.0.0.1:4318`. It enables Copilot Chat OTLP, selects `otlp-http`, sets `captureContent` to `false`, and caps the configured attribute size at 8,192 characters. Custom endpoints must be loopback HTTP base URLs. Threadforge never writes VS Code User or Workspace settings; merge the fragment manually and preserve unrelated settings. Reload VS Code after editing.

## Read-only status and validation

```powershell
npm.cmd run adapter -- vscode otel status [--settings-path <settings.json>] [--log-path <events.jsonl>] [--json]
npm.cmd run adapter -- vscode otel validate [--since 10m] [--settings-path <settings.json>] [--log-path <events.jsonl>] [--json]
```

By default, status reads the current user's VS Code settings path for the current OS. Pass `--settings-path` to inspect an explicit User or Workspace settings file. The reader accepts VS Code JSONC comments and trailing commas, caps the file at 2 MiB, and refuses symlinks. It inspects only the Copilot OTLP settings and does not return paths, endpoint values, unrelated setting values, or event bodies. Local event scanning is bounded to a 16 MiB tail and 64 KiB per JSONL line and counts only `vscode_copilot` OTLP events.

Status reports disabled telemetry, file-exporter conflicts, unsupported exporter types, non-loopback endpoints, and explicit message-content capture. Validation requires enabled local OTLP settings, a complete local event-log scan, and a recent Copilot OTLP event. A recent event does not prove all VS Code agents or managed settings are covered; organization policies and effective runtime settings must still be verified in the installed host.

## Deliberate limits

This is not a VS Code hook installer. VS Code hooks are preview and may be disabled by organization policy; no hook callback or response behavior is enabled here. Threadforge also does not auto-edit managed VS Code settings, perform remote export, or enable content capture. Live host/version verification, installed-setting precedence, packaging, optional hook support, and cross-harness acceptance remain release gates.
