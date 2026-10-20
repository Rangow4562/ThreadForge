# Engineering Design RRSI adapter status

The partial adapter in `python/threadforge_domains/engineering/adapter.py`
loads the private, source-verified EngDesign-Open / EngDesign v1 task-ID bundle,
scores bounded local `meta.json` + `verdict.json` trial pairs, applies the
validity/no-submission guards, and renders bounded traces. Trial records use
`runs/engineering/jobs/<job>/<task-id>/t<trial>/`.

`python/threadforge_domains/engineering/gateway.py` provides the per-task MCP
tool surface: `read_text_file`, `list_files`, `get_directory_tree`,
`search_files`, `write_file`, `read_pdf`, and `code_exec`. It binds to
`127.0.0.1`, checks Host/Origin headers, confines reads to the active task,
limits writes to `output/`, and parses PDFs/runs shell code only through the
digest-pinned isolated container. Execution requires
`THREADFORGE_ENGINEERING_BENCHMARK_ROOT`,
`THREADFORGE_ENGINEERING_EVALUATOR_ROOT`, and a preloaded
`THREADFORGE_ENGINEERING_EXECUTION_IMAGE`.

The 10-question read-only synthetic gateway evaluation is
`python/tests/fixtures/engineering_gateway_evaluation.xml`; its fixture data is
under `python/tests/fixtures/engineering_gateway_workspace/`. The answers are
reconciled through the gateway file/search functions in unit tests, without a
model call.

Before any future execution runner may use external tasks, create and validate
the local runtime pin using the commands in `docs/evolution.md`. The
out-of-tree `threadforge.engineering-runtime-pin.v1` file binds the exact
benchmark/evaluator tree digests, clean evaluator Git revision, private task
inventory digest, image digest, and license-review reference. The pin is a
local integrity record, not a signed approval or release evidence.

EngDesign v1 reuses the same 61 IDs as EngDesign-Open. Accordingly,
`heldout_ids()` is empty; callers may inspect `surface_ids("engdesign-v1")`,
but the generic ID-based RRSI holdout path must not confuse it with an
independent holdout.

`benchmark_execution_opt_in` defaults to `false`. With explicit opt-in,
`Domain.run()` verifies the external runtime pin and candidate harness
fingerprint, stages the task and candidate, launches the agent through the
network-disabled container runner, brokers model calls through host stdio,
starts task MCP inside the container, and runs the pinned canonical evaluator
in a separate offline container. Trial output is recorded locally with
`meta.json` written last as the resume marker; incomplete or linked prior
state is preserved and refused. `smoke()` runs the same path for one task.
Orchestration and transport are covered by mocked tests, but no real Docker,
FastMCP, model, evaluator, or benchmark run has been verified. A reproducible
Engineering image recipe is still missing, and no task data or evaluator is
bundled. Synthetic/imported fixtures are not acceptance evidence.
