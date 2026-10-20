"""Bounded, local-only CPU replay of AnyJev synthetic-label head distillation."""

from __future__ import annotations

import hashlib
import io
import json
import math
import os
import random
import re
import stat
import tempfile
import zipfile
from pathlib import Path
from typing import Any

import numpy as np

from anyjev.calibrate.posthoc import TemperatureScaler
from anyjev.heads import LinearHead, _folds, _standardise, fit_head

MAX_ROOT_CHARS = 4096
MAX_RELATIVE_PATH_CHARS = 4096
MAX_NPZ_BYTES = 256 << 20
MAX_TOTAL_SOURCE_BYTES = 2 << 30
MAX_NPZ_EXPANDED_BYTES = 512 << 20
MAX_NPZ_MEMBERS = 64
MAX_ARRAY_ELEMENTS = 10_000_000
MAX_PROBABILITY_ELEMENTS = 5_000_000
MAX_FIT_EXAMPLES = 5_000
MAX_FEATURE_WIDTH = 65_536
MAX_QUESTIONS = 256
MAX_STUDENTS = 8
MAX_TOTAL_TEST_ITEMS = 100_000
MAX_BOOTSTRAP_RESAMPLES = 10_000
MAX_SOURCE_JSON_BYTES = 64 << 20
MAX_TOTAL_SOURCE_JSON_BYTES = 512 << 20
MAX_TOTAL_DISTILLATION_STATES = 100_000
MAX_TOTAL_GOLD_FEATURE_ELEMENTS = 50_000_000
MAX_TOTAL_GENERATION_ATTEMPTS = 100_000
MAX_TYPED_EXPORT_ROWS_PER_WORKFLOW = MAX_FIT_EXAMPLES
ANYJEV_TYPED_WORKFLOWS = frozenset({
    "security_incidents", "invoice_processing", "customer_service", "agent_trace_observability",
})
EPS = 1e-12
_SAFE_NPZ_MEMBER = re.compile(r"^[A-Za-z0-9_-]{1,64}\.npy$")
_SAFE_WORKFLOW = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$")


class DistillationError(ValueError):
    """Invalid, unsafe, or irreconcilable distillation input."""


def _only_fields(value: dict[str, Any], allowed: set[str], label: str) -> None:
    unknown = set(value) - allowed
    if unknown:
        raise DistillationError(f"{label} contains unsupported fields")


def _sha256_text(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def _real_cache_root(value: Any) -> Path:
    if not isinstance(value, str) or not value.strip() or len(value) > MAX_ROOT_CHARS:
        raise DistillationError("cache_root must be a bounded absolute local directory")
    supplied = Path(value).expanduser()
    if not supplied.is_absolute():
        raise DistillationError("cache_root must be an absolute local directory")
    try:
        info = supplied.lstat()
        root = supplied.resolve(strict=True)
        root_info = root.lstat()
    except OSError:
        raise DistillationError("cache_root is unavailable") from None
    supplied_reparse = bool(getattr(info, "st_file_attributes", 0) & 0x400)
    root_reparse = bool(getattr(root_info, "st_file_attributes", 0) & 0x400)
    if (not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode) or supplied_reparse
            or not stat.S_ISDIR(root_info.st_mode) or stat.S_ISLNK(root_info.st_mode) or root_reparse):
        raise DistillationError("cache_root must be a real local directory")
    return root


def _relative_parts(value: Any, extension: str = ".npz") -> tuple[str, ...]:
    if (not isinstance(value, str) or not value or len(value) > MAX_RELATIVE_PATH_CHARS
            or "\\" in value or ":" in value or "\x00" in value or value.startswith("/")):
        raise DistillationError("cache paths must be bounded POSIX-relative paths")
    parts = tuple(value.split("/"))
    if (len(parts) > 64 or any(part in {"", ".", ".."} or part.endswith((".", " ")) for part in parts)
            or not value.lower().endswith(extension)):
        raise DistillationError("cache path contains an unsupported component")
    return parts


def _stat_identity(info: os.stat_result) -> tuple[int, int, int, int, int]:
    return (stat.S_IFMT(info.st_mode), info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns)


def _read_cache_bytes(root: Path, relative_path: str, total_bytes: list[int], *,
                      extension: str = ".npz", file_limit: int = MAX_NPZ_BYTES,
                      label: str = "NPZ cache") -> tuple[bytes, str]:
    parts = _relative_parts(relative_path, extension)
    current = root
    try:
        root_before = root.lstat()
    except OSError:
        raise DistillationError("cache_root changed before reading") from None
    if (not stat.S_ISDIR(root_before.st_mode) or stat.S_ISLNK(root_before.st_mode)
            or bool(getattr(root_before, "st_file_attributes", 0) & 0x400)):
        raise DistillationError("cache_root must remain a real local directory")
    root_identity = _stat_identity(root_before)
    directories: list[tuple[Path, tuple[int, int, int, int, int]]] = []
    try:
        for index, part in enumerate(parts):
            current = current / part
            info = current.lstat()
            reparse = bool(getattr(info, "st_file_attributes", 0) & 0x400)
            if stat.S_ISLNK(info.st_mode) or reparse:
                raise DistillationError("cache paths cannot contain symlinks or reparse points")
            if index < len(parts) - 1 and not stat.S_ISDIR(info.st_mode):
                raise DistillationError("cache path contains a non-directory component")
            if index < len(parts) - 1:
                directories.append((current, _stat_identity(info)))
            if index == len(parts) - 1 and not stat.S_ISREG(info.st_mode):
                raise DistillationError("cache path must identify a regular file")
        before = current.lstat()
        if before.st_size <= 0 or before.st_size > file_limit:
            raise DistillationError(f"{label} exceeds the per-file size limit")
        if before.st_size > MAX_TOTAL_SOURCE_BYTES - total_bytes[0]:
            raise DistillationError("distillation input exceeds the total source-byte limit")
        flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(current, flags)
        with os.fdopen(descriptor, "rb") as stream:
            opened = os.fstat(stream.fileno())
            if _stat_identity(opened) != _stat_identity(before):
                raise DistillationError("NPZ cache changed before reading")
            raw = stream.read(file_limit + 1)
            after = os.fstat(stream.fileno())
        path_after = current.lstat()
        directories_after = [(path, path.lstat()) for path, _ in directories]
        root_after = root.lstat()
    except DistillationError:
        raise
    except OSError:
        raise DistillationError("NPZ cache is unavailable or changed") from None
    if (len(raw) != before.st_size or len(raw) > file_limit
            or _stat_identity(opened) != _stat_identity(after)
            or _stat_identity(before) != _stat_identity(path_after)
            or _stat_identity(root_after) != root_identity
            or not stat.S_ISDIR(root_after.st_mode)
            or bool(getattr(root_after, "st_file_attributes", 0) & 0x400)):
        raise DistillationError("NPZ cache changed while reading")
    if any(_stat_identity(after_info) != identity or not stat.S_ISDIR(after_info.st_mode)
           or bool(getattr(after_info, "st_file_attributes", 0) & 0x400)
           for (_, identity), (_, after_info) in zip(directories, directories_after)):
        raise DistillationError("NPZ cache directory changed while reading")
    total_bytes[0] += len(raw)
    return raw, hashlib.sha256(raw).hexdigest()


def _reject_credentials(value: Any, label: str) -> None:
    from .protocol import _contains_credential

    if _contains_credential(value):
        raise DistillationError(f"{label} contains credential material")


def _read_json_source(root: Path, relative_path: str, total_bytes: list[int]) -> tuple[dict[str, Any], str]:
    raw, digest = _read_cache_bytes(
        root, relative_path, total_bytes, extension=".json", file_limit=MAX_SOURCE_JSON_BYTES,
        label="distillation source JSON",
    )
    if total_bytes[0] > MAX_TOTAL_SOURCE_JSON_BYTES:
        raise DistillationError("distillation JSON sources exceed the aggregate size limit")

    def unique_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("duplicate JSON key")
            result[key] = value
        return result

    def reject_constant(_value: str) -> None:
        raise ValueError("non-finite JSON constant")

    try:
        document = json.loads(raw, object_pairs_hook=unique_pairs, parse_constant=reject_constant)
    except (UnicodeDecodeError, ValueError):
        raise DistillationError("distillation source JSON is malformed") from None
    if not isinstance(document, dict):
        raise DistillationError("distillation source JSON must be an object")
    _reject_credentials(document, "distillation source JSON")
    return document, digest


def _safe_output_path(root: Path, relative_path: str, extension: str = ".npz") -> Path:
    parts = _relative_parts(relative_path, extension)
    current = root
    root_info = root.lstat()
    root_identity = (root_info.st_dev, root_info.st_ino)
    try:
        for component in parts[:-1]:
            current = current / component
            try:
                current.mkdir(mode=0o700)
            except FileExistsError:
                pass
            info = current.lstat()
            if (not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode)
                    or bool(getattr(info, "st_file_attributes", 0) & 0x400)):
                raise DistillationError("distillation output path contains a link or non-directory")
        target = current / parts[-1]
        root_after = root.lstat()
    except DistillationError:
        raise
    except OSError:
        raise DistillationError("distillation output directory is unavailable") from None
    if ((root_after.st_dev, root_after.st_ino) != root_identity or not stat.S_ISDIR(root_after.st_mode)
            or stat.S_ISLNK(root_after.st_mode) or bool(getattr(root_after, "st_file_attributes", 0) & 0x400)):
        raise DistillationError("distillation output root changed while preparing output")
    return target


def _write_npz_exclusive(root: Path, relative_path: str, arrays: dict[str, np.ndarray]) -> str:
    target = _safe_output_path(root, relative_path)
    for key, value in arrays.items():
        if (not isinstance(key, str) or not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", key)
                or not isinstance(value, np.ndarray) or value.dtype.hasobject
                or value.dtype.kind not in "biuf" or value.size > MAX_ARRAY_ELEMENTS
                or value.nbytes > MAX_NPZ_EXPANDED_BYTES or not np.all(np.isfinite(value))):
            raise DistillationError("generated cache contains an invalid array")

    def existing_digest() -> str:
        fields, digest = _read_npz(root, relative_path, set(arrays), set(arrays), [0])
        if any(fields[key].shape != arrays[key].shape or fields[key].dtype != arrays[key].dtype
               or not np.array_equal(fields[key], arrays[key]) for key in arrays):
            raise DistillationError("an existing distillation cache conflicts with regenerated output")
        return digest

    try:
        try:
            info = target.lstat()
        except FileNotFoundError:
            info = None
        if info is not None:
            if stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & 0x400):
                raise DistillationError("distillation output cannot replace a linked cache")
            if not stat.S_ISREG(info.st_mode):
                raise DistillationError("distillation output path is not a regular file")
            return existing_digest()

        fd, temporary = tempfile.mkstemp(prefix=".threadforge-distill-", suffix=".tmp", dir=target.parent)
        try:
            with os.fdopen(fd, "wb") as stream:
                os.chmod(temporary, 0o600)
                np.savez_compressed(stream, **arrays)
                stream.flush()
                os.fsync(stream.fileno())
            digest = hashlib.sha256()
            size = 0
            with open(temporary, "rb") as stream:
                while chunk := stream.read(1 << 20):
                    size += len(chunk)
                    if size > MAX_NPZ_BYTES:
                        raise DistillationError("generated distillation cache exceeds the per-file size limit")
                    digest.update(chunk)
            try:
                os.link(temporary, target, follow_symlinks=False)
            except FileExistsError:
                return existing_digest()
            except (AttributeError, NotImplementedError, OSError):
                raise DistillationError("atomic exclusive distillation cache installation is unavailable") from None
            installed = target.lstat()
            if not stat.S_ISREG(installed.st_mode) or stat.S_ISLNK(installed.st_mode):
                raise DistillationError("installed distillation cache is not a regular file")
            return digest.hexdigest()
        finally:
            try:
                os.unlink(temporary)
            except OSError:
                pass
    except DistillationError:
        raise
    except OSError:
        raise DistillationError("distillation cache output failed safely") from None


def _write_json_exclusive(root: Path, relative_path: str, content: bytes) -> str:
    if not content or len(content) > MAX_SOURCE_JSON_BYTES:
        raise DistillationError("generated synthetic-state JSON exceeds the size limit")
    target = _safe_output_path(root, relative_path, ".json")
    digest = hashlib.sha256(content).hexdigest()
    try:
        try:
            info = target.lstat()
        except FileNotFoundError:
            info = None
        if info is not None:
            if stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & 0x400):
                raise DistillationError("synthetic-state output cannot replace a linked file")
            if not stat.S_ISREG(info.st_mode):
                raise DistillationError("synthetic-state output is not a regular file")
            existing, existing_digest = _read_cache_bytes(
                root, relative_path, [0], extension=".json", file_limit=MAX_SOURCE_JSON_BYTES,
                label="synthetic-state JSON",
            )
            if existing != content:
                raise DistillationError("an existing synthetic-state file conflicts with regenerated output")
            return existing_digest

        fd, temporary = tempfile.mkstemp(prefix=".threadforge-synth-", suffix=".tmp", dir=target.parent)
        try:
            with os.fdopen(fd, "wb") as stream:
                os.chmod(temporary, 0o600)
                stream.write(content)
                stream.flush()
                os.fsync(stream.fileno())
            try:
                os.link(temporary, target, follow_symlinks=False)
            except FileExistsError:
                existing, existing_digest = _read_cache_bytes(
                    root, relative_path, [0], extension=".json", file_limit=MAX_SOURCE_JSON_BYTES,
                    label="synthetic-state JSON",
                )
                if existing != content:
                    raise DistillationError("an existing synthetic-state file conflicts with regenerated output")
                return existing_digest
            except (AttributeError, NotImplementedError, OSError):
                raise DistillationError("atomic exclusive synthetic-state installation is unavailable") from None
            return digest
        finally:
            try:
                os.unlink(temporary)
            except OSError:
                pass
    except DistillationError:
        raise
    except OSError:
        raise DistillationError("synthetic-state output failed safely") from None


def _read_npz(
    root: Path,
    relative_path: str,
    required_keys: set[str],
    allowed_keys: set[str],
    total_bytes: list[int],
) -> tuple[dict[str, np.ndarray], str]:
    raw, digest = _read_cache_bytes(root, relative_path, total_bytes)
    try:
        with zipfile.ZipFile(io.BytesIO(raw), "r") as archive:
            infos = archive.infolist()
            names = [info.filename for info in infos]
            if (not infos or len(infos) > MAX_NPZ_MEMBERS or len(set(names)) != len(names)
                    or any(not _SAFE_NPZ_MEMBER.fullmatch(name) for name in names)):
                raise DistillationError("NPZ cache has an invalid member set")
            expanded = 0
            available: set[str] = set()
            for info in infos:
                key = info.filename[:-4]
                if key not in allowed_keys or info.is_dir() or info.flag_bits & 0x1:
                    raise DistillationError("NPZ cache contains an unsupported or encrypted member")
                if info.compress_type not in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED):
                    raise DistillationError("NPZ cache uses an unsupported compression method")
                expanded += info.file_size
                if expanded > MAX_NPZ_EXPANDED_BYTES or info.file_size > MAX_NPZ_EXPANDED_BYTES:
                    raise DistillationError("NPZ cache expanded size exceeds the limit")
                if info.file_size > 0 and info.compress_size == 0:
                    raise DistillationError("NPZ cache has an invalid compression ratio")
                if info.compress_size and info.file_size > info.compress_size * 1000:
                    raise DistillationError("NPZ cache compression ratio exceeds the limit")
                available.add(key)
            if not required_keys.issubset(available):
                raise DistillationError("NPZ cache is missing required arrays")
            with np.load(io.BytesIO(raw), allow_pickle=False, max_header_size=64 << 10) as loaded:
                arrays = {key: np.asarray(loaded[key]) for key in required_keys}
    except DistillationError:
        raise
    except (OSError, ValueError, TypeError, EOFError, zipfile.BadZipFile, zipfile.LargeZipFile):
        raise DistillationError("NPZ cache is malformed or contains an unsafe array") from None
    for value in arrays.values():
        if value.dtype.hasobject or value.dtype.kind not in "biuf" or value.size > MAX_ARRAY_ELEMENTS:
            raise DistillationError("NPZ cache contains an unsupported array dtype or shape")
    return arrays, digest


def _scalar_int(value: np.ndarray, label: str, minimum: int, maximum: int) -> int:
    if value.size != 1 or value.dtype.kind not in "iu":
        raise DistillationError(f"{label} must be an integer scalar")
    result = int(value.reshape(-1)[0])
    if result < minimum or result > maximum:
        raise DistillationError(f"{label} is out of range")
    return result


def _labels(value: np.ndarray, classes: int, label: str) -> np.ndarray:
    if value.ndim != 1 or value.dtype.kind not in "iu" or not 1 <= len(value) <= 10_000:
        raise DistillationError(f"{label} must be a bounded integer label vector")
    result = value.astype(np.int64, copy=False)
    if np.any(result < 0) or np.any(result >= classes):
        raise DistillationError(f"{label} contains a class outside the declared range")
    return result


