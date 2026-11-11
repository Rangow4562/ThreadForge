#!/usr/bin/env python3
"""Build and audit version-matched Threadforge Python release artifacts.

The staged source is versioned without modifying the checkout. The wheel is
built from the verified source archive so the two deliverables cannot silently
diverge. No models, benchmark datasets, credentials, or runtime dependencies are
downloaded by this script.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
import re
import shutil
import stat
import sys
import tarfile
import tempfile
import tomllib
import zipfile
from email.parser import BytesParser
from pathlib import Path, PurePosixPath

REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
HELPER_PATH = REPOSITORY_ROOT / "python" / "scripts" / "verify_distribution.py"
HELPER_SPEC = importlib.util.spec_from_file_location("threadforge_verify_distribution", HELPER_PATH)
if HELPER_SPEC is None or HELPER_SPEC.loader is None:
    raise RuntimeError("Python distribution verifier is unavailable")
HELPERS = importlib.util.module_from_spec(HELPER_SPEC)
HELPER_SPEC.loader.exec_module(HELPERS)
REQUIRED_DISTRIBUTION_FILES = HELPERS.REQUIRED_DISTRIBUTION_FILES
_copy_sources = HELPERS._copy_sources
_install_and_smoke = HELPERS._install_and_smoke
_run = HELPERS._run
_sdist_paths = HELPERS._sdist_paths
_verify_archive = HELPERS._verify_archive
_verify_build_backend = HELPERS._verify_build_backend
_wheel_paths = HELPERS._wheel_paths


VERSION_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
PROJECT_NAME = "threadforge-python"
MAX_SDIST_MEMBERS = 25_000
MAX_SDIST_BYTES = 512 * 1024 * 1024


def _set_staged_version(pyproject: Path, version: str) -> None:
    text = pyproject.read_text(encoding="utf-8")
    section = re.search(r"(?ms)^\[project\]\s*\r?\n(.*?)(?=^\[|\Z)", text)
    if section is None:
        raise RuntimeError("staged pyproject.toml has no project table")
    body = section.group(1)
    updated_body, replacements = re.subn(
        r'(?m)^version[ \t]*=[ \t]*["\'][^"\']+["\'][ \t]*$',
        f'version = "{version}"',
        body,
    )
    if replacements != 1:
        raise RuntimeError("staged project version is missing or ambiguous")
    updated = text[: section.start(1)] + updated_body + text[section.end(1) :]
    try:
        parsed = tomllib.loads(updated)
    except tomllib.TOMLDecodeError as error:
        raise RuntimeError("versioned staged pyproject.toml is malformed") from error
    project = parsed.get("project")
    if not isinstance(project, dict) or project.get("version") != version:
        raise RuntimeError("staged Python distribution version did not match the release")
    pyproject.write_text(updated, encoding="utf-8", newline="\n")


def _safe_sdist_extract(sdist: Path, destination: Path) -> Path:
    seen: set[str] = set()
    roots: set[str] = set()
    total_bytes = 0
    try:
        with tarfile.open(sdist, mode="r:gz") as archive:
            members = archive.getmembers()
            if not members or len(members) > MAX_SDIST_MEMBERS:
                raise RuntimeError("source archive has an invalid member count")
            for member in members:
                name = member.name
                if not name or "\\" in name or "\x00" in name:
                    raise RuntimeError("source archive contains an unsafe member path")
                path = PurePosixPath(name)
                if (
                    path.is_absolute()
                    or any(part in {"", ".", ".."} for part in path.parts)
                    or not path.parts
                    or (len(path.parts) == 1 and not member.isdir())
                ):
                    raise RuntimeError("source archive contains an unsafe member path")
                normalized = path.as_posix()
                if normalized in seen or not (member.isdir() or member.isfile()):
                    raise RuntimeError("source archive contains duplicate or non-regular members")
                seen.add(normalized)
                roots.add(path.parts[0])
                if member.isfile():
                    if member.size < 0:
                        raise RuntimeError("source archive contains an invalid file size")
                    total_bytes += member.size
                    if total_bytes > MAX_SDIST_BYTES:
                        raise RuntimeError("source archive exceeds the extraction limit")
            if len(roots) != 1:
                raise RuntimeError("source archive does not have one root directory")
            root_name = next(iter(roots))
            destination.mkdir()
            project_root = destination / root_name
            project_root.mkdir()
            for member in members:
                relative = PurePosixPath(member.name).parts[1:]
                target = project_root.joinpath(*relative)
                if member.isdir():
                    target.mkdir(parents=True, exist_ok=True)
                    continue
                target.parent.mkdir(parents=True, exist_ok=True)
                source = archive.extractfile(member)
                if source is None:
                    raise RuntimeError("source archive member cannot be read")
                remaining = member.size
                with source, target.open("xb") as output:
                    while remaining:
                        chunk = source.read(min(1024 * 1024, remaining))
                        if not chunk:
                            raise RuntimeError("source archive member ended before its declared size")
                        output.write(chunk)
                        remaining -= len(chunk)
            return project_root
    except (OSError, tarfile.TarError, EOFError) as error:
        raise RuntimeError("source archive is malformed or cannot be safely extracted") from error


def _metadata_version(metadata: bytes, *, expected_version: str, label: str) -> None:
    try:
        message = BytesParser().parsebytes(metadata)
    except (TypeError, ValueError) as error:
        raise RuntimeError(f"{label} metadata is malformed") from error
    if message.get("Name", "").casefold().replace("_", "-") != PROJECT_NAME or message.get("Version") != expected_version:
        raise RuntimeError(f"{label} name or version does not match the Threadforge release")


def _verify_sdist_version(sdist: Path, version: str) -> None:
    candidates: list[bytes] = []
    with tarfile.open(sdist, mode="r:gz") as archive:
        for member in archive.getmembers():
            if member.isfile() and len(PurePosixPath(member.name).parts) == 2 and PurePosixPath(member.name).name == "PKG-INFO":
                source = archive.extractfile(member)
                if source is not None:
                    candidates.append(source.read(1024 * 1024 + 1))
    if len(candidates) != 1 or len(candidates[0]) > 1024 * 1024:
        raise RuntimeError("source archive must contain exactly one bounded PKG-INFO")
    _metadata_version(candidates[0], expected_version=version, label="source archive")


def _verify_wheel_version(wheel: Path, version: str) -> None:
    candidates: list[bytes] = []
    with zipfile.ZipFile(wheel) as archive:
        for name in archive.namelist():
            parts = PurePosixPath(name).parts
            if len(parts) == 2 and parts[0].endswith(".dist-info") and parts[1] == "METADATA":
                candidates.append(archive.read(name))
    if len(candidates) != 1 or len(candidates[0]) > 1024 * 1024:
        raise RuntimeError("wheel must contain exactly one bounded dist-info METADATA")
    _metadata_version(candidates[0], expected_version=version, label="wheel")


def build_release_distribution(version: str, output_dir: Path, source_date_epoch: int) -> dict[str, object]:
    if not VERSION_RE.fullmatch(version):
        raise RuntimeError("version must be stable MAJOR.MINOR.PATCH")
    if not isinstance(source_date_epoch, int) or source_date_epoch < 0:
        raise RuntimeError("source date epoch must be a nonnegative integer")
    output = Path(os.path.abspath(output_dir))
    current = output
    while True:
        try:
            info = current.lstat()
        except OSError as error:
            raise RuntimeError("output directory path cannot be inspected") from error
        if stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & 0x400):
            raise RuntimeError("output directory path must not contain links")
        if current == current.parent:
            break
        current = current.parent
    if not stat.S_ISDIR(output.lstat().st_mode):
        raise RuntimeError("output directory must be an existing regular directory")
    sdist_name = f"threadforge_python-{version}.tar.gz"
    wheel_name = f"threadforge_python-{version}-py3-none-any.whl"
    destinations = [output / sdist_name, output / wheel_name]
    if any(path.exists() or path.is_symlink() for path in destinations):
        raise RuntimeError("Python release artifact output already exists")

    _verify_build_backend()
    with tempfile.TemporaryDirectory(prefix="threadforge-python-release-dist-") as temporary:
        work = Path(temporary)
        staging_root = work / "source"
        _copy_sources(staging_root)
        staged_python = staging_root / "python"
        _set_staged_version(staged_python / "pyproject.toml", version)
        sdist_output = work / "sdist"
        sdist_output.mkdir()
        environment = os.environ.copy()
        environment["SOURCE_DATE_EPOCH"] = str(source_date_epoch)
        environment["PYTHONHASHSEED"] = "0"
        _run(
            [sys.executable, "-m", "build", "--no-isolation", "--sdist", "--outdir", str(sdist_output), str(staged_python)],
            cwd=staging_root,
            env=environment,
        )
        sdists = list(sdist_output.glob("*.tar.gz"))
        if len(sdists) != 1 or sdists[0].name != sdist_name:
            raise RuntimeError("versioned Python source archive was not produced exactly once")
        _verify_archive(_sdist_paths(sdists[0]), wheel=False)
        _verify_sdist_version(sdists[0], version)

        extracted_project = _safe_sdist_extract(sdists[0], work / "unpacked-sdist")
        wheel_output = work / "wheel"
        wheel_output.mkdir()
        _run(
            [sys.executable, "-m", "build", "--no-isolation", "--wheel", "--outdir", str(wheel_output), str(extracted_project)],
            cwd=work,
            env=environment,
        )
        wheels = list(wheel_output.glob("*.whl"))
        if len(wheels) != 1 or wheels[0].name != wheel_name:
            raise RuntimeError("versioned Python wheel was not produced exactly once")
        _verify_archive(_wheel_paths(wheels[0]), wheel=True)
        _verify_wheel_version(wheels[0], version)
        _install_and_smoke(wheels[0], work / "installed-wheel", work)

        for source, destination in ((sdists[0], destinations[0]), (wheels[0], destinations[1])):
            with source.open("rb") as input_file, destination.open("xb") as output_file:
                shutil.copyfileobj(input_file, output_file)

    return {
        "schema_version": "threadforge.python-release-distribution.v1",
        "project": PROJECT_NAME,
        "version": version,
        "source_distribution": sdist_name,
        "wheel": wheel_name,
        "source_distribution_sha256": hashlib.sha256(destinations[0].read_bytes()).hexdigest(),
        "wheel_sha256": hashlib.sha256(destinations[1].read_bytes()).hexdigest(),
        "wheel_built_from_verified_source_distribution": True,
        "required_runtime_assets_verified": True,
        "upstream_notices_verified": True,
        "model_or_benchmark_payloads_included": False,
        "release_clearance": False,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="stable release MAJOR.MINOR.PATCH without a leading v")
    parser.add_argument("--source-date-epoch", type=int, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    arguments = parser.parse_args(argv)
    try:
        result = build_release_distribution(arguments.version, arguments.output_dir, arguments.source_date_epoch)
    except (OSError, RuntimeError, tarfile.TarError, zipfile.BadZipFile, json.JSONDecodeError) as error:
        print(f"Python release distribution build failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
