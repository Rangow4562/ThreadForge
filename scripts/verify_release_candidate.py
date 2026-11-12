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
import time
import zipfile
from email.parser import BytesParser
from pathlib import Path, PurePosixPath
from typing import Any, Callable
from urllib.parse import quote


REPOSITORY = "Rangow4562/ThreadForge"
WORKFLOW = ".github/workflows/threadforge-tests.yml"
SLSA_PREDICATE = "https://slsa.dev/provenance/v1"
SPDX_PREDICATE = "https://spdx.dev/Document/v2.3"
ATTESTATION_RETRY_DELAYS_SECONDS = (1.0, 2.0)
TRANSIENT_ATTESTATION_ERROR_MARKERS = (
    "api rate limit exceeded",
    "rate limit",
    "too many requests",
    "http 429",
    "http 500",
    "http 502",
    "http 503",
    "http 504",
    "temporarily unavailable",
    "service unavailable",
    "bad gateway",
    "gateway timeout",
    "connection reset",
    "connection refused",
    "network is unreachable",
    "tls handshake timeout",
    "unexpected eof",
    "i/o timeout",
    "context deadline exceeded",
)
PYTHON_EXTRAS = ["decisions", "evolution", "hf", "serving", "test", "typed-data", "workspace"]
GO_TARGETS = ["linux_amd64", "darwin_amd64", "darwin_arm64", "windows_amd64"]
PYTHON_TARGETS = [(system, version) for system in ("linux", "windows", "darwin") for version in ("3.11", "3.12", "3.13", "3.14")]
PYTHON_DISTRIBUTION_FILES = {
    "threadforge_py/__main__.py",
    "threadforge_py/protocol.py",
    "anyjev/decider.py",
    "rrsi/loop.py",
    "threadforge_domains/workspace/adapter.py",
    "threadforge_domains/workspace/runtime/domain/produce.sh",
    "threadforge_domains/workspace/runtime/archipelago/LICENSE",
    "threadforge_domains/workspace/runtime/archipelago/harness_workspace/main.py",
    "threadforge_domains/workspace/runtime/archipelago/harness_eng/main.py",
    "threadforge_domains/workspace/runtime/legal/CREDITS.anyjev.md",
    "threadforge_domains/workspace/runtime/legal/UPSTREAM_MODIFICATIONS.md",
    "threadforge_py/legal/LICENSE.threadforge",
    "threadforge_py/legal/NOTICE.threadforge",
    "threadforge_py/legal/THIRD_PARTY.md",
    "threadforge_py/legal/LICENSE.anyjev",
    "threadforge_py/legal/LICENSE.rrsi",
    "threadforge_py/legal/CREDITS.anyjev.md",
    "threadforge_py/legal/UPSTREAM_MODIFICATIONS.md",
}
PYTHON_FORBIDDEN_SUFFIXES = (".gguf", ".npz", ".parquet", ".pt", ".pth", ".safetensors")
MAX_BUNDLE_FILE_BYTES = 512 * 1024 * 1024
MAX_ARCHIVE_UNPACKED_BYTES = 512 * 1024 * 1024
MAX_ARCHIVE_MEMBERS = 25_000
MAX_PYTHON_DISTRIBUTION_MEMBERS = 25_000
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
    names.add(f"threadforge_python-{version}.tar.gz")
    names.add(f"threadforge_python-{version}-py3-none-any.whl")
    names.add(f"threadforge_{version}_python.spdx.json")
    if len(names) != 27:
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
        raise ReleaseCandidateError("checksums.txt does not cover exactly the 27 candidate payloads")
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


def _read_zip(path: Path, *, max_members: int = 32) -> dict[str, bytes]:
    _require_regular_file(path)
    members: dict[str, bytes] = {}
    total = 0
    try:
        with zipfile.ZipFile(path) as archive:
            infos = archive.infolist()
            if len(infos) > max_members:
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
) -> dict[str, Any]:
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
    input_hashes = evidence.get("input_hashes")
    if (
        not _is_nonnegative_int(installed_count)
        or installed_count < 1
        or not _is_nonnegative_int(locked_count)
        or locked_count < 1
        or not isinstance(distributions, list)
        or len(distributions) != installed_count
        or not isinstance(input_hashes, dict)
        or set(input_hashes) != {"uv_lock_sha256", "pyproject_sha256"}
        or not isinstance(input_hashes.get("uv_lock_sha256"), str)
        or not SHA256_RE.fullmatch(input_hashes["uv_lock_sha256"])
        or not isinstance(input_hashes.get("pyproject_sha256"), str)
        or not SHA256_RE.fullmatch(input_hashes["pyproject_sha256"])
    ):
        raise ReleaseCandidateError("Python license evidence package counts are inconsistent")
    for distribution in distributions:
        if (
            not isinstance(distribution, dict)
            or not isinstance(distribution.get("normalized_name"), str)
            or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", distribution["normalized_name"])
            or not isinstance(distribution.get("version"), str)
            or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.!+_-]{0,254}", distribution["version"])
        ):
            raise ReleaseCandidateError("Python license evidence has an invalid installed package identity")
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
    return {
        "locked_package_count": locked_count,
        "uv_lock_sha256": input_hashes["uv_lock_sha256"],
        "pyproject_sha256": input_hashes["pyproject_sha256"],
        "distributions": distributions,
    }


