from __future__ import annotations

import io
import subprocess

import pytest

from threadforge_domains.engineering.security import (
    MAX_COMMAND_CHARS,
    MAX_OUTPUT_BYTES,
    docker_run_argv,
    run_sandboxed,
    validate_local_docker,
    validate_task_workspace,
)


def _task(path):
    path.mkdir()
    (path / "output").mkdir()
    return path


def test_engineering_container_is_pinned_offline_and_scoped_to_one_task(tmp_path):
    task = tmp_path / "one-task"
    benchmark = tmp_path / "benchmarks"
    evaluator = tmp_path / "evaluator"
    _task(task)
    benchmark.mkdir()
    evaluator.mkdir()
    image = f"threadforge/engineering@sha256:{'a' * 64}"

    argv = docker_run_argv(
        "docker", image, task, "python3 check.py", benchmark_root=benchmark,
        evaluator_root=evaluator, container_name="threadforge-eng-" + "b" * 24,
    )

    assert "--network=none" in argv
    assert "--read-only" in argv
    assert "--entrypoint=/bin/sh" in argv
    assert "--cap-drop=ALL" in argv
    assert "--security-opt=no-new-privileges" in argv
    assert "--pull=never" in argv
    assert f"type=bind,src={task.resolve()},dst=/task,readonly" in argv
    assert f"type=bind,src={(task / 'output').resolve()},dst=/task/output,rw" in argv
    assert argv.count("--mount") == 2
    assert "--user=65532:65532" in argv
    image_index = argv.index(image)
    assert argv[image_index + 1:] == ["-lc", "python3 check.py"]
    assert "--privileged" not in argv
    assert "/var/run/docker.sock" not in " ".join(argv)


@pytest.mark.parametrize("image", ["threadforge/engineering:latest", "sha256:abc"])
def test_engineering_container_rejects_mutable_or_malformed_image(image, tmp_path):
    task = tmp_path / "task"
    benchmark = tmp_path / "benchmarks"
    evaluator = tmp_path / "evaluator"
    _task(task)
    benchmark.mkdir()
    evaluator.mkdir()
    with pytest.raises(ValueError, match="immutable sha256"):
        docker_run_argv("docker", image, task, "true", benchmark_root=benchmark,
                        evaluator_root=evaluator, container_name="threadforge-eng-" + "c" * 24)


def test_engineering_container_rejects_invalid_command_and_container_name(tmp_path):
    task = tmp_path / "task"
    benchmark = tmp_path / "benchmarks"
    evaluator = tmp_path / "evaluator"
    _task(task)
    benchmark.mkdir()
    evaluator.mkdir()
    image = f"threadforge/engineering@sha256:{'d' * 64}"
    name = "threadforge-eng-" + "e" * 24
    with pytest.raises(ValueError, match="command"):
        docker_run_argv("docker", image, task, "  ", benchmark_root=benchmark,
                        evaluator_root=evaluator, container_name=name)
    with pytest.raises(ValueError, match="command"):
        docker_run_argv("docker", image, task, "x" * (MAX_COMMAND_CHARS + 1),
                        benchmark_root=benchmark, evaluator_root=evaluator, container_name=name)
    with pytest.raises(ValueError, match="container name"):
        docker_run_argv("docker", image, task, "true", benchmark_root=benchmark,
                        evaluator_root=evaluator, container_name="threadforge-eng-latest")


@pytest.mark.parametrize("subdirectory", ["", ".", "..", "../outside", "nested/output", "C:temp"])
def test_engineering_container_rejects_unsafe_writable_subdirectory(tmp_path, subdirectory):
    task = _task(tmp_path / "task")
    benchmark = tmp_path / "benchmarks"
    evaluator = tmp_path / "evaluator"
    benchmark.mkdir()
    evaluator.mkdir()

    with pytest.raises(ValueError, match="one safe task child"):
        docker_run_argv(
            "docker", f"threadforge/engineering@sha256:{'a' * 64}", task, "true",
            benchmark_root=benchmark, evaluator_root=evaluator,
            container_name="threadforge-eng-" + "b" * 24,
            writable_subdirectory=subdirectory,
        )


