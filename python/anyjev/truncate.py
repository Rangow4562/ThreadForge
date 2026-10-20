"""Create a local, ordinary HF checkpoint with trailing transformer blocks removed.

This Threadforge adaptation follows the Apache-2.0 AnyJev truncation contract but
requires materialized local inputs, never overwrites an existing destination, and
returns path-free review metadata through the versioned Threadforge protocol.
"""

from __future__ import annotations

import json
import os
import re
import shutil
import stat
import tempfile
from pathlib import Path
from typing import Any

from .transformers_compat import pretrained_dtype_kwargs

_LAYER = re.compile(r"(?:^|\.)layers\.(\d+)\.")
_TOKENIZER_SIDECARS = (
    "tokenizer.json", "tokenizer_config.json", "special_tokens_map.json",
    "vocab.json", "merges.txt", "tokenizer.model", "generation_config.json",
    "chat_template.jinja", "preprocessor_config.json",
)
_PATH_METADATA_KEYS = frozenset({
    "_name_or_path", "name_or_path", "model_name_or_path", "tokenizer_name_or_path",
})
_MAX_TENSORS = 1_000_000
_MAX_CONFIG_BYTES = 16 << 20


class TruncateError(ValueError):
    """Invalid source, unsupported model layout, or unsafe output request."""


def layer_of(name: str) -> int | None:
    """Return the zero-based transformer block index encoded in a state-dict key."""
    match = _LAYER.search(name)
    return int(match.group(1)) if match else None


