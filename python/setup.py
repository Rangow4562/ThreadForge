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

import shutil
from pathlib import Path

from setuptools import setup
from setuptools.command.build_py import build_py
from setuptools.command.sdist import sdist


SETUP_DIR = Path(__file__).resolve().parent
SOURCE_ROOT = SETUP_DIR.parent
PACKAGE_RUNTIME = SETUP_DIR / "threadforge_domains" / "workspace" / "runtime"


def _asset_sources() -> tuple[Path, Path]:
    staged_domain = PACKAGE_RUNTIME / "domain"
    staged_archipelago = PACKAGE_RUNTIME / "archipelago"
    if PACKAGE_RUNTIME.exists():
        if (staged_domain.is_dir() and staged_archipelago.is_dir() and
                (staged_domain / "produce.sh").is_file() and
                (staged_archipelago / "harness_workspace" / "main.py").is_file()):
            return staged_domain, staged_archipelago
        raise RuntimeError("pre-existing workspace package runtime is incomplete")
    return SOURCE_ROOT / "domains" / "workspace", SOURCE_ROOT / "third_party" / "archipelago"


def _copy_tree(source: Path, destination: Path) -> list[Path]:
    if not source.is_dir():
        raise RuntimeError(f"required workspace packaging source is missing: {source}")
    copied: list[Path] = []
    for item in sorted(source.rglob("*")):
        relative = item.relative_to(source)
        if any(part in {"__pycache__", ".pytest_cache", ".ruff_cache"} for part in relative.parts):
            continue
        if item.is_symlink():
            raise RuntimeError("workspace packaging source contains a symbolic link")
        target = destination / relative
        if item.is_dir():
            target.mkdir(parents=True, exist_ok=True)
        elif item.is_file():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(item, target)
            copied.append(target)
    if not copied:
        raise RuntimeError("workspace packaging source contains no regular files")
    return copied


def _copy_distribution_notices(destination: Path) -> list[Path]:
    copied: list[Path] = []
    for name in ("CREDITS.anyjev.md", "UPSTREAM_MODIFICATIONS.md"):
        source = SETUP_DIR / name
        if not source.is_file() or source.is_symlink():
            raise RuntimeError(f"required Python distribution notice is missing: {name}")
        target = destination / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
        copied.append(target)
    return copied


class ThreadforgeBuildPy(build_py):
    def run(self) -> None:
        super().run()
        domain_source, archipelago_source = _asset_sources()
        runtime_output = Path(self.build_lib) / "threadforge_domains" / "workspace" / "runtime"
        if runtime_output.exists():
            shutil.rmtree(runtime_output)
        _copy_tree(domain_source, runtime_output / "domain")
        _copy_tree(archipelago_source, runtime_output / "archipelago")
        _copy_distribution_notices(runtime_output / "legal")

    def get_outputs(self, include_bytecode: bool = True) -> list[str]:
        outputs = [
            output for output in super().get_outputs(include_bytecode)
            if "__pycache__" not in Path(output).parts and not output.endswith(".pyc")
        ]
        domain_source, archipelago_source = _asset_sources()
        runtime_output = Path(self.build_lib) / "threadforge_domains" / "workspace" / "runtime"
        for source, destination in (
            (domain_source, runtime_output / "domain"),
            (archipelago_source, runtime_output / "archipelago"),
        ):
            for item in source.rglob("*"):
                relative = item.relative_to(source)
                if (item.is_file() and not item.is_symlink() and
                        not any(part in {"__pycache__", ".pytest_cache", ".ruff_cache"}
                                for part in relative.parts)):
                    outputs.append(str(destination / relative))
        legal_output = runtime_output / "legal"
        outputs.extend(str(legal_output / name) for name in (
            "CREDITS.anyjev.md", "UPSTREAM_MODIFICATIONS.md",
        ))
        return list(dict.fromkeys(outputs))


class ThreadforgeSdist(sdist):
    def run(self) -> None:
        if PACKAGE_RUNTIME.exists():
            _asset_sources()
            super().run()
            return
        domain_source, archipelago_source = _asset_sources()
        PACKAGE_RUNTIME.mkdir(parents=True)
        try:
            _copy_tree(domain_source, PACKAGE_RUNTIME / "domain")
            _copy_tree(archipelago_source, PACKAGE_RUNTIME / "archipelago")
            _copy_distribution_notices(PACKAGE_RUNTIME / "legal")
            super().run()
        finally:
            shutil.rmtree(PACKAGE_RUNTIME)


setup(cmdclass={"build_py": ThreadforgeBuildPy, "sdist": ThreadforgeSdist})
