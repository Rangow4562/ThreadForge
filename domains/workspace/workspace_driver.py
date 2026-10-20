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
# Modified by Threadforge: per-task gateways, local-default model policy,
# partial-result preservation, and bounded process cleanup.

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
"""Harvey LAB runner driving the vendored official react_toolbelt engine.

Harvey LAB task contract:

  - task = tasks/<area>/<workflow>[/<scenario>]/{task.json, documents/}
  - copy documents/ into a scratch workspace; agent writes deliverables to
    workspace output/
  - after the run, copy output/* -> runs/<LABEL>/<task_name>/output/ and the
    trajectory to runs/<LABEL>/<task_name>/trajectory.json
  - skip only complete runs and preserve partial prior artifacts
  - create a task-scoped loopback gateway with no web search
  - route code execution through a pinned no-network container

The harness module is imported from this checkout's third_party/archipelago
tree via RRSI_HARNESS_MODULE (default harness_workspace.main), so running the
driver from a candidate worktree evaluates that candidate's harness.
"""

import argparse
import asyncio
import ipaddress
import json
import os
import re
import socket
import shutil
import sys
import time
import traceback
from urllib.parse import urlparse

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "third_party", "archipelago"))
os.environ.setdefault("RRSI_HARNESS_MODULE", "harness_workspace.main")

from runner.agents.models import AgentRunInput  # noqa: E402
from runner.agents.registry import get_agent_impl  # noqa: E402

HARVEY_LAB_ROOT = os.environ.get("HARVEY_LAB_ROOT", "harvey-labs")
MODEL_LABEL = os.environ.get("MODEL_LABEL", "opus48_base")
LOCAL_MODEL = os.environ.get("THREADFORGE_LOCAL_EVALUATION_MODEL", "threadforge-local")
ORCHESTRATOR_MODEL = os.environ.get("ORCHESTRATOR_MODEL", f"openai/{LOCAL_MODEL}")
MAX_CONCURRENT = int(os.environ.get("MAX_CONCURRENT", "6"))
TIMEOUT_PER_TASK = int(os.environ.get("TIMEOUT_PER_TASK", "3600"))
AGENT_PYTHON = os.environ.get("RRSI_AGENT_PYTHON", sys.executable)
GATEWAY_SCRIPT = os.path.join(HERE, "mcp_gateway", "gateway.py")
MAX_OUTPUT_FILES = 5_000
MAX_OUTPUT_BYTES = 512 * 1024 * 1024
MAX_TASK_JSON_BYTES = 2 * 1024 * 1024
JOBS_DIR = os.environ.get("RRSI_JOBS_DIR", os.path.join(HERE, "runs"))
RUNS = os.path.join(JOBS_DIR, MODEL_LABEL)
WORKSPACES = os.path.join(os.environ.get("RRSI_WORKSPACES", os.path.join(HERE, "workspaces")), MODEL_LABEL.replace("/", "__"))

AGENT_CONFIG_VALUES = {
    "max_steps": int(os.environ.get("MAX_STEPS", "250")),
    "timeout": int(os.environ.get("AGENT_TIMEOUT", "10800")),
    "llm_response_timeout": int(os.environ.get("LLM_RESPONSE_TIMEOUT", "600")),
}

_SENSITIVE_ENV = re.compile(
    r"(?:API[_-]?KEY|ACCESS[_-]?KEY|TOKEN|SECRET|PASSWORD|CREDENTIALS?|AUTH(?:ORIZATION)?)", re.I,
)


