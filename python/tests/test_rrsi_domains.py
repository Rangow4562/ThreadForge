import json
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest

from rrsi import inventory as inventory_module
from rrsi.domain import load_domain
from rrsi.inventory import RRSI_TRUSTED_INVENTORIES, RRSI_SOURCE_REVISION
from rrsi.modelpolicy import evaluation_runtime


def _coding_bundle():
    sources = ["terminal-bench/terminal-bench-2-1", "swe-bench/swe-bench-verified"]
    task_sets = [["terminal-fixture-task"], ["swe-fixture-task"]]
    records = []
    for source_id, task_ids in zip(sources, task_sets):
        anchor = RRSI_TRUSTED_INVENTORIES["rrsi-coding"][source_id]
        records.append({
            "source_id": source_id,
            "source_revision": anchor.get("source_revision", RRSI_SOURCE_REVISION),
            "source_file_sha256": anchor["source_file_sha256"],
            "task_ids": task_ids,
        })
    return {
        "schema_version": "threadforge.rrsi-domain-inventory-bundle.v1",
        "domain": "rrsi-coding",
        "benchmark_inventories": records,
    }


def _load_coding_domain(tmp_path, monkeypatch):
    bundle = _coding_bundle()
    path = tmp_path / "coding-inventory.json"
    path.write_text(json.dumps(bundle), encoding="utf-8")
    sources = [entry["source_id"] for entry in bundle["benchmark_inventories"]]
    ids = {task_id.casefold() for entry in bundle["benchmark_inventories"] for task_id in entry["task_ids"]}
    monkeypatch.setattr(
        inventory_module,
        "_validate_benchmark_inventories",
        lambda domain, inventories: (ids, sorted(sources), []),
    )
    monkeypatch.setenv("THREADFORGE_RRSI_INVENTORY_BUNDLE", str(path))
    return load_domain("coding"), bundle


def _workspace_bundle():
    domain = "rrsi-agentic-workspace"
    sources = ["harvey-lab/evolve", "harvey-lab/held-out"]
    task_sets = [["area/evolve-fixture"], ["area/heldout-fixture"]]
    records = []
    for source_id, task_ids in zip(sources, task_sets):
        anchor = RRSI_TRUSTED_INVENTORIES[domain][source_id]
        records.append({
            "source_id": source_id,
            "source_revision": anchor.get("source_revision", RRSI_SOURCE_REVISION),
            "source_file_sha256": anchor["source_file_sha256"],
            "task_ids": task_ids,
        })
    return {
        "schema_version": "threadforge.rrsi-domain-inventory-bundle.v1",
        "domain": domain,
        "benchmark_inventories": records,
    }


def _load_workspace_domain(tmp_path, monkeypatch):
    bundle = _workspace_bundle()
    path = tmp_path / "workspace-inventory.json"
    path.write_text(json.dumps(bundle), encoding="utf-8")
    sources = [entry["source_id"] for entry in bundle["benchmark_inventories"]]
    ids = {task_id.casefold() for entry in bundle["benchmark_inventories"]
           for task_id in entry["task_ids"]}
    monkeypatch.setattr(
        inventory_module,
        "_validate_benchmark_inventories",
        lambda domain, inventories: (ids, sorted(sources), []),
    )
    monkeypatch.setenv("THREADFORGE_RRSI_INVENTORY_BUNDLE", str(path))
    return load_domain("workspace"), bundle