def _validate_python_lock_sbom(
    data: bytes,
    *,
    version: str,
    source_commit: str,
    lock_sha256: str,
    project_sha256: str,
    locked_package_count: int,
    installed_distributions: list[dict[str, Any]],
) -> None:
    document = _decode_json(data, "Python dependency SPDX SBOM", max_bytes=32 * 1024 * 1024)
    if (
        set(document) != {
            "spdxVersion", "dataLicense", "SPDXID", "name", "documentNamespace", "creationInfo",
            "documentDescribes", "packages", "relationships",
        }
        or
        document.get("spdxVersion") != "SPDX-2.3"
        or document.get("dataLicense") != "CC0-1.0"
        or document.get("SPDXID") != "SPDXRef-DOCUMENT"
        or document.get("name") != f"threadforge-python-{version}-all-extras-lock"
    ):
        raise ReleaseCandidateError("Python dependency SPDX document identity is invalid")

    creation = document.get("creationInfo")
    prefix = "threadforge.python-lock-sbom-binding.v1 "
    comment = creation.get("comment") if isinstance(creation, dict) else None
    if not isinstance(comment, str) or not comment.startswith(prefix):
        raise ReleaseCandidateError("Python dependency SPDX binding is missing")
    binding = _decode_json(comment[len(prefix):].encode("utf-8"), "Python dependency SPDX binding", max_bytes=16_384)
    expected_binding_keys = {
        "schema", "source_commit", "release_version", "uv_lock_sha256", "pyproject_sha256",
        "package_record_count", "extras",
    }
    if (
        set(binding) != expected_binding_keys
        or binding.get("schema") != "threadforge.python-lock-sbom-binding.v1"
        or binding.get("source_commit") != source_commit
        or binding.get("release_version") != version
        or binding.get("uv_lock_sha256") != lock_sha256
        or binding.get("pyproject_sha256") != project_sha256
        or binding.get("package_record_count") != locked_package_count
        or binding.get("extras") != PYTHON_EXTRAS
    ):
        raise ReleaseCandidateError("Python dependency SPDX binding does not match the candidate evidence")
    digest = hashlib.sha256(_canonical_json(binding).encode("utf-8")).hexdigest()
    expected_namespace = f"https://threadforge.dev/spdx/python-lock/{version}/{digest}"
    if document.get("documentNamespace") != expected_namespace:
        raise ReleaseCandidateError("Python dependency SPDX namespace is not bound to its manifest")
    if (
        set(creation) != {"creators", "created", "comment"}
        or
        creation.get("creators") != ["Tool: Threadforge Python lock SBOM builder"]
        or not isinstance(creation.get("created"), str)
        or not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", creation["created"])
    ):
        raise ReleaseCandidateError("Python dependency SPDX creation metadata is invalid")

    packages = document.get("packages")
    if not isinstance(packages, list) or len(packages) != locked_package_count or not packages:
        raise ReleaseCandidateError("Python dependency SPDX package count does not match uv.lock")
    by_id: dict[str, dict[str, Any]] = {}
    identities: set[tuple[str, str]] = set()
    project_ids: list[str] = []
    for package in packages:
        if not isinstance(package, dict):
            raise ReleaseCandidateError("Python dependency SPDX package is malformed")
        package_id = package.get("SPDXID")
        name = package.get("name")
        package_version = package.get("versionInfo")
        if (
            set(package) - {
                "SPDXID", "name", "versionInfo", "downloadLocation", "filesAnalyzed", "licenseConcluded",
                "licenseDeclared", "copyrightText", "primaryPackagePurpose", "externalRefs", "sourceInfo",
            }
            or
            not isinstance(package_id, str)
            or not re.fullmatch(r"SPDXRef-Package-[0-9]{6}", package_id)
            or package_id in by_id
            or not isinstance(name, str)
            or not re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9._-]{0,253}[A-Za-z0-9])?", name)
            or not isinstance(package_version, str)
            or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.!+_-]{0,254}", package_version)
            or package.get("downloadLocation") != "NOASSERTION"
            or package.get("filesAnalyzed") is not False
            or package.get("licenseConcluded") != "NOASSERTION"
            or package.get("licenseDeclared") != "NOASSERTION"
            or package.get("copyrightText") != "NOASSERTION"
        ):
            raise ReleaseCandidateError("Python dependency SPDX package metadata is invalid or makes license claims")
        normalized_name = re.sub(r"[-_.]+", "-", name).lower()
        identity = (normalized_name, package_version)
        if identity in identities:
            raise ReleaseCandidateError("Python dependency SPDX contains duplicate package identities")
        identities.add(identity)
        by_id[package_id] = package
        refs = package.get("externalRefs")
        expected_purl = f"pkg:pypi/{quote(normalized_name, safe='-._~')}@{quote(package_version, safe='-._~+!')}"
        if refs != [{
            "referenceCategory": "PACKAGE-MANAGER",
            "referenceType": "purl",
            "referenceLocator": expected_purl,
        }]:
            raise ReleaseCandidateError("Python dependency SPDX package has an invalid PyPI package URL")
        if normalized_name == "threadforge-python":
            project_ids.append(package_id)
            if (
                package_version != version
                or package.get("primaryPackagePurpose") != "APPLICATION"
                or package.get("sourceInfo")
                != f"Candidate version {version}; all declared extras resolved from uv.lock SHA-256 "
                f"{lock_sha256} and pyproject.toml SHA-256 {project_sha256}."
            ):
                raise ReleaseCandidateError("Python dependency SPDX root package does not match the release")
        elif "sourceInfo" in package or "primaryPackagePurpose" in package:
            raise ReleaseCandidateError("Python dependency SPDX third-party package purpose is invalid")

    if len(project_ids) != 1:
        raise ReleaseCandidateError("Python dependency SPDX must describe exactly one Threadforge package")
    described = document.get("documentDescribes")
    if described != project_ids:
        raise ReleaseCandidateError("Python dependency SPDX does not describe the candidate package")
    relationships = document.get("relationships")
    if not isinstance(relationships, list) or len(relationships) > 100_001:
        raise ReleaseCandidateError("Python dependency SPDX relationship graph is malformed")
    known_ids = set(by_id) | {"SPDXRef-DOCUMENT"}
    described_edges: list[dict[str, Any]] = []
    adjacency: dict[str, set[str]] = {}
    edge_keys: set[tuple[str, str, str, str]] = set()
    for relationship in relationships:
        if not isinstance(relationship, dict):
            raise ReleaseCandidateError("Python dependency SPDX contains an invalid relationship")
        source = relationship.get("spdxElementId")
        target = relationship.get("relatedSpdxElement")
        relation_type = relationship.get("relationshipType")
        comment = relationship.get("comment", "")
        if (
            not isinstance(source, str)
            or not isinstance(target, str)
            or not isinstance(relation_type, str)
            or
            source not in known_ids
            or target not in known_ids
            or relation_type not in {"DESCRIBES", "DEPENDS_ON"}
            or not isinstance(comment, str)
            or len(comment) > 8192
            or not re.fullmatch(r"[-A-Za-z0-9_ .<>=!(),;']*", comment)
        ):
            raise ReleaseCandidateError("Python dependency SPDX relationship references invalid elements")
        key = (source, relation_type, target, comment)
        if key in edge_keys:
            raise ReleaseCandidateError("Python dependency SPDX contains duplicate relationships")
        edge_keys.add(key)
        if relation_type == "DESCRIBES":
            described_edges.append(relationship)
            if source != "SPDXRef-DOCUMENT":
                raise ReleaseCandidateError("Python dependency SPDX DESCRIBES relationship is invalid")
        else:
            if source == "SPDXRef-DOCUMENT" or target == "SPDXRef-DOCUMENT":
                raise ReleaseCandidateError("Python dependency SPDX dependency edge references the document")
            adjacency.setdefault(source, set()).add(target)
    if (
        len(described_edges) != 1
        or described_edges[0].get("relatedSpdxElement") != project_ids[0]
        or "files" in document
    ):
        raise ReleaseCandidateError("Python dependency SPDX document relationships are inconsistent")
    reachable = {project_ids[0]}
    pending = [project_ids[0]]
    while pending:
        for target in adjacency.get(pending.pop(), set()):
            if target not in reachable:
                reachable.add(target)
                pending.append(target)
    if reachable != set(by_id):
        raise ReleaseCandidateError("Python dependency SPDX does not connect every locked package to the project")

    for distribution in installed_distributions:
        identity = (distribution["normalized_name"], distribution["version"])
        if identity[0] == "threadforge-python":
            if not any(name == identity[0] for name, _ in identities):
                raise ReleaseCandidateError("Python dependency SPDX omits the installed Threadforge project")
        elif identity not in identities:
            raise ReleaseCandidateError("Python dependency SPDX omits an installed locked distribution")


