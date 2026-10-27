import importlib.util
import json
import shutil
import sys
from pathlib import Path

import jsonschema
import pytest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = ROOT / "scripts" / "generate_dependency_inventory.py"
SCHEMA_PATH = ROOT / "schema" / "dependency-inventory-v3.schema.json"
SPEC = importlib.util.spec_from_file_location("threadforge_dependency_inventory", SCRIPT_PATH)
assert SPEC is not None and SPEC.loader is not None
inventory = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = inventory
SPEC.loader.exec_module(inventory)


def test_go_json_stream_decodes_concatenated_module_records():
    records = inventory.parse_json_stream('{"Path":"one","Version":"v1"}\n{"Path":"two"}\n')
    assert [record["Path"] for record in records] == ["one", "two"]


def test_python_lock_parser_keeps_hashes_and_marks_license_unreviewed():
    package_hash = "a" * 64
    items = inventory.parse_python_lock(
        f"alpha==1.2.3 \\\n    --hash=sha256:{package_hash}\n",
        "requirements.lock",
        "b" * 64,
        "runtime",
    )
    assert len(items) == 1
    assert items[0]["name"] == "alpha"
    assert items[0]["version"] == "1.2.3"
    assert items[0]["integrity"] == [package_hash]
    assert items[0]["license_declared"] is None
    assert items[0]["license_review_status"] == "unreviewed"


def test_python_lock_parser_rejects_unhashed_pins():
    with pytest.raises(inventory.InventoryError, match="without hashes"):
        inventory.parse_python_lock("alpha==1.2.3\n", "requirements.lock", "b" * 64, "runtime")


def test_python_extra_declarations_preserve_exact_direct_requirements(tmp_path):
    path = tmp_path / "pyproject.toml"
    path.write_text(
        '[project]\n'
        'dependencies = []\n'
        '[project.optional-dependencies]\n'
        'decisions = ["numpy>=1.24"]\n'
        'evolution = ["anthropic[vertex]>=0.40", "litellm>=1.50"]\n',
        encoding="utf-8",
    )
    manifests = {}
    items = inventory.python_extra_components(tmp_path, manifests, "pyproject.toml")
    assert [(item["name"], item["version_constraint"], item["group"]) for item in items] == [
        ("numpy", ">=1.24", "decisions"),
        ("anthropic", ">=0.40", "evolution"),
        ("litellm", ">=1.50", "evolution"),
    ]
    assert all(item["version"] is None and item["license_declared"] is None for item in items)
    declaration_source = {
        "source_manifest": "pyproject.toml",
        "source_manifest_sha256": items[0]["source_manifest_sha256"],
    }
    assert [item["direct_requirements"] for item in items] == [
        [{"group": "decisions", "requirement": "numpy>=1.24", **declaration_source}],
        [{"group": "evolution", "requirement": "anthropic[vertex]>=0.40", **declaration_source}],
        [{"group": "evolution", "requirement": "litellm>=1.50", **declaration_source}],
    ]
    assert manifests["pyproject.toml"]["sha256"] == items[0]["source_manifest_sha256"]


def _write_uv_inventory_fixture(root, *, include_direct=True, use_unsupported_source=False):
    (root / "pyproject.toml").write_text(
        '[project]\n'
        'name = "fixture-project"\n'
        'version = "0.1.0"\n'
        'requires-python = ">=3.10"\n'
        'dependencies = []\n'
        '[project.optional-dependencies]\n'
        'decisions = ["direct-lib[feature]>=2.0", '
        '"private-lib @ https://user:secret@example.test/private.whl"]\n',
        encoding="utf-8",
    )
    direct_record = (
        '[[package]]\n'
        'name = "direct-lib"\n'
        'version = "2.1.0"\n'
        'source = { registry = "https://user:secret@example.test/simple" }\n'
        'resolution-markers = ["python_full_version >= \'3.10\'"]\n'
        'wheels = [{ url = "https://user:secret@example.test/direct.whl", '
        'hash = "sha256:' + "a" * 64 + '" }]\n'
    ) if include_direct else ""
    private_url_record = (
        '[[package]]\n'
        'name = "private-lib"\n'
        'version = "1.0.0"\n'
        'source = { registry = "https://user:secret@example.test/simple" }\n'
        'wheels = [{ url = "https://user:secret@example.test/private.whl", '
        'hash = "sha256:' + "c" * 64 + '" }]\n'
    )
    transitive_source = (
        'source = { git = "https://token:secret@example.test/transitive.git" }\n'
        if use_unsupported_source
        else 'source = { registry = "https://user:secret@example.test/simple" }\n'
    )
    transitive_record = (
        '[[package]]\n'
        'name = "transitive-lib"\n'
        'version = "1.4.2"\n'
        + transitive_source
        + 'sdist = { url = "file:///C:/private/transitive.tar.gz", hash = "sha256:'
        + "b" * 64
        + '" }\n'
    )
    root_record = (
        'version = 1\n'
        'revision = 3\n'
        'requires-python = ">=3.10"\n'
        '[[package]]\n'
        'name = "fixture-project"\n'
        'version = "0.1.0"\n'
        'source = { editable = "." }\n'
    )
    (root / "uv.lock").write_text(
        root_record + direct_record + private_url_record + transitive_record,
        encoding="utf-8",
    )