def truncate(params: dict[str, Any]) -> dict[str, Any]:
    """Write a bounded, path-free truncation receipt for an explicit local checkpoint."""
    from threadforge_py.protocol import ProtocolError, _fingerprint_model_directory

    if not isinstance(params, dict) or set(params) - {
        "schema_version", "model_path", "tokenizer_path", "blocks", "output_path", "dtype",
    }:
        raise TruncateError("checkpoint truncation request has unsupported fields")
    if params.get("schema_version") != "threadforge.anyjev-truncate-request.v1":
        raise TruncateError("checkpoint truncation request version is unsupported")
    blocks = params.get("blocks")
    if type(blocks) is not int or not 1 <= blocks <= 4096:
        raise TruncateError("blocks must be an integer between 1 and 4096")
    dtype = params.get("dtype", "auto")
    if not isinstance(dtype, str) or dtype not in {"auto", "float16", "bfloat16", "float32"}:
        raise TruncateError("dtype must be auto, float16, bfloat16, or float32")

    source = _existing_real_directory(params.get("model_path"), "model_path")
    tokenizer_source = _existing_real_directory(params.get("tokenizer_path", str(source)), "tokenizer_path")
    destination = _new_directory_path(params.get("output_path"))
    if _overlaps(source, destination) or _overlaps(tokenizer_source, destination):
        raise TruncateError("output_path must not overlap either local input directory")

    try:
        source_before = _fingerprint_model_directory(str(source))
        tokenizer_before = source_before if tokenizer_source == source else _fingerprint_model_directory(str(tokenizer_source))
    except ProtocolError:
        raise TruncateError("a local checkpoint or tokenizer source could not be fingerprinted safely") from None

    torch, config_loader, model_loader, tokenizer_loader, transformers_version = _load_hf_runtime()
    stage: Path | None = None
    try:
        config = config_loader.from_pretrained(
            str(source), local_files_only=True, trust_remote_code=False,
        )
        total = _block_count(getattr(config, "num_hidden_layers", None))
        if blocks > total:
            raise TruncateError("blocks exceeds the local model's transformer depth")
        torch_dtype = "auto" if dtype == "auto" else getattr(torch, dtype)
        dtype_kwargs = pretrained_dtype_kwargs(transformers_version, torch_dtype)
        model = model_loader.from_pretrained(
            str(source), local_files_only=True, trust_remote_code=False, **dtype_kwargs,
        )
        model_total = _block_count(getattr(model.config, "num_hidden_layers", None))
        if model_total != total:
            raise TruncateError("model depth changed while loading the local checkpoint")

        state = model.state_dict()
        if not isinstance(state, dict) or not 1 <= len(state) <= _MAX_TENSORS:
            raise TruncateError("local model state is empty or exceeds the tensor-count limit")
        kept: dict[str, Any] = {}
        dropped = 0
        for name, tensor in state.items():
            if not isinstance(name, str) or not name or not hasattr(tensor, "contiguous"):
                raise TruncateError("local model contains an unsupported state-dict entry")
            index = layer_of(name)
            if index is not None:
                if index >= total:
                    raise TruncateError("local model state contains a block outside its declared depth")
                if index >= blocks:
                    dropped += 1
                    continue
            kept[name] = tensor.contiguous()
        if not kept or (blocks < total and dropped == 0):
            raise TruncateError("local model layout does not support verified block truncation")

        model.config.num_hidden_layers = blocks
        _clear_source_path_metadata(model.config)
        stage = Path(tempfile.mkdtemp(prefix=".threadforge-truncate-", dir=destination.parent))
        if os.name != "nt":
            os.chmod(stage, 0o700)
        model.save_pretrained(str(stage), state_dict=kept, safe_serialization=True)
        tokenizer_included = _save_local_tokenizer(tokenizer_loader, tokenizer_source, stage)
        if not tokenizer_included:
            raise TruncateError("local tokenizer assets could not be packaged; supply --tokenizer-path")
        _sanitize_config_files(stage, (source, tokenizer_source))

        try:
            source_after = _fingerprint_model_directory(str(source))
            tokenizer_after = source_after if tokenizer_source == source else _fingerprint_model_directory(str(tokenizer_source))
        except ProtocolError:
            raise TruncateError("a local checkpoint or tokenizer source changed during truncation") from None
        if source_after != source_before or tokenizer_after != tokenizer_before:
            raise TruncateError("a local checkpoint or tokenizer source changed during truncation")

        metadata = {
            "schema_version": "threadforge.anyjev-truncated-checkpoint.v1",
            "source_checkpoint_sha256": source_before["sha256"],
            "tokenizer_source_sha256": tokenizer_before["sha256"],
            "original_blocks": total,
            "kept_blocks": blocks,
            "kept_tensor_count": len(kept),
            "dropped_tensor_count": dropped,
            "dtype": dtype,
            "human_review_required": True,
            "execution_verified": False,
            "cutover_evidence_eligible": False,
        }
        metadata_path = stage / "threadforge-truncation.json"
        with metadata_path.open("x", encoding="utf-8", newline="\n") as stream:
            json.dump(metadata, stream, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
            stream.write("\n")
        try:
            output_fingerprint = _fingerprint_model_directory(str(stage))
        except ProtocolError:
            raise TruncateError("truncated checkpoint output did not pass file-integrity validation") from None
        if destination.exists() or destination.is_symlink():
            raise TruncateError("output_path appeared during truncation; existing output was preserved")
        os.rename(stage, destination)
        stage = None
        return {
            **metadata,
            "output_sha256": output_fingerprint["sha256"],
            "output_file_count": output_fingerprint["file_count"],
            "output_total_bytes": output_fingerprint["total_bytes"],
            "output_ready": True,
        }
    except TruncateError:
        raise
    except Exception:
        raise TruncateError("local checkpoint truncation failed safely; source paths and model details were withheld") from None
    finally:
        if stage is not None and stage.exists():
            shutil.rmtree(stage, ignore_errors=True)


def _load_hf_runtime():
    try:
        import torch
        from transformers import AutoConfig, AutoModelForCausalLM, AutoTokenizer
        import transformers
    except Exception:
        raise TruncateError("local HF truncation requires the optional torch and transformers dependencies") from None
    return torch, AutoConfig, AutoModelForCausalLM, AutoTokenizer, transformers.__version__


def _block_count(value: Any) -> int:
    if isinstance(value, bool):
        raise TruncateError("local model config has no supported transformer depth")
    try:
        count = int(value)
    except (TypeError, ValueError, OverflowError):
        raise TruncateError("local model config has no supported transformer depth") from None
    if count < 1 or count > 4096:
        raise TruncateError("local model config transformer depth is outside the supported range")
    return count


def _existing_real_directory(value: Any, label: str) -> Path:
    if not isinstance(value, str) or not value.strip() or len(value) > 4096 or "\0" in value:
        raise TruncateError(f"{label} must be an absolute local directory")
    supplied = Path(value).expanduser()
    if not supplied.is_absolute():
        raise TruncateError(f"{label} must be an absolute local directory")
    try:
        absolute = Path(os.path.abspath(supplied))
        resolved = supplied.resolve(strict=True)
        info = supplied.lstat()
        same_location = os.path.samefile(absolute, resolved)
    except OSError:
        raise TruncateError(f"{label} is unavailable") from None
    reparse = bool(getattr(info, "st_file_attributes", 0) & 0x400)
    if (_path_has_link_or_reparse_component(absolute) or not same_location
            or not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode) or reparse):
        raise TruncateError(f"{label} must not traverse symbolic links or reparse points")
    return resolved


