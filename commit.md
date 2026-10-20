# Commit Conventions & Git Governance (`commit.md`)

Threadforge maintains a clean, auditable, and machine-parseable Git history across its Go core, TypeScript host adapters, Python decision/evolution engines, and JSON schemas. All contributors and automated workflows must follow the commit standards documented below.

---

## 1. Commit Message Format

Threadforge uses **Conventional Commits**:

```text
<type>(<scope>): <short imperative summary>

[optional body explaining motivation, architecture boundaries, and verification]

[optional footer(s): Refs, Breaking Changes, Co-authored-by]
```

### Rules for the Subject Line
- **Length**: Keep the header line under **72 characters** (hard limit: 100 characters).
- **Mood**: Use the **imperative, present tense** (`add`, `fix`, `enforce`, `withhold`, not `added`, `fixes`, or `adding`).
- **Casing**: Lowercase `type`, `scope`, and the first word of the summary (unless it starts with a proper noun or acronym like `OTLP`, `MCP`, `RRSI`, `AnyJev`, `SQLite`, or `Ed25519`).
- **Punctuation**: Do **not** end the subject line with a period.

---

## 2. Allowed Commit Types

| Type | When to Use |
| :--- | :--- |
| `feat` | A new CLI command, host adapter slice, protocol operation, schema contract, or forwarding pack |
| `fix` | A bug fix, race condition fix, cursor conflict fix, redaction fix, or schema validation fix |
| `security` | Hardening loopback boundaries, credential redaction, Ed25519 verification, symlink guards, or CEL threat rules |
| `refactor` | Internal code restructuring across Go, TypeScript, or Python without changing external behavior or contracts |
| `test` | Adding or updating deterministic unit tests, Playwright replay tests, or installed-host smoke fixtures |
| `docs` | Updates to `README.md`, `docs/`, `contributor.md`, `commit.md`, `THIRD_PARTY.md`, or architecture diagrams |
| `build` | Changes to `go.mod`, `pyproject.toml`, `package.json`, `containers/engineering/`, or distribution scripts |
| `ci` | Updates to `.github/workflows/`, `test.ps1`, or composite GitHub Action definitions |
| `chore` | Repository maintenance, `.gitignore` updates, or dependency inventory regeneration |

---

## 3. Canonical Scopes

Choose the most specific scope that matches the primary boundary touched by your commit:

| Category | Scopes |
| :--- | :--- |
| **Go Core & Storage** | `cli`, `service`, `dashboard`, `mcp`, `otlp`, `store`, `sqlite`, `trace` |
| **Memory & Procedures** | `procedure`, `memory`, `skill`, `freshness`, `learning` |
| **Task Continuity & Teams** | `continuity`, `task`, `session`, `handoff`, `requirements`, `plan`, `team` |
| **Python Engines** | `anyjev`, `decision`, `distill`, `rrsi`, `evolution`, `domains` |
| **Host Adapters** | `adapters`, `browser-ext`, `claude`, `codex`, `cursor`, `gemini`, `opencode`, `antigravity`, `copilot`, `cline`, `qwen`, `devin`, `goose`, `openhands`, `senpi`, `dsh`, `qoder` |
| **Security & Operations** | `security`, `forwarding`, `inventory`, `ci-capture`, `migrate`, `cutover`, `schema` |

---

## 4. Commit Body & Evidence Standards

For non-trivial commits, include a concise body covering:

1. **What & Why**: The invariant enforced, contract added, or failure mode resolved.
2. **Privacy & Safety Boundary**: Confirm how untrusted input, file paths, credentials, or host configurations are bounded (e.g., CAS revision check, SHA-256 digest gate, loopback-only listener, or neighbor-preserving config write).
3. **Verification Status**: State which deterministic suites ran and distinguish fixture/unit verification from live installed-host or held-out benchmark verification.

### Example Commit Messages

```text
feat(procedure): enforce SHA-256 source-file freshness on /v2/context reads

Re-compute repository-relative source_files digests locally when the
service runs with --project-root and ignore caller-supplied hashes.
Stale procedures are withheld from active guidance and legacy records
without source bindings are labeled unverified.

Verified:
- go test ./internal/service/... ./internal/projectmemory/...
```

```text
feat(adapters): add digest-gated project skill installer for Command Code

Install reviewed SKILL.md exports under .commandcode/skills/<name>/SKILL.md
with exclusive ownership markers and neighboring-file preservation.
Does not modify host trust or MCP configuration.

Verified:
- cd adapters && npm run check && npm test
- Isolated CLI discovery smoke documented in docs/command-code.md
```

```text
security(forwarding): require explicit consent and directory secrets for Sumo pack

Reject presigned bearer URLs in `threadforge endpoint sumo install-pack`
and resolve tokens exclusively from Vector's directory-secrets backend
so credentials are never read or stored by Threadforge.

Verified:
- go test ./internal/forwarding/... ./internal/cli/...
```

---

## 5. Atomic Commits & Pre-Commit Hygiene

1. **One Logical Change per Commit**: Keep schema/contract additions, runtime implementation, and documentation updates logically grouped; do not mix unrelated refactors with behavioral changes.
2. **Never Commit Forbidden Artifacts**:
   - Compiled binaries or test executables (`*.exe`, `*.test`, `*.out`)
   - Local SQLite databases (`*.db`, `*.sqlite`, `*.db-wal`, `*.db-shm`)
   - Raw telemetry traces, session exports, or Playwright output directories
   - Model checkpoints or benchmark data (`*.pt`, `*.safetensors`, `*.gguf`, `*.npz`, `*.parquet`)
   - API keys, tokens, Ed25519 private keys, or customer endpoints
3. **Run the Gate Before Committing**:
   - Run `go test ./... && go vet ./...` for Go changes.
   - Run `cd python && python -m pytest -q && ruff check threadforge_py tests` for Python changes.
   - Run `cd adapters && npm run check && npm test` for TypeScript adapter changes.
   - Run `./test.ps1` before pushing a branch or opening a pull request.

---

## 6. Note on Internal Git-Ref Team Transport Commits

`threadforge team sync push` creates synthetic commits on a dedicated Git ref to exchange Ed25519-signed, privacy-reviewed procedure records. Those machine-generated ref commits use the fixed deterministic identity:

- `GIT_COMMITTER_NAME="Threadforge Team Transport"`
- `GIT_COMMITTER_EMAIL="noreply@threadforge.invalid"`

Contributors should never manually author commits onto managed `.threadforge` sync refs; always use `threadforge team sync pull|push` with `--expected-revision`.

