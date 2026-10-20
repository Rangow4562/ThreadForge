# Typed decisions and calibration artifacts

AnyJev owns typed question validation, decision scoring, and L1/L2 calibration behind `threadforge.python.v1` JSON-lines. Go owns the local artifact lifecycle. The Python process does not read or write SQLite.

## Calibrate and review

Prepare a `threadforge.decision-calibration.v1` JSON file using the [calibration schema](../schema/decision-calibration-v1.schema.json). It contains one typed question, labeled examples, a decision level, and optionally a backend object. L1 requires at least two labeled examples. L2 requires a hidden-state-capable backend and at least `max(8, 2 × option_count)` labels; the default is local HF, and an explicitly consented vLLM backend can also provide final-layer pooled features.

L2 requests may include `fit_options`, forwarded to AnyJev `Decider.fit_head`: `layers` (default `null`), `kinds` (default `["lda", "ridge"]`), `n_folds` (default `5`), `seed` (default `0`), and `listing` (default `"auto"`; allowed values are `auto`, `random`, and `canonical`). `n_folds` is bounded to 2–10 and `seed` to an unsigned 32-bit integer; layer and head selections are validated before backend creation. These options are L2-only; L1 requests must omit them. The result follows [`threadforge.decision-calibration-result.v1`](../schema/decision-calibration-result-v1.schema.json), echoes normalized L2 options, and remains a review-required candidate—not approval or execution authorization.

Run calibration with an explicitly named artifact ID and audit rationale:

```powershell
threadforge decision calibrate --id parser-choice-v1 --expected-revision 0 --file calibration.json --actor local-user --reason "Fit on the reviewed calibration split"
threadforge decision artifact show --id parser-choice-v1
threadforge decision artifact approve --id parser-choice-v1 --expected-revision 1 --actor reviewer --reason "Reviewed model and question binding"
```

Approval may optionally pin the exact bytes of a materialized local model checkpoint with `--model-path <directory>`. Threadforge stores only its deterministic SHA-256 fingerprint, never the path or file names. Inference with a pinned per-question artifact requires the local HF backend and verifies the checkpoint before and after model loading; a changed or unavailable checkpoint fails closed. Without a pin, the existing active-artifact behavior remains available and reports the model environment as unverified. Checkpoint binding is available only during candidate approval, is preserved in artifact history, and does not authorize execution or make a decision cutover evidence.

Without a backend in the file, `THREADFORGE_MODEL_PATH` selects the local HF model (CPU/float32 defaults). A backend explicitly set to vLLM must still satisfy the Python contract: remote inference requires `remote_opt_in: true` and HTTPS, and credentials are read only from the configured environment variable. Calibration and decision question/state text containing detected credential material is rejected before inference.

### vLLM L2 pooling

The current AnyJev vLLM backend supports two server paths: the existing completion/log-probability path for raw/L0/L1, and a local `/v1/embeddings` pooler for L2 heads. The pooling server exposes only its final normalized vector; it does not provide intermediate layers, logits, or per-token positions. Fit L2 heads against that served representation with `layers: [-1]`. Threadforge reads the real block count from `tokenizer_path` so stored artifact layer indices remain aligned with the source model even when vLLM uses a served-model alias. Unsupported layer/logit/position requests fail instead of substituting a different representation.

The ordinary `decision` and `decision.calibrate` operations do not start, install, or configure vLLM; an operator-managed embedding/pooling server is used for that path. Separately, the explicit offline benchmark command below can manage a temporary local vLLM process. Non-loopback use through the ordinary backend still requires `remote_opt_in: true`, HTTPS, and `THREADFORGE_VLLM_API_KEY`; remote inference cannot satisfy an approved local checkpoint fingerprint. Fixture tests exercise `/v1/embeddings`, vector shape, alias/config identity, and refusal paths; no authentic model run has yet been measured.

### Create a depth-truncated local checkpoint

`threadforge decision model truncate --model-path <local-checkpoint> --blocks <n> --output-path <new-directory> [--tokenizer-path <local-tokenizer>] [--dtype auto|float16|bfloat16|float32]` keeps the first `n` transformer blocks and the embedding, final norm, and language-model head tensors, then updates the ordinary Transformers config. Inputs must already exist locally; Hub identifiers/downloads, remote code, symlinked paths, and existing destinations are rejected. The output is staged beside its destination, source/model-tokenizer directories are fingerprinted before and after processing, and the destination is installed only after validation. Existing files are never overwritten.