def _features(value: np.ndarray, label: str) -> np.ndarray:
    if value.ndim not in (2, 3) or value.dtype.kind not in "fiu" or value.size > MAX_ARRAY_ELEMENTS:
        raise DistillationError(f"{label} must be a bounded 2D or 3D numeric feature array")
    if value.shape[0] < 1 or value.shape[0] > MAX_FIT_EXAMPLES or value.shape[-1] < 1 or value.shape[-1] > MAX_FEATURE_WIDTH:
        raise DistillationError(f"{label} feature dimensions are out of range")
    result = value.astype(np.float32, copy=False)
    if not np.all(np.isfinite(result)):
        raise DistillationError(f"{label} contains non-finite feature values")
    return result


def _probabilities(value: np.ndarray, classes: int, label: str) -> np.ndarray:
    if (value.ndim != 2 or value.shape[1] != classes or value.shape[0] < 1
            or value.shape[0] > 10_000 or value.size > MAX_PROBABILITY_ELEMENTS or value.dtype.kind not in "fiu"):
        raise DistillationError(f"{label} has an invalid probability matrix shape")
    result = value.astype(np.float64, copy=False)
    if (not np.all(np.isfinite(result)) or np.any(result < 0) or np.any(result > 1)
            or not np.allclose(result.sum(axis=1), 1.0, rtol=1e-5, atol=1e-5)):
        raise DistillationError(f"{label} rows must be finite normalized probabilities")
    return result


def _feature_cache(
    root: Path, relative_path: str, block: int, n_layers: int, total_bytes: list[int],
) -> tuple[np.ndarray, np.ndarray, int, str]:
    arrays, digest = _read_npz(root, relative_path, {"H", "y", "K", "layers"},
                               {"H", "G", "Z", "y", "K", "kind", "group", "name", "soft", "lens", "perms", "layers", "seconds"},
                               total_bytes)
    hidden = _features(arrays["H"], "feature cache H")
    classes = _scalar_int(arrays["K"], "feature cache K", 2, 512)
    labels = _labels(arrays["y"], classes, "feature cache y")
    layers = arrays["layers"]
    if (hidden.ndim != 3 or layers.ndim != 1 or layers.dtype.kind not in "iu"
            or hidden.shape[1] != len(layers) or hidden.shape[0] != len(labels)):
        raise DistillationError("feature cache H, y, and layers dimensions do not reconcile")
    absolute_layers = [n_layers + 1 + int(item) if int(item) < 0 else int(item) for item in layers]
    if len(set(absolute_layers)) != len(absolute_layers) or block not in absolute_layers:
        raise DistillationError("requested block is absent from the feature cache")
    return hidden[:, absolute_layers.index(block), :], labels, classes, digest


def _prob_cache(
    root: Path, relative_path: str, require_gold: bool, total_bytes: list[int],
) -> tuple[np.ndarray, int, np.ndarray | None, str]:
    keys = {"probs", "K", "block"} | ({"y_gold"} if require_gold else set())
    allowed = {"probs", "K", "block", "y_gold"}
    arrays, digest = _read_npz(root, relative_path, keys, allowed, total_bytes)
    classes = _scalar_int(arrays["K"], "probability cache K", 2, 512)
    _scalar_int(arrays["block"], "probability cache block", 1, 100_000)
    probs = _probabilities(arrays["probs"], classes, "probability cache probs")
    gold = _labels(arrays["y_gold"], classes, "probability cache y_gold") if require_gold else None
    if gold is not None and len(gold) != len(probs):
        raise DistillationError("teacher test probabilities and labels differ in length")
    return probs, classes, gold, digest


def _synth_feature_cache(
    root: Path, relative_path: str, block: int, classes: int, total_bytes: list[int],
) -> tuple[np.ndarray, str]:
    arrays, digest = _read_npz(root, relative_path, {"H", "block", "K"}, {"H", "block", "K"}, total_bytes)
    hidden = _features(arrays["H"], "synthetic feature cache H")
    actual_block = _scalar_int(arrays["block"], "synthetic feature cache block", 1, 100_000)
    actual_classes = _scalar_int(arrays["K"], "synthetic feature cache K", 2, 512)
    if hidden.ndim != 2 or actual_block != block or actual_classes != classes:
        raise DistillationError("synthetic feature cache does not match the selected block or question")
    return hidden, digest


def _softmax(values: np.ndarray) -> np.ndarray:
    values = np.asarray(values, dtype=np.float64)
    values = values - values.max(axis=1, keepdims=True)
    exp = np.exp(values)
    return exp / exp.sum(axis=1, keepdims=True)


def _ridge_targets(features: np.ndarray, targets: np.ndarray, regularization: float) -> tuple[np.ndarray, np.ndarray]:
    feature_mean = features.mean(axis=0)
    target_mean = targets.mean(axis=0)
    centered_features = features - feature_mean
    centered_targets = targets - target_mean
    gram = centered_features @ centered_features.T + regularization * np.eye(len(centered_features))
    weights = centered_features.T @ np.linalg.solve(gram, centered_targets)
    bias = target_mean - feature_mean @ weights
    return weights, bias


def _fit_soft_head(
    features: np.ndarray,
    targets: np.ndarray,
    lambdas: tuple[float, ...] = (1.0, 10.0, 100.0, 1000.0),
    n_folds: int = 5,
    seed: int = 0,
) -> LinearHead:
    features = np.asarray(features, dtype=np.float64)
    targets = np.asarray(targets, dtype=np.float64)
    folds = _folds(len(targets), n_folds, seed)
    best: tuple[float, float, float] | None = None
    indices = np.arange(len(targets))
    for regularization in lambdas:
        oof = np.zeros_like(targets)
        for fold in folds:
            train = np.setdiff1d(indices, fold)
            mean, scale = _standardise(features[train])
            weights, bias = _ridge_targets((features[train] - mean) / scale, targets[train], regularization)
            oof[fold] = ((features[fold] - mean) / scale) @ weights + bias
        scaler = TemperatureScaler.fit(_softmax(oof), targets.argmax(axis=1))
        calibrated = scaler.apply(_softmax(oof))
        cross_entropy = float(-np.mean(np.sum(targets * np.log(np.clip(calibrated, EPS, None)), axis=1)))
        if best is None or cross_entropy < best[0]:
            best = (cross_entropy, regularization, scaler.temperature)
    if best is None:
        raise DistillationError("soft-target head fitting failed")
    cross_entropy, regularization, temperature = best
    mean, scale = _standardise(features)
    weights, bias = _ridge_targets((features - mean) / scale, targets, regularization)
    return LinearHead(kind="ridge_soft", layer=0, W=weights, b=bias, mean=mean, scale=scale,
                      temperature=temperature, params={"lam": regularization}, n_calib=len(targets),
                      cv={"oof_ce": cross_entropy, "oof_acc": float("nan")})


def _best_hard_head(features: np.ndarray, labels: np.ndarray, classes: int) -> LinearHead:
    candidates: list[LinearHead] = []
    for kind in ("lda", "ridge"):
        try:
            candidates.append(fit_head(features, labels, classes, kind=kind))
        except (ValueError, np.linalg.LinAlgError):
            continue
    if not candidates:
        raise DistillationError("hard-target head fitting failed for all supported methods")
    return min(candidates, key=lambda head: head.cv.get("oof_nll", math.inf))


def _predict_metrics(probabilities: np.ndarray, labels: np.ndarray) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    confidence = probabilities.max(axis=1)
    correct = (probabilities.argmax(axis=1) == labels).astype(np.float64)
    nll = -np.log(np.clip(probabilities[np.arange(len(labels)), labels], EPS, None))
    return confidence, correct, nll


def _ece(confidence: np.ndarray, correct: np.ndarray, bins: int = 15) -> float:
    order = np.argsort(confidence)
    ordered_confidence, ordered_correct = confidence[order], correct[order]
    total = 0.0
    for chunk in np.array_split(np.arange(len(confidence)), min(bins, len(confidence))):
        if len(chunk):
            total += len(chunk) / len(confidence) * abs(
                float(ordered_confidence[chunk].mean()) - float(ordered_correct[chunk].mean()))
    return total


def _series_result(
    confidence: np.ndarray,
    correct: np.ndarray,
    nll: np.ndarray,
    acc_ci: list[float],
    diff_ci: list[float] | None,
    baseline_name: str | None = None,
) -> dict[str, Any]:
    result: dict[str, Any] = {
        "accuracy": float(correct.mean()),
        "expected_calibration_error": _ece(confidence, correct),
        "negative_log_loss": float(nll.mean()),
        "examples": int(len(correct)),
        "accuracy_ci95": acc_ci,
    }
    if diff_ci is not None and baseline_name is not None:
        result[f"diff_vs_{baseline_name}_ci95"] = diff_ci
    return result


def _aggregate_student(
    series: dict[str, list[tuple[np.ndarray, np.ndarray, np.ndarray]]], resamples: int, baseline_name: str,
):
    concatenated = {
        name: tuple(np.concatenate([item[index] for item in items]) for index in range(3))
        for name, items in series.items()
    }
    if baseline_name not in concatenated or "teacher" not in concatenated:
        raise DistillationError("distillation aggregate is missing its teacher or gold baseline")
    baseline = concatenated[baseline_name][1]
    total = len(baseline)
    if total < 2:
        raise DistillationError("distillation evaluation needs at least two test examples")
    rng = np.random.RandomState(0)
    bootstrap_accuracy: dict[str, list[float]] = {name: [] for name in concatenated}
    bootstrap_difference: dict[str, list[float]] = {name: [] for name in concatenated if name != "teacher"}
    for _ in range(resamples):
        sample = rng.randint(0, total, total)
        baseline_sample = baseline[sample]
        for name, (_, correct, _) in concatenated.items():
            bootstrap_accuracy[name].append(float(correct[sample].mean()))
            if name != "teacher":
                bootstrap_difference[name].append(float((correct[sample] - baseline_sample).mean()))
    result: dict[str, dict[str, Any]] = {}
    for name, (confidence, correct, nll) in concatenated.items():
        acc_ci = np.percentile(bootstrap_accuracy[name], [2.5, 97.5]).astype(float).tolist()
        diff_ci = None
        if name != "teacher":
            diff_ci = np.percentile(bootstrap_difference[name], [2.5, 97.5]).astype(float).tolist()
        result[name] = _series_result(confidence, correct, nll, acc_ci, diff_ci, baseline_name)
    return result


def run_distillation_fit(params: dict[str, Any]) -> dict[str, Any]:
    """CPU-only fit/replay stage. Reads only explicitly named local NPZ caches."""
    if not isinstance(params, dict):
        raise DistillationError("distillation parameters must be an object")
    _only_fields(params, {"cache_root", "teacher_model", "calibration_examples", "budgets", "bootstrap_resamples", "questions"},
                 "distillation parameters")
    root = _real_cache_root(params.get("cache_root"))
    teacher_model = params.get("teacher_model")
    if (not isinstance(teacher_model, str) or not teacher_model.strip() or len(teacher_model) > 512
            or "\x00" in teacher_model):
        raise DistillationError("teacher_model must be a bounded non-empty identifier")
    budgets = params.get("budgets", [300, 1200])
    if (not isinstance(budgets, list) or not 1 <= len(budgets) <= 16
            or any(isinstance(item, bool) or not isinstance(item, int) or item < 1 or item > MAX_FIT_EXAMPLES for item in budgets)
            or budgets != sorted(set(budgets))):
        raise DistillationError("budgets must be unique increasing integers from 1 through 10000")
    calibration_examples = params.get("calibration_examples", 300)
    if (isinstance(calibration_examples, bool) or not isinstance(calibration_examples, int)
            or calibration_examples < 8 or calibration_examples > MAX_FIT_EXAMPLES):
        raise DistillationError("calibration_examples must be from 8 through 5000")
    baseline_name = f"gold{calibration_examples}"
    resamples = params.get("bootstrap_resamples", 1000)
    if (isinstance(resamples, bool) or not isinstance(resamples, int)
            or resamples < 100 or resamples > MAX_BOOTSTRAP_RESAMPLES):
        raise DistillationError("bootstrap_resamples must be from 100 through 10000")
    questions = params.get("questions")
    if not isinstance(questions, list) or not 1 <= len(questions) <= MAX_QUESTIONS:
        raise DistillationError("questions must contain from 1 through 256 cache groups")

    total_bytes = [0]
    seen_questions: set[str] = set()
    student_models: list[tuple[str, int, int]] | None = None
    student_series: dict[str, dict[str, list[tuple[np.ndarray, np.ndarray, np.ndarray]]]] = {}
    teacher_series: list[tuple[np.ndarray, np.ndarray, np.ndarray]] = []
    question_results: list[dict[str, Any]] = []
    input_digests: list[dict[str, str]] = []
    total_test_items = 0

    for question in questions:
        if not isinstance(question, dict):
            raise DistillationError("each question cache entry must be an object")
        _only_fields(question, {"question_id", "teacher_synth_path", "teacher_test_path", "students"},
                     "question cache entry")
        question_id = question.get("question_id")
        if not isinstance(question_id, str) or not question_id.strip() or len(question_id) > 512:
            raise DistillationError("question_id must be a bounded non-empty identifier")
        q_digest = _sha256_text(question_id)
        if q_digest in seen_questions:
            raise DistillationError("question identifiers must be unique")
        seen_questions.add(q_digest)
        student_inputs = question.get("students")
        if not isinstance(student_inputs, list) or not 1 <= len(student_inputs) <= MAX_STUDENTS:
            raise DistillationError("each question must include 1 through 8 student cache groups")
        current_models: list[tuple[str, int, int]] = []
        normalized_students: list[tuple[dict[str, Any], str]] = []
        seen_student_hashes: set[str] = set()
        for student in student_inputs:
            if not isinstance(student, dict):
                raise DistillationError("student cache group must be an object")
            _only_fields(student, {"model", "block", "n_layers", "gold_train_path", "gold_test_path",
                                   "synth_features_path"}, "student cache group")
            model = student.get("model")
            block, n_layers = student.get("block"), student.get("n_layers")
            if not isinstance(model, str) or not model.strip() or len(model) > 512:
                raise DistillationError("student model must be a bounded non-empty identifier")
            if (isinstance(block, bool) or not isinstance(block, int) or block < 1 or block > 100_000
                    or isinstance(n_layers, bool) or not isinstance(n_layers, int) or n_layers < block or n_layers > 100_000):
                raise DistillationError("student block and n_layers are invalid")
            model_digest = _sha256_text(model)
            if model_digest in seen_student_hashes:
                raise DistillationError("student model identifiers must be unique per question")
            seen_student_hashes.add(model_digest)
            current_models.append((model_digest, block, n_layers))
            normalized_students.append((student, model_digest))
        current_signature = sorted(current_models)
        if student_models is None:
            student_models = current_signature
            for model_digest, block, _ in student_models:
                student_series[model_digest] = {"teacher": []}
        elif current_signature != student_models:
            raise DistillationError("every question must contain the same student identifiers and blocks")

        teacher_synth, synth_classes, _, synth_digest = _prob_cache(
            root, question.get("teacher_synth_path"), False, total_bytes)
        teacher_test, test_classes, teacher_gold, teacher_test_digest = _prob_cache(
            root, question.get("teacher_test_path"), True, total_bytes)
        if synth_classes != test_classes or teacher_gold is None:
            raise DistillationError("teacher synthetic/test caches have different class counts")
        classes = synth_classes
        teacher_series.append(_predict_metrics(teacher_test, teacher_gold))
        total_test_items += len(teacher_gold)
        q_result: dict[str, Any] = {"question_sha256": q_digest, "examples": len(teacher_gold), "students": []}
        input_digests.extend([
            {"role": "teacher_synthetic_probabilities", "question_sha256": q_digest, "sha256": synth_digest},
            {"role": "teacher_test_probabilities", "question_sha256": q_digest, "sha256": teacher_test_digest},
        ])

        for student, model_digest in normalized_students:
            block = student["block"]
            gold_train, train_labels, train_classes, gold_train_digest = _feature_cache(
                root, student.get("gold_train_path"), block, student["n_layers"], total_bytes)
            gold_test, test_labels, test_feature_classes, gold_test_digest = _feature_cache(
                root, student.get("gold_test_path"), block, student["n_layers"], total_bytes)
            synthetic_features, synthetic_digest = _synth_feature_cache(
                root, student.get("synth_features_path"), block, classes, total_bytes)
            if (train_classes != classes or test_feature_classes != classes or len(test_labels) != len(teacher_gold)
                    or not np.array_equal(test_labels, teacher_gold) or len(gold_train) != len(train_labels)
                    or len(gold_test) != len(test_labels)):
                raise DistillationError("student feature caches do not reconcile with teacher classes or test labels")
            if len(synthetic_features) != len(teacher_synth):
                raise DistillationError("student synthetic features and teacher labels must align exactly")
            if len(train_labels) != calibration_examples or len(train_labels) < 2 * classes:
                raise DistillationError("gold calibration cache count differs from calibration_examples")
            if gold_train.shape[1] != gold_test.shape[1] or gold_train.shape[1] != synthetic_features.shape[1]:
                raise DistillationError("student gold and synthetic feature widths differ")
            if len(test_labels) < 2:
                raise DistillationError("test cache needs at least two held-out examples")
            input_digests.extend([
                {"role": "student_gold_train_features", "question_sha256": q_digest,
                 "model_sha256": model_digest, "sha256": gold_train_digest},
                {"role": "student_gold_test_features", "question_sha256": q_digest,
                 "model_sha256": model_digest, "sha256": gold_test_digest},
                {"role": "student_synthetic_features", "question_sha256": q_digest,
                 "model_sha256": model_digest, "sha256": synthetic_digest},
            ])

            synthetic_count = len(teacher_synth)
            effective_budgets = [min(requested, synthetic_count) for requested in budgets]
            if len(set(effective_budgets)) != len(effective_budgets):
                raise DistillationError("requested budgets collapse to duplicate available sample counts")
            variants: dict[str, np.ndarray] = {
                baseline_name: _best_hard_head(gold_train, train_labels, classes).probs(gold_test)
            }
            for count in effective_budgets:
                features = synthetic_features[:count]
                targets = teacher_synth[:count]
                variants[f"synth{count}-hard"] = _best_hard_head(features, targets.argmax(axis=1), classes).probs(gold_test)
                variants[f"synth{count}-soft"] = _fit_soft_head(features, targets).probs(gold_test)
                combined_features = np.concatenate([gold_train, features], axis=0)
                variants[f"{baseline_name}+synth{count}-hard"] = _best_hard_head(
                    combined_features, np.concatenate([train_labels, targets.argmax(axis=1)]), classes).probs(gold_test)
                combined_targets = np.concatenate([np.eye(classes)[train_labels], targets], axis=0)
                variants[f"{baseline_name}+synth{count}-soft"] = _fit_soft_head(combined_features, combined_targets).probs(gold_test)

            variant_series = student_series[model_digest]
            teacher_metric = _predict_metrics(teacher_test, teacher_gold)
            variant_series["teacher"].append(teacher_metric)
            per_question_methods: dict[str, dict[str, Any]] = {
                "teacher": {"accuracy": float(teacher_metric[1].mean()), "examples": len(teacher_metric[1])}
            }
            for method, probabilities in variants.items():
                metrics = _predict_metrics(probabilities, test_labels)
                variant_series.setdefault(method, []).append(metrics)
                per_question_methods[method] = {"accuracy": float(metrics[1].mean()), "examples": len(metrics[1])}
            q_result["students"].append({
                "model_sha256": model_digest,
                "block": block,
                "methods": per_question_methods,
                "budgets_used": effective_budgets,
            })
        if total_test_items > MAX_TOTAL_TEST_ITEMS:
            raise DistillationError("combined held-out example count exceeds the limit")
        question_results.append(q_result)

    if student_models is None:
        raise DistillationError("distillation request has no student models")
    # Source AnyJev reports compute each student's CIs with a fresh RandomState(0).
    rows: list[dict[str, Any]] = []
    for model_digest, block, _ in student_models:
        series = student_series[model_digest]
        # The teacher series is identical across students; use the row populated alongside this student's caches.
        aggregate = _aggregate_student(series, resamples, baseline_name)
        rows.append({"student_sha256": model_digest, "block": block, "methods": aggregate})
    teacher_confidence, teacher_correct, teacher_nll = (
        np.concatenate([item[index] for item in teacher_series]) for index in range(3)
    )
    rng = np.random.RandomState(0)
    teacher_bootstrap = [float(teacher_correct[rng.randint(0, total_test_items, total_test_items)].mean())
                         for _ in range(resamples)]
    teacher_overall = _series_result(
        teacher_confidence, teacher_correct, teacher_nll,
        np.percentile(teacher_bootstrap, [2.5, 97.5]).astype(float).tolist(), None,
    )
    return {
        "schema_version": "threadforge.anyjev-distillation-fit-result.v1",
        "teacher_sha256": _sha256_text(teacher_model),
        "calibration_examples": calibration_examples,
        "budgets": budgets,
        "bootstrap_resamples": resamples,
        "question_count": len(question_results),
        "total_test_examples": total_test_items,
        "teacher": teacher_overall,
        "students": rows,
        "per_question": question_results,
        "input_digests": input_digests,
        "human_review_required": True,
        "model_inference_verified": False,
        "execution_verified": False,
        "cutover_evidence_eligible": False,
    }


