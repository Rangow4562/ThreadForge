"""Local-only AnyJev vLLM depth/accuracy/latency pipeline.

This is a Threadforge-authored JSON-stdio integration of the current AnyJev
``pipeline`` behavior. It requires materialized local checkpoints and caller-
provided train/test cases, binds vLLM to loopback, and never treats its result
as independent cutover evidence.
"""

from __future__ import annotations

import hashlib
import http.client
import json
import math
import os
import signal
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any

MAX_QUESTIONS = 32
MAX_DEPTHS = 32
MAX_EXAMPLES_PER_SPLIT = 5_000
MAX_TOTAL_EXAMPLES = 100_000
MAX_STATE_BYTES = 256 << 10
MAX_POLL_RESPONSE_BYTES = 1 << 20
SERVED_MODEL_NAME = "threadforge-anyjev"
POOLER_CONFIG = '{"pooling_type":"LAST","normalize":false,"softmax":false}'
_REQUEST_FIELDS = {
    "schema_version", "model_path", "case_root", "questions", "depths", "only_full",
    "repeats", "port", "gpu_memory_utilization", "prefix_caching", "quantization",
    "multi_question_states", "startup_timeout_seconds",
}


class ServingPipelineError(ValueError):
    """Invalid local evaluation input or a failed/unsafe serving run."""


def run_serving_pipeline(params: dict[str, Any]) -> dict[str, Any]:
    import numpy as np

    from anyjev.truncate import _existing_real_directory, truncate
    from threadforge_py.distillation import DistillationError, _read_json_source, _real_cache_root
    from threadforge_py.protocol import ProtocolError, _fingerprint_model_directory

    request = _validate_request(params)
    try:
        model_path = _existing_real_directory(request["model_path"], "model_path")
        case_root = _real_cache_root(request["case_root"])
    except (ValueError, OSError):
        raise ServingPipelineError("model_path and case_root must be real local directories") from None
    if model_path == case_root or model_path in case_root.parents or case_root in model_path.parents:
        raise ServingPipelineError("model_path and case_root must not overlap")

    try:
        source_fingerprint = _fingerprint_model_directory(str(model_path))
    except ProtocolError:
        raise ServingPipelineError("local model checkpoint could not be fingerprinted safely") from None
    total_blocks = _local_model_depth(model_path)
    depths = _depths(request, total_blocks)
    questions, case_set_sha256, total_examples = _load_questions(
        request["questions"], case_root, _read_json_source, DistillationError, np,
    )
    if total_examples > MAX_TOTAL_EXAMPLES:
        raise ServingPipelineError("serving evaluation exceeds the aggregate example limit")

    model_after_read = _fingerprint_model_directory(str(model_path))
    if model_after_read != source_fingerprint:
        raise ServingPipelineError("local model changed while the serving request was prepared")

    rows: list[dict[str, Any]] = []
    with tempfile.TemporaryDirectory(prefix="threadforge-anyjev-serving-") as working_directory:
        work = Path(working_directory)
        if os.name != "nt":
            os.chmod(work, 0o700)
        for blocks in depths:
            served_model = model_path
            checkpoint_sha256 = source_fingerprint["sha256"]
            if blocks < total_blocks:
                served_model = work / f"model-depth-{blocks}"
                try:
                    receipt = truncate({
                        "schema_version": "threadforge.anyjev-truncate-request.v1",
                        "model_path": str(model_path),
                        "blocks": blocks,
                        "output_path": str(served_model),
                        "dtype": "auto",
                    })
                except Exception:
                    raise ServingPipelineError("local checkpoint truncation failed safely") from None
                if (not isinstance(receipt, dict) or receipt.get("kept_blocks") != blocks or
                        receipt.get("original_blocks") != total_blocks or
                        not _is_sha256(receipt.get("output_sha256")) or
                        receipt.get("execution_verified") is not False or
                        receipt.get("cutover_evidence_eligible") is not False):
                    raise ServingPipelineError("local checkpoint truncation returned an invalid receipt")
                checkpoint_sha256 = receipt["output_sha256"]

            server = LocalVLLMServer(
                model_path=served_model,
                port=request["port"],
                gpu_memory_utilization=request["gpu_memory_utilization"],
                prefix_caching=request["prefix_caching"],
                quantization=request["quantization"],
                startup_timeout_seconds=request["startup_timeout_seconds"],
                work_directory=work,
                log_path=work / f"vllm-depth-{blocks}.log",
            )
            with server as running:
                row = _measure_depth(
                    running.url, model_path, blocks, total_blocks, checkpoint_sha256,
                    questions, request, np,
                )
            rows.append(row)

    try:
        source_after = _fingerprint_model_directory(str(model_path))
    except ProtocolError:
        raise ServingPipelineError("local model could not be verified after serving") from None
    if source_after != source_fingerprint:
        raise ServingPipelineError("local model changed during the serving pipeline")

    quantization = request["quantization"]
    result = {
        "schema_version": "threadforge.anyjev-serving-pipeline-result.v1",
        "source_model_sha256": source_fingerprint["sha256"],
        "case_set_sha256": case_set_sha256,
        "total_blocks": total_blocks,
        "parameters": {
            "repeats": request["repeats"],
            "prefix_caching": request["prefix_caching"],
            "gpu_memory_utilization": request["gpu_memory_utilization"],
            "multi_question_states": request["multi_question_states"],
            "port": request["port"],
            "startup_timeout_seconds": request["startup_timeout_seconds"],
            "quantization": quantization,
        },
        "rows": rows,
        "model_inference_verified": True,
        "remote_inference_used": False,
        "independent_holdout_verified": False,
        "execution_verified": False,
        "human_review_required": True,
        "cutover_evidence_eligible": False,
    }
    if not np.isfinite(np.asarray([request["gpu_memory_utilization"]])).all():
        raise ServingPipelineError("serving pipeline parameters are not finite")
    return result