def _require_execution_consent() -> None:
    if os.environ.get("THREADFORGE_BENCHMARK_EXECUTION_OPT_IN") != "true":
        raise SystemExit("benchmark execution requires Threadforge benchmark_execution_opt_in=true")
    backend = os.environ.get("THREADFORGE_EVALUATION_BACKEND", "local")
    if backend == "local":
        parsed = urlparse(os.environ.get("OPENAI_BASE_URL", "http://127.0.0.1:8000/v1"))
        try:
            loopback = parsed.hostname == "localhost" or ipaddress.ip_address(parsed.hostname).is_loopback
        except (ValueError, TypeError):
            loopback = False
        if (not loopback or parsed.scheme != "http" or parsed.username or parsed.password or
                parsed.query or parsed.fragment or parsed.path not in {"", "/v1"}):
            raise SystemExit("local workspace policy inference must use a loopback HTTP endpoint")
        if not ORCHESTRATOR_MODEL.startswith("openai/"):
            raise SystemExit("local workspace inference requires an OpenAI-compatible local model")
        os.environ["OPENAI_BASE_URL"] = os.environ.get(
            "OPENAI_BASE_URL", "http://127.0.0.1:8000/v1",
        )
        os.environ["OPENAI_API_BASE"] = os.environ["OPENAI_BASE_URL"]
        os.environ["OPENAI_API_KEY"] = "threadforge-local-no-secret"
    elif backend == "remote":
        if os.environ.get("THREADFORGE_RRSI_REMOTE_INFERENCE_OPT_IN") != "true":
            raise SystemExit("remote workspace policy inference requires remote_inference_opt_in=true")
        if ORCHESTRATOR_MODEL != os.environ.get("THREADFORGE_REMOTE_EVALUATION_MODEL"):
            raise SystemExit("remote workspace model does not match the explicitly configured model")
    else:
        raise SystemExit("THREADFORGE_EVALUATION_BACKEND must be local or remote")


def _free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


async def _start_gateway(workspace: str, name: str):
    port = _free_port()
    env = {key: value for key, value in os.environ.items() if not _SENSITIVE_ENV.search(key)}
    env.update({
        "GATEWAY_HOST": "127.0.0.1",
        "GATEWAY_PORT": str(port),
        "THREADFORGE_ACTIVE_WORKSPACE": os.path.realpath(workspace),
        "ENABLE_WEB_SEARCH": "0",
    })
    log_dir = os.path.join(JOBS_DIR, MODEL_LABEL, "gateway-logs")
    os.makedirs(log_dir, exist_ok=True)
    log_stream = open(os.path.join(log_dir, f"{name}.log"), "ab")
    try:
        proc = await asyncio.create_subprocess_exec(
            AGENT_PYTHON, GATEWAY_SCRIPT, cwd=HERE, env=env,
            stdout=log_stream, stderr=log_stream,
            start_new_session=(os.name == "posix"),
        )
    except Exception:
        log_stream.close()
        raise

    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        if proc.returncode is not None:
            break
        try:
            _reader, writer = await asyncio.wait_for(
                asyncio.open_connection("127.0.0.1", port), timeout=0.5,
            )
            writer.close()
            await writer.wait_closed()
            return proc, f"http://127.0.0.1:{port}/mcp/", log_stream
        except (OSError, asyncio.TimeoutError):
            await asyncio.sleep(0.1)
    await _stop_gateway(proc, log_stream)
    raise RuntimeError("task-scoped MCP gateway did not become ready")


async def _stop_gateway(proc, log_stream) -> None:
    try:
        if proc.returncode is None:
            proc.terminate()
            try:
                await asyncio.wait_for(proc.wait(), timeout=5)
            except asyncio.TimeoutError:
                proc.kill()
                await proc.wait()
    finally:
        log_stream.close()

# Official ReAct system prompt (react_toolbelt README), web_search mention
# removed: LAB is a closed-workspace benchmark (documents provided; no web).
SYSTEM_PROMPT = """You are an AI assistant that completes tasks by reasoning and using tools.

## Think Before Acting

Before making tool calls, briefly explain your reasoning in 1-3 sentences:
- What you learned from the previous step
- What you're doing next and why

Don't over-explain. Be concise but show your thinking.

## Tools

**Always Available (Meta-Tools):**
- `todo_write` - Task planning: create/update todos. Takes `todos` array [{id, content, status}] and `merge` boolean.
- `toolbelt_list_tools` / `toolbelt_inspect_tool` / `toolbelt_add_tool` / `toolbelt_remove_tool` - Tool management
- `final_answer` - Submit your answer (status: completed/blocked/failed)

**Domain Tools:** Use `toolbelt_list_tools` to discover, then `toolbelt_add_tool` to add them. They include file reading/writing, `code_exec` (shell + a rich scientific Python for producing xlsx/docx/pptx/pdf/csv deliverables), and `read_pdf`.

## Workflow

1. Plan: Use `todo_write` to create todos for complex tasks
2. Discover: Use `toolbelt_list_tools` to find relevant tools
3. Execute: Work through todos, use `todo_write` with `merge=true` to update status
4. Complete: Call `final_answer` (all todos must be completed/cancelled first)

## Rules

- Update todo status with `todo_write`: set `in_progress` when starting, `completed` when done
- Show your work for calculations; use exact values without rounding unless told otherwise
- All file paths are absolute
- `final_answer` is rejected if todos are incomplete"""

