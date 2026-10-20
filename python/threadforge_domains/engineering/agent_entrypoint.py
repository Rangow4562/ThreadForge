# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Trusted, container-only entrypoint for the candidate Engineering harness."""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import re
import stat
import sys
from pathlib import Path
from urllib.parse import urlsplit

MAX_TRAJECTORY_BYTES = 12 << 20
_TRAJECTORY_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
_RESULT_NAME = ".threadforge-agent-result.json"


def _loopback_url(value: str, *, path: str) -> bool:
    try:
        parsed = urlsplit(value)
        return (parsed.scheme == "http" and parsed.hostname == "127.0.0.1" and
                parsed.port is not None and parsed.username is None and parsed.password is None and
                parsed.path == path and not parsed.query and not parsed.fragment)
    except (TypeError, ValueError):
        return False


def _task_file(root: Path, relative: str) -> Path:
    parts = Path(relative).parts
    if (not parts or relative.startswith(("/", "\\")) or
            Path(relative).is_absolute() or Path(relative).drive or
            any(part in {"", ".", ".."} for part in parts)):
        raise ValueError("required task input is unavailable")
    current = root
    for index, part in enumerate(parts):
        current = current / part
        try:
            info = current.lstat()
        except OSError:
            raise ValueError("required task input is unavailable") from None
        if (stat.S_ISLNK(info.st_mode) or
                bool(getattr(info, "st_file_attributes", 0) & 0x400)):
            raise ValueError("linked task input is unavailable")
        if index < len(parts) - 1 and not stat.S_ISDIR(info.st_mode):
            raise ValueError("required task input is unavailable")
        if index == len(parts) - 1 and (
                not stat.S_ISREG(info.st_mode) or getattr(info, "st_nlink", 1) != 1):
            raise ValueError("required task input is unavailable")
    resolved = current.resolve(strict=True)
    if not resolved.is_relative_to(root):
        raise ValueError("required task input is unavailable")
    return resolved


def _write_trajectory(output_dir: Path, output: object) -> None:
    try:
        if not hasattr(output, "model_dump"):
            return
        record = output.model_dump(mode="json")
        raw = json.dumps(record, ensure_ascii=False, allow_nan=False,
                         separators=(",", ":"), default=str).encode("utf-8", errors="strict")
        if len(raw) > MAX_TRAJECTORY_BYTES:
            return
        destination = output_dir / _RESULT_NAME
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(destination, flags, 0o600)
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
    except (OSError, TypeError, ValueError, UnicodeEncodeError, RecursionError):
        # Trajectories are useful local evidence, but never determine the
        # evaluator verdict or whether a candidate payload is accepted.
        return


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--trajectory-id", required=True)
    parser.add_argument("--timeout-seconds", type=int, default=5400)
    parser.add_argument("--max-steps", type=int, default=250)
    parser.add_argument("--llm-timeout-seconds", type=int, default=600)
    return parser.parse_args(argv)


async def _run_agent(args: argparse.Namespace) -> int:
    if not Path("/.dockerenv").is_file():
        return 71
    if (not _TRAJECTORY_ID.fullmatch(args.trajectory_id) or
            not 1 <= args.timeout_seconds <= 10_800 or
            not 1 <= args.max_steps <= 1_000 or
            not 1 <= args.llm_timeout_seconds <= 10_800):
        return 71
    task_root = Path("/task").resolve(strict=True)
    prompt_path = _task_file(task_root, "LLM_prompt.txt")
    schema_path = _task_file(task_root, "output_structure.py")
    candidate_path = _task_file(task_root, "output/payload.py")
    harness_root = task_root / ".threadforge-agent"
    if not harness_root.is_dir() or not (harness_root / "harness_eng" / "main.py").is_file():
        return 71

    gateway_url = os.environ.get("THREADFORGE_MCP_GATEWAY_URL", "")
    proxy_url = os.environ.get("THREADFORGE_MODEL_PROXY_URL", "")
    if (not _loopback_url(gateway_url, path="/mcp/") or
            not _loopback_url(proxy_url, path="/v1")):
        return 71
    model_api_key = os.environ.get("OPENAI_API_KEY", "")
    if model_api_key != "threadforge-container-no-secret":
        return 71

    try:
        from runner.agents.models import AgentRunInput
        from runner.agents.registry import get_agent_impl

        sys.path.insert(0, str(harness_root))
        from harness_eng.prompts import SYSTEM_PROMPT, TASK_TEMPLATE

        user_message = TASK_TEMPLATE.format(
            workspace=str(task_root),
            prompt_file=str(prompt_path),
            schema_file=str(schema_path),
            candidate_file=str(candidate_path),
        )
        run_input = AgentRunInput(
            trajectory_id=args.trajectory_id,
            initial_messages=[
                {"role": "system", "content": SYSTEM_PROMPT},
                {"role": "user", "content": user_message},
            ],
            mcp_gateway_url=gateway_url,
            mcp_gateway_auth_token=None,
            orchestrator_model="openai/threadforge-local",
            orchestrator_extra_args={
                "api_base": proxy_url,
                "api_key": model_api_key,
            },
            agent_config_values={
                "max_steps": args.max_steps,
                "timeout": args.timeout_seconds,
                "llm_response_timeout": args.llm_timeout_seconds,
                "tool_call_timeout": min(3_600, args.timeout_seconds),
            },
        )
        impl = get_agent_impl("react_toolbelt_agent")
        output = await asyncio.wait_for(impl(run_input), timeout=args.timeout_seconds)
    except asyncio.TimeoutError:
        return 21
    except Exception:
        return 70

    _write_trajectory(task_root / "output", output)
    status = getattr(output, "status", None)
    status = getattr(status, "value", status)
    return 0 if status == "completed" else 20


def main(argv: list[str] | None = None) -> int:
    try:
        args = _parse_args(argv)
    except SystemExit:
        return 71
    return asyncio.run(_run_agent(args))


if __name__ == "__main__":
    raise SystemExit(main())
