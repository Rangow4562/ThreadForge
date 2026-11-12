import csv
import hashlib
import importlib.util
import io
import json
import tarfile
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator


ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = ROOT / "scripts" / "build_license_review_index.py"
SCHEMA_PATH = ROOT / "schema" / "license-review-index-v1.schema.json"
SPEC = importlib.util.spec_from_file_location("threadforge_license_review_index", SCRIPT_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("could not load the license review index builder")
INDEX_BUILDER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(INDEX_BUILDER)

VERSION = "1.2.3"
SOURCE_COMMIT = "a" * 40
SOURCE_REF = "refs/heads/main"
REPOSITORY = "Rangow4562/ThreadForge"


def _tar_bytes(entries):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as archive:
        for name, data in entries.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = 0o644
            info.mtime = 0
            archive.addfile(info, io.BytesIO(data))
    return output.getvalue()


def _python_archive(os_name, python_version, expression, *, unsafe=False):
    target = {"os": os_name, "python_version": python_version}
    license_text = b"Example license notice\n"
    license_path = "licenses/example-package/1.0.0/LICENSE"
    distribution = {
        "name": "example-package",
        "normalized_name": "example-package",
        "version": "1.0.0",
        "locked": True,
        "metadata_file_sha256": "b" * 64,
        "license_expression": expression,
        "license_expression_status": "captured_unreviewed",
        "license_expression_sha256": hashlib.sha256(expression.encode()).hexdigest(),
        "license_classifiers": ["License :: OSI Approved :: MIT License"],
        "license_classifier_metadata_malformed": False,
        "license_metadata_malformed": False,
        "legacy_license_field_byte_count": 0,
        "legacy_license_field_sha256": None,
        "license_files": [
            {
                "path": "example_package-1.0.0.dist-info/licenses/LICENSE",
                "archive_path": license_path,
                "sha256": hashlib.sha256(license_text).hexdigest(),
                "size_bytes": len(license_text),
            }
        ],
    }
    evidence = {
        "schema": "threadforge.python-license-evidence.v2",
        "source_commit": SOURCE_COMMIT,
        "release_version": VERSION,
        "target": target,
        "license_review_status": "unreviewed",
        "license_review_required": True,
        "release_clearance": False,
        "distributions": [distribution],
    }
    entries = {
        "python-license-evidence/README.md": b"unreviewed\n",
        "python-license-evidence/evidence.json": json.dumps(evidence).encode(),
        "python-license-evidence/report.csv": b"name,version,expression\nexample-package,1.0.0,MIT\n",
        f"python-license-evidence/{license_path}": license_text,
    }
    if unsafe:
        entries["../outside.txt"] = b"unsafe member"
        del entries[f"python-license-evidence/{license_path}"]
    return _tar_bytes(entries)


def _go_archive(target):
    goos, goarch = target.split("_")
    notice = b"Example Go license\n"
    evidence = {
        "schema": "threadforge.go-license-evidence.v1",
        "source_commit": SOURCE_COMMIT,
        "target": {"goos": goos, "goarch": goarch},
        "license_review_status": "unreviewed",
        "license_review_required": True,
        "release_clearance": False,
        "license_record_count": 1,
        "saved_notice_files": [
            {
                "path": "example.com/module/LICENSE",
                "sha256": hashlib.sha256(notice).hexdigest(),
                "size_bytes": len(notice),
            }
        ],
    }
    return _tar_bytes(
        {
            "go-license-evidence/README.md": b"unreviewed\n",
            "go-license-evidence/evidence.json": json.dumps(evidence).encode(),
            "go-license-evidence/report.csv": b"package,license_url,license_expression\nexample.com/module,https://example.test/LICENSE,MIT\n",
            "go-license-evidence/licenses/example.com/module/LICENSE": notice,
        }
    )


def _make_candidate(root, *, variance=False, unsafe_python_profile=None):
    root.mkdir()
    payloads = INDEX_BUILDER.VERIFIER.expected_payload_names(VERSION)
    for name in payloads:
        (root / name).write_bytes(f"payload:{name}".encode())
    for os_name, python_version in INDEX_BUILDER.VERIFIER.PYTHON_TARGETS:
        profile = f"{os_name}/{python_version}"
        name = f"threadforge_{VERSION}_python_{os_name}_py{python_version}.license-evidence.tar.gz"
        expression = "Apache-2.0" if variance and profile == "linux/3.12" else "MIT"
        (root / name).write_bytes(
            _python_archive(
                os_name,
                python_version,
                expression,
                unsafe=profile == unsafe_python_profile,
            )
        )
    for target in INDEX_BUILDER.VERIFIER.GO_TARGETS:
        name = f"threadforge_{VERSION}_{target}.go-licenses.tar.gz"
        (root / name).write_bytes(_go_archive(target))
    checksums = "".join(
        f"{hashlib.sha256((root / name).read_bytes()).hexdigest()}  {name}\n"
        for name in sorted(payloads)
    ).encode("ascii")
    (root / "checksums.txt").write_bytes(checksums)
    return {
        "schema": "threadforge.release-candidate-verification.v1",
        "repository": REPOSITORY,
        "version": VERSION,
        "source_commit": SOURCE_COMMIT,
        "source_ref": SOURCE_REF,
        "checksums_sha256": hashlib.sha256(checksums).hexdigest(),
        "payload_count": 27,
        "checksum_count": 27,
        "slsa_attestations_verified": 28,
        "archive_sbom_attestations_verified": 4,
        "integrity_verified": True,
        "provenance_verified": True,
        "license_review_required": True,
        "release_clearance": False,
        "release_authorized": False,
    }


def _build(root, output, verification):
    return INDEX_BUILDER.build_license_review_index(
        bundle_dir=root,
        output_dir=output,
        verification=verification,
    )


def test_builds_target_preserving_review_index_and_never_grants_clearance(tmp_path):
    candidate = tmp_path / "candidate"
    verification = _make_candidate(candidate, variance=True)
    output = tmp_path / "review"

    index = _build(candidate, output, verification)

    assert index["candidate"]["source_commit"] == SOURCE_COMMIT
    assert index["candidate"]["checksums_sha256"] == verification["checksums_sha256"]
    assert index["candidate"]["evidence_archive_count"] == 16
    assert index["summary"]["python_profile_count"] == 12
    assert index["summary"]["python_observation_count"] == 12
    assert index["summary"]["go_profile_count"] == 4
    assert index["summary"]["go_observation_count"] == 4
    assert index["summary"]["python_cross_profile_variance_group_count"] == 1
    assert index["python_cross_profile_variance"][0]["differing_fields"] == [
        "license_expression"
    ]
    assert index["release_clearance"] is False
    assert index["release_authorized"] is False
    assert index["automated_license_inference_performed"] is False

    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    Draft202012Validator.check_schema(schema)
    Draft202012Validator(schema).validate(index)
    saved = json.loads((output / "license-review-index.json").read_text(encoding="utf-8"))
    assert saved == index
    assert (output / "evidence" / "checksums.txt").is_file()
    assert len(list((output / "evidence").glob("*.tar.gz"))) == 16
    with (output / "license-review-observations.csv").open(encoding="utf-8", newline="") as source:
        rows = list(csv.DictReader(source))
    assert len(rows) == 16
    assert all(len(row) == 19 for row in rows)
    sums = (output / "SHA256SUMS.txt").read_text(encoding="ascii").splitlines()
    assert len(sums) == 19
    for row in sums:
        digest, relative = row.split("  ", 1)
        assert hashlib.sha256((output / relative).read_bytes()).hexdigest() == digest


def test_rejects_changed_candidate_payload(tmp_path):
    candidate = tmp_path / "candidate"
    verification = _make_candidate(candidate)
    archive = candidate / f"threadforge_{VERSION}_linux_amd64.go-licenses.tar.gz"
    archive.write_bytes(b"changed after verification")

    with pytest.raises(INDEX_BUILDER.LicenseReviewIndexError, match="checksum"):
        _build(candidate, tmp_path / "review", verification)


def test_rejects_verification_receipt_that_claims_clearance(tmp_path):
    candidate = tmp_path / "candidate"
    verification = _make_candidate(candidate)
    verification["release_clearance"] = True

    with pytest.raises(INDEX_BUILDER.LicenseReviewIndexError, match="verification result"):
        _build(candidate, tmp_path / "review", verification)


def test_rejects_unsafe_evidence_archive_members(tmp_path):
    candidate = tmp_path / "candidate"
    verification = _make_candidate(candidate, unsafe_python_profile="linux/3.11")
    payloads = INDEX_BUILDER.VERIFIER.expected_payload_names(VERSION)
    checksums = "".join(
        f"{hashlib.sha256((candidate / item).read_bytes()).hexdigest()}  {item}\n"
        for item in sorted(payloads)
    ).encode("ascii")
    (candidate / "checksums.txt").write_bytes(checksums)
    verification["checksums_sha256"] = hashlib.sha256(checksums).hexdigest()

    with pytest.raises(INDEX_BUILDER.LicenseReviewIndexError, match="unsafe or invalid member"):
        _build(candidate, tmp_path / "review", verification)


def test_refuses_to_overwrite_existing_output_directory(tmp_path):
    candidate = tmp_path / "candidate"
    verification = _make_candidate(candidate)
    output = tmp_path / "review"
    output.mkdir()

    with pytest.raises(INDEX_BUILDER.LicenseReviewIndexError, match="must not already exist"):
        _build(candidate, output, verification)


def test_csv_cells_are_safe_for_spreadsheet_import():
    assert INDEX_BUILDER._csv_value("=SUM(A1:A2)") == "'=SUM(A1:A2)"
    assert INDEX_BUILDER._csv_value("normal-expression") == "normal-expression"


def test_candidate_workflow_publishes_self_contained_review_artifact_without_changing_payload_contract():
    workflow = (ROOT / ".github" / "workflows" / "threadforge-tests.yml").read_text(
        encoding="utf-8"
    )
    marker = "  license-review-index:\n"
    assert marker in workflow
    job = workflow.split(marker, 1)[1].split("\n  engineering-runtime-sbom:\n", 1)[0]
    assert "needs: [release-candidate-bundle]" in job
    assert "contents: read\n      id-token: write\n      attestations: write" in job
    assert "GH_TOKEN: ${{ github.token }}" in job
    assert "python scripts/build_license_review_index.py" in job
    assert "threadforge-license-review-${{ steps.release.outputs.version }}" in job
    assert "Attest the license-review index" in job
    assert "subject-path: ${{ runner.temp }}/threadforge-license-review/license-review-index.json" in job
    assert "retention-days: 90" in job
    assert "contents: write" not in job
    assert "release-publish" not in job

    assert "candidate_payloads=(dist/threadforge_${RELEASE_VERSION}_*" in workflow
    assert "Expected exactly 27 versioned candidate payload files" in workflow


def test_go_variance_compares_target_row_sets_not_duplicate_rows():
    observations = [
        {"package": "example.com/module", "target": target, "license_url": "https://example.test/license",
         "license_expression": expression}
        for target in INDEX_BUILDER.VERIFIER.GO_TARGETS
        for expression in ("MIT", "Apache-2.0")
    ]

    assert INDEX_BUILDER._go_variance(observations) == []

    observations.append(
        {
            "package": "example.com/module",
            "target": "linux_amd64",
            "license_url": "https://example.test/other-license",
            "license_expression": "BSD-3-Clause",
        }
    )
    variance = INDEX_BUILDER._go_variance(observations)
    assert len(variance) == 1
    linux = next(row for row in variance[0]["observations"] if row["target"] == "linux_amd64")
    assert len(linux["licenses"]) == 3


def test_python_variance_surfaces_legacy_license_field_differences():
    shared = {
        "normalized_name": "example-package",
        "version": "1.0.0",
        "license_expression": None,
        "license_expression_status": "missing",
        "license_classifiers": [],
        "license_classifier_metadata_malformed": False,
        "license_metadata_malformed": False,
        "license_files": [],
    }
    observations = [
        {
            **shared,
            "profile": "python/linux/3.14",
            "legacy_license_field_byte_count": 63431,
            "legacy_license_field_sha256": "a" * 64,
        },
        {
            **shared,
            "profile": "python/windows/3.14",
            "legacy_license_field_byte_count": 1657,
            "legacy_license_field_sha256": "b" * 64,
        },
    ]

    variance = INDEX_BUILDER._python_variance(observations)

    assert len(variance) == 1
    assert variance[0]["differing_fields"] == [
        "legacy_license_field_byte_count",
        "legacy_license_field_sha256",
    ]
