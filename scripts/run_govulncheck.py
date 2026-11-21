#!/usr/bin/env python3
"""Run govulncheck, retrying only transient vulnerability-database fetch errors."""

from __future__ import annotations

import subprocess
import sys
import time
from collections.abc import Callable, Sequence
from pathlib import Path
from typing import TextIO


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
COMMAND = ["go", "run", "golang.org/x/vuln/cmd/govulncheck@v1.9.0", "./..."]
MAX_ATTEMPTS = 3
RETRY_DELAYS_SECONDS = (5, 10)
FETCH_ERROR_PREFIX = "govulncheck: fetching vulnerabilities:"
RETRYABLE_NETWORK_MARKERS = (
    "lookup vuln.go.dev: i/o timeout",
    "lookup vuln.go.dev: no such host",
    "lookup vuln.go.dev: temporary failure",
    "server misbehaving",
    "connect: network is unreachable",
    "no route to host",
    "connect: connection refused",
    "connection timed out",
    "connection reset by peer",
    "tls handshake timeout",
    "context deadline exceeded",
    "i/o timeout",
)
ALLOWED_WRAPPER_LINES = {"exit status 1"}


def is_retryable_database_fetch_failure(output: str) -> bool:
    """Recognize only isolated network failures fetching vuln.go.dev data."""
    lines = [line.strip().lower() for line in output.splitlines() if line.strip()]
    fetch_lines = [line for line in lines if line.startswith(FETCH_ERROR_PREFIX)]
    if not fetch_lines or any(
        not line.startswith(FETCH_ERROR_PREFIX) and line not in ALLOWED_WRAPPER_LINES
        for line in lines
    ):
        return False

    fetch_output = "\n".join(fetch_lines)
    return "https://vuln.go.dev/" in fetch_output and any(
        marker in fetch_output for marker in RETRYABLE_NETWORK_MARKERS
    )


def _write_result(result: subprocess.CompletedProcess[str], stdout: TextIO, stderr: TextIO) -> None:
    if result.stdout:
        stdout.write(result.stdout)
        if not result.stdout.endswith("\n"):
            stdout.write("\n")
    if result.stderr:
        stderr.write(result.stderr)
        if not result.stderr.endswith("\n"):
            stderr.write("\n")


def run_govulncheck(
    *,
    run: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
    sleep: Callable[[float], None] = time.sleep,
    stdout: TextIO = sys.stdout,
    stderr: TextIO = sys.stderr,
    command: Sequence[str] = COMMAND,
) -> int:
    """Run the pinned scan; retry only a recognized DB-fetch network failure."""
    for attempt in range(1, MAX_ATTEMPTS + 1):
        result = run(
            list(command),
            cwd=REPOSITORY_ROOT,
            capture_output=True,
            check=False,
            text=True,
            encoding="utf-8",
            errors="replace",
        )
        _write_result(result, stdout, stderr)
        if result.returncode == 0:
            return 0

        output = f"{result.stdout}\n{result.stderr}"
        if not is_retryable_database_fetch_failure(output) or attempt == MAX_ATTEMPTS:
            return result.returncode

        delay = RETRY_DELAYS_SECONDS[attempt - 1]
        stderr.write(
            f"Transient vuln.go.dev database fetch failure; "
            f"retrying govulncheck (attempt {attempt + 1}/{MAX_ATTEMPTS}) in {delay}s.\n"
        )
        sleep(delay)

    return 1


if __name__ == "__main__":
    raise SystemExit(run_govulncheck())