def test_engineering_container_can_mount_a_separate_evaluation_results_child(tmp_path):
    task = tmp_path / "task"
    task.mkdir()
    (task / ".threadforge-eval-output").mkdir()
    benchmark = tmp_path / "benchmarks"
    evaluator = tmp_path / "evaluator"
    benchmark.mkdir()
    evaluator.mkdir()

    argv = docker_run_argv(
        "docker", f"threadforge/engineering@sha256:{'a' * 64}", task, "true",
        benchmark_root=benchmark, evaluator_root=evaluator,
        container_name="threadforge-eng-" + "c" * 24,
        writable_subdirectory=".threadforge-eval-output",
    )

    assert f"type=bind,src={(task / '.threadforge-eval-output').resolve()},dst=/task/.threadforge-eval-output,rw" in argv


def test_engineering_container_refuses_workspace_overlapping_protected_roots(tmp_path):
    task = tmp_path / "task"
    _task(task)
    benchmark = task / "benchmark"
    benchmark.mkdir()
    evaluator = tmp_path / "evaluator"
    evaluator.mkdir()

    with pytest.raises(ValueError, match="separate from benchmark and evaluator"):
        docker_run_argv(
            "docker", f"threadforge/engineering@sha256:{'3' * 64}", task, "true",
            benchmark_root=benchmark, evaluator_root=evaluator,
            container_name="threadforge-eng-" + "4" * 24,
        )


def test_engineering_workspace_must_be_absolute_existing_and_unlinked(tmp_path):
    task = tmp_path / "task"
    _task(task)
    assert validate_task_workspace(task) == task.resolve()

    with pytest.raises(ValueError, match="absolute, existing, unlinked"):
        validate_task_workspace("relative/task")
    with pytest.raises(ValueError, match="absolute, existing, unlinked"):
        validate_task_workspace(tmp_path / "missing")

    outside = tmp_path / "outside"
    outside.mkdir()
    link = tmp_path / "linked-task"
    try:
        link.symlink_to(outside, target_is_directory=True)
    except (OSError, NotImplementedError):
        return
    with pytest.raises(ValueError, match="absolute, existing, unlinked"):
        validate_task_workspace(link)
    linked_task = outside / "nested-task"
    _task(linked_task)
    with pytest.raises(ValueError, match="absolute, existing, unlinked"):
        validate_task_workspace(link / "nested-task")


def test_engineering_refuses_remote_docker_context(monkeypatch):
    monkeypatch.setattr(
        "threadforge_domains.engineering.security.subprocess.run",
        lambda *args, **kwargs: subprocess.CompletedProcess(args[0], 0, "tcp://remote.example:2376\n", ""),
    )
    with pytest.raises(RuntimeError, match="local default context"):
        validate_local_docker("docker", environ={"DOCKER_HOST": "tcp://ignored"})


def test_engineering_docker_context_environment_cannot_redirect_daemon(monkeypatch):
    observed = {}

    def fake_run(argv, **kwargs):
        observed["argv"] = argv
        observed["env"] = kwargs["env"]
        return subprocess.CompletedProcess(argv, 0, "unix:///var/run/docker.sock\n", "")

    monkeypatch.setattr("threadforge_domains.engineering.security.subprocess.run", fake_run)
    validate_local_docker("docker", environ={
        "PATH": "path", "DOCKER_HOST": "tcp://remote.example:2376",
        "DOCKER_CONTEXT": "remote", "OPENAI_API_KEY": "never-pass",
    })

    assert observed["argv"][1:3] == ["--context", "default"]
    assert "DOCKER_HOST" not in observed["env"]
    assert "DOCKER_CONTEXT" not in observed["env"]
    assert "OPENAI_API_KEY" not in observed["env"]


@pytest.mark.parametrize("payload_size, truncated", [(4, False), (MAX_OUTPUT_BYTES + 9, True)])
def test_engineering_sandbox_runs_with_bounded_output_and_local_image(
    tmp_path, monkeypatch, payload_size, truncated,
):
    task = tmp_path / "task"
    benchmark = tmp_path / "benchmarks"
    evaluator = tmp_path / "evaluator"
    _task(task)
    benchmark.mkdir()
    evaluator.mkdir()
    image = f"threadforge/engineering@sha256:{'f' * 64}"
    calls = {}
    payload = b"x" * payload_size

    class FakeProcess:
        stdout = io.BytesIO(payload)

        def wait(self, timeout=None):
            return 0

    monkeypatch.setattr("threadforge_domains.engineering.security.shutil.which", lambda _: "docker")
    monkeypatch.setattr("threadforge_domains.engineering.security.validate_local_docker", lambda *_: None)
    monkeypatch.setattr(
        "threadforge_domains.engineering.security.subprocess.run",
        lambda argv, **kwargs: subprocess.CompletedProcess(argv, 0, "[]", ""),
    )

    def fake_popen(argv, **kwargs):
        calls["argv"] = argv
        calls["env"] = kwargs["env"]
        calls["shell"] = kwargs["shell"]
        return FakeProcess()

    monkeypatch.setattr("threadforge_domains.engineering.security.subprocess.Popen", fake_popen)
    monkeypatch.setenv("OPENAI_API_KEY", "must-not-reach-docker-child")
    result = run_sandboxed(image, task, "python3 check.py", benchmark_root=benchmark,
                           evaluator_root=evaluator)

    assert result.returncode == 0
    assert result.output_truncated is truncated
    assert len(result.output.encode("utf-8")) <= MAX_OUTPUT_BYTES + 64
    assert "--network=none" in calls["argv"]
    assert "--pull=never" in calls["argv"]
    assert calls["shell"] is False
    assert "OPENAI_API_KEY" not in calls["env"]


