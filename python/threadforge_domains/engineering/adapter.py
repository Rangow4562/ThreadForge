# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Inventory-backed, fail-closed RRSI adapter for Engineering Design.

This adapter currently supports private inventory access and validated local
result scoring. Benchmark execution remains disabled until a trusted task and
evaluator runtime is pinned and integrated with the task-scoped sandbox.
"""

from __future__ import annotations

import json
import math
import os
import re
import stat
import sys
import hashlib
import time
from numbers import Real
from pathlib import Path

HERE = Path(__file__).resolve().parent
PYTHON_ROOT = HERE.parent.parent
if str(PYTHON_ROOT) not in sys.path:
    sys.path.insert(0, str(PYTHON_ROOT))

from rrsi.domain import Domain  # noqa: E402
from rrsi.evaluate import TaskResult  # noqa: E402
from rrsi.inventory import load_domain_inventory  # noqa: E402
from threadforge_py.protocol import (  # noqa: E402
    EVOLUTION_DOMAIN_RULES_V3,
    _contains_credential,
)
from threadforge_domains.engineering import briefs, render  # noqa: E402
from threadforge_domains.engineering.runtime import (  # noqa: E402
    EVALUATION_OUTPUT_DIRECTORY,
    RuntimePinError,
    collect_engineering_candidate,
    fingerprint_engineering_candidate_harness,
    load_engineering_runtime,
    prepare_engineering_evaluation_workspace,
    prepare_engineering_task_workspace,
)
from threadforge_domains.engineering.security import (  # noqa: E402
    run_sandboxed,
    run_sandboxed_stdio,
    validate_task_workspace,
)

CFG = json.loads((HERE / "rrsi.json").read_text(encoding="utf-8"))
INVENTORY_ENV = "THREADFORGE_RRSI_INVENTORY_BUNDLE"
JOB_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
TASK_COMPONENT = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
STATUS = re.compile(r"[A-Za-z0-9._-]{1,64}\Z")
MAX_TRIALS = 10
MAX_RESULT_BYTES = 2 << 20
MAX_TRAJECTORY_BYTES = 12 << 20
MAX_TOKEN_COUNT = 10**12


def _task_parts(task_id: str) -> tuple[str, ...]:
    if not isinstance(task_id, str) or "\\" in task_id or task_id.startswith("/"):
        raise ValueError("engineering task ID is not a safe relative identifier")
    parts = tuple(task_id.split("/"))
    if not parts or any(part in {"", ".", ".."} or not TASK_COMPONENT.fullmatch(part)
                        for part in parts):
        raise ValueError("engineering task ID is not a safe relative identifier")
    return parts


def _read_json_value(path: Path, root: Path, max_bytes: int) -> object | None:
    """Read a bounded JSON file only when every path component is unlinked."""
    try:
        root = Path(os.path.abspath(root))
        path = Path(os.path.abspath(path))
        relative = path.relative_to(root)
        components = (*root.parts[1:], *relative.parts)
        current = Path(root.anchor)
        identities = []
        for index, component in enumerate(components):
            current = current / component
            info = current.lstat()
            if (stat.S_ISLNK(info.st_mode) or
                    bool(getattr(info, "st_file_attributes", 0) & 0x400)):
                return None
            final = index == len(components) - 1
            if final:
                if not stat.S_ISREG(info.st_mode) or info.st_size > max_bytes:
                    return None
            elif not stat.S_ISDIR(info.st_mode):
                return None
            identities.append((info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns))

        before = identities[-1]
        flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(path, flags)
        with os.fdopen(descriptor, "rb") as stream:
            opened = os.fstat(stream.fileno())
            identity = (opened.st_dev, opened.st_ino, opened.st_size, opened.st_mtime_ns)
            if not stat.S_ISREG(opened.st_mode) or identity != before:
                return None
            raw = stream.read(max_bytes + 1)
            after = os.fstat(stream.fileno())
        after_identity = (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns)
        path_after = path.lstat()
        path_identity = (path_after.st_dev, path_after.st_ino,
                         path_after.st_size, path_after.st_mtime_ns)
        if (len(raw) != before[2] or len(raw) > max_bytes or after_identity != before or
                path_identity != before or stat.S_ISLNK(path_after.st_mode)):
            return None
        return json.loads(raw.decode("utf-8"))
    except (OSError, ValueError, UnicodeDecodeError, json.JSONDecodeError, RecursionError):
        return None


def _read_json(path: Path, root: Path, max_bytes: int = MAX_RESULT_BYTES) -> dict | None:
    value = _read_json_value(path, root, max_bytes)
    return value if isinstance(value, dict) else None


def _bounded_number(value: object) -> float | None:
    if isinstance(value, bool) or not isinstance(value, Real):
        return None
    result = float(value)
    return result if math.isfinite(result) and 0.0 <= result <= 1.0 else None


def _token_count(value: object) -> int | None:
    if isinstance(value, bool) or not isinstance(value, int) or not 0 <= value <= MAX_TOKEN_COUNT:
        return None
    return value


def _directory_exists_without_links(root: Path, target: Path) -> bool:
    """Check an under-root directory path without following links."""
    root = validate_task_workspace(root)
    target = Path(os.path.abspath(target))
    try:
        relative = target.relative_to(root)
    except ValueError:
        raise RuntimePinError("engineering_trial_path_outside_runs_root") from None
    current = root
    for component in relative.parts:
        current = current / component
        try:
            info = current.lstat()
        except FileNotFoundError:
            return False
        if (stat.S_ISLNK(info.st_mode) or
                bool(getattr(info, "st_file_attributes", 0) & 0x400) or
                not stat.S_ISDIR(info.st_mode)):
            raise RuntimePinError("engineering_trial_path_invalid")
    return True


def _ensure_private_directories(root: Path, target: Path) -> None:
    """Create an under-root directory path while rejecting links/reparse points."""
    root = validate_task_workspace(root)
    target = Path(os.path.abspath(target))
    try:
        relative = target.relative_to(root)
    except ValueError:
        raise RuntimePinError("engineering_trial_path_outside_runs_root") from None
    current = root
    for component in relative.parts:
        current = current / component
        try:
            current.mkdir(mode=0o700)
        except FileExistsError:
            pass
        except OSError:
            raise RuntimePinError("engineering_trial_directory_unavailable") from None
        try:
            info = current.lstat()
        except OSError:
            raise RuntimePinError("engineering_trial_path_invalid") from None
        if (stat.S_ISLNK(info.st_mode) or
                bool(getattr(info, "st_file_attributes", 0) & 0x400) or
                not stat.S_ISDIR(info.st_mode)):
            raise RuntimePinError("engineering_trial_path_invalid")


def _write_private_file_new(path: Path, raw: bytes) -> None:
    """Create one no-replace private trial artifact."""
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags, 0o600)
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
    except FileExistsError:
        raise RuntimePinError("engineering_trial_artifact_conflict") from None
    except OSError:
        raise RuntimePinError("engineering_trial_artifact_write_failed") from None


def _json_bytes(value: dict) -> bytes:
    try:
        return json.dumps(value, ensure_ascii=False, allow_nan=False,
                          separators=(",", ":")).encode("utf-8", errors="strict")
    except (TypeError, ValueError, UnicodeEncodeError, RecursionError):
        raise RuntimePinError("engineering_trial_artifact_invalid") from None


def evaluate_engineering_candidate(
    runtime,
    prepared_task,
    *,
    runs_root: str | os.PathLike[str],
    workspace_path: str | os.PathLike[str],
    protected_roots: tuple[str | os.PathLike[str], ...] = (),
) -> dict:
    """Score a submitted payload using the pinned evaluator inside Docker.

    No task-specific evaluator code is imported or executed in the host
    process. Only allowlisted scalar verdict fields leave the isolated run.
    """
    candidate, submitted = collect_engineering_candidate(prepared_task)
    if not submitted or candidate is None:
        return {"status": "no_payload", "passed": False, "valid": 0.0,
                "combined_score": 0.0, "no_payload": True}

    prepared = prepare_engineering_evaluation_workspace(
        runtime,
        task_id=prepared_task.task_id,
        candidate_bytes=candidate,
        runs_root=runs_root,
        workspace_path=workspace_path,
        agent_workspace=prepared_task.workspace,
        protected_roots=protected_roots,
    )
    results_path = f"/task/{EVALUATION_OUTPUT_DIRECTORY}"
    command = (
        "PYTHONDONTWRITEBYTECODE=1 python3 /task/frontier_eval/evaluate_submission.py "
        "--candidate /task/submission/payload.py --benchmark-dir /task "
        f"--metrics-out {results_path}/metrics.json "
        f"--artifacts-out {results_path}/artifacts.json --task-timeout-s 180"
    )
    try:
        result = run_sandboxed(
            runtime.image, prepared.workspace, command,
            benchmark_root=runtime.benchmark_root,
            evaluator_root=runtime.evaluator_root,
            timeout_seconds=240,
            writable_subdirectory=prepared.writable_subdirectory,
        )
    except (OSError, RuntimeError, ValueError):
        return {"status": "evaluator_unavailable", "passed": False, "valid": 0.0,
                "combined_score": 0.0, "no_payload": False}
    if result.timed_out:
        return {"status": "evaluator_timeout", "passed": False, "valid": 0.0,
                "combined_score": 0.0, "no_payload": False}
    if result.returncode != 0:
        return {"status": "evaluator_failed", "passed": False, "valid": 0.0,
                "combined_score": 0.0, "no_payload": False}

    metrics = _read_json(prepared.metrics_path, prepared.workspace, 64 << 10)
    if metrics is None:
        return {"status": "evaluator_output_invalid", "passed": False, "valid": 0.0,
                "combined_score": 0.0, "no_payload": False}
    valid = _bounded_number(metrics.get("valid"))
    score = _bounded_number(metrics.get("combined_score"))
    passed = metrics.get("passed", False if valid == 0.0 else None)
    if (valid not in {0.0, 1.0} or score is None or not isinstance(passed, bool) or
            (valid == 0.0 and (score != 0.0 or passed)) or
            (passed and valid != 1.0)):
        return {"status": "evaluator_output_invalid", "passed": False, "valid": 0.0,
                "combined_score": 0.0, "no_payload": False}
    return {"status": "ok", "passed": passed, "valid": valid,
            "combined_score": score, "no_payload": False}


class EngineeringDomain(Domain):
    name = "engineering"
    harness_path = "../../third_party/archipelago/harness_eng"
    source_exts = {".py", ".txt", ".md", ".json", ".sh"}
    briefs = {"analyst": briefs.ANALYST, "digester": briefs.DIGESTER,
              "proposer": briefs.PROPOSER, "critic": briefs.CRITIC}
    critic_patterns = [
        (pattern, reason.replace("-", " "))
        for reason, pattern in EVOLUTION_DOMAIN_RULES_V3["rrsi-engineering-design"]
    ]
    component_signals = [
        ("memory", [r"DRMH_STATE_DIR", r"digest\("]),
        ("context_mgmt", [r"resum\.py", r"ReSum", r"summariz", r"compact",
                          r"_should_compress", r"keep_last"]),
        ("output_plumbing", [r"tool_result\.py", r"truncat", r"HEAD_CHARS", r"TAIL_CHARS",
                              r"MAX_RESULT_TOKENS", r"_process_result"]),
        ("control_flow", [r"_handle_tool_calls", r"def step", r"final_answer",
                           r"has_incomplete_todos", r"max_steps", r"_execute_mcp_tool"]),
        ("config", [r"= *\d+ *#", r"threshold", r"_CAP\b", r"_LIMIT\b"]),
        ("prompt", [r"prompts\.py", r"SYSTEM_PROMPT", r"TASK_TEMPLATE", r"prompt",
                     r"description"]),
    ]

    def __init__(self):
        self._inventory = None
        self._tasks: list[str] = []
        self._v1_tasks: list[str] = []
        self._base_critic_patterns = list(self.critic_patterns)

    def _load_inventory(self):
        if self._inventory is not None:
            return self._inventory
        bundle = os.environ.get(INVENTORY_ENV)
        if not bundle:
            raise RuntimeError(f"set {INVENTORY_ENV} to a private, source-verified engineering inventory bundle")
        self._inventory = load_domain_inventory(bundle, "rrsi-engineering-design")
        self._tasks = list(self._inventory.evolve_ids)
        self._v1_tasks = list(self._inventory.heldout_ids)
        self.critic_patterns = self._base_critic_patterns + [
            (rf"(?<![\w-]){re.escape(task_id)}(?![\w-])", "engineering task identifier in diff")
            for task_id in dict.fromkeys(self._tasks + self._v1_tasks)
        ]
        return self._inventory

    def surface_ids(self, surface: str) -> list[str]:
        """Return IDs for an explicit benchmark surface; v1 is not an ID holdout."""
        self._load_inventory()
        if surface == "engdesign-open":
            return list(self._tasks)
        if surface == "engdesign-v1":
            return list(self._v1_tasks)
        raise ValueError("unknown engineering benchmark surface")

    def evolve_ids(self) -> list[str]:
        self._load_inventory()
        return list(self._tasks)

    def heldout_ids(self) -> list[str]:
        # EngDesign v1 hardens the same IDs; it is an explicit OOD surface and
        # cannot be represented as a distinct ID-based split to the RRSI loop.
        self._load_inventory()
        return []

    def smoke_ids(self, incumbent_per_task=None) -> list[str]:
        ids = self.evolve_ids()
        count = max(1, min(len(ids), int(CFG.get("smoke_n", 4))))
        if incumbent_per_task:
            solid = [task_id for task_id in ids if task_id in incumbent_per_task
                     and min(incumbent_per_task[task_id].rewards or [0.0]) >= 1.0]
            if len(solid) >= count:
                return solid[:count]
        return ids[:count]

    def regression_threshold(self, k: int) -> float:
        return 1.0 / max(1, k)

    @staticmethod
    def _safe_job(job: str, k: int) -> None:
        if not isinstance(job, str) or not JOB_ID.fullmatch(job):
            raise ValueError("engineering job ID must be a bounded path-safe identifier")
        if isinstance(k, bool) or not isinstance(k, int) or not 1 <= k <= MAX_TRIALS:
            raise ValueError(f"engineering trial count must be between 1 and {MAX_TRIALS}")

    def _validate_ids(self, ids: list[str]) -> None:
        self._load_inventory()
        if (not isinstance(ids, list) or not ids or
                any(not isinstance(task_id, str) for task_id in ids) or
                len(ids) != len(set(ids))):
            raise ValueError("engineering evaluation requires a nonempty unique verified task set")
        if not set(ids) <= set(self._tasks):
            raise ValueError("engineering RRSI runs accept only the verified EngDesign-Open evolve surface")
        for task_id in ids:
            _task_parts(task_id)

    def _require_benchmark_execution(self) -> None:
        if not isinstance(self.benchmark_execution_opt_in, bool):
            raise ValueError("benchmark_execution_opt_in must be a boolean")
        if not self.benchmark_execution_opt_in:
            raise PermissionError("engineering benchmark execution requires benchmark_execution_opt_in=true")

    def _verified_runtime(self, root, runs_dir):
        values = {
            "benchmark_root": os.environ.get("THREADFORGE_ENGINEERING_BENCHMARK_ROOT", ""),
            "evaluator_root": os.environ.get("THREADFORGE_ENGINEERING_EVALUATOR_ROOT", ""),
            "pin_path": os.environ.get("THREADFORGE_ENGINEERING_RUNTIME_PIN", ""),
            "task_ids": self.evolve_ids(),
            "image": os.environ.get("THREADFORGE_ENGINEERING_EXECUTION_IMAGE", ""),
            "protected_roots": (root, runs_dir),
        }
        if not all(values[key] for key in (
            "benchmark_root", "evaluator_root", "pin_path", "image",
        )):
            raise RuntimePinError("engineering_runtime_pin_required")
        return load_engineering_runtime(**values)

    def run(self, root, runs_dir, job, ids, k, log_prefix=""):
        self._require_benchmark_execution()
        self._safe_job(job, k)
        self._validate_ids(ids)
        runtime = self._verified_runtime(root, runs_dir)
        candidate_root = Path(root).expanduser().resolve(strict=True)
        runs_root = validate_task_workspace(runs_dir)
        harness_source = self.harness_dir(candidate_root).resolve(strict=True)
        if not harness_source.is_relative_to(candidate_root):
            raise RuntimePinError("candidate_harness_source_invalid")
        harness_sha256, _harness_file_count, _harness_bytes = (
            fingerprint_engineering_candidate_harness(harness_source)
        )

        # Preflight every target before the first model/container side effect.
        # Existing complete trials are resumable; partial or linked state is
        # preserved and requires a new job ID rather than being overwritten.
        plan = []
        for task_id in ids:
            task_key = hashlib.sha256(task_id.encode("utf-8")).hexdigest()[:20]
            for trial in range(k):
                trial_dir = self._trial_dir(runs_root, job, task_id, trial)
                if _directory_exists_without_links(runs_root, trial_dir):
                    if self._read_trial(runs_root, job, task_id, trial) is None:
                        raise RuntimePinError("engineering_trial_incomplete_or_invalid")
                    plan.append((task_id, trial, trial_dir, None, None, True))
                    continue
                workspace_base = runs_root / "workspaces" / job / task_key
                agent_workspace = workspace_base / f"t{trial}-agent"
                evaluation_workspace = workspace_base / f"t{trial}-evaluator"
                if (_directory_exists_without_links(runs_root, agent_workspace) or
                        _directory_exists_without_links(runs_root, evaluation_workspace)):
                    raise RuntimePinError("engineering_trial_workspace_already_exists")
                plan.append((task_id, trial, trial_dir, agent_workspace,
                             evaluation_workspace, False))

        for task_id, trial, trial_dir, agent_workspace, evaluation_workspace, complete in plan:
            if complete:
                continue
            trial_started = time.monotonic()
            prepared = prepare_engineering_task_workspace(
                runtime,
                task_id=task_id,
                runs_root=runs_root,
                workspace_path=agent_workspace,
                protected_roots=(candidate_root,),
                candidate_harness_source=harness_source,
            )
            if prepared.candidate_harness_sha256 != harness_sha256:
                raise RuntimePinError("candidate_harness_source_changed")

            trajectory_id = f"engd-{hashlib.sha256(job.encode()).hexdigest()[:12]}-{hashlib.sha256(task_id.encode()).hexdigest()[:12]}-t{trial}"
            command = (
                "python3 -m threadforge_domains.engineering.container_runtime "
                f"--trajectory-id {trajectory_id} --timeout-seconds 5400 "
                "--max-steps 250 --llm-timeout-seconds 600"
            )
            result = run_sandboxed_stdio(
                runtime.image,
                prepared.workspace,
                command,
                benchmark_root=runtime.benchmark_root,
                evaluator_root=runtime.evaluator_root,
                timeout_seconds=5_400,
                remote_inference_opt_in=self.remote_inference_opt_in,
            )
            candidate_bytes, submitted = collect_engineering_candidate(prepared)
            verdict = evaluate_engineering_candidate(
                runtime,
                prepared,
                runs_root=runs_root,
                workspace_path=evaluation_workspace,
                protected_roots=(candidate_root,),
            )
            observed_harness_sha256, _count, _byte_count = (
                fingerprint_engineering_candidate_harness(harness_source)
            )
            if observed_harness_sha256 != harness_sha256:
                raise RuntimePinError("candidate_harness_source_changed")

            if result.timed_out or result.returncode == 21:
                status = "timeout"
            elif result.protocol_error:
                status = "broker_protocol_error"
            elif result.broker_error:
                status = "broker_error"
            elif result.returncode == 0:
                status = "completed"
            elif result.returncode == 20:
                status = "agent_failed"
            else:
                status = "container_failed"

            total_tokens = (
                result.prompt_tokens + result.completion_tokens
                if result.model_calls > 0 and result.unmeasured_calls == 0 else None
            )
            meta = {
                "status": status,
                "no_payload": not submitted,
                "total_tokens": total_tokens,
                "model_calls": result.model_calls,
                "failed_model_calls": result.failed_model_calls,
                "prompt_tokens": result.prompt_tokens,
                "completion_tokens": result.completion_tokens,
                "unmeasured_model_calls": result.unmeasured_calls,
                "broker_requests_handled": result.requests_handled,
                "container_returncode": result.returncode,
                "container_timed_out": result.timed_out,
                "protocol_error": result.protocol_error,
                "broker_error": result.broker_error,
                "stderr_sha256": result.stderr_sha256,
                "stderr_bytes": result.stderr_bytes,
                "stderr_truncated": result.stderr_truncated,
                "runtime_pin_sha256": runtime.pin_sha256,
                "candidate_harness_sha256": harness_sha256,
                "agent_workspace_sha256": prepared.workspace_sha256,
                "trajectory_untrusted": True,
                "duration_seconds": round(time.monotonic() - trial_started, 3),
            }
            trajectory_path = prepared.workspace / "output" / ".threadforge-agent-result.json"
            trajectory = _read_json(trajectory_path, prepared.workspace, MAX_TRAJECTORY_BYTES)

            _ensure_private_directories(runs_root, trial_dir.parent)
            try:
                trial_dir.mkdir(mode=0o700)
            except FileExistsError:
                raise RuntimePinError("engineering_trial_artifact_conflict") from None
            except OSError:
                raise RuntimePinError("engineering_trial_directory_unavailable") from None
            if candidate_bytes is not None:
                _write_private_file_new(trial_dir / "payload.py", candidate_bytes)
            if trajectory is not None:
                _write_private_file_new(trial_dir / "traj.json", _json_bytes(trajectory))
            _write_private_file_new(trial_dir / "verdict.json", _json_bytes(verdict))
            # meta.json is the last-written commit marker; a partial record is
            # never mistaken for a completed trial by the resume path.
            _write_private_file_new(trial_dir / "meta.json", _json_bytes(meta))

    def _trial_dir(self, runs_dir: Path, job: str, task_id: str, trial: int) -> Path:
        self._load_inventory()
        if task_id not in {*self._tasks, *self._v1_tasks}:
            raise ValueError("engineering evidence task is outside the verified inventory")
        parts = _task_parts(task_id)
        self._safe_job(job, 1)
        if isinstance(trial, bool) or not isinstance(trial, int) or not 0 <= trial < MAX_TRIALS:
            raise ValueError("engineering trial index is invalid")
        return Path(runs_dir) / "jobs" / job / Path(*parts) / f"t{trial}"

    def _read_trial(self, runs_dir: Path, job: str, task_id: str, trial: int) -> dict | None:
        folder = self._trial_dir(runs_dir, job, task_id, trial)
        root = Path(os.path.abspath(runs_dir))
        meta = _read_json(folder / "meta.json", root)
        verdict = _read_json(folder / "verdict.json", root)
        if meta is None or verdict is None:
            return None
        no_payload = meta.get("no_payload")
        meta_status = meta.get("status")
        status = verdict.get("status")
        passed = verdict.get("passed")
        valid = _bounded_number(verdict.get("valid"))
        combined_score = _bounded_number(verdict.get("combined_score"))
        if (not isinstance(no_payload, bool) or not isinstance(meta_status, str) or
                not STATUS.fullmatch(meta_status) or not isinstance(status, str) or
                not STATUS.fullmatch(status) or not isinstance(passed, bool) or
                valid is None or combined_score is None):
            return None
        if (status != "ok" and (passed or valid != 0.0 or combined_score != 0.0)):
            return None
        if no_payload and (passed or valid != 0.0 or combined_score != 0.0):
            return None
        if passed and valid != 1.0:
            return None
        return {
            "status": status,
            "passed": passed if status == "ok" else False,
            "valid": valid,
            "combined_score": combined_score,
            "no_payload": no_payload,
            "tokens": _token_count(meta.get("total_tokens")),
            # Do not surface unknown metadata, provider labels, error strings,
            # or evaluator detail to the proposer/analyst roles.
            "meta": {"status": meta_status, "no_payload": no_payload,
                     "total_tokens": _token_count(meta.get("total_tokens"))},
            "verdict": {"status": status, "passed": passed if status == "ok" else False,
                        "valid": valid, "combined_score": combined_score},
        }

    def score(self, runs_dir, job, ids, k):
        self._safe_job(job, k)
        self._validate_ids(ids)
        runs_dir = Path(runs_dir)
        per_task = {}
        passes = 0
        valid_sum = no_payload_sum = combined_sum = 0.0
        for task_id in ids:
            rewards, tokens, combined, valid, no_payload = [], [], [], [], []
            missing = 0
            for trial in range(k):
                record = self._read_trial(runs_dir, job, task_id, trial)
                if record is None:
                    missing += 1
                    rewards.append(0.0)
                    tokens.append(None)
                    combined.append(0.0)
                    valid.append(0.0)
                    no_payload.append(1.0)
                    continue
                reward = 1.0 if record["passed"] else 0.0
                rewards.append(reward)
                tokens.append(record["tokens"])
                combined.append(record["combined_score"])
                valid.append(record["valid"])
                no_payload.append(1.0 if record["no_payload"] else 0.0)
                passes += int(reward)
            combined_sum += sum(combined)
            valid_sum += sum(valid)
            no_payload_sum += sum(no_payload)
            per_task[task_id] = TaskResult(
                rewards=rewards, tokens=tokens, missing=missing,
                extra={"combined_scores": combined, "valid": valid, "no_payload": no_payload},
            )
        total = len(ids) * k
        return per_task, {
            "total_passes": passes,
            "n_trials": total,
            "pass_rate": passes / total,
            "mean_combined_score": combined_sum / total,
            "valid_rate": valid_sum / total,
            "no_payload_rate": no_payload_sum / total,
        }

    def guards(self, incumbent, candidate) -> list[str]:
        out = []
        valid_drop = (incumbent.extra.get("valid_rate", 0.0) -
                      candidate.extra.get("valid_rate", 0.0))
        valid_limit = float(CFG["max_valid_rate_drop"])
        if valid_drop > valid_limit:
            out.append(f"valid rate fell {valid_drop:+.3f} (limit {valid_limit})")
        no_payload_rise = (candidate.extra.get("no_payload_rate", 0.0) -
                           incumbent.extra.get("no_payload_rate", 0.0))
        no_payload_limit = float(CFG["max_no_payload_rise"])
        if no_payload_rise > no_payload_limit:
            out.append(f"no-submission rate rose {no_payload_rise:+.3f} (limit {no_payload_limit})")
        return out

    def load_trial(self, runs_dir, job, task_id, trial):
        folder = self._trial_dir(Path(runs_dir), job, task_id, trial)
        record = self._read_trial(Path(runs_dir), job, task_id, trial)
        if record is None:
            return None
        root = Path(os.path.abspath(runs_dir))
        trajectory = _read_json(folder / "traj.json", root, MAX_TRAJECTORY_BYTES) or {}
        if not trajectory or _contains_credential(trajectory):
            return None
        return {"task_id": task_id, "trial": trial, "meta": record["meta"],
                "verdict": record["verdict"], "trajectory": trajectory}

    def render_trace(self, rec, detail=False):
        return render.render_full(rec, detail=detail)

    def task_row(self, task_id, rec, tr):
        meta = rec.get("meta") or {}
        verdict = rec.get("verdict") or {}
        status = verdict.get("status") or "missing"
        if meta.get("no_payload"):
            outcome = "NO-SUBMISSION"
        elif status != "ok":
            outcome = f"INFRA({status})"
        elif verdict.get("passed"):
            outcome = "passed"
        elif float(verdict.get("valid") or 0.0) < 1.0:
            outcome = "INVALID-DESIGN"
        else:
            outcome = "UNDER-TARGET"
        return (f"{task_id} | {outcome} | pass_rate={tr.mean:.2f} "
                f"score={float(verdict.get('combined_score') or 0.0):.3f} "
                f"valid={verdict.get('valid')} | status={meta.get('status')} "
                f"| tokens={meta.get('total_tokens')}")

    def smoke(self, root, runs_dir, job, ids):
        try:
            self._require_benchmark_execution()
            self._safe_job(job, 1)
            self._validate_ids(ids)
        except PermissionError:
            return False, {"stage": "preflight", "reason_code": "benchmark_execution_opt_in_required",
                           "benchmark_execution_performed": False}
        except (OSError, RuntimeError, ValueError):
            return False, {"stage": "preflight", "reason_code": "engineering_inventory_or_request_invalid",
                           "benchmark_execution_performed": False}
        try:
            runtime = self._verified_runtime(root, runs_dir)
        except RuntimePinError as exc:
            return False, {"stage": "runtime_pin", "reason_code": exc.reason_code,
                           "benchmark_execution_performed": False}
        try:
            # Smoke is a real one-trial task run, not a host-side import/probe.
            # The same explicit execution opt-in covers any model or evaluator
            # calls; all candidate code remains inside the pinned container.
            self.run(root, runs_dir, job, ids, 1, log_prefix=job)
            per_task, aggregate = self.score(runs_dir, job, ids, 1)
        except (OSError, RuntimeError, ValueError, PermissionError) as exc:
            reason = getattr(exc, "reason_code", "engineering_smoke_run_failed")
            return False, {
                "stage": "run",
                "reason_code": reason,
                "runtime_pin_sha256": runtime.pin_sha256,
                "benchmark_execution_performed": True,
                "model_invocations": None,
            }
        missing = sum(result.missing for result in per_task.values())
        passed = (missing == 0 and aggregate["no_payload_rate"] == 0.0 and
                  aggregate["mean_combined_score"] > 0.0)
        return passed, {
            "stage": "complete" if passed else "evaluation",
            "reason_code": "engineering_smoke_passed" if passed else "engineering_smoke_failed",
            "runtime_pin_sha256": runtime.pin_sha256,
            "task_count": len(ids),
            "trial_count": aggregate["n_trials"],
            "missing_trials": missing,
            "pass_rate": aggregate["pass_rate"],
            "valid_rate": aggregate["valid_rate"],
            "no_payload_rate": aggregate["no_payload_rate"],
            "benchmark_execution_performed": True,
        }


DOMAIN = EngineeringDomain()
