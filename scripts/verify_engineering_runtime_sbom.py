#!/usr/bin/env python3
"""Verify a source-bound, non-publishing Engineering runtime image SBOM artifact."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import stat
import subprocess
import sys
import tarfile
from pathlib import Path, PurePosixPath
from typing import Any, Callable


REPOSITORY = "Rangow4562/ThreadForge"
WORKFLOW = ".github/workflows/threadforge-tests.yml"
SLSA_PREDICATE = "https://slsa.dev/provenance/v1"
SPDX_PREDICATE = "https://spdx.dev/Document/v2.3"
VERSION_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
IMAGE_ID_RE = re.compile(r"^sha256:([0-9a-f]{64})$")
SOURCE_REF_RE = re.compile(r"^refs/(?:tags/v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)|heads/main)$")
REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
MAX_ARCHIVE_BYTES = 10 * 1024 * 1024 * 1024
MAX_SBOM_BYTES = 256 * 1024 * 1024
MAX_SMALL_FILE_BYTES = 1024 * 1024
MAX_ARCHIVE_MEMBERS = 100_000
MAX_ARCHIVE_EXPANDED_BYTES = 10 * 1024 * 1024 * 1024
MAX_MANIFEST_BYTES = 1024 * 1024
MAX_IMAGE_CONFIG_BYTES = 32 * 1024 * 1024
CHUNK_SIZE = 1024 * 1024


class EngineeringRuntimeSBOMError(ValueError):
    """The Engineering runtime archive, SPDX inventory, or binding is invalid."""


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise EngineeringRuntimeSBOMError("JSON contains a duplicate object key")
        result[key] = value
    return result


def _reject_constant(value: str) -> None:
    raise EngineeringRuntimeSBOMError(f"JSON contains a non-standard numeric constant: {value}")


def _decode_json(data: bytes, description: str) -> Any:
    try:
        return json.loads(data, object_pairs_hook=_strict_object, parse_constant=_reject_constant)
    except (json.JSONDecodeError, EngineeringRuntimeSBOMError) as error:
        raise EngineeringRuntimeSBOMError(f"{description} is invalid or ambiguous JSON") from error


def _is_link_or_reparse(path: Path) -> bool:
    try:
        info = path.lstat()
    except OSError as error:
        raise EngineeringRuntimeSBOMError("bundle path is missing or unreadable") from error
    flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    return stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & flag)


def _require_regular(path: Path, *, limit: int) -> int:
    if _is_link_or_reparse(path):
        raise EngineeringRuntimeSBOMError("bundle contains a link instead of a regular file")
    try:
        info = path.lstat()
    except OSError as error:
        raise EngineeringRuntimeSBOMError("bundle file is missing or unreadable") from error
    if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
        raise EngineeringRuntimeSBOMError("bundle file is not a bounded regular file")
    return info.st_size


def _sha256(path: Path, *, limit: int) -> tuple[str, int]:
    size = _require_regular(path, limit=limit)
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            while chunk := stream.read(CHUNK_SIZE):
                digest.update(chunk)
    except OSError as error:
        raise EngineeringRuntimeSBOMError("bundle file cannot be hashed") from error
    return digest.hexdigest(), size


def _safe_archive_path(name: str) -> str:
    if not name or "\\" in name or "\x00" in name:
        raise EngineeringRuntimeSBOMError("Docker archive has an unsafe member path")
    path = PurePosixPath(name)
    if path.is_absolute() or path.as_posix() != name or any(part in {"", ".", ".."} for part in path.parts):
        raise EngineeringRuntimeSBOMError("Docker archive has an unsafe member path")
    return path.as_posix()


def _read_tar_member(archive: tarfile.TarFile, member: tarfile.TarInfo, *, limit: int) -> bytes:
    if not member.isfile() or member.size < 0 or member.size > limit:
        raise EngineeringRuntimeSBOMError("Docker archive metadata member is not a bounded regular file")
    stream = archive.extractfile(member)
    if stream is None:
        raise EngineeringRuntimeSBOMError("Docker archive metadata member cannot be read")
    data = stream.read(limit + 1)
    if len(data) != member.size or len(data) > limit:
        raise EngineeringRuntimeSBOMError("Docker archive metadata member size is inconsistent")
    return data


def _validate_docker_archive(path: Path, *, binding: dict[str, Any], expected_tag: str) -> None:
    image = binding.get("image_archive")
    image_id = binding.get("image_id")
    image_id_match = IMAGE_ID_RE.fullmatch(image_id) if isinstance(image_id, str) else None
    if not isinstance(image, dict) or not image_id_match:
        raise EngineeringRuntimeSBOMError("binding is missing Docker archive identity")

    members: dict[str, tarfile.TarInfo] = {}
    expanded_bytes = 0
    try:
        with tarfile.open(path, mode="r:") as archive:
            for index, member in enumerate(archive):
                if index >= MAX_ARCHIVE_MEMBERS:
                    raise EngineeringRuntimeSBOMError("Docker archive contains too many members")
                name = _safe_archive_path(member.name)
                if name in members or not (member.isfile() or member.isdir()):
                    raise EngineeringRuntimeSBOMError("Docker archive contains duplicate or non-regular members")
                if member.size < 0:
                    raise EngineeringRuntimeSBOMError("Docker archive contains a negative member size")
                expanded_bytes += member.size
                if expanded_bytes > MAX_ARCHIVE_EXPANDED_BYTES:
                    raise EngineeringRuntimeSBOMError("Docker archive exceeds the expanded-size bound")
                members[name] = member
            manifest_member = members.get("manifest.json")
            if manifest_member is None:
                raise EngineeringRuntimeSBOMError("Docker archive has no root manifest.json")
            manifest = _decode_json(_read_tar_member(archive, manifest_member, limit=MAX_MANIFEST_BYTES), "Docker manifest")
            if not isinstance(manifest, list) or len(manifest) != 1 or not isinstance(manifest[0], dict):
                raise EngineeringRuntimeSBOMError("Docker archive must contain exactly one image")
            image_manifest = manifest[0]
            config_name = image_manifest.get("Config")
            repo_tags = image_manifest.get("RepoTags")
            layers = image_manifest.get("Layers")
            if not isinstance(repo_tags, list) or repo_tags != [expected_tag]:
                raise EngineeringRuntimeSBOMError("Docker manifest RepoTags do not match the expected image tag")
            if not isinstance(config_name, str) or not isinstance(layers, list) or not layers or len(layers) > 1000:
                raise EngineeringRuntimeSBOMError("Docker manifest config or layer inventory is invalid")
            config_name = _safe_archive_path(config_name)
            config_member = members.get(config_name)
            if config_member is None:
                raise EngineeringRuntimeSBOMError("Docker image config is missing from the archive")
            config_data = _read_tar_member(archive, config_member, limit=MAX_IMAGE_CONFIG_BYTES)
            if hashlib.sha256(config_data).hexdigest() != image_id_match.group(1):
                raise EngineeringRuntimeSBOMError("Docker image config digest differs from the pinned image ID")
            config = _decode_json(config_data, "Docker image config")
            if not isinstance(config, dict):
                raise EngineeringRuntimeSBOMError("Docker image config root is invalid")
            config_fields = config.get("config")
            labels = config_fields.get("Labels") if isinstance(config_fields, dict) else None
            rootfs = config.get("rootfs")
            diff_ids = rootfs.get("diff_ids") if isinstance(rootfs, dict) else None
            if (
                config.get("os") != "linux"
                or config.get("architecture") != "amd64"
                or not isinstance(config_fields, dict)
                or config_fields.get("User") != "65532:65532"
                or not isinstance(labels, dict)
                or labels.get("org.opencontainers.image.base.name") != binding.get("base_image")
                or labels.get("org.threadforge.engineering.requirements-sha256") != binding.get("runtime_requirements_sha256")
                or labels.get("org.threadforge.engineering.build-requirements-sha256") != binding.get("build_requirements_sha256")
                or not isinstance(diff_ids, list)
                or len(diff_ids) != len(layers)
            ):
                raise EngineeringRuntimeSBOMError("Docker image config does not match the Engineering build binding")
            for layer_name in layers:
                if not isinstance(layer_name, str) or _safe_archive_path(layer_name) not in members:
                    raise EngineeringRuntimeSBOMError("Docker manifest references a missing layer")
                if not members[layer_name].isfile():
                    raise EngineeringRuntimeSBOMError("Docker image layer is not a regular file")
    except (OSError, tarfile.TarError) as error:
        raise EngineeringRuntimeSBOMError("Docker image archive is malformed or unreadable") from error


def _parse_checksums(path: Path, expected_names: set[str]) -> dict[str, str]:
    size = _require_regular(path, limit=MAX_SMALL_FILE_BYTES)
    try:
        raw = path.read_bytes()
        text = raw.decode("ascii")
    except (OSError, UnicodeError) as error:
        raise EngineeringRuntimeSBOMError("checksums.txt is not bounded ASCII") from error
    if size != len(raw) or not text.endswith("\n") or "\r" in text:
        raise EngineeringRuntimeSBOMError("checksums.txt has invalid encoding or line endings")
    entries: dict[str, str] = {}
    for line in text.splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9_.-]+)", line)
        if not match or match.group(2) in entries:
            raise EngineeringRuntimeSBOMError("checksums.txt contains malformed or duplicate rows")
        entries[match.group(2)] = match.group(1)
    if set(entries) != expected_names:
        raise EngineeringRuntimeSBOMError("checksums.txt does not cover the exact Engineering SBOM payloads")
    return entries


def _verify_spdx_schema(document: Any, repo_root: Path) -> None:
    try:
        import jsonschema
    except ImportError as error:
        raise EngineeringRuntimeSBOMError("jsonschema is required to verify the Engineering SPDX document") from error
    schema_path = repo_root / "schema" / "third_party" / "spdx-2.3.schema.json"
    try:
        schema = json.loads(schema_path.read_text(encoding="utf-8"))
        jsonschema.Draft7Validator.check_schema(schema)
        errors = list(jsonschema.Draft7Validator(schema).iter_errors(document))
    except (OSError, json.JSONDecodeError, jsonschema.SchemaError) as error:
        raise EngineeringRuntimeSBOMError("vendored SPDX 2.3 schema is unavailable or invalid") from error
    if errors:
        errors.sort(key=lambda item: (list(map(str, item.absolute_path)), item.message))
        raise EngineeringRuntimeSBOMError("Syft SPDX document fails the vendored SPDX 2.3 schema")


def _run_attestation(
    path: Path,
    *,
    predicate_type: str,
    source_commit: str,
    source_ref: str,
    repository: str,
    gh: str,
    run: Callable[..., subprocess.CompletedProcess[str]],
) -> None:
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
        result = run(command, capture_output=True, text=True, check=False, timeout=120)
    except (OSError, subprocess.SubprocessError) as error:
        raise EngineeringRuntimeSBOMError("GitHub attestation verification could not run") from error
    if result.returncode != 0:
        raise EngineeringRuntimeSBOMError(f"GitHub attestation verification failed for {path.name}")
    if not isinstance(result.stdout, str) or len(result.stdout.encode("utf-8")) > 16 * 1024 * 1024:
        raise EngineeringRuntimeSBOMError("GitHub attestation verification returned oversized output")
    try:
        items = json.loads(result.stdout, object_pairs_hook=_strict_object, parse_constant=_reject_constant)
    except (json.JSONDecodeError, EngineeringRuntimeSBOMError) as error:
        raise EngineeringRuntimeSBOMError("GitHub attestation verification returned invalid JSON") from error
    if not isinstance(items, list) or len(items) != 1:
        raise EngineeringRuntimeSBOMError("GitHub attestation verification returned an ambiguous result")
    verification = items[0].get("verificationResult") if isinstance(items[0], dict) else None
    statement = verification.get("statement") if isinstance(verification, dict) else None
    if not isinstance(statement, dict) or statement.get("predicateType") != predicate_type:
        raise EngineeringRuntimeSBOMError("GitHub attestation statement has an unexpected predicate")
    subject = statement.get("subject")
    expected_name = path.name
    digest = _sha256(path, limit=MAX_ARCHIVE_BYTES if path.suffix == ".tar" else MAX_SBOM_BYTES)[0]
    if not isinstance(subject, list) or len(subject) != 1 or not isinstance(subject[0], dict):
        raise EngineeringRuntimeSBOMError("GitHub attestation subject set is ambiguous")
    if subject[0].get("name") != expected_name or subject[0].get("digest") != {"sha256": digest}:
        raise EngineeringRuntimeSBOMError("GitHub attestation subject does not match the verified artifact")


def verify_engineering_runtime_artifact(
    *,
    bundle_dir: Path,
    repo_root: Path,
    version: str,
    source_commit: str,
    source_ref: str,
    repository: str = REPOSITORY,
    gh: str = "gh",
    run: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
    verify_attestations: bool = True,
) -> dict[str, Any]:
    if not VERSION_RE.fullmatch(version) or not COMMIT_RE.fullmatch(source_commit):
        raise EngineeringRuntimeSBOMError("candidate version or source commit is invalid")
    if (
        not SOURCE_REF_RE.fullmatch(source_ref)
        or any(part in {"", ".", ".."} for part in source_ref.split("/")[1:])
        or source_ref not in {f"refs/tags/v{version}", "refs/heads/main"}
    ):
        raise EngineeringRuntimeSBOMError("source ref is not bound to the candidate version or main rehearsal")
    if not REPOSITORY_RE.fullmatch(repository) or any(part in {".", ".."} for part in repository.split("/")):
        raise EngineeringRuntimeSBOMError("repository must be a safe owner/name value")
    if _is_link_or_reparse(bundle_dir) or not bundle_dir.is_dir():
        raise EngineeringRuntimeSBOMError("bundle directory is missing or unsafe")

    archive_name = f"threadforge_{version}_engineering_linux_amd64.docker.tar"
    sbom_name = f"threadforge_{version}_engineering_linux_amd64.spdx.json"
    binding_name = f"threadforge_{version}_engineering_linux_amd64.binding.json"
    expected = {archive_name, sbom_name, binding_name, "checksums.txt"}
    files: dict[str, Path] = {}
    total = 0
    for path in bundle_dir.iterdir():
        if path.name in files or path.name not in expected:
            raise EngineeringRuntimeSBOMError("bundle contains duplicate or unexpected entries")
        limit = MAX_ARCHIVE_BYTES if path.name == archive_name else MAX_SBOM_BYTES if path.name == sbom_name else MAX_SMALL_FILE_BYTES
        size = _require_regular(path, limit=limit)
        total += size
        if total > MAX_ARCHIVE_BYTES + MAX_SBOM_BYTES + 2 * MAX_SMALL_FILE_BYTES:
            raise EngineeringRuntimeSBOMError("Engineering runtime artifact bundle exceeds its aggregate size bound")
        files[path.name] = path
    if set(files) != expected:
        raise EngineeringRuntimeSBOMError("Engineering runtime artifact is incomplete")

    checksums = _parse_checksums(files["checksums.txt"], expected - {"checksums.txt"})
    for name, digest in checksums.items():
        limit = MAX_ARCHIVE_BYTES if name == archive_name else MAX_SBOM_BYTES if name == sbom_name else MAX_SMALL_FILE_BYTES
        if _sha256(files[name], limit=limit)[0] != digest:
            raise EngineeringRuntimeSBOMError(f"checksum mismatch for {name}")

    binding = _decode_json(files[binding_name].read_bytes(), "Engineering SBOM binding")
    if not isinstance(binding, dict):
        raise EngineeringRuntimeSBOMError("Engineering SBOM binding root must be an object")
    try:
        import jsonschema

        binding_schema = json.loads((repo_root / "schema" / "engineering-image-sbom-binding-v1.schema.json").read_text(encoding="utf-8"))
        jsonschema.Draft202012Validator(binding_schema).validate(binding)
    except ImportError as error:
        raise EngineeringRuntimeSBOMError("jsonschema is required to verify the Engineering SBOM binding") from error
    except (OSError, json.JSONDecodeError, jsonschema.ValidationError, jsonschema.SchemaError) as error:
        raise EngineeringRuntimeSBOMError("Engineering SBOM binding fails its versioned contract") from error
    if binding.get("release_version") != version or binding.get("source_commit") != source_commit or binding.get("source_ref") != source_ref:
        raise EngineeringRuntimeSBOMError("Engineering SBOM binding does not match the requested source identity")
    if binding.get("image_archive", {}).get("name") != archive_name or binding.get("sbom", {}).get("name") != sbom_name:
        raise EngineeringRuntimeSBOMError("Engineering SBOM binding names unexpected payloads")

    base_path = repo_root / "containers" / "engineering" / "base-image.txt"
    source_inputs = {
        "base_image": base_path,
        "runtime_requirements_sha256": repo_root / "containers" / "engineering" / "requirements-linux-x86_64-py312.lock",
        "build_requirements_sha256": repo_root / "containers" / "engineering" / "build-requirements.lock",
        "dockerfile_sha256": repo_root / "containers" / "engineering" / "Dockerfile",
    }
    for key, path in source_inputs.items():
        if key == "base_image":
            try:
                actual = path.read_text(encoding="utf-8").strip()
            except (OSError, UnicodeError) as error:
                raise EngineeringRuntimeSBOMError("pinned Engineering image base cannot be read") from error
        else:
            limit = 4 * 1024 * 1024 if key == "dockerfile_sha256" else 16 * 1024 * 1024
            actual = _sha256(path, limit=limit)[0]
        if binding.get(key) != actual:
            raise EngineeringRuntimeSBOMError(f"Engineering SBOM binding does not match current {key}")

    archive_binding = binding.get("image_archive")
    if (
        archive_binding.get("sha256") != _sha256(files[archive_name], limit=MAX_ARCHIVE_BYTES)[0]
        or archive_binding.get("size_bytes") != _require_regular(files[archive_name], limit=MAX_ARCHIVE_BYTES)
    ):
        raise EngineeringRuntimeSBOMError("image archive hash or size differs from the SBOM binding")
    sbom_binding = binding.get("sbom")
    if sbom_binding.get("sha256") != _sha256(files[sbom_name], limit=MAX_SBOM_BYTES)[0]:
        raise EngineeringRuntimeSBOMError("SPDX document hash differs from the SBOM binding")
    sbom = _decode_json(files[sbom_name].read_bytes(), "Syft SPDX document")
    _verify_spdx_schema(sbom, repo_root)
    packages = sbom.get("packages") if isinstance(sbom, dict) else None
    if not isinstance(packages, list) or len(packages) != sbom_binding.get("package_record_count"):
        raise EngineeringRuntimeSBOMError("SPDX package count differs from the SBOM binding")
    _validate_docker_archive(files[archive_name], binding=binding, expected_tag=binding["image_tag"])

    if verify_attestations:
        for path in files.values():
            _run_attestation(
                path,
                predicate_type=SLSA_PREDICATE,
                source_commit=source_commit,
                source_ref=source_ref,
                repository=repository,
                gh=gh,
                run=run,
            )
        _run_attestation(
            files[archive_name],
            predicate_type=SPDX_PREDICATE,
            source_commit=source_commit,
            source_ref=source_ref,
            repository=repository,
            gh=gh,
            run=run,
        )
    return {
        "schema_version": "threadforge.engineering-runtime-sbom-verification.v1",
        "repository": repository,
        "release_version": version,
        "source_commit": source_commit,
        "source_ref": source_ref,
        "platform": binding["platform"],
        "image_id": binding["image_id"],
        "image_archive_verified": True,
        "spdx_schema_verified": True,
        "package_record_count": len(packages),
        "slsa_attestations_verified": 4 if verify_attestations else 0,
        "archive_sbom_attestation_verified": verify_attestations,
        "attestation_checks_skipped": not verify_attestations,
        "license_review_required": True,
        "release_clearance": False,
        "release_authorized": False,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle-dir", type=Path, required=True)
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--version", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-ref", required=True)
    parser.add_argument("--repo", default=REPOSITORY)
    parser.add_argument("--gh", default="gh")
    parser.add_argument(
        "--skip-attestation-checks",
        action="store_true",
        help="validate bundle structure before attestations are created; not a release verification",
    )
    arguments = parser.parse_args(argv)
    try:
        result = verify_engineering_runtime_artifact(
            bundle_dir=arguments.bundle_dir,
            repo_root=arguments.repo_root.resolve(strict=True),
            version=arguments.version,
            source_commit=arguments.source_commit,
            source_ref=arguments.source_ref,
            repository=arguments.repo,
            gh=arguments.gh,
            verify_attestations=not arguments.skip_attestation_checks,
        )
    except (OSError, EngineeringRuntimeSBOMError) as error:
        print(f"Engineering runtime SBOM verification failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
