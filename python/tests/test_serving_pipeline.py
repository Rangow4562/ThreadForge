import json
import importlib
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from types import SimpleNamespace

import numpy as np
import pytest
from jsonschema.validators import validator_for

from threadforge_py import protocol
from threadforge_py import serving_pipeline as serving


def _write_cases(root: Path, name: str, split: str, question_id: str, question: dict, states: list[dict], labels: list[int]) -> str:
    document = {
        "schema_version": f"threadforge.anyjev-distillation-{split}-cases.v1",
        "question_id": question_id,
        "question": question,
        "examples": [{"state": state, "gold_index": label} for state, label in zip(states, labels)],
    }
    path = root / name
    path.write_text(json.dumps(document), encoding="utf-8")
    return name


def _fixture_request(tmp_path: Path, question_count: int = 2) -> dict:
    model = tmp_path / "model"
    model.mkdir()
    (model / "config.json").write_text("{}", encoding="utf-8")
    case_root = tmp_path / "cases"
    case_root.mkdir()
    train_states = [{"case": f"train-{index}"} for index in range(8)]
    test_states = [{"case": "heldout-a"}, {"case": "heldout-b"}, {"case": "heldout-c"}]
    questions = []
    for index in range(question_count):
        question_id = f"fixture.question.{index}"
        spec = {"kind": "choice", "text": f"Choose category {index}", "options": ["no", "yes"], "name": question_id}
        train_path = _write_cases(case_root, f"train-{index}.json", "train", question_id, spec,
                                  train_states, [0, 1, 0, 1, 0, 1, 0, 1])
        test_path = _write_cases(case_root, f"test-{index}.json", "test", question_id, spec,
                                 test_states, [0, 1, 0])
        questions.append({"question_id": question_id, "train_cases_path": train_path, "test_cases_path": test_path})
    return {
        "schema_version": "threadforge.anyjev-serving-pipeline-request.v1",
        "model_path": str(model),
        "case_root": str(case_root),
        "questions": questions,
        "repeats": 3,
        "port": 0,
    }


def _install_fake_runtime(monkeypatch):
    import anyjev
    import anyjev.backends.vllm as vllm_backend

    class FakeDecider:
        def __init__(self, backend, level):
            assert backend is not None
            assert level == "L2"

        def fit_head(self, question, states, labels, *, layers):
            assert len(states) == len(labels) == 8
            assert layers == [-1]
            return {"method": "head:lda"}

        def decide_batch(self, states, question, *, level):
            assert level == "L2"
            vector = np.asarray([0.8, 0.2], dtype=np.float64)
            return [SimpleNamespace(probs=vector.copy()) for _ in states]

    class FakeBackend:
        api_keys = []

        def __init__(self, url, model, **kwargs):
            assert url == "http://127.0.0.1:9000"
            assert model == serving.SERVED_MODEL_NAME
            assert kwargs["tokenizer_name"]
            type(self).api_keys.append(kwargs["api_key"])

    class FakeServer:
        opened = 0
        instances = []

        def __init__(self, **kwargs):
            self.kwargs = kwargs
            type(self).instances.append(self)
            self.url = "http://127.0.0.1:9000"

        def __enter__(self):
            type(self).opened += 1
            return self

        def __exit__(self, *_args):
            return None

    monkeypatch.setattr(anyjev, "Decider", FakeDecider)
    monkeypatch.setattr(vllm_backend, "VLLMBackend", FakeBackend)
    monkeypatch.setattr(serving, "LocalVLLMServer", FakeServer)
    monkeypatch.setattr(serving, "_local_model_depth", lambda _path: 3)
    monkeypatch.setattr(protocol, "_fingerprint_model_directory", lambda _path: {
        "sha256": "a" * 64, "file_count": 1, "total_bytes": 2,
    })

    truncate_module = importlib.import_module("anyjev.truncate")

    def fake_truncate(params):
        Path(params["output_path"]).mkdir()
        return {
            "kept_blocks": params["blocks"], "original_blocks": 3,
            "output_sha256": "b" * 64, "execution_verified": False,
            "cutover_evidence_eligible": False,
        }

    monkeypatch.setattr(truncate_module, "truncate", fake_truncate)
    FakeServer.backend_api_keys = FakeBackend.api_keys
    return FakeServer


def test_local_serving_pipeline_measures_depths_and_emits_non_cutover_receipt(tmp_path, monkeypatch):
    request = _fixture_request(tmp_path)
    fake_server = _install_fake_runtime(monkeypatch)
    request_schema_path = Path(__file__).resolve().parents[2] / "schema" / "anyjev-serving-pipeline-request-v1.schema.json"
    request_schema = json.loads(request_schema_path.read_text(encoding="utf-8"))
    validator_for(request_schema)(request_schema).validate(request)
    result = serving.run_serving_pipeline(request)

    assert [row["blocks"] for row in result["rows"]] == [3, 2]
    assert [row["checkpoint_sha256"] for row in result["rows"]] == ["a" * 64, "b" * 64]
    assert all(len(row["questions"]) == 2 for row in result["rows"])
    assert all(row["multi_question"]["question_count"] == 2 for row in result["rows"])
    assert all(row["questions"][0]["accuracy"] == pytest.approx(2 / 3) for row in result["rows"])
    assert fake_server.opened == 2
    assert result["model_inference_verified"] is True
    assert result["remote_inference_used"] is False
    assert result["independent_holdout_verified"] is False
    assert result["execution_verified"] is False
    assert result["cutover_evidence_eligible"] is False
    assert len(fake_server.instances) == 2
    api_keys = [instance.kwargs["api_key"] for instance in fake_server.instances]
    assert all(isinstance(key, str) and len(key) >= 32 for key in api_keys)
    assert api_keys == fake_server.backend_api_keys
    assert len(set(api_keys)) == 2
    encoded = json.dumps(result)
    for secret in (*api_keys, str(tmp_path), "fixture.question", "heldout-a", "train-0"):
        assert secret not in encoded

    schema_path = Path(__file__).resolve().parents[2] / "schema" / "anyjev-serving-pipeline-result-v1.schema.json"
    schema = json.loads(schema_path.read_text(encoding="utf-8"))
    validator_for(schema)(schema).validate(result)


