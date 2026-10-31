import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def test_rrsi_deliverable_summary_is_content_free_and_advisory_only():
    schema = json.loads((SCHEMA_DIRECTORY / "rrsi-deliverable-summary-v1.schema.json").read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    base = {
        "schema_version": "threadforge.rrsi-deliverable-summary.v1",
        "job_id_sha256": "a" * 64,
        "task_id_sha256": "b" * 64,
        "trial_key_sha256": "c" * 64,
        "artifact_path_sha256": "d" * 64,
        "source_sha256": "e" * 64,
        "source_bytes": 42,
        "content_imported": False,
        "human_review_required": True,
        "execution_authorized": False,
        "cutover_evidence_eligible": False,
    }
    for domain, record_kind in (
        ("coding", "coding_terminal_recording"),
        ("workspace", "workspace_deliverable"),
        ("eng", "engineering_payload"),
    ):
        validator.validate({**base, "domain": domain, "record_kind": record_kind})
    for invalid in (
        {**base, "domain": "coding", "record_kind": "workspace_deliverable"},
        {**base, "domain": "eng", "record_kind": "engineering_payload", "content_imported": True},
        {**base, "domain": "workspace", "record_kind": "workspace_deliverable", "execution_authorized": True},
        {**base, "domain": "coding", "record_kind": "coding_terminal_recording", "private_path": "runs/private.cast"},
        {**base, "domain": "eng", "record_kind": "engineering_payload", "source_bytes": 33_554_433},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)
