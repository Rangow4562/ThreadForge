#!/usr/bin/env python3
"""Bind the Engineering Docker image archive and SPDX inventory to a candidate."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import stat
import sys
from pathlib import Path
from typing import Any


VERSION_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
IMAGE_ID_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
SOURCE_REF_RE = re.compile(r"^refs/(?:heads|tags)/[A-Za-z0-9._/-]+$")
BASE_IMAGE_RE = re.compile(r"^python:3\.12\.[0-9]+-slim-bookworm@sha256:[0-9a-f]{64}$")
MAX_ARCHIVE_BYTES = 10 * 1024 * 1024 * 1024
MAX_SBOM_BYTES = 256 * 1024 * 1024
MAX_RECEIPT_BYTES = 1024 * 1024
MAX_PACKAGES = 200_000
CHUNK_SIZE = 1024 * 1024


class EngineeringSBOMError(ValueError):
    """The image build receipt or its bound SBOM inputs are invalid."""


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise EngineeringSBOMError("input JSON contains a duplicate object key")
        result[key] = value
    return result


def _read_json(path: Path, *, limit: int, description: str) -> Any:
    _require_file(path, limit=limit)
    try:
        raw = path.read_bytes()
        return json.loads(raw, object_pairs_hook=_strict_object, parse_constant=_reject_constant)
    except (OSError, json.JSONDecodeError, EngineeringSBOMError) as error:
        raise EngineeringSBOMError(f"{description} is not valid bounded JSON") from error


def _reject_constant(value: str) -> None:
    raise EngineeringSBOMError(f"JSON contains a non-standard number: {value}")


def _is_link_or_reparse(path: Path) -> bool:
    try:
        info = path.lstat()
    except OSError as error:
        raise EngineeringSBOMError("an SBOM input is missing or unreadable") from error
    flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    return stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & flag)


def _require_file(path: Path, *, limit: int) -> None:
    if _is_link_or_reparse(path):
        raise EngineeringSBOMError("an SBOM input is a link instead of a regular file")
    try:
        info = path.lstat()
    except OSError as error:
        raise EngineeringSBOMError("an SBOM input is missing or unreadable") from error
    if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
        raise EngineeringSBOMError("an SBOM input is not a bounded regular file")


def _sha256(path: Path, *, limit: int) -> tuple[str, int]:
    _require_file(path, limit=limit)
    digest = hashlib.sha256()
    size = 0
    try:
        with path.open("rb") as stream:
            while chunk := stream.read(CHUNK_SIZE):
                size += len(chunk)
                digest.update(chunk)
    except OSError as error:
        raise EngineeringSBOMError("an SBOM input cannot be hashed") from error
    return digest.hexdigest(), size


def _require_new_file(path: Path) -> None:
    if path.exists() or path.is_symlink():
        raise EngineeringSBOMError("refusing to overwrite an existing SBOM binding output")


def build_binding(
    *,
    repo_root: Path,
    output_dir: Path,
    version: str,
    source_commit: str,
    source_ref: str,
    image_tag: str,
    build_receipt: Path,
    image_archive: Path,
    sbom: Path,
) -> dict[str, Any]:
    if not VERSION_RE.fullmatch(version):
        raise EngineeringSBOMError("version must be stable MAJOR.MINOR.PATCH without leading v")
    if not COMMIT_RE.fullmatch(source_commit):
        raise EngineeringSBOMError("source commit must be a full lowercase 40-character SHA-1")
    if (
        not SOURCE_REF_RE.fullmatch(source_ref)
        or any(part in {"", ".", ".."} for part in source_ref.split("/")[1:])
        or source_ref not in {f"refs/tags/v{version}", "refs/heads/main"}
    ):
        raise EngineeringSBOMError("source ref must be this candidate tag or refs/heads/main rehearsal")
    expected_tag = f"threadforge-engineering:{version}-candidate"
    if image_tag != expected_tag:
        raise EngineeringSBOMError("image tag does not match the release candidate version")

    base_path = repo_root / "containers" / "engineering" / "base-image.txt"
    runtime_lock = repo_root / "containers" / "engineering" / "requirements-linux-x86_64-py312.lock"
    build_lock = repo_root / "containers" / "engineering" / "build-requirements.lock"
    dockerfile = repo_root / "containers" / "engineering" / "Dockerfile"
    try:
        base_image = base_path.read_text(encoding="utf-8").strip()
    except (OSError, UnicodeError) as error:
        raise EngineeringSBOMError("pinned Engineering base image cannot be read") from error
    if not BASE_IMAGE_RE.fullmatch(base_image):
        raise EngineeringSBOMError("Engineering base image is not pinned to a supported Python 3.12 digest")

    receipt = _read_json(build_receipt, limit=MAX_RECEIPT_BYTES, description="Engineering image build receipt")
    if not isinstance(receipt, dict) or receipt != {
        "schema_version": "threadforge.engineering-image-build.v1",
        "platform": "linux/amd64",
        "image_id": receipt.get("image_id") if isinstance(receipt, dict) else None,
        "tag": expected_tag,
        "base_image": base_image,
        "runtime_requirements_sha256": receipt.get("runtime_requirements_sha256") if isinstance(receipt, dict) else None,
        "build_requirements_sha256": receipt.get("build_requirements_sha256") if isinstance(receipt, dict) else None,
        "benchmark_data_included": False,
        "evaluator_included": False,
    }:
        raise EngineeringSBOMError("Engineering image receipt does not match the expected safe build contract")
    if not isinstance(receipt.get("image_id"), str) or not IMAGE_ID_RE.fullmatch(receipt["image_id"]):
        raise EngineeringSBOMError("Engineering image receipt has an invalid immutable image ID")

    runtime_hash, _ = _sha256(runtime_lock, limit=16 * 1024 * 1024)
    build_hash, _ = _sha256(build_lock, limit=4 * 1024 * 1024)
    dockerfile_hash, _ = _sha256(dockerfile, limit=4 * 1024 * 1024)
    if receipt["runtime_requirements_sha256"] != runtime_hash or receipt["build_requirements_sha256"] != build_hash:
        raise EngineeringSBOMError("Engineering image receipt does not match the repository lock files")

    archive_name = f"threadforge_{version}_engineering_linux_amd64.docker.tar"
    sbom_name = f"threadforge_{version}_engineering_linux_amd64.spdx.json"
    binding_name = f"threadforge_{version}_engineering_linux_amd64.binding.json"
    if image_archive.name != archive_name or sbom.name != sbom_name:
        raise EngineeringSBOMError("Engineering image SBOM inputs do not use the versioned candidate names")
    archive_hash, archive_size = _sha256(image_archive, limit=MAX_ARCHIVE_BYTES)
    sbom_hash, _ = _sha256(sbom, limit=MAX_SBOM_BYTES)
    sbom_document = _read_json(sbom, limit=MAX_SBOM_BYTES, description="Syft SPDX document")
    packages = sbom_document.get("packages") if isinstance(sbom_document, dict) else None
    if (
        not isinstance(sbom_document, dict)
        or sbom_document.get("spdxVersion") != "SPDX-2.3"
        or not isinstance(packages, list)
        or not packages
        or len(packages) > MAX_PACKAGES
    ):
        raise EngineeringSBOMError("Syft output is not a bounded, non-empty SPDX 2.3 package inventory")

    binding = {
        "schema_version": "threadforge.engineering-image-sbom-binding.v1",
        "release_version": version,
        "source_commit": source_commit,
        "source_ref": source_ref,
        "platform": "linux/amd64",
        "image_tag": image_tag,
        "image_id": receipt["image_id"],
        "base_image": base_image,
        "runtime_requirements_sha256": runtime_hash,
        "build_requirements_sha256": build_hash,
        "dockerfile_sha256": dockerfile_hash,
        "image_archive": {"name": archive_name, "sha256": archive_hash, "size_bytes": archive_size},
        "sbom": {"name": sbom_name, "sha256": sbom_hash, "package_record_count": len(packages)},
        "license_review_status": "unreviewed",
        "license_review_required": True,
        "release_clearance": False,
        "benchmark_data_included": False,
        "evaluator_included": False,
    }
    if output_dir.exists() or output_dir.is_symlink():
        if _is_link_or_reparse(output_dir) or not output_dir.is_dir():
            raise EngineeringSBOMError("SBOM binding output directory is not a regular directory")
    else:
        output_dir.mkdir(parents=True)
    binding_path = output_dir / binding_name
    checksums_path = output_dir / "checksums.txt"
    _require_new_file(binding_path)
    _require_new_file(checksums_path)
    binding_path.write_text(json.dumps(binding, sort_keys=True, indent=2) + "\n", encoding="utf-8", newline="\n")

    payloads = [image_archive, sbom, binding_path]
    checksum_lines = []
    for path in sorted(payloads, key=lambda value: value.name):
        digest, _ = _sha256(path, limit=MAX_ARCHIVE_BYTES if path == image_archive else MAX_SBOM_BYTES)
        checksum_lines.append(f"{digest}  {path.name}")
    checksums_path.write_text("\n".join(checksum_lines) + "\n", encoding="ascii", newline="\n")
    return {
        "schema_version": binding["schema_version"],
        "image_archive": archive_name,
        "sbom": sbom_name,
        "binding": binding_name,
        "checksums": checksums_path.name,
        "image_id": receipt["image_id"],
        "package_record_count": len(packages),
        "license_review_required": True,
        "release_clearance": False,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-ref", required=True)
    parser.add_argument("--image-tag", required=True)
    parser.add_argument("--build-receipt", type=Path, required=True)
    parser.add_argument("--image-archive", type=Path, required=True)
    parser.add_argument("--sbom", type=Path, required=True)
    arguments = parser.parse_args(argv)
    try:
        result = build_binding(
            repo_root=arguments.repo_root.resolve(strict=True),
            output_dir=arguments.output_dir,
            version=arguments.version,
            source_commit=arguments.source_commit,
            source_ref=arguments.source_ref,
            image_tag=arguments.image_tag,
            build_receipt=arguments.build_receipt,
            image_archive=arguments.image_archive,
            sbom=arguments.sbom,
        )
    except (OSError, EngineeringSBOMError) as error:
        print(f"engineering image SBOM binding failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
