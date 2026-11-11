import importlib.util
import json
import subprocess
import sys
from pathlib import Path

import pytest


CHECKER_PATH = Path(__file__).resolve().parents[2] / "scripts" / "verify_release_sbom.py"
CHECKER_SPEC = importlib.util.spec_from_file_location("threadforge_release_sbom", CHECKER_PATH)
if CHECKER_SPEC is None or CHECKER_SPEC.loader is None:
    raise RuntimeError("could not load the release SBOM verifier")
CHECKER = importlib.util.module_from_spec(CHECKER_SPEC)
sys.modules[CHECKER_SPEC.name] = CHECKER
CHECKER_SPEC.loader.exec_module(CHECKER)


BUILD_INFO = """threadforge.exe: go1.27.4
	path example.com/threadforge/cmd/threadforge
	mod example.com/threadforge (devel)
	dep example.com/library v1.2.3 h1:libraryhash
	dep example.com/local v0.0.0
	\t=> ./third_party/local (devel)
"""


def package(name, version="UNKNOWN", declared="NOASSERTION", concluded="NOASSERTION"):
    return {
        "name": name,
        "versionInfo": version,
        "licenseDeclared": declared,
        "licenseConcluded": concluded,
    }


def complete_spdx_document():
    return {
        "spdxVersion": "SPDX-2.3",
        "packages": [
            package("example.com/threadforge"),
            package("example.com/library", "v1.2.3"),
            package("example.com/local"),
            package("stdlib", "go1.27.4"),
            package("threadforge", "sha256:binary"),
            package("threadforge.exe"),
        ],
    }


def run_with_build_info(output=BUILD_INFO, returncode=0):
    def fake_run(*args, **kwargs):
        return subprocess.CompletedProcess(
            args=args[0], returncode=returncode, stdout=output, stderr="synthetic failure"
        )

    return fake_run


def write_inputs(tmp_path, document=None):
    binary = tmp_path / "threadforge.exe"
    binary.write_bytes(b"synthetic executable")
    sbom = tmp_path / "threadforge.spdx.json"
    sbom.write_text(
        json.dumps(document if document is not None else complete_spdx_document()),
        encoding="utf-8",
    )
    return binary, sbom


def verify(binary, sbom, *, run=run_with_build_info()):
    return CHECKER.verify_release_sbom(
        binary,
        sbom,
        expected_main_module="example.com/threadforge",
        run=run,
    )


def test_reconciles_modules_versions_and_keeps_legal_clearance_closed(tmp_path):
    binary, sbom = write_inputs(tmp_path)

    report = verify(binary, sbom)

    assert report["schema"] == "threadforge.release-binary-sbom-check.v1"
    assert report["main_module"] == "example.com/threadforge"
    assert report["go_toolchain"] == "go1.27.4"
    assert report["go_module_count"] == 3
    assert report["spdx_package_count"] == 6
    assert report["declared_license_not_asserted"] == 6
    assert report["concluded_license_not_asserted"] == 6
    assert report["license_review_required"] is True
    assert report["release_clearance"] is False


def test_rejects_missing_build_dependency(tmp_path):
    document = complete_spdx_document()
    document["packages"] = [
        item for item in document["packages"] if item["name"] != "example.com/library"
    ]
    binary, sbom = write_inputs(tmp_path, document)

    with pytest.raises(CHECKER.ReleaseSBOMError, match="missing Go build-info packages"):
        verify(binary, sbom)


def test_rejects_dependency_version_mismatch(tmp_path):
    document = complete_spdx_document()
    next(item for item in document["packages"] if item["name"] == "example.com/library")[
        "versionInfo"
    ] = "v9.9.9"
    binary, sbom = write_inputs(tmp_path, document)

    with pytest.raises(CHECKER.ReleaseSBOMError, match="SPDX version mismatch"):
        verify(binary, sbom)


def test_rejects_unexplained_packages_and_duplicate_names(tmp_path):
    document = complete_spdx_document()
    document["packages"].append(package("example.com/not-in-binary", "v1.0.0"))
    binary, sbom = write_inputs(tmp_path, document)

    with pytest.raises(CHECKER.ReleaseSBOMError, match="not explained by the Go binary"):
        verify(binary, sbom)

    document["packages"].append(package("example.com/threadforge"))
    binary, sbom = write_inputs(tmp_path, document)
    with pytest.raises(CHECKER.ReleaseSBOMError, match="duplicated"):
        verify(binary, sbom)


def test_rejects_unexpected_main_module_and_invalid_spdx_version(tmp_path):
    binary, sbom = write_inputs(tmp_path)
    with pytest.raises(CHECKER.ReleaseSBOMError, match="main module"):
        CHECKER.verify_release_sbom(
            binary,
            sbom,
            expected_main_module="example.com/other",
            run=run_with_build_info(),
        )

    document = complete_spdx_document()
    document["spdxVersion"] = "SPDX-2.2"
    binary, sbom = write_inputs(tmp_path, document)
    with pytest.raises(CHECKER.ReleaseSBOMError, match="SPDX-2.3"):
        verify(binary, sbom)


def test_rejects_missing_toolchain_metadata_and_go_inspection_errors(tmp_path):
    binary, sbom = write_inputs(tmp_path)
    with pytest.raises(CHECKER.ReleaseSBOMError, match="Go toolchain"):
        verify(binary, sbom, run=run_with_build_info("not a Go build record\n"))
    with pytest.raises(CHECKER.ReleaseSBOMError, match="go version -m failed"):
        verify(binary, sbom, run=run_with_build_info(returncode=1))


def test_rejects_linked_binary_inputs(tmp_path):
    binary, sbom = write_inputs(tmp_path)
    linked_binary = tmp_path / "linked.exe"
    try:
        linked_binary.symlink_to(binary)
    except (NotImplementedError, OSError) as error:
        pytest.skip(f"file symlink creation unavailable: {error}")

    with pytest.raises(CHECKER.ReleaseSBOMError, match="non-symlink"):
        verify(linked_binary, sbom)