def test_local_evaluation_runtime_is_loopback_only_and_strips_credentials():
    model, environment = evaluation_runtime(False, {
        "THREADFORGE_LOCAL_MODEL_BASE_URL": "http://localhost:9000/v1",
        "THREADFORGE_LOCAL_EVALUATION_MODEL": "fixture-model",
        "OPENAI_API_KEY": "must-not-be-inherited",
        "GOOGLE_APPLICATION_CREDENTIALS": "must-not-be-inherited",
        "AWS_ACCESS_KEY_ID": "must-not-be-inherited",
        "PATH": "preserved",
    })

    assert model == "openai/fixture-model"
    assert environment["OPENAI_BASE_URL"] == "http://localhost:9000/v1"
    assert environment["OPENAI_API_KEY"] == "threadforge-local-no-secret"
    assert environment["PATH"] == "preserved"
    assert "GOOGLE_APPLICATION_CREDENTIALS" not in environment
    assert "AWS_ACCESS_KEY_ID" not in environment


@pytest.mark.parametrize(
    "environment, message",
    [
        ({"THREADFORGE_EVALUATION_BACKEND": "remote", "THREADFORGE_REMOTE_EVALUATION_MODEL": "provider/model"},
         "requires remote_inference_opt_in=true"),
        ({"THREADFORGE_LOCAL_MODEL_BASE_URL": "https://api.example.test/v1"}, "loopback HTTP"),
        ({"THREADFORGE_EVALUATION_BACKEND": "unknown"}, "must be local or remote"),
    ],
)
def test_evaluation_runtime_fails_closed(environment, message):
    with pytest.raises((PermissionError, ValueError), match=message):
        evaluation_runtime(False, environment)


def test_remote_evaluation_requires_explicit_opt_in_and_model():
    with pytest.raises(ValueError, match="requires a configured model"):
        evaluation_runtime(True, {"THREADFORGE_EVALUATION_BACKEND": "remote"})

    model, environment = evaluation_runtime(True, {
        "THREADFORGE_EVALUATION_BACKEND": "remote",
        "THREADFORGE_REMOTE_EVALUATION_MODEL": "provider/model-v1",
    })
    assert model == "provider/model-v1"
    assert environment["THREADFORGE_EVALUATION_BACKEND"] == "remote"


def test_coding_domain_uses_private_verified_inventories_for_both_splits(tmp_path, monkeypatch):
    domain, _bundle = _load_coding_domain(tmp_path, monkeypatch)

    assert domain.evolve_ids() == ["terminal-fixture-task"]
    assert domain.heldout_ids() == ["swe-fixture-task"]
    assert domain.smoke_ids() == ["terminal-fixture-task"]
    assert all("fixture" in pattern[0] or pattern in domain._base_critic_patterns
               for pattern in domain.critic_patterns)


def test_coding_domain_routes_evolve_and_heldout_to_distinct_harbor_datasets(tmp_path, monkeypatch):
    domain, _bundle = _load_coding_domain(tmp_path, monkeypatch)
    domain.benchmark_execution_opt_in = True
    calls = []
    monkeypatch.setattr(domain, "_harbor", lambda *args: calls.append(args))

    domain.run(tmp_path, tmp_path / "runs", "evolve-job", ["terminal-fixture-task"], 1)
    domain.run(tmp_path, tmp_path / "runs", "heldout-job", ["swe-fixture-task"], 1)

    assert calls[0][5] == "terminal-bench/terminal-bench-2-1"
    assert calls[1][5] == "swe-bench/swe-bench-verified"


def test_coding_direct_run_and_smoke_require_benchmark_opt_in_before_inventory_or_io(tmp_path):
    from rrsi.domain import load_domain

    domain = load_domain("coding")
    runs = tmp_path / "runs"
    with pytest.raises(PermissionError, match="benchmark_execution_opt_in=true"):
        domain.run(tmp_path, runs, "disabled-run", ["task"], 1)
    with pytest.raises(PermissionError, match="benchmark_execution_opt_in=true"):
        domain.smoke(tmp_path, runs, "disabled-smoke", ["task"])

    assert not runs.exists()


