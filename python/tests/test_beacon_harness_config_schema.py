import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


SCHEMA = Path(__file__).resolve().parents[2] / "schema" / "beacon-harness-config-v1.schema.json"


def test_beacon_claude_hook_projection_is_versioned_and_non_authorizing():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "claude_code",
        "beacon_hooks_configured": True,
        "hook_events": ["SessionStart", "PostToolUseFailure"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    Draft202012Validator(schema).validate(result)
    with pytest.raises(ValidationError):
        Draft202012Validator(schema).validate({**result, "execution_authorized": True})
    with pytest.raises(ValidationError):
        Draft202012Validator(schema).validate({**result, "hook_commands": ["beacon-hooks --platform claude"]})


def test_beacon_qwen_hook_projection_uses_same_redacted_non_authorizing_contract():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "qwen_code",
        "beacon_hooks_configured": True,
        "hook_events": ["SessionStart", "PermissionRequest"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    Draft202012Validator(schema).validate(result)
    with pytest.raises(ValidationError):
        Draft202012Validator(schema).validate({**result, "harness": "qwen"})
    with pytest.raises(ValidationError):
        Draft202012Validator(schema).validate({**result, "execution_authorized": True})


def test_beacon_factory_hook_projection_uses_only_supported_event_names():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "factory",
        "beacon_hooks_configured": True,
        "hook_events": ["SessionStart", "PostToolUse"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "hook_events": ["UnsupportedEvent"]})


def test_beacon_cursor_hook_projection_supports_versioned_camel_case_event_names():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "cursor",
        "beacon_hooks_configured": True,
        "hook_events": ["beforeSubmitPrompt", "afterFileEdit"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})


def test_beacon_codex_hook_projection_uses_only_documented_non_authorizing_events():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "codex_cli",
        "beacon_hooks_configured": True,
        "hook_events": ["SessionStart", "Stop"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "harness": "codex"})
    with pytest.raises(ValidationError):
        validator.validate({**result, "hook_events": ["UserPromptSubmit"]})


def test_beacon_antigravity_projection_limits_events_to_its_named_block():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "antigravity",
        "beacon_hooks_configured": True,
        "hook_events": ["PreInvocation", "PreToolUse", "PostInvocation"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "hook_events": ["sessionStart"]})
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})


def test_beacon_devin_desktop_projection_is_limited_to_pinned_cascade_events():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "devin_desktop",
        "beacon_hooks_configured": True,
        "hook_events": ["pre_user_prompt", "post_write_code", "post_run_command", "post_mcp_tool_use", "post_read_code"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "hook_events": ["SessionStart"]})
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})


def test_beacon_openhands_projection_accepts_only_documented_event_spellings():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "openhands",
        "beacon_hooks_configured": True,
        "hook_events": ["session_start", "PostToolUse"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "hook_events": ["PreInvocation"]})
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})


def test_beacon_kiro_projection_limits_events_to_installed_triggers():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "kiro",
        "beacon_hooks_configured": True,
        "hook_events": ["SessionStart", "PostToolUse", "Stop"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "hook_events": ["SessionEnd"]})
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})


def test_beacon_kimi_code_projection_requires_toml_and_allows_only_beacon_events():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    events = [
        "SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
        "PermissionRequest", "PermissionResult", "Stop", "SessionEnd", "SubagentStart",
        "SubagentStop", "PreCompact", "PostCompact",
    ]
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "toml",
        "kind": "host_hook_configuration",
        "harness": "kimi_code",
        "beacon_hooks_configured": True,
        "hook_events": events,
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "source_format": "json"})
    with pytest.raises(ValidationError):
        validator.validate({**result, "hook_events": ["PreInvocation"]})
    with pytest.raises(ValidationError):
        validator.validate({**result, "harness": "kiro"})


def test_beacon_muse_projection_limits_events_to_the_managed_hooks_contract():
    schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    result = {
        "schema_version": "threadforge.beacon-harness-config.v1",
        "source_format": "json",
        "kind": "host_hook_configuration",
        "harness": "muse_code",
        "beacon_hooks_configured": True,
        "hook_events": ["SessionStart", "PermissionRequest", "PreCompact", "Stop"],
        "omitted_fields": [
            "credentials", "environment", "hook_commands", "hook_matchers", "local_paths", "unrelated_settings",
        ],
        "human_review_required": True,
        "execution_authorized": False,
    }
    validator.validate(result)
    with pytest.raises(ValidationError):
        validator.validate({**result, "hook_events": ["PreLLMCall"]})
    with pytest.raises(ValidationError):
        validator.validate({**result, "source_format": "toml"})
    with pytest.raises(ValidationError):
        validator.validate({**result, "execution_authorized": True})

