# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.
"""Create a normalized, single-binary ZIP archive for a Windows CLI release."""

from __future__ import annotations

import os
import stat
import sys
import zipfile
from datetime import datetime, timezone
from pathlib import Path


def package(archive_path: Path, binary_path: Path, member_name: str, source_date_epoch: int) -> None:
    if member_name != "threadforge.exe":
        raise ValueError("Windows release ZIP member must be threadforge.exe")
    if not binary_path.is_file() or binary_path.is_symlink():
        raise ValueError("release binary must be a regular, non-symlink file")
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

    archive_path.parent.mkdir(parents=True, exist_ok=True)
    temporary_path = archive_path.with_name(f".{archive_path.name}.tmp")
    try:
        info = zipfile.ZipInfo(member_name, date_time=dos_timestamp)
        info.create_system = 3
        info.external_attr = (stat.S_IFREG | 0o755) << 16
        info.compress_type = zipfile.ZIP_STORED
        info.extra = b""
        info.comment = b""
        with zipfile.ZipFile(temporary_path, mode="w", compression=zipfile.ZIP_STORED) as archive:
            archive.writestr(info, binary_path.read_bytes())

        with zipfile.ZipFile(temporary_path, mode="r") as archive:
            entries = archive.infolist()
            if (
                len(entries) != 1
                or entries[0].filename != member_name
                or stat.S_IFMT(entries[0].external_attr >> 16) != stat.S_IFREG
                or archive.testzip() is not None
            ):
                raise ValueError("release ZIP failed its single-regular-file integrity check")

        os.replace(temporary_path, archive_path)
    finally:
        temporary_path.unlink(missing_ok=True)


def main(argv: list[str]) -> int:
    if len(argv) != 5:
        print(
            "usage: package_release_zip.py ARCHIVE_PATH BINARY_PATH MEMBER_NAME SOURCE_DATE_EPOCH",
            file=sys.stderr,
        )
        return 2
    try:
        package(Path(argv[1]), Path(argv[2]), argv[3], int(argv[4]))
    except (OSError, ValueError, zipfile.BadZipFile) as exc:
        print(f"release ZIP packaging failed: {exc}", file=sys.stderr)
        return 1
    print(f"Validated single-binary release ZIP: {argv[1]}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
