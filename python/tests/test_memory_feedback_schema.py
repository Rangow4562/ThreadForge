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
    validator.validate({**_feedback(), "retrieval_receipt_id": "retrieval-0123456789abcdef0123456789abcdef"})

    shown_not_used = {**_feedback(), "exposure": "shown_not_used", "utility": "not_assessed"}
    validator.validate(shown_not_used)

    invalid = [
        {**_feedback(), "cutover_evidence_eligible": True},
        {**_feedback(), "execution_authorized": True},
        {**_feedback(), "evidence_class": "verified"},
        {**_feedback(), "exposure": "shown_not_used", "utility": "helpful"},
        {**_feedback(), "retrieval_receipt_id": "../receipt"},
        {**_feedback(), "prompt": "raw prompt must not be stored"},
    ]
    for value in invalid:
        with pytest.raises(ValidationError):
            validator.validate(value)


def test_memory_feedback_v2_accepts_only_redacted_failure_digest_on_failed_tasks():
    validator = _validator("memory-feedback-v2.schema.json")
    failed = {
        **_feedback(),
        "schema_version": "threadforge.memory-feedback.v2",
        "task_result": "failed",
        "failure_signature_sha256": "a" * 64,
    }
    validator.validate(failed)
    validator.validate({**failed, "failure_signature_sha256": "b" * 64})
    for invalid in (
        {**failed, "task_result": "passed"},
        {**failed, "failure_signature_sha256": "A" * 64},
        {**failed, "failure_signature_sha256": "raw failure text"},
        {**failed, "failure_text": "do not store raw failure text"},
        {**failed, "cutover_evidence_eligible": True},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)


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
    legacy_summary = {
        "schema_version": "threadforge.memory-feedback-summary.v1",
        "status": "complete",
        "project_id": "repo:example",
        "records": 1,
        "retrieval_receipt_linked": 0,
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
    _validator("memory-feedback-summary-v1.schema.json").validate(legacy_summary)

    current_summary = {
        **legacy_summary,
        "schema_version": "threadforge.memory-feedback-summary.v2",
        "groups": [{**legacy_summary["groups"][0], "distinct_task_count": 1}],
    }
    _validator("memory-feedback-summary-v2.schema.json").validate(current_summary)
    with pytest.raises(ValidationError):
        _validator("memory-feedback-summary-v2.schema.json").validate(legacy_summary)

    failed_v2 = {
        **feedback,
        "schema_version": "threadforge.memory-feedback.v2",
        "task_result": "failed",
        "failure_signature_sha256": "d" * 64,
    }
    _validator("memory-feedback-result-v2.schema.json").validate(
        {
            "schema_version": "threadforge.memory-feedback-result.v2",
            "status": "recorded",
            "sequence": 2,
            "feedback": failed_v2,
        }
    )
    _validator("memory-feedback-list-v2.schema.json").validate(
        {
            "schema_version": "threadforge.memory-feedback-list.v2",
            "project_id": "repo:example",
            "total": 2,
            "limit": 50,
            "offset": 0,
            "feedback": [feedback, failed_v2],
            "evidence_class": "self_reported",
            "cutover_evidence_eligible": False,
            "execution_authorized": False,
        }
    )
    _validator("memory-feedback-failures-v1.schema.json").validate(
        {
            "schema_version": "threadforge.memory-feedback-failures.v1",
            "status": "complete",
            "project_id": "repo:example",
            "records_scanned": 2,
            "signature_records": 1,
            "groups": [
                {
                    "target_kind": "procedure",
                    "target_id": "build-recovery",
                    "target_revision": 2,
                    "harness": "codex-cli",
                    "exposure": "used",
                    "utility": "helpful",
                    "failure_signature_sha256": "d" * 64,
                    "feedback_count": 1,
                    "distinct_task_count": 1,
                    "repeated_after_first_task_count": 0,
                }
            ],
            "evidence_class": "self_reported",
            "cutover_evidence_eligible": False,
            "execution_authorized": False,
        }
    )


def test_memory_retrieval_receipt_schema_is_scoped_and_does_not_claim_delivery():
    receipt = {
        "schema_version": "threadforge.memory-retrieval-receipt.v1",
        "id": "retrieval-0123456789abcdef0123456789abcdef",
        "project_id": "repo:example",
        "task_id": "task-123",
        "target_kind": "procedure",
        "target_id": "build-recovery",
        "target_revision": 2,
        "tool": "threadforge_procedure_get",
        "freshness": "verified",
        "guidance_included": True,
        "observation_boundary": "mcp_handler_result_prepared",
        "created_at": "2026-10-28T12:00:00Z",
    }
    validator = _validator("memory-retrieval-receipt-v1.schema.json")
    validator.validate(receipt)
    validator.validate({**receipt, "freshness": "stale", "guidance_included": False})
    for invalid in (
        {**receipt, "freshness": "stale"},
        {**receipt, "delivered_to_model": True},
        {**receipt, "procedure_body": "private instructions"},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)


def test_memory_retrieval_receipt_v2_records_project_memory_read_status():
    receipt = {
        "schema_version": "threadforge.memory-retrieval-receipt.v2",
        "id": "retrieval-5123456789abcdef0123456789abcdef",
        "project_id": "repo:example",
        "task_id": "task-123",
        "session_id": "session-123",
        "target_kind": "fact",
        "target_id": "build-invariant",
        "target_revision": 2,
        "tool": "threadforge_project_memory_read_v2",
        "freshness": "verified",
        "result_status": "complete",
        "guidance_included": True,
        "observation_boundary": "mcp_handler_result_prepared",
        "created_at": "2026-10-28T12:00:00Z",
    }
    validator = _validator("memory-retrieval-receipt-v2.schema.json")
    validator.validate(receipt)
    validator.validate({**receipt, "freshness": "stale", "result_status": "stale", "guidance_included": False})
    validator.validate({**receipt, "freshness": "unverified", "result_status": "incomplete"})
    for invalid in (
        {**receipt, "freshness": "stale"},
        {**receipt, "result_status": "conflicting"},
        {**receipt, "guidance_included": False},
        {**receipt, "record": {"content": "memory body"}},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)


def test_memory_retrieval_receipt_v3_records_task_scoped_hook_preparation_only():
    receipt = {
        "schema_version": "threadforge.memory-retrieval-receipt.v3",
        "id": "retrieval-8123456789abcdef0123456789abcdef",
        "project_id": "repo:example",
        "task_id": "task-123",
        "target_kind": "fact",
        "target_id": "build-invariant",
        "target_revision": 2,
        "tool": "threadforge_memory_inject",
        "host": "codex-cli",
        "freshness": "verified",
        "guidance_included": True,
        "observation_boundary": "hook_response_prepared",
        "created_at": "2026-10-28T12:00:00Z",
    }
    validator = _validator("memory-retrieval-receipt-v3.schema.json")
    validator.validate(receipt)
    validator.validate({**receipt, "freshness": "stale", "guidance_included": False})
    for invalid in (
        {**receipt, "freshness": "stale"},
        {**receipt, "model_exposed": True},
        {**receipt, "prompt": "private user prompt"},
        {**receipt, "host": "unknown-host"},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)


def test_memory_retrieval_receipt_list_accepts_all_receipt_versions_without_private_content():
    result = {
        "schema_version": "threadforge.memory-retrieval-receipt-list.v1",
        "project_id": "repo:example",
        "task_id": "task-123",
        "total": 1,
        "limit": 50,
        "offset": 0,
        "receipts": [{
            "schema_version": "threadforge.memory-retrieval-receipt.v3",
            "id": "retrieval-9123456789abcdef0123456789abcdef",
            "project_id": "repo:example",
            "task_id": "task-123",
            "target_kind": "fact",
            "target_id": "build-invariant",
            "target_revision": 2,
            "tool": "threadforge_memory_inject",
            "host": "codex-cli",
            "freshness": "verified",
            "guidance_included": True,
            "observation_boundary": "hook_response_prepared",
            "created_at": "2026-10-28T12:00:00Z",
        }],
    }
    validator = _validator("memory-retrieval-receipt-list-v1.schema.json")
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "private_prompt": "must not be serialized"})


