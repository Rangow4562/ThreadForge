# Cline adapter and project skills

Threadforge has a partial, observational adapter for Cline CLI/SDK plugin callbacks. The adapter exposes `createThreadforgeClinePlugin()` from `@threadforge/adapter-contracts/cline-plugin` and maps `beforeRun`, `beforeTool`, `afterTool`, and `afterRun` callbacks. The Threadforge `cline plugin install|update|uninstall|status` adapter command manages one local-development wrapper in the selected user or project discovery directory; that wrapper imports the checkout by absolute file URL and is not the portable installation path. The manager does not edit Cline settings, MCP configuration, or neighboring plugins. A separate `cline sessions status|sync` poller reads local task JSON, session-message JSON, Kanban state, and the VS Code extension task store; see [Cline local session history](cline-history.md).

Install for the current user (defaults to `~/.cline`, overridden by `CLINE_HOME` or `--cline-dir`):

```sh
npm run adapter -- cline plugin install [--cline-dir <absolute-path>]
npm run adapter -- cline plugin status [--cline-dir <absolute-path>]
npm run adapter -- cline plugin update [--cline-dir <absolute-path>]
npm run adapter -- cline plugin uninstall [--cline-dir <absolute-path>]
```

For project scope, supply `--scope project --project-root <absolute-path>`. The managed entry is `.cline/plugins/threadforge.ts`; user scope installs to `<cline-dir>/plugins/threadforge.ts`. The installer records a content digest in its ownership marker, refuses unmanaged collisions or local edits, and changes only this file. Restart Cline after registration changes. Installation does not grant the callbacks authorization: all captured signals remain observational and are delivered only to loopback intake.

## Portable Cline CLI package

For a self-contained project plugin, build a new package directory from the current checkout and let Cline CLI install it. The builder transpiles the adapter and its local event helper into `index.js` and `emit.js`, rewrites the internal import to the package-local JavaScript file, records source/bundle SHA-256 digests, and includes `LICENSE`, `NOTICE`, and `THIRD_PARTY.md`. It refuses an existing output directory and has no external runtime package dependency.

```powershell
$bundle = Join-Path $env:TEMP ("threadforge-cline-plugin-" + [guid]::NewGuid())
npm.cmd --prefix adapters run build:cline-plugin -- --output $bundle
cline plugin install --cwd <project-root> $bundle
```

The optional installed-host smoke takes an exact Cline CLI version and absolute executable path. It installs into a disposable project with isolated Cline config/data directories, imports the installed entry, and exercises the observational callback with a stubbed local fetch; it makes no model request. It deliberately reports `host_callback_dispatch_status=not_exercised`: Cline's agent loop itself does not run in this smoke.

```powershell
npm.cmd --prefix adapters run test:cline-plugin-host -- --cline-path <absolute-cline-exe> --version <exact-version>
```

The generated package is unsigned and is not a public registry release. Review its manifest, hashes, and notices before installing. Use either the native portable package or the Threadforge checkout wrapper in a given scope, not both, to avoid duplicate callbacks. The project install and manual relocated-entry callback smoke were verified against Cline CLI `3.0.73` on October 23, 2026; this does not establish agent-triggered hook dispatch, model behavior, telemetry receiver availability, VS Code/JetBrains plugin support, or cross-harness task efficacy.

## Optional Cline CLI task-plan gate

The separate `threadforge-task-plan-gate` package is opt-in and bound at build time to one explicit Threadforge task, project, and local project root. It invokes the local `threadforge task plan guard` process without a shell, and covers only Cline CLI `editor` and `apply_patch` calls. The editor request forwards only its target path; a bounded patch request (up to 1 MiB) is passed to the local guard for target parsing. It does not persist or log callback input. In-scope calls return no Cline decision, leaving normal host approval policy in charge. Denied, missing-workspace, malformed, oversized, unavailable, or timed-out checks return Cline's `skip` result. Rebuild the package when the active task changes. Shell commands, custom tools, other Cline products, and any execution outside these direct tools are not covered; this is not a security boundary.

```powershell
$project = (Resolve-Path .).Path
$bundle = Join-Path $env:TEMP ("threadforge-cline-plan-gate-" + [guid]::NewGuid())
npm.cmd --prefix adapters run build:cline-plan-gate -- --output $bundle --task-id <task-id> --project-id <project-id> --project-root $project
cline plugin install $bundle --cwd $project
```

Optional builder flags are `--db <absolute-sqlite-path>`, `--threadforge-bin <command-or-path>`, and `--timeout-ms <100-10000>` (default 5000 ms). The generated package contains the selected identifiers and local paths, is unsigned, and is not a public release; keep it outside source control and do not share it. Review its hash manifest before installation. The SDK callback and `skip` contract follow Cline's [plugin examples](https://docs.cline.bot/sdk/plugin-examples); Threadforge fixture tests exercise the callback shape but do not run an agent turn. Live Cline dispatch remains unverified, so do not rely on this integration as a security control.

## Cline MCP server registration

