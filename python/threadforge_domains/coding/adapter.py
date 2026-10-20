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
"""Coding instance: Terminal-Bench 2.1 through harbor with a Terminus-2 fork.

Evaluate = `scripts/run_eval.sh <job> <dataset> <k> <concurrency> [-i task ...]`
run from the candidate's worktree (PYTHONPATH = that worktree's third_party/,
so `--agent harbor_terminus2:AgentHarness` resolves to the candidate's harness). One
harbor job = one evaluation; trial directories are `<task>__<hash>` under
runs/coding/jobs/<job>/. A trial's reward is 1.0 iff the task's hidden tests
pass; a missing or errored trial counts 0.0 with the full denominator.
"""

from __future__ import annotations

import json
import math
import os
import re
import stat
import subprocess
import sys
from collections import Counter
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent.parent))
sys.path.insert(0, str(HERE))
from rrsi.domain import Domain          # noqa: E402
from rrsi.evaluate import TaskResult, require_benchmark_execution_opt_in  # noqa: E402
from rrsi.inventory import load_domain_inventory  # noqa: E402
from rrsi.modelpolicy import evaluation_runtime  # noqa: E402
import briefs                            # noqa: E402
import render                            # noqa: E402

CFG = json.loads((HERE / "rrsi.json").read_text())
INVENTORY_ENV = "THREADFORGE_RRSI_INVENTORY_BUNDLE"
_JOB_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
MAX_RESULT_JSON_BYTES = 32 << 20


def _has_unlinked_path_components(path: Path, require_directory: bool = False) -> bool:
    absolute = Path(os.path.abspath(path))
    current = Path(absolute.anchor)
    components = absolute.parts[1:] if absolute.anchor else absolute.parts
    for index, component in enumerate(components):
        current = current / component
        try:
            info = current.lstat()
        except OSError:
            return False
        reparse_point = bool(getattr(info, "st_file_attributes", 0) & 0x400)
        if stat.S_ISLNK(info.st_mode) or reparse_point:
            return False
        if index < len(components) - 1 and not stat.S_ISDIR(info.st_mode):
            return False
    return not require_directory or (current.exists() and current.is_dir())


def _coding_python() -> str:
    explicit = os.environ.get("RRSI_CODING_PYTHON")
    if explicit:
        return explicit
    venv = os.environ.get("RRSI_CODING_VENV")
    if venv:
        for candidate in (Path(venv) / "bin" / "python", Path(venv) / "Scripts" / "python.exe"):
            if candidate.is_file():
                return str(candidate)
    return sys.executable


