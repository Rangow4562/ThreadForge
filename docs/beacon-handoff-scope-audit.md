# Beacon session-handoff scope audit

Audit date: 2026-10-09. This is a read-only comparison of the local Beacon source and Threadforge's current adapter inventory. It does not select a new Beacon baseline or authorize launching a harness.

## Source boundary

Threadforge's general Beacon reuse pin remains `5303224c63ddb73b7c2a125278f4233d807348d9`; this does not change that pin for other components. For the handoff capability specifically, clean local Beacon HEAD `c8d56ada361eb7cb4d1eae1fe7b0e2fe69f36558` is the chosen behavior and source reference. Between those revisions, the handoff-related source/docs delta is 40 files and 7,888 insertions. Beacon's current `cli/beacon/internal/handoff/runtimes.go` registers 14 native session-store readers; `docs/cli/handoff.mdx` also documents nine runtimes that can continue from runtime-log sessions without a native reader. The current Beacon `LICENSE` is MIT, while Threadforge-authored code is Apache-2.0. The selected native readers are now vendored under `third_party/beacon-sessionreaders`; exact copied scope and modifications are recorded in `THIRD_PARTY.md`.

## Native-store overlap

| Beacon handoff source | Threadforge history-poller overlap | Handoff parity still missing |
|---|---|---|
| Claude Code: `~/.claude/projects` | `adapters/src/claude-history.ts` | Safe resumeability checks, preview/confirmation, launch, and live source/runtime validation |
| Codex CLI: `~/.codex/sessions` | `adapters/src/codex-rollouts.ts` | Beacon-equivalent grouping validation, safe resume planning, confirmation, launch, and live validation |
| OpenCode: SQLite and legacy file store | `adapters/src/opencode-history.ts` (CLI export path; no direct DB read) | Safe resume planning/confirmation, launch, and live validation |
| Cline: `~/.cline/data` | `adapters/src/cline-history.ts` | Safe resume planning/confirmation, launch, and live validation |
| Pi: `~/.pi/agent/sessions` | `adapters/src/pi-history.ts` | Safe resume planning/confirmation, launch, and live validation |
| Prime Agent: `sessions` and `session-artifacts` | `adapters/src/prime-history.ts` | Parent/subagent continuation semantics, safe resume planning, confirmation, launch, and live validation |
| Factory Droid: `~/.factory/sessions` | `adapters/src/factory-history.ts` | Safe resume planning/confirmation, launch, and live validation |
| GitHub Copilot CLI: `~/.copilot/session-state` | `adapters/src/copilot-history.ts` | Safe resume planning/confirmation, launch, and live validation |
| Hermes Agent: `~/.hermes/state.db` | None found in `adapters/src/` | Safe resume planning/confirmation, launch, and live validation |
| DeepSeek Harness: `$DSH_HOME/sessions` | `adapters/src/deepseek-history.ts` | Safe resume planning/confirmation, launch, and live validation |
| fx: `~/.fx/sessions` | `adapters/src/fx-history.ts` | Safe resume planning/confirmation, launch, and live validation |
| Grok Build: `~/.grok/sessions` | `adapters/src/grok-history.ts` | Safe resume planning/confirmation, launch, and live validation |
| OpenClaw Gateway: per-agent `sessions` | `adapters/src/openclaw-history.ts` | Safe resume planning/confirmation, launch, and live validation of per-agent identity |
| Cursor: project transcripts and global `state.vscdb` | `adapters/src/cursor-history.ts`, `adapters/src/cursor-composer.ts` | Safe resume planning/confirmation, launch, duplicate/source reconciliation, and live validation |

The pollers are not interchangeable with handoff readers: they normalize bounded events for local telemetry, often retain prompt/output content only as digests by default, and use host-specific cursor/version behavior. They do not establish that a session is resumable or preserve the runtime's full private source record.

Beacon's current log-only continuation targets are Oh My Pi, Gemini CLI, Qwen Code, Kiro, Antigravity CLI, Devin CLI, Muse Code, OpenHands, and goose. Beacon documents Kimi Code and Senpi as not startable by this command. Matching that matrix would therefore require more than adding the 14 native readers.

## Capability delta

