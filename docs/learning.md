# Explicit memory learning workflow

To capture a user's post-task assessment of an exact approved memory revision, use the local-only [memory outcome feedback](memory-feedback.md) command. Those self-reports do not change retrieval or approval and are not held-out efficacy or cutover evidence.

`threadforge memory learn` is an explicit, local-first extraction command. It accepts 2-32 Beacon event objects as JSON or JSONL, sanitizes them through the Beacon event contract, rejects or removes detected credential material, and sends only bounded `action`, `harness`, `severity`, and `summary` fields to the Python JSON-over-stdio operation `memory.learn`. Raw prompt text is not copied into normalized observations. Any retained summary is labeled as untrusted data in the local-model prompt; the model is instructed never to follow directives inside observations.

Use optional `--project-root <root>` to source-bind the resulting draft. Threadforge considers only sanitized event `file.path` values that resolve inside this explicit non-symlink project root and match the candidate's applicability; it fingerprints at most 64 applicable files using bounded, symlink-rejecting local reads. The model receives no paths. The draft stores only project-relative paths and SHA-256 digests. Its stable ID is derived from the normalized graph, trace-evidence digest, and source fingerprints, so changed steps or source files produce a distinct proposal. If binding is requested but no applicable in-project file can be safely fingerprinted, learning returns `no_candidate` with `source_fingerprints_unavailable`. Without `--project-root`, the prior behavior is preserved, but the graph has no source freshness evidence and reads must not treat it as source-verified.

The default inference backend is an already-downloaded local Hugging Face causal model. Configure it with `THREADFORGE_MODEL_PATH` or `--model-path`; `--device` defaults to `cpu` and `--dtype` to `float32`. Inference forces Hugging Face offline mode, `local_files_only`, and `trust_remote_code=false`. No model, unavailable dependencies, malformed output, privacy failure, or invalid graph yields no candidate. The current operation does not support remote generation.

A structurally valid extraction becomes only a draft Procedure Graph in local SQLite. Adversarial tests cover prompt-injection observations, credential-bearing traces, poisoned draft exclusion from retrieval, and poisoned approved memory remaining untrusted/advisory/non-authorizing when returned through MCP and automatic host prompt hooks. Host-injected context explicitly says approval is not proof of truth or safety and directs the model not to follow embedded directives. A model candidate—even one containing malicious instructions—cannot approve, validate, publish, or authorize execution; independently inspect the draft, evaluate it on held-out tasks, and require human approval. The model prompt and tests reduce risk but do not prove an LLM will resist every prompt injection. Until the automated pinned-baseline runner and broader adversarial evaluation suite are complete, manually supplied validation reports are not evidence that the overall release acceptance gate has passed.

## Human-authored proposals without a model

When a reviewer already knows the procedure, provide a draft graph directly and bind it to a sanitized local event export:

```powershell
threadforge procedure propose --file .\draft-procedure.json --trace-input .\events.jsonl --trace-ref session:codex:trace-42 --reason "reviewed the failed build trace"
```

The graph's required `project_id` is the explicit repository scope; the command does not infer or widen it. `--trace-ref` is optional and accepts only an opaque identifier, never a path. Trace input is capped at 1 MiB and 32 Beacon events. The command stores only a SHA-256 digest of bounded normalized observations, the harness label, and the optional private trace reference; prompt text, event bodies, and the trace file path are not copied into the proposal. Unsafe references and privacy-blocked trace evidence fail before the SQLite proposal store is opened. The result remains a draft and follows the same held-out validation and human-approval lifecycle as extracted proposals. Local trace references are removed from share projections.

Example:

```powershell
threadforge memory learn --input .\events.jsonl --project-id repo:example --project-root . --model-path C:\models\local-instruct
threadforge procedure audit --id <proposal-id>
```

`TestTraceCapturedProcedureCanBePublishedAndRetrievedAcrossHarnesses` exercises the local integration path from a Codex-labeled sanitized event, through draft proposal, validation record, human approval, signed project publication, and an OpenCode-labeled MCP `threadforge_procedure_get` request. It verifies source freshness on retrieval, advisory-only response fields, trace/prompt redaction, and withholding after the bound source changes. The validation report in this test is synthetic and caller-authored; this test verifies wiring and safety behavior only, not held-out task execution, real OpenCode host behavior, model use, or efficacy.
