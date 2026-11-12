#!/usr/bin/env python3
"""Build a hash-bound, human-reviewable index from a verified release candidate.

The index preserves each target-specific observation. It does not infer or approve
licenses and cannot grant release clearance.
"""

from __future__ import annotations

import argparse
import csv
import hashlib
import importlib.util
import io
import json
import os
import re
import stat
import sys
import tarfile
from collections import defaultdict
from pathlib import Path
from typing import Any


SCRIPT_DIR = Path(__file__).resolve().parent
VERIFIER_PATH = SCRIPT_DIR / "verify_release_candidate.py"
VERIFIER_SPEC = importlib.util.spec_from_file_location(
    "threadforge_release_candidate_verifier", VERIFIER_PATH
)
if VERIFIER_SPEC is None or VERIFIER_SPEC.loader is None:
    raise RuntimeError("could not load the release-candidate verifier")
VERIFIER = importlib.util.module_from_spec(VERIFIER_SPEC)
VERIFIER_SPEC.loader.exec_module(VERIFIER)

VERSION_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
MAX_CHECKSUM_BYTES = 1024 * 1024
MAX_BUNDLE_FILE_BYTES = 512 * 1024 * 1024


class LicenseReviewIndexError(ValueError):
    """The verified candidate cannot safely produce a license review index."""


def _reject_constant(value: str) -> None:
    raise LicenseReviewIndexError(f"JSON contains a non-standard numeric constant: {value}")


def _strict_json_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise LicenseReviewIndexError("JSON contains a duplicate object key")
        result[key] = value
    return result


def _decode_json(data: bytes, label: str) -> dict[str, Any]:
    try:
        value = json.loads(
            data.decode("utf-8"),
            object_pairs_hook=_strict_json_object,
            parse_constant=_reject_constant,
        )
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise LicenseReviewIndexError(f"{label} is not strict UTF-8 JSON") from error
    if not isinstance(value, dict):
        raise LicenseReviewIndexError(f"{label} must be a JSON object")
    return value


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _require_plain_directory(path: Path, label: str) -> None:
    try:
        info = path.lstat()
    except OSError as error:
        raise LicenseReviewIndexError(f"{label} is unavailable") from error
    reparse_flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    if (
        not stat.S_ISDIR(info.st_mode)
        or stat.S_ISLNK(info.st_mode)
        or bool(getattr(info, "st_file_attributes", 0) & reparse_flag)
    ):
        raise LicenseReviewIndexError(f"{label} must be a plain directory, not a link")


def _require_plain_file(path: Path) -> bytes:
    try:
        info = path.lstat()
    except OSError as error:
        raise LicenseReviewIndexError("candidate payload is missing or unreadable") from error
    reparse_flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    if (
        not stat.S_ISREG(info.st_mode)
        or stat.S_ISLNK(info.st_mode)
        or bool(getattr(info, "st_file_attributes", 0) & reparse_flag)
        or info.st_size > MAX_BUNDLE_FILE_BYTES
    ):
        raise LicenseReviewIndexError("candidate payload is linked, non-regular, or too large")
    try:
        return path.read_bytes()
    except OSError as error:
        raise LicenseReviewIndexError("candidate payload cannot be read") from error


