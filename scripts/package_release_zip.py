# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.
"""Create a normalized Windows CLI ZIP with its complete legal payload."""

from __future__ import annotations

import os
import stat
import sys
import zipfile
from datetime import datetime, timezone
from pathlib import Path


MAX_BINARY_BYTES = 128 * 1024 * 1024
MAX_LEGAL_MEMBER_BYTES = 128 * 1024 * 1024
MAX_ARCHIVE_UNCOMPRESSED_BYTES = 256 * 1024 * 1024


def _read_regular_file(path: Path, label: str, maximum: int) -> bytes:
    try:
        metadata = path.lstat()
    except OSError as exc:
        raise ValueError(f"release {label} is unavailable") from exc
    if not stat.S_ISREG(metadata.st_mode):
        raise ValueError(f"release {label} must be a regular, non-symlink file")
    if metadata.st_size <= 0 or metadata.st_size > maximum:
        raise ValueError(f"release {label} is empty or exceeds its size limit")
    try:
        with path.open("rb") as stream:
            data = stream.read(maximum + 1)
    except OSError as exc:
        raise ValueError(f"release {label} could not be read") from exc
    if len(data) != metadata.st_size or len(data) > maximum:
        raise ValueError(f"release {label} changed while being read")
    return data


def package(
    archive_path: Path,
    binary_path: Path,
    member_name: str,
    license_path: Path,
    notice_path: Path,
    third_party_path: Path,
    third_party_licenses_path: Path,
    source_date_epoch: int,
) -> None:
    if member_name != "threadforge.exe":
        raise ValueError("Windows release ZIP binary member must be threadforge.exe")
    if archive_path.is_symlink():
        raise ValueError("release archive target must not be a symlink")

    timestamp = datetime.fromtimestamp(source_date_epoch, timezone.utc)
    if not 1980 <= timestamp.year <= 2107:
        raise ValueError("SOURCE_DATE_EPOCH is outside the ZIP timestamp range")
    dos_timestamp = (
        timestamp.year,
        timestamp.month,
        timestamp.day,
        timestamp.hour,
        timestamp.minute,
        timestamp.second // 2 * 2,
    )

    members = [
        ("LICENSE", _read_regular_file(license_path, "LICENSE", MAX_LEGAL_MEMBER_BYTES), 0o644),
        ("NOTICE", _read_regular_file(notice_path, "NOTICE", MAX_LEGAL_MEMBER_BYTES), 0o644),
        (
            "THIRD_PARTY.md",
            _read_regular_file(third_party_path, "third-party ledger", MAX_LEGAL_MEMBER_BYTES),
            0o644,
        ),
        (
            "THIRD_PARTY_LICENSES.tar.gz",
            _read_regular_file(
                third_party_licenses_path,
                "target-specific third-party license bundle",
                MAX_LEGAL_MEMBER_BYTES,
            ),
            0o644,
        ),
        (member_name, _read_regular_file(binary_path, "binary", MAX_BINARY_BYTES), 0o755),
    ]
    total_size = sum(len(data) for _, data, _ in members)
    if total_size > MAX_ARCHIVE_UNCOMPRESSED_BYTES:
        raise ValueError("release ZIP payload exceeds its total size limit")

    archive_path.parent.mkdir(parents=True, exist_ok=True)
    temporary_path = archive_path.with_name(f".{archive_path.name}.tmp")
    if temporary_path.exists() or temporary_path.is_symlink():
        raise ValueError("temporary release ZIP path already exists")
    try:
        with temporary_path.open("xb") as raw_file:
            with zipfile.ZipFile(raw_file, mode="w", compression=zipfile.ZIP_STORED) as archive:
                for name, data, mode in members:
                    info = zipfile.ZipInfo(name, date_time=dos_timestamp)
                    info.create_system = 3
                    info.external_attr = (stat.S_IFREG | mode) << 16
                    info.compress_type = zipfile.ZIP_STORED
                    info.extra = b""
                    info.comment = b""
                    archive.writestr(info, data)

        with zipfile.ZipFile(temporary_path, mode="r") as archive:
            entries = archive.infolist()
            expected_names = [name for name, _, _ in members]
            if (
                [entry.filename for entry in entries] != expected_names
                or any(stat.S_IFMT(entry.external_attr >> 16) != stat.S_IFREG for entry in entries)
                or archive.testzip() is not None
            ):
                raise ValueError("release ZIP failed its legal-payload and binary integrity check")

        if archive_path.is_symlink():
            raise ValueError("release archive target became a symlink during packaging")
        os.replace(temporary_path, archive_path)
    finally:
        temporary_path.unlink(missing_ok=True)


def main(argv: list[str]) -> int:
    if len(argv) != 9:
        print(
            "usage: package_release_zip.py ARCHIVE_PATH BINARY_PATH MEMBER_NAME "
            "LICENSE NOTICE THIRD_PARTY.md THIRD_PARTY_LICENSES.tar.gz SOURCE_DATE_EPOCH",
            file=sys.stderr,
        )
        return 2
    try:
        package(
            Path(argv[1]),
            Path(argv[2]),
            argv[3],
            Path(argv[4]),
            Path(argv[5]),
            Path(argv[6]),
            Path(argv[7]),
            int(argv[8]),
        )
    except (OSError, ValueError, zipfile.BadZipFile) as exc:
        print(f"release ZIP packaging failed: {exc}", file=sys.stderr)
        return 1
    print(f"Validated Windows release ZIP legal payload: {argv[1]}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
