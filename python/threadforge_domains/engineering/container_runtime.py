# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Trusted orchestration entrypoint executed as PID 1 in the task container."""

from __future__ import annotations

import argparse
import os
import signal
import socket
import subprocess
import sys
import time
from pathlib import Path

from threadforge_domains.engineering.bridge import StdioBrokerClient
from threadforge_domains.engineering.container_proxy import (
    MAX_AGENT_TIMEOUT_SECONDS,
    run_agent_process,
)

GATEWAY_PORT = 8996
GATEWAY_URL = f"http://127.0.0.1:{GATEWAY_PORT}/mcp/"
ARCHIPELAGO_ROOT = Path("/opt/threadforge/archipelago")


def _inside_docker() -> bool:
    return Path("/.dockerenv").is_file()


def _minimal_environment(*, include_task_scope: bool) -> dict[str, str]:
    source = os.environ
    env = {
        key: source[key]
        for key in ("PATH", "LANG", "LC_ALL", "LC_CTYPE")
        if key in source
    }
    env.update({
        "HOME": "/tmp",
        "TMPDIR": "/tmp",
        "PYTHONDONTWRITEBYTECODE": "1",
        "PYTHONUNBUFFERED": "1",
        "PYTHONPATH": os.pathsep.join((
            "/task/.threadforge-agent",
            str(ARCHIPELAGO_ROOT),
        )),
        "RRSI_HARNESS_MODULE": "harness_eng.main",
        "GATEWAY_PORT": str(GATEWAY_PORT),
    })
    if include_task_scope:
        env["THREADFORGE_ACTIVE_WORKSPACE"] = "/task"
    return env


def _gateway_ready(process: subprocess.Popen, timeout_seconds: float = 10.0) -> bool:
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        if process.poll() is not None:
            return False
        try:
            with socket.create_connection(("127.0.0.1", GATEWAY_PORT), timeout=0.2):
                return True
        except OSError:
            time.sleep(0.05)
    return False


def _stop_gateway(process: subprocess.Popen) -> None:
    if process.poll() is not None:
        return
    try:
        if os.name == "posix":
            os.killpg(process.pid, signal.SIGTERM)
        else:
            process.terminate()
        process.wait(timeout=2)
    except (OSError, subprocess.TimeoutExpired):
        try:
            if os.name == "posix":
                os.killpg(process.pid, signal.SIGKILL)
            else:
                process.kill()
        except OSError:
            pass
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            pass


def run_container_agent(
    *,
    trajectory_id: str,
    timeout_seconds: int = 5_400,
    max_steps: int = 250,
    llm_timeout_seconds: int = 600,
) -> int:
    """Start the task-local MCP server, then run the candidate behind the proxy."""
    if not _inside_docker():
        return 71
    if (isinstance(timeout_seconds, bool) or not isinstance(timeout_seconds, int) or
            not 1 <= timeout_seconds <= MAX_AGENT_TIMEOUT_SECONDS or
            isinstance(max_steps, bool) or not isinstance(max_steps, int) or
            not 1 <= max_steps <= 1_000 or
            isinstance(llm_timeout_seconds, bool) or not isinstance(llm_timeout_seconds, int) or
            not 1 <= llm_timeout_seconds <= MAX_AGENT_TIMEOUT_SECONDS):
        return 71
    task = Path("/task")
    harness = task / ".threadforge-agent" / "harness_eng" / "main.py"
    output = task / "output"
    runner_registry = ARCHIPELAGO_ROOT / "runner" / "agents" / "registry.py"
    if (not task.is_dir() or not harness.is_file() or not output.is_dir() or
            not runner_registry.is_file()):
        return 71

    gateway_env = _minimal_environment(include_task_scope=True)
    try:
        gateway = subprocess.Popen(
            [sys.executable, "-m", "threadforge_domains.engineering.container_gateway"],
            stdin=subprocess.DEVNULL,
            stdout=sys.stderr,
            stderr=subprocess.STDOUT,
            cwd="/task",
            env=gateway_env,
            shell=False,
            start_new_session=(os.name == "posix"),
            close_fds=True,
        )
    except OSError:
        return 70

    try:
        if not _gateway_ready(gateway):
            return 70
        child_env = _minimal_environment(include_task_scope=False)
        # run_agent_process copies only its own allowlisted keys from this
        # environment, then adds a random loopback-only OpenAI proxy endpoint.
        client = StdioBrokerClient(sys.stdin.buffer, sys.stdout.buffer)
        result = run_agent_process(
            [
                sys.executable,
                "-m",
                "threadforge_domains.engineering.agent_entrypoint",
                "--trajectory-id",
                trajectory_id,
                "--timeout-seconds",
                str(timeout_seconds),
                "--max-steps",
                str(max_steps),
                "--llm-timeout-seconds",
                str(llm_timeout_seconds),
            ],
            client,
            timeout_seconds=min(MAX_AGENT_TIMEOUT_SECONDS, timeout_seconds + 5),
            environ=child_env,
            gateway_url=GATEWAY_URL,
        )
        return result.returncode
    except (OSError, RuntimeError, ValueError):
        return 70
    finally:
        _stop_gateway(gateway)


def _arguments(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--trajectory-id", required=True)
    parser.add_argument("--timeout-seconds", type=int, default=5_400)
    parser.add_argument("--max-steps", type=int, default=250)
    parser.add_argument("--llm-timeout-seconds", type=int, default=600)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    try:
        args = _arguments(argv)
    except SystemExit:
        return 71
    return run_container_agent(
        trajectory_id=args.trajectory_id,
        timeout_seconds=args.timeout_seconds,
        max_steps=args.max_steps,
        llm_timeout_seconds=args.llm_timeout_seconds,
    )


if __name__ == "__main__":
    raise SystemExit(main())