WRAPPER_TEMPLATE = """=== TASK ===
{title}

=== INSTRUCTIONS ===
{instructions}

=== DOCUMENTS FOLDER (read-only source materials) ===
{documents_dir}

=== OUTPUT DIRECTORY ===
{output_dir}

=== EXPECTED DELIVERABLES (write with these EXACT filenames) ===
{deliverables}

IMPORTANT:
- All source materials are files under {documents_dir}. There is no web access;
  everything you need is in the documents folder.
- Save ONLY the final deliverables to {output_dir}, using the exact filenames
  listed above. Do not save intermediate or temporary files there.
- You MUST only access files within {workspace}. Do NOT access any files or
  directories outside of this path.
- If a file cannot be read directly (e.g., .xlsx, .docx, .pptx), use code to
  extract and process its contents.
- If you encounter ambiguous or conflicting information, analyze the conflict,
  explain your reasoning, and justify the approach you choose."""


def find_tasks(subset_file: str | None) -> list[dict]:
    """Enumerate LAB tasks (both nesting depths). task name = path with '/'->'__'."""
    tasks = []
    troot = os.path.join(HARVEY_LAB_ROOT, "tasks")
    for dirpath, dirnames, filenames in os.walk(troot):
        if "task.json" in filenames and os.path.isdir(os.path.join(dirpath, "documents")):
            rel = os.path.relpath(dirpath, troot)
            tasks.append({"id": rel, "name": rel.replace("/", "__"), "dir": dirpath})
    tasks.sort(key=lambda t: t["id"])
    if subset_file:
        keep = set(json.load(open(subset_file)))
        tasks = [t for t in tasks if t["id"] in keep]
    return tasks


def is_done(name: str) -> bool:
    run_dir = os.path.join(RUNS, name)
    out = os.path.join(run_dir, "output")
    if not os.path.isfile(os.path.join(run_dir, "trajectory.json")) or not os.path.isdir(out):
        return False
    count, _size, safe = _output_tree_metrics(out)
    return count > 0 and safe


def _output_tree_metrics(root: str) -> tuple[int, int, bool]:
    if os.path.islink(root) or not os.path.isdir(root):
        return 0, 0, False
    file_count = total_bytes = 0
    for current, directories, files in os.walk(root, followlinks=False):
        if any(os.path.islink(os.path.join(current, name)) for name in directories + files):
            return file_count, total_bytes, False
        for name in files:
            path = os.path.join(current, name)
            try:
                info = os.stat(path, follow_symlinks=False)
            except OSError:
                return file_count, total_bytes, False
            if not os.path.isfile(path):
                return file_count, total_bytes, False
            file_count += 1
            total_bytes += info.st_size
            if file_count > MAX_OUTPUT_FILES or total_bytes > MAX_OUTPUT_BYTES:
                return file_count, total_bytes, False
    return file_count, total_bytes, True


def _read_task_json(task: dict) -> dict:
    task_path = os.path.join(task["dir"], "task.json")
    try:
        info = os.lstat(task_path)
        if (os.path.islink(task_path) or not os.path.isfile(task_path) or
                info.st_size > MAX_TASK_JSON_BYTES):
            return {}
        with open(task_path, encoding="utf-8") as stream:
            value = json.load(stream)
        return value if isinstance(value, dict) else {}
    except (OSError, json.JSONDecodeError, RecursionError):
        return {}


def _regular_tree(root: str) -> bool:
    if os.path.islink(root) or not os.path.isdir(root):
        return False
    for current, directories, files in os.walk(root, followlinks=False):
        if any(os.path.islink(os.path.join(current, name)) for name in directories + files):
            return False
        if any(not os.path.isdir(os.path.join(current, name)) for name in directories):
            return False
        if any(not os.path.isfile(os.path.join(current, name)) for name in files):
            return False
    return True


