import importlib.util
import hashlib
import json
import sys
from pathlib import Path

import pytest
from jsonschema import Draft7Validator


SCRIPT_PATH = Path(__file__).resolve().parents[2] / "scripts" / "build_python_lock_sbom.py"
SCRIPT_SPEC = importlib.util.spec_from_file_location("threadforge_python_lock_sbom", SCRIPT_PATH)
if SCRIPT_SPEC is None or SCRIPT_SPEC.loader is None:
    raise RuntimeError("could not load the Python lock SBOM builder")
SBOM = importlib.util.module_from_spec(SCRIPT_SPEC)
sys.modules[SCRIPT_SPEC.name] = SBOM
SCRIPT_SPEC.loader.exec_module(SBOM)

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
LOCK_PATH = REPOSITORY_ROOT / "python" / "uv.lock"
PROJECT_PATH = REPOSITORY_ROOT / "python" / "pyproject.toml"
WORKFLOW_PATH = REPOSITORY_ROOT / ".github" / "workflows" / "threadforge-tests.yml"
SPDX_SCHEMA_PATH = REPOSITORY_ROOT / "schema" / "third_party" / "spdx-2.3.schema.json"
THIRD_PARTY_PATH = REPOSITORY_ROOT / "THIRD_PARTY.md"
SOURCE_COMMIT = "a" * 40
SOURCE_DATE_EPOCH = 1_798_358_401


def build(path, *, version="0.5.6", project_path=PROJECT_PATH, lock_path=LOCK_PATH):
    return SBOM.build_python_lock_sbom(
        release_version=version,
        source_commit=SOURCE_COMMIT,
        lock_path=lock_path,
        project_path=project_path,
        output_path=path,
        source_date_epoch=SOURCE_DATE_EPOCH,
    )


def test_spdx_covers_full_locked_all_extras_graph_without_license_claims_or_paths(tmp_path):
    schema_bytes = SPDX_SCHEMA_PATH.read_bytes().replace(b"\r\n", b"\n")
    assert hashlib.sha256(schema_bytes).hexdigest() == (
        "3ec6cd5b8ba0c9a3e821da48536fa1b814567dc7e4376efe98d3e7b2a7a8d230"
    )
    assert "CC0-1.0" in THIRD_PARTY_PATH.read_text(encoding="utf-8")
    output = tmp_path / "python-lock.spdx.json"
    summary = build(output)
    document = json.loads(output.read_text(encoding="utf-8"))
    spdx_schema = json.loads(SPDX_SCHEMA_PATH.read_text(encoding="utf-8"))
    Draft7Validator.check_schema(spdx_schema)
    Draft7Validator(spdx_schema).validate(document)
    packages = document["packages"]
    package_ids = {package["SPDXID"] for package in packages}
    root = next(package for package in packages if package["name"] == "threadforge-python")

    assert document["spdxVersion"] == "SPDX-2.3"
    assert summary["package_record_count"] == 279
    assert len(packages) == 279
    assert summary["dependency_relationship_count"] > 546
    assert root["versionInfo"] == "0.5.6"
    assert root["primaryPackagePurpose"] == "APPLICATION"
    assert root["externalRefs"][0]["referenceLocator"] == "pkg:pypi/threadforge-python@0.5.6"
    assert all(package["licenseDeclared"] == "NOASSERTION" for package in packages)
    assert all(package["licenseConcluded"] == "NOASSERTION" for package in packages)
    assert all(package["downloadLocation"] == "NOASSERTION" for package in packages)
    assert all("NOASSERTION" not in json.dumps(package.get("externalRefs", [])) for package in packages)
    assert all(
        relationship["relatedSpdxElement"] in package_ids
        for relationship in document["relationships"]
        if relationship["relationshipType"] == "DEPENDS_ON"
    )
    assert any("resolution marker=" in relationship.get("comment", "") for relationship in document["relationships"])
    assert any("project extra=" in relationship.get("comment", "") for relationship in document["relationships"])
    assert "files" not in document
    assert b"files.pythonhosted.org" not in output.read_bytes()
    assert str(REPOSITORY_ROOT).encode() not in output.read_bytes()
    assert summary["license_review_status"] == "unreviewed"
    assert summary["release_clearance"] is False


def test_spdx_is_reproducible_and_refuses_overwrite(tmp_path):
    first = tmp_path / "first.spdx.json"
    second = tmp_path / "second.spdx.json"
    first_summary = build(first)
    second_summary = build(second)

    assert first.read_bytes() == second.read_bytes()
    assert first_summary == second_summary
    with pytest.raises(SBOM.PythonLockSBOMError, match="refusing to overwrite"):
        build(first)


def test_lock_and_project_hashes_are_stable_across_crlf_checkout(tmp_path):
    crlf_lock = tmp_path / "uv.lock"
    crlf_project = tmp_path / "pyproject.toml"
    crlf_lock.write_bytes(LOCK_PATH.read_bytes().replace(b"\r\n", b"\n").replace(b"\n", b"\r\n"))
    crlf_project.write_bytes(PROJECT_PATH.read_bytes().replace(b"\r\n", b"\n").replace(b"\n", b"\r\n"))
    normal = tmp_path / "normal.spdx.json"
    windows = tmp_path / "windows.spdx.json"

    normal_summary = build(normal)
    windows_summary = build(windows, lock_path=crlf_lock, project_path=crlf_project)

    assert normal.read_bytes() == windows.read_bytes()
    assert normal_summary == windows_summary


def test_rejects_lock_project_extra_mismatch(tmp_path):
    altered_project = tmp_path / "pyproject.toml"
    altered_project.write_bytes(PROJECT_PATH.read_bytes().replace(b'workspace = [', b'workspace-removed = [', 1))

    with pytest.raises(SBOM.PythonLockSBOMError, match="extras do not match"):
        build(tmp_path / "candidate.spdx.json", project_path=altered_project)


def test_release_workflow_builds_and_attests_the_lock_sbom_in_candidate_payloads():
    workflow = WORKFLOW_PATH.read_text(encoding="utf-8")
    marker = "      - name: Build Python all-extras dependency SPDX SBOM\n"
    assert marker in workflow
    step = workflow.split(marker, 1)[1].split("      - name: Attest Python dependency SPDX SBOM provenance\n", 1)[0]
    assert "python ../scripts/build_python_lock_sbom.py" in step
    assert "--lock-file uv.lock" in step
    assert "--project-file pyproject.toml" in step
    assert "      - name: Attest Python dependency SPDX SBOM provenance\n" in workflow
    assert "dist/threadforge_${RELEASE_VERSION}_python.spdx.json" in workflow
    for target in ("linux_amd64", "darwin_amd64", "darwin_arm64", "windows_amd64", "python"):
        assert f'"dist/threadforge_${{RELEASE_VERSION}}_{target}.spdx.json"' in workflow
    assert "Missing required release SPDX SBOM" in workflow
    assert "Expected exactly 27 versioned candidate payload files" in workflow


@pytest.mark.parametrize("version", ["v0.5.6", "0.5", "01.2.3"])
def test_rejects_non_stable_release_version(tmp_path, version):
    with pytest.raises(SBOM.PythonLockSBOMError, match="stable MAJOR.MINOR.PATCH"):
        build(tmp_path / "candidate.spdx.json", version=version)
