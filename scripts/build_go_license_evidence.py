#!/usr/bin/env python3
"""Collect target-specific Go license text/report evidence for a release candidate.

The output is review evidence only. It never grants license approval or release
clearance; an independent review remains required.
"""

from __future__ import annotations

import argparse
import csv
import gzip
import hashlib
import io
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path
from typing import Any, Callable
from urllib.parse import urlsplit


TOOL_VERSION = "v2.0.1"
TOOL_MODULE = "github.com/google/go-licenses/v2"
EVIDENCE_SCHEMA = "threadforge.go-license-evidence.v1"
ALLOWED_TARGETS = {
    ("linux", "amd64"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
    ("windows", "amd64"),
}
MAX_REPORT_BYTES = 8 * 1024 * 1024
MAX_LICENSE_FILES = 10_000
MAX_LICENSE_BYTES = 64 * 1024 * 1024
MAX_SINGLE_FILE_BYTES = 16 * 1024 * 1024
SOURCE_COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")


class LicenseEvidenceError(ValueError):
    """Raised when the target dependency license evidence is incomplete."""


def _run_tool(
    go_licenses: Path,
    command: str,
    package: str,
    license_root: Path | None,
    *,
    goos: str,
    goarch: str,
    go_executable: Path,
    run: Callable[..., subprocess.CompletedProcess[str]],
) -> subprocess.CompletedProcess[str]:
    argv = [str(go_licenses), command, package]
    if command == "save":
        if license_root is None:
            raise LicenseEvidenceError("license output directory is required for save")
        argv.extend(["--save_path", str(license_root)])
    env = os.environ.copy()
    env.update({"GOOS": goos, "GOARCH": goarch, "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": ""})
    env["PATH"] = str(go_executable.parent) + os.pathsep + env.get("PATH", "")
    try:
        result = run(
            argv,
            check=False,
            capture_output=True,
            text=True,
            timeout=300,
            env=env,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise LicenseEvidenceError(f"go-licenses {command} could not run: {error}") from error
    if result.returncode != 0:
        detail = result.stderr.strip()[:1000]
        raise LicenseEvidenceError(
            f"go-licenses {command} failed" + (f": {detail}" if detail else "")
        )
    return result


def _parse_report(text: str) -> list[dict[str, str]]:
    encoded = text.encode("utf-8")
    if len(encoded) > MAX_REPORT_BYTES:
        raise LicenseEvidenceError("go-licenses report exceeds the 8 MiB limit")
    rows: list[dict[str, str]] = []
    try:
        reader = csv.reader(io.StringIO(text, newline=""), strict=True)
        for line_number, row in enumerate(reader, start=1):
            if len(row) != 3:
                raise LicenseEvidenceError(
                    f"go-licenses report row {line_number} must have three CSV columns"
                )
            package, license_url, expression = (item.strip() for item in row)
            if not package or not expression:
                raise LicenseEvidenceError(
                    f"go-licenses report row {line_number} has an empty package or license expression"
                )
            if expression.casefold() in {"unknown", "none", "noassertion"}:
                raise LicenseEvidenceError(
                    f"go-licenses could not classify the license expression for {package}"
                )
            if license_url and license_url.casefold() != "unknown":
                parsed = urlsplit(license_url)
                if parsed.scheme not in {"http", "https"} or not parsed.hostname:
                    raise LicenseEvidenceError(
                        f"go-licenses returned a non-web license URL for {package}"
                    )
                if parsed.username is not None or parsed.password is not None:
                    raise LicenseEvidenceError(
                        f"go-licenses returned credential-bearing license URL for {package}"
                    )
            rows.append(
                {
                    "package": package,
                    "license_url": license_url or "Unknown",
                    "license_expression": expression,
                }
            )
            if len(rows) > MAX_LICENSE_FILES:
                raise LicenseEvidenceError("go-licenses report exceeded the 10,000-record limit")
    except csv.Error as error:
        raise LicenseEvidenceError(f"go-licenses report is invalid CSV: {error}") from error
    if not rows:
        raise LicenseEvidenceError("go-licenses report contained no package records")
    return sorted(rows, key=lambda row: (row["package"], row["license_url"], row["license_expression"]))


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _resolve_go_executable(value: str) -> Path:
    requested = Path(value)
    if requested.is_absolute() or "/" in value or "\\" in value:
        candidate = requested.resolve()
    else:
        found = shutil.which(value)
        if found is None:
            raise LicenseEvidenceError("the configured Go executable is not available")
        candidate = Path(found).resolve()
    if not candidate.is_file():
        raise LicenseEvidenceError("the configured Go executable is not a regular file")
    return candidate


def _collect_license_files(root: Path) -> tuple[list[dict[str, Any]], dict[str, bytes]]:
    records: list[dict[str, Any]] = []
    contents: dict[str, bytes] = {}
    total_bytes = 0
    for path in sorted(root.rglob("*")):
        try:
            mode = path.lstat().st_mode
        except OSError as error:
            raise LicenseEvidenceError(f"cannot inspect a saved license path: {error}") from error
        if stat.S_ISDIR(mode):
            continue
        if not stat.S_ISREG(mode):
            raise LicenseEvidenceError("go-licenses save produced a non-regular evidence file")
        relative = path.relative_to(root).as_posix()
        if relative.startswith("/") or ".." in Path(relative).parts:
            raise LicenseEvidenceError("go-licenses save produced an unsafe evidence path")
        if len(records) >= MAX_LICENSE_FILES:
            raise LicenseEvidenceError("go-licenses save exceeded the 10,000-file limit")
        size = path.stat().st_size
        if size > MAX_SINGLE_FILE_BYTES:
            raise LicenseEvidenceError("a saved Go license evidence file exceeds the 16 MiB limit")
        total_bytes += size
        if total_bytes > MAX_LICENSE_BYTES:
            raise LicenseEvidenceError("saved Go license evidence exceeds the 64 MiB limit")
        data = path.read_bytes()
        contents[relative] = data
        records.append(
            {
                "path": relative,
                "size_bytes": len(data),
                "sha256": hashlib.sha256(data).hexdigest(),
            }
        )
    if not records:
        raise LicenseEvidenceError("go-licenses save produced no notice files")
    return records, contents


def _diagnostic_counts(stderr: str) -> dict[str, int]:
    lines = stderr.splitlines()
    return {
        "diagnostic_lines": len([line for line in lines if line.strip()]),
        "non_go_native_source_warnings": sum(
            "contains non-Go code that can't be inspected for further dependencies" in line
            for line in lines
        ),
        "license_url_resolution_warnings": sum(
            "Error discovering license URL" in line for line in lines
        ),
    }


def _write_deterministic_tar(
    output: Path, entries: dict[str, bytes], source_date_epoch: int
) -> None:
    if output.exists() or output.is_symlink():
        raise LicenseEvidenceError("license evidence archive output already exists")
    temporary = output.with_name(f".{output.name}.tmp")
    if temporary.exists() or temporary.is_symlink():
        raise LicenseEvidenceError("temporary license evidence archive already exists")
    owns_temporary = False
    try:
        with temporary.open("xb") as raw_file:
            owns_temporary = True
            with gzip.GzipFile(
                filename="", mode="wb", fileobj=raw_file, compresslevel=9, mtime=source_date_epoch
            ) as compressed:
                with tarfile.open(fileobj=compressed, mode="w", format=tarfile.GNU_FORMAT) as archive:
                    for relative in sorted(entries):
                        name = f"go-license-evidence/{relative}"
                        item = tarfile.TarInfo(name)
                        item.size = len(entries[relative])
                        item.mtime = source_date_epoch
                        item.uid = 0
                        item.gid = 0
                        item.uname = ""
                        item.gname = ""
                        item.mode = 0o644
                        item.type = tarfile.REGTYPE
                        archive.addfile(item, io.BytesIO(entries[relative]))
        if output.exists() or output.is_symlink():
            raise LicenseEvidenceError("license evidence archive output appeared during packaging")
        os.replace(temporary, output)
    except (OSError, tarfile.TarError, gzip.BadGzipFile) as error:
        if owns_temporary:
            temporary.unlink(missing_ok=True)
        raise LicenseEvidenceError(f"could not write license evidence archive: {error}") from error
    except LicenseEvidenceError:
        if owns_temporary:
            temporary.unlink(missing_ok=True)
        raise


def build_license_evidence(
    *,
    go_licenses: Path,
    package: str,
    goos: str,
    goarch: str,
    source_commit: str,
    source_date_epoch: int,
    output: Path,
    go_executable: str = "go",
    run: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> dict[str, Any]:
    if (goos, goarch) not in ALLOWED_TARGETS:
        raise LicenseEvidenceError(f"unsupported release target {goos}/{goarch}")
    if not SOURCE_COMMIT_RE.fullmatch(source_commit):
        raise LicenseEvidenceError("source commit must be a 40-character lowercase SHA-1")
    if not 315532800 <= source_date_epoch <= 2_147_483_647:
        raise LicenseEvidenceError("source date epoch is outside the supported range")
    if (
        not package
        or len(package) > 512
        or package.startswith("-")
        or any(character in package for character in ("\x00", "\r", "\n"))
    ):
        raise LicenseEvidenceError("Go package argument is invalid")
    if not go_licenses.is_file() or go_licenses.is_symlink():
        raise LicenseEvidenceError("go-licenses executable must be a regular non-symlink file")
    selected_go = _resolve_go_executable(go_executable)
    if not output.parent.is_dir() or output.parent.is_symlink():
        raise LicenseEvidenceError("license evidence output parent must be a regular directory")

    with tempfile.TemporaryDirectory(prefix=".go-license-evidence-", dir=output.parent) as scratch_name:
        scratch = Path(scratch_name)
        license_root = scratch / "collected-licenses"
        report = _run_tool(
            go_licenses,
            "report",
            package,
            None,
            goos=goos,
            goarch=goarch,
            go_executable=selected_go,
            run=run,
        )
        rows = _parse_report(report.stdout)
        saved = _run_tool(
            go_licenses,
            "save",
            package,
            license_root,
            goos=goos,
            goarch=goarch,
            go_executable=selected_go,
            run=run,
        )
        notice_files, file_contents = _collect_license_files(license_root)

        report_buffer = io.StringIO(newline="")
        writer = csv.writer(report_buffer, lineterminator="\n")
        writer.writerow(["package", "license_url", "license_expression"])
        for row in rows:
            writer.writerow([row["package"], row["license_url"], row["license_expression"]])

        summary: dict[str, Any] = {
            "schema": EVIDENCE_SCHEMA,
            "source_commit": source_commit,
            "target": {"goos": goos, "goarch": goarch},
            "main_package": package,
            "go_executable_sha256": _sha256_file(selected_go),
            "scanner": {
                "module": TOOL_MODULE,
                "version": TOOL_VERSION,
                "binary_sha256": _sha256_file(go_licenses),
            },
            "license_review_status": "unreviewed",
            "license_review_required": True,
            "release_clearance": False,
            "license_record_count": len(rows),
            "license_url_unknown_count": sum(
                row["license_url"].casefold() == "unknown" for row in rows
            ),
            "unknown_license_expression_count": 0,
            "saved_notice_file_count": len(notice_files),
            "saved_notice_total_bytes": sum(item["size_bytes"] for item in notice_files),
            "saved_notice_files": notice_files,
            "diagnostics": {
                "report": _diagnostic_counts(report.stderr),
                "save": _diagnostic_counts(saved.stderr),
            },
        }
        readme = (
            "# Go dependency license evidence\n\n"
            "This bundle contains license-scanner output and notice files for one exact "
            "Threadforge CLI target. `report.csv` records the scanner's package, source URL, "
            "and license-expression classification; `licenses/` contains files returned by "
            "the scanner's save operation.\n\n"
            f"Source commit: `{source_commit}`. Target: `{goos}/{goarch}`. "
            f"Scanner: `{TOOL_MODULE}@{TOOL_VERSION}`.\n\n"
            "All license records remain unreviewed. Scanner classifications are evidence, "
            "not legal conclusions. This archive does not grant redistribution clearance; "
            "`release_clearance` remains false. Review this output with the repository's "
            "third-party ledger and the complete Python/container/runtime inventory.\n"
        ).encode("utf-8")
        entries = {
            "README.md": readme,
            "evidence.json": (json.dumps(summary, indent=2, sort_keys=True) + "\n").encode("utf-8"),
            "report.csv": report_buffer.getvalue().encode("utf-8"),
        }
        entries.update({f"licenses/{relative}": data for relative, data in file_contents.items()})
        _write_deterministic_tar(output, entries, source_date_epoch)
    return summary


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go-licenses", type=Path, required=True)
    parser.add_argument("--package", default="./cmd/threadforge")
    parser.add_argument("--goos", required=True, choices=sorted({item[0] for item in ALLOWED_TARGETS}))
    parser.add_argument("--goarch", required=True, choices=sorted({item[1] for item in ALLOWED_TARGETS}))
    parser.add_argument("--go", default="go", dest="go_executable")
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-date-epoch", required=True, type=int)
    parser.add_argument("--output", type=Path, required=True)
    arguments = parser.parse_args(argv)
    try:
        summary = build_license_evidence(
            go_licenses=arguments.go_licenses,
            package=arguments.package,
            goos=arguments.goos,
            goarch=arguments.goarch,
            go_executable=arguments.go_executable,
            source_commit=arguments.source_commit,
            source_date_epoch=arguments.source_date_epoch,
            output=arguments.output,
        )
    except LicenseEvidenceError as error:
        print(f"Go license evidence collection failed: {error}", file=sys.stderr)
        return 2
    log_summary = {
        key: summary[key]
        for key in (
            "schema",
            "source_commit",
            "target",
            "scanner",
            "license_review_status",
            "license_review_required",
            "release_clearance",
            "license_record_count",
            "license_url_unknown_count",
            "unknown_license_expression_count",
            "saved_notice_file_count",
            "saved_notice_total_bytes",
            "diagnostics",
        )
    }
    print(json.dumps(log_summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
