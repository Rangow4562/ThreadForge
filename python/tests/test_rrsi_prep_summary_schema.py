import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def test_rrsi_prep_summary_schema_keeps_candidate_state_advisory_and_content_free():
    schema = json.loads((SCHEMA_DIRECTORY / "rrsi-prep-summary-v1.schema.json").read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    counts = {
        "prompt": 0,
        "control_flow": 0,
        "config": 0,
        "output_plumbing": 0,
        "context_mgmt": 0,
        "client_tool": 0,
        "skill": 1,
        "memory": 0,
        "subagent": 0,
    }
    base = {
        "schema_version": "threadforge.rrsi-prep-summary.v1",
        "domain": "coding",
        "gate_failure": "",
        "round_sha256": "a" * 64,
        "variant_sha256": "b" * 64,
        "artifact_path_sha256": "c" * 64,
        "commit_sha256": "d" * 64,
        "commit_present": True,
        "branch_matches_identity": True,
        "edit_count": 1,
        "edit_component_counts": counts,
        "diff_path_present": True,
        "mechanism_present": True,
        "diagnostic_bytes": 0,
        "source_sha256": "e" * 64,
        "source_bytes": 432,
        "human_review_required": True,
        "selection_accepted": False,
        "execution_authorized": False,
        "cutover_evidence_eligible": False,
    }
    validator.validate(base)
    validator.validate({
        **base,
        "domain": "workspace",
        "gate_failure": "no_proposal",
        "commit_sha256": "",
        "commit_present": False,
        "branch_matches_identity": False,
        "edit_count": 0,
        "edit_component_counts": {name: 0 for name in counts},
        "diff_path_present": False,
        "mechanism_present": False,
        "diagnostic_bytes": 29,
    })
    validator.validate({
        **base,
        "domain": "eng",
        "gate_failure": "critic_reject",
        "commit_sha256": "",
        "commit_present": False,
        "branch_matches_identity": False,
        "diff_path_present": True,
        "mechanism_present": False,
        "diagnostic_bytes": 25,
    })
    for invalid in (
        {**base, "selection_accepted": True},
        {**base, "execution_authorized": True},
        {**base, "cutover_evidence_eligible": True},
        {**base, "commit_present": False},
        {**base, "branch_matches_identity": False},
        {**base, "edit_component_counts": {"private_component": 1}},
        {**base, "diagnostic": "private text"},
        {
            **base,
            "gate_failure": "smoke_fail",
            "commit_sha256": "",
            "commit_present": False,
            "branch_matches_identity": False,
            "diff_path_present": True,
            "mechanism_present": False,
            "diagnostic_bytes": 1,
            "edit_count": 0,
            "edit_component_counts": {name: 0 for name in counts},
        },
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)
