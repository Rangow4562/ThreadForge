# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Task-scoped, network-disabled Docker execution for engineering tools.

The benchmark checkout and evaluator must never be mounted into this runner.
The task workspace is read-only except for its dedicated output child, and
execution refuses to fall back to a host shell or an unpinned image.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import stat
import subprocess
import threading
import time
import uuid
from dataclasses import dataclass
from pathlib import Path

from threadforge_py.protocol import _contains_credential
from threadforge_domains.workspace.security import valid_pinned_image
from threadforge_domains.engineering.bridge import (
    BridgeProtocolError,
    BrokerOperationError,
    serve_stdio,
)
from threadforge_domains.engineering.modelbroker import (
    MAX_MODEL_REQUEST_TIMEOUT_SECONDS,
    ModelBroker,
)

MAX_COMMAND_CHARS = 256 << 10
MAX_OUTPUT_BYTES = 128 << 10
MAX_TIMEOUT_SECONDS = 600
MAX_INTERACTIVE_TIMEOUT_SECONDS = 10_800
_LOCAL_DOCKER_HOST = ("unix://", "npipe://")
_SAFE_CONTAINER_NAME = re.compile(r"threadforge-eng-[0-9a-f]{24}\Z")
_SAFE_WRITABLE_SUBDIRECTORY = re.compile(r"[A-Za-z0-9._-]{1,80}\Z")
_SENSITIVE_ENV_NAME = re.compile(
    r"(?:API[_-]?KEY|ACCESS[_-]?(?:KEY|TOKEN)|REFRESH[_-]?TOKEN|TOKEN|SECRET|"
    r"PASSWORD|PASSWD|CREDENTIALS?|AUTHORIZATION|PRIVATE[_-]?KEY)",
    re.IGNORECASE,
)
_URL_CREDENTIALS = re.compile(r"[a-z][a-z0-9+.-]*://[^/@\s:]+:[^/@\s]+@", re.IGNORECASE)
MAX_IMAGE_ENV_BYTES = 1 << 20


@dataclass(frozen=True)
class ContainerResult:
    """Bounded result of one isolated task command."""

    returncode: int
    output: str
    timed_out: bool = False
    output_truncated: bool = False


@dataclass(frozen=True)
class BrokeredContainerResult:
    """Sanitized outcome and usage counters for one brokered agent run."""

    returncode: int
    timed_out: bool = False
    protocol_error: bool = False
    broker_error: bool = False
    requests_handled: int = 0
    model_calls: int = 0
    failed_model_calls: int = 0
    prompt_tokens: int = 0
    completion_tokens: int = 0
    unmeasured_calls: int = 0
    stderr_sha256: str = ""
    stderr_bytes: int = 0
    stderr_truncated: bool = False


def _has_link_component(path: Path) -> bool:
    """Reject symlinks and Windows reparse points in a workspace path."""
    try:
        current = path
        while True:
            info = current.lstat()
            if stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & 0x400):
                return True
            if current == current.parent:
                return False
            current = current.parent
    except (OSError, RuntimeError, ValueError):
        return True


def validate_task_workspace(value: str | os.PathLike[str]) -> Path:
    """Return one existing absolute, non-linked task directory."""
    try:
        raw = Path(value)
        if (not raw.is_absolute() or (raw.drive.startswith("\\\\") if raw.drive else False)
                or ".." in raw.parts or _has_link_component(raw)):
            raise ValueError
        resolved = raw.resolve(strict=True)
        if not resolved.is_dir():
            raise ValueError
        # Docker's --mount value is comma-delimited; reject ambiguous host paths.
        if "," in str(resolved):
            raise ValueError
        return resolved
    except (OSError, RuntimeError, TypeError, ValueError):
        raise ValueError("engineering task workspace must be an absolute, existing, unlinked local directory") from None


def _validate_command(command: str) -> None:
    if not isinstance(command, str) or not command.strip() or len(command) > MAX_COMMAND_CHARS:
        raise ValueError(f"engineering command must contain 1..{MAX_COMMAND_CHARS} characters")


