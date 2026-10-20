# Copyright 2026 Threadforge contributors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile
from importlib.metadata import PackageNotFoundError, version
from pathlib import Path, PurePosixPath
from typing import Iterable

from packaging.version import Version


SCRIPT_PATH = Path(__file__).resolve()
PYTHON_ROOT = SCRIPT_PATH.parents[1]
REPOSITORY_ROOT = PYTHON_ROOT.parent
IGNORED_NAMES = {
    ".pytest_cache",
    ".ruff_cache",
    ".venv",
    "__pycache__",
    "build",
    "dist",
    "tests",
}
REQUIRED_DISTRIBUTION_FILES = {
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
}
FORBIDDEN_SUFFIXES = {
    ".gguf",
    ".npz",
    ".parquet",
    ".pt",
    ".pth",
    ".safetensors",
}


def _ignore_python_outputs(directory: str, names: list[str]) -> set[str]:
    ignored = {name for name in names if name in IGNORED_NAMES or name.endswith(".egg-info")}
    if Path(directory).name == "workspace" and "runtime" in names:
        # The sdist hook must freshly stage these assets from the pinned source
        # directories instead of inheriting a previous local build's output.
        ignored.add("runtime")
    return ignored


def _assert_no_symlinks(root: Path) -> None:
    if root.is_symlink() or not root.is_dir():
        raise RuntimeError("required package source directory is missing or unsafe")
    for current, directories, filenames in os.walk(root, topdown=True, followlinks=False):
        current_path = Path(current)
        retained: list[str] = []
        for name in directories:
            entry = current_path / name
            if entry.is_symlink():
                raise RuntimeError("required package source contains a symbolic link")
            if name in IGNORED_NAMES or name.endswith(".egg-info"):
                continue
            if current_path.name == "workspace" and name == "runtime":
                continue
            retained.append(name)
        directories[:] = retained
        if any((current_path / name).is_symlink() for name in filenames):
            raise RuntimeError("required package source contains a symbolic link")


def _copy_sources(staging_root: Path) -> None:
    sources = (
        (PYTHON_ROOT, staging_root / "python", _ignore_python_outputs),
        (REPOSITORY_ROOT / "domains" / "workspace", staging_root / "domains" / "workspace", shutil.ignore_patterns("__pycache__", ".pytest_cache", ".ruff_cache")),
        (REPOSITORY_ROOT / "third_party" / "archipelago", staging_root / "third_party" / "archipelago", shutil.ignore_patterns("__pycache__", ".pytest_cache", ".ruff_cache")),
    )
    for source, destination, ignore in sources:
        _assert_no_symlinks(source)
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(source, destination, ignore=ignore)


