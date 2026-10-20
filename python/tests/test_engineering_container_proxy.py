from __future__ import annotations

import http.client
import json

import pytest

from threadforge_domains.engineering.bridge import BrokerOperationError, MAX_FRAME_BYTES
from threadforge_domains.engineering.container_proxy import (
    BrokerHTTPProxy,
    run_agent_process,
)


def _post(proxy, payload, *, path="/v1/chat/completions", host=None):
    endpoint = proxy.server.server_address
    connection = http.client.HTTPConnection(endpoint[0], endpoint[1], timeout=2)
    raw = payload if isinstance(payload, bytes) else json.dumps(payload).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    if host is not None:
        headers["Host"] = host
    connection.request("POST", path, body=raw, headers=headers)
    response = connection.getresponse()
    result = response.status, response.read()
    connection.close()
    return result


def test_container_proxy_sends_only_chat_payload_to_broker_and_returns_completion():
    class Client:
        def __init__(self):
            self.payloads = []

        def complete(self, payload):
            self.payloads.append(payload)
            return {"id": "completion", "choices": [{"message": {"content": "ok"}}]}

    client = Client()
    with BrokerHTTPProxy(client) as proxy:
        assert proxy.base_url.startswith("http://127.0.0.1:")
        status, body = _post(proxy, {
            "model": "openai/threadforge-local",
            "messages": [{"role": "user", "content": "fixture"}],
            "max_tokens": 10,
        })

    assert status == 200
    assert json.loads(body) == {"id": "completion", "choices": [{"message": {"content": "ok"}}]}
    assert client.payloads == [{
        "model": "openai/threadforge-local",
        "messages": [{"role": "user", "content": "fixture"}],
        "max_tokens": 10,
    }]


def test_container_proxy_rejects_non_loopback_host_paths_and_bad_json_without_broker_calls():
    class Client:
        calls = 0

        def complete(self, _payload):
            self.calls += 1
            return {"choices": []}

    client = Client()
    with BrokerHTTPProxy(client) as proxy:
        # Rejections happen before the request body is read. Use empty bodies
        # here so Windows does not reset the client while closing a socket with
        # unread request bytes; malformed-body coverage is exercised below.
        wrong_host = _post(proxy, b"", host="attacker.example")
        wrong_path = _post(proxy, b"", path="/v1/models")
        malformed = _post(proxy, b'{"messages":[],"messages":[]}')

    assert wrong_host[0] == 403
    assert wrong_path[0] == 404
    assert malformed[0] == 400
    assert client.calls == 0
    assert b"attacker.example" not in wrong_host[1]


def test_container_proxy_bounds_http_frames_and_never_returns_provider_exception_text():
    class Client:
        def complete(self, _payload):
            raise BrokerOperationError("provider_unavailable")

    with BrokerHTTPProxy(Client()) as proxy:
        endpoint = proxy.server.server_address
        connection = http.client.HTTPConnection(*endpoint, timeout=2)
        connection.putrequest("POST", "/v1/chat/completions")
        connection.putheader("Content-Type", "application/json")
        connection.putheader("Content-Length", str(MAX_FRAME_BYTES))
        connection.endheaders()
        oversized_response = connection.getresponse()
        oversized_status = oversized_response.status
        oversized_response.read()
        connection.close()

        provider_status, provider_body = _post(proxy, {
            "messages": [{"role": "user", "content": "fixture"}],
        })

    assert oversized_status == 413
    assert provider_status == 503
    assert json.loads(provider_body)["error"]["message"] == "provider_unavailable"
    assert b"traceback" not in provider_body.lower()


def test_container_proxy_refuses_non_loopback_bind_and_invalid_port():
    client = object()
    with pytest.raises(ValueError, match="loopback"):
        BrokerHTTPProxy(client, host="0.0.0.0")
    with pytest.raises(ValueError, match="port"):
        BrokerHTTPProxy(client, port=65_536)


