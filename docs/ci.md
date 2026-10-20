# CI telemetry

Threadforge currently provides local `ci exec` wrapping, detached `ci start`/`ci finish` sessions for Claude Code and Codex CLI, a checked-in GitHub composite action for those sessions, and `ci validate` for existing Threadforge event JSONL. This remains a partial CI integration, not a replacement for the full Beacon CI workflow.

## Wrap one Claude Code command

```powershell
threadforge ci exec --harness claude-code -- claude -p "Review this change"
```

The child command is launched directly (no shell expansion). Threadforge starts a temporary HTTP receiver bound to `127.0.0.1` on an ephemeral port, sets OTLP/HTTP JSON and the Threadforge event endpoint only in the child environment, then shuts the receiver down after the process exits. It does not edit the runner's host configuration. `ci exec` supports `claude-code` and `codex-cli`.

Prompt, assistant-response, tool-detail, tool-content, and raw-API-body telemetry environment switches are set to `0` for the child. Because live Claude Code contract verification is still pending, treat this as a default configuration request rather than proof that every host version suppresses all content. The child inherits its ordinary environment—including credentials needed to run the agent—but Threadforge does not store those credentials or send artifacts to a remote service.

When `GITHUB_ACTIONS=true`, Threadforge adds an allowlisted subset of workflow/run metadata (provider, run ID/attempt, workflow, job, event, commit, repository, branch, actor, and a valid pull-request ref/number) to OTLP resource attributes; the receiver maps these to typed `run` fields and marks event origin as CI. Values are length-bounded and control characters are removed. Other CI environments receive only generic provider/ephemeral markers. Repository, actor, and run identifiers remain in the local runtime log, so treat it as sensitive CI data.

By default, artifacts are created in a unique directory under `$RUNNER_TEMP/threadforge` (or the system temp directory when `RUNNER_TEMP` is unset). `--output-dir <dir>` selects a local artifact base. Each run writes `runtime.jsonl` and `threadforge-ci-summary.json`; manage their retention as potentially sensitive CI data. Nothing is uploaded or forwarded automatically.

Useful options:

- `--min-events <n>` requires at least `n` valid events from the selected harness (default `1`).
- `--require-telemetry=false` lets the child succeed when telemetry validation fails; a nonzero child exit code is still preserved.
- `--work-dir <dir>` selects an existing, non-symlinked child working directory.
- `--json` emits the final result as JSON on stdout and directs child stdout to stderr so the result remains parseable.

If the child exits nonzero, `ci exec` returns that exit code. If the child succeeds but required telemetry is missing or malformed, it returns `1`.

## Capture Codex CLI

```powershell
threadforge ci exec --harness codex-cli -- codex exec "Review this change"
```

For multi-step workflows, use `threadforge ci start --harness codex-cli` before the Codex step and `threadforge ci finish` in an `always()` step afterward. Threadforge creates a private `<artifact-dir>/codex-home/config.toml` and exports that directory as `CODEX_HOME`; it routes OTLP/HTTP JSON logs and traces to the loopback receiver, sets `otel.log_user_prompt=false`, and disables metrics export to avoid Codex's default Statsig metrics destination and duplicate usage accounting. The temporary home contains no `auth.json` and does not copy or edit `~/.codex/config.toml`. Supply authentication separately to the Codex step (for example through the runner's `CODEX_API_KEY` secret environment); Threadforge does not copy credentials into its config, session state, daemon environment, or `GITHUB_ENV`. If you choose Codex `--json`, its content-bearing JSONL stream remains the child command's stdout; Threadforge normalizes OTLP events separately and does not ingest that raw JSONL stream as telemetry.

## Capture across multiple workflow steps

```yaml
- name: Start local capture
  run: threadforge ci start

- name: Run agent work
  run: claude -p "Review this change"

- name: Stop and validate capture
  if: always()
  run: threadforge ci finish
```

`ci start` creates a private artifact directory, a `runtime.jsonl` file, and a short-lived detached receiver bound only to loopback. It appends a narrow allowlist of telemetry variables and `THREADFORGE_CI_STATE_PATH` to `GITHUB_ENV` when available; the exports also blank inherited per-signal endpoints, headers, client certificates, and Claude's beta tracing endpoint so they cannot bypass the local receiver. Arbitrary inherited `OTEL_RESOURCE_ATTRIBUTES` are replaced by the bounded CI metadata allowlist. Without `GITHUB_ENV`, start prints a versioned JSON result whose `exports` map can be used by a caller. The receiver inherits only a small OS/runtime environment allowlist, not agent credentials or CI secrets. Its state file is mode `0600` on Unix-like systems and contains a random shutdown token; the token is never placed in command arguments, exported variables, or the start result. `ci finish` uses that token over loopback, waits for clean shutdown, validates the log using the stored minimum-event and telemetry-required settings, then writes `threadforge-ci-summary.json`. `--require-telemetry` on `finish` can explicitly override the setting from `start`; absent an override, the start-time choice is used. Re-running finish on a cleanly stopped session revalidates the saved log.

On Windows, state-file replacement retries transient access-denied, sharing, and lock violations for at most 20 attempts spaced 25 ms apart. If the daemon cannot persist its stopping/stopped transition, it marks the session failed where possible; shutdown diagnostics expose only active/idle/new connection counts and never the control token.

