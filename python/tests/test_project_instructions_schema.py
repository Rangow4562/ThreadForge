import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def load_validator(name: str):
    schema = json.loads((SCHEMA_DIRECTORY / name).read_text(encoding="utf-8"))
    return validator_for(schema)(schema)


def test_project_instruction_plan_groups_shared_paths_without_authority():
    validator = load_validator("project-instructions-plan-v1.schema.json")
    plan = {
        "schema_version": "threadforge.project-instructions-plan.v1",
        "status": "planned",
        "project_root_sha256": "a" * 64,
        "targets": [
            {"hosts": ["command-code", "cursor", "antigravity", "codex-cli", "chatgpt-desktop-codex", "codex-ide", "opencode", "zcode", "kimi-code", "qoder", "deepseek-harness"], "path": "AGENTS.md", "action": "append_or_match"},
            {"hosts": ["claude-code"], "path": "CLAUDE.md", "action": "append_or_match"},
            {"hosts": ["github-copilot"], "path": ".github/copilot-instructions.md", "action": "append_or_match"},
            {"hosts": ["gemini-cli"], "path": "GEMINI.md", "action": "append_or_match"},
            {"hosts": ["qwen-code"], "path": "QWEN.md", "action": "append_or_match"},
            {"hosts": ["goose"], "path": ".goosehints", "action": "append_or_match"},
        ],
        "mutates_files": False,
        "host_trust_changed": False,
        "permissions_changed": False,
        "execution_authorized": False,
        "limitations": ["Plan does not read files.", "Host loading is not verified.", "Guidance is not authorization."],
    }
    validator.validate(plan)
    with pytest.raises(ValidationError):
        validator.validate({**plan, "execution_authorized": True})
    with pytest.raises(ValidationError):
        validator.validate({**plan, "targets": [{**plan["targets"][0], "path": "../outside.md"}]})


def test_project_instruction_result_reports_partial_changes_and_managed_failure():
    validator = load_validator("project-instructions-result-v1.schema.json")
    result = {
        "schema_version": "threadforge.project-instructions-result.v1",
        "action": "install",
        "status": "partial",
        "targets": [
            {"hosts": ["cursor"], "path": "AGENTS.md", "result": "installed"},
            {"hosts": ["gemini-cli"], "path": "GEMINI.md", "result": "failed", "failure_code": "instruction_conflict_or_unavailable"},
            {"hosts": ["github-copilot"], "path": ".github/copilot-instructions.md", "result": "installed"},
            {"hosts": ["qwen-code"], "path": "QWEN.md", "result": "unchanged"},
        ],
        "atomic": False,
        "partial_changes_possible": True,
        "host_trust_changed": False,
        "permissions_changed": False,
        "guidance_only": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "partial_changes_possible": False})
    with pytest.raises(ValidationError):
        validator.validate({**result, "host_trust_changed": True})
