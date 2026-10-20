import sys
from importlib.machinery import ModuleSpec
from types import ModuleType, SimpleNamespace

import pytest

from anyjev.transformers_compat import causal_mask_kwargs, pretrained_dtype_kwargs


@pytest.mark.parametrize(
    ("version", "expected"),
    [("4.53.0", "torch_dtype"), ("4.57.1", "torch_dtype"),
     ("5.0.0", "dtype"), ("5.0.0.dev0", "dtype")],
)
def test_pretrained_dtype_keyword_tracks_transformers_major(version, expected):
    marker = object()
    assert pretrained_dtype_kwargs(version, marker) == {expected: marker}


def test_pretrained_dtype_keyword_rejects_unknown_version():
    with pytest.raises(RuntimeError, match="version is unsupported"):
        pretrained_dtype_kwargs("unknown", "auto")


@pytest.mark.parametrize(("version", "expected"), [("4.53.0", "torch_dtype"), ("5.0.0", "dtype")])
def test_hf_backend_constructor_uses_versioned_dtype_keyword(monkeypatch, version, expected):
    from anyjev.backends.hf import HFBackend

    calls = []
    torch = ModuleType("torch")
    torch.__spec__ = ModuleSpec("torch", loader=None)
    torch.bfloat16 = object()

    class Tokenizer:
        padding_side = None
        pad_token = "eos"
        eos_token = "eos"

    class Model:
        config = SimpleNamespace(num_hidden_layers=4, hidden_size=8)
        device = "cpu"

        def eval(self):
            pass

    class AutoTokenizer:
        @staticmethod
        def from_pretrained(*args, **kwargs):
            return Tokenizer()

    class AutoModelForCausalLM:
        @staticmethod
        def from_pretrained(*args, **kwargs):
            calls.append(kwargs)
            return Model()

    transformers = ModuleType("transformers")
    transformers.__spec__ = ModuleSpec("transformers", loader=None)
    transformers.__version__ = version
    transformers.AutoTokenizer = AutoTokenizer
    transformers.AutoModelForCausalLM = AutoModelForCausalLM
    monkeypatch.setitem(sys.modules, "torch", torch)
    monkeypatch.setitem(sys.modules, "transformers", transformers)

    HFBackend("local-fixture", device="cpu")

    assert expected in calls[0]
    assert calls[0][expected] == torch.bfloat16
    assert len({"dtype", "torch_dtype"} & set(calls[0])) == 1


def test_causal_mask_kwargs_support_transformers_4_signature():
    def builder(config, input_embeds, attention_mask, cache_position, past_key_values, position_ids):
        raise AssertionError("signature helper does not invoke the mask builder")

    values = dict(
        config="config", input_embeds="embeds", attention_mask="mask",
        cache_position="cache-position", past_key_values="past", position_ids="positions",
    )
    assert causal_mask_kwargs(builder, **values) == {
        "config": "config", "input_embeds": "embeds", "attention_mask": "mask",
        "cache_position": "cache-position", "past_key_values": "past", "position_ids": "positions",
    }


def test_causal_mask_kwargs_support_transformers_5_signature():
    def builder(config, inputs_embeds, attention_mask, past_key_values, position_ids):
        raise AssertionError("signature helper does not invoke the mask builder")

    values = dict(
        config="config", input_embeds="embeds", attention_mask="mask",
        cache_position="cache-position", past_key_values="past", position_ids="positions",
    )
    assert causal_mask_kwargs(builder, **values) == {
        "config": "config", "inputs_embeds": "embeds", "attention_mask": "mask",
        "past_key_values": "past", "position_ids": "positions",
    }


def test_causal_mask_kwargs_fails_closed_for_unknown_signature():
    def builder(config, attention_mask):
        pass

    with pytest.raises(NotImplementedError, match="no supported embedding argument"):
        causal_mask_kwargs(
            builder, config="config", input_embeds="embeds", attention_mask="mask",
            cache_position="position", past_key_values="past", position_ids="positions",
        )
