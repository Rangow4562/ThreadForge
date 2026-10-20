import json
import sys
from importlib.machinery import ModuleSpec
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest

from anyjev.truncate import TruncateError, layer_of, truncate


class _Tensor:
    def contiguous(self):
        return self


def _checkpoint(root: Path):
    root.mkdir()
    (root / "config.json").write_text('{"num_hidden_layers":4}\n', encoding="utf-8")
    (root / "weights.fixture").write_bytes(b"fixture checkpoint weights")


def _install_fake_hf(monkeypatch, source: Path, *, mutate_source=False, valid_layers=True,
                     transformers_version="4.53.0"):
    calls = []
    torch = ModuleType("torch")
    torch.__spec__ = ModuleSpec("torch", loader=None)
    torch.float16 = "float16"
    torch.bfloat16 = "bfloat16"
    torch.float32 = "float32"

    class AutoConfig:
        @staticmethod
        def from_pretrained(path, **kwargs):
            calls.append(("config", path, kwargs))
            assert kwargs == {"local_files_only": True, "trust_remote_code": False}
            return SimpleNamespace(num_hidden_layers=4)

    class AutoModelForCausalLM:
        @staticmethod
        def from_pretrained(path, **kwargs):
            calls.append(("model", path, kwargs))
            assert kwargs["local_files_only"] is True
            assert kwargs["trust_remote_code"] is False
            dtype_key = "dtype" if int(transformers_version.split(".", 1)[0]) >= 5 else "torch_dtype"
            assert kwargs[dtype_key] == "auto"
            assert len({"dtype", "torch_dtype"} & set(kwargs)) == 1
            if mutate_source:
                (source / "weights.fixture").write_bytes(b"changed source weights")

            class Model:
                config = SimpleNamespace(num_hidden_layers=4, _name_or_path=str(source))

                @staticmethod
                def state_dict():
                    if not valid_layers:
                        return {"model.embed_tokens.weight": _Tensor(), "lm_head.weight": _Tensor()}
                    return {
                        **{f"model.layers.{index}.weight": _Tensor() for index in range(4)},
                        "model.embed_tokens.weight": _Tensor(),
                        "lm_head.weight": _Tensor(),
                    }

                def save_pretrained(self, path, *, state_dict, safe_serialization):
                    assert safe_serialization is True
                    target = Path(path)
                    (target / "config.json").write_text(json.dumps({
                        "num_hidden_layers": self.config.num_hidden_layers,
                        "_name_or_path": self.config._name_or_path,
                    }), encoding="utf-8")
                    (target / "model.safetensors").write_text(
                        json.dumps(sorted(state_dict)), encoding="utf-8",
                    )

            return Model()

    class AutoTokenizer:
        @staticmethod
        def from_pretrained(path, **kwargs):
            calls.append(("tokenizer", path, kwargs))
            assert kwargs == {"local_files_only": True, "trust_remote_code": False}

            class Tokenizer:
                name_or_path = str(source)
                init_kwargs = {"name_or_path": str(source), "tokenizer_class": "FixtureTokenizer"}

                @staticmethod
                def save_pretrained(output):
                    target = Path(output)
                    (target / "tokenizer.json").write_text('{"fixture":true}\n', encoding="utf-8")
                    (target / "tokenizer_config.json").write_text(json.dumps({
                        "name_or_path": str(source), "tokenizer_class": "FixtureTokenizer",
                    }), encoding="utf-8")

            return Tokenizer()

    transformers = ModuleType("transformers")
    transformers.__spec__ = ModuleSpec("transformers", loader=None)
    transformers.__version__ = transformers_version
    transformers.AutoConfig = AutoConfig
    transformers.AutoModelForCausalLM = AutoModelForCausalLM
    transformers.AutoTokenizer = AutoTokenizer
    monkeypatch.setitem(sys.modules, "torch", torch)
    monkeypatch.setitem(sys.modules, "transformers", transformers)
    return calls


def _params(source: Path, output: Path, **overrides):
    return {
        "schema_version": "threadforge.anyjev-truncate-request.v1",
        "model_path": str(source), "blocks": 2, "output_path": str(output),
        **overrides,
    }


def test_layer_of_recognizes_transformer_blocks_only():
    assert layer_of("model.layers.7.self_attn.q_proj.weight") == 7
    assert layer_of("layers.13.input_layernorm.weight") == 13
    assert layer_of("model.embed_tokens_2.weight") is None
    assert layer_of("model.norm.weight") is None