def _candidate_checksums(bundle_dir: Path, version: str) -> tuple[dict[str, str], bytes]:
    _require_plain_directory(bundle_dir, "candidate directory")
    checksum_path = bundle_dir / "checksums.txt"
    checksum_bytes = _require_plain_file(checksum_path)
    if len(checksum_bytes) > MAX_CHECKSUM_BYTES:
        raise LicenseReviewIndexError("candidate checksum manifest exceeds its size limit")
    try:
        text = checksum_bytes.decode("ascii")
    except UnicodeDecodeError as error:
        raise LicenseReviewIndexError("candidate checksum manifest must be ASCII") from error
    if not text.endswith("\n"):
        raise LicenseReviewIndexError("candidate checksum manifest must end with a newline")

    expected = VERIFIER.expected_payload_names(version)
    checksums: dict[str, str] = {}
    for line in text.splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        if match is None:
            raise LicenseReviewIndexError("candidate checksum manifest contains an invalid row")
        digest, name = match.groups()
        if name in checksums:
            raise LicenseReviewIndexError("candidate checksum manifest contains a duplicate name")
        checksums[name] = digest
    if set(checksums) != expected:
        raise LicenseReviewIndexError("candidate checksum manifest does not cover the complete bundle")

    try:
        children = list(bundle_dir.iterdir())
    except OSError as error:
        raise LicenseReviewIndexError("candidate directory cannot be listed") from error
    if {item.name for item in children} != expected | {"checksums.txt"}:
        raise LicenseReviewIndexError("candidate directory contains missing or unexpected payloads")
    for name, expected_digest in checksums.items():
        if _sha256(_require_plain_file(bundle_dir / name)) != expected_digest:
            raise LicenseReviewIndexError("candidate payload checksum does not match checksums.txt")
    return checksums, checksum_bytes


def _verified_candidate(
    bundle_dir: Path,
    version: str,
    source_commit: str,
    source_ref: str,
    repository: str,
    gh: str,
) -> dict[str, Any]:
    try:
        result = VERIFIER.verify_release_candidate(
            bundle_dir=bundle_dir,
            version=version,
            source_commit=source_commit,
            source_ref=source_ref,
            repository=repository,
            gh=gh,
        )
    except VERIFIER.ReleaseCandidateError as error:
        raise LicenseReviewIndexError(f"release candidate verification failed: {error}") from error
    return result


def _validate_verification_result(
    verification: dict[str, Any],
    *,
    version: str,
    source_commit: str,
    source_ref: str,
    repository: str,
    checksum_bytes: bytes,
) -> None:
    if (
        verification.get("schema") != "threadforge.release-candidate-verification.v1"
        or verification.get("repository") != repository
        or verification.get("version") != version
        or verification.get("source_commit") != source_commit
        or verification.get("source_ref") != source_ref
        or verification.get("checksums_sha256") != _sha256(checksum_bytes)
        or verification.get("payload_count") != 27
        or verification.get("checksum_count") != 27
        or verification.get("slsa_attestations_verified") != 28
        or verification.get("archive_sbom_attestations_verified") != 4
        or verification.get("integrity_verified") is not True
        or verification.get("provenance_verified") is not True
        or verification.get("license_review_required") is not True
        or verification.get("release_clearance") is not False
        or verification.get("release_authorized") is not False
    ):
        raise LicenseReviewIndexError("verification result does not bind this candidate or its legal state")


def _read_evidence_archive(data: bytes, label: str) -> dict[str, bytes]:
    members: dict[str, bytes] = {}
    expanded_bytes = 0
    try:
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
            for index, member in enumerate(archive):
                if index >= VERIFIER.MAX_ARCHIVE_MEMBERS:
                    raise LicenseReviewIndexError(f"{label} archive has too many members")
                name = VERIFIER._safe_member_name(member.name)
                if name in members or not member.isfile() or member.size < 0:
                    raise LicenseReviewIndexError(f"{label} archive has duplicate or unsafe members")
                expanded_bytes += member.size
                if expanded_bytes > VERIFIER.MAX_ARCHIVE_UNPACKED_BYTES:
                    raise LicenseReviewIndexError(f"{label} archive exceeds the expanded size limit")
                stream = archive.extractfile(member)
                if stream is None:
                    raise LicenseReviewIndexError(f"{label} archive member cannot be read")
                contents = stream.read(member.size + 1)
                if len(contents) != member.size:
                    raise LicenseReviewIndexError(f"{label} archive member size is inconsistent")
                members[name] = contents
    except (tarfile.TarError, OSError, VERIFIER.ReleaseCandidateError) as error:
        raise LicenseReviewIndexError(f"{label} archive contains an unsafe or invalid member") from error
    return members


