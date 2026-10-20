from __future__ import annotations

import importlib
import json
import re
from pathlib import Path
from types import SimpleNamespace

import pytest

from rrsi import inventory as inventory_module
from rrsi.domain import load_domain
from rrsi.inventory import RRSI_TRUSTED_INVENTORIES, RRSI_SOURCE_REVISION
from threadforge_domains.engineering.runtime import RuntimePinError
from threadforge_domains.engineering.security import (
    BrokeredContainerResult,
)


def _engineering_bundle():
    domain = "rrsi-engineering-design"
    task_ids = ["engineering/open-fixture-01", "engineering/open-fixture-02"]
    records = []
    for source_id in ("engdesign-open", "engdesign-v1"):
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


def _load_engineering_domain(tmp_path, monkeypatch):
    bundle = _engineering_bundle()
    path = tmp_path / "engineering-inventory.json"
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
    return load_domain("engineering")


def _write_trial(domain, runs, job, task_id, trial, *, meta, verdict, trajectory=None):
    folder = domain._trial_dir(runs, job, task_id, trial)
    folder.mkdir(parents=True)
    (folder / "meta.json").write_text(json.dumps(meta), encoding="utf-8")
    (folder / "verdict.json").write_text(json.dumps(verdict), encoding="utf-8")
    if trajectory is not None:
        (folder / "traj.json").write_text(json.dumps(trajectory), encoding="utf-8")
    return folder


def test_engineering_domain_uses_verified_open_and_explicit_v1_surfaces(tmp_path, monkeypatch):
    domain = _load_engineering_domain(tmp_path, monkeypatch)
    open_ids = domain.surface_ids("engdesign-open")
    v1_ids = domain.surface_ids("engdesign-v1")

    assert domain.evolve_ids() == open_ids
    assert v1_ids == open_ids  # the hardened surface reuses the same task IDs
    assert domain.heldout_ids() == []  # it is not an ID-based holdout
    assert len(domain.smoke_ids()) == 2
    assert any(re.search(pattern, open_ids[0]) for pattern, _ in domain.critic_patterns)
    with pytest.raises(ValueError, match="unknown engineering benchmark surface"):
        domain.surface_ids("frontier-eng")


def test_engineering_run_and_smoke_fail_closed_before_side_effects(tmp_path, monkeypatch):
    domain = _load_engineering_domain(tmp_path, monkeypatch)
    task_ids = domain.evolve_ids()[:1]
    runs = tmp_path / "runs"

    with pytest.raises(PermissionError, match="benchmark_execution_opt_in=true"):
        domain.run(tmp_path, runs, "blocked-run", task_ids, 1)
    assert not runs.exists()

    domain.benchmark_execution_opt_in = True
    with pytest.raises(RuntimePinError, match="engineering_runtime_pin_required"):
        domain.run(tmp_path, runs, "blocked-run", task_ids, 1)
    assert not runs.exists()

    ok, details = domain.smoke(tmp_path, runs, "blocked-smoke", task_ids)
    assert ok is False
    assert details["reason_code"] == "engineering_runtime_pin_required"
    assert details["benchmark_execution_performed"] is False
    assert not runs.exists()


