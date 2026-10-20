# Contributing to Threadforge (`contributor.md`)

Thank you for contributing to **Threadforge**—the local-first memory, task continuity, calibrated decision, and regularized harness evolution platform for AI coding agents.

Threadforge unifies the capabilities of **Beacon** (telemetry, threat rules, SIEM forwarding, and session history), **Mancode** (task/session continuity, requirements governance, scoped plans, and team handoffs), **AnyJev** (calibrated typed LLM decisions and distillation), and **RRSI** (regularized recursive self-improvement of agent harnesses) under a single local-first architecture.

Every contribution must uphold Threadforge's core engineering and security invariants.

---

## Core Architectural & Privacy Invariants

1. **Local-First & Loopback-Only by Default**
   - Normal CLI, hook, plugin, MCP, and OTLP execution must never depend on hosted accounts, remote policy services, or external network calls.
   - HTTP (`127.0.0.1:4318`) and gRPC (`127.0.0.1:4317`) receivers bind exclusively to loopback IP addresses. The TypeScript event emitter (`adapters/src/emit.ts`) rejects non-loopback destinations and HTTP redirects.
2. **Strict Runtime Boundaries**
   - **Go (`cmd/`, `internal/`, `pkg/`)** owns the CLI, loopback HTTP/gRPC/MCP server, SQLite state (CAS revisions, proposals, continuity, audit logs), JSONL trace intake, CEL threat-rule scanning, and `.threadforge/` project-file transactions.
   - **TypeScript (`adapters/`, `adapters/browser-extension/`)** translates host-specific hooks, plugins, session stores, and browser events into versioned Threadforge contracts. TypeScript packages **never** open or mutate the Go SQLite database directly.
   - **Python (`python/`, `domains/`)** implements AnyJev decision calibration/distillation and RRSI evolution screening, critic repair, and domain verification over versioned JSON-over-stdio (`threadforge.python.v1`). Python processes **never** open the Go SQLite database.
3. **Advisory Memory & Explicit Human Gates**
   - Learning (`threadforge memory learn`) and procedure drafting (`threadforge procedure propose`) are explicit local operations; no host hook invokes a model to promote memory.
   - Only structurally valid, held-out-validated, human-approved procedures and SHA-256 source-freshness-verified project memory records are returned as active guidance.
   - Retrieved context, task plans, and `SKILL.md` exports are **advisory continuity only**—they never grant tool execution authority or act as an OS security sandbox.
4. **Honest Verification & Cutover Discipline**
   - Never mark a capability, host adapter, or benchmark row `Complete` in [`docs/feature-parity.md`](docs/feature-parity.md) or [`docs/source-matrix.md`](docs/source-matrix.md) without reproducible acceptance evidence.
   - Do not modify legacy repositories (`agent-beacon`, `mancode`, `AnyJev`, `rrsi`) or weaken `threadforge cutover assess`.

---

## Repository Layout

```text
threadforge/
├── cmd/threadforge/            # Go CLI entrypoint
├── internal/                   # Private Go packages
│   ├── cli/                    # Subcommands (procedure, memory, task, session, handoff, decision, evolution, team, endpoint, migrate, cutover, ci, security, inventory)
│   ├── service/                # Loopback HTTP/gRPC/OTLP server & embedded /dashboard/ UI
│   ├── mcp/                    # Stdio & HTTP Model Context Protocol server tools
│   ├── store/                  # SQLite persistence, CAS revision enforcement & append-only audit logs
│   ├── projectmemory/          # Versioned .threadforge/memory/ and .threadforge/procedures/ records
│   ├── forwarding/             # 11 customer-managed metadata-only SIEM/storage pack generators
│   ├── security/               # Vendored Beacon CEL threat-rule compiler, scanner & review ledger
│   ├── handoff/                # 14 native session-store readers, redacted brief export & resume
│   ├── migrate/                # Staged local-pending-review legacy snapshot & converter pipeline
│   ├── cutover/                # Read-only v4 all-or-nothing cutover release gate
│   └── teamtransport/          # Ed25519-signed Git-ref procedure sync & scope lifecycle
├── pkg/                        # Versioned Go domain contracts (proceduregraph, continuity, evaluation)
├── adapters/                   # TypeScript host adapters, skill/MCP installers & CLI (Node 22+)
│   └── browser-extension/      # Opt-in Chromium/Firefox MV3 extension for Claude.ai & ChatGPT Web
├── python/                     # Python stdio protocol (threadforge_py), AnyJev & RRSI packages
├── domains/                    # RRSI coding & workspace domain runners and scripts
├── containers/engineering/     # Locked digest-pinned Docker recipe for EngDesign evaluation
├── schema/                     # Canonical JSON Schemas for all v1/v2/v3/v4 contracts
├── docs/                       # Architecture, per-harness integration guides & feature-parity ledger
├── third_party/                # Vendored upstream contracts & notices (tracked in THIRD_PARTY.md)
└── test.ps1                    # Unified local verification gate
```

---

## Prerequisites & Development Setup