async def run_one(task: dict, sem: asyncio.Semaphore) -> dict:
    name = task["name"]
    async with sem:
        if is_done(name):
            print(f"[skip] {name}", flush=True)
            return {"name": name, "status": "skipped"}

        run_dir = os.path.join(RUNS, name)
        workspace = os.path.join(WORKSPACES, name)
        if os.path.lexists(run_dir) or os.path.lexists(workspace):
            print(f"[preserve] {name}: incomplete prior artifacts exist", flush=True)
            return {"name": name, "status": "incomplete_preserved"}

        tj = _read_task_json(task)
        source_documents = os.path.join(task["dir"], "documents")
        if not tj or not _regular_tree(source_documents):
            print(f"[reject] {name}: malformed task metadata or linked documents", flush=True)
            return {"name": name, "status": "invalid_task_source"}
        ws_docs = os.path.join(workspace, "documents")
        ws_output = os.path.join(workspace, "output")
        os.makedirs(workspace, exist_ok=True)
        shutil.copytree(source_documents, ws_docs)
        os.makedirs(ws_output, exist_ok=True)

        deliverables = sorted((tj.get("deliverables") or {}).keys()) or ["response.md"]
        user_msg = WRAPPER_TEMPLATE.format(
            title=tj.get("title", task["id"]),
            instructions=tj.get("instructions", ""),
            documents_dir=ws_docs,
            output_dir=ws_output,
            workspace=workspace,
            deliverables="\n".join(f"- {d}" for d in deliverables),
        )
        status = "error"
        os.makedirs(run_dir, exist_ok=True)
        gateway_proc = gateway_log = None
        try:
            gateway_proc, gateway_url, gateway_log = await _start_gateway(workspace, name)
            run_input = AgentRunInput(
                trajectory_id=f"workspace-{name}"[:120],
                initial_messages=[
                    {"role": "system", "content": SYSTEM_PROMPT},
                    {"role": "user", "content": user_msg},
                ],
                mcp_gateway_url=gateway_url,
                mcp_gateway_auth_token=None,
                orchestrator_model=ORCHESTRATOR_MODEL,
                orchestrator_extra_args={},
                agent_config_values=dict(AGENT_CONFIG_VALUES),
            )
            impl = get_agent_impl("react_toolbelt_agent")
            output = await asyncio.wait_for(impl(run_input), timeout=TIMEOUT_PER_TASK)
            status = str(output.status)
            with open(os.path.join(run_dir, "trajectory.json"), "x", encoding="utf-8") as f:
                json.dump(output.model_dump(mode="json"), f, indent=2, default=str)
        except asyncio.TimeoutError:
            status = "timeout"
            print(f"[timeout] {name}", flush=True)
        except Exception as e:  # noqa: BLE001
            print(f"[error] {name}: {e!r}", flush=True)
            traceback.print_exc()
        finally:
            if gateway_proc is not None:
                await _stop_gateway(gateway_proc, gateway_log)

        n_files = 0
        output_count, _output_bytes, output_safe = _output_tree_metrics(ws_output)
        if os.path.lexists(ws_output) and not output_safe:
            print(f"[reject] {name}: output contains links or exceeds local bounds", flush=True)
            return {"name": name, "status": "unsafe_output_rejected"}
        if output_count:
            final_out = os.path.join(run_dir, "output")
            os.makedirs(final_out, exist_ok=True)
            for item in os.listdir(ws_output):
                src = os.path.join(ws_output, item)
                dst = os.path.join(final_out, item)
                if os.path.isdir(src):
                    shutil.copytree(src, dst)
                else:
                    if not os.path.isfile(src):
                        continue
                    shutil.copy2(src, dst)
                n_files += 1

        print(f"[done] {name} status={status} files={n_files}", flush=True)
        return {"name": name, "status": status, "n_output_files": n_files}


async def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--subset", default=None, help="JSON file: list of task ids")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--only", default="")
    args = ap.parse_args()

    _require_execution_consent()

    os.makedirs(WORKSPACES, exist_ok=True)
    os.makedirs(RUNS, exist_ok=True)
    tasks = find_tasks(args.subset)
    if args.only:
        subs = [s for s in args.only.split(",") if s]
        tasks = [t for t in tasks if any(s in t["name"] for s in subs)]
    if args.limit:
        tasks = tasks[: args.limit]

    print(f"[driver] {len(tasks)} tasks | model={ORCHESTRATOR_MODEL} "
          f"label={MODEL_LABEL} | gateway=task-scoped loopback "
          f"| concurrency={MAX_CONCURRENT}", flush=True)
    sem = asyncio.Semaphore(MAX_CONCURRENT)
    results = await asyncio.gather(*(run_one(t, sem) for t in tasks))
    by_status: dict[str, int] = {}
    for r in results:
        by_status[r["status"]] = by_status.get(r["status"], 0) + 1
    print(f"[driver] SUMMARY {json.dumps(by_status)}", flush=True)


if __name__ == "__main__":
    asyncio.run(main())