def _run(command: list[str], *, cwd: Path, input_text: str | None = None, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        command,
        cwd=cwd,
        env=env,
        input=input_text,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        detail = "\n".join((result.stderr or result.stdout).splitlines()[-16:])
        raise RuntimeError(f"distribution command failed with exit {result.returncode}: {detail}")
    return result


def _archive_paths(paths: Iterable[str]) -> set[str]:
    return {str(PurePosixPath(name)) for name in paths if name and not name.endswith("/")}


def _sdist_paths(filename: Path) -> set[str]:
    with tarfile.open(filename, mode="r:gz") as archive:
        members = [member for member in archive.getmembers() if member.isfile()]
    roots = {PurePosixPath(member.name).parts[0] for member in members if PurePosixPath(member.name).parts}
    if len(roots) != 1:
        raise RuntimeError("source archive does not have one normalized root directory")
    root = next(iter(roots))
    return _archive_paths(
        "/".join(PurePosixPath(member.name).parts[1:])
        for member in members
        if PurePosixPath(member.name).parts and PurePosixPath(member.name).parts[0] == root
    )


def _wheel_paths(filename: Path) -> set[str]:
    with zipfile.ZipFile(filename) as archive:
        return _archive_paths(archive.namelist())


def _verify_archive(paths: set[str], *, wheel: bool) -> int:
    missing = REQUIRED_DISTRIBUTION_FILES - paths
    if missing:
        raise RuntimeError("Python distribution omits required runtime assets or notices")
    if wheel:
        for license_name in ("LICENSE.anyjev", "LICENSE.rrsi"):
            if not any(path.endswith(f".dist-info/licenses/{license_name}") for path in paths):
                raise RuntimeError("Python wheel omits a vendored-library license notice")
    for path in paths:
        lowered = path.lower()
        if lowered.endswith(tuple(FORBIDDEN_SUFFIXES)):
            raise RuntimeError("Python distribution unexpectedly contains model or benchmark binary data")
        if "/bench/results/" in f"/{lowered}" or "/datasets/" in f"/{lowered}":
            raise RuntimeError("Python distribution unexpectedly contains benchmark results or datasets")
    return len(paths)


def _install_and_smoke(wheel: Path, target: Path, work: Path) -> None:
    _run(
        [sys.executable, "-m", "pip", "install", "--disable-pip-version-check", "--no-deps", "--no-compile", "--target", str(target), str(wheel)],
        cwd=work,
    )
    env = os.environ.copy()
    env.pop("PYTHONPATH", None)
    env["PYTHONPATH"] = str(target)
    request = json.dumps(
        {"schema_version": "threadforge.python.v1", "operation": "capabilities", "params": {}},
        separators=(",", ":"),
    ) + "\n"
    result = _run([sys.executable, "-m", "threadforge_py"], cwd=work, input_text=request, env=env)
    lines = result.stdout.splitlines()
    if len(lines) != 1:
        raise RuntimeError("installed Python protocol did not return exactly one JSON response")
    response = json.loads(lines[0])
    operations = response.get("result", {}).get("operations", [])
    if response.get("schema_version") != "threadforge.python.v1" or response.get("status") != "ok":
        raise RuntimeError("installed Python protocol capabilities request failed")
    if "memory.learn" not in operations or "evolution.screen.v2" not in operations:
        raise RuntimeError("installed Python protocol is missing required operations")


def _verify_build_backend() -> None:
    try:
        setuptools_version = Version(version("setuptools"))
        version("wheel")
    except PackageNotFoundError as error:
        raise RuntimeError("distribution smoke requires installed setuptools>=77 and wheel; install the test extra") from error
    if setuptools_version < Version("77"):
        raise RuntimeError("distribution smoke requires installed setuptools>=77; install the test extra")


def main() -> int:
    _verify_build_backend()
    with tempfile.TemporaryDirectory(prefix="threadforge-python-dist-") as temporary:
        work = Path(temporary)
        staging = work / "source"
        distribution = work / "dist"
        installation = work / "installed-wheel"
        _copy_sources(staging)
        distribution.mkdir()
        _run(
            [sys.executable, "-m", "build", "--no-isolation", "--outdir", str(distribution), str(staging / "python")],
            cwd=staging,
        )
        sdists = list(distribution.glob("*.tar.gz"))
        wheels = list(distribution.glob("*.whl"))
        if len(sdists) != 1 or len(wheels) != 1:
            raise RuntimeError("source build must produce exactly one sdist and one wheel")
        sdist_count = _verify_archive(_sdist_paths(sdists[0]), wheel=False)
        wheel_count = _verify_archive(_wheel_paths(wheels[0]), wheel=True)
        _install_and_smoke(wheels[0], installation, work)
        print(json.dumps({
            "schema_version": "threadforge.python-distribution-smoke.v1",
            "source_distribution_files": sdist_count,
            "wheel_files": wheel_count,
            "wheel_installed_to_temporary_target": True,
            "installed_protocol_smoke": True,
            "workspace_and_engineering_runtime_verified": True,
            "vendored_notices_verified": True,
            "model_or_benchmark_binary_payloads_included": False,
        }, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError, tarfile.TarError, zipfile.BadZipFile, json.JSONDecodeError) as error:
        print(f"Python distribution smoke failed: {error}", file=sys.stderr)
        raise SystemExit(1) from None
