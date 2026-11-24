"""Bounded OpenAI-compatible transport for explicitly opted-in memory learning."""

from __future__ import annotations

import http.client
import ipaddress
import json
import os
import re
import ssl
from urllib.parse import urlsplit


MAX_REMOTE_REQUEST_BYTES = 128 << 10
MAX_REMOTE_RESPONSE_BYTES = 32 << 10
REMOTE_TIMEOUT_SECONDS = 120.0


class RemoteInferenceError(ValueError):
    """A sanitized remote inference configuration or transport failure."""


def generate(system: str, user: str, backend: dict[str, object], max_new_tokens: int = 512) -> str:
    """Generate one bounded completion without following redirects or logging credentials."""
    if not isinstance(backend, dict) or set(backend) - {"type", "base_url", "model", "remote_opt_in"}:
        raise RemoteInferenceError("invalid remote backend configuration")
    if backend.get("type") != "openai_compatible" or backend.get("remote_opt_in") is not True:
        raise RemoteInferenceError("remote inference requires explicit opt-in")
    if isinstance(max_new_tokens, bool) or not isinstance(max_new_tokens, int) or not 1 <= max_new_tokens <= 512:
        raise RemoteInferenceError("invalid remote generation limit")

    base_url = backend.get("base_url")
    model = backend.get("model")
    if not isinstance(base_url, str) or len(base_url) > 2048:
        raise RemoteInferenceError("invalid remote endpoint")
    if (not isinstance(model, str) or not model.strip() or len(model) > 256 or
            any(ord(character) < 32 for character in model)):
        raise RemoteInferenceError("invalid remote model")

    parsed = urlsplit(base_url)
    if (parsed.scheme not in {"http", "https"} or not parsed.hostname or "@" in parsed.netloc or
            parsed.username or parsed.password or
            parsed.query or parsed.fragment or any(character.isspace() for character in base_url) or
            "\\" in base_url):
        raise RemoteInferenceError("invalid remote endpoint")
    try:
        port = parsed.port or (443 if parsed.scheme == "https" else 80)
    except ValueError as exc:
        raise RemoteInferenceError("invalid remote endpoint") from exc
    if not 1 <= port <= 65535:
        raise RemoteInferenceError("invalid remote endpoint")

    hostname = parsed.hostname
    normalized_host = hostname.lower().rstrip(".")
    is_loopback = normalized_host == "localhost"
    try:
        is_loopback = is_loopback or ipaddress.ip_address(normalized_host).is_loopback
    except ValueError:
        pass
    if parsed.scheme == "http" and not is_loopback:
        raise RemoteInferenceError("non-loopback remote endpoints must use HTTPS")

    path = parsed.path.rstrip("/")
    if (not re.fullmatch(r"(?:/[A-Za-z0-9._~-]+)*", path) or
            any(segment in {".", ".."} for segment in path.split("/")) or
            path.endswith("/chat/completions")):
        raise RemoteInferenceError("invalid remote endpoint path")
    request_path = (path or "") + "/chat/completions"

    headers = {"Accept": "application/json", "Content-Type": "application/json"}
    if not is_loopback:
        api_key = os.environ.get("THREADFORGE_MEMORY_API_KEY", "")
        if not api_key:
            raise RemoteInferenceError("remote inference credential is not configured")
        if len(api_key) > 4096 or any(not 33 <= ord(character) <= 126 for character in api_key):
            raise RemoteInferenceError("remote inference credential is invalid")
        headers["Authorization"] = f"Bearer {api_key}"

    if not isinstance(system, str) or not isinstance(user, str):
        raise RemoteInferenceError("invalid remote prompt")
    request_body = json.dumps({
        "model": model.strip(),
        "messages": [{"role": "system", "content": system}, {"role": "user", "content": user}],
        "max_tokens": max_new_tokens,
        "temperature": 0,
        "stream": False,
    }, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    if len(request_body) > MAX_REMOTE_REQUEST_BYTES:
        raise RemoteInferenceError("remote inference request exceeds its size limit")

    connection: http.client.HTTPConnection
    if parsed.scheme == "https":
        connection = http.client.HTTPSConnection(
            hostname, port, timeout=REMOTE_TIMEOUT_SECONDS, context=ssl.create_default_context(),
        )
    else:
        connection = http.client.HTTPConnection(hostname, port, timeout=REMOTE_TIMEOUT_SECONDS)
    try:
        # http.client does not follow redirects, so credentials and observations stay on this origin.
        connection.request("POST", request_path, body=request_body, headers=headers)
        response = connection.getresponse()
        if response.status != 200:
            raise RemoteInferenceError(f"remote endpoint returned HTTP {response.status}")
        raw = response.read(MAX_REMOTE_RESPONSE_BYTES + 1)
    except RemoteInferenceError:
        raise
    except Exception as exc:
        raise RemoteInferenceError(f"remote transport failed: {type(exc).__name__}") from None
    finally:
        connection.close()

    if len(raw) > MAX_REMOTE_RESPONSE_BYTES:
        raise RemoteInferenceError("remote inference response exceeds its size limit")
    try:
        payload = json.loads(raw.decode("utf-8"))
        content = payload["choices"][0]["message"]["content"]
    except (UnicodeDecodeError, json.JSONDecodeError, KeyError, IndexError, TypeError):
        raise RemoteInferenceError("remote endpoint returned an invalid completion") from None
    if not isinstance(content, str) or len(content.encode("utf-8")) > MAX_REMOTE_RESPONSE_BYTES:
        raise RemoteInferenceError("remote endpoint returned an invalid completion")
    return content
