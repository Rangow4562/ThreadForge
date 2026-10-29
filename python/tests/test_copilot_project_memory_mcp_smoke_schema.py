import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA = Path(__file__).resolve().parents[2] / "schema" / "copilot-project-memory-mcp-smoke-v1.schema.json"


def test_copilot_project_memory_mcp_smoke_is_bounded_and_non_authorizing():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.copilot-project-memory-mcp-smoke.v1",
        "status": "verified",
        "host_cli_version": "1.0.64",
        "provider": "deterministic-loopback-fixture",
        "tool": "threadforge_project_memory_read_v2",
        "tool_call_count": 1,
        "tool_result_observed": True,
        "memory_content_retained_in_report": False,
        "project_instruction_guidance_loaded": True,
        "approved_memory_readback_verified": True,
        "memory_freshness_verified": True,
        "advisory_result_markers_verified": True,
        "action_ready": False,
        "guidance_only": True,
        "execution_authorized": False,
        "available_tools_restricted_to_project_memory_read": True,
        "provider_requests": 2,
        "auxiliary_provider_requests": 0,
        "real_model_invocations": 0,
    }
    validator.validate(result)
    validator.validate({
        "schema_version": "threadforge.copilot-project-memory-mcp-smoke.v1",
        "status": "not-installed",
        "real_model_invocations": 0,
    })
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})
    with pytest.raises(ValidationError):
        validator.validate({**result, "project_instruction_guidance_loaded": False})
    with pytest.raises(ValidationError):
        validator.validate({**result, "memory_guidance": "private project memory"})
    with pytest.raises(ValidationError):
        validator.validate({**result, "provider_requests": 21})
    with pytest.raises(ValidationError):
        validator.validate({**result, "tool": "threadforge_project_memory_read"})