def test_incomplete_harbor_job_is_preserved_and_never_silently_replaced(tmp_path, monkeypatch):
    domain, _bundle = _load_coding_domain(tmp_path, monkeypatch)
    runs = tmp_path / "runs"
    existing = runs / "jobs" / "incomplete-job" / "fixture-result"
    existing.mkdir(parents=True)
    marker = existing / "user-note.txt"
    marker.write_text("preserve", encoding="utf-8")

    with pytest.raises(FileExistsError, match="choose a new job ID"):
        domain._harbor(tmp_path, runs, "incomplete-job", ["terminal-fixture-task"], 1,
                       "terminal-bench/terminal-bench-2-1", "incomplete-job")

    assert marker.read_text(encoding="utf-8") == "preserve"


def test_coding_score_reconciles_fixture_harbor_results(tmp_path, monkeypatch):
    domain, _bundle = _load_coding_domain(tmp_path, monkeypatch)
    job = "fixture-score"
    task_id = "terminal-fixture-task"
    task_dir = tmp_path / "runs" / "jobs" / job / "trial-0"
    task_dir.mkdir(parents=True)
    (task_dir / "result.json").write_text(json.dumps({
        "task_name": f"terminal-bench/{task_id}",
        "verifier_result": {"rewards": {"reward": 1.0}},
        "agent_result": {"n_input_tokens": 12, "n_output_tokens": 8},
    }), encoding="utf-8")

    per_task, extra = domain.score(tmp_path / "runs", job, [task_id], 1)

    assert per_task[task_id].rewards == [1.0]
    assert per_task[task_id].tokens == [20]
    assert extra["pass_rate"] == 1.0


def test_coding_score_rejects_present_result_without_verifier_reward(tmp_path, monkeypatch):
    domain, _bundle = _load_coding_domain(tmp_path, monkeypatch)
    task_id = "terminal-fixture-task"
    task_dir = tmp_path / "runs" / "jobs" / "rewardless-score" / "trial-0"
    task_dir.mkdir(parents=True)
    (task_dir / "result.json").write_text(json.dumps({
        "task_name": f"terminal-bench/{task_id}",
        "verifier_result": {},
    }), encoding="utf-8")

    with pytest.raises(RuntimeError, match="missing a verifier reward"):
        domain.score(tmp_path / "runs", "rewardless-score", [task_id], 1)


def test_coding_harbor_nonzero_exit_fails_instead_of_becoming_a_scored_job(tmp_path, monkeypatch):
    domain, _bundle = _load_coding_domain(tmp_path, monkeypatch)
    monkeypatch.setenv("RRSI_CODING_VENV", str(tmp_path / "harbor-venv"))
    adapter_module = sys.modules[type(domain).__module__]
    monkeypatch.setattr(adapter_module.subprocess, "run", lambda *args, **kwargs: SimpleNamespace(returncode=17))

    with pytest.raises(RuntimeError, match="failed with exit code 17"):
        domain._harbor(
            tmp_path,
            tmp_path / "runs",
            "failed-harbor-job",
            ["swe-fixture-task"],
            1,
            "swe-bench/swe-bench-verified",
            "failed-harbor-job",
        )


def test_coding_harbor_success_requires_exact_noninfra_trials_for_requested_tasks(tmp_path, monkeypatch):
    domain, _bundle = _load_coding_domain(tmp_path, monkeypatch)
    monkeypatch.setenv("RRSI_CODING_VENV", str(tmp_path / "harbor-venv"))
    adapter_module = sys.modules[type(domain).__module__]
    runs = tmp_path / "runs"

    def successful_harbor(*args, **kwargs):
        trial = runs / "jobs" / "verified-harbor-job" / "swe-fixture-task__attempt-0"
        trial.mkdir(parents=True)
        (trial / "result.json").write_text(json.dumps({
            "task_name": "swe-bench/swe-fixture-task",
            "verifier_result": {"rewards": {"reward": 1.0}},
            "agent_result": {"n_input_tokens": 11, "n_output_tokens": 7},
        }), encoding="utf-8")
        return SimpleNamespace(returncode=0)

    monkeypatch.setattr(adapter_module.subprocess, "run", successful_harbor)
    domain._harbor(
        tmp_path,
        runs,
        "verified-harbor-job",
        ["swe-fixture-task"],
        1,
        "swe-bench/swe-bench-verified",
        "verified-harbor-job",
    )

    per_task, summary = domain.score(runs, "verified-harbor-job", ["swe-fixture-task"], 1)
    assert per_task["swe-fixture-task"].rewards == [1.0]
    assert summary["pass_rate"] == 1.0


