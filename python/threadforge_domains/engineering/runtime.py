# Copyright 2026 Threadforge contributors.
# Licensed under the Apache License, Version 2.0.

"""Strict local identity checks for the external EngDesign runtime.

The benchmark and evaluator are intentionally not distributed with Threadforge.
Before an Engineering run may use them, an operator must provide an out-of-tree
runtime pin that binds their exact bytes, the source revision, the verified
task-ID set, and a preloaded immutable execution image. This module verifies
that contract; it does not make benchmark results release evidence.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import stat
import subprocess
import tempfile
from dataclasses import dataclass
from pathlib import Path

from threadforge_py.protocol import RRSI_SOURCE_REVISION
from threadforge_domains.workspace.security import valid_pinned_image

PIN_SCHEMA = "threadforge.engineering-runtime-pin.v1"
PIN_ENV = "THREADFORGE_ENGINEERING_RUNTIME_PIN"
BENCHMARK_ENV = "THREADFORGE_ENGINEERING_BENCHMARK_ROOT"
EVALUATOR_ENV = "THREADFORGE_ENGINEERING_EVALUATOR_ROOT"
IMAGE_ENV = "THREADFORGE_ENGINEERING_EXECUTION_IMAGE"
MAX_PIN_BYTES = 64 << 10
MAX_MANIFEST_BYTES = 4 << 20
MAX_TREE_FILES = 100_000
MAX_TREE_ENTRIES = 150_000
MAX_TREE_BYTES = 64 << 30
MAX_TREE_FILE_BYTES = 8 << 30
MAX_PATH_BYTES = 4096
MAX_GIT_STATUS_BYTES = 8 << 20
MAX_AGENT_WORKSPACE_BYTES = 256 << 20
MAX_AGENT_FILES = 2000
MAX_AGENT_FILE_BYTES = 32 << 20
MAX_CANDIDATE_HARNESS_ENTRIES = 5000
MAX_EVALUATION_WORKSPACE_BYTES = 1 << 30
MAX_EVALUATION_FILES = 5000
MAX_EVALUATION_FILE_BYTES = 256 << 20
CANONICAL_EVALUATOR_SCRIPT = "domains/eng/scripts/engdesign/evaluate_submission.py"
EVALUATION_OUTPUT_DIRECTORY = ".threadforge-eval-output"
_SHA256 = re.compile(r"[0-9a-f]{64}\Z")
_SOURCE_REVISION = re.compile(r"[0-9a-f]{40,64}\Z")
_REVIEW_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:/#-]{0,127}\Z")
_LICENSE_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9.+() -]{0,127}\Z")
_REPARSE_POINT = 0x400
_SEED_PAYLOAD_MARKER = b"TODO: solve the task described in LLM_prompt.txt"


class RuntimePinError(ValueError):
    """A local runtime is absent, inconsistent, or not independently pinned."""

    def __init__(self, reason_code: str):
        self.reason_code = reason_code
        super().__init__(reason_code)


@dataclass(frozen=True)
class EngineeringRuntime:
    benchmark_root: Path
    evaluator_root: Path
    tasks: dict[str, Path]
    image: str
    pin_sha256: str
    benchmark_tree_sha256: str
    evaluator_tree_sha256: str
    benchmark_files: dict[str, tuple[int, str]]
    evaluator_files: dict[str, tuple[int, str]]
    evaluator_source_revision: str
    license_review_id: str


@dataclass(frozen=True)
class PreparedEngineeringTask:
    task_id: str
    workspace: Path
    prompt_path: Path
    schema_path: Path
    candidate_path: Path
    candidate_seed_sha256: str
    workspace_sha256: str
    file_count: int
    total_bytes: int
    candidate_harness_sha256: str | None = None
    candidate_harness_file_count: int = 0
    candidate_harness_bytes: int = 0


@dataclass(frozen=True)
class PreparedEngineeringEvaluation:
    workspace: Path
    candidate_path: Path
    evaluator_path: Path
    metrics_path: Path
    artifacts_path: Path
    writable_subdirectory: str
    workspace_sha256: str
    file_count: int
    total_bytes: int


def canonical_task_ids_sha256(task_ids: list[str] | tuple[str, ...]) -> str:
    """Match the canonical task-set digest used by the inventory verifier."""
    canonical = "".join(f"{task_id}\n" for task_id in sorted(task_ids, key=str.casefold))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _is_link_or_reparse(info: os.stat_result) -> bool:
    return stat.S_ISLNK(info.st_mode) or bool(
        getattr(info, "st_file_attributes", 0) & _REPARSE_POINT
    )


def _check_components(path: Path) -> Path:
    """Return an absolute path after refusing symlink/reparse components."""
    if not path.is_absolute() or (path.drive.startswith("\\\\") if path.drive else False):
        raise RuntimePinError("runtime_paths_must_be_absolute_local_paths")
    current = Path(path.anchor)
    for component in path.parts[1:]:
        current /= component
        try:
            info = current.lstat()
        except OSError:
            raise RuntimePinError("runtime_path_component_unavailable") from None
        if _is_link_or_reparse(info):
            raise RuntimePinError("runtime_path_contains_link_or_reparse_point")
    return path.resolve(strict=True)


def _directory(value: str | os.PathLike[str]) -> Path:
    try:
        raw = Path(value).expanduser()
        resolved = _check_components(raw)
        if not resolved.is_dir():
            raise RuntimePinError("runtime_root_is_not_a_directory")
        return resolved
    except RuntimePinError:
        raise
    except (OSError, TypeError, ValueError):
        raise RuntimePinError("runtime_root_is_unavailable") from None


def _overlaps(left: Path, right: Path) -> bool:
    """Whether either path contains the other (after caller normalization)."""
    return left == right or left.is_relative_to(right) or right.is_relative_to(left)


def _validate_runtime_roots(
    benchmark: Path,
    evaluator: Path,
    protected: list[Path],
) -> None:
    """Keep benchmark/evaluator trees separate from each other and all protected roots.

    Protected roots may overlap one another: RRSI commonly places candidate
    worktrees below its runs directory. They are never mounted into the task
    container; only their separation from benchmark/evaluator data matters.
    """
    if _overlaps(benchmark, evaluator) or any(
        _overlaps(root, protected_root)
        for root in (benchmark, evaluator)
        for protected_root in protected
    ):
        raise RuntimePinError("runtime_roots_must_be_disjoint")


def _strict_json(raw: bytes, reason: str) -> object:
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError("duplicate JSON key")
            result[key] = value
        return result

    try:
        return json.loads(
            raw.decode("utf-8"),
            object_pairs_hook=pairs,
            parse_constant=lambda _value: (_ for _ in ()).throw(ValueError("non-finite JSON")),
        )
    except (UnicodeDecodeError, json.JSONDecodeError, ValueError, RecursionError):
        raise RuntimePinError(reason) from None


def _read_stable_file(path: Path, limit: int, reason: str) -> bytes:
    try:
        absolute = Path(os.path.abspath(path))
        _check_components(absolute)
        before = absolute.lstat()
        if (_is_link_or_reparse(before) or not stat.S_ISREG(before.st_mode) or
                before.st_size > limit):
            raise RuntimePinError(reason)
        identity = (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns)
        descriptor = os.open(
            absolute,
            os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0),
        )
        with os.fdopen(descriptor, "rb") as stream:
            opened = os.fstat(stream.fileno())
            if (not stat.S_ISREG(opened.st_mode) or
                    (opened.st_dev, opened.st_ino, opened.st_size, opened.st_mtime_ns) != identity):
                raise RuntimePinError(reason)
            raw = stream.read(limit + 1)
            after = os.fstat(stream.fileno())
        path_after = absolute.lstat()
        if (len(raw) != before.st_size or len(raw) > limit or
                (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns) != identity or
                (path_after.st_dev, path_after.st_ino, path_after.st_size,
                 path_after.st_mtime_ns) != identity or _is_link_or_reparse(path_after)):
            raise RuntimePinError(reason)
        return raw
    except RuntimePinError:
        raise
    except (OSError, ValueError, TypeError):
        raise RuntimePinError(reason) from None


def _relative(value: str) -> tuple[str, ...]:
    if (not isinstance(value, str) or not value or "\\" in value or
            value.startswith("/") or "\x00" in value):
        raise RuntimePinError("benchmark_manifest_path_invalid")
    parts = tuple(value.split("/"))
    if any(part in {"", ".", ".."} or ":" in part for part in parts):
        raise RuntimePinError("benchmark_manifest_path_invalid")
    if len(value.encode("utf-8")) > MAX_PATH_BYTES:
        raise RuntimePinError("benchmark_manifest_path_invalid")
    return parts


def _inside(root: Path, relative: str) -> Path:
    parts = _relative(relative)
    target = root.joinpath(*parts)
    current = root
    for part in parts:
        current = current / part
        try:
            info = current.lstat()
        except OSError:
            raise RuntimePinError("benchmark_manifest_file_unavailable") from None
        if _is_link_or_reparse(info):
            raise RuntimePinError("benchmark_manifest_contains_link_or_reparse_point")
    try:
        target.resolve(strict=True).relative_to(root)
    except (OSError, ValueError):
        raise RuntimePinError("benchmark_manifest_path_escapes_root") from None
    return target


def _tree_fingerprint(root_value: str | os.PathLike[str]) -> tuple[str, dict[str, tuple[int, str]]]:
    """Hash a stable, bounded, link-free tree; `.git` metadata is excluded."""
    root = _directory(root_value)
    entries: list[tuple[str, int, str]] = []
    seen_casefold: set[str] = set()
    entry_count = 0
    total_bytes = 0
    stack = [root]
    while stack:
        directory = stack.pop()
        try:
            children = list(os.scandir(directory))
        except OSError:
            raise RuntimePinError("runtime_tree_could_not_be_read") from None
        for child in children:
            if directory == root and child.name == ".git":
                continue
            try:
                # os.lstat is required on Windows: DirEntry.stat may report
                # st_nlink=0 even for ordinary files, hiding hard-link state.
                info = os.lstat(child.path)
            except OSError:
                raise RuntimePinError("runtime_tree_changed_during_read") from None
            if _is_link_or_reparse(info):
                raise RuntimePinError("runtime_tree_contains_link_or_reparse_point")
            path = Path(child.path)
            relative = path.relative_to(root).as_posix()
            entry_count += 1
            if entry_count > MAX_TREE_ENTRIES:
                raise RuntimePinError("runtime_tree_entry_limit_exceeded")
            folded = relative.casefold()
            if folded in seen_casefold:
                raise RuntimePinError("runtime_tree_casefold_path_collision")
            seen_casefold.add(folded)
            if stat.S_ISDIR(info.st_mode):
                stack.append(path)
                continue
            if (not stat.S_ISREG(info.st_mode) or info.st_size > MAX_TREE_FILE_BYTES or
                    getattr(info, "st_nlink", 1) != 1):
                raise RuntimePinError("runtime_tree_contains_unsupported_file")
            try:
                encoded_path = relative.encode("utf-8", errors="strict")
            except UnicodeEncodeError:
                raise RuntimePinError("runtime_tree_path_invalid") from None
            if (len(encoded_path) > MAX_PATH_BYTES or
                    any(ord(ch) < 32 or ord(ch) == 127 for ch in relative)):
                raise RuntimePinError("runtime_tree_path_invalid")
            if len(entries) >= MAX_TREE_FILES:
                raise RuntimePinError("runtime_tree_file_limit_exceeded")
            total_bytes += info.st_size
            if total_bytes > MAX_TREE_BYTES:
                raise RuntimePinError("runtime_tree_byte_limit_exceeded")
            file_sha = hashlib.sha256()
            identity = (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns)
            try:
                descriptor = os.open(
                    path,
                    os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0),
                )
                with os.fdopen(descriptor, "rb") as stream:
                    opened = os.fstat(stream.fileno())
                    if (not stat.S_ISREG(opened.st_mode) or
                            (opened.st_dev, opened.st_ino, opened.st_size,
                             opened.st_mtime_ns) != identity):
                        raise RuntimePinError("runtime_tree_changed_during_read")
                    while block := stream.read(1 << 20):
                        file_sha.update(block)
                    after = os.fstat(stream.fileno())
                current = path.lstat()
            except RuntimePinError:
                raise
            except OSError:
                raise RuntimePinError("runtime_tree_could_not_be_read") from None
            if ((after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns) != identity or
                    (current.st_dev, current.st_ino, current.st_size,
                     current.st_mtime_ns) != identity or _is_link_or_reparse(current)):
                raise RuntimePinError("runtime_tree_changed_during_read")
            entries.append((relative, info.st_size, file_sha.hexdigest()))

    digest = hashlib.sha256()
    for relative, size, file_sha in sorted(entries, key=lambda row: (row[0].casefold(), row[0])):
        digest.update(f"{relative}\t{size}\t{file_sha}\n".encode("utf-8"))
    manifest = {relative: (size, file_sha) for relative, size, file_sha in entries}
    return digest.hexdigest(), manifest


def tree_sha256(root_value: str | os.PathLike[str]) -> str:
    """Return the content digest of a stable external runtime tree."""
    return _tree_fingerprint(root_value)[0]


def _git_revision(root: Path) -> str:
    env = {key: value for key, value in os.environ.items()
           if key.upper() in {"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TEMP", "TMP"}}
    env.update({
        "GIT_OPTIONAL_LOCKS": "0",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": os.devnull,
    })
    try:
        top = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "--show-toplevel"],
            capture_output=True, text=True, timeout=15, check=False, env=env,
        )
        head = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "HEAD"],
            capture_output=True, text=True, timeout=15, check=False, env=env,
        )
        status = subprocess.run(
            ["git", "-C", str(root), "status", "--porcelain=v1",
             "--ignored=matching", "--untracked-files=all"],
            capture_output=True, text=True, timeout=30, check=False, env=env,
        )
    except (OSError, subprocess.TimeoutExpired):
        raise RuntimePinError("evaluator_git_identity_unavailable") from None
    if (top.returncode or head.returncode or status.returncode or
            len(status.stdout.encode("utf-8", errors="replace")) > MAX_GIT_STATUS_BYTES):
        raise RuntimePinError("evaluator_git_identity_unavailable")
    try:
        git_root = Path(top.stdout.strip()).resolve(strict=True)
    except (OSError, RuntimeError, ValueError):
        raise RuntimePinError("evaluator_git_identity_unavailable") from None
    if git_root != root or status.stdout.strip():
        raise RuntimePinError("evaluator_checkout_not_clean")
    revision = head.stdout.strip().lower()
    if not _SOURCE_REVISION.fullmatch(revision):
        raise RuntimePinError("evaluator_revision_invalid")
    return revision


def _validate_pin(value: object) -> dict:
    if not isinstance(value, dict):
        raise RuntimePinError("runtime_pin_invalid")
    required = {
        "schema_version", "rrsi_source_revision", "evaluator_source_revision",
        "benchmark_tree_sha256", "evaluator_tree_sha256", "task_count",
        "task_ids_sha256", "execution_image", "benchmark_license",
        "evaluator_license", "license_review_id",
    }
    if set(value) != required:
        raise RuntimePinError("runtime_pin_invalid")
    if (value["schema_version"] != PIN_SCHEMA or
            value["rrsi_source_revision"] != RRSI_SOURCE_REVISION or
            not isinstance(value["evaluator_source_revision"], str) or
            not _SOURCE_REVISION.fullmatch(value["evaluator_source_revision"]) or
            any(not isinstance(value[key], str) or not _SHA256.fullmatch(value[key])
                for key in ("benchmark_tree_sha256", "evaluator_tree_sha256", "task_ids_sha256")) or
            isinstance(value["task_count"], bool) or
            not isinstance(value["task_count"], int) or not 1 <= value["task_count"] <= 2048 or
            not valid_pinned_image(value["execution_image"]) or
            not all(isinstance(value[key], str) and _LICENSE_ID.fullmatch(value[key])
                    for key in ("benchmark_license", "evaluator_license")) or
            not isinstance(value["license_review_id"], str) or
            not _REVIEW_ID.fullmatch(value["license_review_id"])):
        raise RuntimePinError("runtime_pin_invalid")
    return value


def _read_pin(path_value: str | os.PathLike[str]) -> tuple[dict, str]:
    try:
        path = Path(path_value).expanduser()
        resolved = _check_components(path)
        raw = _read_stable_file(resolved, MAX_PIN_BYTES, "runtime_pin_invalid")
    except RuntimePinError:
        raise
    except (OSError, TypeError, ValueError):
        raise RuntimePinError("runtime_pin_unavailable") from None
    value = _validate_pin(_strict_json(raw, "runtime_pin_invalid"))
    return value, hashlib.sha256(raw).hexdigest()


def _validate_task_manifest(benchmark_root: Path, task_ids: list[str]) -> dict[str, Path]:
    manifest_path = benchmark_root / "engdesign_manifest.json"
    manifest = _strict_json(
        _read_stable_file(manifest_path, MAX_MANIFEST_BYTES, "benchmark_manifest_invalid"),
        "benchmark_manifest_invalid",
    )
    if (not isinstance(manifest, dict) or set(manifest) != {"env_name", "tasks", "skipped"} or
            not isinstance(manifest.get("env_name"), str) or
            not isinstance(manifest.get("tasks"), list) or
            not isinstance(manifest.get("skipped"), list)):
        raise RuntimePinError("benchmark_manifest_invalid")
    wanted = set(task_ids)
    found: dict[str, Path] = {}
    for record in manifest["tasks"]:
        if not isinstance(record, dict) or set(record) != {
            "task_id", "domain", "rel", "withheld", "agent_files",
        }:
            raise RuntimePinError("benchmark_manifest_invalid")
        task_id, rel, files = record["task_id"], record["rel"], record["agent_files"]
        if (not isinstance(task_id, str) or task_id not in wanted or task_id in found or
                not isinstance(record["domain"], str) or not isinstance(record["withheld"], list) or
                any(not isinstance(item, str) for item in record["withheld"]) or
                not isinstance(files, list) or any(not isinstance(item, str) for item in files)):
            raise RuntimePinError("benchmark_manifest_task_set_mismatch")
        rel_parts = _relative(rel)
        if rel_parts[-1] != task_id:
            raise RuntimePinError("benchmark_manifest_task_path_mismatch")
        task_root = _inside(benchmark_root / "benchmarks", "/".join(rel_parts))
        if not task_root.is_dir():
            raise RuntimePinError("benchmark_task_unavailable")
        required_visible = {"LLM_prompt.txt", "output_structure.py", "submission/payload.py"}
        allowed_visible = required_visible | {"images"}
        if (len(files) != len(set(files)) or not required_visible <= set(files) or
                not set(files) <= allowed_visible):
            raise RuntimePinError("benchmark_agent_visibility_invalid")
        copy_list_path = _inside(task_root, "frontier_eval/copy_files.txt")
        copy_list_raw = _read_stable_file(copy_list_path, 64 << 10,
                                          "benchmark_agent_visibility_invalid")
        try:
            copy_list = [line for line in copy_list_raw.decode("utf-8").splitlines() if line]
        except UnicodeDecodeError:
            raise RuntimePinError("benchmark_agent_visibility_invalid") from None
        if copy_list != files:
            raise RuntimePinError("benchmark_agent_visibility_mismatch")
        for visible in files:
            path = _inside(task_root, visible)
            info = path.lstat()
            if visible == "images":
                if not stat.S_ISDIR(info.st_mode):
                    raise RuntimePinError("benchmark_agent_visibility_mismatch")
            elif not stat.S_ISREG(info.st_mode):
                raise RuntimePinError("benchmark_agent_visibility_mismatch")
        for required in (
            "frontier_eval/candidate_destination.txt",
            "frontier_eval/run_eval.sh",
            "frontier_eval/evaluate_submission.py",
            "evaluate.py",
        ):
            required_path = _inside(task_root, required)
            if not stat.S_ISREG(required_path.lstat().st_mode):
                raise RuntimePinError("benchmark_evaluator_asset_invalid")
        try:
            candidate_path = _read_stable_file(
                task_root / "frontier_eval/candidate_destination.txt", 4096,
                "benchmark_candidate_path_invalid",
            ).decode("utf-8").strip()
        except UnicodeDecodeError:
            raise RuntimePinError("benchmark_candidate_path_invalid") from None
        if candidate_path != "submission/payload.py":
            raise RuntimePinError("benchmark_candidate_path_invalid")
        found[task_id] = task_root
    if set(found) != wanted or canonical_task_ids_sha256(list(found)) != canonical_task_ids_sha256(task_ids):
        raise RuntimePinError("benchmark_manifest_task_set_mismatch")
    return found


def load_engineering_runtime(
    *,
    benchmark_root: str | os.PathLike[str],
    evaluator_root: str | os.PathLike[str],
    pin_path: str | os.PathLike[str],
    task_ids: list[str],
    image: str,
    protected_roots: tuple[str | os.PathLike[str], ...] = (),
) -> EngineeringRuntime:
    """Verify external EngDesign assets against a private operator-reviewed pin."""
    if (not isinstance(task_ids, list) or not task_ids or
            any(not isinstance(task_id, str) or not task_id for task_id in task_ids) or
            len({task_id.casefold() for task_id in task_ids}) != len(task_ids)):
        raise RuntimePinError("verified_task_set_invalid")
    benchmark = _directory(benchmark_root)
    evaluator = _directory(evaluator_root)
    pin = _check_components(Path(pin_path).expanduser())
    protected = []
    for value in protected_roots:
        try:
            p = Path(value).expanduser().resolve(strict=False)
        except (OSError, RuntimeError, TypeError, ValueError):
            raise RuntimePinError("protected_path_invalid") from None
        protected.append(p)
    for protected_path in [*protected, benchmark, evaluator]:
        if pin == protected_path or pin.is_relative_to(protected_path):
            raise RuntimePinError("runtime_pin_must_be_outside_runtime_and_candidate_roots")
    _validate_runtime_roots(benchmark, evaluator, protected)
    if not valid_pinned_image(image):
        raise RuntimePinError("execution_image_must_be_digest_pinned")

    pin_data, pin_sha256 = _read_pin(pin)
    if pin_data["execution_image"] != image:
        raise RuntimePinError("execution_image_pin_mismatch")
    task_ids_sha256 = canonical_task_ids_sha256(task_ids)
    if (pin_data["task_count"] != len(task_ids) or
            pin_data["task_ids_sha256"] != task_ids_sha256):
        raise RuntimePinError("runtime_pin_task_inventory_mismatch")
    actual_revision = _git_revision(evaluator)
    if actual_revision != pin_data["evaluator_source_revision"]:
        raise RuntimePinError("evaluator_revision_pin_mismatch")
    benchmark_sha256, benchmark_files = _tree_fingerprint(benchmark)
    evaluator_sha256, evaluator_files = _tree_fingerprint(evaluator)
    if (benchmark_sha256 != pin_data["benchmark_tree_sha256"] or
            evaluator_sha256 != pin_data["evaluator_tree_sha256"]):
        raise RuntimePinError("runtime_tree_pin_mismatch")
    if CANONICAL_EVALUATOR_SCRIPT not in evaluator_files:
        raise RuntimePinError("canonical_evaluator_not_in_trusted_revision")
    tasks = _validate_task_manifest(benchmark, task_ids)
    if _git_revision(evaluator) != actual_revision:
        raise RuntimePinError("evaluator_changed_during_verification")
    if (tree_sha256(benchmark) != benchmark_sha256 or
            tree_sha256(evaluator) != evaluator_sha256):
        raise RuntimePinError("runtime_changed_during_verification")
    return EngineeringRuntime(
        benchmark_root=benchmark,
        evaluator_root=evaluator,
        tasks=tasks,
        image=image,
        pin_sha256=pin_sha256,
        benchmark_tree_sha256=benchmark_sha256,
        evaluator_tree_sha256=evaluator_sha256,
        benchmark_files=benchmark_files,
        evaluator_files=evaluator_files,
        evaluator_source_revision=actual_revision,
        license_review_id=pin_data["license_review_id"],
    )


def _pinned_benchmark_file(runtime: EngineeringRuntime, path: Path,
                           limit: int = MAX_AGENT_FILE_BYTES) -> bytes:
    try:
        relative = path.relative_to(runtime.benchmark_root).as_posix()
    except ValueError:
        raise RuntimePinError("benchmark_manifest_path_escapes_root") from None
    expected = runtime.benchmark_files.get(relative)
    if expected is None or expected[0] > limit:
        raise RuntimePinError("benchmark_file_not_in_verified_pin")
    try:
        info = path.lstat()
    except OSError:
        raise RuntimePinError("benchmark_file_changed_after_pin") from None
    if (_is_link_or_reparse(info) or not stat.S_ISREG(info.st_mode) or
            getattr(info, "st_nlink", 1) != 1 or info.st_size != expected[0]):
        raise RuntimePinError("benchmark_file_changed_after_pin")
    raw = _read_stable_file(path, limit, "benchmark_file_changed_after_pin")
    if hashlib.sha256(raw).hexdigest() != expected[1]:
        raise RuntimePinError("benchmark_file_changed_after_pin")
    return raw


def _pinned_evaluator_file(runtime: EngineeringRuntime, relative: str,
                           limit: int = MAX_EVALUATION_FILE_BYTES) -> bytes:
    if not isinstance(relative, str) or "\\" in relative or relative.startswith("/"):
        raise RuntimePinError("evaluator_manifest_path_invalid")
    try:
        relative = Path(relative).as_posix()
        path = runtime.evaluator_root.joinpath(*_relative(relative))
        path.relative_to(runtime.evaluator_root)
    except (OSError, RuntimeError, TypeError, ValueError):
        raise RuntimePinError("evaluator_manifest_path_invalid") from None
    expected = runtime.evaluator_files.get(relative)
    if expected is None or expected[0] > limit:
        raise RuntimePinError("evaluator_file_not_in_verified_pin")
    try:
        info = path.lstat()
    except OSError:
        raise RuntimePinError("evaluator_file_changed_after_pin") from None
    if (_is_link_or_reparse(info) or not stat.S_ISREG(info.st_mode) or
            getattr(info, "st_nlink", 1) != 1 or info.st_size != expected[0]):
        raise RuntimePinError("evaluator_file_changed_after_pin")
    raw = _read_stable_file(path, limit, "evaluator_file_changed_after_pin")
    if hashlib.sha256(raw).hexdigest() != expected[1]:
        raise RuntimePinError("evaluator_file_changed_after_pin")
    return raw


def _mkdir_new_or_existing_directory(path: Path) -> None:
    try:
        path.mkdir(mode=0o700)
    except FileExistsError:
        pass
    except OSError:
        raise RuntimePinError("agent_workspace_could_not_be_created") from None
    try:
        info = path.lstat()
    except OSError:
        raise RuntimePinError("agent_workspace_changed_during_creation") from None
    if _is_link_or_reparse(info) or not stat.S_ISDIR(info.st_mode):
        raise RuntimePinError("agent_workspace_path_invalid")
    if os.name == "posix":
        try:
            private = info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) & 0o077 == 0
        except (AttributeError, OSError):
            private = False
        if not private:
            raise RuntimePinError("agent_workspace_parent_not_private")


def _write_new_agent_file(path: Path, raw: bytes) -> None:
    try:
        descriptor = os.open(
            path,
            os.O_WRONLY | os.O_CREAT | os.O_EXCL |
            getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0),
            0o600,
        )
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
    except FileExistsError:
        raise RuntimePinError("agent_workspace_file_conflict") from None
    except OSError:
        raise RuntimePinError("agent_workspace_write_failed") from None


def _candidate_harness_manifest(source_root: Path) -> tuple[list[tuple[str, int, str]], int]:
    """Fingerprint candidate Python modules without following mutable links."""
    entries: list[tuple[str, int, str]] = []
    seen_casefold: set[str] = set()
    total_bytes = 0
    entry_count = 0
    stack = [source_root]
    while stack:
        directory = stack.pop()
        try:
            with os.scandir(directory) as iterator:
                children = []
                for child in iterator:
                    entry_count += 1
                    if entry_count > MAX_CANDIDATE_HARNESS_ENTRIES:
                        raise RuntimePinError("candidate_harness_entry_limit_exceeded")
                    children.append(child)
            children.sort(key=lambda item: (item.name.casefold(), item.name))
        except RuntimePinError:
            raise
        except OSError:
            raise RuntimePinError("candidate_harness_source_unavailable") from None
        for child in children:
            try:
                info = os.lstat(child.path)
            except OSError:
                raise RuntimePinError("candidate_harness_source_changed") from None
            if _is_link_or_reparse(info):
                raise RuntimePinError("candidate_harness_source_contains_link")
            path = Path(child.path)
            relative = path.relative_to(source_root).as_posix()
            _relative(relative)
            if stat.S_ISDIR(info.st_mode):
                if child.name != "__pycache__":
                    stack.append(path)
                continue
            if (not stat.S_ISREG(info.st_mode) or getattr(info, "st_nlink", 1) != 1):
                raise RuntimePinError("candidate_harness_source_file_invalid")
            if path.suffix.casefold() == ".pyc" or path.name == "README.md":
                continue
            if path.suffix.casefold() != ".py":
                raise RuntimePinError("candidate_harness_source_file_type_unsupported")
            folded = relative.casefold()
            if folded in seen_casefold:
                raise RuntimePinError("candidate_harness_source_casefold_collision")
            seen_casefold.add(folded)
            if len(entries) >= MAX_AGENT_FILES:
                raise RuntimePinError("candidate_harness_file_limit_exceeded")
            if info.st_size > MAX_AGENT_FILE_BYTES:
                raise RuntimePinError("candidate_harness_file_too_large")
            raw = _read_stable_file(path, MAX_AGENT_FILE_BYTES,
                                    "candidate_harness_source_changed")
            if len(raw) != info.st_size or getattr(path.lstat(), "st_nlink", 1) != 1:
                raise RuntimePinError("candidate_harness_source_changed")
            total_bytes += len(raw)
            if total_bytes > MAX_AGENT_WORKSPACE_BYTES:
                raise RuntimePinError("candidate_harness_size_limit_exceeded")
            entries.append((relative, len(raw), hashlib.sha256(raw).hexdigest()))
    if not any(relative == "main.py" for relative, _size, _sha256 in entries):
        raise RuntimePinError("candidate_harness_entrypoint_missing")
    entries.sort(key=lambda row: (row[0].casefold(), row[0]))
    return entries, total_bytes


def _candidate_harness_sha256(entries: list[tuple[str, int, str]]) -> str:
    digest = hashlib.sha256()
    for relative, size, file_sha in entries:
        digest.update(f"{relative}\t{size}\t{file_sha}\n".encode("utf-8"))
    return digest.hexdigest()


def fingerprint_engineering_candidate_harness(
    source_root: str | os.PathLike[str],
) -> tuple[str, int, int]:
    """Return a stable source digest, module count, and total bytes for one harness.

    The directory must be named ``harness_eng`` and contain only the bounded
    Python modules accepted by task staging. A second complete scan detects
    concurrent edits before the fingerprint is returned.
    """
    source = _directory(source_root)
    if source.name != "harness_eng":
        raise RuntimePinError("candidate_harness_source_invalid")
    manifest, total_bytes = _candidate_harness_manifest(source)
    verified_manifest, verified_bytes = _candidate_harness_manifest(source)
    if manifest != verified_manifest or total_bytes != verified_bytes:
        raise RuntimePinError("candidate_harness_source_changed")
    return _candidate_harness_sha256(manifest), len(manifest), total_bytes


def _set_agent_workspace_permissions(workspace: Path, staged_names: set[str]) -> None:
    """Expose inputs to the fixed sandbox UID and make only output writable.

    The workspace is mounted directly into Docker, so its ancestors remain
    private to the invoking account while its contents need explicit sandbox
    permissions. Output is a nested writable mount; filesystem modes provide
    a second boundary on platforms that enforce POSIX bind-mount permissions.
    """
    try:
        for relative in staged_names:
            path = workspace.joinpath(*_relative(relative))
            info = path.lstat()
            if _is_link_or_reparse(info) or not stat.S_ISREG(info.st_mode):
                raise RuntimePinError("agent_workspace_file_invalid")
            os.chmod(path, 0o666 if relative == "output/payload.py" else 0o444)

        directories = {workspace, workspace / "output"}
        for relative in staged_names:
            parent = workspace.joinpath(*_relative(relative)).parent
            while parent != workspace:
                directories.add(parent)
                parent = parent.parent
        for directory in sorted(directories, key=lambda path: len(path.parts), reverse=True):
            info = directory.lstat()
            if _is_link_or_reparse(info) or not stat.S_ISDIR(info.st_mode):
                raise RuntimePinError("agent_workspace_path_invalid")
            os.chmod(directory, 0o777 if directory == workspace / "output" else 0o555)
    except RuntimePinError:
        raise
    except OSError:
        raise RuntimePinError("agent_workspace_permissions_unavailable") from None


def prepare_engineering_task_workspace(
    runtime: EngineeringRuntime,
    *,
    task_id: str,
    runs_root: str | os.PathLike[str],
    workspace_path: str | os.PathLike[str],
    protected_roots: tuple[str | os.PathLike[str], ...] = (),
    candidate_harness_source: str | os.PathLike[str] | None = None,
) -> PreparedEngineeringTask:
    """Stage only pinned agent-visible task inputs into a new local workspace.

    The grader/evaluator files are never copied. The upstream seed candidate is
    remapped to `output/payload.py`, matching the gateway's output-only write
    boundary; a later trusted runner must submit that output to the evaluator.
    Partial destinations are preserved for review on failure, never deleted.
    """
    if not isinstance(task_id, str) or task_id not in runtime.tasks:
        raise RuntimePinError("task_id_not_in_verified_runtime")
    runs = _directory(runs_root)
    runtime_roots = [runtime.benchmark_root, runtime.evaluator_root]
    protected = []
    for value in protected_roots:
        try:
            protected.append(Path(value).expanduser().resolve(strict=False))
        except (OSError, RuntimeError, TypeError, ValueError):
            raise RuntimePinError("protected_path_invalid") from None
    _validate_runtime_roots(runtime_roots[0], runtime_roots[1], protected)
    if any(_overlaps(runs, root) for root in runtime_roots):
        raise RuntimePinError("runtime_roots_must_be_disjoint")

    raw_workspace = Path(workspace_path).expanduser()
    if (not raw_workspace.is_absolute() or
            (raw_workspace.drive.startswith("\\\\") if raw_workspace.drive else False) or
            any(part in {".", ".."} for part in raw_workspace.parts)):
        raise RuntimePinError("agent_workspace_path_invalid")
    absolute_workspace = Path(os.path.abspath(raw_workspace))
    try:
        relative_workspace = absolute_workspace.relative_to(runs)
    except ValueError:
        raise RuntimePinError("agent_workspace_must_be_under_runs_root") from None
    if not relative_workspace.parts or any(part in {"", ".", ".."} for part in relative_workspace.parts):
        raise RuntimePinError("agent_workspace_path_invalid")
    if len(relative_workspace.parts) < 2:
        raise RuntimePinError("agent_workspace_requires_private_parent")
    _relative("/".join(relative_workspace.parts))
    workspace = runs.joinpath(*relative_workspace.parts)
    if any(root != runs and _overlaps(workspace, root) for root in protected):
        raise RuntimePinError("agent_workspace_overlaps_candidate_root")
    candidate_harness = None
    harness_manifest: list[tuple[str, int, str]] = []
    harness_bytes = 0
    if candidate_harness_source is not None:
        candidate_harness = _directory(candidate_harness_source)
        if candidate_harness.name != "harness_eng":
            raise RuntimePinError("candidate_harness_source_invalid")
        if any(_overlaps(candidate_harness, root)
               for root in runtime_roots):
            raise RuntimePinError("candidate_harness_overlaps_runtime_root")
        if _overlaps(candidate_harness, workspace):
            raise RuntimePinError("candidate_harness_overlaps_task_workspace")
        harness_manifest, harness_bytes = _candidate_harness_manifest(candidate_harness)
    parent = runs
    for part in relative_workspace.parts[:-1]:
        parent = parent / part
        _mkdir_new_or_existing_directory(parent)
    try:
        workspace.mkdir(mode=0o700)
    except FileExistsError:
        raise RuntimePinError("agent_workspace_already_exists") from None
    except OSError:
        raise RuntimePinError("agent_workspace_could_not_be_created") from None

    source_task = runtime.tasks[task_id]
    copy_list_path = _inside(source_task, "frontier_eval/copy_files.txt")
    copy_list = _pinned_benchmark_file(runtime, copy_list_path, 64 << 10)
    try:
        visible = [line for line in copy_list.decode("utf-8").splitlines() if line]
    except UnicodeDecodeError:
        raise RuntimePinError("benchmark_agent_visibility_invalid") from None
    required = {"LLM_prompt.txt", "output_structure.py", "submission/payload.py"}
    allowed = required | {"images"}
    if (len(visible) != len(set(visible)) or not required <= set(visible) or
            not set(visible) <= allowed):
        raise RuntimePinError("benchmark_agent_visibility_invalid")

    output = workspace / "output"
    output.mkdir(mode=0o700)
    staged_hashes: list[tuple[str, int, str]] = []
    total_bytes = 0

    def stage_file(source_relative: str, destination_relative: str) -> None:
        nonlocal total_bytes
        source = _inside(source_task, source_relative)
        raw = _pinned_benchmark_file(runtime, source)
        total_bytes += len(raw)
        if total_bytes > MAX_AGENT_WORKSPACE_BYTES or len(staged_hashes) >= MAX_AGENT_FILES:
            raise RuntimePinError("agent_workspace_size_limit_exceeded")
        destination_parts = _relative(destination_relative)
        destination = workspace.joinpath(*destination_parts)
        parent = destination.parent
        if parent != workspace:
            relative_parent = parent.relative_to(workspace)
            current = workspace
            for part in relative_parent.parts:
                current = current / part
                _mkdir_new_or_existing_directory(current)
        _write_new_agent_file(destination, raw)
        staged_hashes.append((destination_relative, len(raw), hashlib.sha256(raw).hexdigest()))

    def stage_candidate_bytes(destination_relative: str, raw: bytes) -> None:
        nonlocal total_bytes
        total_bytes += len(raw)
        if total_bytes > MAX_AGENT_WORKSPACE_BYTES or len(staged_hashes) >= MAX_AGENT_FILES:
            raise RuntimePinError("agent_workspace_size_limit_exceeded")
        destination = workspace.joinpath(*_relative(destination_relative))
        current = workspace
        for part in destination.parent.relative_to(workspace).parts:
            current = current / part
            _mkdir_new_or_existing_directory(current)
        _write_new_agent_file(destination, raw)
        staged_hashes.append((destination_relative, len(raw), hashlib.sha256(raw).hexdigest()))

    for source_relative in visible:
        if source_relative == "images":
            source_images = _inside(source_task, "images")
            stack = [source_images]
            base = source_images
            while stack:
                directory = stack.pop()
                try:
                    children = sorted(os.scandir(directory), key=lambda item: (item.name.casefold(), item.name))
                except OSError:
                    raise RuntimePinError("benchmark_agent_file_unavailable") from None
                for child in children:
                    try:
                        info = os.lstat(child.path)
                    except OSError:
                        raise RuntimePinError("benchmark_file_changed_after_pin") from None
                    if _is_link_or_reparse(info):
                        raise RuntimePinError("benchmark_manifest_contains_link_or_reparse_point")
                    child_path = Path(child.path)
                    if stat.S_ISDIR(info.st_mode):
                        stack.append(child_path)
                    elif stat.S_ISREG(info.st_mode) and getattr(info, "st_nlink", 1) == 1:
                        source_relative_path = child_path.relative_to(source_task).as_posix()
                        image_relative_path = child_path.relative_to(base).as_posix()
                        stage_file(source_relative_path, f"images/{image_relative_path}")
                    else:
                        raise RuntimePinError("benchmark_agent_file_invalid")
        elif source_relative == "submission/payload.py":
            stage_file(source_relative, "output/payload.py")
        else:
            stage_file(source_relative, source_relative)

    if candidate_harness is not None:
        for relative, expected_size, expected_sha256 in harness_manifest:
            source = candidate_harness.joinpath(*_relative(relative))
            raw = _read_stable_file(source, MAX_AGENT_FILE_BYTES,
                                    "candidate_harness_source_changed")
            if (len(raw) != expected_size or
                    hashlib.sha256(raw).hexdigest() != expected_sha256):
                raise RuntimePinError("candidate_harness_source_changed")
            stage_candidate_bytes(f".threadforge-agent/harness_eng/{relative}", raw)
        final_manifest, final_bytes = _candidate_harness_manifest(candidate_harness)
        if final_manifest != harness_manifest or final_bytes != harness_bytes:
            raise RuntimePinError("candidate_harness_source_changed")

    staged_names = {entry[0] for entry in staged_hashes}
    required_destinations = {"LLM_prompt.txt", "output_structure.py", "output/payload.py"}
    if (not required_destinations <= staged_names or
            any(name not in required_destinations and not name.startswith("images/") and
                not (name.startswith(".threadforge-agent/harness_eng/") and name.endswith(".py"))
                for name in staged_names)):
        raise RuntimePinError("agent_workspace_manifest_mismatch")
    if (candidate_harness is not None and
            ".threadforge-agent/harness_eng/main.py" not in staged_names):
        raise RuntimePinError("candidate_harness_entrypoint_missing")
    _set_agent_workspace_permissions(workspace, staged_names)
    digest = hashlib.sha256()
    for relative, size, file_sha in sorted(staged_hashes, key=lambda row: (row[0].casefold(), row[0])):
        digest.update(f"{relative}\t{size}\t{file_sha}\n".encode("utf-8"))
    return PreparedEngineeringTask(
        task_id=task_id,
        workspace=workspace,
        prompt_path=workspace / "LLM_prompt.txt",
        schema_path=workspace / "output_structure.py",
        candidate_path=workspace / "output" / "payload.py",
        candidate_seed_sha256=next(
            file_sha for relative, _size, file_sha in staged_hashes
            if relative == "output/payload.py"
        ),
        workspace_sha256=digest.hexdigest(),
        file_count=len(staged_hashes),
        total_bytes=total_bytes,
        candidate_harness_sha256=(
            _candidate_harness_sha256(harness_manifest)
            if candidate_harness is not None else None
        ),
        candidate_harness_file_count=len(harness_manifest),
        candidate_harness_bytes=harness_bytes,
    )


def collect_engineering_candidate(prepared: PreparedEngineeringTask
                                  ) -> tuple[bytes | None, bool]:
    """Read the bounded output deliverable and report whether it changed."""
    try:
        workspace = _directory(prepared.workspace)
        candidate = Path(os.path.abspath(prepared.candidate_path))
        if candidate.relative_to(workspace).as_posix() != "output/payload.py":
            raise RuntimePinError("agent_candidate_path_invalid")
        try:
            info = candidate.lstat()
        except FileNotFoundError:
            return None, False
        if (_is_link_or_reparse(info) or not stat.S_ISREG(info.st_mode) or
                getattr(info, "st_nlink", 1) != 1):
            raise RuntimePinError("agent_candidate_invalid")
        raw = _read_stable_file(candidate, 256 << 10, "agent_candidate_invalid")
    except RuntimePinError:
        raise
    except (OSError, RuntimeError, TypeError, ValueError):
        raise RuntimePinError("agent_candidate_invalid") from None
    digest = hashlib.sha256(raw).hexdigest()
    submitted = (bool(raw.strip()) and digest != prepared.candidate_seed_sha256 and
                 _SEED_PAYLOAD_MARKER not in raw)
    return raw, submitted


def _set_evaluation_workspace_permissions(workspace: Path, staged_names: set[str]) -> None:
    """Make copied benchmark/evaluator inputs readable but not writable by Docker."""
    try:
        for relative in staged_names:
            path = workspace.joinpath(*_relative(relative))
            info = path.lstat()
            if _is_link_or_reparse(info) or not stat.S_ISREG(info.st_mode):
                raise RuntimePinError("evaluation_workspace_file_invalid")
            os.chmod(path, 0o444)

        directories = {workspace, workspace / EVALUATION_OUTPUT_DIRECTORY}
        for relative in staged_names:
            parent = workspace.joinpath(*_relative(relative)).parent
            while parent != workspace:
                directories.add(parent)
                parent = parent.parent
        for directory in sorted(directories, key=lambda path: len(path.parts), reverse=True):
            info = directory.lstat()
            if _is_link_or_reparse(info) or not stat.S_ISDIR(info.st_mode):
                raise RuntimePinError("evaluation_workspace_path_invalid")
            os.chmod(directory, 0o777 if directory == workspace / EVALUATION_OUTPUT_DIRECTORY else 0o555)
    except RuntimePinError:
        raise
    except OSError:
        raise RuntimePinError("evaluation_workspace_permissions_unavailable") from None


def prepare_engineering_evaluation_workspace(
    runtime: EngineeringRuntime,
    *,
    task_id: str,
    candidate_bytes: bytes,
    runs_root: str | os.PathLike[str],
    workspace_path: str | os.PathLike[str],
    agent_workspace: str | os.PathLike[str],
    protected_roots: tuple[str | os.PathLike[str], ...] = (),
) -> PreparedEngineeringEvaluation:
    """Stage a pinned task and candidate for isolated scoring.

    The canonical evaluator entrypoint is copied from the clean, pinned RRSI
    evaluator checkout rather than executed from benchmark-provided wrapper
    code. Task-specific ``evaluate.py`` and other scoring inputs remain from
    the pinned benchmark and run only inside the offline Docker sandbox.
    """
    if not isinstance(task_id, str) or task_id not in runtime.tasks:
        raise RuntimePinError("task_id_not_in_verified_runtime")
    if not isinstance(candidate_bytes, bytes) or len(candidate_bytes) > (256 << 10):
        raise RuntimePinError("evaluation_candidate_invalid")
    runs = _directory(runs_root)
    agent = _directory(agent_workspace)
    if not agent.is_relative_to(runs):
        raise RuntimePinError("agent_workspace_must_be_under_runs_root")

    runtime_roots = [runtime.benchmark_root, runtime.evaluator_root]
    protected = []
    for value in protected_roots:
        try:
            protected.append(Path(value).expanduser().resolve(strict=False))
        except (OSError, RuntimeError, TypeError, ValueError):
            raise RuntimePinError("protected_path_invalid") from None
    _validate_runtime_roots(runtime_roots[0], runtime_roots[1], protected)
    if any(_overlaps(runs, root) for root in runtime_roots):
        raise RuntimePinError("runtime_roots_must_be_disjoint")

    raw_workspace = Path(workspace_path).expanduser()
    if (not raw_workspace.is_absolute() or
            (raw_workspace.drive.startswith("\\\\") if raw_workspace.drive else False) or
            any(part in {".", ".."} for part in raw_workspace.parts)):
        raise RuntimePinError("evaluation_workspace_path_invalid")
    workspace = Path(os.path.abspath(raw_workspace))
    try:
        relative_workspace = workspace.relative_to(runs)
    except ValueError:
        raise RuntimePinError("evaluation_workspace_must_be_under_runs_root") from None
    if len(relative_workspace.parts) < 2:
        raise RuntimePinError("evaluation_workspace_requires_private_parent")
    if any(root != runs and _overlaps(workspace, root) for root in protected):
        raise RuntimePinError("evaluation_workspace_overlaps_candidate_root")
    if (workspace == agent or workspace.is_relative_to(agent) or agent.is_relative_to(workspace)):
        raise RuntimePinError("evaluation_workspace_overlaps_agent_workspace")
    parent = runs
    for part in relative_workspace.parts[:-1]:
        parent = parent / part
        _mkdir_new_or_existing_directory(parent)
    try:
        workspace.mkdir(mode=0o700)
    except FileExistsError:
        raise RuntimePinError("evaluation_workspace_already_exists") from None
    except OSError:
        raise RuntimePinError("evaluation_workspace_could_not_be_created") from None

    source_task = runtime.tasks[task_id]
    task_prefix = source_task.relative_to(runtime.benchmark_root).as_posix() + "/"
    source_files = sorted(
        relative for relative in runtime.benchmark_files
        if relative.startswith(task_prefix)
    )
    if not source_files:
        raise RuntimePinError("benchmark_task_files_unavailable")
    if any(relative.startswith(EVALUATION_OUTPUT_DIRECTORY + "/") or
           relative == EVALUATION_OUTPUT_DIRECTORY
           for relative in (name[len(task_prefix):] for name in source_files)):
        raise RuntimePinError("evaluation_output_directory_collision")

    canonical_evaluator = _pinned_evaluator_file(runtime, CANONICAL_EVALUATOR_SCRIPT)
    output_dir = workspace / EVALUATION_OUTPUT_DIRECTORY
    try:
        output_dir.mkdir(mode=0o700)
    except OSError:
        raise RuntimePinError("evaluation_workspace_could_not_be_created") from None

    staged: list[tuple[str, int, str]] = []
    total_bytes = 0

    def stage(relative: str, raw: bytes) -> None:
        nonlocal total_bytes
        parts = _relative(relative)
        total_bytes += len(raw)
        if total_bytes > MAX_EVALUATION_WORKSPACE_BYTES or len(staged) >= MAX_EVALUATION_FILES:
            raise RuntimePinError("evaluation_workspace_size_limit_exceeded")
        destination = workspace.joinpath(*parts)
        parent = destination.parent
        if parent != workspace:
            current = workspace
            for part in parent.relative_to(workspace).parts:
                current = current / part
                _mkdir_new_or_existing_directory(current)
        _write_new_agent_file(destination, raw)
        staged.append((relative, len(raw), hashlib.sha256(raw).hexdigest()))

    for benchmark_relative in source_files:
        task_relative = benchmark_relative[len(task_prefix):]
        source_path = runtime.benchmark_root.joinpath(*_relative(benchmark_relative))
        if task_relative == "submission/payload.py":
            _pinned_benchmark_file(runtime, source_path, MAX_EVALUATION_FILE_BYTES)
            raw = candidate_bytes
        elif task_relative == "frontier_eval/evaluate_submission.py":
            _pinned_benchmark_file(runtime, source_path, MAX_EVALUATION_FILE_BYTES)
            raw = canonical_evaluator
        else:
            raw = _pinned_benchmark_file(runtime, source_path, MAX_EVALUATION_FILE_BYTES)
        stage(task_relative, raw)

    staged_names = {relative for relative, _size, _digest in staged}
    if ("submission/payload.py" not in staged_names or
            "frontier_eval/evaluate_submission.py" not in staged_names):
        raise RuntimePinError("evaluation_workspace_manifest_mismatch")
    _set_evaluation_workspace_permissions(workspace, staged_names)
    digest = hashlib.sha256()
    for relative, size, file_sha in sorted(staged, key=lambda row: (row[0].casefold(), row[0])):
        digest.update(f"{relative}\t{size}\t{file_sha}\n".encode("utf-8"))
    return PreparedEngineeringEvaluation(
        workspace=workspace,
        candidate_path=workspace / "submission" / "payload.py",
        evaluator_path=workspace / "frontier_eval" / "evaluate_submission.py",
        metrics_path=output_dir / "metrics.json",
        artifacts_path=output_dir / "artifacts.json",
        writable_subdirectory=EVALUATION_OUTPUT_DIRECTORY,
        workspace_sha256=digest.hexdigest(),
        file_count=len(staged),
        total_bytes=total_bytes,
    )


def create_engineering_runtime_pin(
    *,
    benchmark_root: str | os.PathLike[str],
    evaluator_root: str | os.PathLike[str],
    inventory_bundle: str | os.PathLike[str],
    output_path: str | os.PathLike[str],
    image: str,
    benchmark_license: str,
    evaluator_license: str,
    license_review_id: str,
    protected_roots: tuple[str | os.PathLike[str], ...],
    confirm_license_review: bool,
) -> dict:
    """Create a no-replace local pin after checking the private inventory.

    License strings and ``license_review_id`` are attestations supplied by the
    operator; this function does not perform a legal review or authenticate the
    person making that assertion.
    """
    from rrsi.inventory import load_domain_inventory

    if confirm_license_review is not True:
        raise RuntimePinError("license_review_confirmation_required")
    benchmark = _directory(benchmark_root)
    evaluator = _directory(evaluator_root)
    if not valid_pinned_image(image):
        raise RuntimePinError("execution_image_must_be_digest_pinned")
    inventory = _check_components(Path(inventory_bundle).expanduser())
    if not stat.S_ISREG(inventory.lstat().st_mode):
        raise RuntimePinError("inventory_bundle_invalid")
    output = Path(output_path).expanduser()
    if not output.is_absolute() or (output.drive.startswith("\\\\") if output.drive else False):
        raise RuntimePinError("runtime_pin_output_must_be_absolute_local_path")
    output = Path(os.path.abspath(output))
    parent = _directory(output.parent)
    if output.name in {"", ".", ".."} or output.exists() or output.is_symlink():
        raise RuntimePinError("runtime_pin_output_exists_or_invalid")
    protected = []
    for value in protected_roots:
        try:
            protected.append(Path(value).expanduser().resolve(strict=False))
        except (OSError, RuntimeError, TypeError, ValueError):
            raise RuntimePinError("protected_path_invalid") from None
    _validate_runtime_roots(benchmark, evaluator, protected)
    for protected_root in [benchmark, evaluator, *protected]:
        if output == protected_root or output.is_relative_to(protected_root):
            raise RuntimePinError("runtime_pin_must_be_outside_runtime_and_candidate_roots")
    if inventory == output or any(inventory == root or inventory.is_relative_to(root)
                                  for root in [benchmark, evaluator, *protected]):
        raise RuntimePinError("inventory_bundle_must_be_private_and_outside_candidate_roots")

    try:
        loaded = load_domain_inventory(inventory, "rrsi-engineering-design")
    except (OSError, RuntimeError, ValueError):
        raise RuntimePinError("inventory_bundle_invalid") from None
    task_ids = list(loaded.evolve_ids)
    if not task_ids:
        raise RuntimePinError("verified_task_set_invalid")
    _validate_task_manifest(benchmark, task_ids)
    revision = _git_revision(evaluator)
    benchmark_digest = tree_sha256(benchmark)
    evaluator_digest = tree_sha256(evaluator)
    task_digest = canonical_task_ids_sha256(task_ids)
    value = _validate_pin({
        "schema_version": PIN_SCHEMA,
        "rrsi_source_revision": RRSI_SOURCE_REVISION,
        "evaluator_source_revision": revision,
        "benchmark_tree_sha256": benchmark_digest,
        "evaluator_tree_sha256": evaluator_digest,
        "task_count": len(task_ids),
        "task_ids_sha256": task_digest,
        "execution_image": image,
        "benchmark_license": benchmark_license,
        "evaluator_license": evaluator_license,
        "license_review_id": license_review_id,
    })
    if (_git_revision(evaluator) != revision or
            tree_sha256(benchmark) != benchmark_digest or
            tree_sha256(evaluator) != evaluator_digest):
        raise RuntimePinError("runtime_changed_during_pin_creation")
    _validate_task_manifest(benchmark, task_ids)

    raw = (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
    if len(raw) > MAX_PIN_BYTES:
        raise RuntimePinError("runtime_pin_invalid")
    temp_path = None
    try:
        descriptor, temp_name = tempfile.mkstemp(prefix=".threadforge-runtime-pin-", dir=parent)
        temp_path = Path(temp_name)
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        if os.name != "nt":
            os.chmod(temp_path, 0o600)
        os.link(temp_path, output)
    except FileExistsError:
        raise RuntimePinError("runtime_pin_output_exists_or_invalid") from None
    except OSError:
        raise RuntimePinError("runtime_pin_write_failed") from None
    finally:
        if temp_path is not None:
            try:
                temp_path.unlink(missing_ok=True)
            except OSError:
                pass
    return {"pin_sha256": hashlib.sha256(raw).hexdigest(), "task_count": len(task_ids)}


def main(argv: list[str] | None = None) -> int:
    import argparse

    parser = argparse.ArgumentParser(description="Create an operator-reviewed local Engineering runtime pin.")
    parser.add_argument("--benchmark-root", required=True)
    parser.add_argument("--evaluator-root", required=True)
    parser.add_argument("--inventory-bundle", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--image", required=True)
    parser.add_argument("--benchmark-license", required=True)
    parser.add_argument("--evaluator-license", required=True)
    parser.add_argument("--license-review-id", required=True)
    parser.add_argument("--confirm-license-review", action="store_true")
    args = parser.parse_args(argv)
    if not args.confirm_license_review:
        parser.error("--confirm-license-review is required after a human review of both data licenses")
    repo_root = Path(__file__).resolve().parents[3]
    try:
        result = create_engineering_runtime_pin(
            benchmark_root=args.benchmark_root,
            evaluator_root=args.evaluator_root,
            inventory_bundle=args.inventory_bundle,
            output_path=args.output,
            image=args.image,
            benchmark_license=args.benchmark_license,
            evaluator_license=args.evaluator_license,
            license_review_id=args.license_review_id,
            protected_roots=(repo_root,),
            confirm_license_review=args.confirm_license_review,
        )
    except RuntimePinError as exc:
        parser.exit(1, f"runtime pin refused: {exc.reason_code}\n")
    print(json.dumps({"schema_version": PIN_SCHEMA, **result}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
