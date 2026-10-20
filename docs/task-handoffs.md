# Local task handoffs

Threadforge implements the Mancode-documented task handoff states in its private continuity SQLite store:

```text
draft → offered → accepted | rejected | cancelled
```

Create a task with an explicit owner (the authenticated creator), acquire an active claim for that owner, then draft and offer the transfer:

```powershell
threadforge task handoff draft --id <handoff-id> --task-id <task-id> --expected-task-revision <n> `
  --claim-id <claim-id> --expected-claim-revision <n> --baseline-sha256 <digest> `
  --to-owner <recipient> --checkpoint-entry-id <entry-id> `
  --checkpoint-summary "Current progress and next verification" --actor <owner>
threadforge task handoff offer --id <handoff-id> --task-id <task-id> --expected-revision 1 --actor <owner>
```

The draft binds the current task revision, active claim revision, caller-supplied baseline digest, recipient, and bounded checkpoint. The baseline digest is a caller commitment; Threadforge does not independently hash the worktree. Drafting and offering fail if the task or claim is stale or the claim lease has expired.

The recipient accepts or rejects; the current owner may cancel a draft or offer:

```powershell
threadforge task handoff accept --id <handoff-id> --task-id <task-id> --expected-revision 2 `
  --actor <recipient> --reason "Accept implementation ownership"
threadforge task handoff reject --id <handoff-id> --task-id <task-id> --expected-revision 2 --actor <recipient>
threadforge task handoff cancel --id <handoff-id> --task-id <task-id> --expected-revision 2 --actor <owner>
threadforge task handoff show --task-id <task-id> --id <handoff-id>
threadforge task handoff list --task-id <task-id>
```

Acceptance is one SQLite transaction. It compare-and-swaps the task head and owner, transfers the referenced claim to the recipient while preserving its scope, baseline, status, and existing expiry, inserts the checkpoint, appends the accepted transition, and records the task audit revision. The lease is not implicitly renewed. If any write fails, all records roll back. A stale task/claim revision, expired lease, wrong recipient, duplicate event, or checkpoint ID collision fails closed. Rejection and cancellation only append their terminal handoff event; they do not change task ownership or claim state.

For a registered team project, the handoff entry, task-head revision, claim transfer, and checkpoint each require their own signed `continuity.write` authorization. `threadforge team audit verify` replays handoff transitions and checks that each accepted record links to its task revision, transferred claim revision, and checkpoint. Reads always report `execution_authorized=false`; a handoff is coordination evidence, never permission to execute tools.

Task and claim state remain local. `threadforge team sync` continues to transfer only explicitly published, approved, privacy-reviewed procedure records; it does not synchronize handoffs, tasks, claims, checkpoints, identities, or raw traces. Historical Mancode claim, handoff, and task-head files can be staged as typed, privacy-reduced, inert migration summaries. This does not import claim leases, actor identities, task ownership, transport receipts, or authority, and populated-source reconciliation remains incomplete; the new lifecycle is not migration evidence or complete Mancode feature parity.