def test_engineering_domain_run_orchestrates_agent_evaluator_and_resume_safely(
    tmp_path, monkeypatch,
):
    domain = _load_engineering_domain(tmp_path, monkeypatch)
    domain.benchmark_execution_opt_in = True
    task_id = domain.evolve_ids()[0]
    runs = tmp_path / "runs"
    runs.mkdir()
    harness = tmp_path / "third_party" / "archipelago" / "harness_eng"
    harness.mkdir(parents=True)
    (harness / "main.py").write_text("# synthetic harness fixture\n", encoding="utf-8")
    harness_sha = importlib.import_module(
        "threadforge_domains.engineering.runtime"
    ).fingerprint_engineering_candidate_harness(harness)[0]
    runtime = SimpleNamespace(
        pin_sha256="a" * 64,
        image="fixture/image@sha256:" + "b" * 64,
        benchmark_root=tmp_path / "benchmark",
        evaluator_root=tmp_path / "evaluator",
        tasks={task_id: tmp_path / "benchmark" / "task"},
    )
    module = importlib.import_module(domain.__class__.__module__)
    calls = []
    monkeypatch.setattr(domain, "_verified_runtime", lambda *_args: runtime)

    def prepare(_runtime, *, task_id, runs_root, workspace_path, **kwargs):
        calls.append(("prepare", task_id, kwargs))
        workspace = Path(workspace_path)
        output = workspace / "output"
        output.mkdir(parents=True)
        (output / ".threadforge-agent-result.json").write_text(
            json.dumps({"messages": [{"role": "assistant", "content": "local trace"}]}),
            encoding="utf-8",
        )
        return SimpleNamespace(
            task_id=task_id,
            workspace=workspace,
            candidate_path=output / "payload.py",
            candidate_harness_sha256=harness_sha,
            workspace_sha256="c" * 64,
        )

    monkeypatch.setattr(module, "prepare_engineering_task_workspace", prepare)

    def fake_run(image, workspace, command, **kwargs):
        calls.append(("run", image, workspace, command, kwargs))
        return BrokeredContainerResult(
            returncode=0,
            requests_handled=1,
            model_calls=1,
            prompt_tokens=12,
            completion_tokens=8,
        )

    monkeypatch.setattr(module, "run_sandboxed_stdio", fake_run)
    monkeypatch.setattr(module, "collect_engineering_candidate",
                        lambda _prepared: (b"PAYLOAD = {'fixture': True}\n", True))

    def evaluate(_runtime, _prepared, **kwargs):
        calls.append(("evaluate", kwargs))
        return {"status": "ok", "passed": True, "valid": 1.0,
                "combined_score": 0.75, "no_payload": False}

    monkeypatch.setattr(module, "evaluate_engineering_candidate", evaluate)

    domain.run(tmp_path, runs, "integrated-fixture", [task_id], 1)

    folder = domain._trial_dir(runs, "integrated-fixture", task_id, 0)
    meta = json.loads((folder / "meta.json").read_text(encoding="utf-8"))
    verdict = json.loads((folder / "verdict.json").read_text(encoding="utf-8"))
    trajectory = json.loads((folder / "traj.json").read_text(encoding="utf-8"))
    assert (folder / "payload.py").read_text(encoding="utf-8") == "PAYLOAD = {'fixture': True}\n"
    assert meta["status"] == "completed"
    assert meta["total_tokens"] == 20
    assert meta["candidate_harness_sha256"] == harness_sha
    assert meta["trajectory_untrusted"] is True
    assert verdict["passed"] is True
    assert trajectory["messages"][0]["content"] == "local trace"
    assert [call[0] for call in calls] == ["prepare", "run", "evaluate"]
    assert calls[1][4]["remote_inference_opt_in"] is False
    assert calls[2][1]["protected_roots"] == (tmp_path.resolve(),)

    # A complete trial is not rerun or overwritten when the RRSI loop resumes.
    calls.clear()
    domain.run(tmp_path, runs, "integrated-fixture", [task_id], 1)
    assert calls == []
    per_task, _extra = domain.score(runs, "integrated-fixture", [task_id], 1)
    assert per_task[task_id].rewards == [1.0]


def test_engineering_domain_run_refuses_partial_trials_before_any_execution(
    tmp_path, monkeypatch,
):
    domain = _load_engineering_domain(tmp_path, monkeypatch)
    domain.benchmark_execution_opt_in = True
    ids = domain.evolve_ids()
    runs = tmp_path / "runs"
    runs.mkdir()
    harness = tmp_path / "third_party" / "archipelago" / "harness_eng"
    harness.mkdir(parents=True)
    (harness / "main.py").write_text("# fixture\n", encoding="utf-8")
    runtime = SimpleNamespace(pin_sha256="a" * 64)
    monkeypatch.setattr(domain, "_verified_runtime", lambda *_args: runtime)
    partial = domain._trial_dir(runs, "partial-fixture", ids[1], 0)
    partial.mkdir(parents=True)
    (partial / "meta.json").write_text("{}", encoding="utf-8")
    module = importlib.import_module(domain.__class__.__module__)
    executions = []
    monkeypatch.setattr(module, "run_sandboxed_stdio", lambda *args, **kwargs: executions.append(args))

    with pytest.raises(RuntimePinError, match="trial_incomplete_or_invalid"):
        domain.run(tmp_path, runs, "partial-fixture", ids, 1)

    assert executions == []


def test_engineering_smoke_runs_the_integrated_task_flow_only_after_opt_in(
    tmp_path, monkeypatch,
):
    domain = _load_engineering_domain(tmp_path, monkeypatch)
    domain.benchmark_execution_opt_in = True
    task_id = domain.evolve_ids()[0]
    runs = tmp_path / "runs"
    runs.mkdir()
    runtime = SimpleNamespace(
        pin_sha256="a" * 64,
        image="fixture/image@sha256:" + "b" * 64,
        benchmark_root=tmp_path / "benchmark",
        evaluator_root=tmp_path / "evaluator",
        tasks={task_id: tmp_path / "benchmark" / "task"},
    )
    calls = []
    monkeypatch.setattr(domain, "_verified_runtime", lambda *_args: runtime)

    def fake_run(*args, **kwargs):
        calls.append(("run", args, kwargs))

    monkeypatch.setattr(domain, "run", fake_run)
    monkeypatch.setattr(domain, "score", lambda *_args: (
        {task_id: SimpleNamespace(missing=0)},
        {"n_trials": 1, "pass_rate": 1.0, "valid_rate": 1.0,
         "no_payload_rate": 0.0, "mean_combined_score": 0.75},
    ))

    ok, details = domain.smoke(tmp_path, runs, "job", [task_id])

    assert ok is True
    assert details == {
        "stage": "complete",
        "reason_code": "engineering_smoke_passed",
        "runtime_pin_sha256": "a" * 64,
        "task_count": 1, "trial_count": 1, "missing_trials": 0,
        "pass_rate": 1.0, "valid_rate": 1.0, "no_payload_rate": 0.0,
        "benchmark_execution_performed": True,
    }
    assert len(calls) == 1
    assert calls[0][0] == "run"
    assert calls[0][1] == (tmp_path, runs, "job", [task_id], 1)
    assert calls[0][2] == {"log_prefix": "job"}