def _teacher_oof_probabilities(
    train_features: np.ndarray, train_labels: np.ndarray, test_features: np.ndarray, classes: int,
) -> tuple[np.ndarray, np.ndarray]:
    count = len(train_labels)
    folds = np.array_split(np.random.RandomState(0).permutation(count), min(5, count))
    indices = np.arange(count)
    oof = np.zeros((count, classes), dtype=np.float64)
    for fold in folds:
        fit_indices = np.setdiff1d(indices, fold)
        head = _best_hard_head(train_features[fit_indices], train_labels[fit_indices], classes)
        oof[fold] = head.probs(train_features[fold])
    final_head = _best_hard_head(train_features, train_labels, classes)
    return oof, final_head.probs(test_features)


def run_distillation_soft(params: dict[str, Any]) -> dict[str, Any]:
    """Same-state OOF teacher-to-student replay over local feature caches."""
    if not isinstance(params, dict):
        raise DistillationError("distillation parameters must be an object")
    _only_fields(params, {"cache_root", "teacher_model", "teacher_block", "teacher_n_layers",
                          "bootstrap_resamples", "questions"}, "soft distillation parameters")
    root = _real_cache_root(params.get("cache_root"))
    teacher_model = params.get("teacher_model")
    teacher_block, teacher_n_layers = params.get("teacher_block"), params.get("teacher_n_layers")
    if not isinstance(teacher_model, str) or not teacher_model.strip() or len(teacher_model) > 512:
        raise DistillationError("teacher_model must be a bounded non-empty identifier")
    if (isinstance(teacher_block, bool) or not isinstance(teacher_block, int) or teacher_block < 1
            or isinstance(teacher_n_layers, bool) or not isinstance(teacher_n_layers, int)
            or teacher_n_layers < teacher_block or teacher_n_layers > 100_000):
        raise DistillationError("teacher block and n_layers are invalid")
    resamples = params.get("bootstrap_resamples", 1000)
    if isinstance(resamples, bool) or not isinstance(resamples, int) or resamples < 100 or resamples > MAX_BOOTSTRAP_RESAMPLES:
        raise DistillationError("bootstrap_resamples must be from 100 through 10000")
    questions = params.get("questions")
    if not isinstance(questions, list) or not 1 <= len(questions) <= MAX_QUESTIONS:
        raise DistillationError("questions must contain from 1 through 256 cache groups")

    total_bytes = [0]
    seen_questions: set[str] = set()
    student_models: list[tuple[str, int, int]] | None = None
    student_series: dict[str, dict[str, list[tuple[np.ndarray, np.ndarray, np.ndarray]]]] = {}
    teacher_series: list[tuple[np.ndarray, np.ndarray, np.ndarray]] = []
    per_question: list[dict[str, Any]] = []
    input_digests: list[dict[str, str]] = []
    total_test_examples = 0

    for question in questions:
        if not isinstance(question, dict):
            raise DistillationError("each soft distillation question must be an object")
        _only_fields(question, {"question_id", "teacher_train_path", "teacher_test_path", "students"},
                     "soft distillation question")
        question_id = question.get("question_id")
        if not isinstance(question_id, str) or not question_id.strip() or len(question_id) > 512:
            raise DistillationError("question_id must be a bounded non-empty identifier")
        question_sha = _sha256_text(question_id)
        if question_sha in seen_questions:
            raise DistillationError("question identifiers must be unique")
        seen_questions.add(question_sha)
        student_inputs = question.get("students")
        if not isinstance(student_inputs, list) or not 1 <= len(student_inputs) <= MAX_STUDENTS:
            raise DistillationError("each soft distillation question needs 1 through 8 students")

        normalized_students: list[tuple[dict[str, Any], str]] = []
        current_models: list[tuple[str, int, int]] = []
        for student in student_inputs:
            if not isinstance(student, dict):
                raise DistillationError("student cache group must be an object")
            _only_fields(student, {"model", "block", "n_layers", "gold_train_path", "gold_test_path"},
                         "soft distillation student")
            model, block, n_layers = student.get("model"), student.get("block"), student.get("n_layers")
            if not isinstance(model, str) or not model.strip() or len(model) > 512:
                raise DistillationError("student model must be a bounded non-empty identifier")
            if (isinstance(block, bool) or not isinstance(block, int) or block < 1
                    or isinstance(n_layers, bool) or not isinstance(n_layers, int) or n_layers < block
                    or n_layers > 100_000):
                raise DistillationError("student block and n_layers are invalid")
            model_sha = _sha256_text(model)
            current_models.append((model_sha, block, n_layers))
            normalized_students.append((student, model_sha))
        if len({item[0] for item in current_models}) != len(current_models):
            raise DistillationError("student model identifiers must be unique per question")
        current_signature = sorted(current_models)
        if student_models is None:
            student_models = current_signature
            student_series = {model_sha: {"teacher": []} for model_sha, _, _ in student_models}
        elif current_signature != student_models:
            raise DistillationError("every question must contain the same student identifiers and blocks")

        teacher_train, train_labels, classes, teacher_train_digest = _feature_cache(
            root, question.get("teacher_train_path"), teacher_block, teacher_n_layers, total_bytes)
        teacher_test, test_labels, test_classes, teacher_test_digest = _feature_cache(
            root, question.get("teacher_test_path"), teacher_block, teacher_n_layers, total_bytes)
        if classes != test_classes or len(test_labels) < 2 or len(train_labels) < 5 * max(8, 2 * classes) // 4:
            raise DistillationError("teacher feature caches have incompatible classes or insufficient cross-validation examples")
        if len(train_labels) - math.ceil(len(train_labels) / 5) < max(8, 2 * classes):
            raise DistillationError("teacher cross-validation folds are too small for head fitting")
        teacher_oof, teacher_test_probs = _teacher_oof_probabilities(
            teacher_train, train_labels, teacher_test, classes)
        teacher_metrics = _predict_metrics(teacher_test_probs, test_labels)
        teacher_series.append(teacher_metrics)
        total_test_examples += len(test_labels)
        if total_test_examples > MAX_TOTAL_TEST_ITEMS:
            raise DistillationError("combined held-out example count exceeds the limit")
        question_result: dict[str, Any] = {
            "question_sha256": question_sha,
            "examples": len(test_labels),
            "teacher_accuracy": float(teacher_metrics[1].mean()),
            "teacher_oof_agreement_with_gold": float((teacher_oof.argmax(axis=1) == train_labels).mean()),
            "students": [],
        }
        input_digests.extend([
            {"role": "teacher_train_features", "question_sha256": question_sha, "sha256": teacher_train_digest},
            {"role": "teacher_test_features", "question_sha256": question_sha, "sha256": teacher_test_digest},
        ])

        for student, model_sha in normalized_students:
            block = student["block"]
            features_train, labels_train, student_classes, train_digest = _feature_cache(
                root, student.get("gold_train_path"), block, student["n_layers"], total_bytes)
            features_test, labels_test, student_test_classes, test_digest = _feature_cache(
                root, student.get("gold_test_path"), block, student["n_layers"], total_bytes)
            if (student_classes != classes or student_test_classes != classes
                    or not np.array_equal(labels_train, train_labels)
                    or not np.array_equal(labels_test, test_labels)):
                raise DistillationError("teacher and student cache labels/classes do not align")
            if features_train.shape[1] != features_test.shape[1]:
                raise DistillationError("student train/test feature widths differ")
            variants = {
                "gold": _best_hard_head(features_train, labels_train, classes).probs(features_test),
                "teacher-argmax": _best_hard_head(features_train, teacher_oof.argmax(axis=1), classes).probs(features_test),
                "teacher-soft": _fit_soft_head(features_train, teacher_oof).probs(features_test),
                "gold+teacher-soft": _fit_soft_head(
                    features_train,
                    0.5 * np.eye(classes)[labels_train] + 0.5 * teacher_oof,
                ).probs(features_test),
            }
            series = student_series[model_sha]
            series["teacher"].append(teacher_metrics)
            per_question_methods: dict[str, dict[str, Any]] = {
                "teacher": {"accuracy": float(teacher_metrics[1].mean()), "examples": len(test_labels)}
            }
            for method, predictions in variants.items():
                values = _predict_metrics(predictions, labels_test)
                series.setdefault(method, []).append(values)
                per_question_methods[method] = {"accuracy": float(values[1].mean()), "examples": len(labels_test)}
            question_result["students"].append({
                "model_sha256": model_sha,
                "block": block,
                "methods": per_question_methods,
            })
            input_digests.extend([
                {"role": "student_gold_train_features", "question_sha256": question_sha,
                 "model_sha256": model_sha, "sha256": train_digest},
                {"role": "student_gold_test_features", "question_sha256": question_sha,
                 "model_sha256": model_sha, "sha256": test_digest},
            ])
        per_question.append(question_result)

    if student_models is None:
        raise DistillationError("soft distillation request has no students")
    students_result = []
    for model_sha, block, _ in student_models:
        methods = _aggregate_student(student_series[model_sha], resamples, "gold")
        students_result.append({"student_sha256": model_sha, "block": block, "methods": methods})
    teacher_confidence, teacher_correct, teacher_nll = (
        np.concatenate([item[index] for item in teacher_series]) for index in range(3)
    )
    rng = np.random.RandomState(0)
    teacher_bootstrap = [float(teacher_correct[rng.randint(0, total_test_examples, total_test_examples)].mean())
                         for _ in range(resamples)]
    teacher_summary = _series_result(
        teacher_confidence, teacher_correct, teacher_nll,
        np.percentile(teacher_bootstrap, [2.5, 97.5]).astype(float).tolist(), None,
    )
    return {
        "schema_version": "threadforge.anyjev-distillation-soft-result.v1",
        "teacher_sha256": _sha256_text(teacher_model),
        "teacher_block": teacher_block,
        "question_count": len(per_question),
        "total_test_examples": total_test_examples,
        "bootstrap_resamples": resamples,
        "teacher": teacher_summary,
        "students": students_result,
        "per_question": per_question,
        "input_digests": input_digests,
        "human_review_required": True,
        "model_inference_verified": False,
        "execution_verified": False,
        "cutover_evidence_eligible": False,
    }