Options include `ci start --harness claude-code|codex-cli --output-dir <dir> --state-path <file> --github-env <file> --min-events <n> --require-telemetry=false` and `ci finish --state-path <file> --min-events <n> --require-telemetry=false --json`. `GITHUB_ENV` is used by default when present. The state file and telemetry content remain local; the composite action's optional upload is limited to the path-free aggregate receipt described below. Other remote forwarding remains opt-in and is not part of this action.

## Use the checked-in GitHub composite action

The local composite action builds the CLI from the checked-out Threadforge source, starts the session, and carries the binary path and session path through `GITHUB_ENV` for later steps. Its `harness` input accepts `claude-code` or `codex-cli`:

```yaml
- uses: ./.github/actions/threadforge-ci
  with:
    mode: start

- name: Run agent work
  run: claude -p "Review this change"

- if: always()
  uses: ./.github/actions/threadforge-ci
  with:
    mode: finish
```

The action's `mode` is `start` or `finish`; `min-events` and `require-telemetry` default to `inherit` on finish so the start-time gate remains authoritative. On start they default to 1 and true. Optional `output-dir` and `state-path` are start inputs; the state path is automatically exported for finish. The action pins each external dependency to a full commit SHA and disables setup-go caching. `upload-summary` defaults to `false`; when explicitly set to `true` on a successful `finish`, it uploads only `threadforge-ci-public-summary.json` with three-day retention. Failed finish steps are not uploaded. Its `threadforge.ci-public-summary.v1` projection contains harness, status, telemetry requirement, validation status, event counts, malformed-row count, and minimum-event threshold; it omits paths, timestamps, run/repository identity, event bodies, and credentials. It never uploads `runtime.jsonl`, the private `threadforge-ci-summary.json`, or session state; it is diagnostic only, not signed cutover evidence.

An Ubuntu smoke workflow at `.github/workflows/threadforge-ci-action-smoke.yml` starts both Claude and Codex capture, emits synthetic content-free events, checks the temporary Codex config, and finishes each session in an `always()` step. It explicitly opts in to uploading the aggregate receipt for both synthetic runs and has read-only repository permission. The workflow is defined and contract-tested locally but has not yet been executed on GitHub-hosted infrastructure.

The cross-platform test workflow also has an Ubuntu-only Elasticsearch integration job. It generates the pack in the ephemeral runner directory, starts the generated loopback-only Compose stack with pinned images, waits for the synthetic Filebeat fixture to reach the metadata data stream, checks that the indexed document contains only the strict metadata allowlist and `raw_content=false`, verifies the Kibana data view, and stops the stack. The job uses a reserved `.example` endpoint only while generating an inert Vector config; it does not contact that endpoint, read Threadforge traces, or upload service logs. Image pulls are explicit in the workflow, and the hosted runner is discarded afterward. This job is implemented but remains unverified until it completes on GitHub-hosted infrastructure.

## Validate an existing log

```powershell
threadforge ci validate --log-path "$env:RUNNER_TEMP\threadforge\ci-<run>\runtime.jsonl" --harness claude-code --min-events 1 --json
```

Validation streams at most 256 MiB, caps a JSONL row at 1 MiB, validates each row as a canonical Threadforge/Beacon event, and fails if any row is malformed or the matching-event minimum is not met. `--harness all` checks all valid events; aliases `claude`/`claude_code` and `codex`/`codex_cli` are accepted. Validation does not infer that telemetry is complete merely because one event exists.

## Run an opt-in local security gate

```powershell
threadforge ci gate --log-path "$env:RUNNER_TEMP\threadforge\ci-<run>\runtime.jsonl" --harness claude-code --min-events 1 --fail-on high --json
```

`ci gate` validates the selected telemetry log and runs local threat rules against it. It uses the embedded baseline or installed local rules; `--rules-dir` selects an explicit rule pack. The `threadforge.ci-security-gate.v1` result includes validation metadata and aggregate security counts by severity only; it excludes findings, event/session identifiers, prompts, commands, reasons, and local paths. Exit code `0` means validation passed and the threshold was not met, `1` means telemetry validation failed, `2` means the threat scan was incomplete, and `3` means a finding met `--fail-on`. The command makes no network calls, writes no result file, and does not mutate the captured log.

The GitHub composite action accepts optional `security-fail-on: info|low|medium|high|critical` on its `finish` step. It stops and validates capture first, then runs `ci gate` against the captured `runtime.jsonl`. A gate failure fails the action, so the optional success-only receipt upload does not run. The public receipt remains telemetry-only and contains no findings. The checked-in smoke workflow opts into the critical threshold for synthetic Claude and Codex events; hosted execution remains an acceptance gate.

## Remaining CI parity

Codex local OTLP CI capture and the opt-in aggregate-only security gate are implemented, but direct live Codex CLI and third-party Codex action contract tests remain pending. The path-free aggregate summary upload is opt-in; S3/GCS raw-log upload, customer-managed forwarding, and SIEM forwarding remain unimplemented. The composite action's hosted-runner smoke workflow still needs to run successfully before this integration can count as accepted parity.