def test_engineering_scoring_reconciles_trials_and_counts_missing_as_zero(tmp_path, monkeypatch):
    domain = _load_engineering_domain(tmp_path, monkeypatch)
    runs = tmp_path / "runs"
    job = "score-fixture"
    task_ids = domain.evolve_ids()
    _write_trial(
        domain, runs, job, task_ids[0], 0,
        meta={"status": "completed", "no_payload": False, "total_tokens": 91,
              "provider_label": "private-provider", "debug": "unneeded"},
        verdict={"status": "ok", "passed": True, "valid": 1.0, "combined_score": 0.82,
                 "error": "private-error-detail"},
        trajectory={"messages": [{"role": "assistant", "content": "fixture trace"}]},
    )

    per_task, extra = domain.score(runs, job, task_ids, 1)

    assert per_task[task_ids[0]].rewards == [1.0]
    assert per_task[task_ids[0]].tokens == [91]
    assert per_task[task_ids[0]].missing == 0
    assert per_task[task_ids[1]].rewards == [0.0]
    assert per_task[task_ids[1]].tokens == [None]
    assert per_task[task_ids[1]].missing == 1
    assert extra == {
        "total_passes": 1, "n_trials": 2, "pass_rate": 0.5,
        "mean_combined_score": 0.41, "valid_rate": 0.5, "no_payload_rate": 0.5,
    }

    record = domain.load_trial(runs, job, task_ids[0], 0)
    assert record["trajectory"]["messages"][0]["content"] == "fixture trace"
    assert record["meta"] == {"status": "completed", "no_payload": False, "total_tokens": 91}
    assert record["verdict"] == {
        "status": "ok", "passed": True, "valid": 1.0, "combined_score": 0.82,
    }
    assert domain.render_trace(record).startswith("UNTRUSTED ENGINEERING TRIAL DATA")


def test_engineering_rejects_inconsistent_or_linked_result_records(tmp_path, monkeypatch):
    domain = _load_engineering_domain(tmp_path, monkeypatch)
    runs = tmp_path / "runs"
    job = "invalid-result"
    task_ids = domain.evolve_ids()[:1]
    _write_trial(
        domain, runs, job, task_ids[0], 0,
        meta={"status": "timeout", "no_payload": False, "total_tokens": 4},
        verdict={"status": "ERR_TIMEOUT", "passed": True, "valid": 1.0, "combined_score": 1.0},
    )
    per_task, _extra = domain.score(runs, job, task_ids, 1)
    assert per_task[task_ids[0]].rewards == [0.0]
    assert per_task[task_ids[0]].missing == 1

    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "meta.json").write_text(json.dumps({"no_payload": False}), encoding="utf-8")
    link_root = runs / "jobs" / "linked-result" / "engineering"
    link_root.parent.mkdir(parents=True)
    try:
        link_root.symlink_to(outside, target_is_directory=True)
    except (OSError, NotImplementedError):
        return
    folder = link_root / "open-fixture-01" / "t0"
    folder.mkdir(parents=True)
    (folder / "meta.json").write_text(
        json.dumps({"status": "completed", "no_payload": False, "total_tokens": 2}), encoding="utf-8",
    )
    (folder / "verdict.json").write_text(
        json.dumps({"status": "ok", "passed": True, "valid": 1.0, "combined_score": 1.0}),
        encoding="utf-8",
    )
    linked, _extra = domain.score(runs, "linked-result", task_ids[:1], 1)
    assert linked[task_ids[0]].missing == 1


def test_engineering_guards_apply_validity_and_submission_limits(tmp_path, monkeypatch):
    domain = _load_engineering_domain(tmp_path, monkeypatch)
    incumbent = SimpleNamespace(extra={"valid_rate": 0.95, "no_payload_rate": 0.01})
    candidate = SimpleNamespace(extra={"valid_rate": 0.90, "no_payload_rate": 0.04})

    reasons = domain.guards(incumbent, candidate)

    assert len(reasons) == 2
    assert "valid rate fell" in reasons[0]
    assert "no-submission rate rose" in reasons[1]