def _validate_writable_subdirectory(value: str) -> None:
    if (not isinstance(value, str) or not _SAFE_WRITABLE_SUBDIRECTORY.fullmatch(value) or
            value in {".", ".."}):
        raise ValueError("engineering writable subdirectory must be one safe task child")


def docker_run_argv(
    docker: str,
    image: str,
    workspace: str | os.PathLike[str],
    command: str,
    *,
    benchmark_root: str | os.PathLike[str],
    evaluator_root: str | os.PathLike[str],
    container_name: str,
    writable_subdirectory: str = "output",
) -> list[str]:
    """Build a strictly isolated container invocation without executing it."""
    if not valid_pinned_image(image):
        raise ValueError("engineering execution image must use an immutable sha256 digest")
    _validate_command(command)
    if not isinstance(container_name, str) or not _SAFE_CONTAINER_NAME.fullmatch(container_name):
        raise ValueError("engineering container name is invalid")
    _validate_writable_subdirectory(writable_subdirectory)
    task_root = validate_task_workspace(workspace)
    output_root = validate_task_workspace(task_root / writable_subdirectory)
    if output_root.parent != task_root or output_root.name != writable_subdirectory:
        raise ValueError("engineering writable directory must be a direct task workspace child")
    protected = (validate_task_workspace(benchmark_root), validate_task_workspace(evaluator_root))
    if any(task_root.is_relative_to(root) or root.is_relative_to(task_root) for root in protected):
        raise ValueError("engineering task workspace must be separate from benchmark and evaluator roots")
    return [
        docker, "--context", "default", "run", "--rm", "--pull=never",
        "--entrypoint=/bin/sh",
        "--name", container_name,
        "--network=none", "--read-only", "--cap-drop=ALL",
        "--security-opt=no-new-privileges", "--pids-limit=128",
        "--memory=4g", "--cpus=2", "--ulimit", "nofile=1024:1024",
        "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m",
        "--mount", f"type=bind,src={task_root},dst=/task,readonly",
        "--mount", f"type=bind,src={output_root},dst=/task/{writable_subdirectory},rw",
        "--workdir=/task", "--user=65532:65532",
        image, "-lc", command,
    ]


def _docker_environment(source: dict[str, str] | None = None) -> dict[str, str]:
    """Keep Docker client setup, but never honor environment-selected daemons."""
    source = os.environ if source is None else source
    env = {
        key: value for key, value in source.items()
        if key in {"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TEMP", "TMP"}
    }
    # In particular, DOCKER_HOST / DOCKER_CONTEXT must not redirect execution
    # to an operator-configured remote daemon.
    return env