The command records source/tokenizer/output SHA-256 digests and layer/tensor counts, never echoes local paths, and marks the result review-required, execution-unverified, and cutover-ineligible. Heads must be fit on the checkpoint that will be served: a truncated model's final normalized vector differs from the corresponding intermediate HF hidden state. This utility creates a local model artifact only; it does not launch inference or establish accuracy/latency benefit. Its output has mock-runtime tests; an authentic checkpoint, installed vLLM server, and held-out measurement have not been run.

### Local serving-depth benchmark

`threadforge decision benchmark pipeline --file <serving-pipeline-request.json> [--python python]` and Python JSON-stdio operation `decision.serve.pipeline` provide a local AnyJev-style serve/fit/measure path. The versioned request uses `model_path`, `case_root`, and 1–32 question descriptors with relative train/test case paths; train/test documents must bind the same typed question, use disjoint states, and share the same ordered train/test states across questions. Each split allows up to 5,000 examples, with at least `max(8, 2 × option_count)` train examples and two test examples; the aggregate limit is 100,000 examples. Depths default to the full checkpoint and `floor(2/3 × total_blocks)` (deduplicated), unless `only_full` or explicit `depths` is selected.

The benchmark uses local checkpoints only, fingerprints source bytes before and after, creates truncated checkpoints only in a temporary directory, and runs one vLLM embedding server at a time bound to `127.0.0.1`; it requires Linux or WSL for process-group cleanup and never downloads model files. Install the optional runtime with `python -m pip install -e ".[serving,hf]"`. Defaults are `repeats=3`, `port=8100` (`0` selects an available port), `gpu_memory_utilization=0.85`, `prefix_caching=false`, `multi_question_states=64`, and `startup_timeout_seconds=900`; quantization is unset unless requested. Results report checkpoint/case digests, fitted-head method, accuracy, 15-bin ECE, fit and decision latency, and timing spread without returning paths or case content. They always set `independent_holdout_verified=false`, `execution_verified=false`, and `cutover_evidence_eligible=false`: caller-provided test cases are not independently authenticated held-out evidence. Request/result contracts are [here](../schema/anyjev-serving-pipeline-request-v1.schema.json) and [here](../schema/anyjev-serving-pipeline-result-v1.schema.json). Current tests mock the vLLM server and model boundary; no authentic checkpoint, GPU run, live service, or held-out comparison has been completed.

The labeled examples are not written to Threadforge storage. Only the generated calibration parameter artifact, its digest, the model identifier, question-layout hash, level, sample count, and lifecycle audit metadata are persisted. The artifact itself may carry question text or option labels (especially L2); it remains in the private local SQLite store and must not be copied to project/team files.

Calibration creates a `candidate`, never an active artifact. The reviewer must use the current revision to `approve` or `reject`. An approved artifact can be `retire`d; rejected and retired artifacts cannot be revived or overwritten. Recalibration creates a new ID, preserving every prior artifact and decision. `artifact list`, `show`, and `history` are local reads.

One upstream AnyJev per-question L1 temperature artifact or L2 head can be imported as a new local candidate and exported again as its native per-question JSON payload:

```powershell
threadforge decision artifact import --file .\calibration-import.json
threadforge decision artifact export --id imported-calibration
```

The import envelope is defined by [decision-artifact-import-v1](../schema/decision-artifact-import-v1.schema.json); its content must be one structurally valid, credential-free AnyJev calibration/head bound to its embedded model and question key. Imports are recorded with action `imported`, retain revision 1/status `candidate`, and still require human approval before inference. Export prints only the validated per-question payload, not Threadforge lifecycle metadata. This narrow per-question path excludes adaptation statistics and labeled observations; complete native bundles use the separate whole-bundle lifecycle below.

The Python bridge validates native `Decider.export_artifacts()` bundles through `decision.bundle.validate`, with request/result contracts in [decision-bundle-validation-request-v1](../schema/decision-bundle-validation-request-v1.schema.json) and [decision-bundle-validation-v1](../schema/decision-bundle-validation-v1.schema.json). It structurally loads L1 artifacts, L2 heads, label-free adaptation arrays, and—only with explicit opt-in—bounded labeled observations. Validation itself performs no inference or model-environment verification.

