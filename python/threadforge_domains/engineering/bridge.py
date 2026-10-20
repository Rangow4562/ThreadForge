# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Bounded stdio RPC transport for an offline Engineering agent container.

The container may send only versioned model-completion requests to its host
broker. Provider credentials and network configuration never cross this
channel. Candidate stdout must be redirected to stderr by the trusted launcher
so stdout remains reserved for these frames.
"""

from __future__ import annotations

import json
import re
import threading
import uuid
from dataclasses import dataclass
from typing import BinaryIO, Callable

SCHEMA_VERSION = "threadforge.engineering-broker.v1"
MODEL_OPERATION = "model.chat.completions"
MAX_FRAME_BYTES = 16 << 20
MAX_REQUESTS = 512
_REQUEST_ID = re.compile(r"[0-9a-f]{32}\Z")
_ERROR_CODE = re.compile(r"[a-z][a-z0-9_]{0,63}\Z")
_ERROR_CODES = frozenset({
    "model_unavailable",
    "provider_unavailable",
    "request_rejected",
    "request_limit",
})


class BridgeProtocolError(ValueError):
    """Malformed, oversized, or out-of-contract broker traffic."""


class BrokerOperationError(RuntimeError):
    """A safe, allowlisted broker failure that contains no provider detail."""

    def __init__(self, code: str):
        if code not in _ERROR_CODES:
            code = "provider_unavailable"
        self.code = code
        super().__init__(code)


@dataclass(frozen=True)
class BridgeRequest:
    request_id: str
    operation: str
    payload: dict


def _object_pairs(items):
    value = {}
    for key, child in items:
        if key in value:
            raise BridgeProtocolError("duplicate JSON object key")
        value[key] = child
    return value


def _reject_constant(_value: str):
    raise BridgeProtocolError("non-finite JSON number")


def _decode_frame(raw: bytes) -> dict:
    if not isinstance(raw, bytes) or len(raw) > MAX_FRAME_BYTES:
        raise BridgeProtocolError("broker frame exceeds size limit")
    if not raw.endswith(b"\n"):
        raise BridgeProtocolError("broker frame is missing its line terminator")
    try:
        value = json.loads(
            raw[:-1].decode("utf-8", errors="strict"),
            object_pairs_hook=_object_pairs,
            parse_constant=_reject_constant,
        )
    except BridgeProtocolError:
        raise
    except (UnicodeDecodeError, json.JSONDecodeError, RecursionError, ValueError):
        raise BridgeProtocolError("broker frame is not valid UTF-8 JSON") from None
    if not isinstance(value, dict):
        raise BridgeProtocolError("broker frame must be a JSON object")
    return value


def _encode_frame(value: dict) -> bytes:
    try:
        raw = json.dumps(
            value,
            ensure_ascii=False,
            allow_nan=False,
            separators=(",", ":"),
        ).encode("utf-8", errors="strict") + b"\n"
    except (TypeError, ValueError, UnicodeEncodeError, RecursionError):
        raise BridgeProtocolError("broker response is not serializable JSON") from None
    if len(raw) > MAX_FRAME_BYTES:
        raise BridgeProtocolError("broker frame exceeds size limit")
    return raw


def encode_request(request_id: str, payload: dict) -> bytes:
    """Encode one chat-completion RPC request for the container-to-host channel."""
    if not isinstance(request_id, str) or not _REQUEST_ID.fullmatch(request_id):
        raise BridgeProtocolError("broker request ID is invalid")
    if not isinstance(payload, dict):
        raise BridgeProtocolError("broker payload must be an object")
    return _encode_frame({
        "schema_version": SCHEMA_VERSION,
        "type": "request",
        "id": request_id,
        "operation": MODEL_OPERATION,
        "payload": payload,
    })


def _decode_request(raw: bytes) -> BridgeRequest:
    value = _decode_frame(raw)
    if set(value) != {"schema_version", "type", "id", "operation", "payload"}:
        raise BridgeProtocolError("broker request fields are invalid")
    request_id = value["id"]
    if (value["schema_version"] != SCHEMA_VERSION or value["type"] != "request" or
            not isinstance(request_id, str) or not _REQUEST_ID.fullmatch(request_id) or
            value["operation"] != MODEL_OPERATION or not isinstance(value["payload"], dict)):
        raise BridgeProtocolError("broker request envelope is invalid")
    return BridgeRequest(request_id, MODEL_OPERATION, value["payload"])


def _response(request_id: str, *, payload: dict | None = None, error: str | None = None) -> bytes:
    if error is None and isinstance(payload, dict):
        value = {
            "schema_version": SCHEMA_VERSION,
            "type": "response",
            "id": request_id,
            "ok": True,
            "payload": payload,
            "error": None,
        }
    else:
        code = error if error in _ERROR_CODES else "provider_unavailable"
        value = {
            "schema_version": SCHEMA_VERSION,
            "type": "response",
            "id": request_id,
            "ok": False,
            "payload": None,
            "error": {"code": code},
        }
    return _encode_frame(value)


def decode_response(raw: bytes, request_id: str) -> dict:
    """Validate one response and return its payload or a safe broker error."""
    if not isinstance(request_id, str) or not _REQUEST_ID.fullmatch(request_id):
        raise BridgeProtocolError("expected broker request ID is invalid")
    value = _decode_frame(raw)
    if set(value) != {"schema_version", "type", "id", "ok", "payload", "error"}:
        raise BridgeProtocolError("broker response fields are invalid")
    if (value["schema_version"] != SCHEMA_VERSION or value["type"] != "response" or
            value["id"] != request_id or not isinstance(value["ok"], bool)):
        raise BridgeProtocolError("broker response envelope is invalid")
    if value["ok"]:
        if not isinstance(value["payload"], dict) or value["error"] is not None:
            raise BridgeProtocolError("successful broker response is invalid")
        return value["payload"]
    error = value["error"]
    if (value["payload"] is not None or not isinstance(error, dict) or
            set(error) != {"code"} or error["code"] not in _ERROR_CODES):
        raise BridgeProtocolError("failed broker response is invalid")
    raise BrokerOperationError(error["code"])


class StdioBrokerClient:
    """Serialized request client for the container's reserved stdin/stdout."""

    def __init__(
        self,
        input_stream: BinaryIO,
        output_stream: BinaryIO,
        *,
        max_requests: int = MAX_REQUESTS,
    ):
        if (isinstance(max_requests, bool) or not isinstance(max_requests, int) or
                not 1 <= max_requests <= MAX_REQUESTS):
            raise ValueError(f"max_requests must be between 1 and {MAX_REQUESTS}")
        self._input = input_stream
        self._output = output_stream
        self._max_requests = max_requests
        self._requests = 0
        self._lock = threading.Lock()

    def complete(self, payload: dict) -> dict:
        """Send one completion payload and wait for its matching response."""
        with self._lock:
            if self._requests >= self._max_requests:
                raise BrokerOperationError("request_limit")
            request_id = uuid.uuid4().hex
            request = encode_request(request_id, payload)
            self._requests += 1
            try:
                self._output.write(request)
                self._output.flush()
                raw = self._input.readline(MAX_FRAME_BYTES + 1)
            except (OSError, ValueError):
                raise BrokerOperationError("provider_unavailable") from None
            if not raw or len(raw) > MAX_FRAME_BYTES:
                raise BrokerOperationError("provider_unavailable")
            return decode_response(raw, request_id)


