# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Path and process-environment boundaries for the Harvey workspace gateway."""

from __future__ import annotations

import os
import re
from pathlib import Path

_SENSITIVE_ENV = re.compile(
    r"(?:API[_-]?KEY|ACCESS[_-]?KEY|TOKEN|SECRET|PASSWORD|CREDENTIALS?|AUTH(?:ORIZATION)?)", re.I,
)
_PINNED_IMAGE = re.compile(r"(?:[^\s,]+@sha256:[0-9a-f]{64}|sha256:[0-9a-f]{64})\Z")


def active_workspace(environ: dict[str, str] | None = None) -> Path | None:
    source = os.environ if environ is None else environ
    value = source.get("THREADFORGE_ACTIVE_WORKSPACE", "")
    try:
        path = Path(value)
        if not value or not path.is_absolute() or not path.is_dir():
            return None
        return path.resolve(strict=True)
    except (OSError, RuntimeError, ValueError):
        return None


def scoped_path(value: str, *, write: bool = False,
                environ: dict[str, str] | None = None) -> Path | None:
    """Resolve a path inside one task; writes are restricted to ``output``."""
    root = active_workspace(environ)
    if root is None or not isinstance(value, str) or not value:
        return None
    try:
        requested = Path(value)
        candidate = requested if requested.is_absolute() else root / requested
        resolved = candidate.resolve(strict=False)
        if not resolved.is_relative_to(root):
            return None
        if write:
            output = (root / "output").resolve(strict=True)
            if not output.is_dir() or not resolved.is_relative_to(output):
                return None
        return resolved
    except (OSError, RuntimeError, ValueError):
        return None


def safe_exec_env(environ: dict[str, str] | None = None) -> dict[str, str]:
    source = os.environ if environ is None else environ
    return {key: value for key, value in source.items() if not _SENSITIVE_ENV.search(key)}


def valid_pinned_image(image: str) -> bool:
    return isinstance(image, str) and bool(_PINNED_IMAGE.fullmatch(image))
