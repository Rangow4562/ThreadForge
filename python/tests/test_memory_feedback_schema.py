import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError
from referencing import Registry, Resource


SCHEMA_DIR = Path(__file__).resolve().parents[2] / "schema"


def _schema(name):
    return json.loads((SCHEMA_DIR / name).read_text(encoding="utf-8"))


def _feedback():
    return {
        "schema_version": "threadforge.memory-feedback.v1",
        "id": "memory-feedback-0123456789abcdef0123456789abcdef",
        "project_id": "repo:example",
        "task_id": "task-123",
        "target_kind": "procedure",
        "target_id": "build-recovery",
        "target_revision": 2,
        "exposure": "used",
        "utility": "helpful",
        "task_result": "passed",
        "harness": "codex-cli",
        "actor": "local-user",
        "evidence_class": "self_reported",
        "cutover_evidence_eligible": False,
        "execution_authorized": False,
        "created_at": "2025-01-02T12:00:00Z",
    }


def _registry():
    registry = Registry()
    for path in SCHEMA_DIR.glob("*.json"):
        schema = _schema(path.name)
        if "$id" in schema:
            registry = registry.with_resource(schema["$id"], Resource.from_contents(schema))
    return registry


def _validator(name):
    return Draft202012Validator(_schema(name), registry=_registry())


def test_memory_feedback_schema_is_typed_and_non_authorizing():
    validator = _validator("memory-feedback-v1.schema.json")
    validator.validate(_feedback())

    shown_not_used = {**_feedback(), "exposure": "shown_not_used", "utility": "not_assessed"}
    validator.validate(shown_not_used)

    invalid = [
        {**_feedback(), "cutover_evidence_eligible": True},
        {**_feedback(), "execution_authorized": True},
        {**_feedback(), "evidence_class": "verified"},
        {**_feedback(), "exposure": "shown_not_used", "utility": "helpful"},
        {**_feedback(), "prompt": "raw prompt must not be stored"},
    ]
    for value in invalid:
        with pytest.raises(ValidationError):
            validator.validate(value)


def test_memory_feedback_result_list_and_summary_schemas_validate():
    feedback = _feedback()
    _validator("memory-feedback-result-v1.schema.json").validate(
        {"schema_version": "threadforge.memory-feedback-result.v1", "status": "recorded", "sequence": 1, "feedback": feedback}
    )
    _validator("memory-feedback-list-v1.schema.json").validate(
        {
            "schema_version": "threadforge.memory-feedback-list.v1",
            "project_id": "repo:example",
            "total": 1,
            "limit": 50,
            "offset": 0,
            "feedback": [feedback],
            "evidence_class": "self_reported",
            "cutover_evidence_eligible": False,
            "execution_authorized": False,
        }
    )
    _validator("memory-feedback-summary-v1.schema.json").validate(
        {
            "schema_version": "threadforge.memory-feedback-summary.v1",
            "status": "complete",
            "project_id": "repo:example",
            "records": 1,
            "groups": [
                {
                    "target_kind": "procedure",
                    "target_id": "build-recovery",
                    "target_revision": 2,
                    "harness": "codex-cli",
                    "exposure": "used",
                    "utility": "helpful",
                    "task_result": "passed",
                    "count": 1,
                }
            ],
            "evidence_class": "self_reported",
            "cutover_evidence_eligible": False,
            "execution_authorized": False,
        }
    )