Match the pinned toolchain used in [`.github/workflows/threadforge-tests.yml`](.github/workflows/threadforge-tests.yml):

- **Go**: `1.25` or newer (`GOTOOLCHAIN=local`)
- **Python**: `3.12` (with `pip`, `pytest`, `ruff`, `setuptools>=77`, and `wheel`)
- **Node.js**: `22.19.0` or newer (with `npm`)

### 1. Go Core Setup

```powershell
go test ./...
go vet ./...
go run ./cmd/threadforge version
```

### 2. Python Engine Setup

```powershell
cd python
python -m pip install -e ".[test,decisions,workspace]"
python -m pytest -q
python -m compileall -q .
ruff check threadforge_py tests
```

### 3. TypeScript Adapters & Browser Extension Setup

```powershell
cd adapters
npm ci
npm run check
npm test

cd browser-extension
npm ci
npx playwright install chromium
npm run check
npm run test:unit
npm run build
```

> [!NOTE]
> On Windows PowerShell, use `npm.cmd` when forwarding `--` flags to adapter scripts (e.g., `npm.cmd run adapter -- devin ...`) so PowerShell's `npm.ps1` shim does not consume the flags.

---

## Running the Verification Suite

Before opening a pull request, run the unified verification script from the repository root:

```powershell
./test.ps1
```

`./test.ps1` executes every required gate in sequence and exits non-zero if any required check fails or is blocked:

1. **Go unit & integration tests** (`go test ./...`) and static analysis (`go vet ./...`)
2. **Python test suite** (`pytest -q`), bytecode compile check, and offline PEP 517 sdist/wheel packaging & protocol smoke (`python/scripts/verify_distribution.py`)
3. **Dependency manifest inventory check** (`scripts/generate_dependency_inventory.py`)
4. **Runner shell syntax checks** (`bash -n` on coding, workspace, and Elasticsearch scripts when Bash is available)
5. **TypeScript adapter typecheck, unit tests, and tarball payload audit** (`npm run check`, `npm test`, `npm run test:package`)
6. **Browser extension checks** (TypeScript check, unit tests, Chromium build, Playwright Chromium replay suite, Firefox lint, and Chromium/Firefox `web-ext` package smokes)
7. **Optional no-model installed-host smoke** (`npm run test:installed-skills` when `opencode`, `gemini`, `codex`, and `claude` CLIs are installed locally)
8. **Vendored Beacon contract & session-reader Go suites** (`third_party/agent-beacon/pkg/asymptoteobserve` and `third_party/beacon-sessionreaders`)

---

## Contribution Workflows by Area

### Adding or Updating a Host Adapter (`adapters/`)
- Preserve unrelated user and project settings byte-for-byte where possible; use explicit Threadforge ownership markers or hash-gated slots.
- Default to **digest-only** telemetry capture (`sha256` hashes and byte lengths). Any text retention must require an explicit, size-capped opt-in flag and pass through Go intake credential redaction.
- Never alter host project trust, enable hook consent automatically, or emit `"allow"` tool authorization decisions.
- Document exact capabilities, config paths, and unverified surfaces in `docs/<harness>.md`, [`docs/source-matrix.md`](docs/source-matrix.md), and [`docs/feature-parity.md`](docs/feature-parity.md).

### Adding or Modifying Schemas & Contracts (`schema/`, `pkg/`)
- Schemas are immutable once versioned (`v1`, `v2`, `v3`, `v4`). Introduce a new versioned schema file when making breaking changes.
- Keep Go structs, TypeScript validators, Python protocol handlers, and JSON Schemas in lockstep and covered by round-trip fixture tests.

### Modifying Python AnyJev or RRSI Modules (`python/`)
- Ensure all operations exposed over `threadforge_py` validate input bounds, reject credential-bearing strings, and remain deterministic under fixed seeds.
- Run `python scripts/verify_distribution.py` from `python/` to confirm no `.pt`, `.safetensors`, `.gguf`, `.npz`, or `.parquet` payloads leak into the sdist or wheel.

### Updating Third-Party Code or Dependencies
- Do not add unreviewed dependencies casually. If a manifest changes (`go.mod`, `package.json`, `pyproject.toml`, or container lockfiles), verify that `python scripts/generate_dependency_inventory.py` succeeds and update [`THIRD_PARTY.md`](THIRD_PARTY.md) and [`NOTICE`](NOTICE) as appropriate.

---

## Pull Request Checklist

- [ ] Follows the commit message conventions in [`commit.md`](commit.md).
- [ ] Preserves local-first, loopback-only, and digest-first privacy defaults.
- [ ] Includes deterministic unit/integration tests using temporary directories (`t.TempDir()`, `tmp_path`) with zero external network calls.
- [ ] Passes `./test.ps1` locally (or documents any environment-skipped optional checks).
- [ ] Updates `README.md`, `docs/<topic>.md`, `docs/feature-parity.md`, and `docs/source-matrix.md` to reflect user-visible behavior and honest verification status.
- [ ] Contains no secrets, credentials, test binaries (e.g., `*.test.exe`), or benchmark datasets.