def test_task_resume_receipts_and_feedback_bind_revision_without_capturing_context():
    receipt = {
        "schema_version": "threadforge.memory-retrieval-receipt.v4",
        "id": "retrieval-a123456789abcdef0123456789abcdef",
        "project_id": "repo:example",
        "task_id": "task-123",
        "session_id": "session-123",
        "target_kind": "task_context",
        "target_id": "task-resume-context",
        "target_revision": 1,
        "task_revision": 4,
        "tool": "threadforge_task_resume_context_with_receipt",
        "freshness": "snapshot",
        "result_status": "available",
        "guidance_included": True,
        "observation_boundary": "mcp_handler_result_prepared",
        "created_at": "2026-11-01T12:00:00Z",
    }
    _validator("memory-retrieval-receipt-v4.schema.json").validate(receipt)
    context = {
        "schema_version": "threadforge.task-resume-context.v1",
        "status": "available",
        "task": {
            "schema_version": "threadforge.continuity.v1", "id": "task-123", "project_id": "repo:example",
            "owner": "local-user", "title": "Resume", "objective": "Continue safely",
            "acceptance_criteria": ["Review the next step"], "status": "open", "revision": 4,
            "created_at": "2026-11-01T11:00:00Z", "updated_at": "2026-11-01T12:00:00Z",
        },
        "session_choices": [], "session_choices_truncated": False, "entries": [], "entries_truncated": False,
        "warnings": [], "action_ready": False, "guidance_only": True, "execution_authorized": False,
    }
    _validator("task-resume-context-with-receipt-v1.schema.json").validate(
        {
            "schema_version": "threadforge.task-resume-context-with-receipt.v1",
            "context": context,
            "retrieval_receipt": receipt,
        }
    )

    feedback = {
        **_feedback(),
        "schema_version": "threadforge.memory-feedback.v3",
        "target_kind": "task_context",
        "target_id": "task-resume-context",
        "target_revision": 1,
        "retrieval_receipt_id": receipt["id"],
        "task_result": "failed",
        "failure_signature_sha256": "c" * 64,
    }
    _validator("memory-feedback-v3.schema.json").validate(feedback)
    for invalid in (
        {**feedback, "retrieval_receipt_id": ""},
        {**feedback, "target_id": "task-123"},
        {**receipt, "task_revision": 0},
        {**receipt, "result_status": "session_selection_required", "guidance_included": True},
    ):
        schema = "memory-feedback-v3.schema.json" if "schema_version" in invalid and invalid["schema_version"].endswith("feedback.v3") else "memory-retrieval-receipt-v4.schema.json"
        with pytest.raises(ValidationError):
            _validator(schema).validate(invalid)

    _validator("memory-feedback-result-v3.schema.json").validate(
        {"schema_version": "threadforge.memory-feedback-result.v3", "status": "recorded", "sequence": 1, "feedback": feedback}
    )
    _validator("memory-feedback-list-v3.schema.json").validate(
        {
            "schema_version": "threadforge.memory-feedback-list.v3",
            "project_id": "repo:example", "total": 1, "limit": 50, "offset": 0,
            "feedback": [feedback], "evidence_class": "self_reported",
            "cutover_evidence_eligible": False, "execution_authorized": False,
        }
    )
    _validator("memory-feedback-summary-v3.schema.json").validate(
        {
            "schema_version": "threadforge.memory-feedback-summary.v3", "status": "complete",
            "project_id": "repo:example", "records": 1, "retrieval_receipt_linked": 1,
            "groups": [{
                "target_kind": "task_context", "target_id": "task-resume-context", "target_revision": 1,
                "harness": "codex-cli", "exposure": "used", "utility": "harmful", "task_result": "failed",
                "count": 1, "distinct_task_count": 1,
            }],
            "evidence_class": "self_reported", "cutover_evidence_eligible": False, "execution_authorized": False,
        }
    )
    _validator("memory-feedback-failures-v2.schema.json").validate(
        {
            "schema_version": "threadforge.memory-feedback-failures.v2", "status": "complete",
            "project_id": "repo:example", "records_scanned": 1, "signature_records": 1,
            "groups": [{
                "target_kind": "task_context", "target_id": "task-resume-context", "target_revision": 1,
                "harness": "codex-cli", "exposure": "used", "utility": "harmful",
                "failure_signature_sha256": "c" * 64, "feedback_count": 1,
                "distinct_task_count": 1, "repeated_after_first_task_count": 0,
            }],
            "evidence_class": "self_reported", "cutover_evidence_eligible": False, "execution_authorized": False,
        }
    )
    _validator("memory-retrieval-receipt-list-v2.schema.json").validate(
        {
            "schema_version": "threadforge.memory-retrieval-receipt-list.v2", "project_id": "repo:example",
            "task_id": "task-123", "total": 1, "limit": 50, "offset": 0, "receipts": [receipt],
        }
    )


