import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_PATH = Path(__file__).resolve().parents[2] / "schema" / "procedure-skill-export-v2.schema.json"


def test_procedure_skill_export_v2_requires_explicit_freshness_and_safe_identity():
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    result = {
        "schema_version": "threadforge.skill-export.v2",
        "graph_id": "build-check",
        "graph_revision": 3,
        "path": ".threadforge/skills/build-check/rev-000003/SKILL.md",
        "sha256": "a" * 64,
        "already_exists": False,
        "freshness": "verified",
    }
    validator.validate(result)
    validator.validate({**result, "freshness": "unverified"})
    for invalid in (
        {**result, "freshness": "stale"},
        {key: value for key, value in result.items() if key != "freshness"},
        {**result, "path": "../outside/SKILL.md"},
        {**result, "source_files": [{"path": "src/private.go", "sha256": "b" * 64}]},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)
