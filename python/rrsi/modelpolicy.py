# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Fail-closed model environment for external RRSI evaluation runners."""

from __future__ import annotations

import ipaddress
import os
import re
from urllib.parse import urlparse

_MODEL_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}\Z")
_CREDENTIAL_ENV = re.compile(
    r"(?:API[_-]?KEY|ACCESS[_-]?KEY|TOKEN|SECRET|PASSWORD|CREDENTIALS?|AUTH(?:ORIZATION)?)", re.I,
)


def evaluation_runtime(remote_inference_opt_in: bool, environ: dict[str, str] | None = None
                       ) -> tuple[str, dict[str, str]]:
    """Return a model ID and child environment; default is loopback-only local inference.

    The remote mode is available only with an explicit RRSI remote-inference
    opt-in and a fully specified provider/model. Local mode strips inherited
    credential variables before starting an external benchmark runner.
    """
    if not isinstance(remote_inference_opt_in, bool):
        raise ValueError("remote_inference_opt_in must be a boolean")
    source = dict(os.environ if environ is None else environ)
    backend = source.get("THREADFORGE_EVALUATION_BACKEND", "local")

    if backend == "remote":
        if not remote_inference_opt_in:
            raise PermissionError("remote benchmark inference requires remote_inference_opt_in=true")
        model = source.get("THREADFORGE_REMOTE_EVALUATION_MODEL", "")
        if not model or not _MODEL_NAME.fullmatch(model):
            raise ValueError("remote benchmark inference requires a configured model identifier")
        return model, source

    if backend != "local":
        raise ValueError("THREADFORGE_EVALUATION_BACKEND must be local or remote")

    base_url = source.get("THREADFORGE_LOCAL_MODEL_BASE_URL", "http://127.0.0.1:8000/v1")
    parsed = urlparse(base_url)
    if (parsed.scheme != "http" or not parsed.hostname or parsed.username or parsed.password or
            parsed.query or parsed.fragment or parsed.path not in {"", "/v1"}):
        raise ValueError("local model base URL must be a loopback HTTP origin with optional /v1 path")
    try:
        loopback = parsed.hostname.lower() == "localhost" or ipaddress.ip_address(parsed.hostname).is_loopback
    except ValueError:
        loopback = False
    if not loopback:
        raise ValueError("local model base URL must use localhost or a loopback IP address")

    local_name = source.get("THREADFORGE_LOCAL_EVALUATION_MODEL", "threadforge-local")
    if not _MODEL_NAME.fullmatch(local_name):
        raise ValueError("local evaluation model identifier is invalid")
    model = f"openai/{local_name}"
    child = {key: value for key, value in source.items() if not _CREDENTIAL_ENV.search(key)}
    child.update({
        "OPENAI_BASE_URL": base_url,
        "OPENAI_API_BASE": base_url,
        "OPENAI_API_KEY": "threadforge-local-no-secret",
        "RRSI_POLICY_MODEL": model,
        "MODEL": model,
    })
    return model, child
