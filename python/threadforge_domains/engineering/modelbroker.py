# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Host-side model dispatch for the Engineering sandbox stdio bridge."""

from __future__ import annotations

import asyncio
import inspect
import json
import math
from collections.abc import Callable

from rrsi.modelpolicy import evaluation_runtime
from threadforge_domains.engineering.bridge import (
    MAX_FRAME_BYTES,
    BrokerOperationError,
    BridgeRequest,
    serve_stdio,
)

MAX_CHAT_COMPLETION_TOKENS = 16_384
MAX_CHAT_MESSAGES = 512
MAX_CHAT_TOOLS = 128
MAX_MODEL_REQUEST_TIMEOUT_SECONDS = 600
_CHAT_FIELDS = frozenset({
    "model", "messages", "tools", "tool_choice", "temperature", "top_p",
    "max_tokens", "max_completion_tokens", "stop", "presence_penalty",
    "frequency_penalty", "parallel_tool_calls", "response_format", "seed",
    "n", "stream", "logprobs", "top_logprobs",
})
_RESPONSE_FIELDS = frozenset({
    "id", "choices", "created", "model", "object", "service_tier",
    "system_fingerprint", "usage",
})


def _finite_number(value: object, *, minimum: float, maximum: float) -> bool:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return False
    number = float(value)
    return math.isfinite(number) and minimum <= number <= maximum


def _json_size(value: object) -> int:
    try:
        return len(json.dumps(value, ensure_ascii=False, allow_nan=False,
                              separators=(",", ":")).encode("utf-8", errors="strict"))
    except (TypeError, ValueError, UnicodeEncodeError, RecursionError):
        raise BrokerOperationError("request_rejected") from None


def validate_chat_completion_payload(payload: object) -> dict:
    """Validate bounded OpenAI-compatible chat input before provider dispatch."""
    if not isinstance(payload, dict) or set(payload) - _CHAT_FIELDS:
        raise BrokerOperationError("request_rejected")
    if _json_size(payload) > MAX_FRAME_BYTES - 4096:
        raise BrokerOperationError("request_rejected")
    messages = payload.get("messages")
    if not isinstance(messages, list) or not 1 <= len(messages) <= MAX_CHAT_MESSAGES:
        raise BrokerOperationError("request_rejected")
    if any(not isinstance(message, dict) for message in messages):
        raise BrokerOperationError("request_rejected")
    tools = payload.get("tools")
    if tools is not None and (
        not isinstance(tools, list) or len(tools) > MAX_CHAT_TOOLS or
        any(not isinstance(tool, dict) for tool in tools)
    ):
        raise BrokerOperationError("request_rejected")
    if payload.get("stream", False) is not False:
        raise BrokerOperationError("request_rejected")
    if payload.get("n", 1) != 1 or isinstance(payload.get("n", 1), bool):
        raise BrokerOperationError("request_rejected")
    if "temperature" in payload and not _finite_number(
        payload["temperature"], minimum=0.0, maximum=2.0,
    ):
        raise BrokerOperationError("request_rejected")
    for key in ("presence_penalty", "frequency_penalty"):
        if key in payload and not _finite_number(payload[key], minimum=-2.0, maximum=2.0):
            raise BrokerOperationError("request_rejected")
    if "top_p" in payload and not _finite_number(payload["top_p"], minimum=0.0, maximum=1.0):
        raise BrokerOperationError("request_rejected")
    for key in ("max_tokens", "max_completion_tokens"):
        value = payload.get(key)
        if key in payload and (
            isinstance(value, bool) or not isinstance(value, int) or
            not 1 <= value <= MAX_CHAT_COMPLETION_TOKENS
        ):
            raise BrokerOperationError("request_rejected")
    return {key: value for key, value in payload.items() if key != "model" and key != "stream"}


def _response_dict(value: object) -> dict:
    try:
        if isinstance(value, dict):
            result = value
        elif hasattr(value, "model_dump"):
            result = value.model_dump(mode="json", exclude_none=True)
        elif hasattr(value, "dict"):
            result = value.dict(exclude_none=True)
        else:
            raise BrokerOperationError("provider_unavailable")
        if not isinstance(result, dict):
            raise BrokerOperationError("provider_unavailable")
        normalized = {key: child for key, child in result.items() if key in _RESPONSE_FIELDS}
        if not isinstance(normalized.get("choices"), list):
            raise BrokerOperationError("provider_unavailable")
        if _json_size(normalized) > MAX_FRAME_BYTES - 4096:
            raise BrokerOperationError("provider_unavailable")
        return normalized
    except BrokerOperationError:
        raise
    except Exception:
        raise BrokerOperationError("provider_unavailable") from None


