# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Task-local MCP server for use inside Threadforge's offline Docker runtime.

File tools reuse the host gateway's path policy. PDF parsing and code execution
run as bounded child processes inside the already network-disabled task
container; this module refuses to start outside a Docker container.
"""

from __future__ import annotations

import os
import signal
import subprocess
import threading
from pathlib import Path

from threadforge_domains.engineering import gateway as host_gateway
from threadforge_domains.engineering.security import (
    ContainerResult,
    _bounded_reader,
)

FastMCP = host_gateway.FastMCP
GATEWAY_HOST = host_gateway.GATEWAY_HOST
MAX_CODE_CHARS = host_gateway.MAX_CODE_CHARS
MAX_READ_CHARS = host_gateway.MAX_READ_CHARS

if FastMCP is None:
    mcp = None
else:
    mcp = FastMCP(
        "threadforge_engineering_mcp",
        instructions=(
            "Task-local Engineering tools. Reads are confined to /task; writes "
            "are restricted to /task/output. Shell and PDF tools run inside the "
            "network-disabled, resource-limited task container."
        ),
    )


def _inside_docker() -> bool:
    return Path("/.dockerenv").is_file()


def _register(name: str, *, read_only: bool, destructive: bool = False):
    def decorate(function):
        if mcp is not None:
            mcp.tool(name=name, annotations={
                "title": name.replace("_", " ").title(),
                "readOnlyHint": read_only,
                "destructiveHint": destructive,
                "idempotentHint": read_only,
                "openWorldHint": False,
            })(function)
        return function
    return decorate


def _tool_error(reason: str) -> str:
    return f"Error: {reason}"


def _exec_environment() -> dict[str, str]:
    allowed = {"PATH", "LANG", "LC_ALL", "LC_CTYPE", "HOME", "TMP", "TEMP", "TMPDIR"}
    source = os.environ
    env = {key: value for key, value in source.items() if key in allowed}
    env["HOME"] = "/tmp"
    env["TMPDIR"] = "/tmp"
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    return env


def _kill_process_group(process, *, force: bool) -> None:
    try:
        if os.name == "posix":
            os.killpg(process.pid, signal.SIGKILL if force else signal.SIGTERM)
        elif force:
            process.kill()
        else:
            process.terminate()
    except OSError:
        pass


def _run_local_command(
    argv: list[str],
    *,
    cwd: Path,
    timeout_seconds: int,
) -> ContainerResult:
    chunks: list[bytes] = []
    state = {"truncated": False}
    try:
        process = subprocess.Popen(
            argv,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            cwd=cwd,
            env=_exec_environment(),
            shell=False,
            start_new_session=(os.name == "posix"),
            close_fds=True,
        )
    except OSError:
        return ContainerResult(returncode=127, output="isolated command could not start")

    reader = threading.Thread(
        target=_bounded_reader,
        args=(process.stdout, chunks, state),
        name="threadforge-container-tool-output",
        daemon=True,
    )
    reader.start()
    timed_out = False
    try:
        returncode = process.wait(timeout=timeout_seconds)
    except subprocess.TimeoutExpired:
        timed_out = True
        _kill_process_group(process, force=False)
        try:
            returncode = process.wait(timeout=1)
        except subprocess.TimeoutExpired:
            _kill_process_group(process, force=True)
            returncode = process.wait()
    finally:
        # Kill background descendants even when the shell itself exited.
        _kill_process_group(process, force=True)
    reader.join(timeout=3)
    if reader.is_alive():
        _kill_process_group(process, force=True)
        try:
            process.stdout.close()
        except OSError:
            pass
        reader.join(timeout=2)
    output = b"".join(chunks).decode("utf-8", errors="replace")
    if state["truncated"]:
        output += "\n[output truncated by Threadforge]"
    return ContainerResult(returncode=returncode, output=output,
                           timed_out=timed_out, output_truncated=state["truncated"])


@_register("read_text_file", read_only=True)
def read_text_file(path: str, offset: int = 0, limit: int = MAX_READ_CHARS) -> str:
    """Read a bounded UTF-8 text file from the active task workspace."""
    return host_gateway.read_text_file(path, offset, limit)


@_register("list_files", read_only=True)
def list_files(path: str = ".", limit: int = 40, offset: int = 0) -> str:
    """List task-workspace directory entries with bounded pagination."""
    return host_gateway.list_files(path, limit, offset)


@_register("get_directory_tree", read_only=True)
def get_directory_tree(path: str = ".", max_entries: int = 500) -> str:
    """Return a bounded directory tree relative to the task workspace."""
    return host_gateway.get_directory_tree(path, max_entries)


@_register("search_files", read_only=True)
def search_files(path: str, name_contains: str, limit: int = 50, offset: int = 0) -> str:
    """Search task-workspace filenames with bounded paging."""
    return host_gateway.search_files(path, name_contains, limit, offset)


@_register("write_file", read_only=False, destructive=True)
def write_file(path: str, content: str) -> str:
    """Write UTF-8 text under the task's output directory only."""
    return host_gateway.write_file(path, content)