def run_distillation_label(params: dict[str, Any]) -> dict[str, Any]:
    """Run a checkpoint-pinned local teacher over synthetic and held-out states."""
    from .protocol import (
        ProtocolError,
        _backend,
        _fingerprint_model_directory,
        _question,
        validate_decision_bundle,
    )

    if not isinstance(params, dict):
        raise DistillationError("distillation label parameters must be an object")
    allowed = {
        "data_root", "cache_root", "teacher_model", "teacher_model_checkpoint_sha256", "backend",
        "teacher_artifact", "workflows", "questions",
    }
    _only_fields(params, allowed, "distillation label parameters")
    _reject_credentials(params, "distillation label request")
    data_root = _real_cache_root(params.get("data_root"))
    cache_root = _real_cache_root(params.get("cache_root"))
    teacher_model = params.get("teacher_model")
    checkpoint_sha = params.get("teacher_model_checkpoint_sha256")
    if not isinstance(teacher_model, str) or not teacher_model.strip() or len(teacher_model) > 512:
        raise DistillationError("teacher_model must be a bounded non-empty identifier")
    if not isinstance(checkpoint_sha, str) or not re.fullmatch(r"[a-f0-9]{64}", checkpoint_sha):
        raise DistillationError("teacher model checkpoint fingerprint is required")

    backend_config = params.get("backend")
    if not isinstance(backend_config, dict) or backend_config.get("type", "hf") != "hf":
        raise DistillationError("distillation labeling requires the local HF backend")
    artifact = params.get("teacher_artifact")
    if not isinstance(artifact, dict) or not {"model", "artifacts", "heads"}.issubset(artifact):
        raise DistillationError("teacher_artifact must be a native AnyJev decision bundle")
    if set(artifact) - {"model", "artifacts", "heads", "adaptation", "observations"}:
        raise DistillationError("teacher_artifact contains unsupported fields")
    if artifact.get("adaptation") not in (None, {}) or artifact.get("observations") not in (None, {}):
        raise DistillationError("teacher_artifact cannot contain adaptation or labeled observations")
    safe_artifact = {key: artifact[key] for key in ("model", "artifacts", "heads")}
    if artifact.get("model") != teacher_model:
        raise DistillationError("teacher artifact model identity does not match the request")
    try:
        validate_decision_bundle({"bundle": safe_artifact})
    except ProtocolError as error:
        raise DistillationError("teacher_artifact failed native bundle validation") from error

    workflow_inputs = params.get("workflows")
    questions = params.get("questions")
    if not isinstance(workflow_inputs, list) or not 1 <= len(workflow_inputs) <= MAX_QUESTIONS:
        raise DistillationError("workflows must contain from 1 through 256 synthetic-state files")
    if not isinstance(questions, list) or not 1 <= len(questions) <= MAX_QUESTIONS:
        raise DistillationError("questions must contain from 1 through 256 test-case files")

    total_source_bytes = [0]
    workflows: dict[str, tuple[list[Any], str]] = {}
    total_unique_synth_states = 0
    for source in workflow_inputs:
        if not isinstance(source, dict):
            raise DistillationError("each workflow source must be an object")
        _only_fields(source, {"workflow", "synthetic_states_path"}, "workflow source")
        workflow = source.get("workflow")
        if not isinstance(workflow, str) or not _SAFE_WORKFLOW.fullmatch(workflow):
            raise DistillationError("workflow name is invalid")
        if workflow in workflows:
            raise DistillationError("workflow names must be unique")
        document, digest = _read_json_source(data_root, source.get("synthetic_states_path"), total_source_bytes)
        _only_fields(document, {
            "schema_version", "workflow", "states", "generator", "n", "generated", "rejected", "reject_reasons", "settings", "date", "env",
        }, "synthetic-state source")
        states = document.get("states")
        if (document.get("schema_version", "threadforge.anyjev-distillation-synthetic-states.v1")
                != "threadforge.anyjev-distillation-synthetic-states.v1"
                or document.get("workflow") != workflow or not isinstance(states, list)
                or not 1 <= len(states) <= MAX_FIT_EXAMPLES * 2):
            raise DistillationError("synthetic-state source identity or count is invalid")
        if "n" in document and (type(document["n"]) is not int or document["n"] != len(states)):
            raise DistillationError("synthetic-state source count does not reconcile")
        total_unique_synth_states += len(states)
        if total_unique_synth_states > MAX_TOTAL_DISTILLATION_STATES:
            raise DistillationError("synthetic-state source total exceeds the bound")
        workflows[workflow] = (states, digest)

    prepared: list[dict[str, Any]] = []
    seen_question_ids: set[str] = set()
    seen_question_keys: set[str] = set()
    total_test_examples = 0
    repeated_synth_examples = 0
    for item in questions:
        if not isinstance(item, dict):
            raise DistillationError("each distillation question must be an object")
        _only_fields(item, {"question_id", "workflow", "test_cases_path"}, "distillation question")
        question_id, workflow = item.get("question_id"), item.get("workflow")
        if not isinstance(question_id, str) or not question_id.strip() or len(question_id) > 512:
            raise DistillationError("question_id must be a bounded non-empty identifier")
        if not isinstance(workflow, str) or workflow not in workflows:
            raise DistillationError("question refers to an unknown workflow")
        question_sha = _sha256_text(question_id)
        if question_sha in seen_question_ids:
            raise DistillationError("question identifiers must be unique")
        seen_question_ids.add(question_sha)
        test_doc, test_digest = _read_json_source(data_root, item.get("test_cases_path"), total_source_bytes)
        _only_fields(test_doc, {"schema_version", "question_id", "question", "examples"}, "test-case source")
        if (test_doc.get("schema_version") != "threadforge.anyjev-distillation-test-cases.v1"
                or test_doc.get("question_id") != question_id):
            raise DistillationError("test-case source version or question identity does not match")
        try:
            question = _question(test_doc.get("question"))
        except ProtocolError:
            raise DistillationError("typed question definition is invalid") from None
        if question.key in seen_question_keys:
            raise DistillationError("question layouts must be unique")
        seen_question_keys.add(question.key)
        examples = test_doc.get("examples")
        if not isinstance(examples, list) or not 1 <= len(examples) <= MAX_FIT_EXAMPLES:
            raise DistillationError("test-case source must contain from 1 through 5000 examples")
        states: list[Any] = []
        labels: list[int] = []
        for example in examples:
            if not isinstance(example, dict) or set(example) != {"state", "gold_index"}:
                raise DistillationError("test-case entries must contain only state and gold_index")
            label = example.get("gold_index")
            if type(label) is not int or not 0 <= label < question.k:
                raise DistillationError("test-case gold_index is outside the question options")
            states.append(example["state"])
            labels.append(label)
        total_test_examples += len(states)
        repeated_synth_examples += len(workflows[workflow][0])
        if total_test_examples + repeated_synth_examples > MAX_TOTAL_DISTILLATION_STATES:
            raise DistillationError("combined distillation state count exceeds the bound")
        prepared.append({
            "question_id": question_id,
            "question_sha256": question_sha,
            "question": question,
            "workflow": workflow,
            "test_states": states,
            "test_labels": np.asarray(labels, dtype=np.int64),
            "test_source_sha256": test_digest,
        })

    try:
        backend = _backend(
            backend_config, expected_model_name=teacher_model,
            expected_model_checkpoint_sha256=checkpoint_sha,
        )
        if (getattr(backend, "name", None) != teacher_model
                or getattr(backend, "_threadforge_model_environment_verified", False) is not True
                or getattr(backend, "_threadforge_model_checkpoint_sha256", None) != checkpoint_sha):
            raise DistillationError("teacher checkpoint was not locally fingerprint-verified")
        from anyjev import Decider

        decider = Decider(backend, adapt=False)
        if decider.load_artifacts(safe_artifact) < 1:
            raise DistillationError("teacher artifact contains no usable decision heads")
        pending: list[tuple[str, dict[str, np.ndarray], dict[str, Any]]] = []
        for item in prepared:
            question = item["question"]
            workflow_states, workflow_digest = workflows[item["workflow"]]
            for split, states, labels, source_digest in (
                ("synth", workflow_states, None, workflow_digest),
                ("test", item["test_states"], item["test_labels"], item["test_source_sha256"]),
            ):
                decisions = decider.decide_batch(states, question, level="L2")
                if len(decisions) != len(states) or not decisions:
                    raise DistillationError("teacher produced an invalid decision count")
                probabilities = np.stack([np.asarray(decision.probs, dtype=np.float32) for decision in decisions])
                if (probabilities.shape != (len(states), question.k) or not np.all(np.isfinite(probabilities))
                        or np.any(probabilities < 0) or np.any(probabilities > 1)
                        or not np.allclose(probabilities.sum(axis=1), 1.0, rtol=0.0, atol=1e-4)):
                    raise DistillationError("teacher probabilities are malformed")
                blocks = [decision.diagnostics.get("exit_layer") for decision in decisions]
                if (any(type(block) is not int or block < 1 or block > 100_000 for block in blocks)
                        or len(set(blocks)) != 1):
                    raise DistillationError("teacher decisions do not share a valid head block")
                arrays = {"probs": probabilities, "K": np.asarray(question.k), "block": np.asarray(blocks[0])}
                role = "teacher_synthetic_probabilities" if split == "synth" else "teacher_test_probabilities"
                if labels is not None:
                    arrays["y_gold"] = labels
                relative_path = (
                    f"distillation/synth_labels/{checkpoint_sha}/"
                    f"{item['question_sha256']}.{split}.npz"
                )
                metadata = {
                    "role": role,
                    "question_sha256": item["question_sha256"],
                    "teacher_sha256": _sha256_text(teacher_model),
                    "teacher_checkpoint_sha256": checkpoint_sha,
                    "source_sha256": source_digest,
                    "examples": len(states),
                    "classes": question.k,
                    "block": blocks[0],
                }
                pending.append((relative_path, arrays, metadata))

        after = _fingerprint_model_directory(backend_config["model_path"])
        if after["sha256"] != checkpoint_sha:
            raise DistillationError("teacher checkpoint changed during distillation labeling")
        cache_results = []
        total_synth_examples = 0
        total_test_examples = 0
        for relative_path, arrays, metadata in pending:
            output_digest = _write_npz_exclusive(cache_root, relative_path, arrays)
            cache_results.append({**metadata, "relative_path": relative_path, "sha256": output_digest})
            if metadata["role"] == "teacher_synthetic_probabilities":
                total_synth_examples += metadata["examples"]
            else:
                total_test_examples += metadata["examples"]
    except DistillationError:
        raise
    except Exception as error:
        raise DistillationError(f"distillation labeling failed safely: {type(error).__name__}") from None

    return {
        "schema_version": "threadforge.anyjev-distillation-label-result.v1",
        "teacher_sha256": _sha256_text(teacher_model),
        "teacher_checkpoint_sha256": checkpoint_sha,
        "question_count": len(prepared),
        "total_synthetic_examples": total_synth_examples,
        "total_test_examples": total_test_examples,
        "caches": cache_results,
        "human_review_required": True,
        "model_inference_verified": True,
        "execution_verified": False,
        "cutover_evidence_eligible": False,
    }


def run_distillation_extract(params: dict[str, Any]) -> dict[str, Any]:
    """Extract one student's hidden states for each synthetic workflow/question pair."""
    from .protocol import ProtocolError, _backend, _fingerprint_model_directory, _question

    if not isinstance(params, dict):
        raise DistillationError("distillation extract parameters must be an object")
    _only_fields(params, {
        "data_root", "cache_root", "student_model", "student_model_checkpoint_sha256", "backend",
        "block", "seed", "workflows", "questions",
    }, "distillation extract parameters")
    _reject_credentials(params, "distillation extract request")
    data_root = _real_cache_root(params.get("data_root"))
    cache_root = _real_cache_root(params.get("cache_root"))
    student_model = params.get("student_model")
    checkpoint_sha = params.get("student_model_checkpoint_sha256")
    if not isinstance(student_model, str) or not student_model.strip() or len(student_model) > 512:
        raise DistillationError("student_model must be a bounded non-empty identifier")
    if not isinstance(checkpoint_sha, str) or not re.fullmatch(r"[a-f0-9]{64}", checkpoint_sha):
        raise DistillationError("student model checkpoint fingerprint is required")
    block = params.get("block")
    if type(block) is not int or not 1 <= block <= 100_000:
        raise DistillationError("student block must be an integer from 1 through 100000")
    seed = params.get("seed", 0)
    if type(seed) is not int or not 0 <= seed <= 2**32 - 1:
        raise DistillationError("seed must be an unsigned 32-bit integer")
    backend_config = params.get("backend")
    if not isinstance(backend_config, dict) or backend_config.get("type", "hf") != "hf":
        raise DistillationError("distillation feature extraction requires the local HF backend")
    workflow_inputs = params.get("workflows")
    question_inputs = params.get("questions")
    if not isinstance(workflow_inputs, list) or not 1 <= len(workflow_inputs) <= MAX_QUESTIONS:
        raise DistillationError("workflows must contain from 1 through 256 synthetic-state files")
    if not isinstance(question_inputs, list) or not 1 <= len(question_inputs) <= MAX_QUESTIONS:
        raise DistillationError("questions must contain from 1 through 256 question definitions")

    total_source_bytes = [0]
    workflows: dict[str, tuple[list[Any], str]] = {}
    total_unique_states = 0
    for source in workflow_inputs:
        if not isinstance(source, dict):
            raise DistillationError("each workflow source must be an object")
        _only_fields(source, {"workflow", "synthetic_states_path"}, "workflow source")
        workflow = source.get("workflow")
        if not isinstance(workflow, str) or not _SAFE_WORKFLOW.fullmatch(workflow) or workflow in workflows:
            raise DistillationError("workflow name is invalid or duplicated")
        document, digest = _read_json_source(data_root, source.get("synthetic_states_path"), total_source_bytes)
        _only_fields(document, {
            "schema_version", "workflow", "states", "generator", "n", "generated", "rejected", "reject_reasons", "settings", "date", "env",
        }, "synthetic-state source")
        states = document.get("states")
        if (document.get("schema_version", "threadforge.anyjev-distillation-synthetic-states.v1")
                != "threadforge.anyjev-distillation-synthetic-states.v1"
                or document.get("workflow") != workflow or not isinstance(states, list)
                or not 1 <= len(states) <= MAX_FIT_EXAMPLES * 2):
            raise DistillationError("synthetic-state source identity or count is invalid")
        if "n" in document and (type(document["n"]) is not int or document["n"] != len(states)):
            raise DistillationError("synthetic-state source count does not reconcile")
        total_unique_states += len(states)
        if total_unique_states > MAX_TOTAL_DISTILLATION_STATES:
            raise DistillationError("synthetic-state source total exceeds the bound")
        workflows[workflow] = (states, digest)

    prepared: list[dict[str, Any]] = []
    seen_question_ids: set[str] = set()
    seen_question_keys: set[str] = set()
    total_feature_rows = 0
    for item in question_inputs:
        if not isinstance(item, dict):
            raise DistillationError("each extraction question must be an object")
        _only_fields(item, {"question_id", "workflow", "question"}, "extraction question")
        question_id, workflow = item.get("question_id"), item.get("workflow")
        if not isinstance(question_id, str) or not question_id.strip() or len(question_id) > 512:
            raise DistillationError("question_id must be a bounded non-empty identifier")
        if not isinstance(workflow, str) or workflow not in workflows:
            raise DistillationError("question refers to an unknown workflow")
        question_sha = _sha256_text(question_id)
        if question_sha in seen_question_ids:
            raise DistillationError("question identifiers must be unique")
        seen_question_ids.add(question_sha)
        try:
            question = _question(item.get("question"))
        except ProtocolError:
            raise DistillationError("typed question definition is invalid") from None
        if question.key in seen_question_keys:
            raise DistillationError("question layouts must be unique")
        seen_question_keys.add(question.key)
        states, source_digest = workflows[workflow]
        total_feature_rows += len(states)
        if total_feature_rows > MAX_TOTAL_DISTILLATION_STATES:
            raise DistillationError("combined synthetic feature rows exceed the bound")
        prepared.append({
            "question_id": question_id,
            "question_sha256": question_sha,
            "question": question,
            "workflow": workflow,
            "source_sha256": source_digest,
            "states": states,
        })

    try:
        backend = _backend(
            backend_config, expected_model_name=student_model,
            expected_model_checkpoint_sha256=checkpoint_sha,
        )
        if (getattr(backend, "name", None) != student_model
                or getattr(backend, "_threadforge_model_environment_verified", False) is not True
                or getattr(backend, "_threadforge_model_checkpoint_sha256", None) != checkpoint_sha):
            raise DistillationError("student checkpoint was not locally fingerprint-verified")
        from anyjev import Decider
        from anyjev.state import render_state

        decider = Decider(backend, adapt=False)
        pending: list[tuple[str, dict[str, np.ndarray], dict[str, Any]]] = []
        for item in prepared:
            question = item["question"]
            states = item["states"]
            rng = np.random.RandomState(seed)
            perms = None if question.ordered else [list(rng.permutation(question.k)) for _ in states]
            state_texts = [render_state(state) for state in states]
            features = np.asarray(decider._features(question, state_texts, [block], perms), dtype=np.float32)
            if (features.ndim != 3 or features.shape[0] != len(states) or features.shape[1] != 1
                    or not 1 <= features.shape[2] <= MAX_FEATURE_WIDTH or features.size > MAX_ARRAY_ELEMENTS
                    or not np.all(np.isfinite(features))):
                raise DistillationError("student feature extraction returned an invalid shape or value")
            hidden = features[:, 0, :].astype(np.float16)
            if not np.all(np.isfinite(hidden)):
                raise DistillationError("student features cannot be represented in the NPZ cache dtype")
            relative_path = (
                f"distillation/synth_features/{checkpoint_sha}/"
                f"{item['question_sha256']}.synth.npz"
            )
            arrays = {
                "H": hidden,
                "block": np.asarray(block),
                "K": np.asarray(question.k),
            }
            pending.append((relative_path, arrays, {
                "role": "student_synthetic_features",
                "question_sha256": item["question_sha256"],
                "student_sha256": _sha256_text(student_model),
                "student_checkpoint_sha256": checkpoint_sha,
                "source_sha256": item["source_sha256"],
                "examples": len(states),
                "classes": question.k,
                "block": block,
                "feature_width": int(hidden.shape[1]),
            }))
        after = _fingerprint_model_directory(backend_config["model_path"])
        if after["sha256"] != checkpoint_sha:
            raise DistillationError("student checkpoint changed during feature extraction")
        caches = []
        for relative_path, arrays, metadata in pending:
            output_digest = _write_npz_exclusive(cache_root, relative_path, arrays)
            caches.append({**metadata, "relative_path": relative_path, "sha256": output_digest})
    except DistillationError:
        raise
    except Exception as error:
        raise DistillationError(f"distillation feature extraction failed safely: {type(error).__name__}") from None

    return {
        "schema_version": "threadforge.anyjev-distillation-extract-result.v1",
        "student_sha256": _sha256_text(student_model),
        "student_checkpoint_sha256": checkpoint_sha,
        "question_count": len(prepared),
        "total_synthetic_examples": total_feature_rows,
        "caches": caches,
        "human_review_required": True,
        "model_inference_verified": True,
        "execution_verified": False,
        "cutover_evidence_eligible": False,
    }