def _validate_python_distribution_metadata(data: bytes, *, version: str, label: str) -> None:
    if len(data) > 1024 * 1024:
        raise ReleaseCandidateError(f"Python {label} metadata exceeds the size limit")
    try:
        message = BytesParser().parsebytes(data)
    except (TypeError, ValueError) as error:
        raise ReleaseCandidateError(f"Python {label} metadata is malformed") from error
    names = message.get_all("Name", [])
    versions = message.get_all("Version", [])
    canonical_names = [re.sub(r"[-_.]+", "-", name).casefold() for name in names if isinstance(name, str)]
    if canonical_names != ["threadforge-python"] or versions != [version]:
        raise ReleaseCandidateError(f"Python {label} name or version does not match the candidate")


def _validate_python_sdist(path: Path, *, version: str) -> None:
    _require_regular_file(path)
    expected_root = f"threadforge_python-{version}"
    roots: set[str] = set()
    seen: set[str] = set()
    files: dict[str, bytes] = {}
    expanded = 0
    try:
        with tarfile.open(path, mode="r:gz") as archive:
            for index, member in enumerate(archive):
                if index >= MAX_PYTHON_DISTRIBUTION_MEMBERS:
                    raise ReleaseCandidateError("Python source archive contains too many members")
                raw_name = member.name
                if not raw_name or "\\" in raw_name or "\x00" in raw_name:
                    raise ReleaseCandidateError("Python source archive contains an unsafe member path")
                if member.isdir() and raw_name.endswith("/"):
                    raw_name = raw_name[:-1]
                name = _safe_member_name(raw_name)
                parts = PurePosixPath(name).parts
                if not parts or (len(parts) == 1 and not member.isdir()):
                    raise ReleaseCandidateError("Python source archive member is outside its root directory")
                if name in seen or not (member.isdir() or member.isfile()) or member.size < 0:
                    raise ReleaseCandidateError("Python source archive contains duplicate or non-regular members")
                seen.add(name)
                roots.add(parts[0])
                if member.isdir():
                    continue
                relative = "/".join(parts[1:])
                expanded += member.size
                if expanded > MAX_ARCHIVE_UNPACKED_BYTES:
                    raise ReleaseCandidateError("Python source archive exceeds the expanded size limit")
                source = archive.extractfile(member)
                if source is None:
                    raise ReleaseCandidateError("Python source archive member cannot be read")
                with source:
                    content = source.read(member.size + 1)
                if len(content) != member.size:
                    raise ReleaseCandidateError("Python source archive member size does not match its header")
                files[relative] = content
    except ReleaseCandidateError:
        raise
    except (OSError, tarfile.TarError, EOFError) as error:
        raise ReleaseCandidateError("Python source archive is malformed") from error
    if roots != {expected_root}:
        raise ReleaseCandidateError("Python source archive root does not match the candidate version")
    required = PYTHON_DISTRIBUTION_FILES | {
        "PKG-INFO", "LICENSE.threadforge", "NOTICE.threadforge", "THIRD_PARTY.md",
        "LICENSE.anyjev", "LICENSE.rrsi", "CREDITS.anyjev.md", "UPSTREAM_MODIFICATIONS.md",
    }
    if not required.issubset(files):
        raise ReleaseCandidateError("Python source archive omits required runtime files or upstream notices")
    _validate_python_distribution_metadata(files["PKG-INFO"], version=version, label="source")
    _validate_python_package_paths(files)