@_register("read_pdf", read_only=True)
def read_pdf(path: str, max_pages: int = 40) -> str:
    """Extract bounded PDF text from a task-local PDF inside this container."""
    if not _inside_docker():
        return _tool_error("container-only PDF parsing is unavailable")
    if isinstance(max_pages, bool) or not isinstance(max_pages, int) or not 1 <= max_pages <= 100:
        return _tool_error("max_pages must be an integer from 1 to 100")
    target = host_gateway._path(path)
    root = host_gateway._workspace()
    if (target is None or root is None or host_gateway._entry_kind(target) != "file" or
            target.suffix.casefold() != ".pdf"):
        return _tool_error("path must name a PDF file inside the active task workspace")
    relative = target.relative_to(root).as_posix()
    script = (
        "import fitz,sys; d=fitz.open(sys.argv[1]); "
        "print('\\n'.join(f'--- page {i+1} ---\\n'+d[i].get_text() "
        "for i in range(min(len(d),int(sys.argv[2])))))"
    )
    result = _run_local_command(
        ["python3", "-c", script, "/task/" + relative, str(max_pages)],
        cwd=root,
        timeout_seconds=120,
    )
    if result.timed_out:
        return _tool_error("PDF extraction timed out after 120 seconds")
    if result.returncode != 0:
        return _tool_error("PDF extraction failed inside the task container")
    return result.output[:MAX_READ_CHARS]


@_register("code_exec", read_only=False, destructive=True)
def code_exec(code: str, workdir: str | None = None, timeout_seconds: int = 600) -> dict:
    """Run bounded shell code inside the current offline task container."""
    if not _inside_docker():
        return {"success": False, "output": "Error: container-only code execution is unavailable",
                "returncode": None, "timed_out": False}
    if not isinstance(code, str) or not code.strip() or len(code) > MAX_CODE_CHARS:
        return {"success": False, "output": "Error: code must contain 1..65536 characters",
                "returncode": None, "timed_out": False}
    if (isinstance(timeout_seconds, bool) or not isinstance(timeout_seconds, int) or
            not 1 <= timeout_seconds <= 600):
        return {"success": False, "output": "Error: timeout_seconds must be between 1 and 600",
                "returncode": None, "timed_out": False}
    root = host_gateway._workspace()
    cwd = host_gateway._path(workdir) if workdir else root
    if root is None or cwd is None or host_gateway._entry_kind(cwd) != "directory":
        return {"success": False,
                "output": "Error: workdir must be a directory in the active task workspace",
                "returncode": None, "timed_out": False}
    result = _run_local_command(["/bin/sh", "-lc", code], cwd=cwd,
                                timeout_seconds=timeout_seconds)
    return {"success": result.returncode == 0 and not result.timed_out,
            "output": result.output, "returncode": result.returncode,
            "timed_out": result.timed_out}


class LoopbackOriginGuard(host_gateway.LoopbackOriginGuard):
    """Container gateway uses the same strict loopback HTTP origin policy."""


def main() -> None:
    if not _inside_docker():
        raise RuntimeError("Engineering container gateway may only start inside Docker")
    if mcp is None:
        raise RuntimeError("install Threadforge's optional [workspace] dependencies for MCP tools")
    port = host_gateway._gateway_port()
    app = mcp.http_app(path="/mcp/", transport="streamable-http")
    try:
        import uvicorn
    except ImportError:
        raise RuntimeError("install uvicorn with the optional [workspace] dependencies") from None
    uvicorn.run(LoopbackOriginGuard(app, port), host=GATEWAY_HOST, port=port,
                log_level="warning", access_log=False)


if __name__ == "__main__":
    main()