def _state_matches_example_shape(candidate: Any, example: Any) -> bool:
    if isinstance(example, dict):
        return (isinstance(candidate, dict) and set(candidate) == set(example)
                and all(_state_matches_example_shape(candidate[key], example[key]) for key in example))
    if isinstance(example, list):
        if not isinstance(candidate, list) or not example:
            return isinstance(candidate, list)
        return all(any(_state_matches_example_shape(item, sample) for sample in example) for item in candidate)
    if type(example) is float:
        return type(candidate) in (int, float) and math.isfinite(candidate)
    return type(candidate) is type(example)


def _parse_generated_state(text: Any, exemplar_states: list[dict[str, Any]]) -> tuple[dict[str, Any] | None, str]:
    if not isinstance(text, str) or len(text) > 1 << 20:
        return None, "oversized"
    candidate_text = text.strip()
    if "```" in candidate_text:
        parts = candidate_text.split("```")
        candidate_text = max(parts, key=len).strip()
        if candidate_text.startswith("json"):
            candidate_text = candidate_text[4:].strip()
    start, end = candidate_text.find("{"), candidate_text.rfind("}")
    if start < 0 or end <= start:
        return None, "no_json"

    def unique_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("duplicate JSON key")
            result[key] = value
        return result

    def reject_constant(_value: str) -> None:
        raise ValueError("non-finite JSON constant")

    try:
        candidate = json.loads(candidate_text[start:end + 1], object_pairs_hook=unique_pairs,
                               parse_constant=reject_constant)
    except ValueError:
        if candidate_text.count("{") > candidate_text.count("}"):
            return None, "truncated"
        return None, "invalid_json"
    if not isinstance(candidate, dict):
        return None, "not_object"
    if not any(set(candidate) == set(example) for example in exemplar_states):
        return None, "keys"
    if not any(_state_matches_example_shape(candidate, example) for example in exemplar_states):
        return None, "shape"
    try:
        _reject_credentials(candidate, "generated synthetic state")
    except DistillationError:
        return None, "sensitive"
    return candidate, ""


def _synthetic_prompt(tokenizer: Any, workflow: str, exemplars: list[dict[str, Any]]) -> str:
    from anyjev.state import render_state

    prompt = (
        f"You write realistic test cases for a {workflow.replace('_', ' ')} decision system. Below are "
        f"{len(exemplars)} real cases, each a JSON object. Write ONE new case with exactly the same JSON "
        "schema (same keys, same nesting, same value types). Make it a different situation: different "
        "people, products, numbers, dates, wording and outcome. Cover the whole range of what happens "
        "in practice, including ambiguous or borderline situations. Output only the JSON object, nothing else.\n\n"
    )
    for index, example in enumerate(exemplars, 1):
        prompt += f"Case {index}:\n{render_state(example)}\n\n"
    prompt += "New case:"
    messages = [{"role": "user", "content": prompt}]
    try:
        return tokenizer.apply_chat_template(
            messages, tokenize=False, add_generation_prompt=True, enable_thinking=False,
        )
    except TypeError:
        return tokenizer.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)


def _generate_synthetic_batch(
    prompts: list[str], backend: Any, max_new_tokens: int, temperature: float, top_p: float, seed: int,
) -> list[str]:
    import torch

    tokenizer = backend.tokenizer
    model = backend.model
    tokenizer.padding_side = "left"
    if tokenizer.pad_token is None:
        tokenizer.pad_token = tokenizer.eos_token
    encoded = tokenizer(prompts, return_tensors="pt", padding=True, truncation=False)
    if int(encoded["input_ids"].shape[-1]) > 4096:
        raise DistillationError("synthetic generation prompt exceeds the 4096-token limit")
    device = getattr(backend, "device", "cpu")
    encoded = {key: value.to(device) for key, value in encoded.items()}
    generator = torch.Generator(device=device)
    generator.manual_seed(seed)
    with torch.inference_mode():
        generated = model.generate(
            **encoded,
            do_sample=True,
            temperature=temperature,
            top_p=top_p,
            max_new_tokens=max_new_tokens,
            pad_token_id=tokenizer.pad_token_id,
            eos_token_id=tokenizer.eos_token_id,
            generator=generator,
        )
    prompt_width = int(encoded["input_ids"].shape[-1])
    return tokenizer.batch_decode(generated[:, prompt_width:], skip_special_tokens=True)


