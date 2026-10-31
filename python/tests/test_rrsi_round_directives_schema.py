import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def test_rrsi_round_directives_schema_is_bounded_and_review_only():
    schema = json.loads((SCHEMA_DIRECTORY / "rrsi-round-directives-v1.schema.json").read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)
    tried = {name: int(name in {"prompt", "skill"}) for name in (
        "prompt", "control_flow", "config", "output_plumbing", "context_mgmt",
        "client_tool", "skill", "memory", "subagent",
    )}
    untried = {name: 1 - count for name, count in tried.items()}
    pruned = {name: int(name == "prompt") for name in tried}
    base = {
        "schema_version": "threadforge.rrsi-round-directives.v1",
        "domain": "coding",
        "round_index": 0,
        "round_sha256": "a" * 64,
        "artifact_path_sha256": "b" * 64,
        "edit_budget": 2,
        "stall_flag": 1,
        "exploration_slots": 1,
        "noise_band": 0.02,
        "incumbent_score": 0.5,
        "tried_components": tried,
        "untried_components": untried,
        "pruned_components": pruned,
        "accepted_edits_in_prune_set": 1,
        "measured_prune_components": 0,
        "source_sha256": "c" * 64,
        "source_bytes": 543,
        "human_review_required": True,
        "execution_authorized": False,
        "cutover_evidence_eligible": False,
    }
    validator.validate(base)
    for invalid in (
        {**base, "execution_authorized": True},
        {**base, "cutover_evidence_eligible": True},
        {**base, "pruned_components": {"private": 1}},
        {**base, "edit_budget": 81},
        {**base, "hypothesis": "private proposer text"},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)