def validate_local_docker(docker: str, *, environ: dict[str, str] | None = None) -> None:
    """Require Docker's default context to target a local Unix/npipe socket."""
    env = _docker_environment(environ)
    try:
        result = subprocess.run(
            [docker, "--context", "default", "context", "inspect",
             "--format", "{{.Endpoints.docker.Host}}", "default"],
            capture_output=True, text=True, timeout=10, check=False, env=env,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise RuntimeError("could not verify the local Docker context") from exc
    host = result.stdout.strip() if result.returncode == 0 else ""
    if not host.startswith(_LOCAL_DOCKER_HOST):
        raise RuntimeError("engineering execution requires Docker's local default context; remote daemons are refused")


def _validate_image_environment(
    docker: str,
    image: str,
    env: dict[str, str],
) -> None:
    """Refuse a pinned image whose baked environment could expose credentials."""
    try:
        inspected = subprocess.run(
            [docker, "--context", "default", "image", "inspect", "--format",
             "{{json .Config.Env}}", image],
            capture_output=True, text=True, timeout=10, check=False, env=env,
        )
    except (OSError, subprocess.TimeoutExpired):
        raise RuntimeError("could not inspect pinned engineering image environment") from None
    if inspected.returncode:
        raise RuntimeError("pinned engineering image is not present locally; automatic pulls are disabled")
    try:
        raw = inspected.stdout.encode("utf-8", errors="strict")
        if len(raw) > MAX_IMAGE_ENV_BYTES:
            raise ValueError
        values = json.loads(raw.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError, TypeError, ValueError):
        raise RuntimeError("pinned engineering image environment is invalid or oversized") from None
    if values is None:
        values = []
    if (not isinstance(values, list) or len(values) > 4096 or
            any(not isinstance(value, str) or len(value) > 8192 or "=" not in value
                for value in values)):
        raise RuntimeError("pinned engineering image environment is invalid or oversized")
    for entry in values:
        key, value = entry.split("=", 1)
        if (_SENSITIVE_ENV_NAME.search(key) or _contains_credential(value) or
                _URL_CREDENTIALS.search(value)):
            raise RuntimeError("pinned engineering image contains credential-like environment configuration")


def _bounded_reader(stream, chunks: list[bytes], state: dict[str, bool]) -> None:
    total = 0
    while True:
        block = stream.read(8192)
        if not block:
            return
        remaining = MAX_OUTPUT_BYTES - total
        if remaining > 0:
            kept = block[:remaining]
            chunks.append(kept)
            total += len(kept)
        if len(block) > remaining:
            state["truncated"] = True


def _bounded_digest_reader(stream, state: dict) -> None:
    """Drain stderr without retaining or returning candidate/provider text."""
    digest = hashlib.sha256()
    total = 0
    retained = 0
    while True:
        block = stream.read(8192)
        if not block:
            break
        total += len(block)
        remaining = MAX_OUTPUT_BYTES - retained
        if remaining > 0:
            kept = block[:remaining]
            digest.update(kept)
            retained += len(kept)
    state["bytes"] = total
    state["truncated"] = total > MAX_OUTPUT_BYTES
    state["sha256"] = digest.hexdigest()


def _stop_container(docker: str, name: str, env: dict[str, str]) -> None:
    """Request a bounded graceful stop, then force-remove if Docker is stuck."""
    try:
        stopped = subprocess.run(
            [docker, "--context", "default", "stop", "--time", "1", name],
            capture_output=True, timeout=5, check=False, env=env,
        )
        if stopped.returncode == 0:
            return
    except (OSError, subprocess.TimeoutExpired):
        pass
    try:
        subprocess.run(
            [docker, "--context", "default", "rm", "--force", name],
            capture_output=True, timeout=5, check=False, env=env,
        )
    except (OSError, subprocess.TimeoutExpired):
        pass


def run_sandboxed(
    image: str,
    workspace: str | os.PathLike[str],
    command: str,
    *,
    benchmark_root: str | os.PathLike[str],
    evaluator_root: str | os.PathLike[str],
    timeout_seconds: int = 600,
    writable_subdirectory: str = "output",
) -> ContainerResult:
    """Execute a shell command in one task-only, offline, pinned Docker image."""
    if isinstance(timeout_seconds, bool) or not isinstance(timeout_seconds, int) or not 1 <= timeout_seconds <= MAX_TIMEOUT_SECONDS:
        raise ValueError(f"engineering timeout must be between 1 and {MAX_TIMEOUT_SECONDS} seconds")
    _validate_command(command)
    _validate_writable_subdirectory(writable_subdirectory)
    if not valid_pinned_image(image):
        raise ValueError("engineering execution image must use an immutable sha256 digest")
    task_root = validate_task_workspace(workspace)
    benchmark = validate_task_workspace(benchmark_root)
    evaluator = validate_task_workspace(evaluator_root)
    if any(task_root.is_relative_to(root) or root.is_relative_to(task_root)
           for root in (benchmark, evaluator)):
        raise ValueError("engineering task workspace must be separate from benchmark and evaluator roots")
    docker = shutil.which("docker")
    if not docker:
        raise RuntimeError("Docker is required for engineering code execution; host execution is refused")
    env = _docker_environment()
    validate_local_docker(docker)
    _validate_image_environment(docker, image, env)

    name = f"threadforge-eng-{uuid.uuid4().hex[:24]}"
    argv = docker_run_argv(
        docker, image, task_root, command,
        benchmark_root=benchmark, evaluator_root=evaluator, container_name=name,
        writable_subdirectory=writable_subdirectory,
    )
    try:
        process = subprocess.Popen(
            argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT, env=env, shell=False,
        )
    except OSError as exc:
        raise RuntimeError("could not start the isolated engineering container") from exc

    chunks: list[bytes] = []
    state = {"truncated": False}
    reader = threading.Thread(target=_bounded_reader, args=(process.stdout, chunks, state), daemon=True)
    reader.start()
    timed_out = False
    try:
        returncode = process.wait(timeout=timeout_seconds)
    except subprocess.TimeoutExpired:
        timed_out = True
        try:
            stopped = subprocess.run(
                [docker, "--context", "default", "stop", "--time", "1", name],
                capture_output=True, timeout=5, check=False, env=env,
            )
            if stopped.returncode:
                subprocess.run(
                    [docker, "--context", "default", "rm", "--force", name],
                    capture_output=True, timeout=5, check=False, env=env,
                )
        except (OSError, subprocess.TimeoutExpired):
            pass
        try:
            returncode = process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            returncode = process.wait()
    reader.join(timeout=5)
    if reader.is_alive():
        process.kill()
        process.wait()
        reader.join(timeout=5)
    output = b"".join(chunks).decode("utf-8", errors="replace")
    if state["truncated"]:
        output += "\n[output truncated by Threadforge]"
    return ContainerResult(returncode=returncode, output=output,
                           timed_out=timed_out, output_truncated=state["truncated"])


def run_sandboxed_stdio(
    image: str,
    workspace: str | os.PathLike[str],
    command: str,
    *,
    benchmark_root: str | os.PathLike[str],
    evaluator_root: str | os.PathLike[str],
    timeout_seconds: int = 10_800,
    writable_subdirectory: str = "output",
    remote_inference_opt_in: bool = False,
    environ: dict[str, str] | None = None,
    completion=None,
) -> BrokeredContainerResult:
    """Run an offline, task-scoped container with a host-only model broker.

    The trusted container launcher must reserve stdout exclusively for the
    versioned broker protocol and redirect agent logs to stderr. No Docker
    socket, provider configuration, or candidate-selected host command is
    exposed to the container. Missing Docker or model policy never falls back
    to executing candidate code on the host.
    """
    if (isinstance(timeout_seconds, bool) or not isinstance(timeout_seconds, int) or
            not 1 <= timeout_seconds <= MAX_INTERACTIVE_TIMEOUT_SECONDS):
        raise ValueError(
            "brokered engineering timeout must be between 1 and "
            f"{MAX_INTERACTIVE_TIMEOUT_SECONDS} seconds"
        )
    _validate_command(command)
    _validate_writable_subdirectory(writable_subdirectory)
    if not valid_pinned_image(image):
        raise ValueError("engineering execution image must use an immutable sha256 digest")
    task_root = validate_task_workspace(workspace)
    benchmark = validate_task_workspace(benchmark_root)
    evaluator = validate_task_workspace(evaluator_root)
    if any(task_root.is_relative_to(root) or root.is_relative_to(task_root)
           for root in (benchmark, evaluator)):
        raise ValueError("engineering task workspace must be separate from benchmark and evaluator roots")

    # Resolve local/remote policy before Docker starts, so opt-in and config
    # failures have no execution side effects.
    broker = ModelBroker(
        remote_inference_opt_in=remote_inference_opt_in,
        environ=environ,
        completion=completion,
        request_timeout_seconds=min(timeout_seconds, MAX_MODEL_REQUEST_TIMEOUT_SECONDS),
    )
    docker = shutil.which("docker")
    if not docker:
        raise RuntimeError("Docker is required for brokered engineering execution; host execution is refused")
    env = _docker_environment()
    validate_local_docker(docker)
    _validate_image_environment(docker, image, env)

    name = f"threadforge-eng-{uuid.uuid4().hex[:24]}"
    argv = docker_run_argv(
        docker, image, task_root, command,
        benchmark_root=benchmark, evaluator_root=evaluator, container_name=name,
        writable_subdirectory=writable_subdirectory,
    )
    # Attach stdin only: a TTY would transform/buffer the line-framed protocol.
    argv.insert(argv.index("run") + 1, "--interactive")
    try:
        process = subprocess.Popen(
            argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, env=env, shell=False,
        )
    except OSError as exc:
        raise RuntimeError("could not start the isolated brokered engineering container") from exc

    deadline = time.monotonic() + timeout_seconds
    worker_state = {
        "requests_handled": 0,
        "protocol_error": False,
        "broker_error": False,
    }
    stderr_state: dict = {}
    worker_done = threading.Event()

    def dispatch(request):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise BrokerOperationError("model_unavailable")
        return broker.dispatch(request, timeout_seconds=remaining)

    def serve_broker() -> None:
        try:
            worker_state["requests_handled"] = serve_stdio(
                process.stdout, process.stdin, dispatch,
            )
        except BridgeProtocolError:
            worker_state["protocol_error"] = True
        except Exception:
            # Do not preserve or surface exception text; it may contain prompts
            # or provider diagnostics.
            worker_state["broker_error"] = True
        finally:
            worker_done.set()
            try:
                process.stdin.close()
            except OSError:
                pass

    stderr_reader = threading.Thread(
        target=_bounded_digest_reader,
        args=(process.stderr, stderr_state),
        name="threadforge-engineering-stderr",
        daemon=True,
    )
    broker_thread = threading.Thread(
        target=serve_broker,
        name="threadforge-engineering-broker",
        daemon=True,
    )
    stderr_reader.start()
    broker_thread.start()

    timed_out = False
    returncode: int | None = None
    abort = False
    while returncode is None:
        if (worker_done.is_set() and
                (worker_state["protocol_error"] or worker_state["broker_error"]) and
                process.poll() is None):
            abort = True
            break
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            timed_out = True
            abort = True
            break
        try:
            returncode = process.wait(timeout=min(0.1, remaining))
        except subprocess.TimeoutExpired:
            continue

    if abort:
        _stop_container(docker, name, env)
        try:
            returncode = process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            returncode = process.wait()

    # A live model request receives no more than the remaining run budget.
    # Give its normal provider timeout a brief scheduling margin, then return
    # sanitized status rather than allowing a stuck child to block forever.
    broker_thread.join(timeout=min(
        MAX_MODEL_REQUEST_TIMEOUT_SECONDS,
        max(0.0, deadline - time.monotonic()),
    ) + 1.0)
    if broker_thread.is_alive():
        worker_state["broker_error"] = True
    stderr_reader.join(timeout=5)
    if stderr_reader.is_alive():
        process.kill()
        process.wait()
        stderr_reader.join(timeout=5)

    return BrokeredContainerResult(
        returncode=returncode if returncode is not None else -1,
        timed_out=timed_out,
        protocol_error=worker_state["protocol_error"],
        broker_error=worker_state["broker_error"],
        requests_handled=worker_state["requests_handled"],
        model_calls=broker.calls,
        failed_model_calls=broker.failed_calls,
        prompt_tokens=broker.prompt_tokens,
        completion_tokens=broker.completion_tokens,
        unmeasured_calls=broker.unmeasured_calls,
        stderr_sha256=stderr_state.get("sha256", ""),
        stderr_bytes=stderr_state.get("bytes", 0),
        stderr_truncated=stderr_state.get("truncated", False),
    )
