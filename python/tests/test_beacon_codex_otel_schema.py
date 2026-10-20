import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA = Path(__file__).resolve().parents[2] / "schema" / "beacon-codex-otel-config-v1.schema.json"


def _result():
    return {
        "schema_version": "threadforge.beacon-codex-otel-config.v1",
        "source_format": "toml",
        "kind": "codex_otel_configuration",
        "harness": "codex_cli",
        "otel_configured": True,
        "environment": "dev",
        "log_user_prompt": False,
        "log_exporters": [{"name": "otlp-grpc", "endpoint_scope": "loopback"}],
        "trace_exporters": [{"name": "otlp-http", "endpoint_scope": "non_loopback"}],
        "omitted_fields": [
            "credentials", "exporter_endpoints", "headers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }


def test_beacon_codex_otel_projection_is_metadata_only_and_non_authorizing():
    validator = Draft202012Validator(json.loads(SCHEMA.read_text(encoding="utf-8")))
    validator.validate(_result())
    with pytest.raises(ValidationError):
        validator.validate({**_result(), "execution_authorized": True})
    with pytest.raises(ValidationError):
        validator.validate({**_result(), "log_exporters": [{"name": "otlp-grpc", "endpoint": "http://secret"}]})


def test_beacon_codex_otel_projection_rejects_unbounded_or_unknown_exporters():
    validator = Draft202012Validator(json.loads(SCHEMA.read_text(encoding="utf-8")))
    with pytest.raises(ValidationError):
        validator.validate({**_result(), "trace_exporters": [{"name": "custom", "endpoint_scope": "unknown"}]})
    with pytest.raises(ValidationError):
        validator.validate({**_result(), "environment": "production-with-private-label"})