Whole bundles can now be imported to private SQLite as review candidates:

```powershell
threadforge decision bundle import --file .\native-bundle-import.json
threadforge decision bundle list --status candidate
threadforge decision bundle show --id anyjev-bundle
threadforge decision bundle history --id anyjev-bundle
threadforge decision model fingerprint --path .\models\local-checkpoint
threadforge decision bundle approve --id anyjev-bundle --expected-revision 1 --actor reviewer --reason "reviewed model and bundle" --model-path .\models\local-checkpoint
threadforge decision bundle export --id anyjev-bundle
threadforge decision bundle run --file .\decision.json --id anyjev-bundle
threadforge decision bundle batch --file .\decision-batch.json --id anyjev-bundle
```

The import envelope is `threadforge.decision-bundle-import.v1`. Files are capped at 16 MiB plus a 64 KiB envelope; Threadforge runs the versioned Python validator, checks its model/count receipt, credential-scans the full payload, hashes the normalized content, and stores it locally. Lists, `show`, and revision history expose metadata only. Export refuses bundles containing labeled observations unless `--include-observations` is supplied. Approval/rejection/retirement use revision compare-and-swap and append-only audit history. Before approval, `decision model fingerprint --path <directory>` reports a deterministic SHA-256 over relative file names, sizes, and bytes without printing the path or names. Approval may bind that digest with `--model-path`; the directory is rehashed, and the digest is retained in the local bundle snapshot/history rather than in the upstream AnyJev payload. Fingerprinting requires a materialized directory containing only regular files (no symlinks or reparse points), with at most 20,000 files and 1 TiB total. `decision bundle run` and `decision bundle batch` accept only active revision-2 bundles. When a digest is bound, inference requires local HF, verifies the checkpoint before and after loading, and rejects changed bytes; an unbound bundle remains usable but reports `model_environment_verified: false`. Remote vLLM still requires explicit opt-in and HTTPS and cannot satisfy a local checkpoint binding. The verification covers checkpoint-directory bytes, not the Python/runtime/device environment. Results follow [decision-bundle-decision-v1](../schema/decision-bundle-decision-v1.schema.json) and [decision-bundle-batch-result-v1](../schema/decision-bundle-batch-result-v1.schema.json); they remain advisory, with human review required and execution/cutover authorization false.

The `threadforge decision observe` workflow persists AnyJev `Decider.observe()` state in the private local SQLite store while Python remains stateless. Prepare a `threadforge.decision-observe-cli.v1` file using [the CLI request schema](../schema/decision-observe-cli-request-v1.schema.json), then run:

```powershell
threadforge decision observe --stream-id parser-observations --expected-revision 0 --file observe.json --reason "record reviewed labeled examples"
threadforge decision observe --stream-id parser-observations --expected-revision 1 --file observe-next.json --reason "continue the reviewed sample set"
threadforge decision observe history --stream-id parser-observations
```

Each file contains only the new 1-128 labeled examples (at most 256 KiB encoded); the Go command loads and supplies the prior checkpoint. Initial defaults are `fit_at=30`, `refit_factor=2.0`, and fit options `layers=null`, `kinds=["lda","ridge"]`, `n_folds=5`, `seed=0`, `listing="auto"`. The question, model, schedule, and solver options are bound for the stream. The accumulated checkpoint is capped at 4096 observations/8 MiB and stored locally with mode-restricted SQLite, a SHA-256 digest, and append-only revision audit metadata. Existing state/label prefixes cannot be rewritten. `--expected-revision` is compare-and-swap; stale concurrent updates fail. Updating the checkpoint and inserting a newly fitted L2 candidate plus its artifact audit is one transaction. The printed result/history contain counts and digests, never labeled state text. If fitting occurs, the candidate ID defaults to `<stream-id>-fit-<fit-count>`; `--artifact-id` can override it. A candidate always remains pending human review and must separately pass `decision artifact approve` before use. Request/result contracts: [CLI request](../schema/decision-observe-cli-request-v1.schema.json), [CLI result](../schema/decision-observe-cli-result-v1.schema.json), [Python operation request](../schema/decision-observe-request-v1.schema.json), [Python operation result](../schema/decision-observe-result-v1.schema.json), and [audit history](../schema/decision-observe-history-v1.schema.json).

