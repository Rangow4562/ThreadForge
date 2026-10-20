import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA_PATH = Path(__file__).resolve().parents[2] / "schema" / "beacon-hook-log-summary-v1.schema.json"


def test_beacon_hook_log_summary_is_bounded_metadata_and_non_authorizing():
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    summary = {
        "schema_version": "threadforge.beacon-hook-log-summary.v1",
        "source_format": "jsonl",
        "kind": "hook_log_summary",
        "harness": "claude_code",
        "entry_count": 2,
        "level_counts": {"debug": 0, "error": 1, "info": 1, "other": 0, "warn": 0},
        "first_day": "2026-10-07",
        "last_day": "2026-10-08",
        "omitted_fields": ["arbitrary_fields", "hook_names", "message", "raw_source_lines", "session_id"],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(summary)
    assert sum(summary["level_counts"].values()) == summary["entry_count"]

    for invalid in (
        {**summary, "execution_authorized": True},
        {**summary, "entry_count": -1},
        {**summary, "level_counts": {**summary["level_counts"], "credential": 1}},
        {**summary, "message": "private source message"},
        {**summary, "harness": "unknown_host"},
        {**summary, "entry_count": 0},
    ):
        with pytest.raises(ValidationError):
            validator.validate(invalid)
