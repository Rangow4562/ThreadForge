#!/usr/bin/env python3
"""Build a path-free SPDX inventory and dependency graph from the locked Python extras.

The result describes every package record in uv.lock, including all optional
extras and platform alternatives. License fields remain NOASSERTION; this is
dependency inventory evidence, not license review or a runtime OS SBOM.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import stat
import sys
import tomllib
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.parse import quote


MAX_LOCK_BYTES = 64 * 1024 * 1024
MAX_PROJECT_BYTES = 4 * 1024 * 1024
MAX_PACKAGES = 10_000
MAX_RELATIONSHIPS = 100_000
VERSION_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
NAME_RE = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,253}[A-Za-z0-9])?$")
PACKAGE_VERSION_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9.!+_-]{0,254}$")
SOURCE_COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
MARKER_RE = re.compile(r"^[A-Za-z0-9_ .<>=!()'\"]{1,1024}$")


class PythonLockSBOMError(ValueError):
    """The Python lock cannot be represented safely as an SPDX inventory."""


def _normalize_name(value: Any) -> str:
    if not isinstance(value, str) or not NAME_RE.fullmatch(value):
        raise PythonLockSBOMError("uv.lock contains an unsafe package name")
    return re.sub(r"[-_.]+", "-", value).lower()


def _validate_version(value: Any) -> str:
    if not isinstance(value, str) or not PACKAGE_VERSION_RE.fullmatch(value):
        raise PythonLockSBOMError("uv.lock contains an unsafe package version")
    return value


def _read_regular(path: Path, limit: int, description: str) -> bytes:
    try:
        info = path.lstat()
    except OSError as error:
        raise PythonLockSBOMError(f"{description} is missing or cannot be inspected") from error
    attributes = getattr(info, "st_file_attributes", 0)
    if stat.S_ISLNK(info.st_mode) or attributes & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400):
        raise PythonLockSBOMError(f"{description} must not be a link")
    if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
        raise PythonLockSBOMError(f"{description} is not a bounded regular file")
    try:
        data = path.read_bytes()
    except OSError as error:
        raise PythonLockSBOMError(f"{description} cannot be read") from error
    if len(data) > limit:
        raise PythonLockSBOMError(f"{description} exceeds its size limit")
    return data


def _load_toml(path: Path, limit: int, description: str) -> tuple[dict[str, Any], bytes]:
    data = _read_regular(path, limit, description)
    try:
        value = tomllib.loads(data.decode("utf-8"))
    except (UnicodeDecodeError, tomllib.TOMLDecodeError) as error:
        raise PythonLockSBOMError(f"{description} is malformed TOML") from error
    if not isinstance(value, dict):
        raise PythonLockSBOMError(f"{description} root is not a TOML table")
    return value, data


def _source_key(value: Any) -> tuple[tuple[str, str], ...]:
    if not isinstance(value, dict):
        raise PythonLockSBOMError("uv.lock package source is invalid")
    if set(value) == {"registry"}:
        registry = value["registry"]
        if not isinstance(registry, str) or not registry.startswith("https://") or len(registry) > 2048:
            raise PythonLockSBOMError("uv.lock contains an unsupported registry source")
        return (("registry", registry),)
    if value == {"editable": "."}:
        return (("editable", "."),)
    raise PythonLockSBOMError("uv.lock contains a source type outside the reviewed Python lock scope")


def _package_purl(name: str, version: str) -> str:
    return f"pkg:pypi/{quote(name, safe='-._~')}@{quote(version, safe='-._~+!')}"


def _dependency_extras(value: Any) -> list[str]:
    if value is None:
        return []
    extras = [value] if isinstance(value, str) else value
    if not isinstance(extras, list) or any(not isinstance(item, str) or not item for item in extras):
        raise PythonLockSBOMError("uv.lock dependency extras are invalid")
    if len(extras) != len(set(extras)):
        raise PythonLockSBOMError("uv.lock dependency extras contain duplicates")
    return sorted(extras)


def _dependency_comment(
    *, marker: Any, extras: list[str], root_extra: str | None = None, selected_extra: str | None = None
) -> str | None:
    fields: list[str] = []
    if root_extra is not None:
        fields.append(f"project extra={root_extra}")
    if selected_extra is not None:
        fields.append(f"selected package extra={selected_extra}")
    if extras:
        fields.append("dependency extras=" + ",".join(extras))
    if marker is not None:
        if not isinstance(marker, str) or not MARKER_RE.fullmatch(marker):
            raise PythonLockSBOMError("uv.lock contains an invalid dependency marker")
        fields.append(f"resolution marker={marker}")
    return "; ".join(fields) if fields else None


def _canonical_json(value: Any) -> bytes:
    try:
        return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode("utf-8")
    except (TypeError, ValueError) as error:
        raise PythonLockSBOMError("could not serialize the Python SBOM") from error


def build_python_lock_sbom(
    *,
    release_version: str,
    source_commit: str,
    lock_path: Path,
    project_path: Path,
    output_path: Path,
    source_date_epoch: int = 0,
) -> dict[str, Any]:
    if not VERSION_RE.fullmatch(release_version):
        raise PythonLockSBOMError("release version must be stable MAJOR.MINOR.PATCH")
    if not SOURCE_COMMIT_RE.fullmatch(source_commit):
        raise PythonLockSBOMError("source commit must be a full lowercase Git SHA-1")
    if not isinstance(source_date_epoch, int) or not 0 <= source_date_epoch <= 2**32 - 1:
        raise PythonLockSBOMError("source date epoch is outside the supported range")

    lock, lock_bytes = _load_toml(lock_path, MAX_LOCK_BYTES, "uv.lock")
    project, project_bytes = _load_toml(project_path, MAX_PROJECT_BYTES, "pyproject.toml")
    project_data = project.get("project")
    optional = project_data.get("optional-dependencies") if isinstance(project_data, dict) else None
    if not isinstance(optional, dict) or not optional:
        raise PythonLockSBOMError("pyproject.toml must declare Python optional extras")
    extras = sorted(optional)

    records = lock.get("package")
    if not isinstance(records, list) or not 1 <= len(records) <= MAX_PACKAGES:
        raise PythonLockSBOMError("uv.lock package inventory is empty or exceeds its bound")
    by_identity: dict[tuple[str, str], dict[str, Any]] = {}
    by_name: dict[str, list[tuple[str, str]]] = {}
    source_by_identity: dict[tuple[str, str], tuple[tuple[str, str], ...]] = {}
    for record in records:
        if not isinstance(record, dict):
            raise PythonLockSBOMError("uv.lock contains a malformed package record")
        name = _normalize_name(record.get("name"))
        version = _validate_version(record.get("version"))
        identity = (name, version)
        if identity in by_identity:
            raise PythonLockSBOMError("uv.lock contains a duplicate normalized package identity")
        by_identity[identity] = record
        by_name.setdefault(name, []).append(identity)
        source_by_identity[identity] = _source_key(record.get("source"))

    project_name = _normalize_name(project_data.get("name"))
    project_version = _validate_version(project_data.get("version"))
    if project_name != "threadforge-python":
        raise PythonLockSBOMError("pyproject.toml does not identify the Threadforge Python package")
    root_identity = (project_name, project_version)
    if root_identity not in by_identity or source_by_identity[root_identity] != (("editable", "."),):
        raise PythonLockSBOMError("uv.lock does not contain the editable Threadforge project record")
    root_record = by_identity[root_identity]
    root_metadata = root_record.get("metadata")
    provides_extras = root_metadata.get("provides-extras") if isinstance(root_metadata, dict) else None
    if not isinstance(provides_extras, list) or sorted(provides_extras) != extras:
        raise PythonLockSBOMError("uv.lock extras do not match the project optional-dependency groups")

    ordered_identities = sorted(by_identity)
    package_ids = {identity: f"SPDXRef-Package-{index:06d}" for index, identity in enumerate(ordered_identities, 1)}
    lock_hash = hashlib.sha256(lock_bytes.replace(b"\r\n", b"\n")).hexdigest()
    project_hash = hashlib.sha256(project_bytes.replace(b"\r\n", b"\n")).hexdigest()

    def resolve(dependency: Any) -> tuple[str, str]:
        if not isinstance(dependency, dict):
            raise PythonLockSBOMError("uv.lock contains an invalid dependency reference")
        name = _normalize_name(dependency.get("name"))
        candidates = by_name.get(name, [])
        exact_version = dependency.get("version")
        if exact_version is not None:
            candidate = (name, _validate_version(exact_version))
            if candidate not in by_identity:
                raise PythonLockSBOMError("uv.lock dependency points to an absent package version")
            candidates = [candidate]
        source = dependency.get("source")
        if source is not None:
            source_key = _source_key(source)
            candidates = [candidate for candidate in candidates if source_by_identity[candidate] == source_key]
        if len(candidates) != 1:
            raise PythonLockSBOMError("uv.lock dependency does not resolve to exactly one package record")
        return candidates[0]

    packages: list[dict[str, Any]] = []
    for identity in ordered_identities:
        record = by_identity[identity]
        name, locked_version = identity
        version = release_version if identity == root_identity else locked_version
        package: dict[str, Any] = {
            "SPDXID": package_ids[identity],
            "name": str(record["name"]),
            "versionInfo": version,
            "downloadLocation": "NOASSERTION",
            "filesAnalyzed": False,
            "licenseConcluded": "NOASSERTION",
            "licenseDeclared": "NOASSERTION",
            "copyrightText": "NOASSERTION",
            "externalRefs": [{
                "referenceCategory": "PACKAGE-MANAGER",
                "referenceType": "purl",
                "referenceLocator": _package_purl(name, version),
            }],
        }
        if identity == root_identity:
            package["primaryPackagePurpose"] = "APPLICATION"
            package["sourceInfo"] = (
                f"Candidate version {release_version}; all declared extras resolved from uv.lock SHA-256 "
                f"{lock_hash} and pyproject.toml SHA-256 {project_hash}."
            )
        packages.append(package)

    root_optional = root_record.get("optional-dependencies", {})
    if not isinstance(root_optional, dict) or sorted(root_optional) != extras:
        raise PythonLockSBOMError("uv.lock root extras do not match pyproject.toml")
    edges: set[tuple[str, str, str]] = set()
    activated_extras: dict[tuple[str, str], set[str]] = {root_identity: set(extras)}
    processed_base: set[tuple[str, str]] = set()
    processed_extras: dict[tuple[str, str], set[str]] = {}
    pending = [root_identity]
    visited: set[tuple[str, str]] = set()
    while pending:
        identity = pending.pop()
        record = by_identity[identity]
        source_id = package_ids[identity]

        def add_dependency(dependency: Any, *, requested_by_extra: str | None = None) -> None:
            if not isinstance(dependency, dict):
                raise PythonLockSBOMError("uv.lock contains a malformed dependency reference")
            target_identity = resolve(dependency)
            requested_extras = _dependency_extras(dependency.get("extra"))
            comment = _dependency_comment(
                marker=dependency.get("marker"),
                extras=requested_extras,
                root_extra=requested_by_extra if identity == root_identity else None,
                selected_extra=requested_by_extra if identity != root_identity else None,
            )
            edges.add((source_id, package_ids[target_identity], comment or ""))
            target_extras = activated_extras.setdefault(target_identity, set())
            newly_activated = set(requested_extras) - target_extras
            if newly_activated:
                target_extras.update(newly_activated)
                pending.append(target_identity)
            elif target_identity not in visited:
                pending.append(target_identity)

        if identity not in processed_base:
            dependencies = record.get("dependencies", [])
            if not isinstance(dependencies, list):
                raise PythonLockSBOMError("uv.lock package dependencies are malformed")
            for dependency in dependencies:
                add_dependency(dependency)
            processed_base.add(identity)

        active = activated_extras.get(identity, set())
        done = processed_extras.setdefault(identity, set())
        optional_groups = record.get("optional-dependencies", {})
        if not isinstance(optional_groups, dict):
            raise PythonLockSBOMError("uv.lock package optional dependencies are malformed")
        for extra in sorted(active - done):
            entries = optional_groups.get(extra)
            if not isinstance(entries, list):
                raise PythonLockSBOMError("uv.lock omits a requested package extra")
            for dependency in entries:
                add_dependency(dependency, requested_by_extra=extra if identity == root_identity else None)
            done.add(extra)
        visited.add(identity)

    if len(edges) > MAX_RELATIONSHIPS:
        raise PythonLockSBOMError("uv.lock dependency graph exceeds its relationship bound")
    adjacency: dict[str, set[str]] = {}
    for source, target, _ in edges:
        adjacency.setdefault(source, set()).add(target)
    reachable = {package_ids[root_identity]}
    pending = [package_ids[root_identity]]
    while pending:
        for target in adjacency.get(pending.pop(), set()):
            if target not in reachable:
                reachable.add(target)
                pending.append(target)
    if reachable != set(package_ids.values()):
        unreachable = sorted(identity for identity, package_id in package_ids.items() if package_id not in reachable)
        sample = ", ".join(f"{name}=={version}" for name, version in unreachable[:12])
        raise PythonLockSBOMError(
            f"uv.lock contains {len(unreachable)} package records disconnected from Threadforge extras: {sample}"
        )

    created = datetime.fromtimestamp(source_date_epoch, tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    binding = {
        "schema": "threadforge.python-lock-sbom-binding.v1",
        "source_commit": source_commit,
        "release_version": release_version,
        "uv_lock_sha256": lock_hash,
        "pyproject_sha256": project_hash,
        "package_record_count": len(packages),
        "extras": extras,
    }
    namespace_digest = hashlib.sha256(_canonical_json(binding)).hexdigest()
    relationships = [{
        "spdxElementId": "SPDXRef-DOCUMENT",
        "relationshipType": "DESCRIBES",
        "relatedSpdxElement": package_ids[root_identity],
    }]
    relationships.extend(
        {
            "spdxElementId": source,
            "relationshipType": "DEPENDS_ON",
            "relatedSpdxElement": target,
            **({"comment": comment} if comment else {}),
        }
        for source, target, comment in sorted(edges)
    )
    document = {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"threadforge-python-{release_version}-all-extras-lock",
        "documentNamespace": f"https://threadforge.dev/spdx/python-lock/{release_version}/{namespace_digest}",
        "creationInfo": {
            "creators": ["Tool: Threadforge Python lock SBOM builder"],
            "created": created,
            "comment": "threadforge.python-lock-sbom-binding.v1 " + _canonical_json(binding).decode("utf-8"),
        },
        "documentDescribes": [package_ids[root_identity]],
        "packages": packages,
        "relationships": relationships,
    }
    encoded = _canonical_json(document) + b"\n"
    if len(encoded) > MAX_LOCK_BYTES:
        raise PythonLockSBOMError("generated Python SPDX document exceeds its size limit")

    parent = output_path.parent.absolute()
    current = Path(parent.anchor)
    for component in parent.parts[1:]:
        current = current / component
        try:
            info = current.lstat()
        except OSError as error:
            raise PythonLockSBOMError("SBOM output parent must already exist") from error
        attributes = getattr(info, "st_file_attributes", 0)
        if (
            stat.S_ISLNK(info.st_mode)
            or attributes & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
            or not stat.S_ISDIR(info.st_mode)
        ):
            raise PythonLockSBOMError("SBOM output parent path must contain only real directories")
    if output_path.exists() or output_path.is_symlink():
        raise PythonLockSBOMError("refusing to overwrite an existing Python SPDX document")
    opened = False
    try:
        with output_path.open("xb") as stream:
            opened = True
            stream.write(encoded)
    except OSError as error:
        if opened:
            try:
                output_path.unlink(missing_ok=True)
            except OSError:
                pass
        raise PythonLockSBOMError("cannot write Python SPDX document") from error
    return {
        "schema": binding["schema"],
        "source_commit": source_commit,
        "release_version": release_version,
        "package_record_count": len(packages),
        "dependency_relationship_count": len(edges),
        "extras": extras,
        "uv_lock_sha256": lock_hash,
        "pyproject_sha256": project_hash,
        "license_review_status": "unreviewed",
        "release_clearance": False,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-version", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-date-epoch", type=int, required=True)
    parser.add_argument("--lock-file", type=Path, default=Path(__file__).resolve().parents[1] / "python" / "uv.lock")
    parser.add_argument("--project-file", type=Path, default=Path(__file__).resolve().parents[1] / "python" / "pyproject.toml")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        summary = build_python_lock_sbom(
            release_version=args.release_version,
            source_commit=args.source_commit,
            lock_path=args.lock_file,
            project_path=args.project_file,
            output_path=args.output,
            source_date_epoch=args.source_date_epoch,
        )
    except (OSError, PythonLockSBOMError, OverflowError, ValueError) as error:
        print(json.dumps({"error": str(error), "release_clearance": False}, sort_keys=True), file=sys.stderr)
        return 2
    print(json.dumps(summary, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