## Replay AnyJev teacher distillation caches

The distillation family has seven per-stage operations: `export-cases` converts a caller-materialized local DatasetDict into train exemplars and labeled train/test case files; `synth` creates schema-shaped workflow states from caller-supplied exemplars using a fingerprint-pinned local HF generator; `label`, `extract`, and `extract-gold` run bounded inference on an explicitly fingerprinted local HF checkpoint and atomically create NPZ caches; `fit` and `soft` consume existing caches, write no files, activate no heads, and perform no model inference. `pipeline` composes those stages from one versioned request. All results remain human-review-required and cutover-ineligible. Create a `threadforge.anyjev-distillation-fit-request.v1` request using the [fit request schema](../schema/anyjev-distillation-fit-request-v1.schema.json), then run:

```powershell
threadforge decision distill fit --file .\distillation-fit.json
```

`cache_root` must be an absolute local directory. All NPZ references are relative to that root; symlinks, reparse points, traversal, object arrays, invalid dimensions, credentials, changed files, and cache/expanded-size limit violations are rejected. The `fit` request groups one teacher's synthetic and held-out probabilities with each student's gold train/test and synthetic feature caches. `calibration_examples` defaults to 300, `budgets` defaults to `[300,1200]`, and `bootstrap_resamples` defaults to 1000; test labels must exactly match across teacher and student caches, and synthetic probabilities/features must align exactly. The fit reproduces AnyJev's hard-target LDA/ridge selection, soft-target ridge fitting, gold-plus-synthetic variants, and paired seeded accuracy bootstrap.

The separate `soft` operation replays the same-state soft-target stage with a `threadforge.anyjev-distillation-soft-request.v1` request using the [soft request schema](../schema/anyjev-distillation-soft-request-v1.schema.json):

```powershell
threadforge decision distill soft --file .\distillation-soft.json
```

It reads teacher train/test feature caches and each student's gold train/test feature caches under the same bounded local-root rules. Teacher training probabilities are generated by five-fold out-of-fold LDA/ridge selection; it compares teacher, student gold, teacher-argmax, teacher-soft, and gold-plus-teacher-soft methods, with `bootstrap_resamples` defaulting to 1000. Teacher/student classes and train/test labels must align across caches. Fit/soft report aggregate accuracy/ECE/NLL and hashed source-file digests only; identifiers and local paths are not echoed. Their result flags are `model_inference_verified=false`, `execution_verified=false`, and `cutover_evidence_eligible=false`.

### Run the complete local distillation workflow

`threadforge decision distill pipeline --file .\distillation-pipeline.json` composes local train/test export, checkpoint-pinned synthetic generation, teacher labels, teacher/student gold features, student synthetic features, then CPU `fit` and OOF `soft` replay. The [pipeline request schema](../schema/anyjev-distillation-pipeline-request-v1.schema.json) requires one local DatasetDict, separate absolute `staging_root` and `cache_root`, selected workflows, a fingerprinted generator, a fingerprinted teacher plus native artifact, and 1–8 fingerprinted students with feature blocks/layer counts. Defaults are `calibration_examples=300`, `budgets=[300,1200]`, `bootstrap_resamples=1000`, `seed=0`, and `generator.per_workflow=1200`. The train and test exports must fingerprint to the same dataset tree; each question must have the exact requested calibration count and at least two held-out examples before any model is loaded. Existing stage operations enforce local-only HF backends, exact checkpoint pins, bounded cache sizes, and exclusive/idempotent outputs. The [pipeline result schema](../schema/anyjev-distillation-pipeline-result-v1.schema.json) includes each stage receipt and aggregate fit/soft metrics, but never emits absolute paths or raw states/features. Partial cache files from a failed run remain inert and can be reused only when regenerated content is byte/array-equivalent. Successful orchestration is still not a held-out acceptance run: use a provenance-verified source split and independently evaluate before treating metrics as evidence.

### Generate teacher-label caches

