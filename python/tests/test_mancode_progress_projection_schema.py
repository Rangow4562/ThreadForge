import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_PATH = Path(__file__).resolve().parents[2] / "schema" / "mancode-progress-projection-v1.schema.json"


def test_mancode_progress_projection_schema_is_review_only_and_bounded():
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    projection = {
        "schema_version": "threadforge.mancode-progress-projection.v1",
        "source_schema_version": 1,
        "projection_status": "present",
        "authority": "local_pending_review",
        "task_ids_mapped": False,
        "human_review_required": True,
        "execution_authorized": False,
        "tasks": [{"task_id": "module-export", "status": "待审核", "reason": None}],
    }
    validator.validate(projection)
    with pytest.raises(ValidationError):
        validator.validate({**projection, "execution_authorized": True})
    with pytest.raises(ValidationError):
        validator.validate({**projection, "tasks": [{**projection["tasks"][0], "title": "inferred"}]})
    with pytest.raises(ValidationError):
        validator.validate({**projection, "tasks": [{**projection["tasks"][0], "status": "released"}]})