def test_agent_child_gets_only_loopback_endpoints_and_no_docker_stdio_or_provider_config(
    monkeypatch,
):
    import subprocess

    import threadforge_domains.engineering.container_proxy as proxy_module

    calls = {}

    class Process:
        pid = 1234

        def wait(self, timeout=None):
            calls["wait_timeout"] = timeout
            return 0

    def fake_popen(command, **kwargs):
        calls["command"] = command
        calls.update(kwargs)
        return Process()

    monkeypatch.setattr(proxy_module.subprocess, "Popen", fake_popen)
    result = run_agent_process(
        ["python3", "-m", "threadforge_domains.engineering.agent_entrypoint"],
        object(),
        timeout_seconds=12,
        environ={
            "PATH": "/safe/bin",
            "PYTHONPATH": "/opt/threadforge/archipelago",
            "RRSI_HARNESS_MODULE": "harness_eng.main",
            "OPENAI_API_KEY": "provider-secret",
            "ANTHROPIC_API_KEY": "provider-secret-too",
            "LITELLM_PROXY_API_BASE": "https://remote.invalid",
            "VERTEX_PROJECT": "private-project",
            "DOCKER_HOST": "tcp://remote.invalid",
        },
    )

    assert result.returncode == 0 and result.timed_out is False
    assert calls["command"] == ["python3", "-m", "threadforge_domains.engineering.agent_entrypoint"]
    assert calls["stdin"] is subprocess.DEVNULL
    assert calls["stdout"] is proxy_module.sys.stderr
    assert calls["stderr"] == subprocess.STDOUT
    assert calls["cwd"] == "/task"
    assert calls["shell"] is False
    assert calls["start_new_session"] is (proxy_module.os.name == "posix")
    assert calls["wait_timeout"] == 12
    child_env = calls["env"]
    assert child_env["PATH"] == "/safe/bin"
    assert child_env["PYTHONPATH"] == "/opt/threadforge/archipelago"
    assert child_env["RRSI_HARNESS_MODULE"] == "harness_eng.main"
    assert child_env["OPENAI_API_KEY"] == "threadforge-container-no-secret"
    assert child_env["OPENAI_API_BASE"].startswith("http://127.0.0.1:")
    assert child_env["THREADFORGE_MCP_GATEWAY_URL"] == "http://127.0.0.1:8996/mcp/"
    assert not {"ANTHROPIC_API_KEY", "LITELLM_PROXY_API_BASE", "VERTEX_PROJECT", "DOCKER_HOST"} & child_env.keys()


def test_agent_child_timeout_terminates_its_process_group(monkeypatch):
    import subprocess

    import threadforge_domains.engineering.container_proxy as proxy_module

    signals = []

    class Process:
        pid = 9876

        def __init__(self):
            self.waits = 0

        def wait(self, timeout=None):
            self.waits += 1
            if self.waits == 1:
                raise subprocess.TimeoutExpired("agent", timeout)
            return -15

        def terminate(self):
            signals.append("terminate")

    process = Process()
    monkeypatch.setattr(proxy_module.subprocess, "Popen", lambda *_args, **_kwargs: process)
    if proxy_module.os.name == "posix":
        monkeypatch.setattr(proxy_module.os, "killpg", lambda pid, signum: signals.append((pid, signum)))

    result = run_agent_process(["python3", "agent.py"], object(), timeout_seconds=1)

    assert result.timed_out is True
    assert result.returncode == -15
    if proxy_module.os.name == "posix":
        assert signals == [(9876, proxy_module.signal.SIGTERM)]
    else:
        assert signals == ["terminate"]


@pytest.mark.parametrize(
    "command,timeout,gateway",
    [
        ("python agent.py", 10, "http://127.0.0.1:8996/mcp/"),
        (["python", "agent.py"], True, "http://127.0.0.1:8996/mcp/"),
        (["python", "agent.py"], 10, "http://host.docker.internal:8996/mcp/"),
    ],
)
def test_agent_child_rejects_shell_and_unbounded_runtime_settings(command, timeout, gateway):
    with pytest.raises(ValueError):
        run_agent_process(command, object(), timeout_seconds=timeout, gateway_url=gateway)
