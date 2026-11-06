#!/usr/bin/env python3
"""Collect bounded, path-free license metadata and notices from one Python env.

This is machine-collected evidence only. Package metadata is untrusted input;
the output never approves a license or grants release clearance.
"""

from __future__ import annotations

import argparse
import csv
import gzip
import hashlib
import importlib.metadata
import io
import json
import platform
import re
import stat
import sys
import tarfile
import tomllib
from email.message import Message
from pathlib import Path, PurePosixPath
from typing import Any, Iterable


EVIDENCE_SCHEMA = "threadforge.python-license-evidence.v2"
SOURCE_COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
RELEASE_VERSION_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
DIST_NAME_RE = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,253}[A-Za-z0-9])?$")
DIST_VERSION_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9.!+_-]{0,254}$")
SAFE_LICENSE_EXPRESSION_RE = re.compile(r"^[A-Za-z0-9.+()\- \t]{1,1024}$")
SAFE_CLASSIFIER_RE = re.compile(r"^License :: [A-Za-z0-9 .:/()+_-]{1,240}$")
LICENSE_FILENAME_RE = re.compile(r"^(?:license|licence|copying|notice)(?:$|[._ -])", re.IGNORECASE)
SAFE_PATH_COMPONENT_RE = re.compile(r"^[^\x00-\x1f\x7f:/\\]{1,255}$")

MAX_DISTRIBUTIONS = 10_000
MAX_LICENSE_FILES = 20_000
MAX_SINGLE_FILE_BYTES = 16 * 1024 * 1024
MAX_TOTAL_LICENSE_BYTES = 128 * 1024 * 1024
MAX_METADATA_BYTES = 4 * 1024 * 1024
MAX_REPORT_BYTES = 16 * 1024 * 1024
ALLOWED_TARGET_OS = {"linux", "windows", "darwin"}


class LicenseEvidenceError(ValueError):
    """Raised when evidence is unsafe, incomplete, or outside configured bounds."""


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _sha256_lf_normalized_bytes(data: bytes) -> str:
    """Hash text-file content consistently across LF and CRLF checkouts."""
    return _sha256(data.replace(b"\r\n", b"\n"))


def _sha256_source_file(path: Path) -> str:
    digest = hashlib.sha256()
    pending = b""
    total_bytes = 0
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                total_bytes += len(chunk)
                if total_bytes > MAX_METADATA_BYTES:
                    raise LicenseEvidenceError("Python evidence collector source exceeds the 4 MiB limit")
                chunk = pending + chunk
                pending = b""
                if chunk.endswith(b"\r"):
                    pending = b"\r"
                    chunk = chunk[:-1]
                digest.update(chunk.replace(b"\r\n", b"\n"))
        if pending:
            digest.update(pending)
    except LicenseEvidenceError:
        raise
    except OSError as error:
        raise LicenseEvidenceError("cannot hash the Python evidence collector source") from error
    return digest.hexdigest()


def _normalize_distribution_name(name: str) -> str:
    if not isinstance(name, str) or not DIST_NAME_RE.fullmatch(name):
        raise LicenseEvidenceError("installed distribution has an unsafe or missing name")
    return re.sub(r"[-_.]+", "-", name).casefold()


def _validate_distribution_version(version: str) -> str:
    if not isinstance(version, str) or not DIST_VERSION_RE.fullmatch(version):
        raise LicenseEvidenceError("installed distribution has an unsafe or missing version")
    return version


def _safe_relative_path(value: Any) -> PurePosixPath:
    if not isinstance(value, str):
        as_posix = getattr(value, "as_posix", None)
        if not callable(as_posix):
            raise LicenseEvidenceError("installed distribution contains an invalid file path")
        value = as_posix()
    if not isinstance(value, str) or not value or "\\" in value:
        raise LicenseEvidenceError("installed distribution contains an unsafe file path")
    path = PurePosixPath(value)
    if path.is_absolute() or not path.parts or any(part in {"", ".", ".."} for part in path.parts):
        raise LicenseEvidenceError("installed distribution contains an unsafe file path")
    if any(not SAFE_PATH_COMPONENT_RE.fullmatch(part) for part in path.parts):
        raise LicenseEvidenceError("installed distribution contains an unsafe file path")
    return path


