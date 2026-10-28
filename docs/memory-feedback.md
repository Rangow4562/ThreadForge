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

`threadforge_procedure_get`, `threadforge_procedure_guide`, `threadforge_project_memory_read`, and `threadforge_project_memory_read_v2` accept optional `task_id` and `session_id`. `threadforge_context_get` accepts the same opt-in fields and returns `retrieval_receipts`, one per exact procedure or project-memory revision included in its bounded result. Context receipts are stored atomically as one task-scoped batch; per-memory freshness is included in the context response. Procedure targets use `threadforge.memory-retrieval-receipt.v1`; project-memory targets use v2, which also records freshness and target-result status. Pass the matching receipt ID as `--retrieval-receipt-id` when recording feedback; if the receipt has a `session_id`, pass the same `--session-id`. The receipt records that the MCP handler prepared a result; it cannot verify transport delivery, host display, model exposure, interpretation, or use. Receipts that withheld guidance cannot be linked to feedback. Context results that withhold a target or resolve no revision produce no receipt for that target. Omitting `task_id` preserves prior MCP behavior and creates no receipt; `session_id` without `task_id` is rejected.

Automatic prompt-memory hooks also write local receipts when `--task-id` successfully resolves inside the configured project, or when `--discover-active-task` finds exactly one eligible task. These `threadforge.memory-retrieval-receipt.v3` records identify the host (`claude-code`, `codex-cli`, `gemini-cli`, `cursor`, `qwen-code`, `antigravity`, `opencode`, or `copilot-cli`) and use `observation_boundary=hook_response_prepared`. A receipt is recorded per returned procedure or project-memory revision; `guidance_included` is true only when the complete formatted item survives the final bounded hook context and the host response carries that context. The hook never places receipt IDs in host output. Use `threadforge memory feedback receipts --project-id <id> --task-id <id>` to inspect content-free IDs, then pass a matching ID with `memory feedback record --retrieval-receipt-id <id>`. For explicit session selection, also pass the same `--session-id`; unique-task discovery does not attach an inferred session. No task selection, failed/mismatched task resolution, or ambiguous discovery creates hook receipts. These records mean only that a local hook response was prepared—not that a host delivered it, a model saw it, or anyone used it.

The CLI generates a random event ID and appends the typed report to private SQLite schema version 14. Feedback and retrieval-receipt tables are append-only and each is capped at 100,000 records per project. `memory feedback list --project-id <id>` and `memory feedback receipts --project-id <id> --task-id <id>` return bounded pages (default 50, maximum 500); `memory feedback summary --project-id <id> [--max-records 10000]` returns count groups by target revision, harness, exposure, utility, and task result, plus the count linked to a retrieval receipt. A summary is explicitly `truncated` when its bound omits records.

Stored fields are limited to project/task/session identifiers, an optional receipt ID, the target kind/ID/revision, exposure and utility labels, task-result label, harness, reporter label, and timestamp. Receipts contain only identifiers, revision, retrieval tool, host where applicable, freshness, handler result status where applicable, guidance-inclusion status, observation boundary, and timestamp. No task text, memory body, prompt, trace, file path, model output, or free-form rationale is accepted. Feedback and receipts stay in the local SQLite store and are not included in team procedure publication or sync. They do not alter retrieval ranking, memory approval, procedure status, or execution permissions.

Every record and summary sets `evidence_class=self_reported`, `cutover_evidence_eligible=false`, and `execution_authorized=false`. Counts are descriptive only. Independent held-out evaluation, authenticated evaluator execution, and measured cross-harness task outcomes remain required for efficacy or cutover claims.