def serve_stdio(
    input_stream: BinaryIO,
    output_stream: BinaryIO,
    dispatch: Callable[[BridgeRequest], dict],
    *,
    max_requests: int = MAX_REQUESTS,
) -> int:
    """Dispatch bounded, single-line requests until EOF; return handled count.

    Invalid framing terminates the session. Duplicate IDs and requests beyond
    the configured count receive a generic error and are not dispatched.
    Arbitrary exception text is never serialized back to the container.
    """
    if (isinstance(max_requests, bool) or not isinstance(max_requests, int) or
            not 1 <= max_requests <= MAX_REQUESTS):
        raise ValueError(f"max_requests must be between 1 and {MAX_REQUESTS}")
    seen: set[str] = set()
    handled = 0
    while True:
        raw = input_stream.readline(MAX_FRAME_BYTES + 1)
        if raw == b"":
            return handled
        if len(raw) > MAX_FRAME_BYTES:
            raise BridgeProtocolError("broker frame exceeds size limit")
        request = _decode_request(raw)
        if request.request_id in seen:
            raise BridgeProtocolError("broker request ID was reused")
        seen.add(request.request_id)
        if handled >= max_requests:
            output_stream.write(_response(request.request_id, error="request_limit"))
            output_stream.flush()
            return handled
        try:
            result = dispatch(request)
            if not isinstance(result, dict):
                raise BrokerOperationError("provider_unavailable")
            response = _response(request.request_id, payload=result)
        except BrokerOperationError as exc:
            response = _response(request.request_id, error=exc.code)
        except Exception:
            response = _response(request.request_id, error="provider_unavailable")
        output_stream.write(response)
        output_stream.flush()
        handled += 1
