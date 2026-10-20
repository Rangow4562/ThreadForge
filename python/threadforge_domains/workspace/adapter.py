# Copyright 2026 The rrsi Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
# Modified by Threadforge: replaces generated splits with source-verified
# private inventories and adds consent, integrity, and safe-runner checks.
"""Harvey LAB RRSI adapter using private, source-verified split IDs.

Task artifacts stay under the caller's local runs directory. The policy model
defaults to loopback inference; the official Harvey rubric judge is remote and
is reachable only when ``remote_evaluation_opt_in`` is true.
"""

from __future__ import annotations

import hashlib
import json
import math
import os
import re
import signal
import shutil
import socket
import stat
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
PYTHON_ROOT = HERE.parent.parent
if str(PYTHON_ROOT) not in sys.path:
    sys.path.insert(0, str(PYTHON_ROOT))

from rrsi.domain import Domain  # noqa: E402
from rrsi.evaluate import TaskResult  # noqa: E402
from rrsi.inventory import load_domain_inventory  # noqa: E402
from rrsi.modelpolicy import evaluation_runtime  # noqa: E402
from threadforge_domains.workspace import briefs, render  # noqa: E402

CFG = json.loads((HERE / "rrsi.json").read_text(encoding="utf-8"))
INVENTORY_ENV = "THREADFORGE_RRSI_INVENTORY_BUNDLE"
HARVEY_LAB_ROOT = Path(os.environ.get("HARVEY_LAB_ROOT", "harvey-labs")).expanduser()
HARVEY_COMMIT = "1da4750171bc5a534960b3d82d15ba7fd2cf653f"
JOB_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
MAX_TRIALS = 10
MAX_JSON_BYTES = 16 << 20


def _name(task_id: str) -> str:
    return task_id.replace("/", "__")


def _read_json_value(path: Path, max_bytes: int) -> object | None:
    try:
        info = path.lstat()
        if (stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode) or
                bool(getattr(info, "st_file_attributes", 0) & 0x400) or
                info.st_size > max_bytes):
            return None
        identity = (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns)
        flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(path, flags)
        with os.fdopen(descriptor, "rb") as stream:
            opened = os.fstat(stream.fileno())
            if (not stat.S_ISREG(opened.st_mode) or
                    (opened.st_dev, opened.st_ino, opened.st_size, opened.st_mtime_ns) != identity):
                return None
            raw = stream.read(max_bytes + 1)
            after = os.fstat(stream.fileno())
        path_after = path.lstat()
        after_identity = (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns)
        path_identity = (path_after.st_dev, path_after.st_ino,
                         path_after.st_size, path_after.st_mtime_ns)
        if (len(raw) != info.st_size or len(raw) > max_bytes or after_identity != identity or
                path_identity != identity or stat.S_ISLNK(path_after.st_mode)):
            return None
        return json.loads(raw.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError, RecursionError):
        return None


def _read_json(path: Path, max_bytes: int = MAX_JSON_BYTES) -> dict:
    value = _read_json_value(path, max_bytes)
    return value if isinstance(value, dict) else {}


def _read_task_manifest(path: Path) -> list[str] | None:
    value = _read_json_value(path, 2 << 20)
    if (not isinstance(value, list) or any(not isinstance(item, str) for item in value) or
            len(value) != len(set(value))):
        return None
    return value