def test_context_response_schema_accepts_bounded_mixed_task_scoped_receipts():
    response = {
        "schema_version": "threadforge.context-response.v2",
        "status": "incomplete",
        "action_ready": False,
        "procedures": [{
            "id": "context-procedure", "revision": 2, "title": "Build procedure",
            "freshness": "unverified", "guidance": ["Run the focused test."],
        }],
        "memory": [{
            "id": "context-fact", "kind": "fact", "revision": 3, "title": "Build fact",
            "freshness": "unverified", "content": {"fact": "use the build cache"}, "evidence": [],
        }],
        "gaps": ["source freshness is unverified"],
        "retrieval_receipts": [
            {
                "schema_version": "threadforge.memory-retrieval-receipt.v1",
                "id": "retrieval-6123456789abcdef0123456789abcdef",
                "project_id": "repo:example", "task_id": "task-context", "session_id": "session-context",
                "target_kind": "procedure", "target_id": "context-procedure", "target_revision": 2,
                "tool": "threadforge_context_get", "freshness": "unverified", "guidance_included": True,
                "observation_boundary": "mcp_handler_result_prepared", "created_at": "2026-10-28T12:00:00Z",
            },
            {
                "schema_version": "threadforge.memory-retrieval-receipt.v2",
                "id": "retrieval-7123456789abcdef0123456789abcdef",
                "project_id": "repo:example", "task_id": "task-context", "session_id": "session-context",
                "target_kind": "fact", "target_id": "context-fact", "target_revision": 3,
                "tool": "threadforge_context_get", "freshness": "unverified", "result_status": "incomplete",
                "guidance_included": True, "observation_boundary": "mcp_handler_result_prepared",
                "created_at": "2026-10-28T12:00:00Z",
            },
        ],
    }
    _validator("context-response-v2.schema.json").validate(response)

    with pytest.raises(ValidationError):
        _validator("context-response-v2.schema.json").validate({
            **response,
            "retrieval_receipts": response["retrieval_receipts"] * 17,
        })
    with pytest.raises(ValidationError):
        _validator("context-response-v2.schema.json").validate({
            **response,
            "memory": [{**response["memory"][0], "freshness": "stale"}],
        })
