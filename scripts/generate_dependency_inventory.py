#!/usr/bin/env python3
"""Generate a deterministic inventory from Threadforge's checked-in dependency manifests.

This is not a license approval tool or a complete release SBOM. Unknown license
metadata is retained as unreviewed and the output always has clearance=false.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Any


SCHEMA_VERSION = "threadforge.dependency-inventory.v2"
PYTHON_LOCK_HEADER = re.compile(r"^([A-Za-z0-9][A-Za-z0-9._-]*)==([^\s\\]+)")
PYTHON_EXTRA_REQUIREMENT = re.compile(
    r"^\s*([A-Za-z0-9][A-Za-z0-9._-]*)(?:\[[^\]]+\])?(.*)$"
)
PYTHON_HASH = re.compile(r"--hash=sha256:([0-9a-f]{64})")


class InventoryError(RuntimeError):
    pass


def digest_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def component(
    *,
    ecosystem: str,
    name: str,
    version: str | None,
    version_constraint: str | None,
    scope: str,
    group: str,
    direct: bool | None,
    license_declared: str | None,
    source_manifest: str,
    source_manifest_sha256: str,
    integrity: list[str] | None = None,
    replaced: bool = False,
) -> dict[str, Any]:
    return {
        "ecosystem": ecosystem,
        "name": name,
        "version": version,
        "version_constraint": version_constraint,
        "scope": scope,
        "group": group,
        "direct": direct,
        "license_declared": license_declared,
        "license_review_status": "unreviewed",
        "source_manifest": source_manifest,
        "source_manifest_sha256": source_manifest_sha256,
        "integrity": sorted(set(integrity or [])),
        "replaced": replaced,
    }


def register_manifest(root: Path, manifests: dict[str, dict[str, str]], relative: str) -> str:
    path = root / relative
    if not path.is_file():
        raise InventoryError(f"required dependency manifest is missing: {relative}")
    digest = digest_file(path)
    manifests[relative] = {"path": relative, "sha256": digest}
    return digest


def parse_json_stream(value: str) -> list[dict[str, Any]]:
    decoder = json.JSONDecoder()
    index = 0
    objects: list[dict[str, Any]] = []
    while index < len(value):
        while index < len(value) and value[index].isspace():
            index += 1
        if index == len(value):
            break
        item, index = decoder.raw_decode(value, index)
        if not isinstance(item, dict):
            raise InventoryError("go list returned a non-object module record")
        objects.append(item)
    return objects


def go_components(
    root: Path, manifests: dict[str, dict[str, str]]
) -> list[dict[str, Any]]:
    go_mod_digest = register_manifest(root, manifests, "go.mod")
    register_manifest(root, manifests, "go.sum")
    executable = shutil.which("go")
    if executable is None:
        raise InventoryError("Go is required to resolve the module dependency graph")

    env = os.environ.copy()
    existing_flags = env.get("GOFLAGS", "").strip()
    env["GOFLAGS"] = (existing_flags + " -mod=readonly").strip()
    try:
        result = subprocess.run(
            [executable, "list", "-m", "-json", "all"],
            cwd=root,
            env=env,
            check=False,
            capture_output=True,
            text=True,
            timeout=120,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise InventoryError(f"could not resolve Go modules: {error}") from error
    if result.returncode:
        raise InventoryError(
            "go list -m failed without modifying go.mod/go.sum: "
            + result.stderr.strip()[:1000]
        )

    items = []
    for module in parse_json_stream(result.stdout):
        if module.get("Main"):
            continue
        module_path = module.get("Path")
        if not isinstance(module_path, str) or not module_path:
            raise InventoryError("go list returned a module without a path")
        integrity = [
            value
            for value in (module.get("Sum"), module.get("GoModSum"))
            if isinstance(value, str) and value
        ]
        items.append(
            component(
                ecosystem="golang",
                name=module_path,
                version=module.get("Version") or None,
                version_constraint=None,
                scope="runtime-and-build",
                group="module-graph",
                direct=not bool(module.get("Indirect", False)),
                license_declared=None,
                source_manifest="go.mod",
                source_manifest_sha256=go_mod_digest,
                integrity=integrity,
                replaced=bool(module.get("Replace")),
            )
        )
    return items


def npm_components(
    root: Path, manifests: dict[str, dict[str, str]], package_json_path: str, lock_path: str
) -> list[dict[str, Any]]:
    register_manifest(root, manifests, package_json_path)
    lock_digest = register_manifest(root, manifests, lock_path)
    try:
        lock = json.loads((root / lock_path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise InventoryError(f"invalid npm lockfile {lock_path}: {error}") from error
    packages = lock.get("packages")
    if not isinstance(packages, dict):
        raise InventoryError(f"npm lockfile {lock_path} has no packages map")

    root_package = packages.get("", {})
    direct_groups: dict[str, str] = {}
    if isinstance(root_package, dict):
        for key, scope in (
            ("dependencies", "runtime"),
            ("devDependencies", "development"),
            ("optionalDependencies", "optional"),
            ("peerDependencies", "peer"),
        ):
            values = root_package.get(key, {})
            if isinstance(values, dict):
                for name in values:
                    direct_groups[name] = scope

    items = []
    for package_key, package in packages.items():
        if not package_key.startswith("node_modules/") or not isinstance(package, dict):
            continue
        name = package.get("name") or package_key.rsplit("node_modules/", 1)[-1]
        if not isinstance(name, str) or not name:
            raise InventoryError(f"npm lockfile {lock_path} has an unnamed package")
        license_value = package.get("license")
        if isinstance(license_value, dict):
            license_value = license_value.get("type")
        if not isinstance(license_value, str) or not license_value.strip():
            license_value = None
        integrity = package.get("integrity")
        if isinstance(integrity, str) and integrity:
            integrity_values = [integrity]
        else:
            integrity_values = []
        scope = direct_groups.get(name)
        if scope is None:
            scope = "development" if package.get("dev") else "runtime"
            if package.get("optional") and scope == "runtime":
                scope = "optional"
        items.append(
            component(
                ecosystem="npm",
                name=name,
                version=package.get("version") or None,
                version_constraint=None,
                scope=scope,
                group="package-lock",
                direct=name in direct_groups,
                license_declared=license_value,
                source_manifest=lock_path,
                source_manifest_sha256=lock_digest,
                integrity=integrity_values,
            )
        )
    if not items:
        raise InventoryError(f"npm lockfile {lock_path} contains no package entries")
    return items


def parse_python_lock(
    text: str, source_manifest: str, source_digest: str, scope: str
) -> list[dict[str, Any]]:
    items: list[dict[str, Any]] = []
    current: dict[str, Any] | None = None

    def flush() -> None:
        if current is None:
            return
        items.append(
            component(
                ecosystem="pypi",
                name=current["name"],
                version=current["version"],
                version_constraint=None,
                scope=scope,
                group="hash-locked",
                direct=None,
                license_declared=None,
                source_manifest=source_manifest,
                source_manifest_sha256=source_digest,
                integrity=current["integrity"],
            )
        )

    for line in text.splitlines():
        match = PYTHON_LOCK_HEADER.match(line.strip())
        if match:
            flush()
            current = {
                "name": match.group(1),
                "version": match.group(2),
                "integrity": [],
            }
        if current is not None:
            current["integrity"].extend(PYTHON_HASH.findall(line))
    flush()
    if not items:
        raise InventoryError(f"Python lockfile {source_manifest} has no pinned distributions")
    if any(not item["integrity"] for item in items):
        missing = [item["name"] for item in items if not item["integrity"]]
        raise InventoryError(
            f"Python lockfile {source_manifest} has entries without hashes: {missing[:10]}"
        )
    return items


def python_lock_components(
    root: Path, manifests: dict[str, dict[str, str]], relative: str, scope: str
) -> list[dict[str, Any]]:
    digest = register_manifest(root, manifests, relative)
    return parse_python_lock((root / relative).read_text(encoding="utf-8"), relative, digest, scope)


def parse_requirement(requirement: str) -> tuple[str, str]:
    match = PYTHON_EXTRA_REQUIREMENT.match(requirement)
    if match is None:
        raise InventoryError(f"unrecognized Python requirement: {requirement}")
    name = match.group(1)
    constraint = match.group(2).strip() or "*"
    return name, constraint


def python_extra_components(
    root: Path, manifests: dict[str, dict[str, str]], relative: str
) -> list[dict[str, Any]]:
    digest = register_manifest(root, manifests, relative)
    lines = (root / relative).read_text(encoding="utf-8").splitlines()
    in_section = False
    group: str | None = None
    items = []
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            in_section = stripped == "[project.optional-dependencies]"
            group = None
            continue
        if not in_section:
            continue
        group_match = re.match(r"^([A-Za-z0-9_-]+)\s*=\s*\[", stripped)
        if group_match:
            group = group_match.group(1)
        if group is None:
            continue
        for requirement in re.findall(r'"([^"]+)"', stripped):
            name, constraint = parse_requirement(requirement)
            items.append(
                component(
                    ecosystem="pypi",
                    name=name,
                    version=None,
                    version_constraint=constraint,
                    scope="optional",
                    group=group,
                    direct=True,
                    license_declared=None,
                    source_manifest=relative,
                    source_manifest_sha256=digest,
                )
            )
        if stripped == "]":
            group = None
    if not items:
        raise InventoryError(f"no optional dependencies found in {relative}")
    return items


def license_metadata_collection(items: list[dict[str, Any]]) -> dict[str, Any]:
    """Summarize missing manifest declarations without inferring licenses."""
    missing_by_ecosystem = {ecosystem: 0 for ecosystem in ("golang", "npm", "pypi")}
    missing_by_source: dict[tuple[str, str], int] = {}
    declared = 0
    for item in items:
        if item["license_declared"] is not None:
            declared += 1
            continue
        ecosystem = item["ecosystem"]
        source = item["source_manifest"]
        missing_by_ecosystem[ecosystem] += 1
        key = (source, ecosystem)
        missing_by_source[key] = missing_by_source.get(key, 0) + 1
    missing = len(items) - declared
    return {
        "status": "source-declarations-only",
        "declared_components": declared,
        "missing_components": missing,
        "missing_by_ecosystem": missing_by_ecosystem,
        "missing_by_source_manifest": [
            {"source_manifest": source, "ecosystem": ecosystem, "components": count}
            for (source, ecosystem), count in sorted(missing_by_source.items())
        ],
        "automatic_license_inference": False,
        "license_review_complete": False,
        "release_clearance": False,
    }


def build_inventory(root: Path) -> dict[str, Any]:
    manifests: dict[str, dict[str, str]] = {}
    items = go_components(root, manifests)
    items.extend(
        npm_components(
            root,
            manifests,
            "adapters/package.json",
            "adapters/package-lock.json",
        )
    )
    items.extend(
        npm_components(
            root,
            manifests,
            "adapters/browser-extension/package.json",
            "adapters/browser-extension/package-lock.json",
        )
    )
    items.extend(
        python_lock_components(
            root,
            manifests,
            "containers/engineering/requirements-linux-x86_64-py312.lock",
            "runtime",
        )
    )
    items.extend(
        python_lock_components(
            root, manifests, "containers/engineering/build-requirements.lock", "build"
        )
    )
    items.extend(python_extra_components(root, manifests, "python/pyproject.toml"))
    items.sort(
        key=lambda item: (
            item["source_manifest"],
            item["ecosystem"],
            item["name"].casefold(),
            item["version"] or "",
            item["group"],
        )
    )
    missing_license = sum(item["license_declared"] is None for item in items)
    unresolved_versions = sum(item["version"] is None for item in items)
    license_metadata = license_metadata_collection(items)
    return {
        "schema_version": SCHEMA_VERSION,
        "release_clearance": False,
        "coverage": {
            "status": "source-manifest-inventory-only",
            "resolved_python_optional_extras": False,
            "license_review_complete": False,
            "license_metadata_collection": license_metadata,
            "note": (
                "This inventory records checked-in dependency manifests only. It is not a "
                "complete artifact SBOM, legal opinion, or redistribution approval."
            ),
        },
        "counts": {
            "components": len(items),
            "manifests": len(manifests),
            "unresolved_versions": unresolved_versions,
            "missing_declared_license_metadata": missing_license,
        },
        "manifests": [manifests[key] for key in sorted(manifests)],
        "components": items,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=Path(__file__).resolve().parents[1],
        help="Threadforge repository root",
    )
    parser.add_argument(
        "--output",
        type=Path,
        help="write JSON to a new file; omit to print JSON to stdout",
    )
    args = parser.parse_args(argv)
    try:
        report = build_inventory(args.repo_root.resolve())
        rendered = json.dumps(report, indent=2, sort_keys=True) + "\n"
        if args.output is None:
            sys.stdout.write(rendered)
        else:
            output = args.output.resolve()
            output.parent.mkdir(parents=True, exist_ok=True)
            with output.open("x", encoding="utf-8", newline="\n") as stream:
                stream.write(rendered)
            print(
                json.dumps(
                    {
                        "schema_version": SCHEMA_VERSION,
                        "output": str(output),
                        "components": report["counts"]["components"],
                        "manifests": report["counts"]["manifests"],
                        "unresolved_versions": report["counts"]["unresolved_versions"],
                        "missing_declared_license_metadata": report["counts"][
                            "missing_declared_license_metadata"
                        ],
                        "license_metadata_missing_by_ecosystem": report["coverage"][
                            "license_metadata_collection"
                        ]["missing_by_ecosystem"],
                        "release_clearance": False,
                    },
                    sort_keys=True,
                )
            )
    except (InventoryError, OSError, json.JSONDecodeError) as error:
        print(f"dependency inventory failed: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