def _distribution_file_name(value: Any) -> str:
    name = getattr(value, "name", None)
    if isinstance(name, str):
        return name
    as_posix = getattr(value, "as_posix", None)
    if not callable(as_posix):
        return ""
    return as_posix().rsplit("/", 1)[-1]


def _read_regular_file(root: Path, relative: PurePosixPath, maximum: int) -> bytes:
    current = root
    try:
        for index, component in enumerate(relative.parts):
            current = current / component
            mode = current.lstat().st_mode
            attributes = getattr(current.lstat(), "st_file_attributes", 0)
            reparse_flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
            if stat.S_ISLNK(mode) or attributes & reparse_flag:
                raise LicenseEvidenceError("installed distribution contains a linked license path")
            if index < len(relative.parts) - 1 and not stat.S_ISDIR(mode):
                raise LicenseEvidenceError("installed distribution has a non-directory license path")
        if not stat.S_ISREG(current.lstat().st_mode):
            raise LicenseEvidenceError("installed distribution contains a non-regular license file")
        size = current.stat().st_size
        if size > maximum:
            raise LicenseEvidenceError("a Python license-evidence file exceeds its size limit")
        with current.open("rb") as stream:
            data = stream.read(maximum + 1)
    except LicenseEvidenceError:
        raise
    except OSError as error:
        raise LicenseEvidenceError("cannot read a Python license-evidence file") from error
    if len(data) > maximum:
        raise LicenseEvidenceError("a Python license-evidence file exceeds its size limit")
    return data


def _validate_root(value: Any) -> Path:
    if not isinstance(value, Path):
        raise LicenseEvidenceError("installed distribution root is invalid")
    try:
        root = value.resolve(strict=True)
    except OSError as error:
        raise LicenseEvidenceError("installed distribution root is unavailable") from error
    if not root.is_dir():
        raise LicenseEvidenceError("installed distribution root is not a directory")
    return root


def _load_lock_versions(lock_path: Path) -> tuple[dict[tuple[str, str], bool], str]:
    try:
        lock_bytes = lock_path.read_bytes()
        if len(lock_bytes) > MAX_METADATA_BYTES:
            raise LicenseEvidenceError("Python lockfile exceeds the 4 MiB limit")
        document = tomllib.loads(lock_bytes.decode("utf-8"))
    except LicenseEvidenceError:
        raise
    except (OSError, UnicodeDecodeError, tomllib.TOMLDecodeError) as error:
        raise LicenseEvidenceError("cannot parse the Python lockfile") from error
    packages = document.get("package")
    if not isinstance(packages, list) or not packages:
        raise LicenseEvidenceError("Python lockfile contains no package records")
    versions: dict[tuple[str, str], bool] = {}
    for package in packages:
        if not isinstance(package, dict):
            raise LicenseEvidenceError("Python lockfile contains an invalid package record")
        name = _normalize_distribution_name(package.get("name"))
        version = _validate_distribution_version(package.get("version"))
        key = (name, version)
        if key in versions:
            raise LicenseEvidenceError("Python lockfile contains a duplicate package identity")
        versions[key] = True
    return versions, _sha256_lf_normalized_bytes(lock_bytes)


def _load_project(path: Path, extras: list[str]) -> tuple[str, str, str, str]:
    try:
        project_bytes = path.read_bytes()
        if len(project_bytes) > MAX_METADATA_BYTES:
            raise LicenseEvidenceError("Python project file exceeds the 4 MiB limit")
        document = tomllib.loads(project_bytes.decode("utf-8"))
    except LicenseEvidenceError:
        raise
    except (OSError, UnicodeDecodeError, tomllib.TOMLDecodeError) as error:
        raise LicenseEvidenceError("cannot parse the Python project file") from error
    project = document.get("project")
    if not isinstance(project, dict):
        raise LicenseEvidenceError("Python project metadata is missing")
    name = _normalize_distribution_name(project.get("name"))
    version = _validate_distribution_version(project.get("version"))
    optional = project.get("optional-dependencies", {})
    if not isinstance(optional, dict):
        raise LicenseEvidenceError("Python optional dependencies are invalid")
    if not extras or len(set(extras)) != len(extras) or any(extra not in optional for extra in extras):
        raise LicenseEvidenceError("requested Python extras are empty, duplicated, or undeclared")
    return name, version, _sha256_lf_normalized_bytes(project_bytes), str(project.get("license", ""))