def _validate_python_wheel(path: Path, *, version: str) -> None:
    members = _read_zip(path, max_members=MAX_PYTHON_DISTRIBUTION_MEMBERS)
    metadata_names = [
        name for name in members
        if len(PurePosixPath(name).parts) == 2
        and PurePosixPath(name).parts[0].endswith(".dist-info")
        and PurePosixPath(name).parts[1] == "METADATA"
    ]
    expected_dist_info = f"threadforge_python-{version}.dist-info"
    if len(metadata_names) != 1 or PurePosixPath(metadata_names[0]).parts[0] != expected_dist_info:
        raise ReleaseCandidateError("Python wheel dist-info identity does not match the candidate")
    _validate_python_distribution_metadata(members[metadata_names[0]], version=version, label="wheel")
    required = PYTHON_DISTRIBUTION_FILES | {
        f"{expected_dist_info}/licenses/LICENSE.threadforge",
        f"{expected_dist_info}/licenses/LICENSE.anyjev",
        f"{expected_dist_info}/licenses/LICENSE.rrsi",
        f"{expected_dist_info}/RECORD",
    }
    if not required.issubset(members):
        raise ReleaseCandidateError("Python wheel omits required runtime files or upstream notices")
    _validate_python_package_paths(members)


def _validate_python_package_paths(files: dict[str, bytes]) -> None:
    for name in files:
        lowered = name.casefold()
        if lowered.endswith(PYTHON_FORBIDDEN_SUFFIXES):
            raise ReleaseCandidateError("Python distribution includes a model or benchmark binary payload")
        if "/bench/results/" in f"/{lowered}" or "/datasets/" in f"/{lowered}":
            raise ReleaseCandidateError("Python distribution includes benchmark results or datasets")


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
    python_profiles: list[dict[str, Any]] = []
    for system, python_version in PYTHON_TARGETS:
        name = f"threadforge_{version}_python_{system}_py{python_version}.license-evidence.tar.gz"
        python_profiles.append(_validate_python_evidence(
            entries[name].read_bytes(),
            system=system,
            python_version=python_version,
            release_version=version,
            source_commit=source_commit,
        ))
    lock_hashes = {profile["uv_lock_sha256"] for profile in python_profiles}
    project_hashes = {profile["pyproject_sha256"] for profile in python_profiles}
    lock_counts = {profile["locked_package_count"] for profile in python_profiles}
    if len(lock_hashes) != 1 or len(project_hashes) != 1 or len(lock_counts) != 1:
        raise ReleaseCandidateError("Python profile evidence does not share one locked dependency input set")
    installed_distributions = [
        distribution for profile in python_profiles for distribution in profile["distributions"]
    ]
    lock_sbom_name = f"threadforge_{version}_python.spdx.json"
    _validate_python_lock_sbom(
        entries[lock_sbom_name].read_bytes(),
        version=version,
        source_commit=source_commit,
        lock_sha256=next(iter(lock_hashes)),
        project_sha256=next(iter(project_hashes)),
        locked_package_count=next(iter(lock_counts)),
        installed_distributions=installed_distributions,
    )
    _validate_python_sdist(entries[f"threadforge_python-{version}.tar.gz"], version=version)
    _validate_python_wheel(entries[f"threadforge_python-{version}-py3-none-any.whl"], version=version)
    return expected, checksums


