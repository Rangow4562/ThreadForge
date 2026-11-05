#!/usr/bin/env python3
"""Reconcile a release binary's SPDX package set with its embedded Go build info.

This check establishes component coverage for the Go executable only. It does
not review license compatibility or grant release clearance.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import stat
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable


SCHEMA = "threadforge.release-binary-sbom-check.v1"
MAX_SBOM_BYTES = 32 * 1024 * 1024
GO_TOOLCHAIN_RE = re.compile(r"\bgo[0-9]+(?:\.[0-9]+){1,2}(?:[A-Za-z0-9.-]*)?\b")


class ReleaseSBOMError(ValueError):
    """Raised when an artifact SBOM does not reconcile to the binary."""


@dataclass(frozen=True)
class GoBuildInfo:
    toolchain: str
    main_module: str
    dependencies: dict[str, tuple[str, bool]]


def parse_go_build_info(output: str) -> GoBuildInfo:
    """Parse the stable module records emitted by ``go version -m``."""
    first_line = output.splitlines()[0] if output.splitlines() else ""
    toolchain_match = GO_TOOLCHAIN_RE.search(first_line)
    if toolchain_match is None:
        raise ReleaseSBOMError("go version -m did not report a Go toolchain")

    main_module: str | None = None
    dependencies: dict[str, tuple[str, bool]] = {}
    last_dependency: str | None = None

    for line in output.splitlines()[1:]:
        fields = line.strip().split()
        if not fields:
            last_dependency = None
            continue

        if fields[0] == "mod":
            if len(fields) < 2 or main_module is not None:
                raise ReleaseSBOMError("go version -m reported invalid main-module metadata")
            main_module = fields[1]
            last_dependency = None
        elif fields[0] == "dep":
            if len(fields) < 3:
                raise ReleaseSBOMError("go version -m reported an incomplete dependency")
            name, version = fields[1], fields[2]
            if name in dependencies:
                raise ReleaseSBOMError(f"go version -m repeated dependency {name}")
            dependencies[name] = (version, False)
            last_dependency = name
        elif fields[0] == "=>":
            if last_dependency is None:
                raise ReleaseSBOMError("go version -m reported a replacement without a dependency")
            version, _ = dependencies[last_dependency]
            dependencies[last_dependency] = (version, True)
            last_dependency = None
        else:
            last_dependency = None

    if main_module is None:
        raise ReleaseSBOMError("go version -m did not report the main module")
    return GoBuildInfo(
        toolchain=toolchain_match.group(0),
        main_module=main_module,
        dependencies=dependencies,
    )


def _is_regular_non_link(path: Path) -> bool:
    try:
        mode = path.lstat().st_mode
    except OSError:
        return False
    return stat.S_ISREG(mode)


def _license_not_asserted(package: dict[str, Any], field: str) -> bool:
    value = package.get(field)
    return not isinstance(value, str) or value.strip().upper() in {"", "NOASSERTION"}


def reconcile_sbom(
    document: dict[str, Any],
    build_info: GoBuildInfo,
    *,
    expected_main_module: str,
    binary_name: str,
) -> dict[str, Any]:
    if document.get("spdxVersion") != "SPDX-2.3":
        raise ReleaseSBOMError("SBOM must be SPDX-2.3 JSON")
    if build_info.main_module != expected_main_module:
        raise ReleaseSBOMError(
            f"binary main module {build_info.main_module!r} does not match "
            f"expected {expected_main_module!r}"
        )

    packages = document.get("packages")
    if not isinstance(packages, list) or not packages:
        raise ReleaseSBOMError("SPDX document must contain a non-empty packages array")

    packages_by_name: dict[str, dict[str, Any]] = {}
    for package in packages:
        if not isinstance(package, dict):
            raise ReleaseSBOMError("SPDX packages must be objects")
        name = package.get("name")
        if not isinstance(name, str) or not name.strip():
            raise ReleaseSBOMError("SPDX package is missing its name")
        if name in packages_by_name:
            raise ReleaseSBOMError(f"SPDX package name is duplicated: {name}")
        packages_by_name[name] = package

    expected_modules = {build_info.main_module, *build_info.dependencies}
    allowed_binary_packages = {"threadforge", binary_name}
    expected_packages = expected_modules | allowed_binary_packages | {"stdlib"}
    actual_packages = set(packages_by_name)

    missing = sorted(expected_packages - actual_packages)
    if missing:
        raise ReleaseSBOMError(
            "SPDX is missing Go build-info packages: " + ", ".join(missing)
        )
    unexpected = sorted(actual_packages - expected_packages)
    if unexpected:
        raise ReleaseSBOMError(
            "SPDX contains packages not explained by the Go binary: "
            + ", ".join(unexpected)
        )

    version_mismatches: list[str] = []
    for name, (expected_version, replaced) in build_info.dependencies.items():
        if replaced:
            continue
        actual_version = packages_by_name[name].get("versionInfo")
        if actual_version != expected_version:
            version_mismatches.append(
                f"{name}: binary={expected_version!r}, SPDX={actual_version!r}"
            )
    stdlib_version = packages_by_name["stdlib"].get("versionInfo")
    if stdlib_version != build_info.toolchain:
        version_mismatches.append(
            f"stdlib: binary={build_info.toolchain!r}, SPDX={stdlib_version!r}"
        )
    if version_mismatches:
        raise ReleaseSBOMError("SPDX version mismatch: " + "; ".join(version_mismatches))

    return {
        "schema": SCHEMA,
        "main_module": build_info.main_module,
        "go_toolchain": build_info.toolchain,
        "go_module_count": len(expected_modules),
        "spdx_package_count": len(packages),
        "declared_license_not_asserted": sum(
            _license_not_asserted(package, "licenseDeclared")
            for package in packages
        ),
        "concluded_license_not_asserted": sum(
            _license_not_asserted(package, "licenseConcluded")
            for package in packages
        ),
        "license_review_required": True,
        "release_clearance": False,
    }


def verify_release_sbom(
    binary_path: Path,
    sbom_path: Path,
    *,
    expected_main_module: str,
    go_executable: str = "go",
    run: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> dict[str, Any]:
    if not _is_regular_non_link(binary_path):
        raise ReleaseSBOMError("binary must be an existing regular non-symlink file")
    if not _is_regular_non_link(sbom_path):
        raise ReleaseSBOMError("SBOM must be an existing regular non-symlink file")
    if sbom_path.stat().st_size > MAX_SBOM_BYTES:
        raise ReleaseSBOMError("SPDX document exceeds the 32 MiB safety limit")

    try:
        document = json.loads(sbom_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ReleaseSBOMError(f"could not read SPDX JSON: {error}") from error
    if not isinstance(document, dict):
        raise ReleaseSBOMError("SPDX document root must be an object")

    env = os.environ.copy()
    env["GOFLAGS"] = ""
    env["GOWORK"] = "off"
    try:
        result = run(
            [go_executable, "version", "-m", str(binary_path)],
            check=False,
            capture_output=True,
            text=True,
            timeout=20,
            env=env,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ReleaseSBOMError(f"could not inspect Go binary build info: {error}") from error
    if result.returncode != 0:
        detail = result.stderr.strip()[:1000]
        raise ReleaseSBOMError(
            "go version -m failed" + (f": {detail}" if detail else "")
        )

    build_info = parse_go_build_info(result.stdout)
    return reconcile_sbom(
        document,
        build_info,
        expected_main_module=expected_main_module,
        binary_name=binary_path.name,
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--sbom", type=Path, required=True)
    parser.add_argument("--main-module", required=True)
    parser.add_argument("--go", default="go", dest="go_executable")
    arguments = parser.parse_args(argv)

    try:
        report = verify_release_sbom(
            arguments.binary,
            arguments.sbom,
            expected_main_module=arguments.main_module,
            go_executable=arguments.go_executable,
        )
    except ReleaseSBOMError as error:
        print(f"release SBOM verification failed: {error}", file=sys.stderr)
        return 2
    print(json.dumps(report, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
