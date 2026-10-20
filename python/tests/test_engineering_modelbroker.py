from __future__ import annotations

import pytest

from threadforge_domains.engineering.bridge import BridgeRequest, BrokerOperationError
from threadforge_domains.engineering.modelbroker import (
    MAX_CHAT_COMPLETION_TOKENS,
    ModelBroker,
    serve_model_stdio,
    validate_chat_completion_payload,
)


def _request(payload):
    return BridgeRequest("a" * 32, "model.chat.completions", payload)


def _response():
    return {
        "id": "completion-id",
        "object": "chat.completion",
        "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}}],
        "usage": {"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16},
        "provider_private_field": "not-forwarded",
    }


def test_local_model_broker_forces_configured_model_and_keeps_provider_secret_local():
    calls = []
    broker = ModelBroker(
        environ={
            "THREADFORGE_EVALUATION_BACKEND": "local",
            "THREADFORGE_LOCAL_MODEL_BASE_URL": "http://127.0.0.1:8123/v1",
            "THREADFORGE_LOCAL_EVALUATION_MODEL": "local-review-model",
            "OPENAI_API_KEY": "host-secret-must-not-be-forwarded",
            "UNRELATED_TOKEN": "host-token-must-not-be-forwarded",
        },
        completion=lambda **kwargs: calls.append(kwargs) or _response(),
    )

    result = broker.dispatch(_request({
        "model": "attacker-selected/provider",
        "messages": [{"role": "user", "content": "untrusted prompt"}],
        "max_tokens": 300,
    }))

    assert broker.model == "openai/local-review-model"
    assert result["choices"][0]["message"]["content"] == "ok"
    assert "provider_private_field" not in result
    assert calls[0]["model"] == "openai/local-review-model"
    assert calls[0]["api_base"] == "http://127.0.0.1:8123/v1"
    assert calls[0]["api_key"] == "threadforge-local-no-secret"
    assert calls[0]["max_tokens"] == 300
    assert calls[0]["timeout"] == 600
    assert calls[0]["num_retries"] == 0
    assert "host-secret-must-not-be-forwarded" not in repr(calls[0])
    assert "host-token-must-not-be-forwarded" not in repr(calls[0])
    assert broker.calls == 1
    assert broker.prompt_tokens == 11
    assert broker.completion_tokens == 5
    assert broker.unmeasured_calls == 0


def test_remote_model_broker_requires_opt_in_and_never_accepts_caller_provider_settings():
    calls = []
    settings = {
        "THREADFORGE_EVALUATION_BACKEND": "remote",
        "THREADFORGE_REMOTE_EVALUATION_MODEL": "provider/model-v1",
        "PROVIDER_API_KEY": "host-only-provider-secret",
    }
    with pytest.raises(BrokerOperationError, match="model_unavailable"):
        ModelBroker(environ=settings, completion=lambda **kwargs: _response())

    broker = ModelBroker(
        remote_inference_opt_in=True,
        environ=settings,
        completion=lambda **kwargs: calls.append(kwargs) or _response(),
    )
    result = broker.dispatch(_request({
        "model": "attacker/provider",
        "messages": [{"role": "user", "content": "request"}],
    }))

    assert result["choices"]
    assert calls[0]["model"] == "provider/model-v1"
    assert "api_key" not in calls[0]
    assert "api_base" not in calls[0]
    assert "host-only-provider-secret" not in repr(calls[0])


@pytest.mark.parametrize(
    "payload",
    [
        [],
        {"messages": []},
        {"messages": [{"role": "user"}], "api_key": "secret"},
        {"messages": [{"role": "user"}], "api_base": "https://attacker.invalid"},
        {"messages": [{"role": "user"}], "stream": True},
        {"messages": [{"role": "user"}], "n": 2},
        {"messages": [{"role": "user"}], "max_tokens": MAX_CHAT_COMPLETION_TOKENS + 1},
        {"messages": [{"role": "user"}], "temperature": -0.1},
        {"messages": [{"role": "user"}], "temperature": float("nan")},
    ],
)
def test_chat_payload_rejects_invalid_or_untrusted_dispatch_fields(payload):
    with pytest.raises(BrokerOperationError, match="request_rejected"):
        validate_chat_completion_payload(payload)


def test_chat_payload_preserves_tool_messages_but_omits_candidate_model_selector():
    result = validate_chat_completion_payload({
        "model": "untrusted/model",
        "messages": [
            {"role": "assistant", "tool_calls": [{"id": "call-1", "type": "function"}]},
            {"role": "tool", "tool_call_id": "call-1", "content": "result"},
        ],
        "tools": [{"type": "function", "function": {"name": "read_text_file"}}],
        "temperature": 0.2,
    })
    assert "model" not in result
    assert result["messages"][0]["tool_calls"][0]["id"] == "call-1"
    assert result["tools"][0]["function"]["name"] == "read_text_file"


def test_model_broker_fails_closed_on_provider_errors_and_tracks_unmeasured_usage():
    def failure(**_kwargs):
        raise RuntimeError("sensitive provider diagnostic")

    broker = ModelBroker(environ={"THREADFORGE_EVALUATION_BACKEND": "local"}, completion=failure)
    with pytest.raises(BrokerOperationError, match="provider_unavailable"):
        broker.dispatch(_request({"messages": [{"role": "user", "content": "x"}]}))
    assert broker.calls == 1
    assert broker.failed_calls == 1
    assert broker.unmeasured_calls == 1

    broker = ModelBroker(
        environ={"THREADFORGE_EVALUATION_BACKEND": "local"},
        completion=lambda **_kwargs: {"choices": [{"index": 0, "message": {"content": "ok"}}]},
    )
    broker.dispatch(_request({"messages": [{"role": "user", "content": "x"}]}))
    assert broker.calls == 1
    assert broker.unmeasured_calls == 1


def test_serve_model_stdio_uses_policy_configured_host_dispatch():
    import io
    import json

    from threadforge_domains.engineering.bridge import encode_request

    request_id = "b" * 32
    stdin = io.BytesIO(encode_request(request_id, {"messages": [{"role": "user", "content": "x"}]}))
    stdout = io.BytesIO()
    count, broker = serve_model_stdio(
        stdin,
        stdout,
        environ={"THREADFORGE_EVALUATION_BACKEND": "local"},
        completion=lambda **_kwargs: _response(),
    )
    response = json.loads(stdout.getvalue().decode("utf-8"))
    assert count == 1
    assert broker.calls == 1
    assert response["id"] == request_id
    assert response["ok"] is True
