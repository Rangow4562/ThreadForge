# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Loopback-only OpenAI-compatible endpoint for the offline agent container.

The trusted launcher owns the Docker-attached stdio streams. Agent libraries
talk to this HTTP endpoint instead; completion requests are relayed through the
bounded host broker and provider configuration never enters the container.
"""

from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread
from typing import Any

from threadforge_domains.engineering.bridge import (
    MAX_FRAME_BYTES,
    BrokerOperationError,
    StdioBrokerClient,
)

MAX_HTTP_REQUEST_BYTES = MAX_FRAME_BYTES - 1
MAX_AGENT_TIMEOUT_SECONDS = 10_800
_API_PATH = "/v1/chat/completions"
_CHILD_ENV_KEYS = frozenset({
    "HOME", "LANG", "LOGNAME", "PATH", "PYTHONPATH", "PYTHONUNBUFFERED",
    "PYTHONDONTWRITEBYTECODE", "RRSI_HARNESS_MODULE", "TEMP", "TMP", "TMPDIR",
    "USER", "VIRTUAL_ENV", "WINDIR", "SYSTEMROOT", "GATEWAY_PORT",
})


@dataclass(frozen=True)
class AgentProcessResult:
    """Bounded process status for the isolated harness child."""

    returncode: int
    timed_out: bool


def _strict_json(raw: bytes) -> dict:
    def object_pairs(items):
        value = {}
        for key, child in items:
            if key in value:
                raise ValueError("duplicate JSON key")
            value[key] = child
        return value

    value = json.loads(
        raw.decode("utf-8", errors="strict"),
        object_pairs_hook=object_pairs,
        parse_constant=lambda _value: (_ for _ in ()).throw(ValueError("non-finite number")),
    )
    if not isinstance(value, dict):
        raise ValueError("request must be an object")
    return value


def _json_bytes(value: dict) -> bytes:
    return json.dumps(value, ensure_ascii=False, allow_nan=False,
                      separators=(",", ":")).encode("utf-8", errors="strict")


def _child_environment(
    source: dict[str, str] | None,
    *,
    proxy_url: str,
    gateway_url: str,
) -> dict[str, str]:
    """Pass runtime necessities and loopback endpoints, never provider config."""
    source = os.environ if source is None else source
    result = {
        key: value for key, value in source.items()
        if key in _CHILD_ENV_KEYS or (key.startswith("LC_") and len(key) <= 32)
    }
    result.update({
        "OPENAI_API_BASE": proxy_url,
        "OPENAI_BASE_URL": proxy_url,
        "OPENAI_API_KEY": "threadforge-container-no-secret",
        "THREADFORGE_MODEL_PROXY_URL": proxy_url,
        "THREADFORGE_MCP_GATEWAY_URL": gateway_url,
    })
    return result


def run_agent_process(
    command: list[str] | tuple[str, ...],
    client: StdioBrokerClient,
    *,
    timeout_seconds: int = 5_400,
    environ: dict[str, str] | None = None,
    gateway_url: str = "http://127.0.0.1:8996/mcp/",
) -> AgentProcessResult:
    """Run a container-local harness child behind the stdio-backed model proxy.

    The child receives no Docker-attached stdin/stdout, no provider credential,
    and no shell expansion. Its stdout is redirected to container stderr; the
    trusted parent retains stdout exclusively for broker frames.
    """
    if (not isinstance(command, (list, tuple)) or not command or
            len(command) > 64 or any(not isinstance(arg, str) or not arg or len(arg) > (256 << 10)
                                     for arg in command)):
        raise ValueError("agent command must contain 1..64 nonempty arguments")
    if (isinstance(timeout_seconds, bool) or not isinstance(timeout_seconds, int) or
            not 1 <= timeout_seconds <= MAX_AGENT_TIMEOUT_SECONDS):
        raise ValueError(
            f"agent timeout must be between 1 and {MAX_AGENT_TIMEOUT_SECONDS} seconds"
        )
    if gateway_url != "http://127.0.0.1:8996/mcp/":
        raise ValueError("agent MCP endpoint must use the task-local loopback gateway")

    timed_out = False
    with BrokerHTTPProxy(client) as proxy:
        env = _child_environment(environ, proxy_url=proxy.base_url, gateway_url=gateway_url)
        try:
            process = subprocess.Popen(
                list(command),
                stdin=subprocess.DEVNULL,
                stdout=sys.stderr,
                stderr=subprocess.STDOUT,
                cwd="/task",
                env=env,
                shell=False,
                start_new_session=(os.name == "posix"),
            )
        except OSError:
            raise RuntimeError("isolated agent process could not be started") from None
        try:
            returncode = process.wait(timeout=timeout_seconds)
        except subprocess.TimeoutExpired:
            timed_out = True
            try:
                if os.name == "posix":
                    os.killpg(process.pid, signal.SIGTERM)
                else:
                    process.terminate()
                returncode = process.wait(timeout=3)
            except (OSError, subprocess.TimeoutExpired):
                try:
                    if os.name == "posix":
                        os.killpg(process.pid, signal.SIGKILL)
                    else:
                        process.kill()
                except OSError:
                    pass
                returncode = process.wait()
    return AgentProcessResult(returncode, timed_out)


class _LoopbackHTTPServer(ThreadingHTTPServer):
    daemon_threads = True
    block_on_close = False
    request_queue_size = 16


class BrokerHTTPProxy:
    """Expose only POST /v1/chat/completions on a container-local loopback port."""

    def __init__(
        self,
        client: StdioBrokerClient,
        *,
        host: str = "127.0.0.1",
        port: int = 0,
    ):
        if host != "127.0.0.1":
            raise ValueError("model proxy must bind only to IPv4 loopback")
        if (isinstance(port, bool) or not isinstance(port, int) or
                not 0 <= port <= 65535):
            raise ValueError("model proxy port must be 0..65535")
        self.client = client
        self.server = _LoopbackHTTPServer((host, port), self._handler_type())
        self._thread: Thread | None = None

    @property
    def base_url(self) -> str:
        host, port = self.server.server_address[:2]
        return f"http://{host}:{port}/v1"

    @property
    def _expected_host(self) -> str:
        return f"127.0.0.1:{self.server.server_address[1]}"

    def _handler_type(self):
        proxy = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"
            server_version = "ThreadforgeModelProxy/1"
            sys_version = ""

            def log_message(self, _format: str, *args: Any) -> None:
                # Prompts, tool schemas, provider diagnostics, and model output
                # must not be copied into container logs by the HTTP server.
                return

            def _respond(self, status: int, body: dict) -> None:
                try:
                    raw = _json_bytes(body)
                except (TypeError, ValueError, UnicodeEncodeError, RecursionError):
                    status = 502
                    raw = b'{"error":{"message":"provider_unavailable","type":"server_error"}}'
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.send_header("Cache-Control", "no-store")
                self.send_header("Connection", "close")
                self.end_headers()
                try:
                    self.wfile.write(raw)
                except (BrokenPipeError, ConnectionResetError):
                    pass
                self.close_connection = True

            def do_POST(self) -> None:
                if self.path != _API_PATH:
                    self._respond(404, {"error": {"message": "not_found", "type": "invalid_request_error"}})
                    return
                hosts = self.headers.get_all("Host", [])
                if len(hosts) != 1 or hosts[0].lower() != proxy._expected_host:
                    self._respond(403, {"error": {"message": "loopback_only", "type": "invalid_request_error"}})
                    return
                content_types = self.headers.get_all("Content-Type", [])
                if len(content_types) != 1 or content_types[0].split(";", 1)[0].strip().lower() != "application/json":
                    self._respond(415, {"error": {"message": "json_required", "type": "invalid_request_error"}})
                    return
                lengths = self.headers.get_all("Content-Length", [])
                if len(lengths) != 1 or not lengths[0].isascii() or not lengths[0].isdigit():
                    self._respond(400, {"error": {"message": "invalid_content_length", "type": "invalid_request_error"}})
                    return
                length = int(lengths[0])
                if not 1 <= length <= MAX_HTTP_REQUEST_BYTES:
                    self._respond(413, {"error": {"message": "request_too_large", "type": "invalid_request_error"}})
                    return
                try:
                    payload = _strict_json(self.rfile.read(length))
                    response = proxy.client.complete(payload)
                    raw = _json_bytes(response)
                    if len(raw) > MAX_FRAME_BYTES:
                        raise ValueError("response too large")
                except (UnicodeDecodeError, json.JSONDecodeError, ValueError, RecursionError):
                    self._respond(400, {"error": {"message": "invalid_request", "type": "invalid_request_error"}})
                    return
                except BrokerOperationError as exc:
                    status = 503 if exc.code in {"model_unavailable", "provider_unavailable"} else 400
                    self._respond(status, {
                        "error": {"message": exc.code, "type": "server_error" if status == 503 else "invalid_request_error"}
                    })
                    return
                except Exception:
                    self._respond(502, {"error": {"message": "provider_unavailable", "type": "server_error"}})
                    return
                self._respond(200, response)

            def do_GET(self) -> None:
                self._respond(405, {"error": {"message": "method_not_allowed", "type": "invalid_request_error"}})

            def do_PUT(self) -> None:
                self.do_GET()

            def do_DELETE(self) -> None:
                self.do_GET()

        return Handler

    def start(self) -> "BrokerHTTPProxy":
        if self._thread is not None:
            raise RuntimeError("model proxy is already started")
        self._thread = Thread(
            target=self.server.serve_forever,
            kwargs={"poll_interval": 0.05},
            name="threadforge-container-model-proxy",
            daemon=True,
        )
        self._thread.start()
        return self

    def close(self) -> None:
        if self._thread is not None:
            self.server.shutdown()
            self._thread.join(timeout=2)
            self._thread = None
        self.server.server_close()

    def __enter__(self) -> "BrokerHTTPProxy":
        return self.start()

    def __exit__(self, _exc_type, _exc, _traceback) -> None:
        self.close()