def test_uv_lock_inventory_preserves_hashes_markers_and_direct_ranges_without_urls(tmp_path):
    _write_uv_inventory_fixture(tmp_path)
    manifests = {}
    items = inventory.python_uv_lock_components(
        tmp_path, manifests, "uv.lock", "pyproject.toml"
    )
    assert [(item["name"], item["version"], item["direct"]) for item in items] == [
        ("direct-lib", "2.1.0", True),
        ("private-lib", "1.0.0", True),
        ("transitive-lib", "1.4.2", False),
    ]
    direct = items[0]
    assert direct["scope"] == "optional"
    assert direct["group"] == "uv-lock"
    assert direct["direct_requirements"] == [
        {
            "group": "decisions",
            "requirement": "direct-lib[feature]>=2.0",
            "source_manifest": "pyproject.toml",
            "source_manifest_sha256": manifests["pyproject.toml"]["sha256"],
        }
    ]
    assert direct["resolution_markers"] == ["python_full_version >= '3.10'"]
    assert direct["integrity"] == ["sha256:" + "a" * 64]
    assert items[1]["direct_requirements"] == [
        {
            "group": "decisions",
            "requirement": "private-lib @ <redacted-source>",
            "source_manifest": "pyproject.toml",
            "source_manifest_sha256": manifests["pyproject.toml"]["sha256"],
        }
    ]
    assert items[2]["direct_requirements"] == []
    serialized = json.dumps(items)
    assert all(secret not in serialized for secret in ("https://", "file:///", "user:secret", "token:secret", "C:/private"))
    assert manifests["uv.lock"]["sha256"] == items[0]["source_manifest_sha256"]
    assert "pyproject.toml" in manifests


def test_uv_lock_inventory_rejects_unsupported_sources_and_missing_direct_packages(tmp_path):
    _write_uv_inventory_fixture(tmp_path, use_unsupported_source=True)
    with pytest.raises(inventory.InventoryError, match="unsupported non-registry package source"):
        inventory.python_uv_lock_components(tmp_path, {}, "uv.lock", "pyproject.toml")

    _write_uv_inventory_fixture(tmp_path, include_direct=False)
    with pytest.raises(inventory.InventoryError, match="does not resolve declared direct requirements"):
        inventory.python_uv_lock_components(tmp_path, {}, "uv.lock", "pyproject.toml")


def test_npm_lock_inventory_omits_registry_urls_and_retains_declared_license(tmp_path):
    (tmp_path / "package.json").write_text('{"name":"fixture"}\n', encoding="utf-8")
    lock = {
        "packages": {
            "": {"dependencies": {"leaf": "^1.0.0"}},
            "node_modules/leaf": {
                "version": "1.2.3",
                "license": "MIT",
                "resolved": "https://user:secret@example.invalid/leaf.tgz",
                "integrity": "sha512-example",
            },
        }
    }
    (tmp_path / "package-lock.json").write_text(json.dumps(lock), encoding="utf-8")
    manifests = {}
    items = inventory.npm_components(tmp_path, manifests, "package.json", "package-lock.json")
    assert len(items) == 1
    assert items[0]["name"] == "leaf"
    assert items[0]["direct"] is True
    assert items[0]["license_declared"] == "MIT"
    assert "resolved" not in json.dumps(items)
    assert items[0]["integrity"] == ["sha512-example"]