`cline mcp install|update|uninstall|status` separately manages the user-global `mcpServers.threadforge` entry in Cline's MCP settings file. This is independent of the plugin callback installer above. It starts `threadforge mcp serve` without a fixed project root and never adds tools to Cline's auto-approval list. Because the host config is user-global and the server is not bound to a workspace, this path exposes the local SQLite-backed MCP surface only; it does not load version-controlled project memory or procedures. Use project skills or another verified project-scoped MCP configuration for project-specific memory. The path honors `CLINE_MCP_SETTINGS_PATH`; see [MCP setup](mcp.md#cline-user-level-mcp-entry).

## Captured signals

- `beforeRun` emits a session-start event and a prompt event when the run context contains a prompt.
- `beforeTool` emits an invocation; `afterTool` maps explicit failures, command outcomes, file reads/edits, and the documented Cline MCP tool names.
- `afterRun` emits a session completion/cancel/failure event and reported token/cost usage once.
- The observational telemetry plugin does not infer approval or write a Cline decision, prompt rewrite, or output replacement. The separate opt-in task-plan plugin may return `skip` for its two documented direct file tools only.

Task/call identifiers and workspace/file paths are SHA-256 hashed. Prompt, tool input, command, tool output, and file-change values are digested locally up to 64 KiB; oversized values are omitted. Context serialization tolerates cycles and is bounded to depth 8 and 2,000 values. Events are delivered only to the shared loopback `/v1/events` intake; `timeoutMs` defaults to 250 ms and can be configured from 100 to 10,000 ms. The callback fails open if intake or mapping fails, though Cline may await the callback while the bounded send completes.

## Integration limits

The installed smoke verifies one selected Cline CLI package-install path and can import the relocated module, but it does not run an agent turn or observe Cline invoking a hook. The adapter does not claim support for the VS Code extension or JetBrains plugin surfaces. On October 9, 2026, read-only history status parsed 2 records from the local Cline roots with no errors; no events were sent and no cursor was written. Full source-format reconciliation, signed/public distribution, live callback invocation, and cross-harness retrieval remain open release gates.

## Install an approved procedure as a project skill

Threadforge can place an already exported, privacy-reviewed procedure in the project skill directory `.cline/skills/<skill-name>/SKILL.md`. This is separate from the callback adapter above and is a knowledge-only path: it does not install telemetry callbacks, change Cline settings, configure MCP, or grant permission to execute a procedure. Cline must have project skills enabled, and the installed Cline version must recognize project skills before the file will be available to an agent.

First export the approved graph and review the generated file:

```sh
threadforge procedure export-skill --project-root <project> --id <approved-procedure-id> --privacy-reviewed
```

The JSON result includes `path` and `sha256`. After reviewing that exact artifact, run the adapter command from the `adapters/` directory, supplying both values:

```sh
npm run adapter -- cline skills install --project-root <project> --skill-file <exported-path> --sha256 <exported-sha256>
```

The installer accepts only a regular file under that project's `.threadforge/skills/<id>/rev-NNNNNN/SKILL.md`, validates the generated Threadforge frontmatter and advisory/privacy notices, checks the supplied SHA-256, and copies the exact bytes. It writes an ownership marker beside the skill, refuses unowned or conflicting files, and never overwrites or removes content that has changed since installation. To uninstall, pass the same export path and digest:

```sh
npm run adapter -- cline skills uninstall --project-root <project> --skill-file <exported-path> --sha256 <exported-sha256>
```

Each exported revision is immutable and has a revision-specific skill name. Installing a later revision does not remove an earlier one; uninstall an older revision with its original exported file and digest after confirming it is no longer needed. User-added files in the skill directory and neighboring project skills are preserved. The digest check establishes that the installer received the reviewed bytes; it is not a digital signature or proof of who approved them. The skill text itself is advisory and cannot enforce policy or authorize actions.

## Cline CLI project-skill inventory smoke

`npm run test:cline-skill-host -- --cline-path <absolute-cline-exe> --version <exact-version>` creates a synthetic, digest-matched Threadforge export in a disposable project, installs it with the real project-skill installer, and runs the selected Cline CLI's `config --json` inventory with isolated home/config/data directories. It checks that exactly one skill with the expected project path appears, but does not start an agent or ask a model to retrieve/use the procedure. Its receipt schema is `threadforge.cline-skill-inventory-smoke.v1`; `agent_retrieval_status` is fixed to `not_exercised`, `model_invocations` is zero, and no skill name/body is included in the receipt. This inventory check passed on Cline CLI `3.0.73` on October 23, 2026.

On Windows with Cline CLI `3.0.73`, the separate `cline skill list --agent cline` wrapper failed because `skills@latest` was not recognized; it is not used as inventory evidence. An exploratory local-provider agent attempt was stopped after Cline sent a request toward the public OpenAI service instead of the configured loopback fixture. It used only a dummy key and synthetic prompt/procedure; the loopback fixture received zero requests. No real workspace data or credential was configured, but the request may have carried the synthetic test content. Do not treat this as successful retrieval, and do not retry model-bound Cline tests until provider routing is independently verified in a restricted environment.

The project-skill path has automated fixture/CLI coverage and selected-version inventory discovery, but model-bound retrieval, agent use, project/global name-collision behavior, skill-setting behavior, VS Code/JetBrains surfaces, and cross-harness outcomes remain unverified. Do not treat inventory discovery as task efficacy or full Beacon/Mancode parity.

## Advisory project-memory rule

The shared project-instruction bootstrap can install a Threadforge continuity rule into `.cline/rules/threadforge-memory.md`:

```powershell
npm.cmd --prefix adapters run adapter -- bootstrap instructions plan --project-root <project> --hosts cline
npm.cmd --prefix adapters run adapter -- bootstrap instructions install --project-root <project> --hosts cline
```

Use the same command family with `update` or `uninstall` to manage the Threadforge-marked block. The rule is advisory and tells the agent to prefer freshness-aware project-memory v2 exact reads, require an explicit trusted `project_id`, and ask or use an approved portable artifact when the required MCP tool or project binding is unavailable. This does not configure Cline's user-global MCP server, set trust, change approvals, or install a skill. Fixture tests verify that unrelated file content is preserved; Cline rule discovery, prompt behavior, actual MCP retrieval, and efficacy have not been tested.
