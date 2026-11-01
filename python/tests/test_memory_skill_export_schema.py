import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def test_memory_skill_export_schema_validates_export_and_rejects_unsafe_shape():
    for revision in (1, 2):
        schema = json.loads((SCHEMA_DIRECTORY / f"memory-skill-export-v{revision}.schema.json").read_text(encoding="utf-8"))
        validator = validator_for(schema)(schema)
        result = {
            "schema_version": f"threadforge.memory-skill-export.v{revision}",
            "skill_id": "threadforge-memory-continuity",
            "skill_revision": revision,
            "path": f".threadforge/skills/threadforge-memory-continuity/rev-{revision:06d}/SKILL.md",
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
