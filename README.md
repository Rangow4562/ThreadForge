<div align="center">

<img src="assets/threadforge-banner.png" alt="Threadforge — Local-First Memory, Task Continuity, Calibrated Decisions & Harness Evolution for AI Coding Agents" width="100%" style="border-radius: 8px;" />

# Threadforge

**Local-first memory, task continuity, calibrated typed decisions, and procedural learning for AI coding agents.**

[![Release](https://img.shields.io/badge/Release-v0.4.0-2563eb?style=flat-square&logo=github)](https://github.com/Rangow4562/ThreadForge/releases/tag/v0.4.0)
[![License](https://img.shields.io/badge/License-Apache--2.0-2f855a?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](go.mod)
[![Python](https://img.shields.io/badge/Python-3.12%2B-3776AB?style=flat-square&logo=python&logoColor=white)](python/pyproject.toml)
[![Node](https://img.shields.io/badge/Node.js-22.19%2B-339933?style=flat-square&logo=nodedotjs&logoColor=white)](adapters/package.json)
[![Posture](https://img.shields.io/badge/Runtime-Loopback--Only%20%7C%20Local--First-00b4d8?style=flat-square)](docs/architecture.md)
[![M8ven Score](https://m8ven.ai/badge/mcp/rangow4562-threadforge-ttitus)](https://m8ven.ai/mcp/rangow4562-threadforge-ttitus?s=readme)

[Architecture](docs/architecture.md) •
[Feature Parity](docs/feature-parity.md) •
[Source Matrix](docs/source-matrix.md) •
[MCP Setup](docs/mcp.md) •
[Quick Start](#quick-start) •
[Harness Matrix](#agent-harness-integrations) •
[Contributing](contributor.md) •
[Commit Guide](commit.md)

</div>

---

## Overview

**Threadforge** is the local-first successor architecture unifying and advancing the capabilities of **Beacon**, **Mancode**, **AnyJev**, and **RRSI**. Its core objective is to prevent repeated failure modes across disparate AI coding agent harnesses by enforcing strict source-freshness checking, cryptographic procedure provenance, revision-controlled task state, calibrated typed decisions, and verifiable evolution loops.

> [!NOTE]
> Threadforge operates strictly **local-first** and **loopback-only** (`127.0.0.1`). It never transmits telemetry traces, private session stores, or raw credentials over external networks, and requires human-in-the-loop review for all memory promotions.

```
                      ┌──────────────────────────────────────────────┐
                      │    AI Coding Agents & IDE Harnesses          │
                      │ (Claude Code, Cursor, Codex, OpenCode, etc.) │
                      └──────────────────────┬───────────────────────┘
                                             │ Local MCP / Hooks / OTLP
                                             ▼
                      ┌──────────────────────────────────────────────┐
                      │            THREADFORGE CORE ENGINE           │
                      ├──────────────────────────────────────────────┤
                      │  • Loopback OTLP Ingest (:4318 / :4317)      │
                      │  • Local Memory & Procedure Graph Engine     │
                      │  • Revision-Checked CAS Task Store (SQLite)  │
                      │  • Deny-Only Plan Guards & Source Hashing    │
                      │  • AnyJev Calibration & Distillation Heads   │
                      │  • RRSI Screen, Critic & Repair Evaluator    │
                      └──────────────────────┬───────────────────────┘
                                             │ Loopback API / Web UI
                                             ▼
                      ┌──────────────────────────────────────────────┐
                      │       Local Developer Dashboard (:4318)      │
                      │  (Security Scanner, Context Graph, Telemetry)│
                      └──────────────────────────────────────────────┘
```

---

## Core Pillars & Lineage

| Pillar | Legacy Origin | Core Capabilities | Primary CLI Commands |
| :--- | :--- | :--- | :--- |
| **Telemetry, Security & Memory** | **Beacon** | Loopback OTLP/HTTP & gRPC intake, 25+ host adapter slices, CEL threat-rule scanner, 11 metadata-only SIEM sanitizer packs, and SHA-256 source-freshness-checked memory graphs. | `threadforge serve`<br>`threadforge memory`<br>`threadforge procedure`<br>`threadforge security`<br>`threadforge endpoint` |
| **Task & Session Continuity** | **Mancode** | CAS revision-controlled tasks, append-only requirements Q&A, repository-scoped task plans, 14 native session-store handoff readers, and Ed25519-signed team procedure sync. | `threadforge task`<br>`threadforge session`<br>`threadforge handoff`<br>`threadforge team`<br>`threadforge bootstrap` |
| **Calibrated Typed Decisions** | **AnyJev** | L0 cyclic-shift debiasing, L1 temperature scaling, L2 closed-form hidden-state heads, model block truncation, distillation pipeline, and fingerprint-pinned vLLM inference. | `threadforge decision calibrate`<br>`threadforge decision artifact`<br>`threadforge decision distill`<br>`threadforge decision benchmark` |
| **Harness Self-Improvement** | **RRSI** | Leakage critic screening (v3), bounded local repair, score/noise/token-cost candidate selection, pinned benchmark task derivation, and paired bootstrap evaluation. | `threadforge evolution screen`<br>`threadforge evolution critic`<br>`threadforge evolution repair`<br>`threadforge evolution select` |

---

## Visual Architecture & Governance Lifecycle

### System Architecture
<p align="center">
  <img src="assets/architecture.svg" alt="Threadforge Local-First System Architecture" width="100%" />
</p>

### Governed Memory & Continuity Lifecycle
<p align="center">
  <img src="assets/lifecycle.svg" alt="Threadforge Governed Procedure, Memory & Continuity Lifecycle" width="100%" />
</p>

---

## Agent Harness Integrations

Threadforge integrates with over 20 AI coding agent harnesses and runtimes, providing modular adapters, MCP servers, prompt-time memory retrieval, and defense-in-depth task-plan guards.

| Harness / Runtime | Integration Type | Approved Memory Retrieval | Task Continuity / Plan Guard | Local History Poller | Status & Documentation |
| :--- | :--- | :---: | :---: | :---: | :--- |
| **Aider** | Portable approved procedure file | Explicit read-only `--read` argument | None | None | Manual retrieval path ([`docs/aider.md`](docs/aider.md)) |
| **Claude Code** | Native Hooks + MCP | `UserPromptSubmit` (local) | Deny-only `PreToolUse` file guard | JSONL Transcript Poller | Verified Local Smoke ([`docs/claude-history.md`](docs/claude-history.md)) |
| **Codex CLI** | Hooks + OTLP + MCP | `UserPromptSubmit` (local) | Advisory continuity context | Native Session Reader | Verified Loopback OTLP ([`docs/codex-cli-telemetry.md`](docs/codex-cli-telemetry.md)) |
| **Cursor** | Extension + MCP | `sessionStart` Bootstrap | Deny-only `Write` plan gate | Composer DB & Transcripts | Verified Composer Sync ([`docs/cursor-history.md`](docs/cursor-history.md)) |
| **Gemini CLI** | Project Hooks + MCP | `BeforeAgent` Hook | Deny-only `BeforeTool` guard | Loopback OTLP Intake | Verified Tool Guard ([`docs/gemini-cli.md`](docs/gemini-cli.md)) |
| **Antigravity** | PreInvocation Hook | Invocation 0 Bootstrap | Scoped project-task context | Local Telemetry Slices | Verified Hook Transport ([`docs/antigravity-cli.md`](docs/antigravity-cli.md)) |
| **OpenCode** | Plugin + Transform | Opt-in prompt-time project memory ([`docs/opencode-memory.md`](docs/opencode-memory.md)) | Project-scoped planning profile | Session CLI Poller | Verified Prompt-Memory Transport ([`docs/opencode-history.md`](docs/opencode-history.md)) |
| **Cline** | Plugin + Global MCP | `.cline/skills/` + advisory project rule | Task/Session/Kanban stores | Bounded History Poller | Verified Package Build ([`docs/cline.md`](docs/cline.md)) |
| **Qwen Code** | Project Hooks + MCP | `UserPromptExpansion` | Best-effort deny-only file guard | Loopback OTLP Intake | Verified Settings Lock ([`docs/qwen-code.md`](docs/qwen-code.md)) |
| **GitHub Copilot CLI** | Prompts + Skills + MCP | Opt-in prompt-time approved memory ([`docs/copilot-cli-memory.md`](docs/copilot-cli-memory.md)) | Deny-only command hook | Events JSONL Poller | Verified Prompt Installer ([`docs/copilot-prompts.md`](docs/copilot-prompts.md)) |
| **Command Code** | Project Skills + MCP | `.commandcode/skills/` | Advisory continuity context | Stdio Configuration | Verified Skill Discovery ([`docs/command-code.md`](docs/command-code.md)) |
| **Windsurf / Cascade** | Project Skills | `.agents/skills/` | Advisory procedure guidance | Local Project Scope | Verified Skill Placement ([`docs/windsurf-skills.md`](docs/windsurf-skills.md)) |
| **Goose** | Advisory Instruction | `.goosehints` Target | Non-blocking advisory context | Schema-v17 Session Reader | Verified Advisory Target ([`docs/goose-history.md`](docs/goose-history.md)) |
| **Devin (CLI/Desktop/Cloud)** | Observation Slice | Project Skills Export | Local-only telemetry sync | Observation Hooks | Partial / Observation-Only ([`docs/devin-cli.md`](docs/devin-cli.md)) |
| **DeepSeek Harness** | Hooks + Skills | `.dsh/skills/` | Advisory continuity context | JSONL/Zstandard Poller | Verified History Sync ([`docs/deepseek-harness.md`](docs/deepseek-harness.md)) |
| **Hermes Agent** | Print-only Hooks | YAML Configuration | Advisory continuity context | Read-only SQLite Poller | Read-Only Inspection ([`docs/hermes.md`](docs/hermes.md)) |
| **OpenHands** | Project Hooks | `.agents/skills/` | Async non-authorizing telemetry | Hook Dispatcher | Verified Hook Placement ([`docs/openhands.md`](docs/openhands.md)) |
| **OpenClaw** | Gateway Plugin | Loopback Intake | Bounded fail-open plugin | OTLP Event Validator | Verified Gateway Plugin ([`docs/openclaw-gateway.md`](docs/openclaw-gateway.md)) |
| **Browser Extension** | Chrome / Chromium | Loopback Receiver | Web session observation | Local Replay Suite | Verified Privacy Suite ([`docs/browser-extension.md`](docs/browser-extension.md)) |

---

## Quick Start

Linux `amd64` users can install the published Go CLI without root using the [Linux installer](docs/linux-install.md). Python decision/evolution modules and harness adapters are separate packages.

### 1. Prerequisites
- **Go**: `1.25` or newer
- **Python**: `3.12` or newer (managed via `python/pyproject.toml`)
- **Node.js**: `22.19` or newer (for `@threadforge/adapter-contracts`)

### 2. Launch Local Loopback Service & Web Dashboard
Start the local Threadforge daemon with loopback OTLP collectors and the developer dashboard:

```bash
# Starts OTLP/HTTP on 127.0.0.1:4318 and OTLP/gRPC on 127.0.0.1:4317
threadforge serve --project-root .
```

Open your browser to:
- **Developer Dashboard**: `http://127.0.0.1:4318/dashboard/`
- **Context API v2**: `http://127.0.0.1:4318/v2/context`
- **Liveness & Health**: `http://127.0.0.1:4318/healthz`

<p align="center">
  <img src="assets/dashboard-preview.svg" alt="Threadforge Local Loopback Dashboard Preview" width="100%" />
</p>

### 3. Task Continuity & Checkpoint Tracking
Maintain cross-session task state, requirements, and progress without leaking private tokens:

```bash
# 1. Create a CAS-tracked task
threadforge task create \
  --id fix-parser \
  --project-id my-repo \
  --title "Fix parser nested quote regression" \
  --objective "Restore nested quote tokenization in AST parser" \
  --criterion "Nested quote fixture tests pass without regressions"

# 2. Advance task progress (CAS expected-revision check)
threadforge task update \
  --id fix-parser \
  --expected-revision 1 \
  --status in_progress \
  --next-action "Add regression reproduction unit test"

# 3. Associate an active harness session and record a handoff
threadforge session start --id parser-session-1 --task-id fix-parser --harness codex-cli
threadforge task record \
  --id parser-handoff-1 \
  --project-id my-repo \
  --task-id fix-parser \
  --session-id parser-session-1 \
  --kind handoff \
  --summary "Reproduction isolated in test fixture; implement parser state reset next"

# 4. Close session with structured summary
threadforge session update \
  --id parser-session-1 \
  --expected-revision 1 \
  --close \
  --summary "Reproduced the issue and confirmed fixture failure"
```

### 4. Source-Bound Project Memory & Procedures
Publish human-approved, tamper-evident facts, decisions, and procedures bound to exact source file digests:

```bash
# 1. Calculate source fingerprints for referenced files
threadforge memory fingerprint --project-root . --path src/parser.go --path src/tokens.go

# 2. Publish an approved, privacy-reviewed memory candidate
threadforge memory publish \
  --project-root . \
  --file examples/project-memory-candidate.json \
  --expected-revision 0 \
  --reviewer "Lead Architect" \
  --reason "Reviewed parser quote escape requirements" \
  --privacy-reviewed

# 3. Verify exact scope and source freshness on retrieval
threadforge memory read \
  --project-root . \
  --project-id my-repo \
  --kind decision \
  --id parser-quote-handling
```

### 5. AI Harness MCP Integration
Generate or automatically configure Model Context Protocol (MCP) tool bindings for your agent harnesses:

```bash
# Print generic stdio MCP configuration JSON or TOML
threadforge mcp print-config --project-root . --command threadforge --format json

# Or plan and install directly for supported hosts (Claude Code, Cursor, Codex, OpenCode, etc.)
threadforge bootstrap mcp install --host claude-code --project-root .
```

---

## Technical Deep Dives

### 1. Governed Task Plans & Deny-Only Plan Gating
- **Requirements Q&A**: Tasks begin with formal, append-only requirements. Operators record clarifications using `task requirements ask|answer` and finalize them before plan confirmation.
- **Deterministic Scaffolding**: Generate plan scaffolds with `threadforge task plan template --id <task-id>`, edit repository-scoped file inclusion/exclusion rules, and store via `threadforge task plan revise`.
- **Deny-Only Guardrails**: For Claude Code, Qwen Code, Gemini CLI, and GitHub Copilot CLI, Threadforge installs optional local hooks that deny unauthorized direct file modifications outside confirmed plan scopes. These hooks **never grant execution permissions** and operate purely as defense-in-depth safety checks.

### 2. Cryptographic Provenance & Team Sync
- **Ed25519 Signatures**: Shared procedure records require signing keys pinned out-of-band by team administrators. Unsigned, altered, or stale records are withheld during retrieval.
- **Git-Ref Sync**: `threadforge team sync pull|push` synchronizes only signed, sanitized JSON records through a dedicated Git ref. Raw traces, session transcripts, local SQLite state, credentials, and worktrees are **never** transferred.
- **Audited Decommissioning**: Team scopes can be frozen via `threadforge team scope decommission`, halting continuity writes while permanently preserving append-only audit logs.

### 3. Calibrated Decisions & Distillation (AnyJev)
- **Calibration Engine**: Implements L0 cyclic-shift debiasing, L1 temperature scaling, and L2 closed-form hidden-state classification heads to calibrate model confidence before tool execution.
- **Distillation Pipeline**: Composes case generation, synthetic extraction, gold feature extraction (`H`/`y`/`K`/`layers`), model fitting, and same-state soft-target replay (`threadforge decision distill pipeline`).
- **Benchmarking**: Validates model depth, ECE (Expected Calibration Error), and inference latency over loopback vLLM endpoints (`threadforge decision benchmark pipeline`).

### 4. Self-Improving Harness Evolution (RRSI)
- **Task Screening & Leakage Critics**: Automatically verifies test tasks against benchmark contamination using v3 leakage critics.
- **Bounded Local Repair**: Executes iterative repair attempts within bounded cost, token, and noise budgets (`threadforge evolution repair`).
- **Paired Bootstrap Evaluation**: Conducts rigorous non-parametric bootstrap resampling (v1 and v2) across baseline and candidate runs (Terminal-Bench 2.1, SWE-bench Verified, Harvey LAB, JobBench, EngDesign).

### 5. Enterprise SIEM & Telemetry Sanitizer Packs
Threadforge includes 11 local metadata-only sanitizer packs that format runtime telemetry for enterprise security pipelines without exposing sensitive code, prompts, or credentials:
- **Cloud Object Stores**: Amazon S3 (`endpoint s3`), Google Cloud Storage (`endpoint gcs`).
- **Logging & SIEM**: AWS CloudWatch (`endpoint cloudwatch`), Splunk HEC (`endpoint splunk-hec`), CrowdStrike Falcon (`endpoint falcon-hec`), Datadog Agent (`endpoint datadog`), Sumo Logic (`endpoint sumo`), Rapid7 InsightIDR (`endpoint rapid7`), Wazuh SIEM (`endpoint wazuh`), Microsoft Sentinel (`endpoint sentinel`), Elasticsearch (`endpoint elasticsearch`).

---

## Privacy & Security Posture

> [!IMPORTANT]
> **Core Architectural Guarantees**:
> 1. **Zero Autonomous Model Invocations**: Neither hooks nor telemetry pollers ever invoke an external LLM autonomously. `memory learn` and `decision` commands must be explicitly invoked by human operators.
> 2. **Loopback-Only Network Binding**: All internal daemon listeners (`:4318`, `:4317`) enforce loopback IP addresses (`127.0.0.1`), blocking unauthorized LAN or WAN ingress.
> 3. **Automatic Stale-Data Withholding**: Whenever source code files drift from their recorded SHA-256 fingerprints, associated project memory records are immediately marked unverified and withheld from agent prompts.
> 4. **No Raw Trace Forwarding**: SIEM packs export strictly allowlisted, metadata-only projections (timestamps, severity, error codes); prompt bodies, tool outputs, and environment variables stay local.
> 5. **Read-Only Cutover Gating**: Legacy system replacement is governed by `threadforge cutover assess`, a fail-closed verification evaluator requiring full feature parity, held-out outcome evidence, and cryptographic human sign-offs.

---

## Development & Verification

### Running the Comprehensive Test Suite
Execute all cross-ecosystem test suites (Go core, Python modules, TypeScript adapters, browser extension, and host smokes) via the automated test harness:

```powershell
# Run full verification suite on Windows
./test.ps1
```

```bash
# Run Go unit and integration tests
go test ./...

# Verify CLI compilation and version output
go run ./cmd/threadforge version

# Validate procedure graph schemas
go run ./cmd/threadforge procedure validate --file examples/procedure-graph.json

# Run TypeScript adapter contracts test suite
cd adapters
npm test
npm run test:package
```

---

## Documentation Index

- [Architecture & Invariants](docs/architecture.md) — Comprehensive local-first system design and threat model.
- [Feature Parity Ledger](docs/feature-parity.md) — Line-by-line feature tracking against legacy Beacon, Mancode, AnyJev, and RRSI.
- [Host Source Matrix](docs/source-matrix.md) — Detailed coverage analysis for each supported agent harness.
- [Aider Procedure Access](docs/aider.md) — Digest-checked portable procedure loading with Aider's explicit read-only file option.
- [Model Context Protocol (MCP) Setup](docs/mcp.md) — Guide to configuring MCP tools, resources, and handoffs.
- [Team Authorization & Sync](docs/team-authorization.md) — Ed25519 cryptographic trust policies, revision control, and Git-ref sync.
- [Task Continuity & Handoffs](docs/task-handoffs.md) — Cross-session continuity, session stores, and brief exports.
- [Security Scanner & CEL Rules](docs/security.md) — Local telemetry threat scanning with embedded rule packs.
- [Cutover Assessment Gate](docs/cutover.md) — Evaluation requirements for legacy system cutover approval.
- [Contributor Guidelines](contributor.md) — How to contribute code, tests, and documentation.
- [Commit Conventions](commit.md) — Standardized Git commit message formats.

---

## License

Threadforge is licensed under the **[Apache License, Version 2.0](LICENSE)**.
