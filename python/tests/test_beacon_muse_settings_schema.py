import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA = Path(__file__).resolve().parents[2] / "schema" / "beacon-muse-settings-v1.schema.json"


def test_beacon_muse_settings_projection_is_path_free_and_never_verifies_registration():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.beacon-muse-settings.v1",
        "source_format": "json",
        "kind": "muse_managed_hooks_pointer",
        "harness": "muse_code",
        "managed_hooks_path_configured": True,
        "target_basename_matches_beacon_file": True,
        "effective_registration_verified": False,
        "omitted_fields": ["credentials", "managed_hooks_path", "local_paths", "unrelated_settings"],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "effective_registration_verified": True})
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})
    with pytest.raises(ValidationError):
        validator.validate({**result, "managed_hooks_path_configured": False})
    with pytest.raises(ValidationError):
        validator.validate({**result, "managed_hooks_path": "/private/user/path"})


def test_beacon_muse_settings_projection_accepts_an_unconfigured_pointer_as_review_evidence():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    result = {
        "schema_version": "threadforge.beacon-muse-settings.v1",
        "source_format": "json",
        "kind": "muse_managed_hooks_pointer",
        "harness": "muse_code",
        "managed_hooks_path_configured": False,
        "target_basename_matches_beacon_file": False,
        "effective_registration_verified": False,
        "omitted_fields": ["credentials", "managed_hooks_path", "local_paths", "unrelated_settings"],
        "human_review_required": True,
        "execution_authorized": False,
    }
    Draft202012Validator(schema).validate(result)
