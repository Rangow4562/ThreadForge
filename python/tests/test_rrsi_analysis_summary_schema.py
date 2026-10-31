import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def test_rrsi_analysis_summary_schema_keeps_report_content_private_and_non_authoritative():
    schema = json.loads((SCHEMA_DIRECTORY / "rrsi-analysis-summary-v1.schema.json").read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    base = {
        "schema_version": "threadforge.rrsi-analysis-summary.v1",
        "domain": "coding",
        "artifact_kind": "round_report",
        "round_index": 1,
        "round_sha256": "a" * 64,
        "artifact_path_sha256": "b" * 64,
        "failure_mode_count": 1,
        "reported_failure_task_count": 2,
        "failure_evidence_count": 1,
        "capability_gap_count": 1,
        "reported_gap_task_count": 1,
        "gap_evidence_count": 0,
        "success_habit_count": 1,
        "reported_success_task_count": 3,
        "affected_task_reference_count": 3,
        "digest_count": 4,
        "digest_count_present": True,
        "analysis_incomplete": False,
        "source_sha256": "c" * 64,
        "source_bytes": 256,
        "human_review_required": True,
        "execution_authorized": False,
        "cutover_evidence_eligible": False,
    }
    validator.validate(base)
    validator.validate({
        **base,
        "domain": "workspace",
        "artifact_kind": "global_latest",
        "round_index": None,
        "round_sha256": "",
        "capability_gap_count": 0,
        "reported_gap_task_count": 0,
        "gap_evidence_count": 0,
        "digest_count": 0,
        "digest_count_present": False,
    })
    validator.validate({**base, "analysis_incomplete": True})
    for invalid in (
        {**base, "execution_authorized": True},
        {**base, "cutover_evidence_eligible": True},
        {**base, "task_ids": ["private-task"]},
        {**base, "artifact_kind": "global_latest"},
        {**base, "digest_count_present": False},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)
