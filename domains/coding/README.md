# Threadforge RRSI coding domain

This adapter evaluates an evolvable Terminus-2 agent fork on Terminal-Bench 2.1 and uses SWE-bench Verified as its held-out split. It preserves trial-level rewards and token counts for the RRSI scorer.

## Local prerequisites

- A private `threadforge.rrsi-domain-inventory-bundle.v1` file with the pinned coding evolve and held-out inventories. Set `THREADFORGE_RRSI_INVENTORY_BUNDLE` to its absolute path. Keep the file outside the repository; it contains benchmark task identifiers.
- A Linux environment with Docker, Bash, a Harbor CLI virtual environment, and access to the benchmark datasets under their applicable terms. Threadforge does not bundle the Harbor CLI, benchmark data, issue text, task images, or provider credentials.
- Set `RRSI_CODING_VENV` to the Harbor virtual environment used by `domains/coding/scripts/run_eval.sh`. Optionally set `RRSI_CODING_PYTHON` to the Python executable in that environment for the smoke constructor check.
- Enable benchmark execution explicitly in the reviewed local RRSI configuration with `benchmark_execution_opt_in=true`. Without it, RRSI refuses baseline, smoke, candidate, held-out, and re-evaluation execution before side effects.

## Evaluation model

Evaluation uses a loopback OpenAI-compatible model by default:

- `THREADFORGE_EVALUATION_BACKEND=local` (the default)
- `THREADFORGE_LOCAL_MODEL_BASE_URL=http://127.0.0.1:8000/v1` (the default; must remain loopback)
- `THREADFORGE_LOCAL_EVALUATION_MODEL=threadforge-local` (the default model ID)

The adapter strips inherited API keys, tokens, and cloud credentials from the local runner environment. Remote inference requires all of the following: `remote_inference_opt_in=true`, `THREADFORGE_EVALUATION_BACKEND=remote`, and an explicit `THREADFORGE_REMOTE_EVALUATION_MODEL`. Remote provider credentials must be supplied by the operator; Threadforge does not migrate or persist them.

Completed Harbor jobs are reused only when every requested task has exactly `k` non-infrastructure trials, each with a numeric finite verifier reward in `[0, 1]`. A numeric reward of `0` is a valid failed trial; a missing reward is an incomplete result and is rejected by both job validation and scoring. Missing trial directories remain zero-filled in the denominator and are reported as missing. An incomplete job path is preserved and causes an error; choose a new job ID or manually review the existing artifacts instead of having the adapter rename or overwrite them.

The Python adapter and bundled Terminus-2 fork derive from RRSI commit `be50316e1db05914068a973f322770ef08ed7ba1`. Adapter tests use synthetic result artifacts and make no Harbor, Docker, network, or model calls. Live host/dataset compatibility and held-out efficacy remain unverified.
