# Task requirements workflow

Tasks created by the CLI start with `threadforge.task-requirements.v1` in `draft` state. Requirements include the task objective and acceptance criteria plus a revisioned clarification log.

```powershell
threadforge task requirements revise --id fix-parser --expected-revision 1 --objective "Restore nested quote handling" --criterion "Nested quote fixture passes" --criterion "Existing parser tests remain green" --actor owner --reason "Clarify expected behavior"
threadforge task requirements ask --id fix-parser --expected-revision 2 --question-id malformed-input --question "How should malformed nested quotes be handled?" --actor agent --reason "Clarification needed before implementation"
threadforge task requirements answer --id fix-parser --expected-revision 3 --question-id malformed-input --answer "Return the documented parse error and preserve the input." --actor owner --reason "Record product decision"
threadforge task requirements finalize --id fix-parser --expected-revision 4 --actor owner --reason "Objective, acceptance criteria, and clarifications reviewed"
```

Every mutation uses the parent task revision as a compare-and-swap token and creates a task-history snapshot plus an audit event. `revise` changes the objective and/or replaces the full acceptance-criteria list; it can be used only before finalization. `ask` appends a stable-ID question, `answer` records one immutable answer, and `finalize` refuses while any question is unanswered. Questions, answers, objective, criteria, actor, and reason are bounded and credential-scanned.

Without a team policy, the reviewer value is a local audit label. For a registered team project, finalization requires `continuity.write` and `continuity.requirements.finalize` from a trusted identity; the policy subject replaces the supplied actor label and its signed authorization is retained with the audit revision. The local store retains the policy snapshot for historical signature verification; the administrator's out-of-band source digest remains the trust anchor.

Finalized requirements must be explicitly reopened before any change. Reopening preserves the clarification history and clears finalization metadata. Any requirements mutation also clears a prior plan decision so a stale approval cannot survive a changed task definition. A new `governed_execution` plan decision requires finalized requirements and a non-empty implementation include scope. `plan_only` remains available while requirements are draft or clarifying.

These readiness and scope records are auditable planning controls only. They do not sandbox filesystem access, authorize tools, or override harness policy. Legacy task records without a requirements object remain readable; they must complete this workflow before receiving a new governed-execution confirmation.

See the [requirements schema](../schema/task-requirements-v1.schema.json), [continuity schema](../schema/continuity-v1.schema.json), and [task-plan schema](../schema/task-plan-v1.schema.json).