@pytest.mark.parametrize(
    "result, message",
    [
        (None, "exactly k valid trials"),
        ({"task_name": "swe-bench/swe-fixture-task", "verifier_result": {}}, "missing a verifier reward"),
        ({"task_name": "swe-bench/swe-fixture-task", "exception_info": {"exception_type": "RuntimeError", "exception_message": "docker daemon unavailable"}}, "infrastructure-failed trial"),
        ({"task_name": "swe-bench/unrequested-task", "verifier_result": {"rewards": {"reward": 0.0}}}, "outside the requested task set"),
    ],
)
def test_coding_harbor_zero_exit_rejects_incomplete_infra_or_unrequested_results(
    tmp_path, monkeypatch, result, message,
):
    domain, _bundle = _load_coding_domain(tmp_path, monkeypatch)
    monkeypatch.setenv("RRSI_CODING_VENV", str(tmp_path / "harbor-venv"))
    adapter_module = sys.modules[type(domain).__module__]
    runs = tmp_path / "runs"

    def fake_harbor(*args, **kwargs):
        job_dir = runs / "jobs" / "bad-harbor-job"
        job_dir.mkdir(parents=True)
        if result is not None:
            trial = job_dir / "output-trial"
            trial.mkdir(parents=True)
            (trial / "result.json").write_text(json.dumps(result), encoding="utf-8")
        return SimpleNamespace(returncode=0)

    monkeypatch.setattr(adapter_module.subprocess, "run", fake_harbor)
    with pytest.raises(RuntimeError, match=message):
        domain._harbor(
            tmp_path,
            runs,
            "bad-harbor-job",
            ["swe-fixture-task"],
            1,
            "swe-bench/swe-bench-verified",
            "bad-harbor-job",
        )


def test_workspace_domain_uses_only_verified_private_splits(tmp_path, monkeypatch):
    domain, _bundle = _load_workspace_domain(tmp_path, monkeypatch)

    assert domain.evolve_ids() == ["area/evolve-fixture"]
    assert domain.heldout_ids() == ["area/heldout-fixture"]
    assert domain.smoke_ids() == ["area/evolve-fixture"]

    with pytest.raises(ValueError, match="one verified split"):
        domain._split(["area/evolve-fixture", "area/heldout-fixture"])


def test_workspace_evaluation_requires_remote_judge_consent_before_writing(tmp_path, monkeypatch):
    domain, _bundle = _load_workspace_domain(tmp_path, monkeypatch)
    domain.benchmark_execution_opt_in = True
    runs = tmp_path / "runs"

    with pytest.raises(PermissionError, match="remote rubric judge"):
        domain.run(tmp_path, runs, "no-consent", ["area/evolve-fixture"], 1)

    assert not runs.exists()


def test_workspace_remote_judge_flag_is_strict_and_project_is_preflighted(tmp_path, monkeypatch):
    domain, _bundle = _load_workspace_domain(tmp_path, monkeypatch)
    domain.benchmark_execution_opt_in = True
    domain.remote_evaluation_opt_in = "false"
    with pytest.raises(ValueError, match="remote_evaluation_opt_in must be a boolean"):
        domain.run(tmp_path, tmp_path / "bad-flag", "bad-flag", ["area/evolve-fixture"], 1)

    domain.remote_evaluation_opt_in = True
    monkeypatch.delenv("VERTEX_PROJECT", raising=False)
    monkeypatch.delenv("GOOGLE_CLOUD_PROJECT", raising=False)
    with pytest.raises(RuntimeError, match="VERTEX_PROJECT"):
        domain.run(tmp_path, tmp_path / "missing-project", "missing-project",
                   ["area/evolve-fixture"], 1)
    assert not (tmp_path / "missing-project").exists()


