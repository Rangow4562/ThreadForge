import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA = Path(__file__).resolve().parents[2] / "schema" / "beacon-opencode-plugin-config-v1.schema.json"


def test_beacon_opencode_plugin_presence_is_versioned_and_non_authorizing():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    result = {
        "schema_version": "threadforge.beacon-opencode-plugin-config.v1",
        "kind": "managed_plugin_presence",
        "source_format": "typescript_plugin",
        "harness": "opencode",
        "managed_marker_present": True,
        "omitted_fields": [
            "plugin_source", "commands", "credentials", "environment", "local_paths", "unrelated_configuration",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator = Draft202012Validator(schema)
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})
    with pytest.raises(ValidationError):
        validator.validate({**result, "plugin_source": "export default {}"})
    with pytest.raises(ValidationError):
        validator.validate({**result, "managed_marker_present": False})
