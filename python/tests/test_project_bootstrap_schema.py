import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_DIRECTORY = Path(__file__).resolve().parents[2] / "schema"


def load_validator(name: str):
    schema = json.loads((SCHEMA_DIRECTORY / name).read_text(encoding="utf-8"))
    return validator_for(schema)(schema)


def test_project_bootstrap_plan_is_explicit_and_non_mutating():
    validator = load_validator("project-bootstrap-plan-v1.schema.json")
    plan = {
        "schema_version": "threadforge.project-bootstrap-plan.v1",
        "status": "planned",
        "project_root_sha256": "a" * 64,
        "hosts": [
            {"host": "claude-code", "config_path": ".mcp.json", "action": "install_or_match"},
            {"host": "command-code", "config_path": ".mcp.json", "action": "install_or_match"},
            {"host": "github-copilot", "config_path": ".mcp.json", "action": "install_or_match"},
            {"host": "qoder", "config_path": ".mcp.json", "action": "install_or_match"},
            {"host": "zcode", "config_path": ".zcode/config.json", "action": "install_or_match"},
            {"host": "kimi-code", "config_path": ".kimi-code/mcp.json", "action": "install_or_match"},
            {"host": "qwen-code", "config_path": ".qwen/settings.json", "action": "install_or_match"},
            {"host": "chatgpt-desktop-codex", "config_path": ".codex/config.toml", "action": "install_or_match"},
            {"host": "codex-ide", "config_path": ".codex/config.toml", "action": "install_or_match"},
        ],
        "mutates_files": False,
        "configuration_preflight": "not_read",
        "host_trust_changed": False,
        "permissions_changed": False,
        "telemetry_enabled": False,
        "limitations": ["No config was read.", "Install may refuse unowned entries.", "Other hosts use manual fallback."],
    }
    validator.validate(plan)
    with pytest.raises(ValidationError):
        validator.validate({**plan, "mutates_files": True})
    with pytest.raises(ValidationError):
        validator.validate({**plan, "hosts": [{**plan["hosts"][0], "host": "unknown-host"}]})


def test_project_bootstrap_result_exposes_partial_effects_and_denies_authority():
    validator = load_validator("project-bootstrap-result-v1.schema.json")
    result = {
        "schema_version": "threadforge.project-bootstrap-result.v1",
        "action": "install",
        "status": "partial",
        "hosts": [
            {"host": "claude-code", "config_path": ".mcp.json", "result": "installed"},
            {"host": "github-copilot", "config_path": ".mcp.json", "result": "unchanged"},
            {"host": "qoder", "config_path": ".mcp.json", "result": "unchanged"},
            {"host": "zcode", "config_path": ".zcode/config.json", "result": "unchanged"},
            {"host": "kimi-code", "config_path": ".kimi-code/mcp.json", "result": "unchanged"},
            {"host": "qwen-code", "config_path": ".qwen/settings.json", "result": "installed"},
            {
                "host": "cursor",
                "config_path": ".cursor/mcp.json",
                "result": "failed",
                "failure_code": "configuration_conflict_or_unavailable",
            },
        ],
        "atomic": False,
        "partial_changes_possible": True,
        "host_trust_changed": False,
        "permissions_changed": False,
        "telemetry_enabled": False,
        "guidance_only": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "host_trust_changed": True})
    with pytest.raises(ValidationError):
        validator.validate({**result, "partial_changes_possible": False})
    with pytest.raises(ValidationError):
        validator.validate({**result, "hosts": [{**result["hosts"][1], "failure_code": "raw-secret"}]})
    with pytest.raises(ValidationError):
        validator.validate({**result, "hosts": [{**result["hosts"][1], "failure_code": None}]})
    with pytest.raises(ValidationError):
        validator.validate({**result, "hosts": [{**result["hosts"][0], "failure_code": "configuration_conflict_or_unavailable"}]})