def _check_unreviewed(evidence: dict[str, Any], label: str) -> None:
    if (
        evidence.get("license_review_status") != "unreviewed"
        or evidence.get("license_review_required") is not True
        or evidence.get("release_clearance") is not False
    ):
        raise LicenseReviewIndexError(f"{label} evidence has an unexpected legal-review status")


def _load_python_observations(
    bundle_dir: Path,
    version: str,
    source_commit: str,
    checksums: dict[str, str],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], list[dict[str, Any]]]:
    observations: list[dict[str, Any]] = []
    profiles: list[dict[str, Any]] = []
    all_profiles: list[tuple[str, str]] = []
    for system, python_version in VERIFIER.PYTHON_TARGETS:
        profile = f"python/{system}/{python_version}"
        name = f"threadforge_{version}_python_{system}_py{python_version}.license-evidence.tar.gz"
        all_profiles.append((profile, name))
    for profile, name in all_profiles:
        archive_path = bundle_dir / name
        archive_bytes = _require_plain_file(archive_path)
        archive_sha256 = _sha256(archive_bytes)
        if checksums.get(name) != archive_sha256:
            raise LicenseReviewIndexError("Python evidence archive changed after candidate verification")
        members = _read_evidence_archive(archive_bytes, profile)
        evidence = _decode_json(members.get("python-license-evidence/evidence.json", b""), profile)
        _check_unreviewed(evidence, profile)
        if (
            evidence.get("schema") != "threadforge.python-license-evidence.v2"
            or evidence.get("source_commit") != source_commit
            or evidence.get("release_version") != version
            or evidence.get("target")
            != {"os": profile.split("/")[1], "python_version": profile.split("/")[2]}
        ):
            raise LicenseReviewIndexError(f"{profile} metadata does not match the verified candidate")
        distributions = evidence.get("distributions")
        if not isinstance(distributions, list) or not distributions:
            raise LicenseReviewIndexError(f"{profile} has no distribution observations")
        for item in distributions:
            if not isinstance(item, dict):
                raise LicenseReviewIndexError(f"{profile} contains a malformed distribution")
            license_files: list[dict[str, Any]] = []
            for license_file in item.get("license_files", []):
                if not isinstance(license_file, dict):
                    raise LicenseReviewIndexError(f"{profile} contains a malformed license-file record")
                archive_member = f"python-license-evidence/{license_file['archive_path']}"
                data = members.get(archive_member)
                if (
                    data is None
                    or _sha256(data) != license_file.get("sha256")
                    or len(data) != license_file.get("size_bytes")
                ):
                    raise LicenseReviewIndexError(f"{profile} license-file bytes do not match evidence")
                license_files.append(
                    {
                        "archive_member": archive_member,
                        "path": license_file["path"],
                        "sha256": license_file["sha256"],
                        "size_bytes": license_file["size_bytes"],
                    }
                )
            observations.append(
                {
                    "profile": profile,
                    "archive": name,
                    "archive_sha256": archive_sha256,
                    "name": item["name"],
                    "normalized_name": item["normalized_name"],
                    "version": item["version"],
                    "locked": item["locked"],
                    "metadata_file_sha256": item["metadata_file_sha256"],
                    "license_expression": item["license_expression"],
                    "license_expression_status": item["license_expression_status"],
                    "license_expression_sha256": item["license_expression_sha256"],
                    "license_classifiers": item["license_classifiers"],
                    "license_classifier_metadata_malformed": item[
                        "license_classifier_metadata_malformed"
                    ],
                    "license_metadata_malformed": item["license_metadata_malformed"],
                    "legacy_license_field_byte_count": item["legacy_license_field_byte_count"],
                    "legacy_license_field_sha256": item["legacy_license_field_sha256"],
                    "license_files": license_files,
                }
            )
        profiles.append(
            {
                "profile": profile,
                "archive": name,
                "reviewer_archive_path": f"evidence/{name}",
                "archive_sha256": archive_sha256,
                "distribution_count": len(distributions),
                "without_expression_count": sum(
                    item.get("license_expression") is None for item in distributions
                ),
                "without_license_file_count": sum(
                    not item.get("license_files") for item in distributions
                ),
                "unreviewed": True,
            }
        )
    return observations, profiles, []


