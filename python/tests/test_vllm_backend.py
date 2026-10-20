import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from types import SimpleNamespace

import numpy as np
import pytest

from anyjev.backends.vllm import VLLMBackend


class _EmbeddingHandler(BaseHTTPRequestHandler):
    requests = []
    dimension = 8

    def do_POST(self):  # noqa: N802 - stdlib handler contract
        payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        type(self).requests.append((self.path, payload, self.headers.get("Authorization")))
        vector = [float(len(payload["input"]) + index) for index in range(type(self).dimension)]
        body = json.dumps({"data": [{"embedding": vector}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass


@pytest.fixture
def embedding_server():
    _EmbeddingHandler.requests = []
    server = HTTPServer(("127.0.0.1", 0), _EmbeddingHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}"
    finally:
        server.shutdown()
        thread.join(timeout=2)
        server.server_close()


def _install_transformers_stub(monkeypatch, calls):
    class AutoTokenizer:
        @staticmethod
        def from_pretrained(source):
            calls.append(("tokenizer", source))
            class Tokenizer:
                @staticmethod
                def encode(text, add_special_tokens=False):
                    del add_special_tokens
                    return [1 if text.strip() == "A" else 2]

            return Tokenizer()

    class AutoConfig:
        @staticmethod
        def from_pretrained(source):
            calls.append(("config", source))
            return SimpleNamespace(num_hidden_layers=28)

    monkeypatch.setitem(sys.modules, "transformers", SimpleNamespace(AutoTokenizer=AutoTokenizer, AutoConfig=AutoConfig))


def test_vllm_pooling_backend_serves_final_layer_vectors_and_loads_config_from_source(embedding_server, monkeypatch):
    calls = []
    _install_transformers_stub(monkeypatch, calls)
    backend = VLLMBackend(embedding_server, "served-alias", tokenizer_name="local/model-source", api_key="fixture-key")

    features, logprobs, positional = backend.hidden_states(["short", "longer"], [-1])

    assert backend.n_layers == 28
    assert backend.hidden_states(["same"], [28])[0].shape == (1, 1, _EmbeddingHandler.dimension)
    assert features.shape == (2, 1, _EmbeddingHandler.dimension)
    assert features.dtype == np.float32
    assert np.allclose(features[0, 0], np.arange(5, 13, dtype=np.float32))
    assert logprobs == [None, None]
    assert positional is None
    assert calls == [("tokenizer", "local/model-source"), ("config", "local/model-source")]
    assert len(_EmbeddingHandler.requests) == 3
    assert all(path == "/v1/embeddings" for path, _, _ in _EmbeddingHandler.requests)
    assert all(payload["model"] == "served-alias" for _, payload, _ in _EmbeddingHandler.requests)
    assert all(auth == "Bearer fixture-key" for _, _, auth in _EmbeddingHandler.requests)


def test_vllm_pooling_backend_refuses_nonfinal_layers_logits_and_positions(embedding_server, monkeypatch):
    _install_transformers_stub(monkeypatch, [])
    backend = VLLMBackend(embedding_server, "served-alias", tokenizer_name="local/model-source")

    with pytest.raises(ValueError, match="only the final layer"):
        backend.hidden_states(["prompt"], [18])
    with pytest.raises(ValueError, match="returns no logits"):
        backend.hidden_states(["prompt"], [-1], token_ids=[[1, 2]])
    with pytest.raises(ValueError, match="one vector per prompt"):
        backend.hidden_states(["prompt"], [-1], positions=[[0]])
    with pytest.raises(ValueError, match="at least one prompt"):
        backend.hidden_states([], [-1])
    assert _EmbeddingHandler.requests == []


def test_decider_defaults_final_layer_only_backend_to_its_available_layer(embedding_server, monkeypatch, tmp_path):
    from threadforge_py import protocol

    calls = []
    _install_transformers_stub(monkeypatch, calls)
    states = [{"message": "x" * size} for size in range(1, 33)]
    labels = [0 if size <= 16 else 1 for size in range(1, 33)]

    response = protocol.dispatch({
        "schema_version": protocol.PROTOCOL_VERSION,
        "request_id": "vllm-l2-final-layer",
        "operation": "decision.calibrate",
        "params": {
            "question": {"kind": "choice", "text": "Classify the message length", "options": ["short", "long"]},
            "examples": [{"state": state, "label": label} for state, label in zip(states, labels)],
            "backend": {
                "type": "vllm", "base_url": embedding_server, "model": "served-alias",
                "tokenizer_path": str(tmp_path),
            },
            "level": "L2",
        },
    })

    assert response["status"] == "ok", response
    assert response["result"]["artifact"]["layer_abs"] == 28
    assert calls == [("tokenizer", str(tmp_path.resolve())), ("config", str(tmp_path.resolve()))]
    assert _EmbeddingHandler.requests
    assert all(path == "/v1/embeddings" for path, _, _ in _EmbeddingHandler.requests)

