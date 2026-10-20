"""Narrow compatibility helpers for supported Transformers 4.x and 5.x APIs."""

from __future__ import annotations

import inspect
import re
from typing import Any, Callable

_MAJOR_VERSION = re.compile(r"^(\d+)(?:\.|$)")


def pretrained_dtype_kwargs(version: str, dtype: Any) -> dict[str, Any]:
    """Select the documented from_pretrained dtype keyword for Transformers 4 or 5+."""
    if not isinstance(version, str) or len(version) > 64:
        raise RuntimeError("Transformers version is unavailable")
    match = _MAJOR_VERSION.match(version)
    if match is None:
        raise RuntimeError("Transformers version is unsupported")
    keyword = "dtype" if int(match.group(1)) >= 5 else "torch_dtype"
    return {keyword: dtype}


def causal_mask_kwargs(
    builder: Callable[..., Any],
    *,
    config: Any,
    input_embeds: Any,
    attention_mask: Any,
    cache_position: Any,
    past_key_values: Any,
    position_ids: Any,
) -> dict[str, Any]:
    """Build the Transformers 4/5 causal-mask call from its declared signature.

    Transformers 5 renamed ``input_embeds`` to ``inputs_embeds`` and removed
    ``cache_position`` from the mask-builder signature. Unknown signatures fail
    explicitly; guessing here would make the depth-truncated inference path unsafe.
    """
    try:
        accepted = inspect.signature(builder).parameters
    except (TypeError, ValueError):
        raise NotImplementedError("causal-mask builder signature is unavailable") from None

    if "inputs_embeds" in accepted:
        embeds_name = "inputs_embeds"
    elif "input_embeds" in accepted:
        embeds_name = "input_embeds"
    else:
        raise NotImplementedError("causal-mask builder has no supported embedding argument")

    values = {
        "config": config,
        embeds_name: input_embeds,
        "attention_mask": attention_mask,
        "past_key_values": past_key_values,
        "position_ids": position_ids,
    }
    missing = set(values) - set(accepted)
    if missing:
        raise NotImplementedError("causal-mask builder signature is unsupported")
    if "cache_position" in accepted:
        values["cache_position"] = cache_position
    return values