The versioned `threadforge.anyjev-distillation-label-request.v1` request includes `data_root`, an existing writable `cache_root`, `teacher_model`, `teacher_model_checkpoint_sha256`, local HF `backend`, a native teacher artifact bundle, workflow synthetic-state paths, and per-question held-out-case paths. Use [label request](../schema/anyjev-distillation-label-request-v1.schema.json), [test-case input](../schema/anyjev-distillation-test-cases-v1.schema.json), and [label result](../schema/anyjev-distillation-label-result-v1.schema.json). Test-case JSON carries one typed `question` and examples with `state` plus `gold_index`; workflow JSON follows the AnyJev generator shape (`workflow`, `states`, optional provenance fields). The teacher artifact must bind to `teacher_model` and cannot contain adaptation or labeled-observation state. Compute the fingerprint with `threadforge decision model fingerprint --path <teacher-model-directory>`, place its `sha256` in the request, then run:

```powershell
threadforge decision distill label --file .\distillation-label.json
```

The command runs `Decider.decide_batch(..., level="L2")` for synthetic and held-out states. It creates `distillation/synth_labels/<checkpoint-sha>/<question-sha>.{synth,test}.npz`, containing only `probs`, `K`, `block`, and test-only `y_gold`. Output creation is exclusive and atomic; a repeated run reuses only byte-array-equivalent caches and refuses conflicting existing files. Remote backends, changed checkpoints, secrets, linked source paths, malformed inputs, and inconsistent cache outputs fail closed. The receipt hashes source files and cache bytes but does not echo state content or local paths.

### Extract student feature caches

The versioned `threadforge.anyjev-distillation-extract-request.v1` request uses `student_model`, `student_model_checkpoint_sha256`, local HF `backend`, a fixed `block`, `seed` default 0, workflow synthetic-state paths, and typed questions. See [extract request](../schema/anyjev-distillation-extract-request-v1.schema.json) and [extract result](../schema/anyjev-distillation-extract-result-v1.schema.json). Run:

```powershell
threadforge decision distill extract --file .\distillation-extract.json
```

Extraction matches AnyJev's fixed-block `_features` path and uses per-state random option permutations for unordered questions (`seed=0` by default); ordered score questions retain canonical order. It stores float16 hidden states as `H`, plus scalar `block` and `K`, under `distillation/synth_features/<checkpoint-sha>/<question-sha>.synth.npz`. As with label caches, output is local, exclusive/atomic, digest-receipted, and never accepted as cutover evidence by itself.

### Extract gold train/test feature caches

After `export-cases` has produced per-question train and held-out case JSON, `threadforge decision distill extract-gold --file .\distillation-extract-gold.json` reads both files under `data_root` and requires `model_role` (`teacher` or `student`), `model`, `model_checkpoint_sha256`, local HF `backend`, fixed `block`, optional `seed` (default 0), and typed question descriptors with `train_cases_path` and `test_cases_path`. Use the [extract-gold request schema](../schema/anyjev-distillation-extract-gold-request-v1.schema.json) and [result schema](../schema/anyjev-distillation-extract-gold-result-v1.schema.json). Each split is limited to 5,000 examples; aggregate feature output is bounded to 50 million values. Train uses AnyJev's seeded random option listings for unordered questions; test and ordered questions use canonical listing. The exclusive cache path is `distillation/gold_features/<checkpoint-sha>/<question-sha>.<train|test>.npz`; arrays are `H` float16 `[N,1,d]`, `y` int64 labels, scalar `K`, and `layers` int64 `[block]`, compatible with the read-only fit/soft cache reader. Cache receipts bind source-file hashes, checkpoint digest, dimensions, and cache bytes without returning paths outside the cache root, raw states, or labels. A mocked local boundary verifies the contract; no authentic dataset/checkpoint run or split provenance has been verified.

### Generate schema-shaped synthetic states

Prepare a `threadforge.anyjev-distillation-exemplars.v1` JSON file with 3-10,000 distinct examples for each workflow. The versioned [synth request](../schema/anyjev-distillation-synth-request-v1.schema.json) supplies `source_root`, existing `cache_root`, `generator_model`, `generator_checkpoint_sha256`, a local HF backend, and workflow/exemplar paths. Defaults are `per_workflow=1200`, `batch_size=16`, `max_new_tokens=700`, `temperature=1.0`, `top_p=0.95`, `seed=0`, and at most ten attempts per requested state (bounded globally). Fingerprint the local model as described above and run:

```powershell
threadforge decision distill synth --file .\distillation-synth.json
```

