"""vLLM backend over the OpenAI-compatible server.

One request per prompt with `max_tokens=1`, `allowed_token_ids` restricted to
the label tokens and `logprobs=K`. vLLM reports logprobs after its logit
processors, so the K entries are exactly the labels, normalized over them.
Prefix caching on the server makes the K permutations of one state cheap.

    vllm serve Qwen/Qwen3-8B --enable-prefix-caching
    Decider(VLLMBackend("http://localhost:8000", "Qwen/Qwen3-8B"))
"""
from __future__ import annotations

import concurrent.futures as cf
import json
import urllib.request
from typing import List, Optional, Sequence

import numpy as np


class VLLMBackend:
    # L2 can only consume the pooler's final normalized representation.
    final_layer_only = True

    def __init__(self, base_url: str, model: str, tokenizer_name: str | None = None,
                 api_key: str = "EMPTY", workers: int = 16, timeout: float = 120.0):
        from transformers import AutoTokenizer

        self.base_url = base_url.rstrip("/")
        self.name = model
        # The model name can be a --served-model-name alias. Tokenizer/config data
        # must come from a loadable source, not from that server-side alias.
        self.source = tokenizer_name or model
        self.api_key = api_key
        self.workers = workers
        self.timeout = timeout
        self.tokenizer = AutoTokenizer.from_pretrained(self.source)
        self._n_layers: int | None = None

    def _one(self, prompt: str, ids: Sequence[int]) -> np.ndarray:
        body = {
            "model": self.name, "prompt": prompt, "max_tokens": 1, "temperature": 0.0,
            "logprobs": len(ids), "allowed_token_ids": list(ids),
        }
        req = urllib.request.Request(
            self.base_url + "/v1/completions", data=json.dumps(body).encode(),
            headers={"Content-Type": "application/json", "Authorization": f"Bearer {self.api_key}"})
        with urllib.request.urlopen(req, timeout=self.timeout) as r:
            out = json.load(r)
        top = out["choices"][0]["logprobs"]["top_logprobs"][0]   # {token_str: logprob}
        # map back by token id: decode each id the same way the server renders it
        by_id = {}
        for tid in ids:
            tok = self.tokenizer.decode([tid])
            conv = self.tokenizer.convert_ids_to_tokens(tid)
            for key in (tok, conv):
                if key in top:
                    by_id[tid] = float(top[key])
                    break
        return np.array([by_id.get(tid, -30.0) for tid in ids], dtype=np.float64)

    def next_token_logprobs(self, prompts: Sequence[str],
                            token_ids: Sequence[Sequence[int]]) -> List[np.ndarray]:
        with cf.ThreadPoolExecutor(self.workers) as ex:
            return list(ex.map(self._one, prompts, token_ids))

    @property
    def n_layers(self) -> int:
        """Return the local model's block count for artifact-compatible layer indices."""
        if self._n_layers is None:
            from transformers import AutoConfig

            config = AutoConfig.from_pretrained(self.source)
            count = getattr(config, "num_hidden_layers", 0)
            if isinstance(count, bool) or not isinstance(count, int) or count < 1:
                raise ValueError("vLLM tokenizer/config source has no valid num_hidden_layers")
            self._n_layers = count
        return self._n_layers

    def hidden_states(self, prompts: Sequence[str], layers: Optional[Sequence[int]] = None,
                      token_ids=None, positions=None):
        """Return final-layer pooled vectors from a vLLM embed server for L2 heads.

        This server exposes only its final normalized vector, not intermediate block
        activations, next-token logits, or per-position vectors. Callers must fit an L2
        head at the final layer (``layers=[-1]``); a different layer is an error, not
        an invitation to silently substitute the final vector.
        """
        if layers is not None and any(int(layer) not in (-1, self.n_layers) for layer in layers):
            raise ValueError(
                f"a vLLM pooling server serves only the final layer ({self.n_layers}); "
                f"asked for {list(layers)}. Fit the head with layers=[-1]."
            )
        if token_ids is not None:
            raise ValueError("an embed server returns no logits; use a generate server for raw / L0 / L1 and this one for L2")
        if positions is not None:
            raise ValueError("a pooling server returns one vector per prompt, not per position")
        if not prompts:
            raise ValueError("vLLM pooling requires at least one prompt")
        with cf.ThreadPoolExecutor(self.workers) as executor:
            vectors = list(executor.map(self._embed, prompts))
        try:
            features = np.stack(vectors)[:, None, :].astype(np.float32)
        except ValueError:
            raise ValueError("vLLM returned inconsistent embedding dimensions") from None
        return features, [None] * len(prompts), None

    def _embed(self, prompt: str) -> np.ndarray:
        body = {"model": self.name, "input": prompt, "encoding_format": "float"}
        req = urllib.request.Request(
            self.base_url + "/v1/embeddings", data=json.dumps(body).encode(),
            headers={"Content-Type": "application/json", "Authorization": f"Bearer {self.api_key}"})
        with urllib.request.urlopen(req, timeout=self.timeout) as response:
            output = json.load(response)
        try:
            vector = np.asarray(output["data"][0]["embedding"], dtype=np.float32)
        except (KeyError, IndexError, TypeError, ValueError):
            raise ValueError("vLLM returned an invalid embedding response") from None
        if vector.ndim != 1 or vector.size == 0 or not np.isfinite(vector).all():
            raise ValueError("vLLM returned an invalid embedding vector")
        return vector