def _load_go_observations(
    bundle_dir: Path,
    version: str,
    source_commit: str,
    checksums: dict[str, str],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], list[dict[str, Any]]]:
    observations: list[dict[str, Any]] = []
    profiles: list[dict[str, Any]] = []
    notice_files: list[dict[str, Any]] = []
    for target in VERIFIER.GO_TARGETS:
        name = f"threadforge_{version}_{target}.go-licenses.tar.gz"
        archive_path = bundle_dir / name
        archive_bytes = _require_plain_file(archive_path)
        archive_sha256 = _sha256(archive_bytes)
        if checksums.get(name) != archive_sha256:
            raise LicenseReviewIndexError("Go evidence archive changed after candidate verification")
        members = _read_evidence_archive(archive_bytes, target)
        evidence = _decode_json(members.get("go-license-evidence/evidence.json", b""), target)
        _check_unreviewed(evidence, target)
        goos, goarch = target.split("_")
        if (
            evidence.get("schema") != "threadforge.go-license-evidence.v1"
            or evidence.get("source_commit") != source_commit
            or evidence.get("target") != {"goos": goos, "goarch": goarch}
        ):
            raise LicenseReviewIndexError(f"Go {target} metadata does not match the verified candidate")
        report_bytes = members.get("go-license-evidence/report.csv")
        if report_bytes is None:
            raise LicenseReviewIndexError(f"Go {target} report.csv is missing")
        try:
            report_text = report_bytes.decode("utf-8")
            rows = list(csv.DictReader(io.StringIO(report_text, newline=""), strict=True))
        except (UnicodeDecodeError, csv.Error) as error:
            raise LicenseReviewIndexError(f"Go {target} report.csv is invalid") from error
        if not rows or set(rows[0]) != {"package", "license_url", "license_expression"}:
            raise LicenseReviewIndexError(f"Go {target} report.csv has unexpected columns")
        if len(rows) != evidence.get("license_record_count"):
            raise LicenseReviewIndexError(f"Go {target} report count does not match its evidence")
        for row in rows:
            observations.append(
                {
                    "target": target,
                    "archive": name,
                    "archive_sha256": archive_sha256,
                    "package": row["package"],
                    "license_url": row["license_url"],
                    "license_expression": row["license_expression"],
                }
            )
        listed_notices = evidence.get("saved_notice_files")
        if not isinstance(listed_notices, list):
            raise LicenseReviewIndexError(f"Go {target} notice inventory is malformed")
        for notice in listed_notices:
            if not isinstance(notice, dict):
                raise LicenseReviewIndexError(f"Go {target} notice row is malformed")
            archive_member = f"go-license-evidence/licenses/{notice['path']}"
            data = members.get(archive_member)
            if (
                data is None
                or _sha256(data) != notice.get("sha256")
                or len(data) != notice.get("size_bytes")
            ):
                raise LicenseReviewIndexError(f"Go {target} notice bytes do not match evidence")
            notice_files.append(
                {
                    "target": target,
                    "archive": name,
                    "archive_sha256": archive_sha256,
                    "archive_member": archive_member,
                    "path": notice["path"],
                    "sha256": notice["sha256"],
                    "size_bytes": notice["size_bytes"],
                }
            )
        profiles.append(
            {
                "profile": f"go/{target}",
                "archive": name,
                "reviewer_archive_path": f"evidence/{name}",
                "archive_sha256": archive_sha256,
                "license_record_count": len(rows),
                "notice_file_count": len(listed_notices),
                "unreviewed": True,
            }
        )
    return observations, profiles, notice_files


