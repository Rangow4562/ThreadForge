import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_PATH = Path(__file__).resolve().parents[2] / "schema" / "rrsi-proposal-summary-v1.schema.json"


def test_rrsi_proposal_summary_is_bounded_and_non_authoritative():
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    summary = {
        "schema_version": "threadforge.rrsi-proposal-summary.v1",
        "domain": "workspace",
        "proposal_status": "done",
        "file_edit_count": 2,
        "declared_edit_count": 1,
        "edit_components": ["memory"],
        "authority": "local_pending_review",
        "human_review_required": True,
        "selection_accepted": False,
        "execution_authorized": False,
        "omitted_fields": [
            "summary", "reason", "mechanism", "targets_mode", "interaction_log", "edit_ids",
            "hypotheses", "predicted_task_ids", "review_text", "diffs",
        ],
    }
    validator.validate(summary)
    with pytest.raises(ValidationError):
        validator.validate({**summary, "selection_accepted": True})
    with pytest.raises(ValidationError):
        validator.validate({**summary, "execution_authorized": True})
    with pytest.raises(ValidationError):
        validator.validate({**summary, "edit_components": ["arbitrary"]})
    with pytest.raises(ValidationError):
        validator.validate({**summary, "hypothesis": "raw private proposal text"})
