import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from referencing import Registry, Resource
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def _validator_registry():
    documents = {
        path.name: json.loads(path.read_text(encoding="utf-8"))
        for path in SCHEMA_DIRECTORY.glob("*.json")
    }
    resources = [
        (document["$id"], Resource.from_contents(document))
        for document in documents.values()
        if "$id" in document
    ]
    return documents, Registry().with_resources(resources)


def test_task_claim_event_and_read_contracts_are_local_and_non_authorizing():
    documents, registry = _validator_registry()

    def validate(name, instance):
        schema = documents[name]
        validator_for(schema)(schema, registry=registry).validate(instance)

    claim = {
        "schema_version": "threadforge.task-claim.v1",
        "id": "claim-api",
        "project_id": "project-a",
        "task_id": "task-a",
        "owner": "agent-a",
        "scopes": [{"kind": "path", "value": "src/api/**"}],
        "task_revision": 1,
        "baseline_sha256": "a" * 64,
        "lease_expires_at": "2026-10-19T11:00:00Z",
        "status": "active",
        "revision": 1,
        "created_at": "2026-10-19T10:00:00Z",
        "updated_at": "2026-10-19T10:00:00Z",
    }
    event = {
        "schema_version": "threadforge.task-claim-event.v1",
        "action": "acquired",
        "expected_revision": 0,
        "claim": claim,
    }
    validate("task-claim-v1.schema.json", claim)
    validate("task-claim-event-v1.schema.json", event)
    validate("continuity-v1.schema.json", {
        "schema_version": "threadforge.continuity.v1",
        "id": "claim-event-a",
        "project_id": "project-a",
        "task_id": "task-a",
        "kind": "task_claim",
        "actor": "agent-a",
        "summary": "Task claim acquired",
        "task_claim": event,
        "created_at": "2026-10-19T10:00:00Z",
    })
    validate("task-claim-read-v1.schema.json", {
        "schema_version": "threadforge.task-claim-read.v1",
        "task_id": "task-a",
        "claims": [{
            "claim": claim,
            "lease_status": "active",
            "task_revision_status": "current",
            "baseline_status": "unverified",
            "execution_authorized": False,
        }],
        "execution_authorized": False,
    })

    with pytest.raises(ValidationError):
        validate("task-claim-read-v1.schema.json", {
            "schema_version": "threadforge.task-claim-read.v1",
            "task_id": "task-a",
            "claims": [],
            "execution_authorized": True,
        })
    with pytest.raises(ValidationError):
        validate("continuity-v1.schema.json", {
            "schema_version": "threadforge.continuity.v1",
            "id": "claim-event-b",
            "project_id": "project-a",
            "task_id": "task-a",
            "kind": "task_claim",
            "actor": "agent-a",
            "summary": "Claim must use its typed payload",
            "created_at": "2026-10-19T10:00:00Z",
        })
    with pytest.raises(ValidationError):
        validate("task-claim-event-v1.schema.json", {
            **event,
            "raw_prompt": "never belongs in a claim event",
        })
