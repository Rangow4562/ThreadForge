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
"""Evaluate(H', D_evolve, k): the empirical score and cost of Eq. (estimate).

    S_hat(H) = 1/(k|D|) sum_x sum_j r(x, tau_x^(j))
    C_hat(H) = 1/(k|D|) sum_x sum_j c(tau_x^(j))

with r in [0,1] and c the policy tokens of a trajectory. A missing trial (crash,
timeout, infrastructure) contributes r = 0 with the full denominator, never an
absent slot, so a candidate cannot look better by destroying the trials it
finds hard.

Rewards may carry weights. Every coding / engineering trial has weight 1; a
Harvey LAB trial has reward = criteria passed / criteria total and weight =
criteria total, so S_hat is the fraction of criteria passed over all tasks, the
benchmark's own metric (Appendix, Harvey LAB).
"""

from __future__ import annotations

import json
import math
from dataclasses import asdict, dataclass, field
from numbers import Integral, Real
from pathlib import Path


@dataclass
class TaskResult:
    rewards: list[float]                  # one per trial, missing trials -> 0.0
    weights: list[float] = field(default_factory=list)   # default 1.0 each
    tokens: list = field(default_factory=list)           # int or None per trial
    missing: int = 0
    extra: dict = field(default_factory=dict)

    def __post_init__(self):
        if not isinstance(self.rewards, list):
            raise ValueError("task rewards must be a list")
        if self.weights == []:
            self.weights = [1.0] * len(self.rewards)
        if self.tokens == []:
            self.tokens = [None] * len(self.rewards)
        self.validate()

    def validate(self, expected_trials: int | None = None) -> None:
        if not isinstance(self.rewards, list) or not isinstance(self.weights, list) or not isinstance(self.tokens, list):
            raise ValueError("task rewards, weights, and tokens must be lists")
        trial_count = len(self.rewards)
        if len(self.weights) != trial_count or len(self.tokens) != trial_count:
            raise ValueError("task rewards, weights, and tokens must have identical lengths")
        if expected_trials is not None and trial_count != expected_trials:
            raise ValueError(f"task must contain exactly {expected_trials} trial results")
        if any(not _finite_real(value) or not 0.0 <= value <= 1.0 for value in self.rewards):
            raise ValueError("task rewards must be finite values in [0, 1]")
        if any(not _finite_real(value) or value <= 0.0 for value in self.weights):
            raise ValueError("task weights must be finite positive values")
        if any(value is not None and (not _finite_real(value) or value < 0.0) for value in self.tokens):
            raise ValueError("task token counts must be finite nonnegative values or null")
        if isinstance(self.missing, bool) or not isinstance(self.missing, Integral) or not 0 <= self.missing <= trial_count:
            raise ValueError("task missing count must be an integer within its trial count")
        if not isinstance(self.extra, dict):
            raise ValueError("task extra data must be an object")

    @property
    def mean(self) -> float:
        self.validate()
        w = sum(self.weights)
        return (sum(r * x for r, x in zip(self.rewards, self.weights)) / w) if w else 0.0


@dataclass
class EvalResult:
    job: str
    k: int
    per_task: dict                        # task_id -> TaskResult
    S: float                              # S_hat
    C: float | None                       # C_hat (None if no token counts)
    n_expected: int
    missing: int
    extra: dict = field(default_factory=dict)   # domain aggregates (pass counts, ...)

    def to_json(self) -> dict:
        self.validate()
        d = {"job": self.job, "k": self.k, "S": self.S, "C": self.C,
             "n_expected": self.n_expected, "missing": self.missing,
             "extra": self.extra,
             "per_task": {t: asdict(r) for t, r in self.per_task.items()}}
        return d

    @classmethod
    def from_json(cls, d: dict) -> "EvalResult":
        if not isinstance(d, dict) or not isinstance(d.get("per_task"), dict):
            raise ValueError("evaluation result must contain a per_task object")
        per = {}
        for task_id, record in d["per_task"].items():
            if not isinstance(record, dict) or not isinstance(record.get("rewards"), list):
                raise ValueError("persisted task result must contain a rewards list")
            trial_count = len(record["rewards"])
            for field_name in ("weights", "tokens"):
                if field_name in record and (
                    not isinstance(record[field_name], list) or len(record[field_name]) != trial_count
                ):
                    raise ValueError("persisted task reward, weight, and token vectors must have identical lengths")
            per[task_id] = TaskResult(**record)
        result = cls(job=d["job"], k=d["k"], per_task=per, S=d["S"], C=d["C"],
                     n_expected=d["n_expected"], missing=d["missing"],
                     extra=d.get("extra", {}))
        result.validate()
        return result

    def save(self, path: Path | str) -> None:
        Path(path).write_text(json.dumps(self.to_json(), indent=1))

    @classmethod
    def load(cls, path: Path | str) -> "EvalResult":
        return cls.from_json(json.loads(Path(path).read_text()))

    def validate(self) -> None:
        if not isinstance(self.job, str) or not self.job.strip():
            raise ValueError("evaluation job must be a nonempty string")
        if isinstance(self.k, bool) or not isinstance(self.k, Integral) or self.k < 1:
            raise ValueError("evaluation k must be a positive integer")
        if not isinstance(self.per_task, dict) or not self.per_task or not isinstance(self.extra, dict):
            raise ValueError("evaluation per_task and extra fields must be objects")
        if any(not isinstance(task_id, str) or not task_id.strip() for task_id in self.per_task):
            raise ValueError("evaluation task IDs must be nonempty strings")
        for task_result in self.per_task.values():
            if not isinstance(task_result, TaskResult):
                raise ValueError("evaluation task results must be TaskResult values")
            task_result.validate(expected_trials=int(self.k))
        expected = len(self.per_task) * int(self.k)
        missing = sum(task_result.missing for task_result in self.per_task.values())
        if isinstance(self.n_expected, bool) or not isinstance(self.n_expected, Integral) or self.n_expected != expected:
            raise ValueError("evaluation n_expected does not match its task and trial counts")
        if isinstance(self.missing, bool) or not isinstance(self.missing, Integral) or self.missing != missing:
            raise ValueError("evaluation missing count does not match per-task records")
        denominator = sum(sum(task_result.weights) for task_result in self.per_task.values())
        score = (
            sum(sum(reward * weight for reward, weight in zip(task_result.rewards, task_result.weights))
                for task_result in self.per_task.values()) / denominator
            if denominator else 0.0
        )
        tokens = [token for task_result in self.per_task.values() for token in task_result.tokens
                  if token is not None and token > 0]
        cost = sum(tokens) / len(tokens) if tokens else None
        if not _finite_real(self.S) or not math.isclose(float(self.S), score, rel_tol=1e-9, abs_tol=1e-12):
            raise ValueError("evaluation score does not reconcile with per-task trials")
        if cost is None:
            if self.C is not None:
                raise ValueError("evaluation cost must be null when token usage is unavailable")
        elif not _finite_real(self.C) or not math.isclose(float(self.C), cost, rel_tol=1e-9, abs_tol=1e-12):
            raise ValueError("evaluation cost does not reconcile with per-task tokens")