| Capability | Beacon current behavior | Threadforge current evidence |
|---|---|---|
| List/discover | Reads native stores, deduplicates/groups runtime-specific records, supports runtime/store/directory filters, and reports per-source warnings | `threadforge handoff list` reads 14 stores via selected Beacon readers. Shared scan budgets cap each store at 10,000 directory entries, each directory at 10,000 entries, recursive depth at 24, metadata at 1 MiB/file and 64 MiB/store; Cline, DSH, OpenCode, and Hermes apply documented source-specific caps. Partial results carry path-free `scan_truncated` warnings. `--limit` remains an additional response cap, not the I/O budget |
| Export | Builds a Markdown brief from the session store or a lower-fidelity runtime-log fallback; caps at 48 KiB, keeps the last 30 activity entries, and caps entry content at 1,200 runes | `threadforge handoff export --source native --harness <runtime>` reads normalized events from the selected local session store under a 64 MiB aggregate source budget and 10,000-event cap; DSH expansion is capped at 128 MiB. The default `runtime-log` source preserves the local-trace fallback. Both feed the same redacted 48 KiB brief renderer |
| Brief privacy | Local handoff directory is mode 0700; new files mode 0600; existing files are not overwritten; content is redacted/truncated before rendering | Runtime-log and native-store sources share the re-redacting 48 KiB renderer and private, exclusive-create writer; content reaches stdout only with explicit `--print`; symlink/network/insecure output paths are refused |
| Resume | Resolves native reopen versus new-session continuation, checks runtime availability and working directory, prints a plan, and requests confirmation before launch; headless launch requires an explicit yes flag | `threadforge handoff resume <id> --harness <runtime>` chooses a native reopen only when source metadata and target behavior allow it; otherwise it prepares a bounded private brief for one of 20 startable CLIs. `--print --json` uses a versioned plan without writes/launch; real launch requires interactive confirmation or explicit `--yes`. Hermes/fx are native-only; Cursor native reopen is not selected. These plans have fixture coverage but no live host/version validation |
| Session link | New-session prompt can carry a `[beacon-handoff ...]` marker; Beacon explicitly treats it as a claim, not verified identity | Threadforge now appends a bounded marker to brief-based continuation prompts and labels the plan `marker_emitted_unverified`; all 14 vendored native readers parse observed markers into `session.handoff`, and a bridge test verifies the fields survive native read. No identity verification is implied; live cross-harness event-link acceptance remains open |

Threadforge's existing `beacon.session-handoff` row is a mandatory cutover requirement in `pkg/cutover/requirements.go`. Threadforge now supports bounded native listing, native-store/runtime-log brief export, and fixture-tested resume planning/confirmed launch. Live runtime compatibility, full source parity, and cross-harness task/link acceptance remain open; the exact status is recorded in `docs/feature-parity.md`.

## Scope decision and remaining parity

The parity target changes both implementation and provenance:

1. Handoff parity uses current clean Beacon HEAD `c8d56ada361eb7cb4d1eae1fe7b0e2fe69f36558` as its implementation reference; the general reused-source pin remains unchanged.
2. The implementation is intentionally incremental: bounded native list/export and preview-first resume/launch are present, but some native reopen modes, live compatibility, and cross-harness acceptance remain open.
3. Beacon's nine log-only continuation targets remain additional source/runtime scope; supporting the 14 native readers alone is not full documented handoff parity.

Regardless of target, memory retrieval must remain advisory. Resume must be preview-first and must not launch an agent without an explicit human confirmation; no stored prompt, handoff marker, or procedure may grant execution authorization.

## Threadforge implementation update — October 9, 2026

Threadforge first added `threadforge handoff export <session-id>` for the lower-fidelity local runtime-log fallback. It creates a redacted brief under a private, non-overwriting local path or emits content only with explicit `--print`. The existing general Beacon reuse pin remains unchanged.

The same day, Threadforge added a read-only `threadforge handoff list` path for all 14 current Beacon native-store readers and a `threadforge handoff export --source native --harness <runtime>` path that reads mapped native events under a 64 MiB aggregate source budget and a 10,000-event cap (128 MiB expanded cap for DSH), strips raw source maps, and reuses the redacted private brief writer. The selected MIT parser packages are sourced from clean Beacon HEAD `c8d56ada361eb7cb4d1eae1fe7b0e2fe69f36558` in `third_party/beacon-sessionreaders`; a narrow public bridge keeps Threadforge outside Beacon's Go `internal` import boundary. Shared per-store budgets bound directory traversal and listing metadata; partial list scans surface a path-free `scan_truncated` warning. After native export and resume implementation, the full `test.ps1` gate passed 21/21: 0 failed, 0 blocked, and 0 failed/skipped gates. Internal suites reported Python 578 passed/4 skipped, TypeScript adapters 401 passed/9 skipped, browser extension 198 unit tests plus 12 Chromium replay tests passed, with Firefox build/lint/package smoke and installed-skill discovery passing. Go tests/vet and vendored Beacon/native-reader tests passed. Four fx rule-pack integration tests remain skipped in the vendored-reader subset because the full Beacon `rules/` tree is not vendored; they are outside the 21-gate summary. Native marker parsing in the vendored mappers was regression-tested through the bridge.

Threadforge then added `threadforge handoff resume <id> --harness <runtime>`. It supports native reopen planning when the selected reader provides a still-existing compatible reference, otherwise a brief-based new session; it caps and privately writes the brief only after an interactive confirmation or explicit `--yes`. `--print --json` is read-only and uses `threadforge.handoff-resume-plan.v1`. The target matrix includes 20 startable CLIs, pins documented approval-safe modes, and uses direct argument-vector process launch. Focused resume, native bridge, and schema tests pass, and the full gate is green after these changes. Cursor native reopen, actual host/version behavior, successful real task continuation, and observed end-to-end link event remain open.
