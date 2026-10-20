# Engineering harness patterns

## Allowed

- General constraint tracking, independent calculations, bounded validation,
  clear tool errors, and reliable deliverable writing.
- Mechanisms that remain useful across unfamiliar engineering tasks and do not
  depend on one suite's task names or toolchain.
- Explicitly measured improvements with preserved safety boundaries.

## Reject before evaluation

- Task IDs, task-specific names, numeric answers, or expected payloads in code,
  prompts, tests, or configuration.
- Reads of evaluator/reference artifacts, grading internals, hidden tests, or
  benchmark checkout paths.
- Absolute/upward path access, cross-task workspace access, or mounting a
  benchmark/evaluator tree into a code-execution container.
- Network access, package installation, credential forwarding, or any
  fallback to host shell execution when the isolated runner is unavailable.
- Harness specialization to the EngDesign name, its verifier schema, or a
  particular task's external toolchain.
