import copy

import json

import pytest

from rrsi.evaluate import EvalResult, TaskResult, aggregate, evaluate, run_domain_smoke
from rrsi.config import RRSIConfig
from rrsi.loop import Run


def test_aggregate_requires_exact_trial_alignment_and_reconciles_summary():
    result = aggregate(
        "heldout",
        2,
        {
            "task-a": TaskResult(rewards=[1.0, 0.0], tokens=[120, None], missing=1),
            "task-b": TaskResult(rewards=[0.5, 1.0], weights=[2.0, 2.0], tokens=[80, 100]),
        },
    )

    assert result.n_expected == 4
    assert result.missing == 1
    assert result.C == 100
    assert result.S == pytest.approx((1.0 + 0.0 + 1.0 + 2.0) / 6.0)
    assert EvalResult.from_json(result.to_json()).to_json() == result.to_json()


@pytest.mark.parametrize(
    "kwargs, message",
    [
        ({"rewards": [1.0, 0.0], "weights": [1.0], "tokens": [10, 20]}, "identical lengths"),
        ({"rewards": [1.0], "weights": [0.0], "tokens": [10]}, "positive values"),
        ({"rewards": [float("nan")], "tokens": [10]}, "finite values"),
        ({"rewards": [1.1], "tokens": [10]}, "finite values"),
        ({"rewards": [1.0], "tokens": [-1]}, "nonnegative values"),
        ({"rewards": [1.0], "tokens": [True]}, "nonnegative values"),
        ({"rewards": [1.0], "tokens": [10], "missing": 2}, "missing count"),
    ],
)
def test_task_result_rejects_malformed_trial_vectors(kwargs, message):
    with pytest.raises(ValueError, match=message):
        TaskResult(**kwargs)


def test_aggregate_revalidates_mutated_task_results_and_requires_k_trials():
    mutated = TaskResult(rewards=[1.0, 0.0], tokens=[10, 20])
    mutated.weights.pop()
    with pytest.raises(ValueError, match="identical lengths"):
        aggregate("bad", 2, {"task-a": mutated})

    with pytest.raises(ValueError, match="exactly 2 trial results"):
        aggregate("short", 2, {"task-a": TaskResult(rewards=[1.0], tokens=[10])})

    with pytest.raises(ValueError, match="at least one task"):
        aggregate("empty", 1, {})


def test_eval_result_loader_rejects_summary_that_does_not_reconcile():
    result = aggregate("checked", 1, {"task-a": TaskResult(rewards=[1.0], tokens=[10])})
    tampered = copy.deepcopy(result.to_json())
    tampered["S"] = 0.0
    with pytest.raises(ValueError, match="score does not reconcile"):
        EvalResult.from_json(tampered)

    tampered = copy.deepcopy(result.to_json())
    tampered["n_expected"] = 7
    with pytest.raises(ValueError, match="n_expected does not match"):
        EvalResult.from_json(tampered)

    tampered = copy.deepcopy(result.to_json())
    tampered["per_task"]["task-a"]["tokens"] = []
    with pytest.raises(ValueError, match="identical lengths"):
        EvalResult.from_json(tampered)


def test_domain_execution_requires_explicit_opt_in_before_runner_side_effects(tmp_path):
    class Domain:
        called = False

        def run(self, *args, **kwargs):
            self.called = True

        def score(self, *args, **kwargs):
            raise AssertionError("score must not run when benchmark execution is disabled")

    domain = Domain()
    with pytest.raises(PermissionError, match="benchmark execution is disabled"):
        evaluate(domain, tmp_path, tmp_path / "runs", "job", ["task"], 1)
    assert not domain.called


def test_domain_execution_opt_in_runs_and_aggregates(tmp_path):
    class Domain:
        called = False

        def run(self, root, runs_dir, job, ids, k, log_prefix=""):
            self.called = True
            assert (root, runs_dir, job, ids, k, log_prefix) == (
                tmp_path, tmp_path / "runs", "job", ["task"], 1, "job"
            )

        def score(self, runs_dir, job, ids, k):
            return {"task": TaskResult(rewards=[1.0], tokens=[12])}, {"pass_rate": 1.0}

    domain = Domain()
    result = evaluate(
        domain, tmp_path, tmp_path / "runs", "job", ["task"], 1,
        log_prefix="job", benchmark_execution_opt_in=True,
    )
    assert domain.called
    assert result.S == 1.0
    assert result.C == 12


def test_domain_execution_rejects_non_boolean_opt_in(tmp_path):
    with pytest.raises(ValueError, match="must be a boolean"):
        evaluate(object(), tmp_path, tmp_path / "runs", "job", ["task"], 1,
                 benchmark_execution_opt_in="true")


def test_domain_smoke_requires_explicit_opt_in_before_calling_adapter(tmp_path):
    class Domain:
        called = False

        def smoke(self, *args):
            self.called = True
            return True, {"stage": "fixture"}

    domain = Domain()
    with pytest.raises(PermissionError, match="benchmark execution is disabled"):
        run_domain_smoke(domain, tmp_path, tmp_path / "runs", "smoke", ["task"])
    assert not domain.called

    assert run_domain_smoke(
        domain, tmp_path, tmp_path / "runs", "smoke", ["task"],
        benchmark_execution_opt_in=True,
    ) == (True, {"stage": "fixture"})
    assert domain.called


@pytest.mark.parametrize(
    "operation",
    ["baseline", "heldout", "round", "reevaluate", "draft", "candidate_evaluate"],
)
def test_rrsi_run_entrypoints_fail_before_git_or_domain_side_effects(tmp_path, operation):
    class Domain:
        name = "fixture"
        harness_path = "harness"
        called = False

        def evolve_ids(self):
            self.called = True
            raise AssertionError("domain must not be read before benchmark opt-in")

    domain = Domain()
    runner = Run(domain, RRSIConfig(), tmp_path / "repo", tmp_path / "runs")
    with pytest.raises(PermissionError, match="benchmark execution is disabled"):
        if operation == "baseline":
            runner.baseline()
        elif operation == "heldout":
            runner.heldout("fixture", ["task"])
        elif operation == "round":
            runner.round(0)
        elif operation == "reevaluate":
            runner.reevaluate(0)
        elif operation == "draft":
            runner._draft(
                0, "A", tmp_path / "candidate", {}, {}, {}, None, 1,
                {}, False, [], [], "", "",
            )
        else:
            runner._evaluate(0, object(), tmp_path / "candidate", ["task"])

    assert not domain.called
    assert not (tmp_path / "repo" / ".git").exists()


def test_rrsi_config_disables_benchmark_and_remote_evaluation_by_default(tmp_path):
    config_path = tmp_path / "rrsi.json"
    config_path.write_text(
        '{"T": 1, "benchmark_execution_opt_in": true, "remote_evaluation_opt_in": true}'
    )

    assert RRSIConfig().benchmark_execution_opt_in is False
    assert RRSIConfig().remote_evaluation_opt_in is False
    assert RRSIConfig.load(config_path).benchmark_execution_opt_in is True
    assert RRSIConfig.load(config_path).remote_evaluation_opt_in is True


@pytest.mark.parametrize(
    "field",
    ["benchmark_execution_opt_in", "remote_inference_opt_in", "remote_evaluation_opt_in"],
)
def test_rrsi_config_rejects_non_boolean_opt_ins(tmp_path, field):
    config_path = tmp_path / "rrsi-invalid.json"
    config_path.write_text(json.dumps({field: "false"}), encoding="utf-8")

    with pytest.raises(ValueError, match=f"{field} must be a boolean"):
        RRSIConfig.load(config_path)
