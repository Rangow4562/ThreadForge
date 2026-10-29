import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA = Path(__file__).resolve().parents[2] / "schema" / "beacon-cline-plugin-config-v1.schema.json"


def test_beacon_cline_plugin_presence_is_versioned_and_non_authorizing():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    record = {
        "schema_version": "threadforge.beacon-cline-plugin-config.v1",
        "kind": "managed_plugin_presence",
        "source_format": "typescript_plugin",
        "harness": "cline",
        "managed_marker_present": True,
        "omitted_fields": [
            "plugin_source",
            "commands",
            "credentials",
            "environment",
            "local_paths",
            "unrelated_configuration",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(record)
    for invalid in (
        {**record, "execution_authorized": True},
        {**record, "harness": "opencode"},
        {**record, "plugin_source": "private plugin contents"},
        {**record, "managed_marker_present": False},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)