def aggregate(job: str, k: int, per_task: dict, extra: dict | None = None) -> EvalResult:
    """Fold per-task trial records into S_hat and C_hat."""
    if isinstance(k, bool) or not isinstance(k, Integral) or k < 1:
        raise ValueError("k must be a positive integer")
    if not isinstance(per_task, dict) or (extra is not None and not isinstance(extra, dict)):
        raise ValueError("per_task and extra must be objects")
    if not per_task:
        raise ValueError("per_task must contain at least one task")
    num = den = 0.0
    toks = []
    missing = 0
    for task_id, tr in per_task.items():
        if not isinstance(task_id, str) or not task_id.strip() or not isinstance(tr, TaskResult):
            raise ValueError("per_task must map nonempty task IDs to TaskResult values")
        tr.validate(expected_trials=int(k))
        for r, w in zip(tr.rewards, tr.weights):
            num += r * w
            den += w
        toks += [x for x in tr.tokens if isinstance(x, (int, float)) and x > 0]
        missing += tr.missing
    result = EvalResult(job=job, k=int(k), per_task=per_task,
                        S=(num / den) if den else 0.0,
                        C=(sum(toks) / len(toks)) if toks else None,
                        n_expected=len(per_task) * int(k), missing=missing,
                        extra=dict(extra or {}))
    result.validate()
    return result


def evaluate(domain, root: Path, runs_dir: Path, job: str, ids: list[str],
             k: int, log_prefix: str = "", *,
             benchmark_execution_opt_in: bool = False) -> EvalResult:
    """Run H' (the harness checked out under `root`) on `ids` with k trials and
    score it. Resume-safe: the domain runner fills only missing trials.

    Benchmark execution is disabled by default because domain adapters may
    launch Docker, invoke third-party runners, or call billable model APIs.
    The caller must explicitly opt in through its reviewed local config.
    """
    require_benchmark_execution_opt_in(benchmark_execution_opt_in)
    domain.run(root, runs_dir, job, ids, k, log_prefix=log_prefix)
    per_task, extra = domain.score(runs_dir, job, ids, k)
    return aggregate(job, k, per_task, extra)


def run_domain_smoke(domain, root: Path, runs_dir: Path, job: str, ids: list[str], *,
                     benchmark_execution_opt_in: bool = False):
    """Run a domain's liveness smoke only after explicit benchmark consent.

    Domain smoke checks are not necessarily passive: adapters may launch a
    benchmark runner, Docker, or a billable model. Keep their authorization at
    the shared execution boundary instead of relying on each adapter to
    remember a local guard.
    """
    require_benchmark_execution_opt_in(benchmark_execution_opt_in)
    return domain.smoke(root, runs_dir, job, ids)


def require_benchmark_execution_opt_in(value: bool) -> None:
    """Fail closed before any operation that can execute a benchmark."""
    if not isinstance(value, bool):
        raise ValueError("benchmark_execution_opt_in must be a boolean")
    if not value:
        raise PermissionError(
            "benchmark execution is disabled; set benchmark_execution_opt_in=true "
            "in the reviewed local RRSI config to permit domain smoke/run"
        )


def relative_cost_change(C_cand: float | None, C_inc: float | None) -> float:
    """Delta C = (C' - C_t) / C_t; 0 when either side has no token count."""
    if not C_cand or not C_inc:
        return 0.0
    return (C_cand - C_inc) / C_inc


def _finite_real(value) -> bool:
    return isinstance(value, Real) and not isinstance(value, bool) and math.isfinite(float(value))