def test_truncate_writes_path_free_local_review_only_checkpoint(monkeypatch, tmp_path):
    source = tmp_path / "private-source-checkpoint"
    output = tmp_path / "truncated-checkpoint"
    _checkpoint(source)
    calls = _install_fake_hf(monkeypatch, source)
    source_before = (source / "weights.fixture").read_bytes()

    result = truncate(_params(source, output))

    weights = json.loads((output / "model.safetensors").read_text(encoding="utf-8"))
    metadata = json.loads((output / "threadforge-truncation.json").read_text(encoding="utf-8"))
    assert result["schema_version"] == "threadforge.anyjev-truncated-checkpoint.v1"
    assert result["original_blocks"] == 4 and result["kept_blocks"] == 2
    assert result["kept_tensor_count"] == 4 and result["dropped_tensor_count"] == 2
    assert result["output_ready"] is True and result["execution_verified"] is False
    assert result["human_review_required"] is True and result["cutover_evidence_eligible"] is False
    assert "model.layers.0.weight" in weights and "model.layers.1.weight" in weights
    assert "model.layers.2.weight" not in weights and "model.layers.3.weight" not in weights
    assert result["source_checkpoint_sha256"] == result["tokenizer_source_sha256"]
    assert metadata["source_checkpoint_sha256"] == result["source_checkpoint_sha256"]
    assert str(source) not in json.dumps(metadata)
    assert str(source) not in (output / "config.json").read_text(encoding="utf-8")
    assert str(source) not in (output / "tokenizer_config.json").read_text(encoding="utf-8")
    assert (source / "weights.fixture").read_bytes() == source_before
    assert calls[0][0] == "config" and calls[1][0] == "model" and calls[2][0] == "tokenizer"
    assert not list(tmp_path.glob(".threadforge-truncate-*"))


def test_truncate_uses_transformers_5_dtype_keyword(monkeypatch, tmp_path):
    source = tmp_path / "source-v5"
    output = tmp_path / "output-v5"
    _checkpoint(source)
    calls = _install_fake_hf(monkeypatch, source, transformers_version="5.0.0.dev0")

    truncate(_params(source, output))

    model_kwargs = next(kwargs for kind, _path, kwargs in calls if kind == "model")
    assert model_kwargs["dtype"] == "auto"
    assert "torch_dtype" not in model_kwargs


def test_truncate_refuses_remote_paths_existing_destinations_and_unsupported_layouts(monkeypatch, tmp_path):
    source = tmp_path / "source"
    _checkpoint(source)
    output = tmp_path / "output"
    _install_fake_hf(monkeypatch, source)

    with pytest.raises(TruncateError, match="absolute local directory"):
        truncate(_params(source, output, model_path="org/remote-model"))
    output.mkdir()
    with pytest.raises(TruncateError, match="never overwrites"):
        truncate(_params(source, output))
    output.rmdir()
    _install_fake_hf(monkeypatch, source, valid_layers=False)
    with pytest.raises(TruncateError, match="does not support verified block truncation"):
        truncate(_params(source, output))
    assert not output.exists()
    assert not list(tmp_path.glob(".threadforge-truncate-*"))


def test_truncate_detects_changed_source_and_cleans_staging(monkeypatch, tmp_path):
    source = tmp_path / "source"
    output = tmp_path / "output"
    _checkpoint(source)
    _install_fake_hf(monkeypatch, source, mutate_source=True)

    with pytest.raises(TruncateError, match="changed during truncation"):
        truncate(_params(source, output))
    assert not output.exists()
    assert not list(tmp_path.glob(".threadforge-truncate-*"))


def test_versioned_python_protocol_exposes_path_free_truncation_receipt(monkeypatch, tmp_path):
    from threadforge_py.protocol import PROTOCOL_VERSION, dispatch

    source = tmp_path / "source"
    output = tmp_path / "output"
    _checkpoint(source)
    _install_fake_hf(monkeypatch, source)
    response = dispatch({
        "schema_version": PROTOCOL_VERSION,
        "request_id": "truncate-local-checkpoint",
        "operation": "decision.model.truncate",
        "params": _params(source, output),
    })
    assert response["status"] == "ok"
    assert response["result"]["schema_version"] == "threadforge.anyjev-truncated-checkpoint.v1"
    assert response["result"]["output_ready"] is True
    assert str(source) not in json.dumps(response)

    capabilities = dispatch({
        "schema_version": PROTOCOL_VERSION, "request_id": "capabilities",
        "operation": "capabilities", "params": {},
    })["result"]
    assert "decision.model.truncate" in capabilities["operations"]
    assert capabilities["model_truncation"]["downloads"] is False