def run_distillation_synth(params: dict[str, Any]) -> dict[str, Any]:
    """Generate schema-shaped synthetic workflow states with a pinned local model."""
    from .protocol import _backend, _fingerprint_model_directory

    if not isinstance(params, dict):
        raise DistillationError("distillation synth parameters must be an object")
    _only_fields(params, {
        "source_root", "cache_root", "generator_model", "generator_checkpoint_sha256", "backend",
        "per_workflow", "batch_size", "max_new_tokens", "temperature", "top_p", "seed",
        "max_attempts", "workflows",
    }, "distillation synth parameters")
    _reject_credentials(params, "distillation synth request")
    source_root = _real_cache_root(params.get("source_root"))
    cache_root = _real_cache_root(params.get("cache_root"))
    generator_model = params.get("generator_model")
    checkpoint_sha = params.get("generator_checkpoint_sha256")
    if not isinstance(generator_model, str) or not generator_model.strip() or len(generator_model) > 512:
        raise DistillationError("generator_model must be a bounded non-empty identifier")
    if not isinstance(checkpoint_sha, str) or not re.fullmatch(r"[a-f0-9]{64}", checkpoint_sha):
        raise DistillationError("generator checkpoint fingerprint is required")
    per_workflow = params.get("per_workflow", 1200)
    batch_size = params.get("batch_size", 16)
    max_new_tokens = params.get("max_new_tokens", 700)
    temperature = params.get("temperature", 1.0)
    top_p = params.get("top_p", 0.95)
    seed = params.get("seed", 0)
    max_attempts = params.get("max_attempts", min(100_000, per_workflow * 10) if type(per_workflow) is int else 0)
    if type(per_workflow) is not int or not 1 <= per_workflow <= MAX_FIT_EXAMPLES:
        raise DistillationError("per_workflow must be an integer from 1 through 5000")
    if type(batch_size) is not int or not 1 <= batch_size <= 64:
        raise DistillationError("batch_size must be an integer from 1 through 64")
    if type(max_new_tokens) is not int or not 1 <= max_new_tokens <= 1024:
        raise DistillationError("max_new_tokens must be an integer from 1 through 1024")
    if not isinstance(temperature, (int, float)) or isinstance(temperature, bool) or not math.isfinite(temperature) or not 0.01 <= temperature <= 2.0:
        raise DistillationError("temperature must be finite and between 0.01 and 2")
    if not isinstance(top_p, (int, float)) or isinstance(top_p, bool) or not math.isfinite(top_p) or not 0.01 <= top_p <= 1.0:
        raise DistillationError("top_p must be finite and between 0.01 and 1")
    if type(seed) is not int or not 0 <= seed <= 2**32 - 1:
        raise DistillationError("seed must be an unsigned 32-bit integer")
    if type(max_attempts) is not int or not per_workflow <= max_attempts <= 100_000:
        raise DistillationError("max_attempts must be between per_workflow and 100000")
    backend_config = params.get("backend")
    if not isinstance(backend_config, dict) or backend_config.get("type", "hf") != "hf":
        raise DistillationError("synthetic generation requires the local HF backend")
    workflow_inputs = params.get("workflows")
    if not isinstance(workflow_inputs, list) or not 1 <= len(workflow_inputs) <= MAX_QUESTIONS:
        raise DistillationError("workflows must contain from 1 through 256 exemplar files")
    if per_workflow * len(workflow_inputs) > MAX_TOTAL_DISTILLATION_STATES:
        raise DistillationError("requested synthetic-state total exceeds the bound")
    if max_attempts * len(workflow_inputs) > MAX_TOTAL_GENERATION_ATTEMPTS:
        raise DistillationError("combined synthetic generation attempts exceed the bound")

    total_source_bytes = [0]
    prepared: list[dict[str, Any]] = []
    seen_workflows: set[str] = set()
    for item in workflow_inputs:
        if not isinstance(item, dict):
            raise DistillationError("each workflow must be an object")
        _only_fields(item, {"workflow", "exemplars_path"}, "synthesis workflow")
        workflow = item.get("workflow")
        if not isinstance(workflow, str) or not _SAFE_WORKFLOW.fullmatch(workflow) or workflow in seen_workflows:
            raise DistillationError("workflow name is invalid or duplicated")
        seen_workflows.add(workflow)
        source, source_digest = _read_json_source(source_root, item.get("exemplars_path"), total_source_bytes)
        _only_fields(source, {"schema_version", "workflow", "examples"}, "distillation exemplar file")
        exemplars = source.get("examples")
        if (source.get("schema_version") != "threadforge.anyjev-distillation-exemplars.v1"
                or source.get("workflow") != workflow or not isinstance(exemplars, list)
                or not 3 <= len(exemplars) <= 10_000 or not all(isinstance(value, dict) for value in exemplars)):
            raise DistillationError("exemplar file version, workflow, or example count is invalid")
        unique: dict[str, dict[str, Any]] = {}
        try:
            for example in exemplars:
                key = json.dumps(example, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
                unique.setdefault(key, example)
        except (TypeError, ValueError):
            raise DistillationError("exemplar file contains a non-JSON state") from None
        exemplars = list(unique.values())
        if len(exemplars) < 3:
            raise DistillationError("at least three distinct exemplars are required per workflow")
        prepared.append({
            "workflow": workflow,
            "workflow_sha256": _sha256_text(workflow),
            "source_sha256": source_digest,
            "examples": exemplars,
        })

    try:
        backend = _backend(
            backend_config, expected_model_name=generator_model,
            expected_model_checkpoint_sha256=checkpoint_sha,
        )
        if (getattr(backend, "name", None) != generator_model
                or getattr(backend, "_threadforge_model_environment_verified", False) is not True
                or getattr(backend, "_threadforge_model_checkpoint_sha256", None) != checkpoint_sha):
            raise DistillationError("generator checkpoint was not locally fingerprint-verified")
        python_rng = random.Random(seed)
        outputs: list[tuple[str, bytes, dict[str, Any]]] = []
        batch_index = 0
        for item in prepared:
            exemplars = item["examples"]
            state_keys = {
                json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
                for value in exemplars
            }
            states: list[dict[str, Any]] = []
            reasons: dict[str, int] = {}
            generated_count = 0
            while len(states) < per_workflow and generated_count < max_attempts:
                request_count = min(batch_size, max_attempts - generated_count, per_workflow - len(states))
                prompts = [
                    _synthetic_prompt(backend.tokenizer, item["workflow"], python_rng.sample(exemplars, 3))
                    for _ in range(request_count)
                ]
                batch_seed = (seed + batch_index) % (2**32)
                generated_texts = _generate_synthetic_batch(
                    prompts, backend, max_new_tokens, float(temperature), float(top_p), batch_seed,
                )
                if len(generated_texts) != len(prompts):
                    raise DistillationError("local generator returned an invalid batch size")
                batch_index += 1
                for generated_text in generated_texts:
                    generated_count += 1
                    candidate, reason = _parse_generated_state(generated_text, exemplars)
                    if candidate is None:
                        reasons[reason] = reasons.get(reason, 0) + 1
                        continue
                    key = json.dumps(candidate, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
                    if key in state_keys:
                        reasons["duplicate"] = reasons.get("duplicate", 0) + 1
                        continue
                    state_keys.add(key)
                    states.append(candidate)
            if len(states) != per_workflow:
                raise DistillationError("synthetic generation did not reach the requested count within max_attempts")
            output_document = {
                "schema_version": "threadforge.anyjev-distillation-synthetic-states.v1",
                "workflow": item["workflow"],
                "generator": generator_model,
                "n": len(states),
                "generated": generated_count,
                "rejected": sum(reasons.values()),
                "reject_reasons": dict(sorted(reasons.items())),
                "settings": {
                    "temperature": float(temperature),
                    "top_p": float(top_p),
                    "seed": seed,
                    "max_new_tokens": max_new_tokens,
                    "exemplars_per_prompt": 3,
                    "batch_size": batch_size,
                    "max_attempts": max_attempts,
                },
                "states": states,
            }
            content = json.dumps(
                output_document, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False,
            ).encode("utf-8")
            relative_path = (
                f"distillation/synth/{checkpoint_sha}/{item['workflow_sha256']}.json"
            )
            outputs.append((relative_path, content, {
                "workflow_sha256": item["workflow_sha256"],
                "source_sha256": item["source_sha256"],
                "examples": len(states),
                "generated": generated_count,
                "rejected": sum(reasons.values()),
            }))

        after = _fingerprint_model_directory(backend_config["model_path"])
        if after["sha256"] != checkpoint_sha:
            raise DistillationError("generator checkpoint changed during synthetic-state generation")
        output_receipts = []
        for relative_path, content, metadata in outputs:
            output_digest = _write_json_exclusive(cache_root, relative_path, content)
            output_receipts.append({**metadata, "relative_path": relative_path, "sha256": output_digest})
    except DistillationError:
        raise
    except Exception as error:
        raise DistillationError(f"synthetic-state generation failed safely: {type(error).__name__}") from None

    return {
        "schema_version": "threadforge.anyjev-distillation-synth-result.v1",
        "generator_sha256": _sha256_text(generator_model),
        "generator_checkpoint_sha256": checkpoint_sha,
        "workflow_count": len(prepared),
        "total_synthetic_examples": sum(item["examples"] for item in output_receipts),
        "total_generated": sum(item["generated"] for item in output_receipts),
        "total_rejected": sum(item["rejected"] for item in output_receipts),
        "outputs": output_receipts,
        "human_review_required": True,
        "model_inference_verified": True,
        "execution_verified": False,
        "cutover_evidence_eligible": False,
    }


def _load_local_anyjev_split(dataset_path: Path, split: str) -> Any:
    # This intentionally accepts only a caller-materialized DatasetDict, never a Hub identifier.
    try:
        from datasets import DatasetDict, load_from_disk
    except ImportError:
        raise DistillationError("install the optional typed-data dependency to read a local DatasetDict") from None
    try:
        dataset = load_from_disk(str(dataset_path))
    except Exception as error:
        raise DistillationError(f"local typed-decision dataset could not be read: {type(error).__name__}") from None
    if not isinstance(dataset, DatasetDict) or split not in dataset:
        raise DistillationError("dataset_path must contain a local train/test DatasetDict split")
    return dataset[split]


def _strict_source_json(value: Any, label: str) -> Any:
    if not isinstance(value, str) or not value:
        raise DistillationError(f"typed-decision {label} must be bounded JSON text")
    try:
        encoded = value.encode("utf-8")
    except UnicodeError:
        raise DistillationError(f"typed-decision {label} is not valid UTF-8") from None
    if len(encoded) > MAX_SOURCE_JSON_BYTES:
        raise DistillationError(f"typed-decision {label} exceeds the source size limit")

    def unique_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, item in pairs:
            if key in result:
                raise ValueError("duplicate JSON key")
            result[key] = item
        return result

    def reject_constant(_value: str) -> None:
        raise ValueError("non-finite JSON value")

    try:
        return json.loads(value, object_pairs_hook=unique_pairs, parse_constant=reject_constant)
    except (ValueError, TypeError):
        raise DistillationError(f"typed-decision {label} is malformed JSON") from None


def _typed_question_document(qname: str, spec: Any) -> tuple[dict[str, Any], list[str]]:
    if (not isinstance(qname, str) or not qname.strip() or len(qname) > 128
            or not isinstance(spec, dict) or set(spec) - {"type", "instructions", "criteria"}):
        raise DistillationError("typed-decision question specification is invalid")
    kind, text, criteria = spec.get("type"), spec.get("instructions"), spec.get("criteria")
    if not isinstance(text, str) or not text.strip() or len(text) > 10_000:
        raise DistillationError("typed-decision question instructions are invalid")
    if kind == "choice":
        if isinstance(criteria, dict) and criteria and all(
            isinstance(key, str) and isinstance(value, str) for key, value in criteria.items()
        ):
            keys = list(criteria)
            options = [f"{key}: {criteria[key]}" for key in keys]
        elif isinstance(criteria, list) and criteria and all(isinstance(value, str) for value in criteria):
            keys = list(criteria)
            options = list(criteria)
        else:
            raise DistillationError("typed-decision choice criteria are invalid")
        question = {"kind": "choice", "text": text, "name": qname, "options": options}
    elif kind == "noul":
        question_text = f"Is the following statement true? {text}"
        if isinstance(criteria, dict):
            if set(criteria) - {"true", "false"} or not all(
                isinstance(value, str) for value in criteria.values()
            ):
                raise DistillationError("typed-decision boolean criteria are invalid")
            question_text += f"\nYes means: {criteria.get('true', '')}\nNo means: {criteria.get('false', '')}"
        question = {"kind": "noul", "text": question_text, "name": qname}
        keys = ["true", "false"]
    elif kind == "score":
        if isinstance(criteria, dict) and criteria and all(
            isinstance(value, str) for value in criteria.values()
        ):
            levels = list(criteria.values())
        elif isinstance(criteria, list) and criteria and all(isinstance(value, str) for value in criteria):
            levels = list(criteria)
        else:
            raise DistillationError("typed-decision score criteria are invalid")
        question = {"kind": "score", "text": text, "name": qname, "levels": levels}
        keys = [str(index) for index in range(len(levels))]
    else:
        raise DistillationError("typed-decision question kind is unsupported")
    from .protocol import ProtocolError, _question

    try:
        parsed = _question(question)
    except ProtocolError:
        raise DistillationError("typed-decision question cannot be represented by AnyJev") from None
    return question, keys[:parsed.k]


def run_distillation_export_cases(params: dict[str, Any]) -> dict[str, Any]:
    # The saved DatasetDict is local input; this operation never calls load_dataset or the Hub.
    from .protocol import ProtocolError, _fingerprint_model_directory

    if not isinstance(params, dict):
        raise DistillationError("typed-case export parameters must be an object")
    _only_fields(params, {"dataset_path", "output_root", "split", "workflows", "max_cases_per_workflow"},
                 "typed-case export parameters")
    _reject_credentials(params, "typed-case export request")
    dataset_path = _real_cache_root(params.get("dataset_path"))
    output_root = _real_cache_root(params.get("output_root"))
    if dataset_path == output_root or dataset_path.is_relative_to(output_root) or output_root.is_relative_to(dataset_path):
        raise DistillationError("typed-case input and output roots must not overlap")
    split = params.get("split")
    if not isinstance(split, str) or split not in {"train", "test"}:
        raise DistillationError("typed-case export split must be train or test")
    workflows = params.get("workflows")
    if (not isinstance(workflows, list) or not 1 <= len(workflows) <= len(ANYJEV_TYPED_WORKFLOWS)
            or any(not isinstance(item, str) or item not in ANYJEV_TYPED_WORKFLOWS for item in workflows)
            or len(set(workflows)) != len(workflows)):
        raise DistillationError("typed-case export workflows are invalid or duplicated")
    case_limit = params.get("max_cases_per_workflow", 5000)
    if type(case_limit) is not int or not 1 <= case_limit <= MAX_TYPED_EXPORT_ROWS_PER_WORKFLOW:
        raise DistillationError("max_cases_per_workflow must be from 1 through 5000")

    try:
        fingerprint_before = _fingerprint_model_directory(str(dataset_path))["sha256"]
        dataset = _load_local_anyjev_split(dataset_path, split)
        selected = set(workflows)
        case_counts = {workflow: 0 for workflow in workflows}
        case_ids = {workflow: set() for workflow in workflows}
        train_states: dict[str, dict[str, dict[str, Any]]] = {workflow: {} for workflow in workflows}
        train_questions: dict[str, dict[str, dict[str, Any]]] = {workflow: {} for workflow in workflows}
        test_questions: dict[str, dict[str, dict[str, Any]]] = {workflow: {} for workflow in workflows}
        expected_questions: dict[str, dict[str, dict[str, Any]]] = {workflow: {} for workflow in workflows}
        total_examples = 0
        total_source_bytes = 0
        for row in dataset:
            if not isinstance(row, dict):
                raise DistillationError("local typed-decision dataset row is invalid")
            if set(row) != {"id", "workflow", "state", "questions", "gold"}:
                raise DistillationError("local typed-decision row has an unsupported column set")
            workflow = row.get("workflow")
            if workflow not in selected or case_counts[workflow] >= case_limit:
                continue
            case_id = row.get("id")
            if not isinstance(case_id, str) or not case_id.strip() or len(case_id) > 512:
                raise DistillationError("local typed-decision case identity is invalid")
            if case_id in case_ids[workflow]:
                raise DistillationError("local typed-decision split contains duplicate case identities")
            case_ids[workflow].add(case_id)
            raw_state, raw_questions, raw_gold = row.get("state"), row.get("questions"), row.get("gold")
            for raw, label in ((raw_state, "state"), (raw_questions, "questions"), (raw_gold, "gold")):
                if not isinstance(raw, str):
                    raise DistillationError(f"typed-decision {label} must be bounded JSON text")
                try:
                    total_source_bytes += len(raw.encode("utf-8"))
                except UnicodeError:
                    raise DistillationError(f"typed-decision {label} is not valid UTF-8") from None
                if total_source_bytes > MAX_TOTAL_SOURCE_JSON_BYTES:
                    raise DistillationError("typed-decision source exceeds the aggregate size limit")
            state = _strict_source_json(raw_state, "state")
            questions = _strict_source_json(raw_questions, "questions")
            gold = _strict_source_json(raw_gold, "gold")
            if not isinstance(state, dict) or not isinstance(questions, dict) or not isinstance(gold, dict):
                raise DistillationError("local typed-decision row data must be JSON objects")
            if not questions or len(questions) > MAX_QUESTIONS or set(questions) != set(gold):
                raise DistillationError("local typed-decision question and gold keys do not align")
            _reject_credentials({"state": state, "questions": questions}, "typed-decision source row")
            question_documents: dict[str, dict[str, Any]] = {}
            option_keys: dict[str, list[str]] = {}
            for qname, spec in questions.items():
                question_documents[qname], option_keys[qname] = _typed_question_document(qname, spec)
            if expected_questions[workflow]:
                if set(question_documents) != set(expected_questions[workflow]):
                    raise DistillationError("typed-decision question set changed within a workflow")
                for qname, question_document in question_documents.items():
                    current = json.dumps(question_document, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
                    prior = json.dumps(expected_questions[workflow][qname], ensure_ascii=False, sort_keys=True,
                                       separators=(",", ":"))
                    if current != prior:
                        raise DistillationError("typed-decision question layout changed within a workflow")
            else:
                expected_questions[workflow] = question_documents

            if split == "train":
                state_json = json.dumps(state, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
                if state_json not in train_states[workflow]:
                    train_states[workflow][state_json] = state
                    total_examples += 1
            question_collection = train_questions if split == "train" else test_questions
            for qname, question_document in question_documents.items():
                gold_row = gold.get(qname)
                if not isinstance(gold_row, dict) or set(gold_row) - {"label", "probabilities", "score"}:
                    raise DistillationError("typed-decision gold annotation is invalid")
                label = str(gold_row.get("label"))
                if label not in option_keys[qname]:
                    raise DistillationError("typed-decision gold label is outside its question options")
                probabilities = gold_row.get("probabilities")
                if not isinstance(probabilities, dict):
                    raise DistillationError("typed-decision gold probabilities are invalid")
                for value in probabilities.values():
                    if (not isinstance(value, (int, float)) or isinstance(value, bool)
                            or not math.isfinite(value) or value < 0):
                        raise DistillationError("typed-decision gold probabilities are invalid")
                if "score" in gold_row and (
                    not isinstance(gold_row["score"], (int, float)) or isinstance(gold_row["score"], bool)
                    or not math.isfinite(gold_row["score"])
                ):
                    raise DistillationError("typed-decision gold score is invalid")
                record = question_collection[workflow].setdefault(qname, {
                    "question": question_document,
                    "examples": [],
                })
                record["examples"].append({"state": state, "gold_index": option_keys[qname].index(label)})
                total_examples += 1
            case_counts[workflow] += 1
            if total_examples > MAX_TOTAL_DISTILLATION_STATES:
                raise DistillationError("typed-case export exceeds the aggregate example limit")
            if all(case_counts[name] >= case_limit for name in workflows):
                break

        if any(case_counts[name] < 1 for name in workflows):
            raise DistillationError("requested local dataset split is missing one or more workflows")
        if split == "train" and any(len(train_states[name]) < 3 for name in workflows):
            raise DistillationError("each training workflow requires at least three distinct state exemplars")
        question_collections = train_questions if split == "train" else test_questions
        if any(not question_collections[name] for name in workflows):
            raise DistillationError("selected dataset split contains no typed questions")
        fingerprint_after = _fingerprint_model_directory(str(dataset_path))["sha256"]
        if fingerprint_after != fingerprint_before:
            raise DistillationError("local typed-decision dataset changed during export")

        outputs: list[tuple[str, bytes, dict[str, Any]]] = []
        aggregate_output_bytes = 0
        for workflow in workflows:
            workflow_sha = _sha256_text(workflow)
            if split == "train":
                document = {
                    "schema_version": "threadforge.anyjev-distillation-exemplars.v1",
                    "workflow": workflow,
                    "examples": list(train_states[workflow].values()),
                }
                entries = [(f"distillation/source/{fingerprint_before}/train/exemplars/{workflow_sha}.json",
                            document, {"role": "exemplars", "workflow_sha256": workflow_sha,
                                      "examples": len(document["examples"])})]
                for qname, question_record in sorted(train_questions[workflow].items()):
                    question_id = f"typed.{workflow}.{qname}"
                    question_sha = _sha256_text(question_id)
                    train_document = {
                        "schema_version": "threadforge.anyjev-distillation-train-cases.v1",
                        "question_id": question_id,
                        "question": question_record["question"],
                        "examples": question_record["examples"],
                    }
                    entries.append((
                        f"distillation/source/{fingerprint_before}/train/train-cases/{workflow_sha}/{question_sha}.json",
                        train_document,
                        {"role": "train_cases", "workflow_sha256": workflow_sha,
                         "question_sha256": question_sha, "examples": len(train_document["examples"])},
                    ))
            else:
                entries = []
                for qname, question_record in sorted(test_questions[workflow].items()):
                    question_id = f"typed.{workflow}.{qname}"
                    question_sha = _sha256_text(question_id)
                    document = {
                        "schema_version": "threadforge.anyjev-distillation-test-cases.v1",
                        "question_id": question_id,
                        "question": question_record["question"],
                        "examples": question_record["examples"],
                    }
                    entries.append((
                        f"distillation/source/{fingerprint_before}/test/test-cases/{workflow_sha}/{question_sha}.json",
                        document,
                        {"role": "test_cases", "workflow_sha256": workflow_sha,
                         "question_sha256": question_sha, "examples": len(document["examples"])},
                    ))
            for relative_path, document, metadata in entries:
                content = json.dumps(
                    document, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False,
                ).encode("utf-8")
                if len(content) > MAX_SOURCE_JSON_BYTES or len(content) > MAX_TOTAL_SOURCE_JSON_BYTES - aggregate_output_bytes:
                    raise DistillationError("typed-case export output exceeds the JSON size limit")
                aggregate_output_bytes += len(content)
                outputs.append((relative_path, content, metadata))
    except DistillationError:
        raise
    except (ProtocolError, OSError, TypeError, ValueError, KeyError, UnicodeError) as error:
        raise DistillationError(f"typed-case export failed safely: {type(error).__name__}") from None
    except Exception as error:
        raise DistillationError(f"typed-case export failed safely: {type(error).__name__}") from None

    receipts = []
    for relative_path, content, metadata in outputs:
        digest = _write_json_exclusive(output_root, relative_path, content)
        receipts.append({**metadata, "relative_path": relative_path, "sha256": digest})
    return {
        "schema_version": "threadforge.anyjev-distillation-export-cases-result.v1",
        "dataset_sha256": fingerprint_before,
        "split": split,
        "workflow_count": len(workflows),
        "case_count": sum(case_counts.values()),
        "example_count": sum(item["examples"] for item in receipts),
        "output_count": len(receipts),
        "outputs": receipts,
        "human_review_required": True,
        "model_inference_verified": False,
        "execution_verified": False,
        "cutover_evidence_eligible": False,
    }


def run_distillation_extract_gold(params: dict[str, Any]) -> dict[str, Any]:
    """Create checkpoint-pinned gold train/test feature caches for AnyJev fit and soft replay."""
    from .protocol import ProtocolError, _backend, _fingerprint_model_directory, _question

    if not isinstance(params, dict):
        raise DistillationError("gold-feature extraction parameters must be an object")
    _only_fields(params, {
        "data_root", "cache_root", "model_role", "model", "model_checkpoint_sha256", "backend",
        "block", "seed", "questions",
    }, "gold-feature extraction parameters")
    _reject_credentials(params, "gold-feature extraction request")
    data_root = _real_cache_root(params.get("data_root"))
    cache_root = _real_cache_root(params.get("cache_root"))
    if data_root == cache_root or data_root.is_relative_to(cache_root) or cache_root.is_relative_to(data_root):
        raise DistillationError("gold-feature input and output roots must not overlap")
    model_role = params.get("model_role")
    if model_role not in {"teacher", "student"}:
        raise DistillationError("model_role must be teacher or student")
    model = params.get("model")
    checkpoint_sha = params.get("model_checkpoint_sha256")
    if not isinstance(model, str) or not model.strip() or len(model) > 512:
        raise DistillationError("model must be a bounded non-empty identifier")
    if not isinstance(checkpoint_sha, str) or not re.fullmatch(r"[a-f0-9]{64}", checkpoint_sha):
        raise DistillationError("model checkpoint fingerprint is required")
    block = params.get("block")
    if type(block) is not int or not 1 <= block <= 100_000:
        raise DistillationError("block must be an integer from 1 through 100000")
    seed = params.get("seed", 0)
    if type(seed) is not int or not 0 <= seed <= 2**32 - 1:
        raise DistillationError("seed must be an unsigned 32-bit integer")
    backend_config = params.get("backend")
    if not isinstance(backend_config, dict) or backend_config.get("type", "hf") != "hf":
        raise DistillationError("gold-feature extraction requires the local HF backend")
    question_inputs = params.get("questions")
    if not isinstance(question_inputs, list) or not 1 <= len(question_inputs) <= MAX_QUESTIONS:
        raise DistillationError("questions must contain from 1 through 256 entries")

    total_source_bytes = [0]
    total_feature_rows = 0
    total_feature_elements = 0
    prepared: list[dict[str, Any]] = []
    seen_question_ids: set[str] = set()
    seen_question_keys: set[str] = set()
    for item in question_inputs:
        if not isinstance(item, dict):
            raise DistillationError("each gold-feature question must be an object")
        _only_fields(item, {
            "question_id", "workflow", "question", "train_cases_path", "test_cases_path",
        }, "gold-feature question")
        question_id, workflow = item.get("question_id"), item.get("workflow")
        if not isinstance(question_id, str) or not question_id.strip() or len(question_id) > 512:
            raise DistillationError("question_id must be a bounded non-empty identifier")
        if not isinstance(workflow, str) or not _SAFE_WORKFLOW.fullmatch(workflow):
            raise DistillationError("workflow name is invalid")
        question_sha = _sha256_text(question_id)
        if question_sha in seen_question_ids:
            raise DistillationError("question identifiers must be unique")
        seen_question_ids.add(question_sha)
        try:
            question = _question(item.get("question"))
        except ProtocolError:
            raise DistillationError("typed question definition is invalid") from None
        if question.key in seen_question_keys:
            raise DistillationError("question layouts must be unique")
        seen_question_keys.add(question.key)

        split_data: dict[str, dict[str, Any]] = {}
        for split, field_name, schema_version in (
            ("train", "train_cases_path", "threadforge.anyjev-distillation-train-cases.v1"),
            ("test", "test_cases_path", "threadforge.anyjev-distillation-test-cases.v1"),
        ):
            document, source_sha = _read_json_source(data_root, item.get(field_name), total_source_bytes)
            _only_fields(document, {"schema_version", "question_id", "question", "examples"},
                         f"{split} case source")
            if document.get("schema_version") != schema_version or document.get("question_id") != question_id:
                raise DistillationError(f"{split} case source version or question identity does not match")
            try:
                source_question = _question(document.get("question"))
            except ProtocolError:
                raise DistillationError(f"{split} case question definition is invalid") from None
            if source_question.key != question.key:
                raise DistillationError(f"{split} case question layout differs from the extraction request")
            examples = document.get("examples")
            if not isinstance(examples, list) or not 1 <= len(examples) <= MAX_FIT_EXAMPLES:
                raise DistillationError(f"{split} case source must contain from 1 through 5000 examples")
            states: list[dict[str, Any]] = []
            labels: list[int] = []
            for example in examples:
                if not isinstance(example, dict) or set(example) != {"state", "gold_index"}:
                    raise DistillationError("gold-feature case entries must contain only state and gold_index")
                state, label = example["state"], example["gold_index"]
                if not isinstance(state, dict) or type(label) is not int or not 0 <= label < question.k:
                    raise DistillationError("gold-feature case state or label is invalid")
                states.append(state)
                labels.append(label)
            total_feature_rows += len(states)
            if total_feature_rows > MAX_TOTAL_DISTILLATION_STATES:
                raise DistillationError("gold-feature extraction exceeds the aggregate state limit")
            split_data[split] = {
                "states": states,
                "labels": np.asarray(labels, dtype=np.int64),
                "source_sha256": source_sha,
            }
        prepared.append({
            "question_id": question_id,
            "question_sha256": question_sha,
            "workflow": workflow,
            "question": question,
            "classes": question.k,
            "splits": split_data,
        })

    try:
        fingerprint_before = _fingerprint_model_directory(backend_config["model_path"])["sha256"]
        if fingerprint_before != checkpoint_sha:
            raise DistillationError("model checkpoint does not match the requested gold-feature pin")
        backend = _backend(
            backend_config, expected_model_name=model, expected_model_checkpoint_sha256=checkpoint_sha,
        )
        if (getattr(backend, "name", None) != model
                or getattr(backend, "_threadforge_model_environment_verified", False) is not True
                or getattr(backend, "_threadforge_model_checkpoint_sha256", None) != checkpoint_sha):
            raise DistillationError("feature checkpoint was not locally fingerprint-verified")
        from anyjev import Decider
        from anyjev.state import render_state

        decider = Decider(backend, adapt=False)
        pending: list[tuple[str, dict[str, np.ndarray], dict[str, Any]]] = []
        total_train_examples = 0
        total_test_examples = 0
        for item in prepared:
            question = item["question"]
            for split in ("train", "test"):
                source = item["splits"][split]
                states = source["states"]
                labels = source["labels"]
                permutations = None
                if split == "train" and not question.ordered:
                    rng = np.random.RandomState(seed)
                    permutations = [list(rng.permutation(question.k)) for _ in states]
                rendered = [render_state(state) for state in states]
                features = np.asarray(
                    decider._features(question, rendered, [block], permutations), dtype=np.float32,
                )
                if (features.ndim != 3 or features.shape[0] != len(states) or features.shape[1] != 1
                        or not 1 <= features.shape[2] <= MAX_FEATURE_WIDTH
                        or features.size > MAX_ARRAY_ELEMENTS or not np.all(np.isfinite(features))):
                    raise DistillationError("gold feature extraction returned an invalid shape or value")
                total_feature_elements += int(features.size)
                if total_feature_elements > MAX_TOTAL_GOLD_FEATURE_ELEMENTS:
                    raise DistillationError("gold feature extraction exceeds the aggregate feature-element limit")
                hidden = features.astype(np.float16)
                if not np.all(np.isfinite(hidden)):
                    raise DistillationError("gold features cannot be represented in the NPZ cache dtype")
                relative_path = (
                    f"distillation/gold_features/{checkpoint_sha}/"
                    f"{item['question_sha256']}.{split}.npz"
                )
                arrays = {
                    "H": hidden,
                    "y": labels,
                    "K": np.asarray(question.k, dtype=np.int64),
                    "layers": np.asarray([block], dtype=np.int64),
                }
                pending.append((relative_path, arrays, {
                    "role": f"{model_role}_gold_{split}_features",
                    "model_role": model_role,
                    "question_sha256": item["question_sha256"],
                    "model_sha256": _sha256_text(model),
                    "model_checkpoint_sha256": checkpoint_sha,
                    "source_sha256": source["source_sha256"],
                    "examples": len(states),
                    "classes": question.k,
                    "block": block,
                    "feature_width": int(hidden.shape[2]),
                    "split": split,
                }))
                if split == "train":
                    total_train_examples += len(states)
                else:
                    total_test_examples += len(states)
        fingerprint_after = _fingerprint_model_directory(backend_config["model_path"])["sha256"]
        if fingerprint_after != checkpoint_sha or fingerprint_before != checkpoint_sha:
            raise DistillationError("model checkpoint changed during gold-feature extraction")
        caches = []
        for relative_path, arrays, metadata in pending:
            digest = _write_npz_exclusive(cache_root, relative_path, arrays)
            caches.append({**metadata, "relative_path": relative_path, "sha256": digest})
    except DistillationError:
        raise
    except Exception as error:
        raise DistillationError(f"gold-feature extraction failed safely: {type(error).__name__}") from None

    return {
        "schema_version": "threadforge.anyjev-distillation-extract-gold-result.v1",
        "model_role": model_role,
        "model_sha256": _sha256_text(model),
        "model_checkpoint_sha256": checkpoint_sha,
        "question_count": len(prepared),
        "total_train_examples": total_train_examples,
        "total_test_examples": total_test_examples,
        "cache_count": len(caches),
        "caches": caches,
        "human_review_required": True,
        "model_inference_verified": True,
        "execution_verified": False,
        "cutover_evidence_eligible": False,
    }


def run_distillation_pipeline(params: dict[str, Any]) -> dict[str, Any]:
    """Run the local AnyJev typed-data distillation stages as one resumable workflow."""
    from .protocol import ProtocolError, _question

    if not isinstance(params, dict):
        raise DistillationError("distillation pipeline parameters must be an object")
    _only_fields(params, {
        "dataset_path", "staging_root", "cache_root", "workflows", "calibration_examples",
        "budgets", "bootstrap_resamples", "seed", "generator", "teacher", "students",
    }, "distillation pipeline parameters")
    _reject_credentials(params, "distillation pipeline request")
    dataset_path = _real_cache_root(params.get("dataset_path"))
    staging_root = _real_cache_root(params.get("staging_root"))
    cache_root = _real_cache_root(params.get("cache_root"))
    roots = (dataset_path, staging_root, cache_root)
    if any(left == right or left.is_relative_to(right) or right.is_relative_to(left)
           for index, left in enumerate(roots) for right in roots[index + 1:]):
        raise DistillationError("pipeline dataset, staging, and cache roots must be separate")

    workflows = params.get("workflows")
    if (not isinstance(workflows, list) or not 1 <= len(workflows) <= len(ANYJEV_TYPED_WORKFLOWS)
            or any(not isinstance(value, str) or value not in ANYJEV_TYPED_WORKFLOWS for value in workflows)
            or len(set(workflows)) != len(workflows)):
        raise DistillationError("pipeline workflows are invalid or duplicated")
    calibration_examples = params.get("calibration_examples", 300)
    if type(calibration_examples) is not int or not 8 <= calibration_examples <= MAX_FIT_EXAMPLES:
        raise DistillationError("calibration_examples must be from 8 through 5000")
    budgets = params.get("budgets", [300, 1200])
    if (not isinstance(budgets, list) or not 1 <= len(budgets) <= 16
            or any(type(value) is not int or not 1 <= value <= MAX_FIT_EXAMPLES for value in budgets)
            or budgets != sorted(set(budgets))):
        raise DistillationError("budgets must be unique increasing integers from 1 through 5000")
    bootstrap_resamples = params.get("bootstrap_resamples", 1000)
    if type(bootstrap_resamples) is not int or not 100 <= bootstrap_resamples <= MAX_BOOTSTRAP_RESAMPLES:
        raise DistillationError("bootstrap_resamples must be from 100 through 10000")
    seed = params.get("seed", 0)
    if type(seed) is not int or not 0 <= seed <= 2**32 - 1:
        raise DistillationError("seed must be an unsigned 32-bit integer")

    generator = params.get("generator")
    if not isinstance(generator, dict):
        raise DistillationError("pipeline generator must be an object")
    _only_fields(generator, {
        "model", "checkpoint_sha256", "backend", "per_workflow", "batch_size", "max_new_tokens",
        "temperature", "top_p", "max_attempts",
    }, "pipeline generator")
    generator_model = generator.get("model")
    generator_sha = generator.get("checkpoint_sha256")
    per_workflow = generator.get("per_workflow", 1200)
    if (not isinstance(generator_model, str) or not generator_model.strip() or len(generator_model) > 512
            or not isinstance(generator_sha, str) or not re.fullmatch(r"[a-f0-9]{64}", generator_sha)
            or type(per_workflow) is not int or not 1 <= per_workflow <= MAX_FIT_EXAMPLES):
        raise DistillationError("pipeline generator identity or count is invalid")
    if per_workflow * len(workflows) > MAX_TOTAL_DISTILLATION_STATES:
        raise DistillationError("pipeline synthetic-state total exceeds the bound")
    if len({min(budget, per_workflow) for budget in budgets}) != len(budgets):
        raise DistillationError("pipeline synthetic count collapses requested fit budgets")
    generator_backend = generator.get("backend")
    if not isinstance(generator_backend, dict) or generator_backend.get("type", "hf") != "hf":
        raise DistillationError("pipeline generator must use the local HF backend")

    teacher = params.get("teacher")
    if not isinstance(teacher, dict):
        raise DistillationError("pipeline teacher must be an object")
    _only_fields(teacher, {"model", "checkpoint_sha256", "backend", "artifact", "block", "n_layers"},
                 "pipeline teacher")
    teacher_model, teacher_sha = teacher.get("model"), teacher.get("checkpoint_sha256")
    teacher_block, teacher_layers = teacher.get("block"), teacher.get("n_layers")
    if (not isinstance(teacher_model, str) or not teacher_model.strip() or len(teacher_model) > 512
            or not isinstance(teacher_sha, str) or not re.fullmatch(r"[a-f0-9]{64}", teacher_sha)
            or type(teacher_block) is not int or type(teacher_layers) is not int
            or not 1 <= teacher_block <= teacher_layers <= 100_000):
        raise DistillationError("pipeline teacher identity, checkpoint, or block is invalid")
    teacher_backend, teacher_artifact = teacher.get("backend"), teacher.get("artifact")
    if not isinstance(teacher_backend, dict) or teacher_backend.get("type", "hf") != "hf" or not isinstance(teacher_artifact, dict):
        raise DistillationError("pipeline teacher requires a local HF backend and native artifact")

    student_inputs = params.get("students")
    if not isinstance(student_inputs, list) or not 1 <= len(student_inputs) <= MAX_STUDENTS:
        raise DistillationError("pipeline requires from 1 through 8 students")
    students: list[dict[str, Any]] = []
    seen_students: set[str] = set()
    for item in student_inputs:
        if not isinstance(item, dict):
            raise DistillationError("pipeline student must be an object")
        _only_fields(item, {"model", "checkpoint_sha256", "backend", "block", "n_layers"}, "pipeline student")
        model, checkpoint = item.get("model"), item.get("checkpoint_sha256")
        block, n_layers, backend_config = item.get("block"), item.get("n_layers"), item.get("backend")
        if (not isinstance(model, str) or not model.strip() or len(model) > 512
                or not isinstance(checkpoint, str) or not re.fullmatch(r"[a-f0-9]{64}", checkpoint)
                or type(block) is not int or type(n_layers) is not int or not 1 <= block <= n_layers <= 100_000
                or not isinstance(backend_config, dict) or backend_config.get("type", "hf") != "hf"):
            raise DistillationError("pipeline student identity, checkpoint, backend, or block is invalid")
        model_digest = _sha256_text(model)
        if model_digest in seen_students or model_digest == _sha256_text(teacher_model):
            raise DistillationError("pipeline student model identities must be unique and differ from the teacher")
        seen_students.add(model_digest)
        students.append({
            "model": model, "checkpoint_sha256": checkpoint, "backend": backend_config,
            "block": block, "n_layers": n_layers, "model_sha256": model_digest,
        })

    for role, model_path in (
        ("generator", generator_backend.get("model_path")),
        ("teacher", teacher_backend.get("model_path")),
        *((f"student-{index}", item["backend"].get("model_path")) for index, item in enumerate(students)),
    ):
        model_root = _real_cache_root(model_path)
        if any(model_root == root or model_root.is_relative_to(root) or root.is_relative_to(model_root)
               for root in roots):
            raise DistillationError(f"pipeline {role} model root overlaps a data or output root")

    def stage(name: str, function: Any, request: dict[str, Any]) -> dict[str, Any]:
        try:
            result = function(request)
        except Exception as error:
            raise DistillationError(f"pipeline stage {name} failed safely: {type(error).__name__}") from None
        if not isinstance(result, dict):
            raise DistillationError(f"pipeline stage {name} returned an invalid result")
        return result

    def verify_flags(result: dict[str, Any], stage_name: str, model_inference: bool) -> None:
        if (result.get("human_review_required") is not True
                or result.get("model_inference_verified") is not model_inference
                or result.get("execution_verified") is not False
                or result.get("cutover_evidence_eligible") is not False):
            raise DistillationError(f"pipeline stage {stage_name} returned unsafe evidence flags")

    train_export = stage("export-train", run_distillation_export_cases, {
        "dataset_path": str(dataset_path), "output_root": str(staging_root), "split": "train",
        "workflows": workflows, "max_cases_per_workflow": calibration_examples,
    })
    test_export = stage("export-test", run_distillation_export_cases, {
        "dataset_path": str(dataset_path), "output_root": str(staging_root), "split": "test",
        "workflows": workflows, "max_cases_per_workflow": calibration_examples,
    })
    if (train_export.get("schema_version") != "threadforge.anyjev-distillation-export-cases-result.v1"
            or test_export.get("schema_version") != "threadforge.anyjev-distillation-export-cases-result.v1"
            or train_export.get("split") != "train" or test_export.get("split") != "test"
            or train_export.get("dataset_sha256") != test_export.get("dataset_sha256")
            or train_export.get("workflow_count") != len(workflows)
            or test_export.get("workflow_count") != len(workflows)
            or train_export.get("output_count") != len(train_export.get("outputs", []))
            or test_export.get("output_count") != len(test_export.get("outputs", []))):
        raise DistillationError("pipeline train/test exports did not bind to the same dataset snapshot")
    verify_flags(train_export, "export-train", False)
    verify_flags(test_export, "export-test", False)
    dataset_sha = train_export["dataset_sha256"]
    if not re.fullmatch(r"[a-f0-9]{64}", dataset_sha):
        raise DistillationError("pipeline export returned an invalid dataset fingerprint")

    workflow_by_digest = {_sha256_text(workflow): workflow for workflow in workflows}
    train_exemplars: dict[str, str] = {}
    train_cases: dict[str, dict[str, Any]] = {}
    test_cases: dict[str, dict[str, Any]] = {}
    source_bytes = [0]

    def read_export(receipt: dict[str, Any], split: str) -> dict[str, Any]:
        if (not isinstance(receipt, dict) or not isinstance(receipt.get("relative_path"), str)
                or not isinstance(receipt.get("sha256"), str)):
            raise DistillationError("pipeline export receipt is invalid")
        document, digest = _read_json_source(staging_root, receipt["relative_path"], source_bytes)
        if digest != receipt["sha256"]:
            raise DistillationError("pipeline export receipt digest does not match its case file")
        if split == "train" and receipt.get("role") == "exemplars":
            workflow = workflow_by_digest.get(receipt.get("workflow_sha256"))
            examples = document.get("examples")
            if (workflow is None or document.get("schema_version") != "threadforge.anyjev-distillation-exemplars.v1"
                    or document.get("workflow") != workflow or not isinstance(examples, list) or len(examples) < 3):
                raise DistillationError("pipeline train exemplar does not match its receipt")
            if receipt["relative_path"] != f"distillation/source/{dataset_sha}/train/exemplars/{_sha256_text(workflow)}.json":
                raise DistillationError("pipeline train exemplar path does not match its dataset/workflow identity")
            if workflow in train_exemplars:
                raise DistillationError("pipeline train export duplicated a workflow exemplar")
            train_exemplars[workflow] = receipt["relative_path"]
            return document
        expected_role = "train_cases" if split == "train" else "test_cases"
        expected_schema = f"threadforge.anyjev-distillation-{expected_role.replace('_', '-')}.v1"
        workflow = workflow_by_digest.get(receipt.get("workflow_sha256"))
        question_id = document.get("question_id")
        question_digest = _sha256_text(question_id) if isinstance(question_id, str) else ""
        examples = document.get("examples")
        if (workflow is None or receipt.get("role") != expected_role
                or receipt.get("question_sha256") != question_digest
                or document.get("schema_version") != expected_schema
                or not isinstance(question_id, str) or not question_id.startswith(f"typed.{workflow}.")
                or not isinstance(examples, list) or not examples):
            raise DistillationError("pipeline exported case file does not match its receipt")
        expected_split = "train" if split == "train" else "test"
        expected_path = (
            f"distillation/source/{dataset_sha}/{expected_split}/"
            f"{'train-cases' if split == 'train' else 'test-cases'}/{_sha256_text(workflow)}/{question_digest}.json"
        )
        if receipt["relative_path"] != expected_path:
            raise DistillationError("pipeline case path does not match its dataset/workflow/question identity")
        collection = train_cases if split == "train" else test_cases
        if question_id in collection:
            raise DistillationError("pipeline export duplicated a question case file")
        collection[question_id] = {
            "workflow": workflow,
            "question": document.get("question"),
            "path": receipt["relative_path"],
            "examples": len(examples),
        }
        return document

    for receipt in train_export.get("outputs", []):
        read_export(receipt, "train")
    for receipt in test_export.get("outputs", []):
        read_export(receipt, "test")
    if set(train_exemplars) != set(workflows) or set(train_cases) != set(test_cases) or not train_cases:
        raise DistillationError("pipeline export is missing workflows or matching train/test questions")
    if len(train_cases) > min(MAX_QUESTIONS, 128):
        raise DistillationError("pipeline question count exceeds the 128-question orchestration bound")

    question_inputs: list[dict[str, Any]] = []
    seen_layouts: set[str] = set()
    expected_train_examples = 0
    expected_test_examples = 0
    for question_id in sorted(train_cases):
        train_item, test_item = train_cases[question_id], test_cases[question_id]
        try:
            parsed_question = _question(train_item["question"])
        except ProtocolError:
            raise DistillationError("pipeline exported question definition is invalid") from None
        if (train_item["workflow"] != test_item["workflow"]
                or json.dumps(train_item["question"], sort_keys=True, separators=(",", ":")) !=
                   json.dumps(test_item["question"], sort_keys=True, separators=(",", ":"))):
            raise DistillationError("pipeline train and test question layouts differ")
        if parsed_question.key in seen_layouts:
            raise DistillationError("pipeline exported question layouts are duplicated")
        seen_layouts.add(parsed_question.key)
        if (train_item["examples"] != calibration_examples
                or train_item["examples"] < 2 * parsed_question.k
                or test_item["examples"] < 2):
            raise DistillationError("pipeline cases do not satisfy fit calibration and held-out bounds")
        expected_train_examples += train_item["examples"]
        expected_test_examples += test_item["examples"]
        question_inputs.append({
            "question_id": question_id,
            "workflow": train_item["workflow"],
            "question": train_item["question"],
            "train_cases_path": train_item["path"],
            "test_cases_path": test_item["path"],
        })
    if (expected_train_examples + expected_test_examples > MAX_TOTAL_DISTILLATION_STATES
            or per_workflow * len(question_inputs) > MAX_TOTAL_DISTILLATION_STATES
            or per_workflow * len(question_inputs) + expected_test_examples > MAX_TOTAL_DISTILLATION_STATES):
        raise DistillationError("pipeline aggregate train/test/synthetic state count exceeds the bound")

    synth_request = {
        "source_root": str(staging_root), "cache_root": str(staging_root),
        "generator_model": generator_model, "generator_checkpoint_sha256": generator_sha,
        "backend": generator_backend, "per_workflow": per_workflow,
        "seed": seed, "workflows": [
            {"workflow": workflow, "exemplars_path": train_exemplars[workflow]} for workflow in workflows
        ],
    }
    for name in ("batch_size", "max_new_tokens", "temperature", "top_p", "max_attempts"):
        if name in generator:
            synth_request[name] = generator[name]
    synth_result = stage("synth", run_distillation_synth, synth_request)
    if (synth_result.get("schema_version") != "threadforge.anyjev-distillation-synth-result.v1"
            or synth_result.get("workflow_count") != len(workflows)
            or synth_result.get("generator_sha256") != _sha256_text(generator_model)
            or synth_result.get("generator_checkpoint_sha256") != generator_sha):
        raise DistillationError("pipeline synthesis receipt does not match the requested generator")
    verify_flags(synth_result, "synth", True)
    if synth_result.get("total_synthetic_examples") != per_workflow * len(workflows):
        raise DistillationError("pipeline synthesis count does not match the request")
    synthetic_paths: dict[str, str] = {}
    for receipt in synth_result.get("outputs", []):
        workflow = workflow_by_digest.get(receipt.get("workflow_sha256")) if isinstance(receipt, dict) else None
        if (workflow is None or workflow in synthetic_paths or receipt.get("examples") != per_workflow
                or receipt.get("relative_path") !=
                   f"distillation/synth/{generator_sha}/{_sha256_text(workflow)}.json"):
            raise DistillationError("pipeline synthesis output is missing, duplicated, or mismatched")
        document, digest = _read_json_source(staging_root, receipt["relative_path"], source_bytes)
        if digest != receipt.get("sha256") or document.get("workflow") != workflow or document.get("n") != per_workflow:
            raise DistillationError("pipeline synthetic-state file failed receipt reconciliation")
        synthetic_paths[workflow] = receipt["relative_path"]
    if set(synthetic_paths) != set(workflows):
        raise DistillationError("pipeline synthesis omitted a requested workflow")

    label_result = stage("teacher-label", run_distillation_label, {
        "data_root": str(staging_root), "cache_root": str(cache_root),
        "teacher_model": teacher_model, "teacher_model_checkpoint_sha256": teacher_sha,
        "backend": teacher_backend, "teacher_artifact": teacher_artifact,
        "workflows": [{"workflow": workflow, "synthetic_states_path": synthetic_paths[workflow]}
                      for workflow in workflows],
        "questions": [{
            "question_id": item["question_id"], "workflow": item["workflow"],
            "test_cases_path": item["test_cases_path"],
        } for item in question_inputs],
    })
    if (label_result.get("schema_version") != "threadforge.anyjev-distillation-label-result.v1"
            or label_result.get("question_count") != len(question_inputs)
            or label_result.get("teacher_sha256") != _sha256_text(teacher_model)
            or label_result.get("teacher_checkpoint_sha256") != teacher_sha):
        raise DistillationError("pipeline teacher-label receipt does not match the requested model")
    verify_flags(label_result, "teacher-label", True)
    if (label_result.get("total_synthetic_examples") != per_workflow * len(question_inputs)
            or label_result.get("total_test_examples") != expected_test_examples
            or len(label_result.get("caches", [])) != 2 * len(question_inputs)):
        raise DistillationError("pipeline teacher-label counts do not reconcile with the exported cases")

    label_paths: dict[tuple[str, str], str] = {}
    for receipt in label_result.get("caches", []):
        if not isinstance(receipt, dict):
            raise DistillationError("pipeline teacher-label cache receipt is invalid")
        key = (receipt.get("question_sha256"), receipt.get("role"))
        expected_split = "synth" if key[1] == "teacher_synthetic_probabilities" else "test"
        expected_path = f"distillation/synth_labels/{teacher_sha}/{key[0]}.{expected_split}.npz"
        if (key[0] not in {_sha256_text(item["question_id"]) for item in question_inputs}
                or key[1] not in {"teacher_synthetic_probabilities", "teacher_test_probabilities"}
                or key in label_paths or receipt.get("relative_path") != expected_path
                or receipt.get("examples") != (per_workflow if expected_split == "synth" else
                    test_cases[ next(item["question_id"] for item in question_inputs
                                     if _sha256_text(item["question_id"]) == key[0]) ]["examples"])):
            raise DistillationError("pipeline teacher-label cache receipt is inconsistent")
        label_paths[key] = receipt["relative_path"]
    expected_label_keys = {
        (_sha256_text(item["question_id"]), role)
        for item in question_inputs
        for role in ("teacher_synthetic_probabilities", "teacher_test_probabilities")
    }
    if set(label_paths) != expected_label_keys:
        raise DistillationError("pipeline teacher-label output is incomplete")

    teacher_gold_result = stage("teacher-gold-features", run_distillation_extract_gold, {
        "data_root": str(staging_root), "cache_root": str(cache_root),
        "model_role": "teacher", "model": teacher_model, "model_checkpoint_sha256": teacher_sha,
        "backend": teacher_backend, "block": teacher_block, "seed": seed, "questions": question_inputs,
    })
    if (teacher_gold_result.get("schema_version") != "threadforge.anyjev-distillation-extract-gold-result.v1"
            or teacher_gold_result.get("model_role") != "teacher"
            or teacher_gold_result.get("model_sha256") != _sha256_text(teacher_model)
            or teacher_gold_result.get("question_count") != len(question_inputs)):
        raise DistillationError("pipeline teacher gold-feature receipt is inconsistent")
    verify_flags(teacher_gold_result, "teacher-gold-features", True)
    if (teacher_gold_result.get("model_checkpoint_sha256") != teacher_sha
            or teacher_gold_result.get("total_train_examples") != expected_train_examples
            or teacher_gold_result.get("total_test_examples") != expected_test_examples):
        raise DistillationError("pipeline teacher gold-feature counts/checkpoint do not reconcile")
    teacher_gold_paths = {
        (item["question_sha256"], item["split"]): item["relative_path"]
        for item in teacher_gold_result.get("caches", [])
        if isinstance(item, dict) and item.get("model_role") == "teacher"
    }
    expected_gold_keys = {
        (_sha256_text(item["question_id"]), split)
        for item in question_inputs for split in ("train", "test")
    }
    if set(teacher_gold_paths) != expected_gold_keys:
        raise DistillationError("pipeline teacher gold-feature output is incomplete")

    student_stage_results: list[dict[str, Any]] = []
    student_cache_paths: dict[str, dict[tuple[str, str], str]] = {}
    synthetic_workflows = [{"workflow": workflow, "synthetic_states_path": synthetic_paths[workflow]}
                           for workflow in workflows]
    synthetic_questions = [{
        "question_id": item["question_id"], "workflow": item["workflow"], "question": item["question"],
    } for item in question_inputs]
    for student in students:
        model = student["model"]
        digest = student["model_sha256"]
        synth_features = stage(f"student-synthetic-features-{digest[:12]}", run_distillation_extract, {
            "data_root": str(staging_root), "cache_root": str(cache_root),
            "student_model": model, "student_model_checkpoint_sha256": student["checkpoint_sha256"],
            "backend": student["backend"], "block": student["block"], "seed": seed,
            "workflows": synthetic_workflows, "questions": synthetic_questions,
        })
        if (synth_features.get("schema_version") != "threadforge.anyjev-distillation-extract-result.v1"
                or synth_features.get("student_sha256") != digest
                or synth_features.get("question_count") != len(question_inputs)):
            raise DistillationError("pipeline student synthetic-feature receipt is inconsistent")
        verify_flags(synth_features, "student-synthetic-features", True)
        if synth_features.get("student_checkpoint_sha256") != student["checkpoint_sha256"] or \
                synth_features.get("total_synthetic_examples") != per_workflow * len(question_inputs):
            raise DistillationError("pipeline student synthetic-feature counts/checkpoint do not reconcile")
        synth_paths: dict[str, str] = {}
        for receipt in synth_features.get("caches", []):
            if (not isinstance(receipt, dict) or receipt.get("role") != "student_synthetic_features"
                    or receipt.get("student_sha256") != digest
                    or receipt.get("student_checkpoint_sha256") != student["checkpoint_sha256"]
                    or receipt.get("examples") != per_workflow or receipt.get("question_sha256") in synth_paths):
                raise DistillationError("pipeline student synthetic-feature receipt is invalid")
            synth_paths[receipt["question_sha256"]] = receipt["relative_path"]
        if set(synth_paths) != {_sha256_text(item["question_id"]) for item in question_inputs}:
            raise DistillationError("pipeline student synthetic-feature output is incomplete")

        gold_features = stage(f"student-gold-features-{digest[:12]}", run_distillation_extract_gold, {
            "data_root": str(staging_root), "cache_root": str(cache_root),
            "model_role": "student", "model": model, "model_checkpoint_sha256": student["checkpoint_sha256"],
            "backend": student["backend"], "block": student["block"], "seed": seed,
            "questions": question_inputs,
        })
        if (gold_features.get("schema_version") != "threadforge.anyjev-distillation-extract-gold-result.v1"
                or gold_features.get("model_role") != "student" or gold_features.get("model_sha256") != digest
                or gold_features.get("question_count") != len(question_inputs)):
            raise DistillationError("pipeline student gold-feature receipt is inconsistent")
        verify_flags(gold_features, "student-gold-features", True)
        if (gold_features.get("model_checkpoint_sha256") != student["checkpoint_sha256"]
                or gold_features.get("total_train_examples") != expected_train_examples
                or gold_features.get("total_test_examples") != expected_test_examples):
            raise DistillationError("pipeline student gold-feature counts/checkpoint do not reconcile")
        gold_paths: dict[tuple[str, str], str] = {}
        for receipt in gold_features.get("caches", []):
            if (not isinstance(receipt, dict) or receipt.get("model_role") != "student"
                    or receipt.get("model_sha256") != digest or receipt.get("model_checkpoint_sha256") != student["checkpoint_sha256"]):
                raise DistillationError("pipeline student gold-feature cache receipt is invalid")
            key = (receipt.get("question_sha256"), receipt.get("split"))
            if key in gold_paths:
                raise DistillationError("pipeline student gold-feature cache receipt is duplicated")
            gold_paths[key] = receipt.get("relative_path")
        if set(gold_paths) != expected_gold_keys:
            raise DistillationError("pipeline student gold-feature output is incomplete")
        student_cache_paths[digest] = {
            **{(qsha, "synth"): path for qsha, path in synth_paths.items()},
            **gold_paths,
        }
        student_stage_results.append({
            "model_sha256": digest,
            "model_checkpoint_sha256": student["checkpoint_sha256"],
            "block": student["block"], "n_layers": student["n_layers"],
            "synthetic_features": synth_features, "gold_features": gold_features,
        })

    fit_questions: list[dict[str, Any]] = []
    soft_questions: list[dict[str, Any]] = []
    for item in question_inputs:
        question_sha = _sha256_text(item["question_id"])
        fit_students = []
        soft_students = []
        for student in students:
            digest = student["model_sha256"]
            caches = student_cache_paths[digest]
            fit_students.append({
                "model": student["model"], "block": student["block"], "n_layers": student["n_layers"],
                "gold_train_path": caches[(question_sha, "train")],
                "gold_test_path": caches[(question_sha, "test")],
                "synth_features_path": caches[(question_sha, "synth")],
            })
            soft_students.append({
                "model": student["model"], "block": student["block"], "n_layers": student["n_layers"],
                "gold_train_path": caches[(question_sha, "train")],
                "gold_test_path": caches[(question_sha, "test")],
            })
        fit_questions.append({
            "question_id": item["question_id"],
            "teacher_synth_path": label_paths[(question_sha, "teacher_synthetic_probabilities")],
            "teacher_test_path": label_paths[(question_sha, "teacher_test_probabilities")],
            "students": fit_students,
        })
        soft_questions.append({
            "question_id": item["question_id"],
            "teacher_train_path": teacher_gold_paths[(question_sha, "train")],
            "teacher_test_path": teacher_gold_paths[(question_sha, "test")],
            "students": soft_students,
        })

    fit_result = stage("fit", run_distillation_fit, {
        "cache_root": str(cache_root), "teacher_model": teacher_model,
        "calibration_examples": calibration_examples, "budgets": budgets,
        "bootstrap_resamples": bootstrap_resamples, "questions": fit_questions,
    })
    soft_result = stage("soft", run_distillation_soft, {
        "cache_root": str(cache_root), "teacher_model": teacher_model,
        "teacher_block": teacher_block, "teacher_n_layers": teacher_layers,
        "bootstrap_resamples": bootstrap_resamples, "questions": soft_questions,
    })
    if (fit_result.get("schema_version") != "threadforge.anyjev-distillation-fit-result.v1"
            or soft_result.get("schema_version") != "threadforge.anyjev-distillation-soft-result.v1"
            or fit_result.get("question_count") != len(question_inputs)
            or soft_result.get("question_count") != len(question_inputs)
            or fit_result.get("total_test_examples") != expected_test_examples
            or soft_result.get("total_test_examples") != expected_test_examples):
        raise DistillationError("pipeline CPU replay stages returned inconsistent results")
    verify_flags(fit_result, "fit", False)
    verify_flags(soft_result, "soft", False)

    return {
        "schema_version": "threadforge.anyjev-distillation-pipeline-result.v1",
        "dataset_sha256": dataset_sha,
        "workflow_count": len(workflows),
        "question_count": len(question_inputs),
        "student_count": len(students),
        "train_examples_per_question": calibration_examples,
        "test_examples": expected_test_examples,
        "synthetic_examples_per_workflow": per_workflow,
        "generator_sha256": _sha256_text(generator_model),
        "generator_checkpoint_sha256": generator_sha,
        "teacher_sha256": _sha256_text(teacher_model),
        "teacher_checkpoint_sha256": teacher_sha,
        "students": [{
            "model_sha256": student["model_sha256"],
            "model_checkpoint_sha256": student["checkpoint_sha256"],
            "block": student["block"], "n_layers": student["n_layers"],
        } for student in students],
        "stages": {
            "export_train": train_export,
            "export_test": test_export,
            "synth": synth_result,
            "teacher_label": label_result,
            "teacher_gold_features": teacher_gold_result,
            "student_features": student_stage_results,
            "fit": fit_result,
            "soft": soft_result,
        },
        "human_review_required": True,
        "model_inference_verified": True,
        "execution_verified": False,
        "cutover_evidence_eligible": False,
    }
