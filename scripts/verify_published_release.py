#!/usr/bin/env python3
"""Verify uploaded GitHub release assets against a verified candidate.

The default mode performs a read-only post-publication audit. ``--require-draft``
checks uploaded assets before publication. Neither mode creates, edits, publishes,
or deletes a release, and neither result grants legal clearance or authorization.
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
MAX_FILE_BYTES = 512 * 1024 * 1024
MAX_TOTAL_BYTES = 10 * 1024 * 1024 * 1024
COMMAND_TIMEOUT_SECONDS = 3600
CHUNK_SIZE = 1024 * 1024
CANDIDATE_FAILURE_PREFIX = "release-candidate verification failed: "
SAFE_CANDIDATE_DIAGNOSTIC_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._,:()' -]{0,239}$")


class PublishedReleaseVerificationError(ValueError):
    """A release draft or published release does not match a verified candidate."""


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
        if stage == "release-candidate provenance verification":
            detail = _candidate_verification_detail(result.stderr)
            if detail is not None:
                raise PublishedReleaseVerificationError(f"{stage} failed: {detail}")
        raise PublishedReleaseVerificationError(f"{stage} command failed")
    return result


def _candidate_verification_detail(stderr: Any) -> str | None:
    """Expose only a bounded, path-free diagnostic from our candidate verifier."""
    if not isinstance(stderr, str):
        return None
    for line in stderr.splitlines():
        line = line.strip()
        if not line.startswith(CANDIDATE_FAILURE_PREFIX):
            continue
        detail = line[len(CANDIDATE_FAILURE_PREFIX) :].strip()
        if len(detail) <= 240 and SAFE_CANDIDATE_DIAGNOSTIC_RE.fullmatch(detail):
            return detail
    return None


def _verify_release_assets(
    *,
    bundle_dir: Path,
    version: str,
    source_commit: str,
    require_draft: bool,
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
    if "checksums.txt" not in candidate_files:
        raise PublishedReleaseVerificationError("verified candidate omits checksums.txt")

    tag = f"v{version}"
    view = _run_checked(
        [gh, "release", "view", tag, "--repo", repository, "--json", "assets,isDraft,isPrerelease,tagName"],
        run=run,
        stage="release metadata lookup",
    )
    release = _decode_json(view.stdout, "release metadata")
    if not isinstance(release, dict) or release.get("tagName") != tag:
        raise PublishedReleaseVerificationError("GitHub release is missing or bound to another tag")
    if release.get("isDraft") is not require_draft or release.get("isPrerelease") is not False:
        if require_draft:
            raise PublishedReleaseVerificationError("GitHub release is missing, not a draft, prerelease, or bound to another tag")
        raise PublishedReleaseVerificationError("GitHub release is missing, draft, prerelease, or bound to another tag")

    assets = release.get("assets")
    if not isinstance(assets, list):
        raise PublishedReleaseVerificationError("GitHub release asset inventory is missing")
    asset_names: list[str] = []
    for asset in assets:
        if not isinstance(asset, dict) or not isinstance(asset.get("name"), str):
            raise PublishedReleaseVerificationError("GitHub release asset inventory is malformed")
        asset_names.append(asset["name"])
    if len(asset_names) != len(set(asset_names)):
        raise PublishedReleaseVerificationError("release metadata contains duplicate asset names")
    metadata_asset_names = set(asset_names)

    commit_result = _run_checked(
        [gh, "api", f"repos/{repository}/commits/{tag}"],
        run=run,
        stage="release tag commit lookup",
    )
    commit_document = _decode_json(commit_result.stdout, "release tag commit")
    if not isinstance(commit_document, dict) or commit_document.get("sha") != source_commit:
        raise PublishedReleaseVerificationError("release tag does not resolve to the candidate source commit")

    with tempfile.TemporaryDirectory(prefix="threadforge-published-release-") as temporary:
        download_dir = Path(temporary) / "assets"
        download_dir.mkdir()
        _run_checked(
            [gh, "release", "download", tag, "--repo", repository, "--dir", str(download_dir)],
            run=run,
            stage="release asset download",
        )
        published_files = _regular_files(download_dir)
        candidate_names = set(candidate_files)
        published_names = set(published_files)
        metadata_missing = len(candidate_names - metadata_asset_names)
        metadata_extra = len(metadata_asset_names - candidate_names)
        downloaded_missing = len(candidate_names - published_names)
        downloaded_extra = len(published_names - candidate_names)
        shared_names = candidate_names & published_names
        byte_differences = sum(
            _sha256(candidate_files[name]) != _sha256(published_files[name])
            for name in shared_names
        )
        if metadata_missing or metadata_extra or downloaded_missing or downloaded_extra or byte_differences:
            raise PublishedReleaseVerificationError(
                "release asset mismatch: "
                f"metadata missing={metadata_missing}, metadata extra={metadata_extra}, "
                f"download missing={downloaded_missing}, download extra={downloaded_extra}, "
                f"byte differences={byte_differences}"
            )
        checked_assets = len(candidate_files)

    if require_draft:
        return {
            "schema": "threadforge.draft-release-verification.v1",
            "repository": repository,
            "tag": tag,
            "source_commit": source_commit,
            "candidate_provenance_verified": True,
            "tag_commit_matches_candidate": True,
            "draft_release": True,
            "draft_assets_match_candidate": True,
            "prepublication_check_only": True,
            "asset_count": checked_assets,
            "payload_count": checked_assets - 1,
            "license_review_required": True,
            "release_clearance": False,
            "release_authorized": False,
            "publish_authorized": False,
        }

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


def verify_published_release(
    *,
    bundle_dir: Path,
    version: str,
    source_commit: str,
    repository: str = REPOSITORY,
    gh: str = "gh",
    run: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> dict[str, Any]:
    return _verify_release_assets(
        bundle_dir=bundle_dir,
        version=version,
        source_commit=source_commit,
        require_draft=False,
        repository=repository,
        gh=gh,
        run=run,
    )


def verify_draft_release(
    *,
    bundle_dir: Path,
    version: str,
    source_commit: str,
    repository: str = REPOSITORY,
    gh: str = "gh",
    run: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> dict[str, Any]:
    """Verify uploaded draft assets without granting authority to publish them."""
    return _verify_release_assets(
        bundle_dir=bundle_dir,
        version=version,
        source_commit=source_commit,
        require_draft=True,
        repository=repository,
        gh=gh,
        run=run,
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle-dir", type=Path, required=True, help="downloaded and locally verified candidate bundle")
    parser.add_argument("--version", required=True, help="stable MAJOR.MINOR.PATCH without leading v")
    parser.add_argument("--source-commit", required=True, help="full candidate source commit SHA-1")
    parser.add_argument("--repo", default=REPOSITORY, help="GitHub repository owner/name")
    parser.add_argument("--gh", default="gh", help="GitHub CLI executable")
    parser.add_argument(
        "--require-draft",
        action="store_true",
        help="verify an uploaded draft release before publication; success never authorizes publication",
    )
    arguments = parser.parse_args(argv)
    verify = verify_draft_release if arguments.require_draft else verify_published_release
    try:
        result = verify(
            bundle_dir=arguments.bundle_dir,
            version=arguments.version,
            source_commit=arguments.source_commit,
            repository=arguments.repo,
            gh=arguments.gh,
        )
    except PublishedReleaseVerificationError as error:
        stage = "draft-release" if arguments.require_draft else "published-release"
        print(f"{stage} verification failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