def _load_result(trial_dir: Path) -> dict | None:
    if not _has_unlinked_path_components(trial_dir, require_directory=True):
        return None
    rj = trial_dir / "result.json"
    try:
        before = rj.lstat()
    except OSError:
        return None
    reparse_point = bool(getattr(before, "st_file_attributes", 0) & 0x400)
    if (stat.S_ISLNK(before.st_mode) or reparse_point or not stat.S_ISREG(before.st_mode) or
            before.st_size < 0 or before.st_size > MAX_RESULT_JSON_BYTES):
        return None
    identity = (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns)
    try:
        descriptor = os.open(rj, os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(descriptor, "rb") as stream:
            opened = os.fstat(stream.fileno())
            if (not stat.S_ISREG(opened.st_mode) or
                    (opened.st_dev, opened.st_ino, opened.st_size, opened.st_mtime_ns) != identity):
                return None
            raw = stream.read(MAX_RESULT_JSON_BYTES + 1)
            after = os.fstat(stream.fileno())
        path_after = rj.lstat()
        after_identity = (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns)
        path_identity = (path_after.st_dev, path_after.st_ino, path_after.st_size, path_after.st_mtime_ns)
        if len(raw) != before.st_size or after_identity != identity or path_identity != identity:
            return None
        value = json.loads(raw.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError, RecursionError, ValueError):
        return None
    return value if isinstance(value, dict) else None


def _short(result: dict) -> str:
    return str(result.get("task_name", "")).split("/")[-1]


def _reward(result: dict) -> float | None:
    val = ((result.get("verifier_result") or {}).get("rewards") or {}).get("reward")
    if val is None:
        return None
    if isinstance(val, bool) or not isinstance(val, (int, float)):
        raise ValueError("verifier reward must be numeric")
    return float(val)


_INFRA_EXC = re.compile(r"docker|compose|image|pull|registry|no space left|connection refused|"
                        r"daemon|network .* (not found|already exists)", re.I)


def infra_failure(result: dict) -> bool:
    """True when the trial never got a working environment (harbor/docker error),
    as opposed to the agent timing out or the tests failing."""
    e = result.get("exception_info")
    if not e:
        return False
    if isinstance(e, dict):
        typ, msg = str(e.get("exception_type", "")), str(e.get("exception_message", ""))
    else:
        typ, msg = "", str(e)
    if typ in ("AgentTimeoutError", "VerifierTimeoutError"):
        return False
    return bool(_INFRA_EXC.search(msg)) or typ in ("RuntimeError", "OSError")


def _tokens(result: dict) -> int | None:
    if result.get("exception_info"):
        return None
    ar = result.get("agent_result") or {}
    tok = (ar.get("n_input_tokens") or 0) + (ar.get("n_output_tokens") or 0)
    return tok or None


class CodingDomain(Domain):
    name = "coding"
    harness_path = "../../third_party/harbor_terminus2"
    source_exts = {".py", ".txt", ".md", ".json", ".sh"}
    briefs = {k: v.replace("{policy}", CFG.get("policy_label", "the frozen policy"))
              for k, v in {"analyst": briefs.ANALYST, "digester": briefs.DIGESTER,
                           "proposer": briefs.PROPOSER, "critic": briefs.CRITIC}.items()}
    critic_patterns = [
        (r"/tests\b", "verifier tests path in diff"),
        (r"test_outputs", "verifier test file reference in diff"),
        (r"\breward\.(txt|json)\b", "reward file reference in diff"),
        (r"\bctrf\b", "verifier report reference in diff"),
        (r"/solution\b", "oracle solution path in diff"),
        (r"task\.toml", "task config reference in diff"),
        (r"/logs/verifier", "verifier logs path in diff"),
    ]
    component_signals = [
        ("memory",          [r"TBMH_STATE_DIR"]),
        ("subagent",        [r"generate_response"]),
        ("context_mgmt",    [r"_summarize", r"summariz", r"proactive_summarization",
                             r"unwind", r"free_tokens"]),
        ("output_plumbing", [r"_limit_output_length", r"terminal_output", r"truncat",
                             r"_collapse", r"paste.?buffer", r"load-?buffer"]),
        ("control_flow",    [r"_run_agent_loop", r"episode", r"is_task_complete",
                             r"_pending_completion", r"keystrokes"]),
        ("config",          [r"= *\d+ *#", r"threshold", r"max_bytes", r"duration"]),
        ("prompt",          [r"templates/.*\.txt", r"_VERIFICATION", r"_get_.*hint",
                             r"prompt", r"system"]),
    ]

    def __init__(self):
        self._inventory = None
        self._tasks: list[str] = []
        self._heldout_tasks: list[str] = []
        self._base_critic_patterns = list(self.critic_patterns)

    def _load_inventory(self):
        if self._inventory is not None:
            return self._inventory
        bundle = os.environ.get(INVENTORY_ENV)
        if not bundle:
            raise RuntimeError(f"set {INVENTORY_ENV} to a private, source-verified coding inventory bundle")
        self._inventory = load_domain_inventory(bundle, "rrsi-coding")
        self._tasks = list(self._inventory.evolve_ids)
        self._heldout_tasks = list(self._inventory.heldout_ids)
        all_ids = self._tasks + self._heldout_tasks
        self.critic_patterns = self._base_critic_patterns + [
            (rf"(?<![\w-]){re.escape(task_id)}(?![\w-])", "benchmark task identifier in diff")
            for task_id in all_ids
        ]
        return self._inventory

    # ---- task sets ---------------------------------------------------------
    def evolve_ids(self) -> list[str]:
        self._load_inventory()
        return list(self._tasks)

    def heldout_ids(self) -> list[str]:
        self._load_inventory()
        return list(self._heldout_tasks)

    def smoke_ids(self, incumbent_per_task=None) -> list[str]:
        ids = self.evolve_ids()
        return ids[:max(1, min(len(ids), int(CFG.get("smoke_n", 2))))]

    # ---- Evaluate ----------------------------------------------------------
    def _harbor(self, root: Path, runs_dir: Path, job: str, ids: list[str] | None,
                k: int, dataset: str, log_prefix: str) -> None:
        if isinstance(k, bool) or not isinstance(k, int) or k < 1:
            raise ValueError("coding trial count k must be a positive integer")
        if not _JOB_ID.fullmatch(job) or not _JOB_ID.fullmatch(log_prefix or job):
            raise ValueError("coding job and log IDs must be bounded path-safe identifiers")
        self._load_inventory()
        if dataset == CFG["dataset"]:
            split_tasks = self._tasks
        elif dataset == CFG["ood_dataset"]:
            split_tasks = self._heldout_tasks
        else:
            raise ValueError("coding dataset must match one verified inventory split")
        requested_ids = list(split_tasks if ids is None else ids)
        if not requested_ids or len(set(requested_ids)) != len(requested_ids) or not set(requested_ids) <= set(split_tasks):
            raise ValueError("coding run tasks must be a unique nonempty subset of the verified dataset split")
        jobs = runs_dir / "jobs"
        jobs.mkdir(parents=True, exist_ok=True)
        jdir = jobs / job
        if jdir.exists() or jdir.is_symlink():
            try:
                self._validate_job_result_set(runs_dir, job, requested_ids, k)
            except RuntimeError:
                raise FileExistsError(
                    f"Harbor job {job!r} already has incomplete or invalid local results; "
                    "choose a new job ID or review the existing artifacts manually"
                ) from None
            else:
                print(f"[coding] job {job} already complete; not re-running", flush=True)
                return
        venv = os.environ.get("RRSI_CODING_VENV")
        if not venv:
            raise RuntimeError("set RRSI_CODING_VENV to the isolated Harbor virtual environment")
        model, env = evaluation_runtime(self.remote_inference_opt_in)
        script = root / "domains" / "coding" / "scripts" / "run_eval.sh"
        cmd = [str(script), job, dataset, str(k), str(CFG.get("concurrency", 10)),
               "--jobs-dir", str(jobs)]
        if set(requested_ids) != set(split_tasks):
            for t in requested_ids:
                harbor_id = f"terminal-bench/{t}" if dataset == CFG["dataset"] and not t.startswith("terminal-bench/") else t
                cmd += ["-i", harbor_id]
        env.update({"RRSI_CODING_ROOT": str(root / "domains" / "coding"),
                    "RRSI_CODING_VENV": venv,
                    "RRSI_POLICY_MODEL": model})
        log = runs_dir / "logs" / f"{log_prefix or job}.log"
        log.parent.mkdir(parents=True, exist_ok=True)
        with open(log, "a") as lf:
            r = subprocess.run(cmd, cwd=str(root / "domains" / "coding"),
                               stdout=lf, stderr=subprocess.STDOUT, env=env)
        if r.returncode != 0:
            raise RuntimeError(f"Harbor benchmark process failed with exit code {r.returncode}")
        self._validate_job_result_set(runs_dir, job, requested_ids, k)

    def _validate_job_result_set(self, runs_dir: Path, job: str, ids: list[str], k: int) -> None:
        if not ids or len(set(ids)) != len(ids):
            raise RuntimeError("Harbor run task selection is empty or duplicated")
        job_dir = Path(runs_dir) / "jobs" / job
        if not _has_unlinked_path_components(job_dir, require_directory=True):
            raise RuntimeError("Harbor completed without creating a job directory") from None
        try:
            job_info = job_dir.lstat()
        except OSError:
            raise RuntimeError("Harbor job directory changed during validation") from None
        job_reparse = bool(getattr(job_info, "st_file_attributes", 0) & 0x400)
        if stat.S_ISLNK(job_info.st_mode) or job_reparse or not stat.S_ISDIR(job_info.st_mode):
            raise RuntimeError("Harbor job output must be a local non-linked directory")
        expected = set(ids)
        completed: Counter[str] = Counter()
        try:
            entries = sorted(job_dir.iterdir(), key=lambda item: item.name)
        except OSError:
            raise RuntimeError("Harbor job output could not be enumerated") from None
        for trial_dir in entries:
            try:
                trial_info = trial_dir.lstat()
            except OSError:
                raise RuntimeError("Harbor trial output changed during validation") from None
            trial_reparse = bool(getattr(trial_info, "st_file_attributes", 0) & 0x400)
            if stat.S_ISLNK(trial_info.st_mode) or trial_reparse:
                raise RuntimeError("Harbor trial output cannot contain linked paths")
            if not stat.S_ISDIR(trial_info.st_mode):
                continue
            result_path = trial_dir / "result.json"
            if not result_path.exists() and not result_path.is_symlink():
                continue
            result = _load_result(trial_dir)
            if result is None:
                raise RuntimeError("Harbor trial result is missing, malformed, linked, or oversized")
            task_id = _short(result)
            if task_id not in expected:
                raise RuntimeError("Harbor job contains a result outside the requested task set")
            if infra_failure(result):
                raise RuntimeError("Harbor job contains an infrastructure-failed trial")
            try:
                reward = _reward(result)
            except (AttributeError, TypeError, ValueError, OverflowError):
                raise RuntimeError("Harbor trial reward is malformed") from None
            if reward is None:
                raise RuntimeError("Harbor trial result is missing a verifier reward")
            if not math.isfinite(reward) or not 0.0 <= reward <= 1.0:
                raise RuntimeError("Harbor trial reward is invalid")
            completed[task_id] += 1
            if completed[task_id] > k:
                raise RuntimeError("Harbor job contains excess trials for a requested task")
        try:
            final_job_info = job_dir.lstat()
        except OSError:
            raise RuntimeError("Harbor job directory changed during validation") from None
        if (final_job_info.st_dev, final_job_info.st_ino, final_job_info.st_mtime_ns) != (
            job_info.st_dev, job_info.st_ino, job_info.st_mtime_ns,
        ):
            raise RuntimeError("Harbor job directory changed during validation")
        if any(completed[task_id] != k for task_id in ids):
            raise RuntimeError("Harbor job did not produce exactly k valid trials for every requested task")

    def run(self, root, runs_dir, job, ids, k, log_prefix=""):
        require_benchmark_execution_opt_in(self.benchmark_execution_opt_in)
        evolve = set(self.evolve_ids())
        heldout = set(self.heldout_ids())
        selected = set(ids)
        if not selected:
            raise ValueError("RRSI coding evaluation requires a nonempty verified task set")
        if selected <= evolve:
            dataset = CFG["dataset"]
        elif selected <= heldout:
            dataset = CFG["ood_dataset"]
        else:
            raise ValueError("RRSI coding evaluation IDs must belong to one verified split")
        self._harbor(Path(root), Path(runs_dir), job, ids, k, dataset, log_prefix)

    def _trials(self, runs_dir: Path, job: str) -> dict[str, list[Path]]:
        by_task: dict[str, list[Path]] = {}
        jdir = Path(runs_dir) / "jobs" / job
        if not jdir.exists():
            return by_task
        for td in sorted(jdir.iterdir()):
            if not td.is_dir():
                continue
            res = _load_result(td)
            if res is None:
                continue
            by_task.setdefault(_short(res), []).append(td)
        return by_task

    def score(self, runs_dir, job, ids, k):
        trials = self._trials(Path(runs_dir), job)
        per = {}
        total_pass = 0
        for t in ids:
            dirs = trials.get(t, [])[:k]
            rewards, toks = [], []
            for td in dirs:
                res = _load_result(td) or {}
                if infra_failure(res):
                    continue                  # counted as missing below
                try:
                    rw = _reward(res)
                except (AttributeError, TypeError, ValueError, OverflowError):
                    raise RuntimeError("Harbor trial reward is malformed") from None
                if rw is None:
                    raise RuntimeError("Harbor trial result is missing a verifier reward")
                if not math.isfinite(rw) or not 0.0 <= rw <= 1.0:
                    raise RuntimeError("Harbor trial reward is invalid")
                rewards.append(rw)
                toks.append(_tokens(res))
            missing = k - len(rewards)
            rewards += [0.0] * missing
            toks += [None] * missing
            passes = sum(1 for x in rewards if x == 1.0)
            total_pass += passes
            per[t] = TaskResult(rewards=rewards, tokens=toks, missing=missing,
                                extra={"passes": passes,
                                       "trial_dirs": [str(d) for d in dirs]})
        return per, {"total_passes": total_pass, "n_trials": len(ids) * k,
                     "pass_rate": total_pass / max(1, len(ids) * k)}

    # ---- evidence ----------------------------------------------------------
    def load_trial(self, runs_dir, job, task_id, trial):
        dirs = self._trials(Path(runs_dir), job).get(task_id, [])
        if trial >= len(dirs):
            return None
        res = _load_result(dirs[trial]) or {}
        meta = (res.get("agent_result") or {}).get("metadata") or {}
        return {"task_id": task_id, "trial_dir": str(dirs[trial]),
                "reward": _reward(res), "episodes": meta.get("n_episodes"),
                "exception": (str(res.get("exception_info"))[:200]
                              if res.get("exception_info") else None)}

    def render_trace(self, rec, detail=False):
        return render.render_full(rec, detail=detail)

    def task_row(self, task_id, rec, tr):
        return (f"{task_id} | rewards={tr.rewards if tr else '?'} | "
                f"rendered_trial_reward={rec.get('reward')} | "
                f"episodes={rec.get('episodes')} | exception={str(rec.get('exception'))[:60]}")

    # ---- gates -------------------------------------------------------------
    def smoke(self, root, runs_dir, job, ids):
        require_benchmark_execution_opt_in(self.benchmark_execution_opt_in)
        root, runs_dir = Path(root), Path(runs_dir)
        cdir = root / "domains" / "coding"
        python_bin = _coding_python()
        self._load_inventory()
        jdir = runs_dir / "jobs" / job
        if jdir.exists():
            return False, {"stage": "preflight", "detail": "smoke job path already exists; refusing to overwrite"}
        comp = subprocess.run([python_bin, "-m", "compileall", "-q", str(self.harness_dir(root))],
                              capture_output=True, text=True)
        if comp.returncode != 0:
            return False, {"stage": "compile", "detail": (comp.stdout + comp.stderr)[-1500:]}
        model, model_env = evaluation_runtime(self.remote_inference_opt_in)
        code = ("from pathlib import Path\nfrom harbor_terminus2 import AgentHarness\n"
                "a = AgentHarness(logs_dir=Path('/tmp/rrsi_ctor_check'), "
                f"model_name='{model}')\n"
                "assert AgentHarness.name() == 'rrsi-terminus2'\nprint('CTOR_OK')\n")
        ctor_env = dict(model_env)
        ctor_env["PYTHONPATH"] = str(root / "third_party")
        ctor = subprocess.run([python_bin, "-c", code], cwd=str(cdir), capture_output=True,
                              text=True, env=ctor_env)
        if "CTOR_OK" not in ctor.stdout:
            return False, {"stage": "ctor", "detail": (ctor.stderr or ctor.stdout)[-1200:]}
        self._harbor(root, runs_dir, job, ids, 1, CFG["dataset"], job)
        found, details = 0, {}
        for td in (jdir.glob("*") if jdir.exists() else []):
            res = _load_result(td)
            if res is None:
                continue
            found += 1
            if res.get("exception_info"):
                details[td.name] = str(res["exception_info"])[:400]
        if found < len(ids):
            details["missing_trials"] = f"{found}/{len(ids)}"
        if details:
            return False, {"stage": "smoke_run", **details}
        return True, {"stage": "smoke_run", "n": found}


DOMAIN = CodingDomain()
