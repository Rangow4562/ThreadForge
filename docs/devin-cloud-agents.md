# Devin Cloud Agents: approved procedure access

Threadforge can place an already approved, privacy-reviewed procedure in a Devin-connected project as an Agent Skill. Devin's current documentation says it discovers `SKILL.md` files from connected repositories and cloned workspaces under `.agents/skills/<skill-name>/SKILL.md`.

Export the procedure from the local Threadforge store and inspect the exact artifact:

```powershell
threadforge procedure export-skill --project-root <project> --id <approved-procedure-id> --privacy-reviewed
```

Use the export's `path` and `sha256` to install it into the project:

```powershell
cd adapters
npm.cmd run adapter -- devin skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <exported-sha256>
```

The installer accepts only a Threadforge-generated export at `.threadforge/skills/<id>/rev-NNNNNN/SKILL.md`, checks the supplied SHA-256, and copies the exact reviewed bytes to `.agents/skills/<generated-name>/SKILL.md`. A Threadforge ownership record distinguishes this file from other integrations; conflicting, edited, unowned, symlinked, or digest-mismatched files are not overwritten. Other skills and project files are preserved.

Review the installed files, then commit and push them through the project's normal source-control workflow so the connected Devin repository can index the skill. Installation itself does not commit, push, upload, or contact Devin. To remove an unchanged install, run `devin skills uninstall` with the same project root, exported file, and digest. To publish a newer procedure revision, export and install that new revision; existing revisions are immutable and are not silently replaced.

The portable procedure contains approved advisory guidance only. It does not grant tool permissions, override project rules, or authorize execution. The export omits local trace references, evidence digests, validation datasets/results, reviewer identity, and approval rationale. Revalidate the procedure against current source files before export; the exporter does not independently recompute its source fingerprints.

## Local-only cloud session telemetry

Threadforge also provides a read-only Devin v3 organization API poller. Credentials must be reauthorized by the operator and supplied through environment variables; they are not migrated from Beacon, saved by Threadforge, or written to project files:

```powershell
$env:DEVIN_API_KEY = '<your Devin API key>'
$env:DEVIN_ORG_ID = '<your organization ID>'
threadforge cloud devin status
threadforge cloud devin pull --print
threadforge cloud devin pull
```

The API key needs permission to list the organization's sessions and read their messages. Devin applies its role and organization access controls. `pull --print` previews canonical events without writing the trace or cursor. A normal pull appends to Threadforge's private local trace JSONL and keeps a separate cursor at `<Threadforge data root>/state/devin-cloud.v1.json`; event, user, session, and cursor identities are stable SHA-256 aliases. Repeated pulls skip unchanged sessions and deduplicate known event IDs. `--full-resync` fetches sessions again but does not intentionally duplicate already recorded events. `--watch --interval 30s` repeats the bounded sweep until interrupted.

Capture is digest-only by default: prompts and responses are not included in stored event bodies. `--include-content` is an explicit opt-in that retains secret-redacted prompt/response text locally, capped at 2 KiB per message. Titles, status details, session URLs, repository URLs, and raw user/session/event IDs are omitted. Each API response is capped at 16 MiB, aggregate session metadata at 64 MiB, and messages for one session at 128 MiB. A sweep defaults to at most 1,000 sessions, each session defaults to at most 10,000 messages, and hard limits are 5,000 sessions and 50,000 messages per session. Hitting a limit or an invalid page cursor reports an incomplete pull rather than silently treating the data as complete. Individual session failures are reported and retried on a later sweep.

Threadforge does not upload Devin traces to GCS or another remote destination, configure SIEM forwarding for these events, or provision cloud credentials. Those Beacon capabilities remain outside this local-only connector and block full documented parity until a privacy-compatible alternative is designed and accepted. Live Devin API permission/version coverage, live cloud Agent Skill indexing, and cross-harness retrieval have not yet been acceptance-tested. See [source matrix](source-matrix.md), [feature parity](feature-parity.md), and [cutover gate](cutover.md).
