# Copyright 2026 The rrsi Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
# Modified by Threadforge: task-bound file access, container-only code execution,
# secret filtering, and disabled web search.

# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""Task-scoped FastMCP gateway for the Harvey LAB workspace agent:

  - filesystem read tools (read_text_file, list_files, get_directory_tree, search_files)
  - write_file
  - read_pdf (pymupdf)
  - code_exec (pinned local container with no network or credentials)

Deliberately excludes the archipelago mail/calendar/chat app tools and the
buggy spreadsheets/documents meta-tools — office formats go through code_exec
(pandas / openpyxl / python-docx / python-pptx), which is more reliable.

Each task receives its own loopback gateway. Direct file operations are confined
to that task's workspace; writes are restricted to its output directory.
"""

import os
import shutil
import subprocess
import sys
import uuid

from fastmcp import FastMCP
from threadforge_domains.workspace.security import (
    active_workspace as _active_workspace,
    safe_exec_env as _safe_exec_env,
    scoped_path as _scoped_path,
    valid_pinned_image,
)

# ---- config (env-driven; see config.py for defaults) -------------------------
GATEWAY_HOST = os.environ.get("GATEWAY_HOST", "127.0.0.1")
GATEWAY_PORT = int(os.environ.get("GATEWAY_PORT", "8990"))
CODE_EXEC_TIMEOUT = int(os.environ.get("CODE_EXEC_TIMEOUT", "300"))

MAX_READ_CHARS = 200_000
MAX_WRITE_CHARS = 2_000_000
CODE_EXEC_IMAGE = os.environ.get("THREADFORGE_CODE_EXEC_IMAGE", "")


def _bounded_text(value: str) -> str:
    if len(value) <= MAX_READ_CHARS:
        return value
    return value[:MAX_READ_CHARS] + f"\n[truncated at {MAX_READ_CHARS} chars]"



mcp = FastMCP(
    name="threadforge-harvey-workspace",
    instructions=(
        "Tools for completing white-collar file tasks: read/write files, run "
        "shell+python code for data processing and producing deliverables "
        "(xlsx/docx/pptx/pdf/csv), and read PDFs within this task workspace."
    ),
)


# ---------------------------------------------------------------------------- #
# Filesystem tools (absolute paths)
# ---------------------------------------------------------------------------- #
@mcp.tool
def read_text_file(path: str) -> str:
    """Read a UTF-8 text file and return its contents. `path` is an absolute
    path. Use for .txt/.csv/.md/.json/.py and other plaintext. For binary
    office/pdf formats use read_pdf or code_exec (pandas/openpyxl/docx)."""
    p = _scoped_path(path)
    if p is None:
        return "Error: path is outside the active task workspace"
    if not p.is_file():
        return "Error: not a file in the active task workspace"
    data = p.read_text(encoding="utf-8", errors="replace")
    if len(data) > MAX_READ_CHARS:
        return data[:MAX_READ_CHARS] + f"\n[truncated at {MAX_READ_CHARS} chars]"
    return data


@mcp.tool
def list_files(path: str = ".") -> str:
    """List files and folders (one per line, dirs suffixed with '/') in the
    given absolute directory path."""
    p = _scoped_path(path)
    if p is None:
        return "Error: path is outside the active task workspace"
    if not p.is_dir():
        return "Error: not a directory in the active task workspace"
    out = []
    for entry in sorted(p.iterdir()):
        out.append(entry.name + ("/" if entry.is_dir() else ""))
        if len(out) >= 2_000:
            out.append("[truncated at 2000 entries]")
            break
    return _bounded_text("\n".join(out)) if out else "(empty)"


@mcp.tool
def get_directory_tree(path: str = ".", max_entries: int = 500) -> str:
    """Return a recursive directory tree (relative paths) rooted at the given
    absolute path, capped at max_entries lines."""
    if isinstance(max_entries, bool) or not isinstance(max_entries, int):
        return "Error: max_entries must be an integer"
    max_entries = max(1, min(max_entries, 2_000))
    root = _scoped_path(path)
    if root is None:
        return "Error: path is outside the active task workspace"
    if not root.is_dir():
        return "Error: not a directory in the active task workspace"
    lines = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames.sort()
        rel = os.path.relpath(dirpath, root)
        prefix = "" if rel == "." else rel + "/"
        for fn in sorted(filenames):
            lines.append(prefix + fn)
            if len(lines) >= max_entries:
                lines.append(f"[truncated at {max_entries} entries]")
                return _bounded_text("\n".join(lines))
    return _bounded_text("\n".join(lines)) if lines else "(empty)"


@mcp.tool
def search_files(path: str, name_contains: str) -> str:
    """Recursively find files whose name contains the substring `name_contains`
    under the given absolute directory. Returns absolute paths, one per line."""
    if not isinstance(name_contains, str) or not name_contains or len(name_contains) > 256:
        return "Error: name_contains must be between 1 and 256 characters"
    root = _scoped_path(path)
    if root is None:
        return "Error: path is outside the active task workspace"
    if not root.is_dir():
        return "Error: not a directory in the active task workspace"
    hits = []
    for dirpath, _dirnames, filenames in os.walk(root):
        for fn in filenames:
            if name_contains.lower() in fn.lower():
                hits.append(os.path.join(dirpath, fn))
                if len(hits) >= 2_000:
                    return _bounded_text("\n".join(sorted(hits)) + "\n[truncated at 2000 matches]")
    return _bounded_text("\n".join(sorted(hits))) if hits else "(no matches)"


@mcp.tool
def write_file(path: str, content: str) -> str:
    """Write `content` (UTF-8 text) to the absolute `path`, creating parent
    directories as needed. Overwrites. For binary deliverables (xlsx/docx/pptx/
    pdf/images) generate them with code_exec instead."""
    if not isinstance(content, str) or len(content) > MAX_WRITE_CHARS:
        return f"Error: content exceeds the {MAX_WRITE_CHARS}-character limit"
    p = _scoped_path(path, write=True)
    if p is None:
        return "Error: writes are restricted to the active task output directory"
    p.parent.mkdir(parents=True, exist_ok=True)
    p = _scoped_path(str(p), write=True)
    if p is None:
        return "Error: output path changed during validation"
    p.write_text(content, encoding="utf-8")
    return f"Wrote {len(content)} chars to {path}"


@mcp.tool
def read_pdf(path: str, max_pages: int = 100) -> str:
    """Extract text from a PDF file at the absolute `path` (up to max_pages).
    Returns page-delimited text. For scanned/image PDFs text may be empty; fall
    back to code_exec with pdfplumber/pymupdf if needed."""
    import fitz  # pymupdf

    p = _scoped_path(path)
    if p is None:
        return "Error: path is outside the active task workspace"
    if not p.is_file():
        return "Error: not a file in the active task workspace"
    if isinstance(max_pages, bool) or not isinstance(max_pages, int) or max_pages < 1:
        return "Error: max_pages must be a positive integer"
    max_pages = min(max_pages, 500)
    try:
        doc = fitz.open(str(p))
    except Exception as e:  # noqa: BLE001
        return f"Error opening PDF: {e!r}"
    parts = []
    for i, page in enumerate(doc):
        if i >= max_pages:
            parts.append(f"[truncated at {max_pages} pages of {doc.page_count}]")
            break
        parts.append(f"----- page {i + 1} -----\n{page.get_text()}")
    text = "\n".join(parts)
    if len(text) > MAX_READ_CHARS:
        text = text[:MAX_READ_CHARS] + f"\n[truncated at {MAX_READ_CHARS} chars]"
    return text


# ---------------------------------------------------------------------------- #
# Code execution
# ---------------------------------------------------------------------------- #
@mcp.tool
def code_exec(code: str, workdir: str | None = None) -> dict:
    """Run shell+Python code in a pinned, network-disabled container.

    Only this task's documents (read-only) and output directory (writable) are
    mounted. Set THREADFORGE_CODE_EXEC_IMAGE to a locally installed image
    digest; this tool never pulls images or forwards process credentials.
    """
    if not isinstance(code, str) or not code or len(code) > MAX_READ_CHARS:
        return {"success": False, "output": "Error: code is empty or exceeds the input limit"}
    root = _active_workspace()
    if root is None:
        return {"success": False, "output": "Error: active task workspace is unavailable"}
    if os.name != "posix":
        return {"success": False, "output": "Error: container execution requires a POSIX host"}
    if not valid_pinned_image(CODE_EXEC_IMAGE):
        return {"success": False, "output": "Error: configure a locally installed image by immutable sha256 digest"}
    docker = shutil.which("docker")
    if not docker:
        return {"success": False, "output": "Error: Docker is required for isolated code execution"}
    documents = root / "documents"
    output = root / "output"
    if not documents.is_dir() or not output.is_dir() or documents.is_symlink() or output.is_symlink():
        return {"success": False, "output": "Error: task documents/output directories are invalid"}
    requested = _scoped_path(workdir or str(root))
    if requested is None or not requested.is_dir():
        return {"success": False, "output": "Error: workdir must stay inside the active task workspace"}
    relative_workdir = requested.relative_to(root).as_posix()
    container_workdir = "/workspace" + (f"/{relative_workdir}" if relative_workdir != "." else "")
    host_paths = [documents.resolve(), output.resolve()]
    if any("," in str(path) for path in host_paths):
        return {"success": False, "output": "Error: workspace path contains an unsupported mount character"}
    try:
        timeout = int(os.environ.get("CODE_EXEC_TIMEOUT", "300"))
    except ValueError:
        timeout = 300
    timeout = max(1, min(timeout, 900))
    container_name = f"threadforge-ws-{uuid.uuid4().hex}"
    command = [
        docker, "run", "--rm", "--pull=never", "--name", container_name,
        "--network=none", "--read-only",
        "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128",
        "--user", f"{os.getuid()}:{os.getgid()}",
        "--memory=4g", "--cpus=4", "--ulimit", "fsize=524288:524288",
        "--tmpfs=/tmp:rw,nosuid,nodev,size=1g",
        "--mount", f"type=bind,source={host_paths[0]},target=/workspace/documents,readonly",
        "--mount", f"type=bind,source={host_paths[1]},target=/workspace/output",
        "--workdir", container_workdir, CODE_EXEC_IMAGE, "sh", "-lc", code,
    ]
    try:
        proc = subprocess.run(
            command,
            shell=False,
            env=_safe_exec_env(),
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired:
        try:
            subprocess.run(
                [docker, "rm", "--force", container_name], env=_safe_exec_env(),
                capture_output=True, text=True, timeout=10, check=False,
            )
        except (OSError, subprocess.TimeoutExpired):
            pass
        return {"success": False, "output": f"Error: timed out after {timeout}s"}
    except OSError:
        return {"success": False, "output": "Error: isolated container could not be started"}
    out = proc.stdout + (("\n[stderr]\n" + proc.stderr) if proc.stderr else "")
    if len(out) > MAX_READ_CHARS:
        out = out[:MAX_READ_CHARS] + f"\n[output truncated at {MAX_READ_CHARS} chars]"
    return {"success": proc.returncode == 0, "output": out, "returncode": proc.returncode}


if __name__ == "__main__":
    if GATEWAY_HOST != "127.0.0.1" or not 1024 <= GATEWAY_PORT <= 65535:
        raise SystemExit("workspace gateway must bind to loopback on a valid TCP port")
    if _active_workspace() is None:
        raise SystemExit("workspace gateway requires a valid task-scoped workspace")
    print(
        f"[gateway] starting on http://{GATEWAY_HOST}:{GATEWAY_PORT}/mcp/",
        file=sys.stderr,
        flush=True,
    )
    mcp.run(transport="http", host=GATEWAY_HOST, port=GATEWAY_PORT)
