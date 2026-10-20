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

"""Load private, source-verified task inventories for local RRSI adapters.

The inventory bundle contains task identifiers only; benchmark rows, tasks,
credentials, and runner code remain outside the Threadforge source tree. Each
source set is checked against the trust anchors used by the v2 leakage screen.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import stat
from dataclasses import dataclass
from pathlib import Path

from threadforge_py.protocol import (
    ProtocolError,
    RRSI_SOURCE_REVISION,
    RRSI_TRUSTED_INVENTORIES,
    _contains_credential,
    _validate_benchmark_inventories,
)

MAX_BUNDLE_BYTES = 384 << 10
DOMAIN_SPLIT_SOURCES = {
    "rrsi-coding": {
        "evolve": "terminal-bench/terminal-bench-2-1",
        "held_out": "swe-bench/swe-bench-verified",
    },
    "rrsi-agentic-workspace": {
        "evolve": "harvey-lab/evolve",
        "held_out": "harvey-lab/held-out",
    },
    "rrsi-engineering-design": {
        "evolve": "engdesign-open",
        "held_out": "engdesign-v1",
    },
}


class InventoryError(ValueError):
    """A local inventory is absent, malformed, untrusted, or incomplete."""


@dataclass(frozen=True)
class DomainInventory:
    domain: str
    evolve_ids: tuple[str, ...]
    heldout_ids: tuple[str, ...]
    verified_sources: tuple[str, ...]
    source_bundle_sha256: str


def _path_stat(path: Path) -> os.stat_result:
    try:
        info = path.lstat()
    except (OSError, ValueError):
        raise InventoryError("inventory bundle is unavailable") from None
    reparse_point = bool(getattr(info, "st_file_attributes", 0) & 0x400)
    if stat.S_ISLNK(info.st_mode) or reparse_point:
        raise InventoryError("inventory bundle cannot contain linked path components")
    return info


def _read_local_bundle(path_value: str | os.PathLike[str]) -> bytes:
    try:
        path = Path(path_value).expanduser()
    except (TypeError, ValueError):
        raise InventoryError("inventory bundle path must be an absolute local path") from None
    if not path.is_absolute() or (path.drive.startswith("\\\\") if path.drive else False):
        raise InventoryError("inventory bundle path must be an absolute local path")
    if any(part in {".", ".."} for part in path.parts):
        raise InventoryError("inventory bundle path cannot contain traversal components")

    current = Path(path.anchor)
    components = path.parts[1:] if path.anchor else path.parts
    for index, component in enumerate(components):
        current = current / component
        info = _path_stat(current)
        final = index == len(components) - 1
        if final:
            if not stat.S_ISREG(info.st_mode) or info.st_size > MAX_BUNDLE_BYTES:
                raise InventoryError("inventory bundle must be a bounded regular file")
        elif not stat.S_ISDIR(info.st_mode):
            raise InventoryError("inventory bundle path contains a non-directory component")

    before = _path_stat(current)
    flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(current, flags)
        with os.fdopen(descriptor, "rb") as stream:
            opened = os.fstat(stream.fileno())
            identity = (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns)
            if (not stat.S_ISREG(opened.st_mode) or
                    (opened.st_dev, opened.st_ino, opened.st_size, opened.st_mtime_ns) != identity):
                raise InventoryError("inventory bundle changed while opening")
            raw = stream.read(MAX_BUNDLE_BYTES + 1)
            after = os.fstat(stream.fileno())
        path_after = _path_stat(current)
    except InventoryError:
        raise
    except OSError:
        raise InventoryError("inventory bundle could not be read consistently") from None
    after_identity = (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns)
    path_identity = (path_after.st_dev, path_after.st_ino, path_after.st_size, path_after.st_mtime_ns)
    if (len(raw) != before.st_size or len(raw) > MAX_BUNDLE_BYTES or
            after_identity != identity or path_identity != identity):
        raise InventoryError("inventory bundle changed while reading")
    return raw


def _expected_revision_and_hash(domain: str, source_id: str) -> tuple[str, str]:
    anchor = RRSI_TRUSTED_INVENTORIES.get(domain, {}).get(source_id)
    if anchor is None:
        raise InventoryError("required inventory source has no committed trust anchor")
    return anchor.get("source_revision", RRSI_SOURCE_REVISION), anchor["source_file_sha256"]


def load_domain_inventory(path: str | os.PathLike[str], domain: str) -> DomainInventory:
    """Load a private bundle and return only its validated evolve/held-out IDs."""
    if not isinstance(domain, str):
        raise InventoryError("unknown RRSI domain")
    required_roles = DOMAIN_SPLIT_SOURCES.get(domain)
    if required_roles is None:
        raise InventoryError("unknown RRSI domain")

    raw = _read_local_bundle(path)
    try:
        bundle = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError, RecursionError):
        raise InventoryError("inventory bundle is not valid UTF-8 JSON") from None
    if not isinstance(bundle, dict) or set(bundle) != {
        "schema_version", "domain", "benchmark_inventories",
    }:
        raise InventoryError("inventory bundle has an invalid shape")
    if (bundle["schema_version"] != "threadforge.rrsi-domain-inventory-bundle.v1" or
            bundle["domain"] != domain):
        raise InventoryError("inventory bundle version or domain does not match")
    inventories = bundle["benchmark_inventories"]
    expected_sources = set(required_roles.values())
    if not isinstance(inventories, list) or len(inventories) != len(expected_sources):
        raise InventoryError("inventory bundle does not contain the required anchored splits")
    if any(not isinstance(entry, dict) or set(entry) != {
        "source_id", "source_revision", "source_file_sha256", "task_ids",
    } for entry in inventories):
        raise InventoryError("inventory bundle contains a malformed source entry")
    supplied_sources = [entry["source_id"] for entry in inventories]
    if (any(not isinstance(source_id, str) for source_id in supplied_sources) or
            set(supplied_sources) != expected_sources or
            len(set(supplied_sources)) != len(supplied_sources)):
        raise InventoryError("inventory bundle does not contain the required anchored splits")
    if _contains_credential(bundle):
        raise InventoryError("credential-bearing inventory bundle rejected")

    # Refuse source claims that differ from the statically reviewed trust
    # anchors before the complete task-set digest check.
    for entry in inventories:
        source_id = entry.get("source_id")
        expected_revision, expected_hash = _expected_revision_and_hash(domain, source_id)
        if (entry.get("source_revision") != expected_revision or
                entry.get("source_file_sha256") != expected_hash):
            raise InventoryError("inventory provenance does not match its pinned source")
        if (not isinstance(entry.get("task_ids"), list) or
                any(not isinstance(task_id, str) for task_id in entry["task_ids"])):
            raise InventoryError("inventory task list is malformed")

    try:
        verified_ids, verified_sources, _missing_sources = _validate_benchmark_inventories(
            domain, inventories,
        )
    except (ProtocolError, TypeError, AttributeError):
        raise InventoryError("inventory task set does not match its pinned commitment") from None
    if set(verified_sources) != expected_sources:
        raise InventoryError("inventory source validation was incomplete")
    declared_ids = {
        task_id.casefold()
        for entry in inventories
        for task_id in entry["task_ids"]
    }
    if declared_ids != verified_ids:
        raise InventoryError("inventory task set did not reconcile with trust validation")

    by_source = {entry["source_id"]: tuple(entry["task_ids"]) for entry in inventories}
    evolve_ids = by_source[required_roles["evolve"]]
    heldout_ids = by_source[required_roles["held_out"]]
    # The pinned EngDesign evolve and v1 surfaces intentionally share IDs;
    # duplicates across roles are valid and are kept distinct by split.
    if any(not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:/-]{2,127}", task_id)
           for task_id in (*evolve_ids, *heldout_ids)):
        raise InventoryError("inventory contains an invalid task identifier")

    return DomainInventory(
        domain=domain,
        evolve_ids=evolve_ids,
        heldout_ids=heldout_ids,
        verified_sources=tuple(sorted(verified_sources)),
        source_bundle_sha256=hashlib.sha256(raw).hexdigest(),
    )