def _usage_value(usage: object, key: str) -> int | None:
    if isinstance(usage, dict):
        value = usage.get(key)
    else:
        value = getattr(usage, key, None)
    if isinstance(value, bool) or not isinstance(value, int) or not 0 <= value <= 10**9:
        return None
    return value


class ModelBroker:
    """Resolve policy on the host and proxy only bounded chat-completion data.

    `remote_inference_opt_in` controls provider selection through the shared
    RRSI policy helper. The candidate-provided model name is never used to pick
    a provider. In local mode only the validated loopback endpoint and dummy
    key cross into LiteLLM; no provider secret is sent to the sandbox.
    """

    def __init__(
        self,
        *,
        remote_inference_opt_in: bool = False,
        environ: dict[str, str] | None = None,
        completion: Callable | None = None,
        request_timeout_seconds: int = MAX_MODEL_REQUEST_TIMEOUT_SECONDS,
    ):
        if (isinstance(request_timeout_seconds, bool) or
                not isinstance(request_timeout_seconds, int) or
                not 1 <= request_timeout_seconds <= MAX_MODEL_REQUEST_TIMEOUT_SECONDS):
            raise ValueError(
                "request_timeout_seconds must be between 1 and "
                f"{MAX_MODEL_REQUEST_TIMEOUT_SECONDS}"
            )
        try:
            self.model, self._environment = evaluation_runtime(
                remote_inference_opt_in, environ=environ,
            )
        except PermissionError as exc:
            raise BrokerOperationError("model_unavailable") from exc
        except (TypeError, ValueError):
            raise BrokerOperationError("model_unavailable") from None
        self._remote = (self._environment.get("THREADFORGE_EVALUATION_BACKEND", "local")
                        == "remote")
        self._completion = completion
        self._request_timeout_seconds = request_timeout_seconds
        self.calls = 0
        self.failed_calls = 0
        self.prompt_tokens = 0
        self.completion_tokens = 0
        self.unmeasured_calls = 0

    def _completion_callable(self) -> Callable:
        if self._completion is not None:
            return self._completion
        try:
            from litellm import acompletion
        except ImportError:
            raise BrokerOperationError("model_unavailable") from None
        self._completion = acompletion
        return acompletion

    def dispatch(
        self,
        request: BridgeRequest,
        *,
        timeout_seconds: float | None = None,
    ) -> dict:
        if request.operation != "model.chat.completions":
            raise BrokerOperationError("request_rejected")
        timeout = self._request_timeout_seconds
        if timeout_seconds is not None:
            if (isinstance(timeout_seconds, bool) or
                    not isinstance(timeout_seconds, (int, float)) or
                    not math.isfinite(timeout_seconds) or timeout_seconds <= 0):
                raise BrokerOperationError("model_unavailable")
            timeout = min(timeout, timeout_seconds)
        payload = validate_chat_completion_payload(request.payload)
        kwargs = {
            **payload,
            "model": self.model,
            "timeout": timeout,
            "num_retries": 0,
        }
        if not self._remote:
            kwargs["api_base"] = self._environment["OPENAI_BASE_URL"]
            kwargs["api_key"] = self._environment["OPENAI_API_KEY"]
        self.calls += 1
        try:
            result = self._completion_callable()(**kwargs)
            if inspect.isawaitable(result):
                result = asyncio.run(result)
            normalized = _response_dict(result)
        except BrokerOperationError:
            self.failed_calls += 1
            self.unmeasured_calls += 1
            raise
        except Exception:
            self.failed_calls += 1
            self.unmeasured_calls += 1
            raise BrokerOperationError("provider_unavailable") from None

        usage = normalized.get("usage")
        prompt = _usage_value(usage, "prompt_tokens")
        completion = _usage_value(usage, "completion_tokens")
        if prompt is None or completion is None:
            self.unmeasured_calls += 1
        else:
            self.prompt_tokens += prompt
            self.completion_tokens += completion
        return normalized


def serve_model_stdio(
    input_stream,
    output_stream,
    *,
    remote_inference_opt_in: bool = False,
    environ: dict[str, str] | None = None,
    completion: Callable | None = None,
    max_requests: int = 512,
) -> tuple[int, ModelBroker]:
    """Run the host model broker on a Docker process's attached stdio pipes."""
    broker = ModelBroker(
        remote_inference_opt_in=remote_inference_opt_in,
        environ=environ,
        completion=completion,
    )
    count = serve_stdio(input_stream, output_stream, broker.dispatch,
                        max_requests=max_requests)
    return count, broker