def _license_expression(metadata: Message) -> tuple[str | None, str, int, bool]:
    raw = metadata.get("License-Expression")
    if raw is None:
        return None, "missing", 0, False
    encoded = str(raw).encode("utf-8", errors="replace")
    if len(encoded) > MAX_METADATA_BYTES:
        raise LicenseEvidenceError("distribution license-expression metadata exceeds its size limit")
    if SAFE_LICENSE_EXPRESSION_RE.fullmatch(str(raw)):
        return str(raw), "captured_unreviewed", len(encoded), False
    return None, "omitted_unsafe_metadata", len(encoded), True


def _license_classifiers(metadata: Message) -> tuple[list[str], int, bool]:
    raw_values = metadata.get_all("Classifier", []) or []
    selected: list[str] = []
    invalid = False
    license_values = [str(value) for value in raw_values if str(value).startswith("License ::")]
    if len(license_values) > 1000:
        raise LicenseEvidenceError("distribution has too many license classifiers")
    for value in license_values:
        if SAFE_CLASSIFIER_RE.fullmatch(value):
            selected.append(value)
        else:
            invalid = True
    if len(selected) > 64:
        raise LicenseEvidenceError("distribution has too many license classifiers")
    return sorted(set(selected)), len(license_values), invalid


def _metadata_file_digest(distribution: Any, root: Path) -> str | None:
    files = distribution.files
    if files is None:
        return None
    matches = []
    for item in files:
        if _distribution_file_name(item).casefold() == "metadata":
            relative = _safe_relative_path(item)
            if len(relative.parts) == 2 and relative.parts[0].casefold().endswith(".dist-info"):
                matches.append(relative)
    if len(matches) > 1:
        raise LicenseEvidenceError("distribution contains multiple root METADATA files")
    if not matches:
        return None
    data = _read_regular_file(root, matches[0], MAX_METADATA_BYTES)
    return _sha256(data)


def _distribution_evidence(
    distribution: Any,
    lock_versions: dict[tuple[str, str], bool],
    license_payloads: dict[str, bytes],
    total_license_bytes: list[int],
) -> dict[str, Any]:
    metadata = distribution.metadata
    if not isinstance(metadata, Message):
        raise LicenseEvidenceError("installed distribution metadata has an invalid shape")
    name = metadata.get("Name")
    version = metadata.get("Version")
    normalized_name = _normalize_distribution_name(name)
    version = _validate_distribution_version(version)
    root = _validate_root(Path(distribution.locate_file("")))
    expression, expression_status, expression_bytes, expression_malformed = _license_expression(metadata)
    classifiers, classifier_count, classifier_malformed = _license_classifiers(metadata)
    legacy_license = metadata.get("License")
    legacy_bytes = str(legacy_license).encode("utf-8", errors="replace") if legacy_license is not None else b""
    if len(legacy_bytes) > MAX_METADATA_BYTES:
        raise LicenseEvidenceError("distribution legacy-license metadata exceeds its size limit")
    legacy_truncated = False

    files = distribution.files
    license_files: list[dict[str, Any]] = []
    if files is not None:
        candidates = []
        for item in files:
            if LICENSE_FILENAME_RE.match(_distribution_file_name(item)):
                candidates.append(_safe_relative_path(item))
        for relative in sorted(set(candidates), key=lambda item: item.as_posix()):
            if len(license_files) >= MAX_LICENSE_FILES:
                raise LicenseEvidenceError("Python environment exceeds the 20,000 license-file limit")
            if len(license_payloads) >= MAX_LICENSE_FILES:
                raise LicenseEvidenceError("Python environment exceeds the 20,000 license-file limit")
            data = _read_regular_file(root, relative, MAX_SINGLE_FILE_BYTES)
            total_license_bytes[0] += len(data)
            if total_license_bytes[0] > MAX_TOTAL_LICENSE_BYTES:
                raise LicenseEvidenceError("Python license notices exceed the 128 MiB limit")
            archive_path = PurePosixPath("licenses", normalized_name, version, *relative.parts)
            archive_name = archive_path.as_posix()
            if archive_name in license_payloads:
                raise LicenseEvidenceError("duplicate Python license archive path")
            license_payloads[archive_name] = data
            license_files.append(
                {
                    "path": relative.as_posix(),
                    "archive_path": archive_name,
                    "size_bytes": len(data),
                    "sha256": _sha256(data),
                }
            )

    return {
        "name": str(name),
        "normalized_name": normalized_name,
        "version": version,
        "locked": lock_versions.get((normalized_name, version), False),
        "metadata_file_sha256": _metadata_file_digest(distribution, root),
        "license_expression": expression,
        "license_expression_status": expression_status,
        "license_expression_byte_count": expression_bytes,
        "license_expression_sha256": _sha256(str(metadata.get("License-Expression")).encode("utf-8", errors="replace"))
        if metadata.get("License-Expression") is not None
        else None,
        "license_classifiers": classifiers,
        "license_classifier_count": classifier_count,
        "license_classifier_metadata_malformed": classifier_malformed,
        "legacy_license_field_byte_count": len(legacy_bytes),
        "legacy_license_field_sha256": _sha256(legacy_bytes) if legacy_license is not None else None,
        "legacy_license_field_truncated_for_digest": legacy_truncated,
        "license_metadata_malformed": expression_malformed or classifier_malformed,
        "license_files": license_files,
    }