The generator uses three exemplars per prompt, rejects malformed, shape-mismatched, duplicate, or credential-bearing outputs, and fails without writing partial workflow files if any requested workflow cannot reach its count. Versioned generated JSON is atomically installed under `distillation/synth/<checkpoint-sha>/<workflow-sha>.json`; an identical existing file is reused, while a conflicting file is refused. The receipt contains only hashes, counts, and hash-keyed relative paths. Synthetic fixture and mocked CLI coverage does not establish real-model quality or benchmark parity; review generated content before use.

### Export typed train exemplars and held-out cases from a local dataset

The versioned [export-cases request](../schema/anyjev-distillation-export-cases-request-v1.schema.json) accepts an absolute dataset_path to a caller-materialized Hugging Face DatasetDict, a separate existing output_root, a named train or test split, and selected AnyJev workflows. Install the optional dependency with pip install '.[typed-data]' from python/. The operation uses local load_from_disk only; it does not accept a Hub ID or download data. The saved DatasetDict must contain the named split and AnyJev row columns id, workflow, state, questions, and gold.

Run: threadforge decision distill export-cases --file .\distillation-export-cases.json

For train, each workflow produces at least three unique state exemplars under distillation/source/<dataset-sha>/train/exemplars/<workflow-sha>.json, ready for synth, plus per-question threadforge.anyjev-distillation-train-cases.v1 files under hashed workflow/question paths for gold-feature extraction. For test, each typed question produces a threadforge.anyjev-distillation-test-cases.v1 file under a hashed workflow/question path, ready for label and held-out feature extraction. The default cap is 5,000 source cases per workflow; the test split is bounded to 5,000 because that is the downstream label limit. Source files are strict-JSON parsed, credential-scanned, question layouts and case IDs are reconciled, input/output trees must not overlap, and the local dataset tree must retain the same fingerprint during export. Raw state/gold data remains in the caller's private local output directory; the result contains only digests, counts, and hashed relative paths. A named split in a local DatasetDict is not independent held-out provenance: results remain human-review-required and cutover-ineligible until the source snapshot is independently reconciled.

These six interfaces have fixtures and CLI contract coverage, but no real source DatasetDict, checkpoint, or generated cache has been reconciled against an AnyJev run. The local AnyJev checkout lacks the source feature caches required by soft replay. End-to-end orchestration, real-model parity, backend coverage, and held-out benchmark acceptance remain open.

## Run a typed decision

Prepare a `threadforge.decision-request.v1` file using the [decision request schema](../schema/decision-request-v1.schema.json). `threadforge decision run --file decision.json` performs the requested typed decision. To use calibration, supply the active ID explicitly:

```powershell
threadforge decision run --file decision.json --artifact-id parser-choice-v1
```

The runner sends only the artifact content to the versioned Python stdio process; it never gives Python access to the SQLite path. AnyJev checks the embedded model name and question hash when loading an artifact. L1 can be requested as `L1` or `auto`; L2 can be requested as `L2` or `auto`. No automatic tool action follows from a decision. Callers should set `require_level` when downstream logic needs a minimum calibration level, and still treat the result as advisory rather than authorization.

For one typed question evaluated against multiple states, prepare a `threadforge.decision-batch-request.v1` file with `question`, `states`, and optional `backend`, `level`, and `require_level` fields using the [batch request schema](../schema/decision-batch-request-v1.schema.json). Run the upstream AnyJev `Decider.decide_batch` path through:

```powershell
threadforge decision batch --file decision-batch.json --artifact-id parser-choice-v1
```

Batch requests allow 1 to 128 states and cap their combined encoded state payload at 256 KiB. Credentials in the question, states, artifact, or request file are rejected before inference. The versioned result contains one decision per input state and backend counters, but never echoes the states. Batch inference is advisory and does not change the artifact lifecycle or authorize an action. The result contract is [here](../schema/decision-batch-result-v1.schema.json).

The protocol accepts bounded lines up to 18 MiB so a single L2 head can cross stdio; calibration input is bounded to 512 KiB, calibration states to 256 KiB total, individual decision state to 64 KiB, and stored artifact content to 16 MiB. Batch request files are bounded to 512 KiB by the CLI, with a stricter 256 KiB total state limit in the Python protocol. The larger transport bound does not relax nested state or artifact limits.
