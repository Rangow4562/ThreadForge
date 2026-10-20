import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA = Path(__file__).resolve().parents[2] / "schema" / "anyjev-head-study-result-v1.schema.json"


def _payload():
    kinds = ["diff_means", "lda", "ridge", "rrr"]
    methods = ["raw", "L1", "L1@self", "L0-perm", "L0-perm+T", "L0-perm+T@self"]
    head_methods = []
    for kind in kinds:
        for average in ("", "/avg"):
            for label in ("", "@self"):
                head_methods.append(f"head:{kind}{average}{label}")
    methods = sorted(methods + head_methods)
    metrics = {"acc": 0.5, "ece": 0.1, "brier": 0.4, "nll": 0.8, "flip": 0.2}
    pooled = [{"method": method, "metrics": {**metrics, "n": 4}} for method in methods]
    rows = [{"method": method, "metrics": metrics} for method in methods]
    heads = []
    for method in head_methods:
        base = method.removeprefix("head:").removesuffix("@self").removesuffix("/avg")
        parameters = {}
        if base == "lda":
            parameters = {"shrinkage": 0.5}
        elif base == "ridge":
            parameters = {"lambda": 1.0}
        elif base == "rrr":
            parameters = {"lambda": 1.0, "rank": 2}
        heads.append({
            "method": method,
            "layer": 10,
            "parameters": parameters,
            "temperature": 1.0,
            "label_count": 2,
            "oof_nll": 0.8,
            "oof_accuracy": 0.5,
        })
    return {
        "schema_version": "threadforge.anyjev-head-study-result.v1",
        "source_kind": "per_question_head_study",
        "model_sha256": "a" * 64,
        "seed": 7,
        "layers": [-1, -5],
        "kinds": kinds,
        "labels": ["gold", "self"],
        "question_count": 2,
        "test_examples": 4,
        "calibration_examples": 4,
        "pooled": pooled,
        "questions": [
            {
                "question_sha256": "b" * 64,
                "kind": "choice",
                "option_count": 2,
                "test_examples": 2,
                "calibration_examples": 2,
                "rows": rows,
                "heads": heads,
            }
        ],
        "omitted_fields": [
            "model_label", "question_keys", "study_date", "environment_details", "hardware_details",
            "git_commit", "source_paths", "raw_question_content",
        ],
        "human_review_required": True,
        "execution_verified": False,
        "cutover_evidence_eligible": False,
    }


def test_anyjev_head_study_schema_accepts_review_only_projection():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    Draft202012Validator(schema).validate(_payload())


def test_anyjev_head_study_schema_rejects_authority_and_unknown_fields():
    validator = Draft202012Validator(json.loads(SCHEMA.read_text(encoding="utf-8")))
    payload = _payload()
    with pytest.raises(ValidationError):
        validator.validate({**payload, "execution_verified": True})
    with pytest.raises(ValidationError):
        validator.validate({**payload, "raw_model": "private-model"})

