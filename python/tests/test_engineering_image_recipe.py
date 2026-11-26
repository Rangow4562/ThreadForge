from __future__ import annotations

import json
import re
import shutil
import subprocess
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator

from threadforge_domains.workspace.security import valid_pinned_image

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
IMAGE_ROOT = REPOSITORY_ROOT / "containers" / "engineering"
SHA256 = "a" * 64


def _assert_hash_locked_requirements(path: Path, *, minimum_packages: int) -> set[str]:
    entries: dict[str, int] = {}
    current: str | None = None
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line or line.startswith("#"):
            continue
        if not line.startswith(" "):
            match = re.fullmatch(r"([A-Za-z0-9_.-]+)==([^\\\s]+) \\", line)
            assert match, f"unlocked or unexpected requirement line in {path.name}"
            current = match.group(1).lower().replace("_", "-").replace(".", "-")
            assert current not in entries
            entries[current] = 0
            continue
        assert current is not None
        fields = line.strip().split()
        assert len(fields) in {1, 2} and (len(fields) == 1 or fields[1] == "\\")
        assert fields[0].startswith("--hash=sha256:")
        assert re.fullmatch(r"[0-9a-f]{64}", fields[0].removeprefix("--hash=sha256:"))
        entries[current] += 1
    assert len(entries) >= minimum_packages
    assert all(count > 0 for count in entries.values())
    return set(entries)


def test_engineering_image_inputs_are_digest_pinned_and_hash_locked():
    base_image = (IMAGE_ROOT / "base-image.txt").read_text(encoding="utf-8").strip()
    assert re.fullmatch(
        r"python:3\.12\.[0-9]+-slim-bookworm@sha256:[0-9a-f]{64}", base_image,
    )

    runtime_packages = _assert_hash_locked_requirements(
        IMAGE_ROOT / "requirements-linux-x86_64-py312.lock", minimum_packages=100,
    )
    runtime_lock = (IMAGE_ROOT / "requirements-linux-x86_64-py312.lock").read_text(
        encoding="utf-8"
    )
    opentelemetry_version = re.search(
        r"(?m)^opentelemetry-api==([0-9]+(?:\.[0-9]+){2}) \\$", runtime_lock
    )
    assert opentelemetry_version is not None
    assert tuple(map(int, opentelemetry_version.group(1).split("."))) >= (1, 45, 2)
    assert "opentelemetry-api>=1.45.2,<2" in (
        IMAGE_ROOT / "constraints.txt"
    ).read_text(encoding="utf-8")
    build_packages = _assert_hash_locked_requirements(
        IMAGE_ROOT / "build-requirements.lock", minimum_packages=3,
    )
    assert {
        "datadog-api-client",
        "fastmcp",
        "litellm",
        "mcp",
        "opentelemetry-api",
        "pymupdf",
        "uvicorn",
    } <= runtime_packages
    assert {"setuptools", "wheel"} <= build_packages

    dockerfile = (IMAGE_ROOT / "Dockerfile").read_text(encoding="utf-8")
    assert dockerfile.count("FROM ${THREADFORGE_PYTHON_BASE_IMAGE}") == 2
    assert "--require-hashes" in dockerfile
    assert "USER 65532:65532" in dockerfile
    assert "RUN chmod 0555 /opt/threadforge" in dockerfile
    assert "chmod -R" not in dockerfile
    assert "/opt/threadforge/archipelago/runner/agents/models.py" in dockerfile
    assert "/opt/threadforge/archipelago/harness_eng/main.py" in dockerfile

    build_script = (IMAGE_ROOT / "build.ps1").read_text(encoding="utf-8")
    assert "--build-context \"threadforge-python=" in build_script
    assert "--build-context \"threadforge-workspace=" in build_script
    assert "--build-context \"threadforge-archipelago=" in build_script
    assert "--load" in build_script
    assert "--push" not in build_script

    python_context_ignore = (REPOSITORY_ROOT / "python" / ".dockerignore").read_text(
        encoding="utf-8"
    )
    assert "**" in python_context_ignore
    assert "!threadforge_domains/**" in python_context_ignore
    assert "threadforge_domains/workspace/runtime/**" in python_context_ignore
    assert "**/__pycache__/**" in python_context_ignore


def test_runtime_pin_schema_accepts_registry_digest_and_local_image_id():
    schema = json.loads(
        (REPOSITORY_ROOT / "schema" / "engineering-runtime-pin-v1.schema.json")
        .read_text(encoding="utf-8")
    )
    validator = Draft202012Validator(schema)
    pin = {
        "schema_version": "threadforge.engineering-runtime-pin.v1",
        "rrsi_source_revision": "be50316e1db05914068a973f322770ef08ed7ba1",
        "evaluator_source_revision": "b" * 40,
        "benchmark_tree_sha256": SHA256,
        "evaluator_tree_sha256": SHA256,
        "task_count": 1,
        "task_ids_sha256": SHA256,
        "execution_image": f"sha256:{SHA256}",
        "benchmark_license": "Apache-2.0",
        "evaluator_license": "Apache-2.0",
        "license_review_id": "review-1",
    }
    validator.validate(pin)
    assert valid_pinned_image(pin["execution_image"])

    pin["execution_image"] = f"registry.example/threadforge/engineering@sha256:{SHA256}"
    validator.validate(pin)
    assert valid_pinned_image(pin["execution_image"])

    pin["execution_image"] = "threadforge/engineering:latest"
    assert not validator.is_valid(pin)
    assert not valid_pinned_image(pin["execution_image"])


