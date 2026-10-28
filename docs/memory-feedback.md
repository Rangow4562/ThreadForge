# Memory outcome feedback

Threadforge can store an explicit, local report connecting one approved memory revision to a task result. This is a data-collection aid for later review—not proof that a host or model displayed, received, followed, or used the memory, or that memory caused the task result.

```powershell
threadforge memory feedback record `
  --project-root C:\work\repo `
  --project-id repo:example `
  --task-id task-123 `
  --target-kind procedure `
  --target-id build-recovery `
  --target-revision 2 `
  --exposure used `
  --utility helpful `
  --task-result passed `
  --harness codex-cli
```

`--target-kind` accepts `procedure`, `fact`, `decision`, or `policy`. The project/task must match, the target must exist as the named approved revision in the local SQLite or project-file store, and an optional `--session-id` must belong to that same task and project. A `used` report requires utility `helpful`, `neutral`, or `harmful`; `shown_not_used` requires `not_assessed`. Task results are `passed`, `failed`, or `inconclusive`.

`threadforge_procedure_get`, `threadforge_procedure_guide`, `threadforge_project_memory_read`, and `threadforge_project_memory_read_v2` accept optional `task_id` and `session_id`. With those fields, the local MCP server validates task/project/session scope and returns a receipt for a resolved target revision. Procedure reads use `threadforge.memory-retrieval-receipt.v1`; exact project-memory reads use v2, which also records the read status and whether guidance was included. Pass the returned ID as `--retrieval-receipt-id` when recording feedback; if the receipt has a `session_id`, pass the same `--session-id`. The receipt records that the MCP handler prepared a result; it cannot verify transport delivery, host display, model exposure, interpretation, or use. Receipts that withheld guidance cannot be linked to feedback. Reads that resolve no revision return no receipt. Omitting `task_id` preserves prior MCP behavior and creates no receipt.

The CLI generates a random event ID and appends the typed report to private SQLite schema version 14. Feedback and retrieval-receipt tables are append-only and each is capped at 100,000 records per project. `memory feedback list --project-id <id>` returns bounded pages (default 50, maximum 500); `memory feedback summary --project-id <id> [--max-records 10000]` returns count groups by target revision, harness, exposure, utility, and task result, plus the count linked to a handler receipt. A summary is explicitly `truncated` when its bound omits records.

Stored fields are limited to project/task/session identifiers, an optional receipt ID, the target kind/ID/revision, exposure and utility labels, task-result label, harness, reporter label, and timestamp. Receipts contain only identifiers, revision, retrieval tool, freshness, handler result status where applicable, guidance-inclusion status, observation boundary, and timestamp. No task text, memory body, prompt, trace, file path, model output, or free-form rationale is accepted. Feedback and receipts stay in the local SQLite store and are not included in team procedure publication or sync. They do not alter retrieval ranking, memory approval, procedure status, or execution permissions.

Every record and summary sets `evidence_class=self_reported`, `cutover_evidence_eligible=false`, and `execution_authorized=false`. Counts are descriptive only. Independent held-out evaluation, authenticated evaluator execution, and measured cross-harness task outcomes remain required for efficacy or cutover claims.
