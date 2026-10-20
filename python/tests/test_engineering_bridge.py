from __future__ import annotations

import io
import json
from pathlib import Path

from jsonschema import Draft202012Validator
import pytest

from threadforge_domains.engineering.bridge import (
    BridgeProtocolError,
    BrokerOperationError,
    SCHEMA_VERSION,
    StdioBrokerClient,
    _response,
    decode_response,
    encode_request,
    serve_stdio,
)


def _request(request_id: str, payload: dict | None = None) -> bytes:
    return encode_request(request_id, payload or {"messages": [{"role": "user", "content": "x"}]})


def _parse_response(raw: bytes) -> dict:
    return json.loads(raw.decode("utf-8"))


def test_stdio_broker_dispatches_versioned_requests_and_matches_ids():
    first = "1" * 32
    second = "2" * 32
    incoming = io.BytesIO(_request(first) + _request(second, {"messages": []}))
    outgoing = io.BytesIO()
    seen = []

    count = serve_stdio(
        incoming,
        outgoing,
        lambda request: seen.append(request) or {"id": request.request_id, "choices": []},
    )

    replies = [_parse_response(line) for line in outgoing.getvalue().splitlines()]
    assert count == 2
    assert [request.request_id for request in seen] == [first, second]
    assert [request.operation for request in seen] == ["model.chat.completions"] * 2
    assert replies == [
        {
            "schema_version": SCHEMA_VERSION,
            "type": "response",
            "id": first,
            "ok": True,
            "payload": {"id": first, "choices": []},
            "error": None,
        },
        {
            "schema_version": SCHEMA_VERSION,
            "type": "response",
            "id": second,
            "ok": True,
            "payload": {"id": second, "choices": []},
            "error": None,
        },
    ]


def test_stdio_broker_redacts_provider_exception_details():
    output = io.BytesIO()

    serve_stdio(
        io.BytesIO(_request("3" * 32)),
        output,
        lambda _request: (_ for _ in ()).throw(RuntimeError("provider secret: do-not-return")),
    )

    reply = _parse_response(output.getvalue())
    assert reply["ok"] is False
    assert reply["error"] == {"code": "provider_unavailable"}
    assert "do-not-return" not in output.getvalue().decode("utf-8")
    with pytest.raises(BrokerOperationError, match="provider_unavailable"):
        decode_response(output.getvalue(), "3" * 32)


@pytest.mark.parametrize(
    "raw",
    [
        b'{"schema_version":"threadforge.engineering-broker.v1","schema_version":"bad"}\n',
        b'{"schema_version":"threadforge.engineering-broker.v1","type":"request","id":"'
        + b"4" * 32 + b'","operation":"other","payload":{}}\n',
        b'{"schema_version":"threadforge.engineering-broker.v1","type":"request","id":"'
        + b"5" * 32 + b'","operation":"model.chat.completions","payload":{},"api_key":"x"}\n',
        b'{"schema_version":"threadforge.engineering-broker.v1","type":"request","id":"'
        + b"6" * 32 + b'","operation":"model.chat.completions","payload":{"n":NaN}}\n',
        b"not-json\n",
        b'{"schema_version":"threadforge.engineering-broker.v1","type":"request"}',
    ],
)
def test_stdio_broker_rejects_malformed_or_out_of_contract_frames(raw):
    dispatched = []
    with pytest.raises(BridgeProtocolError):
        serve_stdio(io.BytesIO(raw), io.BytesIO(), lambda request: dispatched.append(request) or {})
    assert dispatched == []


def test_stdio_broker_rejects_reused_request_ids_without_dispatching_twice():
    request = _request("7" * 32)
    dispatched = []

    with pytest.raises(BridgeProtocolError, match="request ID was reused"):
        serve_stdio(
            io.BytesIO(request + request),
            io.BytesIO(),
            lambda value: dispatched.append(value) or {"choices": []},
        )
    assert len(dispatched) == 1


def test_stdio_broker_enforces_request_limit():
    first = "8" * 32
    second = "9" * 32
    output = io.BytesIO()
    dispatched = []

    count = serve_stdio(
        io.BytesIO(_request(first) + _request(second)),
        output,
        lambda request: dispatched.append(request) or {"choices": []},
        max_requests=1,
    )

    replies = [_parse_response(line) for line in output.getvalue().splitlines()]
    assert count == 1
    assert [value.request_id for value in dispatched] == [first]
    assert replies[1]["id"] == second
    assert replies[1]["error"] == {"code": "request_limit"}


def test_stdio_broker_client_round_trips_and_validates_response_id():
    class FeedableBuffer(io.BytesIO):
        def append(self, raw: bytes) -> None:
            position = self.tell()
            self.seek(0, io.SEEK_END)
            super().write(raw)
            self.seek(position)

    responses = FeedableBuffer()

    class ResponseWriter:
        def write(self, raw: bytes) -> int:
            request = json.loads(raw.decode("utf-8"))
            responses.append(_response(request["id"], payload={"choices": [{"index": 0}]}))
            return len(raw)

        def flush(self) -> None:
            pass

    client = StdioBrokerClient(responses, ResponseWriter(), max_requests=1)
    assert client.complete({"messages": []}) == {"choices": [{"index": 0}]}
    with pytest.raises(BrokerOperationError, match="request_limit"):
        client.complete({"messages": []})

    wrong_id = _response("a" * 32, payload={"choices": []})
    with pytest.raises(BridgeProtocolError, match="envelope"):
        decode_response(wrong_id, "b" * 32)


def test_stdio_broker_checks_limits_and_frame_terminators():
    with pytest.raises(ValueError, match="max_requests"):
        serve_stdio(io.BytesIO(), io.BytesIO(), lambda _request: {}, max_requests=0)
    with pytest.raises(BridgeProtocolError, match="line terminator"):
        decode_response(b"{}", "c" * 32)


def test_broker_request_and_response_frames_match_the_published_schema():
    root = Path(__file__).resolve().parents[2]
    schema = json.loads((root / "schema" / "engineering-broker-v1.schema.json").read_text())
    validator = Draft202012Validator(schema)
    request = json.loads(_request("d" * 32))
    response = _parse_response(_response("d" * 32, payload={"choices": []}))

    validator.validate(request)
    validator.validate(response)
    invalid = dict(response, ok=False)
    assert list(validator.iter_errors(invalid))