def _new_directory_path(value: Any) -> Path:
    if not isinstance(value, str) or not value.strip() or len(value) > 4096 or "\0" in value:
        raise TruncateError("output_path must be an absolute new directory")
    supplied = Path(value).expanduser()
    if not supplied.is_absolute():
        raise TruncateError("output_path must be an absolute new directory")
    try:
        parent = supplied.parent.resolve(strict=True)
        parent_info = supplied.parent.lstat()
        absolute_parent = Path(os.path.abspath(supplied.parent))
        same_parent = os.path.samefile(parent, absolute_parent)
    except OSError:
        raise TruncateError("output_path parent must already exist") from None
    parent_reparse = bool(getattr(parent_info, "st_file_attributes", 0) & 0x400)
    if (_path_has_link_or_reparse_component(absolute_parent) or not same_parent
            or not stat.S_ISDIR(parent_info.st_mode) or stat.S_ISLNK(parent_info.st_mode) or parent_reparse):
        raise TruncateError("output_path parent must be a real directory without links")
    try:
        output_info = supplied.lstat()
    except FileNotFoundError:
        return parent / supplied.name
    except OSError:
        raise TruncateError("output_path is unavailable") from None
    output_reparse = bool(getattr(output_info, "st_file_attributes", 0) & 0x400)
    if stat.S_ISLNK(output_info.st_mode) or output_reparse:
        raise TruncateError("output_path must not be a symbolic link or reparse point")
    raise TruncateError("output_path already exists; truncation never overwrites local data")


def _path_has_link_or_reparse_component(path: Path) -> bool:
    """Check every existing path component without confusing 8.3 aliases with links."""
    absolute = Path(os.path.abspath(path))
    current = Path(absolute.anchor)
    for component in absolute.parts[len(current.parts):]:
        current = current / component
        try:
            info = current.lstat()
        except OSError:
            return True
        if stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & 0x400):
            return True
    return False


def _overlaps(source: Path, destination: Path) -> bool:
    return source == destination or source in destination.parents or destination in source.parents


def _clear_source_path_metadata(config: Any) -> None:
    for field in ("_name_or_path", "name_or_path"):
        try:
            setattr(config, field, "")
        except Exception:
            pass


def _save_local_tokenizer(loader: Any, source: Path, destination: Path) -> bool:
    try:
        tokenizer = loader.from_pretrained(
            str(source), local_files_only=True, trust_remote_code=False,
        )
        if hasattr(tokenizer, "name_or_path"):
            tokenizer.name_or_path = ""
        init_kwargs = getattr(tokenizer, "init_kwargs", None)
        if isinstance(init_kwargs, dict):
            for key in _PATH_METADATA_KEYS:
                init_kwargs.pop(key, None)
        tokenizer.save_pretrained(str(destination))
        return True
    except Exception:
        copied = 0
        for filename in _TOKENIZER_SIDECARS:
            source_file = source / filename
            try:
                info = source_file.lstat()
            except FileNotFoundError:
                continue
            except OSError:
                raise TruncateError("local tokenizer assets could not be read safely") from None
            reparse = bool(getattr(info, "st_file_attributes", 0) & 0x400)
            if not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode) or reparse:
                raise TruncateError("local tokenizer assets contain an unsupported file")
            shutil.copyfile(source_file, destination / filename)
            copied += 1
        return copied > 0


def _sanitize_config_files(root: Path, input_roots: tuple[Path, ...]) -> None:
    for filename in ("config.json", "generation_config.json", "tokenizer_config.json", "special_tokens_map.json", "preprocessor_config.json"):
        config_path = root / filename
        try:
            info = config_path.lstat()
        except FileNotFoundError:
            continue
        except OSError:
            raise TruncateError("truncated checkpoint config could not be inspected") from None
        if not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode) or info.st_size > _MAX_CONFIG_BYTES:
            raise TruncateError("truncated checkpoint config is not a bounded regular file")
        try:
            payload = json.loads(config_path.read_text(encoding="utf-8"))
        except (OSError, UnicodeError, json.JSONDecodeError):
            raise TruncateError("truncated checkpoint contains malformed JSON config") from None
        payload = _remove_path_metadata(payload)
        encoded = json.dumps(payload, ensure_ascii=False, sort_keys=True, indent=2) + "\n"
        for input_root in input_roots:
            if str(input_root) in encoded or input_root.as_posix() in encoded:
                raise TruncateError("truncated checkpoint config retained a local source path")
        config_path.write_text(encoded, encoding="utf-8", newline="\n")


def _remove_path_metadata(value: Any) -> Any:
    if isinstance(value, dict):
        return {
            key: _remove_path_metadata(item)
            for key, item in value.items()
            if key.lower() not in _PATH_METADATA_KEYS
        }
    if isinstance(value, list):
        return [_remove_path_metadata(item) for item in value]
    return value
