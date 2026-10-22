import json

import pytest

from rrsi import inventory as inventory_module
from rrsi.inventory import InventoryError, load_domain_inventory


def _bundle(domain="rrsi-coding"):
    roles = inventory_module.DOMAIN_SPLIT_SOURCES[domain]
    entries = []
    for source_id in roles.values():
        anchor = inventory_module.RRSI_TRUSTED_INVENTORIES[domain][source_id]
        entries.append({
            "source_id": source_id,
            "source_revision": anchor.get("source_revision", inventory_module.RRSI_SOURCE_REVISION),
            "source_file_sha256": anchor["source_file_sha256"],
            "task_ids": [f"fixture-task-{len(entries):02d}"],
        })
    return {
        "schema_version": "threadforge.rrsi-domain-inventory-bundle.v1",
        "domain": domain,
        "benchmark_inventories": entries,
    }


def _write_bundle(tmp_path, bundle):
    path = tmp_path / "private-inventory.json"
    path.write_text(json.dumps(bundle), encoding="utf-8")
    return path


def test_inventory_loader_routes_only_the_pinned_evolve_and_heldout_sources(tmp_path, monkeypatch):
    bundle = _bundle()
    sources = [entry["source_id"] for entry in bundle["benchmark_inventories"]]
    ids = {task_id.casefold() for row in bundle["benchmark_inventories"] for task_id in row["task_ids"]}
    monkeypatch.setattr(
        inventory_module, "_validate_benchmark_inventories",
        lambda domain, inventories: (ids, sorted(sources), ["unrelated-ood-source"]),
    )

    result = load_domain_inventory(_write_bundle(tmp_path, bundle), "rrsi-coding")

    assert result.domain == "rrsi-coding"
    assert result.evolve_ids == ("fixture-task-00",)
    assert result.heldout_ids == ("fixture-task-01",)
    assert result.ood_ids == ()
    assert result.verified_sources == tuple(sorted(sources))
    assert len(result.source_bundle_sha256) == 64


def test_engineering_v1_inventory_is_ood_not_an_independent_heldout_set(tmp_path, monkeypatch):
    bundle = _bundle("rrsi-engineering-design")
    shared_ids = ["engineering/open-fixture-01", "engineering/open-fixture-02"]
    for entry in bundle["benchmark_inventories"]:
        entry["task_ids"] = shared_ids
    sources = [entry["source_id"] for entry in bundle["benchmark_inventories"]]
    ids = {task_id.casefold() for task_id in shared_ids}
    monkeypatch.setattr(
        inventory_module, "_validate_benchmark_inventories",
        lambda domain, inventories: (ids, sorted(sources), []),
    )

    result = load_domain_inventory(_write_bundle(tmp_path, bundle), "rrsi-engineering-design")

    assert result.evolve_ids == tuple(shared_ids)
    assert result.heldout_ids == ()
    assert result.ood_ids == tuple(shared_ids)


@pytest.mark.parametrize("mutation", ["missing_source", "wrong_domain", "extra_field", "bad_json"])
def test_inventory_loader_rejects_incomplete_or_malformed_bundles(tmp_path, monkeypatch, mutation):
    bundle = _bundle()
    if mutation == "missing_source":
        bundle["benchmark_inventories"].pop()
    elif mutation == "wrong_domain":
        bundle["domain"] = "rrsi-engineering-design"
    elif mutation == "extra_field":
        bundle["unexpected"] = True
    path = _write_bundle(tmp_path, bundle)
    if mutation == "bad_json":
        path.write_text("{broken", encoding="utf-8")
    monkeypatch.setattr(
        inventory_module, "_validate_benchmark_inventories",
        lambda *args: pytest.fail("invalid bundles must fail before trust validation"),
    )

    with pytest.raises(InventoryError):
        load_domain_inventory(path, "rrsi-coding")


def test_inventory_loader_rejects_unpinned_provenance_and_secret_material(tmp_path, monkeypatch):
    bundle = _bundle()
    bundle["benchmark_inventories"][0]["source_file_sha256"] = "0" * 64
    with pytest.raises(InventoryError, match="provenance"):
        load_domain_inventory(_write_bundle(tmp_path, bundle), "rrsi-coding")

    secret = _bundle()
    secret["benchmark_inventories"][0]["source_revision"] = "api_key=must-not-be-returned"
    monkeypatch.setattr(inventory_module, "_contains_credential", lambda _: True)
    with pytest.raises(InventoryError, match="credential-bearing"):
        load_domain_inventory(_write_bundle(tmp_path, secret), "rrsi-coding")


def test_inventory_loader_requires_an_absolute_regular_local_file(tmp_path):
    relative = "relative-inventory.json"
    with pytest.raises(InventoryError, match="absolute local path"):
        load_domain_inventory(relative, "rrsi-coding")
    with pytest.raises(InventoryError, match="absolute local path"):
        load_domain_inventory(42, "rrsi-coding")
    with pytest.raises(InventoryError, match="unknown RRSI domain"):
        load_domain_inventory(tmp_path / "unused.json", [])

    oversized = tmp_path / "oversized.json"
    oversized.write_bytes(b" " * (inventory_module.MAX_BUNDLE_BYTES + 1))
    with pytest.raises(InventoryError, match="bounded regular file"):
        load_domain_inventory(oversized, "rrsi-coding")


def test_inventory_loader_refuses_linked_bundle_paths(tmp_path):
    target = tmp_path / "inventory.json"
    target.write_text("{}", encoding="utf-8")
    link = tmp_path / "inventory-link.json"
    try:
        link.symlink_to(target)
    except OSError:
        pytest.skip("symlink creation is unavailable")

    with pytest.raises(InventoryError, match="linked path components"):
        load_domain_inventory(link, "rrsi-coding")
