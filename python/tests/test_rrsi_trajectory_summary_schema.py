import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_PATH = Path(__file__).resolve().parents[2] / "schema" / "rrsi-trajectory-summary-v1.schema.json"
HASH = "a" * 64


def _workspace_summary(domain="workspace"):
    return {
        "schema_version": "threadforge.rrsi-trajectory-summary.v1",
        "domain": domain,
        "record_kind": "workspace_trajectory" if domain == "workspace" else "engineering_trajectory",
        "job_id_sha256": HASH,
        "task_id_sha256": HASH,
        "trial_key_sha256": HASH,
        "source_sha256": HASH,
        "message_count": 2,
        "system_message_count": 0,
        "user_message_count": 1,
        "assistant_message_count": 1,
        "tool_message_count": 0,
        "other_message_count": 0,
        "tool_call_count": 2,
    }


def test_rrsi_trajectory_tool_sequence_hashes_are_optional_compatible_and_bounded():
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)

    legacy = _workspace_summary()
    validator.validate(legacy)

    projected = {
        **legacy,
        "tool_call_name_hashes": [HASH, "b" * 64],
        "tool_sequence_truncated": False,
    }
    validator.validate(projected)

    with pytest.raises(ValidationError):
        validator.validate({**legacy, "tool_call_name_hashes": [HASH]})
    with pytest.raises(ValidationError):
        validator.validate({**legacy, "tool_call_name_hashes": ["not-a-digest"], "tool_sequence_truncated": False})
    with pytest.raises(ValidationError):
        validator.validate({**legacy, "tool_call_name_hashes": [HASH] * 257, "tool_sequence_truncated": True})

    engineering = _workspace_summary("eng")
    validator.validate({**engineering, "tool_call_name_hashes": [], "tool_sequence_truncated": False})
