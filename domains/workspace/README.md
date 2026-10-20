# Harvey LAB workspace adapter

This is a partial RRSI adapter for the pinned Harvey LAB workspace benchmark.
It uses the private, source-verified 120-task evolve / 40-task held-out bundle;
task IDs, task documents, credentials, and run traces are not committed here.
The adapter, driver, judge wrapper, gateway, and briefs derive from RRSI commit
`be50316e1db05914068a973f322770ef08ed7ba1`; the Archipelago subtree is
vendored at `third_party/archipelago/` with its Apache-2.0 license.

## Local prerequisites

- A clean Harvey LAB checkout at commit
  `1da4750171bc5a534960b3d82d15ba7fd2cf653f`; the `tasks/` tree must not
  contain modified, untracked, or ignored files.
- `HARVEY_PY` pointing to that checkout's evaluation virtual environment.
- `RRSI_AGENT_PYTHON` pointing to Python with Threadforge's optional
  `workspace` dependencies installed.
- Docker with a **preloaded** code-execution image identified by immutable
  `sha256` digest through `THREADFORGE_CODE_EXEC_IMAGE`. The runner never
  pulls an image. The image needs shell/Python and the office/PDF libraries
  used by Harvey tasks.
- A local OpenAI-compatible inference service. The default is
  `http://127.0.0.1:8000/v1` with model `threadforge-local`.
- The official Harvey rubric judge is remote Vertex inference. It requires
  explicit `remote_evaluation_opt_in=true`, a `VERTEX_PROJECT`, and valid
  local Google credentials. Policy-model remote inference is a separate
  setting and remains off by default.

## Consent and run behavior

All benchmark and smoke execution requires `benchmark_execution_opt_in=true`.
Full scoring also requires `remote_evaluation_opt_in=true` because the official
Harvey judge sends deliverables to Vertex. Remote policy inference requires
`remote_inference_opt_in=true`, `THREADFORGE_EVALUATION_BACKEND=remote`, and
an explicit `THREADFORGE_REMOTE_EVALUATION_MODEL`; otherwise the policy model
uses only the loopback endpoint. Missing local inference or Docker fails
closed without remote fallback.

Each concurrent task receives a separate loopback MCP gateway. File reads are
limited to that task's workspace and direct writes to its `output/` directory.
`code_exec` runs in the configured image with no network, no forwarded
environment credentials, read-only documents, and a writable output mount.
Web search is not registered. Partial jobs and workspaces are preserved rather
than silently deleted or overwritten. Task manifests, logs, trajectories,
deliverables, and rubric results remain in the caller's local run directory.

The runner is POSIX-oriented and the actual Harvey/Vertex/Docker path has not
been exercised in this environment. The adapter currently has synthetic
inventory/scoring tests only; it is not evidence of benchmark reproduction,
held-out efficacy, or release acceptance. Workspace OOD adapters for JobBench,
GDPval, and APEX-Agents remain incomplete.

## Python package distribution

From the `python/` directory, install the optional workspace dependencies with
`python -m pip install '.[workspace]'`. The custom setuptools build includes
the workspace runner scripts and the pinned `third_party/archipelago` runtime
under `threadforge_domains/workspace/runtime/`, so the adapter can fall back
to installed package assets when a candidate worktree does not contain the
source-tree runtime paths. The build copies only source/runtime files, the
upstream Apache-2.0 licenses, AnyJev credits, and the modification audit;
Harvey tasks, derived IDs, run results, local credentials, and model data are
excluded. On source distributions, the runtime
assets are staged into the archive and removed from the checkout after the
build.
