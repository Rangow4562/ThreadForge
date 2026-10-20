from __future__ import annotations

import json
import importlib
import os
import stat
import subprocess
from types import SimpleNamespace

import pytest

from threadforge_domains.engineering.runtime import (
    PIN_SCHEMA,
    RuntimePinError,
    canonical_task_ids_sha256,
    collect_engineering_candidate,
    create_engineering_runtime_pin,
    load_engineering_runtime,
    prepare_engineering_evaluation_workspace,
    prepare_engineering_task_workspace,
    tree_sha256,
)
from rrsi import inventory as inventory_module
from threadforge_py.protocol import RRSI_SOURCE_REVISION
from threadforge_domains.engineering.security import ContainerResult

IMAGE = "registry.example/engineering@sha256:" + "a" * 64
TASK_ID = "fixture-01"
TASK_FILES = ["LLM_prompt.txt", "output_structure.py", "submission/payload.py", "images"]


def _write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data if isinstance(data, bytes) else data.encode("utf-8"))


def _runtime_trees(tmp_path):
    benchmark = tmp_path / "benchmark"
    evaluator = tmp_path / "evaluator"
    task = benchmark / "benchmarks" / "Fixture" / TASK_ID
    _write(task / "LLM_prompt.txt", "untrusted synthetic fixture\n")
    _write(task / "output_structure.py", "class Response: pass\n")
    _write(task / "submission" / "payload.py", "PAYLOAD = {}\n")
    _write(task / "images" / "diagram.txt", "synthetic image fixture\n")
    _write(task / "evaluate.py", "def evaluate(): return 0\n")
    _write(task / "frontier_eval" / "copy_files.txt", "\n".join(TASK_FILES) + "\n")
    _write(task / "frontier_eval" / "candidate_destination.txt", "submission/payload.py\n")
    _write(task / "frontier_eval" / "run_eval.sh", "#!/bin/sh\nexit 0\n")
    _write(task / "frontier_eval" / "evaluate_submission.py", "pass\n")
    _write(benchmark / "engdesign_manifest.json", json.dumps({
        "env_name": "fixture",
        "tasks": [{
            "task_id": TASK_ID,
            "domain": "Fixture",
            "rel": f"Fixture/{TASK_ID}",
            "withheld": ["evaluate.py"],
            "agent_files": TASK_FILES,
        }],
        "skipped": [],
    }))

    evaluator.mkdir(parents=True)
    _write(evaluator / "LICENSE", "fixture license reviewed for tests\n")
    _write(
        evaluator / "domains" / "eng" / "scripts" / "engdesign" / "evaluate_submission.py",
        "canonical synthetic evaluator; never execute in unit tests\n",
    )
    for args in (
        ["git", "init", "--quiet", str(evaluator)],
        ["git", "-C", str(evaluator), "config", "user.name", "Threadforge tests"],
        ["git", "-C", str(evaluator), "config", "user.email", "tests@example.invalid"],
        ["git", "-C", str(evaluator), "add", "-A"],
        ["git", "-C", str(evaluator), "commit", "--quiet", "-m", "fixture"],
    ):
        subprocess.run(args, check=True, capture_output=True, text=True)
    revision = subprocess.run(
        ["git", "-C", str(evaluator), "rev-parse", "HEAD"],
        check=True, capture_output=True, text=True,
    ).stdout.strip()
    return benchmark, evaluator, revision


def _write_pin(tmp_path, benchmark, evaluator, revision, *, pin_path=None, **overrides):
    pin_path = pin_path or tmp_path / "engineering-runtime-pin.json"
    data = {
        "schema_version": PIN_SCHEMA,
        "rrsi_source_revision": RRSI_SOURCE_REVISION,
        "evaluator_source_revision": revision,
        "benchmark_tree_sha256": tree_sha256(benchmark),
        "evaluator_tree_sha256": tree_sha256(evaluator),
        "task_count": 1,
        "task_ids_sha256": canonical_task_ids_sha256([TASK_ID]),
        "execution_image": IMAGE,
        "benchmark_license": "fixture-only",
        "evaluator_license": "fixture-only",
        "license_review_id": "fixture-license-review",
    }
    data.update(overrides)
    _write(pin_path, json.dumps(data))
    return pin_path