def _collect_distributions(
    distributions: Iterable[Any], lock_versions: dict[tuple[str, str], bool]
) -> tuple[list[dict[str, Any]], dict[str, bytes]]:
    items: list[dict[str, Any]] = []
    license_payloads: dict[str, bytes] = {}
    seen: set[str] = set()
    total_license_bytes = [0]
    for index, distribution in enumerate(distributions):
        if index >= MAX_DISTRIBUTIONS:
            raise LicenseEvidenceError("Python environment exceeds the 10,000 distribution limit")
        item = _distribution_evidence(distribution, lock_versions, license_payloads, total_license_bytes)
        if item["normalized_name"] in seen:
            raise LicenseEvidenceError("Python environment contains duplicate distributions")
        seen.add(item["normalized_name"])
        items.append(item)
        if len(json.dumps(items, sort_keys=True, separators=(",", ":")).encode("utf-8")) > MAX_METADATA_BYTES:
            raise LicenseEvidenceError("Python distribution metadata exceeds the 4 MiB limit")
    if not items:
        raise LicenseEvidenceError("Python environment contains no installed distributions")
    return sorted(items, key=lambda item: (item["normalized_name"], item["version"])), license_payloads


def _make_report(distributions: list[dict[str, Any]]) -> bytes:
    output = io.StringIO(newline="")
    writer = csv.writer(output, lineterminator="\n")
    writer.writerow(
        [
            "name",
            "version",
            "locked",
            "license_expression",
            "license_expression_status",
            "license_classifier_count",
            "legacy_license_field_sha256",
            "license_file_count",
        ]
    )
    for item in distributions:
        writer.writerow(
            [
                item["name"],
                item["version"],
                "true" if item["locked"] else "false",
                item["license_expression"] or "",
                item["license_expression_status"],
                item["license_classifier_count"],
                item["legacy_license_field_sha256"] or "",
                len(item["license_files"]),
            ]
        )
    data = output.getvalue().encode("utf-8")
    if len(data) > MAX_REPORT_BYTES:
        raise LicenseEvidenceError("Python license report exceeds the 16 MiB limit")
    return data


README = b"""# Threadforge Python license evidence\n\nThis archive records package metadata and license/notice files from one locked Python environment. Metadata and scanner output are unreviewed evidence, not legal advice or approval. A missing field or license file is reported as missing; no license is inferred from a package name, hash, classifier, or file content. `release_clearance` is always false.\n"""


