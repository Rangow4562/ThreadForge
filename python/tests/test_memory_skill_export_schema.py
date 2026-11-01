import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_PATH = Path(__file__).resolve().parents[2] / "schema" / "memory-skill-export-v1.schema.json"


def test_memory_skill_export_schema_validates_export_and_rejects_unsafe_shape():
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    result = {
        "schema_version": "threadforge.memory-skill-export.v1",
        "skill_id": "threadforge-memory-continuity",
        "skill_revision": 1,
        "path": ".threadforge/skills/threadforge-memory-continuity/rev-000001/SKILL.md",
        "sha256": "a" * 64,
        "already_exists": False,
    }
    validator.validate(result)

    with pytest.raises(ValidationError):
        validator.validate({**result, "already_exists": "false"})
    with pytest.raises(ValidationError):
        validator.validate({**result, "path": "C:/private/SKILL.md"})
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})
