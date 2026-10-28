# Memory outcome feedback

Threadforge can store an explicit, local report connecting one approved memory revision to a task result. This is a data-collection aid for later review—not proof that the host displayed the memory, that an agent followed it, or that memory caused the task result.

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

The CLI generates a random event ID and appends the typed record to private SQLite schema version 13. The table is append-only and capped at 100,000 reports per project. `memory feedback list --project-id <id>` returns bounded pages (default 50, maximum 500); `memory feedback summary --project-id <id> [--max-records 10000]` returns count groups by target revision, harness, exposure, utility, and task result. A summary is explicitly `truncated` when its bound omits records.

Stored fields are limited to project/task/session identifiers, the target kind/ID/revision, exposure and utility labels, task-result label, harness, reporter label, and timestamp. No task text, memory body, prompt, trace, file path, model output, or free-form rationale is accepted. Feedback stays in the local SQLite store and is not included in team procedure publication or sync. It does not alter retrieval ranking, memory approval, procedure status, or execution permissions.

Every record and summary sets `evidence_class=self_reported`, `cutover_evidence_eligible=false`, and `execution_authorized=false`. Counts are descriptive only. Independent held-out evaluation, authenticated evaluator execution, and measured cross-harness task outcomes remain required for efficacy or cutover claims.
