from __future__ import annotations

import importlib
import json
import os
import socket
import subprocess
import sys
import time
from pathlib import Path

import pytest


def _gateway_dependency(name: str):
    try:
        return importlib.import_module(name)
    except ImportError:
        message = f"Engineering HTTP integration dependency {name!r} is unavailable"
        if os.environ.get("THREADFORGE_REQUIRE_ENGINEERING_GATEWAY_HTTP") == "1":
            pytest.fail(message, pytrace=False)
        pytest.skip(message)


def test_gateway_dependency_can_skip_only_outside_required_ci(monkeypatch):
    def missing_dependency(name):
        raise ImportError(name)

    monkeypatch.setattr(importlib, "import_module", missing_dependency)
    monkeypatch.delenv("THREADFORGE_REQUIRE_ENGINEERING_GATEWAY_HTTP", raising=False)
    with pytest.raises(pytest.skip.Exception, match="unavailable"):
        _gateway_dependency("fake_gateway_dependency")

    monkeypatch.setenv("THREADFORGE_REQUIRE_ENGINEERING_GATEWAY_HTTP", "1")
    with pytest.raises(pytest.fail.Exception, match="unavailable"):
        _gateway_dependency("fake_gateway_dependency")


def _loopback_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def _rpc_response(response):
    assert response.status_code == 200, response.text[:2000]
    content_type = response.headers.get("content-type", "")
    if "text/event-stream" not in content_type:
        return response.json()
    for line in response.text.splitlines():
        if line.startswith("data:"):
            return json.loads(line.removeprefix("data:").strip())
    raise AssertionError("MCP event stream contained no JSON-RPC response")


def test_engineering_gateway_real_streamable_http_is_scoped_and_guarded(tmp_path):
    _gateway_dependency("fastmcp")
    _gateway_dependency("uvicorn")
    httpx = _gateway_dependency("httpx")

    python_root = Path(__file__).resolve().parents[1]
    workspace = tmp_path / "task-workspace"
    documents = workspace / "documents"
    output = workspace / "output"
    documents.mkdir(parents=True)
    output.mkdir()
    (documents / "design.txt").write_bytes(b"approved workspace fixture\n")
    outside = tmp_path / "outside-secret.txt"
    outside.write_text("must not be readable through the gateway\n", encoding="utf-8")

    port = _loopback_port()
    url = f"http://127.0.0.1:{port}/mcp/"
    keep_environment = {
        "PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP",
        "PYTHONPATH", "LANG",
    }
    server_environment = {
        key: value for key, value in os.environ.items()
        if key.upper() in keep_environment
    }
    server_environment.update({
        "GATEWAY_PORT": str(port),
        "THREADFORGE_ACTIVE_WORKSPACE": str(workspace),
        "PYTHONUTF8": "1",
    })
    server_log = tmp_path / "gateway-stderr.log"
    process = None

    with server_log.open("w+", encoding="utf-8") as log:
        try:
            process = subprocess.Popen(
                [sys.executable, "-m", "threadforge_domains.engineering.gateway"],
                cwd=python_root,
                env=server_environment,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=log,
                shell=False,
            )
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    log.flush()
                    log.seek(0)
                    raise AssertionError(f"MCP gateway exited during startup: {log.read()[-4000:]}")
                try:
                    with socket.create_connection(("127.0.0.1", port), timeout=0.25):
                        break
                except OSError:
                    time.sleep(0.05)
            else:
                log.flush()
                log.seek(0)
                raise AssertionError(f"MCP gateway did not start: {log.read()[-4000:]}")

            headers = {
                "Accept": "application/json, text/event-stream",
                "Content-Type": "application/json",
            }
            with httpx.Client(timeout=10, trust_env=False) as client:
                initialize = client.post(url, headers=headers, json={
                    "jsonrpc": "2.0",
                    "id": 1,
                    "method": "initialize",
                    "params": {
                        "protocolVersion": "2025-03-26",
                        "capabilities": {},
                        "clientInfo": {"name": "threadforge-gateway-smoke", "version": "1"},
                    },
                })
                initialized = _rpc_response(initialize)
                assert initialized["id"] == 1
                negotiated_version = initialized["result"]["protocolVersion"]
                session_id = initialize.headers.get("mcp-session-id")
                session_headers = {
                    **headers,
                    "MCP-Protocol-Version": negotiated_version,
                }
                if session_id:
                    session_headers["Mcp-Session-Id"] = session_id

                notification = client.post(url, headers=session_headers, json={
                    "jsonrpc": "2.0", "method": "notifications/initialized",
                })
                assert notification.status_code in {200, 202, 204}

                listed = _rpc_response(client.post(url, headers=session_headers, json={
                    "jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {},
                }))
                tools = {tool["name"]: tool for tool in listed["result"]["tools"]}
                assert set(tools) == {
                    "read_text_file", "list_files", "get_directory_tree", "search_files",
                    "write_file", "read_pdf", "code_exec",
                }
                assert tools["read_text_file"]["annotations"]["readOnlyHint"] is True
                assert tools["code_exec"]["annotations"]["openWorldHint"] is False

                read_result = _rpc_response(client.post(url, headers=session_headers, json={
                    "jsonrpc": "2.0",
                    "id": 3,
                    "method": "tools/call",
                    "params": {"name": "read_text_file", "arguments": {"path": "documents/design.txt"}},
                }))
                read_text = read_result["result"]["content"][0]["text"]
                assert json.loads(read_text)["text"] == "approved workspace fixture\n"

                denied_result = _rpc_response(client.post(url, headers=session_headers, json={
                    "jsonrpc": "2.0",
                    "id": 4,
                    "method": "tools/call",
                    "params": {"name": "read_text_file", "arguments": {"path": str(outside)}},
                }))
                denied_text = denied_result["result"]["content"][0]["text"]
                assert "outside" in denied_text.lower() or "workspace" in denied_text.lower()
                assert "must not be readable" not in denied_text

                write_result = _rpc_response(client.post(url, headers=session_headers, json={
                    "jsonrpc": "2.0",
                    "id": 5,
                    "method": "tools/call",
                    "params": {"name": "write_file", "arguments": {
                        "path": "output/result.md", "content": "fixture deliverable\n",
                    }},
                }))
                assert "Wrote" in write_result["result"]["content"][0]["text"]
                assert (output / "result.md").read_text(encoding="utf-8") == "fixture deliverable\n"

                untrusted_host = client.post(url, headers={**headers, "Host": "attacker.invalid"}, json={})
                assert untrusted_host.status_code == 403
                untrusted_origin = client.post(url, headers={
                    **headers,
                    "Origin": "https://attacker.invalid",
                }, json={})
                assert untrusted_origin.status_code == 403
        finally:
            if process is not None and process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
