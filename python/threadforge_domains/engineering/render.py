# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Bounded renderer for Engineering trial records."""

from __future__ import annotations

import json

MAX_TRACE_CHARS = 300_000
MAX_DETAIL_CHARS = 800_000


def render_full(record: dict, detail: bool = False) -> str:
    """Render a local trial record without trusting or executing its contents."""
    limit = MAX_DETAIL_CHARS if detail else MAX_TRACE_CHARS
    try:
        text = json.dumps(record, ensure_ascii=False, sort_keys=True, indent=1)
    except (TypeError, ValueError, RecursionError):
        text = "[trace unavailable: record is not safely renderable]"
    if len(text) > limit:
        head, tail = int(limit * 0.6), int(limit * 0.4)
        text = text[:head] + f"\n...[TRUNCATED {len(text) - head - tail} chars]...\n" + text[-tail:]
    return "UNTRUSTED ENGINEERING TRIAL DATA\n" + text