def test_image_build_receipt_schema_is_immutable_and_data_free():
    schema = json.loads(
        (REPOSITORY_ROOT / "schema" / "engineering-image-build-v1.schema.json")
        .read_text(encoding="utf-8")
    )
    receipt = {
        "schema_version": "threadforge.engineering-image-build.v1",
        "platform": "linux/amd64",
        "image_id": f"sha256:{SHA256}",
        "tag": "threadforge-engineering:local",
        "base_image": (IMAGE_ROOT / "base-image.txt").read_text(encoding="utf-8").strip(),
        "runtime_requirements_sha256": SHA256,
        "build_requirements_sha256": SHA256,
        "benchmark_data_included": False,
        "evaluator_included": False,
    }
    Draft202012Validator(schema).validate(receipt)
    receipt["evaluator_included"] = True
    assert not Draft202012Validator(schema).is_valid(receipt)


def test_image_build_receipt_v2_schema_binds_local_id_and_config_digest_separately():
    schema = json.loads(
        (REPOSITORY_ROOT / "schema" / "engineering-image-build-v2.schema.json")
        .read_text(encoding="utf-8")
    )
    receipt = {
        "schema_version": "threadforge.engineering-image-build.v2",
        "platform": "linux/amd64",
        "image_id": f"sha256:{'b' * 64}",
        "image_config_digest": f"sha256:{'a' * 64}",
        "tag": "threadforge-engineering:local",
        "base_image": (IMAGE_ROOT / "base-image.txt").read_text(encoding="utf-8").strip(),
        "runtime_requirements_sha256": SHA256,
        "build_requirements_sha256": SHA256,
        "benchmark_data_included": False,
        "evaluator_included": False,
    }
    Draft202012Validator(schema).validate(receipt)
    receipt["image_config_digest"] = "mutable-tag"
    assert not Draft202012Validator(schema).is_valid(receipt)


def test_powershell_build_script_reads_buildx_config_digest_and_cleans_metadata(tmp_path):
    powershell = shutil.which("pwsh")
    if powershell is None:
        pytest.skip("PowerShell Core is unavailable")

    image_id = f"sha256:{'b' * 64}"
    config_digest = f"sha256:{'a' * 64}"
    metadata_capture = tmp_path / "metadata-path.txt"
    smoke_script = tmp_path / "mock-engineering-build.ps1"

    def ps_literal(value: str) -> str:
        return "'" + value.replace("'", "''") + "'"

    smoke_script.write_text(
        f"""
$ErrorActionPreference = 'Stop'
$metadataCapture = {ps_literal(str(metadata_capture))}
$buildScript = {ps_literal(str(IMAGE_ROOT / 'build.ps1'))}
function docker {{
    $call = @($args)
    if ($call[0] -eq '--context' -and $call[2] -eq 'context') {{
        $global:LASTEXITCODE = 0
        return 'unix://mock-docker.sock'
    }}
    if ($call[0] -eq 'buildx' -and $call[1] -eq 'version') {{
        $global:LASTEXITCODE = 0
        return
    }}
    if ($call[0] -eq '--context' -and $call[2] -eq 'buildx' -and $call[3] -eq 'build') {{
        $metadataIndex = [Array]::IndexOf($call, '--metadata-file')
        if ($metadataIndex -lt 0) {{ throw 'Buildx was not asked to write metadata.' }}
        $metadataPath = [string]$call[$metadataIndex + 1]
        [IO.File]::WriteAllText($metadataCapture, $metadataPath)
        [IO.File]::WriteAllText($metadataPath, '{{"containerimage.config.digest":"{config_digest}"}}')
        $global:LASTEXITCODE = 0
        return
    }}
    if ($call[0] -eq '--context' -and $call[2] -eq 'image' -and $call[3] -eq 'inspect') {{
        $global:LASTEXITCODE = 0
        return '{image_id}'
    }}
    throw 'Unexpected Docker invocation.'
}}
$receiptLines = & $buildScript -Tag 'threadforge-engineering:local'
if ($LASTEXITCODE -ne 0) {{ throw 'Mocked build command returned a failure code.' }}
$receipt = $receiptLines | ConvertFrom-Json -ErrorAction Stop
if ($receipt.schema_version -ne 'threadforge.engineering-image-build.v2') {{ throw 'Unexpected build receipt version.' }}
if ($receipt.image_id -ne '{image_id}' -or $receipt.image_config_digest -ne '{config_digest}') {{ throw 'Build identities were not preserved separately.' }}
if (Test-Path -LiteralPath (Get-Content -LiteralPath $metadataCapture -Raw)) {{ throw 'Temporary Buildx metadata was not removed.' }}
$receiptLines
""",
        encoding="utf-8",
        newline="\n",
    )
    result = subprocess.run(
        [powershell, "-NoProfile", "-File", str(smoke_script)],
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )

    assert result.returncode == 0, result.stderr or result.stdout
    receipt = json.loads(result.stdout.strip())
    assert receipt["schema_version"] == "threadforge.engineering-image-build.v2"
    assert receipt["image_id"] == image_id
    assert receipt["image_config_digest"] == config_digest