def test_pipeline_rejects_remote_model_ids_and_unknown_request_fields(tmp_path):
    request = _fixture_request(tmp_path)
    request["model_path"] = "Qwen/Qwen2.5-7B-Instruct"
    with pytest.raises(serving.ServingPipelineError, match="absolute local directory"):
        serving._validate_request(request)
    second_root = tmp_path / "second"
    second_root.mkdir()
    request = _fixture_request(second_root)
    request["remote_opt_in"] = True
    with pytest.raises(serving.ServingPipelineError, match="unsupported fields"):
        serving._validate_request(request)


def test_pipeline_rejects_overlapping_train_and_test_states(tmp_path, monkeypatch):
    request = _fixture_request(tmp_path, question_count=1)
    case_root = Path(request["case_root"])
    question = request["questions"][0]
    test_path = case_root / question["test_cases_path"]
    document = json.loads(test_path.read_text(encoding="utf-8"))
    train_doc = json.loads((case_root / question["train_cases_path"]).read_text(encoding="utf-8"))
    document["examples"][0]["state"] = train_doc["examples"][0]["state"]
    test_path.write_text(json.dumps(document), encoding="utf-8")
    monkeypatch.setattr(protocol, "_fingerprint_model_directory", lambda _path: {
        "sha256": "a" * 64, "file_count": 1, "total_bytes": 2,
    })
    monkeypatch.setattr(serving, "_local_model_depth", lambda _path: 3)
    with pytest.raises(serving.ServingPipelineError, match="overlap"):
        serving.run_serving_pipeline(request)


def test_offline_child_environment_does_not_forward_credentials(monkeypatch):
    monkeypatch.setenv("HF_TOKEN", "private-token-value")
    monkeypatch.setenv("THREADFORGE_VLLM_API_KEY", "private-api-key")
    monkeypatch.setenv("VLLM_API_KEY", "parent-vllm-api-key")
    environment = serving._offline_environment()
    assert environment["HF_HUB_OFFLINE"] == "1"
    assert environment["TRANSFORMERS_OFFLINE"] == "1"
    assert environment["VLLM_NO_USAGE_STATS"] == "1"
    assert "HF_TOKEN" not in environment
    assert "THREADFORGE_VLLM_API_KEY" not in environment
    assert "VLLM_API_KEY" not in environment


def test_local_server_readiness_requires_the_ephemeral_api_key(tmp_path):
    api_key = "fixture-ephemeral-api-key-that-is-long-enough"
    seen_authorization = []

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            seen_authorization.append(self.headers.get("Authorization"))
            if self.headers.get("Authorization") != f"Bearer {api_key}":
                self.send_response(401)
                self.end_headers()
                return
            payload = json.dumps({"data": [{"id": serving.SERVED_MODEL_NAME}]}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, *_args):
            return

    server = serving.LocalVLLMServer(
        model_path=tmp_path,
        api_key=api_key,
        port=0,
        gpu_memory_utilization=0.85,
        prefix_caching=False,
        quantization=None,
        startup_timeout_seconds=10,
        work_directory=tmp_path,
        log_path=tmp_path / "unused.log",
    )
    command = server._command()
    assert command[command.index("--api-key") + 1] == api_key
    assert command[command.index("--host") + 1] == "127.0.0.1"
    httpd = ThreadingHTTPServer(("127.0.0.1", server.port), Handler)
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    server.process = SimpleNamespace(poll=lambda: None)
    try:
        server._wait_until_ready()
        assert seen_authorization == [None, f"Bearer {api_key}"]
    finally:
        httpd.shutdown()
        httpd.server_close()
        thread.join(timeout=2)


def test_local_server_rejects_readiness_endpoint_without_api_key_auth(tmp_path):
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            payload = json.dumps({"data": [{"id": serving.SERVED_MODEL_NAME}]}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, *_args):
            return

    server = serving.LocalVLLMServer(
        model_path=tmp_path,
        api_key="fixture-ephemeral-api-key-that-is-long-enough",
        port=0,
        gpu_memory_utilization=0.85,
        prefix_caching=False,
        quantization=None,
        startup_timeout_seconds=10,
        work_directory=tmp_path,
        log_path=tmp_path / "unused.log",
    )
    httpd = ThreadingHTTPServer(("127.0.0.1", server.port), Handler)
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    server.process = SimpleNamespace(poll=lambda: None)
    try:
        with pytest.raises(serving.ServingPipelineError, match="does not enforce"):
            server._wait_until_ready()
    finally:
        httpd.shutdown()
        httpd.server_close()
        thread.join(timeout=2)