def _load(tmp_path, benchmark, evaluator, pin_path, *, task_ids=None, protected=None):
    return load_engineering_runtime(
        benchmark_root=benchmark,
        evaluator_root=evaluator,
        pin_path=pin_path,
        task_ids=[TASK_ID] if task_ids is None else task_ids,
        image=IMAGE,
        protected_roots=tuple(protected or (tmp_path / "candidate", tmp_path / "runs")),
    )


def test_runtime_pin_reconciles_task_set_and_external_roots(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)

    runtime = _load(tmp_path, benchmark, evaluator, pin)

    assert list(runtime.tasks) == [TASK_ID]
    assert runtime.image == IMAGE
    assert runtime.evaluator_source_revision == revision
    assert runtime.license_review_id == "fixture-license-review"
    assert "domains/eng/scripts/engdesign/evaluate_submission.py" in runtime.evaluator_files
    assert len(runtime.pin_sha256) == 64


def test_runtime_pin_allows_candidate_worktree_nested_under_runs_root(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runs = tmp_path / "runs"
    candidate = runs / "wt" / "variant-a"
    candidate.mkdir(parents=True)

    runtime = _load(
        tmp_path, benchmark, evaluator, pin,
        protected=(candidate, runs),
    )

    assert runtime.tasks[TASK_ID].is_dir()


def test_runtime_pin_rejects_changed_benchmark_bytes(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    _write(benchmark / "benchmarks" / "Fixture" / TASK_ID / "LLM_prompt.txt", "changed\n")

    with pytest.raises(RuntimePinError, match="runtime_tree_pin_mismatch"):
        _load(tmp_path, benchmark, evaluator, pin)


def test_runtime_pin_rejects_dirty_evaluator_checkout(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    _write(evaluator / "LICENSE", "modified after pin\n")

    with pytest.raises(RuntimePinError, match="evaluator_checkout_not_clean"):
        _load(tmp_path, benchmark, evaluator, pin)


def test_runtime_pin_rejects_task_manifest_visibility_expansion(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    record = json.loads((benchmark / "engdesign_manifest.json").read_text())
    record["tasks"][0]["agent_files"].append("evaluate.py")
    _write(benchmark / "engdesign_manifest.json", json.dumps(record))
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)

    with pytest.raises(RuntimePinError, match="benchmark_agent_visibility_invalid"):
        _load(tmp_path, benchmark, evaluator, pin)


def test_runtime_pin_must_be_outside_candidate_and_run_roots(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    candidate = tmp_path / "candidate"
    candidate.mkdir()
    pin = _write_pin(tmp_path, benchmark, evaluator, revision,
                     pin_path=candidate / "runtime-pin.json")

    with pytest.raises(RuntimePinError, match="runtime_pin_must_be_outside_runtime_and_candidate_roots"):
        _load(tmp_path, benchmark, evaluator, pin)


def test_runtime_pin_rejects_casefold_duplicate_task_ids(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)

    with pytest.raises(RuntimePinError, match="verified_task_set_invalid"):
        _load(tmp_path, benchmark, evaluator, pin, task_ids=["fixture-01", "FIXTURE-01"])


def test_staging_copies_only_pinned_agent_files_and_maps_seed_to_output(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    workspace_path = runs / "workspaces" / "fixture-job" / "t0"

    prepared = prepare_engineering_task_workspace(
        runtime,
        task_id=TASK_ID,
        runs_root=runs,
        workspace_path=workspace_path,
        protected_roots=(tmp_path / "candidate",),
    )

    assert prepared.prompt_path.read_text() == "untrusted synthetic fixture\n"
    assert prepared.schema_path.is_file()
    assert prepared.candidate_path.read_text() == "PAYLOAD = {}\n"
    assert (prepared.workspace / "images" / "diagram.txt").read_text() == "synthetic image fixture\n"
    assert not (prepared.workspace / "evaluate.py").exists()
    assert not (prepared.workspace / "frontier_eval").exists()
    assert not (prepared.workspace / "submission").exists()
    assert len(prepared.workspace_sha256) == 64
    if os.name == "posix":
        assert stat.S_IMODE(prepared.workspace.parent.stat().st_mode) == 0o700
        assert stat.S_IMODE(prepared.workspace.stat().st_mode) == 0o555
        assert stat.S_IMODE(prepared.prompt_path.stat().st_mode) == 0o444
        assert stat.S_IMODE(prepared.schema_path.stat().st_mode) == 0o444
        assert stat.S_IMODE((prepared.workspace / "images").stat().st_mode) == 0o555
        assert stat.S_IMODE((prepared.workspace / "output").stat().st_mode) == 0o777
        assert stat.S_IMODE(prepared.candidate_path.stat().st_mode) == 0o666
    raw, submitted = collect_engineering_candidate(prepared)
    assert raw == b"PAYLOAD = {}\n"
    assert submitted is False

    _write(prepared.candidate_path, "PAYLOAD = {'answer': 1}\n")
    raw, submitted = collect_engineering_candidate(prepared)
    assert raw == b"PAYLOAD = {'answer': 1}\n"
    assert submitted is True
    _write(prepared.candidate_path,
           "PAYLOAD = {'reasoning': 'TODO: solve the task described in LLM_prompt.txt'}\n")
    _raw, submitted = collect_engineering_candidate(prepared)
    assert submitted is False
    prepared.candidate_path.unlink()
    raw, submitted = collect_engineering_candidate(prepared)
    assert raw is None
    assert submitted is False


def test_task_staging_copies_candidate_harness_as_read_only_hashed_source(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    candidate_root = tmp_path / "candidate"
    source = candidate_root / "third_party" / "archipelago" / "harness_eng"
    _write(source / "main.py", "from .tools import answer\n")
    _write(source / "tools.py", "def answer(): return 1\n")
    _write(source / "README.md", "documentation is not executable runtime\n")
    _write(source / "__pycache__" / "main.cpython-312.pyc", b"generated bytecode")

    prepared = prepare_engineering_task_workspace(
        runtime,
        task_id=TASK_ID,
        runs_root=runs,
        workspace_path=runs / "workspaces" / "candidate-a" / "t0",
        protected_roots=(candidate_root,),
        candidate_harness_source=source,
    )

    staged = prepared.workspace / ".threadforge-agent" / "harness_eng"
    assert (staged / "main.py").read_text() == "from .tools import answer\n"
    assert (staged / "tools.py").read_text() == "def answer(): return 1\n"
    assert not (staged / "README.md").exists()
    assert not (staged / "__pycache__").exists()
    assert prepared.candidate_harness_file_count == 2
    assert prepared.candidate_harness_bytes == len(
        b"from .tools import answer\ndef answer(): return 1\n"
    )
    assert prepared.candidate_harness_sha256 is not None
    assert len(prepared.candidate_harness_sha256) == 64
    assert len(prepared.workspace_sha256) == 64
    if os.name == "posix":
        assert stat.S_IMODE((staged / "main.py").stat().st_mode) == 0o444
        assert stat.S_IMODE(staged.stat().st_mode) == 0o555
        assert stat.S_IMODE(prepared.candidate_path.stat().st_mode) == 0o666


def test_candidate_harness_staging_rejects_missing_entrypoint_and_data_files_before_workspace(
    tmp_path,
):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    source = tmp_path / "candidate" / "harness_eng"
    _write(source / "tools.py", "def answer(): return 1\n")
    target = runs / "workspaces" / "candidate-b" / "t0"

    with pytest.raises(RuntimePinError, match="candidate_harness_entrypoint_missing"):
        prepare_engineering_task_workspace(
            runtime, task_id=TASK_ID, runs_root=runs, workspace_path=target,
            candidate_harness_source=source,
        )
    assert not target.exists()

    _write(source / "main.py", "print('candidate')\n")
    _write(source / "credentials.json", "{}")
    with pytest.raises(RuntimePinError, match="candidate_harness_source_file_type_unsupported"):
        prepare_engineering_task_workspace(
            runtime, task_id=TASK_ID, runs_root=runs,
            workspace_path=runs / "workspaces" / "candidate-c" / "t0",
            candidate_harness_source=source,
        )
    assert not (runs / "workspaces" / "candidate-c" / "t0").exists()


def test_task_staging_allows_runs_root_with_separate_nested_candidate_worktree(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    candidate_root = runs / "wt" / "variant-a"
    candidate_root.mkdir(parents=True)

    prepared = prepare_engineering_task_workspace(
        runtime,
        task_id=TASK_ID,
        runs_root=runs,
        workspace_path=runs / "workspaces" / "variant-a" / "t0",
        protected_roots=(candidate_root,),
    )

    assert prepared.prompt_path.is_file()


def test_task_staging_rejects_workspace_inside_candidate_worktree_before_writing(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    candidate_root = runs / "wt" / "variant-a"
    candidate_root.mkdir(parents=True)
    target = candidate_root / "generated" / "t0"

    with pytest.raises(RuntimePinError, match="agent_workspace_overlaps_candidate_root"):
        prepare_engineering_task_workspace(
            runtime,
            task_id=TASK_ID,
            runs_root=runs,
            workspace_path=target,
            protected_roots=(candidate_root,),
        )

    assert not (candidate_root / "generated").exists()


def test_staging_refuses_existing_workspace_without_deleting_user_files(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    workspace = runs / "workspaces" / "existing"
    workspace.mkdir(parents=True)
    if os.name == "posix":
        workspace.parent.chmod(0o700)
    marker = workspace / "user-owned.txt"
    marker.write_text("preserve me")

    with pytest.raises(RuntimePinError, match="agent_workspace_already_exists"):
        prepare_engineering_task_workspace(
            runtime, task_id=TASK_ID, runs_root=runs, workspace_path=workspace,
        )
    assert marker.read_text() == "preserve me"


@pytest.mark.skipif(os.name != "posix", reason="POSIX permission boundary")
def test_staging_refuses_shared_intermediate_workspace_directory(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    parent = runs / "workspaces"
    parent.mkdir()
    if os.name == "posix":
        parent.chmod(0o755)

    expected = "agent_workspace_parent_not_private"
    with pytest.raises(RuntimePinError, match=expected):
        prepare_engineering_task_workspace(
            runtime,
            task_id=TASK_ID,
            runs_root=runs,
            workspace_path=parent / "job" / "t0",
        )


def test_staging_detects_source_file_change_after_pin(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    _write(benchmark / "benchmarks" / "Fixture" / TASK_ID / "images" / "diagram.txt", "changed")

    with pytest.raises(RuntimePinError, match="benchmark_file_changed_after_pin"):
        prepare_engineering_task_workspace(
            runtime,
            task_id=TASK_ID,
            runs_root=runs,
            workspace_path=runs / "workspaces" / "fixture-job" / "workspace",
        )


def test_evaluation_staging_uses_canonical_pinned_evaluator_and_candidate_bytes(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    agent = prepare_engineering_task_workspace(
        runtime,
        task_id=TASK_ID,
        runs_root=runs,
        workspace_path=runs / "workspaces" / "job" / "agent" / "t0",
        protected_roots=(tmp_path / "candidate",),
    )
    candidate = b"PAYLOAD = {'answer': 42}\n"
    _write(agent.candidate_path, candidate)

    prepared = prepare_engineering_evaluation_workspace(
        runtime,
        task_id=TASK_ID,
        candidate_bytes=candidate,
        runs_root=runs,
        workspace_path=runs / "workspaces" / "job" / "evaluator" / "t0",
        agent_workspace=agent.workspace,
        protected_roots=(tmp_path / "candidate",),
    )

    canonical = (evaluator / "domains" / "eng" / "scripts" / "engdesign" /
                 "evaluate_submission.py").read_bytes()
    benchmark_wrapper = benchmark / "benchmarks" / "Fixture" / TASK_ID / "frontier_eval" / "evaluate_submission.py"
    assert prepared.candidate_path.read_bytes() == candidate
    assert prepared.evaluator_path.read_bytes() == canonical
    assert prepared.evaluator_path.read_bytes() != benchmark_wrapper.read_bytes()
    assert (prepared.workspace / "evaluate.py").is_file()
    assert (prepared.workspace / "frontier_eval" / "run_eval.sh").is_file()
    assert prepared.writable_subdirectory == ".threadforge-eval-output"
    assert prepared.metrics_path.parent.is_dir()
    assert not prepared.metrics_path.exists()
    assert prepared.file_count >= 8
    assert len(prepared.workspace_sha256) == 64
    if os.name == "posix":
        assert stat.S_IMODE(prepared.workspace.stat().st_mode) == 0o555
        assert stat.S_IMODE(prepared.candidate_path.stat().st_mode) == 0o444
        assert stat.S_IMODE(prepared.metrics_path.parent.stat().st_mode) == 0o777


def test_evaluator_staging_allows_runs_root_and_nested_candidate_worktree(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    candidate_root = runs / "wt" / "variant-a"
    candidate_root.mkdir(parents=True)
    agent = prepare_engineering_task_workspace(
        runtime,
        task_id=TASK_ID,
        runs_root=runs,
        workspace_path=runs / "workspaces" / "variant-a" / "agent" / "t0",
        protected_roots=(candidate_root,),
    )

    prepared = prepare_engineering_evaluation_workspace(
        runtime,
        task_id=TASK_ID,
        candidate_bytes=b"PAYLOAD = {'answer': 7}\n",
        runs_root=runs,
        workspace_path=runs / "workspaces" / "variant-a" / "evaluation" / "t0",
        agent_workspace=agent.workspace,
        protected_roots=(candidate_root, runs),
    )

    assert prepared.metrics_path.parent.is_dir()


def test_evaluator_stage_rejects_changed_canonical_source_after_pin(tmp_path):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    agent = prepare_engineering_task_workspace(
        runtime, task_id=TASK_ID, runs_root=runs,
        workspace_path=runs / "workspaces" / "job" / "agent" / "t0",
    )
    candidate = b"PAYLOAD = {'answer': 1}\n"
    _write(agent.candidate_path, candidate)
    _write(
        evaluator / "domains" / "eng" / "scripts" / "engdesign" / "evaluate_submission.py",
        "changed canonical evaluator\n",
    )

    with pytest.raises(RuntimePinError, match="evaluator_file_changed_after_pin"):
        prepare_engineering_evaluation_workspace(
            runtime, task_id=TASK_ID, candidate_bytes=candidate,
            runs_root=runs,
            workspace_path=runs / "workspaces" / "job" / "evaluator" / "t0",
            agent_workspace=agent.workspace,
        )


def test_evaluator_execution_is_sandboxed_and_returns_only_allowlisted_metrics(tmp_path, monkeypatch):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    agent = prepare_engineering_task_workspace(
        runtime, task_id=TASK_ID, runs_root=runs,
        workspace_path=runs / "workspaces" / "job" / "agent" / "t0",
    )
    candidate = b"PAYLOAD = {'answer': 1}\n"
    _write(agent.candidate_path, candidate)
    adapter = importlib.import_module("threadforge_domains.engineering.adapter")
    calls = []

    def fake_run(image, workspace, command, **kwargs):
        calls.append((image, workspace, command, kwargs))
        metrics_path = workspace / ".threadforge-eval-output" / "metrics.json"
        metrics_path.write_text(json.dumps({
            "combined_score": 0.75, "valid": 1.0, "passed": True,
            "error": "must not escape evaluator summary",
        }), encoding="utf-8")
        return ContainerResult(0, "synthetic evaluator output")

    monkeypatch.setattr(adapter, "run_sandboxed", fake_run)
    result = adapter.evaluate_engineering_candidate(
        runtime, agent, runs_root=runs,
        workspace_path=runs / "workspaces" / "job" / "evaluator" / "t0",
    )

    assert result == {
        "status": "ok", "passed": True, "valid": 1.0,
        "combined_score": 0.75, "no_payload": False,
    }
    image, workspace, command, options = calls[0]
    assert image == runtime.image
    assert (workspace / "frontier_eval" / "evaluate_submission.py").read_bytes() == (
        evaluator / "domains" / "eng" / "scripts" / "engdesign" / "evaluate_submission.py"
    ).read_bytes()
    assert "python3 /task/frontier_eval/evaluate_submission.py" in command
    assert "--candidate /task/submission/payload.py" in command
    assert "frontier_eval/run_eval.sh" not in command
    assert options["timeout_seconds"] == 240
    assert options["writable_subdirectory"] == ".threadforge-eval-output"


def test_no_payload_skips_evaluator_and_never_calls_docker(tmp_path, monkeypatch):
    benchmark, evaluator, revision = _runtime_trees(tmp_path)
    pin = _write_pin(tmp_path, benchmark, evaluator, revision)
    runtime = _load(tmp_path, benchmark, evaluator, pin)
    runs = tmp_path / "runs"
    runs.mkdir()
    agent = prepare_engineering_task_workspace(
        runtime, task_id=TASK_ID, runs_root=runs,
        workspace_path=runs / "workspaces" / "job" / "agent" / "t0",
    )
    adapter = importlib.import_module("threadforge_domains.engineering.adapter")
    calls = []
    monkeypatch.setattr(adapter, "run_sandboxed", lambda *args, **kwargs: calls.append(args))

    result = adapter.evaluate_engineering_candidate(
        runtime, agent, runs_root=runs,
        workspace_path=runs / "workspaces" / "job" / "evaluator" / "t0",
    )

    assert result == {
        "status": "no_payload", "passed": False, "valid": 0.0,
        "combined_score": 0.0, "no_payload": True,
    }
    assert calls == []


def test_runtime_pin_creation_is_no_replace_and_inventory_bound(tmp_path, monkeypatch):
    benchmark, evaluator, _revision = _runtime_trees(tmp_path)
    inventory = tmp_path / "private-inventory.json"
    _write(inventory, "private fixture bundle")
    monkeypatch.setattr(
        inventory_module,
        "load_domain_inventory",
        lambda path, domain: SimpleNamespace(evolve_ids=(TASK_ID,)),
    )
    output = tmp_path / "private-config" / "engineering-runtime-pin.json"
    output.parent.mkdir()
    runs = tmp_path / "runs"
    candidate = runs / "wt" / "variant-a"
    candidate.mkdir(parents=True)
    protected = (candidate, runs)

    receipt = create_engineering_runtime_pin(
        benchmark_root=benchmark,
        evaluator_root=evaluator,
        inventory_bundle=inventory,
        output_path=output,
        image=IMAGE,
        benchmark_license="fixture-only",
        evaluator_license="fixture-only",
        license_review_id="fixture-license-review",
        protected_roots=protected,
        confirm_license_review=True,
    )

    runtime = _load(tmp_path, benchmark, evaluator, output, protected=protected)
    assert receipt == {"pin_sha256": runtime.pin_sha256, "task_count": 1}
    with pytest.raises(RuntimePinError, match="runtime_pin_output_exists_or_invalid"):
        create_engineering_runtime_pin(
            benchmark_root=benchmark,
            evaluator_root=evaluator,
            inventory_bundle=inventory,
            output_path=output,
            image=IMAGE,
            benchmark_license="fixture-only",
            evaluator_license="fixture-only",
            license_review_id="fixture-license-review",
            protected_roots=protected,
            confirm_license_review=True,
        )


def test_runtime_pin_creation_requires_explicit_license_review_ack(tmp_path):
    with pytest.raises(RuntimePinError, match="license_review_confirmation_required"):
        create_engineering_runtime_pin(
            benchmark_root=tmp_path / "not-read-benchmark",
            evaluator_root=tmp_path / "not-read-evaluator",
            inventory_bundle=tmp_path / "not-read-inventory",
            output_path=tmp_path / "pin.json",
            image=IMAGE,
            benchmark_license="fixture-only",
            evaluator_license="fixture-only",
            license_review_id="fixture-license-review",
            protected_roots=(),
            confirm_license_review=False,
        )
    assert not (tmp_path / "pin.json").exists()