def _python_variance(observations: list[dict[str, Any]]) -> list[dict[str, Any]]:
    grouped: dict[tuple[str, str], list[dict[str, Any]]] = defaultdict(list)
    for item in observations:
        grouped[(item["normalized_name"], item["version"])].append(item)
    result = []
    keys = (
        "license_expression",
        "license_expression_status",
        "license_classifiers",
        "license_classifier_metadata_malformed",
        "license_metadata_malformed",
        "legacy_license_field_byte_count",
        "legacy_license_field_sha256",
        "license_files",
    )
    for (name, version), items in sorted(grouped.items()):
        variants: dict[str, list[dict[str, Any]]] = defaultdict(list)
        for item in items:
            signature = {key: item[key] for key in keys}
            variants[json.dumps(signature, sort_keys=True, separators=(",", ":"))].append(item)
        if len(variants) > 1:
            differing_fields = [
                key
                for key in keys
                if len(
                    {
                        json.dumps(item[key], sort_keys=True, separators=(",", ":"))
                        for item in items
                    }
                )
                > 1
            ]
            result.append(
                {
                    "normalized_name": name,
                    "version": version,
                    "differing_fields": differing_fields,
                    "observations": [
                        {
                            "profile": item["profile"],
                            **{key: item[key] for key in keys},
                        }
                        for item in sorted(items, key=lambda entry: entry["profile"])
                    ],
                    "meaning": "raw observations differ; reviewer determination required",
                }
            )
    return result


def _go_variance(observations: list[dict[str, Any]]) -> list[dict[str, Any]]:
    grouped: dict[str, dict[str, list[dict[str, Any]]]] = defaultdict(
        lambda: defaultdict(list)
    )
    for item in observations:
        grouped[item["package"]][item["target"]].append(item)
    result = []
    for package, target_rows in sorted(grouped.items()):
        target_observations = []
        signatures = set()
        for target, rows in sorted(target_rows.items()):
            licenses = [
                {
                    "license_url": row["license_url"],
                    "license_expression": row["license_expression"],
                }
                for row in sorted(
                    rows,
                    key=lambda entry: (entry["license_url"], entry["license_expression"]),
                )
            ]
            signatures.add(json.dumps(licenses, sort_keys=True, separators=(",", ":")))
            target_observations.append({"target": target, "licenses": licenses})
        if len(signatures) > 1:
            result.append(
                {
                    "package": package,
                    "observations": target_observations,
                    "meaning": "license rows differ across targets; reviewer determination required",
                }
            )
    return result


def _csv_value(value: Any) -> str:
    text = value if isinstance(value, str) else json.dumps(value, sort_keys=True, ensure_ascii=False)
    if text.lstrip().startswith(("=", "+", "-", "@", "\t", "\r")):
        return "'" + text
    return text


def _make_csv(index: dict[str, Any]) -> bytes:
    buffer = io.StringIO(newline="")
    writer = csv.writer(buffer, lineterminator="\n")
    writer.writerow(
        [
            "ecosystem",
            "profile",
            "package_name",
            "normalized_name",
            "version",
            "locked",
            "metadata_file_sha256",
            "license_expression",
            "expression_status",
            "license_expression_sha256",
            "classifiers_json",
            "license_classifier_metadata_malformed",
            "license_metadata_malformed",
            "legacy_license_field_byte_count",
            "legacy_license_field_sha256",
            "license_files_json",
            "source_archive",
            "source_archive_sha256",
            "license_url",
        ]
    )
    for item in index["python_observations"]:
        writer.writerow(
            [_csv_value(value) for value in [
                "pypi",
                item["profile"],
                item["name"],
                item["normalized_name"],
                item["version"],
                item["locked"],
                item["metadata_file_sha256"],
                item["license_expression"],
                item["license_expression_status"],
                item["license_expression_sha256"],
                item["license_classifiers"],
                item["license_classifier_metadata_malformed"],
                item["license_metadata_malformed"],
                item["legacy_license_field_byte_count"],
                item["legacy_license_field_sha256"],
                item["license_files"],
                item["archive"],
                item["archive_sha256"],
                "",
            ]]
        )
    for item in index["go_observations"]:
        writer.writerow(
            [_csv_value(value) for value in [
                "go",
                f"go/{item['target']}",
                item["package"],
                "",
                "",
                "",
                "",
                item["license_expression"],
                "captured_unreviewed",
                "",
                "[]",
                "",
                "",
                "",
                "",
                "",
                item["archive"],
                item["archive_sha256"],
                item["license_url"],
            ]]
        )
    return buffer.getvalue().encode("utf-8")