def test_workspace_runtime_defaults_to_loopback_model_and_strips_credentials(tmp_path, monkeypatch):
    domain, _bundle = _load_workspace_domain(tmp_path, monkeypatch)
    monkeypatch.delenv("THREADFORGE_EVALUATION_BACKEND", raising=False)
    monkeypatch.setenv("THREADFORGE_LOCAL_EVALUATION_MODEL", "workspace-local-fixture")
    monkeypatch.setenv("OPENAI_API_KEY", "must-not-reach-agent-child")
    monkeypatch.setenv("GOOGLE_APPLICATION_CREDENTIALS", "must-not-reach-agent-child")

    source_root = Path(__file__).resolve().parents[2]
    env = domain._runtime_env(source_root, tmp_path / "runs", "runtime-fixture")

    assert env["ORCHESTRATOR_MODEL"] == "openai/workspace-local-fixture"
    assert env["OPENAI_BASE_URL"].startswith("http://127.0.0.1:")
    assert env["OPENAI_API_KEY"] == "threadforge-local-no-secret"
    assert "GOOGLE_APPLICATION_CREDENTIALS" not in env
    assert env["THREADFORGE_BENCHMARK_EXECUTION_OPT_IN"] == "false"


def test_workspace_runtime_assets_fall_back_to_packaged_resources(tmp_path, monkeypatch):
    domain, _bundle = _load_workspace_domain(tmp_path, monkeypatch)
    package_root = tmp_path / "installed" / "threadforge_domains" / "workspace"
    packaged_domain = package_root / "runtime" / "domain"
    packaged_archipelago = package_root / "runtime" / "archipelago"
    (packaged_domain / "mcp_gateway").mkdir(parents=True)
    (packaged_archipelago / "harness_workspace").mkdir(parents=True)
    (packaged_domain / "produce.sh").write_text("#!/bin/sh\n", encoding="utf-8")
    (packaged_domain / "workspace_driver.py").write_text("pass\n", encoding="utf-8")
    (packaged_archipelago / "harness_workspace" / "main.py").write_text("pass\n", encoding="utf-8")
    adapter_module = sys.modules[type(domain).__module__]
    monkeypatch.setattr(adapter_module, "HERE", package_root)

    runtime_domain, runtime_archipelago = domain._runtime_paths(tmp_path / "candidate-without-assets")

    assert runtime_domain == packaged_domain
    assert runtime_archipelago == packaged_archipelago


def test_workspace_scoring_reconciles_criterion_weights_and_tokens(tmp_path, monkeypatch):
    domain, _bundle = _load_workspace_domain(tmp_path, monkeypatch)
    task_id = "area/evolve-fixture"
    task_dir = tmp_path / "runs" / "jobs" / "workspace-score" / "s0" / "area__evolve-fixture"
    task_dir.mkdir(parents=True)
    (task_dir / "trajectory.json").write_text(json.dumps({
        "status": "completed", "usage": {"total_tokens": 321},
        "messages": [{"role": "assistant", "content": "fixture"}],
    }), encoding="utf-8")
    (task_dir / "scores.json").write_text(json.dumps({
        "task": "area__evolve-fixture", "passed": 1, "total": 2, "score": 0.5, "all_pass": 0,
        "criteria": [{"verdict": "pass"}, {"verdict": "fail"}],
    }), encoding="utf-8")

    per_task, extra = domain.score(tmp_path / "runs", "workspace-score", [task_id], 1)

    assert per_task[task_id].rewards == [0.5]
    assert per_task[task_id].weights == [2.0]
    assert per_task[task_id].tokens == [321]
    assert per_task[task_id].missing == 0
    assert extra["criterion_rate"] == 0.5
    assert extra["criteria_passed"] == 1
    assert extra["criteria_total"] == 2