def test_license_metadata_collection_groups_only_uncollected_manifest_fields():
    digest = "a" * 64
    items = [
        inventory.component(
            ecosystem="golang", name="example.org/go", version="v1.0.0",
            version_constraint=None, scope="runtime-and-build", group="module-graph",
            direct=True, license_declared=None, source_manifest="go.mod",
            source_manifest_sha256=digest,
        ),
        inventory.component(
            ecosystem="pypi", name="python-lock-item", version="1.0.0",
            version_constraint=None, scope="runtime", group="hash-locked",
            direct=None, license_declared=None, source_manifest="requirements.lock",
            source_manifest_sha256=digest,
        ),
        inventory.component(
            ecosystem="npm", name="npm-item", version="1.0.0",
            version_constraint=None, scope="runtime", group="package-lock",
            direct=True, license_declared="MIT", source_manifest="package-lock.json",
            source_manifest_sha256=digest,
        ),
    ]
    result = inventory.license_metadata_collection(items)
    assert result == {
        "status": "source-declarations-only",
        "declared_components": 1,
        "missing_components": 2,
        "missing_by_ecosystem": {"golang": 1, "npm": 0, "pypi": 1},
        "missing_by_source_manifest": [
            {"source_manifest": "go.mod", "ecosystem": "golang", "components": 1},
            {"source_manifest": "requirements.lock", "ecosystem": "pypi", "components": 1},
        ],
        "automatic_license_inference": False,
        "license_review_complete": False,
        "release_clearance": False,
    }


def test_inventory_schema_requires_no_clearance_and_unreviewed_components():
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    jsonschema.Draft202012Validator.check_schema(schema)
    components = [
        inventory.component(
            ecosystem="golang",
            name="example.org/module",
            version="v1.2.3",
            version_constraint=None,
            scope="runtime-and-build",
            group="module-graph",
            direct=True,
            license_declared=None,
            source_manifest="go.mod",
            source_manifest_sha256="a" * 64,
        )
    ]
    report = {
        "schema_version": "threadforge.dependency-inventory.v3",
        "release_clearance": False,
        "coverage": {
            "status": "source-manifest-inventory-only",
            "resolved_python_optional_extras": True,
            "license_review_complete": False,
            "license_metadata_collection": inventory.license_metadata_collection(components),
            "note": "Manifest inventory only; legal review remains open.",
        },
        "counts": {
            "components": 1,
            "manifests": 1,
            "unresolved_versions": 0,
            "missing_declared_license_metadata": 1,
        },
        "manifests": [{"path": "go.mod", "sha256": "a" * 64}],
        "components": components,
    }
    jsonschema.validate(report, schema)
    report["release_clearance"] = True
    with pytest.raises(jsonschema.ValidationError):
        jsonschema.validate(report, schema)


def test_license_audit_snapshot_matches_current_source_manifest_inventory():
    if shutil.which("go") is None:
        pytest.skip("Go is required to derive the module-graph inventory")

    report = inventory.build_inventory(ROOT)
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    jsonschema.validate(report, schema)
    audit = (ROOT / "docs" / "license-audit.md").read_text(encoding="utf-8")
    summary = next(
        (line for line in audit.splitlines() if line.startswith("The fresh `scripts/generate_dependency_inventory.py")),
        None,
    )
    assert summary is not None, "license-audit.md must record the latest generated inventory summary"
    counts = report["counts"]
    assert f"contains {counts['components']} components across {counts['manifests']} manifests" in summary
    assert f"{counts['missing_declared_license_metadata']} `missing_declared_license_metadata`" in summary
    assert f"{counts['unresolved_versions']} `unresolved_versions`" in summary
    collection = report["coverage"]["license_metadata_collection"]
    assert collection["automatic_license_inference"] is False
    assert collection["license_review_complete"] is False
    assert collection["release_clearance"] is False
    assert collection["declared_components"] + collection["missing_components"] == counts["components"]
    assert collection["missing_components"] == counts["missing_declared_license_metadata"]
    assert sum(collection["missing_by_ecosystem"].values()) == collection["missing_components"]
    assert sum(item["components"] for item in collection["missing_by_source_manifest"]) == collection["missing_components"]
    expected_sources = ", ".join(
        f"`{item['source_manifest']}` ({item['ecosystem']}): {item['components']}"
        for item in collection["missing_by_source_manifest"]
    )
    assert f"Uncollected license metadata by source manifest: {expected_sources}." in audit
    python_extras_row = next(
        (line for line in audit.splitlines() if line.startswith("| Python package extras |")),
        None,
    )
    assert python_extras_row is not None, "license-audit.md must document optional Python extras"
    assert f"reports {counts['unresolved_versions']} unresolved component versions" in python_extras_row
    assert "python/uv.lock" in report["manifests"][-1]["path"] or any(
        item["path"] == "python/uv.lock" for item in report["manifests"]
    )
    npm_entries = sum(item["ecosystem"] == "npm" for item in report["components"])
    assert f"All {npm_entries} npm lock entries have a nonempty declared license field" in summary
    assert report["release_clearance"] is False
    assert "release_clearance=false" in summary
