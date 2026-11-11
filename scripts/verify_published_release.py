#!/usr/bin/env python3
"""Verify a published GitHub release is byte-identical to a verified candidate.

This is a read-only post-publication audit. It does not create, edit, or delete a
release, and a successful result is not legal clearance or release authorization.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import stat
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any, Callable


REPOSITORY = "Rangow4562/ThreadForge"
VERSION_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
EXPECTED_PAYLOAD_COUNT = 27
MAX_FILE_BYTES = 512 * 1024 * 1024
MAX_TOTAL_BYTES = 10 * 1024 * 1024 * 1024
COMMAND_TIMEOUT_SECONDS = 3600
CHUNK_SIZE = 1024 * 1024


class PublishedReleaseVerificationError(ValueError):
    """The published release does not match a verified candidate."""


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise PublishedReleaseVerificationError("GitHub returned duplicate JSON keys")
        result[key] = value
    return result


def _decode_json(raw: str, description: str) -> Any:
    try:
        return json.loads(raw, object_pairs_hook=_strict_object)
    except (json.JSONDecodeError, PublishedReleaseVerificationError) as error:
        raise PublishedReleaseVerificationError(f"{description} is not valid unambiguous JSON") from error


def _is_link_or_reparse(path: Path) -> bool:
    try:
        info = path.lstat()
    except OSError as error:
        raise PublishedReleaseVerificationError("a release asset path is missing or unreadable") from error
    reparse_flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    return stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) & reparse_flag)


def _regular_files(root: Path) -> dict[str, Path]:
    if _is_link_or_reparse(root) or not root.is_dir():
        raise PublishedReleaseVerificationError("bundle or downloaded release directory is unsafe")
    files: dict[str, Path] = {}
    try:
        children = list(root.iterdir())
    except OSError as error:
        raise PublishedReleaseVerificationError("bundle or downloaded release directory cannot be read") from error
    total_bytes = 0
    for path in children:
        name = path.name
        if name in {"", ".", ".."} or "/" in name or "\\" in name or name in files:
            raise PublishedReleaseVerificationError("release asset inventory contains an unsafe or duplicate name")
        if _is_link_or_reparse(path):
            raise PublishedReleaseVerificationError("release asset inventory contains a link")
        try:
            info = path.lstat()
        except OSError as error:
            raise PublishedReleaseVerificationError("release asset cannot be inspected") from error
        if not stat.S_ISREG(info.st_mode) or info.st_size > MAX_FILE_BYTES:
            raise PublishedReleaseVerificationError("release asset is not a bounded regular file")
        total_bytes += info.st_size
        if total_bytes > MAX_TOTAL_BYTES:
            raise PublishedReleaseVerificationError("release assets exceed the total size limit")
        files[name] = path
    return files


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            while chunk := stream.read(CHUNK_SIZE):
                digest.update(chunk)
    except OSError as error:
        raise PublishedReleaseVerificationError("release asset cannot be hashed") from error
    return digest.hexdigest()


def _run_checked(
    command: list[str],
    *,
    run: Callable[..., subprocess.CompletedProcess[str]],
    stage: str,
) -> subprocess.CompletedProcess[str]:
    try:
        result = run(
            command,
            capture_output=True,
            text=True,
            check=False,
            timeout=COMMAND_TIMEOUT_SECONDS,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise PublishedReleaseVerificationError(f"{stage} command failed or timed out") from error
    if result.returncode != 0:
        raise PublishedReleaseVerificationError(f"{stage} command failed")
    return result


def verify_published_release(
    *,
    bundle_dir: Path,
    version: str,
    source_commit: str,
    repository: str = REPOSITORY,
    gh: str = "gh",
    run: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> dict[str, Any]:
    if not VERSION_RE.fullmatch(version):
        raise PublishedReleaseVerificationError("version must be a stable MAJOR.MINOR.PATCH without leading v")
    if not COMMIT_RE.fullmatch(source_commit):
        raise PublishedReleaseVerificationError("source commit must be a full lowercase 40-character SHA-1")
    if not REPOSITORY_RE.fullmatch(repository) or any(part in {".", ".."} for part in repository.split("/")):
        raise PublishedReleaseVerificationError("repository must use owner/name form")

    source_ref = f"refs/tags/v{version}"
    candidate_verifier = Path(__file__).with_name("verify_release_candidate.py")
    _run_checked(
        [
            sys.executable,
            str(candidate_verifier),
            "--bundle-dir",
            str(bundle_dir),
            "--version",
            version,
            "--source-commit",
            source_commit,
            "--source-ref",
            source_ref,
            "--repo",
            repository,
            "--gh",
            gh,
        ],
        run=run,
        stage="release-candidate provenance verification",
    )

    candidate_files = _regular_files(bundle_dir)
    if "checksums.txt" not in candidate_files or len(candidate_files) != EXPECTED_PAYLOAD_COUNT + 1:
        raise PublishedReleaseVerificationError("verified candidate does not contain the exact 27-payload inventory")

    tag = f"v{version}"
    view = _run_checked(
        [gh, "release", "view", tag, "--repo", repository, "--json", "assets,isDraft,isPrerelease,tagName"],
        run=run,
        stage="published release metadata lookup",
    )
    release = _decode_json(view.stdout, "published release metadata")
    if (
        not isinstance(release, dict)
        or release.get("tagName") != tag
        or release.get("isDraft") is not False
        or release.get("isPrerelease") is not False
    ):
        raise PublishedReleaseVerificationError("GitHub release is missing, draft, prerelease, or bound to another tag")

    assets = release.get("assets")
    if not isinstance(assets, list):
        raise PublishedReleaseVerificationError("GitHub release asset inventory is missing")
    asset_names: list[str] = []
    for asset in assets:
        if not isinstance(asset, dict) or not isinstance(asset.get("name"), str):
            raise PublishedReleaseVerificationError("GitHub release asset inventory is malformed")
        asset_names.append(asset["name"])
    if len(asset_names) != len(set(asset_names)) or set(asset_names) != set(candidate_files):
        raise PublishedReleaseVerificationError("published asset names do not exactly match the verified candidate")

    commit_result = _run_checked(
        [gh, "api", f"repos/{repository}/commits/{tag}"],
        run=run,
        stage="published tag commit lookup",
    )
    commit_document = _decode_json(commit_result.stdout, "published tag commit")
    if not isinstance(commit_document, dict) or commit_document.get("sha") != source_commit:
        raise PublishedReleaseVerificationError("published tag does not resolve to the candidate source commit")

    with tempfile.TemporaryDirectory(prefix="threadforge-published-release-") as temporary:
        download_dir = Path(temporary) / "assets"
        download_dir.mkdir()
        _run_checked(
            [gh, "release", "download", tag, "--repo", repository, "--dir", str(download_dir)],
            run=run,
            stage="published release asset download",
        )
        published_files = _regular_files(download_dir)
        if set(published_files) != set(candidate_files):
            raise PublishedReleaseVerificationError("downloaded public asset set differs from the candidate")

        checked_assets = 0
        for name in sorted(candidate_files):
            if _sha256(candidate_files[name]) != _sha256(published_files[name]):
                raise PublishedReleaseVerificationError(f"published asset bytes differ from candidate: {name}")
            checked_assets += 1

    return {
        "schema": "threadforge.published-release-verification.v1",
        "repository": repository,
        "tag": tag,
        "source_commit": source_commit,
        "candidate_provenance_verified": True,
        "tag_commit_matches_candidate": True,
        "public_assets_match_candidate": True,
        "asset_count": checked_assets,
        "payload_count": checked_assets - 1,
        "license_review_required": True,
        "release_clearance": False,
        "release_authorized": False,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle-dir", type=Path, required=True, help="downloaded and locally verified candidate bundle")
    parser.add_argument("--version", required=True, help="stable MAJOR.MINOR.PATCH without leading v")
    parser.add_argument("--source-commit", required=True, help="full candidate source commit SHA-1")
    parser.add_argument("--repo", default=REPOSITORY, help="GitHub repository owner/name")
    parser.add_argument("--gh", default="gh", help="GitHub CLI executable")
    arguments = parser.parse_args(argv)
    try:
        result = verify_published_release(
            bundle_dir=arguments.bundle_dir,
            version=arguments.version,
            source_commit=arguments.source_commit,
            repository=arguments.repo,
            gh=arguments.gh,
        )
    except PublishedReleaseVerificationError as error:
        print(f"published-release verification failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
