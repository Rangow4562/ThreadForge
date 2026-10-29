# Goose approved procedure materialization

Goose's automatic project-memory retrieval is not currently verified. Threadforge therefore offers an explicit portable fallback: copy one approved, sanitized procedure export into the project's `.goosehints` as quoted advisory text. This does not configure Goose, grant trust, activate a procedure, or authorize execution.

The source must be an immutable export at `.threadforge/skills/<id>/rev-NNNNNN/SKILL.md` inside the selected project. The installer requires an expected lowercase SHA-256 digest and the explicit `--privacy-reviewed` acknowledgement. It rejects paths outside that layout, invalid UTF-8, credentials, reserved markers, and files over 24 KiB. At most eight digest-distinct exports can be present. The source digest is retained in managed markers; raw traces, evidence references, and reviewer details are not added to the Goose file.

Preview first, then install:

```powershell
$project = (Resolve-Path .).Path
$skill = Join-Path $project '.threadforge/skills/my-procedure/rev-000001/SKILL.md'
$digest = (Get-FileHash -Algorithm SHA256 $skill).Hash.ToLowerInvariant()

npm.cmd run adapter -- goose memory plan --project-root $project `
  --skill-file $skill --sha256 $digest --privacy-reviewed
npm.cmd run adapter -- goose memory install --project-root $project `
  --skill-file $skill --sha256 $digest --privacy-reviewed
```

`status` and `uninstall` take the same `--project-root` and `--sha256`; they do not need the source export. `plan` validates the supplied export and arguments but does not inspect `.goosehints`. Uninstall removes only an unchanged Threadforge-managed entry. Edited or malformed managed content fails closed. User-owned `.goosehints` content, UTF-8 BOM, and line endings are preserved. The command output contains digests and bounded counts only, never procedure text or file paths.

Install also adds the generic advisory Threadforge guidance block when it is absent. That guidance limits portable procedure lookup to immutable approved exports and warns that memory is not execution permission. Either managed block can be removed independently. No global Goose configuration, MCP server, hooks, trust settings, or credentials are changed.

Every operation reports `execution_authorized: false`, `host_discovery_verified: false`, and `manual_freshness_review_required: true`. Inspect the resulting `.goosehints` and verify the procedure's scope, source freshness, and current project policy before relying on it. Fixture tests prove file-management behavior only; installed Goose discovery, prompt use, and cross-harness task outcomes remain unverified. The versioned response contract is [goose-procedure-bootstrap-v1](../schema/goose-procedure-bootstrap-v1.schema.json).
