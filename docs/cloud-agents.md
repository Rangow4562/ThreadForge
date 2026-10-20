# Cloud-agent access to approved Threadforge procedures

Threadforge keeps traces and personal preferences local. For a cloud-agent task that already has access to a project workspace, the safe portable fallback is an explicitly reviewed procedure file—not a remote telemetry collector or copied trace history.

## Export a procedure locally

First review that the procedure is approved and revalidate it against current source files locally. The export command does not independently recompute source fingerprints or prove freshness. Export from a workstation with an explicit privacy-review confirmation:

```powershell
threadforge procedure export-skill --project-root . --id <approved-procedure-id> --privacy-reviewed
```

The command prints a `threadforge.skill-export.v1` result containing the graph ID/revision, relative path, SHA-256, and whether the exact artifact already existed. It writes an immutable file at `.threadforge/skills/<id>/rev-NNNNNN/SKILL.md`; a different file at the same revision is a conflict, not an overwrite. Export requires an approved graph, applies the share sanitizer, rejects credential-like content, and caps output at 256 KiB.

The portable file contains the approved procedure instructions and applicability paths/modules. It omits evidence digests and local trace references, validation datasets/results, reviewer identity, and approval rationale. It states that its guidance is not authorization to execute actions. The file is a snapshot: Threadforge does not automatically fingerprint source files in the cloud workspace or mark this exported copy stale. Revalidate the source procedure locally and export a new revision when its applicable code or policy changes.

## Make it available to a cloud session

Only after reviewing the generated artifact, deliberately make that file available through the project's normal source-control or workspace-snapshot process. A cloud agent can be instructed to read the applicable `.threadforge/skills/.../SKILL.md` before acting, check its applicability against the current workspace, and surface ambiguity or conflicts. Do not add raw event logs, local trace references, credentials, personal preference files, or unapproved drafts to the cloud workspace.

This is portable file access, not automatic host injection, MCP connectivity, or an authorization mechanism. The artifact may guide reasoning but cannot grant tool permissions, waive project policy, or replace current verification.

## Cloud telemetry is intentionally not provided

The pinned Beacon Claude Code Cloud Agents integration installs hooks in the cloud sandbox, writes `/tmp/beacon/runtime.jsonl`, and uploads that log to Beacon Managed or a customer GCS/S3 bucket. Its setup also places device or object-storage credentials in the cloud environment. Threadforge does not reproduce that raw-log forwarding path: it would violate the local-trace and no-credential-sharing defaults. No Threadforge cloud hook, telemetry uploader, bucket setup, device-key provisioning, or cloud transcript importer is implemented. This remains a documented parity gap and blocks the all-or-nothing legacy cutover gate until a privacy-compliant alternative is designed and accepted.
