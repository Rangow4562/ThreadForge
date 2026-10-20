from __future__ import annotations

import io
import json
import subprocess

from threadforge_domains.engineering.bridge import encode_request
from threadforge_domains.engineering.security import run_sandboxed_stdio


IMAGE = f"threadforge/engineering@sha256:{'a' * 64}"
MODEL_ENV = {
    "THREADFORGE_EVALUATION_BACKEND": "local",
    "THREADFORGE_LOCAL_MODEL_BASE_URL": "http://127.0.0.1:8123/v1",
    "THREADFORGE_LOCAL_EVALUATION_MODEL": "local-review-model",
    "OPENAI_API_KEY": "host-secret-must-stay-host-side",
}


class _WritableBuffer(io.BytesIO):
    def close(self):
        # Preserve captured protocol bytes for assertions after the worker exits.
        self.was_closed = True


class _FakeProcess:
    def __init__(self, *, stdout: bytes, stderr: bytes = b"", blocking: bool = False):
        self.stdin = _WritableBuffer()
        self.stdout = io.BytesIO(stdout)
        self.stderr = io.BytesIO(stderr)
        self.blocking = blocking
        self.stopped = False
        self.wait_calls = 0

    def wait(self, timeout=None):
        self.wait_calls += 1
        if self.stopped:
            return 137 if self.blocking else 143
        if self.blocking:
            raise subprocess.TimeoutExpired("docker run", timeout)
        return 0

    def poll(self):
        if self.stopped:
            return 137
        return None if self.blocking else 0

    def kill(self):
        self.stopped = True


def _sandbox_roots(tmp_path):
    task = tmp_path / "task"
    task.mkdir()
    (task / "output").mkdir()
    benchmark = tmp_path / "benchmark"
    evaluator = tmp_path / "evaluator"
    benchmark.mkdir()
    evaluator.mkdir()
    return task, benchmark, evaluator


def _mock_docker(monkeypatch, process, calls):
    import threadforge_domains.engineering.security as security

    monkeypatch.setattr(security.shutil, "which", lambda _name: "docker")
    monkeypatch.setattr(security, "validate_local_docker", lambda *_args, **_kwargs: None)

    def fake_run(argv, **_kwargs):
        calls.append(argv)
        if "stop" in argv:
            process.stopped = True
        return subprocess.CompletedProcess(argv, 0, "[]" if "inspect" in argv else "", "")

    monkeypatch.setattr(security.subprocess, "run", fake_run)
    monkeypatch.setattr(security.subprocess, "Popen", lambda *_args, **_kwargs: process)


def test_interactive_runner_bridges_model_calls_without_exposing_logs_or_secrets(
    tmp_path, monkeypatch,
):
    task, benchmark, evaluator = _sandbox_roots(tmp_path)
    request_id = "1" * 32
    request = encode_request(request_id, {
        "messages": [{"role": "user", "content": "synthetic test request"}],
        "model": "candidate-selected-model-is-ignored",
    })
    process = _FakeProcess(stdout=request, stderr=b"private candidate diagnostic")
    docker_calls = []
    popen_options = {}
    import threadforge_domains.engineering.security as security
    original_popen = security.subprocess.Popen
    _mock_docker(monkeypatch, process, docker_calls)

    def capture_popen(argv, **kwargs):
        popen_options.update(kwargs)
        popen_options["argv"] = argv
        return process

    monkeypatch.setattr(security.subprocess, "Popen", capture_popen)
    monkeypatch.setattr(security, "_docker_environment", lambda *_args: {"PATH": "safe-path"})
    monkeypatch.setenv("OPENAI_API_KEY", MODEL_ENV["OPENAI_API_KEY"])
    provider_calls = []

    def completion(**kwargs):
        provider_calls.append(kwargs)
        return {
            "id": "synthetic-completion",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}}],
            "usage": {"prompt_tokens": 13, "completion_tokens": 4},
        }

    result = run_sandboxed_stdio(
        IMAGE,
        task,
        "trusted-launcher --agent /task/harness",
        benchmark_root=benchmark,
        evaluator_root=evaluator,
        timeout_seconds=5,
        environ=MODEL_ENV,
        completion=completion,
    )

    assert result.returncode == 0
    assert result.requests_handled == result.model_calls == 1
    assert result.failed_model_calls == 0
    assert (result.prompt_tokens, result.completion_tokens, result.unmeasured_calls) == (13, 4, 0)
    assert result.stderr_bytes == len(b"private candidate diagnostic")
    assert result.stderr_sha256
    assert "private candidate diagnostic" not in repr(result)
    assert "--interactive" in popen_options["argv"]
    assert "--tty" not in popen_options["argv"]
    assert "--network=none" in popen_options["argv"]
    assert "/var/run/docker.sock" not in " ".join(popen_options["argv"])
    assert popen_options["stdin"] is subprocess.PIPE
    assert popen_options["stdout"] is subprocess.PIPE
    assert popen_options["stderr"] is subprocess.PIPE
    assert popen_options["shell"] is False
    assert "OPENAI_API_KEY" not in popen_options["env"]
    assert provider_calls[0]["model"] == "openai/local-review-model"
    assert provider_calls[0]["api_key"] == "threadforge-local-no-secret"
    assert provider_calls[0]["timeout"] <= 5
    assert "host-secret-must-stay-host-side" not in repr(provider_calls)
    response = json.loads(process.stdin.getvalue().decode("utf-8"))
    assert response["id"] == request_id
    assert response["ok"] is True
    assert docker_calls[0][3:5] == ["image", "inspect"]
    assert docker_calls[0][-1] == IMAGE
    assert original_popen is not None  # confirm the subprocess seam was patched, not invoked


def test_interactive_runner_stops_container_on_malformed_broker_protocol(tmp_path, monkeypatch):
    task, benchmark, evaluator = _sandbox_roots(tmp_path)
    process = _FakeProcess(stdout=b"not-json\n", blocking=True)
    docker_calls = []
    _mock_docker(monkeypatch, process, docker_calls)

    result = run_sandboxed_stdio(
        IMAGE,
        task,
        "trusted-launcher --agent /task/harness",
        benchmark_root=benchmark,
        evaluator_root=evaluator,
        timeout_seconds=5,
        environ=MODEL_ENV,
        completion=lambda **_kwargs: {},
    )

    assert result.protocol_error is True
    assert result.timed_out is False
    assert result.model_calls == 0
    assert process.stopped is True
    assert any("stop" in call for call in docker_calls)


def test_interactive_runner_stops_container_at_run_deadline(tmp_path, monkeypatch):
    task, benchmark, evaluator = _sandbox_roots(tmp_path)
    process = _FakeProcess(stdout=b"", blocking=True)
    docker_calls = []
    _mock_docker(monkeypatch, process, docker_calls)

    ticks = iter([0.0, 0.1, 1.2, 1.3, 1.4])
    monkeypatch.setattr("threadforge_domains.engineering.security.time.monotonic", lambda: next(ticks))

    result = run_sandboxed_stdio(
        IMAGE,
        task,
        "trusted-launcher --agent /task/harness",
        benchmark_root=benchmark,
        evaluator_root=evaluator,
        timeout_seconds=1,
        environ=MODEL_ENV,
        completion=lambda **_kwargs: {},
    )

    assert result.timed_out is True
    assert result.returncode == 137
    assert process.stopped is True
    assert any("stop" in call for call in docker_calls)