def build_license_review_index(
    *,
    bundle_dir: Path,
    output_dir: Path,
    verification: dict[str, Any],
) -> dict[str, Any]:
    """Create a separate reviewer index from an exact, provenance-verified bundle."""
    version = verification.get("version")
    source_commit = verification.get("source_commit")
    source_ref = verification.get("source_ref")
    repository = verification.get("repository")
    if not isinstance(version, str) or not VERSION_RE.fullmatch(version):
        raise LicenseReviewIndexError("verification result contains an invalid version")
    if not isinstance(source_commit, str) or not COMMIT_RE.fullmatch(source_commit):
        raise LicenseReviewIndexError("verification result contains an invalid source commit")
    if not isinstance(source_ref, str) or not isinstance(repository, str):
        raise LicenseReviewIndexError("verification result is missing source identity")
    output_dir = output_dir.absolute()
    _require_plain_directory(output_dir.parent, "output parent")
    if output_dir.exists() or output_dir.is_symlink():
        raise LicenseReviewIndexError("output directory must not already exist")

    checksums, checksum_bytes = _candidate_checksums(bundle_dir, version)
    _validate_verification_result(
        verification,
        version=version,
        source_commit=source_commit,
        source_ref=source_ref,
        repository=repository,
        checksum_bytes=checksum_bytes,
    )
    python_observations, python_profiles, _ = _load_python_observations(
        bundle_dir, version, source_commit, checksums
    )
    go_observations, go_profiles, go_notices = _load_go_observations(
        bundle_dir, version, source_commit, checksums
    )
    if len(python_profiles) != len(VERIFIER.PYTHON_TARGETS) or len(go_profiles) != len(VERIFIER.GO_TARGETS):
        raise LicenseReviewIndexError("candidate license-evidence profile coverage is incomplete")

    python_unique = {
        (item["normalized_name"], item["version"]) for item in python_observations
    }
    go_unique = {item["package"] for item in go_observations}
    index: dict[str, Any] = {
        "schema": "threadforge.license-review-index.v1",
        "candidate": {
            "repository": repository,
            "version": version,
            "source_commit": source_commit,
            "source_ref": source_ref,
            "checksums_sha256": _sha256(checksum_bytes),
            "evidence_archive_count": len(python_profiles) + len(go_profiles),
            "payload_count": verification["payload_count"],
            "slsa_attestations_verified": verification["slsa_attestations_verified"],
            "archive_sbom_attestations_verified": verification[
                "archive_sbom_attestations_verified"
            ],
            "integrity_verified": True,
            "provenance_verified": True,
        },
        "review_status": "unreviewed",
        "license_review_required": True,
        "automated_license_inference_performed": False,
        "release_clearance": False,
        "release_authorized": False,
        "summary": {
            "python_profile_count": len(python_profiles),
            "python_observation_count": len(python_observations),
            "python_unique_package_version_count": len(python_unique),
            "python_observations_without_expression": sum(
                item["license_expression"] is None for item in python_observations
            ),
            "python_observations_without_license_files": sum(
                not item["license_files"] for item in python_observations
            ),
            "go_profile_count": len(go_profiles),
            "go_observation_count": len(go_observations),
            "go_unique_package_count": len(go_unique),
            "go_notice_file_count": len(go_notices),
            "python_cross_profile_variance_group_count": 0,
            "go_cross_target_variance_group_count": 0,
        },
        "profiles": python_profiles + go_profiles,
        "python_observations": python_observations,
        "go_observations": go_observations,
        "go_notice_files": go_notices,
        "python_cross_profile_variance": _python_variance(python_observations),
        "go_cross_target_variance": _go_variance(go_observations),
        "review_instructions": [
            "Treat each profile observation as source evidence; do not infer a license from package identity.",
            "Inspect the referenced archive members and compare them with THIRD_PARTY.md and shipped notices.",
            "The evidence/ directory contains the exact candidate checksum manifest and the referenced Python/Go evidence archives.",
            "Resolve cross-profile variance explicitly; this index does not classify it as safe or unsafe.",
            "Record legal decisions and reviewer identity through the approved release process, not by editing this generated index.",
        ],
    }
    index["summary"]["python_cross_profile_variance_group_count"] = len(
        index["python_cross_profile_variance"]
    )
    index["summary"]["go_cross_target_variance_group_count"] = len(
        index["go_cross_target_variance"]
    )

    # Ensure no payload changed while the index was being generated.
    final_checksums, final_checksum_bytes = _candidate_checksums(bundle_dir, version)
    if final_checksum_bytes != checksum_bytes or final_checksums != checksums:
        raise LicenseReviewIndexError("candidate changed while the review index was being built")

    json_bytes = (json.dumps(index, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode(
        "utf-8"
    )
    csv_bytes = _make_csv(index)
    files: dict[str, bytes] = {
        "license-review-index.json": json_bytes,
        "license-review-observations.csv": csv_bytes,
        "evidence/checksums.txt": checksum_bytes,
    }
    for profile in python_profiles + go_profiles:
        archive_name = profile["archive"]
        archive_bytes = _require_plain_file(bundle_dir / archive_name)
        archive_sha256 = _sha256(archive_bytes)
        if archive_sha256 != profile["archive_sha256"] or checksums.get(archive_name) != archive_sha256:
            raise LicenseReviewIndexError("review evidence archive changed before packaging")
        files[profile["reviewer_archive_path"]] = archive_bytes
    manifest = "".join(f"{_sha256(data)}  {name}\n" for name, data in sorted(files.items())).encode(
        "ascii"
    )
    files["SHA256SUMS.txt"] = manifest
    try:
        output_dir.mkdir(mode=0o700)
        for name, data in files.items():
            destination = output_dir / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            with destination.open("xb") as stream:
                stream.write(data)
            if os.name != "nt":
                destination.chmod(0o600)
    except FileExistsError as error:
        raise LicenseReviewIndexError("output directory or file already exists") from error
    except OSError as error:
        raise LicenseReviewIndexError("review index could not be written completely") from error
    return index


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle-dir", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-ref", required=True)
    parser.add_argument("--repo", default=VERIFIER.REPOSITORY)
    parser.add_argument("--gh", default="gh")
    parser.add_argument("--output-dir", type=Path, required=True)
    arguments = parser.parse_args(argv)
    try:
        verification = _verified_candidate(
            arguments.bundle_dir,
            arguments.version,
            arguments.source_commit,
            arguments.source_ref,
            arguments.repo,
            arguments.gh,
        )
        index = build_license_review_index(
            bundle_dir=arguments.bundle_dir,
            output_dir=arguments.output_dir,
            verification=verification,
        )
    except (LicenseReviewIndexError, OSError, VERIFIER.ReleaseCandidateError) as error:
        print(f"license review index failed: {error}", file=sys.stderr)
        return 1
    print(
        json.dumps(
            {
                "schema": index["schema"],
                "output_dir": str(arguments.output_dir),
                "source_commit": index["candidate"]["source_commit"],
                "version": index["candidate"]["version"],
                "python_observations": index["summary"]["python_observation_count"],
                "go_observations": index["summary"]["go_observation_count"],
                "review_status": index["review_status"],
                "release_clearance": index["release_clearance"],
            },
            sort_keys=True,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