def test_workspace_complete_job_requires_matching_manifest_and_valid_scores(tmp_path, monkeypatch):
    domain, _bundle = _load_workspace_domain(tmp_path, monkeypatch)
    task_id = "area/evolve-fixture"
    trial_dir = tmp_path / "runs" / "jobs" / "complete-fixture" / "s0"
    task_dir = trial_dir / "area__evolve-fixture"
    task_dir.mkdir(parents=True)
    (trial_dir / "_tasks.json").write_text(json.dumps([task_id]), encoding="utf-8")
    (task_dir / "trajectory.json").write_text(json.dumps({"status": "completed"}), encoding="utf-8")
    (task_dir / "scores.json").write_text(json.dumps({
        "task": "area__evolve-fixture", "passed": 1, "total": 1, "score": 1.0,
        "all_pass": 1, "criteria": [{"verdict": "pass"}],
    }), encoding="utf-8")

    assert domain._job_complete(tmp_path / "runs", "complete-fixture", [task_id], 1)
    (trial_dir / "_tasks.json").write_text(json.dumps(["different-task"]), encoding="utf-8")
    assert not domain._job_complete(tmp_path / "runs", "complete-fixture", [task_id], 1)


def test_workspace_invalid_score_is_counted_as_missing_not_accepted(tmp_path, monkeypatch):
    domain, _bundle = _load_workspace_domain(tmp_path, monkeypatch)
    task_id = "area/evolve-fixture"
    monkeypatch.setattr(domain, "_n_criteria", lambda _task: 3)
    task_dir = tmp_path / "runs" / "jobs" / "workspace-invalid" / "s0" / "area__evolve-fixture"
    task_dir.mkdir(parents=True)
    (task_dir / "trajectory.json").write_text("{}", encoding="utf-8")
    (task_dir / "scores.json").write_text(json.dumps({
        "task": "area__evolve-fixture", "passed": 2, "total": 2, "all_pass": 1,
        "criteria": [{"verdict": "pass"}, {"verdict": "fail"}],
    }), encoding="utf-8")

    per_task, _extra = domain.score(tmp_path / "runs", "workspace-invalid", [task_id], 1)

    assert per_task[task_id].rewards == [0.0]
    assert per_task[task_id].weights == [3.0]
    assert per_task[task_id].missing == 1


def test_workspace_partial_job_is_preserved(tmp_path, monkeypatch):
    domain, _bundle = _load_workspace_domain(tmp_path, monkeypatch)
    domain.benchmark_execution_opt_in = True
    domain.remote_evaluation_opt_in = True
    monkeypatch.setenv("VERTEX_PROJECT", "fixture-project")
    repo = Path(__file__).resolve().parents[2]
    monkeypatch.setattr(domain, "_verify_harvey_checkout", lambda: tmp_path)
    monkeypatch.setattr(domain, "_harvey_python", lambda _root: sys.executable)
    marker = tmp_path / "runs" / "jobs" / "partial-workspace" / "partial.txt"
    marker.parent.mkdir(parents=True)
    marker.write_text("preserve", encoding="utf-8")

    with pytest.raises(FileExistsError, match="partial results"):
        domain.run(repo, tmp_path / "runs", "partial-workspace", ["area/evolve-fixture"], 1)

    assert marker.read_text(encoding="utf-8") == "preserve"


def test_workspace_direct_run_requires_benchmark_opt_in_before_inventory_or_io(tmp_path):
    from rrsi.domain import load_domain

    domain = load_domain("workspace")
    runs = tmp_path / "runs"

    with pytest.raises(PermissionError, match="benchmark_execution_opt_in=true"):
        domain.run(tmp_path, runs, "disabled-run", ["area/evolve-fixture"], 1)

    assert not runs.exists()
