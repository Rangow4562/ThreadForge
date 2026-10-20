import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA = Path(__file__).resolve().parents[2] / "schema" / "beacon-gemini-otel-config-v1.schema.json"


def _result():
    return {
        "schema_version": "threadforge.beacon-gemini-otel-config.v1",
        "source_format": "json",
        "kind": "gemini_otel_configuration",
        "harness": "gemini_cli",
        "telemetry_configured": True,
        "target": "local",
        "endpoint_scope": "loopback",
        "otlp_protocol": "grpc",
        "use_collector": True,
        "enabled": True,
        "log_prompts": True,
        "traces": True,
        "outfile_configured": False,
        "omitted_fields": [
            "credentials", "exporter_endpoint", "local_paths", "outfile_path", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }


def test_beacon_gemini_otel_projection_is_non_authorizing_and_retains_privacy_flags():
    validator = Draft202012Validator(json.loads(SCHEMA.read_text(encoding="utf-8")))
    validator.validate(_result())
    with pytest.raises(ValidationError):
        validator.validate({**_result(), "execution_authorized": True})
    with pytest.raises(ValidationError):
        validator.validate({**_result(), "log_prompts": "true"})


def test_beacon_gemini_otel_projection_cannot_include_raw_endpoints_or_paths():
    validator = Draft202012Validator(json.loads(SCHEMA.read_text(encoding="utf-8")))
    with pytest.raises(ValidationError):
        validator.validate({**_result(), "exporter_endpoint": "http://127.0.0.1:4317"})
    with pytest.raises(ValidationError):
        validator.validate({**_result(), "outfile_configured": "/private/telemetry.log"})