def _validate_request(params: Any) -> dict[str, Any]:
    if not isinstance(params, dict) or set(params) - _REQUEST_FIELDS:
        raise ServingPipelineError("serving pipeline request has unsupported fields")
    if params.get("schema_version") != "threadforge.anyjev-serving-pipeline-request.v1":
        raise ServingPipelineError("serving pipeline request version is unsupported")
    if "model_path" not in params or "case_root" not in params:
        raise ServingPipelineError("serving pipeline requires model_path and case_root")
    for key in ("model_path", "case_root"):
        value = params.get(key)
        if (not isinstance(value, str) or not value.strip() or len(value) > 4096 or
                "\x00" in value or not Path(value).expanduser().is_absolute()):
            raise ServingPipelineError(f"{key} must be an absolute local directory")
    questions = params.get("questions")
    if not isinstance(questions, list) or not 1 <= len(questions) <= MAX_QUESTIONS:
        raise ServingPipelineError(f"questions must contain 1 to {MAX_QUESTIONS} entries")
    if "depths" in params and "only_full" in params and params["only_full"] is True:
        raise ServingPipelineError("only_full cannot be combined with explicit depths")

    only_full = params.get("only_full", False)
    repeats = params.get("repeats", 3)
    port = params.get("port", 8100)
    gpu_fraction = params.get("gpu_memory_utilization", 0.85)
    prefix_caching = params.get("prefix_caching", False)
    quantization = params.get("quantization")
    multi_states = params.get("multi_question_states", 64)
    startup_timeout = params.get("startup_timeout_seconds", 900)
    if type(only_full) is not bool or type(prefix_caching) is not bool:
        raise ServingPipelineError("only_full and prefix_caching must be booleans")
    if type(repeats) is not int or not 2 <= repeats <= 10:
        raise ServingPipelineError("repeats must be an integer between 2 and 10")
    if type(port) is not int or not 0 <= port <= 65535:
        raise ServingPipelineError("port must be an integer between 0 and 65535")
    if (isinstance(gpu_fraction, bool) or not isinstance(gpu_fraction, (float, int)) or
            not math.isfinite(float(gpu_fraction)) or not 0.05 < float(gpu_fraction) <= 0.95):
        raise ServingPipelineError("gpu_memory_utilization must be finite and within (0.05, 0.95]")
    if quantization is not None and (not isinstance(quantization, str) or
                                      not 1 <= len(quantization) <= 32 or
                                      not all(ch.isalnum() or ch in "_-" for ch in quantization)):
        raise ServingPipelineError("quantization must be a bounded vLLM identifier")
    if type(multi_states) is not int or not 1 <= multi_states <= 1000:
        raise ServingPipelineError("multi_question_states must be an integer between 1 and 1000")
    if type(startup_timeout) is not int or not 10 <= startup_timeout <= 900:
        raise ServingPipelineError("startup_timeout_seconds must be between 10 and 900")

    return {
        **params,
        "only_full": only_full,
        "repeats": repeats,
        "port": port,
        "gpu_memory_utilization": float(gpu_fraction),
        "prefix_caching": prefix_caching,
        "quantization": quantization,
        "multi_question_states": multi_states,
        "startup_timeout_seconds": startup_timeout,
    }