def _write_deterministic_archive(
    output: Path, entries: dict[str, bytes], source_date_epoch: int
) -> None:
    if output.exists() or output.is_symlink():
        raise LicenseEvidenceError("refusing to overwrite an existing Python evidence archive")
    if not output.parent.is_dir():
        raise LicenseEvidenceError("Python evidence archive parent directory must already exist")
    parent = output.parent.absolute()
    current = Path(parent.anchor)
    for component in parent.parts[1:]:
        current = current / component
        try:
            mode = current.lstat().st_mode
            attributes = getattr(current.lstat(), "st_file_attributes", 0)
        except OSError as error:
            raise LicenseEvidenceError("cannot validate Python evidence archive parent") from error
        reparse_flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
        if stat.S_ISLNK(mode) or attributes & reparse_flag:
            raise LicenseEvidenceError("Python evidence archive parent contains a link")
        if not current.is_dir():
            raise LicenseEvidenceError("Python evidence archive parent is not a directory")
    temp_created = False
    try:
        with output.open("xb") as raw:
            temp_created = True
            with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=source_date_epoch, compresslevel=9) as compressed:
                with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as archive:
                    for name, data in sorted(entries.items()):
                        path = PurePosixPath(name)
                        if path.is_absolute() or any(part in {"", ".", ".."} for part in path.parts):
                            raise LicenseEvidenceError("Python evidence archive contains an unsafe member path")
                        info = tarfile.TarInfo(name=path.as_posix())
                        info.size = len(data)
                        info.mode = 0o644
                        info.uid = 0
                        info.gid = 0
                        info.uname = ""
                        info.gname = ""
                        info.mtime = source_date_epoch
                        archive.addfile(info, io.BytesIO(data))
    except LicenseEvidenceError:
        if temp_created:
            output.unlink(missing_ok=True)
        raise
    except (OSError, tarfile.TarError) as error:
        if temp_created:
            output.unlink(missing_ok=True)
        raise LicenseEvidenceError("cannot write deterministic Python evidence archive") from error


