import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


def test_ci_public_summary_contract_is_aggregate_only_and_rejects_local_paths():
    schema_path = Path(__file__).resolve().parents[2] / "schema" / "ci-public-summary-v1.schema.json"
    schema = json.loads(schema_path.read_text(encoding="utf-8"))
    summary = {
        "schema_version": "threadforge.ci-public-summary.v1",
        "harness": "claude_code",
        "status": "passed",
        "telemetry_required": True,
        "validation_status": "passed",
        "total_events": 4,
        "matched_events": 3,
        "malformed_rows": 0,
        "minimum_events": 1,
    }
    Draft202012Validator(schema).validate(summary)
    with pytest.raises(ValidationError):
        Draft202012Validator(schema).validate({**summary, "artifact_dir": "C:/runner/private"})
    with pytest.raises(ValidationError):
        Draft202012Validator(schema).validate({**summary, "runtime_jsonl": "runtime.jsonl"})