def test_engineering_sandbox_stops_a_timed_out_container(tmp_path, monkeypatch):
    task = tmp_path / "task"
    benchmark = tmp_path / "benchmarks"
    evaluator = tmp_path / "evaluator"
    _task(task)
    benchmark.mkdir()
    evaluator.mkdir()
    image = f"threadforge/engineering@sha256:{'1' * 64}"
    calls = []

    class FakeProcess:
        stdout = io.BytesIO(b"stopped")
        waits = 0

        def wait(self, timeout=None):
            self.waits += 1
            if self.waits == 1:
                raise subprocess.TimeoutExpired("docker run", timeout)
            return 137

    process = FakeProcess()
    monkeypatch.setattr("threadforge_domains.engineering.security.shutil.which", lambda _: "docker")
    monkeypatch.setattr("threadforge_domains.engineering.security.validate_local_docker", lambda *_: None)
    monkeypatch.setattr(
        "threadforge_domains.engineering.security.subprocess.run",
        lambda argv, **kwargs: calls.append(argv) or subprocess.CompletedProcess(
            argv, 0, "[]" if "inspect" in argv else "", "",
        ),
    )
    monkeypatch.setattr("threadforge_domains.engineering.security.subprocess.Popen", lambda *_args, **_kwargs: process)

    result = run_sandboxed(image, task, "python3 slow.py", benchmark_root=benchmark,
                           evaluator_root=evaluator, timeout_seconds=1)

    assert result.timed_out is True
    assert result.returncode == 137
    assert len(calls) == 2  # local image inspection followed by docker stop
    assert calls[-1][3:6] == ["stop", "--time", "1"]


def test_engineering_sandbox_rejects_pinned_images_with_baked_credentials(monkeypatch):
    import json

    from threadforge_domains.engineering.security import _validate_image_environment

    def fake_run(argv, **_kwargs):
        return subprocess.CompletedProcess(argv, 0, json.dumps([
            "PATH=/usr/local/bin:/usr/bin",
            "LANG=C.UTF-8",
        ]), "")

    monkeypatch.setattr("threadforge_domains.engineering.security.subprocess.run", fake_run)
    _validate_image_environment("docker", "fixture@sha256:" + "a" * 64, {"PATH": "safe"})

    for environment in (
        ["OPENAI_API_KEY=do-not-repeat-this-secret"],
        ["PRIVATE_CREDENTIALS=/run/secrets/provider"],
        ["PIP_INDEX_URL=https://user:password@packages.invalid/simple"],
    ):
        monkeypatch.setattr(
            "threadforge_domains.engineering.security.subprocess.run",
            lambda argv, environment=environment, **_kwargs: subprocess.CompletedProcess(
                argv, 0, json.dumps(environment), "",
            ),
        )
        with pytest.raises(RuntimeError, match="credential-like") as error:
            _validate_image_environment("docker", "fixture@sha256:" + "a" * 64, {"PATH": "safe"})
        assert "do-not-repeat-this-secret" not in str(error.value)


@pytest.mark.parametrize("timeout", [0, 601, True, "1"])
def test_engineering_sandbox_rejects_invalid_timeout_before_docker(timeout, tmp_path):
    task = tmp_path / "task"
    benchmark = tmp_path / "benchmarks"
    evaluator = tmp_path / "evaluator"
    _task(task)
    benchmark.mkdir()
    evaluator.mkdir()
    with pytest.raises(ValueError, match="timeout"):
        run_sandboxed(f"threadforge/engineering@sha256:{'2' * 64}", task, "true",
                      benchmark_root=benchmark, evaluator_root=evaluator,
                      timeout_seconds=timeout)
