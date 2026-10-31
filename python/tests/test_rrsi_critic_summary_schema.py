import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def test_rrsi_critic_summary_is_content_free_and_never_accepts_or_authorizes():
    schema = json.loads((SCHEMA_DIRECTORY / "rrsi-critic-summary-v1.schema.json").read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    base = {
        "schema_version": "threadforge.rrsi-critic-summary.v1",
        "domain": "coding",
        "artifact_kind": "final",
        "attempt_index": 0,
        "round_sha256": "a" * 64,
        "variant_sha256": "b" * 64,
        "artifact_path_sha256": "c" * 64,
        "verdict": "accept",
        "reason_count": 0,
        "risk_note_count": 0,
        "source_sha256": "d" * 64,
        "source_bytes": 73,
        "human_review_required": True,
        "selection_accepted": False,
        "execution_authorized": False,
        "cutover_evidence_eligible": False,
    }
    validator.validate(base)
    validator.validate({**base, "artifact_kind": "attempt", "attempt_index": 2, "verdict": "reject"})
    for invalid in (
        {**base, "execution_authorized": True},
        {**base, "selection_accepted": True},
        {**base, "cutover_evidence_eligible": True},
        {**base, "artifact_kind": "final", "attempt_index": 1},
        {**base, "private_reason": "critic prose"},
        {key: value for key, value in base.items() if key != "risk_note_count"},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)
