from types import SimpleNamespace

import pytest

from rrsi import llm
from threadforge_py import protocol


def test_default_generation_uses_local_only_backend(monkeypatch, tmp_path):
    captured = {}

    def local_generate(system, user, backend, max_new_tokens):
        captured.update(system=system, user=user, backend=backend,
                        max_new_tokens=max_new_tokens)
        return '```json\n{"ok":true}\n```'

    monkeypatch.setenv("RRSI_LOCAL_MODEL_PATH", str(tmp_path))
    monkeypatch.setattr(protocol, "_local_generate_text", local_generate)
    monkeypatch.setattr(llm, "_PROJECTS", [{"project_id": "must-not-use", "region": "test"}])
    monkeypatch.setattr(llm, "_client_for", lambda _index: pytest.fail("local default used Vertex"))

    result = llm.generate("prompt", system="system", json_only=True,
                          cache_prefix="stable prefix")

    assert result == '{"ok":true}'
    assert captured["user"] == "stable prefix\n\nprompt"
    assert "Output ONLY a single valid JSON object" in captured["system"]
    assert captured["backend"] == {
        "type": "hf", "model_path": str(tmp_path), "device": "cpu", "dtype": "auto",
    }
    assert captured["max_new_tokens"] == 1024


def test_missing_local_model_fails_without_vertex_fallback(monkeypatch):
    monkeypatch.delenv("RRSI_LOCAL_MODEL_PATH", raising=False)
    monkeypatch.setattr(llm, "_PROJECTS", [{"project_id": "must-not-use", "region": "test"}])
    monkeypatch.setattr(llm, "_client_for", lambda _index: pytest.fail("missing local model fell back to Vertex"))

    with pytest.raises(protocol.ProtocolError, match="no local model is configured"):
        llm.generate("prompt")


@pytest.mark.parametrize("value", [None, "true", 1])
def test_remote_generation_requires_a_boolean_opt_in(value):
    with pytest.raises(ValueError, match="must be a boolean"):
        llm.generate("prompt", remote_inference_opt_in=value)


def test_remote_opt_in_without_projects_fails_before_client_creation(monkeypatch):
    monkeypatch.setattr(llm, "_PROJECTS", [])
    monkeypatch.setattr(llm, "_client_for", lambda _index: pytest.fail("no project should reach client"))

    with pytest.raises(RuntimeError, match="RRSI_VERTEX_PROJECTS is not configured"):
        llm.generate("prompt", remote_inference_opt_in=True)


def test_explicit_remote_opt_in_uses_mocked_client_only(monkeypatch):
    captured = {}

    def create(**kwargs):
        captured.update(kwargs)
        return SimpleNamespace(content=[SimpleNamespace(type="text", text='{"ok":true}')])

    client = SimpleNamespace(messages=SimpleNamespace(create=create))
    monkeypatch.setattr(llm, "_PROJECTS", [{"project_id": "offline-test", "region": "test"}])
    monkeypatch.setattr(llm, "_client_for", lambda _index: client)

    result = llm.generate("prompt", system="system", json_only=True, max_retries=1,
                          remote_inference_opt_in=True)

    assert result == '{"ok":true}'
    assert captured["model"] == llm.MODEL
    assert captured["messages"] == [{"role": "user", "content": "prompt"}]
    assert "Output ONLY a single valid JSON object" in captured["system"]
