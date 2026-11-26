#!/usr/bin/env python3
"""Read the config-byte digest from a bounded Docker-save archive."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import stat
import sys
import tarfile
from pathlib import Path, PurePosixPath
from typing import Any


IMAGE_TAG_RE = re.compile(
    r"^[A-Za-z0-9][A-Za-z0-9._/-]{0,119}(?::[A-Za-z0-9][A-Za-z0-9._-]{0,119})?$"
)
MAX_ARCHIVE_BYTES = 10 * 1024 * 1024 * 1024
MAX_ARCHIVE_MEMBERS = 100_000
MAX_ARCHIVE_EXPANDED_BYTES = 20 * 1024 * 1024 * 1024
MAX_MANIFEST_BYTES = 1024 * 1024
MAX_CONFIG_BYTES = 16 * 1024 * 1024


class DockerArchiveError(ValueError):
    """The Docker-save archive does not contain a bounded single image."""


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise DockerArchiveError("Docker archive JSON has a duplicate object key")
        result[key] = value
    return result


def _reject_constant(value: str) -> None:
    raise DockerArchiveError(f"Docker archive JSON contains a non-standard number: {value}")


def _safe_archive_path(name: str, *, directory: bool = False) -> str:
    if not name or "\\" in name or "\x00" in name:
        raise DockerArchiveError("Docker archive has an unsafe member path")
    if directory and name.endswith("/"):
        name = name[:-1]
    path = PurePosixPath(name)
    if (
        path.is_absolute()
        or path.as_posix() != name
        or any(part in {"", ".", ".."} for part in path.parts)
    ):
        raise DockerArchiveError("Docker archive has an unsafe member path")
    return path.as_posix()


def _read_member(archive: tarfile.TarFile, member: tarfile.TarInfo, *, limit: int) -> bytes:
    if not member.isfile() or member.size < 0 or member.size > limit:
        raise DockerArchiveError("Docker archive metadata is not a bounded regular file")
    stream = archive.extractfile(member)
    if stream is None:
        raise DockerArchiveError("Docker archive metadata cannot be read")
    data = stream.read(limit + 1)
    if len(data) != member.size or len(data) > limit:
        raise DockerArchiveError("Docker archive metadata size is inconsistent")
    return data


def config_digest_from_archive(archive_path: Path, *, expected_tag: str) -> str:
    """Return the SHA-256 of the exact config JSON referenced by manifest.json."""
    if not IMAGE_TAG_RE.fullmatch(expected_tag):
        raise DockerArchiveError("expected Docker image tag is invalid")
    flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    try:
        info = archive_path.lstat()
    except OSError as error:
        raise DockerArchiveError("Docker archive is missing or unreadable") from error
    if (
        stat.S_ISLNK(info.st_mode)
        or bool(getattr(info, "st_file_attributes", 0) & flag)
        or not stat.S_ISREG(info.st_mode)
        or info.st_size <= 0
        or info.st_size > MAX_ARCHIVE_BYTES
    ):
        raise DockerArchiveError("Docker archive is not a bounded regular file")

    members: dict[str, tarfile.TarInfo] = {}
    expanded_bytes = 0
    try:
        with tarfile.open(archive_path, mode="r:") as archive:
            for index, member in enumerate(archive):
                if index >= MAX_ARCHIVE_MEMBERS:
                    raise DockerArchiveError("Docker archive contains too many members")
                name = _safe_archive_path(member.name, directory=member.isdir())
                if name in members or not (member.isfile() or member.isdir()):
                    raise DockerArchiveError("Docker archive has duplicate or non-regular members")
                if member.size < 0:
                    raise DockerArchiveError("Docker archive has a negative member size")
                expanded_bytes += member.size
                if expanded_bytes > MAX_ARCHIVE_EXPANDED_BYTES:
                    raise DockerArchiveError("Docker archive exceeds the expanded-size bound")
                members[name] = member

            manifest_member = members.get("manifest.json")
            if manifest_member is None:
                raise DockerArchiveError("Docker archive has no root manifest.json")
            try:
                manifest = json.loads(
                    _read_member(archive, manifest_member, limit=MAX_MANIFEST_BYTES),
                    object_pairs_hook=_strict_object,
                    parse_constant=_reject_constant,
                )
            except (UnicodeDecodeError, json.JSONDecodeError) as error:
                raise DockerArchiveError("Docker archive manifest is invalid JSON") from error
            if not isinstance(manifest, list) or len(manifest) != 1 or not isinstance(manifest[0], dict):
                raise DockerArchiveError("Docker archive must contain exactly one image")
            image = manifest[0]
            if image.get("RepoTags") != [expected_tag]:
                raise DockerArchiveError("Docker archive tag does not match the requested image")
            config_name = image.get("Config")
            layers = image.get("Layers")
            if not isinstance(config_name, str) or not isinstance(layers, list) or not layers or len(layers) > 1000:
                raise DockerArchiveError("Docker archive manifest has an invalid config or layer inventory")
            config_name = _safe_archive_path(config_name)
            config_member = members.get(config_name)
            if config_member is None:
                raise DockerArchiveError("Docker archive config is missing")
            config_bytes = _read_member(archive, config_member, limit=MAX_CONFIG_BYTES)
            digest = hashlib.sha256(config_bytes).hexdigest()
            if PurePosixPath(config_name).name not in {digest, f"{digest}.json"}:
                raise DockerArchiveError("Docker archive config filename does not match its bytes")
            try:
                config = json.loads(
                    config_bytes,
                    object_pairs_hook=_strict_object,
                    parse_constant=_reject_constant,
                )
            except (UnicodeDecodeError, json.JSONDecodeError) as error:
                raise DockerArchiveError("Docker image config is invalid JSON") from error
            if not isinstance(config, dict) or config.get("os") != "linux" or config.get("architecture") != "amd64":
                raise DockerArchiveError("Docker image config is not linux/amd64")
            for layer in layers:
                if not isinstance(layer, str) or _safe_archive_path(layer) not in members:
                    raise DockerArchiveError("Docker archive manifest references a missing layer")
                if not members[_safe_archive_path(layer)].isfile():
                    raise DockerArchiveError("Docker archive layer is not a regular file")
    except (OSError, tarfile.TarError) as error:
        raise DockerArchiveError("Docker archive is malformed or unreadable") from error
    return f"sha256:{digest}"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    arguments = parser.parse_args(argv)
    try:
        print(config_digest_from_archive(arguments.archive, expected_tag=arguments.tag))
    except (DockerArchiveError, OSError) as error:
        print(f"Engineering Docker archive inspection failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
