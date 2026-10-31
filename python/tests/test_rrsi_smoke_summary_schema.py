import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def test_rrsi_smoke_summary_is_content_free_and_cutover_ineligible():
    schema = json.loads((SCHEMA_DIRECTORY / "rrsi-smoke-summary-v1.schema.json").read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    base = {
        "schema_version": "threadforge.rrsi-smoke-summary.v1",
        "domain": "coding",
        "stage": "smoke_run",
        "round_sha256": "a" * 64,
        "variant_sha256": "b" * 64,
        "artifact_path_sha256": "c" * 64,
        "producer_reported_ok": True,
        "reported_count": 2,
        "reported_count_present": True,
        "missing_count": 0,
        "missing_count_present": False,
        "smoke_id_count": 0,
        "numeric_metric_count": 0,
        "supplemental_field_count": 1,
        "source_sha256": "d" * 64,
        "source_bytes": 88,
        "human_review_required": True,
        "selection_accepted": False,
        "execution_authorized": False,
        "cutover_evidence_eligible": False,
    }
    validator.validate(base)
    validator.validate({**base, "domain": "workspace", "reported_count": 0,
                        "reported_count_present": False, "stage": "compile", "producer_reported_ok": False})
    validator.validate({**base, "domain": "eng", "stage": "run", "reported_count": 0,
                        "reported_count_present": False, "missing_count": 1, "missing_count_present": True,
                        "smoke_id_count": 4, "numeric_metric_count": 6,
                        "supplemental_field_count": 8})
    for invalid in (
        {**base, "selection_accepted": True},
        {**base, "execution_authorized": True},
        {**base, "cutover_evidence_eligible": True},
        {**base, "stage": "compile", "producer_reported_ok": True},
        {**base, "domain": "workspace", "stage": "run"},
        {**base, "domain": "coding", "missing_count_present": True, "missing_count": 1},
        {**base, "domain": "eng", "stage": "smoke_run"},
        {**base, "domain": "eng", "stage": "run", "reported_count": 0,
         "reported_count_present": False, "missing_count": 1, "missing_count_present": True,
         "smoke_id_count": 4, "numeric_metric_count": 5, "supplemental_field_count": 8},
        {**base, "domain": "coding", "stage": "smoke_run", "reported_count_present": False},
        {**base, "task_ids": ["private-id"]},
        {key: value for key, value in base.items() if key != "source_sha256"},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)
