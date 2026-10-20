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
"""Local-first inference for the three RRSI search roles.

The default backend is a configured local-only Hugging Face model. Vertex
inference is available only when the caller separately opts in.
"""

from __future__ import annotations

import itertools
import os
import re
import threading
import time

MODEL = os.environ.get("RRSI_SEARCH_MODEL", "claude-opus-4-8")
_PROJECTS = [
    {"project_id": p.strip(), "region": os.environ.get("RRSI_VERTEX_REGION", "global")}
    for p in os.environ.get("RRSI_VERTEX_PROJECTS", "").split(",")
    if p.strip()
]
MAX_TOKENS = 20_000

_clients: dict = {}
_clients_lock = threading.Lock()
_rr = itertools.count()
_JSON_SUFFIX = ("\n\nOutput ONLY a single valid JSON object. No prose before or "
                "after, no markdown fences.")


def _client_for(idx: int):
    if not _PROJECTS:
        raise RuntimeError("set RRSI_VERTEX_PROJECTS to a comma-separated list of GCP "
                           "projects with Claude on Vertex AI enabled")
    from anthropic import AnthropicVertex
    with _clients_lock:
        c = _clients.get(idx)
        if c is None:
            c = AnthropicVertex(**_PROJECTS[idx])
            _clients[idx] = c
        return c


def extract_json(text: str) -> str:
    t = text.strip()
    m = re.search(r"```(?:json)?\s*(.*?)```", t, re.S)
    if m:
        t = m.group(1).strip()
    if not t.startswith("{") and not t.startswith("["):
        start = min([i for i in (t.find("{"), t.find("[")) if i != -1], default=-1)
        if start != -1:
            t = t[start:]
    if t and t[0] == "{" and not t.endswith("}"):
        end = t.rfind("}")
        if end != -1:
            t = t[:end + 1]
    if t and t[0] == "[" and not t.endswith("]"):
        end = t.rfind("]")
        if end != -1:
            t = t[:end + 1]
    return t


def _generate_local(prompt: str, system: str, max_tokens: int,
                    cache_prefix: str | None) -> str:
    backend = {
        "type": "hf",
        "model_path": os.environ.get("RRSI_LOCAL_MODEL_PATH", ""),
        "device": os.environ.get("RRSI_LOCAL_MODEL_DEVICE", "cpu"),
        "dtype": os.environ.get("RRSI_LOCAL_MODEL_DTYPE", "auto"),
    }
    local_prompt = f"{cache_prefix}\n\n{prompt}" if cache_prefix else prompt
    # Reuse the service's bounded HF backend: local_files_only=True,
    # trust_remote_code=False, 4096-token input and 1024-token output caps.
    from threadforge_py.protocol import _local_generate_text

    return _local_generate_text(system, local_prompt, backend,
                                max_new_tokens=min(max_tokens, 1024))


def _generate_vertex(prompt: str, system: str, max_retries: int,
                     model: str | None, max_tokens: int,
                     cache_prefix: str | None) -> str:
    mdl = model or MODEL
    content = ([{"type": "text", "text": cache_prefix,
                 "cache_control": {"type": "ephemeral"}},
                {"type": "text", "text": prompt}] if cache_prefix else prompt)
    n = len(_PROJECTS)
    if n == 0:
        raise RuntimeError("remote inference is opted in, but RRSI_VERTEX_PROJECTS is not configured")
    start = next(_rr)
    last_err: Exception | None = None
    for attempt in range(max_retries):
        idx = (start + attempt) % n
        try:
            client = _client_for(idx)
            kwargs = {"model": mdl, "max_tokens": max_tokens,
                      "messages": [{"role": "user", "content": content}]}
            if system:
                kwargs["system"] = system
            resp = client.messages.create(**kwargs)
            text = "".join(b.text for b in resp.content
                           if getattr(b, "type", "") == "text")
            if text:
                return text
            last_err = RuntimeError("empty response")
        except Exception as e:  # noqa: BLE001 - rotate to the other project
            last_err = e
            time.sleep(min(2 ** attempt, 30))
    raise RuntimeError(f"generate failed after {max_retries} tries: {last_err}")


def generate(prompt: str, system: str | None = None, max_retries: int = 6,
             json_only: bool = False, model: str | None = None,
             max_tokens: int = MAX_TOKENS, cache_prefix: str | None = None,
             remote_inference_opt_in: bool = False) -> str:
    if not isinstance(remote_inference_opt_in, bool):
        raise ValueError("remote_inference_opt_in must be a boolean")
    if isinstance(max_tokens, bool) or not isinstance(max_tokens, int) or not 1 <= max_tokens <= MAX_TOKENS:
        raise ValueError(f"max_tokens must be an integer from 1 to {MAX_TOKENS}")
    if isinstance(max_retries, bool) or not isinstance(max_retries, int) or not 1 <= max_retries <= 12:
        raise ValueError("max_retries must be an integer from 1 to 12")
    sys_prompt = (system or "") + (_JSON_SUFFIX if json_only else "")
    if remote_inference_opt_in:
        result = _generate_vertex(prompt, sys_prompt, max_retries, model,
                                  max_tokens, cache_prefix)
    else:
        result = _generate_local(prompt, sys_prompt, max_tokens, cache_prefix)
    return extract_json(result) if json_only else result


if __name__ == "__main__":
    print(generate("Reply with exactly: OK", max_tokens=16))
