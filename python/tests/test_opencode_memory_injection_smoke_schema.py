import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA = Path(__file__).resolve().parents[2] / "schema" / "opencode-memory-injection-smoke-v1.schema.json"


def test_opencode_memory_injection_smoke_is_bounded_and_non_authorizing():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    result = {
        "schema_version": "threadforge.opencode-memory-injection-smoke.v1",
        "status": "verified",
        "host_cli_version": "1.18.31",
        "provider": "deterministic-loopback-fixture",
        "tool": "threadforge_procedure_get",
        "tool_call_count": 1,
        "tool_result_observed": True,
        "completion_requests": 2,
        "auxiliary_completion_requests": 0,
        "memory_context_injected": True,
        "task_resume_context_injected": True,
        "task_details_retained_in_report": False,
        "memory_content_retained_in_report": False,
        "plugin_messages_transform_observed": True,
        "approved_memory_readback_verified": True,
        "memory_freshness_verified": True,
        "action_ready": False,
        "guidance_only": True,
        "execution_authorized": False,
        "real_model_invocations": 0,
    }
    validator = Draft202012Validator(schema)
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})
    with pytest.raises(ValidationError):
        validator.validate({**result, "system_context": "private project memory"})
    with pytest.raises(ValidationError):
        validator.validate({**result, "completion_requests": 13})
    with pytest.raises(ValidationError):
        validator.validate({**result, "task_id": "private-task-id"})
    with pytest.raises(ValidationError):
        validator.validate({**result, "task_resume_context_injected": False})
