import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def _validator(name):
    schema = json.loads((SCHEMA_DIRECTORY / name).read_text(encoding="utf-8"))
    return validator_for(schema)(schema)


def test_per_question_artifact_checkpoint_is_only_present_after_approval():
    validator = _validator("decision-artifact-v1.schema.json")
    base = {
        "schema_version": "threadforge.decision-artifact.v1",
        "id": "calibration-one",
        "revision": 1,
        "status": "candidate",
        "model": "local-model",
        "question_key": "a" * 16,
        "level": "L1",
        "calibration_examples": 8,
        "content_sha256": "b" * 64,
        "content": {},
        "created_at": "2026-10-08T00:00:00Z",
        "updated_at": "2026-10-08T00:00:00Z",
    }
    validator.validate(base)
    validator.validate({
        **base,
        "revision": 2,
        "status": "active",
        "model_checkpoint_sha256": "c" * 64,
    })
    validator.validate({
        **base,
        "revision": 3,
        "status": "retired",
        "model_checkpoint_sha256": "c" * 64,
    })
    with pytest.raises(ValidationError):
        validator.validate({**base, "model_checkpoint_sha256": "c" * 64})
    with pytest.raises(ValidationError):
        validator.validate({
            **base,
            "revision": 2,
            "status": "active",
            "model_checkpoint_sha256": "not-a-digest",
        })
    with pytest.raises(ValidationError):
        validator.validate({
            **base,
            "revision": 2,
            "status": "rejected",
            "model_checkpoint_sha256": "c" * 64,
        })


def test_artifact_batch_result_reconciles_checkpoint_verification_claims():
    validator = _validator("decision-batch-result-v1.schema.json")
    base = {
        "schema_version": "threadforge.decision-batch-result.v1",
        "question_id": "question-one",
        "question_key": "a" * 16,
        "count": 1,
        "level": "L1",
        "decisions": [{"kind": "choice", "level": "L1", "answer": "unit", "confidence": 0.8}],
        "backend": "local-model",
        "remote_opt_in": False,
        "backend_stats": {
            "backend_calls": 1,
            "flat_prompts": 1,
            "shared_groups": 0,
            "shared_prompts": 0,
            "adaptive_items": 0,
            "adaptive_shifts_total": 0,
        },
    }
    validator.validate(base)
    validator.validate({
        **base,
        "human_review_required": True,
        "model_environment_verified": True,
        "model_checkpoint_sha256": "c" * 64,
        "execution_authorized": False,
        "cutover_evidence_eligible": False,
    })
    with pytest.raises(ValidationError):
        validator.validate({
            **base,
            "human_review_required": True,
            "model_environment_verified": True,
            "execution_authorized": False,
            "cutover_evidence_eligible": False,
        })
    with pytest.raises(ValidationError):
        validator.validate({
            **base,
            "model_environment_verified": False,
            "model_checkpoint_sha256": "c" * 64,
        })