def _depths(request: dict[str, Any], total_blocks: int) -> list[int]:
    if "depths" in request:
        values = request["depths"]
        if (not isinstance(values, list) or not 1 <= len(values) <= MAX_DEPTHS or
                any(type(value) is not int or not 1 <= value <= total_blocks for value in values) or
                len(set(values)) != len(values)):
            raise ServingPipelineError("depths must be unique model block counts within the local checkpoint")
        return list(values)
    if request["only_full"]:
        return [total_blocks]
    return list(dict.fromkeys((total_blocks, max(1, (total_blocks * 2) // 3))))


def _local_model_depth(model_path: Path) -> int:
    try:
        from transformers import AutoConfig

        config = AutoConfig.from_pretrained(
            str(model_path), local_files_only=True, trust_remote_code=False,
        )
        value = getattr(config, "num_hidden_layers", None)
    except Exception:
        raise ServingPipelineError("local checkpoint has no supported offline Transformers config") from None
    if type(value) is not int or not 1 <= value <= 4096:
        raise ServingPipelineError("local checkpoint transformer depth is outside the supported range")
    return value


def _load_questions(descriptors, case_root, read_source, distillation_error, np):
    from threadforge_py.protocol import ProtocolError, _question

    loaded = []
    seen_ids: set[str] = set()
    seen_paths: set[str] = set()
    source_bytes = [0]
    total_examples = 0
    common_train_states: list[str] | None = None
    common_test_states: list[str] | None = None
    fingerprints: list[dict[str, str]] = []

    for descriptor in descriptors:
        if (not isinstance(descriptor, dict) or set(descriptor) !=
                {"question_id", "train_cases_path", "test_cases_path"}):
            raise ServingPipelineError("question entries must specify an ID and train/test relative paths")
        question_id = descriptor["question_id"]
        if not isinstance(question_id, str) or not question_id.strip() or len(question_id) > 512:
            raise ServingPipelineError("question_id must contain 1 to 512 characters")
        if question_id in seen_ids:
            raise ServingPipelineError("question_id values must be unique")
        seen_ids.add(question_id)
        train_path, test_path = descriptor["train_cases_path"], descriptor["test_cases_path"]
        if (not _safe_relative_json_path(train_path) or not _safe_relative_json_path(test_path) or
                train_path == test_path or train_path in seen_paths or test_path in seen_paths):
            raise ServingPipelineError("train/test case paths must be distinct safe relative JSON paths")
        seen_paths.update((train_path, test_path))
        try:
            train_doc, train_sha = read_source(case_root, train_path, source_bytes)
            test_doc, test_sha = read_source(case_root, test_path, source_bytes)
        except distillation_error:
            raise ServingPipelineError("local train/test case files could not be read safely") from None

        question_spec = train_doc.get("question")
        if (not _valid_case_document(train_doc, "threadforge.anyjev-distillation-train-cases.v1") or
                not _valid_case_document(test_doc, "threadforge.anyjev-distillation-test-cases.v1") or
                train_doc.get("question_id") != question_id or test_doc.get("question_id") != question_id or
                _canonical_bytes(train_doc["question"]) != _canonical_bytes(test_doc["question"])):
            raise ServingPipelineError("train/test cases must use matching versioned question layouts and IDs")
        try:
            question = _question(question_spec)
        except ProtocolError:
            raise ServingPipelineError("typed question definition is invalid") from None

        train_examples = train_doc["examples"]
        test_examples = test_doc["examples"]
        if (not 1 <= len(train_examples) <= MAX_EXAMPLES_PER_SPLIT or
                not 2 <= len(test_examples) <= MAX_EXAMPLES_PER_SPLIT):
            raise ServingPipelineError("train/test case counts exceed the serving limits")
        minimum_train = max(8, 2 * question.k)
        if len(train_examples) < minimum_train:
            raise ServingPipelineError("training examples are below AnyJev's minimum for this question")

        train_states, train_labels, train_hashes = _case_examples(train_examples, question.k, np)
        test_states, test_labels, test_hashes = _case_examples(test_examples, question.k, np)
        if set(train_hashes).intersection(test_hashes):
            raise ServingPipelineError("train and held-out case states overlap")
        if common_train_states is None:
            common_train_states, common_test_states = train_hashes, test_hashes
        elif train_hashes != common_train_states or test_hashes != common_test_states:
            raise ServingPipelineError("multi-question cases must share identical ordered train/test states")

        total_examples += len(train_examples) + len(test_examples)
        if total_examples > MAX_TOTAL_EXAMPLES:
            raise ServingPipelineError("serving evaluation exceeds the aggregate example limit")
        question_sha = _sha256_bytes(_canonical_bytes(question_spec))
        question_id_sha = _sha256_bytes(question_id.encode("utf-8"))
        loaded.append({
            "question": question,
            "question_id_sha256": question_id_sha,
            "question_sha256": question_sha,
            "train_states": train_states,
            "train_labels": train_labels,
            "test_states": test_states,
            "test_labels": test_labels,
            "train_examples": len(train_examples),
            "test_examples": len(test_examples),
        })
        fingerprints.append({"question_id_sha256": question_id_sha, "train_sha256": train_sha, "test_sha256": test_sha})

    if not loaded:
        raise ServingPipelineError("serving pipeline requires typed train/test cases")
    case_set = _canonical_bytes(sorted(fingerprints, key=lambda row: row["question_id_sha256"]))
    return loaded, _sha256_bytes(case_set), total_examples


def _valid_case_document(value: Any, schema_version: str) -> bool:
    if (not isinstance(value, dict) or set(value) != {"schema_version", "question_id", "question", "examples"} or
            value.get("schema_version") != schema_version or not isinstance(value.get("question_id"), str) or
            not isinstance(value.get("question"), dict) or not isinstance(value.get("examples"), list)):
        return False
    spec = value["question"]
    if set(spec) - {"kind", "text", "options", "name", "scale", "bins", "levels"}:
        return False
    return all(isinstance(row, dict) and set(row) == {"state", "gold_index"} for row in value["examples"])


def _case_examples(examples, class_count, np):
    states, labels, digests = [], [], []
    seen: set[str] = set()
    for example in examples:
        state = example["state"]
        try:
            state_bytes = _canonical_bytes(state)
        except (TypeError, ValueError):
            raise ServingPipelineError("case state is not finite JSON") from None
        if not state_bytes or len(state_bytes) > MAX_STATE_BYTES:
            raise ServingPipelineError("case state exceeds the per-example byte limit")
        label = example["gold_index"]
        if type(label) is not int or not 0 <= label < class_count:
            raise ServingPipelineError("case label is outside the typed question options")
        digest = _sha256_bytes(state_bytes)
        if digest in seen:
            raise ServingPipelineError("case split contains duplicate states")
        seen.add(digest)
        states.append(state)
        labels.append(label)
        digests.append(digest)
    return states, np.asarray(labels, dtype=np.int64), digests


def _measure_depth(url, tokenizer_path, blocks, total_blocks, checkpoint_sha256, questions, request, np):
    try:
        from anyjev import Decider
        from anyjev.backends.vllm import VLLMBackend

        backend = VLLMBackend(
            url, SERVED_MODEL_NAME, tokenizer_name=str(tokenizer_path), workers=16, timeout=120.0,
        )
        decider = Decider(backend, level="L2")
        question_results = []
        for item in questions:
            started = time.perf_counter()
            artifact = decider.fit_head(
                item["question"], item["train_states"], item["train_labels"], layers=[-1],
            )
            fit_ms = (time.perf_counter() - started) * 1000.0
            if not isinstance(artifact, dict) or artifact.get("method") not in {"head:lda", "head:ridge"}:
                raise ServingPipelineError("AnyJev returned an unsupported fitted-head receipt")

            test_states = item["test_states"]
            if test_states:
                decider.decide_batch(test_states[:1], item["question"], level="L2")
            durations = []
            probabilities = None
            for _ in range(request["repeats"]):
                started = time.perf_counter()
                decisions = decider.decide_batch(test_states, item["question"], level="L2")
                durations.append(time.perf_counter() - started)
                probabilities = _decision_probabilities(decisions, len(test_states), item["question"].k, np)
            labels = item["test_labels"]
            predicted = probabilities.argmax(axis=1)
            accuracy = float(np.mean(predicted == labels))
            ece = _expected_calibration_error(probabilities, labels, 15, np)
            median_s = float(np.median(durations))
            if not math.isfinite(median_s) or median_s <= 0:
                raise ServingPipelineError("AnyJev decision timing was invalid")
            spread = (max(durations) - min(durations)) / median_s
            question_results.append({
                "question_id_sha256": item["question_id_sha256"],
                "question_sha256": item["question_sha256"],
                "train_examples": item["train_examples"],
                "test_examples": len(test_states),
                "accuracy": accuracy,
                "ece": ece,
                "fit_ms": max(0.0, fit_ms),
                "single_ms": median_s / len(test_states) * 1000.0,
                "single_spread": spread,
                "head_method": artifact["method"],
            })

        sample_count = min(request["multi_question_states"], questions[0]["test_examples"])
        selected_states = questions[0]["test_states"][:sample_count]
        multi_durations = []
        for _ in range(request["repeats"]):
            started = time.perf_counter()
            for item in questions:
                decider.decide_batch(selected_states, item["question"], level="L2")
            multi_durations.append(time.perf_counter() - started)
        multi_median = float(np.median(multi_durations))
        if not math.isfinite(multi_median) or multi_median <= 0:
            raise ServingPipelineError("AnyJev multi-question timing was invalid")
        return {
            "blocks": blocks,
            "total_blocks": total_blocks,
            "checkpoint_sha256": checkpoint_sha256,
            "questions": question_results,
            "multi_question": {
                "question_count": len(questions),
                "sampled_states": sample_count,
                "median_ms_per_decision": multi_median / (sample_count * len(questions)) * 1000.0,
                "spread": (max(multi_durations) - min(multi_durations)) / multi_median,
            },
        }
    except ServingPipelineError:
        raise
    except Exception:
        raise ServingPipelineError("local AnyJev vLLM inference/measurement failed safely") from None


def _decision_probabilities(decisions, expected_rows, option_count, np):
    if not isinstance(decisions, (list, tuple)) or len(decisions) != expected_rows:
        raise ServingPipelineError("AnyJev returned an invalid decision batch")
    try:
        probabilities = np.asarray([decision.probs for decision in decisions], dtype=np.float64)
    except Exception:
        raise ServingPipelineError("AnyJev returned malformed probability vectors") from None
    if (probabilities.shape != (expected_rows, option_count) or not np.isfinite(probabilities).all() or
            (probabilities < 0).any() or not np.allclose(probabilities.sum(axis=1), 1.0, atol=1e-5)):
        raise ServingPipelineError("AnyJev returned invalid probability vectors")
    return probabilities


def _expected_calibration_error(probabilities, labels, bins, np) -> float:
    confidence = probabilities.max(axis=1)
    predicted = probabilities.argmax(axis=1)
    correct = predicted == labels
    total = len(labels)
    result = 0.0
    for index in range(bins):
        lower, upper = index / bins, (index + 1) / bins
        mask = (confidence >= lower) & ((confidence <= upper) if index == bins - 1 else (confidence < upper))
        if mask.any():
            result += float(mask.sum()) / total * abs(float(correct[mask].mean()) - float(confidence[mask].mean()))
    return result


class LocalVLLMServer:
    """Starts one offline vLLM embedding server bound to IPv4 loopback only."""

    def __init__(self, *, model_path, port, gpu_memory_utilization, prefix_caching,
                 quantization, startup_timeout_seconds, work_directory, log_path):
        self.model_path = Path(model_path)
        self.port = _available_loopback_port(port)
        self.gpu_memory_utilization = gpu_memory_utilization
        self.prefix_caching = prefix_caching
        self.quantization = quantization
        self.startup_timeout_seconds = startup_timeout_seconds
        self.work_directory = Path(work_directory)
        self.log_path = Path(log_path)
        self.url = f"http://127.0.0.1:{self.port}"
        self.process = None
        self._log_stream = None

    def __enter__(self):
        if os.name != "posix":
            raise ServingPipelineError("managed vLLM serving requires POSIX process-group cleanup; use Linux or WSL")
        command = [
            sys.executable, "-m", "vllm.entrypoints.openai.api_server",
            "--model", str(self.model_path), "--served-model-name", SERVED_MODEL_NAME,
            "--host", "127.0.0.1", "--port", str(self.port), "--task", "embed",
            "--gpu-memory-utilization", str(self.gpu_memory_utilization),
            "--override-pooler-config", POOLER_CONFIG,
        ]
        command += ["--enable-prefix-caching"] if self.prefix_caching else ["--no-enable-prefix-caching"]
        if self.quantization:
            command += ["--quantization", self.quantization]
        self._log_stream = self.log_path.open("xb")
        if os.name != "nt":
            os.chmod(self.log_path, 0o600)
        try:
            self.process = subprocess.Popen(
                command,
                cwd=str(self.work_directory),
                env=_offline_environment(),
                stdin=subprocess.DEVNULL,
                stdout=self._log_stream,
                stderr=subprocess.STDOUT,
                close_fds=True,
                start_new_session=True,
            )
            self._wait_until_ready()
            return self
        except Exception:
            self._stop()
            raise ServingPipelineError("local vLLM server failed to start safely") from None

    def __exit__(self, _exc_type, _exc, _traceback):
        self._stop()

    def _wait_until_ready(self):
        deadline = time.monotonic() + self.startup_timeout_seconds
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise ServingPipelineError("local vLLM server exited before becoming ready")
            connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=3.0)
            try:
                connection.request("GET", "/v1/models")
                response = connection.getresponse()
                payload = response.read(MAX_POLL_RESPONSE_BYTES + 1)
                if len(payload) > MAX_POLL_RESPONSE_BYTES or response.status != 200:
                    raise OSError("invalid local vLLM readiness response")
                document = json.loads(payload)
                models = document.get("data") if isinstance(document, dict) else None
                if isinstance(models, list) and any(
                    isinstance(item, dict) and item.get("id") == SERVED_MODEL_NAME for item in models
                ):
                    return
            except (OSError, ValueError, http.client.HTTPException):
                pass
            finally:
                connection.close()
            time.sleep(min(1.0, max(0.05, deadline - time.monotonic())))
        raise ServingPipelineError("local vLLM server did not become ready before the configured timeout")

    def _stop(self):
        process = self.process
        if process is not None and process.poll() is None:
            try:
                os.killpg(process.pid, signal.SIGTERM)
                process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=10)
                except Exception:
                    process.kill()
                    process.wait(timeout=10)
            except ProcessLookupError:
                pass
            except Exception:
                try:
                    process.kill()
                    process.wait(timeout=10)
                except Exception:
                    pass
        self.process = None
        if self._log_stream is not None:
            self._log_stream.close()
            self._log_stream = None


def _available_loopback_port(requested: int) -> int:
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 0)
            probe.bind(("127.0.0.1", requested))
            if requested == 0:
                return int(probe.getsockname()[1])
            return requested
    except OSError:
        raise ServingPipelineError("requested local vLLM port is unavailable") from None


def _offline_environment() -> dict[str, str]:
    allowed = {
        "PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TMPDIR", "TEMP", "TMP",
        "SYSTEMROOT", "WINDIR", "VIRTUAL_ENV", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH",
        "CUDA_HOME", "CUDA_VISIBLE_DEVICES", "NVIDIA_VISIBLE_DEVICES", "ROCR_VISIBLE_DEVICES",
        "XDG_CACHE_HOME", "HF_HOME", "VLLM_CACHE_ROOT",
    }
    environment = {
        key: value for key, value in os.environ.items()
        if key.upper() in allowed and isinstance(value, str) and len(value) <= 8192
    }
    environment.update({
        "HF_HUB_OFFLINE": "1",
        "TRANSFORMERS_OFFLINE": "1",
        "VLLM_NO_USAGE_STATS": "1",
        "DO_NOT_TRACK": "1",
        "TOKENIZERS_PARALLELISM": "false",
    })
    return environment


def _safe_relative_json_path(value: Any) -> bool:
    if (not isinstance(value, str) or not value or len(value) > 4096 or "\\" in value or
            ":" in value or "\x00" in value or value.startswith("/")):
        return False
    parts = value.split("/")
    return (len(parts) <= 64 and value.endswith(".json") and
            all(part not in {"", ".", ".."} and not part.endswith((".", " ")) for part in parts))


def _canonical_bytes(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode("utf-8")


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _is_sha256(value: Any) -> bool:
    return isinstance(value, str) and len(value) == 64 and all(char in "0123456789abcdef" for char in value)