def _canonical_json(value: Any) -> str:
    try:
        return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
    except (TypeError, ValueError) as error:
        raise ReleaseCandidateError("attestation contains invalid JSON data") from error


def _is_transient_attestation_error(stderr: Any) -> bool:
    if not isinstance(stderr, str):
        return False
    lowered = stderr.casefold()
    return any(marker in lowered for marker in TRANSIENT_ATTESTATION_ERROR_MARKERS)


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
    result: subprocess.CompletedProcess[str] | None = None
    for attempt in range(len(ATTESTATION_RETRY_DELAYS_SECONDS) + 1):
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
        except subprocess.TimeoutExpired as error:
            if attempt < len(ATTESTATION_RETRY_DELAYS_SECONDS):
                time.sleep(ATTESTATION_RETRY_DELAYS_SECONDS[attempt])
                continue
            raise ReleaseCandidateError("GitHub attestation verification could not run") from error
        except (OSError, subprocess.SubprocessError) as error:
            raise ReleaseCandidateError("GitHub attestation verification could not run") from error
        if result.returncode == 0:
            break
        if attempt < len(ATTESTATION_RETRY_DELAYS_SECONDS) and _is_transient_attestation_error(result.stderr):
            time.sleep(ATTESTATION_RETRY_DELAYS_SECONDS[attempt])
            continue
        raise ReleaseCandidateError(f"GitHub attestation verification failed for {path.name}")
    if result is None or result.returncode != 0:
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
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository) or any(
        part in {".", ".."} for part in repository.split("/")
    ):
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
        "checksums_sha256": _sha256_file(bundle_dir / "checksums.txt"),
        "payload_count": len(expected),
        "checksum_count": len(checksums),
        "slsa_attestations_verified": len(expected) + 1,
        "archive_sbom_attestations_verified": len(GO_TARGETS),
        "python_lock_sboms_verified": 1,
        "python_distribution_artifacts_verified": 2,
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