class WorkspaceDomain(Domain):
    name = "workspace"
    harness_path = "../../third_party/archipelago/harness_workspace"
    briefs = {"analyst": briefs.ANALYST, "digester": briefs.DIGESTER,
              "proposer": briefs.PROPOSER, "critic": briefs.CRITIC}
    critic_patterns = [
        (r"task_[0-9a-f]{32}", "task id in diff"),
        (r"world_[0-9a-f]{32}", "world id in diff"),
        (r"llm_judge|score_rubric|criteria_results", "judge reference in diff"),
        (r"task\.json", "task.json (rubric carrier) referenced in diff"),
    ]
    component_signals = [
        ("memory", [r"DRMH_STATE_DIR", r"digest\("]),
        ("context_mgmt", [r"resum\.py", r"ReSum", r"summariz", r"compact", r"keep_last"]),
        ("output_plumbing", [r"tool_result\.py", r"truncat", r"HEAD_CHARS", r"TAIL_CHARS"]),
        ("control_flow", [r"_handle_tool_calls", r"def step", r"final_answer", r"max_steps"]),
        ("config", [r"= *\d+ *#", r"threshold", r"_CAP\b", r"_LIMIT\b"]),
        ("prompt", [r"SYSTEM_PROMPT", r"WRAPPER_TEMPLATE", r"prompt", r"description"]),
    ]

    def __init__(self):
        self._inventory = None
        self._tasks: list[str] = []
        self._heldout_tasks: list[str] = []
        self._criteria_count: dict[str, int] = {}
        self._base_critic_patterns = list(self.critic_patterns)

    @staticmethod
    def _runtime_paths(root: Path) -> tuple[Path, Path]:
        root = Path(root)
        source_domain = root / "domains" / "workspace"
        source_archipelago = root / "third_party" / "archipelago"
        if ((source_domain / "produce.sh").is_file() and
                (source_domain / "workspace_driver.py").is_file() and
                (source_archipelago / "harness_workspace" / "main.py").is_file()):
            return source_domain, source_archipelago

        packaged_runtime = HERE / "runtime"
        packaged_domain = packaged_runtime / "domain"
        packaged_archipelago = packaged_runtime / "archipelago"
        if ((packaged_domain / "produce.sh").is_file() and
                (packaged_domain / "workspace_driver.py").is_file() and
                (packaged_archipelago / "harness_workspace" / "main.py").is_file()):
            return packaged_domain, packaged_archipelago
        raise RuntimeError("workspace runner assets are unavailable in the source tree and installed package")

    def _load_inventory(self):
        if self._inventory is not None:
            return self._inventory
        bundle = os.environ.get(INVENTORY_ENV)
        if not bundle:
            raise RuntimeError(
                f"set {INVENTORY_ENV} to a private, source-verified workspace inventory bundle"
            )
        self._inventory = load_domain_inventory(bundle, "rrsi-agentic-workspace")
        self._tasks = list(self._inventory.evolve_ids)
        self._heldout_tasks = list(self._inventory.heldout_ids)
        self.critic_patterns = self._base_critic_patterns + [
            (rf"(?<![\w-]){re.escape(task_id)}(?![\w-])", "benchmark task identifier in diff")
            for task_id in self._tasks + self._heldout_tasks
        ]
        return self._inventory

    def _split(self, ids: list[str]) -> str:
        self._load_inventory()
        if (not isinstance(ids, list) or not ids or
                any(not isinstance(task_id, str) for task_id in ids) or
                len(ids) != len(set(ids))):
            raise ValueError("workspace evaluation requires a nonempty unique verified task set")
        selected = set(ids)
        if selected <= set(self._tasks):
            return "evolve"
        if selected <= set(self._heldout_tasks):
            return "held-out"
        raise ValueError("workspace evaluation IDs must belong to one verified split")

    def evolve_ids(self) -> list[str]:
        self._load_inventory()
        return list(self._tasks)

    def heldout_ids(self) -> list[str]:
        self._load_inventory()
        return list(self._heldout_tasks)

    def smoke_ids(self, incumbent_per_task=None) -> list[str]:
        ids = self.evolve_ids()
        count = max(1, min(len(ids), int(CFG.get("smoke_n", 2))))
        return ids[:count]

    def regression_threshold(self, k: int) -> float:
        return 0.05

    def _harvey_root(self) -> Path:
        return Path(os.environ.get("HARVEY_LAB_ROOT", HARVEY_LAB_ROOT)).expanduser().resolve()

    def _verify_harvey_checkout(self) -> Path:
        root = self._harvey_root()
        if not root.is_dir():
            raise RuntimeError("HARVEY_LAB_ROOT must name the pinned local Harvey LAB checkout")
        result = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "HEAD"],
            capture_output=True, text=True, timeout=10, check=False,
        )
        if result.returncode or result.stdout.strip() != HARVEY_COMMIT:
            raise RuntimeError("Harvey LAB checkout does not match the pinned source revision")
        task_changes = subprocess.run(
            ["git", "-C", str(root), "status", "--porcelain", "--ignored=matching",
             "--untracked-files=all", "--", "tasks"],
            capture_output=True, text=True, timeout=30, check=False,
        )
        if task_changes.returncode or task_changes.stdout.strip():
            raise RuntimeError("Harvey LAB task tree has local changes; evaluation refused")
        return root

    def _harvey_python(self, root: Path) -> str:
        configured = os.environ.get("HARVEY_PY")
        candidates = [Path(configured)] if configured else [
            root / ".venv" / "bin" / "python", root / ".venv" / "Scripts" / "python.exe",
        ]
        for candidate in candidates:
            if candidate.is_file():
                return str(candidate)
        raise RuntimeError("set HARVEY_PY to the pinned Harvey LAB Python environment")

    def _task_json(self, task_id: str) -> dict:
        root = self._harvey_root()
        parts = task_id.split("/")
        if any(part in {"", ".", ".."} or "\\" in part for part in parts):
            return {}
        tasks_root = root / "tasks"
        current = tasks_root
        try:
            for part in (*parts, "task.json"):
                current = current / part
                info = current.lstat()
                if stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & 0x400):
                    return {}
            if not current.resolve().is_relative_to(tasks_root.resolve()):
                return {}
        except (OSError, ValueError):
            return {}
        return _read_json(current, 2 << 20)

    def _n_criteria(self, task_id: str) -> int:
        if task_id not in self._criteria_count:
            criteria = self._task_json(task_id).get("criteria")
            count = len(criteria) if isinstance(criteria, list) else 0
            if not 1 <= count <= 1000:
                raise RuntimeError("pinned Harvey task criteria are unavailable or invalid")
            self._criteria_count[task_id] = count
        return self._criteria_count[task_id]

    def _runtime_env(self, root: Path, runs_dir: Path, job: str) -> dict[str, str]:
        model, env = evaluation_runtime(self.remote_inference_opt_in)
        port_seed = hashlib.sha256(job.encode("utf-8")).digest()
        port = int(CFG["gateway_port"]) + int.from_bytes(port_seed[:2], "big") % 400
        agent_python = os.environ.get("RRSI_AGENT_PYTHON", sys.executable)
        _domain_dir, archipelago = self._runtime_paths(root)
        pythonpath = os.pathsep.join(filter(None, (
            str(PYTHON_ROOT), str(archipelago), env.get("PYTHONPATH", ""),
        )))
        env.update({
            "RRSI_JOBS_DIR": str(runs_dir / "jobs"),
            "RRSI_HARNESS_MODULE": "harness_workspace.main",
            "RRSI_WORKSPACES": str(runs_dir / "workspaces"),
            "RRSI_LOGS": str(runs_dir / "logs"),
            "HARVEY_LAB_ROOT": str(self._harvey_root()),
            "RRSI_AGENT_PYTHON": agent_python,
            "VENV": agent_python,
            "PYTHONPATH": pythonpath,
            "GATEWAY_HOST": "127.0.0.1",
            "GATEWAY_PORT": str(port),
            "MAX_CONCURRENT": str(CFG["max_concurrent"]),
            "ORCHESTRATOR_MODEL": model,
            "CODE_EXEC_PYTHON": os.environ.get("CODE_EXEC_PYTHON", agent_python),
            "ENABLE_WEB_SEARCH": "0",
            "THREADFORGE_RRSI_REMOTE_INFERENCE_OPT_IN":
                "true" if self.remote_inference_opt_in else "false",
            "THREADFORGE_BENCHMARK_EXECUTION_OPT_IN":
                "true" if self.benchmark_execution_opt_in else "false",
        })
        return env

    @staticmethod
    def _safe_job(job: str, k: int) -> None:
        if not isinstance(job, str) or not JOB_ID.fullmatch(job):
            raise ValueError("workspace job ID must be a bounded path-safe identifier")
        if isinstance(k, bool) or not isinstance(k, int) or not 1 <= k <= MAX_TRIALS:
            raise ValueError(f"workspace trial count must be between 1 and {MAX_TRIALS}")

    def _require_benchmark_execution(self) -> None:
        if not isinstance(self.benchmark_execution_opt_in, bool):
            raise ValueError("benchmark_execution_opt_in must be a boolean")
        if not self.benchmark_execution_opt_in:
            raise PermissionError("workspace benchmark execution requires benchmark_execution_opt_in=true")

    def _require_remote_judge(self) -> str:
        if not isinstance(self.remote_evaluation_opt_in, bool):
            raise ValueError("remote_evaluation_opt_in must be a boolean")
        if not self.remote_evaluation_opt_in:
            raise PermissionError(
                "Harvey LAB uses a remote rubric judge; set remote_evaluation_opt_in=true"
            )
        project = os.environ.get("VERTEX_PROJECT") or os.environ.get("GOOGLE_CLOUD_PROJECT")
        if not project:
            raise RuntimeError("set VERTEX_PROJECT for the explicitly enabled remote Harvey judge")
        return project

    def _verify_local_runner(self, root: Path, harvey_root: Path) -> None:
        if os.name != "posix" or shutil.which("bash") is None:
            raise RuntimeError("Harvey workspace runner requires a POSIX host with bash")
        agent_python = os.environ.get("RRSI_AGENT_PYTHON", sys.executable)
        if not Path(agent_python).is_file():
            raise RuntimeError("RRSI_AGENT_PYTHON must name an installed executable")
        judge_python = self._harvey_python(harvey_root)
        _domain_dir, archipelago = self._runtime_paths(root)
        harness = archipelago / "harness_workspace" / "main.py"
        if not harness.is_file():
            raise RuntimeError("candidate worktree is missing its vendored Archipelago workspace harness")
        image = os.environ.get("THREADFORGE_CODE_EXEC_IMAGE", "")
        from threadforge_domains.workspace.security import valid_pinned_image

        if not valid_pinned_image(image):
            raise RuntimeError("set THREADFORGE_CODE_EXEC_IMAGE to a preloaded immutable sha256 image")
        docker = shutil.which("docker")
        if not docker:
            raise RuntimeError("Docker is required for isolated workspace code execution")
        inspected = subprocess.run(
            [docker, "image", "inspect", image], capture_output=True, text=True,
            timeout=20, check=False,
        )
        if inspected.returncode:
            raise RuntimeError("configured workspace execution image is not present locally")
        imports = subprocess.run(
            [judge_python, "-c", "from evaluation.judge import Judge; from evaluation.scoring import score_rubric"],
            cwd=harvey_root, capture_output=True, text=True, timeout=30, check=False,
        )
        if imports.returncode:
            raise RuntimeError("HARVEY_PY is missing the pinned Harvey rubric evaluator")
        model, env = evaluation_runtime(self.remote_inference_opt_in)
        del model
        if env.get("THREADFORGE_EVALUATION_BACKEND", "local") == "local":
            from urllib.parse import urlparse

            parsed = urlparse(env["OPENAI_BASE_URL"])
            try:
                with socket.create_connection((parsed.hostname, parsed.port or 80), timeout=1):
                    pass
            except (OSError, TypeError, ValueError) as exc:
                raise RuntimeError("configured loopback policy model is unavailable") from exc

    def _produce(self, root: Path, runs_dir: Path, label: str, ids: list[str]) -> int:
        domain_dir, _archipelago = self._runtime_paths(root)
        script = domain_dir / "produce.sh"
        jobs = runs_dir / "jobs"
        task_file = jobs / label / "_tasks.json"
        task_file.parent.mkdir(parents=True, exist_ok=True)
        try:
            with task_file.open("x", encoding="utf-8") as stream:
                json.dump(ids, stream)
        except FileExistsError:
            raise FileExistsError("workspace task manifest already exists; refusing to overwrite") from None
        log = runs_dir / "logs" / f"{label.replace('/', '_')}.produce.log"
        log.parent.mkdir(parents=True, exist_ok=True)
        env = self._runtime_env(root, runs_dir, label)
        timeout = int(os.environ.get("THREADFORGE_WORKSPACE_PRODUCE_TIMEOUT", "86400"))
        if not 60 <= timeout <= 172800:
            raise ValueError("workspace production timeout must be between 60 and 172800 seconds")
        with log.open("ab") as stream:
            try:
                process = subprocess.Popen(
                    ["bash", str(script), label, str(task_file)], cwd=domain_dir,
                    env=env, stdout=stream, stderr=subprocess.STDOUT,
                    start_new_session=(os.name == "posix"),
                )
                try:
                    process.wait(timeout=timeout)
                except subprocess.TimeoutExpired:
                    if os.name == "posix":
                        try:
                            os.killpg(process.pid, signal.SIGTERM)
                        except ProcessLookupError:
                            pass
                    else:
                        process.terminate()
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        if os.name == "posix":
                            try:
                                os.killpg(process.pid, signal.SIGKILL)
                            except ProcessLookupError:
                                pass
                        else:
                            process.kill()
                        process.wait()
            except OSError:
                return 0
        return sum(
            1 for task_id in ids
            if (jobs / label / _name(task_id) / "trajectory.json").is_file()
        )

    def _judge(self, root: Path, runs_dir: Path, label: str, harvey_root: Path) -> bool:
        project = self._require_remote_judge()
        judge_python = self._harvey_python(harvey_root)
        domain_dir, _archipelago = self._runtime_paths(root)
        log = runs_dir / "logs" / f"{label.replace('/', '_')}.judge.log"
        log.parent.mkdir(parents=True, exist_ok=True)
        env = dict(os.environ)
        env.update({
            "GOOGLE_GENAI_USE_VERTEXAI": "true",
            "GOOGLE_CLOUD_PROJECT": project,
            "GOOGLE_CLOUD_LOCATION": os.environ.get("GOOGLE_CLOUD_LOCATION", "global"),
            "HARVEY_LAB_ROOT": str(harvey_root),
            "RRSI_JOBS_DIR": str(runs_dir / "jobs"),
            "THREADFORGE_RRSI_REMOTE_JUDGE_OPT_IN": "true",
        })
        with log.open("ab") as stream:
            try:
                result = subprocess.run(
                    [judge_python, str(domain_dir / "workspace_judge.py"), label,
                     "--judge-model", str(CFG["judge_model"])],
                    cwd=domain_dir, env=env, stdout=stream, stderr=subprocess.STDOUT,
                    timeout=3600, check=False,
                )
            except (OSError, subprocess.TimeoutExpired):
                return False
        summary = _read_json(runs_dir / "jobs" / label / "SUMMARY.json", 1 << 20)
        return result.returncode == 0 and summary.get("label") == label

    def _job_complete(self, runs_dir: Path, job: str, ids: list[str], k: int) -> bool:
        for trial in range(k):
            task_dir = runs_dir / "jobs" / job / f"s{trial}"
            if _read_task_manifest(task_dir / "_tasks.json") != ids:
                return False
            for task_id in ids:
                folder = task_dir / _name(task_id)
                if (not _read_json(folder / "trajectory.json") or
                        self._score_record(folder / "scores.json", task_id) is None):
                    return False
        return True

    def run(self, root, runs_dir, job, ids, k, log_prefix=""):
        self._require_benchmark_execution()
        self._safe_job(job, k)
        self._split(ids)
        self._require_remote_judge()
        root, runs_dir = Path(root).resolve(), Path(runs_dir).resolve()
        job_dir = runs_dir / "jobs" / job
        if job_dir.exists():
            if self._job_complete(runs_dir, job, ids, k):
                return
            raise FileExistsError("workspace job already contains partial results; choose a new job ID")
        harvey_root = self._verify_harvey_checkout()
        self._verify_local_runner(root, harvey_root)
        for trial in range(k):
            label = f"{job}/s{trial}"
            self._produce(root, runs_dir, label, ids)
            self._judge(root, runs_dir, label, harvey_root)

    def _score_record(self, path: Path, task_id: str) -> tuple[int, int] | None:
        record = _read_json(path, 2 << 20)
        criteria = record.get("criteria")
        total, passed = record.get("total"), record.get("passed")
        if (record.get("task") != _name(task_id) or isinstance(total, bool) or
                not isinstance(total, int) or not 1 <= total <= 1000 or
                isinstance(passed, bool) or not isinstance(passed, int) or
                not 0 <= passed <= total or not isinstance(criteria, list) or len(criteria) != total):
            return None
        if any(not isinstance(item, dict) or not isinstance(item.get("verdict"), str) or
               item["verdict"] not in {"pass", "fail"} for item in criteria):
            return None
        if passed != sum(item["verdict"] == "pass" for item in criteria):
            return None
        score_value = record.get("score")
        try:
            score_float = float(score_value)
        except (OverflowError, TypeError, ValueError):
            return None
        if (isinstance(score_value, bool) or not isinstance(score_value, (int, float)) or
                not math.isfinite(score_float) or
                not math.isclose(score_float, passed / total, rel_tol=1e-6, abs_tol=1e-8)):
            return None
        if record.get("all_pass") not in (0, 1, False, True) or bool(record["all_pass"]) != (passed == total):
            return None
        return passed, total

    def score(self, runs_dir, job, ids, k):
        self._safe_job(job, k)
        self._split(ids)
        runs_dir = Path(runs_dir)
        per = {}
        crit_passed = crit_total = all_pass = 0
        for task_id in ids:
            rewards, weights, tokens, missing = [], [], [], 0
            for trial in range(k):
                folder = runs_dir / "jobs" / job / f"s{trial}" / _name(task_id)
                score = self._score_record(folder / "scores.json", task_id)
                trajectory = _read_json(folder / "trajectory.json")
                if score is None or not trajectory:
                    missing += 1
                    n_criteria = self._n_criteria(task_id)
                    rewards.append(0.0)
                    weights.append(float(n_criteria))
                    tokens.append(None)
                    crit_total += n_criteria
                    continue
                passed, total = score
                rewards.append(passed / total)
                weights.append(float(total))
                crit_passed += passed
                crit_total += total
                all_pass += int(passed == total)
                usage = trajectory.get("usage")
                token_count = usage.get("total_tokens") if isinstance(usage, dict) else None
                if (isinstance(token_count, bool) or not isinstance(token_count, int) or
                        not 0 <= token_count <= 10**12):
                    token_count = None
                tokens.append(token_count)
            per[task_id] = TaskResult(rewards=rewards, weights=weights, tokens=tokens, missing=missing)
        return per, {
            "criteria_passed": crit_passed,
            "criteria_total": crit_total,
            "all_pass_tasks": all_pass,
            "criterion_rate": crit_passed / max(1, crit_total),
        }

    def load_trial(self, runs_dir, job, task_id, trial):
        self._load_inventory()
        if not isinstance(task_id, str) or task_id not in {*self._tasks, *self._heldout_tasks}:
            raise ValueError("workspace evidence task is outside the verified inventory")
        self._safe_job(job, 1)
        if isinstance(trial, bool) or not isinstance(trial, int) or not 0 <= trial < MAX_TRIALS:
            raise ValueError("workspace trial index is invalid")
        folder = Path(runs_dir) / "jobs" / job / f"s{trial}" / _name(task_id)
        trajectory = _read_json(folder / "trajectory.json")
        if not trajectory:
            return None
        scores = _read_json(folder / "scores.json")
        outputs = folder / "output"
        names = sorted(p.name for p in outputs.iterdir() if p.is_file() and not p.is_symlink()) if outputs.is_dir() else []
        return {"task_id": task_id, "domain": task_id.split("/")[0], "trial": trial,
                "trajectory": trajectory, "scores": scores, "output_files": names}

    def render_trace(self, rec, detail=False):
        return render.render_full(rec, detail=detail)

    def task_row(self, task_id, rec, tr):
        scores = rec.get("scores") or {}
        criteria = scores.get("criteria")
        failed = [i for i, item in enumerate(criteria)
                  if not isinstance(item, dict) or item.get("verdict") != "pass"] if isinstance(criteria, list) else []
        trajectory = rec.get("trajectory") or {}
        steps = sum(1 for message in trajectory.get("messages", [])
                    if isinstance(message, dict) and message.get("role") == "assistant")
        return (f"{task_id} | criteria {scores.get('passed')}/{scores.get('total')} "
                f"(mean={tr.mean:.3f}) | status={trajectory.get('status')} | steps={steps} | "
                f"deliverables={rec.get('output_files')} | failed_criteria={failed[:25]}")

    def smoke(self, root, runs_dir, job, ids):
        try:
            self._require_benchmark_execution()
            self._safe_job(job, 1)
            self._split(ids)
            root, runs_dir = Path(root).resolve(), Path(runs_dir).resolve()
            harvey_root = self._verify_harvey_checkout()
            self._verify_local_runner(root, harvey_root)
            agent_python = os.environ.get("RRSI_AGENT_PYTHON", sys.executable)
            harness = self.harness_dir(root)
            result = subprocess.run(
                [agent_python, "-m", "compileall", "-q", str(harness)],
                capture_output=True, text=True, timeout=60, check=False,
            )
            if result.returncode:
                return False, {"stage": "compile", "detail": (result.stdout + result.stderr)[-1200:]}
            if (runs_dir / "jobs" / job).exists():
                return False, {"stage": "preflight", "detail": "smoke job exists; refusing overwrite"}
            done = self._produce(root, runs_dir, f"{job}/s0", ids)
            return (done == len(ids), {"stage": "smoke_run", "completed": done,
                                       "expected": len(ids)})
        except (OSError, PermissionError, RuntimeError, ValueError) as exc:
            return False, {"stage": "preflight", "detail": str(exc)[:1200]}


DOMAIN = WorkspaceDomain()
