from __future__ import annotations

import io
import json

import pytest

from threadforge_domains.engineering import agent_entrypoint, container_gateway, container_runtime
from threadforge_domains.engineering.security import ContainerResult


def _workspace(tmp_path, monkeypatch):
    root = tmp_path / "task"
    (root / "output" / "work").mkdir(parents=True)
    (root / "documents").mkdir()
    (root / "documents" / "fixture.pdf").write_bytes(b"fixture pdf")
    (root / "LLM_prompt.txt").write_text("fixture prompt", encoding="utf-8")
    monkeypatch.setenv("THREADFORGE_ACTIVE_WORKSPACE", str(root))
    return root


def test_container_gateway_refuses_to_execute_tools_outside_docker(tmp_path, monkeypatch):
    root = _workspace(tmp_path, monkeypatch)
    monkeypatch.setattr(container_gateway, "_inside_docker", lambda: False)
    calls = []
    monkeypatch.setattr(container_gateway, "_run_local_command", lambda *args, **kwargs: calls.append(args))

    code = container_gateway.code_exec("echo must-not-run")
    pdf = container_gateway.read_pdf("documents/fixture.pdf")

    assert code["success"] is False
    assert "container-only" in code["output"]
    assert "container-only" in pdf
    assert calls == []
    assert root.is_dir()


def test_container_gateway_exec_and_pdf_tools_use_task_scoped_container_paths(
    tmp_path, monkeypatch,
):
    root = _workspace(tmp_path, monkeypatch)
    monkeypatch.setattr(container_gateway, "_inside_docker", lambda: True)
    calls = []

    def fake_run(argv, *, cwd, timeout_seconds):
        calls.append((argv, cwd, timeout_seconds))
        return ContainerResult(0, "synthetic isolated output")

    monkeypatch.setattr(container_gateway, "_run_local_command", fake_run)
    monkeypatch.setenv("OPENAI_API_KEY", "must-not-enter-code-exec")
    monkeypatch.setenv("THREADFORGE_MODEL_PROXY_URL", "http://127.0.0.1:12345/v1")

    result = container_gateway.code_exec("python3 --version", "output/work", 30)
    pdf = container_gateway.read_pdf("documents/fixture.pdf", max_pages=2)
    denied = container_gateway.code_exec("echo no", str(tmp_path / "outside"), 30)
    env = container_gateway._exec_environment()

    assert result == {
        "success": True, "output": "synthetic isolated output",
        "returncode": 0, "timed_out": False,
    }
    assert pdf == "synthetic isolated output"
    assert len(calls) == 2
    assert calls[0][0] == ["/bin/sh", "-lc", "python3 --version"]
    assert calls[0][1] == root / "output" / "work"
    assert calls[0][2] == 30
    assert calls[1][0][0:2] == ["python3", "-c"]
    assert calls[1][0][2].startswith("import fitz,sys;")
    assert calls[1][0][3] == "/task/documents/fixture.pdf"
    assert calls[1][2] == 120
    assert denied["success"] is False
    assert "OPENAI_API_KEY" not in env
    assert "THREADFORGE_MODEL_PROXY_URL" not in env


@pytest.mark.parametrize(
    "relative,valid",
    [
        ("LLM_prompt.txt", True),
        ("output/payload.py", True),
        ("../outside", False),
        ("/etc/passwd", False),
    ],
)
def test_agent_entrypoint_resolves_only_regular_files_inside_task(tmp_path, relative, valid):
    root = tmp_path / "task"
    (root / "output").mkdir(parents=True)
    (root / "LLM_prompt.txt").write_text("prompt", encoding="utf-8")
    (root / "output" / "payload.py").write_text("PAYLOAD = {}", encoding="utf-8")

    if valid:
        assert agent_entrypoint._task_file(root, relative).is_file()
    else:
        with pytest.raises(ValueError):
            agent_entrypoint._task_file(root, relative)


def test_agent_entrypoint_accepts_only_loopback_proxy_and_gateway_urls():
    assert agent_entrypoint._loopback_url("http://127.0.0.1:4321/v1", path="/v1")
    assert agent_entrypoint._loopback_url("http://127.0.0.1:8996/mcp/", path="/mcp/")
    for url in (
        "https://127.0.0.1:4321/v1",
        "http://localhost:4321/v1",
        "http://127.0.0.1:4321/v1?target=remote",
        "http://user:pass@127.0.0.1:4321/v1",
        "http://host.docker.internal:4321/v1",
    ):
        assert not agent_entrypoint._loopback_url(url, path="/v1")


def test_agent_entrypoint_writes_bounded_private_trajectory_no_replace(tmp_path):
    output_dir = tmp_path / "output"
    output_dir.mkdir()

    class Output:
        def model_dump(self, *, mode):
            assert mode == "json"
            return {"status": "completed", "messages": [{"content": "local trace"}]}

    agent_entrypoint._write_trajectory(output_dir, Output())
    result_path = output_dir / ".threadforge-agent-result.json"
    assert json.loads(result_path.read_text(encoding="utf-8"))["status"] == "completed"
    before = result_path.read_bytes()
    agent_entrypoint._write_trajectory(output_dir, Output())
    assert result_path.read_bytes() == before


def test_container_runtime_refuses_host_execution_and_filters_image_environment(monkeypatch):
    monkeypatch.setattr(container_runtime, "_inside_docker", lambda: False)
    calls = []
    monkeypatch.setattr(container_runtime.subprocess, "Popen", lambda *a, **k: calls.append(a))
    assert container_runtime.run_container_agent(trajectory_id="fixture") == 71
    assert calls == []

    monkeypatch.setenv("OPENAI_API_KEY", "image-baked-secret")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "image-baked-secret")
    monkeypatch.setenv("DOCKER_HOST", "tcp://remote.invalid")
    monkeypatch.setenv("PATH", "/trusted/bin")
    env = container_runtime._minimal_environment(include_task_scope=True)
    assert env["PATH"] == "/trusted/bin"
    assert env["THREADFORGE_ACTIVE_WORKSPACE"] == "/task"
    assert "OPENAI_API_KEY" not in env
    assert "ANTHROPIC_API_KEY" not in env
    assert "DOCKER_HOST" not in env


def test_container_gateway_command_capture_never_executes_test_code_on_host(monkeypatch, tmp_path):
    process_state = {"wait_calls": 0}

    class FakeProcess:
        pid = 7878
        stdout = io.BytesIO(b"bounded fixture output")

        def wait(self, timeout=None):
            process_state["wait_calls"] += 1
            return 0

        def kill(self):
            process_state["killed"] = True

        def terminate(self):
            process_state["terminated"] = True

    observed = {}

    def fake_popen(argv, **kwargs):
        observed["argv"] = argv
        observed.update(kwargs)
        return FakeProcess()

    monkeypatch.setattr(container_gateway.subprocess, "Popen", fake_popen)
    if container_gateway.os.name == "posix":
        monkeypatch.setattr(container_gateway.os, "killpg", lambda *_args: None)

    result = container_gateway._run_local_command(
        ["/bin/sh", "-lc", "this fixture is never executed"],
        cwd=tmp_path,
        timeout_seconds=4,
    )

    assert result.output == "bounded fixture output"
    assert result.returncode == 0 and result.timed_out is False
    assert observed["argv"][-1] == "this fixture is never executed"
    assert observed["shell"] is False
    assert observed["cwd"] == tmp_path
    assert observed["env"]["HOME"] == "/tmp"
    assert process_state["wait_calls"] == 1