def build_python_license_evidence(
    *,
    source_commit: str,
    release_version: str,
    target_os: str,
    python_version: str,
    extras: list[str],
    lock_path: Path,
    project_path: Path,
    output_path: Path,
    source_date_epoch: int = 0,
    distributions: Iterable[Any] | None = None,
    host_system: str | None = None,
    host_python_version: str | None = None,
    host_runtime_version: str | None = None,
) -> dict[str, Any]:
    if not SOURCE_COMMIT_RE.fullmatch(source_commit):
        raise LicenseEvidenceError("source commit must be a full lowercase Git SHA-1")
    if not RELEASE_VERSION_RE.fullmatch(release_version):
        raise LicenseEvidenceError("release version must be stable MAJOR.MINOR.PATCH")
    if target_os not in ALLOWED_TARGET_OS:
        raise LicenseEvidenceError("unsupported Python evidence target OS")
    if python_version not in {"3.11", "3.12"}:
        raise LicenseEvidenceError("unsupported Python evidence version")
    if not isinstance(source_date_epoch, int) or source_date_epoch < 0 or source_date_epoch > 2**32 - 1:
        raise LicenseEvidenceError("source date epoch is outside the supported range")

    actual_system = platform.system() if host_system is None else host_system
    runtime_version = (
        f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}"
        if host_runtime_version is None
        else host_runtime_version
    )
    actual_python = ".".join(runtime_version.split(".")[:2])
    host_os = {"Linux": "linux", "Windows": "windows", "Darwin": "darwin"}.get(actual_system)
    if (
        host_os != target_os
        or actual_python != python_version
        or (host_python_version is not None and host_python_version != actual_python)
    ):
        raise LicenseEvidenceError("requested target does not match the running Python environment")

    lock_versions, lock_sha256 = _load_lock_versions(lock_path)
    project_name, project_version, project_sha256, project_license = _load_project(project_path, extras)
    selected_distributions = list(importlib.metadata.distributions() if distributions is None else distributions)
    items, license_payloads = _collect_distributions(selected_distributions, lock_versions)
    if len(items) > MAX_DISTRIBUTIONS:
        raise LicenseEvidenceError("Python environment exceeds the 10,000 distribution limit")
    if len(json.dumps(items, sort_keys=True, separators=(",", ":")).encode("utf-8")) > MAX_METADATA_BYTES:
        raise LicenseEvidenceError("Python distribution metadata exceeds the 4 MiB limit")

    project_matches = [item for item in items if item["normalized_name"] == project_name]
    if len(project_matches) != 1 or project_matches[0]["version"] != project_version:
        raise LicenseEvidenceError("installed Threadforge Python package does not match pyproject metadata")

    no_declared_license = sum(
        item["license_expression"] is None
        and not item["license_classifiers"]
        and item["legacy_license_field_sha256"] is None
        for item in items
    )
    license_file_count = sum(len(item["license_files"]) for item in items)
    missing_license_files = sum(not item["license_files"] for item in items)
    malformed_metadata = sum(item["license_metadata_malformed"] for item in items)
    unlocked_count = sum(not item["locked"] for item in items)
    if unlocked_count:
        raise LicenseEvidenceError("installed Python distributions are not fully represented in uv.lock")
    evidence: dict[str, Any] = {
        "schema": EVIDENCE_SCHEMA,
        "source_commit": source_commit,
        "release_version": release_version,
        "target": {"os": target_os, "python_version": python_version},
        "scope": {"project": project_name, "project_version": project_version, "extras": sorted(extras)},
        "environment": {
            "implementation": sys.implementation.name,
            "runtime_version": runtime_version,
            "platform_system": actual_system,
        },
        "input_hashes": {"uv_lock_sha256": lock_sha256, "pyproject_sha256": project_sha256},
        "locked_package_count": len(lock_versions),
        "installed_distribution_count": len(items),
        "unlocked_distribution_count": unlocked_count,
        "license_summary": {
            "distributions_without_license_metadata": no_declared_license,
            "distributions_without_license_files": missing_license_files,
            "license_file_count": license_file_count,
            "license_file_bytes": sum(len(data) for data in license_payloads.values()),
            "malformed_license_metadata_count": malformed_metadata,
        },
        "license_review_status": "unreviewed",
        "license_review_required": True,
        "release_clearance": False,
        "project_declared_license": project_license if SAFE_LICENSE_EXPRESSION_RE.fullmatch(project_license) else None,
        "distributions": items,
    }
    evidence_bytes = json.dumps(evidence, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8") + b"\n"
    report_bytes = _make_report(items)
    collector_path = Path(__file__).resolve()
    entries = {
        "python-license-evidence/README.md": README,
        "python-license-evidence/evidence.json": evidence_bytes,
        "python-license-evidence/report.csv": report_bytes,
    }
    entries.update({f"python-license-evidence/{name}": data for name, data in license_payloads.items()})
    evidence["collector_sha256"] = _sha256_source_file(collector_path)
    evidence_bytes = json.dumps(evidence, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8") + b"\n"
    entries["python-license-evidence/evidence.json"] = evidence_bytes
    _write_deterministic_archive(output_path, entries, source_date_epoch)
    return evidence


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--release-version", required=True)
    parser.add_argument("--target-os", required=True)
    parser.add_argument("--python-version", required=True)
    parser.add_argument("--extra", action="append", default=[])
    parser.add_argument("--lock-file", type=Path, default=Path(__file__).resolve().parents[1] / "python" / "uv.lock")
    parser.add_argument("--project-file", type=Path, default=Path(__file__).resolve().parents[1] / "python" / "pyproject.toml")
    parser.add_argument("--source-date-epoch", type=int, default=0)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        evidence = build_python_license_evidence(
            source_commit=args.source_commit,
            release_version=args.release_version,
            target_os=args.target_os,
            python_version=args.python_version,
            extras=args.extra,
            lock_path=args.lock_file,
            project_path=args.project_file,
            output_path=args.output,
            source_date_epoch=args.source_date_epoch,
        )
    except LicenseEvidenceError as error:
        print(json.dumps({"error": str(error), "release_clearance": False}, sort_keys=True), file=sys.stderr)
        return 2
    print(
        json.dumps(
            {
                "schema": evidence["schema"],
                "source_commit": evidence["source_commit"],
                "target": evidence["target"],
                "installed_distribution_count": evidence["installed_distribution_count"],
                "unlocked_distribution_count": evidence["unlocked_distribution_count"],
                "license_summary": evidence["license_summary"],
                "license_review_status": evidence["license_review_status"],
                "release_clearance": evidence["release_clearance"],
            },
            sort_keys=True,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
