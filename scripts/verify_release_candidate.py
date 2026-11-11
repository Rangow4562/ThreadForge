#!/usr/bin/env python3
"""Verify a downloaded Threadforge release-candidate bundle.

This checks bundle integrity, archive/evidence consistency, and GitHub-signed
provenance. It deliberately never grants legal clearance or release approval.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import stat
import subprocess
import sys
import tarfile
import zipfile
from pathlib import Path, PurePosixPath
from typing import Any, Callable


REPOSITORY = "Rangow4562/ThreadForge"
WORKFLOW = ".github/workflows/threadforge-tests.yml"
SLSA_PREDICATE = "https://slsa.dev/provenance/v1"
SPDX_PREDICATE = "https://spdx.dev/Document/v2.3"
PYTHON_EXTRAS = ["decisions", "evolution", "hf", "serving", "test", "typed-data", "workspace"]
GO_TARGETS = ["linux_amd64", "darwin_amd64", "darwin_arm64", "windows_amd64"]
PYTHON_TARGETS = [(system, version) for system in ("linux", "windows", "darwin") for version in ("3.11", "3.12", "3.13", "3.14")]
MAX_BUNDLE_FILE_BYTES = 512 * 1024 * 1024
MAX_ARCHIVE_UNPACKED_BYTES = 512 * 1024 * 1024
MAX_ARCHIVE_MEMBERS = 25_000
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
VERSION_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
SOURCE_REF_RE = re.compile(r"^refs/(?:heads|tags)/[A-Za-z0-9._/-]+$")


class ReleaseCandidateError(ValueError):
    """The candidate does not satisfy the documented verification contract."""


def _strict_json_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ReleaseCandidateError("JSON contains a duplicate object key")
        result[key] = value
    return result


def _reject_json_constant(value: str) -> None:
    raise ReleaseCandidateError(f"JSON contains a non-standard numeric constant: {value}")


def _is_nonnegative_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and value >= 0


def _safe_member_name(name: str) -> str:
    if not name or "\\" in name or "\x00" in name:
        raise ReleaseCandidateError("archive contains an unsafe member name")
    path = PurePosixPath(name)
    if (
        path.is_absolute()
        or name != path.as_posix()
        or any(part in {"", ".", ".."} for part in path.parts)
    ):
        raise ReleaseCandidateError("archive contains an unsafe member path")
    return path.as_posix()


def _is_reparse_or_symlink(path: Path) -> bool:
    try:
        info = path.lstat()
    except OSError as error:
        raise ReleaseCandidateError("candidate path cannot be inspected") from error
    reparse_flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    return stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & reparse_flag)


def _require_regular_file(path: Path, *, limit: int = MAX_BUNDLE_FILE_BYTES) -> None:
    if _is_reparse_or_symlink(path):
        raise ReleaseCandidateError("candidate contains a link instead of a regular file")
    try:
        info = path.lstat()
    except OSError as error:
        raise ReleaseCandidateError("candidate file is missing or unreadable") from error
    if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
        raise ReleaseCandidateError("candidate file is not regular or exceeds the size limit")


def _sha256_file(path: Path) -> str:
    _require_regular_file(path)
    digest = hashlib.sha256()
    try:
        with path.open("rb") as source:
            while chunk := source.read(1024 * 1024):
                digest.update(chunk)
    except OSError as error:
        raise ReleaseCandidateError("candidate file cannot be read") from error
    return digest.hexdigest()


def expected_payload_names(version: str) -> set[str]:
    if not VERSION_RE.fullmatch(version):
        raise ReleaseCandidateError("version must be stable MAJOR.MINOR.PATCH")
    names: set[str] = set()
    for target in GO_TARGETS:
        suffix = ".zip" if target == "windows_amd64" else ".tar.gz"
        names.update(
            {
                f"threadforge_{version}_{target}{suffix}",
                f"threadforge_{version}_{target}.spdx.json",
                f"threadforge_{version}_{target}.go-licenses.tar.gz",
            }
        )
    for system, python_version in PYTHON_TARGETS:
        names.add(f"threadforge_{version}_python_{system}_py{python_version}.license-evidence.tar.gz")
    if len(names) != 24:
        raise ReleaseCandidateError("internal candidate payload inventory is invalid")
    return names


def _parse_checksums(path: Path, expected: set[str]) -> dict[str, str]:
    _require_regular_file(path, limit=1024 * 1024)
    try:
        data = path.read_bytes()
        text = data.decode("ascii")
    except (OSError, UnicodeDecodeError) as error:
        raise ReleaseCandidateError("checksums.txt is missing or is not ASCII") from error
    if not text.endswith("\n"):
        raise ReleaseCandidateError("checksums.txt must end with a newline")
    checksums: dict[str, str] = {}
    for line in text.splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        if match is None:
            raise ReleaseCandidateError("checksums.txt contains a malformed entry")
        digest, name = match.groups()
        if name in checksums:
            raise ReleaseCandidateError("checksums.txt contains a duplicate filename")
        checksums[name] = digest
    if set(checksums) != expected:
        raise ReleaseCandidateError("checksums.txt does not cover exactly the 24 candidate payloads")
    return checksums


def _read_tar(path: Path) -> dict[str, bytes]:
    _require_regular_file(path)
    members: dict[str, bytes] = {}
    total = 0
    try:
        with tarfile.open(path, mode="r:gz") as archive:
            for index, member in enumerate(archive):
                if index >= MAX_ARCHIVE_MEMBERS:
                    raise ReleaseCandidateError("archive contains too many members")
                name = _safe_member_name(member.name)
                if name in members or not member.isfile() or member.size < 0:
                    raise ReleaseCandidateError("archive contains duplicate or non-regular members")
                total += member.size
                if total > MAX_ARCHIVE_UNPACKED_BYTES:
                    raise ReleaseCandidateError("archive exceeds the expanded size limit")
                source = archive.extractfile(member)
                if source is None:
                    raise ReleaseCandidateError("archive member cannot be read")
                with source:
                    data = source.read(member.size + 1)
                if len(data) != member.size:
                    raise ReleaseCandidateError("archive member size does not match its header")
                members[name] = data
    except ReleaseCandidateError:
        raise
    except (OSError, tarfile.TarError, EOFError) as error:
        raise ReleaseCandidateError("candidate tar archive is malformed") from error
    return members


def _read_zip(path: Path) -> dict[str, bytes]:
    _require_regular_file(path)
    members: dict[str, bytes] = {}
    total = 0
    try:
        with zipfile.ZipFile(path) as archive:
            infos = archive.infolist()
            if len(infos) > 32:
                raise ReleaseCandidateError("candidate ZIP contains too many members")
            for info in infos:
                name = _safe_member_name(info.filename)
                mode = info.external_attr >> 16
                if (
                    name in members
                    or info.is_dir()
                    or stat.S_ISLNK(mode)
                    or info.flag_bits & 0x1
                    or info.file_size > MAX_ARCHIVE_UNPACKED_BYTES
                ):
                    raise ReleaseCandidateError("candidate ZIP contains duplicate, linked, or invalid members")
                total += info.file_size
                if total > MAX_ARCHIVE_UNPACKED_BYTES:
                    raise ReleaseCandidateError("candidate ZIP exceeds the expanded size limit")
                with archive.open(info, "r") as source:
                    data = source.read(info.file_size + 1)
                if len(data) != info.file_size:
                    raise ReleaseCandidateError("candidate ZIP member size does not match its header")
                members[name] = data
    except ReleaseCandidateError:
        raise
    except (OSError, zipfile.BadZipFile, RuntimeError, EOFError) as error:
        raise ReleaseCandidateError("candidate ZIP is malformed") from error
    return members


def _decode_json(data: bytes, label: str, *, max_bytes: int = 16 * 1024 * 1024) -> dict[str, Any]:
    if len(data) > max_bytes:
        raise ReleaseCandidateError(f"{label} exceeds the JSON size limit")
    try:
        value = json.loads(data, object_pairs_hook=_strict_json_object, parse_constant=_reject_json_constant)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ReleaseCandidateError(f"{label} is not valid JSON") from error
    if not isinstance(value, dict):
        raise ReleaseCandidateError(f"{label} must contain a JSON object")
    return value


def _validate_review_status(evidence: dict[str, Any]) -> None:
    if (
        evidence.get("license_review_status") != "unreviewed"
        or evidence.get("license_review_required") is not True
        or evidence.get("release_clearance") is not False
    ):
        raise ReleaseCandidateError("license evidence must remain explicitly unreviewed and uncleared")


def _validate_go_evidence(data: bytes, *, target: str, source_commit: str) -> None:
    members = _read_tar_bytes(data, expected_prefix="go-license-evidence/")
    evidence = _decode_json(members["evidence.json"], "Go license evidence")
    goos, goarch = target.split("_", 1)
    if (
        evidence.get("schema") != "threadforge.go-license-evidence.v1"
        or evidence.get("source_commit") != source_commit
        or evidence.get("target") != {"goos": goos, "goarch": goarch}
        or evidence.get("main_package") != "./cmd/threadforge"
        or not _is_nonnegative_int(evidence.get("unknown_license_expression_count"))
        or evidence["unknown_license_expression_count"] != 0
    ):
        raise ReleaseCandidateError("Go license evidence does not match the requested source and target")
    _validate_review_status(evidence)
    scanner = evidence.get("scanner")
    if not isinstance(scanner, dict) or scanner.get("version") != "v2.0.1":
        raise ReleaseCandidateError("Go license evidence scanner identity is invalid")
    notices = evidence.get("saved_notice_files")
    notice_count = evidence.get("saved_notice_file_count")
    if (
        not isinstance(notices, list)
        or not isinstance(notice_count, int)
        or isinstance(notice_count, bool)
        or len(notices) != notice_count
    ):
        raise ReleaseCandidateError("Go license evidence notice inventory is malformed")
    expected = {"README.md", "evidence.json", "report.csv"}
    total_bytes = 0
    seen: set[str] = set()
    for notice in notices:
        if not isinstance(notice, dict):
            raise ReleaseCandidateError("Go license evidence notice record is malformed")
        relative = _safe_member_name(str(notice.get("path", "")))
        digest = notice.get("sha256")
        size = notice.get("size_bytes")
        if relative in seen or not isinstance(digest, str) or not SHA256_RE.fullmatch(digest):
            raise ReleaseCandidateError("Go license evidence notice record is invalid")
        if not isinstance(size, int) or isinstance(size, bool) or size < 0:
            raise ReleaseCandidateError("Go license evidence notice size is invalid")
        member = f"licenses/{relative}"
        content = members.get(member)
        if content is None or len(content) != size or hashlib.sha256(content).hexdigest() != digest:
            raise ReleaseCandidateError("Go license evidence notice file does not match its manifest")
        seen.add(relative)
        expected.add(member)
        total_bytes += size
    if (
        set(members) != expected
        or not _is_nonnegative_int(evidence.get("saved_notice_total_bytes"))
        or evidence["saved_notice_total_bytes"] != total_bytes
        or not _is_nonnegative_int(evidence.get("license_record_count"))
        or evidence["license_record_count"] < 1
    ):
        raise ReleaseCandidateError("Go license evidence archive and manifest do not reconcile")


def _validate_python_evidence(
    data: bytes, *, system: str, python_version: str, release_version: str, source_commit: str
) -> None:
    members = _read_tar_bytes(data, expected_prefix="python-license-evidence/")
    evidence = _decode_json(members["evidence.json"], "Python license evidence")
    scope = evidence.get("scope")
    if (
        evidence.get("schema") != "threadforge.python-license-evidence.v2"
        or evidence.get("source_commit") != source_commit
        or evidence.get("release_version") != release_version
        or evidence.get("target") != {"os": system, "python_version": python_version}
        or not isinstance(scope, dict)
        or scope.get("extras") != PYTHON_EXTRAS
        or not _is_nonnegative_int(evidence.get("unlocked_distribution_count"))
        or evidence["unlocked_distribution_count"] != 0
    ):
        raise ReleaseCandidateError("Python license evidence does not match the requested source, version, or target")
    _validate_review_status(evidence)
    summary = evidence.get("license_summary")
    if not isinstance(summary, dict):
        raise ReleaseCandidateError("Python license evidence summary is malformed")
    installed_count = evidence.get("installed_distribution_count")
    locked_count = evidence.get("locked_package_count")
    distributions = evidence.get("distributions")
    if (
        not _is_nonnegative_int(installed_count)
        or installed_count < 1
        or not _is_nonnegative_int(locked_count)
        or locked_count < 1
        or not isinstance(distributions, list)
        or len(distributions) != installed_count
    ):
        raise ReleaseCandidateError("Python license evidence package counts are inconsistent")
    license_members = {name for name in members if name.startswith("licenses/")}
    license_bytes = sum(len(members[name]) for name in license_members)
    file_count = summary.get("license_file_count")
    file_bytes = summary.get("license_file_bytes")
    if (
        set(members) != {"README.md", "evidence.json", "report.csv", *license_members}
        or not _is_nonnegative_int(file_count)
        or not _is_nonnegative_int(file_bytes)
        or len(license_members) != file_count
        or license_bytes != file_bytes
    ):
        raise ReleaseCandidateError("Python license evidence archive and manifest do not reconcile")


def _read_tar_bytes(data: bytes, *, expected_prefix: str) -> dict[str, bytes]:
    import io

    members: dict[str, bytes] = {}
    total = 0
    try:
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
            for index, member in enumerate(archive):
                if index >= MAX_ARCHIVE_MEMBERS:
                    raise ReleaseCandidateError("license evidence archive contains too many members")
                name = _safe_member_name(member.name)
                if not name.startswith(expected_prefix):
                    raise ReleaseCandidateError("license evidence archive has an unexpected root")
                relative = name[len(expected_prefix) :]
                relative = _safe_member_name(relative)
                if relative in members or not member.isfile() or member.size < 0:
                    raise ReleaseCandidateError("license evidence archive has duplicate or non-regular members")
                total += member.size
                if total > MAX_ARCHIVE_UNPACKED_BYTES:
                    raise ReleaseCandidateError("license evidence archive exceeds the expanded size limit")
                source = archive.extractfile(member)
                if source is None:
                    raise ReleaseCandidateError("license evidence member cannot be read")
                with source:
                    content = source.read(member.size + 1)
                if len(content) != member.size:
                    raise ReleaseCandidateError("license evidence member size does not match its header")
                members[relative] = content
    except ReleaseCandidateError:
        raise
    except (OSError, tarfile.TarError, EOFError) as error:
        raise ReleaseCandidateError("license evidence archive is malformed") from error
    if not {"README.md", "evidence.json", "report.csv"}.issubset(members):
        raise ReleaseCandidateError("license evidence archive is missing required files")
    return members


def _validate_cli_archive(archive_path: Path, go_evidence: bytes, *, windows: bool) -> None:
    expected = {"LICENSE", "NOTICE", "THIRD_PARTY.md", "THIRD_PARTY_LICENSES.tar.gz"}
    binary_name = "threadforge.exe" if windows else "threadforge"
    expected.add(binary_name)
    members = _read_zip(archive_path) if windows else _read_tar(archive_path)
    if set(members) != expected:
        raise ReleaseCandidateError("CLI archive contents do not match the documented package layout")
    if not members["LICENSE"] or not members["NOTICE"] or not members["THIRD_PARTY.md"]:
        raise ReleaseCandidateError("CLI archive is missing required legal notice content")
    nested = members["THIRD_PARTY_LICENSES.tar.gz"]
    if hashlib.sha256(nested).digest() != hashlib.sha256(go_evidence).digest():
        raise ReleaseCandidateError("CLI archive embeds different Go license evidence than its paired payload")


def _validate_bundle_files(bundle_dir: Path, version: str, source_commit: str) -> tuple[set[str], dict[str, str]]:
    if _is_reparse_or_symlink(bundle_dir):
        raise ReleaseCandidateError("candidate bundle root must not be a link")
    if not bundle_dir.is_dir():
        raise ReleaseCandidateError("candidate bundle root is not a directory")
    expected = expected_payload_names(version)
    entries: dict[str, Path] = {}
    try:
        with os.scandir(bundle_dir) as scan:
            for entry in scan:
                if entry.name in entries or entry.is_symlink() or not entry.is_file(follow_symlinks=False):
                    raise ReleaseCandidateError("candidate bundle contains an unexpected directory or link")
                entries[entry.name] = Path(entry.path)
    except OSError as error:
        raise ReleaseCandidateError("candidate bundle cannot be listed") from error
    if set(entries) != expected | {"checksums.txt"}:
        raise ReleaseCandidateError("candidate bundle does not contain exactly the documented payload set")
    checksums = _parse_checksums(entries["checksums.txt"], expected)
    for name in sorted(expected):
        if _sha256_file(entries[name]) != checksums[name]:
            raise ReleaseCandidateError(f"candidate checksum mismatch: {name}")

    for target in GO_TARGETS:
        go_name = f"threadforge_{version}_{target}.go-licenses.tar.gz"
        archive_suffix = ".zip" if target == "windows_amd64" else ".tar.gz"
        archive_name = f"threadforge_{version}_{target}{archive_suffix}"
        spdx_name = f"threadforge_{version}_{target}.spdx.json"
        go_bytes = entries[go_name].read_bytes()
        _validate_go_evidence(go_bytes, target=target, source_commit=source_commit)
        _validate_cli_archive(entries[archive_name], go_bytes, windows=target == "windows_amd64")
        spdx = _decode_json(entries[spdx_name].read_bytes(), "SPDX SBOM")
        expected_spdx_name = "threadforge.exe" if target == "windows_amd64" else "threadforge"
        if spdx.get("spdxVersion") != "SPDX-2.3" or spdx.get("name") != expected_spdx_name:
            raise ReleaseCandidateError("candidate SPDX SBOM has an unexpected document identity")
    for system, python_version in PYTHON_TARGETS:
        name = f"threadforge_{version}_python_{system}_py{python_version}.license-evidence.tar.gz"
        _validate_python_evidence(
            entries[name].read_bytes(),
            system=system,
            python_version=python_version,
            release_version=version,
            source_commit=source_commit,
        )
    return expected, checksums


def _canonical_json(value: Any) -> str:
    try:
        return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
    except (TypeError, ValueError) as error:
        raise ReleaseCandidateError("attestation contains invalid JSON data") from error


def _verified_statement(
    *,
    gh: str,
    path: Path,
    predicate_type: str,
    source_commit: str,
    source_ref: str,
    repository: str,
    run: Callable[..., subprocess.CompletedProcess[str]],
) -> dict[str, Any]:
    command = [
        gh,
        "attestation",
        "verify",
        str(path),
        "--repo",
        repository,
        "--signer-workflow",
        f"{repository}/{WORKFLOW}",
        "--source-ref",
        source_ref,
        "--source-digest",
        source_commit,
        "--predicate-type",
        predicate_type,
        "--format",
        "json",
    ]
    try:
        result = run(
            command,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=120,
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise ReleaseCandidateError("GitHub attestation verification could not run") from error
    if result.returncode != 0:
        raise ReleaseCandidateError(f"GitHub attestation verification failed for {path.name}")
    if not isinstance(result.stdout, str) or len(result.stdout.encode("utf-8")) > 16 * 1024 * 1024:
        raise ReleaseCandidateError("GitHub attestation verification returned invalid or oversized output")
    try:
        result_items = json.loads(
            result.stdout,
            object_pairs_hook=_strict_json_object,
            parse_constant=_reject_json_constant,
        )
    except json.JSONDecodeError as error:
        raise ReleaseCandidateError("GitHub attestation verification returned invalid JSON") from error
    if not isinstance(result_items, list) or len(result_items) != 1:
        raise ReleaseCandidateError("GitHub attestation result is ambiguous")
    verification = result_items[0].get("verificationResult") if isinstance(result_items[0], dict) else None
    statement = verification.get("statement") if isinstance(verification, dict) else None
    if not isinstance(statement, dict) or statement.get("predicateType") != predicate_type:
        raise ReleaseCandidateError("GitHub attestation result has an unexpected predicate")
    return statement


def _verify_slsa_statement(
    statement: dict[str, Any], *, filename: str, digest: str, source_commit: str, source_ref: str, repository: str
) -> None:
    if statement.get("_type") != "https://in-toto.io/Statement/v1":
        raise ReleaseCandidateError("provenance statement type is not in-toto v1")
    subjects = statement.get("subject")
    if not isinstance(subjects, list) or len(subjects) != 1:
        raise ReleaseCandidateError("provenance subject set is ambiguous")
    subject = subjects[0]
    if not isinstance(subject, dict) or subject.get("name") != filename:
        raise ReleaseCandidateError("provenance names a different candidate payload")
    if subject.get("digest") != {"sha256": digest}:
        raise ReleaseCandidateError("provenance digest does not match the candidate payload")
    predicate = statement.get("predicate")
    build = predicate.get("buildDefinition") if isinstance(predicate, dict) else None
    if not isinstance(build, dict) or build.get("buildType") != "https://actions.github.io/buildtypes/workflow/v1":
        raise ReleaseCandidateError("provenance was not produced by the expected GitHub Actions builder")
    parameters = build.get("externalParameters") if isinstance(build, dict) else None
    workflow = parameters.get("workflow") if isinstance(parameters, dict) else None
    if not isinstance(workflow, dict) or workflow != {
        "path": WORKFLOW,
        "ref": source_ref,
        "repository": f"https://github.com/{repository}",
    }:
        raise ReleaseCandidateError("provenance is not bound to the expected repository workflow and ref")
    dependencies = build.get("resolvedDependencies")
    source_uri = f"git+https://github.com/{repository}@{source_ref}"
    if not isinstance(dependencies, list) or not any(
        isinstance(dependency, dict)
        and dependency.get("uri") == source_uri
        and isinstance(dependency.get("digest"), dict)
        and dependency["digest"].get("gitCommit") == source_commit
        for dependency in dependencies
    ):
        raise ReleaseCandidateError("provenance is not bound to the requested source commit")


def verify_release_candidate(
    *,
    bundle_dir: Path,
    version: str,
    source_commit: str,
    source_ref: str,
    repository: str = REPOSITORY,
    gh: str = "gh",
    run: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> dict[str, Any]:
    if not COMMIT_RE.fullmatch(source_commit):
        raise ReleaseCandidateError("source commit must be a full lowercase 40-character SHA-1")
    if not SOURCE_REF_RE.fullmatch(source_ref) or any(part in {"", ".", ".."} for part in source_ref.split("/")[1:]):
        raise ReleaseCandidateError("source ref must be a safe full refs/heads/... or refs/tags/... name")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ReleaseCandidateError("repository must use owner/name form")
    expected, checksums = _validate_bundle_files(bundle_dir, version, source_commit)

    # Check every SLSA statement, including checksums.txt, before any specialized SBOM claims.
    for filename in sorted(expected | {"checksums.txt"}):
        statement = _verified_statement(
            gh=gh,
            path=bundle_dir / filename,
            predicate_type=SLSA_PREDICATE,
            source_commit=source_commit,
            source_ref=source_ref,
            repository=repository,
            run=run,
        )
        digest = _sha256_file(bundle_dir / filename)
        _verify_slsa_statement(
            statement,
            filename=filename,
            digest=digest,
            source_commit=source_commit,
            source_ref=source_ref,
            repository=repository,
        )

    # The four additional SPDX attestations bind each platform archive to its exact SBOM.
    for target in GO_TARGETS:
        suffix = ".zip" if target == "windows_amd64" else ".tar.gz"
        archive_name = f"threadforge_{version}_{target}{suffix}"
        spdx_name = f"threadforge_{version}_{target}.spdx.json"
        statement = _verified_statement(
            gh=gh,
            path=bundle_dir / archive_name,
            predicate_type=SPDX_PREDICATE,
            source_commit=source_commit,
            source_ref=source_ref,
            repository=repository,
            run=run,
        )
        subjects = statement.get("subject")
        if not isinstance(subjects, list) or len(subjects) != 1:
            raise ReleaseCandidateError("SPDX attestation subject set is ambiguous")
        subject = subjects[0]
        if (
            not isinstance(subject, dict)
            or subject.get("name") != archive_name
            or subject.get("digest") != {"sha256": _sha256_file(bundle_dir / archive_name)}
        ):
            raise ReleaseCandidateError("SPDX attestation does not bind the matching CLI archive")
        signed_spdx = statement.get("predicate")
        bundled_spdx = _decode_json((bundle_dir / spdx_name).read_bytes(), "SPDX SBOM")
        if _canonical_json(signed_spdx) != _canonical_json(bundled_spdx):
            raise ReleaseCandidateError("bundled SPDX SBOM differs from the archive-bound attestation")

    return {
        "schema": "threadforge.release-candidate-verification.v1",
        "repository": repository,
        "version": version,
        "source_commit": source_commit,
        "source_ref": source_ref,
        "payload_count": len(expected),
        "checksum_count": len(checksums),
        "slsa_attestations_verified": len(expected) + 1,
        "archive_sbom_attestations_verified": len(GO_TARGETS),
        "integrity_verified": True,
        "provenance_verified": True,
        "license_review_required": True,
        "release_clearance": False,
        "release_authorized": False,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle-dir", type=Path, required=True)
    parser.add_argument("--version", required=True, help="stable version without leading v")
    parser.add_argument("--source-commit", required=True, help="full source commit SHA-1")
    parser.add_argument("--source-ref", required=True, help="full Git ref, e.g. refs/heads/main or refs/tags/v1.2.3")
    parser.add_argument("--repo", default=REPOSITORY, help="GitHub repository owner/name")
    parser.add_argument("--gh", default="gh", help="GitHub CLI executable")
    arguments = parser.parse_args(argv)
    try:
        result = verify_release_candidate(
            bundle_dir=arguments.bundle_dir,
            version=arguments.version,
            source_commit=arguments.source_commit,
            source_ref=arguments.source_ref,
            repository=arguments.repo,
            gh=arguments.gh,
        )
    except ReleaseCandidateError as error:
        print(f"release-candidate verification failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
